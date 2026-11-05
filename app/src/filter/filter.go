// Package filter implements the Google-style search over fetched news:
//
//	"free cash flow" -rumor ticker:AAPL site:sec.gov after:2026-11-01 (dividend OR buyback)
//
// # Grammar
//
// An expression is a list of terms separated by spaces. A space means AND, so
// every term must match. An empty or all-space expression matches everything.
//
// Text terms are matched against the article's Title and Summary joined by one
// space. Text is lower-cased and cut into words. A word is a run of letters,
// digits and the combining marks that belong to them (any script, so "é" and
// "日本語" are words); everything else separates words. The same rule is applied
// to the query, so matching is case-insensitive and ignores punctuation.
//
//	dividend          whole-word match: "ai" does not match inside "said"
//	free-cash         the phrase "free cash": the words in order and adjacent
//	"free cash flow"  a quoted phrase, the same
//	dividen*          prefix match: a trailing * lets the last word continue
//
// A phrase may span the boundary between Title and Summary, because the two are
// joined into one text. A term with no letters or digits, such as "-" or "*", is
// a syntax error.
//
// Negation puts a minus directly in front, with no space. It works on terms,
// phrases, operators and parenthesised groups:
//
//	-rumor   -"price target"   -ticker:TSLA   -(rumor OR leak)
//
// OR is the uppercase word OR or the bar |, written between two terms. OR binds
// tighter than the implicit AND, as in Google, so
//
//	a b OR c       means  a AND (b OR c)
//	a OR b c       means  (a OR b) AND c
//	a OR b OR c    means  a OR b OR c
//
// The lower-case word "or" is an ordinary search word. Parentheses group:
//
//	(a OR b) c     means  (a OR b) AND c
//	(a b) OR c     means  (a AND b) OR c
//
// Operators have the form name:value. Names are case-insensitive. The value is
// the text after the colon up to the next space, parenthesis, bar or quote; put
// it in double quotes to include those characters. Text values are compared
// without case. The operators are:
//
//	ticker:AAPL         one of Article.Tickers
//	site:sec.gov        Article.Domain is sec.gov or a subdomain of it; a
//	                    trailing dot is ignored
//	topic:ai-infra      one of Article.Topics
//	kind:buyback        Score.Kind; the value must be a model.EventKind
//	group:capital       Score.Kind.Group(); the value must be a model.EventGroup
//	sector:technology   Score.Sector
//	form:8-K            Article.Form
//	item:2.02           one of Article.Items
//	after:2026-11-01    published on or after the start of that day
//	before:2026-11-04   published before the start of that day
//	materiality:>=4     Score.Materiality compared with a number
//	relevance:>=0.7     Score.Relevance compared with a number
//
// The comparators are >=, >, <=, < and =; a bare number means =. Numbers are
// plain decimals such as 4 or 0.7. Days are read in a time zone: Match uses UTC
// and MatchIn takes the zone, so after:2026-11-01 in Europe/Berlin starts at
// 2026-10-31 23:00 UTC. An unknown operator name is a syntax error whose message
// lists the known operators.
//
// # Limits
//
// An expression may be at most 1000 bytes, nest parentheses at most 20 deep and
// hold at most 100 terms (every text term, phrase and operator counts; AND, OR
// and parentheses do not). A longer, deeper or larger expression is a syntax
// error.
//
// # Errors
//
// Parse returns a *SyntaxError. Pos is a byte offset into the expression: the
// start of the offending term, operator name, parenthesis or OR, or the start of
// an operator's value when the value is the problem. A limit on length reports
// the first byte past the limit.
//
// # Full-text prefilter
//
// Query.FTS gives a SQLite FTS5 MATCH expression that narrows the rows a store
// must load before Match runs. It assumes the index holds Title and Summary
// joined by a space in a single column, and uses the default unicode61
// tokenizer. It never excludes an article that Match accepts.
package filter

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Limits on what Parse accepts.
const (
	MaxLen   = 1000 // longest expression in bytes
	MaxDepth = 20   // deepest nesting of parentheses
	MaxTerms = 100  // most text terms and operators in one expression
)

