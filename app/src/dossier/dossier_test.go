package dossier

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

var t0 = time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)

func history() []model.EventRevision {
	subj := []model.EventSecurity{{SecurityID: "sec_a", Ticker: "ACME", Role: model.RoleSubject}, {SecurityID: "sec_b", Ticker: "BOLT", Role: model.RoleCounterparty}}
	return []model.EventRevision{
		{EventID: "evt_1", Revision: 1, Kind: "merger", Status: model.StatusReported, Title: "Acme | to buy Bolt", PublicAt: t0, RecordedAt: t0.Add(time.Hour), Materiality: 5, Relevance: 0.9,
			LensVersion: "lens-1", Securities: subj, Evidence: []model.Evidence{{ArticleID: "a1", SourceID: "wire"}}},
		{EventID: "evt_1", Revision: 2, Kind: "merger", Status: model.StatusCorrected, Title: "Acme to buy Bolt for cash", PublicAt: t0, RecordedAt: t0.Add(3 * time.Hour), Materiality: 5, Relevance: 0.9,
			LensVersion: "lens-1", Supersedes: 1, Securities: subj, Topics: []string{"m&a"},
			Evidence:    []model.Evidence{{ArticleID: "a1", SourceID: "wire"}, {ArticleID: "a2", SourceID: "paper"}, {ArticleID: "gone", SourceID: "paper"}},
			Assessments: []model.Assessment{{SecurityID: "sec_b", Rule: "lens-1", Effect: "materiality", Direction: model.DirectionUnknown, Horizon: "unspecified", Confidence: 0.5, Rationale: "deal terms"}}},
	}
}

func articles() map[string]model.Article {
	return map[string]model.Article{
		"a1": {ID: "a1", Domain: "wire.example", Title: "Acme to buy Bolt", URL: "https://wire.example/1", Published: t0},
		"a2": {ID: "a2", Domain: "paper.example", Title: "Deal for Bolt", Published: t0.Add(time.Hour)},
	}
}

func TestEventMarkdownSeparatesObservationFromAssessmentAndKeepsEveryRevision(t *testing.T) {
	md := EventMarkdown(history(), articles())
	for _, want := range []string{
		"# Acme to buy Bolt for cash", "revision 2 of 2", "| subject | ACME | [sec_a](../securities/sec_a.md) |",
		"[Acme to buy Bolt](https://wire.example/1), wire.example (wire)", "`gone` (paper), article not stored",
		"3 observations from 2 publishers", "## Assessments (interpretation, not observation)", "deal terms",
		"| 1 | reported |", "| 2 | corrected |", "Acme \\| to buy Bolt",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
	if md != EventMarkdown(history(), articles()) {
		t.Error("the same history must give the same text")
	}
}

func TestSecurityMarkdownLinksItsEvents(t *testing.T) {
	md := SecurityMarkdown(store.SecurityInfo{ID: "sec_a", Ticker: "ACME", Name: "Acme Corp", CIK: "0001000001"},
		[]string{"last close 11.50 on 2026-11-02"},
		[]store.TimelineEntry{{EventID: "evt_1", Kind: "merger", Status: "corrected", Title: "Acme to buy Bolt", Role: model.RoleSubject, PublicAt: t0, Materiality: 5, Revision: 2, Evidence: 3}})
	for _, want := range []string{"# ACME - Acme Corp", "SEC CIK: 0001000001", "last close 11.50", "[Acme to buy Bolt](../events/evt_1.md)"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
}

func ev(id, kind string, at time.Time, mat int, domains []string, sources ...string) store.SignalEvent {
	return store.SignalEvent{SecurityID: "sec_a", EventID: id, Kind: kind, PublicAt: at, Materiality: mat, Domains: domains, Sources: sources}
}

func TestAssessCorroboratesOnlyByIndependentEvidenceAndNeverByMateriality(t *testing.T) {
	primary := func(s string) bool { return s == "sec-edgar" }
	cases := []struct {
		name   string
		events []store.SignalEvent
		basis  string
		corr   bool
		peak   bool
	}{
		{"nothing", nil, BasisNone, false, false},
		{"one publisher however material", []store.SignalEvent{ev("1", "merger", t0, 5, []string{"a.example"}, "wire")}, BasisSinglePublisher, false, true},
		{"two publishers", []store.SignalEvent{ev("1", "merger", t0, 3, []string{"a.example", "b.example"}, "wire", "paper")}, BasisPublishers, true, true},
		{"a filing and the stories quoting it: one origin", []store.SignalEvent{ev("1", "earnings", t0, 3, []string{"sec.gov", "a.example", "b.example"}, "sec-edgar", "wire", "paper")}, BasisPrimary, true, true},
		{"the release, its 8-K and the follow-up are one episode", []store.SignalEvent{
			ev("1", "earnings", t0, 4, []string{"a.example"}, "wire"), ev("2", "earnings", t0.Add(2*time.Hour), 4, []string{"sec.gov"}, "sec-edgar"),
			ev("3", "earnings", t0.Add(20*time.Hour), 3, []string{"b.example"}, "paper")}, BasisPrimary, true, true},
		{"two separate episodes of different kinds", []store.SignalEvent{
			ev("1", "earnings", t0, 4, []string{"a.example", "b.example"}, "wire", "paper"), ev("2", "buyback", t0.Add(24*time.Hour), 4, []string{"a.example"}, "wire")}, BasisPublishers, true, false},
	}
	for _, c := range cases {
		v := Assess(c.events, primary)
		if v.Basis != c.basis || v.Corroborated != c.corr || v.SinglePeak != c.peak {
			t.Errorf("%s: basis %q corroborated %v peak %v (%s)", c.name, v.Basis, v.Corroborated, v.SinglePeak, v.Reason)
		}
	}
	if v := Assess([]store.SignalEvent{
		ev("1", "earnings", t0, 4, nil, "wire"), ev("2", "earnings", t0.Add(2*time.Hour), 4, nil, "sec-edgar"), ev("3", "earnings", t0.Add(20*time.Hour), 3, nil, "paper")}, primary); v.Events != 3 || v.Episodes != 1 {
		t.Errorf("three events, one episode: %+v", v)
	}
	if v := Assess(nil, nil); v.Caveat == "" {
		t.Error("the caveat is always stated")
	}
}

func TestBuildGraphLinksEventsEvidenceAndPublishers(t *testing.T) {
	sec := store.SecurityInfo{ID: "sec_a", Ticker: "ACME"}
	tl := []store.TimelineEntry{{EventID: "evt_1"}}
	g, v := BuildGraph(sec, tl, map[string][]model.EventRevision{"evt_1": history()}, articles(), nil)
	if !v.Corroborated || v.Publishers != 2 || v.Events != 1 {
		t.Errorf("verdict %+v", v)
	}
	kinds := map[string]int{}
	for _, n := range g.Nodes {
		kinds[n.Kind]++
	}
	if kinds[NodeSecurity] != 2 || kinds[NodeEvent] != 1 || kinds[NodeTopic] != 1 || kinds[NodePublisher] != 2 || kinds[NodeArticle] != 3 {
		t.Errorf("nodes %v", kinds)
	}
	text := g.Text()
	for _, want := range []string{"ACME", "merger: Acme to buy Bolt for cash [subject]", "counterparty: BOLT", "evidence: wire.example (1)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if !strings.Contains(g.DOT(), `"evt_1" -> "sec_a" [label="subject"]`) || !strings.Contains(g.JSON(), `"focus": "sec_a"`) {
		t.Error("dot or json")
	}
}
