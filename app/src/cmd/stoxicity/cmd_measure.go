package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/measure"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/reconcile"
	"github.com/b070nd/stoxicity/app/src/refdata"
	"github.com/b070nd/stoxicity/app/src/research"
	"github.com/b070nd/stoxicity/app/src/store"
)

// scratchMarker is the file that makes a folder a measurement's scratch folder.
const scratchMarker = ".stoxicity-measure"

// cmdMeasure is the read-only feasibility measurement of PRD 7.1: it reads one
// window of public history into a scratch data folder with the real pipeline,
// and writes a report of what the free sources hold, what the pipeline made of
// it, what the reading cost, and what six months would need.
//
// It never opens the configured database: the data folder is replaced by the
// scratch folder, and a configured PostgreSQL server is not used. The scratch
// database holds titles and metadata, never document text, and is removed after
// the report unless -keep is given.
func cmdMeasure(ctx context.Context, a *app, args []string) error {
	fs := newFlags("measure")
	fromS := fs.String("from", "", "first day of the window, YYYY-MM-DD")
	toS := fs.String("to", "", "last day of the window, YYYY-MM-DD")
	scratch := fs.String("scratch", "", "an empty folder for the run's own database (or one an earlier measurement made)")
	out := fs.String("out", "", "where the report goes: FILE.md, with FILE.json beside it (default: in the scratch folder)")
	maxReq := fs.Int("max-requests", 40000, "most HTTP requests of the run, all hosts together; it stops when reached")
	maxMB := fs.Int("max-mb", 2048, "most megabytes the run may download; it stops when reached")
	insiders := fs.Int("insiders", 300, "Form 4 filings to read as a sample of insider trades (0: none)")
	prices := fs.Bool("prices", true, "check the daily bars of the events (needs the free price key)")
	keep := fs.Bool("keep", false, "keep the scratch database after the report")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	from, err := time.Parse(time.DateOnly, *fromS)
	if err != nil {
		return usageErrorf("-from: give the first day as YYYY-MM-DD")
	}
	to, err := time.Parse(time.DateOnly, *toS)
	if err != nil {
		return usageErrorf("-to: give the last day as YYYY-MM-DD")
	}
	today := time.Date(a.now().Year(), a.now().Month(), a.now().Day(), 0, 0, 0, 0, time.UTC)
	switch {
	case to.Before(from):
		return usageErrorf("-to is before -from")
	case to.Sub(from) > 62*24*time.Hour:
		return usageErrorf("a measurement window is at most two months")
	case !to.Before(today):
		return usageErrorf("the window must have ended: -to must be before today")
	case *maxReq < 1 || *maxMB < 1 || *insiders < 0:
		return usageErrorf("-max-requests and -max-mb must be positive, -insiders not negative")
	}
	dir, err := prepareScratch(*scratch, a.dataDir())
	if err != nil {
		return err
	}
	if a.db != nil {
		return errors.New("the database is already open: run measure on its own")
	}
	// From here on every store, cache and quota record lives in the scratch folder.
	a.cfg.DataDir, a.cfg.DatabaseURL = dir, ""
	a.masterDone, a.res, a.univ = false, nil, nil
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	report := *out
	if report == "" {
		report = filepath.Join(dir, fmt.Sprintf("measurement-%s-%s.md", *fromS, *toS))
	}

	in := measure.Inputs{From: *fromS, To: *toS, Revision: revision(), Started: a.now(), Hosts: map[string]measure.Host{}}
	in.Settings = []string{fmt.Sprintf("max-requests %d", *maxReq), fmt.Sprintf("max-mb %d", *maxMB),
		fmt.Sprintf("insiders sample %d", *insiders), fmt.Sprintf("prices %v", *prices), "SQLite in a scratch folder"}
	stage := func(name string, start time.Time, note string) {
		in.Stages = append(in.Stages, measure.Stage{Name: name, Duration: a.now().Sub(start), Note: note})
		fmt.Fprintf(a.stdout, "measure: %s done (%s) %s\n", name, a.now().Sub(start).Round(time.Second), note)
	}

	db, err := a.store()
	if err != nil {
		return err
	}
	real, err := a.httpClient()
	if err != nil {
		return err
	}
	counter := &measure.Counter{Inner: real, MaxRequests: *maxReq, MaxBytes: int64(*maxMB) << 20}
	a.opts.client = counter // every later request of this run goes through the budget

	finish := func(runErr error) error {
		in.Hosts = counter.Hosts()
		in.Stopped = counter.Stopped()
		if runErr != nil && in.Stopped == "" && !errors.Is(runErr, measure.ErrBudget) {
			in.Stopped = runErr.Error()
		}
		in.StoreSize = folderSize(filepath.Join(dir))
		rep := measure.Build(in, a.now())
		if err := writeReport(report, rep); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "measure: %s; report written to %s\n", rep.Recommendation, report)
		if !*keep {
			if err := clearScratch(dir, report); err != nil {
				fmt.Fprintf(a.stderr, "stoxicity: measure: the scratch database could not be removed: %v\n", err)
			}
		}
		return nil
	}

	// 1. The company list, for tickers.
	t0 := a.now()
	if _, err := a.refreshCompanies(ctx, true); err != nil {
		return finish(fmt.Errorf("company list: %w", err))
	}
	stage("company list", t0, fmt.Sprintf("%d companies", len(a.companies().Entities)))

	// 2. The daily indexes of the window.
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
		return errors.New("no filings source with forms is configured: nothing to measure")
	}
	in.Forms = forms
	cfg := edgar.Config{WebBase: a.cfg.Company.InsidersURL, Tickers: edgar.TickersByCIK(a.companies().Entities), Now: a.now}
	rc := reconcile.Config{DB: db, Indexer: edgar.Indexer{Client: counter, Config: cfg}, Forms: forms, Now: a.now, Logger: a.log,
		MaxRequests: 400, NoQuarterly: true}
	t0 = a.now()
	drep, err := rc.Discover(ctx, from, to, "range")
	if err != nil && drep.Days == 0 {
		return finish(fmt.Errorf("index: %w", err))
	}
	if comp, cerr := reconcile.Measure(ctx, db, from, to, forms); cerr == nil {
		in.IndexDays.Days, in.IndexDays.Complete, in.IndexDays.NonFiling = comp.Days, comp.Complete, comp.NonFiling
		in.IndexDays.Unresolved = comp.Days - comp.Resolved()
	}
	listed, err := db.MissingFilings(ctx, *fromS, *toS)
	if err != nil {
		return finish(err)
	}
	stage("daily indexes", t0, fmt.Sprintf("%d filings of forms %s", len(listed), strings.Join(forms, " ")))

	// 3. Reported figures for every filer: the SEC's XBRL frames, a few dozen requests.
	// They come without a filing day; step 4 reads it from the same company records.
	if k := a.fundamentalsKeeper(db, counter); k != nil && counter.Stopped() == "" {
		t0 = a.now()
		fr, _, ferr := k.Refresh(ctx, true)
		note := fmt.Sprintf("%d frames, %d facts of %d companies", fr.Requests, fr.Facts, fr.Companies)
		if ferr != nil {
			note += ": " + ferr.Error()
		}
		stage("reported figures", t0, note)
	}

	// 4. The SEC record of each company: acceptance times and 8-K items, and the
	// filing day of the figures of those companies (no extra request: the same record).
	t0 = a.now()
	want, filed := map[string][]string{}, map[string]string{}
	for _, m := range listed {
		want[m.CIK] = append(want[m.CIK], m.Accession)
		filed[m.Accession] = m.Filed
	}
	figureFilings := 0
	if undated, err := db.FactsWithoutFilingDay(ctx); err == nil {
		for cik, accs := range undated {
			if _, ok := want[cik]; !ok {
				continue // only companies asked anyway
			}
			for _, acc := range accs {
				if _, ok := filed[acc]; ok {
					continue
				}
				want[cik] = append(want[cik], acc)
				// A day no older record file covers: only the company's recent
				// record is read for these, never an extra request.
				filed[acc] = "9999-12-31"
				figureFilings++
			}
		}
	}
	type seen struct {
		accepted time.Time
		items    []string
	}
	record := map[string]seen{}
	subs := reconcile.SubmissionsURL{Record: a.cfg.Company.SubmissionsURL}
	if subs.Record == "" {
		subs.Record = "https://data.sec.gov/submissions/CIK%s.json"
	}
	er, eerr := reconcile.EnrichObserved(ctx, db, counter, subs, want, filed, a.now(), 0, func(_ string, f fundamentals.Filing) {
		s := seen{items: edgar.ParseItems(f.Items)}
		if t, ok := f.AcceptedTime(); ok {
			s.accepted = t
		}
		record[f.Accession] = s
	})
	stage("company records", t0, fmt.Sprintf("%d companies, %d requests, %d filings found (%d of them for figures), %d not in the record", er.Companies, er.Requests, er.Found, figureFilings, er.Missing))
	if eerr != nil && !errors.Is(eerr, measure.ErrBudget) {
		fmt.Fprintf(a.stderr, "stoxicity: measure: company records: %v (continuing with what was read)\n", eerr)
	}
	// Enrich asks the companies in CIK order and stops at the budget: the first
	// er.Companies of them were asked.
	asked := map[string]bool{}
	for i, cik := range slices.Sorted(maps.Keys(want)) {
		if i >= er.Companies {
			break
		}
		asked[cik] = true
	}
	tickers := edgar.TickersByCIK(a.companies().Entities)
	for _, m := range listed {
		f := measure.Filing{Accession: m.Accession, CIK: m.CIK, Company: m.Company, Form: m.Form, Filed: m.Filed, Tickers: tickers[m.CIK], Asked: asked[m.CIK]}
		if s, ok := record[m.Accession]; ok { // record also holds the figures' filings; only listed ones are counted
			f.InRecord, f.Accepted, f.Items = true, s.accepted, s.items
		}
		in.Filings = append(in.Filings, f)
	}

	// 5. The pipeline: one backfilled article per filing, scored and turned into events.
	t0 = a.now()
	rr, err := reconcile.Repair(ctx, db, a.pipeline(db), cfg, src, *fromS, *toS, drep.RunID, a.now())
	if err != nil {
		return finish(fmt.Errorf("pipeline: %w", err))
	}
	stage("pipeline", t0, fmt.Sprintf("%d articles stored, %d dated by acceptance time", rr.Stored, rr.TimeKnown))

	// 6. A sample of Form 4 filings, for insider trades.
	if *insiders > 0 {
		t0 = a.now()
		if k := a.insiderKeeper(db, counter); k != nil {
			ir, ierr := k.Refresh(ctx, *insiders)
			in.Insiders = measure.InsiderSample{Asked: ir.Asked, Trades: ir.Trades, Empty: ir.Empty, Unreadable: ir.Bad, Left: ir.Left}
			note := fmt.Sprintf("%d read, %d trades", ir.Asked, ir.Trades)
			if ierr != nil {
				note += ": " + ierr.Error()
			}
			stage("Form 4 sample", t0, note)
		} else {
			stage("Form 4 sample", t0, "insiders are off in the configuration")
		}
	}

	// 7. The event database of the window.
	t0 = a.now()
	end := to.AddDate(0, 0, 1)
	// From ten days before: a Form 4 is filed up to two business days after the
	// trade, and the build reads trades by their day. The report counts the
	// records by when they became public, inside the window.
	br, err := research.Build(ctx, db, from.AddDate(0, 0, -10), end, a.now())
	if err != nil {
		return finish(fmt.Errorf("event database: %w", err))
	}
	stage("event database", t0, fmt.Sprintf("%d stored events, %d insider trades, %d economic events", br.Events, br.Trades, br.Records))
	events, err := db.SignalEvents(ctx, store.SignalQuery{From: from, To: end})
	if err != nil {
		return finish(err)
	}
	for _, e := range events {
		ev := measure.Event{Kind: e.Kind, Ticker: e.Ticker}
		for _, x := range e.Evidence {
			ev.Accessions = append(ev.Accessions, x.ArticleID)
		}
		in.Events = append(in.Events, ev)
	}
	facts, err := db.EventFacts(ctx, store.EventFactQuery{From: from, To: end})
	if err != nil {
		return finish(err)
	}
	for _, f := range facts {
		in.Facts = append(in.Facts, measure.Fact{Category: string(f.Category), Type: string(f.Type), Symbol: f.Symbol,
			Basis: string(f.Basis), Available: f.AvailableAt, Evidence: f.Evidence, Figures: len(f.Facts)})
	}

	// 8. The daily bars the events would need.
	in.Prices = measure.Prices{Benchmark: "SPY", Note: "not asked for (-prices=false)"}
	if *prices && counter.Stopped() == "" {
		t0 = a.now()
		in.Prices = a.measurePrices(ctx, db, counter, from, to, in.Facts)
		stage("daily bars", t0, in.Prices.Note)
	}
	return finish(nil)
}

