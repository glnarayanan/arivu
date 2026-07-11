package safefetch

import (
	"context"
	"strings"
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
		"http://192.88.99.1/",
		"http://198.18.0.1/",
		"http://224.0.0.1/",
		"http://240.0.0.1/",
		"http://[2001:db8::1]/",
		"http://[fc00::1]/",
		"http://[fe80::1]/",
		"http://[ff02::1]/",
		"http://[64:ff9b::7f00:1]/",
		"http://[64:ff9b:1::1]/",
		"http://[2001:2::1]/",
		"http://[2001:10::1]/",
		"http://[2001:20::1]/",
		"http://[2002:7f00:1::]/",
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

func TestExtractArticlePrefersReadableContent(t *testing.T) {
	html := `<!doctype html><html><head><title>Ignored chrome</title><style>.app{display:none}</style><script id="__NEXT_DATA__">{"props":"bad"}</script></head><body>
		<header>Subscribe now</header><nav>Home Pricing Login</nav>
		<main><article><header><h1>Useful &amp; Specific</h1><p>Byline that belongs to the article.</p></header><p>This article explains the durable idea.</p><p>It keeps the real body copy.</p></article></main>
		<footer>Cookie preferences</footer></body></html>`
	articleHTML, text := ExtractArticle(html)
	for _, unwanted := range []string{"Subscribe", "Home Pricing", "display:none", "__NEXT_DATA__", "Cookie"} {
		if strings.Contains(text, unwanted) || strings.Contains(articleHTML, unwanted) {
			t.Fatalf("chrome leaked into extracted content %q / %q", text, articleHTML)
		}
	}
	if !strings.Contains(text, "Useful & Specific") || !strings.Contains(text, "Byline that belongs") || !strings.Contains(text, "real body copy") {
		t.Fatalf("article text missing body content: %q", text)
	}
	if !strings.Contains(articleHTML, "<article>") || !strings.Contains(articleHTML, "Useful &amp; Specific") {
		t.Fatalf("article html not preserved safely: %q", articleHTML)
	}
}

func TestExtractArticlePrefersSubstackPostBodyOverDiscussion(t *testing.T) {
	html := `<!doctype html><html><head><title>Useful Post</title><meta name="description" content="A useful explanation of agent tools."></head><body>
		<article class="newsletter-post">
			<header><h1>Useful Post</h1></header>
			<div class="available-content"><div class="body markup">
				<p>The actual article starts with a concrete mental model for choosing tools.</p>
				<p>It continues with enough durable detail to make the saved page useful later.</p>
			</div></div>
			<section id="substack-comments" class="comments-section"><h2>Discussion about this post</h2><p>This comment should not become archived article text.</p></section>
		</article>
	</body></html>`
	articleHTML, text := ExtractArticle(html)
	if !strings.Contains(text, "actual article starts") || !strings.Contains(articleHTML, "durable detail") {
		t.Fatalf("post body missing from extraction: %q / %q", text, articleHTML)
	}
	if strings.Contains(text, "Discussion about this post") || strings.Contains(articleHTML, "This comment should not") {
		t.Fatalf("discussion leaked into extracted content: %q / %q", text, articleHTML)
	}
}

func TestExtractDescriptionReadsStandardAndOpenGraphMetadata(t *testing.T) {
	standard := `<html><head><meta name="description" content=" Standard description. "></head></html>`
	if got := ExtractDescription(standard); got != "Standard description." {
		t.Fatalf("standard description = %q", got)
	}
	openGraph := `<html><head><meta property="og:description" content="OpenGraph description."></head></html>`
	if got := ExtractDescription(openGraph); got != "OpenGraph description." {
		t.Fatalf("OpenGraph description = %q", got)
	}
}

func TestContentQualityMarksEmptyAndDiscussionOnlyExtractionsPartial(t *testing.T) {
	for _, text := range []string{
		"",
		"Discussion about this post. Reply Share No posts Ready for more?",
	} {
		if got := contentQuality(text); got != QualityPartial {
			t.Fatalf("contentQuality(%q) = %q, want %q", text, got, QualityPartial)
		}
	}
	if got := contentQuality("A concise release note explains the change, the reason for it, and how readers should use the new behavior safely in their workflow."); got != QualityComplete {
		t.Fatalf("meaningful short content quality = %q, want %q", got, QualityComplete)
	}
	if got := contentQuality("Fixed login redirect handling."); got != QualityComplete {
		t.Fatalf("concise content quality = %q, want %q", got, QualityComplete)
	}
}

func TestTrimLeadingChromeUsesTheArticleTitle(t *testing.T) {
	title := "Useful & Specific Article"
	text := "Skip to content Privacy preferences and tracking options. " + title + " The article starts here."
	if got := trimLeadingChrome(text, title); got != title+" The article starts here." {
		t.Fatalf("trimLeadingChrome() = %q", got)
	}
}

func TestTrimLeadingChromeKeepsOrdinaryArticleIntroductions(t *testing.T) {
	title := "Useful & Specific Article"
	text := "In this introduction, the author foreshadows " + title + ". The article continues."
	if got := trimLeadingChrome(text, title); got != text {
		t.Fatalf("trimLeadingChrome() = %q", got)
	}
}
