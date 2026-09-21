package app

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"bidos/internal/docs"
	"bidos/internal/store"
)

// ExportFormats supported by ExportBid.
var ExportFormats = []string{"md", "docx", "pdf", "html", "json", "csv"}

// ExportGate refuses exports while QA blockers are open (or QA has not run).
func (a *App) ExportGate(orgID, bidID string) error {
	sum, err := a.QASummary(orgID, bidID)
	if err != nil {
		return err
	}
	if sum.RanAt == "" {
		return userErr("Run QA before exporting; the export gate needs a QA result with zero blockers.")
	}
	if sum.Blockers > 0 {
		return userErr("Export is blocked: %d QA blocker(s) are open. Resolve or dismiss them with a reason first.", sum.Blockers)
	}
	return nil
}

// ExportBid renders the assembled proposal (or matrix) in the requested format.
func (a *App) ExportBid(orgID, bidID, format, userID string, ignoreGate bool) (store.ExportJob, []byte, string, error) {
	if !ignoreGate && format != "csv" && format != "json" {
		if err := a.ExportGate(orgID, bidID); err != nil {
			return store.ExportJob{}, nil, "", err
		}
	}
	asm, err := a.Assemble(orgID, bidID)
	if err != nil {
		return store.ExportJob{}, nil, "", err
	}
	var data []byte
	var mime, ext string
	switch format {
	case "md":
		data, mime, ext = []byte(a.markdown(asm)), "text/markdown; charset=utf-8", "md"
	case "html":
		data, mime, ext = []byte(a.printHTML(asm)), "text/html; charset=utf-8", "html"
	case "json":
		data, mime, ext = a.exportJSON(orgID, asm), "application/json", "json"
	case "csv":
		data, mime, ext = a.matrixCSV(orgID, asm), "text/csv", "csv"
	case "docx":
		data, err = docs.BuildDocx(a.docxBlocks(asm))
		if err != nil {
			return store.ExportJob{}, nil, "", err
		}
		mime, ext = docs.MimeFor("x.docx"), "docx"
	case "pdf":
		data, mime, ext = docs.BuildPDF(asm.Bid.Name, strings.Split(a.plainText(asm), "\n")), "application/pdf", "pdf"
	default:
		return store.ExportJob{}, nil, "", userErr("Unknown export format %q.", format)
	}
	name := SafeName(asm.Bid.Name) + "-proposal." + ext
	if format == "csv" {
		name = SafeName(asm.Bid.Name) + "-compliance-matrix.csv"
	}
	job := store.ExportJob{ID: store.NewID(), BidID: bidID, Format: format, Status: "succeeded", CreatedBy: userID, CreatedAt: a.Now()}
	path, err := a.Files.Put(orgID, "exports/"+bidID, job.ID, name, data)
	if err != nil {
		return job, nil, "", err
	}
	job.FilePath = path
	if err := a.DB.CreateExport(job); err != nil {
		return job, nil, "", err
	}
	a.Activity(orgID, userID, "export.created", "export", job.ID, map[string]any{"format": format, "file": name})
	if format != "csv" && format != "json" {
		a.AdvanceBidStatus(bidID, store.BidReady)
	}
	return job, data, mime, nil
}

