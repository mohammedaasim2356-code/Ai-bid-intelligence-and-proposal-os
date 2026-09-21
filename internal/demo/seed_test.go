package demo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"bidos/internal/ai"
	"bidos/internal/app"
	"bidos/internal/config"
	"bidos/internal/retrieval"
	"bidos/internal/store"
)

func dump(t *testing.T, db *store.DB) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		"SELECT id, name, vertical_pack_id FROM organizations ORDER BY id",
		"SELECT id, name, role FROM users ORDER BY id",
		"SELECT id, name, hash, approval_state, expires_at, created_at FROM documents ORDER BY id",
		"SELECT id, section_path, text_hash FROM document_chunks ORDER BY id",
		"SELECT id, code, text, category, mandatory, anchor_status, is_trap, owner_id, created_at FROM requirements ORDER BY id",
		"SELECT id, name, deadline, status, created_at FROM bids ORDER BY id",
		"SELECT id, canonical_question, review_by, citation_chunk_ids FROM answer_library ORDER BY id",
		"SELECT id, key, title, sort_order, updated_at FROM proposal_sections ORDER BY id",
		"SELECT org_id, key, value FROM settings ORDER BY key",
	} {
		rows, err := db.Raw().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				if bs, ok := v.([]byte); ok {
					v = string(bs)
				}
				b.WriteString(strings.ReplaceAll(fmt.Sprint(v), "\n", " "))
				b.WriteString("\t")
			}
			b.WriteString("\n")
		}
		rows.Close()
	}
	return b.String()
}

func seedInto(t *testing.T, dir string) string {
	t.Helper()
	cfg := config.Config{StorageDir: dir, DatabasePath: filepath.Join(dir, "t.db"), MaxUploadBytes: 25 << 20, VerticalPack: "saas-it", EnableEmbeddings: true}
	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	router := ai.NewRouter(ai.LoadConfig(filepath.Join(dir, "none.json")), db, nil, nil)
	a := app.New(cfg, db, router, retrieval.HashEmbedder{}, nil)
	s := &Seeder{App: a}
	if _, err := s.Seed(context.Background(), "saas-it", "", ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Count("SELECT COUNT(*) FROM activity_log"); n != 0 {
		t.Fatal("seed left activity rows")
	}
	return dump(t, db)
}

func TestSeedIsDeterministic(t *testing.T) {
	a := seedInto(t, t.TempDir())
	b := seedInto(t, t.TempDir())
	if a != b {
		la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
		for i := range la {
			if i >= len(lb) || la[i] != lb[i] {
				t.Fatalf("seed differs at line %d:\n%s\n%s", i, la[i], lb[min(i, len(lb)-1)])
			}
		}
		t.Fatal("seed differs")
	}
	if !strings.Contains(a, "req-rfp-6.5") || !strings.Contains(a, "req-q-VSA-01") {
		t.Fatal("expected deterministic requirement ids")
	}
}

func TestResetLeavesNoStaleRows(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{StorageDir: dir, DatabasePath: filepath.Join(dir, "t.db"), MaxUploadBytes: 25 << 20, VerticalPack: "saas-it"}
	db, _ := store.Open(cfg.DatabasePath)
	defer db.Close()
	a := app.New(cfg, db, ai.NewRouter(ai.LoadConfig(""), db, nil, nil), nil, nil)
	s := &Seeder{App: a}
	if _, err := s.Seed(context.Background(), "saas-it", "", ""); err != nil {
		t.Fatal(err)
	}
	before := dump(t, db)
	_ = db.SetSetting(DemoOrgID, "custom", "x")
	if _, err := s.Reset(context.Background(), "saas-it", ""); err != nil {
		t.Fatal(err)
	}
	if dump(t, db) != before {
		t.Fatal("reset did not recreate the identical dataset")
	}
	for _, tbl := range []string{"documents", "document_chunks", "requirements", "bids", "users", "answer_library", "proposal_sections"} {
		n, _ := db.Count("SELECT COUNT(*) FROM " + tbl)
		if n == 0 {
			t.Fatalf("%s empty after reset", tbl)
		}
	}
}
