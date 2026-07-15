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
	_, _ = db.Exec(`UPDATE bookmarks SET thumbnail='/api/media/preview' WHERE id='b-new'`)
	_, _ = db.Exec(`UPDATE bookmarks SET thumbnail='https://tracker.example/preview.png' WHERE id='b-old'`)

	first := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?type=bookmark&limit=1", "")
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "b-new" || items[0].(map[string]any)["thumbnail"] != "/api/media/preview" {
		t.Fatalf("unexpected first page: %#v", first)
	}
	cursor, _ := first["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("missing next cursor: %#v", first)
	}
	second := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?type=bookmark&limit=1&cursor="+cursor, "")
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "b-old" || items[0].(map[string]any)["thumbnail"] != "" {
		t.Fatalf("unexpected second page: %#v", second)
	}
}

func TestLibraryItemsAcceptLegacyNewestCursor(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeBookmark(t, db, "u1", "b-new", "New", "2026-07-10T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "b-old", "Old", "2026-07-09T00:00:00Z")
	legacyCursor := encodeLibraryCursor(libraryCursor{UpdatedAt: "2026-07-10T00:00:00Z", Type: "bookmark", ID: "b-new"})

	payload := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?type=bookmark&limit=1&cursor="+legacyCursor, "")
	items := payload["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "b-old" {
		t.Fatalf("legacy newest cursor did not continue pagination: %#v", payload)
	}
}

func TestLibraryItemsSeparateCapturedContentFromDerivedKnowledge(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeBookmark(t, db, "u1", "saved", "Saved article", "2026-07-10T00:00:00Z")
	seedInsightConcept(t, db, "u1", "saved", "Householder reflections")
	_, _ = db.Exec(`INSERT INTO bookmark_entities(bookmark_id,user_id,entity,source,confidence,validated_at) VALUES('saved','u1','CUDA','model',0.9,'2026-07-10T00:00:00Z')`)

	content := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?scope=content", "")
	for _, raw := range content["items"].([]any) {
		if itemType := raw.(map[string]any)["type"]; itemType == "concept" || itemType == "entity" {
			t.Fatalf("content scope included derived knowledge: %#v", content)
		}
	}
	derived := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?scope=derived", "")
	items := derived["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("derived scope did not return validated knowledge: %#v", derived)
	}
	for _, raw := range items {
		if itemType := raw.(map[string]any)["type"]; itemType != "concept" && itemType != "entity" {
			t.Fatalf("derived scope included primary content: %#v", derived)
		}
	}
}

func TestLibraryItemsHideOnlyUnresolvedTCOPlaceholders(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeBookmark(t, db, "u1", "raw-tco", "https://t.co/raw", "2026-07-12T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "useful-tco", "https://t.co/useful", "2026-07-11T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "ordinary-url", "https://example.com/unprocessed", "2026-07-10T00:00:00Z")
	_, _ = db.Exec(`UPDATE bookmarks SET description='https://t.co/raw',text_content='https://t.co/raw',content_kind='x_link' WHERE id='raw-tco'`)
	_, _ = db.Exec(`UPDATE bookmarks SET description='https://t.co/useful',text_content='Useful source context',content_kind='x_link' WHERE id='useful-tco'`)

	payload := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?type=bookmark&limit=20", "")
	items := payload["items"].([]any)
	if payloadHasItemID(items, "raw-tco") {
		t.Fatalf("Library exposed unresolved t.co placeholder: %#v", payload)
	}
	for _, want := range []string{"useful-tco", "ordinary-url"} {
		if !payloadHasItemID(items, want) {
			t.Fatalf("Library hid useful bookmark %s: %#v", want, payload)
		}
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

func TestKnowledgeSurfacesReplaceGenericBookmarkTitlesWithUsefulContext(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeBookmark(t, db, "u1", "generic-title", "Post / X", "2026-07-12T00:00:00Z")
	_, _ = db.Exec(`UPDATE bookmarks SET description='A useful X post about retrieval systems.',text_content='A useful X post about retrieval systems.',content_kind='x_post',summary_version=?,enrichment_version=? WHERE id='generic-title'`, providers.SummaryPromptVersion, providers.SemanticVersion)
	_, _ = db.Exec(`INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,authority,content_text,content_hash,quality_status,is_selected,created_at,updated_at) VALUES('e-generic','generic-title','u1','source_post','x_api',100,'A useful X post about retrieval systems.','generic-hash','complete',1,'2026-07-12T00:00:00Z','2026-07-12T00:00:00Z')`)

	graph := callKnowledgeHandler(t, service.KnowledgeGraphV2, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/v2?focus=bookmark:generic-title", "")
	nodes := graph["nodes"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["title"] != "A useful X post about retrieval systems." {
		t.Fatalf("Graph retained generic bookmark title: %#v", graph)
	}
	library := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?type=bookmark", "")
	if items := library["items"].([]any); len(items) != 1 || items[0].(map[string]any)["title"] != "A useful X post about retrieval systems." {
		t.Fatalf("Library retained generic bookmark title: %#v", library)
	}
}

func TestKnowledgeGraphV2HidesUnenrichedBookmarksAcrossKnowledgeSurfaces(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeUser(t, db, "u2", "two@example.com")
	seedKnowledgeBookmark(t, db, "u1", "raw", "https://t.co/raw", "2026-07-12T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "complete", "Useful source", "2026-07-11T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "linked", "https://t.co/linked", "2026-07-10T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "foreign-only", "https://t.co/foreign", "2026-07-09T00:00:00Z")
	_, _ = db.Exec(`UPDATE bookmarks SET summary_version=?,enrichment_version=? WHERE id='raw'`, providers.SummaryPromptVersion, providers.SemanticVersion)
	_, _ = db.Exec(`INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,authority,content_text,content_hash,quality_status,is_selected,created_at,updated_at) VALUES('e-raw','raw','u1','source_post','x_api',100,'https://t.co/raw','raw-hash','metadata_only',1,'2026-07-12T00:00:00Z','2026-07-12T00:00:00Z')`)
	_, _ = db.Exec(`UPDATE bookmarks SET summary_version=?,enrichment_version=? WHERE id='complete'`, providers.SummaryPromptVersion, providers.SemanticVersion)
	_, _ = db.Exec(`INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,authority,content_text,content_hash,quality_status,is_selected,created_at,updated_at) VALUES('e-complete','complete','u1','fetched_article','web_fetch',80,'Usable evidence','hash','complete',1,'2026-07-11T00:00:00Z','2026-07-11T00:00:00Z')`)
	_, _ = db.Exec(`INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) VALUES('owned-link','u1','bookmark','linked','bookmark','complete','','manual','2026-07-12T00:00:00Z'),('foreign-link','u2','bookmark','foreign-only','bookmark','complete','','manual','2026-07-12T00:00:00Z')`)

	library := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?type=bookmark&limit=20", "")
	if payloadHasItemID(library["items"].([]any), "raw") {
		t.Fatalf("Library exposed raw t.co placeholder: %#v", library)
	}
	graph := callKnowledgeHandler(t, service.KnowledgeGraphV2, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/v2?node_limit=20", "")
	nodes := graph["nodes"].([]any)
	for _, want := range []string{"bookmark:complete", "bookmark:linked"} {
		if !payloadHasItemID(nodes, want) {
			t.Fatalf("Graph missing eligible %s: %#v", want, graph)
		}
	}
	for _, hidden := range []string{"bookmark:raw", "bookmark:foreign-only"} {
		if payloadHasItemID(nodes, hidden) {
			t.Fatalf("Graph exposed ineligible %s: %#v", hidden, graph)
		}
	}
	focused := callKnowledgeHandler(t, service.KnowledgeGraphV2, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/v2?focus=bookmark:raw", "")
	if len(focused["nodes"].([]any)) != 0 {
		t.Fatalf("focused lookup exposed raw bookmark: %#v", focused)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/feedback", strings.NewReader(`{"target_type":"relationship","target_id":"forged","action":"dismiss","from":"bookmark:raw","to":"bookmark:complete"}`))
	response := httptest.NewRecorder()
	service.SaveFeedback(response, request, auth.User{ID: "u1"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("relationship feedback accepted hidden raw bookmark: status=%d body=%s", response.Code, response.Body.String())
	}
}

func payloadHasItemID(items []any, id string) bool {
	for _, raw := range items {
		if raw.(map[string]any)["id"] == id {
			return true
		}
	}
	return false
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
	seedInsightConcept(t, db, "u1", "a", "Systems")
	seedInsightConcept(t, db, "u1", "b", "Systems")
	seedInsightConcept(t, db, "u2", "foreign", "Systems")

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
	seedInsightConcept(t, db, "u1", "a", "Systems")
	seedInsightConcept(t, db, "u1", "b", "Systems")
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

func TestLibraryAndGraphQuarantineLegacyUnprovenSemantics(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeBookmark(t, db, "u1", "a", "Rotten legacy item", "2026-07-10T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "b", "Another rotten item", "2026-07-09T00:00:00Z")
	_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES('a','u1','quot')`)
	_, _ = db.Exec(`INSERT INTO bookmark_entities(bookmark_id,user_id,entity) VALUES('a','u1','https')`)
	_, _ = db.Exec(`UPDATE bookmarks SET embedding='[1,0]',embedding_dim=2,embedding_model='legacy' WHERE id IN ('a','b')`)

	library := callKnowledgeHandler(t, service.LibraryItems, auth.User{ID: "u1"}, http.MethodGet, "/api/library/items?type=concept", "")
	if items := library["items"].([]any); len(items) != 0 {
		t.Fatalf("library exposed legacy semantics: %#v", library)
	}
	graph := callKnowledgeHandler(t, service.KnowledgeGraphV2, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/v2", "")
	for _, raw := range graph["nodes"].([]any) {
		typeName := raw.(map[string]any)["type"]
		if typeName == "concept" || typeName == "entity" {
			t.Fatalf("graph exposed legacy semantic node: %#v", raw)
		}
	}
	for _, raw := range graph["edges"].([]any) {
		typeName := raw.(map[string]any)["type"]
		if typeName == "shared_concept" || typeName == "shared_entity" || typeName == "semantic_similarity" {
			t.Fatalf("graph exposed legacy semantic edge: %#v", raw)
		}
	}
	seedInsightConcept(t, db, "u1", "a", "Evidence provenance")
	graph = callKnowledgeHandler(t, service.KnowledgeGraphV2, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/v2", "")
	foundValidConcept := false
	for _, raw := range graph["nodes"].([]any) {
		node := raw.(map[string]any)
		foundValidConcept = foundValidConcept || (node["type"] == "concept" && node["title"] == "Evidence provenance")
	}
	if !foundValidConcept {
		t.Fatalf("graph hid validated concept: %#v", graph)
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
