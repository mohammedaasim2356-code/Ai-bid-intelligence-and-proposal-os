package docs

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// ParseDocx extracts paragraphs (with heading trail) and tables from a .docx.
// Paragraph locators ("p{n}") count every w:p in document order, including
// paragraphs inside table cells, so FillDocx can address the same nodes.
func ParseDocx(data []byte) (*ParsedDocument, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("the DOCX package could not be opened (corrupt ZIP container)")
	}
	xmlData, err := readZipFile(zr, "word/document.xml")
	if err != nil {
		return nil, errors.New("the DOCX has no word/document.xml part; it may be a template or a protected file")
	}
	dec := xml.NewDecoder(bytes.NewReader(xmlData))
	doc := &ParsedDocument{}
	trail := []string{}
	var (
		pIndex     = -1
		inP        bool
		pText      strings.Builder
		pStyle     string
		tblDepth   int
		tblIndex   = -1
		curTable   *Table
		curRow     []string
		curRowIdx  int
		cellText   strings.Builder
		inCell     bool
		sectionIdx int
	)
	isW := func(n xml.Name) bool { return n.Space == wordNS || n.Space == "" }
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the DOCX body XML is malformed: %v", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if !isW(t.Name) {
				continue
			}
			switch t.Name.Local {
			case "tbl":
				tblDepth++
				if tblDepth == 1 {
					tblIndex++
					doc.Tables = append(doc.Tables, Table{Sheet: fmt.Sprintf("Table %d", tblIndex+1), DocxIndex: tblIndex})
					curTable = &doc.Tables[len(doc.Tables)-1]
					curRowIdx = -1
				}
			case "tr":
				if tblDepth == 1 {
					curRowIdx++
					curRow = nil
				}
			case "tc":
				if tblDepth == 1 {
					inCell = true
					cellText.Reset()
				}
			case "p":
				pIndex++
				inP = true
				pText.Reset()
				pStyle = ""
			case "pStyle":
				for _, a := range t.Attr {
					if a.Name.Local == "val" {
						pStyle = a.Value
					}
				}
			case "t":
				if inP {
					var s string
					if err := dec.DecodeElement(&s, &t); err == nil {
						pText.WriteString(s)
					}
				}
			case "tab":
				if inP {
					pText.WriteString("\t")
				}
			case "br", "cr":
				if inP {
					pText.WriteString("\n")
				}
			}
		case xml.EndElement:
			if !isW(t.Name) {
				continue
			}
			switch t.Name.Local {
			case "p":
				inP = false
				text := strings.TrimSpace(pText.String())
				if inCell {
					if text != "" {
						if cellText.Len() > 0 {
							cellText.WriteString(" ")
						}
						cellText.WriteString(text)
					}
					continue
				}
				if text == "" {
					continue
				}
				if lvl := headingLevel(pStyle); lvl > 0 {
					if lvl-1 < len(trail) {
						trail = trail[:lvl-1]
					}
					trail = append(trail, text)
					doc.Sections = append(doc.Sections, Section{Path: strings.Join(trail, " > "), Text: text, Index: sectionIdx, Kind: "heading", Cell: fmt.Sprintf("p%d", pIndex)})
				} else {
					doc.Sections = append(doc.Sections, Section{Path: strings.Join(trail, " > "), Text: text, Index: sectionIdx, Kind: "paragraph", Cell: fmt.Sprintf("p%d", pIndex)})
				}
				sectionIdx++
			case "tc":
				if tblDepth == 1 {
					inCell = false
					curRow = append(curRow, strings.TrimSpace(cellText.String()))
				}
			case "tr":
				if tblDepth == 1 && curTable != nil {
					if curRowIdx == 0 {
						curTable.Headers = append([]string{}, curRow...)
						curTable.HeaderRow = 0
						for i := range curRow {
							curTable.Cols = append(curTable.Cols, fmt.Sprintf("c%d", i))
						}
					} else {
						curTable.Rows = append(curTable.Rows, append([]string{}, curRow...))
						curTable.RowNumbers = append(curTable.RowNumbers, curRowIdx)
					}
					if txt := strings.TrimSpace(strings.Join(nonEmpty(curRow), " | ")); txt != "" {
						doc.Sections = append(doc.Sections, Section{Path: strings.Join(trail, " > "), Text: txt, Index: sectionIdx, Kind: "row", Sheet: curTable.Sheet, Cell: fmt.Sprintf("tbl%d/r%d", tblIndex, curRowIdx)})
						sectionIdx++
					}
				}
			case "tbl":
				tblDepth--
				if tblDepth == 0 {
					curTable = nil
				}
			}
		}
	}
	return doc, nil
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func headingLevel(style string) int {
	s := strings.ToLower(style)
	switch {
	case s == "title":
		return 1
	case strings.HasPrefix(s, "heading"):
		n := 0
		fmt.Sscanf(strings.TrimPrefix(s, "heading"), "%d", &n)
		if n == 0 {
			n = 1
		}
		return n + 1
	}
	return 0
}

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not found", name)
}

