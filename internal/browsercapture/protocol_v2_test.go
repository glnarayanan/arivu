package browsercapture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/capture"
	"github.com/glnarayanan/arivu/internal/config"
)

func v2Fixture() (config.BrowserCaptureConfig, v2Response) {
	cfg := config.BrowserCaptureConfig{
		Enabled: true, Protocol: 2, Screenshot: true, Timeout: time.Minute, NavigationTimeout: 30 * time.Second,
		MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxMediaFiles: 4, MaxMediaFileBytes: 1024, MaxMediaTotalBytes: 2048,
	}
	response := v2Response{
		Version: 2, Token: "request-token",
		V2Result: V2Result{
			EngineVersion: "arivu-capture-v2",
			Metadata:      V2Metadata{FinalURL: "https://example.com/article", CanonicalURL: "https://example.com/article", Title: "Article"},
			Content: V2Content{
				HTML: V2File{MIME: "text/html", Size: 50}, Text: V2File{MIME: "text/plain", Size: 24},
				QualityStatus: capture.QualityComplete, QualityScore: 92,
			},
			Artifacts:  []V2Artifact{{Type: "screenshot", V2File: V2File{MIME: "image/jpeg", Size: 4}}},
			Media:      []V2Media{{SourceURL: "https://example.com/image.png", Role: "reader_image", Width: 800, Height: 600, V2File: V2File{MIME: "image/png", Size: 3}}},
			Components: map[string]V2Component{"browser": {Status: "complete"}, "readability": {Status: "complete"}},
		},
	}
	return cfg, response
}

func TestDecodeV2HeaderIsStrictAndBounded(t *testing.T) {
	response := v2Response{
		Version: 2, Token: "token",
		V2Result: V2Result{EngineVersion: "v2", Content: V2Content{QualityStatus: capture.QualityFailed}, ErrorCode: "capture_failed"},
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeV2Header(bufio.NewReader(strings.NewReader(string(raw) + "\n"))); err != nil {
		t.Fatal(err)
	}
	invalid := [][]byte{
		append(bytes.TrimSuffix(raw, []byte("}")), []byte(",\"unknown\":true}\n")...),
		append(append([]byte{}, raw...), []byte(" {}\n")...),
		bytes.Repeat([]byte(" "), maxV2HeaderBytes+1),
		append([]byte{}, raw...),
	}
	for _, input := range invalid {
		if _, err := decodeV2Header(bufio.NewReaderSize(bytes.NewReader(input), maxV2HeaderBytes+1)); captureCode(err) != "browser_invalid_response" {
			t.Fatalf("invalid response error=%v", err)
		}
	}
}

func TestValidateV2HeaderAcceptsCompleteBoundedCapture(t *testing.T) {
	cfg, response := v2Fixture()
	if err := validateV2Header("request-token", []string{"screenshot"}, cfg, response); err != nil {
		t.Fatal(err)
	}
}

func TestValidateV2HeaderRejectsUntrustedManifest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.BrowserCaptureConfig, *v2Response)
		code   string
	}{
		{"wrong token", func(_ *config.BrowserCaptureConfig, response *v2Response) { response.Token = "wrong" }, "browser_invalid_response"},
		{"unknown quality", func(_ *config.BrowserCaptureConfig, response *v2Response) {
			response.Content.QualityStatus = "excellent"
		}, "browser_invalid_content"},
		{"unsafe final url", func(_ *config.BrowserCaptureConfig, response *v2Response) {
			response.Metadata.FinalURL = "http://127.0.0.1/private"
		}, "browser_invalid_metadata"},
		{"unsafe media url", func(_ *config.BrowserCaptureConfig, response *v2Response) {
			response.Media[0].SourceURL = "http://169.254.169.254/meta"
		}, "browser_invalid_media"},
		{"duplicate media", func(_ *config.BrowserCaptureConfig, response *v2Response) {
			response.Media = append(response.Media, response.Media[0])
		}, "browser_invalid_media"},
		{"missing artifact", func(_ *config.BrowserCaptureConfig, response *v2Response) { response.Artifacts = nil }, "browser_missing_artifact"},
		{"oversized media", func(cfg *config.BrowserCaptureConfig, _ *v2Response) { cfg.MaxMediaFileBytes = 2 }, "browser_invalid_media"},
		{"combined budget", func(cfg *config.BrowserCaptureConfig, _ *v2Response) { cfg.MaxTotalBytes = 80 }, "browser_output_too_large"},
		{"failed component without code", func(_ *config.BrowserCaptureConfig, response *v2Response) {
			response.Components["browser"] = V2Component{Status: "failed"}
		}, "browser_invalid_response"},
		{"complete component with code", func(_ *config.BrowserCaptureConfig, response *v2Response) {
			response.Components["browser"] = V2Component{Status: "complete", ErrorCode: "browser_failed"}
		}, "browser_invalid_response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, response := v2Fixture()
			test.mutate(&cfg, &response)
			err := validateV2Header("request-token", []string{"screenshot"}, cfg, response)
			if captureCode(err) != test.code {
				t.Fatalf("error=%v code=%q want=%q", err, captureCode(err), test.code)
			}
		})
	}
}

