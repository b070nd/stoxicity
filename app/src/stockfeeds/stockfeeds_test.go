package stockfeeds

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/store"
)

var clock = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

type mem struct {
	mu       sync.Mutex
	feeds    []store.StockFeed
	searches map[string]store.StockFeedSearch
}

func newMem() *mem { return &mem{searches: map[string]store.StockFeedSearch{}} }

func (m *mem) PutStockFeeds(_ context.Context, list []store.StockFeed, _ time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	added := 0
next:
	for _, f := range list {
		for _, o := range m.feeds {
			if o.Ticker == f.Ticker && o.Kind == f.Kind && o.URL == f.URL {
				continue next
			}
		}
		m.feeds = append(m.feeds, f)
		added++
	}
	return added, nil
}

func (m *mem) StockFeedSearches(context.Context) (map[string]store.StockFeedSearch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]store.StockFeedSearch{}
	for k, v := range m.searches {
		out[k] = v
	}
	return out, nil
}

func (m *mem) SaveStockFeedSearch(_ context.Context, t string, s store.StockFeedSearch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searches[t] = s
	return nil
}

const rssBody = `<?xml version="1.0"?><rss version="2.0"><channel><item><title>x</title><link>https://acme.com/n/1</link></item></channel></rss>`

func page(body string) httpx.Response {
	return httpx.Response{Status: http.StatusOK, Body: []byte(body)}
}

func TestSeedKeepsOneRecordPerCompany(t *testing.T) {
	m := newMem()
	k := &Keeper{Store: m, Now: func() time.Time { return clock }}
	n, err := k.Seed(context.Background(), []Stock{
		{Ticker: "GOOGL", CIK: "1652044"}, {Ticker: "GOOG", CIK: "0001652044"}, {Ticker: "AAPL", CIK: "320193"}, {Ticker: "NOCIK"},
	})
	if err != nil || n != 2 {
		t.Fatalf("seeded %d, %v: want one record each for Alphabet and Apple", n, err)
	}
	got := map[string]string{}
	for _, f := range m.feeds {
		got[f.Ticker] = f.URL
	}
	if got["GOOG"] != "https://data.sec.gov/submissions/CIK0001652044.json" || got["AAPL"] != "https://data.sec.gov/submissions/CIK0000320193.json" || len(got) != 2 {
		t.Errorf("records: %v", got)
	}
	if n, _ := k.Seed(context.Background(), []Stock{{Ticker: "AAPL", CIK: "320193"}}); n != 0 {
		t.Errorf("a second seed adds %d", n)
	}
}

func TestDiscoverFollowsTheInvestorPageAndKeepsOnlyOwnSiteFeeds(t *testing.T) {
	c := httpx.NewFake()
	c.Reply("https://acme.com/robots.txt", page("User-agent: *\nDisallow: /private\n"))
	c.Reply("https://acme.com/", page(`<html><body><a href="/about">About us</a> <a href="/investors/news">Investor news</a></body></html>`))
	c.Reply("https://acme.com/investors/news", page(`<link rel="alternate" type="application/atom+xml" title="Press releases" href="https://investors.acme.com/rss">
	  <link rel="alternate" type="application/rss+xml" href="https://other.example/feed.xml">`))
	c.Reply("https://investors.acme.com/rss", page(rssBody))
	m := newMem()
	k := &Keeper{Store: m, Client: c, Now: func() time.Time { return clock }, Workers: 1}
	rep, err := k.Discover(context.Background(), []Stock{{Ticker: "ACME", Website: "acme.com"}}, nil, 10)
	if err != nil || rep.Found != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	f := m.feeds[0]
	if f.Kind != store.FeedSite || f.URL != "https://investors.acme.com/rss" || f.Title != "Press releases" {
		t.Errorf("feed %+v", f)
	}
	for _, call := range c.Calls() {
		if call.URL == "https://other.example/feed.xml" {
			t.Error("a feed of another site was fetched")
		}
	}
}

