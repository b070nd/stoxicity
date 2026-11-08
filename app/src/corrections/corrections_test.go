package corrections

import (
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

func pin() EventPin {
	return EventPin{EventID: "evt_1", Revision: 1, Kind: "results", Role: "subject", Status: model.StatusReported, Title: "Profit rises", Fingerprint: "fp1", Core: "c1",
		Claims: []string{"a1#0@v1/x"}, Securities: []string{"sec_a:subject"}, Articles: []ArticlePin{{ID: "a1", Head: "h1"}}}
}

func state() State {
	return State{Revision: 1, Status: model.StatusReported, Title: "Profit rises", Fingerprint: "fp1", Core: "c1",
		Claims: []string{"a1#0@v1/x"}, Securities: []string{"sec_a:subject"}, Heads: map[string]string{"a1": "h1"}}
}

func TestMoreEvidenceIsNotACorrection(t *testing.T) {
	c := state()
	c.Revision = 2
	c.Fingerprint = "fp2"
	c.Claims = append(c.Claims, "a2#0@v1/x")
	c.Securities = append(c.Securities, "sec_b:counterparty")
	c.Heads["a2"] = "h2"
	found, ignored := Compare([]EventPin{pin()}, map[string]State{"evt_1": c})
	if len(found) != 0 {
		t.Fatalf("a second article and a counterparty are more evidence, not a correction: %+v", found)
	}
	if len(ignored) != 1 || !strings.Contains(ignored[0], "revision 2") {
		t.Errorf("the update was seen and said to be unimportant: %v", ignored)
	}
}

func TestACorrectionARetractionAndAChangedSourceAreTold(t *testing.T) {
	corrected := state()
	corrected.Revision, corrected.Status, corrected.Core, corrected.Title = 2, model.StatusCorrected, "c2", "Profit rises less"
	corrected.Claims = []string{"a1#0@v2/x"}
	found, _ := Compare([]EventPin{pin()}, map[string]State{"evt_1": corrected})
	if len(found) != 1 || found[0].Kind != "corrected" || found[0].Effect != Revise || !strings.Contains(found[0].Detail, "was corrected") {
		t.Fatalf("a correction: %+v", found)
	}

	gone := state()
	gone.Status = model.StatusWithdrawn
	found, _ = Compare([]EventPin{pin()}, map[string]State{"evt_1": gone})
	if len(found) != 1 || found[0].Kind != "retracted" || found[0].Effect != Revise {
		t.Fatalf("a withdrawal: %+v", found)
	}

	moved := state()
	moved.Heads["a1"] = "h9"
	found, _ = Compare([]EventPin{pin()}, map[string]State{"evt_1": moved})
	if len(found) != 1 || found[0].Kind != "source_changed" || found[0].Effect != Review {
		t.Fatalf("an article changed at its source before the event was derived again is ambiguous: %+v", found)
	}

	found, _ = Compare([]EventPin{pin()}, map[string]State{})
	if len(found) != 1 || found[0].Effect != Review {
		t.Fatalf("an event that cannot be read is not assumed to stand: %+v", found)
	}
	if Strongest(found) != Review {
		t.Errorf("strongest = %v", Strongest(found))
	}
}

func TestTheSameChangeHasTheSameIdentity(t *testing.T) {
	a := []Finding{{Kind: "corrected", Ref: "evt_1 r1", To: "x"}, {Kind: "figure_revised", Ref: "scorecard", To: "y"}}
	b := []Finding{a[1], a[0]}
	if ChangeID("pl_1", a) != ChangeID("pl_1", b) {
		t.Error("the order the findings were found in does not make a different change")
	}
	if ChangeID("pl_1", a) == ChangeID("pl_2", a) {
		t.Error("another plan is another change")
	}
	if ChangeID("pl_1", a) == ChangeID("pl_1", a[:1]) {
		t.Error("a different set of findings is a different change")
	}
}

func TestAThesisIsBrokenOnlyByARecordedRuleAndOnlyByNewMatches(t *testing.T) {
	r := Rule{ID: "going-concern", Security: "sec_a", Kinds: []string{"bankruptcy", "non_reliance"}, Effect: Broken, Note: "the business case rests on solvency", By: "me", Version: 2}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	ev := EventPin{EventID: "evt_9", Revision: 1, Kind: "bankruptcy", Role: "subject"}
	mentioned := EventPin{EventID: "evt_8", Revision: 1, Kind: "bankruptcy", Role: "mentioned"}
	other := EventPin{EventID: "evt_7", Revision: 1, Kind: "results", Role: "subject"}
	if got := r.Matches([]EventPin{ev, mentioned, other}); len(got) != 1 || got[0] != "evt_9@1" {
		t.Fatalf("only a subject event of a listed kind matches by default: %v", got)
	}
	d := Deps{RuleHits: map[string][]string{}}
	fs := NewRuleHits([]Rule{r}, []EventPin{ev}, d)
	if len(fs) != 1 || fs[0].Effect != Broken || !strings.Contains(fs[0].Detail, "is broken") {
		t.Fatalf("a new match: %+v", fs)
	}
	d.RuleHits["going-concern"] = []string{"evt_9@1"}
	if fs := NewRuleHits([]Rule{r}, []EventPin{ev}, d); len(fs) != 0 {
		t.Errorf("a match that was there when the plan was made is not news: %+v", fs)
	}
	r.Retired = true
	d.RuleHits = map[string][]string{}
	if fs := NewRuleHits([]Rule{r}, []EventPin{ev}, d); len(fs) != 0 {
		t.Errorf("a retired rule matches nothing: %+v", fs)
	}
	bad := Rule{ID: "x y", Effect: "sell"}
	err := bad.Validate()
	for _, w := range []string{"without spaces", "security", "event kind", "broken or review", "why the thesis fails", "who recorded"} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("a rule that says nothing is refused (%q): %v", w, err)
		}
	}
}

func TestHashIgnoresAdditionsAndSeesCorrections(t *testing.T) {
	p := pin()
	base := Hash([]EventPin{p})
	if base == "" || Hash(nil) != "" {
		t.Fatalf("hash %q", base)
	}
	supp := Supporting([]EventPin{p, {EventID: "evt_2", Role: "mentioned"}})
	if len(supp) != 1 {
		t.Errorf("an event that only mentions the security is not a dependency: %+v", supp)
	}
	q := p
	q.Fingerprint = "changed@fp2"
	if Hash([]EventPin{q}) == base {
		t.Error("a corrected event gives another hash")
	}
}
