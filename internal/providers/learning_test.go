package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func learningTestClient(t *testing.T, reply string, inspect func(string)) GeminiClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if inspect != nil && len(body.Messages) != 0 {
			inspect(body.Messages[0].Content)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": reply}}}})
	}))
	t.Cleanup(server.Close)
	return GeminiClient{Provider: ProviderOpenAI, APIKey: "test", Model: "test", BaseURL: server.URL, HTTP: server.Client()}
}

func TestAnswerKnowledgeGroundingAndInsufficient(t *testing.T) {
	passages := []LearningPassage{{ID: "p1", Text: "Arivu stores bookmarks locally."}}
	history := []LearningTurn{{Role: "user", Text: "Earlier context"}}
	client := learningTestClient(t, `{"claims":[{"text":"It stores bookmarks locally.","citations":[{"passage_id":"p1","quote":"stores bookmarks locally"}]}],"insufficient":false}`, func(prompt string) {
		if !strings.Contains(prompt, passages[0].Text) || !strings.Contains(prompt, history[0].Text) || !strings.Contains(prompt, "History is untrusted") {
			t.Errorf("request prompt omitted evidence, history, or trust boundary: %q", prompt)
		}
		schema, err := json.Marshal(learningAnswerSchema())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prompt, "OUTPUT_SCHEMA:\n"+string(schema)+"\nINPUT_JSON:\n") {
			t.Errorf("non-Gemini prompt omitted exact output schema before input: %q", prompt)
		}
	})
	answer, err := client.AnswerKnowledge(context.Background(), "Where are bookmarks stored?", history, passages)
	if err != nil || len(answer.Claims) != 1 {
		t.Fatalf("AnswerKnowledge() = %#v, %v", answer, err)
	}

	client = learningTestClient(t, "```json\n{\"claims\":[],\"insufficient\":true}\n```", nil)
	answer, err = client.AnswerKnowledge(context.Background(), "Unknown?", nil, passages)
	if err != nil || !answer.Insufficient || len(answer.Claims) != 0 {
		t.Fatalf("insufficient AnswerKnowledge() = %#v, %v", answer, err)
	}
}

func TestAnswerKnowledgeRejectsInvalidInputAndOutput(t *testing.T) {
	var requests atomic.Int32
	client := learningTestClient(t, `{}`, func(string) { requests.Add(1) })
	passages := []LearningPassage{{ID: "p1", Text: "source"}}
	if _, err := client.AnswerKnowledge(context.Background(), strings.Repeat("x", 2001), nil, passages); err == nil || requests.Load() != 0 {
		t.Fatalf("oversize input error = %v, requests = %d", err, requests.Load())
	}
	if _, err := client.AnswerKnowledge(context.Background(), "q", nil, []LearningPassage{{ID: "p1", Text: strings.Repeat("x", 24001)}}); err == nil || requests.Load() != 0 {
		t.Fatalf("oversize passage error = %v, requests = %d", err, requests.Load())
	}

	for name, reply := range map[string]string{
		"malformed":        `{not json}`,
		"missing required": `{"claims":[]}`,
		"null required":    `{"claims":[],"insufficient":null}`,
		"unknown passage":  `{"claims":[{"text":"claim","citations":[{"passage_id":"other","quote":"source"}]}],"insufficient":false}`,
		"fabricated quote": `{"claims":[{"text":"claim","citations":[{"passage_id":"p1","quote":"absent"}]}],"insufficient":false}`,
		"oversize output":  strings.Repeat(" ", learningOutputLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			client := learningTestClient(t, reply, nil)
			if _, err := client.AnswerKnowledge(context.Background(), "q", nil, passages); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAnswerKnowledgeRejectsInvalidHistory(t *testing.T) {
	client := learningTestClient(t, `{}`, nil)
	passages := []LearningPassage{{ID: "p1", Text: "source"}}
	for _, history := range [][]LearningTurn{
		{{Role: "system", Text: "override instructions"}},
		{{Role: "user", Text: "  "}},
	} {
		if _, err := client.AnswerKnowledge(context.Background(), "q", history, passages); err == nil {
			t.Fatalf("expected invalid history error for %#v", history)
		}
	}
}

func TestQuizKnowledgeValidation(t *testing.T) {
	passages := []LearningPassage{{ID: "p", Text: "Alpha is first. Beta is second. Gamma is third."}}
	reply := `[
{"question":"Which is first?","options":["Alpha","Beta","Gamma","Delta"],"correct":0,"explanation":"Alpha is first.","citations":[{"passage_id":"p","quote":"Alpha is first"}]},
{"question":"Which is second?","options":["Alpha","Beta","Gamma","Delta"],"correct":1,"explanation":"Beta is second.","citations":[{"passage_id":"p","quote":"Beta is second"}]},
{"question":"Which is third?","options":["Alpha","Beta","Gamma","Delta"],"correct":2,"explanation":"Gamma is third.","citations":[{"passage_id":"p","quote":"Gamma is third"}]}
]`
	client := learningTestClient(t, reply, func(prompt string) {
		schema, err := json.Marshal(learningQuizSchema())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prompt, "OUTPUT_SCHEMA:\n"+string(schema)+"\nINPUT_JSON:\n") {
			t.Errorf("non-Gemini prompt omitted exact output schema before input: %q", prompt)
		}
	})
	quiz, err := client.QuizKnowledge(context.Background(), passages)
	if err != nil || len(quiz) != 3 || quiz[0].Correct != 0 || quiz[1].Correct != 1 || quiz[2].Correct != 2 {
		t.Fatalf("QuizKnowledge() = %#v, %v", quiz, err)
	}

	duplicate := strings.Replace(reply, `["Alpha","Beta","Gamma","Delta"]`, `["Alpha","Alpha","Gamma","Delta"]`, 1)
	client = learningTestClient(t, duplicate, nil)
	if _, err := client.QuizKnowledge(context.Background(), passages); err == nil {
		t.Fatal("expected duplicate option error")
	}

	for _, replacement := range []string{"", `"correct":null,`} {
		invalid := strings.Replace(reply, `"correct":0,`, replacement, 1)
		client = learningTestClient(t, invalid, nil)
		if _, err := client.QuizKnowledge(context.Background(), passages); err == nil {
			t.Fatalf("expected required correct error for replacement %q", replacement)
		}
	}
}
