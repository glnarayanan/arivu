package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBookmarkCardsDoNotMistakeMissingDescriptionsForQueuedJobs(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	(&App{}).frontend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("app.js status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `b.description || "No description available."`) {
		t.Fatalf("bookmark card missing-description copy was not embedded")
	}
	if strings.Contains(body, `b.description || "Queued for enrichment"`) {
		t.Fatalf("bookmark cards must not imply missing descriptions are queued jobs")
	}
}
