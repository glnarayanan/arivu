package bookmarks

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	got := extractImportURLs(`<!doctype NETSCAPE-Bookmark-file-1><DT><A HREF="https://example.com/article">Article</A><A HREF="javascript:alert(1)">bad</A>`)
	if len(got) != 1 || got[0].URL != "https://example.com/article" || got[0].Title != "Article" || got[0].Source != "browser" {
		t.Fatalf("unexpected URLs: %#v", got)
	}
}

func TestExtractImportURLsFromWrappedExports(t *testing.T) {
	got := extractImportURLs(`{"source":"raindrop","items":[{"link":"https://example.com/a","name":"A"},{"uri":"https://example.com/b","title":"B"}]}`)
	if len(got) != 2 || got[0].Title != "A" || got[1].URL != "https://example.com/b" || got[0].Source != "raindrop" {
		t.Fatalf("unexpected URLs: %#v", got)
	}
}

func TestExtractImportURLsFromOPML(t *testing.T) {
	got := extractImportURLs(`<opml version="2.0"><body><outline text="Go Blog" xmlUrl="https://go.dev/blog/feed.atom"/><outline title="Bad" xmlUrl="file:///etc/passwd"/></body></opml>`)
	if len(got) != 1 || got[0].URL != "https://go.dev/blog/feed.atom" || got[0].Title != "Go Blog" || got[0].Source != "opml" {
		t.Fatalf("unexpected OPML URLs: %#v", got)
	}
}

func TestExtractImportURLsFromRSSAndAtom(t *testing.T) {
	rss := extractImportURLs(`<rss><channel><item><title>One</title><link>https://example.com/one</link></item><item><title>Bad</title><link>javascript:alert(1)</link></item></channel></rss>`)
	if len(rss) != 1 || rss[0].Title != "One" || rss[0].URL != "https://example.com/one" || rss[0].Source != "rss" {
		t.Fatalf("unexpected RSS URLs: %#v", rss)
	}
	atom := extractImportURLs(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><title>Two</title><link href="https://example.com/two"/></entry></feed>`)
	if len(atom) != 1 || atom[0].Title != "Two" || atom[0].URL != "https://example.com/two" || atom[0].Source != "atom" {
		t.Fatalf("unexpected Atom URLs: %#v", atom)
	}
}

func TestExtractImportURLsFromCSVAndTSV(t *testing.T) {
	csv := extractImportURLs("Title,URL,Highlight\nReadwise Item,https://example.com/readwise,quote\nBad,file:///etc/passwd,nope")
	if len(csv) != 1 || csv[0].Title != "Readwise Item" || csv[0].URL != "https://example.com/readwise" {
		t.Fatalf("unexpected CSV URLs: %#v", csv)
	}
	tsv := extractImportURLs("Book Title\tSource URL\tHighlight\nKindle Item\thttps://example.com/kindle\tquote")
	if len(tsv) != 1 || tsv[0].Title != "Kindle Item" || tsv[0].URL != "https://example.com/kindle" {
		t.Fatalf("unexpected TSV URLs: %#v", tsv)
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

func TestMarkdownHelpersEscapeTitlesAndURLs(t *testing.T) {
	if got := markdownText("A [bracketed] title"); got != `A \[bracketed\] title` {
		t.Fatalf("markdownText escaped to %q", got)
	}
	if got := markdownURL("https://example.com/a)b"); got != "https://example.com/a%29b" {
		t.Fatalf("markdownURL escaped to %q", got)
	}
}

func TestRecordImportJobProgressIsUserScoped(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.ExecContext(ctx, `INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "user-1", "one@example.com", "One", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "user-2", "two@example.com", "Two", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "bookmark-1", "user-1", "https://example.com/one", "One", "example.com", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "bookmark-2", "user-2", "https://example.com/two", "Two", "example.com", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO import_jobs(id,user_id,total_bookmarks,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "import-1", "user-1", 2, "processing", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO import_jobs(id,user_id,total_bookmarks,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "import-2", "user-2", 1, "processing", now, now)
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})

	service.recordImportJobProgress(ctx, "bookmark-1", "import-1", nil)
	assertImportCounters(t, db, "import-1", 1, 1, 0, "processing")

	service.recordImportJobProgress(ctx, "bookmark-2", "import-1", nil)
	assertImportCounters(t, db, "import-1", 1, 1, 0, "processing")

	service.recordImportJobProgress(ctx, "bookmark-1", "import-1", errors.New("fetch failed"))
	assertImportCounters(t, db, "import-1", 1, 1, 1, "completed")
	assertImportCounters(t, db, "import-2", 0, 0, 0, "processing")
}

func assertImportCounters(t *testing.T, db *sql.DB, id string, fetched int, processed int, failed int, status string) {
	t.Helper()
	var gotFetched, gotProcessed, gotFailed int
	var gotStatus string
	if err := db.QueryRow(`SELECT content_fetched,ai_processed,failed,status FROM import_jobs WHERE id=?`, id).Scan(&gotFetched, &gotProcessed, &gotFailed, &gotStatus); err != nil {
		t.Fatalf("scan import job %s: %v", id, err)
	}
	if gotFetched != fetched || gotProcessed != processed || gotFailed != failed || gotStatus != status {
		t.Fatalf("import job %s counters = fetched:%d processed:%d failed:%d status:%s, want fetched:%d processed:%d failed:%d status:%s", id, gotFetched, gotProcessed, gotFailed, gotStatus, fetched, processed, failed, status)
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
