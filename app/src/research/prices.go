package research

import (
	"context"
	"fmt"
	"math"
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
// known at the time: when every candle of a ticker is raw, all are put on the basis of the
// newest. A ticker whose candles are of unknown basis and that has a split after its first bar
// cannot be put on one basis and is left out, with the reason.
func Prices(ctx context.Context, s Store, tickers []string, from, to string, known time.Time) (eventstudy.MapPrices, PriceResult, error) {
	res := PriceResult{Unresolved: map[string]string{}}
	out := eventstudy.MapPrices{}
	wanted := uniqueSorted(tickers)
	if len(wanted) == 0 {
		return out, res, nil
	}
	candles, err := s.CandleSeries(ctx, wanted, from, to, known)
	if err != nil {
		return nil, res, fmt.Errorf("research: candles: %w", err)
	}
	var splits []marketdata.Split
	if known.IsZero() {
		splits, err = s.Splits(ctx)
	} else {
		splits, err = s.SplitsAsOf(ctx, known)
	}
	if err != nil {
		return nil, res, fmt.Errorf("research: splits: %w", err)
	}
	byTicker := map[string][]marketdata.Split{}
	for _, sp := range splits {
		k := marketdata.NormTicker(sp.Ticker)
		byTicker[k] = append(byTicker[k], sp)
	}
	for _, t := range wanted {
		series, why := oneBasis(t, candles[t], byTicker[marketdata.NormTicker(t)])
		if why != "" {
			res.Unresolved[t] = why
			continue
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

// oneBasis makes the series of one ticker; why is not empty when it cannot.
func oneBasis(ticker string, candles []marketdata.Candle, splits []marketdata.Split) (eventstudy.Series, string) {
	cs := append([]marketdata.Candle(nil), candles...)
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Day < cs[j].Day })
	if len(cs) == 0 {
		return eventstudy.Series{}, "no bars"
	}
	sort.SliceStable(splits, func(i, j int) bool { return splits[i].Day < splits[j].Day })
	raw := true
	for _, c := range cs {
		raw = raw && c.Raw
	}
	if !raw {
		for _, sp := range splits {
			if sp.Day > cs[0].Day && sp.Ratio() != 1 {
				return eventstudy.Series{}, "candles of unknown basis and a split on " + sp.Day
			}
		}
	}
	series := eventstudy.Series{Security: ticker}
	for _, c := range cs {
		if c.Open <= 0 || c.High <= 0 || c.Low <= 0 || c.Close <= 0 || c.High < c.Low {
			continue
		}
		if series.Currency == "" {
			series.Currency = c.Currency
		}
		factor := 1.0
		if raw {
			for _, sp := range splits {
				if sp.Day > c.Day {
					factor *= sp.Ratio()
				}
			}
		}
		b := eventstudy.Bar{Day: c.Day, Open: c.Open / factor, High: c.High / factor, Low: c.Low / factor, Close: c.Close / factor,
			Volume: int64(math.Round(float64(c.Volume) * factor))}
		if n := len(series.Bars); n > 0 && series.Bars[n-1].Day == b.Day {
			series.Bars[n-1] = b
			continue
		}
		series.Bars = append(series.Bars, b)
	}
	if len(series.Bars) == 0 {
		return eventstudy.Series{}, "no bars"
	}
	return series, ""
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
