package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	bookmarksvc "github.com/glnarayanan/arivu/internal/bookmarks"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/runtimeconfig"
	"github.com/glnarayanan/arivu/internal/secrets"
)

const xScopes = "bookmark.read tweet.read users.read offline.access"

func (a *App) xEnabled(w http.ResponseWriter, r *http.Request) {
	effective, err := a.runtime.Effective(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load X settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": effective.XIntegrationEnabled})
}

func (a *App) xConnect(w http.ResponseWriter, r *http.Request, user auth.User) {
	xCfg, ok := a.xReady(w, r)
	if !ok {
		return
	}
	verifier := randomURLToken(64)
	state := randomURLToken(32)
	challenge := pkceChallenge(verifier)
	redirectURI := xCfg.XRedirectURI
	verifierCipher, err := secrets.Seal(a.cfg.SecretKey, verifier)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not start X connection")
		return
	}
	now := time.Now().UTC()
	_, _ = a.db.ExecContext(r.Context(), `DELETE FROM oauth_states WHERE provider='x' AND expires_at<=?`, now.Format(time.RFC3339))
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO oauth_states(state,user_id,provider,verifier_cipher,redirect_uri,expires_at,created_at) VALUES(?,?,?,?,?,?,?)`, state, user.ID, "x", verifierCipher, redirectURI, now.Add(10*time.Minute).Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not start X connection")
		return
	}
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", xCfg.XClientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("scope", xScopes)
	values.Set("state", state)
	values.Set("code_challenge", challenge)
	values.Set("code_challenge_method", "S256")
	authorizeURL := strings.TrimRight(a.cfg.XAuthorizeURL, "?")
	if authorizeURL == "" {
		authorizeURL = "https://twitter.com/i/oauth2/authorize"
	}
	writeJSON(w, http.StatusOK, map[string]any{"auth_url": authorizeURL + "?" + values.Encode()})
}

func (a *App) xCallback(w http.ResponseWriter, r *http.Request, user auth.User) {
	xCfg, ok := a.xReady(w, r)
	if !ok {
		return
	}
	var body struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Code == "" || body.State == "" {
		writeError(w, http.StatusBadRequest, "Invalid X callback")
		return
	}
	var verifierCipher, redirectURI, expires string
	err := a.db.QueryRowContext(r.Context(), `SELECT verifier_cipher,redirect_uri,expires_at FROM oauth_states WHERE state=? AND user_id=? AND provider='x'`, body.State, user.ID).Scan(&verifierCipher, &redirectURI, &expires)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid or expired OAuth state")
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, expires)
	if err != nil || time.Now().UTC().After(expiresAt) {
		writeError(w, http.StatusBadRequest, "Invalid or expired OAuth state")
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `DELETE FROM oauth_states WHERE state=?`, body.State)
	verifier, err := secrets.Open(a.cfg.SecretKey, verifierCipher)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid OAuth state")
		return
	}
	client := a.xClient("", xCfg)
	token, err := client.ExchangeCode(r.Context(), body.Code, redirectURI, verifier)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to exchange authorization code")
		return
	}
	profile, err := client.Profile(r.Context(), token.AccessToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to fetch X profile")
		return
	}
	accessCipher, err := secrets.Seal(a.cfg.SecretKey, token.AccessToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not store X connection")
		return
	}
	refreshCipher := ""
	if token.RefreshToken != "" {
		refreshCipher, err = secrets.Seal(a.cfg.SecretKey, token.RefreshToken)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not store X connection")
			return
		}
	}
	expiresAt = time.Now().UTC().Add(time.Duration(fallbackInt(token.ExpiresIn, 7200)) * time.Second)
	now := time.Now().UTC().Format(time.RFC3339)
	scopes, _ := json.Marshal(strings.Fields(token.Scope))
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO x_connections(id,user_id,x_user_id,x_username,x_name,x_profile_image,access_token_cipher,refresh_token_cipher,token_expires_at,scopes_json,connected_at,sync_status,total_synced)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(user_id) DO UPDATE SET x_user_id=excluded.x_user_id,x_username=excluded.x_username,x_name=excluded.x_name,x_profile_image=excluded.x_profile_image,access_token_cipher=excluded.access_token_cipher,refresh_token_cipher=excluded.refresh_token_cipher,token_expires_at=excluded.token_expires_at,scopes_json=excluded.scopes_json,connected_at=excluded.connected_at,sync_status='idle'`,
		ids.New(), user.ID, profile.ID, profile.Username, profile.Name, profile.ProfileImageURL, accessCipher, nullableString(refreshCipher), expiresAt.Format(time.RFC3339), string(scopes), now, "idle", 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not store X connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "x_username": profile.Username, "x_name": profile.Name, "x_profile_image": profile.ProfileImageURL})
}

