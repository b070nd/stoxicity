package eventstudy

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
	"strconv"
	"time"
)

// loaded is a series as the run holds it: checked once, with the reason it
// cannot be used when it cannot.
type loaded struct {
	bars  []Bar
	state string
	why   string
}

type run struct {
	spec   Spec
	ses    sessions
	delay  time.Duration
	prices Prices
	series map[string]*loaded
}

func (r *run) load(security string) *loaded {
	if l, ok := r.series[security]; ok {
		return l
	}
	l := &loaded{state: Measured}
	s, ok := r.prices.Series(security)
	switch {
	case !ok || len(s.Bars) == 0:
		l.state, l.why = NoPrices, "no daily bars for "+security
	default:
		if err := s.Validate(); err != nil {
			l.state, l.why = BadPrices, err.Error()
		} else {
			l.bars = s.Bars
		}
	}
	r.series[security] = l
	return l
}

// Run measures every event of the study's types and summarises them by type,
// holding period, window and condition. The observations may be given in any
// order; an ID given twice is an error, because one event must never count as
// two.
func Run(spec Spec, man Manifest, observations []Observation, prices Prices) (Report, error) {
	if err := spec.Validate(); err != nil {
		return Report{}, err
	}
	if prices == nil {
		return Report{}, fmt.Errorf("eventstudy: study %s was given no prices", spec.ID)
	}
	ses, err := newSessions(spec.Session)
	if err != nil {
		return Report{}, err
	}
	studied := map[string]bool{}
	for _, t := range spec.Types {
		studied[t] = true
	}
	var obs []Observation
	seen := map[string]bool{}
	for _, o := range observations {
		if !studied[o.Type] {
			continue
		}
		if o.ID == "" || seen[o.ID] {
			return Report{}, fmt.Errorf("eventstudy: observation %q is missing its ID or is given twice", o.ID)
		}
		seen[o.ID] = true
		obs = append(obs, o)
	}
	sort.SliceStable(obs, func(i, j int) bool {
		if !obs[i].AvailableAt.Equal(obs[j].AvailableAt) {
			return obs[i].AvailableAt.Before(obs[j].AvailableAt)
		}
		return obs[i].ID < obs[j].ID
	})

	r := &run{spec: spec, ses: ses, delay: time.Duration(spec.EntryDelayMinutes) * time.Minute, prices: prices, series: map[string]*loaded{}}
	rep := Report{Version: Version, Spec: spec, SpecHash: spec.Hash(), Manifest: man, Counts: map[string]int{}}
	recon := map[string]rebuilt{}
	for _, o := range obs {
		out := r.measure(o)
		rep.Counts[out.State]++
		rep.Outcomes = append(rep.Outcomes, out)
		recon[o.ID] = rebuilt{is: o.Reconstructed, attested: o.Reconstructed && o.Attested}
	}
	rep.DataHash = r.dataHash(obs)
	rep.Limits = limits(spec, man)
	rep.Groups, rep.Tested = r.groups(rep, recon)
	rep.Status = Insufficient
	for _, g := range rep.Groups {
		switch {
		case g.Status == Evaluated:
			rep.Status = Evaluated
		case g.Status == Experimental && rep.Status == Insufficient:
			rep.Status = Experimental
		}
	}
	return rep, nil
}

// limits are the facts about the study and its data that keep every group
// from "evaluated".
// rebuilt says how an event came to be held.
type rebuilt struct{ is, attested bool }

func limits(spec Spec, man Manifest) []string {
	var out []string
	if man.Synthetic {
		out = append(out, "the data is synthetic")
	}
	if !man.PointInTimeUniverse {
		out = append(out, "the events were not drawn from the securities that existed at the time (survivorship)")
	}
	if !man.IncludesDelisted {
		out = append(out, "the prices do not cover securities that have stopped trading (survivorship)")
	}
	if spec.Benchmark == "" {
		out = append(out, "no market benchmark: returns are not set against the market")
	}
	if !spec.Validation.set() {
		out = append(out, "no validation window: nothing was confirmed on data apart from development")
	}
	return out
}

