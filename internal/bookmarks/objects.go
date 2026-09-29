package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
)

var defaultObjectTypes = []string{"project", "person", "book", "meeting", "decision", "research_thread"}

type objectRequest struct {
	ObjectType     string `json:"object_type"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Fields         any    `json:"fields"`
	SourceItemType string `json:"source_item_type"`
	SourceItemID   string `json:"source_item_id"`
}

func (s *Service) Objects(w http.ResponseWriter, r *http.Request, user auth.User) {
	objectType := normalizeObjectType(r.URL.Query().Get("type"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := queryInt(r, "limit", 100, 1, 200)
	sqlQuery := `SELECT id,object_type,title,description,fields_json,source_item_type,source_item_id,created_at,updated_at FROM knowledge_objects WHERE user_id=?`
	args := []any{user.ID}
	if objectType != "" {
		sqlQuery += ` AND object_type=?`
		args = append(args, objectType)
	}
	if query != "" {
		sqlQuery += ` AND (title LIKE ? OR description LIKE ? OR fields_json LIKE ?)`
		like := "%" + query + "%"
		args = append(args, like, like, like)
	}
	sqlQuery += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(r.Context(), sqlQuery, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load objects")
		return
	}
	objects, err := scanObjects(rows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load objects")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": objects, "object_types": defaultObjectTypes})
}

func (s *Service) CreateObject(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body objectRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	object, err := s.createObject(r.Context(), user.ID, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": object})
}

func (s *Service) GetObject(w http.ResponseWriter, r *http.Request, user auth.User) {
	object, err := s.object(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Object not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": object})
}

func (s *Service) AgentRecordDecision(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Title          string `json:"title"`
		Decision       string `json:"decision"`
		Rationale      string `json:"rationale"`
		SourceItemType string `json:"source_item_type"`
		SourceItemID   string `json:"source_item_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	fields := map[string]any{
		"decision":  strings.TrimSpace(body.Decision),
		"rationale": strings.TrimSpace(body.Rationale),
	}
	object, err := s.createObject(r.Context(), user.ID, objectRequest{
		ObjectType:     "decision",
		Title:          fallback(body.Title, body.Decision),
		Description:    firstNonEmpty(body.Decision, body.Rationale),
		Fields:         fields,
		SourceItemType: body.SourceItemType,
		SourceItemID:   body.SourceItemID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": object})
}

func (s *Service) CalendarImport(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		ICS    string `json:"ics"`
		Source string `json:"source"`
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/calendar") || strings.HasPrefix(contentType, "text/plain") {
		raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if err != nil {
			writeError(w, http.StatusBadRequest, "Could not read calendar import")
			return
		}
		if len(raw) > 1<<20 {
			writeError(w, http.StatusBadRequest, "Calendar import is too large")
			return
		}
		body.ICS = string(raw)
	} else if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	events := parseICSEvents(body.ICS)
	if len(events) == 0 {
		writeError(w, http.StatusBadRequest, "No calendar events found")
		return
	}
	objects := []map[string]any{}
	for _, event := range events {
		if len(objects) >= 50 {
			break
		}
		title := fallback(stringValue(event["summary"]), "Imported meeting")
		description := firstNonEmpty(stringValue(event["description"]), stringValue(event["location"]), stringValue(event["uid"]))
		event["source"] = strings.TrimSpace(body.Source)
		object, err := s.createObject(r.Context(), user.ID, objectRequest{ObjectType: "meeting", Title: title, Description: description, Fields: event})
		if err == nil {
			objects = append(objects, object)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(objects), "objects": objects})
}

func (s *Service) Evolution(w http.ResponseWriter, r *http.Request, user auth.User) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "Query is required")
		return
	}
	limit := queryInt(r, "limit", 80, 1, 200)
	items := []map[string]any{}
	items = append(items, s.evolutionNotes(r.Context(), user.ID, query, limit)...)
	items = append(items, s.evolutionBookmarks(r.Context(), user.ID, query, limit)...)
	items = append(items, s.evolutionDailyNotes(r.Context(), user.ID, query, limit)...)
	items = append(items, s.evolutionObjects(r.Context(), user.ID, query, limit)...)
	sort.SliceStable(items, func(i, j int) bool {
		return stringValue(items[i]["updated_at"]) > stringValue(items[j]["updated_at"])
	})
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": query, "timeline": items})
}

