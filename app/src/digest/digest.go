// Package digest assembles the daily summary: top relevant articles, the
// per-domain table, new stock candidates and rising trends.
package digest

import (
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/discover"
	"github.com/b070nd/stoxicity/app/src/domains"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/summarize"
	"github.com/b070nd/stoxicity/app/src/trends"
)

const (
	defaultTopN         = 10
	defaultSummaryLines = 5
	defaultBaselineDays = 14
)

// Digest is the result for one day, or for a span of days.
type Digest struct {
	Day time.Time // midnight at the start of the day, in the location given to Build
	// Until is the end of the span BuildWindow covered; zero for the single
	// day of Build.
	Until      time.Time
	Top        []model.Article
	Domains    []domains.Stat
	Candidates []discover.Candidate
	Trends     []trends.Trend
	Summary    summarize.Summary // extracted from Top
}

// Options controls Build.
type Options struct {
	Threshold    float64 // relevance at or above which an article counts
	TopN         int     // most articles in Top; 0 means 10
	SummaryLines int     // most lines in Summary; 0 means 5
	BaselineDays int     // days the baseline covers; 0 means 14
}

// Build assembles the digest for the calendar day of day in loc (UTC if loc is
// nil). Today's articles are those published on that date in loc, so a filing
// made late in the evening New York time belongs to that day. articles holds
// the articles to draw from; baseline holds the earlier articles that trends
// are measured against, typically the BaselineDays before the day.
//
// Top holds today's relevant articles, most material first, with the articles
// of one cluster collapsed to the earliest of them. Domains, Candidates and
// Trends are computed from today's articles against the baseline. Summary is
// extracted from Top.
func Build(day time.Time, loc *time.Location, articles, baseline []model.Article, watchlist []string, opts Options) Digest {
	if loc == nil {
		loc = time.UTC
	}
	y, m, d := day.In(loc).Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	opts = withDefaults(opts)
	return assemble(start, time.Time{}, publishedOn(articles, start, loc), baseline, watchlist, opts)
}

// BuildWindow assembles the digest for the articles published in [from, to), a
// span such as the last 24 hours or the last week, instead of one calendar day.
// Day is the midnight of from's date in loc and Until is to. Everything else
// is as in Build: baseline holds the earlier articles that trends are measured
// against.
func BuildWindow(from, to time.Time, loc *time.Location, articles, baseline []model.Article, watchlist []string, opts Options) Digest {
	if loc == nil {
		loc = time.UTC
	}
	opts = withDefaults(opts)
	y, m, d := from.In(loc).Date()
	var within []model.Article
	for _, a := range articles {
		if !a.Published.Before(from) && a.Published.Before(to) {
			within = append(within, a)
		}
	}
	return assemble(time.Date(y, m, d, 0, 0, 0, 0, loc), to, within, baseline, watchlist, opts)
}

// withDefaults fills the options that are zero.
func withDefaults(opts Options) Options {
	if opts.TopN <= 0 {
		opts.TopN = defaultTopN
	}
	if opts.SummaryLines <= 0 {
		opts.SummaryLines = defaultSummaryLines
	}
	if opts.BaselineDays <= 0 {
		opts.BaselineDays = defaultBaselineDays
	}
	return opts
}

// assemble builds the digest of today, the articles in the span that starts on
// the day start. The options are complete.
func assemble(start, until time.Time, today, baseline []model.Article, watchlist []string, opts Options) Digest {
	top := topArticles(relevant(today, opts.Threshold), opts.TopN)
	return Digest{
		Day:        start,
		Until:      until,
		Top:        top,
		Domains:    domains.Aggregate(today, opts.Threshold),
		Candidates: discover.Find(today, watchlist, discover.Options{Threshold: opts.Threshold}),
		Trends: trends.Detect(today, baseline, opts.BaselineDays, trends.Options{
			Threshold: opts.Threshold,
		}),
		Summary: summarize.Summarize(top, opts.SummaryLines),
	}
}

// publishedOn returns the articles published on the calendar date of start in loc.
func publishedOn(articles []model.Article, start time.Time, loc *time.Location) []model.Article {
	key := start.Format(time.DateOnly)
	var out []model.Article
	for _, a := range articles {
		if a.Published.In(loc).Format(time.DateOnly) == key {
			out = append(out, a)
		}
	}
	return out
}

// relevant returns the articles whose relevance reaches threshold.
func relevant(articles []model.Article, threshold float64) []model.Article {
	var out []model.Article
	for _, a := range articles {
		if a.Score.Relevant(threshold) {
			out = append(out, a)
		}
	}
	return out
}

// topArticles collapses each cluster to its earliest article, sorts the rest by
// materiality, relevance and recency, and keeps the first n.
func topArticles(articles []model.Article, n int) []model.Article {
	earliest := make(map[string]int)
	var out []model.Article
	for _, a := range articles {
		if a.ClusterID == "" {
			out = append(out, a)
			continue
		}
		i, ok := earliest[a.ClusterID]
		if !ok {
			earliest[a.ClusterID] = len(out)
			out = append(out, a)
			continue
		}
		if before(a, out[i]) {
			out[i] = a
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Score.Materiality != b.Score.Materiality {
			return a.Score.Materiality > b.Score.Materiality
		}
		if a.Score.Relevance != b.Score.Relevance {
			return a.Score.Relevance > b.Score.Relevance
		}
		if !a.Published.Equal(b.Published) {
			return a.Published.After(b.Published)
		}
		return a.ID < b.ID
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// before reports whether a was published earlier than b, with the ID breaking a tie.
func before(a, b model.Article) bool {
	if !a.Published.Equal(b.Published) {
		return a.Published.Before(b.Published)
	}
	return a.ID < b.ID
}
