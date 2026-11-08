package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/money"
)

func payout(period string, st distribution.Status, amt, ex, source string) distribution.Obs {
	return distribution.Obs{SecurityID: "sec_apple", Type: distribution.Ordinary, Period: period, Status: st, Amount: money.MustDecimal(amt),
		Currency: "USD", ExDay: ex, Source: source}
}

func TestDistributionRevisionsAreAppendedAndReadableAsOf(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	at1, at2, at3 := t0.Add(time.Hour), t0.Add(48*time.Hour), t0.Add(96*time.Hour)

	c, err := db.PutDistributions(ctx, []distribution.Obs{payout("2026Q3", distribution.Declared, "0.25", "2026-08-11", "co")}, at1)
	if err != nil || c.New != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if c, _ = db.PutDistributions(ctx, []distribution.Obs{payout("2026Q3", distribution.Declared, "0.25", "2026-08-11", "co")}, at2); c.New != 0 || c.Unchanged != 1 {
		t.Errorf("%+v", c)
	}
	// The same source later cancels it.
	cancel := payout("2026Q3", distribution.Cancelled, "0", "2026-08-11", "co")
	if c, _ = db.PutDistributions(ctx, []distribution.Obs{cancel}, at3); c.New != 1 {
		t.Errorf("%+v", c)
	}

	all, _ := db.DistributionObservations(ctx, []string{"sec_apple"}, time.Time{})
	if len(all) != 2 || all[0].Amount.String() != "0.25" {
		t.Fatalf("%+v", all)
	}
	if p := distribution.Resolve(all, time.Time{})[0]; p.State != distribution.StateCancelled || p.Revisions != 2 {
		t.Errorf("%+v", p)
	}
	early, _ := db.DistributionObservations(ctx, nil, at2)
	if p := distribution.Resolve(early, time.Time{})[0]; p.State != distribution.StateDeclared {
		t.Errorf("what was known before the cancellation: %+v", p)
	}
	if other, _ := db.DistributionObservations(ctx, []string{"sec_sap"}, time.Time{}); len(other) != 0 {
		t.Error("another security's rows")
	}

	// A refused batch stores nothing.
	bad := payout("2026Q4", distribution.Declared, "0.25", "2026-11-11", "co")
	bad.Currency = ""
	if _, err := db.PutDistributions(ctx, []distribution.Obs{payout("2027Q1", distribution.Declared, "0.25", "2027-02-10", "co"), bad}, at3); err == nil {
		t.Fatal("accepted")
	}
	if all, _ = db.DistributionObservations(ctx, nil, time.Time{}); len(all) != 2 {
		t.Errorf("a refused batch left rows: %d", len(all))
	}

	h, _ := db.baseHandle()
	if _, err := h.w.ExecContext(ctx, `UPDATE distribution_obs SET amount = '9'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("update allowed: %v", err)
	}
	if _, err := h.w.ExecContext(ctx, `DELETE FROM distribution_obs`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("delete allowed: %v", err)
	}
}
