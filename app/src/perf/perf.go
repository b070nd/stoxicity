// Package perf reports what a book actually earned, apart from what was paid in
// and taken out. Everything is derived from the immutable ledger and from
// marks you supply; nothing is stored.
//
// Conventions, stated once:
//
//   - Value (NAV) at the end of a day is cash, unsettled cash and dividend
//     receivables in the base currency, plus every held security at the latest
//     mark on or before that day. Marks are RAW prices as traded on the day, not
//     adjusted for later splits or for dividends; the ledger's own split
//     transactions change the quantity on the split day. Distributions come only
//     from the ledger. An adjusted-close series must never be used here, because
//     it already contains the dividends and they would be counted twice.
//   - An external flow is a deposit, a withdrawal or a transfer, at the end of
//     the day it happened, valued at market (a transferred security at its mark).
//     Contributions and withdrawals are never investment gain.
//   - A correction (reversal) takes effect on the day of the transaction it
//     reverses, so history is restated rather than showing a flow on the day
//     of the correction.
//   - A day with a held security and no mark on or before it, or a foreign amount
//     with no rate, has no value. Anything that needs that value is unavailable
//     and says why; nothing is filled with zero. A mark older than MaxAge days is
//     used but reported as stale.
//   - Time-weighted return chains daily returns (V_d - F_d) / V_(d-1) - 1 with
//     the flows of the day taken at its end. A day that starts with no capital
//     is skipped; a day that starts with negative value makes it unavailable.
//   - Money-weighted return is the annualised internal rate of return of the flows
//     (Actual/365, end of day) with the starting and ending values. All roots in
//     the searched range are reported; none, or several, is said plainly.
//   - Returns and rates are float64 fractions (0.05 is 5%); amounts are exact.
package perf

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

// DefaultMaxAge is how many calendar days old a mark or rate may be before it
// is reported as stale. It covers a weekend and a short holiday.
const DefaultMaxAge = 5

// Mark is a raw closing price of a security on a day.
type Mark struct {
	Security string
	Day      string
	Price    money.Decimal
	Currency money.Currency
}

// FXRate is how many units of the base currency one unit of Currency is worth
// on a day.
type FXRate struct {
	Currency money.Currency
	Day      string
	Value    money.Decimal
}

// Inputs is everything a report is derived from.
type Inputs struct {
	Book       portfolio.Book
	Txs        []portfolio.Tx
	Marks      []Mark
	Rates      []FXRate
	From, To   string
	MaxAge     int
	Benchmarks []Benchmark
}

// Metric is a rate that may be unavailable. Reason says why when it is.
type Metric struct {
	Available  bool     `json:"available"`
	Value      float64  `json:"value,omitempty"`
	Annualised *float64 `json:"annualised,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Notes      []string `json:"notes,omitempty"`
	// Solutions lists every rate found for a money-weighted return.
	Solutions []float64 `json:"solutions,omitempty"`
}

func unavailable(format string, a ...any) Metric {
	return Metric{Reason: fmt.Sprintf(format, a...)}
}

// Point is one day of the series.
type Point struct {
	Day         string `json:"day"`
	Value       *int64 `json:"value,omitempty"` // base minor units; nil when unavailable
	Flow        int64  `json:"flow"`
	Stale       bool   `json:"stale,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

// Components splits the period's gain. Each is in base minor units.
type Components struct {
	Realized    int64 `json:"realized_gain"`
	Unrealized  int64 `json:"unrealized_change"`
	IncomeGross int64 `json:"income_gross"`
	Withholding int64 `json:"withholding"`
	Costs       int64 `json:"costs"`
	// Transfers removes the market-versus-cost step of securities moved in or
	// out: it was not earned here.
	Transfers int64 `json:"transfers_at_market_vs_cost"`
	// FX is what is left: the effect of rates between the day an amount was
	// booked and the day it is valued. It is zero for a book in one currency.
	FX int64 `json:"fx_and_translation"`
}

// Drawdown of the time-weighted index.
type Drawdown struct {
	Available bool    `json:"available"`
	Reason    string  `json:"reason,omitempty"`
	Max       float64 `json:"max"`
	Peak      string  `json:"peak,omitempty"`
	Trough    string  `json:"trough,omitempty"`
	Recovered string  `json:"recovered,omitempty"`
	Current   float64 `json:"current"`
}

// Report is the result.
type Report struct {
	Book        string         `json:"book"`
	Base        money.Currency `json:"base"`
	From        string         `json:"from"`
	To          string         `json:"to"`
	Conventions []string       `json:"conventions"`

	Start         *money.Amount `json:"start_value,omitempty"`
	End           *money.Amount `json:"end_value,omitempty"`
	Contributions *money.Amount `json:"contributions,omitempty"`
	Withdrawals   *money.Amount `json:"withdrawals,omitempty"`
	Gain          *money.Amount `json:"gain,omitempty"`
	GainReason    string        `json:"gain_unavailable,omitempty"`

	Components       *Components `json:"components,omitempty"`
	ComponentsReason string      `json:"components_unavailable,omitempty"`

	TWR      Metric   `json:"twr"`
	MWR      Metric   `json:"mwr"`
	Drawdown Drawdown `json:"drawdown"`

	StaleMarks []string `json:"stale_marks,omitempty"`
	Baselines  []Result `json:"baselines,omitempty"`
	Series     []Point  `json:"series,omitempty"`
}

// ---- days ----

func dayNum(s string) (int, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return 0, fmt.Errorf("perf: %q is not a day YYYY-MM-DD", s)
	}
	return int(t.Unix() / 86400), nil
}

