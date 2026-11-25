package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeScmProxySource serves a canned ScmProxyURL value.
type fakeScmProxySource struct{ url string }

func (f fakeScmProxySource) ScmProxyURL(context.Context) string { return f.url }

// clearProxyEnv removes proxy variables so ProxyFromEnvironment resolves to
// direct dialing regardless of the machine the suite runs on.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
}

// An explicit SCM proxy setting routes the request through that proxy: the
// proxy host sees the request, the target never does directly.
func TestSCMHTTPClient_ExplicitSettingDialsThroughProxy(t *testing.T) {
	clearProxyEnv(t)

	var proxyGot string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyGot = r.Host
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "via-proxy")
	}))
	defer proxy.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("target must not be contacted directly when a proxy is set")
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()

	client := newSCMHTTPClient(fakeScmProxySource{url: proxy.URL}, slog.Default())
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "via-proxy" {
		t.Fatalf("status=%d body=%q, want 200 via-proxy", resp.StatusCode, body)
	}
	if proxyGot == "" || !strings.HasPrefix(proxyGot, "127.0.0.1") {
		t.Fatalf("proxy handler saw Host=%q, want the target host routed through the proxy", proxyGot)
	}
}

// An empty setting keeps the standard behavior: direct dialing (with a clean
// environment) and no proxy round-trip.
func TestSCMHTTPClient_EmptySettingDialsDirect(t *testing.T) {
	clearProxyEnv(t)

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("proxy must not be consulted when the setting is empty")
		w.WriteHeader(http.StatusTeapot)
	}))
	defer proxy.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	client := newSCMHTTPClient(fakeScmProxySource{}, slog.Default())
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("direct request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
}

// mutableScmProxySource lets a test flip the setting under a live client, the
// way a settings write does in the running daemon.
type mutableScmProxySource struct {
	mu  sync.Mutex
	url string
}

func (m *mutableScmProxySource) ScmProxyURL(context.Context) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.url
}

func (m *mutableScmProxySource) set(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.url = url
}

// The daemon builds the SCM client once for its whole life, so a settings
// change must re-route the next dial on that same client — hot-apply, no
// daemon restart.
func TestSCMHTTPClient_SettingChangeAppliesWithoutRestart(t *testing.T) {
	clearProxyEnv(t)

	var proxyGot, directGot int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyGot++
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		directGot++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	src := &mutableScmProxySource{}
	client := newSCMHTTPClient(src, slog.Default())

	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("unset phase: %v", err)
	}
	resp.Body.Close()
	if directGot != 1 || proxyGot != 0 {
		t.Fatalf("unset phase: direct=%d proxy=%d, want direct dialing", directGot, proxyGot)
	}

	src.set(proxy.URL)
	resp, err = client.Get(target.URL)
	if err != nil {
		t.Fatalf("set phase: %v", err)
	}
	resp.Body.Close()
	if proxyGot != 1 {
		t.Fatalf("set phase: direct=%d proxy=%d, want the request through the proxy", directGot, proxyGot)
	}

	src.set("")
	resp, err = client.Get(target.URL)
	if err != nil {
		t.Fatalf("cleared phase: %v", err)
	}
	resp.Body.Close()
	if directGot != 2 || proxyGot != 1 {
		t.Fatalf("cleared phase: direct=%d proxy=%d, want direct dialing again", directGot, proxyGot)
	}
}

// The client built for the SCM providers always carries the 30s timeout the
// providers' default branches would have applied: an injected client is taken
// verbatim, so the wiring owns the deadline.
func TestSCMHTTPClient_CarriesProviderTimeout(t *testing.T) {
	clearProxyEnv(t)
	client := newSCMHTTPClient(fakeScmProxySource{}, slog.Default())
	if client.Timeout != scmHTTPTimeout {
		t.Fatalf("timeout=%v, want %v", client.Timeout, scmHTTPTimeout)
	}
}

// fakeRoundTripper answers each attempt with the canned errors in order,
// repeating the last one; a nil entry means success.
type fakeRoundTripper struct {
	mu       sync.Mutex
	attempts int
	results  []error
}

func (f *fakeRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.attempts
	f.attempts++
	if i < len(f.results) && f.results[i] != nil {
		return nil, f.results[i]
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

func (f *fakeRoundTripper) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

func scmDialErr() error {
	return &net.OpError{Op: "dial", Err: errors.New("connect: connection refused")}
}

func scmResetErr() error {
	return &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}
}

func scmRetryRequest(t *testing.T, method string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "https://api.github.com/x", body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return req
}

// A dial-level failure means the server never saw the request, so a mutating
// request must be retried with its body replayed.
func TestSCMRetryTransport_DialFailureRetriesMutations(t *testing.T) {
	rt := &fakeRoundTripper{results: []error{scmDialErr(), scmDialErr(), nil}}
	tr := &scmRetryTransport{base: rt, maxRetries: 2, sleep: func(time.Duration) {}}
	req := scmRetryRequest(t, http.MethodPost, strings.NewReader("payload"))
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("POST after dial failures: %v", err)
	}
	resp.Body.Close()
	if rt.count() != 3 {
		t.Fatalf("attempts = %d, want 3 (initial + 2 retries)", rt.count())
	}
}

