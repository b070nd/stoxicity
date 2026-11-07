// Package pool reads the feeds of every stock as two sources: the SEC filing
// record of each company and the news feed each company's own website points
// to. Thousands of feeds cannot each be a source of their own (the board would
// list them all, and the scheduler would hold a timer for each), so a pool is
// one source whose every poll reads a batch of them: the ones that have waited
// longest. Each feed keeps its own validators, newest IDs and schedule in the
// database (table stock_feed), so a poll of the pool costs a request per feed in
// the batch and a feed is asked again only when its cycle is over.
//
// A feed that fails rests longer each time, a permanent failure (a feed that
// is gone) for a week. The SEC is one host: when it blocks or throttles, the
// batch stops and the scheduler's cooldown of that host applies to the pool. A
// company's website that refuses us only costs that company's feed a day.
package pool

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// Defaults for what a source leaves at zero.
const (
	DefaultBatchSEC  = 40
	DefaultBatchSite = 25
	DefaultCycleSEC  = 12 * time.Hour
	DefaultCycleSite = 6 * time.Hour
	// DefaultLookback is how old an article may be and still be stored: a feed
	// read for the first time holds years of history, which is not news.
	DefaultLookback = 14 * 24 * time.Hour

	maxRest     = 7 * 24 * time.Hour
	saveTimeout = 10 * time.Second
	jitter      = 0.10 // of the cycle, either way, so feeds do not fall due together
)

// Store is the part of the database a pool needs.
type Store interface {
	DueStockFeeds(ctx context.Context, kind string, now time.Time, limit int) ([]store.StockFeed, error)
	SaveStockFeed(ctx context.Context, f store.StockFeed) error
}

// Config tunes a Fetcher. The zero value is usable.
type Config struct {
	// Now is the clock; time.Now if nil.
	Now func() time.Time
	// Rand returns a number in [0, 1) for jitter; math/rand/v2 if nil. It is
	// called from one goroutine only.
	Rand func() float64
	// Workers is how many feeds of a batch are read at once; 4 if 0. The client
	// still limits the requests to each host.
	Workers int
	// Lookback: older articles are dropped; DefaultLookback if 0.
	Lookback time.Duration
}

// Fetcher implements feeds.Fetcher for model.KindStockFeeds sources.
type Fetcher struct {
	store    Store
	readers  map[string]feeds.Fetcher // by pool
	cfg      Config
	mu       sync.Mutex // guards cfg.Rand
	lookback time.Duration
}

// New returns a Fetcher that reads the SEC pool with sec (a reader of
// submissions files) and the site pool with site (a feed reader).
func New(st Store, sec, site feeds.Fetcher, cfg Config) *Fetcher {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = DefaultLookback
	}
	return &Fetcher{store: st, readers: map[string]feeds.Fetcher{model.PoolSEC: sec, model.PoolSite: site}, cfg: cfg}
}

// outcome is what reading one feed gave.
type outcome struct {
	feed     store.StockFeed
	res      feeds.Result
	err      error
	skipped  bool // not read, because the batch was stopped
	canceled bool
}

// Fetch reads the next batch of the pool src names. It returns an error only if
// the batch read nothing and the reason is one the scheduler should act on.
func (f *Fetcher) Fetch(ctx context.Context, src model.Source, _ feeds.State) (feeds.Result, error) {
	reader := f.readers[src.Pool]
	if reader == nil {
		return feeds.Result{}, &httpx.Error{Class: httpx.Permanent, URL: src.ID,
			Err: errors.New("pool: source " + src.ID + " names no pool: set pool to sec or site")}
	}
	now := f.cfg.Now().UTC()
	batch, cycle := f.sizes(src)
	due, err := f.store.DueStockFeeds(ctx, src.Pool, now, batch)
	if err != nil {
		return feeds.Result{}, err
	}
	if len(due) == 0 {
		return feeds.Result{NotModified: true}, nil
	}

	var (
		results = make([]outcome, len(due))
		stop    = make(chan struct{})
		once    sync.Once
		wg      sync.WaitGroup
		next    = make(chan int)
	)
	for w := 0; w < min(f.cfg.Workers, len(due)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				select {
				case <-stop:
					results[i] = outcome{feed: due[i], skipped: true}
					continue
				default:
				}
				res, err := reader.Fetch(ctx, f.derive(src, due[i]), due[i].State)
				results[i] = outcome{feed: due[i], res: res, err: err, canceled: ctx.Err() != nil}
				if src.Pool == model.PoolSEC && mustStop(err) {
					once.Do(func() { close(stop) })
				}
			}
		}()
	}
	for i := range due {
		next <- i
	}
	close(next)
	wg.Wait()

	return f.settle(ctx, src, results, now, cycle)
}

