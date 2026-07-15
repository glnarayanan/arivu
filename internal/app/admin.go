package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/runtimeconfig"
	"github.com/glnarayanan/arivu/internal/safefetch"
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
	rows, err := a.db.QueryContext(r.Context(), `SELECT u.id,u.email,u.name,u.created_at,u.banned,u.invite_pending,COUNT(DISTINCT b.id),COUNT(DISTINCT c.id),MAX(b.created_at),COALESCE(failed.failed_job_count,0)
		FROM users u
		LEFT JOIN bookmarks b ON b.user_id=u.id
		LEFT JOIN collections c ON c.user_id=u.id
		LEFT JOIN (SELECT user_id,COUNT(DISTINCT json_extract(CASE WHEN json_valid(payload_json) THEN payload_json ELSE '{}' END,'$.bookmark_id')) failed_job_count FROM jobs WHERE type='bookmark.process' AND status='failed' GROUP BY user_id) failed ON failed.user_id=u.id
		GROUP BY u.id `+orderBy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load users")
		return
	}
	defer rows.Close()
	var users []map[string]any
	for rows.Next() {
		var id, email, name, created string
		var banned, invitePending bool
		var bookmarkCount, collectionCount, failedJobCount int
		var lastBookmark sql.NullString
		_ = rows.Scan(&id, &email, &name, &created, &banned, &invitePending, &bookmarkCount, &collectionCount, &lastBookmark, &failedJobCount)
		users = append(users, map[string]any{"id": id, "email": email, "name": name, "created_at": created, "banned": banned, "invite_pending": invitePending, "bookmark_count": bookmarkCount, "collection_count": collectionCount, "failed_job_count": failedJobCount, "last_bookmark_at": nullStringMap(lastBookmark), "is_admin": a.cfg.AdminEmails[strings.ToLower(email)]})
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
	updates := map[string]any{}
	submitted := func(key string) string {
		value, ok := body[key]
		if !ok || value == nil {
			return ""
		}
		return strings.TrimSpace(requestSettingString(value))
	}
	for key, value := range body {
		if !runtimeconfig.Allowed(key) {
			writeError(w, http.StatusBadRequest, "Unknown setting")
			return
		}
		if runtimeconfig.IsSecret(key) && strings.TrimSpace(requestSettingString(value)) == "" {
			continue
		}
		updates[key] = value
	}

	if rawProvider, ok := body[runtimeconfig.KeyAIProvider]; ok {
		effective, err := a.runtime.Effective(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load settings")
			return
		}
		targetProvider := providers.NormalizeModelProvider(requestSettingString(rawProvider))
		if targetProvider != effective.AIProvider {
			definition := providers.ModelProviderDefinition(targetProvider)
			if submitted(runtimeconfig.KeyAIModel) == "" {
				if definition.DefaultModel == "" {
					writeError(w, http.StatusBadRequest, "Model is required when changing model providers")
					return
				}
				updates[runtimeconfig.KeyAIModel] = definition.DefaultModel
			}
			if submitted(runtimeconfig.KeyAIBaseURL) == "" {
				if definition.BaseURL == "" {
					writeError(w, http.StatusBadRequest, "Base URL is required when changing model providers")
					return
				}
				updates[runtimeconfig.KeyAIBaseURL] = definition.BaseURL
			}
			if submitted(runtimeconfig.KeyAIAPIKey) == "" {
				if targetProvider == providers.ProviderGemini {
					legacy, err := a.runtime.StatusValue(r.Context(), runtimeconfig.KeyGeminiAPIKey)
					if err != nil {
						writeError(w, http.StatusInternalServerError, "Could not load settings")
						return
					}
					if !legacy.Configured {
						writeError(w, http.StatusBadRequest, "API Key is required when changing model providers")
						return
					}
					updates[runtimeconfig.KeyAIAPIKey] = ""
				} else if definition.APIKeyOptional {
					updates[runtimeconfig.KeyAIAPIKey] = ""
				} else {
					writeError(w, http.StatusBadRequest, "API Key is required when changing model providers")
					return
				}
			}
		}
	}

	changed := make([]string, 0, len(updates))
	for key := range updates {
		changed = append(changed, key)
	}
	sort.Strings(changed)
	if len(changed) == 0 {
		writeError(w, http.StatusBadRequest, "No fields to update")
		return
	}
	if err := a.runtime.Apply(r.Context(), updates, user.Email, "primary"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.auditEvent(r.Context(), user.ID, "admin.settings.update", "settings", "", map[string]any{"keys": changed})
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
	status, _ := a.runtime.StatusValue(r.Context(), key)
	writeJSON(w, http.StatusOK, map[string]any{"status": "removed", "key": key, "has_env_fallback": status.Source == "environment"})
}

func (a *App) adminAPIUsage(w http.ResponseWriter, r *http.Request, user auth.User) {
	effective, _ := a.runtime.Effective(r.Context())
	definition := providers.ModelProviderDefinition(effective.AIProvider)
	aiConfigured := effective.AIModel != "" && effective.AIBaseURL != "" && (effective.AIAPIKey != "" || definition.APIKeyOptional)
	geminiConfigured := effective.AIProvider == providers.ProviderGemini && effective.AIAPIKey != ""
	usage := a.usage.Snapshot()
	failedJobs, err := recentFailedJobs(r.Context(), a.db, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load failed jobs")
		return
	}
	failedSummaries, err := recentFailedSummaries(r.Context(), a.db, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load failed summaries")
		return
	}
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
		"ai_configured":           aiConfigured,
		"gemini_configured":       geminiConfigured,
		"summaries_completed":     countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM ai_summaries WHERE processing_status='completed'`),
		"summaries_pending":       countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM ai_summaries WHERE processing_status='pending'`),
		"summaries_failed":        countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM ai_summaries WHERE processing_status='failed'`),
		"background_jobs_failed":  countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM jobs WHERE status='failed'`),
		"background_jobs_queued":  countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM jobs WHERE status='queued'`),
		"background_jobs_running": countWhere(r.Context(), a.db, `SELECT COUNT(*) FROM jobs WHERE status='leased'`),
		"recent_failed_jobs":      failedJobs,
		"recent_failed_summaries": failedSummaries,
	})
}

func (a *App) adminRetryJob(w http.ResponseWriter, r *http.Request, user auth.User) {
	newJobID, err := a.retryAdminJob(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		var retryErr *adminRetryError
		if errors.As(err, &retryErr) {
			if retryErr.JobID != "" {
				writeJSON(w, retryErr.Status, map[string]any{"detail": retryErr.Detail, "job_id": retryErr.JobID})
			} else {
				writeError(w, retryErr.Status, retryErr.Detail)
			}
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not retry job")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "queued", "job_id": newJobID, "retry_of_job_id": r.PathValue("id")})
}

type adminRetryError struct {
	Status int
	Detail string
	JobID  string
}

func (e *adminRetryError) Error() string { return e.Detail }

func retryError(status int, detail string) error {
	return &adminRetryError{Status: status, Detail: detail}
}

func (a *App) retryAdminJob(ctx context.Context, actorUserID, failedJobID string) (string, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var userID sql.NullString
	var jobType, status, payload string
	var priority, maxAttempts int
	err = tx.QueryRowContext(ctx, `SELECT user_id,type,status,priority,payload_json,max_attempts FROM jobs WHERE id=?`, failedJobID).Scan(&userID, &jobType, &status, &priority, &payload, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return "", retryError(http.StatusNotFound, "Job not found")
	}
	if err != nil {
		return "", err
	}
	if status != "failed" {
		return "", retryError(http.StatusConflict, "Only failed jobs can be retried")
	}
	if jobType != "bookmark.process" {
		return "", retryError(http.StatusConflict, "This job type cannot be safely retried from Administration")
	}

	bookmarkID := ""
	var decoded map[string]any
	if json.Unmarshal([]byte(payload), &decoded) != nil {
		return "", retryError(http.StatusConflict, "Job payload is invalid and cannot be retried")
	}
	bookmarkID, _ = decoded["bookmark_id"].(string)
	if bookmarkID == "" {
		return "", retryError(http.StatusConflict, "Bookmark job has no bookmark ID")
	}
	var bookmarkUserID, bookmarkURL string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,url FROM bookmarks WHERE id=?`, bookmarkID).Scan(&bookmarkUserID, &bookmarkURL); errors.Is(err, sql.ErrNoRows) {
		return "", retryError(http.StatusConflict, "The bookmark no longer exists")
	} else if err != nil {
		return "", err
	}
	userID = sql.NullString{String: bookmarkUserID, Valid: true}
	var activeJobID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE type=? AND status IN ('queued','leased') AND json_extract(CASE WHEN json_valid(payload_json) THEN payload_json ELSE '{}' END,'$.bookmark_id')=? ORDER BY created_at LIMIT 1`, jobType, bookmarkID).Scan(&activeJobID)
	if err == nil {
		return "", &adminRetryError{Status: http.StatusConflict, Detail: "This bookmark already has an active job", JobID: activeJobID}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var retryOf string
	_ = tx.QueryRowContext(ctx, `SELECT id FROM capture_attempts WHERE bookmark_id=? AND user_id=? ORDER BY queued_at DESC,id DESC LIMIT 1`, bookmarkID, bookmarkUserID).Scan(&retryOf)
	attemptID := ids.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO capture_attempts(id,bookmark_id,user_id,retry_of_id,status,requested_url,engine,engine_version,queued_at) VALUES(?,?,?,NULLIF(?,''),'queued',?,'direct_http',?,?)`, attemptID, bookmarkID, bookmarkUserID, retryOf, bookmarkURL, safefetch.ExtractorVersion, now)
	if err != nil {
		return "", err
	}
	rawPayload, err := json.Marshal(map[string]string{"bookmark_id": bookmarkID, "url": bookmarkURL, "capture_attempt_id": attemptID})
	if err != nil {
		return "", err
	}
	payload = string(rawPayload)
	newJobID := ids.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,user_id,type,status,priority,payload_json,max_attempts,run_after,created_at,updated_at) VALUES(?,?,?,'queued',?,?,?,?,?,?)`, newJobID, userID, jobType, priority, payload, maxAttempts, now, now, now)
	if err != nil {
		return "", err
	}
	if bookmarkID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE ai_summaries SET processing_status='pending',updated_at=? WHERE bookmark_id=?`, now, bookmarkID); err != nil {
			return "", err
		}
	}
	auditMetadata, _ := json.Marshal(sanitizeAuditMetadata(map[string]any{"new_job_id": newJobID, "job_type": jobType, "bookmark_id": bookmarkID}))
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_user_id,action,target_type,target_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, ids.New(), actorUserID, "admin.job.retry", "job", failedJobID, string(auditMetadata), now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return newJobID, nil
}

