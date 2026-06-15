package migrate

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/sanitize"
	"github.com/glnarayanan/arivu/internal/secrets"
)

type ApplyOptions struct {
	ExportPath    string
	DBPath        string
	OldSecretKey  string
	NewSecretKey  string
	KeyID         string
	SampleLimit   int
	DryRun        bool
	AllowExisting bool
}

type ApplyReport struct {
	Users                 int      `json:"users"`
	Bookmarks             int      `json:"bookmarks"`
	Summaries             int      `json:"summaries"`
	Collections           int      `json:"collections"`
	CollectionMemberships int      `json:"collection_memberships"`
	AccessEvents          int      `json:"access_events"`
	Entities              int      `json:"entities"`
	Concepts              int      `json:"concepts"`
	XConnections          int      `json:"x_connections"`
	Settings              int      `json:"settings"`
	LegacySessionsDropped int      `json:"legacy_sessions_dropped"`
	Warnings              []string `json:"warnings,omitempty"`
}

func ApplyExport(ctx context.Context, opts ApplyOptions) (ApplyReport, error) {
	if opts.ExportPath == "" {
		return ApplyReport{}, errors.New("json export path is required")
	}
	if opts.DBPath == "" {
		return ApplyReport{}, errors.New("sqlite db path is required")
	}
	if opts.NewSecretKey == "" {
		return ApplyReport{}, errors.New("new secret key is required")
	}
	if opts.KeyID == "" {
		opts.KeyID = "primary"
	}
	manifest := baselineManifest(Options{DryRun: opts.DryRun})
	if _, err := ValidateExport(ctx, manifest, opts.ExportPath, opts.SampleLimit); err != nil {
		return ApplyReport{}, err
	}
	export, err := loadExport(ctx, manifest, opts.ExportPath)
	if err != nil {
		return ApplyReport{}, err
	}
	db, err := database.Open(ctx, opts.DBPath)
	if err != nil {
		return ApplyReport{}, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ApplyReport{}, err
	}
	defer tx.Rollback()
	if !opts.AllowExisting {
		if err := assertEmptyTarget(ctx, tx); err != nil {
			return ApplyReport{}, err
		}
	}
	report, err := applyCollections(ctx, tx, export, opts)
	if err != nil {
		return ApplyReport{}, err
	}
	if opts.DryRun {
		return report, nil
	}
	if err := tx.Commit(); err != nil {
		return ApplyReport{}, err
	}
	return report, nil
}

func assertEmptyTarget(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"users", "bookmarks", "collections", "x_connections", "settings"} {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("target table %s is not empty; pass --allow-existing to merge into an existing database", table)
		}
	}
	return nil
}

