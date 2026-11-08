// Package corrections keeps a plan honest about what it was built on.
//
// A plan cites the events about its security, each at the version it was read.
// When the evidence behind one of them is corrected, retracted or withdrawn,
// when a thesis rule the user recorded now matches, or when a figure the plan
// used was revised, the plan no longer says what it said. This package names
// the dependencies of a plan revision, compares them with what is stored now,
// and says what changed and whether it matters.
//
// It decides nothing about the market. A change is material only when the
// understanding of an event changed (its kind, status, day, securities or the
// claims it rests on), never because another article joined it or an article
// was fetched again. A thesis is called broken only by a rule the user recorded
// for that purpose; anything the program cannot classify with a rule is marked
// for review, and a revision that cannot be reassessed with confidence proposes
// nothing instead of defaulting to HOLD.
package corrections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// Source is the evidence store as this package reads it.
type Source interface {
	SecurityTimeline(ctx context.Context, ticker string, knownAt time.Time, limit int) ([]store.TimelineEntry, error)
	EventHistory(ctx context.Context, eventID string) ([]model.EventRevision, error)
	ArticleHeads(ctx context.Context, ids []string) (map[string]string, error)
}

var _ Source = (*store.DB)(nil)

// ArticlePin is an article that supplies a claim, at the version read.
type ArticlePin struct {
	ID   string `json:"id"`
	Head string `json:"head"`
}

// EventPin is one event at the revision a plan read.
type EventPin struct {
	EventID     string       `json:"event"`
	Revision    int          `json:"revision"`
	Kind        string       `json:"kind"`
	Role        string       `json:"role"`
	Status      string       `json:"status"`
	Title       string       `json:"title"`
	Fingerprint string       `json:"fingerprint"`
	Core        string       `json:"core"`
	Claims      []string     `json:"claims,omitempty"`
	Securities  []string     `json:"securities,omitempty"`
	Articles    []ArticlePin `json:"articles,omitempty"`
}

// Ref is how the event is named in words.
func (p EventPin) Ref() string { return fmt.Sprintf("%s r%d", p.EventID, p.Revision) }

// Supporting says whether the event is evidence the plan rests on: the
// security is what it is about, or the other side of it. A security that is
// only mentioned does not make an event a dependency.
func (p EventPin) Supporting() bool {
	return model.Role(p.Role) == model.RoleSubject || model.Role(p.Role) == model.RoleCounterparty
}

// core is what the event is: its kind, whether it stands, the day it takes
// effect and its title. A change here changes what the event says.
func core(r model.EventRevision) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{string(r.Kind), r.Status, r.EffectiveDay, r.Title}, "\x1f")))
	return hex.EncodeToString(sum[:6])
}

func claimIDs(r model.EventRevision) []string {
	var out []string
	for _, c := range r.Claims {
		out = append(out, fmt.Sprintf("%s#%d@%s/%s", c.ArticleID, c.Seq, c.Version, c.Extractor))
	}
	sort.Strings(out)
	return out
}

func securityIDs(r model.EventRevision) []string {
	var out []string
	for _, s := range r.Securities {
		out = append(out, s.SecurityID+":"+string(s.Role))
	}
	sort.Strings(out)
	return out
}

