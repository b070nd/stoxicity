package strategy

import (
	"testing"

	"github.com/b070nd/stoxicity/app/src/money"
)

func TestLookThroughIsUsedOnlyWhenDatedFreshAndWholeFractions(t *testing.T) {
	f := FundFacts{HoldingsDate: "2026-10-31", LookThrough: map[string]money.Decimal{"a": money.MustDecimal("0.30"), "b": money.MustDecimal("0.20")}}
	got, why := f.UsableLookThrough("2026-11-08", 120)
	if got == nil || why != "" || len(got) != 2 {
		t.Fatalf("%v %q", got, why)
	}
	for name, mod := range map[string]func(*FundFacts, *string){
		"no weights":   func(f *FundFacts, _ *string) { f.LookThrough = nil },
		"no date":      func(f *FundFacts, _ *string) { f.HoldingsDate = "" },
		"too old":      func(f *FundFacts, _ *string) { f.HoldingsDate = "2026-01-01" },
		"future":       func(f *FundFacts, _ *string) { f.HoldingsDate = "2026-12-01" },
		"zero weight":  func(f *FundFacts, _ *string) { f.LookThrough["a"] = money.MustDecimal("0") },
		"over a whole": func(f *FundFacts, _ *string) { f.LookThrough["a"] = money.MustDecimal("0.90") },
	} {
		g := FundFacts{HoldingsDate: f.HoldingsDate, LookThrough: map[string]money.Decimal{"a": f.LookThrough["a"], "b": f.LookThrough["b"]}}
		mod(&g, nil)
		if m, why := g.UsableLookThrough("2026-11-08", 120); m != nil || why == "" {
			t.Errorf("%s: used %v", name, m)
		}
	}
}
