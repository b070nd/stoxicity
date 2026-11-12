// Package playbook is the versioned signal engine. A playbook is a set of
// predefined rules. A rule reads one new event and the stored research and
// answers buy, sell or watch, with every test shown.
//
// A rule may ask for a buy or a sell only when the research holds an
// "evaluated" validation group that supports it: enough comparable events and
// a mean whose bound lies beyond the stated edge, in the stated direction.
// Otherwise the answer is watch. A test that cannot be told is "unknown" and
// also keeps the answer at watch.
//
// Nothing here approves or executes anything. A signal is a request. A
// separate risk layer, behind the Gate interface, must approve it, and a watch
// signal is never even offered to it. The package imports no ordering,
// broker, portfolio or policy code: it cannot place an order.
//
// The package is pure and deterministic: the same playbook, event and
// research give the same signals, with the same identifiers.
package playbook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
)

// Version names the signal rules.
const Version = "playbook-1"

// Action is what a signal asks for.
type Action string

// The actions. Watch is what a rule falls back to when its evidence is not
// enough; it is never offered to the risk layer.
const (
	Buy   Action = "buy"
	Sell  Action = "sell"
	Watch Action = "watch"
)

// Evidence is what the research must show before a rule may ask for a trade.
type Evidence struct {
	Sessions  int     `json:"sessions"`            // the holding period whose group is read
	Dimension string  `json:"dimension,omitempty"` // "" or "all": every event of the type; otherwise one of eventstudy.Dimensions(), and the group of the event's own bucket is read
	MinEvents int     `json:"min_events"`
	Direction string  `json:"direction"` // "up" or "down"
	MinEdge   float64 `json:"min_edge"`  // how far beyond zero the bound of the mean must lie, as a return (0.01 is 1%)
}

// Rule is one predefined rule: which events it reads, which conditions the
// event must meet, what the research must show, and what it asks for.
type Rule struct {
	ID       string                 `json:"id"`
	Note     string                 `json:"note,omitempty"`
	Types    []string               `json:"types"`
	When     []eventstudy.Condition `json:"when,omitempty"`
	Evidence Evidence               `json:"evidence"`
	Action   Action                 `json:"action"` // buy or sell; watch is what a rule falls back to, not what it asks for
}

// Playbook is a named, versioned set of rules that reads the report of one
// study.
type Playbook struct {
	ID    string `json:"id"`
	Note  string `json:"note,omitempty"`
	Study string `json:"study"` // the ID of the eventstudy.Spec whose report the rules read
	Rules []Rule `json:"rules"`
}

