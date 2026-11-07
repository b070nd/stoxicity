// Package challenge measures how well the rules read claims out of articles, on a
// labelled corpus built to be hard: competing claims, corrections, denials,
// issuer roles and several events in one article.
//
// Each stage is measured on its own, because one number hides which stage fails:
//
//   - extraction: did the rules find the claims (precision and recall by kind);
//   - polarity: of the claims found, is each current, or negated, denied, about the
//     past or a preview;
//   - attribution: who vouches for it (the company, a report, nobody);
//   - roles: how each company stands to it (subject, counterparty, mentioned);
//   - naming: which claim names the event of the article, or none (an abstention).
//
// Relevance scoring is not measured here. It is measured against the reader's
// verdicts by the eval command; a claim read correctly says nothing about whether
// the article is worth reading.
//
// The corpus is small and written by hand; its sentences are invented. Counts are
// reported with their size and are not an estimate of accuracy on real news.
package challenge

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/assertion"
	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
)

//go:embed corpus.json
var files embed.FS

// Claim is what the rules should find in a case.
type Claim struct {
	Kind string `json:"kind"`
	// Live says the claim is a current event. When false, Why names the reason:
	// negated, denied, historical or anticipatory.
	Live bool `json:"live"`
	// Why may list alternatives with "|": a ruled-out deal is negated or denied.
	Why string `json:"why,omitempty"`
	// Attribution is "own", "reported" or "none"; empty means not judged.
	Attribution string `json:"attribution,omitempty"`
	// Roles gives the role of each named company (subject, counterparty,
	// mentioned); a company not listed is not judged.
	Roles map[string]string `json:"roles,omitempty"`
	// Action is the action the sentence names (suspended, raised, ...); empty
	// means not judged.
	Action string `json:"action,omitempty"`
}

// Case is one article of the corpus.
type Case struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Title string `json:"title"`
	// Summary is the article's text after the title.
	Summary string   `json:"summary,omitempty"`
	Tickers []string `json:"tickers"`
	// Claims is every claim the article makes about a company, exhaustively: a
	// claim found that is not listed counts against precision.
	Claims []Claim `json:"claims"`
	// Headline is the kind that should name the event; "" means none should (an
	// abstention is the right answer). Absent means the article has no single right
	// answer and the naming is not judged.
	Headline *string `json:"headline,omitempty"`
	// KnownGap names a limit the rules are known to have on this case. The case
	// must fail; when it passes, the entry is stale and the corpus test says so.
	KnownGap string `json:"known_gap,omitempty"`
}

// Corpus is the labelled cases.
type Corpus struct {
	Version string              `json:"version"`
	Note    string              `json:"note"`
	Names   map[string][]string `json:"names"`
	Cases   []Case              `json:"cases"`
}

// Load reads the embedded corpus.
func Load() (Corpus, error) {
	b, err := files.ReadFile("corpus.json")
	if err != nil {
		return Corpus{}, err
	}
	var c Corpus
	if err := json.Unmarshal(b, &c); err != nil {
		return Corpus{}, fmt.Errorf("challenge: corpus: %w", err)
	}
	seen := map[string]bool{}
	for _, k := range c.Cases {
		if k.ID == "" || seen[k.ID] {
			return Corpus{}, fmt.Errorf("challenge: case %q has no unique id", k.ID)
		}
		seen[k.ID] = true
		for _, cl := range k.Claims {
			if !model.EventKind(cl.Kind).Valid() {
				return Corpus{}, fmt.Errorf("challenge: %s: %q is not an event kind", k.ID, cl.Kind)
			}
		}
		if k.Headline != nil && *k.Headline != "" && !model.EventKind(*k.Headline).Valid() {
			return Corpus{}, fmt.Errorf("challenge: %s: headline %q is not an event kind", k.ID, *k.Headline)
		}
	}
	return c, nil
}

// Count is hits out of total.
type Count struct{ Hit, Total int }

