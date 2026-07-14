package browsercapture

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/config"
)

func TestCaptureHelperIntegration(t *testing.T) {
	if os.Getenv("ARIVU_CAPTURE_INTEGRATION") != "1" {
		t.Skip("set ARIVU_CAPTURE_INTEGRATION=1 to run the Playwright integration")
	}
	runtimeDir, err := os.MkdirTemp("/tmp", "arivu-capture-integration-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtimeDir)
	socket := filepath.Join(runtimeDir, "helper.sock")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "capture", "src", "index.mjs"))
	fakeMonolith, err := filepath.Abs(filepath.Join("..", "..", "capture", "test-support", "fake-monolith.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "ARIVU_CAPTURE_SOCKET="+socket, "ARIVU_CAPTURE_RUNTIME_DIR="+runtimeDir, "ARIVU_MONOLITH_PATH="+fakeMonolith, "ARIVU_CAPTURE_DEBUG=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	for deadline := time.Now().Add(3 * time.Second); ; {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture helper did not start: %s", stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	duplicate := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "capture", "src", "index.mjs"))
	duplicate.Env = command.Env
	if err := duplicate.Run(); err == nil {
		t.Fatal("a second helper replaced the live capture socket")
	}

	cfg := config.BrowserCaptureConfig{
		Enabled: true, Protocol: 2, Socket: socket, RuntimeDir: runtimeDir, Screenshot: true, SelfContainedHTML: true,
		Timeout: 40 * time.Second, NavigationTimeout: 20 * time.Second,
		MaxFileBytes: 20 << 20, MaxTotalBytes: 50 << 20,
		MaxMediaFiles: 10, MaxMediaFileBytes: 2 << 20, MaxMediaTotalBytes: 10 << 20,
	}
	for attempt := 0; attempt < 2; attempt++ {
		timeoutConfig := cfg
		timeoutConfig.Timeout = 40 * time.Millisecond
		timeoutConfig.NavigationTimeout = 20 * time.Millisecond
		if err := RunV2(t.Context(), timeoutConfig, "https://example.com/", func(V2Result) error { return nil }); captureCode(err) != "browser_timeout" {
			t.Fatalf("abandoned capture error=%v", err)
		}
	}
	var privateRequests atomic.Int32
	privateTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { privateRequests.Add(1) }))
	defer privateTarget.Close()
	html := fmt.Sprintf(`<html><body><article><h1>Rendered capture</h1><p>%s</p><img src="%s/private"></article></body></html>`, strings.Repeat("useful reader evidence ", 250), privateTarget.URL)
	publicFixture := "https://httpbingo.org/base64/" + base64.RawURLEncoding.EncodeToString([]byte(html))
	err = RunV2(ctx, cfg, publicFixture, func(result V2Result) error {
		if result.Content.HTML.Size == 0 || result.Content.Text.Size == 0 || len(result.Artifacts) != 2 || result.Artifacts[0].Type != "self_contained_html" || result.Artifacts[1].Type != "screenshot" {
			t.Fatalf("incomplete integration result: %+v", result)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("capture failed: %v; helper stderr: %s", err, stderr.String())
	}
	if privateRequests.Load() != 0 {
		t.Fatalf("Chromium bypassed the safe proxy for %d private requests", privateRequests.Load())
	}
}
