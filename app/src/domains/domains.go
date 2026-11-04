// Package domains answers "which domains produce how many relevant news
// today", the aggregate that shows which sources are worth keeping.
package domains

import (
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

// unknown is the domain name for articles that carry no domain.
const unknown = "(unknown)"

// Stat is one domain's output for a day.
type Stat struct {
	Domain   string
	Total    int
	Relevant int
}

// Ratio is the share of the domain's articles that were relevant.
func (s Stat) Ratio() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Relevant) / float64(s.Total)
}

// Aggregate counts articles per domain, relevant ones against threshold, most
// relevant first, then by total, then by name. Domains are compared after
// normalisation. Subdomains other than "www" stay separate, so news.example.com
// and example.com are two domains: merging them correctly needs a public suffix
// list, which the project does not have yet.
func Aggregate(articles []model.Article, threshold float64) []Stat {
	index := make(map[string]int)
	var out []Stat
	for _, a := range articles {
		d := normalize(a.Domain)
		i, ok := index[d]
		if !ok {
			i = len(out)
			index[d] = i
			out = append(out, Stat{Domain: d})
		}
		out[i].Total++
		if a.Score.Relevant(threshold) {
			out[i].Relevant++
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Relevant != b.Relevant {
			return a.Relevant > b.Relevant
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return a.Domain < b.Domain
	})
	return out
}

// normalize lower-cases d, removes surrounding space, a leading "www." and a
// trailing dot. An empty result becomes the unknown domain.
func normalize(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(d, "www.")
	d = strings.TrimSuffix(d, ".")
	if d == "" {
		return unknown
	}
	return d
}
