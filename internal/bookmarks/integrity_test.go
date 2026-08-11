package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

func TestCreateRemovesBookmarkWhenDurableCaptureCannotBeQueued(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	service.enqueueCreate = func(context.Context, *sql.Tx, string, string, string) (string, error) {
		return "", errors.New("queue unavailable")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/bookmarks", strings.NewReader(`{"url":"https://example.com/article"}`))
	rec := httptest.NewRecorder()
	service.Create(rec, req, auth.User{ID: "u1"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE user_id='u1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("bookmark survived failed capture enqueue: count=%d", count)
	}
}

func TestCreateExtensionAnnotationRollsBackWhenCaptureCannotBeQueued(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	service.enqueueCreate = func(context.Context, *sql.Tx, string, string, string) (string, error) {
		return "", errors.New("queue unavailable")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/extension/annotations", strings.NewReader(`{"url":"https://example.com/article","quote":"Selected passage"}`))
	rec := httptest.NewRecorder()
	service.CreateExtensionAnnotation(rec, req, auth.User{ID: "u1"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, table := range []string{"bookmarks", "ai_summaries", "item_states", "annotations", "jobs"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s survived failed extension capture enqueue: count=%d err=%v", table, count, err)
		}
	}
}

func TestCreateRollsBackWhenRequiredCompanionFails(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'); CREATE TRIGGER reject_initial_summary BEFORE INSERT ON ai_summaries BEGIN SELECT RAISE(ABORT, 'summary unavailable'); END;`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	_, err = service.CreateBookmark(context.Background(), CreateBookmarkInput{UserID: "u1", URL: "https://example.com/atomic", Title: "Atomic", Domain: "example.com", Note: "remember", Tags: []string{"Go"}})
	if err == nil {
		t.Fatal("CreateBookmark succeeded despite required companion failure")
	}
	for _, table := range []string{"bookmarks", "capture_attempts", "jobs", "item_states", "tags", "annotations"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s survived rollback: count=%d err=%v", table, count, err)
		}
	}
}

func TestCreateDeduplicatesTagsBySlug(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	result, err := service.CreateBookmark(context.Background(), CreateBookmarkInput{UserID: "u1", URL: "https://example.com/tags", Title: "Tags", Domain: "example.com", Tags: []string{"C", "C++", "!!!"}})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmark_tags WHERE bookmark_id=?`, result.BookmarkID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("slug-equivalent tags count=%d err=%v", count, err)
	}
}

func TestRepairSourceCaptureDoesNotDuplicateActiveJobForNormalizedURL(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO bookmarks(id,user_id,url,title,description,domain,text_content,source,created_at,updated_at) VALUES('b1','u1','https://example.com/post?utm_source=x','Old','','example.com','old','x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO jobs(id,user_id,type,status,priority,payload_json,attempts,max_attempts,run_after,created_at,updated_at) VALUES('j1','u1','bookmark.process','queued',0,'{"bookmark_id":"b1","url":"https://example.com/post?utm_source=x"}',0,3,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	repaired, err := service.RepairSourceCapture(context.Background(), "b1", SourceCaptureInput{
		UserID: "u1", URL: "https://example.com/post#section", URLKey: "https://example.com/post",
		Title: "Repaired", Domain: "example.com", Source: "x", Primary: BookmarkEvidence{Kind: "source_post", Origin: "x_api", Text: "repaired", QualityStatus: "complete", ExtractorVersion: "x-api-v1", Selected: true},
	}, true, false)
	if err != nil || !repaired {
		t.Fatalf("RepairSourceCapture repaired=%v err=%v", repaired, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE user_id='u1' AND type='bookmark.process' AND status IN ('queued','leased') AND json_extract(payload_json,'$.bookmark_id')='b1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("active jobs count=%d err=%v", count, err)
	}
}

func TestRepairSourceCaptureReplacesStaleQualityJobForSameURL(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO bookmarks(id,user_id,url,title,description,domain,text_content,source,created_at,updated_at) VALUES('b1','u1','https://example.com/post?utm_source=old','Old','','example.com','old','x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES('s1','b1','u1','pending','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO quality_reprocess_runs(id,scope_type,scope_user_id,target_fetch_version,target_summary_version,target_enrichment_version,status,total_candidates,queued_count,created_at,updated_at) VALUES('run1','user','u1','fetch','summary','enrichment','queued',1,1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO jobs(id,user_id,type,status,priority,payload_json,attempts,max_attempts,run_after,created_at,updated_at) VALUES('j1','u1','bookmark.process','queued',0,'{"bookmark_id":"b1","url":"https://example.com/post?utm_source=old","quality_reprocess_run_id":"run1","expected_evidence_hash":"old-hash"}',0,3,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO quality_reprocess_items(run_id,bookmark_id,user_id,job_id,status,expected_evidence_hash,created_at,updated_at) VALUES('run1','b1','u1','j1','queued','old-hash','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	repaired, err := service.RepairSourceCapture(context.Background(), "b1", SourceCaptureInput{
		UserID: "u1", URL: "https://example.com/post#current", URLKey: "https://example.com/post",
		Title: "Repaired", Domain: "example.com", Source: "x", Primary: BookmarkEvidence{Kind: "source_post", Origin: "x_api", Text: "new source", QualityStatus: "complete", ExtractorVersion: "x-api-v1", Selected: true},
	}, true, false)
	if err != nil || !repaired {
		t.Fatalf("RepairSourceCapture repaired=%v err=%v", repaired, err)
	}
	var replacementPayload string
	if err := db.QueryRow(`SELECT payload_json FROM jobs WHERE user_id='u1' AND type='bookmark.process' AND status='queued' AND id<>'j1' AND json_extract(payload_json,'$.bookmark_id')='b1'`).Scan(&replacementPayload); err != nil {
		t.Fatalf("replacement job: %v", err)
	}
	if err := service.ProcessJob(context.Background(), "bookmark.process", `{"bookmark_id":"b1","url":"https://example.com/post?utm_source=old","quality_reprocess_run_id":"run1","expected_evidence_hash":"old-hash"}`); err != nil {
		t.Fatal(err)
	}
	var qualityStatus string
	if err := db.QueryRow(`SELECT status FROM quality_reprocess_items WHERE run_id='run1' AND bookmark_id='b1'`).Scan(&qualityStatus); err != nil || qualityStatus != "skipped" {
		t.Fatalf("stale quality status=%q err=%v", qualityStatus, err)
	}
	if err := service.ProcessJob(context.Background(), "bookmark.process", replacementPayload); err != nil {
		t.Fatal(err)
	}
	var summaryVersion, enrichmentVersion string
	if err := db.QueryRow(`SELECT summary_version,enrichment_version FROM bookmarks WHERE id='b1'`).Scan(&summaryVersion, &enrichmentVersion); err != nil {
		t.Fatal(err)
	}
	if summaryVersion != providers.SummaryPromptVersion || enrichmentVersion != providers.SemanticVersion {
		t.Fatalf("replacement did not process repaired bookmark: summary=%q enrichment=%q", summaryVersion, enrichmentVersion)
	}
}

func TestRepairSourceCaptureIgnoresActiveJobForDifferentURL(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO bookmarks(id,user_id,url,title,description,domain,text_content,source,created_at,updated_at) VALUES('b1','u1','https://example.com/a','Old','','example.com','old','x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES('s1','b1','u1','pending','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO jobs(id,user_id,type,status,priority,payload_json,attempts,max_attempts,run_after,created_at,updated_at) VALUES('j1','u1','bookmark.process','leased',0,'{"bookmark_id":"b1","url":"https://example.com/b","url_key":"https://example.com/b"}',0,3,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	_, err = service.RepairSourceCapture(context.Background(), "b1", SourceCaptureInput{
		UserID: "u1", URL: "https://example.com/a#current", URLKey: "https://example.com/a",
		Title: "Repaired", Domain: "example.com", Source: "x", Primary: BookmarkEvidence{Kind: "source_post", Origin: "x_api", Text: "new source", QualityStatus: "complete", ExtractorVersion: "x-api-v1", Selected: true},
	}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var replacementPayload string
	if err := db.QueryRow(`SELECT payload_json FROM jobs WHERE user_id='u1' AND type='bookmark.process' AND status='queued' AND id<>'j1' AND json_extract(payload_json,'$.bookmark_id')='b1'`).Scan(&replacementPayload); err != nil {
		t.Fatalf("current URL replacement job: %v", err)
	}
	if err := service.ProcessJob(context.Background(), "bookmark.process", `{"bookmark_id":"b1","url":"https://example.com/b","url_key":"https://example.com/b"}`); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessJob(context.Background(), "bookmark.process", replacementPayload); err != nil {
		t.Fatal(err)
	}
	var summaryVersion, enrichmentVersion string
	if err := db.QueryRow(`SELECT summary_version,enrichment_version FROM bookmarks WHERE id='b1'`).Scan(&summaryVersion, &enrichmentVersion); err != nil {
		t.Fatal(err)
	}
	if summaryVersion != providers.SummaryPromptVersion || enrichmentVersion != providers.SemanticVersion {
		t.Fatalf("current URL replacement was suppressed: summary=%q enrichment=%q", summaryVersion, enrichmentVersion)
	}
}

func TestRepairSourceCaptureDoesNotReuseCompletedLeasedJob(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO bookmarks(id,user_id,url,title,description,domain,text_content,source,created_at,updated_at) VALUES('b1','u1','https://example.com/a','Old','','example.com','old','x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES('s1','b1','u1','pending','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO jobs(id,user_id,type,status,priority,payload_json,attempts,max_attempts,run_after,created_at,updated_at) VALUES('j1','u1','bookmark.process','leased',0,'{"bookmark_id":"b1","url":"https://example.com/a","url_key":"https://example.com/a"}',0,3,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	if err := service.ProcessJob(context.Background(), "bookmark.process", `{"bookmark_id":"b1","url":"https://example.com/a","url_key":"https://example.com/a"}`); err != nil {
		t.Fatal(err)
	}
	_, err = service.RepairSourceCapture(context.Background(), "b1", SourceCaptureInput{
		UserID: "u1", URL: "https://example.com/a", URLKey: "https://example.com/a",
		Title: "Repaired", Domain: "example.com", Source: "x", Primary: BookmarkEvidence{Kind: "source_post", Origin: "x_api", Text: "new source", QualityStatus: "complete", ExtractorVersion: "x-api-v1", Selected: true},
	}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var replacementPayload string
	if err := db.QueryRow(`SELECT payload_json FROM jobs WHERE user_id='u1' AND type='bookmark.process' AND status='queued' AND id<>'j1' AND json_extract(payload_json,'$.bookmark_id')='b1'`).Scan(&replacementPayload); err != nil {
		t.Fatalf("replacement after leased handoff: %v", err)
	}
	if err := service.ProcessJob(context.Background(), "bookmark.process", replacementPayload); err != nil {
		t.Fatal(err)
	}
	var summaryVersion, enrichmentVersion string
	if err := db.QueryRow(`SELECT summary_version,enrichment_version FROM bookmarks WHERE id='b1'`).Scan(&summaryVersion, &enrichmentVersion); err != nil {
		t.Fatal(err)
	}
	if summaryVersion != providers.SummaryPromptVersion || enrichmentVersion != providers.SemanticVersion {
		t.Fatalf("leased handoff suppressed replacement: summary=%q enrichment=%q", summaryVersion, enrichmentVersion)
	}
}

func TestRepairSourceCaptureQueuesChangedNormalizedURL(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO bookmarks(id,user_id,url,title,description,domain,text_content,source,created_at,updated_at) VALUES('b1','u1','https://x.com/user/status/1','Old','','x.com','old','x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO quality_reprocess_runs(id,scope_type,scope_user_id,target_fetch_version,target_summary_version,target_enrichment_version,status,total_candidates,queued_count,created_at,updated_at) VALUES('run1','user','u1','fetch','summary','enrichment','queued',1,1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO jobs(id,user_id,type,status,priority,payload_json,attempts,max_attempts,run_after,created_at,updated_at) VALUES('j1','u1','bookmark.process','queued',0,'{"bookmark_id":"b1","url":"https://x.com/user/status/1","quality_reprocess_run_id":"run1"}',0,3,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO quality_reprocess_items(run_id,bookmark_id,user_id,job_id,status,created_at,updated_at) VALUES('run1','b1','u1','j1','queued','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	repaired, err := service.RepairSourceCapture(context.Background(), "b1", SourceCaptureInput{
		UserID: "u1", URL: "https://example.com/article", URLKey: "https://example.com/article",
		Title: "Article", Domain: "example.com", Source: "x", Primary: BookmarkEvidence{Kind: "source_post", Origin: "x_api", Text: "article", QualityStatus: "complete", ExtractorVersion: "x-api-v1", Selected: true},
	}, true, false)
	if err != nil || !repaired {
		t.Fatalf("RepairSourceCapture repaired=%v err=%v", repaired, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE user_id='u1' AND type='bookmark.process' AND status IN ('queued','leased') AND json_extract(payload_json,'$.bookmark_id')='b1'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("changed URL active jobs count=%d err=%v", count, err)
	}
	if err := service.ProcessJob(context.Background(), "bookmark.process", `{"bookmark_id":"b1","url":"https://x.com/user/status/1","quality_reprocess_run_id":"run1"}`); err != nil {
		t.Fatal(err)
	}
	var currentURL, text string
	if err := db.QueryRow(`SELECT url,text_content FROM bookmarks WHERE id='b1'`).Scan(&currentURL, &text); err != nil {
		t.Fatal(err)
	}
	if currentURL != "https://example.com/article" || text != "article" {
		t.Fatalf("stale job overwrote repaired source: url=%q text=%q", currentURL, text)
	}
	var itemStatus, runStatus string
	if err := db.QueryRow(`SELECT status FROM quality_reprocess_items WHERE run_id='run1' AND bookmark_id='b1'`).Scan(&itemStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM quality_reprocess_runs WHERE id='run1'`).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if itemStatus != "skipped" || runStatus != "completed" {
		t.Fatalf("superseded quality work item=%q run=%q", itemStatus, runStatus)
	}
}

func TestSearchRebuildFailurePreservesExistingProjection(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES('n1','u1','Original','body','manual','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO search_index(user_id,item_type,item_id,title,body,updated_at) VALUES('u1','note','n1','Original','body','2026-01-01T00:00:00Z');
		CREATE TRIGGER reject_search_replacement BEFORE INSERT ON search_index BEGIN SELECT RAISE(ABORT, 'projection unavailable'); END;
		UPDATE notes SET title='Replacement' WHERE id='n1';
	`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	if _, err = service.rebuildSearchIndex(context.Background(), "u1"); err == nil {
		t.Fatal("rebuildSearchIndex succeeded despite replacement failure")
	}
	var title string
	if err = db.QueryRow(`SELECT title FROM search_index WHERE user_id='u1' AND item_type='note' AND item_id='n1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Original" {
		t.Fatalf("existing projection was not preserved: title=%q", title)
	}
}

func TestInsertSourceCaptureRollsBackRequiredWrites(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'); CREATE TRIGGER reject_source_summary BEFORE INSERT ON ai_summaries BEGIN SELECT RAISE(ABORT, 'summary unavailable'); END;`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	_, err = service.InsertSourceCapture(context.Background(), SourceCaptureInput{
		UserID: "u1", URL: "https://x.com/example/status/1", Title: "Source", Domain: "x.com", Source: "x",
		Primary: BookmarkEvidence{Kind: "source_post", Origin: "x_api", Text: "Source text", QualityStatus: "complete", ExtractorVersion: "x-api-v1", Selected: true},
	})
	if err == nil {
		t.Fatal("InsertSourceCapture succeeded despite required summary failure")
	}
	for _, table := range []string{"bookmarks", "bookmark_evidence", "ai_summaries", "jobs"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s survived source capture rollback: count=%d err=%v", table, count, err)
		}
	}
}

