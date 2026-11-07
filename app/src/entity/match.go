package entity

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// token is one word of the text: a run of letters and digits, which may hold a
// full stop between two word characters ("Amazon.com", "3.5").
type token struct {
	start, end int
	sent       int // index of the sentence the token is in
}

// tokenize splits s into words. Apostrophes, hyphens, ampersands and other
// punctuation end a word, so "AAPL's" holds the word AAPL and "Coca-Cola"
// holds Coca and Cola. Entity names go through the same function, which makes
// the two sides comparable.
func tokenize(s string) []token {
	var toks []token
	for i := 0; i < len(s); {
		r, size := wordRuneAt(s, i)
		if !isWordRune(r) {
			i += size
			continue
		}
		start := i
		i += size
		for i < len(s) {
			r, size = wordRuneAt(s, i)
			if isWordRune(r) {
				i += size
				continue
			}
			if r == '.' && dotJoins(s, i+1) {
				i++
				continue
			}
			break
		}
		toks = append(toks, token{start: start, end: i})
	}
	return toks
}

func wordRuneAt(s string, i int) (rune, int) {
	if c := s[i]; c < utf8.RuneSelf {
		return rune(c), 1
	}
	return utf8.DecodeRuneInString(s[i:])
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// dotJoins reports whether a full stop just before s[i:] sits inside a word:
// the next character is a lower-case letter or a digit.
func dotJoins(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	r, _ := wordRuneAt(s, i)
	return unicode.IsLower(r) || unicode.IsDigit(r)
}

// scan holds the state of one Matches call.
type scan struct {
	ix   *Index
	text string
	toks []token
	// ctx says, per sentence, whether it holds a finance context word.
	ctx []bool
	// hard holds entities with a signal that no ordinary word can fake: a
	// cashtag, an exchange form or a multi-word name.
	hard map[int]bool
	// bare holds, per entity, the tokens that spell its ticker in a form
	// that could be one (2 to 5 upper-case letters, not on the stoplist).
	bare map[int][]int
	best map[int]*found
}

// found is the best evidence so far for one entity.
type found struct {
	match Match
	first int // earliest start of any accepted evidence
}

// Matches returns one Match per company found in text, each with its
// strongest evidence (the earliest one when several are equally strong). The
// list is ordered by the first appearance of the company in any form, so it
// agrees with Find; Start of a Match can therefore be later than that of the
// next one when a company is first named weakly and later by cashtag.
//
// A cashtag or an exchange-qualified ticker is always accepted. A name or
// alias must match case-sensitively as whole words; one that is a common word
// or shorter than four letters ("Apple", "Target", "GE") also needs a ticker,
// a cashtag or exchange form, a longer name, a corporate suffix after it, or a
// finance word in its sentence. A bare ticker must be two to five upper-case
// letters, off the stoplist, and share a sentence with a finance word.
func (ix *Index) Matches(text string) []Match {
	if text == "" || len(ix.entities) == 0 {
		return nil
	}
	toks := tokenize(text)
	if len(toks) == 0 {
		return nil
	}
	s := &scan{
		ix:   ix,
		text: text,
		toks: toks,
		hard: make(map[int]bool),
		bare: make(map[int][]int),
		best: make(map[int]*found),
	}
	s.assignSentences()
	s.scanTickers()
	s.scanNames()
	return s.result()
}

// assignSentences numbers the sentence of every token and marks the sentences
// that hold a finance context word.
func (s *scan) assignSentences() {
	starts := sentenceStarts(s.text)
	s.ctx = make([]bool, len(starts)+1)
	k := 0
	for i := range s.toks {
		for k < len(starts) && starts[k] <= s.toks[i].start {
			k++
		}
		s.toks[i].sent = k
		if s.isContext(i) {
			s.ctx[k] = true
		}
	}
}

// isContext reports whether token i is a finance word: shares, stock,
// earnings, CEO, 10-K and the like.
func (s *scan) isContext(i int) bool {
	t := s.toks[i]
	w := s.text[t.start:t.end]
	if _, ok := contextAbbreviations[w]; ok {
		return true
	}
	if (w == "K" || w == "Q") && (strings.HasSuffix(s.text[:t.start], "10-") || strings.HasSuffix(s.text[:t.start], "8-")) {
		return true
	}
	var buf [12]byte
	b, ok := appendLower(buf[:0], w)
	if !ok {
		return false
	}
	_, ok = contextWords[string(b)]
	return ok
}

// sentenceStarts returns the byte offsets where a new sentence begins. A
// sentence ends at ! ? and a line break, and at a full stop followed by
// white space, except after a corporate or title abbreviation (Inc., Mr.), after
// a single capital initial, and when the next word starts in lower case or
// with a digit ("Apple Inc. shares rose").
func sentenceStarts(s string) []int {
	var out []int
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n', '\r', '!', '?':
			out = append(out, i+1)
		case '.':
			if endsSentence(s, i) {
				out = append(out, i+1)
			}
		}
	}
	return out
}

