package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/bookmarks"
	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/runtimeconfig"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

//go:embed web/*
var webFS embed.FS

var webAssetModTime = time.Unix(0, 0).UTC()

type App struct {
	cfg       config.Config
	db        *sql.DB
	auth      *auth.Service
	bookmarks *bookmarks.Service
	jobs      *jobs.Queue
	fetcher   *safefetch.Client
	runtime   *runtimeconfig.Service
	xHTTP     *http.Client
	startedAt time.Time
	usage     *providerUsage
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func New(cfg config.Config) (*App, error) {
	db, err := database.Open(context.Background(), cfg.DBPath)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, db: db, startedAt: time.Now().UTC(), usage: newProviderUsage()}
	a.runtime = runtimeconfig.New(db, cfg)
	a.auth = auth.New(db, cfg)
	a.auth.SetRuntimeSettings(a.runtime.Effective)
	a.jobs = jobs.New(db)
	a.fetcher = safefetch.NewWithUserAgent(cfg.FetchUserAgent)
	a.bookmarks = bookmarks.New(db, a.jobs, a.fetcher, providers.GeminiClient{APIKey: cfg.GeminiAPIKey})
	a.bookmarks.SetGeminiProvider(a.geminiClient)
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.startWorkers(ctx)
	return a, nil
}

