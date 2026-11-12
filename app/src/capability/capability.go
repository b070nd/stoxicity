// Package capability is the register of what each data provider can supply, for
// which markets and instruments, at what cost and under what rights, and how
// that was verified. Nothing is assumed to be covered: an entry says "unknown"
// where nobody has checked, a provider that costs money or needs an account is
// off until the user turns it on, and a request for coverage that no usable
// provider has is answered with the structured import path, labelled import-only.
package capability

import (
	"fmt"
	"sort"
	"strings"
)

// Cost is what using a provider costs.
type Cost string

const (
	CostFree    Cost = "free"     // no account, no key
	CostFreeKey Cost = "free_key" // a free key or registration; may have quotas
	CostAccount Cost = "account"  // needs a funded broker account; some data can cost extra
	CostPaid    Cost = "paid"     // a subscription or per-request charge
)

// DataType is a kind of data a provider may supply.
type DataType string

const (
	Filings         DataType = "filings"
	Fundamentals    DataType = "fundamentals"
	PricesEOD       DataType = "prices_eod"
	PricesHistory   DataType = "prices_history"
	PricesLive      DataType = "prices_live"
	FXReference     DataType = "fx_reference"
	Dividends       DataType = "dividends"
	CorporateAction DataType = "corporate_actions"
	ETFMetadata     DataType = "etf_metadata"
	ETFHoldings     DataType = "etf_holdings"
	Instruments     DataType = "instruments"
)

// DataTypes lists the valid data types.
var DataTypes = []DataType{Filings, Fundamentals, PricesEOD, PricesHistory, PricesLive, FXReference, Dividends, CorporateAction, ETFMetadata, ETFHoldings, Instruments}

// Unknown is written wherever a fact has not been checked.
const Unknown = "unknown"

// Entitlement is one thing a provider supplies.
type Entitlement struct {
	Data DataType `json:"data"`
	// Markets are ISO country codes the data covers; "*" for all, empty if unknown.
	Markets []string `json:"markets"`
	// Instruments are the kinds covered ("stock", "etf"); empty if unknown.
	Instruments []string `json:"instruments"`
	History     string   `json:"history"`    // how far back, or "unknown"
	Delay       string   `json:"delay"`      // how late, or "unknown"
	Currencies  string   `json:"currencies"` // how prices and units are stated, or "unknown"
	// Wired says the product has code that fetches or reads this. A provider can
	// supply something and still be unusable because nothing here talks to it.
	Wired bool   `json:"wired"`
	Note  string `json:"note,omitempty"`
}

// Quota is a documented or configured limit.
type Quota struct {
	Requests int    `json:"requests,omitempty"`
	Per      string `json:"per,omitempty"` // second, minute or day
	Basis    string `json:"basis"`         // documented, configured or unknown
	Note     string `json:"note,omitempty"`
}

// Verification records a check of the provider against the real endpoint. An
// empty At means it was never checked by this system.
type Verification struct {
	At     string `json:"at,omitempty"` // YYYY-MM-DD
	How    string `json:"how,omitempty"`
	Result string `json:"result,omitempty"`
}

// Provider is one source of data.
type Provider struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	DocsURL      string        `json:"docs_url,omitempty"`
	Cost         Cost          `json:"cost"`
	KeyEnv       string        `json:"key_env,omitempty"` // the variable that holds the key, if one is needed
	Rights       string        `json:"rights"`            // what the terms allow, or "unknown"
	Quota        Quota         `json:"quota"`
	Entitlements []Entitlement `json:"entitlements"`
	Verified     Verification  `json:"verified"`
	// ImportOnly marks the structured import path: always available, never automatic.
	ImportOnly bool `json:"import_only,omitempty"`
}

// Supplies returns the provider's entitlement for a data type.
func (p Provider) Supplies(d DataType) (Entitlement, bool) {
	for _, e := range p.Entitlements {
		if e.Data == d {
			return e, true
		}
	}
	return Entitlement{}, false
}

