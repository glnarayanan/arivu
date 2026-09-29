package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/config"
)

func TestRetiredWorkflowRoutesRespectAuthenticationAndAudience(t *testing.T) {
	a, err := New(config.Config{DBPath: filepath.Join(t.TempDir(), "arivu.sqlite3"), SecretKey: "test-secret", SignupEnabled: true, SessionTTL: time.Hour, RefreshTTL: time.Hour, ExtensionTTL: time.Hour, MaxRequestBody: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handler := a.Handler()
	access, csrf := signupForCookies(t, handler, "retired@example.com")
	cliToken := bodyToken(t, handler, http.MethodPost, "/api/auth/cli/login", `{"email":"retired@example.com","password":"correct horse battery staple"}`, "")
	extensionResponse := adminRequest(t, handler, http.MethodPost, "/api/auth/extension-token", "", access, csrf)
	var extensionPayload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(extensionResponse.Body).Decode(&extensionPayload); err != nil {
		t.Fatal(err)
	}
	extensionResponse.Body.Close()

	missingCSRFRequest := httptest.NewRequest(http.MethodPost, "/api/reminders", strings.NewReader(`{}`))
	missingCSRFRequest.Header.Set("Content-Type", "application/json")
	missingCSRFRequest.AddCookie(access)
	missingCSRFRecorder := httptest.NewRecorder()
	handler.ServeHTTP(missingCSRFRecorder, missingCSRFRequest)
	if missingCSRFRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("retired POST without CSRF status = %d body=%s", missingCSRFRecorder.Code, missingCSRFRecorder.Body.String())
	}

	webRoutes := []struct{ method, path string }{
		{http.MethodGet, "/api/daily-notes/2026-09-28"},
		{http.MethodPost, "/api/objects"}, {http.MethodPost, "/api/calendar/import"},
		{http.MethodGet, "/api/evolution"}, {http.MethodGet, "/api/today-board"},
		{http.MethodPatch, "/api/inbox/note:old"}, {http.MethodPost, "/api/reminders"},
		{http.MethodPost, "/api/action-items"}, {http.MethodPost, "/api/assistant/actions"},
	}
	for _, route := range webRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := adminRequest(t, handler, route.method, route.path, `{}`, access, csrf)
			if response.StatusCode != http.StatusGone {
				t.Fatalf("authorized status = %d body=%s", response.StatusCode, readBody(response))
			}
			response.Body.Close()

			response = bearerRequest(t, handler, route.method, route.path, `{}`, extensionPayload.AccessToken)
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("foreign audience status = %d body=%s", response.StatusCode, readBody(response))
			}
			response.Body.Close()

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`)))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated status = %d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	for _, path := range []string{"/api/agent/action-items", "/api/agent/reminders", "/api/agent/decisions"} {
		response := bearerRequest(t, handler, http.MethodPost, path, `{}`, cliToken)
		if response.StatusCode != http.StatusGone {
			t.Fatalf("authorized CLI %s status = %d body=%s", path, response.StatusCode, readBody(response))
		}
		response.Body.Close()
		response = adminRequest(t, handler, http.MethodPost, path, `{}`, access, csrf)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("web audience on %s status = %d body=%s", path, response.StatusCode, readBody(response))
		}
		response.Body.Close()
	}
}

func TestRetiredWorkflowDataMigratesToUserScopedNotes(t *testing.T) {
	a, err := New(config.Config{DBPath: filepath.Join(t.TempDir(), "arivu.sqlite3"), SecretKey: "test-secret", SignupEnabled: true, SessionTTL: time.Hour, RefreshTTL: time.Hour, ExtensionTTL: time.Hour, MaxRequestBody: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handler := a.Handler()
	access, csrf := signupForCookies(t, handler, "legacy@example.com")
	otherAccess, otherCSRF := signupForCookies(t, handler, "legacy-other@example.com")
	userID := userIDForEmail(t, a, "legacy@example.com")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := a.db.ExecContext(context.Background(), `INSERT INTO daily_notes(user_id,note_date,body,created_at,updated_at) VALUES(?,?,?,?,?)`, userID, "2026-09-28", "Preserve this daily writing.", now, now); err != nil {
		t.Fatal(err)
	}
	cfg := a.cfg
	a.Close()
	a, err = New(cfg)
	if err != nil {
		t.Fatalf("reopen retired database: %v", err)
	}
	defer a.Close()
	handler = a.Handler()
	response := adminRequest(t, handler, http.MethodGet, "/api/notes", "", access, csrf)
	var payload struct {
		Notes []map[string]any `json:"notes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(payload.Notes) != 1 || payload.Notes[0]["title"] != "2026-09-28" || payload.Notes[0]["body"] != "Preserve this daily writing." || payload.Notes[0]["source"] != "preserved" {
		t.Fatalf("converted note = %#v", payload.Notes)
	}
	other := adminRequest(t, handler, http.MethodGet, "/api/notes", "", otherAccess, otherCSRF)
	var otherPayload struct {
		Notes []map[string]any `json:"notes"`
	}
	_ = json.NewDecoder(other.Body).Decode(&otherPayload)
	other.Body.Close()
	if len(otherPayload.Notes) != 0 {
		t.Fatalf("converted note leaked to another user: %#v", otherPayload.Notes)
	}
	for _, owner := range []bool{true, false} {
		cookie, token := access, csrf
		if !owner {
			cookie, token = otherAccess, otherCSRF
		}
		response := adminRequest(t, handler, http.MethodGet, "/api/search/items?q=daily", "", cookie, token)
		var result struct {
			Count int `json:"count"`
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if (result.Count == 1) != owner || (!owner && result.Count != 0) {
			t.Fatalf("startup search owner=%v count=%d", owner, result.Count)
		}
	}
}
