package bookmarks

import (
	"archive/zip"
	"bytes"
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
	"github.com/glnarayanan/arivu/internal/browsercapture"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
	"github.com/glnarayanan/arivu/internal/sanitize"
)

var hrefPattern = regexp.MustCompile(`(?i)href=["']([^"']+)["']`)
var errPartialExtraction = errors.New("partial content extraction")

type qualityProcessMeta struct {
	RunID                string
	ExpectedEvidenceHash string
}

func (s *Service) ProcessJob(ctx context.Context, jobType string, payload string) error {
	if jobType == "feed.poll" {
		var b struct {
			SubscriptionID string `json:"subscription_id"`
		}
		if json.Unmarshal([]byte(payload), &b) != nil || b.SubscriptionID == "" {
			return errors.New("invalid feed poll payload")
		}
		return s.pollFeed(ctx, b.SubscriptionID)
	}
	switch jobType {
	case "bookmark.process":
		var body struct {
			BookmarkID           string `json:"bookmark_id"`
			URL                  string `json:"url"`
			ImportJobID          string `json:"import_job_id"`
			QualityRunID         string `json:"quality_reprocess_run_id"`
			ExpectedEvidenceHash string `json:"expected_evidence_hash"`
			AttemptID            string `json:"capture_attempt_id"`
		}
		if err := json.Unmarshal([]byte(payload), &body); err != nil {
			return err
		}
		meta := qualityProcessMeta{RunID: body.QualityRunID, ExpectedEvidenceHash: body.ExpectedEvidenceHash}
		if !s.startQualityProcess(ctx, body.BookmarkID, meta) {
			return nil
		}
		attemptID := body.AttemptID
		if attemptID == "" {
			attemptID = s.ensureAttempt(ctx, body.BookmarkID, body.URL, "")
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE capture_attempts SET status='running',started_at=? WHERE id=? AND status='queued'`, nowString(), attemptID)
		err := s.processBookmarkAttempt(ctx, body.BookmarkID, body.URL, attemptID)
		status, code := "complete", ""
		if errors.Is(err, errPartialExtraction) {
			status = "partial"
		} else if err != nil {
			status, code = "failed", safefetch.FailureReason(err)
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE capture_attempts SET status=?,error_code=CASE WHEN ?='' THEN error_code ELSE ? END,finished_at=? WHERE id=?`, status, code, code, nowString(), attemptID)
		return s.finishBookmarkProcessWithMeta(ctx, body.BookmarkID, body.ImportJobID, meta, err)
	default:
		return fmt.Errorf("unknown job type %s", jobType)
	}
}

func (s *Service) finishBookmarkProcess(ctx context.Context, bookmarkID, importJobID string, err error) error {
	return s.finishBookmarkProcessWithMeta(ctx, bookmarkID, importJobID, qualityProcessMeta{}, err)
}

func (s *Service) finishBookmarkProcessWithMeta(ctx context.Context, bookmarkID, importJobID string, meta qualityProcessMeta, err error) error {
	if errors.Is(err, errPartialExtraction) {
		if importJobID != "" {
			s.recordImportJobProgress(ctx, bookmarkID, importJobID, 0, 0, 1)
		}
		s.completeQualityProcess(ctx, bookmarkID, meta, "partial", errPartialExtraction.Error())
		return nil
	}
	if err == nil && importJobID != "" {
		s.recordImportJobSuccess(ctx, bookmarkID, importJobID)
	}
	if err == nil {
		s.completeQualityProcess(ctx, bookmarkID, meta, "completed", "")
	}
	return err
}

func (s *Service) startQualityProcess(ctx context.Context, bookmarkID string, meta qualityProcessMeta) bool {
	if meta.RunID == "" {
		return true
	}
	if meta.ExpectedEvidenceHash != "" {
		var current string
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(content_hash,'') FROM bookmark_evidence WHERE bookmark_id=? AND is_selected=1`, bookmarkID).Scan(&current)
		if current != meta.ExpectedEvidenceHash {
			s.completeQualityProcess(ctx, bookmarkID, meta, "skipped", "selected_evidence_changed")
			return false
		}
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE quality_reprocess_items SET status='processing',updated_at=? WHERE run_id=? AND bookmark_id=?`, nowString(), meta.RunID, bookmarkID)
	_, _ = s.db.ExecContext(ctx, `UPDATE quality_reprocess_runs SET status='running',updated_at=? WHERE id=?`, nowString(), meta.RunID)
	return true
}

func (s *Service) completeQualityProcess(ctx context.Context, bookmarkID string, meta qualityProcessMeta, status, detail string) {
	if meta.RunID == "" {
		return
	}
	now := nowString()
	_, _ = s.db.ExecContext(ctx, `UPDATE quality_reprocess_items SET status=?,reason=CASE WHEN ?='' THEN reason ELSE ? END,last_error=CASE WHEN ?='failed' THEN ? ELSE last_error END,updated_at=? WHERE run_id=? AND bookmark_id=?`, status, detail, detail, status, detail, now, meta.RunID, bookmarkID)
	_, _ = s.db.ExecContext(ctx, `UPDATE quality_reprocess_runs SET
		completed_count=(SELECT COUNT(*) FROM quality_reprocess_items WHERE run_id=? AND status='completed'),
		partial_count=(SELECT COUNT(*) FROM quality_reprocess_items WHERE run_id=? AND status='partial'),
		failed_count=(SELECT COUNT(*) FROM quality_reprocess_items WHERE run_id=? AND status='failed'),
		skipped_count=(SELECT COUNT(*) FROM quality_reprocess_items WHERE run_id=? AND status='skipped'),
		queued_count=(SELECT COUNT(*) FROM quality_reprocess_items WHERE run_id=? AND status IN ('queued','processing')),
		status=CASE
			WHEN NOT EXISTS (SELECT 1 FROM quality_reprocess_items WHERE run_id=? AND status IN ('eligible','queued','processing'))
			THEN CASE
				WHEN NOT EXISTS (SELECT 1 FROM quality_reprocess_items WHERE run_id=? AND status<>'failed') THEN 'failed'
				WHEN EXISTS (SELECT 1 FROM quality_reprocess_items WHERE run_id=? AND status IN ('failed','partial')) THEN 'partial'
				ELSE 'completed' END
			ELSE 'running' END,
		updated_at=? WHERE id=?`, meta.RunID, meta.RunID, meta.RunID, meta.RunID, meta.RunID, meta.RunID, meta.RunID, meta.RunID, now, meta.RunID)
}

