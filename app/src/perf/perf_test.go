package perf

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

func d(s string) money.Decimal { return money.MustDecimal(s) }

func ev(day string) time.Time {
	t, _ := time.Parse(time.DateOnly, day)
	return t.Add(12 * time.Hour)
}

type fixture struct {
	t    *testing.T
	l    *portfolio.Ledger
	n    int
	book portfolio.Book
}

func newFixture(t *testing.T, base money.Currency) *fixture {
	t.Helper()
	b := portfolio.Book{ID: "b1", Account: "acct", Name: "main", Env: portfolio.Real, Base: base, Method: portfolio.FIFO, AllowNegativeCash: true}
	l, err := portfolio.NewLedger(b)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, l: l, book: b}
}

func (f *fixture) post(tx portfolio.Tx, err error) portfolio.Tx {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
	f.n++
	p, err := f.l.Post(tx, time.Date(2030, 1, 1, 0, 0, f.n, 0, time.UTC))
	if err != nil {
		f.t.Fatal(err)
	}
	return p.Tx
}

func amt(minor int64, cur money.Currency) money.Amount { a, _ := money.FromMinor(minor, cur); return a }

func (f *fixture) deposit(day string, minor int64, cur money.Currency) portfolio.Tx {
	return f.post(f.l.Deposit(portfolio.Common{Event: ev(day), Source: "t"}, amt(minor, cur)))
}
func (f *fixture) withdraw(day string, minor int64, cur money.Currency) portfolio.Tx {
	return f.post(f.l.Withdraw(portfolio.Common{Event: ev(day), Source: "t"}, amt(minor, cur)))
}
func (f *fixture) buy(day, sec, qty string, gross, commission int64, cur money.Currency) portfolio.Tx {
	t := portfolio.Trade{Common: portfolio.Common{Event: ev(day), Source: "t"}, Security: sec, Qty: d(qty), Gross: amt(gross, cur)}
	if commission != 0 {
		t.Commission = amt(commission, cur)
	}
	return f.post(f.l.Buy(t))
}
func (f *fixture) income(day string, gross, withheld int64, cur money.Currency) portfolio.Tx {
	return f.post(f.l.Income(portfolio.Common{Event: ev(day), Source: "t"}, "dividend", amt(gross, cur), amt(withheld, cur)))
}

func mark(sec, day, price string, cur money.Currency) Mark {
	return Mark{Security: sec, Day: day, Price: d(price), Currency: cur}
}

func (f *fixture) inputs(from, to string, marks []Mark, rates ...FXRate) Inputs {
	return Inputs{Book: f.book, Txs: f.l.Txs(), Marks: marks, Rates: rates, From: from, To: to}
}

func mustCompute(t *testing.T, in Inputs) Report {
	t.Helper()
	r, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: %.8f, want %.8f", what, got, want)
	}
}

func TestGainSeparatesFlowsFromIncomeCostsAndPriceChange(t *testing.T) {
	f := newFixture(t, "EUR")
	f.deposit("2026-01-05", 1000000, "EUR")
	f.buy("2026-01-06", "sec_a", "100", 500000, 500, "EUR")
	f.deposit("2026-01-08", 100000, "EUR")
	f.income("2026-01-09", 10000, 1500, "EUR")
	marks := []Mark{mark("sec_a", "2026-01-06", "50", "EUR"), mark("sec_a", "2026-01-07", "55", "EUR"), mark("sec_a", "2026-01-08", "55", "EUR"), mark("sec_a", "2026-01-09", "54", "EUR")}
	r := mustCompute(t, f.inputs("", "2026-01-09", marks))

	if r.Gain == nil || r.Gain.Minor() != 48000 {
		t.Fatalf("gain %v: 11,480.00 end value less 11,000.00 paid in is 480.00", r.Gain)
	}
	if r.Contributions.Minor() != 1100000 || r.Withdrawals.Minor() != 0 || r.End.Minor() != 1148000 {
		t.Fatalf("flows %v %v end %v", r.Contributions, r.Withdrawals, r.End)
	}
	c := r.Components
	if c == nil || c.Unrealized != 40000 || c.IncomeGross != 10000 || c.Withholding != 1500 || c.Costs != 500 || c.Realized != 0 || c.FX != 0 {
		t.Fatalf("components %+v", c)
	}
	if c.Realized+c.Unrealized+c.IncomeGross-c.Withholding-c.Costs != r.Gain.Minor() {
		t.Fatal("the components do not add up to the gain")
	}
	// time-weighted: the deposit of day 4 is not return
	want := (9995.0 / 10000) * (10495.0 / 9995) * (10495.0 / 10495) * (11480.0 / 11495)
	if !r.TWR.Available {
		t.Fatal(r.TWR.Reason)
	}
	near(t, "twr", r.TWR.Value, want-1, 1e-12)
	if r.TWR.Annualised != nil {
		t.Fatal("a period of days is not annualised")
	}
}

