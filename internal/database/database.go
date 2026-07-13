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

// Migrate converges any older additive database to the current schema.
//
// Order is intentional and version-agnostic:
//  1. structure — CREATE TABLE / VIRTUAL TABLE / UNIQUE INDEX (unique indexes stay
//     interleaved so composite foreign keys have a parent unique key)
//  2. additive columns and dependent objects via ensure* helpers
//  3. non-unique indexes from schema.sql (safe only after columns exist)
//
// CREATE TABLE IF NOT EXISTS never alters an existing table, so indexes that
// reference additive columns must not run before ensure* column migrations.
func Migrate(ctx context.Context, db *sql.DB) error {
	structure, indexes, err := loadSchemaPhases()
	if err != nil {
		return err
	}
	if err := execSchemaStatements(ctx, db, structure); err != nil {
		return err
	}
	if err := ensureArtifactStorageReferences(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_artifacts_attempt_type ON artifacts(capture_attempt_id,artifact_type)`); err != nil {
		return err
	}
	if err := ensureReminderColumns(ctx, db); err != nil {
		return err
	}
	if err := ensureBookmarkProvenance(ctx, db); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "bookmark_evidence", "quality_score", "INTEGER NOT NULL DEFAULT 0 CHECK(quality_score BETWEEN 0 AND 100)"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "annotations", "evidence_id", "TEXT REFERENCES bookmark_evidence(id) ON DELETE SET NULL"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "bookmarks", "reading_progress", "REAL NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureReadingProgressConstraint(ctx, db); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "collections", "parent_id", "TEXT REFERENCES collections(id) ON DELETE RESTRICT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "collections", "sibling_order", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	shareColumns := map[string]string{
		"evidence_id": "TEXT REFERENCES bookmark_evidence(id) ON DELETE SET NULL", "public_title": "TEXT NOT NULL DEFAULT ''",
		"public_description": "TEXT NOT NULL DEFAULT ''", "public_url": "TEXT NOT NULL DEFAULT ''", "public_domain": "TEXT NOT NULL DEFAULT ''",
		"public_reader_html": "TEXT NOT NULL DEFAULT ''", "public_text": "TEXT NOT NULL DEFAULT ''", "public_published_at": "TEXT NOT NULL DEFAULT ''",
	}
	for name, definition := range shareColumns {
		if err := ensureColumn(ctx, db, "public_share_items", name, definition); err != nil {
			return err
		}
	}
	if err := ensurePublicShareSnapshotLifetime(ctx, db); err != nil {
		return err
	}
	if err := ensurePublicShareArtifactLifetime(ctx, db); err != nil {
		return err
	}
	// Existing memberships become immutable at migration time. Prefer the selected
	// evidence, while retaining the bookmark fallback used by the former projection.
	if _, err := db.ExecContext(ctx, `UPDATE public_share_items SET
		evidence_id=(SELECT id FROM bookmark_evidence WHERE bookmark_id=public_share_items.bookmark_id AND is_selected=1),
		public_title=COALESCE((SELECT title FROM bookmarks WHERE id=bookmark_id),''),
		public_description=COALESCE((SELECT description FROM bookmarks WHERE id=bookmark_id),''),
		public_url=COALESCE((SELECT url FROM bookmarks WHERE id=bookmark_id),''),
		public_domain=COALESCE((SELECT domain FROM bookmarks WHERE id=bookmark_id),''),
		public_reader_html=COALESCE((SELECT sanitized_html FROM bookmark_evidence WHERE bookmark_id=public_share_items.bookmark_id AND is_selected=1),(SELECT sanitized_html FROM bookmarks WHERE id=bookmark_id),''),
		public_text=COALESCE((SELECT content_text FROM bookmark_evidence WHERE bookmark_id=public_share_items.bookmark_id AND is_selected=1),(SELECT text_content FROM bookmarks WHERE id=bookmark_id),''),
		public_published_at=COALESCE((SELECT published_at FROM bookmark_evidence WHERE bookmark_id=public_share_items.bookmark_id AND is_selected=1),(SELECT source_published_at FROM bookmarks WHERE id=bookmark_id),(SELECT created_at FROM bookmarks WHERE id=bookmark_id),'')
		WHERE public_url=''`); err != nil {
		return err
	}
	if err := ensureGeneratedQualityMetadata(ctx, db); err != nil {
		return err
	}
	if err := ensureInsightFeedback(ctx, db); err != nil {
		return err
	}
	if err := ensureQualityOperations(ctx, db); err != nil {
		return err
	}
	if err := execSchemaStatements(ctx, db, indexes); err != nil {
		return err
	}
	return nil
}

func ensureReadingProgressConstraint(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS bookmarks_reading_progress_insert BEFORE INSERT ON bookmarks WHEN NEW.reading_progress < 0 OR NEW.reading_progress > 1 BEGIN SELECT RAISE(ABORT, 'reading_progress must be between 0 and 1'); END`,
		`CREATE TRIGGER IF NOT EXISTS bookmarks_reading_progress_update BEFORE UPDATE OF reading_progress ON bookmarks WHEN NEW.reading_progress < 0 OR NEW.reading_progress > 1 BEGIN SELECT RAISE(ABORT, 'reading_progress must be between 0 and 1'); END`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("reading progress constraint: %w", err)
		}
	}
	return nil
}

