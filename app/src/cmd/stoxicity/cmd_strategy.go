package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/quotes"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/strategy"
)

const (
	recStrategyConfig = "strategy.config"
	recAssumptions    = "strategy.assumptions"
)

// cmdStrategy proposes, never acts: it reads the stored figures, notes, payouts
// and a price, applies a strategy configuration you have reviewed, and prints
// the action with the checklist behind it. Nothing it prints is an order, a
// probability or a forecast.
func cmdStrategy(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: config | assumptions | propose | fund")
	}
	switch args[0] {
	case "config":
		return strategyConfig(ctx, a, args[1:])
	case "assumptions":
		return strategyAssumptions(ctx, a, args[1:])
	case "propose":
		return strategyPropose(ctx, a, args[1:])
	case "fund":
		return strategyFund(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (config, assumptions, propose, fund)", args[0])
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func printJSON(a *app, v any) error { return writeJSON(a.stdout, v) }

func loadConfig(ctx context.Context, rs store.Records) (strategy.Config, bool, error) {
	r, err := rs.Latest(ctx, recStrategyConfig, strategy.CoreID)
	if errors.Is(err, store.ErrNoRecord) {
		return strategy.DefaultConfig(), false, nil
	}
	if err != nil {
		return strategy.Config{}, false, err
	}
	var c strategy.Config
	if err := json.Unmarshal(r.Body, &c); err != nil {
		return strategy.Config{}, false, err
	}
	return c, true, nil
}

func strategyConfig(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: config example | set -file F | review | show")
	}
	switch args[0] {
	case "example":
		return printJSON(a, strategy.DefaultConfig())
	case "set":
		fs := newFlags("strategy config set")
		file := fs.String("file", "", "JSON configuration (strategy config example shows it)")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 || *file == "" {
			return usageErrorf("usage: strategy config set -file FILE")
		}
		var c strategy.Config
		if err := readJSONFile(*file, &c); err != nil {
			return err
		}
		if err := c.Validate(); err != nil {
			return err
		}
		c.Reviewed = false
		rs, err := a.records()
		if err != nil {
			return err
		}
		body, _ := json.Marshal(c)
		if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recStrategyConfig, Key: c.ID, Body: body}); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "stored %s version %d (%s). It is NOT reviewed: until you run strategy config review, no proposal can be stronger than WATCH.\n", c.ID, c.Version, c.Hash()[:12])
		return nil
	case "review":
		rs, err := a.records()
		if err != nil {
			return err
		}
		c, stored, err := loadConfig(ctx, rs)
		if err != nil {
			return err
		}
		if !stored {
			return errors.New("no configuration is stored: run strategy config set first, so what you review is what is used")
		}
		c.Reviewed = true
		body, _ := json.Marshal(c)
		if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recStrategyConfig, Key: c.ID, Body: body}); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s version %d (%s) is marked as reviewed by you. These thresholds are unvalidated starting points, not evidence of any edge.\n", c.ID, c.Version, c.Hash()[:12])
		return nil
	case "show":
		rs, err := a.records()
		if err != nil {
			return err
		}
		c, stored, err := loadConfig(ctx, rs)
		if err != nil {
			return err
		}
		if !stored {
			fmt.Fprintln(a.stdout, "no configuration stored: the starter configuration is used, unreviewed")
		}
		return printJSON(a, c)
	}
	return usageErrorf("unknown subcommand %q", args[0])
}

func exampleAssumptions() strategy.Assumptions {
	sc := func(n, g, d, t string) strategy.Scenario {
		return strategy.Scenario{Name: n, Growth: money.MustDecimal(g), Discount: money.MustDecimal(d), Terminal: money.MustDecimal(t)}
	}
	return strategy.Assumptions{Method: strategy.MethodOwnerEarningsDCF, Currency: "EUR", Normalization: money.MustDecimal("1"), Years: 10,
		Bear: sc("bear", "0", "0.10", "0.01"), Base: sc("base", "0.03", "0.09", "0.02"), Bull: sc("bull", "0.05", "0.08", "0.025"),
		Source: "REPLACE: who made these assumptions", Ref: "REPLACE: the memo or model"}
}

