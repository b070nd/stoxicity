package pool

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/feeds/rss"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

var clock = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// memStore is a Store in memory.
type memStore struct {
	mu    sync.Mutex
	feeds map[string]store.StockFeed // by kind|ticker|url
}

func newMem(list ...store.StockFeed) *memStore {
	m := &memStore{feeds: map[string]store.StockFeed{}}
	for _, f := range list {
		m.feeds[f.Kind+"|"+f.Ticker+"|"+f.URL] = f
	}
	return m
}

func (m *memStore) DueStockFeeds(_ context.Context, kind string, now time.Time, limit int) ([]store.StockFeed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.StockFeed
	for _, f := range m.feeds {
		if f.Kind == kind && !f.NextDue.After(now) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ticker < out[j].Ticker })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memStore) SaveStockFeed(_ context.Context, f store.StockFeed) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.feeds[f.Kind+"|"+f.Ticker+"|"+f.URL] = f
	return nil
}

func (m *memStore) get(kind, ticker, url string) store.StockFeed {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.feeds[kind+"|"+ticker+"|"+url]
}

const submissions = `{"name":"Acme Corp","tickers":["ACME"],"filings":{"recent":{
 "accessionNumber":["0000001234-26-000001","0000001234-20-000009"],
 "filingDate":["2026-11-05","2020-01-02"],
 "acceptanceDateTime":["2026-11-05T21:00:00.000Z","2020-01-02T21:00:00.000Z"],
 "form":["8-K","10-K"],"items":["2.02",""],"primaryDocument":["a.htm","b.htm"]}}}`

const feedXML = `<?xml version="1.0"?><rss version="2.0"><channel>
<item><title>Acme opens plant</title><link>https://acme.example/news/plant</link><pubDate>Thu, 05 Nov 2026 10:00:00 GMT</pubDate></item>
<item><title>Acme old news</title><link>https://acme.example/news/old</link><pubDate>Mon, 01 Jan 2018 10:00:00 GMT</pubDate></item>
</channel></rss>`

func secURL(cik string) string { return "https://data.test/submissions/CIK" + cik + ".json" }

func newPool(t *testing.T, st Store, c *httpx.Fake) *Fetcher {
	t.Helper()
	cfg := edgar.Config{DataBase: "https://data.test", WebBase: "https://web.test", Now: func() time.Time { return clock }}
	return New(st, edgar.NewSubmissions(c, cfg), rss.New(c, rss.Config{Now: func() time.Time { return clock }}),
		Config{Now: func() time.Time { return clock }, Rand: func() float64 { return 0.5 }, Workers: 2})
}

func secPool() model.Source {
	return model.Source{ID: "stock-feeds-sec", Kind: model.KindStockFeeds, Pool: model.PoolSEC, Forms: []string{"8-K", "10-K"}}
}

