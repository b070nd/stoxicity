package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/watch"
)

func TestFetchedAfter(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	fetched := func(id, source string, at time.Time) model.Article {
		a := artFrom(id, source, t0.Add(-48*time.Hour)) // published long before it was fetched
		a.Fetched = at
		return a
	}
	mustSave(t, db,
		fetched("a1", "pub", t0.Add(time.Hour)),
		fetched("a2", "priv", t0.Add(2*time.Hour)),
		fetched("a3", "pub", t0.Add(3*time.Hour)),
		fetched("b1", "pub", t0.Add(4*time.Hour)), // b1 to b3 share one fetch time
		fetched("b2", "pub", t0.Add(4*time.Hour)),
		fetched("b3", "pub", t0.Add(4*time.Hour)),
		fetched("a4", "pub", t0.Add(5*time.Hour)),
	)
	pub := set("pub")

	t.Run("oldest fetch first, whatever the publication time", func(t *testing.T) {
		got, err := db.FetchedAfter(ctx, time.Time{}, pub, 100)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"a1", "a3", "b1", "b2", "b3", "a4"}; !reflect.DeepEqual(ids(got), want) {
			t.Errorf("FetchedAfter = %v, want %v", ids(got), want)
		}
		if got[0].Score.LensVersion != "" || !got[0].Fetched.Equal(t0.Add(time.Hour)) {
			t.Errorf("article not filled in: %+v", got[0])
		}
	})
	t.Run("after is exclusive", func(t *testing.T) {
		got, _ := db.FetchedAfter(ctx, t0.Add(3*time.Hour), pub, 100)
		if want := []string{"b1", "b2", "b3", "a4"}; !reflect.DeepEqual(ids(got), want) {
			t.Errorf("FetchedAfter = %v, want %v", ids(got), want)
		}
		got, _ = db.FetchedAfter(ctx, t0.Add(5*time.Hour), pub, 100)
		if len(got) != 0 {
			t.Errorf("nothing was fetched after the newest: %v", ids(got))
		}
	})
	t.Run("only allowed sources, applied before the limit", func(t *testing.T) {
		got, _ := db.FetchedAfter(ctx, time.Time{}, set("priv"), 1)
		if !reflect.DeepEqual(ids(got), []string{"a2"}) {
			t.Errorf("priv = %v", ids(got))
		}
		for _, empty := range []map[string]bool{nil, {}, {"pub": false}} {
			got, err := db.FetchedAfter(ctx, time.Time{}, empty, 10)
			if err != nil || len(got) != 0 {
				t.Errorf("allowed %v = %v, %v; want nothing", empty, ids(got), err)
			}
		}
	})
	t.Run("a page never splits articles with one fetch time", func(t *testing.T) {
		got, _ := db.FetchedAfter(ctx, time.Time{}, pub, 3)
		if want := []string{"a1", "a3", "b1", "b2", "b3"}; !reflect.DeepEqual(ids(got), want) {
			t.Errorf("page 1 = %v, want %v", ids(got), want)
		}
		got, _ = db.FetchedAfter(ctx, got[len(got)-1].Fetched, pub, 3)
		if want := []string{"a4"}; !reflect.DeepEqual(ids(got), want) {
			t.Errorf("page 2 = %v, want %v", ids(got), want)
		}
		got, _ = db.FetchedAfter(ctx, time.Time{}, pub, 1)
		if want := []string{"a1"}; !reflect.DeepEqual(ids(got), want) {
			t.Errorf("limit 1 = %v, want %v", ids(got), want)
		}
	})
}

