package valuation

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

// Listing is a security of an issuer that a class can be mapped to.
type Listing struct {
	SecurityID string
	Ticker     string
	// Type is the kind of security (the universe's types: "stock", "adr", ...).
	Type string
}

// Mapping statuses.
const (
	Mapped    = "mapped"
	Ambiguous = "ambiguous"
	Unmapped  = "unmapped"
)

// Mapping methods, strongest first.
const (
	MethodCoverSymbol = "cover-symbol" // the cover lists the class's trading symbol
	MethodClassSuffix = "class-suffix" // one listing of the issuer carries the class letter
	MethodSoleListing = "sole-listing" // one class, one listed common security
	MethodNone        = "none"         // nothing found, or more than one candidate
	// MappingVersion names the rules above.
	MappingVersion = "class-map-1"
)

// Mapping ties a class of an issuer to a security, with how it was decided.
type Mapping struct {
	CIK        string
	Dimensions string
	SecurityID string
	Ticker     string
	// Type is the kind of the security now ("stock", "adr", ...), filled in when
	// valuing from the universe; it is not part of the stored mapping.
	Type       string
	Status     string
	Method     string
	Confidence float64
	// Basis states the evidence, or for an ambiguous mapping the candidates.
	Basis     string
	Accession string
	// MappedAt is when the mapping was made. A mapping never applies to a time
	// before it was made.
	MappedAt time.Time
}

var classLetter = regexp.MustCompile(`(?i)class([A-Z])(?:Common|Ordinary|Share|Stock)?(?:Member)?$`)

// letterOf reads the class letter of dimensions such as
// "us-gaap:StatementClassOfStockAxis=goog:CapitalClassAMember" ("A"); empty when
// the name has none.
func letterOf(dimensions string) string {
	for _, d := range strings.Split(dimensions, ";") {
		if i := strings.LastIndex(d, "="); i >= 0 {
			d = d[i+1:]
		}
		if i := strings.LastIndex(d, ":"); i >= 0 {
			d = d[i+1:]
		}
		if m := classLetter.FindStringSubmatch(d); m != nil {
			return strings.ToUpper(m[1])
		}
	}
	return ""
}

// suffixOf reads the class letter a ticker carries after a separator ("MOG.A",
// "BRK-B"); empty when it has none.
func suffixOf(ticker string) string {
	t := marketdata.NormTicker(ticker)
	if i := strings.LastIndex(t, "-"); i > 0 && len(t)-i == 2 {
		return t[i+1:]
	}
	return ""
}

// MapClasses decides, for each class of the pick, which security of the issuer
// it is. The rules, in order, each stopping at a result:
//
//  1. The cover lists a trading symbol for the class and one listing of the
//     issuer has that symbol: mapped.
//  2. The class name carries a letter (Class B) and exactly one listing of the
//     issuer carries the same letter after a separator in its ticker: mapped;
//     more than one: ambiguous.
//  3. The issuer has one class and one listing of common stock: mapped.
//  4. Otherwise nothing links the class to a security. With listings left
//     unclaimed by other classes it is ambiguous (they are named); with none it
//     is unmapped, which is how an unlisted class is told from a listed one.
//
// A mapping to a symbol that is not an ordinary share of the issuer (a warrant,
// a note) is not made.
func MapClasses(p Pick, listings []Listing, at time.Time) []Mapping {
	var out []Mapping
	common := make([]Listing, 0, len(listings))
	for _, l := range listings {
		if l.Type == "stock" || l.Type == "adr" || l.Type == "" {
			common = append(common, l)
		}
	}
	claimed := map[string]bool{}
	byClass := map[string]Mapping{}
	base := func(c Class) Mapping {
		return Mapping{CIK: p.CIK, Dimensions: c.Dimensions, Status: Unmapped, Method: MethodNone, Accession: p.Accession, MappedAt: at}
	}
	// Rule 1 for all classes first: a stated symbol outranks any guess, and the
	// listings it takes are not candidates of another class.
	for _, c := range p.Classes {
		if c.Ticker == "" {
			continue
		}
		m := base(c)
		for _, l := range common {
			if marketdata.SameTicker(l.Ticker, c.Ticker) {
				m.SecurityID, m.Ticker, m.Status, m.Method, m.Confidence = l.SecurityID, l.Ticker, Mapped, MethodCoverSymbol, 0.95
				m.Basis = fmt.Sprintf("the cover page of %s lists the symbol %s for the class", p.Accession, c.Ticker)
				claimed[l.SecurityID] = true
				byClass[c.Dimensions] = m
				break
			}
		}
	}
	for _, c := range p.Classes {
		if m, ok := byClass[c.Dimensions]; ok {
			out = append(out, m)
			continue
		}
		m := base(c)
		free := make([]Listing, 0, len(common))
		for _, l := range common {
			if !claimed[l.SecurityID] {
				free = append(free, l)
			}
		}
		switch {
		case c.Ticker != "":
			// The cover names a symbol that no listing of the issuer has.
			m.Basis = fmt.Sprintf("the cover page lists the symbol %s, which no security of the issuer carries", c.Ticker)
		case letterOf(c.Dimensions) != "":
			var hits []Listing
			for _, l := range free {
				if suffixOf(l.Ticker) == letterOf(c.Dimensions) {
					hits = append(hits, l)
				}
			}
			switch len(hits) {
			case 1:
				m.SecurityID, m.Ticker, m.Status, m.Method, m.Confidence = hits[0].SecurityID, hits[0].Ticker, Mapped, MethodClassSuffix, 0.8
				m.Basis = fmt.Sprintf("class %s and the ticker %s carries the letter %s", letterOf(c.Dimensions), hits[0].Ticker, letterOf(c.Dimensions))
			case 0:
				m.Basis = fmt.Sprintf("no security of the issuer carries the class letter %s: the class is not listed or not known as listed", letterOf(c.Dimensions))
			default:
				m.Status, m.Basis = Ambiguous, "more than one listing carries the class letter: "+tickers(hits)
			}
		case len(p.Classes) == 1 && c.Dimensions == "":
			switch len(free) {
			case 1:
				m.SecurityID, m.Ticker, m.Status, m.Method, m.Confidence = free[0].SecurityID, free[0].Ticker, Mapped, MethodSoleListing, 0.85
				m.Basis = "the issuer has one class of stock and one listing of common stock"
			case 0:
				m.Basis = "the issuer has no listing of common stock"
			default:
				m.Status, m.Basis = Ambiguous, "the cover states one count and the issuer has several listings: "+tickers(free)
			}
		default:
			if len(free) > 0 {
				m.Status, m.Basis = Ambiguous, "the cover gives the class no symbol and no letter; unclaimed listings: "+tickers(free)
			} else {
				m.Basis = "the cover gives the class no symbol and the issuer has no unclaimed listing"
			}
		}
		out = append(out, m)
	}
	return out
}

func tickers(ls []Listing) string {
	ts := make([]string, len(ls))
	for i, l := range ls {
		ts[i] = l.Ticker
	}
	slices.Sort(ts)
	return strings.Join(ts, ", ")
}
