package allocate

import (
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/money"
)

func dec(s string) money.Decimal { return money.MustDecimal(s) }

func limits() Limits {
	return Limits{MaxIssuer: dec("0.25"), MaxSector: dec("0.30"), MaxFund: dec("0.60"), MaxForeign: dec("0.50"), MaxCountry: dec("0.60"),
		CashReserve: dec("0.20"), MinPosition: 10_000, MaxCostBurden: dec("0.01")}
}

func fees() Fees { return Fees{Known: true, Rate: dec("0.0005"), Min: 125, Source: "test schedule"} }

func stock(id, issuer, sector string, price string, rank int) Candidate {
	return Candidate{Security: id, Kind: "stock", Sleeve: "core", Issuer: issuer, Sector: sector, Country: "DE", Currency: "EUR", Price: dec(price), Rank: rank, Tradable: true}
}

func req(cash int64, cs ...Candidate) Request {
	return Request{Base: "EUR", Cash: cash, Candidates: cs, Limits: limits(), Fees: fees()}
}

func lineOf(p Plan, id string) (Line, bool) {
	for _, l := range p.Lines {
		if l.Security == id {
			return l, true
		}
	}
	return Line{}, false
}

func reason(p Plan, id string) string {
	for _, s := range p.Skipped {
		if s.Security == id {
			return s.Reason
		}
	}
	return ""
}

func TestFeasibleAllocationOfOneThousandEuro(t *testing.T) {
	p := Allocate(req(100_000, stock("A", "ia", "tech", "50", 1), stock("B", "ib", "health", "80", 2), stock("C", "ic", "staples", "120", 3)))
	if !p.Feasible || p.Wait || len(p.Lines) != 3 || p.Spendable != 80_000 || p.Equity != 100_000 {
		t.Fatalf("%+v", p)
	}
	// the issuer limit is 250.00 EUR including fees: 4x50, 3x80, 2x120
	for id, want := range map[string]string{"A": "4", "B": "3", "C": "2"} {
		if l, ok := lineOf(p, id); !ok || l.Qty != want || l.Fee != 125 {
			t.Errorf("%s: %+v", id, l)
		}
	}
	if p.TotalSpend != 20_000+24_000+24_000 || p.TotalFees != 375 || p.CashAfter != 100_000-68_000-375 {
		t.Fatalf("%+v", p)
	}
	if p.CashAfter < 20_000 {
		t.Fatal("the reserve was spent")
	}
	if len(p.Binding) == 0 {
		t.Fatal("the issuer limit shaped these orders and must be named")
	}
	// the sum of every order and fee never exceeds the spendable cash
	if p.TotalSpend+p.TotalFees > p.Spendable {
		t.Fatalf("overspent: %+v", p)
	}
}

func TestMoreCandidatesSpreadTheMoney(t *testing.T) {
	r := req(100_000, stock("A", "ia", "tech", "50", 1), stock("B", "ib", "health", "80", 2), stock("C", "ic", "staples", "120", 3), stock("D", "id", "energy", "30", 4))
	// with a 1% cost limit the 120 EUR order of C (fee 1.25) is too expensive, so the basket shrinks and says why
	p := Allocate(r)
	if !p.Feasible || len(p.Lines) != 3 || !strings.Contains(reason(p, "D"), "C could not be sized") || !strings.Contains(reason(p, "D"), "1.04%") {
		t.Fatalf("%+v", p)
	}
	r.Limits.MaxCostBurden = dec("0.015")
	p = Allocate(r)
	if !p.Feasible || len(p.Lines) != 4 {
		t.Fatalf("%+v", p)
	}
	for _, l := range p.Lines {
		if l.Notional < 10_000 || l.Notional+l.Fee > 20_000 {
			t.Errorf("%+v", l)
		}
	}
}

func TestFeeDrivenBatchAndWait(t *testing.T) {
	r := req(20_000, stock("A", "ia", "tech", "50", 1))
	r.Limits.CashReserve = dec("0")
	r.Fees = Fees{Known: true, Rate: dec("0.0005"), Min: 500, Source: "5 EUR minimum"}
	r.Limits.MaxIssuer, r.Limits.MaxSector, r.Limits.MaxCountry = dec("1"), dec("1"), dec("1")
	r.Contribution = 5_000
	p := Allocate(r)
	if p.Feasible || !p.Wait || len(p.Lines) != 0 {
		t.Fatalf("%+v", p)
	}
	ex := strings.Join(p.Explanation, "|")
	if !strings.Contains(ex, "wait:") || !strings.Contains(ex, "contribution") || p.Contributions < 1 {
		t.Fatalf("%v", p.Explanation)
	}
	if !strings.Contains(reason(p, "A"), "fees of 5.00 EUR") || !strings.Contains(reason(p, "A"), "above the limit 1.00%") {
		t.Fatalf("%q", reason(p, "A"))
	}
	// with enough cash the same candidate is bought in one order
	r.Cash = p.NeedSpendable + 5_000
	if q := Allocate(r); !q.Feasible || len(q.Lines) != 1 {
		t.Fatalf("%+v", q)
	}
}