// unknownContext is a context nothing could be measured for. Liquidity and
// relative momentum are named unknown only when the study measures them.
func unknownContext(spec ContextSpec) Context {
	ctx := Context{Volatility: Unknown, Momentum: Unknown, Size: Unknown, MarketTrend: Unknown, MarketVolatility: Unknown}
	if spec.LiquiditySessions > 0 {
		ctx.Liquidity = Unknown
	}
	if len(spec.RelativeEdges) > 0 {
		ctx.RelativeMomentum = Unknown
	}
	return ctx
}

func (r *run) measure(o Observation) Outcome {
	out := Outcome{Observation: o.ID, Security: o.Security, Type: o.Type, Context: unknownContext(r.spec.Context)}
	s := r.load(o.Security)
	if s.state != Measured {
		out.State, out.Why = s.state, s.why
		return out
	}
	e, ok := entryIndex(s.bars, o, r.spec.Entry, r.delay, r.ses)
	if !ok {
		out.State, out.Why = NoEntry, "the bars end before a session after the information"
		return out
	}
	entry := s.bars[e]
	if gap, ok := daysBetween(availableDay(o, r.ses), entry.Day); !ok || gap > r.spec.MaxEntryGapDays {
		out.State, out.Why = LateEntry, fmt.Sprintf("the first session in the data, %s, is more than %d days after the information", entry.Day, r.spec.MaxEntryGapDays)
		return out
	}
	out.EntryDay, out.EntryPrice = entry.Day, entry.Open
	first := e // the first bar whose range the position lived through
	if r.spec.Entry == NextClose {
		out.EntryPrice, first = entry.Close, e+1
	}
	switch {
	case r.spec.Development.has(entry.Day):
		out.Period = Development
	case r.spec.Validation.has(entry.Day):
		out.Period = Validation
	default:
		out.Period = Outside
	}

	var market []Bar
	marketEntry := -1
	if r.spec.Benchmark != "" {
		if m := r.load(r.spec.Benchmark); m.state == Measured {
			market = m.bars
			if i, ok := indexOfDay(market, entry.Day); ok {
				marketEntry = i
			}
		}
	}
	out.Context = contextAt(r.spec.Context, s.bars, e, market, marketEntry, o.MarketCap)
	for _, c := range r.spec.Where {
		holds, known := c.Holds(o, out.Context)
		if !known {
			out.State, out.Why = Filtered, "cannot be told: "+c.String()
			return out
		}
		if !holds {
			out.State, out.Why = Filtered, "does not hold: "+c.String()
			return out
		}
	}

	var sector []Bar
	if id := r.spec.SectorBenchmarks[o.Sector]; id != "" {
		if m := r.load(id); m.state == Measured {
			sector = m.bars
		}
	}
	out.State = Measured
	cost := r.spec.Costs.perSide()
	for _, h := range r.spec.Horizons {
		held := Held{Sessions: h}
		x := first + h - 1 // the bar the position is closed on, at its close
		switch {
		case x >= len(s.bars):
			held.State = Censored
		case gapped(s.bars, e, x):
			held.State = Gap
		default:
			exit := s.bars[x]
			held.State, held.ExitDay = Measured, exit.Day
			held.Gross = exit.Close/out.EntryPrice - 1
			held.Net = exit.Close*(1-cost)/(out.EntryPrice*(1+cost)) - 1
			low := out.EntryPrice
			for i := first; i <= x; i++ {
				if s.bars[i].Low < low {
					low = s.bars[i].Low
				}
			}
			held.Drawdown = low/out.EntryPrice - 1
			if m, ok := benchmark(market, entry.Day, exit.Day, r.spec.Entry); ok {
				vs := held.Net - m
				held.Market, held.VsMarket = &m, &vs
			}
			if m, ok := benchmark(sector, entry.Day, exit.Day, r.spec.Entry); ok {
				vs := held.Net - m
				held.Sector, held.VsSector = &m, &vs
			}
			if out.Period == Development && r.spec.Validation.set() && exit.Day >= r.spec.Validation.From {
				held.State = Leaks
			}
		}
		out.Held = append(out.Held, held)
	}
	return out
}

