package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/providers"
)

// fakeEmbedder serves an OpenAI-compatible embeddings endpoint in memory and
// lets a test switch the configured embedding space.
type fakeEmbedder struct {
	mu       sync.Mutex
	config   providers.EmbeddingConfig
	calls    []string
	failWith int
}

func newFakeEmbedder(model string) *fakeEmbedder {
	return &fakeEmbedder{config: providers.ResolveEmbedding(providers.TextProviderSettings{}, providers.EmbeddingSelection{Provider: providers.ProviderCustom, Model: model, BaseURL: "https://embeddings.test/v1"})}
}

func (f *fakeEmbedder) setModel(model string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.config.Model = model
}

func (f *fakeEmbedder) client(context.Context) providers.EmbeddingClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return providers.EmbeddingClient{Config: f.config, HTTP: &http.Client{Transport: roundTripFunc(f.roundTrip)}}
}

func (f *fakeEmbedder) roundTrip(r *http.Request) (*http.Response, error) {
	var body struct {
		Input string `json:"input"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.calls = append(f.calls, body.Input)
	fail := f.failWith
	f.mu.Unlock()
	if fail != 0 {
		return jsonResponse(fail, map[string]any{"error": "nope"}), nil
	}
	vector := []float64{1, 0}
	if strings.Contains(strings.ToLower(body.Input), "cooking") {
		vector = []float64{0, 1}
	}
	return jsonResponse(http.StatusOK, map[string]any{"data": []map[string]any{{"embedding": vector}}}), nil
}

func (f *fakeEmbedder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func seedEmbeddableBookmark(t *testing.T, db *sql.DB, userID, id, title, text, space, vector string) {
	t.Helper()
	seedKnowledgeBookmark(t, db, userID, id, title, "2026-07-01T00:00:00Z")
	if _, err := db.Exec(`INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,content_text,content_hash,quality_status,extractor_version,is_selected,created_at,updated_at) VALUES(?,?,?,'fetched_article','test',?,?,'complete','test-v1',1,?,?)`, "ev-"+id, id, userID, text, "hash-"+id, nowString(), nowString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE bookmarks SET enrichment_version=? WHERE id=?`, providers.SemanticVersion, id); err != nil {
		t.Fatal(err)
	}
	if vector != "" {
		dims := strings.Count(vector, ",") + 1
		if _, err := db.Exec(`UPDATE bookmarks SET embedding=?,embedding_dim=?,embedding_model=? WHERE id=?`, []byte(vector), dims, space, id); err != nil {
			t.Fatal(err)
		}
	}
}

