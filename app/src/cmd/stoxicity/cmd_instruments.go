package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/capability"
	"github.com/b070nd/stoxicity/app/src/instrument"
)

// cmdInstruments is the identity of what can be bought: an example file, a check
// of a file, an import into the shared base store, a list, and a resolver that
// answers "which instrument is this" with an explicit ambiguity rather than a
// guess. It reads no broker and sends no request.
func cmdInstruments(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: example | check | import | list | resolve")
	}
	switch args[0] {
	case "example":
		return instrumentsExample(a, args[1:])
	case "check":
		return instrumentsCheck(a, args[1:])
	case "import":
		return instrumentsImport(ctx, a, args[1:])
	case "list":
		return instrumentsList(ctx, a, args[1:])
	case "resolve":
		return instrumentsResolve(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (example, check, import, list, resolve)", args[0])
}

func readInstrumentFile(path string) (instrument.Set, error) {
	f, err := os.Open(path)
	if err != nil {
		return instrument.Set{}, err
	}
	defer f.Close()
	return instrument.Parse(f)
}

func instrumentsExample(a *app, args []string) error {
	fs := newFlags("instruments example")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	b, err := instrument.Marshal(instrument.ExampleSet())
	if err != nil {
		return err
	}
	fmt.Fprint(a.stdout, "# Invented fixtures, not a data source: the fund and its ISINs are made up, the\n"+
		"# real companies carry their real ISINs only to show the shape. Replace every\n"+
		"# value from a source you have checked. Then: stoxicity instruments check -file FILE.\n"+
		"# Each listing states its quote unit (GBp is pence, GBP is pounds) and its first day.\n")
	_, err = a.stdout.Write(b)
	return err
}

func instrumentsCheck(a *app, args []string) error {
	fs := newFlags("instruments check")
	file := fs.String("file", "", "the instrument file")
	asJSON := fs.Bool("json", false, "print the problems as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("usage: instruments check -file FILE [-json]")
	}
	set, err := readInstrumentFile(*file)
	if err != nil {
		return err
	}
	problems := instrument.Validate(set)
	if *asJSON {
		if err := writeJSON(a.stdout, map[string]any{"problems": problems, "valid": len(instrument.Errors(problems)) == 0}); err != nil {
			return err
		}
	} else {
		writeInstrumentProblems(a, problems)
		fmt.Fprintf(a.stdout, "%d issuers, %d securities, %d listings\n", len(set.Issuers), len(set.Securities), len(set.Listings))
	}
	if n := len(instrument.Errors(problems)); n > 0 {
		return fmt.Errorf("%d errors: the file would not be imported", n)
	}
	return nil
}

func writeInstrumentProblems(a *app, ps []instrument.Problem) {
	if len(ps) == 0 {
		fmt.Fprintln(a.stdout, "no problems")
		return
	}
	t := cliTable{Header: []string{"severity", "ref", "problem"}}
	for _, p := range ps {
		st := cliYellow
		if p.Severity == instrument.Error {
			st = cliRed
		}
		t.Rows = append(t.Rows, []cliCell{styled(string(p.Severity), st), plain(p.Ref), plain(p.Message)})
	}
	t.write(a.stdout, useColor(a.stdout))
}

func instrumentsImport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("instruments import")
	file := fs.String("file", "", "the instrument file")
	source := fs.String("source", "", "where these facts come from (a file name, a vendor, 'manual'); stored with every revision")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" || strings.TrimSpace(*source) == "" {
		return usageErrorf("usage: instruments import -file FILE -source TEXT [-json]")
	}
	set, err := readInstrumentFile(*file)
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	rep, err := db.PutInstruments(ctx, set, *source, a.now())
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, rep)
	}
	t := cliTable{Header: []string{"kind", "new", "revised", "unchanged"}, Right: []bool{false, true, true, true}}
	for _, r := range []struct {
		n string
		c instrument.Counts
	}{{"issuers", rep.Issuers}, {"securities", rep.Securities}, {"listings", rep.Listings}} {
		t.Rows = append(t.Rows, []cliCell{plain(r.n), plain(fmt.Sprint(r.c.New)), plain(fmt.Sprint(r.c.Revised)), plain(fmt.Sprint(r.c.Unchanged))})
	}
	t.write(a.stdout, useColor(a.stdout))
	for _, l := range rep.Linked {
		fmt.Fprintf(a.stdout, "linked to existing history: %s\n", l)
	}
	if len(rep.Problems) > 0 {
		fmt.Fprintln(a.stdout)
		writeInstrumentProblems(a, rep.Problems)
	}
	fmt.Fprintln(a.stdout, "\nImported facts are what the file says. Nothing here is verified against a source.")
	return nil
}

func parseAt(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return t, usageErrorf("-at: %v", err)
	}
	return t, nil
}