func dayStr(n int) string { return time.Unix(int64(n)*86400, 0).UTC().Format(time.DateOnly) }

// ---- market data ----

type market struct {
	marks  map[string][]Mark
	rates  map[money.Currency][]FXRate
	base   money.Currency
	maxAge int
	stale  map[string]string // security or rate -> first complaint
}

func newMarket(in Inputs) (*market, error) {
	m := &market{marks: map[string][]Mark{}, rates: map[money.Currency][]FXRate{}, base: in.Book.Base, maxAge: in.MaxAge, stale: map[string]string{}}
	if m.maxAge <= 0 {
		m.maxAge = DefaultMaxAge
	}
	for _, k := range in.Marks {
		if _, err := dayNum(k.Day); err != nil {
			return nil, err
		}
		if k.Price.Sign() <= 0 || !k.Currency.Valid() {
			return nil, fmt.Errorf("perf: the mark of %s on %s needs a positive price and a known currency", k.Security, k.Day)
		}
		m.marks[k.Security] = append(m.marks[k.Security], k)
	}
	for _, r := range in.Rates {
		if _, err := dayNum(r.Day); err != nil {
			return nil, err
		}
		if r.Value.Sign() <= 0 || !r.Currency.Valid() || r.Currency == in.Book.Base {
			return nil, fmt.Errorf("perf: the rate of %s on %s needs a positive value, a known currency other than the base", r.Currency, r.Day)
		}
		m.rates[r.Currency] = append(m.rates[r.Currency], r)
	}
	for _, v := range m.marks {
		sort.SliceStable(v, func(i, j int) bool { return v[i].Day < v[j].Day })
	}
	for _, v := range m.rates {
		sort.SliceStable(v, func(i, j int) bool { return v[i].Day < v[j].Day })
	}
	return m, nil
}

// markAt is the latest mark on or before day, how many days old it is, and
// whether there is one.
func (m *market) markAt(security, day string) (Mark, int, bool) {
	v := m.marks[security]
	i := sort.Search(len(v), func(i int) bool { return v[i].Day > day })
	if i == 0 {
		return Mark{}, 0, false
	}
	a, _ := dayNum(day)
	b, _ := dayNum(v[i-1].Day)
	return v[i-1], a - b, true
}

func (m *market) rateAt(cur money.Currency, day string) (money.Decimal, int, bool) {
	v := m.rates[cur]
	i := sort.Search(len(v), func(i int) bool { return v[i].Day > day })
	if i == 0 {
		return money.Decimal{}, 0, false
	}
	a, _ := dayNum(day)
	b, _ := dayNum(v[i-1].Day)
	return v[i-1].Value, a - b, true
}

// toBase converts minor units of cur to the base currency at the rate of day.
// stale says the rate used was older than the allowance.
func (m *market) toBase(minor int64, cur money.Currency, day string) (out int64, stale bool, err error) {
	if cur == m.base {
		return minor, false, nil
	}
	rate, age, ok := m.rateAt(cur, day)
	if !ok {
		return 0, false, fmt.Errorf("no %s rate on or before %s", cur, day)
	}
	a, err := money.FromMinor(minor, cur)
	if err != nil {
		return 0, false, err
	}
	exp, err := m.base.Exponent()
	if err != nil {
		return 0, false, err
	}
	d, err := a.Decimal().Mul(rate, uint8(exp), money.HalfEven)
	if err != nil {
		return 0, false, err
	}
	b, err := money.NewAmount(d, m.base, money.HalfEven)
	if err != nil {
		return 0, false, err
	}
	if age > m.maxAge {
		m.stale["rate "+string(cur)] = fmt.Sprintf("the %s rate used on %s is %d days old", cur, day, age)
		return b.Minor(), true, nil
	}
	return b.Minor(), false, nil
}

