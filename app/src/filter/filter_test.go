package filter

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/model"
)

// base is the article most match tests run against.
func base() model.Article {
	return model.Article{
		ID:        "a1",
		Domain:    "News.SEC.gov.",
		Title:     "Apple said it will raise the DIVIDEND",
		Summary:   "Free cash flow hit a record; Buyback expanded. 日本語 café",
		Published: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		Form:      "8-K",
		Items:     []string{"2.02", "9.01"},
		Tickers:   []string{"aapl", "MSFT"},
		Topics:    []string{"AI-Infra"},
		Score: model.Score{
			Relevance:   0.75,
			Materiality: 4,
			Kind:        model.EventBuyback,
			Sector:      "Technology",
		},
	}
}

func mustParse(t *testing.T, expr string) Query {
	t.Helper()
	q, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return q
}

func TestMatch(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		// plain terms
		{"dividend", true},
		{"DIVIDEND", true},
		{"divid", false},
		{"dividends", false},
		{"ai", false}, // inside "said"
		{"said", true},
		{"it will", true},
		{"zzz", false},
		{"café", true},
		{"cafe", false},
		{"日本語", true},
		{"日本", false},
		{"CAFÉ", true},

		// prefix
		{"divid*", true},
		{"dividend*", true},
		{"dividends*", false},
		{"a*", true},
		{"zz*", false},
		{"free-cash-fl*", true},
		{"free-cash-fx*", false},

		// phrases
		{"free-cash", true},
		{`"free cash flow"`, true},
		{`"FREE   Cash, Flow"`, true},
		{`"cash free"`, false},
		{`"free flow"`, false},
		{`"flow hit a record"`, true},
		{`"buyback expanded"`, true},
		{`"dividend free"`, true}, // across the Title and Summary join
		{`"record buyback"`, true},
		{"s&p", false},
		{"will-raise", true},

		// negation
		{"-zzz", true},
		{"-dividend", false},
		{`-"free cash"`, false},
		{`-"cash free"`, true},
		{"-ticker:aapl", false},
		{"-ticker:goog", true},
		{"dividend -zzz", true},
		{"dividend -buyback", false},
		{"-(zzz OR yyy)", true},
		{"-(zzz OR apple)", false},
		{"-(apple dividend)", false},
		{"-(apple zzz)", true},
		{"-(-apple)", true},

		// OR and precedence
		{"apple OR zzz", true},
		{"zzz OR yyy", false},
		{"zzz | dividend", true},
		{"zzz|dividend", true},
		{"zzz OR yyy OR dividend", true},
		{"apple dividend OR zzz", true},
		{"zzz apple OR dividend", false}, // zzz AND (apple OR dividend)
		{"apple OR zzz yyy", false},      // (apple OR zzz) AND yyy
		{"(apple OR zzz) yyy", false},
		{"apple OR (zzz yyy)", true},
		{"(zzz yyy) OR apple", true},
		{"(zzz OR apple) (yyy OR dividend)", true},
		{"zzz OR apple yyy OR dividend", true},
		{"apple or dividend", false}, // lower case "or" is a search word
		{"-zzz OR dividend", true},
		{"zzz OR -dividend", false},
		{"(apple)", true},
		{"((apple (dividend)))", true},

		// operators
		{"ticker:AAPL", true},
		{"ticker:aapl", true},
		{"TICKER:msft", true},
		{`ticker:"MSFT"`, true},
		{"ticker:goog", false},
		{"site:sec.gov", true},
		{"site:SEC.GOV.", true},
		{"site:news.sec.gov", true},
		{"site:ec.gov", false},
		{"site:gov", true},
		{"site:other.com", false},
		{"topic:ai-infra", true},
		{"topic:ai", false},
		{"kind:buyback", true},
		{"kind:Buyback", true},
		{"kind:dividend", false},
		{"group:capital", true},
		{"group:results", false},
		{"sector:technology", true},
		{"sector:Technology", true},
		{"sector:tech", false},
		{"form:8-K", true},
		{"form:8-k", true},
		{"form:10-K", false},
		{"item:2.02", true},
		{"item:9.01", true},
		{"item:2.0", false},
		{"item:7.01", false},
		{"after:2026-11-03", true},
		{"after:2026-11-04", false},
		{"after:2026-11-02", true},
		{"before:2026-11-04", true},
		{"before:2026-11-03", false},
		{"before:2026-11-02", false},
		{"materiality:4", true},
		{"materiality:=4", true},
		{"materiality:3", false},
		{"materiality:>=4", true},
		{"materiality:>=5", false},
		{"materiality:>3", true},
		{"materiality:>4", false},
		{"materiality:<=4", true},
		{"materiality:<=3", false},
		{"materiality:<5", true},
		{"materiality:<4", false},
		{"materiality:>=3.5", true},
		{"relevance:0.75", true},
		{"relevance:=0.75", true},
		{"relevance:0.7", false},
		{"relevance:>=0.7", true},
		{"relevance:>=.8", false},
		{"relevance:>0.75", false},
		{"relevance:>0.7", true},
		{"relevance:<=0.75", true},
		{"relevance:<0.75", false},
		{"relevance:<0.8", true},
		{"relevance:<=0.7", false},
		{"relevance:>=1", false},

		// combinations
		{`ticker:AAPL kind:buyback materiality:>=4 "free cash" -rumor`, true},
		{`ticker:AAPL kind:buyback materiality:>=5`, false},
		{"(kind:dividend OR kind:buyback) site:sec.gov", true},
		{"kind:dividend OR group:results", false},

		// everything
		{"", true},
		{"   \t ", true},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			q := mustParse(t, tt.expr)
			if got := q.Match(base()); got != tt.want {
				t.Errorf("Match(%q) = %v, want %v (canonical %q)", tt.expr, got, tt.want, q)
			}
		})
	}
}

