package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
)

// ErrCurrency is a currency mismatch, an unknown currency or a missing one.
var ErrCurrency = errors.New("money: currency mismatch")

// Amount is an exact sum of money: whole minor units (cents) of one currency.
// The zero Amount has no currency and is not valid; build one with NewAmount.
type Amount struct {
	cur   Currency
	minor int64
}

// NewAmount rounds d to the currency's minor unit with the given mode. Use Exact
// to refuse a value that has digits below the minor unit.
func NewAmount(d Decimal, cur Currency, mode Rounding) (Amount, error) {
	e, err := cur.Exponent()
	if err != nil {
		return Amount{}, fmt.Errorf("%w: %v", ErrCurrency, err)
	}
	r, err := d.Rescale(uint8(e), mode)
	if err != nil {
		return Amount{}, err
	}
	return Amount{cur: cur, minor: r.coef}, nil
}

// FromMinor builds an Amount from whole minor units.
func FromMinor(minor int64, cur Currency) (Amount, error) {
	if !cur.Valid() {
		return Amount{}, fmt.Errorf("%w: unknown currency %q", ErrCurrency, string(cur))
	}
	return Amount{cur: cur, minor: minor}, nil
}

// Currency of the amount.
func (a Amount) Currency() Currency { return a.cur }

// Minor is the amount in whole minor units.
func (a Amount) Minor() int64 { return a.minor }

// Sign is -1, 0 or +1.
func (a Amount) Sign() int { return Decimal{coef: a.minor}.Sign() }

// Valid reports whether the amount has a currency.
func (a Amount) Valid() bool { return a.cur.Valid() }

// Decimal is the amount in major units, with the currency's number of digits.
func (a Amount) Decimal() Decimal {
	e, _ := a.cur.Exponent()
	return Decimal{coef: a.minor, scale: uint8(e)}
}

func (a Amount) same(b Amount) error {
	if !a.Valid() || !b.Valid() {
		return fmt.Errorf("%w: an amount without a currency", ErrCurrency)
	}
	if a.cur != b.cur {
		return fmt.Errorf("%w: %s and %s", ErrCurrency, a.cur, b.cur)
	}
	return nil
}

// Add returns a+b; the currencies must be equal.
func (a Amount) Add(b Amount) (Amount, error) {
	if err := a.same(b); err != nil {
		return Amount{}, err
	}
	s := a.minor + b.minor
	if (b.minor > 0 && s < a.minor) || (b.minor < 0 && s > a.minor) {
		return Amount{}, ErrOverflow
	}
	return Amount{cur: a.cur, minor: s}, nil
}

// Sub returns a-b; the currencies must be equal.
func (a Amount) Sub(b Amount) (Amount, error) {
	n, err := b.Neg()
	if err != nil {
		return Amount{}, err
	}
	if err := a.same(b); err != nil {
		return Amount{}, err
	}
	return a.Add(n)
}

// Neg returns -a.
func (a Amount) Neg() (Amount, error) {
	if a.minor == math.MinInt64 {
		return Amount{}, ErrOverflow
	}
	return Amount{cur: a.cur, minor: -a.minor}, nil
}

// Cmp compares two amounts of the same currency.
func (a Amount) Cmp(b Amount) (int, error) {
	if err := a.same(b); err != nil {
		return 0, err
	}
	switch {
	case a.minor < b.minor:
		return -1, nil
	case a.minor > b.minor:
		return 1, nil
	}
	return 0, nil
}

// Times multiplies by a factor (a fee rate, a quantity) and rounds the result
// to the minor unit with mode.
func (a Amount) Times(f Decimal, mode Rounding) (Amount, error) {
	if !a.Valid() {
		return Amount{}, fmt.Errorf("%w: an amount without a currency", ErrCurrency)
	}
	e, _ := a.cur.Exponent()
	d, err := a.Decimal().Mul(f, uint8(e), mode)
	if err != nil {
		return Amount{}, err
	}
	return Amount{cur: a.cur, minor: d.coef}, nil
}

