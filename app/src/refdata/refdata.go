// Package refdata keeps the reference database current: every stock symbol,
// company and its basic data, from the SEC and Nasdaq, in the database
// (package store) where the command line and the interface read it.
//
// Three steps, each safe to repeat:
//
//  1. fetch: package company asks the sources with conditional requests and
//     keeps what it downloaded as a cache of files in the company folder (the
//     bytes, the ETag, the time);
//  2. sync: the parsed content is put in the database, in key order, with a
//     hash of the content. Content that did not change writes nothing, content
//     that did is logged as a snapshot and as the rows that came, went or
//     changed;
//  3. build: the universe (package universe) is made from the database alone.
//
// The database is a function of the content of the sources: the same answers
// from the SEC and Nasdaq give the same rows and the same hashes, whenever and
// however often they were asked, and the universe built from it has the same
// fingerprint.
package refdata

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/stockfeeds"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// Settings say where the sources are and how often to ask. Empty URLs mean the
// real sources.
type Settings struct {
	// Dir is the folder of downloaded files (data/company).
	Dir string

	MasterURL, NasdaqURL, OtherURL, SubmissionsURL, SitesURL string
	// MaxAge is how old the ticker lists may get before they are asked again.
	MaxAge time.Duration
	// Listing says whether the Nasdaq directory is wanted.
	Listing bool
	// PerCycle is how many company records one cycle fetches at most (the SEC
	// allows far more; this is the pace, not the limit). Zero fetches none.
	PerCycle int
	// SitesPerCycle is how many tickers one cycle looks up the official website
	// of (in Wikidata, forty to a request). Zero looks up none.
	SitesPerCycle int
	// IndustryMaxAge is how old a company record may get; zero means
	// company.DefaultIndustryAge.
	IndustryMaxAge time.Duration
	// QuotesPerCycle is how many stocks (for a source of whole market days, how
	// many days) one cycle brings the daily candles of up to date. Zero none.
	QuotesPerCycle int
	// FeedSearch is how many company websites one cycle reads to find the
	// company's own news feed. Zero reads none; the SEC filing record of every
	// stock is written down either way when Refresher.Feeds is set.
	FeedSearch int
}

// Refresher brings the database up to date.
type Refresher struct {
	Ref    store.Reference
	Client httpx.Client
	Set    Settings
	// Now is the clock; time.Now if nil.
	Now func() time.Time
	// Interesting returns the tickers whose company records matter most: the
	// ones you follow, list, tag, or that are in the news. They are fetched
	// before the rest. May be nil.
	Interesting func(ctx context.Context) []string
	// Feeds, when set, keeps the feeds of each stock: after the sync it writes
	// down the SEC filing record of every stock with a CIK and searches the
	// websites of Settings.FeedSearch companies for theirs.
	Feeds *stockfeeds.Keeper
	// Quotes, when set, keeps the daily candles of stocks.
	Quotes *marketdata.Keeper
	// Logf receives one line per notable thing; may be nil.
	Logf func(format string, args ...any)
}

// Report says what one cycle did.
type Report struct {
	Tickers, Listing, Companies store.RefDiff
	Enriched                    company.EnrichResult
	// Sites is what the look-up of official websites did.
	Sites company.SiteResult
	// Quotes is what the refresh of daily candles did.
	Quotes marketdata.Report
	// FeedsSeeded is how many SEC filing records of stocks are new; Feeds is
	// what the search of company websites did.
	FeedsSeeded int
	Feeds       stockfeeds.Report
	// Problems are the steps that failed; the others still ran.
	Problems []error
}

