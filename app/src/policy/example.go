package policy

import (
	"fmt"
	"sort"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Profiles lists the names Example accepts.
func Profiles() []string {
	names := make([]string, 0, len(profiles))
	for n := range profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var profiles = map[string]func() Policy{
	"conservative-eur-1000": conservativeEUR1000,
}

// Example returns a profile that shows the shape of a policy. It is marked
// example, every value carries the source "example" or is unknown, and it
// cannot be activated until the user has read it, replaced what is wrong and set
// example to false. The numbers are starting points, not advice and not tested
// settings.
func Example(profile string) (Policy, error) {
	f, ok := profiles[profile]
	if !ok {
		return Policy{}, fmt.Errorf("policy: no example profile %q (%v)", profile, Profiles())
	}
	return f(), nil
}

func eur(s string) money.Amount {
	a, err := money.ParseAmount(s)
	if err != nil {
		panic(err)
	}
	return a
}

func dec(s string) money.Decimal { return money.MustDecimal(s) }

func conservativeEUR1000() Policy {
	return Policy{
		Schema: SchemaVersion, Name: "conservative-eur-1000", Example: true,
		BaseCurrency: "EUR", Tier: 0,
		Finances: Finances{
			Investable:    Is(eur("1000.00 EUR"), SourceExample),
			CashReserve:   dec("0.20"),
			Contributions: []Contribution{},
			Withdrawals:   []Withdrawal{},
			Liquidity:     "Replace this with when you may need the money back.",
		},
		Goal: Goal{
			Description:    "Grow savings over about five years",
			TargetDate:     Is(Day("2031-11-08"), SourceExample),
			AcceptableLoss: dec("0.25"),
		},
		Preferences: Preferences{
			Objective: "balanced", DividendPreference: "moderate", IncomeMode: "batched",
			Instruments:     []string{"stock", "fund"},
			Markets:         []string{"US", "DE", "NL", "FR", "IE"},
			Currencies:      []money.Currency{"EUR", "USD"},
			ExcludedSectors: []string{}, ExcludedIssuers: []string{}, ExcludedStructures: []string{},
			PreferredCategory: []string{}, MaxTradesPerMonth: 2,
		},
		Constraints: Constraints{
			MaxIssuer: dec("0.10"), MaxSector: dec("0.25"), MaxFund: dec("0.60"),
			MaxCurrency: dec("0.50"), MaxCountry: dec("0.60"),
			MaxEventSleeve: dec("0"), MaxEventTradeRisk: dec("0"),
			DrawdownReview: dec("0.15"), MaxCostBurden: dec("0.02"),
			MinPosition: eur("100.00 EUR"), MinTurnover: eur("1000000.00 EUR"),
			MinEvidence: dec("0.80"), MinCoverage: dec("0.80"),
		},
		// Every account fact is unknown until the user states it.
	}
}
