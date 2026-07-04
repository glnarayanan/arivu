package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bookmarksvc "github.com/glnarayanan/arivu/internal/bookmarks"
	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/providers"
)

func TestWebAuthRequiresCSRFForBookmarkCreate(t *testing.T) {
	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		SignupEnabled:  true,
		SessionTTL:     3600_000_000_000,
		RefreshTTL:     3600_000_000_000,
		ExtensionTTL:   3600_000_000_000,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	handler := a.Handler()

	signupBody := bytes.NewBufferString(`{"email":"user@example.com","password":"correct horse battery staple"}`)
	signupReq := httptest.NewRequest(http.MethodPost, "/api/auth/signup", signupBody)
	signupReq.Header.Set("Content-Type", "application/json")
	signupRec := httptest.NewRecorder()
	handler.ServeHTTP(signupRec, signupReq)
	resp := signupRec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signup status = %d", resp.StatusCode)
	}

	var accessCookie, refreshCookie, csrfCookie *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "access_token" {
			accessCookie = cookie
		}
		if cookie.Name == "refresh_token" {
			refreshCookie = cookie
		}
		if cookie.Name == "csrf_token" {
			csrfCookie = cookie
		}
	}
	if accessCookie == nil || refreshCookie == nil || csrfCookie == nil {
		t.Fatalf("expected access, refresh, and csrf cookies, got %#v", resp.Cookies())
	}
	if !accessCookie.HttpOnly || !refreshCookie.HttpOnly {
		t.Fatalf("expected access and refresh cookies to be HttpOnly")
	}
	if csrfCookie.HttpOnly {
		t.Fatalf("expected csrf cookie to remain readable for the double-submit header")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/bookmarks", strings.NewReader(`{"url":"https://example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(accessCookie)
	missingRec := httptest.NewRecorder()
	handler.ServeHTTP(missingRec, req)
	missingCSRF := missingRec.Result()
	defer missingCSRF.Body.Close()
	if missingCSRF.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bookmark without csrf status = %d", missingCSRF.StatusCode)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/bookmarks", strings.NewReader(`{"url":"https://example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfCookie.Value)
	req.AddCookie(accessCookie)
	req.AddCookie(csrfCookie)
	okRec := httptest.NewRecorder()
	handler.ServeHTTP(okRec, req)
	ok := okRec.Result()
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		var body map[string]any
		_ = json.NewDecoder(ok.Body).Decode(&body)
		t.Fatalf("bookmark with csrf status = %d body=%v", ok.StatusCode, body)
	}
}

func TestAudienceScopedTokensCannotReachWebOrAdminRoutes(t *testing.T) {
	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		AdminEmails:    map[string]bool{"admin@example.com": true},
		SignupEnabled:  true,
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	handler := a.Handler()
	accessCookie, csrfCookie := signupForCookies(t, handler, "admin@example.com")
	cliToken := bodyToken(t, handler, http.MethodPost, "/api/auth/cli/login", `{"email":"admin@example.com","password":"correct horse battery staple"}`, "")

	extensionResp := adminRequest(t, handler, http.MethodPost, "/api/auth/extension-token", "", accessCookie, csrfCookie)
	if extensionResp.StatusCode != http.StatusOK {
		t.Fatalf("extension-token status = %d body=%s", extensionResp.StatusCode, readBody(extensionResp))
	}
	var extensionBody map[string]any
	if err := json.NewDecoder(extensionResp.Body).Decode(&extensionBody); err != nil {
		t.Fatalf("decode extension token: %v", err)
	}
	extensionResp.Body.Close()
	extensionToken, _ := extensionBody["access_token"].(string)
	if extensionToken == "" {
		t.Fatalf("extension token missing: %#v", extensionBody)
	}

	for _, tc := range []struct {
		name   string
		token  string
		method string
		path   string
		body   string
	}{
		{"cli bookmark web route", cliToken, http.MethodPost, "/api/bookmarks", `{"url":"https://example.com"}`},
		{"cli notes web route", cliToken, http.MethodGet, "/api/notes", ``},
		{"cli admin web route", cliToken, http.MethodPut, "/api/admin/api-keys", `{"gemini_api_key":"x"}`},
		{"extension bookmark web route", extensionToken, http.MethodPost, "/api/bookmarks", `{"url":"https://example.com"}`},
		{"extension notes web route", extensionToken, http.MethodGet, "/api/notes", ``},
		{"extension admin web route", extensionToken, http.MethodPut, "/api/admin/api-keys", `{"gemini_api_key":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := bearerRequest(t, handler, tc.method, tc.path, tc.body, tc.token)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(resp))
			}
			resp.Body.Close()
		})
	}

	if resp := bearerRequest(t, handler, http.MethodPost, "/api/cli/bookmarks", `{"url":"https://example.com/cli"}`, cliToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("cli scoped bookmark status = %d body=%s", resp.StatusCode, readBody(resp))
	}
	if resp := bearerRequest(t, handler, http.MethodGet, "/api/extension/collections", "", extensionToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("extension scoped collections status = %d body=%s", resp.StatusCode, readBody(resp))
	}
	if resp := bearerRequest(t, handler, http.MethodPost, "/api/extension/bookmarks", `{"url":"https://example.com/extension"}`, extensionToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("extension scoped bookmark status = %d body=%s", resp.StatusCode, readBody(resp))
	}
}

