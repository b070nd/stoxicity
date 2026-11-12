package eventstudy

// synthetic fixture, not verified against live data

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// weekdayBars builds n bars on consecutive weekdays from start; f gives the
// open and close of bar i.
func weekdayBars(start string, n int, f func(i int) (open, closing float64)) []Bar {
	d, err := time.Parse(time.DateOnly, start)
	if err != nil {
		panic(err)
	}
	var bars []Bar
	for len(bars) < n {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			o, c := f(len(bars))
			bars = append(bars, Bar{Day: d.Format(time.DateOnly), Open: o, Close: c, High: math.Max(o, c) + 1, Low: math.Min(o, c) - 1, Volume: 1000})
		}
		d = d.AddDate(0, 0, 1)
	}
	return bars
}

func rising(i int) (float64, float64) { return 100 + float64(i), 101 + float64(i) }

func flat(int) (float64, float64) { return 100, 100 }

// newYork is a wall-clock time in New York, in winter and so UTC-5.
func newYork(t *testing.T, day, clock string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", day+" "+clock, loc)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func testSpec() Spec {
	s := DefaultSpec()
	s.ID, s.Types = "test", []string{"x"}
	s.Horizons = []int{1, 3}
	s.MinGroup, s.MinClusters, s.Resamples = 3, 2, 200
	return s
}

func runOne(t *testing.T, spec Spec, o Observation, bars []Bar) Outcome {
	t.Helper()
	rep, err := Run(spec, Manifest{}, []Observation{o}, MapPrices{"AAA": {Security: "AAA", Bars: bars}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(rep.Outcomes))
	}
	return rep.Outcomes[0]
}

func TestEntryHasNoLookAhead(t *testing.T) {
	bars := weekdayBars("2024-01-01", 40, rising)
	at := newYork(t, "2024-01-03", "10:00")
	for _, c := range []struct {
		name string
		rule Entry
		obs  Observation
		want string
	}{
		{"open is the next day", NextOpen, Observation{ID: "a", Security: "AAA", Type: "x", AvailableAt: at}, "2024-01-04"},
		{"close is the same day", NextClose, Observation{ID: "a", Security: "AAA", Type: "x", AvailableAt: at}, "2024-01-03"},
		{"known to the day, open", NextOpen, Observation{ID: "a", Security: "AAA", Type: "x", AvailableAt: at.UTC().Truncate(24 * time.Hour), DayPrecision: true}, "2024-01-04"},
		{"known to the day, close", NextClose, Observation{ID: "a", Security: "AAA", Type: "x", AvailableAt: at.UTC().Truncate(24 * time.Hour), DayPrecision: true}, "2024-01-04"},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := testSpec()
			spec.Entry = c.rule
			out := runOne(t, spec, c.obs, bars)
			if out.EntryDay != c.want {
				t.Errorf("entered on %s, want %s (%s)", out.EntryDay, c.want, out.Why)
			}
		})
	}
}

func TestOutcomeIgnoresBarsAfterExit(t *testing.T) {
	bars := weekdayBars("2024-01-01", 40, rising)
	o := Observation{ID: "a", Security: "AAA", Type: "x", AvailableAt: newYork(t, "2024-01-03", "10:00")}
	spec := testSpec()
	before := runOne(t, spec, o, bars)
	if before.State != Measured || len(before.Held) != 2 || before.Held[1].State != Measured {
		t.Fatalf("outcome not measured: %+v", before)
	}
	e, _ := indexOfDay(bars, before.EntryDay)
	changed := append([]Bar(nil), bars...)
	for i := e + 3; i < len(changed); i++ { // the 3-session exit is bar e+2
		changed[i].Open, changed[i].Close, changed[i].Low, changed[i].High = 500, 500, 499, 501
	}
	if after := runOne(t, spec, o, changed); !reflect.DeepEqual(before, after) {
		t.Errorf("bars after the exit changed the outcome:\n%+v\n%+v", before, after)
	}
}

