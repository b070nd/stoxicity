package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/instrument"
)

func TestInstrumentsImportIsIdempotentAndRevisionsAreAppendOnly(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	set := instrument.ExampleSet()
	at1 := t0.Add(time.Hour)

	rep, err := db.PutInstruments(ctx, set, "fixture", at1)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Securities.New != 5 || rep.Listings.New != 7 || rep.Issuers.New != 4 {
		t.Fatalf("first import: %+v", rep)
	}
	again, err := db.PutInstruments(ctx, set, "fixture", at1.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if again.Securities.New+again.Securities.Revised+again.Listings.New+again.Listings.Revised+again.Issuers.New+again.Issuers.Revised != 0 ||
		again.Securities.Unchanged != 5 || again.Listings.Unchanged != 7 {
		t.Fatalf("an unchanged import wrote rows: %+v", again)
	}

	// A correction is a new revision; what was known before stays readable.
	fixed := instrument.ExampleSet()
	fixed.Securities[1].Name = "SAP SE (corrected)"
	at2 := at1.Add(48 * time.Hour)
	rep, err = db.PutInstruments(ctx, fixed, "fixture", at2)
	if err != nil || rep.Securities.Revised != 1 || rep.Securities.Unchanged != 4 {
		t.Fatalf("correction: %+v, %v", rep, err)
	}
	now, _ := db.InstrumentsAsOf(ctx, time.Time{})
	then, _ := db.InstrumentsAsOf(ctx, at1.Add(time.Hour))
	name := func(s instrument.Set) string {
		for _, x := range s.Securities {
			if x.ID == "sec_sap" {
				return x.Name
			}
		}
		return ""
	}
	if name(now) != "SAP SE (corrected)" || name(then) != "SAP SE ordinary shares" {
		t.Errorf("as-of read: now %q, then %q", name(now), name(then))
	}
	before, _ := db.InstrumentsAsOf(ctx, t0)
	if len(before.Securities) != 0 {
		t.Errorf("before the first import there was nothing: %+v", before)
	}

	// Nothing can be rewritten behind the importer's back.
	h, _ := db.baseHandle()
	if _, err := h.w.ExecContext(ctx, `UPDATE listing_revision SET ticker = 'X'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("update allowed: %v", err)
	}
	if _, err := h.w.ExecContext(ctx, `DELETE FROM security_revision`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("delete allowed: %v", err)
	}
}

func TestInstrumentsImportRoundTripsAndResolves(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	if _, err := db.PutInstruments(ctx, instrument.ExampleSet(), "fixture", t0); err != nil {
		t.Fatal(err)
	}
	set, err := db.InstrumentsAsOf(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	reg, problems, err := instrument.New(set)
	if err != nil || len(instrument.Errors(problems)) > 0 {
		t.Fatalf("stored set does not validate: %v %v", err, problems)
	}
	// A subunit quote survives storage.
	r := reg.ResolveListing(instrument.Query{MIC: "XLON", Ticker: "BA", Day: "2026-06-01"})
	if r.Status != instrument.Resolved || r.Listing.QuoteUnit != "GBp" || r.Listing.Currency != "GBP" {
		t.Errorf("%+v", r)
	}
	// A cross-listed fund is ambiguous across venues until one is named.
	r = reg.ResolveListing(instrument.Query{Ticker: "EXWD", Day: "2026-06-01"})
	if r.Status != instrument.Ambiguous {
		t.Errorf("%+v", r)
	}
}

func TestInstrumentsImportRefusesInvalidSetsWholesale(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	set := instrument.ExampleSet()
	set.Listings[2].QuoteUnit = "" // a listing with no quote unit
	if _, err := db.PutInstruments(ctx, set, "fixture", t0); err == nil {
		t.Fatal("accepted")
	}
	got, _ := db.InstrumentsAsOf(ctx, time.Time{})
	if len(got.Securities)+len(got.Listings)+len(got.Issuers) != 0 {
		t.Errorf("a refused import left rows: %+v", got)
	}
	if _, err := db.PutInstruments(ctx, instrument.ExampleSet(), " ", t0); err == nil {
		t.Error("a source is required")
	}
}

func TestInstrumentsLinkToTheSecurityAlreadyInTheHistory(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	h, _ := db.baseHandle()
	if _, err := h.w.ExecContext(ctx, `INSERT INTO security (id, cik, name, created_ms) VALUES ('sec_hist', '0000320193', 'Apple', 1);
		INSERT INTO security_ticker (security_id, ticker, from_day) VALUES ('sec_hist', 'AAPL', '2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	set := instrument.ExampleSet()
	set.Securities[0].LinkTicker = "AAPL"
	rep, err := db.PutInstruments(ctx, set, "fixture", t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Linked) != 1 || !strings.Contains(rep.Linked[0], "sec_hist") {
		t.Fatalf("linked = %v", rep.Linked)
	}
	got, _ := db.InstrumentsAsOf(ctx, time.Time{})
	reg, _, _ := instrument.New(got)
	r := reg.ResolveListing(instrument.Query{MIC: "XNAS", Ticker: "AAPL", Day: "2026-06-01"})
	if r.Status != instrument.Resolved || r.Security.ID != "sec_hist" {
		t.Errorf("the listing must carry the historical identity: %+v", r)
	}
	var n int
	h.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM security WHERE id = 'sec_apple'`).Scan(&n)
	if n != 0 {
		t.Error("a second security was created for a ticker the history already had")
	}
}