// SyntaxError is a problem in a search expression.
type SyntaxError struct {
	Pos int    // byte offset into the expression
	Msg string // what is wrong
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("filter: syntax error at byte %d: %s", e.Pos, e.Msg)
}

// Query is a parsed search expression. The zero Query matches everything.
// Queries are immutable and safe for concurrent use. Two Queries parsed from
// expressions with the same meaning compare equal with reflect.DeepEqual.
type Query struct {
	root node
}

// Parse reads a search expression. The error, if any, is a *SyntaxError.
func Parse(expr string) (Query, error) { return ParseWith(expr, Env{}) }

// ParseWith is Parse with the extra operators of env.
func ParseWith(expr string, env Env) (Query, error) {
	if len(expr) > MaxLen {
		return Query{}, &SyntaxError{Pos: MaxLen, Msg: fmt.Sprintf("expression longer than %d bytes", MaxLen)}
	}
	if !utf8.ValidString(expr) {
		pos := 0
		for pos < len(expr) {
			r, n := utf8.DecodeRuneInString(expr[pos:])
			if r == utf8.RuneError && n == 1 {
				break
			}
			pos += n
		}
		return Query{}, &SyntaxError{Pos: pos, Msg: "invalid UTF-8"}
	}
	p := &parser{s: expr, env: env}
	n, err := p.parseAnd(0)
	if err != nil {
		return Query{}, err
	}
	if p.i < len(p.s) {
		return Query{}, &SyntaxError{Pos: p.i, Msg: "unmatched )"}
	}
	return Query{root: n}, nil
}

// Match reports whether the article satisfies the query. Days in after: and
// before: are read in UTC.
func (q Query) Match(a model.Article) bool { return q.MatchIn(a, time.UTC) }

// MatchIn is Match with after: and before: days read in loc. A nil loc means
// UTC.
func (q Query) MatchIn(a model.Article, loc *time.Location) bool {
	if q.root == nil {
		return true
	}
	if loc == nil {
		loc = time.UTC
	}
	return q.root.matches(&matchCtx{a: &a, loc: loc})
}

// String returns the canonical form of the query. Parsing it gives back an
// equal Query. Words are lower-cased, text operator values are lower-cased,
// and redundant parentheses are dropped. The canonical form can be a few bytes
// longer than the expression it came from (a|b becomes a OR b), so one that
// began near MaxLen may itself be too long for Parse.
func (q Query) String() string {
	if q.root == nil {
		return ""
	}
	return render(q.root, ctxTop)
}

// FTS returns a SQLite FTS5 MATCH expression for a prefilter: every article
// that Match accepts satisfies it, and some that Match rejects may too. Only
// positive text terms and the AND and OR structure are used. Operators and
// negations say nothing the index can check, so they are left out; ok is false
// when nothing is left, and the caller must then scan without a prefilter.
// A branch of an OR with nothing usable makes the whole OR unusable, because
// dropping one branch would narrow the OR and could lose articles.
//
// Every token is double-quoted with embedded quotes doubled, so no FTS5 syntax
// can come from the search text. A prefix term becomes "dividen"*.
func (q Query) FTS() (match string, ok bool) {
	if q.root == nil {
		return "", false
	}
	p, ok := q.root.fts()
	return p.s, ok
}

// ---- syntax tree ----

type node interface {
	matches(c *matchCtx) bool
	// fts returns the prefilter expression and whether there is one.
	fts() (ftsPart, bool)
}

type matchCtx struct {
	a     *model.Article
	loc   *time.Location
	words []string
	split bool
}

func (c *matchCtx) text() []string {
	if !c.split {
		c.words = splitWords(c.a.Title + " " + c.a.Summary)
		c.split = true
	}
	return c.words
}

type andNode struct{ kids []node }
type orNode struct{ kids []node }
type notNode struct{ kid node }

// textNode is a run of words that must appear in order and adjacent. With
// prefix set the last word only has to start the matching word.
type textNode struct {
	words  []string
	prefix bool
}

// strOp is an operator whose value is compared as text.
type strOp struct{ name, val string }

type cmpKind int

