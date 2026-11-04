package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Defaults for the zero fields of Config.
const (
	defaultTimeout      = 20 * time.Second
	defaultMaxBody      = 16 << 20
	defaultMaxRedirects = 5
	defaultMaxAttempts  = 3
	defaultBaseBackoff  = 500 * time.Millisecond
	defaultMaxBackoff   = 30 * time.Second
	defaultRate         = 2.0

	// maxRetryAfter caps any Retry-After value a server sends.
	maxRetryAfter = time.Hour
	// defaultThrottle is the cooldown after a 429 with no usable Retry-After.
	defaultThrottle = time.Minute
	// drainLimit is how much of an unwanted response body is read so the
	// connection can be reused.
	drainLimit = 4 << 10
)

// Config configures New. The zero value of every field except UserAgent
// selects the default shown in brackets.
type Config struct {
	// UserAgent is sent on every request. Required.
	UserAgent string
	// Timeout limits one request, including reading its body [20s]. A
	// redirect hop is a request of its own.
	Timeout time.Duration
	// MaxBody is the largest response body accepted, after decompression
	// [16 MiB].
	MaxBody int64
	// MaxRedirects is how many redirects one attempt follows [5].
	MaxRedirects int
	// MaxAttempts is how many times a retryable failure is tried in all [3].
	MaxAttempts int
	// BaseBackoff and MaxBackoff shape the wait before a retry:
	// BaseBackoff*2^attempt capped at MaxBackoff, then full jitter
	// [500ms, 30s].
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	// DefaultRate is the requests per second allowed for each host group [2].
	// Burst is the number of requests allowed at once; zero means DefaultRate
	// rounded up, at least 1. It also applies to the groups in HostRates.
	DefaultRate float64
	Burst       int
	// HostRates overrides DefaultRate per group key (see Groups). Keys are
	// matched case-insensitively.
	HostRates map[string]float64
	// Groups maps a host name to a group key. Hosts with the same group share
	// one rate limiter and one cooldown. A host that is not listed is its own
	// group, keyed by its lower-case name. Host names are matched
	// case-insensitively and without the port.
	Groups map[string]string
	// AllowPrivate disables the address checks. For tests only.
	AllowPrivate bool

	// Now, Sleep and Rand replace the clock, the waiting and the jitter
	// source in tests. Rand returns a value in [0,1).
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	Rand  func() float64
	// Transport replaces the default transport in tests. The default dials
	// with the address checks and ignores proxy environment variables.
	Transport http.RoundTripper
}

// HTTP is the real Client. It is safe for concurrent use.
//
// Do returns ctx.Err() itself, not an *Error, when the caller's context is
// cancelled or its deadline passes; every other failure is an *Error.
type HTTP struct {
	cfg    Config
	client *http.Client

	mu     sync.Mutex
	groups map[string]*group
}

// group is the state shared by every host mapped to one group key.
type group struct {
	limiter *rate.Limiter

	mu    sync.Mutex
	until time.Time // no request before this instant
}

var _ Client = (*HTTP)(nil)

