package reconcile

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// fakeSEC serves daily and quarterly indexes and companies' records.
type fakeSEC struct {
	days     map[string]string // YYYYMMDD -> body; absent: 404
	failing  map[string]bool   // YYYYMMDD -> the request fails
	quarters map[string][]byte // "2026/QTR3" -> gzip body
	records  map[string]string // CIK -> JSON
	calls    []string
}

func newSEC() *fakeSEC {
	return &fakeSEC{days: map[string]string{}, failing: map[string]bool{}, quarters: map[string][]byte{}, records: map[string]string{}}
}

func (s *fakeSEC) Do(_ context.Context, req httpx.Request) (*httpx.Response, error) {
	s.calls = append(s.calls, req.URL)
	notFound := &httpx.Error{Class: httpx.Permanent, Status: 404, URL: req.URL}
	switch {
	case strings.Contains(req.URL, "/daily-index/"):
		d := strings.TrimSuffix(req.URL[strings.LastIndex(req.URL, "/master.")+len("/master."):], ".idx")
		if s.failing[d] {
			return nil, &httpx.Error{Class: httpx.Retryable, Status: 503, URL: req.URL}
		}
		if body, ok := s.days[d]; ok {
			return &httpx.Response{Status: 200, Body: []byte(body)}, nil
		}
		return nil, notFound
	case strings.Contains(req.URL, "/full-index/"):
		key := req.URL[strings.Index(req.URL, "full-index/")+len("full-index/") : strings.LastIndex(req.URL, "/master.gz")]
		if body, ok := s.quarters[key]; ok {
			return &httpx.Response{Status: 200, Body: body}, nil
		}
		return nil, notFound
	case strings.Contains(req.URL, "/sub/"):
		cik := strings.TrimSuffix(req.URL[strings.LastIndex(req.URL, "CIK")+3:], ".json")
		if body, ok := s.records[cik]; ok {
			return &httpx.Response{Status: 200, Body: []byte(body)}, nil
		}
		return nil, notFound
	}
	return nil, notFound
}

func (s *fakeSEC) daily() int {
	n := 0
	for _, c := range s.calls {
		if strings.Contains(c, "/daily-index/") {
			n++
		}
	}
	return n
}

const header = "Description: Master Index of EDGAR Dissemination Feed\nLast Data Received: x\n\nCIK|Company Name|Form Type|Date Filed|Filename\n--------------------------------------------------------------------------------\n"

// line is one filing of the index: the accession is built from the CIK and a number.
func line(cik int, form, filed string, n int) string {
	return fmt.Sprintf("%d|Company %d|%s|%s|edgar/data/%d/%010d-26-%06d.txt\n", cik, cik, form, filed, cik, cik, n)
}

func index(lines ...string) string { return header + strings.Join(lines, "") }

func gz(t *testing.T, s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return b.Bytes()
}

func setup(t *testing.T, sec *fakeSEC, now time.Time) (Config, *store.DB) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return Config{DB: db, Indexer: edgar.Indexer{Client: sec, Config: edgar.Config{WebBase: "https://sec.test"}}, Forms: []string{"8-K"}, Now: func() time.Time { return now }}, db
}

// Wednesday 11 November 2026 noon: the 10th is a Tuesday, over for six hours.
var now = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func d(s string) time.Time { t, _ := time.Parse(time.DateOnly, s); return t }

