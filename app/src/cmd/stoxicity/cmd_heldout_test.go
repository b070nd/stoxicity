package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/heldout"
)

// chk checks one run: its exit code and, when given, text it printed.
func chk(t *testing.T, r result, code int, outs ...string) {
	t.Helper()
	r.wantCode(t, code)
	if len(outs) > 0 {
		r.wantOut(t, outs...)
	}
}

func heldCorpusJSON(t *testing.T) string {
	t.Helper()
	item := func(id, title, summary string, tickers ...string) heldout.Item {
		return heldout.Item{ID: id, SourceID: "wire", URL: "https://example.com/" + id, Licence: heldout.Redistributable, Published: "2026-11-03", Title: title, Summary: summary,
			Hash: heldout.ContentHash(title, summary), Tickers: tickers, Language: "en"}
	}
	c := heldout.Corpus{
		Name:      "fixture corpus",
		Names:     map[string][]string{"ACME": {"Acme Corp"}, "BOLT": {"Bolt Industries"}},
		Events:    []heldout.EventDef{{Key: "acme-bolt", Description: "the purchase"}},
		Labellers: []heldout.Labeller{{ID: "ann", Independent: true}, {ID: "bea", Independent: true}, {ID: "cy", Independent: true}},
		Items: []heldout.Item{
			item("i1", "Acme Corp completed its acquisition of Bolt Industries", "", "ACME", "BOLT"),
			item("i2", "Weather was fine", "Nothing happened.", "ACME"),
		},
	}
	b, _ := json.Marshal(c)
	return string(b)
}

func heldLabelsJSON(who ...string) string {
	acq := heldout.ItemLabel{Event: "acme-bolt", Claims: []heldout.ClaimLabel{{Kind: "acquisition", Status: "completed", Parties: []heldout.PartyLabel{{Ticker: "ACME", Role: "subject"}, {Ticker: "BOLT", Role: "counterparty"}}}}}
	none := heldout.ItemLabel{NoClaims: true}
	var rs []heldout.Record
	for _, w := range who {
		rs = append(rs, heldout.Record{Labeller: w, Item: "i1", Version: 1, Label: acq}, heldout.Record{Labeller: w, Item: "i2", Version: 1, Label: none})
	}
	b, _ := json.Marshal(rs)
	return string(b)
}

