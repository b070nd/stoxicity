package research

// Every name, figure and price below is a synthetic fixture, not verified against live data.

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/store"
)

func cover(accession, filed, end string, value float64) fundamentals.Fact {
	f := fact(fundamentals.Shares, "shares", "", end, filed, value)
	f.Accession = accession
	return f
}

func TestSharesOutstanding(t *testing.T) {
	average := fact(fundamentals.AvgShares, "shares", "2024-01-01", "2024-12-31", "2025-02-01", 90)
	cases := []struct {
		name   string
		facts  []fundamentals.Fact
		before string
		want   float64
	}{
		{"latest cover filed by the day", []fundamentals.Fact{
			cover("a1", "2025-02-01", "2024-12-31", 100), cover("a2", "2025-05-01", "2025-03-31", 200)}, "2025-04-01", 100},
		{"a later filing is used once filed", []fundamentals.Fact{
			cover("a1", "2025-02-01", "2024-12-31", 100), cover("a2", "2025-05-01", "2025-03-31", 200)}, "2025-05-01", 200},
		{"a fact filed after the day is never used", []fundamentals.Fact{cover("a2", "2025-05-01", "2025-03-31", 200)}, "2025-04-30", 0},
		{"a fact with no filing day is not public", []fundamentals.Fact{cover("a1", "", "2024-12-31", 100)}, "2025-04-01", 0},
		{"the cover wins over the average", []fundamentals.Fact{cover("a1", "2025-02-01", "2024-12-31", 100), average}, "2025-04-01", 100},
		{"the average is the fallback when there is no cover count", []fundamentals.Fact{average}, "2025-04-01", 90},
		{"the average filed later is not used", []fundamentals.Fact{average}, "2025-01-31", 0},
		{"the shortest period of the newest filing", []fundamentals.Fact{average,
			fact(fundamentals.AvgShares, "shares", "2024-10-01", "2024-12-31", "2025-02-01", 95)}, "2025-04-01", 95},
		{"several classes on one cover are not guessed at", []fundamentals.Fact{
			cover("a1", "2025-02-01", "2024-12-31", 100), cover("a1", "2025-02-01", "2024-12-31", 40), average}, "2025-04-01", 0},
		{"a class count with a dimension is not used", []fundamentals.Fact{func() fundamentals.Fact {
			f := cover("a1", "2025-02-01", "2024-12-31", 100)
			f.Dimensions = "ClassA"
			return f
		}()}, "2025-04-01", 0},
		{"a count too old is not used", []fundamentals.Fact{cover("a1", "2023-02-01", "2022-12-31", 100)}, "2025-04-01", 0},
		{"another unit is not a count", []fundamentals.Fact{fact(fundamentals.Shares, "USD", "", "2024-12-31", "2025-02-01", 100)}, "2025-04-01", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := sharesOutstanding(c.facts, c.before)
			if ok != (c.want != 0) || got.count != c.want {
				t.Errorf("got %v, %v; want %v", got.count, ok, c.want)
			}
		})
	}
}

// sizeSeries is the series of one security with the bars given.
func sizeSeries(security, currency string, bars ...eventstudy.Bar) eventstudy.MapPrices {
	return eventstudy.MapPrices{security: eventstudy.Series{Security: security, Currency: currency, Bars: bars}}
}

func bar(day string, close float64) eventstudy.Bar {
	return eventstudy.Bar{Day: day, Open: close, High: close, Low: close, Close: close, Volume: 1000}
}

func sizedRecord(id, symbol, cik string, typ eventfact.Type, at time.Time, precision string, facts ...eventfact.Fact) eventfact.Record {
	return eventfact.Record{ID: id, Symbol: symbol, CIK: cik, Type: typ, Category: typ.Category(), AvailableAt: at, Precision: precision,
		Source: eventfact.SourceFiling, Basis: eventfact.Known, Facts: facts}
}

