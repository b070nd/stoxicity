package coverage

import (
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
)

var t0 = time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

// Synthetic sources: a regulator (tier 1), two newsrooms (tier 2) and a blog
// (tier 3).
var tiers = map[string]model.Tier{"reg": 1, "wire-a": 2, "wire-b": 2, "blog": 3}

func opt() Options {
	return Options{Tier: func(id string) model.Tier { return tiers[id] }}
}

func art(id, source, domain, cluster string, minutes int, rel float64, mat int, tone float64) model.Article {
	return model.Article{
		ID: id, SourceID: source, Domain: domain, ClusterID: cluster,
		Published: t0.Add(time.Duration(minutes) * time.Minute),
		Score:     model.Score{Relevance: rel, Materiality: mat, Tone: tone},
	}
}

func byID(ss []Story) map[string]Story {
	m := map[string]Story{}
	for _, s := range ss {
		m[s.ID] = s
	}
	return m
}

func TestBackingFollowsTheStrongestSource(t *testing.T) {
	arts := []model.Article{
		art("a1", "reg", "reg.example", "primary", 0, 0.9, 5, 0),
		art("a2", "wire-a", "wire-a.example", "primary", 10, 0.8, 4, 0),
		art("b1", "wire-a", "wire-a.example", "corr", 0, 0.7, 3, 0),
		art("b2", "wire-b", "wire-b.example", "corr", 5, 0.7, 3, 0),
		art("c1", "wire-a", "wire-a.example", "alone", 0, 0.6, 3, 0),
		art("d1", "blog", "blog.example", "opinion", 0, 0.6, 2, 0),
		art("d2", "blog", "other-blog.example", "opinion", 3, 0.6, 2, 0),
	}
	got := byID(Stories(arts, opt()))
	for id, want := range map[string]Backing{
		"primary": BackingPrimary, "corr": BackingCorroborated, "alone": BackingUnconfirmed, "opinion": BackingOpinion,
	} {
		if got[id].Backing != want {
			t.Errorf("story %q backing = %q, want %q", id, got[id].Backing, want)
		}
	}
}

func TestOutletsAreDomainsNotSources(t *testing.T) {
	// Two feeds of one domain, the same story: one outlet, not two.
	arts := []model.Article{
		art("a", "wire-a", "www.wire-a.example", "s", 0, 0.7, 3, 0),
		art("b", "wire-b", "WIRE-A.example.", "s", 4, 0.7, 3, 0),
	}
	s := Stories(arts, opt())[0]
	if s.Breadth() != 1 || len(s.Sources) != 2 {
		t.Errorf("breadth %d sources %d, want 1 outlet and 2 sources", s.Breadth(), len(s.Sources))
	}
	if s.Backing != BackingUnconfirmed {
		t.Errorf("backing = %q: two feeds of one outlet are not corroboration", s.Backing)
	}
}

func TestThinMeansMaterialButOnOneOutletWithoutAPrimarySource(t *testing.T) {
	arts := []model.Article{
		art("m", "wire-a", "wire-a.example", "material", 0, 0.9, 5, 0),
		art("n", "wire-a", "wire-a.example", "minor", 0, 0.9, 2, 0),
		art("p", "reg", "reg.example", "filing", 0, 0.9, 5, 0),
	}
	got := byID(Stories(arts, opt()))
	if !got["material"].Thin {
		t.Error("a material story on one newsroom should be thin")
	}
	if got["minor"].Thin {
		t.Error("a minor story is not thin")
	}
	if got["filing"].Thin {
		t.Error("a lone filing rests on a primary source and is not thin")
	}
}

func TestStoryFacts(t *testing.T) {
	a1 := art("a1", "wire-b", "wire-b.example", "s", 30, 0.5, 2, -0.5)
	a1.Tickers, a1.Topics = []string{"ACME"}, []string{"inflation"}
	a2 := art("a2", "wire-a", "wire-a.example", "s", 0, 0.8, 4, 0.5)
	a2.Tickers = []string{"ACME", "GLOB"}
	s := Stories([]model.Article{a1, a2}, opt())[0]
	if s.Lead.ID != "a2" {
		t.Errorf("lead = %q, want the earliest report a2", s.Lead.ID)
	}
	if s.Relevance != 0.8 || s.Materiality != 4 || s.Tone != 0 {
		t.Errorf("relevance %v materiality %d tone %v, want 0.8, 4, 0", s.Relevance, s.Materiality, s.Tone)
	}
	if s.Spread != 30*time.Minute {
		t.Errorf("spread = %v", s.Spread)
	}
	if len(s.Tickers) != 2 || s.Tickers[0] != "ACME" || len(s.Topics) != 1 {
		t.Errorf("tickers %v topics %v", s.Tickers, s.Topics)
	}
}

func TestArticlesWithoutAClusterAreOwnStories(t *testing.T) {
	arts := []model.Article{
		art("x", "wire-a", "wire-a.example", "", 0, 0.5, 2, 0),
		art("y", "wire-a", "wire-a.example", "", 1, 0.5, 2, 0),
	}
	if n := len(Stories(arts, opt())); n != 2 {
		t.Errorf("%d stories, want 2", n)
	}
}

