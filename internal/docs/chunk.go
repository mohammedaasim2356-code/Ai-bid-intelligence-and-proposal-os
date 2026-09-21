package docs

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ChunkOut is a retrieval unit with its source locator.
type ChunkOut struct {
	SectionPath string
	Text        string
	Page        int
	Sheet       string
	CellRange   string
	TextHash    string
}

// TextHash hashes whitespace-normalised text (used to skip re-embedding).
func TextHash(s string) string {
	h := sha256.Sum256([]byte(NormalizeWS(s)))
	return hex.EncodeToString(h[:])
}

// Chunk groups consecutive sections that share a heading path (or sheet) into chunks
// of at most maxChars, keeping cell ranges accurate for spreadsheet rows.
func Chunk(doc *ParsedDocument, maxChars int) []ChunkOut {
	if maxChars <= 0 {
		maxChars = 1200
	}
	var out []ChunkOut
	var cur *ChunkOut
	var curTexts []string
	var firstCell, lastCell string
	flush := func() {
		if cur == nil {
			return
		}
		text := strings.TrimSpace(strings.Join(curTexts, "\n"))
		if text != "" {
			cur.Text = text
			cur.TextHash = TextHash(text)
			if cur.Sheet != "" && firstCell != "" {
				cur.CellRange = spanRange(firstCell, lastCell)
			}
			out = append(out, *cur)
		}
		cur, curTexts, firstCell, lastCell = nil, nil, "", ""
	}
	for _, s := range doc.Sections {
		key := s.Path
		if s.Sheet != "" {
			key = "sheet:" + s.Sheet
		}
		text := s.Text
		if s.Kind == "heading" {
			// headings start a new chunk and are included as context
			flush()
			cur = &ChunkOut{SectionPath: s.Path, Page: s.Page, Sheet: s.Sheet}
			curTexts = []string{text}
			continue
		}
		curKey := ""
		if cur != nil {
			curKey = cur.SectionPath
			if cur.Sheet != "" {
				curKey = "sheet:" + cur.Sheet
			}
		}
		if cur == nil || curKey != key || len(strings.Join(curTexts, "\n"))+len(text) > maxChars {
			flush()
			cur = &ChunkOut{SectionPath: s.Path, Page: s.Page, Sheet: s.Sheet}
		}
		curTexts = append(curTexts, text)
		if s.Sheet != "" {
			if firstCell == "" {
				firstCell = s.Cell
			}
			lastCell = s.Cell
		}
	}
	flush()
	return out
}

// spanRange combines "A2:D2" and "A9:D9" into "A2:D9".
func spanRange(first, last string) string {
	f := strings.Split(first, ":")
	l := strings.Split(last, ":")
	if len(f) == 0 || len(l) == 0 {
		return first
	}
	end := l[len(l)-1]
	return f[0] + ":" + end
}