func TestAnIndexThatIsNotPublishedYetStaysRetryableAndIsReadLater(t *testing.T) {
	sec := newSEC()
	cfg, db := setup(t, sec, now)
	ctx := context.Background()
	// Tuesday 10 November: no index yet.
	rep, err := cfg.Discover(ctx, d("2026-11-10"), d("2026-11-10"), "range")
	if err != nil || rep.Unpublished != 1 || rep.Pending != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	days, _ := db.IndexDays(ctx, "2026-11-10")
	if days["2026-11-10"].Status != store.DayUnpublished || days["2026-11-10"].Status.Resolved() {
		t.Fatalf("an index the SEC had not published is not a resolved day: %+v", days)
	}
	// The SEC publishes it, long after the day was over: the old rule skipped it for good.
	sec.days["20261110"] = index(line(1000001, "8-K", "2026-11-10", 1), line(1000002, "10-K", "2026-11-10", 2))
	cfg.Now = func() time.Time { return now.AddDate(0, 0, 5) }
	rep, err = cfg.Discover(ctx, d("2026-11-10"), d("2026-11-10"), "range")
	if err != nil || rep.Complete != 1 || rep.Added != 1 || rep.Pending != 0 {
		t.Fatalf("it must be read now: %+v %v", rep, err)
	}
	days, _ = db.IndexDays(ctx, "2026-11-10")
	if days["2026-11-10"].Status != store.DayComplete || days["2026-11-10"].Attempts != 2 {
		t.Errorf("%+v", days["2026-11-10"])
	}
	// Resolved: a third run does not ask again.
	before := sec.daily()
	if rep, _ = cfg.Discover(ctx, d("2026-11-10"), d("2026-11-10"), "range"); sec.daily() != before || rep.Skipped != 1 {
		t.Errorf("a resolved day is not read again: %+v", rep)
	}
}

func TestWeekendsAndHolidaysAreExplicitNonFilingDaysAndOthersAreNot(t *testing.T) {
	sec := newSEC()
	cfg, db := setup(t, sec, now)
	ctx := context.Background()
	// Thursday 5 to Monday 9 November: Fri 6th has an index, Sat 7th and Sun 8th are weekend, Mon 9th is missing.
	sec.days["20261105"] = index(line(1000001, "8-K", "2026-11-05", 1))
	sec.days["20261106"] = index(line(1000001, "8-K", "2026-11-06", 2))
	rep, _ := cfg.Discover(ctx, d("2026-11-05"), d("2026-11-09"), "range")
	days, _ := db.IndexDays(ctx, "2026-11-05")
	if days["2026-11-07"].Status != store.DayNonFiling || !strings.HasPrefix(days["2026-11-07"].Basis, "calendar: Saturday") {
		t.Errorf("Saturday: %+v", days["2026-11-07"])
	}
	if days["2026-11-09"].Status != store.DayUnpublished {
		t.Errorf("an ordinary Monday without an index is unpublished, not a holiday: %+v", days["2026-11-09"])
	}
	if rep.Daily != 3 {
		t.Errorf("weekends need no request: %d daily reads", rep.Daily)
	}
	// Thanksgiving 2026 is Thursday 26 November: no index is a holiday, by the calendar, once.
	cfg.Now = func() time.Time { return d("2026-12-05") }
	rep, _ = cfg.Discover(ctx, d("2026-11-26"), d("2026-11-26"), "range")
	days, _ = db.IndexDays(ctx, "2026-11-26")
	if days["2026-11-26"].Status != store.DayNonFiling || !strings.Contains(days["2026-11-26"].Basis, "Thanksgiving") {
		t.Errorf("%+v", days["2026-11-26"])
	}
	for date, want := range map[string]string{"2026-01-01": "New Year's Day", "2026-07-03": "Independence Day", "2027-12-31": "New Year's Day", "2026-06-19": "Juneteenth", "2026-09-07": "Labor Day"} {
		if got, _, nf := edgar.NonFilingDay(d(date)); !nf || got != want {
			t.Errorf("%s: %q %v", date, got, nf)
		}
	}
	if _, _, nf := edgar.NonFilingDay(d("2026-11-10")); nf {
		t.Error("an ordinary Tuesday")
	}
}

