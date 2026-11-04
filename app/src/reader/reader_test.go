package reader

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

var t0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func article(id, source, title string, published time.Time) model.Article {
	return model.Article{
		ID: id, SourceID: source, Domain: "example.com", URL: "https://example.com/" + id,
		Title: title, Published: published, Fetched: published,
	}
}

func save(t *testing.T, db *store.DB, as ...model.Article) {
	t.Helper()
	if err := db.Save(context.Background(), as); err != nil {
		t.Fatal(err)
	}
}

func ids(as []model.Article) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

func parse(t *testing.T, expr string) filter.Query {
	t.Helper()
	q, err := filter.Parse(expr)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	return loc
}

func set(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// TestSearchNotStarvedByInvisibleSources: 1,500 newer articles from an
// invisible source match the query, three older ones from a visible source
// match too. All three must be found.
func TestSearchNotStarvedByInvisibleSources(t *testing.T) {
	db := openDB(t)
	var as []model.Article
	for i := 0; i < 1500; i++ {
		as = append(as, article(fmt.Sprintf("x%04d", i), "secret", "zebra herd", t0.Add(time.Hour+time.Duration(i)*time.Second)))
	}
	for i := 0; i < 3; i++ {
		as = append(as, article(fmt.Sprintf("v%d", i), "open", "zebra herd", t0.Add(-time.Duration(i)*time.Hour)))
	}
	save(t, db, as...)

	r := New(db, "alice", set("open"), time.UTC)
	for _, expr := range []string{"zebra", "zebra -nothing", "site:example.com zebra"} {
		res, err := r.Search(context.Background(), parse(t, expr), time.Time{}, time.Time{}, 50)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"v0", "v1", "v2"}; !reflect.DeepEqual(ids(res.Articles), want) || res.Truncated {
			t.Errorf("%q: got %v truncated=%v, want %v", expr, ids(res.Articles), res.Truncated, want)
		}
	}
}

func TestEmptyVisibleSetReadsNothing(t *testing.T) {
	db := openDB(t)
	save(t, db, article("a1", "open", "zebra", t0), article("a2", "secret", "zebra", t0))
	ctx := context.Background()
	for name, visible := range map[string]map[string]bool{
		"nil": nil, "empty": {}, "all false": {"open": false},
	} {
		t.Run(name, func(t *testing.T) {
			r := New(db, "alice", visible, time.UTC, WithNow(func() time.Time { return t0 }))
			if got, err := r.Day(ctx, t0); err != nil || len(got) != 0 {
				t.Errorf("Day = %v, %v", ids(got), err)
			}
			if got, err := r.Range(ctx, time.Time{}, time.Time{}); err != nil || len(got) != 0 {
				t.Errorf("Range = %v, %v", ids(got), err)
			}
			if _, err := r.Get(ctx, "a1"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("Get: %v, want ErrNotFound", err)
			}
			for _, expr := range []string{"zebra", "-nothing"} {
				res, err := r.Search(ctx, parse(t, expr), time.Time{}, time.Time{}, 10)
				if err != nil || len(res.Articles) != 0 || res.Truncated {
					t.Errorf("Search(%q) = %v truncated=%v, %v", expr, ids(res.Articles), res.Truncated, err)
				}
			}
			if len(r.Visible()) != 0 {
				t.Errorf("Visible = %v", r.Visible())
			}
		})
	}
}

func TestGetInvisibleIsNotFound(t *testing.T) {
	db := openDB(t)
	save(t, db, article("mine", "open", "a", t0), article("theirs", "secret", "b", t0))
	r := New(db, "alice", set("open"), time.UTC)
	ctx := context.Background()

	if a, err := r.Get(ctx, "mine"); err != nil || a.ID != "mine" {
		t.Errorf("Get(mine) = %v, %v", a.ID, err)
	}
	_, hidden := r.Get(ctx, "theirs")
	_, missing := r.Get(ctx, "nope")
	if !errors.Is(hidden, store.ErrNotFound) || !errors.Is(missing, store.ErrNotFound) {
		t.Fatalf("hidden = %v, missing = %v, both want ErrNotFound", hidden, missing)
	}
	if want := fmt.Sprintf("%v", fmt.Errorf("%w: article %q", store.ErrNotFound, "theirs")); hidden.Error() != want {
		t.Errorf("hidden error %q differs from a missing one, %q", hidden, want)
	}
}

