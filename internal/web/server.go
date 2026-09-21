// Package web is the HTML UI and small JSON API: net/http routing, embedded
// html/template pages, session cookies, CSRF, RBAC and org scoping.
package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"bidos/internal/app"
	"bidos/internal/config"
	"bidos/internal/demo"
	"bidos/internal/jobs"
	"bidos/internal/pipeline"
	"bidos/internal/store"
)

//go:embed templates static
var assets embed.FS

// Server holds the wired services and parsed templates.
type Server struct {
	App    *app.App
	Engine *pipeline.Engine
	Jobs   *jobs.Runner
	Seeder *demo.Seeder
	Cfg    config.Config
	Log    *slog.Logger
	secret []byte
	pages  map[string]*template.Template
	mux    *http.ServeMux

	rlMu sync.Mutex
	rl   map[string]*rlBucket
}

type rlBucket struct {
	tokens float64
	last   time.Time
}

// New builds the server and its routes.
func New(cfg config.Config, a *app.App, eng *pipeline.Engine, runner *jobs.Runner, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	secret := []byte(cfg.SessionSecret)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
		log.Warn("SESSION_SECRET not set; CSRF tokens rotate on restart")
	}
	s := &Server{App: a, Engine: eng, Jobs: runner, Seeder: &demo.Seeder{App: a}, Cfg: cfg, Log: log, secret: secret, rl: map[string]*rlBucket{}}
	s.pages = parsePages()
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rw := &statusWriter{ResponseWriter: w, status: 200}
	defer func() {
		if rec := recover(); rec != nil {
			s.Log.Error("panic", "path", r.URL.Path, "err", fmt.Sprint(rec))
			http.Error(rw, "Something went wrong. The error has been logged.", 500)
		}
		s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", rw.status, "ms", time.Since(start).Milliseconds())
	}()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "same-origin")
	if r.Method == http.MethodPost && !s.allowPost(r) {
		http.Error(rw, "Too many requests; slow down.", http.StatusTooManyRequests)
		return
	}
	s.mux.ServeHTTP(rw, r)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// allowPost is a per-IP token bucket for POST requests.
// ponytail: in-memory per-process limiter; move to a shared store when running >1 replica.
func (s *Server) allowPost(r *http.Request) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.rlMu.Lock()
	defer s.rlMu.Unlock()
	b := s.rl[ip]
	now := time.Now()
	if b == nil {
		b = &rlBucket{tokens: 60, last: now}
		s.rl[ip] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * 2 // 120/min refill
	b.last = now
	if b.tokens > 60 {
		b.tokens = 60
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// ---- sessions, CSRF, RBAC -----------------------------------------------------------

type ctxKey int

const sessionCookie = "bidos_session"

type viewer struct {
	User store.User
	Org  store.Organization
	SID  string
	OK   bool
}

func (s *Server) viewer(r *http.Request) viewer {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return viewer{}
	}
	u, org, ok := s.App.SessionUser(c.Value)
	return viewer{User: u, Org: org, SID: c.Value, OK: ok}
}

func (s *Server) csrfToken(sid string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(sid))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, sid string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sid, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: 12 * 3600})
}

// handler is a page handler that already has an authenticated viewer.
type handler func(w http.ResponseWriter, r *http.Request, v viewer)

// auth wraps a handler with session, CSRF (on POST) and optional RBAC action checks.
func (s *Server) auth(action string, h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := s.viewer(r)
		if !v.OK {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost {
			if err := r.ParseMultipartForm(s.Cfg.MaxUploadBytes + 1<<20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
				s.flash(w, "The upload is too large or malformed.")
				http.Redirect(w, r, backTo(r), http.StatusSeeOther)
				return
			}
			if !hmac.Equal([]byte(r.FormValue("_csrf")), []byte(s.csrfToken(v.SID))) {
				http.Error(w, "The form has expired. Reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		if action != "" && !app.Can(v.User.Role, action) {
			s.renderError(w, r, v, 403, "Your role ("+v.User.Role+") cannot perform this action ("+action+").")
			return
		}
		h(w, r, v)
	}
}

// ---- flash, redirects, errors --------------------------------------------------------

func (s *Server) flash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: "flash", Value: url.QueryEscape(msg), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 60})
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie("flash")
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: "flash", Value: "", Path: "/", MaxAge: -1})
	v, _ := url.QueryUnescape(c.Value)
	return v
}

