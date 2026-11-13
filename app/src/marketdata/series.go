package marketdata

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// Bar is one day of a series: the prices and the volume on the basis of the series.
type Bar struct {
	Day                    string // YYYY-MM-DD, the exchange's calendar
	Open, High, Low, Close float64
	Volume                 int64
}

// Series is the daily bars of one ticker on one price basis, oldest first, one
// bar per day. LoadSeries follows one ticker only: a renamed security or a
// changed ticker is not followed there, because it reads nothing that says
// which tickers are one security. LoadSeriesWith does follow verified renames.
type Series struct {
	Ticker string
	// Currency is the first currency a kept bar states; empty when none does.
	// Bars are not compared or converted by currency.
	Currency string
	Bars     []Bar
	// Chain are the symbols whose candles make the bars, oldest first, when
	// LoadSeriesWith joined a verified rename or relisting; nil otherwise.
	Chain []Link
	// EndedOn is the last day of a security that was delisted or merged away,
	// when verified spans say so; the series legitimately stops there. Empty
	// otherwise, and always empty from LoadSeries.
	EndedOn string
}

// The reasons a ticker's candles cannot be put on one basis. The second is
// followed by the day of the split.
const (
	// NoBars says there is no candle with usable prices.
	NoBars = "no bars"
	// UnknownBasisAndSplit says a candle's basis is not known and a split took
	// effect after the first candle, so the candles may be on different bases.
	UnknownBasisAndSplit = "candles of unknown basis and a split on "
)

// PutOnOneBasis makes the series of one ticker from its candles and the splits
// known to the caller; the reason is not empty when it cannot, and then the
// series is empty. It reads its arguments and changes neither.
//
//   - Only the splits of the ticker count, and when through is not empty only
//     those that took effect by that day. The basis is that of a share after the
//     last of them: with an empty through, after every split given, also one
//     later than the newest candle.
//   - When every candle is raw, each bar is divided by the ratios of the splits
//     that took effect after its day and its volume is multiplied by them. A
//     split with an invalid ratio changes nothing.
//   - When any candle is of unknown basis the candles are kept as they are,
//     unless a split with a ratio other than one took effect after the first
//     candle: then they cannot be put on one basis.
//   - A candle with a price that is not positive, or a high under its low, is
//     dropped. Of two candles of one day the later in the input stays.
func PutOnOneBasis(ticker string, candles []Candle, splits []Split, through string) (Series, string) {
	cs := append([]Candle(nil), candles...)
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Day < cs[j].Day })
	if len(cs) == 0 {
		return Series{}, NoBars
	}
	mine := splitsBetween(splits, "", through, ticker)
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].Day < mine[j].Day })
	raw := true
	for _, c := range cs {
		raw = raw && c.Raw
	}
	if !raw {
		for _, sp := range mine {
			if sp.Day > cs[0].Day && sp.Ratio() != 1 {
				return Series{}, UnknownBasisAndSplit + sp.Day
			}
		}
	}
	series := Series{Ticker: ticker}
	for _, c := range cs {
		if c.Open <= 0 || c.High <= 0 || c.Low <= 0 || c.Close <= 0 || c.High < c.Low {
			continue
		}
		if series.Currency == "" {
			series.Currency = c.Currency
		}
		factor := 1.0
		if raw {
			factor = splitFactor(mine, c.Day)
		}
		b := Bar{Day: c.Day, Open: c.Open / factor, High: c.High / factor, Low: c.Low / factor, Close: c.Close / factor,
			Volume: int64(math.Round(float64(c.Volume) * factor))}
		if n := len(series.Bars); n > 0 && series.Bars[n-1].Day == b.Day {
			series.Bars[n-1] = b
			continue
		}
		series.Bars = append(series.Bars, b)
	}
	if len(series.Bars) == 0 {
		return Series{}, NoBars
	}
	return series, ""
}