func strategyAssumptions(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: assumptions example | set -security S -file F | show -security S")
	}
	if args[0] == "example" {
		return printJSON(a, exampleAssumptions())
	}
	fs := newFlags("strategy assumptions")
	sec := fs.String("security", "", "the security ID")
	file := fs.String("file", "", "JSON assumptions")
	rest, err := parseFlags(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) > 0 || *sec == "" {
		return usageErrorf("usage: strategy assumptions set -security S -file F | show -security S")
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	switch args[0] {
	case "set":
		if *file == "" {
			return usageErrorf("usage: strategy assumptions set -security S -file F")
		}
		var as strategy.Assumptions
		if err := readJSONFile(*file, &as); err != nil {
			return err
		}
		if strings.HasPrefix(as.Source, "REPLACE") || strings.HasPrefix(as.Ref, "REPLACE") {
			return errors.New("the example's placeholder source was not replaced: say where your assumptions come from")
		}
		if err := as.Validate(); err != nil {
			return err
		}
		secs, err := securitySet(ctx, a)
		if err != nil {
			return err
		}
		if err := knownSecurity(secs, *sec); err != nil {
			return err
		}
		body, _ := json.Marshal(as)
		if _, fresh, err := rs.Append(ctx, store.RecordInput{Kind: recAssumptions, Key: *sec, Body: body}); err != nil {
			return err
		} else if fresh == 0 {
			fmt.Fprintln(a.stdout, "already stored")
			return nil
		}
		fmt.Fprintf(a.stdout, "stored the valuation assumptions of %s (%s)\n", *sec, as.Hash()[:12])
		return nil
	case "show":
		as, err := loadAssumptions(ctx, rs, *sec)
		if err != nil {
			return err
		}
		if as == nil {
			fmt.Fprintf(a.stdout, "no assumptions are stored for %s: it can be researched but not priced\n", *sec)
			return nil
		}
		return printJSON(a, as)
	}
	return usageErrorf("unknown subcommand %q", args[0])
}

func loadAssumptions(ctx context.Context, rs store.Records, sec string) (*strategy.Assumptions, error) {
	r, err := rs.Latest(ctx, recAssumptions, sec)
	if errors.Is(err, store.ErrNoRecord) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var as strategy.Assumptions
	if err := json.Unmarshal(r.Body, &as); err != nil {
		return nil, err
	}
	return &as, nil
}

func resultCell(r strategy.Result) cliCell {
	switch r {
	case strategy.Pass:
		return styled("pass", cliGreen)
	case strategy.Fail:
		return styled("fail", cliRed)
	case strategy.Unknown:
		return styled("unknown", cliYellow)
	}
	return styled("n/a", cliDim)
}

func actionCell(x strategy.Action) cliCell {
	switch x {
	case strategy.Buy, strategy.Add:
		return styled(string(x), cliGreen)
	case strategy.Abstain:
		return styled(string(x), cliRed)
	}
	return styled(string(x), cliYellow)
}

