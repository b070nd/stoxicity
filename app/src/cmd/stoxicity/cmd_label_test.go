package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// seedEvalData stores six scored articles of the visible source. With the
// threshold 0.5 the lens calls e1 to e4 relevant.
//
//	id  relevance  lens version  domain
//	e1  0.9        v1            a.example.com
//	e2  0.8        v1            a.example.com
//	e3  0.7        v1            a.example.com
//	e4  0.6        v2            a.example.com
//	e5  0.4        v2            b.example.com
//	e6  0.3        v2            b.example.com
func seedEvalData(t *testing.T, e *testEnv) {
	t.Helper()
	type row struct {
		id, domain, version string
		relevance           float64
	}
	var arts []model.Article
	for i, r := range []row{
		{"e1", "a.example.com", "v1", 0.9}, {"e2", "a.example.com", "v1", 0.8}, {"e3", "a.example.com", "v1", 0.7},
		{"e4", "a.example.com", "v2", 0.6}, {"e5", "b.example.com", "v2", 0.4}, {"e6", "b.example.com", "v2", 0.3},
	} {
		a := scoredArticle(r.id, "news-feed", r.domain, utc(3, 1+i, 0), r.relevance, 3, model.EventResults, nil, nil)
		a.Score.LensVersion = r.version
		arts = append(arts, a)
	}
	savePageArticles(t, e, arts...)
}

// storedLabels reads the default tenant's labels straight from the database.
func storedLabels(t *testing.T, e *testEnv) map[string]labels.Label {
	t.Helper()
	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	all, err := db.Tenant("default").Labels().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]labels.Label{}
	for _, l := range all {
		out[l.ArticleID] = l
	}
	return out
}

// labelEvalData gives e1 to e6 the verdicts of TestLabelThenEval.
func labelEvalData(t *testing.T, e *testEnv) {
	t.Helper()
	for _, args := range [][]string{
		{"label", "keep", "e1", "e2"},
		{"label", "drop", "e3"},
		{"label", "keep", "e4"},
		{"label", "drop", "-sampled", "e5"},
		{"label", "keep", "e6", "-sampled"},
	} {
		e.run(t, args...).wantCode(t, exitOK)
	}
}

func TestLabelThenEval(t *testing.T) {
	e := newTestEnv(t)
	seedEvalData(t, e)

	r := e.run(t, "label", "keep", "e1", "e2")
	r.wantCode(t, exitOK)
	r.wantOut(t, "keep\te1\tHeadline of e1", "keep\te2\t")
	labelEvalData(t, e)

	got := storedLabels(t, e)
	if len(got) != 6 {
		t.Fatalf("%d labels stored, want 6", len(got))
	}
	if l := got["e5"]; l.Verdict != labels.Drop || !l.Sampled || l.LensVersion != "v2" || !l.At.Equal(testNow) || l.TenantID != "default" {
		t.Errorf("label of e5 = %+v", l)
	}
	if l := got["e1"]; l.Verdict != labels.Keep || l.Sampled || l.LensVersion != "v1" {
		t.Errorf("label of e1 = %+v", l)
	}

	r = e.run(t, "label", "list", "-n", "3")
	r.wantCode(t, exitOK)
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "\tkeep\tsampled\te6\tHeadline of e6") || !strings.Contains(lines[1], "\tdrop\tsampled\te5\t") || !strings.Contains(lines[2], "\tkeep\t\te4\t") {
		t.Errorf("label list -n 3 =\n%s", r.out)
	}
	if !strings.HasPrefix(lines[0], "2026-11-03 11:00\t") {
		t.Errorf("times are not in the configured zone: %q", lines[0])
	}

	// Ranked: 0.9 yes, 0.8 yes, 0.7 no, 0.6 yes, 0.4 no, 0.3 yes. Brier is
	// (0.01+0.04+0.49+0.16+0.16+0.49)/6, ECE (0.1+0.2+0.7+0.4+0.4+0.7)/6.
	r = e.run(t, "eval")
	r.wantCode(t, exitOK)
	r.wantOut(t,
		"Evaluation of the lens, tenant default",
		": 6, of which 0 skipped",
		"evaluated: 6 (kept 4, dropped 2, sampled 2)",
		"warning: only 6 evaluated labels (fewer than 20); the numbers below are not meaningful",
		"threshold: 0.50",
		"precision@10  0.667  (only 6 cases)",
		"precision@20  0.667  (only 6 cases)",
		"precision@50  0.667  (only 6 cases)",
		"Brier  0.225",
		"ECE    0.417",
		"At threshold 0.50, all labels",
		"precision  0.750 (3/4)",
		"recall     0.750 (3/4)",
		"sampled labels only (2;",
		"precision  n/a",
		"recall     0.000 (0/1)",
	)
	wantFields(t, r.out, "v1", "v1", "3", "2", "0.667", "1.000", "0.180")
	wantFields(t, r.out, "v2", "v2", "3", "2", "1.000", "0.500", "0.270")
	wantFields(t, r.out, "a.example.com", "a.example.com", "4", "4", "3", "0.750")
	r.wantNotOut(t, "b.example.com") // two labels are too few for a row

	r = e.run(t, "eval", "-threshold", "0.65")
	r.wantCode(t, exitOK)
	r.wantOut(t, "threshold: 0.65", "precision  0.667 (2/3)", "recall     0.500 (2/4)")
}

