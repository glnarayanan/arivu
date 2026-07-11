package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestOpenInitializesSchema(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	var name string
	if err := db.QueryRowContext(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&name); err != nil {
		t.Fatalf("users table missing: %v", err)
	}
}

func TestMigrateAddsEvidenceProvenanceToLegacyDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "legacy.sqlite3")+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacy := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, name TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE knowledge_feedback (
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			target_type TEXT NOT NULL, target_id TEXT NOT NULL, feedback TEXT NOT NULL,
			snoozed_until TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			PRIMARY KEY(user_id,target_type,target_id)
		)`,
		`CREATE TABLE bookmarks (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			url TEXT NOT NULL, title TEXT, description TEXT, favicon TEXT, thumbnail TEXT,
			sanitized_html TEXT, text_content TEXT, domain TEXT, reading_time INTEGER NOT NULL DEFAULT 0,
			read_status INTEGER NOT NULL DEFAULT 0, source TEXT NOT NULL DEFAULT 'web', x_tweet_id TEXT,
			x_author_username TEXT, x_author_name TEXT, x_tweet_url TEXT, x_metrics_json TEXT,
			embedding BLOB, embedding_model TEXT, embedding_dim INTEGER NOT NULL DEFAULT 0,
			version INTEGER NOT NULL DEFAULT 1, resurfacing_snoozed_until TEXT,
			resurfacing_archived INTEGER NOT NULL DEFAULT 0, last_accessed TEXT,
			view_count INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			UNIQUE(user_id, url)
		)`,
		`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2025-01-01T00:00:00Z','2025-01-02T00:00:00Z')`,
		`INSERT INTO bookmarks(id,user_id,url,title,text_content,domain,source,x_author_username,created_at,updated_at) VALUES('b1','u1','https://example.com/post','Legacy','Noisy legacy scrape','x.com','x','legacy_author','2025-01-01T00:00:00Z','2025-01-02T00:00:00Z')`,
	}
	for _, statement := range legacy {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("legacy setup: %v", err)
		}
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() legacy database: %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate() must be idempotent: %v", err)
	}

	columns := tableColumns(t, db, "bookmarks")
	for _, column := range []string{"canonical_url", "content_kind", "source_published_at", "source_author_id", "source_publisher_key", "processed_at", "fetch_version", "summary_version", "enrichment_version"} {
		if !slices.Contains(columns, column) {
			t.Errorf("bookmarks.%s was not added; columns=%v", column, columns)
		}
	}
	feedbackColumns := tableColumns(t, db, "knowledge_feedback")
	for _, column := range []string{"detector_family", "detector_version", "reason"} {
		if !slices.Contains(feedbackColumns, column) {
			t.Errorf("knowledge_feedback.%s was not added; columns=%v", column, feedbackColumns)
		}
	}
	var table string
	if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='bookmark_evidence'`).Scan(&table); err != nil {
		t.Fatalf("bookmark_evidence table missing: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='insight_impressions'`).Scan(&table); err != nil {
		t.Fatalf("insight_impressions table missing: %v", err)
	}
	for _, index := range []string{"idx_bookmarks_user_published", "idx_bookmarks_user_pipeline_versions", "idx_evidence_user_bookmark", "idx_evidence_user_published", "idx_evidence_user_quality_version"} {
		if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&table); err != nil {
			t.Errorf("required index %s missing: %v", index, err)
		}
	}
	var title, capturedAt, updatedAt string
	if err := db.QueryRowContext(ctx, `SELECT title,created_at,updated_at FROM bookmarks WHERE id='b1'`).Scan(&title, &capturedAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if title != "Legacy" || capturedAt != "2025-01-01T00:00:00Z" || updatedAt != "2025-01-02T00:00:00Z" {
		t.Fatalf("legacy data changed: title=%q captured=%q updated=%q", title, capturedAt, updatedAt)
	}
	var canonicalURL, contentKind, publisherKey string
	if err := db.QueryRowContext(ctx, `SELECT canonical_url,content_kind,source_publisher_key FROM bookmarks WHERE id='b1'`).Scan(&canonicalURL, &contentKind, &publisherKey); err != nil {
		t.Fatal(err)
	}
	if canonicalURL != "https://example.com/post" || contentKind != "x_post" || publisherKey != "x:legacy_author" {
		t.Fatalf("legacy provenance defaults = canonical:%q kind:%q publisher:%q", canonicalURL, contentKind, publisherKey)
	}
	var evidenceKind, evidenceOrigin, qualityStatus, qualityReasons, text string
	if err := db.QueryRowContext(ctx, `SELECT evidence_kind,evidence_origin,quality_status,quality_reasons_json,content_text FROM bookmark_evidence WHERE bookmark_id='b1' AND user_id='u1'`).Scan(&evidenceKind, &evidenceOrigin, &qualityStatus, &qualityReasons, &text); err != nil {
		t.Fatal(err)
	}
	if evidenceKind != "legacy_scrape" || evidenceOrigin != "legacy" || qualityStatus != "partial" || qualityReasons != `["legacy_unverified"]` || text != "Noisy legacy scrape" {
		t.Fatalf("legacy evidence was overstated or lost: %q/%q/%q/%q/%q", evidenceKind, evidenceOrigin, qualityStatus, qualityReasons, text)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u2','two@example.com','Two','2025-01-01T00:00:00Z','2025-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,created_at,updated_at) VALUES('e1','b1','u2','source_post','source_provided','2025-01-01T00:00:00Z','2025-01-01T00:00:00Z')`); err == nil {
		t.Fatal("cross-user evidence relation unexpectedly passed the database constraint")
	}
}

func tableColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	return columns
}
