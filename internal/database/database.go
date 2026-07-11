package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaFS embed.FS

func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := applyPragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func applyPragmas(ctx context.Context, db *sql.DB) error {
	statements := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA wal_autocheckpoint = 1000",
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return err
	}
	for _, stmt := range strings.Split(string(raw), ";\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			if strings.Contains(stmt, "VIRTUAL TABLE") && strings.Contains(err.Error(), "no such module: fts5") {
				continue
			}
			return fmt.Errorf("schema statement %q: %w", stmt, err)
		}
	}
	if err := ensureReminderColumns(ctx, db); err != nil {
		return err
	}
	if err := ensureBookmarkProvenance(ctx, db); err != nil {
		return err
	}
	return nil
}

func ensureBookmarkProvenance(ctx context.Context, db *sql.DB) error {
	columns := map[string]string{
		"canonical_url":        "TEXT NOT NULL DEFAULT ''",
		"content_kind":         "TEXT NOT NULL DEFAULT ''",
		"source_published_at":  "TEXT",
		"source_author_id":     "TEXT",
		"source_publisher_key": "TEXT",
		"processed_at":         "TEXT",
		"fetch_version":        "TEXT NOT NULL DEFAULT ''",
		"summary_version":      "TEXT NOT NULL DEFAULT ''",
		"enrichment_version":   "TEXT NOT NULL DEFAULT ''",
	}
	for name, definition := range columns {
		if err := ensureColumn(ctx, db, "bookmarks", name, definition); err != nil {
			return err
		}
	}
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_bookmarks_id_user ON bookmarks(id, user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bookmarks_user_published ON bookmarks(user_id, source_published_at DESC) WHERE source_published_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_bookmarks_user_pipeline_versions ON bookmarks(user_id, fetch_version, summary_version, enrichment_version)`,
		`CREATE TABLE IF NOT EXISTS bookmark_evidence (
			id TEXT PRIMARY KEY,
			bookmark_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			evidence_kind TEXT NOT NULL,
			evidence_origin TEXT NOT NULL,
			authority INTEGER NOT NULL DEFAULT 0,
			content_text TEXT NOT NULL DEFAULT '',
			sanitized_html TEXT NOT NULL DEFAULT '',
			canonical_url TEXT NOT NULL DEFAULT '',
			author_id TEXT NOT NULL DEFAULT '',
			publisher_key TEXT NOT NULL DEFAULT '',
			published_at TEXT,
			extraction_method TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			quality_status TEXT NOT NULL DEFAULT 'failed',
			quality_reasons_json TEXT NOT NULL DEFAULT '[]',
			extractor_version TEXT NOT NULL DEFAULT '',
			is_selected INTEGER NOT NULL DEFAULT 0 CHECK(is_selected IN (0,1)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(bookmark_id, user_id) REFERENCES bookmarks(id, user_id) ON DELETE CASCADE,
			UNIQUE(bookmark_id, evidence_kind, content_hash, extractor_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_user_bookmark ON bookmark_evidence(user_id, bookmark_id, authority DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_user_published ON bookmark_evidence(user_id, published_at DESC) WHERE published_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_user_quality_version ON bookmark_evidence(user_id, quality_status, extractor_version)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_content_hash ON bookmark_evidence(content_hash) WHERE content_hash != ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_evidence_selected_bookmark ON bookmark_evidence(bookmark_id) WHERE is_selected = 1`,
		`UPDATE bookmarks SET
			canonical_url=CASE WHEN canonical_url='' THEN url ELSE canonical_url END,
			content_kind=CASE WHEN content_kind='' AND source IN ('x','twitter') THEN 'x_post' WHEN content_kind='' THEN 'web' ELSE content_kind END,
			source_publisher_key=CASE WHEN COALESCE(source_publisher_key,'')='' AND source IN ('x','twitter') AND COALESCE(x_author_username,'')!='' THEN 'x:' || lower(x_author_username) WHEN COALESCE(source_publisher_key,'')='' THEN domain ELSE source_publisher_key END`,
		`INSERT OR IGNORE INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,authority,content_text,sanitized_html,canonical_url,author_id,publisher_key,extraction_method,content_hash,quality_status,quality_reasons_json,extractor_version,is_selected,created_at,updated_at)
			SELECT 'legacy-' || b.id,b.id,b.user_id,'legacy_scrape','legacy',10,COALESCE(b.text_content,''),COALESCE(b.sanitized_html,''),COALESCE(NULLIF(b.canonical_url,''),b.url),'',COALESCE(b.source_publisher_key,b.domain),'legacy_projection','','partial','["legacy_unverified"]','legacy-v1',1,b.created_at,b.updated_at
			FROM bookmarks b
			WHERE (trim(COALESCE(b.text_content,''))!='' OR trim(COALESCE(b.sanitized_html,''))!='')
			AND NOT EXISTS (SELECT 1 FROM bookmark_evidence e WHERE e.bookmark_id=b.id)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("bookmark provenance migration: %w", err)
		}
	}
	return nil
}

func ensureReminderColumns(ctx context.Context, db *sql.DB) error {
	columns := map[string]string{
		"timezone":                 "TEXT NOT NULL DEFAULT 'UTC'",
		"recurrence":               "TEXT NOT NULL DEFAULT 'none'",
		"recurrence_interval_days": "INTEGER NOT NULL DEFAULT 0",
		"notification_channel":     "TEXT NOT NULL DEFAULT 'in_app'",
		"last_notified_at":         "TEXT",
		"last_completed_at":        "TEXT",
	}
	for name, definition := range columns {
		if err := ensureColumn(ctx, db, "reminders", name, definition); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, definition string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}
