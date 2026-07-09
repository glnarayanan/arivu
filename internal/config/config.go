package config

import (
	"os"
	"strings"
	"time"
)

const (
	DefaultGeminiModel   = "gemini-2.5-flash"
	DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com"
)

type Config struct {
	Addr           string
	DBPath         string
	AppURL         string
	SecretKey      string
	AdminEmails    map[string]bool
	SignupEnabled  bool
	CookieSecure   bool
	AIProvider     string
	AIAPIKey       string
	AIModel        string
	AIBaseURL      string
	GeminiAPIKey   string
	GeminiModel    string
	GeminiBaseURL  string
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
	FetchUserAgent string
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
		AIProvider:     os.Getenv("AI_PROVIDER"),
		AIAPIKey:       os.Getenv("AI_API_KEY"),
		AIModel:        os.Getenv("AI_MODEL"),
		AIBaseURL:      os.Getenv("AI_BASE_URL"),
		GeminiAPIKey:   os.Getenv("GEMINI_API_KEY"),
		GeminiModel:    env("GEMINI_MODEL", DefaultGeminiModel),
		GeminiBaseURL:  os.Getenv("GEMINI_BASE_URL"),
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
		FetchUserAgent: env("ARIVU_FETCH_USER_AGENT", "Arivu/2.0"),
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
