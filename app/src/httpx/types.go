package httpx

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Request is one GET. ETag and LastModified, when set, become the conditional
// headers If-None-Match and If-Modified-Since.
type Request struct {
	URL          string
	Header       http.Header // extra headers; the client sets User-Agent itself
	ETag         string
	LastModified string
}

// Response is a completed GET whose status was 2xx or 304.
type Response struct {
	Status      int
	Header      http.Header
	Body        []byte // at most the client's body cap; empty for 304
	NotModified bool   // the server answered 304
	FinalURL    string // after redirects, query string removed
}

// Class says how a caller should react to a failed request.
type Class int

const (
	// Retryable: network errors, timeouts, 5xx. Try again later with backoff.
	Retryable Class = iota + 1
	// Throttled: 429 or an explicit Retry-After. Wait at least RetryAfter; the
	// whole host is cooling down, not just this request.
	Throttled
	// Blocked: the server refused us on purpose (403, or a 200 whose body is
	// not what was asked for, such as an HTML rate-limit page). Back off hard.
	Blocked
	// Permanent: 404, 410, other 4xx, a refused redirect or address. Retrying
	// the same request will not help.
	Permanent
)

func (c Class) String() string {
	switch c {
	case Retryable:
		return "retryable"
	case Throttled:
		return "throttled"
	case Blocked:
		return "blocked"
	case Permanent:
		return "permanent"
	}
	return "unknown"
}

// Error is what Client.Do returns on failure. URL has its query string
// removed, so tokens in a query never reach a log.
type Error struct {
	Class      Class
	Status     int           // HTTP status, 0 if none was received
	RetryAfter time.Duration // minimum wait, 0 if the server gave none
	URL        string
	Err        error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("httpx: %s %s", e.Class, e.URL)
	if e.Status != 0 {
		msg += fmt.Sprintf(" (status %d)", e.Status)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// Client is the HTTP surface fetchers use. The real implementation limits
// rates per host, retries, honours Retry-After and refuses private addresses;
// tests use a fake.
type Client interface {
	Do(ctx context.Context, req Request) (*Response, error)
}
