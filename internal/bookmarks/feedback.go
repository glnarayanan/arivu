package bookmarks

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
)

func (s *Service) SaveFeedback(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		ItemType   string `json:"item_type"`
		ItemID     string `json:"item_id"`
		Surface    string `json:"surface"`
		Feedback   string `json:"feedback"`
		Action     string `json:"action"`
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		From       string `json:"from"`
		To         string `json:"to"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if targetType := strings.TrimSpace(body.TargetType); targetType != "" {
		s.saveKnowledgeFeedback(w, r, user, targetType, strings.TrimSpace(body.TargetID), firstNonEmpty(strings.TrimSpace(body.Action), strings.TrimSpace(body.Feedback)), strings.TrimSpace(body.From), strings.TrimSpace(body.To))
		return
	}
	itemType := strings.TrimSpace(body.ItemType)
	itemID := strings.TrimSpace(body.ItemID)
	surface := feedbackSurface(body.Surface)
	feedback := strings.TrimSpace(body.Feedback)
	if !validFeedback(feedback) || !s.reviewItemExists(r.Context(), user.ID, itemType, itemID) {
		writeError(w, http.StatusBadRequest, "Invalid feedback")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO result_feedback(user_id,item_type,item_id,surface,feedback,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,item_type,item_id,surface) DO UPDATE SET feedback=excluded.feedback,updated_at=excluded.updated_at`, user.ID, itemType, itemID, surface, feedback, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save feedback")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"feedback": map[string]any{"item_type": itemType, "item_id": itemID, "surface": surface, "feedback": feedback, "updated_at": now}})
}

func (s *Service) saveKnowledgeFeedback(w http.ResponseWriter, r *http.Request, user auth.User, targetType, targetID, feedback, from, to string) {
	if !validKnowledgeFeedback(feedback) || !s.ownsKnowledgeTarget(r.Context(), user.ID, targetType, targetID) {
		writeError(w, http.StatusBadRequest, "Invalid feedback")
		return
	}
	now := time.Now().UTC()
	var snoozedUntil any
	if feedback == "snooze" {
		snoozedUntil = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	}
	formatted := now.Format(time.RFC3339)
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO knowledge_feedback(user_id,target_type,target_id,feedback,snoozed_until,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,target_type,target_id) DO UPDATE SET feedback=excluded.feedback,snoozed_until=excluded.snoozed_until,updated_at=excluded.updated_at`, user.ID, targetType, targetID, feedback, snoozedUntil, formatted, formatted)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save feedback")
		return
	}
	if feedback == "confirm" && targetType == "relationship" && from != "" && to != "" {
		edge, edgeOK := s.knowledgeRelationship(r.Context(), user.ID, targetID)
		fromType, fromID, fromOK := splitGraphNodeID(from)
		toType, toID, toOK := splitGraphNodeID(to)
		if edgeOK && edge.From == from && edge.To == to && fromOK && toOK && (fromType == "bookmark" || fromType == "note") && (toType == "bookmark" || toType == "note") && s.reviewItemExists(r.Context(), user.ID, fromType, fromID) && s.reviewItemExists(r.Context(), user.ID, toType, toID) {
			_, _ = s.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, stableKnowledgeID("link", from, to), user.ID, fromType, fromID, toType, toID, "", "confirmed", formatted)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"feedback": map[string]any{"target_type": targetType, "target_id": targetID, "feedback": feedback, "snoozed_until": snoozedUntil, "updated_at": formatted}})
}

func validKnowledgeFeedback(value string) bool {
	return value == "useful" || value == "not_useful" || value == "snooze" || value == "dismiss" || value == "confirm"
}

func (s *Service) ownsKnowledgeTarget(ctx context.Context, userID, targetType, targetID string) bool {
	if targetID == "" || (targetType != "insight" && targetType != "relationship") {
		return false
	}
	if targetType == "insight" {
		for _, insight := range s.deterministicInsights(ctx, userID, false) {
			if insight.ID == targetID {
				return true
			}
		}
		return false
	}
	_, ok := s.knowledgeRelationship(ctx, userID, targetID)
	return ok
}

func (s *Service) knowledgeRelationship(ctx context.Context, userID, targetID string) (graphV2Edge, bool) {
	nodes, err := s.graphV2Nodes(ctx, userID, 200, "", "", false)
	if err != nil {
		return graphV2Edge{}, false
	}
	for _, edge := range s.graphV2Edges(ctx, userID, nodes, 500, false) {
		if edge.ID == targetID {
			return edge, true
		}
	}
	return graphV2Edge{}, false
}

func (s *Service) knowledgeTargetHidden(ctx context.Context, userID, targetType, targetID string) bool {
	var feedback string
	var snoozed sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT feedback,snoozed_until FROM knowledge_feedback WHERE user_id=? AND target_type=? AND target_id=?`, userID, targetType, targetID).Scan(&feedback, &snoozed); err != nil {
		return false
	}
	return feedback == "dismiss" || (feedback == "snooze" && (!snoozed.Valid || snoozed.String > time.Now().UTC().Format(time.RFC3339)))
}

func (s *Service) hiddenKnowledgeTargets(ctx context.Context, userID, targetType string) map[string]bool {
	rows, err := s.db.QueryContext(ctx, `SELECT target_id,feedback,snoozed_until FROM knowledge_feedback WHERE user_id=? AND target_type=?`, userID, targetType)
	if err != nil {
		return map[string]bool{}
	}
	defer rows.Close()
	hidden := map[string]bool{}
	now := time.Now().UTC().Format(time.RFC3339)
	for rows.Next() {
		var targetID, feedback string
		var snoozed sql.NullString
		if rows.Scan(&targetID, &feedback, &snoozed) == nil && (feedback == "dismiss" || (feedback == "snooze" && (!snoozed.Valid || snoozed.String > now))) {
			hidden[targetID] = true
		}
	}
	return hidden
}

func (s *Service) feedbackState(ctx context.Context, userID, itemType, itemID, surface string) string {
	surface = feedbackSurface(surface)
	var value string
	_ = s.db.QueryRowContext(ctx, `SELECT feedback FROM result_feedback WHERE user_id=? AND item_type=? AND item_id=? AND surface=?`, userID, itemType, itemID, surface).Scan(&value)
	return value
}

func (s *Service) anyFeedbackState(ctx context.Context, userID, itemType, itemID string) string {
	var value string
	_ = s.db.QueryRowContext(ctx, `SELECT feedback FROM result_feedback WHERE user_id=? AND item_type=? AND item_id=? ORDER BY updated_at DESC LIMIT 1`, userID, itemType, itemID).Scan(&value)
	return value
}

func validFeedback(value string) bool {
	return value == "useful" || value == "not_useful" || value == "snooze_longer" || value == "never_resurface"
}

func feedbackSurface(value string) string {
	value = strings.TrimSpace(value)
	if value == "review" || value == "answer" {
		return value
	}
	return "search"
}
