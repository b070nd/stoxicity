// Package workspace opens the configuration, the stores and one tenant for the
// interactive programs, and implements tui.Backend over them. The stoxicity
// command does the same start-up in package main; this copy exists so the TUI
// binary does not depend on a main package.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/envfile"
	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/insider"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/reader"
	"github.com/b070nd/stoxicity/app/src/refdata"
	"github.com/b070nd/stoxicity/app/src/sources"
	"github.com/b070nd/stoxicity/app/src/status"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/tenant"
	"github.com/b070nd/stoxicity/app/src/tui"
	"github.com/b070nd/stoxicity/app/src/universe"
	"github.com/b070nd/stoxicity/app/src/valuation"
	"github.com/b070nd/stoxicity/app/src/watch"
)

// Options says where to start and what to open.
type Options struct {
	Root   string // configuration folder; empty means search upwards from Cwd
	Cwd    string // where to search from; empty means the working directory
	Tenant string // empty means $STOXICITY_TENANT, then the configured default
	Watch  string // empty means the tenant's default_watch
	Now    func() time.Time
}

// Workspace is an open tenant. Close it when done.
type Workspace struct {
	cfg     config.Config
	reg     sources.Registry
	db      *store.DB
	ts      store.TenantStore
	tenant  tenant.Tenant
	read    *reader.Reader
	watches watch.FSStore
	now     func() time.Time
	zone    *time.Location
	watchID string

	// The universe is replaced as a whole when a tag or a list changes, or when
	// the reference database does (a "stoxicity run" refreshes it in the
	// background).
	src     universe.Sources
	mu      sync.Mutex
	univ    *universe.Universe
	stamp   string    // the reference database as the universe was built from it
	checked time.Time // when the stamp was last compared
	byID    map[string]watch.Watch
	baseDir string

	// The newest candles, read again once they are older than quotesEvery.
	quotes   map[string]tui.Quote
	quotesAt time.Time

	sampled map[string]bool // articles of the label queue that were drawn at random

	numbers   map[string]map[string]float64
	numbersAt time.Time
	sums      map[string]fundamentals.Summary
	sumsAt    time.Time
	vals      valuation.Set
	why       map[string]map[string]string
	// The insiders' purchases and sales of common stock of the last quarter, summed by company.
	insiders map[string]insider.Summary
}

var _ tui.Backend = (*Workspace)(nil)

