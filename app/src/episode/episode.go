// Package episode decides which events of one security are the same thing
// happening, seen through several documents, and which are not.
//
// A security's events are not episodes just because they have the same kind and
// fall within a few hours of each other: two acquisitions announced on one
// Tuesday are two episodes. Kind and proximity (Window) only make two events
// candidates. A pair is then decided by evidence, in this order:
//
//  1. proof that they are the same: a shared article, a shared filing, one
//     article a copy of the other's, or a report that rests on the other's filing
//     by its accession number;
//  2. proof that they differ: different effective days, different counterparty
//     securities, or different counterparties named in the titles;
//  3. a probable link: a filing and a company release of the same kind in the
//     window with nothing that tells them apart, or coverage that says it rests
//     on a statement and names the same counterparty. A probable link is shown
//     as such, and it is dropped when it is ambiguous (more than one partner);
//  4. otherwise undecided, which counts as separate: nothing links them.
//
// Every decision states its basis, method and confidence, so it can be stored
// and read back. Nothing here changes an event or an observation.
package episode

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/origin"
)

// Version names the rules; stored with every decision.
const Version = "episode-1"

// Window is how close in time two events of one kind must be to be candidates
// for one episode. It is a candidate filter, not evidence.
const Window = 72 * time.Hour

// Verdict is what a decision says.
type Verdict string

const (
	Same      Verdict = "same"
	Distinct  Verdict = "distinct"
	Undecided Verdict = "undecided"
)

// Methods of a decision.
const (
	MethodSharedArticle  = "shared-article"
	MethodSharedFiling   = "shared-filing"
	MethodCopy           = "copy"
	MethodFilingRef      = "filing-reference"
	MethodEffectiveDay   = "effective-day"
	MethodCounterparties = "counterparties"
	MethodTitleParties   = "title-parties"
	MethodFilingRelease  = "filing-and-release"
	MethodStatementRef   = "statement-and-parties"
	MethodAmbiguous      = "ambiguous"
	MethodNothingLinks   = "nothing-links"
	MethodWindow         = "window"
	MethodKind           = "kind"
)

// Filing is a filing among an event's evidence.
type Filing struct {
	Accession, Form string
	Items           []string
}

// Event is what the grouping reads of one event.
type Event struct {
	ID             string
	Kind           string
	Title          string
	PublicAt       time.Time
	EffectiveDay   string
	Counterparties []string // security IDs
	Filings        []Filing
	Articles       []string
	// Origins are the origins of the articles by ID; an article without one
	// is unassessed.
	Origins map[string]origin.Origin
}

// Decision is a stated verdict on a pair of events.
type Decision struct {
	A, B       string // A < B
	Verdict    Verdict
	Basis      string
	Method     string
	Confidence float64
	Probable   bool // a verdict on a probable link rather than on proof
}

func (d Decision) key() [2]string { return [2]string{d.A, d.B} }