func TestEmptyQueryMatchesEmptyArticle(t *testing.T) {
	for _, expr := range []string{"", "  "} {
		if !mustParse(t, expr).Match(model.Article{}) {
			t.Errorf("%q should match everything", expr)
		}
	}
	if !(Query{}).Match(model.Article{}) {
		t.Error("zero Query should match everything")
	}
	if got := (Query{}).String(); got != "" {
		t.Errorf("zero Query String = %q", got)
	}
	if mustParse(t, "ticker:x").Match(model.Article{}) {
		t.Error("operator matched an empty article")
	}
}

func TestMatchEmptyFieldsDoNotMatchOperators(t *testing.T) {
	a := model.Article{}
	for _, expr := range []string{"form:8-K", "sector:x", "site:x", "kind:other", "group:noise", "item:1.01", "topic:x"} {
		if mustParse(t, expr).Match(a) {
			t.Errorf("%q matched an empty article", expr)
		}
	}
}

func TestDatesAcrossBerlinDay(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("no tz data: %v", err)
	}
	// 00:30 on 1 November in Berlin is 23:30 on 31 October in UTC.
	a := base()
	a.Published = time.Date(2026, 11, 1, 0, 30, 0, 0, berlin)
	if got := a.Published.UTC().Format(time.RFC3339); got != "2026-10-31T23:30:00Z" {
		t.Fatalf("setup: %s", got)
	}
	tests := []struct {
		expr   string
		utc    bool
		berlin bool
	}{
		{"after:2026-11-01", false, true},
		{"before:2026-11-01", true, false},
		{"after:2026-10-31", true, true},
		{"before:2026-11-02", true, true},
		{"after:2026-11-01 before:2026-11-02", false, true},
		{"after:2026-10-31 before:2026-11-01", true, false},
	}
	for _, tt := range tests {
		q := mustParse(t, tt.expr)
		if got := q.Match(a); got != tt.utc {
			t.Errorf("Match(%q) = %v, want %v", tt.expr, got, tt.utc)
		}
		if got := q.MatchIn(a, nil); got != tt.utc {
			t.Errorf("MatchIn(%q, nil) = %v, want %v", tt.expr, got, tt.utc)
		}
		if got := q.MatchIn(a, time.UTC); got != tt.utc {
			t.Errorf("MatchIn(%q, UTC) = %v, want %v", tt.expr, got, tt.utc)
		}
		if got := q.MatchIn(a, berlin); got != tt.berlin {
			t.Errorf("MatchIn(%q, Berlin) = %v, want %v", tt.expr, got, tt.berlin)
		}
	}

	// The day is 25 hours long when the clocks go back (25 October 2026).
	edge := base()
	edge.Published = time.Date(2026, 10, 25, 22, 59, 0, 0, time.UTC) // 23:59 Berlin, still the 25th
	if !mustParse(t, "before:2026-10-26").MatchIn(edge, berlin) {
		t.Error("23:59 Berlin on the 25th should be before the 26th")
	}
	edge.Published = time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC) // midnight Berlin
	if !mustParse(t, "after:2026-10-26").MatchIn(edge, berlin) {
		t.Error("midnight Berlin on the 26th should be after:2026-10-26")
	}
}

