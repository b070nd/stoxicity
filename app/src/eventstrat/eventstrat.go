// Package eventstrat is the separate, time-bounded event sleeve. Its one
// template, earnings-followthrough-paper-v1, is explicitly experimental: it
// turns a completed, source-verified earnings event plus a price and volume
// filter into a paper candidate, a research-only note or a WAIT, and never into
// a real-account suggestion. The settings are arbitrary, testable starting
// values, not a claimed earnings edge.
//
// The package is pure. It reads facts the caller supplies (the event, daily
// bars, account figures) and says, test by test, what holds, what fails and
// what is unknown. Unknown required inputs make the answer research-only; a test
// that is evaluated and fails makes it WAIT; nothing is inferred from the
// event being material or from a count of favourable words.
package eventstrat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/b070nd/stoxicity/app/src/allocate"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/paper"
)

// Template is the only event template and its version.
const Template = "earnings-followthrough-paper-v1"

// Status of an evaluation.
type Status string

// The statuses. There is deliberately no status for a real order.
const (
	Paper    Status = "PAPER"    // may be tried on a paper book; still experimental
	Research Status = "RESEARCH" // required inputs are missing or unknown: study it, do not trade it
	Wait     Status = "WAIT"     // evaluated and failed, or too early, or not worth its cost
)

// Result of one test.
type Result string

// The results.
const (
	Pass    Result = "pass"
	Fail    Result = "fail"
	Unknown Result = "unknown"
	Warn    Result = "warn"
)

// Test is one published, persisted check.
type Test struct {
	Name     string `json:"name"`
	Result   Result `json:"result"`
	Required bool   `json:"required"`
	Detail   string `json:"detail"`
}

// Config is versioned and editable; it starts unreviewed.
type Config struct {
	Version string `json:"version"`
	// Lookback is the number of completed sessions before the event session the
	// follow-through filter compares with.
	Lookback int `json:"lookback"`
	// VolumeMultiple is how many times the lookback's mean volume the event
	// session must trade.
	VolumeMultiple money.Decimal `json:"volume_multiple"`
	// ReviewSessions is the time exit, 5 to 20 sessions after entry.
	ReviewSessions int `json:"review_sessions"`
	// ProfitReviewR, when above zero, is a profit review at that multiple of the
	// planned loss. It is a rule, not an expected return.
	ProfitReviewR money.Decimal `json:"profit_review_r"`
	// EntrySlack is how far above the event session's close the entry limit may be.
	EntrySlack money.Decimal `json:"entry_slack"`
	// CostAllowance is a fraction of the entry price added to the planned loss
	// per share for spread and slippage.
	CostAllowance      money.Decimal `json:"cost_allowance"`
	MinComparableYears int           `json:"min_comparable_years"`
	// GapWarn is the opening gap, as a fraction of the previous close, past which
	// the event session is flagged.
	GapWarn money.Decimal `json:"gap_warn"`
	// MaxRisk is the widest distance from entry to invalidation, as a fraction of entry.
	MaxRisk          money.Decimal `json:"max_risk"`
	MinAvgDailyValue money.Decimal `json:"min_avg_daily_value"`
	MaxParticipation money.Decimal `json:"max_participation"`
	// RiskFraction and SleeveFraction size a paper trade: planned loss of one
	// trade and the whole event sleeve, as fractions of equity. An active policy
	// that sets smaller ones lowers them.
	RiskFraction   money.Decimal `json:"risk_fraction"`
	SleeveFraction money.Decimal `json:"sleeve_fraction"`
	// MaxCostBurden bounds the round-trip fees as a fraction of the notional.
	MaxCostBurden money.Decimal `json:"max_cost_burden"`
	Reviewed      bool          `json:"reviewed"`
}

// DefaultConfig holds the example values of the plan. They are illustrations.
func DefaultConfig() Config {
	d := money.MustDecimal
	return Config{Version: Template, Lookback: 20, VolumeMultiple: d("1.5"), ReviewSessions: 10, ProfitReviewR: d("2"),
		EntrySlack: d("0.01"), CostAllowance: d("0.005"), MinComparableYears: 2, GapWarn: d("0.05"), MaxRisk: d("0.15"),
		MinAvgDailyValue: d("1000000"), MaxParticipation: d("0.02"), RiskFraction: d("0.0025"), SleeveFraction: d("0.05"), MaxCostBurden: d("0.02")}
}

