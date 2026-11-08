package strategy

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/scorecard"
)

func dec(s string) money.Decimal { return money.MustDecimal(s) }

type opts struct {
	noDebt, debtFree, noDividends, dividendHeavy, negativeFCF, noEbitda bool
	notes                                                               []scorecard.Annotation
}

func datum(metric, year, value string, o ...string) scorecard.Datum {
	d := scorecard.Datum{Security: "ACME", Metric: metric, End: year + "-12-31", Currency: "EUR", Value: dec(value), Source: "annual report", Ref: "AR " + year}
	switch metric {
	case scorecard.Revenue, scorecard.NetIncome, scorecard.OperatingCashFlow, scorecard.Capex, scorecard.OperatingIncome, scorecard.Ebitda, scorecard.InterestExpense, scorecard.DividendsPaid:
		d.Start = year + "-01-01"
	}
	if metric == scorecard.Shares {
		d.Currency = ""
	}
	y := 0
	fmt.Sscan(year, &y)
	d.Filed = fmt.Sprintf("%d-03-01", y+1)
	return d
}

func notes() []scorecard.Annotation {
	at := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	mk := func(topic string) scorecard.Annotation {
		return scorecard.Annotation{Security: "ACME", Topic: topic, Stance: scorecard.Supports, Text: "sourced " + topic + " note", Source: "analyst", Ref: "memo", At: at}
	}
	return []scorecard.Annotation{mk("thesis"), mk("moat"), mk("management")}
}

