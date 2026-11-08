package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstrat"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	recEventConfig   = "event.config"
	recEventProposal = "event.proposal"
)

// cmdEvent is the separate, experimental event sleeve. It proposes paper
// candidates and exits; it never produces a real-account suggestion and never
// moves a position into the core.
func cmdEvent(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: config | example | evaluate | review | list")
	}
	switch args[0] {
	case "config":
		return eventConfig(ctx, a, args[1:])
	case "example":
		return eventExample(a)
	case "evaluate":
		return eventEvaluate(ctx, a, args[1:])
	case "review":
		return eventReview(ctx, a, args[1:])
	case "list":
		return eventList(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (config, example, evaluate, review, list)", args[0])
}

func eventExample(a *app) error {
	ex := eventstrat.Event{ID: "ev-2026-q3-example", Security: "sec_apple", Issuer: "iss_apple", Kind: "earnings", Day: "2026-10-29", Timing: "post", Status: "completed",
		Verified: false, Source: "REPLACE: the filing or release", Ref: "REPLACE: where it can be checked", Origin: "REPLACE: the underlying source, one per story",
		ComparableYears: 0, ResultsRef: "REPLACE: scorecard or filing the comparison used", SplitConsistent: false, PriceRef: "REPLACE: the adjusted price source"}
	fmt.Fprintln(a.stdout, "# event.json: every REPLACE is a fact you must source. Leave verified false until you have checked it.")
	fmt.Fprintln(a.stdout, "# security_issuer is filled from the instrument register when left out. The bars file is the one the paper engine reads (paper example).")
	return writeJSON(a.stdout, ex)
}

func loadEventConfig(ctx context.Context, a *app) (eventstrat.Config, bool, error) {
	rs, err := a.records()
	if err != nil {
		return eventstrat.Config{}, false, err
	}
	rec, err := rs.Latest(ctx, recEventConfig, "default")
	if errors.Is(err, store.ErrNoRecord) {
		return eventstrat.DefaultConfig(), false, nil
	}
	if err != nil {
		return eventstrat.Config{}, false, err
	}
	var c eventstrat.Config
	if err := json.Unmarshal(rec.Body, &c); err != nil {
		return c, false, fmt.Errorf("the event configuration does not decode: %w", err)
	}
	return c, true, nil
}

func eventConfig(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: event config example|set|review|show")
	}
	save := func(c eventstrat.Config) error {
		rs, err := a.records()
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(c)
		_, _, err = rs.Append(ctx, store.RecordInput{Kind: recEventConfig, Key: "default", Body: raw})
		return err
	}
	switch args[0] {
	case "example":
		return writeJSON(a.stdout, eventstrat.DefaultConfig())
	case "show":
		c, stored, err := loadEventConfig(ctx, a)
		if err != nil {
			return err
		}
		state := "UNREVIEWED"
		if !stored {
			state = "UNREVIEWED (defaults, never set)"
		} else if c.Reviewed {
			state = "reviewed"
		}
		if err := writeJSON(a.stdout, c); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "status: %s. These values are arbitrary, testable starting points for an experimental paper template, not a claimed edge.\n", state)
		return nil
	case "set":
		fs := newFlags("event config set")
		file := fs.String("file", "", "JSON configuration (event config example shows one)")
		if _, err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if *file == "" {
			return usageErrorf("usage: event config set -file FILE")
		}
		var c eventstrat.Config
		if err := readJSONFile(*file, &c); err != nil {
			return err
		}
		if err := c.Validate(); err != nil {
			return err
		}
		c.Reviewed = false
		if err := save(c); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "event configuration saved (hash %s) and NOT reviewed: until you run event config review nothing it evaluates can be more than research.\n", c.Hash()[:12])
		return nil
	case "review":
		fs := newFlags("event config review")
		by := fs.String("by", "", "who reviewed it")
		if _, err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if *by == "" {
			return usageErrorf("usage: event config review -by NAME")
		}
		c, _, err := loadEventConfig(ctx, a)
		if err != nil {
			return err
		}
		c.Reviewed = true
		if err := save(c); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "event configuration (hash %s) reviewed by %s. It is still an experimental paper template: this does not make it fit for a real account.\n", c.Hash()[:12], *by)
		return nil
	}
	return usageErrorf("unknown subcommand %q (example, set, review, show)", args[0])
}

