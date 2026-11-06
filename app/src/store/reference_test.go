package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

var refTickers = []RefTicker{
	{"NVDA", "NVIDIA CORP", "0001045810", "Nasdaq"},
	{"AAPL", "Apple Inc.", "0000320193", "Nasdaq"},
	{"KO", "COCA COLA CO", "0000021344", "NYSE"},
}

func at(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }

func TestReplaceTickersFirstLoadIsASnapshotWithoutChanges(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	d, err := db.ReplaceTickers(ctx, RefSource{ETag: `"v1"`}, refTickers, at(0))
	if err != nil || d.Same || d.Added != 3 || d.Hash == "" {
		t.Fatalf("ReplaceTickers = %+v, %v", d, err)
	}
	got, _ := db.RefTickers(ctx)
	if got[0].Ticker != "AAPL" || got[2].Ticker != "NVDA" {
		t.Errorf("rows are not in ticker order: %v", got)
	}
	src, ok, _ := db.RefSource(ctx, SourceTickers)
	if !ok || src.Hash != d.Hash || src.Records != 3 || src.ETag != `"v1"` || !src.ChangedAt.Equal(at(0)) {
		t.Errorf("source = %+v", src)
	}
	if snaps, _ := db.RefSnapshots(ctx, SourceTickers, 10); len(snaps) != 1 || snaps[0].Hash != d.Hash {
		t.Errorf("snapshots = %v", snaps)
	}
	if ch, _ := db.RefChanges(ctx, time.Time{}, 10); len(ch) != 0 {
		t.Errorf("the first load logged %d changes", len(ch))
	}
}

func TestSameContentInAnyOrderChangesNothing(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	first, _ := db.ReplaceTickers(ctx, RefSource{ETag: `"v1"`}, refTickers, at(0))
	shuffled := []RefTicker{refTickers[2], refTickers[0], refTickers[1]}
	d, err := db.ReplaceTickers(ctx, RefSource{ETag: `"v2"`}, shuffled, at(5))
	if err != nil || !d.Same || d.Hash != first.Hash {
		t.Fatalf("ReplaceTickers = %+v, %v; want the same hash %s", d, err, first.Hash)
	}
	src, _, _ := db.RefSource(ctx, SourceTickers)
	if !src.CheckedAt.Equal(at(5)) || !src.ChangedAt.Equal(at(0)) || src.ETag != `"v2"` {
		t.Errorf("source = %+v, want checked at 5h, changed at 0h", src)
	}
	if snaps, _ := db.RefSnapshots(ctx, SourceTickers, 10); len(snaps) != 1 {
		t.Errorf("%d snapshots, want 1", len(snaps))
	}
}

func TestReplaceTickersLogsWhatChanged(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	db.ReplaceTickers(ctx, RefSource{}, refTickers, at(0))
	next := []RefTicker{
		{"AAPL", "Apple Inc.", "0000320193", "Nasdaq"},
		{"KO", "COCA-COLA CO", "0000021344", "NYSE"}, // renamed
		{"NEW", "New Co", "0000999999", "NYSE"},      // added; NVDA is gone
	}
	d, err := db.ReplaceTickers(ctx, RefSource{}, next, at(24))
	if err != nil || d.Added != 1 || d.Removed != 1 || d.Changed != 1 {
		t.Fatalf("ReplaceTickers = %+v, %v", d, err)
	}
	ch, _ := db.RefChanges(ctx, time.Time{}, 10)
	var got []string
	for _, c := range ch {
		got = append(got, c.Kind+":"+c.Key)
	}
	// Newest first; within one refresh: the rows in key order, then the removals.
	if want := []string{"removed:NVDA", "added:NEW", "changed:KO"}; !reflect.DeepEqual(got, want) {
		t.Errorf("changes = %v, want %v", got, want)
	}
	if ch[2].Detail != `"COCA COLA CO" -> "COCA-COLA CO"` {
		t.Errorf("detail = %q", ch[2].Detail)
	}
	if snaps, _ := db.RefSnapshots(ctx, SourceTickers, 10); len(snaps) != 2 || snaps[0].Records != 3 {
		t.Errorf("snapshots = %v", snaps)
	}
	if later, _ := db.RefChanges(ctx, at(12), 10); len(later) != 3 {
		t.Errorf("since 12h: %d changes", len(later))
	}
}