// measurePrices fetches the market days from a week before the window to 40
// days after it (one request per day, paced for the free plan) and counts the
// events with an entry bar and a bar 20 sessions later. The entry session is
// approximated as the first session after the event's day.
func (a *app) measurePrices(ctx context.Context, db *store.DB, client *measure.Counter, from, to time.Time, facts []measure.Fact) measure.Prices {
	p := measure.Prices{Benchmark: "SPY"}
	k := a.quoteKeeper(db, client)
	if k == nil {
		p.Note = "no price source: set company.quotes to polygon and MASSIVE_API_KEY (free plan) in .env.local"
		return p
	}
	stop := to.AddDate(0, 0, 40)
	if stop.After(a.now()) {
		stop = a.now()
	}
	k.Now = func() time.Time { return stop.AddDate(0, 0, 1).Add(12 * time.Hour) }
	k.Refetch = int(stop.Sub(from.AddDate(0, 0, -7)).Hours()/24) + 1
	k.Pace = 12 * time.Second // the free plan allows five requests a minute
	all, err := refdata.QuoteTickers(ctx, db)
	if err != nil {
		p.Note = "ticker list: " + err.Error()
		return p
	}
	rep, err := k.Refresh(ctx, all, nil, k.Refetch)
	if err != nil {
		p.Note = fmt.Sprintf("price fetch stopped: %v", err)
	}
	_ = rep
	var syms []string
	for _, f := range facts {
		if f.Symbol != "" && !slices.Contains(syms, f.Symbol) {
			syms = append(syms, f.Symbol)
		}
	}
	syms = append(syms, p.Benchmark)
	bars, err := db.CandleSeries(ctx, syms, from.AddDate(0, 0, -7).Format(time.DateOnly), stop.Format(time.DateOnly), time.Time{})
	if err != nil {
		p.Note = "reading bars: " + err.Error()
		return p
	}
	days := func(sym string) []string {
		var out []string
		for _, c := range bars[sym] {
			out = append(out, c.Day)
		}
		slices.Sort(out)
		return out
	}
	bench := days(p.Benchmark)
	has := func(list []string, d string) bool { _, ok := slices.BinarySearch(list, d); return ok }
	for _, f := range facts {
		if f.Symbol == "" {
			continue
		}
		p.Events++
		// The sessions are the benchmark's days: the market calendar as the bars show it.
		day := f.Available.UTC().Format(time.DateOnly)
		i, _ := slices.BinarySearch(bench, day)
		for i < len(bench) && bench[i] <= day {
			i++
		}
		if i >= len(bench) {
			continue
		}
		mine := days(f.Symbol)
		entry := bench[i]
		if has(mine, entry) {
			p.Entry++
		}
		p.BenchEntry++
		if i+20 < len(bench) {
			p.BenchHorizon20++
			if has(mine, bench[i+20]) {
				p.Horizon20++
			}
		}
	}
	p.Checked = len(bench) > 0
	if !p.Checked && p.Note == "" {
		p.Note = "no benchmark bars were stored"
	}
	if p.Note == "" {
		p.Note = "Entry is approximated as the first session after the event's UTC day; the studies' own entry rule (with the delay and session times) is stricter."
	}
	return p
}