func TestSecondBrainRoutesAreScopedAndCSRFProtected(t *testing.T) {
	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		SignupEnabled:  true,
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	handler := a.Handler()
	accessCookie, csrfCookie := signupForCookies(t, handler, "second-brain@example.com")
	otherAccess, otherCSRF := signupForCookies(t, handler, "other@example.com")
	userID := userIDForEmail(t, a, "second-brain@example.com")
	now := time.Now().UTC()
	insertBookmarkForTest(t, a, userID, "capture", "Capture Loop", now.AddDate(0, 0, -21), now.AddDate(0, 0, -14), 0, 5)
	_, _ = a.db.ExecContext(context.Background(), `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,bullet_summary_json,highlight_quotes_json,suggested_tags_json,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "summary-capture", "capture", userID, "The capture loop needs review.", `["Save with context","Recall with evidence"]`, `["Recall with evidence"]`, `["Second Brain"]`, "completed", now.Format(time.RFC3339), now.Format(time.RFC3339))

	missingCSRF := httptest.NewRequest(http.MethodPost, "/api/notes", strings.NewReader(`{"title":"No CSRF"}`))
	missingCSRF.Header.Set("Content-Type", "application/json")
	missingCSRF.AddCookie(accessCookie)
	missingRec := httptest.NewRecorder()
	handler.ServeHTTP(missingRec, missingCSRF)
	missingResp := missingRec.Result()
	if missingResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("note create without csrf status = %d body=%s", missingResp.StatusCode, readBody(missingResp))
	}
	missingResp.Body.Close()

	noteResp := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"Research note","body":"Keep quotes with the source.","bookmark_id":"capture"}`, accessCookie, csrfCookie)
	if noteResp.StatusCode != http.StatusOK {
		t.Fatalf("create note status = %d body=%s", noteResp.StatusCode, readBody(noteResp))
	}
	var noteBody struct {
		Note map[string]any `json:"note"`
	}
	_ = json.NewDecoder(noteResp.Body).Decode(&noteBody)
	noteResp.Body.Close()
	noteID, _ := noteBody.Note["id"].(string)
	if noteID == "" || noteBody.Note["bookmark_id"] != "capture" {
		t.Fatalf("unexpected note body: %#v", noteBody)
	}

	annotationResp := adminRequest(t, handler, http.MethodPost, "/api/bookmarks/capture/annotations", `{"quote":"Recall with evidence","note":"Promote this into review.","selector":{"type":"quote"},"tags":["Evidence","evidence"]}`, accessCookie, csrfCookie)
	if annotationResp.StatusCode != http.StatusOK {
		t.Fatalf("create annotation status = %d body=%s", annotationResp.StatusCode, readBody(annotationResp))
	}
	var annotationBody struct {
		Annotation map[string]any `json:"annotation"`
	}
	_ = json.NewDecoder(annotationResp.Body).Decode(&annotationBody)
	annotationResp.Body.Close()
	annotationID, _ := annotationBody.Annotation["id"].(string)
	if annotationID == "" {
		t.Fatalf("annotation missing id: %#v", annotationBody)
	}

	tagResp := adminRequest(t, handler, http.MethodPost, "/api/tags", `{"name":"Second Brain"}`, accessCookie, csrfCookie)
	if tagResp.StatusCode != http.StatusOK {
		t.Fatalf("create tag status = %d body=%s", tagResp.StatusCode, readBody(tagResp))
	}
	var tagBody struct {
		Tag map[string]any `json:"tag"`
	}
	_ = json.NewDecoder(tagResp.Body).Decode(&tagBody)
	tagResp.Body.Close()
	tagID, _ := tagBody.Tag["id"].(string)
	if tagID == "" || tagBody.Tag["slug"] != "second-brain" {
		t.Fatalf("unexpected tag body: %#v", tagBody)
	}

	aliasResp := adminRequest(t, handler, http.MethodPost, "/api/tags/aliases", `{"tag_id":"`+tagID+`","alias":"PKM"}`, accessCookie, csrfCookie)
	if aliasResp.StatusCode != http.StatusOK {
		t.Fatalf("create alias status = %d body=%s", aliasResp.StatusCode, readBody(aliasResp))
	}
	aliasResp.Body.Close()

	searchResp := adminRequest(t, handler, http.MethodPost, "/api/saved-searches", `{"name":"Unread PKM","query":"capture recall","filters":{"read_status":"unread","tag":"Second Brain"}}`, accessCookie, csrfCookie)
	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("create saved search status = %d body=%s", searchResp.StatusCode, readBody(searchResp))
	}
	searchResp.Body.Close()

	jobID, err := a.jobs.EnqueueWithID(context.Background(), userID, "bookmark.process", `{"bookmark_id":"capture"}`)
	if err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	jobResp := adminRequest(t, handler, http.MethodGet, "/api/jobs/"+jobID, "", accessCookie, csrfCookie)
	if jobResp.StatusCode != http.StatusOK {
		t.Fatalf("job status = %d body=%s", jobResp.StatusCode, readBody(jobResp))
	}
	var jobBody map[string]any
	_ = json.NewDecoder(jobResp.Body).Decode(&jobBody)
	jobResp.Body.Close()
	if jobBody["id"] != jobID || jobBody["status"] != "queued" {
		t.Fatalf("unexpected job body: %#v", jobBody)
	}

	detailResp := adminRequest(t, handler, http.MethodGet, "/api/bookmarks/capture", "", accessCookie, csrfCookie)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("bookmark detail status = %d body=%s", detailResp.StatusCode, readBody(detailResp))
	}
	var detail map[string]any
	_ = json.NewDecoder(detailResp.Body).Decode(&detail)
	detailResp.Body.Close()
	if len(detail["notes"].([]any)) != 1 || len(detail["annotations"].([]any)) != 1 || len(detail["tags"].([]any)) == 0 {
		t.Fatalf("bookmark detail missing second-brain data: %#v", detail)
	}

	filteredResp := adminRequest(t, handler, http.MethodGet, "/api/bookmarks?tag=evidence&date_from="+url.QueryEscape(now.AddDate(0, 0, -30).Format(time.RFC3339)), "", accessCookie, csrfCookie)
	if filteredResp.StatusCode != http.StatusOK {
		t.Fatalf("filtered bookmarks status = %d body=%s", filteredResp.StatusCode, readBody(filteredResp))
	}
	var filtered []map[string]any
	_ = json.NewDecoder(filteredResp.Body).Decode(&filtered)
	filteredResp.Body.Close()
	if len(filtered) != 1 || filtered[0]["id"] != "capture" {
		t.Fatalf("tag/date filter did not return capture: %#v", filtered)
	}

	answerResp := adminRequest(t, handler, http.MethodGet, "/api/search/answer?q=recall&tag=evidence", "", accessCookie, csrfCookie)
	if answerResp.StatusCode != http.StatusOK {
		t.Fatalf("answer status = %d body=%s", answerResp.StatusCode, readBody(answerResp))
	}
	var answerBody struct {
		Answer    string           `json:"answer"`
		Citations []map[string]any `json:"citations"`
	}
	_ = json.NewDecoder(answerResp.Body).Decode(&answerBody)
	answerResp.Body.Close()
	if answerBody.Answer == "" || len(answerBody.Citations) != 1 || answerBody.Citations[0]["id"] != "capture" || answerBody.Citations[0]["snippet"] == "" {
		t.Fatalf("answer mode missing cited capture: %#v", answerBody)
	}

	reviewResp := adminRequest(t, handler, http.MethodGet, "/api/review?limit=5", "", accessCookie, csrfCookie)
	if reviewResp.StatusCode != http.StatusOK {
		t.Fatalf("review status = %d body=%s", reviewResp.StatusCode, readBody(reviewResp))
	}
	var reviewBody struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(reviewResp.Body).Decode(&reviewBody)
	reviewResp.Body.Close()
	if len(reviewBody.Items) != 1 || reviewBody.Items[0]["id"] != "capture" {
		t.Fatalf("unexpected review queue: %#v", reviewBody)
	}

	completeResp := adminRequest(t, handler, http.MethodPost, "/api/review/bookmark:capture/complete", `{}`, accessCookie, csrfCookie)
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("complete review status = %d body=%s", completeResp.StatusCode, readBody(completeResp))
	}
	completeResp.Body.Close()
	var readStatus bool
	_ = a.db.QueryRowContext(context.Background(), `SELECT read_status FROM bookmarks WHERE id='capture' AND user_id=?`, userID).Scan(&readStatus)
	if !readStatus {
		t.Fatal("review completion did not mark bookmark read")
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"other cannot read note", http.MethodGet, "/api/notes/" + noteID, ""},
		{"other cannot patch annotation", http.MethodPatch, "/api/annotations/" + annotationID, `{"quote":"x","selector":{}}`},
		{"other cannot read job", http.MethodGet, "/api/jobs/" + jobID, ""},
		{"other cannot complete review", http.MethodPost, "/api/review/bookmark:capture/complete", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := adminRequest(t, handler, tc.method, tc.path, tc.body, otherAccess, otherCSRF)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(resp))
			}
			resp.Body.Close()
		})
	}
}

