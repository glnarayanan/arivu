package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
	"github.com/glnarayanan/arivu/internal/sanitize"
)

type Service struct {
	db      *sql.DB
	jobs    *jobs.Queue
	fetcher *safefetch.Client
	gemini  func(context.Context) providers.GeminiClient
}

type CountsResult struct {
	Users       int
	Bookmarks   int
	Collections int
	Summaries   int
}

func New(db *sql.DB, jobs *jobs.Queue, fetcher *safefetch.Client, gemini providers.GeminiClient) *Service {
	return &Service{db: db, jobs: jobs, fetcher: fetcher, gemini: func(context.Context) providers.GeminiClient { return gemini }}
}

func (s *Service) SetGeminiProvider(fn func(context.Context) providers.GeminiClient) {
	if fn != nil {
		s.gemini = fn
	}
}

func (s *Service) geminiClient(ctx context.Context) providers.GeminiClient {
	if s.gemini == nil {
		return providers.GeminiClient{}
	}
	return s.gemini(ctx)
}

func (s *Service) Create(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		URL          string   `json:"url"`
		Title        string   `json:"title"`
		CollectionID string   `json:"collection_id"`
		Note         string   `json:"note"`
		Quote        string   `json:"quote"`
		Annotation   string   `json:"annotation"`
		Tags         []string `json:"tags"`
	}
	if err := decodeJSON(r, &body); err != nil || strings.TrimSpace(body.URL) == "" {
		writeError(w, http.StatusBadRequest, "URL is required")
		return
	}
	if err := safefetch.ValidateURL(body.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parsed, _ := url.Parse(body.URL)
	if body.CollectionID != "" && !s.ownsCollection(r.Context(), user.ID, body.CollectionID) {
		writeError(w, http.StatusNotFound, "Collection not found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	bookmarkID := ids.New()
	title := fallback(strings.TrimSpace(body.Title), parsed.Hostname())
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, bookmarkID, user.ID, body.URL, title, parsed.Hostname(), now, now)
	if err != nil {
		writeError(w, http.StatusConflict, "Bookmark already exists")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, ids.New(), bookmarkID, user.ID, "pending", now, now)
	_ = s.upsertItemState(r.Context(), user.ID, "bookmark", bookmarkID, "inbox", 0, strings.TrimSpace(body.Note), now)
	if body.CollectionID != "" {
		_, _ = s.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO collection_bookmarks(collection_id,bookmark_id,user_id,added_at) VALUES(?,?,?,?)`, body.CollectionID, bookmarkID, user.ID, now)
	}
	for _, tag := range cleanStringList(body.Tags, 20) {
		_ = s.attachTag(r.Context(), user.ID, bookmarkID, tag, "manual")
	}
	quote := fallback(body.Quote, body.Annotation)
	if strings.TrimSpace(body.Note) != "" || strings.TrimSpace(quote) != "" {
		selector, _ := jsonObject(nil)
		tagJSON, _ := json.Marshal(cleanStringList(body.Tags, 20))
		_, _ = s.db.ExecContext(r.Context(), `INSERT INTO annotations(id,user_id,bookmark_id,quote,note,selector_json,tags_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, ids.New(), user.ID, bookmarkID, strings.TrimSpace(quote), strings.TrimSpace(body.Note), selector, string(tagJSON), now, now)
	}
	payload, _ := json.Marshal(map[string]string{"bookmark_id": bookmarkID, "url": body.URL})
	jobID, _ := s.jobs.EnqueueWithID(r.Context(), user.ID, "bookmark.process", string(payload))
	bm, _ := s.getBookmark(r.Context(), user.ID, bookmarkID)
	writeJSON(w, http.StatusOK, map[string]any{"bookmark": bm, "job_id": jobID, "connections": []any{}, "connections_count": 0})
}

func (s *Service) Preview(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	result, err := s.fetcher.Fetch(r.Context(), body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":          result.URL,
		"title":        fallback(result.Title, result.Domain),
		"description":  result.Description,
		"domain":       result.Domain,
		"favicon":      nil,
		"thumbnail":    nil,
		"reading_time": readingTime(result.Text),
	})
}

func (s *Service) List(w http.ResponseWriter, r *http.Request, user auth.User) {
	where, args := s.bookmarkFilter(r, user.ID)
	rows, err := s.db.QueryContext(r.Context(), `SELECT b.id,b.url,b.title,b.description,b.domain,b.favicon,b.thumbnail,b.reading_time,b.read_status,b.source,b.created_at,b.updated_at,b.last_accessed,b.view_count,b.version,b.sanitized_html,b.text_content FROM bookmarks b `+where+` ORDER BY b.created_at DESC LIMIT 200`, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list bookmarks")
		return
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		result = append(result, scanBookmark(rows))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) bookmarkFilter(r *http.Request, userID string) (string, []any) {
	query := r.URL.Query()
	search := strings.TrimSpace(query.Get("search"))
	args := []any{userID}
	where := "WHERE b.user_id=?"
	if domain := strings.TrimSpace(query.Get("domain")); domain != "" {
		where += " AND b.domain=?"
		args = append(args, domain)
	}
	if source := strings.TrimSpace(query.Get("source")); source != "" {
		where += " AND b.source=?"
		args = append(args, source)
	}
	if status := query.Get("read_status"); status == "read" || status == "unread" {
		where += " AND b.read_status=?"
		args = append(args, status == "read")
	}
	if from := strings.TrimSpace(query.Get("date_from")); from != "" {
		where += " AND b.created_at>=?"
		args = append(args, from)
	}
	if to := strings.TrimSpace(query.Get("date_to")); to != "" {
		where += " AND b.created_at<=?"
		args = append(args, to)
	}
	if tag := tagSlug(query.Get("tag")); tag != "" {
		where += ` AND EXISTS (
			SELECT 1 FROM bookmark_tags bt
			JOIN tags t ON t.id=bt.tag_id AND t.user_id=bt.user_id
			LEFT JOIN tag_aliases ta ON ta.tag_id=t.id AND ta.user_id=t.user_id
			WHERE bt.bookmark_id=b.id AND bt.user_id=b.user_id AND (t.slug=? OR ta.alias_slug=?)
		)`
		args = append(args, tag, tag)
	}
	if search != "" {
		where += ` AND (
			b.title LIKE ? OR b.description LIKE ? OR b.text_content LIKE ?
			OR EXISTS (SELECT 1 FROM annotations a WHERE a.bookmark_id=b.id AND a.user_id=b.user_id AND (a.quote LIKE ? OR a.note LIKE ?))
			OR EXISTS (
				SELECT 1 FROM bookmark_notes bn
				JOIN notes n ON n.id=bn.note_id AND n.user_id=bn.user_id
				WHERE bn.bookmark_id=b.id AND bn.user_id=b.user_id AND (n.title LIKE ? OR n.body LIKE ?)
			)
		)`
		like := "%" + search + "%"
		args = append(args, like, like, like, like, like, like, like)
	}
	return where, args
}

func (s *Service) Get(w http.ResponseWriter, r *http.Request, user auth.User) {
	bm, err := s.getBookmark(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Bookmark not found")
		return
	}
	summary := s.summary(r.Context(), user.ID, r.PathValue("id"))
	bm["ai_summary"] = summary
	bm["tags"] = s.bookmarkTags(r.Context(), user.ID, r.PathValue("id"))
	bm["annotations"] = s.bookmarkAnnotations(r.Context(), user.ID, r.PathValue("id"))
	bm["notes"] = s.bookmarkNotes(r.Context(), user.ID, r.PathValue("id"))
	bm["item_state"] = s.itemState(r.Context(), user.ID, "bookmark", r.PathValue("id"))
	bm["links"] = s.itemLinks(r.Context(), user.ID, "bookmark", r.PathValue("id"))
	bm["reminders"] = s.itemReminders(r.Context(), user.ID, "bookmark", r.PathValue("id"))
	writeJSON(w, http.StatusOK, bm)
}

func (s *Service) Delete(w http.ResponseWriter, r *http.Request, user auth.User) {
	res, _ := s.db.ExecContext(r.Context(), `DELETE FROM bookmarks WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "Bookmark not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Bookmark deleted"})
}

func (s *Service) ReadStatus(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		ReadStatus bool `json:"read_status"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE bookmarks SET read_status=?, updated_at=? WHERE id=? AND user_id=?`, body.ReadStatus, time.Now().UTC().Format(time.RFC3339), r.PathValue("id"), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"read_status": body.ReadStatus})
}

func (s *Service) Accessed(w http.ResponseWriter, r *http.Request, user auth.User) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.ExecContext(r.Context(), `UPDATE bookmarks SET last_accessed=?, view_count=view_count+1 WHERE id=? AND user_id=?`, now, r.PathValue("id"), user.ID)
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO bookmark_accesses(id,bookmark_id,user_id,accessed_at,context) VALUES(?,?,?,?,?)`, ids.New(), r.PathValue("id"), user.ID, now, "web")
	writeJSON(w, http.StatusOK, map[string]any{"last_accessed": now})
}

func (s *Service) Search(w http.ResponseWriter, r *http.Request, user auth.User) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("query"))
	}
	if len(q) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}
	req := r.Clone(r.Context())
	values := req.URL.Query()
	values.Set("search", q)
	req.URL.RawQuery = values.Encode()
	s.List(w, req, user)
}