func TestValidateV2HeaderAllowsFailedCaptureWithoutPayload(t *testing.T) {
	cfg, response := v2Fixture()
	response.Metadata = V2Metadata{}
	response.Content = V2Content{QualityStatus: capture.QualityFailed}
	response.Artifacts = nil
	response.Media = nil
	response.ErrorCode = "navigation_failed"
	response.Components = map[string]V2Component{"browser": {Status: "failed", ErrorCode: "navigation_failed"}}
	if err := validateV2Header("request-token", []string{"screenshot"}, cfg, response); err != nil {
		t.Fatal(err)
	}
}

func TestValidateV2HeaderAllowsMissingArtifactOnlyWithAttemptError(t *testing.T) {
	cfg, response := v2Fixture()
	response.Artifacts = nil
	response.ErrorCode = "screenshot_failed"
	response.Components["screenshot"] = V2Component{Status: "failed", ErrorCode: "screenshot_failed"}
	if err := validateV2Header("request-token", []string{"screenshot"}, cfg, response); err != nil {
		t.Fatal(err)
	}
}

func TestReceiveV2FilesWritesOnlyGoOwnedPaths(t *testing.T) {
	_, response := v2Fixture()
	root := t.TempDir()
	stream := strings.NewReader(strings.Repeat("h", 50) + strings.Repeat("t", 24) + "jpeg" + "png")
	if err := receiveV2Files(bufio.NewReader(stream), root, &response.V2Result); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path string
		want string
	}{
		{response.Content.HTML.Path, strings.Repeat("h", 50)},
		{response.Content.Text.Path, strings.Repeat("t", 24)},
		{response.Artifacts[0].Path, "jpeg"},
		{response.Media[0].Path, "png"},
	} {
		if filepath.Dir(item.path) != root {
			t.Fatalf("path %q escaped private root", item.path)
		}
		body, err := os.ReadFile(item.path)
		if err != nil || string(body) != item.want {
			t.Fatalf("file %q body=%q error=%v", item.path, body, err)
		}
	}
}

