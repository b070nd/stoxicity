package universe

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Query is a parsed search over companies. The grammar is the one of package
// filter, cut down to what a company has:
//
//	apple                    a word: the ticker, or a word of the name, former
//	                         names or industry, starting with it ("app" finds Apple)
//	"berkshire hathaway"     a phrase: those words in a row
//	-etf  -"blank check"     not
//	a | b   a OR b           either (OR binds tighter than the space, as in Google)
//	ticker:KO  ticker:A*     the ticker; a trailing * is a prefix
//	name:nvidia              the name, aliases and former names (company: is the same)
//	cik:320193
//	exchange:nyse            the SEC's exchange or Nasdaq's venue ("NYSE Arca")
//	kind:etf                 stock, etf or test (needs the Nasdaq directory)
//	industry:software        words of the SIC description (needs "universe enrich")
//	sic:3571  sic:73*
//	state:de                 state of incorporation
//	sector:technology        the sector rules of the configuration
//	market:"global select"   Nasdaq market tier
//	status:deficient         financial status; status:any is any trouble
//	list:compounders         member of one of your lists
//	tag:moat                 carries your tag
//	watched:yes              on your watchlist or in a watch
//	news:>=3  news:yes       relevant articles in the window the screen covers
//	sort:name  sort:-news    the order (ticker, name, news, industry, exchange,
//	                         kind); a leading - reverses it. Not a filter.
//
// Free text ranks by how well it matches: a ticker, then the start of a name,
// then a word inside it, then a former name, then the industry. A query without
// free text lists in the order of the tickers, the order of a catalogue; sort:
// picks another order whatever the query.
type Query struct {
	clauses [][]lit // all clauses must hold; a clause holds if any literal does
	sortBy  string  // "" means the default order
	desc    bool
}

// sortKeys are the orders sort: knows.
var sortKeys = []string{"ticker", "name", "news", "industry", "exchange", "kind"}

// SortKey is the order the query asks for with sort:, without the minus, and
// whether it is reversed; empty when it asks for none.
func (q Query) SortKey() (key string, desc bool) { return q.sortBy, q.desc }

// WithSort returns the query with the order replaced, as if it said sort:key
// (or sort:-key when desc). An unknown key leaves the order as it was.
func (q Query) WithSort(key string, desc bool) Query {
	if slices.Contains(sortKeys, key) {
		q.sortBy, q.desc = key, desc
	}
	return q
}

// SortKeys are the orders sort: knows.
func SortKeys() []string { return slices.Clone(sortKeys) }

// QueryError is a problem in a query.
type QueryError struct {
	Pos int
	Msg string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("universe: query: %s (at byte %d)", e.Msg, e.Pos)
}

// Limits on a query.
const (
	maxQueryLen   = 500
	maxQueryTerms = 40
)

// queryOps is the list of operators, for the error message.
var queryOps = []string{
	"cik", "company", "exchange", "industry", "kind", "list", "market", "name",
	"news", "sector", "sic", "sort", "state", "status", "tag", "ticker", "watched",
}

type cmpKind int

const (
	cmpEQ cmpKind = iota
	cmpGE
	cmpGT
	cmpLE
	cmpLT
)

type lit struct {
	neg   bool
	op    string   // "" for free text
	raw   string   // the lower-case text or value as typed
	words []string // raw split into words
	cmp   cmpKind  // news
	num   int      // news
	yes   bool     // watched, and news:yes / news:no
}

// Facts are what a company's profile cannot say: how much news it has in the
// window being looked at, and whether you follow it.
type Facts struct {
	News    map[string]int
	Watched map[string]bool
}