func TestContextUsesOnlyBarsBeforeEntry(t *testing.T) {
	spec := ContextSpec{VolatilitySessions: 5, MomentumSessions: 5, TrendSessions: 5,
		VolatilityEdges: []float64{0.1, 0.5}, MomentumEdges: []float64{-0.1, 0.1}, SizeEdges: []float64{1e9, 1e10}}
	wobble := func(i int) (float64, float64) { c := 100 + float64(i%3); return c, c }
	bars := weekdayBars("2024-01-01", 30, wobble)
	market := weekdayBars("2024-01-01", 30, rising)
	const entry = 20
	base := contextAt(spec, bars, entry, market, entry, 5e9)
	if base.Volatility == Unknown || base.Momentum == Unknown || base.MarketTrend != "up" || base.Size != "mid" || base.MarketVolatility == Unknown {
		t.Fatalf("context not measured: %+v", base)
	}
	want := bars[entry-1].Close/bars[entry-1-5].Close - 1
	if base.MomentumValue == nil || math.Abs(*base.MomentumValue-want) > 1e-12 {
		t.Errorf("momentum = %v, want %v", base.MomentumValue, want)
	}

	changed := append([]Bar(nil), bars...)
	changedMarket := append([]Bar(nil), market...)
	for i := entry; i < len(changed); i++ {
		changed[i].Close, changed[i].Open = 1000, 1000
		changedMarket[i].Close, changedMarket[i].Open = 1, 1
	}
	if got := contextAt(spec, changed, entry, changedMarket, entry, 5e9); !reflect.DeepEqual(base, got) {
		t.Errorf("bars at or after entry changed the context:\n%+v\n%+v", base, got)
	}
	changed = append([]Bar(nil), bars...)
	changed[entry-1].Close = 1000
	if got := contextAt(spec, changed, entry, market, entry, 5e9); reflect.DeepEqual(base, got) {
		t.Error("the last bar before entry did not change the context")
	}

	short := contextAt(spec, bars, 3, market, -1, 0)
	if short.Volatility != Unknown || short.Momentum != Unknown || short.Size != Unknown || short.MarketTrend != Unknown || short.MarketVolatility != Unknown || short.VolatilityValue != nil {
		t.Errorf("short history should be unknown: %+v", short)
	}
	contextAt(spec, bars, 1000, market, 1000, 1) // must not panic
	contextAt(spec, bars, -4, market, -4, 1)
}

func TestCostsAndBenchmark(t *testing.T) {
	bars := weekdayBars("2024-01-01", 40, rising)
	market := weekdayBars("2024-01-01", 40, func(i int) (float64, float64) { return 200 + float64(i), 200 + float64(i) + 2 })
	spec := testSpec()
	spec.Benchmark = "MKT"
	spec.Costs = Costs{HalfSpreadBps: 5, SlippageBps: 5, FeeBps: 0}
	o := Observation{ID: "a", Security: "AAA", Type: "x", AvailableAt: newYork(t, "2024-01-03", "10:00")}
	rep, err := Run(spec, Manifest{}, []Observation{o}, MapPrices{"AAA": {Bars: bars}, "MKT": {Bars: market}})
	if err != nil {
		t.Fatal(err)
	}
	out := rep.Outcomes[0]
	h := out.Held[0] // one session: bar 3 open to bar 3 close
	side := 10 / 1e4
	if want := bars[3].Close*(1-side)/(bars[3].Open*(1+side)) - 1; math.Abs(h.Net-want) > 1e-12 {
		t.Errorf("net = %v, want %v", h.Net, want)
	}
	if h.Net >= h.Gross || math.Abs(h.Gross-(bars[3].Close/bars[3].Open-1)) > 1e-12 {
		t.Errorf("net %v should be below gross %v", h.Net, h.Gross)
	}
	if h.Market == nil || h.VsMarket == nil {
		t.Fatalf("no benchmark: %+v", h)
	}
	if want := market[3].Close/market[3].Open - 1; math.Abs(*h.Market-want) > 1e-12 {
		t.Errorf("market = %v, want %v", *h.Market, want)
	}
	if math.Abs(*h.VsMarket-(h.Net-*h.Market)) > 1e-12 {
		t.Errorf("vs market = %v, want %v", *h.VsMarket, h.Net-*h.Market)
	}
}

