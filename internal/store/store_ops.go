package store

import (
	"database/sql"
	"strings"
)

// Pipeline runs / steps ---------------------------------------------------------

const runCols = "id, bid_id, status, mode, current_stage, gate, gate_owner, gate_due, auto_gates, only_requirements, error, started_at, finished_at, updated_at"

func scanRun(r scanner) (PipelineRun, error) {
	var x PipelineRun
	var auto int
	var only string
	err := r.Scan(&x.ID, &x.BidID, &x.Status, &x.Mode, &x.CurrentStage, &x.Gate, &x.GateOwner, &x.GateDue, &auto, &only, &x.Error, &x.StartedAt, &x.FinishedAt, &x.UpdatedAt)
	x.AutoGates = auto == 1
	x.OnlyRequirements = strs(only)
	return x, err
}

func (s *DB) CreateRun(x PipelineRun) error {
	return s.Exec("INSERT INTO pipeline_runs ("+runCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		x.ID, x.BidID, x.Status, x.Mode, x.CurrentStage, x.Gate, x.GateOwner, x.GateDue, b2i(x.AutoGates), js(x.OnlyRequirements), x.Error, x.StartedAt, x.FinishedAt, x.UpdatedAt)
}

func (s *DB) UpdateRun(x PipelineRun) error {
	return s.Exec("UPDATE pipeline_runs SET status=?, current_stage=?, gate=?, gate_owner=?, gate_due=?, auto_gates=?, only_requirements=?, error=?, finished_at=?, updated_at=? WHERE id=?",
		x.Status, x.CurrentStage, x.Gate, x.GateOwner, x.GateDue, b2i(x.AutoGates), js(x.OnlyRequirements), x.Error, x.FinishedAt, x.UpdatedAt, x.ID)
}

func (s *DB) GetRun(id string) (PipelineRun, error) {
	return one(s.q, scanRun, "SELECT "+runCols+" FROM pipeline_runs WHERE id = ?", id)
}

func (s *DB) LatestRun(bidID string) (PipelineRun, error) {
	return one(s.q, scanRun, "SELECT "+runCols+" FROM pipeline_runs WHERE bid_id = ? ORDER BY started_at DESC, id DESC", bidID)
}

func (s *DB) ListRuns(bidID string) ([]PipelineRun, error) {
	return all(s.q, scanRun, "SELECT "+runCols+" FROM pipeline_runs WHERE bid_id = ? ORDER BY started_at DESC, id DESC", bidID)
}

func (s *DB) ActiveRuns() ([]PipelineRun, error) {
	return all(s.q, scanRun, "SELECT "+runCols+" FROM pipeline_runs WHERE status IN ('queued','running','waiting_for_human') ORDER BY started_at")
}

const stepCols = "id, run_id, bid_id, stage, entity_type, entity_id, input_hash, status, attempts, output_ref, error, provider_mode, note, started_at, finished_at"

func scanStep(r scanner) (PipelineStep, error) {
	var x PipelineStep
	err := r.Scan(&x.ID, &x.RunID, &x.BidID, &x.Stage, &x.EntityType, &x.EntityID, &x.InputHash, &x.Status, &x.Attempts, &x.OutputRef, &x.Error, &x.ProviderMode, &x.Note, &x.StartedAt, &x.FinishedAt)
	return x, err
}

func (s *DB) CreateStep(x PipelineStep) error {
	return s.Exec("INSERT INTO pipeline_steps ("+stepCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		x.ID, x.RunID, x.BidID, x.Stage, x.EntityType, x.EntityID, x.InputHash, x.Status, x.Attempts, x.OutputRef, x.Error, x.ProviderMode, x.Note, x.StartedAt, x.FinishedAt)
}

func (s *DB) UpdateStep(x PipelineStep) error {
	return s.Exec("UPDATE pipeline_steps SET status=?, attempts=?, output_ref=?, error=?, provider_mode=?, note=?, started_at=?, finished_at=? WHERE id=?",
		x.Status, x.Attempts, x.OutputRef, x.Error, x.ProviderMode, x.Note, x.StartedAt, x.FinishedAt, x.ID)
}

func (s *DB) GetStep(id string) (PipelineStep, error) {
	return one(s.q, scanStep, "SELECT "+stepCols+" FROM pipeline_steps WHERE id = ?", id)
}

