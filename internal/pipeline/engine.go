// Package pipeline orchestrates the bid workflow as a state machine on top of the job
// runner: INTAKE → PARSE → EXTRACT → [Gate 1] → QUALIFY → [Gate 2] → AUTO_DRAFT (fan-out)
// → [Gate 3] → ASSEMBLE → QA → [Gate 4] → EXPORT → LEARN. Every stage is a job; every
// step is idempotent on (stage, entity, input hash); gates are explicit WAITING_FOR_HUMAN
// states that only demo mode may pre-approve.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"bidos/internal/app"
	"bidos/internal/jobs"
	"bidos/internal/store"
)

const (
	StageIntake    = "INTAKE"
	StageParse     = "PARSE"
	StageExtract   = "EXTRACT"
	Gate1          = "GATE_1_CONFIRM_REQUIREMENTS"
	StageQualify   = "QUALIFY"
	Gate2          = "GATE_2_BID_DECISION"
	StageAutoDraft = "AUTO_DRAFT"
	Gate3          = "GATE_3_REVIEWER_APPROVALS"
	StageAssemble  = "ASSEMBLE"
	StageQA        = "QA"
	Gate4          = "GATE_4_EXPORT_SIGNOFF"
	StageExport    = "EXPORT"
	StageLearn     = "LEARN"
	StageDone      = "DONE"
)

// Order is the canonical stage sequence.
var Order = []string{StageIntake, StageParse, StageExtract, Gate1, StageQualify, Gate2, StageAutoDraft, Gate3, StageAssemble, StageQA, Gate4, StageExport, StageLearn, StageDone}

// IsGate reports whether a stage is a human gate.
func IsGate(stage string) bool { return strings.HasPrefix(stage, "GATE_") }

func next(stage string) string {
	for i, s := range Order {
		if s == stage && i+1 < len(Order) {
			return Order[i+1]
		}
	}
	return StageDone
}

// Engine runs pipelines.
type Engine struct {
	App  *app.App
	Jobs *jobs.Runner
}

type advancePayload struct {
	RunID string `json:"runId"`
}

type draftPayload struct {
	RunID string `json:"runId"`
	ReqID string `json:"reqId"`
}

// New wires the engine into the runner and the app hooks.
func New(a *app.App, j *jobs.Runner) *Engine {
	e := &Engine{App: a, Jobs: j}
	j.Handle("pipeline.advance", e.handleAdvance)
	j.Handle("pipeline.draft", e.handleDraft)
	a.Hooks.StartPipeline = e.Start
	return e
}

// Start creates a run (or resumes the active one) and enqueues the first stage.
func (e *Engine) Start(bidID, mode string, autoGates bool, only []string) (string, error) {
	bid, err := e.App.DB.GetBidByID(bidID)
	if err != nil {
		return "", err
	}
	if run, err := e.App.DB.LatestRun(bidID); err == nil && len(only) == 0 && (run.Status == store.JobQueued || run.Status == store.JobRunning || run.Status == store.JobWaiting) {
		if run.Status == store.JobWaiting && autoGates {
			return run.ID, e.ApproveGate(run.ID, "", true)
		}
		if run.Status == store.JobWaiting {
			return run.ID, nil
		}
		return run.ID, e.enqueueAdvance(run)
	}
	now := e.App.Now()
	run := store.PipelineRun{ID: store.NewID(), BidID: bidID, Status: store.JobQueued, Mode: mode, CurrentStage: StageIntake, AutoGates: autoGates, OnlyRequirements: only, StartedAt: now, UpdatedAt: now}
	if len(only) > 0 {
		run.CurrentStage = StageAutoDraft // targeted re-draft (addendum / SME update)
	}
	if err := e.App.DB.CreateRun(run); err != nil {
		return "", err
	}
	e.App.Activity(bid.OrgID, "", "pipeline.started", "bid", bidID, map[string]any{"run": run.ID, "mode": mode, "autoGates": autoGates})
	return run.ID, e.enqueueAdvance(run)
}

