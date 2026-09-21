package app

import (
	"strings"

	"bidos/internal/store"
)

// BidInput is the create/edit form.
type BidInput struct {
	ID             string // optional (deterministic seed)
	Name           string
	BuyerName      string
	BuyerURL       string
	OwnerID        string
	EstimatedValue float64
	Deadline       string
	Status         string
	Description    string
	CreatedAt      string // optional
}

func (a *App) CreateBid(orgID, userID string, in BidInput) (store.Bid, error) {
	if strings.TrimSpace(in.Name) == "" {
		return store.Bid{}, userErr("Give the bid a name (the opportunity or RFP title).")
	}
	deadline, err := ParseDeadline(in.Deadline)
	if err != nil {
		return store.Bid{}, err
	}
	if in.Status == "" {
		in.Status = store.BidIntake
	}
	now := a.Now()
	if in.CreatedAt != "" {
		now = in.CreatedAt
	}
	id := in.ID
	if id == "" {
		id = store.NewID()
	}
	b := store.Bid{ID: id, OrgID: orgID, Name: strings.TrimSpace(in.Name), BuyerName: strings.TrimSpace(in.BuyerName), BuyerURL: strings.TrimSpace(in.BuyerURL), OwnerID: in.OwnerID, EstimatedValue: in.EstimatedValue, Deadline: deadline, Status: in.Status, Description: strings.TrimSpace(in.Description), CreatedAt: now, UpdatedAt: now}
	if err := a.DB.CreateBid(b); err != nil {
		return b, err
	}
	a.Activity(orgID, userID, "bid.created", "bid", b.ID, map[string]any{"name": b.Name})
	_, _ = a.EnsureSections(orgID, b.ID)
	return b, nil
}

func (a *App) UpdateBid(orgID, userID, id string, in BidInput) (store.Bid, error) {
	b, err := a.DB.GetBid(orgID, id)
	if err != nil {
		return b, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return b, userErr("The bid name cannot be empty.")
	}
	deadline, err := ParseDeadline(in.Deadline)
	if err != nil {
		return b, err
	}
	b.Name, b.BuyerName, b.BuyerURL, b.OwnerID = strings.TrimSpace(in.Name), strings.TrimSpace(in.BuyerName), strings.TrimSpace(in.BuyerURL), in.OwnerID
	b.EstimatedValue, b.Deadline, b.Description = in.EstimatedValue, deadline, strings.TrimSpace(in.Description)
	if in.Status != "" {
		b.Status = in.Status
	}
	b.UpdatedAt = a.Now()
	if err := a.DB.UpdateBid(b); err != nil {
		return b, err
	}
	a.Activity(orgID, userID, "bid.updated", "bid", b.ID, nil)
	return b, nil
}

// SetBidDecision records the human bid/no-bid decision (Gate 2).
func (a *App) SetBidDecision(orgID, userID, id, decision string) error {
	b, err := a.DB.GetBid(orgID, id)
	if err != nil {
		return err
	}
	if decision != "bid" && decision != "no_bid" {
		return userErr("Decision must be bid or no_bid.")
	}
	b.Decision = decision
	if decision == "no_bid" {
		b.Status = store.BidClosedLost
	} else if b.Status == store.BidIntake || b.Status == store.BidQualification {
		b.Status = store.BidDrafting
	}
	b.UpdatedAt = a.Now()
	if err := a.DB.UpdateBid(b); err != nil {
		return err
	}
	a.Activity(orgID, userID, "bid.decision", "bid", b.ID, map[string]any{"decision": decision})
	return nil
}

// BidSummary is the command-center header data.
type BidSummary struct {
	Bid           store.Bid
	Owner         store.User
	Total         int
	Answered      int
	Approved      int
	InReview      int
	NeedsEvidence int
	NeedsSME      int
	Drafted       int
	NotStarted    int
	Mandatory     int
	MandatoryDone int
	Unanchored    int
	Unconfirmed   int
	ProgressPct   int
	CoveragePct   int
	Blockers      int
	Warnings      int
	DaysLeft      int
	Run           *store.PipelineRun
	PrimaryAction string
	PrimaryLink   string
	Stage         string
	OpenTasks     int
	ByStatus      map[string]int
	ByCategory    map[string]int
	BySection     map[string]int
	Labels        map[string]int
}

