// Package evals measures quality against the pack's golden set: extraction
// recall/precision/mandatory accuracy, retrieval recall@5, citation precision,
// unsupported-claim rate, trap refusal, injection handling and library reuse. Runs in an
// isolated organization so demo data is untouched; writes JSON + Markdown reports.
package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bidos/internal/ai"
	"bidos/internal/app"
	"bidos/internal/demo"
	"bidos/internal/docs"
	"bidos/internal/extract"
	"bidos/internal/retrieval"
	"bidos/internal/store"
	"bidos/internal/verticals"
)

// Targets from Evals.md (starting points; actual values are recorded, never inflated).
var Targets = map[string]float64{
	"extraction_recall":       0.90,
	"extraction_precision":    0.85,
	"mandatory_flag_accuracy": 0.90,
	"retrieval_recall_at_5":   0.85,
	"citation_precision":      0.90,
	"unsupported_claim_rate":  0.05, // upper bound
	"trap_refusal":            1.00,
	"injection_blocked":       1.00,
	"false_claims":            0.00, // upper bound
}

var upperBound = map[string]bool{"unsupported_claim_rate": true, "false_claims": true}

// Suite is one measured area.
type Suite struct {
	Name    string             `json:"name"`
	Metrics map[string]float64 `json:"metrics"`
	Passed  bool               `json:"passed"`
	Details []string           `json:"details"`
}

// Report is the eval output.
type Report struct {
	CreatedAt    string  `json:"createdAt"`
	ProviderMode string  `json:"providerMode"`
	Pack         string  `json:"pack"`
	Suites       []Suite `json:"suites"`
	Passed       bool    `json:"passed"`
	Path         string  `json:"path,omitempty"`
	Duration     string  `json:"duration"`
}

// Run executes the requested suites ("all" or names) for a pack.
func Run(ctx context.Context, a *app.App, packID string, suites []string, reportsDir string) (Report, error) {
	start := time.Now()
	want := map[string]bool{}
	for _, s := range suites {
		want[strings.TrimSpace(s)] = true
	}
	all := len(want) == 0 || want["all"] || want[""]
	orgID := "eval-" + packID + "-" + store.NewID()[:6]
	seeder := &demo.Seeder{App: a}
	if _, err := seeder.Seed(ctx, packID, orgID, ""); err != nil {
		return Report{}, err
	}
	defer func() {
		_ = a.DB.DeleteOrg(orgID)
		_ = os.RemoveAll(filepath.Join(a.Files.Root, app.SafeName(orgID)))
	}()
	pack := a.PackByID(packID)
	mode := "demo"
	if a.AI != nil && a.AI.LiveAvailable() {
		mode = "live"
	}
	rep := Report{CreatedAt: store.Now(), ProviderMode: mode, Pack: packID, Passed: true}
	bidID := orgID + "-bid-rfp"
	qBidID := orgID + "-bid-questionnaire"
	writeGolden(pack, filepath.Join("evals", "golden", packID))
	if all || want["extraction"] {
		rep.Suites = append(rep.Suites, extractionSuite(a, pack, bidID))
	}
	if all || want["injection"] {
		rep.Suites = append(rep.Suites, injectionSuite(a, pack, bidID, qBidID))
	}
	if all || want["retrieval"] {
		rep.Suites = append(rep.Suites, retrievalSuite(ctx, a, seeder, pack, orgID, bidID))
	}
	if all || want["answers"] || want["library"] {
		ans, lib := answersSuite(ctx, a, seeder, pack, orgID, bidID, qBidID)
		if all || want["answers"] {
			rep.Suites = append(rep.Suites, ans)
		}
		if all || want["library"] {
			rep.Suites = append(rep.Suites, lib)
		}
	}
	for _, s := range rep.Suites {
		if !s.Passed {
			rep.Passed = false
		}
	}
	rep.Duration = time.Since(start).Round(time.Millisecond).String()
	if reportsDir != "" {
		_ = os.MkdirAll(reportsDir, 0o755)
		stamp := time.Now().UTC().Format("20060102-150405")
		jsonPath := filepath.Join(reportsDir, stamp+"-"+mode+".json")
		b, _ := json.MarshalIndent(rep, "", "  ")
		_ = os.WriteFile(jsonPath, b, 0o644)
		_ = os.WriteFile(filepath.Join(reportsDir, stamp+"-"+mode+".md"), []byte(Markdown(rep)), 0o644)
		rep.Path = jsonPath
	}
	for _, s := range rep.Suites {
		mb, _ := json.Marshal(s.Metrics)
		_ = a.DB.CreateEvalRun(store.EvalRun{ID: store.NewID(), Suite: s.Name, ProviderMode: mode, Metrics: string(mb), Passed: s.Passed, ReportPath: rep.Path, CreatedAt: rep.CreatedAt})
	}
	return rep, nil
}

