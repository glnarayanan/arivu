package bookmarks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/glnarayanan/arivu/internal/providers"
)

const (
	// JobTypeReembed rebuilds vectors that are missing or belong to another
	// embedding space. Each job handles one bounded batch and enqueues the
	// next, so the single worker keeps serving captures in between.
	JobTypeReembed = "embedding.reembed"

	reembedBatchSize = 16
	reembedBudget    = 2 * time.Minute
	// reembedPriority runs after interactive capture work (default 100).
	reembedPriority = 500

	SemanticReady         = "ready"
	SemanticUpdating      = "updating"
	SemanticNotConfigured = "not_configured"
)

// noEmbeddingSpace never matches a stored embedding_model, so queries return
// no vectors while embeddings are unconfigured.
const noEmbeddingSpace = "\x00unconfigured"

type reembedPayload struct {
	Space string `json:"space"`
	After string `json:"after"`
}

// EmbeddingStats summarizes vectors for bookmarks whose selected evidence is
// complete (the only bookmarks Arivu embeds).
type EmbeddingStats struct {
	Space      string `json:"space"`
	Current    int    `json:"current"`
	Stale      int    `json:"stale"`
	Missing    int    `json:"missing"`
	JobQueued  bool   `json:"job_queued"`
	JobRunning bool   `json:"job_running"`
}

var embeddingUnavailableLog sync.Map

// SetEmbeddingProvider overrides the embedding client source. Without it the
// service embeds with its text-generation provider ("same" mode).
func (s *Service) SetEmbeddingProvider(fn func(context.Context) providers.EmbeddingClient) {
	if fn != nil {
		s.embed = fn
	}
}

func (s *Service) embedder(ctx context.Context) providers.EmbeddingClient {
	if s.embed != nil {
		return s.embed(ctx)
	}
	return s.aiClient(ctx).SameProviderEmbeddingClient()
}

// embeddingSpaceArg returns the space used to filter stored vectors.
func embeddingSpaceArg(client providers.EmbeddingClient) string {
	if space := client.Space(); space != "" {
		return space
	}
	return noEmbeddingSpace
}

// embedText embeds text and records why embeddings are unavailable once per
// distinct reason so logs name the embedding model, not the text provider.
func (s *Service) embedText(ctx context.Context, client providers.EmbeddingClient, text string) ([]float64, error) {
	values, err := client.GenerateEmbedding(ctx, text)
	if err != nil && providers.IsEmbeddingNotConfigured(err) {
		reason := client.Config.Reason
		if _, seen := embeddingUnavailableLog.LoadOrStore(reason, true); !seen {
			log.Printf("embeddings: embedding model is not configured (%s): %s Semantic search ranking, related items, Graph similarity and similar-content duplicates stay unavailable until it is set in Settings.", fallback(reason, "unknown"), client.Config.ReasonMessage())
		}
	}
	return values, err
}

func embeddingBody(title, description, text string) string {
	return strings.TrimSpace(title + "\n" + description + "\n" + text)
}

const embeddableBookmarkSQL = `EXISTS (SELECT 1 FROM bookmark_evidence ee WHERE ee.bookmark_id=b.id AND ee.user_id=b.user_id AND ee.is_selected=1 AND ee.quality_status='complete')`

// EmbeddingStats counts current, stale and missing vectors across all users.
func (s *Service) EmbeddingStats(ctx context.Context) (EmbeddingStats, error) {
	client := s.embedder(ctx)
	stats := EmbeddingStats{Space: client.Space()}
	space := embeddingSpaceArg(client)
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN b.embedding IS NOT NULL AND b.embedding_model=? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN b.embedding IS NOT NULL AND COALESCE(b.embedding_model,'')<>? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN b.embedding IS NULL THEN 1 ELSE 0 END),0)
		FROM bookmarks b WHERE `+embeddableBookmarkSQL, space, space).Scan(&stats.Current, &stats.Stale, &stats.Missing)
	if err != nil {
		return stats, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT status FROM jobs WHERE type=? AND status IN ('queued','leased')`, JobTypeReembed)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return stats, err
		}
		stats.JobQueued = stats.JobQueued || status == "queued"
		stats.JobRunning = stats.JobRunning || status == "leased"
	}
	return stats, rows.Err()
}