// RoundTrip writes approved/latest answers back into the buyer's original questionnaire file.
func (a *App) RoundTrip(orgID, bidID, docID, userID string, approvedOnly bool) (store.ExportJob, []byte, string, error) {
	doc, err := a.DB.GetDocument(orgID, docID)
	if err != nil {
		return store.ExportJob{}, nil, "", err
	}
	if doc.BidID != bidID {
		return store.ExportJob{}, nil, "", store.ErrNotFound
	}
	original, err := a.DocumentBytes(doc)
	if err != nil {
		return store.ExportJob{}, nil, "", err
	}
	reqs, _ := a.DB.ListRequirements(bidID)
	answers, _ := a.DB.LatestAnswersByBid(bidID)
	fills := map[string]string{}
	sheet := ""
	for _, r := range reqs {
		if r.SourceDocumentID != docID || r.ChangeState == "removed" {
			continue
		}
		loc, cell := SourceLocatorParts(r.SourceLocator)
		if cell == "" {
			continue
		}
		ans, ok := answers[r.ID]
		if !ok || (approvedOnly && ans.Status != store.StatusApproved) || ans.Status == store.StatusNeedsEvidence {
			continue
		}
		text := ans.FinalText
		if text == "" {
			text = ans.DraftText
		}
		fills[cell] = reMarkerAll.ReplaceAllString(text, "")
		if i := strings.Index(loc, "!"); i > 0 && sheet == "" {
			sheet = loc[:i]
		}
	}
	if len(fills) == 0 {
		return store.ExportJob{}, nil, "", userErr("No answers are ready to write back into %s (answers must exist and not be Needs Evidence).", doc.Name)
	}
	var out []byte
	switch strings.ToLower(doc.MimeType) {
	case docs.MimeFor("x.xlsx"):
		if sheet == "" {
			return store.ExportJob{}, nil, "", userErr("Could not determine the worksheet for round-trip export.")
		}
		out, err = docs.FillXlsx(original, sheet, fills)
	case docs.MimeFor("x.docx"):
		out, err = docs.FillDocx(original, fills)
	default:
		return store.ExportJob{}, nil, "", userErr("Round-trip export supports XLSX and DOCX questionnaires; %s is %s.", doc.Name, doc.MimeType)
	}
	if err != nil {
		return store.ExportJob{}, nil, "", fmt.Errorf("round-trip fill failed: %w", err)
	}
	job := store.ExportJob{ID: store.NewID(), BidID: bidID, Format: "roundtrip", Status: "succeeded", CreatedBy: userID, CreatedAt: a.Now()}
	name := strings.TrimSuffix(doc.Name, "."+ext(doc.Name)) + "-RESPONSE." + ext(doc.Name)
	path, err := a.Files.Put(orgID, "exports/"+bidID, job.ID, name, out)
	if err != nil {
		return job, nil, "", err
	}
	job.FilePath = path
	if err := a.DB.CreateExport(job); err != nil {
		return job, nil, "", err
	}
	a.Activity(orgID, userID, "export.roundtrip", "export", job.ID, map[string]any{"document": doc.Name, "cells": len(fills)})
	return job, out, doc.MimeType, nil
}

func ext(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return ""
}

// ExportBytes loads a previous export.
func (a *App) ExportBytes(orgID, exportID string) (store.ExportJob, []byte, error) {
	job, err := a.DB.GetExport(exportID)
	if err != nil {
		return job, nil, err
	}
	if _, err := a.DB.GetBid(orgID, job.BidID); err != nil {
		return job, nil, store.ErrNotFound
	}
	data, err := a.Files.Get(job.FilePath)
	return job, data, err
}

// Renderers ---------------------------------------------------------------------------

func (a *App) markdown(asm Assembled) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", asm.Bid.Name)
	fmt.Fprintf(&b, "Prepared by %s for %s. Deadline %s.\n\n", asm.Org.Name, asm.Bid.BuyerName, asm.Bid.Deadline)
	if asm.Org.Mode == "demo" {
		b.WriteString("_SYNTHETIC DEMO — no real customer data._\n\n")
	}
	for _, s := range asm.Sections {
		fmt.Fprintf(&b, "## %s\n\n", s.Section.Title)
		if strings.TrimSpace(s.Section.Content) != "" {
			b.WriteString(s.Section.Content + "\n\n")
		}
		for _, blk := range s.Blocks {
			fmt.Fprintf(&b, "### %s %s\n\n%s\n\n", blk.Requirement.Code, blk.Requirement.Text, blk.Text)
		}
	}
	b.WriteString("## Appendix: Requirement responses\n\n")
	for _, blk := range asm.Answered {
		status := "not started"
		if blk.Answer.ID != "" {
			status = blk.Answer.Status
		}
		fmt.Fprintf(&b, "### %s %s\n\n_Status: %s_\n\n%s\n\n", blk.Requirement.Code, blk.Requirement.Text, status, blk.Text)
	}
	if len(asm.Footnotes) > 0 {
		b.WriteString("## Sources\n\n")
		for _, f := range asm.Footnotes {
			fmt.Fprintf(&b, "[%d] %s — %s\n", f.Number, f.Doc, f.Locator)
		}
	}
	return b.String()
}

