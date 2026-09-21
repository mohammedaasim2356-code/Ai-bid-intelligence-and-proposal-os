// Command bidos runs the AI Bid Intelligence & Proposal OS: web UI, worker, seeding,
// evals and maintenance commands in one binary.
//
//	bidos serve          web UI + in-process worker (default)
//	bidos worker         job worker only
//	bidos seed           create the synthetic demo workspace
//	bidos reset          drop and re-seed the demo workspace
//	bidos eval [--suite all] [--provider demo]
//	bidos verify-demo    run the full pipeline on an isolated copy of the demo and report
//	bidos tick           fire due schedules and drain queued jobs once (host cron)
//	bidos backup <file>  consistent SQLite copy
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"bidos/internal/ai"
	"bidos/internal/app"
	"bidos/internal/config"
	"bidos/internal/demo"
	"bidos/internal/evals"
	"bidos/internal/integrations"
	"bidos/internal/jobs"
	"bidos/internal/pipeline"
	"bidos/internal/retrieval"
	"bidos/internal/store"
	"bidos/internal/web"
)

type services struct {
	cfg    config.Config
	log    *slog.Logger
	db     *store.DB
	app    *app.App
	runner *jobs.Runner
	engine *pipeline.Engine
}

func boot() (*services, error) {
	cfg := config.Load()
	var h slog.Handler
	if cfg.LogJSON {
		h = slog.NewJSONHandler(os.Stdout, nil)
	} else {
		h = slog.NewTextHandler(os.Stdout, nil)
	}
	log := slog.New(h)
	slog.SetDefault(log)
	if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o755); err != nil {
		return nil, err
	}
	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	router := ai.NewRouter(ai.LoadConfig(cfg.AIProviderConfig), db, ai.OpenAICompatible{}, log)
	var emb retrieval.Embedder = retrieval.HashEmbedder{}
	if cfg.EmbeddingsBaseURL != "" && cfg.EmbeddingsModel != "" {
		emb = retrieval.RemoteEmbedder{BaseURL: cfg.EmbeddingsBaseURL, Model: cfg.EmbeddingsModel, APIKey: cfg.EmbeddingsAPIKey}
	}
	a := app.New(cfg, db, router, emb, log)
	runner := jobs.New(db, log, cfg.Workers)
	eng := pipeline.New(a, runner)
	a.Hooks.StartPipeline = eng.Start
	integrations.Wire(cfg, a, runner, log)
	registerJobs(a, runner)
	return &services{cfg: cfg, log: log, db: db, app: a, runner: runner, engine: eng}, nil
}

// registerJobs wires the scheduled maintenance jobs and the eval job.
func registerJobs(a *app.App, r *jobs.Runner) {
	eachOrg := func(fn func(orgID string) int) jobs.Handler {
		return func(ctx context.Context, job store.Job, report func(int, string)) error {
			orgs, err := a.DB.ListOrgs()
			if err != nil {
				return err
			}
			total := 0
			for _, o := range orgs {
				total += fn(o.ID)
			}
			report(100, fmt.Sprintf("%d actions across %d organizations", total, len(orgs)))
			return nil
		}
	}
	r.Handle("schedule.deadline_watch", eachOrg(a.DeadlineWatch))
	r.Handle("schedule.digest", eachOrg(a.DailyDigest))
	r.Handle("schedule.hygiene", eachOrg(func(id string) int { a.LibraryHygieneJob(id); return 1 }))
	r.Handle("schedule.sandbox_cleanup", func(ctx context.Context, job store.Job, report func(int, string)) error {
		report(100, fmt.Sprintf("%d expired sandboxes removed", a.SandboxCleanup()))
		return nil
	})
	r.Handle("schedule.embed", eachOrg(func(id string) int { n, _ := a.EmbedMissing(context.Background(), id); return n }))
	r.Every(time.Hour, "schedule.deadline_watch", "{}")
	r.Every(24*time.Hour, "schedule.digest", "{}")
	r.Every(24*time.Hour, "schedule.hygiene", "{}")
	r.Every(time.Hour, "schedule.sandbox_cleanup", "{}")
	r.Every(10*time.Minute, "schedule.embed", "{}")
	r.Handle("evals.run", func(ctx context.Context, job store.Job, report func(int, string)) error {
		p, _ := jobs.Payload[map[string]string](job)
		pack := p["pack"]
		if pack == "" {
			pack = a.Cfg.VerticalPack
		}
		report(5, "seeding isolated eval organization")
		rep, err := evals.Run(ctx, a, pack, strings.Split(p["suites"], ","), "evals/reports")
		if err != nil {
			return err
		}
		report(100, fmt.Sprintf("passed=%v report=%s", rep.Passed, rep.Path))
		return nil
	})
}

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	s, err := boot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "bidos:", err)
		os.Exit(1)
	}
	defer s.db.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var runErr error
	switch cmd {
	case "serve":
		runErr = serve(ctx, s)
	case "worker":
		s.runner.Start(ctx)
		<-ctx.Done()
		s.runner.Stop()
	case "seed":
		org, err := (&demo.Seeder{App: s.app}).Ensure(ctx, s.cfg.VerticalPack)
		runErr = err
		if err == nil {
			fmt.Println("seeded", org.ID, org.Name)
		}
	case "reset":
		org, err := (&demo.Seeder{App: s.app}).Reset(ctx, s.cfg.VerticalPack, "")
		runErr = err
		if err == nil {
			fmt.Println("reset", org.ID, org.Name)
		}
	case "eval":
		runErr = runEval(ctx, s, args)
	case "verify-demo":
		runErr = verifyDemo(ctx, s)
	case "tick":
		n := s.runner.Tick(ctx, 100)
		fmt.Printf("ran %d jobs, %d pending\n", n, s.db.PendingJobCount())
	case "backup":
		if len(args) == 0 {
			runErr = fmt.Errorf("usage: bidos backup <file>")
			break
		}
		abs, _ := filepath.Abs(args[0])
		runErr = s.db.Exec("VACUUM INTO ?", filepath.ToSlash(abs))
		if runErr == nil {
			fmt.Println("backup written to", abs)
		}
	case "help", "-h", "--help":
		fmt.Println("commands: serve | worker | seed | reset | eval [--suite all --provider demo] | verify-demo | tick | backup <file>")
	default:
		runErr = fmt.Errorf("unknown command %q", cmd)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "bidos:", runErr)
		os.Exit(1)
	}
}