func TestAnInterruptedRunContinuesWhereItStopped(t *testing.T) {
	sec := newSEC()
	cfg, db := setup(t, sec, now)
	ctx := context.Background()
	for _, day := range []string{"20261102", "20261103", "20261104", "20261105", "20261106"} {
		sec.days[day] = index(line(1000001, "8-K", day[:4]+"-"+day[4:6]+"-"+day[6:], int(day[7]-'0')))
	}
	sec.failing["20261104"], sec.failing["20261105"], sec.failing["20261106"] = true, true, true
	rep, err := cfg.Discover(ctx, d("2026-11-02"), d("2026-11-06"), "range")
	if err == nil || rep.Stopped == "" || rep.Complete != 2 || rep.Errors != 3 {
		t.Fatalf("three failures in a row stop the run: %+v %v", rep, err)
	}
	run, ok, _ := db.LastReconcileRun(ctx)
	if !ok || run.DaysDone != 5 || !run.Finished.IsZero() {
		t.Errorf("progress is recorded and the run is not marked finished: %+v", run)
	}
	delete(sec.failing, "20261104")
	delete(sec.failing, "20261105")
	delete(sec.failing, "20261106")
	sec.calls = nil
	rep, err = cfg.Discover(ctx, d("2026-11-02"), d("2026-11-06"), "range")
	if err != nil || sec.daily() != 3 || rep.Skipped != 2 || rep.Pending != 0 {
		t.Fatalf("only the three unresolved days are read: %d reads %+v %v", sec.daily(), rep, err)
	}
	if missing, _ := db.MissingFilings(ctx, "2026-11-02", "2026-11-06"); len(missing) != 5 {
		t.Errorf("every filing is known once: %d", len(missing))
	}
}

func TestARollingRunCatchesUpAfterAnOutageLongerThanItsWindow(t *testing.T) {
	sec := newSEC()
	cfg, db := setup(t, sec, now)
	ctx := context.Background()
	// The last day resolved is Friday 16 October; today is 11 November and the window is 7 days.
	if _, err := db.PutFilingDay(ctx, store.DayResult{Day: "2026-10-16", Status: store.DayComplete, Source: "daily"}, now); err != nil {
		t.Fatal(err)
	}
	from, to, extended, cut, err := cfg.Rolling(ctx, 7, 90)
	if err != nil || from.Format(time.DateOnly) != "2026-10-17" || to.Format(time.DateOnly) != "2026-11-10" || extended != 18 || cut {
		t.Fatalf("%s %s %d %v %v", from, to, extended, cut, err)
	}
	// An outage longer than the catch-up limit is cut, and says so.
	from, _, _, cut, _ = cfg.Rolling(ctx, 7, 20)
	if !cut || from.Format(time.DateOnly) != "2026-10-22" {
		t.Errorf("%s %v", from, cut)
	}
	// With nothing stored, the window is the overlap.
	empty, _ := setup(t, sec, now)
	if from, _, extended, _, _ := empty.Rolling(ctx, 7, 90); from.Format(time.DateOnly) != "2026-11-04" || extended != 0 {
		t.Errorf("%s %d", from, extended)
	}
}