// Options say what the user has turned on and what the environment holds. The
// zero value turns nothing on.
type Options struct {
	// AllowPaid names the providers the user explicitly allowed to cost money.
	AllowPaid map[string]bool
	// AccountReady names the providers whose account the user has set up.
	AccountReady map[string]bool
	// HasKey says whether a key variable is set. Nil means no key is set.
	HasKey func(env string) bool
}

// State of a provider for a request.
type State string

const (
	Usable         State = "usable"           // can be used now
	NeedsKey       State = "needs_key"        // free, but the key is not set
	NeedsAccount   State = "needs_account"    // needs a broker account the user has not set up
	PaidNotAllowed State = "paid_not_allowed" // costs money and the user did not allow it
	NoEntitlement  State = "no_entitlement"   // does not supply this data or market
	NotWired       State = "not_wired"        // the provider could supply it, but this product has no code for it yet
	ImportOnly     State = "import_only"      // only by structured import, never automatic
)

// Answer is one provider's standing for one request.
type Answer struct {
	Provider Provider `json:"provider"`
	State    State    `json:"state"`
	Reason   string   `json:"reason"`
}

// Registry holds the providers.
type Registry struct{ providers []Provider }

// New builds a registry and refuses a malformed one.
func New(ps []Provider) (*Registry, error) {
	seen := map[string]bool{}
	for _, p := range ps {
		switch {
		case p.ID == "" || p.Name == "":
			return nil, fmt.Errorf("capability: a provider needs an id and a name")
		case seen[p.ID]:
			return nil, fmt.Errorf("capability: provider %s is listed twice", p.ID)
		}
		seen[p.ID] = true
		switch p.Cost {
		case CostFree, CostFreeKey, CostAccount, CostPaid:
		default:
			return nil, fmt.Errorf("capability: %s: cost %q is not free, free_key, account or paid", p.ID, p.Cost)
		}
		if p.Cost == CostFreeKey && p.KeyEnv == "" {
			return nil, fmt.Errorf("capability: %s: a provider that needs a key must name the variable", p.ID)
		}
		if p.Rights == "" {
			return nil, fmt.Errorf("capability: %s: rights must be stated, or %q", p.ID, Unknown)
		}
		switch p.Quota.Basis {
		case "documented", "configured", Unknown:
		default:
			return nil, fmt.Errorf("capability: %s: quota basis %q is not documented, configured or unknown", p.ID, p.Quota.Basis)
		}
		if p.Quota.Basis != Unknown && (p.Quota.Requests <= 0 || p.Quota.Per == "") {
			return nil, fmt.Errorf("capability: %s: a quota with a basis needs requests and a period", p.ID)
		}
		for _, e := range p.Entitlements {
			if !validType(e.Data) {
				return nil, fmt.Errorf("capability: %s: unknown data type %q", p.ID, e.Data)
			}
			if e.History == "" || e.Delay == "" || e.Currencies == "" {
				return nil, fmt.Errorf("capability: %s/%s: history, delay and currencies must be stated, or %q", p.ID, e.Data, Unknown)
			}
		}
		if p.Verified.At != "" && (p.Verified.How == "" || p.Verified.Result == "") {
			return nil, fmt.Errorf("capability: %s: a verification needs how it was done and the result", p.ID)
		}
	}
	return &Registry{providers: append([]Provider(nil), ps...)}, nil
}

func validType(d DataType) bool {
	for _, k := range DataTypes {
		if k == d {
			return true
		}
	}
	return false
}

// Providers returns all providers.
func (r *Registry) Providers() []Provider { return append([]Provider(nil), r.providers...) }

// Request is a question about coverage.
type Request struct {
	Data   DataType
	Market string // ISO country code; empty for any
	Kind   string // instrument kind; empty for any
}

