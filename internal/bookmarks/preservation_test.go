package bookmarks

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

func TestPreservationExportRestoreRemapsNotesAndKeepsTombstones(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "preservation.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"one", "two"} {
		if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,'2026-01-01','2026-01-01')`, id, id+"@example.test", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO daily_notes(user_id,note_date,body,created_at,updated_at) VALUES('one','2026-09-28','Original thought','2026-09-28','2026-09-28');
		INSERT INTO assistant_actions(id,user_id,action_type,payload_json,created_at) VALUES('proposal','one','create_reminder','{"note":"never execute"}','2026-09-28');`); err != nil {
		t.Fatal(err)
	}
	if err := database.PreserveKnowledgeWorkflows(ctx, db); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	exported, err := service.fullExport(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if _, recognized, err := service.restoreFullExport(ctx, "two", raw); err != nil || !recognized {
		t.Fatalf("restore recognized=%v err=%v", recognized, err)
	}
	restored, err := service.exportPreservation(ctx, "two")
	if err != nil || len(restored) != 2 {
		t.Fatalf("preservation=%+v err=%v", restored, err)
	}
	if restored[0].NoteID != nil || string(restored[0].Payload) == "" {
		t.Fatalf("assistant proposal must remain inert: %+v", restored[0])
	}
	var owner, body string
	if restored[1].NoteID == nil {
		t.Fatal("missing converted note")
	}
	if err := db.QueryRow(`SELECT user_id,body FROM notes WHERE id=?`, *restored[1].NoteID).Scan(&owner, &body); err != nil || owner != "two" || body != "Original thought" {
		t.Fatalf("restored note owner=%q body=%q err=%v", owner, body, err)
	}
	if _, err := db.Exec(`DELETE FROM notes WHERE id=?`, *restored[1].NoteID); err != nil {
		t.Fatal(err)
	}
	if err := database.PreserveKnowledgeWorkflows(ctx, db); err != nil {
		t.Fatal(err)
	}
	restored, err = service.exportPreservation(ctx, "two")
	if err != nil || restored[1].NoteID != nil {
		t.Fatalf("deleted conversion resurrected: %+v err=%v", restored, err)
	}
	var executed int
	if err := db.QueryRow(`SELECT count(*) FROM assistant_actions WHERE user_id='two'`).Scan(&executed); err != nil || executed != 0 {
		t.Fatalf("import activated proposals=%d err=%v", executed, err)
	}
}

func TestPreservationRejectsForeignNotesAndFutureBackups(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "preservation.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('one','one@example.test','One','now','now'),('two','two@example.test','Two','now','now');
		INSERT INTO notes(id,user_id,body,created_at,updated_at) VALUES('private','two','Private','now','now');`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	noteID := "old-note"
	records := []preservationRecord{
		{Kind: "daily_notes", LegacyID: "first", Payload: json.RawMessage(`{"body":"first"}`), CreatedAt: "now"},
		{Kind: "daily_notes", LegacyID: "second", Payload: json.RawMessage(`{"body":"second"}`), NoteID: &noteID, CreatedAt: "now"},
	}
	if err := service.restorePreservation(ctx, "one", records, map[string]string{"old-note": "private"}); err == nil {
		t.Fatal("foreign note accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM knowledge_preservation`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial preservation import survived=%d err=%v", count, err)
	}
	if _, recognized, err := service.restoreFullExport(ctx, "one", []byte(`{"version":4,"bookmarks":[]}`)); !recognized || err == nil {
		t.Fatalf("future backup recognized=%v err=%v", recognized, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM import_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unsupported backup wrote jobs=%d err=%v", count, err)
	}
}

