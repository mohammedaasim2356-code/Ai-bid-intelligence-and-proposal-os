package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"bidos/internal/ai"
	"bidos/internal/docs"
	"bidos/internal/extract"
	"bidos/internal/store"
)

// ExtractOptions control persistence of extracted requirements.
type ExtractOptions struct {
	UserID    string
	IDPrefix  string // deterministic ids for seeds/evals
	Now       string
	TrapCodes map[string]bool // demo golden traps (marks is_trap)
}

// ExtractionResult summarises one extraction run.
type ExtractionResult struct {
	Added        int
	Updated      int
	Unanchored   int
	Suggested    int // rule-pass items the model dropped ("possible missed requirements")
	Injections   []string
	ProviderMode string
	ProviderID   string
	DocumentID   string
}

var reCodeClean = regexp.MustCompile(`[^A-Za-z0-9.\-]+`)

// ExtractRequirements runs the span-anchored hybrid extraction for one bid document:
// deterministic rule pass → optional live-model refinement → anchor check → persistence.
func (a *App) ExtractRequirements(ctx context.Context, orgID, bidID, docID string, opts ExtractOptions) (ExtractionResult, error) {
	res := ExtractionResult{DocumentID: docID}
	bid, err := a.DB.GetBid(orgID, bidID)
	if err != nil {
		return res, err
	}
	doc, err := a.DB.GetDocument(orgID, docID)
	if err != nil {
		return res, err
	}
	parsed, err := a.ParseStored(doc)
	if err != nil {
		return res, userErr("%s could not be parsed: %v", doc.Name, err)
	}
	pack := a.Pack(orgID)
	rep := extract.Rules(parsed)
	res.Injections = rep.Injections
	if len(rep.Injections) > 0 {
		a.Activity(orgID, opts.UserID, "extraction.injection_blocked", "document", docID, map[string]any{"lines": rep.Injections})
	}
	type item struct {
		extract.Candidate
		Category  string
		Suggested bool
	}
	var items []item
	res.ProviderMode, res.ProviderID = "demo", "rules"
	for _, c := range rep.Items {
		items = append(items, item{Candidate: c, Category: pack.Classify(c.Text)})
	}
	// Model pass (only refines; cannot invent). Runs when a live provider is configured.
	if a.AI != nil && a.AI.LiveAvailable() && len(rep.Items) > 0 {
		in := ai.ExtractInput{DocumentName: doc.Name, Text: truncateRunes(parsed.FullText(), 24000)}
		for _, c := range pack.Categories {
			in.Categories = append(in.Categories, ai.CategoryRef{ID: c.ID, Label: c.Label})
		}
		for _, c := range rep.Items {
			in.Candidates = append(in.Candidates, ai.ExtractCandidate{Code: c.Code, Text: c.Text, SourceSpan: c.SourceSpan, Section: c.Section})
		}
		out, r, err := a.AI.Extract(ctx, orgID, a.Sensitivity(orgID), in)
		if err == nil && r.Mode != "demo" {
			res.ProviderMode, res.ProviderID = r.Mode, r.ProviderID
			byCode := map[string]item{}
			for _, it := range items {
				byCode[it.Code] = it
			}
			var merged []item
			seen := map[string]bool{}
			for _, mi := range out.Items {
				if extract.IsInjection(mi.Text) || extract.IsInjection(mi.SourceSpan) {
					res.Injections = append(res.Injections, mi.SourceSpan)
					continue
				}
				anchored := extract.AnchorCheck(parsed, mi.SourceSpan)
				base, ok := byCode[mi.Code]
				it := item{Candidate: extract.Candidate{Code: mi.Code, Text: mi.Text, SourceSpan: mi.SourceSpan, Section: mi.Section, Mandatory: mi.Mandatory, Anchored: anchored, Kind: "clause"}}
				if ok {
					it.Locator, it.Sheet, it.Cell, it.Kind = base.Locator, base.Sheet, base.Cell, base.Kind
					if it.Section == "" {
						it.Section = base.Section
					}
					seen[mi.Code] = true
				}
				if mi.Category != "" {
					it.Category = mi.Category
				} else {
					it.Category = pack.Classify(mi.Text)
				}
				if !anchored {
					it.Reason = "model output span not found verbatim in document"
				}
				merged = append(merged, it)
			}
			for _, it := range items { // rule items the model dropped → suggestions
				if !seen[it.Code] {
					it.Suggested = true
					merged = append(merged, it)
				}
			}
			items = merged
		}
	}
	existing, _ := a.DB.ListRequirements(bidID)
	byCode := map[string]store.Requirement{}
	maxSort := 0
	for _, r := range existing {
		if r.SourceDocumentID == docID {
			byCode[r.Code] = r
		}
		if r.SortOrder > maxSort {
			maxSort = r.SortOrder
		}
	}
	now := opts.Now
	if now == "" {
		now = a.Now()
	}
	for i, it := range items {
		code := strings.TrimSpace(it.Code)
		if code == "" {
			code = fmt.Sprintf("R-%03d", i+1)
		}
		anchor := "anchored"
		if !it.Anchored {
			anchor = "unanchored"
			res.Unanchored++
		} else if it.Suggested {
			anchor = "suggested"
			res.Suggested++
		}
		if prev, ok := byCode[code]; ok {
			if prev.Confirmed {
				continue // human-confirmed items are never overwritten by re-extraction
			}
			prev.Text, prev.SourceSpan, prev.Section, prev.Category, prev.Mandatory = it.Text, it.SourceSpan, it.Section, it.Category, it.Mandatory
			prev.SourceLocator, prev.AnchorStatus, prev.UpdatedAt = it.Locator, anchor, now
			if err := a.DB.UpdateRequirement(prev); err != nil {
				return res, err
			}
			res.Updated++
			continue
		}
		id := store.NewID()
		if opts.IDPrefix != "" {
			id = opts.IDPrefix + reCodeClean.ReplaceAllString(code, "_")
		}
		maxSort++
		r := store.Requirement{ID: id, BidID: bidID, Code: code, Section: it.Section, Text: it.Text, Category: it.Category, Mandatory: it.Mandatory, SourceDocumentID: docID, SourceLocator: it.Locator, SourceSpan: it.SourceSpan, AnchorStatus: anchor, ChangeState: "unchanged", Status: store.StatusNotStarted, SortOrder: maxSort, IsTrap: opts.TrapCodes[code], CreatedAt: now, UpdatedAt: now}
		if it.Cell != "" {
			r.SourceLocator = it.Locator + "|answer=" + it.Cell
		}
		if err := a.DB.CreateRequirement(r); err != nil {
			return res, err
		}
		res.Added++
	}
	a.Activity(orgID, opts.UserID, "extraction.completed", "bid", bidID, map[string]any{"document": doc.Name, "added": res.Added, "updated": res.Updated, "unanchored": res.Unanchored, "mode": res.ProviderMode})
	a.AdvanceBidStatus(bid.ID, store.BidQualification)
	return res, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// RequirementInput edits a requirement from the review UI.
type RequirementInput struct {
	Text       string
	Category   string
	Mandatory  bool
	Section    string
	OwnerID    string
	ReviewerID string
	Confirmed  bool
}

func (a *App) UpdateRequirement(orgID, userID, reqID string, in RequirementInput) (store.Requirement, error) {
	r, bid, err := a.requirementInOrg(orgID, reqID)
	if err != nil {
		return r, err
	}
	_ = bid
	if strings.TrimSpace(in.Text) == "" {
		return r, userErr("Requirement text cannot be empty.")
	}
	if in.Text != r.Text && r.AnchorStatus != "manual" {
		r.AnchorStatus = "manual" // human edit: anchored by a person, tracked as such
	}
	r.Text, r.Category, r.Mandatory, r.Section, r.OwnerID, r.ReviewerID, r.Confirmed = strings.TrimSpace(in.Text), in.Category, in.Mandatory, in.Section, in.OwnerID, in.ReviewerID, in.Confirmed
	r.UpdatedAt = a.Now()
	if err := a.DB.UpdateRequirement(r); err != nil {
		return r, err
	}
	a.Activity(orgID, userID, "requirement.updated", "requirement", r.ID, nil)
	return r, nil
}

// AddManualRequirement creates a human-authored requirement (anchor status manual).
func (a *App) AddManualRequirement(orgID, userID, bidID, code, text, category string, mandatory bool) (store.Requirement, error) {
	if _, err := a.DB.GetBid(orgID, bidID); err != nil {
		return store.Requirement{}, err
	}
	if strings.TrimSpace(text) == "" {
		return store.Requirement{}, userErr("Requirement text cannot be empty.")
	}
	existing, _ := a.DB.ListRequirements(bidID)
	if code == "" {
		code = fmt.Sprintf("M-%03d", len(existing)+1)
	}
	if category == "" {
		category = a.Pack(orgID).Classify(text)
	}
	now := a.Now()
	r := store.Requirement{ID: store.NewID(), BidID: bidID, Code: code, Section: "Manual", Text: strings.TrimSpace(text), Category: category, Mandatory: mandatory, SourceLocator: "manual entry", SourceSpan: text, AnchorStatus: "manual", ChangeState: "new", Status: store.StatusNotStarted, SortOrder: len(existing) + 1, Confirmed: true, CreatedAt: now, UpdatedAt: now}
	if err := a.DB.CreateRequirement(r); err != nil {
		return r, err
	}
	a.Activity(orgID, userID, "requirement.added", "requirement", r.ID, nil)
	return r, nil
}

// ConfirmAllRequirements marks every anchored requirement confirmed (Gate 1).
func (a *App) ConfirmAllRequirements(orgID, userID, bidID string) (int, error) {
	if _, err := a.DB.GetBid(orgID, bidID); err != nil {
		return 0, err
	}
	reqs, err := a.DB.ListRequirements(bidID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range reqs {
		if r.Confirmed || r.AnchorStatus == "unanchored" {
			continue
		}
		r.Confirmed = true
		r.UpdatedAt = a.Now()
		if err := a.DB.UpdateRequirement(r); err != nil {
			return n, err
		}
		n++
	}
	a.Activity(orgID, userID, "requirements.confirmed", "bid", bidID, map[string]any{"count": n})
	return n, nil
}

// DeleteRequirement removes a requirement (e.g. an unanchored suggestion).
func (a *App) DeleteRequirement(orgID, userID, reqID string) error {
	r, _, err := a.requirementInOrg(orgID, reqID)
	if err != nil {
		return err
	}
	if err := a.DB.DeleteRequirement(r.ID); err != nil {
		return err
	}
	a.Activity(orgID, userID, "requirement.deleted", "requirement", r.ID, map[string]any{"code": r.Code})
	return nil
}

func (a *App) requirementInOrg(orgID, reqID string) (store.Requirement, store.Bid, error) {
	r, err := a.DB.GetRequirement(reqID)
	if err != nil {
		return r, store.Bid{}, err
	}
	bid, err := a.DB.GetBid(orgID, r.BidID)
	if err != nil {
		return r, bid, store.ErrNotFound
	}
	return r, bid, nil
}

// SourceLocatorParts splits "Sheet!C5|answer=D5" into (display locator, answer cell).
func SourceLocatorParts(loc string) (string, string) {
	if i := strings.Index(loc, "|answer="); i >= 0 {
		return loc[:i], loc[i+len("|answer="):]
	}
	return loc, ""
}

// RFPPreview returns the first sections of a bid document for the "sample RFP" view.
func (a *App) RFPPreview(doc store.Document, max int) []docs.Section {
	parsed, err := a.ParseStored(doc)
	if err != nil {
		return nil
	}
	if len(parsed.Sections) > max {
		return parsed.Sections[:max]
	}
	return parsed.Sections
}
