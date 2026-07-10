package app

import (
	"sync"
	"time"
)

type providerUsage struct {
	mu    sync.Mutex
	since time.Time
	ai    map[string]providerUsageOp
}

type providerUsageOp struct {
	Requests    int    `json:"requests"`
	Errors      int    `json:"errors"`
	LastRequest string `json:"last_request,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

func newProviderUsage() *providerUsage {
	return &providerUsage{since: time.Now().UTC(), ai: map[string]providerUsageOp{}}
}

func (u *providerUsage) RecordAI(operation string, err error) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	item := u.ai[operation]
	item.Requests++
	item.LastRequest = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		item.Errors++
		item.LastError = err.Error()
	}
	u.ai[operation] = item
}

func (u *providerUsage) Snapshot() map[string]any {
	if u == nil {
		return map[string]any{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	ai := map[string]providerUsageOp{}
	totalRequests := 0
	totalErrors := 0
	for key, value := range u.ai {
		ai[key] = value
		totalRequests += value.Requests
		totalErrors += value.Errors
	}
	return map[string]any{
		"since":          u.since.Format(time.RFC3339),
		"requests_total": totalRequests,
		"errors_total":   totalErrors,
		"ai":             ai,
		"gemini":         ai,
	}
}
