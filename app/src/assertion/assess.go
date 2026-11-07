package assertion

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// AssessVersion names the rules that read assertions into assessments. An
// assessment is a reading, made at a time under a version; the assertions it
// rests on are observations and never change with it.
const AssessVersion = "assess-1"

// The dimensions of an assessment. They answer different questions and are never
// folded into one number: how much the event matters to the business, whether it
// is news, how soon it needs a look, and what it will do to the price.
const (
	DimMateriality = "materiality"
	DimNovelty     = "novelty"
	DimUrgency     = "urgency"
	DimPriceImpact = "price_impact"
)

// Dimensions in the order they are reported.
var Dimensions = []string{DimMateriality, DimNovelty, DimUrgency, DimPriceImpact}

// Assessment is one reading of one dimension for one company in one article.
type Assessment struct {
	ArticleID string
	Ticker    string
	Dimension string
	// Value is the reading on the dimension's scale (materiality 1 to 5; novelty
	// 2 new, 1 update, 0 repeat; urgency 1 low to 3 high) and Label its name.
	// Both are zero when Abstained.
	Value int
	Label string
	// Abstained says the evidence does not support a reading; Basis then says
	// why.
	Abstained bool
	Basis     string
	// Seqs are the assertions the reading rests on.
	Seqs    []int
	Version string
}

// Context is what an assessment needs beyond the article's own claims.
type Context struct {
	// Earlier is how many earlier pieces of evidence the same event already has.
	Earlier int
	// Now is when the reading is made.
	Now time.Time
	// Published is when the article came out.
	Published time.Time
}

// importance breaks ties between claims of the same materiality: the claim that
// threatens or reshapes the company comes before one that routinely reports on
// it. It is a judgement, not a measurement.
var importance = []model.EventKind{
	model.EventBankruptcy, model.EventNonReliance, model.EventDelisting, model.EventControlChange, model.EventAcquisition,
	model.EventSpinoff, model.EventCyber, model.EventImpairment, model.EventRestructuring, model.EventAuditorChange,
	model.EventGuidance, model.EventResults, model.EventIssuance, model.EventDebt, model.EventAgreement, model.EventBuyback,
	model.EventDividend, model.EventLitigation, model.EventCompanyRegulatory, model.EventOfficerChange, model.Event13DG,
	model.EventInsiderBuy, model.EventInsiderSell,
}

func rank(k model.EventKind) int {
	if i := slices.Index(importance, k); i >= 0 {
		return i
	}
	return len(importance)
}

// Materiality is the business materiality of one live claim, 1 to 5, and the
// reasons. A filing item rates the kind of filing it is (Rated) and says nothing
// of the amount; text starts at the kind's base, moves up with a magnitude
// phrase and down with a routine one, and down one level while the event is only
// proposed.
func (a Assertion) Materiality() (int, string) {
	var parts []string
	m := a.Kind.BaseMateriality()
	if a.Rated > 0 {
		m = a.Rated
		parts = append(parts, fmt.Sprintf("%s: the filing's item rates %d; the amount involved is not stated", a.Match, m))
	} else {
		parts = append(parts, fmt.Sprintf("kind %s starts at %d", a.Kind, m))
	}
	if a.Magnitude != "" {
		m++
		parts = append(parts, fmt.Sprintf("magnitude phrase %q +1", a.Magnitude))
	}
	if a.Routine != "" {
		m--
		parts = append(parts, fmt.Sprintf("routine phrase %q -1", a.Routine))
	}
	if a.Status == Proposed {
		m--
		parts = append(parts, "only proposed -1")
	}
	return min(max(m, 1), 5), strings.Join(parts, ", ")
}

// Why says in words why a claim is not a live event.
func (a Assertion) Why() string {
	var w []string
	if a.Negated {
		w = append(w, "negated")
	}
	if a.Denied {
		w = append(w, "denied")
	}
	if a.Historical {
		w = append(w, "about the past")
	}
	if a.Anticipatory {
		w = append(w, "looks ahead to the event")
	}
	return strings.Join(w, ", ")
}

// Assess reads the claims of an article into assessments, one per dimension for
// each company a claim is about as subject or counterparty. A company that is
// only mentioned gets none. When the claims about a company are all negated,
// denied, in the past or previews, its readings abstain and say so.
func Assess(r Result, ctx Context) []Assessment {
	var tickers []string
	for _, a := range r.Assertions {
		for _, p := range a.Parties {
			if p.Role != Mentioned && !slices.Contains(tickers, p.Ticker) {
				tickers = append(tickers, p.Ticker)
			}
		}
	}
	var out []Assessment
	for _, t := range tickers {
		out = append(out, assessOne(r, t, ctx)...)
	}
	return out
}

