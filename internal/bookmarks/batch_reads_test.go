package bookmarks

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

func TestCaptureStatusesMatchSingletonReadsAndIsolateOwners(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedKnowledgeUser(t, db, "other", "other@example.com")
	ids := []string{"none", "queued", "running", "partial", "complete", "failed"}
	for _, user := range []string{"owner", "other"} {
		for _, id := range ids {
			bookmarkID := user + "-" + id
			seedKnowledgeBookmark(t, db, user, bookmarkID, id, "2026-01-01T00:00:00Z")
			if id != "none" {
				_, err := db.Exec(`INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,queued_at) VALUES(?,?,?,?,?,?)`, "attempt-"+bookmarkID, bookmarkID, user, id, "https://example.com", "2026-01-01T00:00:00Z")
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	ownerIDs := make([]string, 0, len(ids)+1)
	for _, id := range ids {
		ownerIDs = append(ownerIDs, "owner-"+id)
	}
	ownerIDs = append(ownerIDs, "other-failed")
	got := service.captureStatuses(t.Context(), "owner", ownerIDs)
	expected := map[string]string{"owner-none": "saved", "owner-queued": "processing", "owner-running": "processing", "owner-partial": "partially_preserved", "owner-complete": "saved", "owner-failed": "failed", "other-failed": "saved"}
	for _, id := range ownerIDs {
		if got[id] != expected[id] {
			t.Errorf("%s: batch=%q want=%q", id, got[id], expected[id])
		}
	}
	for _, fixture := range []struct {
		staged  int
		deleted any
		want    string
	}{{0, nil, "preserved"}, {1, nil, "saved"}, {0, "2026-02-01", "saved"}} {
		if _, err := db.Exec(`INSERT OR REPLACE INTO artifacts(id,user_id,bookmark_id,capture_attempt_id,artifact_type,mime_type,byte_size,sha256,storage_key,is_staged,deleted_at,created_at) VALUES('artifact','owner','owner-complete','attempt-owner-complete','pdf','application/pdf',1,'hash','key',?,?, '2026-01-01')`, fixture.staged, fixture.deleted); err != nil {
			t.Fatal(err)
		}
		if status := service.captureStatuses(t.Context(), "owner", ownerIDs)["owner-complete"]; status != fixture.want {
			t.Fatalf("artifact staged=%d deleted=%v: got %q want %q", fixture.staged, fixture.deleted, status, fixture.want)
		}
	}
	if _, err := db.Exec(`INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,queued_at) VALUES('retry','owner-failed','owner','queued','https://example.com','2026-02-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if status := service.captureStatuses(t.Context(), "owner", ownerIDs)["owner-failed"]; status != "processing" {
		t.Fatalf("latest attempt lost: %q", status)
	}
}

func BenchmarkCaptureStatusReads1000(b *testing.B) {
	db, err := database.Open(context.Background(), filepath.Join(b.TempDir(), "arivu.sqlite3"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	if _, err = db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "owner", "owner@example.com", "owner", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
		b.Fatal(err)
	}
	ids := make([]string, 1000)
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for i := range ids {
		ids[i] = fmt.Sprintf("b-%04d", i)
		now := "2026-01-01T00:00:00Z"
		if _, err = tx.Exec(`INSERT INTO bookmarks(id,user_id,url,title,domain,source_published_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, ids[i], "owner", "https://example.com/"+ids[i], ids[i], "example.com", now, now, now); err != nil {
			b.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,queued_at) VALUES(?,?,?,?,?,?)`, "a-"+ids[i], ids[i], "owner", "queued", "https://example.com", now); err != nil {
			b.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.Run("singleton", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			for _, id := range ids {
				service.captureStatus(b.Context(), "owner", id)
			}
		}
	})
	b.Run("batch", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			service.captureStatuses(b.Context(), "owner", ids)
		}
	})
}
