package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/policy"
)

func samplePolicy(t *testing.T, issuer string) policy.Policy {
	t.Helper()
	p, err := policy.Example("conservative-eur-1000")
	if err != nil {
		t.Fatal(err)
	}
	p.Example = false
	p.Name = "mine"
	p.Constraints.MaxIssuer = money.MustDecimal(issuer)
	return p
}

func activate(t *testing.T, st policy.Store, p policy.Policy, at time.Time, accept bool) policy.Record {
	t.Helper()
	h, err := policy.Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := policy.Activate(context.Background(), st, p, h, accept, at)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestPolicyActivationsAreAppendOnlyAndReadableAsOf(t *testing.T) {
	ctx := context.Background()
	db, dir := openTemp(t)
	st := db.Policies("default")

	if _, ok, err := st.AsOf(ctx, time.Time{}); err != nil || ok {
		t.Fatalf("no policy yet: %v %v", ok, err)
	}

	a := samplePolicy(t, "0.10")
	b := samplePolicy(t, "0.05") // tighter: needs no acceptance
	t1, t2 := t0, t0.Add(48*time.Hour)
	ra := activate(t, st, a, t1, false)
	rb := activate(t, st, b, t2, false)
	if ra.Epoch != 1 || rb.Epoch != 2 {
		t.Fatalf("epochs %d %d", ra.Epoch, rb.Epoch)
	}
	if len(rb.Changes) == 0 || len(rb.Effects) == 0 {
		t.Errorf("the activation records what changed and what it invalidates: %+v", rb)
	}

	// Replay at a time between the two sees only the first.
	got, ok, err := st.AsOf(ctx, t1.Add(time.Hour))
	if err != nil || !ok || got.Epoch != 1 || got.Policy.Constraints.MaxIssuer.String() != "0.10" {
		t.Fatalf("as of t1+1h: %+v %v %v", got.Epoch, ok, err)
	}
	if _, ok, _ := st.AsOf(ctx, t1.Add(-time.Second)); ok {
		t.Error("nothing was in force before the first activation")
	}
	now, _, _ := st.AsOf(ctx, time.Time{})
	if now.Epoch != 2 || now.Hash != rb.Hash {
		t.Errorf("now: %+v", now)
	}
	hist, err := st.History(ctx)
	if err != nil || len(hist) != 2 || hist[0].Epoch != 1 {
		t.Fatalf("history: %v %v", hist, err)
	}

	// The stored policy reads back to the same hash.
	if h, _ := policy.Hash(now.Policy); h != now.Hash {
		t.Error("the stored policy does not hash to its recorded hash")
	}

	// The rows cannot be changed or removed, even by SQL.
	h, err := db.Tenant("default").(*tenantStore).handle()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE policy_activation SET at_ms = 0`, `DELETE FROM policy_activation`,
		`UPDATE policy_version SET content = '{}'`, `DELETE FROM policy_version`,
	} {
		if _, err := h.w.ExecContext(ctx, q); err == nil {
			t.Errorf("%s succeeded", q)
		}
	}

	// It survives a reopen.
	db.Close()
	db2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	again, ok, err := db2.Policies("default").AsOf(ctx, time.Time{})
	if err != nil || !ok || again.Hash != rb.Hash {
		t.Errorf("after reopen: %+v %v %v", again.Epoch, ok, err)
	}
}

func TestPolicyActivationAgainstAnOldEpochIsRefused(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	st := db.Policies("default")
	activate(t, st, samplePolicy(t, "0.10"), t0, false)

	// Two candidates were previewed against epoch 1.
	b, c := samplePolicy(t, "0.08"), samplePolicy(t, "0.07")
	hb, _ := policy.Hash(b)
	hc, _ := policy.Hash(c)
	if _, err := st.Append(ctx, policy.Activation{PrevEpoch: 1, Hash: hb, Policy: b, At: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, policy.Activation{PrevEpoch: 1, Hash: hc, Policy: c, At: t0.Add(2 * time.Hour)}); !errors.Is(err, policy.ErrStale) {
		t.Errorf("second activation against epoch 1: %v", err)
	}
	// And a clock that went backwards cannot reorder history.
	if _, err := st.Append(ctx, policy.Activation{PrevEpoch: 2, Hash: hc, Policy: c, At: t0}); err == nil {
		t.Error("an activation dated before the previous one")
	}
	hist, _ := st.History(ctx)
	if len(hist) != 2 {
		t.Errorf("refused activations left rows: %d", len(hist))
	}
}

func TestPolicyIsPerTenantAndRejectsBadTenants(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	activate(t, db.Policies("alice"), samplePolicy(t, "0.10"), t0, false)
	if _, ok, _ := db.Policies("bob").AsOf(ctx, time.Time{}); ok {
		t.Error("bob sees alice's policy")
	}
	if _, _, err := db.Policies("../x").AsOf(ctx, time.Time{}); err == nil {
		t.Error("a path-like tenant ID must fail")
	}
}

func TestPolicyVersionIsSharedWhenContentRepeats(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	st := db.Policies("default")
	a, b := samplePolicy(t, "0.10"), samplePolicy(t, "0.05")
	activate(t, st, a, t0, false)
	activate(t, st, b, t0.Add(time.Hour), false)
	activate(t, st, a, t0.Add(2*time.Hour), true) // back to the first: riskier than b
	h, _ := db.Tenant("default").(*tenantStore).handle()
	var versions, activations int
	h.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM policy_version`).Scan(&versions)
	h.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM policy_activation`).Scan(&activations)
	if versions != 2 || activations != 3 {
		t.Errorf("%d versions and %d activations", versions, activations)
	}
}
