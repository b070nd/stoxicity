package plan

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/store"
)

var t0 = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func card() Card {
	return Card{
		AsOf: "2026-11-03", Expiry: "2026-11-06", Security: "sec_apple", Book: "b1", Sleeve: "core", Strategy: "quality-income-core-v1",
		Action: "BUY", DataState: "complete", StrategyState: "reviewed", ExecutionEligibility: "research_only",
		Reasons: []string{"inside the entry band"}, Unknowns: []string{"spread: no quote is stored"},
		Score: 85, MaxScore: 100,
		Components:   []Component{{Group: "quality", ID: "q.cash", Result: "pass", Awarded: 10, Max: 10}},
		Scenarios:    &Scenarios{Currency: "USD", Bear: "10", Base: "20", Bull: "30", Ceiling: "16", Entry: "base", Margin: "0.2"},
		Sizing:       &Sizing{Qty: "10", Base: "EUR", Notional: 10000, Fee: 125, CashBefore: 100000, CashAfter: 89875, Reserve: "0.20"},
		Invalidation: []string{"free cash flow turns negative"}, ReviewBy: "2027-02-03",
	}
}

func manifest() Manifest {
	return Manifest{AsOf: "2026-11-03", PolicyEpoch: 1, Policy: "p1", Strategy: "quality-income-core-v1", Config: "c1", Scorecard: "s1", Assumptions: "a1", Price: "2026-11-02 supplied", Payouts: 4, Fees: "f1", Rates: "r1", Ledger: "l1"}
}

func repo(t *testing.T) Repo {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewRepo(db.Records("acme"))
}

func TestLifecycleTable(t *testing.T) {
	ok := [][2]State{{Draft, Proposed}, {Proposed, Accepted}, {Proposed, Rejected}, {Accepted, Superseded}, {Accepted, Expired}, {Draft, Rejected}}
	for _, m := range ok {
		if !CanMove(m[0], m[1]) {
			t.Errorf("%s -> %s should be allowed", m[0], m[1])
		}
	}
	bad := [][2]State{{Draft, Accepted}, {Rejected, Proposed}, {Expired, Accepted}, {Superseded, Accepted}, {Accepted, Rejected}, {Accepted, Proposed}}
	for _, m := range bad {
		if CanMove(m[0], m[1]) {
			t.Errorf("%s -> %s should be refused", m[0], m[1])
		}
	}
	for _, s := range []State{Rejected, Expired, Superseded} {
		if !s.Terminal() {
			t.Errorf("%s is terminal", s)
		}
	}
}

func TestCardRulesHoldWhateverProducedIt(t *testing.T) {
	c := card()
	c.Action = "ADD"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "not held cannot receive ADD") {
		t.Errorf("an unheld ADD: %v", err)
	}
	c.Held = "5"
	if err := c.Validate(); err != nil {
		t.Errorf("a held ADD: %v", err)
	}
	c = card()
	c.Action, c.Held, c.Free = "SELL", "10", "4"
	c.Sizing.Qty = "6"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "exceeds the 4 free to sell") {
		t.Errorf("a sale beyond the free holding: %v", err)
	}
	c = card()
	c.Expiry = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "standing authorization") {
		t.Errorf("no expiry: %v", err)
	}
	c = card()
	c.Expiry = "2026-11-01"
	if err := c.Validate(); err == nil {
		t.Error("expiry before as-of")
	}
	c = card()
	c.Sizing = nil
	if err := c.Validate(); err == nil {
		t.Error("a BUY without a quantity")
	}
}

func TestIdenticalFrozenInputsGiveTheSameSnapshot(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	a, err := New(card(), manifest(), t0)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := r.Save(ctx, a, t0)
	if err != nil || !s1.Created || s1.Revision.Rev != 1 {
		t.Fatalf("%v %+v", err, s1)
	}
	// generated again later from the same inputs: the same semantic hash, nothing stored
	b, _ := New(card(), manifest(), t0.Add(48*time.Hour))
	if a.Semantic != b.Semantic || a.ManifestHash != b.ManifestHash {
		t.Fatal("the generation time leaked into the hash")
	}
	s2, err := r.Save(ctx, b, t0.Add(48*time.Hour))
	if err != nil || s2.Created || s2.Revision.Rev != 1 {
		t.Fatalf("%v %+v", err, s2)
	}
	if revs, _ := r.Revisions(ctx, a.PlanID); len(revs) != 1 {
		t.Fatalf("%d revisions", len(revs))
	}
	got, err := r.Get(ctx, a.PlanID, 1)
	if err != nil || got.Verify() != nil || got.Generated != t0 {
		t.Fatalf("%v %v", err, got.Verify())
	}
}