// ---- the ledger, day by day ----

type position struct {
	qty      money.Decimal
	costBase int64
}

type dayState struct {
	nav, mv, cost int64
	ok, stale     bool
	reason        string
}

type txFact struct {
	tx   portfolio.Tx
	day  int
	root portfolio.Kind
}

// effective lists the transactions with the day each takes effect and the kind
// it is when reversals are followed to their origin.
func effective(txs []portfolio.Tx) ([]txFact, error) {
	byID := map[string]portfolio.Tx{}
	for _, t := range txs {
		byID[t.ID] = t
	}
	out := make([]txFact, 0, len(txs))
	for _, t := range txs {
		origin, hops := t, 0
		for origin.Reverses != "" {
			next, ok := byID[origin.Reverses]
			if !ok || hops > len(txs) {
				return nil, fmt.Errorf("perf: %s reverses %s, which is not in the book", origin.ID, origin.Reverses)
			}
			origin, hops = next, hops+1
		}
		// a reversal takes the day, and the kind, of the transaction it undoes
		out = append(out, txFact{tx: t, day: int(origin.Event.UTC().Unix() / 86400), root: origin.Kind})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].day != out[j].day {
			return out[i].day < out[j].day
		}
		return out[i].tx.Seq < out[j].tx.Seq
	})
	return out, nil
}

type accumulators struct {
	flow, realized, income, withhold, costs, transfers []int64
}

