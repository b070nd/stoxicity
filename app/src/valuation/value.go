package valuation

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

// The limits inputs are rejected beyond.
const (
	// MaxCountAge is how old a count may be. Annual and quarterly reports follow
	// each other in under half a year; a count older than this has been replaced
	// by a report that has not been read.
	MaxCountAge = 200 * 24 * time.Hour
	// MaxPriceAge is how old the price session may be.
	MaxPriceAge = 10 * 24 * time.Hour
	// ReportCurrency is the currency capitalisations are reported in. It is a
	// choice of output, not an assumption about the inputs: every price carries its
	// own quote currency, and one in another currency is converted only with a
	// dated, sourced rate (Rate) and rejected without.
	ReportCurrency = "USD"
	// MaxRateAge is how far from the price session a rate's day may be.
	MaxRateAge = 4 * 24 * time.Hour
)

// Rate is the price of one unit of a currency in another on a day, from a
// source. Without a source or a day it is not used.
type Rate struct {
	From, To string
	Day      string // YYYY-MM-DD
	Rate     float64
	Source   string
}

// Price is the last stored session of a security.
type Price struct {
	Ticker string
	Day    string // YYYY-MM-DD, the session
	Close  float64
	// Currency is what the price source states; empty when it states none.
	Currency string
	// Raw says the price is as traded, not adjusted for splits.
	Raw bool
	// Fetched is when this system stored the price; zero when not recorded.
	Fetched time.Time
}

// Market is what the prices and splits say.
type Market struct {
	// Prices by marketdata.NormTicker.
	Prices map[string]Price
	// Splits by marketdata.NormTicker.
	Splits   map[string][]marketdata.Split
	Coverage marketdata.SplitCoverage
	// Rates convert a quote currency to Output; none are stored yet, so a price in
	// another currency than Output is rejected.
	Rates []Rate
	// Output is the currency the values are in; empty means ReportCurrency.
	Output string
	// View is what the market was read as of.
	View View
}

func (m Market) output() string {
	if m.Output == "" {
		return ReportCurrency
	}
	return m.Output
}

// ClassValue is the value of one class, or why there is none.
type ClassValue struct {
	Class
	Mapping Mapping
	Price   Price
	// SplitFactor is by what the count was multiplied to be on the price's side
	// of the splits between the two dates; 1 when none lies between them.
	SplitFactor float64
	// Splits are the stored splits between the count date and the price session.
	Splits []marketdata.Split
	// QuoteCurrency is the currency the price is quoted in and CurrencyBasis how
	// that is known; Currency is the currency of Cap (the market's output).
	QuoteCurrency, CurrencyBasis, Currency string
	// FX is the rate that converted the price, when the quote currency differs.
	FX *Rate
	// Shares at the price session, after the factor; Cap is Adjusted times price,
	// in Currency.
	Adjusted float64
	Cap      float64
	OK       bool
	// Reason is why the class has no value; empty when OK. Code is the same in a
	// word a program can sort and count by.
	Reason string
	Code   Code
}

// Code says in a word why a class has no value.
type Code string

const (
	CodeNotMapped  Code = "not-mapped"         // no mapping has been made for the class yet
	CodeAmbiguous  Code = "ambiguous"          // the class cannot be told from the issuer's other listings
	CodeUnlisted   Code = "unlisted"           // the class has no listed security
	CodeConflict   Code = "conflict"           // the filing states different counts for the class
	CodeBadCount   Code = "bad-count"          // the count or its date cannot be read
	CodeStaleCount Code = "stale-count"        // the count is too old
	CodeNoPrice    Code = "no-price"           // no usable price is stored
	CodeStalePrice Code = "stale-price"        // the price is too old
	CodeBasis      Code = "price-basis"        // the price is adjusted for splits that were not recorded
	CodeCurrency   Code = "currency"           // the quote currency is unknown, conflicting or unconverted
	CodeSplits     Code = "split-coverage"     // the split list does not cover the dates
	CodeReceipt    Code = "depositary-receipt" // a receipt whose share ratio is not known
)

