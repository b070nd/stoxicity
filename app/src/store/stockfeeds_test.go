package store

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
)

func TestStockFeedsKeepTheirStateWhenRecordedAgain(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	n, err := db.PutStockFeeds(ctx, []StockFeed{
		{Ticker: "AAPL", Kind: FeedSEC, URL: "https://data.sec.gov/submissions/CIK0000320193.json", Title: "SEC"},
		{Ticker: "AAPL", Kind: FeedSite, URL: "https://www.apple.com/newsroom/rss-feed.rss", Title: "Newsroom"},
	}, t0)
	if err != nil || n != 2 {
		t.Fatalf("added %d, %v", n, err)
	}
	f := StockFeed{Ticker: "AAPL", Kind: FeedSite, URL: "https://www.apple.com/newsroom/rss-feed.rss",
		State: feeds.State{ETag: "e1", Seen: []string{"a", "b"}}, NextDue: t0.Add(time.Hour), LastOK: t0, LastNew: 3}
	if err := db.SaveStockFeed(ctx, f); err != nil {
		t.Fatal(err)
	}
	// Seeding again changes nothing but a title.
	n, err = db.PutStockFeeds(ctx, []StockFeed{{Ticker: "AAPL", Kind: FeedSite, URL: f.URL, Title: "Apple Newsroom"}}, t0.Add(time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("added %d, %v; a known feed is not new", n, err)
	}
	got, err := db.StockFeedsOf(ctx, "AAPL")
	if err != nil || len(got) != 2 {
		t.Fatalf("feeds %v, %v", got, err)
	}
	site := got[1]
	if site.Kind != FeedSite || site.Title != "Apple Newsroom" || site.State.ETag != "e1" || len(site.State.Seen) != 2 ||
		!site.NextDue.Equal(t0.Add(time.Hour)) || site.LastNew != 3 || !site.FoundAt.Equal(t0) {
		t.Errorf("site feed = %+v", site)
	}
}

func TestDueStockFeedsAreTheLongestWaitingOfTheKind(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	var list []StockFeed
	for _, tk := range []string{"A", "B", "C", "D"} {
		list = append(list, StockFeed{Ticker: tk, Kind: FeedSEC, URL: "https://x/" + tk})
	}
	list = append(list, StockFeed{Ticker: "A", Kind: FeedSite, URL: "https://site/A"})
	if _, err := db.PutStockFeeds(ctx, list, t0); err != nil {
		t.Fatal(err)
	}
	// B was polled and is not due again for an hour; D is overdue longest.
	if err := db.SaveStockFeed(ctx, StockFeed{Ticker: "B", Kind: FeedSEC, URL: "https://x/B", NextDue: t0.Add(time.Hour), LastOK: t0}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveStockFeed(ctx, StockFeed{Ticker: "D", Kind: FeedSEC, URL: "https://x/D", NextDue: t0.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	due, err := db.DueStockFeeds(ctx, FeedSEC, t0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 || due[0].Ticker != "A" || due[1].Ticker != "C" {
		t.Fatalf("due = %+v: never-polled feeds come before overdue ones", due)
	}
	all, _ := db.DueStockFeeds(ctx, FeedSEC, t0, 10)
	if len(all) != 3 {
		t.Errorf("%d due, want A, C and D (B waits, the site feed is another kind)", len(all))
	}
	if all[2].Ticker != "D" {
		t.Errorf("a feed overdue since an hour ago comes after those never polled: %s", all[2].Ticker)
	}
}

func TestStockFeedStatsAndSearches(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	db.PutStockFeeds(ctx, []StockFeed{
		{Ticker: "A", Kind: FeedSEC, URL: "u1"}, {Ticker: "A", Kind: FeedSite, URL: "u2"}, {Ticker: "B", Kind: FeedSEC, URL: "u3"},
	}, t0)
	db.SaveStockFeed(ctx, StockFeed{Ticker: "A", Kind: FeedSEC, URL: "u1", LastOK: t0})
	db.SaveStockFeed(ctx, StockFeed{Ticker: "B", Kind: FeedSEC, URL: "u3", Failures: 2, LastError: "boom"})
	st, err := db.StockFeedStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := st[FeedSEC]; got.Feeds != 2 || got.Stocks != 2 || got.Polled != 1 || got.Failing != 1 {
		t.Errorf("sec stats = %+v", got)
	}
	if got := st[FeedSite]; got.Feeds != 1 || got.Stocks != 1 {
		t.Errorf("site stats = %+v", got)
	}
	if err := db.SaveStockFeedSearch(ctx, "A", StockFeedSearch{CheckedAt: t0, Found: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveStockFeedSearch(ctx, "A", StockFeedSearch{CheckedAt: t0.Add(time.Hour), Found: 0, Error: "no feed"}); err != nil {
		t.Fatal(err)
	}
	s, _ := db.StockFeedSearches(ctx)
	if len(s) != 1 || s["A"].Found != 0 || s["A"].Error != "no feed" || !s["A"].CheckedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("searches = %+v", s)
	}
}