func TestSyntaxErrors(t *testing.T) {
	longTerms := make([]string, MaxTerms+1)
	for i := range longTerms {
		longTerms[i] = fmt.Sprintf("w%d", i)
	}
	tests := []struct {
		expr string
		pos  int
		msg  string
	}{
		{`"abc`, 0, "unterminated quote"},
		{`a "b c`, 2, "unterminated quote"},
		{`ticker:"x`, 7, "unterminated quote"},
		{"a (b", 2, "unclosed ("},
		{"((a)", 0, "unclosed ("},
		{"a )", 2, "unmatched )"},
		{"(a))", 3, "unmatched )"},
		{"()", 0, "empty group"},
		{"a ( )", 2, "empty group"},
		{"a OR", 2, "OR needs a term after it"},
		{"a |", 2, "OR needs a term after it"},
		{"(a OR)", 3, "OR needs a term after it"},
		{"a OR OR b", 2, "OR needs a term after it"},
		{"a | | b", 2, "OR needs a term after it"},
		{"OR a", 0, "OR needs a term before it"},
		{"| a", 0, "OR needs a term before it"},
		{"a (OR b)", 3, "OR needs a term before it"},
		{"-OR", 1, "OR needs a term before it"},
		{"-", 0, "nothing to negate"},
		{"a - b", 2, "nothing to negate"},
		{"(a -)", 3, "nothing to negate"},
		{"a -|", 2, "nothing to negate"},
		{"--a", 0, "cannot follow"},
		{"foo:bar", 0, "unknown operator"},
		{"a bogus:x", 2, "unknown operator"},
		{"-bogus:x", 1, "unknown operator"},
		{":x", 0, "unknown operator"},
		{"ticker:", 7, "needs a value"},
		{`ticker:""`, 8, "needs a value"},
		{`ticker:"  "`, 8, "needs a value"},
		{"a ticker:", 9, "needs a value"},
		{"kind:nope", 5, "unknown kind"},
		{"group:nope", 6, "unknown group"},
		{"group:buyback", 6, "unknown group"},
		{"kind:capital", 5, "unknown kind"},
		{"after:2026-13-01", 6, "YYYY-MM-DD"},
		{"after:2026-02-30", 6, "YYYY-MM-DD"},
		{"after:yesterday", 6, "YYYY-MM-DD"},
		{"before:2026-11-4", 7, "YYYY-MM-DD"},
		{"a before:11/04/2026", 9, "YYYY-MM-DD"},
		{"materiality:>=", 12, "needs a number"},
		{"materiality:abc", 12, "needs a number"},
		{"materiality:>>4", 12, "needs a number"},
		{"materiality:-1", 12, "needs a number"},
		{"relevance:1.2.3", 10, "needs a number"},
		{"relevance:>=0.7x", 10, "needs a number"},
		{"relevance:.", 10, "needs a number"},
		{"relevance:1" + strings.Repeat("0", 400), 10, "out of range"},
		{"site:.", 5, "needs a domain"},
		{"*", 0, "no letters or digits"},
		{"a !!", 2, "no letters or digits"},
		{"a ~~~", 2, "no letters or digits"},
		{`"!!"`, 0, "no letters or digits"},
		{`a ""`, 2, "no letters or digits"},
		{"a\xffb", 1, "invalid UTF-8"},
		{strings.Repeat("a", MaxLen+1), MaxLen, "longer than"},
		{strings.Repeat("(", MaxDepth+1) + "a" + strings.Repeat(")", MaxDepth+1), MaxDepth, "nested deeper"},
		{strings.Join(longTerms, " "), strings.Index(strings.Join(longTerms, " "), "w100"), "more than 100 terms"},
	}
	for _, tt := range tests {
		name := tt.expr
		if len(name) > 40 {
			name = name[:40]
		}
		t.Run(name, func(t *testing.T) {
			_, err := Parse(tt.expr)
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Parse(%q) error = %v, want *SyntaxError", tt.expr, err)
			}
			if se.Pos != tt.pos {
				t.Errorf("Pos = %d, want %d (%v)", se.Pos, tt.pos, se)
			}
			if !strings.Contains(se.Msg, tt.msg) {
				t.Errorf("Msg = %q, want it to contain %q", se.Msg, tt.msg)
			}
			if !strings.HasPrefix(se.Error(), "filter: ") {
				t.Errorf("Error() = %q, want the filter: prefix", se.Error())
			}
		})
	}
}

