package pipeline

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/assertion"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// RebuildStore is what deriving the events again needs of the database.
type RebuildStore interface {
	HomeKinds(ctx context.Context, eventIDs []string) (map[string]model.EventKind, error)
	PutEvents(ctx context.Context, in []store.EventInput, at time.Time) (int, error)
	LiveEvents(ctx context.Context, asOf time.Time) ([]store.LiveEvent, error)
	WithdrawEvents(ctx context.Context, ws []store.Withdrawal, at time.Time) (int, error)
}

// Rebuilt says what RebuildEvents did.
type Rebuilt struct {
	Events    int // events derived
	Written   int // revisions written
	Withdrawn int // stored events the claims no longer support
	// Split counts the stories that are now more than one event.
	Split int
}

// RebuildEvents derives the events of the articles again from their claims. It
// appends: an event whose understanding is the same adds nothing, one whose
// understanding differs gets a revision (and, because the derivation is complete,
// its securities and evidence are exactly what the claims support, not what was
// stored before), and a stored event that none of the claims supports any longer
// is withdrawn. Nothing is deleted, and the earlier revisions can still be read.
//
// Only events whose every article is among the ones looked at are replaced or
// withdrawn: an article that was not looked at cannot have been asserted again.
func RebuildEvents(ctx context.Context, st RebuildStore, arts []model.Article, claims map[string]assertion.Result, now time.Time) (Rebuilt, []store.EventInput, error) {
	var rep Rebuilt
	homes, err := Homes(ctx, st, arts)
	if err != nil {
		return rep, nil, err
	}
	in := EventsFrom(arts, claims, homes)
	scope := make(map[string]bool, len(arts))
	for _, a := range arts {
		scope[a.ID] = true
	}
	emitted := map[string]bool{}
	stories := map[string]int{}
	for i := range in {
		in[i].Complete, in[i].Scope = true, scope
		emitted[in[i].EventID] = true
		stories[storyOf(in[i].EventID)]++
	}
	for _, n := range stories {
		if n > 1 {
			rep.Split++
		}
	}
	rep.Events = len(in)
	for start := 0; start < len(in); start += 500 {
		n, err := st.PutEvents(ctx, in[start:min(start+500, len(in))], now)
		if err != nil {
			return rep, in, err
		}
		rep.Written += n
	}
	live, err := st.LiveEvents(ctx, time.Time{})
	if err != nil {
		return rep, in, err
	}
	var ws []store.Withdrawal
	for _, e := range live {
		if emitted[e.EventID] || len(e.Articles) == 0 {
			continue
		}
		if !slices.ContainsFunc(e.Articles, func(id string) bool { return scope[id] }) {
			continue
		}
		if slices.ContainsFunc(e.Articles, func(id string) bool { return !scope[id] }) {
			continue
		}
		var succ []string
		for _, o := range in {
			if storyOf(o.EventID) == storyOf(e.EventID) {
				succ = append(succ, o.EventID)
			}
		}
		basis := fmt.Sprintf("no live claim of kind %s about a company under the %s rules", e.Kind, assertion.Version)
		if len(succ) > 0 {
			basis += "; the story's claims now make " + strings.Join(succ, ", ")
		}
		ws = append(ws, store.Withdrawal{EventID: e.EventID, Successors: succ, Basis: basis})
	}
	rep.Withdrawn, err = st.WithdrawEvents(ctx, ws, now)
	return rep, in, err
}

// storyOf is the story an event belongs to: its ID without the kind that marks
// the later events of a story.
func storyOf(eventID string) string {
	if i := strings.Index(eventID, "~"); i >= 0 {
		return eventID[:i]
	}
	return eventID
}
