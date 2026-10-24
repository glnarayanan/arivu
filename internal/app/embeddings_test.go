package app

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/providers"
)

func newEmbeddingTestApp(t *testing.T, cfg config.Config) (*App, http.Handler, *http.Cookie, *http.Cookie) {
	t.Helper()
	cfg.DBPath = filepath.Join(t.TempDir(), "arivu.sqlite3")
	cfg.AppURL = "http://localhost:8080"
	cfg.SecretKey = "test-secret"
	cfg.AdminEmails = map[string]bool{"admin@example.com": true}
	cfg.SignupEnabled = true
	cfg.SessionTTL, cfg.RefreshTTL, cfg.ExtensionTTL = time.Hour, time.Hour, time.Hour
	cfg.MaxRequestBody = 1 << 20
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(a.Close)
	handler := a.Handler()
	access, csrf := signupForCookies(t, handler, "admin@example.com")
	return a, handler, access, csrf
}

func decodeJSONBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestAdminEmbeddingStatusExplainsTextOnlyProvider(t *testing.T) {
	_, handler, access, csrf := newEmbeddingTestApp(t, config.Config{AIProvider: "openrouter", AIAPIKey: "or-secret-key"})
	// OpenRouter offers embeddings, so "same" mode works without another key.
	status := decodeJSONBody(t, adminRequest(t, handler, http.MethodGet, "/api/admin/embeddings", "", access, csrf))
	if status["configured"] != true || status["mode"] != "same" || status["provider"] != "openrouter" || status["model"] != "openai/text-embedding-3-small" {
		t.Fatalf("status = %#v", status)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "or-secret-key") {
		t.Fatal("embedding status leaked the API key")
	}
	if len(status["affected_features"].([]any)) == 0 {
		t.Fatal("affected features missing")
	}
	capable := map[string]bool{}
	for _, item := range status["providers"].([]any) {
		preset := item.(map[string]any)
		capable[preset["id"].(string)] = preset["supports_embeddings"].(bool)
	}
	if capable["deepseek"] || !capable["openai"] || !capable["gemini"] {
		t.Fatalf("provider capabilities = %#v", capable)
	}

	update := adminRequest(t, handler, http.MethodPut, "/api/admin/settings", `{"ai_provider":"deepseek","ai_api_key":"deepseek-key"}`, access, csrf)
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update = %d %s", update.StatusCode, readBody(update))
	}
	update.Body.Close()
	status = decodeJSONBody(t, adminRequest(t, handler, http.MethodGet, "/api/admin/embeddings", "", access, csrf))
	if status["configured"] != false || status["reason"] != providers.EmbeddingReasonTextOnly || !strings.Contains(status["message"].(string), "DeepSeek") {
		t.Fatalf("status = %#v", status)
	}
	if status["text_provider_supports_embeddings"] != false {
		t.Fatalf("text provider capability = %#v", status["text_provider_supports_embeddings"])
	}
}

func TestAdminEmbeddingSettingsSwitchProvidersSafely(t *testing.T) {
	a, handler, access, csrf := newEmbeddingTestApp(t, config.Config{AIProvider: "deepseek", AIAPIKey: "deepseek-key"})

	textOnly := adminRequest(t, handler, http.MethodPut, "/api/admin/settings", `{"embedding_provider":"anthropic","embedding_api_key":"k"}`, access, csrf)
	if textOnly.StatusCode != http.StatusBadRequest || !strings.Contains(readBody(textOnly), "embeddings") {
		t.Fatalf("text-only embedding provider accepted: %d", textOnly.StatusCode)
	}
	missingKey := adminRequest(t, handler, http.MethodPut, "/api/admin/settings", `{"embedding_provider":"openai"}`, access, csrf)
	if missingKey.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing key status = %d", missingKey.StatusCode)
	}
	missingKey.Body.Close()

	set := adminRequest(t, handler, http.MethodPut, "/api/admin/settings", `{"embedding_provider":"openai","embedding_api_key":"sk-openai-embed-9876","embedding_dimensions":"512"}`, access, csrf)
	if set.StatusCode != http.StatusOK {
		t.Fatalf("set = %d %s", set.StatusCode, readBody(set))
	}
	set.Body.Close()
	settings := decodeJSONBody(t, adminRequest(t, handler, http.MethodGet, "/api/admin/settings", "", access, csrf))
	key := settings["embedding_api_key"].(map[string]any)
	if key["masked_value"] != "****9876" || key["value"] != nil {
		t.Fatalf("embedding key status = %#v", key)
	}
	status := decodeJSONBody(t, adminRequest(t, handler, http.MethodGet, "/api/admin/embeddings", "", access, csrf))
	if status["configured"] != true || status["space"] != "openai/text-embedding-3-small@512" {
		t.Fatalf("status = %#v", status)
	}

	// Switching to a local provider must not carry the OpenAI key along.
	local := adminRequest(t, handler, http.MethodPut, "/api/admin/settings", `{"embedding_provider":"ollama"}`, access, csrf)
	if local.StatusCode != http.StatusOK {
		t.Fatalf("local = %d %s", local.StatusCode, readBody(local))
	}
	local.Body.Close()
	effective, err := a.runtime.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.EmbeddingAPIKey != "" || effective.EmbeddingDimensions != 0 {
		t.Fatalf("previous provider settings leaked: %#v", effective)
	}
	if embedding := effective.Embedding(); embedding.Space() != "ollama/embeddinggemma" {
		t.Fatalf("space = %q", embedding.Space())
	}
}