// Compute derives the report. It fails only for inputs it cannot read; missing
// marks or rates make the affected figures unavailable instead.
func Compute(in Inputs) (Report, error) {
	b := in.Book
	rep := Report{Book: b.ID, Base: b.Base, From: in.From, To: in.To, Conventions: Conventions()}
	if !b.Base.Valid() {
		return rep, errors.New("perf: the book has no valid base currency")
	}
	to, err := dayNum(in.To)
	if err != nil {
		return rep, err
	}
	mk, err := newMarket(in)
	if err != nil {
		return rep, err
	}
	facts, err := effective(in.Txs)
	if err != nil {
		return rep, err
	}
	if len(facts) == 0 {
		return rep, errors.New("perf: the book has no transactions")
	}
	first := facts[0].day
	from := first
	if in.From != "" {
		if from, err = dayNum(in.From); err != nil {
			return rep, err
		}
	}
	if from > to {
		return rep, fmt.Errorf("perf: the period starts (%s) after it ends (%s)", dayStr(from), dayStr(to))
	}
	rep.From, rep.To = dayStr(from), dayStr(to)
	d0 := first
	n := to - d0 + 1
	if n < 1 {
		return rep, fmt.Errorf("perf: the period ends (%s) before the first transaction (%s)", rep.To, dayStr(first))
	}

	cash := map[money.Currency]int64{}
	pos := map[string]*position{}
	secCur := map[string]money.Currency{}
	days := make([]dayState, n)
	acc := accumulators{make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n)}
	var flowErr, compErr string
	setErr := func(dst *string, e error) {
		if *dst == "" && e != nil {
			*dst = e.Error()
		}
	}

	next := 0
	for i := 0; i < n; i++ {
		day := d0 + i
		ds := dayStr(day)
		for ; next < len(facts) && facts[next].day <= day; next++ {
			f := facts[next]
			if f.day < d0 {
				continue
			}
			idx := f.day - d0
			isFlow := f.root == portfolio.Deposit || f.root == portfolio.Withdrawal || f.root == portfolio.TransferIn || f.root == portfolio.TransferOut
			fds := dayStr(f.day)
			for _, e := range f.tx.Entries {
				cur := e.Amount.Currency()
				minor := e.Amount.Minor()
				conv := func(v int64) int64 {
					x, _, err := mk.toBase(v, cur, fds)
					setErr(&compErr, err)
					return x
				}
				switch {
				case e.Account == "cash" || e.Account == "unsettled" || e.Account == "receivable:dividend":
					cash[cur] += minor
					if isFlow && e.Account == "cash" {
						x, _, err := mk.toBase(minor, cur, fds)
						setErr(&flowErr, err)
						acc.flow[idx] += x
					}
				case len(e.Account) > 11 && e.Account[:11] == "securities:":
					p := pos[e.Security]
					if p == nil {
						p = &position{}
						pos[e.Security] = p
					}
					secCur[e.Security] = cur
					q, err := p.qty.Add(e.Qty)
					if err != nil {
						return rep, err
					}
					p.qty = q
					cb := conv(minor)
					p.costBase += cb
					if q.IsZero() {
						p.costBase = 0
					}
					if isFlow {
						// a transferred security is a flow at its market value
						mv, err := positionValue(mk, e.Security, e.Qty, fds)
						setErr(&flowErr, err)
						acc.flow[idx] += mv
						acc.transfers[idx] += mv - cb
					}
				case e.Account == "gain:realized":
					acc.realized[idx] += conv(-minor)
				case len(e.Account) > 7 && e.Account[:7] == "income:":
					acc.income[idx] += conv(-minor)
				case e.Account == "expense:withholding":
					acc.withhold[idx] += conv(minor)
				case e.Account == "expense:commission" || e.Account == "expense:fee":
					acc.costs[idx] += conv(minor)
				}
			}
		}
		days[i] = valueOf(mk, cash, pos, ds)
	}

	// the series
	for i := 0; i < n; i++ {
		d := days[i]
		pt := Point{Day: dayStr(d0 + i), Flow: acc.flow[i], Stale: d.stale}
		if d.ok {
			v := d.nav
			pt.Value = &v
		} else {
			pt.Unavailable = d.reason
		}
		if d0+i >= from {
			rep.Series = append(rep.Series, pt)
		}
	}
	for _, v := range mk.stale {
		rep.StaleMarks = append(rep.StaleMarks, v)
	}
	sort.Strings(rep.StaleMarks)

	at := func(day int) (dayState, bool) {
		if day < d0 {
			return dayState{ok: true}, true // before the first transaction there is nothing
		}
		if day-d0 >= n {
			return dayState{}, false
		}
		return days[day-d0], true
	}
	startS, _ := at(from - 1)
	endS, _ := at(to)
	amount := func(v int64) *money.Amount { a, _ := money.FromMinor(v, b.Base); return &a }

	var contrib, withdr, net int64
	for day := from; day <= to; day++ {
		f := acc.flow[day-d0]
		net += f
		if f > 0 {
			contrib += f
		} else {
			withdr += -f
		}
	}
	switch {
	case flowErr != "":
		rep.GainReason = "a flow could not be valued: " + flowErr
	case !startS.ok:
		rep.GainReason = "the starting value is unavailable: " + startS.reason
	case !endS.ok:
		rep.GainReason = "the ending value is unavailable: " + endS.reason
	default:
		rep.Start, rep.End = amount(startS.nav), amount(endS.nav)
		rep.Contributions, rep.Withdrawals = amount(contrib), amount(withdr)
		gain := endS.nav - startS.nav - net
		rep.Gain = amount(gain)
		if compErr != "" {
			rep.ComponentsReason = compErr
		} else {
			c := Components{}
			for day := from; day <= to; day++ {
				i := day - d0
				c.Realized += acc.realized[i]
				c.IncomeGross += acc.income[i]
				c.Withholding += acc.withhold[i]
				c.Costs += acc.costs[i]
				c.Transfers += acc.transfers[i]
			}
			c.Unrealized = (endS.mv - endS.cost) - (startS.mv - startS.cost)
			c.FX = gain - (c.Realized + c.Unrealized + c.IncomeGross - c.Withholding - c.Costs - c.Transfers)
			rep.Components = &c
		}
	}

	rep.TWR, rep.Drawdown = timeWeighted(days, acc.flow, d0, from, to, flowErr)
	rep.MWR = moneyWeighted(startS, endS, acc.flow, d0, from, to, flowErr)

	for _, bm := range in.Benchmarks {
		rep.Baselines = append(rep.Baselines, baseline(bm, mk, startS, endS, acc.flow, d0, from, to, flowErr, rep.End))
	}
	return rep, nil
}

