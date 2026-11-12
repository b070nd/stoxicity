package research

import (
	"math"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
)

// period is the stretch of time a fact covers.
type period struct{ start, end string }

// days is the number of days from a to b (YYYY-MM-DD); false when either is not a day.
func days(a, b string) (int, bool) {
	x, err1 := time.Parse(time.DateOnly, a)
	y, err2 := time.Parse(time.DateOnly, b)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return int(math.Round(y.Sub(x).Hours() / 24)), true
}

func (p period) length() int {
	n, _ := days(p.start, p.end)
	return n
}

// periodsOf keeps, for one metric and unit, the consolidated fact of each period that had been
// filed by day; of several filings of one period the latest wins. A fact without a filing day is
// not used: it cannot be shown to have been public.
func periodsOf(facts []fundamentals.Fact, metric, unit, day string) map[period]fundamentals.Fact {
	out := map[period]fundamentals.Fact{}
	for _, f := range facts {
		if f.Metric != metric || f.Unit != unit || f.Dimensions != "" || f.Start == "" || f.End == "" || f.Filed == "" || f.Filed > day {
			continue
		}
		if _, ok := days(f.Start, f.End); !ok {
			continue
		}
		p := period{f.Start, f.End}
		if cur, ok := out[p]; !ok || f.Filed > cur.Filed || (f.Filed == cur.Filed && f.Accession > cur.Accession) {
			out[p] = f
		}
	}
	return out
}

// earlier finds the period of about the same length that ended 350 to 380 days before cur, the
// one closest to a year; false when there is none.
func earlier(cur period, among map[period]fundamentals.Fact) (fundamentals.Fact, bool) {
	var best fundamentals.Fact
	var bestP period
	bestGap, found := 0, false
	for p, f := range among {
		gap, ok := days(p.end, cur.end)
		if !ok || gap < 350 || gap > 380 {
			continue
		}
		if d := p.length() - cur.length(); d < -10 || d > 10 {
			continue
		}
		off := gap - 365
		if off < 0 {
			off = -off
		}
		if !found || off < bestGap || (off == bestGap && p.end > bestP.end) {
			best, bestP, bestGap, found = f, p, off, true
		}
	}
	return best, found
}

func change(now float64, before fundamentals.Fact, ok bool) *float64 {
	if !ok || before.Value == 0 {
		return nil
	}
	v := (now - before.Value) / math.Abs(before.Value)
	return &v
}

// figuresAsOf reads the reported figures of the most recent fiscal period whose facts had been
// filed by day (YYYY-MM-DD): revenue, net income and earnings per share (net income over the
// weighted average shares of the same period), each with its change on the same period a year
// earlier when that had been filed too. It uses consolidated facts only; false when no revenue
// or net income had been filed.
func figuresAsOf(facts []fundamentals.Fact, day string) (eventfact.Figures, bool) {
	revenue := periodsOf(facts, fundamentals.Revenue, "USD", day)
	income := periodsOf(facts, fundamentals.NetIncome, "USD", day)
	shares := periodsOf(facts, fundamentals.AvgShares, "shares", day)
	eps := map[period]fundamentals.Fact{}
	for p, ni := range income {
		if sh, ok := shares[p]; ok && sh.Value > 0 {
			f := ni
			f.Value = ni.Value / sh.Value
			eps[p] = f
		}
	}

	var cur period
	found := false
	pick := func(m map[period]fundamentals.Fact) {
		for p := range m {
			switch {
			case !found, p.end > cur.end,
				p.end == cur.end && p.length() < cur.length(),
				p.end == cur.end && p.length() == cur.length() && p.start > cur.start:
				cur, found = p, true
			}
		}
	}
	pick(revenue)
	pick(income)
	if !found {
		return eventfact.Figures{}, false
	}

	label := "quarter ending " + cur.end
	if cur.length() > 300 {
		label = "FY ending " + cur.end
	}
	out := eventfact.Figures{Period: label, Currency: "USD"}
	set := func(m map[period]fundamentals.Fact, value **float64, yoy **float64) {
		f, ok := m[cur]
		if !ok {
			return
		}
		v := f.Value
		*value = &v
		before, had := earlier(cur, m)
		*yoy = change(v, before, had)
	}
	set(revenue, &out.Revenue, &out.RevenueYoY)
	set(income, &out.NetIncome, &out.NetIncomeYoY)
	set(eps, &out.EPS, &out.EPSYoY)
	if out.Revenue == nil && out.NetIncome == nil {
		return eventfact.Figures{}, false
	}
	return out, true
}