func (s *Service) SearchAnswer(w http.ResponseWriter, r *http.Request, user auth.User) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("query"))
	}
	if len(q) < 2 || len(q) > maxSearchLen {
		writeError(w, http.StatusBadRequest, "query must be between 2 and 2000 characters")
		return
	}
	req := r.Clone(r.Context())
	values := req.URL.Query()
	values.Set("search", q)
	req.URL.RawQuery = values.Encode()
	where, args := s.bookmarkFilter(req, user.ID)
	rows, err := s.db.QueryContext(r.Context(), `SELECT b.id,b.title,b.url,b.domain,b.description,b.text_content FROM bookmarks b `+where+` ORDER BY b.updated_at DESC, b.id ASC LIMIT 8`, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not answer search")
		return
	}
	defer rows.Close()
	type citation struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Title   string `json:"title"`
		URL     string `json:"url"`
		Domain  string `json:"domain"`
		Snippet string `json:"snippet"`
	}
	var citations []citation
	for rows.Next() {
		var id, title, rawURL, domain, description, text string
		_ = rows.Scan(&id, &title, &rawURL, &domain, &description, &text)
		snippet := searchSnippet(q, firstNonEmpty(text, description, title))
		citations = append(citations, citation{ID: id, Type: "bookmark", Title: fallback(title, rawURL), URL: rawURL, Domain: domain, Snippet: snippet})
	}
	if standaloneNotesMatchFilters(values) {
		like := "%" + q + "%"
		noteWhere := `WHERE user_id=? AND NOT EXISTS (SELECT 1 FROM bookmark_notes WHERE note_id=notes.id AND user_id=notes.user_id) AND (title LIKE ? OR body LIKE ?)`
		noteArgs := []any{user.ID, like, like}
		if from := strings.TrimSpace(values.Get("date_from")); from != "" {
			noteWhere += " AND created_at>=?"
			noteArgs = append(noteArgs, from)
		}
		if to := strings.TrimSpace(values.Get("date_to")); to != "" {
			noteWhere += " AND created_at<=?"
			noteArgs = append(noteArgs, to)
		}
		noteRows, err := s.db.QueryContext(r.Context(), `SELECT id,title,body,source FROM notes `+noteWhere+` ORDER BY updated_at DESC, id ASC LIMIT 8`, noteArgs...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not answer search")
			return
		}
		defer noteRows.Close()
		for noteRows.Next() {
			if len(citations) >= 8 {
				break
			}
			var id, title, body, source string
			_ = noteRows.Scan(&id, &title, &body, &source)
			citations = append(citations, citation{ID: id, Type: "note", Title: fallback(title, "Untitled note"), Domain: source, Snippet: searchSnippet(q, firstNonEmpty(body, title))})
		}
	}
	if len(citations) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"answer": "No saved items matched this query.", "citations": []any{}})
		return
	}
	seenAnswerParts := map[string]struct{}{}
	var answerParts []string
	for _, citation := range citations {
		if len(answerParts) >= 8 {
			break
		}
		if citation.Type == "bookmark" {
			summary := s.summary(r.Context(), user.ID, citation.ID)
			appendAnswerPart(&answerParts, seenAnswerParts, summary["one_sentence"], 8)
			appendAnswerPart(&answerParts, seenAnswerParts, summary["highlights"], 8)
			appendAnswerPart(&answerParts, seenAnswerParts, summary["bullet_points"], 8)
		}
		appendAnswerPart(&answerParts, seenAnswerParts, citation.Snippet, 8)
	}
	answer := "Found " + fmt.Sprint(len(citations)) + " saved item"
	if len(citations) != 1 {
		answer += "s"
	}
	if len(answerParts) > 0 {
		answer = "Answer from your saved context: " + strings.Join(answerParts, " ")
	} else {
		answer += " that mention this query. Use the citations below to inspect the original saved context."
	}
	writeJSON(w, http.StatusOK, map[string]any{"answer": answer, "citations": citations})
}

func standaloneNotesMatchFilters(values url.Values) bool {
	return strings.TrimSpace(values.Get("tag")) == "" &&
		strings.TrimSpace(values.Get("domain")) == "" &&
		strings.TrimSpace(values.Get("source")) == "" &&
		strings.TrimSpace(values.Get("read_status")) == ""
}

func (s *Service) Related(w http.ResponseWriter, r *http.Request, user auth.User) {
	limit := queryInt(r, "limit", 5, 1, 50)
	source, err := s.graphBookmark(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Bookmark not found")
		return
	}
	if len(source.Embedding) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"related": []any{}, "message": "Semantic analysis not yet available for this bookmark"})
		return
	}
	bookmarks, err := s.graphBookmarks(r.Context(), user.ID, 500, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load related bookmarks")
		return
	}
	related := []map[string]any{}
	for _, bookmark := range bookmarks {
		if bookmark.ID == source.ID || len(bookmark.Embedding) == 0 {
			continue
		}
		similarity := cosineSimilarity(source.Embedding, bookmark.Embedding)
		if similarity < 0.3 {
			continue
		}
		payload := cloneMap(bookmark.Data)
		payload["entities"] = bookmark.Entities
		payload["concepts"] = bookmark.Concepts
		payload["similarity_score"] = roundFloat(similarity, 4)
		related = append(related, payload)
	}
	sort.SliceStable(related, func(i, j int) bool {
		return numberValue(related[i]["similarity_score"]) > numberValue(related[j]["similarity_score"])
	})
	if len(related) > limit {
		related = related[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"related": related})
}

func (s *Service) Collections(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,description,color,created_at,updated_at FROM collections WHERE user_id=? ORDER BY created_at DESC`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load collections")
		return
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		var id, name, description, color, created, updated sql.NullString
		_ = rows.Scan(&id, &name, &description, &color, &created, &updated)
		result = append(result, map[string]any{"id": id.String, "name": name.String, "description": description.String, "color": color.String, "created_at": created.String, "updated_at": updated.String})
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) CreateCollection(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Color       string `json:"color"`
	}
	if err := decodeJSON(r, &body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO collections(id,user_id,name,description,color,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, user.ID, strings.TrimSpace(body.Name), body.Description, body.Color, now, now)
	if err != nil {
		writeError(w, http.StatusConflict, "Collection already exists")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": body.Name, "description": body.Description, "color": body.Color, "created_at": now, "updated_at": now})
}

func (s *Service) AddToCollection(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		BookmarkID string `json:"bookmark_id"`
	}
	if err := decodeJSON(r, &body); err != nil || body.BookmarkID == "" {
		writeError(w, http.StatusBadRequest, "bookmark_id is required")
		return
	}
	if !s.ownsCollection(r.Context(), user.ID, r.PathValue("id")) || !s.ownsBookmark(r.Context(), user.ID, body.BookmarkID) {
		writeError(w, http.StatusNotFound, "Collection or bookmark not found")
		return
	}
	_, err := s.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO collection_bookmarks(collection_id,bookmark_id,user_id,added_at) VALUES(?,?,?,?)`, r.PathValue("id"), body.BookmarkID, user.ID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not add bookmark to collection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Bookmark added"})
}

