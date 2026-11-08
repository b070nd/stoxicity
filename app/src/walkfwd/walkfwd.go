// Package walkfwd replays what a strategy proposed against finished daily bars
// and says, against criteria fixed beforehand, how much that shows.
//
// The rules that keep it honest:
//
//   - Periods are frozen: train, tune, test and an untouched reserve, in order,
//     separated by an embargo at least as long as the longest holding horizon. A
//     test period is read once for the record and every further read is flagged;
//     the reserve can be read once.
//   - Every strategy and parameter family tried is registered. The number of
//     trials sets the confidence level (Bonferroni), so the more that was tried
//     the more evidence a result needs.
//   - Information known only after a decision cannot be used for it, and an entry
//     is never executable before the session after the decision and the day the
//     information became available. A proposal that breaks that is excluded and
//     counted, not repaired.
//   - A replay is "known" (the installation had the information then) or
//     "reconstructed" (public history rebuilt today). They are never mixed, and a
//     reconstructed replay cannot reach "evaluated".
//   - Fills follow the paper engine's conservative rules on daily bars: a buy
//     fills only if the low went strictly below the limit, at the limit, limited
//     by a share of the volume. If one bar could have both filled the entry and hit
//     the stop, the stop is taken. A stop never fills at its own price: a gap
//     fills at the open, and slippage is added. A delisting with no terminal value
//     is a total loss. A position with no exit in the data is censored and left out
//     of matured figures; it is not marked as a result.
//   - Costs, withholding on distributions, and slippage are shown with their
//     sensitivity, not assumed away.
//   - Trades that overlap in time are not independent. Uncertainty comes from a
//     bootstrap over clusters of overlapping trades, not over trades.
//   - Synthetic data, an unaddressed survivorship bias, too few matured trades,
//     too few clusters, or thin coverage can never give "evaluated". Insufficient
//     evidence is an answer.
package walkfwd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/allocate"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/paper"
)

// Version names the evaluation rules.
const Version = "walkfwd-1"

// Kind of replay.
type Kind string

const (
	Known         Kind = "known"
	Reconstructed Kind = "reconstructed"
)

// Period of the frozen split.
type Period string

const (
	Train   Period = "train"
	Tune    Period = "tune"
	Test    Period = "test"
	Reserve Period = "reserve"
)

// Span is a run of days, inclusive.
type Span struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Splits are the frozen periods.
type Splits struct {
	Train       Span `json:"train"`
	Tune        Span `json:"tune"`
	Test        Span `json:"test"`
	Reserve     Span `json:"reserve"`
	EmbargoDays int  `json:"embargo_days"`
}

func day(s string) (time.Time, error) { return time.Parse(time.DateOnly, s) }

// Validate says why the splits cannot be used.
func (s Splits) Validate() error {
	spans := []struct {
		name string
		span Span
	}{{"train", s.Train}, {"tune", s.Tune}, {"test", s.Test}, {"reserve", s.Reserve}}
	var prevTo time.Time
	for i, sp := range spans {
		f, err := day(sp.span.From)
		if err != nil {
			return fmt.Errorf("walkfwd: %s from %q is not YYYY-MM-DD", sp.name, sp.span.From)
		}
		t, err := day(sp.span.To)
		if err != nil {
			return fmt.Errorf("walkfwd: %s to %q is not YYYY-MM-DD", sp.name, sp.span.To)
		}
		if t.Before(f) {
			return fmt.Errorf("walkfwd: %s ends before it starts", sp.name)
		}
		if i > 0 && f.Sub(prevTo) < time.Duration(s.EmbargoDays+1)*24*time.Hour {
			return fmt.Errorf("walkfwd: %s starts less than the embargo (%d days) after the period before it ends: trades held across the boundary would leak", sp.name, s.EmbargoDays)
		}
		prevTo = t
	}
	if s.EmbargoDays < 1 {
		return errors.New("walkfwd: the embargo must be at least 1 day, and as long as the longest holding horizon")
	}
	return nil
}