func (e *Engine) enqueueAdvance(run store.PipelineRun) error {
	_, err := e.Jobs.Enqueue(e.orgOf(run), "pipeline.advance", advancePayload{RunID: run.ID}, "pipeline.advance:"+run.ID+":"+run.CurrentStage)
	return err
}

func (e *Engine) orgOf(run store.PipelineRun) string {
	b, err := e.App.DB.GetBidByID(run.BidID)
	if err != nil {
		return ""
	}
	return b.OrgID
}

// Resume continues a run from its first non-succeeded step.
func (e *Engine) Resume(runID string) error {
	run, err := e.App.DB.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status == store.JobSucceeded || run.Status == store.JobCancelled {
		return errors.New("run is finished")
	}
	if run.Status == store.JobWaiting {
		return nil
	}
	run.Status, run.Error, run.UpdatedAt = store.JobQueued, "", e.App.Now()
	_ = e.App.DB.UpdateRun(run)
	return e.enqueueAdvance(run)
}

// Cancel stops a run.
func (e *Engine) Cancel(runID string) error {
	run, err := e.App.DB.GetRun(runID)
	if err != nil {
		return err
	}
	run.Status, run.FinishedAt, run.UpdatedAt = store.JobCancelled, e.App.Now(), e.App.Now()
	return e.App.DB.UpdateRun(run)
}

// ApproveGate records the human (or demo) approval and continues the run.
func (e *Engine) ApproveGate(runID, userID string, demoAuto bool) error {
	run, err := e.App.DB.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status != store.JobWaiting {
		return errors.New("run is not waiting at a gate")
	}
	note := "approved by " + userID
	if demoAuto {
		note = "auto-approved (demo mode)"
	}
	now := e.App.Now()
	_ = e.App.DB.CreateStep(store.PipelineStep{ID: store.NewID(), RunID: run.ID, BidID: run.BidID, Stage: run.Gate, EntityType: "gate", EntityID: run.BidID, Status: store.JobSucceeded, Attempts: 1, Note: note, StartedAt: now, FinishedAt: now})
	run.Status, run.CurrentStage, run.Gate, run.GateOwner, run.GateDue, run.UpdatedAt = store.JobQueued, next(run.Gate), "", "", "", now
	if err := e.App.DB.UpdateRun(run); err != nil {
		return err
	}
	e.App.Activity(e.orgOf(run), userID, "pipeline.gate_approved", "bid", run.BidID, map[string]any{"gate": run.Gate, "demo": demoAuto})
	return e.enqueueAdvance(run)
}