// Open reads the configuration and opens the tenant's data. It does not fetch
// anything and does not write.
func Open(opts Options) (*Workspace, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	root := opts.Root
	if root == "" {
		if root = findUp(cwd, hasConfig); root == "" {
			root = cwd
		}
	}
	cfgDir := config.Dir(root)
	envDir := findUp(cwd, hasEnvFile)
	base := envDir
	if base == "" {
		base = root
	}

	var allow []string
	if reg, err := sources.Load(cfgDir); err == nil {
		for _, s := range reg {
			if s.AuthEnv != "" {
				allow = append(allow, s.AuthEnv)
			}
		}
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := envfile.Setup(nil, base, allow, quiet); err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	reg, err := sources.Load(cfgDir)
	if err != nil {
		return nil, err
	}

	tenantsDir := cfg.Tenancy.Dir
	if !filepath.IsAbs(tenantsDir) {
		tenantsDir = filepath.Join(cfgDir, tenantsDir)
	}
	id := tenant.Resolve(opts.Tenant, os.Getenv("STOXICITY_TENANT"), cfg.Tenancy.Default)
	t, err := tenant.FSRegistry{Dir: tenantsDir}.Get(id)
	if err != nil {
		return nil, err
	}

	dataDir := cfg.DataDir
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(base, dataDir)
	}
	db, err := store.Open(dataDir)
	if err != nil {
		return nil, err
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	zone := cfg.Location()
	w := &Workspace{
		cfg: cfg, reg: reg, db: db, ts: db.Tenant(t.ID), tenant: t,
		watches: watch.FSStore{Dir: filepath.Join(tenantsDir, t.ID, "watches")},
		now:     now, zone: zone, byID: map[string]watch.Watch{}, baseDir: base,
	}
	w.read = reader.New(db, t.ID, reg.VisibleTo(t.ID), zone,
		reader.WithNow(now), reader.WithSince(cfg.SinceTime()))
	known := slices.Clone(cfg.Watchlist)
	if ws, err := w.watches.List(); err == nil {
		for _, x := range ws {
			known = append(known, x.Scope.Tickers...)
		}
	}
	sectors := map[string]string{}
	for _, sr := range cfg.Sectors {
		for _, t := range sr.Tickers {
			sectors[universe.Ticker(t)] = sr.Name
		}
	}
	w.src = universe.Sources{
		CompanyDir: filepath.Join(dataDir, "company"),
		LayerPath:  filepath.Join(tenantsDir, t.ID, universe.FileName),
		Sectors:    sectors, Known: known,
	}
	// Data from before the database (the cache files of the company folder) is
	// taken in once; nothing is fetched. A missing reference costs names and
	// facets, and the universe still holds whatever loaded.
	if rows, err := db.RefTickers(context.Background()); err == nil && len(rows) == 0 {
		keeper := &refdata.Refresher{Ref: db, Set: refdata.Settings{Dir: w.src.CompanyDir, Listing: cfg.Company.Listing}, Now: now}
		_, _, _, _ = keeper.Sync(context.Background())
	}
	w.rebuild()
	w.watchID = opts.Watch
	if w.watchID == "" {
		w.watchID = t.DefaultWatch
	}
	return w, nil
}

// Close releases the database.
func (w *Workspace) Close() error { return w.db.Close() }

func (w *Workspace) Tenant() string       { return w.tenant.ID }
func (w *Workspace) Zone() *time.Location { return w.zone }
func (w *Workspace) Threshold() float64   { return w.cfg.Lens.Threshold }
func (w *Workspace) Now() time.Time       { return w.now() }
func (w *Workspace) DefaultWatch() string { return w.watchID }
func (w *Workspace) Watchlist() []string  { return slices.Clone(w.cfg.Watchlist) }
func (w *Workspace) CompanyName(t string) string {
	p, _ := w.Universe().Get(t)
	return p.Name
}

// stampEvery is how often Universe looks at the reference database for a
// change; asking costs one small query.
const stampEvery = 15 * time.Second

// Universe is the current snapshot of every known company. It is built again
// when the reference database has changed since (a refresh by "stoxicity run"
// while the interface is open).
func (w *Workspace) Universe() *universe.Universe {
	w.mu.Lock()
	defer w.mu.Unlock()
	if now := w.now(); now.Sub(w.checked) >= stampEvery {
		w.checked = now
		if st, err := refdata.Stamp(context.Background(), w.db); err == nil && st != w.stamp {
			w.rebuildLocked()
		}
	}
	return w.univ
}

// rebuild makes the universe from the database; w.mu need not be held yet.
func (w *Workspace) rebuild() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rebuildLocked()
}

func (w *Workspace) rebuildLocked() {
	ctx := context.Background()
	st, _ := refdata.Stamp(ctx, w.db)
	if u, _ := refdata.Build(ctx, w.db, w.src.LayerPath, w.src.Sectors, w.src.Known); u != nil {
		w.univ, w.stamp = u, st
	}
}

// quotesEvery is how often Quotes reads the candles again; a refresh of
// "stoxicity run" writes them in the background.
const quotesEvery = 30 * time.Second

// Quotes are the newest daily candle of every stock that has one, with the
// close before it. Empty when no price source has stored anything.
func (w *Workspace) Quotes() map[string]tui.Quote {
	w.mu.Lock()
	defer w.mu.Unlock()
	if now := w.now(); w.quotes != nil && now.Sub(w.quotesAt) < quotesEvery {
		return w.quotes
	}
	w.quotesAt = w.now()
	latest, err := w.db.LatestCandles(context.Background())
	if err != nil {
		w.quotes = map[string]tui.Quote{}
		return w.quotes
	}
	splits, _ := w.db.Splits(context.Background())
	cov, _ := w.db.SplitCoverage(context.Background())
	bySplit := map[string][]marketdata.Split{}
	for _, s := range splits {
		k := marketdata.NormTicker(s.Ticker)
		bySplit[k] = append(bySplit[k], s)
	}
	out := make(map[string]tui.Quote, len(latest))
	for t, c := range latest {
		if len(c) == 0 {
			continue
		}
		q := tui.Quote{Day: c[0].Day, Open: c[0].Open, High: c[0].High, Low: c[0].Low, Close: c[0].Close}
		if len(c) > 1 {
			q.Prev, q.PrevDay = c[1].Close, c[1].Day
			q.Move = marketdata.Compare(c[1], c[0], bySplit[marketdata.NormTicker(t)], cov)
		}
		out[t] = q
	}
	w.quotes = out
	return out
}

