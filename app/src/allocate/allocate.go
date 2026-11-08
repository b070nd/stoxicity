// Package allocate builds a feasible small portfolio from proposals instead of
// an unaffordable ranking. It sizes every line in whole minor units under the
// user's limits and a real fee schedule, spends contributions before anything
// would be sold, and says exactly why a purchase is impossible or should wait.
// It never relaxes a constraint to make a plan come out, and it proposes no
// sale: a breach by existing holdings is reported, and the smallest reduction is
// the job of the review rules.
//
// Conventions, stated once:
//
//   - All limits are fractions of investable equity (cash plus holdings at the
//     values supplied). The sector cap is measured against that equity, not
//     against direct holdings only: on a few positions a share of the direct
//     holdings would forbid any portfolio at all.
//   - Fees are in the base currency: max(minimum, rate x notional) + fixed per
//     order, plus an FX cost rate on the notional of a foreign-currency line.
//     Fees are counted into the budget and into the cost burden
//     (fees / notional of the order). Unknown fees mean no claim of
//     affordability.
//   - A security whose price is in another currency is converted at a rate you
//     supply; without one it is left out.
//   - Event-sleeve risk never moves into the core sleeve: only core candidates
//     are allocated.
package allocate

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Candidate is an instrument a strategy proposed to buy.
type Candidate struct {
	Security string
	Kind     string // stock or fund
	Sleeve   string // core; anything else is not allocated
	Issuer   string // one issuer across all listings and share classes
	Sector   string
	Country  string
	Currency money.Currency
	// Price is in Currency per share; Ceiling is the entry ceiling in the same
	// currency (zero Decimal: none).
	Price   money.Decimal
	Ceiling money.Decimal
	// Rank is the strategy's ordinal ranking, 1 first. It is not a probability.
	Rank int
	// IncomePreference and NextExDay only break ties, after fit and cost.
	IncomePreference bool
	NextExDay        string
	// Fractional says fractional shares can be bought, in steps of Step; otherwise whole shares.
	Fractional bool
	Step       money.Decimal
	// MinQty is the smallest order (shares); zero means one step.
	MinQty money.Decimal
	// LookThroughKnown says a fund's dated holdings are known. An unknown one is shown, never hidden.
	LookThroughKnown bool
	Tradable         bool
}

// Holding is something already owned, valued in the base currency.
type Holding struct {
	Security string
	Kind     string
	Issuer   string
	Sector   string
	Country  string
	Currency money.Currency
	Value    int64 // base currency minor units; ignored when ValueKnown is false
	// ValueKnown is false when no current price exists: the holding is listed but not counted.
	ValueKnown bool
	Sleeve     string
}

// Fees is a commission schedule in the base currency.
type Fees struct {
	Known  bool
	Rate   money.Decimal // fraction of notional
	Min    int64         // minor units per order
	Fixed  int64         // minor units per order
	FXRate money.Decimal // fraction of the notional of a foreign-currency line
	Source string
}

// Limits are the user's constraints, fractions of investable equity.
type Limits struct {
	MaxIssuer       money.Decimal
	MaxSector       money.Decimal
	MaxFund         money.Decimal
	MaxForeign      money.Decimal // all currencies but the base
	MaxCountry      money.Decimal
	CashReserve     money.Decimal // fraction of equity kept as cash
	MinPosition     int64         // smallest position worth opening, minor units
	MaxCostBurden   money.Decimal // fees as a fraction of one order's notional
	ExcludedSectors []string
	ExcludedIssuers []string
	// RequireLookThrough drops funds whose dated holdings are unknown.
	RequireLookThrough bool
}

// Request is one allocation problem.
type Request struct {
	Base money.Currency
	Cash int64 // settled, spendable cash before the reserve, minor units
	// OtherCash is cash held in other currencies, in base minor units at the
	// rates supplied. It counts toward equity, so the caps are measured on what
	// you own, but it is not spent and is not credited against the reserve.
	OtherCash int64
	// OtherCashNotes say which foreign cash could not be valued (no rate).
	OtherCashNotes []string
	Holdings       []Holding
	Candidates     []Candidate
	// Rates are base units per one unit of a foreign currency.
	Rates  map[money.Currency]money.Decimal
	Limits Limits
	Fees   Fees
	// Contribution is the regular contribution, used only to say how many it takes to afford a purchase.
	Contribution int64
}