// Rate is the share, false when nothing was judged.
func (c Count) Rate() (float64, bool) {
	if c.Total == 0 {
		return 0, false
	}
	return float64(c.Hit) / float64(c.Total), true
}

func (c Count) String() string {
	if r, ok := c.Rate(); ok {
		return fmt.Sprintf("%d of %d (%.0f%%)", c.Hit, c.Total, 100*r)
	}
	return "0 of 0"
}

// Error is one disagreement between the rules and the labels.
type Error struct {
	Case    string
	Group   string
	Measure string
	Want    string
	Got     string
	// Why is the rules' own explanation, for naming errors.
	Why      string
	KnownGap string
}

// Naming is the outcome of the headline decision, kept apart so abstentions show.
type Naming struct {
	Correct       int // the right kind named the event
	WrongKind     int // an event was named, but the wrong one
	FalseAbstain  int // an event should have been named; none was
	FalseAssert   int // none should have been named; one was
	RightAbstain  int // none should have been named, and none was
	Baseline      Count
	BaselineWrong []string // case ids the first-keyword reading names wrongly
}

// Report is the measured result. There is deliberately no overall score.
type Report struct {
	Corpus string
	Cases  int
	// Extraction counts: Found is expected claims found (recall), Precise is found
	// claims that were expected (precision).
	Recall, Precision Count
	Polarity          Count
	// PolarityDead is the polarity of claims that are not current events, the
	// ones whose mistake gives a misleading label.
	PolarityDead Count
	Attribution  Count
	Roles        Count
	Action       Count
	Naming       Naming
	// ByGroup is every measure per group of cases.
	ByGroup map[string]*GroupReport
	Errors  []Error
}

// GroupReport is the counts of one group of cases.
type GroupReport struct {
	Cases                                                     int
	Recall, Precision, Polarity, Attribution, Roles, Headline Count
}

func (g *GroupReport) add(m string, hit bool) {
	var c *Count
	switch m {
	case "recall":
		c = &g.Recall
	case "precision":
		c = &g.Precision
	case "polarity":
		c = &g.Polarity
	case "attribution":
		c = &g.Attribution
	case "roles":
		c = &g.Roles
	case "naming":
		c = &g.Headline
	}
	c.Total++
	if hit {
		c.Hit++
	}
}

var published = time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)

// flags names every reason the rules gave for a claim not being a current event.
func flags(c assertion.Assertion) []string {
	var out []string
	for _, f := range []struct {
		on   bool
		name string
	}{{c.Negated, "negated"}, {c.Denied, "denied"}, {c.Historical, "historical"}, {c.Anticipatory, "anticipatory"}} {
		if f.on {
			out = append(out, f.name)
		}
	}
	return out
}

func attributionOf(a assertion.Assertion) string {
	switch a.Attribution {
	case assertion.OwnStatement:
		return "own"
	case assertion.Reported:
		return "reported"
	}
	return "none"
}

// Run reads every case with the rules and compares the result with the labels.
func Run(c Corpus) Report { return RunWith(c, assertion.Extract) }

