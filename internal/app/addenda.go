package app

import (
	"context"
	"encoding/json"
	"strings"

	"bidos/internal/extract"
	"bidos/internal/store"
)

// AddendumDiff summarises what an addendum changed.
type AddendumDiff struct {
	Changed  []string `json:"changed"`
	New      []string `json:"new"`
	Removed  []string `json:"removed"`
	Affected []string `json:"affectedRequirementIds"`
	RunID    string   `json:"runId,omitempty"`
}

// UploadAddendum ingests an addendum, diffs it against current requirements, marks
// affected answers Needs Re-review and re-runs AUTO_DRAFT only for those items.
func (a *App) UploadAddendum(ctx context.Context, orgID, bidID, userID, name string, data []byte) (store.Addendum, AddendumDiff, error) {
	bid, err := a.DB.GetBid(orgID, bidID)
	if err != nil {
		return store.Addendum{}, AddendumDiff{}, err
	}
	doc, parsed, err := a.IngestDocument(ctx, IngestInput{OrgID: orgID, BidID: bidID, UserID: userID, Name: name, Data: data, Category: "addendum", ApprovalState: "n/a", Trust: "untrusted_external", SourceType: "addendum"})
	if err != nil {
		return store.Addendum{}, AddendumDiff{}, err
	}
	reqs, _ := a.DB.ListRequirements(bidID)
	byCode := map[string]store.Requirement{}
	maxSort := 0
	for _, r := range reqs {
		byCode[r.Code] = r
		if r.SortOrder > maxSort {
			maxSort = r.SortOrder
		}
	}
	pack := a.Pack(orgID)
	now := a.Now()
	var diff AddendumDiff
	changes := extract.AddendumChanges(parsed)
	handled := map[string]bool{}
	for _, ch := range changes {
		handled[ch.Code] = true
		switch ch.Action {
		case "removed":
			if r, ok := byCode[ch.Code]; ok {
				r.ChangeState, r.Status, r.UpdatedAt = "removed", store.StatusRemoved, now
				_ = a.DB.UpdateRequirement(r)
				if ans, err := a.DB.LatestAnswer(r.ID); err == nil {
					ans.Status, ans.UpdatedAt = store.StatusRemoved, now
					_ = a.DB.UpdateAnswer(ans)
				}
				diff.Removed = append(diff.Removed, ch.Code)
			}
		case "changed":
			if r, ok := byCode[ch.Code]; ok {
				if strings.TrimSpace(r.Text) == strings.TrimSpace(ch.Text) {
					continue
				}
				r.Text, r.SourceSpan, r.SourceDocumentID, r.SourceLocator = ch.Text, ch.Span, doc.ID, "addendum "+doc.Name
				r.Category, r.ChangeState, r.AnchorStatus, r.UpdatedAt = pack.Classify(ch.Text), "changed", "anchored", now
				if r.Status != store.StatusNotStarted {
					r.Status = store.StatusNeedsReReview
				}
				_ = a.DB.UpdateRequirement(r)
				if ans, err := a.DB.LatestAnswer(r.ID); err == nil && ans.Status != store.StatusNotStarted {
					ans.Status, ans.UpdatedAt = store.StatusNeedsReReview, now
					_ = a.DB.UpdateAnswer(ans)
				}
				diff.Changed = append(diff.Changed, ch.Code)
				diff.Affected = append(diff.Affected, r.ID)
			} else { // amendment to an unknown code: treat as new
				diff.New = append(diff.New, ch.Code)
				maxSort++
				r := a.newAddendumRequirement(bidID, doc.ID, doc.Name, ch.Code, ch.Text, ch.Span, pack.Classify(ch.Text), maxSort, now)
				diff.Affected = append(diff.Affected, r.ID)
			}
		case "new":
			if _, exists := byCode[ch.Code]; exists {
				continue
			}
			maxSort++
			r := a.newAddendumRequirement(bidID, doc.ID, doc.Name, ch.Code, ch.Text, ch.Span, pack.Classify(ch.Text), maxSort, now)
			diff.New = append(diff.New, ch.Code)
			diff.Affected = append(diff.Affected, r.ID)
		}
	}
	// Any other requirement-like lines with codes not covered by explicit instructions.
	rep := extract.Rules(parsed)
	for _, c := range rep.Items {
		if handled[c.Code] || strings.HasPrefix(c.Code, "R-") || !c.Anchored {
			continue
		}
		if r, ok := byCode[c.Code]; ok {
			if strings.TrimSpace(r.Text) != strings.TrimSpace(c.Text) {
				r.Text, r.SourceSpan, r.SourceDocumentID, r.SourceLocator, r.ChangeState, r.UpdatedAt = c.Text, c.SourceSpan, doc.ID, "addendum "+doc.Name, "changed", now
				if r.Status != store.StatusNotStarted {
					r.Status = store.StatusNeedsReReview
				}
				_ = a.DB.UpdateRequirement(r)
				diff.Changed = append(diff.Changed, c.Code)
				diff.Affected = append(diff.Affected, r.ID)
			}
			continue
		}
		maxSort++
		r := a.newAddendumRequirement(bidID, doc.ID, doc.Name, c.Code, c.Text, c.SourceSpan, pack.Classify(c.Text), maxSort, now)
		diff.New = append(diff.New, c.Code)
		diff.Affected = append(diff.Affected, r.ID)
	}
	if len(rep.Injections) > 0 {
		a.Activity(orgID, userID, "extraction.injection_blocked", "document", doc.ID, map[string]any{"lines": rep.Injections})
	}
	// notify owners
	for _, id := range diff.Affected {
		if r, err := a.DB.GetRequirement(id); err == nil && r.OwnerID != "" {
			a.Notify(orgID, r.OwnerID, "Addendum affects "+r.Code, "Requirement "+r.Code+" is "+r.ChangeState+"; its answer will be re-drafted.", "/bids/"+bidID+"/requirements/"+r.ID)
		}
	}
	if bid.OwnerID != "" {
		a.Notify(orgID, bid.OwnerID, "Addendum processed: "+doc.Name, summarizeDiff(diff), "/bids/"+bidID+"/addenda")
	}
	if a.Hooks.StartPipeline != nil && len(diff.Affected) > 0 {
		if runID, err := a.Hooks.StartPipeline(bidID, "addendum", a.Setting(orgID, "pipeline.auto_gates", "false") == "true", diff.Affected); err == nil {
			diff.RunID = runID
		}
	}
	b, _ := json.Marshal(diff)
	add := store.Addendum{ID: store.NewID(), BidID: bidID, DocumentID: doc.ID, ReceivedAt: now, DiffSummary: string(b)}
	if err := a.DB.CreateAddendum(add); err != nil {
		return add, diff, err
	}
	a.Activity(orgID, userID, "addendum.processed", "bid", bidID, map[string]any{"changed": diff.Changed, "new": diff.New, "removed": diff.Removed})
	return add, diff, nil
}

