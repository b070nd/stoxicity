package eventstudy

import (
	"fmt"
	"hash"
	"sort"
	"strconv"
	"time"
)

// The two ways a study that enters intraday states how a position was opened.
const (
	// EntryBasisIntraday says the position was opened at the open of an
	// intraday bar.
	EntryBasisIntraday = "intraday"
	// EntryBasisDailyOpen says it was opened at the open of the first session
	// after the information, as next_open does, because an intraday time cannot
	// be told: the information is known only to the day.
	EntryBasisDailyOpen = "daily_open"
)

// IntradayBar is one finished bar of one security inside a session. Start is
// the start of the bar; the bar covers the Interval of its series from there.
type IntradayBar struct {
	Start                  time.Time
	Open, High, Low, Close float64
	Volume                 int64
}

// IntradaySeries is the intraday bars of one security on one price basis (the
// caller has applied the splits and kept only bars inside sessions), oldest
// first, one bar per start, all of one length.
type IntradaySeries struct {
	Security string
	Interval time.Duration
	Bars     []IntradayBar
}

// Validate says why the series cannot be measured.
func (s IntradaySeries) Validate() error {
	if s.Interval <= 0 {
		return fmt.Errorf("eventstudy: %s: the intraday bars have no length", s.Security)
	}
	for i, b := range s.Bars {
		if b.Open <= 0 || b.Close <= 0 || b.Low <= 0 || b.High < b.Low {
			return fmt.Errorf("eventstudy: %s: the intraday bar of %s has a price that cannot be right", s.Security, b.Start.UTC().Format(time.RFC3339))
		}
		if i > 0 && !b.Start.After(s.Bars[i-1].Start) {
			return fmt.Errorf("eventstudy: %s: intraday bars are not in order at %s", s.Security, b.Start.UTC().Format(time.RFC3339))
		}
	}
	return nil
}

// IntradaySource supplies the intraday bars of a security; false when there are
// none.
type IntradaySource interface {
	IntradaySeries(security string) (IntradaySeries, bool)
}

// MapIntraday is IntradaySource over series held in memory, by security.
type MapIntraday map[string]IntradaySeries

// IntradaySeries implements IntradaySource.
func (m MapIntraday) IntradaySeries(security string) (IntradaySeries, bool) {
	s, ok := m[security]
	return s, ok
}

// PricesWithIntraday is Prices that also supplies intraday bars: what a study
// with the intraday entry needs.
type PricesWithIntraday struct {
	Prices
	IntradaySource
}

// pricesWithAll is Prices that supplies distributions and intraday bars.
type pricesWithAll struct {
	Prices
	DividendSource
	IntradaySource
}

// WithIntraday adds intraday bars to prices. Prices that already supply
// distributions keep supplying them.
func WithIntraday(prices Prices, bars IntradaySource) Prices {
	if d, ok := prices.(DividendSource); ok {
		return pricesWithAll{Prices: prices, DividendSource: d, IntradaySource: bars}
	}
	return PricesWithIntraday{Prices: prices, IntradaySource: bars}
}

// loadedIntraday is the intraday bars of one security as the run holds them:
// checked once, with the reason it cannot be used when it cannot.
type loadedIntraday struct {
	bars []IntradayBar
	why  string
}

func (r *run) loadIntraday(security string) *loadedIntraday {
	if l, ok := r.ibars[security]; ok {
		return l
	}
	l := &loadedIntraday{}
	s, ok := r.intraday.IntradaySeries(security)
	switch {
	case !ok || len(s.Bars) == 0:
		l.why = "no intraday bars for " + security
	case s.Interval != r.interval:
		l.why = fmt.Sprintf("the intraday bars of %s are %s long, the study needs %s", security, s.Interval, r.interval)
	default:
		if err := s.Validate(); err != nil {
			l.why = err.Error()
		} else {
			l.bars = s.Bars
		}
	}
	r.ibars[security] = l
	return l
}

// at is the first bar that starts at or after t and before the session's
// close; false when there is none.
func (l *loadedIntraday) at(t, closes time.Time) (IntradayBar, bool) {
	i := sort.Search(len(l.bars), func(i int) bool { return !l.bars[i].Start.Before(t) })
	if i == len(l.bars) || !l.bars[i].Start.Before(closes) {
		return IntradayBar{}, false
	}
	return l.bars[i], true
}

