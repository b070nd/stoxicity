// Package policy is the investor's written policy: finances, goal, preferences,
// constraints and account facts, kept as immutable versions. A policy is only
// ever data; checking, previewing and activating it never places an order.
// Every fact the user could be wrong or unsure about carries its provenance,
// and an unknown fact is allowed: it switches off only the features that need
// it and says which.
package policy

import (
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// SchemaVersion is the version of the policy file format.
const SchemaVersion = 1

// Source says where a stated fact came from.
type Source string

const (
	SourceUser     Source = "user"     // the user stated it
	SourceImported Source = "imported" // read from a statement or the broker
	SourceEstimate Source = "estimate" // the user's rough guess, to be replaced
	SourceExample  Source = "example"  // a value of an example profile, not yet reviewed
	SourceUnknown  Source = "unknown"  // not known; the empty value means the same
)

func (s Source) valid() bool {
	switch s {
	case "", SourceUser, SourceImported, SourceEstimate, SourceExample, SourceUnknown:
		return true
	}
	return false
}

// Stated is a fact with its provenance. A fact whose source is empty or
// "unknown" is unknown, whatever its value says; the value is a pointer so that
// an unknown fact has no value at all rather than a zero that looks like one.
type Stated[T any] struct {
	Value  *T     `yaml:"value,omitempty" json:"value,omitempty"`
	Source Source `yaml:"source,omitempty" json:"source,omitempty"`
	Note   string `yaml:"note,omitempty" json:"note,omitempty"`
}

// Known reports whether the fact is known: it has a source other than unknown
// and a value.
func (s Stated[T]) Known() bool { return s.Source != "" && s.Source != SourceUnknown && s.Value != nil }

// Get returns the value of a known fact.
func (s Stated[T]) Get() (T, bool) {
	var zero T
	if !s.Known() {
		return zero, false
	}
	return *s.Value, true
}

// Is states a fact from a source.
func Is[T any](v T, src Source) Stated[T] { return Stated[T]{Value: &v, Source: src} }

// claimed reports a source given without a value, or a value without a source.
func (s Stated[T]) claimed() (hasSource, hasValue bool) {
	return s.Source != "" && s.Source != SourceUnknown, s.Value != nil
}

// Day is a calendar date, YYYY-MM-DD, with no time zone.
type Day string

// Parse returns the date at midnight UTC.
func (d Day) Parse() (time.Time, error) {
	t, err := time.Parse(time.DateOnly, string(d))
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a date (YYYY-MM-DD)", string(d))
	}
	return t, nil
}

// Policy is one complete investment policy.
type Policy struct {
	Schema int    `yaml:"schema" json:"schema"`
	Name   string `yaml:"name" json:"name"`
	// Example marks a profile that only shows the shape. It cannot be activated;
	// the user reads every value, replaces what is wrong and sets this to false.
	Example      bool           `yaml:"example" json:"example"`
	BaseCurrency money.Currency `yaml:"base_currency" json:"base_currency"`
	// Tier is the operational tier 0 to 3. What each tier permits is the job of
	// the tier story; here it is a recorded, versioned number.
	Tier        int         `yaml:"tier" json:"tier"`
	Finances    Finances    `yaml:"finances" json:"finances"`
	Goal        Goal        `yaml:"goal" json:"goal"`
	Preferences Preferences `yaml:"preferences" json:"preferences"`
	Constraints Constraints `yaml:"constraints" json:"constraints"`
	Account     Account     `yaml:"account" json:"account"`
}

// Finances is the money that may be invested and how it moves. Holdings and cash
// per currency are the ledger's, not the policy's.
type Finances struct {
	Investable   Stated[money.Amount] `yaml:"investable" json:"investable"`
	ExcludedCash Stated[money.Amount] `yaml:"excluded_cash" json:"excluded_cash"` // emergency and other money never invested
	// CashReserve is the fraction of the investable money kept as cash.
	CashReserve   money.Decimal  `yaml:"cash_reserve" json:"cash_reserve"`
	Contributions []Contribution `yaml:"contributions" json:"contributions"`
	Withdrawals   []Withdrawal   `yaml:"withdrawals" json:"withdrawals"`
	Liquidity     string         `yaml:"liquidity_notes" json:"liquidity_notes"`
}

// Contribution is a regular or single payment into the portfolio.
type Contribution struct {
	Amount money.Amount `yaml:"amount" json:"amount"`
	Every  string       `yaml:"every" json:"every"` // monthly, quarterly, yearly or once
	From   Day          `yaml:"from" json:"from"`
	Until  Day          `yaml:"until,omitempty" json:"until,omitempty"`
	Source Source       `yaml:"source,omitempty" json:"source,omitempty"`
}

// Withdrawal is money the user needs out of the portfolio on a date.
type Withdrawal struct {
	Amount money.Amount `yaml:"amount" json:"amount"`
	On     Day          `yaml:"on" json:"on"`
	Note   string       `yaml:"note,omitempty" json:"note,omitempty"`
}

