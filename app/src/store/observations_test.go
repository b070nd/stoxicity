package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/quotes"
)

func rateObs(v, day, source string) quotes.FXObs {
	return quotes.FXObs{Base: "EUR", Quote: "USD", Value: money.MustDecimal(v), Basis: money.BasisReference, Day: day, Source: source}
}

func TestFXObservationsAreExactAppendOnlyAndAsOf(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	at1, at2, at3 := t0.Add(time.Hour), t0.Add(48*time.Hour), t0.Add(96*time.Hour)

	c, err := db.PutFX(ctx, []quotes.FXObs{rateObs("1.0850", "2026-11-06", "ecb"), rateObs("1.0900", "2026-11-05", "ecb")}, at1)
	if err != nil || c.New != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	// Reading the same document again stores nothing.
	if c, _ = db.PutFX(ctx, []quotes.FXObs{rateObs("1.0850", "2026-11-06", "ecb")}, at2); c.New != 0 || c.Unchanged != 1 {
		t.Errorf("%+v", c)
	}
	// A correction is kept beside the original.
	if c, _ = db.PutFX(ctx, []quotes.FXObs{rateObs("1.0851", "2026-11-06", "ecb")}, at3); c.New != 1 {
		t.Errorf("%+v", c)
	}
	all, _ := db.FXObservations(ctx, "USD", "EUR", time.Time{})
	if len(all) != 3 || all[0].Value.String() != "1.0900" {
		t.Fatalf("%+v", all)
	}
	need := quotes.Need{Day: "2026-11-06"}
	now := quotes.ResolveFX(all, "EUR", "USD", need)
	if now.Verdict != quotes.OK || now.Rate.Value.String() != "1.0851" {
		t.Errorf("%+v", now)
	}
	early, _ := db.FXObservations(ctx, "EUR", "USD", at2)
	then := quotes.ResolveFX(early, "EUR", "USD", need)
	if then.Rate == nil || then.Rate.Value.String() != "1.0850" {
		t.Errorf("what was known before the correction: %+v", then)
	}

	// One bad row stores none.
	bad := rateObs("1.1", "2026-11-07", "ecb")
	bad.Value = money.Int(0)
	if _, err := db.PutFX(ctx, []quotes.FXObs{rateObs("1.2", "2026-11-07", "ecb"), bad}, at3); err == nil {
		t.Fatal("accepted")
	}
	if all, _ = db.FXObservations(ctx, "EUR", "USD", time.Time{}); len(all) != 3 {
		t.Errorf("a refused batch left rows: %d", len(all))
	}
	sums, _ := db.FXSummaries(ctx)
	if len(sums) != 1 || sums[0].Count != 3 || sums[0].First != "2026-11-05" || sums[0].Last != "2026-11-06" {
		t.Errorf("%+v", sums)
	}

	h, _ := db.baseHandle()
	if _, err := h.w.ExecContext(ctx, `UPDATE fx_obs SET value = '9'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("update allowed: %v", err)
	}
	if _, err := h.w.ExecContext(ctx, `DELETE FROM fx_obs`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("delete allowed: %v", err)
	}
}

func TestPriceObservationsRoundTripTheUnit(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	reg, _, _ := instrument.New(instrument.ExampleSet())
	ba, err := quotes.RegistryResolver(reg)("XLON", "BA", "2026-11-06")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := money.ParseQuoteUnit("GBp")
	p, _ := money.NewPrice(money.MustDecimal("1234.50"), u)
	o := quotes.PriceObs{ListingID: ba.ID, Day: "2026-11-06", Kind: quotes.Close, Price: p, UnitBasis: quotes.UnitStated, Adjustment: quotes.Raw,
		Delay: quotes.EndOfDay, QuoteTime: t0, Source: "file", SourceRef: "sha256:x"}
	if c, err := db.PutPrices(ctx, []quotes.PriceObs{o}, t0); err != nil || c.New != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if c, _ := db.PutPrices(ctx, []quotes.PriceObs{o}, t0.Add(time.Hour)); c.New != 0 || c.Unchanged != 1 {
		t.Errorf("%+v", c)
	}
	got, err := db.PriceObservations(ctx, ba.ID, time.Time{})
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	g := got[0]
	if g.Price.Value.String() != "1234.50" || g.Price.Unit.Code != "GBp" || g.Price.Unit.Divisor != 100 || !g.QuoteTime.Equal(t0) || g.Received.IsZero() {
		t.Errorf("%+v", g)
	}
	a := quotes.ResolvePrice(got, ba, quotes.Need{Day: "2026-11-06", RequireStatedUnit: true})
	if a.Verdict != quotes.OK {
		t.Errorf("%+v", a)
	}
	if none, _ := db.PriceObservations(ctx, ba.ID, t0.Add(-time.Hour)); len(none) != 0 {
		t.Error("an observation was visible before it was received")
	}
}
