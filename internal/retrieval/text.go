// Package retrieval implements hybrid evidence retrieval: BM25 lexical ranking,
// local hashed embeddings (or a remote embeddings endpoint) with cosine similarity,
// Reciprocal Rank Fusion, approval/expiry filters and a deterministic evidence gate.
package retrieval

import (
	"regexp"
	"strings"
	"unicode"
)

// Stopwords are generic RFP/proposal vocabulary that carries no evidence value.
var Stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an the and or of for to in with at by on from as be is are was were been that this these those its their per etc such if not no our we you your yours may can could do does did how what which where when who whom will shall must should would please describe provide confirm whether list state identify detail explain outline specify indicate include including submit attach any all each every least following information requirement requirements response responses proposal proposals vendor vendors supplier bidder proposer solution solutions platform system systems service services provider currently offer offers e.g i.e via within also than then there here into over under about between other more most less both either neither only own same so too very just being have has had having ability able using used use`) {
		Stopwords[w] = true
	}
}

var reToken = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}.,%+\-']*`)

// Tokenize lower-cases and splits text into tokens, trimming punctuation at the ends.
func Tokenize(s string) []string {
	var out []string
	for _, m := range reToken.FindAllString(strings.ToLower(s), -1) {
		m = strings.Trim(m, ".,'-")
		if m == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Stem applies a light suffix strip so "encrypted" ~ "encryption" via prefix matching.
func Stem(w string) string {
	if len(w) <= 3 {
		return w
	}
	if strings.HasSuffix(w, "ies") && len(w) > 4 {
		return w[:len(w)-3] + "y"
	}
	for _, suf := range []string{"ing", "ed", "es", "ly"} {
		if strings.HasSuffix(w, suf) && len(w)-len(suf) >= 4 {
			return strings.TrimSuffix(w, suf)
		}
	}
	if strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is") {
		return strings.TrimSuffix(w, "s")
	}
	return w
}

// StemMatch reports whether two stems refer to the same word family.
func StemMatch(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) >= 5 && len(b) >= 5 {
		return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
	}
	return false
}

// Terms returns stemmed content tokens (stopwords and aliases removed).
func Terms(s string, aliases []string) []string {
	s = stripAliases(s, aliases)
	var out []string
	for _, t := range Tokenize(s) {
		if Stopwords[t] || len(t) < 2 {
			continue
		}
		out = append(out, Stem(t))
	}
	return out
}

func stripAliases(s string, aliases []string) string {
	for _, a := range aliases {
		if a == "" {
			continue
		}
		s = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(a)+`('s)?\b`).ReplaceAllString(s, " ")
	}
	return s
}

// HardTerms are specific identifiers a chunk must contain to count as evidence:
// acronyms, tokens with digits, and capitalised words that are not sentence-initial.
func HardTerms(s string, aliases []string) []string {
	s = stripAliases(s, aliases)
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.Trim(t, ".,;:()\"'")
		key := strings.ToLower(t)
		if t == "" || seen[key] || Stopwords[key] {
			return
		}
		seen[key] = true
		out = append(out, t)
	}
	sentences := regexp.MustCompile(`[.!?]\s+`).Split(s, -1)
	for _, sent := range sentences {
		words := strings.Fields(sent)
		for i, w := range words {
			w = strings.Trim(w, "()\"',;:")
			if w == "" {
				continue
			}
			hasDigit := strings.ContainsAny(w, "0123456789")
			letters := 0
			upper := 0
			for _, r := range w {
				if unicode.IsLetter(r) {
					letters++
					if unicode.IsUpper(r) {
						upper++
					}
				}
			}
			switch {
			case hasDigit && !strings.HasPrefix(strings.ToLower(w), "e.g"):
				add(strings.TrimRight(w, "."))
			case letters >= 2 && upper == letters: // acronym
				add(w)
			case i > 0 && letters >= 3 && unicode.IsUpper([]rune(w)[0]) && upper < letters:
				// capitalised non-initial word (proper noun) unless the previous word ends a clause
				prev := words[i-1]
				if strings.HasSuffix(prev, ":") || strings.HasSuffix(prev, ";") {
					continue
				}
				add(w)
			}
		}
	}
	return out
}

// ContainsTerm checks a hard term against text, tolerant of plural/possessive and case.
func ContainsTerm(text, term string) bool {
	lt := strings.ToLower(text)
	t := strings.ToLower(strings.TrimRight(term, ".,;:"))
	if t == "" {
		return true
	}
	if strings.Contains(lt, t) {
		return true
	}
	if strings.HasSuffix(t, "s") && len(t) > 2 && strings.Contains(lt, strings.TrimSuffix(t, "s")) {
		return true
	}
	if strings.Contains(lt, t+"s") {
		return true
	}
	// numbers: "5,000" vs "5000"
	if strings.ContainsAny(t, "0123456789") {
		n := strings.ReplaceAll(t, ",", "")
		if strings.Contains(strings.ReplaceAll(lt, ",", ""), n) {
			return true
		}
	}
	return false
}

// Coverage returns the share of query terms present (by stem family) in the text terms.
func Coverage(queryTerms, textTerms []string) float64 {
	if len(queryTerms) == 0 {
		return 0
	}
	hit := 0
	for _, q := range queryTerms {
		for _, t := range textTerms {
			if StemMatch(q, t) {
				hit++
				break
			}
		}
	}
	return float64(hit) / float64(len(queryTerms))
}
