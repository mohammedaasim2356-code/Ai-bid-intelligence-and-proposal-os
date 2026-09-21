package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"bidos/internal/store"
)

// Sensitivity levels of task input.
const (
	SensSynthetic    = "synthetic"
	SensInternal     = "internal"
	SensConfidential = "confidential"
)

// ProviderConfig is one entry of ai.providers.json (after ${ENV} substitution).
type ProviderConfig struct {
	ID                string  `json:"-"`
	Type              string  `json:"type"` // openai_compatible | demo
	BaseURL           string  `json:"baseUrl"`
	APIKeyEnv         string  `json:"apiKeyEnv"`
	APIKey            string  `json:"-"`
	Model             string  `json:"model"`
	Mode              string  `json:"mode"` // local | free_tier | paid | demo
	RPM               float64 `json:"rpm"`
	TokensPerDay      float64 `json:"tokensPerDay"`
	AllowInternal     bool    `json:"allowInternal"`
	AllowConfidential bool    `json:"allowConfidential"`
	JSONMode          *bool   `json:"jsonMode"`
	TimeoutSeconds    int     `json:"timeoutSeconds"`
	Notes             string  `json:"notes"`
}

// Config is the router configuration.
type Config struct {
	Order         []string                   `json:"order"`
	Providers     map[string]*ProviderConfig `json:"providers"`
	TaskOverrides map[string][]string        `json:"taskOverrides"`
	Source        string                     `json:"-"`
}

// Task is one unit of AI work.
type Task struct {
	Kind        string
	PromptID    string
	Input       any
	Sensitivity string
	MaxTokens   int
	OrgID       string
	Validate    func(raw json.RawMessage) (any, error)
	Demo        func() (any, error) // deterministic floor for this task
}

// Result is the router outcome.
type Result struct {
	Output     any
	Raw        json.RawMessage
	ProviderID string
	Model      string
	Mode       string
	Cached     bool
	Attempts   int
	LatencyMs  int
	Fallbacks  int
	Repaired   bool
}

// Recorder persists cache entries and provider-call logs (implemented by *store.DB).
type Recorder interface {
	CacheGet(key string) (string, bool)
	CachePut(key, providerID, model, output string) error
	LogProviderCall(c store.ProviderCall) error
}

// Caller performs a remote chat completion (implemented by OpenAICompatible).
type Caller interface {
	Complete(ctx context.Context, p *ProviderConfig, system, user string, maxTokens int) (content string, inTok, outTok int, err error)
}

// Router implements the provider chain.
type Router struct {
	cfg      Config
	rec      Recorder
	caller   Caller
	log      *slog.Logger
	mu       sync.Mutex
	buckets  map[string]*bucket
	status   map[string]*ProviderStatus
	cacheOff bool
}

type bucket struct {
	minuteStart time.Time
	minuteCount float64
	dayStart    time.Time
	dayTokens   float64
}

// ProviderStatus is the live status shown on Settings → AI Providers.
type ProviderStatus struct {
	ID, Type, Model, Mode, BaseURL   string
	Configured                       bool
	State                            string // Not configured | Available | Connected | Rate-limited | Error | Demo simulation
	LastError                        string
	LastOK, LastCall                 string
	Calls, Fallbacks, Repairs        int
	AllowInternal, AllowConfidential bool
}

var reEnv = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)

func expandEnv(s string) string {
	return reEnv.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[2 : len(m)-1])
	})
}

