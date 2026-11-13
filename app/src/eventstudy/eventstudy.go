// Package eventstudy measures what a stock did after a kind of event, across
// many events, and says how much that measurement can carry.
//
// It answers one question: for the events of one type, entered by one stated
// rule and held for a stated number of sessions, what were the returns, what
// were they against the market and the sector, and under which conditions. It
// does not say what a stock will do.
//
// The rules that keep it honest:
//
//   - No look-ahead. A position is opened at the first session that starts (or,
//     for an entry at the close, ends) after the information was available plus
//     the stated delay. Information known only to the day is usable from the
//     next session. Conditions are computed from bars before the entry session.
//   - Prices are finished daily bars on one basis. An entry "15 minutes after
//     publication" inside a session cannot be measured with daily bars and is
//     not pretended: the entry is the next open or the next close.
//   - Costs are charged on both sides: half the spread, slippage and fees. The
//     benchmark is held without costs, which is against the event.
//   - A holding period with sessions missing from the data is left out, not
//     bridged. A holding period that the data does not finish is censored, not
//     scored.
//   - Development and validation are separate windows. A development outcome
//     whose holding period reaches into the validation window is left out of
//     development. Only validation can make a result "evaluated".
//   - Every group that is tested counts. The significance of each is corrected
//     for the number tested (Holm), so slicing the data more ways needs more
//     evidence, not less.
//   - Events on nearby days are not independent. Uncertainty comes from a
//     bootstrap over blocks of entry days, not over events.
//   - Synthetic data, a universe without the delisted and a missing benchmark
//     each cap the result at "experimental". So does an event rebuilt from
//     public history, unless it comes from an official filing whose acceptance
//     time is on record (attested): when that became public is the regulator's
//     fact, not this installation's reading. Too few events is
//     "insufficient_history". Both are answers.
//
// The package is pure and deterministic: the same specification and data give
// the same report, bit for bit. It reads what the caller supplies and holds no
// state.
package eventstudy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Version names the measurement rules.
const Version = "eventstudy-1"

// Bar is one finished trading day of one security.
type Bar struct {
	Day                    string  `json:"day"` // YYYY-MM-DD, the exchange's calendar
	Open, High, Low, Close float64 `json:"-"`
	Volume                 int64   `json:"-"`
}

// Series is the daily bars of one security on one price basis (the caller has
// applied the splits), oldest first, one bar per day.
type Series struct {
	Security string
	Currency string
	Bars     []Bar
}

// Validate says why the series cannot be measured.
func (s Series) Validate() error {
	for i, b := range s.Bars {
		if _, err := time.Parse(time.DateOnly, b.Day); err != nil {
			return fmt.Errorf("eventstudy: %s: bar day %q is not YYYY-MM-DD", s.Security, b.Day)
		}
		if b.Open <= 0 || b.Close <= 0 || b.Low <= 0 || b.High < b.Low {
			return fmt.Errorf("eventstudy: %s: the bar of %s has a price that cannot be right", s.Security, b.Day)
		}
		if i > 0 && b.Day <= s.Bars[i-1].Day {
			return fmt.Errorf("eventstudy: %s: bars are not in order at %s", s.Security, b.Day)
		}
	}
	return nil
}

// Prices supplies the series of a security; false when there is none.
type Prices interface {
	Series(security string) (Series, bool)
}

// MapPrices is Prices over series held in memory, by security.
type MapPrices map[string]Series

// Series implements Prices.
func (m MapPrices) Series(security string) (Series, bool) {
	s, ok := m[security]
	return s, ok
}