func TestNoFeasiblePurchaseExplainsEveryCandidate(t *testing.T) {
	r := req(100_000, stock("BIG", "ib", "tech", "900", 1), stock("OVER", "io", "health", "10", 2), stock("EVT", "ie", "tech", "20", 3), stock("NOPRICE", "ip", "tech", "0", 4))
	r.Candidates[1].Ceiling = dec("9")
	r.Candidates[2].Sleeve = "event"
	p := Allocate(r)
	if p.Feasible || len(p.Lines) != 0 {
		t.Fatalf("%+v", p)
	}
	if !strings.Contains(reason(p, "BIG"), "less than the smallest order") && !strings.Contains(reason(p, "BIG"), "issuer limit") {
		t.Fatalf("%q", reason(p, "BIG"))
	}
	if !strings.Contains(reason(p, "OVER"), "above the entry ceiling") || !strings.Contains(reason(p, "EVT"), "event-sleeve risk is never moved") || !strings.Contains(reason(p, "NOPRICE"), "no current price") {
		t.Fatalf("%v", p.Skipped)
	}
	if !strings.Contains(strings.Join(p.Explanation, "|"), "nothing is relaxed") || p.CashAfter != 0 && p.CashAfter != 100_000 {
		t.Fatalf("%v", p.Explanation)
	}
	// the wait explanation accounts for the issuer limit on a larger equity
	if !p.Wait || p.NeedSpendable <= p.Spendable {
		t.Fatalf("%+v", p)
	}
}

func TestUnknownFeesMeanNoClaimOfAffordability(t *testing.T) {
	r := req(100_000, stock("A", "ia", "tech", "50", 1))
	r.Fees = Fees{}
	p := Allocate(r)
	if p.Feasible || !p.Wait || !strings.Contains(strings.Join(p.Explanation, "|"), "no claim is made") || reason(p, "*") != "trading costs are unknown" {
		t.Fatalf("%+v", p)
	}
}

func TestExistingExposureAcrossListingsCountsAgainstTheIssuerLimit(t *testing.T) {
	r := req(80_000, stock("A2", "ia", "tech", "50", 1), stock("B", "ib", "health", "80", 2))
	// 200 of equity 1,000 already sits in issuer ia under another listing
	r.Holdings = []Holding{{Security: "A1", Kind: "stock", Issuer: "ia", Sector: "tech", Country: "DE", Currency: "EUR", Value: 20_000, ValueKnown: true}}
	r.Limits.CashReserve = dec("0.10")
	p := Allocate(r)
	// 250.00 cap - 200.00 held under another listing = 50.00 of room, and the fee comes out of it:
	// not even one 50 EUR share fits, so the second listing is not bought and the first one's weight is not forgotten
	if _, bought := lineOf(p, "A2"); bought || !p.Feasible || !strings.Contains(reason(p, "A2"), "issuer limit ia leaves room for 50.00 EUR") {
		t.Fatalf("%+v", p)
	}
	if _, ok := lineOf(p, "B"); !ok {
		t.Fatalf("%+v", p)
	}
}

func TestExistingBreachesAreReportedAndNothingIsSold(t *testing.T) {
	r := req(50_000, stock("B", "ib", "health", "80", 1))
	r.Holdings = []Holding{{Security: "A1", Kind: "stock", Issuer: "ia", Sector: "tech", Country: "DE", Currency: "EUR", Value: 50_000, ValueKnown: true},
		{Security: "NOVAL", Kind: "stock", Issuer: "iz", Sector: "x", Currency: "EUR", ValueKnown: false}}
	p := Allocate(r)
	b := strings.Join(p.Breaches, "|")
	if !strings.Contains(b, "issuer ia") || !strings.Contains(b, "no sale is proposed") || !strings.Contains(b, "sector tech") {
		t.Fatalf("%v", p.Breaches)
	}
	if !strings.Contains(strings.Join(p.Unknown, "|"), "NOVAL has no current value") {
		t.Fatalf("%v", p.Unknown)
	}
	for _, l := range p.Lines {
		if l.Security == "A1" {
			t.Fatal("a held security was bought more of past its limit")
		}
	}
}

