package money

import (
	"errors"
	"fmt"
)

// Basis says what a rate is good for. A reference rate is for reporting and
// scenarios; only a rate the broker actually gave on a fill is a price paid.
type Basis string

const (
	BasisReference Basis = "reference" // ECB-style daily reference: information only
	BasisFill      Basis = "fill"      // the rate of an executed conversion
	BasisUser      Basis = "user"      // typed by the user as an assumption
)

// Executable reports whether the rate may be presented as one that was or can
// be traded at. A reference rate never is.
func (b Basis) Executable() bool { return b == BasisFill }

// ErrDirection is a conversion asked in the direction the rate does not go.
var ErrDirection = errors.New("money: rate does not convert in this direction")

// Rate says how many units of Quote one unit of Base is worth: 1 Base = Value
// Quote. EUR/USD 1.0850 converts EUR to USD by multiplying, and the other way
// only through Invert, so a direction mistake is an error, never a silent flip.
type Rate struct {
	Base, Quote Currency
	Value       Decimal
	Basis       Basis
}

// NewRate checks the currencies and that the value is positive.
func NewRate(base, quote Currency, value Decimal, basis Basis) (Rate, error) {
	if !base.Valid() || !quote.Valid() {
		return Rate{}, fmt.Errorf("%w: rate %s/%s", ErrCurrency, base, quote)
	}
	if base == quote {
		return Rate{}, fmt.Errorf("money: rate of %s against itself", base)
	}
	if value.Sign() <= 0 {
		return Rate{}, fmt.Errorf("money: rate %s/%s must be positive, not %s", base, quote, value)
	}
	switch basis {
	case BasisReference, BasisFill, BasisUser:
	default:
		return Rate{}, fmt.Errorf("money: rate %s/%s has no basis", base, quote)
	}
	return Rate{Base: base, Quote: quote, Value: value, Basis: basis}, nil
}

// Convert turns an amount of the base currency into the quote currency, rounded
// to the quote's minor unit.
func (r Rate) Convert(a Amount, mode Rounding) (Amount, error) {
	if a.cur != r.Base {
		return Amount{}, fmt.Errorf("%w: the rate is %s/%s and the amount is in %s", ErrDirection, r.Base, r.Quote, a.cur)
	}
	e, err := r.Quote.Exponent()
	if err != nil {
		return Amount{}, err
	}
	d, err := a.Decimal().Mul(r.Value, uint8(e), mode)
	if err != nil {
		return Amount{}, err
	}
	return Amount{cur: r.Quote, minor: d.coef}, nil
}

// Invert returns the rate the other way round, with the given number of digits.
// The inverse of a rounded rate is not exact, so the caller chooses the scale
// and the rounding; converting there and back can differ by a minor unit.
func (r Rate) Invert(scale uint8, mode Rounding) (Rate, error) {
	v, err := Int(1).Div(r.Value, scale, mode)
	if err != nil {
		return Rate{}, err
	}
	return NewRate(r.Quote, r.Base, v, r.Basis)
}
