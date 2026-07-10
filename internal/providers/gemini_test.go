package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGeminiSummaryFieldsUseConfiguredModelAndBaseURL(t *testing.T) {
	var gotPath string
	var gotPrompt string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		var body struct {
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotPrompt = body.Contents[0].Parts[0].Text
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{
					"text": `{"one_sentence":"Specific result.","long_form":"Two useful paragraphs.","bullet_points":["key point"],"highlights":["first","second"],"suggested_tags":["research","go"]}`,
				}}},
			}},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(&buf),
		}, nil
	})}

	gemini := GeminiClient{APIKey: "secret", Model: "gemini-test", BaseURL: "https://gemini.test", HTTP: client}
	longArticle := "Clean article text " + strings.Repeat("detail ", 2200) + "tail-marker"
	fields, err := gemini.GenerateSummaryFields(context.Background(), longArticle)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1beta/models/gemini-test:generateContent" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotPrompt, "Clean article text") {
		t.Fatalf("prompt did not include article text: %q", gotPrompt)
	}
	if !strings.Contains(gotPrompt, "tail-marker") {
		t.Fatalf("summary prompt was truncated before article tail")
	}
	if !strings.Contains(gotPrompt, "100-150 word executive briefing") {
		t.Fatalf("summary prompt did not request the executive briefing format: %q", gotPrompt)
	}
	if fields["one_sentence"] != "Specific result." || len(fields["bullet_points"].([]any)) != 1 || len(fields["highlights"].([]any)) != 2 || len(fields["suggested_tags"].([]any)) != 2 {
		t.Fatalf("unexpected parsed fields: %#v", fields)
	}
}

func TestGeminiEmbeddingUsesCurrentModel(t *testing.T) {
	var gotPath string
	var gotBody struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		OutputDimensionality int `json:"output_dimensionality"`
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]any{
			"embeddings": []map[string]any{{"values": []float64{0.1, 0.2, 0.3}}},
		})
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(&buf)}, nil
	})}

	gemini := GeminiClient{APIKey: "secret", BaseURL: "https://gemini.test", HTTP: client}
	values, err := gemini.GenerateEmbedding(context.Background(), "Useable article text.")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1beta/models/gemini-embedding-2:embedContent" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody.Content.Parts[0].Text != "Useable article text." || gotBody.OutputDimensionality != embeddingDimensions {
		t.Fatalf("request body = %#v", gotBody)
	}
	if len(values) != 3 {
		t.Fatalf("embedding dimensions = %d", len(values))
	}
}

func TestOpenAICompatibleInsightUsesChatCompletions(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotModel string
	var gotPrompt string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel = body.Model
		if len(body.Messages) != 1 || body.Messages[0].Role != "user" {
			t.Fatalf("unexpected messages: %#v", body.Messages)
		}
		gotPrompt = body.Messages[0].Content
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": "provider result"},
			}},
		})
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(&buf)}, nil
	})}

	ai := GeminiClient{Provider: ProviderOpenAI, APIKey: "secret", Model: "test-model", BaseURL: "https://openai.test/v1/", HTTP: client}
	result, err := ai.GenerateInsight(context.Background(), "Summarize this.")
	if err != nil {
		t.Fatal(err)
	}
	if result != "provider result" {
		t.Fatalf("result = %q", result)
	}
	if gotPath != "/v1/chat/completions" || gotAuth != "Bearer secret" || gotModel != "test-model" || gotPrompt != "Summarize this." {
		t.Fatalf("unexpected openai-compatible request path=%q auth=%q model=%q prompt=%q", gotPath, gotAuth, gotModel, gotPrompt)
	}
}

func TestKeylessOllamaInsightUsesChatCompletions(t *testing.T) {
	var gotAuth string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": "local result"},
			}},
		})
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(&buf)}, nil
	})}

	ai := GeminiClient{Provider: ProviderOllama, Model: "llama3.2", HTTP: client}
	result, err := ai.GenerateInsight(context.Background(), "Use the local model.")
	if err != nil {
		t.Fatal(err)
	}
	if result != "local result" || gotAuth != "" {
		t.Fatalf("unexpected keyless result=%q auth=%q", result, gotAuth)
	}
}

func TestProviderClientRejectsRedirects(t *testing.T) {
	client := GeminiClient{}.httpClient()
	redirect, err := http.NewRequest(http.MethodGet, "https://redirected.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(redirect, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy error = %v", err)
	}

	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	ai := GeminiClient{Provider: ProviderAnthropic, APIKey: "do-not-forward", Model: "claude-test", BaseURL: "https://provider.example.test", HTTP: client}
	_, err = ai.GenerateInsight(context.Background(), "Do not follow redirects.")
	if err == nil || !strings.Contains(err.Error(), "status 307") {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestAnthropicInsightUsesMessagesAPI(t *testing.T) {
	var gotPath string
	var gotKey string
	var gotVersion string
	var gotModel string
	var gotMaxTokens int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Messages  []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel = body.Model
		gotMaxTokens = body.MaxTokens
		if len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "Find the insight." {
			t.Fatalf("unexpected messages: %#v", body.Messages)
		}
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "anthropic result"}},
		})
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(&buf)}, nil
	})}

	ai := GeminiClient{Provider: ProviderAnthropic, APIKey: "secret", Model: "claude-test", BaseURL: "https://anthropic.test", HTTP: client}
	result, err := ai.GenerateInsight(context.Background(), "Find the insight.")
	if err != nil {
		t.Fatal(err)
	}
	if result != "anthropic result" {
		t.Fatalf("result = %q", result)
	}
	if gotPath != "/v1/messages" || gotKey != "secret" || gotVersion != "2023-06-01" || gotModel != "claude-test" || gotMaxTokens != 4096 {
		t.Fatalf("unexpected anthropic request path=%q key=%q version=%q model=%q max_tokens=%d", gotPath, gotKey, gotVersion, gotModel, gotMaxTokens)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
