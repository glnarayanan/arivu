package runtimeconfig

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/database"
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
		GeminiAPIKey:  "env-gemini",
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
	if err := service.Set(context.Background(), KeyGeminiAPIKey, "db-gemini", "admin@example.com", "test-key"); err != nil {
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
	if effective.GeminiAPIKey != "db-gemini" || effective.ResendAPIKey != "env-resend" || !effective.XIntegrationEnabled {
		t.Fatalf("unexpected effective config: %#v", effective)
	}
	if effective.AppURL != "https://runtime.example.test" || effective.SignupEnabled || effective.CookieSecure {
		t.Fatalf("unexpected runtime app config: %#v", effective)
	}
	if effective.ResendFromEmail != "hello@example.com" || effective.XRedirectURI != "https://runtime.example.test/settings?section=connections" {
		t.Fatalf("unexpected plain/default config: %#v", effective)
	}

	var cipher, plain, keyID sql.NullString
	if err := db.QueryRowContext(context.Background(), `SELECT value_cipher,value_plain,key_id FROM settings WHERE key=?`, KeyGeminiAPIKey).Scan(&cipher, &plain, &keyID); err != nil {
		t.Fatal(err)
	}
	if !cipher.Valid || plain.Valid || keyID.String != "test-key" {
		t.Fatalf("secret setting stored incorrectly: cipher=%v plain=%v keyID=%q", cipher.Valid, plain.Valid, keyID.String)
	}
	if opened, err := secrets.Open(cfg.SecretKey, cipher.String); err != nil || opened != "db-gemini" {
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
	if status[KeyGeminiAPIKey].MaskedValue != "****mini" || status[KeyGeminiAPIKey].Source != "database" {
		t.Fatalf("unexpected secret status: %#v", status[KeyGeminiAPIKey])
	}
	if status[KeySignupEnabled].Value != false || status[KeyCookieSecure].Value != false || status[KeyAppURL].Value != "https://runtime.example.test" {
		t.Fatalf("unexpected runtime status: %#v", status)
	}
	if status[KeyXRedirectURI].Value != "https://runtime.example.test/settings?section=connections" {
		t.Fatalf("unexpected x redirect status: %#v", status[KeyXRedirectURI])
	}

	if err := service.Set(context.Background(), KeyAppURL, "file:///tmp/arivu", "admin@example.com", ""); err == nil {
		t.Fatal("expected invalid app_url to fail")
	}

	if err := service.Delete(context.Background(), KeyGeminiAPIKey); err != nil {
		t.Fatal(err)
	}
	effective, err = service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.GeminiAPIKey != "env-gemini" {
		t.Fatalf("delete did not restore fallback: %#v", effective)
	}
}
