package httpx

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testUA = "stoxicity-test/1 (test@example.com)"

// clock is a fake clock whose Sleep advances time and records the wait.
type clock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	return nil
}

func (c *clock) Waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

// newClient builds a client on the fake clock with loopback allowed, a high
// rate and a jitter of one half. edit adjusts the config before New.
func newClient(t *testing.T, edit func(*Config)) (*HTTP, *clock) {
	t.Helper()
	clk := newClock()
	cfg := Config{
		UserAgent:    testUA,
		AllowPrivate: true,
		DefaultRate:  1000,
		Now:          clk.Now,
		Sleep:        clk.Sleep,
		Rand:         func() float64 { return 0.5 },
	}
	if edit != nil {
		edit(&cfg)
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, clk
}

// routeTo returns a transport that sends every request, whatever its host
// name, to srv. It lets tests use public-looking names without a network.
func routeTo(srv *httptest.Server) http.RoundTripper {
	var d net.Dialer
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}
}

func asError(t *testing.T, err error) *Error {
	t.Helper()
	var he *Error
	if !errors.As(err, &he) {
		t.Fatalf("error %v (%T) is not *Error", err, err)
	}
	return he
}

func TestNewValidation(t *testing.T) {
	for name, cfg := range map[string]Config{
		"empty user agent":    {},
		"blank user agent":    {UserAgent: "  "},
		"newline in agent":    {UserAgent: "a\r\nX: 1"},
		"negative timeout":    {UserAgent: "a", Timeout: -1},
		"negative rate":       {UserAgent: "a", DefaultRate: -1},
		"bad group rate":      {UserAgent: "a", HostRates: map[string]float64{"sec": 0}},
		"negative group rate": {UserAgent: "a", HostRates: map[string]float64{"sec": -2}},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := New(Config{UserAgent: "a"}); err != nil {
		t.Errorf("minimal config rejected: %v", err)
	}
}

func TestSuccessGzip(t *testing.T) {
	var sawGzip atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawGzip.Store(strings.Contains(r.Header.Get("Accept-Encoding"), "gzip"))
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("X-Thing", "yes")
		zw := gzip.NewWriter(w)
		zw.Write([]byte("hello world"))
		zw.Close()
	}))
	defer srv.Close()
	h, _ := newClient(t, nil)
	resp, err := h.Do(context.Background(), Request{URL: srv.URL + "/feed?apikey=SECRET#frag"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 || string(resp.Body) != "hello world" || resp.NotModified {
		t.Errorf("response = %d %q notmodified=%v", resp.Status, resp.Body, resp.NotModified)
	}
	if resp.FinalURL != srv.URL+"/feed" {
		t.Errorf("FinalURL = %q", resp.FinalURL)
	}
	if resp.Header.Get("X-Thing") != "yes" {
		t.Errorf("headers not passed back: %v", resp.Header)
	}
	if !sawGzip.Load() {
		t.Error("transport did not negotiate gzip")
	}
}

func TestConditionalHeaders(t *testing.T) {
	var gotINM, gotIMS atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM.Store(r.Header.Get("If-None-Match"))
		gotIMS.Store(r.Header.Get("If-Modified-Since"))
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write([]byte("fresh"))
	}))
	defer srv.Close()
	h, _ := newClient(t, nil)

	resp, err := h.Do(context.Background(), Request{URL: srv.URL})
	if err != nil || string(resp.Body) != "fresh" || gotINM.Load() != "" || gotIMS.Load() != "" {
		t.Fatalf("unconditional: %v %v inm=%q ims=%q", resp, err, gotINM.Load(), gotIMS.Load())
	}
	lm := "Sun, 01 Mar 2026 10:00:00 GMT"
	resp, err = h.Do(context.Background(), Request{URL: srv.URL, ETag: `"abc"`, LastModified: lm})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.NotModified || resp.Status != 304 || len(resp.Body) != 0 {
		t.Errorf("response = %+v", resp)
	}
	if gotINM.Load() != `"abc"` || gotIMS.Load() != lm {
		t.Errorf("headers sent: inm=%q ims=%q", gotINM.Load(), gotIMS.Load())
	}
}

