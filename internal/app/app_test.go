package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bookmarksvc "github.com/glnarayanan/arivu/internal/bookmarks"
	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/secrets"
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
	if resp := bearerRequest(t, handler, http.MethodPost, "/api/extension/bookmarks", `{"url":"https://example.com/extension","title":"Extension Capture","annotation":"Selected passage"}`, extensionToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("extension scoped bookmark status = %d body=%s", resp.StatusCode, readBody(resp))
	} else {
		resp.Body.Close()
	}
	userID := userIDForEmail(t, a, "admin@example.com")
	var quote, title string
	if err := a.db.QueryRowContext(context.Background(), `SELECT a.quote,b.title FROM annotations a JOIN bookmarks b ON b.id=a.bookmark_id AND b.user_id=a.user_id WHERE a.user_id=? AND b.url=?`, userID, "https://example.com/extension").Scan(&quote, &title); err != nil {
		t.Fatalf("extension selected-text annotation missing: %v", err)
	}
	if quote != "Selected passage" {
		t.Fatalf("extension quote = %q", quote)
	}
	if title != "Extension Capture" {
		t.Fatalf("extension title = %q", title)
	}
}

func TestProfileRoutesSupportSettingsPanel(t *testing.T) {
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
	accessCookie, csrfCookie := signupForCookies(t, handler, "profile@example.com")

	update := adminRequest(t, handler, http.MethodPut, "/api/user/profile", `{"name":"Profile User"}`, accessCookie, csrfCookie)
	if update.StatusCode != http.StatusOK {
		t.Fatalf("profile update status = %d body=%s", update.StatusCode, readBody(update))
	}
	update.Body.Close()
	profile := adminRequest(t, handler, http.MethodGet, "/api/user/profile", "", accessCookie, csrfCookie)
	if profile.StatusCode != http.StatusOK {
		t.Fatalf("profile get status = %d body=%s", profile.StatusCode, readBody(profile))
	}
	var body map[string]any
	_ = json.NewDecoder(profile.Body).Decode(&body)
	profile.Body.Close()
	if body["email"] != "profile@example.com" || body["name"] != "Profile User" {
		t.Fatalf("unexpected profile body: %#v", body)
	}
	change := adminRequest(t, handler, http.MethodPost, "/api/auth/change-password", `{"current_password":"correct horse battery staple","new_password":"new-password-123"}`, accessCookie, csrfCookie)
	if change.StatusCode != http.StatusOK {
		t.Fatalf("change password status = %d body=%s", change.StatusCode, readBody(change))
	}
	change.Body.Close()
	assertAuditAction(t, a, "auth.password.change", "user", userIDForEmail(t, a, "profile@example.com"))
}

