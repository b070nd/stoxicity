package money

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"math/rand"
	"testing"
)

func dec(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", s, err)
	}
	return d
}

func amt(t *testing.T, s string, c Currency) Amount {
	t.Helper()
	a, err := NewAmount(dec(t, s), c, Exact)
	if err != nil {
		t.Fatalf("NewAmount(%q, %s): %v", s, c, err)
	}
	return a
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", "-", ".", ".5", "5.", "1e3", "1E3", "+1", "1,000", " 1", "1 ", "NaN", "Inf", "0x10", "--1", "1.2.3",
		"9223372036854775808", "1.0000000000000000001"} {
		if d, err := ParseDecimal(s); err == nil {
			t.Errorf("ParseDecimal(%q) = %v, want an error", s, d)
		}
	}
}

func TestParseStringRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		scale := uint8(rng.Intn(MaxScale + 1))
		coef := rng.Int63n(1<<62) - 1<<61
		if i%7 == 0 {
			coef = rng.Int63n(2000) - 1000
		}
		d, err := NewDecimal(coef, scale)
		if err != nil {
			t.Fatal(err)
		}
		back, err := ParseDecimal(d.String())
		if err != nil {
			t.Fatalf("%v: %v", d, err)
		}
		if back != d {
			t.Fatalf("round trip %v -> %v", d, back)
		}
	}
	if got := dec(t, "-0.05").String(); got != "-0.05" {
		t.Errorf("got %s", got)
	}
	if got := dec(t, "1.50").Cmp(dec(t, "1.5")); got != 0 {
		t.Errorf("1.50 and 1.5 compare %d", got)
	}
}

func TestMinInt64Edges(t *testing.T) {
	d := Decimal{coef: math.MinInt64}
	if _, err := d.Neg(); !errors.Is(err, ErrOverflow) {
		t.Errorf("Neg of the minimum: %v", err)
	}
	if got := d.String(); got != "-9223372036854775808" {
		t.Errorf("String of the minimum: %s", got)
	}
	if _, err := dec(t, "9223372036854775807").Add(dec(t, "1")); !errors.Is(err, ErrOverflow) {
		t.Errorf("Add overflow: %v", err)
	}
}

func TestAddSubProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	small := func() Decimal {
		return Decimal{coef: rng.Int63n(2_000_000_000) - 1_000_000_000, scale: uint8(rng.Intn(9))}
	}
	for i := 0; i < 20000; i++ {
		a, b, c := small(), small(), small()
		ab, _ := a.Add(b)
		ba, _ := b.Add(a)
		if ab.Cmp(ba) != 0 {
			t.Fatalf("not commutative: %v %v", a, b)
		}
		x, _ := ab.Add(c)
		bc, _ := b.Add(c)
		y, _ := a.Add(bc)
		if x.Cmp(y) != 0 {
			t.Fatalf("not associative: %v %v %v", a, b, c)
		}
		back, _ := ab.Sub(b)
		if back.Cmp(a) != 0 {
			t.Fatalf("(a+b)-b != a: %v %v", a, b)
		}
	}
}

func TestRoundingModes(t *testing.T) {
	type row struct {
		in   string
		want map[Rounding]string
	}
	rows := []row{
		{"2.5", map[Rounding]string{Down: "2", Up: "3", Floor: "2", Ceiling: "3", HalfEven: "2", HalfUp: "3", HalfDown: "2"}},
		{"3.5", map[Rounding]string{Down: "3", Up: "4", Floor: "3", Ceiling: "4", HalfEven: "4", HalfUp: "4", HalfDown: "3"}},
		{"-2.5", map[Rounding]string{Down: "-2", Up: "-3", Floor: "-3", Ceiling: "-2", HalfEven: "-2", HalfUp: "-3", HalfDown: "-2"}},
		{"-2.4", map[Rounding]string{Down: "-2", Up: "-3", Floor: "-3", Ceiling: "-2", HalfEven: "-2", HalfUp: "-2", HalfDown: "-2"}},
		{"2.6", map[Rounding]string{Down: "2", Up: "3", Floor: "2", Ceiling: "3", HalfEven: "3", HalfUp: "3", HalfDown: "3"}},
		{"2.0", map[Rounding]string{Down: "2", Up: "2", Floor: "2", Ceiling: "2", HalfEven: "2", HalfUp: "2", HalfDown: "2", Exact: "2"}},
	}
	for _, r := range rows {
		for mode, want := range r.want {
			got, err := dec(t, r.in).Rescale(0, mode)
			if err != nil || got.String() != want {
				t.Errorf("%s with %v = %v, %v; want %s", r.in, mode, got, err, want)
			}
		}
	}
	if _, err := dec(t, "2.5").Rescale(0, Exact); !errors.Is(err, ErrInexact) {
		t.Errorf("Exact must refuse to lose a digit: %v", err)
	}
}

