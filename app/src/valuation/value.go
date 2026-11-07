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
	// QuoteCurrency is the currency of the stored prices: they are the daily
	// candles of US exchanges.
	QuoteCurrency = "USD"
)

// Price is the last stored session of a security.
type Price struct {
	Ticker   string
	Day      string // YYYY-MM-DD, the session
	Close    float64
	Currency string
	// Raw says the price is as traded, not adjusted for splits.
	Raw bool
}

// Market is what the prices and splits say.
type Market struct {
	// Prices by marketdata.NormTicker.
	Prices map[string]Price
	// Splits by marketdata.NormTicker.
	Splits   map[string][]marketdata.Split
	Coverage marketdata.SplitCoverage
}

// ClassValue is the value of one class, or why there is none.
type ClassValue struct {
	Class
	Mapping Mapping
	Price   Price
	// SplitFactor is by what the count was multiplied to be on the price's side
	// of the splits between the two dates; 1 when none lies between them.
	SplitFactor float64
	// Shares at the price session, after the factor; Cap is Adjusted times price.
	Adjusted float64
	Cap      float64
	OK       bool
	// Reason is why the class has no value; empty when OK.
	Reason string
}

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
}

// Compute values the classes of the pick. maps are the mappings in force, by
// dimensions; a class without one is reported as not mapped yet.
func Compute(p Pick, maps map[string]Mapping, m Market, now time.Time) Value {
	v := Value{CIK: p.CIK, Accession: p.Accession, Form: p.Form, Filed: p.Filed}
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
	fail := func(format string, args ...any) ClassValue {
		cv.Reason = fmt.Sprintf(format, args...)
		return cv
	}
	if iss := c.Issue(); iss != "" {
		return fail("%s", iss)
	}
	switch mp.Status {
	case "":
		return fail("the class has not been mapped to a security yet")
	case Ambiguous:
		return fail("the class cannot be told from the issuer's other listings (%s)", mp.Basis)
	case Unmapped:
		return fail("no listed security: %s", mp.Basis)
	}
	if mp.Status != Mapped || mp.Ticker == "" {
		return fail("the class has no security")
	}
	asOf, err := time.Parse(time.DateOnly, c.AsOf)
	if err != nil {
		return fail("the count has no readable date (%q)", c.AsOf)
	}
	if now.Sub(asOf) > MaxCountAge {
		return fail("the count is from %s, more than %d days old", c.AsOf, int(MaxCountAge.Hours()/24))
	}
	if c.Shares <= 0 || math.IsNaN(c.Shares) || math.IsInf(c.Shares, 0) {
		return fail("the count %v is not a number of shares", c.Shares)
	}
	key := marketdata.NormTicker(mp.Ticker)
	pr, ok := m.Prices[key]
	if !ok || pr.Close <= 0 {
		return fail("no price of %s is stored", mp.Ticker)
	}
	cv.Price = pr
	if pr.Currency == "" {
		return fail("the price of %s has no currency", mp.Ticker)
	}
	if pr.Currency != QuoteCurrency {
		return fail("the price of %s is in %s and the count is of shares, not of a currency this calculation converts", mp.Ticker, pr.Currency)
	}
	if !pr.Raw {
		return fail("the stored price of %s was adjusted for splits that were not recorded, so it cannot be matched with a count", mp.Ticker)
	}
	day, err := time.Parse(time.DateOnly, pr.Day)
	if err != nil {
		return fail("the price session of %s has no readable date (%q)", mp.Ticker, pr.Day)
	}
	if now.Sub(day) > MaxPriceAge {
		return fail("the price of %s is from %s, more than %d days old", mp.Ticker, pr.Day, int(MaxPriceAge.Hours()/24))
	}
	if mp.Type == "adr" {
		return fail("%s is a depositary receipt: the number of shares one receipt stands for is not known", mp.Ticker)
	}
	factor, err := splitFactor(m, key, asOf, day)
	if err != nil {
		return fail("%v", err)
	}
	cv.SplitFactor = factor
	cv.Adjusted = c.Shares * factor
	cv.Cap = cv.Adjusted * pr.Close
	cv.OK = true
	return cv
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
