package bookmarks

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/safefetch"
	"github.com/glnarayanan/arivu/internal/sanitize"
)

var hrefPattern = regexp.MustCompile(`(?i)href=["']([^"']+)["']`)

func (s *Service) ProcessJob(ctx context.Context, jobType string, payload string) error {
	switch jobType {
	case "bookmark.process":
		var body struct {
			BookmarkID  string `json:"bookmark_id"`
			URL         string `json:"url"`
			ImportJobID string `json:"import_job_id"`
		}
		if err := json.Unmarshal([]byte(payload), &body); err != nil {
			return err
		}
		err := s.processBookmark(ctx, body.BookmarkID, body.URL)
		if body.ImportJobID != "" {
			s.recordImportJobProgress(ctx, body.BookmarkID, body.ImportJobID, err)
		}
		return err
	default:
		return fmt.Errorf("unknown job type %s", jobType)
	}
}

func (s *Service) processBookmark(ctx context.Context, bookmarkID string, rawURL string) error {
	userID, ok := s.bookmarkOwner(ctx, bookmarkID)
	if !ok {
		return errors.New("bookmark not found")
	}
	result, err := s.fetcher.Fetch(ctx, rawURL)
	if err != nil {
		return err
	}
	summary := oneSentence(result.Text)
	if aiSummary, err := s.gemini.GenerateSummary(ctx, result.Text); err == nil && strings.TrimSpace(aiSummary) != "" {
		summary = strings.TrimSpace(aiSummary)
	}
	title := fallback(result.Title, result.Domain)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx, `UPDATE bookmarks SET url=?, title=?, description=?, domain=?, sanitized_html=?, text_content=?, reading_time=?, updated_at=? WHERE id=?`,
		result.URL, title, result.Description, result.Domain, sanitize.HTML(result.HTML), result.Text, readingTime(result.Text), now, bookmarkID)
	if err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE ai_summaries SET processing_status='completed', one_sentence=?, updated_at=? WHERE bookmark_id=?`, summary, now, bookmarkID)
	s.storeEnrichment(ctx, bookmarkID, userID, s.enrichText(ctx, bookmarkID, userID, title, result.Description, result.Text))
	return nil
}

func (s *Service) Import(w http.ResponseWriter, r *http.Request, user auth.User) {
	raw, _ := io.ReadAll(r.Body)
	if len(strings.TrimSpace(string(raw))) == 0 {
		writeError(w, http.StatusBadRequest, "No import content provided")
		return
	}
	urls := extractImportURLs(string(raw))
	if len(urls) > 1000 {
		urls = urls[:1000]
	}
	now := time.Now().UTC().Format(time.RFC3339)
	jobID := ids.New()
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO import_jobs(id,user_id,total_bookmarks,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, jobID, user.ID, len(urls), "processing", now, now)
	count := 0
	for _, item := range urls {
		parsed, _ := url.Parse(item.URL)
		bookmarkID := ids.New()
		if item.Title == "" {
			item.Title = parsed.Hostname()
		}
		source := fallback(item.Source, "import")
		result, err := s.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO bookmarks(id,user_id,url,title,domain,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, bookmarkID, user.ID, item.URL, item.Title, parsed.Hostname(), source, now, now)
		if err != nil {
			continue
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			continue
		}
		if item.Source != "" {
			metadata, _ := json.Marshal(map[string]string{"bookmark_id": bookmarkID, "url": item.URL})
			_, _ = s.db.ExecContext(r.Context(), `INSERT INTO import_sources(id,user_id,import_job_id,source_type,source_name,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, ids.New(), user.ID, jobID, source, item.Title, string(metadata), now)
		}
		_, _ = s.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, ids.New(), bookmarkID, user.ID, "pending", now, now)
		payload, _ := json.Marshal(map[string]string{"bookmark_id": bookmarkID, "url": item.URL, "import_job_id": jobID})
		_ = s.jobs.Enqueue(r.Context(), user.ID, "bookmark.process", string(payload))
		count++
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE import_jobs SET total_bookmarks=?, updated_at=? WHERE id=?`, count, time.Now().UTC().Format(time.RFC3339), jobID)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Import started", "count": count, "import_job_id": jobID, "source_report": s.importSourceReport(r.Context(), user.ID, jobID)})
}

func (s *Service) Export(w http.ResponseWriter, r *http.Request, user auth.User) {
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "json"
	}
	if format == "json" {
		export, err := s.fullExport(r.Context(), user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not export bookmarks")
			return
		}
		writeJSON(w, http.StatusOK, export)
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT url,title,description,created_at FROM bookmarks WHERE user_id=? ORDER BY created_at DESC`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not export bookmarks")
		return
	}
	defer rows.Close()
	type item struct {
		URL         string `json:"url"`
		Title       string `json:"title"`
		Description string `json:"description"`
		CreatedAt   string `json:"created_at"`
	}
	var items []item
	for rows.Next() {
		var it item
		_ = rows.Scan(&it.URL, &it.Title, &it.Description, &it.CreatedAt)
		items = append(items, it)
	}
	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="arivu-bookmarks.csv"`)
		writer := csv.NewWriter(w)
		_ = writer.Write([]string{"url", "title", "description", "created_at"})
		for _, it := range items {
			_ = writer.Write([]string{csvCell(it.URL), csvCell(it.Title), csvCell(it.Description), csvCell(it.CreatedAt)})
		}
		writer.Flush()
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="arivu-bookmarks.html"`)
		_, _ = fmt.Fprintln(w, "<!doctype NETSCAPE-Bookmark-file-1><META HTTP-EQUIV=\"Content-Type\" CONTENT=\"text/html; charset=UTF-8\"><TITLE>Bookmarks</TITLE><H1>Bookmarks</H1><DL><p>")
		for _, it := range items {
			_, _ = fmt.Fprintf(w, "<DT><A HREF=\"%s\">%s</A>\n", html.EscapeString(it.URL), html.EscapeString(it.Title))
		}
		_, _ = fmt.Fprintln(w, "</DL><p>")
	case "md", "markdown", "obsidian":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="arivu-bookmarks.md"`)
		_, _ = fmt.Fprintln(w, "# Arivu Bookmarks")
		_, _ = fmt.Fprintln(w)
		for _, it := range items {
			title := fallback(markdownText(it.Title), markdownText(it.URL))
			_, _ = fmt.Fprintf(w, "- [%s](%s)", title, markdownURL(it.URL))
			if strings.TrimSpace(it.Description) != "" {
				_, _ = fmt.Fprintf(w, " - %s", markdownText(it.Description))
			}
			if strings.TrimSpace(it.CreatedAt) != "" {
				_, _ = fmt.Fprintf(w, " <!-- saved:%s -->", markdownText(it.CreatedAt))
			}
			_, _ = fmt.Fprintln(w)
		}
	default:
		writeJSON(w, http.StatusOK, items)
	}
}

func (s *Service) fullExport(ctx context.Context, userID string) (map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,url,title,description,domain,favicon,thumbnail,reading_time,read_status,source,created_at,updated_at,last_accessed,view_count,version,sanitized_html,text_content FROM bookmarks WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	bookmarks := []map[string]any{}
	for rows.Next() {
		bookmarks = append(bookmarks, scanBookmark(rows))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, bookmark := range bookmarks {
		id, _ := bookmark["id"].(string)
		bookmark["ai_summary"] = s.summary(ctx, userID, id)
		bookmark["tags"] = s.bookmarkTags(ctx, userID, id)
		bookmark["annotations"] = s.bookmarkAnnotations(ctx, userID, id)
		bookmark["notes"] = s.bookmarkNotes(ctx, userID, id)
	}
	return map[string]any{
		"version":        1,
		"exported_at":    time.Now().UTC().Format(time.RFC3339),
		"bookmarks":      bookmarks,
		"notes":          s.exportStandaloneNotes(ctx, userID),
		"tags":           s.exportTags(ctx, userID),
		"saved_searches": s.exportSavedSearches(ctx, userID),
		"import_jobs":    s.exportImportJobs(ctx, userID),
		"import_sources": s.exportImportSources(ctx, userID),
		"review_events":  s.exportReviewEvents(ctx, userID),
	}, nil
}

func (s *Service) exportStandaloneNotes(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT n.id,n.title,n.body,n.source,n.created_at,n.updated_at,'' FROM notes n WHERE n.user_id=? AND NOT EXISTS (SELECT 1 FROM bookmark_notes bn WHERE bn.note_id=n.id AND bn.user_id=n.user_id) ORDER BY n.updated_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	notes := []map[string]any{}
	for rows.Next() {
		notes = append(notes, scanNote(rows))
	}
	return notes
}

func (s *Service) exportTags(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,slug,source,created_at,updated_at FROM tags WHERE user_id=? ORDER BY name COLLATE NOCASE`, userID)
	if err != nil {
		return []map[string]any{}
	}
	tags := []map[string]any{}
	for rows.Next() {
		var id, name, slug, source, created, updated string
		_ = rows.Scan(&id, &name, &slug, &source, &created, &updated)
		tags = append(tags, map[string]any{"id": id, "name": name, "slug": slug, "source": source, "created_at": created, "updated_at": updated})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return []map[string]any{}
	}
	rows.Close()
	for _, tag := range tags {
		id, _ := tag["id"].(string)
		tag["aliases"] = s.exportTagAliases(ctx, userID, id)
	}
	return tags
}