func (a *App) plainText(asm Assembled) string {
	md := a.markdown(asm)
	md = strings.ReplaceAll(md, "### ", "")
	md = strings.ReplaceAll(md, "## ", "")
	md = strings.ReplaceAll(md, "# ", "")
	return strings.ReplaceAll(md, "_", "")
}

func (a *App) docxBlocks(asm Assembled) []docs.Block {
	var out []docs.Block
	out = append(out, docs.Block{Kind: "title", Text: asm.Bid.Name})
	out = append(out, docs.Block{Kind: "meta", Text: fmt.Sprintf("Prepared by %s for %s. Deadline %s.", asm.Org.Name, asm.Bid.BuyerName, asm.Bid.Deadline)})
	if asm.Org.Mode == "demo" {
		out = append(out, docs.Block{Kind: "meta", Text: "SYNTHETIC DEMO — no real customer data."})
	}
	for _, s := range asm.Sections {
		out = append(out, docs.Block{Kind: "h1", Text: s.Section.Title})
		for _, para := range strings.Split(s.Section.Content, "\n\n") {
			if strings.TrimSpace(para) != "" {
				out = append(out, docs.Block{Kind: "p", Text: strings.TrimSpace(para)})
			}
		}
		for _, blk := range s.Blocks {
			out = append(out, docs.Block{Kind: "h2", Text: blk.Requirement.Code + " " + blk.Requirement.Text})
			out = append(out, docs.Block{Kind: "p", Text: blk.Text})
		}
	}
	out = append(out, docs.Block{Kind: "h1", Text: "Appendix: Compliance matrix"})
	rows := [][]string{{"ID", "Requirement", "Status", "Evidence", "Response"}}
	for _, blk := range asm.Answered {
		status, label := "not started", ""
		if blk.Answer.ID != "" {
			status, label = blk.Answer.Status, blk.Answer.ConfidenceLabel
		}
		rows = append(rows, []string{blk.Requirement.Code, blk.Requirement.Text, status, label, blk.Text})
	}
	out = append(out, docs.Block{Kind: "table", Rows: rows})
	if len(asm.Footnotes) > 0 {
		out = append(out, docs.Block{Kind: "h1", Text: "Sources"})
		for _, f := range asm.Footnotes {
			out = append(out, docs.Block{Kind: "p", Text: fmt.Sprintf("[%d] %s — %s", f.Number, f.Doc, f.Locator)})
		}
	}
	return out
}

