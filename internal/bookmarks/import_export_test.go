package bookmarks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

func TestExtractImportURLsFromJSON(t *testing.T) {
	got := extractImportURLs(`[{"url":"https://example.com/a","title":"A"},{"link":"https://example.com/b","name":"B"},{"url":"file:///etc/passwd"}]`)
	if len(got) != 2 {
		t.Fatalf("expected 2 URLs, got %#v", got)
	}
	if got[0].Title != "A" || got[1].Title != "B" {
		t.Fatalf("titles not preserved: %#v", got)
	}
}

func TestExtractImportURLsFromHTML(t *testing.T) {
	got := extractImportURLs(`<DT><A HREF="https://example.com/article">Article</A><A HREF="javascript:alert(1)">bad</A>`)
	if len(got) != 1 || got[0].URL != "https://example.com/article" {
		t.Fatalf("unexpected URLs: %#v", got)
	}
}

func TestImportURLsUseSafeFetchValidation(t *testing.T) {
	got := extractImportURLs("https://127.0.0.1/admin\nhttps://example.com/ok\nftp://example.com/file")
	if len(got) != 1 || got[0].URL != "https://example.com/ok" {
		t.Fatalf("unexpected URLs: %#v", got)
	}
}

func TestCSVCellNeutralizesSpreadsheetFormulas(t *testing.T) {
	for _, value := range []string{"=cmd()", "+SUM(A1:A2)", "-10", "@link"} {
		if got := csvCell(value); got != "'"+value {
			t.Fatalf("csvCell(%q) = %q", value, got)
		}
	}
	if got := csvCell(" ordinary "); got != "ordinary" {
		t.Fatalf("csvCell trimmed ordinary value to %q", got)
	}
}

func TestAnalyticsInsightsIncludeStructuredLocalAndGeminiInsights(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.ExecContext(context.Background(), `INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "user-1", "user@example.com", "User", now, now)
	_, _ = db.ExecContext(context.Background(), `INSERT INTO bookmarks(id,user_id,url,title,domain,read_status,reading_time,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, "bookmark-1", "user-1", "https://example.com/a", "A", "example.com", false, 12, now, now)

	var geminiCalled bool
	geminiHTTP := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		geminiCalled = true
		response := map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{
						"text": "Review the unread example.com item before it goes stale.",
					}},
				},
			}},
		}
		return jsonResponse(http.StatusOK, response), nil
	})}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{APIKey: "test", BaseURL: "https://gemini.test", HTTP: geminiHTTP})
	req := httptest.NewRequest(http.MethodGet, "/api/analytics/insights", nil)
	rec := httptest.NewRecorder()
	service.AnalyticsInsights(rec, req, auth.User{ID: "user-1", Email: "user@example.com"})
	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Insights []map[string]any `json:"insights"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !geminiCalled {
		t.Fatal("expected Gemini insight request")
	}
	if len(body.Insights) < 2 {
		t.Fatalf("expected local and Gemini insights, got %#v", body.Insights)
	}
	for _, insight := range body.Insights {
		if insight["message"] == "" || insight["severity"] == "" || insight["type"] == "" {
			t.Fatalf("insight missing structured fields: %#v", insight)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}
