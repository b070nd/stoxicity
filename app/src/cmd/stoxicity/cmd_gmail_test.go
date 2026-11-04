package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Made-up values for these tests; none is a real credential.
const (
	gmClientID = "cmd-test-client.apps.example"
	gmRefresh  = "cmd-test-refresh-token-zzz"
	gmAccess   = "cmd-test-access-token-zzz"
)

type gmailFake struct {
	srv  *httptest.Server
	mu   sync.Mutex
	sent []string
}

func newGmailFake(t *testing.T) *gmailFake {
	t.Helper()
	g := &gmailFake{}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("grant_type") == "authorization_code" {
			io.WriteString(w, `{"access_token":"`+gmAccess+`","refresh_token":"`+gmRefresh+`","expires_in":3600,"scope":"https://www.googleapis.com/auth/gmail.send"}`)
			return
		}
		if r.PostForm.Get("refresh_token") != gmRefresh {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		io.WriteString(w, `{"access_token":"`+gmAccess+`","expires_in":3600}`)
	})
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Raw string }
		json.NewDecoder(r.Body).Decode(&in)
		g.mu.Lock()
		g.sent = append(g.sent, in.Raw)
		g.mu.Unlock()
		io.WriteString(w, `{"id":"x"}`)
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *gmailFake) options(e *testEnv) runOptions {
	return runOptions{
		cwd: e.dir, client: e.client, now: func() time.Time { return testNow },
		gmailAuthURL: "https://accounts.example/auth", gmailTokenURL: g.srv.URL + "/token", gmailSendURL: g.srv.URL + "/send",
		openURL: func(u string) error {
			parsed, _ := url.Parse(u)
			q := parsed.Query()
			go func() {
				resp, err := http.Get(q.Get("redirect_uri") + "/?code=c&state=" + url.QueryEscape(q.Get("state")))
				if err == nil {
					resp.Body.Close()
				}
			}()
			return nil
		},
	}
}

func clearGmailEnv(t *testing.T) {
	t.Helper()
	for _, n := range []string{"STOXICITY_GMAIL_CLIENT_ID", "STOXICITY_GMAIL_CLIENT_SECRET", "STOXICITY_GMAIL_REFRESH_TOKEN", "STOXICITY_MAIL_FROM", "STOXICITY_MAIL_TO"} {
		t.Setenv(n, "")
	}
}

func TestGmailStatusNamesEmptyValuesAndShowsNone(t *testing.T) {
	e := newTestEnv(t)
	clearGmailEnv(t)
	t.Setenv("STOXICITY_GMAIL_CLIENT_SECRET", "must-not-be-shown")
	r := e.run(t, "gmail", "status")
	r.wantCode(t, exitOK)
	r.wantOut(t, "STOXICITY_GMAIL_CLIENT_ID      EMPTY", "STOXICITY_GMAIL_CLIENT_SECRET  set", "none yet", "7 days")
	r.wantNotOut(t, "must-not-be-shown")
}

func TestGmailAuthStoresTheTokenPrivatelyAndTestSendsWithIt(t *testing.T) {
	e := newTestEnv(t)
	clearGmailEnv(t)
	t.Setenv("STOXICITY_GMAIL_CLIENT_ID", gmClientID)
	t.Setenv("STOXICITY_MAIL_FROM", "me@example.com")
	t.Setenv("STOXICITY_MAIL_TO", "me@example.com")
	g := newGmailFake(t)
	opts := g.options(e)
	args := func(more ...string) []string { return append([]string{"-config-dir", e.dir}, more...) }

	r := runWith(t, opts, args("gmail", "auth")...)
	r.wantCode(t, exitOK)
	r.wantOut(t, "Authorised", "owner-only")
	r.wantNotOut(t, gmRefresh, gmAccess)

	tokenPath := filepath.Join(e.dir, "data", "gmail", "refresh_token")
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, want 0600", info.Mode().Perm())
	}
	if dir, _ := os.Stat(filepath.Dir(tokenPath)); dir.Mode().Perm() != 0o700 {
		t.Errorf("token folder mode = %v, want 0700", dir.Mode().Perm())
	}
	if raw, _ := os.ReadFile(tokenPath); strings.TrimSpace(string(raw)) != gmRefresh {
		t.Error("the stored token is not the one Google issued")
	}

	r = runWith(t, opts, args("gmail", "status")...)
	r.wantOut(t, "stored in")
	r.wantNotOut(t, gmRefresh)

	r = runWith(t, opts, args("gmail", "test")...)
	r.wantCode(t, exitOK)
	r.wantOut(t, "Sent a test message")
	if len(g.sent) != 1 {
		t.Fatalf("%d messages sent, want 1", len(g.sent))
	}
}

func TestGmailTestNamesWhatIsMissing(t *testing.T) {
	e := newTestEnv(t)
	clearGmailEnv(t)
	r := e.run(t, "gmail", "test")
	if r.code == exitOK {
		t.Fatal("exit 0 without any setting")
	}
	for _, want := range []string{"STOXICITY_GMAIL_CLIENT_ID", "STOXICITY_MAIL_FROM", "STOXICITY_MAIL_TO", "gmail auth"} {
		if !strings.Contains(r.errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.errOut)
		}
	}
}
