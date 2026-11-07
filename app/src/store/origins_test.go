package store

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/origin"
)

func TestOriginsAreAppendedAndReadAsOfATimeWithoutBackdating(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	unknown := origin.Origin{ArticleID: "a1", Publisher: "p.example", Kind: origin.KindUnknown, Key: "unknown:p.example", Method: origin.MethodNone, Version: origin.Version}
	if n, err := db.PutOrigins(ctx, []origin.Origin{unknown}, t0); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if n, _ := db.PutOrigins(ctx, []origin.Origin{unknown}, t0.Add(time.Hour)); n != 0 {
		t.Errorf("finding the same again adds nothing: %d", n)
	}
	// A later rule version finds a wire attribution in the same article.
	wire := unknown
	wire.Kind, wire.Key, wire.Method, wire.Version = origin.KindWire, "wire:reuters", origin.MethodWireAttribution, "origin-2"
	if n, _ := db.PutOrigins(ctx, []origin.Origin{wire}, t0.Add(48*time.Hour)); n != 1 {
		t.Fatal("a different finding is a new row")
	}
	now, _ := db.Origins(ctx, []string{"a1"}, time.Time{})
	was, _ := db.Origins(ctx, []string{"a1"}, t0.Add(24*time.Hour))
	none, _ := db.Origins(ctx, []string{"a1"}, t0.Add(-time.Hour))
	if now["a1"].Kind != origin.KindWire || was["a1"].Kind != origin.KindUnknown || len(none) != 0 {
		t.Errorf("an origin found later is not known earlier: now %v was %v none %v", now["a1"].Kind, was["a1"].Kind, none)
	}
}

func TestEpisodeDecisionsAreAppendedAndReadAsOfATime(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	d := EpisodeDecision{SecurityID: "sec_1", EventA: "evt_a", EventB: "evt_b", Decision: DecisionUndecided, Basis: "nothing links them", Method: "nothing-links", Version: "episode-1"}
	if n, err := db.PutEpisodeDecisions(ctx, []EpisodeDecision{d}, t0); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if n, _ := db.PutEpisodeDecisions(ctx, []EpisodeDecision{d}, t0.Add(time.Hour)); n != 0 {
		t.Errorf("the same decision again adds nothing: %d", n)
	}
	later := d
	later.Decision, later.Basis, later.Method = DecisionSame, "both rest on filing x", "shared-filing"
	if n, _ := db.PutEpisodeDecisions(ctx, []EpisodeDecision{later}, t0.Add(48*time.Hour)); n != 1 {
		t.Fatal("a changed decision is appended")
	}
	key := [2]string{"evt_a", "evt_b"}
	now, _ := db.EpisodeDecisions(ctx, "sec_1", time.Time{})
	was, _ := db.EpisodeDecisions(ctx, "sec_1", t0.Add(24*time.Hour))
	none, _ := db.EpisodeDecisions(ctx, "sec_1", t0.Add(-time.Hour))
	if now[key].Decision != DecisionSame || was[key].Decision != DecisionUndecided || len(none) != 0 {
		t.Errorf("a relationship inferred later is never backdated: now %v was %v none %v", now[key].Decision, was[key].Decision, none)
	}
	if _, err := db.PutEpisodeDecisions(ctx, []EpisodeDecision{{SecurityID: "s", EventA: "evt_b", EventB: "evt_a"}}, t0); err == nil {
		t.Error("a pair out of order is refused")
	}
}

func TestTheObservationsBehindASuspicionAreKeptAndReadAsOfATime(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	o := origin.Origin{ArticleID: "a2", Publisher: "p.example", Kind: origin.KindReported, Key: "reported:p.example", Method: origin.MethodPassageOverlap,
		SuspectedFrom: "wire:reuters", Version: origin.Version,
		Matches: []origin.Match{{Other: "a1", Method: origin.MethodPassageOverlap, Score: 0.6, Shared: 14, LongestRun: 13, Passage: "the company said on tuesday it would buy"}}}
	if n, err := db.PutOrigins(ctx, []origin.Origin{o}, t0); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	// Finding the same again neither adds a row nor duplicates the observation.
	db.PutOrigins(ctx, []origin.Origin{o}, t0.Add(time.Hour))
	ms, err := db.OriginMatches(ctx, "a2", time.Time{})
	if err != nil || len(ms) != 1 || ms[0].Other != "a1" || ms[0].LongestRun != 13 || ms[0].Passage == "" || ms[0].Method != origin.MethodPassageOverlap {
		t.Fatalf("matches = %+v, %v", ms, err)
	}
	got, _ := db.Origins(ctx, []string{"a2"}, time.Time{})
	if got["a2"].SuspectedFrom != "wire:reuters" || got["a2"].Standing() != origin.StandingSuspected {
		t.Errorf("a suspicion is stored as a suspicion: %+v", got["a2"])
	}
	if before, _ := db.OriginMatches(ctx, "a2", t0.Add(-time.Hour)); len(before) != 0 {
		t.Errorf("an observation made later shows in an earlier reading: %+v", before)
	}
}
