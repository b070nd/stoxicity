package universe

import (
	"regexp"
	"slices"
	"strings"
)

// The types a security can be, as the list shows and type: matches them. They
// refine Kind: the Nasdaq directory only says stock, ETF or test, and the name
// and the ticker say the rest.
const (
	TypeStock     = "stock" // common shares, the default
	TypePreferred = "preferred"
	TypeWarrant   = "warrant"
	TypeUnit      = "unit"
	TypeRight     = "right"
	TypeADR       = "adr"
	TypeNote      = "note" // debt the exchange lists: notes, debentures, bonds
	TypeETF       = "etf"
	TypeTest      = "test"
)

// Types are the values type: knows, in the order a filter lists them.
func Types() []string {
	return []string{TypeStock, TypePreferred, TypeWarrant, TypeUnit, TypeRight, TypeADR, TypeNote, TypeETF, TypeTest}
}

var (
	rightName = regexp.MustCompile(`\brights?$|\beach right\b|\bsubscription rights\b`)
	unitName  = regexp.MustCompile(`\bunits?,? each\b|\bunits$`)
	adrName   = regexp.MustCompile(`\badrs?\b|american depositary|depositary receipts?`)
)

// suffixIn reports whether the ticker ends in a dash and one of the endings:
// the SEC list spells the securities that are not shares BAC-PL, GNL-PA,
// BEBE-WT, AAC-U, DGAC-R. A bare class letter (BRK-B, JPM-D) says nothing, so
// the name has to.
func suffixIn(ticker string, endings ...string) bool {
	i := strings.LastIndexByte(ticker, '-')
	if i < 0 {
		return false
	}
	return slices.Contains(endings, ticker[i+1:])
}

func isPreferredTicker(t string) bool {
	i := strings.LastIndexByte(t, '-')
	rest := ""
	if i >= 0 {
		rest = t[i+1:]
	}
	return (len(rest) == 1 || len(rest) == 2) && rest[0] == 'P' && (len(rest) == 1 || rest[1] >= 'A' && rest[1] <= 'Z')
}

// Type says what sort of security the profile is, read from what the sources
// say: Kind, then the name the exchanges publish, then how the ticker is
// spelled. It is a reading, not a fact: a name that does not say "warrant" for
// a warrant is taken for a stock. "" means the sources say nothing yet (a
// company only the SEC list knows, whose record is not loaded).
func (p Profile) Type() string {
	if p.typed {
		return p.typ
	}
	return p.work()
}

// work reads the type; Type remembers the answer for a profile of a universe.
func (p Profile) work() string {
	switch p.Kind {
	case KindETF:
		return TypeETF
	case KindTest:
		return TypeTest
	}
	if t := securityType(p.Security); t != "" {
		return t
	}
	own := strings.ToLower(strings.TrimSpace(p.Name))
	name := own + " " + strings.ToLower(strings.Join(p.Aliases, " "))
	switch {
	case strings.Contains(name, "preferred") || isPreferredTicker(p.Ticker):
		return TypePreferred
	case strings.Contains(name, "warrant") || suffixIn(p.Ticker, "W", "WS", "WT", "WTA", "WTB"):
		return TypeWarrant
	case mentions(own, "right") && rightName.MatchString(own) || suffixIn(p.Ticker, "R", "RT") && p.Kind != "":
		return TypeRight
	case mentions(name, "unit") && unitName.MatchString(name) || suffixIn(p.Ticker, "U", "UN") && p.Kind != "":
		return TypeUnit
	case p.attached() != "":
		return p.attached()
	case (mentions(name, "adr") || mentions(name, "depositary")) && adrName.MatchString(name):
		return TypeADR
	case p.Kind == KindStock || p.EntityType == "operating":
		return TypeStock
	}
	return ""
}

// divisions are the SEC's ten SIC divisions, the coarse "domain" a company
// works in, Each takes the two-digit major
// groups of its range; Infrastructure is transport, communications, electric,
// gas and sanitary services, and Finance & Real Estate is finance, insurance and real
// estate.
var divisions = []struct {
	Name     string
	From, To int
}{
	{"Agriculture", 1, 9},
	{"Mining", 10, 14},
	{"Construction", 15, 17},
	{"Manufacturing", 20, 39},
	{"Infrastructure", 40, 49},
	{"Wholesale", 50, 51},
	{"Retail", 52, 59},
	{"Finance & Real Estate", 60, 67},
	{"Services", 70, 89},
	{"Public Administration", 91, 99},
}

// Domains are the names Domain gives, in the order of the SIC codes.
func Domains() []string {
	out := make([]string, len(divisions))
	for i, d := range divisions {
		out[i] = d.Name
	}
	return out
}

// Domain is the SIC division of a code: "Manufacturing" for 3571. "" when the
// code is empty or outside every division.
func Domain(sic string) string {
	if len(sic) < 2 {
		return ""
	}
	n := 0
	for _, r := range sic[:2] {
		if r < '0' || r > '9' {
			return ""
		}
		n = n*10 + int(r-'0')
	}
	for _, d := range divisions {
		if n >= d.From && n <= d.To {
			return d.Name
		}
	}
	return ""
}

// Domain is the SIC division of the company.
func (p Profile) Domain() string { return Domain(p.SIC) }

// mentions is a cheap test that runs before a regular expression: does the
// lower-case text hold the word's letters at all.
func mentions(text, letters string) bool { return strings.Contains(text, letters) }

// attached is the type of a security the exchanges spell as another ticker of
// the same company plus one letter: AACIW is the warrant, AACIU the unit and
// AACIR the right of AACI. "" when the ticker is not spelled so.
func (p Profile) attached() string {
	if len(p.Ticker) < 3 {
		return ""
	}
	last := p.Ticker[len(p.Ticker)-1]
	var kind string
	switch last {
	case 'W':
		kind = TypeWarrant
	case 'U':
		kind = TypeUnit
	case 'R':
		kind = TypeRight
	default:
		return ""
	}
	base := p.Ticker[:len(p.Ticker)-1]
	if slices.Contains(p.Siblings, base) {
		return kind
	}
	return ""
}

// securityType reads the name the Nasdaq directory gives a listing. The part
// after the last " - " says what is listed ("Warrant", "Units", "Series B
// Right", "Common Stock", "5.5% Senior Notes due 2030"); "" when it says
// nothing certain, and the other sources are asked.
func securityType(security string) string {
	s := strings.ToLower(strings.TrimSpace(security))
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, " - "); i >= 0 {
		s = s[i+3:]
	}
	switch {
	case strings.Contains(s, "preferred") || strings.Contains(s, "depositary shares") && !strings.Contains(s, "american"):
		return TypePreferred
	case strings.Contains(s, "warrant"):
		return TypeWarrant
	case strings.HasSuffix(s, " right") || strings.HasSuffix(s, " rights") || s == "right" || s == "rights":
		return TypeRight
	case strings.HasSuffix(s, " units") || strings.HasSuffix(s, " unit") || s == "units" || s == "unit":
		return TypeUnit
	case strings.Contains(s, "american depositary") || strings.Contains(s, "depositary receipt") || strings.HasSuffix(s, " ads"):
		return TypeADR
	case strings.Contains(s, "notes") || strings.Contains(s, "debenture") || strings.Contains(s, "bonds") || strings.Contains(s, " due 20"):
		return TypeNote
	case strings.Contains(s, "common stock") || strings.Contains(s, "ordinary shares") || strings.Contains(s, "common shares") ||
		strings.Contains(s, "class a") || strings.Contains(s, "class b") || strings.Contains(s, "class c"):
		return TypeStock
	}
	return ""
}
