package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/reconcile"
	"github.com/b070nd/stoxicity/app/src/store"
)

// maxRangeDays bounds the range of one run; a longer history is read in several.
const maxRangeDays = 3700

// cmdReconcile compares the filings the SEC's EDGAR indexes list with the
// articles the live feed delivered. It works in separate steps, each of which
// can be run alone and repeated:
//
//	(always)  discover: read the indexes of the days that still need it
//	-enrich   ask the SEC for the acceptance time of the filings the feed missed
//	-facts    ask the SEC for the filing day of fundamentals with none
//	-repair   store an article for each filing the feed missed, as a backfill
//
// The default range is a rolling window (-days, 7) that is extended back to the
// last day that was resolved, up to -catchup days, so an outage longer than the
// window is caught up. -from and -to read an explicit range, using the quarterly
// indexes for the quarters that have ended and the daily ones for the rest.
func cmdReconcile(ctx context.Context, a *app, args []string) error {
	fs := newFlags("reconcile")
	days := fs.Int("days", reconcile.DefaultOverlap, "rolling window: days back to check again")
	catchup := fs.Int("catchup", reconcile.DefaultCatchUp, "after an outage, continue from the last resolved day, at most this many days back")
	fromS := fs.String("from", "", "first day of an explicit range (YYYY-MM-DD)")
	toS := fs.String("to", "", "last day of an explicit range (YYYY-MM-DD), default yesterday")
	noQuarter := fs.Bool("no-quarterly", false, "read daily indexes only, however long the range")
	refetch := fs.Bool("refetch", false, "read days again that are already resolved")
	maxReq := fs.Int("max-requests", reconcile.DefaultRequests, "most daily-index requests in one run; run again to continue")
	enrich := fs.Bool("enrich", false, "step: read the acceptance time of the missed filings from the SEC's record of each company")
	facts := fs.Bool("facts", false, "step: read the filing day of stored fundamentals whose filing day is unknown")
	repair := fs.Bool("repair", false, "step: store an article for each filing the live feed missed, marked as a backfill")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if *days < 1 || *days > 366 {
		return usageErrorf("-days must be between 1 and 366")
	}
	if *catchup < *days || *catchup > maxRangeDays {
		return usageErrorf("-catchup must be between -days and %d", maxRangeDays)
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
	rc := reconcile.Config{DB: db, Indexer: edgar.Indexer{Client: client, Config: cfg}, Forms: forms, Now: a.now, Logger: a.log,
		MaxRequests: *maxReq, NoQuarterly: *noQuarter, Refetch: *refetch}

	var from, to time.Time
	mode := "rolling"
	today := time.Date(a.now().Year(), a.now().Month(), a.now().Day(), 0, 0, 0, 0, time.UTC)
	if *fromS != "" || *toS != "" {
		mode = "range"
		if *fromS == "" {
			return usageErrorf("-to needs -from")
		}
		if from, err = time.Parse(time.DateOnly, *fromS); err != nil {
			return usageErrorf("-from: %v", err)
		}
		to = today.AddDate(0, 0, -1)
		if *toS != "" {
			if to, err = time.Parse(time.DateOnly, *toS); err != nil {
				return usageErrorf("-to: %v", err)
			}
		}
		if to.Before(from) {
			return usageErrorf("-to is before -from")
		}
		if int(to.Sub(from).Hours()/24) > maxRangeDays {
			return usageErrorf("a range is at most %d days; read a longer history in several runs", maxRangeDays)
		}
	} else {
		var extended int
		var cut bool
		from, to, extended, cut, err = rc.Rolling(ctx, *days, *catchup)
		if err != nil {
			return err
		}
		if extended > 0 {
			fmt.Fprintf(a.stdout, "catching up: the window starts %d days earlier, after the last resolved day\n", extended)
		}
		if cut {
			fmt.Fprintf(a.stdout, "the gap is longer than -catchup %d days: the days before %s are not read by this run; read them with -from and -to\n", *catchup, from.Format(time.DateOnly))
		}
	}

	rep, runErr := rc.Discover(ctx, from, to, mode)
	fmt.Fprintf(a.stdout, "discover %s to %s: %d days; %d already resolved, %d daily and %d quarterly indexes read; %d complete, %d without filings by the calendar or an empty quarter, %d unpublished (retried next run), %d failed; %d filings stored for the first time; forms: %s\n",
		rep.From, rep.To, rep.Days, rep.Skipped, rep.Daily, rep.Quarterly, rep.Complete, rep.NonFiling, rep.Unpublished, rep.Errors, rep.Added, strings.Join(forms, " "))
	if rep.FormsChanged > 0 {
		fmt.Fprintf(a.stdout, "%d days were read again because they had been read for other forms than the ones selected now\n", rep.FormsChanged)
	}
	if rep.Pending > 0 {
		fmt.Fprintf(a.stdout, "%d days of the range are still unresolved (unpublished, failed or partial); the next run reads them again\n", rep.Pending)
	}
	if rep.Stopped != "" {
		fmt.Fprintf(a.stdout, "stopped early: %s\n", rep.Stopped)
	}

	f, t := rep.From, rep.To
	if ft, err1 := time.Parse(time.DateOnly, f); err1 == nil {
		if tt, err2 := time.Parse(time.DateOnly, t); err2 == nil && !tt.Before(ft) {
			// Completeness is what is stored, not whether the last requests worked.
			if comp, err := reconcile.Measure(ctx, db, ft, tt, forms); err == nil {
				fmt.Fprintf(a.stdout, "completeness %s to %s for forms %s: %d of %d days resolved (%.0f%%): %d complete, %d without filings, %d unread, %d unpublished, %d failed, %d read too early, %d read for other forms\n",
					f, t, strings.Join(forms, " "), comp.Resolved(), comp.Days, comp.Percent(), comp.Complete, comp.NonFiling, comp.Unread, comp.Unpublished, comp.Failed, comp.Partial, comp.FormsStale)
			}
		}
	}
	a.printDays(ctx, db, f, t)

	missing, err := db.MissingFilings(ctx, f, t)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "the live feed missed %d of the wanted filings of these days\n", len(missing))

	subs := reconcile.SubmissionsURL{Record: a.cfg.Company.SubmissionsURL}
	if subs.Record == "" {
		subs.Record = company.SubmissionsURL
	}
	if *enrich && len(missing) > 0 {
		want, filed := map[string][]string{}, map[string]string{}
		accs := make([]string, len(missing))
		for i, m := range missing {
			accs[i] = m.Accession
		}
		have, err := db.FilingMetas(ctx, accs)
		if err != nil {
			return err
		}
		for _, m := range missing {
			if mm, ok := have[m.Accession]; ok && !mm.Accepted.IsZero() {
				continue
			}
			want[m.CIK] = append(want[m.CIK], m.Accession)
			filed[m.Accession] = m.Filed
		}
		er, err := reconcile.Enrich(ctx, db, client, subs, want, filed, a.now(), *maxReq)
		fmt.Fprintf(a.stdout, "enrich: %d companies, %d requests; %d filings resolved by accession, %d not found in the record\n", er.Companies, er.Requests, er.Found, er.Missing)
		if err != nil {
			return err
		}
	}
	if *facts {
		pending, err := db.FactsWithoutFilingDay(ctx)
		if err != nil {
			return err
		}
		er, err := reconcile.Enrich(ctx, db, client, subs, pending, nil, a.now(), *maxReq)
		fmt.Fprintf(a.stdout, "facts: %d companies have fundamentals without a filing day; %d requests; %d filings resolved by accession, %d not found in the record\n", len(pending), er.Requests, er.Found, er.Missing)
		if err != nil {
			return err
		}
	}
	switch {
	case *repair && len(missing) > 0:
		rr, err := reconcile.Repair(ctx, db, a.pipeline(db), cfg, src, f, t, rep.RunID, a.now())
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "repaired: %d articles stored as backfills (%d late, %d historical); %d dated by their acceptance time, %d to the start of their filing day because the time is not known\n",
			rr.Stored, rr.Late, rr.Historical, rr.TimeKnown, rr.DayOnly)
	case len(missing) > 0:
		fmt.Fprintln(a.stdout, "run with -repair to store them (and -enrich first to date them by acceptance time)")
	}
	return runErr
}

