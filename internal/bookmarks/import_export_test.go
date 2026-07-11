package bookmarks

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
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

	service.recordImportJobSuccess(ctx, "bookmark-1", "import-1")
	assertImportCounters(t, db, "import-1", 1, 1, 0, "processing")

	service.recordImportJobSuccess(ctx, "bookmark-2", "import-1")
	assertImportCounters(t, db, "import-1", 1, 1, 0, "processing")

	service.recordImportJobFailure(ctx, "bookmark-1", "import-1")
	assertImportCounters(t, db, "import-1", 1, 1, 1, "completed")
	assertImportCounters(t, db, "import-2", 0, 0, 0, "processing")
}

func TestImportJobProgressIgnoresRetryableProcessFailures(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.ExecContext(ctx, `INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "user-1", "one@example.com", "One", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "bookmark-1", "user-1", "https://example.com/private", "Private", "example.com", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "summary-1", "bookmark-1", "user-1", "pending", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO import_jobs(id,user_id,total_bookmarks,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "import-1", "user-1", 1, "processing", now, now)
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	payload := `{"bookmark_id":"bookmark-1","url":"http://127.0.0.1/","import_job_id":"import-1"}`

	if err := service.ProcessJob(ctx, "bookmark.process", payload); err == nil {
		t.Fatal("expected process job to fail on blocked target")
	}
	assertImportCounters(t, db, "import-1", 0, 0, 0, "processing")

	service.RecordJobTerminalFailure(ctx, "bookmark.process", payload)
	assertImportCounters(t, db, "import-1", 0, 0, 1, "completed")
	var status string
	if err := db.QueryRowContext(ctx, `SELECT processing_status FROM ai_summaries WHERE bookmark_id='bookmark-1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("summary status = %q, want failed", status)
	}
	_, _ = db.ExecContext(ctx, `UPDATE ai_summaries SET processing_status='pending' WHERE bookmark_id='bookmark-1'`)
	service.RecordJobTerminalFailure(ctx, "bookmark.process", `{"bookmark_id":"bookmark-1"}`)
	if err := db.QueryRowContext(ctx, `SELECT processing_status FROM ai_summaries WHERE bookmark_id='bookmark-1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("reprocess summary status = %q, want failed", status)
	}
}

func TestPartialExtractionCountsAsImportFailureWithoutRetry(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.ExecContext(ctx, `INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "user-1", "one@example.com", "One", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "bookmark-1", "user-1", "https://example.com/post", "Post", "example.com", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "summary-1", "bookmark-1", "user-1", "partial", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO import_jobs(id,user_id,total_bookmarks,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "import-1", "user-1", 1, "processing", now, now)
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})

	if err := service.finishBookmarkProcess(ctx, "bookmark-1", "import-1", errPartialExtraction); err != nil {
		t.Fatalf("partial extraction should complete without retry: %v", err)
	}
	assertImportCounters(t, db, "import-1", 0, 0, 1, "completed")
}

func TestProcessDirectXUsesAuthoritativeEvidenceWithoutScraping(t *testing.T) {
	service, db := xProcessingTestService(t, "x-direct", "http://127.0.0.1/private", "x_post", "Authoritative API post text.", "complete")
	beforeUpdated := "2026-07-11T08:00:00Z"

	if err := service.processBookmark(t.Context(), "x-direct", "http://127.0.0.1/private"); err != nil {
		t.Fatalf("direct X processing should not fetch its URL: %v", err)
	}
	var textContent, updatedAt, processedAt, summary string
	if err := db.QueryRow(`SELECT b.text_content,b.updated_at,COALESCE(b.processed_at,''),COALESCE(s.one_sentence,'') FROM bookmarks b JOIN ai_summaries s ON s.bookmark_id=b.id WHERE b.id='x-direct'`).Scan(&textContent, &updatedAt, &processedAt, &summary); err != nil {
		t.Fatal(err)
	}
	if textContent != "Authoritative API post text." || summary != "Authoritative API post text." || updatedAt != beforeUpdated || processedAt == "" {
		t.Fatalf("direct X projection = text=%q summary=%q updated=%q processed=%q", textContent, summary, updatedAt, processedAt)
	}
}

