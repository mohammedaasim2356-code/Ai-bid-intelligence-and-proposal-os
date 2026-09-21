package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bidos/internal/docs"
	"bidos/internal/store"
)

// IngestInput describes a document to store, parse, chunk and (for knowledge) embed.
type IngestInput struct {
	ID               string // optional deterministic id
	OrgID            string
	BidID            string
	UserID           string
	Name             string
	Data             []byte
	Category         string
	ApprovalState    string
	ExpiresAt        string
	ClientDisclosure string
	Trust            string
	SourceType       string
	Version          int
	Tags             []string
	Metadata         map[string]any
	CreatedAt        string
	SkipEmbedding    bool
}

// IngestDocument validates, stores, parses and chunks a file. Malformed files are rejected
// with a readable message and nothing is persisted.
func (a *App) IngestDocument(ctx context.Context, in IngestInput) (store.Document, *docs.ParsedDocument, error) {
	if len(in.Data) == 0 {
		return store.Document{}, nil, userErr("The file %q is empty.", in.Name)
	}
	if a.Cfg.MaxUploadBytes > 0 && int64(len(in.Data)) > a.Cfg.MaxUploadBytes {
		return store.Document{}, nil, userErr("The file %q is larger than the %d MB upload limit.", in.Name, a.Cfg.MaxUploadBytes>>20)
	}
	name := SafeName(in.Name)
	if err := docs.Sniff(name, in.Data); err != nil {
		return store.Document{}, nil, userErr("%s could not be imported: %v", name, err)
	}
	parsed, err := docs.Parse(name, in.Data)
	if err != nil {
		return store.Document{}, nil, userErr("%s could not be parsed: %v", name, err)
	}
	id := in.ID
	if id == "" {
		id = store.NewID()
	}
	now := a.Now()
	if in.CreatedAt != "" {
		now = in.CreatedAt
	}
	kind := "knowledge"
	if in.BidID != "" {
		kind = "bids/" + in.BidID
	}
	path, err := a.Files.Put(in.OrgID, kind, id, name, in.Data)
	if err != nil {
		return store.Document{}, nil, err
	}
	if in.ApprovalState == "" {
		in.ApprovalState = "unapproved"
	}
	if in.Trust == "" {
		if in.BidID != "" {
			in.Trust = "untrusted_external"
		} else {
			in.Trust = "internal"
		}
	}
	if in.SourceType == "" {
		in.SourceType = "upload"
	}
	if in.Version == 0 {
		in.Version = 1
	}
	meta := in.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	meta["pages"] = parsed.Pages
	meta["sections"] = len(parsed.Sections)
	meta["tables"] = len(parsed.Tables)
	doc := store.Document{ID: id, OrgID: in.OrgID, BidID: in.BidID, Name: name, MimeType: parsed.MimeType, Category: in.Category, Version: in.Version, ApprovalState: in.ApprovalState, SourceType: in.SourceType, FilePath: path, Hash: parsed.SourceHash, ExpiresAt: in.ExpiresAt, ClientDisclosure: in.ClientDisclosure, Trust: in.Trust, Tags: in.Tags, Metadata: meta, CreatedAt: now}
	if err := a.DB.CreateDocument(doc); err != nil {
		return doc, nil, err
	}
	chunks := docs.Chunk(parsed, 1200)
	rows := make([]store.Chunk, 0, len(chunks))
	texts := make([]string, 0, len(chunks))
	for i, c := range chunks {
		rows = append(rows, store.Chunk{ID: fmt.Sprintf("%s-c%03d", id, i), DocumentID: id, SectionPath: c.SectionPath, Text: c.Text, PageNumber: c.Page, SheetName: c.Sheet, CellRange: c.CellRange, ChunkIndex: i, TextHash: c.TextHash})
		texts = append(texts, c.Text)
	}
	if in.BidID == "" && !in.SkipEmbedding && a.Embedder != nil {
		if vecs := a.Embed(ctx, texts); len(vecs) == len(rows) {
			for i := range rows {
				rows[i].Embedding = vecs[i]
				rows[i].EmbeddingModelID = a.Embedder.ModelID()
			}
		}
	}
	if err := a.DB.InsertChunks(rows); err != nil {
		return doc, nil, err
	}
	a.Activity(in.OrgID, in.UserID, "document.ingested", "document", id, map[string]any{"name": name, "chunks": len(rows), "bidId": in.BidID})
	return doc, parsed, nil
}

// DocumentBytes loads the stored file for a document.
func (a *App) DocumentBytes(doc store.Document) ([]byte, error) { return a.Files.Get(doc.FilePath) }

// ParseStored re-parses a stored document.
func (a *App) ParseStored(doc store.Document) (*docs.ParsedDocument, error) {
	data, err := a.DocumentBytes(doc)
	if err != nil {
		return nil, err
	}
	return docs.Parse(doc.Name, data)
}

// SetDocumentApproval changes approval state and re-reviews answers citing the document.
func (a *App) SetDocumentApproval(orgID, docID, state, userID string) error {
	doc, err := a.DB.GetDocument(orgID, docID)
	if err != nil {
		return err
	}
	if state != "approved" && state != "unapproved" && state != "superseded" {
		return userErr("Approval state must be approved, unapproved or superseded.")
	}
	doc.ApprovalState = state
	if err := a.DB.UpdateDocument(doc); err != nil {
		return err
	}
	a.Activity(orgID, userID, "document.approval", "document", docID, map[string]any{"state": state})
	a.ReReviewCiting(orgID, docID, "Knowledge document "+doc.Name+" changed to "+state)
	return nil
}