func instrumentsList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("instruments list")
	at := fs.String("at", "", "what was known at this time (RFC 3339); default now")
	asJSON := fs.Bool("json", false, "print the set as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	when, err := parseAt(*at)
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	set, err := db.InstrumentsAsOf(ctx, when)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, set)
	}
	if len(set.Listings) == 0 {
		fmt.Fprintln(a.stdout, "no instruments imported yet\nstart with: stoxicity instruments example")
		return nil
	}
	reg, problems, err := instrument.New(set)
	if err != nil {
		return err
	}
	secs := map[string]instrument.Security{}
	for _, s := range reg.Set().Securities {
		secs[s.ID] = s
	}
	t := cliTable{Header: []string{"venue", "ticker", "ccy", "quote unit", "from", "to", "isin", "kind", "class", "distribution", "security", "name"}}
	for _, l := range set.Listings {
		s := secs[l.SecurityID]
		t.Rows = append(t.Rows, []cliCell{styled(l.MIC, cliDim), styled(l.Ticker, cliStrong), plain(l.Currency), unitCell(l.QuoteUnit, l.Currency),
			plain(l.From), plain(l.To), plain(s.ISIN), plain(string(s.Kind)), plain(s.ShareClass), plain(string(s.Distribution)), styled(s.ID, cliDim), plain(s.Name)})
	}
	t.write(a.stdout, useColor(a.stdout))
	if len(problems) > 0 {
		fmt.Fprintln(a.stdout)
		writeInstrumentProblems(a, problems)
	}
	return nil
}

// unitCell highlights a quote unit that is not the currency itself, because that
// is where a factor of 100 hides.
func unitCell(unit, ccy string) cliCell {
	if unit != ccy {
		return styled(unit, cliYellow)
	}
	return plain(unit)
}

func instrumentsResolve(ctx context.Context, a *app, args []string) error {
	fs := newFlags("instruments resolve")
	q := instrument.Query{}
	fs.StringVar(&q.ISIN, "isin", "", "ISIN")
	fs.StringVar(&q.MIC, "mic", "", "venue (ISO 10383 MIC)")
	fs.StringVar(&q.Ticker, "ticker", "", "ticker on that venue")
	fs.StringVar(&q.Currency, "currency", "", "the currency the listing trades in")
	fs.StringVar(&q.Broker, "broker", "", "broker name, with -conid")
	fs.StringVar(&q.ConID, "conid", "", "the broker's contract ID")
	fs.StringVar(&q.Day, "day", "", "the day the answer is for, YYYY-MM-DD (required: tickers are reused)")
	security := fs.Bool("security", false, "resolve the security (the share class) rather than one listing")
	asJSON := fs.Bool("json", false, "print the result as JSON")
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
	set, err := db.InstrumentsAsOf(ctx, time.Time{})
	if err != nil {
		return err
	}
	reg, _, err := instrument.New(set)
	if err != nil {
		return err
	}
	var res instrument.Result
	if *security {
		res = reg.ResolveSecurity(q)
	} else {
		res = reg.ResolveListing(q)
	}
	if *asJSON {
		if err := writeJSON(a.stdout, res); err != nil {
			return err
		}
	} else {
		writeResolution(a, res)
	}
	if res.Status != instrument.Resolved {
		return fmt.Errorf("not resolved: %s", res.Status)
	}
	return nil
}

func writeResolution(a *app, r instrument.Result) {
	st := cliRed
	switch r.Status {
	case instrument.Resolved:
		st = cliGreen
	case instrument.Ambiguous:
		st = cliYellow
	}
	fmt.Fprintf(a.stdout, "%s\n", styledText(string(r.Status), st, useColor(a.stdout)))
	for _, x := range r.Reasons {
		fmt.Fprintf(a.stdout, "  %s\n", x)
	}
	var cands []instrument.Candidate
	switch {
	case r.Status == instrument.Resolved && r.Listing != nil:
		cands = []instrument.Candidate{{Listing: *r.Listing, Security: *r.Security, Issuer: *r.Issuer}}
	case r.Status == instrument.Resolved && r.Security != nil:
		fmt.Fprintf(a.stdout, "  %s (%s) %s %s\n", r.Security.Name, r.Security.ID, r.Security.ISIN, r.Security.ShareClass)
	default:
		cands = r.Candidates
	}
	if len(cands) == 0 {
		return
	}
	t := cliTable{Header: []string{"venue", "ticker", "ccy", "quote unit", "from", "to", "isin", "class", "distribution", "security", "issuer", "name"}}
	for _, c := range cands {
		t.Rows = append(t.Rows, []cliCell{styled(c.Listing.MIC, cliDim), styled(c.Listing.Ticker, cliStrong), plain(c.Listing.Currency), unitCell(c.Listing.QuoteUnit, c.Listing.Currency),
			plain(c.Listing.From), plain(c.Listing.To), plain(c.Security.ISIN), plain(c.Security.ShareClass), plain(string(c.Security.Distribution)), styled(c.Security.ID, cliDim), plain(c.Issuer.Name), plain(c.Security.Name)})
	}
	t.write(a.stdout, useColor(a.stdout))
}

