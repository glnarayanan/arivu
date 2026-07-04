package migrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDocumentRejectsUnknownFields(t *testing.T) {
	manifest := baselineManifest(Options{DBName: "arivu_db", DryRun: true})
	err := ValidateDocument(manifest, "bookmarks", map[string]any{
		"id":       "bookmark-1",
		"user_id":  "user-1",
		"url":      "https://example.com",
		"surprise": true,
	})
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestValidateDocumentAllowsManifestFields(t *testing.T) {
	manifest := baselineManifest(Options{DBName: "arivu_db", DryRun: true})
	err := ValidateDocument(manifest, "collections", map[string]any{
		"_id":          "mongo-id",
		"id":           "collection-1",
		"user_id":      "user-1",
		"name":         "Research",
		"bookmark_ids": []string{"bookmark-1"},
	})
	if err != nil {
		t.Fatalf("expected manifest fields to pass: %v", err)
	}
}

func TestValidateDocumentRejectsMissingRequiredFields(t *testing.T) {
	manifest := baselineManifest(Options{DBName: "arivu_db", DryRun: true})
	err := ValidateDocument(manifest, "bookmarks", map[string]any{
		"id":         "bookmark-1",
		"user_id":    "user-1",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
	})
	if err == nil || !strings.Contains(err.Error(), "bookmarks.url") {
		t.Fatalf("expected missing url error, got %v", err)
	}
}

func TestDiscoverMongoSchemaRequiresExportPath(t *testing.T) {
	err := DiscoverMongoSchema(context.Background(), Options{DBName: "arivu_db", OutPath: filepath.Join(t.TempDir(), "manifest.json"), DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "export path") {
		t.Fatalf("expected export path error, got %v", err)
	}
}

func TestValidateExportFromCollectionObject(t *testing.T) {
	manifest := baselineManifest(Options{DBName: "arivu_db", DryRun: true})
	export := map[string]any{
		"users": []map[string]any{{
			"id":            "user-1",
			"email":         "user@example.com",
			"name":          "User",
			"password_hash": "$2b$12$hash",
			"created_at":    "2026-01-01T00:00:00Z",
		}},
		"bookmarks": []map[string]any{{
			"id":                "bookmark-1",
			"user_id":           "user-1",
			"url":               "https://example.com/a",
			"title":             "Example",
			"description":       "Example description",
			"favicon":           "https://example.com/favicon.ico",
			"thumbnail":         "https://example.com/thumb.png",
			"html_content":      "<article>Example</article>",
			"text_content":      "Example",
			"domain":            "example.com",
			"reading_time":      3,
			"read_status":       false,
			"source":            "x",
			"x_tweet_id":        "1",
			"x_author_username": "author",
			"x_author_name":     "Author",
			"x_tweet_url":       "https://x.com/author/status/1",
			"x_metrics":         map[string]any{"likes": 2},
			"embedding":         []float64{0.1, 0.2},
			"embedding_model":   "text-embedding-test",
			"entities":          []string{"Arivu"},
			"concepts":          []string{"bookmarking"},
			"access_history":    []map[string]any{{"accessed_at": "2026-01-02T00:00:00Z", "context": "detail"}},
			"version":           2,
			"last_accessed":     "2026-01-02T00:00:00Z",
			"view_count":        4,
			"created_at":        "2026-01-01T00:00:00Z",
			"updated_at":        "2026-01-02T00:00:00Z",
		}},
		"ai_summaries": []map[string]any{{
			"id":                "summary-1",
			"bookmark_id":       "bookmark-1",
			"user_id":           "user-1",
			"one_sentence":      "A summary.",
			"bullet_points":     []string{"one", "two"},
			"long_form":         "Long summary.",
			"highlights":        []string{"highlight"},
			"suggested_tags":    []string{"tag"},
			"processing_status": "completed",
			"created_at":        "2026-01-01T00:00:00Z",
			"updated_at":        "2026-01-01T00:00:00Z",
		}},
		"collections": []map[string]any{{
			"id":           "collection-1",
			"user_id":      "user-1",
			"name":         "Research",
			"description":  "Reading list",
			"color":        "#00aaff",
			"bookmark_ids": []string{"bookmark-1"},
			"created_at":   "2026-01-01T00:00:00Z",
			"updated_at":   "2026-01-01T00:00:00Z",
		}},
		"x_connections": []map[string]any{{
			"id":                "x-1",
			"user_id":           "user-1",
			"x_user_id":         "123",
			"x_username":        "user",
			"x_name":            "User",
			"x_profile_image":   "https://example.com/avatar.png",
			"access_token_enc":  "encrypted-access",
			"refresh_token_enc": "encrypted-refresh",
			"scopes":            []string{"tweet.read", "users.read"},
			"connected_at":      "2026-01-01T00:00:00Z",
			"last_sync_at":      "2026-01-02T00:00:00Z",
			"next_cursor":       "cursor",
			"sync_status":       "idle",
			"total_synced":      10,
		}},
		"instance_settings": []map[string]any{{
			"api_keys":              map[string]any{"gemini_api_key": "encrypted"},
			"gemini_api_key":        "encrypted",
			"resend_api_key":        "encrypted",
			"resend_from_email":     "hello@example.com",
			"x_client_id":           "client",
			"x_client_secret":       "encrypted",
			"x_redirect_uri":        "https://example.com/callback",
			"x_integration_enabled": true,
		}},
	}
	path := writeJSONFixture(t, export)
	samples, err := ValidateExport(context.Background(), manifest, path, 100)
	if err != nil {
		t.Fatalf("ValidateExport error = %v", err)
	}
	if samples["bookmarks"].Documents != 1 || samples["x_connections"].Documents != 1 {
		t.Fatalf("unexpected samples: %#v", samples)
	}
}

func TestValidateExportDirectorySupportsJSONLinesAndSampleLimit(t *testing.T) {
	manifest := baselineManifest(Options{DBName: "arivu_db", DryRun: true})
	dir := t.TempDir()
	data := strings.Join([]string{
		`{"id":"user-1","email":"one@example.com","created_at":"2026-01-01T00:00:00Z"}`,
		`{"id":"user-2","email":"two@example.com","created_at":"2026-01-01T00:00:00Z"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "users.jsonl"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	samples, err := ValidateExport(context.Background(), manifest, dir, 1)
	if err != nil {
		t.Fatalf("ValidateExport error = %v", err)
	}
	if samples["users"].Documents != 1 {
		t.Fatalf("expected sample limit to cap users at 1, got %#v", samples)
	}
}

func TestValidateExportRejectsUnknownCollection(t *testing.T) {
	manifest := baselineManifest(Options{DBName: "arivu_db", DryRun: true})
	path := writeJSONFixture(t, map[string]any{
		"surprise_collection": []map[string]any{{"id": "1"}},
	})
	_, err := ValidateExport(context.Background(), manifest, path, 100)
	if err == nil || !strings.Contains(err.Error(), "unknown collection surprise_collection") {
		t.Fatalf("expected unknown collection error, got %v", err)
	}
}

func TestDiscoverMongoSchemaWritesJSONExportSamples(t *testing.T) {
	dir := t.TempDir()
	exportPath := writeJSONFixture(t, map[string]any{
		"users": []map[string]any{{"id": "user-1", "email": "user@example.com", "created_at": "2026-01-01T00:00:00Z"}},
	})
	out := filepath.Join(dir, "manifest.json")
	err := DiscoverMongoSchema(context.Background(), Options{ExportPath: exportPath, DBName: "arivu_db", OutPath: out, DryRun: true})
	if err != nil {
		t.Fatalf("DiscoverMongoSchema error = %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Samples["users"].Documents != 1 {
		t.Fatalf("expected users sample count, got %#v", manifest.Samples)
	}
}

func writeJSONFixture(t *testing.T, value any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.json")
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