func (a *App) adminRetryJobs(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		JobIDs []string `json:"job_ids"`
		UserID string   `json:"user_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	body.UserID = strings.TrimSpace(body.UserID)
	if (len(body.JobIDs) == 0) == (body.UserID == "") {
		writeError(w, http.StatusBadRequest, "Provide either job_ids or user_id")
		return
	}
	jobIDs := cleanAdminJobIDs(body.JobIDs, 51)
	if len(jobIDs) > 50 {
		writeError(w, http.StatusBadRequest, "A maximum of 50 jobs can be retried at once")
		return
	}
	if body.UserID != "" {
		var exists int
		if err := a.db.QueryRowContext(r.Context(), `SELECT 1 FROM users WHERE id=?`, body.UserID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "User not found")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load user")
			return
		}
		var err error
		jobIDs, err = recentRetryableJobIDsForUser(r.Context(), a.db, body.UserID, 100)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load failed jobs")
			return
		}
	}
	queued := []map[string]string{}
	failed := []map[string]string{}
	for _, failedJobID := range jobIDs {
		newJobID, err := a.retryAdminJob(r.Context(), user.ID, failedJobID)
		if err != nil {
			failed = append(failed, map[string]string{"job_id": failedJobID, "error": err.Error()})
			continue
		}
		queued = append(queued, map[string]string{"job_id": newJobID, "retry_of_job_id": failedJobID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"queued_count": len(queued), "failed_count": len(failed), "queued": queued, "failed": failed})
}

func cleanAdminJobIDs(values []string, limit int) []string {
	seen := map[string]bool{}
	result := make([]string, 0, min(len(values), limit))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) == limit {
			break
		}
	}
	return result
}

func recentRetryableJobIDsForUser(ctx context.Context, db *sql.DB, userID string, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,payload_json FROM jobs WHERE user_id=? AND type='bookmark.process' AND status='failed' ORDER BY updated_at DESC,id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idsByBookmark := map[string]bool{}
	result := []string{}
	for rows.Next() && len(result) < limit {
		var id, payload string
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, err
		}
		var decoded struct {
			BookmarkID string `json:"bookmark_id"`
		}
		if json.Unmarshal([]byte(payload), &decoded) != nil || decoded.BookmarkID == "" || idsByBookmark[decoded.BookmarkID] {
			continue
		}
		idsByBookmark[decoded.BookmarkID] = true
		result = append(result, id)
	}
	return result, rows.Err()
}

func recentFailedJobs(ctx context.Context, db *sql.DB, limit int) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `SELECT j.id,j.type,j.attempts,j.max_attempts,COALESCE(j.last_error,''),j.created_at,j.updated_at,
		COALESCE(j.user_id,''),COALESCE(u.email,''),COALESCE(b.id,''),COALESCE(b.title,''),COALESCE(b.url,'')
		FROM jobs j
		LEFT JOIN users u ON u.id=j.user_id
		LEFT JOIN bookmarks b ON b.id=json_extract(CASE WHEN json_valid(j.payload_json) THEN j.payload_json ELSE '{}' END,'$.bookmark_id')
		WHERE j.status='failed' ORDER BY j.updated_at DESC,j.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, jobType, lastError, created, updated, userID, email, bookmarkID, title, bookmarkURL string
		var attempts, maxAttempts int
		if err := rows.Scan(&id, &jobType, &attempts, &maxAttempts, &lastError, &created, &updated, &userID, &email, &bookmarkID, &title, &bookmarkURL); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "type": jobType, "attempts": attempts, "max_attempts": maxAttempts, "error": lastError,
			"created_at": created, "failed_at": updated, "user_id": userID, "user_email": email,
			"bookmark_id": bookmarkID, "bookmark_title": title, "bookmark_url": bookmarkURL, "retryable": jobType == "bookmark.process",
		})
	}
	return items, rows.Err()
}

func recentFailedSummaries(ctx context.Context, db *sql.DB, limit int) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.bookmark_id,COALESCE(b.title,''),COALESCE(b.url,''),COALESCE(u.email,''),
		s.validation_status,s.validation_reasons_json,s.provider,s.model,s.updated_at
		FROM ai_summaries s JOIN bookmarks b ON b.id=s.bookmark_id LEFT JOIN users u ON u.id=s.user_id
		WHERE s.processing_status='failed' ORDER BY s.updated_at DESC,s.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var bookmarkID, title, bookmarkURL, email, validationStatus, reasonsRaw, provider, model, failedAt string
		if err := rows.Scan(&bookmarkID, &title, &bookmarkURL, &email, &validationStatus, &reasonsRaw, &provider, &model, &failedAt); err != nil {
			return nil, err
		}
		reasons := []string{}
		if err := json.Unmarshal([]byte(reasonsRaw), &reasons); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"bookmark_id": bookmarkID, "bookmark_title": title, "bookmark_url": bookmarkURL, "user_email": email,
			"validation_status": validationStatus, "validation_reasons": reasons, "provider": provider, "model": model, "failed_at": failedAt,
		})
	}
	return items, rows.Err()
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