func (s *Service) ownsCollection(ctx context.Context, userID, collectionID string) bool {
	var found string
	return s.db.QueryRowContext(ctx, `SELECT id FROM collections WHERE id=? AND user_id=?`, collectionID, userID).Scan(&found) == nil
}

func (s *Service) ownsBookmark(ctx context.Context, userID, bookmarkID string) bool {
	var found string
	return s.db.QueryRowContext(ctx, `SELECT id FROM bookmarks WHERE id=? AND user_id=?`, bookmarkID, userID).Scan(&found) == nil
}

func (s *Service) AnalyticsSummary(w http.ResponseWriter, r *http.Request, user auth.User) {
	var total, read, unread, collections int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(CASE WHEN read_status THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN read_status THEN 0 ELSE 1 END),0) FROM bookmarks WHERE user_id=?`, user.ID).Scan(&total, &read, &unread)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM collections WHERE user_id=?`, user.ID).Scan(&collections)
	writeJSON(w, http.StatusOK, map[string]any{"total_bookmarks": total, "total_collections": collections, "read_bookmarks": read, "unread_bookmarks": unread})
}

func (s *Service) AnalyticsTopics(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT domain,COUNT(*) AS c FROM bookmarks WHERE user_id=? AND domain<>'' GROUP BY domain ORDER BY c DESC LIMIT 20`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load topics")
		return
	}
	defer rows.Close()
	var topics []map[string]any
	for rows.Next() {
		var domain string
		var count int
		_ = rows.Scan(&domain, &count)
		topics = append(topics, map[string]any{"topic": domain, "count": count})
	}
	writeJSON(w, http.StatusOK, map[string]any{"topics": topics})
}

func (s *Service) AnalyticsPatterns(w http.ResponseWriter, r *http.Request, user auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{"patterns": []map[string]any{{"label": "Saved bookmarks", "value": count(r.Context(), s.db, "bookmarks", user.ID)}}})
}

func (s *Service) AnalyticsInsights(w http.ResponseWriter, r *http.Request, user auth.User) {
	insights := s.localInsights(r.Context(), user.ID)
	if generated, err := s.geminiClient(r.Context()).GenerateInsight(r.Context(), s.analyticsPrompt(r.Context(), user.ID)); err == nil && strings.TrimSpace(generated) != "" {
		insights = append(insights, map[string]any{"type": "ai", "message": strings.TrimSpace(generated), "severity": "info"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"insights": insights})
}

func (s *Service) localInsights(ctx context.Context, userID string) []map[string]any {
	var total, unread, savedRecent, readRecent, readingMinutes int
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN read_status=0 THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN created_at>=? THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN last_accessed>=? THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN last_accessed>=? THEN reading_time ELSE 0 END),0) FROM bookmarks WHERE user_id=?`, cutoff, cutoff, cutoff, userID).Scan(&total, &unread, &savedRecent, &readRecent, &readingMinutes)
	insights := []map[string]any{}
	completion := 0.0
	if savedRecent > 0 {
		completion = float64(readRecent) / float64(savedRecent) * 100
	}
	if savedRecent >= 5 && completion < 40 {
		insights = append(insights, map[string]any{"type": "completion", "message": fmt.Sprintf("You save more than you read: %.1f%% of recent bookmarks were revisited.", completion), "severity": "warning"})
	} else if savedRecent > 0 && completion >= 70 {
		insights = append(insights, map[string]any{"type": "completion", "message": fmt.Sprintf("Strong reading habit: %.1f%% of recent bookmarks were revisited.", completion), "severity": "success"})
	}
	if unread > 50 {
		insights = append(insights, map[string]any{"type": "backlog", "message": fmt.Sprintf("You have %d unread bookmarks. Consider reviewing your backlog.", unread), "severity": "warning"})
	} else if unread > 0 {
		insights = append(insights, map[string]any{"type": "backlog", "message": fmt.Sprintf("You have %d unread bookmarks waiting for review.", unread), "severity": "info"})
	}
	if readingMinutes >= 600 {
		insights = append(insights, map[string]any{"type": "reading_time", "message": fmt.Sprintf("You spent %.1f hours reading this month.", float64(readingMinutes)/60), "severity": "success"})
	} else if readingMinutes < 60 && savedRecent >= 10 {
		insights = append(insights, map[string]any{"type": "reading_time", "message": "You save bookmarks but spend little time reading. Try scheduling reading time.", "severity": "info"})
	}
	if len(insights) == 0 && total > 0 {
		insights = append(insights, map[string]any{"type": "library", "message": "Your library is ready for topic review and resurfacing.", "severity": "info"})
	}
	return insights
}

func (s *Service) analyticsPrompt(ctx context.Context, userID string) string {
	var total, unread, read, collections int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN read_status=0 THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN read_status=1 THEN 1 ELSE 0 END),0) FROM bookmarks WHERE user_id=?`, userID).Scan(&total, &unread, &read)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collections WHERE user_id=?`, userID).Scan(&collections)
	return fmt.Sprintf("Write one concise, specific learning insight for an Arivu user. Stats: total bookmarks=%d, read=%d, unread=%d, collections=%d. Avoid generic advice and do not mention implementation details.", total, read, unread, collections)
}

func (s *Service) Resurfacing(w http.ResponseWriter, r *http.Request, user auth.User) {
	limit := queryInt(r, "limit", 5, 1, 50)
	candidates, err := s.resurfacingCandidates(r.Context(), user.ID, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load resurfacing suggestions")
		return
	}
	var suggestions []map[string]any
	for i, candidate := range candidates {
		if i >= limit {
			break
		}
		suggestions = append(suggestions, candidate.Bookmark)
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": suggestions, "total_candidates": len(candidates)})
}

func (s *Service) SnoozeResurfacing(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Days int `json:"days"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if body.Days <= 0 {
		body.Days = 7
	}
	if body.Days > 90 {
		writeError(w, http.StatusBadRequest, "days must be between 1 and 90")
		return
	}
	if !s.bookmarkExists(r.Context(), user.ID, r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "Bookmark not found")
		return
	}
	snoozeUntil := time.Now().UTC().AddDate(0, 0, body.Days).Format(time.RFC3339)
	_, _ = s.db.ExecContext(r.Context(), `UPDATE bookmarks SET resurfacing_snoozed_until=?, updated_at=? WHERE id=? AND user_id=?`, snoozeUntil, time.Now().UTC().Format(time.RFC3339), r.PathValue("id"), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"message": fmt.Sprintf("Bookmark snoozed for %d days", body.Days), "snoozed_until": snoozeUntil})
}

func (s *Service) ArchiveResurfacing(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.setResurfacingArchived(w, r, user, true)
}

func (s *Service) UnarchiveResurfacing(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.setResurfacingArchived(w, r, user, false)
}

func (s *Service) setResurfacingArchived(w http.ResponseWriter, r *http.Request, user auth.User, archived bool) {
	if !s.bookmarkExists(r.Context(), user.ID, r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "Bookmark not found")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE bookmarks SET resurfacing_archived=?, updated_at=? WHERE id=? AND user_id=?`, archived, time.Now().UTC().Format(time.RFC3339), r.PathValue("id"), user.ID)
	if archived {
		writeJSON(w, http.StatusOK, map[string]any{"message": "Bookmark archived from resurfacing"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Bookmark unarchived from resurfacing"})
}

func (s *Service) MemoryJogger(w http.ResponseWriter, r *http.Request, user auth.User) {
	candidates, err := s.resurfacingCandidates(r.Context(), user.ID, 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load memory jogger")
		return
	}
	if len(candidates) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"bookmark": nil, "context": nil, "has_memory": false, "message": "Save more bookmarks to unlock daily memories"})
		return
	}
	top := candidates[0]
	writeJSON(w, http.StatusOK, map[string]any{
		"bookmark":   top.Bookmark,
		"has_memory": true,
		"context": map[string]any{
			"days_since_saved":    daysSinceString(top.Bookmark["created_at"]),
			"days_since_accessed": top.DaysSinceAccess,
			"connection_count":    0,
			"connected_topics":    []any{},
			"reason":              top.Bookmark["resurfacing_reason"],
		},
	})
}

