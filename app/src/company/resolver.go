package company

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/entity"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Resolver finds companies in text. It wraps an entity index built so that a
// company with several tickers (share classes, warrants) is found by name as
// one company, while every ticker still has its name for display.
type Resolver struct {
	*entity.Index
	display map[string]model.Entity
	byCIK   map[string][]model.Entity
}

var _ entity.CIKResolver = (*Resolver)(nil)

// NewResolver builds a Resolver from the master list and extra tickers that
// the user follows (the watchlist, the tickers in watches). An extra ticker
// that the master already knows adds nothing; one it does not know (a non-US
// listing, say) is found by its ticker alone.
//
// Of the tickers that share a CIK, one is the main one: the shortest, then the
// first alphabetically. Only the main ticker is matched by company name, so
// "Alphabet" is one company in a text, not two. The others keep their name in
// Lookup and are found by their ticker.
func NewResolver(m Master, extra []string) *Resolver {
	byCIK := make(map[string][]int, len(m.Entities))
	for i, e := range m.Entities {
		byCIK[e.CIK] = append(byCIK[e.CIK], i)
	}
	main := make(map[int]bool, len(byCIK))
	for cik, idx := range byCIK {
		if cik == "" {
			for _, i := range idx {
				main[i] = true
			}
			continue
		}
		best := idx[0]
		for _, i := range idx[1:] {
			a, b := m.Entities[i].Ticker, m.Entities[best].Ticker
			if len(a) < len(b) || (len(a) == len(b) && a < b) {
				best = i
			}
		}
		main[best] = true
	}

	display := make(map[string]model.Entity, len(m.Entities)+len(extra))
	matching := make([]model.Entity, 0, len(m.Entities)+len(extra))
	for i, e := range m.Entities {
		raw := e.Name
		e.Name = tidyName(raw)
		display[e.Ticker] = e
		if !main[i] {
			e.Name = ""
		} else if raw != e.Name {
			// The press writes NVIDIA and Nvidia alike and the index is case
			// sensitive, so the SEC's spelling stays as an alias.
			e.Aliases = append(slices.Clone(e.Aliases), cleanTags(raw))
		}
		matching = append(matching, e)
	}
	for _, t := range extra {
		t = strings.ToUpper(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if _, known := display[t]; known {
			continue
		}
		e := model.Entity{Ticker: t}
		display[t] = e
		matching = append(matching, e)
	}
	slices.SortFunc(matching, func(a, b model.Entity) int { return strings.Compare(a.Ticker, b.Ticker) })
	ciks := make(map[string][]model.Entity, len(byCIK))
	for _, e := range display {
		if e.CIK != "" {
			ciks[e.CIK] = append(ciks[e.CIK], e)
		}
	}
	for _, list := range ciks {
		slices.SortFunc(list, func(a, b model.Entity) int { return strings.Compare(a.Ticker, b.Ticker) })
	}
	return &Resolver{Index: entity.New(matching), display: display, byCIK: ciks}
}

// ByCIK returns every ticker of the company with the CIK, with its name.
func (r *Resolver) ByCIK(cik string) []model.Entity {
	return slices.Clone(r.byCIK[strings.TrimSpace(cik)])
}

// Lookup returns the entity with the ticker, with its name and CIK.
func (r *Resolver) Lookup(ticker string) (model.Entity, bool) {
	if e, ok := r.display[strings.ToUpper(strings.TrimSpace(ticker))]; ok {
		return e, true
	}
	return model.Entity{}, false
}

// Find returns the companies mentioned in text, each with its name and CIK
// whatever the index matched on.
func (r *Resolver) Find(text string) []model.Entity {
	found := r.Index.Find(text)
	for i, e := range found {
		if d, ok := r.display[e.Ticker]; ok {
			found[i] = d
		}
	}
	return found
}

// registrantTag matches the state or "new company" tag that EDGAR appends to a
// conformed name, such as "/DE/" or "\\DE\\".
var registrantTag = regexp.MustCompile(`\s*[/\\][A-Za-z.\- ]{1,12}[/\\]\s*$`)

// cleanTags removes a trailing registrant tag from a conformed name.
func cleanTags(name string) string {
	return strings.TrimSpace(registrantTag.ReplaceAllString(name, ""))
}

// tidyName makes an EDGAR conformed name readable. A name written entirely in
// capitals is put in title case, except short words, which are usually
// initials (IBM, AMD, HP); a mixed-case name is left as it is. A registrant tag
// at the end is dropped.
func tidyName(name string) string {
	name = cleanTags(name)
	if strings.IndexFunc(name, unicode.IsLower) >= 0 {
		return name
	}
	words := strings.Fields(name)
	for i, w := range words {
		letters := 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				letters++
			}
		}
		_, suffix := titleSuffixes[strings.Trim(strings.ToLower(w), ".,")]
		if !suffix && (letters <= 3 || strings.ContainsAny(w, "0123456789")) {
			continue
		}
		words[i] = titleWord(w)
	}
	return strings.Join(words, " ")
}

// titleSuffixes are short words that are written in title case, not as
// initials, when a name is put in title case.
var titleSuffixes = map[string]struct{}{"inc": {}, "co": {}, "ltd": {}, "cos": {}, "the": {}, "and": {}, "of": {}}

// titleWord upper-cases the first letter of w and lower-cases the rest.
func titleWord(w string) string {
	rs := []rune(strings.ToLower(w))
	for i, r := range rs {
		if unicode.IsLetter(r) {
			rs[i] = unicode.ToUpper(r)
			break
		}
	}
	return string(rs)
}

// TidyName writes an EDGAR conformed name the way the press does: NVIDIA CORP
// becomes Nvidia Corp, and a registrant tag such as /DE/ is dropped.
func TidyName(name string) string { return tidyName(name) }

// CleanTags removes a trailing registrant tag from a conformed name.
func CleanTags(name string) string { return cleanTags(name) }
