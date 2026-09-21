package ai

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"bidos/internal/store"
)

type fakeCaller struct {
	replies []string
	errs    []error
	calls   int
}

func (f *fakeCaller) Complete(_ context.Context, _ *ProviderConfig, _ string, user string, _ int) (string, int, int, error) {
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return "", 0, 0, f.errs[i]
	}
	if i < len(f.replies) {
		return f.replies[i], 10, 10, nil
	}
	return "", 0, 0, errors.New("no more replies")
}

func testConfig() Config {
	os.Setenv("TEST_KEY", "k")
	cfg := Config{Order: []string{"free"}, Providers: map[string]*ProviderConfig{
		"free": {ID: "free", Type: "openai_compatible", BaseURL: "http://x", Model: "m", Mode: "free_tier", APIKeyEnv: "TEST_KEY", APIKey: "k", AllowInternal: true},
		"demo": {ID: "demo", Type: "demo", Mode: "demo", Model: "deterministic-templates", AllowInternal: true, AllowConfidential: true},
	}, TaskOverrides: map[string][]string{}}
	cfg.Order = []string{"free", "demo"}
	return cfg
}

func TestRouterRepairAndFallback(t *testing.T) {
	db, _ := store.Open(":memory:")
	defer db.Close()
	in := DraftInput{Company: "Northstar", Requirement: "Describe encryption.", Evidence: []EvidenceItem{{Marker: 1, Text: "All data is encrypted with AES-256."}}}
	// first reply invalid (no markers) → repair → valid
	fc := &fakeCaller{replies: []string{`{"answer":"Data is encrypted with AES-256."}`, "```json\n{\"answer\":\"Data is encrypted with AES-256 [c1].\"}\n```"}}
	r := NewRouter(testConfig(), db, fc, nil)
	out, res, err := r.Draft(context.Background(), "org", SensInternal, in)
	if err != nil || !res.Repaired || res.ProviderID != "free" || !strings.Contains(out.Answer, "[c1]") {
		t.Fatalf("repair path failed: %v %+v %+v", err, res, out)
	}
	// cached second call: no provider call
	calls := fc.calls
	_, res2, _ := r.Draft(context.Background(), "org", SensInternal, in)
	if !res2.Cached || fc.calls != calls {
		t.Fatalf("cache miss: %+v", res2)
	}
	// provider down → demo floor
	fc2 := &fakeCaller{errs: []error{&HTTPError{Status: 500}, &HTTPError{Status: 500}}}
	r2 := NewRouter(testConfig(), db, fc2, nil)
	in2 := DraftInput{Company: "Northstar", Requirement: "Provide ISO 27001 evidence."}
	out2, res3, err := r2.Draft(context.Background(), "org", SensInternal, in2)
	if err != nil || res3.Mode != "demo" || !out2.NeedsEvidence || res3.Fallbacks != 1 {
		t.Fatalf("fallback failed: %v %+v %+v", err, res3, out2)
	}
	// sensitivity gate: confidential never reaches the free tier
	fc3 := &fakeCaller{replies: []string{`{"answer":"x [c1]"}`}}
	r3 := NewRouter(testConfig(), db, fc3, nil)
	_, res4, _ := r3.Draft(context.Background(), "org", SensConfidential, in)
	if res4.ProviderID != "demo" || fc3.calls != 0 {
		t.Fatalf("sensitivity gate leaked: %+v", res4)
	}
	stats, _ := db.ProviderStats("2000-01-01")
	if len(stats) == 0 {
		t.Fatal("provider calls not logged")
	}
}

func TestLoadConfigEnv(t *testing.T) {
	os.Setenv("AI_BASE_URL", "https://api.example/v1")
	os.Setenv("AI_MODEL", "test-model")
	os.Setenv("AI_API_KEY", "secret")
	defer os.Unsetenv("AI_BASE_URL")
	defer os.Unsetenv("AI_MODEL")
	defer os.Unsetenv("AI_API_KEY")
	cfg := LoadConfig("does-not-exist.json")
	if cfg.Providers["env"] == nil || cfg.Providers["env"].APIKey != "secret" || cfg.Order[len(cfg.Order)-1] != "demo" {
		t.Fatalf("env provider not loaded: %+v", cfg)
	}
	r := NewRouter(cfg, nil, nil, nil)
	if !r.LiveAvailable() {
		t.Fatal("live provider should be available")
	}
	for _, st := range r.Statuses() {
		if st.ID == "env" && st.State != "Available" {
			t.Fatalf("state: %+v", st)
		}
	}
}

func TestDemoDraftVerifies(t *testing.T) {
	ev := "Single sign-on is provided through SAML 2.0 with SCIM provisioning. Multi-factor authentication (MFA) is enforced for all administrative users."
	out := DemoDraft(DraftInput{Company: "Northstar", Requirement: "Provide SAML 2.0 SSO and MFA.", Evidence: []EvidenceItem{{Marker: 1, Text: ev}}})
	v := Verify(out.Answer, map[int]string{1: ev}, 0.5, []string{"Northstar"})
	if v.Unsupported != 0 || v.Factual == 0 {
		t.Fatalf("demo draft should verify: %+v\n%s", v, out.Answer)
	}
}
