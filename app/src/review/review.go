// Package review decides what to do about what is already owned: hold, reduce,
// sell, or say that it cannot tell. It applies the ordered reasons of section 5.7
// of the production plan and returns every finding, not only the one that wins,
// so a sale always names its rule and the conditions that argue against it.
//
// Conventions, stated once:
//
//   - The reasons are ordered, and the first that dictates a sale decides:
//     1 your instruction (a withdrawal or liquidity need), 2 a thesis you recorded
//     as broken, 3 a concentration breach or an approaching goal, 4 valuation
//     above the configured scenario, 5 an event position past its holding limit or
//     invalidated, 6 a scheduled rebalance (not evaluated: no target weights exist).
//   - A quantity never exceeds the free shares, those not reserved by open orders.
//     A concentration reduction is the smallest that cures the breach, bounded by
//     what is free; the part that cannot be cured is reported.
//   - Missing data never becomes HOLD and never becomes a sale: with a gap in what
//     a data-derived rule needs, the review is blocked (ABSTAIN) and says what is missing.
//     Your own recorded decisions (an instruction, a broken thesis) still apply, and
//     a sale then shows its proceeds as unavailable rather than invented.
//   - A review date is not a sale date. A late review is shown as overdue; it is
//     never presented as a fresh HOLD. A dividend cut or a thesis under review makes
//     a review required, not a sale.
//   - Nothing here knows your costs or taxes beyond what you supplied; a sale
//     proposal says so instead of weighing alternatives net of them.
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// ID names the starter review rules.
const ID = "holdings-review-v1"

// Config holds the thresholds. They are unvalidated starting points, inactive
// as a reviewed set until a person marks them reviewed.
type Config struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	Reviewed bool   `json:"reviewed"`
	// ReviewDays is how often a core position is reviewed.
	ReviewDays int `json:"review_days"`
	// GoalWindowMonths is how close the goal date must be to start de-risking.
	GoalWindowMonths int `json:"goal_window_months"`
	// DeRiskFraction is the share of a position proposed for reduction inside the
	// goal window. Zero means only a review is required.
	DeRiskFraction money.Decimal `json:"derisk_fraction"`
	// ValuationRule is off, above_base or above_bull.
	ValuationRule string `json:"valuation_rule"`
	// ValuationReduceFraction is the share proposed for reduction when the rule fires.
	ValuationReduceFraction money.Decimal `json:"valuation_reduce_fraction"`
	// MaxEventSessions is the holding limit of an event position, 5 to 20 sessions.
	MaxEventSessions int `json:"max_event_sessions"`
}

// DefaultConfig is the starter configuration, not reviewed.
func DefaultConfig() Config {
	d := money.MustDecimal
	return Config{ID: ID, Version: 1, ReviewDays: 91, GoalWindowMonths: 12, DeRiskFraction: d("0"),
		ValuationRule: "above_bull", ValuationReduceFraction: d("0.50"), MaxEventSessions: 10}
}

// Validate says why the configuration cannot be used.
func (c Config) Validate() error {
	switch {
	case c.ID != ID:
		return fmt.Errorf("review: unknown rule set %q (%s)", c.ID, ID)
	case c.Version < 1:
		return errors.New("review: the version must be 1 or more")
	case c.ReviewDays < 7 || c.ReviewDays > 400:
		return errors.New("review: the review interval must be 7 to 400 days")
	case c.GoalWindowMonths < 1 || c.GoalWindowMonths > 120:
		return errors.New("review: the goal window must be 1 to 120 months")
	case c.DeRiskFraction.Sign() < 0 || c.DeRiskFraction.Cmp(money.MustDecimal("1")) > 0:
		return errors.New("review: the de-risk fraction must be 0 to 1")
	case c.ValuationRule != "off" && c.ValuationRule != "above_base" && c.ValuationRule != "above_bull":
		return errors.New("review: the valuation rule must be off, above_base or above_bull")
	case c.ValuationReduceFraction.Sign() <= 0 || c.ValuationReduceFraction.Cmp(money.MustDecimal("1")) > 0:
		return errors.New("review: the valuation reduce fraction must be above 0 and at most 1")
	case c.MaxEventSessions < 5 || c.MaxEventSessions > 20:
		return errors.New("review: the event holding limit must be 5 to 20 sessions")
	}
	return nil
}

