// Package money holds the exact number types of the portfolio: decimals,
// amounts in a currency, quote units (pence against pounds), exchange rates with
// a direction, quantities and price ticks. No binary float is ever used for a
// balance. Every operation that can lose digits takes the target precision and
// the rounding mode from the caller, and an overflow, a malformed number or a
// mismatch of currencies is an error, never a wrapped or guessed value.
package money

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Errors of this package. They wrap with detail, so test with errors.Is.
var (
	ErrSyntax   = errors.New("money: not a plain decimal number")
	ErrOverflow = errors.New("money: value out of range")
	ErrScale    = errors.New("money: scale out of range")
	ErrInexact  = errors.New("money: result is not exact at the requested precision")
	ErrDivZero  = errors.New("money: division by zero")
)

// MaxScale is the most digits after the decimal point a Decimal keeps.
const MaxScale = 18

// Rounding says what happens to the digits that do not fit. Exact refuses to
// round: the operation fails with ErrInexact if any digit would be lost.
type Rounding int

const (
	Exact    Rounding = iota // fail rather than lose a digit
	Down                     // toward zero
	Up                       // away from zero
	Floor                    // toward negative infinity
	Ceiling                  // toward positive infinity
	HalfEven                 // to nearest, a tie to the even neighbour
	HalfUp                   // to nearest, a tie away from zero
	HalfDown                 // to nearest, a tie toward zero
)

func (r Rounding) String() string {
	switch r {
	case Exact:
		return "exact"
	case Down:
		return "down"
	case Up:
		return "up"
	case Floor:
		return "floor"
	case Ceiling:
		return "ceiling"
	case HalfEven:
		return "half-even"
	case HalfUp:
		return "half-up"
	case HalfDown:
		return "half-down"
	}
	return fmt.Sprintf("rounding(%d)", int(r))
}

// Decimal is a fixed-point number: coef / 10^scale. The zero value is 0. The
// scale is part of the value's presentation only: 1.50 and 1.5 compare equal.
type Decimal struct {
	coef  int64
	scale uint8
}

var pow10Table = func() []*big.Int {
	t := make([]*big.Int, 80)
	t[0] = big.NewInt(1)
	for i := 1; i < len(t); i++ {
		t[i] = new(big.Int).Mul(t[i-1], big.NewInt(10))
	}
	return t
}()

func pow10(n int) *big.Int { return pow10Table[n] }

// NewDecimal returns coef / 10^scale.
func NewDecimal(coef int64, scale uint8) (Decimal, error) {
	if scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %d digits after the point, at most %d", ErrScale, scale, MaxScale)
	}
	return Decimal{coef: coef, scale: scale}, nil
}

// Int returns the whole number n.
func Int(n int64) Decimal { return Decimal{coef: n} }

// ParseDecimal reads a plain decimal: an optional minus sign, digits, and
// optionally a point followed by digits. An exponent, a plus sign, a bare point,
// thousands separators, NaN and Inf are all refused.
func ParseDecimal(s string) (Decimal, error) {
	orig := s
	neg := false
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	}
	if s == "" {
		return Decimal{}, fmt.Errorf("%w: %q", ErrSyntax, orig)
	}
	intPart, frac, hasPoint := strings.Cut(s, ".")
	if intPart == "" || (hasPoint && frac == "") {
		return Decimal{}, fmt.Errorf("%w: %q", ErrSyntax, orig)
	}
	for _, part := range []string{intPart, frac} {
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return Decimal{}, fmt.Errorf("%w: %q", ErrSyntax, orig)
			}
		}
	}
	if len(frac) > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %q has %d digits after the point, at most %d", ErrScale, orig, len(frac), MaxScale)
	}
	digits := strings.TrimLeft(intPart+frac, "0")
	if digits == "" {
		return Decimal{scale: uint8(len(frac))}, nil
	}
	if len(digits) > 19 {
		return Decimal{}, fmt.Errorf("%w: %q", ErrOverflow, orig)
	}
	u, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || u > math.MaxInt64 {
		return Decimal{}, fmt.Errorf("%w: %q", ErrOverflow, orig)
	}
	c := int64(u)
	if neg {
		c = -c
	}
	return Decimal{coef: c, scale: uint8(len(frac))}, nil
}

