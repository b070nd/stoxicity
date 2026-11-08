package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	kindRevision = "plan.revision"
	kindEvent    = "plan.event"
)

// Repo keeps revisions and lifecycle events in a tenant's record store. Both
// kinds are append-only: a revision is written once, and the state of a
// revision is the last event of its log.
type Repo struct{ rs store.Records }

// NewRepo wraps a tenant's record store.
func NewRepo(rs store.Records) Repo { return Repo{rs: rs} }

func revKey(planID string, rev int) string { return fmt.Sprintf("%s|%04d", planID, rev) }

// ErrNoPlan is returned for a plan or revision that does not exist.
var ErrNoPlan = errors.New("plan: no such plan")

// Saved is the outcome of Save.
type Saved struct {
	Revision Revision
	// Created is false when the same frozen inputs and card were already stored.
	Created bool
	// Superseded names the draft or proposed revision this one replaced.
	Superseded string
	// Accepted names an accepted revision that stays in force until this one is accepted.
	Accepted string
}

// Save stores a generated revision. Identical content to the latest revision is
// not stored again. Otherwise the next number is assigned, the changes against
// the predecessor are recorded, and a draft or proposed predecessor is
// superseded in the same write.
func (r Repo) Save(ctx context.Context, in Revision, now time.Time) (Saved, error) {
	if err := in.Card.Validate(); err != nil {
		return Saved{}, err
	}
	latest, err := r.Latest(ctx, in.PlanID)
	if err != nil && !errors.Is(err, ErrNoPlan) {
		return Saved{}, err
	}
	if err == nil && latest.Semantic == in.Semantic {
		return Saved{Revision: latest}, nil
	}
	rev := in
	rev.Rev, rev.Generated = 1, now.UTC()
	var batch []store.RecordInput
	out := Saved{Created: true}
	if err == nil {
		rev.Rev = latest.Rev + 1
		rev.Previous = latest.Semantic
		rev.Changes = Diff(latest.Card, rev.Card)
		_, log, err := r.State(ctx, in.PlanID, latest.Rev)
		if err != nil {
			return Saved{}, err
		}
		switch st := StateOf(log); st {
		case Draft, Proposed:
			ev := Event{PlanID: in.PlanID, Rev: latest.Rev, Seq: len(log) + 1, From: st, To: Superseded, At: now.UTC(), By: "system", Note: fmt.Sprintf("superseded by revision %d", rev.Rev)}
			b, _ := json.Marshal(ev)
			batch = append(batch, store.RecordInput{Kind: kindEvent, Key: revKey(in.PlanID, latest.Rev), Body: b})
			out.Superseded = latest.ID()
		case Accepted:
			out.Accepted = latest.ID()
		}
	}
	b, err := json.Marshal(rev)
	if err != nil {
		return Saved{}, err
	}
	batch = append(batch, store.RecordInput{Kind: kindRevision, Key: revKey(rev.PlanID, rev.Rev), Body: b})
	if _, _, err := r.rs.Append(ctx, batch...); err != nil {
		return Saved{}, err
	}
	out.Revision = rev
	return out, nil
}

func decodeRevision(rec store.Record) (Revision, error) {
	var rv Revision
	if err := json.Unmarshal(rec.Body, &rv); err != nil {
		return Revision{}, fmt.Errorf("plan: stored revision %s is unreadable: %w", rec.Key, err)
	}
	return rv, nil
}

// Revisions lists a plan's revisions, oldest first.
func (r Repo) Revisions(ctx context.Context, planID string) ([]Revision, error) {
	recs, err := r.rs.All(ctx, kindRevision, planID+"|")
	if err != nil {
		return nil, err
	}
	out := make([]Revision, 0, len(recs))
	for _, rec := range recs {
		rv, err := decodeRevision(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, rv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rev < out[j].Rev })
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoPlan, planID)
	}
	return out, nil
}

// Latest is the newest revision of a plan.
func (r Repo) Latest(ctx context.Context, planID string) (Revision, error) {
	all, err := r.Revisions(ctx, planID)
	if err != nil {
		return Revision{}, err
	}
	return all[len(all)-1], nil
}

