// Package extract turns parsed buyer documents into span-anchored requirement
// candidates using deterministic rules. Document text is untrusted input: an injection
// filter drops instruction-like lines aimed at automated systems, and every candidate
// must be anchored verbatim in the source.
package extract

import (
	"fmt"
	"regexp"
	"strings"

	"bidos/internal/docs"
)

// Candidate is one extracted requirement before persistence.
type Candidate struct {
	Code       string
	Section    string
	Text       string
	SourceSpan string
	Locator    string // page/paragraph/cell locator
	Sheet      string
	Cell       string // answer cell for questionnaires (e.g. "D5")
	Mandatory  bool
	Kind       string // clause | question | row
	Anchored   bool
	Reason     string
}

// Report is the output of a rule pass.
type Report struct {
	Items      []Candidate
	Injections []string // dropped instruction-like lines
	Skipped    int
}

var (
	reCode        = regexp.MustCompile(`^\s*((?:[A-Z]{1,4}-)?\d+(?:\.\d+)*)[.)]?\s+(.*)$`)
	reMandatory   = regexp.MustCompile(`(?i)\b(shall|must|required|mandatory|is required to|are required to)\b`)
	reOptional    = regexp.MustCompile(`(?i)\b(should|may|optional|desirable|preferred|could)\b`)
	reWillSubject = regexp.MustCompile(`(?i)\b(vendor|vendors|supplier|suppliers|bidder|bidders|proposer|contractor|provider|respondent|offeror|solution|system|platform|proposal|proposals|service)\b[^.?!]{0,60}\b(will|should|may)\b`)
	reImperative  = regexp.MustCompile(`(?i)^(please\s+)?(describe|provide|explain|list|confirm|detail|indicate|state|identify|outline|specify|demonstrate|include|submit|attach|complete|define|document|summari[sz]e|present)\b`)
	reQuestion    = regexp.MustCompile(`\?\s*$`)
	reInjection   = regexp.MustCompile(`(?i)(ignore|disregard|forget|override)\s+(all\s+|any\s+|the\s+|your\s+)?(previous|prior|above|earlier|preceding|system|these)\s+(instructions?|rules?|prompts?|guidance)|note to (automated systems|ai assistants|language models)|system\s*(note|prompt)\s*:|answer yes to every|mark (every|all) requirements?`)
	reHeadingOnly = regexp.MustCompile(`^\s*(section|part|appendix)\b`)
)

// Rules runs the deterministic pass over a parsed document.
func Rules(doc *docs.ParsedDocument) Report {
	var rep Report
	if tbl := questionnaireTable(doc); tbl != nil {
		rep = rulesFromTable(doc, tbl)
		if len(rep.Items) > 0 {
			return rep
		}
	}
	full := docs.NormalizeWS(doc.FullText())
	seq := 0
	for _, s := range doc.Sections {
		if s.Kind == "heading" {
			continue
		}
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		if reInjection.MatchString(text) {
			rep.Injections = append(rep.Injections, text)
			continue
		}
		code, body := "", text
		if m := reCode.FindStringSubmatch(text); m != nil && !reHeadingOnly.MatchString(text) {
			code, body = m[1], strings.TrimSpace(m[2])
		}
		kind := classify(body)
		if kind == "" && code == "" {
			rep.Skipped++
			continue
		}
		if kind == "" && len(strings.Fields(body)) < 5 {
			rep.Skipped++
			continue
		}
		if code != "" && kind == "" {
			kind = "clause"
		}
		seq++
		if code == "" {
			code = fmt.Sprintf("R-%03d", seq)
		}
		c := Candidate{
			Code: code, Section: sectionName(s.Path), Text: body, SourceSpan: text,
			Locator: locator(s), Kind: kind, Mandatory: mandatory(body, kind),
		}
		c.Anchored = strings.Contains(full, docs.NormalizeWS(text))
		if !c.Anchored {
			c.Reason = "source span not found verbatim in document"
		}
		rep.Items = append(rep.Items, c)
	}
	return rep
}

func classify(body string) string {
	switch {
	case reQuestion.MatchString(body):
		return "question"
	case reImperative.MatchString(body):
		return "question"
	case reMandatory.MatchString(body):
		return "clause"
	case reWillSubject.MatchString(body):
		return "clause"
	}
	return ""
}

// reInquiry matches yes/no capability inquiries ("Confirm whether you hold…"): the buyer
// accepts "no", so they are optional unless a modal verb makes them mandatory.
var reInquiry = regexp.MustCompile(`(?i)^\s*(?:[0-9.]+\s+)?confirm\b`)

func mandatory(body, kind string) bool {
	if reMandatory.MatchString(body) {
		return true
	}
	if reOptional.MatchString(body) || reInquiry.MatchString(body) {
		return false
	}
	return kind == "question" || kind == "row"
}

func sectionName(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, " > ")
	return parts[len(parts)-1]
}

func locator(s docs.Section) string {
	switch {
	case s.Sheet != "" && s.Cell != "":
		return s.Sheet + "!" + s.Cell
	case s.Page > 0:
		return fmt.Sprintf("page %d, %s", s.Page, s.Cell)
	case s.Cell != "":
		return s.Cell
	}
	return s.Path
}

// questionnaireTable detects a sheet/table with a question column and an answer column.
func questionnaireTable(doc *docs.ParsedDocument) *docs.Table {
	for i := range doc.Tables {
		t := &doc.Tables[i]
		if qCol, aCol := questionColumns(t.Headers); qCol >= 0 && aCol >= 0 && len(t.Rows) > 0 {
			return t
		}
	}
	return nil
}

