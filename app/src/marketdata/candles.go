package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// Candle is one trading day of one stock: where it opened, how high and low it
// went, where it closed, and how much changed hands.
type Candle struct {
	Ticker string
	// Day is the trading day, YYYY-MM-DD, in the exchange's own calendar.
	Day                    string
	Open, High, Low, Close float64
	Volume                 int64
	// Raw says the prices are as traded that day, not adjusted for any split.
	// Candles made before they were kept raw are adjusted by the source for the
	// splits it knew when they were fetched, which is not recorded.
	Raw bool
}

// Valid reports whether the candle is one a chart can draw: positive prices
// with the low under the high and the open and close between them.
func (c Candle) Valid() bool {
	return c.Ticker != "" && c.Day != "" && c.Low > 0 && c.High >= c.Low &&
		c.Open >= c.Low && c.Open <= c.High && c.Close >= c.Low && c.Close <= c.High
}

// Change is the move from the previous close, as a fraction (0.021 is +2.1%).
func Change(prev, close float64) float64 {
	if prev <= 0 {
		return 0
	}
	return close/prev - 1
}

// MaxMove is the ratio of two closes a day apart beyond which they are not
// taken for the same series.
const MaxMove = 5.0

// DayChange is the move from the close before to this close, as a fraction;
// false when there is no close before, or when the move is one a single day
// does not make (below a fifth or above five times): that is two different
// price series mixed, a split not applied or a ticker reused, and showing it
// would put noise at the top of a sort by change.
func DayChange(prev, close float64) (float64, bool) {
	if prev <= 0 || close <= 0 {
		return 0, false
	}
	r := close / prev
	if r < 1/MaxMove || r > MaxMove {
		return 0, false
	}
	return r - 1, true
}

// A Source is where candles come from. It is one of the two kinds below,
// whichever the source can do cheaply: a source of one stock at a time, or of a
// whole market day at a time. Only sources with a documented API and a key of
// your own belong here: free sites that guard their downloads against programs
// (Stooq now asks the browser to solve a puzzle) are not asked.
type Source interface{ Name() string }

// PerTicker gives the history of one stock.
type PerTicker interface {
	Source
	// History returns the daily candles of ticker from since on, oldest first;
	// none (and no error) when the source knows nothing of the stock.
	History(ctx context.Context, ticker string, since time.Time) ([]Candle, error)
}

// PerDay gives every stock of one market day.
type PerDay interface {
	Source
	// Day returns the candles of every stock that traded on day; none (and no
	// error) on a day the market was closed.
	Day(ctx context.Context, day time.Time) ([]Candle, error)
}

// ---- Polygon ----

// PolygonURL is Polygon's grouped daily bars: every US stock for one day in one
// request, which is how a whole market is kept up to date within the free
// plan's five requests a minute. It needs an API key.
const PolygonURL = "https://api.polygon.io/v2/aggs/grouped/locale/us/market/stocks/"

// Polygon is the source that needs a key.
type Polygon struct {
	Client httpx.Client
	Key    string
	// URL replaces PolygonURL (it ends with a slash); tests point it at a server.
	URL string
	// SplitsURL replaces PolygonSplitsURL.
	SplitsURL string
}

// Name implements Source.
func (Polygon) Name() string { return "polygon" }

