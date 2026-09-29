package bookmarks

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
)

func TestGraphV2EdgesSelectedRelationshipParity(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedKnowledgeUser(t, db, "other", "other@example.com")
	for _, id := range []string{"b1", "b2", "irrelevant"} {
		seedKnowledgeBookmark(t, db, "owner", id, id, "2026-01-01T00:00:00Z")
	}
	seedKnowledgeBookmark(t, db, "other", "foreign", "foreign", "2026-01-01T00:00:00Z")
	seedInsightConcept(t, db, "owner", "b1", "Selected concept")
	for _, statement := range []string{
		`UPDATE bookmark_evidence SET content_text='Selected concept Selected entity' WHERE id='evidence-owner-b1'`,
		`INSERT INTO bookmark_entities(bookmark_id,user_id,entity,confidence,evidence_id,evidence_text,evidence_start,evidence_end,enrichment_version) SELECT 'b1','owner','Selected entity',0.9,evidence_id,'Selected entity',17,32,enrichment_version FROM bookmark_concepts WHERE bookmark_id='b1' LIMIT 1`,
		`INSERT INTO notes(id,user_id,title,created_at,updated_at) VALUES('n1','owner','n1','2026-01-01','2026-01-01')`,
		`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,source,created_at) VALUES('early','owner','bookmark','irrelevant','note','n1','manual','2025-01-01')`,
		`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,source,created_at) VALUES('selected','owner','bookmark','b1','bookmark','b2','import','2026-01-01')`,
		`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,source,created_at) VALUES('foreign-link','other','bookmark','foreign','bookmark','b1','manual','2024-01-01')`,
		`INSERT INTO bookmark_notes(bookmark_id,note_id,user_id,created_at) VALUES('b1','n1','owner','2026-01-01')`,
		`INSERT INTO knowledge_objects(id,user_id,object_type,source_item_type,source_item_id,created_at,updated_at) VALUES('ko1','owner','test','object','ko2','2026-01-01','2026-01-01'),('ko2','owner','test','odd','x','2026-01-01','2026-01-01')`,
		`INSERT INTO annotations(id,user_id,bookmark_id,created_at,updated_at) VALUES('a1','owner','b1','2026-01-01','2026-01-01')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	nodes := []graphV2Node{
		{ID: "bookmark:b1", Type: "bookmark", SourceID: "b1"}, {ID: "bookmark:b2", Type: "bookmark", SourceID: "b2"},
		{ID: "note:n1", Type: "note", SourceID: "n1"}, {ID: "concept:Selected concept", Type: "concept", SourceID: "Selected concept"},
		{ID: "entity:Selected entity", Type: "entity", SourceID: "Selected entity"}, {ID: "knowledge_object:ko1", Type: "knowledge_object", SourceID: "ko1"},
		{ID: "knowledge_object:ko2", Type: "knowledge_object", SourceID: "ko2"}, {ID: "odd:x", Type: "odd", SourceID: "x"},
		{ID: "annotation:a1", Type: "annotation", SourceID: "a1"},
		{ID: "bookmark:foreign", Type: "bookmark", SourceID: "foreign"},
	}
	hidden := newGraphV2Edge("explicit", "bookmark:b1", "bookmark:b2", "import", 1)
	_, err := db.Exec(`INSERT INTO knowledge_feedback(user_id,target_type,target_id,feedback,created_at,updated_at) VALUES('owner','relationship',?,'dismiss','2026-01-01','2026-01-01')`, hidden.ID)
	if err != nil {
		t.Fatal(err)
	}

	got := service.graphV2Edges(context.Background(), "owner", nodes, 20, true)
	want := []graphV2Edge{
		newGraphV2Edge("explicit", "bookmark:b1", "note:n1", "bookmark_notes", 1),
		newGraphV2Edge("shared_concept", "bookmark:b1", "concept:Selected concept", "bookmark_concepts", .9),
		newGraphV2Edge("shared_entity", "bookmark:b1", "entity:Selected entity", "bookmark_entities", .9),
		newGraphV2Edge("source", "knowledge_object:ko1", "knowledge_object:ko2", "knowledge_objects.source_item_id", 1),
		newGraphV2Edge("source", "knowledge_object:ko2", "odd:x", "knowledge_objects.source_item_id", 1),
		newGraphV2Edge("source", "annotation:a1", "bookmark:b1", "annotations.bookmark_id", 1),
	}
	sort.Slice(want, func(i, j int) bool { return want[i].ID < want[j].ID })
	if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", want) {
		t.Fatalf("edges differ\n got: %#v\nwant: %#v", got, want)
	}
	// Family precedence remains exact: after the dismissed early explicit edge,
	// bookmark_notes and then concepts consume the small budget before entities.
	limited := service.graphV2Edges(context.Background(), "owner", nodes, 2, true)
	limitedWant := wantByType(want, "explicit", "shared_concept")
	if fmt.Sprintf("%#v", limited) != fmt.Sprintf("%#v", limitedWant) {
		t.Fatalf("family cap changed: got %#v want %#v", limited, limitedWant)
	}
}

func wantByType(edges []graphV2Edge, types ...string) []graphV2Edge {
	want := []graphV2Edge{}
	for _, edge := range edges {
		for _, edgeType := range types {
			if edge.Type == edgeType {
				want = append(want, edge)
			}
		}
	}
	return want
}

func BenchmarkGraphV2EdgesRelationshipHeavyLargeCorpusSmallSelection(b *testing.B) {
	db, err := database.Open(context.Background(), filepath.Join(b.TempDir(), "arivu.sqlite3"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	service := New(db, jobs.New(db), nil, providers.GeminiClient{})
	if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES('owner','owner@example.com','owner','2026-01-01','2026-01-01')`); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 5000; i++ {
		bookmarkID, noteID := fmt.Sprintf("b%05d", i), fmt.Sprintf("n%05d", i)
		if _, err := db.Exec(`INSERT INTO bookmarks(id,user_id,url,title,created_at,updated_at) VALUES(?,?,?,?,'2026-01-01','2026-01-01')`, bookmarkID, "owner", "https://example.com/"+bookmarkID, bookmarkID); err != nil {
			b.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO notes(id,user_id,title,created_at,updated_at) VALUES(?,?,?,'2026-01-01','2026-01-01')`, noteID, "owner", noteID); err != nil {
			b.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO bookmark_notes(bookmark_id,note_id,user_id,created_at) VALUES(?,?,?,'2026-01-01')`, bookmarkID, noteID, "owner"); err != nil {
			b.Fatal(err)
		}
	}
	nodes := []graphV2Node{{ID: "bookmark:b04999", Type: "bookmark", SourceID: "b04999"}, {ID: "note:n04999", Type: "note", SourceID: "n04999"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if edges := service.graphV2Edges(context.Background(), "owner", nodes, 10, true); len(edges) != 1 {
			b.Fatalf("got %d edges", len(edges))
		}
	}
}