func TestDiscoverHonoursRobotsAndRemembersTheSearch(t *testing.T) {
	c := httpx.NewFake()
	c.Reply("https://acme.com/robots.txt", page("User-agent: *\nDisallow: /\n"))
	m := newMem()
	k := &Keeper{Store: m, Client: c, Now: func() time.Time { return clock }, Workers: 1}
	stocks := []Stock{{Ticker: "ACME", Website: "acme.com"}}
	rep, _ := k.Discover(context.Background(), stocks, nil, 10)
	if rep.Failed != 1 || len(m.feeds) != 0 {
		t.Fatalf("%+v: a site that disallows robots is not read", rep)
	}
	for _, call := range c.Calls() {
		if call.URL == "https://acme.com/" {
			t.Fatal("home page requested although robots.txt forbids it")
		}
	}
	// Searched lately (with an error): left alone for now, tried again later.
	n := len(c.Calls())
	if rep, _ = k.Discover(context.Background(), stocks, nil, 10); rep.Searched != 0 || len(c.Calls()) != n {
		t.Errorf("a recent search is repeated: %+v", rep)
	}
	k.Now = func() time.Time { return clock.Add(10 * 24 * time.Hour) }
	if rep, _ = k.Discover(context.Background(), stocks, nil, 10); rep.Searched != 1 {
		t.Errorf("a failed search is not retried after a few days: %+v", rep)
	}
}

func TestDiscoverTakesPriorityStocksFirstAndOnePerWebsite(t *testing.T) {
	c := httpx.NewFake() // nothing scripted: every search fails, which is fine here
	m := newMem()
	k := &Keeper{Store: m, Client: c, Now: func() time.Time { return clock }, Workers: 1}
	stocks := []Stock{
		{Ticker: "AAA", Website: "a.com"}, {Ticker: "BBB", Website: "b.com"}, {Ticker: "CCC", Website: "c.com"},
		{Ticker: "CCD", Website: "www.c.com"}, {Ticker: "NONE"},
	}
	rep, _ := k.Discover(context.Background(), stocks, []string{"CCC"}, 2)
	if rep.Searched != 2 {
		t.Fatalf("budget 2 searched %d", rep.Searched)
	}
	if _, ok := m.searches["CCC"]; !ok {
		t.Error("the stock you follow is searched first")
	}
	if _, ok := m.searches["CCD"]; ok {
		t.Error("two tickers of one website are searched once")
	}
}

func TestRobotsRules(t *testing.T) {
	r := parseRobots("User-agent: *\nDisallow: /private\nAllow: /private/open\nDisallow: /*.pdf$\n\nUser-agent: badbot\nDisallow: /\n")
	for path, want := range map[string]bool{
		"/": true, "/news": true, "/private": false, "/private/x": false, "/private/open/x": true,
		"/a.pdf": false, "/a.pdf/b.pdf": false, "/a.pdf?x": true,
	} {
		if got := r.allowed(path); got != want {
			t.Errorf("allowed(%q) = %v, want %v", path, got, want)
		}
	}
	if own := parseRobots("User-agent: stoxicity\nDisallow: /\n\nUser-agent: *\nDisallow:\n"); own.allowed("/x") {
		t.Error("a group that names this program applies to it")
	}
}

func TestRegisteredDomain(t *testing.T) {
	for host, want := range map[string]string{"www.acme.com": "acme.com", "investors.acme.co.uk": "acme.co.uk", "acme.com": "acme.com", "a.b.example.org": "example.org"} {
		if got := registered(host); got != want {
			t.Errorf("registered(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestDiscoverReadsTheRobotsOfEachHost(t *testing.T) {
	c := httpx.NewFake()
	c.Reply("https://acme.com/", page(`<link rel="alternate" type="application/rss+xml" href="https://investors.acme.com/rss">`))
	c.Reply("https://investors.acme.com/robots.txt", page("User-agent: *\nDisallow: /rss\n"))
	c.Reply("https://investors.acme.com/rss", page(rssBody))
	m := newMem()
	k := &Keeper{Store: m, Client: c, Now: func() time.Time { return clock }, Workers: 1}
	rep, _ := k.Discover(context.Background(), []Stock{{Ticker: "ACME", Website: "acme.com"}}, nil, 10)
	if rep.Found != 0 || rep.None != 1 {
		t.Errorf("%+v: the investor site asks robots to stay away from its feed", rep)
	}
}
