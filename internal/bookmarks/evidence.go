package bookmarks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/sanitize"
)

// BookmarkEvidence is one immutable source or extraction context retained for a
// bookmark. The bookmark's legacy text_content and sanitized_html columns remain
// the reader projection; evidence rows preserve what that projection came from.
type BookmarkEvidence struct {
	ID               string
	BookmarkID       string
	Kind             string
	Origin           string
	Authority        int
	Text             string
	SanitizedHTML    string
	CanonicalURL     string
	AuthorID         string
	PublisherKey     string
	PublishedAt      string
	ExtractionMethod string
	ContentHash      string
	QualityStatus    string
	QualityScore     int
	QualityReasons   []string
	ExtractorVersion string
	Selected         bool
	CreatedAt        string
	UpdatedAt        string
}

// UpsertEvidence retains an evidence context without allowing a caller to cross
// bookmark ownership boundaries. Evidence identity is the bookmark, kind,
// content hash, and extractor version so retries are idempotent while new
// extraction versions remain auditable.
func (s *Service) UpsertEvidence(ctx context.Context, userID, bookmarkID string, evidence BookmarkEvidence) (BookmarkEvidence, error) {
	var owner string
	if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM bookmarks WHERE id=? AND user_id=?`, bookmarkID, userID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BookmarkEvidence{}, errors.New("bookmark not found")
		}
		return BookmarkEvidence{}, err
	}
	return s.upsertEvidence(ctx, userID, bookmarkID, evidence)
}

func (s *Service) upsertEvidence(ctx context.Context, userID, bookmarkID string, evidence BookmarkEvidence) (BookmarkEvidence, error) {
	evidence.ID = fallback(evidence.ID, ids.New())
	evidence.BookmarkID = bookmarkID
	evidence.Kind = fallback(evidence.Kind, "legacy_scrape")
	evidence.Origin = fallback(evidence.Origin, "legacy")
	evidence.QualityStatus = fallback(evidence.QualityStatus, "failed")
	if evidence.QualityScore < 0 {
		evidence.QualityScore = 0
	} else if evidence.QualityScore > 100 {
		evidence.QualityScore = 100
	}
	evidence.QualityReasons = cleanReasonCodes(evidence.QualityReasons)
	evidence.SanitizedHTML = sanitize.HTML(evidence.SanitizedHTML)
	evidence.ContentHash = fallback(evidence.ContentHash, evidenceContentHash(evidence))
	now := time.Now().UTC().Format(time.RFC3339)
	evidence.CreatedAt = fallback(evidence.CreatedAt, now)
	evidence.UpdatedAt = fallback(evidence.UpdatedAt, evidence.CreatedAt)
	reasons, _ := json.Marshal(evidence.QualityReasons)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BookmarkEvidence{}, err
	}
	defer tx.Rollback()
	if evidence.Selected {
		if _, err := tx.ExecContext(ctx, `UPDATE bookmark_evidence SET is_selected=0,updated_at=? WHERE bookmark_id=? AND user_id=? AND is_selected=1`, now, bookmarkID, userID); err != nil {
			return BookmarkEvidence{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,authority,content_text,sanitized_html,canonical_url,author_id,publisher_key,published_at,extraction_method,content_hash,quality_status,quality_score,quality_reasons_json,extractor_version,is_selected,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(bookmark_id,evidence_kind,content_hash,extractor_version) DO UPDATE SET
			evidence_origin=excluded.evidence_origin,authority=excluded.authority,content_text=excluded.content_text,sanitized_html=excluded.sanitized_html,canonical_url=excluded.canonical_url,author_id=excluded.author_id,publisher_key=excluded.publisher_key,published_at=excluded.published_at,extraction_method=excluded.extraction_method,quality_status=excluded.quality_status,quality_score=CASE WHEN excluded.quality_score=0 AND excluded.quality_status=bookmark_evidence.quality_status THEN bookmark_evidence.quality_score ELSE excluded.quality_score END,quality_reasons_json=excluded.quality_reasons_json,is_selected=CASE WHEN excluded.is_selected=1 THEN 1 ELSE bookmark_evidence.is_selected END,updated_at=excluded.updated_at`,
		evidence.ID, bookmarkID, userID, evidence.Kind, evidence.Origin, evidence.Authority, evidence.Text, evidence.SanitizedHTML, evidence.CanonicalURL, evidence.AuthorID, evidence.PublisherKey, nullableStringValue(evidence.PublishedAt), evidence.ExtractionMethod, evidence.ContentHash, evidence.QualityStatus, evidence.QualityScore, string(reasons), evidence.ExtractorVersion, evidence.Selected, evidence.CreatedAt, evidence.UpdatedAt)
	if err != nil {
		return BookmarkEvidence{}, err
	}
	row := tx.QueryRowContext(ctx, `SELECT id,bookmark_id,evidence_kind,evidence_origin,authority,content_text,sanitized_html,canonical_url,author_id,publisher_key,COALESCE(published_at,''),extraction_method,content_hash,quality_status,quality_score,quality_reasons_json,extractor_version,is_selected,created_at,updated_at FROM bookmark_evidence WHERE bookmark_id=? AND user_id=? AND evidence_kind=? AND content_hash=? AND extractor_version=?`, bookmarkID, userID, evidence.Kind, evidence.ContentHash, evidence.ExtractorVersion)
	stored, err := scanEvidence(row)
	if err != nil {
		return BookmarkEvidence{}, err
	}
	if err := tx.Commit(); err != nil {
		return BookmarkEvidence{}, err
	}
	return stored, nil
}