func (a *App) Close() {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	if a.db != nil {
		_ = a.db.Close()
	}
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("POST /api/auth/signup", a.auth.Signup)
	mux.HandleFunc("POST /api/auth/login", a.auth.Login)
	mux.HandleFunc("POST /api/auth/cli/login", a.auth.CLILogin)
	mux.HandleFunc("POST /api/auth/logout", a.withUser(a.auth.Logout))
	mux.HandleFunc("POST /api/auth/refresh", a.auth.RefreshWeb)
	mux.HandleFunc("POST /api/auth/cli/refresh", a.auth.RefreshCLI)
	mux.HandleFunc("GET /api/auth/me", a.withUser(a.auth.Me))
	mux.HandleFunc("POST /api/auth/extension-token", a.withUser(a.auth.ExtensionToken))
	mux.HandleFunc("POST /api/auth/forgot-password", a.auth.ForgotPassword)
	mux.HandleFunc("POST /api/auth/reset-password", a.auth.ResetPassword)
	mux.HandleFunc("POST /api/auth/change-password", a.withUser(a.auth.ChangePassword))
	mux.HandleFunc("GET /api/user/profile", a.withUser(a.auth.Profile))
	mux.HandleFunc("PUT /api/user/profile", a.withUser(a.auth.UpdateProfile))

	mux.HandleFunc("GET /api/bookmarks", a.withUser(a.bookmarks.List))
	mux.HandleFunc("POST /api/bookmarks", a.withUser(a.bookmarks.Create))
	mux.HandleFunc("POST /api/bookmarks/preview", a.withUser(a.bookmarks.Preview))
	mux.HandleFunc("GET /api/bookmarks/aged", a.withUser(a.bookmarks.Aged))
	mux.HandleFunc("GET /api/bookmarks/duplicates/detect", a.withUser(a.bookmarks.Duplicates))
	mux.HandleFunc("POST /api/bookmarks/bulk-delete", a.withUser(a.bookmarks.BulkDelete))
	mux.HandleFunc("POST /api/bookmarks/bulk-mark-read", a.withUser(a.bookmarks.BulkMarkRead))
	mux.HandleFunc("POST /api/bookmarks/merge", a.withUser(a.bookmarks.Merge))
	mux.HandleFunc("GET /api/bookmarks/{id}", a.withUser(a.bookmarks.Get))
	mux.HandleFunc("DELETE /api/bookmarks/{id}", a.withUser(a.bookmarks.Delete))
	mux.HandleFunc("PATCH /api/bookmarks/{id}/read-status", a.withUser(a.bookmarks.ReadStatus))
	mux.HandleFunc("POST /api/bookmarks/{id}/accessed", a.withUser(a.bookmarks.Accessed))
	mux.HandleFunc("POST /api/bookmarks/{id}/annotations", a.withUser(a.bookmarks.CreateAnnotation))
	mux.HandleFunc("GET /api/bookmarks/{id}/related", a.withUser(a.bookmarks.Related))
	mux.HandleFunc("POST /api/bookmarks/import", a.withUser(a.bookmarks.Import))
	mux.HandleFunc("GET /api/bookmarks/export", a.withUser(a.bookmarks.Export))
	mux.HandleFunc("POST /api/bookmarks/backup", a.withUser(a.bookmarks.Backup))
	mux.HandleFunc("GET /api/jobs/{id}", a.withUser(a.bookmarks.JobStatus))
	mux.HandleFunc("GET /api/import-jobs", a.withUser(a.bookmarks.ImportJobs))
	mux.HandleFunc("GET /api/import-jobs/{id}", a.withUser(a.bookmarks.ImportJob))

	mux.HandleFunc("GET /api/search", a.withUser(a.bookmarks.Search))
	mux.HandleFunc("GET /api/search/answer", a.withUser(a.bookmarks.SearchAnswer))
	mux.HandleFunc("GET /api/collections", a.withUser(a.bookmarks.Collections))
	mux.HandleFunc("POST /api/collections", a.withUser(a.bookmarks.CreateCollection))
	mux.HandleFunc("POST /api/collections/{id}/add", a.withUser(a.bookmarks.AddToCollection))
	mux.HandleFunc("GET /api/notes", a.withUser(a.bookmarks.Notes))
	mux.HandleFunc("POST /api/notes", a.withUser(a.bookmarks.CreateNote))
	mux.HandleFunc("GET /api/notes/{id}", a.withUser(a.bookmarks.GetNote))
	mux.HandleFunc("PATCH /api/notes/{id}", a.withUser(a.bookmarks.UpdateNote))
	mux.HandleFunc("DELETE /api/notes/{id}", a.withUser(a.bookmarks.DeleteNote))
	mux.HandleFunc("PATCH /api/annotations/{id}", a.withUser(a.bookmarks.UpdateAnnotation))
	mux.HandleFunc("DELETE /api/annotations/{id}", a.withUser(a.bookmarks.DeleteAnnotation))
	mux.HandleFunc("GET /api/tags", a.withUser(a.bookmarks.Tags))
	mux.HandleFunc("POST /api/tags", a.withUser(a.bookmarks.CreateTag))
	mux.HandleFunc("POST /api/tags/aliases", a.withUser(a.bookmarks.CreateTagAlias))
	mux.HandleFunc("GET /api/saved-searches", a.withUser(a.bookmarks.SavedSearches))
	mux.HandleFunc("POST /api/saved-searches", a.withUser(a.bookmarks.CreateSavedSearch))
	mux.HandleFunc("GET /api/review", a.withUser(a.bookmarks.Review))
	mux.HandleFunc("POST /api/review/{item_id}/complete", a.withUser(a.bookmarks.CompleteReview))
	mux.HandleFunc("POST /api/review/{item_id}/snooze", a.withUser(a.bookmarks.SnoozeReview))
	mux.HandleFunc("GET /api/inbox", a.withUser(a.bookmarks.Inbox))
	mux.HandleFunc("PATCH /api/inbox/{item_id}", a.withUser(a.bookmarks.UpdateInboxItem))
	mux.HandleFunc("GET /api/links", a.withUser(a.bookmarks.Links))
	mux.HandleFunc("POST /api/links", a.withUser(a.bookmarks.CreateLink))
	mux.HandleFunc("DELETE /api/links/{id}", a.withUser(a.bookmarks.DeleteLink))
	mux.HandleFunc("GET /api/cli/bookmarks", a.withAudience("cli", a.bookmarks.List))
	mux.HandleFunc("POST /api/cli/bookmarks", a.withAudience("cli", a.bookmarks.Create))
	mux.HandleFunc("POST /api/cli/bookmarks/preview", a.withAudience("cli", a.bookmarks.Preview))
	mux.HandleFunc("GET /api/extension/collections", a.withAudience("extension", a.bookmarks.Collections))
	mux.HandleFunc("POST /api/extension/bookmarks", a.withAudience("extension", a.bookmarks.Create))
	mux.HandleFunc("GET /api/analytics/summary", a.withUser(a.bookmarks.AnalyticsSummary))
	mux.HandleFunc("GET /api/analytics/reading-stats", a.withUser(a.bookmarks.AnalyticsSummary))
	mux.HandleFunc("GET /api/analytics/topics", a.withUser(a.bookmarks.AnalyticsTopics))
	mux.HandleFunc("GET /api/analytics/patterns", a.withUser(a.bookmarks.AnalyticsPatterns))
	mux.HandleFunc("GET /api/analytics/insights", a.withUser(a.bookmarks.AnalyticsInsights))
	mux.HandleFunc("GET /api/resurfacing", a.withUser(a.bookmarks.Resurfacing))
	mux.HandleFunc("POST /api/resurfacing/{id}/snooze", a.withUser(a.bookmarks.SnoozeResurfacing))
	mux.HandleFunc("POST /api/resurfacing/{id}/archive", a.withUser(a.bookmarks.ArchiveResurfacing))
	mux.HandleFunc("POST /api/resurfacing/{id}/unarchive", a.withUser(a.bookmarks.UnarchiveResurfacing))
	mux.HandleFunc("GET /api/memory-jogger", a.withUser(a.bookmarks.MemoryJogger))
	mux.HandleFunc("GET /api/knowledge-graph/explore", a.withUser(a.bookmarks.KnowledgeGraph))
	mux.HandleFunc("GET /api/knowledge-graph/search", a.withUser(a.bookmarks.GraphSearch))
	mux.HandleFunc("GET /api/knowledge-graph/expand-query", a.withUser(a.bookmarks.ExpandQuery))

	mux.HandleFunc("GET /api/admin/overview", a.withAdmin(a.adminOverview))
	mux.HandleFunc("GET /api/admin/system", a.withAdmin(a.adminSystem))
	mux.HandleFunc("GET /api/admin/users", a.withAdmin(a.adminUsers))
	mux.HandleFunc("GET /api/admin/users/{id}", a.withAdmin(a.adminGetUser))
	mux.HandleFunc("POST /api/admin/users/invite", a.withAdmin(a.adminInviteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/ban", a.withAdmin(a.adminBanUser))
	mux.HandleFunc("POST /api/admin/users/{id}/unban", a.withAdmin(a.adminUnbanUser))
	mux.HandleFunc("POST /api/admin/users/{id}/reset-password", a.withAdmin(a.adminResetPassword))
	mux.HandleFunc("DELETE /api/admin/users/{id}", a.withAdmin(a.adminDeleteUser))
	mux.HandleFunc("GET /api/admin/api-keys", a.withAdmin(a.adminAPIKeys))
	mux.HandleFunc("PUT /api/admin/api-keys", a.withAdmin(a.adminUpdateAPIKeys))
	mux.HandleFunc("DELETE /api/admin/api-keys/{key}", a.withAdmin(a.adminDeleteAPIKey))
	mux.HandleFunc("GET /api/admin/api-usage", a.withAdmin(a.adminAPIUsage))
	mux.HandleFunc("GET /api/admin/activity", a.withAdmin(a.adminActivity))
	mux.HandleFunc("GET /api/admin/collections-stats", a.withAdmin(a.adminCollectionsStats))
	mux.HandleFunc("GET /api/admin/audit-events", a.withAdmin(a.adminAuditEvents))

	mux.HandleFunc("GET /api/auth/x/enabled", a.xEnabled)
	mux.HandleFunc("GET /api/auth/x/status", a.withUser(a.xStatus))
	mux.HandleFunc("GET /api/auth/x/connect", a.withUser(a.xConnect))
	mux.HandleFunc("POST /api/auth/x/callback", a.withUser(a.xCallback))
	mux.HandleFunc("POST /api/auth/x/sync", a.withUser(a.xSync))
	mux.HandleFunc("POST /api/auth/x/disconnect", a.withUser(a.xDisconnect))

	mux.HandleFunc("/", a.frontend)

	return a.recoverPanic(a.securityHeaders(a.limitBody(a.requestLog(mux))))
}

func (a *App) geminiClient(ctx context.Context) providers.GeminiClient {
	key := a.cfg.GeminiAPIKey
	if effective, err := a.runtime.Effective(ctx); err == nil {
		key = effective.GeminiAPIKey
	}
	return providers.GeminiClient{APIKey: key, Recorder: a.usage.RecordGemini}
}

func (a *App) withUser(next func(http.ResponseWriter, *http.Request, auth.User)) http.HandlerFunc {
	return a.withAudience("web", next)
}

func (a *App) withAudience(audience string, next func(http.ResponseWriter, *http.Request, auth.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := a.auth.AuthenticateSession(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		if session.Audience != audience {
			writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		next(w, r, session.User)
	}
}

func (a *App) withAdmin(next func(http.ResponseWriter, *http.Request, auth.User)) http.HandlerFunc {
	return a.withUser(func(w http.ResponseWriter, r *http.Request, user auth.User) {
		if !a.cfg.AdminEmails[strings.ToLower(user.Email)] {
			writeError(w, http.StatusForbidden, "Admin access required")
			return
		}
		next(w, r, user)
	})
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "stack": "go-sqlite", "time": time.Now().UTC().Format(time.RFC3339)})
}

func (a *App) frontend(w http.ResponseWriter, r *http.Request) {
	clean := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if clean == "." || clean == "/" {
		clean = "index.html"
	}
	if clean == "favicon.ico" {
		clean = "favicon.svg"
	}
	if strings.HasPrefix(clean, "api/") {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if data, err := webFS.ReadFile("web/" + clean); err == nil {
		serveAsset(w, r, clean, data)
		return
	}
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Frontend unavailable")
		return
	}
	serveAsset(w, r, "index.html", data)
}

func serveAsset(w http.ResponseWriter, r *http.Request, name string, data []byte) {
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(name, ".webmanifest"):
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.Header().Set("ETag", assetETag(data))
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	}
	http.ServeContent(w, r, name, webAssetModTime, bytes.NewReader(data))
}

func assetETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' https: data: blob:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (a *App) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func (a *App) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic: %v", recovered)
				writeError(w, http.StatusInternalServerError, "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"detail": message})
}

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}
