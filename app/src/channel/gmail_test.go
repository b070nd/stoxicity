package channel

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// The values below are made up for these tests; none is a real credential.
const (
	testClientID     = "test-client-id.apps.example"
	testClientSecret = "test-client-secret-zzz"
	testRefreshToken = "test-refresh-token-zzz"
	testAccessToken  = "test-access-token-zzz"
)

// fakeGoogle serves the token endpoint and the send endpoint.
type fakeGoogle struct {
	srv *httptest.Server

	mu        sync.Mutex
	tokenHits int
	sendHits  int
	// tokenStatus and tokenBody script the token endpoint; defaults give a token.
	tokenStatus int
	tokenBody   string
	// sendStatuses are answered in turn; the last repeats. Empty means 200.
	sendStatuses []int
	lastAuth     string
	lastRaw      string
	lastForm     url.Values
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		g.tokenHits++
		g.lastForm = r.PostForm
		status, body := g.tokenStatus, g.tokenBody
		if status == 0 {
			status = 200
			body = `{"access_token":"` + testAccessToken + `","expires_in":3600,"token_type":"Bearer"}`
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Raw string `json:"raw"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		g.mu.Lock()
		defer g.mu.Unlock()
		g.sendHits++
		g.lastAuth, g.lastRaw = r.Header.Get("Authorization"), in.Raw
		status := 200
		if len(g.sendStatuses) > 0 {
			status = g.sendStatuses[0]
			if len(g.sendStatuses) > 1 {
				g.sendStatuses = g.sendStatuses[1:]
			}
		}
		w.WriteHeader(status)
		io.WriteString(w, `{"id":"abc","error":{"message":"`+testRefreshToken+`"}}`)
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGoogle) config() GmailConfig {
	return GmailConfig{
		ClientID: testClientID, ClientSecret: testClientSecret, RefreshToken: testRefreshToken,
		From: "me@example.com", To: "Me <me@example.com>",
		TokenURL: g.srv.URL + "/token", SendURL: g.srv.URL + "/send",
	}
}

func notification() Notification {
	return Notification{
		TenantID: "default", WatchID: "core", Alert: "any",
		Title: "3 new: Acme Corp reports record earnings",
		Body:  "Three articles matched.",
		At:    time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC),
		Articles: []model.Article{
			{ID: "a1", Title: "Acme beats — “record” year", URL: "https://news.example.com/acme", Domain: "news.example.com"},
		},
	}
}

func decodeRaw(t *testing.T, raw string) (*mail.Message, string) {
	t.Helper()
	b, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("raw is not base64url: %v", err)
	}
	m, err := mail.ReadMessage(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("raw is not an RFC 2822 message: %v", err)
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(m.Body))
	return m, string(body)
}

func TestGmailSendsAnEncodedMessageWithABearerToken(t *testing.T) {
	g := newFakeGoogle(t)
	ch, err := NewGmail(g.config())
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(context.Background(), notification()); err != nil {
		t.Fatal(err)
	}
	if g.lastAuth != "Bearer "+testAccessToken {
		t.Errorf("Authorization = %q, want a bearer token", g.lastAuth)
	}
	for k, want := range map[string]string{"grant_type": "refresh_token", "client_id": testClientID, "refresh_token": testRefreshToken, "client_secret": testClientSecret} {
		if g.lastForm.Get(k) != want {
			t.Errorf("token request %s = %q, want %q", k, g.lastForm.Get(k), want)
		}
	}
	m, body := decodeRaw(t, g.lastRaw)
	dec := new(mime.WordDecoder)
	subject, _ := dec.DecodeHeader(m.Header.Get("Subject"))
	if subject != "3 new: Acme Corp reports record earnings" {
		t.Errorf("Subject = %q", subject)
	}
	if to, _ := m.Header.AddressList("To"); len(to) != 1 || to[0].Address != "me@example.com" {
		t.Errorf("To = %v", to)
	}
	if !strings.HasPrefix(m.Header.Get("Content-Type"), "text/plain") || m.Header.Get("MIME-Version") != "1.0" {
		t.Errorf("headers: %v", m.Header)
	}
	for _, want := range []string{"Three articles matched.", "https://news.example.com/acme", "Acme beats — “record” year"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
}

func TestGmailReusesTheAccessTokenAndRenewsItOnceExpired(t *testing.T) {
	g := newFakeGoogle(t)
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	cfg := g.config()
	cfg.Now = func() time.Time { return now }
	ch, _ := NewGmail(cfg)
	for i := 0; i < 3; i++ {
		if err := ch.Send(context.Background(), notification()); err != nil {
			t.Fatal(err)
		}
	}
	if g.tokenHits != 1 {
		t.Errorf("%d token requests for 3 sends within the lifetime, want 1", g.tokenHits)
	}
	now = now.Add(2 * time.Hour)
	if err := ch.Send(context.Background(), notification()); err != nil {
		t.Fatal(err)
	}
	if g.tokenHits != 2 {
		t.Errorf("%d token requests after expiry, want 2", g.tokenHits)
	}
}

func TestGmailAsksOnceMoreAfter401(t *testing.T) {
	g := newFakeGoogle(t)
	g.sendStatuses = []int{401, 200}
	ch, _ := NewGmail(g.config())
	if err := ch.Send(context.Background(), notification()); err != nil {
		t.Fatalf("Send = %v, want success after the retry", err)
	}
	if g.tokenHits != 2 || g.sendHits != 2 {
		t.Errorf("token %d send %d, want 2 and 2", g.tokenHits, g.sendHits)
	}
	g.sendStatuses = []int{401}
	if err := ch.Send(context.Background(), notification()); !errors.Is(err, ErrReauthorize) {
		t.Errorf("Send = %v, want ErrReauthorize after two 401", err)
	}
}

func TestGmailErrorClasses(t *testing.T) {
	tests := []struct {
		name        string
		tokenStatus int
		tokenBody   string
		sendStatus  []int
		want        error
		wantText    string
	}{
		{name: "invalid_grant", tokenStatus: 400, tokenBody: `{"error":"invalid_grant"}`, want: ErrReauthorize},
		{name: "invalid_client", tokenStatus: 401, tokenBody: `{"error":"invalid_client"}`, wantText: "STOXICITY_GMAIL_CLIENT_ID"},
		{name: "token 503", tokenStatus: 503, tokenBody: `oops`, want: ErrRetry},
		{name: "send 429", sendStatus: []int{429}, want: ErrRetry},
		{name: "send 500", sendStatus: []int{500}, want: ErrRetry},
		{name: "send 403", sendStatus: []int{403}, wantText: "status 403"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGoogle(t)
			g.tokenStatus, g.tokenBody, g.sendStatuses = tc.tokenStatus, tc.tokenBody, tc.sendStatus
			ch, _ := NewGmail(g.config())
			err := ch.Send(context.Background(), notification())
			if err == nil {
				t.Fatal("no error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
			if tc.want == nil && errors.Is(err, ErrRetry) {
				t.Errorf("error %v must not be retryable", err)
			}
			if tc.wantText != "" && !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %v, want it to say %q", err, tc.wantText)
			}
			for _, secret := range []string{testClientSecret, testRefreshToken, testAccessToken} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error holds a secret: %v", err)
				}
			}
		})
	}
}

func TestGmailNeverLeaksASecretWhenUnreachable(t *testing.T) {
	cfg := GmailConfig{ClientID: testClientID, ClientSecret: testClientSecret, RefreshToken: testRefreshToken,
		From: "me@example.com", To: "me@example.com", TokenURL: "http://127.0.0.1:1/token", SendURL: "http://127.0.0.1:1/send"}
	ch, _ := NewGmail(cfg)
	err := ch.Send(context.Background(), notification())
	if !errors.Is(err, ErrRetry) {
		t.Fatalf("error = %v, want a retryable failure", err)
	}
	for _, secret := range []string{testClientSecret, testRefreshToken} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error holds a secret: %v", err)
		}
	}
}

func TestGmailHeadersCannotBeInjectedFromArticleText(t *testing.T) {
	g := newFakeGoogle(t)
	ch, _ := NewGmail(g.config())
	n := notification()
	n.Title = "Hello\r\nBcc: attacker@example.net\r\n\r\nbody"
	if err := ch.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	m, _ := decodeRaw(t, g.lastRaw)
	if m.Header.Get("Bcc") != "" {
		t.Errorf("a Bcc header was injected through the title")
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if strings.ContainsAny(subject, "\r\n") {
		t.Errorf("Subject has a line break: %q", subject)
	}
}

func TestGmailDropsArticlesFromAnOversizedMessage(t *testing.T) {
	g := newFakeGoogle(t)
	ch, _ := NewGmail(g.config())
	n := notification()
	n.Articles = nil
	for i := 0; i < 20; i++ {
		n.Articles = append(n.Articles, model.Article{Title: strings.Repeat("x", 200000), URL: "https://e.example/" + string(rune('a'+i)), Domain: "e.example"})
	}
	if err := ch.Send(context.Background(), n); err != nil {
		t.Fatalf("Send = %v, want articles dropped until it fits", err)
	}
	if got := len(g.lastRaw); got > gmailMaxMessage*2 {
		t.Errorf("sent %d bytes, want the message trimmed", got)
	}
}

func TestNewGmailNamesTheEmptyVariablesAndNeverTheValues(t *testing.T) {
	_, err := NewGmail(GmailConfig{ClientSecret: testClientSecret})
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"STOXICITY_GMAIL_CLIENT_ID", "STOXICITY_MAIL_FROM", "STOXICITY_MAIL_TO", "stoxicity gmail auth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), testClientSecret) {
		t.Error("error holds the secret")
	}
	_, err = NewGmail(GmailConfig{ClientID: "id", RefreshToken: "tok", From: "not an address", To: "me@example.com"})
	if err == nil || !strings.Contains(err.Error(), "STOXICITY_MAIL_FROM") || strings.Contains(err.Error(), "not an address") {
		t.Errorf("error = %v, want the variable named and the value left out", err)
	}
}

func TestBuildMakesTheEmailChannelFromSettings(t *testing.T) {
	g := newFakeGoogle(t)
	cfg := g.config()
	chans, errs := Build([]Kind{Email}, Settings{
		GmailClientID: cfg.ClientID, GmailClientSecret: cfg.ClientSecret, GmailRefreshToken: cfg.RefreshToken,
		MailFrom: cfg.From, MailTo: cfg.To,
	}, nil)
	if len(errs) != 0 || chans[Email] == nil {
		t.Fatalf("Build = %v, %v", chans, errs)
	}
}

// --- authorisation ---

func TestAuthorizeRunsTheLoopbackFlowWithPKCE(t *testing.T) {
	var challenge, method string
	var tokenForm url.Values
	var mu sync.Mutex
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		tokenForm = r.PostForm
		mu.Unlock()
		io.WriteString(w, `{"access_token":"`+testAccessToken+`","refresh_token":"`+testRefreshToken+`","expires_in":3600,"scope":"`+GmailSendScope+`"}`)
	}))
	defer tokenSrv.Close()

	var shown atomic.Value
	cfg := AuthConfig{
		ClientID: testClientID, ClientSecret: testClientSecret,
		AuthURL: "https://accounts.example/auth", TokenURL: tokenSrv.URL, Wait: 5 * time.Second,
		Show: func(u string) {
			shown.Store(u)
			parsed, _ := url.Parse(u)
			q := parsed.Query()
			challenge, method = q.Get("code_challenge"), q.Get("code_challenge_method")
			for k, want := range map[string]string{
				"client_id": testClientID, "response_type": "code", "scope": GmailSendScope,
				"access_type": "offline", "prompt": "consent",
			} {
				if q.Get(k) != want {
					t.Errorf("authorisation address %s = %q, want %q", k, q.Get(k), want)
				}
			}
			// The browser comes back to the loopback listener.
			go func() {
				back := q.Get("redirect_uri") + "/?code=the-code&state=" + url.QueryEscape(q.Get("state"))
				resp, err := http.Get(back)
				if err == nil {
					resp.Body.Close()
				}
			}()
		},
	}
	got, err := Authorize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got != testRefreshToken {
		t.Errorf("refresh token = %q", got)
	}
	if method != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", method)
	}
	if !strings.HasPrefix(shown.Load().(string), "https://accounts.example/auth?") {
		t.Errorf("shown address = %v", shown.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	sum := sha256.Sum256([]byte(tokenForm.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
		t.Error("code_verifier does not match the code_challenge that was sent")
	}
	if l := len(tokenForm.Get("code_verifier")); l < 43 || l > 128 {
		t.Errorf("code_verifier has %d characters, want 43 to 128", l)
	}
	if tokenForm.Get("grant_type") != "authorization_code" || tokenForm.Get("code") != "the-code" ||
		!strings.HasPrefix(tokenForm.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Errorf("token request = %v", tokenForm)
	}
}

func TestAuthorizeRefusesAWrongStateAndAMissingScope(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"a","refresh_token":"`+testRefreshToken+`","scope":"openid"}`)
	}))
	defer tokenSrv.Close()
	run := func(state func(string) string) error {
		_, err := Authorize(context.Background(), AuthConfig{
			ClientID: testClientID, AuthURL: "https://accounts.example/auth", TokenURL: tokenSrv.URL, Wait: 5 * time.Second,
			Show: func(u string) {
				parsed, _ := url.Parse(u)
				q := parsed.Query()
				go func() {
					resp, err := http.Get(q.Get("redirect_uri") + "/?code=c&state=" + url.QueryEscape(state(q.Get("state"))))
					if err == nil {
						resp.Body.Close()
					}
				}()
			},
		})
		return err
	}
	if err := run(func(string) string { return "forged" }); err == nil || !strings.Contains(err.Error(), "state") {
		t.Errorf("wrong state: error = %v", err)
	}
	err := run(func(s string) string { return s })
	if err == nil || !strings.Contains(err.Error(), "permission to send") {
		t.Errorf("missing scope: error = %v", err)
	}
	if err != nil && strings.Contains(err.Error(), testRefreshToken) {
		t.Error("error holds the refresh token")
	}
}

func TestAuthorizeTimesOutWhenNobodyComesBack(t *testing.T) {
	_, err := Authorize(context.Background(), AuthConfig{
		ClientID: testClientID, Wait: 50 * time.Millisecond, Show: func(string) {},
	})
	if err == nil || !strings.Contains(err.Error(), "in time") {
		t.Errorf("error = %v, want a timeout", err)
	}
}

func TestAuthorizeNeedsAClientID(t *testing.T) {
	_, err := Authorize(context.Background(), AuthConfig{Show: func(string) {}})
	if err == nil || !strings.Contains(err.Error(), "STOXICITY_GMAIL_CLIENT_ID") {
		t.Errorf("error = %v", err)
	}
}
