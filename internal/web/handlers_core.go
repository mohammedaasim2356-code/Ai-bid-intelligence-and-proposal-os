package web

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"bidos/internal/demo"
	"bidos/internal/pipeline"
	"bidos/internal/store"
	"bidos/internal/verticals"
)

// ---- landing / auth ---------------------------------------------------------------------

func (s *Server) landing(w http.ResponseWriter, r *http.Request) {
	v := s.viewer(r)
	if v.OK {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	pack := s.App.PackByID(s.Cfg.VerticalPack)
	data := map[string]any{"Title": "BidOS — RFPs in. Decision-ready proposals out.", "Nav": "landing", "Pack": pack, "Company": pack.Demo.Company, "RFP": pack.Demo.RFP, "Traps": countTraps(pack), "ReqCount": len(pack.Demo.RFP.AllItems()), "Sandbox": s.Cfg.EnablePublicSandbox}
	s.render(w, r, v, "landing.html", data)
}

func countTraps(p *verticals.Pack) int {
	n := 0
	for _, it := range p.Demo.RFP.AllItems() {
		if it.Trap {
			n++
		}
	}
	return n
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	v := s.viewer(r)
	if v.OK {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	var users []store.User
	org, err := s.App.DB.GetOrg(demo.DemoOrgID)
	if err == nil && org.Mode == "demo" {
		users, _ = s.App.DB.ListUsers(org.ID)
	}
	orgs, _ := s.App.DB.ListOrgs()
	s.render(w, r, v, "login.html", map[string]any{"Title": "Sign in", "Nav": "login", "Users": users, "DemoOrg": org, "Orgs": orgs, "Next": r.URL.Query().Get("next")})
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	next := r.FormValue("next")
	if !strings.HasPrefix(next, "/") {
		next = "/dashboard"
	}
	var sid string
	var err error
	if uid := r.FormValue("user_id"); uid != "" {
		// demo picker: only for demo-mode organizations
		u, uerr := s.App.DB.GetUser(uid)
		org, oerr := s.App.DB.GetOrg(u.OrgID)
		if uerr != nil || oerr != nil || org.Mode != "demo" {
			s.flash(w, "Demo sign-in is only available for the synthetic workspace.")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		sid, err = s.App.Login(org.ID, u.ID)
	} else {
		orgID := r.FormValue("org_id")
		if orgID == "" {
			orgID = demo.DemoOrgID
		}
		sid, err = s.App.LoginPassword(orgID, strings.TrimSpace(r.FormValue("email")), r.FormValue("password"))
	}
	if err != nil {
		s.flash(w, err.Error())
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.setSession(w, r, sid)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.App.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// demoLaunch seeds (if needed), signs in as the proposal manager and optionally starts
// the full pipeline. With ENABLE_PUBLIC_SANDBOX each launch gets its own expiring org.
func (s *Server) demoLaunch(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.Cfg.DemoMode {
		s.flash(w, "Demo mode is disabled on this deployment.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	var org store.Organization
	var err error
	if s.Cfg.EnablePublicSandbox && r.FormValue("sandbox") == "1" {
		exp := store.FormatTime(time.Now().Add(time.Duration(s.Cfg.SandboxTTLHours) * time.Hour))
		org, err = s.Seeder.Seed(ctx, s.Cfg.VerticalPack, "sandbox-"+store.NewID()[:8], exp)
	} else {
		org, err = s.Seeder.Ensure(ctx, s.Cfg.VerticalPack)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pms, _ := s.App.DB.UsersByRole(org.ID, store.RoleProposalManager)
	if len(pms) == 0 {
		s.flash(w, "The demo workspace has no proposal manager user.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	sid, err := s.App.Login(org.ID, pms[0].ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setSession(w, r, sid)
	bidID := org.ID + "-bid-rfp"
	if r.FormValue("run") == "1" {
		if _, err := s.Engine.Start(bidID, "demo", true, nil); err != nil {
			s.flash(w, "Could not start the pipeline: "+err.Error())
		} else {
			s.flash(w, "Synthetic demo launched: the pipeline is running through the gates.")
		}
	}
	http.Redirect(w, r, "/bids/"+bidID, http.StatusSeeOther)
}

func (s *Server) demoReset(w http.ResponseWriter, r *http.Request, v viewer) {
	if v.Org.Mode != "demo" {
		s.flash(w, "Only demo workspaces can be reset.")
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	email := v.User.Email
	org, err := s.Seeder.Reset(r.Context(), s.App.Pack(v.Org.ID).ID, v.Org.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.App.DB.UserByEmail(org.ID, email)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	sid, _ := s.App.Login(org.ID, u.ID)
	s.setSession(w, r, sid)
	s.flash(w, "Demo workspace reset to its seeded state.")
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// ---- dashboard / notifications ------------------------------------------------------------

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, v viewer) {
	d, err := s.App.Dashboard(v.Org.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	myTasks, _ := s.App.DB.TasksForUser(v.User.ID)
	open := 0
	for _, t := range myTasks {
		if t.Status == "open" || t.Status == "in_progress" {
			open++
		}
	}
	notes, _ := s.App.DB.ListNotifications(v.User.ID, 6)
	sub := fmt.Sprintf("%d active bids · %d requirements · %d open SME tasks", d.ActiveBids, d.TotalRequirements, d.OpenTasks)
	s.render(w, r, v, "dashboard.html", map[string]any{"Title": "Overview", "Nav": "overview", "Subtitle": sub, "D": d, "MyOpenTasks": open, "Notes": notes, "Users": s.userMap(v.Org.ID)})
}

func (s *Server) userMap(orgID string) map[string]store.User {
	m, _ := s.App.DB.UserMap(orgID)
	return m
}

func (s *Server) notifications(w http.ResponseWriter, r *http.Request, v viewer) {
	list, _ := s.App.DB.ListNotifications(v.User.ID, 100)
	s.render(w, r, v, "notifications.html", map[string]any{"Title": "Notifications", "Nav": "overview", "Notes": list})
}

func (s *Server) notificationsRead(w http.ResponseWriter, r *http.Request, v viewer) {
	_ = s.App.DB.MarkNotificationsRead(v.User.ID)
	s.done(w, r, "", "/notifications")
}

// ---- health / cron / JSON -----------------------------------------------------------------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	dbOK := true
	if _, err := s.App.DB.Count("SELECT COUNT(*) FROM organizations"); err != nil {
		dbOK = false
	}
	code := 200
	if !dbOK {
		code = 503
	}
	s.json(w, code, map[string]any{"ok": dbOK, "version": s.App.Version, "demoMode": s.Cfg.DemoMode, "pendingJobs": s.App.DB.PendingJobCount(), "liveAI": s.App.AI.LiveAvailable(), "time": store.Now()})
}

func (s *Server) cronTick(w http.ResponseWriter, r *http.Request) {
	if s.Cfg.CronSecret == "" {
		s.json(w, 404, map[string]any{"error": "CRON_SECRET not configured"})
		return
	}
	got := r.Header.Get("X-Cron-Secret")
	if got == "" {
		got = r.URL.Query().Get("secret")
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.Cfg.CronSecret)) != 1 {
		s.json(w, 403, map[string]any{"error": "forbidden"})
		return
	}
	n := s.Jobs.Tick(r.Context(), 50)
	s.json(w, 200, map[string]any{"ran": n, "pending": s.App.DB.PendingJobCount()})
}

func (s *Server) apiRun(w http.ResponseWriter, r *http.Request, v viewer) {
	run, err := s.App.DB.GetRun(r.PathValue("id"))
	if err != nil {
		s.json(w, 404, map[string]any{"error": "not found"})
		return
	}
	if _, err := s.App.DB.GetBid(v.Org.ID, run.BidID); err != nil {
		s.json(w, 404, map[string]any{"error": "not found"})
		return
	}
	steps, _ := s.App.DB.ListSteps(run.ID)
	counts := map[string]map[string]int{}
	for _, st := range steps {
		if counts[st.Stage] == nil {
			counts[st.Stage] = map[string]int{}
		}
		counts[st.Stage][st.Status]++
	}
	s.json(w, 200, map[string]any{"id": run.ID, "status": run.Status, "stage": run.CurrentStage, "stageLabel": pipeline.HumanStage(run.CurrentStage), "gate": run.Gate, "progress": pipeline.Progress(run), "error": run.Error, "updatedAt": run.UpdatedAt, "steps": counts, "stages": pipeline.StageViews(&run)})
}

func (s *Server) apiJob(w http.ResponseWriter, r *http.Request, v viewer) {
	j, err := s.App.DB.GetJob(r.PathValue("id"))
	if err != nil || (j.OrgID != "" && j.OrgID != v.Org.ID) {
		s.json(w, 404, map[string]any{"error": "not found"})
		return
	}
	s.json(w, 200, map[string]any{"id": j.ID, "type": j.Type, "status": j.Status, "progress": j.Progress, "log": j.Log, "error": j.Error})
}