// Validate refuses a configuration that cannot be used.
func (c Config) Validate() error {
	one := money.Int(1)
	frac := func(name string, v money.Decimal, allowZero bool) error {
		if v.Sign() < 0 || (!allowZero && v.Sign() == 0) || v.Cmp(one) > 0 {
			return fmt.Errorf("eventstrat: %s %s must be a fraction above 0 and at most 1", name, v)
		}
		return nil
	}
	switch {
	case c.Version != Template:
		return fmt.Errorf("eventstrat: version %q is not %s", c.Version, Template)
	case c.Lookback < 5 || c.Lookback > 120:
		return fmt.Errorf("eventstrat: lookback %d must be between 5 and 120 sessions", c.Lookback)
	case c.ReviewSessions < 5 || c.ReviewSessions > 20:
		return fmt.Errorf("eventstrat: review_sessions %d must be between 5 and 20", c.ReviewSessions)
	case c.VolumeMultiple.Sign() <= 0:
		return fmt.Errorf("eventstrat: volume_multiple must be positive")
	case c.ProfitReviewR.Sign() < 0:
		return fmt.Errorf("eventstrat: profit_review_r cannot be negative")
	case c.MinComparableYears < 1:
		return fmt.Errorf("eventstrat: min_comparable_years must be at least 1")
	case c.MinAvgDailyValue.Sign() <= 0:
		return fmt.Errorf("eventstrat: min_avg_daily_value must be positive")
	}
	for name, v := range map[string]money.Decimal{"entry_slack": c.EntrySlack, "cost_allowance": c.CostAllowance, "gap_warn": c.GapWarn} {
		if err := frac(name, v, true); err != nil {
			return err
		}
	}
	for name, v := range map[string]money.Decimal{"max_risk": c.MaxRisk, "max_participation": c.MaxParticipation, "risk_fraction": c.RiskFraction, "sleeve_fraction": c.SleeveFraction, "max_cost_burden": c.MaxCostBurden} {
		if err := frac(name, v, false); err != nil {
			return err
		}
	}
	if c.RiskFraction.Cmp(c.SleeveFraction) > 0 {
		return fmt.Errorf("eventstrat: risk_fraction %s is above the whole sleeve %s", c.RiskFraction, c.SleeveFraction)
	}
	return nil
}

// Hash identifies the configuration, review state excluded.
func (c Config) Hash() string {
	c.Reviewed = false
	return hashJSON(c)
}

