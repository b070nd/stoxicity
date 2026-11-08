package walkfwd

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/allocate"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/paper"
)

func d(s string) money.Decimal { return money.MustDecimal(s) }

func wd(start string, n int) []string {
	t, _ := time.Parse(time.DateOnly, start)
	var out []string
	for len(out) < n {
		if w := t.Weekday(); w != time.Saturday && w != time.Sunday {
			out = append(out, t.Format(time.DateOnly))
		}
		t = t.AddDate(0, 0, 1)
	}
	return out
}

func bar(sec, day, o, h, l, c string, vol int64) paper.Bar {
	return paper.Bar{Security: sec, Day: day, Open: d(o), High: d(h), Low: d(l), Close: d(c), Volume: vol, Currency: "USD"}
}

// flat is n sessions of a security hovering around px.
func flat(sec, start string, n int, px string) []paper.Bar {
	var out []paper.Bar
	for _, day := range wd(start, n) {
		out = append(out, bar(sec, day, px, "101", "99", px, 1000000))
	}
	return out
}

func splits() Splits {
	return Splits{Train: Span{"2020-01-01", "2020-12-31"}, Tune: Span{"2021-02-01", "2021-12-31"}, Test: Span{"2022-02-01", "2022-12-31"}, Reserve: Span{"2023-02-01", "2023-12-31"}, EmbargoDays: 20}
}

func criteria() Criteria {
	return Criteria{MinMatured: 4, MinClusters: 4, MinCoverage: 0.8, MinFillRate: 0.5, MinExcessLower: 0, MaxWorstLoss: 0.3, Alpha: 0.05}
}

func goodManifest() Manifest {
	return Manifest{Kind: Known, PointInTime: true, IncludesDelisted: true, Fund: "fund"}
}