// Key is the pair a decision is about.
func Key(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// Candidate reports whether two events are candidates for one episode: the same
// kind, and publication times within Window.
func Candidate(a, b Event) bool {
	if a.Kind != b.Kind {
		return false
	}
	d := a.PublicAt.Sub(b.PublicAt)
	return d <= Window && -d <= Window
}

func has(set []string, x string) bool { return slices.Contains(set, x) }

// Decide judges one pair of events that are candidates (see Candidate). It does
// not read the other events of the security; ambiguity among partners is judged
// by Decisions.
func Decide(a, b Event) Decision {
	if a.ID > b.ID {
		a, b = b, a
	}
	d := Decision{A: a.ID, B: b.ID}
	same := func(method, basis string, conf float64) Decision {
		d.Verdict, d.Method, d.Basis, d.Confidence = Same, method, basis, conf
		return d
	}
	apart := func(method, basis string, conf float64) Decision {
		d.Verdict, d.Method, d.Basis, d.Confidence = Distinct, method, basis, conf
		return d
	}

	// 1. proof of sameness
	for _, id := range a.Articles {
		if has(b.Articles, id) {
			return same(MethodSharedArticle, "both rest on article "+id, 1)
		}
	}
	for _, fa := range a.Filings {
		for _, fb := range b.Filings {
			if fa.Accession == fb.Accession {
				return same(MethodSharedFiling, "both rest on filing "+fa.Accession, 1)
			}
		}
	}
	for _, x := range []struct{ from, to Event }{{a, b}, {b, a}} {
		for _, id := range x.from.Articles {
			o, ok := x.from.Origins[id]
			if !ok {
				continue
			}
			if o.CopyOf != "" && has(x.to.Articles, o.CopyOf) {
				return same(MethodCopy, fmt.Sprintf("article %s copies article %s of the other event", id, o.CopyOf), 0.95)
			}
			for _, f := range x.to.Filings {
				if o.DerivedFrom == "primary:"+f.Accession {
					return same(MethodFilingRef, fmt.Sprintf("article %s rests on filing %s", id, f.Accession), 0.9)
				}
			}
		}
	}

	// 2. proof of difference
	if a.EffectiveDay != "" && b.EffectiveDay != "" && a.EffectiveDay != b.EffectiveDay {
		return apart(MethodEffectiveDay, fmt.Sprintf("effective %s and %s", a.EffectiveDay, b.EffectiveDay), 0.9)
	}
	if len(a.Counterparties) > 0 && len(b.Counterparties) > 0 && disjoint(a.Counterparties, b.Counterparties) {
		return apart(MethodCounterparties, "different counterparty securities", 0.9)
	}
	pa, pb := Parties(a.Title), Parties(b.Title)
	if len(pa) > 0 && len(pb) > 0 && disjoint(pa, pb) {
		return apart(MethodTitleParties, fmt.Sprintf("titles name %s and %s", strings.Join(pa, "/"), strings.Join(pb, "/")), 0.7)
	}

	// 3. probable link
	if (ownFiling(a) && ownStatement(b)) || (ownFiling(b) && ownStatement(a)) {
		d.Probable = true
		return same(MethodFilingRelease, "a filing and a company release of one kind within 72 hours, nothing tells them apart", 0.6)
	}
	if len(pa) > 0 && len(pb) > 0 && !disjoint(pa, pb) && (restsOnStatement(a) || restsOnStatement(b)) {
		d.Probable = true
		return same(MethodStatementRef, "coverage rests on a statement and names the same counterparty", 0.6)
	}
	d.Verdict, d.Method, d.Basis, d.Confidence = Undecided, MethodNothingLinks, "same kind within 72 hours, nothing links them", 0
	return d
}

func disjoint(a, b []string) bool {
	for _, x := range a {
		if has(b, x) {
			return false
		}
	}
	return true
}

// ownFiling: the event rests on a filing.
func ownFiling(e Event) bool { return len(e.Filings) > 0 }

// ownStatement: the event rests on the company's own release and on no filing.
func ownStatement(e Event) bool {
	if len(e.Filings) > 0 {
		return false
	}
	for _, id := range e.Articles {
		if o, ok := e.Origins[id]; ok && o.Kind == origin.KindIssuer {
			return true
		}
	}
	return false
}

func restsOnStatement(e Event) bool {
	for _, id := range e.Articles {
		if o, ok := e.Origins[id]; ok && o.DerivedFrom != "" {
			return true
		}
	}
	return false
}

// Decisions decides every candidate pair among the events of one security, and
// every pair that facts keep apart. A probable link that is ambiguous (an event
// with more than one probable partner of the other class) is not taken: it is
// undecided, with the count of partners as its basis.
func Decisions(events []Event) []Decision {
	var out []Decision
	for i := range events {
		for j := i + 1; j < len(events); j++ {
			if !Candidate(events[i], events[j]) {
				continue
			}
			out = append(out, Decide(events[i], events[j]))
		}
	}
	// A probable link is ambiguous when an event has several probable partners
	// by the same kind of link (a filing that could belong to either of two
	// releases).
	type slot struct{ id, method string }
	partners := map[slot]int{}
	for _, d := range out {
		if d.Probable {
			partners[slot{d.A, d.Method}]++
			partners[slot{d.B, d.Method}]++
		}
	}
	for i, d := range out {
		if d.Probable && (partners[slot{d.A, d.Method}] > 1 || partners[slot{d.B, d.Method}] > 1) {
			n := max(partners[slot{d.A, d.Method}], partners[slot{d.B, d.Method}])
			out[i].Verdict, out[i].Method, out[i].Probable, out[i].Confidence = Undecided, MethodAmbiguous, false, 0
			out[i].Basis = fmt.Sprintf("a probable link, but one event has %d probable partners", n)
		}
	}
	slices.SortFunc(out, func(x, y Decision) int { return cmp.Or(cmp.Compare(x.A, y.A), cmp.Compare(x.B, y.B)) })
	return out
}

// Episode is a set of events taken to be one thing happening.
type Episode struct {
	Events []string // oldest first
	Kind   string
	Start  time.Time
	// Basis states why the events are joined, one line per joining decision.
	Basis []string
	// Probable is set when a joining decision rests on a probable link.
	Probable bool
	// Undecided counts candidate pairs inside the episode's neighbourhood that
	// nothing linked: they stay separate episodes.
	Undecided int
}

// Group joins events by the decisions: a pair decided Same is one episode, and
// so, through it, are the events it connects. Pairs that are Distinct or
// Undecided are not joined. An event with no decision is its own episode. If a
// Distinct decision falls inside a joined set, the join stands and the conflict
// is stated in the basis.
func Group(events []Event, decisions []Decision) []Episode {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" || parent[x] == x {
			parent[x] = x
			return x
		}
		parent[x] = find(parent[x])
		return parent[x]
	}
	for _, e := range events {
		find(e.ID)
	}
	for _, d := range decisions {
		if d.Verdict == Same {
			parent[find(d.A)] = find(d.B)
		}
	}
	byRoot := map[string]*Episode{}
	var order []string
	byID := map[string]Event{}
	for _, e := range events {
		byID[e.ID] = e
	}
	sorted := slices.Clone(events)
	slices.SortFunc(sorted, func(a, b Event) int { return cmp.Or(a.PublicAt.Compare(b.PublicAt), cmp.Compare(a.ID, b.ID)) })
	for _, e := range sorted {
		r := find(e.ID)
		ep := byRoot[r]
		if ep == nil {
			ep = &Episode{Kind: e.Kind, Start: e.PublicAt}
			byRoot[r] = ep
			order = append(order, r)
		}
		ep.Events = append(ep.Events, e.ID)
	}
	for _, d := range decisions {
		ep := byRoot[find(d.A)]
		switch {
		case d.Verdict == Same:
			ep.Basis = append(ep.Basis, fmt.Sprintf("%s + %s: %s", short(d.A), short(d.B), d.Basis))
			ep.Probable = ep.Probable || d.Probable
		case d.Verdict == Distinct && find(d.A) == find(d.B):
			ep.Basis = append(ep.Basis, fmt.Sprintf("conflict kept: %s and %s were decided distinct (%s) but are joined through other events", short(d.A), short(d.B), d.Basis))
		case d.Verdict == Undecided:
			ep.Undecided++
		}
	}
	out := make([]Episode, 0, len(order))
	for _, r := range order {
		out = append(out, *byRoot[r])
	}
	return out
}

