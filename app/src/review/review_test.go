package review

import (
	"math/big"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/money"
)

var d = money.MustDecimal

func pos() Position {
	return Position{Security: "sec_a", Name: "A", Sleeve: "core", Qty: d("100"), Free: d("100"), OpenedDay: "2026-08-01", Value: 40000, ValueKnown: true}
}

func reviewed() Config {
	c := DefaultConfig()
	c.Reviewed = true
	return c
}

func facts() Facts {
	return Facts{AsOf: "2026-11-03", Equity: 100000, PriceFresh: true, Thesis: Thesis{Status: "intact", Day: "2026-10-15", Note: "ok"}, LastReview: "2026-10-15"}
}

func codes(r Review) string {
	var c []string
	for _, f := range r.Findings {
		c = append(c, f.Code)
	}
	return strings.Join(c, ",")
}

func TestPlainHoldAndTheNextReviewDate(t *testing.T) {
	r := Evaluate(DefaultConfig(), pos(), facts())
	if r.Action != "HOLD" || r.Status != "ok" || r.NextReview != "2027-01-14" || r.Label() != "HOLD" {
		t.Fatalf("%+v", r)
	}
	if len(r.NotEvaluated) == 0 || !strings.Contains(r.NotEvaluated[0], "rebalance") {
		t.Errorf("an unevaluated rule must be said: %v", r.NotEvaluated)
	}
}

func TestALateReviewIsOverdueNeverAFreshHold(t *testing.T) {
	f := facts()
	f.LastReview = "2026-06-01" // next 2026-08-31; 64 days late on 2026-11-03
	r := Evaluate(reviewed(), pos(), f)
	if r.Action != "HOLD" || r.Status != "overdue" || r.DaysOverdue != 64 || !strings.Contains(r.Label(), "review overdue by 64 days") {
		t.Fatalf("%+v %s", r, r.Label())
	}
	// no recorded review: counted from the purchase
	f.LastReview = ""
	r = Evaluate(reviewed(), pos(), f)
	if r.Status != "overdue" || r.NextReview != "2026-10-31" {
		t.Fatalf("%+v", r)
	}
	// a review on the due day
	f.LastReview = "2026-08-04"
	if r = Evaluate(reviewed(), pos(), f); r.Status != "review_due" {
		t.Fatalf("%s", r.Status)
	}
	// the review date never sells anything
	f.LastReview = "2025-01-01"
	if r = Evaluate(reviewed(), pos(), f); r.Action != "HOLD" || r.Qty != "" {
		t.Fatalf("%+v", r)
	}
	// nothing to count from
	p := pos()
	p.OpenedDay = ""
	f.LastReview = ""
	if r = Evaluate(reviewed(), p, f); r.Status != "review_required" || !strings.Contains(codes(r), "never_reviewed") {
		t.Fatalf("%+v", r)
	}
}

func TestAnInstructionIsBoundedByTheFreeShares(t *testing.T) {
	p := pos()
	p.Free = d("6")
	f := facts()
	f.Instruction = &Instruction{Action: "reduce", Qty: d("10"), Why: "withdrawal", By: "me", Day: "2026-11-01"}
	r := Evaluate(reviewed(), p, f)
	if r.Action != "REDUCE" || r.Qty != "6" || r.Rule != 1 || !r.Capped || !strings.Contains(r.Findings[0].Detail, "only 6 are free") {
		t.Fatalf("%+v", r)
	}
	f.Instruction = &Instruction{Action: "sell", All: true, Why: "liquidity", By: "me", Day: "2026-11-01"}
	p.Free = d("100")
	if r = Evaluate(reviewed(), p, f); r.Action != "SELL" || r.Qty != "100" || r.Findings[0].Code != "liquidity" {
		t.Fatalf("%+v", r)
	}
}

func TestTheFirstReasonDecidesAndTheOthersAreConflicts(t *testing.T) {
	f := facts()
	f.Instruction = &Instruction{Action: "reduce", Qty: d("10"), Why: "instruction", Day: "2026-11-01"}
	f.Thesis = Thesis{Status: "broken", Day: "2026-10-30", Note: "fraud finding"}
	f.Breaches = []Breach{{Dimension: "issuer", Name: "iss_a", Share: d("0.40"), Limit: d("0.30")}}
	r := Evaluate(reviewed(), pos(), f)
	if r.Rule != 1 || r.Action != "REDUCE" || r.Qty != "10" {
		t.Fatalf("%+v", r)
	}
	joined := strings.Join(r.Conflicts, "|")
	if !strings.Contains(joined, "thesis_broken") || !strings.Contains(joined, "concentration") {
		t.Errorf("a sale must name the conditions beside it: %v", r.Conflicts)
	}
	// without the instruction the broken thesis decides, and sells what is free
	f.Instruction = nil
	if r = Evaluate(reviewed(), pos(), f); r.Rule != 2 || r.Action != "SELL" || r.Qty != "100" {
		t.Fatalf("%+v", r)
	}
}