func TestCollectionMembershipIsUserScoped(t *testing.T) {
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
	ownerAccess, ownerCSRF := signupForCookies(t, handler, "owner@example.com")
	otherAccess, otherCSRF := signupForCookies(t, handler, "other-collections@example.com")
	ownerID := userIDForEmail(t, a, "owner@example.com")
	insertBookmarkForTest(t, a, ownerID, "owner-bookmark", "Owner Bookmark", time.Now().AddDate(0, 0, -1), time.Now().AddDate(0, 0, -1), 0, 1)

	ownerCollectionResp := adminRequest(t, handler, http.MethodPost, "/api/collections", `{"name":"Owner collection"}`, ownerAccess, ownerCSRF)
	if ownerCollectionResp.StatusCode != http.StatusOK {
		t.Fatalf("owner collection status = %d body=%s", ownerCollectionResp.StatusCode, readBody(ownerCollectionResp))
	}
	var ownerCollection map[string]any
	_ = json.NewDecoder(ownerCollectionResp.Body).Decode(&ownerCollection)
	ownerCollectionResp.Body.Close()
	ownerCollectionID, _ := ownerCollection["id"].(string)

	createWithOtherCollection := adminRequest(t, handler, http.MethodPost, "/api/bookmarks", `{"url":"https://example.com/cross-create","collection_id":"`+ownerCollectionID+`"}`, otherAccess, otherCSRF)
	if createWithOtherCollection.StatusCode != http.StatusNotFound {
		t.Fatalf("cross collection create status = %d body=%s", createWithOtherCollection.StatusCode, readBody(createWithOtherCollection))
	}
	createWithOtherCollection.Body.Close()
	var leakedBookmark int
	_ = a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM bookmarks WHERE url='https://example.com/cross-create'`).Scan(&leakedBookmark)
	if leakedBookmark != 0 {
		t.Fatal("rejected cross-collection create left a bookmark row")
	}

	otherCollectionResp := adminRequest(t, handler, http.MethodPost, "/api/collections", `{"name":"Other collection"}`, otherAccess, otherCSRF)
	if otherCollectionResp.StatusCode != http.StatusOK {
		t.Fatalf("other collection status = %d body=%s", otherCollectionResp.StatusCode, readBody(otherCollectionResp))
	}
	var otherCollection map[string]any
	_ = json.NewDecoder(otherCollectionResp.Body).Decode(&otherCollection)
	otherCollectionResp.Body.Close()
	otherCollectionID, _ := otherCollection["id"].(string)

	addOtherBookmark := adminRequest(t, handler, http.MethodPost, "/api/collections/"+otherCollectionID+"/add", `{"bookmark_id":"owner-bookmark"}`, otherAccess, otherCSRF)
	if addOtherBookmark.StatusCode != http.StatusNotFound {
		t.Fatalf("cross bookmark add status = %d body=%s", addOtherBookmark.StatusCode, readBody(addOtherBookmark))
	}
	addOtherBookmark.Body.Close()

	extensionResp := adminRequest(t, handler, http.MethodPost, "/api/auth/extension-token", "", otherAccess, otherCSRF)
	if extensionResp.StatusCode != http.StatusOK {
		t.Fatalf("extension token status = %d body=%s", extensionResp.StatusCode, readBody(extensionResp))
	}
	var extensionBody map[string]any
	_ = json.NewDecoder(extensionResp.Body).Decode(&extensionBody)
	extensionResp.Body.Close()
	extensionToken, _ := extensionBody["access_token"].(string)
	extensionCreate := bearerRequest(t, handler, http.MethodPost, "/api/extension/bookmarks", `{"url":"https://example.com/extension-cross","collection_id":"`+ownerCollectionID+`"}`, extensionToken)
	if extensionCreate.StatusCode != http.StatusNotFound {
		t.Fatalf("extension cross collection create status = %d body=%s", extensionCreate.StatusCode, readBody(extensionCreate))
	}
	extensionCreate.Body.Close()
}

func TestAuthRateLimitsSensitiveEndpoints(t *testing.T) {
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
	accessCookie, _ := signupForCookies(t, handler, "limited@example.com")
	if accessCookie == nil {
		t.Fatal("signup did not create a session")
	}

	for i := 0; i < 10; i++ {
		resp := publicJSONRequest(t, handler, http.MethodPost, "/api/auth/login", `{"email":"limited@example.com","password":"wrong password"}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("bad login %d status = %d body=%s", i, resp.StatusCode, readBody(resp))
		}
		resp.Body.Close()
	}
	limitedLogin := publicJSONRequest(t, handler, http.MethodPost, "/api/auth/login", `{"email":"limited@example.com","password":"wrong password"}`)
	if limitedLogin.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("limited login status = %d body=%s", limitedLogin.StatusCode, readBody(limitedLogin))
	}
	limitedLogin.Body.Close()

	for i := 0; i < 3; i++ {
		resp := publicJSONRequest(t, handler, http.MethodPost, "/api/auth/forgot-password", `{"email":"limited@example.com"}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("forgot password %d status = %d body=%s", i, resp.StatusCode, readBody(resp))
		}
		resp.Body.Close()
	}
	limitedForgot := publicJSONRequest(t, handler, http.MethodPost, "/api/auth/forgot-password", `{"email":"limited@example.com"}`)
	if limitedForgot.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("limited forgot status = %d body=%s", limitedForgot.StatusCode, readBody(limitedForgot))
	}
	limitedForgot.Body.Close()

	for i := 0; i < 10; i++ {
		resp := publicJSONRequest(t, handler, http.MethodPost, "/api/auth/reset-password", `{"token":"bad-token","new_password":"correct horse battery staple"}`)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("reset password %d status = %d body=%s", i, resp.StatusCode, readBody(resp))
		}
		resp.Body.Close()
	}
	limitedReset := publicJSONRequest(t, handler, http.MethodPost, "/api/auth/reset-password", `{"token":"bad-token","new_password":"correct horse battery staple"}`)
	if limitedReset.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("limited reset status = %d body=%s", limitedReset.StatusCode, readBody(limitedReset))
	}
	limitedReset.Body.Close()

	_, _ = a.db.ExecContext(context.Background(), `DELETE FROM rate_limits`)
	userID := userIDForEmail(t, a, "limited@example.com")
	resetToken := "valid-reset-token"
	sum := sha256.Sum256([]byte(resetToken))
	now := time.Now().UTC()
	if _, err := a.db.ExecContext(context.Background(), `INSERT INTO password_reset_tokens(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)`, hex.EncodeToString(sum[:]), userID, now.Add(time.Hour).Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert reset token: %v", err)
	}
	reset := publicJSONRequest(t, handler, http.MethodPost, "/api/auth/reset-password", `{"token":"valid-reset-token","new_password":"reset-password-123"}`)
	if reset.StatusCode != http.StatusOK {
		t.Fatalf("valid reset status = %d body=%s", reset.StatusCode, readBody(reset))
	}
	reset.Body.Close()
	assertAuditAction(t, a, "auth.password.reset", "user", userID)
}

func TestMutationQuotasAreUserAndAudienceScoped(t *testing.T) {
	oldBookmarkCreate := quotaBookmarkCreate
	oldBookmarkImport := quotaBookmarkImport
	oldNotesWrite := quotaNotesWrite
	quotaBookmarkCreate = mutationQuota{name: "bookmarks.create", limit: 1, window: time.Hour}
	quotaBookmarkImport = mutationQuota{name: "bookmarks.import", limit: 1, window: time.Hour}
	quotaNotesWrite = mutationQuota{name: "notes.write", limit: 1, window: time.Hour}
	t.Cleanup(func() {
		quotaBookmarkCreate = oldBookmarkCreate
		quotaBookmarkImport = oldBookmarkImport
		quotaNotesWrite = oldNotesWrite
	})

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
	accessCookie, csrfCookie := signupForCookies(t, handler, "quota@example.com")
	otherAccess, otherCSRF := signupForCookies(t, handler, "quota-other@example.com")

	firstNote := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"First"}`, accessCookie, csrfCookie)
	if firstNote.StatusCode != http.StatusOK {
		t.Fatalf("first note status = %d body=%s", firstNote.StatusCode, readBody(firstNote))
	}
	firstNote.Body.Close()
	secondNote := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"Second"}`, accessCookie, csrfCookie)
	if secondNote.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("limited note status = %d body=%s", secondNote.StatusCode, readBody(secondNote))
	}
	secondNote.Body.Close()
	otherNote := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"Other user"}`, otherAccess, otherCSRF)
	if otherNote.StatusCode != http.StatusOK {
		t.Fatalf("other user note status = %d body=%s", otherNote.StatusCode, readBody(otherNote))
	}
	otherNote.Body.Close()
	_, _ = a.db.ExecContext(context.Background(), `DELETE FROM rate_limits`)
	afterReset := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"After reset"}`, accessCookie, csrfCookie)
	if afterReset.StatusCode != http.StatusOK {
		t.Fatalf("note after rate limit reset status = %d body=%s", afterReset.StatusCode, readBody(afterReset))
	}
	afterReset.Body.Close()

	_, _ = a.db.ExecContext(context.Background(), `DELETE FROM rate_limits`)
	webBookmark := adminRequest(t, handler, http.MethodPost, "/api/bookmarks", `{"url":"https://example.com/web-quota-1"}`, accessCookie, csrfCookie)
	if webBookmark.StatusCode != http.StatusOK {
		t.Fatalf("web bookmark status = %d body=%s", webBookmark.StatusCode, readBody(webBookmark))
	}
	webBookmark.Body.Close()
	limitedWebBookmark := adminRequest(t, handler, http.MethodPost, "/api/bookmarks", `{"url":"https://example.com/web-quota-2"}`, accessCookie, csrfCookie)
	if limitedWebBookmark.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("limited web bookmark status = %d body=%s", limitedWebBookmark.StatusCode, readBody(limitedWebBookmark))
	}
	limitedWebBookmark.Body.Close()
	extensionResp := adminRequest(t, handler, http.MethodPost, "/api/auth/extension-token", "", accessCookie, csrfCookie)
	if extensionResp.StatusCode != http.StatusOK {
		t.Fatalf("extension token status = %d body=%s", extensionResp.StatusCode, readBody(extensionResp))
	}
	var extensionBody map[string]any
	_ = json.NewDecoder(extensionResp.Body).Decode(&extensionBody)
	extensionResp.Body.Close()
	extensionToken, _ := extensionBody["access_token"].(string)
	extensionBookmark := bearerRequest(t, handler, http.MethodPost, "/api/extension/bookmarks", `{"url":"https://example.com/extension-quota"}`, extensionToken)
	if extensionBookmark.StatusCode != http.StatusOK {
		t.Fatalf("extension bookmark separate audience status = %d body=%s", extensionBookmark.StatusCode, readBody(extensionBookmark))
	}
	extensionBookmark.Body.Close()

	_, _ = a.db.ExecContext(context.Background(), `DELETE FROM rate_limits`)
	importBody := `<!doctype NETSCAPE-Bookmark-file-1><DT><A HREF="https://example.com/import-quota-1">A</A>`
	firstImport := adminRequest(t, handler, http.MethodPost, "/api/bookmarks/import", importBody, accessCookie, csrfCookie)
	if firstImport.StatusCode != http.StatusOK {
		t.Fatalf("first import status = %d body=%s", firstImport.StatusCode, readBody(firstImport))
	}
	firstImport.Body.Close()
	var rowsBefore int
	_ = a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM bookmarks WHERE url='https://example.com/import-quota-2'`).Scan(&rowsBefore)
	limitedImport := adminRequest(t, handler, http.MethodPost, "/api/bookmarks/import", `<!doctype NETSCAPE-Bookmark-file-1><DT><A HREF="https://example.com/import-quota-2">B</A>`, accessCookie, csrfCookie)
	if limitedImport.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("limited import status = %d body=%s", limitedImport.StatusCode, readBody(limitedImport))
	}
	limitedImport.Body.Close()
	var rowsAfter int
	_ = a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM bookmarks WHERE url='https://example.com/import-quota-2'`).Scan(&rowsAfter)
	if rowsBefore != rowsAfter {
		t.Fatalf("limited import created rows before handler ran: before=%d after=%d", rowsBefore, rowsAfter)
	}
}

func TestExtensionPopupCapturesNoteAndTags(t *testing.T) {
	html, err := os.ReadFile(filepath.Join("..", "..", "extension", "popup.html"))
	if err != nil {
		t.Fatalf("read popup html: %v", err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "extension", "popup.js"))
	if err != nil {
		t.Fatalf("read popup js: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join("..", "..", "extension", "manifest.json"))
	if err != nil {
		t.Fatalf("read extension manifest: %v", err)
	}
	background, err := os.ReadFile(filepath.Join("..", "..", "extension", "background.js"))
	if err != nil {
		t.Fatalf("read extension background: %v", err)
	}
	for _, expected := range []string{`id="note"`, `id="tags"`, `id="settingsStatus"`, `src="url-utils.js"`} {
		if !strings.Contains(string(html), expected) {
			t.Fatalf("extension popup missing %s", expected)
		}
	}
	source := string(script)
	for _, expected := range []string{"function splitTags", "payload.title = title", "payload.note = note", "payload.tags = tags", "Saved to Inbox", "Open Inbox", "Open Item", "replaceChildren", "ensureApiPermission", "configureApiOrigin", "ArivuExtensionURL.normalizeApiUrl"} {
		if !strings.Contains(source, expected) {
			t.Fatalf("extension popup script missing %s", expected)
		}
	}
	if !strings.Contains(string(manifest), `"optional_host_permissions"`) || !strings.Contains(string(manifest), `"scripting"`) {
		t.Fatal("extension manifest missing self-hosted permission support")
	}
	if !strings.Contains(string(background), "registerCustomApiContentScript") || !strings.Contains(string(background), "ArivuExtensionURL.senderOriginAllowed") {
		t.Fatal("extension background missing dynamic content script registration")
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
	if _, err := a.db.ExecContext(context.Background(), `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,bullet_points_json,highlights_json,suggested_tags_json,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "summary-capture", "capture", userID, "The capture loop needs review.", `["Save with context","Recall with evidence"]`, `["Recall with evidence"]`, `["Second Brain"]`, "completed", now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatalf("seed summary: %v", err)
	}

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
	missingActionCSRF := httptest.NewRequest(http.MethodPost, "/api/action-items", strings.NewReader(`{"item_type":"bookmark","item_id":"capture","title":"No CSRF"}`))
	missingActionCSRF.Header.Set("Content-Type", "application/json")
	missingActionCSRF.AddCookie(accessCookie)
	missingActionRec := httptest.NewRecorder()
	handler.ServeHTTP(missingActionRec, missingActionCSRF)
	missingActionResp := missingActionRec.Result()
	if missingActionResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("action item without csrf status = %d body=%s", missingActionResp.StatusCode, readBody(missingActionResp))
	}
	missingActionResp.Body.Close()

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

	standaloneResp := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"Standalone","body":"Loose idea"}`, accessCookie, csrfCookie)
	if standaloneResp.StatusCode != http.StatusOK {
		t.Fatalf("create standalone note status = %d body=%s", standaloneResp.StatusCode, readBody(standaloneResp))
	}
	var standaloneBody struct {
		Note map[string]any `json:"note"`
	}
	_ = json.NewDecoder(standaloneResp.Body).Decode(&standaloneBody)
	standaloneResp.Body.Close()
	standaloneID, _ := standaloneBody.Note["id"].(string)
	if standaloneID == "" || standaloneBody.Note["bookmark_id"] != nil {
		t.Fatalf("unexpected standalone note: %#v", standaloneBody)
	}
	updateStandalone := adminRequest(t, handler, http.MethodPatch, "/api/notes/"+standaloneID, `{"title":"Updated standalone","body":"Sharper idea"}`, accessCookie, csrfCookie)
	if updateStandalone.StatusCode != http.StatusOK {
		t.Fatalf("update standalone note status = %d body=%s", updateStandalone.StatusCode, readBody(updateStandalone))
	}
	updateStandalone.Body.Close()
	deleteStandalone := adminRequest(t, handler, http.MethodDelete, "/api/notes/"+standaloneID, "", accessCookie, csrfCookie)
	if deleteStandalone.StatusCode != http.StatusOK {
		t.Fatalf("delete standalone note status = %d body=%s", deleteStandalone.StatusCode, readBody(deleteStandalone))
	}
	deleteStandalone.Body.Close()

	searchNoteResp := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"Recall field note","body":"Standalone recall idea for later synthesis."}`, accessCookie, csrfCookie)
	if searchNoteResp.StatusCode != http.StatusOK {
		t.Fatalf("create searchable standalone note status = %d body=%s", searchNoteResp.StatusCode, readBody(searchNoteResp))
	}
	var searchNoteBody struct {
		Note map[string]any `json:"note"`
	}
	_ = json.NewDecoder(searchNoteResp.Body).Decode(&searchNoteBody)
	searchNoteResp.Body.Close()
	searchNoteID, _ := searchNoteBody.Note["id"].(string)
	if searchNoteID == "" {
		t.Fatalf("searchable note missing id: %#v", searchNoteBody)
	}
	_, _ = a.db.ExecContext(context.Background(), `UPDATE notes SET created_at=?,updated_at=? WHERE id=?`, now.AddDate(0, 0, -3).Format(time.RFC3339), now.AddDate(0, 0, -3).Format(time.RFC3339), searchNoteID)
	otherNoteResp := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"Other recall note","body":"Standalone recall idea from another user."}`, otherAccess, otherCSRF)
	if otherNoteResp.StatusCode != http.StatusOK {
		t.Fatalf("create other note status = %d body=%s", otherNoteResp.StatusCode, readBody(otherNoteResp))
	}
	var otherNoteBody struct {
		Note map[string]any `json:"note"`
	}
	_ = json.NewDecoder(otherNoteResp.Body).Decode(&otherNoteBody)
	otherNoteResp.Body.Close()
	otherNoteID, _ := otherNoteBody.Note["id"].(string)
	if otherNoteID == "" {
		t.Fatalf("other note missing id: %#v", otherNoteBody)
	}
	snoozeNoteResp := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"Snooze recall note","body":"Standalone recall idea to defer."}`, accessCookie, csrfCookie)
	if snoozeNoteResp.StatusCode != http.StatusOK {
		t.Fatalf("create snooze note status = %d body=%s", snoozeNoteResp.StatusCode, readBody(snoozeNoteResp))
	}
	var snoozeNoteBody struct {
		Note map[string]any `json:"note"`
	}
	_ = json.NewDecoder(snoozeNoteResp.Body).Decode(&snoozeNoteBody)
	snoozeNoteResp.Body.Close()
	snoozeNoteID, _ := snoozeNoteBody.Note["id"].(string)
	if snoozeNoteID == "" {
		t.Fatalf("snooze note missing id: %#v", snoozeNoteBody)
	}
	_, _ = a.db.ExecContext(context.Background(), `UPDATE notes SET created_at=?,updated_at=? WHERE id=?`, now.AddDate(0, 0, -4).Format(time.RFC3339), now.AddDate(0, 0, -4).Format(time.RFC3339), snoozeNoteID)

	inboxResp := adminRequest(t, handler, http.MethodGet, "/api/inbox?stage=inbox", "", accessCookie, csrfCookie)
	if inboxResp.StatusCode != http.StatusOK {
		t.Fatalf("inbox status = %d body=%s", inboxResp.StatusCode, readBody(inboxResp))
	}
	var inboxBody struct {
		Items  []map[string]any `json:"items"`
		Counts map[string]int   `json:"counts"`
	}
	_ = json.NewDecoder(inboxResp.Body).Decode(&inboxBody)
	inboxResp.Body.Close()
	var sawCaptureInbox, sawSearchNoteInbox bool
	for _, item := range inboxBody.Items {
		if item["id"] == "capture" && item["item_type"] == "bookmark" && item["stage"] == "inbox" {
			sawCaptureInbox = true
		}
		if item["id"] == searchNoteID && item["item_type"] == "note" && item["stage"] == "inbox" {
			sawSearchNoteInbox = true
		}
		if item["id"] == otherNoteID {
			t.Fatalf("inbox leaked other user's note: %#v", inboxBody)
		}
	}
	if !sawCaptureInbox || !sawSearchNoteInbox || inboxBody.Counts["inbox"] < 3 {
		t.Fatalf("unexpected inbox body: %#v", inboxBody)
	}
	updateInboxResp := adminRequest(t, handler, http.MethodPatch, "/api/inbox/note:"+searchNoteID, `{"stage":"processing","importance":4,"next_action":"Synthesize into the recall project."}`, accessCookie, csrfCookie)
	if updateInboxResp.StatusCode != http.StatusOK {
		t.Fatalf("update inbox status = %d body=%s", updateInboxResp.StatusCode, readBody(updateInboxResp))
	}
	updateInboxResp.Body.Close()
	badInboxResp := adminRequest(t, handler, http.MethodPatch, "/api/inbox/note:"+otherNoteID, `{"stage":"processed"}`, accessCookie, csrfCookie)
	if badInboxResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user inbox update status = %d body=%s", badInboxResp.StatusCode, readBody(badInboxResp))
	}
	badInboxResp.Body.Close()

	linkResp := adminRequest(t, handler, http.MethodPost, "/api/links", `{"from_type":"bookmark","from_id":"capture","to_type":"note","to_id":"`+searchNoteID+`","label":"supports"}`, accessCookie, csrfCookie)
	if linkResp.StatusCode != http.StatusOK {
		t.Fatalf("create link status = %d body=%s", linkResp.StatusCode, readBody(linkResp))
	}
	var linkBody struct {
		Link map[string]any `json:"link"`
	}
	_ = json.NewDecoder(linkResp.Body).Decode(&linkBody)
	linkResp.Body.Close()
	linkID, _ := linkBody.Link["id"].(string)
	if linkID == "" || linkBody.Link["to_title"] != "Recall field note" {
		t.Fatalf("unexpected link body: %#v", linkBody)
	}
	noteLinkResp := adminRequest(t, handler, http.MethodPost, "/api/links", `{"from_type":"note","from_id":"`+searchNoteID+`","to_type":"note","to_id":"`+noteID+`","label":"extends"}`, accessCookie, csrfCookie)
	if noteLinkResp.StatusCode != http.StatusOK {
		t.Fatalf("create note link status = %d body=%s", noteLinkResp.StatusCode, readBody(noteLinkResp))
	}
	var noteLinkBody struct {
		Link map[string]any `json:"link"`
	}
	_ = json.NewDecoder(noteLinkResp.Body).Decode(&noteLinkBody)
	noteLinkResp.Body.Close()
	noteLinkID, _ := noteLinkBody.Link["id"].(string)
	if noteLinkID == "" || noteLinkBody.Link["from_title"] != "Recall field note" || noteLinkBody.Link["to_title"] != "Research note" {
		t.Fatalf("unexpected note link body: %#v", noteLinkBody)
	}
	missingLinkCSRF := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(`{"from_type":"bookmark","from_id":"capture","to_type":"note","to_id":"`+searchNoteID+`"}`))
	missingLinkCSRF.Header.Set("Content-Type", "application/json")
	missingLinkCSRF.AddCookie(accessCookie)
	missingLinkRec := httptest.NewRecorder()
	handler.ServeHTTP(missingLinkRec, missingLinkCSRF)
	missingLinkResp := missingLinkRec.Result()
	if missingLinkResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("link create without csrf status = %d body=%s", missingLinkResp.StatusCode, readBody(missingLinkResp))
	}
	missingLinkResp.Body.Close()
	crossLinkResp := adminRequest(t, handler, http.MethodPost, "/api/links", `{"from_type":"bookmark","from_id":"capture","to_type":"note","to_id":"`+otherNoteID+`"}`, accessCookie, csrfCookie)
	if crossLinkResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user link status = %d body=%s", crossLinkResp.StatusCode, readBody(crossLinkResp))
	}
	crossLinkResp.Body.Close()
	crossLinksResp := adminRequest(t, handler, http.MethodGet, "/api/links?item=note:"+otherNoteID, "", accessCookie, csrfCookie)
	if crossLinksResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user links read status = %d body=%s", crossLinksResp.StatusCode, readBody(crossLinksResp))
	}
	crossLinksResp.Body.Close()
	linksResp := adminRequest(t, handler, http.MethodGet, "/api/links?item=bookmark:capture", "", accessCookie, csrfCookie)
	if linksResp.StatusCode != http.StatusOK {
		t.Fatalf("links status = %d body=%s", linksResp.StatusCode, readBody(linksResp))
	}
	var linksBody struct {
		Outgoing []map[string]any `json:"outgoing"`
		Incoming []map[string]any `json:"incoming"`
	}
	_ = json.NewDecoder(linksResp.Body).Decode(&linksBody)
	linksResp.Body.Close()
	if len(linksBody.Outgoing) != 1 || linksBody.Outgoing[0]["id"] != linkID || linksBody.Outgoing[0]["to_id"] != searchNoteID {
		t.Fatalf("unexpected links body: %#v", linksBody)
	}
	noteDetailResp := adminRequest(t, handler, http.MethodGet, "/api/notes/"+searchNoteID, "", accessCookie, csrfCookie)
	if noteDetailResp.StatusCode != http.StatusOK {
		t.Fatalf("note detail status = %d body=%s", noteDetailResp.StatusCode, readBody(noteDetailResp))
	}
	var noteDetail map[string]any
	_ = json.NewDecoder(noteDetailResp.Body).Decode(&noteDetail)
	noteDetailResp.Body.Close()
	if links, _ := noteDetail["links"].(map[string]any); len(links["outgoing"].([]any)) != 1 || len(links["incoming"].([]any)) != 1 {
		t.Fatalf("note detail missing links: %#v", noteDetail["links"])
	}
	missingDeleteLinkCSRF := httptest.NewRequest(http.MethodDelete, "/api/links/"+noteLinkID, nil)
	missingDeleteLinkCSRF.AddCookie(accessCookie)
	missingDeleteLinkRec := httptest.NewRecorder()
	handler.ServeHTTP(missingDeleteLinkRec, missingDeleteLinkCSRF)
	missingDeleteLinkResp := missingDeleteLinkRec.Result()
	if missingDeleteLinkResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("link delete without csrf status = %d body=%s", missingDeleteLinkResp.StatusCode, readBody(missingDeleteLinkResp))
	}
	missingDeleteLinkResp.Body.Close()
	crossDeleteLinkResp := adminRequest(t, handler, http.MethodDelete, "/api/links/"+noteLinkID, "", otherAccess, otherCSRF)
	if crossDeleteLinkResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user link delete status = %d body=%s", crossDeleteLinkResp.StatusCode, readBody(crossDeleteLinkResp))
	}
	crossDeleteLinkResp.Body.Close()
	cleanupSourceResp := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"Cleanup source","body":"Temporary source"}`, accessCookie, csrfCookie)
	if cleanupSourceResp.StatusCode != http.StatusOK {
		t.Fatalf("cleanup source note status = %d body=%s", cleanupSourceResp.StatusCode, readBody(cleanupSourceResp))
	}
	var cleanupSourceBody struct {
		Note map[string]any `json:"note"`
	}
	_ = json.NewDecoder(cleanupSourceResp.Body).Decode(&cleanupSourceBody)
	cleanupSourceResp.Body.Close()
	cleanupSourceID, _ := cleanupSourceBody.Note["id"].(string)
	cleanupTargetResp := adminRequest(t, handler, http.MethodPost, "/api/notes", `{"title":"Cleanup target","body":"Temporary target"}`, accessCookie, csrfCookie)
	if cleanupTargetResp.StatusCode != http.StatusOK {
		t.Fatalf("cleanup target note status = %d body=%s", cleanupTargetResp.StatusCode, readBody(cleanupTargetResp))
	}
	var cleanupTargetBody struct {
		Note map[string]any `json:"note"`
	}
	_ = json.NewDecoder(cleanupTargetResp.Body).Decode(&cleanupTargetBody)
	cleanupTargetResp.Body.Close()
	cleanupTargetID, _ := cleanupTargetBody.Note["id"].(string)
	cleanupLinkResp := adminRequest(t, handler, http.MethodPost, "/api/links", `{"from_type":"note","from_id":"`+cleanupSourceID+`","to_type":"note","to_id":"`+cleanupTargetID+`","label":"temporary"}`, accessCookie, csrfCookie)
	if cleanupLinkResp.StatusCode != http.StatusOK {
		t.Fatalf("cleanup link status = %d body=%s", cleanupLinkResp.StatusCode, readBody(cleanupLinkResp))
	}
	cleanupLinkResp.Body.Close()
	deleteCleanupNote := adminRequest(t, handler, http.MethodDelete, "/api/notes/"+cleanupSourceID, "", accessCookie, csrfCookie)
	if deleteCleanupNote.StatusCode != http.StatusOK {
		t.Fatalf("delete cleanup note status = %d body=%s", deleteCleanupNote.StatusCode, readBody(deleteCleanupNote))
	}
	deleteCleanupNote.Body.Close()
	var staleLinks int
	if err := a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM item_links WHERE user_id=? AND (from_id=? OR to_id=?)`, userID, cleanupSourceID, cleanupSourceID).Scan(&staleLinks); err != nil {
		t.Fatalf("count stale note links: %v", err)
	}
	if staleLinks != 0 {
		t.Fatalf("deleted note left item links: %d", staleLinks)
	}
	deleteCleanupTarget := adminRequest(t, handler, http.MethodDelete, "/api/notes/"+cleanupTargetID, "", accessCookie, csrfCookie)
	if deleteCleanupTarget.StatusCode != http.StatusOK {
		t.Fatalf("delete cleanup target note status = %d body=%s", deleteCleanupTarget.StatusCode, readBody(deleteCleanupTarget))
	}
	deleteCleanupTarget.Body.Close()
	reminderDue := now.Add(48 * time.Hour).UTC().Format(time.RFC3339)
	reminderResp := adminRequest(t, handler, http.MethodPost, "/api/reminders", `{"item_type":"bookmark","item_id":"capture","due_at":"`+reminderDue+`","note":"Use this in planning."}`, accessCookie, csrfCookie)
	if reminderResp.StatusCode != http.StatusOK {
		t.Fatalf("create reminder status = %d body=%s", reminderResp.StatusCode, readBody(reminderResp))
	}
	var reminderBody struct {
		Reminder map[string]any `json:"reminder"`
	}
	_ = json.NewDecoder(reminderResp.Body).Decode(&reminderBody)
	reminderResp.Body.Close()
	reminderID, _ := reminderBody.Reminder["id"].(string)
	if reminderID == "" || reminderBody.Reminder["item_title"] != "Capture Loop" {
		t.Fatalf("unexpected reminder body: %#v", reminderBody)
	}
	crossReminderResp := adminRequest(t, handler, http.MethodPost, "/api/reminders", `{"item_type":"note","item_id":"`+otherNoteID+`","due_at":"`+reminderDue+`"}`, accessCookie, csrfCookie)
	if crossReminderResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user reminder status = %d body=%s", crossReminderResp.StatusCode, readBody(crossReminderResp))
	}
	crossReminderResp.Body.Close()
	remindersResp := adminRequest(t, handler, http.MethodGet, "/api/reminders", "", accessCookie, csrfCookie)
	if remindersResp.StatusCode != http.StatusOK {
		t.Fatalf("reminders status = %d body=%s", remindersResp.StatusCode, readBody(remindersResp))
	}
	var remindersBody struct {
		Reminders []map[string]any `json:"reminders"`
	}
	_ = json.NewDecoder(remindersResp.Body).Decode(&remindersBody)
	remindersResp.Body.Close()
	if len(remindersBody.Reminders) != 1 || remindersBody.Reminders[0]["id"] != reminderID || remindersBody.Reminders[0]["note"] != "Use this in planning." {
		t.Fatalf("unexpected reminders body: %#v", remindersBody)
	}
	noteReminderResp := adminRequest(t, handler, http.MethodPost, "/api/reminders", `{"item_type":"note","item_id":"`+searchNoteID+`","due_at":"`+now.Add(96*time.Hour).UTC().Format(time.RFC3339)+`","note":"Bring this note back."}`, accessCookie, csrfCookie)
	if noteReminderResp.StatusCode != http.StatusOK {
		t.Fatalf("create note reminder status = %d body=%s", noteReminderResp.StatusCode, readBody(noteReminderResp))
	}
	var noteReminderBody struct {
		Reminder map[string]any `json:"reminder"`
	}
	_ = json.NewDecoder(noteReminderResp.Body).Decode(&noteReminderBody)
	noteReminderResp.Body.Close()
	noteReminderID, _ := noteReminderBody.Reminder["id"].(string)
	if noteReminderID == "" || noteReminderBody.Reminder["item_title"] != "Recall field note" {
		t.Fatalf("unexpected note reminder body: %#v", noteReminderBody)
	}
	missingReminderCSRF := httptest.NewRequest(http.MethodPost, "/api/reminders/"+reminderID+"/complete", strings.NewReader(`{}`))
	missingReminderCSRF.Header.Set("Content-Type", "application/json")
	missingReminderCSRF.AddCookie(accessCookie)
	missingReminderRec := httptest.NewRecorder()
	handler.ServeHTTP(missingReminderRec, missingReminderCSRF)
	missingReminderResp := missingReminderRec.Result()
	if missingReminderResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reminder complete without csrf status = %d body=%s", missingReminderResp.StatusCode, readBody(missingReminderResp))
	}
	missingReminderResp.Body.Close()
	crossCompleteReminder := adminRequest(t, handler, http.MethodPost, "/api/reminders/"+reminderID+"/complete", `{}`, otherAccess, otherCSRF)
	if crossCompleteReminder.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user reminder complete status = %d body=%s", crossCompleteReminder.StatusCode, readBody(crossCompleteReminder))
	}
	crossCompleteReminder.Body.Close()
	crossDeleteReminder := adminRequest(t, handler, http.MethodDelete, "/api/reminders/"+reminderID, "", otherAccess, otherCSRF)
	if crossDeleteReminder.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user reminder delete status = %d body=%s", crossDeleteReminder.StatusCode, readBody(crossDeleteReminder))
	}
	crossDeleteReminder.Body.Close()
	tempReminderResp := adminRequest(t, handler, http.MethodPost, "/api/reminders", `{"item_type":"bookmark","item_id":"capture","due_at":"`+now.Add(72*time.Hour).UTC().Format(time.RFC3339)+`","note":"Temporary reminder."}`, accessCookie, csrfCookie)
	if tempReminderResp.StatusCode != http.StatusOK {
		t.Fatalf("create temp reminder status = %d body=%s", tempReminderResp.StatusCode, readBody(tempReminderResp))
	}
	var tempReminderBody struct {
		Reminder map[string]any `json:"reminder"`
	}
	_ = json.NewDecoder(tempReminderResp.Body).Decode(&tempReminderBody)
	tempReminderResp.Body.Close()
	tempReminderID, _ := tempReminderBody.Reminder["id"].(string)
	completeTempReminder := adminRequest(t, handler, http.MethodPost, "/api/reminders/"+tempReminderID+"/complete", `{}`, accessCookie, csrfCookie)
	if completeTempReminder.StatusCode != http.StatusOK {
		t.Fatalf("complete temp reminder status = %d body=%s", completeTempReminder.StatusCode, readBody(completeTempReminder))
	}
	completeTempReminder.Body.Close()
	deleteTempReminder := adminRequest(t, handler, http.MethodDelete, "/api/reminders/"+tempReminderID, "", accessCookie, csrfCookie)
	if deleteTempReminder.StatusCode != http.StatusOK {
		t.Fatalf("delete temp reminder status = %d body=%s", deleteTempReminder.StatusCode, readBody(deleteTempReminder))
	}
	deleteTempReminder.Body.Close()

	actionResp := adminRequest(t, handler, http.MethodPost, "/api/action-items", `{"item_type":"bookmark","item_id":"capture","title":"Turn this into the launch checklist"}`, accessCookie, csrfCookie)
	if actionResp.StatusCode != http.StatusOK {
		t.Fatalf("create action item status = %d body=%s", actionResp.StatusCode, readBody(actionResp))
	}
	var actionBody struct {
		ActionItem map[string]any `json:"action_item"`
	}
	_ = json.NewDecoder(actionResp.Body).Decode(&actionBody)
	actionResp.Body.Close()
	actionID, _ := actionBody.ActionItem["id"].(string)
	if actionID == "" || actionBody.ActionItem["item_title"] != "Capture Loop" {
		t.Fatalf("unexpected action item body: %#v", actionBody)
	}
	noteActionResp := adminRequest(t, handler, http.MethodPost, "/api/action-items", `{"item_type":"note","item_id":"`+searchNoteID+`","title":"Extract the recall heuristic"}`, accessCookie, csrfCookie)
	if noteActionResp.StatusCode != http.StatusOK {
		t.Fatalf("create note action item status = %d body=%s", noteActionResp.StatusCode, readBody(noteActionResp))
	}
	var noteActionBody struct {
		ActionItem map[string]any `json:"action_item"`
	}
	_ = json.NewDecoder(noteActionResp.Body).Decode(&noteActionBody)
	noteActionResp.Body.Close()
	noteActionID, _ := noteActionBody.ActionItem["id"].(string)
	if noteActionID == "" || noteActionBody.ActionItem["item_title"] != "Recall field note" {
		t.Fatalf("unexpected note action item body: %#v", noteActionBody)
	}
	notesListResp := adminRequest(t, handler, http.MethodGet, "/api/notes", "", accessCookie, csrfCookie)
	if notesListResp.StatusCode != http.StatusOK {
		t.Fatalf("notes list status = %d body=%s", notesListResp.StatusCode, readBody(notesListResp))
	}
	var notesListBody struct {
		Notes []map[string]any `json:"notes"`
	}
	_ = json.NewDecoder(notesListResp.Body).Decode(&notesListBody)
	notesListResp.Body.Close()
	var decoratedNote map[string]any
	for _, note := range notesListBody.Notes {
		if note["id"] == searchNoteID {
			decoratedNote = note
			break
		}
	}
	if decoratedNote == nil {
		t.Fatalf("notes list missing recall note: %#v", notesListBody)
	}
	if state, _ := decoratedNote["item_state"].(map[string]any); state["stage"] != "processing" || state["next_action"] != "Synthesize into the recall project." {
		t.Fatalf("notes list missing note item state: %#v", decoratedNote["item_state"])
	}
	actionItems, _ := decoratedNote["action_items"].([]any)
	if len(actionItems) != 1 || actionItems[0].(map[string]any)["id"] != noteActionID {
		t.Fatalf("notes list missing note action items: %#v", decoratedNote["action_items"])
	}
	reminders, _ := decoratedNote["reminders"].([]any)
	if len(reminders) != 1 || reminders[0].(map[string]any)["id"] != noteReminderID {
		t.Fatalf("notes list missing note reminders: %#v", decoratedNote["reminders"])
	}
	if links, _ := decoratedNote["links"].(map[string]any); len(links["outgoing"].([]any)) != 1 || len(links["incoming"].([]any)) != 1 {
		t.Fatalf("notes list missing note links: %#v", decoratedNote["links"])
	}
	crossActionResp := adminRequest(t, handler, http.MethodPost, "/api/action-items", `{"item_type":"note","item_id":"`+otherNoteID+`","title":"Steal this"}`, accessCookie, csrfCookie)
	if crossActionResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user action item status = %d body=%s", crossActionResp.StatusCode, readBody(crossActionResp))
	}
	crossActionResp.Body.Close()
	actionListResp := adminRequest(t, handler, http.MethodGet, "/api/action-items?item=bookmark:capture", "", accessCookie, csrfCookie)
	if actionListResp.StatusCode != http.StatusOK {
		t.Fatalf("action item list status = %d body=%s", actionListResp.StatusCode, readBody(actionListResp))
	}
	var actionListBody struct {
		ActionItems []map[string]any `json:"action_items"`
	}
	_ = json.NewDecoder(actionListResp.Body).Decode(&actionListBody)
	actionListResp.Body.Close()
	if len(actionListBody.ActionItems) != 1 || actionListBody.ActionItems[0]["id"] != actionID {
		t.Fatalf("unexpected action item list: %#v", actionListBody)
	}
	completeActionResp := adminRequest(t, handler, http.MethodPost, "/api/action-items/"+actionID+"/complete", `{}`, accessCookie, csrfCookie)
	if completeActionResp.StatusCode != http.StatusOK {
		t.Fatalf("complete action item status = %d body=%s", completeActionResp.StatusCode, readBody(completeActionResp))
	}
	completeActionResp.Body.Close()
	deleteActionResp := adminRequest(t, handler, http.MethodDelete, "/api/action-items/"+actionID, "", accessCookie, csrfCookie)
	if deleteActionResp.StatusCode != http.StatusOK {
		t.Fatalf("delete completed action item status = %d body=%s", deleteActionResp.StatusCode, readBody(deleteActionResp))
	}
	deleteActionResp.Body.Close()
	actionResp = adminRequest(t, handler, http.MethodPost, "/api/action-items", `{"item_type":"bookmark","item_id":"capture","title":"Turn this into the launch checklist"}`, accessCookie, csrfCookie)
	if actionResp.StatusCode != http.StatusOK {
		t.Fatalf("recreate action item status = %d body=%s", actionResp.StatusCode, readBody(actionResp))
	}
	_ = json.NewDecoder(actionResp.Body).Decode(&actionBody)
	actionResp.Body.Close()
	actionID, _ = actionBody.ActionItem["id"].(string)
	crossCompleteAction := adminRequest(t, handler, http.MethodPost, "/api/action-items/"+actionID+"/complete", `{}`, otherAccess, otherCSRF)
	if crossCompleteAction.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user action complete status = %d body=%s", crossCompleteAction.StatusCode, readBody(crossCompleteAction))
	}
	crossCompleteAction.Body.Close()
	crossDeleteAction := adminRequest(t, handler, http.MethodDelete, "/api/action-items/"+actionID, "", otherAccess, otherCSRF)
	if crossDeleteAction.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user action delete status = %d body=%s", crossDeleteAction.StatusCode, readBody(crossDeleteAction))
	}
	crossDeleteAction.Body.Close()

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
	editableAnnotationResp := adminRequest(t, handler, http.MethodPost, "/api/bookmarks/capture/annotations", `{"quote":"Draft quote","note":"Draft note","selector":{},"tags":["draft"]}`, accessCookie, csrfCookie)
	if editableAnnotationResp.StatusCode != http.StatusOK {
		t.Fatalf("create editable annotation status = %d body=%s", editableAnnotationResp.StatusCode, readBody(editableAnnotationResp))
	}
	var editableAnnotationBody struct {
		Annotation map[string]any `json:"annotation"`
	}
	_ = json.NewDecoder(editableAnnotationResp.Body).Decode(&editableAnnotationBody)
	editableAnnotationResp.Body.Close()
	editableAnnotationID, _ := editableAnnotationBody.Annotation["id"].(string)
	if editableAnnotationID == "" {
		t.Fatalf("editable annotation missing id: %#v", editableAnnotationBody)
	}
	updateAnnotationResp := adminRequest(t, handler, http.MethodPatch, "/api/annotations/"+editableAnnotationID, `{"quote":"Updated quote","note":"Updated note","selector":{},"tags":["edited"]}`, accessCookie, csrfCookie)
	if updateAnnotationResp.StatusCode != http.StatusOK {
		t.Fatalf("update annotation status = %d body=%s", updateAnnotationResp.StatusCode, readBody(updateAnnotationResp))
	}
	var updateAnnotationBody struct {
		Annotation map[string]any `json:"annotation"`
	}
	_ = json.NewDecoder(updateAnnotationResp.Body).Decode(&updateAnnotationBody)
	updateAnnotationResp.Body.Close()
	if updateAnnotationBody.Annotation["quote"] != "Updated quote" {
		t.Fatalf("annotation did not update: %#v", updateAnnotationBody)
	}
	deleteAnnotationResp := adminRequest(t, handler, http.MethodDelete, "/api/annotations/"+editableAnnotationID, "", accessCookie, csrfCookie)
	if deleteAnnotationResp.StatusCode != http.StatusOK {
		t.Fatalf("delete annotation status = %d body=%s", deleteAnnotationResp.StatusCode, readBody(deleteAnnotationResp))
	}
	deleteAnnotationResp.Body.Close()

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
	if state, _ := detail["item_state"].(map[string]any); state["stage"] != "inbox" {
		t.Fatalf("bookmark detail missing item state: %#v", detail["item_state"])
	}
	if links, _ := detail["links"].(map[string]any); len(links["outgoing"].([]any)) != 1 {
		t.Fatalf("bookmark detail missing links: %#v", detail["links"])
	}
	if reminders, _ := detail["reminders"].([]any); len(reminders) != 1 {
		t.Fatalf("bookmark detail missing reminders: %#v", detail["reminders"])
	}
	if actionItems, _ := detail["action_items"].([]any); len(actionItems) != 1 || actionItems[0].(map[string]any)["id"] != actionID {
		t.Fatalf("bookmark detail missing action items: %#v", detail["action_items"])
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
	if !strings.Contains(answerBody.Answer, "The capture loop needs review.") || !strings.Contains(answerBody.Answer, "Recall with evidence") || len(answerBody.Citations) != 1 || answerBody.Citations[0]["id"] != "capture" || answerBody.Citations[0]["snippet"] == "" {
		t.Fatalf("answer mode missing cited capture: %#v", answerBody)
	}

	noteAnswerResp := adminRequest(t, handler, http.MethodGet, "/api/search/answer?q=recall", "", accessCookie, csrfCookie)
	if noteAnswerResp.StatusCode != http.StatusOK {
		t.Fatalf("note answer status = %d body=%s", noteAnswerResp.StatusCode, readBody(noteAnswerResp))
	}
	var noteAnswerBody struct {
		Answer    string           `json:"answer"`
		Citations []map[string]any `json:"citations"`
	}
	_ = json.NewDecoder(noteAnswerResp.Body).Decode(&noteAnswerBody)
	noteAnswerResp.Body.Close()
	var sawNote bool
	for _, citation := range noteAnswerBody.Citations {
		if citation["id"] == searchNoteID && citation["type"] == "note" && citation["snippet"] != "" {
			sawNote = true
		}
		if citation["id"] == otherNoteID {
			t.Fatalf("answer mode leaked other user's note: %#v", noteAnswerBody)
		}
	}
	if !sawNote || !strings.Contains(noteAnswerBody.Answer, "Standalone recall idea for later synthesis.") {
		t.Fatalf("answer mode missing standalone note citation: %#v", noteAnswerBody)
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
	var sawBookmarkReview, sawNoteReview, sawSnoozeNoteReview bool
	for _, item := range reviewBody.Items {
		if item["id"] == "capture" && item["item_type"] == "bookmark" {
			sawBookmarkReview = true
		}
		if item["id"] == searchNoteID && item["item_type"] == "note" {
			sawNoteReview = true
			state, _ := item["item_state"].(map[string]any)
			if state["stage"] != "processing" || state["next_action"] != "Synthesize into the recall project." {
				t.Fatalf("review note missing item state: %#v", item)
			}
		}
		if item["id"] == snoozeNoteID && item["item_type"] == "note" {
			sawSnoozeNoteReview = true
		}
		if item["id"] == otherNoteID {
			t.Fatalf("review queue leaked other user's note: %#v", reviewBody)
		}
	}
	if !sawBookmarkReview || !sawNoteReview || !sawSnoozeNoteReview {
		t.Fatalf("unexpected review queue: %#v", reviewBody)
	}

	completeNoteResp := adminRequest(t, handler, http.MethodPost, "/api/review/note:"+searchNoteID+"/complete", `{}`, accessCookie, csrfCookie)
	if completeNoteResp.StatusCode != http.StatusOK {
		t.Fatalf("complete note review status = %d body=%s", completeNoteResp.StatusCode, readBody(completeNoteResp))
	}
	completeNoteResp.Body.Close()

	snoozeNoteReviewResp := adminRequest(t, handler, http.MethodPost, "/api/review/note:"+snoozeNoteID+"/snooze", `{"days":7}`, accessCookie, csrfCookie)
	if snoozeNoteReviewResp.StatusCode != http.StatusOK {
		t.Fatalf("snooze note review status = %d body=%s", snoozeNoteReviewResp.StatusCode, readBody(snoozeNoteReviewResp))
	}
	snoozeNoteReviewResp.Body.Close()
	afterNoteActionsResp := adminRequest(t, handler, http.MethodGet, "/api/review?limit=5", "", accessCookie, csrfCookie)
	if afterNoteActionsResp.StatusCode != http.StatusOK {
		t.Fatalf("review after note actions status = %d body=%s", afterNoteActionsResp.StatusCode, readBody(afterNoteActionsResp))
	}
	var afterNoteActionsBody struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(afterNoteActionsResp.Body).Decode(&afterNoteActionsBody)
	afterNoteActionsResp.Body.Close()
	for _, item := range afterNoteActionsBody.Items {
		if item["id"] == searchNoteID || item["id"] == snoozeNoteID {
			t.Fatalf("completed or snoozed note still in review queue: %#v", afterNoteActionsBody)
		}
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

	exportResp := adminRequest(t, handler, http.MethodGet, "/api/bookmarks/export?format=json", "", accessCookie, csrfCookie)
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d body=%s", exportResp.StatusCode, readBody(exportResp))
	}
	exportRaw, err := io.ReadAll(exportResp.Body)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	var exportBody struct {
		Bookmarks     []map[string]any `json:"bookmarks"`
		Notes         []map[string]any `json:"notes"`
		Tags          []map[string]any `json:"tags"`
		SavedSearches []map[string]any `json:"saved_searches"`
		ReviewEvents  []map[string]any `json:"review_events"`
		ItemStates    []map[string]any `json:"item_states"`
		ItemLinks     []map[string]any `json:"item_links"`
		Reminders     []map[string]any `json:"reminders"`
		ActionItems   []map[string]any `json:"action_items"`
	}
	_ = json.Unmarshal(exportRaw, &exportBody)
	exportResp.Body.Close()
	if len(exportBody.Bookmarks) != 1 || len(exportBody.Notes) == 0 || len(exportBody.ReviewEvents) == 0 {
		t.Fatalf("export missing second-brain sections: %#v", exportBody)
	}
	var sawProcessingState bool
	for _, state := range exportBody.ItemStates {
		if state["item_id"] == searchNoteID && state["item_type"] == "note" && state["stage"] == "processed" && state["next_action"] == "Synthesize into the recall project." {
			sawProcessingState = true
		}
		if state["item_id"] == otherNoteID {
			t.Fatalf("export leaked other user's item state: %#v", exportBody.ItemStates)
		}
	}
	if !sawProcessingState {
		t.Fatalf("export missing item state: %#v", exportBody.ItemStates)
	}
	if len(exportBody.ItemLinks) != 2 {
		t.Fatalf("export missing item link: %#v", exportBody.ItemLinks)
	}
	var sawBookmarkNoteLink, sawNoteNoteLink bool
	for _, link := range exportBody.ItemLinks {
		if link["from_id"] == "capture" && link["to_id"] == searchNoteID && link["label"] == "supports" {
			sawBookmarkNoteLink = true
		}
		if link["from_id"] == searchNoteID && link["to_id"] == noteID && link["label"] == "extends" {
			sawNoteNoteLink = true
		}
	}
	if !sawBookmarkNoteLink || !sawNoteNoteLink {
		t.Fatalf("export missing expected item links: %#v", exportBody.ItemLinks)
	}
	var sawBookmarkReminder, sawNoteReminder bool
	for _, reminder := range exportBody.Reminders {
		if reminder["item_id"] == "capture" && reminder["note"] == "Use this in planning." {
			sawBookmarkReminder = true
		}
		if reminder["item_id"] == searchNoteID && reminder["note"] == "Bring this note back." {
			sawNoteReminder = true
		}
	}
	if len(exportBody.Reminders) != 2 || !sawBookmarkReminder || !sawNoteReminder {
		t.Fatalf("export missing reminder: %#v", exportBody.Reminders)
	}
	if len(exportBody.ActionItems) != 2 {
		t.Fatalf("export missing action items: %#v", exportBody.ActionItems)
	}
	var sawBookmarkAction, sawNoteAction bool
	for _, item := range exportBody.ActionItems {
		if item["item_id"] == "capture" && item["title"] == "Turn this into the launch checklist" {
			sawBookmarkAction = true
		}
		if item["item_id"] == searchNoteID && item["title"] == "Extract the recall heuristic" {
			sawNoteAction = true
		}
		if item["item_id"] == otherNoteID {
			t.Fatalf("export leaked other user's action item: %#v", exportBody.ActionItems)
		}
	}
	if !sawBookmarkAction || !sawNoteAction {
		t.Fatalf("export missing expected action items: %#v", exportBody.ActionItems)
	}
	bookmarkExport := exportBody.Bookmarks[0]
	if bookmarkExport["id"] != "capture" || len(bookmarkExport["annotations"].([]any)) == 0 || len(bookmarkExport["notes"].([]any)) == 0 || len(bookmarkExport["tags"].([]any)) == 0 {
		t.Fatalf("bookmark export missing linked data: %#v", bookmarkExport)
	}
	var sawStandaloneNote bool
	for _, note := range exportBody.Notes {
		if note["id"] == searchNoteID {
			sawStandaloneNote = true
		}
		if note["id"] == otherNoteID {
			t.Fatalf("export leaked other user's note: %#v", exportBody.Notes)
		}
	}
	if !sawStandaloneNote {
		t.Fatalf("export missing standalone note: %#v", exportBody.Notes)
	}
	if len(exportBody.SavedSearches) == 0 {
		t.Fatalf("export missing saved searches: %#v", exportBody)
	}
	var sawAlias bool
	for _, tag := range exportBody.Tags {
		for _, alias := range tag["aliases"].([]any) {
			if alias.(map[string]any)["alias"] == "PKM" {
				sawAlias = true
			}
		}
	}
	if !sawAlias {
		t.Fatalf("export missing tag alias: %#v", exportBody.Tags)
	}

	obsidianResp := adminRequest(t, handler, http.MethodGet, "/api/bookmarks/export?format=obsidian", "", accessCookie, csrfCookie)
	if obsidianResp.StatusCode != http.StatusOK {
		t.Fatalf("obsidian export status = %d body=%s", obsidianResp.StatusCode, readBody(obsidianResp))
	}
	if contentType := obsidianResp.Header.Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("obsidian content-type = %q", contentType)
	}
	obsidianRaw, err := io.ReadAll(obsidianResp.Body)
	if err != nil {
		t.Fatalf("read obsidian export: %v", err)
	}
	obsidianResp.Body.Close()
	vault, err := zip.NewReader(bytes.NewReader(obsidianRaw), int64(len(obsidianRaw)))
	if err != nil {
		t.Fatalf("open obsidian export: %v", err)
	}
	var sawBookmarkFile, sawNoteFile bool
	for _, file := range vault.File {
		if strings.HasPrefix(file.Name, "Bookmarks/") && strings.Contains(file.Name, "Capture Loop") {
			sawBookmarkFile = true
			rc, err := file.Open()
			if err != nil {
				t.Fatalf("open obsidian bookmark: %v", err)
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatalf("read obsidian bookmark: %v", err)
			}
			if !strings.Contains(string(content), "The capture loop needs review.") || !strings.Contains(string(content), "Recall with evidence") {
				t.Fatalf("obsidian bookmark missing second-brain content: %s", string(content))
			}
		}
		if strings.HasPrefix(file.Name, "Notes/") && strings.Contains(file.Name, "Recall field note") {
			sawNoteFile = true
		}
	}
	if !sawBookmarkFile || !sawNoteFile {
		t.Fatalf("obsidian export missing bookmark or note files: %#v", vault.File)
	}

	restoreAccess, restoreCSRF := signupForCookies(t, handler, "restore@example.com")
	restoreResp := adminRequest(t, handler, http.MethodPost, "/api/bookmarks/import", string(exportRaw), restoreAccess, restoreCSRF)
	if restoreResp.StatusCode != http.StatusOK {
		t.Fatalf("restore import status = %d body=%s", restoreResp.StatusCode, readBody(restoreResp))
	}
	var restoreBody struct {
		Message     string `json:"message"`
		Count       int    `json:"count"`
		ImportJobID string `json:"import_job_id"`
	}
	_ = json.NewDecoder(restoreResp.Body).Decode(&restoreBody)
	restoreResp.Body.Close()
	if restoreBody.Message != "Backup restored" || restoreBody.Count != 1 || restoreBody.ImportJobID == "" {
		t.Fatalf("unexpected restore body: %#v", restoreBody)
	}
	restoreExportResp := adminRequest(t, handler, http.MethodGet, "/api/bookmarks/export?format=json", "", restoreAccess, restoreCSRF)
	if restoreExportResp.StatusCode != http.StatusOK {
		t.Fatalf("restored export status = %d body=%s", restoreExportResp.StatusCode, readBody(restoreExportResp))
	}
	var restoredExport struct {
		Bookmarks     []map[string]any `json:"bookmarks"`
		Notes         []map[string]any `json:"notes"`
		Tags          []map[string]any `json:"tags"`
		SavedSearches []map[string]any `json:"saved_searches"`
		ReviewEvents  []map[string]any `json:"review_events"`
		ItemStates    []map[string]any `json:"item_states"`
		ItemLinks     []map[string]any `json:"item_links"`
		Reminders     []map[string]any `json:"reminders"`
		ActionItems   []map[string]any `json:"action_items"`
	}
	_ = json.NewDecoder(restoreExportResp.Body).Decode(&restoredExport)
	restoreExportResp.Body.Close()
	if len(restoredExport.Bookmarks) != 1 || len(restoredExport.Notes) == 0 || len(restoredExport.SavedSearches) == 0 || len(restoredExport.ReviewEvents) == 0 {
		t.Fatalf("restored export missing backup sections: %#v", restoredExport)
	}
	var sawRestoredProcessingState bool
	for _, state := range restoredExport.ItemStates {
		if state["item_type"] == "note" && state["stage"] == "processed" && state["next_action"] == "Synthesize into the recall project." {
			sawRestoredProcessingState = true
		}
	}
	if !sawRestoredProcessingState {
		t.Fatalf("restored export missing item state: %#v", restoredExport.ItemStates)
	}
	if len(restoredExport.ItemLinks) != 2 {
		t.Fatalf("restored export missing remapped item link: %#v", restoredExport.ItemLinks)
	}
	var restoredBookmarkNoteLink, restoredNoteNoteLink bool
	for _, link := range restoredExport.ItemLinks {
		if link["from_id"] == "capture" || link["from_id"] == searchNoteID || link["to_id"] == searchNoteID || link["to_id"] == noteID {
			t.Fatalf("restored export kept original link id: %#v", restoredExport.ItemLinks)
		}
		if link["from_type"] == "bookmark" && link["to_type"] == "note" && link["label"] == "supports" {
			restoredBookmarkNoteLink = true
		}
		if link["from_type"] == "note" && link["to_type"] == "note" && link["label"] == "extends" {
			restoredNoteNoteLink = true
		}
	}
	if !restoredBookmarkNoteLink || !restoredNoteNoteLink {
		t.Fatalf("restored export missing remapped item links: %#v", restoredExport.ItemLinks)
	}
	var restoredBookmarkReminder, restoredNoteReminder bool
	for _, reminder := range restoredExport.Reminders {
		if reminder["item_id"] == "capture" || reminder["item_id"] == searchNoteID {
			t.Fatalf("restored export kept original reminder item id: %#v", restoredExport.Reminders)
		}
		if reminder["item_type"] == "bookmark" && reminder["note"] == "Use this in planning." {
			restoredBookmarkReminder = true
		}
		if reminder["item_type"] == "note" && reminder["note"] == "Bring this note back." {
			restoredNoteReminder = true
		}
	}
	if len(restoredExport.Reminders) != 2 || !restoredBookmarkReminder || !restoredNoteReminder {
		t.Fatalf("restored export missing remapped reminder: %#v", restoredExport.Reminders)
	}
	if len(restoredExport.ActionItems) != 2 {
		t.Fatalf("restored export missing action items: %#v", restoredExport.ActionItems)
	}
	var restoredBookmarkAction, restoredNoteAction bool
	for _, item := range restoredExport.ActionItems {
		if item["item_id"] == "capture" || item["item_id"] == searchNoteID || item["item_id"] == otherNoteID {
			t.Fatalf("restored action item reused or leaked original IDs: %#v", restoredExport.ActionItems)
		}
		if item["item_type"] == "bookmark" && item["title"] == "Turn this into the launch checklist" {
			restoredBookmarkAction = true
		}
		if item["item_type"] == "note" && item["title"] == "Extract the recall heuristic" {
			restoredNoteAction = true
		}
	}
	if !restoredBookmarkAction || !restoredNoteAction {
		t.Fatalf("restored export missing remapped action items: %#v", restoredExport.ActionItems)
	}
	restoredBookmark := restoredExport.Bookmarks[0]
	if restoredBookmark["id"] == "capture" || len(restoredBookmark["annotations"].([]any)) == 0 || len(restoredBookmark["notes"].([]any)) == 0 || len(restoredBookmark["tags"].([]any)) == 0 {
		t.Fatalf("restored bookmark missing remapped linked data: %#v", restoredBookmark)
	}
	if summary, _ := restoredBookmark["ai_summary"].(map[string]any); summary["one_sentence"] != "The capture loop needs review." {
		t.Fatalf("restored bookmark missing summary: %#v", restoredBookmark)
	}
	var restoredStandalone, restoredAlias bool
	for _, note := range restoredExport.Notes {
		if note["id"] == searchNoteID || note["id"] == otherNoteID {
			t.Fatalf("restore reused or leaked original note ids: %#v", restoredExport.Notes)
		}
		if note["title"] == "Recall field note" {
			restoredStandalone = true
		}
	}
	for _, tag := range restoredExport.Tags {
		for _, alias := range tag["aliases"].([]any) {
			if alias.(map[string]any)["alias"] == "PKM" {
				restoredAlias = true
			}
		}
	}
	if !restoredStandalone || !restoredAlias {
		t.Fatalf("restore missing standalone note or alias: %#v", restoredExport)
	}

	unsupportedAction := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions", `{"action_type":"delete_item","payload":{}}`, accessCookie, csrfCookie)
	if unsupportedAction.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported assistant action status = %d body=%s", unsupportedAction.StatusCode, readBody(unsupportedAction))
	}
	unsupportedAction.Body.Close()

	crossUserAction := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions", `{"action_type":"create_link","payload":{"from_type":"bookmark","from_id":"capture","to_type":"note","to_id":"`+otherNoteID+`","label":"leak"}}`, accessCookie, csrfCookie)
	if crossUserAction.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-user assistant proposal status = %d body=%s", crossUserAction.StatusCode, readBody(crossUserAction))
	}
	crossUserAction.Body.Close()

	proposeLink := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions", `{"action_type":"create_link","payload":{"from_type":"bookmark","from_id":"capture","to_type":"note","to_id":"`+searchNoteID+`","label":"assistant"}}`, accessCookie, csrfCookie)
	if proposeLink.StatusCode != http.StatusOK {
		t.Fatalf("propose assistant link status = %d body=%s", proposeLink.StatusCode, readBody(proposeLink))
	}
	var proposedLink struct {
		Action map[string]any `json:"action"`
	}
	_ = json.NewDecoder(proposeLink.Body).Decode(&proposedLink)
	proposeLink.Body.Close()
	linkActionID, _ := proposedLink.Action["id"].(string)
	if linkActionID == "" || proposedLink.Action["status"] != "pending" {
		t.Fatalf("unexpected assistant proposal: %#v", proposedLink)
	}
	approveLink := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions/"+linkActionID+"/approve", `{}`, accessCookie, csrfCookie)
	if approveLink.StatusCode != http.StatusOK {
		t.Fatalf("approve assistant link status = %d body=%s", approveLink.StatusCode, readBody(approveLink))
	}
	var approvedLink struct {
		Action map[string]any `json:"action"`
	}
	_ = json.NewDecoder(approveLink.Body).Decode(&approvedLink)
	approveLink.Body.Close()
	if approvedLink.Action["status"] != "executed" {
		t.Fatalf("assistant link did not execute: %#v", approvedLink)
	}
	approveAgain := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions/"+linkActionID+"/approve", `{}`, accessCookie, csrfCookie)
	if approveAgain.StatusCode != http.StatusNotFound {
		t.Fatalf("double assistant approval status = %d body=%s", approveAgain.StatusCode, readBody(approveAgain))
	}
	approveAgain.Body.Close()
	var assistantLinks int
	_ = a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM item_links WHERE user_id=? AND source='assistant' AND label='assistant'`, userID).Scan(&assistantLinks)
	if assistantLinks != 1 {
		t.Fatalf("double approval created %d assistant links", assistantLinks)
	}

	proposeTask := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions", `{"action_type":"create_action_item","payload":{"item_type":"bookmark","item_id":"capture","title":"Assistant suggested task"}}`, accessCookie, csrfCookie)
	if proposeTask.StatusCode != http.StatusOK {
		t.Fatalf("propose assistant action item status = %d body=%s", proposeTask.StatusCode, readBody(proposeTask))
	}
	var proposedTask struct {
		Action map[string]any `json:"action"`
	}
	_ = json.NewDecoder(proposeTask.Body).Decode(&proposedTask)
	proposeTask.Body.Close()
	taskActionID, _ := proposedTask.Action["id"].(string)
	approveTask := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions/"+taskActionID+"/approve", `{}`, accessCookie, csrfCookie)
	if approveTask.StatusCode != http.StatusOK {
		t.Fatalf("approve assistant action item status = %d body=%s", approveTask.StatusCode, readBody(approveTask))
	}
	approveTask.Body.Close()
	var assistantTasks int
	_ = a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM action_items WHERE user_id=? AND title='Assistant suggested task'`, userID).Scan(&assistantTasks)
	if assistantTasks != 1 {
		t.Fatalf("assistant action item count = %d", assistantTasks)
	}

	proposeReject := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions", `{"action_type":"update_item_state","payload":{"item_type":"bookmark","item_id":"capture","stage":"archived","importance":1,"next_action":"reject me"}}`, accessCookie, csrfCookie)
	if proposeReject.StatusCode != http.StatusOK {
		t.Fatalf("propose reject action status = %d body=%s", proposeReject.StatusCode, readBody(proposeReject))
	}
	var rejectBody struct {
		Action map[string]any `json:"action"`
	}
	_ = json.NewDecoder(proposeReject.Body).Decode(&rejectBody)
	proposeReject.Body.Close()
	rejectActionID, _ := rejectBody.Action["id"].(string)
	rejectAction := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions/"+rejectActionID+"/reject", `{}`, accessCookie, csrfCookie)
	if rejectAction.StatusCode != http.StatusOK {
		t.Fatalf("reject assistant action status = %d body=%s", rejectAction.StatusCode, readBody(rejectAction))
	}
	rejectAction.Body.Close()
	approveRejected := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions/"+rejectActionID+"/approve", `{}`, accessCookie, csrfCookie)
	if approveRejected.StatusCode != http.StatusNotFound {
		t.Fatalf("approve rejected assistant action status = %d body=%s", approveRejected.StatusCode, readBody(approveRejected))
	}
	approveRejected.Body.Close()

	staleActionID := "stale-assistant-action"
	if _, err := a.db.ExecContext(context.Background(), `INSERT INTO assistant_actions(id,user_id,action_type,payload_json,status,result_json,created_at) VALUES(?,?,?,?,?,?,?)`, staleActionID, userID, "update_item_state", `{"item_type":"note","item_id":"`+otherNoteID+`","stage":"processed","importance":5,"next_action":"stale"}`, "pending", "{}", now.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert stale assistant action: %v", err)
	}
	approveStale := adminRequest(t, handler, http.MethodPost, "/api/assistant/actions/"+staleActionID+"/approve", `{}`, accessCookie, csrfCookie)
	if approveStale.StatusCode != http.StatusBadRequest {
		t.Fatalf("approve stale assistant action status = %d body=%s", approveStale.StatusCode, readBody(approveStale))
	}
	approveStale.Body.Close()
	var staleStatus, staleError string
	if err := a.db.QueryRowContext(context.Background(), `SELECT status,error FROM assistant_actions WHERE id=?`, staleActionID).Scan(&staleStatus, &staleError); err != nil {
		t.Fatalf("read stale assistant action: %v", err)
	}
	if staleStatus != "failed" || staleError == "" {
		t.Fatalf("stale assistant action not recorded as failed: status=%q error=%q", staleStatus, staleError)
	}
	failedActions := adminRequest(t, handler, http.MethodGet, "/api/assistant/actions?status=failed", "", accessCookie, csrfCookie)
	if failedActions.StatusCode != http.StatusOK {
		t.Fatalf("failed assistant actions status = %d body=%s", failedActions.StatusCode, readBody(failedActions))
	}
	var failedActionsBody struct {
		Actions []map[string]any `json:"actions"`
	}
	_ = json.NewDecoder(failedActions.Body).Decode(&failedActionsBody)
	failedActions.Body.Close()
	if len(failedActionsBody.Actions) != 1 || failedActionsBody.Actions[0]["id"] != staleActionID || failedActionsBody.Actions[0]["error"] == "" {
		t.Fatalf("failed assistant actions missing stale failure: %#v", failedActionsBody)
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"other cannot read note", http.MethodGet, "/api/notes/" + noteID, ""},
		{"other cannot patch annotation", http.MethodPatch, "/api/annotations/" + annotationID, `{"quote":"x","selector":{}}`},
		{"other cannot delete annotation", http.MethodDelete, "/api/annotations/" + annotationID, ""},
		{"other cannot read job", http.MethodGet, "/api/jobs/" + jobID, ""},
		{"other cannot complete note review", http.MethodPost, "/api/review/note:" + searchNoteID + "/complete", `{}`},
		{"other cannot complete review", http.MethodPost, "/api/review/bookmark:capture/complete", `{}`},
		{"other cannot approve assistant action", http.MethodPost, "/api/assistant/actions/" + linkActionID + "/approve", `{}`},
		{"other cannot reject assistant action", http.MethodPost, "/api/assistant/actions/" + linkActionID + "/reject", `{}`},
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

