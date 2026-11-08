package money

import (
	"errors"
	"fmt"
)

// Price is a quoted price in a quote unit. Its value is in that unit: 1234.5 in
// GBp is 12.345 pounds. Use Major or Notional to leave the unit.
type Price struct {
	Value Decimal
	Unit  QuoteUnit
}

// NewPrice checks that the value is not negative and the unit is known.
func NewPrice(v Decimal, unit QuoteUnit) (Price, error) {
	if !unit.Currency.Valid() || unit.Divisor < 1 {
		return Price{}, fmt.Errorf("money: price without a valid quote unit")
	}
	if v.Sign() < 0 {
		return Price{}, fmt.Errorf("money: negative price %s", v)
	}
	return Price{Value: v, Unit: unit}, nil
}

// Major is the price in major currency units (pounds, not pence). A subunit
// price divides by its divisor exactly, so the scale grows by the digits of the
// divisor; a price that has no room for them is refused.
func (p Price) Major() (Decimal, error) {
	if p.Unit.Divisor == 1 {
		return p.Value, nil
	}
	if p.Unit.Divisor != 100 {
		return Decimal{}, fmt.Errorf("money: unsupported quote divisor %d", p.Unit.Divisor)
	}
	return p.Value.Div(Int(100), uint8(p.Value.scale)+2, Exact)
}

// Notional is price times quantity in the currency, rounded to its minor unit.
func Notional(p Price, qty Decimal, mode Rounding) (Amount, error) {
	if qty.Sign() < 0 {
		return Amount{}, errors.New("money: negative quantity")
	}
	major, err := p.Major()
	if err != nil {
		return Amount{}, err
	}
	e, err := p.Unit.Currency.Exponent()
	if err != nil {
		return Amount{}, err
	}
	d, err := major.Mul(qty, uint8(e), mode)
	if err != nil {
		return Amount{}, err
	}
	return Amount{cur: p.Unit.Currency, minor: d.coef}, nil
}

// QtyRules are what a venue or broker accepts as an order quantity.
type QtyRules struct {
	Step Decimal // the smallest increment: 1 for whole shares, 0.0001 for fractions
	Min  Decimal // the smallest order
}

// Check says why a quantity is not an acceptable order size, or nil.
func (r QtyRules) Check(q Decimal) error {
	if r.Step.Sign() <= 0 {
		return errors.New("money: the quantity step is not set")
	}
	if q.Sign() <= 0 {
		return fmt.Errorf("money: quantity %s must be positive", q)
	}
	if q.Cmp(r.Min) < 0 {
		return fmt.Errorf("money: quantity %s is below the minimum %s", q, r.Min)
	}
	m, err := q.Mod(r.Step)
	if err != nil {
		return err
	}
	if !m.IsZero() {
		return fmt.Errorf("money: quantity %s is not a multiple of %s", q, r.Step)
	}
	return nil
}

// Fit rounds a quantity to a multiple of the step with mode (Down never takes
// more than asked). It does not check the minimum.
func (r QtyRules) Fit(q Decimal, mode Rounding) (Decimal, error) {
	if r.Step.Sign() <= 0 {
		return Decimal{}, errors.New("money: the quantity step is not set")
	}
	n, err := q.Div(r.Step, 0, mode)
	if err != nil {
		return Decimal{}, err
	}
	return n.Mul(r.Step, uint8(r.Step.scale), Exact)
}

// TickBand says that from this price up the price moves in steps of Tick.
type TickBand struct {
	From Decimal
	Tick Decimal
}

// TickTable is the price increments of a venue, ordered by From; the first band
// must start at 0.
type TickTable []TickBand

// Validate checks the table.
func (t TickTable) Validate() error {
	if len(t) == 0 {
		return errors.New("money: empty tick table")
	}
	if !t[0].From.IsZero() {
		return errors.New("money: the first tick band must start at 0")
	}
	for i, b := range t {
		if b.Tick.Sign() <= 0 {
			return fmt.Errorf("money: tick band %d has no positive tick", i)
		}
		if i > 0 && b.From.Cmp(t[i-1].From) <= 0 {
			return fmt.Errorf("money: tick bands are not ascending at %d", i)
		}
	}
	return nil
}

func (t TickTable) band(p Decimal) int {
	k := 0
	for i, b := range t {
		if p.Cmp(b.From) >= 0 {
			k = i
		}
	}
	return k
}

// TickAt is the increment that applies at the price.
func (t TickTable) TickAt(p Decimal) (Decimal, error) {
	if err := t.Validate(); err != nil {
		return Decimal{}, err
	}
	return t[t.band(p)].Tick, nil
}

// Valid reports whether p is on the tick grid of its band.
func (t TickTable) Valid(p Decimal) error {
	tick, err := t.TickAt(p)
	if err != nil {
		return err
	}
	if p.Sign() <= 0 {
		return fmt.Errorf("money: price %s must be positive", p)
	}
	m, err := p.Mod(tick)
	if err != nil {
		return err
	}
	if !m.IsZero() {
		return fmt.Errorf("money: price %s is not a multiple of the tick %s", p, tick)
	}
	return nil
}

// Snap moves the price onto the grid with mode (Floor for a buy limit that must
// not exceed what was meant, Ceiling for a sell). A move across a band edge is
// checked again in the new band.
func (t TickTable) Snap(p Decimal, mode Rounding) (Decimal, error) {
	if err := t.Validate(); err != nil {
		return Decimal{}, err
	}
	cur := p
	for i := 0; i < 4; i++ {
		tick := t[t.band(cur)].Tick
		n, err := cur.Div(tick, 0, mode)
		if err != nil {
			return Decimal{}, err
		}
		next, err := n.Mul(tick, uint8(tick.scale), Exact)
		if err != nil {
			return Decimal{}, err
		}
		if t.Valid(next) == nil {
			return next, nil
		}
		cur = next
	}
	return Decimal{}, fmt.Errorf("money: price %s does not settle on the tick grid", p)
}