// intradayEntry is the bar a position is opened on, when the study enters
// intraday: the first bar that starts at or after the time the information is
// usable (its availability plus the delay) and inside the regular session of the
// entry day. Information usable before the session opens is entered at the
// session's first bar. The reason is not empty when there is no such bar; the
// event is then left out, never entered at a daily price instead. day is the
// entry day, the first session that closes after the usable time. When the entry
// day is one of the study's early closes no bar is picked, and the reason says
// so.
func (r *run) intradayEntry(o Observation, day string) (IntradayBar, string) {
	if r.early[day] {
		return IntradayBar{}, "the entry day " + day + " is an early close, which an intraday entry does not guess at"
	}
	l := r.loadIntraday(o.Security)
	if l.why != "" {
		return IntradayBar{}, l.why
	}
	opens, ok1 := r.ses.open(day)
	closes, ok2 := r.ses.close(day)
	if !ok1 || !ok2 {
		return IntradayBar{}, "the entry day " + day + " is not a day"
	}
	t := o.AvailableAt.Add(r.delay)
	if t.Before(opens) {
		t = opens
	}
	bar, found := l.at(t, closes)
	if !found {
		return IntradayBar{}, fmt.Sprintf("no intraday bar of %s starts between %s and the close of the session", o.Security, t.In(r.ses.loc).Format("2006-01-02 15:04"))
	}
	return bar, ""
}

// lowFrom is the lowest price the security's intraday bars reached from a bar
// that starts at or after from, to the close of the session on that day: the
// entry day's part of a position's drawdown. It is the entry price itself when
// no later bar went lower.
func (r *run) lowFrom(security string, entry IntradayBar, closes time.Time) float64 {
	low := entry.Open
	l := r.loadIntraday(security)
	i := sort.Search(len(l.bars), func(i int) bool { return !l.bars[i].Start.Before(entry.Start) })
	for ; i < len(l.bars) && l.bars[i].Start.Before(closes); i++ {
		if l.bars[i].Low < low {
			low = l.bars[i].Low
		}
	}
	return low
}

// intradayEntered is the bar a position was opened on and the close of that
// bar's session.
type intradayEntered struct {
	bar    IntradayBar
	closes time.Time
}

// benchmarkReturn is the return of the benchmark id (whose daily bars are given)
// over a holding period, without costs. For a position opened at an intraday bar
// the benchmark is entered at the same time, at the open of its own first
// intraday bar at or after the stock's entry bar, never at the daily open; it has
// no return when it lacks those bars. Otherwise it is entered by the study's
// daily rule. False when the benchmark lacks the days. A study that counts
// distributions counts the benchmark's own over the same days (see
// benchmarkCash).
func (r *run) benchmarkReturn(id string, bars []Bar, in *intradayEntered, entryDay, exitDay string) (float64, bool) {
	entry, exit, ok := r.benchmarkPrices(id, bars, in, entryDay, exitDay)
	if !ok {
		return 0, false
	}
	cash, ok := r.benchmarkCash(id, entryDay, exitDay)
	if !ok {
		return 0, false
	}
	return (exit+cash)/entry - 1, true
}

// benchmarkPrices is the price the benchmark is entered at and the close of the
// exit day; false when it lacks what the entry needs.
func (r *run) benchmarkPrices(id string, bars []Bar, in *intradayEntered, entryDay, exitDay string) (entry, exit float64, ok bool) {
	if in == nil {
		return benchmarkPrices(bars, entryDay, exitDay, r.spec.Entry)
	}
	if id == "" || len(bars) == 0 {
		return 0, 0, false
	}
	l := r.loadIntraday(id)
	if l.why != "" {
		return 0, 0, false
	}
	b, ok := l.at(in.bar.Start, in.closes)
	x, ok2 := indexOfDay(bars, exitDay)
	if !ok || !ok2 {
		return 0, 0, false
	}
	return b.Open, bars[x].Close, true
}

// startIntraday prepares the run to read intraday bars; it is a no-op for a
// study that does not enter intraday.
func (r *run) startIntraday(prices Prices) error {
	if r.spec.Entry != Intraday {
		return nil
	}
	src, ok := prices.(IntradaySource)
	if !ok || src == nil {
		return fmt.Errorf("eventstudy: study %s enters intraday and was given prices that supply no intraday bars", r.spec.ID)
	}
	r.intraday, r.ibars = src, map[string]*loadedIntraday{}
	r.early = map[string]bool{}
	for _, d := range r.spec.EarlyCloseDays {
		r.early[d] = true
	}
	r.interval = time.Duration(r.spec.IntradayInterval) * time.Second
	return nil
}

// writeIntraday adds the intraday bars the run read to the data hash.
func (r *run) writeIntraday(h hash.Hash) {
	for _, id := range sortedKeys(r.ibars) {
		l := r.ibars[id]
		fmt.Fprintf(h, "intraday:%s:%s:%d\n", id, l.why, r.spec.IntradayInterval)
		for _, b := range l.bars {
			h.Write([]byte(strconv.FormatInt(b.Start.UnixMilli(), 10) + ","))
			for _, v := range []float64{b.Open, b.High, b.Low, b.Close, float64(b.Volume)} {
				writeFloat(h, v)
			}
			h.Write([]byte{'\n'})
		}
	}
}
