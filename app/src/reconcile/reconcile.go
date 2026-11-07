// Package reconcile compares what the SEC's EDGAR indexes list with what this
// system holds, in three separate steps that can be run apart:
//
//	Discover  read the indexes (daily, and quarterly for long ranges) into the
//	          filing index, with the state of every day;
//	Enrich    ask the SEC's record of a company for the acceptance time of the
//	          filings that are wanted, by accession, nothing else;
//	Repair    store an article for each wanted filing the live feed missed,
//	          marked as a backfill.
//
// Progress is persisted after every day, so a run that is interrupted, or an
// outage of any length, is continued by running again. A day is in one of five
// states (see store.DayStatus); a day the SEC has no index for is "unpublished"
// and retried, unless the calendar says it is a weekend or federal holiday, and
// a completed quarterly index that lists nothing for a weekday makes it a
// non-filing day, with the basis stated.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// Defaults of a run.
const (
	// DefaultOverlap is how many days back a rolling run looks again.
	DefaultOverlap = 7
	// DefaultCatchUp is how far back a rolling run continues from the last day
	// it resolved, after an outage. A longer gap is read with an explicit range.
	DefaultCatchUp = 90
	// DefaultRequests is the daily-index requests of one run.
	DefaultRequests = 150
	// DefaultQuarterDays is how many weekdays of one quarter must need reading
	// before its quarterly index is used instead of daily ones: a daily index
	// is about a megabyte, a compressed quarterly one about ten.
	DefaultQuarterDays = 12
	// LateDays is how soon after its filing day a recovered filing still counts
	// as late news rather than as history.
	LateDays = 3
)

// Config carries what a run works with.
type Config struct {
	DB      *store.DB
	Indexer edgar.Indexer
	Forms   []string
	Now     func() time.Time
	Logger  *slog.Logger
	// MaxRequests bounds the daily-index requests of one run (0: DefaultRequests).
	MaxRequests int
	// QuarterDays is the weekdays needing reading from which a quarterly index
	// is used (0: DefaultQuarterDays).
	QuarterDays int
	// NoQuarterly reads daily indexes only.
	NoQuarterly bool
	// Refetch reads days again that are already resolved.
	Refetch bool
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c Config) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// Report says what a Discover run did.
type Report struct {
	RunID    int64
	From, To string
	Days     int // days in the range
	Skipped  int // already resolved
	// FormsChanged counts resolved days read again because they were read for other
	// forms than the ones selected now.
	FormsChanged     int
	Daily, Quarterly int // daily indexes and quarterly indexes read
	Complete         int
	NonFiling        int
	Unpublished      int
	Errors           int
	Added            int    // filings stored that were not before
	Pending          int    // days of the range still unresolved after the run
	Stopped          string // why the run ended early, empty when it ran to the end
}

