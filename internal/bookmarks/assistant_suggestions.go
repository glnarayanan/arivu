package bookmarks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
)

const maxAssistantSuggestions = 12

func (s *Service) AssistantSuggestions(w http.ResponseWriter, r *http.Request, user auth.User) {
	var body struct {
		Mode     string `json:"mode"`
		Stage    string `json:"stage"`
		Query    string `json:"query"`
		ItemType string `json:"item_type"`
		ItemID   string `json:"item_id"`
		Limit    int    `json:"limit"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = "inbox"
	}
	limit := body.Limit
	if limit <= 0 {
		limit = 6
	}
	if limit > maxAssistantSuggestions {
		limit = maxAssistantSuggestions
	}
	sources, err := s.assistantSuggestionSources(r.Context(), user.ID, mode, body.Stage, body.Query, body.ItemType, body.ItemID, limit)
	if err != nil {
		if invalid, ok := err.(errInvalid); ok {
			writeError(w, http.StatusBadRequest, invalid.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not generate assistant suggestions")
		return
	}
	suggestions := s.assistantDrafts(r.Context(), user.ID, sources, limit)
	writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "inert": true, "suggestions": suggestions})
}

func (s *Service) assistantSuggestionSources(ctx context.Context, userID, mode, stage, query, itemType, itemID string, limit int) ([]map[string]any, error) {
	switch mode {
	case "inbox":
		stage = strings.TrimSpace(stage)
		if stage == "" {
			stage = "inbox"
		}
		if !validItemStage(stage) {
			return nil, errInvalid("invalid inbox stage")
		}
		items, err := s.inboxItems(ctx, userID, stage, limit)
		if err != nil {
			return nil, err
		}
		return assistantSourcesFromItems(ctx, s, userID, items), nil
	case "review":
		items, err := s.reviewItems(ctx, userID, limit)
		if err != nil {
			return nil, err
		}
		return assistantSourcesFromItems(ctx, s, userID, items), nil
	case "search":
		query = strings.TrimSpace(query)
		if len(query) < 2 || len(query) > maxSearchLen {
			return nil, errInvalid("query must be between 2 and 2000 characters")
		}
		results, _, err := s.searchIndex(ctx, userID, query, url.Values{}, limit)
		if err != nil {
			return nil, err
		}
		return assistantSourcesFromItems(ctx, s, userID, results), nil
	case "item":
		itemType = strings.TrimSpace(itemType)
		itemID = strings.TrimSpace(itemID)
		if !s.reviewItemExists(ctx, userID, itemType, itemID) {
			return nil, errInvalid("assistant item not found")
		}
		return []map[string]any{assistantSource(ctx, s, userID, itemType, itemID, "")}, nil
	default:
		return nil, errInvalid("invalid assistant suggestion mode")
	}
}

func assistantSourcesFromItems(ctx context.Context, s *Service, userID string, items []map[string]any) []map[string]any {
	sources := []map[string]any{}
	seen := map[string]struct{}{}
	for _, item := range items {
		itemType := stringValue(firstPresent(item, "item_type", "type"))
		itemID := stringValue(firstPresent(item, "item_id", "id"))
		if itemType == "" {
			itemType = "bookmark"
		}
		if itemID == "" || !s.reviewItemExists(ctx, userID, itemType, itemID) {
			continue
		}
		key := itemType + ":" + itemID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		sources = append(sources, assistantSource(ctx, s, userID, itemType, itemID, stringValue(item["title"])))
	}
	return sources
}

func assistantSource(ctx context.Context, s *Service, userID, itemType, itemID, title string) map[string]any {
	title = fallback(strings.TrimSpace(title), s.itemTitle(ctx, userID, itemType, itemID))
	return map[string]any{"item_type": itemType, "item_id": itemID, "title": title, "href": itemHref(itemType, itemID)}
}

func firstPresent(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			return value
		}
	}
	return nil
}

func (s *Service) assistantDrafts(ctx context.Context, userID string, sources []map[string]any, limit int) []map[string]any {
	var drafts []map[string]any
	for _, source := range sources {
		if len(drafts) >= limit {
			break
		}
		itemType := stringValue(source["item_type"])
		itemID := stringValue(source["item_id"])
		title := fallback(stringValue(source["title"]), "saved item")
		state := s.itemState(ctx, userID, itemType, itemID)
		nextAction := strings.TrimSpace(stringValue(state["next_action"]))
		if nextAction == "" {
			nextAction = "Review " + title + " and decide the next use."
		}
		drafts = appendAssistantDraft(ctx, s, userID, drafts, "update_item_state", map[string]any{
			"item_type":   itemType,
			"item_id":     itemID,
			"stage":       assistantNextStage(stringValue(state["stage"])),
			"importance":  assistantImportance(intValue(state["importance"])),
			"next_action": nextAction,
		}, "Move "+title+" forward", "Saved context has not been fully processed.", source, limit)
		if len(drafts) >= limit {
			break
		}
		drafts = appendAssistantDraft(ctx, s, userID, drafts, "create_action_item", map[string]any{
			"item_type": itemType,
			"item_id":   itemID,
			"title":     nextAction,
		}, "Create a task for "+title, "The item has a concrete next action worth tracking.", source, limit)
		if len(drafts) >= limit {
			break
		}
		due := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
		drafts = appendAssistantDraft(ctx, s, userID, drafts, "create_reminder", map[string]any{
			"item_type":                itemType,
			"item_id":                  itemID,
			"due_at":                   due,
			"timezone":                 "UTC",
			"recurrence":               "none",
			"notification_channel":     "in_app",
			"note":                     "Revisit " + title,
			"recurrence_interval_days": 0,
		}, "Set a reminder for "+title, "A dated nudge can bring this context back without changing the item.", source, limit)
	}
	if len(drafts) < limit && len(sources) >= 2 {
		first := sources[0]
		second := sources[1]
		drafts = appendAssistantDraft(ctx, s, userID, drafts, "create_link", map[string]any{
			"from_type": stringValue(first["item_type"]),
			"from_id":   stringValue(first["item_id"]),
			"to_type":   stringValue(second["item_type"]),
			"to_id":     stringValue(second["item_id"]),
			"label":     "related",
		}, "Link related saved items", "Both items appeared in the same planning context.", first, limit)
	}
	for i := range drafts {
		drafts[i]["draft_id"] = fmt.Sprintf("draft-%d", i+1)
	}
	return drafts
}

func appendAssistantDraft(ctx context.Context, s *Service, userID string, drafts []map[string]any, actionType string, payload map[string]any, title string, reason string, source map[string]any, limit int) []map[string]any {
	if len(drafts) >= limit || !validAssistantAction(actionType) {
		return drafts
	}
	raw, _ := json.Marshal(payload)
	if len(raw) > maxActionPayload {
		return drafts
	}
	if err := s.validateAssistantPayload(ctx, userID, actionType, payload); err != nil {
		return drafts
	}
	return append(drafts, map[string]any{"title": title, "reason": reason, "action_type": actionType, "payload": payload, "source": source})
}

func assistantNextStage(stage string) string {
	switch stage {
	case "inbox", "":
		return "processing"
	case "processing":
		return "processed"
	default:
		return stage
	}
}

func assistantImportance(value int) int {
	if value < 1 {
		return 3
	}
	if value > 5 {
		return 5
	}
	return value
}