// LoadConfig reads ai.providers.json when present and merges env-based providers:
//   - LOCAL_MODEL (+ LOCAL_BASE_URL, default http://localhost:11434/v1) → "local"
//   - AI_BASE_URL + AI_MODEL (+ AI_API_KEY, AI_MODE) → "env"
//
// "demo" is always appended as the floor.
func LoadConfig(path string) Config {
	cfg := Config{Providers: map[string]*ProviderConfig{}, TaskOverrides: map[string][]string{}}
	if b, err := os.ReadFile(path); err == nil {
		var raw struct {
			Order         []string                   `json:"order"`
			Providers     map[string]json.RawMessage `json:"providers"`
			TaskOverrides map[string][]string        `json:"taskOverrides"`
		}
		if err := json.Unmarshal(b, &raw); err == nil {
			cfg.Source = path
			cfg.Order = raw.Order
			cfg.TaskOverrides = raw.TaskOverrides
			for id, pr := range raw.Providers {
				var pc ProviderConfig
				// substitute ${ENV} in string values before decoding numbers
				txt := expandEnv(string(pr))
				txt = fixNumericStrings(txt)
				if err := json.Unmarshal([]byte(txt), &pc); err != nil {
					slog.Warn("ai provider config invalid", "provider", id, "err", err)
					continue
				}
				pc.ID = id
				cfg.Providers[id] = &pc
			}
		} else {
			slog.Warn("ai.providers.json unreadable; using env/demo", "err", err)
		}
	}
	if _, ok := cfg.Providers["local"]; !ok {
		if model := os.Getenv("LOCAL_MODEL"); model != "" {
			base := os.Getenv("LOCAL_BASE_URL")
			if base == "" {
				base = "http://localhost:11434/v1"
			}
			cfg.Providers["local"] = &ProviderConfig{ID: "local", Type: "openai_compatible", BaseURL: base, Model: model, Mode: "local", AllowInternal: true, AllowConfidential: true, RPM: 30}
			cfg.Order = append(cfg.Order, "local")
		}
	}
	if base, model := os.Getenv("AI_BASE_URL"), os.Getenv("AI_MODEL"); base != "" && model != "" {
		mode := os.Getenv("AI_MODE")
		if mode == "" {
			mode = "free_tier"
		}
		rpm, _ := strconv.ParseFloat(os.Getenv("AI_RPM"), 64)
		tpd, _ := strconv.ParseFloat(os.Getenv("AI_TOKENS_PER_DAY"), 64)
		cfg.Providers["env"] = &ProviderConfig{ID: "env", Type: "openai_compatible", BaseURL: base, Model: model, Mode: mode, APIKeyEnv: "AI_API_KEY", RPM: rpm, TokensPerDay: tpd, AllowInternal: os.Getenv("AI_ALLOW_INTERNAL") == "true", AllowConfidential: os.Getenv("AI_ALLOW_CONFIDENTIAL") == "true"}
		cfg.Order = append(cfg.Order, "env")
	}
	for id, p := range cfg.Providers {
		p.ID = id
		if p.APIKeyEnv != "" {
			p.APIKey = os.Getenv(p.APIKeyEnv)
		}
		if p.Mode == "" {
			if strings.Contains(p.BaseURL, "localhost") || strings.Contains(p.BaseURL, "127.0.0.1") {
				p.Mode = "local"
			} else {
				p.Mode = "free_tier"
			}
		}
		if p.Type == "" {
			p.Type = "openai_compatible"
		}
	}
	cfg.Providers["demo"] = &ProviderConfig{ID: "demo", Type: "demo", Mode: "demo", Model: "deterministic-templates", AllowInternal: true, AllowConfidential: true}
	if len(cfg.Order) == 0 {
		cfg.Order = []string{}
	}
	// keep only known ids, then ensure demo last
	var order []string
	for _, id := range cfg.Order {
		if _, ok := cfg.Providers[id]; ok && id != "demo" && !contains(order, id) {
			order = append(order, id)
		}
	}
	cfg.Order = append(order, "demo")
	return cfg
}

var reNumStr = regexp.MustCompile(`"(rpm|tokensPerDay|timeoutSeconds)"\s*:\s*"(\d+(?:\.\d+)?)?"`)

