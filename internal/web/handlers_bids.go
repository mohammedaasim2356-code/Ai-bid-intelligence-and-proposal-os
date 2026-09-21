package web

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"bidos/internal/app"
	"bidos/internal/pipeline"
	"bidos/internal/store"
)

// ---- bids ----------------------------------------------------------------------------------

func (s *Server) bidsList(w http.ResponseWriter, r *http.Request, v viewer) {
	sums, err := s.App.BidSummaries(v.Org.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := r.URL.Query().Get("status")
	var out []app.BidSummary
	for _, b := range sums {
		if status == "" || b.Bid.Status == status || (status == "active" && !strings.HasPrefix(b.Bid.Status, "closed") && b.Bid.Status != store.BidArchived) {
			out = append(out, b)
		}
	}
	s.render(w, r, v, "bids.html", map[string]any{"Title": "Bids", "Nav": "bids", "Bids": out, "Status": status})
}

func (s *Server) bidNew(w http.ResponseWriter, r *http.Request, v viewer) {
	users, _ := s.App.DB.ListUsers(v.Org.ID)
	s.render(w, r, v, "bid_new.html", map[string]any{"Title": "New bid", "Nav": "bids", "Users": users, "Me": v.User.ID})
}

func bidInput(r *http.Request) app.BidInput {
	val, _ := strconv.ParseFloat(strings.ReplaceAll(r.FormValue("estimated_value"), ",", ""), 64)
	return app.BidInput{Name: strings.TrimSpace(r.FormValue("name")), BuyerName: strings.TrimSpace(r.FormValue("buyer_name")), BuyerURL: strings.TrimSpace(r.FormValue("buyer_url")), OwnerID: r.FormValue("owner_id"), EstimatedValue: val, Deadline: strings.TrimSpace(r.FormValue("deadline")), Status: r.FormValue("status"), Description: strings.TrimSpace(r.FormValue("description"))}
}

func (s *Server) bidCreate(w http.ResponseWriter, r *http.Request, v viewer) {
	b, err := s.App.CreateBid(v.Org.ID, v.User.ID, bidInput(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Bid created. Upload the RFP or questionnaire to start extraction.", "/bids/"+b.ID)
}

func (s *Server) bidUpdate(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	if _, err := s.App.UpdateBid(v.Org.ID, v.User.ID, b.ID, bidInput(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Bid updated.", "/bids/"+b.ID)
}

func (s *Server) bidDecision(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	if err := s.App.SetBidDecision(v.Org.ID, v.User.ID, b.ID, r.FormValue("decision")); err != nil {
		s.fail(w, r, err)
		return
	}
	if run, err := s.App.DB.LatestRun(b.ID); err == nil && run.Status == store.JobWaiting && run.Gate == pipeline.Gate2 && r.FormValue("decision") == "bid" {
		_ = s.Engine.ApproveGate(run.ID, v.User.ID, false)
	}
	s.done(w, r, "Decision recorded: "+titleCase(r.FormValue("decision"))+".", "/bids/"+b.ID)
}

func (s *Server) bidOverview(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	sum, err := s.App.BidSummary(b)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	qual, _ := s.App.Qualify(v.Org.ID, b.ID)
	var labels []string
	var values []float64
	for _, c := range qual.Criteria {
		labels = append(labels, c.Label)
		values = append(values, qual.Qualification.Scores[c.ID])
	}
	docs, _ := s.App.DB.ListBidDocuments(b.ID)
	runs, _ := s.App.DB.ListRuns(b.ID)
	var steps []store.PipelineStep
	stageCounts := map[string]map[string]int{}
	if sum.Run != nil {
		steps, _ = s.App.DB.ListSteps(sum.Run.ID)
		for _, st := range steps {
			if stageCounts[st.Stage] == nil {
				stageCounts[st.Stage] = map[string]int{}
			}
			stageCounts[st.Stage][st.Status]++
		}
	}
	// run log: newest first, cap 60
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].StartedAt > steps[j].StartedAt })
	if len(steps) > 60 {
		steps = steps[:60]
	}
	activity, _ := s.App.DB.ActivityForEntity(b.ID, 12)
	users, _ := s.App.DB.ListUsers(v.Org.ID)
	tasks, _ := s.App.DB.ListTasks(b.ID)
	openTasks := 0
	for _, t := range tasks {
		if t.Status == "open" || t.Status == "in_progress" {
			openTasks++
		}
	}
	addenda := s.App.AddendaViews(b.ID)
	var preview any
	for _, d := range docs {
		if d.Category == "rfp" || d.Category == "questionnaire" {
			preview = d
			break
		}
	}
	s.render(w, r, v, "bid_overview.html", map[string]any{
		"Title": b.Name, "Nav": "bids", "Bid": b, "BidTab": "overview", "S": sum, "Qual": qual, "RadarLabels": labels, "RadarValues": values,
		"Docs": docs, "Runs": runs, "Steps": steps, "StageCounts": stageCounts, "Stages": pipeline.StageViews(sum.Run), "Activity": activity,
		"Users": users, "UserMap": s.userMap(v.Org.ID), "OpenTasks": openTasks, "Addenda": len(addenda), "PreviewDoc": preview,
		"AutoGates": s.App.Setting(v.Org.ID, "pipeline.auto_gates", "false") == "true", "Pack": s.App.Pack(v.Org.ID),
	})
}

// ---- pipeline ---------------------------------------------------------------------------------

func (s *Server) pipelineStart(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	auto := r.FormValue("auto") == "1" && v.Org.Mode == "demo"
	mode := "manual"
	if auto {
		mode = "demo"
	}
	if _, err := s.Engine.Start(b.ID, mode, auto, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Pipeline started. This page refreshes as stages complete.", "/bids/"+b.ID)
}

func (s *Server) pipelineGate(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	run, err := s.App.DB.LatestRun(b.ID)
	if err != nil || run.Status != store.JobWaiting {
		s.fail(w, r, &app.UserError{Msg: "There is no gate waiting for approval."})
		return
	}
	if err := s.Engine.ApproveGate(run.ID, v.User.ID, false); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Gate approved: "+pipeline.HumanStage(run.Gate)+".", "/bids/"+b.ID)
}

func (s *Server) pipelineCancel(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	if run, err := s.App.DB.LatestRun(b.ID); err == nil {
		if err := s.Engine.Cancel(run.ID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.done(w, r, "Pipeline run cancelled.", "/bids/"+b.ID)
}

func (s *Server) pipelineResume(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	if run, err := s.App.DB.LatestRun(b.ID); err == nil {
		if err := s.Engine.Resume(run.ID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.done(w, r, "Resuming from the first incomplete step.", "/bids/"+b.ID)
}

// ---- documents --------------------------------------------------------------------------------

func (s *Server) bidDocumentUpload(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	name, data, err := formFile(r, "file")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cat := r.FormValue("category")
	if cat == "" {
		cat = "rfp"
	}
	doc, _, err := s.App.IngestDocument(r.Context(), app.IngestInput{OrgID: v.Org.ID, BidID: b.ID, UserID: v.User.ID, Name: name, Data: data, Category: cat, ApprovalState: "n/a"})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if r.FormValue("extract") == "1" && (cat == "rfp" || cat == "questionnaire") {
		res, err := s.App.ExtractRequirements(r.Context(), v.Org.ID, b.ID, doc.ID, app.ExtractOptions{UserID: v.User.ID})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.done(w, r, extractionMsg(res), "/bids/"+b.ID+"/requirements")
		return
	}
	s.done(w, r, "Uploaded "+doc.Name+".", "/bids/"+b.ID)
}

func extractionMsg(res app.ExtractionResult) string {
	msg := "Extraction finished: " + strconv.Itoa(res.Added) + " added, " + strconv.Itoa(res.Updated) + " updated"
	if res.Unanchored > 0 {
		msg += ", " + strconv.Itoa(res.Unanchored) + " unanchored (check the source spans)"
	}
	if len(res.Injections) > 0 {
		msg += "; " + strconv.Itoa(len(res.Injections)) + " suspicious instruction line(s) ignored"
	}
	return msg + " (" + res.ProviderMode + ")."
}

func (s *Server) bidExtract(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	docID := r.FormValue("doc_id")
	if docID == "" {
		docs, _ := s.App.DB.ListBidDocuments(b.ID)
		for _, d := range docs {
			if d.Category == "rfp" || d.Category == "questionnaire" {
				docID = d.ID
				break
			}
		}
	}
	if docID == "" {
		s.fail(w, r, &app.UserError{Msg: "Upload an RFP or questionnaire first."})
		return
	}
	res, err := s.App.ExtractRequirements(r.Context(), v.Org.ID, b.ID, docID, app.ExtractOptions{UserID: v.User.ID})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, extractionMsg(res), "/bids/"+b.ID+"/requirements")
}

func (s *Server) documentPreview(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	doc, err := s.App.DB.GetDocument(v.Org.ID, r.PathValue("doc"))
	if err != nil || doc.BidID != b.ID {
		s.notFound(w, r, v)
		return
	}
	sections := s.App.RFPPreview(doc, 400)
	s.render(w, r, v, "doc_preview.html", map[string]any{"Title": doc.Name, "Nav": "bids", "Bid": b, "BidTab": "overview", "Doc": doc, "Sections": sections})
}

// ---- requirements workspace ---------------------------------------------------------------------

func (s *Server) requirements(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	reqs, _ := s.App.DB.ListRequirements(b.ID)
	answers, _ := s.App.DB.LatestAnswersByBid(b.ID)
	q := r.URL.Query()
	fStatus, fCat, fSec, fText, fFlag := q.Get("status"), q.Get("category"), q.Get("section"), strings.ToLower(strings.TrimSpace(q.Get("q"))), q.Get("flag")
	sections := map[string]bool{}
	var filtered []store.Requirement
	counts := map[string]int{}
	for _, rq := range reqs {
		sections[rq.Section] = true
		counts[rq.Status]++
		if fStatus != "" && rq.Status != fStatus {
			continue
		}
		if fCat != "" && rq.Category != fCat {
			continue
		}
		if fSec != "" && rq.Section != fSec {
			continue
		}
		if fText != "" && !strings.Contains(strings.ToLower(rq.Code+" "+rq.Text), fText) {
			continue
		}
		switch fFlag {
		case "mandatory":
			if !rq.Mandatory {
				continue
			}
		case "unanchored":
			if rq.AnchorStatus == "anchored" {
				continue
			}
		case "unconfirmed":
			if rq.Confirmed {
				continue
			}
		case "changed":
			if rq.ChangeState == "" || rq.ChangeState == "unchanged" {
				continue
			}
		}
		filtered = append(filtered, rq)
	}
	var secList []string
	for k := range sections {
		secList = append(secList, k)
	}
	sort.Strings(secList)
	selID := q.Get("req")
	if selID == "" && len(filtered) > 0 {
		selID = filtered[0].ID
	}
	var detail *app.AnswerDetail
	if selID != "" {
		if d, err := s.App.AnswerDetail(v.Org.ID, selID); err == nil && d.Bid.ID == b.ID {
			detail = &d
		}
	}
	users, _ := s.App.DB.ListUsers(v.Org.ID)
	pack := s.App.Pack(v.Org.ID)
	data := map[string]any{
		"Title": "Requirements · " + b.Name, "Nav": "bids", "Bid": b, "BidTab": "requirements",
		"Reqs": filtered, "Total": len(reqs), "Answers": answers, "Counts": counts, "Sections": secList, "Pack": pack, "Users": users, "UserMap": s.userMap(v.Org.ID),
		"Detail": detail, "Sel": selID, "Now": s.App.Now(), "Filters": map[string]string{"status": fStatus, "category": fCat, "section": fSec, "q": fText, "flag": fFlag},
		"Query": r.URL.RawQuery, "Statuses": []string{store.StatusNotStarted, store.StatusDrafted, store.StatusInReview, store.StatusApproved, store.StatusNeedsEvidence, store.StatusNeedsSME, store.StatusNeedsReReview, store.StatusRejected},
	}
	s.render(w, r, v, "requirements.html", data)
}

func reqBack(b store.Bid, reqID string) string {
	return "/bids/" + b.ID + "/requirements?req=" + reqID
}

func (s *Server) requirementAdd(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	rq, err := s.App.AddManualRequirement(v.Org.ID, v.User.ID, b.ID, strings.TrimSpace(r.FormValue("code")), strings.TrimSpace(r.FormValue("text")), r.FormValue("category"), r.FormValue("mandatory") == "1")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Requirement "+rq.Code+" added.", reqBack(b, rq.ID))
}

func (s *Server) requirementsConfirm(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	n, err := s.App.ConfirmAllRequirements(v.Org.ID, v.User.ID, b.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if run, err := s.App.DB.LatestRun(b.ID); err == nil && run.Status == store.JobWaiting && run.Gate == pipeline.Gate1 {
		_ = s.Engine.ApproveGate(run.ID, v.User.ID, false)
	}
	s.done(w, r, strconv.Itoa(n)+" requirements confirmed.", "/bids/"+b.ID+"/requirements")
}

func (s *Server) generateAll(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	// Runs through the pipeline (fan-out jobs) so long RFPs do not block the request.
	if _, err := s.Engine.Start(b.ID, "manual", false, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Drafting queued through the pipeline; answers appear as they are verified.", "/bids/"+b.ID)
}

func (s *Server) requirementUpdate(w http.ResponseWriter, r *http.Request, v viewer) {
	rq, err := s.App.UpdateRequirement(v.Org.ID, v.User.ID, r.PathValue("id"), app.RequirementInput{
		Text: strings.TrimSpace(r.FormValue("text")), Category: r.FormValue("category"), Mandatory: r.FormValue("mandatory") == "1",
		Section: r.FormValue("section"), OwnerID: r.FormValue("owner_id"), ReviewerID: r.FormValue("reviewer_id"), Confirmed: r.FormValue("confirmed") == "1",
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Requirement "+rq.Code+" saved.", "/bids/"+rq.BidID+"/requirements?req="+rq.ID)
}

func (s *Server) requirementDelete(w http.ResponseWriter, r *http.Request, v viewer) {
	rq, err := s.App.DB.GetRequirement(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, v)
		return
	}
	if err := s.App.DeleteRequirement(v.Org.ID, v.User.ID, rq.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Requirement removed.", "/bids/"+rq.BidID+"/requirements")
}

func (s *Server) requirementGenerate(w http.ResponseWriter, r *http.Request, v viewer) {
	res, err := s.App.GenerateAnswer(r.Context(), v.Org.ID, r.PathValue("id"), app.GenerateOptions{UserID: v.User.ID, Force: true, Mode: "manual"})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msg := "Draft regenerated (" + res.ProviderMode + "): " + res.Answer.ConfidenceLabel + " → " + statusLabel(res.Answer.Status) + "."
	if res.Reused {
		msg = "Reused a verified library answer → " + statusLabel(res.Answer.Status) + "."
	}
	s.done(w, r, msg, "/bids/"+res.Requirement.BidID+"/requirements?req="+res.Requirement.ID)
}

func (s *Server) requirementAssign(w http.ResponseWriter, r *http.Request, v viewer) {
	t, err := s.App.AssignRequirement(v.Org.ID, v.User.ID, r.PathValue("id"), r.FormValue("assignee_id"), strings.TrimSpace(r.FormValue("question")), r.FormValue("due"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "SME task created.", "/bids/"+t.BidID+"/requirements?req="+t.RequirementID)
}

// ---- answers -------------------------------------------------------------------------------------

func (s *Server) answerBack(v viewer, answerID string) string {
	ans, err := s.App.DB.GetAnswer(answerID)
	if err != nil {
		return "/bids"
	}
	rq, err := s.App.DB.GetRequirement(ans.RequirementID)
	if err != nil {
		return "/bids"
	}
	return "/bids/" + rq.BidID + "/requirements?req=" + rq.ID
}

func (s *Server) answerEdit(w http.ResponseWriter, r *http.Request, v viewer) {
	ans, err := s.App.EditAnswer(v.Org.ID, v.User.ID, r.PathValue("id"), r.FormValue("text"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msg := "Answer saved and re-verified: " + ans.ConfidenceLabel + "."
	if ans.UnsupportedCount > 0 {
		msg += " " + strconv.Itoa(ans.UnsupportedCount) + " sentence(s) are unsupported by the cited evidence."
	}
	s.done(w, r, msg, s.answerBack(v, ans.ID))
}

func (s *Server) answerSubmit(w http.ResponseWriter, r *http.Request, v viewer) {
	if err := s.App.SubmitForReview(v.Org.ID, v.User.ID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Submitted for review.", s.answerBack(v, r.PathValue("id")))
}

func (s *Server) answerApprove(w http.ResponseWriter, r *http.Request, v viewer) {
	if err := s.App.ApproveAnswer(v.Org.ID, v.User.ID, r.PathValue("id"), strings.TrimSpace(r.FormValue("comment"))); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Answer approved.", s.answerBack(v, r.PathValue("id")))
}

func (s *Server) answerReject(w http.ResponseWriter, r *http.Request, v viewer) {
	if err := s.App.RejectAnswer(v.Org.ID, v.User.ID, r.PathValue("id"), strings.TrimSpace(r.FormValue("comment"))); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Answer rejected and sent back to the writer.", s.answerBack(v, r.PathValue("id")))
}

func (s *Server) answerPromote(w http.ResponseWriter, r *http.Request, v viewer) {
	e, err := s.App.PromoteAnswer(v.Org.ID, v.User.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Promoted to the Answer Library.", "/library/"+e.ID)
}

// ---- bid tasks / analytics / addenda ------------------------------------------------------------------

func (s *Server) bidTasks(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	tasks, _ := s.App.DB.ListTasks(b.ID)
	views := s.App.TaskViews(v.Org.ID, tasks)
	s.render(w, r, v, "tasks.html", map[string]any{"Title": "Tasks · " + b.Name, "Nav": "bids", "Bid": b, "BidTab": "tasks", "Tasks": views, "Scope": "bid"})
}

func (s *Server) bidAnalytics(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	sum, err := s.App.BidSummary(b)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pack := s.App.Pack(v.Org.ID)
	byStatus := namedCounts(sum.ByStatus, statusLabel)
	byCat := namedCounts(sum.ByCategory, pack.CategoryLabel)
	bySec := namedCounts(sum.BySection, func(s string) string { return s })
	labels := namedCounts(sum.Labels, func(s string) string { return s })
	stats, _ := s.App.DB.ProviderStats(daysAgo(30))
	s.render(w, r, v, "bid_analytics.html", map[string]any{"Title": "Analytics · " + b.Name, "Nav": "bids", "Bid": b, "BidTab": "analytics", "S": sum, "ByStatus": byStatus, "ByCategory": byCat, "BySection": bySec, "Labels": labels, "Providers": stats})
}

func namedCounts(m map[string]int, label func(string) string) []app.NamedCount {
	var out []app.NamedCount
	for k, val := range m {
		out = append(out, app.NamedCount{Name: label(k), Value: val})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Server) addenda(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	views := s.App.AddendaViews(b.ID)
	clar, _ := s.App.DB.ListClarifications(b.ID)
	reqs, _ := s.App.DB.ListRequirements(b.ID)
	reqByID := map[string]store.Requirement{}
	var changed []store.Requirement
	for _, rq := range reqs {
		reqByID[rq.ID] = rq
		if rq.ChangeState != "" && rq.ChangeState != "unchanged" {
			changed = append(changed, rq)
		}
	}
	s.render(w, r, v, "addenda.html", map[string]any{"Title": "Addenda · " + b.Name, "Nav": "bids", "Bid": b, "BidTab": "addenda", "Addenda": views, "Clarifications": clar, "Reqs": reqs, "ReqByID": reqByID, "Changed": changed})
}

func (s *Server) addendumUpload(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	name, data, err := formFile(r, "file")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, diff, err := s.App.UploadAddendum(r.Context(), v.Org.ID, b.ID, v.User.ID, name, data)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msg := "Addendum processed — changed: " + strings.Join(diff.Changed, ", ") + " · new: " + strings.Join(diff.New, ", ") + " · removed: " + strings.Join(diff.Removed, ", ") + ". Only the affected requirements are being re-drafted."
	s.done(w, r, msg, "/bids/"+b.ID+"/addenda")
}

func (s *Server) clarificationCreate(w http.ResponseWriter, r *http.Request, v viewer) {
	b, ok := s.bid(w, r, v)
	if !ok {
		return
	}
	if _, err := s.App.CreateClarification(v.Org.ID, v.User.ID, b.ID, r.FormValue("requirement_id"), strings.TrimSpace(r.FormValue("question")), r.FormValue("due")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Clarification question logged.", "/bids/"+b.ID+"/addenda")
}

func (s *Server) clarificationAnswer(w http.ResponseWriter, r *http.Request, v viewer) {
	if err := s.App.AnswerClarification(v.Org.ID, v.User.ID, r.PathValue("id"), strings.TrimSpace(r.FormValue("response"))); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Buyer response recorded.", "")
}
