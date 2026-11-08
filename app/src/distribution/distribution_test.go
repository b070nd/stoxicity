package distribution

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func ob(sec string, typ Type, period string, st Status, amt, ex, source string, seq int64) Obs {
	return Obs{Seq: seq, SecurityID: sec, Type: typ, Period: period, Status: st, Amount: money.MustDecimal(amt), Currency: "USD",
		ExDay: ex, Source: source, Received: t0.Add(time.Duration(seq) * time.Hour)}
}

func TestObsValidation(t *testing.T) {
	good := ob("sec_apple", Ordinary, "2026Q3", Declared, "0.25", "2026-08-11", "co", 1)
	good.PayDay = "2026-08-14"
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*Obs){
		"no security":       func(o *Obs) { o.SecurityID = "" },
		"bad type":          func(o *Obs) { o.Type = "regular" },
		"bad period":        func(o *Obs) { o.Period = "Q3 2026" },
		"month 13":          func(o *Obs) { o.Period = "2026-13" },
		"zero declared":     func(o *Obs) { o.Amount = money.Int(0) },
		"negative":          func(o *Obs) { o.Amount = money.MustDecimal("-0.1") },
		"unknown currency":  func(o *Obs) { o.Currency = "GBp" },
		"no currency":       func(o *Obs) { o.Currency = "" },
		"no ex date":        func(o *Obs) { o.ExDay = "" },
		"pay before ex":     func(o *Obs) { o.PayDay = "2026-08-01" },
		"record before ex":  func(o *Obs) { o.RecordDay = "2026-08-01" },
		"declared after ex": func(o *Obs) { o.DeclaredDay = "2026-09-01" },
		"no source":         func(o *Obs) { o.Source = "" },
		"unknown status":    func(o *Obs) { o.Status = "paid" },
		"estimated zero":    func(o *Obs) { o.Status, o.Amount = Estimated, money.Int(0) },
	}
	for name, edit := range bad {
		o := good
		edit(&o)
		if o.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	c := good
	c.Status, c.Amount = Cancelled, money.Int(0)
	if err := c.Validate(); err != nil {
		t.Errorf("a cancellation of zero: %v", err)
	}
}

func byKey(ps []Payout, period string) Payout {
	for _, p := range ps {
		if p.Key.Period == period {
			return p
		}
	}
	return Payout{}
}

func TestResolveDeclaredEstimatedCancelledAndConflicting(t *testing.T) {
	obs := []Obs{
		// An estimate, then the company declares: the declaration stands.
		ob("s", Ordinary, "2026Q1", Estimated, "0.24", "2026-02-10", "forecaster", 1),
		ob("s", Ordinary, "2026Q1", Declared, "0.25", "2026-02-11", "company", 2),
		// Two sources, two ex-dates.
		ob("s", Ordinary, "2026Q2", Declared, "0.25", "2026-05-11", "company", 3),
		ob("s", Ordinary, "2026Q2", Declared, "0.25", "2026-05-12", "aggregator", 4),
		// Declared, then withdrawn by the same source: cancelled.
		ob("s", Ordinary, "2026Q3", Declared, "0.25", "2026-08-11", "company", 5),
		ob("s", Ordinary, "2026Q3", Cancelled, "0", "2026-08-11", "company", 6),
		// A cancellation beside a declaration from another source.
		ob("s", Ordinary, "2026Q4", Declared, "0.25", "2026-11-10", "company", 7),
		ob("s", Ordinary, "2026Q4", Cancelled, "0", "2026-11-10", "aggregator", 8),
		// Only a guess.
		ob("s", Ordinary, "2027Q1", Estimated, "0.25", "2027-02-10", "forecaster", 9),
	}
	ps := Resolve(obs, time.Time{})
	q1 := byKey(ps, "2026Q1")
	if q1.State != StateDeclared || q1.Amount.String() != "0.25" || q1.Revisions != 2 || !strings.Contains(strings.Join(q1.Reasons, " "), "estimate(s) ignored") {
		t.Errorf("q1 %+v", q1)
	}
	q2 := byKey(ps, "2026Q2")
	if q2.State != StateConflicting || !strings.Contains(strings.Join(q2.Reasons, " "), "ex-date differs") || q2.ExDay != "2026-05-11" {
		t.Errorf("a conflicting ex-date must not be resolved by picking one: %+v", q2)
	}
	if q3 := byKey(ps, "2026Q3"); q3.State != StateCancelled {
		t.Errorf("q3 %+v", q3)
	}
	if q4 := byKey(ps, "2026Q4"); q4.State != StateConflicting || !strings.Contains(strings.Join(q4.Reasons, " "), "cancelled it") {
		t.Errorf("q4 %+v", q4)
	}
	est := byKey(ps, "2027Q1")
	if est.State != StateEstimated || !strings.Contains(strings.Join(est.Reasons, " "), "not cash") {
		t.Errorf("an estimate must say it is not cash: %+v", est)
	}

	// Revisions: before the cancellation was received the payout was declared.
	early := Resolve(obs, t0.Add(5*time.Hour))
	if byKey(early, "2026Q3").State != StateDeclared {
		t.Errorf("as-of before the cancellation: %+v", byKey(early, "2026Q3"))
	}
	// The same source revising its own amount (a cut) is a revision, not a conflict.
	rev := []Obs{ob("s", Ordinary, "2026Q1", Declared, "0.25", "2026-02-11", "company", 1), ob("s", Ordinary, "2026Q1", Declared, "0.20", "2026-02-11", "company", 2)}
	if p := Resolve(rev, time.Time{})[0]; p.State != StateDeclared || p.Amount.String() != "0.20" {
		t.Errorf("%+v", p)
	}
}

