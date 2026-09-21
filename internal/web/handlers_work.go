package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"bidos/internal/ai"
	"bidos/internal/app"
	"bidos/internal/evals"
	"bidos/internal/store"
)

func daysAgo(n int) string {
	return store.FormatTime(time.Now().Add(-time.Duration(n) * 24 * time.Hour))
}

// ---- proposal editor -----------------------------------------------------------------------------

func (s *Server) proposal(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	if _, err := s.App.EnsureSections(v.Org.ID, b.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	asm, err := s.App.Assemble(v.Org.ID, b.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pack := s.App.Pack(v.Org.ID)
	s.render(w, r, v, "proposal.html", map[string]any{"Title": "Proposal · " + b.Name, "Nav": "bids", "Bid": b, "BidTab": "proposal", "A": asm, "Pack": pack, "Sel": r.URL.Query().Get("section")})
}

func (s *Server) sectionAdd(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	sec, err := s.App.AddSection(v.Org.ID, v.User.ID, b.ID, strings.TrimSpace(r.FormValue("title")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Section added.", "/bids/"+b.ID+"/proposal?section="+sec.ID)
}

func (s *Server) autoPlace(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	n, err := s.App.AutoPlaceAnswers(v.Org.ID, v.User.ID, b.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, strconv.Itoa(n)+" approved answers placed into sections by category.", "/bids/"+b.ID+"/proposal")
}

func (s *Server) sectionBack(id string) string {
	sec, err := s.App.DB.GetSection(id)
	if err != nil {
		return "/bids"
	}
	return "/bids/" + sec.BidID + "/proposal?section=" + sec.ID
}

func (s *Server) sectionUpdate(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	if err := s.App.UpdateSection(v.Org.ID, v.User.ID, id, strings.TrimSpace(r.FormValue("title")), r.FormValue("content")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Section saved.", s.sectionBack(id))
}

func (s *Server) sectionMove(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	delta := -1
	if r.FormValue("dir") == "down" {
		delta = 1
	}
	if err := s.App.MoveSection(v.Org.ID, v.User.ID, id, delta); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "", s.sectionBack(id))
}

func (s *Server) sectionInsert(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	if err := s.App.InsertAnswer(v.Org.ID, v.User.ID, id, r.FormValue("answer_id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Approved answer inserted with its citations as footnotes.", s.sectionBack(id))
}

func (s *Server) sectionRemove(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	if err := s.App.RemoveAnswer(v.Org.ID, v.User.ID, id, r.FormValue("answer_id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Block removed from the section.", s.sectionBack(id))
}

// ---- QA ------------------------------------------------------------------------------------------------

func (s *Server) qa(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	sum, err := s.App.QASummary(v.Org.ID, b.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	groups := map[string][]store.QAResult{"blocker": nil, "warning": nil, "info": nil}
	var resolved []store.QAResult
	for _, f := range sum.Findings {
		if f.Status != "open" {
			resolved = append(resolved, f)
			continue
		}
		groups[f.Severity] = append(groups[f.Severity], f)
	}
	links := map[string]string{}
	for _, f := range sum.Findings {
		links[f.ID] = app.FindingLink(f)
	}
	s.render(w, r, v, "qa.html", map[string]any{"Title": "QA · " + b.Name, "Nav": "bids", "Bid": b, "BidTab": "qa", "Q": sum, "Groups": groups, "Resolved": resolved, "Links": links})
}

func (s *Server) qaRun(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	sum, err := s.App.RunQA(v.Org.ID, b.ID, v.User.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "QA finished: "+strconv.Itoa(sum.Passed)+" of "+strconv.Itoa(sum.Total)+" checks passed, "+strconv.Itoa(sum.Blockers)+" blockers.", "/bids/"+b.ID+"/qa")
}

func (s *Server) qaResolve(w http.ResponseWriter, r *http.Request, v viewer) {
	f, err := s.App.DB.GetQA(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, v)
		return
	}
	if err := s.App.ResolveFinding(v.Org.ID, v.User.ID, f.ID, r.FormValue("status"), strings.TrimSpace(r.FormValue("reason"))); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Finding "+r.FormValue("status")+".", "/bids/"+f.BidID+"/qa")
}

// ---- export ------------------------------------------------------------------------------------------

func (s *Server) exportPage(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	exports, _ := s.App.DB.ListExports(b.ID)
	sort.SliceStable(exports, func(i, j int) bool { return exports[i].CreatedAt > exports[j].CreatedAt })
	gateMsg := ""
	if err := s.App.ExportGate(v.Org.ID, b.ID); err != nil {
		gateMsg = err.Error()
	}
	docs, _ := s.App.DB.ListBidDocuments(b.ID)
	var roundTrip []store.Document
	for _, d := range docs {
		if strings.HasSuffix(strings.ToLower(d.Name), ".xlsx") || strings.HasSuffix(strings.ToLower(d.Name), ".docx") {
			if d.Category == "rfp" || d.Category == "questionnaire" {
				roundTrip = append(roundTrip, d)
			}
		}
	}
	activity, _ := s.App.DB.ActivityForEntity(b.ID, 40)
	s.render(w, r, v, "export.html", map[string]any{"Title": "Export · " + b.Name, "Nav": "bids", "Bid": b, "BidTab": "export", "Exports": exports, "GateMsg": gateMsg, "RoundTrip": roundTrip, "Activity": activity, "UserMap": s.userMap(v.Org.ID)})
}

func (s *Server) exportRun(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	format := r.FormValue("format")
	job, _, _, err := s.App.ExportBid(v.Org.ID, b.ID, format, v.User.ID, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Export ready ("+strings.ToUpper(format)+").", "/exports/"+job.ID+"/download")
}

func (s *Server) roundTrip(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	job, _, _, err := s.App.RoundTrip(v.Org.ID, b.ID, r.FormValue("doc_id"), v.User.ID, r.FormValue("approved_only") == "1")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Round-trip file written: only answer cells changed.", "/exports/"+job.ID+"/download")
}

func (s *Server) exportDownload(w http.ResponseWriter, r *http.Request, v viewer) {
	job, data, err := s.App.ExportBytes(v.Org.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, v)
		return
	}
	name := filepath.Base(job.FilePath)
	if i := strings.Index(name, "-"); i > 0 && i < 20 {
		name = name[i+1:]
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Type", mimeFor(name))
	_, _ = w.Write(data)
}

func mimeFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pdf":
		return "application/pdf"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".html":
		return "text/html; charset=utf-8"
	case ".md", ".txt":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// ---- knowledge ----------------------------------------------------------------------------------------

func (s *Server) knowledge(w http.ResponseWriter, r *http.Request, v viewer) {
	docs, _ := s.App.DB.ListKnowledgeDocs(v.Org.ID)
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	cat, state := r.URL.Query().Get("category"), r.URL.Query().Get("state")
	today := time.Now().UTC().Format("2006-01-02")
	var out []store.Document
	counts := map[string]int{}
	expired := 0
	for _, d := range docs {
		counts[d.ApprovalState]++
		if d.ExpiresAt != "" && d.ExpiresAt < today {
			expired++
		}
		if q != "" && !strings.Contains(strings.ToLower(d.Name+" "+strings.Join(d.Tags, " ")), q) {
			continue
		}
		if cat != "" && d.Category != cat {
			continue
		}
		if state != "" && d.ApprovalState != state {
			continue
		}
		out = append(out, d)
	}
	pack := s.App.Pack(v.Org.ID)
	s.render(w, r, v, "knowledge.html", map[string]any{"Title": "Knowledge", "Nav": "knowledge", "Docs": out, "Total": len(docs), "Counts": counts, "Expired": expired, "Today": today, "Pack": pack, "Filters": map[string]string{"q": q, "category": cat, "state": state}, "EmbedderID": s.App.EmbedderID()})
}

func (s *Server) knowledgeUpload(w http.ResponseWriter, r *http.Request, v viewer) {
	name, data, err := formFile(r, "file")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cat := r.FormValue("category")
	exp := strings.TrimSpace(r.FormValue("expires_at"))
	if exp == "" {
		exp = s.App.DefaultExpiry(v.Org.ID, cat)
	}
	approval := r.FormValue("approval")
	if approval == "" || !app.Can(v.User.Role, "library.edit") {
		approval = "unapproved"
	}
	doc, _, err := s.App.IngestDocument(r.Context(), app.IngestInput{OrgID: v.Org.ID, UserID: v.User.ID, Name: name, Data: data, Category: cat, ApprovalState: approval, ExpiresAt: exp, ClientDisclosure: r.FormValue("disclosure"), SourceType: "upload", Tags: splitTags(r.FormValue("tags"))})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Ingested "+doc.Name+" ("+approval+").", "/knowledge/"+doc.ID)
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (s *Server) knowledgeDoc(w http.ResponseWriter, r *http.Request, v viewer) {
	doc, err := s.App.DB.GetDocument(v.Org.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, v)
		return
	}
	chunks, _ := s.App.DB.ChunksByDocument(doc.ID)
	citing, _ := s.App.DB.AnswersCitingDocument(doc.ID)
	type cite struct {
		Answer store.Answer
		Req    store.Requirement
	}
	var cites []cite
	for _, a := range citing {
		if rq, err := s.App.DB.GetRequirement(a.RequirementID); err == nil {
			cites = append(cites, cite{Answer: a, Req: rq})
		}
	}
	pack := s.App.Pack(v.Org.ID)
	today := time.Now().UTC().Format("2006-01-02")
	s.render(w, r, v, "knowledge_doc.html", map[string]any{"Title": doc.Name, "Nav": "knowledge", "Doc": doc, "Chunks": chunks, "Cites": cites, "Pack": pack, "Expired": doc.ExpiresAt != "" && doc.ExpiresAt < today, "Tags": strings.Join(doc.Tags, ", ")})
}

func (s *Server) knowledgeApproval(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	if err := s.App.SetDocumentApproval(v.Org.ID, id, r.FormValue("state"), v.User.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	msg := "Document marked " + r.FormValue("state") + "."
	if r.FormValue("state") != "approved" {
		msg += " Answers citing it were flagged for re-review."
	}
	s.done(w, r, msg, "/knowledge/"+id)
}

func (s *Server) knowledgeMeta(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	if err := s.App.UpdateDocumentMeta(v.Org.ID, id, v.User.ID, r.FormValue("category"), strings.TrimSpace(r.FormValue("expires_at")), r.FormValue("disclosure"), splitTags(r.FormValue("tags"))); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Metadata saved.", "/knowledge/"+id)
}

func (s *Server) knowledgeVersion(w http.ResponseWriter, r *http.Request, v viewer) {
	name, data, err := formFile(r, "file")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	doc, err := s.App.NewDocumentVersion(r.Context(), v.Org.ID, r.PathValue("id"), v.User.ID, name, data)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Version "+strconv.Itoa(doc.Version)+" uploaded (unapproved until reviewed).", "/knowledge/"+doc.ID)
}

func (s *Server) knowledgeDelete(w http.ResponseWriter, r *http.Request, v viewer) {
	if err := s.App.DeleteDocument(v.Org.ID, r.PathValue("id"), v.User.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Document deleted; citing answers need re-review.", "/knowledge")
}

func (s *Server) documentDownload(w http.ResponseWriter, r *http.Request, v viewer) {
	doc, err := s.App.DB.GetDocument(v.Org.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, v)
		return
	}
	data, err := s.App.DocumentBytes(doc)
	if err != nil {
		s.notFound(w, r, v)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+doc.Name+"\"")
	w.Header().Set("Content-Type", mimeFor(doc.Name))
	_, _ = w.Write(data)
}

// ---- library -------------------------------------------------------------------------------------------

func (s *Server) library(w http.ResponseWriter, r *http.Request, v viewer) {
	entries, _ := s.App.DB.ListLibrary(v.Org.ID)
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	var out []store.LibraryEntry
	for _, e := range entries {
		if q == "" || strings.Contains(strings.ToLower(e.CanonicalQuestion+" "+e.AnswerText), q) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ReuseCount > out[j].ReuseCount })
	hyg := s.App.LibraryHygieneReport(v.Org.ID)
	s.render(w, r, v, "library.html", map[string]any{"Title": "Answer Library", "Nav": "knowledge", "Entries": out, "Hygiene": hyg, "Q": q, "Pack": s.App.Pack(v.Org.ID), "UserMap": s.userMap(v.Org.ID), "Today": time.Now().UTC().Format("2006-01-02")})
}

func (s *Server) libraryEntry(w http.ResponseWriter, r *http.Request, v viewer) {
	e, err := s.App.DB.GetLibraryEntry(v.Org.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, v)
		return
	}
	cites, _ := s.App.DB.ChunkInfosByIDs(e.CitationChunkIDs)
	srcBid, _ := s.App.DB.GetBidByID(e.SourceBidID)
	s.render(w, r, v, "library_entry.html", map[string]any{"Title": "Library entry", "Nav": "knowledge", "E": e, "Cites": cites, "SourceBid": srcBid, "Pack": s.App.Pack(v.Org.ID), "UserMap": s.userMap(v.Org.ID), "Tags": strings.Join(e.Tags, ", "), "Today": time.Now().UTC().Format("2006-01-02")})
}

func (s *Server) libraryUpdate(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	if err := s.App.UpdateLibraryEntry(v.Org.ID, v.User.ID, id, app.LibraryInput{Question: strings.TrimSpace(r.FormValue("question")), Answer: r.FormValue("answer"), Category: r.FormValue("category"), ReviewBy: strings.TrimSpace(r.FormValue("review_by")), Tags: splitTags(r.FormValue("tags"))}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Library entry saved.", "/library/"+id)
}

func (s *Server) libraryReapprove(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	if err := s.App.ReapproveLibraryEntry(v.Org.ID, v.User.ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Entry re-approved with a new review-by date.", "/library/"+id)
}

func (s *Server) libraryDelete(w http.ResponseWriter, r *http.Request, v viewer) {
	if err := s.App.DB.DeleteLibraryEntry(v.Org.ID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.App.Activity(v.Org.ID, v.User.ID, "library.deleted", "library", r.PathValue("id"), nil)
	s.done(w, r, "Library entry deleted.", "/library")
}

// ---- tasks ----------------------------------------------------------------------------------------------

func (s *Server) tasks(w http.ResponseWriter, r *http.Request, v viewer) {
	scope := r.URL.Query().Get("scope")
	var list []store.Task
	if scope == "all" {
		list, _ = s.App.DB.TasksForOrg(v.Org.ID)
	} else {
		scope = "mine"
		list, _ = s.App.DB.TasksForUser(v.User.ID)
	}
	views := s.App.TaskViews(v.Org.ID, list)
	open, done := 0, 0
	for _, t := range views {
		if t.Status == "done" || t.Status == "closed" {
			done++
		} else {
			open++
		}
	}
	sort.SliceStable(views, func(i, j int) bool {
		oi, oj := views[i].Status == "open" || views[i].Status == "in_progress", views[j].Status == "open" || views[j].Status == "in_progress"
		if oi != oj {
			return oi
		}
		return views[i].DueAt < views[j].DueAt
	})
	s.render(w, r, v, "tasks.html", map[string]any{"Title": "Tasks", "Nav": "tasks", "Tasks": views, "Scope": scope, "Open": open, "Done": done})
}

func (s *Server) task(w http.ResponseWriter, r *http.Request, v viewer) {
	t, err := s.App.DB.GetTask(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, v)
		return
	}
	views := s.App.TaskViews(v.Org.ID, []store.Task{t})
	if len(views) == 0 || views[0].Bid.OrgID != v.Org.ID {
		s.notFound(w, r, v)
		return
	}
	tv := views[0]
	detail, _ := s.App.AnswerDetail(v.Org.ID, t.RequirementID)
	users, _ := s.App.DB.ListUsers(v.Org.ID)
	s.render(w, r, v, "task.html", map[string]any{"Title": "Task · " + tv.Requirement.Code, "Nav": "tasks", "T": tv, "Detail": detail, "Users": users, "Bid": tv.Bid, "Pack": s.App.Pack(v.Org.ID)})
}

func (s *Server) taskRespond(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	action := r.FormValue("action")
	if err := s.App.RespondTask(r.Context(), v.Org.ID, v.User.ID, id, action, strings.TrimSpace(r.FormValue("response"))); err != nil {
		s.fail(w, r, err)
		return
	}
	msg := map[string]string{"answer": "Thanks — your statement is now citable evidence and the answer was re-drafted and re-verified.", "request_more_info": "Question sent back to the requester.", "reject": "Task declined.", "comment": "Comment added.", "start": "Task marked in progress."}[action]
	to := "/tasks/" + id
	if action == "answer" {
		if t, err := s.App.DB.GetTask(id); err == nil {
			to = "/bids/" + t.BidID + "/requirements?req=" + t.RequirementID
		}
	}
	s.done(w, r, msg, to)
}

func (s *Server) taskAssign(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	if err := s.App.AssignTask(v.Org.ID, v.User.ID, id, r.FormValue("assignee_id"), r.FormValue("due")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Task reassigned.", "/tasks/"+id)
}

// ---- analytics / settings / integrations / quality -------------------------------------------------------

func (s *Server) analytics(w http.ResponseWriter, r *http.Request, v viewer) {
	d, err := s.App.Dashboard(v.Org.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stats, _ := s.App.DB.ProviderStats(daysAgo(30))
	s.render(w, r, v, "analytics.html", map[string]any{"Title": "Analytics", "Nav": "analytics", "D": d, "Providers": stats})
}

func (s *Server) analyticsROI(w http.ResponseWriter, r *http.Request, v viewer) {
	vals := map[string]string{}
	for _, k := range []string{"baseline_hours_per_question", "loaded_hourly_rate", "draft_saving_fraction", "reuse_saving_fraction", "hours_per_rfp_admin"} {
		if val := strings.TrimSpace(r.FormValue(k)); val != "" {
			vals[k] = val
		}
	}
	if err := s.App.UpdateROISettings(v.Org.ID, v.User.ID, vals); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "ROI assumptions saved. Every figure remains an estimate.", "/analytics")
}

var editableSettings = []struct{ Key, Label, Hint string }{
	{"verify.threshold", "Claim verifier overlap threshold", "0–1; a factual sentence must overlap a cited chunk at least this much (default 0.5)."},
	{"retrieval.min_coverage", "Evidence gate minimum coverage", "0–1; salient-term coverage a chunk needs to count as evidence (default 0.4)."},
	{"retrieval.include_expired", "Include expired knowledge", "true/false; expired documents are excluded by default."},
	{"library.auto_promote", "Auto-promote approved answers", "true/false; approved answers enter the library automatically."},
	{"library.reuse_threshold", "Library reuse similarity", "0–1; near-duplicate threshold for reuse-before-generate (default 0.82)."},
	{"pipeline.auto_gates", "Auto-approve gates (demo only)", "true/false; the synthetic demo passes gates automatically."},
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request, v viewer) {
	pack := s.App.Pack(v.Org.ID)
	users, _ := s.App.DB.ListUsers(v.Org.ID)
	type route struct {
		Category, Label, Email string
		Owner                  store.User
	}
	var routes []route
	for _, c := range pack.Categories {
		if c.Instruction {
			continue
		}
		u, _ := s.App.RouteOwner(v.Org.ID, c.ID)
		routes = append(routes, route{Category: c.ID, Label: c.Label, Email: s.App.Setting(v.Org.ID, "routing."+c.ID, pack.Demo.Company.Routing[c.ID]), Owner: u})
	}
	settings, _ := s.App.DB.AllSettings(v.Org.ID)
	stats, _ := s.App.DB.ProviderStats(daysAgo(7))
	calls, _ := s.App.DB.RecentProviderCalls(15)
	cache, _ := s.App.DB.CacheStats()
	s.render(w, r, v, "settings.html", map[string]any{"Title": "Settings", "Nav": "settings", "Providers": s.App.AI.Statuses(), "RouterSource": s.App.AI.Config().Source, "Stats": stats, "Calls": calls, "Cache": cache, "Routes": routes, "Users": users, "Settings": settings, "Editable": editableSettings, "Pack": pack, "Sensitivity": s.App.Sensitivity(v.Org.ID), "EmbedderID": s.App.EmbedderID()})
}

func (s *Server) settingsAITest(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.FormValue("provider")
	p := s.App.AI.Config().Providers[id]
	if p == nil {
		s.fail(w, r, &app.UserError{Msg: "Unknown provider."})
		return
	}
	if p.Type == "demo" {
		s.done(w, r, "Demo provider is always available (deterministic, no network).", "/settings")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := (ai.OpenAICompatible{}).Ping(ctx, p); err != nil {
		s.done(w, r, "Connection test failed for "+id+": "+err.Error(), "/settings")
		return
	}
	s.done(w, r, "Connection OK: "+id+" ("+p.Model+") responded.", "/settings")
}

func (s *Server) settingsRouting(w http.ResponseWriter, r *http.Request, v viewer) {
	pack := s.App.Pack(v.Org.ID)
	for _, c := range pack.Categories {
		if email := strings.TrimSpace(r.FormValue("route_" + c.ID)); email != "" {
			_ = s.App.DB.SetSetting(v.Org.ID, "routing."+c.ID, email)
		}
	}
	s.App.Activity(v.Org.ID, v.User.ID, "settings.routing", "org", v.Org.ID, nil)
	s.done(w, r, "Routing map saved.", "/settings")
}

func (s *Server) settingsValues(w http.ResponseWriter, r *http.Request, v viewer) {
	for _, e := range editableSettings {
		val := strings.TrimSpace(r.FormValue(e.Key))
		if val == "" {
			continue
		}
		if strings.HasSuffix(e.Key, "threshold") || strings.HasSuffix(e.Key, "coverage") {
			if f, err := strconv.ParseFloat(val, 64); err != nil || f < 0 || f > 1 {
				s.fail(w, r, &app.UserError{Msg: e.Label + " must be a number between 0 and 1."})
				return
			}
		} else if val != "true" && val != "false" {
			s.fail(w, r, &app.UserError{Msg: e.Label + " must be true or false."})
			return
		}
		_ = s.App.DB.SetSetting(v.Org.ID, e.Key, val)
	}
	s.App.Activity(v.Org.ID, v.User.ID, "settings.updated", "org", v.Org.ID, nil)
	s.done(w, r, "Settings saved.", "/settings")
}

func (s *Server) integrations(w http.ResponseWriter, r *http.Request, v viewer) {
	jobsCounts, _ := s.App.DB.JobStatusCounts()
	recent, _ := s.App.DB.ListJobs(v.Org.ID, 20)
	s.render(w, r, v, "integrations.html", map[string]any{"Title": "Integrations", "Nav": "integrations", "Items": s.App.Integrations(), "JobCounts": jobsCounts, "Jobs": recent, "Cfg": map[string]any{"Workers": s.Cfg.Workers, "Cron": s.Cfg.CronSecret != "", "Inbox": s.Cfg.InboxDir}})
}

func (s *Server) quality(w http.ResponseWriter, r *http.Request, v viewer) {
	runs, _ := s.App.DB.ListEvalRuns(60)
	// group by report path (one eval invocation = one report)
	type report struct {
		Path, CreatedAt, Mode string
		Suites                []store.EvalRun
		Passed                bool
		ID                    string
	}
	var reports []*report
	byPath := map[string]*report{}
	for _, run := range runs {
		key := run.ReportPath
		if key == "" {
			key = run.CreatedAt
		}
		rep := byPath[key]
		if rep == nil {
			rep = &report{Path: run.ReportPath, CreatedAt: run.CreatedAt, Mode: run.ProviderMode, Passed: true, ID: run.ID}
			byPath[key] = rep
			reports = append(reports, rep)
		}
		rep.Suites = append(rep.Suites, run)
		if !run.Passed {
			rep.Passed = false
		}
	}
	var latestMD string
	if len(reports) > 0 && reports[0].Path != "" {
		md := strings.TrimSuffix(reports[0].Path, ".json") + ".md"
		if b, err := os.ReadFile(md); err == nil {
			latestMD = string(b)
		}
	}
	jobs, _ := s.App.DB.ListJobs(v.Org.ID, 5)
	var running *store.Job
	for i := range jobs {
		if jobs[i].Type == "evals.run" && (jobs[i].Status == store.JobQueued || jobs[i].Status == store.JobRunning) {
			running = &jobs[i]
			break
		}
	}
	targets := make([]struct {
		Key   string
		Value float64
		Upper bool
	}, 0, len(evals.Targets))
	for k, val := range evals.Targets {
		targets = append(targets, struct {
			Key   string
			Value float64
			Upper bool
		}{k, val, k == "unsupported_claim_rate" || k == "false_claims"})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Key < targets[j].Key })
	s.render(w, r, v, "quality.html", map[string]any{"Title": "Quality", "Nav": "quality", "Reports": reports, "LatestMD": latestMD, "Targets": targets, "Running": running, "Live": s.App.AI.LiveAvailable()})
}

func (s *Server) qualityRun(w http.ResponseWriter, r *http.Request, v viewer) {
	job, err := s.Jobs.Enqueue(v.Org.ID, "evals.run", map[string]any{"pack": s.App.Pack(v.Org.ID).ID, "suites": "all"}, "evals.run:"+time.Now().UTC().Format("2006-01-02T15:04"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Eval run queued (job "+job.ID[:8]+"); the report appears here when it finishes.", "/quality")
}