func TestBrowserFacingFirstRunContracts(t *testing.T) {
	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		SignupEnabled:  true,
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	handler := a.Handler()
	accessCookie, csrfCookie := signupForCookies(t, handler, "first-run@example.com")

	resp := adminRequest(t, handler, http.MethodGet, "/api/bookmarks", "", accessCookie, csrfCookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bookmark list status = %d body=%s", resp.StatusCode, readBody(resp))
	}
	body := readBody(resp)
	if strings.TrimSpace(body) != "[]" {
		t.Fatalf("empty bookmark list must encode as [], got %q", body)
	}

	script, err := webFS.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	source := string(script)
	if !strings.Contains(source, "<h1 class=\"headline\">${escapeHTML(title)}</h1>") {
		t.Fatal("shell must escape route titles before writing them into the DOM")
	}
	if strings.Contains(source, "<h1 class=\"headline\">${title}</h1>") {
		t.Fatal("shell must not write raw route titles into the DOM")
	}
	if !strings.Contains(source, "${content}") {
		t.Fatal("shell must insert first-party route markup as markup, not escaped text")
	}
}

func TestFrontendAssetsUseCacheValidation(t *testing.T) {
	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		SignupEnabled:  true,
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	handler := a.Handler()

	script := frontendRequest(t, handler, "/app.js", "")
	etag := script.Header.Get("ETag")
	if script.StatusCode != http.StatusOK {
		t.Fatalf("script status = %d body=%s", script.StatusCode, readBody(script))
	}
	if etag == "" {
		t.Fatal("script asset must expose a content ETag")
	}
	if got := script.Header.Get("Cache-Control"); got != "public, max-age=0, must-revalidate" {
		t.Fatalf("script cache-control = %q", got)
	}
	_ = readBody(script)

	revalidated := frontendRequest(t, handler, "/app.js", etag)
	defer revalidated.Body.Close()
	if revalidated.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional script status = %d body=%s", revalidated.StatusCode, readBody(revalidated))
	}

	icon := frontendRequest(t, handler, "/favicon.ico", "")
	if icon.StatusCode != http.StatusOK {
		t.Fatalf("favicon status = %d body=%s", icon.StatusCode, readBody(icon))
	}
	if got := icon.Header.Get("Content-Type"); got != "image/svg+xml" {
		t.Fatalf("favicon content-type = %q", got)
	}
	body := readBody(icon)
	if !strings.Contains(body, "<svg") || strings.Contains(body, "<!doctype html>") {
		t.Fatalf("favicon must serve SVG, got %q", body)
	}

	manifest := frontendRequest(t, handler, "/manifest.webmanifest", "")
	if manifest.StatusCode != http.StatusOK {
		t.Fatalf("manifest status = %d body=%s", manifest.StatusCode, readBody(manifest))
	}
	if got := manifest.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/manifest+json") {
		t.Fatalf("manifest content-type = %q", got)
	}
	var manifestBody map[string]any
	if err := json.NewDecoder(manifest.Body).Decode(&manifestBody); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	manifest.Body.Close()
	shareTarget, ok := manifestBody["share_target"].(map[string]any)
	if !ok || shareTarget["action"] != "/dashboard" || shareTarget["method"] != "GET" {
		t.Fatalf("manifest share target missing dashboard GET capture: %#v", manifestBody)
	}
}