func (s *Service) processBookmark(ctx context.Context, bookmarkID string, rawURL string) error {
	return s.processBookmarkAttempt(ctx, bookmarkID, rawURL, "")
}

func (s *Service) processBookmarkAttempt(ctx context.Context, bookmarkID string, rawURL string, attemptID string) error {
	userID, ok := s.bookmarkOwner(ctx, bookmarkID)
	if !ok {
		return errors.New("bookmark not found")
	}
	var source, currentTitle, currentDescription, currentDomain, contentKind, tweetURL string
	if err := s.db.QueryRowContext(ctx, `SELECT source,COALESCE(title,''),COALESCE(description,''),COALESCE(domain,''),COALESCE(content_kind,''),COALESCE(x_tweet_url,'') FROM bookmarks WHERE id=? AND user_id=?`, bookmarkID, userID).Scan(&source, &currentTitle, &currentDescription, &currentDomain, &contentKind, &tweetURL); err != nil {
		return err
	}
	if source == "x" {
		return s.processXBookmark(ctx, userID, bookmarkID, rawURL, currentTitle, currentDescription, currentDomain, contentKind, tweetURL)
	}
	result, err := s.fetcher.Fetch(ctx, rawURL)
	if err != nil {
		_, _ = s.UpsertEvidence(ctx, userID, bookmarkID, BookmarkEvidence{
			Kind: "fetched_article", Origin: "web_fetch", CanonicalURL: rawURL, ExtractionMethod: "generic_web",
			QualityStatus: "failed", QualityReasons: []string{safefetch.FailureReason(err)}, ExtractorVersion: safefetch.ExtractorVersion,
		})
		return err
	}
	evidence := BookmarkEvidence{
		Kind: "fetched_article", Origin: "web_fetch", Authority: 70, Text: result.Text, SanitizedHTML: result.HTML,
		CanonicalURL: result.URL, PublisherKey: result.Domain, ExtractionMethod: result.Quality.Method,
		QualityStatus: string(result.Quality.Status), QualityReasons: result.Quality.Reasons, ExtractorVersion: result.Quality.Version,
		Selected: false,
	}
	storedEvidence, err := s.UpsertEvidence(ctx, userID, bookmarkID, evidence)
	if err != nil {
		return err
	}
	if s.assets != nil && attemptID != "" && len(result.Body) > 0 {
		key, digest, size, storeErr := s.assets.Put(bytes.NewReader(result.Body))
		if storeErr != nil {
			return storeErr
		}
		mime := strings.TrimSpace(strings.Split(result.ContentType, ";")[0])
		if mime == "" {
			mime = "application/octet-stream"
		}
		storeErr = s.commitArtifact(ctx, ids.New(), userID, bookmarkID, attemptID, storedEvidence.ID, "source_response", mime, size, digest, key)
		if storeErr != nil {
			return storeErr
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE capture_attempts SET final_url=?,engine_version=? WHERE id=?`, result.URL, safefetch.ExtractorVersion, attemptID)
	}
	if result.Quality.Status != safefetch.QualityComplete {
		now := nowString()
		title := fallback(result.Title, result.Domain)
		if _, err := s.db.ExecContext(ctx, `UPDATE bookmarks SET canonical_url=?,title=CASE WHEN title='' OR title=domain OR title=url THEN ? ELSE title END,description=CASE WHEN description='' THEN ? ELSE description END,domain=?,processed_at=?,fetch_version=? WHERE id=? AND user_id=?`, result.URL, title, result.Description, result.Domain, now, safefetch.ExtractorVersion, bookmarkID, userID); err != nil {
			return err
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(bookmark_id) DO UPDATE SET processing_status=CASE WHEN ai_summaries.processing_status IN ('completed','fallback') THEN ai_summaries.processing_status ELSE excluded.processing_status END,updated_at=excluded.updated_at`, ids.New(), bookmarkID, userID, string(result.Quality.Status), now, now)
		if err != nil {
			return err
		}
		s.refreshSearchIndex(ctx, userID)
		return errPartialExtraction
	}
	if err := s.persistSelectedEvidence(ctx, userID, bookmarkID, fallback(result.Title, result.Domain), result.Description, result.Domain, storedEvidence); err != nil {
		return err
	}
	if s.browser.Enabled && attemptID != "" && s.assets != nil {
		err := browsercapture.Run(ctx, s.browser, result.URL, func(a browsercapture.Artifact, r io.Reader) error {
			key, digest, size, err := s.assets.Put(r)
			if err != nil {
				return err
			}
			return s.commitArtifact(ctx, ids.New(), userID, bookmarkID, attemptID, storedEvidence.ID, a.Type, a.MIME, size, digest, key)
		})
		if err != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE capture_attempts SET error_code=? WHERE id=?`, err.Error(), attemptID)
			return errPartialExtraction
		}
	}
	return nil
}

func (s *Service) processXBookmark(ctx context.Context, userID, bookmarkID, rawURL, title, description, domain, contentKind, tweetURL string) error {
	evidenceRows, err := s.Evidence(ctx, userID, bookmarkID)
	if err != nil {
		return err
	}
	var sourceEvidence BookmarkEvidence
	for _, evidence := range evidenceRows {
		if evidence.Kind == "source_post" && (evidence.Origin == "x_api" || evidence.ExtractionMethod == "x_api") {
			sourceEvidence = evidence
			if evidence.ExtractorVersion == "x-api-v1" {
				break
			}
		}
	}
	if sourceEvidence.ID == "" {
		if err := s.markInsufficientEvidence(ctx, bookmarkID, "failed"); err != nil {
			return err
		}
		return errPartialExtraction
	}
	isExternalArticle := strings.Contains(contentKind, "article") && normalizeProcessingURL(rawURL) != normalizeProcessingURL(tweetURL)
	if !isExternalArticle {
		if sourceEvidence.QualityStatus != "complete" || strings.TrimSpace(sourceEvidence.Text) == "" {
			if err := s.markInsufficientEvidence(ctx, bookmarkID, sourceEvidence.QualityStatus); err != nil {
				return err
			}
			if sourceEvidence.Origin == "x_api" && sourceEvidence.QualityStatus == "metadata_only" {
				return nil
			}
			return errPartialExtraction
		}
		sourceEvidence.Selected = false
		storedEvidence, err := s.UpsertEvidence(ctx, userID, bookmarkID, sourceEvidence)
		if err != nil {
			return err
		}
		return s.persistSelectedEvidence(ctx, userID, bookmarkID, title, description, domain, storedEvidence)
	}

	result, fetchErr := s.fetcher.Fetch(ctx, rawURL)
	if fetchErr != nil {
		articlePublisher := publisherForURL(rawURL)
		_, _ = s.UpsertEvidence(ctx, userID, bookmarkID, BookmarkEvidence{
			Kind: "fetched_article", Origin: "web_fetch", CanonicalURL: rawURL, PublisherKey: articlePublisher,
			ExtractionMethod: "generic_web", QualityStatus: "failed", QualityReasons: []string{safefetch.FailureReason(fetchErr)}, ExtractorVersion: safefetch.ExtractorVersion,
		})
		return s.useXSourceFallback(ctx, userID, bookmarkID, title, description, domain, sourceEvidence, fetchErr)
	}
	articleEvidence := BookmarkEvidence{
		Kind: "fetched_article", Origin: "web_fetch", Authority: 80, Text: result.Text, SanitizedHTML: result.HTML,
		CanonicalURL: result.URL, PublisherKey: result.Domain,
		ExtractionMethod: result.Quality.Method, QualityStatus: string(result.Quality.Status), QualityReasons: result.Quality.Reasons,
		ExtractorVersion: result.Quality.Version, Selected: false,
	}
	storedArticle, err := s.UpsertEvidence(ctx, userID, bookmarkID, articleEvidence)
	if err != nil {
		return err
	}
	if result.Quality.Status == safefetch.QualityComplete {
		return s.persistSelectedEvidence(ctx, userID, bookmarkID, fallback(result.Title, title), result.Description, result.Domain, storedArticle)
	}
	return s.useXSourceFallback(ctx, userID, bookmarkID, title, description, domain, sourceEvidence, nil)
}

func (s *Service) useXSourceFallback(ctx context.Context, userID, bookmarkID, title, description, domain string, sourceEvidence BookmarkEvidence, fetchErr error) error {
	if sourceEvidence.QualityStatus == "complete" && strings.TrimSpace(sourceEvidence.Text) != "" {
		sourceEvidence.Selected = false
		storedEvidence, err := s.UpsertEvidence(ctx, userID, bookmarkID, sourceEvidence)
		if err != nil {
			return err
		}
		return s.persistSelectedEvidence(ctx, userID, bookmarkID, title, description, domain, storedEvidence)
	}
	if err := s.markInsufficientEvidence(ctx, bookmarkID, sourceEvidence.QualityStatus); err != nil {
		return err
	}
	if fetchErr == nil {
		return errPartialExtraction
	}
	return fetchErr
}

func (s *Service) markInsufficientEvidence(ctx context.Context, bookmarkID, qualityStatus string) error {
	status := "insufficient_evidence"
	if qualityStatus == "failed" {
		status = "failed"
	}
	now := nowString()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var userID, source, title, evidenceOrigin, evidenceStatus, extractorVersion string
	if err := tx.QueryRowContext(ctx, `SELECT b.user_id,b.source,COALESCE(b.title,''),COALESCE(e.evidence_origin,''),COALESCE(e.quality_status,''),COALESCE(e.extractor_version,'') FROM bookmarks b LEFT JOIN bookmark_evidence e ON e.bookmark_id=b.id AND e.user_id=b.user_id AND e.is_selected=1 WHERE b.id=?`, bookmarkID).Scan(&userID, &source, &title, &evidenceOrigin, &evidenceStatus, &extractorVersion); err != nil {
		return err
	}
	if source == "x" || source == "twitter" {
		title = html.UnescapeString(title)
	}
	authoritativeEmpty := evidenceOrigin == "x_api" && evidenceStatus == "metadata_only" && extractorVersion == "x-api-v1"
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmark_entities WHERE bookmark_id=? AND user_id=?`, bookmarkID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmark_concepts WHERE bookmark_id=? AND user_id=?`, bookmarkID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmark_tags WHERE bookmark_id=? AND user_id=? AND source='enrichment'`, bookmarkID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bookmarks SET title=?,embedding=NULL,embedding_dim=0,embedding_model=NULL,processed_at=?,fetch_version=CASE WHEN ? THEN ? ELSE fetch_version END,summary_version=CASE WHEN ? THEN ? ELSE summary_version END,enrichment_version=CASE WHEN ? THEN ? ELSE enrichment_version END WHERE id=?`, title, now, authoritativeEmpty, extractorVersion, authoritativeEmpty, providers.SummaryPromptVersion, authoritativeEmpty, providers.SemanticVersion, bookmarkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ai_summaries SET one_sentence=CASE WHEN validation_status='validated' THEN one_sentence ELSE NULL END,bullet_points_json=CASE WHEN validation_status='validated' THEN bullet_points_json ELSE '[]' END,long_form=CASE WHEN validation_status='validated' THEN long_form ELSE NULL END,highlights_json=CASE WHEN validation_status='validated' THEN highlights_json ELSE '[]' END,suggested_tags_json=CASE WHEN validation_status='validated' THEN suggested_tags_json ELSE '[]' END,processing_status=CASE WHEN validation_status='validated' AND processing_status='completed' THEN processing_status ELSE ? END,validation_status=CASE WHEN validation_status='validated' THEN validation_status ELSE 'insufficient_evidence' END,updated_at=? WHERE bookmark_id=?`, status, now, bookmarkID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) persistSelectedEvidence(ctx context.Context, userID, bookmarkID, title, description, domain string, evidence BookmarkEvidence) error {
	title = fallback(title, domain)
	now := time.Now().UTC().Format(time.RFC3339)
	request := s.summaryRequestForEvidence(ctx, userID, bookmarkID, title, evidence)
	generated, generationErr := s.aiClient(ctx).GenerateSummary(ctx, request)
	if generationErr != nil && s.hasValidActiveSummary(ctx, userID, bookmarkID, evidence.ContentHash) {
		return nil
	}
	validationReasons := summaryFailureReasons(generationErr)
	if generationErr != nil {
		generated = providers.SummaryResult{
			OneSentence: oneSentence(evidence.Text), Status: providers.SummaryStatusCompleted,
			PromptVersion: providers.SummaryPromptVersion, ValidatorVersion: providers.SummaryValidatorVersion,
			ValidationCodes: validationReasons,
		}
	}
	summaryStatus := string(generated.Status)
	validationStatus := "validated"
	if generationErr != nil {
		summaryStatus = "fallback"
		validationStatus = "fallback"
	}
	if summaryStatus == "" {
		summaryStatus = "insufficient_evidence"
		validationStatus = "insufficient_evidence"
	}
	semanticResult := providers.SemanticResult{Entities: generated.Entities, Concepts: generated.Concepts}
	enrichment := enrichment{}
	if evidence.QualityStatus == string(providers.QualityComplete) {
		enrichment = s.enrichText(ctx, bookmarkID, userID, title, description, evidence.Text, semanticResult)
	}
	enrichment.Tags = s.allowedAITags(ctx, userID, generated.SuggestedTags)
	highlightSpans := highlightSpansJSON(evidence.ID, evidence.Text, generated.Highlights)
	generatedAt := nullableTimeString(generated.GeneratedAt)
	htmlContent := evidence.SanitizedHTML
	if htmlContent == "" {
		htmlContent = html.EscapeString(evidence.Text)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE bookmark_evidence SET is_selected=0,updated_at=? WHERE bookmark_id=? AND user_id=?`, now, bookmarkID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bookmark_evidence SET is_selected=1,updated_at=? WHERE id=? AND bookmark_id=? AND user_id=?`, now, evidence.ID, bookmarkID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bookmarks SET canonical_url=CASE WHEN ?='' THEN canonical_url ELSE ? END,title=CASE WHEN title='' OR title=domain OR title=url THEN ? ELSE title END,description=CASE WHEN description='' THEN ? ELSE description END,domain=CASE WHEN ?='' THEN domain ELSE ? END,sanitized_html=?,text_content=?,reading_time=?,processed_at=?,fetch_version=?,summary_version=?,enrichment_version=? WHERE id=? AND user_id=?`,
		evidence.CanonicalURL, evidence.CanonicalURL, title, description, domain, domain, sanitize.HTML(htmlContent), evidence.Text, readingTime(evidence.Text), now, fallback(evidence.ExtractorVersion, safefetch.ExtractorVersion), providers.SummaryPromptVersion, providers.SemanticVersion, bookmarkID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,bullet_points_json,long_form,highlights_json,suggested_tags_json,processing_status,provider,model,prompt_version,validator_version,evidence_hash,validation_status,validation_reasons_json,highlight_spans_json,generated_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(bookmark_id) DO UPDATE SET one_sentence=excluded.one_sentence,bullet_points_json=excluded.bullet_points_json,long_form=excluded.long_form,highlights_json=excluded.highlights_json,suggested_tags_json=excluded.suggested_tags_json,processing_status=excluded.processing_status,provider=excluded.provider,model=excluded.model,prompt_version=excluded.prompt_version,validator_version=excluded.validator_version,evidence_hash=excluded.evidence_hash,validation_status=excluded.validation_status,validation_reasons_json=excluded.validation_reasons_json,highlight_spans_json=excluded.highlight_spans_json,generated_at=excluded.generated_at,updated_at=excluded.updated_at`,
		ids.New(), bookmarkID, userID, generated.OneSentence, jsonListString(generated.BulletPoints), generated.LongForm, jsonListString(generated.Highlights), jsonListString(generated.SuggestedTags), summaryStatus, generated.Provider, generated.Model, fallback(generated.PromptVersion, providers.SummaryPromptVersion), fallback(generated.ValidatorVersion, providers.SummaryValidatorVersion), evidence.ContentHash, validationStatus, jsonListString(validationReasons), highlightSpans, generatedAt, now, now); err != nil {
		return err
	}
	if err := s.replaceGeneratedEnrichmentTx(ctx, tx, bookmarkID, userID, evidence.ID, enrichment); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.refreshSearchIndex(ctx, userID)
	return nil
}

func (s *Service) summaryRequestForEvidence(ctx context.Context, userID, bookmarkID, title string, evidence BookmarkEvidence) providers.SummaryRequest {
	var contentKind, published string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(content_kind,''),COALESCE(source_published_at,'') FROM bookmarks WHERE id=? AND user_id=?`, bookmarkID, userID).Scan(&contentKind, &published)
	if evidence.PublishedAt != "" {
		published = evidence.PublishedAt
	}
	var sourceText string
	_ = s.db.QueryRowContext(ctx, `SELECT content_text FROM bookmark_evidence WHERE bookmark_id=? AND user_id=? AND evidence_kind='source_post' ORDER BY authority DESC LIMIT 1`, bookmarkID, userID).Scan(&sourceText)
	if sourceText == evidence.Text {
		sourceText = ""
	}
	return providers.SummaryRequest{
		ContentKind: providers.ContentKind(normalizeSummaryContentKind(contentKind, evidence.Kind)), Title: title,
		SourceText: sourceText, PrimaryText: evidence.Text, SourcePublished: parseSummaryTime(published),
		QualityStatus: providers.QualityStatus(evidence.QualityStatus), QualityReasons: evidence.QualityReasons,
	}
}

func normalizeSummaryContentKind(contentKind, evidenceKind string) string {
	switch contentKind {
	case "x_post", "x_thread", "article", "document", "note", "transcript", "marketing_page", "metadata_only":
		return contentKind
	}
	if evidenceKind == "source_post" {
		return "x_post"
	}
	if evidenceKind == "fetched_article" {
		return "article"
	}
	return "web_page"
}

func parseSummaryTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, strings.TrimSpace(value))
	return parsed
}

func nullableTimeString(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

func (s *Service) hasValidActiveSummary(ctx context.Context, userID, bookmarkID, evidenceHash string) bool {
	var count int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_summaries summary JOIN bookmarks bookmark ON bookmark.id=summary.bookmark_id AND bookmark.user_id=summary.user_id JOIN bookmark_evidence evidence ON evidence.bookmark_id=summary.bookmark_id AND evidence.user_id=summary.user_id AND evidence.is_selected=1 AND evidence.content_hash=summary.evidence_hash WHERE summary.bookmark_id=? AND summary.user_id=? AND summary.processing_status='completed' AND summary.validation_status='validated' AND summary.prompt_version=? AND summary.validator_version=? AND summary.evidence_hash=? AND trim(COALESCE(summary.one_sentence,''))<>'' AND bookmark.summary_version=? AND bookmark.enrichment_version=? AND trim(COALESCE(bookmark.text_content,''))=trim(COALESCE(evidence.content_text,''))`, bookmarkID, userID, providers.SummaryPromptVersion, providers.SummaryValidatorVersion, evidenceHash, providers.SummaryPromptVersion, providers.SemanticVersion).Scan(&count)
	return count > 0
}

