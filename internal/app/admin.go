package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
	"golang.org/x/crypto/bcrypt"
)

func (a *App) adminOverview(w http.ResponseWriter, r *http.Request, user auth.User) {
	counts := a.bookmarks.Counts(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"users":        map[string]any{"total": counts.Users},
		"bookmarks":    map[string]any{"total": counts.Bookmarks},
		"collections":  map[string]any{"total": counts.Collections},
		"ai_summaries": map[string]any{"total": counts.Summaries},
		"sqlite":       map[string]any{"path": a.cfg.DBPath},
		"mongodb":      map[string]any{"compatibility_note": "Historical import source only; active data is stored locally"},
		"server":       map[string]any{"runtime": runtime.Version(), "now": time.Now().UTC().Format(time.RFC3339)},
	})
}

func (a *App) adminSystem(w http.ResponseWriter, r *http.Request, user auth.User) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	writeJSON(w, http.StatusOK, map[string]any{
		"sqlite": map[string]any{"path": a.cfg.DBPath},
		"redis":  map[string]any{"compatibility_note": "Background work and rate limits are handled by the local database"},
		"system": map[string]any{
			"go":          runtime.Version(),
			"goroutines":  runtime.NumGoroutine(),
			"alloc_bytes": mem.Alloc,
		},
	})
}

func (a *App) adminUsers(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT u.id,u.email,u.name,u.created_at,u.banned,u.invite_pending,COUNT(b.id) FROM users u LEFT JOIN bookmarks b ON b.user_id=u.id GROUP BY u.id ORDER BY u.created_at DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load users")
		return
	}
	defer rows.Close()
	var users []map[string]any
	for rows.Next() {
		var id, email, name, created string
		var banned, invitePending bool
		var bookmarkCount int
		_ = rows.Scan(&id, &email, &name, &created, &banned, &invitePending, &bookmarkCount)
		users = append(users, map[string]any{"id": id, "email": email, "name": name, "created_at": created, "banned": banned, "invite_pending": invitePending, "bookmark_count": bookmarkCount, "is_admin": a.cfg.AdminEmails[strings.ToLower(email)]})
	}
	writeJSON(w, http.StatusOK, users)
}

func (a *App) adminGetUser(w http.ResponseWriter, r *http.Request, user auth.User) {
	id := r.PathValue("id")
	var rowUser = map[string]any{}
	var email, name, created string
	var banned, invitePending bool
	err := a.db.QueryRowContext(r.Context(), `SELECT email,name,created_at,banned,invite_pending FROM users WHERE id=?`, id).Scan(&email, &name, &created, &banned, &invitePending)
	if err != nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	rowUser["id"] = id
	rowUser["email"] = email
	rowUser["name"] = name
	rowUser["created_at"] = created
	rowUser["banned"] = banned
	rowUser["invite_pending"] = invitePending
	rowUser["is_admin"] = a.cfg.AdminEmails[strings.ToLower(email)]
	rowUser["bookmark_count"] = countForUser(r.Context(), a.db, "bookmarks", id)
	rowUser["collection_count"] = countForUser(r.Context(), a.db, "collections", id)
	writeJSON(w, http.StatusOK, rowUser)
}

func (a *App) adminInviteUser(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil || strings.TrimSpace(body.Email) == "" {
		writeError(w, http.StatusBadRequest, "Email is required")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO users(id,email,name,invite_pending,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, strings.ToLower(strings.TrimSpace(body.Email)), strings.TrimSpace(body.Name), true, now, now)
	if err != nil {
		writeError(w, http.StatusConflict, "Email already registered")
		return
	}
	a.auditEvent(r.Context(), user.ID, "admin.user.invite", "user", id, map[string]any{"email": strings.ToLower(strings.TrimSpace(body.Email))})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "email": body.Email, "name": body.Name, "created_at": now, "invite_sent": false})
}

func (a *App) adminBanUser(w http.ResponseWriter, r *http.Request, user auth.User) {
	targetID := r.PathValue("id")
	if targetID == user.ID {
		writeError(w, http.StatusBadRequest, "Cannot ban yourself")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.db.ExecContext(r.Context(), `UPDATE users SET banned=1, updated_at=? WHERE id=?`, now, targetID)
	_, _ = a.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=? WHERE user_id=?`, now, targetID)
	a.auditEvent(r.Context(), user.ID, "admin.user.ban", "user", targetID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "banned", "user_id": targetID})
}

func (a *App) adminUnbanUser(w http.ResponseWriter, r *http.Request, user auth.User) {
	targetID := r.PathValue("id")
	_, _ = a.db.ExecContext(r.Context(), `UPDATE users SET banned=0, updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), targetID)
	a.auditEvent(r.Context(), user.ID, "admin.user.unban", "user", targetID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "unbanned", "user_id": targetID})
}

