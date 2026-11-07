package assertion

import (
	"fmt"
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Candidate is a live claim that could name the event of an article.
type Candidate struct {
	Seq         int
	Kind        model.EventKind
	Materiality int
	// Importance is the place of the kind in the importance order, the most
	// important first; it matters only between claims of equal materiality.
	Importance int
	// Reasons are the steps of the materiality.
	Reasons string
	// Lost says why the claim did not name the event; empty for the one that did.
	Lost string
}

// Excluded is a claim that is not a current event, with why.
type Excluded struct {
	Seq  int
	Kind model.EventKind
	Why  string
}

// Explanation is how the claims of an article decide what event it is evidence of.
type Explanation struct {
	// Chosen is the claim that names the event; zero when Abstained.
	Chosen Candidate
	// Candidates are the live claims about a company, the chosen one first.
	Candidates []Candidate
	// Excluded are the claims that never competed: negated, denied, in the past,
	// previews, or about no company.
	Excluded []Excluded
	// Abstained says no claim is a current event about a company: the article
	// names no event.
	Abstained bool
	// Summary is the explanation in a sentence or two.
	Summary string
}

// Explain says which claim names the event of the article and why: what each
// live claim is worth, why the others lost, and what was left out as not current.
// It decides exactly as Best does and is tested against it.
func (r Result) Explain() Explanation {
	var ex Explanation
	for _, a := range r.Assertions {
		if a.Live() && hasActor(a) {
			m, why := a.Materiality()
			ex.Candidates = append(ex.Candidates, Candidate{Seq: a.Seq, Kind: a.Kind, Materiality: m, Importance: rank(a.Kind), Reasons: why})
			continue
		}
		reason := a.Why()
		if reason == "" {
			reason = "about no company the article is tied to"
		}
		ex.Excluded = append(ex.Excluded, Excluded{Seq: a.Seq, Kind: a.Kind, Why: reason})
	}
	if len(ex.Candidates) == 0 {
		ex.Abstained = true
		if len(ex.Excluded) == 0 {
			ex.Summary = "no claim was found, so no event is named"
		} else {
			var parts []string
			for _, e := range ex.Excluded {
				parts = append(parts, fmt.Sprintf("%s (%s)", e.Kind, e.Why))
			}
			ex.Summary = "no current event: " + strings.Join(parts, "; ")
		}
		return ex
	}
	// The same order Best uses: higher materiality, then the earlier in the
	// importance order, then the earlier claim.
	slices.SortStableFunc(ex.Candidates, func(a, b Candidate) int {
		if a.Materiality != b.Materiality {
			return b.Materiality - a.Materiality
		}
		return a.Importance - b.Importance
	})
	ex.Chosen = ex.Candidates[0]
	for k := 1; k < len(ex.Candidates); k++ {
		c := &ex.Candidates[k]
		switch {
		case c.Kind == ex.Chosen.Kind:
			c.Lost = "another claim of the same kind"
		case c.Materiality < ex.Chosen.Materiality:
			c.Lost = fmt.Sprintf("materiality %d is below %d", c.Materiality, ex.Chosen.Materiality)
		case c.Importance > ex.Chosen.Importance:
			c.Lost = fmt.Sprintf("equal materiality %d, and %s comes before %s in the importance order", c.Materiality, ex.Chosen.Kind, c.Kind)
		default:
			c.Lost = fmt.Sprintf("equal materiality %d and importance; the earlier claim wins", c.Materiality)
		}
	}
	ex.Chosen = ex.Candidates[0]
	var b strings.Builder
	fmt.Fprintf(&b, "claim %d, %s, names the event at materiality %d (%s)", ex.Chosen.Seq, ex.Chosen.Kind, ex.Chosen.Materiality, ex.Chosen.Reasons)
	for _, c := range ex.Candidates[1:] {
		if c.Kind != ex.Chosen.Kind {
			fmt.Fprintf(&b, "; %s lost: %s", c.Kind, c.Lost)
		}
	}
	for _, e := range ex.Excluded {
		fmt.Fprintf(&b, "; %s left out: %s", e.Kind, e.Why)
	}
	ex.Summary = b.String()
	return ex
}
