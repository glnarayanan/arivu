package qualityops

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
	_ "github.com/mattn/go-sqlite3"
)

const (
	TargetFetchVersion      = safefetch.ExtractorVersion
	TargetSummaryVersion    = providers.SummaryPromptVersion
	TargetEnrichmentVersion = providers.SemanticVersion
)

type ReprocessOptions struct {
	DBPath          string
	BackupPath      string
	UserID          string
	AllUsers        bool
	ConfirmAllUsers bool
	BatchSize       int
	Apply           bool
}

type ReprocessResult struct {
	Mode                 string            `json:"mode"`
	RunID                string            `json:"run_id,omitempty"`
	Scope                string            `json:"scope"`
	TargetVersions       map[string]string `json:"target_versions"`
	Eligible             int               `json:"eligible"`
	Queued               int               `json:"queued"`
	AlreadyTracked       int               `json:"already_tracked"`
	PreservedValidOutput int               `json:"preserved_valid_output"`
	BackupVerified       bool              `json:"backup_verified"`
}

func Reprocess(ctx context.Context, options ReprocessOptions) (ReprocessResult, error) {
	options.DBPath = strings.TrimSpace(options.DBPath)
	options.UserID = strings.TrimSpace(options.UserID)
	if options.DBPath == "" {
		return ReprocessResult{}, errors.New("database path is required")
	}
	if options.BatchSize == 0 {
		options.BatchSize = 25
	}
	if options.BatchSize < 1 || options.BatchSize > 100 {
		return ReprocessResult{}, errors.New("batch size must be between 1 and 100")
	}
	if options.UserID != "" && options.AllUsers {
		return ReprocessResult{}, errors.New("choose either --user-id or --all-users, not both")
	}
	if options.Apply && options.UserID == "" && !options.AllUsers {
		return ReprocessResult{}, errors.New("mutating reprocessing requires --user-id or --all-users")
	}
	if options.Apply && options.AllUsers && !options.ConfirmAllUsers {
		return ReprocessResult{}, errors.New("all-user reprocessing requires --confirm-all-users")
	}

	result := ReprocessResult{
		Mode:  "dry-run",
		Scope: scopeLabel(options.UserID, options.AllUsers),
		TargetVersions: map[string]string{
			"fetch":      TargetFetchVersion,
			"summary":    TargetSummaryVersion,
			"enrichment": TargetEnrichmentVersion,
		},
	}
	if !options.Apply {
		db, err := openReadOnly(options.DBPath)
		if err != nil {
			return ReprocessResult{}, err
		}
		defer db.Close()
		result.Eligible, result.PreservedValidOutput, err = candidateCounts(ctx, db, options.UserID)
		return result, err
	}

	backupPath, backupSHA, backupDB, err := verifyBackup(options.DBPath, options.BackupPath)
	if err != nil {
		return ReprocessResult{}, err
	}
	defer backupDB.Close()
	_ = backupPath // Deliberately never persist or report operator filesystem paths.

	db, err := openExistingWrite(options.DBPath)
	if err != nil {
		return ReprocessResult{}, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ReprocessResult{}, err
	}
	defer tx.Rollback()

	sourceDigest, err := protectedDigest(ctx, tx, options.UserID)
	if err != nil {
		return ReprocessResult{}, fmt.Errorf("digest protected source data: %w", err)
	}
	backupDigest, err := protectedDigest(ctx, backupDB, options.UserID)
	if err != nil {
		return ReprocessResult{}, fmt.Errorf("digest protected backup data: %w", err)
	}
	if sourceDigest != backupDigest {
		return ReprocessResult{}, errors.New("backup does not match current protected user data; create a fresh consistent backup")
	}

	result.Mode = "apply"
	result.BackupVerified = true
	result.Eligible, result.PreservedValidOutput, err = candidateCounts(ctx, tx, options.UserID)
	if err != nil {
		return ReprocessResult{}, err
	}
	runID, reused, err := findOrCreateRun(ctx, tx, options, backupSHA, sourceDigest, result)
	if err != nil {
		return ReprocessResult{}, err
	}
	result.RunID = runID
	if reused {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM quality_reprocess_items WHERE run_id=?`, runID).Scan(&result.AlreadyTracked); err != nil {
			return ReprocessResult{}, err
		}
	}

	candidates, err := reprocessCandidates(ctx, tx, runID, options.UserID, options.BatchSize)
	if err != nil {
		return ReprocessResult{}, err
	}
	queue := jobs.New(db)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, candidate := range candidates {
		var existingJobID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE user_id=? AND type='bookmark.process' AND status IN ('queued','leased') AND json_extract(payload_json,'$.bookmark_id')=? AND json_extract(payload_json,'$.quality_reprocess_run_id')=? ORDER BY created_at LIMIT 1`, candidate.UserID, candidate.BookmarkID, runID).Scan(&existingJobID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return ReprocessResult{}, err
		}
		jobID := existingJobID
		if jobID == "" {
			payload, _ := json.Marshal(map[string]string{
				"bookmark_id":               candidate.BookmarkID,
				"url":                       candidate.URL,
				"quality_reprocess_run_id":  runID,
				"target_fetch_version":      TargetFetchVersion,
				"target_summary_version":    TargetSummaryVersion,
				"target_enrichment_version": TargetEnrichmentVersion,
				"expected_evidence_hash":    candidate.EvidenceHash,
			})
			jobID, err = queue.EnqueueWithIDTx(ctx, tx, candidate.UserID, "bookmark.process", string(payload))
			if err != nil {
				return ReprocessResult{}, err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO quality_reprocess_items(run_id,bookmark_id,user_id,job_id,status,reason,expected_evidence_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, runID, candidate.BookmarkID, candidate.UserID, jobID, "queued", "stale_version", candidate.EvidenceHash, now, now); err != nil {
			return ReprocessResult{}, err
		}
		result.Queued++
	}
	if _, err := tx.ExecContext(ctx, `UPDATE quality_reprocess_runs SET status=CASE WHEN ? > 0 THEN 'queued' ELSE status END,queued_count=(SELECT COUNT(*) FROM quality_reprocess_items WHERE run_id=? AND status IN ('queued','processing')),updated_at=? WHERE id=?`, result.Queued, runID, now, runID); err != nil {
		return ReprocessResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReprocessResult{}, err
	}
	return result, nil
}

type candidate struct {
	BookmarkID   string
	UserID       string
	URL          string
	EvidenceHash string
}

func candidateCounts(ctx context.Context, q queryer, userID string) (int, int, error) {
	where, args := scopeWhere(userID)
	query := `SELECT COUNT(*),COALESCE(SUM(CASE WHEN EXISTS (SELECT 1 FROM ai_summaries s WHERE s.bookmark_id=b.id AND s.user_id=b.user_id AND s.processing_status IN ('completed','fallback')) THEN 1 ELSE 0 END),0) FROM bookmarks b WHERE ` + where + ` AND (b.fetch_version<>? OR b.summary_version<>? OR b.enrichment_version<>?)`
	args = append(args, TargetFetchVersion, TargetSummaryVersion, TargetEnrichmentVersion)
	var eligible, preserved int
	if err := q.QueryRowContext(ctx, query, args...).Scan(&eligible, &preserved); err != nil {
		return 0, 0, err
	}
	return eligible, preserved, nil
}

func reprocessCandidates(ctx context.Context, q queryer, runID, userID string, limit int) ([]candidate, error) {
	where, args := scopeWhere(userID)
	query := `SELECT b.id,b.user_id,b.url,COALESCE((SELECT e.content_hash FROM bookmark_evidence e WHERE e.bookmark_id=b.id AND e.user_id=b.user_id AND e.is_selected=1 LIMIT 1),'') FROM bookmarks b WHERE ` + where + ` AND (b.fetch_version<>? OR b.summary_version<>? OR b.enrichment_version<>?) AND NOT EXISTS (SELECT 1 FROM quality_reprocess_items qi WHERE qi.run_id=? AND qi.bookmark_id=b.id) ORDER BY b.user_id,b.created_at,b.id LIMIT ?`
	args = append(args, TargetFetchVersion, TargetSummaryVersion, TargetEnrichmentVersion, runID, limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.BookmarkID, &item.UserID, &item.URL, &item.EvidenceHash); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func findOrCreateRun(ctx context.Context, tx *sql.Tx, options ReprocessOptions, backupSHA, digest string, result ReprocessResult) (string, bool, error) {
	scopeType := "user"
	if options.AllUsers {
		scopeType = "all"
	}
	var runID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM quality_reprocess_runs WHERE scope_type=? AND COALESCE(scope_user_id,'')=? AND target_fetch_version=? AND target_summary_version=? AND target_enrichment_version=? AND protected_digest=? AND status IN ('planned','queued','running','partial') ORDER BY created_at DESC LIMIT 1`, scopeType, options.UserID, TargetFetchVersion, TargetSummaryVersion, TargetEnrichmentVersion, digest).Scan(&runID)
	if err == nil {
		return runID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	runID = ids.New()
	now := time.Now().UTC().Format(time.RFC3339)
	var scopeUser any
	if options.UserID != "" {
		scopeUser = options.UserID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_reprocess_runs(id,scope_type,scope_user_id,target_fetch_version,target_summary_version,target_enrichment_version,backup_sha256,protected_digest,status,total_candidates,preserved_count,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, runID, scopeType, scopeUser, TargetFetchVersion, TargetSummaryVersion, TargetEnrichmentVersion, backupSHA, digest, "planned", result.Eligible, result.PreservedValidOutput, now, now)
	return runID, false, err
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scopeWhere(userID string) (string, []any) {
	if userID == "" {
		return "1=1", nil
	}
	return "b.user_id=?", []any{userID}
}

func scopeLabel(userID string, all bool) string {
	if userID != "" {
		return "user"
	}
	if all {
		return "all"
	}
	return "all-read-only"
}

func openExistingWrite(path string) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("database path is not a regular file")
	}
	db, err := sql.Open("sqlite3", sqliteURI(path, "rw")+"&_foreign_keys=on&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func openReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", sqliteURI(path, "ro")+"&_query_only=true&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func sqliteURI(path, mode string) string {
	abs, _ := filepath.Abs(path)
	u := url.URL{Scheme: "file", Path: abs}
	return u.String() + "?mode=" + mode
}

func verifyBackup(sourcePath, requestedPath string) (string, string, *sql.DB, error) {
	if strings.TrimSpace(requestedPath) == "" {
		return "", "", nil, errors.New("--backup is required for mutating reprocessing")
	}
	backupPath := requestedPath
	if info, err := os.Stat(backupPath); err == nil && info.IsDir() {
		backupPath = filepath.Join(backupPath, "arivu.sqlite3")
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return "", "", nil, err
	}
	backupInfo, err := os.Stat(backupPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("open backup: %w", err)
	}
	if !backupInfo.Mode().IsRegular() || os.SameFile(sourceInfo, backupInfo) {
		return "", "", nil, errors.New("backup must be a distinct regular SQLite database file")
	}
	db, err := openReadOnly(backupPath)
	if err != nil {
		return "", "", nil, err
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		_ = db.Close()
		if err != nil {
			return "", "", nil, fmt.Errorf("verify backup integrity: %w", err)
		}
		return "", "", nil, fmt.Errorf("verify backup integrity: %s", integrity)
	}
	digest, err := fileSHA256(backupPath)
	if err != nil {
		_ = db.Close()
		return "", "", nil, err
	}
	return backupPath, digest, db, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

var protectedQueries = []struct {
	name  string
	query string
}{
	{"bookmarks", `SELECT json_array(id,user_id,url,COALESCE(title,''),COALESCE(description,''),read_status,source,created_at) FROM bookmarks WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"notes", `SELECT json_array(id,user_id,title,body,source,created_at,updated_at) FROM notes WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"daily_notes", `SELECT json_array(user_id,note_date,body,created_at,updated_at) FROM daily_notes WHERE (?='' OR user_id=?) ORDER BY user_id,note_date`},
	{"annotations", `SELECT json_array(id,user_id,bookmark_id,quote,note,selector_json,tags_json,created_at,updated_at) FROM annotations WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"tags", `SELECT json_array(id,user_id,name,slug,source,created_at,updated_at) FROM tags WHERE source<>'enrichment' AND (?='' OR user_id=?) ORDER BY user_id,id`},
	{"tag_aliases", `SELECT json_array(id,user_id,tag_id,alias,alias_slug,created_at) FROM tag_aliases WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"bookmark_tags", `SELECT json_array(bookmark_id,tag_id,user_id,source,created_at) FROM bookmark_tags WHERE source<>'enrichment' AND (?='' OR user_id=?) ORDER BY user_id,bookmark_id,tag_id`},
	{"collections", `SELECT json_array(id,user_id,name,COALESCE(description,''),COALESCE(color,''),created_at,updated_at) FROM collections WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"collection_bookmarks", `SELECT json_array(collection_id,bookmark_id,user_id,added_at) FROM collection_bookmarks WHERE (?='' OR user_id=?) ORDER BY user_id,collection_id,bookmark_id`},
	{"saved_searches", `SELECT json_array(id,user_id,name,query,filters_json,created_at,updated_at) FROM saved_searches WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"review_events", `SELECT json_array(id,user_id,item_type,item_id,action,COALESCE(snoozed_until,''),created_at) FROM review_events WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"item_states", `SELECT json_array(user_id,item_type,item_id,stage,importance,next_action,created_at,updated_at) FROM item_states WHERE (?='' OR user_id=?) ORDER BY user_id,item_type,item_id`},
	{"item_links", `SELECT json_array(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) FROM item_links WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"reminders", `SELECT json_array(id,user_id,item_type,item_id,due_at,timezone,recurrence,recurrence_interval_days,notification_channel,note,status,created_at,COALESCE(completed_at,''),COALESCE(last_notified_at,''),COALESCE(last_completed_at,'')) FROM reminders WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"action_items", `SELECT json_array(id,user_id,item_type,item_id,title,status,created_at,COALESCE(completed_at,'')) FROM action_items WHERE (?='' OR user_id=?) ORDER BY user_id,id`},
	{"knowledge_feedback", `SELECT json_array(user_id,target_type,target_id,feedback,detector_family,detector_version,reason,COALESCE(snoozed_until,''),created_at,updated_at) FROM knowledge_feedback WHERE (?='' OR user_id=?) ORDER BY user_id,target_type,target_id`},
	{"result_feedback", `SELECT json_array(user_id,item_type,item_id,surface,feedback,created_at,updated_at) FROM result_feedback WHERE (?='' OR user_id=?) ORDER BY user_id,item_type,item_id,surface`},
}

func protectedDigest(ctx context.Context, q queryer, userID string) (string, error) {
	hash := sha256.New()
	for _, item := range protectedQueries {
		_, _ = io.WriteString(hash, item.name+"\n")
		rows, err := q.QueryContext(ctx, item.query, userID, userID)
		if err != nil {
			return "", fmt.Errorf("%s: %w", item.name, err)
		}
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				rows.Close()
				return "", err
			}
			_, _ = io.WriteString(hash, row+"\n")
		}
		if err := rows.Close(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
