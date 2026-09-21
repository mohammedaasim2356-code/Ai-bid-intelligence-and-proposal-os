package store

import (
	"testing"
	"time"
)

func TestRoundTripAndJobClaim(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := Now()
	if err := db.CreateOrg(Organization{ID: "org1", Name: "Test", Mode: "demo", VerticalPackID: "saas-it", DataSensitivity: "synthetic", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser(User{ID: "u1", OrgID: "org1", Name: "A", Email: "a@x", Role: RoleAdmin, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateBid(Bid{ID: "b1", OrgID: "org1", Name: "Bid", Status: BidIntake, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetBid("other-org", "b1"); err != ErrNotFound {
		t.Fatalf("org isolation broken: %v", err)
	}
	doc := Document{ID: "d1", OrgID: "org1", Name: "Doc", Tags: []string{"a"}, Metadata: map[string]any{"synthetic": true}, CreatedAt: now, Version: 1, ApprovalState: "approved"}
	if err := db.CreateDocument(doc); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertChunks([]Chunk{{ID: "c1", DocumentID: "d1", Text: "hello", ChunkIndex: 0, Embedding: []float32{0.5, -1}}}); err != nil {
		t.Fatal(err)
	}
	chunks, err := db.KnowledgeChunks("org1")
	if err != nil || len(chunks) != 1 || chunks[0].Embedding[1] != -1 || chunks[0].DocApproval != "approved" {
		t.Fatalf("chunk join wrong: %v %+v", err, chunks)
	}
	if err := db.CreateRequirement(Requirement{ID: "r1", BidID: "b1", Code: "1.1", Text: "Must do X", Mandatory: true, Status: StatusNotStarted, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAnswer(Answer{ID: "a1", RequirementID: "r1", DraftText: "x", Status: StatusDrafted, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAnswer(Answer{ID: "a2", RequirementID: "r1", DraftText: "y", Status: StatusDrafted, Version: 2, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	latest, err := db.LatestAnswersByBid("b1")
	if err != nil || latest["r1"].ID != "a2" {
		t.Fatalf("latest answer wrong: %v %+v", err, latest)
	}
	if err := db.ReplaceCitations("a2", []Citation{{Marker: 1, ChunkID: "c1"}}); err != nil {
		t.Fatal(err)
	}
	cites, err := db.CitationsForAnswer("a2")
	if err != nil || len(cites) != 1 || cites[0].DocName != "Doc" {
		t.Fatalf("citations wrong: %v %+v", err, cites)
	}
	// QA preserves dismissals by fingerprint
	if err := db.ReplaceQAResults("b1", []QAResult{{CheckType: "x", Severity: "warning", Message: "m", Fingerprint: "fp1", Status: "open", CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	qa, _ := db.ListQA("b1")
	if err := db.SetQAStatus(qa[0].ID, "dismissed", "not relevant"); err != nil {
		t.Fatal(err)
	}
	_ = db.ReplaceQAResults("b1", []QAResult{{CheckType: "x", Severity: "warning", Message: "m", Fingerprint: "fp1", Status: "open", CreatedAt: now}})
	qa, _ = db.ListQA("b1")
	if qa[0].Status != "dismissed" {
		t.Fatalf("dismissal not preserved: %+v", qa)
	}
	// jobs: idempotency + lease claim
	j1, err := db.EnqueueJob(Job{OrgID: "org1", Type: "t", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	j2, _ := db.EnqueueJob(Job{OrgID: "org1", Type: "t", IdempotencyKey: "k1"})
	if j1.ID != j2.ID {
		t.Fatal("idempotency key ignored")
	}
	lease := FormatTime(time.Now().Add(time.Minute))
	claimed, err := db.ClaimJob(Now(), lease)
	if err != nil || claimed.ID != j1.ID || claimed.Attempts != 1 {
		t.Fatalf("claim failed: %v %+v", err, claimed)
	}
	if _, err := db.ClaimJob(Now(), lease); err != ErrNotFound {
		t.Fatalf("leased job re-claimed: %v", err)
	}
	// expired lease is reclaimable
	if err := db.ExtendLease(j1.ID, FormatTime(time.Now().Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	if c, err := db.ClaimJob(Now(), lease); err != nil || c.Attempts != 2 {
		t.Fatalf("expired lease not reclaimable: %v %+v", err, c)
	}
	// settings + cache
	_ = db.SetSetting("org1", "k", "v")
	_ = db.SetSetting("org1", "k", "v2")
	if db.GetSetting("org1", "k", "") != "v2" {
		t.Fatal("setting upsert failed")
	}
	_ = db.CachePut("key", "demo", "m", "{}")
	if v, ok := db.CacheGet("key"); !ok || v != "{}" {
		t.Fatal("cache failed")
	}
	if err := db.DeleteOrg("org1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Count("SELECT COUNT(*) FROM document_chunks"); n != 0 {
		t.Fatal("cascade delete failed")
	}
}
