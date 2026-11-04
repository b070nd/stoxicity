package channel

// The email channel sends each notification through the Gmail API with an
// OAuth 2.0 refresh token, for one Google account. It uses plain net/http: no
// Google library, so the only code that touches a credential is in this file
// and gmailauth.go.
//
// Facts it relies on, from Google's documentation (read 2026-11-04, see
// docs/research/gmail-api.md):
//
//   - users.messages.send takes {"raw": base64url(RFC 2822 message)} at
//     POST https://gmail.googleapis.com/gmail/v1/users/me/messages/send, and
//     the scope https://www.googleapis.com/auth/gmail.send is enough for it.
//   - An access token comes from POST https://oauth2.googleapis.com/token with
//     grant_type=refresh_token. client_secret is documented as optional for
//     desktop clients.
//   - A refresh token expires after 7 days while the project's consent screen
//     is in the "Testing" status, and also when it is revoked or unused for six
//     months. The error is then "invalid_grant", reported here as
//     ErrReauthorize.
//
// Configuration comes from the environment, never from config.yaml:
//
//	STOXICITY_GMAIL_CLIENT_ID, STOXICITY_GMAIL_CLIENT_SECRET   .env.local
//	STOXICITY_MAIL_FROM, STOXICITY_MAIL_TO                     .env.local
//	STOXICITY_GMAIL_REFRESH_TOKEN                              optional; the
//	    token that "stoxicity gmail auth" stores in a file is used otherwise
//
// A missing value stops the channel with a message that names the variable.
// No error or log line holds a token, a secret or a response body.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Google's endpoints. GmailConfig can replace them, which tests do.
const (
	GoogleTokenURL = "https://oauth2.googleapis.com/token"
	GmailSendURL   = "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"
	// GmailSendScope is the one scope the channel asks for.
	GmailSendScope = "https://www.googleapis.com/auth/gmail.send"

	gmailTimeout  = 20 * time.Second
	gmailMaxReply = 16 << 10
	// gmailMaxMessage keeps one message far below any limit; articles are
	// dropped from the end of the list until it fits.
	gmailMaxMessage = 1 << 20
	// tokenSlack renews an access token this long before it runs out.
	tokenSlack = time.Minute
)

// ErrReauthorize means Google no longer accepts the refresh token: it expired,
// was revoked or was replaced. Nothing is sent until the user runs
// "stoxicity gmail auth" again. Test for it with errors.Is.
var ErrReauthorize = errors.New(`gmail: Google no longer accepts the stored authorisation; run "stoxicity gmail auth" again`)

// GmailConfig holds what the channel needs.
type GmailConfig struct {
	ClientID     string
	ClientSecret string // may be empty
	RefreshToken string
	From, To     string
	// HTTPClient makes the requests; a client with a 20 second timeout if nil.
	// Redirects are never followed.
	HTTPClient *http.Client
	// TokenURL and SendURL replace Google's addresses; tests set them.
	TokenURL, SendURL string
	// Now is the clock; time.Now if nil.
	Now func() time.Time
}

// GmailChannel sends notifications as email through the Gmail API.
type GmailChannel struct {
	cfg    GmailConfig
	from   mail.Address
	to     mail.Address
	client *http.Client

	mu      sync.Mutex
	access  string
	expires time.Time
}

var _ Channel = (*GmailChannel)(nil)

// NewGmail returns the channel. It checks the addresses and that the values it
// needs are present, and says which variable is empty without printing a
// value; it makes no request.
func NewGmail(cfg GmailConfig) (*GmailChannel, error) {
	var errs []error
	for _, s := range []setting{
		{"STOXICITY_GMAIL_CLIENT_ID", cfg.ClientID},
		{"STOXICITY_MAIL_FROM", cfg.From},
		{"STOXICITY_MAIL_TO", cfg.To},
	} {
		errs = append(errs, unset(Email, s)...)
	}
	if cfg.RefreshToken == "" {
		errs = append(errs, errors.New(`channel: email: no authorisation yet: run "stoxicity gmail auth" (or set STOXICITY_GMAIL_REFRESH_TOKEN)`))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	from, err := parseAddress("STOXICITY_MAIL_FROM", cfg.From)
	if err != nil {
		return nil, err
	}
	to, err := parseAddress("STOXICITY_MAIL_TO", cfg.To)
	if err != nil {
		return nil, err
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = GoogleTokenURL
	}
	if cfg.SendURL == "" {
		cfg.SendURL = GmailSendURL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: gmailTimeout}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &GmailChannel{cfg: cfg, from: from, to: to, client: &c}, nil
}

// parseAddress reads one mailbox. The error names the variable, not the value.
func parseAddress(variable, value string) (mail.Address, error) {
	if strings.ContainsAny(value, "\r\n") {
		return mail.Address{}, fmt.Errorf("channel: email: %s must be one address on one line", variable)
	}
	a, err := mail.ParseAddress(value)
	if err != nil {
		return mail.Address{}, fmt.Errorf("channel: email: %s is not an email address", variable)
	}
	return *a, nil
}

// Kind returns Email.
func (c *GmailChannel) Kind() Kind { return Email }

// Send emails n. A 2xx answer is success. A 429 or 5xx answer, and a failure
// to reach Google, wrap ErrRetry; an authorisation Google refuses returns
// ErrReauthorize.
func (c *GmailChannel) Send(ctx context.Context, n Notification) error {
	raw, err := c.message(n)
	if err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		Raw string `json:"raw"`
	}{base64.URLEncoding.EncodeToString(raw)})
	if err != nil {
		return errors.New("gmail: cannot encode the message")
	}
	// A cached access token can have been revoked early: ask once more with a
	// fresh one before giving up.
	for attempt := 0; ; attempt++ {
		token, err := c.accessToken(ctx, attempt > 0)
		if err != nil {
			return err
		}
		status, err := c.post(ctx, token, body)
		if err != nil {
			return err
		}
		switch {
		case status >= 200 && status < 300:
			return nil
		case status == http.StatusUnauthorized && attempt == 0:
			continue
		case status == http.StatusUnauthorized:
			return ErrReauthorize
		case status == http.StatusTooManyRequests || status >= 500:
			return fmt.Errorf("gmail: send failed with status %d: %w", status, ErrRetry)
		default:
			return fmt.Errorf("gmail: send failed with status %d", status)
		}
	}
}