func (a *App) xStatus(w http.ResponseWriter, r *http.Request, user auth.User) {
	if _, ok := a.xReady(w, r); !ok {
		return
	}
	conn, err := a.xConnection(r.Context(), user.ID)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load X connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connected":       true,
		"x_username":      conn.Username,
		"x_name":          conn.Name,
		"x_profile_image": conn.ProfileImage,
		"connected_at":    conn.ConnectedAt,
		"last_sync_at":    nullStringMap(conn.LastSyncAt),
		"sync_status":     conn.SyncStatus,
		"total_synced":    conn.TotalSynced,
	})
}

func (a *App) xDisconnect(w http.ResponseWriter, r *http.Request, user auth.User) {
	xCfg, ok := a.xReady(w, r)
	if !ok {
		return
	}
	conn, err := a.xConnection(r.Context(), user.ID)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "X account not connected")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load X connection")
		return
	}
	if access, err := secrets.Open(a.cfg.SecretKey, conn.AccessCipher); err == nil {
		_ = a.xClient("", xCfg).Revoke(r.Context(), access)
	}
	_, _ = a.db.ExecContext(r.Context(), `DELETE FROM x_connections WHERE user_id=?`, user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"disconnected": true})
}

func (a *App) xSync(w http.ResponseWriter, r *http.Request, user auth.User) {
	xCfg, ok := a.xReady(w, r)
	if !ok {
		return
	}
	conn, err := a.xConnection(r.Context(), user.ID)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "X account not connected")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load X connection")
		return
	}
	if conn.SyncStatus == "syncing" {
		writeError(w, http.StatusConflict, "Sync already in progress")
		return
	}
	if conn.XUserID == "" {
		writeError(w, http.StatusBadRequest, "X user ID not found. Please reconnect your X account.")
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE x_connections SET sync_status='syncing' WHERE user_id=?`, user.ID)
	result, syncErr := a.syncXBookmarks(r, user, conn, xCfg)
	if syncErr != nil {
		_, _ = a.db.ExecContext(r.Context(), `UPDATE x_connections SET sync_status='error' WHERE user_id=?`, user.ID)
		writeError(w, http.StatusBadGateway, syncErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type xConnectionRow struct {
	UserID        string
	XUserID       string
	Username      string
	Name          string
	ProfileImage  string
	AccessCipher  string
	RefreshCipher sql.NullString
	TokenExpires  sql.NullString
	ConnectedAt   string
	LastSyncAt    sql.NullString
	SyncStatus    string
	TotalSynced   int
	NextCursor    sql.NullString
}

func (a *App) xConnection(ctx context.Context, userID string) (xConnectionRow, error) {
	var conn xConnectionRow
	err := a.db.QueryRowContext(ctx, `SELECT user_id,COALESCE(x_user_id,''),COALESCE(x_username,''),COALESCE(x_name,''),COALESCE(x_profile_image,''),access_token_cipher,refresh_token_cipher,token_expires_at,connected_at,last_sync_at,sync_status,total_synced,next_cursor FROM x_connections WHERE user_id=?`, userID).Scan(&conn.UserID, &conn.XUserID, &conn.Username, &conn.Name, &conn.ProfileImage, &conn.AccessCipher, &conn.RefreshCipher, &conn.TokenExpires, &conn.ConnectedAt, &conn.LastSyncAt, &conn.SyncStatus, &conn.TotalSynced, &conn.NextCursor)
	return conn, err
}

func (a *App) syncXBookmarks(r *http.Request, user auth.User, conn xConnectionRow, xCfg runtimeconfig.Effective) (map[string]any, error) {
	access, err := a.xAccessToken(r, conn, xCfg)
	if err != nil {
		return nil, err
	}
	client := a.xClient(access, xCfg)
	existingTweetIDs, existingURLs := a.existingXDedupSets(r, user.ID)
	var totalFetched, newBookmarks, duplicates int
	cursor := conn.NextCursor.String
	for page := 0; page < 10; page++ {
		result, err := client.BookmarkPage(r.Context(), conn.XUserID, cursor, 100)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch X bookmarks")
		}
		totalFetched += len(result.Bookmarks)
		for _, tweet := range result.Bookmarks {
			if existingTweetIDs[tweet.ID] {
				duplicates++
				continue
			}
			author := result.Users[tweet.AuthorID]
			bookmarkURL := bestTweetURL(tweet, author)
			normalized := normalizeForDedup(bookmarkURL)
			if normalized != "" && existingURLs[normalized] {
				duplicates++
				continue
			}
			if err := a.insertXBookmark(r, user.ID, tweet, author, bookmarkURL, result); err != nil {
				if strings.Contains(err.Error(), "UNIQUE") {
					duplicates++
					continue
				}
				return nil, err
			}
			newBookmarks++
			existingTweetIDs[tweet.ID] = true
			if normalized != "" {
				existingURLs[normalized] = true
			}
		}
		cursor = result.NextToken
		if cursor == "" {
			break
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.db.ExecContext(r.Context(), `UPDATE x_connections SET sync_status='idle',last_sync_at=?,total_synced=total_synced+?,next_cursor=? WHERE user_id=?`, now, newBookmarks, nullableString(cursor), user.ID)
	return map[string]any{"total_fetched": totalFetched, "new_bookmarks": newBookmarks, "duplicates_skipped": duplicates, "sync_status": "idle", "has_more": cursor != ""}, nil
}

func (a *App) insertXBookmark(r *http.Request, userID string, tweet providers.XBookmark, author providers.XUser, bookmarkURL string, page providers.XBookmarkPage) error {
	now := time.Now().UTC().Format(time.RFC3339)
	sourceText := tweet.EvidenceText()
	title := sourceText
	if len(title) > 100 {
		title = title[:100] + "..."
	}
	parsed, _ := url.Parse(bookmarkURL)
	metrics, _ := json.Marshal(tweet.PublicMetrics)
	bookmarkID := ids.New()
	tweetURL := "https://x.com/" + fallbackString(author.Username, "i") + "/status/" + tweet.ID
	hasExternalArticle := normalizeForDedup(bookmarkURL) != normalizeForDedup(tweetURL)
	contentKind := "x_post"
	if hasExternalArticle {
		contentKind = "x_article"
	} else if !hasSubstantiveXText(sourceText) && len(tweet.Attachments.MediaKeys) > 0 {
		contentKind = "x_media"
	} else if !hasSubstantiveXText(sourceText) {
		contentKind = "x_link"
	}
	publisherKey := "x:" + fallbackString(tweet.AuthorID, author.Username)
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO bookmarks(id,user_id,url,title,description,domain,text_content,read_status,source,x_tweet_id,x_author_username,x_author_name,x_tweet_url,x_metrics_json,canonical_url,content_kind,source_published_at,source_author_id,source_publisher_key,fetch_version,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, bookmarkID, userID, bookmarkURL, title, sourceText, parsed.Hostname(), sourceText, false, "x", tweet.ID, author.Username, author.Name, tweetURL, string(metrics), bookmarkURL, contentKind, nullableString(tweet.CreatedAt), nullableString(tweet.AuthorID), publisherKey, "x-api-v1", now, now)
	if err != nil {
		return err
	}
	status, reasons := xEvidenceQuality(sourceText, len(tweet.Attachments.MediaKeys) > 0)
	if _, err := a.bookmarks.UpsertEvidence(r.Context(), userID, bookmarkID, bookmarksvc.BookmarkEvidence{
		Kind: "source_post", Origin: "x_api", Authority: 100, Text: sourceText, CanonicalURL: tweetURL,
		AuthorID: tweet.AuthorID, PublisherKey: publisherKey, PublishedAt: tweet.CreatedAt,
		ExtractionMethod: "x_api", QualityStatus: status, QualityReasons: reasons, ExtractorVersion: "x-api-v1", Selected: !hasExternalArticle,
	}); err != nil {
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM bookmarks WHERE id=? AND user_id=?`, bookmarkID, userID)
		return err
	}
	for _, reference := range tweet.References {
		referenced, ok := page.Tweets[reference.ID]
		if !ok || referenced.EvidenceText() == "" {
			continue
		}
		referenceAuthor := page.Users[referenced.AuthorID]
		referencePublisher := "x:" + fallbackString(referenced.AuthorID, referenceAuthor.Username)
		_, _ = a.bookmarks.UpsertEvidence(r.Context(), userID, bookmarkID, bookmarksvc.BookmarkEvidence{
			Kind: "referenced_x_post_" + fallbackString(reference.Type, "related"), Origin: "x_api", Authority: 90,
			Text: referenced.EvidenceText(), CanonicalURL: "https://x.com/" + fallbackString(referenceAuthor.Username, "i") + "/status/" + referenced.ID,
			AuthorID: referenced.AuthorID, PublisherKey: referencePublisher, PublishedAt: referenced.CreatedAt,
			ExtractionMethod: "x_api_expansion", QualityStatus: "complete", ExtractorVersion: "x-api-v1",
		})
	}
	for _, mediaKey := range tweet.Attachments.MediaKeys {
		media, ok := page.Media[mediaKey]
		if !ok {
			continue
		}
		mediaStatus, mediaReasons := "partial", []string{}
		if strings.TrimSpace(media.AltText) == "" {
			mediaStatus, mediaReasons = "metadata_only", []string{"media_without_transcript"}
		}
		_, _ = a.bookmarks.UpsertEvidence(r.Context(), userID, bookmarkID, bookmarksvc.BookmarkEvidence{
			Kind: "x_media_" + fallbackString(media.Type, "attachment"), Origin: "x_api", Authority: 60,
			Text: strings.TrimSpace(media.AltText), CanonicalURL: fallbackString(media.URL, media.PreviewImageURL),
			AuthorID: tweet.AuthorID, PublisherKey: publisherKey, PublishedAt: tweet.CreatedAt,
			ExtractionMethod: "x_media_metadata", QualityStatus: mediaStatus, QualityReasons: mediaReasons, ExtractorVersion: "x-api-v1",
		})
	}
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, ids.New(), bookmarkID, userID, "pending", now, now)
	_ = a.jobs.Enqueue(r.Context(), userID, "bookmark.process", bookmarksvc.ProcessPayload(bookmarkID, bookmarkURL, ""))
	return nil
}

