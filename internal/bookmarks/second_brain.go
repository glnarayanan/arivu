package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
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
	defer rows.Close()
	var notes []map[string]any
	for rows.Next() {
		notes = append(notes, scanNote(rows))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
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
	if body.BookmarkID != "" && !s.bookmarkExists(r.Context(), user.ID, body.BookmarkID) {
		writeError(w, http.StatusNotFound, "Bookmark not found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, user.ID, title, text, "manual", now, now); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create note")
		return
	}
	_ = s.upsertItemState(r.Context(), user.ID, "note", id, "inbox", 0, "", now)
	if body.BookmarkID != "" {
		_, _ = s.db.ExecContext(r.Context(), `INSERT INTO bookmark_notes(bookmark_id,note_id,user_id,created_at) VALUES(?,?,?,?)`, body.BookmarkID, id, user.ID, now)
	}
	note, _ := s.note(r.Context(), user.ID, id)
	writeJSON(w, http.StatusOK, map[string]any{"note": note})
}

func (s *Service) GetNote(w http.ResponseWriter, r *http.Request, user auth.User) {
	note, err := s.note(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Note not found")
		return
	}
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
	writeJSON(w, http.StatusOK, map[string]any{"note": note})
}

func (s *Service) DeleteNote(w http.ResponseWriter, r *http.Request, user auth.User) {
	res, _ := s.db.ExecContext(r.Context(), `DELETE FROM notes WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "Note not found")
		return
	}
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
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO annotations(id,user_id,bookmark_id,quote,note,selector_json,tags_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, user.ID, bookmarkID, quote, note, selector, string(tagJSON), now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create annotation")
		return
	}
	for _, tag := range tags {
		_ = s.attachTag(r.Context(), user.ID, bookmarkID, tag, "manual")
	}
	annotation, _ := s.annotation(r.Context(), user.ID, id)
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
	writeJSON(w, http.StatusOK, map[string]any{"annotation": annotation})
}

func (s *Service) DeleteAnnotation(w http.ResponseWriter, r *http.Request, user auth.User) {
	res, _ := s.db.ExecContext(r.Context(), `DELETE FROM annotations WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "Annotation not found")
		return
	}
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
	candidates, err := s.resurfacingCandidates(r.Context(), user.ID, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load review queue")
		return
	}
	var items []map[string]any
	for _, candidate := range candidates {
		if len(items) >= limit {
			break
		}
		id, _ := candidate.Bookmark["id"].(string)
		if s.recentReviewEvent(r.Context(), user.ID, "bookmark", id) {
			continue
		}
		item := cloneMap(candidate.Bookmark)
		item["item_type"] = "bookmark"
		item["item_state"] = s.itemState(r.Context(), user.ID, "bookmark", id)
		items = append(items, item)
	}
	if len(items) < limit {
		notes, err := s.reviewNotes(r.Context(), user.ID, limit-len(items))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load review queue")
			return
		}
		items = append(items, notes...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) Inbox(w http.ResponseWriter, r *http.Request, user auth.User) {
	stage := strings.TrimSpace(r.URL.Query().Get("stage"))
	if stage == "" {
		stage = "inbox"
	}
	if !validItemStage(stage) {
		writeError(w, http.StatusBadRequest, "Invalid inbox stage")
		return
	}
	limit := queryInt(r, "limit", 100, 1, 200)
	items, err := s.inboxItems(r.Context(), user.ID, stage, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load inbox")
		return
	}
	counts := s.inboxCounts(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "counts": counts, "stage": stage})
}