func (s *Service) KnowledgeGraph(w http.ResponseWriter, r *http.Request, user auth.User) {
	limit := queryInt(r, "limit", 50, 1, 500)
	bookmarks, err := s.graphBookmarks(r.Context(), user.ID, limit, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load knowledge graph")
		return
	}
	payload := s.graphPayload(bookmarks)
	writeJSON(w, http.StatusOK, payload)
}

func (s *Service) GraphSearch(w http.ResponseWriter, r *http.Request, user auth.User) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		query = strings.TrimSpace(r.URL.Query().Get("q"))
	}
	if len(query) < 3 {
		writeError(w, http.StatusBadRequest, "Query must be at least 3 characters")
		return
	}
	if len(query) > 10240 {
		writeError(w, http.StatusBadRequest, "Query too large (max 10KB)")
		return
	}
	limit := queryInt(r, "limit", 10, 1, 50)
	bookmarks, err := s.graphBookmarks(r.Context(), user.ID, 500, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not search knowledge graph")
		return
	}
	if len(bookmarks) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}, "message": "No bookmarks with semantic data available"})
		return
	}
	var queryEmbedding []float64
	if embedding, err := s.geminiClient(r.Context()).GenerateEmbedding(r.Context(), query, "retrieval_query"); err == nil {
		queryEmbedding = embedding
	}
	results, threshold := rankGraphSearch(query, queryEmbedding, bookmarks)
	if len(results) > limit {
		results = results[:limit]
	}
	var message any
	if len(results) == 0 {
		message = "No strongly matching bookmarks found. Try different search terms."
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "query": query, "adaptive_threshold": roundFloat(threshold, 4), "message": message})
}

func (s *Service) ExpandQuery(w http.ResponseWriter, r *http.Request, user auth.User) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if len(query) < 2 {
		writeError(w, http.StatusBadRequest, "Query must be at least 2 characters")
		return
	}
	if len(query) > 10240 {
		writeError(w, http.StatusBadRequest, "Query too large (max 10KB)")
		return
	}
	maxExpansions := queryInt(r, "max_expansions", 10, 1, 50)
	bookmarks, err := s.graphBookmarks(r.Context(), user.ID, 500, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not expand query")
		return
	}
	response := expandGraphQuery(query, maxExpansions, bookmarks)
	writeJSON(w, http.StatusOK, response)
}
func (s *Service) Aged(w http.ResponseWriter, r *http.Request, user auth.User) { s.List(w, r, user) }
func (s *Service) Duplicates(w http.ResponseWriter, r *http.Request, user auth.User) {
	bookmarks, err := s.duplicateCandidates(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not detect duplicates")
		return
	}
	groups := []map[string]any{}
	byURL := map[string][]duplicateBookmark{}
	for _, bookmark := range bookmarks {
		if bookmark.NormalizedURL != "" {
			byURL[bookmark.NormalizedURL] = append(byURL[bookmark.NormalizedURL], bookmark)
		}
	}
	for normalized, group := range byURL {
		if len(group) < 2 {
			continue
		}
		groups = append(groups, map[string]any{"type": "exact_url", "url": normalized, "count": len(group), "bookmarks": duplicatePayloads(group)})
	}
	seenSimilar := map[string]bool{}
	for i := range bookmarks {
		if len(bookmarks[i].Embedding) == 0 {
			continue
		}
		for j := i + 1; j < len(bookmarks); j++ {
			if len(bookmarks[j].Embedding) == 0 {
				continue
			}
			similarity := cosineSimilarity(bookmarks[i].Embedding, bookmarks[j].Embedding)
			if similarity < 0.85 {
				continue
			}
			key := bookmarks[i].Data["id"].(string) + ":" + bookmarks[j].Data["id"].(string)
			if seenSimilar[key] {
				continue
			}
			seenSimilar[key] = true
			groups = append(groups, map[string]any{"type": "similar_content", "similarity": similarity, "bookmarks": []map[string]any{bookmarks[i].Data, bookmarks[j].Data}})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"duplicates": groups})
}
func (s *Service) Merge(w http.ResponseWriter, r *http.Request, user auth.User) {
	bookmarkIDs, err := decodeMergeIDs(r)
	if err != nil || len(bookmarkIDs) < 2 {
		writeError(w, http.StatusBadRequest, "Need at least 2 bookmarks to merge")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not merge bookmarks")
		return
	}
	defer tx.Rollback()
	owned, err := ownedBookmarkIDs(r.Context(), tx, user.ID, bookmarkIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not merge bookmarks")
		return
	}
	if len(owned) < 2 {
		writeError(w, http.StatusNotFound, "Bookmarks not found")
		return
	}
	keepID := owned[0]
	deleteIDs := owned[1:]
	if err := mergeBookmarkRows(r.Context(), tx, user.ID, keepID, deleteIDs); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not merge bookmarks")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not merge bookmarks")
		return
	}
	kept, _ := s.getBookmark(r.Context(), user.ID, keepID)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Bookmarks merged", "kept_bookmark": kept, "merged_count": len(deleteIDs)})
}
func (s *Service) BulkDelete(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		BookmarkIDs []string `json:"bookmark_ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	deleted := 0
	for _, id := range body.BookmarkIDs {
		res, _ := s.db.ExecContext(r.Context(), `DELETE FROM bookmarks WHERE id=? AND user_id=?`, id, user.ID)
		if n, _ := res.RowsAffected(); n > 0 {
			deleted++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted_count": deleted})
}
func (s *Service) BulkMarkRead(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		BookmarkIDs []string `json:"bookmark_ids"`
		ReadStatus  bool     `json:"read_status"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	updated := 0
	for _, id := range body.BookmarkIDs {
		res, _ := s.db.ExecContext(r.Context(), `UPDATE bookmarks SET read_status=?, updated_at=? WHERE id=? AND user_id=?`, body.ReadStatus, time.Now().UTC().Format(time.RFC3339), id, user.ID)
		if n, _ := res.RowsAffected(); n > 0 {
			updated++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated_count": updated})
}

func (s *Service) Counts(ctx context.Context) CountsResult {
	return CountsResult{Users: count(ctx, s.db, "users", ""), Bookmarks: count(ctx, s.db, "bookmarks", ""), Collections: count(ctx, s.db, "collections", ""), Summaries: count(ctx, s.db, "ai_summaries", "")}
}

func (s *Service) getBookmark(ctx context.Context, userID, id string) (map[string]any, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,url,title,description,domain,favicon,thumbnail,reading_time,read_status,source,created_at,updated_at,last_accessed,view_count,version,sanitized_html,text_content FROM bookmarks WHERE id=? AND user_id=?`, id, userID)
	bm := scanBookmark(row)
	if bm["id"] == "" {
		return nil, errors.New("not found")
	}
	return bm, nil
}

func (s *Service) summary(ctx context.Context, userID, bookmarkID string) map[string]any {
	var one, bullets, long, highlights, tags, status sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT one_sentence,bullet_points_json,long_form,highlights_json,suggested_tags_json,processing_status FROM ai_summaries WHERE bookmark_id=? AND user_id=?`, bookmarkID, userID).Scan(&one, &bullets, &long, &highlights, &tags, &status)
	if err != nil {
		return map[string]any{"processing_status": "pending"}
	}
	return map[string]any{"one_sentence": one.String, "bullet_points": jsonList(bullets.String), "long_form": long.String, "highlights": jsonList(highlights.String), "suggested_tags": jsonList(tags.String), "processing_status": status.String}
}

type duplicateBookmark struct {
	Data          map[string]any
	Embedding     []float64
	NormalizedURL string
}

type graphBookmark struct {
	ID        string
	Data      map[string]any
	Embedding []float64
	Entities  []string
	Concepts  []string
}

func (s *Service) duplicateCandidates(ctx context.Context, userID string) ([]duplicateBookmark, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,url,title,description,domain,favicon,thumbnail,reading_time,read_status,source,created_at,updated_at,last_accessed,view_count,version,sanitized_html,text_content,embedding FROM bookmarks WHERE user_id=? ORDER BY created_at DESC LIMIT 500`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []duplicateBookmark
	for rows.Next() {
		var embedding []byte
		bm := scanBookmarkWithEmbedding(rows, &embedding)
		if bm["id"] == "" {
			continue
		}
		candidates = append(candidates, duplicateBookmark{Data: bm, Embedding: parseEmbedding(embedding), NormalizedURL: normalizeDuplicateURL(bm["url"].(string))})
	}
	return candidates, rows.Err()
}

func (s *Service) graphBookmark(ctx context.Context, userID, bookmarkID string) (graphBookmark, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,url,title,description,domain,favicon,thumbnail,reading_time,read_status,source,created_at,updated_at,last_accessed,view_count,version,sanitized_html,text_content,embedding FROM bookmarks WHERE user_id=? AND id=? LIMIT 1`, userID, bookmarkID)
	if err != nil {
		return graphBookmark{}, err
	}
	if !rows.Next() {
		_ = rows.Close()
		return graphBookmark{}, errors.New("not found")
	}
	var embedding []byte
	data := scanBookmarkWithEmbedding(rows, &embedding)
	scanErr := rows.Err()
	if err := rows.Close(); err != nil {
		return graphBookmark{}, err
	}
	if scanErr != nil {
		return graphBookmark{}, scanErr
	}
	if data["id"] == "" {
		return graphBookmark{}, errors.New("not found")
	}
	entities, err := s.graphTerms(ctx, userID, "bookmark_entities", "entity", []string{bookmarkID})
	if err != nil {
		return graphBookmark{}, err
	}
	concepts, err := s.graphTerms(ctx, userID, "bookmark_concepts", "concept", []string{bookmarkID})
	if err != nil {
		return graphBookmark{}, err
	}
	return graphBookmark{ID: bookmarkID, Data: data, Embedding: parseEmbedding(embedding), Entities: entities[bookmarkID], Concepts: concepts[bookmarkID]}, nil
}