func (s *Service) TodayBoard(w http.ResponseWriter, r *http.Request, user auth.User) {
	inbox, _ := s.inboxItems(r.Context(), user.ID, "inbox", 8)
	working, _ := s.inboxItems(r.Context(), user.ID, "processing", 8)
	review, _ := s.reviewItems(r.Context(), user.ID, 8)
	decisions := s.recentObjects(r.Context(), user.ID, "decision", 8)
	meetings := s.recentObjects(r.Context(), user.ID, "meeting", 8)
	writeJSON(w, http.StatusOK, map[string]any{
		"columns": []map[string]any{
			{"id": "inbox", "title": "Inbox", "items": inbox},
			{"id": "working", "title": "Working", "items": working},
			{"id": "review", "title": "Review", "items": review},
			{"id": "decisions", "title": "Decisions", "items": decisions},
			{"id": "meetings", "title": "Meetings", "items": meetings},
		},
	})
}

func (s *Service) createObject(ctx context.Context, userID string, body objectRequest) (map[string]any, error) {
	objectType := normalizeObjectType(body.ObjectType)
	if objectType == "" {
		return nil, fmt.Errorf("Object type is required")
	}
	title := strings.TrimSpace(body.Title)
	description := strings.TrimSpace(body.Description)
	if title == "" && description == "" {
		return nil, fmt.Errorf("Title or description is required")
	}
	fields, ok := jsonObject(body.Fields)
	if !ok {
		return nil, fmt.Errorf("fields must be an object")
	}
	sourceType := normalizeSourceItemType(body.SourceItemType)
	sourceID := strings.TrimSpace(body.SourceItemID)
	if sourceType == "" {
		sourceID = ""
	}
	if sourceType != "" && !s.sourceItemExists(ctx, userID, sourceType, sourceID) {
		return nil, fmt.Errorf("Source item not found")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO knowledge_objects(id,user_id,object_type,title,description,fields_json,source_item_type,source_item_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, userID, objectType, title, description, fields, sourceType, sourceID, now, now); err != nil {
		return nil, fmt.Errorf("Could not create object")
	}
	return s.object(ctx, userID, id)
}

func (s *Service) object(ctx context.Context, userID, id string) (map[string]any, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,object_type,title,description,fields_json,source_item_type,source_item_id,created_at,updated_at FROM knowledge_objects WHERE id=? AND user_id=?`, id, userID)
	return scanObject(row.Scan)
}

func scanObjects(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	objects := []map[string]any{}
	for rows.Next() {
		object, err := scanObject(rows.Scan)
		if err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return objects, nil
}

func scanObject(scan func(...any) error) (map[string]any, error) {
	var id, objectType, title, description, fields, sourceType, sourceID, created, updated string
	if err := scan(&id, &objectType, &title, &description, &fields, &sourceType, &sourceID, &created, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "object_type": objectType, "title": title, "description": description, "fields": jsonObjectValue(fields), "source_item_type": sourceType, "source_item_id": sourceID, "created_at": created, "updated_at": updated}, nil
}

func normalizeObjectType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	if value == "" {
		return ""
	}
	if value == "research" || value == "thread" {
		return "research_thread"
	}
	for _, allowed := range defaultObjectTypes {
		if value == allowed {
			return value
		}
	}
	return safeMediaKind(value)
}

func normalizeSourceItemType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none":
		return ""
	case "bookmark", "note", "object":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func (s *Service) sourceItemExists(ctx context.Context, userID, itemType, itemID string) bool {
	if itemID == "" {
		return false
	}
	switch itemType {
	case "bookmark":
		return s.bookmarkExists(ctx, userID, itemID)
	case "note":
		var count int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes WHERE user_id=? AND id=?`, userID, itemID).Scan(&count)
		return count > 0
	case "object":
		var count int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_objects WHERE user_id=? AND id=?`, userID, itemID).Scan(&count)
		return count > 0
	default:
		return false
	}
}

func parseICSEvents(raw string) []map[string]any {
	lines := unfoldICSLines(raw)
	events := []map[string]any{}
	var current map[string]any
	for _, line := range lines {
		name, value, ok := icsNameValue(line)
		if !ok {
			continue
		}
		switch name {
		case "BEGIN":
			if strings.EqualFold(value, "VEVENT") {
				current = map[string]any{}
			}
		case "END":
			if strings.EqualFold(value, "VEVENT") && current != nil {
				if len(current) > 0 {
					events = append(events, current)
				}
				current = nil
			}
		case "SUMMARY", "DESCRIPTION", "LOCATION", "UID":
			if current != nil {
				current[strings.ToLower(name)] = cleanICSValue(value)
			}
		case "DTSTART":
			if current != nil {
				current["starts_at"] = cleanICSValue(value)
			}
		case "DTEND":
			if current != nil {
				current["ends_at"] = cleanICSValue(value)
			}
		}
	}
	return events
}

func unfoldICSLines(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	out := []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if len(out) > 0 {
				out[len(out)-1] += strings.TrimLeft(line, " \t")
			}
			continue
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

func icsNameValue(line string) (string, string, bool) {
	if line == "" {
		return "", "", false
	}
	index := strings.Index(line, ":")
	if index < 0 {
		return "", "", false
	}
	name := strings.ToUpper(strings.Split(line[:index], ";")[0])
	return name, line[index+1:], true
}

func cleanICSValue(value string) string {
	value = strings.ReplaceAll(value, `\n`, "\n")
	value = strings.ReplaceAll(value, `\,`, ",")
	value = strings.ReplaceAll(value, `\;`, ";")
	return strings.TrimSpace(value)
}

func (s *Service) evolutionNotes(ctx context.Context, userID, query string, limit int) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,substr(body,1,600),source,updated_at FROM notes WHERE user_id=? AND (title LIKE ? OR body LIKE ?) ORDER BY updated_at DESC LIMIT ?`, userID, "%"+query+"%", "%"+query+"%", limit)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title, body, source, updated string
		_ = rows.Scan(&id, &title, &body, &source, &updated)
		items = append(items, map[string]any{"item_type": "note", "id": id, "title": fallback(title, "Untitled note"), "body": body, "source": source, "updated_at": updated, "href": "/notes/" + id})
	}
	return items
}