// Published item content is an immutable snapshot, not a projection whose
// lifetime is tied to the owner's private bookmark.
func ensurePublicShareSnapshotLifetime(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_list(public_share_items)`)
	if err != nil {
		return err
	}
	removeBookmarkFK := false
	for rows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			rows.Close()
			return err
		}
		removeBookmarkFK = removeBookmarkFK || (table == "bookmarks" && from == "bookmark_id")
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !removeBookmarkFK {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE public_share_items_new (
			share_id TEXT NOT NULL REFERENCES public_shares(id) ON DELETE CASCADE,
			bookmark_id TEXT NOT NULL,
			evidence_id TEXT REFERENCES bookmark_evidence(id) ON DELETE SET NULL,
			public_title TEXT NOT NULL DEFAULT '', public_description TEXT NOT NULL DEFAULT '',
			public_url TEXT NOT NULL DEFAULT '', public_domain TEXT NOT NULL DEFAULT '',
			public_reader_html TEXT NOT NULL DEFAULT '', public_text TEXT NOT NULL DEFAULT '',
			public_published_at TEXT NOT NULL DEFAULT '', added_at TEXT NOT NULL,
			PRIMARY KEY(share_id,bookmark_id))`,
		`INSERT INTO public_share_items_new SELECT share_id,bookmark_id,evidence_id,public_title,public_description,public_url,public_domain,public_reader_html,public_text,public_published_at,added_at FROM public_share_items`,
		`DROP TABLE public_share_items`,
		`ALTER TABLE public_share_items_new RENAME TO public_share_items`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("public share snapshot migration: %w", err)
		}
	}
	return tx.Commit()
}

func ensurePublicShareArtifactLifetime(ctx context.Context, db *sql.DB) error {
	columns, err := tableColumnSet(ctx, db, "public_share_artifacts")
	if err != nil {
		return err
	}
	if columns["storage_key"] && columns["bookmark_id"] && columns["mime_type"] && columns["byte_size"] {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE public_share_artifacts_new (
			share_id TEXT NOT NULL REFERENCES public_shares(id) ON DELETE CASCADE,
			artifact_id TEXT NOT NULL, bookmark_id TEXT NOT NULL,
			artifact_type TEXT NOT NULL CHECK(artifact_type IN ('screenshot','pdf')),
			storage_key TEXT NOT NULL, mime_type TEXT NOT NULL,
			byte_size INTEGER NOT NULL CHECK(byte_size >= 0), added_at TEXT NOT NULL,
			PRIMARY KEY(share_id,artifact_id))`,
		`INSERT INTO public_share_artifacts_new(share_id,artifact_id,bookmark_id,artifact_type,storage_key,mime_type,byte_size,added_at)
		 SELECT sa.share_id,sa.artifact_id,a.bookmark_id,sa.artifact_type,a.storage_key,a.mime_type,a.byte_size,sa.added_at FROM public_share_artifacts sa JOIN artifacts a ON a.id=sa.artifact_id`,
		`DROP TABLE public_share_artifacts`,
		`ALTER TABLE public_share_artifacts_new RENAME TO public_share_artifacts`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("public share artifact snapshot migration: %w", err)
		}
	}
	return tx.Commit()
}

func tableColumnSet(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

// ensureArtifactStorageReferences removes legacy table-level/column UNIQUE
// constraints on storage_key. SQLite can only do that safely by rebuilding the
// table; rows, foreign keys and the current indexes are recreated by Migrate.
func ensureArtifactStorageReferences(ctx context.Context, db *sql.DB) error {
	var ddl string
	if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='artifacts'`).Scan(&ddl); err != nil {
		return err
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(ddl), " "))
	if !strings.Contains(normalized, "storage_key text not null unique") && !strings.Contains(normalized, "unique(storage_key)") && !strings.Contains(normalized, "unique (storage_key)") {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`CREATE TEMP TABLE artifact_share_memberships AS SELECT share_id,artifact_id,artifact_type,added_at FROM public_share_artifacts`,
		`CREATE TABLE artifacts_new (id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,bookmark_id TEXT NOT NULL,capture_attempt_id TEXT NOT NULL REFERENCES capture_attempts(id) ON DELETE CASCADE,evidence_id TEXT REFERENCES bookmark_evidence(id) ON DELETE SET NULL,artifact_type TEXT NOT NULL CHECK(artifact_type IN ('source_response','screenshot','pdf','self_contained_html','uploaded_file')),mime_type TEXT NOT NULL,byte_size INTEGER NOT NULL CHECK(byte_size >= 0),sha256 TEXT NOT NULL,storage_key TEXT NOT NULL,original_filename TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,deleted_at TEXT,FOREIGN KEY(bookmark_id,user_id) REFERENCES bookmarks(id,user_id) ON DELETE CASCADE)`,
		`INSERT INTO artifacts_new SELECT id,user_id,bookmark_id,capture_attempt_id,evidence_id,artifact_type,mime_type,byte_size,sha256,storage_key,original_filename,created_at,deleted_at FROM artifacts`,
		`DROP TABLE artifacts`, `ALTER TABLE artifacts_new RENAME TO artifacts`,
		`INSERT OR IGNORE INTO public_share_artifacts(share_id,artifact_id,artifact_type,added_at) SELECT share_id,artifact_id,artifact_type,added_at FROM artifact_share_memberships`,
		`DROP TABLE artifact_share_memberships`,
	}
	for _, stmt := range stmts {
		if _, err = tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate artifact references: %w", err)
		}
	}
	return tx.Commit()
}

