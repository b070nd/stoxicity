package valuation

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// State is where an issuer's capitalisation stands. Exactly one applies.
type State string

const (
	// StateValued: every class has a value, so the issuer has one.
	StateValued State = "valued"
	// StateStale: a count or a price is too old to be used.
	StateStale State = "stale"
	// StateIncomplete: something is missing that more data would supply: a class
	// without a price, a class not listed, a mapping not yet made, a split list that
	// does not reach.
	StateIncomplete State = "incomplete"
	// StateUnresolved: the evidence does not decide: a class cannot be told from
	// the issuer's other listings, or the filing contradicts itself.
	StateUnresolved State = "unresolved"
	// StateUnsupported: the method cannot value it: a depositary receipt, a foreign
	// currency without a rate, an issuer whose reports have no machine-readable
	// cover page.
	StateUnsupported State = "unsupported"
	// StateFailed: the last attempt to read the issuer's cover page failed.
	StateFailed State = "failed"
	// StatePending: the cover page has not been read yet.
	StatePending State = "pending"
)

// States in the order they are listed.
var States = []State{StateValued, StateStale, StateIncomplete, StateUnresolved, StateUnsupported, StateFailed, StatePending}

// stateOf maps a class's code to the state it puts the issuer in.
func stateOf(c Code) State {
	switch c {
	case CodeStaleCount, CodeStalePrice:
		return StateStale
	case CodeAmbiguous, CodeConflict:
		return StateUnresolved
	case CodeReceipt, CodeCurrency, CodeBasis:
		return StateUnsupported
	}
	return StateIncomplete
}

// severity orders the states an issuer's classes can be in, worst first: what
// stops the value for good comes before what a later reading may fix.
func (s State) severity() int {
	switch s {
	case StateUnresolved:
		return 0
	case StateUnsupported:
		return 1
	case StateStale:
		return 2
	case StateIncomplete:
		return 3
	}
	return 4
}

// Check is where the reading of an issuer's cover page stands (the keeper's
// record, given here so that this package reads no store).
type Check struct {
	At       time.Time
	Problem  string
	Kind     string // "", "failed" or "nocover"
	Failures int
	Next     time.Time
}

// Standing is one issuer's capitalisation and the reason when it has none.
type Standing struct {
	CIK    string
	Ticker string
	State  State
	// Reason says why the state is not valued; empty when valued.
	Reason string
	// Classes counts the classes the report states, Valued those with a value,
	// Mapped those tied to a security.
	Classes, Valued, Mapped int
	// CountDay is the newest count date and PriceDay the newest price session among
	// the classes; empty when there is none.
	CountDay, PriceDay string
	// Followed says the user follows the issuer (it comes first in the walk).
	Followed bool
	// Failures and Next are the persisted retry state; RefreshFailing says the last
	// attempt failed even though older counts may be shown.
	Failures       int
	Next           time.Time
	RefreshFailing bool
	RefreshProblem string
}

// Tally counts issuers by state.
type Tally struct {
	Issuers int
	// Checked have had their cover page looked at, Mapped have every class tied to
	// a security.
	Checked, Mapped int
	ByState         map[State]int
	// Retrying are issuers whose last look failed and that wait for their retry time.
	Retrying int
}

func (t *Tally) add(s Standing, checked bool) {
	t.Issuers++
	if t.ByState == nil {
		t.ByState = map[State]int{}
	}
	t.ByState[s.State]++
	if checked {
		t.Checked++
	}
	if s.Classes > 0 && s.Mapped == s.Classes {
		t.Mapped++
	}
	if s.RefreshFailing {
		t.Retrying++
	}
}

// Coverage is the standing of every issuer, counted overall and for the issuers
// the user follows.
type Coverage struct {
	All, Followed Tally
	Issuers       []Standing
}

// Measure classifies every issuer with a SEC number that has common stock or
// depositary receipts in the list. Every issuer has exactly one state, so the
// counts add up to the number of issuers, and every state other than valued has
// a reason. followed holds the SEC numbers of the issuers the user follows.
func Measure(profiles []universe.Profile, set Set, checks map[string]Check, followed map[string]bool, now time.Time) Coverage {
	var cov Coverage
	seen := map[string]bool{}
	for _, p := range profiles {
		if p.CIK == "" || seen[p.CIK] || (p.Type() != universe.TypeStock && p.Type() != universe.TypeADR) {
			continue
		}
		seen[p.CIK] = true
		st := standing(p, set, checks[p.CIK], hasCheck(checks, p.CIK), now)
		st.Followed = followed[p.CIK]
		cov.Issuers = append(cov.Issuers, st)
		_, checked := checks[p.CIK]
		cov.All.add(st, checked)
		if st.Followed {
			cov.Followed.add(st, checked)
		}
	}
	slices.SortStableFunc(cov.Issuers, func(a, b Standing) int {
		switch {
		case a.Followed != b.Followed:
			if a.Followed {
				return -1
			}
			return 1
		case a.State != b.State:
			return slices.Index(States, a.State) - slices.Index(States, b.State)
		}
		return cmp.Compare(a.Ticker, b.Ticker)
	})
	return cov
}

