package pipeline

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/assertion"
	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Present is the score of an article as its claims read it. The lens rates an
// article by the first keyword that matches, which is how a denied bankruptcy, a
// background buyback or the first of three events in a headline came to name the
// whole article. The article is presented as the most important claim that is a
// current event about a company: its kind, its materiality, and a relevance moved
// by the same 0.15 a level the lens uses. An article none of whose claims is a
// current event (they are denied, negated, in the past, previews, or about no
// company) is presented as no event, and says why.
//
// The lens's reading is not lost: its reasons stay, followed by the claims'. The
// version gains the claims' version, so the score is stored beside the lens's own
// and the two can be compared.
//
// Presentation is a label for reading and ranking. Whether evidence is
// corroborated is judged from reporting origins and never from this.
func Present(a model.Article, res assertion.Result) model.Score {
	s := a.Score
	if len(res.Assertions) == 0 || s.LensVersion == "" {
		return s
	}
	s.Reasons = slices.Clone(s.Reasons)
	best, mat, live := res.Best()
	kind := best.Kind
	var note string
	if live {
		note = fmt.Sprintf("asserted event: claim %d of %d, %s (%q)", best.Seq, len(res.Assertions), best.Kind, best.Match)
		if _, w := best.Materiality(); w != "" {
			note += "; " + w
		}
		if best.Status != assertion.Unknown && best.Status != "" {
			note += "; status " + string(best.Status)
		}
		if best.Attribution != assertion.Unattributed {
			note += "; attribution " + string(best.Attribution)
		}
		if best.Action != "" {
			note += "; action " + best.Action
		}
		var others []string
		for _, c := range res.Explain().Candidates {
			if c.Lost != "" && c.Kind != best.Kind && !slices.ContainsFunc(others, func(o string) bool { return strings.HasPrefix(o, string(c.Kind)+" ") }) {
				others = append(others, fmt.Sprintf("%s (%s)", c.Kind, c.Lost))
			}
		}
		if len(others) > 0 {
			note += "; also current: " + strings.Join(others, ", ")
		}
	} else if slices.ContainsFunc(res.Assertions, assertion.Assertion.Live) {
		// Current claims, but none about a company the article is tied to (a rate
		// decision, a sector rule): nothing here disagrees with the lens, whose
		// reading stands.
		s.Reasons = append(s.Reasons, "the article's current claims name no company; the lens's reading stands")
		return s
	} else {
		kind, mat = model.EventOther, model.EventOther.BaseMateriality()
		var dead []string
		for _, x := range res.Assertions {
			if why := x.Why(); why != "" {
				dead = append(dead, fmt.Sprintf("%s (%s)", x.Kind, why))
			} else if !x.Live() || !slices.ContainsFunc(x.Parties, func(p assertion.Party) bool { return p.Role != assertion.Mentioned }) {
				dead = append(dead, fmt.Sprintf("%s (about no company)", x.Kind))
			}
		}
		if len(dead) == 0 {
			return s // nothing was claimed: the lens's reading of an article of no kind stands
		}
		note = "no current event: " + strings.Join(dead, "; ")
	}
	changed := kind != s.Kind || mat != s.Materiality
	if changed {
		rel := s.Relevance + 0.15*float64(mat-s.Materiality)
		rel = math.Min(math.Max(rel, 0), 1)
		if lens.Noise(kind) && rel > 0.35 {
			rel = 0.35
		}
		s.Relevance = math.Round(rel*1000) / 1000
		note = fmt.Sprintf("lens read the first keyword as %s (materiality %d); ", s.Kind, s.Materiality) + note
	}
	s.Kind, s.Materiality = kind, mat
	s.Reasons = append(s.Reasons, note)
	if changed {
		// A stored score is immutable per version, so a reading that differs from
		// the lens's carries the claims' version; one that agrees keeps the lens's.
		s.LensVersion = lensVersion(s.LensVersion, res.Version)
	}
	return s
}

// present sets the score of a from its claims.
func (p *Pipeline) present(a *model.Article) {
	a.Score = Present(*a, Claims(*a, p.names, p.cfg.ItemRating))
}
