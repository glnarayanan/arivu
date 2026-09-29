package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
)

const (
	maxNoteBody      = 200_000
	maxAnnotationLen = 20_000
	maxSearchLen     = 2_000
)

func (s *Service) Notes(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT n.id,n.title,n.body,n.source,n.created_at,n.updated_at,COALESCE(bn.bookmark_id,'') FROM notes n LEFT JOIN bookmark_notes bn ON bn.note_id=n.id AND bn.user_id=n.user_id WHERE n.user_id=? ORDER BY n.updated_at DESC LIMIT 200`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load notes")
		return
	}
	var notes []map[string]any
	for rows.Next() {
		notes = append(notes, scanNote(rows))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		writeError(w, http.StatusInternalServerError, "Could not load notes")
		return
	}
	if err := rows.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load notes")
		return
	}
	s.decorateNotes(r.Context(), user.ID, notes)
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}

func (s *Service) LinkTargets(w http.ResponseWriter, r *http.Request, user auth.User) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	itemType := strings.TrimSpace(r.URL.Query().Get("type"))
	limit := queryInt(r, "limit", 50, 1, 100)
	results := []map[string]any{}
	if itemType == "" || itemType == "bookmark" {
		results = append(results, s.bookmarkLinkTargets(r.Context(), user.ID, query, limit)...)
	}
	if itemType == "" || itemType == "note" {
		results = append(results, s.noteLinkTargets(r.Context(), user.ID, query, limit)...)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return stringValue(results[i]["updated_at"]) > stringValue(results[j]["updated_at"])
	})
	if len(results) > limit {
		results = results[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": results})
}

func (s *Service) bookmarkLinkTargets(ctx context.Context, userID, query string, limit int) []map[string]any {
	sqlQuery := `SELECT id,title,domain,url,updated_at FROM bookmarks WHERE user_id=?`
	args := []any{userID}
	if query != "" {
		sqlQuery += ` AND (title LIKE ? OR domain LIKE ? OR url LIKE ?)`
		like := "%" + query + "%"
		args = append(args, like, like, like)
	}
	sqlQuery += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	results := []map[string]any{}
	for rows.Next() {
		var id, title, domain, url, updated string
		_ = rows.Scan(&id, &title, &domain, &url, &updated)
		results = append(results, map[string]any{"id": id, "type": "bookmark", "title": title, "domain": domain, "url": url, "updated_at": updated})
	}
	return results
}

func (s *Service) noteLinkTargets(ctx context.Context, userID, query string, limit int) []map[string]any {
	sqlQuery := `SELECT id,title,updated_at FROM notes WHERE user_id=?`
	args := []any{userID}
	if query != "" {
		sqlQuery += ` AND title LIKE ?`
		args = append(args, "%"+query+"%")
	}
	sqlQuery += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	results := []map[string]any{}
	for rows.Next() {
		var id, title, updated string
		_ = rows.Scan(&id, &title, &updated)
		results = append(results, map[string]any{"id": id, "type": "note", "title": title, "updated_at": updated})
	}
	return results
}

func (s *Service) CreateNote(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Title      string `json:"title"`
		Body       string `json:"body"`
		BookmarkID string `json:"bookmark_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	title := strings.TrimSpace(body.Title)
	text := strings.TrimSpace(body.Body)
	if title == "" && text == "" {
		writeError(w, http.StatusBadRequest, "Title or body is required")
		return
	}
	if len(text) > maxNoteBody {
		writeError(w, http.StatusBadRequest, "Note body is too large")
		return
	}
	id, err := s.CreateNoteCommand(r.Context(), CreateNoteInput{UserID: user.ID, Title: title, Body: text, BookmarkID: body.BookmarkID})
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Bookmark not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create note")
		return
	}
	note, _ := s.note(r.Context(), user.ID, id)
	s.decorateNote(r.Context(), user.ID, note)
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"note": note})
}

func (s *Service) GetNote(w http.ResponseWriter, r *http.Request, user auth.User) {
	note, err := s.note(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Note not found")
		return
	}
	s.decorateNote(r.Context(), user.ID, note)
	writeJSON(w, http.StatusOK, note)
}

func (s *Service) UpdateNote(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if len(body.Body) > maxNoteBody {
		writeError(w, http.StatusBadRequest, "Note body is too large")
		return
	}
	res, _ := s.db.ExecContext(r.Context(), `UPDATE notes SET title=?,body=?,updated_at=? WHERE id=? AND user_id=?`, strings.TrimSpace(body.Title), strings.TrimSpace(body.Body), time.Now().UTC().Format(time.RFC3339), r.PathValue("id"), user.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "Note not found")
		return
	}
	note, _ := s.note(r.Context(), user.ID, r.PathValue("id"))
	s.decorateNote(r.Context(), user.ID, note)
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"note": note})
}