func summaryFailureReasons(err error) []string {
	if err == nil {
		return []string{}
	}
	var validationErr *providers.SummaryValidationError
	if errors.As(err, &validationErr) {
		return validationErr.ReasonCodes
	}
	return []string{providers.SafeErrorCode(err)}
}

func highlightSpansJSON(evidenceID, evidenceText string, highlights []string) string {
	spans := make([]map[string]any, 0, len(highlights))
	lowerEvidence := strings.ToLower(evidenceText)
	for _, highlight := range highlights {
		start := strings.Index(lowerEvidence, strings.ToLower(highlight))
		if start < 0 {
			continue
		}
		spans = append(spans, map[string]any{"text": highlight, "evidence_id": evidenceID, "start": start, "end": start + len(highlight)})
	}
	return jsonListString(spans)
}

func normalizeProcessingURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.ToLower(strings.TrimRight(raw, "/"))
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.ToLower(strings.TrimRight(parsed.String(), "/"))
}

func publisherForURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
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
		_ = s.jobs.Enqueue(r.Context(), user.ID, "bookmark.process", bookmarkProcessPayload(bookmarkID, item.URL, jobID))
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
		if existing := s.existingRestoredBookmarkID(ctx, userID, rawURL, bookmark); existing != "" {
			newID = existing
		} else {
			inserted, err := s.insertRestoredBookmark(ctx, userID, newID, rawURL, bookmark, parsed.Hostname(), now)
			if err != nil {
				continue
			}
			if !inserted {
				if oldID == "" {
					continue
				}
				newID = ids.New()
				if retryInserted, retryErr := s.insertRestoredBookmark(ctx, userID, newID, rawURL, bookmark, parsed.Hostname(), now); retryErr != nil || !retryInserted {
					continue
				}
			}
			restored++
		}
		if oldID != "" {
			oldBookmarks[oldID] = newID
		}
		evidenceIDs := s.restoreBookmarkEvidence(ctx, userID, newID, bookmark["evidence"])
		s.restoreBookmarkChildren(ctx, userID, newID, bookmark, oldNotes, evidenceIDs, now)
	}
	s.restoreStandaloneNotes(ctx, userID, backup["notes"], oldNotes, now)
	s.restoreDailyNotes(ctx, userID, backup["daily_notes"], now)
	s.restoreKnowledgeObjects(ctx, userID, backup["knowledge_objects"], oldBookmarks, oldNotes, oldObjects, now)
	s.restoreTags(ctx, userID, backup["tags"], now)
	s.restoreCollections(ctx, userID, backup["collections"], oldBookmarks, now)
	s.restoreSavedSearches(ctx, userID, backup["saved_searches"], now)
	s.restoreReviewEvents(ctx, userID, backup["review_events"], oldBookmarks, oldNotes, now)
	s.restoreItemStates(ctx, userID, backup["item_states"], oldBookmarks, oldNotes, now)
	s.restoreItemLinks(ctx, userID, backup["item_links"], oldBookmarks, oldNotes, now)
	s.restoreReminders(ctx, userID, backup["reminders"], oldBookmarks, oldNotes, now)
	s.restoreActionItems(ctx, userID, backup["action_items"], oldBookmarks, oldNotes, now)
	s.restoreResultFeedback(ctx, userID, backup["result_feedback"], oldBookmarks, oldNotes, now)
	s.restoreKnowledgeFeedback(ctx, userID, backup["knowledge_feedback"], now)
	s.restoreInsightImpressions(ctx, userID, backup["insight_impressions"], now)
	s.restoreImportSources(ctx, userID, jobID, backup["import_sources"], oldBookmarks, now)
	_, _ = s.db.ExecContext(ctx, `UPDATE import_jobs SET total_bookmarks=?,content_fetched=?,ai_processed=?,status='completed',updated_at=? WHERE id=? AND user_id=?`, restored, restored, restored, now, jobID, userID)
	s.refreshSearchIndex(ctx, userID)
	return map[string]any{"message": "Backup restored", "count": restored, "import_job_id": jobID, "source_report": s.importSourceReport(ctx, userID, jobID)}, true, nil
}