func card(t *testing.T, o opts) scorecard.Card {
	t.Helper()
	var ds []scorecard.Datum
	for i, y := range []string{"2023", "2024", "2025"} {
		rev := []string{"1000", "1100", "1210"}[i]
		ni := []string{"100", "121", "133"}[i]
		ocf := []string{"200", "220", "240"}[i]
		cx := []string{"50", "50", "60"}[i]
		if o.negativeFCF && y == "2025" {
			ocf = "40"
		}
		ds = append(ds, datum(scorecard.Revenue, y, rev), datum(scorecard.NetIncome, y, ni), datum(scorecard.OperatingCashFlow, y, ocf), datum(scorecard.Capex, y, cx),
			datum(scorecard.Equity, y, "600"), datum(scorecard.OperatingIncome, y, "200"), datum(scorecard.InterestExpense, y, "20"))
	}
	ds = append(ds, datum(scorecard.Shares, "2025", "100"))
	if !o.noEbitda {
		ds = append(ds, datum(scorecard.Ebitda, "2025", "290"))
	}
	if !o.noDebt {
		debt := "400"
		if o.debtFree {
			debt = "0"
		}
		ds = append(ds, datum(scorecard.TotalDebt, "2025", debt), datum(scorecard.Cash, "2025", "100"))
	}
	if !o.noDividends {
		div := "80"
		if o.dividendHeavy {
			div = "170"
		}
		ds = append(ds, datum(scorecard.DividendsPaid, "2025", div))
	}
	n := o.notes
	if n == nil {
		n = notes()
	}
	c, err := scorecard.Build(scorecard.Input{Security: "ACME", Template: "ordinary", Data: ds, Notes: n, KnownAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func payouts(declared bool) []distribution.Payout {
	st := distribution.StateDeclared
	if !declared {
		st = distribution.StateEstimated
	}
	var out []distribution.Payout
	for i, ex := range []string{"2025-02-10", "2025-05-12", "2025-08-11", "2025-11-10", "2026-02-09", "2024-11-11"} {
		out = append(out, distribution.Payout{Key: distribution.Key{SecurityID: "ACME", Type: distribution.Ordinary, Period: fmt.Sprint("p", i)}, State: st, Amount: dec("0.20"), Currency: "EUR", ExDay: ex})
	}
	return out
}

func assumptions() *Assumptions {
	sc := func(n, g, d, t string) Scenario {
		return Scenario{Name: n, Growth: dec(g), Discount: dec(d), Terminal: dec(t)}
	}
	return &Assumptions{Method: MethodOwnerEarningsDCF, Currency: "EUR", Normalization: dec("1"), Years: 10,
		Bear: sc("bear", "0", "0.10", "0.01"), Base: sc("base", "0.03", "0.09", "0.02"), Bull: sc("bull", "0.05", "0.08", "0.025"),
		Source: "my memo", Ref: "memo 2026-02"}
}

func base(t *testing.T, o opts) Input {
	cfg := DefaultConfig()
	cfg.Reviewed = true
	in := Input{Security: "ACME", AsOf: "2026-03-01", Card: card(t, o), Payouts: payouts(true), Assumptions: assumptions(), Config: cfg}
	if o.noDividends {
		in.Payouts = nil
	}
	return in
}

func ceiling(t *testing.T, in Input) money.Decimal {
	t.Helper()
	p, err := Evaluate(in)
	if err != nil || p.Valuation == nil {
		t.Fatalf("%v %+v", err, p.Reasons)
	}
	return p.Valuation.Ceiling
}

func priceAt(c money.Decimal, delta string) *Price {
	v, _ := c.Add(dec(delta))
	return &Price{Value: v, Currency: "EUR", Day: "2026-02-27", Source: "fixture"}
}

func eval(t *testing.T, in Input) Proposal {
	t.Helper()
	p, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValueIsExactAndOrdered(t *testing.T) {
	// zero growth, 10% discount, zero terminal growth: a perpetuity of 10 is worth 100
	v, err := ValuePerShare(dec("10"), 1, Scenario{Name: "x", Growth: dec("0"), Discount: dec("0.10"), Terminal: dec("0")})
	if err != nil || v.String() != "100.0000" {
		t.Fatalf("%v %v", v, err)
	}
	for _, bad := range []Scenario{
		{Name: "x", Discount: dec("0.05"), Terminal: dec("0.05")}, {Name: "x", Discount: dec("0.05"), Terminal: dec("0.06")},
		{Name: "x", Discount: dec("0"), Terminal: dec("0")}, {Name: "x", Growth: dec("0.9"), Discount: dec("0.1")}, {Name: "x", Discount: dec("0.1"), Terminal: dec("-0.01")},
	} {
		if _, err := ValuePerShare(dec("10"), 5, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if _, err := ValuePerShare(dec("-1"), 5, Scenario{Name: "x", Discount: dec("0.1")}); err == nil {
		t.Fatal("negative cash flow")
	}
	a := assumptions()
	val, err := Value(*a, dec("1.8"), dec("100"), "base", dec("0.20"))
	if err != nil || !val.Ordered || len(val.Sensitivity) != 9 {
		t.Fatalf("%v %+v", err, val)
	}
	if val.Bear.Cmp(val.Base) >= 0 || val.Base.Cmp(val.Bull) >= 0 {
		t.Fatalf("%+v", val)
	}
	want, _ := val.Base.Mul(dec("0.8"), 4, money.HalfEven)
	if val.Ceiling.Cmp(want) != 0 {
		t.Fatalf("ceiling %s want %s", val.Ceiling, want)
	}
	// the sensitivity centre is the base value; a higher discount rate is worth less
	var centre, high money.Decimal
	for _, s := range val.Sensitivity {
		if s.DiscountShift.IsZero() && s.TerminalShift.IsZero() {
			centre = s.Value
		}
		if s.DiscountShift.String() == "0.01" && s.TerminalShift.IsZero() {
			high = s.Value
		}
	}
	if centre.Cmp(val.Base) != 0 || high.Cmp(centre) >= 0 {
		t.Fatalf("%s %s %s", centre, high, val.Base)
	}
	// documented inputs only
	b := *a
	b.Source = ""
	if err := b.Validate(); err == nil {
		t.Fatal("undocumented assumptions")
	}
	b = *a
	b.Normalization = dec("0")
	if err := b.Validate(); err == nil {
		t.Fatal("zero normalization")
	}
	if _, err := Value(*a, dec("1.8"), dec("100"), "bull", dec("0.2")); err == nil {
		t.Fatal("entry scenario bull")
	}
	if _, err := Value(*a, dec("1.8"), dec("100"), "base", dec("0.95")); err == nil {
		t.Fatal("margin of safety 95%")
	}
}

func TestBuyOnlyWhenEligibleReviewedAndInsideTheEntryBand(t *testing.T) {
	in := base(t, opts{})
	c := ceiling(t, in)
	in.Price = priceAt(c, "0")
	p := eval(t, in)
	if p.Action != Buy || p.StrategyState != "eligible" || p.DataState != "complete" || p.Score != 100 || p.MaxScore != 100 {
		t.Fatalf("%s %s %s %d/%d %v", p.Action, p.StrategyState, p.DataState, p.Score, p.MaxScore, p.Reasons)
	}
	if !p.IncomePreference || p.NextExDay != "" {
		t.Fatalf("income preference %v next %q", p.IncomePreference, p.NextExDay)
	}
	if p.ExecutionEligibility != "research_only" || p.Inputs.Config == "" || p.Inputs.Assumptions == "" || p.Hash == "" || len(p.Alternatives) != 2 || len(p.Invalidation) < 4 {
		t.Fatalf("%+v", p)
	}
	if p.Valuation.Ceiling.Cmp(c) != 0 || len(p.Thesis) != 3 {
		t.Fatalf("%+v", p.Valuation)
	}
	again := eval(t, in)
	if again.Hash != p.Hash {
		t.Fatal("a proposal is a pure function of its inputs")
	}

	// one cent above the ceiling is outside the band: WATCH, with the specific reason
	in.Price = priceAt(c, "0.01")
	p = eval(t, in)
	if p.Action != Watch || !strings.Contains(strings.Join(p.Reasons, ";"), "outside the entry band") {
		t.Fatalf("%s %v", p.Action, p.Reasons)
	}
	// unreviewed settings cannot go beyond WATCH
	in.Price = priceAt(c, "0")
	in.Config.Reviewed = false
	p = eval(t, in)
	if p.Action != Watch || !strings.Contains(strings.Join(p.Reasons, ";"), "not been reviewed") {
		t.Fatalf("%s %v", p.Action, p.Reasons)
	}
	// holding it already makes a BUY an ADD
	in.Config.Reviewed = true
	in.Held = true
	if p = eval(t, in); p.Action != Add {
		t.Fatalf("%s", p.Action)
	}
	// no price, a stale price, a price in another currency: WAIT / WATCH, never a guess
	in.Held, in.Price = false, nil
	if p = eval(t, in); p.Action != Wait {
		t.Fatalf("%s %v", p.Action, p.Reasons)
	}
	in.Price = &Price{Value: dec("1"), Currency: "EUR", Day: "2026-02-01", Source: "fixture"}
	if p = eval(t, in); p.Action != Wait || !strings.Contains(strings.Join(p.Reasons, ";"), "older than") {
		t.Fatalf("%s %v", p.Action, p.Reasons)
	}
	in.Price = &Price{Value: dec("1"), Currency: "USD", Day: "2026-02-27", Source: "fixture"}
	if p = eval(t, in); p.Action != Watch || !strings.Contains(strings.Join(p.Reasons, ";"), "no rate is applied") {
		t.Fatalf("%s %v", p.Action, p.Reasons)
	}
	// no assumptions: research only, no invented price
	in.Assumptions, in.Price = nil, priceAt(c, "0")
	p = eval(t, in)
	if p.Action != Watch || p.Valuation != nil || !strings.Contains(strings.Join(p.Reasons, ";"), "no valuation") || p.Score != 80 {
		t.Fatalf("%s %d %v", p.Action, p.Score, p.Reasons)
	}
}

func TestNewsVolumeCannotMoveAProposal(t *testing.T) {
	in := base(t, opts{})
	in.Price = priceAt(ceiling(t, in), "0.5")
	a := eval(t, in)
	in.NewsMentions = 100000
	b := eval(t, in)
	if a.Action != b.Action || a.Score != b.Score || len(b.Ignored) != 1 {
		t.Fatalf("%s %s %d %d %v", a.Action, b.Action, a.Score, b.Score, b.Ignored)
	}
	// and a company with no data at all is not rescued by any amount of news
	in.Card = card(t, opts{})
	in.Card.Years = nil
	in.NewsMentions = 100000
	if p := eval(t, in); p.Action != Abstain || p.DataState != "insufficient" {
		t.Fatalf("%s %s", p.Action, p.DataState)
	}
}

func TestMissingRequiredInputsAbstainWithSpecificReasonsAndNoRenormalizing(t *testing.T) {
	in := base(t, opts{noDebt: true, noEbitda: true})
	in.Price = priceAt(ceiling(t, base(t, opts{})), "0")
	p := eval(t, in)
	if p.Action != Abstain || p.StrategyState != "blocked" || p.DataState != "insufficient" {
		t.Fatalf("%s %s %s", p.Action, p.StrategyState, p.DataState)
	}
	reasons := strings.Join(p.Reasons, ";")
	if !strings.Contains(reasons, "unknown, required: net debt / EBITDA") || !strings.Contains(reasons, "total debt is missing") {
		t.Fatalf("%v", p.Reasons)
	}
	// the leverage points are not awarded and the others are not scaled up
	if p.Score != 90 || p.MaxScore != 100 {
		t.Fatalf("%d/%d", p.Score, p.MaxScore)
	}
	if p.Coverage == "1.0000" {
		t.Fatalf("coverage %s must show the gap", p.Coverage)
	}

	// an optional test that is unknown earns nothing but does not block
	in = base(t, opts{notes: notes()[:1]})
	in.Price = priceAt(ceiling(t, in), "0")
	p = eval(t, in)
	if p.Action != Buy || p.Score != 90 || p.DataState != "partial" {
		t.Fatalf("%s %d %s %v", p.Action, p.Score, p.DataState, p.Reasons)
	}
	// without any thesis note the required business-understanding test blocks
	in = base(t, opts{notes: []scorecard.Annotation{}})
	in.Card.Notes = nil
	if p = eval(t, in); p.StrategyState != "blocked" {
		t.Fatalf("%s", p.StrategyState)
	}
}

func TestFailuresMakeTheCompanyIneligible(t *testing.T) {
	in := base(t, opts{negativeFCF: true})
	p := eval(t, in)
	if p.Action != Abstain || p.StrategyState != "ineligible" || !strings.Contains(strings.Join(p.Reasons, ";"), "failed: latest free cash flow is positive") {
		t.Fatalf("%s %s %v", p.Action, p.StrategyState, p.Reasons)
	}
	if p.Valuation != nil {
		t.Fatal("a valuation was made on negative free cash flow")
	}
	// too little history
	in = base(t, opts{})
	in.Card.Years = in.Card.Years[:2]
	if p = eval(t, in); p.StrategyState != "blocked" || !strings.Contains(strings.Join(p.Reasons, ";"), "2 comparable year") {
		t.Fatalf("%s %v", p.StrategyState, p.Reasons)
	}
	// a sourced debt-free exception replaces the leverage division
	in = base(t, opts{debtFree: true, noEbitda: true})
	p = eval(t, in)
	var lev Test
	for _, x := range p.Tests {
		if x.ID == "res.leverage" {
			lev = x
		}
	}
	if lev.Result != Pass || !strings.Contains(lev.Detail, "debt-free") {
		t.Fatalf("%+v", lev)
	}
	// high leverage fails
	in = base(t, opts{})
	for i := range in.Card.Years[0].Raw {
		_ = i
	}
	if hi := leverage(t, "1500"); hi.Result != Fail {
		t.Fatalf("%+v", hi)
	}
}

func leverage(t *testing.T, debt string) Test {
	t.Helper()
	in := base(t, opts{})
	// rebuild the card with a different debt figure
	var ds []scorecard.Datum
	for _, y := range in.Card.Years {
		for m, c := range y.Raw {
			if !c.Has() {
				continue
			}
			d := y.Used[m]
			if m == scorecard.TotalDebt {
				d.Value = dec(debt)
			}
			ds = append(ds, d)
		}
	}
	c, err := scorecard.Build(scorecard.Input{Security: "ACME", Template: "ordinary", Data: ds, KnownAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	in.Card = c
	for _, x := range eval(t, in).Tests {
		if x.ID == "res.leverage" {
			return x
		}
	}
	t.Fatal("no leverage test")
	return Test{}
}

func TestIncomeTestsAndPreference(t *testing.T) {
	// no dividend at all: income tests are not applicable and no preference is given
	in := base(t, opts{noDividends: true})
	in.Price = priceAt(ceiling(t, in), "0")
	p := eval(t, in)
	if p.Action != Buy || p.IncomePreference || p.Score != 85 || p.MaxScore != 100 {
		t.Fatalf("%s %v %d/%d", p.Action, p.IncomePreference, p.Score, p.MaxScore)
	}
	for _, x := range p.Tests {
		if strings.HasPrefix(x.ID, "inc.") && x.Result != NotApplicable {
			t.Fatalf("%+v", x)
		}
	}
	// a payer above the ceilings still qualifies as a value business but gets no income points or preference
	in = base(t, opts{dividendHeavy: true})
	in.Price = priceAt(ceiling(t, in), "0")
	p = eval(t, in)
	if p.IncomePreference || p.Score != 95 || p.Action != Buy {
		t.Fatalf("%v %d %s %v", p.IncomePreference, p.Score, p.Action, p.Reasons)
	}
	// estimates are not declared distributions
	in = base(t, opts{})
	in.Payouts = payouts(false)
	in.Price = priceAt(ceiling(t, in), "0")
	p = eval(t, in)
	if p.IncomePreference || p.Score != 90 {
		t.Fatalf("%v %d", p.IncomePreference, p.Score)
	}
	// special distributions never count as ordinary income
	in = base(t, opts{noDividends: true})
	in.Payouts = []distribution.Payout{{Key: distribution.Key{SecurityID: "ACME", Type: distribution.Special, Period: "2025"}, State: distribution.StateDeclared, Amount: dec("5"), Currency: "EUR", ExDay: "2026-02-01"}}
	for _, x := range eval(t, in).Tests {
		if strings.HasPrefix(x.ID, "inc.") && x.Result != NotApplicable {
			t.Fatalf("a special distribution is not ordinary income: %+v", x)
		}
	}
	// a cancelled ordinary payout in the last year breaks the history
	in = base(t, opts{})
	in.Payouts = append(payouts(true), distribution.Payout{Key: distribution.Key{SecurityID: "ACME", Type: distribution.Ordinary, Period: "x"}, State: distribution.StateCancelled, Currency: "EUR", ExDay: "2025-12-01"})
	for _, x := range eval(t, in).Tests {
		if x.ID == "inc.history" && x.Result != Fail {
			t.Fatalf("%+v", x)
		}
	}
	// the next ex-date is shown but earns nothing
	in = base(t, opts{})
	in.Payouts = append(payouts(true), distribution.Payout{Key: distribution.Key{SecurityID: "ACME", Type: distribution.Ordinary, Period: "n"}, State: distribution.StateDeclared, Amount: dec("0.2"), Currency: "EUR", ExDay: "2026-03-09"})
	if p = eval(t, in); p.NextExDay != "2026-03-09" || p.Score != 90 {
		t.Fatalf("%q %d", p.NextExDay, p.Score)
	}
}

func TestUnresolvedContradictionBlocksUntilASourcedNoteResolvesIt(t *testing.T) {
	at := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	bad := scorecard.Annotation{Security: "ACME", Topic: "risk", Stance: scorecard.Counter, Text: "Auditor noted a material weakness and a restatement", Source: "10-K", Ref: "item 9A", At: at}
	in := base(t, opts{notes: append(notes(), bad)})
	p := eval(t, in)
	if p.StrategyState != "ineligible" || len(p.Counter) != 1 || !strings.Contains(strings.Join(p.Reasons, ";"), "unresolved") {
		t.Fatalf("%s %v", p.StrategyState, p.Reasons)
	}
	fixed := scorecard.Annotation{Security: "ACME", Topic: "risk", Stance: scorecard.Supports, Text: "Weakness remediated per the auditor's letter", Source: "10-K/A", Ref: "item 9A", At: at.AddDate(0, 1, 0)}
	in = base(t, opts{notes: append(append(notes(), bad), fixed)})
	if p = eval(t, in); p.StrategyState != "eligible" {
		t.Fatalf("%s %v", p.StrategyState, p.Reasons)
	}
	// an identity conflict blocks regardless of notes
	in.IdentityConflict = "two issuers share this ticker"
	if p = eval(t, in); p.StrategyState != "ineligible" || !strings.Contains(strings.Join(p.Reasons, ";"), "identity") {
		t.Fatalf("%s %v", p.StrategyState, p.Reasons)
	}
}

func TestCashYieldIsAComparisonNotAForecast(t *testing.T) {
	in := base(t, opts{})
	in.Price = priceAt(ceiling(t, in), "0")
	y := dec("0.025")
	in.CashYield = &y
	p := eval(t, in)
	if !strings.Contains(p.Alternatives[0].Note, "2.5%") || !strings.Contains(p.Alternatives[0].Note, "not a return") {
		t.Fatalf("%+v", p.Alternatives[0])
	}
	if !strings.Contains(p.Alternatives[1].Note, FundID) {
		t.Fatalf("%+v", p.Alternatives[1])
	}
}

func TestConfigValidation(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	if DefaultConfig().Reviewed {
		t.Fatal("the starter configuration must be unreviewed")
	}
	for name, mut := range map[string]func(*Config){
		"years": func(c *Config) { c.MinYears = 2 }, "mos": func(c *Config) { c.MarginOfSafety = dec("0.95") }, "scenario": func(c *Config) { c.EntryScenario = "bull" },
		"score": func(c *Config) { c.MinScore = 0 }, "id": func(c *Config) { c.ID = "x" }, "age": func(c *Config) { c.MaxPriceAgeDays = 0 },
	} {
		c := DefaultConfig()
		mut(&c)
		if c.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	a, b := DefaultConfig(), DefaultConfig()
	b.MinScore = 71
	if a.Hash() == b.Hash() {
		t.Fatal("hash ignores a threshold")
	}
	if _, err := Evaluate(Input{Security: "ACME", AsOf: "soon", Config: DefaultConfig()}); err == nil {
		t.Fatal("bad as-of day")
	}
}

func TestFundTemplateChecksAreNotAScore(t *testing.T) {
	cost, track := dec("0.0020"), dec("-0.0010")
	f := FundFacts{Security: "FND", Name: "World", Currency: "EUR", Exposure: []money.Currency{"USD", "EUR"}, OngoingCost: &cost, TrackingDifference: &track,
		Domicile: "IE", Structure: "physical", Distribution: "accumulating", Holdings: 1500, HoldingsDate: "2026-02-01", Source: "KID", Ref: "KID 2026"}
	a := AssessFund(f, DefaultFundConfig(), "2026-03-01")
	if !a.Eligible || !a.Broad || len(a.Reasons) != 0 {
		t.Fatalf("%+v", a)
	}
	f.Holdings, f.HoldingsDate = 0, ""
	if a = AssessFund(f, DefaultFundConfig(), "2026-03-01"); a.Eligible || !strings.Contains(strings.Join(a.Reasons, ";"), "look-through stays unknown") {
		t.Fatalf("%+v", a)
	}
	f.Holdings, f.HoldingsDate = 1500, "2025-01-01"
	if a = AssessFund(f, DefaultFundConfig(), "2026-03-01"); a.Eligible || !strings.Contains(strings.Join(a.Reasons, ";"), "too old") {
		t.Fatalf("%+v", a)
	}
	hi := dec("0.0075")
	f.HoldingsDate, f.OngoingCost, f.Structure, f.Domicile, f.Tilted = "2026-02-01", &hi, "synthetic", "US", true
	a = AssessFund(f, DefaultFundConfig(), "2026-03-01")
	r := strings.Join(a.Reasons, ";")
	if a.Eligible || a.Broad || !strings.Contains(r, "fail: documented ongoing cost") || !strings.Contains(r, "synthetic") || !strings.Contains(r, "domicile") || !strings.Contains(r, "broad core alternative") {
		t.Fatalf("%v", a.Reasons)
	}
	f.Source = ""
	if a = AssessFund(f, DefaultFundConfig(), "2026-03-01"); a.Checks[0].Result != Fail {
		t.Fatalf("%+v", a.Checks[0])
	}
}
