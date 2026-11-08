package heldout

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func item(id, title, summary string, tickers ...string) Item {
	return Item{ID: id, SourceID: "wire", URL: "https://example.com/" + id, Licence: Redistributable, Published: "2026-11-03", Title: title, Summary: summary, Hash: ContentHash(title, summary), Tickers: tickers, Language: "en"}
}

func corpus() Corpus {
	return Corpus{
		Name:      "fixture",
		Names:     map[string][]string{"ACME": {"Acme Corp"}, "BOLT": {"Bolt Industries"}},
		Events:    []EventDef{{Key: "acme-bolt", Description: "Acme completes its purchase of Bolt"}, {Key: "acme-dividend", Description: "Acme raises its dividend"}},
		Labellers: []Labeller{{ID: "ann", Independent: true}, {ID: "bea", Independent: true}, {ID: "cy", Independent: true}, {ID: "dev", Independent: false}},
		Items: []Item{
			item("i1", "Acme Corp completed its acquisition of Bolt Industries", "", "ACME", "BOLT"),
			item("i2", "Acme Corp completed its acquisition of Bolt Industries", "", "ACME", "BOLT"),
			item("i3", "Acme Corp raises dividend", "Acme Corp raised its quarterly dividend by 5%.", "ACME"),
			item("i4", "Weather was fine", "Nothing happened.", "ACME"),
		},
	}
}

var acquisition = ClaimLabel{Kind: "acquisition", Status: "completed", Parties: []PartyLabel{{Ticker: "ACME", Role: "subject"}, {Ticker: "BOLT", Role: "counterparty"}}}

func rec(who, it string, l ItemLabel) Record {
	return Record{Labeller: who, Item: it, Version: 1, Label: l}
}

// labels has two independent annotators agree on everything.
func labels() []Record {
	a := ItemLabel{Claims: []ClaimLabel{acquisition}, Event: "acme-bolt"}
	div := ItemLabel{Claims: []ClaimLabel{{Kind: "dividend", Parties: []PartyLabel{{Ticker: "ACME", Role: "subject"}}, Action: "raised"}}, Event: "acme-dividend"}
	none := ItemLabel{NoClaims: true}
	var rs []Record
	for _, who := range []string{"ann", "bea"} {
		rs = append(rs, rec(who, "i1", a), rec(who, "i2", a), rec(who, "i3", div), rec(who, "i4", none))
	}
	return rs
}

func thresholds() Thresholds {
	return Thresholds{MinAnnotators: 2, MinItems: 3, MinPrecision: 0.1, MinRecall: 0.1, MinGroup: 2}
}

func run(c Corpus, rs []Record, adj []Adjudication, t Thresholds, freeze bool) Report {
	r := Run{Corpus: c, Records: rs, Adjudicated: adj, Thresholds: t}
	if freeze {
		r.Frozen = FreezeHash(c, slices.Clone(rs), slices.Clone(adj), t)
	}
	r.Predictions, r.Skipped = Predict(c, nil)
	return Evaluate(r)
}

func stage(rep Report, name string, synthetic bool) StageResult {
	src := rep.Independent
	if synthetic {
		src = rep.Synthetic
	}
	for _, s := range src {
		if s.Stage == name {
			return s
		}
	}
	return StageResult{}
}

func TestNoExternalLabelsIsNeverAPass(t *testing.T) {
	rep := run(corpus(), nil, nil, thresholds(), true)
	if rep.Gate != NoLabels || rep.Labelled != 0 {
		t.Fatalf("%+v", rep.Gate)
	}
	// labels by someone who is not attested independent are not evidence either
	c := corpus()
	c.Labellers = []Labeller{{ID: "ann"}, {ID: "bea"}}
	rep = run(c, labels(), nil, thresholds(), true)
	if rep.Gate != NoLabels {
		t.Fatalf("labels by people not attested independent counted: %v", rep.Gate)
	}
	// and a single independent annotator is not enough to see a disagreement
	one := slices.DeleteFunc(labels(), func(r Record) bool { return r.Labeller == "bea" })
	if rep = run(corpus(), one, nil, thresholds(), true); rep.Gate != NoLabels {
		t.Fatalf("%v", rep.Gate)
	}
}

