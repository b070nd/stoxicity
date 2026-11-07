package store

import (
	"context"
	"testing"

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
