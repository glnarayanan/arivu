package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/glnarayanan/arivu/internal/config"
)

const (
	// EmbeddingProviderSame reuses the text-generation provider, key, and base
	// URL for embeddings when that provider offers an embeddings API.
	EmbeddingProviderSame = "same"
	// EmbeddingProviderNone disables embeddings explicitly.
	EmbeddingProviderNone = "none"

	// MaxEmbeddingDimensions bounds administrator input; no supported model
	// produces larger vectors.
	MaxEmbeddingDimensions = 8192

	embeddingInputLimit = 12000
)

// Reasons an embedding configuration cannot be used. They are stable codes
// exposed through the admin API so the UI can explain what is missing.
const (
	EmbeddingReasonDisabled       = "disabled"
	EmbeddingReasonTextOnly       = "text_provider_has_no_embeddings"
	EmbeddingReasonProviderNoAPI  = "provider_has_no_embeddings"
	EmbeddingReasonMissingAPIKey  = "missing_api_key"
	EmbeddingReasonMissingModel   = "missing_model"
	EmbeddingReasonMissingBaseURL = "missing_base_url"
)

// ErrEmbeddingNotConfigured wraps ErrNotConfigured so existing checks keep
// working while diagnostics can say that the embedding model, rather than the
// text-generation provider, is missing.
var ErrEmbeddingNotConfigured = fmt.Errorf("embedding model not configured: %w", ErrNotConfigured)

// EmbeddingSelection is the administrator's embedding choice before it is
// resolved against the text-generation provider.
type EmbeddingSelection struct {
	Provider   string
	APIKey     string
	Model      string
	BaseURL    string
	Dimensions int
}

// TextProviderSettings is the subset of the text-generation configuration that
// "same as text generation" embeddings reuse.
type TextProviderSettings struct {
	Provider string
	APIKey   string
	BaseURL  string
}

// EmbeddingConfig is a resolved embedding configuration. Reason is empty when
// the configuration is usable.
type EmbeddingConfig struct {
	Mode       string // "same", "separate", or "none"
	Provider   string
	APIKey     string
	Model      string
	BaseURL    string
	Dimensions int
	Reason     string
}

// NormalizeEmbeddingProvider maps the stored embedding provider value to
// "same", "none", or a preset id.
func NormalizeEmbeddingProvider(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", "same", "text", "same-as-text":
		return EmbeddingProviderSame
	case "none", "off", "disabled":
		return EmbeddingProviderNone
	}
	return NormalizeModelProvider(normalized)
}

// ResolveEmbedding turns the administrator's selection into a concrete
// embedding configuration.
func ResolveEmbedding(text TextProviderSettings, selection EmbeddingSelection) EmbeddingConfig {
	mode := NormalizeEmbeddingProvider(selection.Provider)
	if mode == EmbeddingProviderNone {
		return EmbeddingConfig{Mode: EmbeddingProviderNone, Reason: EmbeddingReasonDisabled}
	}
	resolved := EmbeddingConfig{Mode: "separate", Provider: mode, APIKey: strings.TrimSpace(selection.APIKey), Model: strings.TrimSpace(selection.Model), BaseURL: strings.TrimRight(strings.TrimSpace(selection.BaseURL), "/"), Dimensions: selection.Dimensions}
	if mode == EmbeddingProviderSame {
		resolved.Mode = EmbeddingProviderSame
		resolved.Provider = NormalizeModelProvider(text.Provider)
		resolved.APIKey = strings.TrimSpace(text.APIKey)
		definition := ModelProviderDefinition(resolved.Provider)
		textBase := strings.TrimRight(strings.TrimSpace(text.BaseURL), "/")
		if resolved.BaseURL == "" && textBase != "" && textBase != strings.TrimRight(definition.BaseURL, "/") {
			// A customized text endpoint (proxy, self-hosted gateway, or custom
			// provider) also serves embeddings in "same" mode.
			resolved.BaseURL = textBase
		}
	}
	definition := ModelProviderDefinition(resolved.Provider)
	if !definition.SupportsEmbeddings() {
		if resolved.Mode == EmbeddingProviderSame {
			resolved.Reason = EmbeddingReasonTextOnly
		} else {
			resolved.Reason = EmbeddingReasonProviderNoAPI
		}
		return resolved
	}
	if resolved.Model == "" {
		resolved.Model = definition.Embedding.DefaultModel
	}
	if resolved.BaseURL == "" {
		resolved.BaseURL = definition.embeddingBaseURL()
	}
	if resolved.Dimensions <= 0 {
		resolved.Dimensions = definition.Embedding.DefaultDimensions
	}
	if definition.Embedding.DimensionsParam == "" {
		resolved.Dimensions = 0
	}
	switch {
	case resolved.APIKey == "" && !definition.APIKeyOptional:
		resolved.Reason = EmbeddingReasonMissingAPIKey
	case resolved.Model == "":
		resolved.Reason = EmbeddingReasonMissingModel
	case resolved.BaseURL == "":
		resolved.Reason = EmbeddingReasonMissingBaseURL
	}
	return resolved
}

