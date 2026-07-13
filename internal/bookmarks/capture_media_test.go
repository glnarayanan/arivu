package bookmarks

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/assets"
	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/browsercapture"
)

func TestRewriteReaderMediaRehostsOnlyCapturedImages(t *testing.T) {
	input := `<article><img src="/images/kept.jpg" alt="Kept"><img src="https://cdn.example/missing.jpg"></article>`
	media := []storedCaptureMedia{{id: "media-1", sourceURL: "https://example.com/images/kept.jpg"}}
	got := rewriteReaderMedia(input, "https://example.com/article", media)
	if !strings.Contains(got, `src="/api/media/media-1"`) || strings.Contains(got, `src="https://cdn.example/missing.jpg"`) {
		t.Fatalf("rewritten HTML = %q", got)
	}
}

func TestMediaAndArtifactsShareOneQuota(t *testing.T) {
	service, _ := capturePipelineService(t)
	service.SetArtifactQuota(6)
	media := browsercapture.V2Media{SourceURL: "https://example.com/image.png", Role: "reader_image", V2File: browsercapture.V2File{MIME: "image/png", Size: 5}}
	if err := service.commitMedia(t.Context(), "media-1", "user-1", "bookmark-1", "attempt-1", "batch-1", 0, media, 5, "digest", "aa/digest"); err != nil {
		t.Fatal(err)
	}
	if err := service.commitArtifact(t.Context(), "artifact-1", "user-1", "bookmark-1", "attempt-1", "", "screenshot", "image/jpeg", 2, "other", "bb/other"); !errors.Is(err, ErrArtifactQuota) {
		t.Fatalf("artifact quota error=%v", err)
	}
}

func TestMediaReplacementUsesDocumentOrderAndExistingQuotaHeadroom(t *testing.T) {
	service, db := capturePipelineService(t)
	evidence, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "rendered_article", Origin: "browser_render", Text: "Current rendered evidence", SanitizedHTML: `<img src="/api/media/old-media">`, QualityStatus: "complete", ExtractorVersion: "v2", Selected: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bookmark_media(id,user_id,bookmark_id,capture_attempt_id,evidence_id,source_url,media_role,mime_type,byte_size,sha256,storage_key,ordinal,created_at) VALUES('old-media','user-1','bookmark-1','attempt-1',?,'https://example.com/old.png','reader_image','image/png',5,'old','aa/old',0,?)`, evidence.ID, nowString()); err != nil {
		t.Fatal(err)
	}
	service.SetArtifactQuota(6)
	media := browsercapture.V2Media{SourceURL: "https://example.com/new.png", Role: "reader_image", V2File: browsercapture.V2File{MIME: "image/png", Size: 5}}
	if err := service.commitMedia(t.Context(), "new-second", "user-1", "bookmark-1", "attempt-1", "replacement-batch", 1, media, 5, "second", "bb/second"); err != nil {
		t.Fatalf("replacement should reuse old-media quota headroom: %v", err)
	}
	if err := service.commitMedia(t.Context(), "new-first", "user-1", "bookmark-1", "attempt-1", "replacement-batch", 0, media, 1, "first", "cc/first"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE bookmark_media SET evidence_id=? WHERE id IN ('new-first','new-second')`, evidence.ID); err != nil {
		t.Fatal(err)
	}
	if activated, err := service.activateSelectedEvidenceWithMedia(t.Context(), "user-1", "bookmark-1", "Example", "", "example.com", evidence, "", "replacement-batch"); err != nil || !activated {
		t.Fatalf("activate media replacement: activated=%v err=%v", activated, err)
	}
	var thumbnail string
	if err := db.QueryRow(`SELECT thumbnail FROM bookmarks WHERE id='bookmark-1'`).Scan(&thumbnail); err != nil {
		t.Fatal(err)
	}
	if thumbnail != "/api/media/new-first" {
		t.Fatalf("thumbnail=%q", thumbnail)
	}
	var activeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmark_media WHERE bookmark_id='bookmark-1' AND is_staged=0 AND deleted_at IS NULL`).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 2 {
		t.Fatalf("active replacement media=%d, want 2", activeCount)
	}
}

func TestStagedMediaReservationsPreventConcurrentQuotaOvercommit(t *testing.T) {
	service, db := capturePipelineService(t)
	evidence, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "rendered_article", Origin: "browser_render", Text: "Current reader", QualityStatus: "complete", ExtractorVersion: "v2", Selected: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bookmark_media(id,user_id,bookmark_id,capture_attempt_id,evidence_id,source_url,media_role,mime_type,byte_size,sha256,storage_key,created_at) VALUES('old-media','user-1','bookmark-1','attempt-1',?,'https://example.com/old.png','reader_image','image/png',5,'old','aa/old',?)`, evidence.ID, nowString()); err != nil {
		t.Fatal(err)
	}
	service.SetArtifactQuota(6)
	media := browsercapture.V2Media{SourceURL: "https://example.com/new.png", Role: "reader_image", V2File: browsercapture.V2File{MIME: "image/png", Size: 5}}
	if err := service.commitMedia(t.Context(), "first-stage", "user-1", "bookmark-1", "attempt-1", "batch-1", 0, media, 5, "first", "bb/first"); err != nil {
		t.Fatal(err)
	}
	if err := service.commitMedia(t.Context(), "second-stage", "user-1", "bookmark-1", "attempt-1", "batch-2", 0, media, 5, "second", "cc/second"); !errors.Is(err, ErrArtifactQuota) {
		t.Fatalf("concurrent staged quota error=%v", err)
	}
}

