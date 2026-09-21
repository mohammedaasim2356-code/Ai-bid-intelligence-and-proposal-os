package store

import "strings"

// Requirements ------------------------------------------------------------------

const reqCols = "id, bid_id, code, section, text, category, mandatory, source_document_id, source_locator, source_span, anchor_status, change_state, status, owner_id, reviewer_id, sort_order, confirmed, is_trap, created_at, updated_at"

func scanReq(r scanner) (Requirement, error) {
	var x Requirement
	var mand, conf, trap int
	err := r.Scan(&x.ID, &x.BidID, &x.Code, &x.Section, &x.Text, &x.Category, &mand, &x.SourceDocumentID, &x.SourceLocator, &x.SourceSpan, &x.AnchorStatus, &x.ChangeState, &x.Status, &x.OwnerID, &x.ReviewerID, &x.SortOrder, &conf, &trap, &x.CreatedAt, &x.UpdatedAt)
	x.Mandatory, x.Confirmed, x.IsTrap = mand == 1, conf == 1, trap == 1
	return x, err
}

func (s *DB) CreateRequirement(x Requirement) error {
	return s.Exec("INSERT INTO requirements ("+reqCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		x.ID, x.BidID, x.Code, x.Section, x.Text, x.Category, b2i(x.Mandatory), x.SourceDocumentID, x.SourceLocator, x.SourceSpan, x.AnchorStatus, x.ChangeState, x.Status, x.OwnerID, x.ReviewerID, x.SortOrder, b2i(x.Confirmed), b2i(x.IsTrap), x.CreatedAt, x.UpdatedAt)
}

func (s *DB) UpdateRequirement(x Requirement) error {
	return s.Exec("UPDATE requirements SET code=?, section=?, text=?, category=?, mandatory=?, source_document_id=?, source_locator=?, source_span=?, anchor_status=?, change_state=?, status=?, owner_id=?, reviewer_id=?, sort_order=?, confirmed=?, is_trap=?, updated_at=? WHERE id=?",
		x.Code, x.Section, x.Text, x.Category, b2i(x.Mandatory), x.SourceDocumentID, x.SourceLocator, x.SourceSpan, x.AnchorStatus, x.ChangeState, x.Status, x.OwnerID, x.ReviewerID, x.SortOrder, b2i(x.Confirmed), b2i(x.IsTrap), x.UpdatedAt, x.ID)
}

func (s *DB) SetRequirementStatus(id, status, now string) error {
	return s.Exec("UPDATE requirements SET status=?, updated_at=? WHERE id=?", status, now, id)
}

func (s *DB) GetRequirement(id string) (Requirement, error) {
	return one(s.q, scanReq, "SELECT "+reqCols+" FROM requirements WHERE id = ?", id)
}

func (s *DB) ListRequirements(bidID string) ([]Requirement, error) {
	return all(s.q, scanReq, "SELECT "+reqCols+" FROM requirements WHERE bid_id = ? ORDER BY sort_order, code, id", bidID)
}

func (s *DB) DeleteRequirement(id string) error {
	return s.Exec("DELETE FROM requirements WHERE id = ?", id)
}

func (s *DB) DeleteRequirementsByBid(bidID string) error {
	return s.Exec("DELETE FROM requirements WHERE bid_id = ?", bidID)
}

func (s *DB) RequirementStatusCounts(bidID string) (map[string]int, error) {
	return s.Pairs("SELECT status, COUNT(*) FROM requirements WHERE bid_id = ? AND change_state <> 'removed' GROUP BY status", bidID)
}

// Evidence ----------------------------------------------------------------------

const evCols = "id, requirement_id, chunk_id, score, excerpt, approved, rank"

func scanEv(r scanner) (Evidence, error) {
	var e Evidence
	var ap int
	err := r.Scan(&e.ID, &e.RequirementID, &e.ChunkID, &e.Score, &e.Excerpt, &ap, &e.Rank)
	e.Approved = ap == 1
	return e, err
}

