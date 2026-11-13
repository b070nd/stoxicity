package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

const (
	defaultIdentityRequests = 25
	// identityPlanLines is how many requests -plan lists before it only counts.
	identityPlanLines = 100
	// identityMassiveInformation is said once a run makes requests: what the
	// provider's licence makes of the rows it brings.
	identityMassiveInformation = "Rows from Massive are its Information under a personal-use licence: for your own use, not to be shared, to be deleted if the licence ends; " +
		"their source column says massive:... until a person verifies them against a document."
)

// cmdIdentity is the history of which symbols were one security's: renames,
// mergers and delistings, as imported from a document and checked by a person.
func cmdIdentity(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: import | list | show | fetch | verify")
	}
	switch args[0] {
	case "import":
		return identityImport(ctx, a, args[1:])
	case "list":
		return identityList(ctx, a, args[1:])
	case "show":
		return identityShow(ctx, a, args[1:])
	case "fetch":
		return identityFetch(ctx, a, args[1:])
	case "verify":
		return identityVerify(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (import, list, show, fetch, verify)", args[0])
}

// The reasons a CSV row cannot be read as a span, besides those of
// marketdata.SymbolSpan.Validate.
const (
	identityBadVerified = "verified is not 0 or 1"
	identityBadRow      = "the row has more fields than the header"
)

// identityColumns are the columns of the import file and how many of them,
// from the first, must be in the header; the rest may be left out.
func identityColumns() (names []string, required int) {
	return []string{"security", "symbol", "valid_from", "source", "valid_to", "kind", "successor", "source_ref", "verified", "note"}, 4
}

// readSymbolSpans reads the import file. The first record is the header, with
// the column names in any order. A row that cannot be read is counted by its
// reason in rejected and left out; the source of a row that gives none is
// "import:" and the file name.
func readSymbolSpans(r io.Reader, file string) (spans []marketdata.SymbolSpan, rejected map[string]int, err error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("identity: reading the header: %w", err)
	}
	names, required := identityColumns()
	col := map[string]int{}
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		known := false
		for _, c := range names {
			known = known || c == h
		}
		if !known {
			return nil, nil, fmt.Errorf("identity: unknown column %q (known: %s)", h, strings.Join(names, ", "))
		}
		if _, dup := col[h]; dup {
			return nil, nil, fmt.Errorf("identity: column %q appears twice", h)
		}
		col[h] = i
	}
	for _, c := range names[:required] {
		if _, ok := col[c]; !ok {
			return nil, nil, fmt.Errorf("identity: the header has no %q column", c)
		}
	}
	rejected = map[string]int{}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("identity: %w", err)
		}
		if len(rec) > len(header) {
			rejected[identityBadRow]++
			continue
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		s := marketdata.SymbolSpan{
			Security: get("security"), Symbol: get("symbol"), From: get("valid_from"), To: get("valid_to"),
			Kind: marketdata.SpanKind(get("kind")), Successor: get("successor"), Source: get("source"),
			SourceRef: get("source_ref"), Note: get("note"),
		}
		switch get("verified") {
		case "", "0":
		case "1":
			s.Verified = true
		default:
			rejected[identityBadVerified]++
			continue
		}
		s = s.Normalized()
		if s.Source == "" {
			s.Source = "import:" + filepath.Base(file)
		}
		if err := s.Validate(); err != nil {
			rejected[strings.TrimPrefix(err.Error(), "marketdata: symbol span: ")]++
			continue
		}
		spans = append(spans, s)
	}
	return spans, rejected, nil
}