// RunWith reads every case with the extractor and compares the result with the
// labels. Any extractor that is to replace or sit beside the rules is judged on
// this corpus by the Gate before it is enabled.
func RunWith(c Corpus, extract func(assertion.Input) assertion.Result) Report {
	rep := Report{Corpus: c.Version, Cases: len(c.Cases), ByGroup: map[string]*GroupReport{}}
	names := func(t string) []string { return c.Names[t] }
	l := lens.New(config.Lens{}, lens.Options{})
	for _, k := range c.Cases {
		g := rep.ByGroup[k.Group]
		if g == nil {
			g = &GroupReport{}
			rep.ByGroup[k.Group] = g
		}
		g.Cases++
		res := extract(assertion.Input{ArticleID: k.ID, Title: k.Title, Summary: k.Summary, Tickers: k.Tickers, Published: published, Names: names})
		fail := func(measure, want, got, explain string) {
			rep.Errors = append(rep.Errors, Error{Case: k.ID, Group: k.Group, Measure: measure, Want: want, Got: got, Why: explain, KnownGap: k.KnownGap})
		}
		// Extraction. A claim of kind "other" is the absence of a kind; it is not counted.
		found := map[string]assertion.Assertion{}
		for _, a := range res.Assertions {
			if a.Kind == model.EventOther {
				continue
			}
			if _, dup := found[string(a.Kind)]; !dup {
				found[string(a.Kind)] = a
			}
		}
		want := map[string]Claim{}
		for _, cl := range k.Claims {
			want[cl.Kind] = cl
		}
		for kind, cl := range want {
			a, ok := found[kind]
			rep.Recall.Total++
			g.add("recall", ok)
			if !ok {
				fail("recall", kind, "not found", "")
				continue
			}
			rep.Recall.Hit++
			// Polarity.
			live := a.Live()
			rep.Polarity.Total++
			whyOK := cl.Live || cl.Why == "" || slices.ContainsFunc(strings.Split(cl.Why, "|"), func(w string) bool { return slices.Contains(flags(a), w) })
			g.add("polarity", live == cl.Live && whyOK)
			if !cl.Live {
				rep.PolarityDead.Total++
			}
			switch {
			case live != cl.Live || !whyOK:
				fail("polarity", polarity(cl), polarity2(a), "")
			default:
				rep.Polarity.Hit++
				if !cl.Live {
					rep.PolarityDead.Hit++
				}
			}
			if cl.Attribution != "" {
				rep.Attribution.Total++
				got := attributionOf(a)
				g.add("attribution", got == cl.Attribution)
				if got == cl.Attribution {
					rep.Attribution.Hit++
				} else {
					fail("attribution", kind+": "+cl.Attribution, got, "")
				}
			}
			for _, t := range sortedKeys(cl.Roles) {
				rep.Roles.Total++
				got := "none"
				if p, ok := a.Party(t); ok {
					got = string(p.Role)
				}
				// A company that is only mentioned has no part in the claim, so being
				// listed as mentioned and not being listed are the same answer.
				same := got == cl.Roles[t] || (slices.Contains([]string{"none", "mentioned"}, got) && slices.Contains([]string{"none", "mentioned"}, cl.Roles[t]))
				g.add("roles", same)
				if same {
					rep.Roles.Hit++
				} else {
					fail("roles", kind+" "+t+": "+cl.Roles[t], got, "")
				}
			}
			if cl.Action != "" {
				rep.Action.Total++
				if a.Action == cl.Action {
					rep.Action.Hit++
				} else {
					fail("action", kind+": "+cl.Action, orNone(a.Action), "")
				}
			}
		}
		for kind := range found {
			rep.Precision.Total++
			_, ok := want[kind]
			g.add("precision", ok)
			if ok {
				rep.Precision.Hit++
			} else {
				fail("precision", "no "+kind+" claim", kind+" claim: "+found[kind].Span, "")
			}
		}
		// Naming.
		ex := res.Explain()
		best, _, ok := res.Best()
		got := ""
		if ok {
			got = string(best.Kind)
		}
		n := &rep.Naming
		if k.Headline == nil {
			continue
		}
		correct := got == *k.Headline
		g.add("naming", correct)
		switch {
		case correct && got == "":
			n.RightAbstain++
		case correct:
			n.Correct++
		case got == "":
			n.FalseAbstain++
			fail("naming", *k.Headline, "abstained", ex.Summary)
		case *k.Headline == "":
			n.FalseAssert++
			fail("naming", "no event", got, ex.Summary)
		default:
			n.WrongKind++
			fail("naming", *k.Headline, got, ex.Summary)
		}
		// The first-keyword reading the claims replaced, for comparison only. It
		// never abstains: an article of no kind reads as "other".
		base := string(l.Score(model.Article{Title: k.Title, Summary: k.Summary}).Kind)
		if base == string(model.EventOther) {
			base = ""
		}
		n.Baseline.Total++
		if base == *k.Headline {
			n.Baseline.Hit++
		} else {
			n.BaselineWrong = append(n.BaselineWrong, k.ID)
		}
	}
	sort.SliceStable(rep.Errors, func(i, j int) bool {
		if rep.Errors[i].Measure != rep.Errors[j].Measure {
			return rep.Errors[i].Measure < rep.Errors[j].Measure
		}
		return rep.Errors[i].Case < rep.Errors[j].Case
	})
	return rep
}

