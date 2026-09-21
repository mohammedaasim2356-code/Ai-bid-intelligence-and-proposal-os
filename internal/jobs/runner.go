// Package jobs is the DbJobRunner: a database-backed queue with lease-based claiming
// (compare-and-set on the row), retries with backoff for transient errors, progress and
// logs on the job row, and periodic schedules. It works on SQLite and PostgreSQL, survives
// restarts (expired leases are reclaimed) and needs no vendor.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"bidos/internal/store"
)

// Handler processes one job. report() appends progress + a log line.
type Handler func(ctx context.Context, job store.Job, report func(progress int, msg string)) error

// Transient marks an error as retryable.
type Transient struct{ Err error }

func (t *Transient) Error() string { return "transient: " + t.Err.Error() }
func (t *Transient) Unwrap() error { return t.Err }

// Retry wraps an error as transient.
func Retry(err error) error { return &Transient{Err: err} }

// Schedule enqueues a job every period (idempotent per period window).
type Schedule struct {
	Every   time.Duration
	Type    string
	Payload string
}

// Runner drains the jobs table.
type Runner struct {
	DB       *store.DB
	Log      *slog.Logger
	Workers  int
	Lease    time.Duration
	Poll     time.Duration
	handlers map[string]Handler
	sched    []Schedule
	mu       sync.Mutex
	stop     chan struct{}
	wg       sync.WaitGroup
	running  bool
}

func New(db *store.DB, log *slog.Logger, workers int) *Runner {
	if log == nil {
		log = slog.Default()
	}
	if workers <= 0 {
		workers = 2
	}
	return &Runner{DB: db, Log: log, Workers: workers, Lease: 2 * time.Minute, Poll: 400 * time.Millisecond, handlers: map[string]Handler{}}
}

// Handle registers a handler for a job type.
func (r *Runner) Handle(typ string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[typ] = h
}

// Every registers a periodic schedule.
func (r *Runner) Every(d time.Duration, typ, payload string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sched = append(r.sched, Schedule{Every: d, Type: typ, Payload: payload})
}

// Enqueue adds a job (payload is JSON-encoded). idemKey may be empty.
func (r *Runner) Enqueue(orgID, typ string, payload any, idemKey string) (store.Job, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return store.Job{}, err
	}
	return r.DB.EnqueueJob(store.Job{OrgID: orgID, Type: typ, Payload: string(b), IdempotencyKey: idemKey})
}

// EnqueueAt adds a delayed job.
func (r *Runner) EnqueueAt(orgID, typ string, payload any, idemKey string, runAt time.Time) (store.Job, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return store.Job{}, err
	}
	return r.DB.EnqueueJob(store.Job{OrgID: orgID, Type: typ, Payload: string(b), IdempotencyKey: idemKey, RunAt: store.FormatTime(runAt)})
}

