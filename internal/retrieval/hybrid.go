package retrieval

import (
	"context"
	"sort"
	"strings"

	"bidos/internal/store"
)

// Options tune the hybrid search and the evidence gate.
type Options struct {
	TopK              int     // fused candidates considered (default 8)
	MaxEvidence       int     // chunks returned as evidence (default 4)
	MinCoverage       float64 // salient-term coverage a chunk needs to count (default 0.4)
	DirectCoverage    float64 // coverage at which a single chunk "states the answer directly" (default 0.7)
	IncludeExpired    bool
	IncludeUnapproved bool
	Now               string // ISO date/time for expiry checks
	Aliases           []string
	CategoryHint      []string // category keywords appended to the query
	PinnedDocIDs      []string // documents forced into evidence (e.g. SME statements for this requirement)
}

func (o Options) withDefaults() Options {
	if o.TopK <= 0 {
		o.TopK = 8
	}
	if o.MaxEvidence <= 0 {
		o.MaxEvidence = 4
	}
	if o.MinCoverage <= 0 {
		o.MinCoverage = 0.4
	}
	if o.DirectCoverage <= 0 {
		o.DirectCoverage = 0.7
	}
	return o
}

// Candidate is a scored chunk with the gate diagnostics.
type Candidate struct {
	Chunk     store.ChunkInfo
	Fused     float64
	Lexical   float64
	Semantic  float64
	Coverage  float64
	HardHits  []string
	Qualified bool
	Reason    string
	Excerpt   string
	Expired   bool
	Direct    bool
}

// Result is the outcome of retrieval for one requirement.
type Result struct {
	Candidates   []Candidate // all considered, fused order
	Evidence     []Candidate // qualified chunks (≤ MaxEvidence)
	HardTerms    []string
	Missing      []string // hard terms not covered by any qualified chunk
	Sufficient   bool
	Gap          string // human-readable explanation when insufficient
	DistinctDocs int
	Direct       bool // at least one chunk states the answer directly
}

// Search runs the hybrid pipeline over the given chunks (already scoped to the org).
func Search(ctx context.Context, query string, chunks []store.ChunkInfo, emb Embedder, queryVec []float32, opts Options) Result {
	opts = opts.withDefaults()
	res := Result{HardTerms: HardTerms(query, opts.Aliases)}
	// filters: approval, expiry, client disclosure, untrusted buyer documents
	var pool []store.ChunkInfo
	expired := map[string]bool{}
	for _, c := range chunks {
		if c.DocBidID != "" || c.DocTrust == "untrusted_external" {
			continue
		}
		if c.DocClientDisclosure == "do_not_use" {
			continue
		}
		if !opts.IncludeUnapproved && c.DocApproval != "approved" {
			continue
		}
		if c.DocExpiresAt != "" && opts.Now != "" && c.DocExpiresAt < opts.Now[:10] {
			expired[c.ID] = true
			if !opts.IncludeExpired {
				continue
			}
		}
		pool = append(pool, c)
	}
	if len(pool) == 0 {
		res.Gap = "No approved knowledge documents are available for retrieval."
		return res
	}
	docs := make([]Doc, len(pool))
	byID := map[string]store.ChunkInfo{}
	for i, c := range pool {
		docs[i] = Doc{ID: c.ID, Text: c.Text}
		byID[c.ID] = c
	}
	qText := query
	if len(opts.CategoryHint) > 0 {
		qText += " " + strings.Join(opts.CategoryHint, " ")
	}
	qTerms := Terms(qText, opts.Aliases)
	salient := Terms(query, opts.Aliases)
	bm := NewBM25(docs, opts.Aliases)
	lex := bm.Rank(qTerms, opts.TopK*3)
	lexScore := map[string]float64{}
	for _, h := range lex {
		lexScore[h.ID] = h.Score
	}
	// semantic list (only when vectors exist)
	var sem []Hit
	semScore := map[string]float64{}
	if emb != nil && len(queryVec) > 0 {
		for _, c := range pool {
			if len(c.Embedding) == 0 {
				continue
			}
			s := Cosine(queryVec, c.Embedding)
			if s > 0 {
				sem = append(sem, Hit{ID: c.ID, Score: s})
				semScore[c.ID] = s
			}
		}
		sort.SliceStable(sem, func(i, j int) bool { return sem[i].Score > sem[j].Score })
		if len(sem) > opts.TopK*3 {
			sem = sem[:opts.TopK*3]
		}
	}
	fused := RRF(lex, sem)
	if len(fused) > opts.TopK {
		fused = fused[:opts.TopK]
	}
	pinned := map[string]bool{}
	for _, id := range opts.PinnedDocIDs {
		pinned[id] = true
	}
	if len(pinned) > 0 {
		inFused := map[string]bool{}
		for _, h := range fused {
			inFused[h.ID] = true
		}
		var front []Hit
		for _, c := range pool {
			if pinned[c.DocumentID] && !inFused[c.ID] {
				front = append(front, Hit{ID: c.ID, Score: 1})
			}
		}
		fused = append(front, fused...)
	}
	docsSeen := map[string]bool{}
	covered := map[string]bool{}
	for _, h := range fused {
		c := byID[h.ID]
		cand := Candidate{Chunk: c, Fused: h.Score, Lexical: lexScore[h.ID], Semantic: semScore[h.ID], Expired: expired[h.ID]}
		cand.Coverage = Coverage(salient, Terms(c.Text, opts.Aliases))
		for _, t := range res.HardTerms {
			if ContainsTerm(c.Text, t) {
				cand.HardHits = append(cand.HardHits, t)
			}
		}
		switch {
		case pinned[c.DocumentID]:
			cand.Qualified, cand.Direct = true, true
			cand.HardHits = res.HardTerms // an SME statement answers the requirement as a whole
		case cand.Coverage < opts.MinCoverage:
			cand.Reason = "low overlap with the requirement"
		case len(res.HardTerms) > 0 && len(cand.HardHits) == 0:
			cand.Reason = "does not mention the specific terms in the requirement"
		default:
			cand.Qualified = true
			cand.Direct = cand.Coverage >= opts.DirectCoverage
		}
		cand.Excerpt = BestExcerpt(c.Text, salient, res.HardTerms, 320)
		res.Candidates = append(res.Candidates, cand)
	}
	// Minimal covering set: the best qualified chunk, then only chunks that add a hard
	// term not covered yet (or, with no hard terms, a second direct chunk). Fewer, more
	// precise citations beat a long tail of loosely related ones.
	for _, cand := range res.Candidates {
		if !cand.Qualified || len(res.Evidence) >= opts.MaxEvidence {
			continue
		}
		if len(res.Evidence) > 0 {
			adds := false
			for _, h := range cand.HardHits {
				if !covered[strings.ToLower(h)] {
					adds = true
				}
			}
			if !adds && !(len(res.HardTerms) == 0 && len(res.Evidence) < 2 && cand.Direct) {
				continue
			}
		}
		res.Evidence = append(res.Evidence, cand)
		docsSeen[cand.Chunk.DocumentID] = true
		for _, h := range cand.HardHits {
			covered[strings.ToLower(h)] = true
		}
		if cand.Direct {
			res.Direct = true
		}
	}
	for _, t := range res.HardTerms {
		if !covered[strings.ToLower(t)] {
			res.Missing = append(res.Missing, t)
		}
	}
	res.DistinctDocs = len(docsSeen)
	res.Sufficient = len(res.Evidence) > 0 && len(res.Missing) == 0
	if !res.Sufficient {
		switch {
		case len(res.Evidence) == 0 && len(res.HardTerms) > 0:
			res.Gap = "No approved document mentions: " + strings.Join(res.HardTerms, ", ") + "."
		case len(res.Evidence) == 0:
			res.Gap = "No approved document covers this topic closely enough (best overlap below threshold)."
		default:
			res.Gap = "Approved documents do not mention: " + strings.Join(res.Missing, ", ") + "."
			res.Evidence = nil // partial hard-term coverage is not evidence
		}
	}
	return res
}

