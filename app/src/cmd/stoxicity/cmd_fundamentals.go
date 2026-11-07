package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/store"
)

// parseWhen reads a time given as RFC 3339 or as a day (taken as its end, so
// that what was filed that day counts as known).
func parseWhen(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if d, err := time.Parse(time.DateOnly, s); err == nil {
		return d.Add(24 * time.Hour), nil
	}
	return time.Time{}, usageErrorf("%q is neither RFC 3339 nor a day (YYYY-MM-DD)", s)
}

// universeFundamentals reads the SEC's XBRL frames: revenue, net income,
// assets, equity and shares of every filer, in a few dozen small requests. It
// does nothing when the last reading is younger than a week, unless -force.
func (a *app) universeFundamentals(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "history":
			return a.fundamentalsHistory(ctx, args[1:])
		case "classes":
			return a.fundamentalsClasses(ctx, args[1:])
		}
	}
	fs := newFlags("universe fundamentals")
	force := fs.Bool("force", false, "read the market even if the last reading is young")
	if _, err := parseMixed(fs, args); err != nil {
		return err
	}
	if a.cfg.Company.Fundamentals == "off" {
		return fmt.Errorf("fundamentals are off: set company.fundamentals to sec")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	k := a.fundamentalsKeeper(db, client)
	rep, ran, err := k.Refresh(ctx, *force)
	if err != nil {
		return err
	}
	if !ran {
		at, _ := db.FundamentalsRead(ctx)
		fmt.Fprintf(a.stdout, "fundamentals: read %s ago; -force reads again\n", a.now().Sub(at).Round(time.Minute))
		return nil
	}
	fmt.Fprintf(a.stdout, "fundamentals: %d facts of %d companies (%d requests, %d frames not published yet)\n",
		rep.Facts, rep.Companies, rep.Requests, rep.Missing)
	facts, err := db.Fundamentals(ctx)
	if err != nil {
		return err
	}
	sums := fundamentals.Summarize(facts)
	have := map[string]int{}
	for _, s := range sums {
		for k := range s.Num {
			have[k]++
		}
	}
	fmt.Fprintf(a.stdout, "companies with: revenue %d, income %d, growth %d, margin %d, equity %d, roe %d, shares %d\n",
		have[fundamentals.Revenue], have[fundamentals.NetIncome], have["growth"], have["margin"], have[fundamentals.Equity], have["roe"], have[fundamentals.Shares])
	return nil
}

// fundamentalsHistory reads every fact one company ever reported, with the
// filing each comes from, and prints the figures as they were known at a time.
// It asks the SEC for one company's history (a few megabytes) and stores it
// beside the frames' figures; nothing is replaced.
func (a *app) fundamentalsHistory(ctx context.Context, args []string) error {
	fs := newFlags("universe fundamentals history")
	at := fs.String("at", "", "the figures as known at this time (RFC 3339 or YYYY-MM-DD), default now")
	offline := fs.Bool("stored", false, "do not ask the SEC, read what is stored")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("history takes one ticker")
	}
	p, ok := a.universe().Get(rest[0])
	if !ok || p.CIK == "" {
		return fmt.Errorf("%s has no SEC number in the company list", strings.ToUpper(rest[0]))
	}
	knownAt := a.now()
	if *at != "" {
		if knownAt, err = parseWhen(*at); err != nil {
			return err
		}
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	if !*offline {
		client, err := a.httpClient()
		if err != nil {
			return err
		}
		facts, err := a.fundamentalsFetcher(client).History(ctx, p.CIK)
		if err != nil {
			return err
		}
		n, err := db.PutFactHistory(ctx, facts, a.now())
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "history of %s (CIK %s): %d facts read, %d new\n", p.Ticker, p.CIK, len(facts), n)
	}
	all, err := db.FactHistory(ctx, p.CIK)
	if err != nil {
		return err
	}
	unknown := 0
	for _, f := range all {
		if f.Filed == "" {
			unknown++
		}
	}
	sum := fundamentals.Summarize(fundamentals.AsOf(all, knownAt))[p.CIK]
	fmt.Fprintf(a.stdout, "known at %s:", knownAt.UTC().Format(time.DateOnly))
	if len(sum.Num) == 0 {
		fmt.Fprintln(a.stdout, " nothing filed by then")
	} else {
		fmt.Fprintf(a.stdout, " fiscal year ended %s\n", sum.End)
		for _, k := range slices.Sorted(maps.Keys(sum.Num)) {
			fmt.Fprintf(a.stdout, "  %-13s %s\n", k, figureText(k, sum.Num[k]))
		}
	}
	if unknown > 0 {
		fmt.Fprintf(a.stdout, "%d stored facts have no known filing day and are left out of every as-of reading (run the history again, or the EDGAR index)\n", unknown)
	}
	revs := fundamentals.Revisions(fundamentals.KnownBy(all, knownAt)) // later filings are not known yet
	for _, k := range slices.Sorted(maps.Keys(revs)) {
		fs := revs[k]
		parts := make([]string, 0, len(fs))
		for _, f := range fs {
			parts = append(parts, fmt.Sprintf("%s in %s (%s)", figureText(f.Metric, f.Value), f.Accession, f.Filed))
		}
		fmt.Fprintf(a.stdout, "restated %s: %s\n", k, strings.Join(parts, " -> "))
	}
	return nil
}

