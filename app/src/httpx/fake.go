package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
)

// Fake is a Client for other packages' tests. It answers from scripted
// results keyed by exact URL and records every request. It is safe for
// concurrent use.
type Fake struct {
	mu      sync.Mutex
	scripts map[string][]fakeResult
	calls   []Request
}

type fakeResult struct {
	resp Response
	err  error
}

// NewFake returns a Fake with nothing scripted: every URL fails with a
// Permanent Error until Reply or Fail is called for it.
func NewFake() *Fake {
	return &Fake{scripts: make(map[string][]fakeResult)}
}

// Reply queues r as the next result for the exact URL. Each Do consumes the
// next queued result for its URL; the last one left repeats. A Response with
// Status 304 is returned with NotModified set, and an empty FinalURL becomes
// the request URL with its query removed.
func (f *Fake) Reply(url string, r Response) {
	f.queue(url, fakeResult{resp: r})
}

// Fail queues err as the next result for the exact URL, with the same
// consumption rules as Reply.
func (f *Fake) Fail(url string, err error) {
	f.queue(url, fakeResult{err: err})
}

func (f *Fake) queue(url string, r fakeResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[url] = append(f.scripts[url], r)
}

// Calls returns the requests received so far, in order. Header maps are
// copies.
func (f *Fake) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Request, len(f.calls))
	for i, c := range f.calls {
		c.Header = c.Header.Clone()
		out[i] = c
	}
	return out
}

// Do implements Client. A cancelled ctx returns ctx.Err() without recording
// the request.
func (f *Fake) Do(ctx context.Context, req Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	req.Header = req.Header.Clone()
	f.calls = append(f.calls, req)

	queue := f.scripts[req.URL]
	if len(queue) == 0 {
		return nil, &Error{Class: Permanent, URL: fakeURL(req.URL), Err: errors.New("no scripted reply")}
	}
	next := queue[0]
	if len(queue) > 1 {
		f.scripts[req.URL] = queue[1:]
	}
	if next.err != nil {
		return nil, next.err
	}
	r := next.resp
	r.Header = r.Header.Clone()
	r.Body = append([]byte(nil), r.Body...)
	if r.Status == http.StatusNotModified {
		r.NotModified = true
	}
	if r.FinalURL == "" {
		r.FinalURL = fakeURL(req.URL)
	}
	return &r, nil
}

func fakeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return redactRaw(raw)
	}
	return redact(u)
}
