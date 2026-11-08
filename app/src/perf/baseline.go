package perf

import (
	"fmt"
	"math"
	"sort"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Benchmark is a comparison the same cash flows are replayed into. A "fund"
// buys and sells units of one security at its marks; a "cash" benchmark holds
// the money at a stated annual rate, or at zero.
type Benchmark struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"` // fund or cash
	Security string         `json:"security,omitempty"`
	Currency money.Currency `json:"currency,omitempty"`
	// TradeCost is the fraction of each flow lost to buying or selling; CostKnown
	// says it was stated, with CostSource. An unstated cost is not assumed zero:
	// the result says it is gross of costs.
	TradeCost  money.Decimal `json:"trade_cost,omitempty"`
	CostKnown  bool          `json:"cost_known"`
	CostSource string        `json:"cost_source,omitempty"`
	// Distributions are cash paid per unit on an ex-day, reinvested gross at that
	// day's mark. The marks must be raw prices.
	Distributions []Dist `json:"distributions,omitempty"`
	// AnnualRate and RateSource are for a cash benchmark.
	AnnualRate money.Decimal `json:"annual_rate,omitempty"`
	RateSource string        `json:"rate_source,omitempty"`
}

// Dist is a distribution per unit on an ex-day.
type Dist struct {
	ExDay   string        `json:"ex_day"`
	PerUnit money.Decimal `json:"per_unit"`
}

// Result is a benchmark replayed over the same flows.
type Result struct {
	Name      string        `json:"name"`
	Available bool          `json:"available"`
	Reason    string        `json:"reason,omitempty"`
	End       *money.Amount `json:"end_value,omitempty"`
	Gain      *money.Amount `json:"gain,omitempty"`
	// Excess is the book's end value less the benchmark's with the same flows.
	Excess *money.Amount `json:"excess_end_value,omitempty"`
	TWR    Metric        `json:"twr"`
	MWR    Metric        `json:"mwr"`
	Notes  []string      `json:"notes,omitempty"`
}