func TestALongHistoricalRangeUsesTheQuarterlyIndexAndDailyOnesForTheRest(t *testing.T) {
	sec := newSEC()
	cfg, db := setup(t, sec, now)
	ctx := context.Background()
	var lines []string
	// Q3 2026: filings on 1 July, and on 6 July twice; nothing on the other weekdays, so they are non-filing days.
	lines = append(lines, line(1000001, "8-K", "2026-07-01", 1), line(1000002, "8-K", "2026-07-06", 2), line(1000003, "8-K", "2026-07-06", 3), line(1000004, "10-Q", "2026-07-06", 4))
	sec.quarters["2026/QTR3"] = gz(t, index(lines...))
	// The last days of October are read daily.
	sec.days["20261029"] = index(line(1000005, "8-K", "2026-10-29", 5))
	sec.days["20261030"] = index(line(1000005, "8-K", "2026-10-30", 6))
	rep, err := cfg.Discover(ctx, d("2026-07-01"), d("2026-07-31"), "range")
	if err != nil || rep.Quarterly != 1 || rep.Daily != 0 {
		t.Fatalf("a month of weekdays in an ended quarter is read from the quarterly index: %+v %v", rep, err)
	}
	if rep.Added != 3 {
		t.Errorf("only wanted forms of the range's days are stored: %d", rep.Added)
	}
	days, _ := db.IndexDays(ctx, "2026-07-01", "2026-07-31")
	if days["2026-07-06"].Status != store.DayComplete || days["2026-07-06"].Listed != 3 {
		t.Errorf("%+v", days["2026-07-06"])
	}
	if days["2026-07-07"].Status != store.DayNonFiling || !strings.Contains(days["2026-07-07"].Basis, "quarterly") {
		t.Errorf("an empty weekday of a completed quarter is a non-filing day with its basis: %+v", days["2026-07-07"])
	}
	// The same filings read again from the daily index are not stored twice.
	sec.days["20260706"] = index(line(1000002, "8-K", "2026-07-06", 2))
	cfg.Refetch, cfg.NoQuarterly = true, true
	rep, _ = cfg.Discover(ctx, d("2026-07-06"), d("2026-07-06"), "range")
	if rep.Added != 0 {
		t.Errorf("deduplicated by accession: %d", rep.Added)
	}
	// The current quarter is not complete: its days are read daily.
	cfg.Refetch, cfg.NoQuarterly = false, false
	cfg.QuarterDays = 1
	rep, _ = cfg.Discover(ctx, d("2026-10-29"), d("2026-10-30"), "range")
	if rep.Quarterly != 0 || rep.Daily != 2 {
		t.Errorf("an unfinished quarter has no complete index: %+v", rep)
	}
}

type recorder struct{ got []model.Article }

func (r *recorder) Ingest(_ context.Context, _ model.Source, arts []model.Article) (int, error) {
	r.got = append(r.got, arts...)
	return len(arts), nil
}

func TestRepairMarksBackfillsKeepsDayPrecisionAndIsRepeatable(t *testing.T) {
	sec := newSEC()
	cfg, db := setup(t, sec, now)
	ctx := context.Background()
	sec.days["20261110"] = index(line(1000001, "8-K", "2026-11-10", 1), line(1000002, "8-K", "2026-11-10", 2))
	sec.days["20261016"] = index(line(1000003, "8-K", "2026-10-16", 3))
	cfg.Discover(ctx, d("2026-10-16"), d("2026-10-16"), "range")
	cfg.Discover(ctx, d("2026-11-10"), d("2026-11-10"), "range")

	// The record of company 1 gives an acceptance time for its filing; company 2's gives none.
	acc1, acc2, acc3 := "0001000001-26-000001", "0001000002-26-000002", "0001000003-26-000003"
	meta := []store.FilingMeta{{Accession: acc1, CIK: "0001000001", Form: "8-K", FiledDay: "2026-11-10", Accepted: time.Date(2026, 11, 10, 21, 30, 5, 0, time.UTC), Source: "test"}}
	if _, err := db.PutFilingMeta(ctx, meta, now); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	src := model.Source{ID: "sec-edgar"}
	rep, err := Repair(ctx, db, rec, edgar.Config{WebBase: "https://sec.test"}, src, "2026-10-01", "2026-11-10", 7, now)
	if err != nil || rep.Missing != 3 || rep.Stored != 3 || rep.Late != 2 || rep.Historical != 1 || rep.TimeKnown != 1 || rep.DayOnly != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	byID := map[string]model.Article{}
	for _, a := range rec.got {
		byID[a.ID] = a
	}
	if !byID[acc1].Published.Equal(time.Date(2026, 11, 10, 21, 30, 5, 0, time.UTC)) {
		t.Errorf("a known acceptance time is the publication time: %s", byID[acc1].Published)
	}
	if !byID[acc2].Published.Equal(d("2026-11-10")) || !strings.Contains(strings.Join(byID[acc2].Score.Reasons, " "), "filing time unknown") {
		t.Errorf("an unknown time is never made up; the day is kept and the article says so: %s %v", byID[acc2].Published, byID[acc2].Score.Reasons)
	}
	marks, _ := db.Backfilled(ctx, []string{acc1, acc2, acc3})
	if len(marks) != 3 || marks[acc1].Precision != store.PrecisionTime || marks[acc2].Precision != store.PrecisionDay || marks[acc2].Kind != store.BackfillLate || marks[acc3].Kind != store.BackfillHistorical {
		t.Errorf("%+v", marks)
	}
	// Repair twice: the marks stay as first made, nothing is marked again.
	first := marks[acc2].Recovered
	later := now.Add(48 * time.Hour)
	if _, err := Repair(ctx, db, rec, edgar.Config{WebBase: "https://sec.test"}, src, "2026-10-01", "2026-11-10", 8, later); err != nil {
		t.Fatal(err)
	}
	if again, _ := db.Backfilled(ctx, []string{acc2}); !again[acc2].Recovered.Equal(first) || again[acc2].RunID != 7 {
		t.Errorf("a repeat repair must not rewrite the mark: %+v", again[acc2])
	}
}

