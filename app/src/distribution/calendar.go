package distribution

import (
	"sort"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Total adds up per-share amounts of one currency and type. Declared and
// estimated are never added together, and currencies are never converted here.
// These are per-share amounts, not cash.
type Total struct {
	Currency  money.Currency
	Type      Type
	Declared  money.Decimal
	Estimated money.Decimal
	NDeclared int
	NEst      int
}

// Calendar is the payouts with an ex-date in a range, with what is unsettled
// counted rather than hidden.
type Calendar struct {
	From, To    string
	Entries     []Payout
	Totals      []Total
	Cancelled   int
	Conflicting int
}

// BuildCalendar lists the payouts whose ex-date is from..to (inclusive;
// either may be empty for unbounded). Conflicting payouts are listed and
// excluded from every total; cancelled ones are listed and counted.
func BuildCalendar(ps []Payout, from, to string) Calendar {
	c := Calendar{From: from, To: to}
	totals := map[[2]string]*Total{}
	for _, p := range ps {
		if (from != "" && p.ExDay < from) || (to != "" && p.ExDay > to) {
			continue
		}
		c.Entries = append(c.Entries, p)
		switch p.State {
		case StateCancelled:
			c.Cancelled++
			continue
		case StateConflicting:
			c.Conflicting++
			continue
		}
		k := [2]string{string(p.Currency), string(p.Key.Type)}
		t := totals[k]
		if t == nil {
			t = &Total{Currency: p.Currency, Type: p.Key.Type}
			totals[k] = t
		}
		sum := &t.Declared
		n := &t.NDeclared
		if p.State == StateEstimated {
			sum, n = &t.Estimated, &t.NEst
		}
		if s, err := sum.Add(p.Amount); err == nil {
			*sum = s
			*n++
		}
	}
	for _, t := range totals {
		c.Totals = append(c.Totals, *t)
	}
	sort.Slice(c.Totals, func(i, j int) bool {
		if c.Totals[i].Currency != c.Totals[j].Currency {
			return c.Totals[i].Currency < c.Totals[j].Currency
		}
		return c.Totals[i].Type < c.Totals[j].Type
	})
	return c
}
