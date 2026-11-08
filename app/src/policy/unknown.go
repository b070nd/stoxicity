package policy

// Unknown is an unknown fact and what stops working until it is known.
type Unknown struct {
	Path    string   `json:"path"`
	Affects []string `json:"affects"`
}

// ResearchUnaffected is said next to every list of unknowns.
const ResearchUnaffected = "news, evidence, research and local paper workflows do not depend on these facts"

// Unknowns lists the unknown facts of a policy with the features each blocks.
func Unknowns(p Policy) []Unknown {
	var out []Unknown
	add := func(known bool, path string, affects ...string) {
		if !known {
			out = append(out, Unknown{Path: path, Affects: affects})
		}
	}
	f, a := p.Finances, p.Account
	add(f.Investable.Known(), "finances.investable", "initial portfolio construction", "contribution allocation", "position sizes", "affordability after costs")
	add(f.ExcludedCash.Known(), "finances.excluded_cash", "separating emergency money from investable money in the plan")
	add(p.Goal.TargetDate.Known(), "goal.target_date", "de-risking toward the goal date", "the five-year scenario", "holding horizons")
	add(a.TaxResidence.Known(), "account.tax_residence", "net income estimates", "withholding scenarios", "tax-aware position sizing")
	add(a.AccountType.Known(), "account.account_type", "order eligibility", "cash versus margin funding checks")
	add(a.Broker.Known(), "account.broker", "broker connection", "execution costs")
	add(a.Entity.Known(), "account.broker_entity", "product eligibility", "regulatory documents for funds")
	add(a.Permissions.Known(), "account.trading_permissions", "which markets and products can be bought", "confirmed order submission")
	add(a.Fractional.Known(), "account.fractional_shares", "small-lot allocation", "dividend reinvestment", "feasibility at small capital")
	add(a.CommissionSchedule.Known(), "account.commission_schedule", "affordability after costs", "fee-aware allocation", "wait or batch decisions")
	add(a.FXSchedule.Known(), "account.fx_schedule", "cost of buying in a foreign currency", "currency exposure trade-offs")
	add(a.Settlement.Known(), "account.settlement", "cash availability after sales", "dividend entitlement dates")
	add(a.MarketData.Known(), "account.market_data_entitlements", "execution-grade price validation", "API order submission")
	return out
}