// MustDecimal parses s and panics if it is malformed; for constants and tests.
func MustDecimal(s string) Decimal {
	d, err := ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return d
}

// FromFloat converts a float that came from outside (a feed, a JSON number) at
// the boundary, rounded to scale digits. NaN, Inf and values out of range are
// refused. It reads the float's shortest decimal form, so 0.1 is 0.1.
func FromFloat(f float64, scale uint8, mode Rounding) (Decimal, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Decimal{}, fmt.Errorf("%w: %v", ErrSyntax, f)
	}
	if scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %d", ErrScale, scale)
	}
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(f, 'f', -1, 64))
	if !ok {
		return Decimal{}, fmt.Errorf("%w: %v", ErrSyntax, f)
	}
	n := new(big.Int).Mul(r.Num(), pow10(int(scale)))
	q, err := divRound(n, r.Denom(), mode)
	if err != nil {
		return Decimal{}, err
	}
	if !q.IsInt64() {
		return Decimal{}, fmt.Errorf("%w: %v", ErrOverflow, f)
	}
	return Decimal{coef: q.Int64(), scale: scale}, nil
}

// divRound returns n/d rounded to an integer; d must be positive.
func divRound(n, d *big.Int, mode Rounding) (*big.Int, error) {
	q, r := new(big.Int).QuoRem(n, d, new(big.Int))
	if r.Sign() == 0 {
		return q, nil
	}
	sign := big.NewInt(int64(n.Sign()))
	up := new(big.Int).Add(q, sign)
	switch mode {
	case Exact:
		return nil, ErrInexact
	case Down:
		return q, nil
	case Up:
		return up, nil
	case Floor:
		if n.Sign() < 0 {
			return up, nil
		}
		return q, nil
	case Ceiling:
		if n.Sign() > 0 {
			return up, nil
		}
		return q, nil
	case HalfEven, HalfUp, HalfDown:
		twice := new(big.Int).Abs(r)
		twice.Lsh(twice, 1)
		switch c := twice.Cmp(d); {
		case c < 0:
			return q, nil
		case c > 0:
			return up, nil
		}
		switch mode {
		case HalfUp:
			return up, nil
		case HalfDown:
			return q, nil
		}
		if q.Bit(0) == 1 {
			return up, nil
		}
		return q, nil
	}
	return nil, fmt.Errorf("money: unknown rounding %d", int(mode))
}

func (d Decimal) big() *big.Int { return big.NewInt(d.coef) }

func fromBig(b *big.Int, scale int) (Decimal, error) {
	if scale < 0 || scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %d", ErrScale, scale)
	}
	if !b.IsInt64() {
		return Decimal{}, ErrOverflow
	}
	return Decimal{coef: b.Int64(), scale: uint8(scale)}, nil
}

// aligned returns both coefficients at the larger of the two scales.
func aligned(a, b Decimal) (x, y *big.Int, scale int) {
	scale = int(a.scale)
	if int(b.scale) > scale {
		scale = int(b.scale)
	}
	x = new(big.Int).Mul(a.big(), pow10(scale-int(a.scale)))
	y = new(big.Int).Mul(b.big(), pow10(scale-int(b.scale)))
	return x, y, scale
}

// Add returns a+b at the larger scale.
func (a Decimal) Add(b Decimal) (Decimal, error) {
	x, y, s := aligned(a, b)
	return fromBig(x.Add(x, y), s)
}

// Sub returns a-b at the larger scale.
func (a Decimal) Sub(b Decimal) (Decimal, error) {
	x, y, s := aligned(a, b)
	return fromBig(x.Sub(x, y), s)
}