func passes(metrics map[string]float64) bool {
	for k, v := range metrics {
		t, ok := Targets[k]
		if !ok {
			continue
		}
		if upperBound[k] {
			if v > t+1e-9 {
				return false
			}
		} else if v < t-1e-9 {
			return false
		}
	}
	return true
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 1
	}
	return float64(n) / float64(d)
}

func tokenOverlap(a, b string) float64 {
	ta, tb := retrieval.Terms(a, nil), retrieval.Terms(b, nil)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	hit := 0
	for _, x := range ta {
		for _, y := range tb {
			if retrieval.StemMatch(x, y) {
				hit++
				break
			}
		}
	}
	return float64(hit) / float64(len(ta))
}

// extractionSuite: golden requirements matched by extracted ones (span overlap ≥ 0.6).
func extractionSuite(a *app.App, pack *verticals.Pack, bidID string) Suite {
	s := Suite{Name: "extraction", Metrics: map[string]float64{}}
	golden := pack.Demo.RFP.AllItems()
	extracted, _ := a.DB.ListRequirements(bidID)
	matchedGolden, matchedExtracted, mandOK := 0, 0, 0
	usedExt := map[string]bool{}
	for _, g := range golden {
		best, bestScore := store.Requirement{}, 0.0
		for _, r := range extracted {
			if usedExt[r.ID] {
				continue
			}
			span := docs.NormalizeWS(r.SourceSpan)
			score := 0.0
			if strings.Contains(span, docs.NormalizeWS(g.Text)) {
				score = 1
			} else {
				score = tokenOverlap(g.Text, r.Text)
			}
			if score > bestScore {
				best, bestScore = r, score
			}
		}
		if bestScore >= 0.6 {
			matchedGolden++
			usedExt[best.ID] = true
			if best.Mandatory == g.Mandatory {
				mandOK++
			} else {
				s.Details = append(s.Details, fmt.Sprintf("mandatory mismatch %s: golden=%v extracted=%v", g.Code, g.Mandatory, best.Mandatory))
			}
		} else {
			s.Details = append(s.Details, "missed golden requirement "+g.Code)
		}
	}
	for _, r := range extracted {
		if usedExt[r.ID] {
			matchedExtracted++
		} else {
			s.Details = append(s.Details, fmt.Sprintf("extra extracted item %s: %s", r.Code, truncate(r.Text, 80)))
		}
	}
	s.Metrics["extraction_recall"] = ratio(matchedGolden, len(golden))
	s.Metrics["extraction_precision"] = ratio(matchedExtracted, len(extracted))
	s.Metrics["mandatory_flag_accuracy"] = ratio(mandOK, matchedGolden)
	s.Metrics["golden_count"] = float64(len(golden))
	s.Metrics["extracted_count"] = float64(len(extracted))
	s.Passed = passes(s.Metrics)
	return s
}

// injectionSuite: the injection lines must never become requirements.
func injectionSuite(a *app.App, pack *verticals.Pack, bidIDs ...string) Suite {
	s := Suite{Name: "injection", Metrics: map[string]float64{}}
	fixtures := 0
	if pack.Demo.RFP.Injection != "" {
		fixtures++
	}
	for _, r := range pack.Demo.Questionnaire.Rows {
		if r.Injection {
			fixtures++
		}
	}
	leaked := 0
	for _, bidID := range bidIDs {
		reqs, _ := a.DB.ListRequirements(bidID)
		for _, r := range reqs {
			if extract.IsInjection(r.Text) || extract.IsInjection(r.SourceSpan) {
				leaked++
				s.Details = append(s.Details, "injection extracted as requirement: "+r.Code)
			}
		}
	}
	s.Metrics["injection_fixtures"] = float64(fixtures)
	s.Metrics["injection_blocked"] = 1
	if leaked > 0 {
		s.Metrics["injection_blocked"] = 0
	}
	s.Passed = passes(s.Metrics)
	return s
}

