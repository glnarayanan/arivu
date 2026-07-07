package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/runtimeconfig"
)

func (a *App) adminOverview(w http.ResponseWriter, r *http.Request, user auth.User) {
	counts := a.bookmarks.Counts(r.Context())
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour).Format(time.RFC3339)
	week := now.AddDate(0, 0, -int((now.Weekday()+6)%7)).Truncate(24 * time.Hour).Format(time.RFC3339)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	avg := 0.0
	if counts.Users > 0 {
		avg = float64(counts.Bookmarks) / float64(counts.Users)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users": map[string]any{
			"total":      counts.Users,
			"today":      countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM users WHERE created_at>=?`, today),
			"this_week":  countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM users WHERE created_at>=?`, week),
			"this_month": countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM users WHERE created_at>=?`, month),
		},
		"bookmarks": map[string]any{
			"total":        counts.Bookmarks,
			"today":        countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM bookmarks WHERE created_at>=?`, today),
			"this_week":    countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM bookmarks WHERE created_at>=?`, week),
			"this_month":   countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM bookmarks WHERE created_at>=?`, month),
			"avg_per_user": avg,
		},
		"collections":  map[string]any{"total": counts.Collections},
		"ai_summaries": map[string]any{"total": counts.Summaries},
		"sqlite":       sqliteStats(a.cfg.DBPath),
		"server": map[string]any{
			"runtime":        runtime.Version(),
			"started_at":     a.startedAt.Format(time.RFC3339),
			"uptime_seconds": int(time.Since(a.startedAt).Seconds()),
			"now":            now.Format(time.RFC3339),
		},
	})
}

func (a *App) adminSystem(w http.ResponseWriter, r *http.Request, user auth.User) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	writeJSON(w, http.StatusOK, map[string]any{
		"sqlite": sqliteStats(a.cfg.DBPath),
		"db":     a.db.Stats(),
		"tables": tableStats(r.Context(), a.db),
		"system": map[string]any{
			"go":               runtime.Version(),
			"goroutines":       runtime.NumGoroutine(),
			"alloc_bytes":      mem.Alloc,
			"heap_alloc_bytes": mem.HeapAlloc,
			"sys_bytes":        mem.Sys,
			"num_gc":           mem.NumGC,
			"started_at":       a.startedAt.Format(time.RFC3339),
			"uptime_seconds":   int(time.Since(a.startedAt).Seconds()),
		},
	})
}

func (a *App) adminUsers(w http.ResponseWriter, r *http.Request, user auth.User) {
	orderBy := adminUserOrder(r.URL.Query().Get("sort"), r.URL.Query().Get("order"))
	rows, err := a.db.QueryContext(r.Context(), `SELECT u.id,u.email,u.name,u.created_at,u.banned,u.invite_pending,COUNT(DISTINCT b.id),COUNT(DISTINCT c.id),MAX(b.created_at) FROM users u LEFT JOIN bookmarks b ON b.user_id=u.id LEFT JOIN collections c ON c.user_id=u.id GROUP BY u.id `+orderBy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load users")
		return
	}
	defer rows.Close()
	var users []map[string]any
	for rows.Next() {
		var id, email, name, created string
		var banned, invitePending bool
		var bookmarkCount, collectionCount int
		var lastBookmark sql.NullString
		_ = rows.Scan(&id, &email, &name, &created, &banned, &invitePending, &bookmarkCount, &collectionCount, &lastBookmark)
		users = append(users, map[string]any{"id": id, "email": email, "name": name, "created_at": created, "banned": banned, "invite_pending": invitePending, "bookmark_count": bookmarkCount, "collection_count": collectionCount, "last_bookmark_at": nullStringMap(lastBookmark), "is_admin": a.cfg.AdminEmails[strings.ToLower(email)]})
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
	rowUser["recent_bookmarks"] = recentBookmarksForUser(r.Context(), a.db, id)
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
	if err := a.auth.ResetUserPassword(r.Context(), r.PathValue("id"), body.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not reset password")
		return
	}
	a.auditEvent(r.Context(), user.ID, "admin.user.reset_password", "user", r.PathValue("id"), nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "password_reset", "user_id": r.PathValue("id")})
}

func (a *App) adminDeleteUser(w http.ResponseWriter, r *http.Request, user auth.User) {
	targetID := r.PathValue("id")
	if targetID == user.ID {
		writeError(w, http.StatusBadRequest, "Cannot delete yourself")
		return
	}
	deletedBookmarks := countForUser(r.Context(), a.db, "bookmarks", targetID)
	res, _ := a.db.ExecContext(r.Context(), `DELETE FROM users WHERE id=?`, targetID)
	deleted, _ := res.RowsAffected()
	if deleted == 0 {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	a.auditEvent(r.Context(), user.ID, "admin.user.delete", "user", targetID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "user_id": targetID, "deleted_bookmarks": deletedBookmarks})
}

