// Package tagcloud turns term counts into a ranked, tiered list that the TUI
// draws as a cloud: tier 5 is bold and bright, tier 1 is dim. Each term maps
// to a route, so selecting it opens the topic or stock page.
package tagcloud

import (
	"math"
	"sort"
)

// Tiers is the number of weight steps a term can have.
const Tiers = 5

// Term is one word in the cloud.
type Term struct {
	Text  string
	Count int
	Tier  int // 1..Tiers, larger is more prominent
}

// Build keeps the max most frequent terms (ties broken alphabetically) and
// assigns tiers on a logarithmic scale between the smallest and largest kept
// count. Term counts are heavy-tailed: on a linear scale one dominant term
// pushes every other term into the lowest tier. max <= 0 keeps all terms.
func Build(counts map[string]int, max int) []Term {
	terms := make([]Term, 0, len(counts))
	for text, n := range counts {
		if n > 0 {
			terms = append(terms, Term{Text: text, Count: n})
		}
	}
	sort.Slice(terms, func(i, j int) bool {
		if terms[i].Count != terms[j].Count {
			return terms[i].Count > terms[j].Count
		}
		return terms[i].Text < terms[j].Text
	})
	if max > 0 && len(terms) > max {
		terms = terms[:max]
	}
	if len(terms) == 0 {
		return terms
	}
	hi, lo := math.Log(float64(terms[0].Count)), math.Log(float64(terms[len(terms)-1].Count))
	for i := range terms {
		if hi == lo {
			terms[i].Tier = Tiers
			continue
		}
		frac := (math.Log(float64(terms[i].Count)) - lo) / (hi - lo)
		terms[i].Tier = 1 + int(frac*float64(Tiers-1)+0.5)
	}
	return terms
}
