package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/browsercapture"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/sanitize"
	"golang.org/x/net/html"
)

type storedCaptureMedia struct {
	id        string
	sourceURL string
}

func (s *Service) storeCaptureMedia(ctx context.Context, userID, bookmarkID, attemptID, batchID string, media []browsercapture.V2Media) ([]storedCaptureMedia, error) {
	if len(media) == 0 {
		return nil, nil
	}
	if s.assets == nil || attemptID == "" {
		return nil, errors.New("capture media store unavailable")
	}
	if err := s.beginCaptureMediaBatch(ctx, userID, bookmarkID, attemptID, batchID); err != nil {
		return nil, err
	}
	stored := make([]storedCaptureMedia, 0, len(media))
	var firstError error
	recordError := func(err error) {
		if firstError == nil {
			firstError = err
		}
	}
	for ordinal, item := range media {
		file, err := os.Open(item.Path)
		if err != nil {
			recordError(err)
			continue
		}
		key, digest, size, putErr := s.assets.Put(io.LimitReader(file, s.browser.MaxMediaFileBytes+1))
		_ = file.Close()
		if putErr != nil {
			recordError(putErr)
			continue
		}
		if size != item.Size {
			recordError(errors.New("captured media size changed"))
			continue
		}
		id := ids.New()
		if err := s.commitMedia(ctx, id, userID, bookmarkID, attemptID, batchID, ordinal, item, size, digest, key); err != nil {
			recordError(err)
			continue
		}
		stored = append(stored, storedCaptureMedia{id: id, sourceURL: item.SourceURL})
	}
	return stored, firstError
}