// numbersEvery is how often Numbers is made again; the figures of a year change
// weekly, the market value with the price.
const (
	numbersEvery = time.Minute
	sumsEvery    = 10 * time.Minute
)

// Numbers are the figures of each common stock's latest fiscal year and its
// class and issuer market values (package valuation: classcap, issuercap and
// marketcap, which is the issuer's), by name and ticker. Empty until the SEC's
// figures are stored.
func (w *Workspace) Numbers() map[string]map[string]float64 {
	univ := w.Universe() // takes the lock itself
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if w.numbers != nil && now.Sub(w.numbersAt) < numbersEvery {
		return w.numbers
	}
	w.numbersAt = now
	if w.sums == nil || now.Sub(w.sumsAt) >= sumsEvery {
		w.sumsAt = now
		facts, err := w.db.Fundamentals(context.Background())
		if err != nil {
			facts = nil
		}
		w.sums = fundamentals.Summarize(facts)
		w.vals = valuation.Set{}
		if univ != nil {
			if set, err := valuation.Build(context.Background(), w.db, univ.All(), time.Time{}, now); err == nil {
				w.vals = set
			}
		}
		w.insiders = nil
		since := now.AddDate(0, 0, -insider.FigureDays).Format(time.DateOnly)
		if trades, err := w.db.InsiderTrades(context.Background(), since, "", 0); err == nil {
			w.insiders = insider.Summarize(trades, now)
		}
	}
	if univ == nil {
		w.numbers = map[string]map[string]float64{}
		return w.numbers
	}
	w.numbers = fundamentals.Figures(w.sums, univ.All())
	nums, why := w.vals.Figures(univ.All())
	for name, byTicker := range nums {
		w.numbers[name] = byTicker
	}
	w.why = why
	for name, byTicker := range insider.Figures(w.insiders, univ.All()) {
		w.numbers[name] = byTicker
	}
	return w.numbers
}

// Why says, by figure name and ticker, why a market value is missing.
func (w *Workspace) Why() map[string]map[string]string {
	w.Numbers()
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.why
}

// Insiders lists the stored insider trades of a ticker's company over the last
// year, newest first. Empty when there are none or the ticker has no SEC number.
func (w *Workspace) Insiders(ticker string) []model.InsiderTrade {
	p, ok := w.Universe().Get(ticker)
	if !ok || p.CIK == "" {
		return nil
	}
	since := w.now().AddDate(-1, 0, 0).Format(time.DateOnly)
	list, err := w.db.InsiderTrades(context.Background(), since, p.CIK, 0)
	if err != nil {
		return nil
	}
	return list
}

// StockFeeds lists the feeds recorded for a ticker, the SEC record first.
func (w *Workspace) StockFeeds(ticker string) []tui.FeedInfo {
	list, err := w.db.StockFeedsOf(context.Background(), universe.Ticker(ticker))
	if err != nil {
		return nil
	}
	out := make([]tui.FeedInfo, len(list))
	for i, f := range list {
		out[i] = tui.FeedInfo{Kind: f.Kind, Title: f.Title, URL: f.URL, LastOK: f.LastOK,
			LastNew: f.LastNew, Failures: f.Failures, LastError: f.LastError}
	}
	return out
}

// Following lists the names of the tenant's watches whose scope names the
// ticker.
func (w *Workspace) Following(ticker string) []string {
	list, err := w.watches.List()
	if err != nil {
		return nil
	}
	t := universe.Ticker(ticker)
	var out []string
	for _, x := range list {
		if slices.ContainsFunc(x.Scope.Tickers, func(s string) bool { return universe.Ticker(s) == t }) {
			name := x.Name
			if name == "" {
				name = x.ID
			}
			out = append(out, name)
		}
	}
	return out
}

// Tag puts a tag on a stock or takes it off, and rebuilds the universe.
func (w *Workspace) Tag(_ context.Context, ticker, tag string, add bool) error {
	return w.edit(func(l *universe.Layer) (bool, error) {
		if add {
			return l.AddTag(ticker, tag)
		}
		return l.RemoveTag(ticker, tag), nil
	})
}

// SetListMember puts a stock into a list or takes it out, and rebuilds the
// universe.
func (w *Workspace) SetListMember(_ context.Context, listID, ticker string, add bool) error {
	return w.edit(func(l *universe.Layer) (bool, error) { return l.SetMember(listID, ticker, add) })
}