func TestEstimatesNeverBecomeDeclaredOrAddedToDeclared(t *testing.T) {
	obs := []Obs{
		ob("s", Ordinary, "2026Q4", Estimated, "0.30", "2026-11-10", "a", 1),
		ob("s", Ordinary, "2026Q4", Estimated, "0.35", "2026-11-10", "b", 2),
		ob("s", Ordinary, "2026Q3", Declared, "0.25", "2026-08-11", "company", 3),
		ob("s", Ordinary, "2027Q1", Estimated, "0.25", "2027-02-10", "a", 4),
	}
	ps := Resolve(obs, time.Time{})
	if p := byKey(ps, "2026Q4"); p.State != StateConflicting {
		t.Errorf("disagreeing estimates: %+v", p)
	}
	c := BuildCalendar(ps, "2026-01-01", "2027-12-31")
	if c.Conflicting != 1 || len(c.Totals) != 1 {
		t.Fatalf("%+v", c)
	}
	tot := c.Totals[0]
	if tot.Declared.String() != "0.25" || tot.Estimated.String() != "0.25" || tot.NDeclared != 1 || tot.NEst != 1 {
		t.Errorf("declared and estimated are separate columns: %+v", tot)
	}
	// A narrower range leaves the rest out.
	if c = BuildCalendar(ps, "2026-08-01", "2026-08-31"); len(c.Entries) != 1 {
		t.Errorf("%+v", c)
	}
}

func TestSplitsRestateEarlierAmounts(t *testing.T) {
	ps := Resolve([]Obs{
		ob("s", Ordinary, "2026Q1", Declared, "1.00", "2026-02-10", "co", 1),
		ob("s", Ordinary, "2026Q3", Declared, "0.50", "2026-08-10", "co", 2),
		ob("other", Ordinary, "2026Q1", Declared, "1.00", "2026-02-10", "co", 3),
	}, time.Time{})
	adj, err := AdjustForSplits(ps, []Split{{SecurityID: "s", Day: "2026-06-01", From: 1, To: 2}})
	if err != nil {
		t.Fatal(err)
	}
	q1, q3 := byKey(adj, "2026Q1"), byKey(adj, "2026Q3")
	for _, p := range adj {
		if p.Key.SecurityID == "s" && p.Key.Period == "2026Q1" {
			q1 = p
		}
	}
	if q1.Amount.String() != "0.50" || !q1.Adjusted || q3.Adjusted || q3.Amount.String() != "0.50" {
		t.Errorf("q1 %+v q3 %+v", q1, q3)
	}
	for _, p := range adj {
		if p.Key.SecurityID == "other" && (p.Adjusted || p.Amount.String() != "1.00") {
			t.Errorf("another security's payout was adjusted: %+v", p)
		}
	}
	// An inexact division is rounded and says so.
	var mine []Payout
	for _, p := range ps {
		if p.Key.SecurityID == "s" && p.Key.Period == "2026Q1" {
			mine = append(mine, p)
		}
	}
	three, _ := AdjustForSplits(mine, []Split{{SecurityID: "s", Day: "2026-06-01", From: 1, To: 3}})
	if !strings.HasPrefix(three[0].Amount.String(), "0.3333") || !strings.Contains(strings.Join(three[0].Reasons, " "), "rounded half-even") {
		t.Errorf("%+v", three[0])
	}
	if _, err := AdjustForSplits(ps, []Split{{SecurityID: "s", Day: "2026-06-01", From: 0, To: 2}}); err == nil {
		t.Error("a split of zero")
	}
}

