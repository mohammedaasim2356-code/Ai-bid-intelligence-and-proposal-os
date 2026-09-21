package app

import (
	"context"
	"strings"
	"time"

	"bidos/internal/retrieval"
	"bidos/internal/store"
)

// PromoteAnswer copies an approved answer into the Answer Library with a review-by date.
func (a *App) PromoteAnswer(orgID, userID, answerID string) (store.LibraryEntry, error) {
	ans, req, err := a.answerInOrg(orgID, answerID)
	if err != nil {
		return store.LibraryEntry{}, err
	}
	if ans.Status != store.StatusApproved {
		return store.LibraryEntry{}, userErr("Only approved answers can be promoted to the library.")
	}
	if existing, err := a.DB.LibraryBySourceAnswer(ans.ID); err == nil {
		return existing, nil
	}
	text := ans.FinalText
	if text == "" {
		text = ans.DraftText
	}
	cites, _ := a.DB.CitationsForAnswer(ans.ID)
	ids := make([]string, 0, len(cites))
	for _, c := range cites {
		ids = append(ids, c.ChunkID)
	}
	months := a.Pack(orgID).ExpiryMonths(categoryToKnowledge(req.Category))
	if months <= 0 {
		months = 12
	}
	approvedAt := ans.ApprovedAt
	if approvedAt == "" {
		approvedAt = a.Now()
	}
	t, _ := store.ParseTime(approvedAt)
	entry := store.LibraryEntry{ID: store.NewID(), OrgID: orgID, CanonicalQuestion: req.Text, AnswerText: text, CitationChunkIDs: ids, Tags: []string{req.Category}, Category: req.Category, OwnerID: req.OwnerID, ApprovedByID: ans.ApprovedBy, ApprovedAt: approvedAt, ReviewBy: t.AddDate(0, months, 0).Format("2006-01-02"), SourceAnswerID: ans.ID, SourceBidID: req.BidID, CreatedAt: a.Now()}
	if vecs := a.Embed(context.Background(), []string{req.Text}); len(vecs) == 1 {
		entry.Embedding = vecs[0]
	}
	if err := a.DB.CreateLibraryEntry(entry); err != nil {
		return entry, err
	}
	a.Activity(orgID, userID, "library.promoted", "library", entry.ID, map[string]any{"requirement": req.Code})
	return entry, nil
}

func categoryToKnowledge(cat string) string {
	switch cat {
	case "security":
		return "security_policy"
	case "privacy_compliance":
		return "privacy_policy"
	case "implementation":
		return "implementation_methodology"
	case "commercial_legal":
		return "commercial"
	case "team_staffing":
		return "employee_bio"
	case "experience_references":
		return "case_study"
	case "company_overview":
		return "company_overview"
	}
	return "standard_qa"
}

// FindReuse finds a near-duplicate library question that is still within its review-by
// date. Similarity = 0.5 × cosine(embeddings) + 0.5 × Jaccard(term sets); threshold from
// settings (default 0.55). Entries past review-by are never reused.
func (a *App) FindReuse(ctx context.Context, orgID, question, category string) (store.LibraryEntry, float64, bool) {
	entries, err := a.DB.ListLibrary(orgID)
	if err != nil || len(entries) == 0 {
		return store.LibraryEntry{}, 0, false
	}
	threshold := a.SettingFloat(orgID, "library.reuse_threshold", 0.55)
	today := time.Now().UTC().Format("2006-01-02")
	var qvec []float32
	if vecs := a.Embed(ctx, []string{question}); len(vecs) == 1 {
		qvec = vecs[0]
	}
	qTerms := termSet(question)
	var best store.LibraryEntry
	bestScore := 0.0
	for _, e := range entries {
		if e.ReviewBy != "" && e.ReviewBy < today {
			continue
		}
		if category != "" && e.Category != "" && e.Category != category {
			continue
		}
		lex := jaccard(qTerms, termSet(e.CanonicalQuestion))
		score := lex
		if len(qvec) > 0 && len(e.Embedding) == len(qvec) {
			score = 0.5*retrieval.Cosine(qvec, e.Embedding) + 0.5*lex
		}
		if score > bestScore {
			best, bestScore = e, score
		}
	}
	if bestScore >= threshold {
		return best, bestScore, true
	}
	return store.LibraryEntry{}, bestScore, false
}

func termSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, t := range retrieval.Terms(s, nil) {
		m[t] = true
	}
	return m
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		for u := range b {
			if retrieval.StemMatch(t, u) {
				inter++
				break
			}
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// LibraryInput edits a library entry.
type LibraryInput struct {
	Question, Answer, Category, ReviewBy string
	Tags                                 []string
}

func (a *App) UpdateLibraryEntry(orgID, userID, id string, in LibraryInput) error {
	e, err := a.DB.GetLibraryEntry(orgID, id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(in.Question) == "" || strings.TrimSpace(in.Answer) == "" {
		return userErr("Question and answer are required.")
	}
	if in.ReviewBy != "" {
		if _, ok := store.ParseTime(in.ReviewBy); !ok {
			return userErr("Review-by %q is not a valid date.", in.ReviewBy)
		}
	}
	e.CanonicalQuestion, e.AnswerText, e.Category, e.ReviewBy, e.Tags = strings.TrimSpace(in.Question), strings.TrimSpace(in.Answer), in.Category, in.ReviewBy, in.Tags
	if vecs := a.Embed(context.Background(), []string{e.CanonicalQuestion}); len(vecs) == 1 {
		e.Embedding = vecs[0]
	}
	if err := a.DB.UpdateLibraryEntry(e); err != nil {
		return err
	}
	a.Activity(orgID, userID, "library.updated", "library", id, nil)
	return nil
}

// ReapproveLibraryEntry extends the review-by date after a human re-check.
func (a *App) ReapproveLibraryEntry(orgID, userID, id string) error {
	e, err := a.DB.GetLibraryEntry(orgID, id)
	if err != nil {
		return err
	}
	months := a.Pack(orgID).ExpiryMonths(categoryToKnowledge(e.Category))
	if months <= 0 {
		months = 12
	}
	e.ApprovedByID, e.ApprovedAt = userID, a.Now()
	e.ReviewBy = time.Now().AddDate(0, months, 0).Format("2006-01-02")
	if err := a.DB.UpdateLibraryEntry(e); err != nil {
		return err
	}
	a.Activity(orgID, userID, "library.reapproved", "library", id, nil)
	return nil
}

// LibraryHygiene lists expired entries and near-duplicate pairs.
type LibraryHygiene struct {
	Expired    []store.LibraryEntry
	Duplicates [][2]store.LibraryEntry
	Total      int
}

func (a *App) LibraryHygieneReport(orgID string) LibraryHygiene {
	entries, _ := a.DB.ListLibrary(orgID)
	rep := LibraryHygiene{Total: len(entries)}
	today := time.Now().UTC().Format("2006-01-02")
	for _, e := range entries {
		if e.ReviewBy != "" && e.ReviewBy < today {
			rep.Expired = append(rep.Expired, e)
		}
	}
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if jaccard(termSet(entries[i].CanonicalQuestion), termSet(entries[j].CanonicalQuestion)) >= 0.6 {
				rep.Duplicates = append(rep.Duplicates, [2]store.LibraryEntry{entries[i], entries[j]})
			}
		}
	}
	return rep
}