func TestReplaceRejectsDuplicateKeysAndKeepsTheOldRows(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	db.ReplaceTickers(ctx, RefSource{}, refTickers, at(0))
	_, err := db.ReplaceTickers(ctx, RefSource{}, []RefTicker{{"X", "A", "1", ""}, {"X", "B", "2", ""}}, at(1))
	if err == nil {
		t.Fatal("no error for a duplicate ticker")
	}
	if got, _ := db.RefTickers(ctx); len(got) != 3 {
		t.Errorf("%d rows after a refused replace, want 3", len(got))
	}
}

func TestReplaceListingKeepsTheTwoDirectoryFilesApart(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	rows := []RefListing{{Symbol: "SPY", Name: "SPDR", Venue: "NYSE Arca", ETF: true}, {Symbol: "AAPL", Name: "Apple", Venue: "Nasdaq", Market: "Global Select"}}
	d, err := db.ReplaceListing(ctx, RefSource{Name: SourceNasdaq}, rows, at(0))
	if err != nil || d.Added != 2 {
		t.Fatalf("%+v %v", d, err)
	}
	got, _ := db.RefListings(ctx)
	if got[0].Symbol != "AAPL" || !got[1].ETF {
		t.Errorf("listing = %+v", got)
	}
}

func TestPutCompaniesIsIdempotentAndIgnoresFetchTimes(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	c := RefCompany{CIK: "0001045810", Name: "NVIDIA", SIC: "3674", Description: "Semiconductors", State: "DE",
		FiscalYear: "0126", Former: []string{"NVIDIA CORP"}, FetchedAt: at(0)}
	d, err := db.PutCompanies(ctx, []RefCompany{c}, at(0))
	if err != nil || d.Added != 1 {
		t.Fatalf("%+v %v", d, err)
	}
	d, _ = db.PutCompanies(ctx, []RefCompany{c}, at(1))
	if !d.Same {
		t.Errorf("the same record again: %+v", d)
	}
	c.FetchedAt = at(48) // fetched again, same content
	if d, _ = db.PutCompanies(ctx, []RefCompany{c}, at(48)); !d.Same || d.Changed != 0 {
		t.Errorf("a new fetch time alone is a change: %+v", d)
	}
	got, _ := db.RefCompanies(ctx)
	if len(got) != 1 || !got[0].FetchedAt.Equal(at(48)) || got[0].Former[0] != "NVIDIA CORP" {
		t.Errorf("stored = %+v", got)
	}
	c.SIC = "3576"
	if d, _ = db.PutCompanies(ctx, []RefCompany{c}, at(50)); d.Changed != 1 {
		t.Errorf("a new SIC code: %+v", d)
	}
	ch, _ := db.RefChanges(ctx, time.Time{}, 5)
	if len(ch) != 1 || ch[0].Kind != "changed" || ch[0].Key != "0001045810" {
		t.Errorf("changes = %+v", ch)
	}
}

func TestRefConfirmKeepsTheValidatorsOfTheLastGoodAnswer(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	db.ReplaceTickers(ctx, RefSource{ETag: `"good"`, LastModified: "Mon"}, refTickers, at(0))
	if err := db.RefConfirm(ctx, SourceTickers, "", "", at(9), "HTTP 503"); err != nil {
		t.Fatal(err)
	}
	src, _, _ := db.RefSource(ctx, SourceTickers)
	if src.ETag != `"good"` || src.LastModified != "Mon" || src.Error != "HTTP 503" || !src.CheckedAt.Equal(at(0)) {
		t.Errorf("source after a failure = %+v", src)
	}
	db.RefConfirm(ctx, SourceTickers, `"good"`, "Mon", at(10), "")
	src, _, _ = db.RefSource(ctx, SourceTickers)
	if src.Error != "" || !src.CheckedAt.Equal(at(10)) {
		t.Errorf("source after a success = %+v", src)
	}
}