// semanticStatus tells a user whether vector features are usable.
func (s *Service) semanticStatus(ctx context.Context, userID string, client providers.EmbeddingClient) string {
	if !client.Configured() {
		return SemanticNotConfigured
	}
	var pending int
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM bookmarks b WHERE b.user_id=? AND (b.embedding IS NULL OR COALESCE(b.embedding_model,'')<>?) AND `+embeddableBookmarkSQL+`)`, userID, client.Space()).Scan(&pending)
	if pending == 1 {
		return SemanticUpdating
	}
	return SemanticReady
}

func semanticStatusMessage(status string) string {
	switch status {
	case SemanticNotConfigured:
		return "Semantic matching is unavailable because no embedding model is configured. Results use keywords and extracted topics."
	case SemanticUpdating:
		return "Some items are still being embedded with the current embedding model; they will appear in semantic results when ready."
	default:
		return ""
	}
}

// ScheduleReembed enqueues a background re-embedding pass when vectors are
// missing or stale and no pass is already pending. It is safe to call often.
func (s *Service) ScheduleReembed(ctx context.Context) (bool, error) {
	if s.jobs == nil {
		return false, nil
	}
	client := s.embedder(ctx)
	space := client.Space()
	if space == "" {
		return false, nil
	}
	var stale int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM bookmarks b WHERE (b.embedding IS NULL OR COALESCE(b.embedding_model,'')<>?) AND `+embeddableBookmarkSQL+`)`, space).Scan(&stale); err != nil {
		return false, err
	}
	if stale == 0 {
		return false, nil
	}
	raw, err := json.Marshal(reembedPayload{Space: space})
	if err != nil {
		return false, err
	}
	// Atomic check-and-insert: the hourly tick and admin actions may race.
	return s.jobs.EnqueuePriorityIfIdle(ctx, JobTypeReembed, string(raw), reembedPriority)
}

func (s *Service) enqueueReembed(ctx context.Context, payload reembedPayload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.jobs.EnqueuePriority(ctx, "", JobTypeReembed, string(raw), reembedPriority)
	return err
}

// reembedSystemicSkips rejected items without a single success pause the pass.
const reembedSystemicSkips = 3

func systemicEmbeddingRejection(err error) bool {
	message := err.Error()
	return strings.Contains(message, "status 404") || strings.Contains(message, "status 405") || strings.Contains(message, "status 409")
}

type reembedCandidate struct {
	ID, UserID, Body string
}

// processReembed embeds one bounded batch in id order. Progress lives in the
// bookmarks themselves (rows move into the current space), so an interrupted
// or retried job resumes where it stopped.
func (s *Service) processReembed(ctx context.Context, payload string) error {
	var body reembedPayload
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return fmt.Errorf("invalid re-embed payload")
	}
	client := s.embedder(ctx)
	space := client.Space()
	if space == "" {
		return nil
	}
	after := body.After
	if body.Space != space {
		// The configuration changed since this pass started; start over in
		// the new space.
		after = ""
	}
	rows, err := s.db.QueryContext(ctx, `SELECT b.id,b.user_id,COALESCE(NULLIF(b.title,''),b.domain,''),COALESCE(b.description,''),COALESCE(ee.content_text,'')
		FROM bookmarks b JOIN bookmark_evidence ee ON ee.bookmark_id=b.id AND ee.user_id=b.user_id AND ee.is_selected=1 AND ee.quality_status='complete'
		WHERE b.id>? AND (b.embedding IS NULL OR COALESCE(b.embedding_model,'')<>?)
		ORDER BY b.id LIMIT ?`, after, space, reembedBatchSize)
	if err != nil {
		return err
	}
	var candidates []reembedCandidate
	for rows.Next() {
		var item reembedCandidate
		var title, description, text string
		if err := rows.Scan(&item.ID, &item.UserID, &title, &description, &text); err != nil {
			rows.Close()
			return err
		}
		item.Body = embeddingBody(title, description, text)
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	deadline := time.Now().Add(reembedBudget)
	last := after
	embedded, skipped := 0, 0
	for _, item := range candidates {
		if ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		values, err := s.embedText(ctx, client, item.Body)
		if err != nil {
			switch providers.SafeErrorCode(err) {
			case providers.ErrorEmbeddingNotConfigured:
				return nil
			case providers.ErrorProviderRejected, providers.ErrorProviderInvalidResponse:
				// A missing endpoint or model, or rejections with no success
				// at all, point at the configuration rather than one
				// document: pause visibly instead of sweeping the library.
				if systemicEmbeddingRejection(err) || (embedded == 0 && skipped+1 >= reembedSystemicSkips) {
					return fmt.Errorf("re-embed paused (%s): %s Check the embedding model id and base URL.", providers.SafeErrorCode(err), providers.SafeErrorMessage(err))
				}
				// This item cannot be embedded as-is; skip it so one bad
				// document does not block the pass. A later pass retries it.
				skipped++
				log.Printf("embeddings: skipped bookmark %s during re-embed: %s", item.ID, providers.SafeErrorCode(err))
				last = item.ID
				continue
			default:
				return fmt.Errorf("re-embed paused (%s): %s", providers.SafeErrorCode(err), providers.SafeErrorMessage(err))
			}
		}
		raw, _ := json.Marshal(values)
		if _, err := s.db.ExecContext(ctx, `UPDATE bookmarks SET embedding=?,embedding_dim=?,embedding_model=? WHERE id=? AND user_id=?`, raw, len(values), space, item.ID, item.UserID); err != nil {
			return err
		}
		embedded++
		last = item.ID
	}
	if len(candidates) == 0 {
		return nil
	}
	if last != candidates[len(candidates)-1].ID || len(candidates) == reembedBatchSize {
		return s.enqueueReembed(ctx, reembedPayload{Space: space, After: last})
	}
	return nil
}
