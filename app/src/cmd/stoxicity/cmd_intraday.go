package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/research"
)

const (
	defaultIntradayRequests = 25
	// intradayPlanLines is how many requests -plan lists before it only counts.
	intradayPlanLines = 100
	// intradayQuotaKey is the request budget the provider's host is held to.
	intradayQuotaKey = "api.polygon.io"
)

// cmdIntraday keeps the intraday bars that a study entering intraday reads.
func cmdIntraday(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: fetch")
	}
	if args[0] != "fetch" {
		return usageErrorf("unknown subcommand %q (fetch)", args[0])
	}
	return a.intradayFetch(ctx, args[1:])
}

// splitList reads a comma-separated list, without blanks and repeats, upper
// case when upper is set.
func splitList(s string, upper bool) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			if upper {
				part = strings.ToUpper(part)
			}
			out = append(out, part)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// intradayFetch asks the provider for the intraday bars that are wanted and not
// yet held. What is wanted is the tickers of -symbols on every weekday from -from
// to -to, and the tickers of the stored events of -types, available in that
// range, on the days they may be entered. The request budget is the quota the
// HTTP client holds for the provider; -plan prints the requests and makes none.
func (a *app) intradayFetch(ctx context.Context, args []string) error {
	fs := newFlags("intraday fetch")
	from := fs.String("from", "", "first day, YYYY-MM-DD")
	to := fs.String("to", "", "last day, YYYY-MM-DD")
	symbols := fs.String("symbols", "", "tickers to fetch on every weekday of the range, such as the benchmarks, comma-separated")
	types := fs.String("types", "", "event types whose tickers are fetched on the days their events may be entered, comma-separated")
	interval := fs.Int("interval", 60, "bar length in seconds, a whole number of minutes up to 3600")
	maxN := fs.Int("max", defaultIntradayRequests, "most requests to make (one ticker over a run of days each); 0 means no limit")
	refetch := fs.Bool("refetch", false, "ask again for days already asked for")
	pace := fs.Duration("pace", 0, "wait between two requests, for a plan that allows few requests a minute")
	plan := fs.Bool("plan", false, "print what would be requested and make no request")
	yes := fs.Bool("yes", false, "make the requests: they can be many, so say -plan first")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	loc := a.cfg.Location()
	first, err := parseDay(*from, loc)
	if err != nil {
		return usageErrorf("-from: %v", err)
	}
	last, err := parseDay(*to, loc)
	if err != nil {
		return usageErrorf("-to: %v", err)
	}
	switch {
	case last.Before(first):
		return usageErrorf("-to is before -from")
	case *symbols == "" && *types == "":
		return usageErrorf("name what to fetch: -symbols, -types, or both")
	case *interval < 60 || *interval > 3600 || *interval%60 != 0:
		return usageErrorf("-interval must be a whole number of minutes in seconds, between 60 and 3600")
	case *maxN < 0:
		return usageErrorf("-max must not be negative")
	case *pace < 0:
		return usageErrorf("-pace must not be negative")
	case !*plan && !*yes:
		return usageErrorf("this can be many requests: run with -plan to see them, then with -yes to make them")
	}
	if !*plan {
		if a.cfg.Company.Quotes == "off" {
			return fmt.Errorf("quotes are off: set company.quotes to polygon")
		}
		if a.cfg.Company.PolygonKey == "" {
			return fmt.Errorf("intraday bars come from Polygon, which needs a key of your own: set STOXICITY_POLYGON_KEY or MASSIVE_API_KEY (in .env.local)")
		}
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	step := time.Duration(*interval) * time.Second
	wanted := map[string][]string{}
	if syms := splitList(*symbols, true); len(syms) > 0 {
		days, err := marketdata.IntradayDays(first.Format(time.DateOnly), last.Format(time.DateOnly))
		if err != nil {
			return err
		}
		for _, t := range syms {
			wanted[t] = append(wanted[t], days...)
		}
	}
	if kinds := splitList(*types, false); len(kinds) > 0 {
		need, err := research.IntradayNeeds(ctx, db, kinds, first.UTC(), last.UTC().AddDate(0, 0, 1).Add(-time.Millisecond))
		if err != nil {
			return err
		}
		for t, days := range need {
			wanted[t] = append(wanted[t], days...)
		}
	}
	k := &marketdata.IntradayKeeper{Store: db, Now: a.now, Pace: *pace}
	p, err := k.Plan(ctx, wanted, step, *refetch)
	if err != nil {
		return err
	}
	budget := *maxN
	if budget == 0 {
		budget = len(p.Requests)
	}
	if *plan {
		a.printIntradayPlan(p, budget, step)
		return nil
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	c := a.cfg.Company
	aggs := ""
	if c.QuotesURL != "" {
		aggs = c.QuotesURL + "aggs/" // an overridden address (a test) holds both
	}
	k.Source = marketdata.PolygonIntraday{Client: client, Key: c.PolygonKey, URL: aggs}
	rep, err := k.Fetch(ctx, p.Requests, step, budget)
	fmt.Fprintf(a.stdout, "intraday bars (%s, %ds)  %s\n", k.Source.Name(), *interval, rep)
	return err
}

// printIntradayPlan says what a fetch would request, and under which request
// budget.
func (a *app) printIntradayPlan(p marketdata.IntradayPlan, budget int, step time.Duration) {
	tickers := map[string]bool{}
	for _, r := range p.Requests {
		tickers[r.Ticker] = true
	}
	fmt.Fprintf(a.stdout, "intraday plan  %s bars from polygon, regular session only, raw prices\n", step)
	fmt.Fprintf(a.stdout, "tickers        %d\n", len(tickers))
	fmt.Fprintf(a.stdout, "days           %d to ask for, %d already asked for\n", p.Days, p.Covered)
	fmt.Fprintf(a.stdout, "requests       %d, of which %d would be made now\n", len(p.Requests), min(budget, len(p.Requests)))
	if q, ok := a.cfg.Fetch.Quotas[intradayQuotaKey]; ok {
		fmt.Fprintf(a.stdout, "request budget %d per %s (fetch.quotas.%s); a request over it stops the run\n", q.Requests, q.Period, intradayQuotaKey)
	} else {
		fmt.Fprintf(a.stdout, "request budget none configured for %s (fetch.quotas); the plan's own limit decides\n", intradayQuotaKey)
	}
	for i, r := range p.Requests {
		if i >= intradayPlanLines {
			fmt.Fprintf(a.stdout, "... and %d more\n", len(p.Requests)-i)
			break
		}
		fmt.Fprintf(a.stdout, "%-8s %s to %s  %d days\n", r.Ticker, r.From, r.To, len(r.Days))
	}
}
