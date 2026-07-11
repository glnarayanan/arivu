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

func TestFrontendServesBundledFontsWithFontContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/fonts/geist-variable.woff2", nil)
	rec := httptest.NewRecorder()
	(&App{}).frontend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("font status = %d", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "font/woff2" {
		t.Fatalf("font content type = %q", contentType)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("font asset was empty")
	}
}
