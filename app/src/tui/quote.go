package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// Quote is the newest daily candle of a stock and the close before it, what
// the stock lists show of its price. Prev is 0 when only one day is known;
// PrevDay is the day of that earlier close.
type Quote struct {
	Day                    string // YYYY-MM-DD
	Open, High, Low, Close float64
	Prev                   float64
	PrevDay                string // YYYY-MM-DD
	// Move is the move reconciled with the stock splits (marketdata.Compare);
	// the zero value says none was made, and Change then judges by the two
	// closes alone.
	Move marketdata.Move
}

// Unresolved says the move from the day before may be a corporate action that
// is not explained yet: shown as such, never as a price move and never hidden.
func (q Quote) Unresolved() bool { return q.Move.State == marketdata.MoveUnresolved }

// Change is the move of the close from the day before, as a fraction (0.012 is
// +1.2%); false when there is none or it is not a day's move: the earlier close
// is more than a long weekend before, or the ratio is a stock split's or a
// mixed series' (see marketdata.SessionChange).
func (q Quote) Change() (float64, bool) {
	if q.Move.State != "" {
		return q.Move.Change, q.Move.Shown()
	}
	return marketdata.SessionChange(q.PrevDay, q.Day, q.Prev, q.Close)
}

// stale reports whether the candle is too old to be today's picture: more than
// one weekday (a holiday) has passed since its day without a candle. A candle of
// the previous Friday is today's picture on a Monday, but not on a Wednesday.
func (q Quote) stale(now time.Time) bool {
	missed, ok := marketdata.MissingWeekdays(q.Day, now.Format(time.DateOnly))
	return !ok || missed > marketdata.MaxMissingWeekdays
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
	if q.Unresolved() {
		return "split?", Warn
	}
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
	case universe.TypeWarrant, universe.TypeRight, universe.TypeUnit, universe.TypeNote:
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

// moneyText writes an amount of money in the width of a table column: 1.23T,
// 45.6B, 789M, 12.3M, 450K.
func moneyText(v float64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	for _, u := range []struct {
		at   float64
		name string
	}{{1e12, "T"}, {1e9, "B"}, {1e6, "M"}, {1e3, "K"}} {
		if v >= u.at {
			x := v / u.at
			switch {
			case x >= 100:
				return fmt.Sprintf("%s%.0f%s", sign, x, u.name)
			case x >= 10:
				return fmt.Sprintf("%s%.1f%s", sign, x, u.name)
			}
			return fmt.Sprintf("%s%.2f%s", sign, x, u.name)
		}
	}
	return fmt.Sprintf("%s%.0f", sign, v)
}

// percentText writes a fraction as a percentage: 0.123 is 12.3%.
func percentText(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }

// signedPercent is percentText with a sign.
func signedPercent(v float64) string { return fmt.Sprintf("%+.1f%%", v*100) }

// gainStyle is green for a positive figure, red for a negative one.
func gainStyle(v float64) Style {
	switch {
	case v > 0.00005:
		return Good
	case v < -0.00005:
		return Bad
	}
	return Dim
}