// A reset after the request was sent leaves the mutation's outcome unknown:
// retrying could duplicate a side effect (a second review), so a POST is
// never retried on a non-dial transport error.
func TestSCMRetryTransport_NoMutationRetryAfterConnectionReset(t *testing.T) {
	rt := &fakeRoundTripper{results: []error{scmResetErr()}}
	tr := &scmRetryTransport{base: rt, maxRetries: 2, sleep: func(time.Duration) {}}
	req := scmRetryRequest(t, http.MethodPost, strings.NewReader("payload"))
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("POST reset must surface as an error")
	}
	if rt.count() != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry after the request was sent)", rt.count())
	}
}

// GETs are idempotent and their NoBody is replayable, so a reset mid-flight
// is retried.
func TestSCMRetryTransport_RetriesGETOnConnectionReset(t *testing.T) {
	rt := &fakeRoundTripper{results: []error{scmResetErr(), nil}}
	tr := &scmRetryTransport{base: rt, maxRetries: 2, sleep: func(time.Duration) {}}
	// The SCM client builds GETs with http.NoBody, which carries GetBody == nil.
	req := scmRetryRequest(t, http.MethodGet, http.NoBody)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("GET after reset: %v", err)
	}
	resp.Body.Close()
	if rt.count() != 2 {
		t.Fatalf("attempts = %d, want 2", rt.count())
	}
}

// A body that cannot be replayed must never be retried — a retry would send
// an empty or truncated payload.
func TestSCMRetryTransport_NoRetryWithoutReplayableBody(t *testing.T) {
	rt := &fakeRoundTripper{results: []error{scmResetErr(), nil}}
	tr := &scmRetryTransport{base: rt, maxRetries: 2, sleep: func(time.Duration) {}}
	req := scmRetryRequest(t, http.MethodPost, struct{ io.Reader }{strings.NewReader("opaque")})
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("request must fail without a replayable body")
	}
	if rt.count() != 1 {
		t.Fatalf("attempts = %d, want 1", rt.count())
	}
}

// A canceled context stops the retry loop.
func TestSCMRetryTransport_StopsOnCanceledContext(t *testing.T) {
	rt := &fakeRoundTripper{results: []error{scmDialErr()}}
	tr := &scmRetryTransport{base: rt, maxRetries: 2, sleep: func(time.Duration) {}}
	ctx, cancel := context.WithCancel(context.Background())
	req := scmRetryRequest(t, http.MethodGet, nil)
	req = req.WithContext(ctx)
	cancel()
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("canceled request must surface as an error")
	}
	if rt.count() != 1 {
		t.Fatalf("attempts = %d, want 1 after cancellation", rt.count())
	}
}

// The wiring builds the transport with the fast-fail budgets: a dead route
// must cost seconds at dial/TLS/header time, not the legacy 30s dial plus an
// unbounded header wait.
func TestSCMHTTPClient_CarriesFastFailTimeouts(t *testing.T) {
	clearProxyEnv(t)
	client := newSCMHTTPClient(fakeScmProxySource{}, slog.Default())
	tr, ok := client.Transport.(*scmRetryTransport)
	if !ok {
		t.Fatalf("transport = %T, want *scmRetryTransport", client.Transport)
	}
	base, ok := tr.base.(*http.Transport)
	if !ok {
		t.Fatalf("base transport = %T, want *http.Transport", tr.base)
	}
	if base.DialContext == nil {
		t.Fatal("DialContext must be set on the transport")
	}
	if scmDialer.Timeout != scmDialTimeout {
		t.Fatalf("dial timeout = %v, want %v", scmDialer.Timeout, scmDialTimeout)
	}
	if base.TLSHandshakeTimeout != scmTLSHandshakeTimeout {
		t.Fatalf("TLS handshake timeout = %v, want %v", base.TLSHandshakeTimeout, scmTLSHandshakeTimeout)
	}
	if base.ResponseHeaderTimeout != scmResponseHeaderTimeout {
		t.Fatalf("response header timeout = %v, want %v", base.ResponseHeaderTimeout, scmResponseHeaderTimeout)
	}
	if base.MaxIdleConnsPerHost != scmMaxIdleConnsPerHost {
		t.Fatalf("max idle conns per host = %d, want %d", base.MaxIdleConnsPerHost, scmMaxIdleConnsPerHost)
	}
}

// The wired client retries a GET whose connection was reset mid-flight and
// then succeeds: a transient reset must not cost the request.
func TestSCMHTTPClient_RetriesGETAcrossResetConnection(t *testing.T) {
	clearProxyEnv(t)
	var attempts atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("handler does not support hijack")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close()
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	client := newSCMHTTPClient(fakeScmProxySource{}, slog.Default())
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("GET after reset: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status=%d body=%q, want 200 ok", resp.StatusCode, body)
	}
	if attempts.Load() != 2 {
		t.Fatalf("server attempts = %d, want 2 (reset then success)", attempts.Load())
	}
}