// Err joins the problems.
func (r Report) Err() error { return errors.Join(r.Problems...) }

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Refresher) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// Cycle runs the three steps once: ask the sources that are due, sync, and
// fetch up to Settings.PerCycle company records. force asks the ticker lists
// even if they are young. A step that fails is reported and the others go on,
// so a source that is down never keeps the rest from refreshing.
func (r *Refresher) Cycle(ctx context.Context, force bool) Report {
	var rep Report
	failed := map[string]error{}
	problem := func(source string, err error) {
		rep.Problems = append(rep.Problems, fmt.Errorf("%s: %w", source, err))
		failed[source] = err
	}

	_, err := company.Refresh(ctx, r.Client, r.Set.Dir, company.Options{
		URL: r.Set.MasterURL, MaxAge: r.Set.MaxAge, Force: force, Now: r.Now,
	})
	if err != nil {
		problem(store.SourceTickers, err)
	}
	if r.Set.Listing {
		if _, err := company.RefreshListing(ctx, r.Client, r.Set.Dir, company.ListingOptions{
			NasdaqURL: r.Set.NasdaqURL, OtherURL: r.Set.OtherURL, MaxAge: r.Set.MaxAge, Force: force, Now: r.Now,
		}); err != nil {
			problem(store.SourceNasdaq, err)
		}
	}
	if ctx.Err() != nil {
		return rep
	}

	if r.Set.PerCycle > 0 {
		res, err := r.enrich(ctx, r.Set.PerCycle)
		rep.Enriched = res
		if err != nil && ctx.Err() == nil {
			problem(store.SourceCompany, err)
		}
	}

	if r.Set.SitesPerCycle > 0 && ctx.Err() == nil {
		res, err := r.enrichSites(ctx, r.Set.SitesPerCycle)
		rep.Sites = res
		if err != nil && ctx.Err() == nil {
			problem(store.SourceWebsite, err)
		}
	}

	// Sync even when a fetch failed or was interrupted: whatever reached the
	// cache files belongs in the database.
	sctx := context.WithoutCancel(ctx)
	rep.Tickers, rep.Listing, rep.Companies, err = r.Sync(sctx)
	if err != nil {
		rep.Problems = append(rep.Problems, err)
	}
	// The candles come after the sync: on a first cycle the tickers they are for
	// are in the database only now.
	if r.Quotes != nil && r.Set.QuotesPerCycle > 0 && ctx.Err() == nil {
		qrep, err := r.refreshQuotes(ctx)
		rep.Quotes = qrep
		if err != nil && ctx.Err() == nil {
			problem("quotes", err)
		}
	}

	// After the sync, which clears the error of a source whose content is
	// confirmed: a failed refresh is still the latest word about it.
	for _, source := range slices.Sorted(maps.Keys(failed)) {
		_ = r.Ref.RefConfirm(sctx, source, "", "", r.now(), failed[source].Error())
	}
	if r.Feeds != nil && ctx.Err() == nil {
		r.keepFeeds(ctx, &rep, problem)
	}
	if rep.Tickers.Added+rep.Tickers.Removed+rep.Tickers.Changed > 0 {
		r.logf("reference: tickers %+d -%d ~%d", rep.Tickers.Added, rep.Tickers.Removed, rep.Tickers.Changed)
	}
	if rep.Companies.Added+rep.Companies.Changed > 0 {
		r.logf("reference: companies %+d ~%d", rep.Companies.Added, rep.Companies.Changed)
	}
	return rep
}

// keepFeeds writes down the feed of each stock and looks for more.
func (r *Refresher) keepFeeds(ctx context.Context, rep *Report, problem func(string, error)) {
	stocks, err := stockfeeds.Stocks(ctx, r.Ref)
	if err != nil {
		problem("stock feeds", err)
		return
	}
	if rep.FeedsSeeded, err = r.Feeds.Seed(ctx, stocks); err != nil {
		problem("stock feeds", err)
		return
	}
	if rep.FeedsSeeded > 0 {
		r.logf("stock feeds: %d SEC filing records", rep.FeedsSeeded)
	}
	if r.Set.FeedSearch <= 0 {
		return
	}
	var first []string
	if r.Interesting != nil {
		first = r.Interesting(ctx)
	}
	if rep.Feeds, err = r.Feeds.Discover(ctx, stocks, first, r.Set.FeedSearch); err != nil && ctx.Err() == nil {
		problem("stock feeds", err)
	}
}

