package runtimeconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/secrets"
)

const (
	KeyAppURL             = "app_url"
	KeySignupEnabled      = "signups_enabled"
	KeyCookieSecure       = "cookie_secure"
	KeyGeminiAPIKey       = "gemini_api_key"
	KeyGeminiModel        = "gemini_model"
	KeyGeminiBaseURL      = "gemini_base_url"
	KeyResendAPIKey       = "resend_api_key"
	KeyResendFromEmail    = "resend_from_email"
	KeyXClientID          = "x_client_id"
	KeyXClientSecret      = "x_client_secret"
	KeyXRedirectURI       = "x_redirect_uri"
	KeyXIntegrationEnable = "x_integration_enabled"
)

var Keys = []string{
	KeyAppURL,
	KeySignupEnabled,
	KeyCookieSecure,
	KeyGeminiAPIKey,
	KeyGeminiModel,
	KeyGeminiBaseURL,
	KeyResendAPIKey,
	KeyResendFromEmail,
	KeyXClientID,
	KeyXClientSecret,
	KeyXRedirectURI,
	KeyXIntegrationEnable,
}

var SecretKeys = map[string]bool{
	KeyGeminiAPIKey:  true,
	KeyResendAPIKey:  true,
	KeyXClientID:     true,
	KeyXClientSecret: true,
}

var BooleanKeys = map[string]bool{
	KeySignupEnabled:      true,
	KeyCookieSecure:       true,
	KeyXIntegrationEnable: true,
}

type Service struct {
	db  *sql.DB
	cfg config.Config
}

type Effective struct {
	AppURL              string
	SignupEnabled       bool
	CookieSecure        bool
	GeminiAPIKey        string
	GeminiModel         string
	GeminiBaseURL       string
	ResendAPIKey        string
	ResendFromEmail     string
	XClientID           string
	XClientSecret       string
	XRedirectURI        string
	XIntegrationEnabled bool
}

