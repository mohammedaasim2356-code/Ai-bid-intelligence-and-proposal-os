package docs

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// ParseXlsx reads every sheet; the first non-empty row is the header row and each
// following row becomes a section whose locator is the row's cell range.
func ParseXlsx(data []byte) (*ParsedDocument, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("the workbook could not be opened (corrupt ZIP container)")
	}
	sheets, err := workbookSheets(zr)
	if err != nil {
		return nil, err
	}
	shared, _ := sharedStrings(zr)
	doc := &ParsedDocument{}
	idx := 0
	for _, sh := range sheets {
		xmlData, err := readZipFile(zr, sh.path)
		if err != nil {
			continue
		}
		cells, err := sheetCells(xmlData, shared)
		if err != nil {
			return nil, fmt.Errorf("sheet %q could not be read: %v", sh.name, err)
		}
		if len(cells) == 0 {
			continue
		}
		tbl := Table{Sheet: sh.name}
		rows := sortedRowNumbers(cells)
		headerRow := rows[0]
		maxCol := 0
		for _, r := range rows {
			for c := range cells[r] {
				if c+1 > maxCol {
					maxCol = c + 1
				}
			}
		}
		tbl.HeaderRow = headerRow
		tbl.Headers = make([]string, maxCol)
		for c := 0; c < maxCol; c++ {
			tbl.Headers[c] = cells[headerRow][c]
			tbl.Cols = append(tbl.Cols, ColLetters(c))
		}
		for _, r := range rows[1:] {
			row := make([]string, maxCol)
			for c := 0; c < maxCol; c++ {
				row[c] = cells[r][c]
			}
			if len(nonEmpty(row)) == 0 {
				continue
			}
			tbl.Rows = append(tbl.Rows, row)
			tbl.RowNumbers = append(tbl.RowNumbers, r)
			doc.Sections = append(doc.Sections, Section{
				Path: sh.name, Text: rowText(tbl.Headers, row), Sheet: sh.name,
				Cell: fmt.Sprintf("A%d:%s%d", r, ColLetters(maxCol-1), r), Index: idx, Kind: "row",
			})
			idx++
		}
		doc.Tables = append(doc.Tables, tbl)
	}
	if len(doc.Tables) == 0 {
		return nil, errors.New("the workbook contains no readable sheets (protected or empty sheets cannot be imported; try an unlocked copy or CSV)")
	}
	return doc, nil
}

type sheetRef struct{ name, path string }

func workbookSheets(zr *zip.Reader) ([]sheetRef, error) {
	wb, err := readZipFile(zr, "xl/workbook.xml")
	if err != nil {
		return nil, errors.New("the file has no xl/workbook.xml part; it is not an XLSX workbook")
	}
	rels, _ := readZipFile(zr, "xl/_rels/workbook.xml.rels")
	relMap := map[string]string{}
	if rels != nil {
		var r struct {
			Rel []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(rels, &r); err == nil {
			for _, x := range r.Rel {
				t := x.Target
				if strings.HasPrefix(t, "/") {
					t = strings.TrimPrefix(t, "/")
				} else {
					t = path.Join("xl", t)
				}
				relMap[x.ID] = t
			}
		}
	}
	var w struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(wb, &w); err != nil {
		return nil, fmt.Errorf("workbook.xml is malformed: %v", err)
	}
	var out []sheetRef
	for i, s := range w.Sheets {
		p := relMap[s.ID]
		if p == "" {
			p = fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		}
		out = append(out, sheetRef{name: s.Name, path: p})
	}
	return out, nil
}

func sharedStrings(zr *zip.Reader) ([]string, error) {
	data, err := readZipFile(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil, nil
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	var cur strings.Builder
	inSI := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				if inSI {
					var s string
					if err := dec.DecodeElement(&s, &t); err == nil {
						cur.WriteString(s)
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "si" {
				out = append(out, cur.String())
				inSI = false
			}
		}
	}
	return out, nil
}

// sheetCells returns row → (colIndex → text).
func sheetCells(xmlData []byte, shared []string) (map[int]map[int]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(xmlData))
	cells := map[int]map[int]string{}
	var (
		curRef, curType string
		inC, inV, inIS  bool
		val             strings.Builder
	)
	store := func() {
		col, row := SplitRef(curRef)
		if row == 0 {
			return
		}
		v := val.String()
		if curType == "s" {
			if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && i >= 0 && i < len(shared) {
				v = shared[i]
			}
		} else if curType == "b" {
			if v == "1" {
				v = "TRUE"
			} else if v == "0" {
				v = "FALSE"
			}
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if cells[row] == nil {
			cells[row] = map[int]string{}
		}
		cells[row][ColIndex(col)] = v
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "c":
				inC = true
				curRef, curType = "", ""
				val.Reset()
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "r":
						curRef = a.Value
					case "t":
						curType = a.Value
					}
				}
			case "v":
				if inC {
					inV = true
				}
			case "is":
				inIS = true
			case "t":
				if inC && inIS {
					var s string
					if err := dec.DecodeElement(&s, &t); err == nil {
						val.WriteString(s)
					}
				}
			}
		case xml.CharData:
			if inV {
				val.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "is":
				inIS = false
			case "c":
				if inC {
					store()
				}
				inC = false
			}
		}
	}
	return cells, nil
}

