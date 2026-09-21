package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// Organizations ---------------------------------------------------------------

const orgCols = "id, name, mode, vertical_pack_id, data_sensitivity, expires_at, created_at"

func scanOrg(r scanner) (Organization, error) {
	var o Organization
	err := r.Scan(&o.ID, &o.Name, &o.Mode, &o.VerticalPackID, &o.DataSensitivity, &o.ExpiresAt, &o.CreatedAt)
	return o, err
}

func (s *DB) CreateOrg(o Organization) error {
	return s.Exec("INSERT INTO organizations ("+orgCols+") VALUES (?,?,?,?,?,?,?)",
		o.ID, o.Name, o.Mode, o.VerticalPackID, o.DataSensitivity, o.ExpiresAt, o.CreatedAt)
}

func (s *DB) GetOrg(id string) (Organization, error) {
	return one(s.q, scanOrg, "SELECT "+orgCols+" FROM organizations WHERE id = ?", id)
}

func (s *DB) ListOrgs() ([]Organization, error) {
	return all(s.q, scanOrg, "SELECT "+orgCols+" FROM organizations ORDER BY created_at, id")
}

func (s *DB) DeleteOrg(id string) error {
	// Explicit deletes for tables without a foreign key to organizations.
	for _, q := range []string{
		"DELETE FROM jobs WHERE org_id = ?", "DELETE FROM provider_calls WHERE org_id = ?",
		"DELETE FROM activity_log WHERE org_id = ?", "DELETE FROM notifications WHERE org_id = ?",
		"DELETE FROM settings WHERE org_id = ?", "DELETE FROM sessions WHERE org_id = ?",
		"DELETE FROM organizations WHERE id = ?",
	} {
		if err := s.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *DB) ExpiredOrgs(now string) ([]Organization, error) {
	return all(s.q, scanOrg, "SELECT "+orgCols+" FROM organizations WHERE expires_at <> '' AND expires_at < ?", now)
}

// Users -----------------------------------------------------------------------

const userCols = "id, org_id, name, email, role, password_hash, created_at"

func scanUser(r scanner) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.OrgID, &u.Name, &u.Email, &u.Role, &u.PasswordHash, &u.CreatedAt)
	return u, err
}

func (s *DB) CreateUser(u User) error {
	return s.Exec("INSERT INTO users ("+userCols+") VALUES (?,?,?,?,?,?,?)",
		u.ID, u.OrgID, u.Name, u.Email, u.Role, u.PasswordHash, u.CreatedAt)
}

func (s *DB) GetUser(id string) (User, error) {
	return one(s.q, scanUser, "SELECT "+userCols+" FROM users WHERE id = ?", id)
}

func (s *DB) UserByEmail(orgID, email string) (User, error) {
	return one(s.q, scanUser, "SELECT "+userCols+" FROM users WHERE org_id = ? AND lower(email) = lower(?)", orgID, email)
}

func (s *DB) ListUsers(orgID string) ([]User, error) {
	return all(s.q, scanUser, "SELECT "+userCols+" FROM users WHERE org_id = ? ORDER BY created_at, id", orgID)
}

func (s *DB) UsersByRole(orgID, role string) ([]User, error) {
	return all(s.q, scanUser, "SELECT "+userCols+" FROM users WHERE org_id = ? AND role = ? ORDER BY created_at, id", orgID, role)
}

// UserMap returns id → user for an organization.
func (s *DB) UserMap(orgID string) (map[string]User, error) {
	users, err := s.ListUsers(orgID)
	if err != nil {
		return nil, err
	}
	m := make(map[string]User, len(users))
	for _, u := range users {
		m[u.ID] = u
	}
	return m, nil
}

// Sessions --------------------------------------------------------------------

func (s *DB) CreateSession(sess Session) error {
	return s.Exec("INSERT INTO sessions (id, user_id, org_id, expires_at) VALUES (?,?,?,?)", sess.ID, sess.UserID, sess.OrgID, sess.ExpiresAt)
}

