package safefetch

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type closeTrackingTransport struct{ closed atomic.Bool }

func (t *closeTrackingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (t *closeTrackingTransport) CloseIdleConnections() { t.closed.Store(true) }

type hijackRecorder struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (r *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return r.conn, bufio.NewReadWriter(bufio.NewReader(r.conn), bufio.NewWriter(r.conn)), nil
}

func proxyRequest(method, target, token string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	if token != "" {
		request.Header.Set("Proxy-Authorization", "Bearer "+token)
	}
	return request
}

func TestCaptureProxyRequiresAuthenticationBeforeForwarding(t *testing.T) {
	var calls atomic.Int32
	proxy := newCaptureProxy("secret", ProxyLimits{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, nil
	}), nil)

	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, proxyRequest(http.MethodGet, "https://example.com/article", "wrong"))
	if response.Code != http.StatusProxyAuthRequired || calls.Load() != 0 {
		t.Fatalf("status=%d upstream calls=%d", response.Code, calls.Load())
	}
	if got := response.Header().Get("Proxy-Authenticate"); got != `Basic realm="arivu-capture"` {
		t.Fatalf("authentication challenge=%q", got)
	}
}

func TestCaptureProxyAcceptsPerAttemptBasicCredentials(t *testing.T) {
	var calls atomic.Int32
	proxy := newCaptureProxy("secret", ProxyLimits{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	}), nil)
	request := proxyRequest(http.MethodGet, "https://example.com/article", "")
	request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("arivu:secret")))
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d", response.Code, calls.Load())
	}
}

func TestCaptureProxyValidatesAndForwardsHTTPWithoutCredentials(t *testing.T) {
	var forwarded *http.Request
	proxy := newCaptureProxy("secret", ProxyLimits{}, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		forwarded = request
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	}), nil)

	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, proxyRequest(http.MethodGet, "https://example.com/article", "secret"))
	if response.Code != http.StatusOK || response.Body.String() != "ok" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if forwarded == nil || forwarded.URL.String() != "https://example.com/article" || forwarded.Header.Get("Proxy-Authorization") != "" {
		t.Fatalf("forwarded request = %#v", forwarded)
	}
}

func TestCaptureProxyStripsConnectionNamedHeaders(t *testing.T) {
	var leaked string
	proxy := newCaptureProxy("secret", ProxyLimits{}, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		leaked = request.Header.Get("X-Hop-Only")
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Connection": {"X-Upstream-Hop"}, "X-Upstream-Hop": {"secret"}}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	}), nil)
	request := proxyRequest(http.MethodGet, "https://example.com/article", "secret")
	request.Header.Set("Connection", "X-Hop-Only")
	request.Header.Set("X-Hop-Only", "secret")
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if leaked != "" || response.Header().Get("X-Upstream-Hop") != "" {
		t.Fatalf("request leak=%q response leak=%q", leaked, response.Header().Get("X-Upstream-Hop"))
	}
}

func TestCaptureProxyRejectsBlockedAndUnsafeTargets(t *testing.T) {
	var calls atomic.Int32
	proxy := newCaptureProxy("secret", ProxyLimits{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, nil
	}), nil)
	for _, target := range []string{
		"http://127.0.0.1/private",
		"http://169.254.169.254/latest/meta-data",
		"https://example.com:25/",
		"file:///etc/passwd",
	} {
		response := httptest.NewRecorder()
		proxy.ServeHTTP(response, proxyRequest(http.MethodGet, target, "secret"))
		if response.Code != http.StatusForbidden {
			t.Errorf("target %q status=%d", target, response.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("blocked targets reached upstream %d times", calls.Load())
	}
}

func TestCaptureProxyEnforcesResponseAndAttemptBudgets(t *testing.T) {
	responseProxy := newCaptureProxy("secret", ProxyLimits{MaxResponseBytes: 4, MaxTotalBytes: 6}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("12345")), ContentLength: 5}, nil
	}), nil)
	response := httptest.NewRecorder()
	responseProxy.ServeHTTP(response, proxyRequest(http.MethodGet, "https://example.com/a", "secret"))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("oversized response status=%d body=%q", response.Code, response.Body.String())
	}

	var calls atomic.Int32
	attemptProxy := newCaptureProxy("secret", ProxyLimits{MaxResponseBytes: 4, MaxTotalBytes: 6}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := "1234"
		if calls.Add(1) == 2 {
			body = "123"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	}), nil)
	first := httptest.NewRecorder()
	attemptProxy.ServeHTTP(first, proxyRequest(http.MethodGet, "https://example.com/one", "secret"))
	second := httptest.NewRecorder()
	attemptProxy.ServeHTTP(second, proxyRequest(http.MethodGet, "https://example.com/two", "secret"))
	if first.Code != http.StatusOK || second.Code != http.StatusBadGateway {
		t.Fatalf("attempt statuses=%d,%d", first.Code, second.Code)
	}
}

