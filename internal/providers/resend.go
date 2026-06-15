package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type ResendClient struct {
	APIKey string
	From   string
	HTTP   *http.Client
}

func (c ResendClient) Send(ctx context.Context, to string, subject string, html string) error {
	if c.APIKey == "" || c.From == "" {
		return ErrNotConfigured
	}
	body := map[string]any{"from": c.From, "to": []string{to}, "subject": subject, "html": html}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("resend status %d", resp.StatusCode)
	}
	return nil
}
