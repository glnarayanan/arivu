package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/assets"
	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

func TestReadingTimeUsesStandardAdultReadingSpeedAndRoundsUp(t *testing.T) {
	text := strings.TrimSpace(strings.Repeat("word ", 9639))
	if got := readingTime(text); got != 41 {
		t.Fatalf("readingTime() = %d, want 41", got)
	}
}

func TestDirectCaptureActivatesReaderWithoutAI(t *testing.T) {
	service, db := capturePipelineService(t)
	service.fetchPage = func(context.Context, string) (safefetch.Result, error) {
		return completeDirectResult("New rendered-independent evidence", 88), nil
	}

	if err := service.processWebBookmarkAttempt(t.Context(), "user-1", "bookmark-1", "https://example.com/article", "attempt-1", "Example", "", "example.com"); err != nil {
		t.Fatal(err)
	}
	var text, selected, summaryStatus string
	if err := db.QueryRow(`SELECT b.text_content,e.id,s.processing_status FROM bookmarks b JOIN bookmark_evidence e ON e.bookmark_id=b.id AND e.is_selected=1 JOIN ai_summaries s ON s.bookmark_id=b.id WHERE b.id='bookmark-1'`).Scan(&text, &selected, &summaryStatus); err != nil {
		t.Fatal(err)
	}
	if text != "New rendered-independent evidence" || selected == "" || summaryStatus != "fallback" {
		t.Fatalf("text=%q selected=%q summary=%q", text, selected, summaryStatus)
	}
	_, _ = db.Exec(`UPDATE capture_attempts SET status='complete' WHERE id='attempt-1'`)
	if status := service.captureStatus(t.Context(), "user-1", "bookmark-1"); status != "saved" {
		t.Fatalf("text-only direct capture status=%q, want saved", status)
	}
}