func (s *Service) existingRestoredBookmarkID(ctx context.Context, userID, rawURL string, bookmark map[string]any) string {
	if tweetID := strings.TrimSpace(stringValue(bookmark["x_tweet_id"])); tweetID != "" {
		var existing string
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM bookmarks WHERE user_id=? AND x_tweet_id=?`, userID, tweetID).Scan(&existing); err == nil {
			return existing
		}
	}
	var existing string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM bookmarks WHERE user_id=? AND url=?`, userID, rawURL).Scan(&existing); err == nil {
		return existing
	}
	return ""
}

func (s *Service) insertRestoredBookmark(ctx context.Context, userID, id, rawURL string, bookmark map[string]any, domainFallback, now string) (bool, error) {
	xMetrics := ""
	if _, ok := bookmark["x_metrics"]; ok {
		xMetrics = jsonString(bookmark["x_metrics"])
	} else {
		xMetrics = strings.TrimSpace(stringValue(bookmark["x_metrics_json"]))
	}
	source := fallback(stringValue(bookmark["source"]), "restore")
	capturedAt := fallback(stringValue(bookmark["captured_at"]), fallback(stringValue(bookmark["created_at"]), now))
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO bookmarks(id,user_id,url,title,description,domain,favicon,thumbnail,sanitized_html,text_content,reading_time,read_status,source,x_tweet_id,x_author_username,x_author_name,x_tweet_url,x_metrics_json,canonical_url,content_kind,source_published_at,source_author_id,source_publisher_key,processed_at,fetch_version,summary_version,enrichment_version,created_at,updated_at,last_accessed,view_count,version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, userID, rawURL, stringValue(bookmark["title"]), stringValue(bookmark["description"]), fallback(stringValue(bookmark["domain"]), domainFallback), nullableStringValue(stringValue(bookmark["favicon"])), nullableStringValue(stringValue(bookmark["thumbnail"])), sanitize.HTML(stringValue(bookmark["html_content"])), stringValue(bookmark["text_content"]), intValue(bookmark["reading_time"]), boolValue(bookmark["read_status"]), source, nullableStringValue(stringValue(bookmark["x_tweet_id"])), nullableStringValue(stringValue(bookmark["x_author_username"])), nullableStringValue(stringValue(bookmark["x_author_name"])), nullableStringValue(stringValue(bookmark["x_tweet_url"])), nullableStringValue(xMetrics), fallback(stringValue(bookmark["canonical_url"]), rawURL), fallback(stringValue(bookmark["content_kind"]), restoredContentKind(source)), nullableStringValue(stringValue(bookmark["source_published_at"])), nullableStringValue(stringValue(bookmark["source_author_id"])), nullableStringValue(stringValue(bookmark["source_publisher_key"])), nullableStringValue(stringValue(bookmark["processed_at"])), stringValue(bookmark["fetch_version"]), stringValue(bookmark["summary_version"]), stringValue(bookmark["enrichment_version"]), capturedAt, fallback(stringValue(bookmark["updated_at"]), now), nullableStringValue(stringValue(bookmark["last_accessed"])), intValue(bookmark["view_count"]), intValueDefault(bookmark["version"], 1))
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

func restoredContentKind(source string) string {
	if source == "x" || source == "twitter" {
		return "x_post"
	}
	return "web"
}

func (s *Service) restoreBookmarkEvidence(ctx context.Context, userID, bookmarkID string, raw any) map[string]string {
	restoredIDs := map[string]string{}
	for _, item := range mapList(raw) {
		oldID := stringValue(item["id"])
		evidence := BookmarkEvidence{
			ID:               oldID,
			Kind:             stringValue(item["kind"]),
			Origin:           stringValue(item["origin"]),
			Authority:        intValue(item["authority"]),
			Text:             stringValue(item["text"]),
			SanitizedHTML:    stringValue(item["sanitized_html"]),
			CanonicalURL:     stringValue(item["canonical_url"]),
			AuthorID:         stringValue(item["author_id"]),
			PublisherKey:     stringValue(item["publisher_key"]),
			PublishedAt:      stringValue(item["published_at"]),
			ExtractionMethod: stringValue(item["extraction_method"]),
			ContentHash:      stringValue(item["content_hash"]),
			QualityStatus:    stringValue(item["quality_status"]),
			QualityReasons:   stringSlice(item["quality_reasons"]),
			ExtractorVersion: stringValue(item["extractor_version"]),
			Selected:         boolValue(item["selected"]),
			CreatedAt:        stringValue(item["created_at"]),
			UpdatedAt:        stringValue(item["updated_at"]),
		}
		stored, err := s.UpsertEvidence(ctx, userID, bookmarkID, evidence)
		if err != nil && evidence.ID != "" {
			evidence.ID = ""
			stored, err = s.UpsertEvidence(ctx, userID, bookmarkID, evidence)
		}
		if err == nil && oldID != "" {
			restoredIDs[oldID] = stored.ID
		}
	}
	return restoredIDs
}

func (s *Service) restoreBookmarkChildren(ctx context.Context, userID, bookmarkID string, bookmark map[string]any, oldNotes, evidenceIDs map[string]string, now string) {
	if summary, ok := bookmark["ai_summary"].(map[string]any); ok {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,bullet_points_json,long_form,highlights_json,suggested_tags_json,processing_status,provider,model,prompt_version,validator_version,evidence_hash,validation_status,validation_reasons_json,highlight_spans_json,generated_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(bookmark_id) DO UPDATE SET one_sentence=excluded.one_sentence,bullet_points_json=excluded.bullet_points_json,long_form=excluded.long_form,highlights_json=excluded.highlights_json,suggested_tags_json=excluded.suggested_tags_json,processing_status=excluded.processing_status,provider=excluded.provider,model=excluded.model,prompt_version=excluded.prompt_version,validator_version=excluded.validator_version,evidence_hash=excluded.evidence_hash,validation_status=excluded.validation_status,validation_reasons_json=excluded.validation_reasons_json,highlight_spans_json=excluded.highlight_spans_json,generated_at=excluded.generated_at,updated_at=excluded.updated_at`,
			ids.New(), bookmarkID, userID, stringValue(summary["one_sentence"]), jsonListString(summary["bullet_points"]), stringValue(summary["long_form"]), jsonListString(summary["highlights"]), jsonListString(summary["suggested_tags"]), fallback(stringValue(summary["processing_status"]), "completed"), stringValue(summary["provider"]), stringValue(summary["model"]), stringValue(summary["prompt_version"]), stringValue(summary["validator_version"]), stringValue(summary["evidence_hash"]), stringValue(summary["validation_status"]), jsonListString(summary["validation_reasons"]), jsonListString(remapHighlightSpans(summary["highlight_spans"], evidenceIDs)), nullableStringValue(stringValue(summary["generated_at"])), now, now)
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

func remapHighlightSpans(raw any, evidenceIDs map[string]string) []map[string]any {
	spans := mapList(raw)
	for _, span := range spans {
		if restoredID := evidenceIDs[stringValue(span["evidence_id"])]; restoredID != "" {
			span["evidence_id"] = restoredID
		}
	}
	return spans
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
	rows, err := s.db.QueryContext(ctx, `SELECT id,url,title,description,domain,favicon,thumbnail,reading_time,read_status,source,created_at,updated_at,last_accessed,view_count,version,sanitized_html,text_content,x_tweet_id,x_author_username,x_author_name,x_tweet_url,x_metrics_json,canonical_url,content_kind,source_published_at,source_author_id,source_publisher_key,processed_at,fetch_version,summary_version,enrichment_version FROM bookmarks WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	bookmarks := []map[string]any{}
	for rows.Next() {
		var tweetID, authorUsername, authorName, tweetURL, metrics sql.NullString
		var canonicalURL, contentKind, publishedAt, sourceAuthorID, publisherKey, processedAt, fetchVersion, summaryVersion, enrichmentVersion sql.NullString
		bookmark := scanBookmarkRow(rows, &tweetID, &authorUsername, &authorName, &tweetURL, &metrics, &canonicalURL, &contentKind, &publishedAt, &sourceAuthorID, &publisherKey, &processedAt, &fetchVersion, &summaryVersion, &enrichmentVersion)
		bookmark["x_tweet_id"] = nullString(tweetID)
		bookmark["x_author_username"] = nullString(authorUsername)
		bookmark["x_author_name"] = nullString(authorName)
		bookmark["x_tweet_url"] = nullString(tweetURL)
		bookmark["x_metrics"] = jsonObjectValue(metrics.String)
		bookmark["captured_at"] = bookmark["created_at"]
		bookmark["canonical_url"] = canonicalURL.String
		bookmark["content_kind"] = contentKind.String
		bookmark["source_published_at"] = nullString(publishedAt)
		bookmark["source_author_id"] = nullString(sourceAuthorID)
		bookmark["source_publisher_key"] = nullString(publisherKey)
		bookmark["processed_at"] = nullString(processedAt)
		bookmark["fetch_version"] = fetchVersion.String
		bookmark["summary_version"] = summaryVersion.String
		bookmark["enrichment_version"] = enrichmentVersion.String
		bookmarks = append(bookmarks, bookmark)
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
		bookmark["evidence"] = s.exportBookmarkEvidence(ctx, userID, id)
	}
	return map[string]any{
		"version":             2,
		"exported_at":         time.Now().UTC().Format(time.RFC3339),
		"bookmarks":           bookmarks,
		"notes":               s.exportStandaloneNotes(ctx, userID),
		"daily_notes":         s.exportDailyNotes(ctx, userID),
		"knowledge_objects":   s.exportKnowledgeObjects(ctx, userID),
		"tags":                s.exportTags(ctx, userID),
		"collections":         s.exportCollections(ctx, userID),
		"saved_searches":      s.exportSavedSearches(ctx, userID),
		"import_jobs":         s.exportImportJobs(ctx, userID),
		"import_sources":      s.exportImportSources(ctx, userID),
		"review_events":       s.exportReviewEvents(ctx, userID),
		"item_states":         s.exportItemStates(ctx, userID),
		"item_links":          s.exportItemLinks(ctx, userID),
		"reminders":           s.exportReminders(ctx, userID),
		"action_items":        s.exportActionItems(ctx, userID),
		"result_feedback":     s.exportResultFeedback(ctx, userID),
		"knowledge_feedback":  s.exportKnowledgeFeedback(ctx, userID),
		"insight_impressions": s.exportInsightImpressions(ctx, userID),
	}, nil
}

func (s *Service) exportCollections(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,color,parent_id,sibling_order,created_at,updated_at FROM collections WHERE user_id=? ORDER BY COALESCE(parent_id,''),sibling_order,name`, userID)
	if err != nil {
		return []map[string]any{}
	}
	result := []map[string]any{}
	for rows.Next() {
		var id, name, description, color, created, updated string
		var parent sql.NullString
		var order int
		if rows.Scan(&id, &name, &description, &color, &parent, &order, &created, &updated) == nil {
			result = append(result, map[string]any{"id": id, "name": name, "description": description, "color": color, "parent_id": nullString(parent), "sibling_order": order, "bookmark_ids": []string{}, "created_at": created, "updated_at": updated})
		}
	}
	rows.Close()
	for _, collection := range result {
		memberships := []string{}
		members, _ := s.db.QueryContext(ctx, `SELECT bookmark_id FROM collection_bookmarks WHERE collection_id=? AND user_id=? ORDER BY added_at`, collection["id"], userID)
		if members != nil {
			for members.Next() {
				var bookmark string
				_ = members.Scan(&bookmark)
				memberships = append(memberships, bookmark)
			}
			members.Close()
		}
		collection["bookmark_ids"] = memberships
	}
	return result
}

func (s *Service) restoreCollections(ctx context.Context, userID string, raw any, bookmarks map[string]string, now string) {
	items := mapList(raw)
	remap := map[string]string{}
	// First pass creates roots so old exports (which have no hierarchy fields) remain valid.
	for _, item := range items {
		old := stringValue(item["id"])
		id := fallback(old, ids.New())
		name := strings.TrimSpace(stringValue(item["name"]))
		if name == "" {
			continue
		}
		res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO collections(id,user_id,name,description,color,sibling_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, userID, name, stringValue(item["description"]), stringValue(item["color"]), intValue(item["sibling_order"]), fallback(stringValue(item["created_at"]), now), fallback(stringValue(item["updated_at"]), now))
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			_ = s.db.QueryRowContext(ctx, `SELECT id FROM collections WHERE user_id=? AND name=?`, userID, name).Scan(&id)
		}
		if old != "" {
			remap[old] = id
		}
	}
	for _, item := range items {
		id := remap[stringValue(item["id"])]
		if id == "" {
			continue
		}
		parent := remap[stringValue(item["parent_id"])]
		if parent != "" && parent != id && !s.collectionDescendant(ctx, userID, id, parent) {
			_, _ = s.db.ExecContext(ctx, `UPDATE collections SET parent_id=? WHERE id=? AND user_id=?`, parent, id, userID)
		}
		for _, oldBookmark := range stringSlice(item["bookmark_ids"]) {
			bookmark := bookmarks[oldBookmark]
			if bookmark == "" {
				bookmark = oldBookmark
			}
			if s.ownsBookmark(ctx, userID, bookmark) {
				_, _ = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO collection_bookmarks(collection_id,bookmark_id,user_id,added_at) VALUES(?,?,?,?)`, id, bookmark, userID, now)
			}
		}
	}
}