func (s *Service) decorateNote(ctx context.Context, userID string, note map[string]any) {
	id := stringValue(note["id"])
	if id == "" {
		return
	}
	note["links"] = s.itemLinks(ctx, userID, "note", id)
}

// decorateNotes is the list-view equivalent of decorateNote. It keeps the
// singleton response shape and per-relation limits, while reading each
// relation for the selected notes as a bounded owner-scoped set.
func (s *Service) decorateNotes(ctx context.Context, userID string, notes []map[string]any) {
	byID := make(map[string][]map[string]any, len(notes))
	ids := make([]string, 0, len(notes))
	for _, note := range notes {
		id := stringValue(note["id"])
		if id == "" {
			continue
		}
		if _, exists := byID[id]; !exists {
			ids = append(ids, id)
		}
		byID[id] = append(byID[id], note)
		note["links"] = map[string]any{"outgoing": []map[string]any{}, "incoming": []map[string]any{}}
	}
	if len(ids) == 0 {
		return
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 1, len(ids)+1)
	args[0] = userID
	for _, id := range ids {
		args = append(args, id)
	}

	titles := make(map[string]string)
	for id, copies := range byID {
		title := stringValue(copies[0]["title"])
		if title == "" {
			title = "Untitled note"
		}
		titles["note\x00"+id] = title
	}
	readGrouped := func(query string, scan func(scanner) map[string]any, assign func(map[string]any, string)) {
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return
		}
		defer rows.Close()
		var items []map[string]any
		for rows.Next() {
			items = append(items, scan(rows))
		}
		if rows.Err() != nil {
			return
		}
		for _, item := range items {
			id := stringValue(item["item_id"])
			if byID[id] == nil {
				continue
			}
			assign(item, id)
		}
	}
	type linkDirection struct{ field, column string }
	type groupedLink struct {
		field, noteID string
		item          map[string]any
	}
	var groupedLinks []groupedLink
	endpointIDs := map[string]map[string]bool{"bookmark": {}, "note": {}}
	for _, direction := range []linkDirection{{"outgoing", "from_id"}, {"incoming", "to_id"}} {
		query := `SELECT id,from_type,from_id,to_type,to_id,label,source,created_at FROM (SELECT id,from_type,from_id,to_type,to_id,label,source,created_at,ROW_NUMBER() OVER (PARTITION BY ` + direction.column + ` ORDER BY created_at DESC) rank FROM item_links WHERE user_id=? AND ` + strings.TrimSuffix(direction.column, "_id") + `_type='note' AND ` + direction.column + ` IN (` + placeholders + `)) WHERE rank<=100 ORDER BY ` + direction.column + `,created_at DESC`
		readGrouped(query, func(row scanner) map[string]any {
			item := scanLink(row)
			item["item_id"] = item[direction.column]
			return item
		}, func(item map[string]any, id string) {
			delete(item, "item_id")
			for _, endpoint := range []struct{ typ, id string }{{stringValue(item["from_type"]), stringValue(item["from_id"])}, {stringValue(item["to_type"]), stringValue(item["to_id"])}} {
				if endpointIDs[endpoint.typ] != nil {
					endpointIDs[endpoint.typ][endpoint.id] = true
				}
			}
			groupedLinks = append(groupedLinks, groupedLink{direction.field, id, item})
		})
	}
	for itemType, idSet := range endpointIDs {
		endpointArgs := []any{userID}
		for id := range idSet {
			endpointArgs = append(endpointArgs, id)
		}
		for start := 1; start < len(endpointArgs); start += 500 {
			end := min(start+500, len(endpointArgs))
			chunk := append([]any{endpointArgs[0]}, endpointArgs[start:end]...)
			chunkPlaceholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)-1), ",")
			query := `SELECT id,COALESCE(NULLIF(title,''),'Untitled note') FROM notes WHERE user_id=? AND id IN (` + chunkPlaceholders + `)`
			if itemType == "bookmark" {
				query = `SELECT id,COALESCE(NULLIF(title,''),url) FROM bookmarks WHERE user_id=? AND id IN (` + chunkPlaceholders + `)`
			}
			rows, err := s.db.QueryContext(ctx, query, chunk...)
			if err != nil {
				continue
			}
			for rows.Next() {
				var id, title string
				if rows.Scan(&id, &title) == nil {
					titles[itemType+"\x00"+id] = title
				}
			}
			rows.Close()
		}
	}
	for _, grouped := range groupedLinks {
		item := grouped.item
		item["from_title"] = titles[stringValue(item["from_type"])+"\x00"+stringValue(item["from_id"])]
		item["to_title"] = titles[stringValue(item["to_type"])+"\x00"+stringValue(item["to_id"])]
		for _, note := range byID[grouped.noteID] {
			links := note["links"].(map[string]any)
			links[grouped.field] = append(links[grouped.field].([]map[string]any), item)
		}
	}
}