// Line is one proposed purchase.
type Line struct {
	Security string `json:"security"`
	Qty      string `json:"qty"`
	Notional int64  `json:"notional"` // base minor units
	Fee      int64  `json:"fee"`
	Burden   string `json:"cost_burden"`
	Note     string `json:"note,omitempty"`
}

// Skip says why a candidate was not allocated.
type Skip struct {
	Security string `json:"security"`
	Reason   string `json:"reason"`
}

// Plan is the outcome.
type Plan struct {
	Feasible bool   `json:"feasible"`
	Wait     bool   `json:"wait"`
	Lines    []Line `json:"lines"`
	Skipped  []Skip `json:"skipped"`
	// Spendable is the cash available after the reserve.
	Spendable  int64 `json:"spendable"`
	TotalSpend int64 `json:"total_spend"`
	TotalFees  int64 `json:"total_fees"`
	CashAfter  int64 `json:"cash_after"`
	Equity     int64 `json:"equity"`
	// NeedSpendable is the spendable cash at which the best candidate becomes affordable under the cost limit.
	NeedSpendable int64    `json:"need_spendable,omitempty"`
	Contributions int      `json:"contributions_needed,omitempty"`
	Binding       []string `json:"binding"`
	Unknown       []string `json:"unknown"`
	Breaches      []string `json:"breaches"`
	Explanation   []string `json:"explanation"`
}

func ratOf(d money.Decimal) *big.Rat {
	r, _ := new(big.Rat).SetString(d.String())
	return r
}

func floorDiv(a, b *big.Rat) *big.Int {
	q := new(big.Rat).Quo(a, b)
	return new(big.Int).Div(q.Num(), q.Denom())
}

// frac is f x minor rounded half-even to a whole minor unit.
func frac(f money.Decimal, minor int64) int64 {
	r := new(big.Rat).Mul(ratOf(f), new(big.Rat).SetInt64(minor))
	return roundHalfEven(r)
}

func roundHalfEven(r *big.Rat) int64 {
	num, den := r.Num(), r.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Mul(new(big.Int).Abs(rem), big.NewInt(2))
	switch c := twice.Cmp(den); {
	case c > 0, c == 0 && q.Bit(0) == 1:
		if num.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return q.Int64()
}

func (f Fees) of(notional int64, foreign bool) int64 {
	fee := frac(f.Rate, notional)
	if fee < f.Min {
		fee = f.Min
	}
	fee += f.Fixed
	if foreign {
		fee += frac(f.FXRate, notional)
	}
	return fee
}

func (c Candidate) step() money.Decimal {
	switch {
	case !c.Fractional:
		return money.Int(1)
	case c.Step.Sign() > 0:
		return c.Step
	}
	return money.MustDecimal("0.01")
}

func (c Candidate) minQty() money.Decimal {
	if c.MinQty.Sign() > 0 {
		return c.MinQty
	}
	return c.step()
}

// baseValue is the base-currency minor-unit value of qty shares at the price.
func (r Request) baseValue(c Candidate, qty money.Decimal) (int64, error) {
	scale := c.Price.Scale() + qty.Scale()
	if scale > money.MaxScale {
		scale = money.MaxScale
	}
	native, err := c.Price.Mul(qty, uint8(scale), money.HalfEven)
	if err != nil {
		return 0, err
	}
	rate := money.Int(1)
	if c.Currency != r.Base {
		rr, ok := r.Rates[c.Currency]
		if !ok {
			return 0, fmt.Errorf("no %s->%s rate was supplied", c.Currency, r.Base)
		}
		rate = rr
	}
	v := new(big.Rat).Mul(ratOf(native), ratOf(rate))
	exp, err := r.Base.Exponent()
	if err != nil {
		return 0, err
	}
	v.Mul(v, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil)))
	return roundHalfEven(v), nil
}

// sizing is the result of sizing one candidate for a budget.
type sizing struct {
	qty      money.Decimal
	notional int64
	fee      int64
	burden   money.Decimal
}

type exposure struct {
	issuer, sector, country map[string]int64
	fund, foreign, total    int64
}

func (r Request) exposures() exposure {
	e := exposure{issuer: map[string]int64{}, sector: map[string]int64{}, country: map[string]int64{}}
	for _, h := range r.Holdings {
		if !h.ValueKnown {
			continue
		}
		e.total += h.Value
		if h.Issuer != "" {
			e.issuer[h.Issuer] += h.Value
		}
		if h.Sector != "" && h.Kind != "fund" {
			e.sector[h.Sector] += h.Value
		}
		if h.Country != "" {
			e.country[h.Country] += h.Value
		}
		if h.Kind == "fund" {
			e.fund += h.Value
		}
		if h.Currency != r.Base {
			e.foreign += h.Value
		}
	}
	return e
}