func baseline(bm Benchmark, mk *market, start, end dayState, flow []int64, d0, from, to int, flowErr string, bookEnd *money.Amount) Result {
	r := Result{Name: bm.Name}
	fail := func(f string, a ...any) Result { r.Reason = fmt.Sprintf(f, a...); return r }
	if flowErr != "" {
		return fail("the book's flows could not be valued: %s", flowErr)
	}
	if !start.ok || !end.ok {
		return fail("the book's own start or end value is unavailable, so the same flows cannot be replayed")
	}
	// the flows to replay: the starting value, then every external flow
	type fl struct {
		day int
		amt int64
	}
	var fls []fl
	if start.nav != 0 {
		fls = append(fls, fl{from - 1, start.nav})
	}
	for day := from; day <= to; day++ {
		if f := flow[day-d0]; f != 0 {
			fls = append(fls, fl{day, f})
		}
	}
	if len(fls) == 0 {
		return fail("the period has no flows and no starting value: there is nothing to replay")
	}
	cur := bm.Currency
	if cur == "" {
		cur = mk.base
	}
	if !cur.Valid() {
		return fail("the benchmark currency %q is not known", cur)
	}
	if bm.CostKnown && bm.CostSource == "" {
		return fail("a stated trade cost needs its source: an unsourced cost is not used")
	}
	if !bm.CostKnown {
		r.Notes = append(r.Notes, "gross of trading costs: no cost was stated for this benchmark")
	} else {
		r.Notes = append(r.Notes, "trade cost "+bm.TradeCost.String()+" of each flow ("+bm.CostSource+")")
	}
	if cur != mk.base {
		r.Notes = append(r.Notes, "held in "+string(cur)+" and converted at the rate of each day")
	}
	r.Notes = append(r.Notes, "distributions are taken gross and reinvested at the ex-day mark; no withholding is assumed")

	var endBase int64
	switch bm.Kind {
	case "fund":
		units := money.Int(0)
		var dists []Dist
		dists = append(dists, bm.Distributions...)
		sort.Slice(dists, func(i, j int) bool { return dists[i].ExDay < dists[j].ExDay })
		di := 0
		one := money.Int(1)
		cost := money.Int(0)
		if bm.CostKnown {
			cost = bm.TradeCost
		}
		net, err := one.Sub(cost)
		if err != nil || net.Sign() <= 0 {
			return fail("the trade cost %s is not a fraction below 1", bm.TradeCost)
		}
		price := func(day int) (money.Decimal, error) {
			k, age, ok := mk.markAt(bm.Security, dayStr(day))
			if !ok {
				return money.Decimal{}, fmt.Errorf("no benchmark price for %s on or before %s", bm.Security, dayStr(day))
			}
			if k.Currency != cur {
				return money.Decimal{}, fmt.Errorf("the benchmark price of %s is in %s, not %s", bm.Security, k.Currency, cur)
			}
			if age > mk.maxAge {
				r.Notes = append(r.Notes, fmt.Sprintf("the benchmark price used on %s is %d days old", dayStr(day), age))
			}
			return k.Price, nil
		}
		reinvestUpTo := func(day int) error {
			for ; di < len(dists) && dists[di].ExDay <= dayStr(day); di++ {
				if dists[di].ExDay < dayStr(fls[0].day) {
					continue
				}
				p, err := price(mustDay(dists[di].ExDay))
				if err != nil {
					return err
				}
				cashd, err := units.Mul(dists[di].PerUnit, 8, money.HalfEven)
				if err != nil {
					return err
				}
				add, err := cashd.Div(p, 8, money.HalfEven)
				if err != nil {
					return err
				}
				if units, err = units.Add(add); err != nil {
					return err
				}
			}
			return nil
		}
		for _, f := range fls {
			// a unit bought on the ex-day is not entitled: distributions up to and
			// including this day are reinvested on the units held before its trade
			if err := reinvestUpTo(f.day); err != nil {
				return fail("%v", err)
			}
			p, err := price(f.day)
			if err != nil {
				return fail("%v", err)
			}
			amtBench, err := fromBase(mk, f.amt, cur, dayStr(f.day))
			if err != nil {
				return fail("%v", err)
			}
			ad, err := amtBench.Decimal().Abs()
			if err != nil {
				return fail("%v", err)
			}
			if f.amt > 0 {
				invested, err := ad.Mul(net, 12, money.HalfEven)
				if err != nil {
					return fail("%v", err)
				}
				u, err := invested.Div(p, 8, money.HalfEven)
				if err != nil {
					return fail("%v", err)
				}
				if units, err = units.Add(u); err != nil {
					return fail("%v", err)
				}
			} else {
				gross, err := ad.Div(net, 12, money.HalfEven)
				if err != nil {
					return fail("%v", err)
				}
				u, err := gross.Div(p, 8, money.HalfEven)
				if err != nil {
					return fail("%v", err)
				}
				left, err := units.Sub(u)
				if err != nil {
					return fail("%v", err)
				}
				if left.Sign() < 0 {
					r.Notes = append(r.Notes, fmt.Sprintf("the benchmark could not fund the withdrawal of %s: it had fewer units", dayStr(f.day)))
					left = money.Int(0)
				}
				units = left
			}
		}
		if err := reinvestUpTo(to); err != nil {
			return fail("%v", err)
		}
		p, err := price(to)
		if err != nil {
			return fail("%v", err)
		}
		exp, _ := cur.Exponent()
		v, err := units.Mul(p, uint8(exp), money.HalfEven)
		if err != nil {
			return fail("%v", err)
		}
		a, err := money.NewAmount(v, cur, money.HalfEven)
		if err != nil {
			return fail("%v", err)
		}
		endBase, _, err = mk.toBase(a.Minor(), cur, dayStr(to))
		if err != nil {
			return fail("%v", err)
		}
		r.TWR = fundTWR(bm, mk, cur, fls[0].day, to)
	case "cash":
		if bm.AnnualRate.Sign() != 0 && bm.RateSource == "" {
			return fail("a cash rate needs its source: an unsourced rate is not used")
		}
		bal := int64(0)
		prev := fls[0].day
		rf, _ := bm.AnnualRate.Div(money.Int(1), 12, money.HalfEven)
		rate := floatOf(rf)
		for _, f := range fls {
			bal = grow(bal, rate, f.day-prev)
			prev = f.day
			bal += f.amt
		}
		bal = grow(bal, rate, to-prev)
		endBase = bal
		if bm.AnnualRate.Sign() == 0 {
			r.Notes = append(r.Notes, "cash held at 0%: no rate was stated")
		}
		span := to - fls[0].day
		t := math.Pow(1+rate, float64(span)/365) - 1
		r.TWR = Metric{Available: true, Value: t}
	default:
		return fail("the benchmark kind %q is not fund or cash", bm.Kind)
	}

	e, _ := money.FromMinor(endBase, mk.base)
	r.End = &e
	var net int64
	for _, f := range fls {
		net += f.amt
	}
	g, _ := money.FromMinor(endBase-net, mk.base)
	r.Gain = &g
	if bookEnd != nil {
		x, _ := money.FromMinor(bookEnd.Minor()-endBase, mk.base)
		r.Excess = &x
	}
	// the IRR of the same flows with the benchmark's end value
	var cfs []cashflow
	t0 := fls[0].day
	for _, f := range fls {
		cfs = append(cfs, cashflow{t: float64(f.day-t0) / 365, cf: float64(-f.amt)})
	}
	cfs = append(cfs, cashflow{t: float64(to-t0) / 365, cf: float64(endBase)})
	roots := solveIRR(mergeSameTime(cfs))
	switch len(roots) {
	case 1:
		r.MWR = Metric{Available: true, Value: roots[0], Solutions: roots}
	case 0:
		r.MWR = unavailable("no rate solves it")
	default:
		r.MWR = Metric{Solutions: roots, Reason: fmt.Sprintf("%d different rates solve it", len(roots))}
	}
	r.Available = true
	return r
}