// splitsBetween returns, in the order given, the splits of any of the tickers
// that took effect after the day and, when through is not empty, by through.
func splitsBetween(splits []Split, after, through string, tickers ...string) []Split {
	var out []Split
	for _, s := range splits {
		if s.Day <= after || (through != "" && s.Day > through) {
			continue
		}
		for _, t := range tickers {
			if SameTicker(s.Ticker, t) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// splitFactor is how many shares one share of the day became by the splits that
// took effect after it: the product of their ratios, in the order given.
func splitFactor(splits []Split, after string) float64 {
	factor := 1.0
	for _, s := range splits {
		if s.Day > after {
			factor *= s.Ratio()
		}
	}
	return factor
}

// SeriesSource is what LoadSeries reads: the stored candles and splits.
type SeriesSource interface {
	// CandleSeries returns the daily candles of the tickers between two days
	// (inclusive), by ticker; when known is not zero, as held at that time.
	CandleSeries(ctx context.Context, tickers []string, from, to string, known time.Time) (map[string][]Candle, error)
	// Splits returns every stored split.
	Splits(ctx context.Context) ([]Split, error)
	// SplitsAsOf returns the splits stored by the time.
	SplitsAsOf(ctx context.Context, known time.Time) ([]Split, error)
}

// LoadSeries reads the daily candles of the tickers between two days and puts
// each ticker on one price basis with the splits known at the time: every
// stored split when known is zero, otherwise the candles and the splits as
// held at known. It returns the series by ticker and, for each ticker that has
// none, the reason. A ticker is asked for once; an empty one is ignored, and
// with no ticker nothing is read. It does not follow a renamed ticker: only
// the candles stored under the ticker asked for are read (LoadSeriesWith
// follows verified renames).
func LoadSeries(ctx context.Context, src SeriesSource, tickers []string, from, to string, known time.Time) (map[string]Series, map[string]string, error) {
	series, unresolved := map[string]Series{}, map[string]string{}
	seen := map[string]bool{}
	var wanted []string
	for _, t := range tickers {
		if t != "" && !seen[t] {
			seen[t] = true
			wanted = append(wanted, t)
		}
	}
	if len(wanted) == 0 {
		return series, unresolved, nil
	}
	sort.Strings(wanted)
	candles, err := src.CandleSeries(ctx, wanted, from, to, known)
	if err != nil {
		return nil, nil, fmt.Errorf("marketdata: candles: %w", err)
	}
	var splits []Split
	if known.IsZero() {
		splits, err = src.Splits(ctx)
	} else {
		splits, err = src.SplitsAsOf(ctx, known)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("marketdata: splits: %w", err)
	}
	for _, t := range wanted {
		s, why := PutOnOneBasis(t, candles[t], splits, "")
		if why != "" {
			unresolved[t] = why
			continue
		}
		series[t] = s
	}
	return series, unresolved, nil
}

// LoadSeriesWith is LoadSeries that follows renames. A ticker is read as the
// security that held it on the last day of the range in which a verified span
// names it (see anchorDay). When verified spans join that security's symbols
// (see Chain), the candles of every symbol are read over the days it was the
// security's, joined oldest first and put on one price basis with the splits of
// those symbols, so a split recorded under an old symbol restates the earlier
// part and one under the new symbol restates everything before it. A split
// counts only when its day lies in the span of a symbol that matches its
// ticker. The series is keyed and named by the ticker asked for.
//
// Spans are read as held at known, like the candles. A ticker no verified span
// names is read exactly as LoadSeries reads it, as is every ticker when ids is
// nil. A series that ends by a delisting or a merger says so in EndedOn.
func LoadSeriesWith(ctx context.Context, src SeriesSource, ids IdentitySource, tickers []string, from, to string, known time.Time) (map[string]Series, map[string]string, error) {
	if ids == nil {
		return LoadSeries(ctx, src, tickers, from, to, known)
	}
	series, unresolved := map[string]Series{}, map[string]string{}
	seen := map[string]bool{}
	var wanted []string
	for _, t := range tickers {
		if t != "" && !seen[t] {
			seen[t] = true
			wanted = append(wanted, t)
		}
	}
	if len(wanted) == 0 {
		return series, unresolved, nil
	}
	sort.Strings(wanted)
	spans, err := ids.SymbolHistory(ctx, known)
	if err != nil {
		return nil, nil, fmt.Errorf("marketdata: identity: %w", err)
	}
	lineage := map[string]Lineage{}
	asked := map[string]bool{}
	var symbols []string
	ask := func(sym string) {
		if !asked[sym] {
			asked[sym] = true
			symbols = append(symbols, sym)
		}
	}
	for _, t := range wanted {
		if day, ok := anchorDay(spans, t, from, to); ok {
			lineage[t] = Chain(spans, t, day)
		}
		ask(t)
		for _, l := range lineage[t].Links() {
			ask(l.Symbol)
		}
	}
	candles, err := src.CandleSeries(ctx, symbols, from, to, known)
	if err != nil {
		return nil, nil, fmt.Errorf("marketdata: candles: %w", err)
	}
	var splits []Split
	if known.IsZero() {
		splits, err = src.Splits(ctx)
	} else {
		splits, err = src.SplitsAsOf(ctx, known)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("marketdata: splits: %w", err)
	}
	for _, t := range wanted {
		l := lineage[t]
		cs, mine := candles[t], splits
		if len(l.Spans) > 1 {
			cs, mine = nil, nil
			for _, link := range l.Links() {
				inLink := func(day string) bool { return day >= link.From && (link.To == "" || day <= link.To) }
				for _, k := range matchingKeys(candles, link.Symbol) {
					for _, c := range candles[k] {
						if inLink(c.Day) {
							cs = append(cs, c)
						}
					}
				}
				for _, sp := range splits {
					if SameTicker(sp.Ticker, link.Symbol) && inLink(sp.Day) {
						sp.Ticker = t
						mine = append(mine, sp)
					}
				}
			}
		}
		s, why := PutOnOneBasis(t, cs, mine, "")
		if why != "" {
			unresolved[t] = why
			continue
		}
		if len(l.Spans) > 1 {
			s.Chain = l.Links()
		}
		s.EndedOn = l.EndedOn
		series[t] = s
	}
	return series, unresolved, nil
}

// matchingKeys returns the keys of the candle map that are the symbol, as
// SameTicker compares, in sorted order.
func matchingKeys(candles map[string][]Candle, symbol string) []string {
	var keys []string
	for k := range candles {
		if SameTicker(k, symbol) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
