package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/b070nd/stoxicity/app/src/benzinga"
	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/httpx"
)

const (
	defaultAnalystRequests = 50
	// analystPlanLines is how many requests -plan lists before it only counts.
	analystPlanLines = 100
	// analystQuotaKey is the request budget the provider's host is held to.
	analystQuotaKey = "api.polygon.io"
)

// analystFetchUsage is the description of `eventdb fetch-analyst`.
const analystFetchUsage = `usage: eventdb fetch-analyst -from DAY -to DAY -time-zone ZONE [-symbols A,B] (-plan | -yes) [-max N] [-dry-run]

Reads Benzinga's analyst ratings through Massive (a separate add-on that needs
the same key as the price fetches), one request for each day from -from to -to
(every calendar day; the day is the date Benzinga states) and, with -symbols,
for each of those tickers, and stores them as analyst revisions with the rules
of import-analyst:

  upgrades and downgrades        become upgrade and downgrade, with the ratings
  price target raises, lowers    become target_raised and target_cut, with the
                                 targets as issued (never the adjusted ones)
  a record with both             becomes two revisions
  any other action               (maintains, reiterates, initiates, assumes,
                                 terminates, removes, ...) is left out and
                                 counted by reason

-time-zone is required and has no default: Benzinga's documentation says its
times are UTC, but its own samples fit New York time. Give the zone a few known
events show (UTC, America/New_York, any IANA name). published_at is the date and
time read in that zone. held_at is the time of the fetch, so a record fetched
within 72 hours of its time counts as known and anything older as reconstructed.
Without -symbols every stock is asked for, and tickers the reference data does
not know are rejected and counted.

-plan prints the requests and makes none. -yes makes them, at most -max (0 means
no limit), and stores what each brings. -dry-run, with -yes, fetches and
converts but stores nothing. The rows are stored with source import and the
source_ref benzinga:ID, which is how to find them.`

// analystFetchRequest is one request: the ratings of one day, of one ticker or
// (symbol empty) of every stock.
type analystFetchRequest struct{ day, symbol string }

// analystFetchTally adds up what a fetch did.
type analystFetchTally struct {
	read, fresh, changed, same int
	dropped, rejected          map[string]int
}

// labEventdbFetchAnalyst fetches Benzinga's analyst ratings and imports them.
func labEventdbFetchAnalyst(ctx context.Context, a *app, args []string) error {
	fs := newFlags("eventdb fetch-analyst")
	from := fs.String("from", "", "first day, YYYY-MM-DD")
	to := fs.String("to", "", "last day, YYYY-MM-DD, inclusive")
	symbols := fs.String("symbols", "", "only these tickers, comma-separated (one request per day and ticker)")
	zoneName := fs.String("time-zone", "", "zone Benzinga's date and time are in, such as UTC or America/New_York (required)")
	maxN := fs.Int("max", defaultAnalystRequests, "most requests to make; 0 means no limit")
	plan := fs.Bool("plan", false, "print what would be requested and make no request")
	yes := fs.Bool("yes", false, "make the requests: they can be many, so say -plan first")
	dry := fs.Bool("dry-run", false, "with -yes: fetch and convert, store nothing")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("eventdb fetch-analyst takes no arguments, got %q\n%s", rest[0], analystFetchUsage)
	}
	first, err := parseDay(*from, time.UTC)
	if err != nil {
		return usageErrorf("-from: %v", err)
	}
	last, err := parseDay(*to, time.UTC)
	if err != nil {
		return usageErrorf("-to: %v", err)
	}
	if *zoneName == "" {
		return usageErrorf("eventdb fetch-analyst needs -time-zone: Benzinga's documentation says UTC, its samples fit America/New_York, so the program does not guess\n%s", analystFetchUsage)
	}
	zone, err := time.LoadLocation(*zoneName)
	if err != nil {
		return usageErrorf("-time-zone %q is not a zone name such as UTC or America/New_York", *zoneName)
	}
	switch {
	case last.Before(first):
		return usageErrorf("-to is before -from")
	case *maxN < 0:
		return usageErrorf("-max must not be negative")
	case *plan && *yes:
		return usageErrorf("-plan and -yes are alternatives")
	case !*plan && !*yes:
		return usageErrorf("this can be many requests: run with -plan to see them, then with -yes to make them")
	case *dry && !*yes:
		return usageErrorf("-dry-run goes with -yes: it fetches, and stores nothing")
	}
	var requests []analystFetchRequest
	tickers := splitList(*symbols, true)
	if len(tickers) == 0 {
		tickers = []string{""}
	}
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		for _, t := range tickers {
			requests = append(requests, analystFetchRequest{day: d.Format(time.DateOnly), symbol: t})
		}
	}
	budget := *maxN
	if budget == 0 || budget > len(requests) {
		budget = len(requests)
	}
	c := a.cfg.Company
	client := benzinga.Client{Key: c.PolygonKey}
	if c.QuotesURL != "" {
		client.URL = c.QuotesURL + "benzinga/ratings" // an overridden address (a test) holds every Massive request
	}
	if *plan {
		a.printAnalystFetchPlan(client, requests, budget, zone)
		return nil
	}
	if c.PolygonKey == "" {
		return fmt.Errorf("analyst ratings come through Massive, which needs a key of your own: set STOXICITY_POLYGON_KEY or MASSIVE_API_KEY (in .env.local)")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	refs, err := db.RefTickers(ctx)
	if err != nil {
		return err
	}
	ciks := map[string]string{}
	for _, r := range refs {
		if cur, ok := ciks[r.Ticker]; !ok || (cur == "" && r.CIK != "") {
			ciks[r.Ticker] = r.CIK
		}
	}
	if client.HTTP, err = a.httpClient(); err != nil {
		return err
	}
	if !*dry {
		fmt.Fprintln(a.stdout, "licence: this is Benzinga individual-use data; it must be deleted within 30 days of cancelling the add-on (the rows have source import and source_ref benzinga:ID)")
	}

	tally := analystFetchTally{dropped: map[string]int{}, rejected: map[string]int{}}
	asked, failed, left := 0, 0, len(requests)-budget
	var runErr error
	for i, req := range requests[:budget] {
		if err := ctx.Err(); err != nil {
			left += budget - i
			runErr = err
			break
		}
		ratings, err := client.Ratings(ctx, req.day, req.symbol)
		var he *httpx.Error
		if errors.As(err, &he) && (he.Class == httpx.Throttled || he.Class == httpx.Blocked) {
			left += budget - i
			runErr = fmt.Errorf("the provider is pushing back, stopping: %w", err)
			break
		}
		asked++
		if err != nil {
			failed++
			runErr = errors.Join(runErr, fmt.Errorf("%s %s: %w", req.day, req.symbol, err))
			continue
		}
		fetched := a.now()
		var rows []map[string]string
		for _, r := range ratings {
			converted, reason := benzinga.Convert(r, zone, fetched)
			if reason != "" {
				tally.dropped[reason]++
				continue
			}
			rows = append(rows, converted...)
		}
		tally.read += len(ratings)
		imp := eventfact.FromAnalystRows(rows, func(symbol string) (string, bool) {
			cik, ok := ciks[symbol]
			return cik, ok
		})
		for reason, n := range imp.Rejected {
			tally.rejected[reason] += n
		}
		for _, r := range imp.Records {
			hist, err := db.EventFactHistory(ctx, r.ID)
			if err != nil {
				return err
			}
			switch {
			case len(hist) == 0:
				tally.fresh++
			case eventfact.Hash(hist[len(hist)-1].Record) == eventfact.Hash(r):
				tally.same++
			default:
				tally.changed++
			}
		}
		if !*dry {
			if _, err := db.PutEventFacts(context.WithoutCancel(ctx), imp.Records, a.now()); err != nil {
				return err
			}
		}
	}
	a.printAnalystFetchResult(tally, asked, failed, left, *dry)
	return runErr
}

