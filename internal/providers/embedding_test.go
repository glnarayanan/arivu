package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type embeddingRequest struct {
	Path   string
	Auth   string
	Body   map[string]any
	Expiry time.Duration
}

func embeddingServer(t *testing.T, status int, response any) (*httptest.Server, *embeddingRequest) {
	t.Helper()
	got := &embeddingRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Path = r.URL.Path
		got.Auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if raw, ok := response.(string); ok {
			_, _ = w.Write([]byte(raw))
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	return server, got
}

func TestOpenAICompatibleEmbeddingRequestShape(t *testing.T) {
	server, got := embeddingServer(t, http.StatusOK, map[string]any{"data": []map[string]any{{"embedding": []float64{0.25, -0.5, 1}}}})
	var recorded []string
	client := EmbeddingClient{
		Config:   ResolveEmbedding(TextProviderSettings{Provider: ProviderDeepSeek, APIKey: "text-key"}, EmbeddingSelection{Provider: ProviderOpenAI, APIKey: "embed-key", BaseURL: server.URL + "/v1", Dimensions: 256}),
		Recorder: func(operation string, err error) { recorded = append(recorded, operation) },
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if deadline, ok := r.Context().Deadline(); ok {
				got.Expiry = time.Until(deadline)
			}
			return http.DefaultTransport.RoundTrip(r)
		})},
	}
	values, err := client.GenerateEmbedding(context.Background(), "Some saved article text")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || values[1] != -0.5 {
		t.Fatalf("values = %#v", values)
	}
	if got.Path != "/v1/embeddings" || got.Auth != "Bearer embed-key" {
		t.Fatalf("path=%q auth=%q", got.Path, got.Auth)
	}
	if got.Body["model"] != "text-embedding-3-small" || got.Body["input"] != "Some saved article text" || got.Body["dimensions"] != float64(256) {
		t.Fatalf("body = %#v", got.Body)
	}
	if got.Expiry <= 0 || got.Expiry > EmbeddingRequestTimeout {
		t.Fatalf("deadline remaining = %s", got.Expiry)
	}
	if len(recorded) != 1 || recorded[0] != "embedding" {
		t.Fatalf("recorded = %#v", recorded)
	}
	if client.Space() != "openai/text-embedding-3-small@256" {
		t.Fatalf("space = %q", client.Space())
	}
}

func TestEmbeddingDimensionsParameterFollowsProvider(t *testing.T) {
	server, got := embeddingServer(t, http.StatusOK, map[string]any{"data": []map[string]any{{"embedding": []float64{1, 2}}}})
	mistral := EmbeddingClient{Config: ResolveEmbedding(TextProviderSettings{}, EmbeddingSelection{Provider: ProviderMistral, APIKey: "k", Model: "codestral-embed", BaseURL: server.URL, Dimensions: 512})}
	if _, err := mistral.GenerateEmbedding(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}
	if got.Body["output_dimension"] != float64(512) || got.Body["dimensions"] != nil {
		t.Fatalf("mistral body = %#v", got.Body)
	}
	lmstudio := EmbeddingClient{Config: ResolveEmbedding(TextProviderSettings{}, EmbeddingSelection{Provider: ProviderLMStudio, Model: "nomic", BaseURL: server.URL, Dimensions: 512})}
	if lmstudio.Config.Dimensions != 0 {
		t.Fatalf("lmstudio dimensions should be dropped, got %d", lmstudio.Config.Dimensions)
	}
	if _, err := lmstudio.GenerateEmbedding(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Body["dimensions"]; ok || got.Auth != "" {
		t.Fatalf("lmstudio body=%#v auth=%q", got.Body, got.Auth)
	}
}

func TestPerplexityEmbeddingsDecodeBase64Int8(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte{0x01, 0xff, 0x7f, 0x80})
	server, got := embeddingServer(t, http.StatusOK, map[string]any{"data": []map[string]any{{"embedding": encoded}}})
	resolved := ResolveEmbedding(TextProviderSettings{Provider: ProviderPerplexity, APIKey: "pplx", BaseURL: "https://api.perplexity.ai"}, EmbeddingSelection{Provider: EmbeddingProviderSame})
	if resolved.BaseURL != "https://api.perplexity.ai/v1" || resolved.Model != "pplx-embed-v1-0.6b" {
		t.Fatalf("resolved = %#v", resolved)
	}
	resolved.BaseURL = server.URL + "/v1"
	values, err := EmbeddingClient{Config: resolved}.GenerateEmbedding(context.Background(), "text")
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{1, -1, 127, -128}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("values = %#v", values)
		}
	}
	if got.Path != "/v1/embeddings" {
		t.Fatalf("path = %q", got.Path)
	}
	if _, err := decodeEmbeddingValues(json.RawMessage(`"AQID"`), false); err == nil {
		t.Fatal("string vectors must be rejected for float providers")
	}
}

func TestEmbeddingErrorsMapToProviderCodes(t *testing.T) {
	cases := []struct {
		status   int
		response any
		code     string
	}{
		{http.StatusUnauthorized, map[string]any{"error": "bad key"}, ErrorProviderAuth},
		{http.StatusTooManyRequests, map[string]any{}, ErrorProviderRateLimited},
		{http.StatusBadRequest, map[string]any{}, ErrorProviderRejected},
		{http.StatusServiceUnavailable, map[string]any{}, ErrorProviderUnavailable},
		{http.StatusOK, `{"data":[]}`, ErrorProviderInvalidResponse},
		{http.StatusOK, `not json`, ErrorProviderInvalidResponse},
	}
	for _, tc := range cases {
		server, _ := embeddingServer(t, tc.status, tc.response)
		var recordedErr error
		client := EmbeddingClient{
			Config:   ResolveEmbedding(TextProviderSettings{}, EmbeddingSelection{Provider: ProviderCustom, Model: "embed", BaseURL: server.URL}),
			Recorder: func(_ string, err error) { recordedErr = err },
		}
		_, err := client.GenerateEmbedding(context.Background(), "text")
		if code := SafeErrorCode(err); code != tc.code {
			t.Fatalf("status %d: code = %q (%v)", tc.status, code, err)
		}
		if recordedErr == nil {
			t.Fatalf("status %d: usage recorder did not see the error", tc.status)
		}
	}
}