// prepareScratch checks that dir is safe to write: not the configured data
// folder, not inside it, not containing it, and empty or an earlier
// measurement's scratch folder. It returns the absolute path.
func prepareScratch(dir, real string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", usageErrorf("-scratch is required: an empty folder for the run's own database")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	realAbs, _ := filepath.Abs(real)
	inside := func(a, b string) bool { // a is b or below it
		rel, err := filepath.Rel(b, a)
		return err == nil && (rel == "." || !strings.HasPrefix(rel, ".."))
	}
	if realAbs != "" && (inside(abs, realAbs) || inside(realAbs, abs)) {
		return "", fmt.Errorf("-scratch %s overlaps the configured data folder %s: give a separate folder", abs, realAbs)
	}
	entries, err := os.ReadDir(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	case len(entries) > 0:
		if _, err := os.Stat(filepath.Join(abs, scratchMarker)); err != nil {
			return "", fmt.Errorf("-scratch %s is not empty and was not made by a measurement: give an empty folder", abs)
		}
	}
	if err := os.WriteFile(filepath.Join(abs, scratchMarker), []byte("a stoxicity measurement's scratch folder; safe to delete\n"), 0o600); err != nil {
		return "", err
	}
	return abs, nil
}

// clearScratch removes everything in the scratch folder except the marker and
// the report files.
func clearScratch(dir, report string) error {
	keep := map[string]bool{scratchMarker: true}
	if filepath.Dir(report) == dir {
		keep[filepath.Base(report)] = true
		keep[strings.TrimSuffix(filepath.Base(report), ".md")+".json"] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func folderSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func writeReport(path string, r measure.Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(r.Markdown()), 0o600); err != nil {
		return err
	}
	j, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(strings.TrimSuffix(path, ".md")+".json", j, 0o600)
}

// revision is the source revision the binary was built from.
func revision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = " (with uncommitted changes)"
			}
		}
	}
	if rev == "" {
		return version
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return rev + dirty
}
