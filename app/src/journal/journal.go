// Package journal is the decision journal: why the user decided what they did
// about a plan, the thesis as it stood at that moment, and what happened later.
//
// An Entry is written once and never changed. It copies the plan's thesis, the
// evidence that would invalidate it, the exit rules and the counter-evidence as
// they were in the revision the user decided on, and names the manifest hash of
// that revision, so the reasoning can be read years later against what the
// plan said then and not what a regenerated plan says now. The user's reason is
// their own words.
//
// An Outcome is a later look: did the thesis hold, break, or did things go
// differently, with the user's note. It is linked to the entry, appended to the
// journal, and changes nothing that was recorded before: not the entry, not the
// plan, not the evidence the plan cited.
//
// The journal does not grade decisions by price. It carries no prices at all;
// an outcome is the user's reading, made on the horizon they set.
package journal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/store"
)

// The record kinds.
const (
	KindEntry   = "journal.entry"
	KindOutcome = "journal.outcome"
)

// Decision is what the user did with the plan.
type Decision string

// The decisions. Accepted and rejected are also the plan's own states; the
// journal never claims one the plan does not have.
const (
	Accepted Decision = "accepted"
	Rejected Decision = "rejected"
	Deferred Decision = "deferred" // looked at, decided to wait
	Noted    Decision = "noted"    // reasoning about a plan without a decision
)

// Valid reports whether d is a known decision.
func (d Decision) Valid() bool {
	switch d {
	case Accepted, Rejected, Deferred, Noted:
		return true
	}
	return false
}

// Entry is one recorded decision.
type Entry struct {
	ID       string   `json:"id"`
	Plan     string   `json:"plan"` // pl_x.rN
	PlanID   string   `json:"plan_id"`
	Revision int      `json:"revision"`
	Security string   `json:"security"`
	Book     string   `json:"book"`
	Strategy string   `json:"strategy"`
	Action   string   `json:"action"`
	Manifest string   `json:"manifest_hash"`
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason"`  // the user's words
	Horizon  string   `json:"horizon"` // YYYY-MM-DD: the day to look back
	Alert    string   `json:"alert,omitempty"`
	By       string   `json:"by"`
	Day      string   `json:"day"`

	// The thesis as the plan stated it when the decision was made.
	Thesis       []string `json:"thesis"`
	Counter      []string `json:"counter_evidence"`
	Invalidation []string `json:"invalidation"`
	ExitRules    []string `json:"exit_rules"`
}

// Result is the user's reading of how the thesis fared.
type Result string

// The results.
const (
	ThesisHeld   Result = "held"
	ThesisBroken Result = "broken"
	Mixed        Result = "mixed"
	TooEarly     Result = "early"
)

// Valid reports whether r is a known result.
func (r Result) Valid() bool {
	switch r {
	case ThesisHeld, ThesisBroken, Mixed, TooEarly:
		return true
	}
	return false
}

// Outcome is one later look at an entry.
type Outcome struct {
	Entry  string    `json:"entry"`
	Result Result    `json:"result"`
	Note   string    `json:"note"`
	By     string    `json:"by"`
	Day    string    `json:"day"`
	At     time.Time `json:"at"`
}

// Finalizes reports whether the outcome closes the entry: "too early" does not.
func (o Outcome) Finalizes() bool { return o.Result != TooEarly }

// New builds the entry for a decision on a plan revision. day is today, as
// YYYY-MM-DD. horizon may be empty: the plan's review date is used, and without
// one the horizon must be given.
func New(rv plan.Revision, d Decision, reason, by, horizon, alert, day string) (Entry, error) {
	if !d.Valid() {
		return Entry{}, errors.New("journal: a decision is accepted, rejected, deferred or noted")
	}
	reason, by = strings.TrimSpace(reason), strings.TrimSpace(by)
	if reason == "" {
		return Entry{}, errors.New("journal: say why, in your own words (-reason): a journal without the reasoning is a log")
	}
	if by == "" {
		return Entry{}, errors.New("journal: say who is deciding (-by)")
	}
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		return Entry{}, errors.New("journal: the day must be YYYY-MM-DD")
	}
	if horizon == "" {
		horizon = rv.Card.ReviewBy
	}
	if horizon == "" {
		return Entry{}, errors.New("journal: the plan names no review date, so say when to look back (-horizon YYYY-MM-DD)")
	}
	h, err := time.Parse(time.DateOnly, horizon)
	if err != nil {
		return Entry{}, errors.New("journal: the horizon must be YYYY-MM-DD")
	}
	if t, _ := time.Parse(time.DateOnly, day); h.Before(t) {
		return Entry{}, errors.New("journal: the horizon is before today")
	}
	c := rv.Card
	e := Entry{Plan: rv.ID(), PlanID: rv.PlanID, Revision: rv.Rev, Security: c.Security, Book: c.Book, Strategy: c.Strategy, Action: c.Action,
		Manifest: rv.ManifestHash, Decision: d, Reason: reason, Horizon: horizon, Alert: alert, By: by, Day: day,
		Thesis: nz(c.Thesis), Counter: nz(c.Counter), Invalidation: nz(c.Invalidation), ExitRules: nz(c.ExitRules)}
	sum := sha256.Sum256([]byte(strings.Join([]string{e.Plan, string(d), day, by, reason}, "\x00")))
	e.ID = "jr_" + hex.EncodeToString(sum[:])[:10]
	return e, nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return append([]string(nil), s...)
}