func TestAnExDateDropOffsetByIncomeIsNotAReturnAndNotCountedTwice(t *testing.T) {
	f := newFixture(t, "EUR")
	f.deposit("2026-01-05", 500000, "EUR")
	f.buy("2026-01-05", "sec_a", "100", 500000, 0, "EUR")
	f.income("2026-01-06", 10000, 0, "EUR")
	marks := []Mark{mark("sec_a", "2026-01-05", "50", "EUR"), mark("sec_a", "2026-01-06", "49", "EUR")}
	r := mustCompute(t, f.inputs("", "2026-01-06", marks))
	if r.Gain.Minor() != 0 {
		t.Fatalf("the price fell by what was paid out: %v", r.Gain)
	}
	if !r.TWR.Available || math.Abs(r.TWR.Value) > 1e-12 {
		t.Fatalf("%+v", r.TWR)
	}
	if r.Components.IncomeGross != 10000 || r.Components.Unrealized != -10000 {
		t.Fatalf("%+v", r.Components)
	}
}

func TestMissingMarksMakeValuesUnavailableAndStaleMarksAreListed(t *testing.T) {
	f := newFixture(t, "EUR")
	f.deposit("2026-01-05", 1000000, "EUR")
	f.buy("2026-01-06", "sec_a", "10", 500000, 0, "EUR")

	r := mustCompute(t, f.inputs("", "2026-01-07", nil))
	if r.Gain != nil || !strings.Contains(r.GainReason, "no price for sec_a") {
		t.Fatalf("%v %q", r.Gain, r.GainReason)
	}
	if r.TWR.Available || r.MWR.Available || !strings.Contains(r.TWR.Reason, "no value on 2026-01-06") {
		t.Fatalf("nothing is made up for a day without a price: %+v %+v", r.TWR, r.MWR)
	}

	// a price from 12 days before is used, and said to be stale
	r = mustCompute(t, f.inputs("", "2026-01-18", []Mark{mark("sec_a", "2026-01-06", "50", "EUR")}))
	if r.Gain == nil || len(r.StaleMarks) == 0 || !strings.Contains(r.StaleMarks[0], "days old") {
		t.Fatalf("%v %v", r.Gain, r.StaleMarks)
	}
}

func TestASplitChangesQuantityNotValue(t *testing.T) {
	f := newFixture(t, "EUR")
	f.deposit("2026-01-05", 100000, "EUR")
	f.buy("2026-01-05", "sec_a", "10", 100000, 0, "EUR")
	f.post(f.l.SplitShares(portfolio.Common{Event: ev("2026-01-07"), Source: "t"}, "sec_a", 1, 2))
	marks := []Mark{mark("sec_a", "2026-01-05", "100", "EUR"), mark("sec_a", "2026-01-06", "100", "EUR"), mark("sec_a", "2026-01-07", "50", "EUR")}
	r := mustCompute(t, f.inputs("", "2026-01-07", marks))
	if r.Gain.Minor() != 0 || !r.TWR.Available || math.Abs(r.TWR.Value) > 1e-12 {
		t.Fatalf("raw prices and the ledger's split agree: %v %+v", r.Gain, r.TWR)
	}
}

func TestMoneyWeightedReturnPositiveNegativeAndNone(t *testing.T) {
	run := func(endPrice string) Report {
		f := newFixture(t, "EUR")
		f.deposit("2026-01-05", 100000, "EUR")
		f.buy("2026-01-05", "sec_a", "10", 100000, 0, "EUR")
		return mustCompute(t, f.inputs("", "2027-01-05", []Mark{mark("sec_a", "2026-01-05", "100", "EUR"), mark("sec_a", "2027-01-04", endPrice, "EUR")}))
	}
	r := run("110")
	if !r.MWR.Available {
		t.Fatalf("%+v", r.MWR)
	}
	near(t, "mwr", r.MWR.Value, 0.10, 1e-6)
	// the mark is a day old on the last day: not stale, and 365 days are a full year
	if r.TWR.Annualised == nil {
		t.Fatal("a year is annualised")
	}
	r = run("90")
	near(t, "negative mwr", r.MWR.Value, -0.10, 1e-6)

	// everything paid in was lost: the rate has no finite solution
	m := moneyWeighted(dayState{ok: true}, dayState{ok: true}, []int64{0, 1000, 0}, 0, 1, 2, "")
	if m.Available || !strings.Contains(m.Reason, "-100%") {
		t.Fatalf("%+v", m)
	}
	// money in and the same money out again: a rate of zero
	m = moneyWeighted(dayState{ok: true}, dayState{ok: true}, []int64{0, 1000, 0, -1000}, 0, 1, 3, "")
	near(t, "flat", m.Value, 0, 1e-9)
	// money taken out that was never paid in, and a value: the flows never change sign
	m = moneyWeighted(dayState{ok: true}, dayState{ok: true, nav: 3000}, []int64{0, -1000, 0}, 0, 1, 2, "")
	if m.Available || !strings.Contains(m.Reason, "do not change sign") {
		t.Fatalf("%+v", m)
	}
}

