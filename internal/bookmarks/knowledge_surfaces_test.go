package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/database"
	"github.com/glnarayanan/arivu/internal/jobs"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

func TestLibraryItemsAreUserScopedStableAndCursorBounded(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeUser(t, db, "u2", "two@example.com")
	seedKnowledgeBookmark(t, db, "u1", "b-new", "New", "2026-07-10T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "b-old", "Old", "2026-07-09T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u2", "foreign", "Foreign", "2026-07-11T00:00:00Z")

	first := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?type=bookmark&limit=1", "")
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "b-new" {
		t.Fatalf("unexpected first page: %#v", first)
	}
	cursor, _ := first["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("missing next cursor: %#v", first)
	}
	second := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?type=bookmark&limit=1&cursor="+cursor, "")
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "b-old" {
		t.Fatalf("unexpected second page: %#v", second)
	}
}

func TestKnowledgeGraphV2KeepsOldFocusAndBoundsPayload(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	for i, id := range []string{"old", "new-1", "new-2", "new-3"} {
		seedKnowledgeBookmark(t, db, "u1", id, id, time.Date(2020+i, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339))
	}
	_, _ = db.Exec(`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES('link','u1','bookmark','old','bookmark','new-1','','manual','2026-01-01T00:00:00Z')`)

	payload := callKnowledgeHandler(t, service.KnowledgeGraphV2, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/v2?focus=bookmark:old&depth=1&node_limit=2&edge_limit=1", "")
	nodes := payload["nodes"].([]any)
	if len(nodes) > 2 {
		t.Fatalf("node bound exceeded: %#v", payload)
	}
	found := false
	for _, raw := range nodes {
		found = found || raw.(map[string]any)["id"] == "bookmark:old"
	}
	if !found {
		t.Fatalf("old focused node disappeared: %#v", payload)
	}
	if len(payload["edges"].([]any)) > 1 {
		t.Fatalf("edge bound exceeded: %#v", payload)
	}
	edges := payload["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("expected focused relationship: %#v", payload)
	}
	edgeID := edges[0].(map[string]any)["id"].(string)
	edge := edges[0].(map[string]any)
	callKnowledgeHandler(t, service.SaveFeedback, auth.User{ID: "u1"}, http.MethodPost, "/api/feedback", `{"target_type":"relationship","target_id":"`+edgeID+`","action":"dismiss","from":"`+edge["from"].(string)+`","to":"`+edge["to"].(string)+`"}`)
	hidden := callKnowledgeHandler(t, service.KnowledgeGraphV2, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/v2?focus=bookmark:old&depth=1&node_limit=2&edge_limit=1", "")
	if len(hidden["edges"].([]any)) != 0 {
		t.Fatalf("dismissed relationship still returned: %#v", hidden)
	}
}

func TestInsightsNeedOwnedEvidenceAndFeedbackHidesDeterministically(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeUser(t, db, "u2", "two@example.com")
	seedKnowledgeBookmark(t, db, "u1", "a", "Alpha", "2026-07-10T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "b", "Beta", "2026-07-09T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u2", "foreign", "Foreign", "2026-07-10T00:00:00Z")
	_, _ = db.Exec(`UPDATE bookmarks SET domain='one.example' WHERE id='a'`)
	_, _ = db.Exec(`UPDATE bookmarks SET domain='two.example' WHERE id='b'`)
	_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES('a','u1','Systems'),('b','u1','Systems'),('foreign','u2','Systems')`)

	first := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights", "")
	insights := first["insights"].([]any)
	if len(insights) == 0 {
		t.Fatal("expected evidence-backed insight")
	}
	for _, raw := range insights {
		for _, evidence := range raw.(map[string]any)["evidence"].([]any) {
			if evidence.(map[string]any)["id"] == "foreign" {
				t.Fatal("foreign evidence leaked")
			}
		}
	}
	targetID := insights[0].(map[string]any)["id"].(string)
	feedback := `{"target_type":"insight","target_id":"` + targetID + `","feedback":"dismiss"}`
	callKnowledgeHandler(t, service.SaveFeedback, auth.User{ID: "u1"}, http.MethodPost, "/api/feedback", feedback)
	second := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights", "")
	for _, raw := range second["insights"].([]any) {
		if raw.(map[string]any)["id"] == targetID {
			t.Fatalf("dismissed insight still returned: %#v", second)
		}
	}
}

func TestInsightFeedbackRejectsRelationshipOnlyConfirmation(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeBookmark(t, db, "u1", "a", "Alpha", "2026-07-10T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "b", "Beta", "2026-07-09T00:00:00Z")
	_, _ = db.Exec(`UPDATE bookmarks SET domain='one.example' WHERE id='a'`)
	_, _ = db.Exec(`UPDATE bookmarks SET domain='two.example' WHERE id='b'`)
	_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES('a','u1','Systems'),('b','u1','Systems')`)
	insights := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights", "")["insights"].([]any)
	targetID := insights[0].(map[string]any)["id"].(string)
	req := httptest.NewRequest(http.MethodPost, "/api/feedback", strings.NewReader(`{"target_type":"insight","target_id":"`+targetID+`","feedback":"confirm"}`))
	rec := httptest.NewRecorder()
	service.SaveFeedback(rec, req, auth.User{ID: "u1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("confirm insight status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM knowledge_feedback WHERE user_id='u1'`).Scan(&count)
	if count != 0 {
		t.Fatalf("invalid confirmation persisted %d feedback rows", count)
	}
}

func TestKnowledgeFeedbackExportIsOptionalAndUserScoped(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeUser(t, db, "u2", "two@example.com")
	_, _ = db.Exec(`INSERT INTO knowledge_feedback(user_id,target_type,target_id,feedback,created_at,updated_at) VALUES('u1','insight','insight_one','useful','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'),('u2','insight','insight_two','dismiss','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	items := service.exportKnowledgeFeedback(context.Background(), "u1")
	if len(items) != 1 || items[0]["target_id"] != "insight_one" {
		t.Fatalf("unexpected feedback export: %#v", items)
	}
	service.restoreKnowledgeFeedback(context.Background(), "u1", nil, time.Now().UTC().Format(time.RFC3339))
	if _, ok, err := service.restoreFullExport(context.Background(), "u1", []byte(`{"bookmarks":[]}`)); err != nil || !ok {
		t.Fatalf("old backup should remain valid: ok=%v err=%v", ok, err)
	}
}

func newKnowledgeTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, jobs.New(db), safefetch.New(), providers.GeminiClient{}), db
}

func seedKnowledgeUser(t *testing.T, db *sql.DB, id, email string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users(id,email,name,created_at,updated_at) VALUES(?,?,?,?,?)`, id, email, id, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
}

func seedKnowledgeBookmark(t *testing.T, db *sql.DB, userID, id, title, updated string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO bookmarks(id,user_id,url,title,domain,source_published_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, userID, "https://"+id+".example", title, id+".example", updated, updated, updated)
	if err != nil {
		t.Fatal(err)
	}
}

func callKnowledgeHandler(t *testing.T, handler func(http.ResponseWriter, *http.Request, auth.User), user auth.User, method, target, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req, user)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", target, rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
