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
