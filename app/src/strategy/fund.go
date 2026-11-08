package strategy

import (
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// FundFacts are the documented facts about a fund or ETF. A company's earnings
// and payout tests do not apply to it; it is judged on cost, structure,
// domicile, diversification and its distribution policy.
type FundFacts struct {
	Security string `json:"security"`
	Name     string `json:"name"`
	// Currency is the fund's trading currency; Exposure lists the currencies of its holdings, if known.
	Currency money.Currency   `json:"currency"`
	Exposure []money.Currency `json:"currency_exposure,omitempty"`
	// OngoingCost is the documented yearly cost as a fraction (0.002 = 0.20%).
	OngoingCost *money.Decimal `json:"ongoing_cost,omitempty"`
	// TrackingDifference is the documented yearly deviation from the index, as a fraction.
	TrackingDifference *money.Decimal `json:"tracking_difference,omitempty"`
	Domicile           string         `json:"domicile,omitempty"`
	Structure          string         `json:"structure,omitempty"`       // physical or synthetic
	Distribution       string         `json:"distribution,omitempty"`    // accumulating or distributing
	Holdings           int            `json:"holdings,omitempty"`        // count of constituents
	HoldingsDate       string         `json:"holdings_date,omitempty"`   // date of the look-through
	Tilted             bool           `json:"dividend_tilted,omitempty"` // a dividend or factor tilt, not broad
	// Portfolio names the underlying fund portfolio. The share classes and
	// listings of one fund carry the same name, so the single-fund cap counts them
	// together; empty means this security is treated as a fund of its own.
	Portfolio string `json:"portfolio,omitempty"`
	// LookThrough is the weight of each company issuer in the fund as of
	// HoldingsDate, as a fraction of the fund's value. Only what is listed is
	// known: the rest is unknown, never zero.
	LookThrough map[string]money.Decimal `json:"look_through,omitempty"`
	Source      string                   `json:"source"`
	Ref         string                   `json:"ref"`
}

// FundConfig are the starter limits for diversified-core-fund-v1, unvalidated and editable.
type FundConfig struct {
	MaxCost        money.Decimal `json:"max_cost"`
	MaxTracking    money.Decimal `json:"max_tracking_difference"`
	MinHoldings    int           `json:"min_holdings"`
	MaxHoldingsAge int           `json:"max_holdings_age_days"`
	Domiciles      []string      `json:"allowed_domiciles"`
	Distribution   string        `json:"distribution"` // accumulating, distributing or any
}

// DefaultFundConfig is the starter fund configuration.
func DefaultFundConfig() FundConfig {
	return FundConfig{MaxCost: money.MustDecimal("0.0030"), MaxTracking: money.MustDecimal("0.0050"), MinHoldings: 500, MaxHoldingsAge: 120,
		Domiciles: []string{"IE", "LU"}, Distribution: "any"}
}

// FundCheck is one check of a fund; there is no score.
type FundCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Result Result `json:"result"`
	Detail string `json:"detail"`
}

// FundAssessment is the fund template's answer: checks, not a score, so a fund
// is never compared with a company by number.
type FundAssessment struct {
	Strategy string      `json:"strategy"`
	Security string      `json:"security"`
	AsOf     string      `json:"as_of"`
	Checks   []FundCheck `json:"checks"`
	Eligible bool        `json:"eligible"`
	// Broad is false for a tilted fund: it is compared with a broad core alternative, not proposed as the core.
	Broad   bool     `json:"broad"`
	Reasons []string `json:"reasons"`
}

