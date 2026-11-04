package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// fakeEnricher answers every article with the same opinion, records the size of
// each call and can be made to fail.
type fakeEnricher struct {
	opinion lens.Opinion
	version string
	err     error
	calls   []int
	short   bool // answer with one opinion too few
}

func (e *fakeEnricher) Opinions(_ context.Context, as []model.Article) ([]lens.Opinion, error) {
	e.calls = append(e.calls, len(as))
	if e.err != nil {
		return nil, e.err
	}
	out := make([]lens.Opinion, len(as))
	for i := range out {
		out[i] = e.opinion
	}
	if e.short {
		out = out[:len(out)-1]
	}
	return out, nil
}

func (e *fakeEnricher) Version() string { return e.version }

// opinion is a confident answer to the kind, tone, topic and sector questions.
var opinion = lens.Opinion{
	Source: "laya", Version: "laya-test",
	Kind: model.EventNonReliance, KindConfidence: 0.9,
	Tone: -0.456, ToneConfidence: 0.8,
	Topic: "capital", TopicConfidence: 0.85,
	Sector: "technology", SectorConfidence: 0.8,
	Materiality: 5, MaterialityConfidence: 0.9,
}

// withEnricher returns a fixture whose pipeline also asks e, with the given
// answers switched on.
func withEnricher(t *testing.T, e Enricher, use lens.Use) *fixture {
	t.Helper()
	f := newFixture(t)
	f.p.cfg.Enricher = e
	f.p.cfg.Use = use
	f.p.cfg.MinConfidence = 0.7
	return f
}

func TestIngestAppliesTheOpinion(t *testing.T) {
	e := &fakeEnricher{opinion: opinion, version: "laya-auto-q00000001"}
	f := withEnricher(t, e, lens.Use{Kind: true, Tone: true, Topics: true})
	f.p.cfg.Classifier = fakeClassifier{topics: []string{"b-topic"}, version: "c1-1"}
	a := article("a1", "Acme Corp updates its shareholders on the quarter", testNow)
	a.Topics = []string{"z-topic", "capital"}
	f.ingest(t, pubSource, a)

	got := f.get(t, "a1")
	if got.Score.Kind != model.EventNonReliance || got.Score.Materiality != 4 {
		t.Errorf("kind %q materiality %d, want non_reliance 4", got.Score.Kind, got.Score.Materiality)
	}
	if got.Score.Tone != -0.46 {
		t.Errorf("Tone = %v, want -0.46", got.Score.Tone)
	}
	if want := []string{"b-topic", "capital", "z-topic"}; !slices.Equal(got.Topics, want) {
		t.Errorf("Topics = %v, want %v (sorted, no repeat)", got.Topics, want)
	}
	if want := f.lens.Version() + "+c1-1+laya-auto-q00000001"; got.Score.LensVersion != want {
		t.Errorf("LensVersion = %q, want %q", got.Score.LensVersion, want)
	}
	if want := f.p.Version(); got.Score.LensVersion != want {
		t.Errorf("LensVersion = %q, Version() = %q: they must agree", got.Score.LensVersion, want)
	}
	for _, want := range []string{
		"laya: kind non_reliance (0.90)",
		"laya: tone -0.46",
		"laya: topic capital (0.85)",
		"laya (advisory): sector technology (0.80)",
		"laya (advisory): materiality 5.0 (0.90)",
	} {
		if !slices.Contains(got.Score.Reasons, want) {
			t.Errorf("reasons lack %q:\n%q", want, got.Score.Reasons)
		}
	}
	if got.Score.Sector != "" {
		t.Errorf("Sector = %q: the sector was not switched on", got.Score.Sector)
	}
}

func TestIngestRecordsAdvisoryReasonsWhenNothingIsSwitchedOn(t *testing.T) {
	plain := newFixture(t)
	plain.ingest(t, pubSource, article("a1", "Acme Corp updates its shareholders on the quarter", testNow))
	want := plain.get(t, "a1")

	e := &fakeEnricher{opinion: opinion, version: "laya-x"}
	f := withEnricher(t, e, lens.Use{})
	f.ingest(t, pubSource, article("a1", "Acme Corp updates its shareholders on the quarter", testNow))
	got := f.get(t, "a1")

	if got.Score.Kind != want.Score.Kind || got.Score.Tone != want.Score.Tone || got.Score.Relevance != want.Score.Relevance ||
		got.Score.Materiality != want.Score.Materiality || len(got.Topics) != 0 {
		t.Errorf("a switched-off opinion changed the score: %+v, was %+v", got.Score, want.Score)
	}
	for _, field := range []string{"kind non_reliance", "tone -0.46", "topic capital", "sector technology", "materiality 5.0"} {
		if !slices.ContainsFunc(got.Score.Reasons, func(r string) bool { return strings.HasPrefix(r, "laya (advisory): "+field) }) {
			t.Errorf("no advisory reason for %q:\n%q", field, got.Score.Reasons)
		}
	}
	if !strings.HasSuffix(got.Score.LensVersion, "+laya-x") {
		t.Errorf("LensVersion = %q, want the enricher suffix", got.Score.LensVersion)
	}
}