func endsSentence(s string, dot int) bool {
	j := dot + 1
	for j < len(s) {
		r, size := wordRuneAt(s, j)
		if !strings.ContainsRune("\"')]”’", r) {
			break
		}
		j += size
	}
	if j >= len(s) {
		return true
	}
	if r, _ := wordRuneAt(s, j); !unicode.IsSpace(r) {
		return false
	}
	for j < len(s) {
		r, size := wordRuneAt(s, j)
		if !unicode.IsSpace(r) {
			if unicode.IsLower(r) || unicode.IsDigit(r) {
				return false
			}
			break
		}
		j += size
	}
	w := dot
	for w > 0 && isASCIILetter(s[w-1]) {
		w--
	}
	word := s[w:dot]
	if len(word) == 1 && word[0] >= 'A' && word[0] <= 'Z' {
		return false
	}
	var buf [8]byte
	if b, ok := appendLower(buf[:0], word); ok && len(word) <= len(buf) {
		if _, abbr := sentenceAbbreviations[string(b)]; abbr {
			return false
		}
	}
	return true
}

// scanTickers finds cashtags, exchange-qualified tickers and bare tickers.
func (s *scan) scanTickers() {
	for i, t := range s.toks {
		w := s.text[t.start:t.end]
		if t.start > 0 && s.text[t.start-1] == '$' {
			if !wordBefore(s.text, t.start-1) {
				if ent, end, ok := s.ix.tickerAt(s.text, t.start); ok && !s.ix.collides(ent) {
					s.hard[ent] = true
					s.record(ent, EvidenceCashtag, t.start-1, end)
				}
			}
			continue
		}
		if n := s.exchangeTokens(i); n > 0 {
			pos := skipSpaces(s.text, s.toks[i+n-1].end)
			if pos < len(s.text) && s.text[pos] == ':' {
				pos = skipSpaces(s.text, pos+1)
				venue := venueOfPrefix(s.text[t.start:s.toks[i+n-1].end])
				if ent, end, ok := s.ix.tickerAt(s.text, pos); ok && s.ix.venueFits(ent, venue) {
					s.hard[ent] = true
					s.record(ent, EvidenceExchange, t.start, end)
				}
			}
		}
		if len(w) < 2 || len(w) > 5 || !allUpper(w) {
			continue
		}
		ent, ok := s.ix.byTicker[w]
		if !ok || s.ix.collides(ent) {
			continue
		}
		if _, stop := tickerStoplist[w]; stop {
			continue
		}
		s.bare[ent] = append(s.bare[ent], i)
		if s.ctx[t.sent] {
			s.record(ent, EvidenceTicker, t.start, t.end)
		}
	}
}

// exchangeTokens returns how many tokens from i on spell an exchange name
// (0, 1 or 2). The name must start with an upper-case letter.
func (s *scan) exchangeTokens(i int) int {
	t := s.toks[i]
	w := s.text[t.start:t.end]
	if len(w) > 12 || w[0] < 'A' || w[0] > 'Z' {
		return 0
	}
	var buf [24]byte
	b, ok := appendLower(buf[:0], w)
	if !ok {
		return 0
	}
	if i+1 < len(s.toks) && s.toks[i+1].start == t.end+1 && s.text[t.end] == ' ' {
		n := s.toks[i+1]
		if w2 := s.text[n.start:n.end]; len(w2) <= 8 {
			if b2, ok := appendLower(append(b, ' '), w2); ok {
				if _, hit := exchanges[string(b2)]; hit {
					return 2
				}
			}
		}
	}
	if _, hit := exchanges[string(b)]; hit {
		return 1
	}
	return 0
}

// tickerAt reads an upper-case ticker at text[pos:], optionally with a class
// suffix such as .B or -B, and returns the entity and the end offset. The
// ticker must end at a word boundary.
func (ix *Index) tickerAt(text string, pos int) (ent, end int, ok bool) {
	i := pos
	for i < len(text) && i-pos < 6 && (isUpperByte(text[i]) || isDigitByte(text[i])) {
		i++
	}
	if i == pos || !isUpperByte(text[pos]) {
		return 0, 0, false
	}
	base := i
	if i < len(text) && (text[i] == '.' || text[i] == '-') {
		j := i + 1
		for j < len(text) && j-i-1 < 2 && isUpperByte(text[j]) {
			j++
		}
		if j > i+1 && !wordAt(text, j) {
			cand := text[pos:j]
			if ent, ok := ix.byTicker[cand]; ok {
				return ent, j, true
			}
			alt := cand[:base-pos] + otherSeparator(text[i]) + cand[base-pos+1:]
			if ent, ok := ix.byTicker[alt]; ok {
				return ent, j, true
			}
		}
	}
	if wordAt(text, base) {
		return 0, 0, false
	}
	ent, ok = ix.byTicker[text[pos:base]]
	return ent, base, ok
}

func otherSeparator(c byte) string {
	if c == '.' {
		return "-"
	}
	return "."
}

// candidate is a name or alias found in the text, before the ambiguity check.
type candidate struct {
	p           *phrase
	first, last int // token indexes
}

