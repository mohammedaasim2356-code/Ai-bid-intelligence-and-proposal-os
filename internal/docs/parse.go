// Package docs parses RFPs and knowledge files into one normalised representation
// with source locators, chunks them, and writes OOXML back (round-trip + export).
package docs

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Section is one addressable unit of a parsed document.
type Section struct {
	Path  string // heading trail, e.g. "4 Security > 4.2 Encryption"
	Text  string
	Page  int    // PDF page (1-based) or 0
	Sheet string // spreadsheet sheet name
	Cell  string // locator: "A5", "A5:D5", "p12", "tbl0/r3/c1"
	Index int
	Kind  string // heading | paragraph | row
}

// Table is a spreadsheet sheet, CSV, or DOCX table with cell-accurate locators.
type Table struct {
	Sheet      string
	Headers    []string
	HeaderRow  int // 1-based row number of the header row
	Rows       [][]string
	RowNumbers []int    // 1-based row numbers (spreadsheet) or row indexes (docx)
	Cols       []string // column letters (spreadsheet) or "c0".."cN" (docx)
	DocxIndex  int      // index of the table in a DOCX (locator prefix "tbl{i}")
}

// ParsedDocument is the normalised representation used by extraction and chunking.
type ParsedDocument struct {
	Name       string
	MimeType   string
	Sections   []Section
	Tables     []Table
	SourceHash string
	Metadata   map[string]any
	Pages      int
}

// FullText joins all section text (used for span anchoring).
func (d *ParsedDocument) FullText() string {
	var b strings.Builder
	for _, s := range d.Sections {
		b.WriteString(s.Text)
		b.WriteString("\n")
	}
	return b.String()
}

var ErrUnsupported = errors.New("unsupported file type")

// MimeFor maps an extension to a mime type.
func MimeFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".csv":
		return "text/csv"
	case ".md":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	}
	return "application/octet-stream"
}

// Sniff validates that the bytes look like the type the extension claims.
func Sniff(name string, data []byte) error {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".pdf":
		if !bytes.HasPrefix(data, []byte("%PDF")) {
			return errors.New("the file is not a valid PDF (missing %PDF header)")
		}
	case ".docx", ".xlsx":
		if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			return fmt.Errorf("the file is not a valid %s package (not a ZIP container)", strings.TrimPrefix(ext, "."))
		}
	case ".csv", ".txt", ".md":
		if !utf8.Valid(data) {
			return errors.New("the text file is not valid UTF-8; save it as UTF-8 and retry")
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return errors.New("the file contains binary data and cannot be read as text")
		}
	default:
		return fmt.Errorf("%w: %s (supported: PDF, DOCX, XLSX, CSV, TXT, MD)", ErrUnsupported, ext)
	}
	return nil
}

// Hash returns the sha256 hex digest of data.
func Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Parse dispatches on the file extension.
func Parse(name string, data []byte) (*ParsedDocument, error) {
	if err := Sniff(name, data); err != nil {
		return nil, err
	}
	var doc *ParsedDocument
	var err error
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		doc, err = ParsePDF(data)
	case ".docx":
		doc, err = ParseDocx(data)
	case ".xlsx":
		doc, err = ParseXlsx(data)
	case ".csv":
		doc, err = ParseCSV(data)
	default:
		doc, err = ParseText(string(data))
	}
	if err != nil {
		return nil, err
	}
	doc.Name = name
	doc.MimeType = MimeFor(name)
	doc.SourceHash = Hash(data)
	if doc.Metadata == nil {
		doc.Metadata = map[string]any{}
	}
	if len(doc.Sections) == 0 && len(doc.Tables) == 0 {
		return nil, errors.New("no readable text was found in the file (scanned images need OCR; protected files need an unlocked copy)")
	}
	return doc, nil
}

// Text / Markdown ---------------------------------------------------------------

var (
	reHeading  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	reNumbered = regexp.MustCompile(`^\s*(\d+(\.\d+)*)[.)]?\s+\S`)
	reBullet   = regexp.MustCompile(`^\s*([-*•]|\d+[.)])\s+`)
	reSetext   = regexp.MustCompile(`^(=+|-+)\s*$`)
)

