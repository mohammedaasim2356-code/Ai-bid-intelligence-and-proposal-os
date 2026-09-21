package app

import (
	"fmt"
	"strings"
	"time"

	"bidos/internal/store"
)

// DeadlineWatch (hourly): overdue tasks → escalate to proposal managers; bids at T-72h /
// T-24h → readiness summary to the owner; clarification questions past due.
// Notifications are keyed in settings so each fires once.
func (a *App) DeadlineWatch(orgID string) int {
	now := a.Now()
	n := 0
	overdue, _ := a.DB.OverdueTasks(orgID, now)
	users, _ := a.DB.UserMap(orgID)
	for _, t := range overdue {
		key := "notified.task." + t.ID + "." + now[:10]
		if a.Setting(orgID, key, "") != "" {
			continue
		}
		_ = a.DB.SetSetting(orgID, key, now)
		a.NotifyRole(orgID, store.RoleProposalManager, "Overdue SME task", fmt.Sprintf("%s (assigned to %s) was due %s.", truncateRunes(t.Description, 80), users[t.AssigneeID].Name, t.DueAt[:10]), "/tasks/"+t.ID)
		n++
	}
	bids, _ := a.DB.ListBids(orgID)
	for _, b := range bids {
		if b.Status == store.BidClosedLost || b.Status == store.BidClosedWon || b.Status == store.BidArchived {
			continue
		}
		dl, ok := store.ParseTime(b.Deadline)
		if !ok {
			continue
		}
		hours := time.Until(dl).Hours()
		for _, th := range []float64{72, 24} {
			if hours > th || hours < 0 {
				continue
			}
			key := fmt.Sprintf("notified.deadline.%s.%.0f", b.ID, th)
			if a.Setting(orgID, key, "") != "" {
				continue
			}
			_ = a.DB.SetSetting(orgID, key, now)
			s, _ := a.BidSummary(b)
			body := fmt.Sprintf("%d of %d requirements approved; %d mandatory unanswered/unresolved; %d QA blockers; %d open tasks.", s.Approved, s.Total, s.Mandatory-s.MandatoryDone, s.Blockers, s.OpenTasks)
			if b.OwnerID != "" {
				a.Notify(orgID, b.OwnerID, fmt.Sprintf("Deadline in %.0f hours: %s", th, b.Name), body, "/bids/"+b.ID)
			} else {
				a.NotifyRole(orgID, store.RoleProposalManager, fmt.Sprintf("Deadline in %.0f hours: %s", th, b.Name), body, "/bids/"+b.ID)
			}
			n++
		}
	}
	due, _ := a.DB.DueClarifications(orgID, now)
	for _, c := range due {
		key := "notified.clarification." + c.ID
		if a.Setting(orgID, key, "") != "" {
			continue
		}
		_ = a.DB.SetSetting(orgID, key, now)
		a.NotifyRole(orgID, store.RoleProposalManager, "Clarification deadline passed", truncateRunes(c.Question, 100), "/bids/"+c.BidID+"/addenda")
		n++
	}
	return n
}

// DailyDigest (per user, in-app; external adapter optional): my tasks, what changed, what blocks.
func (a *App) DailyDigest(orgID string) int {
	today := a.Now()[:10]
	if a.Setting(orgID, "digest.sent."+today, "") != "" {
		return 0
	}
	_ = a.DB.SetSetting(orgID, "digest.sent."+today, a.Now())
	users, _ := a.DB.ListUsers(orgID)
	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	changes, _ := a.DB.ActivitySince(orgID, since)
	blockers := 0
	bids, _ := a.DB.ListBids(orgID)
	for _, b := range bids {
		s, _ := a.BidSummary(b)
		blockers += s.Blockers
	}
	n := 0
	for _, u := range users {
		tasks, _ := a.DB.TasksForUser(u.ID)
		open := 0
		for _, t := range tasks {
			if t.Status == "open" || t.Status == "in_progress" {
				open++
			}
		}
		body := fmt.Sprintf("You have %d open task(s). %d change(s) in the last 24h. %d QA blocker(s) across active bids.", open, len(changes), blockers)
		a.Notify(orgID, u.ID, "Daily digest "+today, body, "/tasks")
		n++
	}
	if a.Hooks.Notify != nil {
		a.Hooks.Notify(orgID, "BidOS daily digest "+today, fmt.Sprintf("%d changes in 24h, %d QA blockers.", len(changes), blockers), "/")
	}
	return n
}