func apple() instrument.Security {
	return instrument.Security{ID: "s", Kind: instrument.KindStock, Name: "Acme"}
}

func quarterSeries(t *testing.T, amounts map[string]string) []Payout {
	t.Helper()
	var obs []Obs
	seq := int64(0)
	exDay := map[string]string{"2025Q1": "2025-02-10", "2025Q2": "2025-05-10", "2025Q3": "2025-08-10", "2025Q4": "2025-11-10",
		"2026Q1": "2026-02-10", "2026Q2": "2026-05-10", "2026Q3": "2026-08-10"}
	for p, a := range amounts {
		seq++
		st := Declared
		if a == "cancelled" {
			st, a = Cancelled, "0"
		}
		obs = append(obs, ob("s", Ordinary, p, st, a, exDay[p], "co", seq))
	}
	return Resolve(obs, time.Time{})
}

func TestQualityFindsACutAGapAndCountsSpecialsApart(t *testing.T) {
	ps := quarterSeries(t, map[string]string{
		"2025Q1": "0.25", "2025Q2": "0.25", "2025Q3": "0.25", "2025Q4": "0.25",
		"2026Q1": "0.25", "2026Q3": "0.10", // 2026Q2 is missing from the series
	})
	ps = append(ps, Resolve([]Obs{ob("s", Special, "2026", Declared, "1.00", "2026-03-01", "co", 99)}, time.Time{})...)
	q := Analyse(Input{Security: apple(), Day: "2026-09-01", Payouts: ps, SplitsKnown: true})
	if q.Coverage != Observed || q.Frequency != "quarterly" {
		t.Fatalf("%+v", q)
	}
	// 12 months to 2026-09-01: 2025Q4 (Nov), 2026Q1, 2026Q3 ordinary; the special is apart.
	if got := q.OrdinaryTTM["USD"].String(); got != "0.60" {
		t.Errorf("ordinary TTM %s", got)
	}
	if got := q.SpecialTTM["USD"].String(); got != "1.00" {
		t.Errorf("special TTM %s", got)
	}
	if q.Latest == nil || q.Latest.Direction != "cut" || q.Latest.Ratio.String() != "-0.6000" || q.Latest.Prior != "2025Q3" {
		t.Errorf("latest %+v", q.Latest)
	}
	if len(q.Cuts) != 1 {
		t.Errorf("cuts %+v", q.Cuts)
	}
	if len(q.MissedPeriods) != 1 || q.MissedPeriods[0] != "2026Q2" {
		t.Errorf("missed %v", q.MissedPeriods)
	}
	// With no split history the cut is not asserted as a business fact.
	q2 := Analyse(Input{Security: apple(), Day: "2026-09-01", Payouts: ps})
	if !strings.Contains(strings.Join(q2.Unknowns, " "), "a cut may be a share split") {
		t.Errorf("%v", q2.Unknowns)
	}
}

func TestQualityCancellationsAndEstimatesAreKeptOut(t *testing.T) {
	ps := quarterSeries(t, map[string]string{"2025Q3": "0.25", "2025Q4": "cancelled", "2026Q1": "0.25"})
	ps = append(ps, Resolve([]Obs{ob("s", Ordinary, "2026Q4", Estimated, "0.30", "2026-11-10", "f", 50)}, time.Time{})...)
	q := Analyse(Input{Security: apple(), Day: "2026-09-01", Payouts: ps, SplitsKnown: true})
	if q.Cancellations != 1 || q.OrdinaryTTM["USD"].String() != "0.25" {
		t.Errorf("%+v", q)
	}
	if len(q.Estimated) != 1 || q.Estimated[0].State != StateEstimated {
		t.Errorf("an estimate is listed apart: %+v", q.Estimated)
	}
	late := Analyse(Input{Security: apple(), Day: "2026-12-01", Payouts: ps, SplitsKnown: true})
	if !strings.Contains(strings.Join(late.Unknowns, " "), "overdue") {
		t.Errorf("an estimate whose ex-date passed undeclared must be called overdue: %v", late.Unknowns)
	}
}