func polarity(c Claim) string {
	if c.Live {
		return "live"
	}
	if c.Why != "" {
		return "not current: " + c.Why
	}
	return "not current"
}

func polarity2(a assertion.Assertion) string {
	if a.Live() {
		return "live"
	}
	return "not current: " + strings.Join(flags(a), ", ")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Unexpected are the errors on cases not marked as a known gap.
func (r Report) Unexpected() []Error {
	var out []Error
	for _, e := range r.Errors {
		if e.KnownGap == "" {
			out = append(out, e)
		}
	}
	return out
}

// Write prints the report: every measure with its size, the naming outcomes with
// the abstentions, the groups, and every error with its explanation.
func (r Report) Write(w io.Writer) {
	fmt.Fprintf(w, "Claim reading on the challenge corpus %s: %d hand-written cases with invented sentences.\n", r.Corpus, r.Cases)
	fmt.Fprintln(w, "No overall score: each stage is measured on its own, and the counts are the size of the test, not a forecast for real news.")
	fmt.Fprintln(w, "Relevance scoring is not measured here; `eval` measures it against your labels.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  extraction   recall %s   precision %s\n", r.Recall, r.Precision)
	fmt.Fprintf(w, "  polarity     %s of the claims found read as current or not; of the claims that are not current events, %s\n", r.Polarity, r.PolarityDead)
	fmt.Fprintf(w, "  attribution  %s\n", r.Attribution)
	fmt.Fprintf(w, "  roles        %s\n", r.Roles)
	fmt.Fprintf(w, "  action       %s\n", r.Action)
	n := r.Naming
	fmt.Fprintf(w, "  naming       %d right kind · %d wrong kind · %d right abstentions · %d wrongly abstained · %d wrongly named an event where none is current\n",
		n.Correct, n.WrongKind, n.RightAbstain, n.FalseAbstain, n.FalseAssert)
	fmt.Fprintf(w, "               first-keyword reading, which claims replaced and which never abstains: %s\n", n.Baseline)
	fmt.Fprintln(w)
	groups := make([]string, 0, len(r.ByGroup))
	for g := range r.ByGroup {
		groups = append(groups, g)
	}
	slices.Sort(groups)
	fmt.Fprintf(w, "  %-22s %5s  %-9s %-9s %-9s %-9s %-9s %s\n", "group", "cases", "recall", "precision", "polarity", "attrib.", "roles", "naming")
	for _, g := range groups {
		x := r.ByGroup[g]
		fmt.Fprintf(w, "  %-22s %5d  %-9s %-9s %-9s %-9s %-9s %s\n", g, x.Cases, frac(x.Recall), frac(x.Precision), frac(x.Polarity), frac(x.Attribution), frac(x.Roles), frac(x.Headline))
	}
	fmt.Fprintln(w)
	if len(r.Errors) == 0 {
		fmt.Fprintln(w, "  no errors")
		return
	}
	fmt.Fprintf(w, "  %d errors\n", len(r.Errors))
	for _, e := range r.Errors {
		gap := ""
		if e.KnownGap != "" {
			gap = "  [known limit: " + e.KnownGap + "]"
		}
		fmt.Fprintf(w, "  %-12s %-34s want %s; got %s%s\n", e.Measure, e.Case, e.Want, e.Got, gap)
		if e.Why != "" {
			fmt.Fprintf(w, "  %-12s   rules said: %s\n", "", e.Why)
		}
	}
}

func frac(c Count) string {
	return fmt.Sprintf("%d/%d", c.Hit, c.Total)
}