// AssessFund runs diversified-core-fund-v1.
func AssessFund(f FundFacts, cfg FundConfig, asOf string) FundAssessment {
	a := FundAssessment{Strategy: FundID, Security: f.Security, AsOf: asOf, Broad: !f.Tilted}
	add := func(id, label string, r Result, detail string) {
		a.Checks = append(a.Checks, FundCheck{id, label, r, detail})
		if r != Pass {
			a.Reasons = append(a.Reasons, fmt.Sprintf("%s: %s (%s)", r, label, detail))
		}
	}
	switch {
	case f.Source == "" || f.Ref == "":
		add("source", "the facts have a source and a reference", Fail, "an undocumented fund fact is not used")
	default:
		add("source", "the facts have a source and a reference", Pass, f.Source+", "+f.Ref)
	}
	if f.OngoingCost == nil {
		add("cost", fmt.Sprintf("documented ongoing cost at most %s a year", pct(cfg.MaxCost)), Unknown, "no documented cost")
	} else if f.OngoingCost.Cmp(cfg.MaxCost) <= 0 {
		add("cost", fmt.Sprintf("documented ongoing cost at most %s a year", pct(cfg.MaxCost)), Pass, pct(*f.OngoingCost))
	} else {
		add("cost", fmt.Sprintf("documented ongoing cost at most %s a year", pct(cfg.MaxCost)), Fail, pct(*f.OngoingCost))
	}
	if f.TrackingDifference == nil {
		add("tracking", fmt.Sprintf("tracking difference within %s", pct(cfg.MaxTracking)), Unknown, "not documented")
	} else {
		d, _ := f.TrackingDifference.Abs()
		r := Pass
		if d.Cmp(cfg.MaxTracking) > 0 {
			r = Fail
		}
		add("tracking", fmt.Sprintf("tracking difference within %s", pct(cfg.MaxTracking)), r, pct(*f.TrackingDifference))
	}
	switch {
	case f.Domicile == "":
		add("domicile", "domicile is one of "+strings.Join(cfg.Domiciles, ", "), Unknown, "not documented")
	case len(cfg.Domiciles) == 0 || contains(cfg.Domiciles, f.Domicile):
		add("domicile", "domicile is one of "+strings.Join(cfg.Domiciles, ", "), Pass, f.Domicile)
	default:
		add("domicile", "domicile is one of "+strings.Join(cfg.Domiciles, ", "), Fail, f.Domicile)
	}
	switch f.Structure {
	case "physical":
		add("structure", "physical replication", Pass, "physical")
	case "synthetic":
		add("structure", "physical replication", Fail, "synthetic: swap counterparty risk is not assessed here")
	default:
		add("structure", "physical replication", Unknown, "not documented")
	}
	switch {
	case cfg.Distribution == "any" || cfg.Distribution == "":
		add("distribution", "distribution policy documented", map[bool]Result{true: Pass, false: Unknown}[f.Distribution != ""], orNone(f.Distribution))
	case f.Distribution == "":
		add("distribution", "distribution policy is "+cfg.Distribution, Unknown, "not documented")
	case f.Distribution == cfg.Distribution:
		add("distribution", "distribution policy is "+cfg.Distribution, Pass, f.Distribution)
	default:
		add("distribution", "distribution policy is "+cfg.Distribution, Fail, f.Distribution)
	}
	hd, hok := time.Parse(time.DateOnly, f.HoldingsDate)
	ad, _ := time.Parse(time.DateOnly, asOf)
	switch {
	case f.Holdings <= 0 || hok != nil:
		add("diversification", fmt.Sprintf("at least %d holdings in a dated look-through", cfg.MinHoldings), Unknown, "no dated holdings: look-through stays unknown")
	case ad.Sub(hd).Hours()/24 > float64(cfg.MaxHoldingsAge):
		add("diversification", fmt.Sprintf("at least %d holdings in a dated look-through", cfg.MinHoldings), Unknown, "the holdings date "+f.HoldingsDate+" is too old")
	case f.Holdings >= cfg.MinHoldings:
		add("diversification", fmt.Sprintf("at least %d holdings in a dated look-through", cfg.MinHoldings), Pass, fmt.Sprintf("%d holdings on %s", f.Holdings, f.HoldingsDate))
	default:
		add("diversification", fmt.Sprintf("at least %d holdings in a dated look-through", cfg.MinHoldings), Fail, fmt.Sprintf("%d holdings", f.Holdings))
	}
	if len(f.Exposure) == 0 {
		add("currency", "currency exposure documented", Unknown, "the currencies of the holdings are not documented")
	} else {
		add("currency", "currency exposure documented", Pass, fmt.Sprint(f.Exposure))
	}
	a.Eligible = true
	for _, c := range a.Checks {
		if c.Result != Pass {
			a.Eligible = false
		}
	}
	if f.Tilted {
		a.Reasons = append(a.Reasons, "dividend-tilted: compared with a broad core alternative, not proposed as the core holding")
	}
	return a
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// UsableLookThrough returns the company weights when they are dated, no older
// than maxAgeDays on asOf, each above zero and at most one, and add up to at
// most one. Otherwise it returns nil and says why, and the exposure through the
// fund stays unknown.
func (f FundFacts) UsableLookThrough(asOf string, maxAgeDays int) (map[string]money.Decimal, string) {
	if len(f.LookThrough) == 0 {
		return nil, "no look-through weights"
	}
	hd, err := time.Parse(time.DateOnly, f.HoldingsDate)
	if err != nil {
		return nil, "the look-through has no holdings date"
	}
	ad, err := time.Parse(time.DateOnly, asOf)
	if err != nil {
		return nil, "the as-of day is not a date"
	}
	if hd.After(ad) {
		return nil, "the holdings date " + f.HoldingsDate + " is after " + asOf
	}
	if maxAgeDays > 0 && int(ad.Sub(hd).Hours()/24) > maxAgeDays {
		return nil, "the holdings date " + f.HoldingsDate + " is too old"
	}
	sum := money.Int(0)
	out := make(map[string]money.Decimal, len(f.LookThrough))
	for k, w := range f.LookThrough {
		if k == "" || w.Sign() <= 0 || w.Cmp(money.Int(1)) > 0 {
			return nil, "the look-through weight of " + k + " is not a fraction above 0 and at most 1"
		}
		next, err := sum.Add(w)
		if err != nil {
			return nil, err.Error()
		}
		sum = next
		out[k] = w
	}
	if sum.Cmp(money.Int(1)) > 0 {
		return nil, "the look-through weights add up to " + sum.String() + ", more than the whole fund"
	}
	return out, ""
}