// Start launches worker goroutines and the scheduler.
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.stop = make(chan struct{})
	r.mu.Unlock()
	for i := 0; i < r.Workers; i++ {
		r.wg.Add(1)
		go func(n int) {
			defer r.wg.Done()
			for {
				select {
				case <-r.stop:
					return
				case <-ctx.Done():
					return
				default:
				}
				if !r.RunOne(ctx) {
					select {
					case <-time.After(r.Poll):
					case <-r.stop:
						return
					case <-ctx.Done():
						return
					}
				}
			}
		}(i)
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		r.fireSchedules()
		for {
			select {
			case <-t.C:
				r.fireSchedules()
			case <-r.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop halts workers (running jobs finish their current handler call).
func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	close(r.stop)
	r.mu.Unlock()
	r.wg.Wait()
}

func (r *Runner) fireSchedules() {
	r.mu.Lock()
	sched := append([]Schedule{}, r.sched...)
	r.mu.Unlock()
	now := time.Now().UTC()
	for _, s := range sched {
		window := now.Truncate(s.Every).Format(time.RFC3339)
		_, _ = r.DB.EnqueueJob(store.Job{Type: s.Type, Payload: s.Payload, IdempotencyKey: s.Type + "@" + window, MaxAttempts: 1})
	}
}

// Tick fires due schedules and drains up to max jobs (for host cron → /api/cron/tick).
func (r *Runner) Tick(ctx context.Context, max int) int {
	r.fireSchedules()
	n := 0
	for n < max && r.RunOne(ctx) {
		n++
	}
	return n
}

// RunOne claims and executes a single job; returns false when nothing was runnable.
func (r *Runner) RunOne(ctx context.Context) bool {
	now := time.Now().UTC()
	job, err := r.DB.ClaimJob(store.FormatTime(now), store.FormatTime(now.Add(r.Lease)))
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			r.Log.Warn("job claim failed", "err", err)
		}
		return false
	}
	r.mu.Lock()
	h, ok := r.handlers[job.Type]
	r.mu.Unlock()
	if !ok {
		job.Status, job.Error, job.UpdatedAt = store.JobFailed, "no handler registered for "+job.Type, store.Now()
		_ = r.DB.UpdateJob(job)
		return true
	}
	// heartbeat keeps the lease alive while the handler runs
	hbStop := make(chan struct{})
	go func() {
		t := time.NewTicker(r.Lease / 3)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = r.DB.ExtendLease(job.ID, store.FormatTime(time.Now().UTC().Add(r.Lease)))
			case <-hbStop:
				return
			}
		}
	}()
	logLines := []string{}
	if job.Log != "" {
		logLines = strings.Split(job.Log, "\n")
	}
	appendLog := func(msg string) {
		logLines = append(logLines, time.Now().UTC().Format("15:04:05")+" "+msg)
		if len(logLines) > 60 {
			logLines = logLines[len(logLines)-60:]
		}
	}
	report := func(progress int, msg string) {
		job.Progress = progress
		if msg != "" {
			appendLog(msg)
		}
		job.Log, job.UpdatedAt = strings.Join(logLines, "\n"), store.Now()
		_ = r.DB.UpdateJob(job)
	}
	start := time.Now()
	appendLog(fmt.Sprintf("started (attempt %d/%d)", job.Attempts, job.MaxAttempts))
	err = r.safeRun(ctx, h, job, report)
	close(hbStop)
	elapsed := time.Since(start).Round(time.Millisecond)
	var tr *Transient
	switch {
	case err == nil:
		job.Status, job.Progress, job.Error, job.LeaseUntil = store.JobSucceeded, 100, "", ""
		appendLog("succeeded in " + elapsed.String())
	case errors.As(err, &tr) && job.Attempts < job.MaxAttempts:
		delay := time.Duration(math.Pow(2, float64(job.Attempts))) * 5 * time.Second
		job.Status, job.Error, job.LeaseUntil = store.JobRetryable, err.Error(), ""
		job.RunAt = store.FormatTime(time.Now().UTC().Add(delay))
		appendLog(fmt.Sprintf("transient failure, retry in %s: %v", delay, err))
	default:
		job.Status, job.Error, job.LeaseUntil = store.JobFailed, err.Error(), ""
		appendLog("failed after " + elapsed.String() + ": " + err.Error())
		r.Log.Error("job failed", "type", job.Type, "id", job.ID, "err", err)
	}
	job.Log, job.UpdatedAt = strings.Join(logLines, "\n"), store.Now()
	_ = r.DB.UpdateJob(job)
	return true
}

func (r *Runner) safeRun(ctx context.Context, h Handler, job store.Job, report func(int, string)) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()
	return h(ctx, job, report)
}

// Payload decodes a job payload.
func Payload[T any](job store.Job) (T, error) {
	var v T
	err := json.Unmarshal([]byte(job.Payload), &v)
	return v, err
}

// Drain runs jobs until the queue is empty or the deadline passes (tests, verify-demo).
func (r *Runner) Drain(ctx context.Context, deadline time.Duration) int {
	end := time.Now().Add(deadline)
	n := 0
	idle := 0
	for time.Now().Before(end) {
		if r.RunOne(ctx) {
			n++
			idle = 0
			continue
		}
		if r.DB.PendingJobCount() == 0 {
			return n
		}
		idle++
		time.Sleep(50 * time.Millisecond)
		if idle > 200 { // delayed jobs only; wait for their run_at
			time.Sleep(200 * time.Millisecond)
		}
	}
	return n
}