func xEvidenceQuality(text string, hasMedia bool) (string, []string) {
	if hasSubstantiveXText(text) {
		return "complete", []string{}
	}
	if hasMedia {
		return "metadata_only", []string{"media_without_transcript"}
	}
	return "metadata_only", []string{"link_only"}
}

func hasSubstantiveXText(text string) bool {
	for _, field := range strings.Fields(text) {
		lower := strings.ToLower(strings.Trim(field, ".,;:!?()[]{}<>"))
		if lower != "" && !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			return true
		}
	}
	return false
}

func (a *App) xAccessToken(r *http.Request, conn xConnectionRow, xCfg runtimeconfig.Effective) (string, error) {
	access, err := secrets.Open(a.cfg.SecretKey, conn.AccessCipher)
	if err != nil {
		return "", err
	}
	if !conn.TokenExpires.Valid || !conn.RefreshCipher.Valid {
		return access, nil
	}
	expiresAt, err := time.Parse(time.RFC3339, conn.TokenExpires.String)
	if err != nil || time.Now().UTC().Before(expiresAt.Add(-5*time.Minute)) {
		return access, nil
	}
	refresh, err := secrets.Open(a.cfg.SecretKey, conn.RefreshCipher.String)
	if err != nil {
		return "", err
	}
	token, err := a.xClient("", xCfg).Refresh(r.Context(), refresh)
	if err != nil {
		return "", err
	}
	accessCipher, err := secrets.Seal(a.cfg.SecretKey, token.AccessToken)
	if err != nil {
		return "", err
	}
	refreshCipher := conn.RefreshCipher.String
	if token.RefreshToken != "" {
		refreshCipher, err = secrets.Seal(a.cfg.SecretKey, token.RefreshToken)
		if err != nil {
			return "", err
		}
	}
	expiresAt = time.Now().UTC().Add(time.Duration(fallbackInt(token.ExpiresIn, 7200)) * time.Second)
	_, _ = a.db.ExecContext(r.Context(), `UPDATE x_connections SET access_token_cipher=?,refresh_token_cipher=?,token_expires_at=?,sync_status='idle' WHERE user_id=?`, accessCipher, refreshCipher, expiresAt.Format(time.RFC3339), conn.UserID)
	return token.AccessToken, nil
}

