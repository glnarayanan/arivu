package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/glnarayanan/arivu/internal/auth"
)

const maxSearchResults = 50

func (s *Service) SearchItems(w http.ResponseWriter, r *http.Request, user auth.User) {
	query := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("q"), r.URL.Query().Get("query")))
	if len(query) < 2 || len(query) > maxSearchLen {
		writeError(w, http.StatusBadRequest, "query must be between 2 and 2000 characters")
		return
	}
	results, mode, err := s.searchIndex(r.Context(), user.ID, query, r.URL.Query(), queryInt(r, "limit", 20, 1, maxSearchResults))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not search saved items")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": query, "mode": mode, "count": len(results), "results": results})
}

func (s *Service) RebuildSearch(w http.ResponseWriter, r *http.Request, user auth.User) {
	count, err := s.rebuildSearchIndex(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not rebuild search index")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Search index rebuilt", "count": count})
}

func (s *Service) refreshSearchIndex(ctx context.Context, userID string) {
	_, _ = s.rebuildSearchIndex(ctx, userID)
}

func (s *Service) rebuildSearchIndex(ctx context.Context, userID string) (int, error) {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM search_index WHERE user_id=?`, userID); err != nil {
		return 0, err
	}
	ftsEnabled := true
	if _, err := s.db.ExecContext(ctx, `DELETE FROM search_fts WHERE user_id=?`, userID); err != nil {
		ftsEnabled = false
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,url,title,description,domain,text_content,source,updated_at FROM bookmarks WHERE user_id=? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return 0, err
	}
	var bookmarks []map[string]string
	for rows.Next() {
		var id, rawURL, title, description, domain, text, source, updated string
		_ = rows.Scan(&id, &rawURL, &title, &description, &domain, &text, &source, &updated)
		bookmarks = append(bookmarks, map[string]string{"id": id, "url": rawURL, "title": title, "description": description, "domain": domain, "text": text, "source": source, "updated": updated})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	count := 0
	for _, bookmark := range bookmarks {
		tags := strings.Join(tagNames(s.bookmarkTags(ctx, userID, bookmark["id"])), " ")
		body := strings.Join([]string{
			bookmark["url"],
			bookmark["domain"],
			bookmark["description"],
			bookmark["text"],
			searchMapText(s.summary(ctx, userID, bookmark["id"])),
			searchMapListText(s.bookmarkAnnotations(ctx, userID, bookmark["id"])),
			searchMapListText(s.bookmarkNotes(ctx, userID, bookmark["id"])),
		}, " ")
		links := searchLinksText(s.itemLinks(ctx, userID, "bookmark", bookmark["id"]))
		if err := s.insertSearchRow(ctx, ftsEnabled, userID, "bookmark", bookmark["id"], bookmark["title"], body, tags, links, bookmark["source"], bookmark["updated"]); err != nil {
			return 0, err
		}
		count++
	}
	noteRows, err := s.db.QueryContext(ctx, `SELECT id,title,body,source,updated_at FROM notes WHERE user_id=? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return 0, err
	}
	var notes []map[string]string
	for noteRows.Next() {
		var id, title, body, source, updated string
		_ = noteRows.Scan(&id, &title, &body, &source, &updated)
		notes = append(notes, map[string]string{"id": id, "title": title, "body": body, "source": source, "updated": updated})
	}
	if err := noteRows.Err(); err != nil {
		noteRows.Close()
		return 0, err
	}
	noteRows.Close()
	for _, note := range notes {
		links := searchLinksText(s.itemLinks(ctx, userID, "note", note["id"]))
		if err := s.insertSearchRow(ctx, ftsEnabled, userID, "note", note["id"], note["title"], note["body"], "", links, note["source"], note["updated"]); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}

func (s *Service) insertSearchRow(ctx context.Context, ftsEnabled bool, userID, itemType, itemID, title, body, tags, links, source, updated string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO search_index(user_id,item_type,item_id,title,body,tags,links,source,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, userID, itemType, itemID, title, body, tags, links, source, updated); err != nil {
		return err
	}
	if ftsEnabled {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO search_fts(user_id,item_type,item_id,title,body,tags,links,source,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, userID, itemType, itemID, title, body, tags, links, source, updated)
	}
	return nil
}

func (s *Service) searchIndex(ctx context.Context, userID, query string, values url.Values, limit int) ([]map[string]any, string, error) {
	if rows, err := s.searchIndexFTS(ctx, userID, query, values, limit); err == nil {
		return rows, "fts", nil
	}
	rows, err := s.searchIndexLike(ctx, userID, query, values, limit)
	return rows, "like", err
}

