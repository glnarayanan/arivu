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
			BookmarkID string `json:"bookmark_id"`
			URL        string `json:"url"`
		}
		if err := json.Unmarshal([]byte(payload), &body); err != nil {
			return err
		}
		return s.processBookmark(ctx, body.BookmarkID, body.URL)
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
		result, err := s.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, bookmarkID, user.ID, item.URL, item.Title, parsed.Hostname(), now, now)
		if err != nil {
			continue
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			continue
		}
		if item.Source != "" {
			_, _ = s.db.ExecContext(r.Context(), `INSERT INTO import_sources(id,user_id,bookmark_id,source_type,source_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, ids.New(), user.ID, bookmarkID, item.Source, item.URL, "{}", now)
		}
		_, _ = s.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, ids.New(), bookmarkID, user.ID, "pending", now, now)
		payload, _ := json.Marshal(map[string]string{"bookmark_id": bookmarkID, "url": item.URL})
		_ = s.jobs.Enqueue(r.Context(), user.ID, "bookmark.process", string(payload))
		count++
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE import_jobs SET total_bookmarks=?, updated_at=? WHERE id=?`, count, time.Now().UTC().Format(time.RFC3339), jobID)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Import started", "count": count, "import_job_id": jobID})
}

func (s *Service) Export(w http.ResponseWriter, r *http.Request, user auth.User) {
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "json"
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

func (s *Service) Backup(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.Export(w, r, user)
}

func (s *Service) ImportJobs(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,total_bookmarks,content_fetched,ai_processed,failed,status,created_at,updated_at FROM import_jobs WHERE user_id=? ORDER BY created_at DESC`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load import jobs")
		return
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		result = append(result, scanImportJob(rows))
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
	writeJSON(w, http.StatusOK, job)
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
