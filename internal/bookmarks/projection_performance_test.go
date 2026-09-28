package bookmarks

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/providers"
)

func projectionFixture(t testing.TB, size int) *Service {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "projection.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	queries := []string{
		`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('owner','owner@example.test','Owner','2026-01-01','2026-01-01'),('other','other@example.test','Other','2026-01-01','2026-01-01')`,
		`INSERT INTO tags(id,user_id,name,slug,created_at,updated_at) VALUES('tag','owner','Research','research','2026-01-01','2026-01-01')`,
		`INSERT INTO bookmarks(id,user_id,url,title,created_at,updated_at) VALUES('foreign','other','https://other.example','Private','2026-01-01','2026-01-01')`,
	}
	for _, query := range queries {
		if _, err := tx.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	queries = []string{
		`INSERT INTO bookmarks(id,user_id,url,title,description,domain,text_content,created_at,updated_at) VALUES(?1,'owner','https://example.test/'||?1,'Title '||?1,'Description','example.test','Saved text','2026-01-01',?1)`,
		`INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES('n-'||?1,'owner','Note '||?1,'Note body','manual','2026-01-01',?1)`,
		`INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,processing_status,created_at,updated_at) VALUES('s-'||?1,?1,'owner','Summary','completed','2026-01-01',?1)`,
		`INSERT INTO bookmark_tags(user_id,bookmark_id,tag_id,source,created_at) VALUES('owner',?1,'tag','manual','2026-01-01')`,
		`INSERT INTO bookmark_notes(user_id,bookmark_id,note_id,created_at) VALUES('owner',?1,'n-'||?1,'2026-01-01')`,
		`INSERT INTO annotations(id,user_id,bookmark_id,quote,note,created_at,updated_at) VALUES('a-'||?1,'owner',?1,'Quote','Annotation','2026-01-01',?1)`,
		`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,created_at) VALUES('l-'||?1,'owner','bookmark',?1,'note','n-'||?1,'Supports','2026-01-01')`,
	}
	for _, query := range queries {
		stmt, err := tx.Prepare(query)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < size; i++ {
			if _, err := stmt.Exec(fmt.Sprintf("b-%05d", i)); err != nil {
				t.Fatal(err)
			}
		}
		stmt.Close()
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return New(db, nil, nil, providers.GeminiClient{})
}

func BenchmarkSearchRebuild(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			service := projectionFixture(b, size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if count, err := service.rebuildSearchIndex(b.Context(), "owner"); err != nil || count != 2*size {
					b.Fatalf("rebuild count=%d error=%v", count, err)
				}
			}
		})
	}
}

func BenchmarkFullExport(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			service := projectionFixture(b, size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				payload, err := service.fullExport(b.Context(), "owner")
				if err != nil || len(payload["bookmarks"].([]map[string]any)) != size {
					b.Fatalf("export failed: %v", err)
				}
			}
		})
	}
}

func TestSearchProjectionKeepsRelatedTextAndOwnership(t *testing.T) {
	service := projectionFixture(t, 2)
	if count, err := service.rebuildSearchIndex(t.Context(), "owner"); err != nil || count != 4 {
		t.Fatalf("rebuild count=%d error=%v", count, err)
	}
	var title, body, tags, links string
	err := service.db.QueryRow(`SELECT title,body,tags,links FROM search_index WHERE user_id='owner' AND item_type='bookmark' AND item_id='b-00000'`).Scan(&title, &body, &tags, &links)
	if err != nil {
		t.Fatal(err)
	}
	wantBody := "https://example.test/b-00000 example.test Description Saved text Summary []  [] [] Quote Annotation {} [] Note b-00000 Note body manual"
	wantLinks := "bookmark b-00000 note n-b-00000 Supports manual Title b-00000 Note b-00000"
	if title != "Title b-00000" || body != wantBody || tags != "Research" || links != wantLinks {
		t.Fatalf("projection = %q / %q / %q / %q", title, body, tags, links)
	}
	if err := service.db.QueryRow(`SELECT links FROM search_index WHERE user_id='owner' AND item_type='note' AND item_id='n-b-00000'`).Scan(&links); err != nil || links != wantLinks {
		t.Fatalf("incoming note links=%q error=%v", links, err)
	}
}

