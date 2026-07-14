package sanitize

import (
	"strings"
	"testing"
)

func TestHTMLStripsScriptsAndUnsafeAttributes(t *testing.T) {
	got := HTML(`<article><h1 onclick="alert(1)">Title</h1><script>alert(1)</script><style>.x{color:red}</style><a href="javascript:alert(1)">bad</a><a href="https://example.com">ok</a></article>`)
	if strings.Contains(got, "script") || strings.Contains(got, "alert(1)") || strings.Contains(got, "color:red") || strings.Contains(got, "onclick") || strings.Contains(got, "javascript:") {
		t.Fatalf("unsafe content survived sanitizer: %s", got)
	}
	if !strings.Contains(got, `href="https://example.com"`) {
		t.Fatalf("safe link was not preserved: %s", got)
	}
}

func TestHTMLAllowsOnlyRehostedReaderImages(t *testing.T) {
	input := `<figure><img src="/api/media/01J-safe_ID" alt="A &amp; B" width="1200" height="800" srcset="https://evil.test/a 2x" onerror="alert(1)"><figcaption>Caption</figcaption></figure>` +
		`<img src="https://example.com/hotlink.jpg"><img src="data:image/png;base64,AAAA"><img src="/api/media/../other">`
	want := `<figure><img src="/api/media/01J-safe_ID" alt="A &amp; B" width="1200" height="800" loading="lazy" decoding="async"><figcaption>Caption</figcaption></figure>`
	if got := HTML(input); got != want {
		t.Fatalf("HTML() = %q, want %q", got, want)
	}
}