func TestUnknownOperatorListsKnownOnes(t *testing.T) {
	_, err := Parse("colour:red")
	if err == nil {
		t.Fatal("no error")
	}
	for _, name := range operatorNames {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not list %q", err, name)
		}
	}
	if len(operatorNames) != 12 {
		t.Errorf("operatorNames = %v", operatorNames)
	}
}

func TestLimitsAtTheBoundary(t *testing.T) {
	ok := []string{
		strings.Repeat("a", MaxLen),
		strings.Repeat("(", MaxDepth) + "a" + strings.Repeat(")", MaxDepth),
	}
	words := make([]string, MaxTerms)
	for i := range words {
		words[i] = fmt.Sprintf("w%d", i)
	}
	ok = append(ok, strings.Join(words, " "), strings.Join(words, " OR "))
	for _, expr := range ok {
		if _, err := Parse(expr); err != nil {
			t.Errorf("Parse(%.30q...) = %v, want success", expr, err)
		}
	}
	// A deeply negated chain of groups stays inside the limit and does not recurse past it.
	if _, err := Parse(strings.Repeat("-(", MaxDepth) + "a" + strings.Repeat(")", MaxDepth)); err != nil {
		t.Errorf("negated nesting at the limit: %v", err)
	}
}

func TestString(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"  Dividend  ", "dividend"},
		{"free-cash", `"free cash"`},
		{`"free  CASH"`, `"free cash"`},
		{`"Free"`, "free"},
		{"dividen*", "dividen*"},
		{"free-cash*", "free-cash*"},
		{"Dividen**", "dividen*"},
		{"(a b) c", "a b c"},
		{"a (b OR c)", "a b OR c"},
		{"a b OR c", "a b OR c"},
		{"(a b) OR c", "(a b) OR c"},
		{"a | b", "a OR b"},
		{"(a)", "a"},
		{"-(a)", "-a"},
		{"-(a b)", "-(a b)"},
		{"-(a OR b)", "-(a OR b)"},
		{"-(-a)", "-(-a)"},
		{`-"a b"`, `-"a b"`},
		{"a OR b OR c", "a OR b OR c"},
		{"(a OR b) OR c", "a OR b OR c"},
		{"a OR (b c) OR d", "a OR (b c) OR d"},
		{"(a OR b) (c OR d)", "a OR b c OR d"},
		{"TICKER:Aapl", "ticker:aapl"},
		{"-ticker:X", "-ticker:x"},
		{`site:"SEC.gov."`, "site:sec.gov"},
		{`site:"0 ."`, "site:0"},
		{`topic:"a b"`, `topic:"a b"`},
		{`topic:"a(b"`, `topic:"a(b"`},
		{"kind:Buyback", "kind:buyback"},
		{"group:CAPITAL", "group:capital"},
		{"materiality:>=4", "materiality:>=4"},
		{"materiality:=4", "materiality:4"},
		{"materiality:<4", "materiality:<4"},
		{"relevance:>.70", "relevance:>0.7"},
		{"relevance:<=0.700", "relevance:<=0.7"},
		{"after:2026-11-01", "after:2026-11-01"},
		{"BEFORE:2026-11-04", "before:2026-11-04"},
		{"or and", "or and"},
	}
	for _, tt := range tests {
		q := mustParse(t, tt.in)
		if got := q.String(); got != tt.want {
			t.Errorf("String(%q) = %q, want %q", tt.in, got, tt.want)
		}
		assertRoundTrip(t, q)
	}
}