// Repo is a tenant's journal.
type Repo struct {
	RS  store.Records
	Now func() time.Time
}

func (r Repo) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Add stores an entry. Adding the same decision again is not an error and
// stores nothing.
func (r Repo) Add(ctx context.Context, e Entry) (created bool, err error) {
	body, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	_, n, err := r.RS.Append(ctx, store.RecordInput{Kind: KindEntry, Key: e.ID, Body: body})
	return n > 0, err
}

func decodeEntry(rec store.Record) (Entry, error) {
	var e Entry
	if err := json.Unmarshal(rec.Body, &e); err != nil {
		return e, fmt.Errorf("journal entry %s does not decode: %w", rec.Key, err)
	}
	return e, nil
}

// Get returns an entry by its ID.
func (r Repo) Get(ctx context.Context, id string) (Entry, error) {
	rec, err := r.RS.Latest(ctx, KindEntry, id)
	if errors.Is(err, store.ErrNoRecord) {
		return Entry{}, fmt.Errorf("no journal entry %q (journal list shows them)", id)
	}
	if err != nil {
		return Entry{}, err
	}
	return decodeEntry(rec)
}

// List returns every entry, newest first.
func (r Repo) List(ctx context.Context) ([]Entry, error) {
	recs, err := r.RS.Current(ctx, KindEntry, "")
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(recs))
	for _, rec := range recs {
		e, err := decodeEntry(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day > out[j].Day
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// AddOutcome appends a later look at an entry.
func (r Repo) AddOutcome(ctx context.Context, entry string, res Result, note, by, day string) (Outcome, error) {
	if _, err := r.Get(ctx, entry); err != nil {
		return Outcome{}, err
	}
	note, by = strings.TrimSpace(note), strings.TrimSpace(by)
	switch {
	case !res.Valid():
		return Outcome{}, errors.New("journal: the result is held, broken, mixed or early")
	case note == "":
		return Outcome{}, errors.New("journal: say what happened, in your own words (-note)")
	case by == "":
		return Outcome{}, errors.New("journal: say who is looking back (-by)")
	}
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		return Outcome{}, errors.New("journal: the day must be YYYY-MM-DD")
	}
	o := Outcome{Entry: entry, Result: res, Note: note, By: by, Day: day, At: r.now()}
	body, err := json.Marshal(o)
	if err != nil {
		return o, err
	}
	_, _, err = r.RS.Append(ctx, store.RecordInput{Kind: KindOutcome, Key: entry, Body: body})
	return o, err
}

// Outcomes returns every later look at an entry, oldest first.
func (r Repo) Outcomes(ctx context.Context, entry string) ([]Outcome, error) {
	recs, err := r.RS.History(ctx, KindOutcome, entry)
	if err != nil {
		return nil, err
	}
	out := make([]Outcome, 0, len(recs))
	for _, rec := range recs {
		var o Outcome
		if err := json.Unmarshal(rec.Body, &o); err != nil {
			return nil, fmt.Errorf("journal outcome of %s does not decode: %w", entry, err)
		}
		out = append(out, o)
	}
	return out, nil
}

// Closed reports whether an outcome that is not "too early" exists.
func Closed(os []Outcome) bool {
	for _, o := range os {
		if o.Finalizes() {
			return true
		}
	}
	return false
}

// Due lists the entries whose horizon is today or past and that no outcome has
// closed, oldest horizon first.
func (r Repo) Due(ctx context.Context, day string) ([]Entry, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range all {
		if e.Horizon > day {
			continue
		}
		os, err := r.Outcomes(ctx, e.ID)
		if err != nil {
			return nil, err
		}
		if !Closed(os) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Horizon != out[j].Horizon {
			return out[i].Horizon < out[j].Horizon
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Drift compares the thesis an entry recorded with a plan revision, usually the
// plan's latest. Each list holds the statements of the entry that are still
// there ("Same"), those that are gone ("Gone") and those that are new ("New").
type Drift struct {
	Section string
	Same    []string
	Gone    []string
	New     []string
}

// Changed reports whether anything differs.
func (d Drift) Changed() bool { return len(d.Gone) > 0 || len(d.New) > 0 }

// Compare reads the entry against a card. It judges nothing: statements are
// compared as written.
func Compare(e Entry, c plan.Card) []Drift {
	one := func(name string, then, now []string) Drift {
		in := func(s string, xs []string) bool {
			for _, x := range xs {
				if x == s {
					return true
				}
			}
			return false
		}
		d := Drift{Section: name}
		for _, s := range then {
			if in(s, now) {
				d.Same = append(d.Same, s)
			} else {
				d.Gone = append(d.Gone, s)
			}
		}
		for _, s := range now {
			if !in(s, then) {
				d.New = append(d.New, s)
			}
		}
		return d
	}
	return []Drift{
		one("thesis", e.Thesis, c.Thesis),
		one("counter-evidence", e.Counter, c.Counter),
		one("invalidation", e.Invalidation, c.Invalidation),
		one("exit rules", e.ExitRules, c.ExitRules),
	}
}
