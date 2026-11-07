package store

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

func TestCandlesAreReplacedPerDayAndTheLatestTwoComeBack(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	c := func(day string, closePrice float64) marketdata.Candle {
		return marketdata.Candle{Ticker: "KO", Day: day, Open: 70, High: 75, Low: 69, Close: closePrice, Volume: 10}
	}
	if err := db.PutCandles(ctx, []marketdata.Candle{c("2026-11-02", 70), c("2026-11-03", 71), c("2026-11-04", 72),
		{Ticker: "BAD", Day: "2026-11-04", Open: 1, High: 0, Low: 1, Close: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutCandles(ctx, []marketdata.Candle{c("2026-11-04", 73)}); err != nil { // the day again, corrected
		t.Fatal(err)
	}
	got, err := db.LatestCandles(ctx)
	if err != nil || len(got["KO"]) != 2 || got["KO"][0].Close != 73 || got["KO"][1].Day != "2026-11-03" {
		t.Errorf("latest = %+v, %v", got, err)
	}
	if _, ok := got["BAD"]; ok {
		t.Error("a candle that cannot be drawn is not stored")
	}
	if n, _ := db.NewestDays(ctx); n["KO"] != "2026-11-04" {
		t.Errorf("newest = %v", n)
	}
	if stocks, newest, _ := db.CandleStats(ctx); stocks != 1 || newest != "2026-11-04" {
		t.Errorf("stats = %d %s", stocks, newest)
	}
	checks := map[string]marketdata.Check{"KO": {CheckedAt: at(1), Found: 3}, "day:2026-11-03": {CheckedAt: at(2)}}
	if err := db.SaveChecks(ctx, checks); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Checks(ctx); len(got) != 2 || got["KO"].Found != 3 || !got["KO"].CheckedAt.Equal(at(1)) {
		t.Errorf("checks = %+v", got)
	}
}

func TestCandlesKeepTheirBasisAndSplitsAreOnlyAdded(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	rows := []marketdata.Candle{
		{Ticker: "KO", Day: "2026-11-03", Open: 70, High: 75, Low: 69, Close: 71, Raw: true},
		{Ticker: "KO", Day: "2026-11-02", Open: 70, High: 75, Low: 69, Close: 70},
	}
	if err := db.PutCandles(ctx, rows); err != nil {
		t.Fatal(err)
	}
	got, _ := db.LatestCandles(ctx)
	if len(got["KO"]) != 2 || !got["KO"][0].Raw || got["KO"][1].Raw {
		t.Errorf("a candle remembers whether it is raw: %+v", got["KO"])
	}
	cov, err := db.SplitCoverage(ctx)
	if err != nil || cov.Since != "" || !cov.Read.IsZero() {
		t.Errorf("nothing was read yet: %+v, %v", cov, err)
	}
	s := marketdata.Split{Ticker: "KO", Day: "2026-11-03", From: 1, To: 2}
	if err := db.PutSplits(ctx, []marketdata.Split{s, {Ticker: "X", Day: "2026-11-03", From: 0, To: 2}}, at(10), at(20)); err != nil {
		t.Fatal(err)
	}
	// The same split again, from a later reading that starts later.
	if err := db.PutSplits(ctx, []marketdata.Split{s}, at(500), at(600)); err != nil {
		t.Fatal(err)
	}
	all, _ := db.Splits(ctx)
	if len(all) != 1 || all[0] != s {
		t.Errorf("splits = %+v", all)
	}
	cov, _ = db.SplitCoverage(ctx)
	if cov.Since != at(10).Format("2006-01-02") || !cov.Read.Equal(at(600)) {
		t.Errorf("coverage keeps the earliest start and the latest reading: %+v", cov)
	}
}

func TestAPastValuationReadsTheCandlesHeldThenAndKeepsReplacedVersions(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	put := func(fetched time.Time, cs ...marketdata.Candle) {
		t.Helper()
		db.now = func() time.Time { return fetched }
		if err := db.PutCandles(ctx, cs); err != nil {
			t.Fatal(err)
		}
	}
	c := func(d string, closePrice float64, raw bool, cur string) marketdata.Candle {
		return marketdata.Candle{Ticker: "KO", Day: d, Open: 70, High: 75, Low: 69, Close: closePrice, Raw: raw, Currency: cur}
	}
	// Stored on the 5th, adjusted (basis unknown); replaced on the 9th by the raw price.
	put(day.AddDate(0, 0, 1), c("2026-11-03", 71, false, ""), c("2026-11-04", 72, false, ""))
	put(day.AddDate(0, 0, 5), c("2026-11-04", 72, true, "USD"))
	// The same numbers again write no revision.
	put(day.AddDate(0, 0, 6), c("2026-11-04", 72, true, "USD"))
	var revs int
	h, _ := db.baseHandle()
	h.r.QueryRow(`SELECT COUNT(*) FROM candle_revision`).Scan(&revs)
	if revs != 1 {
		t.Errorf("revisions = %d, want only the one change", revs)
	}

	current, _ := db.LatestCandles(ctx)
	if cs := current["KO"]; len(cs) != 2 || !cs[0].Raw || cs[0].Currency != "USD" || cs[0].FetchedAt.IsZero() {
		t.Fatalf("current = %+v", cs)
	}
	// Held on the 7th: the adjusted candle, not the later raw one, and no currency.
	held, err := db.CandlesAsOf(ctx, day.AddDate(0, 0, 3), day.AddDate(0, 0, 3))
	if err != nil || len(held["KO"]) != 2 || held["KO"][0].Raw || held["KO"][0].Close != 72 || held["KO"][0].Currency != "" {
		t.Errorf("as held on the 7th = %+v, %v", held["KO"], err)
	}
	// Held on the 3rd: nothing, it was fetched on the 5th.
	if none, _ := db.CandlesAsOf(ctx, day, day); len(none["KO"]) != 0 {
		t.Errorf("held before the fetch: %+v", none["KO"])
	}
	// Reconstructed for the 4th at noon: the session is not over, only the 3rd counts,
	// and the version is today's (whenever found).
	rec, _ := db.CandlesAsOf(ctx, day, time.Time{})
	if len(rec["KO"]) != 1 || rec["KO"][0].Day != "2026-11-03" {
		t.Errorf("a session is public from the end of its day: %+v", rec["KO"])
	}
	rec, _ = db.CandlesAsOf(ctx, day.AddDate(0, 0, 1), time.Time{})
	if len(rec["KO"]) != 2 || rec["KO"][0].Day != "2026-11-04" || !rec["KO"][0].Raw {
		t.Errorf("reconstructed for the 5th = %+v", rec["KO"])
	}
}

func TestACandleWithoutARecordedFetchTimeCannotBeShownToHaveBeenHeld(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	h, _ := db.baseHandle()
	if _, err := h.w.Exec(`INSERT INTO candle (ticker, day, open, high, low, close, volume, raw, fetched_ms) VALUES ('OLD', '2026-01-02', 1, 2, 1, 2, 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if got, _ := db.CandlesAsOf(ctx, at, at); len(got["OLD"]) != 0 {
		t.Errorf("known view held a candle with no fetch time: %+v", got)
	}
	if got, _ := db.CandlesAsOf(ctx, at, time.Time{}); len(got["OLD"]) != 1 || !got["OLD"][0].FetchedAt.IsZero() {
		t.Errorf("reconstructed view: %+v", got)
	}
}

func TestSplitReadingsAreKeptSoCoverageCanBeToldAsOfATime(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	s := marketdata.Split{Ticker: "KO", Day: "2026-03-03", From: 1, To: 2}
	db.PutSplits(ctx, []marketdata.Split{s}, at(10), at(20))
	late := marketdata.Split{Ticker: "KO", Day: "2026-06-03", From: 1, To: 3}
	db.PutSplits(ctx, []marketdata.Split{late}, at(5), at(100))

	if all, _ := db.SplitsAsOf(ctx, at(50)); len(all) != 1 || all[0] != s {
		t.Errorf("a split first seen later is not held earlier: %+v", all)
	}
	if all, _ := db.SplitsAsOf(ctx, time.Time{}); len(all) != 2 {
		t.Errorf("splits now = %+v", all)
	}
	early, _ := db.SplitCoverageAsOf(ctx, at(50))
	now, _ := db.SplitCoverageAsOf(ctx, time.Time{})
	before, _ := db.SplitCoverageAsOf(ctx, at(1))
	if !early.Read.Equal(at(20)) || early.Since != at(10).Format(time.DateOnly) || !now.Read.Equal(at(100)) || now.Since != at(5).Format(time.DateOnly) || before.Since != "" {
		t.Errorf("coverage early %+v now %+v before %+v", early, now, before)
	}
}
