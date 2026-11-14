package store

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/marketdata"
)

type historySource func(context.Context, string) (marketdata.PriceHistory, error)

func (f historySource) History(ctx context.Context, ticker string) (marketdata.PriceHistory, error) {
	return f(ctx, ticker)
}

func TestPublicHistoryIsAtomicResumableAndSeparateFromDailyCandles(t *testing.T) {
	a, dir := openTemp(t)
	b := openAgain(t, a, dir)
	ctx := context.Background()
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	if err := a.PutCandles(ctx, []marketdata.Candle{{Ticker: "TEST", Day: "2026-11-06", Open: 123, High: 123, Low: 123, Close: 123, Raw: true}}); err != nil {
		t.Fatal(err)
	}
	claimed, err := a.ClaimHistory(ctx, "TEST", time.Time{}, at)
	if err != nil || !claimed {
		t.Fatalf("claim: %t %v", claimed, err)
	}
	claimed, err = b.ClaimHistory(ctx, "TEST", time.Time{}, at)
	if err != nil || claimed {
		t.Fatalf("duplicate lease: %t %v", claimed, err)
	}
	h := marketdata.PriceHistory{Ticker: "TEST", Symbol: "TEST", Currency: "USD", LatestDay: "2026-11-06", FetchedAt: at, Months: []marketdata.HistoryMonth{{Month: "2016-12", Close: 50}, {Month: "2017-12", Close: 60}, {Month: "2025-12", Close: 100}, {Month: "2026-11", Close: 110}}}
	if err := a.PutPriceHistory(ctx, h); err != nil {
		t.Fatal(err)
	}
	claimed, err = b.ClaimHistory(ctx, "TEST", time.Time{}, at)
	if err != nil || claimed {
		t.Fatalf("stale worker duplicated completed fetch: %t %v", claimed, err)
	}
	partial := h
	partial.FetchedAt = at.Add(time.Hour)
	partial.Months = append([]marketdata.HistoryMonth(nil), h.Months[1:]...)
	if err := a.PutPriceHistory(ctx, partial); err == nil {
		t.Fatal("partial refresh dropped previous history")
	}
	if err := a.HistoryFailure(ctx, "TEST", at.Add(time.Hour), "fixture provider failure"); err != nil {
		t.Fatal(err)
	}
	states, err := b.HistoryStates(ctx)
	if err != nil || states["TEST"].Months != 4 || !states["TEST"].FetchedAt.Equal(at) || states["TEST"].Problem != "fixture provider failure" {
		t.Fatalf("good snapshot lost or retimestamped: %+v %v", states, err)
	}
	rows, err := b.CalendarHistories(ctx, "2016-01", "2025-11", "2026-11")
	if err != nil || len(rows["TEST"].Months) != 4 {
		t.Fatalf("stored history: %+v %v", rows, err)
	}
	candles, err := b.LatestCandles(ctx)
	if err != nil || len(candles["TEST"]) != 1 || candles["TEST"][0].Close != 123 || !candles["TEST"][0].Raw {
		t.Fatalf("monthly history polluted daily candle: %+v %v", candles, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b = openAgain(t, a, dir)
	states, err = b.HistoryStates(ctx)
	if err != nil || states["TEST"].Months != 4 {
		t.Fatalf("history not durable: %+v %v", states, err)
	}
}

func TestHistoryKeeperFillsMissingFirstAndStopsOnProviderThrottle(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	old := marketdata.PriceHistory{Ticker: "OLD", Symbol: "OLD", Currency: "USD", LatestDay: "2026-11-06", FetchedAt: at.Add(-48 * time.Hour), Months: []marketdata.HistoryMonth{{Month: "2026-11", Close: 100}}}
	if err := db.PutPriceHistory(ctx, old); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	k := marketdata.HistoryKeeper{Store: db, Now: func() time.Time { return at }, Source: historySource(func(ctx context.Context, ticker string) (marketdata.PriceHistory, error) {
		calls = append(calls, ticker)
		return marketdata.PriceHistory{}, &httpx.Error{Class: httpx.Throttled, Status: 429, RetryAfter: time.Hour}
	})}
	rep, err := k.Refresh(ctx, []string{"OLD", "B", "C"}, []string{"OLD"}, 0, false)
	if err == nil || len(calls) != 1 || calls[0] != "B" || rep.Left != 3 {
		t.Fatalf("did not prioritize missing or stop: %+v %v %v", rep, calls, err)
	}
	states, err := db.HistoryStates(ctx)
	if err != nil || !states["B"].CheckedAt.IsZero() || !states["B"].LeaseUntil.IsZero() || states["OLD"].Months != 1 {
		t.Fatalf("throttle cached as missing or old data lost: %+v %v", states, err)
	}
	k.Source = historySource(func(ctx context.Context, ticker string) (marketdata.PriceHistory, error) {
		h := old
		h.Ticker = ticker
		h.Symbol = ticker
		h.FetchedAt = at
		return h, nil
	})
	rep, err = k.Refresh(ctx, []string{"OLD", "B", "C"}, []string{"OLD"}, 1, false)
	if err != nil || rep.Stored != 1 || rep.Left != 2 {
		t.Fatalf("resume: %+v %v", rep, err)
	}
}