// Observation is one event as the study reads it.
type Observation struct {
	ID       string `json:"id"`
	Security string `json:"security"`
	Type     string `json:"type"`
	// AvailableAt is the earliest time the information could be acted on. When
	// DayPrecision is set only its day (UTC) is meant.
	AvailableAt  time.Time `json:"available_at"`
	DayPrecision bool      `json:"day_precision,omitempty"`
	Sector       string    `json:"sector,omitempty"`
	// Reconstructed says the event was rebuilt from public history, not held by
	// the installation when it happened.
	Reconstructed bool `json:"reconstructed,omitempty"`
	// Attested says a rebuilt event comes from an official filing whose
	// acceptance time is on record. Such an event may count toward an evaluated
	// result; any other rebuilt event may not. It means nothing for an event
	// that is not rebuilt.
	Attested bool `json:"attested,omitempty"`
	// MarketCap is the capitalisation when the event became available, in the
	// currency of the series; zero when unknown.
	MarketCap float64 `json:"market_cap,omitempty"`
	// Numbers and Labels are the event's figures and words, by name, for
	// conditions.
	Numbers map[string]float64 `json:"numbers,omitempty"`
	Labels  map[string]string  `json:"labels,omitempty"`
}

// Entry is the rule by which a position is opened.
type Entry string

// The entries a daily bar can honour.
const (
	NextOpen  Entry = "next_open"  // at the open of the first session that starts after the information
	NextClose Entry = "next_close" // at the close of the first session that ends after the information
)

// Costs are charged on each side of a trade, in basis points of the price.
type Costs struct {
	HalfSpreadBps float64 `json:"half_spread_bps"`
	SlippageBps   float64 `json:"slippage_bps"`
	FeeBps        float64 `json:"fee_bps"`
}

func (c Costs) perSide() float64 { return (c.HalfSpreadBps + c.SlippageBps + c.FeeBps) / 1e4 }

// Session is when the exchange of the series trades.
type Session struct {
	Zone  string `json:"zone"`  // IANA name, "America/New_York"
	Open  string `json:"open"`  // "09:30"
	Close string `json:"close"` // "16:00"
}

// Window is a run of entry days, inclusive. The zero Window is no window.
type Window struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (w Window) set() bool { return w.From != "" || w.To != "" }

func (w Window) has(day string) bool { return w.set() && day >= w.From && day <= w.To }

// ContextSpec says how the conditions around an event are measured and
// bucketed. Each list of edges holds two ascending values and makes the buckets
// low, mid and high.
type ContextSpec struct {
	VolatilitySessions int       `json:"volatility_sessions"`
	MomentumSessions   int       `json:"momentum_sessions"`
	TrendSessions      int       `json:"trend_sessions"`
	VolatilityEdges    []float64 `json:"volatility_edges"` // annualised volatility of daily returns
	MomentumEdges      []float64 `json:"momentum_edges"`   // return over the momentum sessions
	SizeEdges          []float64 `json:"size_edges"`       // market capitalisation

	// LiquiditySessions and LiquidityEdges turn on the liquidity dimension: the
	// mean traded value (close times volume) of the sessions before entry, in the
	// currency of the series. RelativeEdges turns on relative momentum: the
	// stock's return over MomentumSessions less the benchmark's. Both are left out
	// of the specification, and so of its hash and of the report's breakdown, until
	// they are set.
	LiquiditySessions int       `json:"liquidity_sessions,omitempty"`
	LiquidityEdges    []float64 `json:"liquidity_edges,omitempty"`
	RelativeEdges     []float64 `json:"relative_edges,omitempty"`
}

// Dimensions are the conditions the specification breaks a report's groups
// down by, in order: the original five, then liquidity and relative momentum
// when they are turned on.
func (c ContextSpec) Dimensions() []string {
	out := Dimensions()
	if c.LiquiditySessions > 0 {
		out = append(out, "liquidity")
	}
	if len(c.RelativeEdges) > 0 {
		out = append(out, "relative_momentum")
	}
	return out
}

