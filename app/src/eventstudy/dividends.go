package eventstudy

import (
	"fmt"
	"hash"
	"time"
)

// The two ways a report states what its returns include.
const (
	// BasisTotalReturnGross says the stock's returns include the gross cash
	// distributions paid while it was held, not reinvested and before any
	// withholding tax. A report that leaves its basis empty counts prices only.
	BasisTotalReturnGross = "total_return_gross"
)

// Dividend is one cash distribution per share of one security.
type Dividend struct {
	// ExDay is the first day the share trades without the distribution,
	// YYYY-MM-DD.
	ExDay string `json:"ex_day"`
	// Amount is the gross cash per share, in the major unit of Currency, on the
	// price basis of the security's series: the caller has applied the splits.
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency,omitempty"`
	// Unsettled, when not empty, says why the distribution cannot be counted
	// (sources disagree, it was only estimated). A position held over its ex-day
	// is then left out, not scored without it.
	Unsettled string `json:"unsettled,omitempty"`
}

// DividendSource supplies the cash distributions of the securities of a study.
type DividendSource interface {
	// DividendsOf returns the distributions of a security; false when nothing is
	// known about its distributions, which is not the same as having none.
	DividendsOf(security string) ([]Dividend, bool)
}

// MapDividends is a DividendSource over distributions held in memory, by
// security. A security is known when it has an entry, even an empty one.
type MapDividends map[string][]Dividend

// DividendsOf implements DividendSource.
func (m MapDividends) DividendsOf(security string) ([]Dividend, bool) {
	d, ok := m[security]
	return d, ok
}

// PricesWithDividends is Prices that also supplies distributions: what a study
// with total_return needs.
type PricesWithDividends struct {
	Prices
	DividendSource
}

// Validate says why the distribution cannot be used.
func (d Dividend) Validate() error {
	if _, err := time.Parse(time.DateOnly, d.ExDay); err != nil {
		return fmt.Errorf("ex-day %q is not YYYY-MM-DD", d.ExDay)
	}
	if !(d.Amount > 0) {
		return fmt.Errorf("the distribution of %s is not a positive amount", d.ExDay)
	}
	return nil
}

// loadedDividends is the distributions of one security as the run holds them:
// checked once, with the reason the security cannot be measured when it cannot.
type loadedDividends struct {
	list []Dividend
	why  string
}

func (r *run) loadDividends(security string, currency string) *loadedDividends {
	if l, ok := r.divs[security]; ok {
		return l
	}
	l := &loadedDividends{}
	ds, known := r.dividends.DividendsOf(security)
	switch {
	case !known:
		l.why = "no distribution record for " + security
	default:
		for _, d := range ds {
			if err := d.Validate(); err != nil {
				l.why, l.list = "distributions of "+security+": "+err.Error(), nil
				break
			}
			if d.Unsettled == "" && d.Currency != "" && currency != "" && d.Currency != currency {
				d.Unsettled = fmt.Sprintf("paid in %s, the prices are in %s", d.Currency, currency)
			}
			l.list = append(l.list, d)
		}
	}
	r.divs[security] = l
	return l
}

// across returns the gross cash per share a position held from the entry day to
// the close of the exit day received: the distributions whose ex-day falls after
// the entry day (a buyer on or after the ex-day does not get it; one who bought
// the close of the day before does) and not after the exit day (the position
// held the share through the ex-day). The reason is not empty when one of them
// is unsettled.
func (l *loadedDividends) across(entryDay, exitDay string) (cash float64, unsettled string) {
	for _, d := range l.list {
		if d.ExDay <= entryDay || d.ExDay > exitDay {
			continue
		}
		if d.Unsettled != "" {
			return 0, fmt.Sprintf("the distribution of %s: %s", d.ExDay, d.Unsettled)
		}
		cash += d.Amount
	}
	return cash, ""
}

// benchmarkCash is the gross cash per share a benchmark paid between the entry
// day and the exit day, by the rule that counts the stock's (see across), so
// that a study counting the stock's distributions sets it against a benchmark
// that includes its own. It is zero for a study that counts prices only. A
// benchmark with no usable distribution record cannot be told from one that pays
// none: its return stays a price return, and priceOnly says so. False when a
// distribution over the days cannot be counted: the benchmark has no return
// then, which is not a price return passed off as a total one.
func (r *run) benchmarkCash(id, entryDay, exitDay string) (float64, bool) {
	if !r.spec.TotalReturn || id == "" {
		return 0, true
	}
	l := r.loadDividends(id, r.series[id].currency)
	if l.why != "" {
		r.priceOnly[id] = l.why
		return 0, true
	}
	cash, unclear := l.across(entryDay, exitDay)
	return cash, unclear == ""
}

// priceOnlyLimits are the reasons a benchmark that counted prices only keeps
// every group from "evaluated": the stock's return included distributions and
// the benchmark's did not.
func (r *run) priceOnlyLimits() []string {
	var out []string
	for _, id := range sortedKeys(r.priceOnly) {
		out = append(out, "the benchmark "+id+" counts prices only while the returns of the stocks include distributions: "+r.priceOnly[id])
	}
	return out
}

// startDividends prepares the run to count distributions; it is a no-op for a
// study that counts prices only.
func (r *run) startDividends(prices Prices) error {
	if !r.spec.TotalReturn {
		return nil
	}
	src, ok := prices.(DividendSource)
	if !ok || src == nil {
		return fmt.Errorf("eventstudy: study %s counts dividends (total_return) and was given prices that supply none", r.spec.ID)
	}
	r.dividends, r.divs, r.priceOnly = src, map[string]*loadedDividends{}, map[string]string{}
	return nil
}

// writeDividends adds the distributions the run read to the data hash.
func (r *run) writeDividends(h hash.Hash) {
	for _, id := range sortedKeys(r.divs) {
		l := r.divs[id]
		fmt.Fprintf(h, "dividends:%s:%s\n", id, l.why)
		for _, d := range l.list {
			h.Write([]byte(d.ExDay + "," + d.Currency + "," + d.Unsettled + ","))
			writeFloat(h, d.Amount)
			h.Write([]byte{'\n'})
		}
	}
}
