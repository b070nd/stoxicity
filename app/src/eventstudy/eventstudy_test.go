package eventstudy

// synthetic fixture, not verified against live data

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
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

// A study that does not turn on the two extra dimensions keeps its
// specification hash, its report bit for bit and its breakdown. The two digests
// were taken from the code before the dimensions existed.
func TestStudyWithoutExtraDimensionsIsUnchanged(t *testing.T) {
	const (
		specHash   = "f4a29dc957f9879ddb351807d3c396d653d6f2dba90fed66830cba0189ae8d26"
		reportHash = "abed7899c26fbafed5b051b30a2173b6ab248f35f72e278b9c50ed71621d255c"
	)
	obs, prices := studyData(t)
	rep, err := Run(studySpec(), Manifest{PointInTimeUniverse: true, IncludesDelisted: true}, obs, prices)
	if err != nil {
		t.Fatal(err)
	}
	if rep.SpecHash != specHash {
		t.Errorf("spec hash = %s, want %s", rep.SpecHash, specHash)
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != reportHash {
		t.Error("the report no longer encodes as it did before liquidity and relative momentum")
	}
	for _, word := range []string{"liquidity", "relative"} {
		if strings.Contains(string(b), word) {
			t.Errorf("the report mentions %q though the study does not measure it", word)
		}
	}
}

func dimensionsOf(rep Report) []string {
	seen := map[string]bool{}
	for _, g := range rep.Groups {
		if g.Dimension != "all" {
			seen[g.Dimension] = true
		}
	}
	return sortedKeys(seen)
}

func TestBreakdownListsExtraDimensionsOnlyWhenEnabled(t *testing.T) {
	obs, prices := studyData(t)
	base := Dimensions()
	sort.Strings(base)
	both := append(append([]string(nil), base...), "liquidity", "relative_momentum")
	sort.Strings(both)
	liquidityOnly := append(append([]string(nil), base...), "liquidity")
	sort.Strings(liquidityOnly)

	for _, c := range []struct {
		name string
		set  func(*ContextSpec)
		want []string
	}{
		{"neither", func(*ContextSpec) {}, base},
		{"liquidity", func(c *ContextSpec) { c.LiquiditySessions, c.LiquidityEdges = 5, []float64{5e4, 2e5} }, liquidityOnly},
		{"both", func(c *ContextSpec) {
			c.LiquiditySessions, c.LiquidityEdges, c.RelativeEdges = 5, []float64{5e4, 2e5}, []float64{-0.02, 0.02}
		}, both},
	} {
		spec := studySpec()
		spec.Context.MomentumSessions = 5
		c.set(&spec.Context)
		rep, err := Run(spec, Manifest{}, obs, prices)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := dimensionsOf(rep); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: breakdown by %v, want %v", c.name, got, c.want)
		}
		if got := spec.Context.Dimensions(); len(got) != len(c.want) {
			t.Errorf("%s: ContextSpec.Dimensions = %v", c.name, got)
		}
	}

	spec := studySpec()
	spec.Context.MomentumSessions = 5
	spec.Context.LiquiditySessions, spec.Context.LiquidityEdges, spec.Context.RelativeEdges = 5, []float64{5e4, 2e5}, []float64{-0.02, 0.02}
	rep, err := Run(spec, Manifest{}, obs, prices)
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := rep.Find("x", 1, Validation, "liquidity", "mid"); !ok || g.Net.N == 0 {
		t.Errorf("no liquidity group: %+v", g)
	}
	if g, ok := rep.Find("x", 1, Validation, "relative_momentum", "high"); !ok || g.Net.N == 0 {
		t.Errorf("no relative momentum group: %+v", g)
	}
	if rep.SpecHash == studySpec().Hash() {
		t.Error("turning the dimensions on did not change the specification hash")
	}
}

// volumeBars are weekday bars closing at 100 with the given volume on each.
func volumeBars(n int, volume func(i int) int64) []Bar {
	bars := weekdayBars("2024-01-01", n, flat)
	for i := range bars {
		bars[i].Volume = volume(i)
	}
	return bars
}

func TestLiquidityBuckets(t *testing.T) {
	spec := ContextSpec{LiquiditySessions: 5, LiquidityEdges: []float64{1e5, 1e6}}
	const entry = 20
	for _, c := range []struct {
		name   string
		volume int64 // a close of 100 makes the mean traded value 100 times this
		want   string
	}{
		{"below the first edge", 999, "low"},
		{"on the first edge", 1000, "mid"},
		{"below the second edge", 9999, "mid"},
		{"on the second edge", 10000, "high"},
	} {
		bars := volumeBars(30, func(i int) int64 {
			if i >= entry-5 && i < entry {
				return c.volume
			}
			return 1 // older bars are outside the five sessions
		})
		got := contextAt(spec, bars, entry, nil, -1, 0)
		if got.Liquidity != c.want || got.LiquidityValue == nil || *got.LiquidityValue != 100*float64(c.volume) {
			t.Errorf("%s: %s %v, want %s", c.name, got.Liquidity, got.LiquidityValue, c.want)
		}
		if got.RelativeMomentum != "" || got.RelativeMomentumValue != nil {
			t.Errorf("%s: relative momentum measured though it is off: %+v", c.name, got)
		}
	}

	// the mean, not the last session, is what is bucketed
	bars := volumeBars(30, func(i int) int64 {
		if i == entry-1 {
			return 25000
		}
		return 0
	})
	if got := contextAt(spec, bars, entry, nil, -1, 0); got.Liquidity != "mid" {
		t.Errorf("one busy session of five: %s, want mid", got.Liquidity)
	}
}

