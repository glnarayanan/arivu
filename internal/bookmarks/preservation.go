package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Payload is an inert snapshot, not an instruction to restore an active workflow.
type preservationRecord struct {
	Kind      string          `json:"kind"`
	LegacyID  string          `json:"legacy_id"`
	Payload   json.RawMessage `json:"payload"`
	NoteID    *string         `json:"note_id"`
	CreatedAt string          `json:"created_at"`
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
	if raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var records []preservationRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return errors.New("invalid preservation records")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, record := range records {
		switch record.Kind {
		case "daily_notes", "knowledge_objects", "action_items", "reminders", "item_states", "assistant_actions":
		default:
			return errors.New("unknown preservation record kind")
		}
		var payload map[string]json.RawMessage
		if record.LegacyID == "" || record.CreatedAt == "" || json.Unmarshal(record.Payload, &payload) != nil || payload == nil {
			return errors.New("invalid preservation record")
		}
		var noteID sql.NullString
		if record.NoteID != nil {
			noteID = sql.NullString{String: notes[*record.NoteID], Valid: true}
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