func TestHoldingPeriodStates(t *testing.T) {
	spec := testSpec()
	spec.Horizons = []int{1, 100}
	o := Observation{ID: "a", Security: "AAA", Type: "x", AvailableAt: newYork(t, "2024-01-03", "10:00")}
	out := runOne(t, spec, o, weekdayBars("2024-01-01", 40, rising))
	if out.Held[0].State != Measured || out.Held[1].State != Censored {
		t.Errorf("states = %s, %s; want measured, censored", out.Held[0].State, out.Held[1].State)
	}

	bars := weekdayBars("2024-01-01", 40, rising)
	missing := append(append([]Bar(nil), bars[:6]...), bars[11:]...) // a whole week gone after bar 5
	spec.Horizons = []int{1, 10}
	out = runOne(t, spec, o, missing)
	if out.Held[0].State != Measured || out.Held[1].State != Gap {
		t.Errorf("states = %s, %s; want measured, gap", out.Held[0].State, out.Held[1].State)
	}

	spec = testSpec()
	spec.Horizons = []int{1, 5}
	spec.Development = Window{From: "2024-01-01", To: "2024-01-31"}
	spec.Validation = Window{From: "2024-02-01", To: "2024-03-31"}
	o.AvailableAt = newYork(t, "2024-01-29", "10:00") // enters 2024-01-30
	out = runOne(t, spec, o, weekdayBars("2024-01-01", 60, rising))
	if out.Period != Development || out.Held[0].State != Measured || out.Held[1].State != Leaks {
		t.Errorf("period %s, states %s, %s; want development, measured, leaks", out.Period, out.Held[0].State, out.Held[1].State)
	}
}

// studyData builds events on bars 25 to 44 of a rising stock against a flat
// market, all in the validation window.
func studyData(t *testing.T) ([]Observation, Prices) {
	t.Helper()
	bars := weekdayBars("2024-01-01", 80, rising)
	var obs []Observation
	for i := 25; i < 45; i++ {
		obs = append(obs, Observation{ID: "e" + strconv.Itoa(i), Security: "AAA", Type: "x", AvailableAt: newYork(t, bars[i].Day, "10:00")})
	}
	return obs, MapPrices{"AAA": {Bars: bars}, "MKT": {Bars: weekdayBars("2024-01-01", 80, flat)}}
}

func studySpec() Spec {
	s := testSpec()
	s.Horizons = []int{1}
	s.Benchmark = "MKT"
	s.Development = Window{From: "2024-01-01", To: "2024-01-31"}
	s.Validation = Window{From: "2024-02-01", To: "2024-04-30"}
	return s
}

func TestReportIsRepeatableAndRejectsDuplicates(t *testing.T) {
	obs, prices := studyData(t)
	man := Manifest{PointInTimeUniverse: true, IncludesDelisted: true}
	a, err := Run(studySpec(), man, obs, prices)
	if err != nil {
		t.Fatal(err)
	}
	// the order of the observations must not matter either
	reversed := make([]Observation, len(obs))
	for i, o := range obs {
		reversed[len(obs)-1-i] = o
	}
	b, err := Run(studySpec(), man, reversed, prices)
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !bytes.Equal(ja, jb) {
		t.Error("the same inputs gave different reports")
	}
	if _, err := Run(studySpec(), man, append(obs, obs[0]), prices); err == nil {
		t.Error("a duplicate observation ID was accepted")
	}
}

func TestStatus(t *testing.T) {
	obs, prices := studyData(t)
	honest := Manifest{PointInTimeUniverse: true, IncludesDelisted: true}

	few, err := Run(studySpec(), honest, obs[:2], prices)
	if err != nil {
		t.Fatal(err)
	}
	if few.Status != Insufficient {
		t.Errorf("two events: status %s, want %s", few.Status, Insufficient)
	}

	fake := honest
	fake.Synthetic = true
	rep, err := Run(studySpec(), fake, obs, prices)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status == Evaluated {
		t.Error("a synthetic manifest was evaluated")
	}
	for _, g := range rep.Groups {
		if g.Status == Evaluated {
			t.Errorf("group %s/%s is evaluated on synthetic data", g.Dimension, g.Bucket)
		}
	}
	if rep.Status != Experimental {
		t.Errorf("status %s, want %s", rep.Status, Experimental)
	}
}

