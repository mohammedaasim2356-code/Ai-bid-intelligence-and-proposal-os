package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"bidos/internal/ai"
	"bidos/internal/retrieval"
	"bidos/internal/store"
)

const LabelNotApplicable = "Not applicable"

// GenerateOptions control answer generation.
type GenerateOptions struct {
	UserID string
	Force  bool   // regenerate even if an answer exists
	Mode   string // manual | pipeline | sme_update | addendum
}

// GenerateResult is returned to the caller/pipeline step.
type GenerateResult struct {
	Answer       store.Answer
	Requirement  store.Requirement
	Retrieval    retrieval.Result
	Reused       bool
	LibraryEntry *store.LibraryEntry
	Task         *store.Task
	ProviderMode string
	ProviderID   string
	Skipped      bool
}

// GenerateAnswer runs the grounded pipeline for one requirement:
// reuse check → hybrid retrieval → gated draft → claim verification → label → route.
func (a *App) GenerateAnswer(ctx context.Context, orgID, reqID string, opts GenerateOptions) (GenerateResult, error) {
	req, bid, err := a.requirementInOrg(orgID, reqID)
	if err != nil {
		return GenerateResult{}, err
	}
	res := GenerateResult{Requirement: req, ProviderMode: "demo", ProviderID: "demo"}
	if req.ChangeState == "removed" {
		res.Skipped = true
		return res, nil
	}
	if latest, err := a.DB.LatestAnswer(req.ID); err == nil && !opts.Force && latest.Status == store.StatusApproved {
		res.Answer, res.Skipped = latest, true
		return res, nil
	}
	pack := a.Pack(orgID)
	org, _ := a.DB.GetOrg(orgID)
	company := org.Name
	if company == "" {
		company = pack.Demo.Company.Name
	}
	sens := a.Sensitivity(orgID)
	now := a.Now()
	version := 1
	if latest, err := a.DB.LatestAnswer(req.ID); err == nil {
		version = latest.Version + 1
	}
	ans := store.Answer{ID: store.NewID(), RequirementID: req.ID, Version: version, GenerationMode: "generated", ProviderID: "demo", CreatedAt: now, UpdatedAt: now, Verification: "{}"}

	// Instructions are acknowledged, not evidenced.
	if pack.IsInstruction(req.Category) {
		out := ai.DemoDraft(ai.DraftInput{Company: company, Requirement: req.Text, Instruction: true})
		ans.DraftText, ans.ConfidenceLabel, ans.Status = out.Answer, LabelNotApplicable, store.StatusDrafted
		if err := a.DB.CreateAnswer(ans); err != nil {
			return res, err
		}
		_ = a.DB.SetRequirementStatus(req.ID, store.StatusDrafted, now)
		res.Answer = ans
		a.Activity(orgID, opts.UserID, "answer.generated", "requirement", req.ID, map[string]any{"mode": "instruction"})
		return res, nil
	}

	aliases := a.Aliases(orgID)
	chunks, err := a.DB.KnowledgeChunks(orgID)
	if err != nil {
		return res, err
	}

	// 1. Reuse before generate (Rule 14). Reused answers are still verified.
	if entry, _, ok := a.FindReuse(ctx, orgID, req.Text, req.Category); ok {
		cites, _ := a.DB.ChunkInfosByIDs(entry.CitationChunkIDs)
		citeMap := map[int]string{}
		var citations []store.Citation
		valid := true
		for i, c := range cites {
			if c.DocApproval != "approved" {
				valid = false
			}
			citeMap[i+1] = c.Text
			citations = append(citations, store.Citation{Marker: i + 1, ChunkID: c.ID})
		}
		// a reused answer must itself address every hard term of the (possibly amended) requirement
		for _, t := range retrieval.HardTerms(req.Text, aliases) {
			if !retrieval.ContainsTerm(entry.AnswerText, t) {
				valid = false
			}
		}
		if valid && len(cites) == len(entry.CitationChunkIDs) && len(cites) > 0 {
			v := ai.Verify(entry.AnswerText, citeMap, a.SettingFloat(orgID, "verify.threshold", 0.5), aliases)
			if v.Unsupported == 0 {
				approver := ""
				if u, err := a.DB.GetUser(entry.ApprovedByID); err == nil {
					approver = u.Name
				}
				srcBid := ""
				if b, err := a.DB.GetBidByID(entry.SourceBidID); err == nil {
					srcBid = b.Name
				}
				vb, _ := json.Marshal(v)
				ans.DraftText, ans.GenerationMode, ans.LibraryEntryID = entry.AnswerText, "reused", entry.ID
				ans.Verification, ans.VerifiedAt, ans.UnsupportedCount = string(vb), now, 0
				ans.ConfidenceLabel = ai.Label(v, distinctDocs(cites), true, false, anyExpired(cites, now), true)
				ans.Status = store.StatusInReview
				ans.EvidenceGaps = fmt.Sprintf("Reused from %s, approved by %s on %s", srcBid, approver, entry.ApprovedAt[:10])
				if err := a.DB.CreateAnswer(ans); err != nil {
					return res, err
				}
				_ = a.DB.ReplaceCitations(ans.ID, citations)
				_ = a.DB.ReplaceEvidence(req.ID, evidenceFromChunks(cites))
				_ = a.DB.SetRequirementStatus(req.ID, ans.Status, now)
				_ = a.DB.IncrementReuse(entry.ID)
				res.Answer, res.Reused, res.LibraryEntry = ans, true, &entry
				a.Activity(orgID, opts.UserID, "answer.reused", "requirement", req.ID, map[string]any{"libraryEntry": entry.ID})
				return res, nil
			}
		}
	}

	// 2. Retrieval with filters and the evidence gate.
	var qvec []float32
	if vecs := a.Embed(ctx, []string{req.Text}); len(vecs) == 1 {
		qvec = vecs[0]
	}
	ropts := retrieval.Options{Aliases: aliases, Now: now, MinCoverage: a.SettingFloat(orgID, "retrieval.min_coverage", 0.4), IncludeExpired: a.Setting(orgID, "retrieval.include_expired", "false") == "true"}
	if kdocs, err := a.DB.ListKnowledgeDocs(orgID); err == nil {
		for _, d := range kdocs {
			if d.SourceType == "sme" && d.ApprovalState == "approved" && d.Metadata["requirement"] == req.Code {
				ropts.PinnedDocIDs = append(ropts.PinnedDocIDs, d.ID)
			}
		}
	}
	rres := retrieval.Search(ctx, req.Text, chunks, a.Embedder, qvec, ropts)
	res.Retrieval = rres
	evRows := make([]store.Evidence, 0, len(rres.Candidates))
	for _, c := range rres.Candidates {
		evRows = append(evRows, store.Evidence{ChunkID: c.Chunk.ID, Score: c.Fused, Excerpt: c.Excerpt, Approved: c.Qualified})
	}
	_ = a.DB.ReplaceEvidence(req.ID, evRows)

	// 3. Draft (through the router) with delimited evidence blocks.
	in := ai.DraftInput{Company: company, Requirement: req.Text, Category: pack.CategoryLabel(req.Category), Gap: rres.Gap}
	chunkText := map[int]string{}
	for i, e := range rres.Evidence {
		in.Evidence = append(in.Evidence, ai.EvidenceItem{Marker: i + 1, Document: DocDisplayName(e.Chunk.DocName, e.Chunk.DocVersion), Section: e.Chunk.SectionPath, Text: e.Excerpt})
		chunkText[i+1] = e.Chunk.Text
	}
	out, r, err := a.AI.Draft(ctx, orgID, sens, in)
	if err != nil {
		return res, fmt.Errorf("draft failed: %w", err)
	}
	res.ProviderMode, res.ProviderID = r.Mode, r.ProviderID
	ans.ProviderID, ans.Model = r.ProviderID, r.Model
	ans.GenerationMode = r.Mode

	// 4. Needs-evidence path: honest statement + specific SME question + routed task.
	if !rres.Sufficient || out.NeedsEvidence {
		q := out.SMEQuestion
		if q == "" {
			sq, _, _ := a.AI.SMEQuestion(ctx, orgID, sens, ai.SMEQuestionInput{Company: company, Requirement: req.Text, Category: pack.CategoryLabel(req.Category), Gap: rres.Gap, MissingTerms: rres.Missing})
			q = sq.Question
		}
		gap := rres.Gap
		if gap == "" {
			gap = out.EvidenceGaps
		}
		ans.DraftText = out.Answer
		if rres.Sufficient && out.NeedsEvidence { // live model refused despite evidence: keep its statement
			ans.DraftText = out.Answer
		}
		ans.ConfidenceLabel, ans.Status, ans.SMEQuestion, ans.EvidenceGaps = store.LabelInsufficient, store.StatusNeedsEvidence, q, gap
		if err := a.DB.CreateAnswer(ans); err != nil {
			return res, err
		}
		_ = a.DB.SetRequirementStatus(req.ID, store.StatusNeedsEvidence, now)
		task, _ := a.CreateSMETask(orgID, req, q, opts.UserID, bid.Deadline)
		if task.ID != "" {
			res.Task = &task
		}
		res.Answer = ans
		a.Activity(orgID, opts.UserID, "answer.needs_evidence", "requirement", req.ID, map[string]any{"gap": gap, "mode": r.Mode})
		return res, nil
	}

	// 5. Claim verification (deterministic) + optional live second opinion (adds flags only).
	v := ai.Verify(out.Answer, chunkText, a.SettingFloat(orgID, "verify.threshold", 0.5), aliases)
	if a.AI.LiveAvailable() && r.Mode != "demo" {
		vin := ai.VerifyInput{Draft: out.Answer}
		for i, e := range rres.Evidence {
			vin.Evidence = append(vin.Evidence, ai.EvidenceItem{Marker: i + 1, Text: e.Chunk.Text})
		}
		if vo, vr, err := a.AI.VerifyClaims(ctx, orgID, sens, vin); err == nil && vr.Mode != "demo" {
			for _, f := range vo.Flags {
				v.ExtraFlags = append(v.ExtraFlags, f.Sentence+" — "+f.Reason)
				for i := range v.Sentences {
					if v.Sentences[i].Status == "supported" && strings.Contains(v.Sentences[i].Text, strings.TrimSpace(f.Sentence)) {
						v.Sentences[i].Status, v.Sentences[i].Reason = "unsupported", "flagged by live verifier: "+f.Reason
						v.Unsupported++
					}
				}
			}
		}
	}
	vb, _ := json.Marshal(v)
	ans.DraftText, ans.Verification, ans.VerifiedAt, ans.UnsupportedCount = out.Answer, string(vb), now, v.Unsupported
	ans.ConfidenceLabel = ai.Label(v, rres.DistinctDocs, rres.Direct, false, evidenceExpired(rres.Evidence), true)

	// 6. Route by label.
	var citations []store.Citation
	for _, m := range markersIn(out.Answer) {
		if m >= 1 && m <= len(rres.Evidence) {
			citations = append(citations, store.Citation{Marker: m, ChunkID: rres.Evidence[m-1].Chunk.ID})
		}
	}
	switch ans.ConfidenceLabel {
	case store.LabelStrong:
		ans.Status = store.StatusInReview
	case store.LabelModerate:
		ans.Status = store.StatusDrafted
	default: // Weak
		ans.Status = store.StatusNeedsSME
		ans.SMEQuestion = weakQuestion(company, req.Text, v)
	}
	if err := a.DB.CreateAnswer(ans); err != nil {
		return res, err
	}
	_ = a.DB.ReplaceCitations(ans.ID, citations)
	_ = a.DB.SetRequirementStatus(req.ID, ans.Status, now)
	if ans.Status == store.StatusNeedsSME {
		task, _ := a.CreateSMETask(orgID, req, ans.SMEQuestion, opts.UserID, bid.Deadline)
		if task.ID != "" {
			res.Task = &task
		}
	}
	res.Answer = ans
	a.Activity(orgID, opts.UserID, "answer.generated", "requirement", req.ID, map[string]any{"label": ans.ConfidenceLabel, "mode": r.Mode, "provider": r.ProviderID, "unsupported": v.Unsupported})
	a.AdvanceBidStatus(bid.ID, store.BidDrafting)
	return res, nil
}

