package policy

import (
	"fmt"
	"slices"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Severity of a finding.
type Severity string

const (
	// Error blocks activation: the policy contradicts itself or is malformed.
	Error Severity = "error"
	// Warning is shown and does not block: it is true and the user should know it.
	Warning Severity = "warning"
)

// Problem is one finding, tied to the field it is about.
type Problem struct {
	Severity Severity `json:"severity"`
	Path     string   `json:"path"`
	Message  string   `json:"message"`
}

func (p Problem) String() string { return fmt.Sprintf("%s: %s: %s", p.Severity, p.Path, p.Message) }

// Errors returns the findings that block activation.
func Errors(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if p.Severity == Error {
			out = append(out, p)
		}
	}
	return out
}

type checker struct {
	asOf time.Time
	ps   []Problem
}

func (c *checker) errf(path, format string, args ...any) {
	c.ps = append(c.ps, Problem{Error, path, fmt.Sprintf(format, args...)})
}

func (c *checker) warnf(path, format string, args ...any) {
	c.ps = append(c.ps, Problem{Warning, path, fmt.Sprintf(format, args...)})
}

var (
	zero = money.Int(0)
	one  = money.Int(1)
)

// fraction checks 0 < v <= 1 (or 0 <= v <= 1 when zeroOK).
func (c *checker) fraction(path string, v money.Decimal, zeroOK bool) {
	if v.Sign() < 0 || v.Cmp(one) > 0 || (!zeroOK && v.IsZero()) {
		lo := "above 0"
		if zeroOK {
			lo = "at least 0"
		}
		c.errf(path, "%s is not a fraction %s and at most 1 (write 0.10 for 10%%)", v, lo)
	}
}

func oneOf(v string, set ...string) bool { return slices.Contains(set, v) }

func (c *checker) source(path string, s Source) {
	if !s.valid() {
		c.errf(path, "source %q is not user, imported, estimate, example or unknown", string(s))
	}
}

// amountIn checks that an amount is in the base currency and, if positive is
// set, above zero. Conversion needs exchange rates, which a policy does not hold.
func (c *checker) amountIn(path string, a money.Amount, base money.Currency, positive bool) {
	switch {
	case !a.Valid():
		c.errf(path, "the amount has no valid currency")
	case a.Currency() != base:
		c.errf(path, "%s is not in the base currency %s; a policy holds no exchange rates, so state it in %s", a, base, base)
	case a.Sign() < 0 || (positive && a.Sign() == 0):
		c.errf(path, "%s must be %s", a, map[bool]string{true: "above zero", false: "zero or more"}[positive])
	}
}

// Validate checks the policy by itself: formats, ranges and contradictions. It
// reads no clock; asOf says what "in the past" means for dates.
func Validate(p Policy, asOf time.Time) []Problem {
	c := &checker{asOf: asOf}
	if p.Schema != SchemaVersion {
		c.errf("schema", "is %d, this version reads %d", p.Schema, SchemaVersion)
	}
	if p.Name == "" {
		c.errf("name", "is empty")
	}
	if !p.BaseCurrency.Valid() {
		c.errf("base_currency", "%q is not a listed currency", string(p.BaseCurrency))
	}
	if p.Tier < 0 || p.Tier > 3 {
		c.errf("tier", "is %d, must be 0 to 3", p.Tier)
	}
	base := p.BaseCurrency
	if base.Valid() {
		c.finances(p, base)
		c.constraints(p, base)
	}
	c.goal(p)
	c.preferences(p)
	c.account(p)
	if base.Valid() {
		c.ps = append(c.ps, Feasibility(p)...)
	}
	return c.ps
}

func (c *checker) finances(p Policy, base money.Currency) {
	f := p.Finances
	statedCheck(c, "finances.investable", f.Investable)
	statedCheck(c, "finances.excluded_cash", f.ExcludedCash)
	if v, ok := f.Investable.Get(); ok {
		c.amountIn("finances.investable.value", v, base, false)
	}
	if v, ok := f.ExcludedCash.Get(); ok {
		c.amountIn("finances.excluded_cash.value", v, base, false)
	}
	if f.CashReserve.Sign() < 0 || f.CashReserve.Cmp(one) >= 0 {
		c.errf("finances.cash_reserve", "%s must be at least 0 and below 1", f.CashReserve)
	}
	for i, k := range f.Contributions {
		path := fmt.Sprintf("finances.contributions[%d]", i)
		c.amountIn(path+".amount", k.Amount, base, true)
		c.source(path+".source", k.Source)
		if !oneOf(k.Every, "monthly", "quarterly", "yearly", "once") {
			c.errf(path+".every", "%q is not monthly, quarterly, yearly or once", k.Every)
		}
		from, err := k.From.Parse()
		if err != nil {
			c.errf(path+".from", "%v", err)
		}
		if k.Until != "" {
			until, err := k.Until.Parse()
			switch {
			case err != nil:
				c.errf(path+".until", "%v", err)
			case err == nil && from.After(until):
				c.errf(path+".until", "%s is before the start %s", k.Until, k.From)
			case k.Every == "once":
				c.errf(path+".until", "a single contribution has no end date")
			}
		}
	}
	for i, w := range f.Withdrawals {
		path := fmt.Sprintf("finances.withdrawals[%d]", i)
		c.amountIn(path+".amount", w.Amount, base, true)
		if _, err := w.On.Parse(); err != nil {
			c.errf(path+".on", "%v", err)
		}
	}
}