func (a *App) adminAPIKeys(w http.ResponseWriter, r *http.Request, user auth.User) {
	a.adminSettings(w, r, user)
}

func (a *App) adminSettings(w http.ResponseWriter, r *http.Request, user auth.User) {
	status, err := a.runtime.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load settings")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *App) adminUpdateAPIKeys(w http.ResponseWriter, r *http.Request, user auth.User) {
	a.adminUpdateSettings(w, r, user)
}

func (a *App) adminUpdateSettings(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body map[string]any
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	changed := []string{}
	for key, value := range body {
		if !runtimeconfig.Allowed(key) {
			writeError(w, http.StatusBadRequest, "Unknown setting")
			return
		}
		if runtimeconfig.IsSecret(key) && strings.TrimSpace(requestSettingString(value)) == "" {
			continue
		}
		if err := a.runtime.Set(r.Context(), key, value, user.Email, "primary"); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		changed = append(changed, key)
	}
	sort.Strings(changed)
	if len(changed) == 0 {
		writeError(w, http.StatusBadRequest, "No fields to update")
		return
	}
	if len(changed) > 0 {
		a.auditEvent(r.Context(), user.ID, "admin.settings.update", "settings", "", map[string]any{"keys": changed})
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "fields": changed})
}

func (a *App) adminDeleteAPIKey(w http.ResponseWriter, r *http.Request, user auth.User) {
	a.adminDeleteSetting(w, r, user)
}

func (a *App) adminDeleteSetting(w http.ResponseWriter, r *http.Request, user auth.User) {
	key := r.PathValue("key")
	if !runtimeconfig.Allowed(key) {
		writeError(w, http.StatusBadRequest, "Unknown setting")
		return
	}
	if err := a.runtime.Delete(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not remove setting")
		return
	}
	a.auditEvent(r.Context(), user.ID, "admin.settings.delete", "settings", "", map[string]any{"keys": []string{key}})
	status, _ := a.runtime.Status(r.Context())
	source := ""
	if status[key].Source == "environment" {
		source = "environment"
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "removed", "key": key, "has_env_fallback": source == "environment"})
}

func (a *App) adminAPIUsage(w http.ResponseWriter, r *http.Request, user auth.User) {
	status, _ := a.runtime.Status(r.Context())
	usage := a.usage.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"requests_today":          usage["requests_total"],
		"tokens_today":            0,
		"current_rpm":             0,
		"current_tpm":             0,
		"rpm_utilization_pct":     0,
		"tpm_utilization_pct":     0,
		"daily_utilization_pct":   0,
		"limits":                  map[string]any{"max_rpm": 0, "max_tpm": 0, "max_daily": 0},
		"current_date":            time.Now().UTC().Format("2006-01-02"),
		"provider_usage":          usage,
		"gemini_configured":       status[runtimeconfig.KeyGeminiAPIKey].Configured,
		"summaries_completed":     countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM ai_summaries WHERE processing_status='completed'`),
		"summaries_pending":       countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM ai_summaries WHERE processing_status='pending'`),
		"summaries_failed":        countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM ai_summaries WHERE processing_status='failed'`),
		"background_jobs_failed":  countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM jobs WHERE status='failed'`),
		"background_jobs_queued":  countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM jobs WHERE status='queued'`),
		"background_jobs_running": countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM jobs WHERE status='leased'`),
	})
}

func (a *App) adminActivity(w http.ResponseWriter, r *http.Request, user auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"recent_bookmarks":     recentAdminBookmarks(r.Context(), a.db, 50),
		"recent_registrations": recentAdminUsers(r.Context(), a.db, 10),
	})
}

func (a *App) adminCollectionsStats(w http.ResponseWriter, r *http.Request, user auth.User) {
	writeJSON(w, http.StatusOK, collectionStats(r.Context(), a.db))
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
	case []string:
		items := make([]any, 0, min(len(typed), 12))
		for _, item := range typed[:min(len(typed), 12)] {
			items = append(items, truncateRunes(item, 160))
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

func (a *App) auditEvent(ctx context.Context, actorID, action, targetType, targetID string, metadata map[string]any) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata = sanitizeAuditMetadata(metadata)
	raw, _ := json.Marshal(metadata)
	_, _ = a.db.ExecContext(ctx, `INSERT INTO audit_events(id,actor_user_id,action,target_type,target_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, ids.New(), actorID, action, targetType, targetID, string(raw), time.Now().UTC().Format(time.RFC3339))
}

func countForUser(ctx context.Context, db *sql.DB, table string, userID string) int {
	var count int
	switch table {
	case "bookmarks", "collections":
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE user_id=?`, userID).Scan(&count)
	}
	return count
}