// LibraryHygieneJob (weekly): flags expired entries, expired documents and duplicate library questions.
func (a *App) LibraryHygieneJob(orgID string) string {
	rep := a.LibraryHygieneReport(orgID)
	expiredDocs := a.ExpiredDocuments(orgID)
	msg := fmt.Sprintf("%d library entries past review-by, %d near-duplicate pairs, %d approved documents expired.", len(rep.Expired), len(rep.Duplicates), len(expiredDocs))
	week := time.Now().UTC().Format("2006-01") + "-w" + fmt.Sprint(weekOf(time.Now()))
	if a.Setting(orgID, "hygiene.sent."+week, "") == "" && (len(rep.Expired) > 0 || len(rep.Duplicates) > 0 || len(expiredDocs) > 0) {
		_ = a.DB.SetSetting(orgID, "hygiene.sent."+week, a.Now())
		a.NotifyRole(orgID, store.RoleProposalManager, "Library hygiene", msg, "/knowledge/library")
	}
	for _, d := range expiredDocs {
		answers, _ := a.DB.AnswersCitingDocument(d.ID)
		_ = answers // surfaced through QA stale_evidence; no status change here
	}
	return msg
}

func weekOf(t time.Time) int {
	_, w := t.ISOWeek()
	return w
}

// SandboxCleanup removes expired public-demo sandbox organizations.
func (a *App) SandboxCleanup() int {
	orgs, _ := a.DB.ExpiredOrgs(a.Now())
	n := 0
	for _, o := range orgs {
		if err := a.DB.DeleteOrg(o.ID); err == nil {
			n++
			a.Log.Info("sandbox org removed", "org", o.ID)
		}
	}
	return n
}

// IntegrationStatus describes an adapter's state for the Integrations page.
type IntegrationStatus struct {
	Name, State, Detail, Fallback string
}

// Integrations reports adapter states honestly (never "connected" unless configured).
func (a *App) Integrations() []IntegrationStatus {
	st := func(name, configured, detail, fallback string) IntegrationStatus {
		return IntegrationStatus{Name: name, State: configured, Detail: detail, Fallback: fallback}
	}
	slack := "Not configured"
	if a.Cfg.SlackWebhookURL != "" {
		slack = "Connected"
	}
	clickup := "Not configured"
	if a.Cfg.ClickUpToken != "" && a.Cfg.ClickUpListID != "" {
		clickup = "Available"
	}
	inbox := "Not configured"
	if a.Cfg.EnableInbox {
		inbox = "Connected"
	}
	live := "Not configured"
	if a.AI != nil && a.AI.LiveAvailable() {
		live = "Available"
	}
	return []IntegrationStatus{
		st("AI providers (local / free tier)", live, "Configured through ai.providers.json or AI_BASE_URL/AI_MODEL/AI_API_KEY.", "DemoAiProvider (deterministic)"),
		st("Slack / webhook notifications", slack, "SLACK_WEBHOOK_URL receives digests and escalations.", "In-app notifications"),
		st("ClickUp task sync", clickup, "CLICKUP_TOKEN + CLICKUP_LIST_ID mirror SME tasks as ClickUp tasks (one-way).", "Internal task inbox"),
		st("Email / folder intake", inbox, "ENABLE_EMAIL_INTAKE watches "+a.Cfg.InboxDir+" for dropped RFP files and creates draft bids awaiting confirmation.", "Manual upload"),
		st("Google Drive / SharePoint", "Not configured", "Connectors are future adapters; the local knowledge repository is the source today.", "Local knowledge library"),
		st("HubSpot / Salesforce", "Not configured", "CRM sync is a future adapter.", "Manual bid entry"),
		st("Trigger.dev", "Not configured", "Optional job-runner adapter; DbJobRunner is active.", "DbJobRunner (lease-based, DB-backed)"),
	}
}

// Digest text helper for external adapters.
func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

var _ = joinNonEmpty
