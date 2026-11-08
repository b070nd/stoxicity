package strategy

import (
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/scorecard"
)

// Action is what the strategy proposes for an instrument.
type Action string

const (
	Buy     Action = "BUY"
	Add     Action = "ADD"
	Watch   Action = "WATCH"
	Wait    Action = "WAIT"
	Abstain Action = "ABSTAIN"
)

// Result of one test.
type Result string

const (
	Pass          Result = "pass"
	Fail          Result = "fail"
	Unknown       Result = "unknown"
	NotApplicable Result = "not_applicable"
)

// Test is one line of the checklist with the points it can earn.
type Test struct {
	ID       string `json:"id"`
	Group    string `json:"group"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Max      int    `json:"max"`
	Awarded  int    `json:"awarded"`
	Result   Result `json:"result"`
	Detail   string `json:"detail"`
}

// Price is a quotation used for the entry comparison.
type Price struct {
	Value    money.Decimal  `json:"value"`
	Currency money.Currency `json:"currency"`
	Day      string         `json:"day"`
	Source   string         `json:"source"`
}

// Input is everything one proposal is computed from.
type Input struct {
	Security    string
	AsOf        string // YYYY-MM-DD
	Card        scorecard.Card
	Payouts     []distribution.Payout
	Price       *Price
	Assumptions *Assumptions
	Config      Config
	// Held says the investor already owns the security: BUY then reads ADD.
	Held bool
	// IdentityConflict is an unresolved contradiction about who the issuer is.
	IdentityConflict string
	// NewsMentions is recorded and never used: volume of news cannot produce a proposal.
	NewsMentions int
	// CashYield is the yield the investor can get on cash, when known (a fraction).
	CashYield *money.Decimal
}

// Proposal is the strategy's answer. Action, DataState, StrategyState and
// ExecutionEligibility are separate on purpose.
type Proposal struct {
	Security             string        `json:"security"`
	Strategy             string        `json:"strategy"`
	ConfigHash           string        `json:"config_hash"`
	AsOf                 string        `json:"as_of"`
	Action               Action        `json:"action"`
	DataState            string        `json:"data_state"`
	StrategyState        string        `json:"strategy_state"`
	ExecutionEligibility string        `json:"execution_eligibility"`
	Score                int           `json:"score"`
	MaxScore             int           `json:"max_score"`
	Coverage             string        `json:"coverage"`
	Tests                []Test        `json:"tests"`
	Valuation            *Valuation    `json:"valuation,omitempty"`
	Price                *Price        `json:"price,omitempty"`
	Thesis               []string      `json:"thesis,omitempty"`
	Counter              []string      `json:"counter_evidence,omitempty"`
	Reasons              []string      `json:"reasons"`
	Invalidation         []string      `json:"invalidation"`
	Alternatives         []Alternative `json:"alternatives"`
	IncomePreference     bool          `json:"income_preference"`
	NextExDay            string        `json:"next_ex_day,omitempty"`
	ReviewBy             string        `json:"review_by"`
	Ignored              []string      `json:"ignored,omitempty"`
	Inputs               Manifest      `json:"inputs"`
	Hash                 string        `json:"hash"`
}

// Alternative is something the money could do instead. Nothing here claims the
// proposal beats it.
type Alternative struct {
	Name string `json:"name"`
	Note string `json:"note"`
}

// Manifest names the inputs a proposal was computed from.
type Manifest struct {
	Card        string             `json:"scorecard"`
	Versions    scorecard.Versions `json:"versions"`
	Config      string             `json:"config"`
	Assumptions string             `json:"assumptions,omitempty"`
	Price       string             `json:"price,omitempty"`
	Payouts     int                `json:"payouts"`
}

type yearFacts struct {
	end                                 string
	cur                                 money.Currency
	revenue, netIncome, ocf, capex, fcf money.Decimal
}

func (c Config) day(s string) (time.Time, bool) {
	t, err := time.Parse(time.DateOnly, s)
	return t, err == nil
}

// comparableYears returns the newest years that have revenue, net income and the
// cash-flow lines, all in one currency.
func comparableYears(card scorecard.Card) []yearFacts {
	var out []yearFacts
	var cur money.Currency
	for _, y := range card.Years {
		get := func(m string) (money.Decimal, money.Currency, bool) {
			c := y.Raw[m]
			if !c.Has() {
				return money.Decimal{}, "", false
			}
			return c.Value, y.Used[m].Currency, true
		}
		rev, c1, ok1 := get(scorecard.Revenue)
		ni, c2, ok2 := get(scorecard.NetIncome)
		ocf, c3, ok3 := get(scorecard.OperatingCashFlow)
		cx, c4, ok4 := get(scorecard.Capex)
		if !(ok1 && ok2 && ok3 && ok4) || c1 != c2 || c1 != c3 || c1 != c4 {
			continue
		}
		if cur != "" && c1 != cur {
			continue
		}
		cur = c1
		f, err := ocf.Sub(cx)
		if err != nil {
			continue
		}
		out = append(out, yearFacts{end: y.End, cur: c1, revenue: rev, netIncome: ni, ocf: ocf, capex: cx, fcf: f})
	}
	return out
}

func pct(d money.Decimal) string {
	p, err := d.Mul(money.MustDecimal("100"), 1, money.HalfEven)
	if err != nil {
		return d.String()
	}
	return p.String() + "%"
}

// Evaluate runs the core strategy for one security.
func Evaluate(in Input) (Proposal, error) {
	if err := in.Config.Validate(); err != nil {
		return Proposal{}, err
	}
	asOf, ok := in.Config.day(in.AsOf)
	if !ok {
		return Proposal{}, fmt.Errorf("strategy: the as-of day %q must be YYYY-MM-DD", in.AsOf)
	}
	cfg := in.Config
	p := Proposal{Security: in.Security, Strategy: cfg.ID, ConfigHash: cfg.Hash(), AsOf: in.AsOf, ExecutionEligibility: "research_only",
		Inputs: Manifest{Card: in.Card.Hash, Versions: in.Card.Versions, Config: cfg.Hash(), Payouts: len(in.Payouts)}}
	if in.NewsMentions > 0 {
		p.Ignored = append(p.Ignored, fmt.Sprintf("%d news mention(s) were recorded and not used: the volume of news cannot produce a proposal", in.NewsMentions))
	}
	yrs := comparableYears(in.Card)
	add := func(t Test) { p.Tests = append(p.Tests, t) }
	res := func(t Test, r Result, detail string) Test {
		t.Result, t.Detail = r, detail
		if r == Pass {
			t.Awarded = t.Max
		}
		return t
	}

	// ---- business understanding ----
	notes := func(topic string, st scorecard.Stance) (n int) {
		for _, a := range in.Card.Notes {
			if a.Topic == topic && a.Stance == st {
				n++
			}
		}
		return
	}
	thesis := notes("thesis", scorecard.Supports)
	add(res(Test{ID: "bu.thesis", Group: "business understanding", Label: "a sourced thesis note says what the business does and why it earns money", Required: true, Max: 10},
		map[bool]Result{true: Pass, false: Unknown}[thesis > 0], fmt.Sprintf("%d supporting thesis note(s)", thesis)))
	for _, x := range []struct{ id, topic, label string }{{"bu.moat", "moat", "a sourced note on the competitive position"}, {"bu.management", "management", "a sourced note on management and capital allocation"}} {
		n := notes(x.topic, scorecard.Supports)
		add(res(Test{ID: x.id, Group: "business understanding", Label: x.label, Max: 5}, map[bool]Result{true: Pass, false: Unknown}[n > 0], fmt.Sprintf("%d note(s)", n)))
	}

	// ---- financial quality ----
	hist := Test{ID: "fq.history", Group: "financial quality", Label: fmt.Sprintf("at least %d comparable completed fiscal years (revenue, net income, operating cash flow, capex, one currency)", cfg.MinYears), Required: true}
	if len(yrs) >= cfg.MinYears {
		add(res(hist, Pass, fmt.Sprintf("%d years, newest %s", len(yrs), yrs[0].end)))
	} else {
		add(res(hist, Unknown, fmt.Sprintf("%d comparable year(s) in the data", len(yrs))))
	}
	enough := len(yrs) >= cfg.MinYears
	pos := func(id, label string, max int, ok func() (bool, string)) {
		t := Test{ID: id, Group: "financial quality", Label: label, Required: true, Max: max}
		if !enough {
			add(res(t, Unknown, "not enough comparable years"))
			return
		}
		good, d := ok()
		add(res(t, map[bool]Result{true: Pass, false: Fail}[good], d))
	}
	pos("fq.earnings", "latest net income is positive", 5, func() (bool, string) {
		return yrs[0].netIncome.Sign() > 0, "net income " + yrs[0].netIncome.String() + " " + string(yrs[0].cur)
	})
	pos("fq.fcf", "latest free cash flow is positive", 5, func() (bool, string) {
		return yrs[0].fcf.Sign() > 0, "free cash flow " + yrs[0].fcf.String() + " " + string(yrs[0].cur)
	})
	pos("fq.fcf_years", fmt.Sprintf("free cash flow is positive in at least %d of the last three years", cfg.MinFCFPositiveYears), 5, func() (bool, string) {
		n := 0
		for _, y := range yrs[:3] {
			if y.fcf.Sign() > 0 {
				n++
			}
		}
		return n >= cfg.MinFCFPositiveYears, fmt.Sprintf("%d of 3", n)
	})
	ratioTest := func(id, label, ratio string, min money.Decimal, group string, max int, required bool) {
		t := Test{ID: id, Group: group, Label: label, Required: required, Max: max}
		if len(in.Card.Years) == 0 {
			add(res(t, Unknown, "no scorecard year"))
			return
		}
		c := in.Card.Years[0].Ratios[ratio]
		switch {
		case !c.Has():
			add(res(t, Unknown, string(c.Status)+": "+c.Reason))
		case c.Value.Cmp(min) >= 0:
			add(res(t, Pass, ratio+" "+pct(c.Value)))
		default:
			add(res(t, Fail, ratio+" "+pct(c.Value)+" is below "+pct(min)))
		}
	}
	ratioTest("fq.margin", fmt.Sprintf("net margin of at least %s", pct(cfg.MinNetMargin)), scorecard.NetMargin, cfg.MinNetMargin, "financial quality", 5, false)
	ratioTest("fq.roe", fmt.Sprintf("return on equity of at least %s", pct(cfg.MinROE)), scorecard.ROE, cfg.MinROE, "financial quality", 5, false)

	// ---- resilience ----
	debtFree, debtKnown := false, false
	var debt money.Decimal
	if len(in.Card.Years) > 0 {
		if c := in.Card.Years[0].Raw[scorecard.TotalDebt]; c.Has() {
			debtKnown, debt = true, c.Value
			debtFree = c.Value.IsZero()
		}
	}
	lev := Test{ID: "res.leverage", Group: "resilience", Label: fmt.Sprintf("net debt / EBITDA at most %s", cfg.MaxNetDebtToEBITDA), Required: true, Max: 10}
	cov := Test{ID: "res.cover", Group: "resilience", Label: fmt.Sprintf("operating income / interest at least %s", cfg.MinInterestCover), Required: true, Max: 10}
	switch {
	case len(in.Card.Years) == 0:
		add(res(lev, Unknown, "no scorecard year"))
		add(res(cov, Unknown, "no scorecard year"))
	case debtFree:
		add(res(lev, Pass, "debt-free: sourced total debt is zero"))
		add(res(cov, Pass, "debt-free: sourced total debt is zero"))
	default:
		y := in.Card.Years[0]
		eb, cash := y.Raw[scorecard.Ebitda], y.Raw[scorecard.Cash]
		switch {
		case !debtKnown:
			add(res(lev, Unknown, "total debt is missing"))
		case !eb.Has() || !cash.Has():
			add(res(lev, Unknown, "EBITDA or cash is missing"))
		case y.Used[scorecard.TotalDebt].Currency != y.Used[scorecard.Ebitda].Currency || y.Used[scorecard.Cash].Currency != y.Used[scorecard.Ebitda].Currency:
			add(res(lev, Unknown, "debt, cash and EBITDA are not in one currency"))
		case eb.Value.Sign() <= 0:
			add(res(lev, Unknown, "EBITDA is not positive: net debt / EBITDA is not meaningful"))
		default:
			nd, _ := debt.Sub(cash.Value)
			q, err := nd.Div(eb.Value, 4, money.HalfEven)
			switch {
			case err != nil:
				add(res(lev, Unknown, err.Error()))
			case q.Cmp(cfg.MaxNetDebtToEBITDA) <= 0:
				add(res(lev, Pass, "net debt / EBITDA "+q.String()))
			default:
				add(res(lev, Fail, "net debt / EBITDA "+q.String()+" is above "+cfg.MaxNetDebtToEBITDA.String()))
			}
		}
		c := y.Ratios[scorecard.InterestCover]
		switch {
		case !c.Has():
			add(res(cov, Unknown, string(c.Status)+": "+c.Reason))
		case c.Value.Cmp(cfg.MinInterestCover) >= 0:
			add(res(cov, Pass, "cover "+c.Value.String()))
		default:
			add(res(cov, Fail, "cover "+c.Value.String()+" is below "+cfg.MinInterestCover.String()))
		}
	}

	// ---- valuation ----
	var cashFlowShare money.Decimal
	valuation := func() {
		sc := Test{ID: "val.scenario", Group: "valuation", Label: "bear/base/bull values computed from documented assumptions", Max: 10}
		en := Test{ID: "val.entry", Group: "valuation", Label: "the price is at or below the entry ceiling", Max: 10}
		switch {
		case in.Assumptions == nil:
			add(res(sc, Unknown, "no valuation assumptions were supplied: research only, no price is invented"))
			add(res(en, Unknown, "no entry ceiling"))
			return
		case !enough:
			add(res(sc, Unknown, "no valuation without enough comparable years"))
			add(res(en, Unknown, "no entry ceiling"))
			return
		}
		a := *in.Assumptions
		sh := in.Card.Years[0].Raw[scorecard.Shares]
		switch {
		case !sh.Has():
			add(res(sc, Unknown, "the share count for the latest year is missing"))
			add(res(en, Unknown, "no entry ceiling"))
			return
		case yrs[0].cur != a.Currency:
			add(res(sc, Unknown, fmt.Sprintf("the figures are in %s and the assumptions in %s: no rate is applied", yrs[0].cur, a.Currency)))
			add(res(en, Unknown, "no entry ceiling"))
			return
		case yrs[0].fcf.Sign() <= 0:
			add(res(sc, Unknown, "free cash flow is not positive: an owner-earnings valuation is not meaningful"))
			add(res(en, Unknown, "no entry ceiling"))
			return
		}
		cfs, err := yrs[0].fcf.Div(sh.Value, 8, money.HalfEven)
		if err != nil {
			add(res(sc, Unknown, err.Error()))
			add(res(en, Unknown, "no entry ceiling"))
			return
		}
		cashFlowShare = cfs
		v, err := Value(a, cfs, sh.Value, cfg.EntryScenario, cfg.MarginOfSafety)
		if err != nil {
			add(res(sc, Unknown, err.Error()))
			add(res(en, Unknown, "no entry ceiling"))
			return
		}
		p.Valuation = &v
		p.Inputs.Assumptions = a.Hash()
		if !v.Ordered {
			add(res(sc, Fail, v.Note))
		} else {
			add(res(sc, Pass, fmt.Sprintf("bear %s, base %s, bull %s %s per share", v.Bear, v.Base, v.Bull, v.Currency)))
		}
		switch {
		case in.Price == nil:
			add(res(en, Unknown, "no price"))
		case in.Price.Currency != v.Currency:
			add(res(en, Unknown, fmt.Sprintf("the price is in %s and the valuation in %s: no rate is applied", in.Price.Currency, v.Currency)))
		case in.Price.Value.Cmp(v.Ceiling) <= 0:
			add(res(en, Pass, fmt.Sprintf("price %s is at or below the ceiling %s %s", in.Price.Value, v.Ceiling, v.Currency)))
		default:
			add(res(en, Fail, fmt.Sprintf("price %s is above the ceiling %s %s", in.Price.Value, v.Ceiling, v.Currency)))
		}
	}
	valuation()

	// ---- sustainable income ----
	var ordinary []distribution.Payout
	for _, d := range in.Payouts {
		if d.Key.SecurityID == in.Security && d.Key.Type == distribution.Ordinary {
			ordinary = append(ordinary, d)
		}
	}
	paysNothing := len(ordinary) == 0
	if len(in.Card.Years) > 0 {
		if c := in.Card.Years[0].Raw[scorecard.DividendsPaid]; c.Has() && !c.Value.IsZero() {
			paysNothing = false
		}
	}
	decl := Test{ID: "inc.declared", Group: "sustainable income", Label: "a declared ordinary distribution within a year, in a known currency", Max: 5}
	hst := Test{ID: "inc.history", Group: "sustainable income", Label: fmt.Sprintf("at least %d declared ordinary payouts in the last five years, none cancelled or contested in the last year", cfg.MinPayoutHistory), Max: 5}
	pay := Test{ID: "inc.payout", Group: "sustainable income", Label: fmt.Sprintf("dividends paid are at most %s of free cash flow and %s of net income", pct(cfg.MaxPayoutFCF), pct(cfg.MaxPayoutEarnings)), Max: 5}
	if paysNothing {
		for _, t := range []Test{decl, hst, pay} {
			t.Result, t.Detail = NotApplicable, "no ordinary distribution is observed: the business can qualify for value but gets no income preference"
			add(t)
		}
	} else {
		var declaredNear, declaredFiveYears, recentTrouble int
		var cur money.Currency
		for _, d := range ordinary {
			ex, ok := cfg.day(d.ExDay)
			if !ok {
				continue
			}
			age := asOf.Sub(ex).Hours() / 24
			switch d.State {
			case distribution.StateDeclared:
				if age > -400 && age < 400 {
					declaredNear++
					cur = d.Currency
					if d.ExDay >= in.AsOf && (p.NextExDay == "" || d.ExDay < p.NextExDay) {
						p.NextExDay = d.ExDay
					}
				}
				if age >= 0 && age <= 5*366 {
					declaredFiveYears++
				}
			case distribution.StateCancelled, distribution.StateConflicting:
				if age >= 0 && age <= 366 {
					recentTrouble++
				}
			}
		}
		if declaredNear > 0 && cur.Valid() {
			add(res(decl, Pass, fmt.Sprintf("%d declared payout(s) near %s", declaredNear, in.AsOf)))
		} else {
			add(res(decl, Unknown, "no declared ordinary payout within a year of the as-of day"))
		}
		switch {
		case recentTrouble > 0:
			add(res(hst, Fail, fmt.Sprintf("%d ordinary payout(s) cancelled or contested in the last year", recentTrouble)))
		case declaredFiveYears >= cfg.MinPayoutHistory:
			add(res(hst, Pass, fmt.Sprintf("%d declared payouts in five years", declaredFiveYears)))
		default:
			add(res(hst, Unknown, fmt.Sprintf("%d declared payout(s) in five years, %d needed", declaredFiveYears, cfg.MinPayoutHistory)))
		}
		switch {
		case !enough || len(in.Card.Years) == 0:
			add(res(pay, Unknown, "not enough years"))
		case !in.Card.Years[0].Raw[scorecard.DividendsPaid].Has():
			add(res(pay, Unknown, "dividends paid is missing"))
		case in.Card.Years[0].Used[scorecard.DividendsPaid].Currency != yrs[0].cur:
			add(res(pay, Unknown, "dividends paid is in another currency than the cash flow"))
		case yrs[0].fcf.Sign() <= 0 || yrs[0].netIncome.Sign() <= 0:
			add(res(pay, Fail, "free cash flow or net income is not positive: no payout is covered"))
		default:
			dv := in.Card.Years[0].Raw[scorecard.DividendsPaid].Value
			rf, _ := dv.Div(yrs[0].fcf, 4, money.HalfEven)
			re, _ := dv.Div(yrs[0].netIncome, 4, money.HalfEven)
			if rf.Cmp(cfg.MaxPayoutFCF) <= 0 && re.Cmp(cfg.MaxPayoutEarnings) <= 0 {
				add(res(pay, Pass, fmt.Sprintf("%s of free cash flow, %s of net income", pct(rf), pct(re))))
			} else {
				add(res(pay, Fail, fmt.Sprintf("%s of free cash flow, %s of net income", pct(rf), pct(re))))
			}
		}
	}

	// ---- contradictions ----
	critical := map[string]bool{"going_concern": true, "material_weakness": true, "restatement": true}
	contra := Test{ID: "chk.contradiction", Group: "evidence", Label: "no unresolved current financial or identity contradiction", Required: true}
	var open []string
	for i, a := range in.Card.Notes {
		if a.Stance != scorecard.Counter {
			continue
		}
		hit := false
		for _, f := range a.Flags {
			hit = hit || critical[f]
		}
		if !hit {
			continue
		}
		resolved := false
		for _, b := range in.Card.Notes[i+1:] {
			if b.Topic == a.Topic && b.Stance == scorecard.Supports && b.At.After(a.At) {
				resolved = true
			}
		}
		if !resolved {
			open = append(open, fmt.Sprintf("%s (%s): %s", a.At.Format(time.DateOnly), strings.Join(a.Flags, ", "), a.Text))
		}
	}
	switch {
	case in.IdentityConflict != "":
		add(res(contra, Fail, "identity: "+in.IdentityConflict))
	case len(open) > 0:
		add(res(contra, Fail, "unresolved: "+strings.Join(open, "; ")))
	default:
		add(res(contra, Pass, "none recorded"))
	}

	// ---- totals ----
	applicable, known := 0, 0
	for i := range p.Tests {
		t := p.Tests[i]
		if t.Result != NotApplicable {
			applicable++
			if t.Result != Unknown {
				known++
			}
		}
		if t.Max > 0 {
			p.MaxScore += t.Max
		}
		p.Score += t.Awarded
	}
	if applicable > 0 {
		c, _ := money.Int(int64(known)).Div(money.Int(int64(applicable)), 4, money.HalfEven)
		p.Coverage = c.String()
	}
	blocked, failed, optionalUnknown := false, false, false
	for _, t := range p.Tests {
		switch {
		case t.Required && t.Result == Unknown:
			blocked = true
			p.Reasons = append(p.Reasons, "unknown, required: "+t.Label+" ("+t.Detail+")")
		case t.Required && t.Result == Fail:
			failed = true
			p.Reasons = append(p.Reasons, "failed: "+t.Label+" ("+t.Detail+")")
		case t.Result == Unknown:
			optionalUnknown = true
			p.Reasons = append(p.Reasons, "unknown, earns no points: "+t.Label+" ("+t.Detail+")")
		}
	}
	switch {
	case blocked:
		p.DataState = "insufficient"
	case optionalUnknown:
		p.DataState = "partial"
	default:
		p.DataState = "complete"
	}
	switch {
	case failed:
		p.StrategyState = "ineligible"
	case blocked:
		p.StrategyState = "blocked"
	case p.Score < cfg.MinScore:
		p.StrategyState = "ineligible"
		p.Reasons = append(p.Reasons, fmt.Sprintf("score %d of %d is below the minimum %d", p.Score, p.MaxScore, cfg.MinScore))
	default:
		p.StrategyState = "eligible"
	}
	incomePass := 0
	for _, t := range p.Tests {
		if strings.HasPrefix(t.ID, "inc.") && t.Result == Pass {
			incomePass++
		}
	}
	p.IncomePreference = p.StrategyState == "eligible" && incomePass == 3

	// ---- action ----
	p.Action = Abstain
	switch p.StrategyState {
	case "eligible":
		var entry, valid *Test
		for i := range p.Tests {
			switch p.Tests[i].ID {
			case "val.entry":
				entry = &p.Tests[i]
			case "val.scenario":
				valid = &p.Tests[i]
			}
		}
		stale := false
		if in.Price != nil {
			if pd, ok := cfg.day(in.Price.Day); !ok || asOf.Sub(pd).Hours()/24 > float64(cfg.MaxPriceAgeDays) {
				stale = true
			}
			p.Price = in.Price
			p.Inputs.Price = in.Price.Day + " " + in.Price.Source
		}
		switch {
		case valid == nil || valid.Result != Pass:
			p.Action = Watch
			p.Reasons = append(p.Reasons, "eligible, but there is no valuation to set an entry price: research only")
		case in.Price == nil || stale:
			p.Action = Wait
			p.Reasons = append(p.Reasons, "eligible, but there is no current price (none, or older than "+fmt.Sprint(cfg.MaxPriceAgeDays)+" days) to compare with the entry ceiling")
		case entry.Result != Pass:
			p.Action = Watch
			if entry.Result == Fail {
				p.Reasons = append(p.Reasons, "eligible, but outside the entry band: "+entry.Detail)
			} else {
				p.Reasons = append(p.Reasons, "eligible, but the entry cannot be judged: "+entry.Detail)
			}
		case !cfg.Reviewed:
			p.Action = Watch
			p.Reasons = append(p.Reasons, "inside the entry band, but this configuration has not been reviewed: it can propose nothing stronger than WATCH")
		case in.Held:
			p.Action = Add
		default:
			p.Action = Buy
		}
	case "blocked":
		p.Reasons = append(p.Reasons, "abstain: a required input is missing, so no proposal is made")
	default:
		p.Reasons = append(p.Reasons, "abstain: the checklist is not met")
	}
	if in.Held && p.Action == Abstain {
		p.Reasons = append(p.Reasons, "held: hold, reduce and sell are decided by the review rules, not by this proposal")
	}

	for _, a := range in.Card.Notes {
		line := fmt.Sprintf("%s %s: %s (%s, %s)", a.At.Format(time.DateOnly), a.Topic, a.Text, a.Source, a.Ref)
		switch a.Stance {
		case scorecard.Supports:
			p.Thesis = append(p.Thesis, line)
		case scorecard.Counter:
			p.Counter = append(p.Counter, line)
		}
	}
	p.Invalidation = []string{
		fmt.Sprintf("free cash flow is not positive in %d of the next three years, or net income turns negative", cfg.MinFCFPositiveYears),
		fmt.Sprintf("net debt / EBITDA rises above %s or operating income / interest falls below %s", cfg.MaxNetDebtToEBITDA, cfg.MinInterestCover),
		"an ordinary dividend is cut, suspended or paid above the ceilings, if the thesis counted on it",
		"a going-concern, restatement or material-weakness finding that is not resolved by a sourced note",
		"the sourced thesis note is shown wrong by the company's own filings",
	}
	p.Alternatives = alternatives(in, cashFlowShare)
	rb := asOf.AddDate(0, cfg.ReviewMonths, 0)
	p.ReviewBy = rb.Format(time.DateOnly)
	p.Hash = hashJSON(struct {
		P Proposal
	}{p})
	return p, nil
}

func alternatives(in Input, cfs money.Decimal) []Alternative {
	cash := Alternative{Name: "cash", Note: "keeping the money in cash is always an alternative; no cash yield is assumed because none was supplied"}
	if in.CashYield != nil {
		cash.Note = "cash yields " + pct(*in.CashYield) + " as supplied by you; this is not a forecast"
		if in.Price != nil && !cfs.IsZero() && cfs.Sign() > 0 && in.Price.Value.Sign() > 0 {
			if y, err := cfs.Div(in.Price.Value, 4, money.HalfEven); err == nil {
				cash.Note += "; the latest free cash flow per share is " + pct(y) + " of the price (a historical yield, not a return)"
			}
		}
	}
	return []Alternative{cash, {Name: "diversified core fund", Note: "a broad eligible fund assessed by " + FundID + " is the other alternative; its checks are separate and not comparable to this score"}}
}