func (s *DB) ListSteps(runID string) ([]PipelineStep, error) {
	return all(s.q, scanStep, "SELECT "+stepCols+" FROM pipeline_steps WHERE run_id = ? ORDER BY started_at, id", runID)
}

// FindSucceededStep looks for a completed step with the same idempotency key on this bid (any run).
func (s *DB) FindSucceededStep(bidID, stage, entityID, inputHash string) (PipelineStep, error) {
	return one(s.q, scanStep, "SELECT "+stepCols+" FROM pipeline_steps WHERE bid_id = ? AND stage = ? AND entity_id = ? AND input_hash = ? AND status = 'succeeded' ORDER BY finished_at DESC", bidID, stage, entityID, inputHash)
}

func (s *DB) StepStatusCounts(runID, stage string) (map[string]int, error) {
	return s.Pairs("SELECT status, COUNT(*) FROM pipeline_steps WHERE run_id = ? AND stage = ? GROUP BY status", runID, stage)
}

// Jobs --------------------------------------------------------------------------

const jobCols = "id, org_id, type, payload, status, run_at, lease_until, attempts, max_attempts, idempotency_key, progress, log, error, created_at, updated_at"

func scanJob(r scanner) (Job, error) {
	var j Job
	err := r.Scan(&j.ID, &j.OrgID, &j.Type, &j.Payload, &j.Status, &j.RunAt, &j.LeaseUntil, &j.Attempts, &j.MaxAttempts, &j.IdempotencyKey, &j.Progress, &j.Log, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// EnqueueJob inserts a job; with an idempotency key, an existing active job is returned instead.
func (s *DB) EnqueueJob(j Job) (Job, error) {
	if j.IdempotencyKey != "" {
		existing, err := one(s.q, scanJob, "SELECT "+jobCols+" FROM jobs WHERE idempotency_key = ?", j.IdempotencyKey)
		if err == nil {
			if existing.Status == JobQueued || existing.Status == JobRunning || existing.Status == JobRetryable {
				return existing, nil
			}
			// finished job with the same key: allow re-run by clearing the old key
			if err := s.Exec("UPDATE jobs SET idempotency_key = '' WHERE id = ?", existing.ID); err != nil {
				return j, err
			}
		} else if err != ErrNotFound {
			return j, err
		}
	}
	if j.ID == "" {
		j.ID = NewID()
	}
	if j.MaxAttempts == 0 {
		j.MaxAttempts = 3
	}
	if j.Status == "" {
		j.Status = JobQueued
	}
	if j.RunAt == "" {
		j.RunAt = Now()
	}
	if j.CreatedAt == "" {
		j.CreatedAt = Now()
	}
	j.UpdatedAt = j.CreatedAt
	if j.Payload == "" {
		j.Payload = "{}"
	}
	err := s.Exec("INSERT INTO jobs ("+jobCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		j.ID, j.OrgID, j.Type, j.Payload, j.Status, j.RunAt, j.LeaseUntil, j.Attempts, j.MaxAttempts, j.IdempotencyKey, j.Progress, j.Log, j.Error, j.CreatedAt, j.UpdatedAt)
	return j, err
}

// ClaimJob leases the oldest runnable job. Lease-based claiming (compare-and-set on the
// row) works on SQLite and PostgreSQL alike and survives worker crashes.
func (s *DB) ClaimJob(now, leaseUntil string) (Job, error) {
	j, err := one(s.q, scanJob, "SELECT "+jobCols+" FROM jobs WHERE run_at <= ? AND (status IN ('queued','retryable') OR (status = 'running' AND lease_until <> '' AND lease_until < ?)) ORDER BY run_at, created_at", now, now)
	if err != nil {
		return j, err
	}
	res, err := s.q.Exec("UPDATE jobs SET status = 'running', lease_until = ?, attempts = attempts + 1, updated_at = ? WHERE id = ? AND status = ? AND lease_until = ?",
		leaseUntil, now, j.ID, j.Status, j.LeaseUntil)
	if err != nil {
		return j, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return j, ErrNotFound // lost the race; caller retries
	}
	j.Status, j.LeaseUntil, j.Attempts = JobRunning, leaseUntil, j.Attempts+1
	return j, nil
}

func (s *DB) UpdateJob(j Job) error {
	return s.Exec("UPDATE jobs SET status=?, run_at=?, lease_until=?, attempts=?, progress=?, log=?, error=?, updated_at=? WHERE id=?",
		j.Status, j.RunAt, j.LeaseUntil, j.Attempts, j.Progress, j.Log, j.Error, j.UpdatedAt, j.ID)
}

func (s *DB) ExtendLease(id, leaseUntil string) error {
	return s.Exec("UPDATE jobs SET lease_until = ? WHERE id = ?", leaseUntil, id)
}

func (s *DB) GetJob(id string) (Job, error) {
	return one(s.q, scanJob, "SELECT "+jobCols+" FROM jobs WHERE id = ?", id)
}

func (s *DB) ListJobs(orgID string, limit int) ([]Job, error) {
	return all(s.q, scanJob, "SELECT "+jobCols+" FROM jobs WHERE org_id = ? OR org_id = '' ORDER BY created_at DESC LIMIT ?", orgID, limit)
}

func (s *DB) PendingJobCount() int {
	n, _ := s.Count("SELECT COUNT(*) FROM jobs WHERE status IN ('queued','retryable','running')")
	return n
}

func (s *DB) JobStatusCounts() (map[string]int, error) {
	return s.Pairs("SELECT status, COUNT(*) FROM jobs GROUP BY status")
}

// Provider calls / cache --------------------------------------------------------

func (s *DB) LogProviderCall(c ProviderCall) error {
	if c.ID == "" {
		c.ID = NewID()
	}
	if c.CreatedAt == "" {
		c.CreatedAt = Now()
	}
	return s.Exec("INSERT INTO provider_calls (id, org_id, task_kind, prompt_id, provider_id, model, mode, cached, attempts, latency_ms, input_tokens, output_tokens, outcome, error_class, fallback, repaired, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		c.ID, c.OrgID, c.TaskKind, c.PromptID, c.ProviderID, c.Model, c.Mode, b2i(c.Cached), c.Attempts, c.LatencyMs, c.InputTokens, c.OutputTokens, c.Outcome, c.ErrorClass, b2i(c.Fallback), b2i(c.Repaired), c.CreatedAt)
}

func (s *DB) ProviderStats(since string) ([]ProviderStat, error) {
	return all(s.q, func(r scanner) (ProviderStat, error) {
		var p ProviderStat
		var lat sql.NullFloat64
		err := r.Scan(&p.ProviderID, &p.Mode, &p.Calls, &p.Cached, &p.Fallbacks, &p.Repaired, &p.Errors, &lat)
		p.Latency = int(lat.Float64)
		return p, err
	}, "SELECT provider_id, mode, COUNT(*), SUM(cached), SUM(fallback), SUM(repaired), SUM(CASE WHEN outcome <> 'ok' THEN 1 ELSE 0 END), AVG(latency_ms) FROM provider_calls WHERE created_at >= ? GROUP BY provider_id, mode ORDER BY provider_id", since)
}

func (s *DB) RecentProviderCalls(limit int) ([]ProviderCall, error) {
	return all(s.q, func(r scanner) (ProviderCall, error) {
		var c ProviderCall
		var cached, fb, rep int
		err := r.Scan(&c.ID, &c.OrgID, &c.TaskKind, &c.PromptID, &c.ProviderID, &c.Model, &c.Mode, &cached, &c.Attempts, &c.LatencyMs, &c.InputTokens, &c.OutputTokens, &c.Outcome, &c.ErrorClass, &fb, &rep, &c.CreatedAt)
		c.Cached, c.Fallback, c.Repaired = cached == 1, fb == 1, rep == 1
		return c, err
	}, "SELECT id, org_id, task_kind, prompt_id, provider_id, model, mode, cached, attempts, latency_ms, input_tokens, output_tokens, outcome, error_class, fallback, repaired, created_at FROM provider_calls ORDER BY created_at DESC LIMIT ?", limit)
}

func (s *DB) CacheGet(key string) (string, bool) {
	var out string
	if err := s.q.QueryRow("SELECT output FROM ai_cache WHERE key = ?", key).Scan(&out); err != nil {
		return "", false
	}
	return out, true
}

func (s *DB) CachePut(key, providerID, model, output string) error {
	return s.Exec("INSERT INTO ai_cache (key, provider_id, model, output, created_at) VALUES (?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET output = excluded.output, provider_id = excluded.provider_id, model = excluded.model", key, providerID, model, output, Now())
}

func (s *DB) CacheStats() (int, error) { return s.Count("SELECT COUNT(*) FROM ai_cache") }

// Eval runs ---------------------------------------------------------------------

func (s *DB) CreateEvalRun(e EvalRun) error {
	return s.Exec("INSERT INTO eval_runs (id, suite, provider_mode, metrics, passed, report_path, created_at) VALUES (?,?,?,?,?,?,?)", e.ID, e.Suite, e.ProviderMode, e.Metrics, b2i(e.Passed), e.ReportPath, e.CreatedAt)
}

func scanEval(r scanner) (EvalRun, error) {
	var e EvalRun
	var p int
	err := r.Scan(&e.ID, &e.Suite, &e.ProviderMode, &e.Metrics, &p, &e.ReportPath, &e.CreatedAt)
	e.Passed = p == 1
	return e, err
}

func (s *DB) ListEvalRuns(limit int) ([]EvalRun, error) {
	return all(s.q, scanEval, "SELECT id, suite, provider_mode, metrics, passed, report_path, created_at FROM eval_runs ORDER BY created_at DESC LIMIT ?", limit)
}

// Activity / notifications ------------------------------------------------------

func (s *DB) LogActivity(a Activity) error {
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.CreatedAt == "" {
		a.CreatedAt = Now()
	}
	if a.Metadata == "" {
		a.Metadata = "{}"
	}
	return s.Exec("INSERT INTO activity_log (id, org_id, user_id, event_type, entity_type, entity_id, metadata, created_at) VALUES (?,?,?,?,?,?,?,?)",
		a.ID, a.OrgID, a.UserID, a.EventType, a.EntityType, a.EntityID, a.Metadata, a.CreatedAt)
}

func scanActivity(r scanner) (Activity, error) {
	var a Activity
	err := r.Scan(&a.ID, &a.OrgID, &a.UserID, &a.EventType, &a.EntityType, &a.EntityID, &a.Metadata, &a.CreatedAt)
	return a, err
}

func (s *DB) ListActivity(orgID string, limit int) ([]Activity, error) {
	return all(s.q, scanActivity, "SELECT id, org_id, user_id, event_type, entity_type, entity_id, metadata, created_at FROM activity_log WHERE org_id = ? ORDER BY created_at DESC, id DESC LIMIT ?", orgID, limit)
}

func (s *DB) ActivityForEntity(entityID string, limit int) ([]Activity, error) {
	return all(s.q, scanActivity, "SELECT id, org_id, user_id, event_type, entity_type, entity_id, metadata, created_at FROM activity_log WHERE entity_id = ? ORDER BY created_at DESC, id DESC LIMIT ?", entityID, limit)
}

func (s *DB) ActivitySince(orgID, since string) ([]Activity, error) {
	return all(s.q, scanActivity, "SELECT id, org_id, user_id, event_type, entity_type, entity_id, metadata, created_at FROM activity_log WHERE org_id = ? AND created_at >= ? ORDER BY created_at DESC", orgID, since)
}

func (s *DB) CreateNotification(n Notification) error {
	if n.ID == "" {
		n.ID = NewID()
	}
	if n.CreatedAt == "" {
		n.CreatedAt = Now()
	}
	return s.Exec("INSERT INTO notifications (id, org_id, user_id, title, body, link, read, created_at) VALUES (?,?,?,?,?,?,?,?)",
		n.ID, n.OrgID, n.UserID, n.Title, n.Body, n.Link, b2i(n.Read), n.CreatedAt)
}

func (s *DB) ListNotifications(userID string, limit int) ([]Notification, error) {
	return all(s.q, func(r scanner) (Notification, error) {
		var n Notification
		var rd int
		err := r.Scan(&n.ID, &n.OrgID, &n.UserID, &n.Title, &n.Body, &n.Link, &rd, &n.CreatedAt)
		n.Read = rd == 1
		return n, err
	}, "SELECT id, org_id, user_id, title, body, link, read, created_at FROM notifications WHERE user_id = ? ORDER BY created_at DESC LIMIT ?", userID, limit)
}

func (s *DB) UnreadCount(userID string) int {
	n, _ := s.Count("SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read = 0", userID)
	return n
}

func (s *DB) MarkNotificationsRead(userID string) error {
	return s.Exec("UPDATE notifications SET read = 1 WHERE user_id = ?", userID)
}

var _ = strings.Join