func TestIngestOpinionWithoutAnyConfidenceStillGetsTheSuffix(t *testing.T) {
	e := &fakeEnricher{opinion: lens.Opinion{Source: "laya", Kind: model.EventResults}, version: "laya-x"}
	f := withEnricher(t, e, lens.Use{Kind: true, Tone: true, Topics: true})
	f.ingest(t, pubSource, article("a1", "Acme Corp updates its shareholders on the quarter", testNow))
	got := f.get(t, "a1")
	if !strings.HasSuffix(got.Score.LensVersion, "+laya-x") {
		t.Errorf("LensVersion = %q, want the enricher suffix", got.Score.LensVersion)
	}
	for _, r := range got.Score.Reasons {
		if strings.HasPrefix(r, "laya") {
			t.Errorf("reason %q from an opinion that said nothing", r)
		}
	}
}

func TestEnricherDoesNotOverrideTheClassifiersSector(t *testing.T) {
	e := &fakeEnricher{opinion: opinion, version: "laya-x"}
	f := withEnricher(t, e, lens.Use{Sector: true})
	f.p.cfg.Classifier = fakeClassifier{sector: "energy", version: "c1-1"}
	f.ingest(t, pubSource, article("a1", "Acme Corp updates its shareholders on the quarter", testNow))
	if got := f.get(t, "a1").Score.Sector; got != "energy" {
		t.Errorf("Sector = %q, want the classifier's energy", got)
	}

	// Where the classifier found none, the opinion fills it in.
	f.p.cfg.Classifier = fakeClassifier{version: "c1-1"}
	f.ingest(t, pubSource, article("a2", "Acme Corp updates its shareholders on the year", testNow))
	if got := f.get(t, "a2").Score.Sector; got != "technology" {
		t.Errorf("Sector = %q, want laya's technology", got)
	}
}

func TestIngestAsksOnceForTheWholeBatch(t *testing.T) {
	e := &fakeEnricher{opinion: opinion, version: "laya-x"}
	f := withEnricher(t, e, lens.Use{})
	f.ingest(t, pubSource,
		article("a1", "Acme Corp updates its shareholders on the quarter", testNow),
		article("a2", "Beta Corp updates its shareholders on the year", testNow),
		article("a3", "Gamma Corp updates its shareholders on the half", testNow),
		article("a1", "Acme Corp updates its shareholders on the quarter", testNow), // repeated ID
	)
	if !slices.Equal(e.calls, []int{3}) {
		t.Errorf("Opinions calls = %v, want one call with 3 articles", e.calls)
	}
	f.ingest(t, pubSource) // nothing to score
	if len(e.calls) != 1 {
		t.Errorf("an empty batch called the enricher: %v", e.calls)
	}
}

func TestEnricherErrorKeepsRuleScoresAndRescoreAddsTheRow(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := newFixtureOn(t, db, db)
	e := &fakeEnricher{opinion: opinion, version: "laya-x", err: errors.New("sidecar down")}
	f.p.cfg.Enricher = e
	f.p.cfg.Use = lens.Use{Kind: true, Topics: true}
	f.p.cfg.MinConfidence = 0.7

	f.ingest(t, pubSource, article("a1", "Acme Corp updates its shareholders on the quarter", testNow))
	got := f.get(t, "a1")
	if got.Score.LensVersion != f.lens.Version() {
		t.Errorf("LensVersion = %q, want the rule lens version %q alone", got.Score.LensVersion, f.lens.Version())
	}
	if got.Score.Kind != model.EventOther || len(got.Topics) != 0 {
		t.Errorf("kind %q topics %v: a failed enricher must leave the rule score", got.Score.Kind, got.Topics)
	}
	for _, r := range got.Score.Reasons {
		if strings.Contains(r, "laya") {
			t.Errorf("reason %q although the enricher failed", r)
		}
	}
	if v := f.p.Version(); !strings.HasSuffix(v, "+laya-x") {
		t.Errorf("Version() = %q, want the version a new score would carry", v)
	}

	// The sidecar is back: a rescore adds the row with the opinion and keeps the old one.
	e.err = nil
	n, err := f.p.Rescore(context.Background(), testNow.Add(-time.Hour), testNow.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("Rescore = %d, %v; want 1", n, err)
	}
	got = f.get(t, "a1")
	if got.Score.Kind != model.EventNonReliance || !slices.Equal(got.Topics, []string{"capital"}) {
		t.Errorf("after rescore: kind %q topics %v, want non_reliance [capital]", got.Score.Kind, got.Topics)
	}
	want := []string{f.lens.Version(), f.lens.Version() + "+laya-x"}
	slices.Sort(want)
	if versions := scoreVersions(t, dir, "a1"); !slices.Equal(versions, want) {
		t.Errorf("score versions = %v, want %v", versions, want)
	}
}