func weakQuestion(company, requirement string, v ai.Verification) string {
	var bad []string
	for _, s := range v.Sentences {
		if s.Status == "unsupported" {
			bad = append(bad, "\""+strings.TrimSpace(s.Text)+"\"")
		}
	}
	return fmt.Sprintf("The draft response to \"%s\" contains statements that could not be verified against approved sources: %s. Please confirm each statement and point to a document we can cite, or tell us what to remove.", requirement, strings.Join(bad, "; "))
}

func markersIn(text string) []int {
	seen := map[int]bool{}
	var out []int
	for _, m := range strings.Split(text, "[c") {
		if m == "" {
			continue
		}
		n := 0
		ok := false
		for _, ch := range m {
			if ch >= '0' && ch <= '9' {
				n = n*10 + int(ch-'0')
				ok = true
				continue
			}
			break
		}
		if ok && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func distinctDocs(cites []store.ChunkInfo) int {
	m := map[string]bool{}
	for _, c := range cites {
		m[c.DocumentID] = true
	}
	return len(m)
}

func anyExpired(cites []store.ChunkInfo, now string) bool {
	for _, c := range cites {
		if c.DocExpiresAt != "" && c.DocExpiresAt < now[:10] {
			return true
		}
	}
	return false
}

func evidenceExpired(ev []retrieval.Candidate) bool {
	for _, e := range ev {
		if e.Expired {
			return true
		}
	}
	return false
}

func evidenceFromChunks(cites []store.ChunkInfo) []store.Evidence {
	out := make([]store.Evidence, 0, len(cites))
	for _, c := range cites {
		out = append(out, store.Evidence{ChunkID: c.ID, Score: 1, Excerpt: retrieval.BestExcerpt(c.Text, nil, nil, 320), Approved: true})
	}
	return out
}

// EditAnswer stores a human edit as a new version and re-verifies it against the same citations.
func (a *App) EditAnswer(orgID, userID, answerID, text string) (store.Answer, error) {
	prev, req, err := a.answerInOrg(orgID, answerID)
	if err != nil {
		return prev, err
	}
	if strings.TrimSpace(text) == "" {
		return prev, userErr("The answer cannot be empty.")
	}
	cites, _ := a.DB.CitationsForAnswer(prev.ID)
	chunkText := map[int]string{}
	var newCites []store.Citation
	for _, c := range cites {
		chunkText[c.Marker] = c.Text
		newCites = append(newCites, store.Citation{Marker: c.Marker, ChunkID: c.ChunkID})
	}
	now := a.Now()
	ans := prev
	ans.ID, ans.Version, ans.FinalText, ans.CreatedAt, ans.UpdatedAt = store.NewID(), prev.Version+1, strings.TrimSpace(text), now, now
	ans.ApprovedBy, ans.ApprovedAt = "", ""
	pack := a.Pack(orgID)
	if pack.IsInstruction(req.Category) {
		ans.Status = store.StatusDrafted
	} else {
		v := ai.Verify(ans.FinalText, chunkText, a.SettingFloat(orgID, "verify.threshold", 0.5), a.Aliases(orgID))
		vb, _ := json.Marshal(v)
		ans.Verification, ans.VerifiedAt, ans.UnsupportedCount = string(vb), now, v.Unsupported
		sufficient := len(cites) > 0
		ans.ConfidenceLabel = ai.Label(v, citeDocs(cites), true, false, citesExpired(cites, now), sufficient)
		if !sufficient && v.Factual == 0 {
			ans.ConfidenceLabel = store.LabelInsufficient
		}
		ans.Status = store.StatusDrafted
	}
	ans.GenerationMode = "edited"
	if err := a.DB.CreateAnswer(ans); err != nil {
		return ans, err
	}
	_ = a.DB.ReplaceCitations(ans.ID, newCites)
	_ = a.DB.SetRequirementStatus(req.ID, ans.Status, now)
	a.Activity(orgID, userID, "answer.edited", "requirement", req.ID, map[string]any{"unsupported": ans.UnsupportedCount})
	return ans, nil
}

// SubmitForReview moves a verified draft to In Review.
func (a *App) SubmitForReview(orgID, userID, answerID string) error {
	ans, req, err := a.answerInOrg(orgID, answerID)
	if err != nil {
		return err
	}
	if ans.UnsupportedCount > 0 {
		return userErr("This answer has %d unsupported sentence(s). Fix, cite or remove them before review.", ans.UnsupportedCount)
	}
	if ans.Status == store.StatusNeedsEvidence {
		return userErr("This answer still needs evidence. Ask the SME or add a source first.")
	}
	ans.Status, ans.UpdatedAt = store.StatusInReview, a.Now()
	if err := a.DB.UpdateAnswer(ans); err != nil {
		return err
	}
	_ = a.DB.SetRequirementStatus(req.ID, store.StatusInReview, a.Now())
	a.Activity(orgID, userID, "answer.submitted", "requirement", req.ID, nil)
	if req.ReviewerID != "" {
		a.Notify(orgID, req.ReviewerID, "Answer ready for review: "+req.Code, req.Text, "/bids/"+req.BidID+"/requirements/"+req.ID)
	}
	a.AdvanceBidStatus(req.BidID, store.BidReview)
	return nil
}

// ApproveAnswer records human approval (never automatic outside demo gates).
func (a *App) ApproveAnswer(orgID, userID, answerID, comment string) error {
	ans, req, err := a.answerInOrg(orgID, answerID)
	if err != nil {
		return err
	}
	if ans.UnsupportedCount > 0 {
		return userErr("Cannot approve: %d sentence(s) are unsupported by cited evidence.", ans.UnsupportedCount)
	}
	if ans.Status == store.StatusNeedsEvidence {
		return userErr("Cannot approve an answer that still needs evidence.")
	}
	now := a.Now()
	ans.Status, ans.ApprovedBy, ans.ApprovedAt, ans.UpdatedAt = store.StatusApproved, userID, now, now
	if err := a.DB.UpdateAnswer(ans); err != nil {
		return err
	}
	_ = a.DB.CreateReview(store.Review{ID: store.NewID(), AnswerID: ans.ID, ReviewerID: userID, Status: "approved", Comment: comment, CreatedAt: now})
	_ = a.DB.SetRequirementStatus(req.ID, store.StatusApproved, now)
	a.Activity(orgID, userID, "answer.approved", "requirement", req.ID, nil)
	if a.Setting(orgID, "library.auto_promote", "true") == "true" && !a.Pack(orgID).IsInstruction(req.Category) {
		_, _ = a.PromoteAnswer(orgID, userID, ans.ID)
	}
	// close open tasks for the requirement
	tasks, _ := a.DB.OpenTasksForRequirement(req.ID)
	for _, t := range tasks {
		t.Status, t.UpdatedAt = "done", now
		_ = a.DB.UpdateTask(t)
	}
	return nil
}

// RejectAnswer sends an answer back with a comment.
func (a *App) RejectAnswer(orgID, userID, answerID, comment string) error {
	ans, req, err := a.answerInOrg(orgID, answerID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(comment) == "" {
		return userErr("Add a comment explaining what must change.")
	}
	now := a.Now()
	ans.Status, ans.UpdatedAt = store.StatusRejected, now
	if err := a.DB.UpdateAnswer(ans); err != nil {
		return err
	}
	_ = a.DB.CreateReview(store.Review{ID: store.NewID(), AnswerID: ans.ID, ReviewerID: userID, Status: "rejected", Comment: comment, CreatedAt: now})
	_ = a.DB.SetRequirementStatus(req.ID, store.StatusRejected, now)
	a.Activity(orgID, userID, "answer.rejected", "requirement", req.ID, map[string]any{"comment": comment})
	if req.OwnerID != "" {
		a.Notify(orgID, req.OwnerID, "Answer rejected: "+req.Code, comment, "/bids/"+req.BidID+"/requirements/"+req.ID)
	}
	return nil
}

func (a *App) answerInOrg(orgID, answerID string) (store.Answer, store.Requirement, error) {
	ans, err := a.DB.GetAnswer(answerID)
	if err != nil {
		return ans, store.Requirement{}, err
	}
	req, _, err := a.requirementInOrg(orgID, ans.RequirementID)
	return ans, req, err
}

func citeDocs(cites []store.CitationInfo) int {
	m := map[string]bool{}
	for _, c := range cites {
		m[c.DocID] = true
	}
	return len(m)
}

func citesExpired(cites []store.CitationInfo, now string) bool {
	for _, c := range cites {
		if c.DocExpiresAt != "" && c.DocExpiresAt < now[:10] {
			return true
		}
	}
	return false
}

// AnswerDetail bundles everything the requirement workspace shows.
type AnswerDetail struct {
	Requirement  store.Requirement
	Bid          store.Bid
	Answer       *store.Answer
	Text         string // final text if edited, else draft
	Verification ai.Verification
	Citations    []store.CitationInfo
	Evidence     []EvidenceView
	Versions     []store.Answer
	Reviews      []store.Review
	Tasks        []store.Task
	Library      *store.LibraryEntry
	Owner        store.User
	Reviewer     store.User
	Locator      string
	AnswerCell   string
	SourceDoc    store.Document
	Category     string
	Instruction  bool
	CanApprove   bool
	Reused       string
}

// EvidenceView is a retrieval candidate with its chunk for the evidence panel.
type EvidenceView struct {
	store.Evidence
	Chunk   store.ChunkInfo
	Marker  int
	Expired bool
}

func (a *App) AnswerDetail(orgID, reqID string) (AnswerDetail, error) {
	req, bid, err := a.requirementInOrg(orgID, reqID)
	if err != nil {
		return AnswerDetail{}, err
	}
	pack := a.Pack(orgID)
	d := AnswerDetail{Requirement: req, Bid: bid, Category: pack.CategoryLabel(req.Category), Instruction: pack.IsInstruction(req.Category)}
	d.Locator, d.AnswerCell = SourceLocatorParts(req.SourceLocator)
	if req.SourceDocumentID != "" {
		d.SourceDoc, _ = a.DB.GetDocumentByID(req.SourceDocumentID)
	}
	if req.OwnerID != "" {
		d.Owner, _ = a.DB.GetUser(req.OwnerID)
	}
	if req.ReviewerID != "" {
		d.Reviewer, _ = a.DB.GetUser(req.ReviewerID)
	}
	if ans, err := a.DB.LatestAnswer(req.ID); err == nil {
		d.Answer = &ans
		d.Text = ans.FinalText
		if d.Text == "" {
			d.Text = ans.DraftText
		}
		_ = json.Unmarshal([]byte(ans.Verification), &d.Verification)
		d.Citations, _ = a.DB.CitationsForAnswer(ans.ID)
		d.Versions, _ = a.DB.AnswerVersions(req.ID)
		d.Reviews, _ = a.DB.ListReviews(ans.ID)
		d.CanApprove = ans.UnsupportedCount == 0 && ans.Status != store.StatusNeedsEvidence && ans.Status != store.StatusApproved
		if ans.LibraryEntryID != "" {
			if e, err := a.DB.GetLibraryEntry(orgID, ans.LibraryEntryID); err == nil {
				d.Library = &e
				d.Reused = ans.EvidenceGaps
			}
		}
	}
	d.Tasks, _ = a.DB.OpenTasksForRequirement(req.ID)
	if len(d.Tasks) == 0 {
		all, _ := a.DB.ListTasks(bid.ID)
		for _, t := range all {
			if t.RequirementID == req.ID {
				d.Tasks = append(d.Tasks, t)
			}
		}
	}
	evs, _ := a.DB.ListEvidence(req.ID)
	ids := make([]string, 0, len(evs))
	for _, e := range evs {
		ids = append(ids, e.ChunkID)
	}
	chunks, _ := a.DB.ChunkInfosByIDs(ids)
	byID := map[string]store.ChunkInfo{}
	for _, c := range chunks {
		byID[c.ID] = c
	}
	markerByChunk := map[string]int{}
	for _, c := range d.Citations {
		markerByChunk[c.ChunkID] = c.Marker
	}
	today := time.Now().UTC().Format("2006-01-02")
	for _, e := range evs {
		c := byID[e.ChunkID]
		d.Evidence = append(d.Evidence, EvidenceView{Evidence: e, Chunk: c, Marker: markerByChunk[e.ChunkID], Expired: c.DocExpiresAt != "" && c.DocExpiresAt < today})
	}
	return d, nil
}

// GenerateAll generates answers for every requirement without an approved answer.
func (a *App) GenerateAll(ctx context.Context, orgID, bidID, userID string, progress func(done, total int)) (int, error) {
	reqs, err := a.DB.ListRequirements(bidID)
	if err != nil {
		return 0, err
	}
	n := 0
	for i, r := range reqs {
		if r.ChangeState == "removed" || r.Status == store.StatusApproved {
			continue
		}
		if _, err := a.GenerateAnswer(ctx, orgID, r.ID, GenerateOptions{UserID: userID, Force: true, Mode: "bulk"}); err != nil {
			a.Log.Warn("generate failed", "req", r.Code, "err", err)
			continue
		}
		n++
		if progress != nil {
			progress(i+1, len(reqs))
		}
	}
	return n, nil
}
