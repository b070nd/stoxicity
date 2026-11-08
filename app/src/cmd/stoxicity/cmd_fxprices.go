package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/quotes"
)

// cmdFX is exchange-rate observations: fetch the ECB reference rates, import
// your own, list what is stored, and ask for the rate usable on a day.
func cmdFX(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: fetch | import | list | rate")
	}
	switch args[0] {
	case "fetch":
		return fxFetch(ctx, a, args[1:])
	case "import":
		return fxImport(ctx, a, args[1:])
	case "list":
		return fxList(ctx, a, args[1:])
	case "rate":
		return fxRate(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (fetch, import, list, rate)", args[0])
}

func fxFetch(ctx context.Context, a *app, args []string) error {
	fs := newFlags("fx fetch")
	feed := fs.String("feed", "daily", "daily (latest day), 90d (about three months) or history (since 1999)")
	addr := fs.String("url", "", "read this address instead of the ECB's (a mirror, or a test server)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	url := *addr
	if url == "" {
		switch *feed {
		case "daily":
			url = quotes.ECBDailyURL
		case "90d":
			url = quotes.ECB90DayURL
		case "history":
			url = quotes.ECBHistoryURL
		default:
			return usageErrorf("-feed must be daily, 90d or history")
		}
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	obs, rep, err := quotes.ECB{Client: client, Now: a.now}.Fetch(ctx, url)
	if err != nil {
		return err
	}
	c, err := db.PutFX(ctx, obs, a.now())
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s: %d days, %d rates read, %d new, %d already stored\n", url, rep.Days, len(obs), c.New, c.Unchanged)
	if len(rep.Skipped) > 0 {
		var parts []string
		for _, k := range sortedStrings(rep.Skipped) {
			parts = append(parts, k)
		}
		fmt.Fprintf(a.stdout, "skipped (no minor-unit table for them here): %s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintln(a.stdout, "ECB reference rates are for reporting. They are not rates anything was or can be traded at.")
	return nil
}

func sortedStrings(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func fxImport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("fx import")
	file := fs.String("file", "", "CSV with columns base, quote, day, rate and optionally basis (reference, fill, user) and ref")
	source := fs.String("source", "", "who gave these rates (stored with every row)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" || strings.TrimSpace(*source) == "" {
		return usageErrorf("usage: fx import -file FILE -source TEXT")
	}
	body, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	obs, err := quotes.ParseFXCSV(strings.NewReader(string(body)), *source, quotes.Hash(body), a.now())
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	c, err := db.PutFX(ctx, obs, a.now())
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%d rates read, %d new, %d already stored (source %q, %s)\n", len(obs), c.New, c.Unchanged, *source, quotes.Hash(body)[:19])
	fmt.Fprintln(a.stdout, "A rate with no basis column is stored as a user assumption.")
	return nil
}

func fxList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("fx list")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	sums, err := db.FXSummaries(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, sums)
	}
	if len(sums) == 0 {
		fmt.Fprintln(a.stdout, "no exchange rates stored yet\nstart with: stoxicity fx fetch")
		return nil
	}
	t := cliTable{Header: []string{"pair", "basis", "source", "observations", "first day", "last day"}, Right: []bool{false, false, false, true}}
	for _, s := range sums {
		st := cliGreen
		if s.Basis != money.BasisReference {
			st = cliYellow
		}
		t.Rows = append(t.Rows, []cliCell{styled(string(s.Base)+"/"+string(s.Quote), cliStrong), styled(string(s.Basis), st), plain(s.Source), plain(fmt.Sprint(s.Count)), plain(s.First), plain(s.Last)})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func fxRate(ctx context.Context, a *app, args []string) error {
	fs := newFlags("fx rate")
	from := fs.String("from", "", "base currency (1 FROM = rate TO)")
	to := fs.String("to", "", "quote currency")
	day := fs.String("day", "", "the day the rate is for, YYYY-MM-DD (required)")
	maxAge := fs.Int("max-age", 0, "calendar days older than the day the rate may be; 0 means dated that day")
	at := fs.String("at", "", "only what had been received by this time (RFC 3339)")
	basis := fs.String("basis", "reference", "reference, fill or user: a kind of rate is never answered with another")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *from == "" || *to == "" || *day == "" {
		return usageErrorf("usage: fx rate -from EUR -to USD -day YYYY-MM-DD [-max-age N] [-at TIME] [-basis B] [-json]")
	}
	base, err := money.ParseCurrency(*from)
	if err != nil {
		return usageErrorf("-from: %v", err)
	}
	quote, err := money.ParseCurrency(*to)
	if err != nil {
		return usageErrorf("-to: %v", err)
	}
	if *maxAge < 0 {
		return usageErrorf("-max-age must not be negative")
	}
	known, err := parseAt(*at)
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	obs, err := db.FXObservations(ctx, base, quote, known)
	if err != nil {
		return err
	}
	ans := quotes.ResolveFX(obs, base, quote, quotes.Need{Day: *day, KnownAt: known, MaxAgeDays: *maxAge, Basis: money.Basis(*basis)})
	if *asJSON {
		if err := writeJSON(a.stdout, ans); err != nil {
			return err
		}
	} else {
		st := cliRed
		if ans.Verdict == quotes.OK {
			st = cliGreen
		}
		fmt.Fprintln(a.stdout, styledText(string(ans.Verdict), st, useColor(a.stdout)))
		if ans.Rate != nil {
			fmt.Fprintf(a.stdout, "1 %s = %s %s   (%s rate of %s, %d day(s) old; %s)\n", ans.Rate.Base, ans.Rate.Value, ans.Rate.Quote, ans.Rate.Basis, ans.Day, ans.AgeDays, strings.Join(ans.Sources, ", "))
		}
		for _, r := range ans.Reasons {
			fmt.Fprintf(a.stdout, "  %s\n", r)
		}
	}
	if ans.Verdict != quotes.OK {
		return fmt.Errorf("no usable rate: %s", ans.Verdict)
	}
	return nil
}

// cmdPrices is price observations: import your own and ask for the price usable
// for a listing on a day.
func cmdPrices(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: import | at")
	}
	switch args[0] {
	case "import":
		return pricesImport(ctx, a, args[1:])
	case "at":
		return pricesAt(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (import, at)", args[0])
}

func loadRegistry(ctx context.Context, a *app) (*instrument.Registry, error) {
	db, err := a.store()
	if err != nil {
		return nil, err
	}
	set, err := db.InstrumentsAsOf(ctx, time.Time{})
	if err != nil {
		return nil, err
	}
	if len(set.Listings) == 0 {
		return nil, fmt.Errorf("no instruments are imported: run stoxicity instruments import first")
	}
	reg, _, err := instrument.New(set)
	return reg, err
}

func pricesImport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("prices import")
	file := fs.String("file", "", "CSV with columns mic, ticker, day, price and optionally unit, kind, adjustment, delay")
	source := fs.String("source", "", "who gave these prices (stored with every row)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" || strings.TrimSpace(*source) == "" {
		return usageErrorf("usage: prices import -file FILE -source TEXT")
	}
	body, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	reg, err := loadRegistry(ctx, a)
	if err != nil {
		return err
	}
	obs, err := quotes.ParsePriceCSV(strings.NewReader(string(body)), quotes.RegistryResolver(reg), *source, quotes.Hash(body), a.now())
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	c, err := db.PutPrices(ctx, obs, a.now())
	if err != nil {
		return err
	}
	fromListing := 0
	for _, o := range obs {
		if o.UnitBasis == quotes.UnitFromListing {
			fromListing++
		}
	}
	fmt.Fprintf(a.stdout, "%d prices read, %d new, %d already stored (source %q, %s)\n", len(obs), c.New, c.Unchanged, *source, quotes.Hash(body)[:19])
	if fromListing > 0 {
		fmt.Fprintf(a.stdout, "%d row(s) stated no unit; the listing's was used and marked, and an action that sizes money will refuse them.\n", fromListing)
	}
	return nil
}

func pricesAt(ctx context.Context, a *app, args []string) error {
	fs := newFlags("prices at")
	mic := fs.String("mic", "", "venue")
	ticker := fs.String("ticker", "", "ticker")
	isin := fs.String("isin", "", "ISIN (with -mic if the security is cross-listed)")
	day := fs.String("day", "", "the day the price is for, YYYY-MM-DD (required)")
	maxAge := fs.Int("max-age", 0, "calendar days older than the day the price may be; 0 means dated that day")
	at := fs.String("at", "", "only what had been received by this time (RFC 3339)")
	adj := fs.String("adjustment", "raw", "raw, split_adjusted, total_return or unknown")
	strict := fs.Bool("require-stated-unit", false, "refuse a price whose unit the source did not state")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *day == "" {
		return usageErrorf("usage: prices at (-mic M -ticker T | -isin I [-mic M]) -day YYYY-MM-DD [-max-age N] [-at TIME] [-adjustment A] [-require-stated-unit] [-json]")
	}
	if *maxAge < 0 {
		return usageErrorf("-max-age must not be negative")
	}
	known, err := parseAt(*at)
	if err != nil {
		return err
	}
	reg, err := loadRegistry(ctx, a)
	if err != nil {
		return err
	}
	res := reg.ResolveListing(instrument.Query{ISIN: *isin, MIC: *mic, Ticker: *ticker, Day: *day})
	if res.Status != instrument.Resolved {
		writeResolution(a, res)
		return fmt.Errorf("the instrument is %s: name it exactly (instruments resolve shows how)", res.Status)
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	obs, err := db.PriceObservations(ctx, res.Listing.ID, known)
	if err != nil {
		return err
	}
	ans := quotes.ResolvePrice(obs, *res.Listing, quotes.Need{Day: *day, KnownAt: known, MaxAgeDays: *maxAge, Adjustment: quotes.Adjustment(*adj), RequireStatedUnit: *strict})
	if *asJSON {
		if err := writeJSON(a.stdout, ans); err != nil {
			return err
		}
	} else {
		st := cliRed
		if ans.Verdict == quotes.OK {
			st = cliGreen
		}
		fmt.Fprintln(a.stdout, styledText(string(ans.Verdict), st, useColor(a.stdout)))
		if ans.Price != nil {
			fmt.Fprintf(a.stdout, "%s %s on %s: %s %s   (%d day(s) old; %s; unit %s; %s)\n", res.Listing.MIC, res.Listing.Ticker, ans.Day, ans.Price.Value, ans.Price.Unit.Code, ans.AgeDays, ans.Delay, ans.UnitBasis, strings.Join(ans.Sources, ", "))
			if ans.Price.Unit.IsSubunit() {
				if m, err := ans.Price.Major(); err == nil {
					fmt.Fprintf(a.stdout, "  = %s %s\n", m, ans.Price.Unit.Currency)
				}
			}
		}
		for _, r := range ans.Reasons {
			fmt.Fprintf(a.stdout, "  %s\n", r)
		}
	}
	if ans.Verdict != quotes.OK {
		return fmt.Errorf("no usable price: %s", ans.Verdict)
	}
	return nil
}
