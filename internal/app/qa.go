package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"bidos/internal/store"
)

// QA check types (deterministic). Each finding links to the exact entity.
var qaCheckTypes = []string{
	"missing_mandatory_answer", "needs_evidence", "unsupported_sentences", "missing_citation", "weak_evidence",
	"stale_evidence", "expired_library", "contradiction", "missing_attachment", "length_constraint",
	"placeholder_text", "unresolved_sme_task", "missing_section", "inconsistent_names", "unanchored_requirement", "needs_re_review",
}

// QASummary is the launch-checklist header.
type QASummary struct {
	Total, Passed, Blockers, Warnings, Info int
	Findings                                []store.QAResult
	ByType                                  map[string]int
	ExportAllowed                           bool
	RanAt                                   string
	CheckTypes                              []string
}

func fingerprint(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:8])
}

// RunQA executes every check for a bid and stores the findings (preserving dismissals).
func (a *App) RunQA(orgID, bidID, userID string) (QASummary, error) {
	bid, err := a.DB.GetBid(orgID, bidID)
	if err != nil {
		return QASummary{}, err
	}
	pack := a.Pack(orgID)
	reqs, _ := a.DB.ListRequirements(bidID)
	answers, _ := a.DB.LatestAnswersByBid(bidID)
	tasks, _ := a.DB.ListTasks(bidID)
	docsOfBid, _ := a.DB.ListBidDocuments(bidID)
	now := a.Now()
	today := now[:10]
	var findings []store.QAResult
	add := func(check, severity, msg, entityType, entityID string) {
		findings = append(findings, store.QAResult{BidID: bidID, CheckType: check, Severity: severity, Message: msg, EntityType: entityType, EntityID: entityID, Status: "open", Fingerprint: fingerprint(check, entityType, entityID, msg), CreatedAt: now})
	}
	reqLink := func(r store.Requirement) (string, string) { return "requirement", r.ID }
	var attachmentsNeeded []string
	var pageLimit int
	var numberFacts []numberFact
	for _, r := range reqs {
		if r.ChangeState == "removed" {
			continue
		}
		et, eid := reqLink(r)
		ans, hasAns := answers[r.ID]
		instr := pack.IsInstruction(r.Category)
		if r.AnchorStatus == "unanchored" && !r.Confirmed {
			add("unanchored_requirement", "warning", fmt.Sprintf("%s is not anchored to a verbatim source span; confirm or delete it.", r.Code), et, eid)
		}
		if r.Mandatory && (!hasAns || r.Status == store.StatusNotStarted) {
			add("missing_mandatory_answer", "blocker", fmt.Sprintf("Mandatory requirement %s has no answer.", r.Code), et, eid)
			continue
		}
		if r.Mandatory && hasAns && ans.Status != store.StatusApproved && r.Status != store.StatusNeedsEvidence && r.Status != store.StatusNeedsSME {
			add("missing_mandatory_answer", "warning", fmt.Sprintf("Mandatory requirement %s is %s, not approved.", r.Code, strings.ReplaceAll(r.Status, "_", " ")), et, eid)
		}
		if r.Status == store.StatusNeedsEvidence || r.Status == store.StatusNeedsSME {
			sev := "warning"
			if r.Mandatory {
				sev = "blocker"
			}
			add("needs_evidence", sev, fmt.Sprintf("%s has no approved evidence (%s).", r.Code, strings.ReplaceAll(r.Status, "_", " ")), et, eid)
		}
		if r.Status == store.StatusNeedsReReview {
			add("needs_re_review", "warning", fmt.Sprintf("%s changed (addendum or knowledge update) and its answer needs re-review.", r.Code), et, eid)
		}
		if instr {
			lower := strings.ToLower(r.Text)
			for _, m := range reAttachment.FindAllString(r.Text, -1) {
				attachmentsNeeded = append(attachmentsNeeded, m)
			}
			if m := rePageLimit.FindStringSubmatch(lower); m != nil {
				fmt.Sscanf(m[1], "%d", &pageLimit)
			}
			continue
		}
		if !hasAns {
			continue
		}
		text := ans.FinalText
		if text == "" {
			text = ans.DraftText
		}
		if ans.UnsupportedCount > 0 {
			add("unsupported_sentences", "blocker", fmt.Sprintf("%s: %d sentence(s) are not supported by cited evidence.", r.Code, ans.UnsupportedCount), et, eid)
		}
		cites, _ := a.DB.CitationsForAnswer(ans.ID)
		if len(cites) == 0 && ans.Status != store.StatusNeedsEvidence && ans.Status != store.StatusNeedsSME && ans.ConfidenceLabel != LabelNotApplicable {
			add("missing_citation", "warning", fmt.Sprintf("%s has an answer without stored citations.", r.Code), et, eid)
		}
		if ans.ConfidenceLabel == store.LabelWeak {
			add("weak_evidence", "warning", fmt.Sprintf("%s is labelled Weak evidence.", r.Code), et, eid)
		}
		for _, c := range cites {
			if c.DocApproval != "approved" {
				add("stale_evidence", "warning", fmt.Sprintf("%s cites %s which is %s.", r.Code, c.DocName, c.DocApproval), et, eid)
				break
			}
			if c.DocExpiresAt != "" && c.DocExpiresAt < today {
				add("stale_evidence", "warning", fmt.Sprintf("%s cites %s which expired on %s.", r.Code, c.DocName, c.DocExpiresAt), et, eid)
				break
			}
		}
		if ans.LibraryEntryID != "" {
			if e, err := a.DB.GetLibraryEntry(orgID, ans.LibraryEntryID); err == nil && e.ReviewBy != "" && e.ReviewBy < today {
				add("expired_library", "warning", fmt.Sprintf("%s reuses a library answer past its review-by date (%s).", r.Code, e.ReviewBy), et, eid)
			}
		}
		if rePlaceholder.MatchString(text) {
			add("placeholder_text", "blocker", fmt.Sprintf("%s contains placeholder text.", r.Code), et, eid)
		}
		numberFacts = append(numberFacts, collectNumberFacts(r, text)...)
	}
	// contradictions: same metric keyword with different values across answers
	for _, msg := range contradictions(numberFacts) {
		add("contradiction", "warning", msg.text, "requirement", msg.reqID)
	}
	// attachments
	seen := map[string]bool{}
	for _, att := range attachmentsNeeded {
		key := strings.ToUpper(att)
		if seen[key] {
			continue
		}
		seen[key] = true
		found := false
		for _, d := range docsOfBid {
			if strings.Contains(strings.ToUpper(d.Name), strings.ToUpper(strings.ReplaceAll(att, " ", "-"))) || strings.Contains(strings.ToUpper(d.Name), key) || hasTag(d.Tags, "attachment:"+strings.TrimPrefix(key, "ATTACHMENT ")) {
				found = true
				break
			}
		}
		if !found {
			add("missing_attachment", "blocker", fmt.Sprintf("%s is required by the submission instructions but has not been uploaded to the bid.", att), "bid", bidID)
		}
	}
	// proposal sections, placeholders, length
	asm, _ := a.Assemble(orgID, bidID)
	for _, s := range asm.Sections {
		if s.Placeholder {
			add("placeholder_text", "blocker", fmt.Sprintf("Section %q contains placeholder text.", s.Section.Title), "section", s.Section.ID)
		}
		if s.Empty && s.Section.Key != "cover" && s.Section.Key != "appendices" {
			add("missing_section", "warning", fmt.Sprintf("Section %q is empty.", s.Section.Title), "section", s.Section.ID)
		}
	}
	if pageLimit > 0 {
		pages := asm.Words / 450
		if pages > pageLimit {
			add("length_constraint", "warning", fmt.Sprintf("Assembled proposal is about %d pages; the limit is %d pages.", pages, pageLimit), "bid", bidID)
		}
	}
	// unresolved SME tasks
	for _, t := range tasks {
		if t.Status == "open" || t.Status == "in_progress" || t.Status == "waiting" {
			add("unresolved_sme_task", "warning", fmt.Sprintf("Open SME task (%s): %s", t.Status, truncateRunes(t.Description, 90)), "task", t.ID)
		}
	}
	// inconsistent company / buyer names
	org, _ := a.DB.GetOrg(orgID)
	aliases := append([]string{org.Name}, pack.Demo.Company.NameAliases...)
	for _, r := range reqs {
		ans, ok := answers[r.ID]
		if !ok {
			continue
		}
		text := ans.FinalText
		if text == "" {
			text = ans.DraftText
		}
		for _, bad := range nameVariants(text, aliases) {
			add("inconsistent_names", "warning", fmt.Sprintf("%s spells the company name as %q (expected %q).", r.Code, bad, org.Name), "requirement", r.ID)
		}
		if bid.BuyerName != "" {
			for _, bad := range nameVariants(text, []string{bid.BuyerName}) {
				add("inconsistent_names", "warning", fmt.Sprintf("%s spells the buyer name as %q (expected %q).", r.Code, bad, bid.BuyerName), "requirement", r.ID)
			}
		}
	}
	// pack-specific checks
	if pack.Guardrails.ClientDisclosure {
		findings = append(findings, a.clientDisclosureChecks(orgID, bidID, reqs, answers, now)...)
	}
	if pack.Guardrails.ConflictsGate && a.Setting(orgID, "conflicts.cleared."+bidID, "") != "true" {
		findings = append(findings, store.QAResult{BidID: bidID, CheckType: "conflicts_gate", Severity: "blocker", Message: "A human conflicts check has not been recorded for this bid. The system never clears conflicts.", EntityType: "bid", EntityID: bidID, Status: "open", Fingerprint: fingerprint("conflicts_gate", bidID), CreatedAt: now})
	}
	if pack.Guardrails.NoLegalAdvice {
		for _, r := range reqs {
			if ans, ok := answers[r.ID]; ok {
				text := ans.FinalText
				if text == "" {
					text = ans.DraftText
				}
				if reLegalAdvice.MatchString(text) {
					findings = append(findings, store.QAResult{BidID: bidID, CheckType: "legal_advice", Severity: "warning", Message: fmt.Sprintf("%s reads like legal advice; keep responses to capability and experience.", r.Code), EntityType: "requirement", EntityID: r.ID, Status: "open", Fingerprint: fingerprint("legal_advice", r.ID), CreatedAt: now})
				}
			}
		}
	}
	if err := a.DB.ReplaceQAResults(bidID, findings); err != nil {
		return QASummary{}, err
	}
	_ = a.DB.SetSetting(orgID, "qa.ran_at."+bidID, now)
	a.Activity(orgID, userID, "qa.run", "bid", bidID, map[string]any{"findings": len(findings)})
	sum, err := a.QASummary(orgID, bidID)
	if err == nil && sum.Blockers == 0 && sum.Total > 0 {
		a.AdvanceBidStatus(bidID, store.BidFinalQA)
	}
	return sum, err
}