func (s *Service) UpdateInboxItem(w http.ResponseWriter, r *http.Request, user auth.User) {
	itemType, itemID, ok := splitReviewItem(r.PathValue("item_id"))
	if !ok || !s.reviewItemExists(r.Context(), user.ID, itemType, itemID) {
		writeError(w, http.StatusNotFound, "Inbox item not found")
		return
	}
	var body struct {
		Stage      string `json:"stage"`
		Importance int    `json:"importance"`
		NextAction string `json:"next_action"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	stage := strings.TrimSpace(body.Stage)
	if stage == "" {
		stage = "processing"
	}
	if !validItemStage(stage) {
		writeError(w, http.StatusBadRequest, "Invalid inbox stage")
		return
	}
	if body.Importance < 0 || body.Importance > 5 {
		writeError(w, http.StatusBadRequest, "importance must be between 0 and 5")
		return
	}
	nextAction := strings.TrimSpace(body.NextAction)
	if len(nextAction) > 500 {
		writeError(w, http.StatusBadRequest, "next_action is too large")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.upsertItemState(r.Context(), user.ID, itemType, itemID, stage, body.Importance, nextAction, now); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not update inbox item")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item_type": itemType, "item_id": itemID, "stage": stage, "importance": body.Importance, "next_action": nextAction, "updated_at": now})
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
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, user.ID, body.FromType, body.FromID, body.ToType, body.ToID, label, "manual", now)
	if err != nil {
		writeError(w, http.StatusConflict, "Link already exists")
		return
	}
	link, _ := s.link(r.Context(), user.ID, id)
	writeJSON(w, http.StatusOK, map[string]any{"link": link})
}

func (s *Service) DeleteLink(w http.ResponseWriter, r *http.Request, user auth.User) {
	res, _ := s.db.ExecContext(r.Context(), `DELETE FROM item_links WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if rows, _ := res.RowsAffected(); rows == 0 {
		writeError(w, http.StatusNotFound, "Link not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Link deleted"})
}

func (s *Service) Reminders(w http.ResponseWriter, r *http.Request, user auth.User) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "completed" && status != "all" {
		writeError(w, http.StatusBadRequest, "Invalid reminder status")
		return
	}
	where := "user_id=?"
	args := []any{user.ID}
	if status != "all" {
		where += " AND status=?"
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,item_type,item_id,due_at,note,status,created_at,COALESCE(completed_at,'') FROM reminders WHERE `+where+` ORDER BY due_at ASC LIMIT 200`, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load reminders")
		return
	}
	reminders, err := s.scanReminders(r.Context(), user.ID, rows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load reminders")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminders": reminders})
}

func (s *Service) CreateReminder(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		ItemType string `json:"item_type"`
		ItemID   string `json:"item_id"`
		DueAt    string `json:"due_at"`
		Note     string `json:"note"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	body.ItemType = strings.TrimSpace(body.ItemType)
	body.ItemID = strings.TrimSpace(body.ItemID)
	if !s.reviewItemExists(r.Context(), user.ID, body.ItemType, body.ItemID) {
		writeError(w, http.StatusNotFound, "Reminder item not found")
		return
	}
	due, err := time.Parse(time.RFC3339, strings.TrimSpace(body.DueAt))
	if err != nil {
		writeError(w, http.StatusBadRequest, "due_at must be RFC3339")
		return
	}
	note := strings.TrimSpace(body.Note)
	if len(note) > 500 {
		writeError(w, http.StatusBadRequest, "note is too large")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO reminders(id,user_id,item_type,item_id,due_at,note,status,created_at) VALUES(?,?,?,?,?,?,?,?)`, id, user.ID, body.ItemType, body.ItemID, due.UTC().Format(time.RFC3339), note, "pending", now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create reminder")
		return
	}
	reminder, _ := s.reminder(r.Context(), user.ID, id)
	writeJSON(w, http.StatusOK, map[string]any{"reminder": reminder})
}

func (s *Service) CompleteReminder(w http.ResponseWriter, r *http.Request, user auth.User) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, _ := s.db.ExecContext(r.Context(), `UPDATE reminders SET status='completed',completed_at=? WHERE id=? AND user_id=?`, now, r.PathValue("id"), user.ID)
	if rows, _ := res.RowsAffected(); rows == 0 {
		writeError(w, http.StatusNotFound, "Reminder not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Reminder completed", "completed_at": now})
}

func (s *Service) DeleteReminder(w http.ResponseWriter, r *http.Request, user auth.User) {
	res, _ := s.db.ExecContext(r.Context(), `DELETE FROM reminders WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if rows, _ := res.RowsAffected(); rows == 0 {
		writeError(w, http.StatusNotFound, "Reminder not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Reminder deleted"})
}

func (s *Service) inboxItems(ctx context.Context, userID, stage string, limit int) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT 'bookmark',b.id,b.title,b.url,COALESCE(b.description,''),b.source,b.domain,COALESCE(st.stage,'inbox'),COALESCE(st.importance,0),COALESCE(st.next_action,''),b.created_at,b.updated_at
		FROM bookmarks b
		LEFT JOIN item_states st ON st.user_id=b.user_id AND st.item_type='bookmark' AND st.item_id=b.id
		WHERE b.user_id=? AND COALESCE(st.stage,'inbox')=?
		UNION ALL
		SELECT 'note',n.id,n.title,'',substr(n.body,1,500),n.source,'',COALESCE(st.stage,'inbox'),COALESCE(st.importance,0),COALESCE(st.next_action,''),n.created_at,n.updated_at
		FROM notes n
		LEFT JOIN item_states st ON st.user_id=n.user_id AND st.item_type='note' AND st.item_id=n.id
		WHERE n.user_id=? AND COALESCE(st.stage,'inbox')=?
		ORDER BY 12 DESC
		LIMIT ?`, userID, stage, userID, stage, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var itemType, id, title, url, description, source, domain, stateStage, nextAction, created, updated string
		var importance int
		_ = rows.Scan(&itemType, &id, &title, &url, &description, &source, &domain, &stateStage, &importance, &nextAction, &created, &updated)
		items = append(items, map[string]any{"item_type": itemType, "id": id, "title": fallback(title, "Untitled"), "url": url, "description": description, "source": source, "domain": domain, "stage": stateStage, "importance": importance, "next_action": nextAction, "created_at": created, "updated_at": updated})
	}
	return items, rows.Err()
}

func (s *Service) inboxCounts(ctx context.Context, userID string) map[string]int {
	counts := map[string]int{"inbox": 0, "processing": 0, "processed": 0, "archived": 0}
	for _, stage := range []string{"inbox", "processing", "processed", "archived"} {
		var bookmarks, notes int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks b LEFT JOIN item_states st ON st.user_id=b.user_id AND st.item_type='bookmark' AND st.item_id=b.id WHERE b.user_id=? AND COALESCE(st.stage,'inbox')=?`, userID, stage).Scan(&bookmarks)
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes n LEFT JOIN item_states st ON st.user_id=n.user_id AND st.item_type='note' AND st.item_id=n.id WHERE n.user_id=? AND COALESCE(st.stage,'inbox')=?`, userID, stage).Scan(&notes)
		counts[stage] = bookmarks + notes
	}
	return counts
}

func (s *Service) upsertItemState(ctx context.Context, userID, itemType, itemID, stage string, importance int, nextAction, now string) error {
	if !validItemStage(stage) || (itemType != "bookmark" && itemType != "note") {
		return errInvalid("invalid item state")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO item_states(user_id,item_type,item_id,stage,importance,next_action,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(user_id,item_type,item_id) DO UPDATE SET stage=excluded.stage,importance=excluded.importance,next_action=excluded.next_action,updated_at=excluded.updated_at`, userID, itemType, itemID, stage, importance, nextAction, now, now)
	return err
}