func hashJSON(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Event is the fact the template needs, with where it came from.
type Event struct {
	ID       string `json:"id"`
	Security string `json:"security"`
	// Issuer the event is about, and the issuer the security belongs to; they
	// must be the same, and a counterparty that cannot be resolved blocks it.
	Issuer                 string `json:"issuer"`
	SecurityIssuer         string `json:"security_issuer"`
	CounterpartyUnresolved bool   `json:"counterparty_unresolved"`
	Kind                   string `json:"kind"`   // earnings
	Day                    string `json:"day"`    // the day the results were released
	Timing                 string `json:"timing"` // pre (before the open), post (after the close), unknown
	Status                 string `json:"status"` // completed, scheduled, rumoured
	Verified               bool   `json:"verified"`
	Source                 string `json:"source"`
	Ref                    string `json:"ref"`
	// Origin identifies the underlying source; the same origin repeated by
	// several outlets is one event.
	Origin string `json:"origin"`
	// CorrectedOn is the day the figures were corrected or restated, if they were.
	CorrectedOn     string `json:"corrected_on,omitempty"`
	ComparableYears int    `json:"comparable_years"`
	Contradiction   bool   `json:"contradiction"`
	ResultsRef      string `json:"results_ref"`
	SplitConsistent bool   `json:"split_consistent"`
	PriceRef        string `json:"price_ref"`
}

// Account is what the sizing is bounded by.
type Account struct {
	Equity     money.Amount `json:"equity"`
	Cash       money.Amount `json:"cash"` // available settled spendable cash
	Fractional bool         `json:"fractional"`
	Fees       allocate.Fees
	Foreign    bool `json:"foreign"` // the security trades in another currency than the base
	// SeenOrigins are the origins of events already used.
	SeenOrigins []string `json:"seen_origins"`
	// PolicySleeve and PolicyRisk, when above zero, lower the configured ones.
	PolicySleeve money.Decimal `json:"policy_sleeve"`
	PolicyRisk   money.Decimal `json:"policy_risk"`
}

// Input is everything an evaluation reads.
type Input struct {
	Config  Config
	Event   Event
	Bars    []paper.Bar
	Account Account
	AsOf    string // the day of the evaluation; only bars before it are finished
}

// Proposal is the outcome.
type Proposal struct {
	Template     string `json:"template"`
	Experimental bool   `json:"experimental"`
	// RealAllowed is always false: this template is never offered for a real account.
	RealAllowed bool     `json:"real_allowed"`
	Status      Status   `json:"status"`
	Reasons     []string `json:"reasons"`
	Tests       []Test   `json:"tests"`
	Event       string   `json:"event"`
	Security    string   `json:"security"`
	Origin      string   `json:"origin"`

	EventSession   string            `json:"event_session,omitempty"`
	EntryNotBefore string            `json:"entry_not_before,omitempty"`
	EntryLimit     *money.Decimal    `json:"entry_limit,omitempty"`
	Invalidation   *money.Decimal    `json:"invalidation,omitempty"`
	Qty            *money.Decimal    `json:"qty,omitempty"`
	Notional       *money.Amount     `json:"notional,omitempty"`
	RoundTripFees  *money.Amount     `json:"round_trip_fees,omitempty"`
	PlannedLoss    *money.Amount     `json:"planned_loss,omitempty"`
	ReviewSessions int               `json:"review_sessions"`
	ProfitReview   *money.Decimal    `json:"profit_review,omitempty"`
	Catalyst       string            `json:"catalyst"`
	Horizon        string            `json:"horizon"`
	InvalidationBy string            `json:"invalidation_rule"`
	ConfigHash     string            `json:"config_hash"`
	ConfigReviewed bool              `json:"config_reviewed"`
	ManifestHash   string            `json:"manifest_hash"`
	Limits         map[string]string `json:"limits,omitempty"`
}

type evaluation struct {
	p     Proposal
	unk   bool
	fail  bool
	tests []Test
}

func (e *evaluation) add(name string, r Result, required bool, format string, a ...any) {
	t := Test{Name: name, Result: r, Required: required, Detail: fmt.Sprintf(format, a...)}
	e.tests = append(e.tests, t)
	if !required {
		return
	}
	switch r {
	case Unknown:
		e.unk = true
		e.p.Reasons = append(e.p.Reasons, name+": "+t.Detail)
	case Fail:
		e.fail = true
		e.p.Reasons = append(e.p.Reasons, name+": "+t.Detail)
	}
}

func day(s string) (time.Time, bool) {
	t, err := time.Parse(time.DateOnly, s)
	return t, err == nil
}

// Evaluate runs every test and sizes the trade when it can.
func Evaluate(in Input) (Proposal, error) {
	if err := in.Config.Validate(); err != nil {
		return Proposal{}, err
	}
	ev := &evaluation{}
	c, e := in.Config, in.Event
	ev.p = Proposal{Template: Template, Experimental: true, RealAllowed: false, Event: e.ID, Security: e.Security, Origin: e.Origin, ReviewSessions: c.ReviewSessions,
		ConfigHash: c.Hash(), ConfigReviewed: c.Reviewed,
		Catalyst:       "a completed earnings release, source-verified and attributed to the issuer",
		Horizon:        fmt.Sprintf("review or exit after %d completed sessions", c.ReviewSessions),
		InvalidationBy: "a close below the event session's low, when a valid lower price exists"}
	ev.p.ManifestHash = hashJSON(struct {
		Event   Event
		Bars    []paper.Bar
		Config  string
		AsOf    string
		Account Account
	}{e, in.Bars, c.Hash(), in.AsOf, in.Account})

	_, okAsOf := day(in.AsOf)
	ev.add("configuration reviewed", map[bool]Result{true: Pass, false: Unknown}[c.Reviewed], true, "the settings are %s", map[bool]string{true: "reviewed", false: "unreviewed illustrations: review them with event config review"}[c.Reviewed])
	ev.add("as-of day", map[bool]Result{true: Pass, false: Fail}[okAsOf], true, "evaluated on %q", in.AsOf)

	// identity
	switch {
	case e.Security == "" || e.Issuer == "" || e.SecurityIssuer == "" || e.CounterpartyUnresolved:
		ev.add("issuer attribution", Unknown, true, "the event's issuer or counterparty is not resolved to this security's issuer")
	case e.Issuer != e.SecurityIssuer:
		ev.add("issuer attribution", Fail, true, "the event is about %s but %s belongs to %s", e.Issuer, e.Security, e.SecurityIssuer)
	default:
		ev.add("issuer attribution", Pass, true, "the event and the security share the issuer %s", e.Issuer)
	}
	ev.add("event kind", map[bool]Result{true: Pass, false: Fail}[e.Kind == "earnings"], true, "this template handles earnings events, not %q", e.Kind)
	switch e.Status {
	case "completed":
		ev.add("event completed", Pass, true, "the results were released")
	case "scheduled", "rumoured":
		ev.add("event completed", Fail, true, "the event is %s: nothing is traded on an event that has not happened", e.Status)
	default:
		ev.add("event completed", Unknown, true, "the event status %q is not known", e.Status)
	}
	switch {
	case !e.Verified || e.Source == "" || e.Ref == "":
		ev.add("source verified", Unknown, true, "the event has no verified source and reference: a headline alone is not enough")
	default:
		ev.add("source verified", Pass, true, "verified against %s (%s)", e.Source, e.Ref)
	}
	seen := false
	for _, o := range in.Account.SeenOrigins {
		seen = seen || (o == e.Origin && o != "")
	}
	switch {
	case e.Origin == "":
		ev.add("origin unique", Unknown, true, "the event does not say which underlying source it comes from")
	case seen:
		ev.add("origin unique", Fail, true, "origin %s was already used for an earlier proposal: the same story repeated is not a second event", e.Origin)
	default:
		ev.add("origin unique", Pass, true, "origin %s is new", e.Origin)
	}
	if e.CorrectedOn != "" {
		ev.add("not corrected", Fail, true, "the figures were corrected on %s: re-verify the event and run again", e.CorrectedOn)
	} else {
		ev.add("not corrected", Pass, true, "no correction is recorded")
	}
	switch {
	case e.Contradiction:
		ev.add("results comparable", Fail, true, "an unresolved contradiction is recorded against the results")
	case e.ComparableYears < c.MinComparableYears || e.ResultsRef == "":
		ev.add("results comparable", Unknown, true, "%d comparable year(s) with reference %q; %d are needed", e.ComparableYears, e.ResultsRef, c.MinComparableYears)
	default:
		ev.add("results comparable", Pass, true, "%d comparable years (%s), no unresolved contradiction", e.ComparableYears, e.ResultsRef)
	}
	ev.add("prices split consistent", map[bool]Result{true: Pass, false: Unknown}[e.SplitConsistent && e.PriceRef != ""], true, "the daily series is %s", map[bool]string{true: "stated split-consistent (" + e.PriceRef + ")", false: "not stated to be split-consistent with a source"}[e.SplitConsistent && e.PriceRef != ""])

	bars, err := cleanBars(in.Bars, e.Security)
	if err != nil {
		return ev.finish(), err
	}
	// chronology: which session is the event session, and is it finished
	idx := -1
	switch e.Timing {
	case "pre":
		idx = indexOf(bars, e.Day, false)
	case "post":
		idx = indexOf(bars, e.Day, true)
	default:
		ev.add("timing known", Unknown, true, "it is not known whether the results came before the open or after the close, so the first reacting session is unknown")
	}
	if e.Timing == "pre" || e.Timing == "post" {
		ev.add("timing known", Pass, true, "released %s market on %s", map[string]string{"pre": "before the", "post": "after the"}[e.Timing], e.Day)
	}
	var sess *paper.Bar
	if idx >= 0 {
		sess = &bars[idx]
		ev.p.EventSession = sess.Day
		finished := okAsOf && sess.Day < in.AsOf
		ev.add("event session finished", map[bool]Result{true: Pass, false: Fail}[finished], true, "the event session %s is %s", sess.Day, map[bool]string{true: "a finished day", false: "not finished before " + in.AsOf}[finished])
	} else if e.Timing == "pre" || e.Timing == "post" {
		ev.add("event session present", Unknown, true, "no bar for the event session after %s: the price series does not cover it", e.Day)
	}
	if sess == nil || idx < c.Lookback {
		if sess != nil {
			ev.add("history", Unknown, true, "only %d session(s) before the event session; %d are needed", idx, c.Lookback)
		}
		return ev.sizeNone(in), nil
	}
	prior := bars[idx-c.Lookback : idx]
	ev.add("history", Pass, true, "%d completed sessions before the event session", c.Lookback)

	// follow-through filter
	var maxClose, sumVol, sumVal money.Decimal
	for i, b := range prior {
		if i == 0 || b.Close.Cmp(maxClose) > 0 {
			maxClose = b.Close
		}
		sumVol, _ = sumVol.Add(money.Int(b.Volume))
		v, _ := b.Close.Mul(money.Int(b.Volume), 4, money.Down)
		sumVal, _ = sumVal.Add(v)
	}
	lhs, _ := money.Int(sess.Volume).Mul(money.Int(int64(c.Lookback)), 4, money.Down)
	rhs, _ := sumVol.Mul(c.VolumeMultiple, 4, money.Up)
	closeOK := sess.Close.Cmp(maxClose) > 0
	volOK := sumVol.Sign() > 0 && lhs.Cmp(rhs) >= 0
	ev.add("close above prior maximum", map[bool]Result{true: Pass, false: Fail}[closeOK], true, "close %s against the highest close %s of the previous %d sessions", sess.Close, maxClose, c.Lookback)
	ev.add("volume surge", map[bool]Result{true: Pass, false: Fail}[volOK], true, "volume %d against %s times the previous %d sessions' mean (%s)", sess.Volume, c.VolumeMultiple, c.Lookback, meanText(sumVol, c.Lookback))

	// gap
	if prev := prior[len(prior)-1]; prev.Close.Sign() > 0 {
		g, _ := sess.Open.Sub(prev.Close)
		gp, _ := g.Div(prev.Close, 4, money.Down)
		abs, _ := gp.Abs()
		if abs.Cmp(c.GapWarn) >= 0 {
			ev.add("opening gap", Warn, false, "the session opened %s from the previous close: the invalidation reference is far from where the market opened", gp)
		} else {
			ev.add("opening gap", Pass, false, "the opening gap is %s", gp)
		}
	}

	// entry and invalidation
	first := ""
	if idx+1 < len(bars) {
		first = bars[idx+1].Day
	}
	if first == "" {
		first, _ = paper.AddWeekdays(sess.Day, 1)
		ev.p.EntryNotBefore = first
	} else {
		ev.p.EntryNotBefore = first
	}
	ex := 2
	limit, _ := sess.Close.Mul(mustOnePlus(c.EntrySlack), uint8(ex), money.Up)
	ev.p.EntryLimit = &limit
	var inv *money.Decimal
	if sess.Low.Sign() > 0 && sess.Low.Cmp(sess.Close) < 0 {
		l := sess.Low
		inv = &l
		ev.p.Invalidation = inv
		dist, _ := limit.Sub(l)
		dp, _ := dist.Div(limit, 4, money.Up)
		ev.add("invalidation valid", Pass, true, "the event session's low %s is below the entry limit %s", l, limit)
		ev.add("risk distance", map[bool]Result{true: Pass, false: Fail}[dp.Cmp(c.MaxRisk) <= 0], true, "the invalidation is %s below the entry limit (at most %s)", dp, c.MaxRisk)
	} else {
		ev.add("invalidation valid", Unknown, true, "there is no valid price below the entry to invalidate against: stop-based sizing is unavailable and nothing is invented")
	}

	// liquidity
	adv, _ := sumVal.Div(money.Int(int64(c.Lookback)), 2, money.Down)
	ev.add("liquidity", map[bool]Result{true: Pass, false: Fail}[adv.Cmp(c.MinAvgDailyValue) >= 0], true, "average daily value %s against the minimum %s (security currency)", adv, c.MinAvgDailyValue)

	return ev.size(in, inv, limit, adv), nil
}

func meanText(sum money.Decimal, n int) string {
	m, err := sum.Div(money.Int(int64(n)), 0, money.Down)
	if err != nil {
		return "?"
	}
	return m.String()
}

func mustOnePlus(d money.Decimal) money.Decimal {
	s, _ := money.Int(1).Add(d)
	return s
}

// cleanBars keeps the security's bars, sorted, and refuses impossible ones.
func cleanBars(in []paper.Bar, sec string) ([]paper.Bar, error) {
	var out []paper.Bar
	seen := map[string]bool{}
	for _, b := range in {
		if b.Security != sec {
			continue
		}
		if err := b.Validate(); err != nil {
			return nil, err
		}
		if seen[b.Day] {
			return nil, fmt.Errorf("eventstrat: %s has two bars for %s", sec, b.Day)
		}
		seen[b.Day] = true
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

// indexOf finds the event session: the bar on the event day (before the open),
// or the first bar after it (after the close).
func indexOf(bars []paper.Bar, d string, after bool) int {
	for i, b := range bars {
		switch {
		case !after && b.Day == d:
			return i
		case after && b.Day > d:
			return i
		}
	}
	return -1
}

func (e *evaluation) finish() Proposal {
	e.p.Tests = e.tests
	switch {
	case e.fail:
		e.p.Status = Wait
	case e.unk:
		e.p.Status = Research
	default:
		e.p.Status = Paper
	}
	if e.p.Reasons == nil {
		e.p.Reasons = []string{}
	}
	return e.p
}

func (e *evaluation) sizeNone(in Input) Proposal {
	// without a session there is nothing to size
	return e.finish()
}

// size turns the passing event into a quantity, bounded by every limit.
func (e *evaluation) size(in Input, inv *money.Decimal, limit, adv money.Decimal) Proposal {
	c, a := in.Config, in.Account
	if e.unk || e.fail {
		return e.finish() // a trade that cannot be proposed is not sized
	}
	if a.Equity.Currency() == "" || a.Equity.Minor() <= 0 {
		e.add("equity known", Unknown, true, "the account equity is not given")
		return e.finish()
	}
	cur := a.Equity.Currency()
	if inCur := currencyOf(in); inCur != "" && inCur != cur {
		e.add("currency", Unknown, true, "the price is in %s and the equity in %s: no conversion rate was given, so nothing is converted by guess", inCur, cur)
		return e.finish()
	}
	if !a.Fees.Known {
		e.add("trading costs known", Unknown, true, "no fee schedule was given: no claim is made that a small trade is affordable")
		return e.finish()
	}
	if inv == nil {
		return e.finish()
	}
	exp, err := cur.Exponent()
	if err != nil {
		e.add("currency", Unknown, true, "%v", err)
		return e.finish()
	}
	// the budget
	risk, sleeve := c.RiskFraction, c.SleeveFraction
	if a.PolicyRisk.Sign() > 0 && a.PolicyRisk.Cmp(risk) < 0 {
		risk = a.PolicyRisk
	}
	if a.PolicySleeve.Sign() > 0 && a.PolicySleeve.Cmp(sleeve) < 0 {
		sleeve = a.PolicySleeve
	}
	eq := money.Int(a.Equity.Minor())
	budgetMinor, _ := eq.Mul(risk, 0, money.Down)
	sleeveMinor, _ := eq.Mul(sleeve, 0, money.Down)
	// planned loss per share, in minor units, rounded against the trade
	dist, _ := limit.Sub(*inv)
	allow, _ := limit.Mul(c.CostAllowance, 6, money.Up)
	per, _ := dist.Add(allow)
	scale := money.MustDecimal("1")
	for i := 0; i < exp; i++ {
		scale, _ = scale.Mul(money.Int(10), 0, money.Exact)
	}
	perMinor, _ := per.Mul(scale, 0, money.Up)
	limitMinor, _ := limit.Mul(scale, 0, money.Up)
	if perMinor.Sign() <= 0 || limitMinor.Sign() <= 0 {
		e.add("affordable size", Fail, true, "the planned loss per share is not positive")
		return e.finish()
	}
	step := money.Int(1)
	bound := func(name string, minorBudget money.Decimal, per money.Decimal) money.Decimal {
		q, _ := minorBudget.Div(per, 6, money.Down)
		if !a.Fractional {
			q, _ = q.Rescale(0, money.Down)
		}
		_ = name
		return q
	}
	qRisk := bound("risk", budgetMinor, perMinor)
	qSleeve := bound("sleeve", sleeveMinor, limitMinor)
	cashMinor := money.Int(a.Cash.Minor())
	qCash := bound("cash", cashMinor, limitMinor)
	partMinor, _ := adv.Mul(c.MaxParticipation, 2, money.Down)
	partMinor, _ = partMinor.Mul(scale, 0, money.Down)
	qLiq := bound("liquidity", partMinor, limitMinor)
	q := qRisk
	which := "the planned-loss budget"
	for _, x := range []struct {
		n string
		v money.Decimal
	}{{"the event sleeve", qSleeve}, {"available cash", qCash}, {"the share of daily volume", qLiq}} {
		if x.v.Cmp(q) < 0 {
			q, which = x.v, x.n
		}
	}
	e.p.Limits = map[string]string{"planned-loss budget": qRisk.String(), "event sleeve": qSleeve.String(), "available cash": qCash.String(), "share of daily value": qLiq.String()}
	if q.Cmp(step) < 0 {
		e.add("affordable size", Fail, true, "the smallest permitted order does not fit: %s allows %s share(s) at an entry of %s", which, q, limit)
		return e.finish()
	}
	e.add("affordable size", Pass, true, "%s share(s), bounded by %s", q, which)
	notionalD, _ := limit.Mul(q, uint8(exp), money.Up)
	notional, err := money.NewAmount(notionalD, cur, money.Up)
	if err != nil {
		e.add("affordable size", Unknown, true, "%v", err)
		return e.finish()
	}
	fee := a.Fees.Of(notional.Minor(), a.Foreign)
	round := money.Amount{}
	if round, err = money.FromMinor(fee*2, cur); err != nil {
		e.add("trading costs", Unknown, true, "%v", err)
		return e.finish()
	}
	burden, _ := money.Int(fee*2).Div(money.Int(notional.Minor()), 6, money.Up)
	e.add("cost feasible", map[bool]Result{true: Pass, false: Fail}[burden.Cmp(c.MaxCostBurden) <= 0], true, "round-trip fees %s are %s of the %s order (at most %s)", round, burden, notional, c.MaxCostBurden)
	lossMinor, _ := perMinor.Mul(q, 0, money.Up)
	if n, perr := strconv.ParseInt(lossMinor.String(), 10, 64); perr == nil {
		if loss, lerr := money.FromMinor(n, cur); lerr == nil {
			e.p.PlannedLoss = &loss
		}
	}
	e.p.Qty, e.p.Notional, e.p.RoundTripFees = &q, &notional, &round
	if c.ProfitReviewR.Sign() > 0 {
		gain, _ := dist.Mul(c.ProfitReviewR, 6, money.Up)
		pr, _ := limit.Add(gain)
		pr, _ = pr.Rescale(uint8(exp), money.Up)
		e.p.ProfitReview = &pr
	}
	return e.finish()
}

func currencyOf(in Input) money.Currency {
	for _, b := range in.Bars {
		if b.Security == in.Event.Security && b.Currency != "" {
			return b.Currency
		}
	}
	return ""
}

// ExitKind is what a held event position calls for.
type ExitKind string

// The exit kinds. A time exit is a distinct proposal in the event sleeve, never
// a forced sale, and never folded into the core thesis.
const (
	Hold         ExitKind = "HOLD"
	TimeExit     ExitKind = "TIME_EXIT"
	Invalidated  ExitKind = "INVALIDATED"
	ProfitReview ExitKind = "PROFIT_REVIEW"
)

// Holding is an open event position as it was planned.
type Holding struct {
	Security       string         `json:"security"`
	EntryDay       string         `json:"entry_day"`
	EntryPrice     money.Decimal  `json:"entry_price"`
	Invalidation   money.Decimal  `json:"invalidation"`
	ReviewSessions int            `json:"review_sessions"`
	ProfitReview   *money.Decimal `json:"profit_review,omitempty"`
}

// Exit is the review of a holding.
type Exit struct {
	Kind         ExitKind `json:"kind"`
	Reason       string   `json:"reason"`
	SessionsHeld int      `json:"sessions_held"`
	LastBar      string   `json:"last_bar,omitempty"`
	Notes        []string `json:"notes,omitempty"`
}

// Review says what an event holding calls for on a day. The clock of the time
// exit is the weekdays since entry (exchange holidays are not known, so it can
// come a session early, never late), so a gap in the price file cannot extend a
// holding. Invalidation and the profit review need the bars.
func Review(h Holding, bars []paper.Bar, asOf string) (Exit, error) {
	if h.ReviewSessions < 5 || h.ReviewSessions > 20 {
		return Exit{}, fmt.Errorf("eventstrat: review_sessions %d must be between 5 and 20", h.ReviewSessions)
	}
	end, ok := day(asOf)
	start, ok2 := day(h.EntryDay)
	if !ok || !ok2 || !start.Before(end) {
		return Exit{}, fmt.Errorf("eventstrat: entry day %q must be a day before %q", h.EntryDay, asOf)
	}
	held := 0
	for d := start.AddDate(0, 0, 1); d.Before(end); d = d.AddDate(0, 0, 1) {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			held++
		}
	}
	x := Exit{Kind: Hold, SessionsHeld: held}
	cleaned, err := cleanBars(bars, h.Security)
	if err != nil {
		return x, err
	}
	var last *paper.Bar
	for i := range cleaned {
		b := cleaned[i]
		if b.Day <= h.EntryDay || b.Day >= asOf {
			continue
		}
		last = &cleaned[i]
		if b.Close.Cmp(h.Invalidation) < 0 {
			x.Kind = Invalidated
			x.Reason = fmt.Sprintf("%s closed at %s, below the invalidation %s", b.Day, b.Close, h.Invalidation)
			if b.Open.Cmp(h.Invalidation) < 0 {
				x.Notes = append(x.Notes, fmt.Sprintf("it gapped: the session opened at %s, already through the invalidation, so an exit may not get the invalidation price", b.Open))
			}
			x.LastBar = b.Day
			return x, nil
		}
	}
	if last != nil {
		x.LastBar = last.Day
	} else {
		x.Notes = append(x.Notes, "no finished bar since entry: invalidation could not be checked")
	}
	if h.ProfitReview != nil && last != nil && last.Close.Cmp(*h.ProfitReview) >= 0 {
		x.Kind = ProfitReview
		x.Reason = fmt.Sprintf("%s closed at %s, at or above the profit-review level %s (a rule, not an expected return)", last.Day, last.Close, *h.ProfitReview)
		return x, nil
	}
	if held >= h.ReviewSessions {
		x.Kind = TimeExit
		x.Reason = fmt.Sprintf("%d sessions have passed since entry (the time exit is %d): propose the exit in the event sleeve; the position is not moved into the core", held, h.ReviewSessions)
		x.Notes = append(x.Notes, "sessions are counted by weekdays, so a gap in the price file cannot extend the holding")
		return x, nil
	}
	x.Reason = fmt.Sprintf("%d of %d sessions held; no invalidation", held, h.ReviewSessions)
	return x, nil
}
