package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Typed task inputs/outputs. Outputs are validated after parsing; a validation error
// triggers exactly one repair attempt on the same provider.

type EvidenceItem struct {
	Marker   int    `json:"marker"`
	Document string `json:"document"`
	Section  string `json:"section"`
	Text     string `json:"text"`
}

type DraftInput struct {
	Company     string         `json:"company"`
	Requirement string         `json:"requirement"`
	Category    string         `json:"category"`
	Evidence    []EvidenceItem `json:"evidence"`
	Gap         string         `json:"gap"`
	Instruction bool           `json:"instruction"`
}

type DraftOutput struct {
	Answer        string `json:"answer"`
	NeedsEvidence bool   `json:"needsEvidence"`
	SMEQuestion   string `json:"smeQuestion"`
	EvidenceGaps  string `json:"evidenceGaps"`
}

func (o DraftOutput) validate(in DraftInput) error {
	if strings.TrimSpace(o.Answer) == "" {
		return errors.New("answer is empty")
	}
	if !o.NeedsEvidence && len(in.Evidence) > 0 && !strings.Contains(o.Answer, "[c") {
		return errors.New("answer has no [c#] citation markers")
	}
	for _, m := range reMarker.FindAllStringSubmatch(o.Answer, -1) {
		n := 0
		fmt.Sscanf(m[1], "%d", &n)
		if n < 1 || n > len(in.Evidence) {
			return fmt.Errorf("citation marker [c%d] does not exist (evidence has %d items)", n, len(in.Evidence))
		}
	}
	return nil
}

type CategoryRef struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Keywords []string `json:"keywords,omitempty"`
}

type ClassifyInput struct {
	Text       string        `json:"text"`
	Categories []CategoryRef `json:"categories"`
}

type ClassifyOutput struct {
	Category  string `json:"category"`
	Mandatory bool   `json:"mandatory"`
}

func (o ClassifyOutput) validate(in ClassifyInput) error {
	for _, c := range in.Categories {
		if c.ID == o.Category {
			return nil
		}
	}
	return fmt.Errorf("category %q is not one of the allowed ids", o.Category)
}

type ExtractCandidate struct {
	Code       string `json:"code"`
	Text       string `json:"text"`
	SourceSpan string `json:"sourceSpan"`
	Section    string `json:"section"`
	Category   string `json:"category,omitempty"`
	Mandatory  bool   `json:"mandatory"`
}

type ExtractInput struct {
	DocumentName string             `json:"documentName"`
	Categories   []CategoryRef      `json:"categories"`
	Candidates   []ExtractCandidate `json:"candidates"`
	Text         string             `json:"text"`
}

type ExtractOutput struct {
	Items []ExtractCandidate `json:"items"`
}

func (o ExtractOutput) validate(in ExtractInput) error {
	if len(o.Items) == 0 {
		return errors.New("no items returned")
	}
	allowed := map[string]bool{}
	for _, c := range in.Categories {
		allowed[c.ID] = true
	}
	for i, it := range o.Items {
		if strings.TrimSpace(it.Text) == "" || strings.TrimSpace(it.SourceSpan) == "" {
			return fmt.Errorf("item %d is missing text or sourceSpan", i)
		}
		if it.Category != "" && !allowed[it.Category] {
			return fmt.Errorf("item %d has unknown category %q", i, it.Category)
		}
	}
	return nil
}

type VerifyInput struct {
	Draft    string         `json:"draft"`
	Evidence []EvidenceItem `json:"evidence"`
}

type VerifyFlag struct {
	Sentence string `json:"sentence"`
	Reason   string `json:"reason"`
}

type VerifyOutput struct {
	Flags []VerifyFlag `json:"flags"`
}

type SMEQuestionInput struct {
	Company      string   `json:"company"`
	Requirement  string   `json:"requirement"`
	Category     string   `json:"category"`
	Gap          string   `json:"gap"`
	MissingTerms []string `json:"missingTerms"`
}

type SMEQuestionOutput struct {
	Question string `json:"question"`
}

type SummarizeInput struct {
	Text string `json:"text"`
}

type SummarizeOutput struct {
	Summary string `json:"summary"`
}

// decodeStrict parses JSON into a typed value, rejecting empty or non-object payloads.
func decodeStrict(raw json.RawMessage, dst any) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return errors.New("output is not a JSON object")
	}
	return json.Unmarshal(raw, dst)
}
