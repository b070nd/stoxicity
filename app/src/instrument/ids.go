// Package instrument is the identity of what can be bought: the issuer, the
// security it issues (one per share class and per fund share class), and the
// listings of a security on venues, each with its currency and the unit its
// price is quoted in. Identity is never guessed: an identifier that fits
// several instruments resolves to an explicit ambiguity, two identifiers that
// disagree resolve to a conflict, and a quote unit must be stated, not assumed.
package instrument

import (
	"fmt"
	"strings"
)

// ISINCheckDigit returns the check digit (ISO 6166, a Luhn sum over the letters
// expanded to numbers) for the first eleven characters of an ISIN.
func ISINCheckDigit(body string) (byte, error) {
	if len(body) != 11 {
		return 0, fmt.Errorf("an ISIN body is 11 characters, not %d", len(body))
	}
	digits, err := expand(body)
	if err != nil {
		return 0, err
	}
	// Double every second digit from the right, the digit to come being the check digit.
	sum := 0
	double := true
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return byte('0' + (10-sum%10)%10), nil
}

func expand(s string) (string, error) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			fmt.Fprintf(&b, "%d", int(r-'A')+10)
		default:
			return "", fmt.Errorf("%q is not a digit or a capital letter", r)
		}
	}
	return b.String(), nil
}

// ValidISIN reports why s is not an ISIN, or nil: twelve characters, a two-letter
// country prefix, nine letters or digits and a correct check digit.
func ValidISIN(s string) error {
	if len(s) != 12 {
		return fmt.Errorf("%q is %d characters, an ISIN has 12", s, len(s))
	}
	for i := 0; i < 2; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return fmt.Errorf("%q does not start with a two-letter country code in capitals", s)
		}
	}
	want, err := ISINCheckDigit(s[:11])
	if err != nil {
		return fmt.Errorf("%q: %v", s, err)
	}
	if s[11] != want {
		return fmt.Errorf("%q has check digit %c, expected %c", s, s[11], want)
	}
	return nil
}

// ValidMIC reports why s is not an ISO 10383 market identifier: four capital
// letters or digits.
func ValidMIC(s string) error {
	if len(s) != 4 {
		return fmt.Errorf("%q is %d characters, a MIC has 4", s, len(s))
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fmt.Errorf("%q must be capital letters and digits", s)
		}
	}
	return nil
}

// ValidLEI reports why s is not an ISO 17442 legal entity identifier: twenty
// letters or digits whose last two are a mod-97 check.
func ValidLEI(s string) error {
	if len(s) != 20 {
		return fmt.Errorf("%q is %d characters, an LEI has 20", s, len(s))
	}
	digits, err := expand(s)
	if err != nil {
		return fmt.Errorf("%q: %v", s, err)
	}
	rem := 0
	for _, r := range digits {
		rem = (rem*10 + int(r-'0')) % 97
	}
	if rem != 1 {
		return fmt.Errorf("%q fails the mod-97 check", s)
	}
	return nil
}

// Venue is what the table knows about a market identifier.
type Venue struct {
	Name    string
	Country string
}

// Venues are the markets this system has a name for. A well-formed MIC that is
// not here is accepted with a warning: its calendar and fees are unknown.
var Venues = map[string]Venue{
	"XNAS": {"Nasdaq", "US"}, "XNYS": {"New York Stock Exchange", "US"}, "ARCX": {"NYSE Arca", "US"}, "XASE": {"NYSE American", "US"},
	"XETR": {"Xetra", "DE"}, "XFRA": {"Frankfurt Stock Exchange", "DE"},
	"XPAR": {"Euronext Paris", "FR"}, "XAMS": {"Euronext Amsterdam", "NL"}, "XBRU": {"Euronext Brussels", "BE"},
	"XLIS": {"Euronext Lisbon", "PT"}, "XDUB": {"Euronext Dublin", "IE"}, "XMIL": {"Borsa Italiana", "IT"},
	"XMAD": {"Bolsa de Madrid", "ES"}, "XWBO": {"Wiener Boerse", "AT"}, "XSWX": {"SIX Swiss Exchange", "CH"},
	"XLON": {"London Stock Exchange", "GB"}, "XSTO": {"Nasdaq Stockholm", "SE"}, "XHEL": {"Nasdaq Helsinki", "FI"},
	"XCSE": {"Nasdaq Copenhagen", "DK"}, "XOSL": {"Oslo Bors", "NO"}, "XTSE": {"Toronto Stock Exchange", "CA"},
}
