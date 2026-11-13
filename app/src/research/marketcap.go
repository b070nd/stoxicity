package research

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/marketdata"
)

const (
	// maxShareAge is how old, in days, the date of a share count may be at the day it is
	// used: a company that files reports a count every quarter, so an older one is
	// a company whose count is not known.
	maxShareAge = 480
	// maxPriceAge is how many days before the day of use the last close may lie.
	maxPriceAge = 10
	// yearLength is the number of days above which a fiscal period counts as a year.
	yearLength = 300
)

// shareCount is the shares an issuer had on a date, as its filing reported them.
type shareCount struct {
	count float64
	end   string
}

// sharesOutstanding reads from the facts of one issuer the shares it had, as known on the day
// before: the latest cover-page count (metric Shares) filed on or before that day, and no
// fact filed later. Only when the issuer has no cover-page count filed by then does it take
// the latest annual weighted average (metric AvgShares), which is a looser basis: it is an
// average over a past period, not the count on a date.
//
// It gives no count, rather than a guess, when the newest cover page states several counts
// for one date (a dual-class issuer: the cover reports a count per class, and one class alone
// would understate the company), when a count carries a dimension, or when the date of the
// count lies more than maxShareAge days before the day.
func sharesOutstanding(facts []fundamentals.Fact, before string) (shareCount, bool) {
	var cover, average []fundamentals.Fact
	for _, f := range facts {
		if f.Unit != "shares" || f.Filed == "" || f.Filed > before {
			continue
		}
		switch f.Metric {
		case fundamentals.Shares:
			cover = append(cover, f)
		case fundamentals.AvgShares:
			if f.Dimensions == "" && f.Start != "" && f.End != "" {
				average = append(average, f)
			}
		}
	}
	if len(cover) > 0 {
		return singleCount(newestFiling(cover), before)
	}
	if len(average) == 0 {
		return shareCount{}, false
	}
	group := newestFiling(average)
	shortest := -1
	for _, f := range group {
		if n, ok := days(f.Start, f.End); ok && (shortest < 0 || n < shortest) {
			shortest = n
		}
	}
	var same []fundamentals.Fact
	for _, f := range group {
		if n, ok := days(f.Start, f.End); ok && n == shortest {
			same = append(same, f)
		}
	}
	return singleCount(same, before)
}

// newestFiling returns the facts that share the filing day, accession and date of the newest
// of them: latest filing day, then latest date, then greatest accession.
func newestFiling(facts []fundamentals.Fact) []fundamentals.Fact {
	best := facts[0]
	for _, f := range facts[1:] {
		switch {
		case f.Filed != best.Filed:
			if f.Filed > best.Filed {
				best = f
			}
		case f.End != best.End:
			if f.End > best.End {
				best = f
			}
		case f.Accession > best.Accession:
			best = f
		}
	}
	var out []fundamentals.Fact
	for _, f := range facts {
		if f.Filed == best.Filed && f.End == best.End && f.Accession == best.Accession {
			out = append(out, f)
		}
	}
	return out
}

// singleCount is the one count the facts agree on; false when they differ, carry a dimension,
// state a count that is not above zero, or are older than maxShareAge days on the day.
func singleCount(facts []fundamentals.Fact, day string) (shareCount, bool) {
	first := facts[0]
	for _, f := range facts {
		if f.Dimensions != "" || f.Value != first.Value {
			return shareCount{}, false
		}
	}
	age, ok := days(first.End, day)
	if !ok || age < 0 || age > maxShareAge || first.Value <= 0 {
		return shareCount{}, false
	}
	return shareCount{count: first.Value, end: first.End}, true
}

// through brings a count to a price basis: it is multiplied by the ratio of every split of the
// security that took effect after the date of the count and, when day is not empty, by that day.
// With an empty day it is the basis of a series put on one basis after every known split.
func (c shareCount) through(security string, splits []marketdata.Split, day string) float64 {
	n := c.count
	for _, s := range splits {
		if s.Day > c.end && (day == "" || s.Day <= day) && marketdata.SameTicker(s.Ticker, security) {
			n *= s.Ratio()
		}
	}
	return n
}

// closeBefore is the last close on or before the day; false when the series has no bar that day
// or within maxPriceAge days before it.
func closeBefore(bars []eventstudy.Bar, day string) (float64, bool) {
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Day > day })
	if i == 0 {
		return 0, false
	}
	b := bars[i-1]
	if age, ok := days(b.Day, day); !ok || age > maxPriceAge || b.Close <= 0 {
		return 0, false
	}
	return b.Close, true
}

// annualRevenue is the revenue of the latest fiscal year whose facts had been filed by the day
// and that ended no more than maxShareAge days before it; false when there is none.
func annualRevenue(facts []fundamentals.Fact, day string) (float64, bool) {
	var best fundamentals.Fact
	found := false
	for p, f := range periodsOf(facts, fundamentals.Revenue, "USD", day) {
		if p.length() <= yearLength || f.Value <= 0 {
			continue
		}
		if age, ok := days(p.end, day); !ok || age < 0 || age > maxShareAge {
			continue
		}
		if !found || p.end > best.End || (p.end == best.End && f.Filed > best.Filed) {
			best, found = f, true
		}
	}
	return best.Value, found
}