func fixNumericStrings(s string) string {
	return reNumStr.ReplaceAllStringFunc(s, func(m string) string {
		sub := reNumStr.FindStringSubmatch(m)
		if sub[2] == "" {
			return `"` + sub[1] + `": 0`
		}
		return `"` + sub[1] + `": ` + sub[2]
	})
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

// NewRouter builds a router. caller may be nil in tests (remote providers then error).
func NewRouter(cfg Config, rec Recorder, caller Caller, log *slog.Logger) *Router {
	if log == nil {
		log = slog.Default()
	}
	r := &Router{cfg: cfg, rec: rec, caller: caller, log: log, buckets: map[string]*bucket{}, status: map[string]*ProviderStatus{}}
	for id, p := range cfg.Providers {
		st := &ProviderStatus{ID: id, Type: p.Type, Model: p.Model, Mode: p.Mode, BaseURL: p.BaseURL, AllowInternal: p.AllowInternal, AllowConfidential: p.AllowConfidential}
		st.Configured = p.Type == "demo" || (p.BaseURL != "" && p.Model != "" && (p.APIKeyEnv == "" || p.APIKey != ""))
		switch {
		case p.Type == "demo":
			st.State = "Demo simulation"
		case !st.Configured:
			st.State = "Not configured"
		default:
			st.State = "Available"
		}
		r.status[id] = st
	}
	return r
}

// Config returns the loaded configuration.
func (r *Router) Config() Config { return r.cfg }

// Statuses lists providers in routing order.
func (r *Router) Statuses() []ProviderStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ProviderStatus, 0, len(r.status))
	for _, id := range r.cfg.Order {
		if st, ok := r.status[id]; ok {
			out = append(out, *st)
		}
	}
	return out
}

// LiveAvailable reports whether any non-demo provider is configured.
func (r *Router) LiveAvailable() bool {
	for _, id := range r.cfg.Order {
		if id != "demo" && r.status[id] != nil && r.status[id].Configured {
			return true
		}
	}
	return false
}

// DisableCache turns caching off (evals that want fresh calls).
func (r *Router) DisableCache(off bool) { r.cacheOff = off }

// CacheKey is sha256(promptId + model + canonical JSON input).
func CacheKey(promptID, model string, input any) string {
	b, _ := json.Marshal(input) // encoding/json sorts map keys; struct fields are stable
	h := sha256.Sum256([]byte(promptID + "|" + model + "|" + string(b)))
	return hex.EncodeToString(h[:])
}