// Allocate splits the amount in proportion to the weights so that the parts add
// up to the amount exactly: the minor units left over go, one each, to the parts
// with the largest remainders (the earlier part on a tie). Weights must not be
// negative and must not all be zero.
func (a Amount) Allocate(weights []int64) ([]Amount, error) {
	if !a.Valid() {
		return nil, fmt.Errorf("%w: an amount without a currency", ErrCurrency)
	}
	if len(weights) == 0 {
		return nil, errors.New("money: allocate: no weights")
	}
	total := new(big.Int)
	for _, w := range weights {
		if w < 0 {
			return nil, errors.New("money: allocate: negative weight")
		}
		total.Add(total, big.NewInt(w))
	}
	if total.Sign() == 0 {
		return nil, errors.New("money: allocate: all weights are zero")
	}
	abs := new(big.Int).Abs(big.NewInt(a.minor))
	type part struct {
		q   *big.Int
		rem *big.Int
		idx int
	}
	parts := make([]part, len(weights))
	given := new(big.Int)
	for i, w := range weights {
		q, r := new(big.Int).QuoRem(new(big.Int).Mul(abs, big.NewInt(w)), total, new(big.Int))
		parts[i] = part{q: q, rem: r, idx: i}
		given.Add(given, q)
	}
	left := new(big.Int).Sub(abs, given).Int64()
	order := make([]int, len(parts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(x, y int) bool {
		return parts[order[x]].rem.Cmp(parts[order[y]].rem) > 0
	})
	for k := int64(0); k < left; k++ {
		parts[order[k]].q.Add(parts[order[k]].q, big.NewInt(1))
	}
	out := make([]Amount, len(parts))
	for i, p := range parts {
		m := p.q.Int64()
		if a.minor < 0 {
			m = -m
		}
		out[i] = Amount{cur: a.cur, minor: m}
	}
	return out, nil
}

// ParseAmount reads "12.34 EUR": a plain decimal with at most the currency's
// digits, one space, and a listed currency in capitals.
func ParseAmount(s string) (Amount, error) {
	num, cur, ok := strings.Cut(s, " ")
	if !ok || strings.Contains(cur, " ") {
		return Amount{}, fmt.Errorf("money: amount %q must be a number, a space and a currency", s)
	}
	c, err := ParseCurrency(cur)
	if err != nil {
		return Amount{}, err
	}
	d, err := ParseDecimal(num)
	if err != nil {
		return Amount{}, err
	}
	return NewAmount(d, c, Exact)
}

// MarshalText writes "12.34 EUR", the form ParseAmount reads, so YAML and other
// text formats carry an amount as one readable scalar. JSON uses MarshalJSON.
func (a Amount) MarshalText() ([]byte, error) {
	if !a.Valid() {
		return nil, fmt.Errorf("%w: an amount without a currency", ErrCurrency)
	}
	return []byte(a.String()), nil
}

// UnmarshalText reads what MarshalText writes.
func (a *Amount) UnmarshalText(b []byte) error {
	v, err := ParseAmount(string(b))
	if err != nil {
		return err
	}
	*a = v
	return nil
}

// IsZero reports the zero value (no currency, nothing), for YAML's omitempty. An
// amount of 0.00 EUR has a currency and is not the zero value.
func (a Amount) IsZero() bool { return a.cur == "" && a.minor == 0 }

// String is "12.34 EUR".
func (a Amount) String() string {
	if !a.Valid() {
		return "(no currency)"
	}
	return a.Decimal().String() + " " + string(a.cur)
}

type amountJSON struct {
	Amount   string   `json:"amount"`
	Currency Currency `json:"currency"`
}

// MarshalJSON writes {"amount":"12.34","currency":"EUR"}.
func (a Amount) MarshalJSON() ([]byte, error) {
	if !a.Valid() {
		return nil, fmt.Errorf("%w: an amount without a currency", ErrCurrency)
	}
	return json.Marshal(amountJSON{Amount: a.Decimal().String(), Currency: a.cur})
}

// UnmarshalJSON reads what MarshalJSON writes. The amount may not have more
// digits than the currency's minor unit.
func (a *Amount) UnmarshalJSON(b []byte) error {
	var j amountJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	d, err := ParseDecimal(j.Amount)
	if err != nil {
		return err
	}
	v, err := NewAmount(d, j.Currency, Exact)
	if err != nil {
		return err
	}
	*a = v
	return nil
}

// Posting is one line of a journal entry: an amount posted to an account. A
// debit is positive and a credit negative, so a balanced entry sums to zero.
type Posting struct {
	Account string
	Amount  Amount
}

// Balanced checks that the postings of one entry are valid and sum to zero in
// every currency on their own. An FX conversion therefore needs a clearing
// account that takes the leg in each currency.
func Balanced(entry []Posting) error {
	if len(entry) < 2 {
		return errors.New("money: an entry needs at least two postings")
	}
	sums := map[Currency]*big.Int{}
	for i, p := range entry {
		if p.Account == "" {
			return fmt.Errorf("money: posting %d has no account", i)
		}
		if !p.Amount.Valid() {
			return fmt.Errorf("%w: posting %d has no currency", ErrCurrency, i)
		}
		s := sums[p.Amount.cur]
		if s == nil {
			s = new(big.Int)
			sums[p.Amount.cur] = s
		}
		s.Add(s, big.NewInt(p.Amount.minor))
	}
	var bad []string
	for c, s := range sums {
		if s.Sign() != 0 {
			bad = append(bad, fmt.Sprintf("%s off by %s minor units", c, s))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("money: entry does not balance: %v", bad)
	}
	return nil
}