func (s *Service) graphBookmarks(ctx context.Context, userID string, limit int, requireEmbedding bool) ([]graphBookmark, error) {
	query := `SELECT id,url,title,description,domain,favicon,thumbnail,reading_time,read_status,source,created_at,updated_at,last_accessed,view_count,version,sanitized_html,text_content,embedding FROM bookmarks WHERE user_id=?`
	if requireEmbedding {
		query += ` AND embedding IS NOT NULL AND embedding_dim>0`
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	var bookmarks []graphBookmark
	var ids []string
	for rows.Next() {
		var embedding []byte
		data := scanBookmarkWithEmbedding(rows, &embedding)
		id, _ := data["id"].(string)
		if id == "" {
			continue
		}
		bookmarks = append(bookmarks, graphBookmark{ID: id, Data: data, Embedding: parseEmbedding(embedding)})
		ids = append(ids, id)
	}
	scanErr := rows.Err()
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if scanErr != nil {
		return nil, scanErr
	}
	entities, err := s.graphTerms(ctx, userID, "bookmark_entities", "entity", ids)
	if err != nil {
		return nil, err
	}
	concepts, err := s.graphTerms(ctx, userID, "bookmark_concepts", "concept", ids)
	if err != nil {
		return nil, err
	}
	for i := range bookmarks {
		bookmarks[i].Entities = entities[bookmarks[i].ID]
		bookmarks[i].Concepts = concepts[bookmarks[i].ID]
	}
	return bookmarks, nil
}

func (s *Service) graphTerms(ctx context.Context, userID, table, column string, bookmarkIDs []string) (map[string][]string, error) {
	result := map[string][]string{}
	if len(bookmarkIDs) == 0 {
		return result, nil
	}
	allowed := map[string]map[string]bool{
		"bookmark_entities": {"entity": true},
		"bookmark_concepts": {"concept": true},
	}
	if !allowed[table][column] {
		return nil, fmt.Errorf("invalid graph term table")
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(bookmarkIDs)), ",")
	args := []any{userID}
	for _, id := range bookmarkIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT bookmark_id,%s FROM %s WHERE user_id=? AND bookmark_id IN (%s) ORDER BY %s COLLATE NOCASE`, column, table, placeholders, column), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var bookmarkID, term string
		if err := rows.Scan(&bookmarkID, &term); err != nil {
			return nil, err
		}
		if strings.TrimSpace(term) != "" {
			result[bookmarkID] = append(result[bookmarkID], term)
		}
	}
	return result, rows.Err()
}

func scanBookmarkWithEmbedding(row scanner, embedding *[]byte) map[string]any {
	return scanBookmarkRow(row, embedding)
}

func duplicatePayloads(bookmarks []duplicateBookmark) []map[string]any {
	result := make([]map[string]any, 0, len(bookmarks))
	for _, bookmark := range bookmarks {
		result = append(result, bookmark.Data)
	}
	return result
}

func parseEmbedding(raw []byte) []float64 {
	if len(raw) == 0 {
		return nil
	}
	var values []float64
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	return nil
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func normalizeDuplicateURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return strings.ToLower(strings.TrimRight(raw, "/"))
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return strings.TrimRight(parsed.String(), "/")
}

func decodeMergeIDs(r *http.Request) ([]string, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err == nil {
		return ids, nil
	}
	var body struct {
		BookmarkIDs []string `json:"bookmark_ids"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	return body.BookmarkIDs, nil
}

func (s *Service) graphPayload(bookmarks []graphBookmark) map[string]any {
	entityConnections := map[string][]string{}
	conceptConnections := map[string][]string{}
	entityCounts := map[string]int{}
	conceptCounts := map[string]int{}
	nodes := make([]map[string]any, 0, len(bookmarks))
	for _, bookmark := range bookmarks {
		payload := cloneMap(bookmark.Data)
		payload["entities"] = bookmark.Entities
		payload["concepts"] = bookmark.Concepts
		nodes = append(nodes, payload)
		for _, entity := range bookmark.Entities {
			entityConnections[entity] = append(entityConnections[entity], bookmark.ID)
			entityCounts[entity]++
		}
		for _, concept := range bookmark.Concepts {
			conceptConnections[concept] = append(conceptConnections[concept], bookmark.ID)
			conceptCounts[concept]++
		}
	}
	entityImportance := graphImportance(entityCounts, len(bookmarks))
	conceptImportance := graphImportance(conceptCounts, len(bookmarks))
	related := map[string][]any{}
	for _, bookmark := range bookmarks {
		if len(bookmark.Embedding) == 0 {
			continue
		}
		items := []map[string]any{}
		for _, other := range bookmarks {
			if other.ID == bookmark.ID || len(other.Embedding) == 0 {
				continue
			}
			similarity := cosineSimilarity(bookmark.Embedding, other.Embedding)
			if similarity > 0.5 {
				items = append(items, map[string]any{"id": other.ID, "similarity": roundFloat(similarity, 3)})
			}
		}
		sort.SliceStable(items, func(i, j int) bool {
			return numberValue(items[i]["similarity"]) > numberValue(items[j]["similarity"])
		})
		if len(items) > 3 {
			items = items[:3]
		}
		for _, item := range items {
			related[bookmark.ID] = append(related[bookmark.ID], []any{item["id"], item["similarity"]})
		}
	}
	return map[string]any{
		"bookmarks":           nodes,
		"entities":            topGraphTerms(entityImportance, 50),
		"concepts":            topGraphTerms(conceptImportance, 50),
		"concept_connections": conceptConnections,
		"entity_connections":  entityConnections,
		"entity_importance":   entityImportance,
		"concept_importance":  conceptImportance,
		"related_bookmarks":   related,
		"total_bookmarks":     len(bookmarks),
		"total_entities":      len(entityCounts),
		"total_concepts":      len(conceptCounts),
	}
}

func graphImportance(counts map[string]int, totalDocs int) map[string]float64 {
	importance := map[string]float64{}
	for term, count := range counts {
		if count == 0 {
			continue
		}
		idf := math.Log(float64(totalDocs+1)/float64(count+1)) + 1
		connectionScore := math.Log(float64(count) + 1)
		importance[term] = roundFloat(idf*connectionScore, 3)
	}
	return importance
}

func topGraphTerms(importance map[string]float64, limit int) []string {
	type termScore struct {
		Term  string
		Score float64
	}
	terms := make([]termScore, 0, len(importance))
	for term, score := range importance {
		terms = append(terms, termScore{Term: term, Score: score})
	}
	sort.SliceStable(terms, func(i, j int) bool {
		if terms[i].Score == terms[j].Score {
			return strings.ToLower(terms[i].Term) < strings.ToLower(terms[j].Term)
		}
		return terms[i].Score > terms[j].Score
	})
	if len(terms) > limit {
		terms = terms[:limit]
	}
	result := make([]string, 0, len(terms))
	for _, term := range terms {
		result = append(result, term.Term)
	}
	return result
}

