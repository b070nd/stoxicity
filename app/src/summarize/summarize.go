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
// and at most one line comes from each non-empty ClusterID. A sentence that
// differs from a chosen one in denial or in numbers is not alike: it may be the
// correction. Ties go to the
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
	var kept []keptLine
	for _, c := range cands {
		if len(lines) == maxLines {
			break
		}
		cluster := c.article.ClusterID
		words := wordSet(c.text)
		sig := claimOf(c.text)
		// A line that says the opposite of a kept one, or gives other numbers, is
		// not a repeat of it: it may be the correction, and dropping it would
		// leave the claim it corrects.
		if cluster != "" && used[cluster] && !correctsKept(words, sig, cluster, kept) {
			continue
		}
		if isDuplicate(words, sig, kept) {
			continue
		}
		if cluster != "" {
			used[cluster] = true
		}
		kept = append(kept, keptLine{words: words, claim: sig, cluster: cluster})
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

// keptLine is a chosen sentence as the duplicate test sees it.
type keptLine struct {
	words   map[string]bool
	claim   claim
	cluster string
}

// claim is what a sentence asserts beyond its words: whether it denies
// something, and which numbers it gives. Two sentences with the same words but
// another claim are not the same statement.
type claim struct {
	denies  bool
	numbers string // the sorted numbers, joined
}

var negators = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`no not never without neither nor cannot denies denied deny
rejects rejected rejecting cancels cancelled canceled halts halted terminates terminated withdraws
withdrawn scraps scrapped abandons abandoned fails failed`) {
		m[w] = true
	}
	return m
}()

// claimOf reads the denial and the numbers of a sentence.
func claimOf(text string) claim {
	var c claim
	lower := strings.ToLower(text)
	var nums []string
	for _, w := range strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '.' && r != '%' && r != '$' && r != ','
	}) {
		w = strings.Trim(w, ".,'")
		if strings.HasSuffix(w, "n't") || negators[w] {
			c.denies = !c.denies
			continue
		}
		if strings.IndexFunc(w, unicode.IsDigit) >= 0 {
			nums = append(nums, strings.Trim(w, "$,."))
		}
	}
	sort.Strings(nums)
	c.numbers = strings.Join(nums, " ")
	return c
}

// correctsKept says the sentence is about the same thing as a kept line of its
// cluster but claims something else.
func correctsKept(words map[string]bool, c claim, cluster string, kept []keptLine) bool {
	for _, k := range kept {
		if k.cluster == cluster && jaccard(words, k.words) >= dupSimilarity && c != k.claim {
			return true
		}
	}
	return false
}

// isDuplicate reports whether the sentence is at least dupSimilarity alike to a
// kept line and claims the same.
func isDuplicate(words map[string]bool, c claim, kept []keptLine) bool {
	for _, k := range kept {
		if c == k.claim && jaccard(words, k.words) >= dupSimilarity {
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