func (s *Service) exportBookmarkEvidence(ctx context.Context, userID, bookmarkID string) []map[string]any {
	evidence, err := s.Evidence(ctx, userID, bookmarkID)
	if err != nil {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(evidence))
	for _, item := range evidence {
		result = append(result, map[string]any{
			"id": item.ID, "kind": item.Kind, "origin": item.Origin, "authority": item.Authority,
			"text": item.Text, "sanitized_html": item.SanitizedHTML, "canonical_url": item.CanonicalURL,
			"author_id": item.AuthorID, "publisher_key": item.PublisherKey, "published_at": nullableExportString(item.PublishedAt),
			"extraction_method": item.ExtractionMethod, "content_hash": item.ContentHash,
			"quality_status": item.QualityStatus, "quality_reasons": item.QualityReasons,
			"extractor_version": item.ExtractorVersion, "selected": item.Selected,
			"created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
		})
	}
	return result
}

func nullableExportString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
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

func (s *Service) exportKnowledgeFeedback(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT target_type,target_id,feedback,detector_family,detector_version,reason,COALESCE(snoozed_until,''),created_at,updated_at FROM knowledge_feedback WHERE user_id=? ORDER BY updated_at DESC,target_type,target_id`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var targetType, targetID, feedback, family, version, reason, snoozedUntil, created, updated string
		_ = rows.Scan(&targetType, &targetID, &feedback, &family, &version, &reason, &snoozedUntil, &created, &updated)
		items = append(items, map[string]any{"target_type": targetType, "target_id": targetID, "feedback": feedback, "detector_family": family, "detector_version": version, "reason": reason, "snoozed_until": snoozedUntil, "created_at": created, "updated_at": updated})
	}
	return items
}

func (s *Service) exportInsightImpressions(ctx context.Context, userID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT insight_id,detector_family,detector_version,first_seen_at,last_seen_at,impression_count FROM insight_impressions WHERE user_id=? ORDER BY last_seen_at DESC,insight_id`, userID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var insightID, family, version, firstSeen, lastSeen string
		var count int
		_ = rows.Scan(&insightID, &family, &version, &firstSeen, &lastSeen, &count)
		items = append(items, map[string]any{"insight_id": insightID, "detector_family": family, "detector_version": version, "first_seen_at": firstSeen, "last_seen_at": lastSeen, "impression_count": count})
	}
	return items
}