func identityImport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("identity import")
	file := fs.String("file", "", "CSV: security, symbol, valid_from, source (required); valid_to, kind, successor, source_ref, verified, note")
	dry := fs.Bool("dry-run", false, "read and check the file, store nothing")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("usage: identity import -file FILE [-dry-run]")
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	spans, rejected, err := readSymbolSpans(f, *file)
	if err != nil {
		return err
	}
	total := len(spans)
	for _, n := range rejected {
		total += n
	}
	fmt.Fprintf(a.stdout, "%d rows read, %d usable, %d rejected\n", total, len(spans), total-len(spans))
	reasons := make([]string, 0, len(rejected))
	for r := range rejected {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(a.stdout, "  rejected %d: %s\n", rejected[r], r)
	}
	if *dry {
		fmt.Fprintln(a.stdout, "dry run: nothing stored")
		return nil
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	written, err := db.PutSymbolHistory(ctx, spans, a.now())
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%d new revisions stored, %d already stored\n", written, len(spans)-written)
	fmt.Fprintln(a.stdout, "Only verified rows join tickers into one price series; the rest are kept as information.")
	return nil
}

func identityVerified(v bool) cliCell {
	if v {
		return styled("yes", cliGreen)
	}
	return styled("no", cliYellow)
}

func identitySpanTable(spans []marketdata.SymbolSpan) cliTable {
	t := cliTable{Header: []string{"SECURITY", "SYMBOL", "FROM", "TO", "KIND", "SUCCESSOR", "VERIFIED", "SOURCE", "SOURCE REF"}}
	for _, s := range spans {
		t.Rows = append(t.Rows, []cliCell{plain(s.Security), plain(s.Symbol), plain(s.From), plain(s.To), plain(string(s.Kind)),
			plain(s.Successor), identityVerified(s.Verified), plain(s.Source), plain(s.SourceRef)})
	}
	return t
}

func identityList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("identity list")
	symbol := fs.String("symbol", "", "only spans of this ticker")
	unverified := fs.Bool("unverified", false, "only spans that are not verified")
	at := fs.String("at", "", "read the history as recorded by this time (RFC 3339)")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("identity list takes no arguments, got %q", rest[0])
	}
	var asOf time.Time
	if *at != "" {
		if asOf, err = time.Parse(time.RFC3339, *at); err != nil {
			return usageErrorf("-at %q is not an RFC 3339 time such as 2026-11-03T10:00:00Z", *at)
		}
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	all, err := db.SymbolHistory(ctx, asOf)
	if err != nil {
		return err
	}
	var spans []marketdata.SymbolSpan
	for _, s := range all {
		if (*symbol == "" || marketdata.SameTicker(s.Symbol, *symbol)) && (!*unverified || !s.Verified) {
			spans = append(spans, s)
		}
	}
	if *asJSON {
		return writeJSON(a.stdout, spans)
	}
	if len(spans) == 0 {
		fmt.Fprintln(a.stdout, "no symbol spans stored")
		return nil
	}
	identitySpanTable(spans).write(a.stdout, useColor(a.stdout))
	return nil
}

func identityShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("identity show")
	day := fs.String("day", "", "the day the ticker is read on, YYYY-MM-DD (default: its newest span)")
	at := fs.String("at", "", "read the history as recorded by this time (RFC 3339)")
	asJSON := fs.Bool("json", false, "print as JSON")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: identity show SYMBOL [-day DAY] [-at TIME] [-json]")
	}
	if *day != "" {
		if _, err := parseDay(*day, a.location()); err != nil {
			return usageErrorf("-day: %v", err)
		}
	}
	var asOf time.Time
	if *at != "" {
		if asOf, err = time.Parse(time.RFC3339, *at); err != nil {
			return usageErrorf("-at %q is not an RFC 3339 time such as 2026-11-03T10:00:00Z", *at)
		}
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	all, err := db.SymbolHistory(ctx, asOf)
	if err != nil {
		return err
	}
	l := marketdata.Chain(all, pos[0], *day)
	if *asJSON {
		return writeJSON(a.stdout, map[string]any{"symbol": strings.ToUpper(pos[0]), "lineage": l, "links": l.Links()})
	}
	sym := strings.ToUpper(strings.TrimSpace(pos[0]))
	if l.Security == "" {
		fmt.Fprintf(a.stdout, "no verified span names %s, so it is read as the one ticker it is\n", sym)
	} else {
		fmt.Fprintf(a.stdout, "%s belongs to security %s: %d symbol(s) make one series\n", sym, l.Security, len(l.Spans))
		identitySpanTable(l.Spans).write(a.stdout, useColor(a.stdout))
		if l.EndedOn != "" {
			fmt.Fprintf(a.stdout, "the series ends on %s (%s); a study should expect no later prices\n", l.EndedOn, l.EndKind)
		}
	}
	if len(l.Ignored) > 0 {
		fmt.Fprintf(a.stdout, "\n%d span(s) ignored because they are not verified (information only, they join nothing):\n", len(l.Ignored))
		identitySpanTable(l.Ignored).write(a.stdout, useColor(a.stdout))
	}
	return nil
}

