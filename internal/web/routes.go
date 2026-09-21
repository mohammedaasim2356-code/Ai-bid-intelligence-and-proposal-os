package web

import (
	"io/fs"
	"net/http"
)

func (s *Server) routes() {
	m := s.mux
	static, _ := fs.Sub(assets, "static")
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	// public
	m.HandleFunc("GET /{$}", s.landing)
	m.HandleFunc("GET /login", s.loginPage)
	m.HandleFunc("POST /login", s.loginPost)
	m.HandleFunc("POST /logout", s.logout)
	m.HandleFunc("POST /demo/launch", s.demoLaunch)
	m.HandleFunc("GET /health", s.health)
	m.HandleFunc("POST /api/cron/tick", s.cronTick)

	// workspace
	m.HandleFunc("GET /dashboard", s.auth("", s.dashboard))
	m.HandleFunc("POST /demo/reset", s.auth("pipeline.run", s.demoReset))
	m.HandleFunc("GET /notifications", s.auth("", s.notifications))
	m.HandleFunc("POST /notifications/read", s.auth("", s.notificationsRead))
	m.HandleFunc("GET /api/runs/{id}", s.auth("", s.apiRun))
	m.HandleFunc("GET /api/jobs/{id}", s.auth("", s.apiJob))

	// bids
	m.HandleFunc("GET /bids", s.auth("", s.bidsList))
	m.HandleFunc("GET /bids/new", s.auth("bid.create", s.bidNew))
	m.HandleFunc("POST /bids", s.auth("bid.create", s.bidCreate))
	m.HandleFunc("GET /bids/{id}", s.auth("", s.bidOverview))
	m.HandleFunc("POST /bids/{id}", s.auth("bid.edit", s.bidUpdate))
	m.HandleFunc("POST /bids/{id}/decision", s.auth("gate.approve", s.bidDecision))
	m.HandleFunc("POST /bids/{id}/pipeline/start", s.auth("pipeline.run", s.pipelineStart))
	m.HandleFunc("POST /bids/{id}/pipeline/gate", s.auth("gate.approve", s.pipelineGate))
	m.HandleFunc("POST /bids/{id}/pipeline/cancel", s.auth("pipeline.run", s.pipelineCancel))
	m.HandleFunc("POST /bids/{id}/pipeline/resume", s.auth("pipeline.run", s.pipelineResume))
	m.HandleFunc("POST /bids/{id}/documents", s.auth("doc.upload", s.bidDocumentUpload))
	m.HandleFunc("POST /bids/{id}/extract", s.auth("req.edit", s.bidExtract))
	m.HandleFunc("GET /bids/{id}/requirements", s.auth("", s.requirements))
	m.HandleFunc("POST /bids/{id}/requirements", s.auth("req.edit", s.requirementAdd))
	m.HandleFunc("POST /bids/{id}/requirements/confirm", s.auth("req.confirm", s.requirementsConfirm))
	m.HandleFunc("POST /bids/{id}/generate-all", s.auth("answer.generate", s.generateAll))
	m.HandleFunc("GET /bids/{id}/tasks", s.auth("", s.bidTasks))
	m.HandleFunc("GET /bids/{id}/proposal", s.auth("", s.proposal))
	m.HandleFunc("POST /bids/{id}/proposal/sections", s.auth("proposal.edit", s.sectionAdd))
	m.HandleFunc("POST /bids/{id}/proposal/autoplace", s.auth("proposal.edit", s.autoPlace))
	m.HandleFunc("GET /bids/{id}/qa", s.auth("", s.qa))
	m.HandleFunc("POST /bids/{id}/qa/run", s.auth("qa.run", s.qaRun))
	m.HandleFunc("GET /bids/{id}/export", s.auth("", s.exportPage))
	m.HandleFunc("POST /bids/{id}/export", s.auth("export.run", s.exportRun))
	m.HandleFunc("POST /bids/{id}/roundtrip", s.auth("export.run", s.roundTrip))
	m.HandleFunc("GET /bids/{id}/addenda", s.auth("", s.addenda))
	m.HandleFunc("POST /bids/{id}/addenda", s.auth("doc.upload", s.addendumUpload))
	m.HandleFunc("POST /bids/{id}/clarifications", s.auth("clarification.edit", s.clarificationCreate))
	m.HandleFunc("GET /bids/{id}/analytics", s.auth("", s.bidAnalytics))
	m.HandleFunc("GET /bids/{id}/documents/{doc}/preview", s.auth("", s.documentPreview))

	// requirements & answers
	m.HandleFunc("POST /requirements/{id}", s.auth("req.edit", s.requirementUpdate))
	m.HandleFunc("POST /requirements/{id}/delete", s.auth("req.edit", s.requirementDelete))
	m.HandleFunc("POST /requirements/{id}/generate", s.auth("answer.generate", s.requirementGenerate))
	m.HandleFunc("POST /requirements/{id}/assign", s.auth("answer.generate", s.requirementAssign))
	m.HandleFunc("POST /answers/{id}/edit", s.auth("answer.edit", s.answerEdit))
	m.HandleFunc("POST /answers/{id}/submit", s.auth("answer.submit", s.answerSubmit))
	m.HandleFunc("POST /answers/{id}/approve", s.auth("answer.approve", s.answerApprove))
	m.HandleFunc("POST /answers/{id}/reject", s.auth("answer.reject", s.answerReject))
	m.HandleFunc("POST /answers/{id}/promote", s.auth("library.edit", s.answerPromote))

	// proposal sections, QA findings, exports, clarifications
	m.HandleFunc("POST /sections/{id}", s.auth("proposal.edit", s.sectionUpdate))
	m.HandleFunc("POST /sections/{id}/move", s.auth("proposal.edit", s.sectionMove))
	m.HandleFunc("POST /sections/{id}/insert", s.auth("proposal.edit", s.sectionInsert))
	m.HandleFunc("POST /sections/{id}/remove", s.auth("proposal.edit", s.sectionRemove))
	m.HandleFunc("POST /qa/{id}/resolve", s.auth("qa.resolve", s.qaResolve))
	m.HandleFunc("GET /exports/{id}/download", s.auth("", s.exportDownload))
	m.HandleFunc("POST /clarifications/{id}/answer", s.auth("clarification.edit", s.clarificationAnswer))

	// knowledge & library
	m.HandleFunc("GET /knowledge", s.auth("", s.knowledge))
	m.HandleFunc("POST /knowledge/upload", s.auth("doc.upload", s.knowledgeUpload))
	m.HandleFunc("GET /knowledge/{id}", s.auth("", s.knowledgeDoc))
	m.HandleFunc("POST /knowledge/{id}/approval", s.auth("library.edit", s.knowledgeApproval))
	m.HandleFunc("POST /knowledge/{id}/meta", s.auth("library.edit", s.knowledgeMeta))
	m.HandleFunc("POST /knowledge/{id}/version", s.auth("doc.upload", s.knowledgeVersion))
	m.HandleFunc("POST /knowledge/{id}/delete", s.auth("library.edit", s.knowledgeDelete))
	m.HandleFunc("GET /documents/{id}/download", s.auth("", s.documentDownload))
	m.HandleFunc("GET /library", s.auth("", s.library))
	m.HandleFunc("GET /library/{id}", s.auth("", s.libraryEntry))
	m.HandleFunc("POST /library/{id}", s.auth("library.edit", s.libraryUpdate))
	m.HandleFunc("POST /library/{id}/reapprove", s.auth("library.edit", s.libraryReapprove))
	m.HandleFunc("POST /library/{id}/delete", s.auth("library.edit", s.libraryDelete))

	// tasks
	m.HandleFunc("GET /tasks", s.auth("", s.tasks))
	m.HandleFunc("GET /tasks/{id}", s.auth("", s.task))
	m.HandleFunc("POST /tasks/{id}/respond", s.auth("task.respond", s.taskRespond))
	m.HandleFunc("POST /tasks/{id}/assign", s.auth("answer.generate", s.taskAssign))

	// analytics, settings, integrations, quality
	m.HandleFunc("GET /analytics", s.auth("", s.analytics))
	m.HandleFunc("POST /analytics/roi", s.auth("bid.edit", s.analyticsROI))
	m.HandleFunc("GET /settings", s.auth("", s.settings))
	m.HandleFunc("POST /settings/ai/test", s.auth("bid.edit", s.settingsAITest))
	m.HandleFunc("POST /settings/routing", s.auth("bid.edit", s.settingsRouting))
	m.HandleFunc("POST /settings/values", s.auth("bid.edit", s.settingsValues))
	m.HandleFunc("GET /integrations", s.auth("", s.integrations))
	m.HandleFunc("GET /quality", s.auth("", s.quality))
	m.HandleFunc("POST /quality/run", s.auth("qa.run", s.qualityRun))

	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.notFound(w, r, s.viewer(r)) })
}
