package runtimeconfig

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/secrets"
)

func openEmbeddingTestService(t *testing.T, cfg config.Config) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if cfg.SecretKey == "" {
		cfg.SecretKey = "test-secret-with-enough-bytes"
	}
	return New(db, cfg), db
}

func TestEmbeddingAPIKeyIsEncryptedAtRestAndMasked(t *testing.T) {
	ctx := context.Background()
	service, db := openEmbeddingTestService(t, config.Config{AIProvider: providers.ProviderDeepSeek, AIAPIKey: "deepseek-key"})
	err := service.Apply(ctx, map[string]any{
		KeyEmbeddingProvider: "openai",
		KeyEmbeddingAPIKey:   "sk-embedding-secret-1234",
		KeyEmbeddingModel:    "text-embedding-3-large",
		KeyEmbeddingDims:     "1024",
	}, "admin@example.com", "primary")
	if err != nil {
		t.Fatal(err)
	}
	var cipher sql.NullString
	var plain sql.NullString
	if err := db.QueryRow(`SELECT value_cipher,value_plain FROM settings WHERE key=?`, KeyEmbeddingAPIKey).Scan(&cipher, &plain); err != nil {
		t.Fatal(err)
	}
	if plain.Valid || !cipher.Valid || strings.Contains(cipher.String, "sk-embedding-secret") {
		t.Fatalf("embedding key stored insecurely: cipher=%q plain=%#v", cipher.String, plain)
	}
	opened, err := secrets.Open("test-secret-with-enough-bytes", cipher.String)
	if err != nil || opened != "sk-embedding-secret-1234" {
		t.Fatalf("round trip = %q, %v", opened, err)
	}
	status, err := service.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := status[KeyEmbeddingAPIKey]; !got.Configured || got.MaskedValue != "****1234" || got.Value != nil {
		t.Fatalf("status leaks or hides key: %#v", got)
	}
	effective, err := service.Effective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embedding := effective.Embedding()
	if !embedding.Configured() || embedding.Provider != providers.ProviderOpenAI || embedding.APIKey != "sk-embedding-secret-1234" || embedding.Model != "text-embedding-3-large" || embedding.Dimensions != 1024 {
		t.Fatalf("embedding = %#v", embedding)
	}
	if embedding.Space() != "openai/text-embedding-3-large@1024" {
		t.Fatalf("space = %q", embedding.Space())
	}
}

func TestEmbeddingDefaultsToSameProviderAndExplainsTextOnlyProviders(t *testing.T) {
	ctx := context.Background()
	service, _ := openEmbeddingTestService(t, config.Config{AIProvider: providers.ProviderDeepSeek, AIAPIKey: "deepseek-key"})
	status, err := service.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := status[KeyEmbeddingProvider]; got.Value != providers.EmbeddingProviderSame || got.Source != "default" {
		t.Fatalf("embedding provider status = %#v", got)
	}
	effective, err := service.Effective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if embedding := effective.Embedding(); embedding.Configured() || embedding.Reason != providers.EmbeddingReasonTextOnly {
		t.Fatalf("DeepSeek same-mode embedding = %#v", embedding)
	}

	gemini, _ := openEmbeddingTestService(t, config.Config{GeminiAPIKey: "gemini-key"})
	effective, err = gemini.Effective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if space := effective.Embedding().Space(); space != "gemini/gemini-embedding-2@768" {
		t.Fatalf("legacy Gemini deployments must keep their vector space, got %q", space)
	}
}

func TestEmbeddingSettingsValidationAndOverrideRemoval(t *testing.T) {
	ctx := context.Background()
	service, _ := openEmbeddingTestService(t, config.Config{EmbeddingModel: "env-embed", EmbeddingProvider: "ollama"})
	for key, value := range map[string]any{
		KeyEmbeddingDims:    "-4",
		KeyEmbeddingModel:   "bad model",
		KeyEmbeddingBaseURL: "http://embeddings.example.com/v1",
	} {
		if err := service.Set(ctx, key, value, "admin", ""); err == nil {
			t.Fatalf("%s=%v should be rejected", key, value)
		}
	}
	if err := service.Set(ctx, KeyEmbeddingDims, "99999", "admin", ""); err == nil {
		t.Fatal("oversized dimensions accepted")
	}
	if err := service.Apply(ctx, map[string]any{KeyEmbeddingModel: "db-embed", KeyEmbeddingBaseURL: "http://127.0.0.1:11434/v1/", KeyEmbeddingDims: 256.0}, "admin", ""); err != nil {
		t.Fatal(err)
	}
	effective, _ := service.Effective(ctx)
	if effective.EmbeddingModel != "db-embed" || effective.EmbeddingBaseURL != "http://127.0.0.1:11434/v1" || effective.EmbeddingDimensions != 256 {
		t.Fatalf("effective = %#v", effective)
	}
	if err := service.Apply(ctx, map[string]any{KeyEmbeddingModel: "", KeyEmbeddingDims: ""}, "admin", ""); err != nil {
		t.Fatal(err)
	}
	effective, _ = service.Effective(ctx)
	if effective.EmbeddingModel != "env-embed" || effective.EmbeddingDimensions != 0 {
		t.Fatalf("empty values should restore environment/default: %#v", effective)
	}
	value, err := service.StatusValue(ctx, KeyEmbeddingModel)
	if err != nil || value.Source != "environment" {
		t.Fatalf("model source = %#v, %v", value, err)
	}
}