// identityFetch asks the provider for candidate spans: the ticker changes of the
// named or stored tickers, or the delistings of tickers no longer active. A
// candidate is stored unverified, so it joins nothing until identity verify.
func identityFetch(ctx context.Context, a *app, args []string) error {
	fs := newFlags("identity fetch")
	symbols := fs.String("symbols", "", "tickers (or composite FIGIs) to ask about, comma-separated")
	fromUniverse := fs.Bool("from-universe", false, "ask about every ticker of the stored universe")
	delisted := fs.Bool("delisted", false, "ask for delistings instead of ticker changes: the overview of each of -symbols, or, with -from-universe, "+
		"the provider's whole list of tickers no longer active (a page is one request) and the overview of each")
	after := fs.String("after", "", "only tickers after this one, to continue a run that the request budget ended")
	maxN := fs.Int("max", defaultIdentityRequests, "most requests to make; 0 means no limit")
	pace := fs.Duration("pace", 0, "wait between two requests, for a plan that allows few requests a minute")
	plan := fs.Bool("plan", false, "print what would be requested and make no request")
	yes := fs.Bool("yes", false, "make the requests: they can be many, so say -plan first")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	switch {
	case len(rest) != 0:
		return usageErrorf("unexpected argument %q", rest[0])
	case (*symbols == "") == !*fromUniverse:
		return usageErrorf("name what to ask about: -symbols or -from-universe, not both")
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
			return fmt.Errorf("ticker history comes from Massive (Polygon), which needs a key of your own: set STOXICITY_POLYGON_KEY or MASSIVE_API_KEY (in .env.local)")
		}
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	tickers := splitList(*symbols, true)
	if *fromUniverse && !*delisted {
		refs, err := db.RefTickers(ctx)
		if err != nil {
			return err
		}
		for _, r := range refs {
			tickers = append(tickers, r.Ticker)
		}
		if len(tickers) == 0 {
			return fmt.Errorf("the stored universe is empty: refresh the reference data first")
		}
	}
	p := marketdata.NewIdentityPlan(tickers, *delisted, *after)
	if *fromUniverse && *delisted {
		p = marketdata.IdentityPlan{ListInactive: true, After: p.After}
	}
	budget := *maxN
	if *plan {
		a.printIdentityPlan(p, budget)
		return nil
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	c := a.cfg.Company
	ref := ""
	if c.QuotesURL != "" {
		ref = c.QuotesURL + "identity/" // an overridden address (a test) holds all of them
	}
	k := &marketdata.IdentityKeeper{Store: db, Feed: marketdata.MassiveIdentity{Client: client, Key: c.PolygonKey, URL: ref}, Now: a.now, Pace: *pace}
	fmt.Fprintln(a.stdout, identityMassiveInformation)
	rep, err := k.Fetch(ctx, p, budget)
	fmt.Fprintf(a.stdout, "identity candidates (%s)  %s\n", k.Feed.Name(), rep)
	if rep.Left > 0 && rep.Last != "" {
		fmt.Fprintf(a.stdout, "to continue: -after %s\n", rep.Last)
	}
	return err
}

// printIdentityPlan says what a fetch would request, and under which request
// budget.
func (a *app) printIdentityPlan(p marketdata.IdentityPlan, budget int) {
	fmt.Fprintln(a.stdout, "identity plan  candidate spans from massive, all unverified")
	if p.ListInactive {
		fmt.Fprintf(a.stdout, "list           tickers no longer active, from the start of the list (after %q), 1000 a page; then the overview of each, "+
			"which cannot be counted until the list is read\n", p.After)
	}
	if budget == 0 {
		fmt.Fprintf(a.stdout, "requests       %d named, none held back\n", len(p.Requests))
	} else {
		fmt.Fprintf(a.stdout, "requests       %d named, of which %d would be made now (-max %d)\n", len(p.Requests), min(budget, len(p.Requests)), budget)
	}
	if q, ok := a.cfg.Fetch.Quotas[intradayQuotaKey]; ok {
		fmt.Fprintf(a.stdout, "request budget %d per %s (fetch.quotas.%s); a request over it stops the run\n", q.Requests, q.Period, intradayQuotaKey)
	} else {
		fmt.Fprintf(a.stdout, "request budget none configured for %s (fetch.quotas); the plan's own limit decides\n", intradayQuotaKey)
	}
	for i, r := range p.Requests {
		if i >= identityPlanLines {
			fmt.Fprintf(a.stdout, "... and %d more\n", len(p.Requests)-i)
			break
		}
		fmt.Fprintf(a.stdout, "%-8s %s\n", r.Ticker, r.Ask)
	}
}

// identityVerify writes a new revision of a candidate span that says a person
// checked it against the document given. The store keeps the earlier revision.
func identityVerify(ctx context.Context, a *app, args []string) error {
	fs := newFlags("identity verify")
	security := fs.String("security", "", "the key of the security of the span (required)")
	symbol := fs.String("symbol", "", "the symbol of the span (required)")
	from := fs.String("from", "", "the first day of the span, YYYY-MM-DD (required)")
	ref := fs.String("source-ref", "", "the document that states it: an EDGAR accession number or an address (required)")
	note := fs.String("note", "", "text added to the span's note")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || strings.TrimSpace(*security) == "" || strings.TrimSpace(*symbol) == "" || strings.TrimSpace(*ref) == "" {
		return usageErrorf("usage: identity verify -security S -symbol X -from DAY -source-ref REF [-note TEXT]")
	}
	if _, err := parseDay(*from, a.location()); err != nil {
		return usageErrorf("-from: %v", err)
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	all, err := db.SymbolHistory(ctx, time.Time{})
	if err != nil {
		return err
	}
	want := marketdata.SymbolSpan{Security: *security, Symbol: *symbol, From: *from}.Normalized()
	for _, s := range all {
		if s.Security != want.Security || s.From != want.From || !marketdata.SameTicker(s.Symbol, want.Symbol) {
			continue
		}
		if s.Verified {
			return fmt.Errorf("the span of %s as %s from %s is already verified (source_ref %s)", s.Security, s.Symbol, s.From, s.SourceRef)
		}
		s.Verified, s.SourceRef, s.Source = true, strings.TrimSpace(*ref), "verified:"+s.Source
		if t := strings.TrimSpace(*note); t != "" {
			if s.Note != "" {
				t = s.Note + "; " + t
			}
			s.Note = t
		}
		if _, err := db.PutSymbolHistory(ctx, []marketdata.SymbolSpan{s}, a.now()); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "verified %s as %s from %s against %s; the earlier revision is kept\n", s.Security, s.Symbol, s.From, s.SourceRef)
		return nil
	}
	return fmt.Errorf("no candidate span of %s as %s from %s is stored (see identity list -symbol %s)", want.Security, want.Symbol, want.From, want.Symbol)
}
