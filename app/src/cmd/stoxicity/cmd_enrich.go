package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/stockfeeds"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/universe"
)

const (
	// defaultFeedSearch is how many company websites enrich reads for a news feed.
	defaultFeedSearch = 50
	// enrichWorkers is how many SEC requests are in flight at once; the HTTP
	// client's limit of ten a second for the SEC applies to all of them.
	enrichWorkers = 4
)

// sitesPerRequest is how many tickers the website look-up covers for each SEC
// request the -max budget allows: one query asks about forty at once.
const sitesPerRequest = 10

// enrichShowLimit is the most tickers named on the command line whose finished
// profile is printed.
const enrichShowLimit = 3

// universeEnrich fills in what the sources know of stocks, in the order of what
// matters most to least (the tickers named, or those you follow and that are in
// the news, then the rest):
//
//  1. the SEC record: industry, exchange, address, identifiers, former names;
//  2. the official website, from Wikidata, which the SEC record does not give;
//  3. the feeds: the SEC filing record, and the news feed the website announces.
//
// Every step is resumable and rate limited: what is young is not asked again,
// so the same call repeated fills the list until nothing is left to ask.
func (a *app) universeEnrich(ctx context.Context, args []string) error {
	fs := newFlags("universe enrich")
	all := fs.Bool("all", false, "every company in the list, without a limit unless -max is given (the SEC record of about 10,000 companies takes some 20 minutes)")
	maxReq := fs.Int("max", defaultEnrichMax, "most SEC requests to make; 0 means no limit")
	force := fs.Bool("force", false, "ask again even for answers that are young")
	sites := fs.Bool("sites", true, "also look up the official website of each stock")
	feedSearch := fs.Int("feeds", defaultFeedSearch, "company websites to read for their news feed; 0 reads none")
	tickers, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	maxSet := false
	fs.Visit(func(f *flag.Flag) { maxSet = maxSet || f.Name == "max" })
	if *all && !maxSet {
		*maxReq = 0
	}
	if *maxReq < 0 || *feedSearch < 0 {
		return usageErrorf("-max and -feeds must not be negative")
	}
	u := a.universe()
	if a.companies().Empty() {
		return fmt.Errorf("no company list yet: run \"stoxicity universe update\"")
	}
	db, err := a.store()
	if err != nil {
		return err
	}

	// What to work on, as groups in the order of work.
	var groups [][]universe.Profile
	var named []string
	switch {
	case len(tickers) > 0:
		var g []universe.Profile
		for _, t := range tickers {
			p, ok := u.Get(t)
			if !ok {
				return fmt.Errorf("%s is not in the company list", strings.ToUpper(t))
			}
			g = append(g, p)
			named = append(named, p.Ticker)
		}
		groups = [][]universe.Profile{g}
	case *all:
		groups = [][]universe.Profile{u.All()}
	default:
		var g []universe.Profile
		for _, t := range interestingTickers(ctx, db, a.knownTickers(), a.layerPath(), a.cfg.Lens.Threshold, a.now()) {
			if p, ok := u.Get(t); ok {
				g = append(g, p)
			}
		}
		groups = [][]universe.Profile{g, u.All()}
	}
	if len(groups[0]) == 0 && len(groups) == 1 {
		fmt.Fprintln(a.stdout, "nothing to look up: follow some stocks (watchlist, lists, tags) or name tickers")
		return nil
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}

	// 1. The SEC record.
	opts := company.EnrichOptions{URL: a.cfg.Company.SubmissionsURL, Force: *force, Now: a.now, Workers: enrichWorkers}
	opts.Progress = func(done, total int) {
		if done%100 == 0 || done == total {
			fmt.Fprintf(a.stderr, "SEC records: %d of %d\n", done, total)
		}
	}
	var sec company.EnrichResult
	var runErr error
	for _, g := range groups {
		opts.Max = 0
		if *maxReq > 0 {
			if opts.Max = *maxReq - sec.Fetched - sec.Missing - len(sec.Failed); opts.Max <= 0 {
				break
			}
		}
		res, err := company.Enrich(ctx, client, a.companyDir(), cikList(g), opts)
		sec.Fetched += res.Fetched
		sec.Skipped += res.Skipped
		sec.Missing += res.Missing
		sec.Left = res.Left
		sec.Failed = append(sec.Failed, res.Failed...)
		if err != nil {
			runErr = err
			break
		}
	}
	fmt.Fprintf(a.stdout, "SEC records   stored %d; %d young enough, %d unknown to the SEC", sec.Fetched, sec.Skipped, sec.Missing)
	if sec.Left > 0 {
		fmt.Fprintf(a.stdout, ", %d left (raise -max)", sec.Left)
	}
	fmt.Fprintln(a.stdout)
	for _, f := range sec.Failed {
		fmt.Fprintf(a.stdout, "failed: %s\n", f)
	}

	// 2. The official websites.
	if *sites && runErr == nil {
		sopts := company.SiteOptions{URL: a.cfg.Company.SitesURL, Force: *force, Now: a.now}
		sopts.Progress = func(done, total int) { fmt.Fprintf(a.stderr, "websites: %d of %d\n", done, total) }
		var res company.SiteResult
		for _, g := range groups {
			sopts.Max = 0
			if *maxReq > 0 {
				if sopts.Max = *maxReq*sitesPerRequest - res.Found - res.Missing; sopts.Max <= 0 {
					break
				}
			}
			r, err := company.EnrichWebsites(ctx, client, a.companyDir(), siteTargets(g), sopts)
			res.Found += r.Found
			res.Missing += r.Missing
			res.Skipped += r.Skipped
			res.Left = r.Left
			res.Failed = append(res.Failed, r.Failed...)
			if err != nil {
				runErr = err
				break
			}
		}
		fmt.Fprintf(a.stdout, "websites      found %d; %d not on Wikidata; %d young enough", res.Found, res.Missing, res.Skipped)
		if res.Left > 0 {
			fmt.Fprintf(a.stdout, ", %d left (raise -max)", res.Left)
		}
		fmt.Fprintln(a.stdout)
		for _, f := range res.Failed {
			fmt.Fprintf(a.stdout, "failed: %s\n", f)
		}
	}
	a.syncReference(context.WithoutCancel(ctx))

	// 3. The feeds.
	if runErr == nil {
		if err := a.enrichFeeds(ctx, db, client, named, groups, *feedSearch); err != nil {
			runErr = err
		}
	}
	for _, line := range a.coverage(ctx, db) {
		fmt.Fprintln(a.stdout, line)
	}
	if len(named) > 0 && len(named) <= enrichShowLimit {
		for _, t := range named {
			fmt.Fprintln(a.stdout)
			if err := a.universeShow(ctx, []string{t}); err != nil {
				return err
			}
		}
	}
	return runErr
}

