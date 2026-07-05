package app

import (
	"sync"
	"time"
)

type providerUsage struct {
	mu     sync.Mutex
	since  time.Time
	gemini map[string]providerUsageOp
}

type providerUsageOp struct {
	Requests    int    `json:"requests"`
	Errors      int    `json:"errors"`
	LastRequest string `json:"last_request,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

func newProviderUsage() *providerUsage {
	return &providerUsage{since: time.Now().UTC(), gemini: map[string]providerUsageOp{}}
}

func (u *providerUsage) RecordGemini(operation string, err error) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	item := u.gemini[operation]
	item.Requests++
	item.LastRequest = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		item.Errors++
		item.LastError = err.Error()
	}
	u.gemini[operation] = item
}

func (u *providerUsage) Snapshot() map[string]any {
	if u == nil {
		return map[string]any{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	gemini := map[string]providerUsageOp{}
	totalRequests := 0
	totalErrors := 0
	for key, value := range u.gemini {
		gemini[key] = value
		totalRequests += value.Requests
		totalErrors += value.Errors
	}
	return map[string]any{
		"since":          u.since.Format(time.RFC3339),
		"requests_total": totalRequests,
		"errors_total":   totalErrors,
		"gemini":         gemini,
	}
}