func hasCheck(checks map[string]Check, cik string) bool { _, ok := checks[cik]; return ok }

func standing(p universe.Profile, set Set, ck Check, checked bool, now time.Time) Standing {
	st := Standing{CIK: p.CIK, Ticker: p.Ticker, Failures: ck.Failures, Next: ck.Next}
	if checked && ck.Kind == "failed" {
		st.RefreshFailing, st.RefreshProblem = true, ck.Problem
	}
	v, ok := set.ByCIK[p.CIK]
	if !ok {
		switch {
		case !checked:
			st.State, st.Reason = StatePending, "the cover page of the company's latest report has not been read yet"
		case ck.Kind == "nocover":
			st.State, st.Reason = StateUnsupported, ck.Problem
		case ck.Kind == "failed":
			st.State = StateFailed
			st.Reason = fmt.Sprintf("the last attempt failed (%d in a row): %s", max(ck.Failures, 1), ck.Problem)
			if !ck.Next.IsZero() {
				st.Reason += "; next try " + ck.Next.UTC().Format("2006-01-02 15:04Z")
			}
		default:
			st.State, st.Reason = StateIncomplete, "the cover page was read and states no share count"
		}
		return st
	}
	st.Classes = len(v.Classes)
	var worst *ClassValue
	for i := range v.Classes {
		c := &v.Classes[i]
		if c.Mapping.Status == Mapped {
			st.Mapped++
		}
		if c.OK {
			st.Valued++
		}
		if c.AsOf > st.CountDay {
			st.CountDay = c.AsOf
		}
		if c.Price.Day > st.PriceDay && c.Price.Close > 0 {
			st.PriceDay = c.Price.Day
		}
		if !c.OK && (worst == nil || stateOf(c.Code).severity() < stateOf(worst.Code).severity()) {
			worst = c
		}
	}
	switch {
	case v.IssuerOK:
		st.State = StateValued
	case worst != nil:
		st.State = stateOf(worst.Code)
		st.Reason = v.IssuerReason
	default: // a class the report before stated and this one does not
		st.State, st.Reason = StateIncomplete, v.IssuerReason
	}
	if st.RefreshFailing {
		st.Reason = strings.TrimSpace(st.Reason + "; the last refresh of the cover page failed: " + st.RefreshProblem)
		st.Reason = strings.TrimPrefix(st.Reason, "; ")
	}
	return st
}

// FigureStanding is what a list shows next to a capitalisation: the state in a
// word, whether the figure is the issuer's or one class's, and the dates of the
// count and the price it rests on.
type FigureStanding struct {
	State    State
	Reason   string
	CountDay string
	PriceDay string
	// Scope is "issuer" for the capitalisation of every class together on a
	// security that is one of several classes, "" for an issuer with one class.
	Scope    string
	Currency string
}

// Standings gives, by ticker, the standing of the capitalisation of each common
// stock and receipt of an issuer with a SEC number: what the figure is and, when
// it is missing, why in a word. It reads what is stored; no request is made.
func Standings(profiles []universe.Profile, set Set, checks map[string]Check, now time.Time) map[string]FigureStanding {
	cov := Measure(profiles, set, checks, nil, now)
	byCIK := map[string]Standing{}
	for _, s := range cov.Issuers {
		byCIK[s.CIK] = s
	}
	out := map[string]FigureStanding{}
	for _, p := range profiles {
		if p.CIK == "" || (p.Type() != universe.TypeStock && p.Type() != universe.TypeADR) {
			continue
		}
		s, ok := byCIK[p.CIK]
		if !ok {
			continue
		}
		fs := FigureStanding{State: s.State, Reason: s.Reason, CountDay: s.CountDay, PriceDay: s.PriceDay, Currency: set.ByCIK[p.CIK].Currency}
		if s.Classes > 1 || len(p.Siblings) > 0 {
			fs.Scope = "issuer"
		}
		// A security that is not one of the report's classes has no figure of its own.
		if v, ok := set.ByCIK[p.CIK]; ok {
			listed := false
			for _, c := range v.Classes {
				if c.Mapping.Status == Mapped && marketdata.SameTicker(c.Mapping.Ticker, p.Ticker) {
					listed = true
				}
			}
			if !listed {
				fs.State, fs.Reason = StateIncomplete, fmt.Sprintf("%s is not mapped to a class on the cover page of %s", p.Ticker, v.Accession)
			}
		}
		out[p.Ticker] = fs
	}
	return out
}
