// Package summarize writes the short summaries on stock and topic pages.
// The first implementation is extractive: it picks sentences from the
// articles and links each to its source, so nothing is invented.
package summarize

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/b070nd/stoxicity/app/src/model"
)

const (
	minLength     = 20  // characters; shorter sentences are fragments
	maxLength     = 300 // characters; longer sentences are unlikely to be one line
	dupSimilarity = 0.6 // Jaccard similarity at or above which two lines say the same thing
	recencyBonus  = 0.5 // added to articles in the newest third
)

// abbreviations are words that end in a period without ending a sentence. A
// single capital letter, such as the "J." in "J. Smith", is also treated as one.
var abbreviations = map[string]bool{
	"inc": true, "corp": true, "ltd": true, "co": true, "u.s": true, "u.k": true,
	"no": true, "vs": true, "mr": true, "mrs": true, "dr": true, "st": true, "jr": true,
}

// Line is one summary sentence and where it came from.
type Line struct {
	Text    string
	Article model.Article
}

// Summary is the text for a page.
type Summary struct {
	Lines []Line
}

// candidate is a sentence that might become a Line.
type candidate struct {
	text    string
	article model.Article
	score   float64
	seq     int // position in generation order, the last tie-break
}

// Summarize picks up to maxLines sentences from articles, most material first.
// Each sentence from an article's title and summary is scored as twice its
// materiality plus its relevance, with a bonus for articles in the newest third.
// Sentences shorter than 20 or longer than 300 characters are skipped. A sentence
// that is at least 60% alike, by shared words, to one already chosen is dropped,
// and at most one line comes from each non-empty ClusterID. Ties go to the
// earlier article, then the lower article ID. maxLines below 1 returns no lines.
func Summarize(articles []model.Article, maxLines int) Summary {
	if maxLines <= 0 {
		return Summary{}
	}
	recent := newestThird(articles)
	var cands []candidate
	for i, a := range articles {
		score := 2*float64(a.Score.Materiality) + a.Score.Relevance
		if recent[i] {
			score += recencyBonus
		}
		texts := append(sentences(a.Title), sentences(a.Summary)...)
		for _, text := range texts {
			n := utf8.RuneCountInString(text)
			if n < minLength || n > maxLength {
				continue
			}
			cands = append(cands, candidate{text: text, article: a, score: score, seq: len(cands)})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return better(cands[i], cands[j]) })

	var lines []Line
	used := make(map[string]bool)
	var kept []map[string]bool
	for _, c := range cands {
		if len(lines) == maxLines {
			break
		}
		cluster := c.article.ClusterID
		if cluster != "" && used[cluster] {
			continue
		}
		words := wordSet(c.text)
		if isDuplicate(words, kept) {
			continue
		}
		if cluster != "" {
			used[cluster] = true
		}
		kept = append(kept, words)
		lines = append(lines, Line{Text: c.text, Article: c.article})
	}
	return Summary{Lines: lines}
}

// better reports whether candidate a should come before b.
func better(a, b candidate) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if !a.article.Published.Equal(b.article.Published) {
		return a.article.Published.Before(b.article.Published)
	}
	if a.article.ID != b.article.ID {
		return a.article.ID < b.article.ID
	}
	return a.seq < b.seq
}

// newestThird reports, for each article index, whether that article is among
// the newest third of the input by publication time.
func newestThird(articles []model.Article) []bool {
	order := make([]int, len(articles))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return articles[order[i]].Published.After(articles[order[j]].Published)
	})
	n := (len(articles) + 2) / 3
	out := make([]bool, len(articles))
	for _, i := range order[:n] {
		out[i] = true
	}
	return out
}

// sentences splits text into sentences. It breaks after a '.', '!' or '?' that
// is followed by a space and then an upper-case letter or a digit. A period
// that ends a known abbreviation does not break. The result is not filtered by
// length.
func sentences(text string) []string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	var out []string
	start := 0
	for i, r := range runes {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i+2 >= len(runes) || runes[i+1] != ' ' {
			continue
		}
		if next := runes[i+2]; !unicode.IsUpper(next) && !unicode.IsDigit(next) {
			continue
		}
		if r == '.' && isAbbreviation(runes[:i]) {
			continue
		}
		out = append(out, string(runes[start:i+1]))
		start = i + 2
	}
	return append(out, string(runes[start:]))
}

// isAbbreviation reports whether the word that ends before (the text before a
// period) is a known abbreviation or a single capital letter.
func isAbbreviation(before []rune) bool {
	i := len(before)
	for i > 0 && (unicode.IsLetter(before[i-1]) || before[i-1] == '.') {
		i--
	}
	word := before[i:]
	if len(word) == 0 {
		return false
	}
	if len(word) == 1 && unicode.IsUpper(word[0]) {
		return true
	}
	return abbreviations[strings.ToLower(string(word))]
}

// wordSet is the set of lower-case words in text. A word is a run of letters
// or digits.
func wordSet(text string) map[string]bool {
	set := make(map[string]bool)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		set[w] = true
	}
	return set
}

// isDuplicate reports whether words is at least dupSimilarity alike to any set in kept.
func isDuplicate(words map[string]bool, kept []map[string]bool) bool {
	for _, k := range kept {
		if jaccard(words, k) >= dupSimilarity {
			return true
		}
	}
	return false
}

// jaccard is the size of the intersection of a and b over the size of their union.
func jaccard(a, b map[string]bool) float64 {
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}