func serve(ctx context.Context, s *services) error {
	if s.cfg.DemoMode {
		if _, err := (&demo.Seeder{App: s.app}).Ensure(ctx, s.cfg.VerticalPack); err != nil {
			s.log.Warn("demo seed", "err", err)
		}
	}
	if s.cfg.Workers > 0 {
		s.runner.Start(ctx)
		defer s.runner.Stop()
	}
	srv := web.New(s.cfg, s.app, s.engine, s.runner, s.log)
	return srv.Serve(ctx, s.cfg.Addr)
}

func runEval(ctx context.Context, s *services, args []string) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	suite := fs.String("suite", "all", "suite name or all")
	provider := fs.String("provider", "auto", "demo forces the deterministic floor")
	out := fs.String("out", "evals/reports", "report directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *provider == "demo" {
		full := s.app.AI.Config()
		demoOnly := ai.Config{Order: []string{"demo"}, Providers: map[string]*ai.ProviderConfig{"demo": full.Providers["demo"]}, TaskOverrides: map[string][]string{}}
		s.app.AI = ai.NewRouter(demoOnly, s.db, ai.OpenAICompatible{}, s.log)
	}
	rep, err := evals.Run(ctx, s.app, s.cfg.VerticalPack, strings.Split(*suite, ","), *out)
	if err != nil {
		return err
	}
	fmt.Print(evals.Markdown(rep))
	if !rep.Passed {
		return fmt.Errorf("eval targets not met")
	}
	return nil
}

// verifyDemo seeds an isolated copy of the demo, runs the pipeline with auto gates and
// prints what a demo viewer would see. It never touches the real demo organization.
func verifyDemo(ctx context.Context, s *services) error {
	seeder := &demo.Seeder{App: s.app}
	orgID := "verify-" + store.NewID()[:6]
	org, err := seeder.Seed(ctx, s.cfg.VerticalPack, orgID, "")
	if err != nil {
		return err
	}
	defer func() {
		_ = s.db.DeleteOrg(orgID)
		_ = os.RemoveAll(filepath.Join(s.app.Files.Root, app.SafeName(orgID)))
	}()
	bidID := org.ID + "-bid-rfp"
	runID, err := s.engine.Start(bidID, "demo", true, nil)
	if err != nil {
		return err
	}
	start := time.Now()
	s.runner.Drain(ctx, 5*time.Minute)
	run, _ := s.db.GetRun(runID)
	reqs, _ := s.db.ListRequirements(bidID)
	answers, _ := s.db.LatestAnswersByBid(bidID)
	counts := map[string]int{}
	traps, refused := 0, 0
	for _, r := range reqs {
		a := answers[r.ID]
		counts[a.Status]++
		if r.IsTrap {
			traps++
			if a.Status == store.StatusNeedsEvidence {
				refused++
			}
		}
	}
	qa, _ := s.app.QASummary(org.ID, bidID)
	result := map[string]any{"run": run.Status, "stage": run.CurrentStage, "gate": run.Gate, "requirements": len(reqs), "answersByStatus": counts, "traps": traps, "trapsRefused": refused, "qaBlockers": qa.Blockers, "qaPassed": qa.Passed, "qaTotal": qa.Total, "elapsed": time.Since(start).Round(time.Millisecond).String()}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
	if refused != traps || run.Status != store.JobWaiting || run.Gate != pipeline.Gate4 {
		return fmt.Errorf("verify-demo: unexpected outcome")
	}
	return nil
}
