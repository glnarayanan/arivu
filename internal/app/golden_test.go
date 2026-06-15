package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/config"
)

func TestGoldenParityFixtures(t *testing.T) {
	a, err := New(config.Config{
		DBPath:         filepath.Join(t.TempDir(), "arivu.sqlite3"),
		SecretKey:      "test-secret",
		SignupEnabled:  true,
		SessionTTL:     time.Hour,
		RefreshTTL:     time.Hour,
		ExtensionTTL:   time.Hour,
		MaxRequestBody: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	handler := a.Handler()
	accessCookie, csrfCookie := signupForCookies(t, handler, "golden@example.com")
	userID := userIDForEmail(t, a, "golden@example.com")
	seedGoldenParityData(t, a, userID)

	assertGolden(t, "api_search_ids.json", collectIDs(requestJSON(t, handler, http.MethodGet, "/api/search?q=vector", "", accessCookie, csrfCookie)["items"]))
	assertGolden(t, "analytics_summary.json", requestJSON(t, handler, http.MethodGet, "/api/analytics/summary", "", accessCookie, csrfCookie))
	assertGolden(t, "analytics_insight_types.json", normalizeInsights(requestJSON(t, handler, http.MethodGet, "/api/analytics/insights", "", accessCookie, csrfCookie)))
	assertGolden(t, "duplicate_groups.json", normalizeDuplicates(requestJSON(t, handler, http.MethodGet, "/api/bookmarks/duplicates/detect", "", accessCookie, csrfCookie)))
	assertGolden(t, "related_ids.json", collectIDs(requestJSON(t, handler, http.MethodGet, "/api/bookmarks/vector/related?limit=3", "", accessCookie, csrfCookie)["related"]))
	assertGolden(t, "graph_summary.json", normalizeGraph(requestJSON(t, handler, http.MethodGet, "/api/knowledge-graph/explore?limit=10", "", accessCookie, csrfCookie)))
	assertGolden(t, "resurfacing_ids.json", collectIDs(requestJSON(t, handler, http.MethodGet, "/api/resurfacing?limit=3", "", accessCookie, csrfCookie)["suggestions"]))
	assertGolden(t, "memory_jogger.json", normalizeMemoryJogger(requestJSON(t, handler, http.MethodGet, "/api/memory-jogger", "", accessCookie, csrfCookie)))
}

func seedGoldenParityData(t *testing.T, a *App, userID string) {
	t.Helper()
	now := time.Now().UTC()
	insertBookmarkForTest(t, a, userID, "vector", "SQLite Vector Search", now.AddDate(0, 0, -14), now.AddDate(0, 0, -7), 3, 4)
	insertBookmarkForTest(t, a, userID, "embedding", "Embedding Index Design", now.AddDate(0, 0, -13), now.AddDate(0, 0, -3), 1, 5)
	insertBookmarkForTest(t, a, userID, "recipe", "Recipe Timing", now.AddDate(0, 0, -45), now.AddDate(0, 0, -40), 0, 8)
	insertBookmarkForTest(t, a, userID, "backlog", "Unread Backlog", now.AddDate(0, 0, -5), now.AddDate(0, 0, -5), 0, 2)
	updates := []struct {
		ID          string
		URL         string
		Description string
		Text        string
		Embedding   string
		Read        bool
	}{
		{"vector", "https://example.com/vector?utm_source=golden", "Vector database ranking", "SQLite embeddings and vector search", `[1,0]`, false},
		{"embedding", "https://example.com/vector#section", "ANN index ranking", "Embedding search and vector graph retrieval", `[0.97,0.03]`, false},
		{"recipe", "https://cooking.example/recipe", "Kitchen reference", "Recipe timing and ingredients", `[0,1]`, true},
		{"backlog", "https://example.com/backlog", "Later reading", "Queued reading list", `[0.2,-0.98]`, false},
	}
	for _, update := range updates {
		_, err := a.db.ExecContext(context.Background(), `UPDATE bookmarks SET url=?,description=?,text_content=?,embedding=?,embedding_dim=2,embedding_model='test',read_status=? WHERE id=?`, update.URL, update.Description, update.Text, []byte(update.Embedding), update.Read, update.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		BookmarkID string
		Entity     string
		Concept    string
	}{
		{"vector", "SQLite", "Vector Search"},
		{"vector", "Embeddings", "Knowledge Graph"},
		{"embedding", "Embeddings", "Vector Search"},
		{"recipe", "Cooking", "Recipes"},
		{"backlog", "Reading", "Backlog"},
	} {
		_, _ = a.db.ExecContext(context.Background(), `INSERT INTO bookmark_entities(bookmark_id,user_id,entity) VALUES(?,?,?)`, row.BookmarkID, userID, row.Entity)
		_, _ = a.db.ExecContext(context.Background(), `INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES(?,?,?)`, row.BookmarkID, userID, row.Concept)
	}
	_, _ = a.db.ExecContext(context.Background(), `INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,suggested_tags_json,processing_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, "summary-vector", "vector", userID, "Worth revisiting.", `["vector"]`, "completed", now.Format(time.RFC3339), now.Format(time.RFC3339))
}

func requestJSON(t *testing.T, handler http.Handler, method string, path string, body string, accessCookie, csrfCookie *http.Cookie) map[string]any {
	t.Helper()
	resp := adminRequest(t, handler, method, path, body, accessCookie, csrfCookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s status = %d body=%s", method, path, resp.StatusCode, readBody(resp))
	}
	var value any
	if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{"items": value}
}

func collectIDs(value any) []string {
	items, _ := value.([]any)
	ids := []string{}
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			if id, _ := object["id"].(string); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func normalizeInsights(value map[string]any) []map[string]any {
	items, _ := value["insights"].([]any)
	result := []map[string]any{}
	for _, item := range items {
		object, _ := item.(map[string]any)
		result = append(result, map[string]any{"type": object["type"], "severity": object["severity"]})
	}
	return result
}

func normalizeDuplicates(value map[string]any) []map[string]any {
	items, _ := value["duplicates"].([]any)
	result := []map[string]any{}
	for _, item := range items {
		object, _ := item.(map[string]any)
		ids := collectIDs(object["bookmarks"])
		sort.Strings(ids)
		result = append(result, map[string]any{"type": object["type"], "ids": ids})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i]["type"] == result[j]["type"] {
			return stringsForGolden(result[i]["ids"]) < stringsForGolden(result[j]["ids"])
		}
		return result[i]["type"].(string) < result[j]["type"].(string)
	})
	return result
}

func normalizeGraph(value map[string]any) map[string]any {
	related, _ := value["related_bookmarks"].(map[string]any)
	vectorRelated := []string{}
	if raw, ok := related["vector"].([]any); ok {
		for _, item := range raw {
			pair, _ := item.([]any)
			if len(pair) > 0 {
				id, _ := pair[0].(string)
				vectorRelated = append(vectorRelated, id)
			}
		}
	}
	return map[string]any{
		"entities":        value["entities"],
		"concepts":        value["concepts"],
		"total_bookmarks": value["total_bookmarks"],
		"total_entities":  value["total_entities"],
		"total_concepts":  value["total_concepts"],
		"vector_related":  vectorRelated,
	}
}

func normalizeMemoryJogger(value map[string]any) map[string]any {
	bookmark, _ := value["bookmark"].(map[string]any)
	return map[string]any{"has_memory": value["has_memory"], "bookmark_id": bookmark["id"]}
}

func assertGolden(t *testing.T, name string, value any) {
	t.Helper()
	actual, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')
	path := filepath.Join("testdata", "golden", name)
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("golden %s mismatch\nactual:\n%s\nexpected:\n%s", name, actual, expected)
	}
}

func stringsForGolden(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