// room is how much more of an exposure the cap allows, never negative.
func room(capFrac money.Decimal, equity, used int64) int64 {
	m := frac(capFrac, equity) - used
	if m < 0 {
		return 0
	}
	return m
}

func (r Request) size(c Candidate, budget int64, e exposure, equity int64) (sizing, string, string) {
	// returns the sizing, a failure reason, and the binding constraint when a cap shrank the order
	if c.Price.Sign() <= 0 {
		return sizing{}, "no usable price", ""
	}
	binding := ""
	cap := budget
	limit := func(name string, capFrac money.Decimal, used int64) {
		if capFrac.Sign() > 0 {
			if m := room(capFrac, equity, used); m < cap {
				cap, binding = m, name
			}
		}
	}
	limit("issuer limit "+c.Issuer, r.Limits.MaxIssuer, e.issuer[c.Issuer])
	if c.Kind == "fund" {
		limit("fund limit", r.Limits.MaxFund, e.fund)
	} else {
		limit("sector limit "+c.Sector, r.Limits.MaxSector, e.sector[c.Sector])
	}
	limit("country limit "+c.Country, r.Limits.MaxCountry, e.country[c.Country])
	foreign := c.Currency != r.Base
	if foreign {
		limit("foreign-currency limit", r.Limits.MaxForeign, e.foreign)
	}
	step, minQty := c.step(), c.minQty()
	// first guess from the price in base units, then walk down until notional + fee fits
	unit, err := r.baseValue(c, step)
	if err != nil {
		return sizing{}, err.Error(), ""
	}
	if unit <= 0 {
		return sizing{}, "the price rounds to nothing in the base currency", ""
	}
	n := new(big.Int).Div(big.NewInt(cap), big.NewInt(unit))
	for ; n.Sign() > 0; n.Sub(n, big.NewInt(1)) {
		q, err := step.Mul(money.Int(n.Int64()), uint8(step.Scale()), money.Exact)
		if err != nil {
			return sizing{}, err.Error(), ""
		}
		notional, err := r.baseValue(c, q)
		if err != nil {
			return sizing{}, err.Error(), ""
		}
		fee := r.Fees.of(notional, foreign)
		if notional+fee > cap {
			continue
		}
		if q.Cmp(minQty) < 0 {
			break
		}
		burden := money.Decimal{}
		if notional > 0 {
			b := new(big.Rat).SetFrac64(fee, notional)
			burden = ratToDecimal(b)
		}
		if notional < r.Limits.MinPosition {
			return sizing{}, fmt.Sprintf("the largest order that fits (%s) is below the minimum position %s", minorText(notional, r.Base), minorText(r.Limits.MinPosition, r.Base)), binding
		}
		if r.Limits.MaxCostBurden.Sign() > 0 && burden.Cmp(r.Limits.MaxCostBurden) > 0 {
			return sizing{}, fmt.Sprintf("fees of %s on %s are %s of the order, above the limit %s", minorText(fee, r.Base), minorText(notional, r.Base), pc(burden), pc(r.Limits.MaxCostBurden)), binding
		}
		return sizing{qty: q, notional: notional, fee: fee, burden: burden}, "", binding
	}
	if binding != "" {
		return sizing{}, fmt.Sprintf("the %s leaves room for %s, less than the smallest order of %s shares", binding, minorText(cap, r.Base), minQty), binding
	}
	return sizing{}, fmt.Sprintf("%s buys less than the smallest order of %s shares at this price", minorText(cap, r.Base), minQty), ""
}

func ratToDecimal(r *big.Rat) money.Decimal {
	f := big.NewInt(1000000)
	v := roundHalfEven(new(big.Rat).Mul(r, new(big.Rat).SetInt(f)))
	d, _ := money.NewDecimal(v, 6)
	return d
}

func pc(d money.Decimal) string {
	p, err := d.Mul(money.MustDecimal("100"), 2, money.HalfEven)
	if err != nil {
		return d.String()
	}
	return p.String() + "%"
}

func minorText(minor int64, cur money.Currency) string {
	a, err := money.FromMinor(minor, cur)
	if err != nil {
		return fmt.Sprint(minor)
	}
	return a.String()
}