func sortedRowNumbers(cells map[int]map[int]string) []int {
	rows := make([]int, 0, len(cells))
	for r := range cells {
		rows = append(rows, r)
	}
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j] < rows[i] {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	return rows
}

// FillXlsx writes text into specific cells of a sheet, leaving every other byte of the
// workbook untouched (raw ZIP copy). Cells are written as inline strings.
func FillXlsx(original []byte, sheet string, fills map[string]string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		return nil, err
	}
	sheets, err := workbookSheets(zr)
	if err != nil {
		return nil, err
	}
	var target string
	for _, s := range sheets {
		if s.name == sheet {
			target = s.path
		}
	}
	if target == "" {
		return nil, fmt.Errorf("sheet %q not found in workbook", sheet)
	}
	xmlData, err := readZipFile(zr, target)
	if err != nil {
		return nil, err
	}
	s := string(xmlData)
	// group fills by row
	byRow := map[int]map[string]string{}
	for ref, text := range fills {
		col, row := SplitRef(ref)
		if row == 0 {
			return nil, fmt.Errorf("bad cell reference %q", ref)
		}
		if byRow[row] == nil {
			byRow[row] = map[string]string{}
		}
		byRow[row][col] = text
	}
	rows := make([]int, 0, len(byRow))
	for r := range byRow {
		rows = append(rows, r)
	}
	sortDesc(rows) // edit from the bottom so earlier offsets stay valid
	for _, r := range rows {
		s, err = fillRow(s, r, byRow[r])
		if err != nil {
			return nil, err
		}
	}
	return rewriteZip(original, map[string][]byte{target: []byte(s)})
}

var reRowTag = regexp.MustCompile(`<row\s[^>]*?\br="(\d+)"[^>]*?(/?)>`)
var reCellTag = regexp.MustCompile(`<c\s[^>]*?\br="([A-Z]+)(\d+)"[^>]*?(/?)>`)

func inlineCell(col string, row int, style, text string) string {
	st := ""
	if style != "" {
		st = ` s="` + style + `"`
	}
	return `<c r="` + col + strconv.Itoa(row) + `"` + st + ` t="inlineStr"><is><t xml:space="preserve">` + xmlEscape(text) + `</t></is></c>`
}

