package migrate

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/secrets"
)

func TestApplyExportMigratesRowsAndRekeysSecrets(t *testing.T) {
	oldSecret := "old-secret-key-with-at-least-32-bytes"
	newSecret := "new-secret-key-with-at-least-32-bytes"
	export := map[string]any{
		"users": []map[string]any{{
			"id":            "user-1",
			"email":         "User@Example.com",
			"name":          "User",
			"password_hash": "$2b$12$hash",
			"created_at":    "2026-01-01T00:00:00Z",
		}},
		"bookmarks": []map[string]any{{
			"id":              "bookmark-1",
			"user_id":         "user-1",
			"url":             "https://example.com/article",
			"title":           "Example",
			"html_content":    `<article><script>alert(1)</script><p>Safe</p></article>`,
			"text_content":    "Safe",
			"embedding":       []float64{0.1, 0.2},
			"embedding_model": "text-embedding-test",
			"entities":        []string{"SQLite"},
			"concepts":        []string{"Migration"},
			"access_history":  []map[string]any{{"accessed_at": "2026-01-02T00:00:00Z", "context": "detail"}},
			"created_at":      "2026-01-01T00:00:00Z",
			"updated_at":      "2026-01-02T00:00:00Z",
		}},
		"ai_summaries": []map[string]any{{
			"id":                "summary-1",
			"bookmark_id":       "bookmark-1",
			"user_id":           "user-1",
			"one_sentence":      "A migrated summary.",
			"bullet_points":     []string{"one"},
			"processing_status": "completed",
		}},
		"collections": []map[string]any{{
			"id":           "collection-1",
			"user_id":      "user-1",
			"name":         "Research",
			"bookmark_ids": []string{"bookmark-1"},
		}},
		"x_connections": []map[string]any{{
			"id":                "x-1",
			"user_id":           "user-1",
			"x_user_id":         "x-user",
			"x_username":        "arivu",
			"access_token_enc":  fernetSealForTest(t, oldSecret, "x-access"),
			"refresh_token_enc": fernetSealForTest(t, oldSecret, "x-refresh"),
			"scopes":            []string{"tweet.read"},
		}},
		"instance_settings": []map[string]any{{
			"gemini_api_key":        fernetSealForTest(t, oldSecret, "gemini-key"),
			"resend_api_key":        fernetSealForTest(t, oldSecret, "resend-key"),
			"resend_from_email":     "hello@example.com",
			"x_client_id":           fernetSealForTest(t, oldSecret, "client-id"),
			"x_client_secret":       fernetSealForTest(t, oldSecret, "client-secret"),
			"x_redirect_uri":        "https://example.com/callback",
			"x_integration_enabled": true,
		}},
		"sessions": []map[string]any{{
			"id":         "legacy-session",
			"user_id":    "user-1",
			"token":      "legacy-token",
			"created_at": "2026-01-01T00:00:00Z",
		}},
	}
	dbPath := filepath.Join(t.TempDir(), "arivu.sqlite3")
	report, err := ApplyExport(context.Background(), ApplyOptions{
		ExportPath:    writeJSONFixture(t, export),
		DBPath:        dbPath,
		OldSecretKey:  oldSecret,
		NewSecretKey:  newSecret,
		KeyID:         "migration-2026",
		SampleLimit:   100,
		AllowExisting: false,
	})
	if err != nil {
		t.Fatalf("ApplyExport error = %v", err)
	}
	if report.Users != 1 || report.Bookmarks != 1 || report.XConnections != 1 || report.Settings != 7 || report.LegacySessionsDropped != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.SourceDocuments["users"] != 1 || report.SourceDocuments["bookmarks"] != 1 || report.Skipped["sessions"] != 1 {
		t.Fatalf("report missing source/skipped counts: %#v", report)
	}
	db, err := database.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var email string
	if err := db.QueryRowContext(context.Background(), `SELECT email FROM users WHERE id='user-1'`).Scan(&email); err != nil {
		t.Fatal(err)
	}
	if email != "user@example.com" {
		t.Fatalf("email was not normalized: %q", email)
	}
	var html string
	var embeddingDim int
	if err := db.QueryRowContext(context.Background(), `SELECT sanitized_html,embedding_dim FROM bookmarks WHERE id='bookmark-1'`).Scan(&html, &embeddingDim); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "script") || embeddingDim != 2 {
		t.Fatalf("unexpected migrated bookmark html=%q dim=%d", html, embeddingDim)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM collection_bookmarks WHERE collection_id='collection-1' AND bookmark_id='bookmark-1'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM bookmark_entities WHERE bookmark_id='bookmark-1' AND entity='SQLite'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM bookmark_concepts WHERE bookmark_id='bookmark-1' AND concept='Migration'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM bookmark_accesses WHERE bookmark_id='bookmark-1'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM sessions`, 0)
	var accessCipher string
	if err := db.QueryRowContext(context.Background(), `SELECT access_token_cipher FROM x_connections WHERE user_id='user-1'`).Scan(&accessCipher); err != nil {
		t.Fatal(err)
	}
	if got := openMigratedSecretForTest(t, newSecret, accessCipher); got != "x-access" {
		t.Fatalf("x token was not rekeyed: %q", got)
	}
	var settingCipher, settingPlain, keyID string
	if err := db.QueryRowContext(context.Background(), `SELECT COALESCE(value_cipher,''),COALESCE(value_plain,''),COALESCE(key_id,'') FROM settings WHERE key='gemini_api_key'`).Scan(&settingCipher, &settingPlain, &keyID); err != nil {
		t.Fatal(err)
	}
	if settingPlain != "" || keyID != "migration-2026" || openMigratedSecretForTest(t, newSecret, settingCipher) != "gemini-key" {
		t.Fatalf("secret setting was not rekeyed with key id: keyID=%q plain=%q", keyID, settingPlain)
	}
	if err := db.QueryRowContext(context.Background(), `SELECT COALESCE(value_cipher,''),COALESCE(value_plain,''),COALESCE(key_id,'') FROM settings WHERE key='x_integration_enabled'`).Scan(&settingCipher, &settingPlain, &keyID); err != nil {
		t.Fatal(err)
	}
	if settingCipher != "" || keyID != "" || settingPlain != "true" {
		t.Fatalf("boolean setting was not preserved plainly: keyID=%q cipher=%q plain=%q", keyID, settingCipher, settingPlain)
	}
	if err := db.QueryRowContext(context.Background(), `SELECT COALESCE(value_cipher,''),COALESCE(value_plain,'') FROM settings WHERE key='x_redirect_uri'`).Scan(&settingCipher, &settingPlain); err != nil {
		t.Fatal(err)
	}
	if settingCipher != "" || settingPlain != "https://example.com/callback" {
		t.Fatalf("redirect setting was not preserved plainly: cipher=%q plain=%q", settingCipher, settingPlain)
	}
}

func TestApplyExportRollsBackOnOrphanCollectionBookmark(t *testing.T) {
	export := map[string]any{
		"users": []map[string]any{{
			"id":         "user-1",
			"email":      "user@example.com",
			"created_at": "2026-01-01T00:00:00Z",
		}},
		"collections": []map[string]any{{
			"id":           "collection-1",
			"user_id":      "user-1",
			"name":         "Research",
			"bookmark_ids": []string{"missing-bookmark"},
		}},
	}
	dbPath := filepath.Join(t.TempDir(), "arivu.sqlite3")
	_, err := ApplyExport(context.Background(), ApplyOptions{
		ExportPath:   writeJSONFixture(t, export),
		DBPath:       dbPath,
		NewSecretKey: "new-secret-key-with-at-least-32-bytes",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown bookmark") {
		t.Fatalf("expected orphan bookmark error, got %v", err)
	}
	db, openErr := database.Open(context.Background(), dbPath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	assertCount(t, db, `SELECT COUNT(*) FROM users`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM collections`, 0)
}

