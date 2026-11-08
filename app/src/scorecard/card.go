package scorecard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Status of one cell of a scorecard.
type Status string

const (
	OK                  Status = "ok"
	Missing             Status = "missing"
	Unsupported         Status = "unsupported"
	NegativeDenominator Status = "negative_denominator"
	ZeroDenominator     Status = "zero_denominator"
	PeriodMismatch      Status = "period_mismatch"
	CurrencyMismatch    Status = "currency_mismatch"
	NoPriorYear         Status = "no_prior_year"
	Conflicting         Status = "conflicting"
	NotYetFiled         Status = "not_yet_filed"
)

// Cell is one ratio of one year: a value, or the reason there is none.
type Cell struct {
	Status Status        `json:"status"`
	Value  money.Decimal `json:"value"`
	Reason string        `json:"reason,omitempty"`
	// Inputs are the content hashes of the figures the value was computed from.
	Inputs []string `json:"inputs,omitempty"`
}

// Has reports whether the cell holds a value.
func (c Cell) Has() bool { return c.Status == OK }

// Year is one fiscal year of the card: its raw figures and its ratios.
type Year struct {
	End    string           `json:"end"`
	Raw    map[string]Cell  `json:"raw"`
	Ratios map[string]Cell  `json:"ratios"`
	Used   map[string]Datum `json:"-"`
}

// Input is everything a scorecard is computed from.
type Input struct {
	Security string
	Template string
	Data     []Datum
	Notes    []Annotation
	// KnownAt limits the figures to those filed by then; zero reads everything,
	// including figures with no filing date.
	KnownAt time.Time
	// Years is how many fiscal years to show; 0 means 5.
	Years int
}

// Card is the scorecard.
type Card struct {
	Security string       `json:"security"`
	Template string       `json:"template"`
	KnownAt  time.Time    `json:"known_at,omitempty"`
	Versions Versions     `json:"versions"`
	Years    []Year       `json:"years"`
	Notes    []Annotation `json:"notes,omitempty"`
	// Gaps lists, in words, the figures missing from the newest year.
	Gaps []string `json:"gaps,omitempty"`
	Hash string   `json:"hash"`
}

// Build computes the scorecard. It never invents a figure: whatever cannot be
// computed is a cell with a status and a reason.
func Build(in Input) (Card, error) {
	tpl, ok := TemplateByName(in.Template)
	if !ok {
		names := make([]string, 0, len(templates))
		for _, t := range templates {
			names = append(names, t.Name)
		}
		return Card{}, fmt.Errorf("scorecard: unknown template %q (%s)", in.Template, strings.Join(names, ", "))
	}
	n := in.Years
	if n <= 0 {
		n = 5
	}
	card := Card{Security: in.Security, Template: tpl.Name, KnownAt: in.KnownAt.UTC(), Versions: CurrentVersions()}
	var mine []Datum
	for _, d := range in.Data {
		if d.Security == in.Security {
			if err := d.Validate(); err != nil {
				return Card{}, err
			}
			mine = append(mine, d)
		}
	}
	// the fiscal years: the ends of annual flows, newest first
	seen := map[string]bool{}
	var ends []string
	for _, d := range mine {
		// a period exists for the reader only once something in it was filed
		if !in.KnownAt.IsZero() {
			f, ok := day(d.Filed)
			if !ok || f.After(in.KnownAt) {
				continue
			}
		}
		if p, _ := d.Period(); p == Annual && !seen[d.End] {
			seen[d.End] = true
			ends = append(ends, d.End)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ends)))
	if len(ends) > n {
		ends = ends[:n]
	}
	sel := selector{data: mine, knownAt: in.KnownAt}
	for i, end := range ends {
		y := Year{End: end, Raw: map[string]Cell{}, Ratios: map[string]Cell{}, Used: map[string]Datum{}}
		for _, m := range Metrics() {
			d, c := sel.pick(m, end)
			y.Raw[m] = c
			if c.Has() {
				y.Used[m] = d
			}
		}
		prev := ""
		for _, e := range ends[i+1:] {
			if gap := daysApart(e, end); gap >= 350 && gap <= 380 {
				prev = e
				break
			}
		}
		for _, r := range Ratios {
			y.Ratios[r] = ratio(tpl, r, y, prev, sel)
		}
		card.Years = append(card.Years, y)
	}
	if len(card.Years) > 0 {
		for _, m := range []string{Revenue, NetIncome, OperatingCashFlow, Equity, TotalDebt} {
			if c := card.Years[0].Raw[m]; !c.Has() {
				card.Gaps = append(card.Gaps, fmt.Sprintf("%s for the year ended %s: %s", m, card.Years[0].End, c.Reason))
			}
		}
	}
	for _, a := range in.Notes {
		if a.Security != in.Security {
			continue
		}
		if err := a.Validate(); err != nil {
			return Card{}, err
		}
		a.Flags = Flags(a.Text)
		card.Notes = append(card.Notes, a)
	}
	sort.SliceStable(card.Notes, func(i, j int) bool { return card.Notes[i].At.Before(card.Notes[j].At) })
	card.Hash = card.hash()
	return card, nil
}