// Goal is what the money is for and when it is needed.
type Goal struct {
	Description string      `yaml:"description" json:"description"`
	TargetDate  Stated[Day] `yaml:"target_date" json:"target_date"`
	// AcceptableLoss is the largest fall of the portfolio's value the user can
	// bear without selling for need, as a fraction.
	AcceptableLoss money.Decimal `yaml:"acceptable_loss" json:"acceptable_loss"`
}

// Preferences are what the user wants to own and how to be paid.
type Preferences struct {
	Objective          string           `yaml:"objective" json:"objective"`                     // growth, income or balanced
	DividendPreference string           `yaml:"dividend_preference" json:"dividend_preference"` // none, low, moderate or strong
	IncomeMode         string           `yaml:"income_mode" json:"income_mode"`                 // cash, drip or batched
	Instruments        []string         `yaml:"instruments" json:"instruments"`                 // stock, fund
	Markets            []string         `yaml:"markets" json:"markets"`                         // ISO country codes, e.g. US, DE
	Currencies         []money.Currency `yaml:"currencies" json:"currencies"`
	ExcludedSectors    []string         `yaml:"excluded_sectors" json:"excluded_sectors"`
	ExcludedIssuers    []string         `yaml:"excluded_issuers" json:"excluded_issuers"`
	ExcludedStructures []string         `yaml:"excluded_structures" json:"excluded_structures"`
	PreferredCategory  []string         `yaml:"preferred_categories" json:"preferred_categories"`
	MaxTradesPerMonth  int              `yaml:"max_trades_per_month" json:"max_trades_per_month"`
}

// Constraints are the limits proposals must stay inside. Fractions are of the
// investable money unless the name says otherwise.
type Constraints struct {
	// MaxIssuer limits one operating company: its shares in every class and
	// listing, plus the exposure to it through funds whose dated holdings are known.
	// A fund is not an issuer of its provider's other funds.
	MaxIssuer money.Decimal `yaml:"max_issuer" json:"max_issuer"`
	// MaxSector is measured against the whole portfolio (cash plus holdings), and
	// the share of the invested holdings is shown beside it.
	MaxSector money.Decimal `yaml:"max_sector" json:"max_sector"`
	// MaxFund limits one underlying fund: all its share classes and listings
	// together. A broad core fund is not held to the company cap.
	MaxFund money.Decimal `yaml:"max_fund" json:"max_fund"`
	// MaxProvider optionally limits the funds of one provider together. Zero: no
	// cap, the share is still reported.
	MaxProvider       money.Decimal `yaml:"max_provider,omitempty" json:"max_provider,omitzero"`
	MaxCurrency       money.Decimal `yaml:"max_foreign_currency" json:"max_foreign_currency"` // all currencies but the base
	MaxCountry        money.Decimal `yaml:"max_country" json:"max_country"`
	MaxEventSleeve    money.Decimal `yaml:"max_event_sleeve" json:"max_event_sleeve"`
	MaxEventTradeRisk money.Decimal `yaml:"max_event_trade_risk" json:"max_event_trade_risk"` // planned loss of one event trade
	DrawdownReview    money.Decimal `yaml:"drawdown_review" json:"drawdown_review"`           // fall that forces a review
	MaxCostBurden     money.Decimal `yaml:"max_cost_burden" json:"max_cost_burden"`           // costs as a fraction of one trade's value
	MinPosition       money.Amount  `yaml:"min_position" json:"min_position"`                 // smallest position worth holding
	MinTurnover       money.Amount  `yaml:"min_daily_turnover" json:"min_daily_turnover"`     // liquidity floor
	MinEvidence       money.Decimal `yaml:"min_evidence" json:"min_evidence"`                 // share of required evidence that must be present
	MinCoverage       money.Decimal `yaml:"min_data_coverage" json:"min_data_coverage"`       // share of required data that must be present
}

// Account is the facts about the broker account that decide what is possible
// and what it costs. Any of them may be unknown.
type Account struct {
	TaxResidence       Stated[string]   `yaml:"tax_residence" json:"tax_residence"`
	AccountType        Stated[string]   `yaml:"account_type" json:"account_type"`
	Broker             Stated[string]   `yaml:"broker" json:"broker"`
	Entity             Stated[string]   `yaml:"broker_entity" json:"broker_entity"`
	Permissions        Stated[[]string] `yaml:"trading_permissions" json:"trading_permissions"`
	Fractional         Stated[bool]     `yaml:"fractional_shares" json:"fractional_shares"`
	CommissionSchedule Stated[string]   `yaml:"commission_schedule" json:"commission_schedule"`
	FXSchedule         Stated[string]   `yaml:"fx_schedule" json:"fx_schedule"`
	Settlement         Stated[string]   `yaml:"settlement" json:"settlement"`
	MarketData         Stated[[]string] `yaml:"market_data_entitlements" json:"market_data_entitlements"`
}