// Validate says why the playbook cannot be used.
func (p Playbook) Validate() error {
	switch {
	case p.ID == "":
		return errors.New("playbook: the playbook needs an id")
	case p.Study == "":
		return fmt.Errorf("playbook: %s names no study", p.ID)
	case len(p.Rules) == 0:
		return fmt.Errorf("playbook: %s has no rule", p.ID)
	}
	seen := map[string]bool{}
	for i, r := range p.Rules {
		if r.ID == "" {
			return fmt.Errorf("playbook: rule %d has no id", i+1)
		}
		if seen[r.ID] {
			return fmt.Errorf("playbook: rule %s is given twice", r.ID)
		}
		seen[r.ID] = true
		if err := r.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (r Rule) validate() error {
	if len(r.Types) == 0 {
		return fmt.Errorf("playbook: rule %s names no event type", r.ID)
	}
	if r.Action != Buy && r.Action != Sell {
		return fmt.Errorf("playbook: rule %s asks for %q, not buy or sell", r.ID, r.Action)
	}
	for _, c := range r.When {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("playbook: rule %s: %w", r.ID, err)
		}
	}
	e := r.Evidence
	switch {
	case e.Sessions < 1:
		return fmt.Errorf("playbook: rule %s needs a holding period of at least 1 session", r.ID)
	case e.MinEvents < 2:
		return fmt.Errorf("playbook: rule %s needs at least 2 events", r.ID)
	case e.MinEdge < 0:
		return fmt.Errorf("playbook: rule %s has a negative edge", r.ID)
	case e.Dimension != "" && e.Dimension != "all" && !isDimension(e.Dimension):
		return fmt.Errorf("playbook: rule %s: dimension %q is not all or a known condition", r.ID, e.Dimension)
	case e.Direction != "up" && e.Direction != "down":
		return fmt.Errorf("playbook: rule %s: direction %q is not up or down", r.ID, e.Direction)
	case r.Action == Buy && e.Direction != "up":
		return fmt.Errorf("playbook: rule %s buys but reads a direction of %s", r.ID, e.Direction)
	case r.Action == Sell && e.Direction != "down":
		return fmt.Errorf("playbook: rule %s sells but reads a direction of %s", r.ID, e.Direction)
	}
	return nil
}

func isDimension(name string) bool {
	for _, d := range eventstudy.Dimensions() {
		if d == name {
			return true
		}
	}
	return false
}

// Hash is the sha256 digest, in hex, of the JSON encoding of the playbook.
func (p Playbook) Hash() string {
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Result of one test.
type Result string

// The results. Unknown means the test could not be told, which is never taken
// for a pass.
const (
	Pass    Result = "pass"
	Fail    Result = "fail"
	Unknown Result = "unknown"
)

// Test is one published check behind a signal.
type Test struct {
	Name   string `json:"name"`
	Result Result `json:"result"`
	Detail string `json:"detail"`
}

// Signal is the answer of one rule to one event. It is a request, not an
// order: Action is what the research supports, and a separate risk layer must
// still approve it.
type Signal struct {
	ID           string            `json:"id"`
	Playbook     string            `json:"playbook"`
	PlaybookHash string            `json:"playbook_hash"`
	Rule         string            `json:"rule"`
	Observation  string            `json:"observation"`
	Security     string            `json:"security"`
	Type         string            `json:"type"`
	AvailableAt  time.Time         `json:"available_at"`
	Requested    Action            `json:"requested"`
	Action       Action            `json:"action"`
	Sessions     int               `json:"sessions"`
	Entry        eventstudy.Entry  `json:"entry"`
	Tests        []Test            `json:"tests"`
	Comparable   *eventstudy.Group `json:"comparable,omitempty"`  // the validation group the decision rests on
	Development  *eventstudy.Group `json:"development,omitempty"` // the same group in development, for comparison only
	Study        string            `json:"study"`
	SpecHash     string            `json:"spec_hash"`
	DataHash     string            `json:"data_hash"`
	Explanation  string            `json:"explanation"`
}

// Evaluate applies every rule of the playbook to one event, in rule order. A
// rule that does not read the event's type, or whose conditions are known not
// to hold, gives no signal. It returns an error only when the playbook does not
// validate.
func Evaluate(p Playbook, o eventstudy.Observation, ctx eventstudy.Context, research eventstudy.Report) ([]Signal, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	hash := p.Hash()
	var out []Signal
	for _, r := range p.Rules {
		if s, ok := r.apply(p, hash, o, ctx, research); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r Rule) apply(p Playbook, hash string, o eventstudy.Observation, ctx eventstudy.Context, research eventstudy.Report) (Signal, bool) {
	matches := false
	for _, t := range r.Types {
		if t == o.Type {
			matches = true
		}
	}
	if !matches {
		return Signal{}, false
	}
	var tests []Test
	for _, c := range r.When {
		holds, known := c.Holds(o, ctx)
		switch {
		case !known:
			tests = append(tests, Test{Name: "condition", Result: Unknown, Detail: "cannot be told whether " + c.String()})
		case !holds:
			return Signal{}, false
		default:
			tests = append(tests, Test{Name: "condition", Result: Pass, Detail: c.String()})
		}
	}

	e := r.Evidence
	// The study the research comes from.
	switch {
	case research.Spec.ID != p.Study:
		tests = append(tests, Test{Name: "study", Result: Fail, Detail: fmt.Sprintf("the research is of study %q, the playbook reads %q", research.Spec.ID, p.Study)})
	case research.Version == "":
		tests = append(tests, Test{Name: "study", Result: Fail, Detail: "the research carries no version"})
	default:
		tests = append(tests, Test{Name: "study", Result: Pass, Detail: fmt.Sprintf("research of study %s, %s", research.Spec.ID, research.Version)})
	}

	// The rule may not narrow the events further than the study did: the
	// research would then describe a wider set than the one the rule fires on,
	// and a subset picked after the fact is how a pattern is invented.
	var narrower []string
	for _, c := range r.When {
		covered := c.Field == "context:"+e.Dimension
		for _, w := range research.Spec.Where {
			if w == c {
				covered = true
			}
		}
		if !covered {
			narrower = append(narrower, c.String())
		}
	}
	if len(narrower) > 0 {
		tests = append(tests, Test{Name: "population", Result: Fail, Detail: "the study did not select on " + strings.Join(narrower, "; ") + ": its figures describe a wider set of events than this rule fires on"})
	} else {
		tests = append(tests, Test{Name: "population", Result: Pass, Detail: "the rule fires on the events the study measured"})
	}

	dimension, bucket := "all", "all"
	bucketKnown := true
	if e.Dimension != "" && e.Dimension != "all" {
		dimension, bucket = e.Dimension, ctx.Bucket(e.Dimension)
		bucketKnown = bucket != "" && bucket != eventstudy.Unknown
	}
	scope := fmt.Sprintf("%s events held %d sessions", o.Type, e.Sessions)
	if dimension != "all" {
		scope += fmt.Sprintf(" with %s %s", dimension, bucket)
	}

	var group eventstudy.Group
	found := false
	if bucketKnown {
		group, found = research.Find(o.Type, e.Sessions, eventstudy.Validation, dimension, bucket)
	}
	var dev *eventstudy.Group
	if bucketKnown {
		if g, ok := research.Find(o.Type, e.Sessions, eventstudy.Development, dimension, bucket); ok {
			dev = &g
		}
	}
	switch {
	case !bucketKnown:
		tests = append(tests, Test{Name: "comparable", Result: Unknown, Detail: fmt.Sprintf("the %s of this event is not known", dimension)})
	case !found:
		tests = append(tests, Test{Name: "comparable", Result: Fail, Detail: "the research has no validation group of " + scope})
	default:
		tests = append(tests, Test{Name: "comparable", Result: Pass, Detail: "validation group of " + scope})
	}

	unread := func(name string) Test {
		return Test{Name: name, Result: Unknown, Detail: "there is no comparable group to read"}
	}
	var primary eventstudy.Stats
	measure := "after costs"
	if found {
		primary = group.Primary()
		if group.VsMarket != nil {
			measure = "against the market"
		}
		if group.Status == eventstudy.Evaluated {
			tests = append(tests, Test{Name: "evaluated", Result: Pass, Detail: "the group is evaluated"})
		} else {
			d := fmt.Sprintf("the group is %s", group.Status)
			if group.Why != "" {
				d += ": " + group.Why
			}
			tests = append(tests, Test{Name: "evaluated", Result: Fail, Detail: d})
		}
		n := Test{Name: "events", Result: Pass, Detail: fmt.Sprintf("%d comparable events, at least %d needed", primary.N, e.MinEvents)}
		if primary.N < e.MinEvents {
			n.Result = Fail
		}
		tests = append(tests, n)
		tests = append(tests, edgeTest(e, primary, measure))
	} else {
		tests = append(tests, unread("evaluated"), unread("events"), unread("edge"))
	}

	action := Watch
	allPass := true
	for _, t := range tests {
		if t.Result != Pass {
			allPass = false
		}
	}
	if allPass {
		action = r.Action
	}
	s := Signal{
		Playbook: p.ID, PlaybookHash: hash, Rule: r.ID,
		Observation: o.ID, Security: o.Security, Type: o.Type, AvailableAt: o.AvailableAt,
		Requested: r.Action, Action: action,
		Sessions: e.Sessions, Entry: research.Spec.Entry, Tests: tests,
		Development: dev,
		Study:       research.Spec.ID, SpecHash: research.SpecHash, DataHash: research.DataHash,
	}
	if found {
		s.Comparable = &group
	}
	s.ID = signalID(s)
	s.Explanation = explain(r, s, primary, measure, scope, found)
	return s, true
}

func edgeTest(e Evidence, st eventstudy.Stats, measure string) Test {
	var bound float64
	var name string
	var ok bool
	if e.Direction == "up" {
		bound, name, ok = st.Lower, "lower", st.Lower > e.MinEdge
	} else {
		bound, name, ok = st.Upper, "upper", st.Upper < -e.MinEdge
	}
	want := fmt.Sprintf("above %s", pct(e.MinEdge))
	if e.Direction == "down" {
		want = fmt.Sprintf("below %s", pct(-e.MinEdge))
	}
	t := Test{Name: "edge", Result: Fail, Detail: fmt.Sprintf("the %s bound of the mean is %s (mean %s) %s, needed %s", name, pct(bound), pct(st.Mean), measure, want)}
	if ok {
		t.Result = Pass
	}
	return t
}

func pct(v float64) string { return fmt.Sprintf("%.2f%%", v*100) }

func signalID(s Signal) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{s.PlaybookHash, s.Rule, s.Observation, s.SpecHash, s.DataHash}, "\x00")))
	return "sig_" + hex.EncodeToString(sum[:])[:16]
}

