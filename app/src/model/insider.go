package model

import (
	"slices"
	"strings"
)

// InsiderTrade is one transaction in a company's own shares that an officer,
// director or large holder reported on a Form 4. Only the transactions in
// ordinary (non-derivative) securities are kept: they are the ones that say
// whether the people who know the company best are buying or selling it.
type InsiderTrade struct {
	// Accession is the filing the transaction was reported in; Seq numbers the
	// transactions of one filing from 1.
	Accession string
	Seq       int
	// CIK is the company whose shares were traded (the issuer), ten digits.
	CIK string
	// Owner is who traded, OwnerCIK their SEC number (the first one if several
	// filed together, and Owner then lists all of them), and Role what they are
	// to the company: "Chief Executive Officer, director", "director",
	// "10% owner".
	Owner    string
	OwnerCIK string
	Role     string
	// Security is the title of the security traded as the form gives it
	// ("Common Stock", "Series A Preferred Stock"); only a trade in common
	// shares says something about the common stock (see IsCommon).
	Security string
	// Form is "4" or its amendment "4/A", Period the day the form reports on
	// (periodOfReport). An amendment replaces the earlier forms of the same
	// owner and period.
	Form   string
	Period string
	// Date is the day of the transaction, Filed the day the form was filed
	// (both YYYY-MM-DD). A form may be filed up to two business days late.
	Date  string
	Filed string
	// Code is the SEC transaction code ("P" a purchase, "S" a sale, both on the
	// open market or private, "A" granted, "F" shares withheld for tax, "M" an option exercised,
	// "G" given away...); see InsiderCodes.
	Code string
	// Acquired says the owner got the shares (A) rather than gave them up (D).
	Acquired bool
	// Shares is the number traded, Price the price of one share (zero when the
	// form gives none, as for a grant), After the shares the owner holds
	// afterwards.
	Shares float64
	Price  float64
	After  float64
	// Direct says the shares are the owner's own (D) rather than held through a
	// trust, a company or a family member (I).
	Direct bool
	// Plan says the form states the trade was made under a Rule 10b5-1 plan,
	// scheduled in advance: a sale under one says little about the owner's view.
	Plan bool
}

// Insider trade kinds: purchases and sales are a signal of what an insider
// thinks, the rest is pay, tax and bookkeeping. The codes P and S cover private
// transactions as well as the open market, so they are called purchases and
// sales, not open market trades.
const (
	TradeBuy  = "buy"
	TradeSell = "sell"
)

// Kind is TradeBuy for a purchase (code P), TradeSell for a sale (code S), and
// empty for everything else (awards, exercises, tax withholding, gifts).
func (t InsiderTrade) Kind() string {
	switch t.Code {
	case "P":
		return TradeBuy
	case "S":
		return TradeSell
	}
	return ""
}

// commonWords and otherWords decide whether a security title is common shares.
var (
	commonWords = []string{"common", "ordinary", "capital stock", "beneficial interest"}
	otherWords  = []string{"preferred", "preference", "note", "warrant", "option", "right", "unit", "debenture", "bond",
		"convertible", "phantom", "appreciation", "depositary"}
)

// IsCommon says the trade is in common (or ordinary) shares: the title names
// them and names nothing else. A purchase of preferred stock, notes or warrants
// is not a vote of confidence in the common stock. An empty title (a filing
// read before titles were kept) is not known to be common.
func (t InsiderTrade) IsCommon() bool {
	title := strings.ToLower(t.Security)
	if !slices.ContainsFunc(commonWords, func(w string) bool { return strings.Contains(title, w) }) {
		return false
	}
	return !slices.ContainsFunc(otherWords, func(w string) bool { return strings.Contains(title, w) })
}

// Value is the money the trade moved: shares times price.
func (t InsiderTrade) Value() float64 { return t.Shares * t.Price }

// InsiderCodes names the transaction codes of Form 4 in plain words.
var InsiderCodes = map[string]string{
	"P": "purchase",
	"S": "sale",
	"A": "granted or awarded",
	"D": "sold back to the company",
	"F": "shares withheld to pay tax or exercise cost",
	"M": "option or right exercised",
	"X": "in-the-money option exercised",
	"C": "converted",
	"G": "gift",
	"W": "will or inheritance",
	"I": "discretionary transaction",
	"J": "other",
	"K": "equity swap",
	"L": "small acquisition",
	"O": "out-of-the-money option exercised",
	"U": "tendered in a takeover offer",
	"V": "voluntarily reported",
	"Z": "deposit in a voting trust",
	"E": "short position expired",
	"H": "long position expired",
}

// InsiderCodeLabel is the plain-word label of a transaction code, the code
// itself when it is unknown.
func InsiderCodeLabel(code string) string {
	if s, ok := InsiderCodes[code]; ok {
		return s
	}
	return code
}
