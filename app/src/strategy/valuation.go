// Package strategy turns a scorecard, the declared payouts, a price and the
// user's written assumptions into a proposal: an action with its reasons, a
// scored checklist, bear/base/bull valuation and an entry ceiling. It never
// emits a win probability or an expected return: the score is a policy ordering
// and a scenario is an assumption laid out, not a forecast.
//
// A proposal is research output. The strategies here are unvalidated starting
// points: until the user has reviewed a configuration (Config.Reviewed) no
// proposal can be BUY, and nothing here places an order.
package strategy

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Scenario is one set of valuation assumptions. All three numbers are the
// user's; none has a default, because an invented growth rate would make the
// price look precise.
type Scenario struct {
	Name     string        `json:"name"`
	Growth   money.Decimal `json:"growth"`   // annual growth of the cash flow per share for the explicit years
	Discount money.Decimal `json:"discount"` // annual discount rate
	Terminal money.Decimal `json:"terminal"` // perpetual growth after the explicit years
}

// Assumptions are the documented inputs of a valuation.
type Assumptions struct {
	// Method names the model; only the owner-earnings discounted cash flow exists.
	Method   string         `json:"method"`
	Currency money.Currency `json:"currency"`
	// Normalization scales the latest free cash flow per share (1 = as reported). It
	// is explicit so that an adjustment cannot be applied silently.
	Normalization money.Decimal `json:"normalization"`
	Years         int           `json:"years"`
	Bear          Scenario      `json:"bear"`
	Base          Scenario      `json:"base"`
	Bull          Scenario      `json:"bull"`
	// Source and Ref say where the assumptions come from (the user's own memo, a model).
	Source string `json:"source"`
	Ref    string `json:"ref"`
}

// Method names.
const MethodOwnerEarningsDCF = "owner_earnings_dcf"

