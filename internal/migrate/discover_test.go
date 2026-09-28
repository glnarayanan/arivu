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
	manifest := baselineManifest(Options{DryRun: true})
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

func TestValidateDocumentRejectsMissingRequiredFields(t *testing.T) {
	manifest := baselineManifest(Options{DryRun: true})
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

func TestValidateExportDirectorySupportsJSONLinesAndSampleLimit(t *testing.T) {
	manifest := baselineManifest(Options{DryRun: true})
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
	manifest := baselineManifest(Options{DryRun: true})
	path := writeJSONFixture(t, map[string]any{
		"surprise_collection": []map[string]any{{"id": "1"}},
	})
	_, err := ValidateExport(context.Background(), manifest, path, 100)
	if err == nil || !strings.Contains(err.Error(), "unknown collection surprise_collection") {
		t.Fatalf("expected unknown collection error, got %v", err)
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