// Value is the valuation of one issuer.
type Value struct {
	CIK       string
	Accession string
	Form      string
	Filed     string
	Classes   []ClassValue
	// Issuer is the value of every class together; IssuerOK is false, with
	// IssuerReason, when any class cannot be valued.
	Issuer       float64
	IssuerOK     bool
	IssuerReason string
	// Currency is the currency of Issuer and of every class value.
	Currency string
	// View is what the valuation is as of, Coverage what the stored split list
	// covered for it.
	View     View
	Coverage marketdata.SplitCoverage
}

// Compute values the classes of the pick. maps are the mappings in force, by
// dimensions; a class without one is reported as not mapped yet.
func Compute(p Pick, maps map[string]Mapping, m Market, now time.Time) Value {
	v := Value{CIK: p.CIK, Accession: p.Accession, Form: p.Form, Filed: p.Filed, Currency: m.output(), View: m.View, Coverage: m.Coverage}
	var why []string
	for _, c := range p.Classes {
		cv := valueClass(c, maps[c.Dimensions], m, now)
		v.Classes = append(v.Classes, cv)
		if !cv.OK {
			why = append(why, fmt.Sprintf("%s: %s", c.Name(), cv.Reason))
		}
	}
	for _, d := range p.Dropped {
		why = append(why, fmt.Sprintf("%s: stated by the report before and not by %s", ClassName(d), p.Accession))
	}
	switch {
	case len(p.Classes) == 0:
		v.IssuerReason = "no share count"
	case len(why) > 0:
		v.IssuerReason = strings.Join(why, "; ")
	default:
		v.IssuerOK = true
		for _, cv := range v.Classes {
			v.Issuer += cv.Cap
		}
	}
	return v
}

func valueClass(c Class, mp Mapping, m Market, now time.Time) ClassValue {
	cv := ClassValue{Class: c, Mapping: mp, SplitFactor: 1}
	fail := func(code Code, format string, args ...any) ClassValue {
		cv.Code, cv.Reason = code, fmt.Sprintf(format, args...)
		return cv
	}
	if iss := c.Issue(); iss != "" {
		return fail(CodeConflict, "%s", iss)
	}
	switch mp.Status {
	case "":
		return fail(CodeNotMapped, "the class has not been mapped to a security yet")
	case Ambiguous:
		return fail(CodeAmbiguous, "the class cannot be told from the issuer's other listings (%s)", mp.Basis)
	case Unmapped:
		return fail(CodeUnlisted, "no listed security: %s", mp.Basis)
	}
	if mp.Status != Mapped || mp.Ticker == "" {
		return fail(CodeNotMapped, "the class has no security")
	}
	// Whatever price is stored is shown with the class, even when the class fails
	// before the price is judged, so the inputs are complete.
	if pr, ok := m.Prices[marketdata.NormTicker(mp.Ticker)]; ok {
		cv.Price = pr
	}
	asOf, err := time.Parse(time.DateOnly, c.AsOf)
	if err != nil {
		return fail(CodeBadCount, "the count has no readable date (%q)", c.AsOf)
	}
	if now.Sub(asOf) > MaxCountAge {
		return fail(CodeStaleCount, "the count is from %s, more than %d days old", c.AsOf, int(MaxCountAge.Hours()/24))
	}
	if c.Shares <= 0 || math.IsNaN(c.Shares) || math.IsInf(c.Shares, 0) {
		return fail(CodeBadCount, "the count %v is not a number of shares", c.Shares)
	}
	key := marketdata.NormTicker(mp.Ticker)
	pr, ok := m.Prices[key]
	if !ok || pr.Close <= 0 {
		return fail(CodeNoPrice, "no price of %s is stored", mp.Ticker)
	}
	cv.Price = pr
	quote, basis, problem := QuoteCurrency(pr.Currency, mp.Venue)
	if problem != "" {
		return fail(CodeCurrency, "the quote currency of %s is unknown: %s", mp.Ticker, problem)
	}
	cv.QuoteCurrency, cv.CurrencyBasis = quote, basis
	if !pr.Raw {
		return fail(CodeBasis, "the stored price of %s was adjusted for splits that were not recorded, so it cannot be matched with a count", mp.Ticker)
	}
	day, err := time.Parse(time.DateOnly, pr.Day)
	if err != nil {
		return fail(CodeNoPrice, "the price session of %s has no readable date (%q)", mp.Ticker, pr.Day)
	}
	if now.Sub(day) > MaxPriceAge {
		return fail(CodeStalePrice, "the price of %s is from %s, more than %d days old", mp.Ticker, pr.Day, int(MaxPriceAge.Hours()/24))
	}
	if mp.Type == "adr" {
		return fail(CodeReceipt, "%s is a depositary receipt: the number of shares one receipt stands for is not known", mp.Ticker)
	}
	factor, err := splitFactor(m, key, asOf, day)
	if err != nil {
		return fail(CodeSplits, "%v", err)
	}
	cv.SplitFactor = factor
	cv.Splits = splitsBetween(m, key, asOf, day)
	cv.Adjusted = c.Shares * factor
	value, cur := cv.Adjusted*pr.Close, quote
	if out := m.output(); quote != out {
		rate, ok := m.rate(quote, out, day)
		if !ok {
			return fail(CodeCurrency, "%s is quoted in %s and the value is reported in %s: no dated, sourced %s to %s rate is stored for %s, so the value is not converted",
				mp.Ticker, quote, out, quote, out, pr.Day)
		}
		cv.FX, value, cur = &rate, value*rate.Rate, out
	}
	cv.Cap, cv.Currency = value, cur
	cv.OK = true
	return cv
}

