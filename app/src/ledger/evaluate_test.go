package ledger

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/watch"
)

func TestLateFetchedArticleIsUnreadAndAlerts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.saveWatch(t, "alice", alertWatch("all", alert("any", channel.File)))
	ts := f.db.Tenant("alice")

	// The user looked at 12:00. The article is from 10:00 but was only fetched
	// at 12:30.
	if err := ts.MarkSeen(ctx, "all", day(12, 0)); err != nil {
		t.Fatal(err)
	}
	f.save(t, article("late", "pub", "Late filing about annual results", day(10, 0), day(12, 30)))
	f.at(day(12, 35))

	if n, err := ts.Unread(ctx, "all"); err != nil || n != 0 {
		t.Fatalf("Unread before Evaluate = %d, %v; want 0", n, err)
	}
	if got := f.evaluate(t, "alice"); got["all"] != 1 {
		t.Fatalf("Evaluate = %v, want one new hit for all", got)
	}
	if n, err := ts.Unread(ctx, "all"); err != nil || n != 1 {
		t.Errorf("Unread after Evaluate = %d, %v; want 1 (detected after the marker)", n, err)
	}
	hits, err := ts.Hits(ctx, "all", time.Time{})
	if err != nil || len(hits) != 1 || !hits[0].At.Equal(day(12, 35)) {
		t.Errorf("hits = %+v, %v; want one detected at 12:35, not at the publication time", hits, err)
	}

	if d := f.deliver(t, "alice"); d.Sent != 1 {
		t.Errorf("Deliver = %+v, want one notification for the late article", d)
	}
	if got := f.file.notifications(); len(got) != 1 || !reflect.DeepEqual(titles(got[0].Articles), []string{"Late filing about annual results"}) {
		t.Errorf("file channel got %+v", got)
	}

	// Looking again moves the marker; the article is read.
	if err := ts.MarkSeen(ctx, "all", day(12, 36)); err != nil {
		t.Fatal(err)
	}
	if n, _ := ts.Unread(ctx, "all"); n != 0 {
		t.Errorf("Unread after MarkSeen = %d, want 0", n)
	}
}

func TestSecondEvaluateScansNothing(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", watch.Watch{ID: "all", Name: "All"})
	f.saveWatch(t, "alice", watch.Watch{ID: "query", Name: "Query", Query: "buyback"})
	f.save(t,
		article("a1", "pub", "Share buyback announced", day(9, 0), day(11, 0)),
		article("a2", "pub", "Annual results published", day(9, 30), day(11, 5)),
	)

	want := map[string]int{"all": 2, "query": 1}
	if got := f.evaluate(t, "alice"); !reflect.DeepEqual(got, want) {
		t.Fatalf("first Evaluate = %v, want %v", got, want)
	}
	if f.counter.articles != 4 { // two watches, two articles each
		t.Errorf("first Evaluate loaded %d articles, want 4", f.counter.articles)
	}

	f.counter.articles, f.counter.calls = 0, 0
	f.at(day(12, 30))
	if got := f.evaluate(t, "alice"); got["all"] != 0 || got["query"] != 0 {
		t.Errorf("second Evaluate = %v, want no new hits", got)
	}
	if f.counter.articles != 0 {
		t.Errorf("second Evaluate loaded %d articles, want none", f.counter.articles)
	}
	if f.counter.calls != 2 {
		t.Errorf("second Evaluate made %d lookups, want one per watch", f.counter.calls)
	}

	// Only what arrives later is looked at.
	f.save(t, article("a3", "pub", "Another buyback", day(12, 0), day(12, 40)))
	f.at(day(12, 45))
	f.counter.articles = 0
	if got := f.evaluate(t, "alice"); got["all"] != 1 || got["query"] != 1 {
		t.Errorf("third Evaluate = %v, want one new hit each", got)
	}
	if f.counter.articles != 2 {
		t.Errorf("third Evaluate loaded %d articles, want 2 (the new one, once per watch)", f.counter.articles)
	}
	if got, want := f.hitIDs(t, "alice", "all"), []string{"a3", "a1", "a2"}; !slices.Equal(got, want) {
		t.Errorf("hits of all = %v, want %v (each article once)", got, want)
	}
}

