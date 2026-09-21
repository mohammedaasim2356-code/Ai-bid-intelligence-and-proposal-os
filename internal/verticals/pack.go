// Package verticals loads industry configuration packs (categories, sections,
// qualification rubric, knowledge types, demo data, guardrails) from embedded files.
// Domain code never branches on an industry; it reads the active pack.
package verticals

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed packs
var packFS embed.FS

type Category struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Keywords    []string `json:"keywords"`
	OwnerRole   string   `json:"ownerRole"`
	Instruction bool     `json:"instruction"` // buyer instructions: acknowledged, not evidenced
}

type SectionTemplate struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Hint  string `json:"hint"`
}

type Criterion struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Weight      float64 `json:"weight"`
	Description string  `json:"description"`
	HumanOnly   bool    `json:"humanOnly"`
}

type KnowledgeType struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	ExpiryMonths int    `json:"expiryMonths"`
}

type Guardrails struct {
	ClientDisclosure bool     `json:"clientDisclosure"`
	NoLegalAdvice    bool     `json:"noLegalAdvice"`
	ConflictsGate    bool     `json:"conflictsGate"`
	QAExtraChecks    []string `json:"qaExtraChecks"`
}

type DemoUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	Title string `json:"title"`
}

type Company struct {
	Name        string            `json:"name"`
	Tagline     string            `json:"tagline"`
	Users       []DemoUser        `json:"users"`
	Routing     map[string]string `json:"routing"` // category → user email
	Settings    map[string]string `json:"settings"`
	NameAliases []string          `json:"nameAliases"`
}

type RFPItem struct {
	Code         string   `json:"code"`
	Text         string   `json:"text"`
	Category     string   `json:"category"`
	Mandatory    bool     `json:"mandatory"`
	Trap         bool     `json:"trap"`
	Evidence     []string `json:"evidence"`
	MustContain  []string `json:"mustContain"`
	MustNotClaim []string `json:"mustNotClaim"`
	Note         string   `json:"note"`
}

type RFPSection struct {
	Number string    `json:"number"`
	Title  string    `json:"title"`
	Intro  []string  `json:"intro"`
	Items  []RFPItem `json:"items"`
	Outro  []string  `json:"outro"`
}

type RFP struct {
	ID             string       `json:"id"`
	Title          string       `json:"title"`
	Buyer          string       `json:"buyer"`
	BuyerURL       string       `json:"buyerUrl"`
	Deadline       string       `json:"deadline"`
	EstimatedValue float64      `json:"estimatedValue"`
	Description    string       `json:"description"`
	Intro          []string     `json:"intro"`
	Sections       []RFPSection `json:"sections"`
	Injection      string       `json:"injection"`
	FileName       string       `json:"fileName"`
}

type QuestionnaireRow struct {
	ID        string   `json:"id"`
	Domain    string   `json:"domain"`
	Question  string   `json:"question"`
	Category  string   `json:"category"`
	Trap      bool     `json:"trap"`
	Evidence  []string `json:"evidence"`
	Injection bool     `json:"injection"`
}

type Questionnaire struct {
	Title     string             `json:"title"`
	Sheet     string             `json:"sheet"`
	Columns   []string           `json:"columns"`
	AnswerCol string             `json:"answerCol"`
	FileName  string             `json:"fileName"`
	Rows      []QuestionnaireRow `json:"rows"`
}

type Addendum struct {
	Title      string   `json:"title"`
	FileName   string   `json:"fileName"`
	Paragraphs []string `json:"paragraphs"`
	Expected   struct {
		Changed []string `json:"changed"`
		New     []string `json:"new"`
		Removed []string `json:"removed"`
	} `json:"expected"`
}

type KnowledgeDoc struct {
	Key              string   `json:"key"`
	File             string   `json:"file"`
	Title            string   `json:"title"`
	Category         string   `json:"category"`
	Version          int      `json:"version"`
	Approval         string   `json:"approval"`
	ExpiresAt        string   `json:"expiresAt"`
	Tags             []string `json:"tags"`
	ClientDisclosure string   `json:"clientDisclosure"`
	ClientName       string   `json:"clientName"`
	Content          string   `json:"-"`
}

type Demo struct {
	Company       Company        `json:"company"`
	RFP           RFP            `json:"rfp"`
	Questionnaire Questionnaire  `json:"questionnaire"`
	Addendum      Addendum       `json:"addendum"`
	Knowledge     []KnowledgeDoc `json:"knowledge"`
	Library       []LibrarySeed  `json:"library"`
}

// LibrarySeed pre-populates the Answer Library (e.g. an expired entry for hygiene demos).
type LibrarySeed struct {
	Question   string   `json:"question"`
	Answer     string   `json:"answer"`
	Category   string   `json:"category"`
	Evidence   []string `json:"evidence"`
	ApprovedAt string   `json:"approvedAt"`
	ReviewBy   string   `json:"reviewBy"`
	Tags       []string `json:"tags"`
}