func callRelated(t *testing.T, service *Service, id, query string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/bookmarks/"+id+"/related"+query, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	service.Related(rec, req, auth.User{ID: "u1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("related %s status=%d body=%s", id, rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func embeddingSpaceOf(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var space sql.NullString
	if err := db.QueryRow(`SELECT embedding_model FROM bookmarks WHERE id=?`, id).Scan(&space); err != nil {
		t.Fatal(err)
	}
	return space.String
}

func runQueuedReembedJobs(t *testing.T, service *Service, db *sql.DB, maxJobs int) int {
	t.Helper()
	ran := 0
	for ; ran < maxJobs; ran++ {
		var id, payload string
		err := db.QueryRow(`SELECT id,payload_json FROM jobs WHERE type=? AND status='queued' ORDER BY created_at,rowid LIMIT 1`, JobTypeReembed).Scan(&id, &payload)
		if err == sql.ErrNoRows {
			return ran
		}
		if err != nil {
			t.Fatal(err)
		}
		_, _ = db.Exec(`UPDATE jobs SET status='leased' WHERE id=?`, id)
		processErr := service.ProcessJob(context.Background(), JobTypeReembed, payload)
		status := "completed"
		if processErr != nil {
			status = "failed"
		}
		_, _ = db.Exec(`UPDATE jobs SET status=?,last_error=? WHERE id=?`, status, func() string {
			if processErr != nil {
				return processErr.Error()
			}
			return ""
		}(), id)
	}
	return ran
}

func TestStaleEmbeddingsAreNeverComparedWithCurrentVectors(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	embedder := newFakeEmbedder("embed-v2")
	service.SetEmbeddingProvider(embedder.client)
	current := embedder.client(context.Background()).Space()
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedEmbeddableBookmark(t, db, "u1", "source", "Vector search", "Vector search text", current, "[1,0]")
	seedEmbeddableBookmark(t, db, "u1", "close", "Index design", "Index text", current, "[0.98,0.02]")
	// Same dimensions, different model: must be ignored even though the
	// vector would look identical.
	seedEmbeddableBookmark(t, db, "u1", "stale", "Old space", "Old text", "custom/embed-v1", "[1,0]")

	related := callRelated(t, service, "source", "?limit=5")
	items := related["related"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "close" {
		t.Fatalf("related = %#v", related)
	}
	// The stale bookmark is still waiting for re-embedding.
	if related["semantic_status"] != SemanticUpdating {
		t.Fatalf("semantic_status = %#v", related["semantic_status"])
	}

	staleRelated := callRelated(t, service, "stale", "")
	if len(staleRelated["related"].([]any)) != 0 {
		t.Fatalf("stale bookmark compared across spaces: %#v", staleRelated)
	}

	duplicates := callKnowledgeHandler(t, service.Duplicates, auth.User{ID: "u1"}, http.MethodGet, "/api/bookmarks/duplicates/detect", "")
	for _, raw := range duplicates["duplicates"].([]any) {
		group := raw.(map[string]any)
		for _, bookmark := range group["bookmarks"].([]any) {
			if bookmark.(map[string]any)["id"] == "stale" {
				t.Fatalf("stale vector used for duplicates: %#v", group)
			}
		}
	}

	search := callKnowledgeHandler(t, service.GraphSearch, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/search?query=vector+search", "")
	if search["mode"] != "semantic" {
		t.Fatalf("search mode = %#v", search["mode"])
	}
	results := search["results"].([]any)
	if len(results) == 0 {
		t.Fatal("semantic search returned nothing")
	}
	for _, raw := range results {
		if raw.(map[string]any)["id"] == "stale" {
			t.Fatalf("stale vector ranked in semantic search: %#v", raw)
		}
	}
}

func TestSemanticSurfacesDegradeWhenEmbeddingsAreNotConfigured(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedEmbeddableBookmark(t, db, "u1", "a", "Vector search", "Vector search with SQLite", "gemini/gemini-embedding-2@768", "[1,0]")
	seedEmbeddableBookmark(t, db, "u1", "b", "Vector index", "Vector index design", "gemini/gemini-embedding-2@768", "[1,0]")
	// GeminiClient{} has no key: embeddings are unconfigured.
	search := callKnowledgeHandler(t, service.GraphSearch, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/search?query=vector", "")
	if search["semantic_status"] != SemanticNotConfigured || search["mode"] != "keyword" {
		t.Fatalf("search = %#v", search)
	}
	if !strings.Contains(search["semantic_message"].(string), "no embedding model is configured") {
		t.Fatalf("message = %#v", search["semantic_message"])
	}
	if len(search["results"].([]any)) == 0 {
		t.Fatal("keyword fallback returned nothing")
	}
	related := callRelated(t, service, "a", "")
	if len(related["related"].([]any)) != 0 || related["semantic_status"] != SemanticNotConfigured {
		t.Fatalf("related = %#v", related)
	}
	graph := callKnowledgeHandler(t, service.KnowledgeGraph, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/explore", "")
	if graph["total_bookmarks"].(float64) != 2 || len(graph["related_bookmarks"].(map[string]any)) != 0 {
		t.Fatalf("graph should keep bookmarks without similarity links: %#v", graph)
	}
	graphV2 := callKnowledgeHandler(t, service.KnowledgeGraphV2, auth.User{ID: "u1"}, http.MethodGet, "/api/knowledge-graph/v2", "")
	if graphV2["semantic_status"] != SemanticNotConfigured {
		t.Fatalf("graph v2 = %#v", graphV2["semantic_status"])
	}
	if queued, err := service.ScheduleReembed(context.Background()); err != nil || queued {
		t.Fatalf("unconfigured embeddings must not schedule work: queued=%v err=%v", queued, err)
	}
}

func TestReembedRebuildsStaleAndMissingVectorsInBoundedResumableBatches(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	embedder := newFakeEmbedder("embed-v1")
	service.SetEmbeddingProvider(embedder.client)
	oldSpace := embedder.client(context.Background()).Space()
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	total := reembedBatchSize + 4
	for i := range total {
		id := "bm-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		seedEmbeddableBookmark(t, db, "u1", id, "Item "+id, "Body "+id, oldSpace, "[1,0]")
	}
	// A bookmark that never got a vector (for example while embeddings were
	// unconfigured) and one without complete evidence.
	seedEmbeddableBookmark(t, db, "u1", "zz-missing", "Cooking notes", "Cooking text", "", "")
	seedKnowledgeBookmark(t, db, "u1", "zz-no-evidence", "No evidence", "2026-07-01T00:00:00Z")

	if queued, err := service.ScheduleReembed(context.Background()); err != nil || !queued {
		t.Fatalf("missing vector should schedule work: queued=%v err=%v", queued, err)
	}
	if queued, _ := service.ScheduleReembed(context.Background()); queued {
		t.Fatal("a pending pass must not be duplicated")
	}
	if ran := runQueuedReembedJobs(t, service, db, 5); ran != 1 {
		t.Fatalf("ran %d jobs", ran)
	}
	if got := embeddingSpaceOf(t, db, "zz-missing"); got != oldSpace {
		t.Fatalf("missing vector not filled: %q", got)
	}
	if embedder.callCount() != 1 {
		t.Fatalf("current vectors were re-embedded: %d calls", embedder.callCount())
	}

	// Changing the model makes every vector stale.
	embedder.setModel("embed-v2")
	newSpace := embedder.client(context.Background()).Space()
	stats, err := service.EmbeddingStats(context.Background())
	if err != nil || stats.Current != 0 || stats.Stale != total+1 || stats.Missing != 0 {
		t.Fatalf("stats after change = %#v err=%v", stats, err)
	}
	if status := service.semanticStatus(context.Background(), "u1", embedder.client(context.Background())); status != SemanticUpdating {
		t.Fatalf("status = %q", status)
	}
	if queued, err := service.ScheduleReembed(context.Background()); err != nil || !queued {
		t.Fatalf("schedule after change: queued=%v err=%v", queued, err)
	}
	// The first batch is bounded and enqueues its continuation.
	if ran := runQueuedReembedJobs(t, service, db, 1); ran != 1 {
		t.Fatal("first batch did not run")
	}
	stats, _ = service.EmbeddingStats(context.Background())
	if stats.Current != reembedBatchSize || !stats.JobQueued {
		t.Fatalf("after first batch = %#v", stats)
	}
	// A transient provider failure pauses the pass without losing progress.
	embedder.mu.Lock()
	embedder.failWith = http.StatusTooManyRequests
	embedder.mu.Unlock()
	runQueuedReembedJobs(t, service, db, 1)
	var lastError string
	_ = db.QueryRow(`SELECT COALESCE(last_error,'') FROM jobs WHERE type=? AND status='failed'`, JobTypeReembed).Scan(&lastError)
	if !strings.Contains(lastError, providers.ErrorProviderRateLimited) {
		t.Fatalf("last_error = %q", lastError)
	}
	embedder.mu.Lock()
	embedder.failWith = 0
	embedder.mu.Unlock()
	if queued, _ := service.ScheduleReembed(context.Background()); !queued {
		t.Fatal("resume was not scheduled")
	}
	runQueuedReembedJobs(t, service, db, 5)
	stats, _ = service.EmbeddingStats(context.Background())
	if stats.Current != total+1 || stats.Stale != 0 || stats.Missing != 0 || stats.JobQueued {
		t.Fatalf("final stats = %#v", stats)
	}
	if got := embeddingSpaceOf(t, db, "zz-no-evidence"); got != "" {
		t.Fatalf("bookmark without complete evidence embedded: %q", got)
	}
	if got := embeddingSpaceOf(t, db, "bm-aa"); got != newSpace {
		t.Fatalf("space = %q, want %q", got, newSpace)
	}
	if queued, _ := service.ScheduleReembed(context.Background()); queued {
		t.Fatal("nothing left to re-embed")
	}
}

func TestReembedRestartsWhenSpaceChangesMidPass(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	embedder := newFakeEmbedder("embed-v1")
	service.SetEmbeddingProvider(embedder.client)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedEmbeddableBookmark(t, db, "u1", "a", "A", "A text", "", "")
	seedEmbeddableBookmark(t, db, "u1", "b", "B", "B text", "", "")
	// A continuation written for an older space with a cursor past "a".
	if err := service.enqueueReembed(context.Background(), reembedPayload{Space: "custom/old", After: "a"}); err != nil {
		t.Fatal(err)
	}
	runQueuedReembedJobs(t, service, db, 3)
	space := embedder.client(context.Background()).Space()
	if embeddingSpaceOf(t, db, "a") != space || embeddingSpaceOf(t, db, "b") != space {
		t.Fatal("cursor from an older space must be discarded")
	}
}

func TestEnrichmentStoresTheEmbeddingSpace(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	embedder := newFakeEmbedder("embed-v3")
	service.SetEmbeddingProvider(embedder.client)
	seedKnowledgeUser(t, db, "u1", "one@example.com")
	seedKnowledgeBookmark(t, db, "u1", "a", "A", "2026-07-01T00:00:00Z")
	item := service.enrichText(context.Background(), "a", "u1", "A", "", "Some evidence text")
	if item.EmbeddingSpace != "custom/embed-v3" || len(item.Embedding) != 2 {
		t.Fatalf("enrichment = %#v", item)
	}
	service.storeEnrichment(context.Background(), "a", "u1", item, true)
	if got := embeddingSpaceOf(t, db, "a"); got != "custom/embed-v3" {
		t.Fatalf("stored space = %q", got)
	}
}

func TestReembedPausesOnSystemicRejection(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusBadRequest} {
		service, db := newKnowledgeTestService(t)
		embedder := newFakeEmbedder("wrong-model")
		embedder.failWith = status
		service.SetEmbeddingProvider(embedder.client)
		seedKnowledgeUser(t, db, "u1", "one@example.com")
		for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
			seedEmbeddableBookmark(t, db, "u1", id, "Item "+id, "Body "+id, "", "")
		}
		if queued, err := service.ScheduleReembed(context.Background()); err != nil || !queued {
			t.Fatalf("schedule: queued=%v err=%v", queued, err)
		}
		runQueuedReembedJobs(t, service, db, 5)
		var failed int
		_ = db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type=? AND status='failed' AND last_error LIKE 're-embed paused (provider_rejected)%'`, JobTypeReembed).Scan(&failed)
		if failed != 1 {
			t.Fatalf("status %d: a misconfigured model must pause visibly, failed jobs=%d", status, failed)
		}
		maxCalls := 1
		if status == http.StatusBadRequest {
			maxCalls = reembedSystemicSkips
		}
		if calls := embedder.callCount(); calls > maxCalls {
			t.Fatalf("status %d: swept %d items before pausing", status, calls)
		}
	}
}