func evidenceContentHash(evidence BookmarkEvidence) string {
	content := strings.TrimSpace(evidence.Text)
	if content == "" {
		content = strings.TrimSpace(evidence.SanitizedHTML)
	}
	if content == "" {
		content = strings.TrimSpace(evidence.CanonicalURL)
	}
	if content == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

// Evidence returns only evidence owned by userID for the requested bookmark.
func (s *Service) Evidence(ctx context.Context, userID, bookmarkID string) ([]BookmarkEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,bookmark_id,evidence_kind,evidence_origin,authority,content_text,sanitized_html,canonical_url,author_id,publisher_key,COALESCE(published_at,''),extraction_method,content_hash,quality_status,quality_score,quality_reasons_json,extractor_version,is_selected,created_at,updated_at FROM bookmark_evidence WHERE bookmark_id=? AND user_id=? ORDER BY is_selected DESC,authority DESC,created_at ASC,id ASC`, bookmarkID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []BookmarkEvidence{}
	for rows.Next() {
		evidence, err := scanEvidence(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, evidence)
	}
	return result, rows.Err()
}

func scanEvidence(row scanner) (BookmarkEvidence, error) {
	var evidence BookmarkEvidence
	var reasons string
	var selected bool
	err := row.Scan(&evidence.ID, &evidence.BookmarkID, &evidence.Kind, &evidence.Origin, &evidence.Authority, &evidence.Text, &evidence.SanitizedHTML, &evidence.CanonicalURL, &evidence.AuthorID, &evidence.PublisherKey, &evidence.PublishedAt, &evidence.ExtractionMethod, &evidence.ContentHash, &evidence.QualityStatus, &evidence.QualityScore, &reasons, &evidence.ExtractorVersion, &selected, &evidence.CreatedAt, &evidence.UpdatedAt)
	if err != nil {
		return BookmarkEvidence{}, err
	}
	evidence.Selected = selected
	_ = json.Unmarshal([]byte(reasons), &evidence.QualityReasons)
	if evidence.QualityReasons == nil {
		evidence.QualityReasons = []string{}
	}
	return evidence, nil
}

func cleanReasonCodes(reasons []string) []string {
	clean := make([]string, 0, len(reasons))
	seen := map[string]bool{}
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason == "" || seen[reason] {
			continue
		}
		seen[reason] = true
		clean = append(clean, reason)
	}
	return clean
}