func TestCaptureProxyConcurrentResponsesCannotOvershootAttemptBudget(t *testing.T) {
	proxy := newCaptureProxy("secret", ProxyLimits{MaxResponseBytes: 4, MaxTotalBytes: 6}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("1234")), ContentLength: -1}, nil
	}), nil)
	var wg sync.WaitGroup
	var delivered atomic.Int64
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, proxyRequest(http.MethodGet, "https://example.com/data", "secret"))
			delivered.Add(int64(response.Body.Len()))
		}()
	}
	wg.Wait()
	if delivered.Load() > 6 || proxy.totalBytes.Load() > 6 {
		t.Fatalf("delivered=%d reserved=%d", delivered.Load(), proxy.totalBytes.Load())
	}
}

func TestProxyBudgetWriterAppliesPerTunnelLimit(t *testing.T) {
	proxy := newCaptureProxy("secret", ProxyLimits{MaxResponseBytes: 4, MaxTotalBytes: 10}, nil, nil)
	var output strings.Builder
	writer := &proxyBudgetWriter{proxy: proxy, writer: &output, maxBytes: 4}
	written, err := writer.Write([]byte("12345"))
	if written != 4 || err == nil || output.String() != "1234" || proxy.totalBytes.Load() != 4 {
		t.Fatalf("written=%d err=%v output=%q total=%d", written, err, output.String(), proxy.totalBytes.Load())
	}
}

func TestCaptureProxyCloseClosesIdleUpstreamConnections(t *testing.T) {
	transport := &closeTrackingTransport{}
	proxy := newCaptureProxy("secret", ProxyLimits{}, transport, nil)
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if !transport.closed.Load() {
		t.Fatal("idle upstream connections were not closed")
	}
}

func TestCaptureProxyValidatesConnectBeforeDial(t *testing.T) {
	var dials atomic.Int32
	proxy := newCaptureProxy("secret", ProxyLimits{}, nil, func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, nil
	})
	request := proxyRequest(http.MethodConnect, "https://127.0.0.1:443", "secret")
	request.Host = "127.0.0.1:443"
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || dials.Load() != 0 {
		t.Fatalf("status=%d dials=%d", response.Code, dials.Load())
	}
}

func TestCaptureProxyConnectsOnlyAfterPublicTargetValidation(t *testing.T) {
	client, proxyClient := net.Pipe()
	proxyUpstream, upstream := net.Pipe()
	defer client.Close()
	defer upstream.Close()
	var dials atomic.Int32
	proxy := newCaptureProxy("secret", ProxyLimits{}, nil, func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "example.com:443" {
			t.Fatalf("dial %s %s", network, address)
		}
		dials.Add(1)
		return proxyUpstream, nil
	})
	request := proxyRequest(http.MethodConnect, "https://example.com:443", "secret")
	request.Host = "example.com:443"
	recorder := &hijackRecorder{ResponseRecorder: httptest.NewRecorder(), conn: proxyClient}
	done := make(chan struct{})
	go func() {
		proxy.ServeHTTP(recorder, request)
		close(done)
	}()
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	<-done
	if response.StatusCode != http.StatusOK || dials.Load() != 1 {
		t.Fatalf("status=%d dials=%d", response.StatusCode, dials.Load())
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("CONNECT tunnel remained open after proxy close")
	}
}

func TestStartCaptureProxyUsesPrivateUnixSocketAndCleansUp(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "arivu-proxy-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "capture.sock")
	proxy, err := StartCaptureProxy(t.Context(), path, "secret", ProxyLimits{})
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox does not permit Unix listeners")
	}
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o660 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket info=%v err=%v", info, err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket remains after close: %v", err)
	}
}
