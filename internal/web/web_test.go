package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"bidos/internal/ai"
	"bidos/internal/app"
	"bidos/internal/config"
	"bidos/internal/demo"
	"bidos/internal/jobs"
	"bidos/internal/pipeline"
	"bidos/internal/retrieval"
	"bidos/internal/store"
)

func testServer(t *testing.T) (*Server, *app.App, *jobs.Runner) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{StorageDir: dir, DatabasePath: filepath.Join(dir, "t.db"), MaxUploadBytes: 25 << 20, VerticalPack: "saas-it", EnableEmbeddings: true, DemoMode: true, SessionSecret: "test-secret", BaseURL: "http://test", Workers: 2, CronSecret: "cron"}
	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	router := ai.NewRouter(ai.LoadConfig(filepath.Join(dir, "none.json")), db, ai.OpenAICompatible{}, nil)
	a := app.New(cfg, db, router, retrieval.HashEmbedder{}, nil)
	runner := jobs.New(db, nil, 2)
	eng := pipeline.New(a, runner)
	a.Hooks.StartPipeline = eng.Start
	return New(cfg, a, eng, runner, nil), a, runner
}

var reCSRF = regexp.MustCompile(`name="_csrf" value="([0-9a-f]+)"`)

type client struct {
	t   *testing.T
	s   *Server
	sid string
}

func (c *client) do(method, path string, form url.Values) (*httptest.ResponseRecorder, string) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.sid != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.sid})
	}
	rr := httptest.NewRecorder()
	c.s.ServeHTTP(rr, req)
	return rr, rr.Body.String()
}

func (c *client) get(path string) string {
	c.t.Helper()
	rr, body := c.do(http.MethodGet, path, nil)
	if rr.Code != 200 {
		c.t.Fatalf("GET %s → %d\n%s", path, rr.Code, firstLines(body, 20))
	}
	if strings.Contains(body, "template error:") {
		c.t.Fatalf("GET %s rendered a template error:\n%s", path, firstLines(body, 20))
	}
	return body
}

