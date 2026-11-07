package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/classify"
	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/entity"
	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

var testNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

var (
	pubSource = model.Source{ID: "pub", Domain: "pub.example", Licence: model.LicenceRedistributable}
	intSource = model.Source{ID: "int", Domain: "int.example", Licence: model.LicenceInternal, Owner: "alice"}
)

// fixture is a Pipeline over a real store in a temporary folder.
type fixture struct {
	p    *Pipeline
	db   *store.DB
	lens *lens.Lens
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return newFixtureOn(t, db, db)
}

// newFixtureOn builds a fixture whose pipeline writes to s while db is used
// for reading back.
func newFixtureOn(t *testing.T, db *store.DB, s store.Store) *fixture {
	t.Helper()
	l := lens.New(config.Lens{Threshold: 0.5, Boost: []string{"earnings"}}, lens.Options{})
	f := &fixture{db: db, lens: l}
	f.p = New(Config{
		Store: s,
		Lens:  l,
		Entities: entity.New([]model.Entity{
			{Ticker: "AAPL", Name: "Apple Inc"},
			{Ticker: "MSFT", Name: "Microsoft Corp"},
		}),
		Now: func() time.Time { return testNow },
	})
	return f
}

func article(id, title string, published time.Time) model.Article {
	return model.Article{
		ID:        id,
		URL:       "https://example.com/" + id,
		Title:     title,
		Summary:   "",
		Published: published,
		Fetched:   testNow,
	}
}

func (f *fixture) get(t *testing.T, id string) model.Article {
	t.Helper()
	a, err := f.db.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get(%q): %v", id, err)
	}
	return a
}

func (f *fixture) ingest(t *testing.T, src model.Source, as ...model.Article) int {
	t.Helper()
	n, err := f.p.Ingest(context.Background(), src, as)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return n
}

func TestIngestSanitizesAndNormalises(t *testing.T) {
	f := newFixture(t)
	a := article("a1", "\x1b[31mAcme\x1b[0m reports ​earnings\x1b]0;evil\x07", testNow)
	a.Summary = "Line\x1b[2J one"
	a.Domain = " WWW.Example.COM. "
	f.ingest(t, pubSource, a)

	got := f.get(t, "a1")
	if got.Title != "Acme reports earnings" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Summary != "Line one" {
		t.Errorf("Summary = %q", got.Summary)
	}
	if got.Domain != "example.com" {
		t.Errorf("Domain = %q, want example.com", got.Domain)
	}
	if got.SourceID != "pub" {
		t.Errorf("SourceID = %q, want pub", got.SourceID)
	}
}

func TestIngestDomainFallsBackToSource(t *testing.T) {
	f := newFixture(t)
	f.ingest(t, pubSource, article("a1", "Quarterly report published today", testNow))
	if got := f.get(t, "a1").Domain; got != "pub.example" {
		t.Errorf("Domain = %q, want pub.example", got)
	}
}

func TestEachCommitGetsAFetchTimeAfterTheEarlierOnes(t *testing.T) {
	f := newFixture(t)
	a := article("a1", "First quarterly report published today", testNow)
	a.Fetched = testNow.Add(-time.Hour) // the start of a slow poll
	b := article("b1", "Second annual report published today", testNow)

	f.ingest(t, pubSource, b)
	f.ingest(t, pubSource, a)
	got, later := f.get(t, "a1").Fetched, f.get(t, "b1").Fetched
	if !got.After(later) {
		t.Errorf("a1 fetched %v, b1 %v: the later commit must have the later fetch time, whatever the poll started", got, later)
	}
}

func TestIngestCountsOnlyNew(t *testing.T) {
	f := newFixture(t)
	a1 := article("a1", "First quarterly report published today", testNow)
	a2 := article("a2", "Second annual report published today", testNow)
	a3 := article("a3", "Third dividend notice published today", testNow)

	if n := f.ingest(t, pubSource, a1, a2, a1); n != 2 {
		t.Errorf("first ingest = %d new, want 2 (a repeated ID counts once)", n)
	}
	if n := f.ingest(t, pubSource, a1, a2); n != 0 {
		t.Errorf("repeat ingest = %d new, want 0", n)
	}
	if n := f.ingest(t, pubSource, a2, a3); n != 1 {
		t.Errorf("mixed ingest = %d new, want 1", n)
	}
}

