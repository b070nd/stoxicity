package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

type historyClient func(context.Context, httpx.Request) (*httpx.Response, error)

func (f historyClient) Do(ctx context.Context, r httpx.Request) (*httpx.Response, error) {
	return f(ctx, r)
}

func TestYahooHistoryUsesPublicMonthlyPriceClosesAndExchangeCalendar(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	zone, _ := time.LoadLocation("Asia/Tokyo")
	stamps := []int64{time.Date(2025, 12, 1, 0, 0, 0, 0, zone).Unix(), time.Date(2026, 1, 1, 0, 0, 0, 0, zone).Unix(), time.Date(2026, 2, 1, 0, 0, 0, 0, zone).Unix(), time.Date(2026, 2, 27, 15, 0, 0, 0, zone).Unix()}
	body, _ := json.Marshal(map[string]any{"chart": map[string]any{"result": []any{map[string]any{
		"meta":      map[string]any{"symbol": "TEST", "currency": "JPY", "exchangeTimezoneName": "Asia/Tokyo", "dataGranularity": "1mo", "regularMarketTime": stamps[3]},
		"timestamp": stamps, "indicators": map[string]any{"quote": []any{map[string]any{"close": []any{100, nil, 110, 111}}}, "adjclose": []any{map[string]any{"adjclose": []any{999, 999, 999, 999}}}},
	}}}})
	client := historyClient(func(ctx context.Context, r httpx.Request) (*httpx.Response, error) {
		u, err := url.Parse(r.URL)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if u.Host != "query1.finance.yahoo.com" || q.Get("interval") != "1mo" || q.Get("includeAdjustedClose") != "false" || q.Get("apikey") != "" || q.Get("period1") != "1451606400" {
			t.Fatalf("wrong public history request: %s", r.URL)
		}
		return &httpx.Response{Status: 200, Body: body}, nil
	})
	h, err := (YahooHistory{Client: client, Now: func() time.Time { return now }}).History(context.Background(), "TEST")
	if err != nil {
		t.Fatal(err)
	}
	if h.Currency != "JPY" || h.LatestDay != "2026-02-27" || !h.FetchedAt.Equal(now) || len(h.Months) != 2 || h.Months[0].Month != "2025-12" || h.Months[0].Close != 100 || h.Months[1].Month != "2026-02" || h.Months[1].Close != 111 {
		t.Fatalf("bad monthly history: %+v", h)
	}
}

func TestYahooHistoryRejectsWrongIdentityAndMalformedSeries(t *testing.T) {
	for _, body := range []string{
		`{"chart":{"result":[]}}`,
		`{"chart":{"result":[{"meta":{"symbol":"OTHER","currency":"USD","dataGranularity":"1mo","regularMarketTime":100,"exchangeTimezoneName":"UTC"},"timestamp":[100],"indicators":{"quote":[{"close":[1]}]}}]}}`,
		`{"chart":{"result":[{"meta":{"symbol":"TEST","currency":"USD","dataGranularity":"1mo","regularMarketTime":100,"exchangeTimezoneName":"UTC"},"timestamp":[100,200],"indicators":{"quote":[{"close":[1]}]}}]}}`,
		`{"chart":{"result":[{"meta":{"symbol":"TEST","currency":"USD","dataGranularity":"1mo","regularMarketTime":100,"exchangeTimezoneName":"UTC"},"timestamp":[100],"indicators":{"quote":[{"close":[-1]}]}}]}}`,
	} {
		client := historyClient(func(context.Context, httpx.Request) (*httpx.Response, error) {
			return &httpx.Response{Status: 200, Body: []byte(body)}, nil
		})
		if _, err := (YahooHistory{Client: client}).History(context.Background(), "TEST"); err == nil {
			t.Errorf("accepted invalid history: %s", body)
		}
	}
	client := historyClient(func(context.Context, httpx.Request) (*httpx.Response, error) {
		return &httpx.Response{Status: 200, Body: []byte(`{"chart":{"result":null,"error":{"code":"Not Found"}}}`)}, nil
	})
	if _, err := (YahooHistory{Client: client}).History(context.Background(), "TEST"); !errors.Is(err, ErrNoHistory) {
		t.Fatalf("missing listing not distinguished: %v", err)
	}
}

func TestAdjustedHistoryCalendarDoesNotDoubleAdjustOrHideRealLargeReturns(t *testing.T) {
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	h := PriceHistory{Ticker: "TEST", Currency: "USD", LatestDay: "2026-11-06", Months: []HistoryMonth{{"2016-12", 50}, {"2017-12", 100}, {"2025-11", 100}, {"2025-12", 50}, {"2026-01", 100}, {"2026-11", 110}}}
	c := BuildHistoryCalendar(at, h)
	if !c.Months[0].HasChange || c.Months[0].Change != -.5 || !c.Months[1].HasChange || c.Months[1].Change != 1 || !c.Years[0].HasChange || c.Years[0].Change != 1 {
		t.Fatalf("normalized price changes hidden or double adjusted: %+v", c)
	}
	if c.Months[11].Day != "2026-11-06" || c.Months[2].Day != "" {
		t.Fatal("as-of date or missing month invented")
	}
	_ = BuildHistoryCalendar(at, PriceHistory{}) // empty history must stay readable
}