func applyCollections(ctx context.Context, tx *sql.Tx, export map[string][]map[string]any, opts ApplyOptions) (ApplyReport, error) {
	report := ApplyReport{}
	now := time.Now().UTC().Format(time.RFC3339)
	if sessions, ok := export["sessions"]; ok {
		report.LegacySessionsDropped = len(sessions)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return report, err
	}
	userIDs := map[string]bool{}
	for _, doc := range export["users"] {
		id := cleanString(doc["id"])
		email := strings.ToLower(cleanString(doc["email"]))
		if id == "" || email == "" {
			return report, fmt.Errorf("user is missing id or email")
		}
		created := fallbackTime(doc["created_at"], now)
		updated := fallbackTime(doc["updated_at"], created)
		_, err := tx.ExecContext(ctx, `INSERT INTO users(id,email,name,password_hash,password_scheme,banned,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			id, email, cleanString(doc["name"]), nullable(cleanString(doc["password_hash"])), "bcrypt", boolValue(doc["banned"]), created, updated)
		if err != nil {
			return report, fmt.Errorf("insert user %s: %w", id, err)
		}
		userIDs[id] = true
		report.Users++
	}
	bookmarkOwners := map[string]string{}
	for _, doc := range export["bookmarks"] {
		id := cleanString(doc["id"])
		userID := cleanString(doc["user_id"])
		rawURL := cleanString(doc["url"])
		if id == "" || userID == "" || rawURL == "" {
			return report, fmt.Errorf("bookmark is missing id, user_id, or url")
		}
		if !userIDs[userID] {
			return report, fmt.Errorf("bookmark %s references unknown user %s", id, userID)
		}
		embedding, embeddingDim, err := embeddingBlob(doc["embedding"])
		if err != nil {
			return report, fmt.Errorf("bookmark %s embedding: %w", id, err)
		}
		metrics, err := jsonBlob(doc["x_metrics"])
		if err != nil {
			return report, fmt.Errorf("bookmark %s x_metrics: %w", id, err)
		}
		created := fallbackTime(doc["created_at"], now)
		updated := fallbackTime(doc["updated_at"], created)
		_, err = tx.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,description,favicon,thumbnail,sanitized_html,text_content,domain,reading_time,read_status,source,x_tweet_id,x_author_username,x_author_name,x_tweet_url,x_metrics_json,embedding,embedding_model,embedding_dim,version,last_accessed,view_count,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, userID, rawURL, nullable(cleanString(doc["title"])), nullable(cleanString(doc["description"])), nullable(cleanString(doc["favicon"])), nullable(cleanString(doc["thumbnail"])),
			nullable(sanitize.HTML(cleanString(doc["html_content"]))), nullable(cleanString(doc["text_content"])), fallbackString(cleanString(doc["domain"]), domainFromURL(rawURL)),
			intValue(doc["reading_time"]), boolValue(doc["read_status"]), fallbackString(cleanString(doc["source"]), "web"), nullable(cleanString(doc["x_tweet_id"])),
			nullable(cleanString(doc["x_author_username"])), nullable(cleanString(doc["x_author_name"])), nullable(cleanString(doc["x_tweet_url"])), nullable(string(metrics)),
			nullableBytes(embedding), nullable(cleanString(doc["embedding_model"])), embeddingDim, fallbackIntValue(doc["version"], 1), nullable(cleanString(doc["last_accessed"])), intValue(doc["view_count"]), created, updated)
		if err != nil {
			return report, fmt.Errorf("insert bookmark %s: %w", id, err)
		}
		bookmarkOwners[id] = userID
		report.Bookmarks++
		if n, err := insertTerms(ctx, tx, "bookmark_entities", "entity", id, userID, stringList(doc["entities"])); err != nil {
			return report, err
		} else {
			report.Entities += n
		}
		if n, err := insertTerms(ctx, tx, "bookmark_concepts", "concept", id, userID, stringList(doc["concepts"])); err != nil {
			return report, err
		} else {
			report.Concepts += n
		}
		if n, err := insertAccessHistory(ctx, tx, id, userID, doc["access_history"]); err != nil {
			return report, err
		} else {
			report.AccessEvents += n
		}
	}
	for _, doc := range export["ai_summaries"] {
		bookmarkID := cleanString(doc["bookmark_id"])
		if bookmarkID == "" || bookmarkOwners[bookmarkID] == "" {
			return report, fmt.Errorf("summary references unknown bookmark %s", bookmarkID)
		}
		userID := fallbackString(cleanString(doc["user_id"]), bookmarkOwners[bookmarkID])
		if userID != bookmarkOwners[bookmarkID] {
			return report, fmt.Errorf("summary for bookmark %s has mismatched user %s", bookmarkID, userID)
		}
		id := fallbackString(cleanString(doc["id"]), ids.New())
		bullets, _ := jsonBlob(doc["bullet_points"])
		highlights, _ := jsonBlob(doc["highlights"])
		tags, _ := jsonBlob(doc["suggested_tags"])
		created := fallbackTime(doc["created_at"], now)
		updated := fallbackTime(doc["updated_at"], created)
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,bullet_points_json,long_form,highlights_json,suggested_tags_json,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, bookmarkID, userID, nullable(cleanString(doc["one_sentence"])), fallbackString(string(bullets), "[]"), nullable(cleanString(doc["long_form"])), fallbackString(string(highlights), "[]"), fallbackString(string(tags), "[]"), fallbackString(cleanString(doc["processing_status"]), "completed"), created, updated)
		if err != nil {
			return report, fmt.Errorf("insert summary %s: %w", id, err)
		}
		report.Summaries++
	}
	for _, doc := range export["collections"] {
		id := cleanString(doc["id"])
		userID := cleanString(doc["user_id"])
		if id == "" || userID == "" || cleanString(doc["name"]) == "" {
			return report, fmt.Errorf("collection is missing id, user_id, or name")
		}
		if !userIDs[userID] {
			return report, fmt.Errorf("collection %s references unknown user %s", id, userID)
		}
		created := fallbackTime(doc["created_at"], now)
		updated := fallbackTime(doc["updated_at"], created)
		_, err := tx.ExecContext(ctx, `INSERT INTO collections(id,user_id,name,description,color,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, userID, cleanString(doc["name"]), nullable(cleanString(doc["description"])), nullable(cleanString(doc["color"])), created, updated)
		if err != nil {
			return report, fmt.Errorf("insert collection %s: %w", id, err)
		}
		report.Collections++
		for _, bookmarkID := range stringList(doc["bookmark_ids"]) {
			if bookmarkOwners[bookmarkID] == "" {
				return report, fmt.Errorf("collection %s references unknown bookmark %s", id, bookmarkID)
			}
			if bookmarkOwners[bookmarkID] != userID {
				return report, fmt.Errorf("collection %s references bookmark %s owned by another user", id, bookmarkID)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO collection_bookmarks(collection_id,bookmark_id,user_id,added_at) VALUES(?,?,?,?)`, id, bookmarkID, userID, created); err != nil {
				return report, err
			}
			report.CollectionMemberships++
		}
	}
	for _, doc := range export["x_connections"] {
		if err := insertXConnection(ctx, tx, doc, opts, userIDs, now); err != nil {
			return report, err
		}
		report.XConnections++
	}
	settings, err := insertSettings(ctx, tx, export["instance_settings"], opts, now)
	if err != nil {
		return report, err
	}
	report.Settings = settings
	return report, nil
}

func loadExport(ctx context.Context, manifest Manifest, exportPath string) (map[string][]map[string]any, error) {
	info, err := os.Stat(exportPath)
	if err != nil {
		return nil, err
	}
	result := map[string][]map[string]any{}
	if info.IsDir() {
		err := filepath.WalkDir(exportPath, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			collection, ok := collectionFromFilename(manifest, entry.Name())
			if !ok {
				return nil
			}
			docs, err := readCollectionDocs(ctx, manifest, collection, path)
			if err != nil {
				return err
			}
			result[collection] = append(result[collection], docs...)
			return nil
		})
		return result, err
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		return nil, err
	}
	var byCollection map[string]json.RawMessage
	if err := json.Unmarshal(data, &byCollection); err == nil && looksLikeCollectionExport(manifest, byCollection) {
		for collection, raw := range byCollection {
			docs, err := docsFromRaw(ctx, manifest, collection, raw)
			if err != nil {
				return nil, err
			}
			result[collection] = docs
		}
		return result, nil
	}
	collection, ok := collectionFromFilename(manifest, filepath.Base(exportPath))
	if !ok {
		return nil, fmt.Errorf("could not infer collection from %s", exportPath)
	}
	docs, err := readCollectionDocs(ctx, manifest, collection, exportPath)
	if err != nil {
		return nil, err
	}
	result[collection] = docs
	return result, nil
}

func readCollectionDocs(ctx context.Context, manifest Manifest, collection string, path string) ([]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	docs, err := docsFromRaw(ctx, manifest, collection, data)
	if err == nil {
		return docs, nil
	}
	if !strings.HasSuffix(strings.ToLower(path), ".jsonl") && !strings.HasSuffix(strings.ToLower(path), ".ndjson") {
		return nil, err
	}
	file, openErr := os.Open(path)
	if openErr != nil {
		return nil, openErr
	}
	defer file.Close()
	var result []map[string]any
	decoder := json.NewDecoder(file)
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if err := ValidateDocument(manifest, collection, doc); err != nil {
			return nil, err
		}
		result = append(result, doc)
	}
	return result, nil
}

func docsFromRaw(ctx context.Context, manifest Manifest, collection string, raw []byte) ([]map[string]any, error) {
	var docs []map[string]any
	if err := json.Unmarshal(raw, &docs); err != nil {
		var one map[string]any
		if oneErr := json.Unmarshal(raw, &one); oneErr != nil {
			return nil, fmt.Errorf("invalid %s export: expected object or array of objects", collection)
		}
		docs = []map[string]any{one}
	}
	for _, doc := range docs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if err := ValidateDocument(manifest, collection, doc); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

func insertTerms(ctx context.Context, tx *sql.Tx, table, column, bookmarkID, userID string, terms []string) (int, error) {
	count := 0
	for _, term := range terms {
		if term == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT OR IGNORE INTO %s(bookmark_id,user_id,%s) VALUES(?,?,?)`, table, column), bookmarkID, userID, term); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func insertAccessHistory(ctx context.Context, tx *sql.Tx, bookmarkID, userID string, raw any) (int, error) {
	entries, ok := raw.([]any)
	if !ok {
		return 0, nil
	}
	count := 0
	for _, entry := range entries {
		when := cleanString(entry)
		contextValue := "migration"
		if doc, ok := entry.(map[string]any); ok {
			when = fallbackString(cleanString(doc["accessed_at"]), cleanString(doc["timestamp"]))
			contextValue = fallbackString(cleanString(doc["context"]), "migration")
		}
		if when == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bookmark_accesses(id,bookmark_id,user_id,accessed_at,context) VALUES(?,?,?,?,?)`, ids.New(), bookmarkID, userID, when, contextValue); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func insertXConnection(ctx context.Context, tx *sql.Tx, doc map[string]any, opts ApplyOptions, userIDs map[string]bool, now string) error {
	userID := cleanString(doc["user_id"])
	if userID == "" || !userIDs[userID] {
		return fmt.Errorf("x connection references unknown user %s", userID)
	}
	access, err := migrateSecret(cleanString(doc["access_token_enc"]), opts)
	if err != nil {
		return fmt.Errorf("x connection %s access token: %w", userID, err)
	}
	refresh := ""
	if cleanString(doc["refresh_token_enc"]) != "" {
		refresh, err = migrateSecret(cleanString(doc["refresh_token_enc"]), opts)
		if err != nil {
			return fmt.Errorf("x connection %s refresh token: %w", userID, err)
		}
	}
	scopes, _ := jsonBlob(doc["scopes"])
	_, err = tx.ExecContext(ctx, `INSERT INTO x_connections(id,user_id,x_user_id,x_username,x_name,x_profile_image,access_token_cipher,refresh_token_cipher,token_expires_at,scopes_json,connected_at,last_sync_at,sync_status,total_synced,next_cursor) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		fallbackString(cleanString(doc["id"]), ids.New()), userID, nullable(cleanString(doc["x_user_id"])), nullable(cleanString(doc["x_username"])), nullable(cleanString(doc["x_name"])), nullable(cleanString(doc["x_profile_image"])),
		access, nullable(refresh), nullable(cleanString(doc["token_expires_at"])), fallbackString(string(scopes), "[]"), fallbackTime(doc["connected_at"], now), nullable(cleanString(doc["last_sync_at"])), fallbackString(cleanString(doc["sync_status"]), "idle"), intValue(doc["total_synced"]), nullable(cleanString(doc["next_cursor"])))
	return err
}

func insertSettings(ctx context.Context, tx *sql.Tx, docs []map[string]any, opts ApplyOptions, now string) (int, error) {
	count := 0
	encryptedKeys := map[string]bool{"gemini_api_key": true, "x_client_id": true, "x_client_secret": true, "resend_api_key": true}
	for _, doc := range docs {
		for _, key := range []string{"gemini_api_key", "resend_api_key", "resend_from_email", "x_client_id", "x_client_secret", "x_redirect_uri", "x_integration_enabled"} {
			value, ok := doc[key]
			if !ok || value == nil || value == "" {
				continue
			}
			raw := settingString(value)
			var err error
			if encryptedKeys[key] {
				raw, err = decryptLegacySecret(raw, opts.OldSecretKey)
				if err != nil {
					return count, fmt.Errorf("setting %s: %w", key, err)
				}
			}
			ciphertext, err := sealNewSecret(opts.NewSecretKey, raw)
			if err != nil {
				return count, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value_cipher,key_id,updated_by,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET value_cipher=excluded.value_cipher,key_id=excluded.key_id,updated_by=excluded.updated_by,updated_at=excluded.updated_at`, key, ciphertext, opts.KeyID, "migration", now); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

func migrateSecret(ciphertext string, opts ApplyOptions) (string, error) {
	plaintext, err := decryptLegacySecret(ciphertext, opts.OldSecretKey)
	if err != nil {
		return "", err
	}
	return sealNewSecret(opts.NewSecretKey, plaintext)
}

func decryptLegacySecret(token string, oldSecretKey string) (string, error) {
	if oldSecretKey == "" {
		return "", errors.New("old secret key is required")
	}
	key := legacyFernetKey(oldSecretKey)
	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(token)
	}
	if err != nil {
		return "", err
	}
	if len(raw) < 1+8+16+32 || raw[0] != 0x80 {
		return "", errors.New("invalid Fernet token")
	}
	body := raw[:len(raw)-32]
	mac := raw[len(raw)-32:]
	h := hmac.New(sha256.New, key[:16])
	_, _ = h.Write(body)
	if !hmac.Equal(mac, h.Sum(nil)) {
		return "", errors.New("invalid Fernet token signature")
	}
	_ = binary.BigEndian.Uint64(raw[1:9])
	iv := raw[9:25]
	ciphertext := raw[25 : len(raw)-32]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", errors.New("invalid Fernet ciphertext")
	}
	block, err := aes.NewCipher(key[16:])
	if err != nil {
		return "", err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	plaintext, err = unpadPKCS7(plaintext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func sealNewSecret(secretKey, plaintext string) (string, error) {
	key, err := secrets.EncryptionKey(secretKey)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

func unpadPKCS7(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, errors.New("empty padded value")
	}
	padding := int(value[len(value)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(value) {
		return nil, errors.New("invalid padding")
	}
	for _, b := range value[len(value)-padding:] {
		if int(b) != padding {
			return nil, errors.New("invalid padding")
		}
	}
	return value[:len(value)-padding], nil
}

func legacyFernetKey(secretKey string) []byte {
	// The legacy Python app encrypted provider tokens with a Fernet-compatible
	// key derived this way. Migration must reproduce it exactly so existing
	// encrypted tokens can be re-keyed into the v2 HKDF/AES-GCM format.
	sum := sha256.Sum256([]byte(secretKey))
	return sum[:]
}

func embeddingBlob(value any) ([]byte, int, error) {
	if value == nil {
		return nil, 0, nil
	}
	values, ok := floatList(value)
	if !ok {
		return nil, 0, errors.New("expected numeric vector")
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, 0, err
	}
	return raw, len(values), nil
}

func jsonBlob(value any) ([]byte, error) {
	if value == nil {
		return []byte("[]"), nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return []byte("[]"), nil
	}
	return raw, nil
}

func cleanString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func settingString(value any) string {
	if raw := cleanString(value); raw != "" {
		return raw
	}
	switch typed := value.(type) {
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%g", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	default:
		raw, err := json.Marshal(value)
		if err != nil || bytes.Equal(raw, []byte("null")) {
			return ""
		}
		return string(raw)
	}
}

func stringList(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	result := []string{}
	seen := map[string]bool{}
	for _, item := range raw {
		value := cleanString(item)
		if value != "" && !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result
}

func floatList(value any) ([]float64, bool) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]float64, 0, len(raw))
	for _, item := range raw {
		switch typed := item.(type) {
		case float64:
			result = append(result, typed)
		case int:
			result = append(result, float64(typed))
		default:
			return nil, false
		}
	}
	return result, true
}

func boolValue(value any) bool {
	raw, _ := value.(bool)
	return raw
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func fallbackIntValue(value any, fallback int) int {
	if result := intValue(value); result != 0 {
		return result
	}
	return fallback
}

func fallbackTime(value any, fallback string) string {
	raw := cleanString(value)
	if raw == "" {
		return fallback
	}
	return raw
}

func fallbackString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func domainFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