func (s *Service) setItemStage(ctx context.Context, userID, itemType, itemID, stage, now string) error {
	if !validItemStage(stage) || (itemType != "bookmark" && itemType != "note") {
		return errInvalid("invalid item state")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE item_states SET stage=?,updated_at=? WHERE user_id=? AND item_type=? AND item_id=?`, stage, now, userID, itemType, itemID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows > 0 {
		return nil
	}
	return s.upsertItemState(ctx, userID, itemType, itemID, stage, 0, "", now)
}

func (s *Service) itemState(ctx context.Context, userID, itemType, itemID string) map[string]any {
	state := map[string]any{"stage": "inbox", "importance": 0, "next_action": ""}
	var stage, nextAction, created, updated string
	var importance int
	err := s.db.QueryRowContext(ctx, `SELECT stage,importance,next_action,created_at,updated_at FROM item_states WHERE user_id=? AND item_type=? AND item_id=?`, userID, itemType, itemID).Scan(&stage, &importance, &nextAction, &created, &updated)
	if err != nil {
		return state
	}
	state["stage"] = stage
	state["importance"] = importance
	state["next_action"] = nextAction
	state["created_at"] = created
	state["updated_at"] = updated
	return state
}

func validItemStage(stage string) bool {
	return stage == "inbox" || stage == "processing" || stage == "processed" || stage == "archived"
}

func (s *Service) reviewNotes(ctx context.Context, userID string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		return []map[string]any{}, nil
	}
	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -1).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `SELECT n.id,n.title,substr(n.body,1,500),n.source,n.created_at,n.updated_at,COALESCE(st.stage,'inbox'),COALESCE(st.importance,0),COALESCE(st.next_action,'') FROM notes n LEFT JOIN item_states st ON st.user_id=n.user_id AND st.item_type='note' AND st.item_id=n.id WHERE n.user_id=? AND n.updated_at<=? AND NOT EXISTS (SELECT 1 FROM bookmark_notes bn WHERE bn.note_id=n.id AND bn.user_id=n.user_id) AND NOT EXISTS (SELECT 1 FROM review_events re WHERE re.user_id=n.user_id AND re.item_type='note' AND re.item_id=n.id AND (re.created_at>=? OR (re.action='snoozed' AND re.snoozed_until>?))) ORDER BY n.updated_at ASC LIMIT ?`, userID, cutoff, cutoff, now.Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []map[string]any
	for rows.Next() {
		var id, title, body, source, created, updated, stage, nextAction string
		var importance int
		_ = rows.Scan(&id, &title, &body, &source, &created, &updated, &stage, &importance, &nextAction)
		notes = append(notes, map[string]any{"id": id, "item_type": "note", "title": fallback(title, "Untitled note"), "description": body, "source": source, "created_at": created, "updated_at": updated, "resurfacing_reason": "Review note", "item_state": map[string]any{"stage": stage, "importance": importance, "next_action": nextAction}})
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
	_ = s.setItemStage(r.Context(), user.ID, itemType, itemID, "processed", now)
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

func (s *Service) annotation(ctx context.Context, userID, id string) (map[string]any, error) {
	var bookmarkID, quote, note, selector, tags, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT bookmark_id,quote,note,selector_json,tags_json,created_at,updated_at FROM annotations WHERE id=? AND user_id=?`, id, userID).Scan(&bookmarkID, &quote, &note, &selector, &tags, &created, &updated)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "bookmark_id": bookmarkID, "quote": quote, "note": note, "selector": jsonObjectValue(selector), "tags": jsonList(tags), "created_at": created, "updated_at": updated}, nil
}

