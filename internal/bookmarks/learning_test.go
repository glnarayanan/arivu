package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/providers"
)

type learningProviderStub struct {
	mu       sync.Mutex
	replies  []string
	requests []string
	server   *httptest.Server
}

func newLearningProviderStub(t *testing.T, replies ...string) *learningProviderStub {
	t.Helper()
	stub := &learningProviderStub{replies: replies}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		stub.mu.Lock()
		defer stub.mu.Unlock()
		content := ""
		if len(request.Messages) > 0 {
			content = request.Messages[0].Content
		}
		stub.requests = append(stub.requests, content)
		reply := `{malformed`
		if len(stub.replies) > 0 {
			reply, stub.replies = stub.replies[0], stub.replies[1:]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": reply}}}})
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *learningProviderStub) client() providers.GeminiClient {
	return providers.GeminiClient{Provider: providers.ProviderOpenAI, APIKey: "test", Model: "test-model", BaseURL: s.server.URL, HTTP: s.server.Client()}
}

func seedLearningSource(t *testing.T, db *sql.DB, userID, bookmarkID, selected, unselected, summary string) {
	t.Helper()
	seedKnowledgeBookmark(t, db, userID, bookmarkID, "Learning source", "2026-01-01T00:00:00Z")
	_, err := db.Exec(`INSERT INTO bookmark_evidence(id,bookmark_id,user_id,evidence_kind,evidence_origin,authority,content_text,content_hash,quality_status,is_selected,created_at,updated_at) VALUES
		(?,?,?,'fetched_article','test',10,?,'selected-hash','complete',1,'2026-01-01','2026-01-01'),
		(?,?,?,'fetched_article','test',1,?,'old-hash','complete',0,'2026-01-01','2026-01-01')`, "selected-"+bookmarkID, bookmarkID, userID, selected, "old-"+bookmarkID, bookmarkID, userID, unselected)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO ai_summaries(id,bookmark_id,user_id,one_sentence,processing_status,created_at,updated_at) VALUES(?,?,?,?,'completed','2026-01-01','2026-01-01')`, "summary-"+bookmarkID, bookmarkID, userID, summary)
	if err != nil {
		t.Fatal(err)
	}
}

func learningRequest(t *testing.T, handler func(http.ResponseWriter, *http.Request, auth.User), user auth.User, method, id, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/learning/"+id, strings.NewReader(body))
	if id != "" {
		req.SetPathValue("id", id)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, req, user)
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder, payload
}

func prepareLearning(t *testing.T, service *Service, user auth.User, kind, question, bookmarkID string) map[string]any {
	t.Helper()
	body := `{"kind":` + quoteJSON(kind) + `,"question":` + quoteJSON(question) + `,"sources":[{"type":"bookmark","id":` + quoteJSON(bookmarkID) + `}]}`
	recorder, payload := learningRequest(t, service.PrepareLearning, user, http.MethodPost, "", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("prepare status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	return payload
}

func quoteJSON(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestLearningPrepareUsesSelectedOriginalEvidenceWithoutProvider(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedKnowledgeUser(t, db, "other", "other@example.com")
	seedLearningSource(t, db, "owner", "source", "SELECTED original passage about orbital bees.", "UNSELECTED stale passage", "AI SUMMARY must never be evidence")
	provider := newLearningProviderStub(t)
	service.SetAIProvider(func(context.Context) providers.GeminiClient { return provider.client() })

	payload := prepareLearning(t, service, auth.User{ID: "owner"}, "chat", "What about bees?", "source")
	raw, _ := json.Marshal(payload["passages"])
	text := string(raw)
	if !strings.Contains(text, "SELECTED original") || strings.Contains(text, "UNSELECTED") || strings.Contains(text, "AI SUMMARY") {
		t.Fatalf("prepare passages did not preserve selected original evidence: %s", raw)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("prepare contacted provider %d times", len(provider.requests))
	}

	for name, handler := range map[string]func(http.ResponseWriter, *http.Request, auth.User){"read": service.Learning, "generate": service.GenerateLearning, "save": service.SaveLearningNote, "delete": service.DeleteLearning} {
		t.Run("foreign_"+name, func(t *testing.T) {
			body := `{"question":"q","revision":0}`
			if name == "save" {
				body = `{"passage":"p1"}`
			}
			recorder, _ := learningRequest(t, handler, auth.User{ID: "other"}, http.MethodPost, payload["id"].(string), body)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("foreign %s status=%d body=%s", name, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestLearningGenerationConsentSourceFreshnessHistoryAndSavedNote(t *testing.T) {
	answer1 := `{"claims":[{"text":"Bees navigate by stars.","citations":[{"passage_id":"p1","quote":"orbital bees"}]}],"insufficient":false}`
	answer2 := `{"claims":[{"text":"They also dance.","citations":[{"passage_id":"p1","quote":"orbital bees"}]}],"insufficient":false}`
	provider := newLearningProviderStub(t, answer1, answer2)
	service, db := newKnowledgeTestService(t)
	service.SetAIProvider(func(context.Context) providers.GeminiClient { return provider.client() })
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedLearningSource(t, db, "owner", "source", "The orbital bees navigate by stars and dance.", "old", "summary")
	user := auth.User{ID: "owner"}
	prepared := prepareLearning(t, service, user, "chat", "Initial question", "source")
	id := prepared["id"].(string)
	consent := prepared["provider"].(map[string]any)["consent"].(string)

	recorder, _ := learningRequest(t, service.GenerateLearning, user, http.MethodPost, id, `{"question":"First?","revision":0}`)
	if recorder.Code != http.StatusConflict || len(provider.requests) != 0 {
		t.Fatalf("missing consent status=%d provider calls=%d", recorder.Code, len(provider.requests))
	}
	_, _ = db.Exec(`UPDATE bookmark_evidence SET content_text='changed' WHERE id='selected-source'`)
	recorder, _ = learningRequest(t, service.GenerateLearning, user, http.MethodPost, id, `{"question":"First?","revision":0,"consent":`+quoteJSON(consent)+`}`)
	if recorder.Code != http.StatusConflict || len(provider.requests) != 0 {
		t.Fatalf("changed source status=%d provider calls=%d", recorder.Code, len(provider.requests))
	}
	_, _ = db.Exec(`UPDATE bookmark_evidence SET content_text='The orbital bees navigate by stars and dance.' WHERE id='selected-source'`)
	recorder, generated := learningRequest(t, service.GenerateLearning, user, http.MethodPost, id, `{"question":"First?","revision":0,"consent":`+quoteJSON(consent)+`}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	exchanges := generated["exchanges"].([]any)
	citation := exchanges[0].(map[string]any)["answer"].(map[string]any)["claims"].([]any)[0].(map[string]any)["citations"].([]any)[0].(map[string]any)
	if citation["quote"] != "orbital bees" {
		t.Fatalf("exact citation lost: %#v", citation)
	}
	recorder, _ = learningRequest(t, service.GenerateLearning, user, http.MethodPost, id, `{"question":"Follow up?","revision":1,"consent":`+quoteJSON(consent)+`}`)
	if recorder.Code != http.StatusOK || len(provider.requests) != 2 || !strings.Contains(provider.requests[1], "First?") || !strings.Contains(provider.requests[1], "Bees navigate by stars.") {
		t.Fatalf("follow-up omitted history: status=%d request=%q", recorder.Code, provider.requests[len(provider.requests)-1])
	}
	recorder, saved := learningRequest(t, service.SaveLearningNote, user, http.MethodPost, id, `{"exchange":0}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("save note status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body, source string
	if err := db.QueryRow(`SELECT body,source FROM notes WHERE id=? AND user_id='owner'`, saved["id"]).Scan(&body, &source); err != nil {
		t.Fatal(err)
	}
	if source != "ai:answer" || !strings.Contains(body, "AI-generated answer") || !strings.Contains(body, "> orbital bees") {
		t.Fatalf("saved note lacks AI label or quote: source=%q body=%q", source, body)
	}
	var links int
	_ = db.QueryRow(`SELECT count(*) FROM item_links WHERE user_id='owner' AND from_id=? AND to_type='bookmark' AND to_id='source'`, saved["id"]).Scan(&links)
	if links != 1 {
		t.Fatalf("saved note source links=%d", links)
	}
}

func TestLearningMalformedRetryAndQuizAnswerHiding(t *testing.T) {
	quiz := `[{"question":"Q1","options":["a","b","c","d"],"correct":0,"explanation":"E1","citations":[{"passage_id":"p1","quote":"orbital bees"}]},{"question":"Q2","options":["a","b","c","d"],"correct":2,"explanation":"E2","citations":[{"passage_id":"p1","quote":"navigate by stars"}]},{"question":"Q3","options":["a","b","c","d"],"correct":3,"explanation":"E3","citations":[{"passage_id":"p1","quote":"dance"}]}]`
	provider := newLearningProviderStub(t, `{bad`, quiz)
	service, db := newKnowledgeTestService(t)
	service.SetAIProvider(func(context.Context) providers.GeminiClient { return provider.client() })
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedLearningSource(t, db, "owner", "source", "The orbital bees navigate by stars and dance.", "old", "summary")
	user := auth.User{ID: "owner"}
	prepared := prepareLearning(t, service, user, "quiz", "quiz", "source")
	id := prepared["id"].(string)
	consent := prepared["provider"].(map[string]any)["consent"].(string)
	body := `{"question":"Generate quiz","revision":0,"consent":` + quoteJSON(consent) + `}`
	recorder, _ := learningRequest(t, service.GenerateLearning, user, http.MethodPost, id, body)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("malformed status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var revision int
	var stored string
	_ = db.QueryRow(`SELECT revision,payload_json FROM learning_sessions WHERE id=?`, id).Scan(&revision, &stored)
	if revision != 0 || strings.Contains(stored, "Q1") {
		t.Fatalf("failed generation persisted state: revision=%d payload=%s", revision, stored)
	}
	recorder, generated := learningRequest(t, service.GenerateLearning, user, http.MethodPost, id, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	questions := generated["questions"].([]any)
	for _, raw := range questions {
		q := raw.(map[string]any)
		if _, ok := q["correct"]; ok || q["explanation"] != nil || q["citations"] != nil {
			t.Fatalf("quiz leaked answer before submission: %#v", q)
		}
	}
	recorder, revealed := learningRequest(t, service.SubmitLearningQuiz, user, http.MethodPost, id, `{"choices":[0,1,3],"revision":1}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	want := []float64{0, 2, 3}
	for i, raw := range revealed["questions"].([]any) {
		q := raw.(map[string]any)
		if q["correct"] != want[i] || q["explanation"] == nil || q["citations"] == nil {
			t.Fatalf("question %d reveal=%#v", i, q)
		}
	}
}

func TestLearningLibraryFallbackAndBackupRemapWithoutProvider(t *testing.T) {
	provider := newLearningProviderStub(t)
	service, db := newKnowledgeTestService(t)
	service.SetAIProvider(func(context.Context) providers.GeminiClient { return provider.client() })
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedKnowledgeUser(t, db, "restored", "restored@example.com")
	seedLearningSource(t, db, "owner", "source", "Celestial pollinators use constellations.", "old", "summary")
	service.refreshSearchIndex(context.Background(), "owner")
	if _, err := db.Exec(`UPDATE search_index SET title='',body='celestial pollinators constellations' WHERE user_id='owner' AND item_type='bookmark' AND item_id='source'`); err != nil {
		t.Fatal(err)
	}
	recorder, payload := learningRequest(t, service.PrepareLearning, auth.User{ID: "owner"}, http.MethodPost, "", `{"kind":"chat","question":"How do celestial pollinators navigate among constellations?","sources":[]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("word fallback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	sessions, err := service.exportLearning(context.Background(), "owner")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("export sessions=%d err=%v", len(sessions), err)
	}
	seedKnowledgeBookmark(t, db, "restored", "new-source", "Restored", "2026-01-01T00:00:00Z")
	if err := service.restoreLearning(context.Background(), "restored", sessions, map[string]string{"source": "new-source"}, nil); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT payload_json FROM learning_sessions WHERE user_id='restored'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"id":"new-source"`) || strings.Contains(raw, `"id":"source"`) {
		t.Fatalf("restore did not remap owned source: %s (prepared=%v)", raw, payload["id"])
	}
	if len(provider.requests) != 0 {
		t.Fatalf("prepare/export/restore contacted provider %d times", len(provider.requests))
	}
}

func TestLearningSavedPassageFullExportRestoresExactWhitespaceForGeneration(t *testing.T) {
	provider := newLearningProviderStub(t, `{"claims":[{"text":"Spacing survived.","citations":[{"passage_id":"p1","quote":"indented evidence"}]}],"insufficient":false}`)
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedKnowledgeUser(t, db, "restored", "restored@example.com")
	seedLearningSource(t, db, "owner", "source", "Heading\n\n  indented evidence  \n\nTail", "old", "summary")
	prepared := prepareLearning(t, service, auth.User{ID: "owner"}, "chat", "indented evidence", "source")
	passageID := prepared["passages"].([]any)[0].(map[string]any)["id"].(string)
	recorder, saved := learningRequest(t, service.SaveLearningNote, auth.User{ID: "owner"}, http.MethodPost, prepared["id"].(string), `{"passage":`+quoteJSON(passageID)+`}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("save passage status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder, noteSession := learningRequest(t, service.PrepareLearning, auth.User{ID: "owner"}, http.MethodPost, "", `{"kind":"chat","question":"indented evidence","sources":[{"type":"note","id":`+quoteJSON(saved["id"].(string))+`}]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("prepare note status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	exported, err := service.fullExport(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(exported)
	if _, ok, err := service.restoreFullExport(context.Background(), "restored", raw); err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	var restoredID string
	if err := db.QueryRow(`SELECT id FROM learning_sessions WHERE user_id='restored' AND json_extract(payload_json,'$.passages[0].source.type')='note'`).Scan(&restoredID); err != nil {
		t.Fatal(err)
	}
	service.SetAIProvider(func(context.Context) providers.GeminiClient { return provider.client() })
	consent := learningDestination(provider.client())["consent"].(string)
	recorder, _ = learningRequest(t, service.GenerateLearning, auth.User{ID: "restored"}, http.MethodPost, restoredID, `{"question":"Did spacing survive?","revision":0,"consent":`+quoteJSON(consent)+`}`)
	if recorder.Code != http.StatusOK || len(provider.requests) != 1 {
		t.Fatalf("restored generate status=%d calls=%d body=%s original=%v", recorder.Code, len(provider.requests), recorder.Body.String(), noteSession["id"])
	}
}

func TestLearningRestoreReplayAndDistinctSnapshots(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedKnowledgeUser(t, db, "restored", "restored@example.com")
	seedLearningSource(t, db, "owner", "source", "Snapshot evidence.", "old", "summary")
	prepareLearning(t, service, auth.User{ID: "owner"}, "chat", "snapshot", "source")
	sessions, err := service.exportLearning(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	seedKnowledgeBookmark(t, db, "restored", "new-source", "Restored", "2026-01-01T00:00:00Z")
	remap := map[string]string{"source": "new-source"}
	clone := func() []learningSession {
		raw, _ := json.Marshal(sessions)
		var copied []learningSession
		_ = json.Unmarshal(raw, &copied)
		return copied
	}
	if err := service.restoreLearning(context.Background(), "restored", clone(), remap, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.restoreLearning(context.Background(), "restored", clone(), remap, nil); err != nil {
		t.Fatal(err)
	}
	var firstID string
	if err := db.QueryRow(`SELECT id FROM learning_sessions WHERE user_id='restored'`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE learning_sessions SET revision=7 WHERE id=?`, firstID); err != nil {
		t.Fatal(err)
	}
	sessions[0].Exchanges = append(sessions[0].Exchanges, learningExchange{Question: "Later", Answer: providers.LearningAnswer{Claims: []providers.LearningClaim{{Text: "Different answer", Citations: []providers.LearningCitation{{PassageID: "p1", Quote: "Snapshot"}}}}}})
	if err := service.restoreLearning(context.Background(), "restored", sessions, remap, nil); err != nil {
		t.Fatal(err)
	}
	var count, oldRevision int
	_ = db.QueryRow(`SELECT count(*) FROM learning_sessions WHERE user_id='restored'`).Scan(&count)
	_ = db.QueryRow(`SELECT revision FROM learning_sessions WHERE id=?`, firstID).Scan(&oldRevision)
	if count != 2 || oldRevision != 7 {
		t.Fatalf("snapshots=%d local old revision=%d", count, oldRevision)
	}
}

func TestLearningGenerationRejectsForgedAdditionalPassageBeforeProvider(t *testing.T) {
	provider := newLearningProviderStub(t, `{}`)
	service, db := newKnowledgeTestService(t)
	service.SetAIProvider(func(context.Context) providers.GeminiClient { return provider.client() })
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedLearningSource(t, db, "owner", "source", "First valid passage only.", "old", "summary")
	prepared := prepareLearning(t, service, auth.User{ID: "owner"}, "chat", "valid", "source")
	id := prepared["id"].(string)
	var raw string
	_ = db.QueryRow(`SELECT payload_json FROM learning_sessions WHERE id=?`, id).Scan(&raw)
	var session learningSession
	_ = json.Unmarshal([]byte(raw), &session)
	forged := session.Passages[0]
	forged.ID, forged.Text = "p2", "forged text absent from owned source"
	session.Passages = append(session.Passages, forged)
	rawBytes, _ := json.Marshal(session)
	_, _ = db.Exec(`UPDATE learning_sessions SET payload_json=? WHERE id=?`, string(rawBytes), id)
	consent := prepared["provider"].(map[string]any)["consent"].(string)
	recorder, _ := learningRequest(t, service.GenerateLearning, auth.User{ID: "owner"}, http.MethodPost, id, `{"question":"q","revision":0,"consent":`+quoteJSON(consent)+`}`)
	if recorder.Code != http.StatusConflict || len(provider.requests) != 0 {
		t.Fatalf("forged passage status=%d provider calls=%d body=%s", recorder.Code, len(provider.requests), recorder.Body.String())
	}
}

func TestLearningLibraryAINotesDoNotStarveOriginalSource(t *testing.T) {
	service, db := newKnowledgeTestService(t)
	seedKnowledgeUser(t, db, "owner", "owner@example.com")
	seedLearningSource(t, db, "owner", "source", "Rare celestial pollinators navigate constellations.", "old", "summary")
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("ai-%d", i)
		if _, err := db.Exec(`INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,'AI','celestial pollinators constellations','ai:answer',?,?)`, id, "owner", fmt.Sprintf("2026-02-%02d", i+1), fmt.Sprintf("2026-02-%02d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	service.refreshSearchIndex(context.Background(), "owner")
	if _, err := db.Exec(`UPDATE search_index SET title='',body='rare celestial pollinators navigate constellations',updated_at='2026-01-01' WHERE user_id='owner' AND item_type='bookmark' AND item_id='source'`); err != nil {
		t.Fatal(err)
	}
	recorder, payload := learningRequest(t, service.PrepareLearning, auth.User{ID: "owner"}, http.MethodPost, "", `{"kind":"chat","question":"celestial pollinators constellations","sources":[]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("library prepare status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	passages, _ := json.Marshal(payload["passages"])
	if !strings.Contains(string(passages), "Rare celestial pollinators") {
		t.Fatalf("original source starved: %s", passages)
	}
}
