package journal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/store"
)

func revision(rev int, thesis ...string) plan.Revision {
	return plan.Revision{PlanID: "pl_abc", Rev: rev, ManifestHash: "mh" + string(rune('0'+rev)),
		Card: plan.Card{Security: "KO", Book: "bk_1", Strategy: "quality-income-core-v1", Action: "BUY", ReviewBy: "2027-02-01",
			Thesis: thesis, Counter: []string{"payout ratio is high"}, Invalidation: []string{"dividend cut"}, ExitRules: []string{"sell if the thesis breaks"}}}
}

func repo(t *testing.T, now *time.Time) Repo {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return Repo{RS: db.Records("acme"), Now: func() time.Time { return *now }}
}

func TestEntryKeepsTheThesisAsItWas(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	r := repo(t, &now)
	ctx := context.Background()
	e, err := New(revision(1, "steady dividend", "wide moat"), Accepted, "I want the income", "me", "", "", "2026-11-04")
	if err != nil {
		t.Fatal(err)
	}
	if e.Horizon != "2027-02-01" || !strings.HasPrefix(e.ID, "jr_") || e.Manifest != "mh1" || e.Plan != "pl_abc.r1" {
		t.Fatalf("%+v", e)
	}
	if created, err := r.Add(ctx, e); err != nil || !created {
		t.Fatal(created, err)
	}
	if created, err := r.Add(ctx, e); err != nil || created {
		t.Fatalf("the same decision is stored once: %v %v", created, err)
	}
	// the plan is regenerated with a different thesis; the entry does not move
	got, err := r.Get(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	drift := Compare(got, revision(2, "steady dividend", "pricing power").Card)
	var th Drift
	for _, d := range drift {
		if d.Section == "thesis" {
			th = d
		}
	}
	if !th.Changed() || len(th.Same) != 1 || th.Same[0] != "steady dividend" || th.Gone[0] != "wide moat" || th.New[0] != "pricing power" {
		t.Errorf("thesis drift: %+v", th)
	}
	for _, d := range drift {
		if d.Section == "invalidation" && d.Changed() {
			t.Errorf("an unchanged section is not a change: %+v", d)
		}
	}
	if len(got.Thesis) != 2 || got.Thesis[1] != "wide moat" {
		t.Errorf("the entry still holds what the plan said then: %v", got.Thesis)
	}
}

func TestEntryNeedsTheReasoning(t *testing.T) {
	rv := revision(1, "x")
	for name, f := range map[string]func() error{
		"no reason":   func() error { _, err := New(rv, Accepted, "  ", "me", "", "", "2026-11-04"); return err },
		"no name":     func() error { _, err := New(rv, Accepted, "why", "", "", "", "2026-11-04"); return err },
		"bad choice":  func() error { _, err := New(rv, "maybe", "why", "me", "", "", "2026-11-04"); return err },
		"bad horizon": func() error { _, err := New(rv, Accepted, "why", "me", "next year", "", "2026-11-04"); return err },
		"past":        func() error { _, err := New(rv, Accepted, "why", "me", "2026-11-01", "", "2026-11-04"); return err },
		"bad day":     func() error { _, err := New(rv, Accepted, "why", "me", "", "", "today"); return err },
		"no horizon": func() error {
			x := revision(1, "x")
			x.Card.ReviewBy = ""
			_, err := New(x, Accepted, "why", "me", "", "", "2026-11-04")
			return err
		},
	} {
		if f() == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
}

func TestOutcomesAreLinkedAndAppended(t *testing.T) {
	now := time.Date(2027, 2, 2, 9, 0, 0, 0, time.UTC)
	r := repo(t, &now)
	ctx := context.Background()
	e, _ := New(revision(1, "steady dividend"), Accepted, "income", "me", "", "", "2026-11-04")
	r.Add(ctx, e)
	if _, err := r.AddOutcome(ctx, "jr_nothing", ThesisHeld, "ok", "me", "2027-02-02"); err == nil {
		t.Error("an outcome needs an entry")
	}
	if _, err := r.AddOutcome(ctx, e.ID, "great", "ok", "me", "2027-02-02"); err == nil {
		t.Error("a result is one of the four")
	}
	if _, err := r.AddOutcome(ctx, e.ID, ThesisHeld, " ", "me", "2027-02-02"); err == nil {
		t.Error("an outcome needs its note")
	}
	due, _ := r.Due(ctx, "2027-02-01")
	if len(due) != 1 {
		t.Fatalf("the horizon has come: %v", due)
	}
	if due, _ := r.Due(ctx, "2027-01-31"); len(due) != 0 {
		t.Error("not before the horizon")
	}
	if _, err := r.AddOutcome(ctx, e.ID, TooEarly, "dividend not yet paid", "me", "2027-02-02"); err != nil {
		t.Fatal(err)
	}
	if due, _ := r.Due(ctx, "2027-02-02"); len(due) != 1 {
		t.Error("'too early' leaves the entry due")
	}
	if _, err := r.AddOutcome(ctx, e.ID, ThesisBroken, "the dividend was cut", "me", "2027-03-01"); err != nil {
		t.Fatal(err)
	}
	if due, _ := r.Due(ctx, "2027-03-01"); len(due) != 0 {
		t.Error("a real result closes it")
	}
	os, err := r.Outcomes(ctx, e.ID)
	if err != nil || len(os) != 2 || os[0].Result != TooEarly || os[1].Result != ThesisBroken || os[1].Entry != e.ID {
		t.Fatalf("both looks are kept, in order: %+v %v", os, err)
	}
	hist, _ := r.RS.History(ctx, KindEntry, e.ID)
	if len(hist) != 1 {
		t.Errorf("the entry itself was never rewritten: %d", len(hist))
	}
	all, _ := r.List(ctx)
	if len(all) != 1 || all[0].Reason != "income" {
		t.Errorf("%+v", all)
	}
	if _, err := r.Get(ctx, "jr_none"); err == nil {
		t.Error("unknown entry")
	}
}