func assertRoundTrip(t *testing.T, q Query) {
	t.Helper()
	s := q.String()
	q2, err := Parse(s)
	if err != nil {
		t.Fatalf("reparse %q: %v", s, err)
	}
	if !reflect.DeepEqual(q, q2) {
		t.Fatalf("round trip of %q changed the query:\n%#v\n%#v", s, q, q2)
	}
	if s2 := q2.String(); s2 != s {
		t.Fatalf("String not stable: %q then %q", s, s2)
	}
}

func TestEqualQueries(t *testing.T) {
	groups := [][]string{
		{"a b c", "(a b) c", "a (b c)", "((a) b) c"},
		{"a OR b OR c", "(a OR b) OR c", "a OR (b OR c)", "a | b | c"},
		{"TICKER:aapl", "ticker:AAPL", `ticker:"AAPL"`},
		{"free-cash", `"free cash"`, `"FREE, cash"`},
	}
	for _, g := range groups {
		first := mustParse(t, g[0])
		for _, e := range g[1:] {
			if q := mustParse(t, e); !reflect.DeepEqual(first, q) {
				t.Errorf("%q and %q should be equal queries", g[0], e)
			}
		}
	}
	if reflect.DeepEqual(mustParse(t, "a b OR c"), mustParse(t, "(a b) OR c")) {
		t.Error("precedence lost")
	}
}

func TestFTS(t *testing.T) {
	tests := []struct {
		expr string
		want string // empty means not usable
	}{
		{"", ""},
		{"dividend", `"dividend"`},
		{"Dividend", `"dividend"`},
		{"dividen*", `"dividen"*`},
		{"free-cash", `"free cash"`},
		{`"free cash flow"`, `"free cash flow"`},
		{"free-cash*", `"free cash"*`},
		{"café 日本語", `"café" AND "日本語"`},
		{"a b", `"a" AND "b"`},
		{"a b c", `"a" AND "b" AND "c"`},
		{"a OR b", `"a" OR "b"`},
		{"a | b | c", `"a" OR "b" OR "c"`},
		{"a b OR c", `"a" AND ("b" OR "c")`},
		{"a OR b c", `("a" OR "b") AND "c"`},
		{"(a b) OR c", `("a" AND "b") OR "c"`},
		{"(a OR b) (c OR d)", `("a" OR "b") AND ("c" OR "d")`},
		{`a "b c" d*`, `"a" AND "b c" AND "d"*`},

		// nothing usable
		{"-a", ""},
		{`-"a b"`, ""},
		{"-(a b)", ""},
		{"ticker:AAPL", ""},
		{"kind:buyback", ""},
		{"materiality:>=4 after:2026-11-01", ""},
		{"-a ticker:X after:2026-01-01", ""},

		// dropped from an AND
		{"a -b", `"a"`},
		{"a ticker:X", `"a"`},
		{"a -b ticker:X c", `"a" AND "c"`},
		{"-a b after:2026-01-01", `"b"`},

		// an OR with an unusable branch is unusable, so the AND drops it
		{"a OR ticker:X", ""},
		{"a OR -b", ""},
		{"ticker:X OR a", ""},
		{"a OR b OR ticker:X", ""},
		{"c (a OR ticker:X)", `"c"`},
		{"(a OR b OR -c) d", `"d"`},
		{"(a OR (b OR ticker:X)) c", `"c"`},
		{"(a OR -b) (c OR -d)", ""},

		// an AND inside an OR branch may lose its unusable parts: that widens the branch
		{"c (a OR b ticker:X)", `"c" AND ("a" OR "b")`},
		{"(a -b) OR (c ticker:X)", `"a" OR "c"`},
		{"(a -b) OR (ticker:X after:2026-01-01)", ""},

		// FTS keywords and syntax in the search text stay inside quotes
		{"x AND y", `"x" AND "and" AND "y"`},
		{"NEAR(a b)", `"near" AND "a" AND "b"`},
		{"a* OR NOT b", `("a"* OR "not") AND "b"`},
		{"col:x", ""}, // not an operator: syntax error handled below
	}
	for _, tt := range tests {
		q, err := Parse(tt.expr)
		if tt.expr == "col:x" {
			if err == nil {
				t.Errorf("Parse(%q) should fail", tt.expr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.expr, err)
		}
		got, ok := q.FTS()
		if tt.want == "" {
			if ok || got != "" {
				t.Errorf("FTS(%q) = %q, %v, want unusable", tt.expr, got, ok)
			}
			continue
		}
		if !ok || got != tt.want {
			t.Errorf("FTS(%q) = %q, %v, want %q", tt.expr, got, ok, tt.want)
		}
	}
}

func TestFTSQuote(t *testing.T) {
	if got := ftsQuote(`a"b`); got != `"a""b"` {
		t.Errorf("ftsQuote = %s", got)
	}
	if got := ftsQuote(`"`); got != `""""` {
		t.Errorf("ftsQuote = %s", got)
	}
}

func TestFTSMarkOnlyWordIsUnusable(t *testing.T) {
	q := mustParse(t, "́")
	if got, ok := q.FTS(); ok {
		t.Errorf("FTS = %q, want unusable", got)
	}
	if got, ok := mustParse(t, "a ́").FTS(); !ok || got != `"a"` {
		t.Errorf("FTS = %q, %v", got, ok)
	}
}

// ---- property: Match implies the FTS prefilter ----

// docTokens models the FTS5 unicode61 tokenizer for the test: lower-cased runs
// of letters, numbers and marks.
func docTokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r))
	})
}