func (e Entitlement) covers(market, kind string) (bool, string) {
	if market != "" {
		ok := false
		for _, m := range e.Markets {
			if m == "*" || m == market {
				ok = true
			}
		}
		if !ok {
			if len(e.Markets) == 0 {
				return false, "the markets it covers are not known"
			}
			return false, "covers " + strings.Join(e.Markets, ", ") + ", not " + market
		}
	}
	if kind != "" {
		ok := false
		for _, k := range e.Instruments {
			if k == "*" || k == kind {
				ok = true
			}
		}
		if !ok {
			if len(e.Instruments) == 0 {
				return false, "the instruments it covers are not known"
			}
			return false, "covers " + strings.Join(e.Instruments, ", ") + ", not " + kind
		}
	}
	return true, ""
}

func (p Provider) state(req Request, o Options) (State, string) {
	e, ok := p.Supplies(req.Data)
	if !ok {
		return NoEntitlement, "does not supply " + string(req.Data)
	}
	if cov, why := e.covers(req.Market, req.Kind); !cov {
		return NoEntitlement, why
	}
	if !e.Wired {
		return NotWired, "this product has no code for it yet"
	}
	if p.ImportOnly {
		return ImportOnly, "supplied only by structured import, with source and time kept; nothing is fetched"
	}
	switch p.Cost {
	case CostPaid:
		if !o.AllowPaid[p.ID] {
			return PaidNotAllowed, "costs money and is off until you allow it"
		}
	case CostAccount:
		if !o.AccountReady[p.ID] {
			return NeedsAccount, "needs a funded broker account that has not been set up; market data can cost extra"
		}
	case CostFreeKey:
		if o.HasKey == nil || !o.HasKey(p.KeyEnv) {
			return NeedsKey, "free, but " + p.KeyEnv + " is not set"
		}
	}
	return Usable, "usable now"
}

// Coverage answers, for every provider, whether it can serve the request. The
// order puts what can be used first, free before any cost, and the import path
// last. It never hides a provider and never promotes a paid one.
func (r *Registry) Coverage(req Request, o Options) []Answer {
	var out []Answer
	for _, p := range r.providers {
		st, why := p.state(req, o)
		out = append(out, Answer{Provider: p, State: st, Reason: why})
	}
	rank := map[State]int{Usable: 0, NeedsKey: 1, NeedsAccount: 2, PaidNotAllowed: 3, ImportOnly: 4, NotWired: 5, NoEntitlement: 6}
	cost := map[Cost]int{CostFree: 0, CostFreeKey: 1, CostAccount: 2, CostPaid: 3}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.State] != rank[b.State] {
			return rank[a.State] < rank[b.State]
		}
		if cost[a.Provider.Cost] != cost[b.Provider.Cost] {
			return cost[a.Provider.Cost] < cost[b.Provider.Cost]
		}
		return a.Provider.ID < b.Provider.ID
	})
	return out
}

// Chosen is the outcome of picking a source for a request.
type Chosen struct {
	Provider *Provider `json:"provider,omitempty"`
	// Automatic is true when a usable provider was found. False means the data
	// can only come in by import, or not at all.
	Automatic  bool   `json:"automatic"`
	ImportOnly bool   `json:"import_only"`
	Reason     string `json:"reason"`
}

// Choose picks the first usable provider. A provider that costs money is never
// returned unless the user allowed it. When none is usable the answer says so
// and points at the import path if one exists for the data type; it does not
// fall back to anything that is off.
func (r *Registry) Choose(req Request, o Options) Chosen {
	var imp *Provider
	for _, a := range r.Coverage(req, o) {
		switch a.State {
		case Usable:
			p := a.Provider
			return Chosen{Provider: &p, Automatic: true, Reason: a.Reason}
		case ImportOnly:
			p := a.Provider
			imp = &p
		}
	}
	if imp != nil {
		return Chosen{Provider: imp, ImportOnly: true, Reason: fmt.Sprintf("no usable provider for %s; coverage is import-only", req.Data)}
	}
	return Chosen{Reason: fmt.Sprintf("no usable provider and no import path for %s", req.Data)}
}