func (a *App) adminResetPassword(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(r, &body); err != nil || len(body.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new_password must be at least 8 characters")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not reset password")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.db.ExecContext(r.Context(), `UPDATE users SET password_hash=?, password_scheme='bcrypt', invite_pending=0, updated_at=? WHERE id=?`, string(hash), now, r.PathValue("id"))
	_, _ = a.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=? WHERE user_id=?`, now, r.PathValue("id"))
	a.auditEvent(r.Context(), user.ID, "admin.user.reset_password", "user", r.PathValue("id"), nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "password_reset", "user_id": r.PathValue("id")})
}

func (a *App) adminDeleteUser(w http.ResponseWriter, r *http.Request, user auth.User) {
	targetID := r.PathValue("id")
	if targetID == user.ID {
		writeError(w, http.StatusBadRequest, "Cannot delete yourself")
		return
	}
	res, _ := a.db.ExecContext(r.Context(), `DELETE FROM users WHERE id=?`, targetID)
	deleted, _ := res.RowsAffected()
	if deleted == 0 {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	a.auditEvent(r.Context(), user.ID, "admin.user.delete", "user", targetID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "user_id": targetID})
}

func (a *App) adminAPIKeys(w http.ResponseWriter, r *http.Request, user auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"gemini_api_key":        keyStatus(a.cfg.GeminiAPIKey),
		"resend_api_key":        keyStatus(a.cfg.ResendAPIKey),
		"x_client_id":           keyStatus(a.cfg.XClientID),
		"x_client_secret":       keyStatus(a.cfg.XClientSecret),
		"x_redirect_uri":        map[string]any{"value": a.cfg.XRedirectURI, "source": source(a.cfg.XRedirectURI)},
		"x_integration_enabled": map[string]any{"value": a.cfg.XEnabled, "source": "environment"},
	})
}

func (a *App) adminUpdateAPIKeys(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body map[string]any
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	changed := []string{}
	for key, value := range body {
		if !allowedSettingKey(key) {
			continue
		}
		raw, _ := json.Marshal(value)
		_, _ = a.db.ExecContext(r.Context(), `INSERT INTO settings(key,value_plain,updated_by,updated_at) VALUES(?,?,?,?) ON CONFLICT(key) DO UPDATE SET value_plain=excluded.value_plain,updated_by=excluded.updated_by,updated_at=excluded.updated_at`, key, string(raw), user.Email, now)
		changed = append(changed, key)
	}
	sort.Strings(changed)
	if len(changed) > 0 {
		a.auditEvent(r.Context(), user.ID, "admin.settings.update", "settings", "", map[string]any{"keys": changed})
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
}

func (a *App) adminAuditEvents(w http.ResponseWriter, r *http.Request, user auth.User) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT e.id,e.actor_user_id,COALESCE(u.email,''),e.action,e.target_type,e.target_id,e.metadata_json,e.created_at FROM audit_events e LEFT JOIN users u ON u.id=e.actor_user_id ORDER BY e.created_at DESC, e.id DESC LIMIT ?`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load audit events")
		return
	}
	defer rows.Close()
	events := []map[string]any{}
	for rows.Next() {
		var id, action, targetType, targetID, metadataRaw, created string
		var actorID, actorEmail sql.NullString
		if err := rows.Scan(&id, &actorID, &actorEmail, &action, &targetType, &targetID, &metadataRaw, &created); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load audit events")
			return
		}
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(metadataRaw), &metadata); err != nil {
			metadata = map[string]any{}
		}
		metadata = sanitizeAuditMetadata(metadata)
		events = append(events, map[string]any{
			"id":          id,
			"actor_id":    actorID.String,
			"actor_email": actorEmail.String,
			"action":      action,
			"target_type": targetType,
			"target_id":   targetID,
			"metadata":    metadata,
			"created_at":  created,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load audit events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func sanitizeAuditMetadata(metadata map[string]any) map[string]any {
	sanitized := map[string]any{}
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" || len([]rune(key)) > 64 {
			continue
		}
		if sensitiveAuditMetadataKey(key) {
			sanitized[key] = "[redacted]"
			continue
		}
		sanitized[key] = sanitizeAuditMetadataValue(value)
	}
	raw, _ := json.Marshal(sanitized)
	if len(raw) > 1200 {
		return map[string]any{"truncated": true}
	}
	return sanitized
}

func sensitiveAuditMetadataKey(key string) bool {
	lower := strings.ToLower(key)
	if lower == "keys" {
		return false
	}
	return strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "api_key") ||
		strings.HasSuffix(lower, "_key")
}

func sanitizeAuditMetadataValue(value any) any {
	switch typed := value.(type) {
	case string:
		return truncateRunes(typed, 160)
	case float64, bool, nil:
		return typed
	case []any:
		items := make([]any, 0, min(len(typed), 12))
		for _, item := range typed[:min(len(typed), 12)] {
			items = append(items, sanitizeAuditMetadataValue(item))
		}
		return items
	default:
		return "[object]"
	}
}

func truncateRunes(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func allowedSettingKey(key string) bool {
	switch key {
	case "gemini_api_key", "resend_api_key", "x_client_id", "x_client_secret", "x_redirect_uri", "x_integration_enabled":
		return true
	default:
		return false
	}
}

func (a *App) auditEvent(ctx context.Context, actorID, action, targetType, targetID string, metadata map[string]any) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	raw, _ := json.Marshal(metadata)
	_, _ = a.db.ExecContext(ctx, `INSERT INTO audit_events(id,actor_user_id,action,target_type,target_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, ids.New(), actorID, action, targetType, targetID, string(raw), time.Now().UTC().Format(time.RFC3339))
}

func keyStatus(value string) map[string]any {
	return map[string]any{"configured": value != "", "masked_value": mask(value), "source": source(value)}
}

func mask(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

func source(value string) string {
	if value == "" {
		return "none"
	}
	return "environment"
}

func countForUser(ctx context.Context, db *sql.DB, table string, userID string) int {
	var count int
	switch table {
	case "bookmarks", "collections":
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE user_id=?`, userID).Scan(&count)
	}
	return count
}