func explain(r Rule, s Signal, st eventstudy.Stats, measure, scope string, found bool) string {
	var matched []string
	for _, c := range r.When {
		matched = append(matched, c.String())
	}
	head := fmt.Sprintf("Rule %s read this %s event", r.ID, s.Type)
	if len(matched) > 0 {
		head += " (" + strings.Join(matched, ", ") + ")"
	}
	if s.Action != Watch {
		return fmt.Sprintf("%s and found %d comparable events held %d sessions, with a mean of %s and a lower-to-upper range of %s to %s %s, so it requests a %s for the risk layer to decide on.",
			head, st.N, s.Sessions, pct(st.Mean), pct(st.Lower), pct(st.Upper), measure, s.Action)
	}
	var kept []string
	for _, t := range s.Tests {
		if t.Result != Pass {
			kept = append(kept, fmt.Sprintf("%s was %s (%s)", t.Name, t.Result, t.Detail))
		}
	}
	return fmt.Sprintf("%s but stays at watch instead of a %s: %s.", head, r.Action, strings.Join(kept, "; "))
}

// Decision is the answer of the risk layer.
type Decision struct {
	Approved bool     `json:"approved"`
	Mode     string   `json:"mode,omitempty"` // "paper" or "real" when approved
	By       string   `json:"by"`
	Reasons  []string `json:"reasons,omitempty"`
}

// Gate is the independent risk layer. The engine never decides in its place.
type Gate interface{ Approve(s Signal) Decision }

// Approve asks the gate about a signal. A watch signal is never offered and
// never approved; with no gate nothing is approved.
func Approve(g Gate, s Signal) Decision {
	if s.Action != Buy && s.Action != Sell {
		return Decision{By: "playbook", Reasons: []string{"a watch signal is never offered for approval"}}
	}
	if g == nil {
		return Decision{By: "playbook", Reasons: []string{"there is no risk layer to approve it"}}
	}
	return g.Approve(s)
}