func TestEnricherWithTooFewOpinionsCountsAsFailure(t *testing.T) {
	e := &fakeEnricher{opinion: opinion, version: "laya-x", short: true}
	f := withEnricher(t, e, lens.Use{Kind: true})
	f.ingest(t, pubSource,
		article("a1", "Acme Corp updates its shareholders on the quarter", testNow),
		article("a2", "Beta Corp updates its shareholders on the year", testNow))
	for _, id := range []string{"a1", "a2"} {
		if got := f.get(t, id).Score; got.LensVersion != f.lens.Version() || got.Kind != model.EventOther {
			t.Errorf("%s: %+v, want the untouched rule score", id, got)
		}
	}
}

func TestRescoreAsksOncePerBatchAndStopsAfterAFailure(t *testing.T) {
	e := &fakeEnricher{opinion: opinion, version: "laya-x"}
	f := withEnricher(t, e, lens.Use{})
	var as []model.Article
	for i := range rescoreBatch + 1 {
		as = append(as, article(fmt.Sprintf("a%03d", i), fmt.Sprintf("Company number %d updates its shareholders", i), testNow.Add(-time.Duration(i)*time.Second)))
	}
	f.ingest(t, pubSource, as...)
	e.calls = nil

	if n, err := f.p.Rescore(context.Background(), testNow.Add(-time.Hour), testNow.Add(time.Hour)); err != nil || n != len(as) {
		t.Fatalf("Rescore = %d, %v; want %d", n, err, len(as))
	}
	if !slices.Equal(e.calls, []int{rescoreBatch, 1}) {
		t.Errorf("calls = %v, want one per batch of %d", e.calls, rescoreBatch)
	}

	e.calls, e.err = nil, errors.New("sidecar down")
	if _, err := f.p.Rescore(context.Background(), testNow.Add(-time.Hour), testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(e.calls, []int{rescoreBatch}) {
		t.Errorf("calls = %v after a failure, want the second batch not to ask again", e.calls)
	}
}

func TestFailureLogDoesNotNameTheAddress(t *testing.T) {
	var ue error = &url.Error{Op: "Post", URL: "http://user:secret@127.0.0.1:8000/v1/x", Err: errors.New("connection refused")}
	ue = fmt.Errorf("laya: %w", ue)
	if got := withoutAddress(ue); got != "connection refused" {
		t.Errorf("withoutAddress = %q, want the cause alone", got)
	}
	if got := withoutAddress(errors.New("plain")); got != "plain" {
		t.Errorf("withoutAddress = %q, want plain", got)
	}
}

func TestVersionComposition(t *testing.T) {
	f := newFixture(t)
	l := f.lens.Version()
	if got := f.p.Version(); got != l {
		t.Errorf("Version() = %q, want the lens alone %q", got, l)
	}
	f.p.cfg.Classifier = fakeClassifier{version: "c1-1"}
	if got := f.p.Version(); got != l+"+c1-1" {
		t.Errorf("Version() = %q", got)
	}
	f.p.cfg.Classifier = fakeClassifier{} // no version
	f.p.cfg.Enricher = &fakeEnricher{version: "laya-x"}
	if got := f.p.Version(); got != l+"+laya-x" {
		t.Errorf("Version() = %q", got)
	}
	f.p.cfg.Classifier = fakeClassifier{version: "c1-1"}
	if got := f.p.Version(); got != l+"+c1-1+laya-x" {
		t.Errorf("Version() = %q", got)
	}
}

func TestFilingsAreNotSentToTheEnricher(t *testing.T) {
	e := &fakeEnricher{opinion: opinion, version: "laya-auto-q00000001"}
	f := withEnricher(t, e, lens.Use{Kind: true, Tone: true, Topics: true})
	filing := article("f1", "8-K - Acme Corp (0000000001) (Filer)", testNow)
	filing.Form = "8-K"
	news := article("n1", "Acme Corp updates its shareholders on the quarter", testNow)
	f.ingest(t, pubSource, filing, news)

	if !slices.Equal(e.calls, []int{1}) {
		t.Errorf("enricher calls = %v, want one call with only the article that has no form", e.calls)
	}
	if v := f.get(t, "f1").Score.LensVersion; strings.Contains(v, "laya") {
		t.Errorf("filing version = %q, want no enricher part", v)
	}
	if v := f.get(t, "n1").Score.LensVersion; !strings.HasSuffix(v, "laya-auto-q00000001") {
		t.Errorf("news version = %q, want the enricher part", v)
	}
}

func TestOnlyFilingsMeansNoEnricherCall(t *testing.T) {
	e := &fakeEnricher{opinion: opinion, version: "laya-auto-q00000001"}
	f := withEnricher(t, e, lens.Use{Kind: true})
	filing := article("f1", "4 - Acme Corp", testNow)
	filing.Form = "4"
	f.ingest(t, pubSource, filing)
	if len(e.calls) != 0 {
		t.Errorf("enricher calls = %v, want none", e.calls)
	}
}