func TestImportQueuesJobsWithVisibleProgressID(t *testing.T) {
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
	accessCookie, csrfCookie := signupForCookies(t, handler, "importer@example.com")

	importBody := `<!doctype NETSCAPE-Bookmark-file-1><DT><A HREF="https://example.com/a">A</A><DT><A HREF="https://example.com/b">B</A>`
	resp := adminRequest(t, handler, http.MethodPost, "/api/bookmarks/import", importBody, accessCookie, csrfCookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d body=%s", resp.StatusCode, readBody(resp))
	}
	var body struct {
		Count        int              `json:"count"`
		ImportJobID  string           `json:"import_job_id"`
		SourceReport []map[string]any `json:"source_report"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 || body.ImportJobID == "" {
		t.Fatalf("unexpected import body: %#v", body)
	}
	assertSourceReport(t, body.SourceReport, "browser", 2)
	var total int
	if err := a.db.QueryRow(`SELECT total_bookmarks FROM import_jobs WHERE id=?`, body.ImportJobID).Scan(&total); err != nil {
		t.Fatalf("scan import job: %v", err)
	}
	if total != 2 {
		t.Fatalf("total_bookmarks = %d, want 2", total)
	}
	var sources int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM import_sources WHERE import_job_id=? AND source_type='browser'`, body.ImportJobID).Scan(&sources); err != nil {
		t.Fatalf("count import sources: %v", err)
	}
	if sources != 2 {
		t.Fatalf("import source rows = %d, want 2", sources)
	}
	var bookmarkSources int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE source='browser'`).Scan(&bookmarkSources); err != nil {
		t.Fatalf("count bookmark sources: %v", err)
	}
	if bookmarkSources != 2 {
		t.Fatalf("browser bookmark sources = %d, want 2", bookmarkSources)
	}
	jobResp := adminRequest(t, handler, http.MethodGet, "/api/import-jobs/"+body.ImportJobID, "", accessCookie, csrfCookie)
	if jobResp.StatusCode != http.StatusOK {
		t.Fatalf("import job status = %d body=%s", jobResp.StatusCode, readBody(jobResp))
	}
	var jobBody struct {
		SourceReport []map[string]any `json:"source_report"`
		Items        []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(jobResp.Body).Decode(&jobBody)
	jobResp.Body.Close()
	assertSourceReport(t, jobBody.SourceReport, "browser", 2)
	if len(jobBody.Items) != 2 || jobBody.Items[0]["bookmark_id"] == "" || jobBody.Items[0]["url"] == "" {
		t.Fatalf("unexpected import item provenance: %#v", jobBody.Items)
	}

	listResp := adminRequest(t, handler, http.MethodGet, "/api/import-jobs", "", accessCookie, csrfCookie)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("import jobs status = %d body=%s", listResp.StatusCode, readBody(listResp))
	}
	var listBody []map[string]any
	_ = json.NewDecoder(listResp.Body).Decode(&listBody)
	listResp.Body.Close()
	if len(listBody) != 1 {
		t.Fatalf("import jobs length = %d, want 1", len(listBody))
	}
	report, _ := listBody[0]["source_report"].([]any)
	if len(report) != 1 || report[0].(map[string]any)["source"] != "browser" || int(report[0].(map[string]any)["count"].(float64)) != 2 {
		t.Fatalf("unexpected import jobs source report: %#v", listBody)
	}
	if listBody[0]["items"] != nil {
		t.Fatalf("import job list should stay aggregate-only: %#v", listBody)
	}
	exportResp := adminRequest(t, handler, http.MethodGet, "/api/bookmarks/export?format=json", "", accessCookie, csrfCookie)
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d body=%s", exportResp.StatusCode, readBody(exportResp))
	}
	var exportBody struct {
		Bookmarks     []map[string]any `json:"bookmarks"`
		ImportJobs    []map[string]any `json:"import_jobs"`
		ImportSources []map[string]any `json:"import_sources"`
	}
	_ = json.NewDecoder(exportResp.Body).Decode(&exportBody)
	exportResp.Body.Close()
	if len(exportBody.Bookmarks) != 2 || len(exportBody.ImportJobs) != 1 || len(exportBody.ImportSources) != 2 {
		t.Fatalf("export missing import provenance: %#v", exportBody)
	}
	var exportedBrowserSources int
	for _, bookmark := range exportBody.Bookmarks {
		if bookmark["source"] == "browser" {
			exportedBrowserSources++
		}
	}
	if exportedBrowserSources != 2 {
		t.Fatalf("export missing imported bookmark source: %#v", exportBody.Bookmarks)
	}
	rows, err := a.db.Query(`SELECT payload_json FROM jobs WHERE type='bookmark.process' ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query jobs: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan job payload: %v", err)
		}
		var payload map[string]string
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode job payload: %v", err)
		}
		if payload["import_job_id"] != body.ImportJobID || payload["bookmark_id"] == "" || payload["url"] == "" {
			t.Fatalf("unexpected job payload: %#v", payload)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate jobs: %v", err)
	}
	if seen != 2 {
		t.Fatalf("queued jobs = %d, want 2", seen)
	}
}