// FormsKey writes a selection of forms in the way the days record it: upper case,
// sorted, without repeats, comma separated.
func FormsKey(forms []string) string {
	seen := map[string]bool{}
	var out []string
	for _, f := range forms {
		if f = strings.ToUpper(strings.TrimSpace(f)); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

// readFor says whether a day read for the forms in stored covers the forms in
// want. A day read before the selection was recorded covers nothing.
func readFor(stored, want string) bool {
	if want == "" {
		return true
	}
	if stored == "" {
		return false
	}
	have := map[string]bool{}
	for _, f := range strings.Split(stored, ",") {
		have[f] = true
	}
	for _, f := range strings.Split(want, ",") {
		if !have[f] {
			return false
		}
	}
	return true
}

// covered says whether a stored day needs no further reading for the forms: it is
// resolved and, when its filings were taken from an index, taken for these forms.
// A day with no filings by the calendar or by an empty completed quarter has none
// of any form.
func covered(d store.IndexDay, forms string) bool {
	if !d.Status.Resolved() {
		return false
	}
	return d.Status == store.DayNonFiling || readFor(d.Forms, forms)
}

func day(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }

// Rolling returns the range of a rolling run: the overlap days back from
// yesterday, extended back to the day after the last day that was resolved when
// that is earlier (the catch-up after an outage), but not beyond catchUp days.
// extended says how far it was extended and whether the catch-up limit cut it.
func (c Config) Rolling(ctx context.Context, overlap, catchUp int) (from, to time.Time, extended int, cut bool, err error) {
	today := day(c.now())
	to = today.AddDate(0, 0, -1)
	from = today.AddDate(0, 0, -overlap)
	last, ok, err := c.DB.LastResolvedDay(ctx)
	if err != nil {
		return from, to, 0, false, err
	}
	if !ok {
		return from, to, 0, false, nil
	}
	d, perr := time.Parse(time.DateOnly, last)
	if perr != nil {
		return from, to, 0, false, nil
	}
	if next := d.AddDate(0, 0, 1); next.Before(from) {
		floor := today.AddDate(0, 0, -catchUp)
		if next.Before(floor) {
			next, cut = floor, true
		}
		extended = int(from.Sub(next).Hours() / 24)
		from = next
	}
	// Days that are unresolved before the window stay unresolved until read.
	un, err := c.DB.UnresolvedDays(ctx, today.AddDate(0, 0, -catchUp).Format(time.DateOnly), from.AddDate(0, 0, -1).Format(time.DateOnly))
	if err != nil {
		return from, to, extended, cut, err
	}
	for _, u := range un {
		if t, perr := time.Parse(time.DateOnly, u.Day); perr == nil && t.Before(from) {
			extended += int(from.Sub(t).Hours() / 24)
			from = t
		}
	}
	return from, to, extended, cut, nil
}

// dayOver is when the index of a day can be taken for complete. Filings are
// accepted until 22:00 New York time and dated to that day, which is 03:00 UTC
// (02:00 in winter) the next day; three hours more are allowed for the index to
// be built. An index read earlier is stored as partial and read again.
func dayOver(d time.Time) time.Time { return d.AddDate(0, 0, 1).Add(6 * time.Hour) }

// Discover reads the indexes for the days of [from, to] (UTC days, to before
// today) that still need it.
func (c Config) Discover(ctx context.Context, from, to time.Time, mode string) (Report, error) {
	now := c.now()
	today := day(now)
	from, to = day(from), day(to)
	if !to.Before(today) {
		to = today.AddDate(0, 0, -1)
	}
	rep := Report{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly)}
	if to.Before(from) {
		return rep, nil
	}
	stored, err := c.DB.IndexDays(ctx, rep.From, rep.To)
	if err != nil {
		return rep, err
	}
	forms := FormsKey(c.Forms)
	var need []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		rep.Days++
		if h, ok := stored[d.Format(time.DateOnly)]; ok && !c.Refetch {
			if covered(h, forms) {
				rep.Skipped++
				continue
			}
			if h.Status.Resolved() {
				rep.FormsChanged++
			}
		}
		need = append(need, d)
	}
	// What was read is written even when the run is being cut off: a cancelled
	// context stops the requests, not the record of what they found.
	wctx := context.WithoutCancel(ctx)
	run, err := c.DB.StartReconcileRun(wctx, rep.From, rep.To, mode, len(need), now)
	if err != nil {
		return rep, err
	}
	rep.RunID = run
	done := 0
	progress := func(note string, finished bool) {
		var fin time.Time
		if finished {
			fin = c.now()
		}
		if err := c.DB.UpdateReconcileRun(wctx, run, done, note, fin); err != nil {
			c.log().Warn("reconcile: could not record progress", "err", err)
		}
	}

	// Weekends need no request.
	var weekdays []time.Time
	for _, d := range need {
		if reason, weekend, _ := edgar.NonFilingDay(d); weekend {
			if _, err := c.DB.PutFilingDay(ctx, store.DayResult{Day: d.Format(time.DateOnly), Status: store.DayNonFiling, Basis: "calendar: " + reason, Source: "calendar"}, now); err != nil {
				return rep, err
			}
			rep.NonFiling++
			done++
			continue
		}
		weekdays = append(weekdays, d)
	}

	// Long ranges: a quarterly index for each ended quarter with many days to read.
	if !c.NoQuarterly {
		var err error
		weekdays, err = c.quarterly(ctx, weekdays, today, now, &rep, &done)
		if err != nil {
			progress("stopped: "+err.Error(), false)
			return rep, err
		}
	}

	// Daily indexes for the rest.
	budget := c.MaxRequests
	if budget <= 0 {
		budget = DefaultRequests
	}
	consecutive := 0
	var runErr error
	for _, d := range weekdays {
		if err := ctx.Err(); err != nil {
			runErr = err
			rep.Stopped = "interrupted"
			break
		}
		if rep.Daily >= budget {
			rep.Stopped = fmt.Sprintf("request budget of %d reached; run again to continue", budget)
			break
		}
		key := d.Format(time.DateOnly)
		entries, found, err := c.Indexer.Day(ctx, d)
		if cerr := ctx.Err(); cerr != nil {
			// The run was cut off while the request was out: nothing was learned of the
			// day, and that is not the SEC's failure. It stays as it was and is read next time.
			runErr = cerr
			rep.Stopped = "interrupted"
			break
		}
		rep.Daily++
		res := store.DayResult{Day: key, Source: "daily", Forms: forms}
		switch {
		case err != nil:
			res.Status, res.Err = store.DayError, err.Error()
			rep.Errors++
			consecutive++
		case !found:
			consecutive = 0
			if reason, _, nf := edgar.NonFilingDay(d); nf {
				res.Status, res.Basis, res.Source = store.DayNonFiling, "calendar: "+reason+"; the SEC has no index", "calendar"
				rep.NonFiling++
			} else {
				res.Status, res.Basis = store.DayUnpublished, "the SEC had no index for this day when it was asked"
				rep.Unpublished++
			}
		default:
			consecutive = 0
			res.Listed = len(entries)
			for _, e := range edgar.Wanted(entries, c.Forms) {
				res.Refs = append(res.Refs, refOf(e))
			}
			res.Status, res.Basis = store.DayComplete, "daily index"
			if now.Before(dayOver(d)) {
				res.Status, res.Basis = store.DayPartial, "read before the day's late filings were listed"
			} else {
				rep.Complete++
			}
		}
		added, perr := c.DB.PutFilingDay(wctx, res, now)
		if perr != nil {
			return rep, perr
		}
		rep.Added += added
		done++
		progress("", false)
		if err != nil {
			runErr = fmt.Errorf("%s: %w", key, err)
			if consecutive >= 3 {
				rep.Stopped = "three requests failed in a row; run again to continue"
				break
			}
		}
	}
	rem, err := c.DB.UnresolvedDays(wctx, rep.From, rep.To)
	if err != nil {
		return rep, err
	}
	rep.Pending = len(rem)
	progress(rep.Stopped, rep.Stopped == "")
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return rep, runErr
	}
	return rep, runErr
}

