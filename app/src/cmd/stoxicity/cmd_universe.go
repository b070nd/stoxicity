package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/insider"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/stockfeeds"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/universe"
	"github.com/b070nd/stoxicity/app/src/valuation"
)

const (
	defaultUniverseLimit = 25
	defaultNewsDays      = 14
	// enrichNewsDays is how far back "universe enrich" looks for tickers that
	// are in the news.
	enrichNewsDays   = 30
	defaultEnrichMax = 300
)

const universeUsage = "expected: status | update [-force] | changes [-days N] [-limit N] | enrich [-all] [-max N] [-force] [-sites=false] [-feeds N] [TICKER...] | feeds [-search N] | quotes [-max N] [TICKER...] | fundamentals [-force] | fundamentals history [-at TIME] [-stored] TICKER | fundamentals classes [-stored] [-at TIME [-basis known|reconstructed]] TICKER | fundamentals coverage [-list STATE,...|all] [-followed] | insiders [-max N] [TICKER] | find [-limit N] [-days N] [-format table|tsv|json] QUERY... | show TICKER | tag TICKER [-]TAG... | list [ID] | list add|remove ID TICKER..."

// cmdUniverse is the reference list of stocks: every company the program
// knows, searched, tagged and grouped.
func cmdUniverse(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("%s | %s", universeUsage, universeHistoryUsage)
	}
	switch args[0] {
	case "status":
		if len(args) > 1 {
			return usageErrorf("status takes no arguments")
		}
		return a.universeStatus()
	case "update":
		return a.universeUpdate(ctx, args[1:])
	case "changes":
		return a.universeChanges(args[1:])
	case "enrich":
		return a.universeEnrich(ctx, args[1:])
	case "feeds":
		return a.universeFeeds(ctx, args[1:])
	case "quotes":
		return a.universeQuotes(ctx, args[1:])
	case "history":
		return a.universeHistory(ctx, args[1:])
	case "fundamentals":
		return a.universeFundamentals(ctx, args[1:])
	case "insiders":
		return a.universeInsiders(ctx, args[1:])
	case "find":
		return a.universeFind(ctx, args[1:])
	case "show":
		return a.universeShow(ctx, args[1:])
	case "tag":
		return a.universeTag(args[1:])
	case "list":
		return a.universeList(args[1:])
	}
	return usageErrorf("unknown subcommand %q; %s | %s", args[0], universeUsage, universeHistoryUsage)
}

