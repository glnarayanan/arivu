package bookmarks

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/providers"
)

func TestStoreEnrichmentPreservesCompletedAISummary(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, statement := range []string{
		`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('user-1','one@example.com','One','` + now + `','` + now + `')`,
		`INSERT INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES('bookmark-1','user-1','https://example.com','Example','example.com','` + now + `','` + now + `')`,
		`INSERT INTO ai_summaries(id,bookmark_id,user_id,bullet_points_json,highlights_json,suggested_tags_json,processing_status,created_at,updated_at) VALUES('summary-1','bookmark-1','user-1','["model bullet"]','["model highlight"]','["model-tag"]','completed','` + now + `','` + now + `')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	service := New(db, nil, nil, providers.GeminiClient{})
	fallback := enrichment{
		Bullets:    []string{"fallback bullet"},
		Highlights: []string{"fallback highlight"},
		Tags:       []string{"fallback-tag"},
	}
	service.storeEnrichment(ctx, "bookmark-1", "user-1", fallback, true)
	assertSummaryLists(t, db, "[\"model bullet\"]", "[\"model highlight\"]", "[\"model-tag\"]")

	service.storeEnrichment(ctx, "bookmark-1", "user-1", fallback, false)
	assertSummaryLists(t, db, "[\"fallback bullet\"]", "[\"fallback highlight\"]", "[\"fallback-tag\"]")
}

func assertSummaryLists(t *testing.T, db *sql.DB, bullets, highlights, tags string) {
	t.Helper()
	var gotBullets, gotHighlights, gotTags string
	if err := db.QueryRow(`SELECT bullet_points_json,highlights_json,suggested_tags_json FROM ai_summaries WHERE bookmark_id='bookmark-1'`).Scan(&gotBullets, &gotHighlights, &gotTags); err != nil {
		t.Fatal(err)
	}
	if gotBullets != bullets || gotHighlights != highlights || gotTags != tags {
		t.Fatalf("summary lists = (%q, %q, %q), want (%q, %q, %q)", gotBullets, gotHighlights, gotTags, bullets, highlights, tags)
	}
}
