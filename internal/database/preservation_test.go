package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const preservationTime = "2026-09-28T12:34:56.789Z"

func TestPreserveKnowledgeWorkflowsSnapshotsMetadataAndSelectivelyCreatesNotes(t *testing.T) {
	ctx, db := openPreservationDB(t)
	seedPreservationFixtures(t, ctx, db)

	if err := PreserveKnowledgeWorkflows(ctx, db); err != nil {
		t.Fatalf("PreserveKnowledgeWorkflows: %v", err)
	}

	expected := expectedPreservationPayloads()
	rows, err := db.QueryContext(ctx, `SELECT user_id,kind,legacy_id,payload_json,note_id,created_at FROM knowledge_preservation`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make(map[string]map[string]any)
	noteLinks := make(map[string]bool)
	for rows.Next() {
		var userID, kind, legacyID, payload, createdAt string
		var noteID sql.NullString
		if err := rows.Scan(&userID, &kind, &legacyID, &payload, &noteID, &createdAt); err != nil {
			t.Fatal(err)
		}
		key := preservationKey(userID, kind, legacyID)
		got[key] = decodeJSONObject(t, payload)
		noteLinks[key] = noteID.Valid
		if createdAt == "" {
			t.Errorf("%s has empty preservation created_at", key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var foreignNotes int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_preservation p JOIN notes n ON n.id=p.note_id WHERE n.user_id<>p.user_id`).Scan(&foreignNotes); err != nil || foreignNotes != 0 {
		t.Fatalf("cross-owner converted notes=%d err=%v", foreignNotes, err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("preserved payloads differ\n got: %#v\nwant: %#v", got, expected)
	}

	wantNotes := map[string]bool{
		preservationKey("u1", "daily_notes", "2026-09-28"):     true,
		preservationKey("u2", "daily_notes", "2026-09-28"):     true,
		preservationKey("u1", "knowledge_objects", "object-1"): true,
		preservationKey("u1", "action_items", "task-1"):        true,
		preservationKey("u1", "reminders", "reminder-1"):       true,
		preservationKey("u1", "item_states", "note:n-target"):  true,
	}
	for key := range expected {
		if noteLinks[key] != wantNotes[key] {
			t.Errorf("%s note link = %v, want %v", key, noteLinks[key], wantNotes[key])
		}
	}
	var generatedNotes int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM notes WHERE id <> 'n-target'`).Scan(&generatedNotes); err != nil {
		t.Fatal(err)
	}
	if generatedNotes != len(wantNotes) {
		t.Errorf("generated notes = %d, want %d", generatedNotes, len(wantNotes))
	}

	// Reading the source rows back as JSON also guards against conversion updating,
	// normalizing, or deleting legacy data.
	assertSourcePayloadsUnchanged(t, ctx, db, expected)
}

func TestPreserveKnowledgeWorkflowsIsIdempotentAfterConvertedNotesChange(t *testing.T) {
	ctx, db := openPreservationDB(t)
	seedPreservationFixtures(t, ctx, db)
	if err := PreserveKnowledgeWorkflows(ctx, db); err != nil {
		t.Fatal(err)
	}

	var editedID, deletedID string
	if err := db.QueryRowContext(ctx, `SELECT note_id FROM knowledge_preservation WHERE user_id='u1' AND kind='knowledge_objects' AND legacy_id='object-1'`).Scan(&editedID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT note_id FROM knowledge_preservation WHERE user_id='u1' AND kind='reminders' AND legacy_id='reminder-1'`).Scan(&deletedID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE notes SET title='user edited title',body='user edited body' WHERE id=?`, editedID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM notes WHERE id=?`, deletedID); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM notes`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	if err := PreserveKnowledgeWorkflows(ctx, db); err != nil {
		t.Fatalf("second conversion: %v", err)
	}
	var after, snapshots int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM notes`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_preservation`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if before != after || snapshots != len(expectedPreservationPayloads()) {
		t.Fatalf("repeat changed counts: notes %d -> %d, snapshots=%d", before, after, snapshots)
	}
	var title, body string
	if err := db.QueryRowContext(ctx, `SELECT title,body FROM notes WHERE id=?`, editedID).Scan(&title, &body); err != nil {
		t.Fatal(err)
	}
	if title != "user edited title" || body != "user edited body" {
		t.Fatalf("edited converted note was overwritten: title=%q body=%q", title, body)
	}
	var deletedLink sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT note_id FROM knowledge_preservation WHERE user_id='u1' AND kind='reminders' AND legacy_id='reminder-1'`).Scan(&deletedLink); err != nil {
		t.Fatal(err)
	}
	if deletedLink.Valid {
		t.Fatalf("deleted converted note was recreated as %q", deletedLink.String)
	}
}

func TestPreserveKnowledgeWorkflowsRollsBackSnapshotAndNotesOnRejection(t *testing.T) {
	ctx, db := openPreservationDB(t)
	// Initialize the preservation schema before installing the failure injection.
	if err := PreserveKnowledgeWorkflows(ctx, db); err != nil {
		t.Fatalf("initialize preservation: %v", err)
	}
	seedPreservationFixtures(t, ctx, db)
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_preserved_reminder BEFORE INSERT ON knowledge_preservation
		WHEN NEW.kind='reminders' BEGIN SELECT RAISE(ABORT, 'test rejection'); END`); err != nil {
		t.Fatal(err)
	}

	if err := PreserveKnowledgeWorkflows(ctx, db); err == nil {
		t.Fatal("conversion unexpectedly succeeded despite rejecting trigger")
	}
	var snapshots, notes int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_preservation`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM notes WHERE id <> 'n-target'`).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 || notes != 0 {
		t.Fatalf("failed conversion left partial work: snapshots=%d generated_notes=%d", snapshots, notes)
	}
	assertSourcePayloadsUnchanged(t, ctx, db, expectedPreservationPayloads())
}

func openPreservationDB(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "preservation.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return ctx, db
}

func seedPreservationFixtures(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO users(id,email,name,created_at,updated_at) VALUES
		 ('u1','one@example.test','One',?,?),('u2','two@example.test','Two',?,?)`,
		`INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES('n-target','u1','Existing','Do not alter','manual',?,?)`,
		`INSERT INTO daily_notes(user_id,note_date,body,created_at,updated_at) VALUES
		 ('u1','2026-09-28','alpha daily',?,?),('u2','2026-09-28','beta daily',?,?),('u1','2026-09-27','',?,?)`,
		`INSERT INTO knowledge_objects(id,user_id,object_type,title,description,fields_json,source_item_type,source_item_id,created_at,updated_at)
		 VALUES('object-1','u1','project','Nested object','full metadata','{"plan":{"steps":[{"name":"ship","done":false}],"owner":null},"labels":["a","β"]}','note','n-target',?,?)`,
		`INSERT INTO action_items(id,user_id,item_type,item_id,title,status,created_at,completed_at)
		 VALUES('task-1','u1','note','n-target','Finish migration','completed',?,'2026-09-29T01:02:03Z')`,
		`INSERT INTO reminders(id,user_id,item_type,item_id,due_at,timezone,recurrence,recurrence_interval_days,notification_channel,note,status,created_at,completed_at,last_notified_at,last_completed_at)
		 VALUES('reminder-1','u1','note','n-target','2026-10-31T23:59:58.123456Z','Asia/Kathmandu','custom',17,'email','retain timestamps','completed',?,'2026-11-01T00:00:00Z',NULL,'2026-11-02T02:03:04.500Z')`,
		`INSERT INTO item_states(user_id,item_type,item_id,stage,importance,next_action,created_at,updated_at) VALUES
		 ('u1','note','n-target','processing',4,'Call Alice',?,?),('u2','bookmark','missing-bookmark','inbox',0,'',?,?)`,
		`INSERT INTO assistant_actions(id,user_id,action_type,payload_json,status,result_json,error,created_at,decided_at,executed_at)
		 VALUES('assistant-1','u1','create_reminder','{"input":{"at":"tomorrow","options":[1,{"x":null}]}}','rejected','{"reason":"owner choice"}','not run',?,'2026-09-28T13:00:00Z',NULL)`,
	}
	for i, statement := range statements {
		args := make([]any, countPlaceholders(statement))
		for j := range args {
			args[j] = preservationTime
		}
		if _, err := db.ExecContext(ctx, statement, args...); err != nil {
			t.Fatalf("fixture statement %d: %v", i, err)
		}
	}
}

func expectedPreservationPayloads() map[string]map[string]any {
	return map[string]map[string]any{
		preservationKey("u1", "daily_notes", "2026-09-28"):                decodeExpected(`{"note_date":"2026-09-28","body":"alpha daily","created_at":"2026-09-28T12:34:56.789Z","updated_at":"2026-09-28T12:34:56.789Z"}`),
		preservationKey("u2", "daily_notes", "2026-09-28"):                decodeExpected(`{"note_date":"2026-09-28","body":"beta daily","created_at":"2026-09-28T12:34:56.789Z","updated_at":"2026-09-28T12:34:56.789Z"}`),
		preservationKey("u1", "daily_notes", "2026-09-27"):                decodeExpected(`{"note_date":"2026-09-27","body":"","created_at":"2026-09-28T12:34:56.789Z","updated_at":"2026-09-28T12:34:56.789Z"}`),
		preservationKey("u1", "knowledge_objects", "object-1"):            decodeExpected(`{"id":"object-1","object_type":"project","title":"Nested object","description":"full metadata","fields_json":"{\"plan\":{\"steps\":[{\"name\":\"ship\",\"done\":false}],\"owner\":null},\"labels\":[\"a\",\"β\"]}","source_item_type":"note","source_item_id":"n-target","created_at":"2026-09-28T12:34:56.789Z","updated_at":"2026-09-28T12:34:56.789Z"}`),
		preservationKey("u1", "action_items", "task-1"):                   decodeExpected(`{"id":"task-1","item_type":"note","item_id":"n-target","title":"Finish migration","status":"completed","created_at":"2026-09-28T12:34:56.789Z","completed_at":"2026-09-29T01:02:03Z"}`),
		preservationKey("u1", "reminders", "reminder-1"):                  decodeExpected(`{"id":"reminder-1","item_type":"note","item_id":"n-target","due_at":"2026-10-31T23:59:58.123456Z","timezone":"Asia/Kathmandu","recurrence":"custom","recurrence_interval_days":17,"notification_channel":"email","note":"retain timestamps","status":"completed","created_at":"2026-09-28T12:34:56.789Z","completed_at":"2026-11-01T00:00:00Z","last_notified_at":null,"last_completed_at":"2026-11-02T02:03:04.500Z"}`),
		preservationKey("u1", "item_states", "note:n-target"):             decodeExpected(`{"item_type":"note","item_id":"n-target","stage":"processing","importance":4,"next_action":"Call Alice","created_at":"2026-09-28T12:34:56.789Z","updated_at":"2026-09-28T12:34:56.789Z"}`),
		preservationKey("u2", "item_states", "bookmark:missing-bookmark"): decodeExpected(`{"item_type":"bookmark","item_id":"missing-bookmark","stage":"inbox","importance":0,"next_action":"","created_at":"2026-09-28T12:34:56.789Z","updated_at":"2026-09-28T12:34:56.789Z"}`),
		preservationKey("u1", "assistant_actions", "assistant-1"):         decodeExpected(`{"id":"assistant-1","action_type":"create_reminder","payload_json":"{\"input\":{\"at\":\"tomorrow\",\"options\":[1,{\"x\":null}]}}","status":"rejected","result_json":"{\"reason\":\"owner choice\"}","error":"not run","created_at":"2026-09-28T12:34:56.789Z","decided_at":"2026-09-28T13:00:00Z","executed_at":null}`),
	}
}

func assertSourcePayloadsUnchanged(t *testing.T, ctx context.Context, db *sql.DB, expected map[string]map[string]any) {
	t.Helper()
	for _, source := range []struct{ table, legacyExpr string }{
		{"daily_notes", "note_date"}, {"knowledge_objects", "id"}, {"action_items", "id"},
		{"reminders", "id"}, {"item_states", "item_type || ':' || item_id"}, {"assistant_actions", "id"},
	} {
		columns := tableColumns(t, db, source.table)
		var payloadParts []string
		for _, column := range columns {
			if column != "user_id" {
				payloadParts = append(payloadParts, fmt.Sprintf("'%s',%s", column, column))
			}
		}
		query := fmt.Sprintf(`SELECT user_id,%s,json_object(%s) FROM %s`, source.legacyExpr, joinComma(payloadParts), source.table)
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var userID, legacyID, payload string
			if err := rows.Scan(&userID, &legacyID, &payload); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			key := preservationKey(userID, source.table, legacyID)
			if got := decodeJSONObject(t, payload); !reflect.DeepEqual(got, expected[key]) {
				t.Errorf("source changed for %s: got %#v want %#v", key, got, expected[key])
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func preservationKey(userID, kind, legacyID string) string {
	return userID + "/" + kind + "/" + legacyID
}

func decodeExpected(value string) map[string]any {
	var result map[string]any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		panic(err)
	}
	return result
}

func decodeJSONObject(t *testing.T, value string) map[string]any {
	t.Helper()
	var result map[string]any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode JSON %q: %v", value, err)
	}
	return result
}

func countPlaceholders(s string) int {
	n := 0
	for _, r := range s {
		if r == '?' {
			n++
		}
	}
	return n
}

func joinComma(values []string) string {
	sort.Strings(values)
	result := ""
	for i, value := range values {
		if i > 0 {
			result += ","
		}
		result += value
	}
	return result
}
