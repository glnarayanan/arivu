package safefetch

import (
	"context"
	"testing"
)

func TestValidateURLBlocksLocalTargets(t *testing.T) {
	blocked := []string{
		"http://localhost/",
		"http://127.0.0.1/",
		"http://[::ffff:127.0.0.1]/",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data",
		"http://100.64.0.1/",
		"http://192.0.2.10/",
		"http://198.18.0.1/",
		"http://224.0.0.1/",
		"http://240.0.0.1/",
		"http://[2001:db8::1]/",
		"http://[fc00::1]/",
		"http://[fe80::1]/",
		"http://[ff02::1]/",
		"file:///etc/passwd",
		"https://user:pass@example.com/",
	}
	for _, raw := range blocked {
		if err := ValidateURL(raw); err == nil {
			t.Fatalf("expected %s to be blocked", raw)
		}
	}
}

func TestResolveSafeRejectsResolvedBlockedIP(t *testing.T) {
	if _, err := resolveSafe(context.Background(), "100.64.0.10"); err == nil {
		t.Fatal("expected resolved shared-address target to be blocked")
	}
	if _, err := resolveSafe(context.Background(), "93.184.216.34"); err != nil {
		t.Fatalf("expected public resolved IP to be allowed: %v", err)
	}
}

func TestValidateURLAllowsPublicHTTP(t *testing.T) {
	if err := ValidateURL("https://example.com/article"); err != nil {
		t.Fatalf("expected public URL to be allowed: %v", err)
	}
}

func TestValidatedURLKeepsPublicTarget(t *testing.T) {
	parsed, err := validatedURL("https://example.com:443/articles?id=1#section")
	if err != nil {
		t.Fatalf("validatedURL error = %v", err)
	}
	if parsed.Scheme != "https" || parsed.Hostname() != "example.com" || parsed.Port() != "443" {
		t.Fatalf("unexpected parsed URL: %#v", parsed)
	}
}

func TestFetchUsesConfiguredUserAgent(t *testing.T) {
	client := NewWithUserAgent("ForkedArivu/1.0")
	req, err := client.newRequest(t.Context(), "https://example.com/article")
	if err != nil {
		t.Fatalf("newRequest error = %v", err)
	}
	if req.UserAgent() != "ForkedArivu/1.0" {
		t.Fatalf("User-Agent = %q", req.UserAgent())
	}
}

func TestNewWithUserAgentFallsBackToNeutralDefault(t *testing.T) {
	client := NewWithUserAgent(" ")
	if client.userAgent != DefaultUserAgent {
		t.Fatalf("userAgent = %q, want %q", client.userAgent, DefaultUserAgent)
	}
}
