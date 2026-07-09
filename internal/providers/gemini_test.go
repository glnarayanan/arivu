package providers

import (
	"bytes"
	"context"
	"encoding/json"
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
					"text": `{"one_sentence":"Specific result.","long_form":"Two useful paragraphs.","highlights":["first","second"],"suggested_tags":["research","go"]}`,
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
	if fields["one_sentence"] != "Specific result." || len(fields["highlights"].([]any)) != 2 || len(fields["suggested_tags"].([]any)) != 2 {
		t.Fatalf("unexpected parsed fields: %#v", fields)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