func (s *Service) exportTagAliases(ctx context.Context, userID, tagID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT alias,alias_slug,created_at FROM tag_aliases WHERE user_id=? AND tag_id=? ORDER BY alias COLLATE NOCASE`, userID, tagID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	aliases := []map[string]any{}
	for rows.Next() {
		var alias, slug, created string
		_ = rows.Scan(&alias, &slug, &created)
		aliases = append(aliases, map[string]any{"alias": alias, "alias_slug": slug, "created_at": created})
	}
	return aliases
}

func (s *Service) exportSavedSearches(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,query,filters_json,created_at,updated_at FROM saved_searches WHERE user_id=? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	searches := []map[string]any{}
	for rows.Next() {
		var id, name, query, filters, created, updated string
		_ = rows.Scan(&id, &name, &query, &filters, &created, &updated)
		searches = append(searches, map[string]any{"id": id, "name": name, "query": query, "filters": jsonObjectValue(filters), "created_at": created, "updated_at": updated})
	}
	return searches
}

func (s *Service) exportImportJobs(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,total_bookmarks,content_fetched,ai_processed,failed,status,created_at,updated_at FROM import_jobs WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	jobs := []map[string]any{}
	for rows.Next() {
		jobs = append(jobs, scanImportJob(rows))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return []map[string]any{}
	}
	rows.Close()
	for _, job := range jobs {
		id, _ := job["id"].(string)
		job["source_report"] = s.importSourceReport(ctx, userID, id)
	}
	return jobs
}

func (s *Service) exportImportSources(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT import_job_id,source_type,source_name,metadata_json,created_at FROM import_sources WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var jobID, source, name, metadata, created string
		_ = rows.Scan(&jobID, &source, &name, &metadata, &created)
		items = append(items, map[string]any{"import_job_id": jobID, "source": source, "title": name, "metadata": jsonObjectValue(metadata), "created_at": created})
	}
	return items
}

func (s *Service) exportReviewEvents(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT item_type,item_id,action,COALESCE(snoozed_until,''),created_at FROM review_events WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	events := []map[string]any{}
	for rows.Next() {
		var itemType, itemID, action, snoozedUntil, created string
		_ = rows.Scan(&itemType, &itemID, &action, &snoozedUntil, &created)
		events = append(events, map[string]any{"item_type": itemType, "item_id": itemID, "action": action, "snoozed_until": snoozedUntil, "created_at": created})
	}
	return events
}

func (s *Service) recordImportJobProgress(ctx context.Context, bookmarkID string, importJobID string, processErr error) {
	userID, ok := s.bookmarkOwner(ctx, bookmarkID)
	if !ok {
		return
	}
	fetched := 1
	processed := 1
	failed := 0
	if processErr != nil {
		fetched = 0
		processed = 0
		failed = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.ExecContext(ctx, `UPDATE import_jobs SET content_fetched=content_fetched+?, ai_processed=ai_processed+?, failed=failed+?, updated_at=? WHERE id=? AND user_id=?`, fetched, processed, failed, now, importJobID, userID)
	_, _ = s.db.ExecContext(ctx, `UPDATE import_jobs SET status=CASE WHEN total_bookmarks > 0 AND content_fetched + failed >= total_bookmarks THEN 'completed' ELSE status END, updated_at=? WHERE id=? AND user_id=?`, now, importJobID, userID)
}

func (s *Service) Backup(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.Export(w, r, user)
}

func (s *Service) ImportJobs(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,total_bookmarks,content_fetched,ai_processed,failed,status,created_at,updated_at FROM import_jobs WHERE user_id=? ORDER BY created_at DESC`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load import jobs")
		return
	}
	var result []map[string]any
	for rows.Next() {
		result = append(result, scanImportJob(rows))
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load import jobs")
		return
	}
	rows.Close()
	for _, job := range result {
		if id, _ := job["id"].(string); id != "" {
			job["source_report"] = s.importSourceReport(r.Context(), user.ID, id)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) ImportJob(w http.ResponseWriter, r *http.Request, user auth.User) {
	row := s.db.QueryRowContext(r.Context(), `SELECT id,total_bookmarks,content_fetched,ai_processed,failed,status,created_at,updated_at FROM import_jobs WHERE user_id=? AND id=?`, user.ID, r.PathValue("id"))
	job := scanImportJob(row)
	if job["id"] == "" {
		writeError(w, http.StatusNotFound, "Import job not found")
		return
	}
	job["source_report"] = s.importSourceReport(r.Context(), user.ID, r.PathValue("id"))
	job["items"] = s.importSourceItems(r.Context(), user.ID, r.PathValue("id"))
	writeJSON(w, http.StatusOK, job)
}

func (s *Service) importSourceReport(ctx context.Context, userID, importJobID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT source_type,COUNT(*) FROM import_sources WHERE user_id=? AND import_job_id=? GROUP BY source_type ORDER BY COUNT(*) DESC, source_type`, userID, importJobID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	var report []map[string]any
	for rows.Next() {
		var source string
		var count int
		_ = rows.Scan(&source, &count)
		report = append(report, map[string]any{"source": source, "count": count})
	}
	return report
}

func (s *Service) importSourceItems(ctx context.Context, userID, importJobID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT source_type,source_name,metadata_json FROM import_sources WHERE user_id=? AND import_job_id=? ORDER BY created_at ASC LIMIT 100`, userID, importJobID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var source, name, metadata string
		_ = rows.Scan(&source, &name, &metadata)
		meta := map[string]string{}
		_ = json.Unmarshal([]byte(metadata), &meta)
		items = append(items, map[string]any{"source": source, "title": name, "bookmark_id": meta["bookmark_id"], "url": meta["url"]})
	}
	return items
}