func (a *app) universeStatus() error {
	u := a.universe()
	db, err := a.store()
	if err != nil {
		return err
	}
	ctx := context.Background()
	fmt.Fprintf(a.stdout, "database    %s (reference tables)\n", a.relative(filepath.Join(a.dataDir(), "base.db")))
	fmt.Fprintf(a.stdout, "downloads   %s\n", a.companyDir())
	fmt.Fprintf(a.stdout, "layer       %s\n", a.relative(a.layerPath()))
	srcs, err := db.RefSources(ctx)
	if err != nil {
		return err
	}
	byName := map[string]store.RefSource{}
	for _, s := range srcs {
		byName[s.Name] = s
	}
	line := func(label, name, unit, none string) {
		s, ok := byName[name]
		if !ok || s.Hash == "" {
			fmt.Fprintf(a.stdout, "%-11s %s\n", label, none)
			if ok && s.Error != "" {
				fmt.Fprintf(a.stdout, "%-11s last refresh failed: %s\n", "", s.Error)
			}
			return
		}
		fmt.Fprintf(a.stdout, "%-11s %d %s, content %s, checked %s, changed %s\n", label, s.Records, unit,
			s.Hash[:8], a.ago(s.CheckedAt), a.ago(s.ChangedAt))
		if s.Error != "" {
			fmt.Fprintf(a.stdout, "%-11s last refresh failed: %s\n", "", s.Error)
		}
	}
	line("companies", store.SourceTickers, "tickers", "none yet: run \"stoxicity universe update\"")
	if a.cfg.Company.Listing {
		line("directory", store.SourceNasdaq, "symbols", "none yet: run \"stoxicity universe update\"")
	} else {
		fmt.Fprintln(a.stdout, "directory   off (company.listing): ETFs cannot be told from stocks")
	}
	rows, err := db.RefCompanies(ctx)
	if err != nil {
		return err
	}
	withData := 0
	for _, c := range rows {
		if !c.Missing {
			withData++
		}
	}
	tickers, _ := db.RefTickers(ctx)
	fmt.Fprintf(a.stdout, "industry    %d of %d companies have an SEC record (the background refresh of \"run\" and \"stoxicity universe enrich\" add more)\n",
		withData, len(distinctCIKs(tickers)))
	for _, l := range a.coverage(ctx, db) {
		fmt.Fprintln(a.stdout, strings.Replace(l, "coverage      ", "coverage    ", 1))
	}
	fmt.Fprintf(a.stdout, "lists       %d, tags %d (tenant %s)\n", len(u.Lists()), len(u.Tags()), a.tenantID)
	fmt.Fprintf(a.stdout, "universe    %d profiles, fingerprint %s\n", u.Len(), shortHash(u.Fingerprint()))
	since := a.now().AddDate(0, 0, -7)
	if ch, err := db.RefChanges(ctx, since, 1000); err == nil && len(ch) > 0 {
		var added, removed, changed int
		for _, c := range ch {
			switch c.Kind {
			case "added":
				added++
			case "removed":
				removed++
			default:
				changed++
			}
		}
		fmt.Fprintf(a.stdout, "changes     last 7 days: %d new, %d gone, %d changed (\"stoxicity universe changes\")\n", added, removed, changed)
	}
	return nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func distinctCIKs(ts []store.RefTicker) map[string]bool {
	m := map[string]bool{}
	for _, t := range ts {
		if t.CIK != "" {
			m[t.CIK] = true
		}
	}
	return m
}

// universeChanges lists what the background refresh found: tickers that
// appeared, went or changed.
func (a *app) universeChanges(args []string) error {
	fs := newFlags("universe changes")
	days := fs.Int("days", 7, "how many days back")
	limit := fs.Int("limit", 50, "most rows to print")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *days < 1 || *limit < 1 {
		return usageErrorf("expected [-days N] [-limit N]")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	ch, err := db.RefChanges(context.Background(), a.now().AddDate(0, 0, -*days), *limit)
	if err != nil {
		return err
	}
	if len(ch) == 0 {
		fmt.Fprintln(a.stdout, "no changes")
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WHEN\tSOURCE\tKIND\tKEY\tDETAIL")
	for _, c := range ch {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.At.In(a.location()).Format("2006-01-02 15:04"), c.Source, c.Kind, c.Key, c.Detail)
	}
	return tw.Flush()
}

// ago writes how long ago t was, or "never".
func (a *app) ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return fmt.Sprintf("%s (%s ago)", t.In(a.location()).Format("2006-01-02 15:04"), a.now().Sub(t).Round(time.Hour))
}

func (a *app) universeUpdate(ctx context.Context, args []string) error {
	fs := newFlags("universe update")
	force := fs.Bool("force", false, "ask even if the cached files are young")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	res, err := a.refreshCompanies(ctx, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "SEC list      %s\n", describe(res))
	if !a.cfg.Company.Listing {
		fmt.Fprintln(a.stdout, "symbol files  off (company.listing)")
		return nil
	}
	res, err = a.refreshListing(ctx, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "symbol files  %s\n", describe(res))
	return nil
}

func describe(r company.Result) string {
	switch {
	case r.Skipped:
		return fmt.Sprintf("young enough, %d entries (use -force to ask anyway)", r.Records)
	case r.Updated:
		return fmt.Sprintf("new copy stored, %d entries", r.Records)
	}
	return fmt.Sprintf("unchanged, %d entries", r.Records)
}

// interestingTickers are the tickers worth an industry record: those you
// follow, those in your lists and tags, and those in relevant news lately. It
// reads the layer file and the database afresh and uses only what it is given,
// so the background refresh can call it while the command goes on.
func interestingTickers(ctx context.Context, db *store.DB, known []string, layerPath string, threshold float64, now time.Time) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t = universe.Ticker(t); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range known {
		add(t)
	}
	if layer, err := universe.LoadLayer(layerPath); err == nil {
		for _, l := range layer.Lists {
			for _, t := range l.Tickers {
				add(t)
			}
		}
		for t, tags := range layer.Tags {
			if len(tags) > 0 {
				add(t)
			}
		}
	}
	if arts, err := db.Range(ctx, now.AddDate(0, 0, -enrichNewsDays), now.Add(time.Hour)); err == nil {
		for t := range universe.FactsFrom(arts, threshold, nil).News {
			add(t)
		}
	}
	slices.Sort(out)
	return out
}