// ftsEval is a tiny evaluator for the FTS5 subset FTS() produces: quoted
// phrases with an optional trailing *, AND, OR and parentheses.
type ftsEval struct {
	toks []string // lexer output
	pos  int
	doc  []string
	err  error
}

func evalFTS(expr string, doc []string) (bool, error) {
	e := &ftsEval{doc: doc}
	if err := e.lex(expr); err != nil {
		return false, err
	}
	v := e.or()
	if e.err == nil && e.pos != len(e.toks) {
		e.err = fmt.Errorf("trailing tokens in %q", expr)
	}
	return v, e.err
}

func (e *ftsEval) lex(s string) error {
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ':
			i++
		case c == '(' || c == ')' || c == '*':
			e.toks = append(e.toks, string(c))
			i++
		case c == '"':
			j := i + 1
			var sb strings.Builder
			for {
				if j >= len(s) {
					return fmt.Errorf("unterminated quote in %q", s)
				}
				if s[j] == '"' {
					if j+1 < len(s) && s[j+1] == '"' {
						sb.WriteByte('"')
						j += 2
						continue
					}
					break
				}
				sb.WriteByte(s[j])
				j++
			}
			e.toks = append(e.toks, `"`+sb.String())
			i = j + 1
		case strings.HasPrefix(s[i:], "AND"):
			e.toks = append(e.toks, "AND")
			i += 3
		case strings.HasPrefix(s[i:], "OR"):
			e.toks = append(e.toks, "OR")
			i += 2
		default:
			return fmt.Errorf("unexpected %q in %q", string(c), s)
		}
	}
	return nil
}

func (e *ftsEval) peek() string {
	if e.pos < len(e.toks) {
		return e.toks[e.pos]
	}
	return ""
}

func (e *ftsEval) or() bool {
	v := e.and()
	for e.peek() == "OR" {
		e.pos++
		r := e.and()
		v = v || r
	}
	return v
}

func (e *ftsEval) and() bool {
	v := e.atom()
	for e.peek() == "AND" {
		e.pos++
		r := e.atom()
		v = v && r
	}
	return v
}

func (e *ftsEval) atom() bool {
	t := e.peek()
	switch {
	case t == "(":
		e.pos++
		v := e.or()
		if e.peek() != ")" {
			e.err = errors.New("missing )")
			return false
		}
		e.pos++
		return v
	case strings.HasPrefix(t, `"`):
		e.pos++
		prefix := false
		if e.peek() == "*" {
			prefix = true
			e.pos++
		}
		return phraseIn(e.doc, docTokens(t[1:]), prefix)
	}
	e.err = fmt.Errorf("unexpected token %q", t)
	return false
}

