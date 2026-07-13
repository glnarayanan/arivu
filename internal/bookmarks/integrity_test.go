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