const (
	cmpEQ cmpKind = iota
	cmpGE
	cmpGT
	cmpLE
	cmpLT
)

var cmpText = [...]string{cmpEQ: "=", cmpGE: ">=", cmpGT: ">", cmpLE: "<=", cmpLT: "<"}

// numOp is materiality or relevance compared with a number.
type numOp struct {
	name string
	cmp  cmpKind
	num  float64
}

// dateOp is after or before with a calendar day.
type dateOp struct {
	name    string
	y, m, d int
}

func mkAnd(kids []node) node {
	var flat []node
	for _, k := range kids {
		if a, ok := k.(andNode); ok {
			flat = append(flat, a.kids...)
		} else {
			flat = append(flat, k)
		}
	}
	switch len(flat) {
	case 0:
		return nil
	case 1:
		return flat[0]
	}
	return andNode{flat}
}

func mkOr(kids []node) node {
	var flat []node
	for _, k := range kids {
		if o, ok := k.(orNode); ok {
			flat = append(flat, o.kids...)
		} else {
			flat = append(flat, k)
		}
	}
	if len(flat) == 1 {
		return flat[0]
	}
	return orNode{flat}
}

// ---- matching ----

func (n andNode) matches(c *matchCtx) bool {
	for _, k := range n.kids {
		if !k.matches(c) {
			return false
		}
	}
	return true
}

func (n orNode) matches(c *matchCtx) bool {
	for _, k := range n.kids {
		if k.matches(c) {
			return true
		}
	}
	return false
}

func (n notNode) matches(c *matchCtx) bool { return !n.kid.matches(c) }

func (n textNode) matches(c *matchCtx) bool {
	text := c.text()
	last := len(n.words) - 1
	for i := 0; i+last < len(text); i++ {
		j := 0
		for ; j < last; j++ {
			if text[i+j] != n.words[j] {
				break
			}
		}
		if j < last {
			continue
		}
		if w := text[i+last]; w == n.words[last] || (n.prefix && strings.HasPrefix(w, n.words[last])) {
			return true
		}
	}
	return false
}

func (n strOp) matches(c *matchCtx) bool {
	a := c.a
	switch n.name {
	case "ticker":
		return anyFold(a.Tickers, n.val)
	case "topic":
		return anyFold(a.Topics, n.val)
	case "item":
		return anyFold(a.Items, n.val)
	case "sector":
		return strings.EqualFold(a.Score.Sector, n.val)
	case "form":
		return strings.EqualFold(a.Form, n.val)
	case "kind":
		return a.Score.Kind == model.EventKind(n.val)
	case "group":
		return a.Score.Kind.Group() == model.EventGroup(n.val)
	case "site":
		d := strings.ToLower(strings.TrimRight(a.Domain, "."))
		return d == n.val || strings.HasSuffix(d, "."+n.val)
	}
	return false
}

func anyFold(list []string, v string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, v) })
}

func (n numOp) matches(c *matchCtx) bool {
	var x float64
	if n.name == "materiality" {
		x = float64(c.a.Score.Materiality)
	} else {
		x = c.a.Score.Relevance
	}
	switch n.cmp {
	case cmpGE:
		return x >= n.num
	case cmpGT:
		return x > n.num
	case cmpLE:
		return x <= n.num
	case cmpLT:
		return x < n.num
	}
	return x == n.num
}

func (n dateOp) matches(c *matchCtx) bool {
	start := time.Date(n.y, time.Month(n.m), n.d, 0, 0, 0, 0, c.loc)
	if n.name == "after" {
		return !c.a.Published.Before(start)
	}
	return c.a.Published.Before(start)
}

// ---- full-text prefilter ----

// ftsPart is an FTS5 expression. atom is true for a single quoted token, which
// needs no parentheses when it is combined with others.
type ftsPart struct {
	s    string
	atom bool
}

// combine joins parts with an FTS5 operator, parenthesising composite parts.
func combine(parts []ftsPart, op string) (ftsPart, bool) {
	switch len(parts) {
	case 0:
		return ftsPart{}, false
	case 1:
		return parts[0], true
	}
	strs := make([]string, len(parts))
	for i, p := range parts {
		strs[i] = p.s
		if !p.atom {
			strs[i] = "(" + p.s + ")"
		}
	}
	return ftsPart{s: strings.Join(strs, " "+op+" ")}, true
}