func strategyPropose(ctx context.Context, a *app, args []string) error {
	fs := newFlags("strategy propose")
	sec := fs.String("security", "", "the security ID")
	asOf := fs.String("as-of", "", "the day, YYYY-MM-DD; default today")
	priceFlag := fs.String("price", "", `a price you supply, e.g. "22.50 EUR" (with -price-day)`)
	priceDay := fs.String("price-day", "", "the day of the supplied price")
	mic := fs.String("mic", "", "venue, to read the stored price instead")
	ticker := fs.String("ticker", "", "ticker, with -mic")
	held := fs.Bool("held", false, "the security is already owned")
	cashYield := fs.String("cash-yield", "", "the yield of cash as a fraction, e.g. 0.025")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *sec == "" {
		return usageErrorf("usage: strategy propose -security S [-as-of DAY] [-price \"22.50 EUR\" -price-day DAY | -mic M -ticker T] [-held] [-cash-yield F] [-json]")
	}
	day := a.now().UTC().Format(time.DateOnly)
	if *asOf != "" {
		day = *asOf
	}
	dayT, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return usageErrorf("-as-of must be YYYY-MM-DD")
	}
	known := dayT.Add(24*time.Hour - time.Nanosecond)
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	if err := knownSecurity(secs, *sec); err != nil {
		return err
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	cfg, stored, err := loadConfig(ctx, rs)
	if err != nil {
		return err
	}
	card, err := loadCard(ctx, rs, *sec, known, 5)
	if err != nil {
		return err
	}
	as, err := loadAssumptions(ctx, rs, *sec)
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	obs, err := db.DistributionObservations(ctx, []string{*sec}, known)
	if err != nil {
		return err
	}
	in := strategy.Input{Security: *sec, AsOf: day, Card: card, Payouts: distribution.Resolve(obs, time.Time{}), Assumptions: as, Config: cfg, Held: *held}
	if *cashYield != "" {
		y, err := money.ParseDecimal(*cashYield)
		if err != nil {
			return usageErrorf("-cash-yield: %v", err)
		}
		in.CashYield = &y
	}
	switch {
	case *priceFlag != "":
		if *priceDay == "" {
			return usageErrorf("-price needs -price-day")
		}
		amt, err := money.ParseAmount(*priceFlag)
		if err != nil {
			return usageErrorf("-price: %v", err)
		}
		v, err := money.ParseDecimal(strings.Fields(*priceFlag)[0])
		if err != nil {
			return usageErrorf("-price: %v", err)
		}
		in.Price = &strategy.Price{Value: v, Currency: amt.Currency(), Day: *priceDay, Source: "supplied"}
	case *mic != "" && *ticker != "":
		pr, why, err := storedPrice(ctx, a, db, *mic, *ticker, day, known, cfg.MaxPriceAgeDays)
		if err != nil {
			return err
		}
		if pr == nil {
			fmt.Fprintf(a.stderr, "no usable stored price (%s): the proposal will say so\n", why)
		}
		in.Price = pr
	}
	p, err := strategy.Evaluate(in)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, p)
	}
	if !stored {
		fmt.Fprintln(a.stdout, "using the starter configuration (unreviewed): strategy config example / set / review")
	}
	fmt.Fprintf(a.stdout, "%s on %s by %s: %s   data %s, strategy %s, execution %s\n", p.Security, p.AsOf, p.Strategy,
		styledText(string(p.Action), actionCell(p.Action).Style, useColor(a.stdout)), p.DataState, p.StrategyState, p.ExecutionEligibility)
	fmt.Fprintf(a.stdout, "score %d of %d (a policy ordering, not a probability), coverage %s\n\n", p.Score, p.MaxScore, orDash(p.Coverage))
	t := cliTable{Header: []string{"group", "test", "required", "result", "points", "detail"}, Right: []bool{false, false, false, false, true}}
	for _, x := range p.Tests {
		req := ""
		if x.Required {
			req = "yes"
		}
		pts := fmt.Sprintf("%d/%d", x.Awarded, x.Max)
		if x.Max == 0 {
			pts = "-"
		}
		t.Rows = append(t.Rows, []cliCell{plain(x.Group), plain(x.ID), plain(req), resultCell(x.Result), plain(pts), plain(x.Detail)})
	}
	t.write(a.stdout, useColor(a.stdout))
	if v := p.Valuation; v != nil {
		fmt.Fprintf(a.stdout, "\nvaluation per share in %s (assumptions %s; cash flow per share %s):\n  bear %s   base %s   bull %s   entry ceiling %s (%s less %s margin of safety)\n", v.Currency, p.Inputs.Assumptions[:12], v.CashFlowShare, v.Bear, v.Base, v.Bull, v.Ceiling, v.EntryScenario, pct4(v.MarginOfSafety))
		st := cliTable{Header: []string{"discount shift", "terminal growth shift", "base value"}, Right: []bool{true, true, true}}
		for _, s := range v.Sensitivity {
			val := "unavailable"
			if s.Available {
				val = s.Value.String()
			}
			st.Rows = append(st.Rows, []cliCell{plain(s.DiscountShift.String()), plain(s.TerminalShift.String()), plain(val)})
		}
		st.write(a.stdout, useColor(a.stdout))
		fmt.Fprintln(a.stdout, "scenarios are assumptions laid out, not targets and not forecasts.")
	}
	if p.Price != nil {
		fmt.Fprintf(a.stdout, "price %s %s on %s (%s)\n", p.Price.Value, p.Price.Currency, p.Price.Day, p.Price.Source)
	}
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(a.stdout, "\n%s:\n", title)
		for _, l := range lines {
			fmt.Fprintln(a.stdout, "  - "+l)
		}
	}
	section("reasons", p.Reasons)
	section("thesis (supporting notes)", p.Thesis)
	section("counter-evidence", p.Counter)
	section("invalidation", p.Invalidation)
	section("ignored", p.Ignored)
	var alts []string
	for _, x := range p.Alternatives {
		alts = append(alts, x.Name+": "+x.Note)
	}
	section("alternatives", alts)
	if p.IncomePreference {
		fmt.Fprintf(a.stdout, "\nincome preference: yes (a tie-breaker among qualifying proposals; no points). next ex-date %s\n", orDash(p.NextExDay))
	}
	fmt.Fprintf(a.stdout, "\nreview by %s. proposal %s; inputs: scorecard %s, config %s\n", p.ReviewBy, p.Hash[:16], orDash(trunc(p.Inputs.Card, 12)), trunc(p.Inputs.Config, 12))
	return nil
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func pct4(d money.Decimal) string {
	p, err := d.Mul(money.MustDecimal("100"), 1, money.HalfEven)
	if err != nil {
		return d.String()
	}
	return p.String() + "%"
}

