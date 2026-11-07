package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/universe"
)

// Quote is the newest daily candle of a stock and the close before it, what
// the stock lists show of its price. Prev is 0 when only one day is known.
type Quote struct {
	Day                    string // YYYY-MM-DD
	Open, High, Low, Close float64
	Prev                   float64
}

// Change is the move of the close from the day before, as a fraction (0.012 is
// +1.2%); false when there is no day before.
func (q Quote) Change() (float64, bool) {
	if q.Prev <= 0 {
		return 0, false
	}
	return q.Close/q.Prev - 1, true
}

// staleAfter is how old the newest candle may be before the list draws it
// dim: a long weekend with a holiday is four days.
const staleAfter = 5 * 24 * time.Hour

// stale reports whether the candle is too old to be today's picture.
func (q Quote) stale(now time.Time) bool {
	day, err := time.Parse(time.DateOnly, q.Day)
	return err != nil || now.Sub(day) > staleAfter
}

// candleWidth is the cells of the day candle in a table.
const candleWidth = 8

// candleBar draws the day as a horizontal candle w cells wide: the whole bar is
// the day's range from low to high, the thin line is the wick, the thick part
// the body from open to close, so a body at the right end is a day that closed
// near its high and a body filling the bar a day that ran from low to high.
// The style is green for a day that closed above its open, red below, dim
// for a day that closed where it opened or whose candle is old.
func candleBar(q Quote, w int, now time.Time) (string, Style) {
	if q.High <= 0 || q.Low <= 0 || q.High < q.Low || w < 3 {
		return "", Plain
	}
	at := func(v float64) int {
		if q.High == q.Low {
			return w / 2
		}
		return min(max(int(math.Round((v-q.Low)/(q.High-q.Low)*float64(w-1))), 0), w-1)
	}
	o, c := at(q.Open), at(q.Close)
	lo, hi := min(o, c), max(o, c)
	var b strings.Builder
	for i := 0; i < w; i++ {
		switch {
		case i < lo || i > hi:
			b.WriteString("─")
		case lo == hi:
			b.WriteString("┿") // a doji: the open and the close share a cell
		default:
			b.WriteString("█")
		}
	}
	style := Dim
	switch {
	case q.stale(now):
	case q.Close > q.Open:
		style = Good
	case q.Close < q.Open:
		style = Bad
	}
	return b.String(), style
}

// priceText writes a price in the width of the CLOSE column: cents under
// 10,000, whole units above.
func priceText(v float64) string {
	if v >= 10000 {
		return thousands(int(math.Round(v)))
	}
	return fmt.Sprintf("%.2f", v)
}

// changeText writes the move as a signed percentage and gives the style it is
// drawn in.
func changeText(q Quote, now time.Time) (string, Style) {
	ch, ok := q.Change()
	if !ok {
		return "", Plain
	}
	style := Dim
	switch {
	case q.stale(now):
	case ch > 0.00005:
		style = Good
	case ch < -0.00005:
		style = Bad
	}
	return fmt.Sprintf("%+.2f%%", ch*100), style
}

// typeStyle is how a type of security is coloured: ordinary stock plain, the
// kinds that behave differently from it each in a colour of their own.
func typeStyle(t string) Style {
	switch t {
	case universe.TypeETF:
		return Info
	case universe.TypePreferred:
		return Warn
	case universe.TypeWarrant, universe.TypeRight, universe.TypeUnit:
		return Accent
	case universe.TypeADR:
		return Blue
	case universe.TypeTest:
		return Dim
	}
	return Plain
}

// domainStyle gives each SIC division a colour of its own, so a screen of the
// table can be read by colour alone.
func domainStyle(domain string) Style {
	for i, d := range universe.Domains() {
		if d == domain {
			return Hue1 + Style(i)
		}
	}
	return Plain
}

// exchangeStyle gives the listing venues colours of their own.
func exchangeStyle(name string) Style {
	switch strings.ToLower(name) {
	case "nasdaq":
		return Blue
	case "nyse":
		return Accent
	case "otc":
		return Warn
	case "cboe":
		return Info
	case "txse":
		return Good
	}
	return Plain
}

// newsStyle warms with the number of articles: none plain, a few yellow, many
// bold yellow.
func newsStyle(n int) Style {
	switch {
	case n >= 5:
		return Hot
	case n > 0:
		return Warn
	}
	return Plain
}

// toneStyle is green for positive news, red for negative, dim for neutral.
func toneStyle(t float64) Style {
	switch {
	case t >= 0.05:
		return Good
	case t <= -0.05:
		return Bad
	}
	return Dim
}