// Context is the conditions around one event, measured from bars before the
// entry session. A bucket is "unknown" when the data does not reach.
type Context struct {
	Volatility       string `json:"volatility"`        // of the stock: low, mid, high
	Momentum         string `json:"momentum"`          // the stock's earlier return: low, mid, high
	Size             string `json:"size"`              // by market capitalisation: low, mid, high
	MarketTrend      string `json:"market_trend"`      // the benchmark against its own average: up, down
	MarketVolatility string `json:"market_volatility"` // of the benchmark: low, mid, high

	// Liquidity and RelativeMomentum are empty when the study does not measure
	// them: low, mid, high, or "unknown" when it does and the data does not reach.
	Liquidity        string `json:"liquidity,omitempty"`         // mean traded value before entry
	RelativeMomentum string `json:"relative_momentum,omitempty"` // the stock's earlier return less the benchmark's

	VolatilityValue       *float64 `json:"volatility_value,omitempty"`
	MomentumValue         *float64 `json:"momentum_value,omitempty"`
	MarketTrendValue      *float64 `json:"market_trend_value,omitempty"`
	MarketVolatilityValue *float64 `json:"market_volatility_value,omitempty"`
	LiquidityValue        *float64 `json:"liquidity_value,omitempty"`
	RelativeMomentumValue *float64 `json:"relative_momentum_value,omitempty"`
}

// Unknown is the bucket of a condition that could not be measured.
const Unknown = "unknown"

// Dimensions are the conditions every report breaks its groups down by, in
// order. A study may add the two in ExtraDimensions; ContextSpec.Dimensions
// lists the ones a specification uses.
func Dimensions() []string {
	return []string{"volatility", "momentum", "size", "market_trend", "market_volatility"}
}

// ExtraDimensions are the conditions a study breaks its groups down by only
// when its context specification turns them on.
func ExtraDimensions() []string {
	return []string{"liquidity", "relative_momentum"}
}

// AllDimensions are the names Bucket and a context condition understand.
func AllDimensions() []string {
	return append(Dimensions(), ExtraDimensions()...)
}

// Bucket returns the bucket of one dimension; empty for a name that is not one
// and for a dimension the study did not measure.
func (c Context) Bucket(dimension string) string {
	switch dimension {
	case "volatility":
		return c.Volatility
	case "momentum":
		return c.Momentum
	case "size":
		return c.Size
	case "market_trend":
		return c.MarketTrend
	case "market_volatility":
		return c.MarketVolatility
	case "liquidity":
		return c.Liquidity
	case "relative_momentum":
		return c.RelativeMomentum
	}
	return ""
}

// Spec is a study, stated before it is run.
type Spec struct {
	ID   string `json:"id"`
	Note string `json:"note,omitempty"`
	// Types are the event types studied; each is measured on its own.
	Types []string `json:"types"`
	// Where keeps only the events that meet every condition. A condition on a
	// figure the event does not have leaves the event out.
	Where    []Condition `json:"where,omitempty"`
	Horizons []int       `json:"horizons"` // sessions held
	Entry    Entry       `json:"entry"`
	// EntryDelayMinutes is added to the time the information was available
	// before the first usable session is looked for.
	EntryDelayMinutes int     `json:"entry_delay_minutes"`
	Costs             Costs   `json:"costs"`
	Session           Session `json:"session"`
	// MaxEntryGapDays refuses an entry more than this many calendar days after
	// the information: the sessions in between are missing from the data.
	MaxEntryGapDays int `json:"max_entry_gap_days"`
	// Benchmark is the security whose series stands for the market; empty for
	// none. SectorBenchmarks gives the series of a sector, by sector name.
	Benchmark        string            `json:"benchmark,omitempty"`
	SectorBenchmarks map[string]string `json:"sector_benchmarks,omitempty"`
	Context          ContextSpec       `json:"context"`
	// Development is where patterns may be looked for; Validation is read to
	// confirm them. They must not overlap.
	Development Window `json:"development"`
	Validation  Window `json:"validation"`
	// MinGroup and MinClusters are the fewest events and the fewest independent
	// blocks of entry days a group needs before its figures are read at all.
	MinGroup    int     `json:"min_group"`
	MinClusters int     `json:"min_clusters"`
	Alpha       float64 `json:"alpha"`
	Resamples   int     `json:"resamples"`
}

