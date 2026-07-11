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

func TestEnrichTextWithoutProviderDoesNotCreateTokenSemantics(t *testing.T) {
	service := &Service{}
	item := service.enrichText(context.Background(), "bookmark-1", "user-1", "Quot HTTPS Com", "Jun 10 views", "Microsoft documents row-level security.")
	if len(item.Entities) != 0 || len(item.Concepts) != 0 || len(item.Tags) != 0 {
		t.Fatalf("no-provider enrichment repopulated token semantics: %#v", item)
	}
}

func TestEnrichTextAcceptsOnlyEvidenceBackedTypedSemantics(t *testing.T) {
	service := &Service{}
	item := service.enrichText(context.Background(), "bookmark-1", "user-1", "Microsoft", "", "Microsoft documents row-level security.", providers.SemanticResult{
		Entities: []providers.SemanticTerm{
			{Label: "Microsoft", Type: "organization", Confidence: 0.98, Evidence: "Microsoft"},
			{Label: "https", Type: "technology", Confidence: 1, Evidence: "https"},
		},
		Concepts: []providers.SemanticTerm{
			{Label: "row-level security", Confidence: 0.91, Evidence: "row-level security"},
			{Label: "database", Confidence: 0.2, Evidence: "database"},
		},
	})
	if len(item.Entities) != 1 || item.Entities[0].NormalizedKey != "microsoft" {
		t.Fatalf("entities = %#v", item.Entities)
	}
	if len(item.Concepts) != 1 || item.Concepts[0].NormalizedKey != "row-level security" {
		t.Fatalf("concepts = %#v", item.Concepts)
	}
}

func TestStoreEnrichmentPreservesManualTagsAndStopsConceptTagProjection(t *testing.T) {
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
		`INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES('summary-1','bookmark-1','user-1','completed','` + now + `','` + now + `')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	service := New(db, nil, nil, providers.GeminiClient{})
	if err := service.attachTag(ctx, "user-1", "bookmark-1", "keep-me", "manual"); err != nil {
		t.Fatal(err)
	}
	if err := service.attachTag(ctx, "user-1", "bookmark-1", "old-generated", "enrichment"); err != nil {
		t.Fatal(err)
	}
	service.storeEnrichment(ctx, "bookmark-1", "user-1", enrichment{
		Concepts: []providers.SemanticTerm{{Label: "row-level security", NormalizedKey: "row-level security", Confidence: 0.9, Evidence: "row-level security"}},
	}, true)

	var manualCount, generatedCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmark_tags WHERE bookmark_id='bookmark-1' AND source='manual'`).Scan(&manualCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmark_tags WHERE bookmark_id='bookmark-1' AND source='enrichment'`).Scan(&generatedCount); err != nil {
		t.Fatal(err)
	}
	if manualCount != 1 || generatedCount != 0 {
		t.Fatalf("manual tags=%d generated tags=%d", manualCount, generatedCount)
	}
	var concept string
	if err := db.QueryRowContext(ctx, `SELECT concept FROM bookmark_concepts WHERE bookmark_id='bookmark-1'`).Scan(&concept); err != nil || concept != "row-level security" {
		t.Fatalf("concept=%q err=%v", concept, err)
	}
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
