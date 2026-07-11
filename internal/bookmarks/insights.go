package bookmarks

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
)

type insightEvidence struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type deterministicInsight struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Title       string            `json:"title"`
	Explanation string            `json:"explanation"`
	Window      string            `json:"window"`
	Confidence  float64           `json:"confidence"`
	WhyDetected string            `json:"why_detected"`
	Evidence    []insightEvidence `json:"evidence"`
	Actions     []string          `json:"actions"`
}

func (s *Service) Insights(w http.ResponseWriter, r *http.Request, user auth.User) {
	limit := queryInt(r, "limit", 20, 1, 100)
	insights := s.deterministicInsights(r.Context(), user.ID, true)
	if len(insights) > limit {
		insights = insights[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"insights": insights})
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
	sort.Slice(insights, func(i, j int) bool {
		if insights[i].Type != insights[j].Type {
			return insights[i].Type < insights[j].Type
		}
		return insights[i].ID < insights[j].ID
	})
	return insights
}

func (s *Service) changedThinkingInsights(ctx context.Context, userID string) []deterministicInsight {
	rows, err := s.db.QueryContext(ctx, `SELECT item_type,item_id,title FROM (
		SELECT 'note' item_type,id item_id,COALESCE(NULLIF(title,''),'Untitled note') title,body,updated_at FROM notes WHERE user_id=?
		UNION ALL
		SELECT 'daily_note',note_date,note_date,body,updated_at FROM daily_notes WHERE user_id=?
	) WHERE lower(body) LIKE '%changed my mind%' OR lower(body) LIKE '%no longer%' OR lower(body) LIKE '%instead of%' OR lower(body) LIKE '%revised%' ORDER BY updated_at DESC,item_id LIMIT 20`, userID, userID)
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
		result = append(result, deterministicInsight{ID: stableKnowledgeID("insight", "changed_thinking", itemType, itemID), Type: "changed_thinking", Title: "Thinking changed in " + title, Explanation: "Your writing contains explicit language that marks a revised position.", Window: "all_time", Confidence: 0.85, WhyDetected: "explicit change language in an authored note", Evidence: []insightEvidence{{ID: itemID, Type: itemType, Title: title}}, Actions: []string{"review", "connect"}})
	}
	return result
}

func (s *Service) emergingThemeInsights(ctx context.Context, userID string, now time.Time) []deterministicInsight {
	recent := now.AddDate(0, 0, -30).Format(time.RFC3339)
	prior := now.AddDate(0, 0, -60).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `SELECT c.concept,
 SUM(CASE WHEN b.updated_at>=? THEN 1 ELSE 0 END) recent_count,
 SUM(CASE WHEN b.updated_at>=? AND b.updated_at<? THEN 1 ELSE 0 END) prior_count
 FROM bookmark_concepts c JOIN bookmarks b ON b.user_id=c.user_id AND b.id=c.bookmark_id
 WHERE c.user_id=? GROUP BY c.concept HAVING recent_count>=2 AND recent_count>prior_count ORDER BY c.concept`, recent, prior, recent, userID)
	if err != nil {
		return nil
	}
	type themeCandidate struct {
		concept                 string
		recentCount, priorCount int
	}
	candidates := []themeCandidate{}
	for rows.Next() {
		var candidate themeCandidate
		if rows.Scan(&candidate.concept, &candidate.recentCount, &candidate.priorCount) == nil {
			candidates = append(candidates, candidate)
		}
	}
	rows.Close()
	result := []deterministicInsight{}
	for _, candidate := range candidates {
		evidence := s.conceptEvidence(ctx, userID, candidate.concept, 3)
		result = append(result, deterministicInsight{ID: stableKnowledgeID("insight", "emerging_theme", candidate.concept), Type: "emerging_theme", Title: candidate.concept + " is emerging", Explanation: "This theme appears more often in your recently updated sources than in the previous period.", Window: "last_30_days", Confidence: ratioConfidence(candidate.recentCount, candidate.recentCount+candidate.priorCount), WhyDetected: intString(candidate.recentCount) + " recent sources versus " + intString(candidate.priorCount) + " in the preceding 30 days", Evidence: evidence, Actions: []string{"review", "create_note"}})
	}
	return result
}

func (s *Service) recurringConnectionInsights(ctx context.Context, userID string) []deterministicInsight {
	rows, err := s.db.QueryContext(ctx, `SELECT c.concept,COUNT(DISTINCT b.id),COUNT(DISTINCT b.domain) FROM bookmark_concepts c JOIN bookmarks b ON b.user_id=c.user_id AND b.id=c.bookmark_id WHERE c.user_id=? GROUP BY c.concept HAVING COUNT(DISTINCT b.id)>=2 AND COUNT(DISTINCT b.domain)>=2 ORDER BY c.concept`, userID)
	if err != nil {
		return nil
	}
	type connectionCandidate struct {
		concept        string
		count, domains int
	}
	candidates := []connectionCandidate{}
	for rows.Next() {
		var candidate connectionCandidate
		if rows.Scan(&candidate.concept, &candidate.count, &candidate.domains) == nil {
			candidates = append(candidates, candidate)
		}
	}
	rows.Close()
	result := []deterministicInsight{}
	for _, candidate := range candidates {
		result = append(result, deterministicInsight{ID: stableKnowledgeID("insight", "recurring_connection", candidate.concept), Type: "recurring_connection", Title: "Recurring connection: " + candidate.concept, Explanation: "The same concept recurs across unrelated sources.", Window: "all_time", Confidence: ratioConfidence(candidate.domains, candidate.count), WhyDetected: intString(candidate.count) + " sources across " + intString(candidate.domains) + " domains", Evidence: s.conceptEvidence(ctx, userID, candidate.concept, 4), Actions: []string{"review", "connect"}})
	}
	return result
}