func TestSettingsChangeSchedulesReembedding(t *testing.T) {
	a, handler, access, csrf := newEmbeddingTestApp(t, config.Config{GeminiAPIKey: "gemini-key"})
	userID := userIDForEmail(t, a, "admin@example.com")
	now := time.Now().UTC()
	insertBookmarkForTest(t, a, userID, "b1", "Vector notes", now, now, 0, 1)
	if _, err := a.db.Exec(`INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,content_text,content_hash,quality_status,is_selected,created_at,updated_at) VALUES('e1','b1',?,'article','test','Vector text','h1','complete',1,?,?)`, userID, now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE bookmarks SET embedding='[1,0]',embedding_dim=2,embedding_model='gemini/gemini-embedding-2@768' WHERE id='b1'`); err != nil {
		t.Fatal(err)
	}
	countJobs := func() int {
		var count int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='embedding.reembed' AND status IN ('queued','leased')`).Scan(&count)
		return count
	}
	if countJobs() != 0 {
		t.Fatal("current vectors should not be re-embedded")
	}
	update := adminRequest(t, handler, http.MethodPut, "/api/admin/settings", `{"embedding_provider":"ollama"}`, access, csrf)
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update = %d %s", update.StatusCode, readBody(update))
	}
	update.Body.Close()
	if countJobs() != 1 {
		t.Fatalf("re-embed jobs = %d", countJobs())
	}
	var priority int
	_ = a.db.QueryRow(`SELECT priority FROM jobs WHERE type='embedding.reembed'`).Scan(&priority)
	if priority <= 100 {
		t.Fatalf("re-embedding must run after capture work, priority=%d", priority)
	}
	usage := decodeJSONBody(t, adminRequest(t, handler, http.MethodGet, "/api/admin/api-usage", "", access, csrf))
	embedding := usage["embedding"].(map[string]any)
	vectors := embedding["vectors"].(map[string]any)
	if embedding["provider"] != "ollama" || vectors["stale"].(float64) != 1 || vectors["job_queued"] != true {
		t.Fatalf("usage embedding = %#v", embedding)
	}
	if _, ok := embedding["providers"]; ok {
		t.Fatal("usage payload should not repeat the preset list")
	}
	manual := decodeJSONBody(t, adminRequest(t, handler, http.MethodPost, "/api/admin/embeddings/reembed", "{}", access, csrf))
	if manual["queued"] != false {
		t.Fatalf("pending pass duplicated: %#v", manual)
	}
}

func TestEmbeddingUsageNamesTheEmbeddingModel(t *testing.T) {
	usage := newProviderUsage()
	client := providers.EmbeddingClient{Config: providers.ResolveEmbedding(providers.TextProviderSettings{Provider: "deepseek", APIKey: "k"}, providers.EmbeddingSelection{}), Recorder: usage.RecordAI}
	_, _ = client.GenerateEmbedding(context.Background(), "text")
	ops := usage.Snapshot()["ai"].(map[string]providerUsageOp)
	item := ops["embedding"]
	if item.Errors != 1 || item.LastErrorCode != providers.ErrorEmbeddingNotConfigured || !strings.Contains(item.LastError, "embedding model is not configured") {
		t.Fatalf("usage = %#v", item)
	}
}