type Pack struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	Description    string            `json:"description"`
	Categories     []Category        `json:"-"`
	Sections       []SectionTemplate `json:"-"`
	Qualification  []Criterion       `json:"-"`
	KnowledgeTypes []KnowledgeType   `json:"-"`
	Guardrails     Guardrails        `json:"guardrails"`
	Prompts        map[string]string `json:"-"`
	Demo           Demo              `json:"-"`
}

// IDs lists the embedded pack ids.
func IDs() []string {
	entries, err := packFS.ReadDir("packs")
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// Load reads a pack by id.
func Load(id string) (*Pack, error) {
	base := path.Join("packs", id)
	p := &Pack{Prompts: map[string]string{}}
	if err := readJSON(path.Join(base, "pack.json"), p); err != nil {
		return nil, fmt.Errorf("vertical pack %q: %w", id, err)
	}
	must := []struct {
		file string
		dst  any
	}{
		{"categories.json", &p.Categories},
		{"sections.json", &p.Sections},
		{"qualification.json", &p.Qualification},
		{"knowledge-types.json", &p.KnowledgeTypes},
		{"demo/company.json", &p.Demo.Company},
		{"demo/rfp.json", &p.Demo.RFP},
		{"demo/knowledge.json", &p.Demo.Knowledge},
	}
	for _, m := range must {
		if err := readJSON(path.Join(base, m.file), m.dst); err != nil {
			return nil, fmt.Errorf("vertical pack %q: %s: %w", id, m.file, err)
		}
	}
	_ = readJSON(path.Join(base, "demo/questionnaire.json"), &p.Demo.Questionnaire)
	_ = readJSON(path.Join(base, "demo/addendum.json"), &p.Demo.Addendum)
	_ = readJSON(path.Join(base, "demo/library.json"), &p.Demo.Library)
	for i := range p.Demo.Knowledge {
		b, err := packFS.ReadFile(path.Join(base, "demo/knowledge", p.Demo.Knowledge[i].File))
		if err != nil {
			return nil, fmt.Errorf("vertical pack %q: knowledge file %s: %w", id, p.Demo.Knowledge[i].File, err)
		}
		p.Demo.Knowledge[i].Content = strings.ReplaceAll(string(b), "\r\n", "\n")
	}
	// optional prompt overrides: prompts/<promptId>.txt
	if entries, err := fs.ReadDir(packFS, path.Join(base, "prompts")); err == nil {
		for _, e := range entries {
			if b, err := packFS.ReadFile(path.Join(base, "prompts", e.Name())); err == nil {
				p.Prompts[strings.TrimSuffix(e.Name(), ".txt")] = string(b)
			}
		}
	}
	if p.ID == "" {
		p.ID = id
	}
	return p, nil
}

func readJSON(name string, dst any) error {
	b, err := packFS.ReadFile(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// Category returns the category by id (or a generic fallback).
func (p *Pack) Category(id string) Category {
	for _, c := range p.Categories {
		if c.ID == id {
			return c
		}
	}
	return Category{ID: "general", Label: "General"}
}

// CategoryLabel is a template helper.
func (p *Pack) CategoryLabel(id string) string { return p.Category(id).Label }

// IsInstruction reports whether a category is a buyer instruction (acknowledged, not evidenced).
func (p *Pack) IsInstruction(categoryID string) bool { return p.Category(categoryID).Instruction }

// Classify picks the best category for a text by keyword hits; falls back to "general".
func (p *Pack) Classify(text string) string {
	lower := " " + strings.ToLower(text) + " "
	best, bestScore := "general", 0
	for _, c := range p.Categories {
		score := 0
		for _, k := range c.Keywords {
			if KeywordHit(lower, strings.ToLower(k)) {
				score += 1 + len(strings.Fields(k)) // multi-word hints weigh more
			}
		}
		if score > bestScore {
			best, bestScore = c.ID, score
		}
	}
	return best
}

// KeywordHit reports whether keyword k occurs in lower starting at a word boundary
// (keywords may be stems, so only the start is bounded: "cv" matches "CVs", not "pcv").
func KeywordHit(lower, k string) bool {
	for i := strings.Index(lower, k); i >= 0; {
		if i == 0 || !isWordByte(lower[i-1]) {
			return true
		}
		j := strings.Index(lower[i+1:], k)
		if j < 0 {
			return false
		}
		i += j + 1
	}
	return false
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b >= 0x80
}

// AllItems flattens the demo RFP items.
func (r *RFP) AllItems() []RFPItem {
	var out []RFPItem
	for _, s := range r.Sections {
		out = append(out, s.Items...)
	}
	return out
}

// ExpiryMonths returns the default expiry for a knowledge category (0 = none).
func (p *Pack) ExpiryMonths(category string) int {
	for _, k := range p.KnowledgeTypes {
		if k.ID == category {
			return k.ExpiryMonths
		}
	}
	return 0
}