func TestSetSizesMarketCap(t *testing.T) {
	const cik = "0000000001"
	event := time.Date(2025, 3, 10, 14, 0, 0, 0, time.UTC)
	// The second count is filed on the event day: it is not known the day before and must not be used.
	shares := []fundamentals.Fact{cover("a1", "2025-02-01", "2024-12-31", 100), cover("a2", "2025-03-10", "2025-03-01", 7000)}
	normal := sizeSeries("AAA", "USD", bar("2025-03-06", 40), bar("2025-03-07", 50), bar("2025-03-10", 9999))
	cases := []struct {
		name      string
		cik       string
		precision string
		facts     []fundamentals.Fact
		prices    eventstudy.MapPrices
		splits    []marketdata.Split
		want      float64
	}{
		{"shares of the latest filing by the day before, close before the event day", cik, eventfact.PrecisionTime, shares, normal, nil, 5000},
		{"a day-precision event reads no price of its day", cik, eventfact.PrecisionDay, shares, normal, nil, 5000},
		{"no issuer number", "", eventfact.PrecisionTime, shares, normal, nil, 0},
		{"no shares", cik, eventfact.PrecisionTime, nil, normal, nil, 0},
		{"no series", cik, eventfact.PrecisionTime, shares, eventstudy.MapPrices{}, nil, 0},
		{"no close before the day", cik, eventfact.PrecisionTime, shares, sizeSeries("AAA", "USD", bar("2025-03-10", 9999)), nil, 0},
		{"the last close is too old", cik, eventfact.PrecisionTime, shares, sizeSeries("AAA", "USD", bar("2025-02-20", 50)), nil, 0},
		{"a split after the count and before the prices: the series is on the later basis", cik, eventfact.PrecisionTime, shares,
			sizeSeries("AAA", "USD", bar("2025-03-07", 25)), []marketdata.Split{{Ticker: "AAA", Day: "2025-03-05", From: 1, To: 2}}, 5000},
		{"a split after the event is in the basis of the series too", cik, eventfact.PrecisionTime, shares,
			sizeSeries("AAA", "USD", bar("2025-03-07", 25)), []marketdata.Split{{Ticker: "AAA", Day: "2025-03-20", From: 1, To: 2}}, 5000},
		{"a split before the count is in the count already", cik, eventfact.PrecisionTime, shares,
			normal, []marketdata.Split{{Ticker: "AAA", Day: "2024-06-01", From: 1, To: 2}}, 5000},
		{"a split of another ticker", cik, eventfact.PrecisionTime, shares,
			normal, []marketdata.Split{{Ticker: "BBB", Day: "2025-03-05", From: 1, To: 2}}, 5000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := sizedRecord("ef_1", "AAA", c.cik, eventfact.InsiderBuy, event, c.precision)
			obs := Observations([]eventfact.Record{rec}, nil)
			SetSizes(obs, []eventfact.Record{rec}, map[string][]fundamentals.Fact{cik: c.facts}, c.prices, c.splits)
			if math.Abs(obs[0].MarketCap-c.want) > 1e-6 {
				t.Errorf("market cap = %v, want %v", obs[0].MarketCap, c.want)
			}
		})
	}
}