func (s *Service) bookmarkAnnotations(ctx context.Context, userID, bookmarkID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,quote,note,selector_json,tags_json,created_at,updated_at FROM annotations WHERE user_id=? AND bookmark_id=? ORDER BY created_at DESC LIMIT 100`, userID, bookmarkID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		var id, quote, note, selector, tags, created, updated string
		_ = rows.Scan(&id, &quote, &note, &selector, &tags, &created, &updated)
		result = append(result, map[string]any{"id": id, "bookmark_id": bookmarkID, "quote": quote, "note": note, "selector": jsonObjectValue(selector), "tags": jsonList(tags), "created_at": created, "updated_at": updated})
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

func scanLink(row scanner) map[string]any {
	var id, fromType, fromID, toType, toID, label, source, created string
	if err := row.Scan(&id, &fromType, &fromID, &toType, &toID, &label, &source, &created); err != nil {
		return map[string]any{"id": ""}
	}
	return map[string]any{"id": id, "from_type": fromType, "from_id": fromID, "to_type": toType, "to_id": toID, "label": label, "source": source, "created_at": created}
}

func (s *Service) reminder(ctx context.Context, userID, id string) (map[string]any, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,item_type,item_id,due_at,note,status,created_at,COALESCE(completed_at,'') FROM reminders WHERE id=? AND user_id=?`, id, userID)
	reminder := scanReminder(row)
	if reminder["id"] == "" {
		return nil, sql.ErrNoRows
	}
	reminder["item_title"] = s.itemTitle(ctx, userID, stringValue(reminder["item_type"]), stringValue(reminder["item_id"]))
	return reminder, nil
}

func (s *Service) itemReminders(ctx context.Context, userID, itemType, itemID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,item_type,item_id,due_at,note,status,created_at,COALESCE(completed_at,'') FROM reminders WHERE user_id=? AND item_type=? AND item_id=? ORDER BY due_at ASC LIMIT 50`, userID, itemType, itemID)
	if err != nil {
		return []map[string]any{}
	}
	reminders, err := s.scanReminders(ctx, userID, rows)
	if err != nil {
		return []map[string]any{}
	}
	return reminders
}

func (s *Service) scanReminders(ctx context.Context, userID string, rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	reminders := []map[string]any{}
	for rows.Next() {
		reminders = append(reminders, scanReminder(rows))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, reminder := range reminders {
		reminder["item_title"] = s.itemTitle(ctx, userID, stringValue(reminder["item_type"]), stringValue(reminder["item_id"]))
	}
	return reminders, nil
}

func scanReminder(row scanner) map[string]any {
	var id, itemType, itemID, dueAt, note, status, created, completed string
	if err := row.Scan(&id, &itemType, &itemID, &dueAt, &note, &status, &created, &completed); err != nil {
		return map[string]any{"id": ""}
	}
	return map[string]any{"id": id, "item_type": itemType, "item_id": itemID, "due_at": dueAt, "note": note, "status": status, "created_at": created, "completed_at": completed}
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

func (s *Service) bookmarkTags(ctx context.Context, userID, bookmarkID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id,t.name,t.slug,bt.source,bt.created_at FROM tags t JOIN bookmark_tags bt ON bt.tag_id=t.id AND bt.user_id=t.user_id WHERE bt.user_id=? AND bt.bookmark_id=? ORDER BY t.name COLLATE NOCASE`, userID, bookmarkID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		var id, name, slug, source, created string
		_ = rows.Scan(&id, &name, &slug, &source, &created)
		result = append(result, map[string]any{"id": id, "name": name, "slug": slug, "source": source, "created_at": created})
	}
	return result
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