func (a *App) existingXDedupSets(r *http.Request, userID string) (map[string]bool, map[string]bool) {
	tweetIDs := map[string]bool{}
	urls := map[string]bool{}
	rows, err := a.db.QueryContext(r.Context(), `SELECT COALESCE(x_tweet_id,''),url FROM bookmarks WHERE user_id=?`, userID)
	if err != nil {
		return tweetIDs, urls
	}
	defer rows.Close()
	for rows.Next() {
		var tweetID, rawURL string
		_ = rows.Scan(&tweetID, &rawURL)
		if tweetID != "" {
			tweetIDs[tweetID] = true
		}
		if normalized := normalizeForDedup(rawURL); normalized != "" {
			urls[normalized] = true
		}
	}
	return tweetIDs, urls
}

func (a *App) xReady(w http.ResponseWriter, r *http.Request) (runtimeconfig.Effective, bool) {
	xCfg, err := a.runtime.Effective(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load X settings")
		return runtimeconfig.Effective{}, false
	}
	if !xCfg.XIntegrationEnabled {
		writeError(w, http.StatusNotFound, "Not found")
		return runtimeconfig.Effective{}, false
	}
	if xCfg.XClientID == "" {
		writeError(w, http.StatusServiceUnavailable, "X integration not configured")
		return runtimeconfig.Effective{}, false
	}
	return xCfg, true
}