func TestSeveralRatesAreReportedNotPickedFrom(t *testing.T) {
	roots := solveIRR([]cashflow{{0, -1000}, {1, 2300}, {2, -1320}})
	if len(roots) != 2 {
		t.Fatalf("%v", roots)
	}
	near(t, "first", roots[0], 0.10, 1e-6)
	near(t, "second", roots[1], 0.20, 1e-6)
}

func TestNegativeOrZeroValuationBaseIsExplicit(t *testing.T) {
	f := newFixture(t, "EUR")
	f.deposit("2026-01-05", 100000, "EUR")
	f.withdraw("2026-01-06", 150000, "EUR") // the book allows negative cash
	r := mustCompute(t, f.inputs("", "2026-01-08", nil))
	if r.TWR.Available || !strings.Contains(r.TWR.Reason, "negative") {
		t.Fatalf("%+v", r.TWR)
	}

	// a book that was emptied and refilled skips the empty days
	g := newFixture(t, "EUR")
	g.deposit("2026-01-05", 100000, "EUR")
	g.withdraw("2026-01-06", 100000, "EUR")
	g.deposit("2026-01-08", 200000, "EUR")
	r = mustCompute(t, g.inputs("", "2026-01-09", nil))
	if !r.TWR.Available || math.Abs(r.TWR.Value) > 1e-12 || len(r.TWR.Notes) == 0 {
		t.Fatalf("%+v", r.TWR)
	}
}

func TestForeignCurrencyEffectIsTheRemainderAndMissingRatesAreExplicit(t *testing.T) {
	f := newFixture(t, "EUR")
	f.deposit("2026-01-05", 100000, "USD")
	rates := []FXRate{{Currency: "USD", Day: "2026-01-05", Value: d("0.90")}, {Currency: "USD", Day: "2026-01-12", Value: d("0.95")}}
	r := mustCompute(t, f.inputs("", "2026-01-12", nil, rates...))
	if r.Gain == nil || r.Gain.Minor() != 5000 || r.Components == nil || r.Components.FX != 5000 {
		t.Fatalf("1,000 USD went from 900 to 950 EUR: %v %+v", r.Gain, r.Components)
	}
	if r.Contributions.Minor() != 90000 {
		t.Fatalf("the deposit counts at the rate of its day: %v", r.Contributions)
	}
	r = mustCompute(t, f.inputs("", "2026-01-12", nil))
	if r.Gain != nil || !strings.Contains(r.GainReason, "no USD rate") {
		t.Fatalf("%v %q", r.Gain, r.GainReason)
	}
}

func TestACorrectionTakesEffectOnTheDayOfTheTransactionItReverses(t *testing.T) {
	f := newFixture(t, "EUR")
	dep := f.deposit("2026-01-05", 100000, "EUR")
	f.post(f.l.Reverse(portfolio.Common{Event: ev("2026-01-20"), Source: "t"}, dep.ID))
	r := mustCompute(t, f.inputs("", "2026-01-21", nil))
	for _, p := range r.Series {
		if p.Flow != 0 || p.Value == nil || *p.Value != 0 {
			t.Fatalf("%+v: the deposit and its reversal are one nothing", p)
		}
	}
	if r.Gain == nil || r.Gain.Minor() != 0 {
		t.Fatalf("%v", r.Gain)
	}
}