func TestProjectionAndExportKeepPerBookmarkLimits(t *testing.T) {
	service := projectionFixture(t, 202)
	for i := 1; i < 202; i++ {
		id := fmt.Sprintf("b-%05d", i)
		for _, query := range []string{
			`INSERT INTO bookmark_notes(user_id,bookmark_id,note_id,created_at) VALUES('owner','b-00000','n-'||?1,?1)`,
			`INSERT INTO annotations(id,user_id,bookmark_id,quote,created_at,updated_at) VALUES('extra-'||?1,'owner','b-00000','Quote-'||?1,?1,?1)`,
			`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,created_at) VALUES('extra-'||?1,'owner','bookmark','b-00000','note','n-'||?1,'Link-'||?1,?1)`,
		} {
			if _, err := service.db.Exec(query, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := service.db.Exec(`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,created_at) VALUES('self','owner','bookmark','b-00000','bookmark','b-00000','Self link','z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.rebuildSearchIndex(t.Context(), "owner"); err != nil {
		t.Fatal(err)
	}
	var body, links string
	if err := service.db.QueryRow(`SELECT body,links FROM search_index WHERE user_id='owner' AND item_type='bookmark' AND item_id='b-00000'`).Scan(&body, &links); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Quote-b-00102") || strings.Contains(body, "Quote-b-00101") || !strings.Contains(body, "Note b-00102") || strings.Contains(body, "Note b-00101") {
		t.Fatal("search lost the newest-100 annotation/note boundary")
	}
	if strings.Count(links, "Self link") != 1 || !strings.Contains(links, "Link-b-00003") || strings.Contains(links, "Link-b-00002") {
		t.Fatal("search lost the 200-link boundary or duplicated a self link")
	}
	payload, err := service.fullExport(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range payload["bookmarks"].([]map[string]any) {
		if bookmark["id"] == "foreign" {
			t.Fatal("export contains another owner's bookmark")
		}
		if bookmark["id"] != "b-00000" {
			continue
		}
		annotations := bookmark["annotations"].([]map[string]any)
		notes := bookmark["notes"].([]map[string]any)
		if len(annotations) != 100 || annotations[0]["id"] != "extra-b-00201" || annotations[99]["id"] != "extra-b-00102" || annotations[0]["resolution_state"] != "unresolved" {
			t.Fatalf("annotation limit/order/resolution changed: %#v", annotations)
		}
		if len(notes) != 100 || notes[0]["id"] != "n-b-00201" || notes[99]["id"] != "n-b-00102" || notes[0]["bookmark_id"] != "b-00000" {
			t.Fatalf("note limit/order/ownership changed: %#v", notes)
		}
	}
}

func TestExportRejectsPartialEvidenceOnScanError(t *testing.T) {
	service := projectionFixture(t, 2)
	for _, id := range []string{"b-00000", "b-00001"} {
		for _, suffix := range []string{"valid", "invalid"} {
			_, err := service.UpsertEvidence(t.Context(), "owner", id, BookmarkEvidence{ID: id + suffix, Kind: "article", Origin: "direct", Text: id + suffix, QualityStatus: "complete"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// Simulate an unreadable row in a damaged database.
	if _, err := service.db.Exec(`PRAGMA ignore_check_constraints=ON; UPDATE bookmark_evidence SET quality_score='invalid' WHERE id='b-00000invalid'; PRAGMA ignore_check_constraints=OFF;`); err != nil {
		t.Fatal(err)
	}
	payload, err := service.fullExport(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range payload["bookmarks"].([]map[string]any) {
		want := 2
		if bookmark["id"] == "b-00000" {
			want = 0
		}
		if got := len(bookmark["evidence"].([]map[string]any)); got != want {
			t.Errorf("%s: evidence count=%d want=%d", bookmark["id"], got, want)
		}
	}
}

func TestExportKeepsEmptyShapesAndEvidenceResolution(t *testing.T) {
	service := projectionFixture(t, 2)
	for _, query := range []string{
		`DELETE FROM ai_summaries WHERE bookmark_id='b-00001'`,
		`DELETE FROM bookmark_tags WHERE bookmark_id='b-00001'`,
		`DELETE FROM bookmark_notes WHERE bookmark_id='b-00001'`,
		`DELETE FROM annotations WHERE bookmark_id='b-00001'`,
	} {
		if _, err := service.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	_, err := service.UpsertEvidence(t.Context(), "owner", "b-00000", BookmarkEvidence{ID: "e-active", Kind: "article", Origin: "direct", Text: "Evidence", QualityStatus: "complete", Selected: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`UPDATE annotations SET evidence_id='e-active',selector_json='{"exact":"Quote"}',tags_json='["tag"]' WHERE id='a-b-00000'`); err != nil {
		t.Fatal(err)
	}
	payload, err := service.fullExport(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range payload["bookmarks"].([]map[string]any) {
		if bookmark["id"] == "b-00001" {
			for field, want := range map[string]string{"tags": "null", "notes": "null", "annotations": "null", "evidence": "[]", "ai_summary": `{"processing_status":"pending"}`} {
				got, err := json.Marshal(bookmark[field])
				if err != nil || string(got) != want {
					t.Errorf("%s = %s, want %s (error=%v)", field, got, want, err)
				}
			}
		} else if bookmark["id"] == "b-00000" {
			annotation := bookmark["annotations"].([]map[string]any)[0]
			if annotation["resolution_state"] != "resolved" || annotation["selector"].(map[string]any)["exact"] != "Quote" || annotation["tags"].([]any)[0] != "tag" {
				t.Fatalf("annotation JSON changed: %#v", annotation)
			}
			evidence := bookmark["evidence"].([]map[string]any)
			if len(evidence) != 1 || evidence[0]["id"] != "e-active" || evidence[0]["selected"] != true || evidence[0]["published_at"] != nil {
				t.Fatalf("evidence JSON changed: %#v", evidence)
			}
		}
	}
}
