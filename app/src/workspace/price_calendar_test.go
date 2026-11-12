package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

func TestPriceCalendarsUseStoredHistoryAndRefreshAcrossMonthBoundary(t *testing.T) {
	dir := setup(t)
	write(t, dir, "config.yaml", "since: 2026-11-01\ntimezone: UTC\ndata_dir: ./data\n")
	at := time.Date(2026, 11, 30, 23, 59, 50, 0, time.UTC)
	w, err := Open(Options{Root: dir, Cwd: dir, Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	put := func(day string, price float64) {
		t.Helper()
		if err := w.db.PutCandles(context.Background(), []marketdata.Candle{{Ticker: "TEST", Day: day, Open: price, High: price, Low: price, Close: price, Raw: true, Currency: "USD"}}); err != nil {
			t.Fatal(err)
		}
	}
	put("2016-12-30", 100)
	put("2017-12-29", 110)
	put("2018-12-31", 110)
	put("2025-12-31", 95)
	put("2026-11-30", 100)
	first, err := w.PriceCalendars()
	if err != nil || first["TEST"].Months[0].Close != 95 || first["TEST"].Months[11].Close != 100 {
		t.Fatalf("history constrained by news window: %+v %v", first, err)
	}
	if first["TEST"].Years[0].Year != "2017" || first["TEST"].Years[0].Close != 110 || !first["TEST"].Years[0].HasChange || !first["TEST"].Years[1].HasChange || first["TEST"].Years[1].Change != 0 {
		t.Fatalf("annual endpoints/baseline not loaded: %+v", first["TEST"].Years)
	}
	put("2026-11-30", 110)
	at = at.Add(5 * time.Second)
	cached, err := w.PriceCalendars()
	if err != nil || cached["TEST"].Months[11].Close != 100 {
		t.Fatalf("cache missed: %+v %v", cached, err)
	}
	at = at.Add(10 * time.Second)
	next, err := w.PriceCalendars()
	if err != nil || next["TEST"].Months[0].Month != "2026-01" || next["TEST"].Months[10].Close != 110 || next["TEST"].Months[11].Day != "" {
		t.Fatalf("calendar did not roll over: %+v %v", next, err)
	}
	if first["TEST"].Months[11].Close != 100 {
		t.Fatal("read-only snapshot mutated")
	}
	at = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	newYear, err := w.PriceCalendars()
	if err != nil || newYear["TEST"].Years[0].Year != "2018" || !newYear["TEST"].Years[0].HasChange || newYear["TEST"].Years[9].Year != "2027" || newYear["TEST"].Years[9].Day != "" {
		t.Fatalf("annual rollover: %+v %v", newYear, err)
	}
}
