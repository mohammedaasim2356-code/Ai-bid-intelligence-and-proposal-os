// Package app holds the domain services: bids, knowledge, extraction, grounded answers,
// SME workflow, answer library, QA, exports, addenda, analytics and schedules.
// HTTP handlers and the pipeline call into this package; it never imports them.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"bidos/internal/ai"
	"bidos/internal/config"
	"bidos/internal/retrieval"
	"bidos/internal/store"
	"bidos/internal/verticals"
)

// Hooks are set at wiring time by packages the app must not import.
type Hooks struct {
	// StartPipeline starts (or resumes) a pipeline run; only may restrict AUTO_DRAFT to ids.
	StartPipeline func(bidID, mode string, autoGates bool, only []string) (string, error)
	// External notification adapter (Slack etc.). Optional.
	Notify func(orgID, title, body, link string)
	// Error reporter adapter. Optional.
	ReportError func(err error, where string)
	// TaskCreated mirrors new SME tasks to an external tracker. Optional.
	TaskCreated func(t store.Task)
}

// App is the service container.
type App struct {
	DB       *store.DB
	Cfg      config.Config
	AI       *ai.Router
	Embedder retrieval.Embedder
	Files    *Files
	Log      *slog.Logger
	Hooks    Hooks
	Version  string
	// NowFunc overrides the clock (deterministic seeds/tests).
	NowFunc func() string

	mu    sync.Mutex
	packs map[string]*verticals.Pack
}

// UserError is a message safe to show to end users.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

func userErr(format string, args ...any) error { return &UserError{Msg: fmt.Sprintf(format, args...)} }

// IsUserError reports whether err carries a user-facing message.
func IsUserError(err error) bool {
	var ue *UserError
	return errors.As(err, &ue)
}

func New(cfg config.Config, db *store.DB, router *ai.Router, emb retrieval.Embedder, log *slog.Logger) *App {
	if log == nil {
		log = slog.Default()
	}
	return &App{DB: db, Cfg: cfg, AI: router, Embedder: emb, Files: &Files{Root: cfg.StorageDir}, Log: log, packs: map[string]*verticals.Pack{}, Version: "2.0.0"}
}

// PackByID loads (and caches) a vertical pack.
func (a *App) PackByID(id string) *verticals.Pack {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.packs[id]; ok {
		return p
	}
	p, err := verticals.Load(id)
	if err != nil {
		a.Log.Warn("vertical pack missing; falling back to saas-it", "pack", id, "err", err)
		if id == "saas-it" {
			panic(err)
		}
		return a.PackByIDUnlocked("saas-it")
	}
	a.packs[id] = p
	return p
}

func (a *App) PackByIDUnlocked(id string) *verticals.Pack {
	if p, ok := a.packs[id]; ok {
		return p
	}
	p, err := verticals.Load(id)
	if err != nil {
		panic(err)
	}
	a.packs[id] = p
	return p
}

// Pack returns the pack for an organization.
func (a *App) Pack(orgID string) *verticals.Pack {
	org, err := a.DB.GetOrg(orgID)
	if err != nil || org.VerticalPackID == "" {
		return a.PackByID(a.Cfg.VerticalPack)
	}
	return a.PackByID(org.VerticalPackID)
}

// Now returns the canonical timestamp (overridable for deterministic seeds).
func (a *App) Now() string {
	if a.NowFunc != nil {
		return a.NowFunc()
	}
	return store.Now()
}

// Sensitivity returns the org's data sensitivity for AI routing.
func (a *App) Sensitivity(orgID string) string {
	org, err := a.DB.GetOrg(orgID)
	if err != nil || org.DataSensitivity == "" {
		return ai.SensInternal
	}
	return org.DataSensitivity
}

// Aliases lists the company names to ignore in term matching.
func (a *App) Aliases(orgID string) []string {
	p := a.Pack(orgID)
	out := append([]string{}, p.Demo.Company.NameAliases...)
	if org, err := a.DB.GetOrg(orgID); err == nil {
		out = append(out, org.Name)
	}
	return out
}

// Activity writes an audit entry.
func (a *App) Activity(orgID, userID, event, entityType, entityID string, meta map[string]any) {
	m := "{}"
	if meta != nil {
		b, _ := json.Marshal(meta)
		m = string(b)
	}
	if err := a.DB.LogActivity(store.Activity{OrgID: orgID, UserID: userID, EventType: event, EntityType: entityType, EntityID: entityID, Metadata: m}); err != nil {
		a.Log.Warn("activity log failed", "err", err)
	}
}

// Notify creates an in-app notification (and forwards to the external adapter if any).
func (a *App) Notify(orgID, userID, title, body, link string) {
	if userID != "" {
		_ = a.DB.CreateNotification(store.Notification{OrgID: orgID, UserID: userID, Title: title, Body: body, Link: link})
	}
	if a.Hooks.Notify != nil {
		a.Hooks.Notify(orgID, title, body, link)
	}
}

// NotifyRole notifies every user with the role.
func (a *App) NotifyRole(orgID, role, title, body, link string) {
	users, _ := a.DB.UsersByRole(orgID, role)
	for _, u := range users {
		a.Notify(orgID, u.ID, title, body, link)
	}
}

func (a *App) reportError(err error, where string) {
	a.Log.Error(where, "err", err)
	if a.Hooks.ReportError != nil {
		a.Hooks.ReportError(err, where)
	}
}

// Setting reads an org setting with a default.
func (a *App) Setting(orgID, key, def string) string { return a.DB.GetSetting(orgID, key, def) }

// SettingFloat reads a numeric setting.
func (a *App) SettingFloat(orgID, key string, def float64) float64 {
	var f float64
	if _, err := fmt.Sscanf(a.DB.GetSetting(orgID, key, ""), "%g", &f); err == nil {
		return f
	}
	return def
}

// Embed embeds texts with the configured embedder (nil-safe).
func (a *App) Embed(ctx context.Context, texts []string) [][]float32 {
	if a.Embedder == nil || len(texts) == 0 {
		return nil
	}
	vecs, err := a.Embedder.Embed(ctx, texts)
	if err != nil {
		a.Log.Warn("embedding failed; continuing lexical-only", "err", err)
		return nil
	}
	return vecs
}

// EmbedderID returns the active embedding model id ("" when disabled).
func (a *App) EmbedderID() string {
	if a.Embedder == nil {
		return ""
	}
	return a.Embedder.ModelID()
}

// ParseDeadline validates a deadline string ("2026-10-30" or RFC3339).
func ParseDeadline(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	t, ok := store.ParseTime(s)
	if !ok {
		return "", userErr("The deadline %q is not a valid date. Use YYYY-MM-DD.", s)
	}
	return store.FormatTime(t), nil
}

// DaysUntil returns whole days from now until a timestamp (negative when past).
func DaysUntil(ts string) int {
	t, ok := store.ParseTime(ts)
	if !ok {
		return 0
	}
	return int(time.Until(t).Hours() / 24)
}