func TestDayBoundariesBerlin(t *testing.T) {
	loc := berlin(t)
	db := openDB(t)
	// 2026-11-03 in Berlin (UTC+1) is [11-02 23:00Z, 11-03 23:00Z).
	save(t, db,
		article("before", "open", "x", time.Date(2026, 11, 2, 22, 59, 59, 999e6, time.UTC)),
		article("first", "open", "x", time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)),
		article("last", "open", "x", time.Date(2026, 11, 3, 22, 59, 59, 999e6, time.UTC)),
		article("after", "open", "x", time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC)),
		article("hidden", "secret", "x", time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)),
		// 2026-03-29 has 23 hours in Berlin: [03-28 23:00Z, 03-29 22:00Z).
		article("dst-in", "open", "x", time.Date(2026, 3, 29, 21, 59, 0, 0, time.UTC)),
		article("dst-out", "open", "x", time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC)),
	)
	r := New(db, "alice", set("open"), loc)
	ctx := context.Background()

	// Any instant of the day, given in any zone, selects the Berlin day.
	for _, day := range []time.Time{
		time.Date(2026, 11, 3, 0, 0, 0, 0, loc),
		time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 3, 22, 30, 0, 0, time.UTC), // 23:30 in Berlin
	} {
		got, err := r.Day(ctx, day)
		if err != nil || !reflect.DeepEqual(ids(got), []string{"last", "first"}) {
			t.Errorf("Day(%v) = %v, %v", day, ids(got), err)
		}
	}
	// 23:30 UTC on 11-03 is already 11-04 in Berlin.
	if got, _ := r.Day(ctx, time.Date(2026, 11, 3, 23, 30, 0, 0, time.UTC)); !reflect.DeepEqual(ids(got), []string{"after"}) {
		t.Errorf("next Berlin day = %v", ids(got))
	}
	if got, _ := r.Day(ctx, time.Date(2026, 3, 29, 12, 0, 0, 0, loc)); !reflect.DeepEqual(ids(got), []string{"dst-in"}) {
		t.Errorf("DST day = %v", ids(got))
	}

	got, err := r.Range(ctx, time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC))
	if err != nil || !reflect.DeepEqual(ids(got), []string{"last", "first"}) {
		t.Errorf("Range = %v, %v", ids(got), err)
	}
}