// fts of an AND drops the children with nothing usable: that only widens it.
func (n andNode) fts() (ftsPart, bool) {
	var parts []ftsPart
	for _, k := range n.kids {
		if p, ok := k.fts(); ok {
			parts = append(parts, p)
		}
	}
	return combine(parts, "AND")
}

// fts of an OR is unusable as soon as one branch is: dropping a branch would
// narrow it.
func (n orNode) fts() (ftsPart, bool) {
	parts := make([]ftsPart, 0, len(n.kids))
	for _, k := range n.kids {
		p, ok := k.fts()
		if !ok {
			return ftsPart{}, false
		}
		parts = append(parts, p)
	}
	return combine(parts, "OR")
}

func (n notNode) fts() (ftsPart, bool) { return ftsPart{}, false }
func (n strOp) fts() (ftsPart, bool)   { return ftsPart{}, false }
func (n setOp) fts() (ftsPart, bool)   { return ftsPart{}, false }
func (n numOp) fts() (ftsPart, bool)   { return ftsPart{}, false }
func (n dateOp) fts() (ftsPart, bool)  { return ftsPart{}, false }

func (n textNode) fts() (ftsPart, bool) {
	// A word made only of combining marks may produce no token in the index.
	for _, w := range n.words {
		if !strings.ContainsFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			return ftsPart{}, false
		}
	}
	s := ftsQuote(strings.Join(n.words, " "))
	if n.prefix {
		s += "*"
	}
	return ftsPart{s: s, atom: true}, true
}

func ftsQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// ---- canonical text ----

const (
	ctxTop = iota // at the top or inside an AND: OR needs no parentheses
	ctxOr         // inside an OR: AND needs parentheses
)

func render(n node, ctx int) string {
	switch n := n.(type) {
	case andNode:
		parts := make([]string, len(n.kids))
		for i, k := range n.kids {
			parts[i] = render(k, ctxTop)
		}
		s := strings.Join(parts, " ")
		if ctx == ctxOr {
			return "(" + s + ")"
		}
		return s
	case orNode:
		parts := make([]string, len(n.kids))
		for i, k := range n.kids {
			parts[i] = render(k, ctxOr)
		}
		return strings.Join(parts, " OR ")
	case notNode:
		switch n.kid.(type) {
		case andNode, orNode, notNode:
			return "-(" + render(n.kid, ctxTop) + ")"
		}
		return "-" + render(n.kid, ctxTop)
	case textNode:
		switch {
		case n.prefix:
			return strings.Join(n.words, "-") + "*"
		case len(n.words) == 1:
			return n.words[0]
		}
		return `"` + strings.Join(n.words, " ") + `"`
	case strOp:
		return n.name + ":" + quoteValue(n.val)
	case setOp:
		return n.name + ":" + quoteValue(n.val)
	case numOp:
		op := cmpText[n.cmp]
		if n.cmp == cmpEQ {
			op = ""
		}
		return n.name + ":" + op + strconv.FormatFloat(n.num, 'f', -1, 64)
	case dateOp:
		return fmt.Sprintf("%s:%04d-%02d-%02d", n.name, n.y, n.m, n.d)
	}
	return ""
}

// quoteValue writes an operator value bare when it can be read back that way.
func quoteValue(v string) string {
	if !strings.ContainsFunc(v, isDelim) {
		return v
	}
	return `"` + v + `"`
}

// ---- words ----

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || unicode.Is(unicode.Co, r)
}

func splitWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !isWordRune(r) })
}

// isDelim reports whether r ends an unquoted token.
func isDelim(r rune) bool {
	return unicode.IsSpace(r) || r == '(' || r == ')' || r == '"' || r == '|'
}

// ---- parser ----

// operatorNames lists the operators, in the order the error message gives them.
var operatorNames = []string{
	"after", "before", "form", "group", "item", "kind",
	"materiality", "relevance", "sector", "site", "ticker", "topic",
}

