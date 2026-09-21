// Package integrations holds optional adapters. Each one is off unless configured and
// falls back to internal behaviour (in-app notifications, internal tasks, manual upload).
package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bidos/internal/app"
	"bidos/internal/config"
	"bidos/internal/jobs"
	"bidos/internal/store"
)

// Wire attaches configured adapters to the app hooks and job runner.
func Wire(cfg config.Config, a *app.App, runner *jobs.Runner, log *slog.Logger) {
	if cfg.SlackWebhookURL != "" {
		a.Hooks.Notify = SlackNotifier(cfg.SlackWebhookURL, cfg.BaseURL, log)
	}
	if cfg.ErrorWebhookURL != "" {
		a.Hooks.ReportError = ErrorReporter(cfg.ErrorWebhookURL, log)
	}
	if cfg.ClickUpToken != "" && cfg.ClickUpListID != "" {
		a.Hooks.TaskCreated = ClickUpSync(cfg.ClickUpToken, cfg.ClickUpListID, log)
	}
	if cfg.EnableInbox {
		runner.Handle("inbox.scan", InboxHandler(a, cfg.InboxDir, log))
		runner.Every(time.Minute, "inbox.scan", "{}")
	}
}

func post(url string, payload any, headers map[string]string) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// SlackNotifier posts notifications to an incoming webhook (async, never blocks).
func SlackNotifier(webhook, baseURL string, log *slog.Logger) func(orgID, title, body, link string) {
	return func(orgID, title, body, link string) {
		go func() {
			text := "*" + title + "*\n" + body
			if link != "" {
				text += "\n" + strings.TrimRight(baseURL, "/") + link
			}
			if err := post(webhook, map[string]string{"text": text}, nil); err != nil {
				log.Warn("slack webhook failed", "err", err)
			}
		}()
	}
}

// ErrorReporter forwards errors to a generic webhook.
func ErrorReporter(url string, log *slog.Logger) func(err error, where string) {
	return func(err error, where string) {
		go func() {
			_ = post(url, map[string]string{"where": where, "error": err.Error(), "at": time.Now().UTC().Format(time.RFC3339)}, nil)
		}()
	}
}

// ClickUpSync mirrors new SME tasks into a ClickUp list (one-way).
func ClickUpSync(token, listID string, log *slog.Logger) func(t store.Task) {
	return func(t store.Task) {
		go func() {
			payload := map[string]any{"name": "[BidOS] " + t.Description, "description": t.Question}
			if err := post("https://api.clickup.com/api/v2/list/"+listID+"/task", payload, map[string]string{"Authorization": token}); err != nil {
				log.Warn("clickup sync failed", "err", err)
			}
		}()
	}
}

// InboxHandler scans a folder for dropped RFP files and creates draft bids awaiting
// confirmation (never starts drafting automatically).
func InboxHandler(a *app.App, dir string, log *slog.Logger) jobs.Handler {
	return func(ctx context.Context, job store.Job, report func(int, string)) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // folder absent: nothing to do
		}
		orgs, _ := a.DB.ListOrgs()
		if len(orgs) == 0 {
			return nil
		}
		org := orgs[0]
		processed := filepath.Join(dir, "processed")
		_ = os.MkdirAll(processed, 0o755)
		n := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext != ".pdf" && ext != ".docx" && ext != ".xlsx" && ext != ".csv" && ext != ".txt" && ext != ".md" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			owner := ""
			if users, _ := a.DB.UsersByRole(org.ID, store.RoleProposalManager); len(users) > 0 {
				owner = users[0].ID
			}
			bid, err := a.CreateBid(org.ID, "", app.BidInput{Name: "Inbox: " + e.Name(), OwnerID: owner, Description: "Created from the intake folder. Confirm the sender and details before starting the pipeline."})
			if err != nil {
				log.Warn("inbox bid failed", "err", err)
				continue
			}
			if _, _, err := a.IngestDocument(ctx, app.IngestInput{OrgID: org.ID, BidID: bid.ID, Name: e.Name(), Data: data, Category: "rfp", ApprovalState: "n/a", Trust: "untrusted_external", SourceType: "inbox"}); err != nil {
				log.Warn("inbox ingest failed", "file", e.Name(), "err", err)
			}
			_ = os.Rename(filepath.Join(dir, e.Name()), filepath.Join(processed, e.Name()))
			a.NotifyRole(org.ID, store.RoleProposalManager, "New RFP in intake folder", e.Name()+" created a draft bid awaiting confirmation.", "/bids/"+bid.ID)
			n++
		}
		report(100, "inbox scan: "+string(rune('0'+min(n, 9)))+" file(s)")
		return nil
	}
}