var (
	reAttachment  = regexp.MustCompile(`Attachment\s+[A-Z0-9]+`)
	rePageLimit   = regexp.MustCompile(`not exceed (\d+) pages`)
	reLegalAdvice = regexp.MustCompile(`(?i)\b(in our legal opinion|we advise that|our advice is|legally binding opinion)\b`)
	reMetric      = regexp.MustCompile(`(?i)\b(availability|uptime|retention|discount|liability|notice|rpo|rto)\b[^.%\d]{0,50}?(\d+(?:\.\d+)?)\s*(%|days|months|hours|minutes|years)`)
)

type numberFact struct {
	reqID, code, metric, value string
}

func collectNumberFacts(r store.Requirement, text string) []numberFact {
	var out []numberFact
	for _, m := range reMetric.FindAllStringSubmatch(text, -1) {
		out = append(out, numberFact{reqID: r.ID, code: r.Code, metric: strings.ToLower(m[1]) + "/" + strings.ToLower(m[3]), value: m[2]})
	}
	return out
}

type contradictionMsg struct{ reqID, text string }

func contradictions(facts []numberFact) []contradictionMsg {
	byMetric := map[string]map[string][]string{}
	for _, f := range facts {
		if byMetric[f.metric] == nil {
			byMetric[f.metric] = map[string][]string{}
		}
		byMetric[f.metric][f.value] = append(byMetric[f.metric][f.value], f.code)
	}
	var out []contradictionMsg
	metrics := make([]string, 0, len(byMetric))
	for m := range byMetric {
		metrics = append(metrics, m)
	}
	sort.Strings(metrics)
	for _, m := range metrics {
		vals := byMetric[m]
		if len(vals) < 2 {
			continue
		}
		var parts []string
		keys := make([]string, 0, len(vals))
		for v := range vals {
			keys = append(keys, v)
		}
		sort.Strings(keys)
		first := ""
		for _, v := range keys {
			parts = append(parts, fmt.Sprintf("%s in %s", v, strings.Join(vals[v], ", ")))
			if first == "" {
				first = vals[v][0]
			}
		}
		out = append(out, contradictionMsg{reqID: "", text: fmt.Sprintf("Conflicting values for %s: %s.", strings.ReplaceAll(m, "/", " "), strings.Join(parts, "; "))})
	}
	return out
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}