// BestExcerpt picks the sentence window with the most query-term hits. Sentences that
// carry a hard term (the requirement's specific numbers/acronyms) are always included so a
// draft built from the excerpt addresses them.
func BestExcerpt(text string, salient, hard []string, maxLen int) string {
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return truncate(text, maxLen)
	}
	best, bestScore := 0, -1
	keep := make([]bool, len(sentences))
	for i, s := range sentences {
		score := 0
		st := Terms(s, nil)
		for _, q := range salient {
			for _, t := range st {
				if StemMatch(q, t) {
					score++
					break
				}
			}
		}
		for _, h := range hard {
			if ContainsTerm(s, h) {
				score += 2
				keep[i] = true
			}
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	keep[best] = true
	length := 0
	for i, k := range keep {
		if k {
			length += len(sentences[i]) + 1
		}
	}
	// extend with neighbours of the best sentence while under the limit
	for j := best + 1; j < len(sentences) && !keep[j] && length+len(sentences[j]) < maxLen; j++ {
		keep[j] = true
		length += len(sentences[j]) + 1
	}
	var parts []string
	for i, k := range keep {
		if k {
			parts = append(parts, sentences[i])
		}
	}
	return strings.Join(parts, " ") // hard-term sentences are exempt from the budget
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// splitSentences splits on sentence terminators followed by whitespace (keeps abbreviations
// like "e.g." and decimals intact).
func splitSentences(text string) []string {
	var out []string
	var cur strings.Builder
	runes := []rune(strings.TrimSpace(text))
	for i, r := range runes {
		cur.WriteRune(r)
		if (r == '.' || r == '!' || r == '?') && i+1 < len(runes) && (runes[i+1] == ' ' || runes[i+1] == '\n') {
			s := strings.TrimSpace(cur.String())
			lower := strings.ToLower(s)
			if strings.HasSuffix(lower, "e.g.") || strings.HasSuffix(lower, "i.e.") || strings.HasSuffix(lower, " vs.") || strings.HasSuffix(lower, " no.") {
				continue
			}
			if s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// SplitSentences is exported for the claim verifier.
func SplitSentences(text string) []string { return splitSentences(text) }
