package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// maxReconcileDays bounds one run: each day is one request of the SEC's daily
// index, which holds every filing of the day (about a megabyte).
const maxReconcileDays = 60

// cmdReconcile compares the filings the SEC's daily indexes list with the
// articles the live feed delivered, and reports (or, with -repair, fills in)
// what the feed missed. A day's index is read once, after the day is over.
func cmdReconcile(ctx context.Context, a *app, args []string) error {
	fs := newFlags("reconcile")
	days := fs.Int("days", 7, "days back to check, at most 60")
	repair := fs.Bool("repair", false, "store an article for each filing the live feed missed")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if *days < 1 || *days > maxReconcileDays {
		return usageErrorf("-days must be between 1 and %d", maxReconcileDays)
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	var src model.Source
	var forms []string
	for _, id := range slices.Sorted(maps.Keys(a.reg)) {
		s := a.reg[id]
		if s.Kind == model.KindFilings && len(s.Forms) > 0 {
			if src.ID == "" {
				src = s
			}
			for _, f := range s.Forms {
				if !slices.Contains(forms, f) {
					forms = append(forms, f)
				}
			}
		}
	}
	if src.ID == "" {
		return fmt.Errorf("no filings source with forms is configured: nothing to reconcile")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	cfg := edgar.Config{WebBase: a.cfg.Company.InsidersURL, Tickers: edgar.TickersByCIK(a.companies().Entities), Now: a.now}
	ix := edgar.Indexer{Client: client, Config: cfg}

	now := a.now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	first := today.AddDate(0, 0, -*days)
	have, err := db.IndexDays(ctx, first.Format(time.DateOnly))
	if err != nil {
		return err
	}
	var read, skipped, unpublished int
	var runErr error
	for d := first; d.Before(today); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		key := d.Format(time.DateOnly)
		if h, ok := have[key]; ok && h.Read.After(d.AddDate(0, 0, 1)) {
			skipped++ // read after the day was over: the index of a past day does not change
			continue
		}
		entries, found, err := ix.Day(ctx, d)
		if err != nil {
			runErr = fmt.Errorf("%s: %w", key, err)
			break
		}
		var refs []store.FilingRef
		for _, e := range edgar.Wanted(entries, forms) {
			refs = append(refs, store.FilingRef{Accession: string(e.Accession), CIK: e.CIK, Form: e.Form, Filed: e.Filed, Company: e.Company})
		}
		if _, err := db.PutFilingIndex(ctx, key, refs, len(entries), !found, now); err != nil {
			return err
		}
		read++
		if !found {
			unpublished++
		}
	}
	fmt.Fprintf(a.stdout, "daily index: %d days read, %d already stored, %d without an index (holiday or not published); forms: %s\n",
		read, skipped, unpublished, strings.Join(forms, " "))

	from, to := first.Format(time.DateOnly), today.AddDate(0, 0, -1).Format(time.DateOnly)
	counts, err := db.FilingsOnDays(ctx, from, to)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DAY\tIN THE INDEX\tIN THE NEWS\tMISSED")
	var totalMissed int
	for _, day := range slices.Sorted(maps.Keys(counts)) {
		c := counts[day]
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\n", day, c[0], c[1], c[0]-c[1])
		totalMissed += c[0] - c[1]
	}
	tw.Flush()
	missing, err := db.MissingFilings(ctx, from, to)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "the live feed missed %d of the wanted filings of these days\n", len(missing))
	if *repair && len(missing) > 0 {
		arts := make([]model.Article, 0, len(missing))
		for _, m := range missing {
			acc, perr := edgar.ParseAccession(m.Accession)
			if perr != nil {
				continue
			}
			arts = append(arts, cfg.Article(src, edgar.IndexEntry{CIK: m.CIK, Company: m.Company, Form: m.Form, Filed: m.Filed, Accession: acc}, now))
		}
		n, err := a.pipeline(db).Ingest(ctx, src, arts)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "repaired: %d articles stored, dated to the start of their filing day (the index does not give the time)\n", n)
	} else if len(missing) > 0 {
		fmt.Fprintln(a.stdout, "run with -repair to store them")
	}
	return runErr
}