func (p ModelProvider) embeddingBaseURL() string {
	if p.Embedding.BaseURL != "" {
		return strings.TrimRight(p.Embedding.BaseURL, "/")
	}
	if p.Embedding.Style == EmbeddingStyleGemini && p.BaseURL == "" {
		return config.DefaultGeminiBaseURL
	}
	return strings.TrimRight(p.BaseURL, "/")
}

// Configured reports whether the configuration can produce embeddings.
func (c EmbeddingConfig) Configured() bool {
	return c.Reason == "" && c.Mode != EmbeddingProviderNone && c.Provider != ""
}

// Space identifies the vector space produced by this configuration. Vectors
// are only comparable when their spaces are identical. Empty means that no
// space is available because embeddings are not configured.
func (c EmbeddingConfig) Space() string {
	if !c.Configured() {
		return ""
	}
	return EmbeddingSpace(c.Provider, c.Model, c.Dimensions)
}

// EmbeddingSpace builds the identifier stored in bookmarks.embedding_model.
// Requested dimensions are part of the identity because the same model
// truncated to a different size produces incomparable vectors.
func EmbeddingSpace(provider, model string, dimensions int) string {
	space := NormalizeModelProvider(provider) + "/" + strings.TrimSpace(model)
	if dimensions > 0 {
		space += "@" + strconv.Itoa(dimensions)
	}
	return space
}

// ReasonMessage explains an unusable embedding configuration in plain words.
func (c EmbeddingConfig) ReasonMessage() string {
	definition := ModelProviderDefinition(c.Provider)
	switch c.Reason {
	case "":
		return ""
	case EmbeddingReasonDisabled:
		return "Embeddings are turned off."
	case EmbeddingReasonTextOnly:
		message := "The text generation provider (" + definition.Name + ") has no embeddings API, so an embedding model must be configured separately."
		if definition.Embedding.Note != "" {
			message += " " + definition.Embedding.Note
		}
		return message
	case EmbeddingReasonProviderNoAPI:
		message := definition.Name + " has no embeddings API."
		if definition.Embedding.Note != "" {
			message = definition.Embedding.Note
		}
		return message
	case EmbeddingReasonMissingAPIKey:
		if c.Mode == EmbeddingProviderSame {
			return "Embeddings use the text generation provider (" + definition.Name + "), which has no API key configured."
		}
		return "The embedding provider (" + definition.Name + ") needs an API key."
	case EmbeddingReasonMissingModel:
		return "Choose an embedding model id for " + definition.Name + "."
	case EmbeddingReasonMissingBaseURL:
		return "The embedding provider needs a base URL."
	default:
		return "The embedding model is not configured."
	}
}

// EmbeddingClient generates text embeddings with Gemini's native API or any
// OpenAI-compatible POST {base}/embeddings endpoint.
type EmbeddingClient struct {
	Config   EmbeddingConfig
	HTTP     *http.Client
	Recorder func(operation string, err error)
}

func (c EmbeddingClient) Configured() bool { return c.Config.Configured() }