type importURL struct {
	URL    string
	Title  string
	Source string
}

func extractImportURLs(raw string) []importURL {
	source := detectImportSource(raw)
	var jsonItems []map[string]any
	if err := json.Unmarshal([]byte(raw), &jsonItems); err == nil {
		var result []importURL
		for _, item := range jsonItems {
			link := firstString(item, "url", "href", "link", "uri")
			if validImportURL(link) {
				result = append(result, importURL{URL: link, Title: firstString(item, "title", "name"), Source: source})
			}
		}
		return result
	}
	var jsonObject map[string]any
	if err := json.Unmarshal([]byte(raw), &jsonObject); err == nil {
		for _, key := range []string{"items", "bookmarks", "results", "links"} {
			if items, ok := jsonObject[key].([]any); ok {
				var result []importURL
				for _, value := range items {
					item, ok := value.(map[string]any)
					if !ok {
						continue
					}
					link := firstString(item, "url", "href", "link", "uri")
					if validImportURL(link) {
						result = append(result, importURL{URL: link, Title: firstString(item, "title", "name"), Source: source})
					}
				}
				return result
			}
		}
	}
	matches := hrefPattern.FindAllStringSubmatch(raw, -1)
	var result []importURL
	for _, match := range matches {
		if len(match) > 1 && validImportURL(match[1]) {
			result = append(result, importURL{URL: match[1], Title: htmlTitleForHref(raw, match[1]), Source: source})
		}
	}
	if len(result) == 0 {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if validImportURL(line) {
				result = append(result, importURL{URL: line, Source: source})
			}
		}
	}
	return result
}

