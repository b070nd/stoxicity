package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	resp, err := p.Client.Do(ctx, httpx.Request{URL: base + day.Format(time.DateOnly) + "?" + url.Values{"adjusted": {"true"}, "apiKey": {p.Key}}.Encode()})
	if err != nil {
		return nil, err
	}
	var ans struct {
		Results []struct {
			T string  `json:"T"`
			O float64 `json:"o"`
			H float64 `json:"h"`
			L float64 `json:"l"`
			C float64 `json:"c"`
			V float64 `json:"v"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp.Body, &ans); err != nil {
		return nil, fmt.Errorf("marketdata: read Polygon's answer for %s: %w", day.Format(time.DateOnly), err)
	}
	out := make([]Candle, 0, len(ans.Results))
	for _, r := range ans.Results {
		c := Candle{Ticker: r.T, Day: day.Format(time.DateOnly), Open: r.O, High: r.H, Low: r.L, Close: r.C, Volume: int64(r.V)}
		if c.Valid() {
			out = append(out, c)
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