// rate finds the rate from one currency to another nearest to the day, within
// MaxRateAge, that has a source and a usable value.
func (m Market) rate(from, to string, day time.Time) (Rate, bool) {
	var best Rate
	bestGap, found := time.Duration(0), false
	for _, r := range m.Rates {
		if r.From != from || r.To != to || r.Source == "" || r.Rate <= 0 || math.IsNaN(r.Rate) || math.IsInf(r.Rate, 0) {
			continue
		}
		d, err := time.Parse(time.DateOnly, r.Day)
		if err != nil {
			continue
		}
		gap := d.Sub(day)
		if gap < 0 {
			gap = -gap
		}
		if gap > MaxRateAge || (found && gap >= bestGap) {
			continue
		}
		best, bestGap, found = r, gap, true
	}
	return best, found
}

// splitsBetween lists the stored splits of the stock that lie between the two
// days, the count date and the price session, as splitFactor reads them.
func splitsBetween(m Market, key string, countDay, priceDay time.Time) []marketdata.Split {
	lo, hi := countDay, priceDay
	if hi.Before(lo) {
		lo, hi = hi, lo
	}
	var out []marketdata.Split
	for _, s := range m.Splits[key] {
		if d, err := time.Parse(time.DateOnly, s.Day); err == nil && d.After(lo) && !d.After(hi) {
			out = append(out, s)
		}
	}
	return out
}

// splitFactor is the multiplier that brings a count made on the count date to
// the price session: the product of the ratios of the splits between the two
// dates. A split on the count date is already in the count; one on the session
// day is already in the raw price. It fails when the stored splits do not cover
// the dates between.
func splitFactor(m Market, key string, countDay, priceDay time.Time) (float64, error) {
	if countDay.Equal(priceDay) {
		return 1, nil
	}
	lo, hi := countDay, priceDay
	if hi.Before(lo) {
		lo, hi = hi, lo
	}
	cov := m.Coverage
	since, err := time.Parse(time.DateOnly, cov.Since)
	if cov.Since == "" || err != nil || since.After(lo) || !cov.Read.After(hi.AddDate(0, 0, 1)) {
		return 0, fmt.Errorf("the stock split list does not cover %s to %s, so a split between the count and the price cannot be ruled out",
			lo.Format(time.DateOnly), hi.Format(time.DateOnly))
	}
	f := 1.0
	for _, s := range m.Splits[key] {
		d, err := time.Parse(time.DateOnly, s.Day)
		if err != nil || s.Ratio() <= 0 {
			continue
		}
		if !countDay.Before(priceDay) {
			// The count is newer than the price: a split in (price, count] is in the
			// count and not in the price.
			if d.After(priceDay) && !d.After(countDay) {
				f /= s.Ratio()
			}
			continue
		}
		if d.After(countDay) && !d.After(priceDay) {
			f *= s.Ratio()
		}
	}
	return f, nil
}