func TestAdminUserMutations(t *testing.T) {
	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		AdminEmails:    map[string]bool{"admin@example.com": true},
		SignupEnabled:  true,
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	handler := a.Handler()
	accessCookie, csrfCookie := signupForCookies(t, handler, "admin@example.com")

	invite := adminRequest(t, handler, http.MethodPost, "/api/admin/users/invite", `{"email":"invitee@example.com","name":"Invitee"}`, accessCookie, csrfCookie)
	if invite.StatusCode != http.StatusOK {
		t.Fatalf("invite status = %d body=%s", invite.StatusCode, readBody(invite))
	}
	var invited map[string]any
	_ = json.NewDecoder(invite.Body).Decode(&invited)
	invite.Body.Close()
	userID, _ := invited["id"].(string)
	if userID == "" {
		t.Fatalf("invite response missing id: %#v", invited)
	}

	list := adminRequest(t, handler, http.MethodGet, "/api/admin/users", "", accessCookie, csrfCookie)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d body=%s", list.StatusCode, readBody(list))
	}
	list.Body.Close()

	detail := adminRequest(t, handler, http.MethodGet, "/api/admin/users/"+userID, "", accessCookie, csrfCookie)
	if detail.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", detail.StatusCode, readBody(detail))
	}
	detail.Body.Close()

	reset := adminRequest(t, handler, http.MethodPost, "/api/admin/users/"+userID+"/reset-password", `{"new_password":"new-password-123"}`, accessCookie, csrfCookie)
	if reset.StatusCode != http.StatusOK {
		t.Fatalf("reset status = %d body=%s", reset.StatusCode, readBody(reset))
	}
	reset.Body.Close()

	for _, path := range []string{"/api/admin/users/" + userID + "/ban", "/api/admin/users/" + userID + "/unban"} {
		resp := adminRequest(t, handler, http.MethodPost, path, `{}`, accessCookie, csrfCookie)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, resp.StatusCode, readBody(resp))
		}
		resp.Body.Close()
	}

	deleted := adminRequest(t, handler, http.MethodDelete, "/api/admin/users/"+userID, "", accessCookie, csrfCookie)
	if deleted.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", deleted.StatusCode, readBody(deleted))
	}
	deleted.Body.Close()
}