func styledText(s string, st cliStyle, color bool) string {
	if code := cliCodes[st]; color && code != "" {
		return code + s + "\x1b[0m"
	}
	return s
}

// sourcesCapabilities answers what each source can supply, for which markets and
// instruments, at what cost and under which terms, and which of it this product
// can use now. Unknown stays unknown, and a paid source is off unless named.
func sourcesCapabilities(a *app, args []string) error {
	fs := newFlags("sources capabilities")
	data := fs.String("data", "", "limit to one data type: "+dataTypeList())
	market := fs.String("market", "", "with -data: the market (two-letter country) the question is about")
	kind := fs.String("kind", "", "with -data: stock, etf, ...")
	allow := fs.String("allow-paid", "", "comma-separated provider IDs the user allows to cost money")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	reg := capability.Default()
	opts := capability.Options{AllowPaid: map[string]bool{}, HasKey: func(env string) bool { return os.Getenv(env) != "" }}
	for _, id := range strings.Split(*allow, ",") {
		if id = strings.TrimSpace(id); id != "" {
			opts.AllowPaid[id] = true
		}
	}
	if *data == "" {
		if *market != "" || *kind != "" {
			return usageErrorf("-market and -kind need -data")
		}
		if *asJSON {
			return writeJSON(a.stdout, reg.Providers())
		}
		writeCatalogue(a, reg)
		return nil
	}
	dt := capability.DataType(*data)
	if !slicesContains(capability.DataTypes, dt) {
		return usageErrorf("unknown data type %q (%s)", *data, dataTypeList())
	}
	req := capability.Request{Data: dt, Market: strings.ToUpper(*market), Kind: strings.ToLower(*kind)}
	answers := reg.Coverage(req, opts)
	chosen := reg.Choose(req, opts)
	if *asJSON {
		return writeJSON(a.stdout, map[string]any{"request": req, "answers": answers, "chosen": chosen})
	}
	t := cliTable{Header: []string{"provider", "cost", "state", "why"}}
	for _, an := range answers {
		st := cliRed
		switch an.State {
		case capability.Usable:
			st = cliGreen
		case capability.NeedsKey, capability.NeedsAccount, capability.ImportOnly:
			st = cliYellow
		case capability.NotWired, capability.NoEntitlement, capability.PaidNotAllowed:
			st = cliDim
		}
		t.Rows = append(t.Rows, []cliCell{styled(an.Provider.ID, cliStrong), plain(string(an.Provider.Cost)), styled(string(an.State), st), plain(an.Reason)})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintf(a.stdout, "\nchosen: %s\n", chosen.Reason)
	if chosen.Provider != nil {
		fmt.Fprintf(a.stdout, "  %s (%s)\n", chosen.Provider.Name, chosen.Provider.ID)
	}
	return nil
}

func dataTypeList() string {
	names := make([]string, len(capability.DataTypes))
	for i, d := range capability.DataTypes {
		names[i] = string(d)
	}
	return strings.Join(names, ", ")
}

func slicesContains(ds []capability.DataType, d capability.DataType) bool {
	for _, x := range ds {
		if x == d {
			return true
		}
	}
	return false
}

func writeCatalogue(a *app, reg *capability.Registry) {
	t := cliTable{Header: []string{"provider", "cost", "key", "data", "markets", "instruments", "history", "delay", "currencies", "code", "rights", "verified"}}
	for _, p := range reg.Providers() {
		ver := "never"
		if p.Verified.At != "" {
			ver = p.Verified.At + " " + p.Verified.Result
		}
		costSt := cliGreen
		switch p.Cost {
		case capability.CostFreeKey, capability.CostAccount:
			costSt = cliYellow
		case capability.CostPaid:
			costSt = cliRed
		}
		if len(p.Entitlements) == 0 {
			t.Rows = append(t.Rows, []cliCell{styled(p.ID, cliStrong), styled(string(p.Cost), costSt), plain(p.KeyEnv), plain("-"), plain("-"), plain("-"), plain("-"), plain("-"), plain("-"), plain("-"), plain(p.Rights), plain(ver)})
			continue
		}
		for _, e := range p.Entitlements {
			code := styled("none", cliDim)
			if e.Wired {
				code = styled("wired", cliGreen)
			}
			t.Rows = append(t.Rows, []cliCell{styled(p.ID, cliStrong), styled(string(p.Cost), costSt), plain(p.KeyEnv), plain(string(e.Data)),
				plain(strings.Join(e.Markets, ",")), plain(strings.Join(e.Instruments, ",")), plain(e.History), plain(e.Delay), plain(e.Currencies), code, plain(p.Rights), plain(ver)})
		}
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintln(a.stdout, "\nunknown means not checked. Nothing here is verified against a provider; a paid provider is off unless named with -allow-paid.")
}
