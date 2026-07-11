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
	if !validKnowledgeFeedback(targetType, feedback) || targetID == "" {
		writeError(w, http.StatusBadRequest, "Invalid feedback")
		return
	}
	var relationship graphV2Edge
	var targetOwned bool
	if targetType == "insight" {
		targetOwned = s.ownsInsight(r.Context(), user.ID, targetID)
	} else {
		relationship, targetOwned = s.knowledgeRelationshipBetween(r.Context(), user.ID, targetID, from, to)
	}
	if !targetOwned {
		writeError(w, http.StatusBadRequest, "Invalid feedback")
		return
	}
	fromType, fromID, fromOK := splitGraphNodeID(relationship.From)
	toType, toID, toOK := splitGraphNodeID(relationship.To)
	if feedback == "confirm" && (!fromOK || !toOK || (fromType != "bookmark" && fromType != "note") || (toType != "bookmark" && toType != "note")) {
		writeError(w, http.StatusBadRequest, "Relationship cannot be confirmed as an explicit link")
		return
	}
	now := time.Now().UTC()
	var snoozedUntil any
	if feedback == "snooze" {
		snoozedUntil = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	}
	formatted := now.Format(time.RFC3339)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save feedback")
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO knowledge_feedback(user_id,target_type,target_id,feedback,snoozed_until,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,target_type,target_id) DO UPDATE SET feedback=excluded.feedback,snoozed_until=excluded.snoozed_until,updated_at=excluded.updated_at`, user.ID, targetType, targetID, feedback, snoozedUntil, formatted, formatted); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save feedback")
		return
	}
	confirmedLinkCreated := false
	if feedback == "confirm" {
		result, linkErr := tx.ExecContext(r.Context(), `INSERT OR IGNORE INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, stableKnowledgeID("link", relationship.From, relationship.To), user.ID, fromType, fromID, toType, toID, "", "confirmed", formatted)
		if linkErr != nil {
			writeError(w, http.StatusInternalServerError, "Could not confirm relationship")
			return
		}
		created, _ := result.RowsAffected()
		confirmedLinkCreated = created > 0
	}
	if err = tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save feedback")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"feedback": map[string]any{"target_type": targetType, "target_id": targetID, "feedback": feedback, "snoozed_until": snoozedUntil, "confirmed_link_created": confirmedLinkCreated, "updated_at": formatted}})
}

func validKnowledgeFeedback(targetType, value string) bool {
	if targetType == "insight" {
		return value == "useful" || value == "not_useful" || value == "snooze" || value == "dismiss"
	}
	return targetType == "relationship" && (value == "useful" || value == "not_useful" || value == "dismiss" || value == "confirm")
}

func (s *Service) ownsInsight(ctx context.Context, userID, targetID string) bool {
	for _, insight := range s.deterministicInsights(ctx, userID, false) {
		if insight.ID == targetID {
			return true
		}
	}
	return false
}

func (s *Service) knowledgeRelationshipBetween(ctx context.Context, userID, targetID, from, to string) (graphV2Edge, bool) {
	fromType, fromID, fromOK := splitGraphNodeID(from)
	toType, toID, toOK := splitGraphNodeID(to)
	if !fromOK || !toOK {
		return graphV2Edge{}, false
	}
	fromNode, fromOwned := s.graphV2Node(ctx, userID, fromType, fromID)
	toNode, toOwned := s.graphV2Node(ctx, userID, toType, toID)
	if !fromOwned || !toOwned {
		return graphV2Edge{}, false
	}
	for _, edge := range s.graphV2Edges(ctx, userID, []graphV2Node{fromNode, toNode}, 32, false) {
		if edge.ID == targetID && edge.From == from && edge.To == to {
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