// settle stores the outcome of each feed and gathers the articles.
func (f *Fetcher) settle(ctx context.Context, src model.Source, results []outcome, now time.Time, cycle time.Duration) (feeds.Result, error) {
	var (
		articles []model.Article
		first    error
		read     int
		stopped  error
	)
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout*time.Duration(1+len(results)/10))
	defer cancel()
	cutoff := now.Add(-f.cfg.Lookback)
	// A feed that returned articles is saved only after they are stored (see
	// feeds.Result.Commit); the rest are saved now.
	var after []store.StockFeed
	saved := func(feed store.StockFeed, deferred bool) {
		if deferred {
			after = append(after, feed)
			return
		}
		if err := f.store.SaveStockFeed(sctx, feed); err != nil && first == nil {
			first = err
		}
	}
	for _, o := range results {
		if o.skipped || o.canceled {
			continue
		}
		feed := o.feed
		kept := 0
		if o.err != nil {
			if src.Pool == model.PoolSEC && mustStop(o.err) {
				stopped = o.err // leave the feed due; the host is cooling down
				continue
			}
			feed.Failures++
			feed.LastError = o.err.Error()
			feed.NextDue = now.Add(f.rest(feed.Failures, o.err, cycle))
			if first == nil {
				first = o.err
			}
		} else {
			read++
			kept = 0
			for _, a := range o.res.Articles {
				if a.Published.Before(cutoff) {
					continue
				}
				if src.Pool == model.PoolSite && len(a.Tickers) == 0 {
					a.Tickers = []string{feed.Ticker}
				}
				articles = append(articles, a)
				kept++
			}
			feed.State = o.res.State
			feed.LastOK, feed.LastNew, feed.Failures, feed.LastError = now, kept, 0, ""
			feed.NextDue = now.Add(f.around(cycle))
		}
		saved(feed, o.err == nil && kept > 0)
	}
	if read == 0 && stopped != nil {
		return feeds.Result{}, stopped
	}
	if read == 0 && first != nil {
		return feeds.Result{}, first
	}
	if len(articles) == 0 {
		return feeds.Result{NotModified: true}, nil
	}
	return feeds.Result{Articles: articles, Commit: func(ctx context.Context) error {
		cctx, stop := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout*time.Duration(1+len(after)/10))
		defer stop()
		var errs []error
		for _, feed := range after {
			if err := f.store.SaveStockFeed(cctx, feed); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}}, nil
}

// derive is the source the reader of one feed is given: the pool's licence and
// tier, the address of the feed.
func (f *Fetcher) derive(pool model.Source, feed store.StockFeed) model.Source {
	one := pool
	one.URL = feed.URL
	one.Pool, one.Batch, one.Cycle = "", 0, ""
	switch pool.Pool {
	case model.PoolSEC:
		one.Kind = model.KindFilings
		one.Domain = "sec.gov"
	default:
		one.Kind = model.KindRSS
		if u, err := url.Parse(feed.URL); err == nil {
			one.Domain = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		}
	}
	return one
}

// sizes is how many feeds one poll reads and how long a feed rests, from the
// source or else the pool's own defaults.
func (f *Fetcher) sizes(src model.Source) (int, time.Duration) {
	batch, cycle := src.Batch, time.Duration(0)
	if d, err := time.ParseDuration(src.Cycle); err == nil {
		cycle = d
	}
	switch src.Pool {
	case model.PoolSEC:
		if batch <= 0 {
			batch = DefaultBatchSEC
		}
		if cycle <= 0 {
			cycle = DefaultCycleSEC
		}
	default:
		if batch <= 0 {
			batch = DefaultBatchSite
		}
		if cycle <= 0 {
			cycle = DefaultCycleSite
		}
	}
	return batch, cycle
}

// around is d with a little random spread.
func (f *Fetcher) around(d time.Duration) time.Duration {
	f.mu.Lock()
	r := f.cfg.Rand()
	f.mu.Unlock()
	return time.Duration(float64(d) * (1 - jitter + 2*jitter*r))
}

// rest is how long a feed that has failed n times in a row is left alone: a
// cycle, doubling with each failure, and a week for a feed that is gone.
func (f *Fetcher) rest(n int, err error, cycle time.Duration) time.Duration {
	var he *httpx.Error
	if errors.As(err, &he) && he.Class == httpx.Permanent {
		return f.around(maxRest)
	}
	d := cycle
	if errors.As(err, &he) && he.Class == httpx.Blocked {
		d = max(d, 24*time.Hour)
	}
	for i := 1; i < n && d < maxRest; i++ {
		d *= 2
	}
	return f.around(min(d, maxRest))
}

// mustStop reports whether err means the host should be left alone, so the rest
// of the batch is not sent to it.
func mustStop(err error) bool {
	var he *httpx.Error
	return errors.As(err, &he) && (he.Class == httpx.Blocked || he.Class == httpx.Throttled)
}