// Fingerprint names one version of an event: its core, the claims it rests on
// and the securities it is about. It labels a version for people and for a
// change's identity; whether a change matters is decided by Stands, because a
// claim added to an event is more evidence, not a correction.
func Fingerprint(r model.EventRevision) string {
	parts := append(claimIDs(r), securityIDs(r)...)
	sum := sha256.Sum256([]byte(core(r) + "\x00" + strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func subset(have, want []string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func claimArticles(r model.EventRevision) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range r.Claims {
		if !seen[c.ArticleID] {
			seen[c.ArticleID] = true
			out = append(out, c.ArticleID)
		}
	}
	sort.Strings(out)
	return out
}

func pinOf(r model.EventRevision, role model.Role, heads map[string]string) EventPin {
	p := EventPin{EventID: r.EventID, Revision: r.Revision, Kind: string(r.Kind), Role: string(role), Status: r.Status, Title: r.Title,
		Fingerprint: Fingerprint(r), Core: core(r), Claims: claimIDs(r), Securities: securityIDs(r)}
	for _, id := range claimArticles(r) {
		p.Articles = append(p.Articles, ArticlePin{ID: id, Head: heads[id]})
	}
	return p
}

// Collect reads the events about the securities with these tickers as they
// stand at the time. Withdrawn events are not in the timeline.
func Collect(ctx context.Context, src Source, tickers []string, at time.Time) ([]EventPin, error) {
	type found struct {
		role model.Role
		rev  int
	}
	seen := map[string]found{}
	for _, tk := range tickers {
		es, err := src.SecurityTimeline(ctx, tk, at, 200)
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			if cur, ok := seen[e.EventID]; !ok || e.Role.Strength() > cur.role.Strength() {
				seen[e.EventID] = found{e.Role, e.Revision}
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	revs := map[string]model.EventRevision{}
	var arts []string
	for _, id := range ids {
		hist, err := src.EventHistory(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, r := range hist {
			if r.Revision == seen[id].rev {
				revs[id] = r
				arts = append(arts, claimArticles(r)...)
			}
		}
	}
	heads, err := src.ArticleHeads(ctx, arts)
	if err != nil {
		return nil, err
	}
	out := make([]EventPin, 0, len(ids))
	for _, id := range ids {
		if r, ok := revs[id]; ok {
			out = append(out, pinOf(r, seen[id].role, heads))
		}
	}
	return out, nil
}

// Supporting keeps the pins the plan rests on.
func Supporting(pins []EventPin) []EventPin {
	var out []EventPin
	for _, p := range pins {
		if p.Supporting() {
			out = append(out, p)
		}
	}
	return out
}

// Hash identifies the versions of a set of pins. Only what a decision could
// rest on is in it, so an event that merely gained another article keeps it.
func Hash(pins []EventPin) string {
	if len(pins) == 0 {
		return ""
	}
	var parts []string
	for _, p := range pins {
		parts = append(parts, p.EventID+"@"+p.Fingerprint)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:8])
}

// Lines describe the pins for the plan card.
func Lines(pins []EventPin) []string {
	var out []string
	for _, p := range pins {
		out = append(out, fmt.Sprintf("%s (%s, %s): %s [version %s]", p.Ref(), p.Kind, p.Role, p.Title, p.Fingerprint))
	}
	sort.Strings(out)
	return out
}

// State is an event as it stands now.
type State struct {
	Revision    int
	Status      string
	Title       string
	Fingerprint string
	Core        string
	Claims      []string
	Securities  []string
	Heads       map[string]string // article -> head now
}

// Stands says whether the event still says what the plan read: its core is the
// same, every claim the plan rested on is still there and every security it
// named is still named in the same role. More claims or more securities are
// more evidence, not a correction.
func Stands(p EventPin, c State) bool {
	return c.Status != model.StatusWithdrawn && c.Core == p.Core && subset(c.Claims, p.Claims) && subset(c.Securities, p.Securities)
}

// Observe reads the events with these IDs at their newest revision, withdrawn
// ones included.
func Observe(ctx context.Context, src Source, ids []string) (map[string]State, error) {
	out := map[string]State{}
	revs := map[string]model.EventRevision{}
	var arts []string
	for _, id := range ids {
		hist, err := src.EventHistory(ctx, id)
		if err != nil {
			return nil, err
		}
		if len(hist) == 0 {
			continue
		}
		last := hist[len(hist)-1]
		revs[id] = last
		arts = append(arts, claimArticles(last)...)
	}
	heads, err := src.ArticleHeads(ctx, arts)
	if err != nil {
		return nil, err
	}
	for id, r := range revs {
		st := State{Revision: r.Revision, Status: r.Status, Title: r.Title, Fingerprint: Fingerprint(r), Core: core(r), Claims: claimIDs(r), Securities: securityIDs(r), Heads: map[string]string{}}
		for _, a := range claimArticles(r) {
			st.Heads[a] = heads[a]
		}
		out[id] = st
	}
	return out, nil
}

// CurrentHash is the hash of the pins as the events stand now, for a manifest
// to compare with the one a revision was generated with. An event that is gone
// from the store or withdrawn counts as withdrawn.
func CurrentHash(ctx context.Context, src Source, pinned []EventPin) (string, error) {
	if len(pinned) == 0 {
		return "", nil
	}
	ids := make([]string, len(pinned))
	for i, p := range pinned {
		ids[i] = p.EventID
	}
	cur, err := Observe(ctx, src, ids)
	if err != nil {
		return "", err
	}
	now := make([]EventPin, 0, len(pinned))
	for _, p := range pinned {
		c, ok := cur[p.EventID]
		switch {
		case !ok:
			p.Fingerprint = "missing"
		case c.Status == model.StatusWithdrawn:
			p.Fingerprint = "withdrawn"
		case !Stands(p, c):
			p.Fingerprint = "changed@" + c.Fingerprint
		}
		now = append(now, p)
	}
	return Hash(now), nil
}

// Effect is what a finding asks for.
type Effect string

// The effects, strongest last.
const (
	Revise Effect = "revise" // reassess and write a revision
	Review Effect = "review" // a person must look; the revision proposes nothing
	Broken Effect = "broken" // a recorded thesis rule matched; the revision proposes nothing
)

func (e Effect) rank() int {
	switch e {
	case Broken:
		return 3
	case Review:
		return 2
	}
	return 1
}

// Finding is one relevant change.
type Finding struct {
	Kind   string `json:"kind"` // retracted | corrected | source_changed | figure_revised | thesis_rule | cannot_reassess
	Ref    string `json:"ref"`
	Effect Effect `json:"effect"`
	Detail string `json:"detail"`
	// To identifies the new version, so the same change is the same finding.
	To string `json:"to"`
}

// Compare says what changed in the events a revision rests on. ignored names the
// changes that were looked at and do not matter, so a report can show that the
// program saw them.
func Compare(pinned []EventPin, cur map[string]State) (found []Finding, ignored []string) {
	for _, p := range pinned {
		c, ok := cur[p.EventID]
		switch {
		case !ok:
			found = append(found, Finding{"retracted", p.Ref(), Review, fmt.Sprintf("%s is no longer in the store: the evidence it rested on cannot be read", p.Ref()), "missing"})
			continue
		case c.Status == model.StatusWithdrawn:
			found = append(found, Finding{"retracted", p.Ref(), Revise, fmt.Sprintf("%s (%s) was withdrawn: under the rules now in force the evidence does not support it", p.Ref(), p.Title), "withdrawn"})
			continue
		case !Stands(p, c):
			what := "a claim it rested on is gone or was replaced"
			switch {
			case c.Core != p.Core && c.Status == model.StatusCorrected && p.Status != model.StatusCorrected:
				what = "it was corrected"
			case c.Core != p.Core:
				what = "what it says changed"
			case !subset(c.Securities, p.Securities):
				what = "the securities it is about, or their roles, changed"
			}
			found = append(found, Finding{"corrected", p.Ref(), Revise, fmt.Sprintf("%s (%s) now stands as revision %d: %s", p.Ref(), p.Title, c.Revision, what), c.Fingerprint})
			continue
		}
		moved := false
		for _, a := range p.Articles {
			if c.Heads[a.ID] != a.Head {
				found = append(found, Finding{"source_changed", p.Ref(), Review, fmt.Sprintf("article %s, which supplies a claim of %s, was changed at its source and the event has not been derived again: whether the claim still stands is not known", a.ID, p.Ref()), a.ID + "@" + c.Heads[a.ID]})
				moved = true
			}
		}
		if !moved && c.Revision != p.Revision {
			ignored = append(ignored, fmt.Sprintf("%s is now revision %d: nothing it says that the plan rests on changed", p.Ref(), c.Revision))
		}
	}
	return found, ignored
}

// Rule is a thesis rule the user recorded: when a live event of one of these
// kinds appears about the security, the thesis is broken or needs a look. It
// is the only way this program calls a thesis broken.
type Rule struct {
	ID       string   `json:"id"`
	Security string   `json:"security"`
	Kinds    []string `json:"kinds"`
	// Roles the security must have in the event; the default is subject.
	Roles   []string  `json:"roles,omitempty"`
	Effect  Effect    `json:"effect"` // broken or review
	Note    string    `json:"note"`
	Version int       `json:"version"`
	Retired bool      `json:"retired,omitempty"`
	By      string    `json:"by"`
	At      time.Time `json:"at"`
}

// Validate says why a rule cannot be recorded.
func (r Rule) Validate() error {
	var bad []string
	if r.ID == "" || strings.ContainsAny(r.ID, " |") {
		bad = append(bad, "the rule needs an ID without spaces")
	}
	if r.Security == "" {
		bad = append(bad, "the rule needs a security")
	}
	if len(r.Kinds) == 0 {
		bad = append(bad, "the rule needs at least one event kind")
	}
	if r.Effect != Broken && r.Effect != Review {
		bad = append(bad, "the effect is broken or review")
	}
	for _, role := range r.Roles {
		switch model.Role(role) {
		case model.RoleSubject, model.RoleCounterparty, model.RoleMentioned:
		default:
			bad = append(bad, fmt.Sprintf("%q is not a role", role))
		}
	}
	if strings.TrimSpace(r.Note) == "" {
		bad = append(bad, "the rule says why the thesis fails when it matches (note)")
	}
	if strings.TrimSpace(r.By) == "" {
		bad = append(bad, "the rule says who recorded it (by)")
	}
	if len(bad) > 0 {
		return fmt.Errorf("corrections: %s", strings.Join(bad, "; "))
	}
	return nil
}

func (r Rule) roles() []string {
	if len(r.Roles) == 0 {
		return []string{string(model.RoleSubject)}
	}
	return r.Roles
}

// Matches lists the events (as "id@revision") that satisfy the rule.
func (r Rule) Matches(pins []EventPin) []string {
	if r.Retired {
		return nil
	}
	var out []string
	for _, p := range pins {
		if p.Status == model.StatusWithdrawn {
			continue
		}
		if !contains(r.Kinds, p.Kind) || !contains(r.roles(), p.Role) {
			continue
		}
		out = append(out, fmt.Sprintf("%s@%d", p.EventID, p.Revision))
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// RulePin is a rule at the version a revision was assessed with.
type RulePin struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// Deps is what one plan revision depends on.
type Deps struct {
	Plan     string   `json:"plan"`
	Rev      int      `json:"rev"`
	Security string   `json:"security"`
	Tickers  []string `json:"tickers"`
	// Events are the supporting events at the versions read.
	Events []EventPin `json:"events"`
	// Rules are the thesis rules in force and the events that already matched
	// them: a match that was there when the plan was made is not news.
	Rules    []RulePin           `json:"rules,omitempty"`
	RuleHits map[string][]string `json:"rule_hits,omitempty"`
	Recorded time.Time           `json:"recorded"`
}

// NewRuleHits compares the matches now with those the revision was made with.
func NewRuleHits(rules []Rule, pins []EventPin, d Deps) []Finding {
	var out []Finding
	for _, r := range rules {
		if r.Retired {
			continue
		}
		known := map[string]bool{}
		for _, h := range d.RuleHits[r.ID] {
			known[h] = true
		}
		for _, m := range r.Matches(pins) {
			if known[m] {
				continue
			}
			effect := r.Effect
			verb := "needs a look"
			if effect == Broken {
				verb = "is broken"
			}
			out = append(out, Finding{"thesis_rule", r.ID + " " + m, effect, fmt.Sprintf("rule %s (version %d) matched %s: the thesis %s. %s", r.ID, r.Version, m, verb, r.Note), r.ID + "@" + m})
		}
	}
	return out
}

// Strongest is the strongest effect among the findings.
func Strongest(fs []Finding) Effect {
	best := Effect("")
	for _, f := range fs {
		if best == "" || f.Effect.rank() > best.rank() {
			best = f.Effect
		}
	}
	return best
}

// ChangeID names a set of findings, so the same change found twice is one
// change. It does not depend on when it was found.
func ChangeID(plan string, fs []Finding) string {
	var parts []string
	for _, f := range fs {
		parts = append(parts, f.Kind+"|"+f.Ref+"|"+f.To)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(plan + "\n" + strings.Join(parts, "\n")))
	return "chg_" + hex.EncodeToString(sum[:6])
}