// ParseQuery reads a query. The error, if any, is a *QueryError.
func ParseQuery(expr string) (Query, error) {
	if len(expr) > maxQueryLen {
		return Query{}, &QueryError{Pos: maxQueryLen, Msg: fmt.Sprintf("longer than %d bytes", maxQueryLen)}
	}
	var q Query
	var cur []lit
	flush := func() {
		if len(cur) > 0 {
			q.clauses = append(q.clauses, cur)
			cur = nil
		}
	}
	terms, orPending, orPos := 0, false, 0
	i := 0
	for i < len(expr) {
		if expr[i] == ' ' || expr[i] == '\t' {
			i++
			continue
		}
		start := i
		if expr[i] == '|' || strings.HasPrefix(expr[i:], "OR") && (i+2 == len(expr) || expr[i+2] == ' ') {
			if len(cur) == 0 || orPending {
				return Query{}, &QueryError{Pos: i, Msg: "OR needs a term before it"}
			}
			orPending, orPos = true, i
			if expr[i] == '|' {
				i++
			} else {
				i += 2
			}
			continue
		}
		neg := false
		if expr[i] == '-' && i+1 < len(expr) && expr[i+1] != ' ' {
			neg = true
			i++
		}
		var l lit
		if expr[i] == '"' {
			s, n, err := quoted(expr, i)
			if err != nil {
				return Query{}, err
			}
			i = n
			if l, err = textLit(s, start); err != nil {
				return Query{}, err
			}
		} else {
			j := i
			for j < len(expr) && expr[j] != ' ' && expr[j] != '\t' && expr[j] != '"' && expr[j] != '|' {
				j++
			}
			tok := expr[i:j]
			i = j
			if colon := strings.IndexByte(tok, ':'); colon >= 0 {
				name, val, valPos := strings.ToLower(tok[:colon]), tok[colon+1:], start+colon+1
				if neg {
					valPos++
				}
				if val == "" && i < len(expr) && expr[i] == '"' {
					s, n, err := quoted(expr, i)
					if err != nil {
						return Query{}, err
					}
					val, valPos, i = s, i+1, n
				}
				if name == "sort" {
					if neg || orPending {
						return Query{}, &QueryError{Pos: start, Msg: "sort: orders the list; it cannot be negated or joined with OR"}
					}
					desc := strings.HasPrefix(val, "-")
					key := strings.ToLower(strings.TrimPrefix(val, "-"))
					if !slices.Contains(sortKeys, key) {
						return Query{}, &QueryError{Pos: valPos, Msg: fmt.Sprintf("unknown order %q; use %s", val, strings.Join(sortKeys, ", "))}
					}
					q.sortBy, q.desc = key, desc
					continue
				}
				var err error
				if l, err = opLit(name, val, start, valPos); err != nil {
					return Query{}, err
				}
			} else {
				var err error
				if l, err = textLit(strings.TrimRight(tok, "*"), start); err != nil {
					return Query{}, err
				}
			}
		}
		l.neg = neg
		if terms++; terms > maxQueryTerms {
			return Query{}, &QueryError{Pos: start, Msg: fmt.Sprintf("more than %d terms", maxQueryTerms)}
		}
		if orPending {
			orPending = false
		} else {
			flush()
		}
		cur = append(cur, l)
	}
	if orPending {
		return Query{}, &QueryError{Pos: orPos, Msg: "OR needs a term after it"}
	}
	flush()
	return q, nil
}

// quoted reads a double-quoted string at s[i] and returns it with the index
// after the closing quote.
func quoted(s string, i int) (string, int, error) {
	end := strings.IndexByte(s[i+1:], '"')
	if end < 0 {
		return "", 0, &QueryError{Pos: i, Msg: "unterminated quote"}
	}
	return s[i+1 : i+1+end], i + end + 2, nil
}

func textLit(s string, pos int) (lit, error) {
	raw := strings.ToLower(strings.TrimSpace(s))
	words := splitWords(raw)
	if len(words) == 0 {
		return lit{}, &QueryError{Pos: pos, Msg: "a term needs letters or digits"}
	}
	return lit{raw: raw, words: words}, nil
}

