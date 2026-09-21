package ai

import (
	"regexp"
	"strings"

	"bidos/internal/retrieval"
)

// Sentence is the verification result for one sentence of a draft.
type Sentence struct {
	Text    string   `json:"text"`
	Factual bool     `json:"factual"`
	Markers []int    `json:"markers"`
	Status  string   `json:"status"` // supported | unsupported | connective
	Reason  string   `json:"reason,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

// Verification is the deterministic claim-verifier output stored on the answer.
type Verification struct {
	Sentences   []Sentence `json:"sentences"`
	Unsupported int        `json:"unsupported"`
	Factual     int        `json:"factual"`
	Cited       int        `json:"cited"`
	Threshold   float64    `json:"threshold"`
	ExtraFlags  []string   `json:"extraFlags,omitempty"` // from an optional live-model second opinion
}

var (
	reMarker      = regexp.MustCompile(`\[c(\d+)\]`)
	reFactualHint = regexp.MustCompile(`(?i)\b(support|supports|provide|provides|offer|offers|deliver|delivers|maintain|maintains|encrypt|encrypts|comply|complies|certified|certification|hold|holds|operate|operates|include|includes|use|uses|has|have|is|are|located|available|perform|performs|commit|commits|retain|retains|notif|guarantee|meets|achieved|deployed|hosted|run|runs)\b`)
	reNumberish   = regexp.MustCompile(`\d`)
	reConnective  = regexp.MustCompile(`(?i)^(in summary|in addition|additionally|furthermore|please|we are pleased|thank you|as described|as noted|see |this section|the following|no approved evidence|evidence is insufficient|review required|subject matter expert)`)
)

// Verify checks every factual sentence of a draft against its cited chunks.
// A factual sentence must carry at least one [c#] marker; every number, date/percentage
// and hard term in the sentence must appear in a cited chunk; and lexical overlap with a
// cited chunk must reach the threshold.
func Verify(draft string, chunks map[int]string, threshold float64, aliases []string) Verification {
	if threshold <= 0 {
		threshold = 0.5
	}
	v := Verification{Threshold: threshold}
	for _, raw := range retrieval.SplitSentences(draft) {
		s := Sentence{Text: raw}
		for _, m := range reMarker.FindAllStringSubmatch(raw, -1) {
			n := 0
			for _, ch := range m[1] {
				n = n*10 + int(ch-'0')
			}
			s.Markers = append(s.Markers, n)
		}
		clean := strings.TrimSpace(reMarker.ReplaceAllString(raw, ""))
		s.Factual = isFactual(clean, aliases)
		if !s.Factual {
			s.Status = "connective"
			v.Sentences = append(v.Sentences, s)
			continue
		}
		v.Factual++
		if len(s.Markers) == 0 {
			s.Status, s.Reason = "unsupported", "factual sentence has no citation"
			v.Unsupported++
			v.Sentences = append(v.Sentences, s)
			continue
		}
		v.Cited++
		hard := retrieval.HardTerms(clean, aliases)
		terms := retrieval.Terms(clean, aliases)
		best := 0.0
		var missing []string
		supported := false
		for _, m := range s.Markers {
			chunk, ok := chunks[m]
			if !ok {
				continue
			}
			var miss []string
			for _, h := range hard {
				if !retrieval.ContainsTerm(chunk, h) {
					miss = append(miss, h)
				}
			}
			cov := retrieval.Coverage(terms, retrieval.Terms(chunk, aliases))
			if cov > best {
				best = cov
			}
			if len(miss) == 0 && cov >= threshold {
				supported = true
				break
			}
			if len(missing) == 0 || len(miss) < len(missing) {
				missing = miss
			}
		}
		if supported {
			s.Status = "supported"
		} else {
			s.Status = "unsupported"
			s.Missing = missing
			switch {
			case len(missing) > 0:
				s.Reason = "cited evidence does not contain: " + strings.Join(missing, ", ")
			default:
				s.Reason = "wording overlaps too little with the cited evidence"
			}
			v.Unsupported++
		}
		v.Sentences = append(v.Sentences, s)
	}
	return v
}

func isFactual(sentence string, aliases []string) bool {
	if reConnective.MatchString(sentence) {
		return false
	}
	if reNumberish.MatchString(sentence) {
		return true
	}
	if len(retrieval.HardTerms(sentence, aliases)) > 0 {
		return true
	}
	return reFactualHint.MatchString(sentence)
}

// Label derives the evidence label from verification + retrieval facts (criteria live here,
// not in prompts, and never as probabilities):
//
//	Strong:       every factual sentence verified and (≥2 distinct approved sources or a direct source)
//	Moderate:     every factual sentence verified, single indirect source
//	Weak:         unsupported sentences were removed, or cited evidence has expired
//	Insufficient: no qualifying evidence
func Label(v Verification, distinctDocs int, direct bool, removedUnsupported bool, expiredCitation bool, sufficient bool) string {
	if !sufficient {
		return "Insufficient evidence"
	}
	if v.Unsupported > 0 {
		return "Weak evidence"
	}
	if removedUnsupported || expiredCitation {
		return "Weak evidence"
	}
	if distinctDocs >= 2 || direct {
		return "Strong evidence"
	}
	return "Moderate evidence"
}

// StripUnsupported removes unsupported sentences from a draft (used for "clean" copies).
func StripUnsupported(v Verification) string {
	var keep []string
	for _, s := range v.Sentences {
		if s.Status != "unsupported" {
			keep = append(keep, s.Text)
		}
	}
	return strings.Join(keep, " ")
}