func TestIndependentLabelsAreScoredAgainstTheProgramsOwnReading(t *testing.T) {
	rep := run(corpus(), labels(), nil, thresholds(), true)
	if rep.Labelled != 4 {
		t.Fatalf("%d", rep.Labelled)
	}
	ex := stage(rep, Extraction, false)
	if ex.All.Items != 4 || ex.All.TP < 3 {
		t.Fatalf("the program reads acquisitions and dividends: %+v", ex.All)
	}
	if ab := stage(rep, Abstention, false); ab.All.TP != 1 {
		t.Fatalf("an article with no claim is read as none: %+v", ab.All)
	}
	// two copies of one story are one event; the dividend is another
	id := stage(rep, Identity, false)
	if id.All.Items != 3 || id.All.TP != 1 || id.All.FP != 0 || id.All.FN != 0 {
		t.Fatalf("pairs: %+v", id.All)
	}
	if _, ok := stage(rep, Extraction, false).Groups["kind:acquisition"]; !ok {
		t.Fatal("results are grouped by event kind")
	}
	if rep.Gate == NoLabels || rep.Gate == NotFrozen || rep.Gate == Changed || rep.Gate == NoThresholds {
		t.Fatalf("%v %v", rep.Gate, rep.GateWhy)
	}
}

func TestDisagreementsAreRecordedAndOnlyAThirdIndependentPersonSettlesThem(t *testing.T) {
	rs := labels()
	// bea reads the dividend as not raised
	for i := range rs {
		if rs[i].Labeller == "bea" && rs[i].Item == "i3" {
			rs[i].Label.Claims = []ClaimLabel{{Kind: "dividend", Parties: []PartyLabel{{Ticker: "ACME", Role: "subject"}}, Action: "cut"}}
		}
	}
	rep := run(corpus(), rs, nil, thresholds(), true)
	if rep.Open != 1 || rep.Disagreements[0].Item != "i3" || rep.Disagreements[0].Stage != Polarity {
		t.Fatalf("%+v", rep.Disagreements)
	}
	if got := stage(rep, Polarity, false).All; got.Contested != 1 || got.Items != 3 {
		t.Fatalf("the contested item is left out and counted: %+v", got)
	}
	if stage(rep, Polarity, false).Agreement.Value >= 1 {
		t.Fatal("agreement is reported")
	}

	for _, tc := range []struct {
		adj  Adjudication
		want string
	}{
		{Adjudication{Item: "i3", Stage: Polarity, By: "ann", Tokens: []string{"dividend|neg=0|den=0|act=raised"}, Rationale: "x"}, "cannot settle their own"},
		{Adjudication{Item: "i3", Stage: Polarity, By: "dev", Tokens: []string{"x"}, Rationale: "x"}, "not attested independent"},
		{Adjudication{Item: "i3", Stage: Polarity, By: "cy", Tokens: []string{"x"}}, "needs a rationale"},
	} {
		rep = run(corpus(), rs, []Adjudication{tc.adj}, thresholds(), true)
		if rep.Open != 1 || !strings.Contains(rep.Disagreements[0].Problem, tc.want) {
			t.Fatalf("%s: %+v", tc.want, rep.Disagreements)
		}
	}
	adj := Adjudication{Item: "i3", Stage: Polarity, By: "cy", Tokens: []string{"dividend|neg=0|den=0|act=raised"}, Rationale: "the release says raised"}
	rep = run(corpus(), rs, []Adjudication{adj}, thresholds(), true)
	if rep.Open != 0 || rep.Resolved != 1 || stage(rep, Polarity, false).All.Items != 4 {
		t.Fatalf("%+v", rep)
	}
	if stage(rep, Polarity, false).Agreement.High == 1 && stage(rep, Polarity, false).Agreement.Value == 1 {
		t.Fatal("agreement is the raw agreement before adjudication")
	}
}