func (s *Service) searchIndexFTS(ctx context.Context, userID, query string, values url.Values, limit int) ([]map[string]any, error) {
	where, args := searchFilters(userID, values)
	args = append([]any{ftsQuery(query)}, args...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT si.item_type,si.item_id,si.title,si.body,si.tags,si.links,si.source,si.updated_at FROM search_fts JOIN search_index si ON si.user_id=search_fts.user_id AND si.item_type=search_fts.item_type AND si.item_id=search_fts.item_id WHERE search_fts MATCH ? AND `+where+` ORDER BY bm25(search_fts), si.updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSearchResults(rows, query)
}

func (s *Service) searchIndexLike(ctx context.Context, userID, query string, values url.Values, limit int) ([]map[string]any, error) {
	where, args := searchFilters(userID, values)
	like := "%" + query + "%"
	args = append(args, like, like, like, like, like, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT item_type,item_id,title,body,tags,links,source,updated_at FROM search_index si WHERE `+where+` AND (title LIKE ? OR body LIKE ? OR tags LIKE ? OR links LIKE ? OR source LIKE ?) ORDER BY updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSearchResults(rows, query)
}

func searchFilters(userID string, values url.Values) (string, []any) {
	where := "si.user_id=?"
	args := []any{userID}
	if itemType := strings.TrimSpace(firstNonEmpty(values.Get("item_type"), values.Get("type"))); itemType == "bookmark" || itemType == "note" {
		where += " AND si.item_type=?"
		args = append(args, itemType)
	}
	if source := strings.TrimSpace(values.Get("source")); source != "" {
		where += " AND si.source=?"
		args = append(args, source)
	}
	if tag := strings.TrimSpace(values.Get("tag")); tag != "" {
		where += " AND si.tags LIKE ?"
		args = append(args, "%"+tag+"%")
	}
	if from := strings.TrimSpace(values.Get("date_from")); from != "" {
		where += " AND si.updated_at>=?"
		args = append(args, from)
	}
	if to := strings.TrimSpace(values.Get("date_to")); to != "" {
		where += " AND si.updated_at<=?"
		args = append(args, to)
	}
	return where, args
}

func scanSearchResults(rows *sql.Rows, query string) ([]map[string]any, error) {
	results := []map[string]any{}
	for rows.Next() {
		var itemType, itemID, title, body, tags, links, source, updated string
		if err := rows.Scan(&itemType, &itemID, &title, &body, &tags, &links, &source, &updated); err != nil {
			return nil, err
		}
		results = append(results, map[string]any{
			"item_type":  itemType,
			"item_id":    itemID,
			"title":      fallback(title, itemID),
			"snippet":    searchSnippet(query, firstNonEmpty(body, links, tags, title)),
			"source":     source,
			"updated_at": updated,
			"href":       itemHref(itemType, itemID),
		})
	}
	return results, rows.Err()
}

func searchMapText(item map[string]any) string {
	var parts []string
	for _, value := range item {
		parts = appendSearchValue(parts, value)
	}
	return strings.Join(parts, " ")
}

func searchMapListText(items []map[string]any) string {
	var parts []string
	for _, item := range items {
		for _, value := range item {
			parts = appendSearchValue(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

func appendSearchValue(parts []string, value any) []string {
	switch typed := value.(type) {
	case string:
		return append(parts, typed)
	case []any:
		for _, item := range typed {
			parts = appendSearchValue(parts, item)
		}
	case []string:
		parts = append(parts, strings.Join(typed, " "))
	}
	return parts
}

func searchLinksText(links map[string]any) string {
	var parts []string
	for _, side := range []string{"outgoing", "incoming"} {
		if items, ok := links[side].([]map[string]any); ok {
			parts = append(parts, searchMapListText(items))
		}
	}
	return strings.Join(parts, " ")
}

func ftsQuery(query string) string {
	var terms []string
	for _, term := range strings.Fields(query) {
		term = strings.ReplaceAll(term, `"`, `""`)
		terms = append(terms, fmt.Sprintf(`"%s"`, term))
	}
	return strings.Join(terms, " ")
}

func itemHref(itemType, itemID string) string {
	if itemType == "note" {
		return "/notes?note=" + url.QueryEscape(itemID)
	}
	return "/bookmark/" + url.PathEscape(itemID)
}