func (s *Service) beginCaptureMediaBatch(ctx context.Context, userID, bookmarkID, attemptID, batchID string) error {
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var currentAttempt string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM capture_attempts WHERE user_id=? AND bookmark_id=? ORDER BY queued_at DESC,rowid DESC LIMIT 1`, userID, bookmarkID).Scan(&currentAttempt); err != nil {
		return err
	}
	if currentAttempt != attemptID {
		return errors.New("capture attempt superseded")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bookmark_media SET deleted_at=? WHERE user_id=? AND bookmark_id=? AND is_staged=1 AND capture_batch_id<>?`, nowString(), userID, bookmarkID, batchID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE artifacts SET deleted_at=? WHERE user_id=? AND bookmark_id=? AND is_staged=1 AND capture_batch_id<>?`, nowString(), userID, bookmarkID, batchID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) discardCaptureMedia(ctx context.Context, userID, bookmarkID, batchID string) {
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
	_, _ = s.db.ExecContext(ctx, `UPDATE bookmark_media SET deleted_at=? WHERE user_id=? AND bookmark_id=? AND capture_batch_id=? AND is_staged=1`, nowString(), userID, bookmarkID, batchID)
	_, _ = s.db.ExecContext(ctx, `UPDATE artifacts SET deleted_at=? WHERE user_id=? AND bookmark_id=? AND capture_batch_id=? AND is_staged=1`, nowString(), userID, bookmarkID, batchID)
}

func (s *Service) commitMedia(ctx context.Context, id, userID, bookmarkID, attemptID, batchID string, ordinal int, media browsercapture.V2Media, size int64, digest, key string) error {
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var currentAttempt string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM capture_attempts WHERE user_id=? AND bookmark_id=? ORDER BY queued_at DESC,rowid DESC LIMIT 1`, userID, bookmarkID).Scan(&currentAttempt); err != nil {
		return err
	}
	if currentAttempt != attemptID {
		return errors.New("capture attempt superseded")
	}
	var used int64
	if used, err = usedAssetBytesReplacingBookmark(ctx, tx, userID, bookmarkID); err != nil {
		return err
	}
	if s.artifactQuota > 0 && (size > s.artifactQuota || used > s.artifactQuota-size) {
		return ErrArtifactQuota
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO bookmark_media(id,user_id,bookmark_id,capture_attempt_id,capture_batch_id,source_url,media_role,mime_type,byte_size,sha256,storage_key,width,height,ordinal,is_staged,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?)`, id, userID, bookmarkID, attemptID, batchID, media.SourceURL, media.Role, media.MIME, size, digest, key, media.Width, media.Height, ordinal, nowString())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func linkCaptureMediaTx(ctx context.Context, tx *sql.Tx, userID, bookmarkID, attemptID, batchID, evidenceID string, media []storedCaptureMedia) error {
	for _, item := range media {
		result, err := tx.ExecContext(ctx, `UPDATE bookmark_media SET evidence_id=? WHERE id=? AND user_id=? AND bookmark_id=? AND capture_attempt_id=? AND capture_batch_id=? AND is_staged=1`, evidenceID, item.id, userID, bookmarkID, attemptID, batchID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return errors.New("captured media ownership changed")
		}
	}
	return nil
}

func (s *Service) storeRenderedEvidenceWithMedia(ctx context.Context, userID, bookmarkID, attemptID, batchID string, evidence BookmarkEvidence, media []storedCaptureMedia) (BookmarkEvidence, error) {
	evidence, reasons, now := prepareEvidence(bookmarkID, evidence)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BookmarkEvidence{}, err
	}
	defer tx.Rollback()
	stored, err := upsertEvidenceTx(ctx, tx, userID, bookmarkID, evidence, reasons, now)
	if err != nil {
		return BookmarkEvidence{}, err
	}
	if err := linkCaptureMediaTx(ctx, tx, userID, bookmarkID, attemptID, batchID, stored.ID, media); err != nil {
		return BookmarkEvidence{}, err
	}
	if err := tx.Commit(); err != nil {
		return BookmarkEvidence{}, err
	}
	return stored, nil
}

func rewriteReaderMedia(input, baseURL string, media []storedCaptureMedia) string {
	refs := make(map[string]string, len(media))
	for _, item := range media {
		refs[item.sourceURL] = item.id
	}
	root, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return input
	}
	base, _ := url.Parse(baseURL)
	var rewrite func(*html.Node)
	rewrite = func(node *html.Node) {
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "img") {
			for i := range node.Attr {
				if !strings.EqualFold(node.Attr[i].Key, "src") {
					continue
				}
				source, parseErr := url.Parse(strings.TrimSpace(node.Attr[i].Val))
				if parseErr == nil && base != nil {
					source = base.ResolveReference(source)
				}
				if id := refs[source.String()]; id != "" {
					node.Attr[i].Val = "/api/media/" + id
				} else {
					node.Attr[i].Val = ""
				}
				break
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			rewrite(child)
		}
	}
	rewrite(root)
	var out strings.Builder
	if err := html.Render(&out, root); err != nil {
		return input
	}
	return out.String()
}

func portableReaderHTML(input string) string {
	return sanitize.HTML(rewriteReaderMedia(input, "", nil))
}

func (s *Service) MediaContent(w http.ResponseWriter, r *http.Request, user auth.User) {
	var key, mime string
	var size int64
	err := s.db.QueryRowContext(r.Context(), `SELECT storage_key,mime_type,byte_size FROM bookmark_media WHERE id=? AND user_id=? AND is_staged=0 AND deleted_at IS NULL`, r.PathValue("id"), user.ID).Scan(&key, &mime, &size)
	if errors.Is(err, sql.ErrNoRows) || s.assets == nil {
		writeError(w, http.StatusNotFound, "Media not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load media")
		return
	}
	s.writeMediaContent(w, key, mime, size, "private, max-age=31536000, immutable")
}

func (s *Service) writeMediaContent(w http.ResponseWriter, key, mime string, size int64, cacheControl string) {
	file, err := s.assets.Open(key)
	if err != nil {
		writeError(w, http.StatusNotFound, "Media not found")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", fmt.Sprint(size))
	w.Header().Set("Content-Disposition", `inline; filename="reader-image"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = io.Copy(w, file)
}