func exampleFund() strategy.FundFacts {
	c, tr := money.MustDecimal("0.0020"), money.MustDecimal("-0.0010")
	return strategy.FundFacts{Security: "REPLACE", Name: "REPLACE: a broad world equity ETF", Currency: "EUR", Exposure: []money.Currency{"USD", "EUR", "JPY"}, OngoingCost: &c, TrackingDifference: &tr,
		Domicile: "IE", Structure: "physical", Distribution: "accumulating", Holdings: 1500, HoldingsDate: "2026-01-31", Source: "REPLACE: KID or factsheet", Ref: "REPLACE: document and date"}
}

func strategyFund(ctx context.Context, a *app, args []string) error {
	fs := newFlags("strategy fund")
	file := fs.String("file", "", "JSON facts about the fund (strategy fund -example shows them)")
	example := fs.Bool("example", false, "print an example facts file")
	asOf := fs.String("as-of", "", "the day, YYYY-MM-DD; default today")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if *example {
		return printJSON(a, exampleFund())
	}
	if *file == "" {
		return usageErrorf("usage: strategy fund -file FACTS.json [-as-of DAY] [-json] | -example")
	}
	var f strategy.FundFacts
	if err := readJSONFile(*file, &f); err != nil {
		return err
	}
	if strings.HasPrefix(f.Source, "REPLACE") || f.Security == "REPLACE" {
		return errors.New("the example's placeholders were not replaced")
	}
	day := a.now().UTC().Format(time.DateOnly)
	if *asOf != "" {
		day = *asOf
	}
	as := strategy.AssessFund(f, strategy.DefaultFundConfig(), day)
	if *asJSON {
		return printJSON(a, as)
	}
	t := cliTable{Header: []string{"check", "result", "detail"}}
	for _, c := range as.Checks {
		t.Rows = append(t.Rows, []cliCell{plain(c.Label), resultCell(c.Result), plain(c.Detail)})
	}
	t.write(a.stdout, useColor(a.stdout))
	verdict := "not eligible as a core fund"
	if as.Eligible && as.Broad {
		verdict = "eligible as a broad core fund"
	}
	fmt.Fprintf(a.stdout, "\n%s by %s on %s: %s (checks, not a score: a fund is never compared with a company by number)\n", f.Security, as.Strategy, as.AsOf, verdict)
	for _, r := range as.Reasons {
		fmt.Fprintln(a.stdout, "  - "+r)
	}
	return nil
}

// storedPrice reads the usable stored close of a listing for a day. It returns
// nil and the reason when there is none; an unresolvable listing is an error.
func storedPrice(ctx context.Context, a *app, db *store.DB, mic, ticker, day string, known time.Time, maxAge int) (*strategy.Price, string, error) {
	reg, err := loadRegistry(ctx, a)
	if err != nil {
		return nil, "", err
	}
	res := reg.ResolveListing(instrument.Query{MIC: mic, Ticker: ticker, Day: day})
	if res.Status != instrument.Resolved {
		writeResolution(a, res)
		return nil, "", fmt.Errorf("the instrument %s %s is %s: name it exactly", mic, ticker, res.Status)
	}
	pobs, err := db.PriceObservations(ctx, res.Listing.ID, known)
	if err != nil {
		return nil, "", err
	}
	ans := quotes.ResolvePrice(pobs, *res.Listing, quotes.Need{Day: day, KnownAt: known, MaxAgeDays: maxAge, Adjustment: quotes.Adjustment("raw"), RequireStatedUnit: true})
	if ans.Verdict != quotes.OK || ans.Price == nil {
		return nil, string(ans.Verdict), nil
	}
	v, err := ans.Price.Major()
	if err != nil {
		return nil, "", err
	}
	return &strategy.Price{Value: v, Currency: ans.Price.Unit.Currency, Day: ans.Day, Source: strings.Join(ans.Sources, ",")}, "", nil
}
