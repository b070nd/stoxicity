package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/quotes"
)

// cmdDividends is dividend and fund-distribution observations: import them, list
// the calendar, and judge how reliable a security's history is. Nothing here is
// cash, and nothing is forecast.
func cmdDividends(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: import | calendar | quality")
	}
	switch args[0] {
	case "import":
		return dividendsImport(ctx, a, args[1:])
	case "calendar":
		return dividendsCalendar(ctx, a, args[1:])
	case "quality":
		return dividendsQuality(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (import, calendar, quality)", args[0])
}

func dividendsImport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("dividends import")
	file := fs.String("file", "", "CSV: type, period, status, amount, currency, ex_day and isin or mic+ticker; optional record_day, pay_day, declared_day")
	source := fs.String("source", "", "who gave these payouts (stored with every row)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" || strings.TrimSpace(*source) == "" {
		return usageErrorf("usage: dividends import -file FILE -source TEXT")
	}
	body, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	reg, err := loadRegistry(ctx, a)
	if err != nil {
		return err
	}
	obs, err := distribution.ParseCSV(strings.NewReader(string(body)), reg, *source, quotes.Hash(body), a.now())
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	c, err := db.PutDistributions(ctx, obs, a.now())
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%d observations read, %d new, %d already stored (source %q, %s)\n", len(obs), c.New, c.Unchanged, *source, quotes.Hash(body)[:19])
	fmt.Fprintln(a.stdout, "A declared payout is the issuer's promise per share and an estimate is a guess. Neither is cash.")
	return nil
}

func readSplits(path string) ([]distribution.Split, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return distribution.ParseSplitsCSV(f)
}

// securityNames maps a security ID to the text shown for it.
func securityNames(reg *instrument.Registry) map[string]instrument.Security {
	m := map[string]instrument.Security{}
	for _, s := range reg.Set().Securities {
		m[s.ID] = s
	}
	return m
}

func stateCell(s distribution.State) cliCell {
	switch s {
	case distribution.StateDeclared:
		return styled(string(s), cliGreen)
	case distribution.StateEstimated:
		return styled(string(s), cliYellow)
	}
	return styled(string(s), cliRed)
}

func dividendsCalendar(ctx context.Context, a *app, args []string) error {
	fs := newFlags("dividends calendar")
	isin := fs.String("isin", "", "limit to this ISIN")
	mic := fs.String("mic", "", "venue, with -ticker")
	ticker := fs.String("ticker", "", "limit to this ticker")
	from := fs.String("from", "", "first ex-date, YYYY-MM-DD")
	to := fs.String("to", "", "last ex-date, YYYY-MM-DD")
	at := fs.String("at", "", "only what had been received by this time (RFC 3339)")
	splitFile := fs.String("splits", "", "CSV of share splits (security, day, from, to) to restate earlier amounts")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	known, err := parseAt(*at)
	if err != nil {
		return err
	}
	reg, err := loadRegistry(ctx, a)
	if err != nil {
		return err
	}
	var ids []string
	if *isin != "" || *ticker != "" {
		day := a.now().Format(time.DateOnly)
		if *to != "" {
			day = *to
		}
		res := reg.ResolveSecurity(instrument.Query{ISIN: *isin, MIC: *mic, Ticker: *ticker, Day: day})
		if res.Status != instrument.Resolved {
			writeResolution(a, res)
			return fmt.Errorf("the security is %s", res.Status)
		}
		ids = []string{res.Security.ID}
	}
	splits, err := readSplits(*splitFile)
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	obs, err := db.DistributionObservations(ctx, ids, known)
	if err != nil {
		return err
	}
	payouts, err := distribution.AdjustForSplits(distribution.Resolve(obs, time.Time{}), splits)
	if err != nil {
		return err
	}
	cal := distribution.BuildCalendar(payouts, *from, *to)
	if *asJSON {
		return writeJSON(a.stdout, cal)
	}
	if len(cal.Entries) == 0 {
		fmt.Fprintln(a.stdout, "no payouts in range\nimport some with: stoxicity dividends import")
		return nil
	}
	names := securityNames(reg)
	t := cliTable{Header: []string{"ex-date", "type", "period", "state", "per share", "ccy", "record", "pay", "sources", "security", "note"}, Right: []bool{false, false, false, false, true}}
	for _, p := range cal.Entries {
		amt := p.Amount.String()
		if p.State == distribution.StateConflicting {
			amt = "?"
		}
		t.Rows = append(t.Rows, []cliCell{plain(p.ExDay), plain(string(p.Key.Type)), plain(p.Key.Period), stateCell(p.State), plain(amt), plain(string(p.Currency)),
			plain(p.RecordDay), plain(p.PayDay), plain(strings.Join(p.Sources, ",")), styled(names[p.Key.SecurityID].Name, cliStrong), plain(strings.Join(p.Reasons, "; "))})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintln(a.stdout)
	tt := cliTable{Header: []string{"ccy", "type", "declared", "estimated", "n declared", "n estimated"}, Right: []bool{false, false, true, true, true, true}}
	for _, x := range cal.Totals {
		tt.Rows = append(tt.Rows, []cliCell{plain(string(x.Currency)), plain(string(x.Type)), plain(x.Declared.String()), styled(x.Estimated.String(), cliYellow), plain(fmt.Sprint(x.NDeclared)), plain(fmt.Sprint(x.NEst))})
	}
	tt.write(a.stdout, useColor(a.stdout))
	fmt.Fprintf(a.stdout, "\nper-share amounts, not cash; declared and estimated are never added; currencies are not converted. %d cancelled, %d conflicting (excluded from the totals).\n", cal.Cancelled, cal.Conflicting)
	return nil
}

func dividendsQuality(ctx context.Context, a *app, args []string) error {
	fs := newFlags("dividends quality")
	isin := fs.String("isin", "", "the security's ISIN")
	mic := fs.String("mic", "", "venue, with -ticker")
	ticker := fs.String("ticker", "", "ticker")
	day := fs.String("day", "", "the as-of day, YYYY-MM-DD (required)")
	at := fs.String("at", "", "only what had been received by this time (RFC 3339)")
	splitFile := fs.String("splits", "", "CSV of share splits; without it a cut may be a split and the report says so")
	sector := fs.String("sector", "", "the kind of business (technology, utility, bank, reit, ...); empty is unknown")
	fcf := fs.String("fcf-per-share", "", "trailing free cash flow per share, with -fcf-currency")
	fcfCur := fs.String("fcf-currency", "", "currency of -fcf-per-share")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *day == "" || (*isin == "" && *ticker == "") {
		return usageErrorf("usage: dividends quality (-isin I | -mic M -ticker T) -day YYYY-MM-DD [-splits FILE] [-sector S] [-fcf-per-share X -fcf-currency C] [-at TIME] [-json]")
	}
	known, err := parseAt(*at)
	if err != nil {
		return err
	}
	in := distribution.Input{Day: *day, Sector: *sector}
	if *fcf != "" {
		v, err := money.ParseDecimal(*fcf)
		if err != nil {
			return usageErrorf("-fcf-per-share: %v", err)
		}
		cur, err := money.ParseCurrency(*fcfCur)
		if err != nil {
			return usageErrorf("-fcf-currency: %v", err)
		}
		in.FCFPerShare, in.FCFCurrency = &v, cur
	}
	reg, err := loadRegistry(ctx, a)
	if err != nil {
		return err
	}
	res := reg.ResolveSecurity(instrument.Query{ISIN: *isin, MIC: *mic, Ticker: *ticker, Day: *day})
	if res.Status != instrument.Resolved {
		writeResolution(a, res)
		return fmt.Errorf("the security is %s", res.Status)
	}
	in.Security = *res.Security
	splits, err := readSplits(*splitFile)
	if err != nil {
		return err
	}
	in.SplitsKnown = *splitFile != ""
	db, err := a.store()
	if err != nil {
		return err
	}
	obs, err := db.DistributionObservations(ctx, []string{in.Security.ID}, known)
	if err != nil {
		return err
	}
	in.Payouts, err = distribution.AdjustForSplits(distribution.Resolve(obs, time.Time{}), splits)
	if err != nil {
		return err
	}
	q := distribution.Analyse(in)
	if *asJSON {
		return writeJSON(a.stdout, q)
	}
	writeQuality(a, in.Security, q)
	return nil
}

func ccyMap(m map[money.Currency]money.Decimal) string {
	if len(m) == 0 {
		return "none on record"
	}
	var keys []string
	for c := range m {
		keys = append(keys, string(c))
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, m[money.Currency(k)].String()+" "+k)
	}
	return strings.Join(parts, ", ")
}

func writeQuality(a *app, s instrument.Security, q distribution.Quality) {
	cov := cliYellow
	switch q.Coverage {
	case distribution.Observed:
		cov = cliGreen
	case distribution.Unknown:
		cov = cliRed
	}
	t := cliTable{Header: []string{"measure", "value"}}
	row := func(k string, v cliCell) { t.Rows = append(t.Rows, []cliCell{plain(k), v}) }
	row("security", styled(s.Name+" ("+s.ID+")", cliStrong))
	row("as of", plain(q.Day))
	row("coverage", styled(string(q.Coverage), cov))
	row("frequency", plain(q.Frequency))
	row("ordinary, declared, last 12 months", plain(ccyMap(q.OrdinaryTTM)))
	row("special, declared, last 12 months", plain(ccyMap(q.SpecialTTM)))
	if q.Latest != nil {
		st := cliGreen
		if q.Latest.Direction == "cut" {
			st = cliRed
		}
		row("latest vs "+q.Latest.Prior, styled(fmt.Sprintf("%s: %s -> %s %s (%s)", q.Latest.Direction, q.Latest.PriorAmt, q.Latest.Amount, q.Latest.Currency, q.Latest.Ratio), st))
	} else {
		row("latest vs prior year", styled("no comparable payout", cliDim))
	}
	row("cuts in 24 months", plain(fmt.Sprint(len(q.Cuts))))
	row("cancellations in 24 months", plain(fmt.Sprint(q.Cancellations)))
	row("missed periods in the series", plain(strings.Join(q.MissedPeriods, ", ")))
	row("history", plain(fmt.Sprintf("%d months", q.HistoryMonths)))
	cs := cliYellow
	if q.Cover == distribution.CoverComputed {
		cs = cliGreen
	}
	cover := string(q.Cover)
	if q.CoverRatio != nil {
		cover += ": " + q.CoverRatio.String() + "x"
	}
	row("free cash flow cover", styled(cover, cs))
	row("  why", styled(q.CoverWhy, cliDim))
	t.write(a.stdout, useColor(a.stdout))
	if len(q.Estimated) > 0 {
		fmt.Fprintln(a.stdout, "\nestimated, not declared (not in any figure above):")
		for _, p := range q.Estimated {
			fmt.Fprintf(a.stdout, "  %s %s %s %s on %s\n", p.Key.Type, p.Key.Period, p.Amount, p.Currency, p.ExDay)
		}
	}
	if len(q.Unknowns) > 0 {
		fmt.Fprintln(a.stdout, "\nunknown or unreliable:")
		for _, u := range q.Unknowns {
			fmt.Fprintf(a.stdout, "  %s\n", u)
		}
	}
	fmt.Fprintln(a.stdout, "\nper-share history of what issuers declared. It is not cash received and not a forecast.")
}
