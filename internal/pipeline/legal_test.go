package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"bidos/internal/demo"
	"bidos/internal/store"
)

// TestLegalPackGuardrails switches the whole demo to the legal-services pack with no code
// change: traps refuse, the conflicts gate blocks export until a human clears it, and a
// do_not_use client name in an answer is a QA blocker.
func TestLegalPackGuardrails(t *testing.T) {
	ctx := context.Background()
	a, runner, eng := testApp(t)
	org, err := (&demo.Seeder{App: a}).Seed(ctx, "legal-services", "org-legal", "")
	if err != nil {
		t.Fatal(err)
	}
	bidID := org.ID + "-bid-rfp"
	runID, err := eng.Start(bidID, "demo", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner.Drain(ctx, 4*time.Minute)
	run, _ := a.DB.GetRun(runID)
	if run.Status != store.JobWaiting || run.Gate != Gate4 {
		t.Fatalf("expected gate 4, got %s at %s (%s)", run.Status, run.CurrentStage, run.Error)
	}
	reqs, _ := a.DB.ListRequirements(bidID)
	answers, _ := a.DB.LatestAnswersByBid(bidID)
	traps, refused := 0, 0
	var approved store.Answer
	for _, r := range reqs {
		ans := answers[r.ID]
		if r.IsTrap {
			traps++
			if ans.Status == store.StatusNeedsEvidence {
				refused++
			} else {
				t.Errorf("trap %s not refused: %s (%s)", r.Code, ans.Status, ans.ConfidenceLabel)
			}
		} else if ans.Status == store.StatusApproved && approved.ID == "" {
			approved = ans
		}
		if strings.Contains(ans.DraftText, "Vantage") {
			t.Errorf("%s cites the do_not_use client", r.Code)
		}
	}
	if traps < 10 || refused != traps {
		t.Fatalf("traps %d refused %d", traps, refused)
	}
	reviewer, _ := a.DB.UserByEmail(org.ID, "marcus.whitfield@haldencrewe.example")
	// a human edit that names the restricted client must be caught by QA
	if _, err := a.EditAnswer(org.ID, reviewer.ID, approved.ID, approved.DraftText+" We also acted for Vantage Bank on a similar refinancing."); err != nil {
		t.Fatal(err)
	}
	sum, err := a.RunQA(org.ID, bidID, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range sum.Findings {
		if f.Status == "open" && f.Severity == "blocker" {
			found[f.CheckType] = true
		}
	}
	if !found["client_disclosure"] {
		t.Error("expected a client_disclosure blocker for the do_not_use client name")
	}
	if !found["conflicts_gate"] {
		t.Error("expected the conflicts gate to block until a human records clearance")
	}
	if sum.ExportAllowed {
		t.Error("export must be blocked while blockers exist")
	}
	_ = a.DB.SetSetting(org.ID, "conflicts.cleared."+bidID, "true")
	sum, _ = a.RunQA(org.ID, bidID, reviewer.ID)
	for _, f := range sum.Findings {
		if f.Status == "open" && f.CheckType == "conflicts_gate" {
			t.Error("conflicts gate should clear once recorded")
		}
	}
}
