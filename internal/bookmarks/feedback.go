package bookmarks

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
)

func (s *Service) SaveFeedback(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		ItemType string `json:"item_type"`
		ItemID   string `json:"item_id"`
		Surface  string `json:"surface"`
		Feedback string `json:"feedback"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
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