func TestReceiveV2FilesRejectsTruncatedAndTrailingStreams(t *testing.T) {
	for _, stream := range []string{
		strings.Repeat("x", 80),
		strings.Repeat("x", 82) + "extra",
	} {
		_, response := v2Fixture()
		err := receiveV2Files(bufio.NewReader(strings.NewReader(stream)), t.TempDir(), &response.V2Result)
		if captureCode(err) != "browser_invalid_response" {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestRunV2UsesUnixProtocolAndCleansPrivateFiles(t *testing.T) {
	runtimeDir, err := os.MkdirTemp("/tmp", "arivu-v2-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	socket := filepath.Join(runtimeDir, "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("unix sockets are unavailable in this sandbox")
		}
		t.Fatal(err)
	}
	defer listener.Close()

	helperError := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			helperError <- err
			return
		}
		defer conn.Close()
		var request v2Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			helperError <- err
			return
		}
		response := v2Response{
			Version: 2, Token: request.Token,
			V2Result: V2Result{
				EngineVersion: "test-helper", Metadata: V2Metadata{FinalURL: request.URL},
				Content: V2Content{
					HTML: V2File{MIME: "text/html", Size: 4}, Text: V2File{MIME: "text/plain", Size: 4},
					QualityStatus: capture.QualityComplete, QualityScore: 90,
				},
				Components: map[string]V2Component{"browser": {Status: "complete"}, "readability": {Status: "complete"}},
			},
		}
		header, err := json.Marshal(response)
		if err == nil {
			_, err = conn.Write(append(header, '\n'))
		}
		if err == nil {
			_, err = io.WriteString(conn, "htmltext")
		}
		helperError <- err
	}()

	cfg := config.BrowserCaptureConfig{
		Enabled: true, Protocol: 2, Socket: socket, RuntimeDir: runtimeDir,
		Timeout: time.Second, NavigationTimeout: time.Second, MaxFileBytes: 1024, MaxTotalBytes: 2048,
		MaxMediaFiles: 1, MaxMediaFileBytes: 128, MaxMediaTotalBytes: 128,
	}
	var privateRoot string
	err = RunV2(t.Context(), cfg, "https://example.com/article", func(result V2Result) error {
		privateRoot = result.root
		for path, want := range map[string]string{result.Content.HTML.Path: "html", result.Content.Text.Path: "text"} {
			body, readErr := os.ReadFile(path)
			if readErr != nil || string(body) != want {
				return errors.New("unexpected captured content")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-helperError; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(privateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private result directory was not removed: %v", err)
	}
}

func TestRunV2ReportsHeaderAndPayloadStallsAsTimeouts(t *testing.T) {
	for _, phase := range []string{"header", "payload"} {
		t.Run(phase, func(t *testing.T) {
			runtimeDir, err := os.MkdirTemp("/tmp", "arivu-v2-timeout-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(runtimeDir) })
			listener, err := net.Listen("unix", filepath.Join(runtimeDir, "helper.sock"))
			if err != nil {
				if errors.Is(err, os.ErrPermission) {
					t.Skip("unix sockets are unavailable in this sandbox")
				}
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				conn, acceptErr := listener.Accept()
				if acceptErr != nil {
					return
				}
				defer conn.Close()
				var request v2Request
				if json.NewDecoder(conn).Decode(&request) != nil {
					return
				}
				if phase == "payload" {
					response := v2Response{
						Version: 2, Token: request.Token,
						V2Result: V2Result{
							EngineVersion: "test-helper", Metadata: V2Metadata{FinalURL: request.URL},
							Content:    V2Content{HTML: V2File{MIME: "text/html", Size: 4}, Text: V2File{MIME: "text/plain", Size: 4}, QualityStatus: capture.QualityComplete, QualityScore: 90},
							Components: map[string]V2Component{"browser": {Status: "complete"}, "readability": {Status: "complete"}},
						},
					}
					header, _ := json.Marshal(response)
					_, _ = conn.Write(append(header, '\n'))
					_, _ = io.WriteString(conn, "html")
				}
				time.Sleep(100 * time.Millisecond)
			}()

			cfg := config.BrowserCaptureConfig{
				Enabled: true, Protocol: 2, Socket: filepath.Join(runtimeDir, "helper.sock"), RuntimeDir: runtimeDir,
				Timeout: 20 * time.Millisecond, NavigationTimeout: time.Second, MaxFileBytes: 1024, MaxTotalBytes: 2048,
				MaxMediaFiles: 1, MaxMediaFileBytes: 128, MaxMediaTotalBytes: 128,
			}
			if err := RunV2(t.Context(), cfg, "https://example.com/article", func(V2Result) error { return nil }); captureCode(err) != "browser_timeout" {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