func TestADividendCutIsAReviewNotASale(t *testing.T) {
	f := facts()
	f.DividendCuts = 1
	r := Evaluate(reviewed(), pos(), f)
	if r.Action != "HOLD" || r.Status != "review_required" || !strings.Contains(codes(r), "dividend_cut") || r.Qty != "" {
		t.Fatalf("%+v", r)
	}
	f.Thesis.DependsOnDividend = true
	f.DividendCancelled = 1
	r = Evaluate(reviewed(), pos(), f)
	if r.Action != "HOLD" || r.Status != "review_required" || !strings.Contains(r.Findings[0].Detail, "inconsistent with the recorded thesis") || !strings.Contains(r.Findings[0].Detail, "not a sale") {
		t.Fatalf("%+v", r.Findings)
	}
	// a thesis under review is a different reason
	f = facts()
	f.Thesis.Status = "under_review"
	if r = Evaluate(reviewed(), pos(), f); r.Status != "review_required" || !strings.Contains(codes(r), "thesis_under_review") || strings.Contains(codes(r), "dividend_cut") {
		t.Fatalf("%+v", r)
	}
	// and an unrecorded thesis is never "intact"
	f.Thesis = Thesis{}
	if r = Evaluate(reviewed(), pos(), f); r.Status != "review_required" || !strings.Contains(codes(r), "thesis_unrecorded") {
		t.Fatalf("%+v", r)
	}
}

func TestConcentrationIsTheSmallestCureBoundedByFreeShares(t *testing.T) {
	f := facts()
	f.Breaches = []Breach{{Dimension: "issuer", Name: "iss_a", Share: d("0.40"), Limit: d("0.30")}}
	// 40,000 of 100,000 is 40%; 30% is 30,000; excess 10,000 at 400 per share is 25 shares
	r := Evaluate(reviewed(), pos(), f)
	if r.Rule != 3 || r.Action != "REDUCE" || r.Qty != "25" || r.Capped {
		t.Fatalf("%+v", r)
	}
	p := pos()
	p.Free = d("20")
	r = Evaluate(reviewed(), p, f)
	if r.Qty != "20" || !r.Capped || !strings.Contains(r.Findings[0].Detail, "5 of the breach cannot be cured now") {
		t.Fatalf("%+v", r)
	}
	// fractional shares round up to eight digits
	p = pos()
	p.Fractional = true
	p.Qty, p.Free, p.Value = d("3"), d("3"), 40000
	f.Breaches[0].Limit = d("0.30")
	if r = Evaluate(reviewed(), p, f); r.Qty != "0.75000000" && r.Qty != "0.75" {
		t.Fatalf("%s", r.Qty)
	}
	// pending reserves: nothing free means the sale is blocked, not invented
	p = pos()
	p.Free = d("0")
	r = Evaluate(reviewed(), p, f)
	if r.Action != "HOLD" || r.Status != "blocked" || !strings.Contains(codes(r), "no_free_shares") {
		t.Fatalf("%+v", r)
	}
}

func TestValuationRuleUsesTheConfiguredScenario(t *testing.T) {
	f := facts()
	f.Valuation = &ValuationFacts{Currency: "USD", Bear: d("10"), Base: d("20"), Bull: d("30"), Price: d("35"), PriceCur: "USD", PriceDay: "2026-11-02"}
	r := Evaluate(reviewed(), pos(), f)
	if r.Rule != 4 || r.Action != "REDUCE" || r.Qty != "50" || !strings.Contains(r.Findings[0].Detail, "above the bull-case value 30") || !strings.Contains(r.Findings[0].Detail, "not weighed here") {
		t.Fatalf("%+v", r)
	}
	f.Valuation.Price = d("25") // above base, below bull
	if r = Evaluate(reviewed(), pos(), f); r.Action != "HOLD" {
		t.Fatalf("%+v", r)
	}
	cfg := reviewed()
	cfg.ValuationRule = "above_base"
	if r = Evaluate(cfg, pos(), f); r.Action != "REDUCE" || r.Findings[0].Code != "valuation_above_base" {
		t.Fatalf("%+v", r)
	}
	cfg.ValuationRule = "off"
	if r = Evaluate(cfg, pos(), f); r.Action != "HOLD" {
		t.Fatalf("%+v", r)
	}
	// another currency is not compared
	f.Valuation.PriceCur = "EUR"
	f.Valuation.Price = d("99")
	if r = Evaluate(reviewed(), pos(), f); r.Action != "HOLD" || !strings.Contains(strings.Join(r.NotEvaluated, "|"), "no rate is applied") {
		t.Fatalf("%+v", r)
	}
}

func TestApproachingGoalIsAReviewUnlessAFractionIsConfigured(t *testing.T) {
	f := facts()
	f.GoalDate = "2027-06-01"
	r := Evaluate(reviewed(), pos(), f)
	if r.Action != "HOLD" || r.Status != "review_required" || !strings.Contains(codes(r), "approaching_goal") {
		t.Fatalf("%+v", r)
	}
	cfg := reviewed()
	cfg.DeRiskFraction = d("0.25")
	r = Evaluate(cfg, pos(), f)
	if r.Rule != 3 || r.Action != "REDUCE" || r.Qty != "25" || !strings.Contains(r.Findings[0].Detail, "where proceeds would go") {
		t.Fatalf("%+v", r)
	}
	f.GoalDate = "2031-06-01"
	if r = Evaluate(cfg, pos(), f); r.Action != "HOLD" {
		t.Fatalf("a far goal: %+v", r)
	}
}

