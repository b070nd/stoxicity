package watch

import (
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Options carries what the watch does not own.
type Options struct {
	// DefaultThreshold is used when the watch sets no lens.threshold.
	DefaultThreshold float64
	// Query evaluates Watch.Query against an article (package filter). Nil
	// means the query is not applied.
	Query func(model.Article) bool
	// Detected is the time Evaluate stamps on the hits it makes: when the
	// ledger found the articles, not when they were published. Zero leaves
	// Hit.At zero, which the store records as the time of the write.
	Detected time.Time
}

// Match reports whether the article belongs to the watch.
func (w Watch) Match(a model.Article, o Options) bool {
	threshold := o.DefaultThreshold
	if w.Lens.Threshold > 0 {
		threshold = w.Lens.Threshold
	}
	if !a.Score.Relevant(threshold) || a.Score.Materiality < w.Lens.MinMateriality {
		return false
	}

	s := w.Scope
	if inDomains(s.ExcludeDomains, a.Domain) {
		return false
	}
	if len(s.Domains) > 0 && !inDomains(s.Domains, a.Domain) {
		return false
	}
	if len(s.Groups) > 0 && !slices.Contains(s.Groups, a.Score.Kind.Group()) {
		return false
	}
	if len(s.Kinds) > 0 && !slices.Contains(s.Kinds, a.Score.Kind) {
		return false
	}

	if len(s.Tickers)+len(s.Topics)+len(s.Sectors) > 0 {
		if !anyFold(s.Tickers, a.Tickers) &&
			!anyFold(s.Topics, a.Topics) &&
			!containsFold(s.Sectors, a.Score.Sector) {
			return false
		}
	}

	if w.Query != "" && o.Query != nil && !o.Query(a) {
		return false
	}
	return true
}

// Evaluate returns a Hit for every article that matches the watch, with the
// alerts whose conditions also hold. Each hit is stamped with o.Detected.
func Evaluate(w Watch, articles []model.Article, o Options) []Hit {
	var hits []Hit
	for _, a := range articles {
		if !w.Match(a, o) {
			continue
		}
		h := Hit{WatchID: w.ID, ArticleID: a.ID, At: o.Detected}
		for _, al := range w.Alerts {
			if al.When.holds(a) {
				h.Alerts = append(h.Alerts, al.Name)
			}
		}
		hits = append(hits, h)
	}
	return hits
}

func (c When) holds(a model.Article) bool {
	return a.Score.Relevance >= c.MinRelevance &&
		a.Score.Materiality >= c.MinMateriality &&
		(len(c.Kinds) == 0 || slices.Contains(c.Kinds, a.Score.Kind))
}

// inDomains reports whether d is one of the listed domains or a subdomain of
// one. Entries and d are compared without case and without a trailing dot.
func inDomains(list []string, d string) bool {
	d = strings.ToLower(strings.TrimSuffix(d, "."))
	return slices.ContainsFunc(list, func(entry string) bool {
		entry = strings.ToLower(strings.TrimSuffix(entry, "."))
		return entry != "" && (d == entry || strings.HasSuffix(d, "."+entry))
	})
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}

func anyFold(list, in []string) bool {
	return slices.ContainsFunc(in, func(s string) bool { return containsFold(list, s) })
}
