package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/universe"
)

const (
	defaultUniverseLimit = 25
	defaultNewsDays      = 14
	// enrichNewsDays is how far back "universe enrich" looks for tickers that
	// are in the news.
	enrichNewsDays   = 30
	defaultEnrichMax = 300
)

const universeUsage = "expected: status | update [-force] | changes [-days N] [-limit N] | enrich [-all] [-max N] [TICKER...] | find [-limit N] [-days N] QUERY... | show TICKER | tag TICKER [-]TAG... | list [ID] | list add|remove ID TICKER..."

// cmdUniverse is the reference list of stocks: every company the program
// knows, searched, tagged and grouped.
func cmdUniverse(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("%s", universeUsage)
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
	case "find":
		return a.universeFind(ctx, args[1:])
	case "show":
		return a.universeShow(ctx, args[1:])
	case "tag":
		return a.universeTag(args[1:])
	case "list":
		return a.universeList(args[1:])
	}
	return usageErrorf("unknown subcommand %q; %s", args[0], universeUsage)
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

// universeEnrich fetches the SEC record of the companies you care about.
func (a *app) universeEnrich(ctx context.Context, args []string) error {
	fs := newFlags("universe enrich")
	all := fs.Bool("all", false, "every company in the list (about 10,000 requests at 2 a second: over an hour)")
	maxReq := fs.Int("max", defaultEnrichMax, "most requests to make; 0 means no limit")
	force := fs.Bool("force", false, "ask again even for records that are young")
	tickers, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	u := a.universe()
	if a.companies().Empty() {
		return fmt.Errorf("no company list yet: run \"stoxicity universe update\"")
	}
	var ciks []string
	add := func(t string) {
		if p, ok := u.Get(t); ok && p.CIK != "" {
			ciks = append(ciks, p.CIK)
		}
	}
	switch {
	case len(tickers) > 0:
		for _, t := range tickers {
			if _, ok := u.Get(t); !ok {
				return fmt.Errorf("%s is not in the company list", t)
			}
			add(t)
		}
	case *all:
		for _, p := range u.All() {
			add(p.Ticker)
		}
	default:
		db, err := a.store()
		if err != nil {
			return err
		}
		for _, t := range interestingTickers(ctx, db, a.knownTickers(), a.layerPath(), a.cfg.Lens.Threshold, a.now()) {
			add(t)
		}
	}
	if len(ciks) == 0 {
		fmt.Fprintln(a.stdout, "nothing to look up: follow some stocks (watchlist, lists, tags) or name tickers")
		return nil
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	opts := company.EnrichOptions{URL: a.cfg.Company.SubmissionsURL, Force: *force, Max: *maxReq, Now: a.now}
	opts.Progress = func(done, total int) {
		if done%25 == 0 || done == total {
			fmt.Fprintf(a.stderr, "enrich: %d of %d\n", done, total)
		}
	}
	res, err := company.Enrich(ctx, client, a.companyDir(), ciks, opts)
	a.syncReference(context.WithoutCancel(ctx))
	fmt.Fprintf(a.stdout, "stored %d records; %d were young enough, %d are unknown to the SEC", res.Fetched, res.Skipped, res.Missing)
	if res.Left > 0 {
		fmt.Fprintf(a.stdout, ", %d left (raise -max)", res.Left)
	}
	fmt.Fprintln(a.stdout)
	for _, f := range res.Failed {
		fmt.Fprintf(a.stdout, "failed: %s\n", f)
	}
	return err
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
	limit := fs.Int("limit", defaultUniverseLimit, "most rows to print")
	days := fs.Int("days", defaultNewsDays, "days of news that news: counts")
	words, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if *limit < 1 || *days < 1 {
		return usageErrorf("-limit and -days must be at least 1")
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
	if len(hits) == 0 {
		fmt.Fprintln(a.stdout, "no company matches")
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TICKER\tNAME\tEXCHANGE\tKIND\tINDUSTRY\tNEWS\tLISTS\tTAGS")
	for _, h := range hits[:min(len(hits), *limit)] {
		p := h.Profile
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", p.Ticker, truncate(p.Name, 34), dash(p.Exchange), dash(p.Kind),
			dash(truncate(p.Industry, 28)), h.News, dash(strings.Join(p.Lists, ",")), dash(strings.Join(p.Tags, ",")))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(hits) > *limit {
		fmt.Fprintf(a.stdout, "%d more; narrow the query or raise -limit\n", len(hits)-*limit)
	}
	return nil
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
	return universe.FactsFrom(arts, a.cfg.Lens.Threshold, a.knownTickers())
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
	row("website", p.Website)
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
	if n := a.facts(ctx, defaultNewsDays).News[p.Ticker]; n > 0 {
		row("news", fmt.Sprintf("%d relevant articles in %d days (stoxicity stock %s)", n, defaultNewsDays, p.Ticker))
	}
	if p.SIC == "" && p.CIK != "" {
		row("hint", "no industry yet: stoxicity universe enrich "+p.Ticker)
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
