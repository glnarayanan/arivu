package bookmarks

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/providers"
)

func searchFeedbackTestService(t testing.TB) *Service {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "search-feedback.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES
		('owner','owner@example.test','Owner','2026-01-01','2026-01-01'),
		('other','other@example.test','Other','2026-01-01','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	return New(db, nil, nil, providers.GeminiClient{})
}

func searchFeedbackResults(size int) []map[string]any {
	results := make([]map[string]any, size)
	for i := range results {
		results[i] = map[string]any{
			"item_type":  "bookmark",
			"item_id":    fmt.Sprintf("item-%02d", i),
			"updated_at": "not-a-date",
		}
	}
	return results
}

func cloneSearchFeedbackResults(results []map[string]any) []map[string]any {
	cloned := make([]map[string]any, len(results))
	for i, result := range results {
		cloned[i] = make(map[string]any, len(result))
		for key, value := range result {
			cloned[i][key] = value
		}
	}
	return cloned
}

func decorateSearchResultsSingleton(ctx context.Context, service *Service, results []map[string]any) {
	for index, result := range results {
		feedback := service.feedbackState(ctx, "owner", stringValue(result["item_type"]), stringValue(result["item_id"]), "search")
		freshness := freshnessScore(stringValue(result["updated_at"]))
		result["freshness_score"] = freshness
		result["feedback_state"] = feedback
		result["result_score"] = roundFloat(100-float64(index*2)+freshness+feedbackSearchWeight(feedback), 2)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return numberValue(results[i]["result_score"]) > numberValue(results[j]["result_score"])
	})
}

func TestDecorateSearchResultsBatchesExactFeedbackPairsAndPreservesScoring(t *testing.T) {
	service := searchFeedbackTestService(t)
	results := searchFeedbackResults(11)
	results[0]["item_id"] = "shared"
	results[1]["item_type"], results[1]["item_id"] = "note", "shared"
	for _, row := range []struct{ user, itemType, itemID, surface, feedback string }{
		{"owner", "bookmark", "shared", "search", "not_useful"},
		{"owner", "note", "shared", "search", "useful"},
		{"owner", "bookmark", "item-02", "review", "useful"},
		{"other", "bookmark", "item-03", "search", "useful"},
	} {
		if _, err := service.db.Exec(`INSERT INTO result_feedback(user_id,item_type,item_id,surface,feedback,created_at,updated_at) VALUES(?,?,?,?,?,'2026-01-01','2026-01-01')`, row.user, row.itemType, row.itemID, row.surface, row.feedback); err != nil {
			t.Fatal(err)
		}
	}

	got := service.decorateSearchResults(t.Context(), "owner", results, "invalid-normalizes-to-search")
	byPair := make(map[feedbackItem]map[string]any, len(got))
	for _, result := range got {
		byPair[feedbackItem{stringValue(result["item_type"]), stringValue(result["item_id"])}] = result
	}
	checks := []struct {
		item     feedbackItem
		feedback string
		score    float64
	}{
		{feedbackItem{"bookmark", "shared"}, "not_useful", 80},
		{feedbackItem{"note", "shared"}, "useful", 113},
		{feedbackItem{"bookmark", "item-02"}, "", 96},
		{feedbackItem{"bookmark", "item-03"}, "", 94},
		{feedbackItem{"bookmark", "item-10"}, "", 80},
	}
	for _, check := range checks {
		result := byPair[check.item]
		if result["feedback_state"] != check.feedback || numberValue(result["result_score"]) != check.score || numberValue(result["freshness_score"]) != 0 {
			t.Errorf("%+v: feedback=%q score=%v freshness=%v", check.item, result["feedback_state"], result["result_score"], result["freshness_score"])
		}
	}
	// The first and last candidates tie at 80; stable sorting must retain their candidate order.
	positions := map[feedbackItem]int{}
	for i, result := range got {
		positions[feedbackItem{stringValue(result["item_type"]), stringValue(result["item_id"])}] = i
	}
	if positions[feedbackItem{"bookmark", "shared"}] >= positions[feedbackItem{"bookmark", "item-10"}] {
		t.Fatalf("stable tie order changed: shared=%d item-10=%d", positions[feedbackItem{"bookmark", "shared"}], positions[feedbackItem{"bookmark", "item-10"}])
	}
}

func BenchmarkDecorateSearchResultsFeedback(b *testing.B) {
	service := searchFeedbackTestService(b)
	for i := 0; i < 1000; i++ {
		if _, err := service.db.Exec(`INSERT INTO result_feedback(user_id,item_type,item_id,surface,feedback,created_at,updated_at) VALUES('owner','bookmark',?,'search','useful','2026-01-01','2026-01-01')`, fmt.Sprintf("item-%02d", i)); err != nil {
			b.Fatal(err)
		}
	}
	for _, size := range []int{20, 50} {
		seed := searchFeedbackResults(size)
		b.Run(fmt.Sprintf("%d/singleton", size), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				decorateSearchResultsSingleton(b.Context(), service, cloneSearchFeedbackResults(seed))
			}
		})
		b.Run(fmt.Sprintf("%d/batch", size), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				service.decorateSearchResults(b.Context(), "owner", cloneSearchFeedbackResults(seed), "search")
			}
		})
	}
}
