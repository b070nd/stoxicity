// Package discover finds stocks the user does not follow yet: tickers that
// show up repeatedly in relevant news but are not on the watchlist.
package discover

import (
	"sort"
	"strings"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/model"
)

// defaultMinMentions is how many stories must mention a ticker before it is a
// candidate, when the caller does not say otherwise.
const defaultMinMentions = 2

// Candidate is a ticker worth a closer look.
type Candidate struct {
	Ticker   string
	Mentions int             // distinct stories that mention it
	Weight   int             // sum of the materiality of the articles in Articles
	Articles []model.Article // one article per story, newest first
}

// Options controls Find.
type Options struct {
	Threshold   float64 // relevance at or above which an article counts
	MinMentions int     // stories needed for a candidate; 0 means 2
	Limit       int     // most candidates returned; 0 means no limit
}

// storyKey identifies a story. Articles sharing a non-empty ClusterID are one
// story; an article without one stands alone and is keyed by its ID.
type storyKey struct{ cluster, id string }

// keyOf is the story an article belongs to.
func keyOf(a model.Article) storyKey {
	if a.ClusterID != "" {
		return storyKey{cluster: a.ClusterID}
	}
	return storyKey{id: a.ID}
}

// Find returns candidates from articles, leaving out the watchlist. Only
// relevant articles count. Tickers are compared without regard to case and
// reported in upper case. Each story counts once per ticker, represented by its
// most material article, with the newest winning a tie. Candidates are sorted
// by weight, then mentions, then ticker.
func Find(articles []model.Article, watchlist []string, opts Options) []Candidate {
	minMentions := opts.MinMentions
	if minMentions <= 0 {
		minMentions = defaultMinMentions
	}
	watched := make(map[string]bool, len(watchlist))
	for _, w := range watchlist {
		watched[symbol(w)] = true
	}

	stories := make(map[string]map[storyKey]model.Article)
	for _, a := range articles {
		if !a.Score.Relevant(opts.Threshold) {
			continue
		}
		k := keyOf(a)
		seen := make(map[string]bool, len(a.Tickers))
		for _, t := range a.Tickers {
			s := symbol(t)
			if s == "" || watched[s] || seen[s] || !mayBeSubject(a, s) {
				continue
			}
			seen[s] = true
			if stories[s] == nil {
				stories[s] = make(map[storyKey]model.Article)
			}
			if cur, ok := stories[s][k]; !ok || moreRepresentative(a, cur) {
				stories[s][k] = a
			}
		}
	}

	var out []Candidate
	for ticker, byStory := range stories {
		if len(byStory) < minMentions {
			continue
		}
		c := Candidate{Ticker: ticker, Mentions: len(byStory)}
		for _, a := range byStory {
			c.Weight += a.Score.Materiality
			c.Articles = append(c.Articles, a)
		}
		sort.Slice(c.Articles, func(i, j int) bool { return newer(c.Articles[i], c.Articles[j]) })
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Weight != b.Weight {
			return a.Weight > b.Weight
		}
		if a.Mentions != b.Mentions {
			return a.Mentions > b.Mentions
		}
		return a.Ticker < b.Ticker
	})
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out
}

// symbol is a ticker in the form used for comparison.
func symbol(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// moreRepresentative reports whether a should stand for its story in place of b.
func moreRepresentative(a, b model.Article) bool {
	if a.Score.Materiality != b.Score.Materiality {
		return a.Score.Materiality > b.Score.Materiality
	}
	return newer(a, b)
}

// newer reports whether a was published after b, with the ID breaking a tie.
func newer(a, b model.Article) bool {
	if !a.Published.Equal(b.Published) {
		return a.Published.After(b.Published)
	}
	return a.ID < b.ID
}

// maxPlainTickers is the most tickers an article may carry for each of them to
// count without being named in the title. An article that lists more is a
// roundup, a table or a footer of cashtags, and a ticker it only lists is a
// background mention, not the company the story is about.
const maxPlainTickers = 3

// mayBeSubject says the article can be about the ticker's company: it names
// few companies, or the ticker stands in its title. The articles do not record
// which company is the subject (see PROTOCOL.md), so this keeps out the plain
// roundups and footers but cannot tell a counterparty from the subject.
func mayBeSubject(a model.Article, ticker string) bool {
	if len(a.Tickers) <= maxPlainTickers {
		return true
	}
	for _, w := range strings.FieldsFunc(strings.ToUpper(a.Title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-'
	}) {
		if strings.Trim(w, ".-") == ticker {
			return true
		}
	}
	return false
}