func refOf(e edgar.IndexEntry) store.FilingRef {
	return store.FilingRef{Accession: string(e.Accession), CIK: e.CIK, Form: e.Form, Filed: e.Filed, Company: e.Company}
}

// quarterly reads the quarterly index of each ended quarter that has enough days
// to read and resolves those days from it; it returns the days still to read.
func (c Config) quarterly(ctx context.Context, days []time.Time, today, now time.Time, rep *Report, done *int) ([]time.Time, error) {
	min := c.QuarterDays
	if min <= 0 {
		min = DefaultQuarterDays
	}
	type q struct{ y, n int }
	by := map[q][]time.Time{}
	for _, d := range days {
		k := q{d.Year(), edgar.QuarterOf(d)}
		by[k] = append(by[k], d)
	}
	handled := map[time.Time]bool{}
	keys := make([]q, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b q) int {
		if a.y != b.y {
			return a.y - b.y
		}
		return a.n - b.n
	})
	for _, k := range keys {
		ds := by[k]
		end := time.Date(k.y, time.Month(k.n*3+1), 1, 0, 0, 0, 0, time.UTC) // first day after the quarter
		if len(ds) < min || end.AddDate(0, 0, 2).After(today) {
			continue // too few days to be worth the download, or the quarter is not complete yet
		}
		entries, found, err := c.Indexer.Quarter(ctx, k.y, k.n)
		if err != nil {
			if perr := c.DB.PutFilingQuarter(ctx, store.QuarterRow{Year: k.y, Quarter: k.n, Status: "error", LastError: err.Error()}, now); perr != nil {
				return days, perr
			}
			c.log().Warn("reconcile: quarterly index failed, reading daily indexes", "year", k.y, "quarter", k.n, "err", err)
			rep.Errors++
			continue
		}
		if !found {
			continue
		}
		rep.Quarterly++
		byDay := map[string][]edgar.IndexEntry{}
		for _, e := range entries {
			byDay[e.Filed] = append(byDay[e.Filed], e)
		}
		wanted := 0
		for _, d := range ds {
			key := d.Format(time.DateOnly)
			list := byDay[key]
			res := store.DayResult{Day: key, Source: "quarterly", Listed: len(list), Status: store.DayComplete, Basis: "quarterly index", Forms: FormsKey(c.Forms)}
			for _, e := range edgar.Wanted(list, c.Forms) {
				res.Refs = append(res.Refs, refOf(e))
			}
			wanted += len(res.Refs)
			if len(list) == 0 {
				res.Status, res.Basis = store.DayNonFiling, "a completed quarterly index lists no filing for this weekday"
				rep.NonFiling++
			} else {
				rep.Complete++
			}
			added, err := c.DB.PutFilingDay(ctx, res, now)
			if err != nil {
				return days, err
			}
			rep.Added += added
			handled[d] = true
			*done++
		}
		if err := c.DB.PutFilingQuarter(ctx, store.QuarterRow{Year: k.y, Quarter: k.n, Status: "complete", Listed: len(entries), Wanted: wanted}, now); err != nil {
			return days, err
		}
	}
	var rest []time.Time
	for _, d := range days {
		if !handled[d] {
			rest = append(rest, d)
		}
	}
	return rest, nil
}