func daysApart(a, b string) int {
	x, ok1 := day(a)
	y, ok2 := day(b)
	if !ok1 || !ok2 {
		return 0
	}
	return int(y.Sub(x).Hours() / 24)
}

// hash is the content hash of what the card shows: the versions, the inputs
// that were used and the notes. Any change to a figure, a note, the keyword
// table, the extractor or the rules changes it.
func (c Card) hash() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s\n", c.Security, c.Template, c.KnownAt.Format(time.RFC3339), c.Versions.Extractor, c.Versions.Rules, c.Versions.Keywords)
	var hs []string
	for _, y := range c.Years {
		for _, d := range y.Used {
			hs = append(hs, d.Hash())
		}
	}
	sort.Strings(hs)
	b.WriteString(strings.Join(hs, ","))
	for _, a := range c.Notes {
		b.WriteString("\n" + a.canonical() + "|" + strings.Join(a.Flags, ","))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// selector chooses, among the revisions of a figure, the one that was known.
type selector struct {
	data    []Datum
	knownAt time.Time
}

// pick finds an annual (or instant) figure for a metric at an end date.
func (s selector) pick(metric, end string) (Datum, Cell) {
	info := metrics[metric]
	var cands []Datum
	var otherPeriod, undated bool
	for _, d := range s.data {
		if d.Metric != metric || d.End != end {
			continue
		}
		p, _ := d.Period()
		if info.flow && p != Annual {
			otherPeriod = true
			continue
		}
		if !s.knownAt.IsZero() {
			if d.Filed == "" {
				undated = true
				continue
			}
			f, _ := day(d.Filed)
			if f.After(s.knownAt) {
				continue
			}
		}
		cands = append(cands, d)
	}
	if len(cands) == 0 {
		switch {
		case otherPeriod:
			return Datum{}, Cell{Status: PeriodMismatch, Reason: "only a quarterly figure exists for that date, not a full year"}
		case undated:
			return Datum{}, Cell{Status: NotYetFiled, Reason: "the figure has no filing date, so it cannot be shown as of that day"}
		}
		return Datum{}, Cell{Status: Missing, Reason: "no figure was supplied"}
	}
	// newest filing wins; a tie between different values from different sources is a conflict
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Filed > cands[j].Filed })
	best := cands[0]
	for _, o := range cands[1:] {
		if o.Filed == best.Filed && (o.Value.Cmp(best.Value) != 0 || o.Currency != best.Currency) {
			return Datum{}, Cell{Status: Conflicting, Reason: fmt.Sprintf("%s and %s report different values filed on the same day", best.Source, o.Source)}
		}
	}
	return best, Cell{Status: OK, Value: best.Value, Inputs: []string{best.Hash()}}
}

const ratioScale = 4

func ratio(tpl Template, name string, y Year, prevEnd string, sel selector) Cell {
	def := ratioDefs[name]
	if !tpl.supports(name) {
		why := tpl.Why
		if why == "" {
			why = "this ratio is not defined for the template"
		}
		return Cell{Status: Unsupported, Reason: fmt.Sprintf("not computed for a %s: %s", tpl.Name, why)}
	}
	if def.Growth {
		return growth(y, prevEnd, sel)
	}
	num, ncell := operand(def.Num, y)
	if !ncell.Has() {
		return Cell{Status: ncell.Status, Reason: def.Num + ": " + ncell.Reason}
	}
	den, dcell := operand(def.Den, y)
	if !dcell.Has() {
		return Cell{Status: dcell.Status, Reason: def.Den + ": " + dcell.Reason}
	}
	return divide(def, num, den, ncell.Inputs, dcell.Inputs)
}

