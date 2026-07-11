package bookmarks

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/providers"
)

type enrichment struct {
	Bullets    []string
	Highlights []string
	Tags       []string
	Entities   []providers.SemanticTerm
	Concepts   []providers.SemanticTerm
	Embedding  []float64
}

func (s *Service) enrichText(ctx context.Context, bookmarkID, userID, title, description, text string, semanticResults ...providers.SemanticResult) enrichment {
	body := strings.TrimSpace(title + "\n" + description + "\n" + text)
	result := enrichment{
		Bullets:    bulletSummary(text, 4),
		Highlights: highlightSentences(text, 5),
		Tags:       []string{},
		Entities:   []providers.SemanticTerm{},
		Concepts:   []providers.SemanticTerm{},
	}
	if len(semanticResults) > 0 {
		validated := providers.ValidateSemantics(providers.SemanticRequest{
			ContentKind: providers.ContentKindDocument, EvidenceText: body, QualityStatus: providers.QualityComplete,
		}, semanticResults[0])
		result.Entities = validated.Entities
		result.Concepts = validated.Concepts
	}
	if embedding, err := s.aiClient(ctx).GenerateEmbedding(ctx, body); err == nil && len(embedding) > 0 {
		result.Embedding = embedding
	}
	return result
}

func (s *Service) storeEnrichment(ctx context.Context, bookmarkID, userID string, item enrichment, keepAISummary bool) {
	now := nowString()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if !keepAISummary {
		bullets, _ := json.Marshal(item.Bullets)
		highlights, _ := json.Marshal(item.Highlights)
		tags, _ := json.Marshal(item.Tags)
		if _, err := tx.ExecContext(ctx, `UPDATE ai_summaries SET bullet_points_json=?,highlights_json=?,suggested_tags_json=?,updated_at=? WHERE bookmark_id=? AND user_id=?`, string(bullets), string(highlights), string(tags), now, bookmarkID, userID); err != nil {
			return
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmark_entities WHERE bookmark_id=? AND user_id=?`, bookmarkID, userID); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmark_concepts WHERE bookmark_id=? AND user_id=?`, bookmarkID, userID); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmark_tags WHERE bookmark_id=? AND user_id=? AND source='enrichment'`, bookmarkID, userID); err != nil {
		return
	}
	for _, entity := range item.Entities {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_entities(bookmark_id,user_id,entity) VALUES(?,?,?)`, bookmarkID, userID, entity.Label); err != nil {
			return
		}
	}
	for _, concept := range item.Concepts {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES(?,?,?)`, bookmarkID, userID, concept.Label); err != nil {
			return
		}
	}
	if len(item.Embedding) > 0 {
		raw, _ := json.Marshal(item.Embedding)
		if _, err := tx.ExecContext(ctx, `UPDATE bookmarks SET embedding=?,embedding_dim=?,embedding_model=?,updated_at=? WHERE id=? AND user_id=?`, []byte(raw), len(item.Embedding), "gemini/"+providers.GeminiEmbeddingModel, now, bookmarkID, userID); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

func (s *Service) bookmarkOwner(ctx context.Context, bookmarkID string) (string, bool) {
	var userID string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM bookmarks WHERE id=?`, bookmarkID).Scan(&userID)
	return userID, err == nil
}

func bulletSummary(text string, limit int) []string {
	sentences := highlightSentences(text, limit)
	if len(sentences) > 0 {
		return sentences
	}
	if one := oneSentence(text); one != "" {
		return []string{one}
	}
	return []string{}
}

func highlightSentences(text string, limit int) []string {
	raw := splitSentences(text)
	var result []string
	for _, sentence := range raw {
		sentence = strings.TrimSpace(sentence)
		if len(sentence) < 30 {
			continue
		}
		if len(sentence) > 260 {
			sentence = sentence[:260]
		}
		result = append(result, sentence)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func splitSentences(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}