func TestLiquidityIsUnknownWhenTheDataDoesNotReach(t *testing.T) {
	spec := ContextSpec{LiquiditySessions: 5, LiquidityEdges: []float64{1e5, 1e6}}
	busy := func(int) int64 { return 5000 }
	negative := volumeBars(30, busy)
	negative[18].Volume = -1
	for _, c := range []struct {
		name  string
		bars  []Bar
		entry int
	}{
		{"fewer sessions than asked for", volumeBars(30, busy), 4},
		{"no entry session", volumeBars(30, busy), -3},
		{"no volume at all", volumeBars(30, func(int) int64 { return 0 }), 20},
		{"a volume that cannot be right", negative, 20},
	} {
		got := contextAt(spec, c.bars, c.entry, nil, -1, 0)
		if got.Liquidity != Unknown || got.LiquidityValue != nil {
			t.Errorf("%s: %s %v, want unknown", c.name, got.Liquidity, got.LiquidityValue)
		}
	}
	if got := contextAt(ContextSpec{}, volumeBars(30, busy), 20, nil, -1, 0); got.Liquidity != "" {
		t.Errorf("liquidity off: %q, want empty", got.Liquidity)
	}

	on, off := studySpec(), studySpec()
	on.Context.LiquiditySessions, on.Context.LiquidityEdges = 5, []float64{1e5, 1e6}
	o := Observation{ID: "a", Security: "ZZZ", Type: "x", AvailableAt: newYork(t, "2024-01-03", "10:00")}
	if got, err := ContextFor(on, o, MapPrices{}); err != nil || got.Liquidity != Unknown {
		t.Errorf("no series, liquidity on: %q, %v; want unknown", got.Liquidity, err)
	}
	if got, err := ContextFor(off, o, MapPrices{}); err != nil || got.Liquidity != "" {
		t.Errorf("no series, liquidity off: %q, %v; want empty", got.Liquidity, err)
	}
}

// momentumBars close at from five sessions before the last bar before entry and
// at to on it.
func momentumBars(entry int, from, to float64) []Bar {
	return weekdayBars("2024-01-01", 30, func(i int) (float64, float64) {
		c := from
		if i >= entry-1 {
			c = to
		}
		return c, c
	})
}

func TestRelativeMomentumBuckets(t *testing.T) {
	spec := ContextSpec{MomentumSessions: 5, MomentumEdges: []float64{-0.1, 0.1}, RelativeEdges: []float64{-0.05, 0.05}}
	const entry = 20
	for _, c := range []struct {
		name          string
		stock, market float64 // the last close before entry, from 100 five sessions earlier
		want          string
		value         float64
	}{
		{"ahead of the market", 110, 102, "high", 0.08},
		{"level with the market", 100, 102, "mid", -0.02},
		{"behind the market", 90, 100, "low", -0.1},
		{"up in a rising market is not ahead", 106, 106, "mid", 0},
	} {
		got := contextAt(spec, momentumBars(entry, 100, c.stock), entry, momentumBars(entry, 100, c.market), entry, 0)
		if got.RelativeMomentum != c.want || got.RelativeMomentumValue == nil || math.Abs(*got.RelativeMomentumValue-c.value) > 1e-12 {
			t.Errorf("%s: %s %v, want %s %v", c.name, got.RelativeMomentum, got.RelativeMomentumValue, c.want, c.value)
		}
	}

	stock, market := momentumBars(entry, 100, 110), momentumBars(entry, 100, 100)
	for _, c := range []struct {
		name        string
		market      []Bar
		marketEntry int
		spec        ContextSpec
		want        string
	}{
		{"no benchmark bar for the entry day", market, -1, spec, Unknown},
		{"a benchmark too short for the lookback", market, 3, spec, Unknown},
		{"not asked for", market, entry, ContextSpec{MomentumSessions: 5, MomentumEdges: spec.MomentumEdges}, ""},
	} {
		if got := contextAt(c.spec, stock, entry, c.market, c.marketEntry, 0); got.RelativeMomentum != c.want || got.RelativeMomentumValue != nil {
			t.Errorf("%s: %q %v, want %q", c.name, got.RelativeMomentum, got.RelativeMomentumValue, c.want)
		}
	}
	if got := contextAt(spec, momentumBars(entry, 100, 110)[:4], 3, market, entry, 0); got.RelativeMomentum != Unknown {
		t.Errorf("a stock too short for the lookback: %q, want unknown", got.RelativeMomentum)
	}
}