// Day implements PerDay.
func (p Polygon) Day(ctx context.Context, day time.Time) ([]Candle, error) {
	if p.Key == "" {
		return nil, errors.New("marketdata: Polygon needs an API key (STOXICITY_POLYGON_KEY)")
	}
	base := p.URL
	if base == "" {
		base = PolygonURL
	}
	resp, err := p.Client.Do(ctx, httpx.Request{URL: base + day.Format(time.DateOnly) + "?" + url.Values{"adjusted": {"false"}, "apiKey": {p.Key}}.Encode()})
	if err != nil {
		return nil, err
	}
	// A result holds both "T" (the ticker) and "t" (a time in milliseconds), and
	// encoding/json matches names without regard to case, so the keys are read
	// by their exact spelling.
	var ans struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(resp.Body, &ans); err != nil {
		return nil, fmt.Errorf("marketdata: read Polygon's answer for %s: %w", day.Format(time.DateOnly), err)
	}
	out := make([]Candle, 0, len(ans.Results))
	for _, r := range ans.Results {
		var ticker string
		var o, h, l, c, v float64
		for key, dst := range map[string]any{"T": &ticker, "o": &o, "h": &h, "l": &l, "c": &c, "v": &v} {
			if raw, ok := r[key]; ok {
				if err := json.Unmarshal(raw, dst); err != nil {
					return nil, fmt.Errorf("marketdata: read Polygon's answer for %s: field %q: %w", day.Format(time.DateOnly), key, err)
				}
			}
		}
		candle := Candle{Ticker: ticker, Day: day.Format(time.DateOnly), Open: o, High: h, Low: l, Close: c, Volume: int64(v), Raw: true}
		if candle.Valid() {
			out = append(out, candle)
		}
	}
	return out, nil
}

// SameTicker reports whether two spellings are one ticker: BRK.B, BRK-B and
// brk-b are.
func SameTicker(a, b string) bool { return NormTicker(a) == NormTicker(b) }

// NormTicker is the spelling tickers are compared by.
func NormTicker(t string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(t), ".", "-"))
}

// MaxMissingWeekdays is how many weekdays may lie between two closes that are
// still one day's move: none on an ordinary day or over a weekend, one when the
// exchange was closed for a holiday. More means sessions are missing from the
// store, and the move between the two closes is that of several days.
const MaxMissingWeekdays = 1

// weekdaysBetween counts the Monday-to-Friday days strictly between a and b.
func weekdaysBetween(a, b time.Time) int {
	n := 0
	for d := a.AddDate(0, 0, 1); d.Before(b); d = d.AddDate(0, 0, 1) {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n
}

// MissingWeekdays counts the Monday-to-Friday days strictly between two days
// (YYYY-MM-DD), false when either is not a day. It is how many sessions a series
// ending at the earlier day has missed by the later one, holidays included.
func MissingWeekdays(from, to string) (int, bool) {
	a, errA := time.Parse(time.DateOnly, from)
	b, errB := time.Parse(time.DateOnly, to)
	if errA != nil || errB != nil {
		return 0, false
	}
	return weekdaysBetween(a, b), true
}

// splitRatios are the closing-price ratios a stock split makes: a 2-for-1
// halves the price, a 1-for-10 reverse split multiplies it by ten.
var splitRatios = func() []float64 {
	out := []float64{1.5, 1 / 1.5}
	for k := 2; k <= 20; k++ {
		out = append(out, float64(k), 1/float64(k))
	}
	return out
}()

// LooksLikeSplit says whether the ratio of two closes is, within 1.5%, one a
// stock split makes. The candles are stored as the source adjusted them on the
// day they were fetched; a split that took effect since then leaves the older
// rows on the old basis, so the ratio across it is the split ratio and not a
// move. A real fall of exactly half in a day looks the same, which is rare
// enough that no change is shown rather than a false one.
func LooksLikeSplit(ratio float64) bool {
	for _, s := range splitRatios {
		if math.Abs(ratio/s-1) < 0.015 {
			return true
		}
	}
	return false
}

// SessionChange is DayChange for two stored candles: it also refuses a move
// between closes with more than MaxMissingWeekdays weekdays between them
// (sessions are missing, so it is not the day's move) and one that looks like a stock split (see
// LooksLikeSplit). prevDay and day are YYYY-MM-DD.
func SessionChange(prevDay, day string, prev, close float64) (float64, bool) {
	p, err1 := time.Parse(time.DateOnly, prevDay)
	d, err2 := time.Parse(time.DateOnly, day)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	if !d.After(p) || weekdaysBetween(p, d) > MaxMissingWeekdays {
		return 0, false
	}
	ch, ok := DayChange(prev, close)
	if !ok || LooksLikeSplit(close/prev) {
		return 0, false
	}
	return ch, true
}