// DefaultSpec is a starting point. Every number in it is a conservative,
// arbitrary choice to be reviewed, not a finding.
func DefaultSpec() Spec {
	return Spec{
		Horizons:          []int{1, 3, 5, 10, 20},
		Entry:             NextOpen,
		EntryDelayMinutes: 15,
		Costs:             Costs{HalfSpreadBps: 5, SlippageBps: 5, FeeBps: 5},
		Session:           Session{Zone: "America/New_York", Open: "09:30", Close: "16:00"},
		MaxEntryGapDays:   5,
		Context: ContextSpec{
			VolatilitySessions: 20, MomentumSessions: 60, TrendSessions: 200,
			VolatilityEdges: []float64{0.25, 0.50}, MomentumEdges: []float64{-0.10, 0.10}, SizeEdges: []float64{2e9, 1e10},
		},
		MinGroup: 30, MinClusters: 10, Alpha: 0.05, Resamples: 2000,
	}
}

// Validate says why the specification cannot be run.
func (s Spec) Validate() error {
	switch {
	case s.ID == "":
		return errors.New("eventstudy: the study needs an id")
	case len(s.Types) == 0:
		return errors.New("eventstudy: the study names no event type")
	case len(s.Horizons) == 0:
		return errors.New("eventstudy: the study names no holding period")
	case s.Entry != NextOpen && s.Entry != NextClose:
		return fmt.Errorf("eventstudy: entry %q is not next_open or next_close: a daily bar can honour no other", s.Entry)
	case s.EntryDelayMinutes < 0 || s.EntryDelayMinutes > 24*60:
		return errors.New("eventstudy: the entry delay must be between 0 minutes and a day")
	case s.Costs.HalfSpreadBps < 0 || s.Costs.SlippageBps < 0 || s.Costs.FeeBps < 0 || s.Costs.perSide() >= 0.05:
		return errors.New("eventstudy: costs must not be negative and must stay under 500 basis points a side")
	case s.MaxEntryGapDays < 1:
		return errors.New("eventstudy: max_entry_gap_days must be at least 1")
	case s.MinGroup < 2 || s.MinClusters < 2:
		return errors.New("eventstudy: min_group and min_clusters must be at least 2")
	case s.Alpha <= 0 || s.Alpha >= 0.5:
		return errors.New("eventstudy: alpha must be between 0 and 0.5")
	case s.Resamples < 100:
		return errors.New("eventstudy: at least 100 resamples are needed for an interval")
	}
	seen := map[int]bool{}
	for _, h := range s.Horizons {
		if h < 1 || h > 252 || seen[h] {
			return fmt.Errorf("eventstudy: holding period %d is not between 1 and 252 sessions, or is given twice", h)
		}
		seen[h] = true
	}
	enabled := map[string]bool{}
	for _, d := range s.Context.Dimensions() {
		enabled[d] = true
	}
	for _, c := range s.Where {
		if err := c.Validate(); err != nil {
			return err
		}
		if kind, name := c.kind(); kind == "context" && !enabled[name] {
			return fmt.Errorf("eventstudy: condition %q needs the %s dimension, which the context does not turn on", c.Field, name)
		}
	}
	if _, err := newSessions(s.Session); err != nil {
		return err
	}
	for name, edges := range map[string][]float64{"volatility_edges": s.Context.VolatilityEdges, "momentum_edges": s.Context.MomentumEdges, "size_edges": s.Context.SizeEdges} {
		if !twoAscending(edges) {
			return fmt.Errorf("eventstudy: context %s must be two ascending values", name)
		}
	}
	switch {
	case s.Context.LiquiditySessions < 0:
		return errors.New("eventstudy: context liquidity_sessions must not be negative")
	case s.Context.LiquiditySessions == 0 && len(s.Context.LiquidityEdges) > 0:
		return errors.New("eventstudy: context liquidity_edges need liquidity_sessions of at least 1")
	case s.Context.LiquiditySessions > 0 && !twoAscending(s.Context.LiquidityEdges):
		return errors.New("eventstudy: context liquidity_edges must be two ascending values")
	case len(s.Context.RelativeEdges) > 0 && !twoAscending(s.Context.RelativeEdges):
		return errors.New("eventstudy: context relative_edges must be two ascending values")
	}
	if s.Context.VolatilitySessions < 5 || s.Context.MomentumSessions < 5 || s.Context.TrendSessions < 5 {
		return errors.New("eventstudy: context lookbacks must be at least 5 sessions")
	}
	for _, w := range []struct {
		name string
		w    Window
	}{{"development", s.Development}, {"validation", s.Validation}} {
		if !w.w.set() {
			continue
		}
		if _, err := time.Parse(time.DateOnly, w.w.From); err != nil {
			return fmt.Errorf("eventstudy: %s from %q is not YYYY-MM-DD", w.name, w.w.From)
		}
		if _, err := time.Parse(time.DateOnly, w.w.To); err != nil {
			return fmt.Errorf("eventstudy: %s to %q is not YYYY-MM-DD", w.name, w.w.To)
		}
		if w.w.To < w.w.From {
			return fmt.Errorf("eventstudy: %s ends before it starts", w.name)
		}
	}
	if s.Development.set() && s.Validation.set() && s.Validation.From <= s.Development.To {
		return errors.New("eventstudy: validation must start after development ends")
	}
	return nil
}