// Get returns one revision; rev 0 means the latest.
func (r Repo) Get(ctx context.Context, planID string, rev int) (Revision, error) {
	if rev == 0 {
		return r.Latest(ctx, planID)
	}
	rec, err := r.rs.Latest(ctx, kindRevision, revKey(planID, rev))
	if errors.Is(err, store.ErrNoRecord) {
		return Revision{}, fmt.Errorf("%w: %s revision %d", ErrNoPlan, planID, rev)
	}
	if err != nil {
		return Revision{}, err
	}
	return decodeRevision(rec)
}

// Plans lists the latest revision of every plan, newest first.
func (r Repo) Plans(ctx context.Context) ([]Revision, error) {
	recs, err := r.rs.All(ctx, kindRevision, "")
	if err != nil {
		return nil, err
	}
	latest := map[string]Revision{}
	for _, rec := range recs {
		rv, err := decodeRevision(rec)
		if err != nil {
			return nil, err
		}
		if cur, ok := latest[rv.PlanID]; !ok || rv.Rev > cur.Rev {
			latest[rv.PlanID] = rv
		}
	}
	out := make([]Revision, 0, len(latest))
	for _, v := range latest {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Generated.Equal(out[j].Generated) {
			return out[i].Generated.After(out[j].Generated)
		}
		return out[i].PlanID < out[j].PlanID
	})
	return out, nil
}

// State returns the stored state of a revision and its log.
func (r Repo) State(ctx context.Context, planID string, rev int) (State, []Event, error) {
	recs, err := r.rs.History(ctx, kindEvent, revKey(planID, rev))
	if err != nil {
		return "", nil, err
	}
	log := make([]Event, 0, len(recs))
	for _, rec := range recs {
		var ev Event
		if err := json.Unmarshal(rec.Body, &ev); err != nil {
			return "", nil, fmt.Errorf("plan: stored event %s is unreadable: %w", rec.Key, err)
		}
		log = append(log, ev)
	}
	sort.Slice(log, func(i, j int) bool { return log[i].Seq < log[j].Seq })
	return StateOf(log), log, nil
}

// Move records a lifecycle move after CheckMove. Accepting a revision supersedes
// any other accepted revision of the plan in the same write.
func (r Repo) Move(ctx context.Context, rv Revision, to State, by, note string, current Manifest, now time.Time) (Event, error) {
	_, log, err := r.State(ctx, rv.PlanID, rv.Rev)
	if err != nil {
		return Event{}, err
	}
	if err := CheckMove(rv, log, to, by, note, current, now); err != nil {
		return Event{}, err
	}
	from := StateOf(log)
	ev := Event{PlanID: rv.PlanID, Rev: rv.Rev, Seq: len(log) + 1, From: from, To: to, At: now.UTC(), By: strings.TrimSpace(by), Note: strings.TrimSpace(note)}
	b, _ := json.Marshal(ev)
	batch := []store.RecordInput{{Kind: kindEvent, Key: revKey(rv.PlanID, rv.Rev), Body: b}}
	if to == Accepted {
		all, err := r.Revisions(ctx, rv.PlanID)
		if err != nil {
			return Event{}, err
		}
		for _, other := range all {
			if other.Rev == rv.Rev {
				continue
			}
			st, olog, err := r.State(ctx, other.PlanID, other.Rev)
			if err != nil {
				return Event{}, err
			}
			if st == Accepted || st == Proposed || st == Draft {
				oe := Event{PlanID: other.PlanID, Rev: other.Rev, Seq: len(olog) + 1, From: st, To: Superseded, At: now.UTC(), By: "system", Note: fmt.Sprintf("superseded by the acceptance of revision %d", rv.Rev)}
				ob, _ := json.Marshal(oe)
				batch = append(batch, store.RecordInput{Kind: kindEvent, Key: revKey(other.PlanID, other.Rev), Body: ob})
			}
		}
	}
	if _, _, err := r.rs.Append(ctx, batch...); err != nil {
		return Event{}, err
	}
	return ev, nil
}
