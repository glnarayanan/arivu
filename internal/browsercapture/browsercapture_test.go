package browsercapture

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/config"
)

func fakeHelper(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func captureCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestRunFakeHelperBoundaries(t *testing.T) {
	base := config.BrowserCaptureConfig{Enabled: true, Screenshot: true, Timeout: 5 * time.Second, MaxFileBytes: 32, MaxTotalBytes: 64}
	tests := []struct{ name, body, code string }{
		{"success", `r=$(cat); d=$(printf '%s' "$r"|sed -n 's/.*"output_dir":"\([^"]*\)".*/\1/p'); t=$(printf '%s' "$r"|sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); printf x > "$d/a.png"; printf '{"version":1,"token":"%s","artifacts":[{"type":"screenshot","mime":"image/png","path":"a.png"}]}' "$t"`, ""},
		{"traversal", `r=$(cat); t=$(printf '%s' "$r"|sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); printf '{"version":1,"token":"%s","artifacts":[{"type":"screenshot","mime":"image/png","path":"../x"}]}' "$t"`, "browser_path_escape"},
		{"oversize", `r=$(cat); d=$(printf '%s' "$r"|sed -n 's/.*"output_dir":"\([^"]*\)".*/\1/p'); t=$(printf '%s' "$r"|sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); dd if=/dev/zero of="$d/a.png" bs=64 count=1 2>/dev/null; printf '{"version":1,"token":"%s","artifacts":[{"type":"screenshot","mime":"image/png","path":"a.png"}]}' "$t"`, "browser_output_too_large"},
		{"type", `r=$(cat); t=$(printf '%s' "$r"|sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); printf '{"version":1,"token":"%s","artifacts":[{"type":"raw","mime":"text/plain","path":"a"}]}' "$t"`, "browser_invalid_artifact"},
		{"token", `cat >/dev/null; printf '{"version":1,"token":"wrong","artifacts":[]}'`, "browser_invalid_response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.Command = fakeHelper(t, tt.body)
			n := 0
			err := Run(context.Background(), cfg, "https://example.com", func(_ Artifact, r io.Reader) error { b, _ := io.ReadAll(r); n += len(b); return nil })
			if captureCode(err) != tt.code {
				t.Fatalf("error=%v code=%q", err, captureCode(err))
			}
			if tt.code == "" && n != 1 {
				t.Fatalf("ingested=%d", n)
			}
		})
	}
}

func TestRunFakeHelperTimeout(t *testing.T) {
	cfg := config.BrowserCaptureConfig{Enabled: true, Command: fakeHelper(t, "sleep 2"), Timeout: 20 * time.Millisecond, MaxFileBytes: 32, MaxTotalBytes: 64}
	err := Run(context.Background(), cfg, "https://example.com", func(Artifact, io.Reader) error { return nil })
	if !strings.Contains(captureCode(err), "timeout") {
		t.Fatalf("error=%v", err)
	}
}
