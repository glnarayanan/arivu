package config

import "testing"

func TestBrowserCaptureDefaultsPreserveV1AndEnableV2Screenshot(t *testing.T) {
	for _, key := range []string{"ARIVU_BROWSER_CAPTURE_SCREENSHOT", "ARIVU_BROWSER_CAPTURE_TIMEOUT", "ARIVU_BROWSER_CAPTURE_MAX_FILE_BYTES", "ARIVU_BROWSER_CAPTURE_MAX_TOTAL_BYTES"} {
		t.Setenv(key, "")
	}
	t.Run("v1", func(t *testing.T) {
		t.Setenv("ARIVU_BROWSER_CAPTURE_PROTOCOL", "1")
		cfg := FromEnv().BrowserCapture
		if cfg.Timeout.String() != "30s" || cfg.MaxFileBytes != 10<<20 || cfg.MaxTotalBytes != 20<<20 || cfg.Screenshot {
			t.Fatalf("unexpected v1 defaults: %+v", cfg)
		}
	})
	t.Run("v2", func(t *testing.T) {
		t.Setenv("ARIVU_BROWSER_CAPTURE_PROTOCOL", "2")
		cfg := FromEnv().BrowserCapture
		if cfg.Timeout.String() != "1m30s" || cfg.MaxFileBytes != 50<<20 || cfg.MaxTotalBytes != 100<<20 || !cfg.Screenshot {
			t.Fatalf("unexpected v2 defaults: %+v", cfg)
		}
	})
}

func TestValidateRequiresSocketForCaptureProtocolV2(t *testing.T) {
	config := Config{BrowserCapture: BrowserCaptureConfig{Enabled: true, Protocol: 2, Screenshot: true}}
	if err := config.Validate(); err == nil {
		t.Fatal("protocol v2 without a helper socket was accepted")
	}
	config.BrowserCapture.Socket = "/run/arivu/capture.sock"
	config.BrowserCapture.Screenshot = false
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsUnknownCaptureProtocol(t *testing.T) {
	config := Config{BrowserCapture: BrowserCaptureConfig{Enabled: true, Protocol: 3, Screenshot: true}}
	if err := config.Validate(); err == nil {
		t.Fatal("unknown capture protocol was accepted")
	}
}

func TestBrowserCaptureV2Environment(t *testing.T) {
	t.Setenv("ARIVU_BROWSER_CAPTURE_PROTOCOL", "2")
	t.Setenv("ARIVU_BROWSER_CAPTURE_SOCKET", "/run/arivu-capture/helper.sock")
	t.Setenv("ARIVU_BROWSER_CAPTURE_RUNTIME_DIR", "/run/arivu-capture/attempts")
	t.Setenv("ARIVU_BROWSER_CAPTURE_NAVIGATION_TIMEOUT", "12s")
	t.Setenv("ARIVU_BROWSER_CAPTURE_MAX_MEDIA_FILES", "7")
	t.Setenv("ARIVU_BROWSER_CAPTURE_MAX_MEDIA_FILE_BYTES", "2048")
	t.Setenv("ARIVU_BROWSER_CAPTURE_MAX_MEDIA_TOTAL_BYTES", "8192")
	cfg := FromEnv().BrowserCapture
	if cfg.Protocol != 2 || cfg.Socket != "/run/arivu-capture/helper.sock" || cfg.RuntimeDir != "/run/arivu-capture/attempts" || cfg.NavigationTimeout.String() != "12s" || cfg.MaxMediaFiles != 7 || cfg.MaxMediaFileBytes != 2048 || cfg.MaxMediaTotalBytes != 8192 {
		t.Fatalf("unexpected v2 environment: %+v", cfg)
	}
}