func TestSetSizesRatios(t *testing.T) {
	const cik = "0000000001"
	event := time.Date(2025, 3, 10, 14, 0, 0, 0, time.UTC)
	facts := []fundamentals.Fact{
		cover("a1", "2025-02-01", "2024-12-31", 100),
		fact(fundamentals.Revenue, "USD", "2024-01-01", "2024-12-31", "2025-02-01", 2000),
		fact(fundamentals.Revenue, "USD", "2024-10-01", "2024-12-31", "2025-02-01", 600),
		fact(fundamentals.Revenue, "USD", "2025-01-01", "2025-12-31", "2026-02-01", 9999),
	}
	prices := sizeSeries("AAA", "USD", bar("2025-03-07", 50))
	usd := func(name string, v float64) eventfact.Fact { return eventfact.Fact{Name: name, Value: v, Unit: "USD"} }
	cases := []struct {
		name   string
		typ    eventfact.Type
		facts  []eventfact.Fact
		prices eventstudy.MapPrices
		split  []marketdata.Split
		want   map[string]float64
	}{
		{"buyback against the capitalisation and the shares", eventfact.BuybackAnnounced,
			[]eventfact.Fact{usd("amount", 500), {Name: "shares", Value: 10, Unit: "shares"}}, prices, nil,
			map[string]float64{"pct_of_market_cap": 0.1, "pct_of_shares_outstanding": 0.1, "amount": 500, "shares": 10}},
		{"the shares of a buyback are against the count after the splits to the day", eventfact.BuybackAnnounced,
			[]eventfact.Fact{{Name: "shares", Value: 10, Unit: "shares"}}, prices, []marketdata.Split{{Ticker: "AAA", Day: "2025-03-05", From: 1, To: 2}},
			map[string]float64{"pct_of_shares_outstanding": 0.05, "shares": 10}},
		{"buyback in a currency other than the series", eventfact.BuybackAnnounced,
			[]eventfact.Fact{{Name: "amount", Value: 500, Unit: "EUR"}}, prices, nil, map[string]float64{"amount": 500}},
		{"buyback without a series has no capitalisation ratio", eventfact.BuybackAnnounced,
			[]eventfact.Fact{usd("amount", 500), {Name: "shares", Value: 10, Unit: "shares"}}, eventstudy.MapPrices{}, nil,
			map[string]float64{"pct_of_shares_outstanding": 0.1, "amount": 500, "shares": 10}},
		{"contract against the capitalisation and the revenue of the last filed year", eventfact.ContractAward,
			[]eventfact.Fact{usd("value", 1000), usd("annual_value", 200)}, prices, nil,
			map[string]float64{"pct_of_market_cap": 0.2, "pct_of_revenue": 0.1, "value": 1000, "annual_value": 200}},
		{"a total value alone says nothing of a year", eventfact.ContractAward,
			[]eventfact.Fact{usd("value", 1000)}, prices, nil, map[string]float64{"pct_of_market_cap": 0.2, "value": 1000}},
		{"another type gets no ratio", eventfact.InsiderBuy, []eventfact.Fact{usd("value", 1000)}, prices, nil, map[string]float64{"value": 1000}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := sizedRecord("ef_1", "AAA", cik, c.typ, event, eventfact.PrecisionTime, c.facts...)
			obs := Observations([]eventfact.Record{rec}, nil)
			SetSizes(obs, []eventfact.Record{rec}, map[string][]fundamentals.Fact{cik: facts}, c.prices, c.split)
			if len(obs[0].Numbers) != len(c.want) {
				t.Errorf("numbers = %v, want %v", obs[0].Numbers, c.want)
			}
			for name, want := range c.want {
				if got, ok := obs[0].Numbers[name]; !ok || math.Abs(got-want) > 1e-9 {
					t.Errorf("%s = %v, %v; want %v", name, got, ok, want)
				}
			}
		})
	}
}

// studyFake serves the stored events a study reads.
type studyFake struct {
	*fake
	records []eventfact.Record
}

func (f studyFake) EventFacts(context.Context, store.EventFactQuery) ([]eventfact.Record, error) {
	return f.records, nil
}

func weekdayCandles(ticker string, from, to time.Time, price float64) []marketdata.Candle {
	var out []marketdata.Candle
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		out = append(out, replayCandle(ticker, d.Format(time.DateOnly), price, true, "USD"))
	}
	return out
}

func TestStudyKnowsTheSizeOfACompany(t *testing.T) {
	from, to := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2025, 5, 30, 0, 0, 0, 0, time.UTC)
	event := time.Date(2025, 3, 10, 14, 0, 0, 0, time.UTC)
	rec := sizedRecord("ef_1", "AAA", "0000000001", eventfact.InsiderBuy, event, eventfact.PrecisionTime)
	spec := eventstudy.DefaultSpec()
	spec.ID, spec.Types, spec.Benchmark = "size", []string{string(eventfact.InsiderBuy)}, "SPY"
	size := func(facts []fundamentals.Fact) string {
		f := studyFake{fake: &fake{
			candles: map[string][]marketdata.Candle{"AAA": weekdayCandles("AAA", from, to, 20), "SPY": weekdayCandles("SPY", from, to, 400)},
			facts:   facts,
		}, records: []eventfact.Record{rec}}
		report, _, err := Study(context.Background(), f, StudyInput{Spec: spec})
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Outcomes) != 1 {
			t.Fatalf("outcomes = %+v", report.Outcomes)
		}
		return report.Outcomes[0].Context.Size
	}
	// 1 billion shares at 20 is 20 billion, above the upper edge of 10 billion.
	if got := size([]fundamentals.Fact{cover("a1", "2025-02-01", "2024-12-31", 1e9)}); got != "high" {
		t.Errorf("size with shares = %q", got)
	}
	if got := size(nil); got != eventstudy.Unknown {
		t.Errorf("size without shares = %q", got)
	}
}