func TestSECPoolReadsDueFeedsAndSchedulesThemAgain(t *testing.T) {
	c := httpx.NewFake()
	c.Reply(secURL("0000001234"), httpx.Response{Status: http.StatusOK, Body: []byte(submissions), Header: http.Header{"Etag": {"v1"}}})
	url := "https://data.sec.gov/submissions/CIK0000001234.json"
	st := newMem(store.StockFeed{Ticker: "ACME", Kind: store.FeedSEC, URL: url})

	res, err := newPool(t, st, c).Fetch(context.Background(), secPool(), feeds.State{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Articles) != 1 || res.Articles[0].Form != "8-K" {
		t.Fatalf("articles = %+v: the 2020 annual report is older than the lookback", res.Articles)
	}
	if res.Articles[0].SourceID != "stock-feeds-sec" {
		t.Errorf("source id %q: articles belong to the pool", res.Articles[0].SourceID)
	}
	// Until the articles are stored the feed has not moved: a crash now would
	// otherwise leave its items marked as seen and never stored.
	if before := st.get(store.FeedSEC, "ACME", url); before.State.ETag != "" || !before.LastOK.IsZero() {
		t.Errorf("feed saved before its articles were stored: %+v", before)
	}
	if res.Commit == nil {
		t.Fatal("no Commit to run after the articles are stored")
	}
	if err := res.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := st.get(store.FeedSEC, "ACME", url)
	if !f.LastOK.Equal(clock) || f.LastNew != 1 || f.Failures != 0 {
		t.Errorf("feed after a good read: %+v", f)
	}
	if f.NextDue.Sub(clock) != DefaultCycleSEC {
		t.Errorf("next due in %v, want the cycle %v", f.NextDue.Sub(clock), DefaultCycleSEC)
	}
	if f.State.ETag != "v1" || len(f.State.Seen) == 0 {
		t.Errorf("the feed keeps its own state: %+v", f.State)
	}

	// Not due again until its cycle is over: nothing is asked.
	n := len(c.Calls())
	res, err = newPool(t, st, c).Fetch(context.Background(), secPool(), feeds.State{})
	if err != nil || len(res.Articles) != 0 || !res.NotModified || len(c.Calls()) != n {
		t.Errorf("second poll: %+v %v, %d calls", res, err, len(c.Calls())-n)
	}
}

func TestSitePoolTagsArticlesWithTheStock(t *testing.T) {
	c := httpx.NewFake()
	feed := "https://acme.example/news.xml"
	c.Reply(feed, httpx.Response{Status: http.StatusOK, Body: []byte(feedXML)})
	st := newMem(store.StockFeed{Ticker: "ACME", Kind: store.FeedSite, URL: feed})
	src := model.Source{ID: "stock-feeds-sites", Kind: model.KindStockFeeds, Pool: model.PoolSite, Licence: model.LicenceInternal, Owner: "default"}

	res, err := newPool(t, st, c).Fetch(context.Background(), src, feeds.State{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Articles) != 1 {
		t.Fatalf("articles = %+v, want the one inside the lookback", res.Articles)
	}
	a := res.Articles[0]
	if len(a.Tickers) != 1 || a.Tickers[0] != "ACME" || a.Domain != "acme.example" {
		t.Errorf("article %+v: tagged with the stock and its publisher", a)
	}
}

func TestAFailingFeedRestsLongerEachTimeAndDoesNotHideTheOthers(t *testing.T) {
	c := httpx.NewFake()
	good, bad := "https://good.example/feed.xml", "https://bad.example/feed.xml"
	c.Reply(good, httpx.Response{Status: http.StatusOK, Body: []byte(feedXML)})
	c.Fail(bad, &httpx.Error{Class: httpx.Retryable, URL: bad, Err: errors.New("timeout")})
	st := newMem(
		store.StockFeed{Ticker: "BAD", Kind: store.FeedSite, URL: bad},
		store.StockFeed{Ticker: "GOOD", Kind: store.FeedSite, URL: good},
	)
	src := model.Source{ID: "s", Kind: model.KindStockFeeds, Pool: model.PoolSite}
	res, err := newPool(t, st, c).Fetch(context.Background(), src, feeds.State{})
	if err != nil || len(res.Articles) != 1 {
		t.Fatalf("one good feed must still be read: %+v %v", res, err)
	}
	b := st.get(store.FeedSite, "BAD", bad)
	if b.Failures != 1 || b.LastError == "" || !b.NextDue.After(clock) {
		t.Errorf("failed feed: %+v", b)
	}
	first := b.NextDue.Sub(clock)
	b.NextDue = time.Time{}
	_ = st.SaveStockFeed(context.Background(), b)
	_, _ = newPool(t, st, c).Fetch(context.Background(), src, feeds.State{})
	if second := st.get(store.FeedSite, "BAD", bad).NextDue.Sub(clock); second <= first {
		t.Errorf("rest %v after a second failure is not longer than %v", second, first)
	}
}

func TestAGoneFeedRestsForAWeek(t *testing.T) {
	c := httpx.NewFake()
	gone := "https://gone.example/feed.xml"
	c.Fail(gone, &httpx.Error{Class: httpx.Permanent, Status: 404, URL: gone})
	st := newMem(store.StockFeed{Ticker: "GONE", Kind: store.FeedSite, URL: gone})
	_, err := newPool(t, st, c).Fetch(context.Background(), model.Source{ID: "s", Kind: model.KindStockFeeds, Pool: model.PoolSite}, feeds.State{})
	if err == nil {
		t.Fatal("a batch that read nothing is an error")
	}
	if rest := st.get(store.FeedSite, "GONE", gone).NextDue.Sub(clock); rest < 6*24*time.Hour {
		t.Errorf("a feed that is gone rests %v", rest)
	}
}

func TestTheSECBlockingStopsTheBatchAndLeavesFeedsDue(t *testing.T) {
	c := httpx.NewFake()
	u1, u2 := "https://data.sec.gov/submissions/CIK0000000001.json", "https://data.sec.gov/submissions/CIK0000000002.json"
	c.Fail(secURL("0000000001"), &httpx.Error{Class: httpx.Throttled, Status: 429, URL: u1})
	c.Fail(secURL("0000000002"), &httpx.Error{Class: httpx.Throttled, Status: 429, URL: u2})
	st := newMem(store.StockFeed{Ticker: "A", Kind: store.FeedSEC, URL: u1}, store.StockFeed{Ticker: "B", Kind: store.FeedSEC, URL: u2})
	p := newPool(t, st, c)
	p.cfg.Workers = 1
	_, err := p.Fetch(context.Background(), secPool(), feeds.State{})
	var he *httpx.Error
	if !errors.As(err, &he) || he.Class != httpx.Throttled {
		t.Fatalf("err = %v, want the throttle to reach the scheduler", err)
	}
	if len(c.Calls()) != 1 {
		t.Errorf("%d requests: the second feed must not be sent to a throttled host", len(c.Calls()))
	}
	for _, tk := range []string{"A", "B"} {
		u := u1
		if tk == "B" {
			u = u2
		}
		if f := st.get(store.FeedSEC, tk, u); f.Failures != 0 || !f.NextDue.IsZero() {
			t.Errorf("%s: a throttle is the host's fault, the feed stays due: %+v", tk, f)
		}
	}
}

func TestSourceNeedsAPool(t *testing.T) {
	_, err := newPool(t, newMem(), httpx.NewFake()).Fetch(context.Background(), model.Source{ID: "x", Kind: model.KindStockFeeds}, feeds.State{})
	if err == nil {
		t.Fatal("a pool source with no pool must fail")
	}
}