// EnrichReport says what Enrich did.
type EnrichReport struct {
	Companies, Requests, Found, Missing int
}

// SubmissionsURL builds the address of a company's record (it holds a %s for the
// ten-digit CIK); FileURL the address of an extra file of it.
type SubmissionsURL struct {
	Record string
	// File replaces the address of an extra file; it holds a %s for the file name.
	File string
}

func (u SubmissionsURL) fileURL(name string) string {
	if u.File != "" {
		return fmt.Sprintf(u.File, name)
	}
	return "https://data.sec.gov/submissions/" + name
}

// Enrich reads the SEC's record of each company for the wanted accessions and
// stores the filing metadata of those, and only those: the acceptance time when
// the record gives one, else just the filing day. want maps a CIK to the
// accessions to resolve. Older filings, kept by the record in extra files, are
// read only for accessions that the recent list does not hold, and only the
// files whose filing days could hold them.
func Enrich(ctx context.Context, db *store.DB, client httpx.Client, urls SubmissionsURL, want map[string][]string, filed map[string]string, now time.Time, maxRequests int) (EnrichReport, error) {
	var rep EnrichReport
	ciks := make([]string, 0, len(want))
	for c := range want {
		ciks = append(ciks, c)
	}
	slices.Sort(ciks)
	for _, cik := range ciks {
		if maxRequests > 0 && rep.Requests >= maxRequests {
			return rep, nil
		}
		rep.Companies++
		missing := map[string]bool{}
		for _, a := range want[cik] {
			missing[a] = true
		}
		var metas []store.FilingMeta
		take := func(list []fundamentals.Filing) {
			for _, f := range list {
				if !missing[f.Accession] {
					continue
				}
				delete(missing, f.Accession)
				m := store.FilingMeta{Accession: f.Accession, CIK: cik, Form: f.Form, FiledDay: f.Filed, ReportDay: f.Report, Document: f.Document, Source: "sec-submissions"}
				if t, ok := f.AcceptedTime(); ok {
					m.Accepted = t
				}
				metas = append(metas, m)
				rep.Found++
			}
		}
		rep.Requests++
		resp, err := client.Do(ctx, httpx.Request{URL: fmt.Sprintf(urls.Record, cik)})
		if err != nil {
			return rep, fmt.Errorf("record of company %s: %w", cik, err)
		}
		sub, err := fundamentals.ParseSubmissions(resp.Body)
		if err != nil {
			return rep, fmt.Errorf("record of company %s: %w", cik, err)
		}
		take(sub.Recent)
		for _, f := range sub.Files {
			if len(missing) == 0 || (maxRequests > 0 && rep.Requests >= maxRequests) {
				break
			}
			if !coversAny(f, missing, filed) {
				continue
			}
			rep.Requests++
			part, err := client.Do(ctx, httpx.Request{URL: urls.fileURL(f.Name)})
			if err != nil {
				return rep, fmt.Errorf("record of company %s, %s: %w", cik, f.Name, err)
			}
			more, err := fundamentals.ParseSubmissions(part.Body)
			if err != nil {
				return rep, fmt.Errorf("record of company %s, %s: %w", cik, f.Name, err)
			}
			take(more.Recent)
		}
		rep.Missing += len(missing)
		if _, err := db.PutFilingMeta(ctx, metas, now); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// coversAny says whether the extra file's filing days could hold one of the
// accessions: when no filing day is known for an accession, the file might.
func coversAny(f fundamentals.FileRef, missing map[string]bool, filed map[string]string) bool {
	for a := range missing {
		d := filed[a]
		if d == "" || (d >= f.From && d <= f.To) {
			return true
		}
	}
	return false
}

// Ingester stores articles of a source.
type Ingester interface {
	Ingest(ctx context.Context, src model.Source, articles []model.Article) (int, error)
}

// RepairReport says what Repair did.
type RepairReport struct {
	Missing, Stored    int
	Late, Historical   int
	TimeKnown, DayOnly int
}

// Repair stores an article for each wanted filing in [from, to] that no stored
// article has, marked as a backfill before it is stored: it is evidence, but it
// was not news when this system stored it, so alerts do not treat it as new. The
// article is dated to the acceptance time when the filing's metadata has one;
// otherwise to the start of its filing day, and that precision is recorded.
func Repair(ctx context.Context, db *store.DB, ing Ingester, cfg edgar.Config, src model.Source, from, to string, runID int64, now time.Time) (RepairReport, error) {
	var rep RepairReport
	missing, err := db.MissingFilings(ctx, from, to)
	if err != nil {
		return rep, err
	}
	rep.Missing = len(missing)
	if len(missing) == 0 {
		return rep, nil
	}
	accs := make([]string, len(missing))
	for i, m := range missing {
		accs[i] = m.Accession
	}
	metas, err := db.FilingMetas(ctx, accs)
	if err != nil {
		return rep, err
	}
	var arts []model.Article
	var marks []store.Backfill
	today := day(now)
	for _, m := range missing {
		acc, perr := edgar.ParseAccession(m.Accession)
		if perr != nil {
			continue
		}
		var accepted time.Time
		prec := store.PrecisionDay
		if meta, ok := metas[m.Accession]; ok && !meta.Accepted.IsZero() {
			accepted, prec = meta.Accepted, store.PrecisionTime
			rep.TimeKnown++
		} else {
			rep.DayOnly++
		}
		arts = append(arts, cfg.ArticleAt(src, edgar.IndexEntry{CIK: m.CIK, Company: m.Company, Form: m.Form, Filed: m.Filed, Accession: acc}, now, accepted))
		kind := store.BackfillHistorical
		if d, perr := time.Parse(time.DateOnly, m.Filed); perr == nil && !d.Before(today.AddDate(0, 0, -LateDays)) {
			kind = store.BackfillLate
			rep.Late++
		} else {
			rep.Historical++
		}
		marks = append(marks, store.Backfill{ArticleID: m.Accession, Kind: kind, RunID: runID, FiledDay: m.Filed, Precision: prec,
			Reason: "listed by the EDGAR index, missed by the live feed"})
	}
	if err := db.PutBackfill(ctx, marks, now); err != nil {
		return rep, err
	}
	n, err := ing.Ingest(ctx, src, arts)
	rep.Stored = n
	return rep, err
}