func rankGraphSearch(query string, queryEmbedding []float64, bookmarks []graphBookmark) ([]map[string]any, float64) {
	queryTokens := tokenize(query)
	semanticScores := map[string]float64{}
	rawScores := []float64{}
	for _, bookmark := range bookmarks {
		score := 0.0
		if len(queryEmbedding) > 0 && len(bookmark.Embedding) == len(queryEmbedding) {
			score = cosineSimilarity(queryEmbedding, bookmark.Embedding)
		} else {
			score = lexicalGraphScore(queryTokens, bookmark)
		}
		semanticScores[bookmark.ID] = score
		rawScores = append(rawScores, score)
	}
	threshold := adaptiveThreshold(rawScores)
	entityIDF := entityIDF(bookmarks)
	results := []map[string]any{}
	for _, bookmark := range bookmarks {
		semantic := semanticScores[bookmark.ID]
		if semantic < threshold {
			continue
		}
		entityScore := entityBoost(queryTokens, appendStringSlices(bookmark.Entities, bookmark.Concepts), entityIDF)
		textScore := lexicalGraphScore(queryTokens, bookmark)
		rrf := semantic
		if entityScore > 0 {
			rrf += entityScore
		}
		if textScore > 0 {
			rrf += textScore * 0.25
		}
		payload := cloneMap(bookmark.Data)
		payload["entities"] = bookmark.Entities
		payload["concepts"] = bookmark.Concepts
		payload["similarity_score"] = roundFloat(semantic, 4)
		payload["entity_score"] = roundFloat(entityScore, 4)
		payload["rrf_score"] = roundFloat(rrf, 4)
		results = append(results, payload)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if numberValue(results[i]["rrf_score"]) == numberValue(results[j]["rrf_score"]) {
			return results[i]["created_at"].(string) > results[j]["created_at"].(string)
		}
		return numberValue(results[i]["rrf_score"]) > numberValue(results[j]["rrf_score"])
	})
	return results, threshold
}

func adaptiveThreshold(scores []float64) float64 {
	if len(scores) == 0 {
		return 0.15
	}
	total := 0.0
	for _, score := range scores {
		total += score
	}
	mean := total / float64(len(scores))
	var variance float64
	for _, score := range scores {
		diff := score - mean
		variance += diff * diff
	}
	threshold := mean - math.Sqrt(variance/float64(len(scores)))
	if threshold < 0.10 {
		return 0.10
	}
	return threshold
}

func lexicalGraphScore(queryTokens []string, bookmark graphBookmark) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	text := strings.ToLower(strings.Join([]string{
		stringValue(bookmark.Data["title"]),
		stringValue(bookmark.Data["description"]),
		stringValue(bookmark.Data["domain"]),
		stringValue(bookmark.Data["text_content"]),
		strings.Join(bookmark.Entities, " "),
		strings.Join(bookmark.Concepts, " "),
	}, " "))
	matches := 0
	for _, token := range queryTokens {
		if strings.Contains(text, token) {
			matches++
		}
	}
	return float64(matches) / float64(len(queryTokens))
}

func entityIDF(bookmarks []graphBookmark) map[string]float64 {
	counts := map[string]int{}
	for _, bookmark := range bookmarks {
		seen := map[string]bool{}
		for _, term := range appendStringSlices(bookmark.Entities, bookmark.Concepts) {
			normalized := strings.ToLower(term)
			if normalized != "" && !seen[normalized] {
				counts[normalized]++
				seen[normalized] = true
			}
		}
	}
	idf := map[string]float64{}
	total := len(bookmarks)
	for term, count := range counts {
		idf[term] = math.Log(float64(total+1) / float64(count+1))
	}
	return idf
}

func entityBoost(tokens []string, terms []string, idf map[string]float64) float64 {
	score := 0.0
	for _, term := range terms {
		normalized := strings.ToLower(term)
		for _, token := range tokens {
			if strings.Contains(normalized, token) {
				score += idf[normalized] + 1
				break
			}
		}
	}
	return score
}

func expandGraphQuery(query string, maxExpansions int, bookmarks []graphBookmark) map[string]any {
	queryTokens := tokenize(query)
	entityToBookmarks := map[string]map[string]bool{}
	conceptToBookmarks := map[string]map[string]bool{}
	for _, bookmark := range bookmarks {
		for _, entity := range bookmark.Entities {
			addTermBookmark(entityToBookmarks, entity, bookmark.ID)
		}
		for _, concept := range bookmark.Concepts {
			addTermBookmark(conceptToBookmarks, concept, bookmark.ID)
		}
	}
	directEntities := directGraphMatches(queryTokens, entityToBookmarks)
	directConcepts := directGraphMatches(queryTokens, conceptToBookmarks)
	matchedBookmarkIDs := map[string]bool{}
	for _, entity := range directEntities {
		for id := range entityToBookmarks[entity] {
			matchedBookmarkIDs[id] = true
		}
	}
	for _, concept := range directConcepts {
		for id := range conceptToBookmarks[concept] {
			matchedBookmarkIDs[id] = true
		}
	}
	coEntities := map[string]int{}
	coConcepts := map[string]int{}
	directEntitySet := stringSet(directEntities)
	directConceptSet := stringSet(directConcepts)
	for _, bookmark := range bookmarks {
		if !matchedBookmarkIDs[bookmark.ID] {
			continue
		}
		for _, entity := range bookmark.Entities {
			if !directEntitySet[entity] {
				coEntities[entity]++
			}
		}
		for _, concept := range bookmark.Concepts {
			if !directConceptSet[concept] {
				coConcepts[concept]++
			}
		}
	}
	expansions := []map[string]any{}
	for _, entity := range firstStrings(directEntities, 5) {
		expansions = append(expansions, map[string]any{"term": entity, "type": "entity", "source": "direct_match", "relevance": 1.0})
	}
	for _, concept := range firstStrings(directConcepts, 5) {
		expansions = append(expansions, map[string]any{"term": concept, "type": "concept", "source": "direct_match", "relevance": 1.0})
	}
	topEntities := topCountTerms(coEntities, maxExpansions)
	topConcepts := topCountTerms(coConcepts, maxExpansions)
	for _, term := range firstStrings(topEntities, 5) {
		expansions = append(expansions, map[string]any{"term": term, "type": "entity", "source": "co_occurrence", "relevance": roundFloat(minFloat(float64(coEntities[term])/5, 0.8), 2)})
	}
	for _, term := range firstStrings(topConcepts, 5) {
		expansions = append(expansions, map[string]any{"term": term, "type": "concept", "source": "co_occurrence", "relevance": roundFloat(minFloat(float64(coConcepts[term])/5, 0.8), 2)})
	}
	sort.SliceStable(expansions, func(i, j int) bool {
		return numberValue(expansions[i]["relevance"]) > numberValue(expansions[j]["relevance"])
	})
	if len(expansions) > maxExpansions {
		expansions = expansions[:maxExpansions]
	}
	return map[string]any{
		"query":                   query,
		"expansions":              expansions,
		"related_entities":        appendStringSlices(firstStrings(directEntities, 10), firstStrings(topEntities, 5)),
		"related_concepts":        appendStringSlices(firstStrings(directConcepts, 10), firstStrings(topConcepts, 5)),
		"total_entities_searched": len(entityToBookmarks),
		"total_concepts_searched": len(conceptToBookmarks),
	}
}

func addTermBookmark(index map[string]map[string]bool, term, bookmarkID string) {
	term = strings.TrimSpace(term)
	if term == "" {
		return
	}
	if index[term] == nil {
		index[term] = map[string]bool{}
	}
	index[term][bookmarkID] = true
}

func directGraphMatches(tokens []string, index map[string]map[string]bool) []string {
	var matches []string
	for term := range index {
		normalized := strings.ToLower(term)
		for _, token := range tokens {
			if strings.Contains(normalized, token) {
				matches = append(matches, term)
				break
			}
		}
	}
	sort.Strings(matches)
	return matches
}