func TestProcessXArticleFailureFallsBackWithoutDestroyingSource(t *testing.T) {
	service, db := xProcessingTestService(t, "x-article", "http://127.0.0.1/article", "x_article", "Post context survives article failure.", "complete")
	_, _ = db.Exec(`UPDATE bookmarks SET x_metrics_json=? WHERE id='x-article'`, `{"view_count":999,"like_count":100}`)

	if err := service.processBookmark(t.Context(), "x-article", "http://127.0.0.1/article"); err != nil {
		t.Fatalf("usable X source should absorb linked article failure: %v", err)
	}
	evidence, err := service.Evidence(t.Context(), "user-1", "x-article")
	if err != nil {
		t.Fatal(err)
	}
	var selectedSource, failedArticle bool
	for _, item := range evidence {
		selectedSource = selectedSource || (item.Kind == "source_post" && item.Selected && item.Text == "Post context survives article failure.")
		failedArticle = failedArticle || (item.Kind == "fetched_article" && item.QualityStatus == "failed" && len(item.QualityReasons) > 0)
	}
	if !selectedSource || !failedArticle {
		t.Fatalf("fallback evidence = %#v", evidence)
	}
	var textContent, summary string
	if err := db.QueryRow(`SELECT b.text_content,COALESCE(s.one_sentence,'') FROM bookmarks b JOIN ai_summaries s ON s.bookmark_id=b.id WHERE b.id='x-article'`).Scan(&textContent, &summary); err != nil {
		t.Fatal(err)
	}
	if textContent != "Post context survives article failure." || summary != textContent || strings.Contains(summary, "999") || strings.Contains(summary, "100") {
		t.Fatalf("metrics or failed article leaked into selected evidence: text=%q summary=%q", textContent, summary)
	}
}

func TestProcessMetadataOnlyXDoesNotGenerateClaims(t *testing.T) {
	service, db := xProcessingTestService(t, "x-media", "https://x.com/author/status/3", "x_media", "https://t.co/media", "metadata_only")
	if err := service.processBookmark(t.Context(), "x-media", "https://x.com/author/status/3"); err != nil {
		t.Fatal(err)
	}
	var status, summary string
	if err := db.QueryRow(`SELECT processing_status,COALESCE(one_sentence,'') FROM ai_summaries WHERE bookmark_id='x-media'`).Scan(&status, &summary); err != nil {
		t.Fatal(err)
	}
	if status != "insufficient_evidence" || summary != "" {
		t.Fatalf("metadata-only summary = status=%q summary=%q", status, summary)
	}
}