func TestEmbeddingNotConfiguredNamesTheEmbeddingModel(t *testing.T) {
	var recordedErr error
	client := EmbeddingClient{
		Config:   ResolveEmbedding(TextProviderSettings{Provider: ProviderDeepSeek, APIKey: "deepseek"}, EmbeddingSelection{Provider: EmbeddingProviderSame}),
		Recorder: func(_ string, err error) { recordedErr = err },
	}
	if client.Configured() || client.Space() != "" {
		t.Fatalf("DeepSeek must not be embedding-capable: %#v", client.Config)
	}
	_, err := client.GenerateEmbedding(context.Background(), "text")
	if !errors.Is(err, ErrNotConfigured) || !IsEmbeddingNotConfigured(err) {
		t.Fatalf("err = %v", err)
	}
	if SafeErrorCode(recordedErr) != ErrorEmbeddingNotConfigured {
		t.Fatalf("code = %q", SafeErrorCode(recordedErr))
	}
	if message := SafeErrorMessage(err); !strings.Contains(message, "embedding model is not configured") {
		t.Fatalf("message = %q", message)
	}
	if reason := client.Config.ReasonMessage(); !strings.Contains(reason, "DeepSeek") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestResolveEmbeddingChoices(t *testing.T) {
	gemini := ResolveEmbedding(TextProviderSettings{Provider: ProviderGemini, APIKey: "g", BaseURL: "https://generativelanguage.googleapis.com"}, EmbeddingSelection{})
	if !gemini.Configured() || gemini.Mode != EmbeddingProviderSame || gemini.Space() != "gemini/gemini-embedding-2@768" {
		t.Fatalf("gemini = %#v space=%q", gemini, gemini.Space())
	}
	openrouter := ResolveEmbedding(TextProviderSettings{Provider: ProviderOpenRouter, APIKey: "or", BaseURL: "https://openrouter.ai/api/v1"}, EmbeddingSelection{Provider: "same"})
	if !openrouter.Configured() || openrouter.APIKey != "or" || openrouter.Model != "openai/text-embedding-3-small" || openrouter.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("openrouter = %#v", openrouter)
	}
	proxied := ResolveEmbedding(TextProviderSettings{Provider: ProviderOpenAI, APIKey: "k", BaseURL: "https://gateway.example/v1"}, EmbeddingSelection{})
	if proxied.BaseURL != "https://gateway.example/v1" {
		t.Fatalf("customized text base URL should be reused, got %q", proxied.BaseURL)
	}
	if missing := ResolveEmbedding(TextProviderSettings{Provider: ProviderDeepSeek, APIKey: "d"}, EmbeddingSelection{Provider: ProviderOpenAI}); missing.Reason != EmbeddingReasonMissingAPIKey {
		t.Fatalf("separate provider must not borrow the text key: %#v", missing)
	}
	if local := ResolveEmbedding(TextProviderSettings{}, EmbeddingSelection{Provider: ProviderOllama}); !local.Configured() || local.Model != "embeddinggemma" || local.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("ollama = %#v", local)
	}
	if lm := ResolveEmbedding(TextProviderSettings{}, EmbeddingSelection{Provider: ProviderLMStudio}); lm.Reason != EmbeddingReasonMissingModel {
		t.Fatalf("lmstudio = %#v", lm)
	}
	if custom := ResolveEmbedding(TextProviderSettings{}, EmbeddingSelection{Provider: ProviderCustom, Model: "m"}); custom.Reason != EmbeddingReasonMissingBaseURL {
		t.Fatalf("custom = %#v", custom)
	}
	if none := ResolveEmbedding(TextProviderSettings{Provider: ProviderGemini, APIKey: "g"}, EmbeddingSelection{Provider: "none"}); none.Configured() || none.Reason != EmbeddingReasonDisabled {
		t.Fatalf("none = %#v", none)
	}
	if xai := ResolveEmbedding(TextProviderSettings{}, EmbeddingSelection{Provider: ProviderXAI, APIKey: "x", Model: "m"}); xai.Reason != EmbeddingReasonProviderNoAPI {
		t.Fatalf("xai = %#v", xai)
	}
}

func TestEmbeddingCapabilityMatrix(t *testing.T) {
	capable := map[string]bool{
		ProviderOpenAI: true, ProviderOpenRouter: true, ProviderGemini: true, ProviderMistral: true,
		ProviderFireworks: true, ProviderPerplexity: true, ProviderLMStudio: true, ProviderOllama: true, ProviderCustom: true,
	}
	for _, provider := range ModelProviders() {
		if provider.SupportsEmbeddings() != capable[provider.ID] {
			t.Errorf("%s embeddings = %v", provider.ID, provider.SupportsEmbeddings())
		}
		if !provider.SupportsEmbeddings() && provider.Embedding.Note == "" {
			t.Errorf("%s is text-only but has no explanation", provider.ID)
		}
	}
}

func TestTruncateUTF8KeepsRuneBoundaries(t *testing.T) {
	text := strings.Repeat("é", 10)
	cut := truncateUTF8(text, 5)
	if cut != "éé" {
		t.Fatalf("cut = %q", cut)
	}
}