// wantFields checks that out has a line whose fields are exactly want, found
// by the first field.
func wantFields(t *testing.T, out, first string, want ...string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == first {
			if !slices.Equal(f, want) {
				t.Errorf("row %q = %q, want %q", first, f, want)
			}
			return
		}
	}
	t.Errorf("no row starting with %q:\n%s", first, out)
}

func TestEvalWithoutLabels(t *testing.T) {
	e := newTestEnv(t)
	seedEvalData(t, e)
	r := e.run(t, "eval")
	r.wantCode(t, exitOK)
	r.wantOut(t, "labels made since", ": 0, of which 0 skipped", "evaluated: 0", "warning: only 0 evaluated labels", "No labelled articles to evaluate")
	r.wantNotOut(t, "precision@")
}

func TestEvalIgnoresLabelsOlderThanTheWindow(t *testing.T) {
	e := newTestEnv(t)
	seedEvalData(t, e)
	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	ls := db.Tenant("default").Labels()
	for id, at := range map[string]time.Time{"e1": testNow.AddDate(0, 0, -100), "e2": testNow.AddDate(0, 0, -10)} {
		if err := ls.Put(context.Background(), labels.Label{ArticleID: id, Verdict: labels.Keep, At: at, LensVersion: "v1"}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	r := e.run(t, "eval", "-days", "30")
	r.wantCode(t, exitOK)
	r.wantOut(t, "evaluated: 1 (kept 1, dropped 0, sampled 0)")
}

func TestLabelRejectsInvisibleArticles(t *testing.T) {
	e := newTestEnv(t)
	addOtherTenant(t, e)
	savePageArticles(t, e,
		scoredArticle("e1", "news-feed", "news.example.com", utc(3, 8, 0), 0.8, 3, model.EventResults, nil, nil),
		scoredArticle("x1", "private-feed", "private.example.com", utc(3, 8, 0), 0.8, 3, model.EventResults, nil, nil),
	)

	r := e.run(t, "label", "keep", "x1", "e1", "ghost")
	r.wantCode(t, exitError)
	r.wantOut(t, "keep\te1\t")
	r.wantNotOut(t, "x1", "ghost")
	for _, id := range []string{`"x1"`, `"ghost"`} {
		if !strings.Contains(r.errOut, id) {
			t.Errorf("stderr does not name %s:\n%s", id, r.errOut)
		}
	}
	if strings.Contains(r.errOut, "private") {
		t.Errorf("stderr reveals the source:\n%s", r.errOut)
	}
	// An invisible article reads exactly like a missing one.
	if a, b := strings.Replace(lineWith(r.errOut, `"x1"`), "x1", "ghost", 1), lineWith(r.errOut, `"ghost"`); a != b {
		t.Errorf("errors differ:\n%q\n%q", a, b)
	}
	got := storedLabels(t, e)
	if len(got) != 1 || got["e1"].Verdict != labels.Keep {
		t.Errorf("stored labels = %+v, want only e1", got)
	}

	// The owner of the personal source may label it.
	r = e.run(t, "-tenant", "other", "label", "drop", "x1")
	r.wantCode(t, exitOK)
	r.wantOut(t, "drop\tx1\t")
}

func lineWith(out, part string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, part) {
			return l
		}
	}
	return ""
}