func TestUserAgentEnforced(t *testing.T) {
	var mu sync.Mutex
	var got http.Header
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got, gotHost = r.Header.Clone(), r.Host
		mu.Unlock()
	}))
	defer srv.Close()
	h, _ := newClient(t, nil)
	_, err := h.Do(context.Background(), Request{URL: srv.URL, Header: http.Header{
		"User-Agent":      {"evil/1"},
		"user-agent":      {"evil/2"},
		"Host":            {"evil.example"},
		"Accept-Encoding": {"br"},
		"X-Custom":        {"one", "two"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got.Get("User-Agent") != testUA || len(got.Values("User-Agent")) != 1 {
		t.Errorf("User-Agent = %q", got.Values("User-Agent"))
	}
	if gotHost == "evil.example" {
		t.Error("Host override accepted")
	}
	if got.Get("Accept-Encoding") != "gzip" {
		t.Errorf("Accept-Encoding = %q, want the transport's gzip", got.Get("Accept-Encoding"))
	}
	if v := got.Values("X-Custom"); len(v) != 2 {
		t.Errorf("X-Custom = %v", v)
	}
}

func TestRetrySequence(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	h, clk := newClient(t, nil)
	resp, err := h.Do(context.Background(), Request{URL: srv.URL})
	if err != nil || string(resp.Body) != "ok" {
		t.Fatalf("%v %v", resp, err)
	}
	// Jitter is 0.5: half of 500ms, then half of 1s.
	want := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}
	if got := clk.Waits(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("waits = %v, want %v", got, want)
	}
}

func TestRetriesExhausted(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.WriteHeader(status)
		}))
		h, clk := newClient(t, nil)
		_, err := h.Do(context.Background(), Request{URL: srv.URL})
		srv.Close()
		he := asError(t, err)
		if he.Class != Retryable || he.Status != status {
			t.Errorf("%d: %+v", status, he)
		}
		if n.Load() != 3 || len(clk.Waits()) != 2 {
			t.Errorf("%d: %d requests, waits %v", status, n.Load(), clk.Waits())
		}
	}
}

func TestBackoffCapped(t *testing.T) {
	h, _ := newClient(t, func(c *Config) {
		c.BaseBackoff = time.Second
		c.MaxBackoff = 3 * time.Second
		c.Rand = func() float64 { return 0.9999999 }
	})
	for attempt, wantMax := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second, 3 * time.Second} {
		got := h.backoff(attempt)
		if got > wantMax || got < wantMax-time.Millisecond {
			t.Errorf("attempt %d: backoff %v, want about %v", attempt, got, wantMax)
		}
	}
	if got := h.backoff(200); got > 3*time.Second {
		t.Errorf("huge attempt: %v", got)
	}
}