func fillRow(s string, rowNum int, cells map[string]string) (string, error) {
	// locate row
	rowStart, rowEnd, selfClosing := -1, -1, false
	for _, m := range reRowTag.FindAllStringSubmatchIndex(s, -1) {
		n, _ := strconv.Atoi(s[m[2]:m[3]])
		if n == rowNum {
			rowStart = m[0]
			selfClosing = s[m[4]:m[5]] == "/"
			rowEnd = m[1]
			break
		}
	}
	if rowStart < 0 {
		// insert a new row in order before the first row with a larger number, or before </sheetData>
		insertAt := strings.Index(s, "</sheetData>")
		if insertAt < 0 {
			return "", errors.New("sheet has no sheetData")
		}
		for _, m := range reRowTag.FindAllStringSubmatchIndex(s, -1) {
			n, _ := strconv.Atoi(s[m[2]:m[3]])
			if n > rowNum {
				insertAt = m[0]
				break
			}
		}
		var b strings.Builder
		b.WriteString(`<row r="` + strconv.Itoa(rowNum) + `">`)
		for _, col := range sortedCols(cells) {
			b.WriteString(inlineCell(col, rowNum, "", cells[col]))
		}
		b.WriteString("</row>")
		return s[:insertAt] + b.String() + s[insertAt:], nil
	}
	if selfClosing {
		// expand <row .../> into <row ...></row>
		open := s[rowStart:rowEnd-2] + ">"
		s = s[:rowStart] + open + "</row>" + s[rowEnd:]
		rowEnd = rowStart + len(open)
	}
	closeIdx := strings.Index(s[rowEnd:], "</row>")
	if closeIdx < 0 {
		return "", errors.New("unterminated row")
	}
	closeIdx += rowEnd
	rowBody := s[rowEnd:closeIdx]
	for _, col := range sortedCols(cells) {
		text := cells[col]
		replaced := false
		for _, m := range reCellTag.FindAllStringSubmatchIndex(rowBody, -1) {
			if rowBody[m[2]:m[3]] != col {
				continue
			}
			tag := rowBody[m[0]:m[1]]
			style := ""
			if sm := regexp.MustCompile(`\bs="(\d+)"`).FindStringSubmatch(tag); sm != nil {
				style = sm[1]
			}
			end := m[1]
			if rowBody[m[6]:m[7]] != "/" {
				e := strings.Index(rowBody[m[1]:], "</c>")
				if e < 0 {
					return "", errors.New("unterminated cell")
				}
				end = m[1] + e + len("</c>")
			}
			rowBody = rowBody[:m[0]] + inlineCell(col, rowNum, style, text) + rowBody[end:]
			replaced = true
			break
		}
		if replaced {
			continue
		}
		// insert in column order
		insertAt := len(rowBody)
		for _, m := range reCellTag.FindAllStringSubmatchIndex(rowBody, -1) {
			if ColIndex(rowBody[m[2]:m[3]]) > ColIndex(col) {
				insertAt = m[0]
				break
			}
		}
		rowBody = rowBody[:insertAt] + inlineCell(col, rowNum, "", text) + rowBody[insertAt:]
	}
	return s[:rowEnd] + rowBody + s[closeIdx:], nil
}

func sortedCols(cells map[string]string) []string {
	cols := make([]string, 0, len(cells))
	for c := range cells {
		cols = append(cols, c)
	}
	for i := 0; i < len(cols); i++ {
		for j := i + 1; j < len(cols); j++ {
			if ColIndex(cols[j]) < ColIndex(cols[i]) {
				cols[i], cols[j] = cols[j], cols[i]
			}
		}
	}
	return cols
}

// SheetData is input for BuildXlsx.
type SheetData struct {
	Name string
	Rows [][]string
}

// BuildXlsx generates a minimal valid workbook with inline strings.
func BuildXlsx(sheets []SheetData) ([]byte, error) {
	if len(sheets) == 0 {
		return nil, errors.New("no sheets")
	}
	files := map[string]string{}
	var wbSheets, wbRels, ctOverrides strings.Builder
	for i, sh := range sheets {
		n := i + 1
		var sd strings.Builder
		sd.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
		for ri, row := range sh.Rows {
			sd.WriteString(`<row r="` + strconv.Itoa(ri+1) + `">`)
			for ci, cell := range row {
				if cell == "" {
					continue
				}
				sd.WriteString(inlineCell(ColLetters(ci), ri+1, "", cell))
			}
			sd.WriteString("</row>")
		}
		sd.WriteString("</sheetData></worksheet>")
		files[fmt.Sprintf("xl/worksheets/sheet%d.xml", n)] = sd.String()
		wbSheets.WriteString(fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscape(sh.Name), n, n))
		wbRels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, n, n))
		ctOverrides.WriteString(fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, n))
	}
	styleRel := len(sheets) + 1
	files["[Content_Types].xml"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` + ctOverrides.String() + `</Types>`
	files["_rels/.rels"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`
	files["xl/workbook.xml"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>` + wbSheets.String() + `</sheets></workbook>`
	files["xl/_rels/workbook.xml.rels"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + wbRels.String() + fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, styleRel) + `</Relationships>`
	files["xl/styles.xml"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts><fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills><borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/></cellXfs></styleSheet>`
	return writeZip(files)
}

// CellValues returns sheet → cellRef → value for diffing workbooks in tests/exports.
func CellValues(data []byte) (map[string]map[string]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	sheets, err := workbookSheets(zr)
	if err != nil {
		return nil, err
	}
	shared, _ := sharedStrings(zr)
	out := map[string]map[string]string{}
	for _, sh := range sheets {
		xmlData, err := readZipFile(zr, sh.path)
		if err != nil {
			continue
		}
		cells, err := sheetCells(xmlData, shared)
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		for r, cols := range cells {
			for c, v := range cols {
				m[ColLetters(c)+strconv.Itoa(r)] = v
			}
		}
		out[sh.name] = m
	}
	return out, nil
}