var reQHeader = regexp.MustCompile(`(?i)^(question|questions|requirement|requirements|control|item|criteria|description|query)`)
var reAHeader = regexp.MustCompile(`(?i)(answer|response|reply|vendor comment|supplier response)`)

func questionColumns(headers []string) (int, int) {
	q, a := -1, -1
	for i, h := range headers {
		h = strings.TrimSpace(h)
		if q < 0 && reQHeader.MatchString(h) {
			q = i
		}
		if a < 0 && reAHeader.MatchString(h) {
			a = i
		}
	}
	return q, a
}

func rulesFromTable(doc *docs.ParsedDocument, t *docs.Table) Report {
	var rep Report
	qCol, aCol := questionColumns(t.Headers)
	idCol := -1
	for i, h := range t.Headers {
		if regexp.MustCompile(`(?i)^(id|ref|no\.?|#|reference|code)$`).MatchString(strings.TrimSpace(h)) {
			idCol = i
		}
	}
	full := docs.NormalizeWS(doc.FullText())
	for ri, row := range t.Rows {
		if qCol >= len(row) {
			continue
		}
		q := strings.TrimSpace(row[qCol])
		if q == "" {
			continue
		}
		if reInjection.MatchString(q) {
			rep.Injections = append(rep.Injections, q)
			continue
		}
		if aCol < len(row) && strings.TrimSpace(row[aCol]) != "" {
			rep.Skipped++ // already answered
			continue
		}
		code := ""
		if idCol >= 0 && idCol < len(row) {
			code = strings.TrimSpace(row[idCol])
		}
		if code == "" {
			code = fmt.Sprintf("Q-%03d", ri+1)
		}
		rowNum := ri + 1
		if ri < len(t.RowNumbers) {
			rowNum = t.RowNumbers[ri]
		}
		var cell, loc string
		if t.DocxIndex >= 0 && strings.HasPrefix(t.Cols[0], "c") {
			cell = fmt.Sprintf("tbl%d/r%d/c%d", t.DocxIndex, rowNum, aCol)
			loc = fmt.Sprintf("%s, row %d", t.Sheet, rowNum)
		} else {
			cell = fmt.Sprintf("%s%d", docs.ColLetters(aCol), rowNum)
			loc = fmt.Sprintf("%s!%s%d", t.Sheet, docs.ColLetters(qCol), rowNum)
		}
		section := t.Sheet
		for i, h := range t.Headers {
			if regexp.MustCompile(`(?i)^(domain|section|category|area|topic)$`).MatchString(strings.TrimSpace(h)) && i < len(row) && row[i] != "" {
				section = row[i]
			}
		}
		c := Candidate{Code: code, Section: section, Text: q, SourceSpan: q, Locator: loc, Sheet: t.Sheet, Cell: cell, Kind: "row", Mandatory: mandatory(q, "row")}
		c.Anchored = strings.Contains(full, docs.NormalizeWS(q))
		if !c.Anchored {
			c.Reason = "source span not found verbatim in document"
		}
		rep.Items = append(rep.Items, c)
	}
	return rep
}

// AnchorCheck verifies a span (e.g. from a model pass) exists verbatim in the document.
func AnchorCheck(doc *docs.ParsedDocument, span string) bool {
	return strings.Contains(docs.NormalizeWS(doc.FullText()), docs.NormalizeWS(span))
}

// IsInjection exposes the injection filter for other callers (addenda, model output).
func IsInjection(text string) bool { return reInjection.MatchString(text) }

// AddendumChange is a parsed instruction from an addendum.
type AddendumChange struct {
	Code   string
	Action string // changed | new | removed
	Text   string
	Span   string
}

var (
	reAmend    = regexp.MustCompile(`(?i)^requirement\s+([A-Z]{0,4}-?\d+(?:\.\d+)*)\s+is\s+(amended|revised|replaced|modified)(?:\s+to\s+read)?\s*:?\s*(.+)$`)
	reNew      = regexp.MustCompile(`(?i)^new\s+requirement\s+([A-Z]{0,4}-?\d+(?:\.\d+)*)\s*:?\s*(.+)$`)
	reWithdraw = regexp.MustCompile(`(?i)^requirement\s+([A-Z]{0,4}-?\d+(?:\.\d+)*)\s+is\s+(withdrawn|removed|deleted|cancelled)\b`)
)

// AddendumChanges parses explicit change instructions; other requirement-like lines are
// returned through the regular rule pass by the caller.
func AddendumChanges(doc *docs.ParsedDocument) []AddendumChange {
	var out []AddendumChange
	for _, s := range doc.Sections {
		text := strings.TrimSpace(s.Text)
		if reInjection.MatchString(text) {
			continue
		}
		if m := reWithdraw.FindStringSubmatch(text); m != nil {
			out = append(out, AddendumChange{Code: m[1], Action: "removed", Span: text})
			continue
		}
		if m := reAmend.FindStringSubmatch(text); m != nil {
			out = append(out, AddendumChange{Code: m[1], Action: "changed", Text: strings.TrimSpace(m[3]), Span: text})
			continue
		}
		if m := reNew.FindStringSubmatch(text); m != nil {
			out = append(out, AddendumChange{Code: m[1], Action: "new", Text: strings.TrimSpace(m[2]), Span: text})
		}
	}
	return out
}