// rewriteZip copies every entry raw (byte-identical) except those in replace.
func rewriteZip(data []byte, replace map[string][]byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if nb, ok := replace[f.Name]; ok {
			w, err := zw.Create(f.Name)
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(nb); err != nil {
				return nil, err
			}
			continue
		}
		rc, err := f.OpenRaw()
		if err != nil {
			return nil, err
		}
		hdr := f.FileHeader
		w, err := zw.CreateRaw(&hdr)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(w, rc); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// FillDocx writes answers back into the original document. Locators:
//   - "tbl{i}/r{j}/c{k}": replace the content of that table cell
//   - "p{n}": insert a new paragraph directly after paragraph n
//
// Everything else in the package is copied byte-for-byte.
func FillDocx(original []byte, fills map[string]string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		return nil, err
	}
	xmlData, err := readZipFile(zr, "word/document.xml")
	if err != nil {
		return nil, err
	}
	s := string(xmlData)
	// Apply table-cell fills first (they do not change paragraph numbering order
	// relative to later paragraph inserts because we process paragraph inserts on
	// the already-modified string using fresh indexes computed each time).
	for loc, text := range fills {
		if strings.HasPrefix(loc, "tbl") {
			var ti, ri, ci int
			if _, err := fmt.Sscanf(loc, "tbl%d/r%d/c%d", &ti, &ri, &ci); err != nil {
				return nil, fmt.Errorf("bad locator %q", loc)
			}
			s, err = replaceTableCell(s, ti, ri, ci, text)
			if err != nil {
				return nil, err
			}
		}
	}
	// Paragraph inserts: process highest index first so earlier indexes stay valid.
	var pLocs []int
	for loc := range fills {
		var n int
		if _, err := fmt.Sscanf(loc, "p%d", &n); err == nil && strings.HasPrefix(loc, "p") {
			pLocs = append(pLocs, n)
		}
	}
	sortDesc(pLocs)
	for _, n := range pLocs {
		end := paragraphEnd(s, n)
		if end < 0 {
			return nil, fmt.Errorf("paragraph p%d not found", n)
		}
		s = s[:end] + paragraphXML("", fills[fmt.Sprintf("p%d", n)]) + s[end:]
	}
	return rewriteZip(original, map[string][]byte{"word/document.xml": []byte(s)})
}

func sortDesc(a []int) {
	for i := 0; i < len(a); i++ {
		for j := i + 1; j < len(a); j++ {
			if a[j] > a[i] {
				a[i], a[j] = a[j], a[i]
			}
		}
	}
}

var reWP = regexp.MustCompile(`<w:p(?:\s[^>]*)?/?>|</w:p>`)

// paragraphEnd returns the byte offset just after the closing tag of paragraph n
// (counting <w:p> start tags in document order, including self-closing ones).
func paragraphEnd(s string, n int) int {
	depth, count := 0, -1
	for _, m := range reWP.FindAllStringIndex(s, -1) {
		tag := s[m[0]:m[1]]
		if strings.HasPrefix(tag, "</") {
			depth--
			if depth == 0 && count == n {
				return m[1]
			}
			continue
		}
		count++
		if strings.HasSuffix(tag, "/>") {
			if count == n {
				return m[1]
			}
			continue
		}
		depth++
	}
	return -1
}

// replaceTableCell replaces the content of cell (ti, ri, ci) with a single paragraph,
// keeping the cell's <w:tcPr> if present.
func replaceTableCell(s string, ti, ri, ci int, text string) (string, error) {
	tblStart := nthTag(s, "<w:tbl>", "<w:tbl ", ti, 0)
	if tblStart < 0 {
		return "", fmt.Errorf("table %d not found", ti)
	}
	tblEnd := strings.Index(s[tblStart:], "</w:tbl>")
	if tblEnd < 0 {
		return "", errors.New("unterminated table")
	}
	tblEnd += tblStart
	rowStart := nthTag(s[:tblEnd], "<w:tr>", "<w:tr ", ri, tblStart)
	if rowStart < 0 {
		return "", fmt.Errorf("row %d not found in table %d", ri, ti)
	}
	rowEnd := strings.Index(s[rowStart:tblEnd], "</w:tr>")
	if rowEnd < 0 {
		return "", errors.New("unterminated row")
	}
	rowEnd += rowStart
	cellStart := nthTag(s[:rowEnd], "<w:tc>", "<w:tc ", ci, rowStart)
	if cellStart < 0 {
		return "", fmt.Errorf("cell %d not found in row %d", ci, ri)
	}
	cellEnd := strings.Index(s[cellStart:rowEnd], "</w:tc>")
	if cellEnd < 0 {
		return "", errors.New("unterminated cell")
	}
	cellEnd += cellStart
	cell := s[cellStart:cellEnd]
	openEnd := strings.Index(cell, ">") + 1
	body := cell[openEnd:]
	tcPr := ""
	if i := strings.Index(body, "<w:tcPr"); i == 0 {
		if j := strings.Index(body, "</w:tcPr>"); j >= 0 {
			tcPr = body[:j+len("</w:tcPr>")]
		} else if j := strings.Index(body, "/>"); j >= 0 {
			tcPr = body[:j+2]
		}
	}
	newCell := cell[:openEnd] + tcPr + paragraphXML("", text)
	return s[:cellStart] + newCell + s[cellEnd:], nil
}

