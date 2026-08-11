package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

var ErrCollectionNotFound = errors.New("collection not found")

type CreateBookmarkInput struct {
	UserID, URL, Title, Domain, CollectionID, Note, Quote string
	Tags                                                  []string
}

type CreateBookmarkResult struct{ BookmarkID, JobID string }

// CreateBookmark commits the bookmark and every required initial companion as
// one aggregate write. Callers may safely read and refresh projections after it returns.
func (s *Service) CreateBookmark(ctx context.Context, in CreateBookmarkInput) (CreateBookmarkResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreateBookmarkResult{}, err
	}
	defer tx.Rollback()
	if in.CollectionID != "" {
		var found int
		if err = tx.QueryRowContext(ctx, `SELECT 1 FROM collections WHERE id=? AND user_id=?`, in.CollectionID, in.UserID).Scan(&found); errors.Is(err, sql.ErrNoRows) {
			return CreateBookmarkResult{}, ErrCollectionNotFound
		}
		if err != nil {
			return CreateBookmarkResult{}, err
		}
	}
	now, bookmarkID, attemptID := nowString(), ids.New(), ids.New()
	if _, err = tx.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,domain,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, bookmarkID, in.UserID, in.URL, in.Title, in.Domain, now, now); err != nil {
		return CreateBookmarkResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO capture_attempts(id,bookmark_id,user_id,status,requested_url,engine,engine_version,queued_at) VALUES(?,?,?,'queued',?,'direct_http',?,?)`, attemptID, bookmarkID, in.UserID, in.URL, safefetch.ExtractorVersion, now); err != nil {
		return CreateBookmarkResult{}, err
	}
	jobID, err := s.enqueueCreate(ctx, tx, in.UserID, "bookmark.process", bookmarkProcessPayload(bookmarkID, in.URL, "", attemptID))
	if err != nil {
		return CreateBookmarkResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,'pending',?,?)`, ids.New(), bookmarkID, in.UserID, now, now); err != nil {
		return CreateBookmarkResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO item_states(user_id,item_type,item_id,stage,importance,next_action,created_at,updated_at) VALUES(?,'bookmark',?,'inbox',0,?,?,?)`, in.UserID, bookmarkID, strings.TrimSpace(in.Note), now, now); err != nil {
		return CreateBookmarkResult{}, err
	}
	if in.CollectionID != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO collection_bookmarks(collection_id,bookmark_id,user_id,added_at) VALUES(?,?,?,?)`, in.CollectionID, bookmarkID, in.UserID, now); err != nil {
			return CreateBookmarkResult{}, err
		}
	}
	tags := cleanStringList(in.Tags, 20)
	seenTagSlugs := map[string]bool{}
	for _, name := range tags {
		tagID, slug := ids.New(), tagSlug(name)
		if slug == "" || seenTagSlugs[slug] {
			continue
		}
		seenTagSlugs[slug] = true
		if _, err = tx.ExecContext(ctx, `INSERT INTO tags(id,user_id,name,slug,source,created_at,updated_at) VALUES(?,?,?,?,'manual',?,?) ON CONFLICT(user_id,slug) DO UPDATE SET updated_at=excluded.updated_at`, tagID, in.UserID, name, slug, now, now); err != nil {
			return CreateBookmarkResult{}, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id=? AND slug=?`, in.UserID, slug).Scan(&tagID); err != nil {
			return CreateBookmarkResult{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO bookmark_tags(bookmark_id,tag_id,user_id,source,created_at) VALUES(?,?,?,'manual',?)`, bookmarkID, tagID, in.UserID, now); err != nil {
			return CreateBookmarkResult{}, err
		}
	}
	if strings.TrimSpace(in.Note) != "" || strings.TrimSpace(in.Quote) != "" {
		tagJSON, _ := json.Marshal(tags)
		if _, err = tx.ExecContext(ctx, `INSERT INTO annotations(id,user_id,bookmark_id,quote,note,selector_json,tags_json,created_at,updated_at) VALUES(?,?,?,?,?,'{}',?,?,?)`, ids.New(), in.UserID, bookmarkID, strings.TrimSpace(in.Quote), strings.TrimSpace(in.Note), string(tagJSON), now, now); err != nil {
			return CreateBookmarkResult{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return CreateBookmarkResult{}, err
	}
	return CreateBookmarkResult{BookmarkID: bookmarkID, JobID: jobID}, nil
}

type SourceCaptureInput struct {
	UserID, URL, Title, Description, Domain, Source                string
	URLKey                                                         string
	SourceID, AuthorUsername, AuthorName, SourceURL, MetricsJSON   string
	ContentKind, PublishedAt, AuthorID, PublisherKey, FetchVersion string
	Primary                                                        BookmarkEvidence
	Supporting                                                     []BookmarkEvidence
}

func (s *Service) InsertSourceCapture(ctx context.Context, in SourceCaptureInput) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now, id := nowString(), ids.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO bookmarks(id,user_id,url,title,description,domain,text_content,read_status,source,x_tweet_id,x_author_username,x_author_name,x_tweet_url,x_metrics_json,canonical_url,content_kind,source_published_at,source_author_id,source_publisher_key,fetch_version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,0,?,?,?,?,?,?,?,?,NULLIF(?,''),NULLIF(?,''),?,?,?,?)`, id, in.UserID, in.URL, in.Title, in.Description, in.Domain, in.Primary.Text, in.Source, in.SourceID, in.AuthorUsername, in.AuthorName, in.SourceURL, in.MetricsJSON, in.URL, in.ContentKind, in.PublishedAt, in.AuthorID, in.PublisherKey, in.FetchVersion, now, now)
	if err != nil {
		return "", err
	}
	for _, evidence := range append([]BookmarkEvidence{in.Primary}, in.Supporting...) {
		evidence, reasons, prepared := prepareEvidence(id, evidence)
		if _, err = upsertEvidenceTx(ctx, tx, in.UserID, id, evidence, reasons, prepared); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,'pending',?,?)`, ids.New(), id, in.UserID, now, now); err != nil {
		return "", err
	}
	if _, err = s.enqueueCreate(ctx, tx, in.UserID, "bookmark.process", ProcessPayload(id, in.URL, "")); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (s *Service) RepairSourceCapture(ctx context.Context, bookmarkID string, in SourceCaptureInput, titleNeedsRepair, descriptionNeedsRepair bool) (bool, error) {
	s.sourceMu.Lock()
	defer s.sourceMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentURL, currentTitle, currentDescription, currentText, summaryVersion, enrichmentVersion string
	if err = tx.QueryRowContext(ctx, `SELECT url,COALESCE(title,''),COALESCE(description,''),COALESCE(text_content,''),summary_version,enrichment_version FROM bookmarks WHERE user_id=? AND id=?`, in.UserID, bookmarkID).Scan(&currentURL, &currentTitle, &currentDescription, &currentText, &summaryVersion, &enrichmentVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var evidenceCount int
	prepared, reasons, preparedAt := prepareEvidence(bookmarkID, in.Primary)
	prepared.Selected = true
	_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmark_evidence WHERE bookmark_id=? AND user_id=? AND evidence_kind=? AND evidence_origin=? AND extractor_version=? AND content_text=? AND quality_status=?`, bookmarkID, in.UserID, prepared.Kind, prepared.Origin, prepared.ExtractorVersion, prepared.Text, prepared.QualityStatus).Scan(&evidenceCount)
	incomingURLKey := in.URLKey
	if incomingURLKey == "" {
		incomingURLKey = normalizeDedupURL(in.URL)
	}
	sameURL := currentURL == in.URL || (incomingURLKey != "" && normalizeDedupURL(currentURL) == incomingURLKey)
	if evidenceCount > 0 && currentText == in.Primary.Text && !titleNeedsRepair && !descriptionNeedsRepair && summaryVersion == providers.SummaryPromptVersion && enrichmentVersion == providers.SemanticVersion && sameURL {
		return false, nil
	}
	if _, err = upsertEvidenceTx(ctx, tx, in.UserID, bookmarkID, prepared, reasons, preparedAt); err != nil {
		return false, err
	}
	now := nowString()
	_, err = tx.ExecContext(ctx, `UPDATE bookmarks SET title=CASE WHEN title='' OR title=url OR ? THEN ? ELSE title END,description=CASE WHEN description='' OR ? THEN ? ELSE description END,url=?,canonical_url=?,domain=?,text_content=?,x_tweet_id=?,x_author_username=?,x_author_name=?,x_tweet_url=?,x_metrics_json=?,content_kind=?,source_published_at=NULLIF(?,''),source_author_id=NULLIF(?,''),source_publisher_key=?,fetch_version=?,summary_version='',enrichment_version='',updated_at=? WHERE id=? AND user_id=?`, titleNeedsRepair, in.Title, descriptionNeedsRepair, in.Description, in.URL, in.URL, in.Domain, in.Primary.Text, in.SourceID, in.AuthorUsername, in.AuthorName, in.SourceURL, in.MetricsJSON, in.ContentKind, in.PublishedAt, in.AuthorID, in.PublisherKey, in.FetchVersion, now, bookmarkID, in.UserID)
	if err != nil {
		return false, err
	}
	var active int
	if sameURL {
		rows, queryErr := tx.QueryContext(ctx, `SELECT COALESCE(json_extract(payload_json,'$.url_key'),''),COALESCE(json_extract(payload_json,'$.url'),''),COALESCE(json_extract(payload_json,'$.expected_evidence_hash'),'') FROM jobs WHERE user_id=? AND type='bookmark.process' AND status='queued' AND json_extract(payload_json,'$.bookmark_id')=?`, in.UserID, bookmarkID)
		if queryErr != nil {
			return false, queryErr
		}
		for rows.Next() {
			var jobURLKey, jobURL, expectedHash string
			if err = rows.Scan(&jobURLKey, &jobURL, &expectedHash); err != nil {
				rows.Close()
				return false, err
			}
			if jobURLKey == "" {
				jobURLKey = normalizeDedupURL(jobURL)
			}
			if jobURLKey == incomingURLKey && (expectedHash == "" || expectedHash == prepared.ContentHash) {
				active++
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
	}
	if active == 0 {
		if _, err = s.enqueueCreate(ctx, tx, in.UserID, "bookmark.process", ProcessPayload(bookmarkID, in.URL, "")); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

type CreateNoteInput struct {
	UserID, Title, Body, BookmarkID string
}

// CreateNoteCommand owns the note aggregate write, including its initial state
// and optional bookmark association.
func (s *Service) CreateNoteCommand(ctx context.Context, in CreateNoteInput) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if in.BookmarkID != "" {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM bookmarks WHERE id=? AND user_id=?`, in.BookmarkID, in.UserID).Scan(&exists); err != nil {
			return "", err
		}
	}
	now, id := nowString(), ids.New()
	if _, err = tx.ExecContext(ctx, `INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, in.UserID, in.Title, in.Body, "manual", now, now); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO item_states(user_id,item_type,item_id,stage,importance,next_action,created_at,updated_at) VALUES(?,'note',?,'inbox',0,'',?,?)`, in.UserID, id, now, now); err != nil {
		return "", err
	}
	if in.BookmarkID != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO bookmark_notes(bookmark_id,note_id,user_id,created_at) VALUES(?,?,?,?)`, in.BookmarkID, id, in.UserID, now); err != nil {
			return "", err
		}
	}
	return id, tx.Commit()
}

func (s *Service) DeleteItemCommand(ctx context.Context, userID, itemType, itemID string) (bool, error) {
	deleted, err := s.DeleteItemsCommand(ctx, userID, itemType, []string{itemID})
	return deleted > 0, err
}

func (s *Service) DeleteItemsCommand(ctx context.Context, userID, itemType string, itemIDs []string) (int, error) {
	if itemType != "bookmark" && itemType != "note" {
		return 0, errors.New("invalid item type")
	}
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var ftsTableCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='search_fts'`).Scan(&ftsTableCount); err != nil {
		return 0, err
	}
	table := "notes"
	if itemType == "bookmark" {
		table = "bookmarks"
	}
	deleted := 0
	for _, itemID := range itemIDs {
		res, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=? AND user_id=?`, itemID, userID)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if n == 0 {
			continue
		}
		deleted++
		if err = cleanupDeletedItemTx(ctx, tx, userID, itemType, itemID, ftsTableCount > 0); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func cleanupDeletedItemTx(ctx context.Context, tx *sql.Tx, userID, itemType, itemID string, ftsEnabled bool) error {
	for _, statement := range []string{
		`DELETE FROM action_items WHERE user_id=? AND item_type=? AND item_id=?`,
		`DELETE FROM reminders WHERE user_id=? AND item_type=? AND item_id=?`,
		`DELETE FROM item_states WHERE user_id=? AND item_type=? AND item_id=?`,
		`DELETE FROM review_events WHERE user_id=? AND item_type=? AND item_id=?`,
		`DELETE FROM result_feedback WHERE user_id=? AND item_type=? AND item_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, userID, itemType, itemID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_links WHERE user_id=? AND ((from_type=? AND from_id=?) OR (to_type=? AND to_id=?))`, userID, itemType, itemID, itemType, itemID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE user_id=? AND item_type=? AND item_id=?`, userID, itemType, itemID); err != nil {
		return err
	}
	if ftsEnabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_fts WHERE user_id=? AND item_type=? AND item_id=?`, userID, itemType, itemID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteUserCommand owns account deletion and projections that SQLite foreign
// keys cannot cascade, serialized with search projection rebuilds.
func (s *Service) DeleteUserCommand(ctx context.Context, userID string) (int, bool, error) {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var bookmarkCount, ftsTableCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks WHERE user_id=?`, userID).Scan(&bookmarkCount); err != nil {
		return 0, false, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='search_fts'`).Scan(&ftsTableCount); err != nil {
		return 0, false, err
	}
	if ftsTableCount > 0 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM search_fts WHERE user_id=?`, userID); err != nil {
			return 0, false, err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, userID)
	if err != nil {
		return 0, false, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if err = tx.Commit(); err != nil {
		return 0, false, err
	}
	return bookmarkCount, deleted > 0, nil
}