func (s *Service) restoreKnowledgeFeedback(ctx context.Context, userID string, raw any, now string) {
	for _, rawItem := range listValue(raw) {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		targetType := stringValue(item["target_type"])
		targetID := stringValue(item["target_id"])
		feedback := stringValue(item["feedback"])
		if targetID == "" || !validKnowledgeFeedback(targetType, feedback) {
			continue
		}
		created := fallback(stringValue(item["created_at"]), now)
		updated := fallback(stringValue(item["updated_at"]), now)
		family := stringValue(item["detector_family"])
		version := stringValue(item["detector_version"])
		reason := stringValue(item["reason"])
		var snoozed any
		if value := stringValue(item["snoozed_until"]); value != "" {
			snoozed = value
		}
		_, _ = s.db.ExecContext(ctx, `INSERT INTO knowledge_feedback(user_id,target_type,target_id,feedback,detector_family,detector_version,reason,snoozed_until,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,target_type,target_id) DO UPDATE SET feedback=excluded.feedback,detector_family=excluded.detector_family,detector_version=excluded.detector_version,reason=excluded.reason,snoozed_until=excluded.snoozed_until,updated_at=excluded.updated_at`, userID, targetType, targetID, feedback, family, version, reason, snoozed, created, updated)
	}
}

func (s *Service) restoreInsightImpressions(ctx context.Context, userID string, raw any, now string) {
	for _, rawItem := range listValue(raw) {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		insightID := stringValue(item["insight_id"])
		family := stringValue(item["detector_family"])
		version := stringValue(item["detector_version"])
		if insightID == "" || family == "" || version == "" {
			continue
		}
		firstSeen := fallback(stringValue(item["first_seen_at"]), now)
		lastSeen := fallback(stringValue(item["last_seen_at"]), firstSeen)
		count := intValueDefault(item["impression_count"], 1)
		_, _ = s.db.ExecContext(ctx, `INSERT INTO insight_impressions(user_id,insight_id,detector_family,detector_version,first_seen_at,last_seen_at,impression_count) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,insight_id,detector_version) DO UPDATE SET detector_family=excluded.detector_family,first_seen_at=excluded.first_seen_at,last_seen_at=excluded.last_seen_at,impression_count=excluded.impression_count`, userID, insightID, family, version, firstSeen, lastSeen, count)
	}
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
		BookmarkID   string `json:"bookmark_id"`
		ImportJobID  string `json:"import_job_id"`
		QualityRunID string `json:"quality_reprocess_run_id"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil || body.BookmarkID == "" {
		return
	}
	now := nowString()
	_, _ = s.db.ExecContext(ctx, `UPDATE ai_summaries SET processing_status=CASE WHEN processing_status IN ('completed','fallback') THEN processing_status ELSE 'failed' END, updated_at=? WHERE bookmark_id=?`, now, body.BookmarkID)
	s.completeQualityProcess(ctx, body.BookmarkID, qualityProcessMeta{RunID: body.QualityRunID}, "failed", "job_attempts_exhausted")
	if body.ImportJobID != "" {
		s.recordImportJobProgress(ctx, body.BookmarkID, body.ImportJobID, 0, 0, 1)
	}
}

func (s *Service) recordImportJobSuccess(ctx context.Context, bookmarkID string, importJobID string) {
	s.recordImportJobProgress(ctx, bookmarkID, importJobID, 1, 1, 0)
}

func (s *Service) recordImportJobFailure(ctx context.Context, bookmarkID string, importJobID string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE ai_summaries SET processing_status=CASE WHEN processing_status IN ('completed','fallback') THEN processing_status ELSE 'failed' END, updated_at=? WHERE bookmark_id=?`, nowString(), bookmarkID)
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