func TestFundsUnknownLookThroughAndTheThreeWayComparison(t *testing.T) {
	fund := Candidate{Security: "F", Kind: "fund", Sleeve: "core", Issuer: "if", Country: "IE", Currency: "EUR", Price: dec("100"), Rank: 1, Tradable: true, Fractional: true, Step: dec("0.1")}
	s1, s2 := stock("A", "ia", "tech", "50", 1), stock("B", "ib", "health", "80", 2)
	r := req(100_000, fund, s1, s2)
	r.Limits.MaxIssuer = dec("0.70")
	r.Limits.MaxFund = dec("0.70")
	r.Limits.MaxCountry = dec("1")
	opts := Compare(r)
	if len(opts) != 3 || opts[0].Name != "diversified core fund" || opts[2].Name != "cash and contributions" {
		t.Fatalf("%+v", opts)
	}
	f := opts[0].Plan
	if !f.Feasible || len(f.Lines) != 1 || f.Lines[0].Security != "F" {
		t.Fatalf("%+v", f)
	}
	if !strings.Contains(strings.Join(f.Unknown, "|"), "look-through holdings unknown") {
		t.Fatalf("unknown look-through must stay visible: %v", f.Unknown)
	}
	// 800 spendable, 70% cap of 1,000 equity = 700: 6.9 fund units (fractional step 0.1)
	if f.Lines[0].Qty != "6.9" {
		t.Fatalf("%+v", f.Lines[0])
	}
	if len(opts[1].Plan.Lines) == 0 || len(opts[2].Plan.Lines) != 0 || opts[2].Plan.CashAfter != 100_000 {
		t.Fatalf("%+v", opts)
	}
	// a policy that requires look-through leaves the fund out
	r.Limits.RequireLookThrough = true
	if reason(Allocate(r), "F") == "" {
		t.Fatal("fund not skipped")
	}
}

func TestForeignCurrencyNeedsARateAndPaysTheFXCost(t *testing.T) {
	us := stock("US", "iu", "tech", "100", 1)
	us.Currency, us.Country = "USD", "US"
	r := req(100_000, us)
	r.Fees.FXRate = dec("0.0020")
	if p := Allocate(r); p.Feasible || !strings.Contains(reason(p, "US"), "no USD->EUR rate") {
		t.Fatalf("%+v", p)
	}
	r.Rates = map[money.Currency]money.Decimal{"USD": dec("0.92")}
	p := Allocate(r)
	l, ok := lineOf(p, "US")
	if !p.Feasible || !ok {
		t.Fatalf("%+v", p)
	}
	// 2 shares = 200 USD = 184.00 EUR; fee = max(1.25, 0.0005x184) + 0.2% of 184 = 1.25 + 0.37
	if l.Qty != "2" || l.Notional != 18_400 || l.Fee != 125+37 {
		t.Fatalf("%+v", l)
	}
	// the foreign-currency cap applies
	r.Limits.MaxForeign = dec("0.10")
	if p := Allocate(r); p.Feasible {
		t.Fatalf("a 10%% foreign cap leaves room for 100 EUR: %+v", p)
	}
}

func TestExclusionsAndTieBreaks(t *testing.T) {
	r := req(100_000, stock("A", "ia", "tobacco", "50", 1), stock("B", "ib", "health", "80", 2))
	r.Limits.ExcludedSectors = []string{"tobacco"}
	p := Allocate(r)
	if !strings.Contains(reason(p, "A"), "excluded") {
		t.Fatalf("%v", p.Skipped)
	}
	// equal rank: lower cost first, then the income preference, then the earlier ex-date
	x, y := stock("X", "ix", "s1", "50", 1), stock("Y", "iy", "s2", "50", 1)
	y.IncomePreference = true
	r = req(30_000, x, y)
	r.Limits.CashReserve = dec("0")
	r.Limits.MaxIssuer, r.Limits.MaxSector, r.Limits.MaxCountry = dec("0.5"), dec("1"), dec("1")
	r.Limits.MaxCostBurden = dec("0.02")
	p = Allocate(r)
	if len(p.Lines) == 0 || p.Lines[0].Security != "Y" {
		t.Fatalf("income preference breaks a tie only after fit and cost: %+v", p.Lines)
	}
	// but a better rank always wins over income preference
	x.Rank = 1
	y.Rank = 2
	r = req(30_000, x, y)
	r.Limits.CashReserve = dec("0")
	r.Limits.MaxIssuer = dec("0.5")
	r.Limits.MinPosition = 14_000
	r.Limits.MaxIssuer = dec("0.6")
	r.Limits.MaxCostBurden = dec("0.02")
	r.Limits.MaxSector, r.Limits.MaxCountry = dec("1"), dec("1")
	if p = Allocate(r); len(p.Lines) != 1 || p.Lines[0].Security != "X" {
		t.Fatalf("%+v", p)
	}
}

func TestNothingSpendableAboveTheReserve(t *testing.T) {
	r := req(100_000, stock("A", "ia", "tech", "50", 1))
	r.Limits.CashReserve = dec("1")
	p := Allocate(r)
	if p.Feasible || !p.Wait || p.Spendable != 0 || !strings.Contains(strings.Join(p.Explanation, "|"), "not above the reserve") {
		t.Fatalf("%+v", p)
	}
	if p = Allocate(req(100_000)); p.Feasible || len(p.Lines) != 0 {
		t.Fatalf("%+v", p)
	}
}