// handleAdvance executes the run's current stage, then enqueues the next one.
func (e *Engine) handleAdvance(ctx context.Context, job store.Job, report func(int, string)) error {
	p, err := jobs.Payload[advancePayload](job)
	if err != nil {
		return err
	}
	run, err := e.App.DB.GetRun(p.RunID)
	if err != nil {
		return err
	}
	if run.Status == store.JobCancelled || run.Status == store.JobSucceeded {
		return nil
	}
	bid, err := e.App.DB.GetBidByID(run.BidID)
	if err != nil {
		return err
	}
	orgID := bid.OrgID
	run.Status, run.UpdatedAt = store.JobRunning, e.App.Now()
	_ = e.App.DB.UpdateRun(run)
	stage := run.CurrentStage
	report(5, "stage "+stage)
	fail := func(msg string) error {
		run.Status, run.Error, run.UpdatedAt = store.JobFailed, msg, e.App.Now()
		_ = e.App.DB.UpdateRun(run)
		e.App.Activity(orgID, "", "pipeline.failed", "bid", run.BidID, map[string]any{"stage": stage, "error": msg})
		return errors.New(msg)
	}
	wait := func(gate string, ownerRole string) error {
		owner := bid.OwnerID
		if ownerRole != "" {
			if users, _ := e.App.DB.UsersByRole(orgID, ownerRole); len(users) > 0 {
				owner = users[0].ID
			}
		}
		run.Status, run.Gate, run.GateOwner, run.GateDue, run.UpdatedAt = store.JobWaiting, gate, owner, store.FormatTime(time.Now().Add(48*time.Hour)), e.App.Now()
		_ = e.App.DB.UpdateRun(run)
		if owner != "" {
			e.App.Notify(orgID, owner, "Pipeline gate: "+humanGate(gate), "Bid "+bid.Name+" is waiting for your decision.", "/bids/"+bid.ID+"/pipeline")
		}
		return nil
	}
	proceed := func(nextStage string) error {
		run.CurrentStage, run.Status, run.UpdatedAt = nextStage, store.JobQueued, e.App.Now()
		if nextStage == StageDone {
			run.Status, run.FinishedAt = store.JobSucceeded, e.App.Now()
			_ = e.App.DB.UpdateRun(run)
			e.App.Activity(orgID, "", "pipeline.completed", "bid", run.BidID, map[string]any{"run": run.ID})
			return nil
		}
		if err := e.App.DB.UpdateRun(run); err != nil {
			return err
		}
		return e.enqueueAdvance(run)
	}
	buyerDocs := func() []store.Document {
		docs, _ := e.App.DB.ListBidDocuments(bid.ID)
		var out []store.Document
		for _, d := range docs {
			if d.Category == "addendum" || d.SourceType == "addendum" {
				continue
			}
			out = append(out, d)
		}
		return out
	}

	switch stage {
	case StageIntake:
		docs := buyerDocs()
		if len(docs) == 0 {
			return fail("Upload the RFP or questionnaire before running the pipeline.")
		}
		e.step(run, StageIntake, "bid", bid.ID, hash(fmt.Sprint(len(docs))), func() (string, string, string, error) {
			return "", fmt.Sprintf("%d buyer document(s) present", len(docs)), "", nil
		})
		return proceed(StageParse)
	case StageParse:
		for _, d := range buyerDocs() {
			d := d
			e.step(run, StageParse, "document", d.ID, hash(d.Hash), func() (string, string, string, error) {
				parsed, err := e.App.ParseStored(d)
				if err != nil {
					return "", "", "", err
				}
				return d.ID, fmt.Sprintf("%s: %d sections, %d tables", d.Name, len(parsed.Sections), len(parsed.Tables)), "", nil
			})
		}
		return proceed(StageExtract)
	case StageExtract:
		pack := e.App.Pack(orgID)
		for _, d := range buyerDocs() {
			d := d
			e.step(run, StageExtract, "document", d.ID, hash(d.Hash, pack.Version, "extract.requirements@2"), func() (string, string, string, error) {
				res, err := e.App.ExtractRequirements(ctx, orgID, bid.ID, d.ID, app.ExtractOptions{TrapCodes: trapCodes(e.App, orgID)})
				if err != nil {
					return "", "", "", err
				}
				return d.ID, fmt.Sprintf("%d added, %d updated, %d unanchored, %d injection line(s) blocked", res.Added, res.Updated, res.Unanchored, len(res.Injections)), res.ProviderMode, nil
			})
		}
		return proceed(Gate1)
	case Gate1:
		if !run.AutoGates {
			return wait(Gate1, "")
		}
		n, _ := e.App.ConfirmAllRequirements(orgID, "", bid.ID)
		e.gateStep(run, Gate1, fmt.Sprintf("auto-approved (demo mode): %d requirements confirmed", n))
		return proceed(StageQualify)
	case StageQualify:
		e.step(run, StageQualify, "bid", bid.ID, hash(e.App.Now()[:13]), func() (string, string, string, error) {
			q, err := e.App.Qualify(orgID, bid.ID)
			if err != nil {
				return "", "", "", err
			}
			return "", fmt.Sprintf("priority %s (%.0f%% weighted signal), %d flags", q.Priority, q.Weighted*100, len(q.Qualification.Flags)), "", nil
		})
		return proceed(Gate2)
	case Gate2:
		if !run.AutoGates {
			return wait(Gate2, "")
		}
		if bid.Decision == "" {
			_ = e.App.SetBidDecision(orgID, "", bid.ID, "bid")
		}
		e.gateStep(run, Gate2, "auto-approved (demo mode): decision = bid")
		return proceed(StageAutoDraft)
	case StageAutoDraft:
		return e.autoDraft(ctx, run, bid, report, proceed)
	case Gate3:
		if !run.AutoGates {
			return wait(Gate3, store.RoleReviewer)
		}
		n := e.autoApprove(orgID, bid.ID)
		e.gateStep(run, Gate3, fmt.Sprintf("auto-approved (demo mode): %d verified answers approved; evidence gaps left for SMEs", n))
		return proceed(StageAssemble)
	case StageAssemble:
		e.step(run, StageAssemble, "bid", bid.ID, hash(approvedFingerprint(e.App, bid.ID)), func() (string, string, string, error) {
			n, err := e.App.AutoPlaceAnswers(orgID, "", bid.ID)
			if err != nil {
				return "", "", "", err
			}
			return "", fmt.Sprintf("%d approved answers placed into proposal sections", n), "", nil
		})
		return proceed(StageQA)
	case StageQA:
		e.step(run, StageQA, "bid", bid.ID, hash(e.App.Now()), func() (string, string, string, error) {
			sum, err := e.App.RunQA(orgID, bid.ID, "")
			if err != nil {
				return "", "", "", err
			}
			return "", fmt.Sprintf("%d of %d checks passed; %d blockers, %d warnings", sum.Passed, sum.Total, sum.Blockers, sum.Warnings), "", nil
		})
		return proceed(Gate4)
	case Gate4:
		sum, _ := e.App.QASummary(orgID, bid.ID)
		if sum.Blockers > 0 || !run.AutoGates {
			if sum.Blockers > 0 {
				run.Error = fmt.Sprintf("%d QA blocker(s) must be resolved before export sign-off", sum.Blockers)
			}
			return wait(Gate4, store.RoleProposalManager)
		}
		e.gateStep(run, Gate4, "auto-approved (demo mode): zero QA blockers")
		return proceed(StageExport)
	case StageExport:
		// QA re-check right before export so fixes made at the gate count.
		if sum, err := e.App.RunQA(orgID, bid.ID, ""); err == nil {
			e.step(run, StageExport, "qa-recheck", bid.ID, hash(e.App.Now()), func() (string, string, string, error) {
				return "", fmt.Sprintf("QA re-check: %d blockers, %d warnings", sum.Blockers, sum.Warnings), "", nil
			})
		}
		if err := e.App.ExportGate(orgID, bid.ID); err != nil {
			run.Error = err.Error()
			return wait(Gate4, store.RoleProposalManager)
		}
		for _, format := range []string{"docx", "csv", "json"} {
			format := format
			e.step(run, StageExport, "export:"+format, bid.ID, hash(approvedFingerprint(e.App, bid.ID), format), func() (string, string, string, error) {
				job, _, _, err := e.App.ExportBid(orgID, bid.ID, format, "", false)
				if err != nil {
					return "", "", "", err
				}
				return job.ID, "exported " + format, "", nil
			})
		}
		for _, d := range buyerDocs() {
			if !strings.HasSuffix(strings.ToLower(d.Name), ".xlsx") && !(strings.HasSuffix(strings.ToLower(d.Name), ".docx") && d.Category == "questionnaire") {
				continue
			}
			d := d
			e.step(run, StageExport, "roundtrip", d.ID, hash(approvedFingerprint(e.App, bid.ID), d.ID), func() (string, string, string, error) {
				job, _, _, err := e.App.RoundTrip(orgID, bid.ID, d.ID, "", true)
				if err != nil {
					return "", "", "", err
				}
				return job.ID, "round-trip export into " + d.Name, "", nil
			})
		}
		return proceed(StageLearn)
	case StageLearn:
		e.step(run, StageLearn, "bid", bid.ID, hash(approvedFingerprint(e.App, bid.ID)), func() (string, string, string, error) {
			answers, _ := e.App.DB.LatestAnswersByBid(bid.ID)
			reqs, _ := e.App.DB.ListRequirements(bid.ID)
			pack := e.App.Pack(orgID)
			n := 0
			for _, r := range reqs {
				ans, ok := answers[r.ID]
				if !ok || ans.Status != store.StatusApproved || pack.IsInstruction(r.Category) {
					continue
				}
				if _, err := e.App.PromoteAnswer(orgID, ans.ApprovedBy, ans.ID); err == nil {
					n++
				}
			}
			return "", fmt.Sprintf("%d approved answers available in the Answer Library", n), "", nil
		})
		return proceed(StageDone)
	case StageDone:
		return proceed(StageDone)
	}
	return fail("unknown stage " + stage)
}

