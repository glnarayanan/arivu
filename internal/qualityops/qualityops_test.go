package qualityops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/database"
)

func TestAuditIsRedactedAndReadOnly(t *testing.T) {
	dbPath := seedQualityDatabase(t, 2)
	report, err := Audit(context.Background(), AuditOptions{DBPath: dbPath, Now: time.Date(2026, 7, 11, 9, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts["bookmarks"] != 2 || report.Evidence.Statuses["complete"] != 2 {
		t.Fatalf("unexpected audit counts: %#v", report)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private.example.test", "private evidence", "user@example.test", dbPath} {
		if strings.Contains(string(raw), private) {
			t.Errorf("redacted report contains %q: %s", private, raw)
		}
	}

	db, err := database.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var runs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM quality_reprocess_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("audit wrote reprocess state: runs=%d err=%v", runs, err)
	}
}

func TestReprocessDryRunDoesNotWrite(t *testing.T) {
	dbPath := seedQualityDatabase(t, 3)
	result, err := Reprocess(context.Background(), ReprocessOptions{DBPath: dbPath, BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "dry-run" || result.Eligible != 3 || result.Queued != 0 || result.BackupVerified {
		t.Fatalf("dry-run result = %#v", result)
	}
	db, err := database.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var runs, jobs int
	_ = db.QueryRow(`SELECT COUNT(*) FROM quality_reprocess_runs`).Scan(&runs)
	_ = db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs)
	if runs != 0 || jobs != 0 {
		t.Fatalf("dry run wrote runs=%d jobs=%d", runs, jobs)
	}
}

func TestReprocessApplyRequiresVerifiedMatchingBackupAndQueuesBoundedIdempotentBatches(t *testing.T) {
	dbPath := seedQualityDatabase(t, 3)
	backupPath := filepath.Join(t.TempDir(), "arivu.sqlite3")
	copyFile(t, dbPath, backupPath)

	options := ReprocessOptions{DBPath: dbPath, BackupPath: backupPath, UserID: "user-1", BatchSize: 2, Apply: true}
	first, err := Reprocess(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !first.BackupVerified || first.Queued != 2 || first.RunID == "" {
		t.Fatalf("first apply = %#v", first)
	}
	second, err := Reprocess(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if second.RunID != first.RunID || second.Queued != 1 || second.AlreadyTracked != 2 {
		t.Fatalf("second apply = %#v, first = %#v", second, first)
	}
	third, err := Reprocess(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if third.Queued != 0 || third.AlreadyTracked != 3 {
		t.Fatalf("third apply = %#v", third)
	}

	db, err := database.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var runs, items, queuedJobs int
	_ = db.QueryRow(`SELECT COUNT(*) FROM quality_reprocess_runs`).Scan(&runs)
	_ = db.QueryRow(`SELECT COUNT(*) FROM quality_reprocess_items`).Scan(&items)
	_ = db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='bookmark.process'`).Scan(&queuedJobs)
	if runs != 1 || items != 3 || queuedJobs != 3 {
		t.Fatalf("durable state runs=%d items=%d jobs=%d", runs, items, queuedJobs)
	}
	var payload string
	if err := db.QueryRow(`SELECT payload_json FROM jobs ORDER BY created_at LIMIT 1`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"quality_reprocess_run_id", "target_fetch_version", "target_summary_version", "target_enrichment_version", "expected_evidence_hash"} {
		if !strings.Contains(payload, field) {
			t.Errorf("job payload missing %s: %s", field, payload)
		}
	}
}

func TestReprocessApplyRejectsUnsafeScopeAndBackup(t *testing.T) {
	dbPath := seedQualityDatabase(t, 1)
	if _, err := Reprocess(context.Background(), ReprocessOptions{DBPath: dbPath, BackupPath: dbPath, UserID: "user-1", Apply: true}); err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("same-file backup error = %v", err)
	}
	if _, err := Reprocess(context.Background(), ReprocessOptions{DBPath: dbPath, BackupPath: dbPath, AllUsers: true, Apply: true}); err == nil || !strings.Contains(err.Error(), "confirm-all-users") {
		t.Fatalf("all-user confirmation error = %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "arivu.sqlite3")
	copyFile(t, dbPath, backupPath)
	db, err := database.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE bookmarks SET title='changed after backup' WHERE id='bookmark-1'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Reprocess(context.Background(), ReprocessOptions{DBPath: dbPath, BackupPath: backupPath, UserID: "user-1", Apply: true}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("protected-data mismatch error = %v", err)
	}
}

func seedQualityDatabase(t *testing.T, bookmarks int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "arivu.sqlite3")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := "2026-07-11T08:00:00Z"
	if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('user-1','user@example.test','Private User',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= bookmarks; i++ {
		id := "bookmark-" + string(rune('0'+i))
		url := "https://private.example.test/" + id
		if _, err := db.Exec(`INSERT INTO bookmarks(id,user_id,url,title,text_content,domain,source,canonical_url,content_kind,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, "user-1", url, "Private title", "private evidence", "private.example.test", "web", url, "article", now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,authority,content_text,canonical_url,publisher_key,published_at,extraction_method,content_hash,quality_status,quality_reasons_json,extractor_version,is_selected,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "evidence-"+id, id, "user-1", "web_article", "fetched", 80, "private evidence", url, "private.example.test", now, "readability", "hash-"+id, "complete", "[]", "old-fetch", 1, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "summary-"+id, id, "user-1", "Private summary", "completed", now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func copyFile(t *testing.T, source, target string) {
	t.Helper()
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