func assertSourceReport(t *testing.T, report []map[string]any, source string, count int) {
	t.Helper()
	for _, item := range report {
		gotSource, _ := item["source"].(string)
		gotCount, _ := item["count"].(float64)
		if gotSource == source && int(gotCount) == count {
			return
		}
	}
	t.Fatalf("source report missing %s=%d: %#v", source, count, report)
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
	for _, expected := range []string{`id="filter-source"`, `id="filter-date-from"`, `id="filter-date-to"`, `"source", "date_from", "date_to"`, `id="profile-form"`, `id="api-keys-form"`, `id="x-connect"`, `id="x-sync"`, `id="x-disconnect"`, `id="admin-tabs"`, `/admin/api-usage`, `/admin/activity`, `/admin/collections-stats`, `data-admin-user-action`, `/notes?note=${encodeURIComponent(item.id)}`, `function focusNoteFromQuery()`, `async function focusPage()`, `/action-items?status=pending`, `/reminders?status=pending`, `actionItemsPanel("note", note.id, note.action_items || [])`, `reminderForm("note", note.id)`, `function bindReminderControls()`, `noteLinkForm(note, notes)`, `function bindNoteLinkForms()`, `function bindLinkDeleteControls()`} {
		if !strings.Contains(source, expected) {
			t.Fatalf("embedded frontend missing %s", expected)
		}
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

	keyStatus := adminRequest(t, handler, http.MethodGet, "/api/admin/api-keys", "", accessCookie, csrfCookie)
	if keyStatus.StatusCode != http.StatusOK {
		t.Fatalf("api key status = %d body=%s", keyStatus.StatusCode, readBody(keyStatus))
	}
	keyStatus.Body.Close()
	keyUpdate := adminRequest(t, handler, http.MethodPut, "/api/admin/api-keys", `{"gemini_api_key":"test-gemini","x_redirect_uri":"https://example.com/x/callback","x_integration_enabled":true,"resend_from_email":"hello@example.com"}`, accessCookie, csrfCookie)
	if keyUpdate.StatusCode != http.StatusOK {
		t.Fatalf("api key update = %d body=%s", keyUpdate.StatusCode, readBody(keyUpdate))
	}
	keyUpdate.Body.Close()
	var storedCipher, storedPlain string
	if err := a.db.QueryRowContext(context.Background(), `SELECT COALESCE(value_cipher,''),COALESCE(value_plain,'') FROM settings WHERE key='gemini_api_key'`).Scan(&storedCipher, &storedPlain); err != nil {
		t.Fatalf("stored api key setting: %v", err)
	}
	if storedCipher == "" || storedPlain != "" {
		t.Fatalf("secret setting was not stored encrypted: cipher=%q plain=%q", storedCipher, storedPlain)
	}
	if opened, err := secrets.Open(a.cfg.SecretKey, storedCipher); err != nil || opened != "test-gemini" {
		t.Fatalf("secret setting did not decrypt: opened=%q err=%v", opened, err)
	}
	if err := a.db.QueryRowContext(context.Background(), `SELECT COALESCE(value_cipher,''),COALESCE(value_plain,'') FROM settings WHERE key='resend_from_email'`).Scan(&storedCipher, &storedPlain); err != nil {
		t.Fatalf("stored plain setting: %v", err)
	}
	if storedCipher != "" || storedPlain != "hello@example.com" {
		t.Fatalf("plain setting was not stored plainly: cipher=%q plain=%q", storedCipher, storedPlain)
	}
	xEnabled := adminRequest(t, handler, http.MethodGet, "/api/auth/x/enabled", "", accessCookie, csrfCookie)
	var xEnabledBody map[string]any
	_ = json.NewDecoder(xEnabled.Body).Decode(&xEnabledBody)
	xEnabled.Body.Close()
	if xEnabledBody["enabled"] != true {
		t.Fatalf("runtime X setting was not effective: %#v", xEnabledBody)
	}
	deleteKey := adminRequest(t, handler, http.MethodDelete, "/api/admin/api-keys/gemini_api_key", "", accessCookie, csrfCookie)
	if deleteKey.StatusCode != http.StatusOK {
		t.Fatalf("api key delete = %d body=%s", deleteKey.StatusCode, readBody(deleteKey))
	}
	deleteKey.Body.Close()
	badKeyUpdate := adminRequest(t, handler, http.MethodPut, "/api/admin/api-keys", `{"unexpected_setting":"nope"}`, accessCookie, csrfCookie)
	if badKeyUpdate.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected api key update status = %d body=%s", badKeyUpdate.StatusCode, readBody(badKeyUpdate))
	}
	badKeyUpdate.Body.Close()
	var unexpected int
	_ = a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM settings WHERE key='unexpected_setting'`).Scan(&unexpected)
	if unexpected != 0 {
		t.Fatal("api key update stored an unknown setting")
	}
	assertAuditAction(t, a, "admin.settings.update", "settings", "")
	assertAuditAction(t, a, "admin.settings.delete", "settings", "")
	adminID := userIDForEmail(t, a, "admin@example.com")
	a.auditEvent(context.Background(), adminID, "admin.secret.stored", "settings", "", map[string]any{"token": "do-not-store", "note": "visible"})
	var storedMetadata string
	if err := a.db.QueryRowContext(context.Background(), `SELECT metadata_json FROM audit_events WHERE action='admin.secret.stored'`).Scan(&storedMetadata); err != nil {
		t.Fatalf("stored sanitized audit event: %v", err)
	}
	if strings.Contains(storedMetadata, "do-not-store") || !strings.Contains(storedMetadata, "[redacted]") {
		t.Fatalf("audit metadata was not redacted before storage: %s", storedMetadata)
	}
	if _, err := a.db.ExecContext(context.Background(), `INSERT INTO audit_events(id,actor_user_id,action,target_type,target_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, "audit-secret", userIDForEmail(t, a, "admin@example.com"), "admin.secret.test", "settings", "", `{"token":"do-not-leak","note":"visible"}`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed secret audit event: %v", err)
	}
	audit := adminRequest(t, handler, http.MethodGet, "/api/admin/audit-events?limit=10", "", accessCookie, csrfCookie)
	if audit.StatusCode != http.StatusOK {
		t.Fatalf("audit events status = %d body=%s", audit.StatusCode, readBody(audit))
	}
	var auditBody struct {
		Events []map[string]any `json:"events"`
	}
	_ = json.NewDecoder(audit.Body).Decode(&auditBody)
	audit.Body.Close()
	var sawSettingsAudit, sawRedactedAudit bool
	for _, event := range auditBody.Events {
		if event["action"] == "admin.settings.update" && event["actor_email"] == "admin@example.com" {
			metadata, _ := event["metadata"].(map[string]any)
			keys, _ := metadata["keys"].([]any)
			if containsAll(keys, "gemini_api_key", "resend_from_email", "x_integration_enabled", "x_redirect_uri") {
				sawSettingsAudit = true
			}
			if strings.Contains(fmt.Sprint(metadata), "unexpected_setting") {
				t.Fatalf("audit metadata included rejected setting: %#v", metadata)
			}
		}
		if event["action"] == "admin.secret.test" {
			metadata, _ := event["metadata"].(map[string]any)
			if metadata["token"] == "[redacted]" && metadata["note"] == "visible" {
				sawRedactedAudit = true
			}
			if strings.Contains(fmt.Sprint(metadata), "do-not-leak") {
				t.Fatalf("audit metadata leaked sensitive value: %#v", metadata)
			}
		}
	}
	if !sawSettingsAudit || !sawRedactedAudit {
		t.Fatalf("audit events missing settings update: %#v", auditBody)
	}
	for _, path := range []string{"/api/admin/overview", "/api/admin/system", "/api/admin/api-usage", "/api/admin/activity", "/api/admin/collections-stats"} {
		resp := adminRequest(t, handler, http.MethodGet, path, "", accessCookie, csrfCookie)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, resp.StatusCode, readBody(resp))
		}
		resp.Body.Close()
	}
	badLimit := adminRequest(t, handler, http.MethodGet, "/api/admin/audit-events?limit=500", "", accessCookie, csrfCookie)
	if badLimit.StatusCode != http.StatusBadRequest {
		t.Fatalf("audit bad limit status = %d body=%s", badLimit.StatusCode, readBody(badLimit))
	}
	badLimit.Body.Close()
	nonAdminAccess, _ := signupForCookies(t, handler, "viewer@example.com")
	nonAdminAudit := adminRequest(t, handler, http.MethodGet, "/api/admin/audit-events", "", nonAdminAccess, csrfCookie)
	if nonAdminAudit.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin audit status = %d body=%s", nonAdminAudit.StatusCode, readBody(nonAdminAudit))
	}
	nonAdminAudit.Body.Close()

	reset := adminRequest(t, handler, http.MethodPost, "/api/admin/users/"+userID+"/reset-password", `{"new_password":"new-password-123"}`, accessCookie, csrfCookie)
	if reset.StatusCode != http.StatusOK {
		t.Fatalf("reset status = %d body=%s", reset.StatusCode, readBody(reset))
	}
	reset.Body.Close()
	var passwordScheme string
	if err := a.db.QueryRowContext(context.Background(), `SELECT password_scheme FROM users WHERE id=?`, userID).Scan(&passwordScheme); err != nil {
		t.Fatalf("reset password scheme: %v", err)
	}
	if passwordScheme != "argon2id" {
		t.Fatalf("admin reset stored password scheme %q, want argon2id", passwordScheme)
	}

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
	for _, action := range []string{"admin.user.invite", "admin.user.reset_password", "admin.user.ban", "admin.user.unban", "admin.user.delete"} {
		assertAuditAction(t, a, action, "user", userID)
	}
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

func publicJSONRequest(t *testing.T, handler http.Handler, method string, path string, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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

func assertAuditAction(t *testing.T, a *App, action, targetType, targetID string) {
	t.Helper()
	var count int
	if err := a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_events WHERE action=? AND target_type=? AND target_id=?`, action, targetType, targetID).Scan(&count); err != nil {
		t.Fatalf("count audit action %s: %v", action, err)
	}
	if count == 0 {
		t.Fatalf("missing audit action %s target=%s:%s", action, targetType, targetID)
	}
}

func containsAll(values []any, expected ...string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if text, ok := value.(string); ok {
			seen[text] = true
		}
	}
	for _, value := range expected {
		if !seen[value] {
			return false
		}
	}
	return true
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