func (s *DB) GetSession(id string) (Session, error) {
	return one(s.q, func(r scanner) (Session, error) {
		var x Session
		err := r.Scan(&x.ID, &x.UserID, &x.OrgID, &x.ExpiresAt)
		return x, err
	}, "SELECT id, user_id, org_id, expires_at FROM sessions WHERE id = ?", id)
}

func (s *DB) DeleteSession(id string) error { return s.Exec("DELETE FROM sessions WHERE id = ?", id) }

func (s *DB) PurgeSessions(now string) error {
	return s.Exec("DELETE FROM sessions WHERE expires_at < ?", now)
}

// Settings --------------------------------------------------------------------

func (s *DB) GetSetting(orgID, key, def string) string {
	var v string
	if err := s.q.QueryRow("SELECT value FROM settings WHERE org_id = ? AND key = ?", orgID, key).Scan(&v); err != nil {
		return def
	}
	return v
}

func (s *DB) SetSetting(orgID, key, value string) error {
	return s.Exec("INSERT INTO settings (org_id, key, value) VALUES (?,?,?) ON CONFLICT(org_id, key) DO UPDATE SET value = excluded.value", orgID, key, value)
}

func (s *DB) AllSettings(orgID string) (map[string]string, error) {
	rows, err := s.q.Query("SELECT key, value FROM settings WHERE org_id = ?", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Bids ------------------------------------------------------------------------

const bidCols = "id, org_id, name, buyer_name, buyer_url, owner_id, estimated_value, deadline, status, description, decision, created_at, updated_at"

func scanBid(r scanner) (Bid, error) {
	var b Bid
	err := r.Scan(&b.ID, &b.OrgID, &b.Name, &b.BuyerName, &b.BuyerURL, &b.OwnerID, &b.EstimatedValue, &b.Deadline, &b.Status, &b.Description, &b.Decision, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

func (s *DB) CreateBid(b Bid) error {
	return s.Exec("INSERT INTO bids ("+bidCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
		b.ID, b.OrgID, b.Name, b.BuyerName, b.BuyerURL, b.OwnerID, b.EstimatedValue, b.Deadline, b.Status, b.Description, b.Decision, b.CreatedAt, b.UpdatedAt)
}

func (s *DB) UpdateBid(b Bid) error {
	return s.Exec("UPDATE bids SET name=?, buyer_name=?, buyer_url=?, owner_id=?, estimated_value=?, deadline=?, status=?, description=?, decision=?, updated_at=? WHERE id=?",
		b.Name, b.BuyerName, b.BuyerURL, b.OwnerID, b.EstimatedValue, b.Deadline, b.Status, b.Description, b.Decision, b.UpdatedAt, b.ID)
}

func (s *DB) SetBidStatus(id, status, now string) error {
	return s.Exec("UPDATE bids SET status=?, updated_at=? WHERE id=?", status, now, id)
}

// GetBid loads a bid scoped to an organization; a mismatch is ErrNotFound (isolation).
func (s *DB) GetBid(orgID, id string) (Bid, error) {
	return one(s.q, scanBid, "SELECT "+bidCols+" FROM bids WHERE org_id = ? AND id = ?", orgID, id)
}

func (s *DB) GetBidByID(id string) (Bid, error) {
	return one(s.q, scanBid, "SELECT "+bidCols+" FROM bids WHERE id = ?", id)
}

func (s *DB) ListBids(orgID string) ([]Bid, error) {
	return all(s.q, scanBid, "SELECT "+bidCols+" FROM bids WHERE org_id = ? ORDER BY deadline, created_at, id", orgID)
}

func (s *DB) DeleteBid(orgID, id string) error {
	return s.Exec("DELETE FROM bids WHERE org_id = ? AND id = ?", orgID, id)
}

// Documents -------------------------------------------------------------------

const docCols = "id, org_id, bid_id, name, mime_type, category, version, approval_state, source_type, file_path, hash, expires_at, client_disclosure, trust, tags, metadata, parse_error, created_at"

func scanDoc(r scanner) (Document, error) {
	var d Document
	var tags, meta string
	err := r.Scan(&d.ID, &d.OrgID, &d.BidID, &d.Name, &d.MimeType, &d.Category, &d.Version, &d.ApprovalState, &d.SourceType, &d.FilePath, &d.Hash, &d.ExpiresAt, &d.ClientDisclosure, &d.Trust, &tags, &meta, &d.ParseError, &d.CreatedAt)
	d.Tags = strs(tags)
	d.Metadata = unjs[map[string]any](meta)
	if d.Metadata == nil {
		d.Metadata = map[string]any{}
	}
	return d, err
}

func (s *DB) CreateDocument(d Document) error {
	if d.Tags == nil {
		d.Tags = []string{}
	}
	if d.Metadata == nil {
		d.Metadata = map[string]any{}
	}
	return s.Exec("INSERT INTO documents ("+docCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		d.ID, d.OrgID, d.BidID, d.Name, d.MimeType, d.Category, d.Version, d.ApprovalState, d.SourceType, d.FilePath, d.Hash, d.ExpiresAt, d.ClientDisclosure, d.Trust, js(d.Tags), js(d.Metadata), d.ParseError, d.CreatedAt)
}

func (s *DB) UpdateDocument(d Document) error {
	return s.Exec("UPDATE documents SET name=?, mime_type=?, category=?, version=?, approval_state=?, source_type=?, file_path=?, hash=?, expires_at=?, client_disclosure=?, trust=?, tags=?, metadata=?, parse_error=? WHERE id=?",
		d.Name, d.MimeType, d.Category, d.Version, d.ApprovalState, d.SourceType, d.FilePath, d.Hash, d.ExpiresAt, d.ClientDisclosure, d.Trust, js(d.Tags), js(d.Metadata), d.ParseError, d.ID)
}

func (s *DB) GetDocument(orgID, id string) (Document, error) {
	return one(s.q, scanDoc, "SELECT "+docCols+" FROM documents WHERE org_id = ? AND id = ?", orgID, id)
}

func (s *DB) GetDocumentByID(id string) (Document, error) {
	return one(s.q, scanDoc, "SELECT "+docCols+" FROM documents WHERE id = ?", id)
}

// ListKnowledgeDocs returns organization-level (non-bid) documents.
func (s *DB) ListKnowledgeDocs(orgID string) ([]Document, error) {
	return all(s.q, scanDoc, "SELECT "+docCols+" FROM documents WHERE org_id = ? AND bid_id = '' ORDER BY category, name, version", orgID)
}

func (s *DB) ListBidDocuments(bidID string) ([]Document, error) {
	return all(s.q, scanDoc, "SELECT "+docCols+" FROM documents WHERE bid_id = ? ORDER BY created_at, id", bidID)
}

func (s *DB) ListAllDocuments(orgID string) ([]Document, error) {
	return all(s.q, scanDoc, "SELECT "+docCols+" FROM documents WHERE org_id = ? ORDER BY created_at, id", orgID)
}

func (s *DB) DeleteDocument(orgID, id string) error {
	return s.Exec("DELETE FROM documents WHERE org_id = ? AND id = ?", orgID, id)
}

func (s *DB) DocumentMap(orgID string) (map[string]Document, error) {
	docs, err := s.ListAllDocuments(orgID)
	if err != nil {
		return nil, err
	}
	m := make(map[string]Document, len(docs))
	for _, d := range docs {
		m[d.ID] = d
	}
	return m, nil
}

// Chunks ----------------------------------------------------------------------

const chunkCols = "id, document_id, section_path, text, page_number, sheet_name, cell_range, chunk_index, text_hash, embedding, embedding_model_id"

func scanChunk(r scanner) (Chunk, error) {
	var c Chunk
	var emb []byte
	err := r.Scan(&c.ID, &c.DocumentID, &c.SectionPath, &c.Text, &c.PageNumber, &c.SheetName, &c.CellRange, &c.ChunkIndex, &c.TextHash, &emb, &c.EmbeddingModelID)
	c.Embedding = DecodeVec(emb)
	return c, err
}

func (s *DB) InsertChunks(chunks []Chunk) error {
	return s.WithTx(func(tx *DB) error {
		for _, c := range chunks {
			if err := tx.Exec("INSERT INTO document_chunks ("+chunkCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?)",
				c.ID, c.DocumentID, c.SectionPath, c.Text, c.PageNumber, c.SheetName, c.CellRange, c.ChunkIndex, c.TextHash, EncodeVec(c.Embedding), c.EmbeddingModelID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *DB) DeleteChunksByDocument(docID string) error {
	return s.Exec("DELETE FROM document_chunks WHERE document_id = ?", docID)
}

func (s *DB) ChunksByDocument(docID string) ([]Chunk, error) {
	return all(s.q, scanChunk, "SELECT "+chunkCols+" FROM document_chunks WHERE document_id = ? ORDER BY chunk_index", docID)
}

func (s *DB) GetChunk(id string) (Chunk, error) {
	return one(s.q, scanChunk, "SELECT "+chunkCols+" FROM document_chunks WHERE id = ?", id)
}

func (s *DB) UpdateChunkEmbedding(id string, vec []float32, modelID string) error {
	return s.Exec("UPDATE document_chunks SET embedding = ?, embedding_model_id = ? WHERE id = ?", EncodeVec(vec), modelID, id)
}

const chunkInfoCols = "c.id, c.document_id, c.section_path, c.text, c.page_number, c.sheet_name, c.cell_range, c.chunk_index, c.text_hash, c.embedding, c.embedding_model_id, d.name, d.category, d.approval_state, d.expires_at, d.client_disclosure, d.trust, d.bid_id, d.version"

func scanChunkInfo(r scanner) (ChunkInfo, error) {
	var c ChunkInfo
	var emb []byte
	err := r.Scan(&c.ID, &c.DocumentID, &c.SectionPath, &c.Text, &c.PageNumber, &c.SheetName, &c.CellRange, &c.ChunkIndex, &c.TextHash, &emb, &c.EmbeddingModelID,
		&c.DocName, &c.DocCategory, &c.DocApproval, &c.DocExpiresAt, &c.DocClientDisclosure, &c.DocTrust, &c.DocBidID, &c.DocVersion)
	c.Embedding = DecodeVec(emb)
	return c, err
}

// KnowledgeChunks returns every chunk of the organization's knowledge documents (bid_id = ”).
func (s *DB) KnowledgeChunks(orgID string) ([]ChunkInfo, error) {
	return all(s.q, scanChunkInfo, "SELECT "+chunkInfoCols+" FROM document_chunks c JOIN documents d ON d.id = c.document_id WHERE d.org_id = ? AND d.bid_id = '' ORDER BY d.name, c.chunk_index", orgID)
}

// ChunkInfosByIDs loads chunks (with document info) by id, preserving the requested order.
func (s *DB) ChunkInfosByIDs(ids []string) ([]ChunkInfo, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		marks[i] = "?"
	}
	rows, err := all(s.q, scanChunkInfo, "SELECT "+chunkInfoCols+" FROM document_chunks c JOIN documents d ON d.id = c.document_id WHERE c.id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	byID := map[string]ChunkInfo{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]ChunkInfo, 0, len(ids))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// ChunksMissingEmbedding lists knowledge chunks whose embedding is absent or from another model.
func (s *DB) ChunksMissingEmbedding(orgID, modelID string) ([]Chunk, error) {
	return all(s.q, scanChunk, "SELECT "+chunkCols+" FROM document_chunks WHERE document_id IN (SELECT id FROM documents WHERE org_id = ? AND bid_id = '') AND (embedding IS NULL OR embedding_model_id <> ?)", orgID, modelID)
}

// helper for optional scanning of nullable text
func nullStr(ns sql.NullString) string { return ns.String }

var _ = fmt.Sprintf
var _ = nullStr