func TestCoverageUnknownVersusNoneByDesign(t *testing.T) {
	q := Analyse(Input{Security: apple(), Day: "2026-09-01", SplitsKnown: true})
	if q.Coverage != Unknown || !strings.Contains(strings.Join(q.Unknowns, " "), "not evidence") {
		t.Errorf("no data is not no dividend: %+v", q)
	}
	acc := instrument.Security{ID: "acc", Kind: instrument.KindETF, Distribution: instrument.Accumulating}
	q = Analyse(Input{Security: acc, Day: "2026-09-01", SplitsKnown: true})
	if q.Coverage != NoneByDesign || q.Cover != CoverUnsupported {
		t.Errorf("an accumulating class pays nothing by design: %+v", q)
	}
	dist := instrument.Security{ID: "dist", Kind: instrument.KindETF, Distribution: instrument.Distributing}
	if q = Analyse(Input{Security: dist, Day: "2026-09-01"}); q.Coverage != Unknown {
		t.Errorf("a distributing fund with nothing on record: %+v", q)
	}
}

func TestCoverNeedsASupportedSectorAndInputs(t *testing.T) {
	ps := quarterSeries(t, map[string]string{"2025Q4": "0.25", "2026Q1": "0.25", "2026Q3": "0.25"})
	fcf := money.MustDecimal("3.00")
	in := Input{Security: apple(), Day: "2026-09-01", Payouts: ps, SplitsKnown: true, Sector: "technology", FCFPerShare: &fcf, FCFCurrency: "USD"}
	q := Analyse(in)
	if q.Cover != CoverComputed || q.CoverRatio.String() != "4.00" {
		t.Errorf("3.00 / 0.75: %+v", q)
	}
	in.Sector = "Regional Bank"
	if q = Analyse(in); q.Cover != CoverUnsupported || q.CoverRatio != nil || !strings.Contains(q.CoverWhy, "bank") {
		t.Errorf("%+v", q)
	}
	in.Sector = ""
	if q = Analyse(in); q.Cover != CoverUnknown {
		t.Errorf("%+v", q)
	}
	in.Sector, in.FCFCurrency = "technology", "EUR"
	if q = Analyse(in); q.Cover != CoverUnknown || !strings.Contains(q.CoverWhy, "convert") {
		t.Errorf("currency mismatch: %+v", q)
	}
	in.Sector, in.FCFPerShare, in.FCFCurrency = "technology", nil, "USD"
	if q = Analyse(in); q.Cover != CoverUnknown {
		t.Errorf("%+v", q)
	}
	in.Sector, in.Security.Kind = "technology", instrument.KindETF
	in.FCFPerShare = &fcf
	if q = Analyse(in); q.Cover != CoverUnsupported {
		t.Errorf("a fund has no free cash flow cover: %+v", q)
	}
}