// ParseText handles TXT and Markdown: headings become the section path,
// paragraphs (blank-line separated, or numbered clauses) become sections.
func ParseText(text string) (*ParsedDocument, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	doc := &ParsedDocument{}
	trail := []string{}
	var para []string
	idx := 0
	flush := func() {
		if len(para) == 0 {
			return
		}
		t := strings.TrimSpace(strings.Join(para, " "))
		para = nil
		if t == "" {
			return
		}
		doc.Sections = append(doc.Sections, Section{Path: strings.Join(trail, " > "), Text: t, Index: idx, Kind: "paragraph", Cell: fmt.Sprintf("p%d", idx)})
		idx++
	}
	for i, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		if m := reHeading.FindStringSubmatch(line); m != nil {
			flush()
			level := len(m[1])
			title := strings.TrimSpace(m[2])
			if level-1 < len(trail) {
				trail = trail[:level-1]
			}
			trail = append(trail, title)
			doc.Sections = append(doc.Sections, Section{Path: strings.Join(trail, " > "), Text: title, Index: idx, Kind: "heading", Cell: fmt.Sprintf("p%d", idx)})
			idx++
			continue
		}
		// setext heading: text line followed by === or ---
		if i+1 < len(lines) && reSetext.MatchString(lines[i+1]) && strings.TrimSpace(line) != "" && len(para) == 0 {
			flush()
			trail = []string{strings.TrimSpace(line)}
			doc.Sections = append(doc.Sections, Section{Path: strings.Join(trail, " > "), Text: strings.TrimSpace(line), Index: idx, Kind: "heading", Cell: fmt.Sprintf("p%d", idx)})
			idx++
			continue
		}
		if reSetext.MatchString(line) {
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if reNumbered.MatchString(line) || reBullet.MatchString(line) {
			flush()
		}
		para = append(para, strings.TrimSpace(line))
	}
	flush()
	return doc, nil
}

// ParseCSV treats the first row as headers and every following row as a section.
func ParseCSV(data []byte) (*ParsedDocument, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("the CSV could not be read: %v", err)
	}
	if len(records) == 0 {
		return nil, errors.New("the CSV file is empty")
	}
	doc := &ParsedDocument{}
	tbl := Table{Sheet: "Sheet1", Headers: records[0], HeaderRow: 1}
	for i := range records[0] {
		tbl.Cols = append(tbl.Cols, ColLetters(i))
	}
	for ri, rec := range records[1:] {
		rowNum := ri + 2
		tbl.Rows = append(tbl.Rows, rec)
		tbl.RowNumbers = append(tbl.RowNumbers, rowNum)
		doc.Sections = append(doc.Sections, Section{
			Path: "Sheet1", Text: rowText(records[0], rec), Sheet: "Sheet1",
			Cell: fmt.Sprintf("A%d:%s%d", rowNum, ColLetters(max(len(rec), 1)-1), rowNum), Index: ri, Kind: "row",
		})
	}
	doc.Tables = []Table{tbl}
	return doc, nil
}

func rowText(headers, rec []string) string {
	var parts []string
	for i, v := range rec {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if i < len(headers) && strings.TrimSpace(headers[i]) != "" {
			parts = append(parts, strings.TrimSpace(headers[i])+": "+v)
		} else {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " | ")
}

// ColLetters converts a 0-based column index to spreadsheet letters (0 → A, 26 → AA).
func ColLetters(i int) string {
	s := ""
	i++
	for i > 0 {
		i--
		s = string(rune('A'+i%26)) + s
		i /= 26
	}
	return s
}

// ColIndex converts letters to a 0-based index.
func ColIndex(letters string) int {
	n := 0
	for _, r := range strings.ToUpper(letters) {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
	}
	return n - 1
}

var reCellRef = regexp.MustCompile(`^([A-Za-z]+)(\d+)$`)

// SplitRef splits "C12" into ("C", 12).
func SplitRef(ref string) (string, int) {
	m := reCellRef.FindStringSubmatch(ref)
	if m == nil {
		return "", 0
	}
	n := 0
	fmt.Sscanf(m[2], "%d", &n)
	return strings.ToUpper(m[1]), n
}

// NormalizeWS collapses whitespace and straightens quotes for anchoring comparisons.
func NormalizeWS(s string) string {
	r := strings.NewReplacer("‘", "'", "’", "'", "“", `"`, "”", `"`, " ", " ", "–", "-", "—", "-")
	s = r.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// linesToSections groups plain lines into paragraph sections (shared by PDF and text).
func linesToSections(lines []string, page int, startIdx int) []Section {
	doc, _ := ParseText(strings.Join(lines, "\n"))
	out := make([]Section, 0, len(doc.Sections))
	for i, s := range doc.Sections {
		s.Page = page
		s.Index = startIdx + i
		s.Cell = fmt.Sprintf("page%d/p%d", page, i)
		out = append(out, s)
	}
	return out
}