func TestLegacyRetirementImportsAreInertAndIdempotentPerOwner(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "preservation.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES
		('one','one@example.test','One','now','now'),('two','two@example.test','Two','now','now'),('three','three@example.test','Three','now','now')`); err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})

	for i, owner := range []string{"one", "two", "three"} {
		version := i + 1
		backup := map[string]any{
			"version":           version,
			"bookmarks":         []any{},
			"action_items":      []any{map[string]any{"id": "task", "title": "Legacy task", "status": "pending"}},
			"reminders":         []any{map[string]any{"id": "reminder", "note": "Legacy reminder", "due_at": "2026-10-01T09:00:00Z", "status": "pending"}},
			"assistant_actions": []any{map[string]any{"id": "proposal", "action_type": "create_reminder", "payload": map[string]any{"note": "never execute"}}},
		}
		raw, err := json.Marshal(backup)
		if err != nil {
			t.Fatal(err)
		}
		if _, recognized, err := service.restoreFullExport(ctx, owner, raw); err != nil || !recognized {
			t.Fatalf("v%d first import recognized=%v err=%v", version, recognized, err)
		}

		var taskNote, reminderNote string
		if err := db.QueryRow(`SELECT note_id FROM knowledge_preservation WHERE user_id=? AND kind='action_items' AND legacy_id='task'`, owner).Scan(&taskNote); err != nil {
			t.Fatalf("v%d task conversion: %v", version, err)
		}
		if err := db.QueryRow(`SELECT note_id FROM knowledge_preservation WHERE user_id=? AND kind='reminders' AND legacy_id='reminder'`, owner).Scan(&reminderNote); err != nil {
			t.Fatalf("v%d reminder conversion: %v", version, err)
		}
		if _, err := db.Exec(`UPDATE notes SET body='owner edit' WHERE id=? AND user_id=?; DELETE FROM notes WHERE id=? AND user_id=?`, taskNote, owner, reminderNote, owner); err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.restoreFullExport(ctx, owner, raw); err != nil {
			t.Fatalf("v%d repeat import: %v", version, err)
		}

		var snapshots, notes, deleted, active int
		if err := db.QueryRow(`SELECT count(*) FROM knowledge_preservation WHERE user_id=?`, owner).Scan(&snapshots); err != nil || snapshots != 3 {
			t.Fatalf("v%d snapshots=%d err=%v", version, snapshots, err)
		}
		if err := db.QueryRow(`SELECT count(*) FROM notes WHERE user_id=? AND source='preserved'`, owner).Scan(&notes); err != nil || notes != 1 {
			t.Fatalf("v%d preserved notes=%d err=%v", version, notes, err)
		}
		if err := db.QueryRow(`SELECT count(*) FROM notes WHERE id=?`, reminderNote).Scan(&deleted); err != nil || deleted != 0 {
			t.Fatalf("v%d deleted converted note recreated=%d err=%v", version, deleted, err)
		}
		var edited string
		if err := db.QueryRow(`SELECT body FROM notes WHERE id=? AND user_id=?`, taskNote, owner).Scan(&edited); err != nil || edited != "owner edit" {
			t.Fatalf("v%d edited converted note=%q err=%v", version, edited, err)
		}
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM reminders WHERE user_id=? AND status='pending') + (SELECT count(*) FROM jobs WHERE user_id=? AND status IN ('queued','leased'))`, owner, owner).Scan(&active); err != nil || active != 0 {
			t.Fatalf("v%d activated reminders/jobs=%d err=%v", version, active, err)
		}
	}
}

func TestV3RepeatImportKeepsBookmarkLinkedConvertedNoteDeleted(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "preservation.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('one','one@example.test','One','now','now')`); err != nil {
		t.Fatal(err)
	}
	noteID := "converted-note"
	backup := map[string]any{
		"version":                3,
		"bookmarks":              []any{map[string]any{"id": "bookmark", "url": "https://example.com/retired", "notes": []any{map[string]any{"id": noteID, "title": "Saved task", "body": "legacy body", "source": "preserved"}}}},
		"notes":                  []any{map[string]any{"id": noteID, "title": "Saved task", "body": "legacy body", "source": "preserved"}},
		"knowledge_preservation": []any{map[string]any{"kind": "action_items", "legacy_id": "task", "payload": map[string]any{"id": "task"}, "note_id": noteID, "created_at": "2026-09-28T00:00:00Z"}},
		"action_items":           []any{map[string]any{"id": "task", "title": "Must not duplicate converted note"}},
		"item_links":             []any{map[string]any{"from_type": "note", "from_id": noteID, "to_type": "bookmark", "to_id": "bookmark", "label": "Preserved context"}},
	}
	raw, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
	if _, _, err := service.restoreFullExport(ctx, "one", raw); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := db.QueryRow(`SELECT count(*) FROM item_links WHERE user_id='one' AND from_id=? AND to_type='bookmark'`, noteID).Scan(&links); err != nil || links != 1 {
		t.Fatalf("bookmark-linked fixture links=%d err=%v", links, err)
	}
	if _, err := db.Exec(`DELETE FROM notes WHERE id=? AND user_id='one'`, noteID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.restoreFullExport(ctx, "one", raw); err != nil {
		t.Fatalf("repeat import: %v", err)
	}
	var notes int
	if err := db.QueryRow(`SELECT count(*) FROM notes WHERE id=? AND user_id='one'`, noteID).Scan(&notes); err != nil || notes != 0 {
		t.Fatalf("deleted bookmark-linked converted note recreated=%d err=%v", notes, err)
	}
}

func TestRetirementImportPreflightRejectsMalformedDataWithoutWrites(t *testing.T) {
	fixtures := []struct {
		name string
		raw  string
	}{
		{"v1 duplicate identity", `{"version":1,"bookmarks":[],"reminders":[{"id":"same"},{"id":"same"}]}`},
		{"v2 invalid records", `{"version":2,"bookmarks":[],"action_items":{"id":"not-a-list"}}`},
		{"v3 invalid preservation", `{"version":3,"bookmarks":[],"knowledge_preservation":[{"kind":"reminders","legacy_id":"r1","payload":null,"created_at":"now"}]}`},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := database.Open(ctx, filepath.Join(t.TempDir(), "preservation.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('one','one@example.test','One','now','now')`); err != nil {
				t.Fatal(err)
			}
			service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
			if _, recognized, err := service.restoreFullExport(ctx, "one", []byte(fixture.raw)); !recognized || err == nil {
				t.Fatalf("recognized=%v err=%v", recognized, err)
			}
			var writes int
			if err := db.QueryRow(`SELECT (SELECT count(*) FROM import_jobs) + (SELECT count(*) FROM knowledge_preservation) + (SELECT count(*) FROM notes) + (SELECT count(*) FROM jobs)`).Scan(&writes); err != nil || writes != 0 {
				t.Fatalf("malformed preflight wrote rows=%d err=%v", writes, err)
			}
		})
	}
}
