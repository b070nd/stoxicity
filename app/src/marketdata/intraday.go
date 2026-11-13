package marketdata

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// IntradayZone is the exchange calendar intraday bars are put on a day by: the
// bars come from US exchanges, whose sessions are kept in New York time.
const IntradayZone = "America/New_York"

// IntradayBar is one bar of one stock inside a trading day: where it opened,
// how high and low it went, where it closed and how much changed hands, over
// the Interval that starts at Start.
type IntradayBar struct {
	Ticker string
	// Start is the start of the bar, in UTC.
	Start time.Time
	// Interval is the length of the bar.
	Interval               time.Duration
	Open, High, Low, Close float64
	Volume                 int64
	// Raw says the prices are as traded that day, not adjusted for any split.
	Raw bool
	// Currency is the currency the source states for the prices; empty when it
	// does not say. Nothing fills it in by assumption.
	Currency string
	// Source names where the bar came from.
	Source string
	// FetchedAt is when this system stored the bar; zero when it was stored
	// before that was recorded.
	FetchedAt time.Time
}

// Valid reports whether the bar is one a study can use: a ticker, a start, a
// positive length, positive prices with the low under the high and the open and
// close between them, and a volume that is not negative.
func (b IntradayBar) Valid() bool {
	return b.Ticker != "" && !b.Start.IsZero() && b.Interval > 0 && b.Volume >= 0 &&
		b.Low > 0 && b.High >= b.Low && b.Open >= b.Low && b.Open <= b.High && b.Close >= b.Low && b.Close <= b.High
}

// FirstBarAtOrAfter returns the index of the first bar that starts at or after
// t, in bars sorted by start, oldest first; false when every bar starts before
// t. It reads the bars and changes nothing.
func FirstBarAtOrAfter(bars []IntradayBar, t time.Time) (int, bool) {
	i := sort.Search(len(bars), func(i int) bool { return !bars[i].Start.Before(t) })
	return i, i < len(bars)
}

// PutIntradayOnBasis puts the bars of one ticker on the price basis of the
// ticker's daily series, with the same rules PutOnOneBasis follows (the splits
// are those of the ticker, a bar's day is its day in the exchange's calendar).
// dailyRaw says that every daily candle of the series is raw; the reason is not
// empty when the bars cannot be put on that basis, and then the result is empty.
//
//   - When the daily candles are raw and so are the bars, each bar is divided by
//     the ratios of the splits that took effect after its day and its volume is
//     multiplied by them: the basis is that of a share after every split given.
//   - Otherwise the bars are kept as they are, unless a split with a ratio other
//     than one took effect after the first bar's day: then they cannot be put on
//     one basis, as in PutOnOneBasis.
//   - A bar that is not valid is dropped. Of two bars of one start the later in
//     the input stays.
func PutIntradayOnBasis(ticker string, bars []IntradayBar, splits []Split, dailyRaw bool) ([]IntradayBar, string) {
	loc, err := time.LoadLocation(IntradayZone)
	if err != nil {
		return nil, fmt.Sprintf("the exchange zone %s is not known", IntradayZone)
	}
	bs := append([]IntradayBar(nil), bars...)
	sort.SliceStable(bs, func(i, j int) bool { return bs[i].Start.Before(bs[j].Start) })
	mine := splitsBetween(splits, "", "", ticker)
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].Day < mine[j].Day })
	raw := dailyRaw
	for _, b := range bs {
		raw = raw && b.Raw
	}
	if !raw && len(bs) > 0 {
		first := bs[0].Start.In(loc).Format(time.DateOnly)
		for _, sp := range mine {
			if sp.Day > first && sp.Ratio() != 1 {
				return nil, UnknownBasisAndSplit + sp.Day
			}
		}
	}
	var out []IntradayBar
	for _, b := range bs {
		if !b.Valid() {
			continue
		}
		if raw {
			f := splitFactor(mine, b.Start.In(loc).Format(time.DateOnly))
			b.Open, b.High, b.Low, b.Close = b.Open/f, b.High/f, b.Low/f, b.Close/f
			b.Volume = int64(math.Round(float64(b.Volume) * f))
		}
		if n := len(out); n > 0 && out[n-1].Start.Equal(b.Start) {
			out[n-1] = b
			continue
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, NoBars
	}
	return out, ""
}

// IntradayQuery says which intraday bars LoadIntraday reads.
type IntradayQuery struct {
	Tickers  []string
	Interval time.Duration
	// From and To bound the bars read, by start, both inclusive.
	From, To time.Time
	// DailyFrom and DailyTo are the days (YYYY-MM-DD) the daily series was read
	// for: whether its candles are raw, which sets the basis the bars are put on,
	// is read over those days.
	DailyFrom, DailyTo string
	// Known, when not zero, reads the bars, candles and splits as held then.
	Known time.Time
}

// IntradayLoadSource is what LoadIntraday reads: the stored daily candles and
// splits, and the stored intraday bars.
type IntradayLoadSource interface {
	SeriesSource
	// IntradayBars returns the stored bars of one ticker and bar length that
	// start between the two times (UTC milliseconds, inclusive), oldest first;
	// when known is not zero, as held at that time.
	IntradayBars(ctx context.Context, ticker string, interval time.Duration, fromMs, toMs int64, known time.Time) ([]IntradayBar, error)
}

// LoadIntraday reads the intraday bars of the tickers and puts each ticker on
// the price basis of its daily series (PutIntradayOnBasis). It returns the bars
// by ticker, oldest first, and for each ticker whose bars cannot be put on that
// basis, the reason. A ticker with no stored bars is in neither. A ticker is
// asked for once; an empty one is ignored, and with no ticker nothing is read.
func LoadIntraday(ctx context.Context, src IntradayLoadSource, q IntradayQuery) (map[string][]IntradayBar, map[string]string, error) {
	out, unresolved := map[string][]IntradayBar{}, map[string]string{}
	seen := map[string]bool{}
	var wanted []string
	for _, t := range q.Tickers {
		if t != "" && !seen[t] {
			seen[t] = true
			wanted = append(wanted, t)
		}
	}
	if len(wanted) == 0 {
		return out, unresolved, nil
	}
	sort.Strings(wanted)
	candles, err := src.CandleSeries(ctx, wanted, q.DailyFrom, q.DailyTo, q.Known)
	if err != nil {
		return nil, nil, fmt.Errorf("marketdata: candles: %w", err)
	}
	var splits []Split
	if q.Known.IsZero() {
		splits, err = src.Splits(ctx)
	} else {
		splits, err = src.SplitsAsOf(ctx, q.Known)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("marketdata: splits: %w", err)
	}
	for _, t := range wanted {
		bars, err := src.IntradayBars(ctx, t, q.Interval, q.From.UnixMilli(), q.To.UnixMilli(), q.Known)
		if err != nil {
			return nil, nil, fmt.Errorf("marketdata: intraday bars of %s: %w", t, err)
		}
		if len(bars) == 0 {
			continue
		}
		raw := len(candles[t]) > 0
		for _, c := range candles[t] {
			raw = raw && c.Raw
		}
		put, why := PutIntradayOnBasis(t, bars, splits, raw)
		if why != "" {
			unresolved[t] = why
			continue
		}
		out[t] = put
	}
	return out, unresolved, nil
}
