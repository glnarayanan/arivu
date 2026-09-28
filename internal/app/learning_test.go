package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/config"
)

func TestLearningRoutesAuthenticationCSRFAudienceAndOwnership(t *testing.T) {
	a, err := New(config.Config{DBPath: filepath.Join(t.TempDir(), "arivu.sqlite3"), SecretKey: "test-secret", SignupEnabled: true, SessionTTL: time.Hour, RefreshTTL: time.Hour, ExtensionTTL: time.Hour, MaxRequestBody: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handler := a.Handler()
	ownerAccess, ownerCSRF := signupForCookies(t, handler, "learning-owner@example.com")
	otherAccess, otherCSRF := signupForCookies(t, handler, "learning-other@example.com")
	cliToken := bodyToken(t, handler, http.MethodPost, "/api/auth/cli/login", `{"email":"learning-owner@example.com","password":"correct horse battery staple"}`, "")
	extensionToken := extensionTokenForTest(t, handler, ownerAccess, ownerCSRF)
	ownerID := userIDForEmail(t, a, "learning-owner@example.com")
	session := `{"id":"owned-session","kind":"quiz","question":"q","passages":[{"id":"p1","text":"owned evidence","source":{"type":"note","id":"source-note"},"title":"Source","url":"","hash":"hash"}],"exchanges":[],"questions":[{"question":"q1","options":["a","b","c","d"],"correct":0,"explanation":"e","citations":[]},{"question":"q2","options":["a","b","c","d"],"correct":1,"explanation":"e","citations":[]},{"question":"q3","options":["a","b","c","d"],"correct":2,"explanation":"e","citations":[]}],"choices":[],"revision":0,"created_at":"2026-01-01"}`
	if _, err := a.db.Exec(`INSERT INTO learning_sessions(id,user_id,payload_json,created_at,updated_at) VALUES('owned-session',?,?,?,?)`, ownerID, session, "2026-01-01", "2026-01-01"); err != nil {
		t.Fatal(err)
	}

	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/learning", ""},
		{http.MethodPost, "/api/learning", `{"kind":"chat","question":"q","sources":[]}`},
		{http.MethodGet, "/api/learning/owned-session", ""},
		{http.MethodDelete, "/api/learning/owned-session", ""},
		{http.MethodPost, "/api/learning/owned-session/generate", `{"question":"q","revision":0}`},
		{http.MethodPost, "/api/learning/owned-session/submit", `{"choices":[0,1,2],"revision":0}`},
		{http.MethodPost, "/api/learning/owned-session/note", `{"passage":"p1"}`},
	}
	for _, route := range routes {
		name := route.method + " " + route.path
		t.Run(name, func(t *testing.T) {
			for audience, token := range map[string]string{"cli": cliToken, "extension": extensionToken} {
				resp := bearerRequest(t, handler, route.method, route.path, route.body, token)
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("%s audience status=%d body=%s", audience, resp.StatusCode, readBody(resp))
				}
				resp.Body.Close()
			}
			req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous status=%d body=%s", rec.Code, rec.Body.String())
			}
			if route.method != http.MethodGet {
				for csrfName, csrfValue := range map[string]string{"missing": "", "mismatched": "wrong"} {
					req = httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
					req.AddCookie(ownerAccess)
					req.AddCookie(ownerCSRF)
					if csrfValue != "" {
						req.Header.Set("X-CSRF-Token", csrfValue)
					}
					rec = httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					if rec.Code != http.StatusUnauthorized {
						t.Fatalf("%s csrf status=%d body=%s", csrfName, rec.Code, rec.Body.String())
					}
				}
			}
			if route.path == "/api/learning" {
				return
			}
			resp := adminRequest(t, handler, route.method, route.path, route.body, otherAccess, otherCSRF)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				var body any
				_ = json.NewDecoder(resp.Body).Decode(&body)
				t.Fatalf("foreign session status=%d body=%v", resp.StatusCode, body)
			}
		})
	}
}