func positionValue(mk *market, security string, qty money.Decimal, day string) (int64, error) {
	k, age, ok := mk.markAt(security, day)
	if !ok {
		return 0, fmt.Errorf("no price for %s on or before %s", security, day)
	}
	exp, err := k.Currency.Exponent()
	if err != nil {
		return 0, err
	}
	d, err := qty.Mul(k.Price, uint8(exp), money.HalfEven)
	if err != nil {
		return 0, err
	}
	a, err := money.NewAmount(d, k.Currency, money.HalfEven)
	if err != nil {
		return 0, err
	}
	v, _, err := mk.toBase(a.Minor(), k.Currency, day)
	if age > mk.maxAge {
		mk.stale[security] = fmt.Sprintf("the price of %s used on %s is %d days old (from %s)", security, day, age, k.Day)
	}
	return v, err
}

func valueOf(mk *market, cash map[money.Currency]int64, pos map[string]*position, day string) dayState {
	var s dayState
	s.ok = true
	curs := make([]string, 0, len(cash))
	for c := range cash {
		curs = append(curs, string(c))
	}
	sort.Strings(curs)
	for _, cs := range curs {
		v, st, err := mk.toBase(cash[money.Currency(cs)], money.Currency(cs), day)
		if err != nil {
			return dayState{reason: err.Error()}
		}
		s.stale = s.stale || st
		s.nav += v
	}
	ids := make([]string, 0, len(pos))
	for id := range pos {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := pos[id]
		if p.qty.IsZero() {
			continue
		}
		v, err := positionValue(mk, id, p.qty, day)
		if err != nil {
			return dayState{reason: err.Error()}
		}
		if _, age, _ := mk.markAt(id, day); age > mk.maxAge {
			s.stale = true
		}
		s.mv += v
		s.cost += p.costBase
		s.nav += v
	}
	return s
}

// Conventions are the documented rules, repeated in every report.
func Conventions() []string {
	return []string{
		"value = cash + unsettled cash + dividend receivables + securities at the latest raw mark on or before the day; marks are as traded, not adjusted for splits or dividends",
		"deposits, withdrawals and transfers are external flows at the end of their day and are never gain; a transferred security is a flow at its mark",
		"income comes only from the ledger: using an adjusted-close series with it would count the dividends twice",
		"a day without a mark or rate has no value; what needs it is unavailable and says so, never zero; marks older than the allowance are used but listed as stale",
		"time-weighted return chains (V_d - F_d) / V_(d-1) - 1; money-weighted return is the annualised IRR on Actual/365 with every root reported",
		"a correction takes effect on the day of the transaction it reverses",
		"fx_and_translation is the remainder after the other components; it is zero for a book in one currency",
	}
}

// ---- time-weighted return and drawdown ----

func timeWeighted(days []dayState, flow []int64, d0, from, to int, flowErr string) (Metric, Drawdown) {
	if flowErr != "" {
		r := "a flow could not be valued: " + flowErr
		return unavailable("%s", r), Drawdown{Reason: r}
	}
	index := 1.0
	m := Metric{Available: true}
	dd := Drawdown{Available: true}
	peak, peakDay := 1.0, dayStr(from-1)
	started := false
	skipped := 0
	for day := from; day <= to; day++ {
		i := day - d0
		var prev dayState
		if i == 0 {
			prev = dayState{ok: true}
		} else {
			prev = days[i-1]
		}
		cur := days[i]
		if !cur.ok {
			r := fmt.Sprintf("no value on %s: %s", dayStr(day), cur.reason)
			return unavailable("%s", r), Drawdown{Reason: r}
		}
		if !prev.ok {
			r := fmt.Sprintf("no value on %s: %s", dayStr(day-1), prev.reason)
			return unavailable("%s", r), Drawdown{Reason: r}
		}
		if prev.nav < 0 {
			r := fmt.Sprintf("the value at the start of %s is negative (%d minor units): a return on it is not defined", dayStr(day), prev.nav)
			return unavailable("%s", r), Drawdown{Reason: r}
		}
		if prev.nav == 0 {
			skipped++
			continue
		}
		r := float64(cur.nav-flow[i])/float64(prev.nav) - 1
		if r < -1 {
			rs := fmt.Sprintf("the return on %s is below -100%% (the value became negative)", dayStr(day))
			return unavailable("%s", rs), Drawdown{Reason: rs}
		}
		started = true
		index *= 1 + r
		if index > peak {
			peak, peakDay = index, dayStr(day)
			if dd.Trough != "" && dd.Recovered == "" {
				dd.Recovered = dayStr(day)
			}
		}
		if cur := index/peak - 1; cur < dd.Max {
			dd.Max, dd.Trough, dd.Peak, dd.Recovered = cur, dayStr(day), peakDay, ""
		}
		dd.Current = index/peak - 1
	}
	if !started {
		m = unavailable("there was no capital at the start of any day in the period")
		return m, Drawdown{Reason: m.Reason}
	}
	m.Value = index - 1
	span := to - from + 1
	if span >= 365 {
		a := math.Pow(index, 365/float64(span)) - 1
		m.Annualised = &a
	} else {
		m.Notes = append(m.Notes, fmt.Sprintf("the period is %d days: not annualised", span))
	}
	if skipped > 0 {
		m.Notes = append(m.Notes, fmt.Sprintf("%d day(s) started with no capital and were skipped", skipped))
	}
	return m, dd
}

