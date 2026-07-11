package bookmarks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/auth"
)

func TestEmergingThemesUseSourceTimeAndIgnoreBulkImportUpdates(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	now := time.Now().UTC()
	for index, id := range []string{"recent-a", "recent-b", "recent-c", "recent-d", "prior"} {
		seedKnowledgeBookmark(t, db, "u1", id, id, now.Format(time.RFC3339))
		published := now.Add(-time.Duration(index+1) * 24 * time.Hour)
		if id == "prior" {
			published = now.Add(-40 * 24 * time.Hour)
		}
		publisher := "publisher-a"
		if index%2 == 1 {
			publisher = "publisher-b"
		}
		_, _ = db.Exec(`UPDATE bookmarks SET source_published_at=?,source_publisher_key=?,updated_at=? WHERE id=?`, published.Format(time.RFC3339), publisher, now.Format(time.RFC3339), id)
		_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES(?,'u1','Distributed systems')`, id)
	}

	first := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=emerging_theme", "")
	if len(first["insights"].([]any)) != 1 {
		t.Fatalf("expected source-time emerging theme: %#v", first)
	}
	_, _ = db.Exec(`UPDATE bookmarks SET updated_at='2035-01-01T00:00:00Z' WHERE user_id='u1'`)
	second := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=emerging_theme", "")
	if second["insights"].([]any)[0].(map[string]any)["id"] != first["insights"].([]any)[0].(map[string]any)["id"] {
		t.Fatalf("updated_at changed trend identity: first=%#v second=%#v", first, second)
	}

	_, _ = db.Exec(`UPDATE bookmarks SET source_published_at=? WHERE id LIKE 'recent-%'`, now.Add(-365*24*time.Hour).Format(time.RFC3339))
	bulkImported := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=emerging_theme", "")
	if len(bulkImported["insights"].([]any)) != 0 {
		t.Fatalf("old sources imported today became emerging: %#v", bulkImported)
	}
}

func TestRecurringConnectionsRequireThreeSourcesAndIndependentPublishers(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	for index, id := range []string{"a", "b", "c"} {
		seedKnowledgeBookmark(t, db, "u1", id, id, time.Date(2026, 7, 1+index, 0, 0, 0, 0, time.UTC).Format(time.RFC3339))
		_, _ = db.Exec(`UPDATE bookmarks SET source='x',domain='x.com',source_publisher_key='x:same-author' WHERE id=?`, id)
		_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES(?,'u1','Row level security')`, id)
	}
	missingDiversity := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=recurring_connection", "")
	if len(missingDiversity["insights"].([]any)) != 0 {
		t.Fatalf("one author satisfied publisher diversity: %#v", missingDiversity)
	}
	_, _ = db.Exec(`UPDATE bookmarks SET source_publisher_key='x:other-author' WHERE id='c'`)
	qualified := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=recurring_connection", "")
	if len(qualified["insights"].([]any)) != 1 {
		t.Fatalf("distinct X authors did not qualify: %#v", qualified)
	}
	insight := qualified["insights"].([]any)[0].(map[string]any)
	if insight["detector_version"] != insightDetectorVersion || insight["evidence_strength"] == "" {
		t.Fatalf("missing detector audit metadata: %#v", insight)
	}
}

func TestInsightCursorIsStableAndDetectsCorpusChanges(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	for index, id := range []string{"a", "b", "c"} {
		seedKnowledgeBookmark(t, db, "u1", id, id, time.Date(2026, 7, 1+index, 0, 0, 0, 0, time.UTC).Format(time.RFC3339))
		_, _ = db.Exec(`UPDATE bookmarks SET source_publisher_key=? WHERE id=?`, "publisher-"+id, id)
		_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES(?,'u1','Alpha systems'),(?,'u1','Beta systems')`, id, id)
	}
	first := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=recurring_connection&limit=1", "")
	cursor, _ := first["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("expected cursor: %#v", first)
	}
	second := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=recurring_connection&limit=1&cursor="+cursor, "")
	repeat := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=recurring_connection&limit=1&cursor="+cursor, "")
	if second["insights"].([]any)[0].(map[string]any)["id"] != repeat["insights"].([]any)[0].(map[string]any)["id"] {
		t.Fatalf("unchanged corpus pagination was unstable: second=%#v repeat=%#v", second, repeat)
	}
	_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES('a','u1','New corpus concept')`)
	changed := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=recurring_connection&limit=1&cursor="+cursor, "")
	if changed["state"] != "corpus_changed" || changed["restart_required"] != true {
		t.Fatalf("changed corpus did not invalidate cursor: %#v", changed)
	}
}

func TestInsightImpressionsAndReasonedFeedbackAreVersionedAndScoped(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeUser(t, db, "u2", "two@example.com")
	for index, id := range []string{"a", "b", "c"} {
		seedKnowledgeBookmark(t, db, "u1", id, id, time.Date(2026, 7, 1+index, 0, 0, 0, 0, time.UTC).Format(time.RFC3339))
		_, _ = db.Exec(`UPDATE bookmarks SET source_publisher_key=? WHERE id=?`, "publisher-"+id, id)
		_, _ = db.Exec(`INSERT INTO bookmark_concepts(bookmark_id,user_id,concept) VALUES(?,'u1','Privacy engineering')`, id)
	}
	insight := callKnowledgeHandler(t, service.Insights, auth.User{ID: "u1"}, http.MethodGet, "/api/insights?family=recurring_connection", "")["insights"].([]any)[0].(map[string]any)
	targetID := insight["id"].(string)
	callKnowledgeHandler(t, service.SaveFeedback, auth.User{ID: "u1"}, http.MethodPost, "/api/feedback", `{"target_type":"insight_impression","target_ids":["`+targetID+`","`+targetID+`"]}`)
	callKnowledgeHandler(t, service.SaveFeedback, auth.User{ID: "u1"}, http.MethodPost, "/api/feedback", `{"target_type":"insight","target_id":"`+targetID+`","feedback":"not_useful","reason":"generic"}`)
	var impressionCount int
	var family, version, reason string
	if err := db.QueryRow(`SELECT impression_count FROM insight_impressions WHERE user_id='u1' AND insight_id=? AND detector_version=?`, targetID, insightDetectorVersion).Scan(&impressionCount); err != nil || impressionCount != 1 {
		t.Fatalf("impression was not deduplicated and versioned: count=%d err=%v", impressionCount, err)
	}
	if err := db.QueryRow(`SELECT detector_family,detector_version,reason FROM knowledge_feedback WHERE user_id='u1' AND target_id=?`, targetID).Scan(&family, &version, &reason); err != nil || family != "recurring_connection" || version != insightDetectorVersion || reason != "generic" {
		t.Fatalf("feedback metadata mismatch: family=%q version=%q reason=%q err=%v", family, version, reason, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/feedback", strings.NewReader(`{"target_type":"insight_impression","target_ids":["`+targetID+`"]}`))
	rec := httptest.NewRecorder()
	service.SaveFeedback(rec, req, auth.User{ID: "u2"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign user impression status=%d body=%s", rec.Code, rec.Body.String())
	}
}
