package bookmarks

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"
)

type enrichment struct {
	Bullets    []string
	Highlights []string
	Tags       []string
	Entities   []string
	Concepts   []string
	Embedding  []float64
}

func (s *Service) enrichText(ctx context.Context, bookmarkID, userID, title, description, text string) enrichment {
	body := strings.TrimSpace(title + "\n" + description + "\n" + text)
	tags := keyTerms(body, 8)
	result := enrichment{
		Bullets:    bulletSummary(text, 4),
		Highlights: highlightSentences(text, 5),
		Tags:       tags,
		Entities:   titleTerms(title, body, 10),
		Concepts:   tags,
	}
	if embedding, err := s.geminiClient(ctx).GenerateEmbedding(ctx, body, "retrieval_document"); err == nil && len(embedding) > 0 {
		result.Embedding = embedding
	}
	return result
}

func (s *Service) storeEnrichment(ctx context.Context, bookmarkID, userID string, item enrichment) {
	now := nowString()
	bullets, _ := json.Marshal(item.Bullets)
	highlights, _ := json.Marshal(item.Highlights)
	tags, _ := json.Marshal(item.Tags)
	_, _ = s.db.ExecContext(ctx, `UPDATE ai_summaries SET bullet_points_json=?,highlights_json=?,suggested_tags_json=?,updated_at=? WHERE bookmark_id=? AND user_id=?`, string(bullets), string(highlights), string(tags), now, bookmarkID, userID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM bookmark_entities WHERE bookmark_id=? AND user_id=?`, bookmarkID, userID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM bookmark_concepts WHERE bookmark_id=? AND user_id=?`, bookmarkID, userID)
	for _, entity := range item.Entities {
		_, _ = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_entities(bookmark_id,user_id,entity) VALUES(?,?,?)`, bookmarkID, userID, entity)
	}
	for _, concept := range item.Concepts {
		_, _ = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES(?,?,?)`, bookmarkID, userID, concept)
		_ = s.attachTag(ctx, userID, bookmarkID, concept, "enrichment")
	}
	if len(item.Embedding) > 0 {
		raw, _ := json.Marshal(item.Embedding)
		_, _ = s.db.ExecContext(ctx, `UPDATE bookmarks SET embedding=?,embedding_dim=?,embedding_model=?,updated_at=? WHERE id=? AND user_id=?`, []byte(raw), len(item.Embedding), "gemini/text-embedding-004", now, bookmarkID, userID)
	}
}

func (s *Service) bookmarkOwner(ctx context.Context, bookmarkID string) (string, bool) {
	var userID string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM bookmarks WHERE id=?`, bookmarkID).Scan(&userID)
	return userID, err == nil
}

func keyTerms(text string, limit int) []string {
	counts := map[string]int{}
	for _, token := range tokenizeWords(text) {
		if len(token) < 3 || stopWords[token] {
			continue
		}
		counts[token]++
	}
	return topTerms(counts, limit)
}

func titleTerms(title, fallbackText string, limit int) []string {
	counts := map[string]int{}
	for _, token := range tokenizeWords(title) {
		if len(token) >= 3 && !stopWords[token] {
			counts[token] += 3
		}
	}
	if len(counts) == 0 {
		for _, token := range tokenizeWords(fallbackText) {
			if len(token) >= 3 && !stopWords[token] {
				counts[token]++
			}
		}
	}
	return titleCaseTerms(topTerms(counts, limit))
}

func topTerms(counts map[string]int, limit int) []string {
	type pair struct {
		term  string
		count int
	}
	items := make([]pair, 0, len(counts))
	for term, count := range counts {
		items = append(items, pair{term: term, count: count})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].term < items[j].term
		}
		return items[i].count > items[j].count
	})
	if len(items) > limit {
		items = items[:limit]
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.term)
	}
	return result
}

func tokenizeWords(text string) []string {
	var out []string
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
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

func titleCaseTerms(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		result = append(result, strings.ToUpper(value[:1])+value[1:])
	}
	return result
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}

var stopWords = map[string]bool{
	"about": true, "after": true, "again": true, "also": true, "and": true, "are": true, "because": true, "been": true,
	"but": true, "can": true, "could": true, "does": true, "for": true, "from": true, "has": true, "have": true,
	"into": true, "its": true, "more": true, "not": true, "one": true, "our": true, "out": true, "over": true,
	"that": true, "the": true, "their": true, "then": true, "there": true, "these": true, "this": true, "through": true,
	"was": true, "were": true, "what": true, "when": true, "where": true, "which": true, "with": true, "would": true,
	"you": true, "your": true,
}
