package bookmarks

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
)

const insightDetectorVersion = "2.0.0"

type insightEvidence struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	PublishedAt  string `json:"published_at,omitempty"`
	PublisherKey string `json:"publisher_key,omitempty"`
}

type insightScore struct {
	Evidence    float64 `json:"evidence"`
	Specificity float64 `json:"specificity"`
	Diversity   float64 `json:"diversity"`
	Temporal    float64 `json:"temporal"`
	Novelty     float64 `json:"novelty"`
}

type deterministicInsight struct {
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	Type             string            `json:"type"`
	DetectorVersion  string            `json:"detector_version"`
	Title            string            `json:"title"`
	Explanation      string            `json:"explanation"`
	Window           string            `json:"window"`
	EvidenceStrength string            `json:"evidence_strength"`
	Confidence       float64           `json:"confidence,omitempty"`
	Score            float64           `json:"score"`
	WhyDetected      string            `json:"why_detected"`
	Evidence         []insightEvidence `json:"evidence"`
	Actions          []string          `json:"actions"`
	ScoreComponents  insightScore      `json:"-"`
}

type insightCursor struct {
	Watermark string  `json:"w"`
	LastID    string  `json:"i"`
	LastScore float64 `json:"s"`
}

func (s *Service) Insights(w http.ResponseWriter, r *http.Request, user auth.User) {
	limit := queryInt(r, "limit", 20, 1, 100)
	family := strings.TrimSpace(r.URL.Query().Get("family"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	insights := s.deterministicInsights(r.Context(), user.ID, true)
	filtered := make([]deterministicInsight, 0, len(insights))
	for _, insight := range insights {
		if (family == "" || insight.Type == family) && (kind == "" || insight.Kind == kind) {
			filtered = append(filtered, insight)
		}
	}
	insights = filtered
	watermark := s.insightCorpusWatermark(r.Context(), user.ID)
	start := 0
	if rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor")); rawCursor != "" {
		cursor, ok := decodeInsightCursor(rawCursor)
		if !ok {
			writeError(w, http.StatusBadRequest, "Invalid cursor")
			return
		}
		if cursor.Watermark != watermark {
			writeJSON(w, http.StatusOK, map[string]any{"insights": []deterministicInsight{}, "state": "corpus_changed", "restart_required": true, "corpus_watermark": watermark})
			return
		}
		start = len(insights)
		for index := range insights {
			if insights[index].ID == cursor.LastID && insights[index].Score == cursor.LastScore {
				start = index + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(insights) {
		end = len(insights)
	}
	page := insights[start:end]
	nextCursor := ""
	if end < len(insights) && len(page) > 0 {
		nextCursor = encodeInsightCursor(insightCursor{Watermark: watermark, LastID: page[len(page)-1].ID, LastScore: page[len(page)-1].Score})
	}
	state := "ready"
	if len(page) == 0 {
		state = "no_results"
		if (family == "" || family == "emerging_theme") && !s.hasInsightHistory(r.Context(), user.ID, time.Now().UTC()) {
			state = "not_enough_history"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"insights": page, "state": state, "next_cursor": nextCursor, "corpus_watermark": watermark})
}

func (s *Service) deterministicInsights(ctx context.Context, userID string, hideFeedback bool) []deterministicInsight {
	now := time.Now().UTC()
	insights := []deterministicInsight{}
	insights = append(insights, s.emergingThemeInsights(ctx, userID, now)...)
	insights = append(insights, s.recurringConnectionInsights(ctx, userID)...)
	insights = append(insights, s.changedThinkingInsights(ctx, userID)...)
	insights = append(insights, s.forgottenValueInsights(ctx, userID, now)...)
	insights = append(insights, s.knowledgeGapInsights(ctx, userID)...)
	insights = append(insights, s.serendipitousInsights(ctx, userID)...)
	if hideFeedback {
		filtered := insights[:0]
		for _, insight := range insights {
			if !s.knowledgeTargetHidden(ctx, userID, "insight", insight.ID) {
				filtered = append(filtered, insight)
			}
		}
		insights = filtered
	}
	return diversifyInsights(deduplicateInsights(insights))
}

func newInsight(family, subject string) deterministicInsight {
	return deterministicInsight{ID: stableKnowledgeID("insight", "v2", family, strings.ToLower(strings.TrimSpace(subject))), Kind: "insight", Type: family, DetectorVersion: insightDetectorVersion}
}

func finalizeInsight(insight deterministicInsight, score insightScore) deterministicInsight {
	insight.ScoreComponents = score
	insight.Score = minFloat(0.99, score.Evidence*.30+score.Specificity*.20+score.Diversity*.20+score.Temporal*.20+score.Novelty*.10)
	insight.Confidence = insight.Score
	insight.EvidenceStrength = evidenceStrength(insight.Score)
	if insight.Kind == "recommendation" {
		insight.Confidence = 0
	}
	return insight
}

func (s *Service) changedThinkingInsights(ctx context.Context, userID string) []deterministicInsight {
	rows, err := s.db.QueryContext(ctx, `SELECT item_type,item_id,title FROM (
		SELECT 'note' item_type,id item_id,COALESCE(NULLIF(title,''),'Untitled note') title,body,updated_at FROM notes WHERE user_id=?
		UNION ALL SELECT 'daily_note',note_date,note_date,body,updated_at FROM daily_notes WHERE user_id=?
	) WHERE lower(body) LIKE '%changed my mind%' OR lower(body) LIKE '%no longer%' OR lower(body) LIKE '%instead of%' OR lower(body) LIKE '%revised%' ORDER BY updated_at DESC,item_id LIMIT 100`, userID, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []deterministicInsight{}
	for rows.Next() {
		var itemType, itemID, title string
		if rows.Scan(&itemType, &itemID, &title) != nil {
			continue
		}
		insight := newInsight("changed_thinking", itemType+":"+itemID)
		insight.Title, insight.Explanation, insight.Window = "Thinking changed in "+title, "Your writing contains explicit language that marks a revised position.", "all_time"
		insight.WhyDetected, insight.Evidence, insight.Actions = "explicit change language in an authored note", []insightEvidence{{ID: itemID, Type: itemType, Title: title}}, []string{"review", "connect"}
		result = append(result, finalizeInsight(insight, insightScore{Evidence: .9, Specificity: .9, Diversity: .5, Temporal: .6, Novelty: .7}))
	}
	return result
}

type conceptSource struct{ id, title, publishedAt, publisher string }

func (s *Service) conceptSources(ctx context.Context, userID, concept string) []conceptSource {
	rows, err := s.db.QueryContext(ctx, `SELECT b.id,COALESCE(NULLIF(b.title,''),b.url),COALESCE(b.source_published_at,''),
		COALESCE(NULLIF(b.source_publisher_key,''),NULLIF(b.source_author_id,''),NULLIF(b.domain,''),b.id)
		FROM bookmark_concepts c JOIN bookmarks b ON b.user_id=c.user_id AND b.id=c.bookmark_id
		WHERE c.user_id=? AND c.concept=? ORDER BY COALESCE(b.source_published_at,'') DESC,b.id`, userID, concept)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []conceptSource{}
	for rows.Next() {
		var source conceptSource
		if rows.Scan(&source.id, &source.title, &source.publishedAt, &source.publisher) == nil {
			result = append(result, source)
		}
	}
	return result
}

func (s *Service) conceptNames(ctx context.Context, userID string) []string {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT concept FROM bookmark_concepts WHERE user_id=? ORDER BY concept`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var concept string
		if rows.Scan(&concept) == nil {
			result = append(result, concept)
		}
	}
	return result
}

func (s *Service) emergingThemeInsights(ctx context.Context, userID string, now time.Time) []deterministicInsight {
	recentStart, priorStart := now.AddDate(0, 0, -30), now.AddDate(0, 0, -60)
	result := []deterministicInsight{}
	for _, concept := range s.conceptNames(ctx, userID) {
		if !validInsightConcept(concept) {
			continue
		}
		sources := s.conceptSources(ctx, userID, concept)
		var recent, prior []conceptSource
		publishers := map[string]bool{}
		for _, source := range sources {
			published, err := time.Parse(time.RFC3339, source.publishedAt)
			if err != nil {
				continue
			}
			if !published.Before(recentStart) && !published.After(now) {
				recent = append(recent, source)
				publishers[source.publisher] = true
			} else if !published.Before(priorStart) && published.Before(recentStart) {
				prior = append(prior, source)
			}
		}
		if len(recent) < 3 || len(prior) < 1 || len(publishers) < 2 || len(recent) <= len(prior) {
			continue
		}
		lift := float64(len(recent)-len(prior)) / float64(len(prior))
		if lift < .25 {
			continue
		}
		insight := newInsight("emerging_theme", concept)
		insight.Title, insight.Explanation, insight.Window = concept+" is emerging", "This theme appears more often in sources published recently than in the preceding period.", "last_30_days"
		insight.WhyDetected = fmt.Sprintf("%d recently published sources versus %d in the preceding 30 days across %d publishers", len(recent), len(prior), len(publishers))
		insight.Evidence, insight.Actions = sourceEvidence(recent, 4), []string{"review", "create_note"}
		result = append(result, finalizeInsight(insight, insightScore{Evidence: minFloat(1, float64(len(recent))/5), Specificity: conceptSpecificity(concept), Diversity: minFloat(1, float64(len(publishers))/3), Temporal: minFloat(1, .6+lift*.2), Novelty: .7}))
	}
	return result
}

func (s *Service) recurringConnectionInsights(ctx context.Context, userID string) []deterministicInsight {
	result := []deterministicInsight{}
	for _, concept := range s.conceptNames(ctx, userID) {
		if !validInsightConcept(concept) {
			continue
		}
		sources := s.conceptSources(ctx, userID, concept)
		publishers := map[string]bool{}
		publishedDates := map[string]bool{}
		for _, source := range sources {
			publishers[source.publisher] = true
			if published, err := time.Parse(time.RFC3339, source.publishedAt); err == nil {
				publishedDates[published.UTC().Format(time.DateOnly)] = true
			}
		}
		if len(sources) < 3 || len(publishers) < 2 || len(publishedDates) < 2 {
			continue
		}
		insight := newInsight("recurring_connection", concept)
		insight.Title, insight.Explanation, insight.Window = "Recurring connection: "+concept, "The same specific concept recurs across independent sources.", "all_time"
		insight.WhyDetected = fmt.Sprintf("%d sources across %d independent publishers and %d publication dates", len(sources), len(publishers), len(publishedDates))
		insight.Evidence, insight.Actions = sourceEvidence(sources, 4), []string{"review", "connect"}
		result = append(result, finalizeInsight(insight, insightScore{Evidence: minFloat(1, float64(len(sources))/5), Specificity: conceptSpecificity(concept), Diversity: minFloat(1, float64(len(publishers))/3), Temporal: .65, Novelty: .6}))
	}
	return result
}

func (s *Service) forgottenValueInsights(ctx context.Context, userID string, now time.Time) []deterministicInsight {
	cutoff := now.AddDate(0, 0, -90).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `SELECT b.id,COALESCE(b.title,b.url),st.importance FROM bookmarks b JOIN item_states st ON st.user_id=b.user_id AND st.item_type='bookmark' AND st.item_id=b.id WHERE b.user_id=? AND st.importance>=3 AND COALESCE(b.last_accessed,b.updated_at)<? ORDER BY b.id LIMIT 100`, userID, cutoff)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []deterministicInsight{}
	for rows.Next() {
		var id, title string
		var importance int
		if rows.Scan(&id, &title, &importance) != nil {
			continue
		}
		insight := newInsight("forgotten_value", id)
		insight.Kind = "recommendation"
		insight.Title = "Revisit " + title
		insight.Explanation = "An item you marked as important has not been revisited recently."
		insight.Window = "older_than_90_days"
		insight.WhyDetected = fmt.Sprintf("importance %d and no recent access", importance)
		insight.Evidence = []insightEvidence{{ID: id, Type: "bookmark", Title: title}}
		insight.Actions = []string{"review", "snooze"}
		result = append(result, finalizeInsight(insight, insightScore{Evidence: .8, Specificity: .6, Diversity: .4, Temporal: .8, Novelty: .5}))
	}
	return result
}

func (s *Service) knowledgeGapInsights(ctx context.Context, userID string) []deterministicInsight {
	rows, err := s.db.QueryContext(ctx, `SELECT b.id,COALESCE(b.title,b.url) FROM bookmarks b JOIN item_states st ON st.user_id=b.user_id AND st.item_type='bookmark' AND st.item_id=b.id WHERE b.user_id=? AND st.importance>=3 AND NOT EXISTS(SELECT 1 FROM bookmark_concepts c WHERE c.user_id=b.user_id AND c.bookmark_id=b.id) AND NOT EXISTS(SELECT 1 FROM item_links l WHERE l.user_id=b.user_id AND ((l.from_type='bookmark' AND l.from_id=b.id) OR (l.to_type='bookmark' AND l.to_id=b.id))) ORDER BY b.id LIMIT 100`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []deterministicInsight{}
	for rows.Next() {
		var id, title string
		if rows.Scan(&id, &title) != nil {
			continue
		}
		insight := newInsight("knowledge_gap", id)
		insight.Kind = "recommendation"
		insight.Title = "Develop " + title
		insight.Explanation = "This important item has neither concepts nor explicit connections."
		insight.Window = "current"
		insight.WhyDetected = "important item with zero concepts and zero explicit links"
		insight.Evidence = []insightEvidence{{ID: id, Type: "bookmark", Title: title}}
		insight.Actions = []string{"connect", "create_note"}
		result = append(result, finalizeInsight(insight, insightScore{Evidence: .7, Specificity: .6, Diversity: .4, Temporal: .5, Novelty: .5}))
	}
	return result
}

func (s *Service) serendipitousInsights(ctx context.Context, userID string) []deterministicInsight {
	result := []deterministicInsight{}
	explicitLinks := s.explicitBookmarkLinks(ctx, userID)
	for _, concept := range s.conceptNames(ctx, userID) {
		if !validInsightConcept(concept) {
			continue
		}
		sources := s.conceptSources(ctx, userID, concept)
		for left := 0; left < len(sources); left++ {
			for right := left + 1; right < len(sources); right++ {
				if sources[left].publisher == sources[right].publisher || explicitLinks[bookmarkPairKey(sources[left].id, sources[right].id)] {
					continue
				}
				pair := []conceptSource{sources[left], sources[right]}
				insight := newInsight("serendipitous_connection", concept+":"+sources[left].id+":"+sources[right].id)
				insight.Title = sources[left].title + " and " + sources[right].title
				insight.Explanation = "Two independent sources share a specific concept but are not explicitly connected."
				insight.Window = "all_time"
				insight.WhyDetected = "shared concept " + concept + " across independent publishers"
				insight.Evidence = sourceEvidence(pair, 2)
				insight.Actions = []string{"review", "connect"}
				result = append(result, finalizeInsight(insight, insightScore{Evidence: .75, Specificity: conceptSpecificity(concept), Diversity: 1, Temporal: .5, Novelty: .8}))
			}
		}
	}
	return result
}

func (s *Service) explicitBookmarkLinks(ctx context.Context, userID string) map[string]bool {
	rows, err := s.db.QueryContext(ctx, `SELECT from_id,to_id FROM item_links WHERE user_id=? AND from_type='bookmark' AND to_type='bookmark'`, userID)
	if err != nil {
		return map[string]bool{}
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var leftID, rightID string
		if rows.Scan(&leftID, &rightID) == nil {
			result[bookmarkPairKey(leftID, rightID)] = true
		}
	}
	return result
}

func bookmarkPairKey(leftID, rightID string) string {
	if leftID > rightID {
		leftID, rightID = rightID, leftID
	}
	return leftID + "\x00" + rightID
}

func sourceEvidence(sources []conceptSource, limit int) []insightEvidence {
	if len(sources) > limit {
		sources = sources[:limit]
	}
	result := make([]insightEvidence, 0, len(sources))
	for _, source := range sources {
		result = append(result, insightEvidence{ID: source.id, Type: "bookmark", Title: source.title, PublishedAt: source.publishedAt, PublisherKey: source.publisher})
	}
	return result
}

func evidenceStrength(score float64) string {
	if score >= .8 {
		return "strong"
	}
	if score >= .65 {
		return "moderate"
	}
	return "limited"
}

func conceptSpecificity(concept string) float64 {
	words := strings.Fields(concept)
	if len(words) >= 2 {
		return .9
	}
	if len(concept) >= 8 {
		return .75
	}
	return .6
}

var insightArtifactPattern = regexp.MustCompile(`(?i)^(?:\d{1,4}|\d{1,2}:\d{2}(?:\s*[ap]m)?|\d{1,4}[-/.]\d{1,2}(?:[-/.]\d{1,4})?)$`)
var invalidInsightConcepts = map[string]bool{"amp": true, "com": true, "http": true, "https": true, "nbsp": true, "quot": true, "www": true, "jan": true, "feb": true, "mar": true, "apr": true, "may": true, "jun": true, "jul": true, "aug": true, "sep": true, "oct": true, "nov": true, "dec": true, "thing": true, "things": true, "people": true, "time": true, "good": true, "new": true, "use": true, "using": true}

func validInsightConcept(concept string) bool {
	normalized := strings.ToLower(strings.Trim(strings.TrimSpace(concept), "&;.,:!?()[]{}\"'"))
	if len(normalized) < 3 || invalidInsightConcepts[normalized] || insightArtifactPattern.MatchString(normalized) {
		return false
	}
	return !strings.Contains(normalized, "://") && !strings.HasPrefix(normalized, "www.")
}

func deduplicateInsights(insights []deterministicInsight) []deterministicInsight {
	seen := map[string]bool{}
	result := make([]deterministicInsight, 0, len(insights))
	for _, insight := range insights {
		key := insight.Type + ":" + strings.ToLower(insight.Title)
		if seen[insight.ID] || seen[key] {
			continue
		}
		seen[insight.ID] = true
		seen[key] = true
		result = append(result, insight)
	}
	return result
}

func diversifyInsights(insights []deterministicInsight) []deterministicInsight {
	sort.SliceStable(insights, func(i, j int) bool {
		if insights[i].Score != insights[j].Score {
			return insights[i].Score > insights[j].Score
		}
		return insights[i].ID < insights[j].ID
	})
	buckets := map[string][]deterministicInsight{}
	families := []string{}
	for _, insight := range insights {
		if _, ok := buckets[insight.Type]; !ok {
			families = append(families, insight.Type)
		}
		buckets[insight.Type] = append(buckets[insight.Type], insight)
	}
	sort.SliceStable(families, func(i, j int) bool { return buckets[families[i]][0].Score > buckets[families[j]][0].Score })
	result := make([]deterministicInsight, 0, len(insights))
	for len(result) < len(insights) {
		for _, family := range families {
			if len(buckets[family]) == 0 {
				continue
			}
			result = append(result, buckets[family][0])
			buckets[family] = buckets[family][1:]
		}
	}
	return result
}

func (s *Service) hasInsightHistory(ctx context.Context, userID string, now time.Time) bool {
	var count int
	priorStart := now.AddDate(0, 0, -60).Format(time.RFC3339)
	recentStart := now.AddDate(0, 0, -30).Format(time.RFC3339)
	return s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks WHERE user_id=? AND source_published_at>=? AND source_published_at<?`, userID, priorStart, recentStart).Scan(&count) == nil && count > 0
}

func (s *Service) insightCorpusWatermark(ctx context.Context, userID string) string {
	hash := sha256.New()
	rows, err := s.db.QueryContext(ctx, `SELECT c.concept,b.id,COALESCE(b.source_published_at,''),COALESCE(NULLIF(b.source_publisher_key,''),NULLIF(b.source_author_id,''),NULLIF(b.domain,''),b.id) FROM bookmark_concepts c JOIN bookmarks b ON b.user_id=c.user_id AND b.id=c.bookmark_id WHERE c.user_id=? ORDER BY c.concept,b.id`, userID)
	if err == nil {
		for rows.Next() {
			var values [4]string
			if rows.Scan(&values[0], &values[1], &values[2], &values[3]) == nil {
				fmt.Fprintf(hash, "%q|%q|%q|%q\n", values[0], values[1], values[2], values[3])
			}
		}
		rows.Close()
	}
	noteRows, err := s.db.QueryContext(ctx, `SELECT id,updated_at FROM notes WHERE user_id=? ORDER BY id`, userID)
	if err == nil {
		for noteRows.Next() {
			var id, updated string
			if noteRows.Scan(&id, &updated) == nil {
				fmt.Fprintf(hash, "n:%q|%q\n", id, updated)
			}
		}
		noteRows.Close()
	}
	dailyRows, err := s.db.QueryContext(ctx, `SELECT note_date,updated_at FROM daily_notes WHERE user_id=? ORDER BY note_date`, userID)
	if err == nil {
		for dailyRows.Next() {
			var id, updated string
			if dailyRows.Scan(&id, &updated) == nil {
				fmt.Fprintf(hash, "d:%q|%q\n", id, updated)
			}
		}
		dailyRows.Close()
	}
	stateRows, err := s.db.QueryContext(ctx, `SELECT item_type,item_id,importance FROM item_states WHERE user_id=? ORDER BY item_type,item_id`, userID)
	if err == nil {
		for stateRows.Next() {
			var itemType, itemID string
			var importance int
			if stateRows.Scan(&itemType, &itemID, &importance) == nil {
				fmt.Fprintf(hash, "s:%q|%q|%d\n", itemType, itemID, importance)
			}
		}
		stateRows.Close()
	}
	linkRows, err := s.db.QueryContext(ctx, `SELECT from_type,from_id,to_type,to_id FROM item_links WHERE user_id=? ORDER BY from_type,from_id,to_type,to_id`, userID)
	if err == nil {
		for linkRows.Next() {
			var values [4]string
			if linkRows.Scan(&values[0], &values[1], &values[2], &values[3]) == nil {
				fmt.Fprintf(hash, "l:%q|%q|%q|%q\n", values[0], values[1], values[2], values[3])
			}
		}
		linkRows.Close()
	}
	return fmt.Sprintf("%x", hash.Sum(nil))[:24]
}

func encodeInsightCursor(cursor insightCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeInsightCursor(raw string) (insightCursor, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return insightCursor{}, false
	}
	var cursor insightCursor
	if json.Unmarshal(decoded, &cursor) != nil || cursor.Watermark == "" || cursor.LastID == "" {
		return insightCursor{}, false
	}
	return cursor, true
}
func minFloat(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}