func TestMediaBatchRetryRetiresPriorStagingAndRejectsSupersededAttempts(t *testing.T) {
	service, db := capturePipelineService(t)
	media := browsercapture.V2Media{SourceURL: "https://example.com/image.png", Role: "reader_image", V2File: browsercapture.V2File{MIME: "image/png", Size: 5}}
	if err := service.commitMedia(t.Context(), "old-stage", "user-1", "bookmark-1", "attempt-1", "old-batch", 0, media, 5, "old", "aa/old"); err != nil {
		t.Fatal(err)
	}
	if err := service.beginCaptureMediaBatch(t.Context(), "user-1", "bookmark-1", "attempt-1", "retry-batch"); err != nil {
		t.Fatal(err)
	}
	var retired int
	if err := db.QueryRow(`SELECT deleted_at IS NOT NULL FROM bookmark_media WHERE id='old-stage'`).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if retired != 1 {
		t.Fatal("prior retry staging remains reserved")
	}
	queued := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,engine,queued_at) VALUES('attempt-2','bookmark-1','user-1','running','https://example.com/article','direct_http',?)`, queued); err != nil {
		t.Fatal(err)
	}
	if err := service.commitMedia(t.Context(), "stale-stage", "user-1", "bookmark-1", "attempt-1", "stale-batch", 0, media, 5, "stale", "bb/stale"); err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("stale media commit error=%v", err)
	}
}

func TestReplacementQuotaCreditsActiveMediaWhenDirectEvidenceIsSelected(t *testing.T) {
	service, db := capturePipelineService(t)
	rendered, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "rendered_article", Origin: "browser_render", Text: "Rendered reader", QualityStatus: "complete", ExtractorVersion: "v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bookmark_media(id,user_id,bookmark_id,capture_attempt_id,evidence_id,source_url,media_role,mime_type,byte_size,sha256,storage_key,created_at) VALUES('old-media','user-1','bookmark-1','attempt-1',?,'https://example.com/old.png','reader_image','image/png',5,'old','aa/old',?)`, rendered.ID, nowString()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "web_article", Origin: "direct_http", Text: "Direct reader", QualityStatus: "complete", ExtractorVersion: "web-v2", Selected: true,
	}); err != nil {
		t.Fatal(err)
	}
	service.SetArtifactQuota(5)
	media := browsercapture.V2Media{SourceURL: "https://example.com/new.png", Role: "reader_image", V2File: browsercapture.V2File{MIME: "image/png", Size: 5}}
	if err := service.commitMedia(t.Context(), "replacement", "user-1", "bookmark-1", "attempt-1", "replacement-batch", 0, media, 5, "new", "bb/new"); err != nil {
		t.Fatalf("replacement should credit the bookmark's active media: %v", err)
	}
}