// New checks cfg, fills in defaults and returns a client. It fails when
// UserAgent is empty or a number is negative, not a number, or infinite.
func New(cfg Config) (*HTTP, error) {
	if strings.TrimSpace(cfg.UserAgent) == "" {
		return nil, errors.New("httpx: user agent is empty")
	}
	if strings.ContainsAny(cfg.UserAgent, "\r\n\x00") {
		return nil, errors.New("httpx: user agent contains control characters")
	}
	if cfg.Timeout < 0 || cfg.MaxBody < 0 || cfg.MaxRedirects < 0 || cfg.MaxAttempts < 0 ||
		cfg.BaseBackoff < 0 || cfg.MaxBackoff < 0 || cfg.Burst < 0 {
		return nil, errors.New("httpx: negative limit in config")
	}
	if !validRate(cfg.DefaultRate, true) {
		return nil, fmt.Errorf("httpx: invalid default rate %v", cfg.DefaultRate)
	}
	hostRates := make(map[string]float64, len(cfg.HostRates))
	for k, v := range cfg.HostRates {
		if !validRate(v, false) {
			return nil, fmt.Errorf("httpx: invalid rate %v for group %q", v, k)
		}
		hostRates[strings.ToLower(k)] = v
	}
	cfg.HostRates = hostRates
	groups := make(map[string]string, len(cfg.Groups))
	for host, key := range cfg.Groups {
		groups[strings.ToLower(host)] = strings.ToLower(key)
	}
	cfg.Groups = groups

	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxBody == 0 {
		cfg.MaxBody = defaultMaxBody
	}
	if cfg.MaxRedirects == 0 {
		cfg.MaxRedirects = defaultMaxRedirects
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.BaseBackoff == 0 {
		cfg.BaseBackoff = defaultBaseBackoff
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = defaultMaxBackoff
	}
	if cfg.DefaultRate == 0 {
		cfg.DefaultRate = defaultRate
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}
	rt := cfg.Transport
	if rt == nil {
		rt = newTransport(cfg.AllowPrivate)
	}
	return &HTTP{
		cfg: cfg,
		client: &http.Client{
			Transport: rt,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		groups: make(map[string]*group),
	}, nil
}

func validRate(r float64, zeroOK bool) bool {
	if math.IsNaN(r) || math.IsInf(r, 0) || r < 0 {
		return false
	}
	return r > 0 || zeroOK
}

func newTransport(allowPrivate bool) *http.Transport {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		d.Control = denyPrivate
	}
	return &http.Transport{
		Proxy:               nil,
		DialContext:         d.DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 4,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Do performs a GET. It waits for the host group's rate limiter and
// cooldown, retries network errors and 5xx responses with backoff, and never
// retries a 429. Failures are *Error with the query string and userinfo
// removed from the URL; see the package types for the classes.
func (h *HTTP) Do(ctx context.Context, req Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start, err := h.prepare(req)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		resp, err := h.attempt(ctx, start, req)
		if err == nil {
			return resp, nil
		}
		var he *Error
		if !errors.As(err, &he) || he.Class != Retryable || attempt+1 >= h.cfg.MaxAttempts {
			return nil, err
		}
		wait := max(h.backoff(attempt), he.RetryAfter)
		if err := h.cfg.Sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// prepare parses and checks the request before anything is sent.
func (h *HTTP) prepare(req Request) (*url.URL, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, &Error{Class: Permanent, URL: redactRaw(req.URL), Err: errors.New("invalid URL")}
	}
	if e := h.checkURL(u); e != nil {
		return nil, e
	}
	bad := func(s string) bool { return strings.ContainsAny(s, "\r\n\x00") }
	if bad(req.ETag) || bad(req.LastModified) {
		return nil, newError(Permanent, 0, 0, u, errors.New("invalid conditional header value"))
	}
	for k, vs := range req.Header {
		if bad(k) || strings.Contains(k, ":") || strings.Contains(k, " ") {
			return nil, newError(Permanent, 0, 0, u, errors.New("invalid header name"))
		}
		for _, v := range vs {
			if bad(v) {
				return nil, newError(Permanent, 0, 0, u, errors.New("invalid header value"))
			}
		}
	}
	return u, nil
}

// checkURL applies the scheme and address rules to one hop.
func (h *HTTP) checkURL(u *url.URL) *Error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return newError(Permanent, 0, 0, u, fmt.Errorf("unsupported scheme %q", u.Scheme))
	}
	host := u.Hostname()
	if host == "" {
		return newError(Permanent, 0, 0, u, errors.New("missing host"))
	}
	if !h.cfg.AllowPrivate {
		if a, err := netip.ParseAddr(host); err == nil && isDeniedAddr(a) {
			return newError(Permanent, 0, 0, u, &addrError{addr: a.String()})
		}
	}
	return nil
}

// backoff is the jittered wait before retry number attempt+1.
func (h *HTTP) backoff(attempt int) time.Duration {
	ceiling := h.cfg.MaxBackoff
	if attempt < 31 {
		if d := h.cfg.BaseBackoff << attempt; d > 0 && d < ceiling {
			ceiling = d
		}
	}
	r := min(max(h.cfg.Rand(), 0), 1)
	return time.Duration(r * float64(ceiling))
}

// attempt performs one try: the request and every redirect hop.
func (h *HTTP) attempt(ctx context.Context, start *url.URL, req Request) (*Response, error) {
	cur := start
	for hops := 0; ; hops++ {
		if e := h.checkURL(cur); e != nil {
			return nil, e
		}
		g := h.groupFor(cur.Hostname())
		if err := h.wait(ctx, g); err != nil {
			return nil, err
		}
		rep, err := h.get(ctx, cur, h.header(req, strings.EqualFold(cur.Host, start.Host)))
		if err != nil {
			return nil, err
		}
		switch s := rep.status; {
		case s >= 200 && s < 300:
			return &Response{Status: s, Header: rep.header, Body: rep.body, FinalURL: redact(cur)}, nil
		case s == http.StatusNotModified:
			return &Response{Status: s, Header: rep.header, NotModified: true, FinalURL: redact(cur)}, nil
		case s >= 300 && s < 400:
			next, err := h.redirect(cur, rep, hops)
			if err != nil {
				return nil, err
			}
			cur = next
		case s == http.StatusTooManyRequests:
			d, ok := parseRetryAfter(rep.header.Get("Retry-After"), h.cfg.Now())
			if !ok || d == 0 {
				d = defaultThrottle
			}
			h.cooldown(g, d)
			return nil, newError(Throttled, s, d, cur, nil)
		case s == http.StatusForbidden:
			return nil, newError(Blocked, s, 0, cur, nil)
		case s >= 500 && s < 600:
			d, ok := parseRetryAfter(rep.header.Get("Retry-After"), h.cfg.Now())
			if ok && d > h.cfg.MaxBackoff {
				h.cooldown(g, d)
				return nil, newError(Throttled, s, d, cur, nil)
			}
			return nil, newError(Retryable, s, d, cur, nil)
		default:
			return nil, newError(Permanent, s, 0, cur, nil)
		}
	}
}

// redirect resolves the Location of a 3xx reply into the next URL.
func (h *HTTP) redirect(cur *url.URL, rep *reply, hops int) (*url.URL, error) {
	loc := strings.TrimSpace(rep.header.Get("Location"))
	if loc == "" {
		return nil, newError(Permanent, rep.status, 0, cur, errors.New("redirect without a Location"))
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return nil, newError(Permanent, rep.status, 0, cur, errors.New("redirect to an invalid Location"))
	}
	if hops >= h.cfg.MaxRedirects {
		return nil, newError(Permanent, rep.status, 0, cur, errors.New("too many redirects"))
	}
	return cur.ResolveReference(ref), nil
}

// header builds the request headers. Custom headers go only to the host the
// caller named, never to a host a redirect leads to.
func (h *HTTP) header(req Request, sameHost bool) http.Header {
	hdr := make(http.Header)
	if sameHost {
		for k, vs := range req.Header {
			switch http.CanonicalHeaderKey(k) {
			case "User-Agent", "Host", "Accept-Encoding":
				continue
			}
			hdr[http.CanonicalHeaderKey(k)] = append([]string(nil), vs...)
		}
	}
	hdr.Set("User-Agent", h.cfg.UserAgent)
	if req.ETag != "" {
		hdr.Set("If-None-Match", req.ETag)
	}
	if req.LastModified != "" {
		hdr.Set("If-Modified-Since", req.LastModified)
	}
	return hdr
}

// reply is one HTTP exchange; body is set only for 2xx.
type reply struct {
	status int
	header http.Header
	body   []byte
}

// get sends one request and reads the body of a 2xx reply, within the
// per-request timeout.
func (h *HTTP) get(ctx context.Context, u *url.URL, hdr http.Header) (*reply, error) {
	rctx, cancel := context.WithTimeout(ctx, h.cfg.Timeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(rctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, newError(Permanent, 0, 0, u, stripURLError(err))
	}
	hr.Header = hdr
	resp, err := h.client.Do(hr)
	if err != nil {
		return nil, h.transportError(ctx, u, err)
	}
	defer resp.Body.Close()
	rep := &reply{status: resp.StatusCode, header: resp.Header}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, drainLimit))
		return rep, nil
	}
	tooLarge := newError(Permanent, resp.StatusCode, 0, u, errors.New("body too large"))
	if resp.ContentLength > h.cfg.MaxBody {
		return nil, tooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, h.cfg.MaxBody+1))
	if err != nil {
		return nil, h.transportError(ctx, u, err)
	}
	if int64(len(body)) > h.cfg.MaxBody {
		return nil, tooLarge
	}
	rep.body = body
	return rep, nil
}