func (c *checker) goal(p Policy) {
	g := p.Goal
	statedCheck(c, "goal.target_date", g.TargetDate)
	if d, ok := g.TargetDate.Get(); ok {
		t, err := d.Parse()
		switch {
		case err != nil:
			c.errf("goal.target_date.value", "%v", err)
		case !c.asOf.IsZero() && !t.After(c.asOf):
			c.errf("goal.target_date.value", "%s is not after %s", d, c.asOf.Format(time.DateOnly))
		}
	}
	c.fraction("goal.acceptable_loss", g.AcceptableLoss, false)
	for i, w := range p.Finances.Withdrawals {
		d, ok := g.TargetDate.Get()
		if !ok {
			break
		}
		wt, err1 := w.On.Parse()
		gt, err2 := d.Parse()
		if err1 == nil && err2 == nil && wt.After(gt) {
			c.warnf(fmt.Sprintf("finances.withdrawals[%d].on", i), "%s is after the goal date %s", w.On, d)
		}
	}
}

func (c *checker) preferences(p Policy) {
	pr := p.Preferences
	if !oneOf(pr.Objective, "growth", "income", "balanced") {
		c.errf("preferences.objective", "%q is not growth, income or balanced", pr.Objective)
	}
	if !oneOf(pr.DividendPreference, "none", "low", "moderate", "strong") {
		c.errf("preferences.dividend_preference", "%q is not none, low, moderate or strong", pr.DividendPreference)
	}
	if !oneOf(pr.IncomeMode, "cash", "drip", "batched") {
		c.errf("preferences.income_mode", "%q is not cash, drip or batched", pr.IncomeMode)
	}
	if pr.Objective == "income" && pr.DividendPreference == "none" {
		c.errf("preferences.dividend_preference", "an income objective with no dividend preference contradicts itself")
	}
	if len(pr.Instruments) == 0 {
		c.errf("preferences.instruments", "is empty; allow stock, fund or both")
	}
	for _, in := range pr.Instruments {
		if !oneOf(in, "stock", "fund") {
			c.errf("preferences.instruments", "%q is not stock or fund", in)
		}
	}
	if len(pr.Markets) == 0 {
		c.errf("preferences.markets", "is empty; name at least one market, such as US or DE")
	}
	for _, m := range pr.Markets {
		if len(m) != 2 || m != upper(m) {
			c.errf("preferences.markets", "%q is not a two-letter country code in capitals", m)
		}
	}
	if len(pr.Currencies) == 0 {
		c.errf("preferences.currencies", "is empty; name at least one currency")
	}
	seen := map[money.Currency]bool{}
	for _, cur := range pr.Currencies {
		if !cur.Valid() {
			c.errf("preferences.currencies", "%q is not a listed currency", string(cur))
		}
		if seen[cur] {
			c.errf("preferences.currencies", "%s is listed twice", cur)
		}
		seen[cur] = true
	}
	if pr.MaxTradesPerMonth < 0 {
		c.errf("preferences.max_trades_per_month", "is %d, must be 0 or more", pr.MaxTradesPerMonth)
	}
	if frac, ok := p.Account.Fractional.Get(); pr.IncomeMode == "drip" && ok && !frac {
		c.warnf("preferences.income_mode", "reinvesting every dividend needs fractional shares, and the account does not have them; small dividends will sit as cash")
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if ch >= 'a' && ch <= 'z' {
			b[i] = ch - 32
		}
	}
	return string(b)
}