// nameVariants finds spellings that look like the company name but differ (e.g. "North Star").
func nameVariants(text string, aliases []string) []string {
	var out []string
	for _, alias := range aliases {
		words := strings.Fields(alias)
		if len(words) == 0 || len(words[0]) < 6 {
			continue
		}
		first := words[0]
		// split camel/compound: "Northstar" → "North star"/"North Star"/"NorthStar"
		for i := 3; i < len(first)-2; i++ {
			variant := first[:i] + " " + first[i:]
			re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(variant) + `\b`)
			if m := re.FindString(text); m != "" && !strings.EqualFold(m, first) {
				out = append(out, m)
			}
		}
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(first[:1]) + `(?i:` + regexp.QuoteMeta(first[1:]) + `)\b`)
		for _, m := range re.FindAllString(text, -1) {
			if m != first && strings.EqualFold(m, first) {
				out = append(out, m)
			}
		}
	}
	return out
}

func (a *App) clientDisclosureChecks(orgID, bidID string, reqs []store.Requirement, answers map[string]store.Answer, now string) []store.QAResult {
	docs, _ := a.DB.ListKnowledgeDocs(orgID)
	type restricted struct{ name, disclosure string }
	var names []restricted
	for _, d := range docs {
		if d.ClientDisclosure == "anonymised" || d.ClientDisclosure == "do_not_use" {
			if n, ok := d.Metadata["clientName"].(string); ok && n != "" {
				names = append(names, restricted{n, d.ClientDisclosure})
			}
		}
	}
	var out []store.QAResult
	for _, r := range reqs {
		ans, ok := answers[r.ID]
		if !ok {
			continue
		}
		text := ans.FinalText
		if text == "" {
			text = ans.DraftText
		}
		for _, n := range names {
			if strings.Contains(strings.ToLower(text), strings.ToLower(n.name)) {
				out = append(out, store.QAResult{BidID: bidID, CheckType: "client_disclosure", Severity: "blocker", Message: fmt.Sprintf("%s names client %q whose disclosure status is %s.", r.Code, n.name, n.disclosure), EntityType: "requirement", EntityID: r.ID, Status: "open", Fingerprint: fingerprint("client_disclosure", r.ID, n.name), CreatedAt: now})
			}
		}
	}
	return out
}