func (s *Service) evolutionBookmarks(ctx context.Context, userID, query string, limit int) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,url,COALESCE(description,''),source,updated_at FROM bookmarks WHERE user_id=? AND (title LIKE ? OR description LIKE ? OR text_content LIKE ?) ORDER BY updated_at DESC LIMIT ?`, userID, "%"+query+"%", "%"+query+"%", "%"+query+"%", limit)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title, url, description, source, updated string
		_ = rows.Scan(&id, &title, &url, &description, &source, &updated)
		items = append(items, map[string]any{"item_type": "bookmark", "id": id, "title": fallback(title, url), "body": description, "source": source, "updated_at": updated, "href": "/bookmark/" + id})
	}
	return items
}

func (s *Service) evolutionDailyNotes(ctx context.Context, userID, query string, limit int) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT note_date,substr(body,1,600),updated_at FROM daily_notes WHERE user_id=? AND body LIKE ? ORDER BY updated_at DESC LIMIT ?`, userID, "%"+query+"%", limit)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var date, body, updated string
		_ = rows.Scan(&date, &body, &updated)
		items = append(items, map[string]any{"item_type": "daily_note", "id": date, "title": "Daily note " + date, "body": body, "source": "daily", "updated_at": updated, "href": "/today"})
	}
	return items
}

func (s *Service) evolutionObjects(ctx context.Context, userID, query string, limit int) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,object_type,title,description,fields_json,updated_at FROM knowledge_objects WHERE user_id=? AND (title LIKE ? OR description LIKE ? OR fields_json LIKE ?) ORDER BY updated_at DESC LIMIT ?`, userID, "%"+query+"%", "%"+query+"%", "%"+query+"%", limit)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, objectType, title, description, fields, updated string
		_ = rows.Scan(&id, &objectType, &title, &description, &fields, &updated)
		items = append(items, map[string]any{"item_type": "object", "object_type": objectType, "id": id, "title": fallback(title, objectType), "body": firstNonEmpty(description, fields), "source": "object", "updated_at": updated, "href": "/objects"})
	}
	return items
}

func (s *Service) recentObjects(ctx context.Context, userID, objectType string, limit int) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,object_type,title,description,fields_json,source_item_type,source_item_id,created_at,updated_at FROM knowledge_objects WHERE user_id=? AND object_type=? ORDER BY updated_at DESC LIMIT ?`, userID, objectType, limit)
	if err != nil {
		return []map[string]any{}
	}
	objects, err := scanObjects(rows)
	if err != nil {
		return []map[string]any{}
	}
	return objects
}
