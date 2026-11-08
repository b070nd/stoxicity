package money

import (
	"fmt"
	"strings"
)

// Currency is an ISO 4217 code in capitals.
type Currency string

// exponents are the minor-unit digits of the currencies this system handles. A
// code that is not listed is refused rather than assumed to have two digits.
var exponents = map[Currency]int{
	"EUR": 2, "USD": 2, "GBP": 2, "CHF": 2, "SEK": 2, "NOK": 2, "DKK": 2, "PLN": 2,
	"CZK": 2, "HUF": 2, "RON": 2, "BGN": 2, "CAD": 2, "AUD": 2, "NZD": 2, "SGD": 2,
	"HKD": 2, "CNY": 2, "ZAR": 2, "ILS": 2, "MXN": 2, "BRL": 2, "TRY": 2, "INR": 2,
	"JPY": 0, "KRW": 0, "ISK": 0, "CLP": 0,
}

// ParseCurrency accepts exactly the listed codes, in capitals.
func ParseCurrency(s string) (Currency, error) {
	c := Currency(s)
	if _, ok := exponents[c]; !ok {
		if strings.EqualFold(s, string(c)) && len(s) == 3 && strings.ToUpper(s) != s {
			return "", fmt.Errorf("money: currency %q must be written in capitals (%s)", s, strings.ToUpper(s))
		}
		return "", fmt.Errorf("money: unknown currency %q", s)
	}
	return c, nil
}

// Valid reports whether c is a listed currency.
func (c Currency) Valid() bool { _, ok := exponents[c]; return ok }

// Exponent is the number of minor-unit digits (2 for EUR, 0 for JPY).
func (c Currency) Exponent() (int, error) {
	e, ok := exponents[c]
	if !ok {
		return 0, fmt.Errorf("money: unknown currency %q", string(c))
	}
	return e, nil
}

// QuoteUnit is the unit a price is quoted in: a currency, or a hundredth of it.
// Case matters: GBP is pounds and GBp is pence, a factor of 100 apart.
type QuoteUnit struct {
	Currency Currency
	Divisor  int64 // minor units per major unit as quoted: 1, or 100 for pence
	Code     string
}

var subunits = map[string]QuoteUnit{
	"GBp": {Currency: "GBP", Divisor: 100, Code: "GBp"},
	"GBX": {Currency: "GBP", Divisor: 100, Code: "GBX"},
	"ZAc": {Currency: "ZAR", Divisor: 100, Code: "ZAc"},
	"ZAC": {Currency: "ZAR", Divisor: 100, Code: "ZAC"},
	"ILA": {Currency: "ILS", Divisor: 100, Code: "ILA"},
}

// ParseQuoteUnit reads a currency code or a known subunit code exactly as
// written; it never folds the case.
func ParseQuoteUnit(s string) (QuoteUnit, error) {
	if u, ok := subunits[s]; ok {
		return u, nil
	}
	c, err := ParseCurrency(s)
	if err != nil {
		return QuoteUnit{}, fmt.Errorf("money: unknown quote unit %q", s)
	}
	return QuoteUnit{Currency: c, Divisor: 1, Code: s}, nil
}

// IsSubunit reports whether prices in this unit are a fraction of the currency.
func (u QuoteUnit) IsSubunit() bool { return u.Divisor != 1 }
