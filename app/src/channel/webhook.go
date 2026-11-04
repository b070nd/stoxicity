package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	webhookMaxBody  = 256 << 10 // request body limit, in bytes
	webhookMaxReply = 4 << 10   // most of the response that is read
	webhookTimeout  = 10 * time.Second
)

// ErrRetry marks a webhook failure that may succeed if sent again later: the
// receiver answered 429 Too Many Requests or a 5xx status. Test for it with
// errors.Is.
var ErrRetry = errors.New("retry later")

// WebhookChannel posts each notification as JSON to one address, such as a
// chat incoming webhook. The address is a secret: no error or log names it.
type WebhookChannel struct {
	url    string
	client *http.Client
}

var _ Channel = (*WebhookChannel)(nil)

// NewWebhook returns a channel that posts to rawURL. The URL must be https, or
// http for a loopback host (localhost, 127.0.0.1 or ::1), and must not carry
// user information. A nil client gets a 10 second timeout. Redirects are never
// followed, so the payload cannot reach another address.
func NewWebhook(rawURL string, client *http.Client) (*WebhookChannel, error) {
	// url.Parse errors quote the input, which is the secret; do not wrap them.
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("webhook: URL is not valid")
	}
	if u.User != nil {
		return nil, errors.New("webhook: URL must not contain user information")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, errors.New("webhook: URL has no host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(host) {
			return nil, errors.New("webhook: http is allowed only for localhost, 127.0.0.1 or ::1; use https")
		}
	default:
		return nil, errors.New("webhook: URL must use https")
	}
	if client == nil {
		client = &http.Client{Timeout: webhookTimeout}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &WebhookChannel{url: rawURL, client: &c}, nil
}

// isLoopbackHost reports whether a lower-case host name is one of the loopback
// addresses that may be reached over plain http.
func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// Kind returns Webhook.
func (c *WebhookChannel) Kind() Kind { return Webhook }

// Send posts n as JSON. A 2xx answer is success. A 429 or 5xx answer returns an
// error that wraps ErrRetry; any other status is an error without it.
func (c *WebhookChannel) Send(ctx context.Context, n Notification) error {
	body, err := encodeWebhook(n)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return errors.New("webhook: cannot build request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("webhook: POST failed: no response")
	}
	defer resp.Body.Close()
	io.CopyN(io.Discard, resp.Body, webhookMaxReply)

	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusTooManyRequests || code >= 500:
		return fmt.Errorf("webhook: POST failed with status %d: %w", code, ErrRetry)
	default:
		return fmt.Errorf("webhook: POST failed with status %d", code)
	}
}

// webhookPayload is the JSON document posted for one notification.
type webhookPayload struct {
	Tenant   string           `json:"tenant"`
	Watch    string           `json:"watch"`
	Alert    string           `json:"alert"`
	Title    string           `json:"title"`
	Body     string           `json:"body"`
	Link     string           `json:"link"`
	At       time.Time        `json:"at"`
	Articles []webhookArticle `json:"articles"`
}

type webhookArticle struct {
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Domain    string    `json:"domain"`
	Published time.Time `json:"published"`
}

// encodeWebhook returns the JSON body for n. When it is over the size limit,
// articles are dropped from the end until it fits; the structure stays whole.
func encodeWebhook(n Notification) ([]byte, error) {
	arts := make([]webhookArticle, len(n.Articles))
	for i, a := range n.Articles {
		arts[i] = webhookArticle{Title: a.Title, URL: a.URL, Domain: a.Domain, Published: a.Published.UTC()}
	}
	p := webhookPayload{
		Tenant: n.TenantID, Watch: n.WatchID, Alert: n.Alert, Title: n.Title,
		Body: n.Body, Link: n.Link, At: n.At.UTC(),
	}
	marshal := func(k int) ([]byte, error) {
		p.Articles = arts[:k]
		return json.Marshal(p)
	}
	data, err := marshal(len(arts))
	if err != nil {
		return nil, fmt.Errorf("webhook: encode: %w", err)
	}
	if len(data) <= webhookMaxBody {
		return data, nil
	}
	// Size grows with each article, so binary search for the largest count
	// that fits.
	var fitted []byte
	lo, hi := 0, len(arts)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		d, err := marshal(mid)
		if err != nil {
			return nil, fmt.Errorf("webhook: encode: %w", err)
		}
		if len(d) <= webhookMaxBody {
			fitted = d
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if fitted == nil {
		return nil, errors.New("webhook: notification too large to send even without articles")
	}
	return fitted, nil
}
