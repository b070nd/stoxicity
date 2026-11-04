package channel

// The one-time authorisation of the Gmail channel: Google's OAuth 2.0 flow for
// desktop apps with a loopback redirect and PKCE. The user opens an address in
// a browser, approves "send email on your behalf", and Google redirects to a
// listener on 127.0.0.1 that this process runs for the occasion. The refresh
// token that results is returned to the caller to store; this file never
// prints or logs it.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GoogleAuthURL is the authorization endpoint.
const GoogleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"

// authWait is how long the browser has to come back.
const authWait = 5 * time.Minute

// AuthConfig holds what the flow needs.
type AuthConfig struct {
	ClientID     string
	ClientSecret string // may be empty
	// Show is called once with the address the user must open. It is the only
	// place the address goes; it holds the client ID and no secret.
	Show func(authURL string)
	// HTTPClient makes the token request; a client with a 20 second timeout if
	// nil.
	HTTPClient *http.Client
	// AuthURL and TokenURL replace Google's addresses; tests set them.
	AuthURL, TokenURL string
	// Wait replaces the five minutes the browser has to come back.
	Wait time.Duration
}

// Authorize runs the flow and returns the refresh token. It asks for
// access_type=offline and prompt=consent so that Google issues a refresh token
// even when the account has approved the client before.
func Authorize(ctx context.Context, cfg AuthConfig) (string, error) {
	var errs []error
	errs = append(errs, unset(Email, setting{"STOXICITY_GMAIL_CLIENT_ID", cfg.ClientID})...)
	if len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	if cfg.Show == nil {
		return "", errors.New("gmail: no way to show the address to open")
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = GoogleAuthURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = GoogleTokenURL
	}
	if cfg.Wait <= 0 {
		cfg.Wait = authWait
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: gmailTimeout}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	verifier, err := randomString(48) // 64 characters, within the 43 to 128 allowed
	if err != nil {
		return "", err
	}
	state, err := randomString(24)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("gmail: cannot listen on the loopback address: %w", err)
	}
	redirect := "http://" + ln.Addr().String()

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			var res result
			switch {
			case q.Get("state") != state:
				res.err = errors.New("gmail: the answer does not belong to this request (state differs)")
			case q.Get("error") != "":
				res.err = fmt.Errorf("gmail: Google reported %q", safeToken(q.Get("error")))
			case q.Get("code") == "":
				res.err = errors.New("gmail: the answer holds no code")
			default:
				res.code = q.Get("code")
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			msg := "You can close this tab and go back to the terminal."
			if res.err != nil {
				msg = "Authorisation failed: " + res.err.Error()
			}
			fmt.Fprintf(w, "<!doctype html><title>stoxicity</title><p>%s</p>", html.EscapeString(msg))
			select {
			case done <- res:
			default:
			}
		}),
	}
	go srv.Serve(ln)
	defer srv.Close()

	q := url.Values{
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {redirect},
		"response_type":         {"code"},
		"scope":                 {GmailSendScope},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	cfg.Show(cfg.AuthURL + "?" + q.Encode())

	wait := time.NewTimer(cfg.Wait)
	defer wait.Stop()
	var res result
	select {
	case res = <-done:
	case <-wait.C:
		return "", errors.New("gmail: no answer from the browser in time; run the command again")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if res.err != nil {
		return "", res.err
	}

	form := url.Values{
		"client_id":     {cfg.ClientID},
		"code":          {res.code},
		"code_verifier": {verifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirect},
	}
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	tok, err := postToken(ctx, &c, cfg.TokenURL, form)
	if err != nil {
		if errors.Is(err, ErrReauthorize) {
			return "", errors.New("gmail: Google refused the code; run the command again")
		}
		return "", err
	}
	if tok.RefreshToken == "" {
		return "", errors.New("gmail: Google issued no refresh token; remove this app's access in your Google account and run the command again")
	}
	if !strings.Contains(" "+tok.Scope+" ", " "+GmailSendScope+" ") {
		return "", errors.New("gmail: the permission to send email was not granted; run the command again and keep it ticked")
	}
	return tok.RefreshToken, nil
}

// randomString returns n random bytes as unpadded base64url.
func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gmail: no randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// safeToken keeps the characters of an OAuth error code, which is a short
// lower-case word, and drops anything else so a hostile redirect cannot put
// text in our messages.
func safeToken(s string) string {
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || r == '_' {
			return r
		}
		return -1
	}, s)
}