func assertCount(t *testing.T, db *sql.DB, query string, expected int) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("%s count = %d, want %d", query, count, expected)
	}
}

func fernetSealForTest(t *testing.T, secretKey string, plaintext string) string {
	t.Helper()
	key := sha256.Sum256([]byte(secretKey))
	block, err := aes.NewCipher(key[16:])
	if err != nil {
		t.Fatal(err)
	}
	iv := bytes.Repeat([]byte{1}, aes.BlockSize)
	padded := pkcs7ForTest([]byte(plaintext))
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	body := []byte{0x80}
	timestamp := make([]byte, 8)
	binary.BigEndian.PutUint64(timestamp, uint64(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()))
	body = append(body, timestamp...)
	body = append(body, iv...)
	body = append(body, ciphertext...)
	mac := hmac.New(sha256.New, key[:16])
	_, _ = mac.Write(body)
	body = append(body, mac.Sum(nil)...)
	return base64.URLEncoding.EncodeToString(body)
}

func pkcs7ForTest(value []byte) []byte {
	padding := aes.BlockSize - (len(value) % aes.BlockSize)
	return append(value, bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func openMigratedSecretForTest(t *testing.T, secretKey, encoded string) string {
	t.Helper()
	plaintext, err := secrets.Open(secretKey, encoded)
	if err != nil {
		t.Fatal(err)
	}
	return plaintext
}
