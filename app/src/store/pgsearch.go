package store

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ftsToTSQuery turns a full-text expression of the filter package into a
// Postgres tsquery. The expression is the subset of SQLite's FTS5 syntax that
// filter.Query.FTS emits and that the store's own callers use: words and
// "quoted phrases" (adjacent words), a trailing * for a prefix, AND (also
// written as a space), OR, NOT and parentheses. Anything else (a column filter
// such as tickers:AAPL, NEAR, a leading minus) is refused with an error rather
// than guessed at.
//
// The expression is a prefilter: the reader checks every row it returns, so
// what matters here is that nothing a row needs is left out. Words are split
// the way the index splits text (letters and digits make a word, the rest
// separates), see the generated column in the Postgres baseline.
func ftsToTSQuery(expr string) (string, error) {
	p := &ftsParser{src: []rune(expr)}
	out, err := p.or()
	if err != nil {
		return "", err
	}
	p.space()
	if p.i < len(p.src) {
		return "", fmt.Errorf("full-text expression: unexpected %q", string(p.src[p.i]))
	}
	return out, nil
}

type ftsParser struct {
	src []rune
	i   int
}

func (p *ftsParser) space() {
	for p.i < len(p.src) && unicode.IsSpace(p.src[p.i]) {
		p.i++
	}
}

// word reports whether the keyword (upper case) is next, as a whole word.
func (p *ftsParser) keyword(k string) bool {
	p.space()
	n := len(k)
	if p.i+n > len(p.src) || !strings.EqualFold(string(p.src[p.i:p.i+n]), k) {
		return false
	}
	if p.i+n < len(p.src) && isWordRune(p.src[p.i+n]) {
		return false
	}
	p.i += n
	return true
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func (p *ftsParser) or() (string, error) {
	left, err := p.and()
	if err != nil {
		return "", err
	}
	for p.keyword("OR") {
		right, err := p.and()
		if err != nil {
			return "", err
		}
		left = "(" + left + " | " + right + ")"
	}
	return left, nil
}

func (p *ftsParser) and() (string, error) {
	left, err := p.not()
	if err != nil {
		return "", err
	}
	for {
		p.space()
		if p.i >= len(p.src) || p.src[p.i] == ')' || p.peekKeyword("OR") {
			return left, nil
		}
		p.keyword("AND")
		right, err := p.not()
		if err != nil {
			return "", err
		}
		left = "(" + left + " & " + right + ")"
	}
}

func (p *ftsParser) peekKeyword(k string) bool {
	save := p.i
	ok := p.keyword(k)
	p.i = save
	return ok
}

func (p *ftsParser) not() (string, error) {
	left, err := p.primary()
	if err != nil {
		return "", err
	}
	for p.keyword("NOT") {
		right, err := p.primary()
		if err != nil {
			return "", err
		}
		left = "(" + left + " & !" + right + ")"
	}
	return left, nil
}

func (p *ftsParser) primary() (string, error) {
	p.space()
	if p.i >= len(p.src) {
		return "", errors.New("full-text expression: ends where a term is expected")
	}
	switch c := p.src[p.i]; {
	case c == '(':
		p.i++
		out, err := p.or()
		if err != nil {
			return "", err
		}
		p.space()
		if p.i >= len(p.src) || p.src[p.i] != ')' {
			return "", errors.New("full-text expression: missing )")
		}
		p.i++
		return out, nil
	case c == '"':
		p.i++
		var text []rune
		for {
			if p.i >= len(p.src) {
				return "", errors.New("full-text expression: missing closing quote")
			}
			r := p.src[p.i]
			p.i++
			if r == '"' {
				if p.i < len(p.src) && p.src[p.i] == '"' { // a doubled quote
					text = append(text, '"')
					p.i++
					continue
				}
				break
			}
			text = append(text, r)
		}
		return p.terms(string(text))
	case isWordRune(c):
		start := p.i
		for p.i < len(p.src) && isWordRune(p.src[p.i]) {
			p.i++
		}
		if p.i < len(p.src) && p.src[p.i] == ':' {
			return "", fmt.Errorf("full-text expression: a column filter (%s:) is not supported", string(p.src[start:p.i]))
		}
		return p.terms(string(p.src[start:p.i]))
	default:
		return "", fmt.Errorf("full-text expression: unexpected %q", string(c))
	}
}

// terms makes the tsquery of a run of text, with a prefix marker (*) right
// after it applying to the last word.
func (p *ftsParser) terms(text string) (string, error) {
	prefix := false
	if p.i < len(p.src) && p.src[p.i] == '*' {
		prefix = true
		p.i++
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !isWordRune(r) })
	if len(words) == 0 {
		return "", errors.New("full-text expression: a term without a word")
	}
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = "'" + w + "'"
		if prefix && i == len(words)-1 {
			parts[i] += ":*"
		}
	}
	return "(" + strings.Join(parts, " <-> ") + ")", nil
}
