package sanitize

import (
	"strings"
	"testing"
)

func TestHTMLStripsScriptsAndUnsafeAttributes(t *testing.T) {
	got := HTML(`<article><h1 onclick="alert(1)">Title</h1><script>alert(1)</script><a href="javascript:alert(1)">bad</a><a href="https://example.com">ok</a></article>`)
	if strings.Contains(got, "script") || strings.Contains(got, "onclick") || strings.Contains(got, "javascript:") {
		t.Fatalf("unsafe content survived sanitizer: %s", got)
	}
	if !strings.Contains(got, `href="https://example.com"`) {
		t.Fatalf("safe link was not preserved: %s", got)
	}
}
