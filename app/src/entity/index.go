package entity

import (
	"sort"
	"strings"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Evidence says how a company was found in a text. Larger values are stronger
// evidence, so the values can be compared directly.
type Evidence int

// The kinds of evidence, from weakest to strongest.
const (
	// EvidenceTicker is a bare upper-case ticker such as AAPL in a sentence
	// that also has a finance word.
	EvidenceTicker Evidence = iota + 1
	// EvidenceName is the company name or one of its aliases.
	EvidenceName
	// EvidenceExchange is an exchange-qualified ticker such as
	// (NASDAQ: AAPL) or NYSE:KO.
	EvidenceExchange
	// EvidenceCashtag is a ticker with a dollar sign, such as $AAPL.
	EvidenceCashtag
)

// String returns the lower-case name of the evidence.
func (e Evidence) String() string {
	switch e {
	case EvidenceTicker:
		return "ticker"
	case EvidenceName:
		return "name"
	case EvidenceExchange:
		return "exchange"
	case EvidenceCashtag:
		return "cashtag"
	}
	return "none"
}

// Match is one company found in a text.
type Match struct {
	// Entity is the company that was found.
	Entity model.Entity
	// Evidence is the strongest evidence found for the company.
	Evidence Evidence
	// Start and End are the byte offsets of that evidence in the text, so
	// text[Start:End] is the cashtag, exchange form, name or ticker.
	Start, End int
}

// phrase is one way the text may name an entity: the normalised name, an
// alias, or the first word of a multi-word name.
type phrase struct {
	ent   int
	words []string
	// short marks the first word of a multi-word name used on its own.
	short bool
	// ambiguous marks a phrase that needs a second signal in the text.
	ambiguous bool
	// hardOnly marks an ambiguous short form that finance context cannot
	// back up, only a ticker, cashtag, exchange form or fuller name.
	hardOnly bool
}

// Index finds companies in text. Build it once with New; it is read-only
// afterwards and safe for concurrent use. It implements Resolver.
type Index struct {
	entities []model.Entity
	byTicker map[string]int
	// names maps the first word of a phrase, in its original spelling, to the
	// phrases that start with it, longest first.
	names map[string][]phrase
}

var _ Resolver = (*Index)(nil)

// New builds an index over entities. Entities with an empty ticker are
// ignored, and when two entities share a ticker the first one is kept.
func New(entities []model.Entity) *Index {
	ix := &Index{
		entities: make([]model.Entity, 0, len(entities)),
		byTicker: make(map[string]int, len(entities)),
		names:    make(map[string][]phrase),
	}
	for _, e := range entities {
		key := strings.ToUpper(strings.TrimSpace(e.Ticker))
		if key == "" {
			continue
		}
		if _, dup := ix.byTicker[key]; dup {
			continue
		}
		id := len(ix.entities)
		ix.entities = append(ix.entities, e)
		ix.byTicker[key] = id
		ix.addPhrases(id, e)
	}
	for _, list := range ix.names {
		sort.SliceStable(list, func(a, b int) bool { return len(list[a].words) > len(list[b].words) })
	}
	return ix
}

func (ix *Index) addPhrases(id int, e model.Entity) {
	seen := make(map[string]struct{})
	add := func(p phrase) {
		key := strings.Join(p.words, "\x00")
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		ix.names[p.words[0]] = append(ix.names[p.words[0]], p)
	}
	addName := func(name string) []string {
		words := normalisedWords(name)
		if len(words) == 0 {
			return nil
		}
		add(phrase{ent: id, words: words, ambiguous: len(words) == 1 && ambiguousWord(words[0])})
		return words
	}
	words := addName(e.Name)
	for _, a := range e.Aliases {
		addName(a)
	}
	// The first word alone is added last so an alias spelled the same way
	// wins over it.
	if len(words) > 1 && letterCount(words[0]) >= 4 {
		amb := ambiguousWord(words[0])
		add(phrase{ent: id, words: words[:1], short: true, ambiguous: amb, hardOnly: amb})
	}
}

// Lookup returns the entity with the given ticker, ignoring case.
func (ix *Index) Lookup(ticker string) (model.Entity, bool) {
	id, ok := ix.byTicker[strings.ToUpper(strings.TrimSpace(ticker))]
	if !ok {
		return model.Entity{}, false
	}
	return ix.entities[id], true
}

// Find returns the distinct entities mentioned in text, in order of first
// appearance. It applies the same rules as Matches.
func (ix *Index) Find(text string) []model.Entity {
	matches := ix.Matches(text)
	if len(matches) == 0 {
		return nil
	}
	out := make([]model.Entity, len(matches))
	for i, m := range matches {
		out[i] = m.Entity
	}
	return out
}

// dottedSuffixes holds corporate suffixes written with full stops, which the
// tokenizer would otherwise leave glued to each other ("S.p.A.").
var dottedSuffixes = []string{
	"s.p.a", "s.a", "n.v", "a.g", "s.e", "l.l.c", "p.l.c", "l.p", "a.b", "b.v",
	"a.s.a", "l.t.d", "i.n.c", "c.o",
}

// normalisedWords splits a company name into words, drops a leading "The" and
// drops trailing corporate suffixes (Inc, Corp, Ltd, plc, S.A., Holdings,
// Group...). A name made only of suffixes keeps its last word.
func normalisedWords(name string) []string {
	name = stripDottedSuffixes(name)
	toks := tokenize(name)
	words := make([]string, len(toks))
	for i, t := range toks {
		words[i] = name[t.start:t.end]
	}
	if len(words) > 1 && words[0] == "The" {
		words = words[1:]
	}
	for len(words) > 1 {
		if _, ok := corporateSuffixes[strings.ToLower(words[len(words)-1])]; !ok {
			break
		}
		words = words[:len(words)-1]
	}
	return words
}

// stripDottedSuffixes removes trailing suffixes such as "S.A." and "S.p.A."
// from name, together with the separator before them.
func stripDottedSuffixes(name string) string {
	for {
		trimmed := strings.TrimRight(name, " ,.")
		cut := false
		for _, suf := range dottedSuffixes {
			n := len(trimmed) - len(suf)
			if n < 1 || !strings.EqualFold(trimmed[n:], suf) || (trimmed[n-1] != ' ' && trimmed[n-1] != ',') {
				continue
			}
			name, cut = trimmed[:n], true
			break
		}
		if !cut {
			return name
		}
	}
}

// ambiguousWord reports whether a single name word cannot identify a company
// on its own: it is on the ambiguous list or shorter than four letters.
func ambiguousWord(w string) bool {
	if letterCount(w) < 4 {
		return true
	}
	_, ok := ambiguousWords[strings.ToLower(w)]
	return ok
}

func letterCount(w string) int {
	n := 0
	for _, r := range w {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}