func TestFetchedAfterUsesTheIndex(t *testing.T) {
	db, _ := openTemp(t)
	rows, err := db.base.r.Query(`EXPLAIN QUERY PLAN SELECT a.id FROM articles a WHERE a.fetched_ms > ? AND a.fetched_ms <= ?
		AND a.source_id IN (SELECT value FROM json_each(?)) ORDER BY a.fetched_ms ASC, a.id ASC`, 0, 1, `["pub"]`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if want := "articles_fetched"; !strings.Contains(plan, want) {
		t.Errorf("query plan does not use %s:\n%s", want, plan)
	}
}

func TestAddHitsCountsNewOnes(t *testing.T) {
	db, _ := openTemp(t)
	ts := db.Tenant("acme")
	ctx := context.Background()
	hits := []watch.Hit{{WatchID: "w", ArticleID: "a", At: t0}, {WatchID: "w", ArticleID: "b", At: t0}}
	if n, err := ts.AddHits(ctx, hits); err != nil || n != 2 {
		t.Fatalf("AddHits = %d, %v; want 2", n, err)
	}
	more := append(hits, watch.Hit{WatchID: "w", ArticleID: "c", At: t0.Add(time.Hour)})
	if n, err := ts.AddHits(ctx, more); err != nil || n != 1 {
		t.Errorf("AddHits again = %d, %v; want 1 (only c is new)", n, err)
	}
	if n, err := ts.AddHits(ctx, nil); err != nil || n != 0 {
		t.Errorf("AddHits(nil) = %d, %v", n, err)
	}
}

func TestUnreadCountsHitsDetectedAfterTheMarker(t *testing.T) {
	db, _ := openTemp(t)
	ts := db.Tenant("acme")
	ctx := context.Background()
	if err := ts.MarkSeen(ctx, "w", t0); err != nil {
		t.Fatal(err)
	}
	// The hit is stamped with its detection time, which is after the marker.
	if err := ts.PutHits(ctx, []watch.Hit{{WatchID: "w", ArticleID: "late", At: t0.Add(30 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	if n, err := ts.Unread(ctx, "w"); err != nil || n != 1 {
		t.Errorf("Unread = %d, %v; want 1", n, err)
	}
}

func TestCursor(t *testing.T) {
	db, _ := openTemp(t)
	ts := db.Tenant("acme")
	ctx := context.Background()
	if _, ok, err := ts.Cursor(ctx, "w"); err != nil || ok {
		t.Fatalf("new watch: cursor present %v, %v", ok, err)
	}
	want := Cursor{Fetched: t0, Hash: "h1"}
	if err := ts.SetCursor(ctx, "w", want); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := ts.Cursor(ctx, "w"); err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("Cursor = %+v, %v, %v; want %+v", got, ok, err, want)
	}
	want = Cursor{Fetched: t0.Add(time.Hour), Hash: "h2"}
	if err := ts.SetCursor(ctx, "w", want); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := ts.Cursor(ctx, "w"); !reflect.DeepEqual(got, want) {
		t.Errorf("replaced Cursor = %+v, want %+v", got, want)
	}
	if _, ok, _ := ts.Cursor(ctx, "other"); ok {
		t.Error("a cursor leaked to another watch")
	}
	if _, ok, _ := db.Tenant("beta").Cursor(ctx, "w"); ok {
		t.Error("a cursor leaked to another tenant")
	}
	if err := ts.SetCursor(ctx, "", want); err == nil {
		t.Error("empty watch ID accepted")
	}
}

func TestPendingAlertsAndDelivery(t *testing.T) {
	db, _ := openTemp(t)
	ts := db.Tenant("acme")
	ctx := context.Background()
	err := ts.PutHits(ctx, []watch.Hit{
		{WatchID: "w", ArticleID: "a1", At: t0, Alerts: []string{"big", "any"}},
		{WatchID: "w", ArticleID: "a2", At: t0.Add(time.Hour), Alerts: []string{"any"}},
		{WatchID: "w", ArticleID: "a3", At: t0.Add(time.Hour)}, // no alerts: never pending
		{WatchID: "v", ArticleID: "a1", At: t0.Add(2 * time.Hour), Alerts: []string{"any"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []PendingAlert{
		{"w", "a1", "any", t0}, {"w", "a1", "big", t0},
		{"w", "a2", "any", t0.Add(time.Hour)}, {"v", "a1", "any", t0.Add(2 * time.Hour)},
	}
	got, err := ts.PendingAlerts(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingAlerts = %+v, %v\nwant %+v", got, err, want)
	}

	if _, ok, err := ts.LastDelivery(ctx, "w", "any"); err != nil || ok {
		t.Fatalf("LastDelivery before any delivery: %v, %v", ok, err)
	}
	at := t0.Add(3 * time.Hour)
	if err := ts.MarkDelivered(ctx, want[:3], at); err != nil {
		t.Fatal(err)
	}
	if err := ts.MarkDelivered(ctx, want[:1], at.Add(time.Hour)); err != nil { // a second mark keeps the first
		t.Fatal(err)
	}
	got, _ = ts.PendingAlerts(ctx)
	if !reflect.DeepEqual(got, want[3:]) {
		t.Errorf("PendingAlerts after delivery = %+v, want %+v", got, want[3:])
	}
	if last, ok, err := ts.LastDelivery(ctx, "w", "any"); err != nil || !ok || !last.Equal(at) {
		t.Errorf("LastDelivery = %v, %v, %v; want %v", last, ok, err, at)
	}
	if _, ok, _ := ts.LastDelivery(ctx, "v", "any"); ok {
		t.Error("another watch's alert counts as delivered")
	}
	if got, _ := db.Tenant("beta").PendingAlerts(ctx); len(got) != 0 {
		t.Errorf("another tenant sees %+v", got)
	}
	if err := ts.MarkDelivered(ctx, []PendingAlert{{WatchID: "w", ArticleID: "x"}}, at); err == nil {
		t.Error("an alert without a name was accepted")
	}
}

func TestDigestSent(t *testing.T) {
	db, _ := openTemp(t)
	ts := db.Tenant("acme")
	ctx := context.Background()
	if sent, err := ts.DigestSent(ctx, "w", "2026-11-03"); err != nil || sent {
		t.Fatalf("before: %v, %v", sent, err)
	}
	if err := ts.MarkDigestSent(ctx, "w", "2026-11-03", t0); err != nil {
		t.Fatal(err)
	}
	if err := ts.MarkDigestSent(ctx, "w", "2026-11-03", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{"2026-11-03": true, "2026-11-04": false} {
		if got, _ := ts.DigestSent(ctx, "w", key); got != want {
			t.Errorf("DigestSent(%s) = %v, want %v", key, got, want)
		}
	}
	if got, _ := ts.DigestSent(ctx, "other", "2026-11-03"); got {
		t.Error("another watch's digest counts as sent")
	}
	if got, _ := db.Tenant("beta").DigestSent(ctx, "w", "2026-11-03"); got {
		t.Error("another tenant's digest counts as sent")
	}
	if err := ts.MarkDigestSent(ctx, "w", "", t0); err == nil {
		t.Error("empty period accepted")
	}
}

func TestLedgerStateRejectsInvalidTenant(t *testing.T) {
	db, dir := openTemp(t)
	ctx := context.Background()
	before := listTree(t, dir)
	ts := db.Tenant("../x")
	_, _, e1 := ts.Cursor(ctx, "w")
	_, e2 := ts.PendingAlerts(ctx)
	_, _, e3 := ts.LastDelivery(ctx, "w", "a")
	_, e4 := ts.DigestSent(ctx, "w", "k")
	_, e5 := ts.AddHits(ctx, []watch.Hit{{WatchID: "w", ArticleID: "a"}})
	for name, err := range map[string]error{
		"Cursor": e1, "PendingAlerts": e2, "LastDelivery": e3, "DigestSent": e4, "AddHits": e5,
		"SetCursor":      ts.SetCursor(ctx, "w", Cursor{}),
		"MarkDelivered":  ts.MarkDelivered(ctx, []PendingAlert{{WatchID: "w", ArticleID: "a", Alert: "x"}}, t0),
		"MarkDigestSent": ts.MarkDigestSent(ctx, "w", "k", t0),
	} {
		if err == nil {
			t.Errorf("%s accepted an invalid tenant", name)
		}
	}
	if after := listTree(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("files changed:\nbefore %v\nafter  %v", before, after)
	}
}
