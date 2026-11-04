package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// artFrom is art with a source.
func artFrom(id, source string, published time.Time) model.Article {
	a := art(id, published)
	a.SourceID = source
	return a
}

func set(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// twoSources saves a1 and a3 from source "pub" and a2 and a4 from "priv", one
// hour apart, all containing the word zebra.
func twoSources(t *testing.T) *DB {
	t.Helper()
	db, _ := openTemp(t)
	var as []model.Article
	for i, src := range []string{"pub", "priv", "pub", "priv"} {
		a := artFrom(fmt.Sprintf("a%d", i+1), src, t0.Add(time.Duration(i)*time.Hour))
		a.Title = "zebra " + a.ID
		as = append(as, a)
	}
	mustSave(t, db, as...)
	return db
}

func TestFilteredReads(t *testing.T) {
	db := twoSources(t)
	ctx := context.Background()
	pub := set("pub")
	all := t0.Add(-time.Hour)
	end := t0.Add(24 * time.Hour)

	got, err := db.RangeIn(ctx, all, end, pub)
	if err != nil || !reflect.DeepEqual(ids(got), []string{"a3", "a1"}) {
		t.Errorf("RangeIn = %v, %v", ids(got), err)
	}
	got, _ = db.RangeIn(ctx, time.Time{}, time.Time{}, pub)
	if !reflect.DeepEqual(ids(got), []string{"a3", "a1"}) {
		t.Errorf("RangeIn open bounds = %v", ids(got))
	}
	got, _ = db.RangeIn(ctx, all, t0.Add(2*time.Hour), pub)
	if !reflect.DeepEqual(ids(got), []string{"a1"}) {
		t.Errorf("RangeIn is not half-open: %v", ids(got))
	}
	got, _ = db.DayIn(ctx, t0, set("pub", "priv"))
	if !reflect.DeepEqual(ids(got), []string{"a4", "a3", "a2", "a1"}) {
		t.Errorf("DayIn both = %v", ids(got))
	}
	got, _ = db.DayIn(ctx, t0, set("priv"))
	if !reflect.DeepEqual(ids(got), []string{"a4", "a2"}) {
		t.Errorf("DayIn priv = %v", ids(got))
	}
	if got, _ := db.DayIn(ctx, t0.AddDate(0, 0, 1), pub); len(got) != 0 {
		t.Errorf("DayIn next day = %v", ids(got))
	}

	got, err = db.SearchIn(ctx, "zebra", time.Time{}, time.Time{}, pub, 0, 0)
	if err != nil || !reflect.DeepEqual(ids(got), []string{"a3", "a1"}) {
		t.Errorf("SearchIn = %v, %v", ids(got), err)
	}
	got, _ = db.SearchIn(ctx, "zebra", t0.Add(time.Hour), time.Time{}, set("priv"), 0, 0)
	if !reflect.DeepEqual(ids(got), []string{"a4", "a2"}) {
		t.Errorf("SearchIn from = %v", ids(got))
	}

	if a, err := db.GetIn(ctx, "a1", pub); err != nil || a.ID != "a1" {
		t.Errorf("GetIn visible = %v, %v", a.ID, err)
	}
	if _, err := db.GetIn(ctx, "a2", pub); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetIn of another source: %v, want ErrNotFound", err)
	}
	_, other := db.GetIn(ctx, "a2", pub)
	_, missing := db.GetIn(ctx, "nope", pub)
	if !errors.Is(missing, ErrNotFound) || other.Error() != fmt.Sprintf("%v", fmt.Errorf("%w: article %q", ErrNotFound, "a2")) {
		t.Errorf("GetIn errors differ: %v / %v", other, missing)
	}

	has, err := db.HasIn(ctx, []string{"a1", "a2", "a3", "zz"}, pub)
	if err != nil || !reflect.DeepEqual(has, map[string]bool{"a1": true, "a3": true}) {
		t.Errorf("HasIn = %v, %v", has, err)
	}
	if has, err := db.HasIn(ctx, nil, pub); err != nil || len(has) != 0 {
		t.Errorf("HasIn(nil) = %v, %v", has, err)
	}
}

func TestFilteredReadsEmptySetAllowsNothing(t *testing.T) {
	db := twoSources(t)
	ctx := context.Background()
	for name, allowed := range map[string]map[string]bool{
		"nil":       nil,
		"empty":     {},
		"all false": {"pub": false, "priv": false},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := db.DayIn(ctx, t0, allowed); err != nil || len(got) != 0 {
				t.Errorf("DayIn = %v, %v", ids(got), err)
			}
			if got, err := db.RangeIn(ctx, time.Time{}, time.Time{}, allowed); err != nil || len(got) != 0 {
				t.Errorf("RangeIn = %v, %v", ids(got), err)
			}
			if got, err := db.RangePageIn(ctx, time.Time{}, time.Time{}, allowed, 10, 0); err != nil || len(got) != 0 {
				t.Errorf("RangePageIn = %v, %v", ids(got), err)
			}
			if got, err := db.SearchIn(ctx, "zebra", time.Time{}, time.Time{}, allowed, 10, 0); err != nil || len(got) != 0 {
				t.Errorf("SearchIn = %v, %v", ids(got), err)
			}
			if _, err := db.GetIn(ctx, "a1", allowed); !errors.Is(err, ErrNotFound) {
				t.Errorf("GetIn: %v, want ErrNotFound", err)
			}
			if got, err := db.HasIn(ctx, []string{"a1"}, allowed); err != nil || len(got) != 0 {
				t.Errorf("HasIn = %v, %v", got, err)
			}
			if _, err := db.SearchIn(ctx, " ", time.Time{}, time.Time{}, allowed, 10, 0); err == nil {
				t.Error("SearchIn with an empty match returned no error")
			}
		})
	}
}

