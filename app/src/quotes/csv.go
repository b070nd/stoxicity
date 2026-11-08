package quotes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
)

// Hash identifies a file's exact bytes; it is written as the source reference of
// everything imported from it.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ParseFXCSV reads rates from a file with the columns base, quote, day, rate and
// optionally basis (reference, fill or user; default user: a typed rate is an
// assumption until the file says otherwise) and ref. Every problem is collected
// so one pass shows them all; any problem means no rows.
func ParseFXCSV(r io.Reader, source, fileRef string, received time.Time) ([]FXObs, error) {
	rows, err := csvtable.Read(r, []string{"base", "quote", "day", "rate"}, []string{"basis", "ref"})
	if err != nil {
		return nil, err
	}
	var out []FXObs
	var bad []string
	for _, row := range rows {
		fail := func(f string, a ...any) {
			bad = append(bad, fmt.Sprintf("line %s: %s", row["_line"], fmt.Sprintf(f, a...)))
		}
		base, e1 := money.ParseCurrency(row["base"])
		quote, e2 := money.ParseCurrency(row["quote"])
		val, e3 := money.ParseDecimal(row["rate"])
		if e1 != nil || e2 != nil || e3 != nil {
			fail("%v", firstErr(e1, e2, e3))
			continue
		}
		basis := money.Basis(row["basis"])
		if basis == "" {
			basis = money.BasisUser
		}
		ref := fileRef
		if row["ref"] != "" {
			ref = fileRef + " " + row["ref"]
		}
		o := FXObs{Base: base, Quote: quote, Value: val, Basis: basis, Day: row["day"], Source: source, SourceRef: ref, Received: received}
		if err := o.Validate(); err != nil {
			fail("%v", err)
			continue
		}
		out = append(out, o)
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("quotes: not imported:\n  %s", strings.Join(bad, "\n  "))
	}
	return out, nil
}

// ListingResolver finds the listing a row means on its day; ambiguity is an error.
type ListingResolver func(mic, ticker, day string) (instrument.Listing, error)

// RegistryResolver resolves against an instrument registry and turns anything
// but one listing into an error that says why.
func RegistryResolver(reg *instrument.Registry) ListingResolver {
	return func(mic, ticker, day string) (instrument.Listing, error) {
		res := reg.ResolveListing(instrument.Query{MIC: mic, Ticker: ticker, Day: day})
		if res.Status != instrument.Resolved {
			return instrument.Listing{}, fmt.Errorf("%s:%s on %s is %s: %s", mic, ticker, day, res.Status, strings.Join(res.Reasons, "; "))
		}
		return *res.Listing, nil
	}
}

// ParsePriceCSV reads prices from a file with the columns mic, ticker, day and
// price, and optionally unit, kind, adjustment, delay. A row with no unit takes
// the listing's and is marked as such; a row that states one must state it
// exactly, in the right case. Adjustment defaults to unknown, not raw: a file
// that does not say is not trusted to be unadjusted.
func ParsePriceCSV(r io.Reader, resolve ListingResolver, source, fileRef string, received time.Time) ([]PriceObs, error) {
	rows, err := csvtable.Read(r, []string{"mic", "ticker", "day", "price"}, []string{"unit", "kind", "adjustment", "delay"})
	if err != nil {
		return nil, err
	}
	var out []PriceObs
	var bad []string
	for _, row := range rows {
		fail := func(f string, a ...any) {
			bad = append(bad, fmt.Sprintf("line %s: %s", row["_line"], fmt.Sprintf(f, a...)))
		}
		if !validDay(row["day"]) {
			fail("the day %q is not YYYY-MM-DD", row["day"])
			continue
		}
		l, err := resolve(row["mic"], row["ticker"], row["day"])
		if err != nil {
			fail("%v", err)
			continue
		}
		val, err := money.ParseDecimal(row["price"])
		if err != nil {
			fail("%v", err)
			continue
		}
		unitCode, basis := row["unit"], UnitStated
		if unitCode == "" {
			unitCode, basis = l.QuoteUnit, UnitFromListing
		}
		unit, err := money.ParseQuoteUnit(unitCode)
		if err != nil {
			fail("%v", err)
			continue
		}
		kind := Kind(row["kind"])
		if kind == "" {
			kind = Close
		}
		adj := Adjustment(row["adjustment"])
		if adj == "" {
			adj = AdjUnknown
		}
		delay := Delay(row["delay"])
		if delay == "" {
			delay = DelayUnk
		}
		p, err := money.NewPrice(val, unit)
		if err != nil {
			fail("%v", err)
			continue
		}
		o := PriceObs{ListingID: l.ID, Day: row["day"], Kind: kind, Price: p, UnitBasis: basis, Adjustment: adj, Delay: delay, Source: source, SourceRef: fileRef, Received: received}
		if err := o.Validate(); err != nil {
			fail("%v", err)
			continue
		}
		out = append(out, o)
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("quotes: not imported:\n  %s", strings.Join(bad, "\n  "))
	}
	return out, nil
}

func firstErr(es ...error) error {
	for _, e := range es {
		if e != nil {
			return e
		}
	}
	return nil
}
