// Package stockfeeds finds the feeds of each stock and records them for the
// pools of package feeds/pool to read. There are two kinds, both the company's
// own publication and nothing from a third-party newsroom:
//
//   - its filing record at the SEC, known for every stock that has a CIK, so
//     it is only written down (Seed);
//   - the news or investor-relations feed its website points to, which has to be
//     looked for (Discover): the home page is read for <link rel="alternate">
//     feeds, and, if it names none, the news, press and investor pages it links
//     to. A candidate is kept only if it parses as a feed with entries and lives
//     on the company's own site. The site's robots.txt is honoured, and a company
//     is searched again only after Recheck, so one whose website has no feed is
//     not asked every time.
package stockfeeds

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/store"
)

// Defaults.
const (
	DefaultRecheck = 30 * 24 * time.Hour
	DefaultWorkers = 4

	maxPages      = 3 // news, press or investor pages read when the home page names no feed
	maxCandidates = 8 // feed addresses tried per company
	maxFeeds      = 3 // feeds kept per company
)

// SECURL is the submissions file of a company, where its filings are listed.
func SECURL(cik string) string {
	return "https://data.sec.gov/submissions/CIK" + padCIK(cik) + ".json"
}

func padCIK(cik string) string {
	cik = strings.TrimSpace(cik)
	if cik == "" || strings.Trim(cik, "0123456789") != "" {
		return ""
	}
	return strings.Repeat("0", max(10-len(cik), 0)) + cik
}

// Stock is what the search needs to know of a stock.
type Stock struct {
	Ticker  string
	CIK     string // empty when the SEC has no record of it
	Website string // as the SEC gives it, often without a scheme
}

// Stocks joins the SEC ticker list with the company records: each ticker with
// its CIK and its website: the one the SEC record gives, else the one Wikidata
// knows for the ticker.
func Stocks(ctx context.Context, ref store.Reference) ([]Stock, error) {
	tickers, err := ref.RefTickers(ctx)
	if err != nil {
		return nil, err
	}
	companies, err := ref.RefCompanies(ctx)
	if err != nil {
		return nil, err
	}
	site := make(map[string]string, len(companies))
	for _, c := range companies {
		if !c.Missing && c.Website != "" {
			site[padCIK(c.CIK)] = c.Website
		}
	}
	sites, err := ref.RefWebsites(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]string, len(sites))
	for _, w := range sites {
		if w.URL != "" {
			known[w.Ticker] = w.URL
		}
	}
	out := make([]Stock, 0, len(tickers))
	for _, t := range tickers {
		cik := padCIK(t.CIK)
		web := site[cik]
		if web == "" {
			web = known[t.Ticker]
		}
		out = append(out, Stock{Ticker: t.Ticker, CIK: cik, Website: web})
	}
	return out, nil
}

// Store is the part of the database the keeper needs.
type Store interface {
	PutStockFeeds(ctx context.Context, list []store.StockFeed, at time.Time) (int, error)
	StockFeedSearches(ctx context.Context) (map[string]store.StockFeedSearch, error)
	SaveStockFeedSearch(ctx context.Context, ticker string, s store.StockFeedSearch) error
}

// Keeper records and looks for the feeds of stocks.
type Keeper struct {
	Store  Store
	Client httpx.Client // may be nil when only Seed is used
	Now    func() time.Time
	// Recheck is how long a search stands before it is made again; DefaultRecheck if 0.
	Recheck time.Duration
	// Workers is how many companies are searched at once; DefaultWorkers if 0.
	Workers int
	// Logf receives one line per notable thing; may be nil.
	Logf func(format string, args ...any)
}

func (k *Keeper) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

// Seed records the SEC filing record of every stock that has a CIK. Several
// tickers of one company (GOOG, GOOGL) share one record, kept under the first
// of them, so the SEC is asked once. It returns how many records are new.
func (k *Keeper) Seed(ctx context.Context, stocks []Stock) (int, error) {
	first := map[string]string{}
	for _, s := range stocks {
		cik := padCIK(s.CIK)
		if cik == "" {
			continue
		}
		if t, ok := first[cik]; !ok || s.Ticker < t {
			first[cik] = s.Ticker
		}
	}
	list := make([]store.StockFeed, 0, len(first))
	for cik, ticker := range first {
		list = append(list, store.StockFeed{Ticker: ticker, Kind: store.FeedSEC, URL: SECURL(cik), Title: "SEC filings"})
	}
	slices.SortFunc(list, func(a, b store.StockFeed) int { return strings.Compare(a.Ticker, b.Ticker) })
	return k.Store.PutStockFeeds(ctx, list, k.now())
}