func (c EmbeddingClient) Space() string { return c.Config.Space() }

// GenerateEmbedding returns one vector for text. Errors are classified by
// SafeErrorCode; an unusable configuration returns ErrEmbeddingNotConfigured.
func (c EmbeddingClient) GenerateEmbedding(ctx context.Context, text string) (values []float64, err error) {
	defer func() {
		if c.Recorder != nil {
			c.Recorder("embedding", err)
		}
	}()
	if !c.Config.Configured() {
		return nil, ErrEmbeddingNotConfigured
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("embedding text is required")
	}
	if len(text) > embeddingInputLimit {
		text = truncateUTF8(text, embeddingInputLimit)
	}
	ctx, cancel := context.WithTimeout(ctx, EmbeddingRequestTimeout)
	defer cancel()
	definition := ModelProviderDefinition(c.Config.Provider)
	if definition.Embedding.Style == EmbeddingStyleGemini {
		values, err = c.generateGemini(ctx, text)
	} else {
		values, err = c.generateOpenAICompatible(ctx, definition.Embedding, text)
	}
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("embedding provider returned no content")
	}
	return values, nil
}

func (c EmbeddingClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTPClient
}

func (c EmbeddingClient) generateGemini(ctx context.Context, text string) ([]float64, error) {
	body := map[string]any{
		"content": map[string]any{
			"parts": []map[string]string{{"text": text}},
		},
	}
	if c.Config.Dimensions > 0 {
		body["output_dimensionality"] = c.Config.Dimensions
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}
	model := strings.TrimPrefix(c.Config.Model, "models/")
	endpoint := strings.TrimRight(c.Config.BaseURL, "/") + "/v1beta/models/" + model + ":embedContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.Config.APIKey)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gemini embedding status %d", resp.StatusCode)
	}
	var decoded struct {
		Embedding struct {
			Values []float64 `json:"values"`
		} `json:"embedding"`
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(decoded.Embedding.Values) > 0 {
		return decoded.Embedding.Values, nil
	}
	if len(decoded.Embeddings) > 0 && len(decoded.Embeddings[0].Values) > 0 {
		return decoded.Embeddings[0].Values, nil
	}
	return nil, fmt.Errorf("gemini returned no embedding content")
}

func (c EmbeddingClient) generateOpenAICompatible(ctx context.Context, support EmbeddingSupport, text string) ([]float64, error) {
	body := map[string]any{
		"model": c.Config.Model,
		"input": text,
	}
	if c.Config.Dimensions > 0 && support.DimensionsParam != "" {
		body[support.DimensionsParam] = c.Config.Dimensions
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Config.BaseURL, "/")+"/embeddings", &buf)
	if err != nil {
		return nil, err
	}
	if c.Config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.Config.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding provider status %d", resp.StatusCode)
	}
	var decoded struct {
		Data []struct {
			Embedding json.RawMessage `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding provider returned no content")
	}
	return decodeEmbeddingValues(decoded.Data[0].Embedding, support.Base64Int8)
}

// decodeEmbeddingValues accepts a JSON float array or, for providers that
// only return quantized vectors, a base64 string of signed int8 values.
func decodeEmbeddingValues(raw json.RawMessage, base64Int8 bool) ([]float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		if !base64Int8 {
			return nil, fmt.Errorf("embedding provider returned a malformed vector")
		}
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return nil, fmt.Errorf("embedding provider returned a malformed vector")
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(decoded) == 0 {
			return nil, fmt.Errorf("embedding provider returned a malformed vector")
		}
		values := make([]float64, len(decoded))
		for i, b := range decoded {
			values[i] = float64(int8(b))
		}
		return values, nil
	}
	var values []float64
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, fmt.Errorf("embedding provider returned a malformed vector")
	}
	return values, nil
}

func truncateUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && cut < len(text) && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut]
}

// IsEmbeddingNotConfigured reports whether err means no usable embedding model.
func IsEmbeddingNotConfigured(err error) bool {
	return errors.Is(err, ErrEmbeddingNotConfigured)
}
