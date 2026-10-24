package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateRecordsLegacyEmbeddingSpace(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := "2026-01-01T00:00:00Z"
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	vector768 := "[" + strings.TrimSuffix(strings.Repeat("0.1,", 768), ",") + "]"
	rows := []struct {
		id, model string
		embedding any
		dim       int
	}{
		{"labelled", "gemini/gemini-embedding-2", vector768, 768},
		{"unlabelled", "", vector768, 0},
		{"null-model", "", vector768, 768},
		{"other-model", "openai/text-embedding-3-small", `[0.1,0.2]`, 2},
		{"small-unlabelled", "", `[0.1,0.2,0.3]`, 0},
		{"none", "", nil, 0},
	}
	for _, row := range rows {
		var model any = row.model
		if row.id == "null-model" || row.id == "none" {
			model = nil
		}
		var embedding any
		if row.embedding != nil {
			embedding = []byte(row.embedding.(string))
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,embedding,embedding_model,embedding_dim,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, row.id, "u1", "https://example.com/"+row.id, row.id, embedding, model, row.dim, now, now); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]struct {
		model string
		dim   int
	}{
		"labelled":         {LegacyEmbeddingSpace, 768},
		"unlabelled":       {LegacyEmbeddingSpace, 768},
		"null-model":       {LegacyEmbeddingSpace, 768},
		"other-model":      {"openai/text-embedding-3-small", 2},
		"small-unlabelled": {"", 3},
		"none":             {"", 0},
	}
	for id, expected := range want {
		var model sql.NullString
		var dim int
		if err := db.QueryRowContext(ctx, `SELECT embedding_model,embedding_dim FROM bookmarks WHERE id=?`, id).Scan(&model, &dim); err != nil {
			t.Fatal(err)
		}
		if model.String != expected.model || dim != expected.dim {
			t.Errorf("%s: model=%q dim=%d, want %q %d", id, model.String, dim, expected.model, expected.dim)
		}
	}
}
