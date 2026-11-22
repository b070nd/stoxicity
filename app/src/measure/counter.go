package measure

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// ErrBudget is returned once a run has used its request or byte budget. No
// request is sent after it.
var ErrBudget = errors.New("measure: budget used up")

// Counter counts the requests, bytes and failures per host of the client it
// wraps, and refuses further requests once a budget is used up. Retries happen
// inside the wrapped client and count as one request here.
type Counter struct {
	Inner       httpx.Client
	MaxRequests int   // 0: no limit
	MaxBytes    int64 // 0: no limit

	mu       sync.Mutex
	hosts    map[string]Host
	requests int
	bytes    int64
	stopped  string
}

// Do sends the request unless a budget is used up.
func (c *Counter) Do(ctx context.Context, req httpx.Request) (*httpx.Response, error) {
	host := "unknown"
	if u, err := url.Parse(req.URL); err == nil && u.Host != "" {
		host = u.Host
	}
	c.mu.Lock()
	if c.stopped != "" {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrBudget, c.stopped)
	}
	switch {
	case c.MaxRequests > 0 && c.requests >= c.MaxRequests:
		c.stopped = fmt.Sprintf("the request budget of %d was reached", c.MaxRequests)
	case c.MaxBytes > 0 && c.bytes >= c.MaxBytes:
		c.stopped = fmt.Sprintf("the download budget of %d MB was reached", c.MaxBytes>>20)
	}
	if c.stopped != "" {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrBudget, c.stopped)
	}
	c.requests++
	c.mu.Unlock()

	resp, err := c.Inner.Do(ctx, req)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hosts == nil {
		c.hosts = map[string]Host{}
	}
	h := c.hosts[host]
	h.Requests++
	if err != nil {
		h.Failures++
	} else if resp != nil {
		h.Bytes += int64(len(resp.Body))
		c.bytes += int64(len(resp.Body))
	}
	c.hosts[host] = h
	return resp, err
}

// Hosts returns a copy of the traffic per host.
func (c *Counter) Hosts() map[string]Host {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]Host, len(c.hosts))
	for k, v := range c.hosts {
		out[k] = v
	}
	return out
}

// Stopped says why the counter refused requests, or "" when it has not.
func (c *Counter) Stopped() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}