type amount struct {
	v   money.Decimal
	cur money.Currency
}

// operand reads a metric of the year, or derives free cash flow.
func operand(metric string, y Year) (amount, Cell) {
	if metric == "fcf" {
		ocf, c1 := operandRaw(OperatingCashFlow, y)
		if !c1.Has() {
			return amount{}, Cell{Status: c1.Status, Reason: "operating cash flow: " + c1.Reason}
		}
		cx, c2 := operandRaw(Capex, y)
		if !c2.Has() {
			return amount{}, Cell{Status: c2.Status, Reason: "capex: " + c2.Reason}
		}
		if ocf.cur != cx.cur {
			return amount{}, Cell{Status: CurrencyMismatch, Reason: fmt.Sprintf("operating cash flow is in %s and capex in %s", ocf.cur, cx.cur)}
		}
		v, err := ocf.v.Sub(cx.v)
		if err != nil {
			return amount{}, Cell{Status: Missing, Reason: err.Error()}
		}
		return amount{v, ocf.cur}, Cell{Status: OK, Inputs: append(c1.Inputs, c2.Inputs...)}
	}
	return operandRaw(metric, y)
}

func operandRaw(metric string, y Year) (amount, Cell) {
	c := y.Raw[metric]
	if !c.Has() {
		return amount{}, c
	}
	d := y.Used[metric]
	return amount{c.Value, d.Currency}, c
}

func divide(def ratioDef, num, den amount, in1, in2 []string) Cell {
	if num.cur != den.cur {
		return Cell{Status: CurrencyMismatch, Reason: fmt.Sprintf("%s is in %s and %s in %s: no rate is applied", def.Num, num.cur, def.Den, den.cur)}
	}
	switch {
	case den.v.IsZero():
		return Cell{Status: ZeroDenominator, Reason: def.Den + " is zero"}
	case den.v.Sign() < 0:
		return Cell{Status: NegativeDenominator, Reason: def.Den + " is negative, so the ratio would mislead"}
	}
	q, err := num.v.Div(den.v, ratioScale, money.HalfEven)
	if err != nil {
		return Cell{Status: Missing, Reason: err.Error()}
	}
	return Cell{Status: OK, Value: q, Inputs: append(append([]string(nil), in1...), in2...)}
}

func growth(y Year, prevEnd string, sel selector) Cell {
	cur, c := operandRaw(Revenue, y)
	if !c.Has() {
		return Cell{Status: c.Status, Reason: "revenue: " + c.Reason}
	}
	if prevEnd == "" {
		return Cell{Status: NoPriorYear, Reason: "no fiscal year ending about a year earlier is in the data"}
	}
	d, pc := sel.pick(Revenue, prevEnd)
	if !pc.Has() {
		return Cell{Status: pc.Status, Reason: "prior revenue: " + pc.Reason}
	}
	if d.Currency != cur.cur {
		return Cell{Status: CurrencyMismatch, Reason: fmt.Sprintf("revenue is in %s now and was in %s a year earlier: no rate is applied", cur.cur, d.Currency)}
	}
	switch {
	case d.Value.IsZero():
		return Cell{Status: ZeroDenominator, Reason: "prior revenue is zero"}
	case d.Value.Sign() < 0:
		return Cell{Status: NegativeDenominator, Reason: "prior revenue is negative"}
	}
	q, err := cur.v.Div(d.Value, ratioScale+2, money.HalfEven)
	if err != nil {
		return Cell{Status: Missing, Reason: err.Error()}
	}
	g, err := q.Sub(money.Int(1))
	if err != nil {
		return Cell{Status: Missing, Reason: err.Error()}
	}
	g, _ = g.Rescale(ratioScale, money.HalfEven)
	return Cell{Status: OK, Value: g, Inputs: []string{d.Hash(), cur.hashOf(y)}}
}

func (a amount) hashOf(y Year) string { return y.Used[Revenue].Hash() }