func TestSummarize(t *testing.T) {
	values := []float64{1, 2, 3, -1}
	st := summarize(values, []string{"a", "b", "c", "d"}, 5, 0.05, 200, 7)
	if st.N != 4 || st.Clusters != 4 || st.Mean != 1.25 || st.Median != 1.5 || st.WinRate != 0.75 || st.Worst != -1 || st.Best != 3 {
		t.Errorf("descriptives wrong: %+v", st)
	}
	if want := math.Sqrt(8.75 / 3); math.Abs(st.StdDev-want) > 1e-12 {
		t.Errorf("std dev = %v, want %v", st.StdDev, want)
	}
	if want := st.Mean / st.StdDev * math.Sqrt(252.0/5); math.Abs(st.RiskAdjusted-want) > 1e-12 {
		t.Errorf("risk adjusted = %v, want %v", st.RiskAdjusted, want)
	}
	if st.Lower > st.Mean || st.Upper < st.Mean || st.P < 0 || st.P > 1 {
		t.Errorf("interval or p wrong: %+v", st)
	}
	if again := summarize(values, []string{"a", "b", "c", "d"}, 5, 0.05, 200, 7); again != st {
		t.Error("the same seed gave a different summary")
	}

	one := summarize(values, []string{"a", "a", "a", "a"}, 5, 0.05, 200, 7)
	if one.Clusters != 1 || one.P != 1 || one.Lower != one.Mean || one.Upper != one.Mean {
		t.Errorf("one cluster should say nothing: %+v", one)
	}
	if empty := summarize(nil, nil, 5, 0.05, 200, 7); empty != (Stats{P: 1}) {
		t.Errorf("empty = %+v", empty)
	}

	up := summarize([]float64{1, 2, 3, 4, 5, 6}, []string{"a", "b", "c", "d", "e", "f"}, 1, 0.05, 500, 1)
	// 500 resamples cannot show a probability below 2/(500+1): the floor keeps a
	// result from passing any correction for the number of groups tested.
	if up.Lower <= 0 || up.P != 2.0/501 {
		t.Errorf("an all-positive sample should exclude zero, at the floor of its resampling: %+v", up)
	}
}

func TestHolm(t *testing.T) {
	got := holm([]float64{0.03, 0.04, 0.01, 0.005})
	want := []float64{0.06, 0.06, 0.03, 0.02}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-12 {
			t.Errorf("holm = %v, want %v", got, want)
			break
		}
	}
	if len(holm(nil)) != 0 {
		t.Error("empty in should be empty out")
	}
	if got := holm([]float64{0.9, 0.8}); got[0] != 1 || got[1] != 1 {
		t.Errorf("holm = %v, want capped at 1", got)
	}
}

// A rebuilt event keeps a group from "evaluated" unless it is attested: from
// an official filing whose acceptance time is on record.
func TestRebuiltEventsCountOnlyWhenAttested(t *testing.T) {
	spec := Spec{MinGroup: 2, MinClusters: 2, Alpha: 0.05}
	passing := Stats{N: 40, Clusters: 12, Lower: 0.01, Upper: 0.03, PAdjusted: 0.001}
	g := Group{Period: Validation, Net: passing, Attested: 40}
	if status, why := verdict(g, spec, nil, 1); status != Evaluated {
		t.Errorf("attested rebuilt events: %s (%s), want evaluated", status, why)
	}
	g.Reconstructed, g.Attested = 1, 39
	if status, _ := verdict(g, spec, nil, 1); status != Experimental {
		t.Errorf("one rebuilt event without an attested time: %s, want experimental", status)
	}
}