// opLit builds the literal of name:value.
func opLit(name, value string, pos, valPos int) (lit, error) {
	val := strings.ToLower(strings.TrimSpace(value))
	if !slices.Contains(queryOps, name) {
		return lit{}, &QueryError{Pos: pos, Msg: fmt.Sprintf("unknown operator %q; known operators: %s", name, strings.Join(queryOps, ", "))}
	}
	if val == "" {
		return lit{}, &QueryError{Pos: valPos, Msg: fmt.Sprintf("operator %s needs a value", name)}
	}
	l := lit{op: name, raw: val, words: splitWords(val)}
	switch name {
	case "kind":
		if !slices.Contains([]string{KindStock, KindETF, KindTest}, val) {
			return lit{}, &QueryError{Pos: valPos, Msg: fmt.Sprintf("unknown kind %q; use stock, etf or test", val)}
		}
	case "name", "company", "industry", "market":
		if len(l.words) == 0 {
			return lit{}, &QueryError{Pos: valPos, Msg: fmt.Sprintf("operator %s needs letters or digits", name)}
		}
	case "watched":
		y, ok := yesNo(val)
		if !ok {
			return lit{}, &QueryError{Pos: valPos, Msg: "watched takes yes or no"}
		}
		l.yes = y
	case "news":
		if y, ok := yesNo(val); ok {
			l.yes = y
			l.cmp, l.num = cmpEQ, 0
			if y {
				l.cmp, l.num = cmpGT, 0
			}
			break
		}
		cmp, rest := cmpEQ, val
		for _, c := range []struct {
			text string
			kind cmpKind
		}{{">=", cmpGE}, {"<=", cmpLE}, {">", cmpGT}, {"<", cmpLT}, {"=", cmpEQ}} {
			if t, ok := strings.CutPrefix(val, c.text); ok {
				cmp, rest = c.kind, t
				break
			}
		}
		n, err := strconv.Atoi(rest)
		if err != nil || n < 0 {
			return lit{}, &QueryError{Pos: valPos, Msg: "news takes yes, no or a number such as >=3"}
		}
		l.cmp, l.num = cmp, n
	}
	return l, nil
}

func yesNo(s string) (yes, ok bool) {
	switch s {
	case "yes", "true", "1":
		return true, true
	case "no", "false", "0":
		return false, true
	}
	return false, false
}

// splitWords lower-cases s and cuts it into runs of letters and digits.
func splitWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// HasText reports whether the query has a free-text term, which ranks the
// matches by how well they fit.
func (q Query) HasText() bool {
	for _, clause := range q.clauses {
		for _, l := range clause {
			if l.op == "" && !l.neg {
				return true
			}
		}
	}
	return false
}

// IsEmpty reports whether the query has no terms and so matches everything.
func (q Query) IsEmpty() bool { return len(q.clauses) == 0 }

// Match reports whether the profile satisfies the query.
func (q Query) Match(p *Profile, f Facts) bool {
	ok, _ := q.eval(p, f)
	return ok
}

// eval returns whether the profile matches and how well its text terms do.
func (q Query) eval(p *Profile, f Facts) (bool, int) {
	total := 0
	for _, clause := range q.clauses {
		held, best := false, 0
		for _, l := range clause {
			ok, s := l.eval(p, f)
			if l.neg {
				ok, s = !ok, 0
			}
			if ok {
				held = true
				best = max(best, s)
			}
		}
		if !held {
			return false, 0
		}
		total += best
	}
	return true, total
}