// printDays shows the days of the range that are not plainly complete, or that
// have filings the feed missed.
func (a *app) printDays(ctx context.Context, db *store.DB, from, to string) {
	days, err := db.IndexDays(ctx, from, to)
	if err != nil {
		return
	}
	counts, err := db.FilingsOnDays(ctx, from, to)
	if err != nil {
		return
	}
	t := cliTable{Header: []string{"DAY", "STATUS", "IN THE INDEX", "IN THE NEWS", "MISSED", "BASIS"}, Right: []bool{false, false, true, true, true, false}}
	shown, hidden := 0, 0
	for _, day := range slices.Sorted(maps.Keys(days)) {
		d := days[day]
		c := counts[day]
		missed := c[0] - c[1]
		if d.Status == store.DayComplete && missed == 0 || d.Status == store.DayNonFiling && strings.HasPrefix(d.Basis, "calendar: S") {
			hidden++
			continue
		}
		if shown == 40 {
			hidden++
			continue
		}
		shown++
		light := map[store.DayStatus]cliStyle{store.DayComplete: cliGreen, store.DayNonFiling: cliDim, store.DayUnpublished: cliYellow, store.DayPartial: cliYellow, store.DayError: cliRed}[d.Status]
		mstyle := cliGreen
		if missed > 0 {
			mstyle = cliRed
		}
		basis := d.Basis
		if d.LastError != "" && !d.Status.Resolved() {
			basis += ": " + d.LastError
		}
		t.Rows = append(t.Rows, []cliCell{plain(day), styled(string(d.Status), light), plain(fmt.Sprint(c[0])), plain(fmt.Sprint(c[1])), styled(fmt.Sprint(missed), mstyle), plain(basis)})
	}
	if shown > 0 {
		t.write(a.stdout, useColor(a.stdout))
	}
	if hidden > 0 {
		fmt.Fprintf(a.stdout, "(%d more days are complete with nothing missed, or weekends, and are not listed)\n", hidden)
	}
}