func TestCSVResolvesSecuritiesAndRefusesAccumulatingClasses(t *testing.T) {
	reg, _, err := instrument.New(instrument.ExampleSet())
	if err != nil {
		t.Fatal(err)
	}
	good := "type,period,status,amount,currency,ex_day,pay_day,mic,ticker\n" +
		"ordinary,2026Q3,declared,0.25,USD,2026-08-11,2026-08-14,XNAS,AAPL\n" +
		"ordinary,2026Q3,declared,0.4120,EUR,2026-09-10,,XETR,EXWD\n"
	obs, err := ParseCSV(strings.NewReader(good), reg, "file", "sha256:x", t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 || obs[0].SecurityID != "sec_apple" || obs[1].SecurityID != "sec_exw_dist" || obs[1].Amount.String() != "0.4120" {
		t.Fatalf("%+v", obs)
	}
	bad := map[string]string{
		"accumulating":   "type,period,status,amount,currency,ex_day,mic,ticker\nordinary,2026Q3,declared,0.4,EUR,2026-09-10,XETR,EXWA\n",
		"no security":    "type,period,status,amount,currency,ex_day\nordinary,2026Q3,declared,0.4,EUR,2026-09-10\n",
		"unknown":        "type,period,status,amount,currency,ex_day,mic,ticker\nordinary,2026Q3,declared,0.4,EUR,2026-09-10,XNAS,NOPE\n",
		"no currency":    "type,period,status,amount,currency,ex_day,mic,ticker\nordinary,2026Q3,declared,0.4,,2026-09-10,XNAS,AAPL\n",
		"pence":          "type,period,status,amount,currency,ex_day,mic,ticker\nordinary,2026Q3,declared,40,GBp,2026-09-10,XLON,BA\n",
		"exponent":       "type,period,status,amount,currency,ex_day,mic,ticker\nordinary,2026Q3,declared,4e-1,USD,2026-09-10,XNAS,AAPL\n",
		"unknown column": "type,period,status,amount,currency,ex_day,mic,ticker,div\nordinary,2026Q3,declared,0.4,USD,2026-09-10,XNAS,AAPL,1\n",
	}
	for name, body := range bad {
		if _, err := ParseCSV(strings.NewReader(body), reg, "file", "ref", t0); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// The cross-listed fund resolves to one security from either venue.
	both := "type,period,status,amount,currency,ex_day,mic,ticker\nordinary,2026Q3,declared,0.4,EUR,2026-09-10,XLON,EXWD\n"
	if o, err := ParseCSV(strings.NewReader(both), reg, "file", "ref", t0); err != nil || o[0].SecurityID != "sec_exw_dist" {
		t.Errorf("%+v %v", o, err)
	}
	// Two listings of one class are one security: a ticker alone is enough here.
	one := "type,period,status,amount,currency,ex_day,ticker\nordinary,2026Q3,declared,0.4,EUR,2026-09-10,EXWD\n"
	if o, err := ParseCSV(strings.NewReader(one), reg, "file", "ref", t0); err != nil || o[0].SecurityID != "sec_exw_dist" {
		t.Errorf("%+v %v", o, err)
	}
	sp, err := ParseSplitsCSV(strings.NewReader("security,day,from,to\nsec_apple,2020-08-31,1,4\n"))
	if err != nil || len(sp) != 1 || sp[0].To != 4 {
		t.Errorf("%+v %v", sp, err)
	}
	if _, err := ParseSplitsCSV(strings.NewReader("security,day,from,to\nsec_apple,2020-08-31,x,4\n")); err == nil {
		t.Error("non-numeric split")
	}
}

func TestPeriods(t *testing.T) {
	for in, want := range map[string]string{"2026Q3": "2025Q3", "2026H1": "2025H1", "2026-11": "2025-11", "2026": "2025"} {
		if got := PriorYear(in); got != want {
			t.Errorf("PriorYear(%s) = %s", in, got)
		}
	}
	if nextPeriod("2026Q4") != "2027Q1" || nextPeriod("2026-12") != "2027-01" || nextPeriod("2026H2") != "2027H1" || nextPeriod("2026") != "2027" {
		t.Error("nextPeriod")
	}
	if SectorOK, _ := SectorSupport("bank holding"); SectorOK {
		t.Error("bank")
	}
}

func TestAConflictingPeriodIsNotMissed(t *testing.T) {
	obs := []Obs{
		ob("s", Ordinary, "2026Q1", Declared, "0.25", "2026-02-10", "co", 1),
		ob("s", Ordinary, "2026Q2", Declared, "0.25", "2026-05-10", "co", 2),
		ob("s", Ordinary, "2026Q2", Declared, "0.25", "2026-05-11", "agg", 3),
		ob("s", Ordinary, "2026Q3", Declared, "0.25", "2026-08-10", "co", 4),
	}
	q := Analyse(Input{Security: apple(), Day: "2026-09-01", Payouts: Resolve(obs, time.Time{}), SplitsKnown: true})
	if len(q.MissedPeriods) != 0 {
		t.Errorf("a disputed period is present, not missed: %v", q.MissedPeriods)
	}
}
