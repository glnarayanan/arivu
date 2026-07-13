package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/ids"
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
			EvidenceText:  text,
			QualityStatus: providers.QualityComplete,
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
	if err := s.replaceGeneratedEnrichmentTx(ctx, tx, bookmarkID, userID, "", item); err != nil {
		return
	}
	_ = tx.Commit()
}

func (s *Service) replaceGeneratedEnrichmentTx(ctx context.Context, tx *sql.Tx, bookmarkID, userID, evidenceID string, item enrichment) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmark_entities WHERE bookmark_id=? AND user_id=?`, bookmarkID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmark_concepts WHERE bookmark_id=? AND user_id=?`, bookmarkID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmark_tags WHERE bookmark_id=? AND user_id=? AND source='enrichment'`, bookmarkID, userID); err != nil {
		return err
	}
	mode := "off"
	_ = tx.QueryRowContext(ctx, `SELECT value FROM user_settings WHERE user_id=? AND key=?`, userID, aiTaggingKey).Scan(&mode)
	for _, name := range item.Tags {
		slug := tagSlug(name)
		if slug == "" {
			continue
		}
		var tagID string
		err := tx.QueryRowContext(ctx, `SELECT t.id FROM tags t LEFT JOIN tag_aliases a ON a.tag_id=t.id AND a.user_id=t.user_id WHERE t.user_id=? AND (t.slug=? OR a.alias_slug=?) LIMIT 1`, userID, slug, slug).Scan(&tagID)
		if err == sql.ErrNoRows {
			if mode != "allow-new" {
				continue
			}
			tagID = ids.New()
			_, err = tx.ExecContext(ctx, `INSERT INTO tags(id,user_id,name,slug,source,created_at,updated_at) VALUES(?,?,?,?, 'generated',?,?)`, tagID, userID, name, slug, nowString(), nowString())
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_tags(bookmark_id,tag_id,user_id,source,created_at) VALUES(?,?,?,'enrichment',?)`, bookmarkID, tagID, userID, nowString())
		}
		if err != nil {
			return err
		}
	}
	for _, entity := range item.Entities {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_entities(bookmark_id,user_id,entity,normalized_key,entity_type,confidence,extraction_method,evidence_id,evidence_text,evidence_start,evidence_end,enrichment_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, bookmarkID, userID, entity.Label, entity.NormalizedKey, entity.Type, entity.Confidence, entity.Method, nullableStringValue(evidenceID), entity.Evidence, entity.EvidenceStart, entity.EvidenceEnd, entity.Version); err != nil {
			return err
		}
	}
	for _, concept := range item.Concepts {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_concepts(bookmark_id,user_id,concept,normalized_key,confidence,extraction_method,evidence_id,evidence_text,evidence_start,evidence_end,enrichment_version) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, bookmarkID, userID, concept.Label, concept.NormalizedKey, concept.Confidence, concept.Method, nullableStringValue(evidenceID), concept.Evidence, concept.EvidenceStart, concept.EvidenceEnd, concept.Version); err != nil {
			return err
		}
	}
	if len(item.Embedding) > 0 {
		raw, _ := json.Marshal(item.Embedding)
		if _, err := tx.ExecContext(ctx, `UPDATE bookmarks SET embedding=?,embedding_dim=?,embedding_model=? WHERE id=? AND user_id=?`, []byte(raw), len(item.Embedding), "gemini/"+providers.GeminiEmbeddingModel, bookmarkID, userID); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE bookmarks SET embedding=NULL,embedding_dim=0,embedding_model=NULL WHERE id=? AND user_id=?`, bookmarkID, userID); err != nil {
			return err
		}
	}
	return nil
}

// semanticEligibilitySQL is used only with hard-coded table aliases. It keeps
// every knowledge surface on the same provenance gate as insight generation.
func semanticEligibilitySQL(alias string) string {
	return alias + ".confidence>=0.65 AND " + alias + ".enrichment_version='" + providers.SemanticVersion +
		"' AND " + alias + ".evidence_id IS NOT NULL AND " + alias + ".evidence_text<>'' AND " +
		alias + ".evidence_end>" + alias + ".evidence_start AND EXISTS (SELECT 1 FROM bookmark_evidence quality_evidence WHERE quality_evidence.id=" +
		alias + ".evidence_id AND quality_evidence.bookmark_id=" + alias + ".bookmark_id AND quality_evidence.user_id=" + alias +
		".user_id AND quality_evidence.is_selected=1 AND quality_evidence.quality_status='complete' AND lower(CAST(substr(CAST(quality_evidence.content_text AS BLOB)," + alias + ".evidence_start+1," + alias + ".evidence_end-" + alias + ".evidence_start) AS TEXT))=lower(" + alias + ".evidence_text))"
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
