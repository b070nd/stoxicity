package fundamentals

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// CompanyFactsURL is the SEC's list of every fact one company ever reported; it
// ends with a slash and is followed by CIK and the ten digits of the number.
const CompanyFactsURL = "https://data.sec.gov/api/xbrl/companyfacts/"

// Source names for where a fact was read from.
const (
	SourceFrames       = "sec-frames"
	SourceCompanyFacts = "sec-companyfacts"
)

// History reads every fact of the metrics we keep that one company ever
// reported, each with the filing it came from and the day that filing was filed.
// A figure restated in a later filing appears again with the later accession.
// The answer holds the whole history of one company (up to several megabytes),
// so it is asked for one company on request, never for the market.
func (f Fetcher) History(ctx context.Context, cik string) ([]Fact, error) {
	base := f.HistoryURL
	if base == "" {
		base = CompanyFactsURL
	}
	cik = fmt.Sprintf("%010s", strings.TrimLeft(cik, "0"))
	resp, err := f.Client.Do(ctx, httpx.Request{URL: fmt.Sprintf("%sCIK%s.json", base, cik)})
	if err != nil {
		return nil, fmt.Errorf("fundamentals: history of %s: %w", cik, err)
	}
	return parseCompanyFacts(resp.Body, cik)
}

type cfFact struct {
	Start string  `json:"start"`
	End   string  `json:"end"`
	Val   float64 `json:"val"`
	Accn  string  `json:"accn"`
	Form  string  `json:"form"`
	Filed string  `json:"filed"`
}

func parseCompanyFacts(body []byte, cik string) ([]Fact, error) {
	var ans struct {
		Facts map[string]map[string]struct {
			Units map[string][]cfFact `json:"units"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, fmt.Errorf("fundamentals: read the history of %s: %w", cik, err)
	}
	var out []Fact
	for _, s := range sources {
		for _, x := range ans.Facts[s.taxonomy][s.concept].Units[s.unit] {
			if x.End == "" || x.Accn == "" || math.IsNaN(x.Val) || math.IsInf(x.Val, 0) {
				continue
			}
			fact := Fact{CIK: cik, Metric: s.metric, End: x.End, Value: x.Val, Accession: x.Accn,
				Taxonomy: s.taxonomy, Concept: s.concept, Unit: s.unit, Form: x.Form, Filed: x.Filed}
			if !s.instant {
				days, ok := daysBetween(x.Start, x.End)
				if !ok || days < 350 || days > 380 {
					continue
				}
				fact.Start = x.Start
			}
			out = append(out, fact)
		}
	}
	return out, nil
}

// rank is the place of a fact's concept in the order of preference for its
// metric; lower is preferred.
func rank(f Fact) int {
	for i, s := range sources {
		if s.metric == f.Metric && s.concept == f.Concept {
			return i
		}
	}
	return len(sources)
}

// KnownBy returns the facts whose filing day is known and before the day of the
// time: what had been filed, and so could have been read, by then.
func KnownBy(facts []Fact, at time.Time) []Fact {
	cut := at.UTC().Format(time.DateOnly)
	var out []Fact
	for _, f := range facts {
		if f.Filed != "" && f.Filed < cut {
			out = append(out, f)
		}
	}
	return out
}

// AsOf is what the facts say as it was known at a time: only facts filed on a
// day before the time's day (a filing is counted as public the day after, since
// the time of day it was accepted is not kept), and for each metric and period
// the preferred concept, then the latest filing among those. A fact whose filing
// day is not known (Filed empty) is left out, because it cannot be shown to have
// been public. The result is what Summarize reads.
func AsOf(facts []Fact, at time.Time) []Fact {
	type key struct{ cik, metric, start, end string }
	best := map[key]Fact{}
	for _, f := range KnownBy(facts, at) {
		if f.Dimensions != "" {
			continue
		}
		k := key{f.CIK, f.Metric, f.Start, f.End}
		cur, ok := best[k]
		switch {
		case !ok:
		case rank(f) < rank(cur):
		case rank(f) == rank(cur) && (f.Filed > cur.Filed || f.Filed == cur.Filed && f.Accession > cur.Accession):
		default:
			continue
		}
		best[k] = f
	}
	out := make([]Fact, 0, len(best))
	for _, f := range best {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b Fact) int {
		return strings.Compare(a.CIK+a.Metric+a.End+a.Start, b.CIK+b.Metric+b.End+b.Start)
	})
	return out
}

// Revisions returns, for each concept and period that more than one filing
// reported with different values, the figures in the order they were filed,
// each shown once where it first appeared and again only where it changed: what
// a restatement changed. Later filings that repeat the same figure (the next
// quarters' comparatives) are not revisions. The key is "concept period-end".
func Revisions(facts []Fact) map[string][]Fact {
	by := map[string][]Fact{}
	for _, f := range facts {
		if f.Dimensions != "" {
			continue
		}
		k := f.Concept + " " + f.End
		by[k] = append(by[k], f)
	}
	out := map[string][]Fact{}
	for k, fs := range by {
		slices.SortFunc(fs, func(a, b Fact) int { return strings.Compare(a.Filed+a.Accession, b.Filed+b.Accession) })
		var changes []Fact
		for _, f := range fs {
			if n := len(changes); n == 0 || changes[n-1].Value != f.Value {
				changes = append(changes, f)
			}
		}
		if len(changes) > 1 {
			out[k] = changes
		}
	}
	return out
}
