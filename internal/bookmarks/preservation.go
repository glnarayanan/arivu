package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/glnarayanan/arivu/internal/ids"
)

// Payload is an inert snapshot, not an instruction to restore an active workflow.
type preservationRecord struct {
	Kind      string          `json:"kind"`
	LegacyID  string          `json:"legacy_id"`
	Payload   json.RawMessage `json:"payload"`
	NoteID    *string         `json:"note_id"`
	CreatedAt string          `json:"created_at"`
}

var preservationKinds = []string{"daily_notes", "knowledge_objects", "action_items", "reminders", "item_states", "assistant_actions"}

func validatePreservationImport(backup map[string]any) error {
	for _, kind := range preservationKinds {
		seen := map[string]bool{}
		if raw := backup[kind]; raw != nil {
			items, ok := raw.([]any)
			if !ok {
				return fmt.Errorf("invalid legacy %s records", kind)
			}
			for _, item := range items {
				record, ok := item.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid legacy %s record", kind)
				}
				legacyID := stringValue(record["id"])
				if kind == "daily_notes" {
					legacyID = fallback(stringValue(record["date"]), stringValue(record["note_date"]))
				} else if kind == "item_states" {
					legacyID = stringValue(record["item_type"]) + ":" + stringValue(record["item_id"])
				}
				if legacyID == "" || seen[legacyID] {
					return fmt.Errorf("legacy %s record has invalid or duplicate identity", kind)
				}
				seen[legacyID] = true
			}
		}
	}
	_, err := decodePreservationRecords(backup["knowledge_preservation"])
	return err
}

func decodePreservationRecords(raw any) ([]preservationRecord, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var records []preservationRecord
	if json.Unmarshal(data, &records) != nil {
		return nil, errors.New("invalid preservation records")
	}
	for _, record := range records {
		validKind := false
		for _, kind := range preservationKinds {
			validKind = validKind || record.Kind == kind
		}
		var payload map[string]json.RawMessage
		if !validKind || record.LegacyID == "" || record.CreatedAt == "" || json.Unmarshal(record.Payload, &payload) != nil || payload == nil {
			return nil, errors.New("invalid preservation record")
		}
	}
	return records, nil
}

func (s *Service) exportPreservation(ctx context.Context, userID string) ([]preservationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind,legacy_id,payload_json,note_id,created_at FROM knowledge_preservation WHERE user_id=? ORDER BY kind,legacy_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []preservationRecord{}
	for rows.Next() {
		var record preservationRecord
		var payload string
		if err := rows.Scan(&record.Kind, &record.LegacyID, &payload, &record.NoteID, &record.CreatedAt); err != nil {
			return nil, err
		}
		record.Payload = json.RawMessage(payload)
		result = append(result, record)
	}
	return result, rows.Err()
}

