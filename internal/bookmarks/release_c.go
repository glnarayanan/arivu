package bookmarks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

const aiTaggingKey = "ai_tagging_mode"

func (s *Service) aiTaggingMode(ctx context.Context, userID string) string {
	var mode string
	_ = s.db.QueryRowContext(ctx, `SELECT value FROM user_settings WHERE user_id=? AND key=?`, userID, aiTaggingKey).Scan(&mode)
	if mode != "existing-vocabulary" && mode != "allow-new" {
		return "off"
	}
	return mode
}

type feedItem struct {
	Title, Link, GUID, Content string
	Published                  time.Time
}
type feedXML struct {
	Channel struct {
		Items []struct {
			Title, Link, GUID, Description string
			PubDate                        string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
	Entries []struct {
		Title string `xml:"title"`
		ID    string `xml:"id"`
		Links []struct {
			Href, Rel string `xml:"href,attr"`
		} `xml:"link"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
		Updated   string `xml:"updated"`
		Published string `xml:"published"`
	} `xml:"entry"`
}

func parseFeed(raw []byte) ([]feedItem, error) {
	var doc feedXML
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	dec.Strict = true
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid feed XML: %w", err)
	}
	out := make([]feedItem, 0, len(doc.Channel.Items)+len(doc.Entries))
	for _, x := range doc.Channel.Items {
		out = append(out, feedItem{Title: strings.TrimSpace(x.Title), Link: strings.TrimSpace(x.Link), GUID: strings.TrimSpace(x.GUID), Content: x.Description, Published: feedTime(x.PubDate)})
	}
	for _, x := range doc.Entries {
		link := ""
		for _, l := range x.Links {
			if link == "" || l.Rel == "alternate" {
				link = l.Href
			}
		}
		out = append(out, feedItem{Title: strings.TrimSpace(x.Title), Link: strings.TrimSpace(link), GUID: strings.TrimSpace(x.ID), Content: x.Summary + x.Content, Published: feedTime(fallback(x.Published, x.Updated))})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("feed has no entries")
	}
	return out, nil
}
func feedTime(v string) time.Time {
	for _, f := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, e := time.Parse(f, strings.TrimSpace(v)); e == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
func canonicalFeedURL(raw string) string {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil {
		return ""
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ScheduleDueFeeds durably claims due subscriptions. A unique active job is
// achieved by moving next_poll_at before enqueueing; crashes merely delay one interval.
func (s *Service) ScheduleDueFeeds(ctx context.Context) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id FROM feed_subscriptions WHERE enabled=1 AND next_poll_at<=? LIMIT 50`, now.Format(time.RFC3339))
	if err != nil {
		return
	}
	defer rows.Close()
	type due struct{ id, user string }
	var all []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.user); err != nil {
			return
		}
		all = append(all, d)
	}
	if err := rows.Err(); err != nil {
		return
	}
	if err := rows.Close(); err != nil {
		return
	}
	for _, d := range all {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return
		}
		next := now.Add(time.Hour).Format(time.RFC3339)
		res, err := tx.ExecContext(ctx, `UPDATE feed_subscriptions SET next_poll_at=?,status='queued' WHERE id=? AND next_poll_at<=?`, next, d.id, now.Format(time.RFC3339))
		if err != nil {
			_ = tx.Rollback()
			continue
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			_ = tx.Rollback()
			continue
		}
		p, err := json.Marshal(map[string]string{"subscription_id": d.id})
		if err == nil {
			_, err = s.jobs.EnqueueWithIDTx(ctx, tx, d.user, "feed.poll", string(p))
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}
}