type schemaStatementKind int

const (
	schemaStatementStructure schemaStatementKind = iota
	schemaStatementIndex
)

func loadSchemaPhases() (structure, indexes []string, err error) {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return nil, nil, err
	}
	for _, stmt := range strings.Split(string(raw), ";\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		switch classifySchemaStatement(stmt) {
		case schemaStatementIndex:
			indexes = append(indexes, stmt)
		default:
			structure = append(structure, stmt)
		}
	}
	return structure, indexes, nil
}

func classifySchemaStatement(stmt string) schemaStatementKind {
	upper := strings.ToUpper(strings.Join(strings.Fields(stmt), " "))
	// Non-unique indexes may reference additive columns and must run after ensure*.
	// UNIQUE INDEX stays with structure so composite FK parents exist before children.
	if strings.HasPrefix(upper, "CREATE INDEX ") {
		return schemaStatementIndex
	}
	return schemaStatementStructure
}

func execSchemaStatements(ctx context.Context, db *sql.DB, statements []string) error {
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			if strings.Contains(stmt, "VIRTUAL TABLE") && strings.Contains(err.Error(), "no such module: fts5") {
				continue
			}
			return fmt.Errorf("schema statement %q: %w", stmt, err)
		}
	}
	return nil
}

func ensureGeneratedQualityMetadata(ctx context.Context, db *sql.DB) error {
	summaryColumns := map[string]string{
		"provider":                "TEXT NOT NULL DEFAULT ''",
		"model":                   "TEXT NOT NULL DEFAULT ''",
		"prompt_version":          "TEXT NOT NULL DEFAULT ''",
		"validator_version":       "TEXT NOT NULL DEFAULT ''",
		"evidence_hash":           "TEXT NOT NULL DEFAULT ''",
		"validation_status":       "TEXT NOT NULL DEFAULT ''",
		"validation_reasons_json": "TEXT NOT NULL DEFAULT '[]'",
		"highlight_spans_json":    "TEXT NOT NULL DEFAULT '[]'",
		"generated_at":            "TEXT",
	}
	for name, definition := range summaryColumns {
		if err := ensureColumn(ctx, db, "ai_summaries", name, definition); err != nil {
			return err
		}
	}
	entityColumns := map[string]string{
		"normalized_key":     "TEXT NOT NULL DEFAULT ''",
		"entity_type":        "TEXT NOT NULL DEFAULT ''",
		"confidence":         "REAL NOT NULL DEFAULT 0",
		"extraction_method":  "TEXT NOT NULL DEFAULT ''",
		"evidence_id":        "TEXT",
		"evidence_text":      "TEXT NOT NULL DEFAULT ''",
		"evidence_start":     "INTEGER NOT NULL DEFAULT 0",
		"evidence_end":       "INTEGER NOT NULL DEFAULT 0",
		"enrichment_version": "TEXT NOT NULL DEFAULT ''",
	}
	for name, definition := range entityColumns {
		if err := ensureColumn(ctx, db, "bookmark_entities", name, definition); err != nil {
			return err
		}
	}
	conceptColumns := map[string]string{
		"normalized_key":     "TEXT NOT NULL DEFAULT ''",
		"confidence":         "REAL NOT NULL DEFAULT 0",
		"extraction_method":  "TEXT NOT NULL DEFAULT ''",
		"evidence_id":        "TEXT",
		"evidence_text":      "TEXT NOT NULL DEFAULT ''",
		"evidence_start":     "INTEGER NOT NULL DEFAULT 0",
		"evidence_end":       "INTEGER NOT NULL DEFAULT 0",
		"enrichment_version": "TEXT NOT NULL DEFAULT ''",
	}
	for name, definition := range conceptColumns {
		if err := ensureColumn(ctx, db, "bookmark_concepts", name, definition); err != nil {
			return err
		}
	}
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_entities_quality ON bookmark_entities(user_id, enrichment_version, confidence DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_concepts_quality ON bookmark_concepts(user_id, enrichment_version, confidence DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_entities_bookmark_quality ON bookmark_entities(bookmark_id, user_id, enrichment_version, confidence DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_concepts_bookmark_quality ON bookmark_concepts(bookmark_id, user_id, enrichment_version, confidence DESC)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("generated quality metadata migration: %w", err)
		}
	}
	return nil
}

func ensureQualityOperations(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS quality_reprocess_runs (
			id TEXT PRIMARY KEY,
			scope_type TEXT NOT NULL CHECK(scope_type IN ('user','all')),
			scope_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
			target_fetch_version TEXT NOT NULL,
			target_summary_version TEXT NOT NULL,
			target_enrichment_version TEXT NOT NULL,
			backup_sha256 TEXT NOT NULL DEFAULT '',
			protected_digest TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL CHECK(status IN ('planned','queued','running','completed','partial','failed')) DEFAULT 'planned',
			total_candidates INTEGER NOT NULL DEFAULT 0,
			queued_count INTEGER NOT NULL DEFAULT 0,
			completed_count INTEGER NOT NULL DEFAULT 0,
			partial_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0,
			skipped_count INTEGER NOT NULL DEFAULT 0,
			preserved_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_quality_reprocess_runs_scope ON quality_reprocess_runs(scope_type, scope_user_id, status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS quality_reprocess_items (
			run_id TEXT NOT NULL REFERENCES quality_reprocess_runs(id) ON DELETE CASCADE,
			bookmark_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			job_id TEXT REFERENCES jobs(id) ON DELETE SET NULL,
			status TEXT NOT NULL CHECK(status IN ('eligible','queued','processing','completed','partial','failed','skipped')) DEFAULT 'eligible',
			reason TEXT NOT NULL DEFAULT '',
			expected_evidence_hash TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(run_id, bookmark_id),
			FOREIGN KEY(bookmark_id, user_id) REFERENCES bookmarks(id, user_id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_quality_reprocess_items_job ON quality_reprocess_items(job_id) WHERE job_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_quality_reprocess_items_status ON quality_reprocess_items(run_id, status, updated_at)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("quality operations migration: %w", err)
		}
	}
	return nil
}

func ensureInsightFeedback(ctx context.Context, db *sql.DB) error {
	columns := map[string]string{
		"detector_family":  "TEXT NOT NULL DEFAULT ''",
		"detector_version": "TEXT NOT NULL DEFAULT ''",
		"reason":           "TEXT NOT NULL DEFAULT ''",
	}
	for name, definition := range columns {
		if err := ensureColumn(ctx, db, "knowledge_feedback", name, definition); err != nil {
			return err
		}
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS insight_impressions (
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			insight_id TEXT NOT NULL,
			detector_family TEXT NOT NULL,
			detector_version TEXT NOT NULL,
			first_seen_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			impression_count INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY(user_id, insight_id, detector_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_insight_impressions_detector ON insight_impressions(user_id, detector_family, detector_version, last_seen_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("insight feedback migration: %w", err)
		}
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
			quality_score INTEGER NOT NULL DEFAULT 0 CHECK(quality_score BETWEEN 0 AND 100),
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