func TestLabelSampleExcludesLabelled(t *testing.T) {
	e := newTestEnv(t)
	addOtherTenant(t, e)
	var arts []model.Article
	for i, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
		arts = append(arts, scoredArticle(id, "news-feed", "news.example.com", utc(1+i%3, 5+i, 0), 0.5, 3, model.EventResults, nil, nil))
	}
	arts = append(arts,
		scoredArticle("old", "news-feed", "news.example.com", utc(3, 5, 0).AddDate(0, 0, -20), 0.5, 3, model.EventResults, nil, nil),
		scoredArticle("x1", "private-feed", "private.example.com", utc(3, 5, 0), 0.5, 3, model.EventResults, nil, nil),
	)
	savePageArticles(t, e, arts...)
	e.run(t, "label", "keep", "s1").wantCode(t, exitOK)
	e.run(t, "label", "drop", "-sampled", "s2").wantCode(t, exitOK)

	r := e.run(t, "label", "sample")
	r.wantCode(t, exitOK)
	r.wantOut(t, "3 of the unlabelled articles", "label keep|drop -sampled")
	ids := sampleIDs(r.out)
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"s3", "s4", "s5"}) {
		t.Errorf("sample = %v, want s3 s4 s5 (not labelled ones, not older than 7 days, not invisible ones)", ids)
	}
	r.wantNotOut(t, "0.50", "private")

	// The clock seeds the choice, so the same clock gives the same sample.
	again := e.run(t, "label", "sample")
	if again.out != r.out {
		t.Errorf("sample changed with the same clock:\n%s\n%s", r.out, again.out)
	}
	r = e.run(t, "label", "sample", "-n", "2")
	r.wantCode(t, exitOK)
	if ids := sampleIDs(r.out); len(ids) != 2 {
		t.Errorf("-n 2 gave %v", ids)
	}
	r = e.run(t, "label", "sample", "-days", "30")
	if ids := sampleIDs(r.out); len(ids) != 4 || !slices.Contains(ids, "old") {
		t.Errorf("-days 30 gave %v, want s3 s4 s5 old", ids)
	}

	for _, id := range []string{"s3", "s4", "s5"} {
		e.run(t, "label", "keep", "-sampled", id).wantCode(t, exitOK)
	}
	r = e.run(t, "label", "sample")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no unlabelled articles between")

	// The labels remember that they came from the sample.
	if !storedLabels(t, e)["s3"].Sampled {
		t.Error("s3 is not marked sampled")
	}
}

// sampleIDs reads the article IDs of `label sample` output.
func sampleIDs(out string) []string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var ids []string
	for _, l := range lines[1:] {
		ids = append(ids, strings.Split(l, "\t")[0])
	}
	return ids
}

