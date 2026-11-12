package research

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/marketdata"
)

// PriceResult says what Prices could and could not put on one basis.
type PriceResult struct {
	// Series is how many tickers have a series.
	Series int
	// Unresolved gives, for each ticker left out, why.
	Unresolved map[string]string
	// First and Last are the earliest and latest day over all series; empty when there is none.
	First, Last string
}

// Prices reads the daily bars of the tickers and puts each on one price basis with the splits
// known at the time (marketdata.LoadSeries holds the rule): when every candle of a ticker is
// raw, all are put on the basis after the last known split. A ticker whose candles are of
// unknown basis and that has a split after its first bar cannot be put on one basis and is left
// out, with the reason.
func Prices(ctx context.Context, s Store, tickers []string, from, to string, known time.Time) (eventstudy.MapPrices, PriceResult, error) {
	res := PriceResult{Unresolved: map[string]string{}}
	loaded, unresolved, err := marketdata.LoadSeries(ctx, s, tickers, from, to, known)
	if err != nil {
		return nil, res, fmt.Errorf("research: %w", err)
	}
	res.Unresolved = unresolved
	out := eventstudy.MapPrices{}
	for t, m := range loaded {
		series := eventstudy.Series{Security: m.Ticker, Currency: m.Currency, Bars: make([]eventstudy.Bar, len(m.Bars))}
		for i, b := range m.Bars {
			series.Bars[i] = eventstudy.Bar{Day: b.Day, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume}
		}
		out[t] = series
		res.Series++
		first, last := series.Bars[0].Day, series.Bars[len(series.Bars)-1].Day
		if res.First == "" || first < res.First {
			res.First = first
		}
		if last > res.Last {
			res.Last = last
		}
	}
	return out, res, nil
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