func TestStoriesAreOrderedByRelevanceThenBreadth(t *testing.T) {
	arts := []model.Article{
		art("low", "wire-a", "wire-a.example", "low", 0, 0.4, 5, 0),
		art("mid", "wire-a", "wire-a.example", "mid", 0, 0.8, 3, 0),
		art("top1", "wire-a", "wire-a.example", "top", 0, 0.8, 3, 0),
		art("top2", "wire-b", "wire-b.example", "top", 2, 0.8, 3, 0),
	}
	got := Stories(arts, opt())
	if got[0].ID != "top" || got[1].ID != "mid" || got[2].ID != "low" {
		t.Errorf("order = %s, %s, %s", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestSourcesMeasureEarlyRedundantAndAlone(t *testing.T) {
	arts := []model.Article{
		// s1: wire-a first by 10 and 20 minutes, wire-b follows.
		art("s1a", "wire-a", "wire-a.example", "s1", 0, 0.9, 3, 0),
		art("s1b", "wire-b", "wire-b.example", "s1", 10, 0.9, 3, 0),
		art("s2a", "wire-a", "wire-a.example", "s2", 0, 0.9, 3, 0),
		art("s2b", "wire-b", "wire-b.example", "s2", 20, 0.9, 3, 0),
		// s3: wire-b alone, low relevance.
		art("s3b", "wire-b", "wire-b.example", "s3", 0, 0.1, 1, 0),
		// s4: a filing nobody else carries.
		art("s4r", "reg", "reg.example", "s4", 0, 0.9, 5, 0),
	}
	verdicts := map[string]labels.Verdict{"s1a": labels.Keep, "s2a": labels.Keep, "s1b": labels.Keep, "s3b": labels.Drop}
	stats := map[string]SourceStat{}
	for _, s := range Sources(Stories(arts, opt()), 0.5, verdicts) {
		stats[s.SourceID] = s
	}
	a, b, r := stats["wire-a"], stats["wire-b"], stats["reg"]

	if a.First != 2 || a.Shared != 2 {
		t.Errorf("wire-a first %d of %d shared, want 2 of 2", a.First, a.Shared)
	}
	if rate, ok := a.Scoop(); !ok || rate != 1 {
		t.Errorf("wire-a scoop = %v, %v", rate, ok)
	}
	if b.First != 0 || b.Shared != 2 || b.Alone != 1 || b.Stories != 3 {
		t.Errorf("wire-b first %d shared %d alone %d stories %d, want 0, 2, 1, 3", b.First, b.Shared, b.Alone, b.Stories)
	}
	// Lags 10 and 20 minutes; the median of two takes the upper one.
	if b.MedianLag != 20*time.Minute || b.LagSamples != 2 {
		t.Errorf("wire-b median lag %v over %d, want 20m over 2", b.MedianLag, b.LagSamples)
	}
	if b.Relevant != 2 || b.Articles != 3 {
		t.Errorf("wire-b relevant %d of %d", b.Relevant, b.Articles)
	}
	if p, ok := b.Precision(); !ok || p != 0.5 {
		t.Errorf("wire-b precision = %v, %v, want 0.5 (1 kept, 1 dropped)", p, ok)
	}
	if _, ok := r.Precision(); ok {
		t.Error("reg has no verdicts, so no precision")
	}
	if _, ok := r.Scoop(); ok {
		t.Error("reg shared nothing, so no scoop rate")
	}
	if r.Backed != 1 || r.Alone != 1 {
		t.Errorf("reg backed %d alone %d, want 1 and 1", r.Backed, r.Alone)
	}
}

func TestSourcesFromOneOutletAreNeitherEarlyNorLate(t *testing.T) {
	// reg-a and reg-b are two feeds of one domain, reporting one story.
	tiers["reg-a"], tiers["reg-b"] = 1, 1
	arts := []model.Article{
		art("1", "reg-a", "reg.example", "s", 0, 0.9, 3, 0),
		art("2", "reg-b", "reg.example", "s", 5, 0.9, 3, 0),
		art("3", "wire-a", "wire-a.example", "s", 9, 0.9, 3, 0),
	}
	stats := map[string]SourceStat{}
	for _, s := range Sources(Stories(arts, Options{Tier: func(id string) model.Tier { return tiers[id] }}), 0.5, nil) {
		stats[s.SourceID] = s
	}
	if stats["reg-a"].First != 1 {
		t.Errorf("reg-a first = %d, want 1", stats["reg-a"].First)
	}
	if b := stats["reg-b"]; b.First != 0 || b.LagSamples != 0 {
		t.Errorf("reg-b first %d lag samples %d, want 0 and 0", b.First, b.LagSamples)
	}
	if w := stats["wire-a"]; w.LagSamples != 1 || w.MedianLag != 9*time.Minute {
		t.Errorf("wire-a lag %v over %d, want 9m over 1", w.MedianLag, w.LagSamples)
	}
}