func TestMulDivAgainstBigRat(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 20000; i++ {
		a := Decimal{coef: rng.Int63n(2_000_000) - 1_000_000, scale: uint8(rng.Intn(7))}
		b := Decimal{coef: rng.Int63n(2_000_000) - 1_000_000, scale: uint8(rng.Intn(7))}
		scale := uint8(rng.Intn(9))
		got, err := a.Mul(b, scale, HalfEven)
		want := new(big.Rat).Mul(ratOf(a), ratOf(b))
		if errors.Is(err, ErrOverflow) {
			if scaled := new(big.Int).Div(new(big.Int).Mul(want.Num(), pow10(int(scale))), want.Denom()); scaled.IsInt64() {
				t.Fatalf("%v*%v at %d reported an overflow but fits", a, b, scale)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if diff := new(big.Rat).Sub(want, ratOf(got)); new(big.Rat).Abs(diff).Cmp(halfUnit(scale)) > 0 {
			t.Fatalf("%v*%v at %d = %v", a, b, scale, got)
		}
		if b.IsZero() {
			if _, err := a.Div(b, scale, HalfEven); !errors.Is(err, ErrDivZero) {
				t.Fatalf("division by zero: %v", err)
			}
			continue
		}
		q, err := a.Div(b, scale, HalfEven)
		wantQ := new(big.Rat).Quo(ratOf(a), ratOf(b))
		if errors.Is(err, ErrOverflow) {
			if scaled := new(big.Int).Div(new(big.Int).Mul(wantQ.Num(), pow10(int(scale))), wantQ.Denom()); scaled.IsInt64() {
				t.Fatalf("%v/%v at %d reported an overflow but fits", a, b, scale)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if diff := new(big.Rat).Sub(wantQ, ratOf(q)); new(big.Rat).Abs(diff).Cmp(halfUnit(scale)) > 0 {
			t.Fatalf("%v/%v at %d = %v", a, b, scale, q)
		}
	}
}

func ratOf(d Decimal) *big.Rat {
	return new(big.Rat).SetFrac(big.NewInt(d.coef), pow10(int(d.scale)))
}

func halfUnit(scale uint8) *big.Rat {
	return new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Mul(big.NewInt(2), pow10(int(scale))))
}

func TestFromFloatAtTheBoundary(t *testing.T) {
	if d, err := FromFloat(0.1, 4, Exact); err != nil || d.String() != "0.1000" {
		t.Errorf("0.1 -> %v, %v", d, err)
	}
	if d, err := FromFloat(2.675, 2, HalfUp); err != nil || d.String() != "2.68" {
		t.Errorf("2.675 half-up -> %v, %v: the float's own shortest form is read", d, err)
	}
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e30, -1e30} {
		if d, err := FromFloat(f, 2, HalfEven); err == nil {
			t.Errorf("FromFloat(%v) = %v, want an error", f, d)
		}
	}
	if _, err := FromFloat(1.005, 2, Exact); !errors.Is(err, ErrInexact) {
		t.Errorf("Exact must refuse 1.005 at 2 digits: %v", err)
	}
}

func TestAmountRoundingAndCurrencyDigits(t *testing.T) {
	if _, err := NewAmount(dec(t, "10.005"), "EUR", Exact); !errors.Is(err, ErrInexact) {
		t.Errorf("EUR has two digits: %v", err)
	}
	if a, err := NewAmount(dec(t, "10.005"), "EUR", HalfEven); err != nil || a.String() != "10.00 EUR" {
		t.Errorf("half-even tie to the even cent: %v, %v", a, err)
	}
	if a, err := NewAmount(dec(t, "1234.5"), "JPY", HalfUp); err != nil || a.String() != "1235 JPY" {
		t.Errorf("JPY has no minor unit: %v, %v", a, err)
	}
	if _, err := NewAmount(dec(t, "1"), "XYZ", Exact); !errors.Is(err, ErrCurrency) {
		t.Errorf("an unknown currency is not assumed to have two digits: %v", err)
	}
	if _, err := ParseCurrency("eur"); err == nil {
		t.Error("a currency in lower case must be refused")
	}
}

func TestAmountArithmeticGuards(t *testing.T) {
	e := amt(t, "10.00", "EUR")
	u := amt(t, "10.00", "USD")
	if _, err := e.Add(u); !errors.Is(err, ErrCurrency) {
		t.Errorf("EUR+USD: %v", err)
	}
	if _, err := e.Cmp(u); !errors.Is(err, ErrCurrency) {
		t.Errorf("EUR cmp USD: %v", err)
	}
	if _, err := (Amount{}).Add(e); !errors.Is(err, ErrCurrency) {
		t.Errorf("the zero Amount has no currency: %v", err)
	}
	max, _ := FromMinor(math.MaxInt64, "EUR")
	one, _ := FromMinor(1, "EUR")
	if _, err := max.Add(one); !errors.Is(err, ErrOverflow) {
		t.Errorf("overflow: %v", err)
	}
	min, _ := FromMinor(math.MinInt64, "EUR")
	if _, err := min.Neg(); !errors.Is(err, ErrOverflow) {
		t.Errorf("neg overflow: %v", err)
	}
	if _, err := e.Sub(min); err == nil {
		t.Error("subtracting the minimum overflows")
	}
}

func TestAmountJSON(t *testing.T) {
	a := amt(t, "-12.30", "EUR")
	b, err := json.Marshal(a)
	if err != nil || string(b) != `{"amount":"-12.30","currency":"EUR"}` {
		t.Fatalf("%s, %v", b, err)
	}
	var back Amount
	if err := json.Unmarshal(b, &back); err != nil || back != a {
		t.Fatalf("%v, %v", back, err)
	}
	if err := json.Unmarshal([]byte(`{"amount":"1.234","currency":"EUR"}`), &back); err == nil {
		t.Error("a third digit on a EUR amount must be refused")
	}
	if err := json.Unmarshal([]byte(`{"amount":1.5,"currency":"EUR"}`), &back); err == nil {
		t.Error("a JSON number must not be accepted where a string is required")
	}
	if _, err := json.Marshal(Amount{}); err == nil {
		t.Error("an amount without a currency must not be written")
	}
}

func TestAllocateAddsUpExactly(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 20000; i++ {
		total, _ := FromMinor(rng.Int63n(2_000_001)-1_000_000, "EUR")
		n := 1 + rng.Intn(8)
		w := make([]int64, n)
		var sum int64
		for k := range w {
			w[k] = rng.Int63n(1000)
			sum += w[k]
		}
		if sum == 0 {
			w[0] = 1
			sum = 1
		}
		parts, err := total.Allocate(w)
		if err != nil {
			t.Fatal(err)
		}
		var got int64
		for k, p := range parts {
			got += p.Minor()
			ideal := float64(total.Minor()) * float64(w[k]) / float64(sum)
			if math.Abs(float64(p.Minor())-ideal) >= 1 {
				t.Fatalf("part %d is %d, ideal %.3f", k, p.Minor(), ideal)
			}
			if w[k] == 0 && p.Minor() != 0 {
				t.Fatalf("a zero weight received %d", p.Minor())
			}
		}
		if got != total.Minor() {
			t.Fatalf("parts add up to %d, not %d (weights %v)", got, total.Minor(), w)
		}
	}
	total := amt(t, "0.05", "EUR")
	parts, _ := total.Allocate([]int64{1, 1, 1})
	if parts[0].Minor() != 2 || parts[1].Minor() != 2 || parts[2].Minor() != 1 {
		t.Errorf("a tie goes to the earlier part: %v", parts)
	}
	if _, err := total.Allocate([]int64{0, 0}); err == nil {
		t.Error("all-zero weights")
	}
	if _, err := total.Allocate([]int64{1, -1}); err == nil {
		t.Error("negative weight")
	}
}

func TestBalancedEntries(t *testing.T) {
	buy := []Posting{
		{"cash:EUR", amt(t, "-100.00", "EUR")},
		{"securities:ABC", amt(t, "99.00", "EUR")},
		{"fees:commission", amt(t, "1.00", "EUR")},
	}
	if err := Balanced(buy); err != nil {
		t.Errorf("a balanced buy: %v", err)
	}
	buy[2].Amount = amt(t, "1.01", "EUR")
	if err := Balanced(buy); err == nil {
		t.Error("a one-cent difference must be refused")
	}
	// An FX conversion balances in each currency through a clearing account.
	fx := []Posting{
		{"cash:EUR", amt(t, "-100.00", "EUR")},
		{"fx-clearing:EUR", amt(t, "100.00", "EUR")},
		{"fx-clearing:USD", amt(t, "-108.50", "USD")},
		{"cash:USD", amt(t, "108.50", "USD")},
	}
	if err := Balanced(fx); err != nil {
		t.Errorf("fx entry: %v", err)
	}
	// Without the clearing legs the entry sums to zero across currencies only by
	// accident of the numbers, and must be refused.
	bad := []Posting{{"cash:EUR", amt(t, "-100.00", "EUR")}, {"cash:USD", amt(t, "100.00", "USD")}}
	if err := Balanced(bad); err == nil {
		t.Error("100 EUR against 100 USD does not balance")
	}
	if err := Balanced(buy[:1]); err == nil {
		t.Error("a single posting")
	}
	if err := Balanced([]Posting{{"", amt(t, "1.00", "EUR")}, {"x", amt(t, "-1.00", "EUR")}}); err == nil {
		t.Error("a posting without an account")
	}
}

func TestRateDirectionAndRoundTrip(t *testing.T) {
	r, err := NewRate("EUR", "USD", dec(t, "1.0850"), BasisReference)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Convert(amt(t, "100.00", "EUR"), HalfEven); err != nil || got.String() != "108.50 USD" {
		t.Errorf("EUR to USD: %v, %v", got, err)
	}
	if _, err := r.Convert(amt(t, "100.00", "USD"), HalfEven); !errors.Is(err, ErrDirection) {
		t.Errorf("USD through EUR/USD must be refused, not silently inverted: %v", err)
	}
	if r.Basis.Executable() {
		t.Error("a reference rate is not an executable rate")
	}
	if _, err := NewRate("EUR", "EUR", dec(t, "1"), BasisUser); err == nil {
		t.Error("EUR against EUR")
	}
	if _, err := NewRate("EUR", "USD", dec(t, "0"), BasisUser); err == nil {
		t.Error("a zero rate")
	}
	if _, err := NewRate("EUR", "USD", dec(t, "-1.1"), BasisUser); err == nil {
		t.Error("a negative rate")
	}
	if _, err := NewRate("EUR", "USD", dec(t, "1.1"), ""); err == nil {
		t.Error("a rate without a basis")
	}

	// There and back never drifts by more than one minor unit for rates from
	// 0.5 to 5, because each rounding is at most half a unit.
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 20000; i++ {
		v := Decimal{coef: 500_000 + rng.Int63n(4_500_001), scale: 6}
		fwd, _ := NewRate("EUR", "USD", v, BasisReference)
		back, err := fwd.Invert(12, HalfEven)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := FromMinor(rng.Int63n(100_000_000), "EUR")
		there, err := fwd.Convert(a, HalfEven)
		if err != nil {
			t.Fatal(err)
		}
		home, err := back.Convert(there, HalfEven)
		if err != nil {
			t.Fatal(err)
		}
		if d := home.Minor() - a.Minor(); d < -1 || d > 1 {
			t.Fatalf("%v at %v came back as %v", a, v, home)
		}
	}
}

func TestPenceAgainstPounds(t *testing.T) {
	gbp, err := ParseQuoteUnit("GBP")
	if err != nil {
		t.Fatal(err)
	}
	gbx, err := ParseQuoteUnit("GBp")
	if err != nil {
		t.Fatal(err)
	}
	if gbp == gbx || !gbx.IsSubunit() || gbp.IsSubunit() {
		t.Fatalf("GBP and GBp are different units: %+v %+v", gbp, gbx)
	}
	if _, err := ParseQuoteUnit("gbp"); err == nil {
		t.Error("the case is never folded")
	}
	p, err := NewPrice(dec(t, "1234.5"), gbx)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := p.Major(); err != nil || m.String() != "12.345" {
		t.Errorf("1234.5 pence is 12.345 pounds: %v, %v", m, err)
	}
	n, err := Notional(p, dec(t, "100"), Exact)
	if err != nil || n.String() != "1234.50 GBP" {
		t.Errorf("100 shares at 1234.5p: %v, %v (a 100x error would give 123450.00)", n, err)
	}
	wrong, _ := NewPrice(dec(t, "1234.5"), gbp)
	if n2, _ := Notional(wrong, dec(t, "100"), Exact); n2.Minor() != n.Minor()*100 {
		t.Errorf("the same number in pounds is 100 times as much: %v", n2)
	}
	if n, err := Notional(p, dec(t, "3"), Exact); !errors.Is(err, ErrInexact) {
		t.Errorf("3 shares at 12.345 pounds is 37.035: an exact conversion must refuse it, got %v, %v", n, err)
	}
	if n, err := Notional(p, dec(t, "3"), HalfEven); err != nil || n.String() != "37.04 GBP" {
		t.Errorf("rounded: %v, %v", n, err)
	}
	if _, err := NewPrice(dec(t, "-1"), gbp); err == nil {
		t.Error("a negative price")
	}
	if _, err := Notional(p, dec(t, "-1"), Exact); err == nil {
		t.Error("a negative quantity")
	}
}

func TestFractionalQuantities(t *testing.T) {
	r := QtyRules{Step: dec(t, "0.001"), Min: dec(t, "0.01")}
	for _, ok := range []string{"0.01", "0.011", "5", "1234.567"} {
		if err := r.Check(dec(t, ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"0", "-1", "0.009", "0.0105", "1.0001"} {
		if err := r.Check(dec(t, bad)); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	if got, err := r.Fit(dec(t, "1.23456"), Down); err != nil || got.String() != "1.234" {
		t.Errorf("fit down: %v, %v", got, err)
	}
	whole := QtyRules{Step: dec(t, "1"), Min: dec(t, "1")}
	if got, _ := whole.Fit(dec(t, "7.9"), Down); got.String() != "7" {
		t.Errorf("whole shares: %v", got)
	}
	if err := (QtyRules{}).Check(dec(t, "1")); err == nil {
		t.Error("rules without a step")
	}
}

func TestTickGrid(t *testing.T) {
	// Three bands in the style of a European venue: finer ticks for cheaper prices.
	tt := TickTable{
		{From: dec(t, "0"), Tick: dec(t, "0.0001")},
		{From: dec(t, "1"), Tick: dec(t, "0.001")},
		{From: dec(t, "10"), Tick: dec(t, "0.01")},
	}
	if err := tt.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"0.5001", "1.234", "9.999", "10", "10.01", "250.25"} {
		if err := tt.Valid(dec(t, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"0.50001", "1.2345", "10.005", "0", "-1"} {
		if err := tt.Valid(dec(t, p)); err == nil {
			t.Errorf("%s must be off the grid", p)
		}
	}
	if got, err := tt.Snap(dec(t, "10.005"), Floor); err != nil || got.String() != "10.00" {
		t.Errorf("buy limit floors: %v, %v", got, err)
	}
	if got, err := tt.Snap(dec(t, "10.005"), Ceiling); err != nil || got.String() != "10.01" {
		t.Errorf("sell limit ceils: %v, %v", got, err)
	}
	// 9.9995 ceils to 10.000 in the 0.001 band, which is a valid price in the next band.
	if got, err := tt.Snap(dec(t, "9.9995"), Ceiling); err != nil || tt.Valid(got) != nil {
		t.Errorf("across a band edge: %v, %v", got, err)
	}
	rng := rand.New(rand.NewSource(6))
	for i := 0; i < 5000; i++ {
		p := Decimal{coef: 1 + rng.Int63n(5_000_000), scale: 5}
		for _, mode := range []Rounding{Floor, Ceiling, HalfEven} {
			got, err := tt.Snap(p, mode)
			if err != nil || tt.Valid(got) != nil {
				t.Fatalf("%v with %v -> %v, %v", p, mode, got, err)
			}
			again, _ := tt.Snap(got, mode)
			if again.Cmp(got) != 0 {
				t.Fatalf("snapping is not idempotent: %v -> %v -> %v", p, got, again)
			}
		}
	}
	if err := (TickTable{{From: dec(t, "1"), Tick: dec(t, "0.01")}}).Validate(); err == nil {
		t.Error("the first band must start at 0")
	}
	if err := (TickTable{{From: dec(t, "0"), Tick: dec(t, "0.01")}, {From: dec(t, "0"), Tick: dec(t, "0.1")}}).Validate(); err == nil {
		t.Error("bands must ascend")
	}
}

func TestAmountText(t *testing.T) {
	a, err := ParseAmount("1000.00 EUR")
	if err != nil || a.Minor() != 100000 || a.Currency() != "EUR" {
		t.Fatalf("%v, %v", a, err)
	}
	b, _ := a.MarshalText()
	if string(b) != "1000.00 EUR" {
		t.Errorf("%s", b)
	}
	for _, bad := range []string{"", "1000", "1000.00", "1000.00 eur", "1000.00 XYZ", "1000.001 EUR", "1,000 EUR", "1000.00  EUR", "1000.00 EUR extra", "x EUR"} {
		if v, err := ParseAmount(bad); err == nil {
			t.Errorf("ParseAmount(%q) = %v, want an error", bad, v)
		}
	}
	if !(Amount{}).IsZero() {
		t.Error("the zero value is zero")
	}
	if z, _ := ParseAmount("0.00 EUR"); z.IsZero() {
		t.Error("0.00 EUR has a currency and is not the zero value")
	}
}