func TestDirectCaptureLocalizesReaderImagesWithoutBrowserHelper(t *testing.T) {
	service, db := capturePipelineService(t)
	store, err := assets.New(filepath.Join(t.TempDir(), "capture.sqlite3"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	service.SetAssetStore(store)
	service.fetchPage = func(context.Context, string) (safefetch.Result, error) {
		result := completeDirectResult("Direct evidence with an image", 88)
		result.ReaderHTML = `<article><p>Direct evidence with an image</p><figure><img src="https://cdn.example.com/guide.webp" alt="Guide screenshot"><figcaption>Guide</figcaption></figure></article>`
		return result, nil
	}
	service.fetchMedia = func(context.Context, string, int64) (safefetch.MediaResult, error) {
		return safefetch.MediaResult{URL: "https://cdn.example.com/guide.webp", Body: []byte("webp"), ContentType: "image/webp"}, nil
	}

	if err := service.processWebBookmarkAttempt(t.Context(), "user-1", "bookmark-1", "https://example.com/article", "attempt-1", "Example", "", "example.com"); err != nil {
		t.Fatal(err)
	}
	var readerHTML, thumbnail string
	if err := db.QueryRow(`SELECT sanitized_html,COALESCE(thumbnail,'') FROM bookmarks WHERE id='bookmark-1'`).Scan(&readerHTML, &thumbnail); err != nil {
		t.Fatal(err)
	}
	var mediaCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmark_media WHERE bookmark_id='bookmark-1' AND is_staged=0 AND deleted_at IS NULL`).Scan(&mediaCount); err != nil {
		t.Fatal(err)
	}
	if mediaCount != 1 || !strings.Contains(readerHTML, `<img src="/api/media/`) || strings.Contains(readerHTML, "cdn.example.com") || !strings.HasPrefix(thumbnail, "/api/media/") {
		t.Fatalf("media_count=%d html=%q thumbnail=%q", mediaCount, readerHTML, thumbnail)
	}
	_, _ = db.Exec(`UPDATE capture_attempts SET status='complete' WHERE id='attempt-1'`)
	if status := service.captureStatus(t.Context(), "user-1", "bookmark-1"); status != "preserved" {
		t.Fatalf("localized direct capture status=%q, want preserved", status)
	}
}

func TestUnchangedCompleteCaptureIsSuccessful(t *testing.T) {
	service, db := capturePipelineService(t)
	service.fetchPage = func(context.Context, string) (safefetch.Result, error) {
		return completeDirectResult("Stable complete evidence", 88), nil
	}
	for run := 0; run < 2; run++ {
		if err := service.processWebBookmarkAttempt(t.Context(), "user-1", "bookmark-1", "https://example.com/article", "attempt-1", "Example", "", "example.com"); err != nil {
			t.Fatalf("run %d: %v", run+1, err)
		}
	}
	var selectedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmark_evidence WHERE bookmark_id='bookmark-1' AND is_selected=1`).Scan(&selectedCount); err != nil {
		t.Fatal(err)
	}
	if selectedCount != 1 {
		t.Fatalf("selected evidence rows=%d", selectedCount)
	}
}

func TestStaleCaptureCannotReplaceNewerSelectedEvidence(t *testing.T) {
	service, db := capturePipelineService(t)
	current, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "fetched_article", Origin: "web_fetch", Text: "Current reader evidence", QualityStatus: "complete", QualityScore: 60, ExtractorVersion: "current", Selected: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`UPDATE bookmarks SET text_content=? WHERE id='bookmark-1'`, current.Text)
	now := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	_, _ = db.Exec(`INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,engine,queued_at) VALUES('attempt-2','bookmark-1','user-1','queued','https://example.com/article','direct_http',?)`, now)
	service.fetchPage = func(context.Context, string) (safefetch.Result, error) {
		return completeDirectResult("Stale higher-scored evidence", 99), nil
	}

	if err := service.processWebBookmarkAttempt(t.Context(), "user-1", "bookmark-1", "https://example.com/article", "attempt-1", "Example", "", "example.com"); err != nil {
		t.Fatal(err)
	}
	var text, selected string
	if err := db.QueryRow(`SELECT b.text_content,e.id FROM bookmarks b JOIN bookmark_evidence e ON e.bookmark_id=b.id AND e.is_selected=1 WHERE b.id='bookmark-1'`).Scan(&text, &selected); err != nil {
		t.Fatal(err)
	}
	if text != current.Text || selected != current.ID {
		t.Fatalf("stale capture selected text=%q evidence=%q", text, selected)
	}
}

func TestCaptureFailurePreservesLastGoodReader(t *testing.T) {
	service, db := capturePipelineService(t)
	current, _ := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "fetched_article", Origin: "web_fetch", Text: "Last good reader", QualityStatus: "complete", QualityScore: 80, ExtractorVersion: "current", Selected: true,
	})
	_, _ = db.Exec(`UPDATE bookmarks SET text_content=? WHERE id='bookmark-1'`, current.Text)
	service.fetchPage = func(context.Context, string) (safefetch.Result, error) {
		return safefetch.Result{}, errors.New("network failed")
	}

	if err := service.processWebBookmarkAttempt(t.Context(), "user-1", "bookmark-1", "https://example.com/article", "attempt-1", "Example", "", "example.com"); !errors.Is(err, errPartialExtraction) {
		t.Fatalf("error=%v", err)
	}
	var text, selected string
	if err := db.QueryRow(`SELECT b.text_content,e.id FROM bookmarks b JOIN bookmark_evidence e ON e.bookmark_id=b.id AND e.is_selected=1 WHERE b.id='bookmark-1'`).Scan(&text, &selected); err != nil {
		t.Fatal(err)
	}
	if text != current.Text || selected != current.ID {
		t.Fatalf("last good reader changed text=%q evidence=%q", text, selected)
	}
}

func TestLateAIResultCannotOverwriteNewerReaderSelection(t *testing.T) {
	service, db := capturePipelineService(t)
	started := make(chan struct{})
	release := make(chan struct{})
	service.SetAIProvider(func(context.Context) providers.GeminiClient {
		return providers.GeminiClient{APIKey: "test", BaseURL: "https://gemini.test", HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.Path, ":embedContent") {
				return jsonResponse(http.StatusOK, map[string]any{"embedding": map[string]any{"values": []float64{0.1, 0.2}}}), nil
			}
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
			return jsonResponse(http.StatusOK, map[string]any{"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{{"text": `{"one_sentence":"Old generated summary.","long_form":"","bullet_points":[],"highlights":[],"suggested_tags":[],"entities":[],"concepts":[]}`}}}}}}), nil
		})}}
	})
	oldEvidence, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "fetched_article", Origin: "web_fetch", Text: "Older complete evidence", QualityStatus: "complete", QualityScore: 70, ExtractorVersion: "old",
	})
	if err != nil {
		t.Fatal(err)
	}
	newEvidence, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "rendered_article", Origin: "browser_render", Text: "Newer complete evidence", QualityStatus: "complete", QualityScore: 90, ExtractorVersion: "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- service.persistSelectedEvidence(t.Context(), "user-1", "bookmark-1", "Example", "", "example.com", oldEvidence)
	}()
	<-started
	if activated, err := service.activateSelectedEvidence(t.Context(), "user-1", "bookmark-1", "Example", "", "example.com", newEvidence, ""); err != nil || !activated {
		t.Fatalf("activate newer evidence: activated=%v err=%v", activated, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var selectedID, text, summary string
	if err := db.QueryRow(`SELECT e.id,b.text_content,COALESCE(s.one_sentence,'') FROM bookmarks b JOIN bookmark_evidence e ON e.bookmark_id=b.id AND e.is_selected=1 JOIN ai_summaries s ON s.bookmark_id=b.id WHERE b.id='bookmark-1'`).Scan(&selectedID, &text, &summary); err != nil {
		t.Fatal(err)
	}
	if selectedID != newEvidence.ID || text != newEvidence.Text || summary != "" {
		t.Fatalf("late AI result changed newer reader: selected=%q text=%q summary=%q", selectedID, text, summary)
	}
}

func TestLateAIResultCannotOverwriteNewerAttemptForSameEvidence(t *testing.T) {
	service, db := capturePipelineService(t)
	started := make(chan struct{})
	release := make(chan struct{})
	service.SetAIProvider(func(context.Context) providers.GeminiClient {
		return providers.GeminiClient{APIKey: "test", BaseURL: "https://gemini.test", HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
			return jsonResponse(http.StatusOK, map[string]any{"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{{"text": `{"one_sentence":"Late old summary.","long_form":"","bullet_points":[],"highlights":[],"suggested_tags":[],"entities":[],"concepts":[]}`}}}}}}), nil
		})}}
	})
	evidence, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "fetched_article", Origin: "web_fetch", Text: "Identical evidence", QualityStatus: "complete", QualityScore: 90, ExtractorVersion: "same",
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- service.persistSelectedEvidenceForAttempt(t.Context(), "user-1", "bookmark-1", "Example", "", "example.com", evidence, "attempt-1")
	}()
	<-started
	newer := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,engine,queued_at) VALUES('attempt-2','bookmark-1','user-1','running','https://example.com/article','direct_http',?)`, newer); err != nil {
		t.Fatal(err)
	}
	if activated, err := service.activateSelectedEvidence(t.Context(), "user-1", "bookmark-1", "Example", "", "example.com", evidence, "attempt-2"); err != nil || !activated {
		t.Fatalf("activate newer attempt: activated=%v err=%v", activated, err)
	}
	if _, err := db.Exec(`UPDATE ai_summaries SET one_sentence='Newer summary',processing_status='completed',evidence_hash=? WHERE bookmark_id='bookmark-1'`, evidence.ContentHash); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var summary string
	if err := db.QueryRow(`SELECT one_sentence FROM ai_summaries WHERE bookmark_id='bookmark-1'`).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if summary != "Newer summary" {
		t.Fatalf("late attempt overwrote summary with %q", summary)
	}
}