type parser struct {
	s     string
	i     int
	terms int
	env   Env
}

func (p *parser) errAt(pos int, format string, args ...any) error {
	return &SyntaxError{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) skipSpace() {
	for p.i < len(p.s) {
		r, n := utf8.DecodeRuneInString(p.s[p.i:])
		if !unicode.IsSpace(r) {
			return
		}
		p.i += n
	}
}

// scanWord returns the end of the unquoted token starting at p.i.
func (p *parser) scanWord() int {
	j := p.i
	for j < len(p.s) {
		r, n := utf8.DecodeRuneInString(p.s[j:])
		if isDelim(r) {
			break
		}
		j += n
	}
	return j
}

// atOr reports whether the next token is the OR keyword or a bar.
func (p *parser) atOr() bool {
	if p.i >= len(p.s) {
		return false
	}
	if p.s[p.i] == '|' {
		return true
	}
	return p.s[p.i] == 'O' && p.s[p.i:p.scanWord()] == "OR"
}

// parseAnd reads terms until a closing parenthesis or the end. It returns nil
// for no terms.
func (p *parser) parseAnd(depth int) (node, error) {
	var kids []node
	for {
		p.skipSpace()
		if p.i >= len(p.s) || p.s[p.i] == ')' {
			return mkAnd(kids), nil
		}
		n, err := p.parseOr(depth)
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
	}
}

// parseOr reads one term, then any OR terms that follow it.
func (p *parser) parseOr(depth int) (node, error) {
	first, err := p.parseUnary(depth)
	if err != nil {
		return nil, err
	}
	kids := []node{first}
	for {
		p.skipSpace()
		if !p.atOr() {
			return mkOr(kids), nil
		}
		orPos := p.i
		if p.s[p.i] == '|' {
			p.i++
		} else {
			p.i += 2
		}
		p.skipSpace()
		if p.i >= len(p.s) || p.s[p.i] == ')' || p.atOr() {
			return nil, p.errAt(orPos, "OR needs a term after it")
		}
		n, err := p.parseUnary(depth)
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
	}
}

func (p *parser) parseUnary(depth int) (node, error) {
	if p.s[p.i] != '-' {
		return p.parseAtom(depth)
	}
	pos := p.i
	p.i++
	if p.i >= len(p.s) {
		return nil, p.errAt(pos, "nothing to negate after -")
	}
	if r, _ := utf8.DecodeRuneInString(p.s[p.i:]); r == ')' || r == '|' || unicode.IsSpace(r) {
		return nil, p.errAt(pos, "nothing to negate after -")
	}
	if p.s[p.i] == '-' {
		return nil, p.errAt(pos, "- cannot follow -")
	}
	kid, err := p.parseAtom(depth)
	if err != nil {
		return nil, err
	}
	return notNode{kid}, nil
}

// leaf counts one more term against the limit.
func (p *parser) leaf(pos int) error {
	p.terms++
	if p.terms > MaxTerms {
		return p.errAt(pos, "more than %d terms", MaxTerms)
	}
	return nil
}

func (p *parser) parseAtom(depth int) (node, error) {
	start := p.i
	switch p.s[start] {
	case '(':
		if depth+1 > MaxDepth {
			return nil, p.errAt(start, "parentheses nested deeper than %d", MaxDepth)
		}
		p.i++
		n, err := p.parseAnd(depth + 1)
		if err != nil {
			return nil, err
		}
		if p.i >= len(p.s) {
			return nil, p.errAt(start, "unclosed (")
		}
		p.i++
		if n == nil {
			return nil, p.errAt(start, "empty group")
		}
		return n, nil
	case ')':
		return nil, p.errAt(start, "unexpected )")
	case '"':
		if err := p.leaf(start); err != nil {
			return nil, err
		}
		content, err := p.quoted()
		if err != nil {
			return nil, err
		}
		words := splitWords(content)
		if len(words) == 0 {
			return nil, p.errAt(start, "phrase has no letters or digits")
		}
		return textNode{words: words}, nil
	}
	if p.atOr() {
		return nil, p.errAt(start, "OR needs a term before it")
	}
	if err := p.leaf(start); err != nil {
		return nil, err
	}
	end := p.scanWord()
	w := p.s[start:end]
	p.i = end
	if colon := strings.IndexByte(w, ':'); colon >= 0 {
		return p.operator(start, colon, w)
	}
	prefix := strings.HasSuffix(w, "*")
	words := splitWords(strings.TrimRight(w, "*"))
	if len(words) == 0 {
		return nil, p.errAt(start, "term has no letters or digits")
	}
	return textNode{words: words, prefix: prefix}, nil
}

// quoted reads a double-quoted string at p.i and returns what is inside.
func (p *parser) quoted() (string, error) {
	end := strings.IndexByte(p.s[p.i+1:], '"')
	if end < 0 {
		return "", p.errAt(p.i, "unterminated quote")
	}
	content := p.s[p.i+1 : p.i+1+end]
	p.i += end + 2
	return content, nil
}

// operator reads name:value. w is the token from start and colon is the offset
// of the colon in it; p.i is just past the token.
func (p *parser) operator(start, colon int, w string) (node, error) {
	name := strings.ToLower(w[:colon])
	resolve, isSet := p.env.Tickers[name]
	if !isSet && !slices.Contains(operatorNames, name) {
		return nil, p.errAt(start, "unknown operator %q; known operators: %s", w[:colon], strings.Join(p.env.names(), ", "))
	}
	val, valPos := w[colon+1:], start+colon+1
	if val == "" && p.i < len(p.s) && p.s[p.i] == '"' {
		valPos = p.i + 1
		var err error
		if val, err = p.quoted(); err != nil {
			return nil, err
		}
	}
	val = strings.ToLower(strings.TrimSpace(val))
	if val == "" {
		return nil, p.errAt(valPos, "operator %s needs a value", name)
	}
	if isSet {
		set, err := resolve(val)
		if err != nil {
			return nil, p.errAt(valPos, "%s: %v", name, err)
		}
		return setOp{name: name, val: val, set: set}, nil
	}
	switch name {
	case "kind":
		if !model.EventKind(val).Valid() {
			return nil, p.errAt(valPos, "unknown kind %q", val)
		}
	case "group":
		if !model.EventGroup(val).Valid() {
			return nil, p.errAt(valPos, "unknown group %q", val)
		}
	case "site":
		if val = trimSite(val); val == "" {
			return nil, p.errAt(valPos, "operator site needs a domain")
		}
	case "after", "before":
		t, err := time.Parse(time.DateOnly, val)
		if err != nil {
			return nil, p.errAt(valPos, "%s needs a day written YYYY-MM-DD", name)
		}
		return dateOp{name, t.Year(), int(t.Month()), t.Day()}, nil
	case "materiality", "relevance":
		return p.number(name, val, valPos)
	}
	return strOp{name, val}, nil
}

// trimSite drops trailing dots and the spaces a quoted value may hide among them.
func trimSite(v string) string {
	for {
		t := strings.TrimSpace(strings.TrimRight(v, "."))
		if t == v {
			return v
		}
		v = t
	}
}

// number reads an optional comparator and a plain decimal.
func (p *parser) number(name, val string, valPos int) (node, error) {
	cmp := cmpEQ
	for _, c := range []struct {
		text string
		kind cmpKind
	}{{">=", cmpGE}, {"<=", cmpLE}, {">", cmpGT}, {"<", cmpLT}, {"=", cmpEQ}} {
		if rest, ok := strings.CutPrefix(val, c.text); ok {
			cmp, val = c.kind, rest
			break
		}
	}
	digits, dots, other := 0, 0, 0
	for _, r := range val {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.':
			dots++
		default:
			other++
		}
	}
	if digits == 0 || dots > 1 || other > 0 {
		return nil, p.errAt(valPos, "%s needs a number, optionally after >=, >, <=, < or =", name)
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil || math.IsInf(f, 0) {
		return nil, p.errAt(valPos, "%s: number out of range", name)
	}
	return numOp{name, cmp, f}, nil
}