// TestSearchPagingAndTruncation uses small batches and a small scan cap so the
// paths of the real limits (500 and 20,000) run on little data.
func TestSearchPagingAndTruncation(t *testing.T) {
	db := openDB(t)
	// 30 visible candidates, newest first c29 ... c00. The full-text part of
	// the query, "zebra", accepts all of them; those whose number is divisible
	// by 10 contain "drop" and fail the negation.
	var as []model.Article
	for i := 0; i < 30; i++ {
		title := "zebra keep"
		if i%10 == 0 {
			title = "zebra drop"
		}
		as = append(as, article(fmt.Sprintf("c%02d", i), "open", title, t0.Add(time.Duration(i)*time.Minute)))
	}
	save(t, db, as...)
	ctx := context.Background()
	q := parse(t, "zebra -drop")

	newReader := func(batch, scanCap int) *Reader {
		r := New(db, "alice", set("open"), time.UTC)
		r.batch, r.scanCap = batch, scanCap
		return r
	}
	tests := []struct {
		name           string
		batch, scanCap int
		limit          int
		want           []string
		truncated      bool
	}{
		{"limit inside first batch", 7, 20, 3, []string{"c29", "c28", "c27"}, false},
		{"limit across batches", 7, 100, 12, []string{"c29", "c28", "c27", "c26", "c25", "c24", "c23", "c22", "c21", "c19", "c18", "c17"}, false},
		{"candidates run out", 7, 100, 100, nil, false}, // filled in below
		{"cap reached, more candidates", 7, 20, 100, []string{"c29", "c28", "c27", "c26", "c25", "c24", "c23", "c22", "c21", "c19", "c18", "c17", "c16", "c15", "c14", "c13", "c12", "c11"}, true},
		{"cap equals candidates", 7, 30, 100, nil, false}, // filled in below
		{"cap not a multiple of the batch", 7, 15, 100, []string{"c29", "c28", "c27", "c26", "c25", "c24", "c23", "c22", "c21", "c19", "c18", "c17", "c16", "c15"}, true},
		{"limit reached exactly at the cap", 7, 9, 8, []string{"c29", "c28", "c27", "c26", "c25", "c24", "c23", "c22"}, false},
	}
	var all []string
	for i := 29; i >= 0; i-- {
		if i%10 != 0 {
			all = append(all, fmt.Sprintf("c%02d", i))
		}
	}
	for i := range tests {
		if tests[i].want == nil {
			tests[i].want = all
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := newReader(tc.batch, tc.scanCap).Search(ctx, q, time.Time{}, time.Time{}, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ids(res.Articles), tc.want) || res.Truncated != tc.truncated {
				t.Errorf("got %v truncated=%v, want %v truncated=%v", ids(res.Articles), res.Truncated, tc.want, tc.truncated)
			}
		})
	}

	t.Run("from and to", func(t *testing.T) {
		res, err := newReader(7, 100).Search(ctx, q, t0.Add(5*time.Minute), t0.Add(8*time.Minute), 100)
		if err != nil || !reflect.DeepEqual(ids(res.Articles), []string{"c07", "c06", "c05"}) {
			t.Errorf("got %v, %v", ids(res.Articles), err)
		}
	})
	t.Run("bad query reaches the caller", func(t *testing.T) {
		if res, err := newReader(7, 100).Search(ctx, parse(t, "zebra"), time.Time{}, time.Time{}, 0); err != nil || len(res.Articles) != 30 {
			t.Errorf("limit 0: %d articles, %v", len(res.Articles), err)
		}
	})
}

// TestSearchScanCapFullSize runs the real limits: 20,001 visible candidates
// that all fail the query's negation.
func TestSearchScanCapFullSize(t *testing.T) {
	if testing.Short() {
		t.Skip("saves 20,001 articles")
	}
	db := openDB(t)
	var as []model.Article
	for i := 0; i < ScanCap+1; i++ {
		as = append(as, article(fmt.Sprintf("n%05d", i), "open", "zebra drop", t0.Add(-time.Duration(i)*time.Second)))
	}
	save(t, db, as...)
	r := New(db, "alice", set("open"), time.UTC)
	res, err := r.Search(context.Background(), parse(t, "zebra -drop"), time.Time{}, time.Time{}, 10)
	if err != nil || len(res.Articles) != 0 || !res.Truncated {
		t.Errorf("got %d articles truncated=%v, %v; want none, truncated", len(res.Articles), res.Truncated, err)
	}
}