func xProcessingTestService(t *testing.T, bookmarkID, rawURL, contentKind, sourceText, qualityStatus string) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := "2026-07-11T08:00:00Z"
	_, _ = db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "user-1", "one@example.com", "One", now, now)
	_, err = db.Exec(`INSERT INTO bookmarks(id,user_id,url,title,description,domain,text_content,source,x_tweet_url,canonical_url,content_kind,source_published_at,source_author_id,source_publisher_key,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, bookmarkID, "user-1", rawURL, "Original title", sourceText, "x.com", sourceText, "x", "https://x.com/author/status/1", rawURL, contentKind, "2026-07-10T04:00:00Z", "author-1", "x:author-1", now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "summary-"+bookmarkID, bookmarkID, "user-1", "pending", now, now)
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	reasons := []string{}
	if qualityStatus != "complete" {
		reasons = []string{"media_without_transcript"}
	}
	if _, err := service.UpsertEvidence(t.Context(), "user-1", bookmarkID, BookmarkEvidence{Kind: "source_post", Origin: "x_api", Authority: 100, Text: sourceText, CanonicalURL: "https://x.com/author/status/1", AuthorID: "author-1", PublisherKey: "x:author-1", PublishedAt: "2026-07-10T04:00:00Z", ExtractionMethod: "x_api", QualityStatus: qualityStatus, QualityReasons: reasons, ExtractorVersion: "x-api-v1", Selected: contentKind != "x_article"}); err != nil {
		t.Fatal(err)
	}
	return service, db
}

func TestReprocessQueuesExistingBookmarkWithoutDeletingManualData(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.ExecContext(ctx, `INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "user-1", "one@example.com", "One", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,description,domain,sanitized_html,text_content,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "bookmark-1", "user-1", "https://example.com/post", "Manual title", "Old description", "example.com", "<p>Old content</p>", "Old content", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "summary-1", "bookmark-1", "user-1", "Old summary", "completed", now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO annotations(id,user_id,bookmark_id,quote,note,selector_json,tags_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, "annotation-1", "user-1", "bookmark-1", "Keep this quote", "Keep this note", "{}", "[]", now, now)
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	req := httptest.NewRequest(http.MethodPost, "/api/bookmarks/bookmark-1/reprocess", nil)
	req.SetPathValue("id", "bookmark-1")
	rec := httptest.NewRecorder()

	service.Reprocess(rec, req, auth.User{ID: "user-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response["job_id"] == "" {
		t.Fatalf("missing reprocess job: %s", rec.Body.String())
	}
	var status, summary string
	if err := db.QueryRowContext(ctx, `SELECT processing_status,COALESCE(one_sentence,'') FROM ai_summaries WHERE bookmark_id=?`, "bookmark-1").Scan(&status, &summary); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || summary != "Old summary" {
		t.Fatalf("summary state = %q/%q, want pending with previous summary preserved", status, summary)
	}
	var annotations int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM annotations WHERE bookmark_id=?`, "bookmark-1").Scan(&annotations)
	if annotations != 1 {
		t.Fatalf("annotations = %d, want preserved", annotations)
	}
	rec = httptest.NewRecorder()
	service.Reprocess(rec, req, auth.User{ID: "user-1"})
	var queuedJobs int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE user_id=? AND type='bookmark.process'`, "user-1").Scan(&queuedJobs)
	if queuedJobs != 1 {
		t.Fatalf("queued jobs = %d, want repeated reprocess requests deduplicated", queuedJobs)
	}
}

