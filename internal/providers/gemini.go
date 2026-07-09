package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/config"
)

const (
	defaultGeneratePromptLimit = 12000
	summaryGeneratePromptLimit = 50000
)

type GeminiClient struct {
	APIKey   string
	BaseURL  string
	Model    string
	Provider string
	HTTP     *http.Client
	Recorder func(operation string, err error)
}

func (c GeminiClient) GenerateSummary(ctx context.Context, text string) (string, error) {
	summary, err := c.GenerateSummaryFields(ctx, text)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stringMapValue(summary, "one_sentence")), nil
}

func (c GeminiClient) GenerateSummaryFields(ctx context.Context, text string) (map[string]any, error) {
	prompt := `You are an expert content analyst. Return only valid JSON with these keys:
- one_sentence: one precise sentence, maximum 25 words
- long_form: two concise paragraphs with the core argument, evidence, and takeaway
- highlights: array of 4 to 6 standalone insights
- suggested_tags: array of 4 to 6 lowercase hyphenated tags

Avoid generic phrases like "this article discusses". Use article-specific names, numbers, and claims when present.

ARTICLE:
` + text
	raw, err := c.generate(ctx, "summary", prompt, summaryGeneratePromptLimit)
	if err != nil {
		return nil, err
	}
	return parseSummaryFields(raw)
}

func (c GeminiClient) GenerateInsight(ctx context.Context, prompt string) (string, error) {
	return c.generate(ctx, "insight", prompt, defaultGeneratePromptLimit)
}

func (c GeminiClient) ExtractImageText(ctx context.Context, mimeType string, data []byte) (result string, err error) {
	defer func() { c.record("ocr", err) }()
	if c.APIKey == "" || !c.usesGeminiNative() {
		return "", ErrNotConfigured
	}
	mimeType = strings.TrimSpace(strings.Split(mimeType, ";")[0])
	if !strings.HasPrefix(mimeType, "image/") {
		return "", fmt.Errorf("image mime type is required")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("image data is required")
	}
	if len(data) > 4<<20 {
		return "", fmt.Errorf("image is too large for OCR")
	}
	body := map[string]any{
		"contents": []map[string]any{{
			"parts": []map[string]any{
				{"text": "Extract all readable text from this image. Return only the extracted text, preserving line breaks where useful."},
				{"inline_data": map[string]string{
					"mime_type": mimeType,
					"data":      base64.StdEncoding.EncodeToString(data),
				}},
			},
		}},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return "", err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("gemini ocr status %d", resp.StatusCode)
	}
	var decoded struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if len(decoded.Candidates) == 0 || len(decoded.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned no content")
	}
	return decoded.Candidates[0].Content.Parts[0].Text, nil
}

func (c GeminiClient) GenerateEmbedding(ctx context.Context, text string, taskType string) (values []float64, err error) {
	defer func() { c.record("embedding", err) }()
	if c.APIKey == "" || !c.usesGeminiNative() {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("embedding text is required")
	}
	if len(text) > 12000 {
		text = text[:12000]
	}
	body := map[string]any{
		"model": "models/text-embedding-004",
		"content": map[string]any{
			"parts": []map[string]string{{"text": text}},
		},
	}
	if taskType != "" {
		body["taskType"] = normalizeEmbeddingTaskType(taskType)
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.embeddingEndpoint(), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
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
		return nil, err
	}
	if len(decoded.Embedding.Values) == 0 {
		if len(decoded.Embeddings) == 0 || len(decoded.Embeddings[0].Values) == 0 {
			return nil, fmt.Errorf("gemini returned no embedding")
		}
		return decoded.Embeddings[0].Values, nil
	}
	return decoded.Embedding.Values, nil
}

func (c GeminiClient) generate(ctx context.Context, operation string, prompt string, promptLimit int) (result string, err error) {
	defer func() { c.record(operation, err) }()
	if c.APIKey == "" {
		return "", ErrNotConfigured
	}
	if promptLimit > 0 && len(prompt) > promptLimit {
		prompt = prompt[:promptLimit]
	}
	switch c.provider().Style {
	case ProviderStyleAnthropic:
		return c.generateAnthropic(ctx, prompt)
	case ProviderStyleOpenAI:
		return c.generateOpenAICompatible(ctx, prompt)
	default:
		return c.generateGemini(ctx, prompt)
	}
}

