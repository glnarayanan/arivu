package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	DefaultGeminiModel   = "gemini-2.5-flash"
	DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com"
)

type Config struct {
	Addr               string
	DBPath             string
	AppURL             string
	SecretKey          string
	AdminEmails        map[string]bool
	SignupEnabled      bool
	CookieSecure       bool
	AIProvider         string
	AIAPIKey           string
	AIModel            string
	AIBaseURL          string
	GeminiAPIKey       string
	GeminiModel        string
	GeminiBaseURL      string
	ResendAPIKey       string
	ResendFrom         string
	XEnabled           bool
	XClientID          string
	XClientSecret      string
	XRedirectURI       string
	XAPIBaseURL        string
	XAuthorizeURL      string
	SessionTTL         time.Duration
	RefreshTTL         time.Duration
	ExtensionTTL       time.Duration
	MaxRequestBody     int64
	FetchUserAgent     string
	ArtifactQuotaBytes int64
	AssetGCGrace       time.Duration
	BrowserCapture     BrowserCaptureConfig
}

type BrowserCaptureConfig struct {
	Enabled, Screenshot, PDF, SelfContainedHTML bool
	Command, Socket, RuntimeDir                 string
	Protocol                                    int
	Timeout, NavigationTimeout                  time.Duration
	MaxFileBytes, MaxTotalBytes                 int64
	MaxMediaFiles                               int
	MaxMediaFileBytes, MaxMediaTotalBytes       int64
}

func FromEnv() Config {
	browserProtocol := envInt("ARIVU_BROWSER_CAPTURE_PROTOCOL", 1)
	browserTimeout := 30 * time.Second
	browserMaxFileBytes := int64(10 << 20)
	browserMaxTotalBytes := int64(20 << 20)
	if browserProtocol == 2 {
		browserTimeout = 90 * time.Second
		browserMaxFileBytes = 50 << 20
		browserMaxTotalBytes = 100 << 20
	}
	return Config{
		Addr:               env("ARIVU_ADDR", ":8080"),
		DBPath:             env("ARIVU_DB", "arivu.sqlite3"),
		AppURL:             env("APP_URL", "http://localhost:8080"),
		SecretKey:          env("SECRET_KEY", "dev-only-change-me"),
		AdminEmails:        emailSet(os.Getenv("ADMIN_EMAILS")),
		SignupEnabled:      envBool("SIGNUPS_ENABLED", true),
		CookieSecure:       envBool("COOKIE_SECURE", false),
		AIProvider:         os.Getenv("AI_PROVIDER"),
		AIAPIKey:           os.Getenv("AI_API_KEY"),
		AIModel:            os.Getenv("AI_MODEL"),
		AIBaseURL:          os.Getenv("AI_BASE_URL"),
		GeminiAPIKey:       os.Getenv("GEMINI_API_KEY"),
		GeminiModel:        env("GEMINI_MODEL", DefaultGeminiModel),
		GeminiBaseURL:      os.Getenv("GEMINI_BASE_URL"),
		ResendAPIKey:       os.Getenv("RESEND_API_KEY"),
		ResendFrom:         os.Getenv("RESEND_FROM_EMAIL"),
		XEnabled:           envBool("X_INTEGRATION_ENABLED", false),
		XClientID:          os.Getenv("X_CLIENT_ID"),
		XClientSecret:      os.Getenv("X_CLIENT_SECRET"),
		XRedirectURI:       os.Getenv("X_REDIRECT_URI"),
		XAPIBaseURL:        env("X_API_BASE_URL", "https://api.twitter.com"),
		XAuthorizeURL:      env("X_AUTHORIZE_URL", "https://twitter.com/i/oauth2/authorize"),
		SessionTTL:         time.Hour,
		RefreshTTL:         30 * 24 * time.Hour,
		ExtensionTTL:       30 * 24 * time.Hour,
		MaxRequestBody:     10 << 20,
		FetchUserAgent:     env("ARIVU_FETCH_USER_AGENT", "Arivu/2.0"),
		ArtifactQuotaBytes: envInt64("ARIVU_ARTIFACT_QUOTA_BYTES", 1<<30),
		AssetGCGrace:       envDuration("ARIVU_ASSET_GC_GRACE", 24*time.Hour),
		BrowserCapture: BrowserCaptureConfig{
			Enabled:            envBool("ARIVU_BROWSER_CAPTURE_ENABLED", false),
			Screenshot:         envBool("ARIVU_BROWSER_CAPTURE_SCREENSHOT", browserProtocol == 2),
			PDF:                envBool("ARIVU_BROWSER_CAPTURE_PDF", false),
			SelfContainedHTML:  envBool("ARIVU_BROWSER_CAPTURE_SELF_CONTAINED_HTML", false),
			Command:            strings.TrimSpace(os.Getenv("ARIVU_BROWSER_CAPTURE_COMMAND")),
			Socket:             strings.TrimSpace(os.Getenv("ARIVU_BROWSER_CAPTURE_SOCKET")),
			RuntimeDir:         strings.TrimSpace(os.Getenv("ARIVU_BROWSER_CAPTURE_RUNTIME_DIR")),
			Protocol:           browserProtocol,
			Timeout:            envDuration("ARIVU_BROWSER_CAPTURE_TIMEOUT", browserTimeout),
			NavigationTimeout:  envDuration("ARIVU_BROWSER_CAPTURE_NAVIGATION_TIMEOUT", 30*time.Second),
			MaxFileBytes:       envInt64("ARIVU_BROWSER_CAPTURE_MAX_FILE_BYTES", browserMaxFileBytes),
			MaxTotalBytes:      envInt64("ARIVU_BROWSER_CAPTURE_MAX_TOTAL_BYTES", browserMaxTotalBytes),
			MaxMediaFiles:      envInt("ARIVU_BROWSER_CAPTURE_MAX_MEDIA_FILES", 40),
			MaxMediaFileBytes:  envInt64("ARIVU_BROWSER_CAPTURE_MAX_MEDIA_FILE_BYTES", 5<<20),
			MaxMediaTotalBytes: envInt64("ARIVU_BROWSER_CAPTURE_MAX_MEDIA_TOTAL_BYTES", 40<<20),
		},
	}
}

func (c Config) Validate() error {
	if c.BrowserCapture.Enabled && c.BrowserCapture.Protocol != 2 && !c.BrowserCapture.Screenshot && !c.BrowserCapture.PDF && !c.BrowserCapture.SelfContainedHTML {
		return errors.New("browser capture is enabled but no formats are requested")
	}
	if c.BrowserCapture.Enabled && c.BrowserCapture.Protocol == 2 && c.BrowserCapture.Socket == "" {
		return errors.New("browser capture protocol v2 requires a helper socket")
	}
	if c.BrowserCapture.Enabled && c.BrowserCapture.Protocol != 1 && c.BrowserCapture.Protocol != 2 {
		return errors.New("browser capture protocol must be 1 or 2")
	}
	return nil
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}
func envInt64(key string, fallback int64) int64 {
	var v int64
	if _, err := fmt.Sscan(os.Getenv(key), &v); err == nil && v > 0 {
		return v
	}
	return fallback
}
func envInt(key string, fallback int) int {
	value := envInt64(key, int64(fallback))
	if int64(int(value)) == value {
		return int(value)
	}
	return fallback
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
