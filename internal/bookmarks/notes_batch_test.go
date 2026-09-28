package bookmarks

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

func TestDecorateNotesMatchesSingletonAtLimitsAndIsolatesOwners(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	for _, user := range []string{"owner", "other"} {
		seedKnowledgeUser(t, db, user, user+"@example.com")
		seedKnowledgeBookmark(t, db, user, user+"-b1", "Bookmark one", "2026-01-01T00:00:00Z")
		seedKnowledgeBookmark(t, db, user, user+"-b2", "Bookmark two", "2026-01-01T00:00:00Z")
	}
	for _, id := range []string{"empty", "full"} {
		if _, err := db.Exec(`INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, "owner", map[string]string{"empty": "", "full": "Full"}[id], "body", "manual", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO item_states(user_id,item_type,item_id,stage,importance,next_action,created_at,updated_at) VALUES('owner','note','full','processed',4,'ship','2026-01-01','2026-01-02')`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO action_items(id,user_id,item_type,item_id,title,status,created_at) VALUES('empty-title','owner','note','empty','Task','pending','2026-01-01')`,
		`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES('empty-link','owner','note','empty','bookmark','other-b1','Unowned endpoint','manual','2026-01-01')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 101; i++ {
		stamp := fmt.Sprintf("2026-01-01T00:%02d:%02dZ", i/60, i%60)
		status := "pending"
		if i%2 == 0 {
			status = "completed"
		}
		if _, err := db.Exec(`INSERT INTO action_items(id,user_id,item_type,item_id,title,status,created_at) VALUES(?,?,'note','full',?,?,?)`, fmt.Sprintf("a-%03d", i), "owner", fmt.Sprintf("action %d", i), status, stamp); err != nil {
			t.Fatal(err)
		}
		if i < 51 {
			if _, err := db.Exec(`INSERT INTO reminders(id,user_id,item_type,item_id,due_at,timezone,recurrence,recurrence_interval_days,notification_channel,note,status,created_at) VALUES(?,?,'note','full',?,'UTC','none',0,'in_app','','pending','2026-01-01')`, fmt.Sprintf("r-%03d", i), "owner", stamp); err != nil {
				t.Fatal(err)
			}
		}
		for _, direction := range []string{"out", "in"} {
			fromType, fromID, toType, toID := "note", "full", "bookmark", "owner-b1"
			if direction == "in" {
				fromType, fromID, toType, toID = "bookmark", "owner-b2", "note", "full"
			}
			if _, err := db.Exec(`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("l-%s-%03d", direction, i), "owner", fromType, fromID, toType, toID, fmt.Sprintf("label-%03d", i), "manual", stamp); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A repeated list row (one note linked to two bookmarks) must receive the
	// same decoration without collapsing either top-level row.
	batch := []map[string]any{
		{"id": "empty", "title": ""},
		{"id": "full", "title": "Full", "bookmark_id": "owner-b1"},
		{"id": "full", "title": "Full", "bookmark_id": "owner-b2"},
	}
	// Foreign rows deliberately target the owner's note ID.
	if _, err := db.Exec(`INSERT INTO action_items(id,user_id,item_type,item_id,title,status,created_at) VALUES('foreign','other','note','full','foreign','pending','2030-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES('foreign-link','other','note','full','bookmark','other-b1','foreign','manual','2030-01-01')`); err != nil {
		t.Fatal(err)
	}
	service.decorateNotes(t.Context(), "owner", batch)
	for i, source := range batch {
		want := map[string]any{"id": source["id"], "title": source["title"]}
		if bookmarkID, ok := source["bookmark_id"]; ok {
			want["bookmark_id"] = bookmarkID
		}
		service.decorateNote(t.Context(), "owner", want)
		if !reflect.DeepEqual(source, want) {
			t.Fatalf("row %d batch differs from singleton\nbatch=%#v\nsingle=%#v", i, source, want)
		}
	}
}

func BenchmarkDecorateNotes200(b *testing.B) {
	for _, density := range []int{0, 100} {
		b.Run(map[int]string{0: "sparse", 100: "dense"}[density], func(b *testing.B) {
			db, err := database.Open(context.Background(), filepath.Join(b.TempDir(), "arivu.sqlite3"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { db.Close() })
			service := New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{})
			if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('owner','owner@example.com','Owner','2026-01-01','2026-01-01')`); err != nil {
				b.Fatal(err)
			}
			notes := make([]map[string]any, 200)
			for i := range notes {
				id := fmt.Sprintf("n-%03d", i)
				if _, err := db.Exec(`INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,'owner',?,'','manual','2026-01-01','2026-01-01')`, id, id); err != nil {
					b.Fatal(err)
				}
				notes[i] = map[string]any{"id": id, "title": id}
				for j := 0; j < density; j++ {
					if _, err := db.Exec(`INSERT INTO action_items(id,user_id,item_type,item_id,title,status,created_at) VALUES(?,'owner','note',?,?,'pending',?)`, fmt.Sprintf("a-%03d-%03d", i, j), id, "action", fmt.Sprintf("2026-01-01T00:%02d:%02dZ", j/60, j%60)); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.Run("singleton", func(b *testing.B) {
				b.ReportAllocs()
				for n := 0; n < b.N; n++ {
					for _, note := range notes {
						service.decorateNote(context.Background(), "owner", map[string]any{"id": note["id"], "title": note["title"]})
					}
				}
			})
			b.Run("batch", func(b *testing.B) {
				b.ReportAllocs()
				for n := 0; n < b.N; n++ {
					copies := make([]map[string]any, len(notes))
					for i, note := range notes {
						copies[i] = map[string]any{"id": note["id"], "title": note["title"]}
					}
					service.decorateNotes(context.Background(), "owner", copies)
				}
			})
		})
	}
}
