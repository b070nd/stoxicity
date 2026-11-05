package filter

import (
	"slices"
	"strings"
)

// Env adds operators that depend on data this package does not hold, such as
// the list of companies. The parser stays free of that data: each extra
// operator turns its value into a set of tickers while the expression is
// parsed, and an article matches when it names any ticker of the set. A
// Query therefore stays a plain value that can be matched anywhere.
//
// The zero Env adds nothing.
type Env struct {
	// Tickers maps an operator name (lower case) to the function that turns the
	// operator's value (lower case, trimmed, never empty) into upper-case
	// tickers. An error is reported as a syntax error at the value. A name that
	// is also a built-in operator replaces it.
	Tickers map[string]func(value string) (map[string]struct{}, error)
}

// names lists every known operator for an error message.
func (e Env) names() []string {
	all := append([]string(nil), operatorNames...)
	for name := range e.Tickers {
		if !slices.Contains(all, name) {
			all = append(all, name)
		}
	}
	slices.Sort(all)
	return all
}

// setOp is an operator whose value stands for a set of tickers.
type setOp struct {
	name, val string
	set       map[string]struct{}
}

func (n setOp) matches(c *matchCtx) bool {
	for _, t := range c.a.Tickers {
		if _, ok := n.set[strings.ToUpper(strings.TrimSpace(t))]; ok {
			return true
		}
	}
	return false
}