// SetSizes sets the market capitalisation of each observation and, for buybacks and contracts,
// the ratios that put the event's figures against the size of the company.
//
// The capitalisation is the shares outstanding times the last close strictly before the day the
// event became available (a UTC day: for an event with day precision the day itself is not
// finished, and a time-precise event is treated the same, so that no price of the day is read).
// The shares are the latest count the issuer's filings had reported by the day before (see
// sharesOutstanding for the rules, the looser fallback and the dual-class limit), brought by the
// splits to the basis of the price series, which is the basis after the last known split. It is
// in the currency of the series, and is left at zero whenever the issuer, the shares, the series
// or a recent close is missing: it is never guessed. A capitalisation already set is kept.
//
// The ratios are added to Numbers, from the figures of the event's record (records are matched
// by observation ID), only when every input exists and is in one currency:
//
//   - pct_of_market_cap, of a buyback: amount over the capitalisation; of a contract: value over
//     the capitalisation.
//   - pct_of_shares_outstanding, of a buyback: the shares authorised over the shares outstanding.
//   - pct_of_revenue, of a contract: the annual value (stated or worked out from value and
//     duration; a total value alone says nothing about a year) over the revenue of the latest
//     fiscal year filed by then.
//
// facts are the issuers' facts by CIK, prices the series on one price basis and splits the
// splits known at the same time as the prices.
func SetSizes(obs []eventstudy.Observation, records []eventfact.Record, facts map[string][]fundamentals.Fact, prices eventstudy.Prices, splits []marketdata.Split) {
	byID := make(map[string]eventfact.Record, len(records))
	for _, r := range records {
		byID[r.ID] = r
	}
	for i := range obs {
		o := &obs[i]
		r, ok := byID[o.ID]
		if !ok || r.CIK == "" {
			continue
		}
		before := o.AvailableAt.UTC().AddDate(0, 0, -1).Format(time.DateOnly)
		issuer := facts[r.CIK]
		series, hasSeries := prices.Series(o.Security)
		held, hasShares := sharesOutstanding(issuer, before)

		capital := o.MarketCap
		if capital == 0 && hasSeries && hasShares {
			if px, ok := closeBefore(series.Bars, before); ok {
				capital = held.through(o.Security, splits, "") * px
				o.MarketCap = capital
			}
		}
		money := func(f eventfact.Fact) bool {
			return strings.EqualFold(f.Unit, "USD") && (series.Currency == "" || strings.EqualFold(series.Currency, "USD"))
		}
		setRatio := func(name string, part, whole float64) {
			if _, present := o.Numbers[name]; present || part <= 0 || whole <= 0 {
				return
			}
			if o.Numbers == nil {
				o.Numbers = map[string]float64{}
			}
			o.Numbers[name] = part / whole
		}
		switch r.Category {
		case eventfact.Buyback:
			if f, ok := r.Fact("amount"); ok && money(f) && hasSeries {
				setRatio("pct_of_market_cap", f.Value, capital)
			}
			if f, ok := r.Fact("shares"); ok && f.Unit == "shares" && hasShares {
				setRatio("pct_of_shares_outstanding", f.Value, held.through(o.Security, splits, before))
			}
		case eventfact.Contract:
			if f, ok := r.Fact("value"); ok && money(f) && hasSeries {
				setRatio("pct_of_market_cap", f.Value, capital)
			}
			if f, ok := r.Fact("annual_value"); ok && strings.EqualFold(f.Unit, "USD") {
				if revenue, ok := annualRevenue(issuer, before); ok {
					setRatio("pct_of_revenue", f.Value, revenue)
				}
			}
		}
	}
}

// sizeObservations reads what SetSizes needs of the store: the facts of each issuer that has a
// series among the prices (once per issuer) and the splits known at the time, zero for as they
// stand. Observations whose security has no series are left as they are: nothing is measured
// for them.
func sizeObservations(ctx context.Context, s Store, obs []eventstudy.Observation, records []eventfact.Record, prices eventstudy.Prices, known time.Time) error {
	ciks := map[string]string{}
	for _, r := range records {
		ciks[r.ID] = r.CIK
	}
	facts := map[string][]fundamentals.Fact{}
	for _, o := range obs {
		cik := ciks[o.ID]
		if _, ok := prices.Series(o.Security); !ok || cik == "" {
			continue
		}
		if _, cached := facts[cik]; cached {
			continue
		}
		fs, err := s.FactHistory(ctx, cik)
		if err != nil {
			return fmt.Errorf("research: fundamentals of %s: %w", cik, err)
		}
		facts[cik] = fs
	}
	var splits []marketdata.Split
	var err error
	if known.IsZero() {
		splits, err = s.Splits(ctx)
	} else {
		splits, err = s.SplitsAsOf(ctx, known)
	}
	if err != nil {
		return fmt.Errorf("research: splits: %w", err)
	}
	SetSizes(obs, records, facts, prices, splits)
	return nil
}