// Hash identifies the configuration.
func (c Config) Hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Position is one holding, in the book.
type Position struct {
	Security string
	Name     string
	Sleeve   string // core or event
	Qty      money.Decimal
	// Free is the part not reserved by open sell orders.
	Free       money.Decimal
	Fractional bool
	OpenedDay  string
	// Value is the position's value in the base currency's minor units, when a current price exists.
	Value      int64
	ValueKnown bool
}

// Instruction is what you told the program to do with a holding.
type Instruction struct {
	Action string        // reduce or sell
	Qty    money.Decimal // zero with All
	All    bool
	Why    string // withdrawal, liquidity or instruction
	By     string
	Day    string
}

// Thesis is your recorded view of why you own it.
type Thesis struct {
	Status            string // intact, under_review or broken; empty when never recorded
	DependsOnDividend bool
	Note              string
	Day               string
}

// Breach is one limit the holding's exposure is over.
type Breach struct {
	Dimension string // issuer, fund, ...
	Name      string
	Share     money.Decimal
	Limit     money.Decimal
}

// ValuationFacts are the scenario values and the price they are compared with.
type ValuationFacts struct {
	Currency money.Currency
	Bear     money.Decimal
	Base     money.Decimal
	Bull     money.Decimal
	Price    money.Decimal
	PriceCur money.Currency
	PriceDay string
}

// Facts are everything besides the position that the rules read.
type Facts struct {
	AsOf    string
	Equity  int64
	Missing []string
	// Caveats are limits on what the findings mean that do not block them.
	Caveats []string
	// PriceFresh says the position's value rests on a current price.
	PriceFresh        bool
	Instruction       *Instruction
	Thesis            Thesis
	DividendCuts      int
	DividendCancelled int
	Breaches          []Breach
	Valuation         *ValuationFacts
	GoalDate          string // YYYY-MM-DD, empty when unknown
	LastReview        string
	EventInvalidated  bool
	EventDetail       string
}