func (a *app) universeFind(ctx context.Context, args []string) error {
	fs := newFlags("universe find")
	limit := fs.Int("limit", defaultUniverseLimit, "most rows to print; 0 prints every match")
	days := fs.Int("days", defaultNewsDays, "days of news that news: counts")
	format := fs.String("format", "table", "table, tsv (tab-separated, with a header) or json (every field)")
	words, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if *limit < 0 || *days < 1 {
		return usageErrorf("-limit must be 0 or more and -days at least 1")
	}
	if !slices.Contains([]string{"table", "tsv", "json"}, *format) {
		return usageErrorf("-format must be table, tsv or json")
	}
	q, err := universe.ParseQuery(strings.Join(words, " "))
	if err != nil {
		return usageErrorf("%v", err)
	}
	u := a.universe()
	if a.companies().Empty() {
		return fmt.Errorf("no company list yet: run \"stoxicity universe update\"")
	}
	facts := a.facts(ctx, *days)
	hits := u.Find(q, facts, 0)
	shown := hits
	if *limit > 0 && len(shown) > *limit {
		shown = shown[:*limit]
	}
	switch *format {
	case "json":
		return writeHitsJSON(a.stdout, shown)
	case "tsv":
		return writeHitsTSV(a.stdout, shown, facts)
	}
	if len(hits) == 0 {
		fmt.Fprintln(a.stdout, "no company matches")
		return nil
	}
	t := cliTable{Header: []string{"TICKER", "EXCHANGE", "KIND", "INDUSTRY", "NEWS", "LISTS", "TAGS", "NAME"}, Right: []bool{false, false, false, false, true}}
	for _, h := range shown {
		p := h.Profile
		news := styled(fmt.Sprint(h.News), cliDim)
		if h.News > 0 {
			news = styled(fmt.Sprint(h.News), cliGreen)
		}
		t.Rows = append(t.Rows, []cliCell{styled(p.Ticker, cliStrong), plain(dash(p.Exchange)), styled(dash(p.Kind), kindLight(p.Kind)),
			plain(dash(p.Industry)), news, plain(dash(strings.Join(p.Lists, ","))), plain(dash(strings.Join(p.Tags, ","))), plain(p.Name)})
	}
	t.write(a.stdout, useColor(a.stdout))
	if len(hits) > len(shown) {
		fmt.Fprintf(a.stdout, "%d of %d shown; narrow the query, raise -limit or use -limit 0 for all\n", len(shown), len(hits))
	}
	return nil
}

// writeHitsTSV prints the matches as tab-separated values with a header line,
// every field complete (no truncation), for a spreadsheet or a script.
func writeHitsTSV(w io.Writer, hits []universe.Hit, f universe.Facts) error {
	cell := func(s string) string { return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s) }
	fmt.Fprintln(w, "ticker\tname\tcik\texchange\tvenue\tkind\tmarket\tstatus\tsic\tindustry\tsector\tstate\tfiscal_year\twebsite\tnews\tlists\ttags\tclose\tchange\tmarketcap\tclasscap\tissuercap\trevenue\tincome\tgrowth\tmargin\troe\tbuys\tsells")
	for _, h := range hits {
		p := h.Profile
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s",
			p.Ticker, cell(p.Name), p.CIK, p.Exchange, p.Venue, p.Kind, p.Market, p.Status, p.SIC, cell(p.Industry), cell(p.Sector),
			p.State, p.FiscalYear, cell(p.Website), h.News, strings.Join(p.Lists, ","), strings.Join(p.Tags, ","))
		for _, op := range []string{"close", "change", "marketcap", "classcap", "issuercap", "revenue", "income", "growth", "margin", "roe", "buys", "sells"} {
			if v, ok := figure(f, op, p.Ticker); ok {
				fmt.Fprintf(w, "\t%s", strconv.FormatFloat(v, 'g', 8, 64))
			} else {
				fmt.Fprint(w, "\t")
			}
		}
		fmt.Fprintln(w)
	}
	return nil
}