// nthTag finds the n-th occurrence (0-based) of an element start tag after offset.
func nthTag(s, exact, prefix string, n, from int) int {
	count := -1
	i := from
	for i < len(s) {
		a := strings.Index(s[i:], exact)
		b := strings.Index(s[i:], prefix)
		pos := -1
		switch {
		case a < 0 && b < 0:
			return -1
		case a < 0:
			pos = i + b
		case b < 0:
			pos = i + a
		default:
			pos = i + min(a, b)
		}
		count++
		if count == n {
			return pos
		}
		i = pos + 1
	}
	return -1
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// paragraphXML renders a paragraph; multi-line text becomes line breaks.
func paragraphXML(style, text string) string {
	var b strings.Builder
	b.WriteString("<w:p>")
	if style != "" {
		b.WriteString(`<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`)
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i > 0 {
			b.WriteString("<w:r><w:br/></w:r>")
		}
		b.WriteString(`<w:r><w:t xml:space="preserve">` + xmlEscape(line) + `</w:t></w:r>`)
	}
	b.WriteString("</w:p>")
	return b.String()
}

// Block is one element of a generated DOCX.
type Block struct {
	Kind string // title | h1 | h2 | h3 | p | bullet | table | meta
	Text string
	Rows [][]string
}

// BuildDocx generates a minimal, valid .docx from blocks.
func BuildDocx(blocks []Block) ([]byte, error) {
	var body strings.Builder
	for _, b := range blocks {
		switch b.Kind {
		case "title":
			body.WriteString(paragraphXML("Title", b.Text))
		case "h1":
			body.WriteString(paragraphXML("Heading1", b.Text))
		case "h2":
			body.WriteString(paragraphXML("Heading2", b.Text))
		case "h3":
			body.WriteString(paragraphXML("Heading3", b.Text))
		case "bullet":
			body.WriteString(paragraphXML("ListParagraph", "• "+b.Text))
		case "meta":
			body.WriteString(paragraphXML("Meta", b.Text))
		case "table":
			body.WriteString(tableXML(b.Rows))
		default:
			body.WriteString(paragraphXML("", b.Text))
		}
	}
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="` + wordNS + `" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>` + body.String() + `<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr></w:body></w:document>`
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`,
		"word/document.xml": document,
		"word/styles.xml":   docxStyles,
	}
	return writeZip(files)
}

func tableXML(rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="0" w:type="auto"/><w:tblBorders><w:top w:val="single" w:sz="4" w:space="0" w:color="BFC5D2"/><w:left w:val="single" w:sz="4" w:space="0" w:color="BFC5D2"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="BFC5D2"/><w:right w:val="single" w:sz="4" w:space="0" w:color="BFC5D2"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="BFC5D2"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="BFC5D2"/></w:tblBorders></w:tblPr>`)
	for ri, row := range rows {
		b.WriteString("<w:tr>")
		for _, cell := range row {
			b.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr>`)
			if ri == 0 {
				b.WriteString(`<w:p><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">` + xmlEscape(cell) + `</w:t></w:r></w:p>`)
			} else {
				b.WriteString(paragraphXML("", cell))
			}
			b.WriteString("</w:tc>")
		}
		b.WriteString("</w:tr>")
	}
	b.WriteString("</w:tbl>")
	b.WriteString(paragraphXML("", ""))
	return b.String()
}

func writeZip(files map[string]string) ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	// deterministic order
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(files[n])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func sortStrings(a []string) {
	for i := 0; i < len(a); i++ {
		for j := i + 1; j < len(a); j++ {
			if a[j] < a[i] {
				a[i], a[j] = a[j], a[i]
			}
		}
	}
}

const docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:cs="Calibri"/><w:sz w:val="22"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="240"/></w:pPr><w:rPr><w:b/><w:sz w:val="48"/><w:color w:val="14203F"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="360" w:after="120"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:sz w:val="32"/><w:color w:val="14203F"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="80"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:sz w:val="26"/><w:color w:val="0F8F9E"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="160" w:after="60"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:b/><w:sz w:val="23"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:pPr><w:ind w:left="360"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Meta"><w:name w:val="Meta"/><w:basedOn w:val="Normal"/><w:rPr><w:i/><w:sz w:val="18"/><w:color w:val="6B7280"/></w:rPr></w:style>
<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:left w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:right w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="auto"/></w:tblBorders></w:tblPr></w:style>
</w:styles>`
