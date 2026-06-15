package safefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const MaxBodyBytes = 10 << 20

type Client struct {
	http *http.Client
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
	}}
}

func (c *Client) Fetch(ctx context.Context, rawURL string) (Result, error) {
	parsedURL, err := validatedURL(rawURL)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "Arivu/2.0 (+https://github.com/glnarayanan/arivu)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
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
	html := string(body)
	text := ExtractText(html)
	title := ExtractTitle(html)
	return Result{URL: parsed.String(), Title: title, HTML: html, Text: text, Domain: parsed.Hostname()}, nil
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
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
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
	var b strings.Builder
	inTag := false
	for _, r := range html {
		switch r {
		case '<':
			inTag = true
			b.WriteRune(' ')
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