// Of says which period a day falls in; false for a day in an embargo or outside.
func (s Splits) Of(d string) (Period, bool) {
	for _, p := range []struct {
		p Period
		s Span
	}{{Train, s.Train}, {Tune, s.Tune}, {Test, s.Test}, {Reserve, s.Reserve}} {
		if d >= p.s.From && d <= p.s.To {
			return p.p, true
		}
	}
	return "", false
}

// Trial is a strategy and parameter family that was tried.
type Trial struct {
	ID       string `json:"id"`
	Strategy string `json:"strategy"`
	Params   string `json:"params"`
	Note     string `json:"note,omitempty"`
}

// Criteria are stated before the test period is read.
type Criteria struct {
	MinMatured     int     `json:"min_matured_trades"`
	MinClusters    int     `json:"min_clusters"`
	MinCoverage    float64 `json:"min_coverage"`
	MinFillRate    float64 `json:"min_fill_rate"`
	MinExcessLower float64 `json:"min_excess_lower_bound"`
	// MaxWorstLoss is the downside limit: the worst single net return must not be
	// below minus this.
	MaxWorstLoss float64 `json:"max_worst_loss"`
	Alpha        float64 `json:"alpha"`
}

// Validate says why criteria cannot be used.
func (c Criteria) Validate() error {
	switch {
	case c.MinMatured < 1 || c.MinClusters < 2:
		return errors.New("walkfwd: min_matured_trades must be at least 1 and min_clusters at least 2")
	case c.MinCoverage <= 0 || c.MinCoverage > 1 || c.MinFillRate <= 0 || c.MinFillRate > 1:
		return errors.New("walkfwd: min_coverage and min_fill_rate must be above 0 and at most 1")
	case c.MaxWorstLoss <= 0 || c.MaxWorstLoss > 1:
		return errors.New("walkfwd: max_worst_loss must be above 0 and at most 1")
	case c.Alpha <= 0 || c.Alpha >= 0.5:
		return errors.New("walkfwd: alpha must be between 0 and 0.5")
	}
	return nil
}

// Config of the simulation.
type Config struct {
	MaxParticipation float64       `json:"max_participation"`
	MinFill          float64       `json:"min_fill"`
	StopSlippage     float64       `json:"stop_slippage"`
	ExitSlippage     float64       `json:"exit_slippage"`
	GapTolerance     int           `json:"gap_tolerance_days"`
	Withholding      float64       `json:"withholding"`
	Fees             allocate.Fees `json:"-"`
	CashRate         float64       `json:"cash_rate"`
	FundCost         float64       `json:"fund_cost_per_side"`
	Slippage         []float64     `json:"slippage_sensitivity"`
	WithholdingSens  []float64     `json:"withholding_sensitivity"`
}

// DefaultConfig is conservative and arbitrary, like every number here.
func DefaultConfig() Config {
	return Config{MaxParticipation: 0.01, MinFill: 0.25, StopSlippage: 0.005, ExitSlippage: 0.001, GapTolerance: 4, Withholding: 0.15,
		Slippage: []float64{0, 0.002, 0.005}, WithholdingSens: []float64{0, 0.15, 0.30}}
}

// Proposal is what a strategy proposed. Qty zero is a decision not to trade; it
// is kept, because only counting the winners would be cherry-picking.
type Proposal struct {
	ID              string         `json:"id"`
	Trial           string         `json:"trial"`
	Security        string         `json:"security"`
	Sector          string         `json:"sector,omitempty"`
	Decided         string         `json:"decided"`  // the day the decision was made
	KnownOn         string         `json:"known_on"` // the day the information became available
	Limit           money.Decimal  `json:"limit"`
	Qty             money.Decimal  `json:"qty"`
	Invalidation    *money.Decimal `json:"invalidation,omitempty"`
	HorizonSessions int            `json:"horizon_sessions"`
	Currency        money.Currency `json:"currency"`
}

// Dist is a cash distribution per share.
type Dist struct {
	Security string        `json:"security"`
	ExDay    string        `json:"ex_day"`
	PerShare money.Decimal `json:"per_share"`
}