// edit changes the tenant's stocks.yaml and loads the universe again. The
// file is read fresh each time, so a change made by the command line meanwhile
// is not lost.
func (w *Workspace) edit(change func(*universe.Layer) (bool, error)) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	layer, err := universe.LoadLayer(w.src.LayerPath)
	if err != nil {
		return err
	}
	changed, err := change(&layer)
	if err != nil || !changed {
		return err
	}
	if err := layer.Save(w.src.LayerPath); err != nil {
		return err
	}
	w.rebuildLocked()
	return nil
}

// TopicNames are the topics the configuration defines.
func (w *Workspace) TopicNames() []string {
	out := make([]string, 0, len(w.cfg.Topics))
	for _, t := range w.cfg.Topics {
		out = append(out, t.Name)
	}
	return out
}

// Watches lists the tenant's watches with their unread counts.
func (w *Workspace) Watches(ctx context.Context) ([]tui.WatchInfo, error) {
	list, err := w.watches.List()
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	clear(w.byID)
	for _, x := range list {
		w.byID[x.ID] = x
	}
	w.mu.Unlock()
	out := make([]tui.WatchInfo, 0, len(list))
	for _, x := range list {
		n, err := w.ts.Unread(ctx, x.ID)
		if err != nil {
			return nil, err
		}
		name := x.Name
		if name == "" {
			name = x.ID
		}
		out = append(out, tui.WatchInfo{ID: x.ID, Name: name, Unread: n})
	}
	return out, nil
}

// watchFor returns the watch with the ID. An empty ID means no watch.
func (w *Workspace) watchFor(id string) (watch.Watch, bool, error) {
	if id == "" {
		return watch.Watch{}, false, nil
	}
	w.mu.Lock()
	x, ok := w.byID[id]
	w.mu.Unlock()
	if ok {
		return x, true, nil
	}
	x, err := w.watches.Get(id)
	if err != nil {
		return watch.Watch{}, false, err
	}
	w.mu.Lock()
	w.byID[id] = x
	w.mu.Unlock()
	return x, true, nil
}

// matcher builds the predicate of a watch, or nil for all news.
func (w *Workspace) matcher(id string) (func(model.Article) bool, error) {
	x, ok, err := w.watchFor(id)
	if err != nil || !ok {
		return nil, err
	}
	opts := watch.Options{DefaultThreshold: w.cfg.Lens.Threshold}
	if x.Query != "" {
		q, err := filter.ParseWith(x.Query, w.Universe().FilterEnv())
		if err != nil {
			return nil, fmt.Errorf("watch %s: %w", id, err)
		}
		opts.Query = func(a model.Article) bool { return q.MatchIn(a, w.zone) }
	}
	return func(a model.Article) bool { return x.Match(a, opts) }, nil
}

// Articles returns what the watch matches in [from, to), newest first.
func (w *Workspace) Articles(ctx context.Context, watchID string, from, to time.Time) ([]model.Article, error) {
	match, err := w.matcher(watchID)
	if err != nil {
		return nil, err
	}
	arts, err := w.read.Range(ctx, from, to)
	if err != nil || match == nil {
		return arts, err
	}
	return slices.DeleteFunc(arts, func(a model.Article) bool { return !match(a) }), nil
}