// Finding is one rule's reading of the holding.
type Finding struct {
	Rule   int    `json:"rule"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
	// Action is "SELL" or "REDUCE" when the rule dictates a sale, otherwise empty.
	Action string `json:"action,omitempty"`
	Qty    string `json:"qty,omitempty"`
}

// Review is the outcome for one holding.
type Review struct {
	Security string `json:"security"`
	AsOf     string `json:"as_of"`
	Action   string `json:"action"` // HOLD, REDUCE, SELL or ABSTAIN
	Qty      string `json:"qty,omitempty"`
	// Status is ok, review_due, overdue, review_required or blocked.
	Status    string    `json:"status"`
	Rule      int       `json:"rule,omitempty"`
	Findings  []Finding `json:"findings"`
	Conflicts []string  `json:"conflicts,omitempty"`
	Proceeds  string    `json:"proceeds"`
	// ProceedsMinor is the estimate behind Proceeds, in the base currency's minor
	// units; meaningful only when ProceedsKnown.
	ProceedsMinor int64    `json:"proceeds_minor,omitempty"`
	ProceedsKnown bool     `json:"proceeds_known"`
	Missing       []string `json:"missing,omitempty"`
	NotEvaluated  []string `json:"not_evaluated,omitempty"`
	LastReview    string   `json:"last_review,omitempty"`
	NextReview    string   `json:"next_review,omitempty"`
	DaysOverdue   int      `json:"days_overdue,omitempty"`
	Capped        bool     `json:"capped,omitempty"`
}

// Label is the one-line state: a late review is never a fresh HOLD.
func (r Review) Label() string {
	s := r.Action
	if r.Qty != "" && (r.Action == "REDUCE" || r.Action == "SELL") {
		s += " " + r.Qty
	}
	switch r.Status {
	case "overdue":
		s += fmt.Sprintf(" (review overdue by %d days)", r.DaysOverdue)
	case "review_required":
		s += " (review required)"
	case "blocked":
		s += " (blocked)"
	case "review_due":
		s += " (review due)"
	}
	return s
}

func rat(d money.Decimal) *big.Rat {
	r, _ := new(big.Rat).SetString(d.String())
	return r
}

// unit rounds a quantity up (cure) or down to a whole share or to eight digits.
func unit(r *big.Rat, fractional, up bool) money.Decimal {
	scale := int64(0)
	if fractional {
		scale = 8
	}
	f := new(big.Rat).Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(scale), nil)))
	q, rem := new(big.Int).QuoRem(f.Num(), f.Denom(), new(big.Int))
	if up && rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	out := new(big.Rat).SetFrac(q, new(big.Int).Exp(big.NewInt(10), big.NewInt(scale), nil))
	d, err := money.ParseDecimal(out.FloatString(int(scale)))
	if err != nil {
		return money.Decimal{}
	}
	return d
}

func minDec(a, b money.Decimal) money.Decimal {
	if a.Cmp(b) < 0 {
		return a
	}
	return b
}

func day(s string) (time.Time, bool) {
	t, err := time.Parse(time.DateOnly, s)
	return t, err == nil
}

func weekdaysBetween(from, to time.Time) int {
	n := 0
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		if wd := d.AddDate(0, 0, 1).Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n
}

// Evaluate reviews one holding.
func Evaluate(cfg Config, p Position, f Facts) Review {
	r := Review{Security: p.Security, AsOf: f.AsOf, Missing: f.Missing, LastReview: f.LastReview,
		NotEvaluated: append([]string{"scheduled rebalance (rule 6): no target weights are defined, so no rebalance is proposed"}, f.Caveats...)}
	asOf, okAsOf := day(f.AsOf)
	free := p.Free
	var fs []Finding
	add := func(x Finding) { fs = append(fs, x) }

	// 1: your instruction
	if in := f.Instruction; in != nil {
		want := in.Qty
		if in.All {
			want = p.Free
		}
		qty := minDec(want, free)
		act := "REDUCE"
		if qty.Cmp(p.Qty) >= 0 {
			act = "SELL"
		}
		code := in.Why
		d := fmt.Sprintf("you told the program on %s (%s) to %s", in.Day, orBy(in.By), in.Action)
		if in.All {
			d += " all of it"
		} else {
			d += " " + in.Qty.String()
		}
		if !in.All && want.Cmp(free) > 0 {
			d += fmt.Sprintf("; only %s are free to sell", free)
			r.Capped = true
		}
		add(Finding{Rule: 1, Code: code, Detail: d, Action: act, Qty: qty.String()})
	}

	// 2: the thesis, and dividends as evidence about it
	switch f.Thesis.Status {
	case "broken":
		add(Finding{Rule: 2, Code: "thesis_broken", Detail: "you recorded the thesis as broken on " + f.Thesis.Day + ": " + f.Thesis.Note, Action: "SELL", Qty: free.String()})
	case "under_review":
		add(Finding{Rule: 2, Code: "thesis_under_review", Detail: "you recorded the thesis as under review on " + f.Thesis.Day + ": " + f.Thesis.Note})
	case "":
		add(Finding{Rule: 2, Code: "thesis_unrecorded", Detail: "no thesis status is recorded: a review is required before anything is said to be intact"})
	}
	if f.DividendCancelled > 0 || f.DividendCuts > 0 {
		what := fmt.Sprintf("%d cut(s) and %d cancellation(s) of the ordinary distribution in the last 24 months", f.DividendCuts, f.DividendCancelled)
		d := what + ": a thesis review, not a sale"
		if f.Thesis.DependsOnDividend {
			d = what + ", and your thesis counted on the dividend: inconsistent with the recorded thesis, so a thesis review is required (it is not a sale until you record the thesis as broken)"
		}
		add(Finding{Rule: 2, Code: "dividend_cut", Detail: d})
	}

	// 3: concentration and the approaching goal
	for _, b := range f.Breaches {
		if !p.ValueKnown || p.Value <= 0 || f.Equity <= 0 || p.Qty.Sign() <= 0 {
			continue
		}
		excess := new(big.Rat).Sub(rat(b.Share), rat(b.Limit))
		excess.Mul(excess, new(big.Rat).SetInt64(f.Equity))
		perUnit := new(big.Rat).SetFrac64(p.Value, 1)
		perUnit.Quo(perUnit, rat(p.Qty))
		need := unit(new(big.Rat).Quo(excess, perUnit), p.Fractional, true)
		qty := minDec(need, free)
		d := fmt.Sprintf("%s %s is %s of equity, over the limit %s: the smallest reduction that cures it is %s", b.Dimension, b.Name, pct(b.Share), pct(b.Limit), need)
		act := "REDUCE"
		if qty.Cmp(p.Qty) >= 0 {
			act = "SELL"
		}
		if need.Cmp(free) > 0 {
			d += fmt.Sprintf("; only %s are free, so %s of the breach cannot be cured now", free, minusDec(need, free))
			r.Capped = true
		}
		add(Finding{Rule: 3, Code: "concentration", Detail: d, Action: act, Qty: qty.String()})
	}
	if g, ok := day(f.GoalDate); ok && okAsOf {
		months := int(g.Sub(asOf).Hours() / 24 / 30.4375)
		if months <= cfg.GoalWindowMonths {
			d := fmt.Sprintf("the goal date %s is about %d month(s) away, inside the %d-month window; where proceeds would go (cash or other non-equity choices) is yours to decide", f.GoalDate, months, cfg.GoalWindowMonths)
			if cfg.DeRiskFraction.Sign() > 0 {
				qty := minDec(unit(new(big.Rat).Mul(rat(free), rat(cfg.DeRiskFraction)), p.Fractional, true), free)
				if qty.Sign() > 0 {
					add(Finding{Rule: 3, Code: "approaching_goal", Detail: d + fmt.Sprintf("; the configured de-risk fraction %s of the free shares is %s", cfg.DeRiskFraction, qty), Action: "REDUCE", Qty: qty.String()})
				}
			} else {
				add(Finding{Rule: 3, Code: "approaching_goal", Detail: d + "; no de-risk fraction is configured, so a review is required, not a sale"})
			}
		}
	}

	// 4: valuation
	if v := f.Valuation; v != nil && cfg.ValuationRule != "off" {
		switch {
		case v.PriceCur != v.Currency:
			r.NotEvaluated = append(r.NotEvaluated, fmt.Sprintf("valuation (rule 4): the price is in %s and the scenarios in %s, and no rate is applied here", v.PriceCur, v.Currency))
		case !f.PriceFresh:
			r.NotEvaluated = append(r.NotEvaluated, "valuation (rule 4): there is no current price")
		default:
			limit, name := v.Bull, "bull"
			if cfg.ValuationRule == "above_base" {
				limit, name = v.Base, "base"
			}
			if v.Price.Cmp(limit) > 0 {
				qty := minDec(unit(new(big.Rat).Mul(rat(free), rat(cfg.ValuationReduceFraction)), p.Fractional, true), free)
				if qty.Sign() > 0 {
					add(Finding{Rule: 4, Code: "valuation_above_" + name, Detail: fmt.Sprintf("the price %s %s (%s) is above the %s-case value %s under your assumptions; alternatives net of costs and taxes are not weighed here (no costs or tax treatment were supplied)", v.Price, v.PriceCur, v.PriceDay, name, limit), Action: "REDUCE", Qty: qty.String()})
				}
			}
		}
	} else if f.Valuation == nil && cfg.ValuationRule != "off" {
		r.NotEvaluated = append(r.NotEvaluated, "valuation (rule 4): no valuation assumptions exist for this holding")
	}

	// 5: event position
	if p.Sleeve == "event" {
		if f.EventInvalidated {
			add(Finding{Rule: 5, Code: "event_invalidated", Detail: orStr(f.EventDetail, "the invalidation level was reached"), Action: "SELL", Qty: free.String()})
		}
		if o, ok := day(p.OpenedDay); ok && okAsOf {
			if n := weekdaysBetween(o, asOf); n >= cfg.MaxEventSessions {
				add(Finding{Rule: 5, Code: "event_expiry", Detail: fmt.Sprintf("held about %d weekday sessions since %s, at or past the limit of %d (public holidays are not subtracted)", n, p.OpenedDay, cfg.MaxEventSessions), Action: "SELL", Qty: free.String()})
			}
		}
	}

	if !cfg.Reviewed {
		// thresholds nobody has read are advisory: they require a review, not a sale
		for i := range fs {
			if fs[i].Action != "" && (fs[i].Code == "approaching_goal" || strings.HasPrefix(fs[i].Code, "valuation_") || fs[i].Code == "event_expiry") {
				fs[i].Action, fs[i].Qty = "", ""
				fs[i].Detail += " (the review rule set has not been reviewed by you, so this requires a review and proposes no sale)"
			}
		}
	}
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].Rule < fs[j].Rule })
	r.Findings = fs

	// the dictating finding: with a gap in the data, the rules that read prices
	// and values cannot dictate; your own recorded decisions still can
	var dict *Finding
	for i := range fs {
		if fs[i].Action == "" || (len(f.Missing) > 0 && dataDerived(fs[i].Code)) {
			continue
		}
		dict = &fs[i]
		break
	}
	reviewRequired := false
	for _, x := range fs {
		if x.Action == "" {
			reviewRequired = true
		}
	}
	switch {
	case dict != nil:
		r.Action, r.Qty, r.Rule = dict.Action, dict.Qty, dict.Rule
		if free.Sign() <= 0 || dict.Qty == "" || dict.Qty == "0" {
			r.Action, r.Qty = "HOLD", ""
			r.Status = "blocked"
			r.Findings = append(r.Findings, Finding{Rule: dict.Rule, Code: "no_free_shares", Detail: fmt.Sprintf("rule %d calls for a sale but no shares are free to sell: they are reserved by open orders", dict.Rule)})
		}
	case len(f.Missing) > 0:
		r.Action, r.Status = "ABSTAIN", "blocked"
	default:
		r.Action = "HOLD"
	}
	if r.Status == "" {
		r.Status = "ok"
	}

	// conflicting conditions: the other findings, and a recorded intact thesis
	if dict != nil && r.Rule != 0 {
		for _, x := range fs {
			if x.Code != dict.Code {
				r.Conflicts = append(r.Conflicts, fmt.Sprintf("rule %d (%s): %s", x.Rule, x.Code, x.Detail))
			}
		}
		if f.Thesis.Status == "intact" && r.Rule != 2 {
			r.Conflicts = append(r.Conflicts, "you recorded the thesis as intact on "+f.Thesis.Day+": this sale is not a verdict on the business")
		}
	}

	// review timing: a review date never forces a sale, and a late one is not a fresh HOLD
	if r.Status != "blocked" {
		base := f.LastReview
		if base == "" {
			base = p.OpenedDay
		}
		if b, ok := day(base); ok && okAsOf {
			next := b.AddDate(0, 0, cfg.ReviewDays)
			r.NextReview = next.Format(time.DateOnly)
			switch {
			case asOf.After(next):
				r.Status = "overdue"
				r.DaysOverdue = int(asOf.Sub(next).Hours() / 24)
			case asOf.Equal(next):
				r.Status = "review_due"
			}
		} else {
			r.Status = "review_required"
			r.Findings = append(r.Findings, Finding{Rule: 0, Code: "never_reviewed", Detail: "there is no recorded review and no purchase date to count from"})
		}
		if r.Status == "ok" && reviewRequired {
			r.Status = "review_required"
		}
		if r.Status == "review_due" && reviewRequired {
			r.Status = "review_required"
		}
	}

	// proceeds are an estimate only from a current price; otherwise they are not invented
	switch {
	case r.Qty == "" || (r.Action != "SELL" && r.Action != "REDUCE"):
		r.Proceeds = "not applicable"
	case !p.ValueKnown || !f.PriceFresh || p.Qty.Sign() <= 0:
		r.Proceeds = "unavailable: there is no current price, so no proceeds are estimated"
	default:
		q, _ := new(big.Rat).SetString(r.Qty)
		est := new(big.Rat).Mul(new(big.Rat).SetFrac64(p.Value, 1), q)
		est.Quo(est, rat(p.Qty))
		r.ProceedsMinor, r.ProceedsKnown = roundMinor(est), true
		r.Proceeds = fmt.Sprintf("about %d minor units of the base currency at the current price, before costs and taxes", r.ProceedsMinor)
	}
	return r
}

// roundMinor rounds half to even to a whole minor unit.
func roundMinor(r *big.Rat) int64 {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	twice := new(big.Int).Mul(rem, big.NewInt(2))
	switch c := twice.CmpAbs(r.Denom()); {
	case c > 0 || (c == 0 && q.Bit(0) == 1):
		if r.Sign() >= 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	return q.Int64()
}

func dataDerived(code string) bool {
	return code == "concentration" || strings.HasPrefix(code, "valuation_")
}

func orBy(s string) string {
	if s == "" {
		return "unnamed"
	}
	return s
}

func orStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func pct(d money.Decimal) string {
	r := new(big.Rat).Mul(rat(d), big.NewRat(100, 1))
	return strings.TrimRight(strings.TrimRight(r.FloatString(2), "0"), ".") + "%"
}

func minusDec(a, b money.Decimal) money.Decimal {
	d, err := a.Sub(b)
	if err != nil {
		return a
	}
	return d
}