// enrich fetches up to budget company records: first those of interesting
// tickers, then of everything else, each group in CIK order, so the work done
// does not depend on how the lists happen to be ordered.
func (r *Refresher) enrich(ctx context.Context, budget int) (company.EnrichResult, error) {
	master, err := company.Load(r.Set.Dir)
	if err != nil || master.Empty() {
		return company.EnrichResult{}, err
	}
	byTicker := map[string]string{}
	var all []string
	for _, e := range master.Entities {
		byTicker[universe.Ticker(e.Ticker)] = e.CIK
		all = append(all, e.CIK)
	}
	var first []string
	if r.Interesting != nil {
		for _, t := range r.Interesting(ctx) {
			if cik := byTicker[universe.Ticker(t)]; cik != "" {
				first = append(first, cik)
			}
		}
	}
	opts := company.EnrichOptions{URL: r.Set.SubmissionsURL, MaxAge: r.Set.IndustryMaxAge, Now: r.Now}
	var total company.EnrichResult
	for _, group := range [][]string{first, all} {
		left := budget - total.Fetched - total.Missing
		if left <= 0 {
			break
		}
		opts.Max = left
		res, err := company.Enrich(ctx, r.Client, r.Set.Dir, group, opts)
		total.Fetched += res.Fetched
		total.Missing += res.Missing
		total.Skipped += res.Skipped
		total.Failed = append(total.Failed, res.Failed...)
		total.Left = res.Left
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// refreshQuotes brings the candles of the listed stocks and ETFs up to date.
func (r *Refresher) refreshQuotes(ctx context.Context) (marketdata.Report, error) {
	tickers, err := QuoteTickers(ctx, r.Ref)
	if err != nil {
		return marketdata.Report{}, err
	}
	var first []string
	if r.Interesting != nil {
		first = r.Interesting(ctx)
	}
	return r.Quotes.Refresh(ctx, tickers, first, r.Set.QuotesPerCycle)
}

// QuoteTickers are the tickers worth a candle: every SEC ticker and every
// security of the Nasdaq directory except test symbols.
func QuoteTickers(ctx context.Context, ref store.Reference) ([]string, error) {
	ts, err := ref.RefTickers(ctx)
	if err != nil {
		return nil, err
	}
	ls, err := ref.RefListings(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t = universe.Ticker(t); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range ts {
		add(t.Ticker)
	}
	for _, l := range ls {
		if !l.Test {
			add(l.Symbol)
		}
	}
	return marketdata.Tidy(out), nil
}

// enrichSites looks up the websites of up to budget tickers: those of
// interesting tickers first, then the rest, each group in ticker order.
func (r *Refresher) enrichSites(ctx context.Context, budget int) (company.SiteResult, error) {
	master, err := company.Load(r.Set.Dir)
	if err != nil || master.Empty() {
		return company.SiteResult{}, err
	}
	names := map[string]string{}
	var all []company.SiteTarget
	for _, e := range master.Entities {
		t := universe.Ticker(e.Ticker)
		if _, dup := names[t]; !dup {
			names[t] = e.Name
			all = append(all, company.SiteTarget{Ticker: t, Name: e.Name})
		}
	}
	var first []company.SiteTarget
	if r.Interesting != nil {
		for _, t := range r.Interesting(ctx) {
			t = universe.Ticker(t)
			if name, ok := names[t]; ok {
				first = append(first, company.SiteTarget{Ticker: t, Name: name})
			}
		}
	}
	opts := company.SiteOptions{URL: r.Set.SitesURL, Now: r.Now}
	var total company.SiteResult
	for _, group := range [][]company.SiteTarget{first, all} {
		left := budget - total.Found - total.Missing
		if left <= 0 {
			break
		}
		opts.Max = left
		res, err := company.EnrichWebsites(ctx, r.Client, r.Set.Dir, group, opts)
		total.Found += res.Found
		total.Missing += res.Missing
		total.Skipped += res.Skipped
		total.Failed = append(total.Failed, res.Failed...)
		total.Left = res.Left
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// Sync puts what is in the cache files into the database. It is the only
// place the files are read for the database, and it is safe to call at any
// time: unchanged content writes nothing.
func (r *Refresher) Sync(ctx context.Context) (tickers, listing, companies store.RefDiff, err error) {
	at := r.now()
	var errs []error
	master, merr := company.Load(r.Set.Dir)
	switch {
	case merr != nil:
		errs = append(errs, merr)
	case !master.Empty():
		src := store.RefSource{Name: store.SourceTickers}
		tickers, merr = r.Ref.ReplaceTickers(ctx, src, TickerRows(master), at)
		if merr != nil {
			errs = append(errs, merr)
		}
	}
	if r.Set.Listing {
		l, lerr := company.LoadListing(r.Set.Dir)
		switch {
		case lerr != nil:
			errs = append(errs, lerr)
		case l.Len() > 0:
			listing, lerr = r.Ref.ReplaceListing(ctx, store.RefSource{Name: store.SourceNasdaq}, ListingRows(l), at)
			if lerr != nil {
				errs = append(errs, lerr)
			}
		}
	}
	ind, ierr := company.LoadIndustry(r.Set.Dir)
	switch {
	case ierr != nil:
		errs = append(errs, ierr)
	case len(ind) > 0:
		companies, ierr = r.Ref.PutCompanies(ctx, CompanyRows(ind), at)
		if ierr != nil {
			errs = append(errs, ierr)
		}
	}
	sites, serr := company.LoadWebsites(r.Set.Dir)
	switch {
	case serr != nil:
		errs = append(errs, serr)
	case len(sites) > 0:
		if _, serr = r.Ref.PutWebsites(ctx, WebsiteRows(sites), at); serr != nil {
			errs = append(errs, serr)
		}
	}
	return tickers, listing, companies, errors.Join(errs...)
}

// Run runs a cycle at once and then every interval until ctx ends. A cycle
// never starts while the one before it is running. The interval is fixed, with
// no random jitter: the work of each cycle is the same for the same state.
func (r *Refresher) Run(ctx context.Context, every time.Duration) {
	r.run(ctx, every, time.After)
}

func (r *Refresher) run(ctx context.Context, every time.Duration, after func(time.Duration) <-chan time.Time) {
	for {
		rep := r.Cycle(ctx, false)
		if err := rep.Err(); err != nil && ctx.Err() == nil {
			r.logf("reference: refresh: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-after(every):
		}
	}
}

// TickerRows are the rows of the SEC list.
func TickerRows(m company.Master) []store.RefTicker {
	rows := make([]store.RefTicker, 0, len(m.Entities))
	for _, e := range m.Entities {
		rows = append(rows, store.RefTicker{Ticker: e.Ticker, Name: e.Name, CIK: e.CIK, Exchange: e.Exchange})
	}
	return rows
}

// ListingRows are the rows of the Nasdaq directory.
func ListingRows(l company.Listing) []store.RefListing {
	var rows []store.RefListing
	for _, s := range l.All() {
		rows = append(rows, store.RefListing{Symbol: s.Symbol, Name: s.Name, Venue: s.Venue,
			Market: s.Market, Status: s.Status, ETF: s.ETF, Test: s.Test})
	}
	return rows
}

// CompanyRows are the rows of the company records.
func CompanyRows(c company.IndustryCache) []store.RefCompany {
	rows := make([]store.RefCompany, 0, len(c))
	for _, cik := range c.CIKs() {
		i := c[cik]
		rows = append(rows, store.RefCompany{CIK: i.CIK, Name: i.Name, SIC: i.SIC, Description: i.Description,
			EntityType: i.EntityType, Category: i.Category, State: i.State, FiscalYear: i.FiscalYear,
			Website: i.Website, Former: slices.Clone(i.Former), Exchanges: slices.Clone(i.Exchanges), City: i.City,
			Region: i.Region, Phone: i.Phone, EIN: i.EIN, LEI: i.LEI, Investor: i.Investor,
			Missing: i.Missing, FetchedAt: i.FetchedAt})
	}
	return rows
}

// WebsiteRows are the rows of the website cache.
func WebsiteRows(c company.WebsiteCache) []store.RefWebsite {
	rows := make([]store.RefWebsite, 0, len(c))
	for _, t := range c.Tickers() {
		w := c[t]
		rows = append(rows, store.RefWebsite{Ticker: t, URL: w.URL, Label: w.Label, FetchedAt: w.FetchedAt})
	}
	return rows
}

// Inputs reads the three tables and returns the sources of a universe. No
// file is read: the universe is a function of the database.
func Inputs(ctx context.Context, ref store.Reference) (universe.Inputs, error) {
	var in universe.Inputs
	ts, err := ref.RefTickers(ctx)
	if err != nil {
		return in, err
	}
	for _, t := range ts {
		in.Master.Entities = append(in.Master.Entities, model.Entity{Ticker: t.Ticker, Name: t.Name, CIK: t.CIK, Exchange: t.Exchange})
	}
	ls, err := ref.RefListings(ctx)
	if err != nil {
		return in, err
	}
	secs := make([]company.Security, 0, len(ls))
	for _, l := range ls {
		secs = append(secs, company.Security{Symbol: l.Symbol, Name: l.Name, Venue: l.Venue,
			Market: l.Market, Status: l.Status, ETF: l.ETF, Test: l.Test})
	}
	in.Listing = company.NewListing(secs, time.Time{})
	cs, err := ref.RefCompanies(ctx)
	if err != nil {
		return in, err
	}
	in.Industry = company.IndustryCache{}
	for _, c := range cs {
		in.Industry[c.CIK] = company.Industry{CIK: c.CIK, Name: c.Name, SIC: c.SIC, Description: c.Description,
			EntityType: c.EntityType, Category: c.Category, State: c.State, FiscalYear: c.FiscalYear,
			Website: c.Website, Former: slices.Clone(c.Former), Exchanges: slices.Clone(c.Exchanges), City: c.City,
			Region: c.Region, Phone: c.Phone, EIN: c.EIN, LEI: c.LEI, Investor: c.Investor,
			Missing: c.Missing, FetchedAt: c.FetchedAt}
	}
	ws, err := ref.RefWebsites(ctx)
	if err != nil {
		return in, err
	}
	in.Websites = make(map[string]string, len(ws))
	for _, w := range ws {
		if w.URL != "" {
			in.Websites[w.Ticker] = w.URL
		}
	}
	return in, nil
}

// Build makes the universe from the database, the user's layer file and the
// extra tickers. A damaged layer file is reported with a universe that lacks
// it, as universe.Load does.
func Build(ctx context.Context, ref store.Reference, layerPath string, sectors map[string]string, known []string) (*universe.Universe, error) {
	in, err := Inputs(ctx, ref)
	if err != nil {
		return nil, err
	}
	in.Sectors, in.Known = sectors, known
	var lerr error
	if layerPath != "" {
		if in.Layer, lerr = universe.LoadLayer(layerPath); lerr != nil {
			in.Layer = universe.Layer{}
		}
	}
	return universe.New(in), lerr
}

// Stamp is a short string that changes when the reference data does. A reader
// that holds a universe asks for it to know when to build a new one.
func Stamp(ctx context.Context, ref store.Reference) (string, error) {
	srcs, err := ref.RefSources(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, s := range srcs { // by name
		fmt.Fprintf(&b, "%s=%s;", s.Name, s.Hash)
	}
	return b.String(), nil
}
