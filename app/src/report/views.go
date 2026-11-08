package report

// The views below are what the terminal interface shows. They hold text that is
// ready to show and carry no store, no clock and no network: the interface
// builds its screens from them with pure functions.

// BookRef names a book.
type BookRef struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	Name    string `json:"name"`
	Env     string `json:"environment"`
	Base    string `json:"base_currency"`
	Status  string `json:"status"`
}

// Ref is how the book is written on the command line.
func (b BookRef) Ref() string { return b.Account + "/" + b.Name }

// PlanRow is one plan in a list: its latest revision.
type PlanRow struct {
	Revision  string `json:"revision"`
	Security  string `json:"security"`
	Name      string `json:"name"`
	Book      string `json:"book"`
	Sleeve    string `json:"sleeve"`
	Action    string `json:"action"`
	State     string `json:"state"`
	Recorded  string `json:"recorded_state"`
	Qty       string `json:"qty"`
	Expiry    string `json:"expiry"`
	Data      string `json:"data_state"`
	Strategy  string `json:"strategy_state"`
	Execution string `json:"execution_eligibility"`
	Unknowns  int    `json:"unknowns"`
	Currency  string `json:"currency"`
	ReviewBy  string `json:"review_by"`
}

// IncomeRow is one payout the book may be owed, with where the ledger stands.
type IncomeRow struct {
	Book     string `json:"book"`
	Payout   string `json:"payout"`
	Security string `json:"security"`
	ExDay    string `json:"ex_day"`
	PayDay   string `json:"pay_day"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	Shares   string `json:"shares"`
	PerShare string `json:"per_share"`
	Gross    string `json:"gross"`
	Booked   string `json:"booked"`
}

// IncomeView is the income of every book with the findings of the income check.
type IncomeView struct {
	Day      string      `json:"day"`
	Rows     []IncomeRow `json:"rows"`
	Findings []string    `json:"findings"`
}

// OrderRow is an order with every term the confirmation covers.
type OrderRow struct {
	ID          string   `json:"id"`
	Book        string   `json:"book"`
	BookName    string   `json:"book_name"`
	Env         string   `json:"environment"`
	State       string   `json:"state"`
	Side        string   `json:"side"`
	Security    string   `json:"security"`
	Qty         string   `json:"qty"`
	Filled      string   `json:"filled"`
	Remaining   string   `json:"remaining"`
	Limit       string   `json:"limit"`
	Currency    string   `json:"currency"`
	Type        string   `json:"type"`
	TIF         string   `json:"tif"`
	FeeEstimate string   `json:"fee_estimate"`
	TermsHash   string   `json:"terms_hash"`
	Plan        string   `json:"plan"`
	Reservation string   `json:"reservation"`
	Warnings    []string `json:"warnings"`
	Why         []string `json:"why"`
	Late        []string `json:"late"`
	Created     string   `json:"created"`
	Expires     string   `json:"expires"`
	ConfirmedBy string   `json:"confirmed_by"`
	ConfirmedAt string   `json:"confirmed_until"`
	BrokerRef   string   `json:"broker_ref"`
	// Confirmable says the interface may confirm this order itself; Why not says
	// why it may not.
	Confirmable bool   `json:"confirmable"`
	NotBecause  string `json:"not_confirmable_because"`
	Unknown     bool   `json:"outcome_unknown"`
}

// PolicyChange is one setting that changed in the last activation.
type PolicyChange struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// PolicyStatus is the policy in force and what its activation changed.
type PolicyStatus struct {
	Active       bool           `json:"active"`
	Epoch        int64          `json:"epoch"`
	Hash         string         `json:"hash"`
	Tier         int            `json:"tier"`
	At           string         `json:"at"`
	RiskAccepted bool           `json:"risk_accepted"`
	RiskNotes    []string       `json:"risk_notes"`
	Changes      []PolicyChange `json:"changes"`
	Effects      []string       `json:"effects"`
}