func TestReplacementBatchActivatesMediaAndArtifactAtFinalQuota(t *testing.T) {
	service, db := capturePipelineService(t)
	evidence, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "rendered_article", Origin: "browser_render", Text: "Replacement reader", QualityStatus: "complete", ExtractorVersion: "v2+batch", Selected: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bookmark_media(id,user_id,bookmark_id,capture_attempt_id,evidence_id,source_url,media_role,mime_type,byte_size,sha256,storage_key,created_at) VALUES('old-media','user-1','bookmark-1','attempt-1',?,'https://example.com/old.png','reader_image','image/png',8,'old','aa/old',?)`, evidence.ID, nowString()); err != nil {
		t.Fatal(err)
	}
	service.SetArtifactQuota(10)
	media := browsercapture.V2Media{SourceURL: "https://example.com/new.png", Role: "reader_image", V2File: browsercapture.V2File{MIME: "image/png", Size: 8}}
	if err := service.commitMedia(t.Context(), "new-media", "user-1", "bookmark-1", "attempt-1", "replacement-batch", 0, media, 8, "new", "bb/new"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE bookmark_media SET evidence_id=? WHERE id='new-media'`, evidence.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.commitStagedArtifact(t.Context(), "artifact-1", "user-1", "bookmark-1", "attempt-1", "replacement-batch", evidence.ID, "screenshot", "image/jpeg", 2, "shot", "cc/shot"); err != nil {
		t.Fatalf("artifact should fit the post-replacement quota: %v", err)
	}
	if activated, err := service.activateSelectedEvidenceWithMedia(t.Context(), "user-1", "bookmark-1", "Example", "", "example.com", evidence, "attempt-1", "replacement-batch"); err != nil || !activated {
		t.Fatalf("activate replacement batch: activated=%v err=%v", activated, err)
	}
	var activeBytes, staged int64
	if err := db.QueryRow(`SELECT COALESCE((SELECT SUM(byte_size) FROM bookmark_media WHERE deleted_at IS NULL),0)+COALESCE((SELECT SUM(byte_size) FROM artifacts WHERE deleted_at IS NULL),0)`).Scan(&activeBytes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmark_media WHERE is_staged=1 AND deleted_at IS NULL`).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if activeBytes != 10 || staged != 0 {
		t.Fatalf("active bytes=%d staged=%d", activeBytes, staged)
	}
}

func TestStaleRenderedMediaCannotReplaceActiveReaderImages(t *testing.T) {
	service, db := capturePipelineService(t)
	active, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "rendered_article", Origin: "browser_render", Text: "Newer reader", SanitizedHTML: `<img src="/api/media/newer-media">`, QualityStatus: "complete", ExtractorVersion: "v2+attempt-attempt-2", Selected: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	newer := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,engine,queued_at) VALUES('attempt-2','bookmark-1','user-1','running','https://example.com/article','direct_http',?)`, newer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bookmark_media(id,user_id,bookmark_id,capture_attempt_id,evidence_id,source_url,media_role,mime_type,byte_size,sha256,storage_key,ordinal,created_at) VALUES('newer-media','user-1','bookmark-1','attempt-2',?,'https://example.com/newer.png','reader_image','image/png',5,'newer','aa/newer',0,?)`, active.ID, nowString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE bookmarks SET sanitized_html=?,text_content=?,thumbnail='/api/media/newer-media' WHERE id='bookmark-1'`, active.SanitizedHTML, active.Text); err != nil {
		t.Fatal(err)
	}
	staleMedia := browsercapture.V2Media{SourceURL: "https://example.com/stale.png", Role: "reader_image", V2File: browsercapture.V2File{MIME: "image/png", Size: 5}}
	if _, err := db.Exec(`INSERT INTO bookmark_media(id,user_id,bookmark_id,capture_attempt_id,capture_batch_id,source_url,media_role,mime_type,byte_size,sha256,storage_key,ordinal,is_staged,created_at) VALUES('stale-media','user-1','bookmark-1','attempt-1','stale-batch',?,'reader_image','image/png',5,'stale','bb/stale',0,1,?)`, staleMedia.SourceURL, nowString()); err != nil {
		t.Fatal(err)
	}
	stale, err := service.storeRenderedEvidenceWithMedia(t.Context(), "user-1", "bookmark-1", "attempt-1", "stale-batch", BookmarkEvidence{
		Kind: "rendered_article", Origin: "browser_render", Text: "Older reader", SanitizedHTML: `<img src="/api/media/stale-media">`, QualityStatus: "complete", ExtractorVersion: "v2+attempt-attempt-1",
	}, []storedCaptureMedia{{id: "stale-media", sourceURL: staleMedia.SourceURL}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.persistSelectedEvidenceForAttemptWithMedia(t.Context(), "user-1", "bookmark-1", "Example", "", "example.com", stale, "attempt-1", "stale-batch"); err != nil {
		t.Fatal(err)
	}
	var reader, thumbnail string
	if err := db.QueryRow(`SELECT sanitized_html,thumbnail FROM bookmarks WHERE id='bookmark-1'`).Scan(&reader, &thumbnail); err != nil {
		t.Fatal(err)
	}
	var activeDeleted, staleLive int
	if err := db.QueryRow(`SELECT deleted_at IS NOT NULL FROM bookmark_media WHERE id='newer-media'`).Scan(&activeDeleted); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT deleted_at IS NULL FROM bookmark_media WHERE id='stale-media'`).Scan(&staleLive); err != nil {
		t.Fatal(err)
	}
	if reader != active.SanitizedHTML || thumbnail != "/api/media/newer-media" || activeDeleted != 0 || staleLive != 0 {
		t.Fatalf("reader=%q thumbnail=%q active_deleted=%d stale_live=%d", reader, thumbnail, activeDeleted, staleLive)
	}
}

func TestFullExportRemovesInstanceLocalMediaReferences(t *testing.T) {
	service, db := capturePipelineService(t)
	evidence, err := service.UpsertEvidence(t.Context(), "user-1", "bookmark-1", BookmarkEvidence{
		Kind: "rendered_article", Origin: "browser_render", Text: "Portable reader text", SanitizedHTML: `<p>Portable reader text</p><img src="/api/media/local-media">`, QualityStatus: "complete", ExtractorVersion: "v2", Selected: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE bookmarks SET sanitized_html=?,text_content=?,thumbnail='/api/media/local-media' WHERE id='bookmark-1'`, evidence.SanitizedHTML, evidence.Text); err != nil {
		t.Fatal(err)
	}
	exported, err := service.fullExport(t.Context(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	bookmark := mapList(exported["bookmarks"])[0]
	raw := stringValue(bookmark["html_content"]) + stringValue(bookmark["thumbnail"])
	for _, item := range mapList(bookmark["evidence"]) {
		raw += stringValue(item["sanitized_html"])
	}
	if strings.Contains(raw, "/api/media/") || !strings.Contains(raw, "Portable reader text") {
		t.Fatalf("non-portable export content=%q", raw)
	}
}

func TestBookmarkMergeDoesNotCopyDeletedBookmarkMediaReferences(t *testing.T) {
	_, db := capturePipelineService(t)
	now := nowString()
	if _, err := db.Exec(`INSERT INTO bookmarks(id,user_id,url,title,domain,thumbnail,sanitized_html,text_content,created_at,updated_at) VALUES('bookmark-2','user-1','https://example.com/duplicate','Duplicate','example.com','/api/media/duplicate-media','<p>Duplicate reader</p><img src="/api/media/duplicate-media">','Duplicate reader',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mergeOneBookmark(t.Context(), tx, "user-1", "bookmark-1", "bookmark-2"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var thumbnail, reader, text string
	if err := db.QueryRow(`SELECT COALESCE(thumbnail,''),COALESCE(sanitized_html,''),COALESCE(text_content,'') FROM bookmarks WHERE id='bookmark-1'`).Scan(&thumbnail, &reader, &text); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(thumbnail+reader, "/api/media/") || text != "Duplicate reader" {
		t.Fatalf("thumbnail=%q reader=%q text=%q", thumbnail, reader, text)
	}
}

func TestMediaContentIsOwnerScopedAndPublicSharesUseSnapshots(t *testing.T) {
	service, db := capturePipelineService(t)
	store, err := assets.New(t.TempDir()+"/arivu.sqlite3", 1024)
	if err != nil {
		t.Fatal(err)
	}
	service.SetAssetStore(store)
	key, digest, size, err := store.Put(strings.NewReader("image"))
	if err != nil {
		t.Fatal(err)
	}
	mediaID := "media-1"
	if _, err := db.Exec(`INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,content_text,sanitized_html,content_hash,quality_status,extractor_version,is_selected,created_at,updated_at) VALUES('evidence-1','bookmark-1','user-1','rendered_article','browser_render','Reader','<p>Reader</p><img src="/api/media/media-1">','hash','complete','v2',1,?,?)`, nowString(), nowString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bookmark_media(id,user_id,bookmark_id,capture_attempt_id,evidence_id,source_url,media_role,mime_type,byte_size,sha256,storage_key,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, mediaID, "user-1", "bookmark-1", "attempt-1", "evidence-1", "https://example.com/image.png", "reader_image", "image/png", size, digest, key, nowString()); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/media/"+mediaID, nil)
	request.SetPathValue("id", mediaID)
	response := httptest.NewRecorder()
	service.MediaContent(response, request, auth.User{ID: "user-1"})
	if response.Code != http.StatusOK || response.Body.String() != "image" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("owner media response status=%d body=%q headers=%v", response.Code, response.Body.String(), response.Header())
	}
	denied := httptest.NewRecorder()
	service.MediaContent(denied, request, auth.User{ID: "user-2"})
	if denied.Code != http.StatusNotFound {
		t.Fatalf("cross-owner media status=%d", denied.Code)
	}

	token := "public-token"
	if _, err := db.Exec(`INSERT INTO public_shares(id,user_id,token_digest,title,created_at,updated_at) VALUES('share-1','user-1',?,'Shared',?,?)`, tokenDigest(token), nowString(), nowString()); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceShareMembers(t.Context(), tx, "share-1", "user-1", []string{"bookmark-1"}, nil, nowString()); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	shareRequest := httptest.NewRequest(http.MethodGet, "/api/public/shares/"+token, nil)
	shareRequest.SetPathValue("token", token)
	shareRequest.RemoteAddr = "203.0.113.10:1234"
	shareResponse := httptest.NewRecorder()
	service.PublicShareJSON(shareResponse, shareRequest)
	shareBody := shareResponse.Body.String()
	if shareResponse.Code != http.StatusOK || !strings.Contains(shareBody, "/s/"+token+"/media/"+mediaID) {
		t.Fatalf("public share status=%d body=%s", shareResponse.Code, shareBody)
	}
	publicRequest := httptest.NewRequest(http.MethodGet, "/s/"+token+"/media/"+mediaID, nil)
	publicRequest.SetPathValue("token", token)
	publicRequest.SetPathValue("media", mediaID)
	publicRequest.RemoteAddr = "203.0.113.11:1234"
	publicResponse := httptest.NewRecorder()
	service.PublicMedia(publicResponse, publicRequest)
	if publicResponse.Code != http.StatusOK || publicResponse.Body.String() != "image" {
		t.Fatalf("public media status=%d body=%q", publicResponse.Code, publicResponse.Body.String())
	}
}

func TestPreservedHTMLViewerIsInlineAndLockedDown(t *testing.T) {
	service, db := capturePipelineService(t)
	store, err := assets.New(filepath.Join(t.TempDir(), "assets.sqlite3"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	service.SetAssetStore(store)
	body := `<html><head><base href="https://tracker.example/"><meta http-equiv="refresh" content="0;url=https://tracker.example/live"><script>location='https://tracker.example/script'</script></head><body><p>Offline copy</p><a href="https://tracker.example/link" onclick="alert(1)">Original link</a><iframe src="https://tracker.example/frame"></iframe><form action="https://tracker.example/form"><button formaction="https://tracker.example/button">Submit</button></form><img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt="Embedded image"></body></html>`
	key, digest, size, err := store.Put(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO artifacts(id,user_id,bookmark_id,capture_attempt_id,artifact_type,mime_type,byte_size,sha256,storage_key,created_at) VALUES('html-artifact','user-1','bookmark-1','attempt-1','self_contained_html','text/html',?,?,?,?)`, size, digest, key, nowString()); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/artifacts/html-artifact/content?view=1", nil)
	req.SetPathValue("id", "html-artifact")
	recorder := httptest.NewRecorder()
	recorder.Header().Set("X-Frame-Options", "DENY")
	service.ArtifactContent(recorder, req, auth.User{ID: "user-1"})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Offline copy") {
		t.Fatalf("viewer status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	for _, unsafe := range []string{"tracker.example", "<script", "<iframe", " href=", " onclick=", " action=", " formaction="} {
		if strings.Contains(recorder.Body.String(), unsafe) {
			t.Fatalf("viewer retained navigation or active content %q in %q", unsafe, recorder.Body.String())
		}
	}
	if !strings.Contains(recorder.Body.String(), "Original link") || !strings.Contains(recorder.Body.String(), "data:image/gif") {
		t.Fatalf("viewer removed preserved text or embedded media: %q", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "<body inert") {
		t.Fatalf("viewer body is still interactive: %q", recorder.Body.String())
	}
	if disposition := recorder.Header().Get("Content-Disposition"); !strings.HasPrefix(disposition, "inline") {
		t.Fatalf("viewer disposition=%q", disposition)
	}
	policy := recorder.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "sandbox") || !strings.Contains(policy, "default-src 'none'") || recorder.Header().Get("X-Frame-Options") != "" {
		t.Fatalf("viewer headers=%v", recorder.Header())
	}
}

func TestPreservedHTMLViewerRejectsOversizedPreview(t *testing.T) {
	service, db := capturePipelineService(t)
	store, err := assets.New(filepath.Join(t.TempDir(), "assets.sqlite3"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	service.SetAssetStore(store)
	key, digest, _, err := store.Put(strings.NewReader("<p>Offline copy</p>"))
	if err != nil {
		t.Fatal(err)
	}
	size := archivedHTMLPreviewLimit + 1
	if _, err := db.Exec(`INSERT INTO artifacts(id,user_id,bookmark_id,capture_attempt_id,artifact_type,mime_type,byte_size,sha256,storage_key,created_at) VALUES('large-html-artifact','user-1','bookmark-1','attempt-1','self_contained_html','text/html',?,?,?,?)`, size, digest, key, nowString()); err != nil {
		t.Fatal(err)
	}
	artifacts := service.bookmarkArtifacts(t.Context(), "user-1", "bookmark-1")
	if len(artifacts) != 1 || artifacts[0]["preview_url"] != nil {
		t.Fatalf("oversized artifact unexpectedly previewable: %#v", artifacts)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/artifacts/large-html-artifact/content?view=1", nil)
	req.SetPathValue("id", "large-html-artifact")
	recorder := httptest.NewRecorder()
	service.ArtifactContent(recorder, req, auth.User{ID: "user-1"})
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "too large to preview safely") {
		t.Fatalf("viewer status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
