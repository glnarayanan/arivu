package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/providers"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	db  *sql.DB
	cfg config.Config
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type Session struct {
	User     User
	Audience string
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func New(db *sql.DB, cfg config.Config) *Service {
	return &Service{db: db, cfg: cfg}
}

func (s *Service) Signup(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SignupEnabled {
		writeJSON(w, http.StatusForbidden, map[string]any{"detail": "Signups are disabled"})
		return
	}
	var body credentials
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid request"})
		return
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	if body.Email == "" || len(body.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Email and password are required"})
		return
	}
	user := User{ID: newID(), Email: body.Email, Name: strings.TrimSpace(body.Name)}
	hash, err := hashArgon2id(body.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Could not create user"})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO users(id,email,name,password_hash,password_scheme,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, user.ID, user.Email, user.Name, hash, "argon2id", now, now); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"detail": "Email already registered"})
		return
	}
	s.issueWebSession(w, r, user)
}

func (s *Service) Login(w http.ResponseWriter, r *http.Request) {
	s.loginWithAudience(w, r, "web")
}

func (s *Service) CLILogin(w http.ResponseWriter, r *http.Request) {
	s.loginWithAudience(w, r, "cli")
}

func (s *Service) loginWithAudience(w http.ResponseWriter, r *http.Request, audience string) {
	var body credentials
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid request"})
		return
	}
	user, scheme, hash, err := s.userByEmail(r.Context(), body.Email)
	if err != nil || !verifyPassword(body.Password, scheme, hash) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Invalid credentials"})
		return
	}
	if scheme != "argon2id" {
		if newHash, err := hashArgon2id(body.Password); err == nil {
			_, _ = s.db.ExecContext(r.Context(), `UPDATE users SET password_hash=?, password_scheme='argon2id', updated_at=? WHERE id=?`, newHash, time.Now().UTC().Format(time.RFC3339), user.ID)
		}
	}
	if audience == "cli" {
		s.issueBodySession(w, r, user, "cli", s.cfg.RefreshTTL)
		return
	}
	s.issueWebSession(w, r, user)
}

func (s *Service) Me(w http.ResponseWriter, r *http.Request, user User) {
	writeJSON(w, http.StatusOK, user)
}

func (s *Service) Logout(w http.ResponseWriter, r *http.Request, user User) {
	if token := bearerToken(r); token != "" {
		_, _ = s.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=? WHERE access_hash=?`, time.Now().UTC().Format(time.RFC3339), tokenHash(token))
	}
	if cookie, err := r.Cookie("access_token"); err == nil {
		_, _ = s.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=? WHERE access_hash=?`, time.Now().UTC().Format(time.RFC3339), tokenHash(cookie.Value))
	}
	clearCookie(w, "access_token", s.cfg.CookieSecure)
	clearCookie(w, "refresh_token", s.cfg.CookieSecure)
	clearCookie(w, "csrf_token", s.cfg.CookieSecure)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Logged out successfully"})
}

func (s *Service) RefreshWeb(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Refresh token required"})
		return
	}
	user, err := s.rotateRefresh(r.Context(), cookie.Value, "web")
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Invalid refresh token"})
		return
	}
	s.issueWebSession(w, r, user)
}

func (s *Service) RefreshCLI(w http.ResponseWriter, r *http.Request) {
	var body refreshRequest
	if err := decodeJSON(r, &body); err != nil || body.RefreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Refresh token required"})
		return
	}
	user, err := s.rotateRefresh(r.Context(), body.RefreshToken, "cli")
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Invalid refresh token"})
		return
	}
	s.issueBodySession(w, r, user, "cli", s.cfg.RefreshTTL)
}

func (s *Service) ExtensionToken(w http.ResponseWriter, r *http.Request, user User) {
	s.issueBodySession(w, r, user, "extension", s.cfg.ExtensionTTL)
}

func (s *Service) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var body forgotPasswordRequest
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid request"})
		return
	}
	user, _, _, err := s.userByEmail(r.Context(), body.Email)
	if err == nil {
		token := randomToken()
		now := time.Now().UTC()
		_, _ = s.db.ExecContext(r.Context(), `INSERT INTO password_reset_tokens(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)`, tokenHash(token), user.ID, now.Add(time.Hour).Format(time.RFC3339), now.Format(time.RFC3339))
		_ = s.sendResetEmail(r.Context(), user.Email, token)
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "If that account exists, reset instructions will be sent."})
}