func (s *Service) restorePreservation(ctx context.Context, userID string, raw any, notes map[string]string) error {
	records, err := decodePreservationRecords(raw)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, record := range records {
		var already int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_preservation WHERE user_id=? AND kind=? AND legacy_id=?`, userID, record.Kind, record.LegacyID).Scan(&already); err != nil {
			return err
		}
		if already > 0 {
			continue
		}
		var noteID sql.NullString
		if record.NoteID != nil {
			mapped := notes[*record.NoteID]
			if mapped == "" {
				return errors.New("preserved note could not be restored")
			}
			noteID = sql.NullString{String: mapped, Valid: true}
			var owned int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM notes WHERE id=? AND user_id=?`, noteID.String, userID).Scan(&owned); err != nil {
				return errors.New("preserved note could not be restored")
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_preservation(user_id,kind,legacy_id,payload_json,note_id,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(user_id,kind,legacy_id) DO NOTHING`, userID, record.Kind, record.LegacyID, string(record.Payload), noteID, record.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// preparePreservationNoteMappings honors an existing preservation row as a
// tombstone before ordinary notes are restored. A deleted converted note must
// not reappear merely because the same backup is imported again.
func (s *Service) preparePreservationNoteMappings(ctx context.Context, userID string, raw any, notes map[string]string) error {
	records, err := decodePreservationRecords(raw)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.NoteID == nil {
			continue
		}
		var current sql.NullString
		err := s.db.QueryRowContext(ctx, `SELECT note_id FROM knowledge_preservation WHERE user_id=? AND kind=? AND legacy_id=?`, userID, record.Kind, record.LegacyID).Scan(&current)
		if err == nil {
			notes[*record.NoteID] = current.String
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (s *Service) restoreLegacyPreservation(ctx context.Context, userID string, backup map[string]any, oldBookmarks, oldNotes map[string]string, now string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, kind := range preservationKinds {
		for _, raw := range listValue(backup[kind]) {
			record := raw.(map[string]any) // preflight guarantees this shape
			legacyID := stringValue(record["id"])
			if kind == "daily_notes" {
				legacyID = fallback(stringValue(record["date"]), stringValue(record["note_date"]))
			}
			if kind == "item_states" {
				legacyID = stringValue(record["item_type"]) + ":" + stringValue(record["item_id"])
			}
			if legacyID == "" {
				return fmt.Errorf("legacy %s record has no identity", kind)
			}
			payload, err := json.Marshal(record)
			if err != nil {
				return err
			}
			created := fallback(stringValue(record["created_at"]), now)
			result, err := tx.ExecContext(ctx, `INSERT INTO knowledge_preservation(user_id,kind,legacy_id,payload_json,created_at) VALUES(?,?,?,?,?) ON CONFLICT(user_id,kind,legacy_id) DO NOTHING`, userID, kind, legacyID, string(payload), created)
			if err != nil {
				return err
			}
			inserted, err := result.RowsAffected()
			if err != nil || inserted == 0 {
				if err != nil {
					return err
				}
				continue
			}
			title, body := legacyPreservedNote(kind, record)
			if title == "" {
				continue
			}
			noteID := ids.New()
			updated := fallback(stringValue(record["updated_at"]), created)
			if _, err := tx.ExecContext(ctx, `INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,?,?,'preserved',?,?)`, noteID, userID, title, body, created, updated); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE knowledge_preservation SET note_id=? WHERE user_id=? AND kind=? AND legacy_id=?`, noteID, userID, kind, legacyID); err != nil {
				return err
			}
			itemType, oldID := stringValue(record["item_type"]), stringValue(record["item_id"])
			if kind == "knowledge_objects" {
				itemType, oldID = stringValue(record["source_item_type"]), stringValue(record["source_item_id"])
			}
			itemID := remapItemID(itemType, oldID, oldBookmarks, oldNotes)
			if (itemType == "bookmark" || itemType == "note") && itemID != "" {
				table := "bookmarks"
				if itemType == "note" {
					table = "notes"
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) SELECT ?,?,'note',?,?,?,'Preserved context','migration',? WHERE EXISTS(SELECT 1 FROM `+table+` WHERE id=? AND user_id=?)`, ids.New(), userID, noteID, itemType, itemID, now, itemID, userID); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

func legacyPreservedNote(kind string, record map[string]any) (string, string) {
	switch kind {
	case "daily_notes":
		if body := stringValue(record["body"]); strings.TrimSpace(body) != "" {
			return fallback(stringValue(record["date"]), stringValue(record["note_date"])), body
		}
	case "knowledge_objects":
		title := stringValue(record["title"])
		if title == "" {
			title = "Saved " + stringValue(record["object_type"])
		}
		fields := record["object"]
		if fields == nil {
			fields = record["fields"]
		}
		if fields == nil {
			fields = record["fields_json"]
		}
		return title, stringValue(record["description"]) + "\n\nOriginal fields:\n\n" + jsonString(fields)
	case "action_items":
		check := " "
		if stringValue(record["status"]) == "completed" {
			check = "x"
		}
		title := stringValue(record["title"])
		if title != "" {
			return "Saved task: " + title, "- [" + check + "] " + title
		}
	case "reminders":
		return "Saved reminder: " + stringValue(record["due_at"]), stringValue(record["note"]) + "\n\nDue: " + stringValue(record["due_at"]) + "\nTime zone: " + stringValue(record["timezone"]) + "\nRepeat: " + stringValue(record["recurrence"]) + "\nStatus: " + stringValue(record["status"])
	case "item_states":
		if intValue(record["importance"]) > 0 || strings.TrimSpace(stringValue(record["next_action"])) != "" {
			return "Saved context", fmt.Sprintf("%s\n\nPrevious priority: %d\nPrevious stage: %s", stringValue(record["next_action"]), intValue(record["importance"]), stringValue(record["stage"]))
		}
	}
	return "", ""
}