func (s *Service) DeleteNote(w http.ResponseWriter, r *http.Request, user auth.User) {
	deleted, err := s.DeleteItemCommand(r.Context(), user.ID, "note", r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "Could not delete note")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "Note not found")
		return
	}
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Note deleted"})
}

func (s *Service) CreateAnnotation(w http.ResponseWriter, r *http.Request, user auth.User) {
	bookmarkID := r.PathValue("id")
	if !s.bookmarkExists(r.Context(), user.ID, bookmarkID) {
		writeError(w, http.StatusNotFound, "Bookmark not found")
		return
	}
	var body struct {
		Quote    string   `json:"quote"`
		Note     string   `json:"note"`
		Selector any      `json:"selector"`
		Tags     []string `json:"tags"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	quote := strings.TrimSpace(body.Quote)
	note := strings.TrimSpace(body.Note)
	if quote == "" && note == "" {
		writeError(w, http.StatusBadRequest, "Quote or note is required")
		return
	}
	if len(quote) > maxAnnotationLen || len(note) > maxAnnotationLen {
		writeError(w, http.StatusBadRequest, "Annotation is too large")
		return
	}
	selector, ok := jsonObject(body.Selector)
	if !ok {
		writeError(w, http.StatusBadRequest, "selector must be an object")
		return
	}
	tags := cleanStringList(body.Tags, 20)
	tagJSON, _ := json.Marshal(tags)
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	var evidenceID string
	_ = s.db.QueryRowContext(r.Context(), `SELECT id FROM bookmark_evidence WHERE bookmark_id=? AND user_id=? AND is_selected=1`, bookmarkID, user.ID).Scan(&evidenceID)
	var evidence sql.NullString
	if evidenceID != "" {
		evidence = sql.NullString{String: evidenceID, Valid: true}
	}
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO annotations(id,user_id,bookmark_id,quote,note,selector_json,tags_json,evidence_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, user.ID, bookmarkID, quote, note, selector, string(tagJSON), evidence, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create annotation")
		return
	}
	for _, tag := range tags {
		_ = s.attachTag(r.Context(), user.ID, bookmarkID, tag, "manual")
	}
	annotation, _ := s.annotation(r.Context(), user.ID, id)
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"annotation": annotation})
}

func (s *Service) UpdateAnnotation(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Quote    string   `json:"quote"`
		Note     string   `json:"note"`
		Selector any      `json:"selector"`
		Tags     []string `json:"tags"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if len(body.Quote) > maxAnnotationLen || len(body.Note) > maxAnnotationLen {
		writeError(w, http.StatusBadRequest, "Annotation is too large")
		return
	}
	selector, ok := jsonObject(body.Selector)
	if !ok {
		writeError(w, http.StatusBadRequest, "selector must be an object")
		return
	}
	tags := cleanStringList(body.Tags, 20)
	tagJSON, _ := json.Marshal(tags)
	res, _ := s.db.ExecContext(r.Context(), `UPDATE annotations SET quote=?,note=?,selector_json=?,tags_json=?,updated_at=? WHERE id=? AND user_id=?`, strings.TrimSpace(body.Quote), strings.TrimSpace(body.Note), selector, string(tagJSON), time.Now().UTC().Format(time.RFC3339), r.PathValue("id"), user.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "Annotation not found")
		return
	}
	annotation, _ := s.annotation(r.Context(), user.ID, r.PathValue("id"))
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"annotation": annotation})
}