func (s *Service) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var body resetPasswordRequest
	if err := decodeJSON(r, &body); err != nil || len(body.NewPassword) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid reset request"})
		return
	}
	var userID, expires string
	err := s.db.QueryRowContext(r.Context(), `SELECT user_id,expires_at FROM password_reset_tokens WHERE token_hash=? AND used_at IS NULL`, tokenHash(body.Token)).Scan(&userID, &expires)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid or expired reset token"})
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, expires)
	if err != nil || time.Now().UTC().After(expiresAt) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid or expired reset token"})
		return
	}
	hash, err := hashArgon2id(body.NewPassword)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Could not reset password"})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.ExecContext(r.Context(), `UPDATE users SET password_hash=?, password_scheme='argon2id', updated_at=? WHERE id=?`, hash, now, userID)
	_, _ = s.db.ExecContext(r.Context(), `UPDATE password_reset_tokens SET used_at=? WHERE token_hash=?`, now, tokenHash(body.Token))
	_, _ = s.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=? WHERE user_id=?`, now, userID)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Password reset successfully"})
}

func (s *Service) ChangePassword(w http.ResponseWriter, r *http.Request, user User) {
	var body changePasswordRequest
	if err := decodeJSON(r, &body); err != nil || len(body.NewPassword) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid password change request"})
		return
	}
	_, scheme, hash, err := s.userByEmail(r.Context(), user.Email)
	if err != nil || !verifyPassword(body.CurrentPassword, scheme, hash) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Current password is incorrect"})
		return
	}
	newHash, err := hashArgon2id(body.NewPassword)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Could not change password"})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.ExecContext(r.Context(), `UPDATE users SET password_hash=?, password_scheme='argon2id', updated_at=? WHERE id=?`, newHash, now, user.ID)
	_, _ = s.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=? WHERE user_id=? AND audience<>'web'`, now, user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Password changed successfully"})
}

func (s *Service) Profile(w http.ResponseWriter, r *http.Request, user User) {
	writeJSON(w, http.StatusOK, user)
}

func (s *Service) UpdateProfile(w http.ResponseWriter, r *http.Request, user User) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid request"})
		return
	}
	name := strings.TrimSpace(body.Name)
	_, _ = s.db.ExecContext(r.Context(), `UPDATE users SET name=?, updated_at=? WHERE id=?`, name, time.Now().UTC().Format(time.RFC3339), user.ID)
	user.Name = name
	writeJSON(w, http.StatusOK, user)
}

func (s *Service) Authenticate(r *http.Request) (User, error) {
	session, err := s.AuthenticateSession(r)
	if err != nil {
		return User{}, err
	}
	return session.User, nil
}

func (s *Service) AuthenticateSession(r *http.Request) (Session, error) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie("access_token"); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		return Session{}, errors.New("missing token")
	}
	hash := tokenHash(token)
	var user User
	var audience string
	var expires string
	err := s.db.QueryRowContext(r.Context(), `SELECT u.id,u.email,u.name,s.audience,s.access_expires_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.access_hash=? AND s.revoked_at IS NULL AND u.banned=0`, hash).Scan(&user.ID, &user.Email, &user.Name, &audience, &expires)
	if err != nil {
		return Session{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339, expires)
	if err != nil || time.Now().UTC().After(expiresAt) {
		return Session{}, errors.New("expired token")
	}
	if audience == "web" && mutates(r.Method) {
		cookie, err := r.Cookie("csrf_token")
		if err != nil || cookie.Value == "" || r.Header.Get("X-CSRF-Token") != cookie.Value {
			return Session{}, errors.New("csrf token mismatch")
		}
	}
	return Session{User: user, Audience: audience}, nil
}

func (s *Service) issueWebSession(w http.ResponseWriter, r *http.Request, user User) {
	tokens, err := s.createSession(r.Context(), user, "web", s.cfg.RefreshTTL, r.UserAgent(), r.Host)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Could not create session"})
		return
	}
	setCookie(w, "access_token", tokens.AccessToken, s.cfg.SessionTTL, true, s.cfg.CookieSecure)
	setCookie(w, "refresh_token", tokens.RefreshToken, s.cfg.RefreshTTL, true, s.cfg.CookieSecure)
	setCookie(w, "csrf_token", tokens.CSRFToken, s.cfg.RefreshTTL, false, s.cfg.CookieSecure)
	writeJSON(w, http.StatusOK, map[string]any{"access_token": tokens.AccessToken, "refresh_token": tokens.RefreshToken, "token_type": "bearer", "csrf_token": tokens.CSRFToken})
}