func TestEventPositionsExpireAndInvalidate(t *testing.T) {
	p := pos()
	p.Sleeve, p.OpenedDay = "event", "2026-10-19" // a Monday; 10 weekday sessions by 2026-11-02
	f := facts()
	f.AsOf = "2026-11-02"
	r := Evaluate(reviewed(), p, f)
	if r.Rule != 5 || r.Action != "SELL" || r.Findings[len(r.Findings)-1].Code != "event_expiry" {
		t.Fatalf("%+v", r)
	}
	f.AsOf = "2026-10-30"
	if r = Evaluate(reviewed(), p, f); r.Action != "HOLD" {
		t.Fatalf("%+v", r)
	}
	f.EventInvalidated, f.EventDetail = true, "closed below the event-session low"
	if r = Evaluate(reviewed(), p, f); r.Action != "SELL" || r.Findings[0].Code != "event_invalidated" {
		t.Fatalf("%+v", r)
	}
	// a core position is not subject to the event limit
	f.AsOf, f.EventInvalidated = "2026-11-02", false
	c := pos()
	c.OpenedDay = "2026-10-19"
	if r = Evaluate(DefaultConfig(), c, f); r.Action != "HOLD" {
		t.Fatalf("%+v", r)
	}
}

func TestMissingDataNeverBecomesHoldOrASale(t *testing.T) {
	f := facts()
	f.Missing = []string{"no current price"}
	f.PriceFresh = false
	f.Breaches = []Breach{{Dimension: "issuer", Name: "iss_a", Share: d("0.40"), Limit: d("0.30")}}
	r := Evaluate(reviewed(), pos(), f)
	if r.Action != "ABSTAIN" || r.Status != "blocked" || r.Qty != "" || r.Label() != "ABSTAIN (blocked)" {
		t.Fatalf("%+v", r)
	}
	// your own recorded decisions still apply, with the proceeds not invented
	f.Thesis = Thesis{Status: "broken", Day: "2026-11-01", Note: "n"}
	r = Evaluate(reviewed(), pos(), f)
	if r.Action != "SELL" || r.Rule != 2 || !strings.HasPrefix(r.Proceeds, "unavailable") {
		t.Fatalf("%+v", r)
	}
	// with a current price the proceeds are an estimate, labelled
	f.PriceFresh, f.Missing = true, nil
	r = Evaluate(reviewed(), pos(), f)
	if !strings.HasPrefix(r.Proceeds, "about 40000") || !strings.Contains(r.Proceeds, "before costs and taxes") {
		t.Fatalf("%s", r.Proceeds)
	}
}

func TestConfigRules(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(*Config){
		func(c *Config) { c.ID = "x" }, func(c *Config) { c.ReviewDays = 3 }, func(c *Config) { c.GoalWindowMonths = 0 },
		func(c *Config) { c.DeRiskFraction = d("1.5") }, func(c *Config) { c.ValuationRule = "maybe" },
		func(c *Config) { c.ValuationReduceFraction = d("0") }, func(c *Config) { c.MaxEventSessions = 4 }, func(c *Config) { c.MaxEventSessions = 21 },
	}
	for i, f := range bad {
		c := DefaultConfig()
		f(&c)
		if c.Validate() == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	a, b := DefaultConfig(), DefaultConfig()
	b.ReviewDays = 90
	if a.Hash() == b.Hash() || a.Hash() != DefaultConfig().Hash() {
		t.Error("hash")
	}
}

func TestAnUnreviewedRuleSetRequiresAReviewAndProposesNoSale(t *testing.T) {
	f := facts()
	f.Valuation = &ValuationFacts{Currency: "USD", Bear: d("10"), Base: d("20"), Bull: d("30"), Price: d("35"), PriceCur: "USD", PriceDay: "2026-11-02"}
	r := Evaluate(DefaultConfig(), pos(), f)
	if r.Action != "HOLD" || r.Status != "review_required" || !strings.Contains(r.Findings[0].Detail, "has not been reviewed by you") {
		t.Fatalf("%+v", r)
	}
	// your own decisions and your policy limits do not wait for that review
	f.Thesis = Thesis{Status: "broken", Day: "2026-11-01", Note: "n"}
	if r = Evaluate(DefaultConfig(), pos(), f); r.Action != "SELL" || r.Rule != 2 {
		t.Fatalf("%+v", r)
	}
	if got := roundMinor(big.NewRat(5, 2)); got != 2 {
		t.Errorf("half-even 2.5 gave %d", got)
	}
	if got := roundMinor(big.NewRat(7, 2)); got != 4 {
		t.Errorf("half-even 3.5 gave %d", got)
	}
}
