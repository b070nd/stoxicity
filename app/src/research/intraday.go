package research

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/store"
)

// IntradayWindowDays is how many calendar days after the day (UTC) an event
// became available its entry day is looked for: far enough for a weekend and a
// holiday after it. The entry day is never before that day, because a session
// that has closed is not entered.
const IntradayWindowDays = 4

// intradayDays are the weekdays on which an event available at the time may be
// entered: its day and the IntradayWindowDays after it, less the weekend.
func intradayDays(at time.Time) []string {
	y, m, d := at.UTC().Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	var out []string
	for i := 0; i <= IntradayWindowDays; i++ {
		if t := day.AddDate(0, 0, i); t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			out = append(out, t.Format(time.DateOnly))
		}
	}
	return out
}

// IntradayNeeds says which intraday bars a study of the event types needs: by
// ticker, the days on which its events, available between from and to (zero:
// open), may be entered. An event known only to the day is entered at the daily
// open, not intraday, and needs none. The days are in order and without repeats.
func IntradayNeeds(ctx context.Context, s Store, types []string, from, to time.Time) (map[string][]string, error) {
	q := store.EventFactQuery{From: from, To: to}
	for _, t := range types {
		q.Types = append(q.Types, eventfact.Type(t))
	}
	records, err := s.EventFacts(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("research: events: %w", err)
	}
	seen := map[string]map[string]bool{}
	for _, r := range records {
		if r.Precision == eventfact.PrecisionDay || r.Symbol == "" {
			continue
		}
		if seen[r.Symbol] == nil {
			seen[r.Symbol] = map[string]bool{}
		}
		for _, d := range intradayDays(r.AvailableAt) {
			seen[r.Symbol][d] = true
		}
	}
	out := make(map[string][]string, len(seen))
	for t, days := range seen {
		for d := range days {
			out[t] = append(out[t], d)
		}
		sort.Strings(out[t])
	}
	return out, nil
}

// withIntraday adds to the prices the intraday bars an intraday entry reads: those of the
// securities of the events that are not known only to the day, of the benchmark and of the
// sector benchmarks, from the day before the first event to the end of the entry window of the
// last, as the database held them at asOf (zero: as they stand). It is called only for a study
// that enters intraday.
//
// The bars are put on the price basis of the daily series, with the splits and the rawness of the
// daily candles read over dailyFrom to dailyTo, the days Prices was asked for
// (marketdata.PutIntradayOnBasis holds the rule). A ticker whose bars cannot be put on that basis
// is left without them, and unresolved gives, under "TICKER (intraday)", why; the study then
// leaves its events out as having no intraday bar, and never enters them at a daily price.
func withIntraday(ctx context.Context, s Store, prices eventstudy.Prices, obs []eventstudy.Observation, spec eventstudy.Spec,
	dailyFrom, dailyTo string, asOf time.Time, unresolved map[string]string) (eventstudy.Prices, error) {
	src, ok := s.(marketdata.IntradayLoadSource)
	if !ok {
		return nil, fmt.Errorf("research: the store does not hold intraday bars")
	}
	var tickers []string
	var first, last time.Time
	for _, o := range obs {
		if o.DayPrecision {
			continue
		}
		tickers = append(tickers, o.Security)
		if first.IsZero() || o.AvailableAt.Before(first) {
			first = o.AvailableAt
		}
		if o.AvailableAt.After(last) {
			last = o.AvailableAt
		}
	}
	out := eventstudy.MapIntraday{}
	if len(tickers) == 0 {
		return eventstudy.WithIntraday(prices, out), nil
	}
	tickers = append(tickers, spec.Benchmark)
	for _, id := range spec.SectorBenchmarks {
		tickers = append(tickers, id)
	}
	interval := time.Duration(spec.IntradayInterval) * time.Second
	y, m, d := first.UTC().Date()
	from := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	y, m, d = last.UTC().Date()
	to := time.Date(y, m, d+IntradayWindowDays+1, 0, 0, 0, 0, time.UTC).Add(-time.Millisecond)
	loaded, why, err := marketdata.LoadIntraday(ctx, src, marketdata.IntradayQuery{
		Tickers: uniqueSorted(tickers), Interval: interval, From: from, To: to, DailyFrom: dailyFrom, DailyTo: dailyTo, Known: asOf})
	if err != nil {
		return nil, fmt.Errorf("research: %w", err)
	}
	for t, reason := range why {
		unresolved[t+" (intraday)"] = reason
	}
	for t, bars := range loaded {
		series := eventstudy.IntradaySeries{Security: t, Interval: interval, Bars: make([]eventstudy.IntradayBar, len(bars))}
		for i, b := range bars {
			series.Bars[i] = eventstudy.IntradayBar{Start: b.Start, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume}
		}
		out[t] = series
	}
	return eventstudy.WithIntraday(prices, out), nil
}