func TestRenderedCaptureOutranksDirectAndPersistsBothEvidenceRows(t *testing.T) {
	service, db := capturePipelineService(t)
	store, err := assets.New(filepath.Join(t.TempDir(), "capture.sqlite3"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	service.SetAssetStore(store)
	runtimeDir, err := os.MkdirTemp("/tmp", "arivu-bookmark-capture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	socket := filepath.Join(runtimeDir, "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("unix sockets are unavailable in this sandbox")
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	helperDone := make(chan error, 1)
	go func() { helperDone <- serveRenderedCaptureFixture(listener, nil, nil) }()
	service.SetBrowserCapture(config.BrowserCaptureConfig{
		Enabled: true, Protocol: 2, Socket: socket, RuntimeDir: runtimeDir,
		Timeout: 2 * time.Second, NavigationTimeout: time.Second,
		MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxMediaFiles: 1, MaxMediaFileBytes: 1024, MaxMediaTotalBytes: 1024,
	})
	service.fetchPage = func(context.Context, string) (safefetch.Result, error) {
		result := completeDirectResult("Direct evidence with the higher numeric score", 99)
		result.Description = "Publisher-authored description."
		return result, nil
	}

	if err := service.processWebBookmarkAttempt(t.Context(), "user-1", "bookmark-1", "https://example.com/article", "attempt-1", "Example", "", "example.com"); err != nil {
		t.Fatal(err)
	}
	if err := <-helperDone; err != nil {
		t.Fatal(err)
	}
	var origin, text, description, readerHTML, thumbnail string
	if err := db.QueryRow(`SELECT e.evidence_origin,b.text_content,b.description,b.sanitized_html,COALESCE(b.thumbnail,'') FROM bookmark_evidence e JOIN bookmarks b ON b.id=e.bookmark_id WHERE e.bookmark_id='bookmark-1' AND e.is_selected=1`).Scan(&origin, &text, &description, &readerHTML, &thumbnail); err != nil {
		t.Fatal(err)
	}
	var evidenceCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmark_evidence WHERE bookmark_id='bookmark-1' AND evidence_origin IN ('web_fetch','browser_render')`).Scan(&evidenceCount); err != nil {
		t.Fatal(err)
	}
	var mediaCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmark_media WHERE bookmark_id='bookmark-1' AND deleted_at IS NULL`).Scan(&mediaCount); err != nil {
		t.Fatal(err)
	}
	if origin != "browser_render" || text != "Rendered evidence" || description != "Publisher-authored description." || evidenceCount != 2 || mediaCount != 1 || !strings.Contains(readerHTML, `<img src="/api/media/`) || strings.Contains(readerHTML, "https://example.com/image.png") || !strings.HasPrefix(thumbnail, "/api/media/") {
		t.Fatalf("origin=%q text=%q description=%q evidence_count=%d media_count=%d html=%q thumbnail=%q", origin, text, description, evidenceCount, mediaCount, readerHTML, thumbnail)
	}
}

func TestDirectReaderActivatesBeforeRenderedCaptureFinishes(t *testing.T) {
	service, db := capturePipelineService(t)
	runtimeDir, err := os.MkdirTemp("/tmp", "arivu-bookmark-capture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	listener, err := net.Listen("unix", filepath.Join(runtimeDir, "helper.sock"))
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("unix sockets are unavailable in this sandbox")
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	accepted := make(chan struct{})
	release := make(chan struct{})
	helperDone := make(chan error, 1)
	go func() { helperDone <- serveRenderedCaptureFixture(listener, accepted, release) }()
	service.SetBrowserCapture(config.BrowserCaptureConfig{
		Enabled: true, Protocol: 2, Socket: listener.Addr().String(), RuntimeDir: runtimeDir,
		Timeout: 2 * time.Second, NavigationTimeout: time.Second,
		MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxMediaFiles: 1, MaxMediaFileBytes: 1024, MaxMediaTotalBytes: 1024,
	})
	service.fetchPage = func(context.Context, string) (safefetch.Result, error) {
		return completeDirectResult("Direct evidence available now", 90), nil
	}
	processDone := make(chan error, 1)
	go func() {
		processDone <- service.processWebBookmarkAttempt(t.Context(), "user-1", "bookmark-1", "https://example.com/article", "attempt-1", "Example", "", "example.com")
	}()
	<-accepted
	deadline := time.Now().Add(time.Second)
	for {
		var text string
		_ = db.QueryRow(`SELECT text_content FROM bookmarks WHERE id='bookmark-1'`).Scan(&text)
		if text == "Direct evidence available now" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("direct evidence was not activated while rendered capture was pending")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if err := <-helperDone; err != nil {
		t.Fatal(err)
	}
	if err := <-processDone; err != nil && !errors.Is(err, errPartialExtraction) {
		t.Fatal(err)
	}
}

func TestMediaStorageFailureKeepsRenderedReaderEvidence(t *testing.T) {
	service, db := capturePipelineService(t)
	store, err := assets.New(filepath.Join(t.TempDir(), "capture.sqlite3"), 2)
	if err != nil {
		t.Fatal(err)
	}
	service.SetAssetStore(store)
	runtimeDir, err := os.MkdirTemp("/tmp", "arivu-bookmark-capture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	listener, err := net.Listen("unix", filepath.Join(runtimeDir, "helper.sock"))
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("unix sockets are unavailable in this sandbox")
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	helperDone := make(chan error, 1)
	go func() { helperDone <- serveRenderedCaptureFixture(listener, nil, nil) }()
	service.SetBrowserCapture(config.BrowserCaptureConfig{
		Enabled: true, Protocol: 2, Socket: listener.Addr().String(), RuntimeDir: runtimeDir,
		Timeout: 2 * time.Second, NavigationTimeout: time.Second,
		MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxMediaFiles: 1, MaxMediaFileBytes: 1024, MaxMediaTotalBytes: 1024,
	})
	service.fetchPage = func(context.Context, string) (safefetch.Result, error) {
		return completeDirectResult("Direct evidence", 99), nil
	}
	err = service.processWebBookmarkAttempt(t.Context(), "user-1", "bookmark-1", "https://example.com/article", "attempt-1", "Example", "", "example.com")
	if !errors.Is(err, errPartialExtraction) {
		t.Fatalf("capture error=%v", err)
	}
	if err := <-helperDone; err != nil {
		t.Fatal(err)
	}
	var origin, text string
	if err := db.QueryRow(`SELECT e.evidence_origin,b.text_content FROM bookmark_evidence e JOIN bookmarks b ON b.id=e.bookmark_id WHERE e.bookmark_id='bookmark-1' AND e.is_selected=1`).Scan(&origin, &text); err != nil {
		t.Fatal(err)
	}
	if origin != "browser_render" || text != "Rendered evidence" {
		t.Fatalf("origin=%q text=%q", origin, text)
	}
}

func TestRejectedRenderedCandidateReleasesStagedBatch(t *testing.T) {
	service, db := capturePipelineService(t)
	store, err := assets.New(filepath.Join(t.TempDir(), "capture.sqlite3"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	service.SetAssetStore(store)
	runtimeDir, err := os.MkdirTemp("/tmp", "arivu-bookmark-capture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	listener, err := net.Listen("unix", filepath.Join(runtimeDir, "helper.sock"))
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("unix sockets are unavailable in this sandbox")
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	helperDone := make(chan error, 1)
	go func() { helperDone <- serveRenderedCaptureFixtureWithChallenge(listener) }()
	service.SetBrowserCapture(config.BrowserCaptureConfig{
		Enabled: true, Protocol: 2, Socket: listener.Addr().String(), RuntimeDir: runtimeDir,
		Timeout: 2 * time.Second, NavigationTimeout: time.Second,
		MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxMediaFiles: 1, MaxMediaFileBytes: 1024, MaxMediaTotalBytes: 1024,
	})
	service.fetchPage = func(context.Context, string) (safefetch.Result, error) {
		return safefetch.Result{}, errors.New("direct fetch failed")
	}
	if err := service.processWebBookmarkAttempt(t.Context(), "user-1", "bookmark-1", "https://example.com/article", "attempt-1", "Example", "", "example.com"); err == nil {
		t.Fatal("rejected direct and rendered candidates unexpectedly succeeded")
	}
	if err := <-helperDone; err != nil {
		t.Fatal(err)
	}
	var staged int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmark_media WHERE is_staged=1 AND deleted_at IS NULL`).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if staged != 0 {
		t.Fatalf("orphaned staged media=%d", staged)
	}
}

func serveRenderedCaptureFixture(listener net.Listener, accepted chan<- struct{}, release <-chan struct{}) error {
	return serveRenderedCaptureFixtureResponse(listener, accepted, release, false)
}

func serveRenderedCaptureFixtureWithChallenge(listener net.Listener) error {
	return serveRenderedCaptureFixtureResponse(listener, nil, nil, true)
}

func serveRenderedCaptureFixtureResponse(listener net.Listener, accepted chan<- struct{}, release <-chan struct{}, challenge bool) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	var request struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		return err
	}
	if accepted != nil {
		close(accepted)
	}
	if release != nil {
		<-release
	}
	htmlBody := `<article><p>Rendered evidence</p><figure><img src="https://example.com/image.png" alt="Example image"></figure></article>`
	textBody := "Rendered evidence"
	mediaBody := "png"
	reasons := []string{}
	if challenge {
		reasons = []string{"challenge_detected"}
	}
	response := map[string]any{
		"version": 2, "token": request.Token, "engine_version": "fixture-v2",
		"metadata": map[string]any{"final_url": request.URL, "canonical_url": request.URL, "title": "Rendered title", "description": "Rendered description"},
		"content": map[string]any{
			"html": map[string]any{"mime": "text/html", "size": len(htmlBody)}, "text": map[string]any{"mime": "text/plain", "size": len(textBody)},
			"quality_status": "complete", "quality_score": 80, "quality_reasons": reasons, "challenge": challenge,
		},
		"artifacts": []any{}, "media": []any{map[string]any{"source_url": "https://example.com/image.png", "role": "reader_image", "width": 800, "height": 600, "mime": "image/png", "size": len(mediaBody)}},
		"components": map[string]any{"browser": map[string]any{"status": "complete", "error_code": ""}, "readability": map[string]any{"status": "complete", "error_code": ""}},
		"error_code": "",
	}
	header, err := json.Marshal(response)
	if err == nil {
		_, err = conn.Write(append(header, '\n'))
	}
	if err == nil {
		_, err = io.WriteString(conn, htmlBody+textBody+mediaBody)
	}
	return err
}

func capturePipelineService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('user-1','one@example.com','One','` + now + `','` + now + `')`,
		`INSERT INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES('bookmark-1','user-1','https://example.com/article','Example','example.com','` + now + `','` + now + `')`,
		`INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES('summary-1','bookmark-1','user-1','pending','` + now + `','` + now + `')`,
		`INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,engine,queued_at) VALUES('attempt-1','bookmark-1','user-1','running','https://example.com/article','direct_http','` + now + `')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{}), db
}

func completeDirectResult(text string, score int) safefetch.Result {
	return safefetch.Result{
		URL: "https://example.com/article", Title: "Example", Description: "Description", Domain: "example.com",
		HTML: "<article><p>" + text + "</p></article>", Text: text,
		Quality: safefetch.Assessment{Status: safefetch.QualityComplete, Score: score, Method: "article", Version: safefetch.ExtractorVersion},
		Body:    []byte("<html></html>"), ContentType: "text/html",
	}
}