func TestRefreshAppendsARevisionAndSupersedesAnOpenOne(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	a, _ := New(card(), manifest(), t0)
	if _, err := r.Save(ctx, a, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Move(ctx, a, Proposed, "me", "", manifest(), t0); err != nil {
		t.Fatal(err)
	}
	c2 := card()
	c2.Sizing.Qty = "8"
	c2.Sizing.Notional = 8000
	c2.Thesis = []string{"a new note"}
	m2 := manifest()
	m2.Scorecard = "s2"
	b, _ := New(c2, m2, t0.Add(time.Hour))
	s, err := r.Save(ctx, b, t0.Add(time.Hour))
	if err != nil || !s.Created || s.Revision.Rev != 2 || s.Superseded != a.ID() {
		t.Fatalf("%v %+v", err, s)
	}
	if !s.Revision.Material() {
		t.Error("a quantity change is material")
	}
	var fields []string
	for _, c := range s.Revision.Changes {
		fields = append(fields, c.Field)
	}
	for _, want := range []string{"quantity", "notional", "thesis"} {
		if !strings.Contains(strings.Join(fields, ","), want) {
			t.Errorf("missing change %q in %v", want, fields)
		}
	}
	if st, _, _ := r.State(ctx, a.PlanID, 1); st != Superseded {
		t.Errorf("revision 1 is %s", st)
	}
	// the first revision is unchanged
	old, _ := r.Get(ctx, a.PlanID, 1)
	if old.Semantic != a.Semantic || old.Card.Sizing.Qty != "10" {
		t.Error("an earlier revision was rewritten")
	}
}

func TestNonMaterialChangeIsRecordedButNotMaterial(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	a, _ := New(card(), manifest(), t0)
	r.Save(ctx, a, t0)
	c2 := card()
	c2.Thesis = []string{"reworded"}
	c2.ReviewBy = "2027-02-04"
	b, _ := New(c2, manifest(), t0.Add(time.Hour))
	s, err := r.Save(ctx, b, t0.Add(time.Hour))
	if err != nil || !s.Created || s.Revision.Material() {
		t.Fatalf("%v material=%v changes=%v", err, s.Revision.Material(), s.Revision.Changes)
	}
}

func TestAcceptedPlanIsNotRewrittenByARefresh(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	a, _ := New(card(), manifest(), t0)
	r.Save(ctx, a, t0)
	r.Move(ctx, a, Proposed, "me", "", manifest(), t0)
	if _, err := r.Move(ctx, a, Accepted, "me", "ok", manifest(), t0); err != nil {
		t.Fatal(err)
	}
	c2 := card()
	c2.Action = "WATCH"
	c2.Sizing = nil
	b, _ := New(c2, manifest(), t0.Add(time.Hour))
	s, err := r.Save(ctx, b, t0.Add(time.Hour))
	if err != nil || s.Accepted != a.ID() || s.Superseded != "" {
		t.Fatalf("%v %+v", err, s)
	}
	if st, _, _ := r.State(ctx, a.PlanID, 1); st != Accepted {
		t.Fatalf("the accepted revision became %s", st)
	}
	if st, _, _ := r.State(ctx, a.PlanID, 2); st != Draft {
		t.Fatalf("the refresh is %s", st)
	}
	// a WATCH view has nothing to accept
	if _, err := r.Move(ctx, s.Revision, Accepted, "me", "", manifest(), t0); err == nil {
		t.Fatal("a view was accepted")
	}
	// a second actionable revision supersedes the accepted one only when it is accepted
	c3 := card()
	c3.Sizing.Qty = "7"
	c3.Sizing.Notional = 7000
	d, _ := New(c3, manifest(), t0.Add(2*time.Hour))
	s3, _ := r.Save(ctx, d, t0.Add(2*time.Hour))
	if st, _, _ := r.State(ctx, a.PlanID, 1); st != Accepted {
		t.Fatalf("revision 1 is %s before r3 is accepted", st)
	}
	r.Move(ctx, s3.Revision, Proposed, "me", "", manifest(), t0)
	if _, err := r.Move(ctx, s3.Revision, Accepted, "me", "", manifest(), t0); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := r.State(ctx, a.PlanID, 1); st != Superseded {
		t.Fatalf("revision 1 is %s after r3 is accepted", st)
	}
}

func TestStaleInputsBlockAcceptanceButNotReading(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	a, _ := New(card(), manifest(), t0)
	r.Save(ctx, a, t0)
	r.Move(ctx, a, Proposed, "me", "", manifest(), t0)
	cur := manifest()
	cur.Policy, cur.PolicyEpoch, cur.Ledger = "p2", 2, "l2"
	_, err := r.Move(ctx, a, Accepted, "me", "", cur, t0)
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "policy") || !strings.Contains(err.Error(), "book") {
		t.Fatalf("%v", err)
	}
	if got, err := r.Get(ctx, a.PlanID, 1); err != nil || got.Verify() != nil {
		t.Fatalf("a stale revision stays readable: %v", err)
	}
	if ch := a.Manifest.Changed(manifest()); len(ch) != 0 {
		t.Errorf("an unchanged manifest reported %v", ch)
	}
}