func TestEvalAndLabelListHideOtherTenantsSources(t *testing.T) {
	e := newTestEnv(t)
	addOtherTenant(t, e)
	seedEvalData(t, e)
	savePageArticles(t, e,
		scoredArticle("x1", "private-feed", "private.example.com", utc(3, 8, 0), 0.9, 5, model.EventResults, nil, []string{"secretsauce"}),
		scoredArticle("x2", "private-feed", "private.example.com", utc(3, 8, 1), 0.9, 5, model.EventResults, nil, nil),
		scoredArticle("x3", "private-feed", "private.example.com", utc(3, 8, 2), 0.9, 5, model.EventResults, nil, nil),
	)
	labelEvalData(t, e)

	// Labels on articles this tenant cannot read, as if the licence had
	// changed after the user labelled them, and on one that never existed.
	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"x1", "x2", "x3", "ghost"} {
		if err := db.Tenant("default").Labels().Put(context.Background(), labels.Label{ArticleID: id, Verdict: labels.Keep, At: testNow, LensVersion: "v1"}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	r := e.run(t, "eval")
	r.wantCode(t, exitOK)
	r.wantOut(t, ": 10, of which 4 skipped because the article is gone or not visible", "evaluated: 6 (kept 4, dropped 2, sampled 2)")
	r.wantNotOut(t, "private", "secret", "x1")

	r = e.run(t, "label", "list", "-n", "50")
	r.wantCode(t, exitOK)
	r.wantNotOut(t, "private", "secret", "x1", "ghost")
	if n := strings.Count(r.out, "\n"); n != 6 {
		t.Errorf("label list has %d lines, want 6:\n%s", n, r.out)
	}

	// The same labels, read by the tenant that owns the source, are not skipped.
	e.run(t, "-tenant", "other", "label", "keep", "x1").wantCode(t, exitOK)
	r = e.run(t, "-tenant", "other", "eval")
	r.wantCode(t, exitOK)
	r.wantOut(t, "evaluated: 1 (kept 1")
}

func TestLabelQueueOffersTheDoubtfulOnesAcrossKindsWithoutScoresAndSkipsLabelled(t *testing.T) {
	e := newTestEnv(t)
	addOtherTenant(t, e)
	var arts []model.Article
	kinds := []model.EventKind{model.EventResults, model.EventResults, model.EventResults, model.EventBankruptcy, model.EventOfficerChange, model.EventBylaws}
	for i, k := range kinds {
		arts = append(arts, scoredArticle(fmt.Sprintf("q%d", i+1), "news-feed", "news.example.com", utc(3, 5+i, 0), 0.45+float64(i)/50, 3, k, nil, nil))
	}
	arts = append(arts, scoredArticle("x1", "private-feed", "private.example.com", utc(3, 5, 0), 0.5, 3, model.EventResults, nil, nil))
	savePageArticles(t, e, arts...)
	e.run(t, "label", "keep", "q1").wantCode(t, exitOK)

	r := e.run(t, "label", "queue", "-n", "4")
	r.wantCode(t, exitOK)
	r.wantOut(t, "1 labels so far, 299 to go to 300", "label keep|drop ID...")
	r.wantNotOut(t, "private", "0.45", "0.47")
	ids := sampleIDs(r.out)
	if len(ids) != 4 || slices.Contains(ids, "q1") || slices.Contains(ids, "x1") {
		t.Fatalf("queue = %v: four, none labelled, none of another tenant", ids)
	}
	// Four picks: three uncertain ones from three kinds in turn, one at random.
	if n := strings.Count(r.out, "\tsampled\t"); n != 1 {
		t.Errorf("one in four is marked sampled, got %d:\n%s", n, r.out)
	}
	if again := e.run(t, "label", "queue", "-n", "4"); again.out != r.out {
		t.Errorf("the queue changed within the day:\n%s\n%s", r.out, again.out)
	}
	e.run(t, "label", "queue", "-n", "0").wantCode(t, exitUsage)
}

func TestEvalTellsPrecisionAndRecallPerKindOfEvent(t *testing.T) {
	e := newTestEnv(t)
	seedEvalData(t, e)
	labelEvalData(t, e)
	r := e.run(t, "eval")
	r.wantCode(t, exitOK)
	r.wantOut(t, "By kind of event")
	wantFields(t, r.out, "results", "results", "6", "4", "4", "0.750", "0.750")
}
