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
)

type GeminiClient struct {
	APIKey   string
	BaseURL  string
	HTTP     *http.Client
	Recorder func(operation string, err error)
}

func (c GeminiClient) GenerateSummary(ctx context.Context, text string) (string, error) {
	return c.generate(ctx, "summary", "Summarize this saved page in one concise sentence:\n\n"+text)
}

func (c GeminiClient) GenerateInsight(ctx context.Context, prompt string) (string, error) {
	return c.generate(ctx, "insight", prompt)
}

func (c GeminiClient) ExtractImageText(ctx context.Context, mimeType string, data []byte) (result string, err error) {
	defer func() { c.record("ocr", err) }()
	if c.APIKey == "" {
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
	if c.APIKey == "" {
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

func (c GeminiClient) generate(ctx context.Context, operation string, prompt string) (result string, err error) {
	defer func() { c.record(operation, err) }()
	if c.APIKey == "" {
		return "", ErrNotConfigured
	}
	if len(prompt) > 12000 {
		prompt = prompt[:12000]
	}
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

func (c GeminiClient) record(operation string, err error) {
	if c.Recorder != nil {
		c.Recorder(operation, err)
	}
}

func (c GeminiClient) endpoint() string {
	base := c.BaseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	return strings.TrimRight(base, "/") + "/v1beta/models/gemini-2.5-flash:generateContent?key=" + c.APIKey
}

func (c GeminiClient) embeddingEndpoint() string {
	base := c.BaseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	return strings.TrimRight(base, "/") + "/v1beta/models/text-embedding-004:embedContent?key=" + c.APIKey
}

func normalizeEmbeddingTaskType(taskType string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(taskType), "-", "_"))
}