func (c *checker) constraints(p Policy, base money.Currency) {
	k := p.Constraints
	c.fraction("constraints.max_issuer", k.MaxIssuer, false)
	c.fraction("constraints.max_sector", k.MaxSector, false)
	c.fraction("constraints.max_fund", k.MaxFund, true)
	c.fraction("constraints.max_provider", k.MaxProvider, true)
	c.fraction("constraints.max_foreign_currency", k.MaxCurrency, true)
	c.fraction("constraints.max_country", k.MaxCountry, false)
	c.fraction("constraints.max_event_sleeve", k.MaxEventSleeve, true)
	c.fraction("constraints.max_event_trade_risk", k.MaxEventTradeRisk, true)
	c.fraction("constraints.drawdown_review", k.DrawdownReview, false)
	c.fraction("constraints.max_cost_burden", k.MaxCostBurden, false)
	c.fraction("constraints.min_evidence", k.MinEvidence, true)
	c.fraction("constraints.min_data_coverage", k.MinCoverage, true)
	c.amountIn("constraints.min_position", k.MinPosition, base, false)
	c.amountIn("constraints.min_daily_turnover", k.MinTurnover, base, false)

	if k.MaxSector.Cmp(k.MaxIssuer) < 0 {
		c.errf("constraints.max_sector", "%s is below the issuer cap %s; an issuer sits in one sector, so the sector cap cannot be lower", k.MaxSector, k.MaxIssuer)
	}
	if k.MaxProvider.Sign() > 0 && k.MaxFund.Sign() > 0 && k.MaxProvider.Cmp(k.MaxFund) < 0 {
		c.errf("constraints.max_provider", "%s is below the single-fund cap %s; a provider's funds include each of its funds, so the provider cap cannot be lower", k.MaxProvider, k.MaxFund)
	}
	if k.MaxEventTradeRisk.Cmp(k.MaxEventSleeve) > 0 {
		c.errf("constraints.max_event_trade_risk", "%s is above the whole event sleeve %s; one trade cannot plan to lose more than the sleeve holds", k.MaxEventTradeRisk, k.MaxEventSleeve)
	}
	if room, err := one.Sub(p.Finances.CashReserve); err == nil && k.MaxEventSleeve.Cmp(room) > 0 {
		c.errf("constraints.max_event_sleeve", "%s is more than the %s left after the cash reserve %s", k.MaxEventSleeve, room, p.Finances.CashReserve)
	}
	if k.MaxEventSleeve.Sign() > 0 && p.Tier < 2 {
		c.warnf("constraints.max_event_sleeve", "an event sleeve of %s is set at tier %d; real event trades stay off below tier 2, so it applies to paper trading only", k.MaxEventSleeve, p.Tier)
	}
	if k.DrawdownReview.Cmp(p.Goal.AcceptableLoss) > 0 {
		c.warnf("constraints.drawdown_review", "%s review threshold is above the acceptable loss %s; the review would come after the loss you said you can bear", k.DrawdownReview, p.Goal.AcceptableLoss)
	}
}

// statedCheck checks one stated fact's source and that source and value agree.
func statedCheck[T any](c *checker, path string, s Stated[T]) {
	c.source(path+".source", s.Source)
	hasSource, hasValue := s.claimed()
	switch {
	case hasSource && !hasValue:
		c.errf(path+".value", "has the source %q but no value; leave the source unknown or give the value", string(s.Source))
	case !hasSource && hasValue:
		c.errf(path+".source", "has a value but no source; say whether you stated, imported or estimated it, or remove the value")
	}
}

func (c *checker) account(p Policy) {
	a := p.Account
	statedCheck(c, "account.tax_residence", a.TaxResidence)
	statedCheck(c, "account.account_type", a.AccountType)
	statedCheck(c, "account.broker", a.Broker)
	statedCheck(c, "account.broker_entity", a.Entity)
	statedCheck(c, "account.trading_permissions", a.Permissions)
	statedCheck(c, "account.fractional_shares", a.Fractional)
	statedCheck(c, "account.commission_schedule", a.CommissionSchedule)
	statedCheck(c, "account.fx_schedule", a.FXSchedule)
	statedCheck(c, "account.settlement", a.Settlement)
	statedCheck(c, "account.market_data_entitlements", a.MarketData)
	if v, ok := a.TaxResidence.Get(); ok {
		if len(v) != 2 || v != upper(v) {
			c.errf("account.tax_residence.value", "%q is not a two-letter country code in capitals; a time zone does not establish tax residence", v)
		}
	}
	if v, ok := a.AccountType.Get(); ok {
		if !oneOf(v, "cash", "margin", "retirement", "other") {
			c.errf("account.account_type.value", "%q is not cash, margin, retirement or other", v)
		}
		if v == "margin" {
			c.warnf("account.account_type.value", "a margin account is allowed, but the system only proposes cash-funded long positions")
		}
	}
}