func (e *Engine) autoDraft(ctx context.Context, run store.PipelineRun, bid store.Bid, report func(int, string), proceed func(string) error) error {
	orgID := bid.OrgID
	reqs, _ := e.App.DB.ListRequirements(bid.ID)
	only := map[string]bool{}
	for _, id := range run.OnlyRequirements {
		only[id] = true
	}
	kb := knowledgeFingerprint(e.App, orgID)
	queued, done := 0, 0
	for _, r := range reqs {
		if r.ChangeState == "removed" {
			continue
		}
		if len(only) > 0 && !only[r.ID] {
			continue
		}
		if len(only) == 0 && r.Status == store.StatusApproved {
			continue
		}
		h := hash(r.Text, r.Category, kb, "draft.answer@3", fmt.Sprint(len(only) > 0))
		if _, err := e.App.DB.FindSucceededStep(bid.ID, StageAutoDraft, r.ID, h); err == nil {
			done++
			continue
		}
		existing := e.stepFor(run.ID, StageAutoDraft, r.ID)
		if existing.ID == "" {
			_ = e.App.DB.CreateStep(store.PipelineStep{ID: store.NewID(), RunID: run.ID, BidID: bid.ID, Stage: StageAutoDraft, EntityType: "requirement", EntityID: r.ID, InputHash: h, Status: store.JobQueued})
		} else if existing.Status == store.JobSucceeded {
			done++
			continue
		}
		_, _ = e.Jobs.Enqueue(orgID, "pipeline.draft", draftPayload{RunID: run.ID, ReqID: r.ID}, "pipeline.draft:"+run.ID+":"+r.ID)
		queued++
	}
	counts, _ := e.App.DB.StepStatusCounts(run.ID, StageAutoDraft)
	pending := counts[store.JobQueued] + counts[store.JobRunning]
	total := 0
	for _, v := range counts {
		total += v
	}
	report(50, fmt.Sprintf("AUTO_DRAFT: %d pending, %d succeeded, %d failed", pending, counts[store.JobSucceeded], counts[store.JobFailed]))
	if pending > 0 {
		run.UpdatedAt = e.App.Now()
		_ = e.App.DB.UpdateRun(run)
		_, err := e.Jobs.EnqueueAt(orgID, "pipeline.advance", advancePayload{RunID: run.ID}, "", time.Now().Add(1200*time.Millisecond))
		return err
	}
	e.App.Activity(orgID, "", "pipeline.auto_draft_done", "bid", bid.ID, map[string]any{"succeeded": counts[store.JobSucceeded], "failed": counts[store.JobFailed], "skipped": done})
	return proceed(Gate3)
}