func (s *Service) forgottenValueInsights(ctx context.Context, userID string, now time.Time) []deterministicInsight {
	cutoff := now.AddDate(0, 0, -90).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `SELECT b.id,COALESCE(b.title,b.url),st.importance FROM bookmarks b JOIN item_states st ON st.user_id=b.user_id AND st.item_type='bookmark' AND st.item_id=b.id WHERE b.user_id=? AND st.importance>=3 AND COALESCE(b.last_accessed,b.updated_at)<? ORDER BY b.id LIMIT 20`, userID, cutoff)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []deterministicInsight{}
	for rows.Next() {
		var id, title string
		var importance int
		_ = rows.Scan(&id, &title, &importance)
		result = append(result, deterministicInsight{ID: stableKnowledgeID("insight", "forgotten_value", id), Type: "forgotten_value", Title: "Revisit " + title, Explanation: "An item you marked as important has not been revisited recently.", Window: "older_than_90_days", Confidence: 0.9, WhyDetected: "importance " + intString(importance) + " and no recent access", Evidence: []insightEvidence{{ID: id, Type: "bookmark", Title: title}}, Actions: []string{"review", "snooze"}})
	}
	return result
}

func (s *Service) knowledgeGapInsights(ctx context.Context, userID string) []deterministicInsight {
	rows, err := s.db.QueryContext(ctx, `SELECT b.id,COALESCE(b.title,b.url) FROM bookmarks b JOIN item_states st ON st.user_id=b.user_id AND st.item_type='bookmark' AND st.item_id=b.id WHERE b.user_id=? AND st.importance>=3 AND NOT EXISTS(SELECT 1 FROM bookmark_concepts c WHERE c.user_id=b.user_id AND c.bookmark_id=b.id) AND NOT EXISTS(SELECT 1 FROM item_links l WHERE l.user_id=b.user_id AND ((l.from_type='bookmark' AND l.from_id=b.id) OR (l.to_type='bookmark' AND l.to_id=b.id))) ORDER BY b.id LIMIT 20`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []deterministicInsight{}
	for rows.Next() {
		var id, title string
		_ = rows.Scan(&id, &title)
		result = append(result, deterministicInsight{ID: stableKnowledgeID("insight", "knowledge_gap", id), Type: "knowledge_gap", Title: "Develop " + title, Explanation: "This important item has neither concepts nor explicit connections.", Window: "current", Confidence: 1, WhyDetected: "important item with zero concepts and zero explicit links", Evidence: []insightEvidence{{ID: id, Type: "bookmark", Title: title}}, Actions: []string{"connect", "create_note"}})
	}
	return result
}

func (s *Service) serendipitousInsights(ctx context.Context, userID string) []deterministicInsight {
	rows, err := s.db.QueryContext(ctx, `SELECT a.concept,a.bookmark_id,b.bookmark_id,COALESCE(ba.title,ba.url),COALESCE(bb.title,bb.url) FROM bookmark_concepts a JOIN bookmark_concepts b ON b.user_id=a.user_id AND b.concept=a.concept AND b.bookmark_id>a.bookmark_id JOIN bookmarks ba ON ba.user_id=a.user_id AND ba.id=a.bookmark_id JOIN bookmarks bb ON bb.user_id=b.user_id AND bb.id=b.bookmark_id WHERE a.user_id=? AND ba.domain<>bb.domain AND NOT EXISTS(SELECT 1 FROM item_links l WHERE l.user_id=a.user_id AND ((l.from_type='bookmark' AND l.from_id=a.bookmark_id AND l.to_type='bookmark' AND l.to_id=b.bookmark_id) OR (l.from_type='bookmark' AND l.from_id=b.bookmark_id AND l.to_type='bookmark' AND l.to_id=a.bookmark_id))) ORDER BY a.concept,a.bookmark_id,b.bookmark_id LIMIT 20`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []deterministicInsight{}
	for rows.Next() {
		var concept, leftID, rightID, leftTitle, rightTitle string
		_ = rows.Scan(&concept, &leftID, &rightID, &leftTitle, &rightTitle)
		result = append(result, deterministicInsight{ID: stableKnowledgeID("insight", "serendipitous_connection", concept, leftID, rightID), Type: "serendipitous_connection", Title: leftTitle + " and " + rightTitle, Explanation: "Two sources from different domains share a concept but are not explicitly connected.", Window: "all_time", Confidence: 0.8, WhyDetected: "shared concept " + concept + " across different source domains", Evidence: []insightEvidence{{ID: leftID, Type: "bookmark", Title: leftTitle}, {ID: rightID, Type: "bookmark", Title: rightTitle}}, Actions: []string{"review", "connect"}})
	}
	return result
}

func (s *Service) conceptEvidence(ctx context.Context, userID, concept string, limit int) []insightEvidence {
	rows, err := s.db.QueryContext(ctx, `SELECT b.id,COALESCE(b.title,b.url) FROM bookmark_concepts c JOIN bookmarks b ON b.user_id=c.user_id AND b.id=c.bookmark_id WHERE c.user_id=? AND c.concept=? ORDER BY b.updated_at DESC,b.id LIMIT ?`, userID, concept, limit)
	if err != nil {
		return []insightEvidence{}
	}
	defer rows.Close()
	result := []insightEvidence{}
	for rows.Next() {
		var id, title string
		_ = rows.Scan(&id, &title)
		result = append(result, insightEvidence{ID: id, Type: "bookmark", Title: title})
	}
	return result
}

func ratioConfidence(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	value := float64(numerator) / float64(denominator)
	if value < 0.5 {
		return 0.5
	}
	if value > 1 {
		return 1
	}
	return value
}

func intString(value int) string { return fmt.Sprintf("%d", value) }