// Report says what a round of searching did.
type Report struct {
	Searched int // companies whose website was read
	Found    int // feeds recorded that were not known
	None     int // companies whose website names no feed
	Failed   int // companies whose website could not be read
}

// Discover searches the websites of up to budget stocks that have not been
// searched lately, those named in priority first, then in ticker order. Stocks
// that share a website (two classes of one company) are searched once.
func (k *Keeper) Discover(ctx context.Context, stocks []Stock, priority []string, budget int) (Report, error) {
	var rep Report
	if budget <= 0 || k.Client == nil {
		return rep, nil
	}
	done, err := k.Store.StockFeedSearches(ctx)
	if err != nil {
		return rep, err
	}
	now := k.now()
	recheck := k.Recheck
	if recheck <= 0 {
		recheck = DefaultRecheck
	}
	rank := make(map[string]int, len(priority))
	for i, t := range priority {
		rank[t] = i + 1
	}
	var todo []Stock
	for _, s := range stocks {
		if s.Website == "" || webAddress(s.Website) == "" {
			continue
		}
		last, searched := done[s.Ticker]
		wait := recheck
		if searched && last.Error != "" {
			wait = recheck / 6 // a site that was down is tried again sooner
		}
		if searched && now.Sub(last.CheckedAt) < wait {
			continue
		}
		todo = append(todo, s)
	}
	slices.SortStableFunc(todo, func(a, b Stock) int {
		ra, rb := rank[a.Ticker], rank[b.Ticker]
		switch {
		case ra != rb && ra != 0 && rb != 0:
			return ra - rb
		case ra != rb && ra != 0:
			return -1
		case ra != rb:
			return 1
		}
		return strings.Compare(a.Ticker, b.Ticker)
	})
	// One search per website: the others of the company get the same feeds.
	seen := map[string]bool{}
	var batch []Stock
	for _, s := range todo {
		host := hostOf(webAddress(s.Website))
		if seen[host] {
			continue
		}
		seen[host] = true
		if batch = append(batch, s); len(batch) >= budget {
			break
		}
	}

	workers := k.Workers
	if workers <= 0 {
		workers = DefaultWorkers
	}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
		ch = make(chan Stock)
	)
	for w := 0; w < min(workers, len(batch)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range ch {
				if ctx.Err() != nil {
					continue
				}
				found, err := k.search(ctx, s)
				sctx := context.WithoutCancel(ctx)
				var added int
				if len(found) > 0 {
					added, _ = k.Store.PutStockFeeds(sctx, found, now)
				}
				search := store.StockFeedSearch{CheckedAt: now, Found: len(found)}
				if err != nil {
					search.Error = err.Error()
				}
				_ = k.Store.SaveStockFeedSearch(sctx, s.Ticker, search)
				mu.Lock()
				rep.Searched++
				rep.Found += added
				switch {
				case err != nil:
					rep.Failed++
				case len(found) == 0:
					rep.None++
				}
				mu.Unlock()
			}
		}()
	}
	for _, s := range batch {
		ch <- s
	}
	close(ch)
	wg.Wait()
	if rep.Found > 0 && k.Logf != nil {
		k.Logf("stock feeds: %d websites read, %d feeds found, %d have none, %d unreachable", rep.Searched, rep.Found, rep.None, rep.Failed)
	}
	return rep, ctx.Err()
}

// webAddress makes a website an https address: the SEC gives it without a scheme.
func webAddress(site string) string {
	site = strings.TrimSpace(site)
	if site == "" {
		return ""
	}
	if !strings.Contains(site, "://") {
		site = "https://" + site
	}
	u, err := url.Parse(site)
	if err != nil || u.Hostname() == "" || !strings.Contains(u.Hostname(), ".") {
		return ""
	}
	u.Scheme = "https"
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// String names a report in a line.
func (r Report) String() string {
	return fmt.Sprintf("%d websites read, %d new feeds, %d without one, %d unreachable", r.Searched, r.Found, r.None, r.Failed)
}