func twoAscending(edges []float64) bool {
	return len(edges) == 2 && edges[0] < edges[1]
}

// Hash is a digest of the specification.
func (s Spec) Hash() string { return hashJSON(s) }

func hashJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Manifest states what the data is. The study cannot find these out; whoever
// assembled the data says them, and the report repeats them.
type Manifest struct {
	// Synthetic says the events or prices are invented.
	Synthetic bool `json:"synthetic"`
	// PointInTimeUniverse says the events were drawn from every security that
	// existed at the time, not from those that exist today.
	PointInTimeUniverse bool `json:"point_in_time_universe"`
	// IncludesDelisted says the prices cover securities that have since stopped
	// trading.
	IncludesDelisted bool   `json:"includes_delisted"`
	Note             string `json:"note,omitempty"`
}

// Periods of an outcome.
const (
	Development = "development"
	Validation  = "validation"
	Outside     = "outside" // in neither window, or no window was stated
)

// States of an outcome and of a holding period.
const (
	Measured  = "measured"
	NoPrices  = "no_prices"  // no series for the security
	BadPrices = "bad_prices" // the series cannot be right
	NoEntry   = "no_entry"   // the data ends before a session after the information
	LateEntry = "late_entry" // the first session in the data is too long after the information
	Filtered  = "filtered"   // a condition of the study does not hold or cannot be told
	Censored  = "censored"   // the data ends before the holding period does
	Gap       = "gap"        // sessions are missing inside the holding period
	Leaks     = "leaks"      // a development holding period reaches into validation
)

// Held is one holding period of one event.
type Held struct {
	Sessions int    `json:"sessions"`
	State    string `json:"state"`
	ExitDay  string `json:"exit_day,omitempty"`
	// Gross is the price return; Net is after costs on both sides.
	Gross float64 `json:"gross"`
	Net   float64 `json:"net"`
	// Market and Sector are the returns of the benchmarks over the same days,
	// without costs; nil when there is no benchmark or it lacks those days.
	Market *float64 `json:"market,omitempty"`
	Sector *float64 `json:"sector,omitempty"`
	// VsMarket and VsSector are Net less the benchmark.
	VsMarket *float64 `json:"vs_market,omitempty"`
	VsSector *float64 `json:"vs_sector,omitempty"`
	// Drawdown is the lowest price reached while held against the entry price,
	// as a change; zero or below.
	Drawdown float64 `json:"drawdown"`
}