// Search runs a Google-style query over what the watch matches.
func (w *Workspace) Search(ctx context.Context, watchID, query string, from, to time.Time, limit int) ([]model.Article, error) {
	q, err := filter.ParseWith(query, w.Universe().FilterEnv())
	if err != nil {
		return nil, err
	}
	match, err := w.matcher(watchID)
	if err != nil {
		return nil, err
	}
	if match == nil {
		res, err := w.read.Search(ctx, q, from, to, limit)
		return res.Articles, err
	}
	// The watch narrows the result after the search, so ask for more than the
	// limit and cut afterwards.
	res, err := w.read.Search(ctx, q, from, to, max(limit, 1)*4)
	if err != nil {
		return nil, err
	}
	out := res.Articles[:0]
	for _, a := range res.Articles {
		if match(a) {
			out = append(out, a)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Sources reports every configured source with its last known health. The
// health level is graded now, from when the source last worked, so a source
// that nothing polls any more does not stay green.
func (w *Workspace) Sources(ctx context.Context) ([]status.Source, error) {
	now := w.now()
	rows := make([]status.Source, 0, len(w.reg))
	for _, id := range slices.Sorted(maps.Keys(w.reg)) {
		s := w.reg[id]
		_, h, err := w.db.LoadSource(ctx, id)
		if err != nil {
			return nil, err
		}
		rows = append(rows, status.FromStored(s, h, w.pollInterval(s, now), now))
	}
	return rows, nil
}

// pollInterval is how often the scheduler polls src at time now: the same
// order of preference as the scheduler's own (the fast window, the source's
// own interval, the default).
func (w *Workspace) pollInterval(src model.Source, now time.Time) time.Duration {
	if d := w.cfg.IntervalFor(src, now); d > 0 {
		return d
	}
	if d, err := time.ParseDuration(src.Interval); err == nil && d > 0 {
		return d
	}
	return w.cfg.FetchInterval()
}

// Verdicts are the user's keep/drop labels by article ID.
func (w *Workspace) Verdicts(ctx context.Context) (map[string]labels.Verdict, error) {
	all, err := w.ts.Labels().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]labels.Verdict, len(all))
	for _, l := range all {
		out[l.ArticleID] = l.Verdict
	}
	return out, nil
}

// Label records a keep or drop verdict on an article the tenant can see. An
// article the label queue drew at random is recorded as sampled.
func (w *Workspace) Label(ctx context.Context, articleID string, keep bool) error {
	a, err := w.read.Get(ctx, articleID)
	if err != nil {
		return err
	}
	v := labels.Drop
	if keep {
		v = labels.Keep
	}
	w.mu.Lock()
	sampled := w.sampled[a.ID]
	w.mu.Unlock()
	return w.ts.Labels().Put(ctx, labels.Label{
		TenantID: w.tenant.ID, ArticleID: a.ID, Verdict: v,
		At: w.now(), LensVersion: a.Score.LensVersion, Sampled: sampled,
	})
}

// queueDays is how far back the label queue looks for articles to judge.
const queueDays = 30

// LabelQueue chooses the next n articles to judge (see labels.Queue). The day
// seeds the random part, so the queue keeps its order while it is worked on.
func (w *Workspace) LabelQueue(ctx context.Context, n int) ([]labels.Pick, error) {
	now := w.now()
	arts, err := w.read.Range(ctx, now.AddDate(0, 0, -queueDays), now.Add(time.Minute))
	if err != nil {
		return nil, err
	}
	have, err := w.ts.Labels().All(ctx)
	if err != nil {
		return nil, err
	}
	done := make(map[string]bool, len(have))
	for _, l := range have {
		done[l.ArticleID] = true
	}
	picks := labels.Queue(arts, done, w.cfg.Lens.Threshold, n, now.In(w.zone).Format(time.DateOnly))
	w.mu.Lock()
	w.sampled = map[string]bool{}
	for _, p := range picks {
		if p.Sampled {
			w.sampled[p.Article.ID] = true
		}
	}
	w.mu.Unlock()
	return picks, nil
}

// LabelProgress counts the labels and measures the lens against them. Labels of
// articles the tenant can no longer read are left out.
func (w *Workspace) LabelProgress(ctx context.Context) (labels.Progress, error) {
	all, err := w.ts.Labels().All(ctx)
	if err != nil {
		return labels.Progress{}, err
	}
	items := make([]labels.Judged, 0, len(all))
	for _, l := range all {
		a, err := w.read.Get(ctx, l.ArticleID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return labels.Progress{}, err
		}
		items = append(items, labels.Judged{Label: l, Score: a.Score})
	}
	return labels.Measure(items, w.cfg.Lens.Threshold), nil
}

// MarkSeen moves the watch's read marker to now.
func (w *Workspace) MarkSeen(ctx context.Context, watchID string) error {
	if watchID == "" {
		return errors.New("workspace: no watch to mark")
	}
	return w.ts.MarkSeen(ctx, watchID, w.now())
}

func findUp(dir string, match func(string) bool) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if match(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func hasConfig(dir string) bool {
	for _, p := range []string{"app/config/config.yaml", "config/config.yaml", "config.yaml"} {
		if exists(filepath.Join(dir, p)) {
			return true
		}
	}
	return false
}

func hasEnvFile(dir string) bool {
	return exists(filepath.Join(dir, ".env")) || exists(filepath.Join(dir, ".env.local"))
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