// figure is a number of the facts: the last close or move, or one of the
// SEC's figures.
func figure(f universe.Facts, op, ticker string) (float64, bool) {
	switch op {
	case "close":
		v, ok := f.Close[ticker]
		return v, ok
	case "change":
		v, ok := f.Change[ticker]
		return v, ok
	}
	v, ok := f.Num[op][ticker]
	return v, ok
}

// writeHitsJSON prints the matches as a JSON array, one object per company.
func writeHitsJSON(w io.Writer, hits []universe.Hit) error {
	type row struct {
		universe.Profile
		News int `json:"news"`
	}
	out := make([]row, len(hits))
	for i, h := range hits {
		out[i] = row{Profile: *h.Profile, News: h.News}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// facts counts news per ticker over the last days, for news: and ranking. If
// the database cannot be read the counts are empty.
func (a *app) facts(ctx context.Context, days int) universe.Facts {
	rd, err := a.reader()
	if err != nil {
		return universe.Facts{}
	}
	now := a.now()
	arts, err := rd.Range(ctx, now.AddDate(0, 0, -days), now.Add(time.Hour))
	if err != nil {
		return universe.Facts{}
	}
	f := universe.FactsFrom(arts, a.cfg.Lens.Threshold, a.knownTickers())
	a.addFigures(ctx, &f)
	return f
}

// addFigures gives the facts the prices and the SEC's figures, for close:,
// marketcap:, revenue: and the rest. What is not stored is left out.
func (a *app) addFigures(ctx context.Context, f *universe.Facts) {
	db, err := a.store()
	if err != nil {
		return
	}
	f.Close, f.Change = map[string]float64{}, map[string]float64{}
	if latest, err := db.LatestCandles(ctx); err == nil {
		splits, _ := db.Splits(ctx)
		cov, _ := db.SplitCoverage(ctx)
		bySplit := map[string][]marketdata.Split{}
		for _, s := range splits {
			k := marketdata.NormTicker(s.Ticker)
			bySplit[k] = append(bySplit[k], s)
		}
		for t, c := range latest {
			if len(c) == 0 {
				continue
			}
			f.Close[t] = c[0].Close
			if len(c) > 1 {
				if m := marketdata.Compare(c[1], c[0], bySplit[marketdata.NormTicker(t)], cov); m.Shown() {
					f.Change[t] = m.Change
				}
			}
		}
	}
	if facts, err := db.Fundamentals(ctx); err == nil {
		f.Num = fundamentals.Figures(fundamentals.Summarize(facts), a.universe().All())
	}
	// Market values are class aware: see package valuation.
	if set, err := valuation.Build(ctx, db, a.universe().All(), valuation.View{}, a.now()); err == nil {
		nums, why := set.Figures(a.universe().All())
		if f.Num == nil {
			f.Num = map[string]map[string]float64{}
		}
		for name, byTicker := range nums {
			f.Num[name] = byTicker
		}
		f.Why = why
	}
	if trades, err := db.InsiderTrades(ctx, a.now().AddDate(0, 0, -insider.FigureDays).Format(time.DateOnly), "", 0); err == nil {
		if f.Num == nil {
			f.Num = map[string]map[string]float64{}
		}
		for name, byTicker := range insider.Figures(insider.Summarize(trades, a.now()), a.universe().All()) {
			f.Num[name] = byTicker
		}
	}
}

func (a *app) universeShow(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return usageErrorf("show takes one ticker")
	}
	u := a.universe()
	p, ok := u.Get(args[0])
	if !ok {
		if a.companies().Empty() {
			return fmt.Errorf("no company list yet: run \"stoxicity universe update\"")
		}
		return fmt.Errorf("%s is not in the company list; try: stoxicity universe find %s", strings.ToUpper(args[0]), args[0])
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(tw, "%s\t%s\n", k, v)
		}
	}
	row("ticker", p.Ticker)
	row("name", p.Name)
	row("also known as", strings.Join(p.Aliases, "; "))
	row("CIK", p.CIK)
	row("exchange", strings.TrimSpace(p.Exchange+" "+venueNote(p)))
	row("kind", p.Kind)
	row("market", p.Market)
	row("status", p.Status)
	if p.SIC != "" {
		row("industry", p.Industry+" (SIC "+p.SIC+")")
	}
	row("incorporated", p.State)
	row("fiscal year end", p.FiscalYear)
	row("filer category", p.Category)
	row("headquarters", p.HQ)
	row("phone", p.Phone)
	row("website", p.Website)
	row("investors", p.Investor)
	row("EIN", p.EIN)
	row("LEI", p.LEI)
	row("ISIN", p.ISIN)
	row("sector", p.Sector)
	row("same company", strings.Join(p.Siblings, ", "))
	row("lists", strings.Join(p.Lists, ", "))
	row("tags", strings.Join(p.Tags, ", "))
	var following []string
	if ws, err := a.watches(a.tenantID).List(); err == nil {
		for _, w := range ws {
			if slices.ContainsFunc(w.Scope.Tickers, func(t string) bool { return universe.Ticker(t) == p.Ticker }) {
				following = append(following, w.ID)
			}
		}
	}
	row("followed by", strings.Join(following, ", "))
	row("source", p.Source)
	if p.CIK != "" {
		row("EDGAR", "https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&CIK="+p.CIK)
	}
	facts := a.facts(ctx, defaultNewsDays)
	if n := facts.News[p.Ticker]; n > 0 {
		row("news", fmt.Sprintf("%d relevant articles in %d days (stoxicity stock %s)", n, defaultNewsDays, p.Ticker))
	}
	if p.CIK != "" && (p.Type() == universe.TypeStock || p.Type() == universe.TypeADR) {
		for _, f := range []struct{ name, key string }{{"market cap", "marketcap"}, {"class cap", "classcap"}, {"issuer cap", "issuercap"}} {
			if v, ok := facts.Num[f.key][p.Ticker]; ok {
				row(f.name, moneyText(v)+" USD")
			} else if why := facts.Why[f.key][p.Ticker]; why != "" {
				row(f.name, "unavailable: "+why)
			}
		}
	}
	if db, err := a.store(); err == nil {
		if feeds, err := db.StockFeedsOf(ctx, p.Ticker); err == nil {
			for _, f := range feeds {
				row("feed ("+f.Kind+")", f.URL)
			}
		}
	}
	if p.SIC == "" && p.CIK != "" || p.Website == "" && p.Kind == universe.KindStock {
		row("hint", "not filled in yet: stoxicity universe enrich "+p.Ticker)
	}
	return tw.Flush()
}