// scanNames finds names and aliases, keeps the longest phrase at each
// position, and accepts the ambiguous ones that have a second signal.
func (s *scan) scanNames() {
	var cands []candidate
	for i, t := range s.toks {
		list := s.ix.names[s.text[t.start:t.end]]
		longest := 0
		for k := range list {
			p := &list[k]
			if longest != 0 && len(p.words) < longest {
				break
			}
			last, ok := s.matchPhrase(i, p.words)
			if !ok {
				continue
			}
			longest = len(p.words)
			cands = append(cands, candidate{p: p, first: i, last: last})
			if !p.short && !p.ambiguous {
				s.hard[p.ent] = true
			}
		}
	}
	for _, c := range cands {
		p := c.p
		ok := !p.ambiguous || s.hard[p.ent] || s.tickerOutside(p.ent, c.first, c.last)
		if !ok && !p.hardOnly {
			ok = s.ctx[s.toks[c.first].sent] || s.suffixFollows(c.last)
		}
		if ok {
			s.record(p.ent, EvidenceName, s.toks[c.first].start, s.toks[c.last].end)
		}
	}
}

// tickerOutside reports whether the text spells the ticker of ent somewhere
// other than in tokens first to last, so that an alias equal to the ticker
// does not vouch for itself.
func (s *scan) tickerOutside(ent, first, last int) bool {
	for _, i := range s.bare[ent] {
		if i < first || i > last {
			return true
		}
	}
	return false
}

// matchPhrase checks that the tokens from i on spell words exactly, with only
// light punctuation between them, and returns the index of the last token.
func (s *scan) matchPhrase(i int, words []string) (int, bool) {
	for k, w := range words {
		j := i + k
		if j >= len(s.toks) {
			return 0, false
		}
		t := s.toks[j]
		if s.text[t.start:t.end] != w {
			return 0, false
		}
		if k > 0 && !wordGap(s.text[s.toks[j-1].end:t.start], false) {
			return 0, false
		}
	}
	return i + len(words) - 1, true
}

// suffixFollows reports whether the token after last is a capitalised
// corporate suffix ("Apple Inc", "Block, Inc.").
func (s *scan) suffixFollows(last int) bool {
	if last+1 >= len(s.toks) {
		return false
	}
	n := s.toks[last+1]
	if !wordGap(s.text[s.toks[last].end:n.start], true) {
		return false
	}
	w := s.text[n.start:n.end]
	if r, _ := wordRuneAt(w, 0); !unicode.IsUpper(r) {
		return false
	}
	_, ok := corporateSuffixes[strings.ToLower(w)]
	return ok
}

// wordGap reports whether gap holds only the light punctuation that may sit
// inside a name: spaces, &, hyphens, apostrophes, slashes and dashes, and a
// comma when allowComma is set.
func wordGap(gap string, allowComma bool) bool {
	if gap == "" {
		return false
	}
	for _, r := range gap {
		switch r {
		case ' ', '\t', '&', '-', '\'', '/', '’', '–':
		case ',':
			if !allowComma {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// record keeps the strongest, then earliest, evidence per entity.
func (s *scan) record(ent int, ev Evidence, start, end int) {
	f := s.best[ent]
	if f == nil {
		s.best[ent] = &found{
			match: Match{Entity: s.ix.entities[ent], Evidence: ev, Start: start, End: end},
			first: start,
		}
		return
	}
	if start < f.first {
		f.first = start
	}
	if ev > f.match.Evidence || (ev == f.match.Evidence && start < f.match.Start) {
		f.match.Evidence, f.match.Start, f.match.End = ev, start, end
	}
}

func (s *scan) result() []Match {
	if len(s.best) == 0 {
		return nil
	}
	list := make([]*found, 0, len(s.best))
	for _, f := range s.best {
		list = append(list, f)
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].first != list[b].first {
			return list[a].first < list[b].first
		}
		return list[a].match.Entity.Ticker < list[b].match.Entity.Ticker
	})
	out := make([]Match, len(list))
	for i, f := range list {
		out[i] = f.match
	}
	return out
}

// appendLower appends the ASCII lower-case form of s to dst. It reports false
// if s holds a non-ASCII byte.
func appendLower(dst []byte, s string) ([]byte, bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			return dst, false
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst, true
}

func skipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// wordAt reports whether a letter or digit starts at s[i].
func wordAt(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	r, _ := wordRuneAt(s, i)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordBefore reports whether a letter or digit ends just before s[i].
func wordBefore(s string, i int) bool {
	if i <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func allUpper(w string) bool {
	for i := 0; i < len(w); i++ {
		if !isUpperByte(w[i]) {
			return false
		}
	}
	return true
}

func isUpperByte(c byte) bool   { return c >= 'A' && c <= 'Z' }
func isDigitByte(c byte) bool   { return c >= '0' && c <= '9' }
func isASCIILetter(c byte) bool { return isUpperByte(c) || (c >= 'a' && c <= 'z') }