// Mul returns a*b rounded to scale digits with the given mode.
func (a Decimal) Mul(b Decimal, scale uint8, mode Rounding) (Decimal, error) {
	if scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %d", ErrScale, scale)
	}
	n := new(big.Int).Mul(a.big(), b.big())
	n.Mul(n, pow10(int(scale)))
	d := pow10(int(a.scale) + int(b.scale))
	q, err := divRound(n, d, mode)
	if err != nil {
		return Decimal{}, err
	}
	return fromBig(q, int(scale))
}

// Div returns a/b rounded to scale digits with the given mode.
func (a Decimal) Div(b Decimal, scale uint8, mode Rounding) (Decimal, error) {
	if b.coef == 0 {
		return Decimal{}, ErrDivZero
	}
	if scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %d", ErrScale, scale)
	}
	n := new(big.Int).Mul(a.big(), pow10(int(b.scale)+int(scale)))
	d := new(big.Int).Mul(b.big(), pow10(int(a.scale)))
	if d.Sign() < 0 {
		n.Neg(n)
		d.Neg(d)
	}
	q, err := divRound(n, d, mode)
	if err != nil {
		return Decimal{}, err
	}
	return fromBig(q, int(scale))
}

// Rescale returns the number at scale digits; fewer digits round with mode.
func (a Decimal) Rescale(scale uint8, mode Rounding) (Decimal, error) {
	if scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %d", ErrScale, scale)
	}
	if scale >= a.scale {
		return fromBig(new(big.Int).Mul(a.big(), pow10(int(scale-a.scale))), int(scale))
	}
	q, err := divRound(a.big(), pow10(int(a.scale-scale)), mode)
	if err != nil {
		return Decimal{}, err
	}
	return fromBig(q, int(scale))
}

// Cmp compares by value: -1, 0 or +1.
func (a Decimal) Cmp(b Decimal) int {
	x, y, _ := aligned(a, b)
	return x.Cmp(y)
}

// Sign is -1, 0 or +1.
func (a Decimal) Sign() int {
	switch {
	case a.coef < 0:
		return -1
	case a.coef > 0:
		return 1
	}
	return 0
}

// IsZero reports whether the value is 0.
func (a Decimal) IsZero() bool { return a.coef == 0 }

// Scale is the number of digits after the point.
func (a Decimal) Scale() int { return int(a.scale) }

// Neg returns -a.
func (a Decimal) Neg() (Decimal, error) {
	if a.coef == math.MinInt64 {
		return Decimal{}, ErrOverflow
	}
	return Decimal{coef: -a.coef, scale: a.scale}, nil
}

// Abs returns |a|.
func (a Decimal) Abs() (Decimal, error) {
	if a.coef < 0 {
		return a.Neg()
	}
	return a, nil
}

// Mod returns the remainder of a/step with the sign of a (a - step*trunc(a/step)),
// exactly. It is zero when a is a whole multiple of step.
func (a Decimal) Mod(step Decimal) (Decimal, error) {
	if step.coef == 0 {
		return Decimal{}, ErrDivZero
	}
	x, y, s := aligned(a, step)
	return fromBig(x.Rem(x, y), s)
}

// String is the plain decimal with exactly Scale digits after the point.
func (a Decimal) String() string {
	u := uint64(a.coef)
	neg := a.coef < 0
	if neg {
		u = -u
	}
	s := strconv.FormatUint(u, 10)
	if a.scale > 0 {
		if len(s) <= int(a.scale) {
			s = strings.Repeat("0", int(a.scale)-len(s)+1) + s
		}
		s = s[:len(s)-int(a.scale)] + "." + s[len(s)-int(a.scale):]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// MarshalText writes the plain decimal, so JSON carries it as a string and no
// float parser ever sees it.
func (a Decimal) MarshalText() ([]byte, error) { return []byte(a.String()), nil }

// UnmarshalText reads a plain decimal.
func (a *Decimal) UnmarshalText(b []byte) error {
	d, err := ParseDecimal(string(b))
	if err != nil {
		return err
	}
	*a = d
	return nil
}