func countWhere(ctx context.Context, db *sql.DB, query string, args ...any) int {
	var count int
	_ = db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count
}

func sqliteStats(path string) map[string]any {
	stats := map[string]any{"path": path}
	if info, err := os.Stat(path); err == nil {
		stats["size_bytes"] = info.Size()
	}
	if info, err := os.Stat(path + "-wal"); err == nil {
		stats["wal_size_bytes"] = info.Size()
	}
	if info, err := os.Stat(path + "-shm"); err == nil {
		stats["shm_size_bytes"] = info.Size()
	}
	return stats
}

func tableStats(ctx context.Context, db *sql.DB) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, table := range []string{"users", "bookmarks", "ai_summaries", "collections", "sessions", "x_connections", "settings", "audit_events", "jobs"} {
		result[table] = map[string]any{"count": countWhere(ctx, db, `SELECT COUNT(*) FROM `+table)}
	}
	return result
}

func adminUserOrder(sortBy string, order string) string {
	column := map[string]string{
		"bookmarks":        "bookmark_count",
		"bookmark_count":   "bookmark_count",
		"collections":      "collection_count",
		"collection_count": "collection_count",
		"created_at":       "u.created_at",
		"name":             "u.name",
		"email":            "u.email",
		"last_bookmark_at": "last_bookmark_at",
	}[sortBy]
	if column == "" {
		column = "u.created_at"
	}
	direction := "DESC"
	if strings.EqualFold(order, "asc") {
		direction = "ASC"
	}
	return `ORDER BY ` + column + ` ` + direction
}

func recentBookmarksForUser(ctx context.Context, db *sql.DB, userID string) []map[string]any {
	rows, err := db.QueryContext(ctx, `SELECT id,COALESCE(title,''),url,COALESCE(domain,''),created_at FROM bookmarks WHERE user_id=? ORDER BY created_at DESC LIMIT 10`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title, url, domain, created string
		if rows.Scan(&id, &title, &url, &domain, &created) == nil {
			items = append(items, map[string]any{"id": id, "title": title, "url": url, "domain": domain, "created_at": created})
		}
	}
	return items
}

func recentAdminBookmarks(ctx context.Context, db *sql.DB, limit int) []map[string]any {
	rows, err := db.QueryContext(ctx, `SELECT b.id,b.user_id,COALESCE(u.email,''),COALESCE(b.title,''),b.url,COALESCE(b.domain,''),b.created_at FROM bookmarks b LEFT JOIN users u ON u.id=b.user_id ORDER BY b.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, userID, email, title, url, domain, created string
		if rows.Scan(&id, &userID, &email, &title, &url, &domain, &created) == nil {
			items = append(items, map[string]any{"id": id, "user_id": userID, "user_email": email, "title": title, "url": url, "domain": domain, "created_at": created})
		}
	}
	return items
}

func recentAdminUsers(ctx context.Context, db *sql.DB, limit int) []map[string]any {
	rows, err := db.QueryContext(ctx, `SELECT id,email,name,created_at FROM users ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, email, name, created string
		if rows.Scan(&id, &email, &name, &created) == nil {
			items = append(items, map[string]any{"id": id, "email": email, "name": name, "created_at": created})
		}
	}
	return items
}

func collectionStats(ctx context.Context, db *sql.DB) []map[string]any {
	rows, err := db.QueryContext(ctx, `SELECT c.id,c.name,c.user_id,COALESCE(u.email,''),COUNT(cb.bookmark_id),MAX(cb.added_at),c.created_at FROM collections c LEFT JOIN users u ON u.id=c.user_id LEFT JOIN collection_bookmarks cb ON cb.collection_id=c.id GROUP BY c.id ORDER BY COUNT(cb.bookmark_id) DESC,c.created_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, userID, email, created string
		var bookmarkCount int
		var latest sql.NullString
		if rows.Scan(&id, &name, &userID, &email, &bookmarkCount, &latest, &created) == nil {
			items = append(items, map[string]any{"id": id, "name": name, "user_id": userID, "user_email": email, "bookmark_count": bookmarkCount, "latest_added_at": nullStringMap(latest), "created_at": created})
		}
	}
	return items
}

func requestSettingString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		raw, _ := json.Marshal(typed)
		return string(raw)
	}
}