func short(id string) string { return strings.TrimPrefix(id, "evt_") }

var companySuffix = map[string]bool{"inc": true, "corp": true, "co": true, "ltd": true, "plc": true, "llc": true, "sa": true, "ag": true, "nv": true, "group": true, "holdings": true, "the": true}

// Parties reads the counterparty a title names after an acquisition verb ("to
// buy Bolt", "acquires Cog Industries"), as a lowercase first word. It returns
// nothing when the title names none; a missing party is never evidence.
func Parties(title string) []string {
	low := strings.ToLower(title)
	verbs := []string{"to buy ", "to acquire ", "to purchase ", "buys ", "acquires ", "acquiring ", "buying ", "acquisition of ", "merger with ", "merge with ", "takeover of ", "takes over ", "bid for ", "offer for ", "sells "}
	best := -1
	var after string
	for _, v := range verbs {
		if i := strings.Index(low, v); i >= 0 && (best < 0 || i < best) {
			best, after = i, title[i+len(v):]
		}
	}
	if best < 0 {
		return nil
	}
	for _, w := range strings.Fields(after) {
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if w == "" {
			continue
		}
		if companySuffix[strings.ToLower(w)] {
			continue
		}
		if r := []rune(w)[0]; unicode.IsUpper(r) || unicode.IsDigit(r) {
			return []string{strings.ToLower(w)}
		}
		return nil
	}
	return nil
}