func (a *App) BidSummary(bid store.Bid) (BidSummary, error) {
	s := BidSummary{Bid: bid, ByStatus: map[string]int{}, ByCategory: map[string]int{}, BySection: map[string]int{}, Labels: map[string]int{}}
	if bid.OwnerID != "" {
		s.Owner, _ = a.DB.GetUser(bid.OwnerID)
	}
	reqs, err := a.DB.ListRequirements(bid.ID)
	if err != nil {
		return s, err
	}
	answers, _ := a.DB.LatestAnswersByBid(bid.ID)
	pack := a.Pack(bid.OrgID)
	evidenceBearing := 0
	covered := 0
	for _, r := range reqs {
		if r.ChangeState == "removed" {
			continue
		}
		s.Total++
		s.ByStatus[r.Status]++
		s.ByCategory[pack.CategoryLabel(r.Category)]++
		s.BySection[r.Section]++
		if r.Mandatory {
			s.Mandatory++
			if r.Status == store.StatusApproved {
				s.MandatoryDone++
			}
		}
		if r.AnchorStatus != "anchored" && r.AnchorStatus != "manual" {
			s.Unanchored++
		}
		if !r.Confirmed {
			s.Unconfirmed++
		}
		switch r.Status {
		case store.StatusApproved:
			s.Approved++
		case store.StatusInReview:
			s.InReview++
		case store.StatusNeedsEvidence:
			s.NeedsEvidence++
		case store.StatusNeedsSME:
			s.NeedsSME++
		case store.StatusDrafted, store.StatusNeedsReReview:
			s.Drafted++
		case store.StatusNotStarted:
			s.NotStarted++
		}
		if r.Status != store.StatusNotStarted {
			s.Answered++
		}
		if !pack.IsInstruction(r.Category) {
			evidenceBearing++
			if ans, ok := answers[r.ID]; ok {
				s.Labels[ans.ConfidenceLabel]++
				if ans.ConfidenceLabel == store.LabelStrong || ans.ConfidenceLabel == store.LabelModerate {
					covered++
				}
			}
		}
	}
	if s.Total > 0 {
		s.ProgressPct = s.Approved * 100 / s.Total
	}
	if evidenceBearing > 0 {
		s.CoveragePct = covered * 100 / evidenceBearing
	}
	qa, _ := a.DB.ListQA(bid.ID)
	for _, q := range qa {
		if q.Status != "open" {
			continue
		}
		switch q.Severity {
		case "blocker":
			s.Blockers++
		case "warning":
			s.Warnings++
		}
	}
	tasks, _ := a.DB.ListTasks(bid.ID)
	for _, t := range tasks {
		if t.Status == "open" || t.Status == "in_progress" {
			s.OpenTasks++
		}
	}
	s.DaysLeft = DaysUntil(bid.Deadline)
	if run, err := a.DB.LatestRun(bid.ID); err == nil {
		s.Run = &run
		s.Stage = run.CurrentStage
	}
	s.PrimaryAction, s.PrimaryLink = a.primaryAction(s)
	return s, nil
}

func (a *App) primaryAction(s BidSummary) (string, string) {
	base := "/bids/" + s.Bid.ID
	switch {
	case s.Total == 0:
		return "Upload RFP & extract", base + "/requirements"
	case s.Run != nil && s.Run.Status == store.JobWaiting:
		return "Approve gate: " + s.Run.Gate, base + "/pipeline"
	case s.NotStarted > 0:
		return "Generate answers", base + "/requirements"
	case s.NeedsEvidence+s.NeedsSME > 0:
		return "Resolve evidence gaps", base + "/tasks"
	case s.Blockers > 0:
		return "Resolve blockers", base + "/qa"
	case s.InReview > 0:
		return "Review answers", base + "/answers"
	case s.Approved == s.Total && s.Blockers == 0:
		return "Export", base + "/export"
	}
	return "Run QA", base + "/qa"
}

// BidSummaries lists all bids with their summaries.
func (a *App) BidSummaries(orgID string) ([]BidSummary, error) {
	bids, err := a.DB.ListBids(orgID)
	if err != nil {
		return nil, err
	}
	out := make([]BidSummary, 0, len(bids))
	for _, b := range bids {
		s, err := a.BidSummary(b)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// AdvanceBidStatus moves the bid status forward based on workflow facts (never backwards past closed).
func (a *App) AdvanceBidStatus(bidID, status string) {
	b, err := a.DB.GetBidByID(bidID)
	if err != nil || b.Status == store.BidClosedLost || b.Status == store.BidClosedWon || b.Status == store.BidArchived {
		return
	}
	order := map[string]int{store.BidIntake: 0, store.BidQualification: 1, store.BidDrafting: 2, store.BidReview: 3, store.BidFinalQA: 4, store.BidReady: 5}
	if order[status] > order[b.Status] {
		_ = a.DB.SetBidStatus(bidID, status, a.Now())
	}
}