type RetryError struct {
	Status        int
	Detail, JobID string
}

func (e *RetryError) Error() string { return e.Detail }

func (s *Service) RetryFailedProcess(ctx context.Context, actorUserID, failedJobID string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var userID sql.NullString
	var jobType, status, payload string
	var priority, maxAttempts int
	err = tx.QueryRowContext(ctx, `SELECT user_id,type,status,priority,payload_json,max_attempts FROM jobs WHERE id=?`, failedJobID).Scan(&userID, &jobType, &status, &priority, &payload, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &RetryError{Status: http.StatusNotFound, Detail: "Job not found"}
	}
	if err != nil {
		return "", err
	}
	if status != "failed" {
		return "", &RetryError{Status: http.StatusConflict, Detail: "Only failed jobs can be retried"}
	}
	if jobType != "bookmark.process" {
		return "", &RetryError{Status: http.StatusConflict, Detail: "This job type cannot be safely retried from Administration"}
	}
	parsed, err := ParseProcessPayload(payload)
	if err != nil {
		return "", &RetryError{Status: http.StatusConflict, Detail: "Job payload is invalid and cannot be retried"}
	}
	if parsed.BookmarkID == "" {
		return "", &RetryError{Status: http.StatusConflict, Detail: "Bookmark job has no bookmark ID"}
	}
	var owner, rawURL string
	if err = tx.QueryRowContext(ctx, `SELECT user_id,url FROM bookmarks WHERE id=?`, parsed.BookmarkID).Scan(&owner, &rawURL); errors.Is(err, sql.ErrNoRows) {
		return "", &RetryError{Status: http.StatusConflict, Detail: "The bookmark no longer exists"}
	} else if err != nil {
		return "", err
	}
	var active string
	err = tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE type='bookmark.process' AND status IN ('queued','leased') AND json_extract(CASE WHEN json_valid(payload_json) THEN payload_json ELSE '{}' END,'$.bookmark_id')=? ORDER BY created_at LIMIT 1`, parsed.BookmarkID).Scan(&active)
	if err == nil {
		return "", &RetryError{Status: http.StatusConflict, Detail: "This bookmark already has an active job", JobID: active}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	now, attemptID := nowString(), ids.New()
	var retryOf string
	_ = tx.QueryRowContext(ctx, `SELECT id FROM capture_attempts WHERE bookmark_id=? AND user_id=? ORDER BY queued_at DESC,id DESC LIMIT 1`, parsed.BookmarkID, owner).Scan(&retryOf)
	if _, err = tx.ExecContext(ctx, `INSERT INTO capture_attempts(id,bookmark_id,user_id,retry_of_id,status,requested_url,engine,engine_version,queued_at) VALUES(?,?,?,NULLIF(?,''),'queued',?,'direct_http',?,?)`, attemptID, parsed.BookmarkID, owner, retryOf, rawURL, safefetch.ExtractorVersion, now); err != nil {
		return "", err
	}
	newID := ids.New()
	payload = bookmarkProcessPayload(parsed.BookmarkID, rawURL, "", attemptID)
	if _, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,user_id,type,status,priority,payload_json,max_attempts,run_after,created_at,updated_at) VALUES(?,?,?,'queued',?,?,?,?,?,?)`, newID, owner, jobType, priority, payload, maxAttempts, now, now, now); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_summaries SET processing_status='pending',updated_at=? WHERE bookmark_id=?`, now, parsed.BookmarkID); err != nil {
		return "", err
	}
	meta, _ := json.Marshal(map[string]any{"new_job_id": newID, "job_type": jobType, "bookmark_id": parsed.BookmarkID})
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_user_id,action,target_type,target_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, ids.New(), actorUserID, "admin.job.retry", "job", failedJobID, string(meta), now); err != nil {
		return "", err
	}
	return newID, tx.Commit()
}

type ProcessJobPayload struct {
	BookmarkID       string `json:"bookmark_id"`
	URL              string `json:"url"`
	URLKey           string `json:"url_key,omitempty"`
	ImportJobID      string `json:"import_job_id,omitempty"`
	CaptureAttemptID string `json:"capture_attempt_id,omitempty"`
}

func ParseProcessPayload(raw string) (ProcessJobPayload, error) {
	var p ProcessJobPayload
	err := json.Unmarshal([]byte(raw), &p)
	return p, err
}