// TestSearchInLimitAfterVisibility is the starvation scenario: invisible
// articles that are newer and far more numerous than the cap must not push the
// visible ones out of the result.
func TestSearchInLimitAfterVisibility(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	var as []model.Article
	for i := 0; i < 1500; i++ {
		a := artFrom(fmt.Sprintf("x%04d", i), "priv", t0.Add(time.Hour+time.Duration(i)*time.Second))
		a.Title = "zebra crossing"
		as = append(as, a)
	}
	for i := 0; i < 3; i++ {
		a := artFrom(fmt.Sprintf("v%d", i), "pub", t0.Add(-time.Duration(i)*time.Hour))
		a.Title = "zebra crossing"
		as = append(as, a)
	}
	mustSave(t, db, as...)

	got, err := db.SearchIn(ctx, "zebra", time.Time{}, time.Time{}, set("pub"), 10, 0)
	if err != nil || !reflect.DeepEqual(ids(got), []string{"v0", "v1", "v2"}) {
		t.Errorf("SearchIn = %v, %v", ids(got), err)
	}
	got, _ = db.SearchIn(ctx, "zebra", time.Time{}, time.Time{}, set("pub"), 2, 0)
	if !reflect.DeepEqual(ids(got), []string{"v0", "v1"}) {
		t.Errorf("limit 2 = %v", ids(got))
	}
	got, _ = db.SearchIn(ctx, "zebra", time.Time{}, time.Time{}, set("pub"), 2, 2)
	if !reflect.DeepEqual(ids(got), []string{"v2"}) {
		t.Errorf("offset 2 = %v", ids(got))
	}
}

func TestPaging(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	var as []model.Article
	for i := 0; i < 25; i++ {
		a := artFrom(fmt.Sprintf("p%02d", i), "pub", t0.Add(time.Duration(i)*time.Minute))
		a.Title = "zebra"
		as = append(as, a)
		as = append(as, artFrom(fmt.Sprintf("q%02d", i), "priv", t0.Add(time.Duration(i)*time.Minute+time.Second)))
	}
	mustSave(t, db, as...)
	pub := set("pub")

	full, err := db.RangeIn(ctx, time.Time{}, time.Time{}, pub)
	if err != nil || len(full) != 25 {
		t.Fatalf("RangeIn = %d articles, %v", len(full), err)
	}
	for name, fetch := range map[string]func(limit, offset int) ([]model.Article, error){
		"range": func(limit, offset int) ([]model.Article, error) {
			return db.RangePageIn(ctx, time.Time{}, time.Time{}, pub, limit, offset)
		},
		"search": func(limit, offset int) ([]model.Article, error) {
			return db.SearchIn(ctx, "zebra", time.Time{}, time.Time{}, pub, limit, offset)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var got []model.Article
			for offset := 0; ; offset += 10 {
				p, err := fetch(10, offset)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, p...)
				if len(p) < 10 {
					break
				}
			}
			if !reflect.DeepEqual(ids(got), ids(full)) {
				t.Errorf("pages = %v, want %v", ids(got), ids(full))
			}
			if p, _ := fetch(10, 25); len(p) != 0 {
				t.Errorf("page past the end = %v", ids(p))
			}
			if p, _ := fetch(0, 0); len(p) != 25 {
				t.Errorf("limit 0 returned %d, want the default 100 to cover 25", len(p))
			}
		})
	}
}

// TestFilteredReadsLargeSets checks that the allowed set has no size limit:
// 2,000 and 40,000 IDs (above SQLite's 32,766 variable limit) both work.
func TestFilteredReadsLargeSets(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	mustSave(t, db, artFrom("in", "src-1999", t0), artFrom("out", "elsewhere", t0.Add(time.Hour)))
	for _, n := range []int{2000, 40000} {
		allowed := map[string]bool{}
		for i := 0; i < n; i++ {
			allowed[fmt.Sprintf("src-%d", i)] = true
		}
		got, err := db.RangeIn(ctx, time.Time{}, time.Time{}, allowed)
		if err != nil || !reflect.DeepEqual(ids(got), []string{"in"}) {
			t.Errorf("%d sources: RangeIn = %v, %v", n, ids(got), err)
		}
		got, err = db.SearchIn(ctx, "Title", time.Time{}, time.Time{}, allowed, 10, 0)
		if err != nil || !reflect.DeepEqual(ids(got), []string{"in"}) {
			t.Errorf("%d sources: SearchIn = %v, %v", n, ids(got), err)
		}
		if _, err := db.GetIn(ctx, "in", allowed); err != nil {
			t.Errorf("%d sources: GetIn: %v", n, err)
		}
		has, err := db.HasIn(ctx, []string{"in", "out"}, allowed)
		if err != nil || !reflect.DeepEqual(has, map[string]bool{"in": true}) {
			t.Errorf("%d sources: HasIn = %v, %v", n, has, err)
		}
	}
}

func TestFilteredReadsClosedStore(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	db.Close()
	pub := set("pub")
	if _, err := db.RangeIn(ctx, time.Time{}, time.Time{}, pub); err == nil {
		t.Error("RangeIn after Close returned no error")
	}
	if _, err := db.SearchIn(ctx, "x", time.Time{}, time.Time{}, pub, 1, 0); err == nil {
		t.Error("SearchIn after Close returned no error")
	}
	if _, err := db.GetIn(ctx, "x", pub); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("GetIn after Close: %v", err)
	}
	if _, err := db.HasIn(ctx, []string{"x"}, pub); err == nil {
		t.Error("HasIn after Close returned no error")
	}
}