// Allocate builds the plan.
func Allocate(r Request) Plan {
	p := Plan{Lines: []Line{}, Skipped: []Skip{}, Binding: []string{}, Unknown: []string{}, Breaches: []string{}, Explanation: []string{}}
	e := r.exposures()
	equity := r.Cash + r.OtherCash + e.total
	p.Unknown = append(p.Unknown, r.OtherCashNotes...)
	if r.OtherCash != 0 {
		p.Explanation = append(p.Explanation, fmt.Sprintf("%s of cash in other currencies counts toward equity so the caps are measured on what you own; it is not spent here and is not credited against the reserve", minorText(r.OtherCash, r.Base)))
	}
	p.Equity = equity
	reserve := frac(r.Limits.CashReserve, equity)
	spend := r.Cash - reserve
	if spend < 0 {
		spend = 0
	}
	p.Spendable = spend
	for _, h := range r.Holdings {
		if !h.ValueKnown {
			p.Unknown = append(p.Unknown, fmt.Sprintf("%s has no current value: concentration is understated by it", h.Security))
		}
	}
	// existing breaches are reported, never fixed by a sale here
	chk := func(name string, used int64, capFrac money.Decimal) {
		if capFrac.Sign() > 0 && equity > 0 && used > frac(capFrac, equity) {
			p.Breaches = append(p.Breaches, fmt.Sprintf("%s: %s of %s equity is above %s; no sale is proposed here, contributions go elsewhere", name, minorText(used, r.Base), minorText(equity, r.Base), pc(capFrac)))
		}
	}
	for k, v := range e.issuer {
		chk("issuer "+k, v, r.Limits.MaxIssuer)
	}
	for k, v := range e.sector {
		chk("sector "+k, v, r.Limits.MaxSector)
	}
	for k, v := range e.country {
		chk("country "+k, v, r.Limits.MaxCountry)
	}
	chk("funds", e.fund, r.Limits.MaxFund)
	chk("foreign currency", e.foreign, r.Limits.MaxForeign)
	sort.Strings(p.Breaches)

	if !r.Fees.Known {
		p.Wait = true
		p.Skipped = append(p.Skipped, Skip{"*", "trading costs are unknown"})
		p.Explanation = append(p.Explanation, "no claim is made that any purchase is affordable: enter your broker's commission schedule (policy account.commission_schedule) and regenerate")
		return p
	}
	// eligibility filter
	var cands []Candidate
	for _, c := range r.Candidates {
		reason := ""
		switch {
		case c.Sleeve != "core":
			reason = "not a core candidate: event-sleeve risk is never moved into the core sleeve"
		case !c.Tradable:
			reason = "not tradable on the account"
		case c.Price.Sign() <= 0:
			reason = "no current price"
		case c.Ceiling.Sign() > 0 && c.Price.Cmp(c.Ceiling) > 0:
			reason = fmt.Sprintf("the price %s is above the entry ceiling %s", c.Price, c.Ceiling)
		case contains(r.Limits.ExcludedSectors, c.Sector):
			reason = "its sector " + c.Sector + " is excluded by your policy"
		case contains(r.Limits.ExcludedIssuers, c.Issuer):
			reason = "its issuer " + c.Issuer + " is excluded by your policy"
		case c.Kind == "fund" && !c.LookThroughKnown && r.Limits.RequireLookThrough:
			reason = "its look-through holdings are unknown and your policy requires them"
		case c.Currency != r.Base && r.Rates[c.Currency].Sign() <= 0:
			reason = fmt.Sprintf("no %s->%s rate was supplied", c.Currency, r.Base)
		}
		if reason != "" {
			p.Skipped = append(p.Skipped, Skip{c.Security, reason})
			continue
		}
		cands = append(cands, c)
	}
	// order: rank, then cost at the smallest order (fit and cost), then income preference and the earlier ex-date
	cost := map[string]string{}
	for _, c := range cands {
		if q, err := r.baseValue(c, c.minQty()); err == nil {
			cost[c.Security] = fmt.Sprintf("%020d", r.Fees.of(q, c.Currency != r.Base))
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		switch {
		case a.Rank != b.Rank:
			return a.Rank < b.Rank
		case cost[a.Security] != cost[b.Security]:
			return cost[a.Security] < cost[b.Security]
		case a.IncomePreference != b.IncomePreference:
			return a.IncomePreference
		case a.NextExDay != b.NextExDay && a.NextExDay != "" && b.NextExDay != "":
			return a.NextExDay < b.NextExDay
		}
		return a.Security < b.Security
	})
	for _, c := range cands {
		if c.Kind == "fund" && !c.LookThroughKnown {
			p.Unknown = append(p.Unknown, fmt.Sprintf("%s: look-through holdings unknown, so issuer, sector and country exposure through it is not counted", c.Security))
		}
	}
	if len(cands) == 0 {
		p.Explanation = append(p.Explanation, "no candidate is eligible: see the reasons per candidate; cash is kept")
		return p
	}
	if spend == 0 {
		p.Wait = true
		p.Explanation = append(p.Explanation, fmt.Sprintf("nothing is spendable: cash %s is not above the reserve %s", minorText(r.Cash, r.Base), minorText(reserve, r.Base)))
		return p
	}

	var chosen []Line
	var failures map[string]string
	var firstFail string
	for n := len(cands); n >= 1; n-- {
		budget := spend / int64(n)
		var ok []Line
		fails := map[string]string{}
		var bind []string
		for _, c := range cands {
			sz, why, binding := r.size(c, budget, e, equity)
			if why != "" {
				fails[c.Security] = why
				continue
			}
			ln := Line{Security: c.Security, Qty: sz.qty.String(), Notional: sz.notional, Fee: sz.fee, Burden: pc(sz.burden)}
			if binding != "" {
				ln.Note = "sized by the " + binding
				bind = append(bind, c.Security+": "+binding)
			}
			ok = append(ok, ln)
		}
		failures = fails
		if n == len(cands) {
			for _, c := range cands {
				if why, bad := fails[c.Security]; bad {
					firstFail = fmt.Sprintf("with all %d candidates %s could not be sized: %s", n, c.Security, why)
					break
				}
			}
		}
		if len(ok) >= n {
			chosen = ok[:n]
			for _, b := range bind {
				for _, l := range chosen {
					if strings.HasPrefix(b, l.Security+":") {
						p.Binding = append(p.Binding, b)
					}
				}
			}
			break
		}
		if n == 1 {
			chosen = nil
		}
	}
	if len(chosen) == 0 {
		for _, c := range cands {
			if why, ok := failures[c.Security]; ok {
				p.Skipped = append(p.Skipped, Skip{c.Security, why})
			}
		}
		p.Explanation = append(p.Explanation, "no feasible purchase under your limits and fees: cash is kept and nothing is relaxed")
		r.waitFor(&p, cands, e, equity)
		return p
	}
	taken := map[string]bool{}
	for _, l := range chosen {
		taken[l.Security] = true
		p.TotalSpend += l.Notional
		p.TotalFees += l.Fee
	}
	for _, c := range cands {
		if !taken[c.Security] {
			if why, ok := failures[c.Security]; ok {
				p.Skipped = append(p.Skipped, Skip{c.Security, why})
			} else {
				why := "left out of a smaller basket so that every order meets the minimum position and the cost limit"
				if firstFail != "" {
					why += " (" + firstFail + ")"
				}
				p.Skipped = append(p.Skipped, Skip{c.Security, why})
			}
		}
	}
	p.Lines = chosen
	p.Feasible = true
	p.CashAfter = r.Cash - p.TotalSpend - p.TotalFees
	p.Explanation = append(p.Explanation, fmt.Sprintf("%d position(s): %s spent, %s fees, %s cash left of which %s is the reserve; contributions are used before any sale, and no sale is proposed",
		len(chosen), minorText(p.TotalSpend, r.Base), minorText(p.TotalFees, r.Base), minorText(p.CashAfter, r.Base), minorText(reserve, r.Base)))
	return p
}

