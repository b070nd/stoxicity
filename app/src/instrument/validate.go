package instrument

import (
	"fmt"
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Normalize fills in the listing IDs that derive from the listing itself and
// upper-cases tickers. It changes nothing else.
func Normalize(s Set) Set {
	out := Set{
		Issuers:    slices.Clone(s.Issuers),
		Securities: slices.Clone(s.Securities),
		Listings:   slices.Clone(s.Listings),
	}
	for i, l := range out.Listings {
		out.Listings[i].Ticker = strings.ToUpper(strings.TrimSpace(l.Ticker))
		if l.ID == "" {
			out.Listings[i].ID = ListingKey(l.MIC, out.Listings[i].Ticker, l.From)
		}
	}
	return out
}

// Validate checks a set as a whole: each record, and the relations between them.
// It reads no clock and no network.
func Validate(s Set) []Problem {
	var ps []Problem
	errf := func(ref, f string, a ...any) { ps = append(ps, Problem{Error, ref, fmt.Sprintf(f, a...)}) }
	warnf := func(ref, f string, a ...any) { ps = append(ps, Problem{Warning, ref, fmt.Sprintf(f, a...)}) }
	s = Normalize(s)

	issuers := map[string]Issuer{}
	for _, is := range s.Issuers {
		ref := "issuer " + is.ID
		switch {
		case is.ID == "":
			errf("issuer", "has no id")
			continue
		case issuers[is.ID].ID != "":
			errf(ref, "is defined twice")
			continue
		}
		issuers[is.ID] = is
		if is.Name == "" {
			errf(ref, "has no name")
		}
		if is.Country != "" && (len(is.Country) != 2 || is.Country != strings.ToUpper(is.Country)) {
			errf(ref, "country %q is not a two-letter code in capitals", is.Country)
		}
		if is.LEI != "" {
			if err := ValidLEI(is.LEI); err != nil {
				errf(ref, "%v", err)
			}
		}
	}

	secs := map[string]Security{}
	isins := map[string]string{}
	classes := map[string]string{}
	for _, sec := range s.Securities {
		ref := "security " + sec.ID
		switch {
		case sec.ID == "":
			errf("security", "%q has no id", sec.Name)
			continue
		case secs[sec.ID].ID != "":
			errf(ref, "is defined twice")
			continue
		}
		secs[sec.ID] = sec
		if sec.Name == "" {
			errf(ref, "has no name")
		}
		if _, ok := issuers[sec.IssuerID]; !ok {
			errf(ref, "issuer %q is not defined", sec.IssuerID)
		}
		switch sec.Kind {
		case KindStock, KindETF, KindOther:
		default:
			errf(ref, "kind %q is not stock, etf or other", string(sec.Kind))
		}
		if sec.ISIN == "" {
			warnf(ref, "has no ISIN; it can only be found by venue and ticker")
		} else if err := ValidISIN(sec.ISIN); err != nil {
			errf(ref, "%v", err)
		} else if other, dup := isins[sec.ISIN]; dup {
			errf(ref, "ISIN %s is already the security %s; a fund's distributing and accumulating classes have different ISINs", sec.ISIN, other)
		} else {
			isins[sec.ISIN] = sec.ID
		}
		key := sec.IssuerID + "|" + strings.ToLower(sec.Name) + "|" + strings.ToLower(sec.ShareClass)
		if other, dup := classes[key]; dup {
			errf(ref, "has the same issuer, name and share class as %s", other)
		}
		classes[key] = sec.ID

		switch sec.Kind {
		case KindETF:
			switch sec.Distribution {
			case Distributing, Accumulating:
			case Unknown, "":
				warnf(ref, "distribution is unknown: whether it pays cash income cannot be told")
			default:
				errf(ref, "a fund's distribution is distributing, accumulating or unknown, not %q", string(sec.Distribution))
			}
			switch sec.UCITS {
			case Yes, No, UnknownTri, "":
			default:
				errf(ref, "ucits %q is not yes, no or unknown", string(sec.UCITS))
			}
			if sec.Domicile != "" && (len(sec.Domicile) != 2 || sec.Domicile != strings.ToUpper(sec.Domicile)) {
				errf(ref, "domicile %q is not a two-letter code in capitals", sec.Domicile)
			}
		default:
			if sec.Distribution != "" && sec.Distribution != NotApplicable {
				errf(ref, "distribution applies to funds, not to a %s", sec.Kind)
			}
			if sec.UCITS != "" && sec.UCITS != UnknownTri {
				errf(ref, "ucits applies to funds, not to a %s", sec.Kind)
			}
		}
	}

	ids := map[string]bool{}
	conids := map[string]string{}
	for _, l := range s.Listings {
		ref := "listing " + l.ID
		if ids[l.ID] {
			errf(ref, "is defined twice")
			continue
		}
		ids[l.ID] = true
		if _, ok := secs[l.SecurityID]; !ok {
			errf(ref, "security %q is not defined", l.SecurityID)
		}
		if err := ValidMIC(l.MIC); err != nil {
			errf(ref, "%v", err)
		} else if _, known := Venues[l.MIC]; !known {
			warnf(ref, "venue %s is not in the table; its calendar and fees are unknown", l.MIC)
		}
		if l.Ticker == "" || strings.ContainsAny(l.Ticker, " \t") {
			errf(ref, "ticker %q must be non-empty without spaces", l.Ticker)
		}
		cur := money.Currency(l.Currency)
		if !cur.Valid() {
			errf(ref, "currency %q is not a listed currency", l.Currency)
		}
		if l.QuoteUnit == "" {
			errf(ref, "the quote unit is not stated; pence and pounds differ by 100, so it is never assumed")
		} else if u, err := money.ParseQuoteUnit(l.QuoteUnit); err != nil {
			errf(ref, "%v", err)
		} else if cur.Valid() && u.Currency != cur {
			errf(ref, "quote unit %s is a unit of %s, but the listing trades in %s", l.QuoteUnit, u.Currency, cur)
		} else if l.MIC == "XLON" && !u.IsSubunit() && u.Currency == "GBP" {
			warnf(ref, "quoted in pounds on the London Stock Exchange; most lines there quote pence (GBp), so check the source")
		}
		if !validDay(l.From) {
			errf(ref, "from %q is not a date (YYYY-MM-DD)", l.From)
		}
		if l.To != "" && (!validDay(l.To) || l.To <= l.From) {
			errf(ref, "to %q is not a date after from %s", l.To, l.From)
		}
		switch l.Primary {
		case Yes, No, UnknownTri, "":
		default:
			errf(ref, "primary %q is not yes, no or unknown", string(l.Primary))
		}
		for _, c := range l.Contracts {
			if c.Broker == "" || c.ConID == "" {
				errf(ref, "a broker contract needs a broker and a contract id")
				continue
			}
			k := c.Broker + "/" + c.ConID
			if other, dup := conids[k]; dup && other != l.ID {
				errf(ref, "broker contract %s is already the listing %s", k, other)
			}
			conids[k] = l.ID
		}
	}

	// A ticker on a venue belongs to one listing at a time; it may be reused later.
	for i, a := range s.Listings {
		for _, b := range s.Listings[i+1:] {
			if a.MIC == b.MIC && a.Ticker == b.Ticker && overlaps(a, b) {
				errf("listing "+b.ID, "overlaps %s: the ticker %s on %s belongs to one listing at a time", a.ID, a.Ticker, a.MIC)
			}
		}
	}
	// A security has one primary listing at a time.
	for i, a := range s.Listings {
		for _, b := range s.Listings[i+1:] {
			if a.SecurityID == b.SecurityID && a.Primary == Yes && b.Primary == Yes && overlaps(a, b) {
				errf("security "+a.SecurityID, "has two primary listings at once: %s and %s", a.ID, b.ID)
			}
		}
	}
	return ps
}

func overlaps(a, b Listing) bool {
	aEndsBeforeB := a.To != "" && a.To <= b.From
	bEndsBeforeA := b.To != "" && b.To <= a.From
	return !aEndsBeforeB && !bEndsBeforeA
}
