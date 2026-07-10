package safefetch

import (
	"context"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/glnarayanan/arivu/internal/sanitize"
)

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

const (
	MaxBodyBytes     = 10 << 20
	DefaultUserAgent = "Arivu/2.0"
)

type Client struct {
	http      *http.Client
	userAgent string
}

type Result struct {
	URL         string
	Title       string
	Description string
	HTML        string
	Text        string
	Domain      string
}

func New() *Client {
	return NewWithUserAgent(DefaultUserAgent)
}

func NewWithUserAgent(userAgent string) *Client {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ip, err := resolveSafe(ctx, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	return &Client{http: &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return ValidateURL(req.URL.String())
		},
	}, userAgent: userAgent}
}

func (c *Client) Fetch(ctx context.Context, rawURL string) (Result, error) {
	req, err := c.newRequest(ctx, rawURL)
	if err != nil {
		return Result{}, err
	}
	// The request URL has passed scheme/host validation above. The custom
	// transport also disables proxy env use and re-resolves every dial target,
	// including redirects, before connecting so DNS rebinding cannot bypass the
	// private/reserved IP blocklist.
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Result{}, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "text/plain") {
		return Result{}, fmt.Errorf("unsupported content type %q", contentType)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(body) > MaxBodyBytes {
		return Result{}, errors.New("content too large")
	}
	parsed := resp.Request.URL
	raw := string(body)
	if strings.Contains(contentType, "text/plain") {
		text := strings.Join(strings.Fields(raw), " ")
		return Result{URL: parsed.String(), Title: parsed.Hostname(), HTML: stdhtml.EscapeString(text), Text: text, Domain: parsed.Hostname()}, nil
	}
	title := ExtractTitle(raw)
	articleHTML, text := ExtractArticle(raw)
	return Result{URL: parsed.String(), Title: title, HTML: articleHTML, Text: trimLeadingChrome(text, title), Domain: parsed.Hostname()}, nil
}

func (c *Client) newRequest(ctx context.Context, rawURL string) (*http.Request, error) {
	parsedURL, err := validatedURL(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
	return req, nil
}

func ValidateURL(raw string) error {
	_, err := validatedURL(raw)
	return err
}

func validatedURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("only HTTP and HTTPS URLs are allowed")
	}
	if parsed.User != nil {
		return nil, errors.New("embedded credentials are not allowed")
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("missing hostname")
	}
	if strings.EqualFold(parsed.Hostname(), "localhost") || strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".local") {
		return nil, errors.New("local hostnames are blocked")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && blockedIP(ip) {
		return nil, errors.New("private or reserved IPs are blocked")
	}
	return parsed, nil
}

func resolveSafe(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return nil, errors.New("private or reserved IPs are blocked")
		}
		return ip, nil
	}
	resolver := net.Resolver{}
	ips, err := resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if !blockedIP(ip) {
			return ip, nil
		}
	}
	return nil, errors.New("hostname resolves only to blocked IPs")
}

func blockedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return !addr.IsGlobalUnicast()
}

func ExtractTitle(html string) string {
	lower := strings.ToLower(html)
	start := strings.Index(lower, "<title")
	if start == -1 {
		return ""
	}
	start = strings.Index(lower[start:], ">") + start + 1
	end := strings.Index(lower[start:], "</title>")
	if start <= 0 || end == -1 {
		return ""
	}
	return strings.TrimSpace(html[start : start+end])
}

func ExtractText(html string) string {
	_, text := ExtractArticle(html)
	return text
}

func trimLeadingChrome(text, title string) string {
	text = strings.TrimSpace(text)
	title = strings.TrimSpace(title)
	if len(title) < 16 {
		return text
	}
	index := strings.Index(strings.ToLower(text), strings.ToLower(title))
	if index <= 0 || index > 1600 || !hasLeadingChrome(text[:index]) {
		return text
	}
	return strings.TrimSpace(text[index:])
}

func hasLeadingChrome(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{"skip to content", "cookie", "privacy", "consent", "analytics", "advertising", "tracking"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func ExtractArticle(input string) (string, string) {
	root, err := html.Parse(strings.NewReader(input))
	if err != nil {
		text := strings.Join(strings.Fields(stdhtml.UnescapeString(input)), " ")
		return stdhtml.EscapeString(text), text
	}
	dropNonContent(root)
	node := bestArticleNode(root)
	var htmlOut strings.Builder
	_ = html.Render(&htmlOut, node)
	articleHTML := strings.TrimSpace(sanitize.HTML(htmlOut.String()))
	text := strings.Join(strings.Fields(textContent(node)), " ")
	if text == "" {
		text = strings.Join(strings.Fields(textContent(root)), " ")
		articleHTML = stdhtml.EscapeString(text)
	}
	return articleHTML, text
}

func dropNonContent(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && shouldDropElement(c) {
			n.RemoveChild(c)
		} else {
			dropNonContent(c)
		}
		c = next
	}
}

func shouldDropElement(n *html.Node) bool {
	tag := strings.ToLower(n.Data)
	switch tag {
	case "script", "style", "noscript", "template", "svg", "canvas", "nav", "footer", "aside", "form", "button":
		return true
	case "header":
		return !hasAncestor(n, "article") && !hasAncestor(n, "main")
	}
	for _, attr := range n.Attr {
		key := strings.ToLower(attr.Key)
		value := strings.ToLower(attr.Val)
		if key != "id" && key != "class" && key != "type" {
			continue
		}
		if strings.Contains(value, "__next_data__") || strings.Contains(value, "cookie") || strings.Contains(value, "advert") || strings.Contains(value, "promo") || strings.Contains(value, "newsletter") || strings.Contains(value, "sidebar") {
			return true
		}
	}
	return false
}

func hasAncestor(n *html.Node, tag string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, tag) {
			return true
		}
	}
	return false
}

func bestArticleNode(root *html.Node) *html.Node {
	if n := firstElement(root, "article"); n != nil {
		return n
	}
	if n := firstElement(root, "main"); n != nil {
		return n
	}
	best := root
	bestScore := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			if tag == "section" || tag == "div" || tag == "body" {
				if score := len(strings.Fields(textContent(n))); score > bestScore {
					bestScore = score
					best = n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return best
}

func firstElement(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, tag) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := firstElement(c, tag); found != nil {
			return found
		}
	}
	return nil
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		switch node.Type {
		case html.TextNode:
			b.WriteByte(' ')
			b.WriteString(stdhtml.UnescapeString(node.Data))
		case html.ElementNode:
			if shouldDropElement(node) {
				return
			}
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		default:
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}