type eventRecord struct {
	Proposal eventstrat.Proposal `json:"proposal"`
	Day      string              `json:"day"`
}

func loadEventRecords(ctx context.Context, a *app) ([]eventRecord, error) {
	rs, err := a.records()
	if err != nil {
		return nil, err
	}
	recs, err := rs.Current(ctx, recEventProposal, "")
	if err != nil {
		return nil, err
	}
	var out []eventRecord
	for _, r := range recs {
		var e eventRecord
		if err := json.Unmarshal(r.Body, &e); err != nil {
			return nil, fmt.Errorf("proposal %s does not decode: %w", r.Key, err)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Proposal.Event < out[j].Proposal.Event })
	return out, nil
}

func eventEvaluate(ctx context.Context, a *app, args []string) error {
	fs := newFlags("event evaluate")
	eventFile := fs.String("event", "", "JSON file with the event and where it was verified (event example)")
	barsFile := fs.String("bars", "", "CSV of finished daily bars as traded and split-consistent: security, day, open, high, low, close, volume [, currency]")
	feesFile := fs.String("fees", "", "JSON fee schedule with its source")
	equityS := fs.String("equity", "", "account equity in the security's currency, e.g. 10000.00 USD")
	book := fs.String("book", "", "the book whose available cash bounds the size (default: the equity)")
	asOf := fs.String("as-of", "", "evaluation day (default: today); only earlier bars count as finished")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *eventFile == "" || *barsFile == "" || *equityS == "" {
		return usageErrorf("usage: event evaluate -event FILE -bars FILE -equity AMOUNT [-book BOOK] [-fees FILE] [-as-of DAY] [-json]")
	}
	var ev eventstrat.Event
	if err := readJSONFile(*eventFile, &ev); err != nil {
		return err
	}
	bars, err := readBars(*barsFile)
	if err != nil {
		return err
	}
	equity, err := money.ParseAmount(*equityS)
	if err != nil {
		return usageErrorf("-equity: %v", err)
	}
	day := *asOf
	if day == "" {
		day = a.now().UTC().Format(time.DateOnly)
	}
	cfg, _, err := loadEventConfig(ctx, a)
	if err != nil {
		return err
	}
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	if s, ok := secs[ev.Security]; ok {
		if ev.SecurityIssuer == "" {
			ev.SecurityIssuer = s.IssuerID
		}
	}
	fees, err := readFees(*feesFile, equity.Currency())
	if err != nil {
		return err
	}
	acct := eventstrat.Account{Equity: equity, Cash: equity, Fees: fees}
	if *book != "" {
		ps, err := a.portfolios()
		if err != nil {
			return err
		}
		b, err := ps.FindBook(ctx, *book)
		if err != nil {
			return err
		}
		l, err := ps.Ledger(ctx, b.ID)
		if err != nil {
			return err
		}
		avail := l.Projection().Available(equity.Currency())
		if acct.Cash, err = money.FromMinor(avail, equity.Currency()); err != nil {
			return err
		}
		acct.Foreign = b.Base != equity.Currency()
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	if rec, active, err := db.Policies(a.tenantID).AsOf(ctx, time.Time{}); err != nil {
		return err
	} else if active {
		acct.PolicySleeve = rec.Policy.Constraints.MaxEventSleeve
		acct.PolicyRisk = rec.Policy.Constraints.MaxEventTradeRisk
	}
	prev, err := loadEventRecords(ctx, a)
	if err != nil {
		return err
	}
	for _, r := range prev {
		if r.Proposal.Event != ev.ID && r.Proposal.Status == eventstrat.Paper {
			acct.SeenOrigins = append(acct.SeenOrigins, r.Proposal.Origin)
		}
	}
	p, err := eventstrat.Evaluate(eventstrat.Input{Config: cfg, Event: ev, Bars: bars, Account: acct, AsOf: day})
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(eventRecord{Proposal: p, Day: day})
	rs, err := a.records()
	if err != nil {
		return err
	}
	if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recEventProposal, Key: ev.ID, Body: raw}); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, p)
	}
	printEventProposal(a, p, equity.Currency())
	return nil
}