func TestRetryAfterOnServerError(t *testing.T) {
	clk0 := newClock()
	tests := []struct {
		name      string
		header    string
		wantWaits []time.Duration
		wantClass Class
		wantRA    time.Duration
		requests  int32
	}{
		{"seconds beat backoff", "2", []time.Duration{2 * time.Second, 2 * time.Second}, Retryable, 2 * time.Second, 3},
		// The date is absolute: after the first wait it has passed.
		{"date", clk0.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat), []time.Duration{3 * time.Second, 500 * time.Millisecond}, Retryable, 0, 3},
		{"smaller than backoff", "0", []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}, Retryable, 0, 3},
		{"negative is absent", "-5", []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}, Retryable, 0, 3},
		{"garbage is absent", "soon", []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}, Retryable, 0, 3},
		{"past date is absent", clk0.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}, Retryable, 0, 3},
		{"above max backoff is throttled", "120", nil, Throttled, 2 * time.Minute, 1},
		{"clamped to an hour", "999999999999999999999", nil, Throttled, time.Hour, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n.Add(1)
				w.Header().Set("Retry-After", tc.header)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			h, clk := newClient(t, nil)
			_, err := h.Do(context.Background(), Request{URL: srv.URL})
			he := asError(t, err)
			if he.Class != tc.wantClass || he.RetryAfter != tc.wantRA || he.Status != 503 {
				t.Errorf("error = %+v", he)
			}
			if n.Load() != tc.requests {
				t.Errorf("%d requests, want %d", n.Load(), tc.requests)
			}
			if got := clk.Waits(); fmt.Sprint(got) != fmt.Sprint(tc.wantWaits) {
				t.Errorf("waits = %v, want %v", got, tc.wantWaits)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 0, false},
		{"0", 0, true},
		{"120", 2 * time.Minute, true},
		{" 7 ", 7 * time.Second, true},
		{"3600", time.Hour, true},
		{"3601", time.Hour, true},
		{"-1", 0, false},
		{"+5", 0, false},
		{"1.5", 0, false},
		{"tomorrow", 0, false},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second, true},
		{now.Add(48 * time.Hour).Format(http.TimeFormat), time.Hour, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, false},
	}
	for _, tc := range tests {
		got, ok := parseRetryAfter(tc.in, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseRetryAfter(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestThrottledCooldownPerGroup(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.Host]++
		first := hits[r.Host] == 1
		mu.Unlock()
		if r.Host == "a.test" && first {
			w.Header().Set("Retry-After", "10")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	h, clk := newClient(t, func(c *Config) {
		c.Transport = routeTo(srv)
		c.Groups = map[string]string{"A.test": "g", "a2.test": "G"}
	})
	ctx := context.Background()

	_, err := h.Do(ctx, Request{URL: "http://a.test/x?token=SECRET"})
	he := asError(t, err)
	if he.Class != Throttled || he.RetryAfter != 10*time.Second || he.Status != 429 {
		t.Fatalf("error = %+v", he)
	}
	mu.Lock()
	if hits["a.test"] != 1 {
		t.Errorf("429 was retried: %d requests", hits["a.test"])
	}
	mu.Unlock()
	if len(clk.Waits()) != 0 {
		t.Fatalf("waited without a cooldown: %v", clk.Waits())
	}

	if _, err := h.Do(ctx, Request{URL: "http://b.test/"}); err != nil {
		t.Fatal(err)
	}
	if len(clk.Waits()) != 0 {
		t.Errorf("another group waited: %v", clk.Waits())
	}

	// a2.test shares the group "g" and so waits out the cooldown.
	if _, err := h.Do(ctx, Request{URL: "http://a2.test/"}); err != nil {
		t.Fatal(err)
	}
	if got := clk.Waits(); fmt.Sprint(got) != fmt.Sprint([]time.Duration{10 * time.Second}) {
		t.Errorf("waits = %v, want one 10s wait", got)
	}
	// The cooldown is over now: the same host proceeds without waiting.
	if _, err := h.Do(ctx, Request{URL: "http://a.test/x"}); err != nil {
		t.Fatal(err)
	}
	if len(clk.Waits()) != 1 {
		t.Errorf("waited again: %v", clk.Waits())
	}
}

func TestThrottledDefaultRetryAfter(t *testing.T) {
	for _, header := range []string{"", "junk", "0"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if header != "" {
				w.Header().Set("Retry-After", header)
			}
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		h, clk := newClient(t, nil)
		_, err := h.Do(context.Background(), Request{URL: srv.URL})
		he := asError(t, err)
		if he.Class != Throttled || he.RetryAfter != time.Minute {
			t.Errorf("Retry-After %q: %+v", header, he)
		}
		_, err = h.Do(context.Background(), Request{URL: srv.URL})
		srv.Close()
		if asError(t, err).Class != Throttled || fmt.Sprint(clk.Waits()) != "[1m0s]" {
			t.Errorf("Retry-After %q: second request did not wait out the cooldown: %v", header, clk.Waits())
		}
	}
}

func TestGroupsShareLimiter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	tests := []struct {
		name     string
		edit     func(*Config)
		wantWait string
	}{
		{"default rate", func(c *Config) { c.DefaultRate = 1 }, "[1s]"},
		{"group rate overrides", func(c *Config) { c.DefaultRate = 1000; c.HostRates = map[string]float64{"SEC": 0.5} }, "[2s]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, clk := newClient(t, func(c *Config) {
				c.Transport = routeTo(srv)
				c.Groups = map[string]string{"www.sec.test": "sec", "data.sec.test": "sec"}
				tc.edit(c)
			})
			ctx := context.Background()
			get := func(u string) {
				t.Helper()
				if _, err := h.Do(ctx, Request{URL: u}); err != nil {
					t.Fatal(err)
				}
			}
			get("http://www.sec.test/a")
			get("http://other.test/")
			get("http://other2.test/")
			if len(clk.Waits()) != 0 {
				t.Fatalf("waited before the group's burst was used: %v", clk.Waits())
			}
			get("http://DATA.sec.test/b")
			if got := fmt.Sprint(clk.Waits()); got != tc.wantWait {
				t.Errorf("waits = %s, want %s", got, tc.wantWait)
			}
		})
	}
}