// enrichFeeds writes down the SEC filing record of every stock and searches the
// websites of up to budget of the chosen ones for their news feed.
func (a *app) enrichFeeds(ctx context.Context, db *store.DB, client httpx.Client, named []string, groups [][]universe.Profile, budget int) error {
	stocks, err := stockfeeds.Stocks(ctx, db)
	if err != nil {
		return err
	}
	k := &stockfeeds.Keeper{Store: db, Client: client, Now: a.now}
	seeded, err := k.Seed(ctx, stocks)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "feeds         %d new SEC filing records", seeded)
	if budget == 0 {
		fmt.Fprintln(a.stdout)
		return nil
	}
	first := named
	if len(first) == 0 {
		for _, p := range groups[0] {
			first = append(first, p.Ticker)
		}
	}
	rep, err := k.Discover(ctx, stocks, first, budget)
	fmt.Fprintf(a.stdout, "; company websites: %s\n", rep)
	return err
}

func cikList(g []universe.Profile) []string {
	var out []string
	for _, p := range g {
		if p.CIK != "" {
			out = append(out, p.CIK)
		}
	}
	return out
}

// siteTargets are the stocks of a group worth looking the website of up: not
// ETFs and test symbols, whose websites are their issuers'.
func siteTargets(g []universe.Profile) []company.SiteTarget {
	var out []company.SiteTarget
	for _, p := range g {
		if p.Kind == universe.KindETF || p.Kind == universe.KindTest || p.Name == "" {
			continue
		}
		out = append(out, company.SiteTarget{Ticker: p.Ticker, Name: p.Name})
	}
	return out
}

// coverage says how much of the stock list is filled in. A stock is a company
// with shares: the Nasdaq directory says so, or the SEC record of a company the
// directory lacks.
func (a *app) coverage(ctx context.Context, db *store.DB) []string {
	u := a.universe()
	var stocks, record, exchange, site, hq, priced int
	candles, _ := db.NewestDays(ctx)
	for _, p := range u.All() {
		if p.Kind != universe.KindStock {
			continue
		}
		stocks++
		if p.SIC != "" || p.EntityType != "" {
			record++
		}
		if p.Exchange != "" {
			exchange++
		}
		if p.Website != "" {
			site++
		}
		if p.HQ != "" {
			hq++
		}
		if candles[p.Ticker] != "" {
			priced++
		}
	}
	if stocks == 0 {
		return nil
	}
	pc := func(n int) string { return fmt.Sprintf("%d%%", (min(n, stocks)*100+stocks/2)/stocks) }
	lines := []string{fmt.Sprintf("coverage      %s stocks: SEC record %s · address %s · exchange %s · website %s",
		thousandsInt(stocks), pc(record), pc(hq), pc(exchange), pc(site))}
	if stats, err := db.StockFeedStats(ctx); err == nil {
		lines[0] += fmt.Sprintf(" · filing feed %s · news feed %s", pc(stats[store.FeedSEC].Stocks), pc(stats[store.FeedSite].Stocks))
	}
	if len(candles) > 0 {
		lines[0] += " · price " + pc(priced)
	}
	return lines
}

func thousandsInt(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