// ---- money-weighted return ----

type cashflow struct {
	t  float64 // years from the first flow
	cf float64
}

func moneyWeighted(start, end dayState, flow []int64, d0, from, to int, flowErr string) Metric {
	if flowErr != "" {
		return unavailable("a flow could not be valued: %s", flowErr)
	}
	if !start.ok {
		return unavailable("the starting value is unavailable: %s", start.reason)
	}
	if !end.ok {
		return unavailable("the ending value is unavailable: %s", end.reason)
	}
	type dayCF struct {
		day int
		cf  int64
	}
	var cfs []dayCF
	if start.nav != 0 {
		cfs = append(cfs, dayCF{from - 1, -start.nav})
	}
	for day := from; day <= to; day++ {
		if f := flow[day-d0]; f != 0 {
			cfs = append(cfs, dayCF{day, -f})
		}
	}
	if end.nav != 0 {
		cfs = append(cfs, dayCF{to, end.nav})
	}
	merged := map[int]int64{}
	for _, c := range cfs {
		merged[c.day] += c.cf
	}
	var ds []int
	for d, v := range merged {
		if v != 0 {
			ds = append(ds, d)
		}
	}
	sort.Ints(ds)
	var flows []cashflow
	var pos, neg bool
	for _, d := range ds {
		flows = append(flows, cashflow{t: float64(d-ds[0]) / 365, cf: float64(merged[d])})
		pos = pos || merged[d] > 0
		neg = neg || merged[d] < 0
	}
	if len(flows) < 2 || !(pos && neg) {
		if neg && !pos && end.nav == 0 {
			return unavailable("no rate solves it: everything paid in is gone (the rate tends to -100%%)")
		}
		return unavailable("no rate solves it: the cash flows do not change sign")
	}
	roots := solveIRR(flows)
	span := ds[len(ds)-1] - ds[0]
	switch len(roots) {
	case 0:
		return unavailable("no annual rate between -99.99%% and 7e12%% solves it")
	case 1:
		m := Metric{Available: true, Value: roots[0], Solutions: roots}
		if span < 365 {
			m.Notes = append(m.Notes, fmt.Sprintf("the flows span %d days: the annual rate extrapolates; over the period it is %.4f%%", span, 100*(math.Pow(1+roots[0], float64(span)/365)-1)))
		}
		return m
	}
	m := Metric{Solutions: roots}
	m.Reason = fmt.Sprintf("%d different rates solve it, so none is the money-weighted return", len(roots))
	return m
}

func solveIRR(f []cashflow) []float64 {
	npv := func(x float64) float64 {
		s := 0.0
		for _, c := range f {
			s += c.cf * math.Exp(-x*c.t)
		}
		return s
	}
	var roots []float64
	lo, hi, step := -9.0, 25.0, 0.002
	prevX, prevV := lo, npv(lo)
	for x := lo + step; x <= hi; x += step {
		v := npv(x)
		switch {
		case v == 0:
			roots = append(roots, math.Exp(x)-1)
		case prevV != 0 && (prevV < 0) != (v < 0):
			a, b := prevX, x
			for i := 0; i < 200; i++ {
				mid := (a + b) / 2
				if (npv(mid) < 0) == (prevV < 0) {
					a = mid
				} else {
					b = mid
				}
			}
			roots = append(roots, math.Exp((a+b)/2)-1)
		}
		prevX, prevV = x, v
	}
	sort.Float64s(roots)
	var out []float64
	for _, r := range roots {
		if len(out) == 0 || math.Abs(r-out[len(out)-1]) > 1e-9 {
			out = append(out, r)
		}
	}
	return out
}