func TestDrawdownIsOnTheReturnIndexNotOnDeposits(t *testing.T) {
	f := newFixture(t, "EUR")
	f.deposit("2026-01-05", 100000, "EUR")
	f.buy("2026-01-05", "sec_a", "10", 100000, 0, "EUR")
	f.deposit("2026-01-09", 900000, "EUR") // a big deposit must not look like recovery
	f.buy("2026-01-09", "sec_a", "100", 900000, 0, "EUR")
	marks := []Mark{
		mark("sec_a", "2026-01-05", "100", "EUR"), mark("sec_a", "2026-01-06", "110", "EUR"),
		mark("sec_a", "2026-01-07", "88", "EUR"), mark("sec_a", "2026-01-08", "90", "EUR"),
		mark("sec_a", "2026-01-09", "90", "EUR"), mark("sec_a", "2026-01-12", "112", "EUR"),
	}
	r := mustCompute(t, f.inputs("", "2026-01-12", marks))
	if !r.Drawdown.Available {
		t.Fatal(r.Drawdown.Reason)
	}
	near(t, "max drawdown", r.Drawdown.Max, 88.0/110-1, 1e-9)
	if r.Drawdown.Peak != "2026-01-06" || r.Drawdown.Trough != "2026-01-07" || r.Drawdown.Recovered != "2026-01-12" {
		t.Fatalf("%+v", r.Drawdown)
	}
}

func TestBenchmarkReplaysTheSameFlowsWithStatedCosts(t *testing.T) {
	f := newFixture(t, "EUR")
	f.deposit("2026-01-05", 100000, "EUR")
	f.buy("2026-01-05", "sec_a", "10", 100000, 0, "EUR")
	marks := []Mark{mark("sec_a", "2026-01-05", "100", "EUR"), mark("sec_a", "2026-01-12", "100", "EUR"),
		mark("fund", "2026-01-05", "10", "EUR"), mark("fund", "2026-01-12", "11", "EUR"),
		mark("fundusd", "2026-01-05", "10", "USD"), mark("fundusd", "2026-01-12", "11", "USD")}
	in := f.inputs("", "2026-01-12", marks)
	in.Benchmarks = []Benchmark{
		{Name: "fund gross", Kind: "fund", Security: "fund"},
		{Name: "fund net", Kind: "fund", Security: "fund", TradeCost: d("0.01"), CostKnown: true, CostSource: "my broker's schedule"},
		{Name: "cash", Kind: "cash"},
		{Name: "no data", Kind: "fund", Security: "missing_fund"},
		{Name: "dollars", Kind: "fund", Security: "fundusd", Currency: "USD"},
		{Name: "unsourced", Kind: "fund", Security: "fund", TradeCost: d("0.01"), CostKnown: true},
	}
	r := mustCompute(t, in)
	by := map[string]Result{}
	for _, b := range r.Baselines {
		by[b.Name] = b
	}
	g := by["fund gross"]
	if !g.Available || g.End.Minor() != 110000 || g.Gain.Minor() != 10000 || g.Excess.Minor() != -10000 {
		t.Fatalf("%+v %v", g, g.End)
	}
	if !strings.Contains(strings.Join(g.Notes, " "), "gross of trading costs") {
		t.Fatalf("an unstated cost must be said: %v", g.Notes)
	}
	n := by["fund net"]
	if !n.Available || n.End.Minor() != 108900 {
		t.Fatalf("1,000 less 1%% buys 99 units, worth 1,089: %v", n.End)
	}
	if c := by["cash"]; !c.Available || c.Gain.Minor() != 0 || !strings.Contains(strings.Join(c.Notes, " "), "0%") {
		t.Fatalf("%+v", c)
	}
	if nd := by["no data"]; nd.Available || !strings.Contains(nd.Reason, "no benchmark price for missing_fund") {
		t.Fatalf("%+v", nd)
	}
	if dl := by["dollars"]; dl.Available || !strings.Contains(dl.Reason, "no USD rate") {
		t.Fatalf("a different currency without a rate is unavailable: %+v", dl)
	}
	if u := by["unsourced"]; u.Available || !strings.Contains(u.Reason, "source") {
		t.Fatalf("%+v", u)
	}
}

func TestBenchmarkDistributionsAreReinvestedOnUnitsHeldBeforeTheExDay(t *testing.T) {
	f := newFixture(t, "EUR")
	f.deposit("2026-01-05", 100000, "EUR")
	marks := []Mark{mark("fund", "2026-01-05", "10", "EUR"), mark("fund", "2026-01-12", "9.5", "EUR")}
	in := f.inputs("", "2026-01-12", marks)
	in.Benchmarks = []Benchmark{{Name: "fund", Kind: "fund", Security: "fund", Distributions: []Dist{{ExDay: "2026-01-12", PerUnit: d("0.5")}}}}
	r := mustCompute(t, in)
	b := r.Baselines[0]
	if !b.Available {
		t.Fatal(b.Reason)
	}
	// 100 units, 50 paid out, reinvested at 9.50 = 5.26315789 units: 105.26315789 x 9.50 = 1000.00
	if b.End.Minor() != 100000 || !b.TWR.Available {
		t.Fatalf("%v %+v", b.End, b.TWR)
	}
	near(t, "total return", b.TWR.Value, 0, 1e-12)
}