// scenario: five trades a month apart, each buying at 100 limit 100.5 and rising.
func scenario(t *testing.T) Input {
	t.Helper()
	var bars []paper.Bar
	var props []Proposal
	for i := 0; i < 5; i++ {
		start := time.Date(2022, time.Month(3+i*2), 1, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
		days := wd(start, 30)
		sec := "s" + string(rune('a'+i))
		for j, day := range days {
			px := "100"
			if j >= 3 {
				px = "110"
			}
			bars = append(bars, bar(sec, day, px, "112", "99", px, 1000000))
		}
		props = append(props, Proposal{ID: "p" + sec, Trial: "t1", Security: sec, Sector: "tech", Decided: days[0], KnownOn: days[0], Limit: d("100.5"), Qty: d("10"), HorizonSessions: 8, Currency: "USD"})
	}
	// the fund is flat
	fundDays := wd("2022-03-01", 400)
	for _, day := range fundDays {
		bars = append(bars, bar("fund", day, "50", "50", "50", "50", 1))
	}
	return Input{Trial: Trial{ID: "t1", Strategy: "x"}, Trials: 1, Period: Test, Splits: splits(), Criteria: criteria(), Config: DefaultConfig(), Manifest: goodManifest(), Proposals: props, Bars: bars}
}

func TestACleanKnownReplayWithEnoughIndependentTradesIsEvaluated(t *testing.T) {
	r, err := Run(scenario(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Matured != 5 || r.ClusterN != 5 || r.Status != Evaluated {
		t.Fatalf("%+v", r)
	}
	if r.Mean <= 0 || r.Excess == nil || r.Excess.Low <= 0 {
		t.Fatalf("%+v %+v", r.Mean, r.Excess)
	}
	// the entry is the session after the decision, at the limit, never before
	for _, o := range r.Outcomes {
		if o.Entry <= "2022-03-01" {
			t.Fatalf("entry %s", o.Entry)
		}
	}
	r2, _ := Run(scenario(t))
	if r2.Manifest != r.Manifest || r2.Excess.Low != r.Excess.Low {
		t.Fatal("a rerun gives the same manifest and the same interval")
	}
}

func TestLookaheadIsExcludedAndInformationLateInTheDayWaitsForTheNextSession(t *testing.T) {
	in := scenario(t)
	in.Proposals[0].KnownOn = "2022-03-02" // known after the decision
	r, _ := Run(in)
	if r.Excluded != 1 || r.Matured != 4 {
		t.Fatalf("%+v", r)
	}
	in = scenario(t)
	in.Proposals[1].Decided = in.Proposals[1].KnownOn
	in.Proposals[1].KnownOn = in.Proposals[1].Decided
	r, _ = Run(in)
	for _, o := range r.Outcomes {
		if o.Proposal == in.Proposals[1].ID && o.Entry <= in.Proposals[1].Decided {
			t.Fatalf("same-bar entry: %+v", o)
		}
	}
}

func TestAnEntryNeedsTheLowToGoStrictlyBelowTheLimitAndVolumeToTakeIt(t *testing.T) {
	in := scenario(t)
	// touching the limit is not a fill
	for i := range in.Bars {
		if in.Bars[i].Security == "sa" && in.Bars[i].Day == wd("2022-03-01", 2)[1] {
			in.Bars[i].Low = d("100.5")
		}
	}
	r, _ := Run(in)
	if r.Unfilled != 1 || r.Matured != 4 {
		t.Fatalf("%+v", r)
	}
	// a thin day: 1% of 500 shares is 5, below a quarter of 10 is 2.5? it is 5, so it fills; 100 shares of volume is 1
	in = scenario(t)
	for i := range in.Bars {
		if in.Bars[i].Security == "sb" {
			in.Bars[i].Volume = 100
		}
	}
	r, _ = Run(in)
	if r.Unfilled != 5-r.Matured || r.Matured >= 5 {
		t.Fatalf("a thin market is not filled: %+v", r)
	}
	if r.FillRate >= 1 {
		t.Fatalf("%v", r.FillRate)
	}
}

func TestAStopNeverFillsAtItsOwnPriceAndAnAmbiguousBarTakesTheStop(t *testing.T) {
	in := scenario(t)
	inv := d("98")
	in.Proposals[0].Invalidation = &inv
	// day after entry gaps down through the stop
	days := wd("2022-03-01", 30)
	for i := range in.Bars {
		if in.Bars[i].Security == "sa" && in.Bars[i].Day == days[3] {
			in.Bars[i] = bar("sa", days[3], "90", "95", "89", "92", 1000000)
		}
	}
	r, _ := Run(in)
	var o Outcome
	for _, x := range r.Outcomes {
		if x.Proposal == "psa" {
			o = x
		}
	}
	if o.Reason != "invalidated" || !o.Gap || o.exitPx >= 90 {
		t.Fatalf("a gap fills at the open less slippage, not at the stop: %+v", o)
	}
	// the entry bar itself reaches the stop: both could have happened, the stop is taken
	in = scenario(t)
	in.Proposals[0].Invalidation = &inv
	for i := range in.Bars {
		if in.Bars[i].Security == "sa" && in.Bars[i].Day == days[1] {
			in.Bars[i] = bar("sa", days[1], "100", "101", "97", "99", 1000000)
		}
	}
	r, _ = Run(in)
	if r.Ambiguous != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestDelistingWithoutATerminalValueIsATotalLossAndOpenTradesAreCensored(t *testing.T) {
	in := scenario(t)
	// sa's bars stop after entry, and it is delisted
	days := wd("2022-03-01", 30)
	var kept []paper.Bar
	for _, b := range in.Bars {
		if b.Security == "sa" && b.Day > days[4] {
			continue
		}
		kept = append(kept, b)
	}
	in.Bars = kept
	r, _ := Run(in)
	if r.Censored != 1 || r.Matured != 4 {
		t.Fatalf("no exit in the data and no delisting record is a censored trade: %+v", r)
	}
	in.Delist = []Delisting{{Security: "sa", Day: days[4]}}
	r, _ = Run(in)
	if r.Delisted != 1 || r.Worst > -0.99 {
		t.Fatalf("%+v %v", r.Delisted, r.Worst)
	}
	if r.Status == Evaluated {
		t.Fatalf("a total loss beyond the downside limit cannot be evaluated: %v", r.Why)
	}
	term := d("60")
	in.Delist = []Delisting{{Security: "sa", Day: days[4], Terminal: &term}}
	r, _ = Run(in)
	if r.Worst < -0.5 || r.Worst > -0.3 {
		t.Fatalf("a terminal value of 60 on 100: %v", r.Worst)
	}
}

func TestCostsDistributionsAndSlippageChangeTheResultAndAreShown(t *testing.T) {
	in := scenario(t)
	base, _ := Run(in)
	in.Config.Fees = allocate.Fees{Known: true, Min: 500, Source: "test"} // 5.00 a side
	costly, _ := Run(in)
	if costly.Mean >= base.Mean || costly.Costs <= base.Costs {
		t.Fatalf("%v %v", costly.Mean, base.Mean)
	}
	in = scenario(t)
	in.Dists = []Dist{{Security: "sa", ExDay: wd("2022-03-01", 10)[6], PerShare: d("2")}}
	withDist, _ := Run(in)
	if withDist.Mean <= base.Mean {
		t.Fatal("a distribution in the holding window adds")
	}
	// bought on the ex-day: not entitled
	in.Dists = []Dist{{Security: "sa", ExDay: wd("2022-03-01", 2)[1], PerShare: d("2")}}
	if r, _ := Run(in); r.Mean != base.Mean {
		t.Fatal("the buyer on the ex-day is not entitled")
	}
	if len(base.SlippageSens) != 3 || base.SlippageSens["0.0050"] >= base.SlippageSens["0.0000"] {
		t.Fatalf("%v", base.SlippageSens)
	}
	if base.WithholdingSens["0.30"] > base.WithholdingSens["0.00"] {
		t.Fatalf("%v", base.WithholdingSens)
	}
}

func TestNotEnoughHistoryIsAnAnswerNotAFailureToAnswer(t *testing.T) {
	in := scenario(t)
	in.Proposals = in.Proposals[:2]
	r, _ := Run(in)
	if r.Status != InsufficientHistory || !strings.Contains(strings.Join(r.Why, " "), "matured trades") {
		t.Fatalf("%+v", r)
	}
}

func TestOverlappingTradesAreOneIndependentObservation(t *testing.T) {
	rs := []Outcome{{Entry: "2022-03-01", Exit: "2022-03-10"}, {Entry: "2022-03-05", Exit: "2022-03-12"}, {Entry: "2022-04-01", Exit: "2022-04-05"}}
	if cl := clusters(rs); len(cl) != 2 || len(cl[0]) != 2 {
		t.Fatalf("%v", cl)
	}
}

func TestStatusIsCappedByWhatTheDataIs(t *testing.T) {
	for name, edit := range map[string]func(*Input){
		"synthetic":         func(in *Input) { in.Manifest.Synthetic = true },
		"reconstructed":     func(in *Input) { in.Manifest.Kind = Reconstructed },
		"survivorship":      func(in *Input) { in.Manifest.IncludesDelisted = false },
		"not point in time": func(in *Input) { in.Manifest.PointInTime = false },
		"test read before":  func(in *Input) { in.TestReads = 1 },
	} {
		in := scenario(t)
		edit(&in)
		r, _ := Run(in)
		if r.Status != Experimental {
			t.Errorf("%s: %v %v", name, r.Status, r.Why)
		}
	}
	// tuning periods never give evaluated
	in := scenario(t)
	in.Period = Tune
	in.Proposals = nil
	if r, _ := Run(in); r.Status == Evaluated {
		t.Fatal("tune")
	}
}

func TestMoreTrialsNeedMoreEvidence(t *testing.T) {
	one, _ := Run(scenario(t))
	in := scenario(t)
	in.Trials = 40
	many, _ := Run(in)
	if many.Level <= one.Level || many.Excess.Low > one.Excess.Low {
		t.Fatalf("%v %v %v %v", one.Level, many.Level, one.Excess.Low, many.Excess.Low)
	}
	if !strings.Contains(strings.Join(many.Diagnostics, " "), "40 trials") {
		t.Fatal(many.Diagnostics)
	}
}

func TestSplitsMustBeOrderedAndSeparatedByTheEmbargo(t *testing.T) {
	if err := splits().Validate(); err != nil {
		t.Fatal(err)
	}
	s := splits()
	s.Tune.From = "2021-01-05" // 5 days after train, embargo 20
	if s.Validate() == nil {
		t.Fatal("embargo")
	}
	s = splits()
	s.EmbargoDays = 0
	if s.Validate() == nil {
		t.Fatal("no embargo")
	}
	s = splits()
	s.Test.To = "2021-12-01"
	if s.Validate() == nil {
		t.Fatal("order")
	}
	if p, ok := splits().Of("2022-06-01"); !ok || p != Test {
		t.Fatal(p)
	}
	if _, ok := splits().Of("2022-01-10"); ok {
		t.Fatal("an embargo day belongs to no period")
	}
	if _, err := Run(Input{Splits: splits(), Criteria: criteria(), Manifest: goodManifest()}); err == nil {
		t.Fatal("an unregistered trial must not run")
	}
}

func TestNoTradeDecisionsStayInTheCounts(t *testing.T) {
	in := scenario(t)
	in.Proposals = append(in.Proposals, Proposal{ID: "wait", Trial: "t1", Security: "sa", Decided: "2022-03-01", KnownOn: "2022-03-01", Qty: d("0")})
	r, _ := Run(in)
	if r.NoTrade != 1 || r.Proposals != 6 {
		t.Fatalf("%+v", r)
	}
}