func TestHeldoutNoExternalLabelsIsNotAPassAndFreezingLocksEverything(t *testing.T) {
	e := newTestEnv(t)
	chk(t, e.run(t, "heldout", "example"), exitOK, "REPLACE", "redistributable", "supersedes")

	// an unlicensed corpus, or text kept under an internal licence, is refused
	bad := strings.Replace(heldCorpusJSON(t), `"licence":"redistributable"`, `"licence":"unknown"`, 1)
	chk(t, e.run(t, "heldout", "corpus", "import", "-file", writeFile(t, e, "bad.json", bad)), exitError)
	internal := strings.Replace(heldCorpusJSON(t), `"licence":"redistributable"`, `"licence":"internal"`, 1)
	r := e.run(t, "heldout", "corpus", "import", "-file", writeFile(t, e, "bad2.json", internal))
	r.wantCode(t, exitError)

	chk(t, e.run(t, "heldout", "corpus", "import", "-file", writeFile(t, e, "corpus.json", heldCorpusJSON(t))), exitOK, "2 items", "3 labellers", "not evidence")

	// no labels, no thresholds: nothing passes
	r = e.run(t, "heldout", "evaluate")
	r.wantCode(t, exitOK)
	r.wantOut(t, "NO_EXTERNAL_LABELS", "a missing external label is not a pass", "NOT evidence")

	// a label from someone outside the corpus is refused
	chk(t, e.run(t, "heldout", "label", "-file", writeFile(t, e, "l0.json", heldLabelsJSON("zed"))), exitError)
	chk(t, e.run(t, "heldout", "label", "-file", writeFile(t, e, "l1.json", heldLabelsJSON("ann", "bea"))), exitOK, "4 label version")

	// a revision must say what it supersedes and why
	rev := `[{"labeller":"ann","item":"i2","version":2,"label":{"no_claims":true}}]`
	chk(t, e.run(t, "heldout", "label", "-file", writeFile(t, e, "rev.json", rev)), exitError)
	rev = `[{"labeller":"ann","item":"i2","version":2,"supersedes":1,"reason":"re-read the summary","label":{"no_claims":true}}]`
	chk(t, e.run(t, "heldout", "label", "-file", writeFile(t, e, "rev2.json", rev)), exitOK)

	r = e.run(t, "heldout", "evaluate")
	r.wantOut(t, "THRESHOLDS_NOT_STATED", "2 independently labelled", "1 superseded")

	th := `{"min_annotators":2,"min_items":2,"min_precision":0.1,"min_recall":0.1,"min_group":1}`
	chk(t, e.run(t, "heldout", "thresholds", "-file", writeFile(t, e, "th.json", th)), exitOK)
	chk(t, e.run(t, "heldout", "evaluate"), exitOK, "NOT_FROZEN")
	chk(t, e.run(t, "heldout", "disagreements"), exitOK, "no disagreement")

	chk(t, e.run(t, "heldout", "freeze"), exitOK, "frozen")
	chk(t, e.run(t, "heldout", "freeze"), exitError)
	// everything is locked after the freeze
	chk(t, e.run(t, "heldout", "label", "-file", writeFile(t, e, "l2.json", heldLabelsJSON("cy"))), exitError)
	chk(t, e.run(t, "heldout", "thresholds", "-file", writeFile(t, e, "th2.json", th)), exitError)
	chk(t, e.run(t, "heldout", "adjudicate", "-item", "i1", "-stage", "polarity", "-by", "cy", "-tokens", "x", "-rationale", "y"), exitError)

	r = e.run(t, "heldout", "evaluate")
	r.wantCode(t, exitOK)
	if strings.Contains(r.out, "NOT_FROZEN") || strings.Contains(r.out, "CHANGED_AFTER_FREEZE") || strings.Contains(r.out, "NO_EXTERNAL") {
		t.Fatalf("a frozen, labelled corpus:\n%s", r.out)
	}
	r.wantOut(t, "extraction", "parties", "abstention", "identity", "synthetic regressions")
	r = e.run(t, "heldout", "evaluate", "-json")
	r.wantOut(t, `"independent"`, `"synthetic_regressions"`, `"gate"`)
}

func TestHeldoutDisagreementsAreSettledOnlyByAThirdIndependentPerson(t *testing.T) {
	e := newTestEnv(t)
	chk(t, e.run(t, "heldout", "corpus", "import", "-file", writeFile(t, e, "corpus.json", heldCorpusJSON(t))), exitOK)
	chk(t, e.run(t, "heldout", "label", "-file", writeFile(t, e, "l1.json", heldLabelsJSON("ann"))), exitOK)
	// bea reads the acquisition as announced rather than completed
	other := strings.Replace(heldLabelsJSON("bea"), `"completed"`, `"announced"`, 1)
	chk(t, e.run(t, "heldout", "label", "-file", writeFile(t, e, "l2.json", other)), exitOK)

	r := e.run(t, "heldout", "disagreements")
	r.wantOut(t, "i1", "temporal", "open", "ann:", "bea:")
	// ann cannot settle their own disagreement
	chk(t, e.run(t, "heldout", "adjudicate", "-item", "i1", "-stage", "temporal", "-by", "ann", "-tokens", "acquisition|completed|hist=0", "-rationale", "it closed"), exitOK)
	chk(t, e.run(t, "heldout", "disagreements"), exitOK, "not accepted", "cannot settle their own")
	chk(t, e.run(t, "heldout", "adjudicate", "-item", "i1", "-stage", "temporal", "-by", "cy", "-tokens", "acquisition|completed|hist=0", "-rationale", "the filing says it closed"), exitOK)
	chk(t, e.run(t, "heldout", "disagreements"), exitOK, "settled", "the filing says it closed")
	chk(t, e.run(t, "heldout", "adjudicate", "-item", "i1", "-stage", "bogus", "-by", "cy", "-tokens", "x", "-rationale", "y"), exitUsage)
}
