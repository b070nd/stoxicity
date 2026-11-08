package instrument

import (
	"fmt"
	"strings"
	"time"
)

// Kind is what a security is.
type Kind string

const (
	KindStock Kind = "stock"
	KindETF   Kind = "etf"   // an exchange-traded fund
	KindOther Kind = "other" // anything else; listed so it is visible, not supported
)

// Distribution says whether a fund share class pays its income out.
type Distribution string

const (
	Distributing  Distribution = "distributing"
	Accumulating  Distribution = "accumulating" // income stays in the fund; no cash dividend reaches the account
	NotApplicable Distribution = "not_applicable"
	Unknown       Distribution = "unknown"
)

// Tri is a yes, a no or not known.
type Tri string

const (
	Yes        Tri = "yes"
	No         Tri = "no"
	UnknownTri Tri = "unknown"
)

// Issuer is the legal entity behind securities. Several securities, the share
// classes of a company or the share classes of a fund, share one.
type Issuer struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Country string `yaml:"country" json:"country"` // two-letter, where it is established; empty if unknown
	CIK     string `yaml:"cik,omitempty" json:"cik,omitempty"`
	LEI     string `yaml:"lei,omitempty" json:"lei,omitempty"`
}

// Security is one class of what an issuer issues. It is identified by its ISIN
// where one is known; a fund's accumulating and distributing classes are two
// securities, never one.
type Security struct {
	ID       string `yaml:"id" json:"id"`
	IssuerID string `yaml:"issuer" json:"issuer"`
	Name     string `yaml:"name" json:"name"`
	Kind     Kind   `yaml:"kind" json:"kind"`
	// ShareClass names the class as the issuer does ("A", "B", "common", "USD Dist").
	ShareClass string `yaml:"share_class,omitempty" json:"share_class,omitempty"`
	ISIN       string `yaml:"isin,omitempty" json:"isin,omitempty"`
	// Distribution is for funds. A stock leaves it empty (not applicable).
	Distribution Distribution `yaml:"distribution,omitempty" json:"distribution,omitempty"`
	Domicile     string       `yaml:"domicile,omitempty" json:"domicile,omitempty"` // fund domicile, two letters
	UCITS        Tri          `yaml:"ucits,omitempty" json:"ucits,omitempty"`
	// LinkTicker, on import only, attaches this to the security that already
	// carries the ticker in the evidence history, so events keep their identity.
	LinkTicker string `yaml:"link_ticker,omitempty" json:"link_ticker,omitempty"`
}

// Contract is a broker's identifier for a listing. Routing (the exchange an
// order is sent to) is separate from the listing's primary venue.
type Contract struct {
	Broker string `yaml:"broker" json:"broker"`
	ConID  string `yaml:"conid" json:"conid"`
	Route  string `yaml:"route,omitempty" json:"route,omitempty"`
}

// Listing is a security on a venue, with the currency it trades in and the unit
// its price is quoted in. The unit must be stated: a London line quoted in pence
// (GBp) and one in pounds (GBP) are a factor of 100 apart.
type Listing struct {
	ID         string     `yaml:"id,omitempty" json:"id"`
	SecurityID string     `yaml:"security" json:"security"`
	MIC        string     `yaml:"mic" json:"mic"`
	Ticker     string     `yaml:"ticker" json:"ticker"`
	Currency   string     `yaml:"currency" json:"currency"`
	QuoteUnit  string     `yaml:"quote_unit" json:"quote_unit"`
	From       string     `yaml:"from" json:"from"` // first day it traded under this ticker, YYYY-MM-DD
	To         string     `yaml:"to,omitempty" json:"to,omitempty"`
	Primary    Tri        `yaml:"primary,omitempty" json:"primary,omitempty"`
	Contracts  []Contract `yaml:"contracts,omitempty" json:"contracts,omitempty"`
}

// Set is a collection of instruments, as imported or loaded.
type Set struct {
	Issuers    []Issuer   `yaml:"issuers" json:"issuers"`
	Securities []Security `yaml:"securities" json:"securities"`
	Listings   []Listing  `yaml:"listings" json:"listings"`
}

// ListingKey is the identity of a listing: venue, ticker and the first day.
func ListingKey(mic, ticker, from string) string {
	return fmt.Sprintf("%s:%s@%s", mic, strings.ToUpper(ticker), from)
}

// Active reports whether the listing traded on the day (To is exclusive: the
// first day the ticker no longer belongs to it).
func (l Listing) Active(day string) bool {
	return l.From <= day && (l.To == "" || day < l.To)
}

func validDay(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// Severity of a finding.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Problem is one finding tied to the thing it is about.
type Problem struct {
	Severity Severity `json:"severity"`
	Ref      string   `json:"ref"`
	Message  string   `json:"message"`
}

func (p Problem) String() string { return fmt.Sprintf("%s: %s: %s", p.Severity, p.Ref, p.Message) }

// Errors returns the findings that stop a set from being used.
func Errors(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if p.Severity == Error {
			out = append(out, p)
		}
	}
	return out
}