// waitFor works out when the best candidate would become affordable and says so.
// Adding cash raises equity too, so a concentration cap moves with it.
func (r Request) waitFor(p *Plan, cands []Candidate, e exposure, equity int64) {
	c := cands[0]
	foreign := c.Currency != r.Base
	if unit, err := r.baseValue(c, c.minQty()); err != nil || unit <= 0 {
		return
	}
	resFrac := ratOf(r.Limits.CashReserve)
	keep := new(big.Rat).Sub(big.NewRat(1, 1), resFrac) // share of a deposit that becomes spendable
	type capUse struct {
		name string
		frac money.Decimal
		used int64
	}
	caps := []capUse{{"issuer", r.Limits.MaxIssuer, e.issuer[c.Issuer]}, {"country", r.Limits.MaxCountry, e.country[c.Country]}}
	if c.Kind == "fund" {
		caps = append(caps, capUse{"fund", r.Limits.MaxFund, e.fund})
	} else {
		caps = append(caps, capUse{"sector", r.Limits.MaxSector, e.sector[c.Sector]})
	}
	for k := int64(1); k <= 100000; k++ {
		q, err := c.minQty().Mul(money.Int(k), uint8(c.minQty().Scale()), money.Exact)
		if err != nil {
			return
		}
		n, err := r.baseValue(c, q)
		if err != nil {
			return
		}
		fee := r.Fees.of(n, foreign)
		if n < r.Limits.MinPosition {
			continue
		}
		if r.Limits.MaxCostBurden.Sign() > 0 && ratOf(r.Limits.MaxCostBurden).Cmp(new(big.Rat).SetFrac64(fee, n)) < 0 {
			continue
		}
		total := n + fee
		// deposit X so that spendable + X*keep >= total and every cap holds on the larger equity
		x := new(big.Rat).Quo(new(big.Rat).SetInt64(total-p.Spendable), keep)
		for _, cu := range caps {
			if cu.frac.Sign() > 0 {
				need := new(big.Rat).Quo(new(big.Rat).SetInt64(total+cu.used), ratOf(cu.frac))
				need.Sub(need, new(big.Rat).SetInt64(equity))
				if need.Cmp(x) > 0 {
					x = need
				}
			}
		}
		if x.Sign() < 0 {
			x = new(big.Rat)
		}
		deposit := ceilRat(x)
		p.Wait = true
		p.NeedSpendable = p.Spendable + roundHalfEven(new(big.Rat).Mul(new(big.Rat).SetInt64(deposit), keep))
		p.Explanation = append(p.Explanation, fmt.Sprintf("wait: the smallest order of %s that meets the minimum position, the cost limit (%s) and your concentration limits is %s plus %s fees; that needs about %s more cash than the %s now held",
			c.Security, pc(r.Limits.MaxCostBurden), minorText(n, r.Base), minorText(fee, r.Base), minorText(deposit, r.Base), minorText(r.Cash, r.Base)))
		if r.Contribution > 0 {
			p.Contributions = int((deposit + r.Contribution - 1) / r.Contribution)
			p.Explanation = append(p.Explanation, fmt.Sprintf("that is %d contribution(s) of %s: batch them instead of buying in pieces", p.Contributions, minorText(r.Contribution, r.Base)))
		}
		return
	}
}