func TestDeleteItemCommandRemovesPolymorphicState(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES('n1','u1','One','','manual','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'),('n2','u1','Two','','manual','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO item_states(user_id,item_type,item_id,stage,importance,next_action,created_at,updated_at) VALUES('u1','note','n1','inbox',0,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO review_events(id,user_id,item_type,item_id,action,created_at) VALUES('r1','u1','note','n1','completed','2026-01-01T00:00:00Z');
		INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES('l1','u1','note','n1','note','n2','','manual','2026-01-01T00:00:00Z');
		INSERT INTO result_feedback(user_id,item_type,item_id,surface,feedback,created_at,updated_at) VALUES('u1','note','n1','search','useful','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO search_index(user_id,item_type,item_id,title,body,updated_at) VALUES('u1','note','n1','One','','2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	deleted, err := service.DeleteItemCommand(context.Background(), "u1", "note", "n1")
	if err != nil || !deleted {
		t.Fatalf("DeleteItemCommand deleted=%v err=%v", deleted, err)
	}
	for _, table := range []string{"item_states", "review_events", "item_links", "result_feedback", "search_index"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE user_id='u1'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained deleted note state: count=%d err=%v", table, count, err)
		}
	}
}

func TestDeleteItemCommandWaitsForSearchProjectionOwner(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES('n1','u1','One','','manual','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	service.searchMu.Lock()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := service.DeleteItemCommand(context.Background(), "u1", "note", "n1")
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		service.searchMu.Unlock()
		t.Fatalf("delete bypassed search projection ownership: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	service.searchMu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notes WHERE id='n1'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("note remained after serialized deletion: count=%d err=%v", count, err)
	}
}

func TestDeleteItemCommandRollsBackWhenProjectionDeleteFails(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		INSERT INTO users(id,email,name,created_at,updated_at) VALUES('u1','one@example.com','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES('n1','u1','One','','manual','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
		INSERT INTO search_index(user_id,item_type,item_id,title,body,updated_at) VALUES('u1','note','n1','One','','2026-01-01T00:00:00Z');
		CREATE TRIGGER reject_search_delete BEFORE DELETE ON search_index BEGIN SELECT RAISE(ABORT, 'projection unavailable'); END;
	`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	if _, err = service.DeleteItemCommand(context.Background(), "u1", "note", "n1"); err == nil {
		t.Fatal("DeleteItemCommand succeeded despite projection delete failure")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM notes WHERE id='n1' AND user_id='u1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("source note did not roll back: count=%d err=%v", count, err)
	}
}

func TestEvidencePayloadUsesStableFrontendFields(t *testing.T) {
	payload := evidencePayload([]BookmarkEvidence{{
		ID: "e1", Kind: "source_native", Origin: "x", Authority: 90,
		CanonicalURL: "https://example.com/source", ExtractionMethod: "api",
		QualityStatus: "complete", QualityReasons: []string{"authoritative"},
		ExtractorVersion: "x-v1", Selected: true, Text: strings.Repeat("e", 900),
	}})
	if len(payload) != 1 || payload[0]["kind"] != "source_native" || payload[0]["selected"] != true {
		t.Fatalf("unexpected evidence payload: %#v", payload)
	}
	preview, _ := payload[0]["preview"].(string)
	if len([]rune(preview)) > 803 || !strings.HasSuffix(preview, "...") || payload[0]["canonical_url"] != "https://example.com/source" {
		t.Fatalf("evidence inspection fields missing: %#v", payload[0])
	}
}
