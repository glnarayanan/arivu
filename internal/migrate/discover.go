package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Options struct {
	ExportPath  string
	OutPath     string
	DryRun      bool
	SampleLimit int
}

type Manifest struct {
	GeneratedAt string             `json:"generated_at"`
	DryRun      bool               `json:"dry_run"`
	Collections map[string][]Field `json:"collections"`
	Samples     map[string]Sample  `json:"samples,omitempty"`
	Notes       []string           `json:"notes"`
}

type Field struct {
	Name        string `json:"name"`
	Action      string `json:"action"`
	Target      string `json:"target"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

type Sample struct {
	Documents int `json:"documents"`
}

func DiscoverLegacyExport(ctx context.Context, opts Options) error {
	if opts.ExportPath == "" {
		return errors.New("json export path is required")
	}
	manifest := baselineManifest(opts)
	samples, err := ValidateExport(ctx, manifest, opts.ExportPath, opts.SampleLimit)
	if err != nil {
		return err
	}
	manifest.Samples = samples
	manifest.Notes = append(manifest.Notes, "JSON export samples were validated against the field allowlist.")
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(opts.OutPath, raw, 0o600)
}

func ValidateDocument(manifest Manifest, collection string, doc map[string]any) error {
	fields, ok := manifest.Collections[collection]
	if !ok {
		return fmt.Errorf("unknown collection %s", collection)
	}
	known := map[string]bool{"_id": true}
	for _, field := range fields {
		known[field.Name] = true
	}
	for key := range doc {
		if !known[key] {
			return fmt.Errorf("unknown field %s.%s", collection, key)
		}
	}
	for _, field := range fields {
		if !field.Required {
			continue
		}
		value, ok := doc[field.Name]
		if !ok || value == nil || value == "" {
			return fmt.Errorf("missing required field %s.%s", collection, field.Name)
		}
	}
	return nil
}

func ValidateExport(ctx context.Context, manifest Manifest, exportPath string, sampleLimit int) (map[string]Sample, error) {
	info, err := os.Stat(exportPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return validateExportDirectory(ctx, manifest, exportPath, sampleLimit)
	}
	return validateExportFile(ctx, manifest, exportPath, sampleLimit)
}

func validateExportDirectory(ctx context.Context, manifest Manifest, dir string, sampleLimit int) (map[string]Sample, error) {
	samples := map[string]Sample{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		collection, ok := collectionFromFilename(manifest, entry.Name())
		if !ok {
			return nil
		}
		fileSamples, err := validateCollectionFile(ctx, manifest, collection, path, sampleLimit)
		if err != nil {
			return err
		}
		samples[collection] = Sample{Documents: samples[collection].Documents + fileSamples.Documents}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return samples, nil
}

func validateExportFile(ctx context.Context, manifest Manifest, path string, sampleLimit int) (map[string]Sample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var byCollection map[string]json.RawMessage
	if err := json.Unmarshal(data, &byCollection); err == nil && looksLikeCollectionExport(manifest, byCollection) {
		samples := map[string]Sample{}
		for collection, raw := range byCollection {
			count, err := validateCollectionRaw(ctx, manifest, collection, raw, sampleLimit)
			if err != nil {
				return nil, err
			}
			samples[collection] = Sample{Documents: count}
		}
		return samples, nil
	} else if err == nil && looksLikeUnknownCollectionExport(manifest, byCollection) {
		for collection := range byCollection {
			if _, ok := manifest.Collections[collection]; !ok {
				return nil, fmt.Errorf("unknown collection %s", collection)
			}
		}
	}

	collection, ok := collectionFromFilename(manifest, filepath.Base(path))
	if !ok {
		return nil, fmt.Errorf("could not infer collection from %s", path)
	}
	sample, err := validateCollectionFile(ctx, manifest, collection, path, sampleLimit)
	if err != nil {
		return nil, err
	}
	return map[string]Sample{collection: sample}, nil
}

func validateCollectionFile(ctx context.Context, manifest Manifest, collection string, path string, sampleLimit int) (Sample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Sample{}, err
	}
	count, err := validateCollectionRaw(ctx, manifest, collection, data, sampleLimit)
	if err == nil {
		return Sample{Documents: count}, nil
	}
	if !strings.HasSuffix(strings.ToLower(path), ".jsonl") && !strings.HasSuffix(strings.ToLower(path), ".ndjson") {
		return Sample{}, err
	}
	file, openErr := os.Open(path)
	if openErr != nil {
		return Sample{}, openErr
	}
	defer file.Close()
	count, err = validateCollectionJSONLines(ctx, manifest, collection, file, sampleLimit)
	if err != nil {
		return Sample{}, err
	}
	return Sample{Documents: count}, nil
}

func validateCollectionRaw(ctx context.Context, manifest Manifest, collection string, raw []byte, sampleLimit int) (int, error) {
	var docs []map[string]any
	if err := json.Unmarshal(raw, &docs); err != nil {
		var one map[string]any
		if oneErr := json.Unmarshal(raw, &one); oneErr != nil {
			return 0, fmt.Errorf("invalid %s export: expected object or array of objects", collection)
		}
		docs = []map[string]any{one}
	}
	return validateDocuments(ctx, manifest, collection, docs, sampleLimit)
}

func validateCollectionJSONLines(ctx context.Context, manifest Manifest, collection string, reader io.Reader, sampleLimit int) (int, error) {
	decoder := json.NewDecoder(reader)
	var docs []map[string]any
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
		docs = append(docs, doc)
		if sampleLimit > 0 && len(docs) >= sampleLimit {
			break
		}
	}
	return validateDocuments(ctx, manifest, collection, docs, sampleLimit)
}

func validateDocuments(ctx context.Context, manifest Manifest, collection string, docs []map[string]any, sampleLimit int) (int, error) {
	limit := len(docs)
	if sampleLimit > 0 && sampleLimit < limit {
		limit = sampleLimit
	}
	for i := 0; i < limit; i++ {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		if err := ValidateDocument(manifest, collection, docs[i]); err != nil {
			return 0, err
		}
	}
	return limit, nil
}

func looksLikeCollectionExport(manifest Manifest, values map[string]json.RawMessage) bool {
	if len(values) == 0 {
		return false
	}
	for key := range values {
		if _, ok := manifest.Collections[key]; !ok {
			return false
		}
	}
	return true
}

func looksLikeUnknownCollectionExport(manifest Manifest, values map[string]json.RawMessage) bool {
	if len(values) == 0 {
		return false
	}
	for key, raw := range values {
		if _, ok := manifest.Collections[key]; ok {
			return true
		}
		trimmed := strings.TrimSpace(string(raw))
		if strings.HasPrefix(trimmed, "[") {
			return true
		}
	}
	return false
}

func collectionFromFilename(manifest Manifest, name string) (string, bool) {
	base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".jsonl"), ".ndjson"), ".json")
	if _, ok := manifest.Collections[base]; ok {
		return base, true
	}
	collections := make([]string, 0, len(manifest.Collections))
	for collection := range manifest.Collections {
		collections = append(collections, collection)
	}
	sort.Strings(collections)
	for _, collection := range collections {
		if strings.HasPrefix(base, collection+".") || strings.HasPrefix(base, collection+"-") {
			return collection, true
		}
	}
	return "", false
}

func baselineManifest(opts Options) Manifest {
	return Manifest{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		DryRun:      opts.DryRun,
		Notes: []string{
			"Baseline manifest generated from a legacy JSON export.",
			"Migration must compare exported document keys against this allowlist and fail dry-run on unknown fields.",
			"Existing sessions are intentionally not migrated; users reauthenticate after cutover.",
		},
		Collections: map[string][]Field{
			"users": {
				{"id", "column", "users.id", true, "Stable public user identifier"},
				{"email", "column", "users.email", true, "Login email"},
				{"name", "column", "users.name", false, "Display name"},
				{"password_hash", "column", "users.password_hash", false, "Legacy bcrypt retained until next login rehash"},
				{"created_at", "column", "users.created_at", true, "Original creation timestamp"},
				{"updated_at", "column", "users.updated_at", false, "Original update timestamp"},
				{"banned", "column", "users.banned", false, "Admin access control state"},
			},
			"bookmarks": {
				{"id", "column", "bookmarks.id", true, "Stable bookmark identifier"},
				{"user_id", "column", "bookmarks.user_id", true, "FK to users"},
				{"url", "column", "bookmarks.url", true, "Canonical saved URL"},
				{"title", "column", "bookmarks.title", false, "Bookmark title"},
				{"description", "column", "bookmarks.description", false, "Bookmark description"},
				{"favicon", "column", "bookmarks.favicon", false, "Bookmark favicon URL or data URI"},
				{"thumbnail", "column", "bookmarks.thumbnail", false, "Bookmark thumbnail URL or data URI"},
				{"html_content", "derived", "bookmarks.sanitized_html", false, "Sanitized during migration"},
				{"text_content", "column", "bookmarks.text_content", false, "Extracted readable text"},
				{"domain", "column", "bookmarks.domain", false, "Source domain"},
				{"reading_time", "column", "bookmarks.reading_time", false, "Estimated reading minutes"},
				{"read_status", "column", "bookmarks.read_status", false, "Read/unread state"},
				{"source", "column", "bookmarks.source", false, "Bookmark source such as web or x"},
				{"x_tweet_id", "column", "bookmarks.x_tweet_id", false, "Linked X bookmark tweet id"},
				{"x_author_username", "column", "bookmarks.x_author_username", false, "Linked X author username"},
				{"x_author_name", "column", "bookmarks.x_author_name", false, "Linked X author display name"},
				{"x_tweet_url", "column", "bookmarks.x_tweet_url", false, "Canonical X tweet URL"},
				{"x_metrics", "JSON blob", "bookmarks.x_metrics_json", false, "Preserved X metrics payload"},
				{"embedding", "column", "bookmarks.embedding", false, "Validated vector blob"},
				{"embedding_model", "column", "bookmarks.embedding_model", false, "Embedding provider/model version"},
				{"entities", "join table", "bookmark_entities", false, "One row per entity"},
				{"concepts", "join table", "bookmark_concepts", false, "One row per concept"},
				{"access_history", "join table", "bookmark_accesses", false, "One row per access event"},
				{"version", "column", "bookmarks.version", false, "Optimistic-locking version"},
				{"last_accessed", "column", "bookmarks.last_accessed", false, "Most recent access timestamp"},
				{"view_count", "column", "bookmarks.view_count", false, "Access count"},
				{"created_at", "column", "bookmarks.created_at", true, "Original creation timestamp"},
				{"updated_at", "column", "bookmarks.updated_at", true, "Original update timestamp"},
			},
			"ai_summaries": {
				{"id", "column", "ai_summaries.id", false, "Stable summary identifier when present"},
				{"bookmark_id", "column", "ai_summaries.bookmark_id", true, "Unique FK to bookmarks"},
				{"user_id", "column", "ai_summaries.user_id", false, "Owner copied from source summary when present"},
				{"one_sentence", "column", "ai_summaries.one_sentence", false, "AI summary"},
				{"bullet_points", "JSON blob", "ai_summaries.bullet_points_json", false, "Preserved ordered bullets"},
				{"long_form", "column", "ai_summaries.long_form", false, "Long-form AI summary"},
				{"highlights", "JSON blob", "ai_summaries.highlights_json", false, "Preserved highlights"},
				{"suggested_tags", "JSON blob", "ai_summaries.suggested_tags_json", false, "Preserved suggested tags"},
				{"processing_status", "column", "ai_summaries.processing_status", false, "Summary processing state"},
				{"created_at", "column", "ai_summaries.created_at", false, "Original creation timestamp"},
				{"updated_at", "column", "ai_summaries.updated_at", false, "Original update timestamp"},
			},
			"collections": {
				{"id", "column", "collections.id", true, "Stable collection identifier"},
				{"user_id", "column", "collections.user_id", true, "FK to users"},
				{"name", "column", "collections.name", true, "Collection name"},
				{"description", "column", "collections.description", false, "Collection description"},
				{"color", "column", "collections.color", false, "Collection display color"},
				{"bookmark_ids", "join table", "collection_bookmarks", false, "Membership is normalized"},
				{"created_at", "column", "collections.created_at", false, "Original creation timestamp"},
				{"updated_at", "column", "collections.updated_at", false, "Original update timestamp"},
			},
			"x_connections": {
				{"id", "column", "x_connections.id", false, "Stable connection identifier when present"},
				{"user_id", "column", "x_connections.user_id", true, "FK to users"},
				{"x_user_id", "column", "x_connections.x_user_id", false, "Provider user id"},
				{"x_username", "column", "x_connections.x_username", false, "Provider username"},
				{"x_name", "column", "x_connections.x_name", false, "Provider display name"},
				{"x_profile_image", "column", "x_connections.x_profile_image", false, "Provider avatar"},
				{"access_token_enc", "derived", "x_connections.access_token_cipher", true, "Requires old SECRET_KEY then re-encryption"},
				{"refresh_token_enc", "derived", "x_connections.refresh_token_cipher", false, "Requires old SECRET_KEY then re-encryption"},
				{"scopes", "JSON blob", "x_connections.scopes_json", false, "Granted X OAuth scopes"},
				{"connected_at", "column", "x_connections.connected_at", false, "Connection timestamp"},
				{"last_sync_at", "column", "x_connections.last_sync_at", false, "Last successful sync timestamp"},
				{"next_cursor", "column", "x_connections.next_cursor", false, "Sync continuation state"},
				{"sync_status", "column", "x_connections.sync_status", false, "Current sync state"},
				{"total_synced", "column", "x_connections.total_synced", false, "Total synced bookmark count"},
			},
			"instance_settings": {
				{"api_keys", "derived", "settings", false, "Encrypted settings are re-keyed with key IDs"},
				{"gemini_api_key", "derived", "settings.gemini_api_key", false, "Runtime Gemini override"},
				{"resend_api_key", "derived", "settings.resend_api_key", false, "Runtime Resend override"},
				{"resend_from_email", "derived", "settings.resend_from_email", false, "Runtime Resend sender override"},
				{"x_client_id", "derived", "settings.x_client_id", false, "Runtime X OAuth client id override"},
				{"x_client_secret", "derived", "settings.x_client_secret", false, "Runtime X OAuth client secret override"},
				{"x_redirect_uri", "derived", "settings.x_redirect_uri", false, "Runtime X OAuth redirect URI override"},
				{"x_integration_enabled", "column", "settings.x_integration_enabled", false, "Runtime X enablement override"},
			},
			"sessions": {
				{"id", "dropped with reason", "not migrated", false, "Legacy sessions are invalidated intentionally"},
				{"user_id", "dropped with reason", "not migrated", false, "Users reauthenticate after cutover"},
				{"token", "dropped with reason", "not migrated", false, "Legacy bearer/cookie tokens are not trusted after migration"},
				{"refresh_token", "dropped with reason", "not migrated", false, "Refresh tokens are not migrated"},
				{"created_at", "dropped with reason", "not migrated", false, "Session timestamps are historical only"},
				{"expires_at", "dropped with reason", "not migrated", false, "Session timestamps are historical only"},
			},
		},
	}
}