func TestRevisionsAreNewVersionsThatNameWhatTheySupersede(t *testing.T) {
	v1 := rec("ann", "i3", ItemLabel{NoClaims: true})
	v2 := Record{Labeller: "ann", Item: "i3", Version: 2, Supersedes: 1, Reason: "the second paragraph states a dividend", Label: ItemLabel{Claims: []ClaimLabel{{Kind: "dividend"}}}}
	if err := ValidateRecords([]Record{v1, v2}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Record{
		{Labeller: "ann", Item: "i3", Version: 2, Supersedes: 1},              // no reason
		{Labeller: "ann", Item: "i3", Version: 2, Supersedes: 0, Reason: "x"}, // names nothing
		{Labeller: "ann", Item: "i3", Version: 3, Supersedes: 2, Reason: "x"}, // skips a version
	} {
		if err := ValidateRecords([]Record{v1, bad}); err == nil {
			t.Fatalf("%+v was accepted", bad)
		}
	}
	latest, n := Latest([]Record{v1, v2})
	if n != 1 || latest[[2]string{"ann", "i3"}].Version != 2 {
		t.Fatalf("%d %+v", n, latest)
	}
	rs := append(labels(), v2)
	rs = slices.DeleteFunc(rs, func(r Record) bool { return r.Labeller == "ann" && r.Item == "i3" && r.Version == 1 })
	rs = append(rs, v1)
	if rep := run(corpus(), rs, nil, thresholds(), true); rep.Superseded < 1 {
		t.Fatalf("the history is reported: %d", rep.Superseded)
	}
}

func TestSyntheticRegressionsAreReportedApartAndNeverCount(t *testing.T) {
	c := corpus()
	for i := range c.Items {
		c.Items[i].Synthetic = true
	}
	rep := run(c, labels(), nil, thresholds(), true)
	if rep.Gate != NoLabels {
		t.Fatalf("labels on synthetic items passed or advanced the gate: %v", rep.Gate)
	}
	if rep.Labelled != 0 || stage(rep, Extraction, true).All.Items != 4 || stage(rep, Extraction, false).All.Items != 0 {
		t.Fatalf("%d %+v", rep.Labelled, stage(rep, Extraction, true).All)
	}
	if !strings.Contains(rep.SyntheticNote, "never count") {
		t.Fatal(rep.SyntheticNote)
	}
}

func TestTheGateNeedsStatedThresholdsAFreezeAndEnoughItems(t *testing.T) {
	c, rs := corpus(), labels()
	if rep := run(c, rs, nil, Thresholds{}, true); rep.Gate != NoThresholds {
		t.Fatalf("%v", rep.Gate)
	}
	if rep := run(c, rs, nil, thresholds(), false); rep.Gate != NotFrozen {
		t.Fatalf("%v", rep.Gate)
	}
	// changing a label, a threshold or the corpus after the freeze is visible
	r := Run{Corpus: c, Records: slices.Clone(rs), Thresholds: thresholds()}
	r.Frozen = FreezeHash(c, slices.Clone(rs), nil, thresholds())
	r.Predictions, _ = Predict(c, nil)
	r.Thresholds.MinPrecision = 0.01
	if rep := Evaluate(r); rep.Gate != Changed {
		t.Fatalf("thresholds changed after the freeze: %v", rep.Gate)
	}
	r.Thresholds = thresholds()
	r.Records[0].Label.Event = "acme-dividend"
	if rep := Evaluate(r); rep.Gate != Changed {
		t.Fatalf("a label changed after the freeze: %v", rep.Gate)
	}
	// too few items is insufficient, not a pass
	th := thresholds()
	th.MinItems = 50
	if rep := run(c, rs, nil, th, true); rep.Gate != Insufficient {
		t.Fatalf("%v %v", rep.Gate, rep.GateWhy)
	}
	// a threshold the program cannot meet is a fail, whatever the point estimate
	th = thresholds()
	th.MinPrecision, th.MinRecall = 0.999, 0.999
	if rep := run(c, rs, nil, th, true); rep.Gate != Fail {
		t.Fatalf("%v %v", rep.Gate, rep.GateWhy)
	}
}

func TestTextNotKeptInTheCorpusMustMatchItsHash(t *testing.T) {
	c := corpus()
	c.Items[0].Licence = Internal
	c.Items[0].Title, c.Items[0].Summary = "", ""
	if _, skipped := Predict(c, nil); !strings.Contains(skipped["i1"], "no article store") {
		t.Fatalf("%v", skipped)
	}
	good := func(Item) (string, string, error) {
		return "Acme Corp completed its acquisition of Bolt Industries", "", nil
	}
	if p, skipped := Predict(c, good); len(skipped) != 0 || len(p["i1"].Label.Claims) == 0 {
		t.Fatalf("%v", skipped)
	}
	changed := func(Item) (string, string, error) {
		return "Acme Corp completed its acquisition of Bolt Industries.", "edited", nil
	}
	if _, skipped := Predict(c, changed); !strings.Contains(skipped["i1"], "no longer matches") {
		t.Fatalf("%v", skipped)
	}
	missing := func(Item) (string, string, error) { return "", "", errors.New("article gone") }
	if _, skipped := Predict(c, missing); skipped["i1"] != "article gone" {
		t.Fatalf("%v", skipped)
	}
}

func TestWilsonInterval(t *testing.T) {
	if w := Wilson(0, 0); w.Low != 0 || w.High != 1 {
		t.Fatalf("%+v", w)
	}
	w := Wilson(8, 10)
	if w.Low < 0.48 || w.Low > 0.50 || w.High < 0.93 || w.High > 0.95 {
		t.Fatalf("%+v", w)
	}
	if w := Wilson(10, 10); w.High != 1 || w.Low < 0.7 {
		t.Fatalf("%+v", w)
	}
}

func TestThresholdValidation(t *testing.T) {
	for _, bad := range []Thresholds{{}, {MinAnnotators: 1, MinItems: 1, MinPrecision: .5, MinRecall: .5, MinGroup: 1}, {MinAnnotators: 2, MinItems: 1, MinPrecision: 0, MinRecall: .5, MinGroup: 1}} {
		if bad.Validate() == nil {
			t.Fatalf("%+v", bad)
		}
	}
	if err := thresholds().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestIssuerCounterpartyRolesAndContradictionsAreMeasuredBySlice(t *testing.T) {
	c := Corpus{
		Name:      "identity",
		Names:     map[string][]string{"ACME": {"Acme Corp"}, "BOLT": {"Bolt Industries"}},
		Labellers: []Labeller{{ID: "ann", Independent: true}, {ID: "bea", Independent: true}},
		Items: []Item{
			item("p1", "Bolt Industries is to be acquired by Acme Corp", "", "ACME", "BOLT"),
			item("p2", "Correction: Acme Corp did not announce a restatement", "An earlier version of this story said Acme Corp announced a restatement. It did not.", "ACME"),
		},
	}
	acq := ItemLabel{Claims: []ClaimLabel{{Kind: "acquisition", Status: "announced", Parties: []PartyLabel{{Ticker: "ACME", Role: "subject"}, {Ticker: "BOLT", Role: "counterparty"}}}}}
	corr := ItemLabel{Claims: []ClaimLabel{{Kind: "non_reliance", Status: "announced", Denied: true, Parties: []PartyLabel{{Ticker: "ACME", Role: "subject"}}}}, Slices: []string{"contradiction", "correction"}}
	var rs []Record
	for _, who := range []string{"ann", "bea"} {
		rs = append(rs, rec(who, "p1", acq), rec(who, "p2", corr))
	}
	th := Thresholds{MinAnnotators: 2, MinItems: 1, MinPrecision: 0.1, MinRecall: 0.1, MinGroup: 1}
	rep := run(c, rs, nil, th, true)

	// the acquirer is the subject and the target the counterparty, whatever the voice
	if p := stage(rep, Parties, false).All; p.FP != 0 || p.FN != 0 || p.TP != 3 {
		t.Fatalf("roles: %+v", p)
	}
	// the contradiction slice is its own line of results, with the sample size
	g, ok := stage(rep, Polarity, false).Groups["slice:contradiction"]
	if !ok || g.Items != 1 {
		t.Fatalf("%+v", stage(rep, Polarity, false).Groups)
	}
	if _, ok := stage(rep, Extraction, false).Groups["slice:correction"]; !ok {
		t.Fatal("a correction slice")
	}
}

func TestUnstatedThresholdsDoNotCrashSettlingAndStillNeedTwoAnnotators(t *testing.T) {
	g := Settle(corpus(), labels(), nil, Thresholds{})
	if len(g.Labelled) != 4 {
		t.Fatalf("%v", g.Labelled)
	}
	one := slices.DeleteFunc(labels(), func(r Record) bool { return r.Labeller == "bea" })
	if g := Settle(corpus(), one, nil, Thresholds{}); len(g.Labelled) != 0 {
		t.Fatalf("one annotator is not enough: %v", g.Labelled)
	}
}