// Delisting says a security stopped trading; Terminal is the per-share value
// received, nil when unknown.
type Delisting struct {
	Security string         `json:"security"`
	Day      string         `json:"day"`
	Terminal *money.Decimal `json:"terminal,omitempty"`
}

// Manifest states what the data is.
type Manifest struct {
	Kind             Kind   `json:"kind"`
	Synthetic        bool   `json:"synthetic"`
	PointInTime      bool   `json:"point_in_time_universe"`
	IncludesDelisted bool   `json:"includes_delisted"`
	Fund             string `json:"fund_security,omitempty"` // the diversified baseline, in the bars
	Note             string `json:"note,omitempty"`
}

// Outcome of one proposal.
type Outcome struct {
	Proposal string `json:"proposal"`
	Security string `json:"security"`
	Sector   string `json:"sector,omitempty"`
	// State: no_trade, excluded (lookahead), no_data, unfilled, thin, open
	// (censored), closed.
	State           string   `json:"state"`
	Why             string   `json:"why,omitempty"`
	Entry           string   `json:"entry,omitempty"`
	Exit            string   `json:"exit,omitempty"`
	Reason          string   `json:"exit_reason,omitempty"`
	Days            int      `json:"sessions_held,omitempty"`
	Cost            int64    `json:"cost,omitempty"` // minor units paid for the shares and entry fee
	PnL             int64    `json:"pnl,omitempty"`
	Fees            int64    `json:"fees,omitempty"`
	Dist            int64    `json:"distributions,omitempty"`
	Return          float64  `json:"net_return,omitempty"`
	Fund            *float64 `json:"fund_return,omitempty"`
	Gap             bool     `json:"gap,omitempty"`
	Ambiguous       bool     `json:"ambiguous_bar,omitempty"`
	Delisted        bool     `json:"delisted,omitempty"`
	Missing         bool     `json:"missing_history,omitempty"`
	qty             float64
	entryPx, exitPx float64
	notional        float64
	distGross       float64
	feeIn, feeOut   float64
}

func toF(d money.Decimal) float64 {
	var f float64
	fmt.Sscanf(d.String(), "%g", &f)
	return f
}

func rounded(f float64) int64 { return int64(math.Round(f)) }

type series struct {
	bars []paper.Bar
	idx  map[string]int
}

func index(bars []paper.Bar) map[string]*series {
	by := map[string]*series{}
	for _, b := range bars {
		s := by[b.Security]
		if s == nil {
			s = &series{idx: map[string]int{}}
			by[b.Security] = s
		}
		s.bars = append(s.bars, b)
	}
	for _, s := range by {
		sort.SliceStable(s.bars, func(i, j int) bool { return s.bars[i].Day < s.bars[j].Day })
		for i, b := range s.bars {
			s.idx[b.Day] = i
		}
	}
	return by
}

