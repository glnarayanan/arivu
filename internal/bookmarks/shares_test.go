package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

func TestPublicRSSUsesRSSDatesAndBoundsNewestItems(t *testing.T) {
	service, db := newShareTestService(t)
	token := "public-token"
	seedPublicShare(t, db, token)
	for i := 0; i < 105; i++ {
		id := "bookmark-" + threeDigits(i)
		url := "https://example.com/" + id
		added := "2026-01-01T00:" + twoDigits(i%60) + ":00Z"
		_, err := db.Exec(`INSERT INTO bookmarks(id,user_id,url,title,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, "user-1", url, id, added, added)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`INSERT INTO public_share_items(share_id,bookmark_id,public_title,public_url,public_published_at,added_at) VALUES('share-1',?,?,?,?,?)`, id, id, url, "2026-01-02T03:04:05Z", threeDigits(i))
		if err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/s/"+token+"/rss", nil)
	req.SetPathValue("token", token)
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	service.PublicRSS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if got := strings.Count(body, "<item>"); got != 100 {
		t.Fatalf("item count=%d, want 100", got)
	}
	if !strings.Contains(body, "<pubDate>Fri, 02 Jan 2026 03:04:05 +0000</pubDate>") {
		t.Fatalf("RSS date was not RFC 1123Z: %s", body[:min(len(body), 500)])
	}
	if strings.Contains(body, "bookmark-000") || !strings.Contains(body, "bookmark-104") {
		t.Fatal("feed did not retain the newest bounded items")
	}
}

func TestPublicShareJSONGroupsArtifactsWithItems(t *testing.T) {
	service, db := newShareTestService(t)
	token := "public-token"
	seedPublicShare(t, db, token)
	_, err := db.Exec(`INSERT INTO bookmarks(id,user_id,url,title,created_at,updated_at) VALUES('bookmark-1','user-1','https://example.com/one','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,queued_at) VALUES('attempt-1','bookmark-1','user-1','complete','https://example.com/one','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO artifacts(id,user_id,bookmark_id,capture_attempt_id,artifact_type,mime_type,byte_size,sha256,storage_key,created_at) VALUES('artifact-1','user-1','bookmark-1','attempt-1','screenshot','image/png',42,'digest','objects/artifact-1','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO public_share_items(share_id,bookmark_id,public_title,public_url,public_published_at,added_at) VALUES('share-1','bookmark-1','One','https://example.com/one','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO public_share_artifacts(share_id,artifact_id,bookmark_id,artifact_type,storage_key,mime_type,byte_size,added_at) VALUES('share-1','artifact-1','bookmark-1','screenshot','objects/artifact-1','image/png',42,'2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/s/"+token+".json", nil)
	req.SetPathValue("token", token)
	req.RemoteAddr = "192.0.2.11:1234"
	rec := httptest.NewRecorder()
	service.PublicShareJSON(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []struct {
			Artifacts []struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"artifacts"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || len(payload.Items[0].Artifacts) != 1 || payload.Items[0].Artifacts[0].ID != "artifact-1" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload.Items[0].Artifacts[0].URL != "/s/public-token/artifacts/artifact-1" {
		t.Fatalf("unexpected artifact URL: %q", payload.Items[0].Artifacts[0].URL)
	}
	if _, err := db.Exec(`DELETE FROM bookmarks WHERE id='bookmark-1'`); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	service.PublicShareJSON(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot after private deletion: status=%d body=%s", rec.Code, rec.Body.String())
	}
	payload.Items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || len(payload.Items[0].Artifacts) != 1 || payload.Items[0].Artifacts[0].ID != "artifact-1" {
		t.Fatalf("published artifact did not survive private bookmark deletion: %#v", payload)
	}
}

func TestPublicRateRemovesExpiredWindows(t *testing.T) {
	service, db := newShareTestService(t)
	publicRateCleanupUnix.Store(0)
	_, err := db.Exec(`INSERT INTO rate_limits(key,window_start,count,expires_at) VALUES('expired','2025-01-01T00:00:00Z',5,'2025-01-01T00:01:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/s/token", nil)
	req.RemoteAddr = "192.0.2.12:1234"
	if !service.publicRate(req) {
		t.Fatal("first request should be allowed")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM rate_limits WHERE key='expired'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("expired rate-limit window was not removed")
	}
}

func TestValidShareRejectsRevokedAndExpiredTokens(t *testing.T) {
	service, db := newShareTestService(t)
	token := "public-token"
	seedPublicShare(t, db, token)
	req := httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
	req.SetPathValue("token", token)
	if _, err := service.validShare(req); err != nil {
		t.Fatalf("active share rejected: %v", err)
	}
	if _, err := db.Exec(`UPDATE public_shares SET revoked_at='2026-01-01T00:00:00Z' WHERE id='share-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.validShare(req); err == nil {
		t.Fatal("revoked share remained accessible")
	}
	if _, err := db.Exec(`UPDATE public_shares SET revoked_at=NULL,expires_at='2025-01-01T00:00:00Z' WHERE id='share-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.validShare(req); err == nil {
		t.Fatal("expired share remained accessible")
	}
}

func newShareTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{}), db
}

func seedPublicShare(t *testing.T, db *sql.DB, token string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('user-1','user@example.com','User','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO public_shares(id,user_id,token_digest,title,description,indexable,created_at,updated_at) VALUES('share-1','user-1',?,'Shared','Description',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, tokenDigest(token))
	if err != nil {
		t.Fatal(err)
	}
}

func threeDigits(value int) string {
	return string(rune('0'+value/100)) + twoDigits(value%100)
}

func twoDigits(value int) string {
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}