func (s *Service) DeleteAnnotation(w http.ResponseWriter, r *http.Request, user auth.User) {
	res, _ := s.db.ExecContext(r.Context(), `DELETE FROM annotations WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "Annotation not found")
		return
	}
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Annotation deleted"})
}

func (s *Service) Tags(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT t.id,t.name,t.slug,t.source,t.created_at,t.updated_at,COUNT(bt.bookmark_id) FROM tags t LEFT JOIN bookmark_tags bt ON bt.tag_id=t.id AND bt.user_id=t.user_id WHERE t.user_id=? GROUP BY t.id ORDER BY t.name COLLATE NOCASE LIMIT 500`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load tags")
		return
	}
	defer rows.Close()
	var tags []map[string]any
	for rows.Next() {
		var id, name, slug, source, created, updated string
		var count int
		_ = rows.Scan(&id, &name, &slug, &source, &created, &updated, &count)
		tags = append(tags, map[string]any{"id": id, "name": name, "slug": slug, "source": source, "bookmark_count": count, "created_at": created, "updated_at": updated})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Service) CreateTag(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	tag, err := s.upsertTag(r.Context(), user.ID, body.Name, "manual")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tag": tag})
}

func (s *Service) CreateTagAlias(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		TagID string `json:"tag_id"`
		Alias string `json:"alias"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	alias := strings.TrimSpace(body.Alias)
	slug := tagSlug(alias)
	if body.TagID == "" || slug == "" {
		writeError(w, http.StatusBadRequest, "tag_id and alias are required")
		return
	}
	var exists int
	_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM tags WHERE id=? AND user_id=?`, body.TagID, user.ID).Scan(&exists)
	if exists != 1 {
		writeError(w, http.StatusNotFound, "Tag not found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO tag_aliases(id,user_id,tag_id,alias,alias_slug,created_at) VALUES(?,?,?,?,?,?)`, ids.New(), user.ID, body.TagID, alias, slug, now)
	if err != nil {
		writeError(w, http.StatusConflict, "Alias already exists")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alias": map[string]any{"tag_id": body.TagID, "alias": alias, "alias_slug": slug, "created_at": now}})
}

func (s *Service) SavedSearches(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,query,filters_json,created_at,updated_at FROM saved_searches WHERE user_id=? ORDER BY updated_at DESC`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load saved searches")
		return
	}
	defer rows.Close()
	var searches []map[string]any
	for rows.Next() {
		var id, name, query, filters, created, updated string
		_ = rows.Scan(&id, &name, &query, &filters, &created, &updated)
		searches = append(searches, map[string]any{"id": id, "name": name, "query": query, "filters": jsonObjectValue(filters), "created_at": created, "updated_at": updated})
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved_searches": searches})
}

func (s *Service) CreateSavedSearch(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Name    string `json:"name"`
		Query   string `json:"query"`
		Filters any    `json:"filters"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	name := strings.TrimSpace(body.Name)
	query := strings.TrimSpace(body.Query)
	if name == "" || len(query) > maxSearchLen {
		writeError(w, http.StatusBadRequest, "Valid name and query are required")
		return
	}
	filters, ok := jsonObject(body.Filters)
	if !ok {
		writeError(w, http.StatusBadRequest, "filters must be an object")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO saved_searches(id,user_id,name,query,filters_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, user.ID, name, query, filters, now, now)
	if err != nil {
		writeError(w, http.StatusConflict, "Saved search already exists")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved_search": map[string]any{"id": id, "name": name, "query": query, "filters": jsonObjectValue(filters), "created_at": now, "updated_at": now}})
}

func (s *Service) Review(w http.ResponseWriter, r *http.Request, user auth.User) {
	limit := queryInt(r, "limit", 10, 1, 50)
	items, err := s.reviewItems(r.Context(), user.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load review queue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) reviewItems(ctx context.Context, userID string, limit int) ([]map[string]any, error) {
	candidates, err := s.resurfacingCandidates(ctx, userID, 500)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	for _, candidate := range candidates {
		id, _ := candidate.Bookmark["id"].(string)
		if s.recentReviewEvent(ctx, userID, "bookmark", id) {
			continue
		}
		item := cloneMap(candidate.Bookmark)
		item["item_type"] = "bookmark"
		s.decorateReviewItem(ctx, userID, item)
		if stringValue(item["feedback_state"]) == "never_resurface" {
			continue
		}
		items = append(items, item)
	}
	notes, err := s.reviewNotes(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	for _, note := range notes {
		s.decorateReviewItem(ctx, userID, note)
		if stringValue(note["feedback_state"]) == "never_resurface" {
			continue
		}
		items = append(items, note)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return numberValue(items[i]["review_priority"]) > numberValue(items[j]["review_priority"])
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Service) decorateReviewItem(ctx context.Context, userID string, item map[string]any) {
	itemType := stringValue(item["item_type"])
	itemID := stringValue(item["id"])
	reasons := []string{}
	score := numberValue(item["resurfacing_score"])
	now := time.Now().UTC()
	if itemType == "note" {
		updated, err := time.Parse(time.RFC3339, stringValue(item["updated_at"]))
		if err == nil {
			age := int(now.Sub(updated).Hours() / 24)
			if age >= 1 {
				reasons = append(reasons, "unreviewed note")
				score += float64(age)
			}
		}
	}
	if reason := strings.TrimSpace(stringValue(item["resurfacing_reason"])); reason != "" {
		reasons = append(reasons, reason)
	}
	feedback := s.anyFeedbackState(ctx, userID, itemType, itemID)
	item["feedback_state"] = feedback
	switch feedback {
	case "useful":
		reasons = append(reasons, "marked useful")
		score += 10
	case "not_useful":
		reasons = append(reasons, "marked not useful")
		score -= 20
	case "snooze_longer":
		reasons = append(reasons, "asked to snooze longer")
		score -= 30
	case "never_resurface":
		reasons = append(reasons, "never resurface")
		score -= 100
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "ready for review")
	}
	item["review_reasons"] = reasons
	item["review_priority"] = roundFloat(score, 2)
	item["freshness_score"] = freshnessScore(stringValue(item["updated_at"]))
	item["why_shown"] = reasons
}

func (s *Service) Links(w http.ResponseWriter, r *http.Request, user auth.User) {
	itemType, itemID, ok := splitReviewItem(r.URL.Query().Get("item"))
	if !ok || !s.reviewItemExists(r.Context(), user.ID, itemType, itemID) {
		writeError(w, http.StatusNotFound, "Item not found")
		return
	}
	writeJSON(w, http.StatusOK, s.itemLinks(r.Context(), user.ID, itemType, itemID))
}

func (s *Service) CreateLink(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		FromType string `json:"from_type"`
		FromID   string `json:"from_id"`
		ToType   string `json:"to_type"`
		ToID     string `json:"to_id"`
		Label    string `json:"label"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	body.FromType = strings.TrimSpace(body.FromType)
	body.FromID = strings.TrimSpace(body.FromID)
	body.ToType = strings.TrimSpace(body.ToType)
	body.ToID = strings.TrimSpace(body.ToID)
	label := strings.TrimSpace(body.Label)
	if len(label) > 80 {
		writeError(w, http.StatusBadRequest, "label is too large")
		return
	}
	if !s.reviewItemExists(r.Context(), user.ID, body.FromType, body.FromID) || !s.reviewItemExists(r.Context(), user.ID, body.ToType, body.ToID) {
		writeError(w, http.StatusNotFound, "Link item not found")
		return
	}
	if body.FromType == body.ToType && body.FromID == body.ToID {
		writeError(w, http.StatusBadRequest, "Cannot link an item to itself")
		return
	}
	link, err := s.createItemLink(r.Context(), user.ID, body.FromType, body.FromID, body.ToType, body.ToID, label, "manual", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		writeError(w, http.StatusConflict, "Link already exists")
		return
	}
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"link": link})
}

func (s *Service) DeleteLink(w http.ResponseWriter, r *http.Request, user auth.User) {
	res, _ := s.db.ExecContext(r.Context(), `DELETE FROM item_links WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if rows, _ := res.RowsAffected(); rows == 0 {
		writeError(w, http.StatusNotFound, "Link not found")
		return
	}
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Link deleted"})
}

func (s *Service) itemTitle(ctx context.Context, userID, itemType, itemID string) string {
	var title string
	switch itemType {
	case "bookmark":
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(title,''),url) FROM bookmarks WHERE id=? AND user_id=?`, itemID, userID).Scan(&title)
	case "note":
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(title,''),'Untitled note') FROM notes WHERE id=? AND user_id=?`, itemID, userID).Scan(&title)
	}
	return title
}

func (s *Service) reviewNotes(ctx context.Context, userID string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		return []map[string]any{}, nil
	}
	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -1).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `SELECT n.id,n.title,substr(n.body,1,500),n.source,n.created_at,n.updated_at FROM notes n WHERE n.user_id=? AND n.updated_at<=? AND NOT EXISTS (SELECT 1 FROM bookmark_notes bn WHERE bn.note_id=n.id AND bn.user_id=n.user_id) AND NOT EXISTS (SELECT 1 FROM review_events re WHERE re.user_id=n.user_id AND re.item_type='note' AND re.item_id=n.id AND (re.created_at>=? OR (re.action='snoozed' AND re.snoozed_until>?))) ORDER BY n.updated_at ASC LIMIT ?`, userID, cutoff, cutoff, now.Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []map[string]any
	for rows.Next() {
		var id, title, body, source, created, updated string
		_ = rows.Scan(&id, &title, &body, &source, &created, &updated)
		notes = append(notes, map[string]any{"id": id, "item_type": "note", "title": fallback(title, "Untitled note"), "description": body, "source": source, "created_at": created, "updated_at": updated, "resurfacing_reason": "Review note"})
	}
	return notes, rows.Err()
}

func (s *Service) CompleteReview(w http.ResponseWriter, r *http.Request, user auth.User) {
	itemType, itemID, ok := splitReviewItem(r.PathValue("item_id"))
	if !ok || !s.reviewItemExists(r.Context(), user.ID, itemType, itemID) {
		writeError(w, http.StatusNotFound, "Review item not found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO review_events(id,user_id,item_type,item_id,action,created_at) VALUES(?,?,?,?,?,?)`, ids.New(), user.ID, itemType, itemID, "completed", now)
	if itemType == "bookmark" {
		_, _ = s.db.ExecContext(r.Context(), `UPDATE bookmarks SET read_status=1,last_accessed=?,view_count=view_count+1,updated_at=? WHERE id=? AND user_id=?`, now, now, itemID, user.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Review completed"})
}

func (s *Service) SnoozeReview(w http.ResponseWriter, r *http.Request, user auth.User) {
	itemType, itemID, ok := splitReviewItem(r.PathValue("item_id"))
	if !ok || !s.reviewItemExists(r.Context(), user.ID, itemType, itemID) {
		writeError(w, http.StatusNotFound, "Review item not found")
		return
	}
	var body struct {
		Days int `json:"days"`
	}
	_ = decodeJSON(r, &body)
	if body.Days <= 0 {
		body.Days = 7
	}
	if body.Days > 90 {
		writeError(w, http.StatusBadRequest, "days must be between 1 and 90")
		return
	}
	now := time.Now().UTC()
	until := now.AddDate(0, 0, body.Days).Format(time.RFC3339)
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO review_events(id,user_id,item_type,item_id,action,snoozed_until,created_at) VALUES(?,?,?,?,?,?,?)`, ids.New(), user.ID, itemType, itemID, "snoozed", until, now.Format(time.RFC3339))
	if itemType == "bookmark" {
		_, _ = s.db.ExecContext(r.Context(), `UPDATE bookmarks SET resurfacing_snoozed_until=?,updated_at=? WHERE id=? AND user_id=?`, until, now.Format(time.RFC3339), itemID, user.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Review snoozed", "snoozed_until": until})
}

func (s *Service) JobStatus(w http.ResponseWriter, r *http.Request, user auth.User) {
	var id, jobType, status, runAfter, created, updated string
	var attempts, maxAttempts int
	var leasedUntil, lastError sql.NullString
	err := s.db.QueryRowContext(r.Context(), `SELECT id,type,status,attempts,max_attempts,COALESCE(run_after,''),leased_until,last_error,created_at,updated_at FROM jobs WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID).Scan(&id, &jobType, &status, &attempts, &maxAttempts, &runAfter, &leasedUntil, &lastError, &created, &updated)
	if err != nil {
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "type": jobType, "status": status, "attempts": attempts, "max_attempts": maxAttempts, "run_after": runAfter, "leased_until": nullString(leasedUntil), "last_error": publicJobError(lastError.String), "created_at": created, "updated_at": updated})
}

func (s *Service) note(ctx context.Context, userID, id string) (map[string]any, error) {
	row := s.db.QueryRowContext(ctx, `SELECT n.id,n.title,n.body,n.source,n.created_at,n.updated_at,COALESCE(bn.bookmark_id,'') FROM notes n LEFT JOIN bookmark_notes bn ON bn.note_id=n.id AND bn.user_id=n.user_id WHERE n.id=? AND n.user_id=?`, id, userID)
	note := scanNote(row)
	if note["id"] == "" {
		return nil, sql.ErrNoRows
	}
	return note, nil
}

func scanNote(row scanner) map[string]any {
	var id, title, body, source, created, updated, bookmarkID string
	if err := row.Scan(&id, &title, &body, &source, &created, &updated, &bookmarkID); err != nil {
		return map[string]any{"id": ""}
	}
	result := map[string]any{"id": id, "title": title, "body": body, "source": source, "created_at": created, "updated_at": updated}
	if bookmarkID != "" {
		result["bookmark_id"] = bookmarkID
	}
	return result
}

const annotationSelect = `SELECT a.id,a.bookmark_id,a.quote,a.note,a.selector_json,a.tags_json,a.created_at,a.updated_at,COALESCE(a.evidence_id,''),
	CASE WHEN e.id IS NULL THEN 'unresolved' WHEN e.is_selected=1 THEN 'resolved' ELSE 'version_mismatch' END
	FROM annotations a LEFT JOIN bookmark_evidence e ON e.id=a.evidence_id AND e.bookmark_id=a.bookmark_id`

func (s *Service) annotation(ctx context.Context, userID, id string) (map[string]any, error) {
	return scanAnnotation(s.db.QueryRowContext(ctx, annotationSelect+` WHERE a.id=? AND a.user_id=?`, id, userID))
}

func scanAnnotation(row scanner) (map[string]any, error) {
	var id, bookmarkID, quote, note, selector, tags, created, updated, evidenceID, resolution string
	err := row.Scan(&id, &bookmarkID, &quote, &note, &selector, &tags, &created, &updated, &evidenceID, &resolution)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "bookmark_id": bookmarkID, "quote": quote, "note": note, "selector": jsonObjectValue(selector), "tags": jsonList(tags), "evidence_id": evidenceID, "resolution_state": resolution, "created_at": created, "updated_at": updated}, nil
}

func (s *Service) bookmarkAnnotations(ctx context.Context, userID, bookmarkID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, annotationSelect+` WHERE a.user_id=? AND a.bookmark_id=? ORDER BY a.created_at DESC LIMIT 100`, userID, bookmarkID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		if item, err := scanAnnotation(rows); err == nil {
			result = append(result, item)
		}
	}
	return result
}

func (s *Service) bookmarkNotes(ctx context.Context, userID, bookmarkID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT n.id,n.title,n.body,n.source,n.created_at,n.updated_at FROM notes n JOIN bookmark_notes bn ON bn.note_id=n.id AND bn.user_id=n.user_id WHERE bn.user_id=? AND bn.bookmark_id=? ORDER BY n.updated_at DESC LIMIT 100`, userID, bookmarkID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		var id, title, body, source, created, updated string
		_ = rows.Scan(&id, &title, &body, &source, &created, &updated)
		result = append(result, map[string]any{"id": id, "title": title, "body": body, "source": source, "bookmark_id": bookmarkID, "created_at": created, "updated_at": updated})
	}
	return result
}

func (s *Service) itemLinks(ctx context.Context, userID, itemType, itemID string) map[string]any {
	outgoing := s.links(ctx, userID, `from_type=? AND from_id=?`, itemType, itemID)
	incoming := s.links(ctx, userID, `to_type=? AND to_id=?`, itemType, itemID)
	return map[string]any{"outgoing": outgoing, "incoming": incoming}
}

func (s *Service) links(ctx context.Context, userID, predicate, itemType, itemID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,from_type,from_id,to_type,to_id,label,source,created_at FROM item_links WHERE user_id=? AND `+predicate+` ORDER BY created_at DESC LIMIT 100`, userID, itemType, itemID)
	if err != nil {
		return []map[string]any{}
	}
	result := []map[string]any{}
	for rows.Next() {
		result = append(result, scanLink(rows))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return []map[string]any{}
	}
	rows.Close()
	for _, link := range result {
		link["from_title"] = s.itemTitle(ctx, userID, stringValue(link["from_type"]), stringValue(link["from_id"]))
		link["to_title"] = s.itemTitle(ctx, userID, stringValue(link["to_type"]), stringValue(link["to_id"]))
	}
	return result
}

func (s *Service) link(ctx context.Context, userID, id string) (map[string]any, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,from_type,from_id,to_type,to_id,label,source,created_at FROM item_links WHERE id=? AND user_id=?`, id, userID)
	link := scanLink(row)
	if link["id"] == "" {
		return nil, sql.ErrNoRows
	}
	link["from_title"] = s.itemTitle(ctx, userID, stringValue(link["from_type"]), stringValue(link["from_id"]))
	link["to_title"] = s.itemTitle(ctx, userID, stringValue(link["to_type"]), stringValue(link["to_id"]))
	return link, nil
}

func (s *Service) createItemLink(ctx context.Context, userID, fromType, fromID, toType, toID, label, source, now string) (map[string]any, error) {
	id := ids.New()
	_, err := s.db.ExecContext(ctx, `INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, userID, fromType, fromID, toType, toID, label, source, now)
	if err != nil {
		return nil, err
	}
	return s.link(ctx, userID, id)
}

func scanLink(row scanner) map[string]any {
	var id, fromType, fromID, toType, toID, label, source, created string
	if err := row.Scan(&id, &fromType, &fromID, &toType, &toID, &label, &source, &created); err != nil {
		return map[string]any{"id": ""}
	}
	return map[string]any{"id": id, "from_type": fromType, "from_id": fromID, "to_type": toType, "to_id": toID, "label": label, "source": source, "created_at": created}
}

func (s *Service) bookmarkTags(ctx context.Context, userID, bookmarkID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id,t.name,t.slug,bt.source,bt.created_at FROM tags t JOIN bookmark_tags bt ON bt.tag_id=t.id AND bt.user_id=t.user_id WHERE bt.user_id=? AND bt.bookmark_id=? ORDER BY t.name COLLATE NOCASE`, userID, bookmarkID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		result = append(result, scanBookmarkTag(rows))
	}
	return result
}

func scanBookmarkTag(row scanner, extra ...any) map[string]any {
	var id, name, slug, source, created string
	_ = row.Scan(append([]any{&id, &name, &slug, &source, &created}, extra...)...)
	return map[string]any{"id": id, "name": name, "slug": slug, "source": source, "created_at": created}
}

func (s *Service) upsertTag(ctx context.Context, userID, name, source string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	slug := tagSlug(name)
	if slug == "" {
		return nil, errInvalid("tag name is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	_, err := s.db.ExecContext(ctx, `INSERT INTO tags(id,user_id,name,slug,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,slug) DO UPDATE SET updated_at=excluded.updated_at`, id, userID, name, slug, source, now, now)
	if err != nil {
		return nil, err
	}
	var tagID, tagName, tagSlugValue, tagSource, created, updated string
	_ = s.db.QueryRowContext(ctx, `SELECT id,name,slug,source,created_at,updated_at FROM tags WHERE user_id=? AND slug=?`, userID, slug).Scan(&tagID, &tagName, &tagSlugValue, &tagSource, &created, &updated)
	return map[string]any{"id": tagID, "name": tagName, "slug": tagSlugValue, "source": tagSource, "created_at": created, "updated_at": updated}, nil
}

func (s *Service) attachTag(ctx context.Context, userID, bookmarkID, name, source string) error {
	tag, err := s.upsertTag(ctx, userID, name, source)
	if err != nil {
		return err
	}
	tagID, _ := tag["id"].(string)
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_tags(bookmark_id,tag_id,user_id,source,created_at) VALUES(?,?,?,?,?)`, bookmarkID, tagID, userID, source, time.Now().UTC().Format(time.RFC3339))
	return err
}