// fundamentalsClasses reads the cover page of the company's newest annual or
// quarterly report for the shares outstanding of each class, stores them and
// prints the value of each class and of the issuer.
func (a *app) fundamentalsClasses(ctx context.Context, args []string) error {
	fs := newFlags("universe fundamentals classes")
	offline := fs.Bool("stored", false, "do not ask the SEC, read what is stored")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("classes takes one ticker")
	}
	p, ok := a.universe().Get(rest[0])
	if !ok || p.CIK == "" {
		return fmt.Errorf("%s has no SEC number in the company list", strings.ToUpper(rest[0]))
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	if !*offline {
		client, err := a.httpClient()
		if err != nil {
			return err
		}
		if err := a.readCover(ctx, db, client, p.CIK); err != nil {
			return err
		}
	}
	classes, acc, filed, err := db.ShareClasses(ctx, p.CIK)
	if err != nil {
		return err
	}
	if len(classes) == 0 {
		return fmt.Errorf("no cover page share count is stored for %s", p.Ticker)
	}
	prices := map[string]float64{}
	if latest, err := db.LatestCandles(ctx); err == nil {
		for t, c := range latest {
			if len(c) > 0 {
				prices[marketdata.NormTicker(t)] = c[0].Close
			}
		}
	}
	capn := fundamentals.Capitalise(classes, p.Ticker, func(t string) (float64, bool) { v, ok := prices[marketdata.NormTicker(t)]; return v, ok })
	fmt.Fprintf(a.stdout, "%s: cover page of filing %s (filed %s)\n", p.Ticker, acc, filed)
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CLASS\tTICKER\tAS OF\tSHARES\tPRICE\tVALUE")
	for _, c := range capn.Classes {
		class := c.Dimensions
		if class == "" {
			class = "all (one class)"
		}
		price, value := "no price", "unknown"
		if c.Priced {
			price, value = fmt.Sprintf("%.2f", c.Price), moneyOrBlank(c.Cap)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", class, c.Ticker, c.AsOf, countText(c.Shares), price, value)
	}
	tw.Flush()
	if capn.IssuerKnown {
		fmt.Fprintf(a.stdout, "issuer value (all classes): %s\n", moneyOrBlank(capn.Issuer))
	} else {
		fmt.Fprintf(a.stdout, "issuer value: not known (%s)\n", capn.Note)
	}
	return nil
}

// readCover finds the company's newest annual or quarterly report, reads its
// cover page and stores the share counts.
func (a *app) readCover(ctx context.Context, db *store.DB, client httpx.Client, cik string) error {
	sub := a.cfg.Company.SubmissionsURL
	if sub == "" {
		sub = company.SubmissionsURL
	}
	resp, err := client.Do(ctx, httpx.Request{URL: fmt.Sprintf(sub, cik)})
	if err != nil {
		return err
	}
	filings, err := fundamentals.RecentFilings(resp.Body)
	if err != nil {
		return err
	}
	for _, f := range filings {
		if !f.Periodic() || f.Document == "" {
			continue
		}
		doc, err := client.Do(ctx, httpx.Request{URL: f.DocumentURL(a.fundamentalsArchives(), cik)})
		if err != nil {
			return err
		}
		classes, err := fundamentals.ParseCover(doc.Body)
		if err != nil {
			return fmt.Errorf("filing %s: %w", f.Accession, err)
		}
		if len(classes) == 0 {
			continue // this report has no machine-readable cover page; try the one before
		}
		if err := db.PutShareClasses(ctx, cik, f.Accession, f.Form, f.Filed, classes, a.now()); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "read the cover page of %s %s (filed %s): %d share counts\n", f.Form, f.Accession, f.Filed, len(classes))
		return nil
	}
	return errors.New("no annual or quarterly report with a readable cover page among the company's recent filings")
}

func (a *app) fundamentalsFetcher(client httpx.Client) fundamentals.Fetcher {
	f := fundamentals.Fetcher{Client: client, URL: a.cfg.Company.FundamentalsURL, Now: a.now}
	if a.cfg.Company.FundamentalsURL != "" {
		// A test address holds the history beside the frames.
		f.HistoryURL = a.cfg.Company.FundamentalsURL + "companyfacts/"
	}
	return f
}

func (a *app) fundamentalsArchives() string {
	if a.cfg.Company.FundamentalsURL != "" {
		return a.cfg.Company.FundamentalsURL + "archives/"
	}
	return ""
}

// figureText writes a figure by its name: ratios as percentages, money and
// counts with thousands separators.
func figureText(name string, v float64) string {
	switch name {
	case "growth", "margin", "roe", "equity_ratio":
		return fmt.Sprintf("%.1f%%", v*100)
	}
	return fmt.Sprintf("%.0f", v) // negative figures (a loss) stay visible
}