func TestStatusClasses(t *testing.T) {
	tests := []struct {
		status int
		class  Class
	}{
		{403, Blocked},
		{401, Permanent},
		{404, Permanent},
		{410, Permanent},
		{400, Permanent},
		{418, Permanent},
		{451, Permanent},
	}
	for _, tc := range tests {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.WriteHeader(tc.status)
		}))
		h, clk := newClient(t, nil)
		_, err := h.Do(context.Background(), Request{URL: srv.URL})
		srv.Close()
		he := asError(t, err)
		if he.Class != tc.class || he.Status != tc.status {
			t.Errorf("%d: %+v", tc.status, he)
		}
		if n.Load() != 1 || len(clk.Waits()) != 0 {
			t.Errorf("%d: retried (%d requests, waits %v)", tc.status, n.Load(), clk.Waits())
		}
	}
}

func TestRedirectWithoutLocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	h, _ := newClient(t, nil)
	_, err := h.Do(context.Background(), Request{URL: srv.URL})
	if he := asError(t, err); he.Class != Permanent || he.Status != 302 {
		t.Errorf("error = %+v", he)
	}
}

func TestBodyCap(t *testing.T) {
	big := func(n int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { w.Write(bytes.Repeat([]byte("x"), n)) }
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exact":
			big(10)(w, r)
		case "/over":
			big(11)(w, r)
		case "/chunked":
			w.(http.Flusher).Flush()
			big(1000)(w, r)
		case "/gzipbomb":
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			zw.Write(bytes.Repeat([]byte("0"), 1<<20))
			zw.Close()
		}
	}))
	defer srv.Close()
	h, clk := newClient(t, func(c *Config) { c.MaxBody = 10 })
	resp, err := h.Do(context.Background(), Request{URL: srv.URL + "/exact"})
	if err != nil || len(resp.Body) != 10 {
		t.Fatalf("exact: %v %v", resp, err)
	}
	for _, p := range []string{"/over", "/chunked", "/gzipbomb"} {
		_, err := h.Do(context.Background(), Request{URL: srv.URL + p})
		he := asError(t, err)
		if he.Class != Permanent || !strings.Contains(he.Error(), "body too large") {
			t.Errorf("%s: %v", p, err)
		}
	}
	if len(clk.Waits()) != 0 {
		t.Errorf("oversize body was retried: %v", clk.Waits())
	}
}

func TestRedirects(t *testing.T) {
	var loops atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a":
			http.Redirect(w, r, "/b?token=SECRET", http.StatusMovedPermanently)
		case "/b":
			w.Write([]byte("arrived"))
		case "/loop":
			loops.Add(1)
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer srv.Close()
	h, _ := newClient(t, func(c *Config) { c.MaxRedirects = 3 })

	resp, err := h.Do(context.Background(), Request{URL: srv.URL + "/a?first=1"})
	if err != nil || string(resp.Body) != "arrived" || resp.FinalURL != srv.URL+"/b" {
		t.Fatalf("follow: %+v %v", resp, err)
	}

	_, err = h.Do(context.Background(), Request{URL: srv.URL + "/loop"})
	he := asError(t, err)
	if he.Class != Permanent || !strings.Contains(he.Error(), "too many redirects") {
		t.Errorf("loop: %v", err)
	}
	if loops.Load() != 4 {
		t.Errorf("loop requests = %d, want 1 + 3 redirects", loops.Load())
	}
}

