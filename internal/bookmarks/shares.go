package bookmarks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
)

var publicRateCleanupUnix atomic.Int64

type shareInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	ExpiresAt   *string  `json:"expires_at"`
	Indexable   bool     `json:"indexable"`
	ItemIDs     []string `json:"item_ids"`
	ArtifactIDs []string `json:"artifact_ids"`
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func newShareToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Service) Shares(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,title,description,COALESCE(expires_at,''),COALESCE(revoked_at,''),indexable,created_at,updated_at FROM public_shares WHERE user_id=? ORDER BY created_at DESC`, user.ID)
	if err != nil {
		writeError(w, 500, "Could not list shares")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title, desc, expiry, revoked, created, updated string
		var indexable bool
		_ = rows.Scan(&id, &title, &desc, &expiry, &revoked, &indexable, &created, &updated)
		out = append(out, map[string]any{"id": id, "title": title, "description": desc, "expires_at": expiry, "revoked_at": revoked, "indexable": indexable, "created_at": created, "updated_at": updated})
	}
	writeJSON(w, 200, out)
}

func (s *Service) CreateShare(w http.ResponseWriter, r *http.Request, user auth.User) {
	var in shareInput
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Title) == "" || !validExpiry(in.ExpiresAt) {
		writeError(w, 400, "Title is required")
		return
	}
	token, err := newShareToken()
	if err != nil {
		writeError(w, 500, "Could not create share")
		return
	}
	id, now := ids.New(), time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO public_shares(id,user_id,token_digest,title,description,expires_at,indexable,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, user.ID, tokenDigest(token), strings.TrimSpace(in.Title), strings.TrimSpace(in.Description), nullableTime(in.ExpiresAt), in.Indexable, now, now)
	}
	if err == nil {
		err = replaceShareMembers(r.Context(), tx, id, user.ID, in.ItemIDs, in.ArtifactIDs, now)
	}
	if err != nil {
		if tx != nil {
			_ = tx.Rollback()
		}
		writeError(w, 400, "Invalid share membership or expiry")
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "Could not create share")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "token": token, "url": "/s/" + token, "title": strings.TrimSpace(in.Title)})
}

func nullableTime(v *string) any {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*v))
	if err != nil {
		return "invalid"
	}
	return t.UTC().Format(time.RFC3339)
}

func validExpiry(v *string) bool {
	if v == nil || strings.TrimSpace(*v) == "" {
		return true
	}
	_, err := time.Parse(time.RFC3339, strings.TrimSpace(*v))
	return err == nil
}

func replaceShareMembers(c context.Context, tx *sql.Tx, shareID, userID string, items, artifacts []string, now string) error {
	if _, err := tx.ExecContext(c, `DELETE FROM public_share_media WHERE share_id=?`, shareID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(c, `DELETE FROM public_share_artifacts WHERE share_id=?`, shareID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(c, `DELETE FROM public_share_items WHERE share_id=?`, shareID); err != nil {
		return err
	}
	for _, id := range items {
		res, err := tx.ExecContext(c, `INSERT INTO public_share_items(share_id,bookmark_id,evidence_id,public_title,public_description,public_url,public_domain,public_reader_html,public_text,public_published_at,added_at)
			SELECT ?,b.id,e.id,COALESCE(b.title,''),COALESCE(b.description,''),b.url,COALESCE(b.domain,''),COALESCE(e.sanitized_html,b.sanitized_html,''),COALESCE(e.content_text,b.text_content,''),COALESCE(e.published_at,b.source_published_at,b.created_at),?
			FROM bookmarks b LEFT JOIN bookmark_evidence e ON e.bookmark_id=b.id AND e.is_selected=1 WHERE b.id=? AND b.user_id=?`, shareID, now, id, userID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return errors.New("item not owned")
		}
	}
	if _, err := tx.ExecContext(c, `INSERT INTO public_share_media(share_id,media_id,bookmark_id,storage_key,mime_type,byte_size,sha256,width,height,ordinal,added_at)
		SELECT ?,m.id,m.bookmark_id,m.storage_key,m.mime_type,m.byte_size,m.sha256,m.width,m.height,m.ordinal,?
		FROM bookmark_media m JOIN public_share_items i ON i.share_id=? AND i.bookmark_id=m.bookmark_id AND i.evidence_id=m.evidence_id
		WHERE m.user_id=? AND m.is_staged=0 AND m.deleted_at IS NULL`, shareID, now, shareID, userID); err != nil {
		return err
	}
	for _, id := range artifacts {
		var kind string
		err := tx.QueryRowContext(c, `SELECT artifact_type FROM artifacts WHERE id=? AND user_id=? AND is_staged=0 AND deleted_at IS NULL AND artifact_type IN ('screenshot','pdf')`, id, userID).Scan(&kind)
		if err != nil {
			return errors.New("artifact not allowed")
		}
		res, err := tx.ExecContext(c, `INSERT INTO public_share_artifacts(share_id,artifact_id,bookmark_id,artifact_type,storage_key,mime_type,byte_size,added_at)
			SELECT ?,a.id,a.bookmark_id,a.artifact_type,a.storage_key,a.mime_type,a.byte_size,? FROM artifacts a
			WHERE a.id=? AND a.user_id=? AND a.is_staged=0 AND a.deleted_at IS NULL AND a.artifact_type=?
			AND EXISTS(SELECT 1 FROM public_share_items WHERE share_id=? AND bookmark_id=a.bookmark_id)`, shareID, now, id, userID, kind, shareID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return errors.New("artifact item not shared")
		}
	}
	return nil
}

func (s *Service) UpdateShare(w http.ResponseWriter, r *http.Request, user auth.User) {
	var in shareInput
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Title) == "" || !validExpiry(in.ExpiresAt) {
		writeError(w, 400, "Title is required")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "Could not update share")
		return
	}
	res, err := tx.ExecContext(r.Context(), `UPDATE public_shares SET title=?,description=?,expires_at=?,indexable=?,updated_at=? WHERE id=? AND user_id=?`, strings.TrimSpace(in.Title), strings.TrimSpace(in.Description), nullableTime(in.ExpiresAt), in.Indexable, now, r.PathValue("id"), user.ID)
	if err == nil {
		n, _ := res.RowsAffected()
		if n != 1 {
			err = sql.ErrNoRows
		}
	}
	if err == nil {
		err = replaceShareMembers(r.Context(), tx, r.PathValue("id"), user.ID, in.ItemIDs, in.ArtifactIDs, now)
	}
	if err != nil {
		_ = tx.Rollback()
		writeError(w, 404, "Share or membership not found")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, "Could not update share")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Service) RevokeShare(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.shareMutation(w, r, user, `UPDATE public_shares SET revoked_at=?,updated_at=? WHERE id=? AND user_id=?`, true)
}
func (s *Service) DeleteShare(w http.ResponseWriter, r *http.Request, user auth.User) {
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM public_shares WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if err != nil {
		writeError(w, 500, "Could not delete share")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, 404, "Share not found")
		return
	}
	w.WriteHeader(204)
}
func (s *Service) shareMutation(w http.ResponseWriter, r *http.Request, user auth.User, q string, _ bool) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(r.Context(), q, now, now, r.PathValue("id"), user.ID)
	if err != nil {
		writeError(w, 500, "Could not revoke share")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, 404, "Share not found")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type publicShare struct {
	ID, Title, Description, Expires string
	Indexable                       bool
}

func (s *Service) validShare(r *http.Request) (publicShare, error) {
	var x publicShare
	err := s.db.QueryRowContext(r.Context(), `SELECT id,title,description,COALESCE(expires_at,''),indexable FROM public_shares WHERE token_digest=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)`, tokenDigest(r.PathValue("token")), time.Now().UTC().Format(time.RFC3339)).Scan(&x.ID, &x.Title, &x.Description, &x.Expires, &x.Indexable)
	return x, err
}
func (s *Service) PublicShareJSON(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate(r) {
		writeError(w, 429, "Too many requests")
		return
	}
	sh, err := s.validShare(r)
	if err != nil {
		writeError(w, 404, "Share not found")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT bookmark_id,public_title,public_url,public_description,public_domain,public_reader_html,public_text,public_published_at FROM public_share_items WHERE share_id=? ORDER BY added_at`, sh.ID)
	if err != nil {
		writeError(w, 500, "Could not load share")
		return
	}
	type shareItem struct {
		id, title, url, desc, domain, reader, text, created string
	}
	materialized := []shareItem{}
	for rows.Next() {
		var item shareItem
		if err := rows.Scan(&item.id, &item.title, &item.url, &item.desc, &item.domain, &item.reader, &item.text, &item.created); err != nil {
			_ = rows.Close()
			writeError(w, 500, "Could not load share")
			return
		}
		materialized = append(materialized, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		writeError(w, 500, "Could not load share")
		return
	}
	// Close the item cursor before artifact lookups. SQLite deployments commonly
	// use a single connection, where querying while rows are open deadlocks.
	_ = rows.Close()
	artifactsByBookmark := make(map[string][]map[string]any)
	artifactRows, err := s.db.QueryContext(r.Context(), `SELECT bookmark_id,artifact_id,artifact_type,mime_type,byte_size
		FROM public_share_artifacts
		WHERE share_id=?
		ORDER BY added_at`, sh.ID)
	if err != nil {
		writeError(w, 500, "Could not load share")
		return
	}
	for artifactRows.Next() {
		var bookmarkID, aid, kind, mime string
		var size int64
		if err := artifactRows.Scan(&bookmarkID, &aid, &kind, &mime, &size); err != nil {
			_ = artifactRows.Close()
			writeError(w, 500, "Could not load share")
			return
		}
		artifactsByBookmark[bookmarkID] = append(artifactsByBookmark[bookmarkID], map[string]any{
			"id": aid, "type": kind, "mime_type": mime, "byte_size": size,
			"url": "/s/" + r.PathValue("token") + "/artifacts/" + aid,
		})
	}
	if err := artifactRows.Err(); err != nil {
		_ = artifactRows.Close()
		writeError(w, 500, "Could not load share")
		return
	}
	_ = artifactRows.Close()
	items := []map[string]any{}
	for _, item := range materialized {
		arts := artifactsByBookmark[item.id]
		if arts == nil {
			arts = []map[string]any{}
		}
		reader := strings.ReplaceAll(item.reader, "/api/media/", "/s/"+r.PathValue("token")+"/media/")
		items = append(items, map[string]any{"id": item.id, "title": item.title, "url": item.url, "description": item.desc, "domain": item.domain, "reader_html": reader, "text": item.text, "created_at": item.created, "artifacts": arts})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"title": sh.Title, "description": sh.Description, "indexable": sh.Indexable, "items": items})
}
func (s *Service) publicRate(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	key := "rl:share:" + tokenDigest(host)
	now := time.Now().UTC()
	lastCleanup := publicRateCleanupUnix.Load()
	if now.Unix()-lastCleanup >= 60 && publicRateCleanupUnix.CompareAndSwap(lastCleanup, now.Unix()) {
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM rate_limits WHERE key IN (SELECT key FROM rate_limits WHERE expires_at<=? LIMIT 500)`, now.Format(time.RFC3339))
	}
	var count int
	err := s.db.QueryRowContext(r.Context(), `INSERT INTO rate_limits(key,window_start,count,expires_at) VALUES(?,?,1,?)
		ON CONFLICT(key) DO UPDATE SET count=CASE WHEN expires_at<=? THEN 1 ELSE count+1 END,
		window_start=CASE WHEN expires_at<=? THEN excluded.window_start ELSE window_start END,
		expires_at=CASE WHEN expires_at<=? THEN excluded.expires_at ELSE expires_at END RETURNING count`,
		key, now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339)).Scan(&count)
	return err == nil && count <= 120
}
func (s *Service) PublicArtifact(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate(r) {
		writeError(w, 429, "Too many requests")
		return
	}
	sh, err := s.validShare(r)
	if err != nil {
		writeError(w, 404, "Share not found")
		return
	}
	var key, mime, kind string
	var size int64
	err = s.db.QueryRowContext(r.Context(), `SELECT storage_key,mime_type,artifact_type,byte_size FROM public_share_artifacts WHERE share_id=? AND artifact_id=? AND artifact_type IN ('screenshot','pdf')`, sh.ID, r.PathValue("artifact")).Scan(&key, &mime, &kind, &size)
	if err != nil || s.assets == nil {
		writeError(w, 404, "Artifact not found")
		return
	}
	f, err := s.assets.Open(key)
	if err != nil {
		writeError(w, 404, "Artifact not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", `inline; filename="`+kind+`"`)
	w.Header().Set("Content-Length", fmt.Sprint(size))
	_, _ = io.Copy(w, f)
}

func (s *Service) PublicMedia(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate(r) {
		writeError(w, http.StatusTooManyRequests, "Too many requests")
		return
	}
	share, err := s.validShare(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "Share not found")
		return
	}
	var key, mime string
	var size int64
	err = s.db.QueryRowContext(r.Context(), `SELECT storage_key,mime_type,byte_size FROM public_share_media WHERE share_id=? AND media_id=?`, share.ID, r.PathValue("media")).Scan(&key, &mime, &size)
	if err != nil || s.assets == nil {
		writeError(w, http.StatusNotFound, "Media not found")
		return
	}
	s.writeMediaContent(w, key, mime, size, "no-store")
}
func (s *Service) PublicRSS(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate(r) {
		writeError(w, 429, "Too many requests")
		return
	}
	sh, err := s.validShare(r)
	if err != nil {
		writeError(w, 404, "Share not found")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT public_title,public_url,public_description,public_published_at FROM public_share_items WHERE share_id=? ORDER BY added_at DESC LIMIT 100`, sh.ID)
	if err != nil {
		writeError(w, 500, "Could not load share")
		return
	}
	defer rows.Close()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>` + html.EscapeString(sh.Title) + `</title><description>` + html.EscapeString(sh.Description) + `</description>`)
	for rows.Next() {
		var title, url, desc, created string
		_ = rows.Scan(&title, &url, &desc, &created)
		b.WriteString(`<item><title>` + html.EscapeString(title) + `</title><link>` + html.EscapeString(url) + `</link><guid isPermaLink="false">` + html.EscapeString(url) + `</guid><description>` + html.EscapeString(desc) + `</description>`)
		if publishedAt, ok := rssDate(created); ok {
			b.WriteString(`<pubDate>` + publishedAt + `</pubDate>`)
		}
		b.WriteString(`</item>`)
	}
	b.WriteString(`</channel></rss>`)
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

func rssDate(value string) (string, bool) {
	publishedAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", false
	}
	return html.EscapeString(publishedAt.UTC().Format(time.RFC1123Z)), true
}

func (s *Service) PublicSharePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	share, err := s.validShare(r)
	if err != nil || !share.Indexable {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
	_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset=utf-8><meta name=viewport content="width=device-width"><title>Shared knowledge</title><link rel="stylesheet" href="/public-share.css"></head><body><header><h1 id=t>Shared knowledge</h1><p id=d></p><div class=controls><label for=q>Search shared knowledge</label><input id=q type=search><label for=sort>Sort items</label><select id=sort><option value=new>Newest</option><option value=old>Oldest</option><option value=title>Title</option></select></div><p id=result-count class=result-count aria-live=polite></p></header><main id=list></main><script src="/public-share.js" defer></script></body></html>`)
}