func TestChangedDefinitionResetsTheCursor(t *testing.T) {
	f := newFixture(t)
	w := watch.Watch{ID: "apple", Name: "Apple", Scope: watch.Scope{Tickers: []string{"AAPL"}}}
	f.saveWatch(t, "alice", w)
	a1 := article("a1", "pub", "Microsoft buyback", day(9, 0), day(10, 0))
	a1.Tickers = []string{"MSFT"}
	old := article("old", "pub", "Microsoft old news", day(1, 0), day(1, 0).AddDate(0, 0, -5))
	old.Tickers = []string{"MSFT"}
	f.save(t, a1, old)

	if got := f.evaluate(t, "alice"); got["apple"] != 0 {
		t.Fatalf("Evaluate = %v, want no hit for AAPL", got)
	}

	// Renaming does not start the watch over: nothing is loaded again.
	w.Name = "Apple Inc"
	f.saveWatch(t, "alice", w)
	f.counter.articles = 0
	f.evaluate(t, "alice")
	if f.counter.articles != 0 {
		t.Errorf("renaming the watch loaded %d articles, want none", f.counter.articles)
	}

	// Following MSFT instead starts again at now minus the window. The article
	// fetched 5 days ago is outside it.
	w.Scope.Tickers = []string{"MSFT"}
	f.saveWatch(t, "alice", w)
	f.at(day(12, 0))
	if got := f.evaluate(t, "alice"); got["apple"] != 1 {
		t.Errorf("Evaluate after the change = %v, want one hit", got)
	}
	if got := f.hitIDs(t, "alice", "apple"); !slices.Equal(got, []string{"a1"}) {
		t.Errorf("hits = %v, want only a1", got)
	}

	// The new cursor holds: nothing more is loaded.
	f.counter.articles = 0
	f.evaluate(t, "alice")
	if f.counter.articles != 0 {
		t.Errorf("Evaluate after the reset loaded %d articles, want none", f.counter.articles)
	}

	// A new query is a new definition too.
	w.Query = "microsoft"
	f.saveWatch(t, "alice", w)
	f.evaluate(t, "alice")
	if f.counter.articles == 0 {
		t.Error("changing the query did not restart the evaluation")
	}
}

func TestChangedThresholdResetsTheCursor(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", watch.Watch{ID: "all", Name: "All"})
	weak := article("weak", "pub", "Minor note", day(9, 0), day(10, 0))
	weak.Score.Relevance = 0.4
	f.save(t, weak)
	f.evaluate(t, "alice")
	if got := f.hitIDs(t, "alice", "all"); len(got) != 0 {
		t.Fatalf("hits = %v, want none below the threshold", got)
	}
	f.l.cfg.Threshold = 0.3
	if got := f.evaluate(t, "alice"); got["all"] != 1 {
		t.Errorf("Evaluate with a lower default threshold = %v, want one hit", got)
	}
}

func TestEvaluateReportsBadQueryAndKeepsOthers(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", watch.Watch{ID: "bad", Name: "Bad", Query: "ticker:"})
	f.saveWatch(t, "alice", watch.Watch{ID: "good", Name: "Good"})
	f.save(t, article("a1", "pub", "Annual results published this morning", day(9, 0), day(11, 0)))

	counts, err := f.l.Evaluate(context.Background(), "alice")
	if err == nil || !strings.Contains(err.Error(), `watch "bad"`) {
		t.Errorf("error = %v, want one that names watch \"bad\"", err)
	}
	if counts["good"] != 1 {
		t.Errorf("counts = %v, want good: 1", counts)
	}
	if _, ok := counts["bad"]; ok {
		t.Errorf("counts = %v, want no entry for the bad watch", counts)
	}
	if got := f.hitIDs(t, "alice", "good"); !slices.Equal(got, []string{"a1"}) {
		t.Errorf("good hits = %v", got)
	}
}

func TestEvaluateIsIdempotentPerWatchAndArticle(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", watch.Watch{ID: "all", Name: "All"})
	f.save(t, article("a1", "pub", "Annual results published", day(9, 0), day(11, 0)))
	f.evaluate(t, "alice")

	// A watch reset re-evaluates the article; the hit stays single and keeps
	// its detection time.
	f.at(day(13, 0))
	w, _ := f.l.cfg.Watches("alice").Get("all")
	w.Lens.MinMateriality = 1
	f.saveWatch(t, "alice", w)
	if got := f.evaluate(t, "alice"); got["all"] != 0 {
		t.Errorf("re-evaluation reported %v new hits, want 0", got)
	}
	hits, _ := f.db.Tenant("alice").Hits(context.Background(), "all", time.Time{})
	if len(hits) != 1 || !hits[0].At.Equal(day(12, 0)) {
		t.Errorf("hits = %+v, want one still stamped 12:00", hits)
	}
}