func printEventProposal(a *app, p eventstrat.Proposal, cur money.Currency) {
	color := useColor(a.stdout)
	st := map[eventstrat.Status]cliStyle{eventstrat.Paper: cliYellow, eventstrat.Research: cliYellow, eventstrat.Wait: cliRed}[p.Status]
	fmt.Fprintf(a.stdout, "%s  %s  %s  (%s, EXPERIMENTAL, paper only: never offered for a real account)\n", styledText(string(p.Status), st, color), p.Security, p.Event, p.Template)
	t := cliTable{Header: []string{"test", "result", "required", "detail"}}
	for _, x := range p.Tests {
		r := map[eventstrat.Result]cliCell{eventstrat.Pass: styled("pass", cliGreen), eventstrat.Fail: styled("FAIL", cliRed), eventstrat.Unknown: styled("unknown", cliYellow), eventstrat.Warn: styled("warn", cliYellow)}[x.Result]
		t.Rows = append(t.Rows, []cliCell{styled(x.Name, cliStrong), r, plain(map[bool]string{true: "yes", false: "no"}[x.Required]), plain(x.Detail)})
	}
	t.write(a.stdout, color)
	if p.Status == eventstrat.Paper && p.Qty != nil {
		fmt.Fprintf(a.stdout, "paper candidate: buy at most %s share(s) of %s with a limit of at most %s, not before %s; invalidation %s (%s); review or exit after %d sessions", p.Qty, p.Security, p.EntryLimit, p.EntryNotBefore, p.Invalidation, p.InvalidationBy, p.ReviewSessions)
		if p.ProfitReview != nil {
			fmt.Fprintf(a.stdout, "; profit review at %s (a rule, not an expected return)", p.ProfitReview)
		}
		fmt.Fprintln(a.stdout, ".")
		fmt.Fprintf(a.stdout, "planned loss %s, round-trip fees %s, order value %s. Planned loss is not a guaranteed maximum: a gap can pass the invalidation.\n", p.PlannedLoss, p.RoundTripFees, p.Notional)
		fmt.Fprintln(a.stdout, "Place it on a paper book with order draft; the event position stays in the event sleeve and is never moved into the core automatically.")
		return
	}
	for _, r := range p.Reasons {
		fmt.Fprintln(a.stdout, "- "+r)
	}
	switch p.Status {
	case eventstrat.Research:
		fmt.Fprintln(a.stdout, "RESEARCH: something this template needs is missing or unknown. No quantity is proposed and nothing is inferred from the event being material.")
	case eventstrat.Wait:
		fmt.Fprintln(a.stdout, "WAIT: a test was evaluated and failed, or it is too early, or the trade does not pay for its costs.")
	}
}