func ceilRat(r *big.Rat) int64 {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Int64()
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v != "" && v == x {
			return true
		}
	}
	return false
}

// Option is one way to use the money, planned under the same limits.
type Option struct {
	Name string `json:"name"`
	Plan Plan   `json:"plan"`
}

// Compare plans the three starting choices of a small account: a diversified
// fund, a few individual stocks, and keeping cash while contributions build up.
// It ranks nothing: the options are shown side by side with their costs,
// positions and unknowns.
func Compare(r Request) []Option {
	only := func(kind string) Request {
		q := r
		q.Candidates = nil
		for _, c := range r.Candidates {
			if c.Kind == kind {
				q.Candidates = append(q.Candidates, c)
			}
		}
		return q
	}
	fund := Allocate(only("fund"))
	stocks := Allocate(only("stock"))
	cash := Plan{Feasible: true, Lines: []Line{}, Skipped: []Skip{}, Binding: []string{}, Unknown: []string{}, Breaches: []string{}, Explanation: []string{}, Spendable: stocks.Spendable, CashAfter: r.Cash, Equity: stocks.Equity}
	cash.Explanation = append(cash.Explanation, "nothing is bought; cash stays available and contributions accumulate. No interest on cash is assumed unless you supply it.")
	for _, p := range []Plan{fund, stocks} {
		if p.NeedSpendable > cash.NeedSpendable {
			cash.NeedSpendable = p.NeedSpendable
		}
	}
	if cash.NeedSpendable > 0 {
		cash.Explanation = append(cash.Explanation, "an order meeting your minimum position and cost limit needs about "+minorText(cash.NeedSpendable, r.Base)+" of spendable cash")
	}
	return []Option{{"diversified core fund", fund}, {"individual stocks", stocks}, {"cash and contributions", cash}}
}