func TestXOAuthStatusSyncAndDisconnect(t *testing.T) {
	var tokenCalls, profileCalls, bookmarksCalls int
	xHTTP := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/2/oauth2/token":
			tokenCalls++
			form := requestForm(t, req)
			if form.Get("grant_type") != "authorization_code" || form.Get("code_verifier") == "" {
				t.Fatalf("unexpected token form: %v", form)
			}
			return jsonResponse(http.StatusOK, map[string]any{"access_token": "x-access", "refresh_token": "x-refresh", "expires_in": 7200, "scope": "bookmark.read tweet.read users.read offline.access"}), nil
		case "/2/users/me":
			profileCalls++
			if got := req.Header.Get("Authorization"); got != "Bearer x-access" {
				t.Fatalf("profile auth = %q", got)
			}
			return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"id": "x-user", "username": "arivu", "name": "Arivu", "profile_image_url": "https://example.com/avatar.png"}}), nil
		case "/2/users/x-user/bookmarks":
			bookmarksCalls++
			if got := req.Header.Get("Authorization"); got != "Bearer x-access" {
				t.Fatalf("bookmarks auth = %q", got)
			}
			return jsonResponse(http.StatusOK, map[string]any{
				"data": []map[string]any{{
					"id":        "tweet-1",
					"text":      "A useful saved link",
					"author_id": "x-user",
					"entities":  map[string]any{"urls": []map[string]any{{"expanded_url": "https://example.com/article?utm_source=x"}}},
					"public_metrics": map[string]any{
						"like_count": 3,
					},
				}},
				"includes": map[string]any{"users": []map[string]any{{"id": "x-user", "username": "arivu", "name": "Arivu"}}},
				"meta":     map[string]any{},
			}), nil
		case "/2/oauth2/revoke":
			return jsonResponse(http.StatusOK, map[string]any{"revoked": true}), nil
		default:
			t.Fatalf("unexpected X API path: %s", req.URL.Path)
			return jsonResponse(http.StatusNotFound, map[string]any{"detail": "not found"}), nil
		}
	})}

	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		AppURL:         "https://app.example.test",
		SecretKey:      "test-secret",
		SignupEnabled:  true,
		XEnabled:       true,
		XClientID:      "client-id",
		XClientSecret:  "client-secret",
		XRedirectURI:   "https://app.example.test/settings?section=connections",
		XAPIBaseURL:    "https://x-api.example.test",
		XAuthorizeURL:  "https://x.example.test/i/oauth2/authorize",
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	a.xHTTP = xHTTP
	handler := a.Handler()
	accessCookie, csrfCookie := signupForCookies(t, handler, "x-user@example.com")

	connect := adminRequest(t, handler, http.MethodGet, "/api/auth/x/connect", "", accessCookie, csrfCookie)
	if connect.StatusCode != http.StatusOK {
		t.Fatalf("connect status = %d body=%s", connect.StatusCode, readBody(connect))
	}
	var connectBody map[string]string
	_ = json.NewDecoder(connect.Body).Decode(&connectBody)
	connect.Body.Close()
	authURL, err := url.Parse(connectBody["auth_url"])
	if err != nil {
		t.Fatalf("invalid auth_url: %v", err)
	}
	state := authURL.Query().Get("state")
	if state == "" || authURL.Query().Get("code_challenge") == "" {
		t.Fatalf("auth_url missing state/challenge: %s", connectBody["auth_url"])
	}

	callback := adminRequest(t, handler, http.MethodPost, "/api/auth/x/callback", `{"code":"oauth-code","state":"`+state+`"}`, accessCookie, csrfCookie)
	if callback.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d body=%s", callback.StatusCode, readBody(callback))
	}
	callback.Body.Close()

	status := adminRequest(t, handler, http.MethodGet, "/api/auth/x/status", "", accessCookie, csrfCookie)
	if status.StatusCode != http.StatusOK {
		t.Fatalf("status status = %d body=%s", status.StatusCode, readBody(status))
	}
	var statusBody map[string]any
	_ = json.NewDecoder(status.Body).Decode(&statusBody)
	status.Body.Close()
	if statusBody["connected"] != true || statusBody["x_username"] != "arivu" {
		t.Fatalf("unexpected status: %#v", statusBody)
	}

	syncResp := adminRequest(t, handler, http.MethodPost, "/api/auth/x/sync", `{}`, accessCookie, csrfCookie)
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d body=%s", syncResp.StatusCode, readBody(syncResp))
	}
	var syncBody map[string]any
	_ = json.NewDecoder(syncResp.Body).Decode(&syncBody)
	syncResp.Body.Close()
	if syncBody["new_bookmarks"].(float64) != 1 || bookmarksCalls != 1 {
		t.Fatalf("unexpected sync: body=%#v calls=%d", syncBody, bookmarksCalls)
	}

	list := adminRequest(t, handler, http.MethodGet, "/api/bookmarks?source=x", "", accessCookie, csrfCookie)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d body=%s", list.StatusCode, readBody(list))
	}
	var bookmarks []map[string]any
	_ = json.NewDecoder(list.Body).Decode(&bookmarks)
	list.Body.Close()
	if len(bookmarks) != 1 || bookmarks[0]["source"] != "x" {
		t.Fatalf("unexpected x bookmarks: %#v", bookmarks)
	}

	disconnect := adminRequest(t, handler, http.MethodPost, "/api/auth/x/disconnect", `{}`, accessCookie, csrfCookie)
	if disconnect.StatusCode != http.StatusOK {
		t.Fatalf("disconnect status = %d body=%s", disconnect.StatusCode, readBody(disconnect))
	}
	disconnect.Body.Close()
	if tokenCalls != 1 || profileCalls != 1 {
		t.Fatalf("unexpected fake X API calls: token=%d profile=%d", tokenCalls, profileCalls)
	}
}