func TestCustomHeadersNotForwardedAcrossHosts(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path+"@"+r.Host] = r.Header.Clone()
		mu.Unlock()
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/same", http.StatusFound)
		case "/same":
			http.Redirect(w, r, "http://b.test/other", http.StatusFound)
		}
	}))
	defer srv.Close()
	h, _ := newClient(t, func(c *Config) { c.Transport = routeTo(srv) })
	_, err := h.Do(context.Background(), Request{
		URL:    "http://a.test/start",
		Header: http.Header{"Authorization": {"Bearer x"}, "X-Api-Key": {"k"}},
		ETag:   `"v1"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["/same@a.test"].Get("Authorization") == "" {
		t.Error("same-host redirect lost the caller's headers")
	}
	other := seen["/other@b.test"]
	if other == nil {
		t.Fatalf("second host not reached: %v", seen)
	}
	if other.Get("Authorization") != "" || other.Get("X-Api-Key") != "" {
		t.Errorf("custom headers leaked to another host: %v", other)
	}
	if other.Get("User-Agent") != testUA {
		t.Errorf("User-Agent on the other host = %q", other.Get("User-Agent"))
	}
}

type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("request sent to %s", r.URL.Redacted())
	return nil, errors.New("must not be called")
}

func TestRefusedURLs(t *testing.T) {
	h, _ := newClient(t, func(c *Config) {
		c.AllowPrivate = false
		c.Transport = failTransport{t}
	})
	for _, u := range []string{
		"http://127.0.0.1:8080/",
		"http://127.1.2.3/",
		"http://10.0.0.1/",
		"http://172.16.5.4/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://[::]/",
		"http://0.0.0.0/",
		"http://100.64.0.1/",
		"http://100.127.255.255/",
		"http://224.0.0.1/",
		"http://[ff02::1]/",
		"http://[fe80::1]/",
		"http://[fc00::1]/",
		"http://[fd12:3456::1]/",
		"http://[::ffff:127.0.0.1]/",
		"http://[::ffff:10.0.0.1]/",
		"http://[::ffff:169.254.169.254]/",
		"http://[64:ff9b::a9fe:a9fe]/",
		"https://user:pw@10.0.0.1/",
		"ftp://example.com/file",
		"file:///etc/passwd",
		"example.com/no-scheme",
		"http:///nohost",
	} {
		_, err := h.Do(context.Background(), Request{URL: u})
		he := asError(t, err)
		if he.Class != Permanent || strings.Contains(he.Error(), "pw") {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestIsDeniedAddr(t *testing.T) {
	tests := []struct {
		addr string
		deny bool
	}{
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.32.0.1", false},
		{"172.15.255.255", false},
		{"100.63.255.255", false},
		{"100.128.0.1", false},
		{"2606:4700:4700::1111", false},
		{"::ffff:8.8.8.8", false},
		{"64:ff9b::808:808", false},
		{"127.0.0.1", true},
		{"172.31.255.255", true},
		{"100.64.0.0", true},
		{"169.254.0.1", true},
		{"0.1.2.3", true},
		{"255.255.255.255", true},
		{"fe80::1%eth0", true},
		{"::ffff:192.168.0.1", true},
		{"::ffff:100.64.0.1", true},
		{"fc00::", true},
		{"ff02::1", true},
	}
	for _, tc := range tests {
		a, err := netip.ParseAddr(tc.addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := isDeniedAddr(a); got != tc.deny {
			t.Errorf("isDeniedAddr(%s) = %v, want %v", tc.addr, got, tc.deny)
		}
	}
}

func TestDialTimeAddressCheck(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	h, clk := newClient(t, func(c *Config) { c.AllowPrivate = false })
	// "localhost" passes the up-front check and is refused only when the
	// dialer sees the loopback address it resolved to.
	_, err := h.Do(context.Background(), Request{URL: "http://localhost:" + port + "/?k=SECRET"})
	he := asError(t, err)
	if he.Class != Permanent || !strings.Contains(he.Error(), "not allowed") || strings.Contains(he.Error(), "SECRET") {
		t.Errorf("error = %v", err)
	}
	if hits.Load() != 0 || len(clk.Waits()) != 0 {
		t.Errorf("connection made or retried: %d hits, waits %v", hits.Load(), clk.Waits())
	}
}

func TestRedirectToMetadataRefused(t *testing.T) {
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[::ffff:a9fe:a9fe]/",
		"http://10.0.0.1/",
		"ftp://example.com/x",
	} {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			http.Redirect(w, r, target, http.StatusFound)
		}))
		h, _ := newClient(t, func(c *Config) {
			c.AllowPrivate = false
			c.Transport = routeTo(srv)
		})
		_, err := h.Do(context.Background(), Request{URL: "http://public.example/start"})
		srv.Close()
		he := asError(t, err)
		if he.Class != Permanent {
			t.Errorf("%s: %+v", target, he)
		}
		if hits.Load() != 1 {
			t.Errorf("%s: %d requests reached the server", target, hits.Load())
		}
	}
}

func TestErrorsHideSecrets(t *testing.T) {
	const secret = "SECRETKEY"
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/404":
			w.WriteHeader(404)
		case "/503":
			w.WriteHeader(503)
		case "/redirect":
			http.Redirect(w, r, "/redirect?next="+secret, http.StatusFound)
		}
	}))
	defer srv.Close()
	userinfo := strings.Replace(srv.URL, "http://", "http://bob:hunter2@", 1)
	h, _ := newClient(t, func(c *Config) { c.MaxRedirects = 1; c.MaxAttempts = 2 })

	for name, u := range map[string]string{
		"404":        userinfo + "/404?apikey=" + secret,
		"503":        userinfo + "/503?apikey=" + secret,
		"redirect":   userinfo + "/redirect?apikey=" + secret,
		"refused":    strings.Replace(deadURL, "http://", "http://bob:hunter2@", 1) + "/x?apikey=" + secret,
		"bad URL":    "http://bob:hunter2@exa mple.com/x?apikey=" + secret,
		"bad scheme": "gopher://bob:hunter2@example.com/x?apikey=" + secret,
	} {
		_, err := h.Do(context.Background(), Request{URL: u, Header: http.Header{"X-Token": {secret}}})
		he := asError(t, err)
		for _, s := range []string{secret, "hunter2", "bob", "apikey"} {
			if strings.Contains(he.Error(), s) || strings.Contains(he.URL, s) {
				t.Errorf("%s: %q leaked in %q", name, s, he.Error())
			}
		}
	}
}

func TestContextCancelled(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	h, _ := newClient(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.Do(ctx, Request{URL: srv.URL})
	if err != context.Canceled {
		t.Errorf("err = %v (%T), want context.Canceled itself", err, err)
	}
	if hits.Load() != 0 {
		t.Error("request sent with a cancelled context")
	}

	ctx, cancel = context.WithTimeout(context.Background(), -time.Second)
	defer cancel()
	if _, err := h.Do(ctx, Request{URL: srv.URL}); err != context.DeadlineExceeded {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestContextCancelledInFlight(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	h, _ := newClient(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := h.Do(ctx, Request{URL: srv.URL})
	if err != context.Canceled {
		t.Errorf("err = %v (%T)", err, err)
	}
}

func TestContextCancelledWhileWaiting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	h, _ := newClient(t, func(c *Config) {
		c.Sleep = func(ctx context.Context, d time.Duration) error {
			cancel()
			return ctx.Err()
		}
	})
	_, err := h.Do(ctx, Request{URL: srv.URL})
	if err != context.Canceled {
		t.Errorf("err = %v (%T)", err, err)
	}
}

func TestAttemptTimeout(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	h, clk := newClient(t, func(c *Config) { c.Timeout = 50 * time.Millisecond; c.MaxAttempts = 2 })
	_, err := h.Do(context.Background(), Request{URL: srv.URL})
	he := asError(t, err)
	if he.Class != Retryable || !strings.Contains(he.Error(), "timed out") {
		t.Errorf("error = %v", err)
	}
	if hits.Load() != 2 || len(clk.Waits()) != 1 {
		t.Errorf("%d requests, waits %v", hits.Load(), clk.Waits())
	}
}

func TestNetworkErrorRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	h, clk := newClient(t, nil)
	_, err := h.Do(context.Background(), Request{URL: url})
	if he := asError(t, err); he.Class != Retryable || he.Status != 0 {
		t.Errorf("error = %+v", he)
	}
	if len(clk.Waits()) != 2 {
		t.Errorf("waits = %v", clk.Waits())
	}
}

func TestRealSleepAndRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	h, err := New(Config{UserAgent: testUA, AllowPrivate: true, DefaultRate: 20})
	if err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	for range 5 {
		if _, err := h.Do(context.Background(), Request{URL: srv.URL}); err != nil {
			t.Fatal(err)
		}
	}
	// Burst 20 lets five requests through at once; the limiter must not slow
	// a client that stays under its rate.
	if time.Since(begin) > 2*time.Second {
		t.Errorf("five requests took %v", time.Since(begin))
	}
}

func TestInvalidHeadersRefused(t *testing.T) {
	h, _ := newClient(t, func(c *Config) { c.Transport = failTransport{t} })
	for name, req := range map[string]Request{
		"etag":         {URL: "http://a.test/", ETag: "a\r\nX: 1"},
		"header value": {URL: "http://a.test/", Header: http.Header{"X-A": {"b\nc"}}},
		"header name":  {URL: "http://a.test/", Header: http.Header{"X A": {"b"}}},
	} {
		_, err := h.Do(context.Background(), req)
		if asError(t, err).Class != Permanent {
			t.Errorf("%s: %v", name, err)
		}
	}
}