func jsonObject(value any) (string, bool) {
	if value == nil {
		return "{}", true
	}
	if _, ok := value.(map[string]any); !ok {
		return "", false
	}
	raw, err := json.Marshal(value)
	return string(raw), err == nil && len(raw) <= 20_000
}

func jsonObjectValue(raw string) map[string]any {
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return map[string]any{}
	}
	return value
}

func cleanStringList(values []string, limit int) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func tagSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out []rune
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out = append(out, r)
			lastDash = false
			continue
		}
		if !lastDash && len(out) > 0 {
			out = append(out, '-')
			lastDash = true
		}
	}
	return strings.Trim(string(out), "-")
}

func splitReviewItem(raw string) (string, string, bool) {
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) == 2 && (parts[0] == "bookmark" || parts[0] == "note") && parts[1] != "" {
		return parts[0], parts[1], true
	}
	if raw != "" {
		return "bookmark", raw, true
	}
	return "", "", false
}

func (s *Service) reviewItemExists(ctx context.Context, userID, itemType, itemID string) bool {
	var exists int
	switch itemType {
	case "bookmark":
		_ = s.db.QueryRowContext(ctx, `SELECT 1 FROM bookmarks WHERE id=? AND user_id=?`, itemID, userID).Scan(&exists)
	case "note":
		_ = s.db.QueryRowContext(ctx, `SELECT 1 FROM notes WHERE id=? AND user_id=?`, itemID, userID).Scan(&exists)
	}
	return exists == 1
}

func (s *Service) recentReviewEvent(ctx context.Context, userID, itemType, itemID string) bool {
	cutoff := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	var exists int
	_ = s.db.QueryRowContext(ctx, `SELECT 1 FROM review_events WHERE user_id=? AND item_type=? AND item_id=? AND created_at>=? LIMIT 1`, userID, itemType, itemID, cutoff).Scan(&exists)
	return exists == 1
}

func publicJobError(message string) any {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	return "Job failed. Check server logs for details."
}

type errInvalid string

func (e errInvalid) Error() string { return string(e) }
