package retrieval

import (
	"context"
	"testing"

	"bidos/internal/store"
)

func chunk(id, doc, text string) store.ChunkInfo {
	c := store.ChunkInfo{}
	c.ID, c.DocumentID, c.Text = id, doc, text
	c.DocApproval = "approved"
	return c
}

func TestHardTermsAndGate(t *testing.T) {
	ht := HardTerms("Provide evidence of ISO 27001 certification, including the certificate scope and expiry date.", []string{"Northstar"})
	if len(ht) != 2 || ht[0] != "ISO" || ht[1] != "27001" {
		t.Fatalf("hard terms: %v", ht)
	}
	ht = HardTerms("The vendor shall provide SAML 2.0 based single sign-on and enforce multi-factor authentication for all administrative users.", nil)
	if len(ht) != 2 {
		t.Fatalf("hard terms: %v", ht)
	}
	chunks := []store.ChunkInfo{
		chunk("c1", "d1", "Single sign-on is provided through SAML 2.0 with SCIM provisioning. Multi-factor authentication (MFA) is enforced for all administrative users."),
		chunk("c2", "d2", "Northstar holds a current SOC 2 Type II report covering the Security, Availability and Confidentiality criteria."),
		chunk("c3", "d3", "Payment is due net 30 days from invoice date; liability is capped at fees paid in the 12 months preceding the claim."),
	}
	emb := HashEmbedder{}
	for i := range chunks {
		v, _ := emb.Embed(context.Background(), []string{chunks[i].Text})
		chunks[i].Embedding = v[0]
	}
	q := "The vendor shall provide SAML 2.0 based single sign-on and enforce multi-factor authentication for all administrative users."
	qv, _ := emb.Embed(context.Background(), []string{q})
	res := Search(context.Background(), q, chunks, emb, qv[0], Options{Aliases: []string{"Northstar"}})
	if !res.Sufficient || len(res.Evidence) != 1 || res.Evidence[0].Chunk.ID != "c1" {
		t.Fatalf("expected c1 evidence: %+v gap=%s", res.Evidence, res.Gap)
	}
	trap := "Provide evidence of ISO 27001 certification, including the certificate scope and expiry date."
	tv, _ := emb.Embed(context.Background(), []string{trap})
	res = Search(context.Background(), trap, chunks, emb, tv[0], Options{Aliases: []string{"Northstar"}})
	if res.Sufficient || res.Gap == "" {
		t.Fatalf("trap should be insufficient: %+v", res)
	}
	// unapproved chunk excluded
	chunks[0].DocApproval = "unapproved"
	res = Search(context.Background(), q, chunks, emb, qv[0], Options{})
	if res.Sufficient {
		t.Fatal("unapproved evidence used")
	}
	// expired excluded by default
	chunks[0].DocApproval, chunks[0].DocExpiresAt = "approved", "2020-01-01"
	res = Search(context.Background(), q, chunks, emb, qv[0], Options{Now: "2026-09-18T00:00:00Z"})
	if res.Sufficient {
		t.Fatal("expired evidence used")
	}
	res = Search(context.Background(), q, chunks, emb, qv[0], Options{Now: "2026-09-18T00:00:00Z", IncludeExpired: true})
	if !res.Sufficient || !res.Evidence[0].Expired {
		t.Fatal("expired flag missing")
	}
}

func TestStemAndCoverage(t *testing.T) {
	if !StemMatch(Stem("encrypted"), Stem("encryption")) {
		t.Fatal("stem family failed")
	}
	if Coverage([]string{"audit", "log"}, Terms("Audit logs are retained for 365 days", nil)) < 0.99 {
		t.Fatal("coverage failed")
	}
	if !ContainsTerm("validated at 5,000 concurrent users", "5,000") || !ContainsTerm("a full CV is available", "CVs") {
		t.Fatal("contains term failed")
	}
}

func TestRRF(t *testing.T) {
	f := RRF([]Hit{{"a", 3}, {"b", 2}}, []Hit{{"b", 0.9}, {"c", 0.5}})
	if f[0].ID != "b" {
		t.Fatalf("rrf order wrong: %+v", f)
	}
}