// transportError classifies a failure to send or read: a cancelled caller
// context is returned as is, a refused address is permanent, anything else is
// worth retrying.
func (h *HTTP) transportError(ctx context.Context, u *url.URL, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	err = stripURLError(err)
	var ae *addrError
	switch {
	case errors.As(err, &ae):
		return newError(Permanent, 0, 0, u, ae)
	case errors.Is(err, context.DeadlineExceeded):
		return newError(Retryable, 0, 0, u, fmt.Errorf("timed out after %s", h.cfg.Timeout))
	}
	return newError(Retryable, 0, 0, u, err)
}

// groupFor returns the shared state for a host name.
func (h *HTTP) groupFor(host string) *group {
	key := strings.ToLower(host)
	if k, ok := h.cfg.Groups[key]; ok {
		key = k
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if g, ok := h.groups[key]; ok {
		return g
	}
	r, ok := h.cfg.HostRates[key]
	if !ok {
		r = h.cfg.DefaultRate
	}
	burst := h.cfg.Burst
	if burst == 0 {
		burst = max(1, int(math.Ceil(r)))
	}
	g := &group{limiter: rate.NewLimiter(rate.Limit(r), burst)}
	h.groups[key] = g
	return g
}

// cooldown stops every request to the group for d from now.
func (h *HTTP) cooldown(g *group, d time.Duration) {
	until := h.cfg.Now().Add(d)
	g.mu.Lock()
	defer g.mu.Unlock()
	if until.After(g.until) {
		g.until = until
	}
}

// wait blocks until the group's cooldown is over and its limiter grants a
// request. It returns ctx's error when ctx ends first.
func (h *HTTP) wait(ctx context.Context, g *group) error {
	reserved := false
	for {
		g.mu.Lock()
		remaining := g.until.Sub(h.cfg.Now())
		g.mu.Unlock()
		if remaining > 0 {
			if err := h.cfg.Sleep(ctx, remaining); err != nil {
				return err
			}
			continue
		}
		if reserved {
			return nil
		}
		now := h.cfg.Now()
		r := g.limiter.ReserveN(now, 1)
		reserved = true
		if d := r.DelayFrom(now); d > 0 {
			if err := h.cfg.Sleep(ctx, d); err != nil {
				r.CancelAt(h.cfg.Now())
				return err
			}
		}
	}
}

// parseRetryAfter reads a Retry-After value, either delta-seconds or an HTTP
// date, and clamps it to an hour. A negative, past or unparsable value is
// reported as absent.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if strings.Trim(v, "0123456789") == "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter, true
		}
		return time.Duration(n) * time.Second, true
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return 0, false
	}
	d := t.Sub(now)
	if d < 0 {
		return 0, false
	}
	return min(d, maxRetryAfter), true
}

func newError(class Class, status int, retryAfter time.Duration, u *url.URL, err error) *Error {
	return &Error{Class: class, Status: status, RetryAfter: retryAfter, URL: redact(u), Err: err}
}

// redact returns u without userinfo, query string and fragment.
func redact(u *url.URL) string {
	c := *u
	c.User = nil
	c.RawQuery = ""
	c.ForceQuery = false
	c.Fragment = ""
	c.RawFragment = ""
	return c.String()
}

// redactRaw does the same for a string that did not parse as a URL.
func redactRaw(raw string) string {
	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return "(invalid URL)"
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	rest := raw[scheme+3:]
	end := strings.IndexByte(rest, '/')
	if end < 0 {
		end = len(rest)
	}
	if at := strings.LastIndexByte(rest[:end], '@'); at >= 0 {
		raw = raw[:scheme+3] + rest[at+1:]
	}
	return raw
}

// stripURLError removes the *url.Error wrapper, whose message repeats the
// full URL including its query string.
func stripURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