func (l lit) eval(p *Profile, f Facts) (bool, int) {
	switch l.op {
	case "":
		s := textScore(l, p)
		return s > 0, s
	case "ticker":
		return tickerIs(p, l.raw), 0
	case "name", "company":
		return nameScore(l.words, p) > 0, 0
	case "cik":
		return p.CIK != "" && strings.TrimLeft(p.CIK, "0") == strings.TrimLeft(l.raw, "0"), 0
	case "exchange":
		return eqOrPrefix(p.Exchange, l.raw) || eqOrPrefix(p.Venue, l.raw), 0
	case "kind":
		return p.Kind == l.raw, 0
	case "industry":
		return seq(splitWords(p.Industry), l.words) >= 0, 0
	case "sic":
		return p.SIC != "" && eqOrPrefix(p.SIC, l.raw), 0
	case "state":
		return strings.EqualFold(p.State, l.raw), 0
	case "sector":
		return strings.EqualFold(p.Sector, l.raw), 0
	case "market":
		return seq(splitWords(p.Market), l.words) >= 0, 0
	case "status":
		if l.raw == "any" {
			return p.Status != "", 0
		}
		return strings.Contains(p.Status, l.raw), 0
	case "list":
		return slices.Contains(p.Lists, l.raw), 0
	case "tag":
		return slices.Contains(p.Tags, l.raw), 0
	case "watched":
		return f.Watched[p.Ticker] == l.yes, 0
	case "news":
		n := f.News[p.Ticker]
		switch l.cmp {
		case cmpGE:
			return n >= l.num, 0
		case cmpGT:
			return n > l.num, 0
		case cmpLE:
			return n <= l.num, 0
		case cmpLT:
			return n < l.num, 0
		}
		return n == l.num, 0
	}
	return false, 0
}

// eqOrPrefix compares without case; a trailing * in want makes it a prefix.
func eqOrPrefix(have, want string) bool {
	if have == "" {
		return false
	}
	have = strings.ToLower(have)
	if p, ok := strings.CutSuffix(want, "*"); ok {
		return strings.HasPrefix(have, p)
	}
	return have == want
}

func tickerIs(p *Profile, want string) bool {
	if eqOrPrefix(p.Ticker, want) {
		return true
	}
	// BRK.B finds BRK-B.
	return !strings.HasSuffix(want, "*") && strings.EqualFold(p.Ticker, strings.ToUpper(strings.NewReplacer(".", "-", "/", "-").Replace(want)))
}

