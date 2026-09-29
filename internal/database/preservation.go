package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/ids"
)

// PreserveKnowledgeWorkflows makes editable notes without destroying the exact
// legacy records. Migrate invokes it when opening the database.
// The preservation row doubles as a tombstone: deleting or editing a converted
// note must never cause the next migration to recreate or overwrite it.
func PreserveKnowledgeWorkflows(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, kind := range []string{"daily_notes", "knowledge_objects", "action_items", "reminders", "item_states", "assistant_actions"} {
		if err := preserveWorkflow(ctx, tx, kind); err != nil {
			return fmt.Errorf("preserve %s: %w", kind, err)
		}
	}
	return tx.Commit()
}

func preserveWorkflow(ctx context.Context, tx *sql.Tx, kind string) error {
	// kind is an internal allowlisted table name, never input from a request.
	identity := "legacy.id"
	if kind == "daily_notes" {
		identity = "legacy.note_date"
	} else if kind == "item_states" {
		identity = "legacy.item_type || ':' || legacy.item_id"
	}
	rows, err := tx.QueryContext(ctx, `SELECT legacy.* FROM `+kind+` legacy WHERE NOT EXISTS(SELECT 1 FROM knowledge_preservation p WHERE p.user_id=legacy.user_id AND p.kind=? AND p.legacy_id=`+identity+`)`, kind)
	if err != nil {
		return err
	}
	columns, err := rows.Columns()
	if err != nil {
		rows.Close()
		return err
	}
	var records []map[string]any
	for rows.Next() {
		values, targets := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			rows.Close()
			return err
		}
		record := make(map[string]any, len(columns))
		for i, column := range columns {
			if bytes, ok := values[i].([]byte); ok {
				values[i] = string(bytes)
			}
			record[column] = values[i]
		}
		records = append(records, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, record := range records {
		owner := preservedText(record, "user_id")
		delete(record, "user_id")
		legacyID := preservedText(record, "id")
		if kind == "daily_notes" {
			legacyID = preservedText(record, "note_date")
		} else if kind == "item_states" {
			legacyID = preservedText(record, "item_type") + ":" + preservedText(record, "item_id")
		}
		payload, err := json.Marshal(record)
		if err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339)
		result, err := tx.ExecContext(ctx, `INSERT INTO knowledge_preservation(user_id,kind,legacy_id,payload_json,created_at) VALUES(?,?,?,?,?) ON CONFLICT(user_id,kind,legacy_id) DO NOTHING`, owner, kind, legacyID, string(payload), now)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			continue
		}
		title, body := preservedNote(kind, record)
		if title == "" {
			continue
		}
		noteID := ids.New()
		created, updated := preservedText(record, "created_at"), preservedText(record, "updated_at")
		if created == "" {
			created = now
		}
		if updated == "" {
			updated = created
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,?,?,'preserved',?,?)`, noteID, owner, title, body, created, updated); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_preservation SET note_id=? WHERE user_id=? AND kind=? AND legacy_id=?`, noteID, owner, kind, legacyID); err != nil {
			return err
		}
		itemType, itemID := preservedText(record, "item_type"), preservedText(record, "item_id")
		if kind == "knowledge_objects" {
			itemType, itemID = preservedText(record, "source_item_type"), preservedText(record, "source_item_id")
		}
		if itemType == "bookmark" || itemType == "note" {
			table := "bookmarks"
			if itemType == "note" {
				table = "notes"
			}
			// Imported polymorphic references may be stale or belong to someone else.
			if _, err := tx.ExecContext(ctx, `INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) SELECT ?,?,'note',?,?,?,'Preserved context','migration',? WHERE EXISTS(SELECT 1 FROM `+table+` WHERE id=? AND user_id=?)`, ids.New(), owner, noteID, itemType, itemID, now, itemID, owner); err != nil {
				return err
			}
		}
	}
	return nil
}

func preservedText(record map[string]any, key string) string {
	value, _ := record[key].(string)
	return value
}

func preservedNote(kind string, record map[string]any) (string, string) {
	switch kind {
	case "daily_notes":
		if strings.TrimSpace(preservedText(record, "body")) != "" {
			return preservedText(record, "note_date"), preservedText(record, "body")
		}
	case "knowledge_objects":
		title := preservedText(record, "title")
		if title == "" {
			title = "Saved " + preservedText(record, "object_type")
		}
		return title, preservedText(record, "description") + "\n\nOriginal fields:\n\n" + preservedText(record, "fields_json")
	case "action_items":
		check := " "
		if preservedText(record, "status") == "completed" {
			check = "x"
		}
		return "Saved task: " + preservedText(record, "title"), "- [" + check + "] " + preservedText(record, "title")
	case "reminders":
		return "Saved reminder: " + preservedText(record, "due_at"), preservedText(record, "note") + "\n\nDue: " + preservedText(record, "due_at") + "\nTime zone: " + preservedText(record, "timezone") + "\nRepeat: " + preservedText(record, "recurrence") + "\nStatus: " + preservedText(record, "status")
	case "item_states":
		importance, _ := record["importance"].(int64)
		if importance > 0 || strings.TrimSpace(preservedText(record, "next_action")) != "" {
			return "Saved context", fmt.Sprintf("%s\n\nPrevious priority: %d\nPrevious stage: %s", preservedText(record, "next_action"), importance, preservedText(record, "stage"))
		}
	}
	return "", ""
}