func backTo(r *http.Request) string {
	if b := r.FormValue("_back"); strings.HasPrefix(b, "/") {
		return b
	}
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Path != "" {
			return u.RequestURI()
		}
	}
	return "/dashboard"
}

// fail flashes a user-facing message (or a generic one) and redirects back.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	msg := "Something went wrong. The error has been logged."
	if app.IsUserError(err) {
		msg = err.Error()
	} else {
		s.Log.Error("request failed", "path", r.URL.Path, "err", err)
	}
	s.flash(w, msg)
	http.Redirect(w, r, backTo(r), http.StatusSeeOther)
}

func (s *Server) done(w http.ResponseWriter, r *http.Request, msg, to string) {
	if msg != "" {
		s.flash(w, msg)
	}
	if to == "" {
		to = backTo(r)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, v viewer, code int, msg string) {
	w.WriteHeader(code)
	s.render(w, r, v, "error.html", map[string]any{"Title": fmt.Sprintf("%d", code), "Code": code, "Message": msg})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, v viewer) {
	s.renderError(w, r, v, 404, "That page or record does not exist in this workspace.")
}

// ---- rendering -------------------------------------------------------------------------

// Page is the data every template receives.
type Page struct {
	Title   string
	Viewer  viewer
	CSRF    string
	Flash   string
	Nav     string // top-level nav key
	Bid     *store.Bid
	BidTab  string
	Unread  int
	Demo    bool
	Live    bool
	Version string
	Path    string
	Data    map[string]any
	Pack    packInfo
	Year    int
	BaseURL string
}

// CanAction reports whether the viewer's role may perform an action (RBAC).
func (p Page) CanAction(action string) bool { return app.Can(p.Viewer.User.Role, action) }

type packInfo struct{ ID, Name string }

func (s *Server) render(w http.ResponseWriter, r *http.Request, v viewer, name string, data map[string]any) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "template missing: "+name, 500)
		return
	}
	p := Page{Viewer: v, CSRF: s.csrfToken(v.SID), Flash: s.takeFlash(w, r), Data: data, Version: s.App.Version, Path: r.URL.Path, Year: time.Now().Year(), BaseURL: s.Cfg.BaseURL, Live: s.App.AI != nil && s.App.AI.LiveAvailable()}
	if data == nil {
		p.Data = map[string]any{}
	}
	if t, ok := p.Data["Title"].(string); ok {
		p.Title = t
	}
	if n, ok := p.Data["Nav"].(string); ok {
		p.Nav = n
	}
	if b, ok := p.Data["Bid"].(store.Bid); ok {
		p.Bid = &b
	}
	if t, ok := p.Data["BidTab"].(string); ok {
		p.BidTab = t
	}
	if v.OK {
		p.Unread = s.App.DB.UnreadCount(v.User.ID)
		p.Demo = v.Org.Mode == "demo"
		pk := s.App.Pack(v.Org.ID)
		p.Pack = packInfo{ID: pk.ID, Name: pk.Name}
	} else {
		p.Demo = s.Cfg.DemoMode
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", p); err != nil {
		s.Log.Error("render", "page", name, "err", err)
		fmt.Fprintf(w, "<!-- template error: %s -->", template.HTMLEscapeString(err.Error()))
	}
}

func (s *Server) json(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func parsePages() map[string]*template.Template {
	pages := map[string]*template.Template{}
	entries, err := fs.ReadDir(assets, "templates/pages")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		name := e.Name()
		t := template.New("layout").Funcs(funcMap())
		t = template.Must(t.ParseFS(assets, "templates/layout.html", "templates/partials/*.html", "templates/pages/"+name))
		pages[name] = t
	}
	return pages
}

// ---- helpers used by handlers ----------------------------------------------------------

func (s *Server) bid(w http.ResponseWriter, r *http.Request, v viewer) (store.Bid, bool) {
	b, err := s.App.DB.GetBid(v.Org.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, v)
		return store.Bid{}, false
	}
	return b, true
}

func formFile(r *http.Request, field string) (string, []byte, error) {
	f, hdr, err := r.FormFile(field)
	if err != nil {
		return "", nil, &app.UserError{Msg: "Choose a file to upload."}
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", nil, err
	}
	return hdr.Filename, data, nil
}

// Serve runs the HTTP server until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	s.Log.Info("bidos listening", "addr", addr, "url", s.Cfg.BaseURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