// Validate says why the assumptions cannot be used.
func (a Assumptions) Validate() error {
	if a.Method != MethodOwnerEarningsDCF {
		return fmt.Errorf("strategy: unknown valuation method %q (%s)", a.Method, MethodOwnerEarningsDCF)
	}
	if !a.Currency.Valid() {
		return fmt.Errorf("strategy: the valuation currency %q is not known", a.Currency)
	}
	if a.Years < 1 || a.Years > 30 {
		return errors.New("strategy: the explicit years must be 1 to 30")
	}
	if a.Normalization.Sign() <= 0 || a.Normalization.Cmp(money.MustDecimal("2")) > 0 {
		return errors.New("strategy: normalization must be greater than 0 and at most 2 (1 means none)")
	}
	if a.Source == "" || a.Ref == "" {
		return errors.New("strategy: assumptions need their source and reference: an undocumented input is not accepted")
	}
	for _, s := range []Scenario{a.Bear, a.Base, a.Bull} {
		if err := s.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (s Scenario) validate() error {
	one := money.MustDecimal("1")
	lo, hi := money.MustDecimal("-0.5"), money.MustDecimal("0.5")
	switch {
	case s.Growth.Cmp(lo) < 0 || s.Growth.Cmp(hi) > 0:
		return fmt.Errorf("strategy: the %s growth %s is outside -50%% to 50%% a year", s.Name, s.Growth)
	case s.Discount.Sign() <= 0 || s.Discount.Cmp(one) >= 0:
		return fmt.Errorf("strategy: the %s discount rate %s must be above 0 and below 100%%", s.Name, s.Discount)
	case s.Terminal.Sign() < 0:
		return fmt.Errorf("strategy: the %s terminal growth %s must not be negative", s.Name, s.Terminal)
	case s.Terminal.Cmp(s.Discount) >= 0:
		return fmt.Errorf("strategy: the %s terminal growth %s must be below its discount rate %s", s.Name, s.Terminal, s.Discount)
	}
	return nil
}

func rat(d money.Decimal) *big.Rat {
	r, ok := new(big.Rat).SetString(d.String())
	if !ok {
		panic("strategy: a decimal that does not parse: " + d.String())
	}
	return r
}

// roundRat rounds half-even to scale digits.
func roundRat(r *big.Rat, scale uint8) (money.Decimal, error) {
	f := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	num := new(big.Int).Mul(r.Num(), f)
	q, rem := new(big.Int).QuoRem(num, r.Denom(), new(big.Int))
	// QuoRem truncates toward zero; adjust by magnitude of the remainder
	twice := new(big.Int).Mul(new(big.Int).Abs(rem), big.NewInt(2))
	switch c := twice.Cmp(r.Denom()); {
	case c > 0, c == 0 && q.Bit(0) == 1:
		if num.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	if !q.IsInt64() {
		return money.Decimal{}, errors.New("strategy: value out of range")
	}
	return money.NewDecimal(q.Int64(), scale)
}

// ValuePerShare is the present value of the cash flow per share under one
// scenario: the explicit years grown at Growth, then a terminal value. The
// arithmetic is exact (rational) and rounded once, half-even, to four digits.
func ValuePerShare(cf money.Decimal, years int, s Scenario) (money.Decimal, error) {
	if cf.Sign() <= 0 {
		return money.Decimal{}, errors.New("strategy: the cash flow per share must be positive")
	}
	if err := s.validate(); err != nil {
		return money.Decimal{}, err
	}
	one := big.NewRat(1, 1)
	g := new(big.Rat).Add(one, rat(s.Growth))
	r := new(big.Rat).Add(one, rat(s.Discount))
	pv := new(big.Rat)
	flow := rat(cf)
	disc := big.NewRat(1, 1)
	for t := 1; t <= years; t++ {
		flow = new(big.Rat).Mul(flow, g)
		disc = new(big.Rat).Mul(disc, r)
		pv.Add(pv, new(big.Rat).Quo(flow, disc))
	}
	tv := new(big.Rat).Mul(flow, new(big.Rat).Add(one, rat(s.Terminal)))
	tv.Quo(tv, new(big.Rat).Sub(rat(s.Discount), rat(s.Terminal)))
	pv.Add(pv, new(big.Rat).Quo(tv, disc))
	return roundRat(pv, 4)
}

// Valuation is the result of the three scenarios for one security.
type Valuation struct {
	Currency      money.Currency `json:"currency"`
	CashFlowShare money.Decimal  `json:"cash_flow_per_share"`
	Shares        money.Decimal  `json:"shares"`
	Bear          money.Decimal  `json:"bear"`
	Base          money.Decimal  `json:"base"`
	Bull          money.Decimal  `json:"bull"`
	Ordered       bool           `json:"ordered"`
	// Ceiling is the entry scenario's value less the margin of safety.
	EntryScenario  string        `json:"entry_scenario"`
	MarginOfSafety money.Decimal `json:"margin_of_safety"`
	Ceiling        money.Decimal `json:"entry_ceiling"`
	Sensitivity    []Sensitivity `json:"sensitivity"`
	Note           string        `json:"note"`
}

// Sensitivity is the base value with the discount rate and terminal growth moved.
type Sensitivity struct {
	DiscountShift money.Decimal `json:"discount_shift"`
	TerminalShift money.Decimal `json:"terminal_shift"`
	Value         money.Decimal `json:"value"`
	Available     bool          `json:"available"`
}

// Value computes the scenarios for a cash flow per share.
func Value(a Assumptions, cf, shares money.Decimal, entryScenario string, mos money.Decimal) (Valuation, error) {
	if err := a.Validate(); err != nil {
		return Valuation{}, err
	}
	if mos.Sign() < 0 || mos.Cmp(money.MustDecimal("0.9")) > 0 {
		return Valuation{}, errors.New("strategy: the margin of safety must be 0 to 90%")
	}
	cf, err := cf.Mul(a.Normalization, 8, money.HalfEven)
	if err != nil {
		return Valuation{}, err
	}
	v := Valuation{Currency: a.Currency, CashFlowShare: cf, Shares: shares, EntryScenario: entryScenario, MarginOfSafety: mos}
	for _, p := range []struct {
		s   Scenario
		dst *money.Decimal
	}{{a.Bear, &v.Bear}, {a.Base, &v.Base}, {a.Bull, &v.Bull}} {
		if *p.dst, err = ValuePerShare(cf, a.Years, p.s); err != nil {
			return Valuation{}, fmt.Errorf("%s: %w", p.s.Name, err)
		}
	}
	v.Ordered = v.Bear.Cmp(v.Base) <= 0 && v.Base.Cmp(v.Bull) <= 0
	if !v.Ordered {
		v.Note = "the bear, base and bull values are not in order: the assumptions contradict their names"
	}
	var entry money.Decimal
	switch entryScenario {
	case "bear":
		entry = v.Bear
	case "base":
		entry = v.Base
	default:
		return Valuation{}, fmt.Errorf("strategy: the entry scenario %q must be bear or base", entryScenario)
	}
	keep, _ := money.MustDecimal("1").Sub(mos)
	if v.Ceiling, err = entry.Mul(keep, 4, money.HalfEven); err != nil {
		return Valuation{}, err
	}
	for _, dr := range []string{"-0.01", "0", "0.01"} {
		for _, tg := range []string{"-0.005", "0", "0.005"} {
			s := a.Base
			dd, tt := money.MustDecimal(dr), money.MustDecimal(tg)
			s.Discount, _ = s.Discount.Add(dd)
			s.Terminal, _ = s.Terminal.Add(tt)
			cell := Sensitivity{DiscountShift: dd, TerminalShift: tt}
			if val, err := ValuePerShare(cf, a.Years, s); err == nil {
				cell.Value, cell.Available = val, true
			}
			v.Sensitivity = append(v.Sensitivity, cell)
		}
	}
	return v, nil
}