func (a *App) xClient(accessToken string, xCfg runtimeconfig.Effective) providers.XClient {
	return providers.XClient{AccessToken: accessToken, ClientID: xCfg.XClientID, ClientSecret: xCfg.XClientSecret, APIBaseURL: a.cfg.XAPIBaseURL, HTTP: a.xHTTP}
}

func bestTweetURL(tweet providers.XBookmark, author providers.XUser) string {
	if urls, ok := tweet.Entities["urls"].([]any); ok {
		for _, raw := range urls {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			expanded, _ := entry["expanded_url"].(string)
			parsed, err := url.Parse(expanded)
			if err == nil && parsed.Hostname() != "" && !strings.Contains(parsed.Hostname(), "x.com") && !strings.Contains(parsed.Hostname(), "twitter.com") {
				return expanded
			}
		}
	}
	return "https://x.com/" + fallbackString(author.Username, "i") + "/status/" + tweet.ID
}

func normalizeForDedup(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	values := parsed.Query()
	for key := range values {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" {
			values.Del(key)
		}
	}
	parsed.RawQuery = values.Encode()
	parsed.Fragment = ""
	return strings.ToLower(strings.TrimRight(parsed.String(), "/"))
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomURLToken(size int) string {
	buf := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func fallbackInt(value int, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func fallbackString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullStringMap(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