func (c *GmailChannel) post(ctx context.Context, token string, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.SendURL, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("gmail: cannot build the request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		return 0, fmt.Errorf("gmail: send failed: no response: %w", ErrRetry)
	}
	defer resp.Body.Close()
	io.CopyN(io.Discard, resp.Body, gmailMaxReply)
	return resp.StatusCode, nil
}

// accessToken returns a valid access token, asking Google for a new one when
// the cached one is missing, near its end or refresh is set.
func (c *GmailChannel) accessToken(ctx context.Context, refresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !refresh && c.access != "" && c.cfg.Now().Add(tokenSlack).Before(c.expires) {
		return c.access, nil
	}
	form := url.Values{
		"client_id":     {c.cfg.ClientID},
		"refresh_token": {c.cfg.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	if c.cfg.ClientSecret != "" {
		form.Set("client_secret", c.cfg.ClientSecret)
	}
	tok, err := postToken(ctx, c.client, c.cfg.TokenURL, form)
	if err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", errors.New("gmail: Google's answer holds no access token")
	}
	c.access = tok.AccessToken
	c.expires = c.cfg.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return c.access, nil
}

// tokenReply is the part of the token endpoint's answer that is used.
type tokenReply struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// postToken sends form to the token endpoint and decodes the answer. Its errors
// never hold the request or the answer.
func postToken(ctx context.Context, client *http.Client, endpoint string, form url.Values) (tokenReply, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenReply{}, errors.New("gmail: cannot build the token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return tokenReply{}, ctxErr
		}
		return tokenReply{}, fmt.Errorf("gmail: token request failed: no response: %w", ErrRetry)
	}
	defer resp.Body.Close()
	var tok tokenReply
	data, _ := io.ReadAll(io.LimitReader(resp.Body, gmailMaxReply))
	// A body that is not JSON leaves tok empty; the status decides.
	json.Unmarshal(data, &tok)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return tok, nil
	case tok.Error == "invalid_grant":
		return tokenReply{}, ErrReauthorize
	case tok.Error == "invalid_client":
		return tokenReply{}, errors.New("gmail: Google does not know this client; check STOXICITY_GMAIL_CLIENT_ID and STOXICITY_GMAIL_CLIENT_SECRET")
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return tokenReply{}, fmt.Errorf("gmail: token request failed with status %d: %w", resp.StatusCode, ErrRetry)
	}
	return tokenReply{}, fmt.Errorf("gmail: token request failed with status %d", resp.StatusCode)
}

// message builds the RFC 2822 message for n. When it would exceed the size
// limit, articles are dropped from the end until it fits.
func (c *GmailChannel) message(n Notification) ([]byte, error) {
	for {
		raw, err := c.render(n)
		if err != nil {
			return nil, err
		}
		if len(raw) <= gmailMaxMessage {
			return raw, nil
		}
		if len(n.Articles) == 0 {
			return nil, errors.New("gmail: the notification is too large to send even without articles")
		}
		n.Articles = n.Articles[:len(n.Articles)/2]
	}
}

func (c *GmailChannel) render(n Notification) ([]byte, error) {
	subject := oneLine(n.Title)
	if subject == "" {
		subject = "stoxicity"
	}
	if r := []rune(subject); len(r) > 200 {
		subject = string(r[:200])
	}
	sum := sha256.Sum256([]byte(n.Key()))
	var b bytes.Buffer
	hdr := func(name, value string) { fmt.Fprintf(&b, "%s: %s\r\n", name, value) }
	hdr("From", c.from.String())
	hdr("To", c.to.String())
	hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
	hdr("Date", n.At.UTC().Format(time.RFC1123Z))
	hdr("Message-ID", "<"+hex.EncodeToString(sum[:12])+"@stoxicity.invalid>")
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", `text/plain; charset="UTF-8"`)
	hdr("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	w := quotedprintable.NewWriter(&b)
	text := strings.ReplaceAll(string(renderMarkdown(n)), "\r\n", "\n")
	if _, err := w.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n"))); err != nil {
		return nil, fmt.Errorf("gmail: encode the body: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("gmail: encode the body: %w", err)
	}
	return b.Bytes(), nil
}