// seq finds words as a run in hay, the last one as a prefix and the others
// whole. It returns the index where the run starts, or -1.
func seq(hay, words []string) int {
	if len(words) == 0 || len(hay) < len(words) {
		return -1
	}
	for i := 0; i+len(words) <= len(hay); i++ {
		ok := true
		for j, w := range words {
			h := hay[i+j]
			if j == len(words)-1 {
				ok = strings.HasPrefix(h, w)
			} else {
				ok = h == w
			}
			if !ok {
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// nameScore rates how well words match the name, the aliases or the former
// names: 700 at the start of the name, 500 inside it, 400 in an alias.
func nameScore(words []string, p *Profile) int {
	best := 0
	if i := seq(splitWords(p.Name), words); i >= 0 {
		s := 500
		if i == 0 {
			s = 700
		}
		if len(words) == len(splitWords(p.Name)) && slices.Equal(words, splitWords(p.Name)) {
			s += 100
		}
		best = s
	}
	for _, a := range p.Aliases {
		if seq(splitWords(a), words) >= 0 {
			best = max(best, 400)
		}
	}
	return best
}

func textScore(l lit, p *Profile) int {
	best := 0
	if len(l.words) == 1 || !strings.ContainsAny(l.raw, " ") {
		t := strings.ToLower(p.Ticker)
		switch {
		case t == l.raw || t == strings.NewReplacer(".", "-", "/", "-").Replace(l.raw):
			best = 1000
		case strings.HasPrefix(t, l.raw):
			best = 800
		}
	}
	best = max(best, nameScore(l.words, p))
	if best == 0 && seq(splitWords(p.Industry), l.words) >= 0 {
		best = 100
	}
	return best
}

// Hit is a profile that matched, with its rank.
type Hit struct {
	Profile *Profile
	Score   int
	News    int
}

// Find returns the profiles that match. Free text ranks them best first, then
// by news in the window, then by ticker; without free text they come in the
// order of the tickers; sort: overrides both. limit <= 0 means all.
func (u *Universe) Find(q Query, f Facts, limit int) []Hit {
	if u == nil {
		return nil
	}
	var hits []Hit
	ranked := false
	for i := range u.profiles {
		p := &u.profiles[i]
		if ok, s := q.eval(p, f); ok {
			ranked = ranked || s > 0
			hits = append(hits, Hit{Profile: p, Score: s, News: f.News[p.Ticker]})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if q.sortBy != "" {
			if c := compareBy(q.sortBy, a, b); c != 0 {
				return (c < 0) != q.desc
			}
			return a.Profile.Ticker < b.Profile.Ticker
		}
		if ranked {
			if a.Score != b.Score {
				return a.Score > b.Score
			}
			if a.News != b.News {
				return a.News > b.News
			}
		}
		return a.Profile.Ticker < b.Profile.Ticker
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// compareBy orders two hits by one sort key. News counts most-first when
// ascending, so that sort:news lists the busiest companies first.
func compareBy(key string, a, b Hit) int {
	switch key {
	case "name":
		return strings.Compare(strings.ToLower(a.Profile.Name), strings.ToLower(b.Profile.Name))
	case "news":
		return b.News - a.News
	case "industry":
		return emptyLast(strings.ToLower(a.Profile.Industry), strings.ToLower(b.Profile.Industry))
	case "exchange":
		return strings.Compare(a.Profile.Exchange, b.Profile.Exchange)
	case "kind":
		return strings.Compare(a.Profile.Kind, b.Profile.Kind)
	}
	return strings.Compare(a.Profile.Ticker, b.Profile.Ticker)
}

// emptyLast compares two strings, an empty one after every other.
func emptyLast(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	return strings.Compare(a, b)
}

// FilterEnv gives package filter the operators that turn a company, a list, a
// tag, an exchange or an industry into the tickers of an article:
//
//	company:apple  list:compounders  tag:moat  exchange:nyse  industry:software
//	sic:73*  state:de  cik:320193
//
// "ticker:" stays the filter's own. list: reports an unknown list, naming the
// lists that exist; tag: with an unknown tag simply matches nothing.
func (u *Universe) FilterEnv() filter.Env {
	env := filter.Env{Tickers: map[string]func(string) (map[string]struct{}, error){}}
	for _, op := range []string{"company", "list", "tag", "exchange", "industry", "sic", "state", "cik"} {
		env.Tickers[op] = func(value string) (map[string]struct{}, error) {
			if op == "list" {
				if _, ok := u.List(value); !ok {
					var ids []string
					for _, l := range u.Lists() {
						ids = append(ids, l.ID)
					}
					if len(ids) == 0 {
						return nil, fmt.Errorf("there is no list %q (you have no lists yet)", value)
					}
					return nil, fmt.Errorf("there is no list %q; your lists: %s", value, strings.Join(ids, ", "))
				}
			}
			l, err := opLit(op, value, 0, 0)
			if err != nil {
				return nil, err
			}
			set := map[string]struct{}{}
			for i := range u.profiles {
				if ok, _ := l.eval(&u.profiles[i], Facts{}); ok {
					set[u.profiles[i].Ticker] = struct{}{}
				}
			}
			return set, nil
		}
	}
	return env
}

// FactsFrom counts, for each ticker, the articles that are relevant at the
// threshold, and marks the tickers you follow. Pass the articles of the window
// the screen or the command covers.
func FactsFrom(arts []model.Article, threshold float64, watched []string) Facts {
	f := Facts{News: map[string]int{}, Watched: make(map[string]bool, len(watched))}
	for _, w := range watched {
		f.Watched[Ticker(w)] = true
	}
	for _, a := range arts {
		if !a.Score.Relevant(threshold) {
			continue
		}
		seen := map[string]bool{}
		for _, t := range a.Tickers {
			if t = Ticker(t); t != "" && !seen[t] {
				seen[t] = true
				f.News[t]++
			}
		}
	}
	return f
}