func detectImportSource(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "pocket"):
		return "pocket"
	case strings.Contains(lower, "raindrop"):
		return "raindrop"
	case strings.Contains(lower, "linkwarden"):
		return "linkwarden"
	case strings.Contains(lower, "linkding"):
		return "linkding"
	case strings.Contains(lower, "karakeep"), strings.Contains(lower, "hoarder"):
		return "karakeep"
	case strings.Contains(lower, "netscape-bookmark-file"):
		return "browser"
	default:
		return "import"
	}
}

func htmlTitleForHref(raw, href string) string {
	idx := strings.Index(raw, href)
	if idx < 0 {
		return ""
	}
	rest := raw[idx+len(href):]
	start := strings.Index(rest, ">")
	end := strings.Index(rest, "</A>")
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(rest[start+1 : end]))
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := item[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func validImportURL(raw string) bool {
	return safefetch.ValidateURL(raw) == nil
}

func csvCell(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch value[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}

func markdownText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, "[", "\\[")
	value = strings.ReplaceAll(value, "]", "\\]")
	return value
}

func markdownURL(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), ")", "%29")
}

func scanImportJob(row scanner) map[string]any {
	var id, status, created, updated sql.NullString
	var total, fetched, ai, failed sql.NullInt64
	if err := row.Scan(&id, &total, &fetched, &ai, &failed, &status, &created, &updated); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return map[string]any{"id": ""}
		}
		return map[string]any{"id": ""}
	}
	return map[string]any{"id": id.String, "total_bookmarks": total.Int64, "content_fetched": fetched.Int64, "ai_processed": ai.Int64, "failed": failed.Int64, "status": status.String, "created_at": created.String, "updated_at": updated.String}
}

func oneSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.IndexAny(text, ".!?"); idx > 0 && idx < 280 {
		return text[:idx+1]
	}
	if len(text) > 280 {
		return text[:280] + "..."
	}
	return text
}
