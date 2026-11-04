package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/scheduler"
	"github.com/b070nd/stoxicity/app/src/watch"
)

var t0 = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dir
}

func art(id string, published time.Time) model.Article {
	return model.Article{
		ID: id, SourceID: "src", Domain: "example.com", URL: "https://example.com/" + id,
		Title: "Title " + id, Summary: "Summary " + id, Published: published, Fetched: published.Add(time.Minute),
	}
}

func ids(as []model.Article) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

func mustSave(t *testing.T, db *DB, as ...model.Article) {
	t.Helper()
	if err := db.Save(context.Background(), as); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteFeaturesAndPragmas(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	var fts int
	if err := db.base.r.QueryRowContext(ctx, `SELECT sqlite_compileoption_used('ENABLE_FTS5')`).Scan(&fts); err != nil || fts != 1 {
		t.Fatalf("FTS5 not compiled in: %d %v", fts, err)
	}
	for name, conn := range map[string]*sql.DB{"write": db.base.w, "read": db.base.r} {
		want := map[string]string{"journal_mode": "wal", "busy_timeout": "5000", "foreign_keys": "1", "synchronous": "1"}
		if name == "read" {
			want["query_only"] = "1"
		}
		for pragma, w := range want {
			var got string
			if err := conn.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil || got != w {
				t.Errorf("%s PRAGMA %s = %q, %v; want %q", name, pragma, got, err, w)
			}
		}
	}
	if _, err := db.base.r.ExecContext(ctx, `DELETE FROM articles`); err == nil {
		t.Error("read pool accepted a write")
	}
}

func TestFileModes(t *testing.T) {
	db, dir := openTemp(t)
	if err := db.Save(context.Background(), []model.Article{art("a", t0)}); err != nil {
		t.Fatal(err)
	}
	if err := db.Tenant("acme").MarkSeen(context.Background(), "w", t0); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(dir, "base.db"):            0o600,
		filepath.Join(dir, "tenants", "acme.db"): 0o600,
		filepath.Join(dir, "tenants"):            0o755,
	} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode %o, want %o", path, got, want)
		}
	}
}

func TestSaveIdempotent(t *testing.T) {
	db, _ := openTemp(t)
	a := art("a1", t0)
	a.Items, a.Tickers, a.Topics = []string{"2.02"}, []string{"AAPL"}, []string{"ai"}
	a.Form, a.ClusterID = "8-K", "c1"
	a.Score = model.Score{Relevance: 0.8, Materiality: 4, Tone: -0.5, Kind: model.EventResults, Sector: "tech", Reasons: []string{"r1", "r2"}, LensVersion: "v1"}
	mustSave(t, db, a, art("a2", t0.Add(time.Hour)))
	mustSave(t, db, a, art("a2", t0.Add(time.Hour)))
	mustSave(t, db, a)

	got, err := db.Range(context.Background(), t0.Add(-time.Hour), t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a2", "a1"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("ids %v, want %v", ids(got), want)
	}
	if !reflect.DeepEqual(got[1], a) {
		t.Errorf("round trip\n got %+v\nwant %+v", got[1], a)
	}
	if got[0].Score.LensVersion != "" || got[0].Score.Relevance != 0 {
		t.Errorf("unscored article has score %+v", got[0].Score)
	}
	var scores int
	db.base.r.QueryRow(`SELECT COUNT(*) FROM scores`).Scan(&scores)
	if scores != 1 {
		t.Errorf("%d score rows, want 1", scores)
	}
}

func TestSaveImmutableFieldsAndScoreRule(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	a := art("a1", t0)
	a.Score = model.Score{Relevance: 0.9, Materiality: 5, Kind: model.EventResults, LensVersion: "v1"}
	db.now = func() time.Time { return t0 }
	mustSave(t, db, a)

	b := a
	b.SourceID, b.Domain, b.URL = "other", "other.com", "https://other.com/x"
	b.Published, b.Fetched = t0.Add(48*time.Hour), t0.Add(49*time.Hour)
	b.Title, b.Summary, b.Form = "New title", "New summary", "10-Q"
	b.Items, b.Tickers, b.Topics, b.ClusterID = []string{"1.01"}, []string{"MSFT"}, []string{"cloud"}, "c9"
	b.Score = model.Score{Relevance: 0.1, Materiality: 1, LensVersion: "v1"} // same version: ignored
	mustSave(t, db, b)

	got, err := db.Get(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	want := a
	want.Title, want.Summary, want.Form = b.Title, b.Summary, b.Form
	want.Items, want.Tickers, want.Topics, want.ClusterID = b.Items, b.Tickers, b.Topics, b.ClusterID
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after update\n got %+v\nwant %+v", got, want)
	}

	// A new lens version adds a row and becomes the latest score.
	db.now = func() time.Time { return t0.Add(time.Hour) }
	c := a
	c.Score = model.Score{Relevance: 0.3, Materiality: 2, LensVersion: "v2"}
	mustSave(t, db, c)
	got, _ = db.Get(ctx, "a1")
	if got.Score.LensVersion != "v2" || got.Score.Relevance != 0.3 {
		t.Errorf("score %+v, want v2", got.Score)
	}
	var n int
	db.base.r.QueryRow(`SELECT COUNT(*) FROM scores WHERE article_id = 'a1'`).Scan(&n)
	if n != 2 {
		t.Errorf("%d score rows, want 2", n)
	}
}