func (e *Engine) handleDraft(ctx context.Context, job store.Job, report func(int, string)) error {
	p, err := jobs.Payload[draftPayload](job)
	if err != nil {
		return err
	}
	run, err := e.App.DB.GetRun(p.RunID)
	if err != nil {
		return err
	}
	if run.Status == store.JobCancelled {
		return nil
	}
	bid, err := e.App.DB.GetBidByID(run.BidID)
	if err != nil {
		return err
	}
	step := e.stepFor(run.ID, StageAutoDraft, p.ReqID)
	if step.ID == "" {
		return fmt.Errorf("step missing for requirement %s", p.ReqID)
	}
	if step.Status == store.JobSucceeded {
		return nil
	}
	step.Status, step.Attempts, step.StartedAt = store.JobRunning, step.Attempts+1, e.App.Now()
	_ = e.App.DB.UpdateStep(step)
	force := len(run.OnlyRequirements) > 0
	res, err := e.App.GenerateAnswer(ctx, bid.OrgID, p.ReqID, app.GenerateOptions{Force: force, Mode: "pipeline"})
	step.FinishedAt = e.App.Now()
	if err != nil {
		step.Status, step.Error = store.JobFailed, err.Error()
		_ = e.App.DB.UpdateStep(step)
		return nil // the run continues; QA surfaces the gap
	}
	step.Status, step.OutputRef, step.ProviderMode = store.JobSucceeded, res.Answer.ID, res.ProviderMode
	switch {
	case res.Skipped:
		step.Note = "already approved; skipped"
	case res.Reused:
		step.Note = "reused library answer → " + res.Answer.Status
	default:
		step.Note = res.Answer.ConfidenceLabel + " → " + res.Answer.Status
	}
	_ = e.App.DB.UpdateStep(step)
	report(100, step.Note)
	return nil
}