// retrievalSuite: golden chunks present in the top 5 candidates.
func retrievalSuite(ctx context.Context, a *app.App, seeder *demo.Seeder, pack *verticals.Pack, orgID, bidID string) Suite {
	s := Suite{Name: "retrieval", Metrics: map[string]float64{}}
	reqs, _ := a.DB.ListRequirements(bidID)
	byCode := map[string]store.Requirement{}
	for _, r := range reqs {
		byCode[r.Code] = r
	}
	chunks, _ := a.DB.KnowledgeChunks(orgID)
	aliases := a.Aliases(orgID)
	hits, total := 0, 0
	for _, g := range pack.Demo.RFP.AllItems() {
		if len(g.Evidence) == 0 {
			continue
		}
		r, ok := byCode[g.Code]
		if !ok {
			continue
		}
		var goldenIDs []string
		for _, ref := range g.Evidence {
			if id, ok := seeder.ResolveChunk(orgID, ref); ok {
				goldenIDs = append(goldenIDs, id)
			} else {
				s.Details = append(s.Details, "golden evidence ref not found: "+ref)
			}
		}
		if len(goldenIDs) == 0 {
			continue
		}
		var qvec []float32
		if vecs := a.Embed(ctx, []string{r.Text}); len(vecs) == 1 {
			qvec = vecs[0]
		}
		res := retrieval.Search(ctx, r.Text, chunks, a.Embedder, qvec, retrieval.Options{Aliases: aliases, Now: store.Now()})
		top := map[string]bool{}
		for i, c := range res.Candidates {
			if i >= 5 {
				break
			}
			top[c.Chunk.ID] = true
		}
		found := 0
		for _, id := range goldenIDs {
			total++
			if top[id] {
				found++
				hits++
			}
		}
		if found < len(goldenIDs) {
			var got []string
			for i, c := range res.Candidates {
				if i >= 5 {
					break
				}
				got = append(got, c.Chunk.SectionPath)
			}
			s.Details = append(s.Details, fmt.Sprintf("%s: %d/%d golden chunks in top5; got %s", g.Code, found, len(goldenIDs), strings.Join(got, " | ")))
		}
	}
	s.Metrics["retrieval_recall_at_5"] = ratio(hits, total)
	s.Metrics["golden_chunks"] = float64(total)
	s.Passed = passes(s.Metrics)
	return s
}