func TestFullExportRestoreRoundTripsEvidenceAndProvenance(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := "2026-07-11T08:00:00Z"
	for _, user := range []struct{ id, email string }{{"user-1", "one@example.com"}, {"user-2", "two@example.com"}, {"user-3", "three@example.com"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, user.id, user.email, user.id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,domain,source,canonical_url,content_kind,source_published_at,source_author_id,source_publisher_key,processed_at,fetch_version,summary_version,enrichment_version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"bookmark-1", "user-1", "https://x.com/author/status/1", "Post", "x.com", "x", "https://example.com/article", "x_post_with_article", "2026-07-10T04:00:00Z", "author-1", "x:author", "2026-07-11T07:00:00Z", "x-api-v2", "summary-v2", "semantic-v2", "2026-07-11T06:00:00Z", now)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "bookmark-2", "user-2", "https://example.org/private", "Private", "example.org", now, now)
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	post, err := service.UpsertEvidence(ctx, "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "source_post", Origin: "source_provided", Authority: 100, Text: "Authoritative post text.", CanonicalURL: "https://x.com/author/status/1", AuthorID: "author-1", PublisherKey: "x:author", PublishedAt: "2026-07-10T04:00:00Z", ExtractionMethod: "x_api", ContentHash: "post-hash", QualityStatus: "complete", QualityReasons: []string{}, ExtractorVersion: "x-api-v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	article, err := service.UpsertEvidence(ctx, "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "linked_article", Origin: "fetched", Authority: 80, Text: "Complete linked article text.", SanitizedHTML: "<p>Complete linked article text.</p>", CanonicalURL: "https://example.com/article", PublisherKey: "example.com", PublishedAt: "2026-07-09T03:00:00Z", ExtractionMethod: "readability", ContentHash: "article-hash", QualityStatus: "complete", ExtractorVersion: "readability-v1", Selected: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if post.ID == "" || article.ID == "" {
		t.Fatal("evidence IDs were not assigned")
	}
	if _, err := service.UpsertEvidence(ctx, "user-2", "bookmark-1", BookmarkEvidence{Kind: "source_post", Text: "cross-user"}); err == nil {
		t.Fatal("cross-user evidence write unexpectedly succeeded")
	}

	exported, err := service.fullExport(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if exported["version"] != 2 {
		t.Fatalf("export version = %#v, want 2", exported["version"])
	}
	bookmarks := mapList(exported["bookmarks"])
	if len(bookmarks) != 1 {
		t.Fatalf("exported bookmarks = %#v", bookmarks)
	}
	bookmark := bookmarks[0]
	if bookmark["captured_at"] != "2026-07-11T06:00:00Z" || bookmark["source_published_at"] != "2026-07-10T04:00:00Z" || bookmark["processed_at"] != "2026-07-11T07:00:00Z" || bookmark["updated_at"] != now {
		t.Fatalf("timestamps not distinguished: %#v", bookmark)
	}
	evidence := mapList(bookmark["evidence"])
	if len(evidence) != 2 || evidence[0]["user_id"] != nil || evidence[1]["user_id"] != nil {
		t.Fatalf("evidence export malformed or leaked owner IDs: %#v", evidence)
	}

	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.restoreFullExport(ctx, "user-3", raw); err != nil || !ok {
		t.Fatalf("restoreFullExport() ok=%v err=%v", ok, err)
	}
	var restoredID, canonicalURL, contentKind, publishedAt, processedAt, fetchVersion, summaryVersion, enrichmentVersion, capturedAt, updatedAt string
	if err := db.QueryRowContext(ctx, `SELECT id,canonical_url,content_kind,source_published_at,processed_at,fetch_version,summary_version,enrichment_version,created_at,updated_at FROM bookmarks WHERE user_id=?`, "user-3").Scan(&restoredID, &canonicalURL, &contentKind, &publishedAt, &processedAt, &fetchVersion, &summaryVersion, &enrichmentVersion, &capturedAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if canonicalURL != "https://example.com/article" || contentKind != "x_post_with_article" || publishedAt != "2026-07-10T04:00:00Z" || processedAt != "2026-07-11T07:00:00Z" || fetchVersion != "x-api-v2" || summaryVersion != "summary-v2" || enrichmentVersion != "semantic-v2" || capturedAt != "2026-07-11T06:00:00Z" || updatedAt != now {
		t.Fatalf("restored provenance mismatch: canonical=%q kind=%q published=%q processed=%q versions=%q/%q/%q captured=%q updated=%q", canonicalURL, contentKind, publishedAt, processedAt, fetchVersion, summaryVersion, enrichmentVersion, capturedAt, updatedAt)
	}
	restoredEvidence, err := service.Evidence(ctx, "user-3", restoredID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredEvidence) != 2 || restoredEvidence[0].BookmarkID != restoredID || restoredEvidence[1].BookmarkID != restoredID {
		t.Fatalf("restored evidence = %#v", restoredEvidence)
	}
	if other, err := service.Evidence(ctx, "user-2", restoredID); err != nil || len(other) != 0 {
		t.Fatalf("cross-user evidence read = %#v, err=%v", other, err)
	}
}

func TestRestoreV1FullExportDefaultsProvenance(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.ExecContext(ctx, `INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "user-1", "one@example.com", "One", now, now)
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	raw := []byte(`{"version":1,"bookmarks":[{"id":"old-bookmark","url":"https://example.com/old","title":"Old","source":"browser","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-02T00:00:00Z"}]}`)
	if _, ok, err := service.restoreFullExport(ctx, "user-1", raw); err != nil || !ok {
		t.Fatalf("restore v1 ok=%v err=%v", ok, err)
	}
	var canonicalURL, contentKind, capturedAt, updatedAt string
	if err := db.QueryRowContext(ctx, `SELECT canonical_url,content_kind,created_at,updated_at FROM bookmarks WHERE user_id='user-1'`).Scan(&canonicalURL, &contentKind, &capturedAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if canonicalURL != "https://example.com/old" || contentKind != "web" || capturedAt != "2025-01-01T00:00:00Z" || updatedAt != "2025-01-02T00:00:00Z" {
		t.Fatalf("v1 defaults = canonical:%q kind:%q captured:%q updated:%q", canonicalURL, contentKind, capturedAt, updatedAt)
	}
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