func TestNewDimensionsUseOnlyBarsBeforeEntry(t *testing.T) {
	spec := ContextSpec{MomentumSessions: 5, MomentumEdges: []float64{-0.1, 0.1}, LiquiditySessions: 5,
		LiquidityEdges: []float64{1e5, 1e6}, RelativeEdges: []float64{-0.05, 0.05}}
	const entry = 20
	bars, market := momentumBars(entry, 100, 110), momentumBars(entry, 100, 100)
	for i := range bars {
		bars[i].Volume = 5000
	}
	base := contextAt(spec, bars, entry, market, entry, 0)
	if base.Liquidity != "mid" || base.RelativeMomentum != "high" {
		t.Fatalf("context not measured: %+v", base)
	}

	changed, changedMarket := append([]Bar(nil), bars...), append([]Bar(nil), market...)
	for i := entry; i < len(changed); i++ {
		changed[i].Close, changed[i].Open, changed[i].Volume = 1e6, 1e6, 1e12
		changedMarket[i].Close, changedMarket[i].Open, changedMarket[i].Volume = 1, 1, 1e12
	}
	if got := contextAt(spec, changed, entry, changedMarket, entry, 0); !reflect.DeepEqual(base, got) {
		t.Errorf("bars at or after entry changed the context:\n%+v\n%+v", base, got)
	}
	changed[entry-1].Volume = 1e12
	if got := contextAt(spec, changed, entry, market, entry, 0); got.Liquidity != "high" {
		t.Errorf("the last bar before entry did not count: %s", got.Liquidity)
	}
}

func TestContextSpecValidation(t *testing.T) {
	good := func(mutate func(*Spec)) Spec {
		s := testSpec()
		s.Context.LiquiditySessions, s.Context.LiquidityEdges, s.Context.RelativeEdges = 20, []float64{1e6, 1e7}, []float64{-0.05, 0.05}
		mutate(&s)
		return s
	}
	for _, c := range []struct {
		name   string
		mutate func(*Spec)
		ok     bool
	}{
		{"both on", func(*Spec) {}, true},
		{"both off", func(s *Spec) {
			s.Context.LiquiditySessions, s.Context.LiquidityEdges, s.Context.RelativeEdges = 0, nil, nil
		}, true},
		{"liquidity sessions negative", func(s *Spec) { s.Context.LiquiditySessions = -1 }, false},
		{"liquidity edges without sessions", func(s *Spec) { s.Context.LiquiditySessions = 0 }, false},
		{"liquidity sessions without edges", func(s *Spec) { s.Context.LiquidityEdges = nil }, false},
		{"liquidity edges descending", func(s *Spec) { s.Context.LiquidityEdges = []float64{1e7, 1e6} }, false},
		{"liquidity edges equal", func(s *Spec) { s.Context.LiquidityEdges = []float64{1e6, 1e6} }, false},
		{"liquidity edges three", func(s *Spec) { s.Context.LiquidityEdges = []float64{1, 2, 3} }, false},
		{"relative edges descending", func(s *Spec) { s.Context.RelativeEdges = []float64{0.05, -0.05} }, false},
		{"relative edges one", func(s *Spec) { s.Context.RelativeEdges = []float64{0.05} }, false},
		{"condition on liquidity that is on", func(s *Spec) { s.Where = []Condition{{Field: "context:liquidity", Op: "is", Text: "high"}} }, true},
		{"condition on liquidity that is off", func(s *Spec) {
			s.Context.LiquiditySessions, s.Context.LiquidityEdges = 0, nil
			s.Where = []Condition{{Field: "context:liquidity", Op: "is", Text: "high"}}
		}, false},
		{"condition on relative momentum that is off", func(s *Spec) {
			s.Context.RelativeEdges = nil
			s.Where = []Condition{{Field: "context:relative_momentum", Op: "not", Text: "low"}}
		}, false},
	} {
		if err := good(c.mutate).Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %t", c.name, err, c.ok)
		}
	}
	if err := (Condition{Field: "context:relative_momentum", Op: "is", Text: "high"}).Validate(); err != nil {
		t.Errorf("a condition on relative momentum alone: %v", err)
	}
	if err := (Condition{Field: "context:nonsense", Op: "is", Text: "high"}).Validate(); err == nil {
		t.Error("a condition on no dimension was accepted")
	}
	ctx := Context{Liquidity: "high", RelativeMomentum: "low"}
	if ctx.Bucket("liquidity") != "high" || ctx.Bucket("relative_momentum") != "low" || ctx.Bucket("nonsense") != "" {
		t.Errorf("Bucket does not know the new names: %+v", ctx)
	}
}