func (r *Router) providersFor(t Task) []*ProviderConfig {
	order := r.cfg.Order
	if o, ok := r.cfg.TaskOverrides[t.Kind]; ok && len(o) > 0 {
		order = append([]string{}, o...)
		if !contains(order, "demo") {
			order = append(order, "demo")
		}
	}
	var out []*ProviderConfig
	for _, id := range order {
		p := r.cfg.Providers[id]
		if p == nil {
			continue
		}
		switch t.Sensitivity {
		case SensConfidential:
			if !p.AllowConfidential {
				continue
			}
		case SensInternal:
			if !p.AllowInternal {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

func (r *Router) allow(p *ProviderConfig, estTokens float64) bool {
	if p.RPM <= 0 && p.TokensPerDay <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.buckets[p.ID]
	now := time.Now()
	if b == nil {
		b = &bucket{minuteStart: now, dayStart: now}
		r.buckets[p.ID] = b
	}
	if now.Sub(b.minuteStart) >= time.Minute {
		b.minuteStart, b.minuteCount = now, 0
	}
	if now.Sub(b.dayStart) >= 24*time.Hour {
		b.dayStart, b.dayTokens = now, 0
	}
	if p.RPM > 0 && b.minuteCount+1 > p.RPM {
		return false
	}
	if p.TokensPerDay > 0 && b.dayTokens+estTokens > p.TokensPerDay {
		return false
	}
	b.minuteCount++
	b.dayTokens += estTokens
	return true
}

// Run executes the routing policy: sensitivity gate → cache → budget → call + validate
// (+ one repair) → backoff on transient errors → next provider → demo floor.
func (r *Router) Run(ctx context.Context, t Task) (Result, error) {
	if t.Sensitivity == "" {
		t.Sensitivity = SensInternal
	}
	if t.MaxTokens <= 0 {
		t.MaxTokens = 1200
	}
	prompt, ok := PromptByID(t.PromptID)
	if !ok {
		return Result{}, fmt.Errorf("unknown prompt %q", t.PromptID)
	}
	inputJSON, _ := json.MarshalIndent(t.Input, "", "  ")
	userMsg := "Input schema: " + prompt.InputSchema + "\nOutput schema (return exactly this JSON shape): " + prompt.OutputSchema + "\n<<<DATA\n" + string(inputJSON) + "\n>>>"
	fallbacks := 0
	var lastErr error
	for _, p := range r.providersFor(t) {
		if p.Type == "demo" {
			start := time.Now()
			out, err := t.Demo()
			if err != nil {
				return Result{}, err
			}
			raw, _ := json.Marshal(out)
			res := Result{Output: out, Raw: raw, ProviderID: "demo", Model: p.Model, Mode: "demo", Attempts: 1, LatencyMs: int(time.Since(start).Milliseconds()), Fallbacks: fallbacks}
			r.record(t, res, "ok", "", fallbacks > 0, false)
			return res, nil
		}
		st := r.status[p.ID]
		if st == nil || !st.Configured {
			continue
		}
		key := CacheKey(t.PromptID, p.Model, t.Input)
		if !r.cacheOff && r.rec != nil {
			if cached, ok := r.rec.CacheGet(key); ok {
				if out, err := t.Validate(json.RawMessage(cached)); err == nil {
					res := Result{Output: out, Raw: json.RawMessage(cached), ProviderID: p.ID, Model: p.Model, Mode: p.Mode, Cached: true, Attempts: 0, Fallbacks: fallbacks}
					r.record(t, res, "ok", "", fallbacks > 0, false)
					return res, nil
				}
			}
		}
		est := float64(len(userMsg)/4 + t.MaxTokens)
		if !r.allow(p, est) {
			r.setState(p.ID, "Rate-limited", "local budget exhausted (rpm/tokensPerDay)")
			r.record(t, Result{ProviderID: p.ID, Model: p.Model, Mode: p.Mode}, "skipped", "budget", false, false)
			fallbacks++
			continue
		}
		if r.caller == nil {
			lastErr = errors.New("no remote caller configured")
			fallbacks++
			continue
		}
		res, outcome, errClass, err := r.callWithRepair(ctx, p, prompt, userMsg, t)
		res.Fallbacks = fallbacks
		r.record(t, res, outcome, errClass, fallbacks > 0, res.Repaired)
		if err == nil {
			r.setState(p.ID, "Connected", "")
			if !r.cacheOff && r.rec != nil {
				_ = r.rec.CachePut(key, p.ID, p.Model, string(res.Raw))
			}
			return res, nil
		}
		lastErr = err
		if errClass == "rate_limited" {
			r.setState(p.ID, "Rate-limited", err.Error())
		} else {
			r.setState(p.ID, "Error", err.Error())
		}
		r.log.Warn("ai provider failed; falling back", "provider", p.ID, "kind", t.Kind, "err", err)
		fallbacks++
	}
	if lastErr == nil {
		lastErr = errors.New("no provider available")
	}
	return Result{}, lastErr
}

// callWithRepair performs the call, parses + validates, retries once with the validation
// error on schema failure, and backs off once on transient errors.
func (r *Router) callWithRepair(ctx context.Context, p *ProviderConfig, prompt Prompt, userMsg string, t Task) (Result, string, string, error) {
	res := Result{ProviderID: p.ID, Model: p.Model, Mode: p.Mode}
	start := time.Now()
	var lastErr error
	transientRetried := false
	for attempt := 1; attempt <= 3; attempt++ {
		res.Attempts = attempt
		cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutFor(p))*time.Second)
		content, inTok, outTok, err := r.caller.Complete(cctx, p, prompt.System, userMsg, t.MaxTokens)
		cancel()
		if err != nil {
			class := classifyErr(err)
			lastErr = err
			if (class == "rate_limited" || class == "server_error" || class == "timeout") && !transientRetried {
				transientRetried = true
				time.Sleep(backoff(class))
				continue
			}
			res.LatencyMs = int(time.Since(start).Milliseconds())
			return res, "error", class, err
		}
		raw := extractJSON(content)
		out, verr := t.Validate(raw)
		if verr == nil {
			res.Output, res.Raw = out, raw
			res.LatencyMs = int(time.Since(start).Milliseconds())
			_ = inTok
			_ = outTok
			return res, "ok", "", nil
		}
		lastErr = fmt.Errorf("schema validation failed: %w", verr)
		if res.Repaired {
			res.LatencyMs = int(time.Since(start).Milliseconds())
			return res, "error", "schema", lastErr
		}
		res.Repaired = true
		userMsg += "\n\nYour previous output failed validation: " + verr.Error() + ". Return only corrected JSON that matches the output schema exactly."
	}
	res.LatencyMs = int(time.Since(start).Milliseconds())
	return res, "error", classifyErr(lastErr), lastErr
}

func timeoutFor(p *ProviderConfig) int {
	if p.TimeoutSeconds > 0 {
		return p.TimeoutSeconds
	}
	if p.Mode == "local" {
		return 180
	}
	return 90
}

func backoff(class string) time.Duration {
	if class == "rate_limited" {
		return 3 * time.Second
	}
	return 1500 * time.Millisecond
}

// HTTPError carries a status code from a provider call.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider returned HTTP %d: %s", e.Status, truncate(e.Body, 200))
}

func classifyErr(err error) string {
	var he *HTTPError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &he):
		switch {
		case he.Status == 429:
			return "rate_limited"
		case he.Status >= 500:
			return "server_error"
		case he.Status == 401 || he.Status == 403:
			return "auth"
		default:
			return "bad_request"
		}
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline"):
		return "timeout"
	case strings.Contains(err.Error(), "schema"):
		return "schema"
	}
	return "network"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// extractJSON pulls the first JSON object out of a model reply (tolerates ```json fences).