func mustDay(s string) int { n, _ := dayNum(s); return n }

func mergeSameTime(in []cashflow) []cashflow {
	var out []cashflow
	for _, c := range in {
		if n := len(out); n > 0 && out[n-1].t == c.t {
			out[n-1].cf += c.cf
			continue
		}
		out = append(out, c)
	}
	return out
}

func floatOf(d money.Decimal) float64 {
	var f float64
	fmt.Sscanf(d.String(), "%g", &f)
	return f
}

// grow compounds a balance daily at an annual rate.
func grow(bal int64, annual float64, days int) int64 {
	if days <= 0 || annual == 0 || bal == 0 {
		return bal
	}
	return int64(math.Round(float64(bal) * math.Pow(1+annual, float64(days)/365)))
}

// fromBase converts base minor units into the benchmark currency.
func fromBase(mk *market, minor int64, cur money.Currency, day string) (money.Amount, error) {
	if cur == mk.base {
		return money.FromMinor(minor, cur)
	}
	rate, _, ok := mk.rateAt(cur, day)
	if !ok {
		return money.Amount{}, fmt.Errorf("no %s rate on or before %s", cur, day)
	}
	a, err := money.FromMinor(minor, mk.base)
	if err != nil {
		return money.Amount{}, err
	}
	exp, _ := cur.Exponent()
	d, err := a.Decimal().Div(rate, uint8(exp), money.HalfEven)
	if err != nil {
		return money.Amount{}, err
	}
	return money.NewAmount(d, cur, money.HalfEven)
}

// fundTWR chains the benchmark's own price returns with its distributions.
func fundTWR(bm Benchmark, mk *market, cur money.Currency, startDay, to int) Metric {
	v := mk.marks[bm.Security]
	p0, _, ok := mk.markAt(bm.Security, dayStr(startDay))
	if !ok {
		return unavailable("no benchmark price on or before %s", dayStr(startDay))
	}
	dist := map[string]money.Decimal{}
	for _, d := range bm.Distributions {
		dist[d.ExDay] = d.PerUnit
	}
	index := 1.0
	prev := floatOf(p0.Price)
	for _, k := range v {
		kd := mustDay(k.Day)
		if kd <= startDay || kd > to {
			continue
		}
		if k.Currency != cur {
			return unavailable("a benchmark price is in %s, not %s", k.Currency, cur)
		}
		p := floatOf(k.Price)
		index *= (p + floatOf(dist[k.Day])) / prev
		prev = p
	}
	return Metric{Available: true, Value: index - 1, Notes: []string{"price return plus distributions in the benchmark's currency; no cost applied"}}
}
