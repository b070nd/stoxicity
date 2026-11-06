package registry

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// scriptedClient returns the same answer to every request and records the URLs.
type scriptedClient struct {
	resp *httpx.Response
	err  error
	urls []string
}

func (c *scriptedClient) Do(_ context.Context, req httpx.Request) (*httpx.Response, error) {
	c.urls = append(c.urls, req.URL)
	return c.resp, c.err
}

func TestNewHasEveryKind(t *testing.T) {
	m := New(&scriptedClient{}, Options{})
	for _, k := range []model.Kind{model.KindRSS, model.KindFilings, model.KindAPI} {
		if m[k] == nil {
			t.Errorf("no fetcher for kind %q", k)
		}
	}
	if len(m) != 3 {
		t.Errorf("got %d fetchers, want 3: the pools need a store", len(m))
	}
	if m := New(&scriptedClient{}, Options{StockFeeds: poolStore{}}); m[model.KindStockFeeds] == nil || len(m) != 4 {
		t.Errorf("with a store the pools have a fetcher: %d fetchers", len(m))
	}
}

// poolStore is a store with nothing in it.
type poolStore struct{}

func (poolStore) DueStockFeeds(context.Context, string, time.Time, int) ([]store.StockFeed, error) {
	return nil, nil
}
func (poolStore) SaveStockFeed(context.Context, store.StockFeed) error { return nil }

func TestDispatch(t *testing.T) {
	const atom = `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"></feed>`
	const rssBody = `<rss><channel></channel></rss>`
	tests := []struct {
		name    string
		src     model.Source
		body    string
		wantURL string
	}{
		{"rss", model.Source{ID: "r", Kind: model.KindRSS, URL: "https://feed.example/x.xml"}, rssBody, "https://feed.example/x.xml"},
		{"current", model.Source{ID: "c", Kind: model.KindFilings, URL: "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&output=atom", Forms: []string{"8-K"}}, atom, "http://web.test/cgi-bin/browse-edgar?"},
		{"form4", model.Source{ID: "f", Kind: model.KindFilings, URL: "https://www.sec.gov/cgi-bin/browse-edgar", Tags: []string{"form4"}}, atom, "type=4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &scriptedClient{resp: &httpx.Response{Status: 200, Body: []byte(tc.body)}}
			m := New(c, Options{WebBase: "http://web.test", DataBase: "http://data.test"})
			if _, err := m[tc.src.Kind].Fetch(context.Background(), tc.src, feeds.State{}); err != nil {
				t.Fatal(err)
			}
			if len(c.urls) != 1 || !strings.Contains(c.urls[0], tc.wantURL) {
				t.Errorf("requested %v, want it to contain %q", c.urls, tc.wantURL)
			}
		})
	}

	t.Run("submissions", func(t *testing.T) {
		c := &scriptedClient{resp: &httpx.Response{Status: 200, Body: []byte(`{"name":"X","filings":{"recent":{}}}`)}}
		m := New(c, Options{WebBase: "http://web.test", DataBase: "http://data.test"})
		src := model.Source{ID: "s", Kind: model.KindFilings, URL: "https://data.sec.gov/submissions/CIK0000320193.json"}
		if _, err := m[model.KindFilings].Fetch(context.Background(), src, feeds.State{}); err != nil {
			t.Fatal(err)
		}
		if len(c.urls) != 1 || c.urls[0] != "http://data.test/submissions/CIK0000320193.json" {
			t.Errorf("requested %v", c.urls)
		}
	})
}

// TestCredentialNeverInErrors sets a value in the variable a source names with
// auth_env and checks that no fetcher puts it in an error, whether the request
// fails, returns junk or is blocked.
func TestCredentialNeverInErrors(t *testing.T) {
	const secret = "tok-0123456789-do-not-leak"
	t.Setenv("STOXICITY_TEST_TOKEN", secret)

	failures := map[string]*scriptedClient{
		"transport": {err: &httpx.Error{Class: httpx.Retryable, URL: "https://x.example/feed", Err: errors.New("connection reset")}},
		"junk":      {resp: &httpx.Response{Status: 200, Body: []byte("not a feed")}},
		"html":      {resp: &httpx.Response{Status: 200, Header: http.Header{}, Body: []byte("<html>blocked</html>")}},
	}
	sources := []model.Source{
		{ID: "a", Kind: model.KindRSS, URL: "https://x.example/feed?key=" + secret, AuthEnv: "STOXICITY_TEST_TOKEN"},
		{ID: "b", Kind: model.KindFilings, URL: "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent", AuthEnv: "STOXICITY_TEST_TOKEN", Forms: []string{"8-K"}},
		{ID: "c", Kind: model.KindFilings, URL: "https://data.sec.gov/submissions/CIK0000320193.json?k=" + secret, AuthEnv: "STOXICITY_TEST_TOKEN"},
		{ID: "d", Kind: model.KindAPI, URL: "https://api.example.com/v1?token=" + secret, AuthEnv: "STOXICITY_TEST_TOKEN"},
	}
	for name, c := range failures {
		for _, src := range sources {
			t.Run(name+"/"+src.ID, func(t *testing.T) {
				m := New(c, Options{})
				_, err := m[src.Kind].Fetch(context.Background(), src, feeds.State{})
				if err == nil {
					t.Fatal("expected an error")
				}
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaks the credential: %q", err)
				}
			})
		}
	}
}
