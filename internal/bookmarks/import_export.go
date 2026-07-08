package bookmarks

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
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
		if err == nil && body.ImportJobID != "" {
			s.recordImportJobSuccess(ctx, body.BookmarkID, body.ImportJobID)
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
	if aiSummary, err := s.geminiClient(ctx).GenerateSummary(ctx, result.Text); err == nil && strings.TrimSpace(aiSummary) != "" {
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
	s.refreshSearchIndex(ctx, userID)
	return nil
}

func (s *Service) Import(w http.ResponseWriter, r *http.Request, user auth.User) {
	raw, _ := io.ReadAll(r.Body)
	if len(strings.TrimSpace(string(raw))) == 0 {
		writeError(w, http.StatusBadRequest, "No import content provided")
		return
	}
	if result, ok, err := s.restoreFullExport(r.Context(), user.ID, raw); ok {
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
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
	if count > 0 {
		s.refreshSearchIndex(r.Context(), user.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Import started", "count": count, "import_job_id": jobID, "source_report": s.importSourceReport(r.Context(), user.ID, jobID)})
}

func (s *Service) restoreFullExport(ctx context.Context, userID string, raw []byte) (map[string]any, bool, error) {
	var backup map[string]any
	if err := json.Unmarshal(raw, &backup); err != nil {
		return nil, false, nil
	}
	bookmarksRaw, ok := backup["bookmarks"].([]any)
	if !ok {
		return nil, false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	jobID := ids.New()
	oldBookmarks := map[string]string{}
	oldNotes := map[string]string{}
	oldObjects := map[string]string{}
	restored := 0
	_, _ = s.db.ExecContext(ctx, `INSERT INTO import_jobs(id,user_id,total_bookmarks,content_fetched,ai_processed,failed,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, jobID, userID, len(bookmarksRaw), 0, 0, 0, "processing", now, now)
	for _, rawBookmark := range bookmarksRaw {
		bookmark, ok := rawBookmark.(map[string]any)
		if !ok {
			continue
		}
		rawURL := stringValue(bookmark["url"])
		if err := safefetch.ValidateURL(rawURL); err != nil {
			continue
		}
		oldID := stringValue(bookmark["id"])
		newID := fallback(oldID, ids.New())
		parsed, _ := url.Parse(rawURL)
		inserted, err := s.insertRestoredBookmark(ctx, userID, newID, rawURL, bookmark, parsed.Hostname(), now)
		if err != nil {
			continue
		}
		if !inserted {
			var existing string
			if err := s.db.QueryRowContext(ctx, `SELECT id FROM bookmarks WHERE user_id=? AND url=?`, userID, rawURL).Scan(&existing); err == nil {
				newID = existing
			} else if err == sql.ErrNoRows && oldID != "" {
				newID = ids.New()
				if retryInserted, retryErr := s.insertRestoredBookmark(ctx, userID, newID, rawURL, bookmark, parsed.Hostname(), now); retryErr != nil || !retryInserted {
					continue
				}
				restored++
			} else {
				continue
			}
		} else {
			restored++
		}
		if oldID != "" {
			oldBookmarks[oldID] = newID
		}
		s.restoreBookmarkChildren(ctx, userID, newID, bookmark, oldNotes, now)
	}
	s.restoreStandaloneNotes(ctx, userID, backup["notes"], oldNotes, now)
	s.restoreDailyNotes(ctx, userID, backup["daily_notes"], now)
	s.restoreKnowledgeObjects(ctx, userID, backup["knowledge_objects"], oldBookmarks, oldNotes, oldObjects, now)
	s.restoreTags(ctx, userID, backup["tags"], now)
	s.restoreSavedSearches(ctx, userID, backup["saved_searches"], now)
	s.restoreReviewEvents(ctx, userID, backup["review_events"], oldBookmarks, oldNotes, now)
	s.restoreItemStates(ctx, userID, backup["item_states"], oldBookmarks, oldNotes, now)
	s.restoreItemLinks(ctx, userID, backup["item_links"], oldBookmarks, oldNotes, now)
	s.restoreReminders(ctx, userID, backup["reminders"], oldBookmarks, oldNotes, now)
	s.restoreActionItems(ctx, userID, backup["action_items"], oldBookmarks, oldNotes, now)
	s.restoreResultFeedback(ctx, userID, backup["result_feedback"], oldBookmarks, oldNotes, now)
	s.restoreImportSources(ctx, userID, jobID, backup["import_sources"], oldBookmarks, now)
	_, _ = s.db.ExecContext(ctx, `UPDATE import_jobs SET total_bookmarks=?,content_fetched=?,ai_processed=?,status='completed',updated_at=? WHERE id=? AND user_id=?`, restored, restored, restored, now, jobID, userID)
	s.refreshSearchIndex(ctx, userID)
	return map[string]any{"message": "Backup restored", "count": restored, "import_job_id": jobID, "source_report": s.importSourceReport(ctx, userID, jobID)}, true, nil
}

func (s *Service) insertRestoredBookmark(ctx context.Context, userID, id, rawURL string, bookmark map[string]any, domainFallback, now string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO bookmarks(id,user_id,url,title,description,domain,favicon,thumbnail,sanitized_html,text_content,reading_time,read_status,source,created_at,updated_at,last_accessed,view_count,version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, userID, rawURL, stringValue(bookmark["title"]), stringValue(bookmark["description"]), fallback(stringValue(bookmark["domain"]), domainFallback), nullableStringValue(stringValue(bookmark["favicon"])), nullableStringValue(stringValue(bookmark["thumbnail"])), sanitize.HTML(stringValue(bookmark["html_content"])), stringValue(bookmark["text_content"]), intValue(bookmark["reading_time"]), boolValue(bookmark["read_status"]), fallback(stringValue(bookmark["source"]), "restore"), fallback(stringValue(bookmark["created_at"]), now), fallback(stringValue(bookmark["updated_at"]), now), nullableStringValue(stringValue(bookmark["last_accessed"])), intValue(bookmark["view_count"]), intValueDefault(bookmark["version"], 1))
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

func (s *Service) restoreBookmarkChildren(ctx context.Context, userID, bookmarkID string, bookmark map[string]any, oldNotes map[string]string, now string) {
	if summary, ok := bookmark["ai_summary"].(map[string]any); ok {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,bullet_points_json,long_form,highlights_json,suggested_tags_json,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(bookmark_id) DO UPDATE SET one_sentence=excluded.one_sentence,bullet_points_json=excluded.bullet_points_json,long_form=excluded.long_form,highlights_json=excluded.highlights_json,suggested_tags_json=excluded.suggested_tags_json,processing_status=excluded.processing_status,updated_at=excluded.updated_at`,
			ids.New(), bookmarkID, userID, stringValue(summary["one_sentence"]), jsonListString(summary["bullet_points"]), stringValue(summary["long_form"]), jsonListString(summary["highlights"]), jsonListString(summary["suggested_tags"]), fallback(stringValue(summary["processing_status"]), "completed"), now, now)
	}
	for _, rawTag := range listValue(bookmark["tags"]) {
		if tag, ok := rawTag.(map[string]any); ok {
			_ = s.attachTag(ctx, userID, bookmarkID, stringValue(tag["name"]), fallback(stringValue(tag["source"]), "restore"))
		}
	}
	for _, rawAnnotation := range listValue(bookmark["annotations"]) {
		if annotation, ok := rawAnnotation.(map[string]any); ok {
			tags := stringSlice(annotation["tags"])
			tagJSON, _ := json.Marshal(tags)
			selector := jsonString(annotation["selector"])
			if selector == "" {
				selector = "{}"
			}
			annotationID := fallback(stringValue(annotation["id"]), ids.New())
			inserted := s.insertRestoredAnnotation(ctx, userID, bookmarkID, annotationID, annotation, selector, string(tagJSON), now)
			if !inserted && stringValue(annotation["id"]) != "" {
				_ = s.insertRestoredAnnotation(ctx, userID, bookmarkID, ids.New(), annotation, selector, string(tagJSON), now)
			}
			for _, tag := range tags {
				_ = s.attachTag(ctx, userID, bookmarkID, tag, "restore")
			}
		}
	}
	for _, rawNote := range listValue(bookmark["notes"]) {
		if note, ok := rawNote.(map[string]any); ok {
			oldID := stringValue(note["id"])
			noteID := oldNotes[oldID]
			if noteID == "" {
				noteID = s.restoreNote(ctx, userID, note, now)
			}
			if noteID != "" {
				oldNotes[oldID] = noteID
				_, _ = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_notes(bookmark_id,note_id,user_id,created_at) VALUES(?,?,?,?)`, bookmarkID, noteID, userID, now)
			}
		}
	}
}

func (s *Service) insertRestoredAnnotation(ctx context.Context, userID, bookmarkID, annotationID string, annotation map[string]any, selector, tagJSON, now string) bool {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO annotations(id,user_id,bookmark_id,quote,note,selector_json,tags_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, annotationID, userID, bookmarkID, strings.TrimSpace(stringValue(annotation["quote"])), strings.TrimSpace(stringValue(annotation["note"])), selector, tagJSON, fallback(stringValue(annotation["created_at"]), now), fallback(stringValue(annotation["updated_at"]), now))
	if err != nil {
		return false
	}
	rows, _ := res.RowsAffected()
	return rows > 0
}

func (s *Service) restoreStandaloneNotes(ctx context.Context, userID string, raw any, oldNotes map[string]string, now string) {
	for _, rawNote := range listValue(raw) {
		if note, ok := rawNote.(map[string]any); ok {
			oldID := stringValue(note["id"])
			if oldNotes[oldID] != "" {
				continue
			}
			if noteID := s.restoreNote(ctx, userID, note, now); noteID != "" {
				oldNotes[oldID] = noteID
			}
		}
	}
}

func (s *Service) restoreDailyNotes(ctx context.Context, userID string, raw any, now string) {
	for _, rawNote := range listValue(raw) {
		note, ok := rawNote.(map[string]any)
		if !ok {
			continue
		}
		date, valid := dailyNoteDate(stringValue(note["date"]))
		if !valid {
			continue
		}
		body := strings.TrimSpace(stringValue(note["body"]))
		if len(body) > maxNoteBody {
			body = body[:maxNoteBody]
		}
		created := fallback(stringValue(note["created_at"]), now)
		updated := fallback(stringValue(note["updated_at"]), now)
		_, _ = s.db.ExecContext(ctx, `INSERT INTO daily_notes(user_id,note_date,body,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(user_id,note_date) DO UPDATE SET body=excluded.body,updated_at=excluded.updated_at`, userID, date, body, created, updated)
	}
}

func (s *Service) restoreKnowledgeObjects(ctx context.Context, userID string, raw any, oldBookmarks, oldNotes, oldObjects map[string]string, now string) {
	type pendingSource struct {
		id         string
		sourceType string
		sourceID   string
	}
	pending := []pendingSource{}
	for _, rawObject := range listValue(raw) {
		object, ok := rawObject.(map[string]any)
		if !ok {
			continue
		}
		objectType := normalizeObjectType(stringValue(object["object_type"]))
		title := strings.TrimSpace(stringValue(object["title"]))
		description := strings.TrimSpace(stringValue(object["description"]))
		if objectType == "" || title == "" && description == "" {
			continue
		}
		fields := jsonString(object["fields"])
		oldID := stringValue(object["id"])
		id := fallback(oldID, ids.New())
		res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO knowledge_objects(id,user_id,object_type,title,description,fields_json,source_item_type,source_item_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, userID, objectType, title, description, fields, "", "", fallback(stringValue(object["created_at"]), now), fallback(stringValue(object["updated_at"]), now))
		if err != nil {
			continue
		}
		if rows, _ := res.RowsAffected(); rows == 0 {
			if oldID == "" {
				continue
			}
			id = ids.New()
			if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO knowledge_objects(id,user_id,object_type,title,description,fields_json,source_item_type,source_item_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, userID, objectType, title, description, fields, "", "", fallback(stringValue(object["created_at"]), now), fallback(stringValue(object["updated_at"]), now)); err != nil {
				continue
			}
		}
		if oldID != "" {
			oldObjects[oldID] = id
		}
		sourceType := normalizeSourceItemType(stringValue(object["source_item_type"]))
		sourceID := stringValue(object["source_item_id"])
		if sourceType != "" && sourceID != "" {
			pending = append(pending, pendingSource{id: id, sourceType: sourceType, sourceID: sourceID})
		}
	}
	for _, item := range pending {
		sourceID := remapObjectSourceID(item.sourceType, item.sourceID, oldBookmarks, oldNotes, oldObjects)
		if sourceID == "" || !s.sourceItemExists(ctx, userID, item.sourceType, sourceID) {
			continue
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE knowledge_objects SET source_item_type=?,source_item_id=? WHERE id=? AND user_id=?`, item.sourceType, sourceID, item.id, userID)
	}
}

func (s *Service) restoreNote(ctx context.Context, userID string, note map[string]any, now string) string {
	if strings.TrimSpace(stringValue(note["title"])+stringValue(note["body"])) == "" {
		return ""
	}
	id := fallback(stringValue(note["id"]), ids.New())
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, userID, strings.TrimSpace(stringValue(note["title"])), strings.TrimSpace(stringValue(note["body"])), fallback(stringValue(note["source"]), "restore"), fallback(stringValue(note["created_at"]), now), fallback(stringValue(note["updated_at"]), now))
	if err != nil {
		return ""
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		if stringValue(note["id"]) == "" {
			return ""
		}
		id = ids.New()
		res, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, userID, strings.TrimSpace(stringValue(note["title"])), strings.TrimSpace(stringValue(note["body"])), fallback(stringValue(note["source"]), "restore"), fallback(stringValue(note["created_at"]), now), fallback(stringValue(note["updated_at"]), now))
		if err != nil {
			return ""
		}
		if rows, _ := res.RowsAffected(); rows == 0 {
			return ""
		}
	}
	return id
}

func (s *Service) restoreTags(ctx context.Context, userID string, raw any, now string) {
	for _, rawTag := range listValue(raw) {
		tag, ok := rawTag.(map[string]any)
		if !ok {
			continue
		}
		restored, err := s.upsertTag(ctx, userID, stringValue(tag["name"]), fallback(stringValue(tag["source"]), "restore"))
		if err != nil {
			continue
		}
		tagID, _ := restored["id"].(string)
		for _, rawAlias := range listValue(tag["aliases"]) {
			if alias, ok := rawAlias.(map[string]any); ok {
				name := strings.TrimSpace(stringValue(alias["alias"]))
				if name != "" {
					_, _ = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO tag_aliases(id,user_id,tag_id,alias,alias_slug,created_at) VALUES(?,?,?,?,?,?)`, ids.New(), userID, tagID, name, tagSlug(name), fallback(stringValue(alias["created_at"]), now))
				}
			}
		}
	}
}

func (s *Service) restoreSavedSearches(ctx context.Context, userID string, raw any, now string) {
	for _, rawSearch := range listValue(raw) {
		search, ok := rawSearch.(map[string]any)
		if !ok || strings.TrimSpace(stringValue(search["name"])) == "" {
			continue
		}
		searchID := fallback(stringValue(search["id"]), ids.New())
		inserted := s.insertRestoredSavedSearch(ctx, userID, searchID, search, now)
		if !inserted && stringValue(search["id"]) != "" {
			_ = s.insertRestoredSavedSearch(ctx, userID, ids.New(), search, now)
		}
	}
}

func (s *Service) insertRestoredSavedSearch(ctx context.Context, userID, searchID string, search map[string]any, now string) bool {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO saved_searches(id,user_id,name,query,filters_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, searchID, userID, strings.TrimSpace(stringValue(search["name"])), strings.TrimSpace(stringValue(search["query"])), jsonString(search["filters"]), fallback(stringValue(search["created_at"]), now), fallback(stringValue(search["updated_at"]), now))
	if err != nil {
		return false
	}
	rows, _ := res.RowsAffected()
	return rows > 0
}

func (s *Service) restoreReviewEvents(ctx context.Context, userID string, raw any, oldBookmarks, oldNotes map[string]string, now string) {
	for _, rawEvent := range listValue(raw) {
		event, ok := rawEvent.(map[string]any)
		if !ok {
			continue
		}
		itemType := stringValue(event["item_type"])
		itemID := stringValue(event["item_id"])
		if itemType == "bookmark" {
			itemID = oldBookmarks[itemID]
		} else if itemType == "note" {
			itemID = oldNotes[itemID]
		}
		if itemID == "" || (itemType != "bookmark" && itemType != "note") {
			continue
		}
		_, _ = s.db.ExecContext(ctx, `INSERT INTO review_events(id,user_id,item_type,item_id,action,snoozed_until,created_at) VALUES(?,?,?,?,?,?,?)`, ids.New(), userID, itemType, itemID, fallback(stringValue(event["action"]), "completed"), nullableStringValue(stringValue(event["snoozed_until"])), fallback(stringValue(event["created_at"]), now))
	}
}

func (s *Service) restoreImportSources(ctx context.Context, userID, jobID string, raw any, oldBookmarks map[string]string, now string) {
	for _, rawSource := range listValue(raw) {
		source, ok := rawSource.(map[string]any)
		if !ok {
			continue
		}
		metadata := map[string]any{}
		if meta, ok := source["metadata"].(map[string]any); ok {
			for key, value := range meta {
				metadata[key] = value
			}
			if old, _ := meta["bookmark_id"].(string); oldBookmarks[old] != "" {
				metadata["bookmark_id"] = oldBookmarks[old]
			}
		}
		metaJSON, _ := json.Marshal(metadata)
		_, _ = s.db.ExecContext(ctx, `INSERT INTO import_sources(id,user_id,import_job_id,source_type,source_name,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, ids.New(), userID, jobID, fallback(stringValue(source["source"]), "restore"), stringValue(source["title"]), string(metaJSON), fallback(stringValue(source["created_at"]), now))
	}
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
	if format == "obsidian" || format == "obsidian-zip" {
		if err := s.writeObsidianExport(r.Context(), w, user.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not export Obsidian vault")
		}
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
	case "md", "markdown":
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

func (s *Service) writeObsidianExport(ctx context.Context, w http.ResponseWriter, userID string) error {
	export, err := s.fullExport(ctx, userID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="arivu-obsidian-vault.zip"`)
	vault := zip.NewWriter(w)
	defer vault.Close()
	bookmarks := mapList(export["bookmarks"])
	notes := obsidianNotes(export)
	itemLinks := mapList(export["item_links"])
	index := obsidianIndex(bookmarks, notes)
	for _, bookmark := range bookmarks {
		id := stringValue(bookmark["id"])
		name := index[obsidianItemKey("bookmark", id)].Path + ".md"
		file, err := vault.Create(name)
		if err != nil {
			return err
		}
		writeObsidianBookmark(file, bookmark, index, itemLinks)
	}
	for _, note := range notes {
		id := stringValue(note["id"])
		name := index[obsidianItemKey("note", id)].Path + ".md"
		file, err := vault.Create(name)
		if err != nil {
			return err
		}
		writeObsidianNote(file, note, index, itemLinks)
	}
	return nil
}

func (s *Service) fullExport(ctx context.Context, userID string) (map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,url,title,description,domain,favicon,thumbnail,reading_time,read_status,source,created_at,updated_at,last_accessed,view_count,version,sanitized_html,text_content FROM bookmarks WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	bookmarks := []map[string]any{}
	for rows.Next() {
		bookmarks = append(bookmarks, scanBookmarkRow(rows))
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
		"version":           1,
		"exported_at":       time.Now().UTC().Format(time.RFC3339),
		"bookmarks":         bookmarks,
		"notes":             s.exportStandaloneNotes(ctx, userID),
		"daily_notes":       s.exportDailyNotes(ctx, userID),
		"knowledge_objects": s.exportKnowledgeObjects(ctx, userID),
		"tags":              s.exportTags(ctx, userID),
		"saved_searches":    s.exportSavedSearches(ctx, userID),
		"import_jobs":       s.exportImportJobs(ctx, userID),
		"import_sources":    s.exportImportSources(ctx, userID),
		"review_events":     s.exportReviewEvents(ctx, userID),
		"item_states":       s.exportItemStates(ctx, userID),
		"item_links":        s.exportItemLinks(ctx, userID),
		"reminders":         s.exportReminders(ctx, userID),
		"action_items":      s.exportActionItems(ctx, userID),
		"result_feedback":   s.exportResultFeedback(ctx, userID),
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

func (s *Service) exportDailyNotes(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT note_date,body,created_at,updated_at FROM daily_notes WHERE user_id=? ORDER BY note_date DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	notes := []map[string]any{}
	for rows.Next() {
		var date, body, created, updated string
		_ = rows.Scan(&date, &body, &created, &updated)
		notes = append(notes, map[string]any{"date": date, "body": body, "created_at": created, "updated_at": updated})
	}
	return notes
}

func (s *Service) exportKnowledgeObjects(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,object_type,title,description,fields_json,source_item_type,source_item_id,created_at,updated_at FROM knowledge_objects WHERE user_id=? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	objects := []map[string]any{}
	for rows.Next() {
		object, err := scanObject(rows.Scan)
		if err == nil {
			objects = append(objects, object)
		}
	}
	return objects
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

func (s *Service) exportItemStates(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT item_type,item_id,stage,importance,next_action,created_at,updated_at FROM item_states WHERE user_id=? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	states := []map[string]any{}
	for rows.Next() {
		var itemType, itemID, stage, nextAction, created, updated string
		var importance int
		_ = rows.Scan(&itemType, &itemID, &stage, &importance, &nextAction, &created, &updated)
		states = append(states, map[string]any{"item_type": itemType, "item_id": itemID, "stage": stage, "importance": importance, "next_action": nextAction, "created_at": created, "updated_at": updated})
	}
	return states
}

func (s *Service) exportItemLinks(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,from_type,from_id,to_type,to_id,label,source,created_at FROM item_links WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	links := []map[string]any{}
	for rows.Next() {
		links = append(links, scanLink(rows))
	}
	return links
}

func (s *Service) exportReminders(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,item_type,item_id,due_at,timezone,recurrence,recurrence_interval_days,notification_channel,note,status,created_at,COALESCE(completed_at,''),COALESCE(last_notified_at,''),COALESCE(last_completed_at,'') FROM reminders WHERE user_id=? ORDER BY due_at ASC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	reminders, err := s.scanReminders(ctx, userID, rows)
	if err != nil {
		return []map[string]any{}
	}
	return reminders
}

func (s *Service) exportActionItems(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,item_type,item_id,title,status,created_at,COALESCE(completed_at,'') FROM action_items WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	items, err := s.scanActionItems(ctx, userID, rows)
	if err != nil {
		return []map[string]any{}
	}
	return items
}

func (s *Service) exportResultFeedback(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT item_type,item_id,surface,feedback,created_at,updated_at FROM result_feedback WHERE user_id=? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var itemType, itemID, surface, feedback, created, updated string
		_ = rows.Scan(&itemType, &itemID, &surface, &feedback, &created, &updated)
		items = append(items, map[string]any{"item_type": itemType, "item_id": itemID, "surface": surface, "feedback": feedback, "created_at": created, "updated_at": updated})
	}
	return items
}

func (s *Service) restoreItemStates(ctx context.Context, userID string, raw any, oldBookmarks, oldNotes map[string]string, now string) {
	for _, rawState := range listValue(raw) {
		state, ok := rawState.(map[string]any)
		if !ok {
			continue
		}
		itemType := stringValue(state["item_type"])
		itemID := stringValue(state["item_id"])
		if itemType == "bookmark" {
			itemID = oldBookmarks[itemID]
		} else if itemType == "note" {
			itemID = oldNotes[itemID]
		}
		stage := stringValue(state["stage"])
		if itemID == "" || !validItemStage(stage) {
			continue
		}
		importance := intValue(state["importance"])
		if importance < 0 || importance > 5 {
			importance = 0
		}
		nextAction := strings.TrimSpace(stringValue(state["next_action"]))
		if len(nextAction) > 500 {
			nextAction = nextAction[:500]
		}
		_ = s.upsertItemState(ctx, userID, itemType, itemID, stage, importance, nextAction, fallback(stringValue(state["updated_at"]), now))
	}
}

func (s *Service) restoreResultFeedback(ctx context.Context, userID string, raw any, oldBookmarks, oldNotes map[string]string, now string) {
	for _, rawItem := range listValue(raw) {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemType := stringValue(item["item_type"])
		itemID := remapItemID(itemType, stringValue(item["item_id"]), oldBookmarks, oldNotes)
		feedback := stringValue(item["feedback"])
		if itemID == "" || !validFeedback(feedback) || !s.reviewItemExists(ctx, userID, itemType, itemID) {
			continue
		}
		surface := feedbackSurface(stringValue(item["surface"]))
		created := fallback(stringValue(item["created_at"]), now)
		updated := fallback(stringValue(item["updated_at"]), now)
		_, _ = s.db.ExecContext(ctx, `INSERT INTO result_feedback(user_id,item_type,item_id,surface,feedback,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,item_type,item_id,surface) DO UPDATE SET feedback=excluded.feedback,updated_at=excluded.updated_at`, userID, itemType, itemID, surface, feedback, created, updated)
	}
}

func (s *Service) restoreReminders(ctx context.Context, userID string, raw any, oldBookmarks, oldNotes map[string]string, now string) {
	for _, rawReminder := range listValue(raw) {
		reminder, ok := rawReminder.(map[string]any)
		if !ok {
			continue
		}
		itemType := stringValue(reminder["item_type"])
		itemID := remapItemID(itemType, stringValue(reminder["item_id"]), oldBookmarks, oldNotes)
		due, err := time.Parse(time.RFC3339, stringValue(reminder["due_at"]))
		if itemID == "" || err != nil || !s.reviewItemExists(ctx, userID, itemType, itemID) {
			continue
		}
		status := stringValue(reminder["status"])
		if status != "completed" {
			status = "pending"
		}
		note := strings.TrimSpace(stringValue(reminder["note"]))
		if len(note) > 500 {
			note = note[:500]
		}
		timezoneName := fallback(stringValue(reminder["timezone"]), "UTC")
		if _, err := time.LoadLocation(timezoneName); err != nil {
			timezoneName = "UTC"
		}
		recurrence := fallback(stringValue(reminder["recurrence"]), "none")
		if !validReminderRecurrence(recurrence) {
			recurrence = "none"
		}
		interval := intValue(reminder["recurrence_interval_days"])
		if recurrence != "custom" || interval < 1 || interval > 365 {
			interval = 0
		}
		channel := fallback(stringValue(reminder["notification_channel"]), "in_app")
		if channel != "email" {
			channel = "in_app"
		}
		completed := nullableStringValue(stringValue(reminder["completed_at"]))
		lastCompleted := nullableStringValue(stringValue(reminder["last_completed_at"]))
		reminderID := ids.New()
		dueUTC := due.UTC().Format(time.RFC3339)
		_, _ = s.db.ExecContext(ctx, `INSERT INTO reminders(id,user_id,item_type,item_id,due_at,timezone,recurrence,recurrence_interval_days,notification_channel,note,status,created_at,completed_at,last_completed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, reminderID, userID, itemType, itemID, dueUTC, timezoneName, recurrence, interval, channel, note, status, fallback(stringValue(reminder["created_at"]), now), completed, lastCompleted)
		if status == "pending" && channel == "email" && due.After(time.Now().UTC()) {
			s.scheduleReminderNotification(ctx, userID, reminderID, dueUTC, channel)
		}
	}
}

func (s *Service) restoreActionItems(ctx context.Context, userID string, raw any, oldBookmarks, oldNotes map[string]string, now string) {
	for _, rawItem := range listValue(raw) {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemType := stringValue(item["item_type"])
		itemID := remapItemID(itemType, stringValue(item["item_id"]), oldBookmarks, oldNotes)
		if itemID == "" || !s.reviewItemExists(ctx, userID, itemType, itemID) {
			continue
		}
		title := strings.TrimSpace(stringValue(item["title"]))
		if title == "" {
			continue
		}
		if len(title) > 300 {
			title = title[:300]
		}
		status := stringValue(item["status"])
		if status != "completed" {
			status = "pending"
		}
		completed := nullableStringValue(stringValue(item["completed_at"]))
		_, _ = s.db.ExecContext(ctx, `INSERT INTO action_items(id,user_id,item_type,item_id,title,status,created_at,completed_at) VALUES(?,?,?,?,?,?,?,?)`, ids.New(), userID, itemType, itemID, title, status, fallback(stringValue(item["created_at"]), now), completed)
	}
}

func (s *Service) restoreItemLinks(ctx context.Context, userID string, raw any, oldBookmarks, oldNotes map[string]string, now string) {
	for _, rawLink := range listValue(raw) {
		link, ok := rawLink.(map[string]any)
		if !ok {
			continue
		}
		fromType := stringValue(link["from_type"])
		toType := stringValue(link["to_type"])
		fromID := remapItemID(fromType, stringValue(link["from_id"]), oldBookmarks, oldNotes)
		toID := remapItemID(toType, stringValue(link["to_id"]), oldBookmarks, oldNotes)
		label := strings.TrimSpace(stringValue(link["label"]))
		if fromID == "" || toID == "" || !s.reviewItemExists(ctx, userID, fromType, fromID) || !s.reviewItemExists(ctx, userID, toType, toID) || len(label) > 80 {
			continue
		}
		_, _ = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, ids.New(), userID, fromType, fromID, toType, toID, label, fallback(stringValue(link["source"]), "restore"), fallback(stringValue(link["created_at"]), now))
	}
}

func remapItemID(itemType, itemID string, oldBookmarks, oldNotes map[string]string) string {
	if itemType == "bookmark" {
		return oldBookmarks[itemID]
	}
	if itemType == "note" {
		return oldNotes[itemID]
	}
	return ""
}

func remapObjectSourceID(itemType, itemID string, oldBookmarks, oldNotes, oldObjects map[string]string) string {
	if itemType == "object" {
		return oldObjects[itemID]
	}
	return remapItemID(itemType, itemID, oldBookmarks, oldNotes)
}

type obsidianItem struct {
	Path  string
	Title string
}

func obsidianNotes(export map[string]any) []map[string]any {
	seen := map[string]bool{}
	var notes []map[string]any
	for _, note := range mapList(export["notes"]) {
		id := stringValue(note["id"])
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		notes = append(notes, note)
	}
	for _, bookmark := range mapList(export["bookmarks"]) {
		for _, note := range mapList(bookmark["notes"]) {
			id := stringValue(note["id"])
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			notes = append(notes, note)
		}
	}
	return notes
}

func obsidianIndex(bookmarks, notes []map[string]any) map[string]obsidianItem {
	index := map[string]obsidianItem{}
	for _, bookmark := range bookmarks {
		id := stringValue(bookmark["id"])
		title := fallback(stringValue(bookmark["title"]), stringValue(bookmark["url"]))
		index[obsidianItemKey("bookmark", id)] = obsidianItem{Path: "Bookmarks/" + strings.TrimSuffix(obsidianFileName(title, id), ".md"), Title: title}
	}
	for _, note := range notes {
		id := stringValue(note["id"])
		title := fallback(stringValue(note["title"]), "Untitled Note")
		index[obsidianItemKey("note", id)] = obsidianItem{Path: "Notes/" + strings.TrimSuffix(obsidianFileName(title, id), ".md"), Title: title}
	}
	return index
}

func obsidianItemKey(itemType, itemID string) string {
	return itemType + ":" + itemID
}

func writeObsidianBookmark(w io.Writer, bookmark map[string]any, index map[string]obsidianItem, links []map[string]any) {
	title := fallback(stringValue(bookmark["title"]), stringValue(bookmark["url"]))
	fmt.Fprintf(w, "# %s\n\n", markdownText(title))
	fmt.Fprintf(w, "Source: %s\n", markdownURL(stringValue(bookmark["url"])))
	if domain := stringValue(bookmark["domain"]); domain != "" {
		fmt.Fprintf(w, "Domain: %s\n", markdownText(domain))
	}
	if created := stringValue(bookmark["created_at"]); created != "" {
		fmt.Fprintf(w, "Saved: %s\n", markdownText(created))
	}
	if tags := tagNames(bookmark["tags"]); len(tags) > 0 {
		fmt.Fprintf(w, "Tags: %s\n", markdownText(strings.Join(tags, ", ")))
	}
	fmt.Fprintln(w)
	if summary, ok := bookmark["ai_summary"].(map[string]any); ok {
		if one := stringValue(summary["one_sentence"]); one != "" {
			fmt.Fprintf(w, "## Summary\n\n%s\n\n", markdownText(one))
		}
		if bullets := stringSlice(summary["bullet_points"]); len(bullets) > 0 {
			fmt.Fprintln(w, "## Key Points")
			fmt.Fprintln(w)
			for _, bullet := range bullets {
				fmt.Fprintf(w, "- %s\n", markdownText(bullet))
			}
			fmt.Fprintln(w)
		}
	}
	if annotations := mapList(bookmark["annotations"]); len(annotations) > 0 {
		fmt.Fprintln(w, "## Annotations")
		fmt.Fprintln(w)
		for _, annotation := range annotations {
			if quote := stringValue(annotation["quote"]); quote != "" {
				fmt.Fprintf(w, "> %s\n", markdownText(quote))
			}
			if note := stringValue(annotation["note"]); note != "" {
				fmt.Fprintf(w, "\n%s\n", markdownText(note))
			}
			fmt.Fprintln(w)
		}
	}
	if notes := mapList(bookmark["notes"]); len(notes) > 0 {
		fmt.Fprintln(w, "## Linked Notes")
		fmt.Fprintln(w)
		for _, note := range notes {
			if title := stringValue(note["title"]); title != "" {
				fmt.Fprintf(w, "### %s\n\n", markdownText(title))
			}
			if body := stringValue(note["body"]); body != "" {
				fmt.Fprintf(w, "%s\n\n", markdownText(body))
			}
		}
	}
	if text := strings.TrimSpace(stringValue(bookmark["text_content"])); text != "" {
		fmt.Fprintf(w, "## Archived Text\n\n%s\n", markdownText(text))
	}
	writeObsidianLinks(w, "bookmark", stringValue(bookmark["id"]), index, links)
}

func writeObsidianNote(w io.Writer, note map[string]any, index map[string]obsidianItem, links []map[string]any) {
	fmt.Fprintf(w, "# %s\n\n", markdownText(fallback(stringValue(note["title"]), "Untitled Note")))
	if created := stringValue(note["created_at"]); created != "" {
		fmt.Fprintf(w, "Created: %s\n\n", markdownText(created))
	}
	if body := stringValue(note["body"]); body != "" {
		fmt.Fprintf(w, "%s\n\n", markdownText(body))
	}
	writeObsidianLinks(w, "note", stringValue(note["id"]), index, links)
}

func writeObsidianLinks(w io.Writer, itemType, itemID string, index map[string]obsidianItem, links []map[string]any) {
	var outgoing, incoming []string
	for _, link := range links {
		label := fallback(stringValue(link["label"]), "linked")
		if stringValue(link["from_type"]) == itemType && stringValue(link["from_id"]) == itemID {
			target := index[obsidianItemKey(stringValue(link["to_type"]), stringValue(link["to_id"]))]
			if target.Path != "" {
				outgoing = append(outgoing, fmt.Sprintf("- %s: %s", markdownText(label), obsidianWikiLink(target)))
			}
		}
		if stringValue(link["to_type"]) == itemType && stringValue(link["to_id"]) == itemID {
			source := index[obsidianItemKey(stringValue(link["from_type"]), stringValue(link["from_id"]))]
			if source.Path != "" {
				incoming = append(incoming, fmt.Sprintf("- %s from %s", markdownText(label), obsidianWikiLink(source)))
			}
		}
	}
	if len(outgoing) == 0 && len(incoming) == 0 {
		return
	}
	fmt.Fprintln(w, "## Links")
	fmt.Fprintln(w)
	for _, line := range outgoing {
		fmt.Fprintln(w, line)
	}
	for _, line := range incoming {
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)
}

func obsidianWikiLink(item obsidianItem) string {
	return fmt.Sprintf("[[%s|%s]]", item.Path, markdownText(item.Title))
}

func (s *Service) RecordJobTerminalFailure(ctx context.Context, jobType string, payload string) {
	if jobType != "bookmark.process" {
		return
	}
	var body struct {
		BookmarkID  string `json:"bookmark_id"`
		ImportJobID string `json:"import_job_id"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil || body.ImportJobID == "" {
		return
	}
	s.recordImportJobFailure(ctx, body.BookmarkID, body.ImportJobID)
}

func (s *Service) recordImportJobSuccess(ctx context.Context, bookmarkID string, importJobID string) {
	s.recordImportJobProgress(ctx, bookmarkID, importJobID, 1, 1, 0)
}

func (s *Service) recordImportJobFailure(ctx context.Context, bookmarkID string, importJobID string) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.ExecContext(ctx, `UPDATE ai_summaries SET processing_status='failed', updated_at=? WHERE bookmark_id=?`, now, bookmarkID)
	s.recordImportJobProgress(ctx, bookmarkID, importJobID, 0, 0, 1)
}

func (s *Service) recordImportJobProgress(ctx context.Context, bookmarkID string, importJobID string, fetched int, processed int, failed int) {
	userID, ok := s.bookmarkOwner(ctx, bookmarkID)
	if !ok {
		return
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
	if xmlItems := extractXMLImportURLs(raw, source); len(xmlItems) > 0 {
		return xmlItems
	}
	if csvItems := extractCSVImportURLs(raw, source); len(csvItems) > 0 {
		return csvItems
	}
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

func extractXMLImportURLs(raw, source string) []importURL {
	decoder := xml.NewDecoder(strings.NewReader(raw))
	decoder.Strict = false
	var result []importURL
	var inItem, inEntry bool
	var title, link string
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch item := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(item.Name.Local)
			if name == "outline" {
				var urlValue, titleValue string
				for _, attr := range item.Attr {
					switch strings.ToLower(attr.Name.Local) {
					case "xmlurl", "htmlurl", "url":
						if urlValue == "" {
							urlValue = strings.TrimSpace(attr.Value)
						}
					case "title", "text":
						if titleValue == "" {
							titleValue = strings.TrimSpace(attr.Value)
						}
					}
				}
				if validImportURL(urlValue) {
					result = append(result, importURL{URL: urlValue, Title: titleValue, Source: source})
				}
			}
			if name == "item" || name == "entry" {
				inItem = name == "item"
				inEntry = name == "entry"
				title, link = "", ""
			}
			if (inItem || inEntry) && name == "title" {
				title = readElementText(decoder, item)
			}
			if inItem && name == "link" {
				link = readElementText(decoder, item)
			}
			if inEntry && name == "link" {
				for _, attr := range item.Attr {
					if strings.ToLower(attr.Name.Local) == "href" && validImportURL(attr.Value) {
						link = strings.TrimSpace(attr.Value)
					}
				}
			}
		case xml.EndElement:
			name := strings.ToLower(item.Name.Local)
			if (name == "item" && inItem) || (name == "entry" && inEntry) {
				if validImportURL(link) {
					result = append(result, importURL{URL: link, Title: title, Source: source})
				}
				inItem, inEntry = false, false
			}
		}
	}
	return dedupeImportURLs(result)
}

func readElementText(decoder *xml.Decoder, start xml.StartElement) string {
	var text string
	if err := decoder.DecodeElement(&text, &start); err != nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(text))
}

func extractCSVImportURLs(raw, source string) []importURL {
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 {
		reader = csv.NewReader(strings.NewReader(raw))
		reader.Comma = '\t'
		reader.FieldsPerRecord = -1
		reader.LazyQuotes = true
		records, err = reader.ReadAll()
		if err != nil || len(records) < 2 {
			return nil
		}
	}
	headers := map[string]int{}
	for i, header := range records[0] {
		headers[normalizeImportHeader(header)] = i
	}
	if !hasImportHeader(headers, "url", "sourceurl", "articleurl", "href", "link") && strings.Contains(raw, "\t") {
		reader = csv.NewReader(strings.NewReader(raw))
		reader.Comma = '\t'
		reader.FieldsPerRecord = -1
		reader.LazyQuotes = true
		if tabRecords, tabErr := reader.ReadAll(); tabErr == nil && len(tabRecords) >= 2 {
			records = tabRecords
			headers = map[string]int{}
			for i, header := range records[0] {
				headers[normalizeImportHeader(header)] = i
			}
		}
	}
	var result []importURL
	for _, record := range records[1:] {
		link := csvField(record, headers, "url", "sourceurl", "articleurl", "href", "link")
		if !validImportURL(link) {
			continue
		}
		title := csvField(record, headers, "title", "booktitle", "documenttitle", "article")
		result = append(result, importURL{URL: link, Title: title, Source: source})
	}
	return dedupeImportURLs(result)
}

func hasImportHeader(headers map[string]int, keys ...string) bool {
	for _, key := range keys {
		if _, ok := headers[key]; ok {
			return true
		}
	}
	return false
}

func normalizeImportHeader(value string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return -1
	}, value)
}

func csvField(record []string, headers map[string]int, keys ...string) string {
	for _, key := range keys {
		if index, ok := headers[key]; ok && index >= 0 && index < len(record) {
			if value := strings.TrimSpace(record[index]); value != "" {
				return value
			}
		}
	}
	return ""
}

func dedupeImportURLs(items []importURL) []importURL {
	seen := map[string]bool{}
	var result []importURL
	for _, item := range items {
		key := strings.TrimSpace(item.URL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item)
	}
	return result
}

func detectImportSource(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "<opml"):
		return "opml"
	case strings.Contains(lower, "<rss"):
		return "rss"
	case strings.Contains(lower, "<feed") && strings.Contains(lower, "http://www.w3.org/2005/atom"):
		return "atom"
	case strings.Contains(lower, "readwise"):
		return "readwise"
	case strings.Contains(lower, "kindle"):
		return "kindle"
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

func intValue(value any) int {
	switch raw := value.(type) {
	case int:
		return raw
	case int64:
		return int(raw)
	case float64:
		return int(raw)
	case json.Number:
		parsed, _ := raw.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func intValueDefault(value any, fallback int) int {
	if value == nil {
		return fallback
	}
	if parsed := intValue(value); parsed != 0 {
		return parsed
	}
	return fallback
}

func boolValue(value any) bool {
	raw, _ := value.(bool)
	return raw
}

func listValue(value any) []any {
	raw, _ := value.([]any)
	return raw
}

func stringSlice(value any) []string {
	if typed, ok := value.([]string); ok {
		return typed
	}
	values := []string{}
	for _, item := range listValue(value) {
		if text := strings.TrimSpace(stringValue(item)); text != "" {
			values = append(values, text)
		}
	}
	return values
}

func mapList(value any) []map[string]any {
	if mapped, ok := value.([]map[string]any); ok {
		return mapped
	}
	items := []map[string]any{}
	for _, item := range listValue(value) {
		if mapped, ok := item.(map[string]any); ok {
			items = append(items, mapped)
		}
	}
	return items
}

func tagNames(value any) []string {
	names := []string{}
	for _, tag := range mapList(value) {
		if name := stringValue(tag["name"]); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func jsonString(value any) string {
	if value == nil {
		return "{}"
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 20000 {
		return "{}"
	}
	return string(raw)
}

func jsonListString(value any) string {
	if value == nil {
		return "[]"
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 20000 {
		return "[]"
	}
	return string(raw)
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

func obsidianFileName(title, id string) string {
	base := fallback(markdownText(title), "Untitled")
	base = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		default:
			return r
		}
	}, base)
	base = strings.Trim(strings.Join(strings.Fields(base), " "), ". ")
	if len(base) > 80 {
		base = strings.TrimSpace(base[:80])
	}
	if id != "" {
		base += "-" + id[:min(len(id), 8)]
	}
	return fallback(base, "Untitled") + ".md"
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
