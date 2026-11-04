package laya

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	batchPath  = "v1/systemone/batch"
	healthPath = "health"

	// maxResponseBytes caps what is read from the server.
	maxResponseBytes = 8 << 20
	// maxAttempts is how often a request is sent while the server says it is busy.
	maxAttempts = 3
	// maxTotalWait caps the time spent waiting for a busy server.
	maxTotalWait = 10 * time.Second
	// defaultRetryWait is the wait when a 503 carries no usable Retry-After.
	defaultRetryWait = time.Second
	// serverErrorWait is the wait before the one retry of another 5xx.
	serverErrorWait = 250 * time.Millisecond
)

// statusError is a reply with a status the client does not accept. It never
// carries the URL or the credentials.
type statusError struct {
	code       int
	retryAfter time.Duration
}

func (e *statusError) Error() string {
	return fmt.Sprintf("laya: request failed: %d %s", e.code, http.StatusText(e.code))
}

// batchRequest is the body of POST /v1/systemone/batch.
type batchRequest struct {
	States    []string            `json:"states"`
	Questions map[string]question `json:"questions"`
	Model     string              `json:"model,omitempty"`
}

// batchResponse is the reply, with the results in the order of the states.
type batchResponse struct {
	Results []result `json:"results"`
}

// result is what the model said about one state. The answers stay raw so that
// an answer of an unexpected shape can be ignored on its own.
type result struct {
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   struct {
		Truncated bool `json:"truncated"`
	} `json:"usage"`
}

// batch sends one request and returns one result for each state.
func (s *Scorer) batch(ctx context.Context, states []string, qs map[string]question) ([]result, error) {
	body, err := json.Marshal(batchRequest{States: states, Questions: qs, Model: s.model})
	if err != nil {
		return nil, errors.New("laya: encoding the request failed")
	}
	reply, err := s.send(ctx, http.MethodPost, s.batchURL, body)
	if err != nil {
		return nil, err
	}
	var resp batchResponse
	if err := json.Unmarshal(reply, &resp); err != nil {
		return nil, errors.New("laya: the reply is not valid JSON of the expected shape")
	}
	if len(resp.Results) != len(states) {
		return nil, fmt.Errorf("laya: the reply has %d results for %d states", len(resp.Results), len(states))
	}
	return resp.Results, nil
}

// Ping asks the server for its health and returns nil when it says it is ok.
// The error never contains the address.
func (s *Scorer) Ping(ctx context.Context) error {
	reply, err := s.send(ctx, http.MethodGet, s.healthURL, nil)
	if err != nil {
		return err
	}
	var h struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(reply, &h); err != nil {
		return errors.New("laya: the health reply is not valid JSON")
	}
	if h.Status != "ok" {
		return fmt.Errorf("laya: the server reports status %q", truncateRunes(h.Status, 40))
	}
	return nil
}

// send sends a request, retrying a busy server (503, honouring Retry-After) up
// to maxAttempts times and any other 5xx once, and returns the body of a 2xx.
func (s *Scorer) send(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var waited time.Duration
	retried5xx := false
	for attempt := 1; ; attempt++ {
		reply, err := s.sendOnce(ctx, method, url, body)
		var se *statusError
		if err == nil || !errors.As(err, &se) {
			return reply, err
		}
		var wait time.Duration
		switch {
		case se.code == http.StatusServiceUnavailable && attempt < maxAttempts:
			wait = se.retryAfter
		case se.code >= 500 && se.code != http.StatusServiceUnavailable && !retried5xx:
			retried5xx = true
			wait = serverErrorWait
		default:
			return nil, err
		}
		if wait > maxTotalWait-waited {
			wait = maxTotalWait - waited
		}
		waited += wait
		if err := s.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// sendOnce makes one HTTP request. Transport errors are reduced to a short
// description because net/http puts the URL into them.
func (s *Scorer) sendOnce(parent context.Context, method, url string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()

	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, errors.New("laya: request failed: could not build the request")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, transportError(parent, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &statusError{code: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, transportError(parent, err)
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("laya: request failed: the reply is larger than 8 MiB")
	}
	return data, nil
}

// transportError describes a failure below HTTP. A cancelled caller context
// is returned as its own error.
func transportError(parent context.Context, err error) error {
	if perr := parent.Err(); perr != nil {
		return perr
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return errors.New("laya: request failed: timeout")
	}
	return errors.New("laya: request failed: connection error")
}

// parseRetryAfter reads a Retry-After given in seconds. Anything else, and a
// missing header, gives defaultRetryWait.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs < 0 {
		return defaultRetryWait
	}
	if secs > int(maxTotalWait/time.Second) {
		return maxTotalWait
	}
	return time.Duration(secs) * time.Second
}

// sleepContext waits for d or until ctx ends.
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

// truncateRunes shortens s to at most n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