type Value struct {
	Configured  bool   `json:"configured"`
	MaskedValue string `json:"masked_value,omitempty"`
	Value       any    `json:"value,omitempty"`
	Source      string `json:"source"`
	KeyID       string `json:"key_id,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type resolvedValue struct {
	key       string
	value     string
	source    string
	keyID     string
	updatedAt string
}

func New(db *sql.DB, cfg config.Config) *Service {
	return &Service{db: db, cfg: cfg}
}

func FromConfig(cfg config.Config) Effective {
	return Effective{
		AppURL:              cfg.AppURL,
		SignupEnabled:       cfg.SignupEnabled,
		CookieSecure:        cfg.CookieSecure,
		GeminiAPIKey:        cfg.GeminiAPIKey,
		GeminiModel:         fallbackString(cfg.GeminiModel, "gemini-2.5-flash"),
		GeminiBaseURL:       cfg.GeminiBaseURL,
		ResendAPIKey:        cfg.ResendAPIKey,
		ResendFromEmail:     cfg.ResendFrom,
		XClientID:           cfg.XClientID,
		XClientSecret:       cfg.XClientSecret,
		XRedirectURI:        defaultXRedirectURI(cfg.AppURL),
		XIntegrationEnabled: cfg.XEnabled,
	}
}

func Allowed(key string) bool {
	for _, candidate := range Keys {
		if key == candidate {
			return true
		}
	}
	return false
}

func IsSecret(key string) bool {
	return SecretKeys[key]
}

func IsBoolean(key string) bool {
	return BooleanKeys[key]
}

func (s *Service) Effective(ctx context.Context) (Effective, error) {
	appURL, err := s.resolve(ctx, KeyAppURL)
	if err != nil {
		return Effective{}, err
	}
	signups, err := s.resolve(ctx, KeySignupEnabled)
	if err != nil {
		return Effective{}, err
	}
	cookieSecure, err := s.resolve(ctx, KeyCookieSecure)
	if err != nil {
		return Effective{}, err
	}
	gemini, err := s.resolve(ctx, KeyGeminiAPIKey)
	if err != nil {
		return Effective{}, err
	}
	geminiModel, err := s.resolve(ctx, KeyGeminiModel)
	if err != nil {
		return Effective{}, err
	}
	geminiBaseURL, err := s.resolve(ctx, KeyGeminiBaseURL)
	if err != nil {
		return Effective{}, err
	}
	resendKey, err := s.resolve(ctx, KeyResendAPIKey)
	if err != nil {
		return Effective{}, err
	}
	resendFrom, err := s.resolve(ctx, KeyResendFromEmail)
	if err != nil {
		return Effective{}, err
	}
	xClientID, err := s.resolve(ctx, KeyXClientID)
	if err != nil {
		return Effective{}, err
	}
	xClientSecret, err := s.resolve(ctx, KeyXClientSecret)
	if err != nil {
		return Effective{}, err
	}
	xRedirect, err := s.resolve(ctx, KeyXRedirectURI)
	if err != nil {
		return Effective{}, err
	}
	if xRedirect.source == "default" {
		xRedirect.value = defaultXRedirectURI(appURL.value)
	}
	xEnabled, err := s.resolve(ctx, KeyXIntegrationEnable)
	if err != nil {
		return Effective{}, err
	}
	return Effective{
		AppURL:              appURL.value,
		SignupEnabled:       parseBool(signups.value),
		CookieSecure:        parseBool(cookieSecure.value),
		GeminiAPIKey:        gemini.value,
		GeminiModel:         geminiModel.value,
		GeminiBaseURL:       geminiBaseURL.value,
		ResendAPIKey:        resendKey.value,
		ResendFromEmail:     resendFrom.value,
		XClientID:           xClientID.value,
		XClientSecret:       xClientSecret.value,
		XRedirectURI:        xRedirect.value,
		XIntegrationEnabled: parseBool(xEnabled.value),
	}, nil
}

func (s *Service) Status(ctx context.Context) (map[string]Value, error) {
	result := map[string]Value{}
	appURL, err := s.resolve(ctx, KeyAppURL)
	if err != nil {
		return nil, err
	}
	for _, key := range Keys {
		value := appURL
		if key != KeyAppURL {
			var err error
			value, err = s.resolve(ctx, key)
			if err != nil {
				return nil, err
			}
		}
		if key == KeyXRedirectURI && value.source == "default" {
			value.value = defaultXRedirectURI(appURL.value)
		}
		result[key] = statusValue(value)
	}
	return result, nil
}

func (s *Service) StatusValue(ctx context.Context, key string) (Value, error) {
	value, err := s.resolve(ctx, key)
	if err != nil {
		return Value{}, err
	}
	if key == KeyXRedirectURI && value.source == "default" {
		appURL, err := s.resolve(ctx, KeyAppURL)
		if err != nil {
			return Value{}, err
		}
		value.value = defaultXRedirectURI(appURL.value)
	}
	return statusValue(value), nil
}

func statusValue(value resolvedValue) Value {
	item := Value{Source: value.source, KeyID: value.keyID, UpdatedAt: value.updatedAt}
	if IsSecret(value.key) {
		item.Configured = value.value != ""
		item.MaskedValue = Mask(value.value)
	} else if IsBoolean(value.key) {
		item.Configured = value.source != "unset"
		item.Value = parseBool(value.value)
	} else {
		item.Configured = value.value != ""
		item.Value = value.value
	}
	return item
}

func (s *Service) Set(ctx context.Context, key string, value any, updatedBy string, keyID string) error {
	if !Allowed(key) {
		return fmt.Errorf("unknown setting key %s", key)
	}
	raw, err := normalizeValue(key, value)
	if err != nil {
		return err
	}
	if key == KeyXRedirectURI && raw == "" {
		return s.Delete(ctx, key)
	}
	now := nowRFC3339()
	if IsSecret(key) {
		ciphertext, err := secrets.Seal(s.cfg.SecretKey, raw)
		if err != nil {
			return err
		}
		if keyID == "" {
			keyID = "primary"
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key,value_cipher,value_plain,key_id,updated_by,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET value_cipher=excluded.value_cipher,value_plain=NULL,key_id=excluded.key_id,updated_by=excluded.updated_by,updated_at=excluded.updated_at`, key, ciphertext, nil, keyID, updatedBy, now)
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key,value_cipher,value_plain,key_id,updated_by,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET value_cipher=NULL,value_plain=excluded.value_plain,key_id=NULL,updated_by=excluded.updated_by,updated_at=excluded.updated_at`, key, nil, raw, nil, updatedBy, now)
	return err
}

func (s *Service) Delete(ctx context.Context, key string) error {
	if !Allowed(key) {
		return fmt.Errorf("unknown setting key %s", key)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
	return err
}

func (s *Service) resolve(ctx context.Context, key string) (resolvedValue, error) {
	if !Allowed(key) {
		return resolvedValue{}, fmt.Errorf("unknown setting key %s", key)
	}
	var cipher, plain, keyID, updatedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT value_cipher,value_plain,key_id,updated_at FROM settings WHERE key=?`, key).Scan(&cipher, &plain, &keyID, &updatedAt)
	if err == nil {
		if IsSecret(key) {
			if !cipher.Valid || cipher.String == "" {
				return resolvedValue{key: key, source: "database", keyID: keyID.String, updatedAt: updatedAt.String}, nil
			}
			opened, err := secrets.Open(s.cfg.SecretKey, cipher.String)
			if err != nil {
				return resolvedValue{}, err
			}
			return resolvedValue{key: key, value: opened, source: "database", keyID: keyID.String, updatedAt: updatedAt.String}, nil
		}
		value := resolvedValue{key: key, value: normalizeStoredPlain(plain.String), source: "database", keyID: keyID.String, updatedAt: updatedAt.String}
		return validateResolved(value)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return resolvedValue{}, err
	}
	return validateResolved(s.fallback(key))
}

