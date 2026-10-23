package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLocalHealthURL(t *testing.T) {
	for addr, want := range map[string]string{
		":8080":          "http://127.0.0.1:8080/api/health",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/api/health",
		"127.0.0.1:8123": "http://127.0.0.1:8123/api/health",
		"bad":            "http://127.0.0.1:8080/api/health",
	} {
		if got := localHealthURL(addr); got != want {
			t.Errorf("localHealthURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestCheckHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ok.Close()
	if err := checkHealth(ok.URL+"/api/health", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := checkHealth(ok.URL+"/missing", time.Second); err == nil {
		t.Fatal("expected a non-200 response to fail")
	}
}
