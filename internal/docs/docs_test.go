package docs

import (
	"strings"
	"testing"
)

func TestTextAndMarkdown(t *testing.T) {
	doc, err := Parse("rfp.md", []byte("# 1 Intro\n\nHello world.\n\n## 1.1 Scope\n\n1.1.1 The vendor shall provide X.\n1.1.2 The vendor must provide Y.\n\n- bullet one\n- bullet two\n"))
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range doc.Sections {
		texts = append(texts, s.Kind+":"+s.Text+"@"+s.Path)
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{"heading:1 Intro@1 Intro", "paragraph:Hello world.@1 Intro", "paragraph:1.1.1 The vendor shall provide X.@1 Intro > 1.1 Scope", "paragraph:1.1.2 The vendor must provide Y.@1 Intro > 1.1 Scope", "paragraph:- bullet one@"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	chunks := Chunk(doc, 1200)
	if len(chunks) < 2 {
		t.Fatalf("expected heading-grouped chunks, got %d", len(chunks))
	}
}

func TestCSV(t *testing.T) {
	doc, err := Parse("q.csv", []byte("ID,Question,Answer\n1,Do you encrypt data at rest?,\n2,\"Describe, briefly, your SLA\",\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Tables) != 1 || len(doc.Tables[0].Rows) != 2 || doc.Tables[0].Headers[1] != "Question" {
		t.Fatalf("bad table: %+v", doc.Tables)
	}
	if doc.Sections[1].Cell != "A3:C3" || !strings.Contains(doc.Sections[1].Text, "Describe, briefly, your SLA") {
		t.Fatalf("bad section: %+v", doc.Sections[1])
	}
}

func TestDocxRoundTrip(t *testing.T) {
	data, err := BuildDocx([]Block{
		{Kind: "title", Text: "Vendor Questionnaire"},
		{Kind: "h1", Text: "1 Security"},
		{Kind: "p", Text: "1.1 The vendor shall encrypt data at rest."},
		{Kind: "table", Rows: [][]string{{"Ref", "Question", "Answer"}, {"Q1", "Do you hold SOC 2?", ""}, {"Q2", "Describe your SLA.", ""}}},
		{Kind: "p", Text: "End."},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse("q.docx", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Tables) != 1 || len(doc.Tables[0].Rows) != 2 || doc.Tables[0].Rows[0][1] != "Do you hold SOC 2?" {
		t.Fatalf("bad docx table: %+v", doc.Tables)
	}
	var found bool
	for _, s := range doc.Sections {
		if s.Text == "1.1 The vendor shall encrypt data at rest." && s.Path == "Vendor Questionnaire > 1 Security" && s.Cell == "p2" {
			found = true
		}
	}
	if !found {
		for _, s := range doc.Sections {
			t.Logf("%+v", s)
		}
		t.Fatal("paragraph with heading trail not found")
	}
	filled, err := FillDocx(data, map[string]string{"tbl0/r1/c2": "Yes, SOC 2 Type II report available.", "tbl0/r2/c2": "99.9% uptime.", "p2": "Response: encrypted with AES-256."})
	if err != nil {
		t.Fatal(err)
	}
	doc2, err := Parse("q.docx", filled)
	if err != nil {
		t.Fatal(err)
	}
	if doc2.Tables[0].Rows[0][2] != "Yes, SOC 2 Type II report available." || doc2.Tables[0].Rows[1][2] != "99.9% uptime." {
		t.Fatalf("fill failed: %+v", doc2.Tables[0].Rows)
	}
	var seen bool
	for i, s := range doc2.Sections {
		if s.Text == "Response: encrypted with AES-256." && i > 0 && doc2.Sections[i-1].Text == "1.1 The vendor shall encrypt data at rest." {
			seen = true
		}
	}
	if !seen {
		t.Fatal("paragraph insert not placed after p2")
	}
}

func TestXlsxRoundTrip(t *testing.T) {
	data, err := BuildXlsx([]SheetData{{Name: "Questions", Rows: [][]string{{"ID", "Question", "Vendor Response"}, {"Q1", "Do you encrypt data at rest?", ""}, {"Q2", "Do you hold ISO 27001?", ""}}}, {Name: "Info", Rows: [][]string{{"Note"}, {"keep me"}}}})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse("q.xlsx", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Tables) != 2 || doc.Tables[0].Headers[2] != "Vendor Response" || doc.Tables[0].RowNumbers[1] != 3 {
		t.Fatalf("bad xlsx tables: %+v", doc.Tables)
	}
	filled, err := FillXlsx(data, "Questions", map[string]string{"C2": "Yes (AES-256)", "C3": "No", "E9": "new row cell"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := CellValues(data)
	after, err := CellValues(filled)
	if err != nil {
		t.Fatal(err)
	}
	if after["Questions"]["C2"] != "Yes (AES-256)" || after["Questions"]["C3"] != "No" || after["Questions"]["E9"] != "new row cell" {
		t.Fatalf("fill failed: %+v", after)
	}
	// every other cell must be identical
	for sheet, cells := range before {
		for ref, v := range cells {
			if sheet == "Questions" && (ref == "C2" || ref == "C3") {
				continue
			}
			if after[sheet][ref] != v {
				t.Fatalf("cell %s!%s changed: %q → %q", sheet, ref, v, after[sheet][ref])
			}
		}
	}
	if after["Info"]["A2"] != "keep me" {
		t.Fatal("other sheet altered")
	}
}

func TestPDF(t *testing.T) {
	data := BuildPDF("Orion RFP", []string{"1 Introduction", "The vendor shall provide encryption at rest.", "", "2 Scope", "Describe your support model (hours, channels)."})
	doc, err := Parse("rfp.pdf", data)
	if err != nil {
		t.Fatal(err)
	}
	full := doc.FullText()
	for _, want := range []string{"encryption at rest", "support model"} {
		if !strings.Contains(full, want) {
			t.Fatalf("pdf text missing %q: %s", want, full)
		}
	}
	if doc.Sections[0].Page != 1 {
		t.Fatalf("page locator missing: %+v", doc.Sections[0])
	}
}

func TestSniff(t *testing.T) {
	if err := Sniff("x.pdf", []byte("not a pdf")); err == nil {
		t.Fatal("expected pdf sniff error")
	}
	if err := Sniff("x.exe", []byte("MZ")); err == nil {
		t.Fatal("expected unsupported")
	}
	if _, err := Parse("empty.txt", []byte("   \n")); err == nil {
		t.Fatal("expected empty error")
	}
}
