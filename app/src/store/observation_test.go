package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type obsRow struct{ Source, Title string }

func observations(t *testing.T, db *DB, id string) []obsRow {
	t.Helper()
	rows, err := db.base.r.Query(`SELECT source_id, title FROM observation WHERE article_id = ? ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []obsRow
	for rows.Next() {
		var o obsRow
		if err := rows.Scan(&o.Source, &o.Title); err != nil {
			t.Fatal(err)
		}
		out = append(out, o)
	}
	return out
}

func TestEveryDistinctStatementIsKeptAsAnObservation(t *testing.T) {
	db, _ := openTemp(t)
	a := art("a1", t0)
	mustSave(t, db, a)
	mustSave(t, db, a) // the same again: not a new observation

	other := a
	other.SourceID, other.Title = "other", "Fabricated"
	mustSave(t, db, other) // another source's copy: kept, though the article row refuses it

	fixed := a
	fixed.Title = "Correction"
	mustSave(t, db, fixed) // the first source corrects itself

	want := []obsRow{{"src", "Title a1"}, {"other", "Fabricated"}, {"src", "Correction"}}
	if got := observations(t, db, "a1"); !reflect.DeepEqual(got, want) {
		t.Errorf("observations = %v, want %v", got, want)
	}
	if got, err := db.Get(context.Background(), "a1"); err != nil || got.Title != "Correction" || got.SourceID != "src" {
		t.Errorf("article = %+v, %v; want the first source's corrected text", got, err)
	}
}

func TestChangedAfterFollowsTheOrderOfCommits(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	all := map[string]bool{"src": true}

	// B commits first with the later fetch time, A commits second with an older
	// stamp (a slow worker): a consumer that already read B must still get A.
	b := art("b", t0.Add(time.Hour))
	mustSave(t, db, b)
	got, pos, err := db.ChangedAfter(ctx, 0, all, 10)
	if err != nil || ids(got)[0] != "b" || len(got) != 1 {
		t.Fatalf("first read = %v, %v", ids(got), err)
	}
	a := art("a", t0)
	mustSave(t, db, a)
	got, next, err := db.ChangedAfter(ctx, pos, all, 10)
	if err != nil || len(got) != 1 || got[0].ID != "a" || next <= pos {
		t.Fatalf("late commit: %v, position %d -> %d, %v", ids(got), pos, next, err)
	}

	// An unchanged save gives no new number; a change does.
	mustSave(t, db, a)
	if got, _, _ := db.ChangedAfter(ctx, next, all, 10); len(got) != 0 {
		t.Errorf("an unchanged save was read again: %v", ids(got))
	}
	a.Title = "corrected"
	mustSave(t, db, a)
	if got, _, _ := db.ChangedAfter(ctx, next, all, 10); len(got) != 1 || got[0].Title != "corrected" {
		t.Errorf("a correction was not read: %v", ids(got))
	}

	// Paging and the allowed set.
	if got, _, _ := db.ChangedAfter(ctx, 0, all, 1); len(got) != 1 {
		t.Errorf("limit 1 gave %d", len(got))
	}
	if got, p, _ := db.ChangedAfter(ctx, 0, map[string]bool{}, 10); len(got) != 0 || p != 0 {
		t.Errorf("an empty allowed set read %v", ids(got))
	}
}