func topCountTerms(counts map[string]int, limit int) []string {
	terms := make([]string, 0, len(counts))
	for term := range counts {
		terms = append(terms, term)
	}
	sort.SliceStable(terms, func(i, j int) bool {
		if counts[terms[i]] == counts[terms[j]] {
			return strings.ToLower(terms[i]) < strings.ToLower(terms[j])
		}
		return counts[terms[i]] > counts[terms[j]]
	})
	if len(terms) > limit {
		terms = terms[:limit]
	}
	return terms
}

func firstStrings(values []string, limit int) []string {
	if len(values) > limit {
		return values[:limit]
	}
	return values
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}

func appendStringSlices(a, b []string) []string {
	result := make([]string, 0, len(a)+len(b))
	result = append(result, a...)
	result = append(result, b...)
	return result
}

func tokenize(value string) []string {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	var tokens []string
	seen := map[string]bool{}
	for _, field := range fields {
		if len(field) < 2 || seen[field] {
			continue
		}
		tokens = append(tokens, field)
		seen[field] = true
	}
	return tokens
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func stringValue(value any) string {
	raw, _ := value.(string)
	return raw
}

func roundFloat(value float64, precision int) float64 {
	multiplier := math.Pow10(precision)
	return math.Round(value*multiplier) / multiplier
}

func ownedBookmarkIDs(ctx context.Context, tx *sql.Tx, userID string, requested []string) ([]string, error) {
	owned := []string{}
	for _, id := range requested {
		var found string
		err := tx.QueryRowContext(ctx, `SELECT id FROM bookmarks WHERE id=? AND user_id=?`, id, userID).Scan(&found)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		owned = append(owned, found)
	}
	return owned, nil
}

func mergeBookmarkRows(ctx context.Context, tx *sql.Tx, userID, keepID string, deleteIDs []string) error {
	for _, deleteID := range deleteIDs {
		if err := mergeOneBookmark(ctx, tx, userID, keepID, deleteID); err != nil {
			return err
		}
	}
	return nil
}

func mergeOneBookmark(ctx context.Context, tx *sql.Tx, userID, keepID, deleteID string) error {
	var keepTitle, keepDescription, keepFavicon, keepThumbnail, keepHTML, keepText, keepLast sql.NullString
	var keepRead sql.NullBool
	var keepReading, keepViews sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT title,description,favicon,thumbnail,sanitized_html,text_content,last_accessed,read_status,reading_time,view_count FROM bookmarks WHERE id=? AND user_id=?`, keepID, userID).Scan(&keepTitle, &keepDescription, &keepFavicon, &keepThumbnail, &keepHTML, &keepText, &keepLast, &keepRead, &keepReading, &keepViews)
	if err != nil {
		return err
	}
	var dupTitle, dupDescription, dupFavicon, dupThumbnail, dupHTML, dupText, dupLast sql.NullString
	var dupRead sql.NullBool
	var dupReading, dupViews sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT title,description,favicon,thumbnail,sanitized_html,text_content,last_accessed,read_status,reading_time,view_count FROM bookmarks WHERE id=? AND user_id=?`, deleteID, userID).Scan(&dupTitle, &dupDescription, &dupFavicon, &dupThumbnail, &dupHTML, &dupText, &dupLast, &dupRead, &dupReading, &dupViews)
	if err != nil {
		return err
	}
	mergedTitle := preferString(keepTitle, dupTitle)
	mergedDescription := preferString(keepDescription, dupDescription)
	mergedFavicon := preferString(keepFavicon, dupFavicon)
	mergedThumbnail := preferString(keepThumbnail, dupThumbnail)
	mergedHTML := preferString(keepHTML, dupHTML)
	mergedText := preferString(keepText, dupText)
	mergedLast := maxTimeString(keepLast, dupLast)
	mergedRead := keepRead.Bool || dupRead.Bool
	mergedReading := maxInt64(keepReading.Int64, dupReading.Int64)
	mergedViews := keepViews.Int64 + dupViews.Int64
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `UPDATE bookmarks SET title=?,description=?,favicon=?,thumbnail=?,sanitized_html=?,text_content=?,last_accessed=?,read_status=?,reading_time=?,view_count=?,version=version+1,updated_at=? WHERE id=? AND user_id=?`, mergedTitle, mergedDescription, nullableStringValue(mergedFavicon), nullableStringValue(mergedThumbnail), nullableStringValue(mergedHTML), nullableStringValue(mergedText), nullableStringValue(mergedLast), mergedRead, mergedReading, mergedViews, now, keepID, userID); err != nil {
		return err
	}
	for _, stmt := range []string{
		`UPDATE OR IGNORE collection_bookmarks SET bookmark_id=? WHERE bookmark_id=? AND user_id=?`,
		`UPDATE OR IGNORE bookmark_accesses SET bookmark_id=? WHERE bookmark_id=? AND user_id=?`,
		`UPDATE OR IGNORE bookmark_entities SET bookmark_id=? WHERE bookmark_id=? AND user_id=?`,
		`UPDATE OR IGNORE bookmark_concepts SET bookmark_id=? WHERE bookmark_id=? AND user_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, keepID, deleteID, userID); err != nil {
			return err
		}
	}
	if err := mergeSummary(ctx, tx, userID, keepID, deleteID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ai_summaries WHERE bookmark_id=? AND user_id=?`, deleteID, userID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM bookmarks WHERE id=? AND user_id=?`, deleteID, userID)
	return err
}

func mergeSummary(ctx context.Context, tx *sql.Tx, userID, keepID, deleteID string) error {
	var keepStatus sql.NullString
	_ = tx.QueryRowContext(ctx, `SELECT processing_status FROM ai_summaries WHERE bookmark_id=? AND user_id=?`, keepID, userID).Scan(&keepStatus)
	var dupID, dupOne, dupBullets, dupLong, dupHighlights, dupTags, dupStatus sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,one_sentence,bullet_points_json,long_form,highlights_json,suggested_tags_json,processing_status FROM ai_summaries WHERE bookmark_id=? AND user_id=?`, deleteID, userID).Scan(&dupID, &dupOne, &dupBullets, &dupLong, &dupHighlights, &dupTags, &dupStatus)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if keepStatus.String == "completed" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,bullet_points_json,long_form,highlights_json,suggested_tags_json,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(bookmark_id) DO UPDATE SET one_sentence=excluded.one_sentence,bullet_points_json=excluded.bullet_points_json,long_form=excluded.long_form,highlights_json=excluded.highlights_json,suggested_tags_json=excluded.suggested_tags_json,processing_status=excluded.processing_status,updated_at=excluded.updated_at`,
		ids.New(), keepID, userID, dupOne.String, fallback(dupBullets.String, "[]"), dupLong.String, fallback(dupHighlights.String, "[]"), fallback(dupTags.String, "[]"), fallback(dupStatus.String, "completed"), now, now)
	return err
}

func preferString(primary, secondary sql.NullString) string {
	if primary.Valid && strings.TrimSpace(primary.String) != "" {
		return primary.String
	}
	return secondary.String
}

func nullableStringValue(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func maxTimeString(a, b sql.NullString) string {
	if !a.Valid || a.String == "" {
		return b.String
	}
	if !b.Valid || b.String == "" {
		return a.String
	}
	ta, errA := time.Parse(time.RFC3339, a.String)
	tb, errB := time.Parse(time.RFC3339, b.String)
	if errA != nil || errB != nil {
		return a.String
	}
	if tb.After(ta) {
		return b.String
	}
	return a.String
}

func maxInt64(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}

type resurfacingCandidate struct {
	Bookmark        map[string]any
	Score           float64
	DaysSinceAccess int
}

func (s *Service) resurfacingCandidates(ctx context.Context, userID string, capCount int) ([]resurfacingCandidate, error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx, `SELECT id,url,title,description,domain,favicon,thumbnail,reading_time,read_status,source,created_at,updated_at,last_accessed,view_count,version,sanitized_html,text_content FROM bookmarks WHERE user_id=? AND resurfacing_archived=0 AND (resurfacing_snoozed_until IS NULL OR resurfacing_snoozed_until='' OR resurfacing_snoozed_until<=?) ORDER BY created_at DESC LIMIT ?`, userID, now.Format(time.RFC3339), capCount)
	if err != nil {
		return nil, err
	}
	var bookmarks []map[string]any
	for rows.Next() {
		bm := scanBookmark(rows)
		bookmarks = append(bookmarks, bm)
	}
	scanErr := rows.Err()
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if scanErr != nil {
		return nil, scanErr
	}
	var candidates []resurfacingCandidate
	for _, bm := range bookmarks {
		if bm["title"] == "" {
			continue
		}
		days := daysSinceString(bm["last_accessed"])
		if bm["last_accessed"] == nil {
			days = daysSinceString(bm["created_at"])
		}
		if days < 1 {
			continue
		}
		summary := s.summary(ctx, userID, bm["id"].(string))
		score, breakdown := resurfacingScore(bm, summary, days)
		bm["ai_summary"] = summary
		bm["resurfacing_score"] = score
		bm["resurfacing_breakdown"] = breakdown
		bm["resurfacing_reason"] = resurfacingReason(bm, breakdown, days)
		bm["days_since_access"] = days
		candidates = append(candidates, resurfacingCandidate{Bookmark: bm, Score: score, DaysSinceAccess: days})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	return candidates, nil
}

func resurfacingScore(bookmark map[string]any, summary map[string]any, daysSinceAccess int) (float64, map[string]float64) {
	breakdown := map[string]float64{}
	ageScore := 0.0
	if daysSinceAccess >= 7 && daysSinceAccess <= 90 {
		ageScore = minFloat(float64(daysSinceAccess)/10, 10)
	} else if daysSinceAccess > 90 {
		ageScore = 10
	}
	breakdown["age"] = ageScore
	viewCount := numberValue(bookmark["view_count"])
	breakdown["engagement"] = minFloat(viewCount*2, 10)
	quality := 0.0
	if one, _ := summary["one_sentence"].(string); one != "" {
		quality += 3
	}
	if tags, ok := summary["suggested_tags"].([]any); ok && len(tags) > 0 {
		quality += 2
	}
	breakdown["quality"] = quality
	readingTime := numberValue(bookmark["reading_time"])
	if readingTime > 0 && readingTime <= 10 {
		breakdown["reading_time"] = 10 - readingTime
	} else {
		breakdown["reading_time"] = 0
	}
	spaced := 0.0
	for _, interval := range []int{1, 3, 7, 14, 30} {
		delta := daysSinceAccess - interval
		if delta < 0 {
			delta = -delta
		}
		if delta == 0 {
			spaced = 15
			break
		}
		if delta <= 1 && spaced < 10 {
			spaced = 10
		}
	}
	breakdown["spaced_repetition"] = spaced
	total := 0.0
	for _, value := range breakdown {
		total += value
	}
	breakdown["total"] = total
	return total, breakdown
}

func resurfacingReason(bookmark map[string]any, breakdown map[string]float64, daysSinceAccess int) string {
	reasons := []string{}
	if breakdown["spaced_repetition"] >= 10 {
		switch daysSinceAccess {
		case 1:
			reasons = append(reasons, "Review from yesterday")
		case 7:
			reasons = append(reasons, "Weekly review")
		case 30:
			reasons = append(reasons, "Monthly review")
		default:
			reasons = append(reasons, fmt.Sprintf("Optimal review timing (%d days)", daysSinceAccess))
		}
	} else if breakdown["age"] >= 5 {
		if daysSinceAccess >= 30 {
			reasons = append(reasons, fmt.Sprintf("Not opened in %d days", daysSinceAccess))
		} else {
			reasons = append(reasons, "Time to revisit")
		}
	}
	if numberValue(bookmark["view_count"]) >= 3 {
		reasons = append(reasons, fmt.Sprintf("You've found this valuable (%.0f views)", numberValue(bookmark["view_count"])))
	}
	if rt := numberValue(bookmark["reading_time"]); rt > 0 && rt <= 5 {
		reasons = append(reasons, fmt.Sprintf("Quick %.0f min read", rt))
	}
	if len(reasons) == 0 {
		return "Worth another look"
	}
	if len(reasons) > 2 {
		reasons = reasons[:2]
	}
	return strings.Join(reasons, " • ")
}

func (s *Service) bookmarkExists(ctx context.Context, userID, bookmarkID string) bool {
	var exists int
	_ = s.db.QueryRowContext(ctx, `SELECT 1 FROM bookmarks WHERE id=? AND user_id=?`, bookmarkID, userID).Scan(&exists)
	return exists == 1
}

func queryInt(r *http.Request, key string, fallbackValue, minValue, maxValue int) int {
	value := fallbackValue
	if raw := r.URL.Query().Get(key); raw != "" {
		_, _ = fmt.Sscanf(raw, "%d", &value)
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func daysSinceString(value any) int {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return 30
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 30
	}
	return int(time.Since(parsed).Hours() / 24)
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	default:
		return 0
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

type scanner interface {
	Scan(dest ...any) error
}

func scanBookmark(row scanner) map[string]any {
	return scanBookmarkRow(row)
}

func scanBookmarkRow(row scanner, extra ...any) map[string]any {
	var id, urlv, title, description, domain, favicon, thumbnail, source, created, updated, lastAccessed, html, text sql.NullString
	var readingTime, viewCount, version sql.NullInt64
	var readStatus sql.NullBool
	dest := []any{&id, &urlv, &title, &description, &domain, &favicon, &thumbnail, &readingTime, &readStatus, &source, &created, &updated, &lastAccessed, &viewCount, &version, &html, &text}
	err := row.Scan(append(dest, extra...)...)
	if err != nil {
		return map[string]any{"id": ""}
	}
	return map[string]any{"id": id.String, "url": urlv.String, "title": title.String, "description": description.String, "domain": domain.String, "favicon": nullString(favicon), "thumbnail": nullString(thumbnail), "reading_time": readingTime.Int64, "read_status": readStatus.Bool, "source": source.String, "created_at": created.String, "updated_at": updated.String, "last_accessed": nullString(lastAccessed), "view_count": viewCount.Int64, "version": version.Int64, "html_content": sanitize.HTML(html.String), "text_content": text.String}
}

func count(ctx context.Context, db *sql.DB, table, userID string) int {
	query := `SELECT COUNT(*) FROM ` + table
	args := []any{}
	if userID != "" {
		query += ` WHERE user_id=?`
		args = append(args, userID)
	}
	var n int
	_ = db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n
}

func jsonList(raw string) []any {
	var result []any
	if raw == "" {
		return result
	}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func nullString(value sql.NullString) any {
	if !value.Valid || value.String == "" {
		return nil
	}
	return value.String
}

func fallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func appendAnswerPart(parts *[]string, seen map[string]struct{}, value any, maxParts int) {
	if len(*parts) >= maxParts {
		return
	}
	switch typed := value.(type) {
	case string:
		appendAnswerString(parts, seen, typed)
	case []any:
		for _, item := range typed {
			if len(*parts) >= maxParts {
				return
			}
			appendAnswerPart(parts, seen, item, maxParts)
		}
	}
}

func appendAnswerString(parts *[]string, seen map[string]struct{}, value string) {
	cleaned := strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if len(cleaned) < 2 {
		return
	}
	cleaned = truncateAnswerPart(cleaned, 240)
	key := strings.ToLower(cleaned)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*parts = append(*parts, cleaned)
}

func truncateAnswerPart(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func searchSnippet(query, text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 320 {
		return text
	}
	lowerText := strings.ToLower(text)
	start := 0
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if len(term) < 2 {
			continue
		}
		if idx := strings.Index(lowerText, term); idx >= 0 {
			start = idx - 110
			if start < 0 {
				start = 0
			}
			break
		}
	}
	end := start + 320
	if end > len(text) {
		end = len(text)
	}
	prefix := ""
	if start > 0 {
		prefix = "..."
	}
	suffix := ""
	if end < len(text) {
		suffix = "..."
	}
	return prefix + strings.TrimSpace(text[start:end]) + suffix
}

func readingTime(text string) int {
	words := len(strings.Fields(text))
	if words == 0 {
		return 0
	}
	minutes := words / 200
	if minutes < 1 {
		return 1
	}
	return minutes
}

func decodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"detail": message})
}