// Outcome is what happened after one event.
type Outcome struct {
	Observation string  `json:"observation"`
	Security    string  `json:"security"`
	Type        string  `json:"type"`
	State       string  `json:"state"`
	Why         string  `json:"why,omitempty"`
	Period      string  `json:"period,omitempty"`
	EntryDay    string  `json:"entry_day,omitempty"`
	EntryPrice  float64 `json:"entry_price,omitempty"`
	Context     Context `json:"context"`
	Held        []Held  `json:"held,omitempty"`
}

// Stats summarises one measure over the events of a group.
type Stats struct {
	N        int     `json:"n"`
	Clusters int     `json:"clusters"`
	Mean     float64 `json:"mean"`
	Median   float64 `json:"median"`
	WinRate  float64 `json:"win_rate"` // the share above zero
	StdDev   float64 `json:"std_dev"`
	Worst    float64 `json:"worst"`
	Best     float64 `json:"best"`
	// RiskAdjusted is the mean over the standard deviation, scaled to a year of
	// sessions for the holding period.
	RiskAdjusted float64 `json:"risk_adjusted"`
	// Lower and Upper bound the mean at the study's confidence, from a
	// bootstrap over blocks of entry days. P is the two-sided share of resamples
	// on the other side of zero; PAdjusted is P corrected for the number of
	// groups tested, and is 1 for a group that was not tested.
	Lower     float64 `json:"lower"`
	Upper     float64 `json:"upper"`
	P         float64 `json:"p"`
	PAdjusted float64 `json:"p_adjusted"`
}

// Statuses of a group and of a report.
const (
	Insufficient = "insufficient_history"
	Experimental = "experimental"
	Evaluated    = "evaluated"
)

// Group is the events of one type, holding period, window and condition.
type Group struct {
	Type      string `json:"type"`
	Sessions  int    `json:"sessions"`
	Period    string `json:"period"`
	Dimension string `json:"dimension"` // "all", or one of the study's ContextSpec.Dimensions
	Bucket    string `json:"bucket"`    // "all", or the bucket of the dimension
	// Net is after costs; VsMarket and VsSector are Net less the benchmark, over
	// the events that have one.
	Net      Stats  `json:"net"`
	VsMarket *Stats `json:"vs_market,omitempty"`
	VsSector *Stats `json:"vs_sector,omitempty"`
	// MeanDrawdown and MaxDrawdown are the average and the deepest fall below
	// the entry price while held.
	MeanDrawdown float64 `json:"mean_drawdown"`
	MaxDrawdown  float64 `json:"max_drawdown"`
	// Reconstructed is how many of the events were rebuilt from public history
	// without an attested time; Attested is how many were rebuilt with one.
	Reconstructed int    `json:"reconstructed"`
	Attested      int    `json:"attested"`
	Status        string `json:"status"`
	Why           string `json:"why,omitempty"`
}

// Primary is the measure a group is judged by: against the market when there
// is a benchmark, after costs alone when there is none.
func (g Group) Primary() Stats {
	if g.VsMarket != nil {
		return *g.VsMarket
	}
	return g.Net
}

// Report is the result of one run of one study.
type Report struct {
	Version  string   `json:"version"`
	Spec     Spec     `json:"spec"`
	SpecHash string   `json:"spec_hash"`
	DataHash string   `json:"data_hash"`
	Manifest Manifest `json:"manifest"`
	// Counts is how many events ended in each state.
	Counts map[string]int `json:"counts"`
	// Tested is how many validation groups the significance was corrected for.
	Tested int     `json:"tested"`
	Groups []Group `json:"groups"`
	// Limits are the reasons no group of this report can be "evaluated".
	Limits   []string  `json:"limits,omitempty"`
	Status   string    `json:"status"`
	Outcomes []Outcome `json:"outcomes,omitempty"`
}

// Find returns the group of a type, holding period, window, dimension and
// bucket; false when the report has none.
func (r Report) Find(typ string, sessions int, period, dimension, bucket string) (Group, bool) {
	for _, g := range r.Groups {
		if g.Type == typ && g.Sessions == sessions && g.Period == period && g.Dimension == dimension && g.Bucket == bucket {
			return g, true
		}
	}
	return Group{}, false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
