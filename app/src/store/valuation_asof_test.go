package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/universe"
	"github.com/b070nd/stoxicity/app/src/valuation"
)

// A past valuation excludes filings, mappings, prices and splits that came later,
// shows the inputs it used, and says whether it is what the installation held or
// a reconstruction.
func TestAHistoricalValuationExcludesWhatCameLaterAndSaysWhatItIs(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	d := func(s string) time.Time { v, _ := time.Parse("2006-01-02 15:04", s); return v }
	const cik = "0000000042"

	for _, r := range []fundamentals.CoverRead{
		{CIK: cik, Accession: "a-1", Form: "10-Q", Filed: "2026-08-01", At: d("2026-08-02 08:00"), Classes: []fundamentals.ClassShares{{AsOf: "2026-07-25", Shares: 100}}},
		{CIK: cik, Accession: "a-2", Form: "10-Q", Filed: "2026-10-30", At: d("2026-10-31 08:00"), Classes: []fundamentals.ClassShares{{AsOf: "2026-10-24", Shares: 120}}},
	} {
		if err := db.PutCover(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// The mapping was made on 1 November, after the first filing's time.
	if n, err := db.PutClassMaps(ctx, []valuation.Mapping{{CIK: cik, SecurityID: "sec_1", Ticker: "ACME", Status: valuation.Mapped,
		Method: valuation.MethodSoleListing, Confidence: 0.8, Basis: "one class, one listing", Accession: "a-2", MappedAt: d("2026-11-01 09:00")}}); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	put := func(fetched time.Time, cs ...marketdata.Candle) {
		db.now = func() time.Time { return fetched }
		if err := db.PutCandles(ctx, cs); err != nil {
			t.Fatal(err)
		}
	}
	cd := func(day string, closePrice float64) marketdata.Candle {
		return marketdata.Candle{Ticker: "ACME", Day: day, Open: closePrice, High: closePrice, Low: closePrice, Close: closePrice, Raw: true}
	}
	put(d("2026-08-29 06:00"), cd("2026-08-28", 10))
	put(d("2026-11-05 06:00"), cd("2026-08-30", 11), cd("2026-11-04", 20)) // a backfill, after the time asked about
	if err := db.PutSplits(ctx, nil, d("2026-01-01 00:00"), d("2026-08-31 00:00")); err != nil {
		t.Fatal(err)
	}
	if err := db.PutSplits(ctx, []marketdata.Split{{Ticker: "ACME", Day: "2026-10-15", From: 1, To: 2}}, d("2026-01-01 00:00"), d("2026-11-05 07:00")); err != nil {
		t.Fatal(err)
	}
	profiles := []universe.Profile{{Ticker: "ACME", Name: "Acme Corp", CIK: cik, Exchange: "Nasdaq"}}
	now := d("2026-11-06 12:00")
	value := func(v valuation.View) valuation.Value {
		t.Helper()
		set, err := valuation.Build(ctx, db, profiles, v, now)
		if err != nil {
			t.Fatal(err)
		}
		return set.ByCIK[cik]
	}
	at := d("2026-09-01 00:00")

	// Today: the newer count, the newer price, the split between them counted in neither.
	cur := value(valuation.View{})
	if !cur.IssuerOK || cur.Accession != "a-2" || cur.Issuer != 120*20 || cur.Currency != "USD" {
		t.Errorf("current = %+v", cur)
	}
	// What the installation held on 1 September: the first filing and the price it
	// had stored, but no mapping yet, so no value, and it says why.
	known := value(valuation.View{Mode: valuation.ModeKnown, At: at})
	if known.Accession != "a-1" || known.IssuerOK || !strings.Contains(known.IssuerReason, "not been mapped") || known.View.Mode != valuation.ModeKnown {
		t.Errorf("known = %+v", known)
	}
	if known.Coverage.Since != "2026-01-01" || !known.Coverage.Read.Equal(d("2026-08-31 00:00")) {
		t.Errorf("the split list as read by then: %+v", known.Coverage)
	}
	// The reconstruction uses the 30 August session found in November, today's mapping,
	// and not the later filing or the October split.
	rec := value(valuation.View{Mode: valuation.ModeReconstructed, At: at})
	if !rec.IssuerOK || rec.Accession != "a-1" || rec.Issuer != 100*11 || len(rec.Classes[0].Splits) != 0 {
		t.Errorf("reconstructed = %+v", rec)
	}
	cv := rec.Classes[0]
	if cv.Price.Day != "2026-08-30" || !cv.Price.Fetched.Equal(d("2026-11-05 06:00")) || cv.QuoteCurrency != "USD" || cv.CurrencyBasis == "" || cv.Mapping.MappedAt.IsZero() || cv.Received.IsZero() {
		t.Errorf("the inputs are shown with their availability times: %+v", cv)
	}
	if !strings.Contains(rec.View.Caveat(), "not what the installation showed then") {
		t.Error("a reconstruction must say it is one")
	}
	// Known later, after the mapping and the backfill, the reconstruction and the record agree.
	later := value(valuation.View{Mode: valuation.ModeKnown, At: d("2026-11-05 12:00")})
	if later.Accession != "a-2" || !later.IssuerOK {
		t.Errorf("known on 5 November = %+v", later)
	}
}