func bookmarkProcessPayload(bookmarkID, rawURL, importJobID string, attemptID ...string) string {
	payload := map[string]string{"bookmark_id": bookmarkID, "url": rawURL}
	if importJobID != "" {
		payload["import_job_id"] = importJobID
	}
	if len(attemptID) > 0 && attemptID[0] != "" {
		payload["capture_attempt_id"] = attemptID[0]
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func (s *Service) ensureAttempt(ctx context.Context, bookmarkID, rawURL, retryOf string) string {
	userID, ok := s.bookmarkOwner(ctx, bookmarkID)
	if !ok {
		return ""
	}
	id := ids.New()
	_, err := s.db.ExecContext(ctx, `INSERT INTO capture_attempts(id,bookmark_id,user_id,retry_of_id,status,requested_url,engine,engine_version,queued_at) VALUES(?,?,?,NULLIF(?,''),'queued',?,'direct_http',?,?)`, id, bookmarkID, userID, retryOf, rawURL, safefetch.ExtractorVersion, nowString())
	if err != nil {
		return ""
	}
	return id
}

// ProcessPayload builds the durable bookmark processing payload used by
// integrations that have already inserted source evidence.
func ProcessPayload(bookmarkID, rawURL, importJobID string) string {
	return bookmarkProcessPayload(bookmarkID, rawURL, importJobID)
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
