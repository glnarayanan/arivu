package safefetch

import "testing"

func TestValidateURLBlocksLocalTargets(t *testing.T) {
	blocked := []string{
		"http://localhost/",
		"http://127.0.0.1/",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data",
		"file:///etc/passwd",
		"https://user:pass@example.com/",
	}
	for _, raw := range blocked {
		if err := ValidateURL(raw); err == nil {
			t.Fatalf("expected %s to be blocked", raw)
		}
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
