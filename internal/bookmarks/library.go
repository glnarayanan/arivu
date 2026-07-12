package bookmarks

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/glnarayanan/arivu/internal/auth"
)

type libraryCursor struct {
	UpdatedAt string `json:"updated_at"`
	Type      string `json:"type"`
	ID        string `json:"id"`
}

func (s *Service) LibraryItems(w http.ResponseWriter, r *http.Request, user auth.User) {
	limit := queryInt(r, "limit", 50, 1, 200)
	where := []string{"user_id=?"}
	args := []any{user.ID}
	for _, filter := range []struct {
		key, column string
	}{{"type", "item_type"}, {"source", "source"}, {"stage", "stage"}, {"connection", "connection_state"}} {
		if value := strings.TrimSpace(r.URL.Query().Get(filter.key)); value != "" {
			where = append(where, filter.column+"=?")
			args = append(args, value)
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("q")); value != "" {
		where = append(where, "(title LIKE ? OR body LIKE ? OR topic LIKE ?)")
		like := "%" + value + "%"
		args = append(args, like, like, like)
	}
	if value := strings.TrimSpace(r.URL.Query().Get("topic")); value != "" {
		where = append(where, "topic LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(r.URL.Query().Get("date_from")); value != "" {
		where = append(where, "updated_at>=?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(r.URL.Query().Get("date_to")); value != "" {
		where = append(where, "updated_at<=?")
		args = append(args, value+"T23:59:59Z")
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		cursor, ok := decodeLibraryCursor(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "Invalid cursor")
			return
		}
		where = append(where, "(updated_at<? OR (updated_at=? AND item_type>?) OR (updated_at=? AND item_type=? AND id>?))")
		args = append(args, cursor.UpdatedAt, cursor.UpdatedAt, cursor.Type, cursor.UpdatedAt, cursor.Type, cursor.ID)
	}

	query := libraryUnion + " WHERE " + strings.Join(where, " AND ") + " ORDER BY updated_at DESC,item_type ASC,id ASC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load library")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var rowUserID, id, itemType, title, body, source, stage, topic, connection, created, updated string
		if err := rows.Scan(&rowUserID, &id, &itemType, &title, &body, &source, &stage, &topic, &connection, &created, &updated); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load library")
			return
		}
		items = append(items, map[string]any{"id": id, "type": itemType, "title": title, "body": body, "source": source, "stage": stage, "topic": topic, "connection": connection, "created_at": created, "updated_at": updated})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load library")
		return
	}
	var next any
	if len(items) > limit {
		last := items[limit-1]
		next = encodeLibraryCursor(libraryCursor{UpdatedAt: stringValue(last["updated_at"]), Type: stringValue(last["type"]), ID: stringValue(last["id"])})
		items = items[:limit]
	}
	facets := map[string]map[string]int{"type": {}, "source": {}, "stage": {}, "connection": {}}
	for _, item := range items {
		for _, key := range []string{"type", "source", "stage", "connection"} {
			facets[key][stringValue(item[key])]++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next, "facets": facets})
}

var libraryUnion = `SELECT * FROM (
 SELECT b.user_id,b.id,'bookmark' item_type,COALESCE(b.title,b.url) title,substr(COALESCE(b.description,b.text_content,''),1,600) body,b.source,COALESCE(st.stage,'inbox') stage,
  COALESCE((SELECT group_concat(value,' ') FROM (SELECT entity value FROM bookmark_entities topic_entity WHERE user_id=b.user_id AND bookmark_id=b.id AND ` + semanticEligibilitySQL("topic_entity") + ` UNION SELECT concept FROM bookmark_concepts topic_concept WHERE user_id=b.user_id AND bookmark_id=b.id AND ` + semanticEligibilitySQL("topic_concept") + `)),'') topic,
  CASE WHEN EXISTS(SELECT 1 FROM item_links l WHERE l.user_id=b.user_id AND ((l.from_type='bookmark' AND l.from_id=b.id) OR (l.to_type='bookmark' AND l.to_id=b.id))) THEN 'connected' ELSE 'unconnected' END connection_state,b.created_at,b.updated_at
 FROM bookmarks b LEFT JOIN item_states st ON st.user_id=b.user_id AND st.item_type='bookmark' AND st.item_id=b.id
 UNION ALL
 SELECT n.user_id,n.id,'note',n.title,substr(n.body,1,600),n.source,COALESCE(st.stage,'inbox'),'',CASE WHEN EXISTS(SELECT 1 FROM item_links l WHERE l.user_id=n.user_id AND ((l.from_type='note' AND l.from_id=n.id) OR (l.to_type='note' AND l.to_id=n.id))) THEN 'connected' ELSE 'unconnected' END,n.created_at,n.updated_at
 FROM notes n LEFT JOIN item_states st ON st.user_id=n.user_id AND st.item_type='note' AND st.item_id=n.id
 UNION ALL SELECT d.user_id,d.note_date,'daily_note',d.note_date,substr(d.body,1,600),'daily_note','processed','', 'unconnected',d.created_at,d.updated_at FROM daily_notes d
 UNION ALL SELECT a.user_id,a.id,'annotation',substr(a.quote,1,160),substr(a.note,1,600),'annotation','processed',a.tags_json,'connected',a.created_at,a.updated_at FROM annotations a
 UNION ALL SELECT o.user_id,o.id,'knowledge_object',o.title,substr(o.description,1,600),o.object_type,'processed',o.object_type,CASE WHEN o.source_item_id<>'' THEN 'connected' ELSE 'unconnected' END,o.created_at,o.updated_at FROM knowledge_objects o
 UNION ALL SELECT e.user_id,e.entity,'entity',e.entity,'','derived','processed',e.entity,'connected',MIN(b.created_at),MAX(b.updated_at) FROM bookmark_entities e JOIN bookmarks b ON b.user_id=e.user_id AND b.id=e.bookmark_id WHERE ` + semanticEligibilitySQL("e") + ` GROUP BY e.user_id,e.entity
 UNION ALL SELECT c.user_id,c.concept,'concept',c.concept,'','derived','processed',c.concept,'connected',MIN(b.created_at),MAX(b.updated_at) FROM bookmark_concepts c JOIN bookmarks b ON b.user_id=c.user_id AND b.id=c.bookmark_id WHERE ` + semanticEligibilitySQL("c") + ` GROUP BY c.user_id,c.concept
) library`

func encodeLibraryCursor(cursor libraryCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeLibraryCursor(raw string) (libraryCursor, bool) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return libraryCursor{}, false
	}
	var cursor libraryCursor
	err = json.Unmarshal(data, &cursor)
	return cursor, err == nil && cursor.UpdatedAt != "" && cursor.Type != "" && cursor.ID != ""
}
