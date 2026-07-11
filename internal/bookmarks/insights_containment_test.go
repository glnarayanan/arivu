package bookmarks

import (
	"net/http"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
)

func TestInsightContainmentFiltersFamilyBeforeLimitAndRanksCandidates(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	for _, id := range []string{"a", "b", "c"} {
		seedKnowledgeBookmark(t, db, "u1", id, id, "2026-07-10T00:00:00Z")
	}
	_, _ = db.Exec(`UPDATE bookmarks SET domain='one.example' WHERE id='a'`)
	_, _ = db.Exec(`UPDATE bookmarks SET domain='two.example' WHERE id='b'`)
	_, _ = db.Exec(`UPDATE bookmarks SET domain='three.example' WHERE id='c'`)
	_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES
		('a','u1','Alpha'),('b','u1','Alpha'),
		('a','u1','Beta'),('b','u1','Beta'),('c','u1','Beta')`)
	_, _ = db.Exec(`INSERT INTO notes(id,user_id,title,body,created_at,updated_at) VALUES('n1','u1','Revision','I changed my mind about this.','2026-01-01T00:00:00Z','2026-07-10T00:00:00Z')`)

	payload := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=recurring_connection&limit=1", "")
	insights := payload["insights"].([]any)
	if len(insights) != 1 {
		t.Fatalf("expected one recurring insight, got %#v", payload)
	}
	insight := insights[0].(map[string]any)
	if insight["type"] != "recurring_connection" || insight["title"] != "Recurring connection: Beta" {
		t.Fatalf("family filter or ranking applied after limit: %#v", payload)
	}
}

func TestInsightContainmentSuppressesArtifactAndGenericConcepts(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeBookmark(t, db, "u1", "a", "Alpha", "2026-07-10T00:00:00Z")
	seedKnowledgeBookmark(t, db, "u1", "b", "Beta", "2026-07-09T00:00:00Z")
	_, _ = db.Exec(`UPDATE bookmarks SET domain=CASE id WHEN 'a' THEN 'one.example' ELSE 'two.example' END`)
	for _, concept := range []string{"quot", "https", "com", "Jun", "2026", "10:30 PM", "things"} {
		_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES('a','u1',?),('b','u1',?)`, concept, concept)
	}

	payload := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=recurring_connection&limit=40", "")
	if insights := payload["insights"].([]any); len(insights) != 0 {
		t.Fatalf("junk concepts escaped containment: %#v", payload)
	}
}

func TestInsightContainmentReportsMissingHistoryWithoutFullConfidence(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	now := time.Now().UTC()
	for index, id := range []string{"a", "b", "c"} {
		seedKnowledgeBookmark(t, db, "u1", id, id, now.Add(-time.Duration(index)*time.Hour).Format(time.RFC3339))
		_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES(?,'u1','Systems')`, id)
	}

	payload := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=emerging_theme", "")
	if payload["state"] != "not_enough_history" {
		t.Fatalf("expected an explicit missing-history state: %#v", payload)
	}
	if insights := payload["insights"].([]any); len(insights) != 0 {
		t.Fatalf("zero-baseline themes must not be emitted as confident insights: %#v", payload)
	}
}

func TestInsightContainmentDiversifiesFamiliesAndTypesRecommendations(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	for _, id := range []string{"a", "b", "c"} {
		seedKnowledgeBookmark(t, db, "u1", id, id, "2025-01-01T00:00:00Z")
	}
	_, _ = db.Exec(`UPDATE bookmarks SET domain=id||'.example'`)
	_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES('a','u1','Systems'),('b','u1','Systems'),('c','u1','Systems')`)
	for _, id := range []string{"n1", "n2", "n3"} {
		_, _ = db.Exec(`INSERT INTO notes(id,user_id,title,body,created_at,updated_at) VALUES(?,'u1',?,'I changed my mind about this.','2026-01-01T00:00:00Z','2026-07-10T00:00:00Z')`, id, id)
	}
	if _, err := db.Exec(`INSERT INTO item_states(user_id,item_type,item_id,stage,importance,created_at,updated_at) VALUES('u1','bookmark','a','processed',3,'2025-01-01T00:00:00Z','2025-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	payload := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?limit=3", "")
	insights := payload["insights"].([]any)
	families := map[any]bool{}
	foundRecommendation := false
	for _, raw := range insights {
		insight := raw.(map[string]any)
		families[insight["type"]] = true
		if insight["type"] == "forgotten_value" {
			foundRecommendation = insight["kind"] == "recommendation"
			if _, mixed := insight["confidence"]; mixed {
				t.Fatalf("recommendation exposed analytical confidence: %#v", insight)
			}
		}
	}
	if len(families) < 2 {
		t.Fatalf("one family monopolized the first page: %#v", payload)
	}
	if !foundRecommendation {
		all := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=forgotten_value", "")["insights"].([]any)
		foundRecommendation = len(all) == 1 && all[0].(map[string]any)["kind"] == "recommendation"
		if foundRecommendation {
			if _, mixed := all[0].(map[string]any)["confidence"]; mixed {
				t.Fatalf("recommendation exposed analytical confidence: %#v", all[0])
			}
		}
	}
	if !foundRecommendation {
		t.Fatalf("forgotten value was not typed as a recommendation: %#v", payload)
	}
}