func assessOne(r Result, ticker string, ctx Context) []Assessment {
	var live, dead []Assertion
	for _, a := range r.Assertions {
		if p, ok := a.Party(ticker); !ok || p.Role == Mentioned {
			continue
		}
		if a.Live() {
			live = append(live, a)
		} else {
			dead = append(dead, a)
		}
	}
	base := Assessment{ArticleID: r.ArticleID, Ticker: ticker, Version: AssessVersion}
	mk := func(dim string) Assessment { b := base; b.Dimension = dim; return b }
	abstain := func(dim, why string, as []Assertion) Assessment {
		a := mk(dim)
		a.Abstained, a.Basis = true, why
		for _, x := range as {
			a.Seqs = append(a.Seqs, x.Seq)
		}
		return a
	}
	pi := abstain(DimPriceImpact, "no calibrated model of price impact exists; materiality says how much the event matters to the business, not what the price will do", nil)
	if len(live) == 0 {
		why := "no claim about the company in this article"
		if len(dead) > 0 {
			var parts []string
			for _, a := range dead {
				parts = append(parts, fmt.Sprintf("%s (%s)", a.Kind, a.Why()))
			}
			why = "the claims about the company are not current events: " + strings.Join(parts, "; ")
		}
		return []Assessment{abstain(DimMateriality, why, dead), abstain(DimNovelty, why, dead), abstain(DimUrgency, why, dead), pi}
	}
	// The claim that matters most decides; the others stay as assertions.
	slices.SortStableFunc(live, func(a, b Assertion) int {
		ma, _ := a.Materiality()
		mb, _ := b.Materiality()
		if ma != mb {
			return mb - ma
		}
		return rank(a.Kind) - rank(b.Kind)
	})
	top := live[0]
	m, why := top.Materiality()
	mat := mk(DimMateriality)
	mat.Value, mat.Label, mat.Basis, mat.Seqs = m, string(top.Kind), why, []int{top.Seq}
	if len(live) > 1 {
		var others []string
		for _, a := range live[1:] {
			mm, _ := a.Materiality()
			others = append(others, fmt.Sprintf("%s %d", a.Kind, mm))
			mat.Seqs = append(mat.Seqs, a.Seq)
		}
		mat.Basis += "; other current claims: " + strings.Join(others, ", ")
	}

	nov := mk(DimNovelty)
	switch {
	case ctx.Earlier > 0:
		nov.Value, nov.Label, nov.Basis = 0, "repeat", fmt.Sprintf("%d earlier pieces of evidence of the same event", ctx.Earlier)
	default:
		nov.Value, nov.Label, nov.Basis = 2, "new", "first evidence of the event"
	}
	nov.Seqs = []int{top.Seq}

	urg := mk(DimUrgency)
	urg.Seqs = []int{top.Seq}
	age := time.Duration(0)
	if !ctx.Now.IsZero() && !ctx.Published.IsZero() {
		age = ctx.Now.Sub(ctx.Published)
	}
	switch {
	case ctx.Now.IsZero() || ctx.Published.IsZero():
		urg.Abstained, urg.Basis, urg.Seqs = true, "the time of publication or of the reading is not known", nil
	case top.Status == Proposed || top.Attribution == Reported:
		urg.Value, urg.Label, urg.Basis = 2, "medium", "proposed or reported, not confirmed: it needs a look but not an alert"
	case age <= 24*time.Hour && top.Kind.Group() != model.GroupNoise && m >= 4:
		urg.Value, urg.Label, urg.Basis = 3, "high", fmt.Sprintf("a current event of materiality %d published %s ago", m, age.Round(time.Minute))
	case age <= 72*time.Hour && m >= 3:
		urg.Value, urg.Label, urg.Basis = 2, "medium", fmt.Sprintf("a current event of materiality %d published %s ago", m, age.Round(time.Hour))
	default:
		urg.Value, urg.Label, urg.Basis = 1, "low", "older or of low materiality"
	}
	return []Assessment{mat, nov, urg, pi}
}

// Best is the claim of the result that matters most for any company, and its
// materiality; ok is false when no claim is a live event. It is what names the
// event an article is evidence of.
func (r Result) Best() (a Assertion, materiality int, ok bool) {
	for _, x := range r.Assertions {
		if !x.Live() || !hasActor(x) {
			continue
		}
		m, _ := x.Materiality()
		if !ok || m > materiality || (m == materiality && rank(x.Kind) < rank(a.Kind)) {
			a, materiality, ok = x, m, true
		}
	}
	return a, materiality, ok
}

func hasActor(a Assertion) bool {
	return slices.ContainsFunc(a.Parties, func(p Party) bool { return p.Role != Mentioned })
}