func (a *App) newAddendumRequirement(bidID, docID, docName, code, text, span, category string, sort int, now string) store.Requirement {
	r := store.Requirement{ID: store.NewID(), BidID: bidID, Code: code, Section: "Addendum", Text: text, Category: category, Mandatory: extractMandatory(text), SourceDocumentID: docID, SourceLocator: "addendum " + docName, SourceSpan: span, AnchorStatus: "anchored", ChangeState: "new", Status: store.StatusNotStarted, SortOrder: sort, CreatedAt: now, UpdatedAt: now}
	_ = a.DB.CreateRequirement(r)
	return r
}

func extractMandatory(text string) bool {
	l := strings.ToLower(text)
	return strings.Contains(l, " must ") || strings.Contains(l, " shall ") || strings.Contains(l, "required") || strings.HasPrefix(l, "confirm") || strings.HasPrefix(l, "describe") || strings.HasPrefix(l, "provide")
}

func summarizeDiff(d AddendumDiff) string {
	return "Changed: " + strings.Join(d.Changed, ", ") + " | New: " + strings.Join(d.New, ", ") + " | Removed: " + strings.Join(d.Removed, ", ")
}

// AddendumView joins an addendum with its document and diff.
type AddendumView struct {
	store.Addendum
	Document store.Document
	Diff     AddendumDiff
}

func (a *App) AddendaViews(bidID string) []AddendumView {
	list, _ := a.DB.ListAddenda(bidID)
	out := make([]AddendumView, 0, len(list))
	for _, ad := range list {
		v := AddendumView{Addendum: ad}
		v.Document, _ = a.DB.GetDocumentByID(ad.DocumentID)
		_ = json.Unmarshal([]byte(ad.DiffSummary), &v.Diff)
		out = append(out, v)
	}
	return out
}

// Clarification questions -----------------------------------------------------------

func (a *App) CreateClarification(orgID, userID, bidID, reqID, question, dueAt string) (store.Clarification, error) {
	if _, err := a.DB.GetBid(orgID, bidID); err != nil {
		return store.Clarification{}, err
	}
	if strings.TrimSpace(question) == "" {
		return store.Clarification{}, userErr("Write the question to send to the buyer.")
	}
	if dueAt != "" {
		if _, ok := store.ParseTime(dueAt); !ok {
			return store.Clarification{}, userErr("Due date %q is not valid.", dueAt)
		}
	}
	c := store.Clarification{ID: store.NewID(), BidID: bidID, RequirementID: reqID, Question: strings.TrimSpace(question), SentAt: a.Now(), DueAt: dueAt, Status: "open", CreatedAt: a.Now()}
	if err := a.DB.CreateClarification(c); err != nil {
		return c, err
	}
	a.Activity(orgID, userID, "clarification.sent", "clarification", c.ID, nil)
	return c, nil
}

func (a *App) AnswerClarification(orgID, userID, id, response string) error {
	c, err := a.DB.GetClarification(id)
	if err != nil {
		return err
	}
	if _, err := a.DB.GetBid(orgID, c.BidID); err != nil {
		return store.ErrNotFound
	}
	c.BuyerResponse, c.Status = strings.TrimSpace(response), "answered"
	if c.BuyerResponse == "" {
		c.Status = "open"
	}
	if err := a.DB.UpdateClarification(c); err != nil {
		return err
	}
	a.Activity(orgID, userID, "clarification.answered", "clarification", c.ID, nil)
	return nil
}