func validateResolved(value resolvedValue) (resolvedValue, error) {
	if value.source == "default" || value.value == "" {
		return value, nil
	}
	if value.key != KeyXRedirectURI && value.key != KeyGeminiModel && value.key != KeyGeminiBaseURL {
		return value, nil
	}
	normalized, err := normalizeValue(value.key, value.value)
	if err != nil {
		return resolvedValue{}, err
	}
	value.value = normalized
	return value, nil
}

func (s *Service) fallback(key string) resolvedValue {
	value := ""
	source := "unset"
	switch key {
	case KeyAppURL:
		value, source = envFallback("APP_URL", s.cfg.AppURL)
	case KeySignupEnabled:
		if _, ok := os.LookupEnv("SIGNUPS_ENABLED"); ok || s.cfg.SignupEnabled {
			value, source = boolString(s.cfg.SignupEnabled), "environment"
		} else {
			value, source = "false", "default"
		}
	case KeyCookieSecure:
		if _, ok := os.LookupEnv("COOKIE_SECURE"); ok || s.cfg.CookieSecure {
			value, source = boolString(s.cfg.CookieSecure), "environment"
		} else {
			value, source = "false", "default"
		}
	case KeyGeminiAPIKey:
		value, source = envFallback("GEMINI_API_KEY", s.cfg.GeminiAPIKey)
	case KeyGeminiModel:
		value, source = envFallback("GEMINI_MODEL", fallbackString(s.cfg.GeminiModel, "gemini-2.5-flash"))
	case KeyGeminiBaseURL:
		value, source = envFallback("GEMINI_BASE_URL", s.cfg.GeminiBaseURL)
	case KeyResendAPIKey:
		value, source = envFallback("RESEND_API_KEY", s.cfg.ResendAPIKey)
	case KeyResendFromEmail:
		value, source = envFallback("RESEND_FROM_EMAIL", s.cfg.ResendFrom)
	case KeyXClientID:
		value, source = envFallback("X_CLIENT_ID", s.cfg.XClientID)
	case KeyXClientSecret:
		value, source = envFallback("X_CLIENT_SECRET", s.cfg.XClientSecret)
	case KeyXRedirectURI:
		value, source = envFallback("X_REDIRECT_URI", s.cfg.XRedirectURI)
		if value == "" {
			value, source = defaultXRedirectURI(s.cfg.AppURL), "default"
		}
	case KeyXIntegrationEnable:
		if _, ok := os.LookupEnv("X_INTEGRATION_ENABLED"); ok || s.cfg.XEnabled {
			value, source = boolString(s.cfg.XEnabled), "environment"
		} else {
			value, source = "false", "default"
		}
	}
	return resolvedValue{key: key, value: value, source: source}
}

func envFallback(envKey string, cfgValue string) (string, string) {
	if _, ok := os.LookupEnv(envKey); ok || cfgValue != "" {
		return cfgValue, "environment"
	}
	return "", "unset"
}

func defaultXRedirectURI(appURL string) string {
	return strings.TrimRight(appURL, "/") + "/settings?section=connections"
}

func normalizeValue(key string, value any) (string, error) {
	raw := settingString(value)
	if key == KeyAppURL {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "", fmt.Errorf("app_url must be an absolute http or https URL")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("app_url must use http or https")
		}
		return strings.TrimRight(raw, "/"), nil
	}
	if key == KeyXRedirectURI || key == KeyGeminiBaseURL {
		if raw == "" {
			return "", nil
		}
		if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return "", fmt.Errorf("%s must not contain whitespace or control characters", key)
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "", fmt.Errorf("%s must be an absolute http or https URL", key)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("%s must use http or https", key)
		}
		if key == KeyGeminiBaseURL && parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
			return "", fmt.Errorf("gemini_base_url must use https unless it points to localhost")
		}
		if key == KeyGeminiBaseURL {
			return strings.TrimRight(raw, "/"), nil
		}
		return raw, nil
	}
	if key == KeyGeminiModel {
		if raw == "" {
			return "gemini-2.5-flash", nil
		}
		raw = strings.TrimPrefix(raw, "models/")
		if strings.ContainsAny(raw, "/ \t\r\n") || strings.ContainsFunc(raw, unicode.IsControl) {
			return "", fmt.Errorf("gemini_model must be a model id, such as gemini-2.5-flash or models/gemini-2.5-flash")
		}
		return raw, nil
	}
	if IsBoolean(key) {
		return boolString(parseBool(raw)), nil
	}
	return raw, nil
}

func fallbackString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func settingString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case bool:
		return boolString(typed)
	case float64:
		return fmt.Sprintf("%g", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	default:
		raw, err := json.Marshal(value)
		if err != nil || string(raw) == "null" {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}
}

func normalizeStoredPlain(value string) string {
	value = strings.TrimSpace(value)
	var decoded string
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		return strings.TrimSpace(decoded)
	}
	return value
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func Mask(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

func nowRFC3339() string {
	if value := strings.TrimSpace(os.Getenv("ARIVU_TEST_NOW")); value != "" {
		return strings.ReplaceAll(value, "\n", "")
	}
	return time.Now().UTC().Format(time.RFC3339)
}