func TestSearchWithoutUsableFullText(t *testing.T) {
	db := openDB(t)
	day := 24 * time.Hour
	save(t, db,
		article("old", "open", "alpha", t0.Add(-40*day)),
		article("recent", "open", "alpha", t0.Add(-10*day)),
		article("now", "open", "alpha beta", t0),
		article("future", "open", "alpha", t0.Add(2*day)),
		article("hidden", "secret", "alpha", t0.Add(-time.Hour)),
	)
	ctx := context.Background()
	now := func() time.Time { return t0.Add(time.Hour) }

	tests := []struct {
		name string
		opts []Option
		expr string
		from time.Time
		to   time.Time
		want []string
	}{
		{"default window is 30 days up to a day ahead", []Option{WithNow(now)}, "-beta", time.Time{}, time.Time{}, []string{"recent"}},
		{"operator only", []Option{WithNow(now)}, "site:example.com", time.Time{}, time.Time{}, []string{"now", "recent"}},
		{"negation and operator", []Option{WithNow(now)}, "site:example.com -beta", time.Time{}, time.Time{}, []string{"recent"}},
		{"since option", []Option{WithNow(now), WithSince(t0.Add(-50 * day))}, "site:example.com", time.Time{}, time.Time{}, []string{"now", "recent", "old"}},
		{"explicit from and to override", []Option{WithNow(now)}, "site:example.com", t0.Add(-50 * day), t0.Add(3 * day), []string{"future", "now", "recent", "old"}},
		{"nil option ignored", []Option{WithNow(nil), WithNow(now)}, "site:example.com", time.Time{}, time.Time{}, []string{"now", "recent"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New(db, "alice", set("open"), time.UTC, tc.opts...)
			res, err := r.Search(ctx, parse(t, tc.expr), tc.from, tc.to, 50)
			if err != nil || !reflect.DeepEqual(ids(res.Articles), tc.want) || res.Truncated {
				t.Errorf("got %v truncated=%v, %v; want %v", ids(res.Articles), res.Truncated, err, tc.want)
			}
		})
	}

	t.Run("paging over a range", func(t *testing.T) {
		var as []model.Article
		for i := 0; i < 23; i++ {
			as = append(as, article(fmt.Sprintf("r%02d", i), "open", "gamma", t0.Add(-time.Duration(i)*time.Minute)))
		}
		save(t, db, as...)
		r := New(db, "alice", set("open"), time.UTC, WithNow(now))
		r.batch, r.scanCap = 5, 100
		res, err := r.Search(ctx, parse(t, "-beta -alpha"), time.Time{}, time.Time{}, 50)
		if err != nil || len(res.Articles) != 23 || res.Truncated {
			t.Fatalf("got %d articles truncated=%v, %v", len(res.Articles), res.Truncated, err)
		}
		// The ten newest candidates are "now" (which has beta) and r00 to r08.
		r.scanCap = 10
		res, _ = r.Search(ctx, parse(t, "-beta -alpha"), time.Time{}, time.Time{}, 50)
		if len(res.Articles) != 9 || !res.Truncated {
			t.Errorf("capped: %d articles truncated=%v", len(res.Articles), res.Truncated)
		}
	})
}

func TestMatchInUsesZone(t *testing.T) {
	loc := berlin(t)
	db := openDB(t)
	// 23:30Z on 11-02 is 00:30 on 11-03 in Berlin.
	save(t, db, article("late", "open", "zebra", time.Date(2026, 11, 2, 23, 30, 0, 0, time.UTC)))
	q := parse(t, "zebra after:2026-11-03")
	for _, tc := range []struct {
		zone *time.Location
		want int
	}{{loc, 1}, {time.UTC, 0}, {nil, 0}} {
		res, err := New(db, "alice", set("open"), tc.zone).Search(context.Background(), q, time.Time{}, time.Time{}, 10)
		if err != nil || len(res.Articles) != tc.want {
			t.Errorf("zone %v: %d articles, %v; want %d", tc.zone, len(res.Articles), err, tc.want)
		}
	}
}

func TestAccessorsCopy(t *testing.T) {
	in := map[string]bool{"open": true, "off": false}
	r := New(nil, "alice", in, nil)
	in["sneaky"] = true
	if got := r.Visible(); !reflect.DeepEqual(got, map[string]bool{"open": true}) {
		t.Errorf("Visible = %v", got)
	}
	r.Visible()["sneaky"] = true
	if got := r.Visible(); !reflect.DeepEqual(got, map[string]bool{"open": true}) {
		t.Errorf("Visible changed through its copy: %v", got)
	}
	if r.ForTenant() != "alice" {
		t.Errorf("ForTenant = %q", r.ForTenant())
	}
}

func TestStoreErrorsReachTheCaller(t *testing.T) {
	db := openDB(t)
	r := New(db, "alice", set("open"), time.UTC)
	db.Close()
	ctx := context.Background()
	if _, err := r.Day(ctx, t0); err == nil {
		t.Error("Day on a closed store returned no error")
	}
	if _, err := r.Range(ctx, t0, t0); err == nil {
		t.Error("Range on a closed store returned no error")
	}
	if _, err := r.Get(ctx, "x"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get on a closed store: %v", err)
	}
	for _, expr := range []string{"zebra", "-zebra"} {
		if _, err := r.Search(ctx, parse(t, expr), time.Time{}, time.Time{}, 10); err == nil {
			t.Errorf("Search(%q) on a closed store returned no error", expr)
		}
	}
}
