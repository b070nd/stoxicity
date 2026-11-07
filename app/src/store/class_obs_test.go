package store

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/valuation"
)

func TestPutCoverKeepsDuplicatesConflictsAndRevisionsWithoutOverwriting(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	const dimA, dimB = "ax=a:ClassAMember", "ax=a:ClassBMember"
	read := fundamentals.CoverRead{CIK: "0000000001", Accession: "0000000001-26-000001", Form: "10-Q", Filed: "2026-10-30", At: at,
		Accepted: time.Date(2026, 10, 30, 16, 5, 0, 0, time.UTC),
		Classes: []fundamentals.ClassShares{
			{Dimensions: dimA, AsOf: "2026-10-20", Shares: 100, Ticker: "AA"},
			{Dimensions: dimA, AsOf: "2026-10-20", Shares: 100}, // the same count in a second context
			{Dimensions: dimB, AsOf: "2026-10-20", Shares: 50},  // class B ...
			{Dimensions: dimB, AsOf: "2026-10-20", Shares: 55},  // ... stated twice, differently
		}}
	if err := db.PutCover(ctx, read); err != nil {
		t.Fatal(err)
	}
	// The same reading again changes nothing.
	read.At = at.Add(time.Hour)
	if err := db.PutCover(ctx, read); err != nil {
		t.Fatal(err)
	}
	obs, err := db.ClassObs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 3 {
		t.Fatalf("duplicate contexts are one row, a different count another: %+v", obs)
	}
	var dup valuation.Obs
	for _, o := range obs {
		if o.Dimensions == dimA {
			dup = o
		}
	}
	if dup.Duplicates != 2 || dup.Ticker != "AA" || !dup.Accepted.Equal(read.Accepted) || !dup.Received.Equal(at) {
		t.Errorf("the first reading is kept, with how many facts it stands for and when it became public: %+v", dup)
	}
	pick, ok := valuation.Select(obs, time.Time{})
	if !ok {
		t.Fatal("nothing picked")
	}
	for _, c := range pick.Classes {
		if c.Dimensions == dimB && (c.Issue() == "" || len(c.Conflict) != 2) {
			t.Errorf("class B is in conflict and is not resolved by the store: %+v", c)
		}
	}
	// An amendment stating another count is another row; the first stays.
	am := fundamentals.CoverRead{CIK: "0000000001", Accession: "0000000001-26-000002", Form: "10-Q/A", Filed: "2026-11-02", At: at.Add(24 * time.Hour),
		Classes: []fundamentals.ClassShares{{Dimensions: dimA, AsOf: "2026-10-20", Shares: 120}}}
	if err := db.PutCover(ctx, am); err != nil {
		t.Fatal(err)
	}
	obs, _ = db.ClassObs(ctx)
	if len(obs) != 4 {
		t.Fatalf("%d rows", len(obs))
	}
	// As known after the amendment: the amendment's count, flagged as revised. Before: the original.
	after, _ := valuation.Select(obs, at.Add(72*time.Hour))
	before, _ := valuation.Select(obs, at.Add(2*time.Hour))
	if after.Accession != "0000000001-26-000002" || !after.Classes[0].Revised || after.Classes[0].Was != 100 {
		t.Errorf("after: %+v", after)
	}
	if before.Accession != "0000000001-26-000001" {
		t.Errorf("a historical reading excludes what was stored and public only later: %+v", before)
	}
	checks, _ := db.CoverChecks(ctx)
	if checks["0000000001"].Accession != "0000000001-26-000002" || checks["0000000001"].Problem != "" {
		t.Errorf("%+v", checks)
	}
	if err := db.PutCoverCheck(ctx, "0000000001", "", "boom", at.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c, _ := db.CoverChecks(ctx); c["0000000001"].Problem != "boom" || c["0000000001"].Accession != "0000000001-26-000002" {
		t.Errorf("a failure keeps the accession read before: %+v", c)
	}
}

func TestClassMapsAreAppendedOnlyWhenTheyChangeAndReadAsOfATime(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	m := valuation.Mapping{CIK: "0000000001", Dimensions: "d", Status: valuation.Unmapped, Method: valuation.MethodNone, Basis: "no listing", Accession: "a", MappedAt: t0}
	if n, err := db.PutClassMaps(ctx, []valuation.Mapping{m}); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if n, _ := db.PutClassMaps(ctx, []valuation.Mapping{{CIK: m.CIK, Dimensions: "d", Status: valuation.Unmapped, Method: valuation.MethodNone, Basis: "no listing, said again", Accession: "a", MappedAt: t0.Add(time.Hour)}}); n != 0 {
		t.Errorf("the same answer is not stored again: %d", n)
	}
	later := valuation.Mapping{CIK: m.CIK, Dimensions: "d", SecurityID: "sec_1", Ticker: "XYZ", Status: valuation.Mapped, Method: valuation.MethodCoverSymbol, Confidence: 0.95, Basis: "symbol", Accession: "b", MappedAt: t0.Add(48 * time.Hour)}
	if n, _ := db.PutClassMaps(ctx, []valuation.Mapping{later}); n != 1 {
		t.Fatalf("a changed mapping is appended: %d", n)
	}
	now, _ := db.ClassMaps(ctx, time.Time{})
	was, _ := db.ClassMaps(ctx, t0.Add(24*time.Hour))
	none, _ := db.ClassMaps(ctx, t0.Add(-time.Hour))
	if now["0000000001"]["d"].SecurityID != "sec_1" || was["0000000001"]["d"].Status != valuation.Unmapped || len(none) != 0 {
		t.Errorf("a new relationship is never backdated:\nnow %+v\nwas %+v\nnone %+v", now, was, none)
	}
	hist, _ := db.ClassMapHistory(ctx, "0000000001")
	if len(hist) != 2 || hist[0].Status != valuation.Unmapped || hist[1].Method != valuation.MethodCoverSymbol {
		t.Errorf("history keeps both: %+v", hist)
	}
}

func TestMapClassesUsesTheNewestKnownCountAndCreatesNoSecurityTwice(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	if err := db.PutCover(ctx, fundamentals.CoverRead{CIK: "0000000009", Accession: "x-1", Form: "10-K", Filed: "2026-10-01", At: at,
		Classes: []fundamentals.ClassShares{{AsOf: "2026-09-20", Shares: 10}}}); err != nil {
		t.Fatal(err)
	}
	id1, err := db.SecurityOf(ctx, "ONLY", at)
	if err != nil {
		t.Fatal(err)
	}
	if id2, _ := db.SecurityOf(ctx, "ONLY", at.Add(time.Hour)); id2 != id1 {
		t.Errorf("a ticker keeps its security: %s %s", id1, id2)
	}
	n, err := db.MapClasses(ctx, "0000000009", []valuation.Listing{{SecurityID: id1, Ticker: "ONLY", Type: "stock"}}, at)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	got, _ := db.ClassMaps(ctx, time.Time{})
	if m := got["0000000009"][""]; m.SecurityID != id1 || m.Status != valuation.Mapped || m.Basis == "" {
		t.Errorf("%+v", m)
	}
	if n, _ := db.MapClasses(ctx, "0000000009", []valuation.Listing{{SecurityID: id1, Ticker: "ONLY", Type: "stock"}}, at.Add(time.Hour)); n != 0 {
		t.Errorf("mapping again with the same result stores nothing: %d", n)
	}
	if n, err := db.MapClasses(ctx, "0000000404", nil, at); err != nil || n != 0 {
		t.Errorf("an issuer without a count has nothing to map: %d %v", n, err)
	}
}