func TestSaveRejectsScoreWithoutLensVersion(t *testing.T) {
	db, _ := openTemp(t)
	bad := art("bad", t0)
	bad.Score = model.Score{Relevance: 0.5}
	err := db.Save(context.Background(), []model.Article{art("good", t0), bad})
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, err := db.Get(context.Background(), "good"); !errors.Is(err, ErrNotFound) {
		t.Errorf("batch was partly saved: %v", err)
	}
	if err := db.Save(context.Background(), []model.Article{{SourceID: "s"}}); err == nil {
		t.Error("article without ID accepted")
	}
}

func TestLatestScore(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	a := art("a", t0)
	clock := t0
	db.now = func() time.Time { return clock }
	save := func(version string, rel float64, at time.Time) {
		clock = at
		x := a
		x.Score = model.Score{Relevance: rel, LensVersion: version}
		mustSave(t, db, x)
	}
	save("v2", 0.2, t0)
	save("v1", 0.1, t0.Add(time.Hour)) // newer by time wins over the larger version
	got, _ := db.Get(ctx, "a")
	if got.Score.LensVersion != "v1" {
		t.Errorf("latest by time: got %q, want v1", got.Score.LensVersion)
	}
	save("v3", 0.3, t0.Add(time.Hour)) // tie on time: larger version wins
	got, _ = db.Get(ctx, "a")
	if got.Score.LensVersion != "v3" {
		t.Errorf("tie: got %q, want v3", got.Score.LensVersion)
	}
	day, _ := db.Day(ctx, t0)
	if len(day) != 1 || day[0].Score.LensVersion != "v3" {
		t.Errorf("Day returned %+v", day)
	}
}

func TestDayBoundariesNonUTC(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	plus5 := time.FixedZone("plus5", 5*3600)
	// Local day 2026-03-10 in plus5 is [2026-03-09 19:00 UTC, 2026-03-10 19:00 UTC).
	start := time.Date(2026, 3, 10, 0, 0, 0, 0, plus5)
	mustSave(t, db,
		art("before", start.Add(-time.Millisecond)),
		art("first", start),
		art("last", start.Add(24*time.Hour-time.Millisecond)),
		art("after", start.Add(24*time.Hour)),
	)
	got, err := db.Day(ctx, time.Date(2026, 3, 10, 15, 30, 0, 0, plus5))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"last", "first"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("plus5 day: %v, want %v", ids(got), want)
	}
	// The same instant seen in UTC falls on a different calendar day.
	got, _ = db.Day(ctx, time.Date(2026, 3, 10, 10, 30, 0, 0, time.UTC))
	if want := []string{"last", "first"}; len(got) != 0 && reflect.DeepEqual(ids(got), want) {
		t.Errorf("UTC day returned the plus5 day: %v", ids(got))
	}

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	// 2026-03-08 has 23 hours in New York (clocks go forward).
	ds := time.Date(2026, 3, 8, 0, 0, 0, 0, ny)
	mustSave(t, db, art("ny-in", ds.Add(22*time.Hour+59*time.Minute)), art("ny-out", ds.Add(23*time.Hour)))
	got, _ = db.Day(ctx, ds.Add(5*time.Hour))
	if want := []string{"ny-in"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("DST day: %v, want %v", ids(got), want)
	}
}