// gapped reports whether sessions are missing between the bars from to to.
func gapped(bars []Bar, from, to int) bool {
	for i := from + 1; i <= to; i++ {
		if missingWeekdays(bars[i-1].Day, bars[i].Day) > maxMissingWeekdays {
			return true
		}
	}
	return false
}

// benchmark is the return of a benchmark from the entry day to the close of
// the exit day, entered by the same rule; false when it lacks either day.
func benchmark(bars []Bar, entryDay, exitDay string, rule Entry) (float64, bool) {
	e, ok1 := indexOfDay(bars, entryDay)
	x, ok2 := indexOfDay(bars, exitDay)
	if !ok1 || !ok2 {
		return 0, false
	}
	in := bars[e].Open
	if rule == NextClose {
		in = bars[e].Close
	}
	return bars[x].Close/in - 1, true
}

type cellKey struct {
	typ       string
	sessions  int
	period    string
	dimension string
	bucket    string
}

type cell struct {
	net, vsMarket, vsSector      []float64
	cNet, cVsMarket, cVsSector   []string
	drawdownSum, drawdownDeepest float64
	reconstructed, attested      int
	withDrawdownOf               int
}

func periodRank(p string) int {
	switch p {
	case Development:
		return 0
	case Validation:
		return 1
	}
	return 2
}

func dimensionRank(d string) int {
	for i, name := range AllDimensions() {
		if name == d {
			return i + 1
		}
	}
	return 0
}

// groups summarises the measured holding periods and corrects the validation
// groups for how many of them were tested.
func (r *run) groups(rep Report, recon map[string]rebuilt) ([]Group, int) {
	cells := map[cellKey]*cell{}
	spec := r.spec
	for _, o := range rep.Outcomes {
		if o.State != Measured {
			continue
		}
		for _, h := range o.Held {
			if h.State != Measured {
				continue
			}
			keys := []cellKey{{o.Type, h.Sessions, o.Period, "all", "all"}}
			for _, d := range spec.Context.Dimensions() {
				keys = append(keys, cellKey{o.Type, h.Sessions, o.Period, d, o.Context.Bucket(d)})
			}
			b := block(o.EntryDay, h.Sessions)
			for _, k := range keys {
				c := cells[k]
				if c == nil {
					c = &cell{}
					cells[k] = c
				}
				c.net, c.cNet = append(c.net, h.Net), append(c.cNet, b)
				if h.VsMarket != nil {
					c.vsMarket, c.cVsMarket = append(c.vsMarket, *h.VsMarket), append(c.cVsMarket, b)
				}
				if h.VsSector != nil {
					c.vsSector, c.cVsSector = append(c.vsSector, *h.VsSector), append(c.cVsSector, b)
				}
				c.drawdownSum += h.Drawdown
				c.withDrawdownOf++
				if h.Drawdown < c.drawdownDeepest {
					c.drawdownDeepest = h.Drawdown
				}
				switch how := recon[o.Observation]; {
				case how.attested:
					c.attested++
				case how.is:
					c.reconstructed++
				}
			}
		}
	}
	keys := make([]cellKey, 0, len(cells))
	for k := range cells {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		switch {
		case a.typ != b.typ:
			return a.typ < b.typ
		case a.sessions != b.sessions:
			return a.sessions < b.sessions
		case a.period != b.period:
			return periodRank(a.period) < periodRank(b.period)
		case a.dimension != b.dimension:
			return dimensionRank(a.dimension) < dimensionRank(b.dimension)
		}
		return a.bucket < b.bucket
	})

	out := make([]Group, 0, len(keys))
	var tested []int
	for _, k := range keys {
		c := cells[k]
		seed := func(measure string) uint64 {
			return seedOf(rep.SpecHash, rep.DataHash, k.typ, strconv.Itoa(k.sessions), k.period, k.dimension, k.bucket, measure)
		}
		g := Group{Type: k.typ, Sessions: k.sessions, Period: k.period, Dimension: k.dimension, Bucket: k.bucket,
			Net:           summarize(c.net, c.cNet, k.sessions, spec.Alpha, spec.Resamples, seed("net")),
			MeanDrawdown:  c.drawdownSum / float64(c.withDrawdownOf),
			MaxDrawdown:   c.drawdownDeepest,
			Reconstructed: c.reconstructed,
			Attested:      c.attested,
		}
		g.Net.PAdjusted = 1
		if len(c.vsMarket) > 0 {
			s := summarize(c.vsMarket, c.cVsMarket, k.sessions, spec.Alpha, spec.Resamples, seed("vs_market"))
			s.PAdjusted = 1
			g.VsMarket = &s
		}
		if len(c.vsSector) > 0 {
			s := summarize(c.vsSector, c.cVsSector, k.sessions, spec.Alpha, spec.Resamples, seed("vs_sector"))
			s.PAdjusted = 1
			g.VsSector = &s
		}
		if p := g.Primary(); k.period == Validation && p.N >= spec.MinGroup && p.Clusters >= spec.MinClusters {
			tested = append(tested, len(out))
		}
		out = append(out, g)
	}

	ps := make([]float64, len(tested))
	for i, at := range tested {
		ps[i] = out[at].Primary().P
	}
	for i, p := range holm(ps) {
		g := &out[tested[i]]
		if g.VsMarket != nil {
			g.VsMarket.PAdjusted = p
		} else {
			g.Net.PAdjusted = p
		}
	}
	for i := range out {
		out[i].Status, out[i].Why = verdict(out[i], spec, rep.Limits, len(tested))
	}
	return out, len(tested)
}

