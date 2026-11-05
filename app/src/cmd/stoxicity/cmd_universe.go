package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
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

const universeUsage = "expected: status | update [-force] | enrich [-all] [-max N] [TICKER...] | find [-limit N] [-days N] QUERY... | show TICKER | tag TICKER [-]TAG... | list [ID] | list add|remove ID TICKER..."

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
	dir := a.companyDir()
	fmt.Fprintf(a.stdout, "folder      %s\n", dir)
	fmt.Fprintf(a.stdout, "layer       %s\n", a.relative(a.layerPath()))
	m := a.companies()
	if m.Empty() {
		fmt.Fprintln(a.stdout, "companies   none yet: run \"stoxicity universe update\"")
	} else {
		fmt.Fprintf(a.stdout, "companies   %d tickers, checked %s\n", len(m.Entities), a.ago(m.FetchedAt))
	}
	l, err := company.LoadListing(dir)
	switch {
	case err != nil:
		fmt.Fprintf(a.stdout, "directory   damaged: %v\n", err)
	case l.Len() == 0:
		fmt.Fprintf(a.stdout, "directory   none (company.listing is %v): ETFs cannot be told from stocks\n", a.cfg.Company.Listing)
	default:
		fmt.Fprintf(a.stdout, "directory   %d symbols, checked %s\n", l.Len(), a.ago(l.FetchedAt))
	}
	ind, err := company.LoadIndustry(dir)
	if err != nil {
		fmt.Fprintf(a.stdout, "industry    damaged: %v\n", err)
	} else {
		withData := 0
		for _, i := range ind {
			if !i.Missing {
				withData++
			}
		}
		fmt.Fprintf(a.stdout, "industry    %d companies have an SEC record (run \"stoxicity universe enrich\" for more)\n", withData)
	}
	fmt.Fprintf(a.stdout, "lists       %d, tags %d (tenant %s)\n", len(u.Lists()), len(u.Tags()), a.tenantID)
	fmt.Fprintf(a.stdout, "universe    %d profiles\n", u.Len())
	return nil
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
		for _, t := range a.interestingTickers(ctx, u) {
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
	a.univ = nil
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
// follow, those in your lists and tags, and those in relevant news lately.
func (a *app) interestingTickers(ctx context.Context, u *universe.Universe) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t = universe.Ticker(t); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range a.knownTickers() {
		add(t)
	}
	for _, l := range u.Lists() {
		for _, t := range l.Tickers {
			add(t)
		}
	}
	for _, p := range u.All() {
		if len(p.Tags) > 0 {
			add(p.Ticker)
		}
	}
	if rd, err := a.reader(); err == nil {
		now := a.now()
		if arts, err := rd.Range(ctx, now.AddDate(0, 0, -enrichNewsDays), now.Add(time.Hour)); err == nil {
			f := universe.FactsFrom(arts, a.cfg.Lens.Threshold, nil)
			for t := range f.News {
				add(t)
			}
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
