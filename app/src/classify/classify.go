// Package classify files an article under topics and a sector. It is the
// producer of Article.Topics and Score.Sector, which the topic pages, the tag
// cloud, the topic: and sector: filter operators and the scope of a watch read.
//
// The rules come from the configuration (topics and sectors in config.yaml)
// and are plain words and tickers; there is no model. Everything is
// deterministic and explainable.
//
// # Matching
//
// The text is the title, a space and the summary. It is lower-cased, dashes and
// curly quotes are unified and white space is collapsed. A word or phrase of a
// rule matches when it occurs in the text with a word boundary at each end
// (where it starts or ends with a letter or digit), so "ai" does not match
// "said" and "rate cut" does not match "karate cutlery". A ticker matches when
// it is in Article.Tickers, compared in upper case. Tickers are never searched
// for in the text: resolving them is the job of package entity.
//
// # Topics
//
// An article belongs to a topic when any of its words matches or any of its
// tickers is among the article's tickers. The topics come back sorted and
// without repeats.
//
// # Sector
//
// Each sector scores 3 for every one of its tickers among the article's
// tickers plus 1 for every distinct one of its words found in the text. The
// sector with the highest score wins. A tie for the highest score, or a best
// score of zero, gives no sector: the classifier never guesses.
//
// Version changes whenever a rule changes.
package classify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Points a sector scores for each hit.
const (
	tickerPoints = 3
	wordPoints   = 1
)

// rule is one topic or sector with its words and tickers normalised.
type rule struct {
	name    string
	words   []string // normalised, distinct, sorted
	tickers []string // upper case, distinct, sorted
}

// Classifier files articles under the configured topics and sectors. It is
// immutable after New and safe for concurrent use. A nil *Classifier is valid
// and classifies nothing.
type Classifier struct {
	topics  []rule // sorted by name
	sectors []rule
	version string
}

// New returns a Classifier for the given rules. Rules that share a name are
// merged, so a name is one topic or one sector whichever way it was written.
// Package classify imports package config for the rule types; config does not
// import classify, so there is no import cycle.
func New(topics []config.TopicRule, sectors []config.SectorRule) *Classifier {
	c := &Classifier{}
	t := make([]rule, len(topics))
	for i, r := range topics {
		t[i] = rule{name: r.Name, words: r.Words, tickers: r.Tickers}
	}
	s := make([]rule, len(sectors))
	for i, r := range sectors {
		s[i] = rule{name: r.Name, words: r.Words, tickers: r.Tickers}
	}
	c.topics, c.sectors = build(t), build(s)
	c.version = "c1-" + digest(c.topics, c.sectors)
	return c
}

// Version identifies the rules this Classifier files articles by. It is "c1-"
// followed by 8 hex characters of a hash over the topics and sectors with their
// words and tickers, so it does not depend on the order in which the rules or
// their words were written. A nil Classifier has the empty version.
func (c *Classifier) Version() string {
	if c == nil {
		return ""
	}
	return c.version
}

// Classify returns the topics of a, sorted and without repeats, and its
// sector, or "" when the rules do not point to exactly one.
func (c *Classifier) Classify(a model.Article) (topics []string, sector string) {
	if c == nil || (len(c.topics) == 0 && len(c.sectors) == 0) {
		return nil, ""
	}
	text := normalize(a.Title + " " + a.Summary)
	tickers := make(map[string]bool, len(a.Tickers))
	for _, t := range a.Tickers {
		tickers[strings.ToUpper(strings.TrimSpace(t))] = true
	}

	for _, r := range c.topics {
		if r.matches(text, tickers) {
			topics = append(topics, r.name)
		}
	}

	best, bestScore, tie := "", 0, false
	for _, r := range c.sectors {
		s := r.score(text, tickers)
		switch {
		case s > bestScore:
			best, bestScore, tie = r.name, s, false
		case s == bestScore && s > 0:
			tie = true
		}
	}
	if bestScore > 0 && !tie {
		sector = best
	}
	return topics, sector
}

// matches reports whether any word is in text or any ticker is in tickers.
func (r rule) matches(text string, tickers map[string]bool) bool {
	for _, t := range r.tickers {
		if tickers[t] {
			return true
		}
	}
	for _, w := range r.words {
		if hasPhrase(text, w) {
			return true
		}
	}
	return false
}

// score is the sector score of text and tickers.
func (r rule) score(text string, tickers map[string]bool) int {
	n := 0
	for _, t := range r.tickers {
		if tickers[t] {
			n += tickerPoints
		}
	}
	for _, w := range r.words {
		if hasPhrase(text, w) {
			n += wordPoints
		}
	}
	return n
}

// build normalises the rules, merges those with the same name and sorts them
// by name. Rules left without a name or without any word or ticker are dropped.
func build(in []rule) []rule {
	byName := make(map[string]*rule, len(in))
	for _, r := range in {
		name := strings.TrimSpace(r.name)
		if name == "" {
			continue
		}
		m := byName[name]
		if m == nil {
			m = &rule{name: name}
			byName[name] = m
		}
		for _, w := range r.words {
			if w = normalize(w); w != "" {
				m.words = append(m.words, w)
			}
		}
		for _, t := range r.tickers {
			if t = strings.ToUpper(strings.TrimSpace(t)); t != "" {
				m.tickers = append(m.tickers, t)
			}
		}
	}
	out := make([]rule, 0, len(byName))
	for _, m := range byName {
		slices.Sort(m.words)
		m.words = slices.Compact(m.words)
		slices.Sort(m.tickers)
		m.tickers = slices.Compact(m.tickers)
		if len(m.words)+len(m.tickers) > 0 {
			out = append(out, *m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// hasPhrase reports whether phrase occurs in text with a word boundary at each
// end where the phrase itself starts or ends with a letter or digit.
func hasPhrase(text, phrase string) bool {
	if phrase == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(phrase)
	last, _ := utf8.DecodeLastRuneInString(phrase)
	for from := 0; from+len(phrase) <= len(text); {
		i := strings.Index(text[from:], phrase)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(phrase)
		okBefore, okAfter := true, true
		if isWord(first) && i > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:i])
			okBefore = !isWord(r)
		}
		if isWord(last) && end < len(text) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			okAfter = !isWord(r)
		}
		if okBefore && okAfter {
			return true
		}
		from = i + 1
	}
	return false
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

var punctuation = strings.NewReplacer(
	"‐", "-", "‑", "-", "‒", "-", "–", "-", "—", "-",
	"‘", "'", "’", "'",
)

// normalize lower-cases s, unifies dashes and quotes and collapses white space.
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(punctuation.Replace(s))), " ")
}

// digest is the first 8 hex characters of sha256 over the canonical encoding of
// the rules. The rules arrive sorted by name with sorted words and tickers.
func digest(topics, sectors []rule) string {
	var b strings.Builder
	for _, group := range []struct {
		kind  string
		rules []rule
	}{{"topic", topics}, {"sector", sectors}} {
		for _, r := range group.rules {
			fmt.Fprintf(&b, "%s %q words %q tickers %q\n", group.kind, r.name, r.words, r.tickers)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:8]
}
