package store

import (
	"context"
	"testing"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

func TestMonthlyCandlesSelectsLastStoredSessionWithinBounds(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var candles []marketdata.Candle
	for _, row := range []struct {
		ticker, day string
		close       float64
	}{
		{"ONE", "2025-10-31", 80}, {"ONE", "2025-11-28", 90}, {"ONE", "2025-12-15", 100}, {"ONE", "2025-12-31", 101},
		{"ONE", "2026-11-06", 120}, {"ONE", "2026-11-30", 999}, {"TWO", "2025-12-30", 42},
	} {
		candles = append(candles, marketdata.Candle{Ticker: row.ticker, Day: row.day, Open: row.close, High: row.close, Low: row.close, Close: row.close, Raw: true, Currency: "USD"})
	}
	if err := db.PutCandles(ctx, candles); err != nil {
		t.Fatal(err)
	}
	rows, err := db.MonthlyCandles(ctx, "2025-11-01", "2026-11-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows["ONE"]) != 3 || rows["ONE"][1].Day != "2025-12-31" || rows["ONE"][1].Close != 101 || rows["ONE"][2].Close != 120 || len(rows["TWO"]) != 1 {
		t.Fatalf("monthly selection: %+v", rows)
	}
	if !rows["ONE"][0].Raw || rows["ONE"][0].Currency != "USD" {
		t.Fatal("lost price basis")
	}
}

func TestCalendarCandlesCombinesYearlyAndMonthlyEndpoints(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var candles []marketdata.Candle
	for _, day := range []string{"2015-12-31", "2016-06-30", "2016-12-30", "2017-06-30", "2017-12-29", "2025-06-30", "2025-10-31", "2025-11-28", "2025-12-31", "2026-01-15", "2026-01-30", "2026-11-06", "2026-11-30"} {
		candles = append(candles, marketdata.Candle{Ticker: "ONE", Day: day, Open: 100, High: 100, Low: 100, Close: 100, Raw: true})
	}
	if err := db.PutCandles(ctx, candles); err != nil {
		t.Fatal(err)
	}
	rows, err := db.CalendarCandles(ctx, "2016-01-01", "2025-11-01", "2026-11-08")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2016-12-30", "2017-12-29", "2025-10-31", "2025-11-28", "2025-12-31", "2026-01-30", "2026-11-06"}
	if len(rows["ONE"]) != len(want) {
		t.Fatalf("wrong endpoints: %+v", rows["ONE"])
	}
	for i, day := range want {
		if rows["ONE"][i].Day != day {
			t.Fatalf("endpoint %d: %s want %s", i, rows["ONE"][i].Day, day)
		}
	}
}