func phraseIn(doc, phrase []string, prefix bool) bool {
	if len(phrase) == 0 {
		return true
	}
	last := len(phrase) - 1
	for i := 0; i+last < len(doc); i++ {
		ok := true
		for j := 0; j < last && ok; j++ {
			ok = doc[i+j] == phrase[j]
		}
		if !ok {
			continue
		}
		if w := doc[i+last]; w == phrase[last] || (prefix && strings.HasPrefix(w, phrase[last])) {
			return true
		}
	}
	return false
}

func TestEvalFTSModel(t *testing.T) {
	doc := docTokens("Apple said it will raise the dividend, free cash flow")
	tests := []struct {
		expr string
		want bool
	}{
		{`"apple"`, true},
		{`"ai"`, false},
		{`"free cash"`, true},
		{`"cash free"`, false},
		{`"divid"*`, true},
		{`"divid"`, false},
		{`"free cash fl"*`, true},
		{`"zzz" OR "apple"`, true},
		{`"zzz" AND "apple"`, false},
		{`"zzz" AND ("apple" OR "said")`, false},
		{`("zzz" OR "apple") AND "said"`, true},
		{`"say""x"`, false},
	}
	for _, tt := range tests {
		got, err := evalFTS(tt.expr, doc)
		if err != nil || got != tt.want {
			t.Errorf("evalFTS(%s) = %v, %v, want %v", tt.expr, got, err, tt.want)
		}
	}
}

var vocab = []string{
	"ai", "said", "dividend", "dividends", "free", "cash", "flow", "café", "cafe",
	"日本語", "buyback", "apple", "10", "q3", "or", "and", "near", "étoile", "naïve",
}

type gen struct {
	r     *rand.Rand
	terms int
}

func (g *gen) word() string { return vocab[g.r.Intn(len(vocab))] }

func (g *gen) term() string {
	g.terms++
	switch g.r.Intn(6) {
	case 0:
		return g.word()
	case 1:
		if w := g.word(); w != "or" { // "OR" would be the operator
			return strings.ToUpper(w)
		}
		return "or"
	case 2:
		rs := []rune(g.word())
		return string(rs[:1+g.r.Intn(len(rs))]) + "*"
	case 3:
		return `"` + g.word() + " " + g.word() + `"`
	case 4:
		return g.word() + "-" + g.word()
	default:
		return `"` + g.word() + " " + g.word() + " " + g.word() + `"`
	}
}

var genOps = []string{
	"ticker:AAPL", "ticker:goog", "site:sec.gov", "topic:ai-infra", "kind:buyback", "kind:dividend",
	"group:capital", "sector:technology", "form:8-K", "item:2.02", "after:2026-11-02",
	"before:2026-11-04", "materiality:>=3", "materiality:<2", "relevance:>=0.5", "relevance:0.3",
}

func (g *gen) unit(depth int) string {
	neg := ""
	if g.r.Intn(4) == 0 {
		neg = "-"
	}
	switch k := g.r.Intn(10); {
	case k < 6:
		return neg + g.term()
	case k < 8:
		g.terms++
		return neg + genOps[g.r.Intn(len(genOps))]
	case depth < 4:
		return neg + "(" + g.and(depth+1) + ")"
	}
	return neg + g.term()
}

func (g *gen) or(depth int) string {
	s := g.unit(depth)
	for g.r.Intn(4) == 0 {
		sep := " OR "
		if g.r.Intn(3) == 0 {
			sep = " | "
		}
		s += sep + g.unit(depth)
	}
	return s
}

func (g *gen) and(depth int) string {
	parts := []string{g.or(depth)}
	for g.r.Intn(3) != 0 && g.terms < 40 {
		parts = append(parts, g.or(depth))
	}
	return strings.Join(parts, " ")
}