// UpdateDocumentMeta edits category, tags, expiry and disclosure.
func (a *App) UpdateDocumentMeta(orgID, docID, userID, category, expiresAt, disclosure string, tags []string) error {
	doc, err := a.DB.GetDocument(orgID, docID)
	if err != nil {
		return err
	}
	if expiresAt != "" {
		if _, ok := store.ParseTime(expiresAt); !ok {
			return userErr("Expiry %q is not a valid date (YYYY-MM-DD).", expiresAt)
		}
	}
	doc.Category, doc.ExpiresAt, doc.ClientDisclosure, doc.Tags = category, expiresAt, disclosure, tags
	if err := a.DB.UpdateDocument(doc); err != nil {
		return err
	}
	a.Activity(orgID, userID, "document.updated", "document", docID, nil)
	return nil
}

// NewDocumentVersion ingests a replacement file, supersedes the old version and re-reviews citing answers.
func (a *App) NewDocumentVersion(ctx context.Context, orgID, docID, userID, name string, data []byte) (store.Document, error) {
	old, err := a.DB.GetDocument(orgID, docID)
	if err != nil {
		return old, err
	}
	doc, _, err := a.IngestDocument(ctx, IngestInput{OrgID: orgID, BidID: old.BidID, UserID: userID, Name: name, Data: data, Category: old.Category, ApprovalState: "unapproved", ExpiresAt: a.DefaultExpiry(orgID, old.Category), ClientDisclosure: old.ClientDisclosure, Trust: old.Trust, Version: old.Version + 1, Tags: old.Tags, Metadata: map[string]any{"supersedes": old.ID}})
	if err != nil {
		return doc, err
	}
	old.ApprovalState = "superseded"
	_ = a.DB.UpdateDocument(old)
	a.ReReviewCiting(orgID, old.ID, "A new version of "+old.Name+" was uploaded")
	return doc, nil
}

// DeleteDocument removes a document, its chunks and file.
func (a *App) DeleteDocument(orgID, docID, userID string) error {
	doc, err := a.DB.GetDocument(orgID, docID)
	if err != nil {
		return err
	}
	a.ReReviewCiting(orgID, docID, "Knowledge document "+doc.Name+" was deleted")
	if err := a.DB.DeleteDocument(orgID, docID); err != nil {
		return err
	}
	_ = a.Files.Delete(doc.FilePath)
	a.Activity(orgID, userID, "document.deleted", "document", docID, map[string]any{"name": doc.Name})
	return nil
}

// ReReviewCiting marks every latest answer citing the document as Needs Re-review.
func (a *App) ReReviewCiting(orgID, docID, reason string) int {
	answers, err := a.DB.AnswersCitingDocument(docID)
	if err != nil {
		return 0
	}
	n := 0
	for _, ans := range answers {
		latest, err := a.DB.LatestAnswer(ans.RequirementID)
		if err != nil || latest.ID != ans.ID {
			continue
		}
		if latest.Status == store.StatusRejected {
			continue
		}
		latest.Status = store.StatusNeedsReReview
		latest.UpdatedAt = a.Now()
		_ = a.DB.UpdateAnswer(latest)
		_ = a.DB.SetRequirementStatus(latest.RequirementID, store.StatusNeedsReReview, a.Now())
		if req, err := a.DB.GetRequirement(latest.RequirementID); err == nil {
			a.Activity(orgID, "", "answer.re_review", "requirement", req.ID, map[string]any{"reason": reason})
			if req.OwnerID != "" {
				a.Notify(orgID, req.OwnerID, "Answer needs re-review: "+req.Code, reason, "/bids/"+req.BidID+"/requirements/"+req.ID)
			}
		}
		n++
	}
	return n
}

// DefaultExpiry computes an expiry date from the pack's knowledge type (empty = none).
func (a *App) DefaultExpiry(orgID, category string) string {
	months := a.Pack(orgID).ExpiryMonths(category)
	if months <= 0 {
		return ""
	}
	return time.Now().AddDate(0, months, 0).Format("2006-01-02")
}

// EmbedMissing embeds knowledge chunks lacking a vector for the current model.
func (a *App) EmbedMissing(ctx context.Context, orgID string) (int, error) {
	if a.Embedder == nil {
		return 0, nil
	}
	chunks, err := a.DB.ChunksMissingEmbedding(orgID, a.Embedder.ModelID())
	if err != nil || len(chunks) == 0 {
		return 0, err
	}
	done := 0
	for i := 0; i < len(chunks); i += 32 {
		end := min(i+32, len(chunks))
		batch := chunks[i:end]
		texts := make([]string, len(batch))
		for j, c := range batch {
			texts[j] = c.Text
		}
		vecs := a.Embed(ctx, texts)
		if len(vecs) != len(batch) {
			return done, fmt.Errorf("embedding batch failed")
		}
		for j, c := range batch {
			if err := a.DB.UpdateChunkEmbedding(c.ID, vecs[j], a.Embedder.ModelID()); err != nil {
				return done, err
			}
			done++
		}
	}
	return done, nil
}

// ExpiredDocuments lists approved knowledge documents past expiry.
func (a *App) ExpiredDocuments(orgID string) []store.Document {
	all, _ := a.DB.ListKnowledgeDocs(orgID)
	today := time.Now().UTC().Format("2006-01-02")
	var out []store.Document
	for _, d := range all {
		if d.ExpiresAt != "" && d.ExpiresAt < today && d.ApprovalState == "approved" {
			out = append(out, d)
		}
	}
	return out
}

// DocDisplayName is a helper for citations.
func DocDisplayName(name string, version int) string {
	if version > 1 {
		return fmt.Sprintf("%s (v%d)", strings.TrimSuffix(name, ".md"), version)
	}
	return strings.TrimSuffix(name, ".md")
}