func (c GeminiClient) generateGemini(ctx context.Context, prompt string) (string, error) {
	body := map[string]any{
		"contents": []map[string]any{{
			"parts": []map[string]string{{"text": prompt}},
		}},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return "", err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("gemini status %d", resp.StatusCode)
	}
	var decoded struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if len(decoded.Candidates) == 0 || len(decoded.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned no content")
	}
	return decoded.Candidates[0].Content.Parts[0].Text, nil
}

func (c GeminiClient) generateOpenAICompatible(ctx context.Context, prompt string) (string, error) {
	model := c.model()
	if model == "" {
		return "", fmt.Errorf("ai model is required")
	}
	baseURL := c.baseURL()
	if baseURL == "" {
		return "", fmt.Errorf("ai base url is required")
	}
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{{
			"role":    "user",
			"content": prompt,
		}},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("ai provider status %d", resp.StatusCode)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("ai provider returned no content")
	}
	return decoded.Choices[0].Message.Content, nil
}

func (c GeminiClient) generateAnthropic(ctx context.Context, prompt string) (string, error) {
	model := c.model()
	if model == "" {
		return "", fmt.Errorf("ai model is required")
	}
	baseURL := c.baseURL()
	if baseURL == "" {
		return "", fmt.Errorf("ai base url is required")
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": 4096,
		"messages": []map[string]string{{
			"role":    "user",
			"content": prompt,
		}},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/messages", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("anthropic status %d", resp.StatusCode)
	}
	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	for _, part := range decoded.Content {
		if strings.TrimSpace(part.Text) != "" {
			return part.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic returned no content")
}

func (c GeminiClient) record(operation string, err error) {
	if c.Recorder != nil {
		c.Recorder(operation, err)
	}
}

func (c GeminiClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c GeminiClient) provider() ModelProvider {
	return ModelProviderDefinition(c.Provider)
}

func (c GeminiClient) usesGeminiNative() bool {
	return c.provider().Style == ProviderStyleGemini
}

func (c GeminiClient) baseURL() string {
	base := strings.TrimSpace(c.BaseURL)
	if base != "" {
		return strings.TrimRight(base, "/")
	}
	return strings.TrimRight(c.provider().BaseURL, "/")
}

func (c GeminiClient) model() string {
	model := strings.TrimSpace(c.Model)
	if model != "" {
		return model
	}
	return strings.TrimSpace(c.provider().DefaultModel)
}

func (c GeminiClient) endpoint() string {
	base := c.baseURL()
	if base == "" {
		base = config.DefaultGeminiBaseURL
	}
	model := c.model()
	if model == "" {
		model = config.DefaultGeminiModel
	}
	return strings.TrimRight(base, "/") + "/v1beta/models/" + model + ":generateContent?key=" + c.APIKey
}

func (c GeminiClient) embeddingEndpoint() string {
	base := c.baseURL()
	if base == "" {
		base = config.DefaultGeminiBaseURL
	}
	return strings.TrimRight(base, "/") + "/v1beta/models/text-embedding-004:embedContent?key=" + c.APIKey
}

func normalizeEmbeddingTaskType(taskType string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(taskType), "-", "_"))
}

func parseSummaryFields(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "{"); start > 0 {
		raw = raw[start:]
	}
	if end := strings.LastIndex(raw, "}"); end >= 0 && end < len(raw)-1 {
		raw = raw[:end+1]
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, err
	}
	decoded["one_sentence"] = strings.TrimSpace(stringMapValue(decoded, "one_sentence"))
	decoded["long_form"] = strings.TrimSpace(stringMapValue(decoded, "long_form"))
	decoded["highlights"] = stringSlice(decoded["highlights"])
	decoded["suggested_tags"] = stringSlice(decoded["suggested_tags"])
	return decoded, nil
}

func stringMapValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func stringSlice(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return []any{}
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}
