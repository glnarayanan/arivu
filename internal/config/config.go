package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	Addr           string
	DBPath         string
	AppURL         string
	SecretKey      string
	AdminEmails    map[string]bool
	SignupEnabled  bool
	CookieSecure   bool
	GeminiAPIKey   string
	ResendAPIKey   string
	ResendFrom     string
	XEnabled       bool
	XClientID      string
	XClientSecret  string
	XRedirectURI   string
	XAPIBaseURL    string
	XAuthorizeURL  string
	SessionTTL     time.Duration
	RefreshTTL     time.Duration
	ExtensionTTL   time.Duration
	MaxRequestBody int64
}

func FromEnv() Config {
	return Config{
		Addr:           env("ARIVU_ADDR", ":8080"),
		DBPath:         env("ARIVU_DB", "arivu.sqlite3"),
		AppURL:         env("APP_URL", "http://localhost:8080"),
		SecretKey:      env("SECRET_KEY", "dev-only-change-me"),
		AdminEmails:    emailSet(os.Getenv("ADMIN_EMAILS")),
		SignupEnabled:  envBool("SIGNUPS_ENABLED", true),
		CookieSecure:   envBool("COOKIE_SECURE", false),
		GeminiAPIKey:   os.Getenv("GEMINI_API_KEY"),
		ResendAPIKey:   os.Getenv("RESEND_API_KEY"),
		ResendFrom:     os.Getenv("RESEND_FROM_EMAIL"),
		XEnabled:       envBool("X_INTEGRATION_ENABLED", false),
		XClientID:      os.Getenv("X_CLIENT_ID"),
		XClientSecret:  os.Getenv("X_CLIENT_SECRET"),
		XRedirectURI:   os.Getenv("X_REDIRECT_URI"),
		XAPIBaseURL:    env("X_API_BASE_URL", "https://api.twitter.com"),
		XAuthorizeURL:  env("X_AUTHORIZE_URL", "https://twitter.com/i/oauth2/authorize"),
		SessionTTL:     time.Hour,
		RefreshTTL:     30 * 24 * time.Hour,
		ExtensionTTL:   30 * 24 * time.Hour,
		MaxRequestBody: 10 << 20,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func emailSet(csv string) map[string]bool {
	result := map[string]bool{}
	for _, part := range strings.Split(csv, ",") {
		email := strings.ToLower(strings.TrimSpace(part))
		if email != "" {
			result[email] = true
		}
	}
	return result
}