func TestExpiryAndDecisionRules(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	a, _ := New(card(), manifest(), t0)
	r.Save(ctx, a, t0)
	if _, err := r.Move(ctx, a, Proposed, "", "", manifest(), t0); err == nil {
		t.Error("a decision without a name")
	}
	if _, err := r.Move(ctx, a, Rejected, "me", " ", manifest(), t0); err == nil {
		t.Error("a rejection without a reason")
	}
	if _, err := r.Move(ctx, a, Accepted, "me", "", manifest(), t0); err == nil {
		t.Error("draft -> accepted")
	}
	late := t0.Add(5 * 24 * time.Hour)
	if got := Effective(Proposed, "2026-11-06", late); got != Expired {
		t.Errorf("effective state %s", got)
	}
	if got := Effective(Proposed, "2026-11-06", time.Date(2026, 11, 6, 23, 59, 0, 0, time.UTC)); got != Proposed {
		t.Errorf("valid through the end of its expiry day, got %s", got)
	}
	if _, err := r.Move(ctx, a, Proposed, "me", "", manifest(), late); err == nil || !strings.Contains(err.Error(), "expired on 2026-11-06") {
		t.Errorf("proposing an expired plan: %v", err)
	}
	if _, err := r.Move(ctx, a, Expired, "system", "past its expiry", manifest(), late); err != nil {
		t.Errorf("recording the expiry: %v", err)
	}
	if st, log, _ := r.State(ctx, a.PlanID, 1); st != Expired || len(log) != 1 || log[0].Seq != 1 {
		t.Errorf("%s %v", st, log)
	}
	if _, err := r.Move(ctx, a, Proposed, "me", "", manifest(), t0); err == nil {
		t.Error("an expired plan was revived")
	}
}

func TestTamperedRevisionIsDetected(t *testing.T) {
	a, _ := New(card(), manifest(), t0)
	a.Card.Sizing.Qty = "1000"
	if err := a.Verify(); err == nil || !strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("%v", err)
	}
	b, _ := New(card(), manifest(), t0)
	b.Manifest.Ledger = "other"
	if err := b.Verify(); err == nil {
		t.Fatal("a changed manifest was accepted")
	}
}

func TestPlanIDIsTheQuestionNotTheAnswer(t *testing.T) {
	if PlanID("b", "s", "core", "x") != PlanID("b", "s", "core", "x") || PlanID("b", "s", "core", "x") == PlanID("b", "s", "event", "x") || PlanID("b", "s", "core", "x") == PlanID("c", "s", "core", "x") {
		t.Fatal("plan identity")
	}
}

func TestPlansListsTheLatestRevisionOfEach(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	a, _ := New(card(), manifest(), t0)
	r.Save(ctx, a, t0)
	c2 := card()
	c2.Security = "sec_sap"
	b, _ := New(c2, manifest(), t0.Add(time.Minute))
	r.Save(ctx, b, t0.Add(time.Minute))
	c3 := card()
	c3.Sizing.Qty = "9"
	d, _ := New(c3, manifest(), t0.Add(time.Hour))
	r.Save(ctx, d, t0.Add(time.Hour))
	ps, err := r.Plans(ctx)
	if err != nil || len(ps) != 2 {
		t.Fatalf("%v %d", err, len(ps))
	}
	if ps[0].Card.Security != "sec_apple" || ps[0].Rev != 2 {
		t.Errorf("newest first: %+v", ps[0].ID())
	}
	if _, err := r.Latest(ctx, "pl_none"); err == nil {
		t.Error("an unknown plan")
	}
}