func TestResurfacingScoringAndMutations(t *testing.T) {
	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		SignupEnabled:  true,
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	handler := a.Handler()
	accessCookie, csrfCookie := signupForCookies(t, handler, "resurface@example.com")
	userID := userIDForEmail(t, a, "resurface@example.com")
	now := time.Now().UTC()
	insertBookmarkForTest(t, a, userID, "weekly", "Weekly Review", now.AddDate(0, 0, -14), now.AddDate(0, 0, -7), 4, 3)
	insertBookmarkForTest(t, a, userID, "recent", "Recent Item", now.Add(-2*time.Hour), now.Add(-2*time.Hour), 0, 2)
	_, _ = a.db.ExecContext(context.Background(), `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,suggested_tags_json,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, "summary-weekly", "weekly", userID, "Worth revisiting.", `["research"]`, "completed", now.Format(time.RFC3339), now.Format(time.RFC3339))

	resp := adminRequest(t, handler, http.MethodGet, "/api/resurfacing?limit=2", "", accessCookie, csrfCookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resurfacing status = %d body=%s", resp.StatusCode, readBody(resp))
	}
	var body struct {
		Suggestions     []map[string]any `json:"suggestions"`
		TotalCandidates int              `json:"total_candidates"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if len(body.Suggestions) != 1 || body.Suggestions[0]["id"] != "weekly" {
		t.Fatalf("expected only weekly candidate, got %#v", body)
	}
	if body.Suggestions[0]["resurfacing_score"].(float64) <= 0 || body.Suggestions[0]["resurfacing_reason"] == "" {
		t.Fatalf("missing score or reason: %#v", body.Suggestions[0])
	}

	snooze := adminRequest(t, handler, http.MethodPost, "/api/resurfacing/weekly/snooze", `{"days":3}`, accessCookie, csrfCookie)
	if snooze.StatusCode != http.StatusOK {
		t.Fatalf("snooze status = %d body=%s", snooze.StatusCode, readBody(snooze))
	}
	snooze.Body.Close()
	resp = adminRequest(t, handler, http.MethodGet, "/api/resurfacing", "", accessCookie, csrfCookie)
	var snoozedBody struct {
		Suggestions []map[string]any `json:"suggestions"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&snoozedBody)
	resp.Body.Close()
	if len(snoozedBody.Suggestions) != 0 {
		t.Fatalf("expected snoozed bookmark hidden, got %#v", snoozedBody.Suggestions)
	}

	unarchive := adminRequest(t, handler, http.MethodPost, "/api/resurfacing/weekly/unarchive", `{}`, accessCookie, csrfCookie)
	if unarchive.StatusCode != http.StatusOK {
		t.Fatalf("unarchive status = %d body=%s", unarchive.StatusCode, readBody(unarchive))
	}
	unarchive.Body.Close()
	_, _ = a.db.ExecContext(context.Background(), `UPDATE bookmarks SET resurfacing_snoozed_until=NULL WHERE id='weekly'`)
	archive := adminRequest(t, handler, http.MethodPost, "/api/resurfacing/weekly/archive", `{}`, accessCookie, csrfCookie)
	if archive.StatusCode != http.StatusOK {
		t.Fatalf("archive status = %d body=%s", archive.StatusCode, readBody(archive))
	}
	archive.Body.Close()
	resp = adminRequest(t, handler, http.MethodGet, "/api/memory-jogger", "", accessCookie, csrfCookie)
	var jogger map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&jogger)
	resp.Body.Close()
	if jogger["has_memory"] != false {
		t.Fatalf("expected archived bookmark hidden from memory jogger, got %#v", jogger)
	}
}

func TestDuplicateDetectionAndMergePolicy(t *testing.T) {
	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		SignupEnabled:  true,
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	handler := a.Handler()
	accessCookie, csrfCookie := signupForCookies(t, handler, "duplicates@example.com")
	userID := userIDForEmail(t, a, "duplicates@example.com")
	now := time.Now().UTC()
	insertBookmarkForTest(t, a, userID, "keep", "Keep", now.AddDate(0, 0, -10), now.AddDate(0, 0, -7), 1, 2)
	insertBookmarkForTest(t, a, userID, "dupe", "Duplicate", now.AddDate(0, 0, -9), now.AddDate(0, 0, -1), 4, 8)
	_, _ = a.db.ExecContext(context.Background(), `UPDATE bookmarks SET url=?, description=?, embedding=?, embedding_dim=?, embedding_model=? WHERE id=?`, "https://example.com/article?utm_source=news", "Better description", []byte(`[1,0]`), 2, "test", "dupe")
	_, _ = a.db.ExecContext(context.Background(), `UPDATE bookmarks SET url=?, embedding=?, embedding_dim=?, embedding_model=? WHERE id=?`, "https://example.com/article#section", []byte(`[0.99,0.01]`), 2, "test", "keep")
	insertBookmarkForTest(t, a, userID, "similar", "Similar", now.AddDate(0, 0, -8), now.AddDate(0, 0, -4), 0, 2)
	_, _ = a.db.ExecContext(context.Background(), `UPDATE bookmarks SET url=?, embedding=?, embedding_dim=?, embedding_model=? WHERE id=?`, "https://other.example.com/item", []byte(`[0.98,0.02]`), 2, "test", "similar")
	_, _ = a.db.ExecContext(context.Background(), `INSERT INTO collections(id,user_id,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "collection-1", userID, "Research", now.Format(time.RFC3339), now.Format(time.RFC3339))
	_, _ = a.db.ExecContext(context.Background(), `INSERT INTO collection_bookmarks(collection_id,bookmark_id,user_id,added_at) VALUES(?,?,?,?)`, "collection-1", "dupe", userID, now.Format(time.RFC3339))
	_, _ = a.db.ExecContext(context.Background(), `INSERT INTO bookmark_entities(bookmark_id,user_id,entity) VALUES(?,?,?)`, "dupe", userID, "Arivu")
	_, _ = a.db.ExecContext(context.Background(), `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "summary-dupe", "dupe", userID, "Merged summary", "completed", now.Format(time.RFC3339), now.Format(time.RFC3339))

	resp := adminRequest(t, handler, http.MethodGet, "/api/bookmarks/duplicates/detect", "", accessCookie, csrfCookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("duplicates status = %d body=%s", resp.StatusCode, readBody(resp))
	}
	var dupes struct {
		Duplicates []map[string]any `json:"duplicates"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&dupes)
	resp.Body.Close()
	if len(dupes.Duplicates) < 2 {
		t.Fatalf("expected exact and similar duplicate groups, got %#v", dupes.Duplicates)
	}
	var sawExact, sawSimilar bool
	for _, group := range dupes.Duplicates {
		if group["type"] == "exact_url" {
			sawExact = true
		}
		if group["type"] == "similar_content" {
			sawSimilar = true
		}
		if _, hasEmbedding := group["embedding"]; hasEmbedding {
			t.Fatalf("duplicate group leaked embedding: %#v", group)
		}
	}
	if !sawExact || !sawSimilar {
		t.Fatalf("missing duplicate types: %#v", dupes.Duplicates)
	}

	merge := adminRequest(t, handler, http.MethodPost, "/api/bookmarks/merge", `["keep","dupe"]`, accessCookie, csrfCookie)
	if merge.StatusCode != http.StatusOK {
		t.Fatalf("merge status = %d body=%s", merge.StatusCode, readBody(merge))
	}
	merge.Body.Close()
	var count int
	_ = a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM bookmarks WHERE id='dupe'`).Scan(&count)
	if count != 0 {
		t.Fatal("duplicate bookmark was not deleted")
	}
	var description string
	var views int
	_ = a.db.QueryRowContext(context.Background(), `SELECT description,view_count FROM bookmarks WHERE id='keep'`).Scan(&description, &views)
	if description != "Better description" || views != 5 {
		t.Fatalf("metadata not merged: description=%q views=%d", description, views)
	}
	_ = a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM collection_bookmarks WHERE bookmark_id='keep' AND collection_id='collection-1'`).Scan(&count)
	if count != 1 {
		t.Fatal("collection membership was not transferred")
	}
	_ = a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM bookmark_entities WHERE bookmark_id='keep' AND entity='Arivu'`).Scan(&count)
	if count != 1 {
		t.Fatal("entity relationship was not transferred")
	}
	var summary string
	_ = a.db.QueryRowContext(context.Background(), `SELECT one_sentence FROM ai_summaries WHERE bookmark_id='keep'`).Scan(&summary)
	if summary != "Merged summary" {
		t.Fatalf("summary not promoted: %q", summary)
	}
}

func TestSemanticKnowledgeGraphParity(t *testing.T) {
	geminiHTTP := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1beta/models/text-embedding-004:embedContent" {
			t.Fatalf("unexpected Gemini path: %s", r.URL.Path)
		}
		var body struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode Gemini request: %v", err)
		}
		text := ""
		if len(body.Content.Parts) > 0 {
			text = body.Content.Parts[0].Text
		}
		values := []float64{1, 0}
		if strings.Contains(strings.ToLower(text), "orthogonal") {
			values = []float64{0, 1}
		}
		return jsonResponse(http.StatusOK, map[string]any{"embedding": map[string]any{"values": values}}), nil
	})}

	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		SignupEnabled:  true,
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	a.bookmarks = bookmarksvc.New(a.db, a.jobs, a.fetcher, providers.GeminiClient{APIKey: "test-key", BaseURL: "https://gemini.example.test", HTTP: geminiHTTP})
	handler := a.Handler()
	accessCookie, csrfCookie := signupForCookies(t, handler, "graph@example.com")
	userID := userIDForEmail(t, a, "graph@example.com")
	now := time.Now().UTC()
	insertBookmarkForTest(t, a, userID, "source", "SQLite Vector Search", now.AddDate(0, 0, -4), now.AddDate(0, 0, -3), 1, 4)
	insertBookmarkForTest(t, a, userID, "close", "Embedding Index Design", now.AddDate(0, 0, -3), now.AddDate(0, 0, -2), 1, 5)
	insertBookmarkForTest(t, a, userID, "far", "Cooking Notes", now.AddDate(0, 0, -2), now.AddDate(0, 0, -1), 0, 2)
	_, _ = a.db.ExecContext(context.Background(), `UPDATE bookmarks SET description=?,text_content=?,embedding=?,embedding_dim=?,embedding_model=? WHERE id=?`, "Vector database ranking", "SQLite embeddings and vector search", []byte(`[1,0]`), 2, "test", "source")
	_, _ = a.db.ExecContext(context.Background(), `UPDATE bookmarks SET description=?,text_content=?,embedding=?,embedding_dim=?,embedding_model=? WHERE id=?`, "ANN index ranking", "Embedding search and graph retrieval", []byte(`[0.96,0.04]`), 2, "test", "close")
	_, _ = a.db.ExecContext(context.Background(), `UPDATE bookmarks SET description=?,text_content=?,embedding=?,embedding_dim=?,embedding_model=? WHERE id=?`, "Kitchen reference", "Recipe timing and ingredients", []byte(`[0,1]`), 2, "test", "far")
	for _, row := range []struct {
		BookmarkID string
		Entity     string
		Concept    string
	}{
		{"source", "SQLite", "Vector Search"},
		{"source", "Embeddings", "Knowledge Graph"},
		{"close", "Embeddings", "Vector Search"},
		{"far", "Cooking", "Recipes"},
	} {
		if row.Entity != "" {
			_, _ = a.db.ExecContext(context.Background(), `INSERT INTO bookmark_entities(bookmark_id,user_id,entity) VALUES(?,?,?)`, row.BookmarkID, userID, row.Entity)
		}
		if row.Concept != "" {
			_, _ = a.db.ExecContext(context.Background(), `INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES(?,?,?)`, row.BookmarkID, userID, row.Concept)
		}
	}

	related := adminRequest(t, handler, http.MethodGet, "/api/bookmarks/source/related?limit=2", "", accessCookie, csrfCookie)
	if related.StatusCode != http.StatusOK {
		t.Fatalf("related status = %d body=%s", related.StatusCode, readBody(related))
	}
	var relatedBody struct {
		Related []map[string]any `json:"related"`
	}
	_ = json.NewDecoder(related.Body).Decode(&relatedBody)
	related.Body.Close()
	if len(relatedBody.Related) != 1 || relatedBody.Related[0]["id"] != "close" {
		t.Fatalf("expected close as only related result, got %#v", relatedBody.Related)
	}
	if relatedBody.Related[0]["similarity_score"].(float64) < 0.9 {
		t.Fatalf("related similarity too low: %#v", relatedBody.Related[0])
	}

	explore := adminRequest(t, handler, http.MethodGet, "/api/knowledge-graph/explore?limit=10", "", accessCookie, csrfCookie)
	if explore.StatusCode != http.StatusOK {
		t.Fatalf("explore status = %d body=%s", explore.StatusCode, readBody(explore))
	}
	var exploreBody map[string]any
	_ = json.NewDecoder(explore.Body).Decode(&exploreBody)
	explore.Body.Close()
	if exploreBody["total_bookmarks"].(float64) != 3 || exploreBody["total_entities"].(float64) != 3 {
		t.Fatalf("unexpected graph totals: %#v", exploreBody)
	}
	relatedGraph := exploreBody["related_bookmarks"].(map[string]any)
	sourceRelated := relatedGraph["source"].([]any)
	if len(sourceRelated) == 0 || sourceRelated[0].([]any)[0] != "close" {
		t.Fatalf("graph related did not rank close first: %#v", relatedGraph)
	}

	search := adminRequest(t, handler, http.MethodGet, "/api/knowledge-graph/search?query=vector%20database&limit=2", "", accessCookie, csrfCookie)
	if search.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d body=%s", search.StatusCode, readBody(search))
	}
	var searchBody struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.NewDecoder(search.Body).Decode(&searchBody)
	search.Body.Close()
	if len(searchBody.Results) < 2 || searchBody.Results[0]["id"] != "source" || searchBody.Results[1]["id"] != "close" {
		t.Fatalf("semantic search ranking mismatch: %#v", searchBody.Results)
	}

	expanded := adminRequest(t, handler, http.MethodGet, "/api/knowledge-graph/expand-query?query=sqlite&max_expansions=5", "", accessCookie, csrfCookie)
	if expanded.StatusCode != http.StatusOK {
		t.Fatalf("expand status = %d body=%s", expanded.StatusCode, readBody(expanded))
	}
	var expandedBody struct {
		RelatedEntities []string         `json:"related_entities"`
		RelatedConcepts []string         `json:"related_concepts"`
		Expansions      []map[string]any `json:"expansions"`
	}
	_ = json.NewDecoder(expanded.Body).Decode(&expandedBody)
	expanded.Body.Close()
	if !containsString(expandedBody.RelatedEntities, "SQLite") || !containsString(expandedBody.RelatedConcepts, "Vector Search") || len(expandedBody.Expansions) == 0 {
		t.Fatalf("query expansion missing direct and co-occurring terms: %#v", expandedBody)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func requestForm(t *testing.T, req *http.Request) url.Values {
	t.Helper()
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func jsonResponse(status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}

func signupForCookies(t *testing.T, handler http.Handler, email string) (*http.Cookie, *http.Cookie) {
	t.Helper()
	signupReq := httptest.NewRequest(http.MethodPost, "/api/auth/signup", strings.NewReader(`{"email":"`+email+`","password":"correct horse battery staple"}`))
	signupReq.Header.Set("Content-Type", "application/json")
	signupRec := httptest.NewRecorder()
	handler.ServeHTTP(signupRec, signupReq)
	resp := signupRec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signup status = %d body=%s", resp.StatusCode, readBody(resp))
	}
	var accessCookie, csrfCookie *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "access_token" {
			accessCookie = cookie
		}
		if cookie.Name == "csrf_token" {
			csrfCookie = cookie
		}
	}
	if accessCookie == nil || csrfCookie == nil {
		t.Fatalf("expected access and csrf cookies, got %#v", resp.Cookies())
	}
	return accessCookie, csrfCookie
}

func adminRequest(t *testing.T, handler http.Handler, method string, path string, body string, accessCookie, csrfCookie *http.Cookie) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.AddCookie(accessCookie)
	req.AddCookie(csrfCookie)
	req.Header.Set("X-CSRF-Token", csrfCookie.Value)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Result()
}

func bearerRequest(t *testing.T, handler http.Handler, method string, path string, body string, token string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Result()
}

func frontendRequest(t *testing.T, handler http.Handler, path string, etag string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Result()
}

func bodyToken(t *testing.T, handler http.Handler, method string, path string, body string, token string) string {
	t.Helper()
	resp := bearerRequest(t, handler, method, path, body, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token request status = %d body=%s", resp.StatusCode, readBody(resp))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	accessToken, _ := out["access_token"].(string)
	if accessToken == "" {
		t.Fatalf("missing access token: %#v", out)
	}
	return accessToken
}

func readBody(resp *http.Response) string {
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return string(data)
}

func userIDForEmail(t *testing.T, a *App, email string) string {
	t.Helper()
	var id string
	if err := a.db.QueryRowContext(context.Background(), `SELECT id FROM users WHERE email=?`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertBookmarkForTest(t *testing.T, a *App, userID, id, title string, createdAt, lastAccessed time.Time, viewCount, readingTime int) {
	t.Helper()
	_, err := a.db.ExecContext(context.Background(), `INSERT INTO bookmarks(id,user_id,url,title,domain,reading_time,read_status,created_at,updated_at,last_accessed,view_count) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, userID, "https://example.com/"+id, title, "example.com", readingTime, false, createdAt.Format(time.RFC3339), createdAt.Format(time.RFC3339), lastAccessed.Format(time.RFC3339), viewCount)
	if err != nil {
		t.Fatal(err)
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
