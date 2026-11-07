package episode

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/origin"
)

var t0 = time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)

func ev(id, title string, at time.Duration) Event {
	return Event{ID: id, Kind: "acquisition", Title: title, PublicAt: t0.Add(at), Articles: []string{"art-" + id}, Origins: map[string]origin.Origin{}}
}

func TestKindAndProximityOnlyMakeCandidates(t *testing.T) {
	a, b := ev("evt_a", "Acme to buy Bolt", 0), ev("evt_b", "Acme to buy Cog", 2*time.Hour)
	if !Candidate(a, b) {
		t.Fatal("same kind within 72 hours are candidates")
	}
	if d := Decide(a, b); d.Verdict == Same {
		t.Errorf("being candidates is not evidence of one episode: %+v", d)
	}
	far := ev("evt_c", "Acme to buy Bolt", 73*time.Hour)
	other := ev("evt_d", "Acme dividend", time.Hour)
	other.Kind = "dividend"
	if Candidate(a, far) || Candidate(a, other) {
		t.Error("outside the window or of another kind: not candidates")
	}
}

func TestTwoAcquisitionsWithinSeventyTwoHoursStayTwoEpisodes(t *testing.T) {
	a, b := ev("evt_a", "Acme to buy Bolt Industries", 0), ev("evt_b", "Acme agrees to acquire Cog Systems", 20*time.Hour)
	ds := Decisions([]Event{a, b})
	if len(ds) != 1 || ds[0].Verdict != Distinct || ds[0].Method != MethodTitleParties {
		t.Fatalf("%+v", ds)
	}
	if eps := Group([]Event{a, b}, ds); len(eps) != 2 {
		t.Errorf("%d episodes", len(eps))
	}
	// Different effective days or counterparties decide the same.
	a.EffectiveDay, b.EffectiveDay = "2026-12-01", "2027-01-15"
	if d := Decide(a, b); d.Method != MethodEffectiveDay || d.Verdict != Distinct {
		t.Errorf("%+v", d)
	}
	a.EffectiveDay, b.EffectiveDay = "", ""
	a.Counterparties, b.Counterparties = []string{"sec_bolt"}, []string{"sec_cog"}
	if d := Decide(a, b); d.Method != MethodCounterparties {
		t.Errorf("%+v", d)
	}
}

func TestAReleaseItsFilingAndDerivativeCoverageFormOneEpisode(t *testing.T) {
	release := ev("evt_rel", "Acme to acquire Bolt Industries", 0)
	release.Origins["art-evt_rel"] = origin.Origin{Kind: origin.KindIssuer, Key: "issuer:businesswire"}
	filing := ev("evt_fil", "8-K - Acme Corp", 3*time.Hour)
	filing.Articles = []string{"0001000001-26-000044"}
	filing.Filings = []Filing{{Accession: "0001000001-26-000044", Form: "8-K", Items: []string{"1.01"}}}
	coverage := ev("evt_cov", "Why Acme wants Bolt", 5*time.Hour)
	coverage.Origins["art-evt_cov"] = origin.Origin{Kind: origin.KindDerived, Key: "derived:primary:0001000001-26-000044", DerivedFrom: "primary:0001000001-26-000044"}
	events := []Event{release, filing, coverage}
	ds := Decisions(events)
	eps := Group(events, ds)
	if len(eps) != 1 || len(eps[0].Events) != 3 || !eps[0].Probable {
		t.Fatalf("one episode of three, joined partly on a probable link: %+v\n%+v", eps, ds)
	}
	for _, d := range ds {
		if d.Basis == "" || d.Method == "" {
			t.Errorf("every decision states its basis and method: %+v", d)
		}
	}
	// The report that rests on the filing is joined on proof, not on a guess.
	if d := Decide(filing, coverage); d.Verdict != Same || d.Method != MethodFilingRef || d.Probable {
		t.Errorf("%+v", d)
	}
}

func TestAmbiguousProbableLinksAreNotTaken(t *testing.T) {
	r1 := ev("evt_r1", "Acme to acquire Bolt Industries", 0)
	r1.Origins["art-evt_r1"] = origin.Origin{Kind: origin.KindIssuer, Key: "issuer:a"}
	r2 := ev("evt_r2", "Acme to acquire Bolt Industries", 4*time.Hour)
	r2.Origins["art-evt_r2"] = origin.Origin{Kind: origin.KindIssuer, Key: "issuer:b"}
	f := ev("evt_f", "8-K - Acme Corp", 2*time.Hour)
	f.Articles = []string{"acc-1"}
	f.Filings = []Filing{{Accession: "acc-1", Form: "8-K"}}
	ds := Decisions([]Event{r1, r2, f})
	for _, d := range ds {
		if d.Verdict == Same && d.Probable {
			t.Errorf("a filing that could belong to either of two releases belongs to neither: %+v", d)
		}
	}
	var amb int
	for _, d := range ds {
		if d.Method == MethodAmbiguous {
			amb++
			if !strings.Contains(d.Basis, "probable partners") {
				t.Errorf("%+v", d)
			}
		}
	}
	if amb != 2 {
		t.Errorf("%d ambiguous decisions in %+v", amb, ds)
	}
}

func TestSharedEvidenceIsProofAndOrderOfAGroupIsStable(t *testing.T) {
	a, b := ev("evt_a", "Acme to buy Bolt", 0), ev("evt_b", "Acme to buy Bolt", time.Hour)
	b.Articles = append(b.Articles, "art-evt_a")
	if d := Decide(b, a); d.Verdict != Same || d.Method != MethodSharedArticle || d.A != "evt_a" || d.B != "evt_b" {
		t.Errorf("%+v", d)
	}
	copyEv := ev("evt_c", "Acme to buy Bolt", 2*time.Hour)
	copyEv.Origins["art-evt_c"] = origin.Origin{Kind: origin.KindWire, Key: "wire:reuters", CopyOf: "art-evt_a"}
	if d := Decide(a, copyEv); d.Method != MethodCopy {
		t.Errorf("%+v", d)
	}
	eps := Group([]Event{b, a}, []Decision{Decide(a, b)})
	if len(eps) != 1 || eps[0].Events[0] != "evt_a" {
		t.Errorf("oldest first: %+v", eps)
	}
}

func TestADistinctDecisionInsideAJoinedSetIsKeptAsAConflict(t *testing.T) {
	a, b, c := ev("evt_a", "Acme to buy Bolt", 0), ev("evt_b", "Acme to buy Cog", time.Hour), ev("evt_c", "Acme to buy Bolt", 2*time.Hour)
	ds := []Decision{
		{A: "evt_a", B: "evt_b", Verdict: Distinct, Basis: "titles differ"},
		{A: "evt_a", B: "evt_c", Verdict: Same, Basis: "shared"},
		{A: "evt_b", B: "evt_c", Verdict: Same, Basis: "shared too"},
	}
	eps := Group([]Event{a, b, c}, ds)
	if len(eps) != 1 || !strings.Contains(strings.Join(eps[0].Basis, "\n"), "conflict kept") {
		t.Errorf("%+v", eps)
	}
}

func TestPartiesReadsTheCounterpartyOnlyWhenTheTitleNamesOne(t *testing.T) {
	if got := Parties("Acme to buy Bolt Industries"); len(got) != 1 || got[0] != "bolt" {
		t.Errorf("%v", got)
	}
	if got := Parties("Acme posts record earnings"); len(got) != 0 {
		t.Errorf("a missing party is not evidence: %v", got)
	}
}