func venueNote(p universe.Profile) string {
	if p.Venue != "" && !strings.EqualFold(p.Venue, p.Exchange) {
		return "(" + p.Venue + ")"
	}
	return ""
}

// universeTag adds tags to a stock, or removes those written with a leading -.
func (a *app) universeTag(args []string) error {
	if len(args) < 2 {
		return usageErrorf("tag needs a ticker and at least one tag (a leading - removes)")
	}
	u := a.universe()
	p, ok := u.Get(args[0])
	ticker := universe.Ticker(args[0])
	if ok {
		ticker = p.Ticker
	} else if !a.companies().Empty() {
		fmt.Fprintf(a.stderr, "stoxicity: %s is not in the company list; tagging it anyway\n", ticker)
	}
	layer, err := universe.LoadLayer(a.layerPath())
	if err != nil {
		return err
	}
	changed := 0
	for _, tag := range args[1:] {
		if rest, rm := strings.CutPrefix(tag, "-"); rm {
			if layer.RemoveTag(ticker, rest) {
				changed++
			}
			continue
		}
		ch, err := layer.AddTag(ticker, tag)
		if err != nil {
			return err
		}
		if ch {
			changed++
		}
	}
	if changed > 0 {
		if err := layer.Save(a.layerPath()); err != nil {
			return err
		}
	}
	a.univ = nil
	cur, _ := a.universe().Get(ticker)
	fmt.Fprintf(a.stdout, "%s tags: %s\n", ticker, dash(strings.Join(cur.Tags, ", ")))
	return nil
}