func randomArticle(r *rand.Rand) model.Article {
	text := func(max int) string {
		var sb strings.Builder
		for i, n := 0, r.Intn(max+1); i < n; i++ {
			if i > 0 {
				sb.WriteString([]string{" ", " ", "-", ", ", ". ", " "}[r.Intn(6)])
			}
			w := vocab[r.Intn(len(vocab))]
			if r.Intn(5) == 0 {
				w = strings.ToUpper(w)
			}
			if r.Intn(8) == 0 {
				w += "s"
			}
			sb.WriteString(w)
		}
		return sb.String()
	}
	pick := func(from ...string) []string {
		var out []string
		for _, s := range from {
			if r.Intn(2) == 0 {
				out = append(out, s)
			}
		}
		return out
	}
	kinds := []model.EventKind{model.EventBuyback, model.EventDividend, model.EventResults, model.EventOther}
	return model.Article{
		Title:     text(6),
		Summary:   text(10),
		Domain:    []string{"sec.gov", "www.sec.gov", "example.com", ""}[r.Intn(4)],
		Published: time.Date(2026, 11, 1+r.Intn(5), r.Intn(24), 0, 0, 0, time.UTC),
		Form:      []string{"8-K", "10-K", ""}[r.Intn(3)],
		Items:     pick("2.02", "9.01"),
		Tickers:   pick("AAPL", "MSFT"),
		Topics:    pick("ai-infra", "rates"),
		Score: model.Score{
			Relevance:   float64(r.Intn(11)) / 10,
			Materiality: 1 + r.Intn(5),
			Kind:        kinds[r.Intn(len(kinds))],
			Sector:      []string{"Technology", "Energy"}[r.Intn(2)],
		},
	}
}

func TestMatchImpliesFTS(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	articles := make([]model.Article, 150)
	for i := range articles {
		articles[i] = randomArticle(r)
	}
	matched, withFTS, filtered := 0, 0, 0
	for i := 0; i < 1000; i++ {
		g := &gen{r: r}
		expr := g.and(0)
		q, err := Parse(expr)
		if err != nil {
			t.Fatalf("generated query %q does not parse: %v", expr, err)
		}
		assertRoundTrip(t, q)
		fts, ok := q.FTS()
		if ok {
			withFTS++
		}
		q2 := mustParse(t, q.String())
		for _, a := range articles {
			m := q.Match(a)
			if q2.Match(a) != m {
				t.Fatalf("canonical form of %q changes the result for %+v", expr, a)
			}
			if !ok {
				continue
			}
			sat, err := evalFTS(fts, docTokens(a.Title+" "+a.Summary))
			if err != nil {
				t.Fatalf("query %q gave bad FTS %s: %v", expr, fts, err)
			}
			if m {
				matched++
				if !sat {
					t.Fatalf("query %q accepts %q / %q but FTS %s does not", expr, a.Title, a.Summary, fts)
				}
			} else if !sat {
				filtered++
			}
		}
	}
	if matched < 2000 || withFTS < 500 || filtered < 10000 {
		t.Errorf("weak property test: %d matches, %d queries with FTS, %d rejected by FTS", matched, withFTS, filtered)
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"", " ", "a", "a b", "a OR b", "a | b c", "(a b) OR c", "-a", "-(a OR b)", `"free cash flow"`,
		"free-cash dividen*", "ticker:AAPL", `site:"sec.gov"`, "materiality:>=4 relevance:<0.7",
		"after:2026-11-01 before:2026-11-04", "kind:buyback group:capital", "((a))", "a OR", "(", ")", `"`,
		"-", "--a", "x:", "日本語 café", "́", "a\xff", strings.Repeat("(", 25),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	art := base()
	f.Fuzz(func(t *testing.T, s string) {
		q, err := Parse(s)
		if err != nil {
			var se *SyntaxError
			if !errors.As(err, &se) || se.Pos < 0 || se.Pos > len(s) {
				t.Fatalf("Parse(%q) error %v is not a *SyntaxError inside the input", s, err)
			}
			return
		}
		q.Match(art)
		q.FTS()
		canon := q.String()
		if len(canon) > MaxLen {
			return // Parse would reject the canonical form for its length alone
		}
		q2, err := Parse(canon)
		if err != nil {
			t.Fatalf("String() of %q is %q, which does not parse: %v", s, canon, err)
		}
		if !reflect.DeepEqual(q, q2) {
			t.Fatalf("String() of %q is %q, which parses to a different query", s, canon)
		}
		if q2.Match(art) != q.Match(art) {
			t.Fatalf("String() of %q changes the match", s)
		}
	})
}