func weekdaysBetween(a, b string) int {
	x, _ := day(a)
	y, _ := day(b)
	n := 0
	for x.Before(y) {
		x = x.AddDate(0, 0, 1)
		if wd := x.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n
}

// Simulate runs one proposal over the bars.
func Simulate(cfg Config, p Proposal, by map[string]*series, dists []Dist, delist []Delisting, fund *series) Outcome {
	o := Outcome{Proposal: p.ID, Security: p.Security, Sector: p.Sector}
	if p.Qty.Sign() <= 0 {
		o.State, o.Why = "no_trade", "the strategy decided not to trade"
		return o
	}
	if p.KnownOn > p.Decided {
		o.State, o.Why = "excluded", fmt.Sprintf("lookahead: the information was known on %s, after the decision on %s", p.KnownOn, p.Decided)
		return o
	}
	avail := p.KnownOn
	if p.Decided > avail {
		avail = p.Decided
	}
	s := by[p.Security]
	if s == nil {
		o.State, o.Why, o.Missing = "no_data", "no bars for the security", true
		return o
	}
	e := sort.Search(len(s.bars), func(i int) bool { return s.bars[i].Day > avail })
	if e >= len(s.bars) {
		o.State, o.Why, o.Missing = "no_data", "no bar after the day the decision could be acted on", true
		return o
	}
	eb := s.bars[e]
	limit := toF(p.Limit)
	if !(toF(eb.Low) < limit) {
		o.State, o.Why = "unfilled", fmt.Sprintf("the low of %s (%s) did not go strictly below the limit %s", eb.Day, eb.Low, p.Limit)
		return o
	}
	qty := math.Floor(math.Min(toF(p.Qty), float64(eb.Volume)*cfg.MaxParticipation))
	if qty < cfg.MinFill*toF(p.Qty) || qty < 1 {
		o.State, o.Why = "thin", fmt.Sprintf("the volume of %s allows %.0f of %s shares: below the minimum fill", eb.Day, qty, p.Qty)
		return o
	}
	o.Entry = eb.Day
	o.qty, o.entryPx = qty, limit
	o.notional = qty * limit
	o.feeIn = float64(cfg.Fees.Of(rounded(o.notional*100), false)) / 100
	// find the exit
	exitDay, exitPx, reason := "", 0.0, ""
	stop := 0.0
	if p.Invalidation != nil {
		stop = toF(*p.Invalidation)
	}
	stopFill := func(b paper.Bar) float64 {
		px := stop
		if toF(b.Open) <= stop {
			o.Gap = true
			px = toF(b.Open)
		}
		return px * (1 - cfg.StopSlippage)
	}
	if stop > 0 && toF(eb.Low) <= stop {
		o.Ambiguous = true // the bar could have filled the entry and hit the stop: the stop is taken
		exitDay, exitPx, reason = eb.Day, stopFill(eb), "invalidated"
	}
	var ds *Delisting
	for i := range delist {
		if delist[i].Security == p.Security {
			ds = &delist[i]
		}
	}
	last := e
	for i := e + 1; exitDay == "" && i < len(s.bars); i++ {
		b := s.bars[i]
		if weekdaysBetween(s.bars[i-1].Day, b.Day) > cfg.GapTolerance {
			o.Missing = true
		}
		last = i
		if stop > 0 && toF(b.Low) <= stop {
			exitDay, exitPx, reason = b.Day, stopFill(b), "invalidated"
			break
		}
		if i-e >= p.HorizonSessions {
			// the time exit is decided at the close and executed at the next open
			if i+1 < len(s.bars) {
				nb := s.bars[i+1]
				exitDay, exitPx, reason = nb.Day, toF(nb.Open)*(1-cfg.ExitSlippage), "time"
			}
			break
		}
	}
	if exitDay == "" {
		switch {
		case ds != nil && ds.Day >= s.bars[last].Day:
			o.Delisted = true
			exitDay, reason = ds.Day, "delisted"
			if ds.Terminal != nil {
				exitPx = toF(*ds.Terminal)
			} // else a total loss: exitPx 0
		default:
			o.State, o.Why = "open", "the data ends before the exit: the trade is censored, not a result"
			return o
		}
	}
	o.Exit, o.Reason = exitDay, reason
	o.Days = weekdaysBetween(o.Entry, exitDay)
	o.exitPx = exitPx
	o.feeOut = float64(cfg.Fees.Of(rounded(qty*exitPx*100), false)) / 100
	for _, d := range dists {
		if d.Security == p.Security && d.ExDay > o.Entry && d.ExDay <= exitDay {
			o.distGross += qty * toF(d.PerShare)
		}
	}
	o.State = "closed"
	o.finish(cfg.Withholding, 0)
	if fund != nil {
		a, aok := fund.idx[o.Entry]
		b, bok := fund.idx[exitDay]
		if !bok { // the nearest fund bar on or before the exit
			k := sort.Search(len(fund.bars), func(i int) bool { return fund.bars[i].Day > exitDay })
			b, bok = k-1, k > 0
		}
		if aok && bok && b > a {
			r := toF(fund.bars[b].Close)/toF(fund.bars[a].Close) - 1 - 2*cfg.FundCost
			o.Fund = &r
		}
	}
	return o
}

// finish computes the money figures with a withholding rate and extra slippage.
func (o *Outcome) finish(withholding, slip float64) {
	cost := o.qty*o.entryPx*(1+slip) + o.feeIn
	proceeds := o.qty*o.exitPx*(1-slip) - o.feeOut
	dist := o.distGross * (1 - withholding)
	o.Cost, o.Fees, o.Dist = rounded(cost*100), rounded((o.feeIn+o.feeOut)*100), rounded(dist*100)
	o.PnL = rounded((proceeds - cost + dist) * 100)
	if cost > 0 {
		o.Return = (proceeds - cost + dist) / cost
	}
}

func (o Outcome) returnWith(withholding, slip float64) float64 {
	c := o
	c.finish(withholding, slip)
	return c.Return
}

// ---- statistics ----

// Interval of a bootstrap.
type Interval struct {
	Mean, Low, High float64
	N, Clusters     int
}

// clusters groups trades whose holding windows overlap.
func clusters(rs []Outcome) [][]int {
	idx := make([]int, len(rs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return rs[idx[a]].Entry < rs[idx[b]].Entry })
	var out [][]int
	end := ""
	for _, i := range idx {
		if len(out) > 0 && rs[i].Entry <= end {
			out[len(out)-1] = append(out[len(out)-1], i)
			if rs[i].Exit > end {
				end = rs[i].Exit
			}
			continue
		}
		out = append(out, []int{i})
		end = rs[i].Exit
	}
	return out
}

// bootstrap resamples clusters, not trades, with a seed that depends on the
// manifest so that a rerun gives the same interval.
func bootstrap(vals []float64, cl [][]int, alpha float64, seed uint64) Interval {
	iv := Interval{N: len(vals), Clusters: len(cl)}
	if len(vals) == 0 {
		return iv
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	iv.Mean = sum / float64(len(vals))
	if len(cl) < 2 {
		iv.Low, iv.High = math.Inf(-1), math.Inf(1)
		return iv
	}
	const B = 2000
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	means := make([]float64, 0, B)
	for b := 0; b < B; b++ {
		s, n := 0.0, 0
		for range cl {
			for _, i := range cl[rng.IntN(len(cl))] {
				s += vals[i]
				n++
			}
		}
		means = append(means, s/float64(n))
	}
	sort.Float64s(means)
	iv.Low = means[int(math.Floor(alpha/2*float64(B)))]
	iv.High = means[min(B-1, int(math.Ceil((1-alpha/2)*float64(B)))-1)]
	return iv
}

// Group is a breakdown line.
type Group struct {
	Name string  `json:"name"`
	N    int     `json:"n"`
	Mean float64 `json:"mean_net_return"`
}

// Status of the evidence.
type Status string

const (
	Evaluated           Status = "evaluated"
	Experimental        Status = "experimental"
	InsufficientHistory Status = "insufficient_history"
)

// Report is the result of one replay of one trial over one period.
type Report struct {
	Version  string   `json:"version"`
	Trial    string   `json:"trial"`
	Period   Period   `json:"period"`
	Kind     Kind     `json:"kind"`
	Manifest string   `json:"manifest_hash"`
	Status   Status   `json:"status"`
	Why      []string `json:"why"`

	Trials      int     `json:"trials_registered"`
	Proposals   int     `json:"proposals"`
	NoTrade     int     `json:"no_trade_decisions"`
	Excluded    int     `json:"excluded_lookahead"`
	Filled      int     `json:"filled"`
	Unfilled    int     `json:"unfilled_or_thin"`
	Matured     int     `json:"matured"`
	Censored    int     `json:"censored"`
	Delisted    int     `json:"delisted"`
	Ambiguous   int     `json:"ambiguous_bars"`
	MissingData int     `json:"missing_history"`
	Coverage    float64 `json:"coverage"`
	FillRate    float64 `json:"fill_rate"`
	ClusterN    int     `json:"clusters"`

	Mean       float64   `json:"mean_net_return"`
	Median     float64   `json:"median_net_return"`
	HitRate    float64   `json:"hit_rate"`
	Worst      float64   `json:"worst_net_return"`
	Fifth      float64   `json:"fifth_percentile"`
	Costs      int64     `json:"fees_total"`
	Turnover   int64     `json:"gross_traded"`
	MeanDays   float64   `json:"mean_sessions_held"`
	Excess     *Interval `json:"excess_vs_fund,omitempty"`
	ExcessCash *Interval `json:"excess_vs_cash,omitempty"`
	Level      float64   `json:"confidence_level"`

	SlippageSens    map[string]float64 `json:"mean_return_by_extra_slippage"`
	WithholdingSens map[string]float64 `json:"mean_return_by_withholding"`
	ByQuarter       []Group            `json:"by_quarter"`
	BySector        []Group            `json:"by_sector"`
	Diagnostics     []string           `json:"diagnostics"`
	Outcomes        []Outcome          `json:"outcomes,omitempty"`
}

// Input is a replay.
type Input struct {
	Trial     Trial
	Trials    int
	Period    Period
	Splits    Splits
	Criteria  Criteria
	Config    Config
	Manifest  Manifest
	Proposals []Proposal
	Bars      []paper.Bar
	Dists     []Dist
	Delist    []Delisting
	// TestReads is how many times the test period was read before this run.
	TestReads int
}

// ManifestHash binds every input to the result.
func ManifestHash(in Input) string {
	b, _ := json.Marshal(struct {
		V string
		I Input
	}{Version, in})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Run replays the proposals of one trial in one period.
func Run(in Input) (Report, error) {
	if err := in.Splits.Validate(); err != nil {
		return Report{}, err
	}
	if err := in.Criteria.Validate(); err != nil {
		return Report{}, err
	}
	if in.Trials < 1 {
		return Report{}, errors.New("walkfwd: the trial must be registered, and counted, before it is run")
	}
	if in.Manifest.Kind != Known && in.Manifest.Kind != Reconstructed {
		return Report{}, errors.New("walkfwd: the manifest must say whether the replay is known or reconstructed")
	}
	r := Report{Version: Version, Trial: in.Trial.ID, Period: in.Period, Kind: in.Manifest.Kind, Manifest: ManifestHash(in), Trials: in.Trials,
		SlippageSens: map[string]float64{}, WithholdingSens: map[string]float64{}}
	r.Level = 1 - in.Criteria.Alpha/float64(in.Trials)
	by := index(in.Bars)
	var fund *series
	if in.Manifest.Fund != "" {
		fund = by[in.Manifest.Fund]
	}
	var closed []Outcome
	for _, p := range in.Proposals {
		if p.Trial != in.Trial.ID {
			continue
		}
		if per, ok := in.Splits.Of(p.Decided); !ok || per != in.Period {
			continue // a decision in another period, or in an embargo, belongs to no run of this one
		}
		r.Proposals++
		o := Simulate(in.Config, p, by, in.Dists, in.Delist, fund)
		switch o.State {
		case "no_trade":
			r.NoTrade++
		case "excluded":
			r.Excluded++
		case "unfilled", "thin":
			r.Unfilled++
		case "open":
			r.Filled++
			r.Censored++
		case "no_data":
			r.MissingData++
		case "closed":
			r.Filled++
			r.Matured++
			closed = append(closed, o)
		}
		if o.Ambiguous {
			r.Ambiguous++
		}
		if o.Delisted {
			r.Delisted++
		}
		if o.Missing && o.State == "closed" {
			r.MissingData++
		}
		r.Outcomes = append(r.Outcomes, o)
	}
	decided := r.Proposals - r.NoTrade
	if decided > 0 {
		r.Coverage = float64(decided-r.Excluded-r.MissingData) / float64(decided)
		r.FillRate = float64(r.Filled) / float64(decided-r.Excluded)
	}
	cl := clusters(closed)
	r.ClusterN = len(cl)
	vals := make([]float64, len(closed))
	var excess, excessCash []float64
	rets := make([]float64, len(closed))
	for i, o := range closed {
		vals[i] = o.Return
		rets[i] = o.Return
		r.Costs += o.Fees
		r.Turnover += rounded((o.notional + o.qty*o.exitPx) * 100)
		r.MeanDays += float64(o.Days)
		if o.Fund != nil {
			excess = append(excess, o.Return-*o.Fund)
		} else {
			excess = append(excess, math.NaN())
		}
		excessCash = append(excessCash, o.Return-in.Config.CashRate*float64(o.Days)*7/5/365)
		if o.Return > 0 {
			r.HitRate++
		}
	}
	if n := len(closed); n > 0 {
		r.MeanDays /= float64(n)
		r.HitRate /= float64(n)
		sorted := slices.Clone(rets)
		sort.Float64s(sorted)
		r.Median = sorted[n/2]
		if n%2 == 0 {
			r.Median = (sorted[n/2-1] + sorted[n/2]) / 2
		}
		r.Worst = sorted[0]
		r.Fifth = sorted[int(math.Floor(0.05*float64(n-1)))]
		for _, v := range rets {
			r.Mean += v
		}
		r.Mean /= float64(n)
		seed := binarySeed(r.Manifest)
		alpha := in.Criteria.Alpha / float64(in.Trials)
		var ev []float64
		var ec [][]int
		for _, c := range cl {
			var cc []int
			for _, i := range c {
				if !math.IsNaN(excess[i]) {
					cc = append(cc, len(ev))
					ev = append(ev, excess[i])
				}
			}
			if len(cc) > 0 {
				ec = append(ec, cc)
			}
		}
		if len(ev) > 0 {
			iv := bootstrap(ev, ec, alpha, seed)
			r.Excess = &iv
		}
		ic := bootstrap(excessCash, cl, alpha, seed+1)
		r.ExcessCash = &ic
		for _, s := range in.Config.Slippage {
			t := 0.0
			for _, o := range closed {
				t += o.returnWith(in.Config.Withholding, s)
			}
			r.SlippageSens[fmt.Sprintf("%.4f", s)] = t / float64(n)
		}
		for _, w := range in.Config.WithholdingSens {
			t := 0.0
			for _, o := range closed {
				t += o.returnWith(w, 0)
			}
			r.WithholdingSens[fmt.Sprintf("%.2f", w)] = t / float64(n)
		}
		r.ByQuarter = groups(closed, func(o Outcome) string { return quarter(o.Entry) })
		r.BySector = groups(closed, func(o Outcome) string {
			if o.Sector == "" {
				return "(none)"
			}
			return o.Sector
		})
	}
	verdict(&r, in)
	return r, nil
}

func binarySeed(h string) uint64 {
	b, _ := hex.DecodeString(h[:16])
	var s uint64
	for _, x := range b {
		s = s<<8 | uint64(x)
	}
	return s
}

func quarter(d string) string {
	t, _ := day(d)
	return fmt.Sprintf("%d-Q%d", t.Year(), (int(t.Month())-1)/3+1)
}

func groups(rs []Outcome, key func(Outcome) string) []Group {
	m := map[string]*Group{}
	for _, o := range rs {
		k := key(o)
		g := m[k]
		if g == nil {
			g = &Group{Name: k}
			m[k] = g
		}
		g.N++
		g.Mean += o.Return
	}
	var out []Group
	for _, g := range m {
		g.Mean /= float64(g.N)
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// verdict applies the preregistered criteria. It can only lower the status.
func verdict(r *Report, in Input) {
	c := in.Criteria
	why := func(f string, a ...any) { r.Why = append(r.Why, fmt.Sprintf(f, a...)) }
	diag := func(f string, a ...any) { r.Diagnostics = append(r.Diagnostics, fmt.Sprintf(f, a...)) }
	short := false
	switch {
	case r.Matured < c.MinMatured:
		short = true
		why("%d matured trades, %d needed", r.Matured, c.MinMatured)
	case r.ClusterN < c.MinClusters:
		short = true
		why("%d independent clusters of overlapping trades, %d needed: trades that overlap are not independent observations", r.ClusterN, c.MinClusters)
	}
	if r.Proposals-r.NoTrade > 0 && r.Coverage < c.MinCoverage {
		short = true
		why("coverage %.2f of the decisions had usable history, %.2f needed", r.Coverage, c.MinCoverage)
	}
	if r.Proposals == 0 {
		short = true
		why("no proposal falls in the %s period", in.Period)
	}
	if short {
		r.Status = InsufficientHistory
	} else {
		r.Status = Evaluated
	}
	cap := func(reason string) {
		why("%s", reason)
		if r.Status == Evaluated {
			r.Status = Experimental
		}
	}
	if !short {
		if r.FillRate < c.MinFillRate {
			cap(fmt.Sprintf("fill rate %.2f is below %.2f: the returns come from the trades that filled", r.FillRate, c.MinFillRate))
		}
		if -r.Worst > c.MaxWorstLoss {
			cap(fmt.Sprintf("the worst trade lost %.1f%%, beyond the %.1f%% downside limit", -100*r.Worst, 100*c.MaxWorstLoss))
		}
		switch {
		case r.Excess == nil:
			cap("no benchmark return is available for these trades: an edge needs a comparison")
		case r.Excess.Low <= c.MinExcessLower:
			cap(fmt.Sprintf("the lower bound of the net excess return over the fund, %.4f at the %.2f%% level for %d trials, is not above %.4f", r.Excess.Low, 100*r.Level, r.Trials, c.MinExcessLower))
		}
	}
	if in.Manifest.Kind == Reconstructed {
		cap("a reconstructed replay shows what public history would have said, not what this installation knew then: it cannot be evaluated")
		diag("reconstructed: information that was public earlier but was ingested today was not known here at the time")
	}
	if in.Manifest.Synthetic {
		cap("the data is synthetic: a profit on synthetic data validates nothing")
	}
	if !in.Manifest.PointInTime {
		cap("the universe is not point-in-time: survivorship and coverage bias are not addressed")
		diag("survivorship: the universe was not declared point-in-time")
	}
	if !in.Manifest.IncludesDelisted {
		cap("delisted and failed securities are not declared included: survivorship bias is not addressed")
	}
	if in.Period != Test && in.Period != Reserve && r.Status == Evaluated {
		r.Status = Experimental
		why("only the test or reserve period can give evaluated: %s is for tuning", in.Period)
	}
	if in.Period == Test && in.TestReads > 0 {
		cap(fmt.Sprintf("the test period was read %d time(s) before: anything tuned after reading it is optimised against it", in.TestReads))
	}
	if r.Censored > 0 {
		diag("%d filled trade(s) had not reached their exit in the data (censored) and are left out of the figures", r.Censored)
	}
	if r.Excluded > 0 {
		diag("%d proposal(s) used information known after the decision and were excluded, not repaired", r.Excluded)
	}
	if r.Ambiguous > 0 {
		diag("%d bar(s) could have filled the entry and hit the stop: the stop was taken", r.Ambiguous)
	}
	if r.Delisted > 0 {
		diag("%d position(s) ended in a delisting; without a terminal value that is a total loss", r.Delisted)
	}
	if r.Matured > 0 && r.ClusterN < r.Matured {
		diag("%d trades fall into %d clusters of overlapping holding windows; uncertainty is a bootstrap over the clusters", r.Matured, r.ClusterN)
	}
	if r.Trials > 1 {
		diag("%d trials were registered, so the interval is at the %.2f%% level rather than %.2f%%", r.Trials, 100*r.Level, 100*(1-c.Alpha))
	}
	if r.Status == Evaluated {
		why("every preregistered criterion was met on the %s period; this is evidence about this period, not a forecast", in.Period)
	}
	if strings.TrimSpace(strings.Join(r.Why, "")) == "" {
		why("no criterion applied")
	}
}