// QASummary computes the checklist header from stored findings.
func (a *App) QASummary(orgID, bidID string) (QASummary, error) {
	if _, err := a.DB.GetBid(orgID, bidID); err != nil {
		return QASummary{}, err
	}
	findings, err := a.DB.ListQA(bidID)
	if err != nil {
		return QASummary{}, err
	}
	pack := a.Pack(orgID)
	types := append([]string{}, qaCheckTypes...)
	if pack.Guardrails.ClientDisclosure {
		types = append(types, "client_disclosure")
	}
	if pack.Guardrails.ConflictsGate {
		types = append(types, "conflicts_gate")
	}
	if pack.Guardrails.NoLegalAdvice {
		types = append(types, "legal_advice")
	}
	s := QASummary{Total: len(types), Findings: findings, ByType: map[string]int{}, CheckTypes: types, RanAt: a.Setting(orgID, "qa.ran_at."+bidID, "")}
	failing := map[string]bool{}
	for _, f := range findings {
		if f.Status != "open" {
			continue
		}
		s.ByType[f.CheckType]++
		failing[f.CheckType] = true
		switch f.Severity {
		case "blocker":
			s.Blockers++
		case "warning":
			s.Warnings++
		default:
			s.Info++
		}
	}
	s.Passed = s.Total - len(failing)
	s.ExportAllowed = s.RanAt != "" && s.Blockers == 0
	return s, nil
}

// ResolveFinding marks a finding resolved or dismissed with a reason and recomputes readiness.
func (a *App) ResolveFinding(orgID, userID, findingID, status, reason string) error {
	f, err := a.DB.GetQA(findingID)
	if err != nil {
		return err
	}
	if _, err := a.DB.GetBid(orgID, f.BidID); err != nil {
		return store.ErrNotFound
	}
	if status != "resolved" && status != "dismissed" && status != "open" {
		return userErr("Status must be resolved, dismissed or open.")
	}
	if status == "dismissed" && strings.TrimSpace(reason) == "" {
		return userErr("Give a reason when dismissing a finding.")
	}
	if err := a.DB.SetQAStatus(f.ID, status, strings.TrimSpace(reason)); err != nil {
		return err
	}
	a.Activity(orgID, userID, "qa."+status, "qa", f.ID, map[string]any{"reason": reason, "check": f.CheckType})
	if sum, err := a.QASummary(orgID, f.BidID); err == nil && sum.Blockers == 0 {
		a.AdvanceBidStatus(f.BidID, store.BidFinalQA)
	}
	return nil
}

// FindingLink returns the deep link for a finding's entity.
func FindingLink(f store.QAResult) string {
	switch f.EntityType {
	case "requirement":
		return "/bids/" + f.BidID + "/requirements/" + f.EntityID
	case "task":
		return "/tasks/" + f.EntityID
	case "section":
		return "/bids/" + f.BidID + "/proposal#" + f.EntityID
	}
	return "/bids/" + f.BidID
}

var _ = time.Now