func (a *App) printHTML(asm Assembled) string {
	var b strings.Builder
	esc := html.EscapeString
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>" + esc(asm.Bid.Name) + "</title><style>body{font-family:Georgia,serif;max-width:800px;margin:40px auto;line-height:1.5;color:#111}h1{font-size:28px}h2{font-size:20px;border-bottom:1px solid #ccc;padding-bottom:4px;margin-top:32px}h3{font-size:15px;margin-bottom:4px}.meta{color:#666;font-size:13px}.src{font-size:12px;color:#444}@media print{a{color:inherit;text-decoration:none}}</style></head><body>")
	fmt.Fprintf(&b, "<h1>%s</h1><p class=\"meta\">Prepared by %s for %s. Deadline %s.</p>", esc(asm.Bid.Name), esc(asm.Org.Name), esc(asm.Bid.BuyerName), esc(asm.Bid.Deadline))
	if asm.Org.Mode == "demo" {
		b.WriteString("<p class=\"meta\">SYNTHETIC DEMO — no real customer data.</p>")
	}
	for _, s := range asm.Sections {
		fmt.Fprintf(&b, "<h2>%s</h2>", esc(s.Section.Title))
		for _, para := range strings.Split(s.Section.Content, "\n\n") {
			if strings.TrimSpace(para) != "" {
				fmt.Fprintf(&b, "<p>%s</p>", esc(strings.TrimSpace(para)))
			}
		}
		for _, blk := range s.Blocks {
			fmt.Fprintf(&b, "<h3>%s %s</h3><p>%s</p>", esc(blk.Requirement.Code), esc(blk.Requirement.Text), esc(blk.Text))
		}
	}
	b.WriteString("<h2>Appendix: Requirement responses</h2>")
	for _, blk := range asm.Answered {
		fmt.Fprintf(&b, "<h3>%s %s</h3><p>%s</p>", esc(blk.Requirement.Code), esc(blk.Requirement.Text), esc(blk.Text))
	}
	if len(asm.Footnotes) > 0 {
		b.WriteString("<h2>Sources</h2>")
		for _, f := range asm.Footnotes {
			fmt.Fprintf(&b, "<p class=\"src\">[%d] %s — %s</p>", f.Number, esc(f.Doc), esc(f.Locator))
		}
	}
	b.WriteString("</body></html>")
	return b.String()
}

func (a *App) exportJSON(orgID string, asm Assembled) []byte {
	type cite struct {
		Marker  int    `json:"marker"`
		ChunkID string `json:"chunkId"`
		Doc     string `json:"document"`
		Locator string `json:"locator"`
	}
	type req struct {
		Code, Section, Text, Category string
		Mandatory                     bool
		Status, Label, Response       string
		Citations                     []cite
		SourceLocator                 string
	}
	out := map[string]any{"bid": asm.Bid, "organization": map[string]any{"name": asm.Org.Name, "mode": asm.Org.Mode}, "synthetic": asm.Org.Mode == "demo", "exportedAt": a.Now()}
	var reqs []req
	for _, blk := range asm.Answered {
		x := req{Code: blk.Requirement.Code, Section: blk.Requirement.Section, Text: blk.Requirement.Text, Category: blk.Requirement.Category, Mandatory: blk.Requirement.Mandatory, SourceLocator: blk.Requirement.SourceLocator, Response: blk.Text}
		if blk.Answer.ID != "" {
			x.Status, x.Label = blk.Answer.Status, blk.Answer.ConfidenceLabel
		}
		for _, n := range blk.Notes {
			x.Citations = append(x.Citations, cite{Marker: n.Number, ChunkID: n.ChunkID, Doc: n.Doc, Locator: n.Locator})
		}
		reqs = append(reqs, x)
	}
	out["requirements"] = reqs
	var sections []map[string]any
	for _, s := range asm.Sections {
		var ids []string
		for _, b := range s.Blocks {
			ids = append(ids, b.Requirement.Code)
		}
		sections = append(sections, map[string]any{"title": s.Section.Title, "content": s.Section.Content, "answers": ids})
	}
	out["sections"] = sections
	b, _ := json.MarshalIndent(out, "", "  ")
	return b
}

func (a *App) matrixCSV(orgID string, asm Assembled) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"Requirement ID", "Source", "Requirement", "Category", "Mandatory", "Owner", "Response status", "Evidence coverage", "Citation count", "Reviewer", "Comments", "Last updated"})
	users, _ := a.DB.UserMap(orgID)
	pack := a.Pack(orgID)
	for _, blk := range asm.Answered {
		r := blk.Requirement
		status, label, updated, comments := "not_started", "", r.UpdatedAt, ""
		if blk.Answer.ID != "" {
			status, label, updated = blk.Answer.Status, blk.Answer.ConfidenceLabel, blk.Answer.UpdatedAt
			if blk.Answer.EvidenceGaps != "" {
				comments = blk.Answer.EvidenceGaps
			}
		}
		loc, _ := SourceLocatorParts(r.SourceLocator)
		_ = w.Write([]string{r.Code, loc, r.Text, pack.CategoryLabel(r.Category), fmt.Sprint(r.Mandatory), users[r.OwnerID].Name, status, label, fmt.Sprint(len(blk.Notes)), users[r.ReviewerID].Name, comments, updated})
	}
	w.Flush()
	return buf.Bytes()
}