func (s *Service) pollFeed(ctx context.Context, id string) error {
	var userID, rawURL, etag, modified, collection, tagsJSON string
	var failures int
	err := s.db.QueryRowContext(ctx, `SELECT user_id,url,etag,last_modified,COALESCE(collection_id,''),tags_json,failure_count FROM feed_subscriptions WHERE id=? AND enabled=1`, id).Scan(&userID, &rawURL, &etag, &modified, &collection, &tagsJSON, &failures)
	if err != nil {
		return err
	}
	r, err := s.fetcher.FetchRaw(ctx, rawURL, etag, modified)
	now := time.Now().UTC()
	if err != nil {
		failures++
		delay := time.Hour * time.Duration(1<<min(failures, 6))
		_, _ = s.db.ExecContext(ctx, `UPDATE feed_subscriptions SET status='error',last_error=?,failure_count=?,last_poll_at=?,next_poll_at=?,updated_at=? WHERE id=?`, err.Error(), failures, nowString(), now.Add(delay).Format(time.RFC3339), nowString(), id)
		return nil
	}
	if r.Status == http.StatusNotModified {
		_, _ = s.db.ExecContext(ctx, `UPDATE feed_subscriptions SET status='ok',last_error='',failure_count=0,last_poll_at=?,next_poll_at=?,updated_at=? WHERE id=?`, nowString(), now.Add(time.Hour).Format(time.RFC3339), nowString(), id)
		return nil
	}
	items, err := parseFeed(r.Body)
	if err != nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE feed_subscriptions SET status='error',last_error=?,failure_count=failure_count+1,last_poll_at=?,next_poll_at=? WHERE id=?`, err.Error(), nowString(), now.Add(2*time.Hour).Format(time.RFC3339), id)
		return nil
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Published.After(items[j].Published) })
	if len(items) > 50 {
		items = items[:50]
	}
	for _, item := range items {
		if err := s.acceptFeedItem(ctx, id, userID, collection, tagsJSON, item); err != nil {
			failures++
			delay := time.Hour * time.Duration(1<<min(failures, 6))
			if _, updateErr := s.db.ExecContext(ctx, `UPDATE feed_subscriptions SET status='error',last_error=?,failure_count=?,last_poll_at=?,next_poll_at=?,updated_at=? WHERE id=?`, "entry persistence failed", failures, nowString(), now.Add(delay).Format(time.RFC3339), nowString(), id); updateErr != nil {
				return updateErr
			}
			return err
		}
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE feed_subscriptions SET status='ok',last_error='',failure_count=0,etag=?,last_modified=?,last_poll_at=?,next_poll_at=?,updated_at=? WHERE id=?`, r.ETag, r.LastModified, nowString(), now.Add(time.Hour).Format(time.RFC3339), nowString(), id)
	return nil
}
func (s *Service) acceptFeedItem(ctx context.Context, subscription, userID, collection, tagsJSON string, item feedItem) error {
	canonical := canonicalFeedURL(item.Link)
	if canonical == "" || safefetch.ValidateURL(canonical) != nil {
		return nil
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{strings.TrimSpace(item.Title), canonical, strings.TrimSpace(item.Content), item.Published.Format(time.RFC3339)}, "\x00")))
	fingerprint := fmt.Sprintf("%x", sum[:])
	key := item.GUID
	if key == "" {
		key = canonical
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM feed_entries WHERE subscription_id=? AND (entry_key=? OR fingerprint=?)`, subscription, key, fingerprint).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks WHERE user_id=? AND url=?`, userID, canonical).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO feed_entries(subscription_id,entry_key,fingerprint,created_at) VALUES(?,?,?,?)`, subscription, key, fingerprint, nowString())
		if err == nil {
			err = tx.Commit()
		}
		return err
	}
	bid := ids.New()
	host, _ := url.Parse(canonical)
	attempt := ids.New()
	now := nowString()
	_, err = tx.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,domain,source,created_at,updated_at) VALUES(?,?,?,?,?,'rss',?,?)`, bid, userID, canonical, fallback(item.Title, host.Hostname()), host.Hostname(), now, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO feed_entries(subscription_id,entry_key,bookmark_id,fingerprint,created_at) VALUES(?,?,?,?,?)`, subscription, key, bid, fingerprint, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,engine,engine_version,queued_at) VALUES(?,?,?,'queued',?,'direct_http',?,?)`, attempt, bid, userID, canonical, safefetch.ExtractorVersion, now)
	if err != nil {
		return err
	}
	if collection != "" {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO collection_bookmarks(collection_id,bookmark_id,user_id,added_at) VALUES(?,?,?,?)`, collection, bid, userID, now)
	}
	var tags []string
	_ = json.Unmarshal([]byte(tagsJSON), &tags)
	for _, name := range tags {
		slug := tagSlug(name)
		var tid string
		_ = tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id=? AND slug=?`, userID, slug).Scan(&tid)
		if tid != "" {
			_, _ = tx.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_tags(bookmark_id,tag_id,user_id,source,created_at) VALUES(?,?,?,'manual',?)`, bid, tid, userID, now)
		}
	}
	if err == nil {
		_, err = s.jobs.EnqueueWithIDTx(ctx, tx, userID, "bookmark.process", bookmarkProcessPayload(bid, canonical, "", attempt))
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}

func (s *Service) GetAITaggingSetting(w http.ResponseWriter, r *http.Request, user auth.User) {
	writeJSON(w, 200, map[string]string{"mode": s.aiTaggingMode(r.Context(), user.ID)})
}
func (s *Service) SetAITaggingSetting(w http.ResponseWriter, r *http.Request, user auth.User) {
	var b struct {
		Mode string `json:"mode"`
	}
	if decodeJSON(r, &b) != nil || (b.Mode != "off" && b.Mode != "existing-vocabulary" && b.Mode != "allow-new") {
		writeError(w, 400, "Invalid AI tagging mode")
		return
	}
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO user_settings(user_id,key,value,updated_at) VALUES(?,?,?,?) ON CONFLICT(user_id,key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, user.ID, aiTaggingKey, b.Mode, nowString())
	if err != nil {
		writeError(w, 500, "Could not save setting")
		return
	}
	writeJSON(w, 200, map[string]string{"mode": b.Mode})
}

func (s *Service) allowedAITags(ctx context.Context, userID string, suggestions []string) []string {
	mode := s.aiTaggingMode(ctx, userID)
	if mode == "off" {
		return nil
	}
	result := []string{}
	seen := map[string]bool{}
	for _, raw := range suggestions {
		slug := tagSlug(raw)
		if slug == "" || seen[slug] {
			continue
		}
		var canonical string
		err := s.db.QueryRowContext(ctx, `SELECT t.name FROM tags t LEFT JOIN tag_aliases a ON a.tag_id=t.id AND a.user_id=t.user_id WHERE t.user_id=? AND (t.slug=? OR a.alias_slug=?) LIMIT 1`, userID, slug, slug).Scan(&canonical)
		if err == sql.ErrNoRows && mode == "allow-new" {
			canonical = strings.TrimSpace(raw)
		}
		if canonical != "" {
			result = append(result, canonical)
			seen[slug] = true
		}
		if len(result) == 8 {
			break
		}
	}
	return result
}

func (s *Service) Subscriptions(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,url,name,COALESCE(collection_id,''),tags_json,enabled,status,last_error,etag,last_modified,COALESCE(last_poll_at,''),next_poll_at FROM feed_subscriptions WHERE user_id=? ORDER BY name,url`, user.ID)
	if err != nil {
		writeError(w, 500, "Could not load subscriptions")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, u, n, c, t, st, e, et, lm, lp, np string
		var enabled bool
		_ = rows.Scan(&id, &u, &n, &c, &t, &enabled, &st, &e, &et, &lm, &lp, &np)
		var tags []string
		_ = json.Unmarshal([]byte(t), &tags)
		out = append(out, map[string]any{"id": id, "url": u, "name": n, "collection_id": c, "tags": tags, "enabled": enabled, "status": st, "error": e, "etag": et, "last_modified": lm, "last_poll_at": lp, "next_poll_at": np})
	}
	writeJSON(w, 200, out)
}
func (s *Service) CreateSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	var b struct {
		URL          string   `json:"url"`
		Name         string   `json:"name"`
		CollectionID string   `json:"collection_id"`
		Tags         []string `json:"tags"`
	}
	if decodeJSON(r, &b) != nil || safefetch.ValidateURL(b.URL) != nil {
		writeError(w, 400, "A safe HTTP or HTTPS feed URL is required")
		return
	}
	if b.CollectionID != "" && !s.ownsCollection(r.Context(), user.ID, b.CollectionID) {
		writeError(w, 404, "Collection not found")
		return
	}
	tags, _ := json.Marshal(cleanStringList(b.Tags, 20))
	id := ids.New()
	now := nowString()
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO feed_subscriptions(id,user_id,url,name,collection_id,tags_json,next_poll_at,created_at,updated_at) VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?)`, id, user.ID, b.URL, strings.TrimSpace(b.Name), b.CollectionID, string(tags), now, now, now)
	if err != nil {
		writeError(w, 409, "Subscription already exists")
		return
	}
	writeJSON(w, 201, map[string]string{"id": id})
}
func (s *Service) UpdateSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	var b struct {
		Enabled *bool   `json:"enabled"`
		Name    *string `json:"name"`
	}
	if decodeJSON(r, &b) != nil {
		writeError(w, 400, "Invalid subscription")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE feed_subscriptions SET enabled=COALESCE(?,enabled),name=COALESCE(?,name),next_poll_at=CASE WHEN COALESCE(?,enabled) THEN next_poll_at ELSE next_poll_at END,updated_at=? WHERE id=? AND user_id=?`, b.Enabled, b.Name, b.Enabled, nowString(), r.PathValue("id"), user.ID)
	if err != nil {
		writeError(w, 500, "Could not update subscription")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, 404, "Subscription not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) DeleteSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	res, _ := s.db.ExecContext(r.Context(), `DELETE FROM feed_subscriptions WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, 404, "Subscription not found")
		return
	}
	w.WriteHeader(204)
}
