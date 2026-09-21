package ai

import (
	"bidos/internal/verticals"
	"fmt"
	"strings"

	"bidos/internal/retrieval"
)

// DemoProvider is the deterministic floor: template-based, evidence-injecting, never
// calling a model. Its drafts cite verbatim evidence sentences so the claim verifier
// can verify them, and it refuses (needsEvidence) when no evidence is supplied.

// DemoDraft composes a grounded draft from evidence excerpts.
func DemoDraft(in DraftInput) DraftOutput {
	if in.Instruction {
		return DraftOutput{Answer: "Acknowledged. This instruction will be followed as stated: " + strings.TrimSpace(in.Requirement)}
	}
	if len(in.Evidence) == 0 {
		gap := in.Gap
		if gap == "" {
			gap = "No approved evidence was found for this requirement."
		}
		return DraftOutput{
			Answer:        "No approved evidence was found for this requirement. Review required before a position can be stated. " + gap,
			NeedsEvidence: true,
			SMEQuestion:   DemoSMEQuestion(SMEQuestionInput{Company: in.Company, Requirement: in.Requirement, Category: in.Category, Gap: gap}).Question,
			EvidenceGaps:  gap,
		}
	}
	var b strings.Builder
	b.WriteString("The following approved sources address this requirement. ")
	used := 0
	for _, ev := range in.Evidence {
		sentences := retrieval.SplitSentences(ev.Text)
		count := 0
		for _, s := range sentences {
			s = strings.TrimSpace(s)
			if s == "" || strings.HasSuffix(s, "…") || len(s) < 20 {
				continue
			}
			b.WriteString(strings.TrimRight(s, ".") + " [c" + fmt.Sprint(ev.Marker) + "]. ")
			count++
			used++
			if count >= 2 {
				break
			}
		}
	}
	if used == 0 {
		ev := in.Evidence[0]
		b.WriteString(strings.TrimRight(strings.TrimSuffix(ev.Text, "…"), ".") + " [c" + fmt.Sprint(ev.Marker) + "]. ")
	}
	return DraftOutput{Answer: strings.TrimSpace(b.String())}
}

// DemoSMEQuestion writes a specific question for the routed expert.
func DemoSMEQuestion(in SMEQuestionInput) SMEQuestionOutput {
	company := in.Company
	if company == "" {
		company = "our company"
	}
	q := fmt.Sprintf("Can %s meet the following requirement, and is there an approved document we can cite? \"%s\"", company, strings.TrimSpace(in.Requirement))
	if len(in.MissingTerms) > 0 {
		q += " No approved source mentions: " + strings.Join(in.MissingTerms, ", ") + "."
	} else if in.Gap != "" {
		q += " " + in.Gap
	}
	q += " If the answer is no, please state the position we should communicate to the buyer."
	return SMEQuestionOutput{Question: q}
}

// DemoClassify picks the category by keyword hits (same rule as the vertical pack).
func DemoClassify(in ClassifyInput) ClassifyOutput {
	lower := " " + strings.ToLower(in.Text) + " "
	best, bestScore := "general", 0
	for _, c := range in.Categories {
		score := 0
		for _, k := range c.Keywords {
			if verticals.KeywordHit(lower, strings.ToLower(k)) {
				score += 1 + len(strings.Fields(k))
			}
		}
		if score > bestScore {
			best, bestScore = c.ID, score
		}
	}
	if best == "general" && len(in.Categories) > 0 {
		best = in.Categories[0].ID
	}
	mand := strings.Contains(lower, " shall ") || strings.Contains(lower, " must ") || strings.Contains(lower, "required")
	return ClassifyOutput{Category: best, Mandatory: mand}
}

// DemoExtract returns the rule-pass candidates unchanged (the rule pass is the
// deterministic extractor; the model pass only refines when a live provider exists).
func DemoExtract(in ExtractInput) ExtractOutput {
	return ExtractOutput{Items: in.Candidates}
}

// DemoSummarize returns the first two sentences.
func DemoSummarize(in SummarizeInput) SummarizeOutput {
	s := retrieval.SplitSentences(in.Text)
	if len(s) > 2 {
		s = s[:2]
	}
	return SummarizeOutput{Summary: strings.Join(s, " ")}
}
