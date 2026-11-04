package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Synthetic fixture, not verified against live data.
// secretPart stands in for the secret part of a webhook address. No error may
// contain it.
const secretPart = "s3cr3t-hook"

func TestWebhookPayloadShape(t *testing.T) {
	var gotMethod, gotType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ch, err := NewWebhook(srv.URL+"/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Kind() != Webhook {
		t.Errorf("Kind() = %q", ch.Kind())
	}
	if err := ch.Send(context.Background(), sampleNotification()); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q", gotType)
	}

	var got map[string]any
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	want := map[string]any{
		"tenant": "buffett-core",
		"watch":  "acme",
		"alert":  "guidance cut",
		"title":  "Acme cuts guidance",
		"body":   "Acme lowered its full-year outlook.\n\nShares fell.",
		"link":   "/w/buffett-core/stock/ACME",
		"at":     "2026-11-04T10:30:05Z",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if len(got) != len(want)+1 {
		t.Errorf("payload has %d keys, want %d plus articles", len(got), len(want))
	}
	arts, ok := got["articles"].([]any)
	if !ok || len(arts) != 2 {
		t.Fatalf("articles = %v, want 2 entries", got["articles"])
	}
	first, ok := arts[0].(map[string]any)
	if !ok {
		t.Fatalf("article is %T", arts[0])
	}
	for _, k := range []string{"title", "url", "domain", "published"} {
		if _, ok := first[k]; !ok {
			t.Errorf("article has no %q key", k)
		}
	}
	if first["url"] != "https://news.example.com/acme-cut" {
		t.Errorf("article url = %v", first["url"])
	}
}

func TestWebhookHTTPSWorks(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ch, err := NewWebhook(srv.URL+"/hook", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(context.Background(), sampleNotification()); err != nil {
		t.Errorf("https send: %v", err)
	}
}

func TestNewWebhookURLRules(t *testing.T) {
	cases := []struct {
		raw string
		ok  bool
	}{
		{"https://hooks.example.com/services/T1", true},
		{"http://localhost:8080/hook", true},
		{"http://127.0.0.1:8080/hook", true},
		{"http://[::1]:8080/hook", true},
		{"http://hooks.example.com/" + secretPart, false},
		{"http://127.0.0.2/hook", false},
		{"ftp://hooks.example.com/" + secretPart, false},
		{"https://user:" + secretPart + "@hooks.example.com/x", false},
		{"https:///" + secretPart, false},
		{"http://%zz/" + secretPart, false},
	}
	for _, c := range cases {
		_, err := NewWebhook(c.raw, nil)
		if (err == nil) != c.ok {
			t.Errorf("NewWebhook(%s): err = %v, want ok=%v", c.raw, err, c.ok)
		}
		if err != nil && strings.Contains(err.Error(), secretPart) {
			t.Errorf("NewWebhook error names the secret: %v", err)
		}
	}
}

func TestNewWebhookDefaultClient(t *testing.T) {
	ch, err := NewWebhook("https://hooks.example.com/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ch.client.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", ch.client.Timeout)
	}
	if ch.client.CheckRedirect == nil {
		t.Error("default client follows redirects")
	}
}

func TestWebhookStatusHandling(t *testing.T) {
	cases := []struct {
		code      int
		wantErr   bool
		wantRetry bool
	}{
		{http.StatusOK, false, false},
		{http.StatusCreated, false, false},
		{http.StatusNoContent, false, false},
		{http.StatusBadRequest, true, false},
		{http.StatusForbidden, true, false},
		{http.StatusNotFound, true, false},
		{http.StatusTooManyRequests, true, true},
		{http.StatusInternalServerError, true, true},
		{http.StatusServiceUnavailable, true, true},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.code)
			}))
			defer srv.Close()
			ch, err := NewWebhook(srv.URL+"/hook", nil)
			if err != nil {
				t.Fatal(err)
			}
			err = ch.Send(context.Background(), sampleNotification())
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, want error=%v", err, c.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), fmt.Sprintf("status %d", c.code)) {
				t.Errorf("error %q does not mention status %d", err, c.code)
			}
			if errors.Is(err, ErrRetry) != c.wantRetry {
				t.Errorf("retryable = %v, want %v", errors.Is(err, ErrRetry), c.wantRetry)
			}
		})
	}
}

func TestWebhookErrorsNeverNameTheURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "echo "+secretPart)
	}))
	ch, err := NewWebhook(srv.URL+"/hook/"+secretPart+"?key="+secretPart, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = ch.Send(context.Background(), sampleNotification())
	if err == nil {
		t.Fatal("expected a status error")
	}
	if strings.Contains(err.Error(), secretPart) {
		t.Errorf("status error names the secret: %v", err)
	}
	if want := "webhook: POST failed with status 400"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	srv.Close()
	err = ch.Send(context.Background(), sampleNotification())
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), secretPart) {
		t.Errorf("transport error names the secret: %v", err)
	}
}

func TestWebhookDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/elsewhere", http.StatusFound)
	}))
	defer redirect.Close()

	for _, client := range []*http.Client{nil, redirect.Client()} {
		ch, err := NewWebhook(redirect.URL+"/hook", client)
		if err != nil {
			t.Fatal(err)
		}
		err = ch.Send(context.Background(), sampleNotification())
		if err == nil || !strings.Contains(err.Error(), "status 302") {
			t.Errorf("err = %v, want a 302 status error", err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("redirect target was contacted %d times", n)
	}
}

func TestWebhookBodyIsCapped(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n := sampleNotification()
	n.Articles = nil
	long := strings.Repeat("t", 1000)
	for i := 0; i < 1000; i++ {
		n.Articles = append(n.Articles, model.Article{
			URL:    fmt.Sprintf("https://news.example.com/%d", i),
			Title:  fmt.Sprintf("%d %s", i, long),
			Domain: "news.example.com",
		})
	}
	ch, err := NewWebhook(srv.URL+"/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(got) > 256<<10 {
		t.Fatalf("request body is %d bytes, over the 256 KiB limit", len(got))
	}
	var p struct {
		Tenant   string `json:"tenant"`
		Articles []struct {
			URL string `json:"url"`
		} `json:"articles"`
	}
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("truncated body is not valid JSON: %v", err)
	}
	if p.Tenant != "buffett-core" {
		t.Errorf("tenant = %q; the structure must stay whole", p.Tenant)
	}
	if len(p.Articles) == 0 || len(p.Articles) >= 1000 {
		t.Fatalf("kept %d articles, want some but not all", len(p.Articles))
	}
	if p.Articles[0].URL != "https://news.example.com/0" {
		t.Errorf("first kept article = %q; the first articles must be kept", p.Articles[0].URL)
	}
}

func TestWebhookRefusesOversizedNotification(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	n := sampleNotification()
	n.Articles = nil
	n.Body = strings.Repeat("x", 300<<10)
	ch, err := NewWebhook(srv.URL+"/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(context.Background(), n); err == nil {
		t.Error("expected an error for a body that cannot fit")
	}
	if hits.Load() != 0 {
		t.Error("an oversized notification was sent")
	}
}

// endlessBody yields bytes without end, up to a limit, and counts what is read.
type endlessBody struct {
	read int
}

func (e *endlessBody) Read(p []byte) (int, error) {
	if e.read >= 1<<20 {
		return 0, io.EOF
	}
	for i := range p {
		p[i] = 'x'
	}
	e.read += len(p)
	return len(p), nil
}

// fakeTransport answers every request with a 200 and an endless body.
type fakeTransport struct {
	body *endlessBody
}

func (f fakeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(f.body),
	}, nil
}

func TestWebhookResponseIsCapped(t *testing.T) {
	body := &endlessBody{}
	client := &http.Client{Transport: fakeTransport{body: body}}
	ch, err := NewWebhook("https://hooks.example.com/x", client)
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(context.Background(), sampleNotification()); err != nil {
		t.Fatal(err)
	}
	if body.read > 4<<10 {
		t.Errorf("read %d bytes of the response, want at most 4 KiB", body.read)
	}
}
