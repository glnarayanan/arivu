package bookmarks

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
)

func BenchmarkGraphV2Edges10KCorpus(b *testing.B) {
	db, err := database.Open(context.Background(), filepath.Join(b.TempDir(), "arivu.sqlite3"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	service := New(db, jobs.New(db), nil, providers.GeminiClient{})
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,created_at,updated_at) VALUES('owner','owner@example.com','x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		b.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	bookmark, err := tx.Prepare(`INSERT INTO bookmarks(id,user_id,url,title,embedding,embedding_dim,enrichment_version,created_at,updated_at) VALUES(?,?,?,?,?,3,?,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	evidence, err := tx.Prepare(`INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,authority,content_text,content_hash,quality_status,is_selected,created_at,updated_at) VALUES(?,?,?,'article','web',1,'text',?,'complete',1,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	stamp := "2026-01-01T00:00:00Z"
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("b-%05d", i)
		if _, err := bookmark.Exec(id, "owner", "https://example.com/"+id, id, `[1,0.5,0.25]`, providers.SemanticVersion, stamp, stamp); err != nil {
			b.Fatal(err)
		}
		if _, err := evidence.Exec("e-"+id, id, "owner", id, stamp, stamp); err != nil {
			b.Fatal(err)
		}
	}
	bookmark.Close()
	evidence.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	nodes := make([]graphV2Node, 80)
	for i := range nodes {
		nodes[i] = graphV2Node{ID: graphNodeID("bookmark", fmt.Sprintf("b-%05d", i)), Type: "bookmark", SourceID: fmt.Sprintf("b-%05d", i)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if edges := service.graphV2Edges(context.Background(), "owner", nodes, 500, true); len(edges) != 500 {
			b.Fatalf("got %d edges", len(edges))
		}
	}
}

func TestCosineNormReuseIsExactlyEquivalent(t *testing.T) {
	vectors := [][]float64{nil, {}, {0, 0}, {1}, {1, -2, 3}, {math.SmallestNonzeroFloat64, 1}, {math.Inf(1), 1}, {math.NaN(), 1}}
	for _, left := range vectors {
		for _, right := range vectors {
			want := cosineSimilarity(left, right)
			got := cosineSimilarityWithNorms(left, right, embeddingNorm(left), embeddingNorm(right))
			if !(want == got || math.IsNaN(want) && math.IsNaN(got)) {
				t.Fatalf("left=%v right=%v: got %v want %v", left, right, got, want)
			}
		}
	}
	nearThreshold := []float64{0.82, math.Sqrt(1 - 0.82*0.82)}
	if got := cosineSimilarityWithNorms([]float64{1, 0}, nearThreshold, 1, embeddingNorm(nearThreshold)); got != cosineSimilarity([]float64{1, 0}, nearThreshold) || got < 0.82 {
		t.Fatalf("threshold changed: %v", got)
	}
}

func TestInsightConceptSourcesAreOwnerIsolatedAndOrdered(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedKnowledgeUser(t, db, "other", "other@example.com")
	for _, fixture := range []struct{ user, id, published string }{
		{"owner", "older", "2026-01-01T00:00:00Z"},
		{"owner", "newer", "2026-02-01T00:00:00Z"},
		{"other", "foreign", "2026-03-01T00:00:00Z"},
	} {
		seedKnowledgeBookmark(t, db, fixture.user, fixture.id, fixture.id, fixture.published)
		seedInsightConcept(t, db, fixture.user, fixture.id, "Exact arithmetic")
	}
	concepts, sources := service.insightConceptSources(context.Background(), "owner")
	if len(concepts) != 1 || concepts[0] != "Exact arithmetic" {
		t.Fatalf("unexpected concepts: %v", concepts)
	}
	got := sources[concepts[0]]
	if len(got) != 2 || got[0].id != "newer" || got[1].id != "older" {
		t.Fatalf("sources were reordered or crossed owners: %#v", got)
	}
}