func (a *app) universeList(args []string) error {
	u := a.universe()
	if len(args) == 0 {
		if len(u.Lists()) == 0 {
			fmt.Fprintf(a.stdout, "no lists yet; make one with: stoxicity universe list add NAME TICKER...\n")
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "LIST\tNAME\tSTOCKS")
		for _, l := range u.Lists() {
			fmt.Fprintf(tw, "%s\t%s\t%d\n", l.ID, l.Name, len(l.Tickers))
		}
		return tw.Flush()
	}
	if args[0] == "add" || args[0] == "remove" {
		if len(args) < 3 {
			return usageErrorf("list %s needs a list ID and at least one ticker", args[0])
		}
		layer, err := universe.LoadLayer(a.layerPath())
		if err != nil {
			return err
		}
		changed := 0
		for _, t := range args[2:] {
			ch, err := layer.SetMember(args[1], t, args[0] == "add")
			if err != nil {
				return err
			}
			if ch {
				changed++
			}
		}
		if changed > 0 {
			if err := layer.Save(a.layerPath()); err != nil {
				return err
			}
		}
		a.univ = nil
		args = args[1:2]
	}
	if len(args) != 1 {
		return usageErrorf("list takes an ID, or add|remove ID TICKER...")
	}
	l, ok := a.universe().List(args[0])
	if !ok {
		return fmt.Errorf("no list %q", args[0])
	}
	fmt.Fprintf(a.stdout, "%s (%s): %s\n", l.ID, l.Name, dash(strings.Join(l.Tickers, " ")))
	return nil
}

// universeFeeds writes down the SEC filing record of every stock, reads the
// websites of up to -search companies for their own news feed, and says how many
// feeds of each kind there are.
func (a *app) universeFeeds(ctx context.Context, args []string) error {
	fs := newFlags("universe feeds")
	search := fs.Int("search", 100, "company websites to read for their news feed; 0 reads none")
	if _, err := parseMixed(fs, args); err != nil {
		return err
	}
	if *search < 0 {
		return usageErrorf("-search must not be negative")
	}
	if a.companies().Empty() {
		return fmt.Errorf("no company list yet: run \"stoxicity universe update\"")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	var client httpx.Client
	if *search > 0 {
		if client, err = a.httpClient(); err != nil {
			return err
		}
	}
	k := &stockfeeds.Keeper{Store: db, Client: client, Now: a.now}
	stocks, err := stockfeeds.Stocks(ctx, db)
	if err != nil {
		return err
	}
	seeded, err := k.Seed(ctx, stocks)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "SEC filing records: %d new\n", seeded)
	if *search > 0 {
		rep, err := k.Discover(ctx, stocks, interestingTickers(ctx, db, a.knownTickers(), a.layerPath(), a.cfg.Lens.Threshold, a.now()), *search)
		fmt.Fprintf(a.stdout, "company websites: %s\n", rep)
		if err != nil {
			return err
		}
	}
	stats, err := db.StockFeedStats(ctx)
	if err != nil {
		return err
	}
	for _, kind := range []string{store.FeedSEC, store.FeedSite} {
		s := stats[kind]
		fmt.Fprintf(a.stdout, "%-5s feeds   %d of %d stocks, %d read so far, %d failing\n", kind, s.Feeds, s.Stocks, s.Polled, s.Failing)
	}
	return nil
}