// verdict says how much one group shows.
func verdict(g Group, spec Spec, limits []string, tested int) (status, why string) {
	p := g.Primary()
	switch {
	case p.N < spec.MinGroup:
		return Insufficient, fmt.Sprintf("%d events, fewer than the %d a group needs", p.N, spec.MinGroup)
	case p.Clusters < spec.MinClusters:
		return Insufficient, fmt.Sprintf("%d independent blocks of entry days, fewer than the %d a group needs", p.Clusters, spec.MinClusters)
	case g.Period != Validation:
		return Experimental, "only the validation window can confirm a pattern"
	case p.PAdjusted >= spec.Alpha || (p.Lower <= 0 && p.Upper >= 0):
		return Experimental, fmt.Sprintf("not distinguishable from zero after correcting for the %d groups tested", tested)
	case len(limits) > 0:
		return Experimental, limits[0]
	case g.Reconstructed > 0:
		return Experimental, fmt.Sprintf("%d of the events were rebuilt from public history without a recorded acceptance time", g.Reconstructed)
	}
	return Evaluated, ""
}

// seedOf derives the seed of one resampling from what it resamples, so that a
// report can be reproduced and no seed is chosen by hand.
func seedOf(parts ...string) uint64 {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return binary.BigEndian.Uint64(h.Sum(nil)[:8])
}

func writeFloat(h hash.Hash, v float64) {
	h.Write([]byte(strconv.FormatFloat(v, 'g', -1, 64)))
	h.Write([]byte{','})
}

// dataHash is a digest of the events of the study and of every series it read.
func (r *run) dataHash(obs []Observation) string {
	h := sha256.New()
	for _, o := range obs {
		fmt.Fprintf(h, "%s|%s|%s|%s|%t|%t|%t|", o.ID, o.Security, o.Type, o.AvailableAt.UTC().Format(time.RFC3339Nano), o.DayPrecision, o.Reconstructed, o.Attested)
		writeFloat(h, o.MarketCap)
		h.Write([]byte(o.Sector))
		for _, k := range sortedKeys(o.Numbers) {
			h.Write([]byte(k + "="))
			writeFloat(h, o.Numbers[k])
		}
		for _, k := range sortedKeys(o.Labels) {
			h.Write([]byte(k + "=" + o.Labels[k] + ","))
		}
		h.Write([]byte{'\n'})
	}
	for _, id := range sortedKeys(r.series) {
		h.Write([]byte(id + ":" + r.series[id].state + "\n"))
		for _, b := range r.series[id].bars {
			h.Write([]byte(b.Day + ","))
			for _, v := range []float64{b.Open, b.High, b.Low, b.Close, float64(b.Volume)} {
				writeFloat(h, v)
			}
			h.Write([]byte{'\n'})
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
