package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/marketdata"
)

func testFact(symbol, ref string, avail time.Time, eps float64) eventfact.Record {
	key := eventfact.NewKey(eventfact.Earnings, symbol, "Q1 2026")
	return eventfact.Record{
		ID: eventfact.ID(key), Key: key, Category: eventfact.Earnings, Type: eventfact.EarningsReported,
		Symbol: symbol, PublishedAt: avail, AvailableAt: avail, Precision: eventfact.PrecisionTime,
		Source: eventfact.SourceFiling, SourceRef: ref, Evidence: []string{ref},
		Facts: []eventfact.Fact{{Name: "eps", Value: eps, Unit: "USD/share"}},
		Basis: eventfact.Known, Extractor: "test", Version: eventfact.Version,
	}
}

func TestEventFactsAreAppendedPerChangeAndReadAsOf(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	r1 := testFact("AAA", "ref-1", at(1), 1.5)
	if n, err := db.PutEventFacts(ctx, []eventfact.Record{r1}, at(2)); n != 1 || err != nil {
		t.Fatalf("first put = %d, %v", n, err)
	}
	if n, err := db.PutEventFacts(ctx, []eventfact.Record{r1}, at(3)); n != 0 || err != nil {
		t.Fatalf("same record again = %d, %v", n, err)
	}
	r2 := testFact("AAA", "ref-1", at(1), 1.6)
	if n, err := db.PutEventFacts(ctx, []eventfact.Record{r2}, at(10)); n != 1 || err != nil {
		t.Fatalf("changed record = %d, %v", n, err)
	}
	now, err := db.EventFacts(ctx, EventFactQuery{Symbol: "AAA", Types: []eventfact.Type{eventfact.EarningsReported}})
	if err != nil || len(now) != 1 || !reflect.DeepEqual(now[0], r2) {
		t.Errorf("as it stands = %+v, %v", now, err)
	}
	then, err := db.EventFacts(ctx, EventFactQuery{AsOf: at(5)})
	if err != nil || len(then) != 1 || !reflect.DeepEqual(then[0], r1) {
		t.Errorf("as of before the change = %+v, %v", then, err)
	}
	if h, err := db.EventFactHistory(ctx, r1.ID); err != nil || len(h) != 2 || h[0].Revision != 1 || h[1].Revision != 2 {
		t.Errorf("history = %+v, %v", h, err)
	}
	if c, err := db.EventFactCounts(ctx); err != nil || len(c) != 1 || c[0].Records != 1 || c[0].Known != 1 {
		t.Errorf("counts = %+v, %v", c, err)
	}
}

func TestEventFactsRefuseInvalidRecordsAndAnyRewrite(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	good := testFact("AAA", "ref-1", at(1), 1.5)
	bad := testFact("BBB", "ref-2", at(1), 1.5)
	bad.Precision = "never"
	if n, err := db.PutEventFacts(ctx, []eventfact.Record{good, bad}, at(2)); err == nil || n != 0 {
		t.Fatalf("invalid record = %d, %v", n, err)
	}
	if got, _ := db.EventFacts(ctx, EventFactQuery{}); len(got) != 0 {
		t.Errorf("nothing may be written: %+v", got)
	}
	if _, err := db.PutEventFacts(ctx, []eventfact.Record{good}, at(2)); err != nil {
		t.Fatal(err)
	}
	h, _ := db.baseHandle()
	if _, err := h.w.ExecContext(ctx, `UPDATE event_fact SET symbol = 'X'`); err == nil {
		t.Error("update must fail")
	}
	if _, err := h.w.ExecContext(ctx, `DELETE FROM event_fact`); err == nil {
		t.Error("delete must fail")
	}
}

func TestCandleSeriesReturnsARangeAndHonoursKnown(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	c := func(ticker, day string, closePrice float64) marketdata.Candle {
		return marketdata.Candle{Ticker: ticker, Day: day, Open: 10, High: 20, Low: 5, Close: closePrice, Volume: 1}
	}
	db.SetClock(func() time.Time { return at(1) })
	if err := db.PutCandles(ctx, []marketdata.Candle{c("AAA", "2026-03-03", 11), c("AAA", "2026-03-02", 10), c("AAA", "2026-03-04", 12), c("BBB", "2026-03-03", 7)}); err != nil {
		t.Fatal(err)
	}
	db.SetClock(func() time.Time { return at(5) })
	if err := db.PutCandles(ctx, []marketdata.Candle{c("AAA", "2026-03-03", 15)}); err != nil {
		t.Fatal(err)
	}
	got, err := db.CandleSeries(ctx, []string{"AAA"}, "2026-03-03", "", time.Time{})
	if err != nil || len(got) != 1 || len(got["AAA"]) != 2 || got["AAA"][0].Day != "2026-03-03" || got["AAA"][0].Close != 15 || got["AAA"][1].Day != "2026-03-04" {
		t.Errorf("range = %+v, %v", got, err)
	}
	then, err := db.CandleSeries(ctx, nil, "", "2026-03-03", at(2))
	if err != nil || len(then["AAA"]) != 2 || then["AAA"][1].Close != 11 || len(then["BBB"]) != 1 {
		t.Errorf("as held then = %+v, %v", then, err)
	}
}
