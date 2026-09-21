package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bidos/internal/ai"
	"bidos/internal/app"
	"bidos/internal/config"
	"bidos/internal/demo"
	"bidos/internal/docs"
	"bidos/internal/jobs"
	"bidos/internal/retrieval"
	"bidos/internal/store"
)

func testApp(t *testing.T) (*app.App, *jobs.Runner, *Engine) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{StorageDir: dir, DatabasePath: filepath.Join(dir, "t.db"), MaxUploadBytes: 25 << 20, VerticalPack: "saas-it", EnableEmbeddings: true}
	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	router := ai.NewRouter(ai.LoadConfig(filepath.Join(dir, "none.json")), db, ai.OpenAICompatible{}, nil)
	a := app.New(cfg, db, router, retrieval.HashEmbedder{}, nil)
	runner := jobs.New(db, nil, 3)
	eng := New(a, runner)
	return a, runner, eng
}

func TestFullPipelineDemo(t *testing.T) {
	ctx := context.Background()
	a, runner, eng := testApp(t)
	seeder := &demo.Seeder{App: a}
	org, err := seeder.Seed(ctx, "saas-it", "", "")
	if err != nil {
		t.Fatal(err)
	}
	bidID := org.ID + "-bid-rfp"
	reqs, _ := a.DB.ListRequirements(bidID)
	if len(reqs) < 60 || len(reqs) > 100 {
		t.Fatalf("expected 60-100 requirements, got %d", len(reqs))
	}
	traps := 0
	for _, r := range reqs {
		if strings.Contains(strings.ToLower(r.Text), "ignore all previous instructions") {
			t.Fatalf("injection line became a requirement: %s", r.Code)
		}
		if r.IsTrap {
			traps++
		}
	}
	if traps < 10 {
		t.Fatalf("expected >= 10 traps, got %d", traps)
	}
	runID, err := eng.Start(bidID, "demo", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner.Drain(ctx, 4*time.Minute)
	run, _ := a.DB.GetRun(runID)
	if run.Status != store.JobWaiting || run.Gate != Gate4 {
		steps, _ := a.DB.ListSteps(runID)
		for _, s := range steps {
			t.Logf("%s %s %s %s %s", s.Stage, s.EntityID, s.Status, s.Note, s.Error)
		}
		t.Fatalf("expected run waiting at gate 4 (QA blockers from traps), got %s at %s (%s)", run.Status, run.CurrentStage, run.Error)
	}
	reqs, _ = a.DB.ListRequirements(bidID)
	answers, _ := a.DB.LatestAnswersByBid(bidID)
	pack := a.Pack(org.ID)
	approved, evidenceBearing, refused := 0, 0, 0
	for _, r := range reqs {
		ans, ok := answers[r.ID]
		if !ok {
			t.Fatalf("requirement %s has no answer", r.Code)
		}
		if r.IsTrap {
			if ans.Status != store.StatusNeedsEvidence {
				t.Errorf("trap %s should be needs_evidence, got %s (%s)", r.Code, ans.Status, ans.ConfidenceLabel)
			} else {
				refused++
			}
			if ans.SMEQuestion == "" {
				t.Errorf("trap %s has no SME question", r.Code)
			}
			continue
		}
		if pack.IsInstruction(r.Category) {
			continue
		}
		evidenceBearing++
		if ans.Status == store.StatusApproved {
			approved++
			if a.DB.CitationCount(ans.ID) == 0 {
				t.Errorf("approved answer %s has no stored citations", r.Code)
			}
		} else {
			t.Logf("non-approved: %s %s %s", r.Code, ans.Status, ans.ConfidenceLabel)
		}
	}
	if refused != traps {
		t.Fatalf("trap refusal %d/%d", refused, traps)
	}
	if approved*100/evidenceBearing < 85 {
		t.Fatalf("only %d/%d evidence-bearing requirements approved", approved, evidenceBearing)
	}
	tasks, _ := a.DB.ListTasks(bidID)
	open := 0
	for _, tk := range tasks {
		if tk.Status == "open" {
			open++
		}
	}
	if open < traps {
		t.Fatalf("expected an SME task per trap, got %d open tasks", open)
	}
	// SME loop: answer one task → re-draft → in review → approve
	var target store.Task
	for _, tk := range tasks {
		r, _ := a.DB.GetRequirement(tk.RequirementID)
		if r.Code == "6.5" {
			target = tk
		}
	}
	sme, _ := a.DB.UserByEmail(org.ID, "meiling.chen@northstar.example")
	if err := a.RespondTask(ctx, org.ID, sme.ID, target.ID, "answer", "Northstar does not hold ISO 27001 certification. The SOC 2 Type II report covers equivalent controls and ISO 27001 certification is planned for 2027."); err != nil {
		t.Fatal(err)
	}
	ans, _ := a.DB.LatestAnswer(target.RequirementID)
	if ans.Status != store.StatusInReview || a.DB.CitationCount(ans.ID) == 0 {
		t.Fatalf("SME answer should re-draft into review with citations: %s %s", ans.Status, ans.ConfidenceLabel)
	}
	reviewer, _ := a.DB.UserByEmail(org.ID, "marcus.bell@northstar.example")
	if err := a.ApproveAnswer(org.ID, reviewer.ID, ans.ID, "ok"); err != nil {
		t.Fatal(err)
	}
	// resolve the rest of the traps the same way, then re-run QA and sign off the export gate
	tasks, _ = a.DB.ListTasks(bidID)
	for _, tk := range tasks {
		if tk.Status != "open" {
			continue
		}
		if err := a.RespondTask(ctx, org.ID, sme.ID, tk.ID, "answer", "Confirmed position for this requirement: Northstar does not currently offer this; the buyer should be told it is not available."); err != nil {
			t.Fatal(err)
		}
		if ans, err := a.DB.LatestAnswer(tk.RequirementID); err == nil && ans.UnsupportedCount == 0 && ans.Status != store.StatusNeedsEvidence {
			_ = a.ApproveAnswer(org.ID, reviewer.ID, ans.ID, "ok")
		}
	}
	// attachments required by instructions: upload placeholders so the attachment check passes
	for _, name := range []string{"Attachment-B-Pricing-Template.md", "Attachment-C-Non-Collusion.md", "Attachment-D-Certificate-of-Insurance.md", "Attachment-E-VSA.md", "Attachment-F-Terms.md"} {
		if _, _, err := a.IngestDocument(ctx, app.IngestInput{OrgID: org.ID, BidID: bidID, Name: name, Data: []byte("# " + name + "\n\nSynthetic attachment placeholder content."), Category: "attachment", ApprovalState: "n/a"}); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := a.RunQA(org.ID, bidID, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range sum.Findings {
		if f.Severity == "blocker" && f.Status == "open" {
			if f.CheckType == "placeholder_text" || f.CheckType == "missing_mandatory_answer" {
				_ = a.ResolveFinding(org.ID, reviewer.ID, f.ID, "dismissed", "test: accepted for synthetic run")
			} else {
				t.Errorf("unexpected blocker: %s %s", f.CheckType, f.Message)
			}
		}
	}
	if err := eng.ApproveGate(runID, reviewer.ID, false); err != nil {
		t.Fatal(err)
	}
	runner.Drain(ctx, 2*time.Minute)
	run, _ = a.DB.GetRun(runID)
	if run.Status != store.JobSucceeded {
		steps, _ := a.DB.ListSteps(runID)
		for _, s := range steps {
			if s.Status == store.JobFailed {
				t.Logf("failed step %s %s: %s", s.Stage, s.EntityType, s.Error)
			}
		}
		t.Fatalf("run did not finish: %s at %s (%s)", run.Status, run.CurrentStage, run.Error)
	}
	exports, _ := a.DB.ListExports(bidID)
	if len(exports) < 3 {
		t.Fatalf("expected exports, got %d", len(exports))
	}
	lib, _ := a.DB.ListLibrary(org.ID)
	if len(lib) < 40 {
		t.Fatalf("library should hold the approved answers, got %d", len(lib))
	}
	// idempotent resume: a second run skips unchanged steps
	runID2, _ := eng.Start(bidID, "demo", true, nil)
	runner.Drain(ctx, 2*time.Minute)
	steps, _ := a.DB.ListSteps(runID2)
	skipped := 0
	for _, s := range steps {
		if s.Status == "skipped" {
			skipped++
		}
	}
	if skipped == 0 {
		t.Fatal("second run should skip unchanged steps")
	}
	// questionnaire bid: library reuse + round-trip export
	qBid := org.ID + "-bid-questionnaire"
	runID3, _ := eng.Start(qBid, "demo", true, nil)
	runner.Drain(ctx, 3*time.Minute)
	qAnswers, _ := a.DB.LatestAnswersByBid(qBid)
	reused := 0
	for _, ans := range qAnswers {
		if ans.GenerationMode == "reused" {
			reused++
		}
	}
	t.Logf("questionnaire: %d answers, %d reused from library; run %s", len(qAnswers), reused, runID3)
	qDocs, _ := a.DB.ListBidDocuments(qBid)
	_, out, _, err := a.RoundTrip(org.ID, qBid, qDocs[0].ID, reviewer.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := a.DocumentBytes(qDocs[0])
	before, _ := docs.CellValues(original)
	after, err := docs.CellValues(out)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for sheet, cells := range after {
		for ref, v := range cells {
			if before[sheet][ref] != v {
				changed++
				if !strings.HasPrefix(ref, "D") || sheet != "Assessment" {
					t.Fatalf("round-trip changed a non-answer cell %s!%s", sheet, ref)
				}
			}
		}
	}
	for sheet, cells := range before {
		for ref, v := range cells {
			if after[sheet][ref] != v {
				t.Fatalf("round-trip lost original cell %s!%s", sheet, ref)
			}
		}
	}
	if changed == 0 {
		t.Fatal("round-trip wrote no answers")
	}
	t.Logf("round-trip wrote %d answer cells", changed)
}

func TestAddendumTargetsOnlyAffected(t *testing.T) {
	ctx := context.Background()
	a, runner, eng := testApp(t)
	seeder := &demo.Seeder{App: a}
	org, err := seeder.Seed(ctx, "saas-it", "", "")
	if err != nil {
		t.Fatal(err)
	}
	bidID := org.ID + "-bid-rfp"
	runID, _ := eng.Start(bidID, "demo", true, nil)
	runner.Drain(ctx, 4*time.Minute)
	_ = runID
	pack := a.Pack(org.ID)
	data, _ := demo.RenderAddendum(pack.Demo.Addendum)
	_, diff, err := a.UploadAddendum(ctx, org.ID, bidID, "", pack.Demo.Addendum.FileName, data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(diff.Changed, ",") != "4.11,9.7" || strings.Join(diff.New, ",") != "5.9,6.13" || strings.Join(diff.Removed, ",") != "10.6" {
		t.Fatalf("diff wrong: %+v", diff)
	}
	runner.Drain(ctx, 2*time.Minute)
	run, _ := a.DB.GetRun(diff.RunID)
	steps, _ := a.DB.ListSteps(run.ID)
	drafted := 0
	for _, s := range steps {
		if s.Stage == StageAutoDraft {
			drafted++
		}
	}
	if drafted != 4 {
		t.Fatalf("expected exactly 4 re-drafts (2 changed + 2 new), got %d", drafted)
	}
	reqs, _ := a.DB.ListRequirements(bidID)
	answers, _ := a.DB.LatestAnswersByBid(bidID)
	for _, r := range reqs {
		switch r.Code {
		case "4.11":
			// the old 12-month answer must not be reused; the redraft cites the 24-month extension
			if ans := answers[r.ID]; ans.GenerationMode == "reused" || !strings.Contains(ans.DraftText, "24 months") {
				t.Errorf("4.11 amended to 24 months should be freshly drafted about 24 months, got %s %q", ans.GenerationMode, ans.DraftText)
			}
		case "10.6":
			if r.ChangeState != "removed" {
				t.Errorf("10.6 should be removed")
			}
		case "5.9", "6.13":
			if answers[r.ID].ID == "" {
				t.Errorf("%s should have a fresh answer", r.Code)
			}
		}
	}
}
