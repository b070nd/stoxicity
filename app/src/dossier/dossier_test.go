package dossier

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/episode"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/origin"
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
	md := EventMarkdown(history(), articles(), nil)
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
	if md != EventMarkdown(history(), articles(), nil) {
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

// art builds an evidence article together with the origin the rules give it.
func art(id, domain, title, text string, at time.Time, form string) (store.SignalEvidence, origin.Origin) {
	in := origin.Input{ArticleID: id, Domain: domain, Title: title, Summary: text, Form: form, Published: at}
	src := "paper"
	if form != "" {
		src = "sec-edgar"
	}
	return store.SignalEvidence{ArticleID: id, SourceID: src, Domain: domain, Form: form, Title: title, Published: at}, origin.Detect(in)
}

// sig builds an event of the security from articles, linking copies as the pipeline does.
func sig(id, kind, title string, at time.Time, mat int, arts ...func() (store.SignalEvidence, origin.Origin)) store.SignalEvent {
	e := store.SignalEvent{SecurityID: "sec_a", Ticker: "ACME", EventID: id, Kind: kind, Title: title, PublicAt: at, Materiality: mat, Origins: map[string]origin.Origin{}}
	var ins []origin.Input
	var os []origin.Origin
	for _, f := range arts {
		x, o := f()
		e.Evidence = append(e.Evidence, x)
		ins = append(ins, origin.Input{ArticleID: x.ArticleID, Domain: x.Domain, Title: x.Title, Summary: "", Published: x.Published, Form: x.Form})
		os = append(os, o)
	}
	for _, o := range origin.Link(ins, os) {
		e.Origins[o.ArticleID] = o
	}
	return e
}

func a(id, domain, title, text string, at time.Time, form string) func() (store.SignalEvidence, origin.Origin) {
	return func() (store.SignalEvidence, origin.Origin) { return art(id, domain, title, text, at, form) }
}

const reutersText = "NEW YORK (Reuters) - Acme Corp agreed on Monday to buy Bolt Industries for $4.2 billion in cash, the companies said, in a deal expected to close next year."
const bloombergText = "(Bloomberg) - Acme is in talks over a bid for Bolt, people familiar with the matter said, a person with knowledge of the discussions added."

func assess(events ...store.SignalEvent) Verdict {
	return Assess(events, episode.Decisions(SignalEpisodeEvents(events)), nil)
}

func TestAssessSeparatesPrimaryFromCorroboratedFromUnresolved(t *testing.T) {
	cases := []struct {
		name   string
		events []store.SignalEvent
		state  State
	}{
		{"nothing", nil, StateNone},
		{"one filing is primary evidence, not corroboration", []store.SignalEvent{sig("e1", "results", "Form 8-K - Acme", t0, 3, a("0001-26-1", "sec.gov", "8-K - Acme", "", t0, "8-K"))}, StatePrimary},
		{"a filing and articles quoting it are one origin", []store.SignalEvent{sig("e1", "results", "Acme results", t0, 3,
			a("0001-26-1", "sec.gov", "8-K - Acme", "", t0, "8-K"),
			a("q1", "a.example", "Acme results", "Acme said in a filing with the Securities and Exchange Commission that revenue rose", t0.Add(time.Hour), ""),
			a("q2", "b.example", "Acme revenue rises", "Details in 0001-26-1 show revenue rose", t0.Add(2*time.Hour), ""))}, StatePrimary},
		{"a wire story republished on three sites is one origin", []store.SignalEvent{sig("e1", "acquisition", "Acme to buy Bolt", t0, 5,
			a("w1", "a.example", "Acme to buy Bolt", reutersText, t0, ""), a("w2", "b.example", "Acme to buy Bolt", reutersText, t0.Add(time.Hour), ""), a("w3", "c.example", "Acme to buy Bolt", reutersText, t0.Add(2*time.Hour), ""))}, StateUnresolved},
		{"two agencies reporting on their own are corroboration", []store.SignalEvent{sig("e1", "acquisition", "Acme to buy Bolt", t0, 1,
			a("w1", "a.example", "Acme to buy Bolt", reutersText, t0, ""), a("b1", "b.example", "Acme bid for Bolt", bloombergText, t0.Add(time.Hour), ""))}, StateCorroborated},
		{"a filing plus independent follow-up reporting", []store.SignalEvent{sig("e1", "acquisition", "Acme to buy Bolt", t0, 5,
			a("0001-26-2", "sec.gov", "8-K - Acme", "", t0, "8-K"), a("w1", "a.example", "Acme to buy Bolt", reutersText, t0.Add(3*time.Hour), ""))}, StateCorroborated},
		{"two sites of unknown origin: independence is not established", []store.SignalEvent{sig("e1", "acquisition", "Acme to buy Bolt", t0, 5,
			a("u1", "a.example", "Acme to buy Bolt", "Acme Corp agreed to buy Bolt", t0, ""), a("u2", "b.example", "Acme buys Bolt", "Bolt is to be bought by Acme", t0.Add(time.Hour), ""))}, StateUnresolved},
	}
	for _, c := range cases {
		v := assess(c.events...)
		if v.State != c.state || v.Corroborated != (c.state == StateCorroborated) {
			t.Errorf("%s: state %s (%s)", c.name, v.State, v.Reason)
		}
	}
}

func TestMaterialityNeverChangesTheVerdict(t *testing.T) {
	for _, mat := range []int{1, 3, 5} {
		e := sig("e1", "acquisition", "Acme to buy Bolt", t0, mat, a("w1", "a.example", "Acme to buy Bolt", reutersText, t0, ""), a("b1", "b.example", "Acme bid for Bolt", bloombergText, t0.Add(time.Hour), ""))
		if v := assess(e); v.State != StateCorroborated || v.PeakShare != 1 {
			t.Errorf("materiality %d: %s %.2f", mat, v.State, v.PeakShare)
		}
	}
}

func TestReleaseItsFilingAndCoverageAreOneEpisodeButTwoAcquisitionsAreTwo(t *testing.T) {
	release := sig("e1", "acquisition", "Acme to acquire Bolt Industries", t0, 4,
		a("r1", "businesswire.com", "Acme to acquire Bolt Industries", "(BUSINESS WIRE) Acme today announced an agreement to acquire Bolt Industries", t0, ""))
	filing := sig("e2", "acquisition", "8-K - Acme Corp", t0.Add(2*time.Hour), 4, a("0001-26-3", "sec.gov", "8-K - Acme Corp", "", t0.Add(2*time.Hour), "8-K"))
	coverage := sig("e3", "acquisition", "Acme buys Bolt", t0.Add(5*time.Hour), 4,
		a("c1", "paper.example", "Acme buys Bolt", "Acme will buy Bolt, according to a press release", t0.Add(5*time.Hour), ""))
	v := assess(release, filing, coverage)
	if v.Episodes != 2 && v.Episodes != 1 {
		t.Fatalf("episodes %d", v.Episodes)
	}
	// The release and the filing are one episode (a probable link); the coverage names the same party and rests on a statement.
	if v.Episodes != 1 {
		t.Errorf("release, filing and coverage should form one episode, got %d: %+v", v.Episodes, v.Details)
	}
	if !v.Details[0].Episode.Probable {
		t.Error("the join rests on a probable link and must say so")
	}

	other := sig("e4", "acquisition", "Acme to acquire Cog Industries", t0.Add(30*time.Hour), 4,
		a("r2", "businesswire.com", "Acme to acquire Cog Industries", "(BUSINESS WIRE) Acme today announced an agreement to acquire Cog Industries", t0.Add(30*time.Hour), ""))
	for _, d := range episode.Decisions(SignalEpisodeEvents([]store.SignalEvent{release, other})) {
		if d.Verdict != episode.Distinct {
			t.Errorf("two acquisitions of different parties within 72h must stay distinct: %+v", d)
		}
	}
	// The 8-K could belong to either release: that is ambiguous, so it joins neither.
	v = assess(release, filing, other)
	if v.Episodes != 3 {
		t.Errorf("two releases and an 8-K that fits both: three episodes, got %d", v.Episodes)
	}
	for _, d := range episode.Decisions(SignalEpisodeEvents([]store.SignalEvent{release, filing, other})) {
		if d.Method == episode.MethodFilingRelease {
			t.Errorf("an ambiguous probable link must not be taken: %+v", d)
		}
	}
}

func TestConcentrationCountsReportsAfterDeduplication(t *testing.T) {
	// Episode one: a wire story on four sites (one report) plus the filing (a second); episode two: one report.
	big := sig("e1", "results", "Acme results", t0, 3,
		a("0001-26-4", "sec.gov", "8-K - Acme", "", t0, "8-K"),
		a("w1", "a.example", "Acme results", reutersText, t0, ""), a("w2", "b.example", "Acme results", reutersText, t0.Add(time.Hour), ""),
		a("w3", "c.example", "Acme results", reutersText, t0.Add(2*time.Hour), ""), a("w4", "d.example", "Acme results", reutersText, t0.Add(3*time.Hour), ""))
	small := sig("e2", "buyback", "Acme buyback", t0.Add(24*time.Hour), 3, a("b1", "b.example", "Acme buyback", bloombergText, t0.Add(24*time.Hour), ""))
	v := assess(big, small)
	if v.TotalReports != 3 || v.PeakReports != 2 {
		t.Errorf("copies count once: %d of %d", v.PeakReports, v.TotalReports)
	}
	if v.SinglePeak {
		t.Errorf("2 of 3 reports (%.0f%%) is below the %.0f%% line", v.PeakShare*100, SinglePeakShare*100)
	}
}

func TestBuildGraphLinksEventsEvidenceAndPublishers(t *testing.T) {
	sec := store.SecurityInfo{ID: "sec_a", Ticker: "ACME"}
	tl := []store.TimelineEntry{{EventID: "evt_1"}}
	g, v := BuildGraph(sec, tl, map[string][]model.EventRevision{"evt_1": history()}, articles(), nil, nil)
	// Two sites, neither says where its report came from: independence is not established.
	if v.Corroborated || v.State != StateUnresolved || v.Publishers != 2 || v.Events != 1 {
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