// printAnalystFetchPlan says what a fetch would request, and under which
// request budget.
func (a *app) printAnalystFetchPlan(client benzinga.Client, requests []analystFetchRequest, budget int, zone *time.Location) {
	fmt.Fprintf(a.stdout, "analyst fetch plan  Benzinga ratings through Massive, times read in %s\n", zone)
	fmt.Fprintf(a.stdout, "requests       %d, of which %d would be made now\n", len(requests), budget)
	if q, ok := a.cfg.Fetch.Quotas[analystQuotaKey]; ok {
		fmt.Fprintf(a.stdout, "request budget %d per %s (fetch.quotas.%s); a request over it stops the run\n", q.Requests, q.Period, analystQuotaKey)
	} else {
		fmt.Fprintf(a.stdout, "request budget none configured for %s (fetch.quotas); the plan's own limit decides\n", analystQuotaKey)
	}
	for i, r := range requests {
		if i >= analystPlanLines {
			fmt.Fprintf(a.stdout, "... and %d more\n", len(requests)-i)
			break
		}
		fmt.Fprintf(a.stdout, "GET %s\n", client.RequestURL(r.day, r.symbol))
	}
}

// printAnalystFetchResult prints what a fetch read, left out, and stored.
func (a *app) printAnalystFetchResult(t analystFetchTally, asked, failed, left int, dry bool) {
	verb := "stored"
	if dry {
		verb = "would be stored"
	}
	fmt.Fprintf(a.stdout, "requests: %d made, %d failed, %d not made\n", asked, failed, left)
	fmt.Fprintf(a.stdout, "records read: %d\n", t.read)
	a.printReasonTable("records dropped", t.dropped)
	fmt.Fprintf(a.stdout, "revisions %s: %d new, %d corrected, %d unchanged\n", verb, t.fresh, t.changed, t.same)
	a.printReasonTable("rows rejected", t.rejected)
}

// printReasonTable prints a count of what was left out and, when it is not
// none, the counts by reason.
func (a *app) printReasonTable(title string, byReason map[string]int) {
	total := 0
	for _, n := range byReason {
		total += n
	}
	fmt.Fprintf(a.stdout, "%s: %d\n", title, total)
	if total == 0 {
		return
	}
	t := cliTable{Header: []string{"reason", "count"}, Right: []bool{false, true}}
	for _, reason := range labKeys(byReason) {
		t.Rows = append(t.Rows, []cliCell{plain(reason), plain(strconv.Itoa(byReason[reason]))})
	}
	t.write(a.stdout, useColor(a.stdout))
}