// autoApprove approves verified answers on the reviewer's behalf (demo gates only).
func (e *Engine) autoApprove(orgID, bidID string) int {
	reviewer := ""
	if users, _ := e.App.DB.UsersByRole(orgID, store.RoleReviewer); len(users) > 0 {
		reviewer = users[0].ID
	}
	answers, _ := e.App.DB.LatestAnswersByBid(bidID)
	reqs, _ := e.App.DB.ListRequirements(bidID)
	n := 0
	for _, r := range reqs {
		ans, ok := answers[r.ID]
		if !ok || r.ChangeState == "removed" {
			continue
		}
		if ans.Status != store.StatusInReview && ans.Status != store.StatusDrafted {
			continue
		}
		if ans.UnsupportedCount > 0 {
			continue
		}
		if err := e.App.ApproveAnswer(orgID, reviewer, ans.ID, "Auto-approved by demo gate (synthetic data only)."); err == nil {
			n++
		}
	}
	return n
}

func (e *Engine) gateStep(run store.PipelineRun, gate, note string) {
	now := e.App.Now()
	_ = e.App.DB.CreateStep(store.PipelineStep{ID: store.NewID(), RunID: run.ID, BidID: run.BidID, Stage: gate, EntityType: "gate", EntityID: run.BidID, Status: store.JobSucceeded, Attempts: 1, Note: note, StartedAt: now, FinishedAt: now})
}

// step runs fn once per (stage, entity, inputHash); unchanged inputs are skipped.
func (e *Engine) step(run store.PipelineRun, stage, entityType, entityID, inputHash string, fn func() (outputRef, note, providerMode string, err error)) {
	now := e.App.Now()
	if prev, err := e.App.DB.FindSucceededStep(run.BidID, stage, entityID, inputHash); err == nil {
		_ = e.App.DB.CreateStep(store.PipelineStep{ID: store.NewID(), RunID: run.ID, BidID: run.BidID, Stage: stage, EntityType: entityType, EntityID: entityID, InputHash: inputHash, Status: "skipped", Note: "unchanged input; reused result from " + prev.FinishedAt, OutputRef: prev.OutputRef, StartedAt: now, FinishedAt: now})
		return
	}
	st := store.PipelineStep{ID: store.NewID(), RunID: run.ID, BidID: run.BidID, Stage: stage, EntityType: entityType, EntityID: entityID, InputHash: inputHash, Status: store.JobRunning, Attempts: 1, StartedAt: now}
	_ = e.App.DB.CreateStep(st)
	out, note, mode, err := fn()
	st.FinishedAt = e.App.Now()
	if err != nil {
		st.Status, st.Error = store.JobFailed, err.Error()
	} else {
		st.Status, st.OutputRef, st.Note, st.ProviderMode = store.JobSucceeded, out, note, mode
	}
	_ = e.App.DB.UpdateStep(st)
}