// answersSuite generates every answer, then measures citations, unsupported claims,
// trap refusal and fact checks; the library suite approves them and measures reuse on
// the second synthetic RFP (report only).
func answersSuite(ctx context.Context, a *app.App, seeder *demo.Seeder, pack *verticals.Pack, orgID, bidID, qBidID string) (Suite, Suite) {
	s := Suite{Name: "answers", Metrics: map[string]float64{}}
	lib := Suite{Name: "library", Metrics: map[string]float64{}, Passed: true}
	reqs, _ := a.DB.ListRequirements(bidID)
	byCode := map[string]store.Requirement{}
	for _, r := range reqs {
		byCode[r.Code] = r
	}
	citedOK, citedTotal, unsupported, factual, traps, refused, mustHit, mustTotal, falseClaims := 0, 0, 0, 0, 0, 0, 0, 0, 0
	reviewer := ""
	if users, _ := a.DB.UsersByRole(orgID, store.RoleReviewer); len(users) > 0 {
		reviewer = users[0].ID
	}
	for _, g := range pack.Demo.RFP.AllItems() {
		r, ok := byCode[g.Code]
		if !ok || pack.IsInstruction(g.Category) {
			continue
		}
		res, err := a.GenerateAnswer(ctx, orgID, r.ID, app.GenerateOptions{Force: true, Mode: "eval"})
		if err != nil {
			s.Details = append(s.Details, g.Code+": generate error "+err.Error())
			continue
		}
		ans := res.Answer
		text := ans.DraftText
		if g.Trap {
			traps++
			if ans.Status == store.StatusNeedsEvidence {
				refused++
			} else {
				s.Details = append(s.Details, fmt.Sprintf("trap %s NOT refused: %s (%s)", g.Code, ans.Status, ans.ConfidenceLabel))
			}
			for _, bad := range g.MustNotClaim {
				if ans.Status != store.StatusNeedsEvidence && strings.Contains(strings.ToLower(text), strings.ToLower(bad)) {
					falseClaims++
					s.Details = append(s.Details, fmt.Sprintf("%s claims %q", g.Code, bad))
				}
			}
			continue
		}
		var v ai.Verification
		_ = json.Unmarshal([]byte(ans.Verification), &v)
		unsupported += v.Unsupported
		factual += v.Factual
		golden := map[string]bool{}
		for _, ref := range g.Evidence {
			if id, ok := seeder.ResolveChunk(orgID, ref); ok {
				golden[id] = true
			}
		}
		cites, _ := a.DB.CitationsForAnswer(ans.ID)
		for _, c := range cites {
			citedTotal++
			if golden[c.ChunkID] {
				citedOK++
			} else {
				s.Details = append(s.Details, fmt.Sprintf("%s cites non-golden chunk %s (%s)", g.Code, c.DocName, c.SectionPath))
			}
		}
		if len(cites) == 0 {
			s.Details = append(s.Details, fmt.Sprintf("%s has no citations (status %s)", g.Code, ans.Status))
		}
		for _, must := range g.MustContain {
			mustTotal++
			if retrieval.ContainsTerm(text, must) {
				mustHit++
			} else {
				s.Details = append(s.Details, fmt.Sprintf("%s missing expected fact %q", g.Code, must))
			}
		}
		for _, bad := range g.MustNotClaim {
			if strings.Contains(strings.ToLower(text), strings.ToLower(bad)) {
				falseClaims++
				s.Details = append(s.Details, fmt.Sprintf("%s claims %q", g.Code, bad))
			}
		}
		// approve verified answers for the library flywheel
		if ans.UnsupportedCount == 0 && ans.Status != store.StatusNeedsEvidence && ans.Status != store.StatusNeedsSME {
			_ = a.ApproveAnswer(orgID, reviewer, ans.ID, "eval approval")
		}
	}
	s.Metrics["citation_precision"] = ratio(citedOK, citedTotal)
	s.Metrics["unsupported_claim_rate"] = 0
	if factual > 0 {
		s.Metrics["unsupported_claim_rate"] = float64(unsupported) / float64(factual)
	}
	s.Metrics["trap_refusal"] = ratio(refused, traps)
	s.Metrics["trap_count"] = float64(traps)
	s.Metrics["fact_recall"] = ratio(mustHit, mustTotal)
	s.Metrics["false_claims"] = float64(falseClaims)
	s.Passed = passes(s.Metrics)
	// library reuse on the questionnaire bid
	qreqs, _ := a.DB.ListRequirements(qBidID)
	reused, generated := 0, 0
	for _, r := range qreqs {
		res, err := a.GenerateAnswer(ctx, orgID, r.ID, app.GenerateOptions{Force: true, Mode: "eval"})
		if err != nil {
			continue
		}
		generated++
		if res.Reused {
			reused++
			lib.Details = append(lib.Details, r.Code+" reused: "+truncate(res.LibraryEntry.CanonicalQuestion, 70))
		}
	}
	entries, _ := a.DB.ListLibrary(orgID)
	lib.Metrics["library_entries"] = float64(len(entries))
	lib.Metrics["library_reuse_rate"] = 0
	if generated > 0 {
		lib.Metrics["library_reuse_rate"] = float64(reused) / float64(generated)
	}
	lib.Metrics["second_rfp_questions"] = float64(generated)
	return s, lib
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Markdown renders a report summary.
func Markdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval report — %s (%s mode)\n\n", r.Pack, r.ProviderMode)
	fmt.Fprintf(&b, "Created %s · duration %s · overall: %s\n\n", r.CreatedAt, r.Duration, passFail(r.Passed))
	b.WriteString("| Suite | Metric | Value | Target | Result |\n|---|---|---|---|---|\n")
	for _, s := range r.Suites {
		keys := make([]string, 0, len(s.Metrics))
		for k := range s.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t, has := Targets[k]
			target, result := "report only", "—"
			if has {
				if upperBound[k] {
					target = fmt.Sprintf("≤ %.2f", t)
					result = passFail(s.Metrics[k] <= t+1e-9)
				} else {
					target = fmt.Sprintf("≥ %.2f", t)
					result = passFail(s.Metrics[k] >= t-1e-9)
				}
			}
			fmt.Fprintf(&b, "| %s | %s | %.3f | %s | %s |\n", s.Name, k, s.Metrics[k], target, result)
		}
	}
	for _, s := range r.Suites {
		if len(s.Details) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s details\n\n", s.Name)
		for _, d := range s.Details {
			b.WriteString("- " + d + "\n")
		}
	}
	return b.String()
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// writeGolden materialises the golden set from the pack (single source of truth).
func writeGolden(pack *verticals.Pack, dir string) {
	_ = os.MkdirAll(dir, 0o755)
	type req struct {
		Code, Text, Category string
		Mandatory, Trap      bool
		SourceSpan           string
	}
	var reqs []req
	evidence := map[string][]string{}
	answers := map[string]map[string][]string{}
	for _, it := range pack.Demo.RFP.AllItems() {
		reqs = append(reqs, req{Code: it.Code, Text: it.Text, Category: it.Category, Mandatory: it.Mandatory, Trap: it.Trap, SourceSpan: it.Code + " " + it.Text})
		evidence[it.Code] = it.Evidence
		if evidence[it.Code] == nil {
			evidence[it.Code] = []string{}
		}
		answers[it.Code] = map[string][]string{"mustContain": nz(it.MustContain), "mustNotClaim": nz(it.MustNotClaim)}
	}
	write := func(name string, v any) {
		b, _ := json.MarshalIndent(v, "", "  ")
		_ = os.WriteFile(filepath.Join(dir, name), b, 0o644)
	}
	write("requirements.json", reqs)
	write("evidence.json", evidence)
	write("answers.json", answers)
	if len(pack.Demo.Questionnaire.Rows) > 0 {
		write("questionnaire.json", pack.Demo.Questionnaire.Rows)
	}
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
