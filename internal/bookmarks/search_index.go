package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

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
	if _, err := s.rebuildSearchIndex(ctx, userID); err != nil {
		log.Printf("search projection rebuild failed for user %s: %v", userID, err)
	}
}

func (s *Service) rebuildSearchIndex(ctx context.Context, userID string) (int, error) {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	return s.rebuildSearchIndexLocked(ctx, userID)
}

func (s *Service) rebuildSearchIndexLocked(ctx context.Context, userID string) (int, error) {
	// Build the replacement before touching the currently searchable projection.
	// Ordered subqueries retain the same per-item limits and text order without
	// issuing five additional database round trips for each bookmark.
	rows, err := s.db.QueryContext(ctx, `WITH note_text AS MATERIALIZED (
		SELECT bookmark_id,group_concat(value,' ' ORDER BY rank) body FROM (
			SELECT bn.bookmark_id,n.title||' '||n.body||' '||n.source value,
				row_number() OVER (PARTITION BY bn.bookmark_id ORDER BY n.updated_at DESC) rank
			FROM bookmark_notes bn JOIN notes n ON bn.note_id=n.id AND bn.user_id=n.user_id WHERE bn.user_id=?1
		) WHERE rank<=100 GROUP BY bookmark_id
	), link_values AS MATERIALIZED (
		SELECT from_type,from_id,to_type,to_id,created_at,
			from_type||' '||from_id||' '||to_type||' '||to_id||' '||label||' '||source||' '||
			CASE from_type WHEN 'bookmark' THEN COALESCE((SELECT NULLIF(title,'') FROM bookmarks WHERE id=from_id AND user_id=item_links.user_id),(SELECT url FROM bookmarks WHERE id=from_id AND user_id=item_links.user_id),'') WHEN 'note' THEN COALESCE((SELECT NULLIF(title,'') FROM notes WHERE id=from_id AND user_id=item_links.user_id),'Untitled note') ELSE '' END||' '||
			CASE to_type WHEN 'bookmark' THEN COALESCE((SELECT NULLIF(title,'') FROM bookmarks WHERE id=to_id AND user_id=item_links.user_id),(SELECT url FROM bookmarks WHERE id=to_id AND user_id=item_links.user_id),'') WHEN 'note' THEN COALESCE((SELECT NULLIF(title,'') FROM notes WHERE id=to_id AND user_id=item_links.user_id),'Untitled note') ELSE '' END value
		FROM item_links WHERE user_id=?1
	), link_text AS MATERIALIZED (
		SELECT item_type,item_id,group_concat(value,' ' ORDER BY rank) body FROM (
			SELECT *,row_number() OVER (PARTITION BY item_type,item_id ORDER BY created_at DESC) rank FROM (
				SELECT from_type item_type,from_id item_id,created_at,value FROM link_values
				UNION ALL SELECT to_type,to_id,created_at,value FROM link_values WHERE from_type<>to_type OR from_id<>to_id
			)
		) WHERE rank<=200 GROUP BY item_type,item_id
	), items AS (
		SELECT 'bookmark' item_type,b.id,COALESCE(b.title,'') title,
			b.url||' '||COALESCE(b.domain,'')||' '||COALESCE(b.description,'')||' '||COALESCE(b.text_content,'')||' '||
			COALESCE((SELECT COALESCE(one_sentence,'')||' '||COALESCE(bullet_points_json,'')||' '||COALESCE(long_form,'')||' '||COALESCE(highlights_json,'')||' '||COALESCE(suggested_tags_json,'') FROM ai_summaries WHERE user_id=?1 AND bookmark_id=b.id),'')||' '||
			COALESCE((SELECT group_concat(value,' ') FROM (SELECT quote||' '||note||' '||selector_json||' '||tags_json value FROM annotations WHERE user_id=?1 AND bookmark_id=b.id ORDER BY created_at DESC LIMIT 100)),'')||' '||
			COALESCE(nt.body,'') body,
			COALESCE((SELECT group_concat(name,' ') FROM (SELECT t.name FROM tags t JOIN bookmark_tags bt ON bt.tag_id=t.id AND bt.user_id=t.user_id WHERE bt.user_id=?1 AND bt.bookmark_id=b.id ORDER BY t.name COLLATE NOCASE)),'') tags,
			COALESCE(b.source,'') source,b.updated_at
		FROM bookmarks b LEFT JOIN note_text nt ON nt.bookmark_id=b.id WHERE b.user_id=?1
		UNION ALL SELECT 'note',id,COALESCE(title,''),COALESCE(body,''),'',COALESCE(source,''),updated_at FROM notes WHERE user_id=?1
	)
	SELECT i.item_type,i.id,i.title,i.body,i.tags,COALESCE(lt.body,''),i.source,i.updated_at
	FROM items i LEFT JOIN link_text lt ON lt.item_type=i.item_type AND lt.item_id=i.id ORDER BY i.item_type,i.updated_at DESC`, userID)
	if err != nil {
		return 0, err
	}
	type projectionRow struct{ itemType, itemID, title, body, tags, links, source, updated string }
	var projection []projectionRow
	for rows.Next() {
		var row projectionRow
		if err := rows.Scan(&row.itemType, &row.itemID, &row.title, &row.body, &row.tags, &row.links, &row.source, &row.updated); err != nil {
			rows.Close()
			return 0, err
		}
		projection = append(projection, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	var ftsTableCount int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='search_fts'`).Scan(&ftsTableCount); err != nil {
		return 0, err
	}
	ftsEnabled := ftsTableCount > 0
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM search_index WHERE user_id=?`, userID); err != nil {
		return 0, err
	}
	if ftsEnabled {
		if _, err = tx.ExecContext(ctx, `DELETE FROM search_fts WHERE user_id=?`, userID); err != nil {
			return 0, err
		}
	}
	tables := []string{"search_index"}
	if ftsEnabled {
		tables = append(tables, "search_fts")
	}
	for _, table := range tables {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO `+table+`(user_id,item_type,item_id,title,body,tags,links,source,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return 0, err
		}
		defer stmt.Close()
		for _, row := range projection {
			if _, err := stmt.ExecContext(ctx, userID, row.itemType, row.itemID, row.title, row.body, row.tags, row.links, row.source, row.updated); err != nil {
				return 0, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return len(projection), nil
}

func (s *Service) searchIndex(ctx context.Context, userID, query string, values url.Values, limit int) ([]map[string]any, string, error) {
	if rows, err := s.searchIndexFTS(ctx, userID, query, values, limit); err == nil {
		return s.decorateSearchResults(ctx, userID, rows, "search"), "fts", nil
	}
	rows, err := s.searchIndexLike(ctx, userID, query, values, limit)
	if err != nil {
		return rows, "like", err
	}
	return s.decorateSearchResults(ctx, userID, rows, "search"), "like", nil
}

func (s *Service) decorateSearchResults(ctx context.Context, userID string, results []map[string]any, surface string) []map[string]any {
	for index, result := range results {
		itemType := stringValue(result["item_type"])
		itemID := stringValue(result["item_id"])
		freshness := freshnessScore(stringValue(result["updated_at"]))
		feedback := s.feedbackState(ctx, userID, itemType, itemID, surface)
		result["freshness_score"] = freshness
		result["feedback_state"] = feedback
		result["result_score"] = roundFloat(100-float64(index*2)+freshness+feedbackSearchWeight(feedback), 2)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return numberValue(results[i]["result_score"]) > numberValue(results[j]["result_score"])
	})
	return results
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
			"why_shown":  searchWhyShown(query, title, body, tags, links, source),
		})
	}
	return results, rows.Err()
}

func searchWhyShown(query, title, body, tags, links, source string) []string {
	query = strings.ToLower(query)
	fields := []struct {
		name  string
		value string
	}{
		{"title match", title},
		{"saved text match", body},
		{"tag match", tags},
		{"link context match", links},
		{"source match", source},
	}
	var reasons []string
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field.value), query) {
			reasons = append(reasons, field.name)
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "semantic or ranked text match")
	}
	return reasons
}

func freshnessScore(updated string) float64 {
	parsed, err := time.Parse(time.RFC3339, updated)
	if err != nil {
		return 0
	}
	days := time.Since(parsed).Hours() / 24
	switch {
	case days <= 7:
		return 15
	case days <= 30:
		return 10
	case days <= 90:
		return 5
	default:
		return 0
	}
}

func feedbackSearchWeight(feedback string) float64 {
	switch feedback {
	case "useful":
		return 15
	case "not_useful":
		return -20
	case "snooze_longer":
		return -30
	case "never_resurface":
		return -45
	default:
		return 0
	}
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
		return "/notes/" + url.PathEscape(itemID)
	}
	return "/bookmark/" + url.PathEscape(itemID)
}