func eventReview(ctx context.Context, a *app, args []string) error {
	fs := newFlags("event review")
	prop := fs.String("proposal", "", "the event ID of a saved proposal to take the invalidation, horizon and profit review from")
	sec := fs.String("security", "", "security (without -proposal)")
	entryDay := fs.String("entry-day", "", "the day the paper position was entered")
	entryPrice := fs.String("entry-price", "", "the price it was entered at")
	invalid := fs.String("invalidation", "", "the invalidation level (without -proposal)")
	sessions := fs.Int("sessions", 0, "the time exit in sessions, 5-20 (without -proposal)")
	profit := fs.String("profit-review", "", "the profit-review level (optional)")
	barsFile := fs.String("bars", "", "CSV of finished daily bars")
	asOf := fs.String("as-of", "", "review day (default: today)")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *entryDay == "" || *barsFile == "" {
		return usageErrorf("usage: event review (-proposal EVENT | -security S -invalidation X -sessions N) -entry-day D -entry-price P -bars FILE [-profit-review X] [-as-of DAY]")
	}
	h := eventstrat.Holding{Security: *sec, EntryDay: *entryDay, ReviewSessions: *sessions}
	var err error
	if *prop != "" {
		recs, err := loadEventRecords(ctx, a)
		if err != nil {
			return err
		}
		found := false
		for _, r := range recs {
			if r.Proposal.Event == *prop {
				found = true
				if r.Proposal.Invalidation == nil {
					return fmt.Errorf("%s has no invalidation level: it was never a sized proposal", *prop)
				}
				h.Security, h.Invalidation, h.ReviewSessions, h.ProfitReview = r.Proposal.Security, *r.Proposal.Invalidation, r.Proposal.ReviewSessions, r.Proposal.ProfitReview
			}
		}
		if !found {
			return fmt.Errorf("no saved proposal for event %s (event list)", *prop)
		}
	} else {
		if *sec == "" || *invalid == "" || *sessions == 0 {
			return usageErrorf("without -proposal give -security, -invalidation and -sessions")
		}
		if h.Invalidation, err = money.ParseDecimal(*invalid); err != nil {
			return usageErrorf("-invalidation: %v", err)
		}
	}
	if *entryPrice != "" {
		if h.EntryPrice, err = money.ParseDecimal(*entryPrice); err != nil {
			return usageErrorf("-entry-price: %v", err)
		}
	}
	if *profit != "" {
		p, err := money.ParseDecimal(*profit)
		if err != nil {
			return usageErrorf("-profit-review: %v", err)
		}
		h.ProfitReview = &p
	}
	bars, err := readBars(*barsFile)
	if err != nil {
		return err
	}
	day := *asOf
	if day == "" {
		day = a.now().UTC().Format(time.DateOnly)
	}
	x, err := eventstrat.Review(h, bars, day)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, x)
	}
	color := useColor(a.stdout)
	st := map[eventstrat.ExitKind]cliStyle{eventstrat.Hold: cliGreen, eventstrat.TimeExit: cliYellow, eventstrat.Invalidated: cliRed, eventstrat.ProfitReview: cliYellow}[x.Kind]
	fmt.Fprintf(a.stdout, "%s  %s: %s\n", styledText(string(x.Kind), st, color), h.Security, x.Reason)
	for _, n := range x.Notes {
		fmt.Fprintln(a.stdout, "- "+n)
	}
	switch x.Kind {
	case eventstrat.TimeExit, eventstrat.Invalidated:
		fmt.Fprintln(a.stdout, "This is a distinct exit proposal in the event sleeve. Nothing was sold, and the position is not moved into the core: that would need a new thesis and your recorded decision.")
	case eventstrat.ProfitReview:
		fmt.Fprintln(a.stdout, "A review point, not an instruction to sell.")
	}
	return nil
}

func eventList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("event list")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	recs, err := loadEventRecords(ctx, a)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, recs)
	}
	if len(recs) == 0 {
		fmt.Fprintln(a.stdout, "no event has been evaluated.")
		return nil
	}
	t := cliTable{Header: []string{"event", "security", "status", "evaluated", "reasons"}}
	for _, r := range recs {
		st := map[eventstrat.Status]cliStyle{eventstrat.Paper: cliYellow, eventstrat.Research: cliYellow, eventstrat.Wait: cliRed}[r.Proposal.Status]
		t.Rows = append(t.Rows, []cliCell{styled(r.Proposal.Event, cliStrong), plain(r.Proposal.Security), styled(string(r.Proposal.Status), st), plain(r.Day), plain(strings.Join(r.Proposal.Reasons, "; "))})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}
