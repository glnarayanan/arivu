package safefetch

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	defaultProxyResponseBytes = 50 << 20
	defaultProxyTotalBytes    = 100 << 20
)

// ProxyLimits bound data returned through one short-lived capture proxy.
type ProxyLimits struct {
	MaxResponseBytes int64
	MaxTotalBytes    int64
}

// CaptureProxy is an authenticated, attempt-scoped HTTP/CONNECT proxy. Its
// outbound dials use the same DNS and reserved-address policy as direct fetch.
type CaptureProxy struct {
	token      string
	limits     ProxyLimits
	transport  http.RoundTripper
	dial       func(context.Context, string, string) (net.Conn, error)
	totalBytes atomic.Int64
	server     *http.Server
	connMu     sync.Mutex
	conns      map[net.Conn]struct{}
	closed     atomic.Bool
	closeOnce  sync.Once
}

// StartCaptureProxy listens on a caller-owned, previously absent Unix socket.
// The token should be random and unique to the capture attempt.
func StartCaptureProxy(ctx context.Context, socketPath, token string, limits ProxyLimits) (*CaptureProxy, error) {
	if strings.TrimSpace(socketPath) == "" || strings.TrimSpace(token) == "" {
		return nil, errors.New("capture proxy socket and token are required")
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		_ = listener.Close()
		return nil, err
	}
	proxy := newCaptureProxy(token, limits, nil, nil)
	proxy.server = &http.Server{Handler: proxy, ReadHeaderTimeout: RequestTimeout}
	go func() { _ = proxy.server.Serve(listener) }()
	go func() {
		<-ctx.Done()
		_ = proxy.Close()
	}()
	return proxy, nil
}

func newCaptureProxy(token string, limits ProxyLimits, transport http.RoundTripper, dial func(context.Context, string, string) (net.Conn, error)) *CaptureProxy {
	if limits.MaxResponseBytes <= 0 {
		limits.MaxResponseBytes = defaultProxyResponseBytes
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = defaultProxyTotalBytes
	}
	if transport == nil {
		transport = newSafeTransport()
	}
	if dial == nil {
		dial = safeDialContext
	}
	return &CaptureProxy{token: token, limits: limits, transport: transport, dial: dial, conns: make(map[net.Conn]struct{})}
}

func (p *CaptureProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.authorized(r.Header.Get("Proxy-Authorization")) {
		w.Header().Set("Proxy-Authenticate", "Bearer")
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	if err := validateWebURL(r.URL.String()); err != nil {
		http.Error(w, "capture target blocked", http.StatusForbidden)
		return
	}
	request := r.Clone(r.Context())
	request.RequestURI = ""
	removeHopHeaders(request.Header)
	request.Header.Del("Proxy-Authorization")
	response, err := p.transport.RoundTrip(request)
	if err != nil {
		http.Error(w, "capture upstream failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.ContentLength > p.limits.MaxResponseBytes || (response.ContentLength > 0 && response.ContentLength > p.remaining()) {
		http.Error(w, "capture response exceeded budget", http.StatusBadGateway)
		return
	}
	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(&proxyBudgetWriter{proxy: p, writer: w, maxBytes: p.limits.MaxResponseBytes}, response.Body)
}

func (p *CaptureProxy) connect(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || port != "443" {
		http.Error(w, "capture target blocked", http.StatusForbidden)
		return
	}
	if err := validateWebURL("https://" + net.JoinHostPort(host, port)); err != nil {
		http.Error(w, "capture target blocked", http.StatusForbidden)
		return
	}
	upstream, err := p.dial(r.Context(), "tcp", net.JoinHostPort(host, port))
	if err != nil {
		http.Error(w, "capture upstream failed", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "proxy tunnel unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	_ = buffered.Flush()
	p.connMu.Lock()
	if p.closed.Load() {
		p.connMu.Unlock()
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	p.conns[client] = struct{}{}
	p.conns[upstream] = struct{}{}
	p.connMu.Unlock()
	var closeOnce sync.Once
	closeTunnel := func() {
		closeOnce.Do(func() {
			_ = client.Close()
			_ = upstream.Close()
			p.connMu.Lock()
			delete(p.conns, client)
			delete(p.conns, upstream)
			p.connMu.Unlock()
		})
	}
	go func() {
		_, _ = io.Copy(upstream, client)
		closeTunnel()
	}()
	go func() {
		_, _ = io.Copy(&proxyBudgetWriter{proxy: p, writer: client, maxBytes: p.limits.MaxResponseBytes}, upstream)
		closeTunnel()
	}()
}

func (p *CaptureProxy) authorized(value string) bool {
	bearer := subtle.ConstantTimeCompare([]byte(value), []byte("Bearer "+p.token))
	basicValue := base64.StdEncoding.EncodeToString([]byte("arivu:" + p.token))
	basic := subtle.ConstantTimeCompare([]byte(value), []byte("Basic "+basicValue))
	return bearer|basic == 1
}

func (p *CaptureProxy) take(size int64) int64 {
	for {
		used := p.totalBytes.Load()
		remaining := p.limits.MaxTotalBytes - used
		if size <= 0 || remaining <= 0 {
			return 0
		}
		if size > remaining {
			size = remaining
		}
		if p.totalBytes.CompareAndSwap(used, used+size) {
			return size
		}
	}
}

func (p *CaptureProxy) remaining() int64 {
	remaining := p.limits.MaxTotalBytes - p.totalBytes.Load()
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Close terminates the proxy and removes only the socket it created.
func (p *CaptureProxy) Close() error {
	var closeErr error
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		if p.server != nil {
			closeErr = p.server.Close()
		}
		if transport, ok := p.transport.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
		p.connMu.Lock()
		for conn := range p.conns {
			_ = conn.Close()
		}
		clear(p.conns)
		p.connMu.Unlock()
	})
	return closeErr
}

type proxyBudgetWriter struct {
	proxy        *CaptureProxy
	writer       io.Writer
	maxBytes     int64
	writtenBytes int64
}

func (w *proxyBudgetWriter) Write(data []byte) (int, error) {
	remaining := w.maxBytes - w.writtenBytes
	if remaining <= 0 {
		return 0, errors.New("capture response exceeded budget")
	}
	wanted := int64(len(data))
	if wanted > remaining {
		wanted = remaining
	}
	allowed := w.proxy.take(wanted)
	if allowed <= 0 {
		return 0, errors.New("capture response exceeded budget")
	}
	written, err := w.writer.Write(data[:allowed])
	w.writtenBytes += int64(written)
	if err == nil && written < len(data) {
		err = errors.New("capture response exceeded budget")
	}
	return written, err
}

func validateWebURL(raw string) error {
	parsed, err := validatedURL(raw)
	if err != nil {
		return err
	}
	if port := parsed.Port(); port != "" && port != "80" && port != "443" {
		return errors.New("non-web ports are blocked")
	}
	return nil
}

func copyHeaders(dst, src http.Header) {
	connectionHeaders := connectionHeaderNames(src)
	for key, values := range src {
		if isHopHeader(key) || connectionHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func removeHopHeaders(header http.Header) {
	connectionHeaders := connectionHeaderNames(header)
	for key := range header {
		if isHopHeader(key) || connectionHeaders[strings.ToLower(key)] {
			header.Del(key)
		}
	}
}

func connectionHeaderNames(header http.Header) map[string]bool {
	names := make(map[string]bool)
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
				names[name] = true
			}
		}
	}
	return names
}

func isHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}