func (e *Engine) stepFor(runID, stage, entityID string) store.PipelineStep {
	steps, _ := e.App.DB.ListSteps(runID)
	for _, s := range steps {
		if s.Stage == stage && s.EntityID == entityID {
			return s
		}
	}
	return store.PipelineStep{}
}

func hash(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:8])
}

func knowledgeFingerprint(a *app.App, orgID string) string {
	n, _ := a.DB.Count("SELECT COUNT(*) FROM document_chunks c JOIN documents d ON d.id = c.document_id WHERE d.org_id = ? AND d.bid_id = '' AND d.approval_state = 'approved'", orgID)
	last, _ := a.DB.Strings("SELECT COALESCE(MAX(created_at), '') FROM documents WHERE org_id = ? AND bid_id = ''", orgID)
	lib, _ := a.DB.Count("SELECT COUNT(*) FROM answer_library WHERE org_id = ?", orgID)
	return fmt.Sprintf("%d|%s|%d", n, strings.Join(last, ""), lib)
}

func approvedFingerprint(a *app.App, bidID string) string {
	ids, _ := a.DB.Strings("SELECT id FROM answers WHERE status = 'approved' AND requirement_id IN (SELECT id FROM requirements WHERE bid_id = ?) ORDER BY id", bidID)
	return strings.Join(ids, ",")
}

func trapCodes(a *app.App, orgID string) map[string]bool {
	org, err := a.DB.GetOrg(orgID)
	if err != nil || org.Mode != "demo" {
		return nil
	}
	pack := a.Pack(orgID)
	m := map[string]bool{}
	for _, it := range pack.Demo.RFP.AllItems() {
		if it.Trap {
			m[it.Code] = true
		}
	}
	for _, r := range pack.Demo.Questionnaire.Rows {
		if r.Trap {
			m[r.ID] = true
		}
	}
	return m
}

func humanGate(g string) string {
	switch g {
	case Gate1:
		return "Confirm requirements"
	case Gate2:
		return "Bid / no-bid decision"
	case Gate3:
		return "Reviewer approvals"
	case Gate4:
		return "Export sign-off"
	}
	return g
}

// HumanStage returns a display label for a stage.
func HumanStage(s string) string {
	if IsGate(s) {
		return "Gate: " + humanGate(s)
	}
	switch s {
	case StageAutoDraft:
		return "Auto-draft"
	case StageQA:
		return "QA"
	}
	return strings.Title(strings.ToLower(s))
}

// Progress returns percent complete for a run based on stage index.
func Progress(run store.PipelineRun) int {
	for i, s := range Order {
		if s == run.CurrentStage {
			return i * 100 / (len(Order) - 1)
		}
	}
	return 0
}

// StageView is used by the stage tracker.
type StageView struct {
	Stage   string
	Label   string
	Gate    bool
	State   string // done | current | waiting | pending | failed
	Percent int
}

// StageViews renders the tracker for a run.
func StageViews(run *store.PipelineRun) []StageView {
	var out []StageView
	cur := -1
	if run != nil {
		for i, s := range Order {
			if s == run.CurrentStage {
				cur = i
			}
		}
	}
	for i, s := range Order {
		if s == StageDone {
			continue
		}
		v := StageView{Stage: s, Label: HumanStage(s), Gate: IsGate(s), Percent: i * 100 / (len(Order) - 1)}
		switch {
		case run == nil:
			v.State = "pending"
		case i < cur || run.Status == store.JobSucceeded:
			v.State = "done"
		case i == cur && run.Status == store.JobWaiting:
			v.State = "waiting"
		case i == cur && run.Status == store.JobFailed:
			v.State = "failed"
		case i == cur:
			v.State = "current"
		default:
			v.State = "pending"
		}
		out = append(out, v)
	}
	return out
}