func (s *DB) ReplaceEvidence(reqID string, items []Evidence) error {
	return s.WithTx(func(tx *DB) error {
		if err := tx.Exec("DELETE FROM evidence WHERE requirement_id = ?", reqID); err != nil {
			return err
		}
		for i, e := range items {
			if e.ID == "" {
				e.ID = NewID()
			}
			if err := tx.Exec("INSERT INTO evidence ("+evCols+") VALUES (?,?,?,?,?,?,?)", e.ID, reqID, e.ChunkID, e.Score, e.Excerpt, b2i(e.Approved), i); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *DB) ListEvidence(reqID string) ([]Evidence, error) {
	return all(s.q, scanEv, "SELECT "+evCols+" FROM evidence WHERE requirement_id = ? ORDER BY rank", reqID)
}

// Answers -----------------------------------------------------------------------

const ansCols = "id, requirement_id, draft_text, final_text, generation_mode, confidence_label, status, version, approved_by, approved_at, library_entry_id, unsupported_count, verified_at, verification, sme_question, evidence_gaps, provider_id, model, created_at, updated_at"

func scanAns(r scanner) (Answer, error) {
	var a Answer
	err := r.Scan(&a.ID, &a.RequirementID, &a.DraftText, &a.FinalText, &a.GenerationMode, &a.ConfidenceLabel, &a.Status, &a.Version, &a.ApprovedBy, &a.ApprovedAt, &a.LibraryEntryID, &a.UnsupportedCount, &a.VerifiedAt, &a.Verification, &a.SMEQuestion, &a.EvidenceGaps, &a.ProviderID, &a.Model, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (s *DB) CreateAnswer(a Answer) error {
	return s.Exec("INSERT INTO answers ("+ansCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		a.ID, a.RequirementID, a.DraftText, a.FinalText, a.GenerationMode, a.ConfidenceLabel, a.Status, a.Version, a.ApprovedBy, a.ApprovedAt, a.LibraryEntryID, a.UnsupportedCount, a.VerifiedAt, a.Verification, a.SMEQuestion, a.EvidenceGaps, a.ProviderID, a.Model, a.CreatedAt, a.UpdatedAt)
}

func (s *DB) UpdateAnswer(a Answer) error {
	return s.Exec("UPDATE answers SET draft_text=?, final_text=?, generation_mode=?, confidence_label=?, status=?, version=?, approved_by=?, approved_at=?, library_entry_id=?, unsupported_count=?, verified_at=?, verification=?, sme_question=?, evidence_gaps=?, provider_id=?, model=?, updated_at=? WHERE id=?",
		a.DraftText, a.FinalText, a.GenerationMode, a.ConfidenceLabel, a.Status, a.Version, a.ApprovedBy, a.ApprovedAt, a.LibraryEntryID, a.UnsupportedCount, a.VerifiedAt, a.Verification, a.SMEQuestion, a.EvidenceGaps, a.ProviderID, a.Model, a.UpdatedAt, a.ID)
}

func (s *DB) GetAnswer(id string) (Answer, error) {
	return one(s.q, scanAns, "SELECT "+ansCols+" FROM answers WHERE id = ?", id)
}

// LatestAnswer returns the highest-version answer for a requirement.
func (s *DB) LatestAnswer(reqID string) (Answer, error) {
	return one(s.q, scanAns, "SELECT "+ansCols+" FROM answers WHERE requirement_id = ? ORDER BY version DESC, created_at DESC", reqID)
}

func (s *DB) AnswerVersions(reqID string) ([]Answer, error) {
	return all(s.q, scanAns, "SELECT "+ansCols+" FROM answers WHERE requirement_id = ? ORDER BY version DESC", reqID)
}

// LatestAnswersByBid returns the latest answer per requirement, keyed by requirement id.
func (s *DB) LatestAnswersByBid(bidID string) (map[string]Answer, error) {
	rows, err := all(s.q, scanAns, "SELECT "+ansCols+" FROM answers WHERE requirement_id IN (SELECT id FROM requirements WHERE bid_id = ?) ORDER BY requirement_id, version", bidID)
	if err != nil {
		return nil, err
	}
	out := map[string]Answer{}
	for _, a := range rows {
		out[a.RequirementID] = a // ascending version → last wins
	}
	return out, nil
}

// AnswerIDsCitingDocument returns latest answers that cite any chunk of the document.
func (s *DB) AnswersCitingDocument(docID string) ([]Answer, error) {
	return all(s.q, scanAns, "SELECT DISTINCT "+ansCols+" FROM answers WHERE id IN (SELECT answer_id FROM citations WHERE chunk_id IN (SELECT id FROM document_chunks WHERE document_id = ?))", docID)
}

// Citations ---------------------------------------------------------------------

func (s *DB) ReplaceCitations(answerID string, cites []Citation) error {
	return s.WithTx(func(tx *DB) error {
		if err := tx.Exec("DELETE FROM citations WHERE answer_id = ?", answerID); err != nil {
			return err
		}
		for _, c := range cites {
			if c.ID == "" {
				c.ID = NewID()
			}
			if err := tx.Exec("INSERT INTO citations (id, answer_id, marker, chunk_id) VALUES (?,?,?,?)", c.ID, answerID, c.Marker, c.ChunkID); err != nil {
				return err
			}
		}
		return nil
	})
}

const citeInfoCols = "ci.id, ci.answer_id, ci.marker, ci.chunk_id, c.text, c.section_path, c.sheet_name, c.cell_range, d.name, d.id, d.expires_at, d.approval_state, c.page_number, d.version"

func scanCiteInfo(r scanner) (CitationInfo, error) {
	var c CitationInfo
	err := r.Scan(&c.ID, &c.AnswerID, &c.Marker, &c.ChunkID, &c.Text, &c.SectionPath, &c.SheetName, &c.CellRange, &c.DocName, &c.DocID, &c.DocExpiresAt, &c.DocApproval, &c.PageNumber, &c.DocVersion)
	return c, err
}

func (s *DB) CitationsForAnswer(answerID string) ([]CitationInfo, error) {
	return all(s.q, scanCiteInfo, "SELECT "+citeInfoCols+" FROM citations ci JOIN document_chunks c ON c.id = ci.chunk_id JOIN documents d ON d.id = c.document_id WHERE ci.answer_id = ? ORDER BY ci.marker", answerID)
}

func (s *DB) CitationCount(answerID string) int {
	n, _ := s.Count("SELECT COUNT(*) FROM citations WHERE answer_id = ?", answerID)
	return n
}

// Tasks -------------------------------------------------------------------------

const taskCols = "id, bid_id, requirement_id, assignee_id, requested_by, type, status, due_at, description, question, comment, response, created_at, updated_at"

func scanTask(r scanner) (Task, error) {
	var t Task
	err := r.Scan(&t.ID, &t.BidID, &t.RequirementID, &t.AssigneeID, &t.RequestedBy, &t.Type, &t.Status, &t.DueAt, &t.Description, &t.Question, &t.Comment, &t.Response, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (s *DB) CreateTask(t Task) error {
	return s.Exec("INSERT INTO tasks ("+taskCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		t.ID, t.BidID, t.RequirementID, t.AssigneeID, t.RequestedBy, t.Type, t.Status, t.DueAt, t.Description, t.Question, t.Comment, t.Response, t.CreatedAt, t.UpdatedAt)
}

func (s *DB) UpdateTask(t Task) error {
	return s.Exec("UPDATE tasks SET requirement_id=?, assignee_id=?, requested_by=?, type=?, status=?, due_at=?, description=?, question=?, comment=?, response=?, updated_at=? WHERE id=?",
		t.RequirementID, t.AssigneeID, t.RequestedBy, t.Type, t.Status, t.DueAt, t.Description, t.Question, t.Comment, t.Response, t.UpdatedAt, t.ID)
}

func (s *DB) GetTask(id string) (Task, error) {
	return one(s.q, scanTask, "SELECT "+taskCols+" FROM tasks WHERE id = ?", id)
}

func (s *DB) ListTasks(bidID string) ([]Task, error) {
	return all(s.q, scanTask, "SELECT "+taskCols+" FROM tasks WHERE bid_id = ? ORDER BY CASE status WHEN 'open' THEN 0 WHEN 'in_progress' THEN 1 ELSE 2 END, due_at, created_at", bidID)
}

func (s *DB) TasksForOrg(orgID string) ([]Task, error) {
	return all(s.q, scanTask, "SELECT "+taskCols+" FROM tasks WHERE bid_id IN (SELECT id FROM bids WHERE org_id = ?) ORDER BY CASE status WHEN 'open' THEN 0 WHEN 'in_progress' THEN 1 ELSE 2 END, due_at, created_at", orgID)
}

func (s *DB) TasksForUser(userID string) ([]Task, error) {
	return all(s.q, scanTask, "SELECT "+taskCols+" FROM tasks WHERE assignee_id = ? ORDER BY CASE status WHEN 'open' THEN 0 WHEN 'in_progress' THEN 1 ELSE 2 END, due_at, created_at", userID)
}

func (s *DB) OpenTasksForRequirement(reqID string) ([]Task, error) {
	return all(s.q, scanTask, "SELECT "+taskCols+" FROM tasks WHERE requirement_id = ? AND status IN ('open','in_progress') ORDER BY created_at", reqID)
}

func (s *DB) OverdueTasks(orgID, now string) ([]Task, error) {
	return all(s.q, scanTask, "SELECT "+taskCols+" FROM tasks WHERE bid_id IN (SELECT id FROM bids WHERE org_id = ?) AND status IN ('open','in_progress') AND due_at <> '' AND due_at < ? ORDER BY due_at", orgID, now)
}

// Reviews -----------------------------------------------------------------------

func (s *DB) CreateReview(r Review) error {
	return s.Exec("INSERT INTO reviews (id, answer_id, reviewer_id, status, comment, created_at) VALUES (?,?,?,?,?,?)", r.ID, r.AnswerID, r.ReviewerID, r.Status, r.Comment, r.CreatedAt)
}

func (s *DB) ListReviews(answerID string) ([]Review, error) {
	return all(s.q, func(r scanner) (Review, error) {
		var x Review
		err := r.Scan(&x.ID, &x.AnswerID, &x.ReviewerID, &x.Status, &x.Comment, &x.CreatedAt)
		return x, err
	}, "SELECT id, answer_id, reviewer_id, status, comment, created_at FROM reviews WHERE answer_id = ? ORDER BY created_at", answerID)
}

// QA ----------------------------------------------------------------------------

const qaCols = "id, bid_id, check_type, severity, message, entity_type, entity_id, status, resolution, fingerprint, created_at"

func scanQA(r scanner) (QAResult, error) {
	var x QAResult
	err := r.Scan(&x.ID, &x.BidID, &x.CheckType, &x.Severity, &x.Message, &x.EntityType, &x.EntityID, &x.Status, &x.Resolution, &x.Fingerprint, &x.CreatedAt)
	return x, err
}

// ReplaceQAResults stores a fresh set of findings, preserving dismissals/resolutions by fingerprint.
func (s *DB) ReplaceQAResults(bidID string, items []QAResult) error {
	return s.WithTx(func(tx *DB) error {
		old, err := tx.ListQA(bidID)
		if err != nil {
			return err
		}
		kept := map[string]QAResult{}
		for _, o := range old {
			if o.Status != "open" {
				kept[o.Fingerprint] = o
			}
		}
		if err := tx.Exec("DELETE FROM qa_results WHERE bid_id = ?", bidID); err != nil {
			return err
		}
		for _, it := range items {
			if it.ID == "" {
				it.ID = NewID()
			}
			if prev, ok := kept[it.Fingerprint]; ok {
				it.Status, it.Resolution = prev.Status, prev.Resolution
			}
			if err := tx.Exec("INSERT INTO qa_results ("+qaCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?)",
				it.ID, bidID, it.CheckType, it.Severity, it.Message, it.EntityType, it.EntityID, it.Status, it.Resolution, it.Fingerprint, it.CreatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *DB) ListQA(bidID string) ([]QAResult, error) {
	return all(s.q, scanQA, "SELECT "+qaCols+" FROM qa_results WHERE bid_id = ? ORDER BY CASE severity WHEN 'blocker' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, check_type, message", bidID)
}

func (s *DB) GetQA(id string) (QAResult, error) {
	return one(s.q, scanQA, "SELECT "+qaCols+" FROM qa_results WHERE id = ?", id)
}

func (s *DB) SetQAStatus(id, status, resolution string) error {
	return s.Exec("UPDATE qa_results SET status = ?, resolution = ? WHERE id = ?", status, resolution, id)
}

// Exports -----------------------------------------------------------------------

func (s *DB) CreateExport(e ExportJob) error {
	return s.Exec("INSERT INTO export_jobs (id, bid_id, format, status, file_path, created_by, created_at) VALUES (?,?,?,?,?,?,?)", e.ID, e.BidID, e.Format, e.Status, e.FilePath, e.CreatedBy, e.CreatedAt)
}

func (s *DB) ListExports(bidID string) ([]ExportJob, error) {
	return all(s.q, func(r scanner) (ExportJob, error) {
		var x ExportJob
		err := r.Scan(&x.ID, &x.BidID, &x.Format, &x.Status, &x.FilePath, &x.CreatedBy, &x.CreatedAt)
		return x, err
	}, "SELECT id, bid_id, format, status, file_path, created_by, created_at FROM export_jobs WHERE bid_id = ? ORDER BY created_at DESC", bidID)
}

func (s *DB) GetExport(id string) (ExportJob, error) {
	return one(s.q, func(r scanner) (ExportJob, error) {
		var x ExportJob
		err := r.Scan(&x.ID, &x.BidID, &x.Format, &x.Status, &x.FilePath, &x.CreatedBy, &x.CreatedAt)
		return x, err
	}, "SELECT id, bid_id, format, status, file_path, created_by, created_at FROM export_jobs WHERE id = ?", id)
}

// Answer library ----------------------------------------------------------------

const libCols = "id, org_id, canonical_question, answer_text, citation_chunk_ids, tags, category, owner_id, approved_by_id, approved_at, review_by, source_answer_id, source_bid_id, reuse_count, embedding, created_at"

func scanLib(r scanner) (LibraryEntry, error) {
	var e LibraryEntry
	var cites, tags string
	var emb []byte
	err := r.Scan(&e.ID, &e.OrgID, &e.CanonicalQuestion, &e.AnswerText, &cites, &tags, &e.Category, &e.OwnerID, &e.ApprovedByID, &e.ApprovedAt, &e.ReviewBy, &e.SourceAnswerID, &e.SourceBidID, &e.ReuseCount, &emb, &e.CreatedAt)
	e.CitationChunkIDs, e.Tags, e.Embedding = strs(cites), strs(tags), DecodeVec(emb)
	return e, err
}

func (s *DB) CreateLibraryEntry(e LibraryEntry) error {
	return s.Exec("INSERT INTO answer_library ("+libCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		e.ID, e.OrgID, e.CanonicalQuestion, e.AnswerText, js(e.CitationChunkIDs), js(e.Tags), e.Category, e.OwnerID, e.ApprovedByID, e.ApprovedAt, e.ReviewBy, e.SourceAnswerID, e.SourceBidID, e.ReuseCount, EncodeVec(e.Embedding), e.CreatedAt)
}

func (s *DB) UpdateLibraryEntry(e LibraryEntry) error {
	return s.Exec("UPDATE answer_library SET canonical_question=?, answer_text=?, citation_chunk_ids=?, tags=?, category=?, owner_id=?, approved_by_id=?, approved_at=?, review_by=?, reuse_count=?, embedding=? WHERE id=?",
		e.CanonicalQuestion, e.AnswerText, js(e.CitationChunkIDs), js(e.Tags), e.Category, e.OwnerID, e.ApprovedByID, e.ApprovedAt, e.ReviewBy, e.ReuseCount, EncodeVec(e.Embedding), e.ID)
}

func (s *DB) GetLibraryEntry(orgID, id string) (LibraryEntry, error) {
	return one(s.q, scanLib, "SELECT "+libCols+" FROM answer_library WHERE org_id = ? AND id = ?", orgID, id)
}

func (s *DB) ListLibrary(orgID string) ([]LibraryEntry, error) {
	return all(s.q, scanLib, "SELECT "+libCols+" FROM answer_library WHERE org_id = ? ORDER BY category, canonical_question", orgID)
}

func (s *DB) LibraryBySourceAnswer(answerID string) (LibraryEntry, error) {
	return one(s.q, scanLib, "SELECT "+libCols+" FROM answer_library WHERE source_answer_id = ?", answerID)
}

func (s *DB) DeleteLibraryEntry(orgID, id string) error {
	return s.Exec("DELETE FROM answer_library WHERE org_id = ? AND id = ?", orgID, id)
}

func (s *DB) IncrementReuse(id string) error {
	return s.Exec("UPDATE answer_library SET reuse_count = reuse_count + 1 WHERE id = ?", id)
}

// Addenda / clarifications ------------------------------------------------------

func (s *DB) CreateAddendum(a Addendum) error {
	return s.Exec("INSERT INTO addenda (id, bid_id, document_id, received_at, diff_summary) VALUES (?,?,?,?,?)", a.ID, a.BidID, a.DocumentID, a.ReceivedAt, a.DiffSummary)
}

func (s *DB) ListAddenda(bidID string) ([]Addendum, error) {
	return all(s.q, func(r scanner) (Addendum, error) {
		var x Addendum
		err := r.Scan(&x.ID, &x.BidID, &x.DocumentID, &x.ReceivedAt, &x.DiffSummary)
		return x, err
	}, "SELECT id, bid_id, document_id, received_at, diff_summary FROM addenda WHERE bid_id = ? ORDER BY received_at DESC", bidID)
}

const clarCols = "id, bid_id, requirement_id, question, sent_at, due_at, buyer_response, status, created_at"

func scanClar(r scanner) (Clarification, error) {
	var x Clarification
	err := r.Scan(&x.ID, &x.BidID, &x.RequirementID, &x.Question, &x.SentAt, &x.DueAt, &x.BuyerResponse, &x.Status, &x.CreatedAt)
	return x, err
}

func (s *DB) CreateClarification(c Clarification) error {
	return s.Exec("INSERT INTO clarifications ("+clarCols+") VALUES (?,?,?,?,?,?,?,?,?)", c.ID, c.BidID, c.RequirementID, c.Question, c.SentAt, c.DueAt, c.BuyerResponse, c.Status, c.CreatedAt)
}

func (s *DB) UpdateClarification(c Clarification) error {
	return s.Exec("UPDATE clarifications SET question=?, sent_at=?, due_at=?, buyer_response=?, status=? WHERE id=?", c.Question, c.SentAt, c.DueAt, c.BuyerResponse, c.Status, c.ID)
}

func (s *DB) GetClarification(id string) (Clarification, error) {
	return one(s.q, scanClar, "SELECT "+clarCols+" FROM clarifications WHERE id = ?", id)
}

func (s *DB) ListClarifications(bidID string) ([]Clarification, error) {
	return all(s.q, scanClar, "SELECT "+clarCols+" FROM clarifications WHERE bid_id = ? ORDER BY created_at", bidID)
}

func (s *DB) DueClarifications(orgID, before string) ([]Clarification, error) {
	return all(s.q, scanClar, "SELECT "+clarCols+" FROM clarifications WHERE bid_id IN (SELECT id FROM bids WHERE org_id = ?) AND status = 'open' AND due_at <> '' AND due_at < ?", orgID, before)
}

// Proposal sections -------------------------------------------------------------

const secCols = "id, bid_id, key, title, sort_order, content, answer_ids, updated_at"

func scanSec(r scanner) (ProposalSection, error) {
	var x ProposalSection
	var ids string
	err := r.Scan(&x.ID, &x.BidID, &x.Key, &x.Title, &x.SortOrder, &x.Content, &ids, &x.UpdatedAt)
	x.AnswerIDs = strs(ids)
	return x, err
}

func (s *DB) CreateSection(x ProposalSection) error {
	return s.Exec("INSERT INTO proposal_sections ("+secCols+") VALUES (?,?,?,?,?,?,?,?)", x.ID, x.BidID, x.Key, x.Title, x.SortOrder, x.Content, js(x.AnswerIDs), x.UpdatedAt)
}

func (s *DB) UpdateSection(x ProposalSection) error {
	return s.Exec("UPDATE proposal_sections SET title=?, sort_order=?, content=?, answer_ids=?, updated_at=? WHERE id=?", x.Title, x.SortOrder, x.Content, js(x.AnswerIDs), x.UpdatedAt, x.ID)
}

func (s *DB) GetSection(id string) (ProposalSection, error) {
	return one(s.q, scanSec, "SELECT "+secCols+" FROM proposal_sections WHERE id = ?", id)
}

func (s *DB) ListSections(bidID string) ([]ProposalSection, error) {
	return all(s.q, scanSec, "SELECT "+secCols+" FROM proposal_sections WHERE bid_id = ? ORDER BY sort_order, id", bidID)
}

func (s *DB) DeleteSection(id string) error {
	return s.Exec("DELETE FROM proposal_sections WHERE id = ?", id)
}

// Qualification -------------------------------------------------------------------

func (s *DB) UpsertQualification(q Qualification) error {
	return s.Exec("INSERT INTO qualification (bid_id, scores, flags, priority, summary, updated_at) VALUES (?,?,?,?,?,?) ON CONFLICT(bid_id) DO UPDATE SET scores=excluded.scores, flags=excluded.flags, priority=excluded.priority, summary=excluded.summary, updated_at=excluded.updated_at",
		q.BidID, js(q.Scores), js(q.Flags), q.Priority, q.Summary, q.UpdatedAt)
}

func (s *DB) GetQualification(bidID string) (Qualification, error) {
	return one(s.q, func(r scanner) (Qualification, error) {
		var q Qualification
		var scores, flags string
		err := r.Scan(&q.BidID, &scores, &flags, &q.Priority, &q.Summary, &q.UpdatedAt)
		q.Scores = unjs[map[string]float64](scores)
		q.Flags = strs(flags)
		return q, err
	}, "SELECT bid_id, scores, flags, priority, summary, updated_at FROM qualification WHERE bid_id = ?", bidID)
}

var _ = strings.TrimSpace