func TestLicenceHitsAndNotifications(t *testing.T) {
	f := newFixture(t)
	for _, tenantID := range []string{"alice", "bob"} {
		f.saveWatch(t, tenantID, alertWatch("all", alert("any", channel.File)))
	}
	f.save(t,
		article("p1", "pub", "Public filing about annual results", day(9, 0), day(11, 0)),
		article("i1", "int", "Internal note about annual results", day(9, 30), day(11, 5)),
	)

	for tenantID, want := range map[string][]string{"alice": {"i1", "p1"}, "bob": {"p1"}} {
		f.evaluate(t, tenantID)
		got := f.hitIDs(t, tenantID, "all")
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s hits = %v, want %v", tenantID, got, want)
		}
	}

	f.deliver(t, "bob")
	for _, n := range f.file.notifications() {
		if n.TenantID != "bob" {
			continue
		}
		for _, a := range n.Articles {
			if a.SourceID != "pub" {
				t.Errorf("bob's notification carries the article %q of source %q", a.ID, a.SourceID)
			}
		}
		if strings.Contains(n.Body, "Internal note") {
			t.Errorf("bob's notification body mentions an invisible article:\n%s", n.Body)
		}
	}
}

func TestPreviewWritesNothingAndRespectsLicences(t *testing.T) {
	f := newFixture(t)
	w := watch.Watch{ID: "all", Name: "All"}
	f.saveWatch(t, "bob", w)
	f.save(t,
		article("p1", "pub", "Public filing about annual results", day(11, 0), day(11, 30)),
		article("i1", "int", "Internal note about annual results", day(11, 0), day(11, 30)),
		article("weak", "pub", "Minor note", day(11, 0), day(11, 30)),
	)
	weak := article("weak", "pub", "Minor note", day(11, 0), day(11, 30))
	weak.Score.Relevance = 0.1
	weak.Score.LensVersion = "l2" // a later score that is not relevant
	f.save(t, weak)
	f.at(day(12, 0))

	got, err := f.l.Preview(context.Background(), "bob", w, day(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "p1" {
		t.Errorf("Preview = %v, want only p1", titles(got))
	}
	if hits := f.hitIDs(t, "bob", "all"); len(hits) != 0 {
		t.Errorf("Preview stored hits %v", hits)
	}
	if cur, ok, _ := f.db.Tenant("bob").Cursor(context.Background(), "all"); ok {
		t.Errorf("Preview stored a cursor %+v", cur)
	}

	w.Query = "ticker:"
	if _, err := f.l.Preview(context.Background(), "bob", w, day(10, 0)); err == nil {
		t.Error("Preview accepted a bad query")
	}
}

func TestInvalidTenantID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.l.Evaluate(ctx, "../x"); err == nil {
		t.Error("Evaluate accepted \"../x\" as a tenant ID")
	}
	if _, err := f.l.Deliver(ctx, "../x"); err == nil {
		t.Error("Deliver accepted \"../x\" as a tenant ID")
	}
	if _, err := f.l.Digests(ctx, "../x"); err == nil {
		t.Error("Digests accepted \"../x\" as a tenant ID")
	}
	if _, err := f.l.Preview(ctx, "../x", watch.Watch{}, day(0, 0)); err == nil {
		t.Error("Preview accepted \"../x\" as a tenant ID")
	}
}

func TestEvaluatePagesThroughManyArticles(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", watch.Watch{ID: "all", Name: "All"})
	var as []model.Article
	for i := 0; i < pageSize+50; i++ {
		as = append(as, article(fmt.Sprintf("n%04d", i), "pub", "Annual results "+fmt.Sprintf("n%04d", i), day(9, 0), day(10, 0).Add(time.Duration(i/7)*time.Second)))
	}
	f.save(t, as...)
	if got := f.evaluate(t, "alice"); got["all"] != pageSize+50 {
		t.Errorf("Evaluate = %v hits, want %d", got["all"], pageSize+50)
	}
	if got := f.evaluate(t, "alice"); got["all"] != 0 {
		t.Errorf("second Evaluate = %v, want 0", got)
	}
}