func (c *client) post(path string, kv ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	form := url.Values{}
	if c.sid != "" {
		form.Set("_csrf", c.s.csrfToken(c.sid))
	}
	for i := 0; i+1 < len(kv); i += 2 {
		form.Set(kv[i], kv[i+1])
	}
	rr, body := c.do(http.MethodPost, path, form)
	if rr.Code != 303 && rr.Code != 200 {
		c.t.Fatalf("POST %s → %d\n%s", path, rr.Code, firstLines(body, 10))
	}
	return rr
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func TestAllPagesRender(t *testing.T) {
	s, a, runner := testServer(t)
	ctx := context.Background()
	c := &client{t: t, s: s}

	// public pages
	c.get("/")
	c.get("/login")
	if rr, _ := c.do(http.MethodGet, "/dashboard", nil); rr.Code != 303 {
		t.Fatalf("unauthenticated dashboard should redirect, got %d", rr.Code)
	}
	if rr, _ := c.do(http.MethodPost, "/api/cron/tick", nil); rr.Code != 403 {
		t.Fatalf("cron without secret should be 403, got %d", rr.Code)
	}

	// launch demo: seeds + logs in as PM + starts the pipeline
	rr := c.post("/demo/launch", "run", "1")
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == sessionCookie {
			c.sid = ck.Value
		}
	}
	if c.sid == "" {
		t.Fatal("demo launch did not set a session cookie")
	}
	runner.Drain(ctx, 4*time.Minute)
	org, _ := a.DB.GetOrg(demo.DemoOrgID)
	bidID := org.ID + "-bid-rfp"
	run, _ := a.DB.LatestRun(bidID)
	if run.Status != store.JobWaiting {
		t.Fatalf("pipeline should wait at a gate, got %s at %s (%s)", run.Status, run.CurrentStage, run.Error)
	}

	// every authenticated page
	reqs, _ := a.DB.ListRequirements(bidID)
	docs, _ := a.DB.ListBidDocuments(bidID)
	kdocs, _ := a.DB.ListKnowledgeDocs(org.ID)
	tasks, _ := a.DB.ListTasks(bidID)
	lib, _ := a.DB.ListLibrary(org.ID)
	pages := []string{"/dashboard", "/bids", "/bids?status=active", "/bids/new", "/bids/" + bidID, "/bids/" + bidID + "/requirements", "/bids/" + bidID + "/requirements?status=needs_evidence&flag=mandatory",
		"/bids/" + bidID + "/tasks", "/bids/" + bidID + "/proposal", "/bids/" + bidID + "/qa", "/bids/" + bidID + "/export", "/bids/" + bidID + "/addenda", "/bids/" + bidID + "/analytics",
		"/knowledge", "/knowledge?state=approved", "/library", "/tasks", "/tasks?scope=all", "/analytics", "/settings", "/integrations", "/quality", "/notifications", "/health", "/api/runs/" + run.ID}
	if len(reqs) > 0 {
		pages = append(pages, "/bids/"+bidID+"/requirements?req="+reqs[5].ID)
	}
	if len(docs) > 0 {
		pages = append(pages, "/bids/"+bidID+"/documents/"+docs[0].ID+"/preview", "/documents/"+docs[0].ID+"/download")
	}
	if len(kdocs) > 0 {
		pages = append(pages, "/knowledge/"+kdocs[0].ID)
	}
	if len(tasks) > 0 {
		pages = append(pages, "/tasks/"+tasks[0].ID)
	}
	if len(lib) > 0 {
		pages = append(pages, "/library/"+lib[0].ID)
	}
	for _, p := range pages {
		c.get(p)
	}
	// trap requirement shows the honest label and the SME question
	var trap store.Requirement
	for _, r := range reqs {
		if r.IsTrap {
			trap = r
			break
		}
	}
	body := c.get("/bids/" + bidID + "/requirements?req=" + trap.ID)
	if !strings.Contains(body, "Needs evidence") || !strings.Contains(body, "Generated SME question") {
		t.Fatal("trap requirement page should show Needs evidence and the SME question")
	}

	// cross-org access by URL is a 404
	if rr, _ := c.do(http.MethodGet, "/bids/other-org-bid", nil); rr.Code != 404 {
		t.Fatalf("cross-org bid should be 404, got %d", rr.Code)
	}
	// CSRF is enforced
	if rr, _ := c.do(http.MethodPost, "/bids/"+bidID+"/qa/run", url.Values{}); rr.Code != 403 {
		t.Fatalf("POST without CSRF should be 403, got %d", rr.Code)
	}

	// actions: run QA, regenerate one answer, respond to a task as the SME, approve as reviewer, export CSV
	c.post("/bids/" + bidID + "/qa/run")
	c.post("/requirements/" + reqs[5].ID + "/generate")
	c.post("/bids/"+bidID+"/export", "format", "csv")
	c.post("/analytics/roi", "loaded_hourly_rate", "120")
	c.post("/settings/values", "verify.threshold", "0.5")
	c.post("/settings/ai/test", "provider", "demo")
	exports, _ := a.DB.ListExports(bidID)
	if len(exports) == 0 {
		t.Fatal("csv export not recorded")
	}
	c.get("/exports/" + exports[0].ID + "/download")

	// RBAC: a viewer cannot run QA
	viewer, _ := a.DB.UsersByRole(org.ID, store.RoleViewer)
	if len(viewer) > 0 {
		vc := &client{t: t, s: s}
		vc.sid, _ = a.Login(org.ID, viewer[0].ID)
		form := url.Values{"_csrf": {s.csrfToken(vc.sid)}}
		if rr, _ := vc.do(http.MethodPost, "/bids/"+bidID+"/qa/run", form); rr.Code != 403 {
			t.Fatalf("viewer running QA should be 403, got %d", rr.Code)
		}
	}
	// SME loop through the UI
	sme, _ := a.DB.UserByEmail(org.ID, "meiling.chen@northstar.example")
	sc := &client{t: t, s: s}
	sc.sid, _ = a.Login(org.ID, sme.ID)
	var target store.Task
	for _, tk := range tasks {
		if tk.AssigneeID == sme.ID && tk.Status == "open" {
			target = tk
			break
		}
	}
	if target.ID != "" {
		sc.get("/tasks/" + target.ID)
		sc.post("/tasks/"+target.ID+"/respond", "action", "answer", "response", "Northstar does not hold this certification. The SOC 2 Type II report covers equivalent controls; certification is planned for 2027.")
		ans, _ := a.DB.LatestAnswer(target.RequirementID)
		if ans.Status != store.StatusInReview {
			t.Fatalf("SME response should re-draft into review, got %s (%s)", ans.Status, ans.ConfidenceLabel)
		}
		c.get("/bids/" + bidID + "/requirements?req=" + target.RequirementID)
	}
	// cron tick with secret
	req := httptest.NewRequest(http.MethodPost, "/api/cron/tick", nil)
	req.Header.Set("X-Cron-Secret", "cron")
	rr2 := httptest.NewRecorder()
	s.ServeHTTP(rr2, req)
	if rr2.Code != 200 {
		t.Fatalf("cron tick with secret → %d", rr2.Code)
	}
	// logout
	c.post("/logout")
}
