package docs

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
)

// ParsePDF extracts text page by page. Scanned/image-only PDFs yield no text and
// produce a readable error.
func ParsePDF(data []byte) (doc *ParsedDocument, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, fmt.Errorf("the PDF could not be parsed (%v); try re-saving it or exporting to DOCX", r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("the PDF could not be opened: %v", err)
	}
	doc = &ParsedDocument{Pages: r.NumPage()}
	idx := 0
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		rows, err := p.GetTextByRow()
		if err != nil {
			continue
		}
		var lines []string
		for _, row := range rows {
			var b strings.Builder
			for _, t := range row.Content {
				b.WriteString(t.S)
			}
			lines = append(lines, strings.TrimSpace(b.String()))
		}
		secs := linesToSections(lines, i, idx)
		doc.Sections = append(doc.Sections, secs...)
		idx += len(secs)
	}
	if len(doc.Sections) == 0 {
		return nil, errors.New("the PDF has no extractable text; it is probably a scanned image and needs OCR before import")
	}
	return doc, nil
}

// BuildPDF renders plain text lines as a simple multi-page PDF (Helvetica, Latin-1).
// ponytail: text only, no fonts embedding or wrapping beyond a fixed width; upgrade to a
// real renderer when rich PDF export matters.
func BuildPDF(title string, lines []string) []byte {
	const perPage = 48
	var wrapped []string
	for _, l := range lines {
		wrapped = append(wrapped, wrapLine(l, 92)...)
	}
	var pages [][]string
	for i := 0; i < len(wrapped); i += perPage {
		end := min(i+perPage, len(wrapped))
		pages = append(pages, wrapped[i:end])
	}
	if len(pages) == 0 {
		pages = [][]string{{""}}
	}
	var objs []string // object bodies (1-based ids)
	add := func(s string) int { objs = append(objs, s); return len(objs) }
	catalog := add("")  // 1
	pagesObj := add("") // 2
	font := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	fontB := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	var pageIDs []int
	for pi, pg := range pages {
		var c strings.Builder
		y := 800
		if pi == 0 && title != "" {
			c.WriteString(fmt.Sprintf("BT /F2 16 Tf 50 %d Td (%s) Tj ET\n", y, pdfEscape(title)))
			y -= 28
		}
		for _, l := range pg {
			c.WriteString(fmt.Sprintf("BT /F1 10 Tf 50 %d Td (%s) Tj ET\n", y, pdfEscape(l)))
			y -= 15
		}
		content := c.String()
		stream := add(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
		page := add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 595 842] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> >>", pagesObj, stream, font, fontB))
		pageIDs = append(pageIDs, page)
	}
	kids := make([]string, len(pageIDs))
	for i, id := range pageIDs {
		kids[i] = fmt.Sprintf("%d 0 R", id)
	}
	objs[pagesObj-1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pageIDs))
	objs[catalog-1] = fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObj)
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = out.Len()
		out.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, body))
	}
	xref := out.Len()
	out.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objs)+1))
	for i := 1; i <= len(objs); i++ {
		out.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	out.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, catalog, xref))
	return out.Bytes()
}

func pdfEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 32:
			b.WriteByte(' ')
		case r > 255:
			b.WriteByte('?')
		default:
			b.WriteByte(byte(r))
		}
	}
	return b.String()
}

func wrapLine(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	cur := ""
	for _, w := range words {
		if cur == "" {
			cur = w
			continue
		}
		if len(cur)+1+len(w) > width {
			out = append(out, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return append(out, cur)
}