func extractJSON(content string) json.RawMessage {
	s := strings.TrimSpace(content)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return json.RawMessage(s)
	}
	return json.RawMessage(s[start : end+1])
}

func (r *Router) setState(id, state, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.status[id]
	if st == nil {
		return
	}
	st.State = state
	st.LastError = errMsg
	st.LastCall = time.Now().UTC().Format(time.RFC3339)
	if state == "Connected" {
		st.LastOK = st.LastCall
	}
}

func (r *Router) record(t Task, res Result, outcome, errClass string, fallback, repaired bool) {
	r.mu.Lock()
	if st := r.status[res.ProviderID]; st != nil {
		st.Calls++
		if fallback {
			st.Fallbacks++
		}
		if repaired {
			st.Repairs++
		}
	}
	r.mu.Unlock()
	if r.rec == nil {
		return
	}
	_ = r.rec.LogProviderCall(store.ProviderCall{
		OrgID: t.OrgID, TaskKind: t.Kind, PromptID: t.PromptID, ProviderID: res.ProviderID, Model: res.Model, Mode: res.Mode,
		Cached: res.Cached, Attempts: res.Attempts, LatencyMs: res.LatencyMs, Outcome: outcome, ErrorClass: errClass, Fallback: fallback, Repaired: repaired,
	})
}

// OrderedProviderIDs is a helper for templates.
func (c Config) OrderedProviderIDs() []string {
	ids := append([]string{}, c.Order...)
	sort.Strings(ids)
	return ids
}
