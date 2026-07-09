package runtimeconfig

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/secrets"
)

func TestRuntimeConfigDatabaseOverridesAndEnvFallback(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Config{
		AppURL:        "https://app.example.test",
		SecretKey:     "test-secret-with-enough-bytes",
		SignupEnabled: true,
		CookieSecure:  true,
		AIProvider:    "openrouter",
		AIAPIKey:      "env-ai",
		AIModel:       "env/model.v1",
		AIBaseURL:     "https://ai.env.test/v1",
		GeminiAPIKey:  "env-gemini",
		GeminiModel:   "env-model",
		GeminiBaseURL: "https://gemini.env.test",
		ResendAPIKey:  "env-resend",
		XClientID:     "env-x-client",
	}
	service := New(db, cfg)
	if err := service.Set(context.Background(), KeyAppURL, "https://runtime.example.test/", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeySignupEnabled, false, "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyCookieSecure, false, "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyAIProvider, providers.ProviderOpenAI, "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyAIAPIKey, "db-ai-secret", "admin@example.com", "test-key"); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyAIModel, "vendor/model.v1:preview", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyAIBaseURL, "https://ai.db.test/v1/", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyGeminiAPIKey, "db-gemini", "admin@example.com", "test-key"); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyGeminiModel, "gemini-custom", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyGeminiBaseURL, "https://gemini.db.test/", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyXIntegrationEnable, true, "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyResendFromEmail, "hello@example.com", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}

	effective, err := service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.AIProvider != providers.ProviderOpenAI || effective.AIAPIKey != "db-ai-secret" || effective.AIModel != "vendor/model.v1:preview" || effective.AIBaseURL != "https://ai.db.test/v1" {
		t.Fatalf("unexpected effective ai config: %#v", effective)
	}
	if effective.GeminiAPIKey != "db-gemini" || effective.GeminiModel != "gemini-custom" || effective.GeminiBaseURL != "https://gemini.db.test" || effective.ResendAPIKey != "env-resend" || !effective.XIntegrationEnabled {
		t.Fatalf("unexpected effective config: %#v", effective)
	}
	if effective.AppURL != "https://runtime.example.test" || effective.SignupEnabled || effective.CookieSecure {
		t.Fatalf("unexpected runtime app config: %#v", effective)
	}
	if effective.ResendFromEmail != "hello@example.com" || effective.XRedirectURI != "https://runtime.example.test/settings?section=connections" {
		t.Fatalf("unexpected plain/default config: %#v", effective)
	}

	var cipher, plain, keyID sql.NullString
	if err := db.QueryRowContext(context.Background(), `SELECT value_cipher,value_plain,key_id FROM settings WHERE key=?`, KeyAIAPIKey).Scan(&cipher, &plain, &keyID); err != nil {
		t.Fatal(err)
	}
	if !cipher.Valid || plain.Valid || keyID.String != "test-key" {
		t.Fatalf("secret setting stored incorrectly: cipher=%v plain=%v keyID=%q", cipher.Valid, plain.Valid, keyID.String)
	}
	if opened, err := secrets.Open(cfg.SecretKey, cipher.String); err != nil || opened != "db-ai-secret" {
		t.Fatalf("secret did not decrypt: value=%q err=%v", opened, err)
	}

	if err := db.QueryRowContext(context.Background(), `SELECT value_cipher,value_plain FROM settings WHERE key=?`, KeyResendFromEmail).Scan(&cipher, &plain); err != nil {
		t.Fatal(err)
	}
	if cipher.Valid || plain.String != "hello@example.com" {
		t.Fatalf("plain setting stored incorrectly: cipher=%v plain=%q", cipher.Valid, plain.String)
	}

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status[KeyAIProvider].Value != providers.ProviderOpenAI || status[KeyAIAPIKey].MaskedValue != "****cret" || status[KeyAIAPIKey].Source != "database" {
		t.Fatalf("unexpected ai status: %#v", status)
	}
	if status[KeyAIModel].Value != "vendor/model.v1:preview" || status[KeyAIBaseURL].Value != "https://ai.db.test/v1" {
		t.Fatalf("unexpected ai provider status: %#v", status)
	}
	if status[KeyGeminiModel].Value != "gemini-custom" || status[KeyGeminiBaseURL].Value != "https://gemini.db.test" {
		t.Fatalf("unexpected gemini provider status: %#v", status)
	}
	if status[KeySignupEnabled].Value != false || status[KeyCookieSecure].Value != false || status[KeyAppURL].Value != "https://runtime.example.test" {
		t.Fatalf("unexpected runtime status: %#v", status)
	}
	if status[KeyXRedirectURI].Value != "https://runtime.example.test/settings?section=connections" {
		t.Fatalf("unexpected x redirect status: %#v", status[KeyXRedirectURI])
	}
	xRedirect, err := service.StatusValue(context.Background(), KeyXRedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	if xRedirect.Value != status[KeyXRedirectURI].Value {
		t.Fatalf("single-key status drifted: %#v", xRedirect)
	}

	if err := service.Set(context.Background(), KeyAppURL, "file:///tmp/arivu", "admin@example.com", ""); err == nil {
		t.Fatal("expected invalid app_url to fail")
	}

	if err := service.Delete(context.Background(), KeyAIAPIKey); err != nil {
		t.Fatal(err)
	}
	effective, err = service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.AIAPIKey != "env-ai" {
		t.Fatalf("delete did not restore fallback: %#v", effective)
	}
	if err := service.Set(context.Background(), KeyAIModel, "accounts/fireworks/models/deepseek-v3p1", "admin@example.com", ""); err != nil {
		t.Fatalf("expected slashy ai model to be allowed: %v", err)
	}
	if err := service.Set(context.Background(), KeyAIModel, "models with spaces", "admin@example.com", ""); err == nil {
		t.Fatal("expected whitespace in ai model to fail")
	}
	if err := service.Set(context.Background(), KeyAIModel, "", "admin@example.com", ""); err == nil {
		t.Fatal("expected blank ai model to fail")
	}
	if err := service.Set(context.Background(), KeyAIBaseURL, "http://localhost:1234/v1/", "admin@example.com", ""); err != nil {
		t.Fatalf("expected localhost ai base url to be allowed: %v", err)
	}
	effective, err = service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.AIBaseURL != "http://localhost:1234/v1" {
		t.Fatalf("localhost ai base url was not normalized: %#v", effective)
	}
	if err := service.Set(context.Background(), KeyAIBaseURL, "http://provider.example.com", "admin@example.com", ""); err == nil {
		t.Fatal("expected remote http ai base url to fail")
	}
	if err := service.Set(context.Background(), KeyGeminiModel, "models/gemini-2.5-flash", "admin@example.com", ""); err != nil {
		t.Fatalf("expected prefixed gemini model to normalize: %v", err)
	}
	effective, err = service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.GeminiModel != "gemini-2.5-flash" {
		t.Fatalf("prefixed gemini model was not normalized: %#v", effective)
	}
	if err := service.Set(context.Background(), KeyGeminiModel, "models/vendor/bad", "admin@example.com", ""); err == nil {
		t.Fatal("expected slashy gemini model to fail")
	}
	if err := service.Set(context.Background(), KeyGeminiBaseURL, "http://localhost:8080/gemini/", "admin@example.com", ""); err != nil {
		t.Fatalf("expected localhost gemini base url to be allowed: %v", err)
	}
	effective, err = service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.GeminiBaseURL != "http://localhost:8080/gemini" {
		t.Fatalf("localhost gemini base url was not normalized: %#v", effective)
	}
	if err := service.Set(context.Background(), KeyGeminiBaseURL, "http://gemini.example.com", "admin@example.com", ""); err == nil {
		t.Fatal("expected remote http gemini base url to fail")
	}
	if err := service.Set(context.Background(), KeyGeminiBaseURL, "file:///tmp/gemini", "admin@example.com", ""); err == nil {
		t.Fatal("expected invalid gemini base url to fail")
	}
}

func TestRuntimeConfigGeminiBaseURLDefaultsToGoogle(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service := New(db, config.Config{SecretKey: "test-secret-with-enough-bytes"})
	effective, err := service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.GeminiBaseURL != config.DefaultGeminiBaseURL {
		t.Fatalf("GeminiBaseURL = %q, want %q", effective.GeminiBaseURL, config.DefaultGeminiBaseURL)
	}
	if effective.AIProvider != providers.ProviderGemini || effective.AIModel != config.DefaultGeminiModel || effective.AIBaseURL != config.DefaultGeminiBaseURL {
		t.Fatalf("AI defaults = %#v", effective)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status[KeyAIProvider].Value != providers.ProviderGemini || status[KeyAIProvider].Source != "default" {
		t.Fatalf("ai_provider status = %#v", status[KeyAIProvider])
	}
	if status[KeyAIBaseURL].Value != config.DefaultGeminiBaseURL || status[KeyAIBaseURL].Source != "default" {
		t.Fatalf("ai_base_url status = %#v", status[KeyAIBaseURL])
	}
	if status[KeyGeminiBaseURL].Value != config.DefaultGeminiBaseURL || status[KeyGeminiBaseURL].Source != "default" {
		t.Fatalf("gemini_base_url status = %#v", status[KeyGeminiBaseURL])
	}
}

func TestRuntimeConfigLegacyGeminiFallbackForDefaultProvider(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Config{
		SecretKey:     "test-secret-with-enough-bytes",
		GeminiAPIKey:  "env-gemini",
		GeminiModel:   "env-gemini-model",
		GeminiBaseURL: "https://gemini.env.test",
	}
	service := New(db, cfg)
	effective, err := service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.AIProvider != providers.ProviderGemini || effective.AIAPIKey != "env-gemini" || effective.AIModel != "env-gemini-model" || effective.AIBaseURL != "https://gemini.env.test" {
		t.Fatalf("legacy environment fallback not applied: %#v", effective)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status[KeyAIAPIKey].Source != "legacy_environment" || status[KeyAIModel].Source != "legacy_environment" || status[KeyAIBaseURL].Source != "legacy_environment" {
		t.Fatalf("legacy status sources not surfaced: %#v", status)
	}

	if err := service.Set(context.Background(), KeyGeminiAPIKey, "db-legacy", "admin@example.com", "legacy-key"); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyGeminiModel, "gemini-legacy", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(context.Background(), KeyGeminiBaseURL, "https://gemini.db.test", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	effective, err = service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.AIAPIKey != "db-legacy" || effective.AIModel != "gemini-legacy" || effective.AIBaseURL != "https://gemini.db.test" {
		t.Fatalf("legacy database fallback not applied: %#v", effective)
	}
	status, err = service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status[KeyAIAPIKey].Source != "legacy_database" || status[KeyAIAPIKey].MaskedValue != "****gacy" {
		t.Fatalf("legacy database source not surfaced: %#v", status[KeyAIAPIKey])
	}

	if err := service.Set(context.Background(), KeyAIProvider, providers.ProviderOpenAI, "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	effective, err = service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.AIAPIKey != "" || effective.AIBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("non-gemini provider should not reuse gemini credentials: %#v", effective)
	}
}

func TestXRedirectURIValidationAndBlankReset(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Config{AppURL: "https://app.example.test", SecretKey: "test-secret-with-enough-bytes"}
	service := New(db, cfg)
	if err := service.Set(context.Background(), KeyXRedirectURI, "https://auth.example.test/callback?provider=x", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	effective, err := service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.XRedirectURI != "https://auth.example.test/callback?provider=x" {
		t.Fatalf("unexpected x redirect override: %#v", effective)
	}

	for _, value := range []string{
		"/settings?section=connections",
		"javascript:alert(1)",
		"file:///tmp/arivu",
		"https://example.com/callback\nSet-Cookie: bad=1",
		"https://exa mple.com/callback",
		"://missing-scheme",
	} {
		if err := service.Set(context.Background(), KeyXRedirectURI, value, "admin@example.com", ""); err == nil {
			t.Fatalf("expected x_redirect_uri %q to be rejected", value)
		}
	}

	if err := service.Set(context.Background(), KeyXRedirectURI, "   ", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	effective, err = service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.XRedirectURI != "https://app.example.test/settings?section=connections" {
		t.Fatalf("blank x redirect did not reset to default: %#v", effective)
	}
	var rows int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM settings WHERE key=?`, KeyXRedirectURI).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("blank x redirect left database override rows=%d", rows)
	}
}

func TestInvalidXRedirectURIFallbackFailsClosed(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service := New(db, config.Config{
		AppURL:       "https://app.example.test",
		SecretKey:    "test-secret-with-enough-bytes",
		XRedirectURI: "javascript:alert(1)",
	})
	if _, err := service.Effective(context.Background()); err == nil {
		t.Fatal("expected invalid fallback x_redirect_uri to fail")
	}
	if _, err := service.StatusValue(context.Background(), KeyXRedirectURI); err == nil {
		t.Fatal("expected invalid fallback x_redirect_uri status to fail")
	}
}