func TestEnrichResolvesWantedAccessionsOnlyAndNeverMakesUpATime(t *testing.T) {
	sec := newSEC()
	_, db := setup(t, sec, now)
	ctx := context.Background()
	sec.records["0001000001"] = `{"filings":{"recent":{
		"accessionNumber":["0001000001-26-000001","0001000001-26-000002","0001000001-26-000009"],
		"form":["8-K","8-K","10-K"],"filingDate":["2026-11-10","2026-11-09","2026-02-01"],"reportDate":["","",""],"primaryDocument":["a.htm","b.htm","c.htm"],
		"acceptanceDateTime":["2026-11-10T21:30:05.000Z","","2026-02-01T10:00:00.000Z"]},
		"files":[{"name":"CIK0001000001-submissions-001.json","filingFrom":"2010-01-01","filingTo":"2019-12-31"}]}}`
	client := sec
	urls := SubmissionsURL{Record: "https://sec.test/sub/CIK%s.json", File: "https://sec.test/sub/%s"}
	rep, err := Enrich(ctx, db, client, urls, map[string][]string{"0001000001": {"0001000001-26-000001", "0001000001-26-000002"}}, map[string]string{"0001000001-26-000001": "2026-11-10", "0001000001-26-000002": "2026-11-09"}, now, 0)
	if err != nil || rep.Found != 2 || rep.Missing != 0 || rep.Requests != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	got, _ := db.FilingMetas(ctx, []string{"0001000001-26-000001", "0001000001-26-000002", "0001000001-26-000009"})
	if len(got) != 2 {
		t.Errorf("the 10-K of the same record was not asked for and is not imported: %d", len(got))
	}
	if m := got["0001000001-26-000001"]; m.Precision != store.PrecisionTime || !m.Accepted.Equal(time.Date(2026, 11, 10, 21, 30, 5, 0, time.UTC)) {
		t.Errorf("%+v", m)
	}
	if m := got["0001000001-26-000002"]; m.Precision != store.PrecisionDay || !m.Accepted.IsZero() || m.FiledDay != "2026-11-09" {
		t.Errorf("a record with no acceptance time gives a filing day only: %+v", m)
	}
	// An accession the record does not list is reported, and the older files are read only when their days could hold it.
	rep, _ = Enrich(ctx, db, client, urls, map[string][]string{"0001000001": {"0001000001-26-000077"}}, map[string]string{"0001000001-26-000077": "2026-11-09"}, now, 0)
	if rep.Missing != 1 || rep.Requests != 1 {
		t.Errorf("a recent filing day is not in a file of 2010 to 2019: %+v", rep)
	}
	var he *httpx.Error
	if _, err := Enrich(ctx, db, client, urls, map[string][]string{"0009999999": {"x"}}, nil, now, 0); !errors.As(err, &he) {
		t.Errorf("an unknown company is an error: %v", err)
	}
}