func TestIngestDropsArticlesWithoutIDOrTitle(t *testing.T) {
	f := newFixture(t)
	n := f.ingest(t, pubSource,
		article("", "A title without an ID", testNow),
		article("blank", " ​\x1b[1m ", testNow),
		article("ok", "A proper title for this article", testNow),
	)
	if n != 1 {
		t.Errorf("new = %d, want 1", n)
	}
	has, err := f.db.Has(context.Background(), []string{"", "blank", "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if has["blank"] || !has["ok"] {
		t.Errorf("stored = %v, want only ok", has)
	}
	if n := f.ingest(t, pubSource); n != 0 {
		t.Errorf("empty ingest = %d, want 0", n)
	}
}

func TestIngestUnionsTickers(t *testing.T) {
	f := newFixture(t)
	a := article("a1", "Apple (NASDAQ: AAPL) reports record earnings", testNow)
	a.Tickers = []string{" msft ", "AAPL", ""}
	b := article("a2", "Nothing to see in this headline today", testNow)
	f.ingest(t, pubSource, a, b)

	if got, want := f.get(t, "a1").Tickers, []string{"AAPL", "MSFT"}; !slices.Equal(got, want) {
		t.Errorf("Tickers = %v, want %v", got, want)
	}
	if got := f.get(t, "a2").Tickers; len(got) != 0 {
		t.Errorf("Tickers = %v, want none", got)
	}
}

func TestIngestScores(t *testing.T) {
	f := newFixture(t)
	f.ingest(t, pubSource, article("a1", "Apple reports record quarterly earnings", testNow))
	got := f.get(t, "a1").Score
	if got.LensVersion != f.lens.Version() {
		t.Errorf("LensVersion = %q, want %q", got.LensVersion, f.lens.Version())
	}
	if got.Relevance <= 0 || got.Materiality == 0 || got.Kind == "" || len(got.Reasons) == 0 {
		t.Errorf("score missing: %+v", got)
	}
}

func TestIngestClustersAcrossPolls(t *testing.T) {
	f := newFixture(t)
	const headline = "Acme Corp announces new share buyback programme worth billions"
	first := article("a1", headline, testNow.Add(-3*time.Hour))
	other := article("o1", "Federal Reserve leaves interest rates unchanged this week", testNow.Add(-3*time.Hour))
	f.ingest(t, pubSource, first, other)
	firstCluster := f.get(t, "a1").ClusterID
	if firstCluster == "" || firstCluster == f.get(t, "o1").ClusterID {
		t.Fatalf("clusters after first poll: a1 %q, o1 %q", firstCluster, f.get(t, "o1").ClusterID)
	}

	// A syndicated copy from another domain arrives in a later poll.
	copyA := article("a2", headline, testNow.Add(-1*time.Hour))
	copyA.Domain = "other.example"
	f.ingest(t, pubSource, copyA)
	if got := f.get(t, "a2").ClusterID; got != firstCluster {
		t.Errorf("later copy ClusterID = %q, want %q", got, firstCluster)
	}

	// A copy that claims an earlier publication time must not change the
	// cluster IDs already stored.
	earlier := article("a0", headline, testNow.Add(-5*time.Hour))
	f.ingest(t, pubSource, earlier)
	if got := f.get(t, "a0").ClusterID; got != firstCluster {
		t.Errorf("earlier copy ClusterID = %q, want %q", got, firstCluster)
	}
	if got := f.get(t, "a1").ClusterID; got != firstCluster {
		t.Errorf("stored article ClusterID changed to %q", got)
	}
}

func TestIngestClustersWithinBatch(t *testing.T) {
	f := newFixture(t)
	const headline = "Acme Corp announces new share buyback programme worth billions"
	a, b := article("a1", headline, testNow), article("a2", headline, testNow.Add(time.Hour))
	f.ingest(t, pubSource, a, b)
	if x, y := f.get(t, "a1").ClusterID, f.get(t, "a2").ClusterID; x == "" || x != y {
		t.Errorf("cluster IDs %q and %q, want equal and not empty", x, y)
	}
}

func TestIngestIsIdempotent(t *testing.T) {
	f := newFixture(t)
	const headline = "Acme Corp announces new share buyback programme worth billions"
	batch := []model.Article{
		article("a1", headline, testNow),
		article("a2", headline, testNow.Add(time.Hour)),
		article("a3", "Federal Reserve leaves interest rates unchanged this week", testNow),
	}
	f.ingest(t, pubSource, batch...)
	before, err := f.db.Range(context.Background(), testNow.Add(-24*time.Hour), testNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n := f.ingest(t, pubSource, batch...); n != 0 {
		t.Errorf("second ingest = %d new, want 0", n)
	}
	after, err := f.db.Range(context.Background(), testNow.Add(-24*time.Hour), testNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 3 || len(after) != 3 {
		t.Fatalf("articles: %d then %d, want 3", len(before), len(after))
	}
	for i := range before {
		if before[i].ID != after[i].ID || before[i].ClusterID != after[i].ClusterID || before[i].Score.LensVersion != after[i].Score.LensVersion {
			t.Errorf("article %s changed: %+v -> %+v", before[i].ID, before[i], after[i])
		}
	}
}

// failingRange is a store whose Range fails, as a stage before Save would.
type failingRange struct{ store.Store }

func (failingRange) Range(context.Context, time.Time, time.Time) ([]model.Article, error) {
	return nil, errors.New("disk on fire")
}

func TestIngestFailureStoresNothing(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := newFixtureOn(t, db, failingRange{db})

	_, err = f.p.Ingest(context.Background(), pubSource, []model.Article{article("a1", "A headline that is long enough", testNow)})
	if err == nil || !strings.HasPrefix(err.Error(), "pipeline:") {
		t.Fatalf("error = %v, want one starting with \"pipeline:\"", err)
	}
	has, err := db.Has(context.Background(), []string{"a1"})
	if err != nil {
		t.Fatal(err)
	}
	if has["a1"] {
		t.Error("article stored although the pipeline failed before Save")
	}
}

// fakeClassifier returns fixed answers and counts nothing else.
type fakeClassifier struct {
	topics  []string
	sector  string
	version string
}

func (c fakeClassifier) Classify(model.Article) ([]string, string) { return c.topics, c.sector }
func (c fakeClassifier) Version() string                           { return c.version }

// withClassifier returns a fixture whose pipeline also uses c.
func withClassifier(t *testing.T, c Classifier) *fixture {
	t.Helper()
	f := newFixture(t)
	f.p.cfg.Classifier = c
	return f
}

func TestIngestClassifies(t *testing.T) {
	cl := classify.New(
		[]config.TopicRule{
			{Name: "buybacks-and-dividends", Words: []string{"buyback"}},
			{Name: "ai-infrastructure", Tickers: []string{"MSFT"}},
		},
		[]config.SectorRule{{Name: "technology", Tickers: []string{"MSFT", "AAPL"}}},
	)
	f := withClassifier(t, cl)
	a := article("a1", "Apple (NASDAQ: AAPL) announces a record buyback", testNow)
	a.Topics = []string{"fetcher-topic", "buybacks-and-dividends"}
	b := article("a2", "Microsoft expands its cloud", testNow)
	c := article("a3", "Nothing to see in this headline today", testNow)
	f.ingest(t, pubSource, a, b, c)

	got := f.get(t, "a1")
	if want := []string{"buybacks-and-dividends", "fetcher-topic"}; !slices.Equal(got.Topics, want) {
		t.Errorf("a1 Topics = %v, want %v (fetcher topic kept, no repeat, sorted)", got.Topics, want)
	}
	if got.Score.Sector != "technology" {
		t.Errorf("a1 Sector = %q, want technology", got.Score.Sector)
	}
	// The ticker comes from entity resolution, which runs before classification.
	if got := f.get(t, "a2"); !slices.Equal(got.Topics, []string{"ai-infrastructure"}) || got.Score.Sector != "technology" {
		t.Errorf("a2 = topics %v sector %q, want [ai-infrastructure] technology", got.Topics, got.Score.Sector)
	}
	if got := f.get(t, "a3"); len(got.Topics) != 0 || got.Score.Sector != "" {
		t.Errorf("a3 = topics %v sector %q, want none", got.Topics, got.Score.Sector)
	}
}

func TestIngestClassifierVersion(t *testing.T) {
	f := withClassifier(t, fakeClassifier{topics: []string{"x"}, version: "c1-abcd1234"})
	f.ingest(t, pubSource, article("a1", "Apple reports record quarterly earnings", testNow))
	if got, want := f.get(t, "a1").Score.LensVersion, f.lens.Version()+"+c1-abcd1234"; got != want {
		t.Errorf("LensVersion = %q, want %q", got, want)
	}
}

func TestIngestEmptyClassifierVersionLeavesLensVersion(t *testing.T) {
	f := withClassifier(t, fakeClassifier{topics: []string{"x"}, sector: "energy"})
	f.ingest(t, pubSource, article("a1", "Apple reports record quarterly earnings", testNow))
	got := f.get(t, "a1")
	if got.Score.LensVersion != f.lens.Version() {
		t.Errorf("LensVersion = %q, want %q", got.Score.LensVersion, f.lens.Version())
	}
	if !slices.Equal(got.Topics, []string{"x"}) || got.Score.Sector != "energy" {
		t.Errorf("topics %v sector %q, want [x] energy", got.Topics, got.Score.Sector)
	}
}

func TestIngestNewRulesGiveNewScoreRow(t *testing.T) {
	f := withClassifier(t, fakeClassifier{sector: "energy", version: "c1-00000001"})
	f.ingest(t, pubSource, article("a1", "Apple reports record quarterly earnings", testNow))
	f.p.cfg.Classifier = fakeClassifier{sector: "technology", version: "c1-00000002"}
	f.ingest(t, pubSource, article("a1", "Apple reports record quarterly earnings", testNow))
	if got := f.get(t, "a1").Score; got.Sector != "technology" || !strings.HasSuffix(got.LensVersion, "+c1-00000002") {
		t.Errorf("latest score = sector %q version %q, want technology and the new version", got.Sector, got.LensVersion)
	}
}

func TestIngestClassifierDoesNotOverrideLensSector(t *testing.T) {
	f := newFixture(t)
	f.p.cfg.Classifier = fakeClassifier{sector: "energy", version: "c1-1"}
	a := article("a1", "Apple reports record quarterly earnings", testNow)
	batch := prepare(pubSource, []model.Article{a})
	f.p.classify(&batch[0])
	batch[0].Score = model.Score{Sector: "financials", LensVersion: "l"}
	f.p.applyClassification(&batch[0], "energy")
	if batch[0].Score.Sector != "financials" {
		t.Errorf("Sector = %q, want the lens sector financials", batch[0].Score.Sector)
	}
}

func TestIngestNilClassifierChangesNothing(t *testing.T) {
	for name, cl := range map[string]Classifier{
		"nil interface":   nil,
		"nil *Classifier": (*classify.Classifier)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			f := withClassifier(t, cl)
			a := article("a1", "Apple reports record quarterly earnings with a buyback", testNow)
			a.Topics = []string{"fetcher-topic"}
			f.ingest(t, pubSource, a)
			got := f.get(t, "a1")
			if !slices.Equal(got.Topics, []string{"fetcher-topic"}) {
				t.Errorf("Topics = %v, want only the fetcher's", got.Topics)
			}
			// The classifier adds nothing; the claims moved the label from the lens's first
			// keyword (a buyback) to the results the headline leads with, and say so in the version.
			if got.Score.Sector != "" || got.Score.LensVersion != f.lens.Version()+"+assert-2" || got.Score.Kind != model.EventResults {
				t.Errorf("Sector %q, LensVersion %q, kind %s, want empty, %q and results", got.Score.Sector, got.Score.LensVersion, got.Score.Kind, f.lens.Version()+"+assert-2")
			}
		})
	}
}

// scoreVersions lists the lens versions stored for an article, read straight
// from the database file in dir.
func scoreVersions(t *testing.T, dir, id string) []string {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "base.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	rows, err := raw.Query(`SELECT lens_version FROM scores WHERE article_id = ? ORDER BY lens_version`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestRescoreAddsAScoreVersionAndKeepsTheOld(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := newFixtureOn(t, db, db)
	f.p.cfg.Classifier = fakeClassifier{sector: "energy", version: "c1-00000001"}
	headline := "Acme Corp announces new share buyback programme worth billions"
	a1 := article("a1", headline, testNow.Add(-2*time.Hour))
	a2 := article("a2", headline, testNow.Add(-time.Hour))
	other := article("o1", "Federal Reserve leaves interest rates unchanged this week", testNow.Add(-90*time.Minute))
	outside := article("old", "Apple (NASDAQ: AAPL) reports annual results", testNow.Add(-30*24*time.Hour))
	f.ingest(t, pubSource, a1, a2, other)
	f.ingest(t, intSource, outside)
	oldVersion := f.get(t, "a1").Score.LensVersion
	cluster := f.get(t, "a1").ClusterID

	f.p.cfg.Classifier = fakeClassifier{topics: []string{"capital"}, sector: "technology", version: "c1-00000002"}
	n, err := f.p.Rescore(context.Background(), testNow.Add(-3*time.Hour), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("Rescore = %d articles, want 3 (the old one is outside the span)", n)
	}
	for _, id := range []string{"a1", "a2", "o1"} {
		got := f.get(t, id)
		if got.Score.Sector != "technology" || !strings.HasSuffix(got.Score.LensVersion, "+c1-00000002") {
			t.Errorf("%s: latest score = sector %q version %q, want the new classifier", id, got.Score.Sector, got.Score.LensVersion)
		}
		if !slices.Equal(got.Topics, []string{"capital"}) {
			t.Errorf("%s: Topics = %v, want [capital]", id, got.Topics)
		}
	}
	if got := f.get(t, "a1").ClusterID; got != cluster || got != f.get(t, "a2").ClusterID {
		t.Errorf("a1 cluster %q, a2 cluster %q, want both to stay %q", got, f.get(t, "a2").ClusterID, cluster)
	}
	if got := f.get(t, "old").Score; got.Sector != "energy" || strings.HasSuffix(got.LensVersion, "c1-00000002") {
		t.Errorf("an article outside the span was rescored: %+v", got)
	}

	want := []string{oldVersion, f.lens.Version() + "+c1-00000002"}
	slices.Sort(want)
	if got := scoreVersions(t, dir, "a1"); !slices.Equal(got, want) {
		t.Errorf("score versions of a1 = %v, want %v", got, want)
	}
}

func TestRescoreWithSameRulesChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.ingest(t, pubSource, article("a1", "Apple reports record quarterly earnings", testNow))
	before := f.get(t, "a1")
	n, err := f.p.Rescore(context.Background(), testNow.Add(-time.Hour), testNow.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("Rescore = %d, %v; want 1", n, err)
	}
	if after := f.get(t, "a1"); after.Score.LensVersion != before.Score.LensVersion || after.ClusterID != before.ClusterID {
		t.Errorf("article changed: %+v -> %+v", before, after)
	}
}

func TestRescoreFailureIsReported(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := newFixtureOn(t, db, failingRange{db})
	if _, err := f.p.Rescore(context.Background(), testNow.Add(-time.Hour), testNow); err == nil || !strings.HasPrefix(err.Error(), "pipeline:") {
		t.Errorf("error = %v, want one starting with \"pipeline:\"", err)
	}
}