func TestRangeHalfOpen(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	mustSave(t, db, art("c", t0), art("a", t0), art("b", t0.Add(time.Second)), art("d", t0.Add(2*time.Second)))
	got, err := db.Range(ctx, t0, t0.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	// published desc, then id asc; the upper bound is excluded.
	if want := []string{"b", "a", "c"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("got %v, want %v", ids(got), want)
	}
	if got, _ := db.Range(ctx, t0, t0); len(got) != 0 {
		t.Errorf("empty range returned %v", ids(got))
	}
	if got[0].Published.Location() != time.UTC {
		t.Errorf("Published location %v, want UTC", got[0].Published.Location())
	}
}

func TestGetNotFound(t *testing.T) {
	db, _ := openTemp(t)
	_, err := db.Get(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSearch(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	a1 := art("a1", t0)
	a1.Title, a1.Tickers = "Apple raises quarterly dividend", []string{"AAPL"}
	a2 := art("a2", t0.Add(time.Hour))
	a2.Title, a2.Summary, a2.Tickers = "Chip makers rally", "Free cash flow improves at Microsoft", []string{"MSFT", "NVDA"}
	a3 := art("a3", t0.Add(2*time.Hour))
	a3.Title, a3.Summary = "Central bank holds rates", "Cash is king"
	mustSave(t, db, a1, a2, a3)

	tests := []struct {
		name, match string
		from, to    time.Time
		limit       int
		want        []string
	}{
		{"ticker", "AAPL", time.Time{}, time.Time{}, 0, []string{"a1"}},
		{"ticker lower case", "nvda", time.Time{}, time.Time{}, 0, []string{"a2"}},
		{"ticker column", "tickers:MSFT", time.Time{}, time.Time{}, 0, []string{"a2"}},
		{"phrase", `"free cash flow"`, time.Time{}, time.Time{}, 0, []string{"a2"}},
		{"phrase no match", `"cash free flow"`, time.Time{}, time.Time{}, 0, nil},
		{"or", "dividend OR rates", time.Time{}, time.Time{}, 0, []string{"a3", "a1"}},
		{"not", "cash NOT flow", time.Time{}, time.Time{}, 0, []string{"a3"}},
		{"prefix", "quart*", time.Time{}, time.Time{}, 0, []string{"a1"}},
		{"from", "dividend OR rates", t0.Add(time.Hour), time.Time{}, 0, []string{"a3"}},
		{"to excludes", "dividend OR rates", time.Time{}, t0.Add(2 * time.Hour), 0, []string{"a1"}},
		{"limit", "dividend OR rates", time.Time{}, time.Time{}, 1, []string{"a3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := db.Search(ctx, tc.match, tc.from, tc.to, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ids(got), tc.want) {
				t.Errorf("got %v, want %v", ids(got), tc.want)
			}
		})
	}

	for _, bad := range []string{"", "   ", `"unterminated`, "AND AND", "foo(", "BRK.B", "nosuchcolumn:x"} {
		if _, err := db.Search(ctx, bad, time.Time{}, time.Time{}, 0); err == nil {
			t.Errorf("Search(%q) returned no error", bad)
		}
	}

	// The index follows updates: the old title stops matching.
	a1.Title = "Apple announces buyback"
	mustSave(t, db, a1)
	if got, _ := db.Search(ctx, "dividend", time.Time{}, time.Time{}, 0); len(got) != 0 {
		t.Errorf("stale index entry: %v", ids(got))
	}
	if got, _ := db.Search(ctx, "buyback", time.Time{}, time.Time{}, 0); !reflect.DeepEqual(ids(got), []string{"a1"}) {
		t.Errorf("updated title not found: %v", ids(got))
	}
	checkFTS(t, db)
}

func TestSearchReturnsLatestScore(t *testing.T) {
	db, _ := openTemp(t)
	a := art("a", t0)
	a.Score = model.Score{Relevance: 0.7, LensVersion: "v1"}
	mustSave(t, db, a)
	got, err := db.Search(context.Background(), "title", time.Time{}, time.Time{}, 0)
	if err != nil || len(got) != 1 || got[0].Score.LensVersion != "v1" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestHas(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	var as []model.Article
	var all []string
	for i := range 1200 { // more than one chunk
		as = append(as, art(fmt.Sprintf("id%04d", i), t0))
		all = append(all, fmt.Sprintf("id%04d", i))
	}
	mustSave(t, db, as...)
	query := append([]string{"missing1", "id0000", "id1199", "id0600", "missing2"}, all[:10]...)
	got, err := db.Has(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if !got["id0000"] || !got["id1199"] || !got["id0600"] || got["missing1"] || got["missing2"] {
		t.Errorf("Has = %v", got)
	}
	if len(got) != 12 {
		t.Errorf("%d found, want 12", len(got))
	}
	if got, err := db.Has(ctx, nil); err != nil || len(got) != 0 {
		t.Errorf("Has(nil) = %v, %v", got, err)
	}
	if got, err := db.Has(ctx, all); err != nil || len(got) != len(all) {
		t.Errorf("Has(all) = %d, %v", len(got), err)
	}
}

func TestFilterVisible(t *testing.T) {
	as := []model.Article{
		{ID: "1", SourceID: "a"}, {ID: "2", SourceID: "b"}, {ID: "3", SourceID: "a"}, {ID: "4", SourceID: "c"},
	}
	got := FilterVisible(as, map[string]bool{"a": true, "c": true, "b": false})
	if want := []string{"1", "3", "4"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("got %v, want %v", ids(got), want)
	}
	if got := FilterVisible(as, nil); len(got) != 0 {
		t.Errorf("nil map kept %v", ids(got))
	}
	if got := FilterVisible(nil, map[string]bool{"a": true}); len(got) != 0 {
		t.Errorf("nil articles gave %v", got)
	}
}

func TestSourceState(t *testing.T) {
	db, dir := openTemp(t)
	ctx := context.Background()
	st, h, err := db.LoadSource(ctx, "unknown")
	if err != nil || !reflect.DeepEqual(st, feeds.State{}) || !reflect.DeepEqual(h, scheduler.Health{}) {
		t.Fatalf("unknown source: %+v %+v %v", st, h, err)
	}
	st = feeds.State{ETag: `"abc"`, LastModified: "Mon", Seen: []string{"x", "y"}, Cursor: "40"}
	h = scheduler.Health{SourceID: "s1", LastOK: t0, LastError: "boom", Failures: 3, NextDue: t0.Add(time.Hour), CircuitOpen: true}
	if err := db.SaveSource(ctx, "s1", st, h); err != nil {
		t.Fatal(err)
	}
	st2 := feeds.State{Cursor: "80"}
	if err := db.SaveSource(ctx, "s2", st2, scheduler.Health{}); err != nil {
		t.Fatal(err)
	}
	check := func(db *DB) {
		t.Helper()
		gotS, gotH, err := db.LoadSource(ctx, "s1")
		if err != nil || !reflect.DeepEqual(gotS, st) || !gotH.LastOK.Equal(h.LastOK) || !gotH.NextDue.Equal(h.NextDue) ||
			gotH.LastError != "boom" || gotH.Failures != 3 || !gotH.CircuitOpen || gotH.SourceID != "s1" {
			t.Errorf("s1: %+v %+v %v", gotS, gotH, err)
		}
		if gotS, _, _ := db.LoadSource(ctx, "s2"); !reflect.DeepEqual(gotS, st2) {
			t.Errorf("s2: %+v", gotS)
		}
	}
	check(db)
	var _ scheduler.StateStore = db
	st.Cursor = "replaced"
	if err := db.SaveSource(ctx, "s1", st, h); err != nil {
		t.Fatal(err)
	}
	if gotS, _, _ := db.LoadSource(ctx, "s1"); gotS.Cursor != "replaced" {
		t.Errorf("not replaced: %+v", gotS)
	}
	st.Cursor = "40"
	db.SaveSource(ctx, "s1", st, h)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	check(db2)
}

func TestTenantIsolation(t *testing.T) {
	db, dir := openTemp(t)
	ctx := context.Background()
	a, b := db.Tenant("acme"), db.Tenant("beta")
	if a.ID() != "acme" || b.ID() != "beta" {
		t.Fatalf("ids %q %q", a.ID(), b.ID())
	}
	if err := a.Labels().Put(ctx, labels.Label{ArticleID: "x", Verdict: labels.Keep, At: t0, LensVersion: "v1", Sampled: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.PutHits(ctx, []watch.Hit{{WatchID: "w1", ArticleID: "x", At: t0, Alerts: []string{"big"}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.MarkSeen(ctx, "w1", t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Labels().All(ctx); err != nil || len(got) != 0 {
		t.Errorf("beta labels: %+v %v", got, err)
	}
	if got, err := b.Hits(ctx, "w1", time.Time{}); err != nil || len(got) != 0 {
		t.Errorf("beta hits: %+v %v", got, err)
	}
	if n, err := b.Unread(ctx, "w1"); err != nil || n != 0 {
		t.Errorf("beta unread %d %v", n, err)
	}
	got, err := a.Labels().All(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("acme labels: %+v %v", got, err)
	}
	want := labels.Label{TenantID: "acme", ArticleID: "x", Verdict: labels.Keep, At: t0, LensVersion: "v1", Sampled: true}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("label %+v, want %+v", got[0], want)
	}
	// Writing a label that names another tenant is refused.
	if err := b.Labels().Put(ctx, labels.Label{TenantID: "acme", ArticleID: "y", Verdict: labels.Drop}); err == nil {
		t.Error("cross-tenant label accepted")
	}
	for _, name := range []string{"acme.db", "beta.db"} {
		if _, err := os.Stat(filepath.Join(dir, "tenants", name)); err != nil {
			t.Errorf("missing tenant file: %v", err)
		}
	}
	// Tenant data is not in the base file.
	var n int
	err = db.base.r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('labels', 'hits', 'seen')`).Scan(&n)
	if err != nil || n != 0 {
		t.Errorf("tenant tables in base: %d %v", n, err)
	}
}

func TestInvalidTenantID(t *testing.T) {
	db, dir := openTemp(t)
	ctx := context.Background()
	before := listTree(t, dir)
	for _, id := range []string{"../x", "", "a/b", "..", "A", "x.y", `a\b`, strings.Repeat("a", 41), "-a", "a\x00b"} {
		ts := db.Tenant(id)
		calls := map[string]error{
			"Put":      ts.Labels().Put(ctx, labels.Label{ArticleID: "a", Verdict: labels.Keep}),
			"PutHits":  ts.PutHits(ctx, []watch.Hit{{WatchID: "w", ArticleID: "a"}}),
			"MarkSeen": ts.MarkSeen(ctx, "w", t0),
		}
		_, calls["All"] = ts.Labels().All(ctx)
		_, calls["Hits"] = ts.Hits(ctx, "w", time.Time{})
		_, calls["Unread"] = ts.Unread(ctx, "w")
		for name, err := range calls {
			if err == nil {
				t.Errorf("tenant %q: %s returned no error", id, name)
			}
		}
	}
	if after := listTree(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("files changed:\nbefore %v\nafter  %v", before, after)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "x.db")); err == nil {
		t.Error("path traversal created a file")
	}
}

func listTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if !strings.HasPrefix(rel, "base.db") { // -wal and -shm come and go
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLabelHistory(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	ls := db.Tenant("acme").Labels()
	put := func(article string, v labels.Verdict, at time.Time) {
		t.Helper()
		if err := ls.Put(ctx, labels.Label{ArticleID: article, Verdict: v, At: at}); err != nil {
			t.Fatal(err)
		}
	}
	put("a", labels.Keep, t0)
	put("b", labels.Drop, t0.Add(time.Minute))
	put("a", labels.Drop, t0.Add(2*time.Minute)) // supersedes the first
	put("c", labels.Keep, t0.Add(time.Minute))   // ties with b; appended later, so newer
	got, err := ls.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type pair struct {
		a string
		v labels.Verdict
	}
	var have []pair
	for _, l := range got {
		have = append(have, pair{l.ArticleID, l.Verdict})
	}
	if want := []pair{{"a", labels.Drop}, {"c", labels.Keep}, {"b", labels.Drop}}; !reflect.DeepEqual(have, want) {
		t.Errorf("got %v, want %v", have, want)
	}
	var rows int
	db.tenantsFor(t, "acme").QueryRow(`SELECT COUNT(*) FROM labels`).Scan(&rows)
	if rows != 4 {
		t.Errorf("history has %d rows, want 4", rows)
	}
	if err := ls.Put(ctx, labels.Label{ArticleID: "z", Verdict: "maybe"}); err == nil {
		t.Error("unknown verdict accepted")
	}
	if err := ls.Put(ctx, labels.Label{Verdict: labels.Keep}); err == nil {
		t.Error("empty article accepted")
	}
}

// tenantsFor returns the read pool of an open tenant database.
func (db *DB) tenantsFor(t *testing.T, id string) *sql.DB {
	t.Helper()
	h, err := db.tenantHandle(id)
	if err != nil {
		t.Fatal(err)
	}
	return h.r
}

func TestHitsAndUnread(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	ts := db.Tenant("acme")
	if n, err := ts.Unread(ctx, "w1"); err != nil || n != 0 {
		t.Fatalf("empty unread %d %v", n, err)
	}
	hits := []watch.Hit{
		{WatchID: "w1", ArticleID: "a", At: t0, Alerts: []string{"one", "two"}},
		{WatchID: "w1", ArticleID: "b", At: t0.Add(time.Hour)},
		{WatchID: "w1", ArticleID: "c", At: t0.Add(time.Hour)},
		{WatchID: "w2", ArticleID: "a", At: t0},
	}
	if err := ts.PutHits(ctx, hits); err != nil {
		t.Fatal(err)
	}
	// The first record of a (watch, article) pair wins.
	if err := ts.PutHits(ctx, []watch.Hit{{WatchID: "w1", ArticleID: "a", At: t0.Add(24 * time.Hour), Alerts: []string{"changed"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := ts.Hits(ctx, "w1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []watch.Hit{hits[1], hits[2], hits[0]}; !reflect.DeepEqual(got, want) {
		t.Errorf("hits\n got %+v\nwant %+v", got, want)
	}
	if got, _ := ts.Hits(ctx, "w1", t0.Add(time.Hour)); len(got) != 2 { // since is inclusive
		t.Errorf("since: %d hits, want 2", len(got))
	}
	if n, _ := ts.Unread(ctx, "w1"); n != 3 {
		t.Errorf("unread %d, want 3", n)
	}
	if n, _ := ts.Unread(ctx, "w2"); n != 1 {
		t.Errorf("w2 unread %d, want 1", n)
	}
	if err := ts.MarkSeen(ctx, "w1", t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := ts.Unread(ctx, "w1"); n != 2 { // strictly newer than the marker
		t.Errorf("unread %d, want 2", n)
	}
	if err := ts.MarkSeen(ctx, "w1", t0.Add(-time.Hour)); err != nil { // never moves back
		t.Fatal(err)
	}
	if n, _ := ts.Unread(ctx, "w1"); n != 2 {
		t.Errorf("marker moved back: unread %d, want 2", n)
	}
	if err := ts.MarkSeen(ctx, "w1", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, _ := ts.Unread(ctx, "w1"); n != 0 {
		t.Errorf("unread %d, want 0", n)
	}
	if n, _ := ts.Unread(ctx, "w2"); n != 1 {
		t.Errorf("w2 marker leaked: unread %d, want 1", n)
	}
	if err := ts.PutHits(ctx, []watch.Hit{{WatchID: "w1", ArticleID: "d", At: t0.Add(2 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	if n, _ := ts.Unread(ctx, "w1"); n != 1 {
		t.Errorf("unread %d, want 1", n)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := art("a", t0)
	a.Tickers = []string{"AAPL"}
	a.Score = model.Score{Relevance: 0.5, LensVersion: "v1", Reasons: []string{"r"}}
	mustSave(t, db, a)
	ts := db.Tenant("acme")
	ts.Labels().Put(ctx, labels.Label{ArticleID: "a", Verdict: labels.Keep, At: t0})
	ts.PutHits(ctx, []watch.Hit{{WatchID: "w", ArticleID: "a", At: t0}})
	ts.MarkSeen(ctx, "w", t0.Add(-time.Second))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.Get(ctx, "a")
	if err != nil || !reflect.DeepEqual(got, a) {
		t.Errorf("article after reopen: %+v %v", got, err)
	}
	if s, _ := db.Search(ctx, "AAPL", time.Time{}, time.Time{}, 0); len(s) != 1 {
		t.Errorf("search after reopen: %v", ids(s))
	}
	ts = db.Tenant("acme")
	if l, _ := ts.Labels().All(ctx); len(l) != 1 {
		t.Errorf("labels after reopen: %v", l)
	}
	if n, _ := ts.Unread(ctx, "w"); n != 1 {
		t.Errorf("unread after reopen %d", n)
	}
	var versions int
	db.base.r.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&versions)
	if versions != 2 {
		t.Errorf("schema version %d", versions)
	}
}

func TestMigrationRefusesNewerDatabase(t *testing.T) {
	for _, kind := range []string{"base", "tenant"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			db, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "base.db")
			if kind == "tenant" {
				if err := db.Tenant("acme").MarkSeen(context.Background(), "w", t0); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(dir, "tenants", "acme.db")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(`INSERT INTO schema_migrations (version, name, applied_ms) VALUES (999, '0999_future.sql', 0)`); err != nil {
				t.Fatal(err)
			}
			raw.Close()

			db, err = Open(dir)
			if kind == "base" {
				if err == nil || !strings.Contains(err.Error(), "newer than this binary") {
					t.Fatalf("Open err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			err = db.Tenant("acme").MarkSeen(context.Background(), "w", t0)
			if err == nil || !strings.Contains(err.Error(), "newer than this binary") {
				t.Fatalf("tenant err = %v", err)
			}
		})
	}
}

func TestMigrationsApplyInOrderAndAtomically(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0001_a.sql": {Data: []byte(`CREATE TABLE a (x INTEGER);`)},
		"m/0002_b.sql": {Data: []byte(`CREATE TABLE b (x INTEGER); INSERT INTO nosuchtable VALUES (1);`)},
	}
	ms, err := loadMigrations(fsys, "m")
	if err != nil || len(ms) != 2 {
		t.Fatalf("%v %v", ms, err)
	}
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	if err := migrate(context.Background(), raw, ms, time.Now); err == nil {
		t.Fatal("expected failure of migration 2")
	}
	var tables, version int
	raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('a', 'b')`).Scan(&tables)
	raw.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version)
	if tables != 1 || version != 1 {
		t.Errorf("tables=%d version=%d, want 1 and 1 (migration 2 rolled back)", tables, version)
	}
}

func TestLoadMigrationsValidation(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		ok    bool
	}{
		{"fine", []string{"0001_a.sql", "0002_b.sql"}, true},
		{"gap", []string{"0001_a.sql", "0003_c.sql"}, false},
		{"starts at two", []string{"0002_b.sql"}, false},
		{"bad name", []string{"init.sql"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for _, f := range tc.files {
				fsys["m/"+f] = &fstest.MapFile{Data: []byte("SELECT 1;")}
			}
			_, err := loadMigrations(fsys, "m")
			if (err == nil) != tc.ok {
				t.Errorf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
	for _, set := range []struct {
		name string
		fs   migrationFS
		dir  string
	}{{"base", baseFS, "migrations/base"}, {"tenant", tenantFS, "migrations/tenant"}} {
		if ms, err := loadMigrations(set.fs, set.dir); err != nil || len(ms) == 0 {
			t.Errorf("%s: %v %v", set.name, ms, err)
		}
	}
}

func TestClosedStore(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	ts := db.Tenant("acme")
	if err := ts.MarkSeen(ctx, "w", t0); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	errs := map[string]error{
		"Save":    db.Save(ctx, []model.Article{art("a", t0)}),
		"Put":     ts.Labels().Put(ctx, labels.Label{ArticleID: "a", Verdict: labels.Keep}),
		"PutHits": ts.PutHits(ctx, []watch.Hit{{WatchID: "w", ArticleID: "a"}}),
		"Mark":    ts.MarkSeen(ctx, "w", t0),
		"Save2":   db.SaveSource(ctx, "s", feeds.State{}, scheduler.Health{}),
	}
	_, errs["Day"] = db.Day(ctx, t0)
	_, errs["Range"] = db.Range(ctx, t0, t0)
	_, errs["Get"] = db.Get(ctx, "a")
	_, errs["Search"] = db.Search(ctx, "a", time.Time{}, time.Time{}, 0)
	_, errs["Has"] = db.Has(ctx, []string{"a"})
	_, _, errs["Load"] = db.LoadSource(ctx, "s")
	_, errs["All"] = ts.Labels().All(ctx)
	_, errs["Hits"] = ts.Hits(ctx, "w", time.Time{})
	_, errs["Unread"] = ts.Unread(ctx, "w")
	for name, err := range errs {
		if err == nil {
			t.Errorf("%s after Close returned no error", name)
		}
	}
	if _, err := db.Tenant("other").Unread(ctx, "w"); err == nil {
		t.Error("new tenant opened after Close")
	}
}

func TestConcurrentWritersAndReaders(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	const writers, batches, per = 6, 10, 20
	var wg sync.WaitGroup
	errc := make(chan error, 64)
	stop := make(chan struct{})

	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ts := db.Tenant(fmt.Sprintf("tenant-%d", w%3))
			for b := range batches {
				var as []model.Article
				for i := range per {
					a := art(fmt.Sprintf("w%d-b%d-i%d", w, b, i), t0.Add(time.Duration(b*per+i)*time.Second))
					a.Tickers = []string{"AAPL"}
					a.Score = model.Score{Relevance: 0.5, LensVersion: "v1"}
					as = append(as, a)
				}
				if err := db.Save(ctx, as); err != nil {
					errc <- err
					return
				}
				if err := ts.PutHits(ctx, []watch.Hit{{WatchID: "w", ArticleID: as[0].ID, At: t0}}); err != nil {
					errc <- err
					return
				}
				if err := ts.Labels().Put(ctx, labels.Label{ArticleID: as[0].ID, Verdict: labels.Keep}); err != nil {
					errc <- err
					return
				}
				if err := db.SaveSource(ctx, fmt.Sprintf("s%d", w), feeds.State{Cursor: fmt.Sprint(b)}, scheduler.Health{}); err != nil {
					errc <- err
					return
				}
			}
		}()
	}
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			ts := db.Tenant("tenant-1")
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := db.Range(ctx, t0.Add(-time.Hour), t0.Add(time.Hour)); err != nil {
					errc <- err
					return
				}
				if _, err := db.Search(ctx, "AAPL", time.Time{}, time.Time{}, 10); err != nil {
					errc <- err
					return
				}
				if _, err := db.Has(ctx, []string{"w0-b0-i0"}); err != nil {
					errc <- err
					return
				}
				if _, err := ts.Unread(ctx, "w"); err != nil {
					errc <- err
					return
				}
				if _, err := ts.Labels().All(ctx); err != nil {
					errc <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	readers.Wait()
	close(errc)
	for err := range errc {
		t.Error(err)
	}
	got, err := db.Range(ctx, t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil || len(got) != writers*batches*per {
		t.Errorf("%d articles, want %d (%v)", len(got), writers*batches*per, err)
	}
	if s, _ := db.Search(ctx, "AAPL", time.Time{}, time.Time{}, 1000000); len(s) != writers*batches*per {
		t.Errorf("search found %d", len(s))
	}
}

func TestSaveTenThousand(t *testing.T) {
	db, _ := openTemp(t)
	as := make([]model.Article, 10000)
	for i := range as {
		a := art(fmt.Sprintf("id%05d", i), t0.Add(time.Duration(i)*time.Second))
		a.Tickers, a.Items = []string{"AAPL", "MSFT"}, []string{"2.02"}
		a.Score = model.Score{Relevance: 0.4, Materiality: 3, Kind: model.EventResults, Sector: "tech", Reasons: []string{"a", "b"}, LensVersion: "v1"}
		as[i] = a
	}
	start := time.Now()
	if err := db.Save(context.Background(), as); err != nil {
		t.Fatal(err)
	}
	t.Logf("Save took %v", time.Since(start))
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Save of %d articles took %v, want under 5s", len(as), d)
	}
	got, err := db.Range(context.Background(), t0, t0.Add(24*time.Hour))
	if err != nil || len(got) != len(as) {
		t.Errorf("read back %d, %v", len(got), err)
	}
	checkFTS(t, db)
}

func TestSaveDuplicateIDsInOneBatch(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	first := art("a", t0)
	first.Title, first.Score = "first title", model.Score{Relevance: 0.1, LensVersion: "v1"}
	second := art("a", t0.Add(time.Hour))
	second.SourceID, second.Title = "other", "second title"
	second.Score = model.Score{Relevance: 0.9, LensVersion: "v1"}
	third := second
	third.Score = model.Score{Relevance: 0.5, LensVersion: "v2"}
	mustSave(t, db, first, second, third)

	got, err := db.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "second title" || got.SourceID != "src" || !got.Published.Equal(t0) {
		t.Errorf("merged article %+v", got)
	}
	var n int
	db.base.r.QueryRow(`SELECT COUNT(*) FROM scores WHERE article_id = 'a'`).Scan(&n)
	if n != 2 {
		t.Errorf("%d score rows, want 2", n)
	}
	var r float64
	db.base.r.QueryRow(`SELECT relevance FROM scores WHERE article_id = 'a' AND lens_version = 'v1'`).Scan(&r)
	if r != 0.1 {
		t.Errorf("v1 relevance %v, want the first (0.1)", r)
	}
	if s, err := db.Search(ctx, "first", time.Time{}, time.Time{}, 0); err != nil || len(s) != 0 {
		t.Errorf("stale index: %v %v", ids(s), err)
	}
	if s, err := db.Search(ctx, "second", time.Time{}, time.Time{}, 0); err != nil || len(s) != 1 {
		t.Errorf("index: %v %v", ids(s), err)
	}
	checkFTS(t, db)
}

// checkFTS verifies that the full-text index matches the articles table.
func checkFTS(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.base.w.Exec(`INSERT INTO articles_fts (articles_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		t.Errorf("fts integrity check: %v", err)
	}
}