func (s *Service) issueBodySession(w http.ResponseWriter, r *http.Request, user User, audience string, ttl time.Duration) {
	tokens, err := s.createSession(r.Context(), user, audience, ttl, r.UserAgent(), r.Host)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Could not create session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":                  tokens.AccessToken,
		"refresh_token":                 tokens.RefreshToken,
		"token_type":                    "bearer",
		"access_token_expires_at":       time.Now().UTC().Add(s.cfg.SessionTTL).Format(time.RFC3339),
		"refresh_token_expires_at":      time.Now().UTC().Add(ttl).Format(time.RFC3339),
		"user":                          user,
		"reauth_required_after_migrate": false,
	})
}

type tokenSet struct {
	AccessToken  string
	RefreshToken string
	CSRFToken    string
}

func (s *Service) createSession(ctx context.Context, user User, audience string, refreshTTL time.Duration, ua, origin string) (tokenSet, error) {
	access := randomToken()
	refresh := randomToken()
	csrf := randomToken()
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id,user_id,audience,access_hash,refresh_hash,csrf_hash,user_agent,origin,access_expires_at,refresh_expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		newID(), user.ID, audience, tokenHash(access), tokenHash(refresh), tokenHash(csrf), ua, origin, now.Add(s.cfg.SessionTTL).Format(time.RFC3339), now.Add(refreshTTL).Format(time.RFC3339), now.Format(time.RFC3339))
	return tokenSet{AccessToken: access, RefreshToken: refresh, CSRFToken: csrf}, err
}

func (s *Service) rotateRefresh(ctx context.Context, refresh string, audience string) (User, error) {
	hash := tokenHash(refresh)
	var user User
	var expires string
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.email,u.name,s.refresh_expires_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.refresh_hash=? AND s.audience=? AND s.revoked_at IS NULL AND u.banned=0`, hash, audience).Scan(&user.ID, &user.Email, &user.Name, &expires)
	if err != nil {
		return User{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339, expires)
	if err != nil || time.Now().UTC().After(expiresAt) {
		return User{}, errors.New("expired refresh")
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE refresh_hash=?`, time.Now().UTC().Format(time.RFC3339), hash)
	return user, nil
}

func (s *Service) userByEmail(ctx context.Context, email string) (User, string, string, error) {
	var user User
	var scheme, hash string
	err := s.db.QueryRowContext(ctx, `SELECT id,email,name,password_scheme,password_hash FROM users WHERE lower(email)=lower(?) AND banned=0`, strings.TrimSpace(email)).Scan(&user.ID, &user.Email, &user.Name, &scheme, &hash)
	return user, scheme, hash, err
}

func (s *Service) sendResetEmail(ctx context.Context, email string, token string) error {
	resetURL, err := url.JoinPath(strings.TrimRight(s.cfg.AppURL, "/"), "reset-password")
	if err != nil {
		return err
	}
	values := url.Values{"token": []string{token}}
	link := resetURL + "?" + values.Encode()
	body := `<p>Use this link to reset your Arivu password:</p><p><a href="` + html.EscapeString(link) + `">Reset password</a></p><p>This link expires in one hour.</p>`
	return providers.ResendClient{APIKey: s.cfg.ResendAPIKey, From: s.cfg.ResendFrom}.Send(ctx, email, "Reset your Arivu password", body)
}

func hashArgon2id(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	payload := map[string]string{"salt": base64.RawStdEncoding.EncodeToString(salt), "key": base64.RawStdEncoding.EncodeToString(key)}
	raw, _ := json.Marshal(payload)
	return string(raw), nil
}

func verifyPassword(password, scheme, stored string) bool {
	switch scheme {
	case "argon2id":
		var payload map[string]string
		if err := json.Unmarshal([]byte(stored), &payload); err != nil {
			return false
		}
		salt, err1 := base64.RawStdEncoding.DecodeString(payload["salt"])
		expected, err2 := base64.RawStdEncoding.DecodeString(payload["key"])
		if err1 != nil || err2 != nil {
			return false
		}
		actual := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
		return subtleEqual(actual, expected)
	default:
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil
	}
}

func subtleEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := range a {
		result |= a[i] ^ b[i]
	}
	return result == 0
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return hex.EncodeToString(buf[0:4]) + "-" + hex.EncodeToString(buf[4:6]) + "-" + hex.EncodeToString(buf[6:8]) + "-" + hex.EncodeToString(buf[8:10]) + "-" + hex.EncodeToString(buf[10:])
}

func mutates(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func setCookie(w http.ResponseWriter, name, value string, ttl time.Duration, httpOnly, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(ttl.Seconds()), HttpOnly: httpOnly, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
