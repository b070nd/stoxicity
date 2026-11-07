package valuation

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/universe"
)

func prof(ticker, cik string) universe.Profile {
	return universe.Profile{Ticker: ticker, Name: ticker + " Corp", CIK: cik, Exchange: "Nasdaq", Kind: universe.KindStock}
}

// valueOf computes one single-class issuer's value for the standing tests.
func valueOf(t *testing.T, cik, ticker string, mut func(*Obs, *Mapping, *Market)) Value {
	t.Helper()
	o := ob(cik, cik+"-1", "", ticker, 100)
	mp := Mapping{Ticker: ticker, SecurityID: "s_" + ticker, Status: Mapped, Venue: "Nasdaq"}
	m := market(map[string]float64{ticker: 5})
	if mut != nil {
		mut(&o, &mp, &m)
	}
	return Compute(pickOf(t, o), maps([]Mapping{mp}), m, now)
}

func TestEveryIssuerHasOneStateAndEveryMissingValueAReason(t *testing.T) {
	profiles := []universe.Profile{
		prof("VAL", "1"), prof("OLD", "2"), prof("AMB", "3"), prof("NOPX", "4"), prof("ADRX", "5"), prof("PEND", "6"),
		prof("BAD", "7"), prof("NOCV", "8"), prof("FOR", "9"),
	}
	set := Set{ByCIK: map[string]Value{
		"1": valueOf(t, "1", "VAL", nil),
		"2": valueOf(t, "2", "OLD", func(o *Obs, _ *Mapping, _ *Market) { o.AsOf = "2026-01-02" }),
		"3": valueOf(t, "3", "AMB", func(_ *Obs, mp *Mapping, _ *Market) { mp.Status = Ambiguous; mp.Basis = "two candidates" }),
		"4": valueOf(t, "4", "NOPX", func(_ *Obs, _ *Mapping, m *Market) { m.Prices = map[string]Price{} }),
		"5": valueOf(t, "5", "ADRX", func(_ *Obs, mp *Mapping, _ *Market) { mp.Type = "adr" }),
		"9": valueOf(t, "9", "FOR", func(_ *Obs, _ *Mapping, m *Market) {
			p := m.Prices["FOR"]
			p.Currency = "EUR"
			m.Prices["FOR"] = p
		}),
	}}
	checks := map[string]Check{
		"1": {At: now}, "2": {At: now}, "3": {At: now}, "4": {At: now}, "5": {At: now}, "9": {At: now},
		"7": {At: now, Kind: "failed", Problem: "timeout", Failures: 2, Next: now.Add(6 * 24 * time.Hour)},
		"8": {At: now, Kind: "nocover", Problem: "no annual or quarterly report with a machine-readable cover page", Next: now.Add(30 * 24 * time.Hour)},
	}
	cov := Measure(profiles, set, checks, map[string]bool{"3": true, "6": true}, now)
	want := map[string]State{"VAL": StateValued, "OLD": StateStale, "AMB": StateUnresolved, "NOPX": StateIncomplete, "ADRX": StateUnsupported,
		"PEND": StatePending, "BAD": StateFailed, "NOCV": StateUnsupported, "FOR": StateUnsupported}
	for _, s := range cov.Issuers {
		if s.State != want[s.Ticker] {
			t.Errorf("%s is %s (%s), want %s", s.Ticker, s.State, s.Reason, want[s.Ticker])
		}
		if (s.State == StateValued) != (s.Reason == "") {
			t.Errorf("%s: a missing value says why and a value does not: %q", s.Ticker, s.Reason)
		}
	}
	sum := 0
	for _, n := range cov.All.ByState {
		sum += n
	}
	if cov.All.Issuers != 9 || sum != 9 || cov.All.Checked != 8 || cov.All.Mapped != 5 || cov.All.Retrying != 1 {
		t.Errorf("all = %+v: the states add up to the issuers", cov.All)
	}
	if cov.Followed.Issuers != 2 || cov.Followed.ByState[StateUnresolved] != 1 || cov.Followed.ByState[StatePending] != 1 {
		t.Errorf("followed = %+v", cov.Followed)
	}
	if first := cov.Issuers[0]; !first.Followed || cov.Issuers[1].Followed == false {
		t.Errorf("the followed issuers are listed first: %+v", cov.Issuers[:2])
	}
	for _, s := range cov.Issuers {
		if s.Ticker == "BAD" && (!strings.Contains(s.Reason, "2 in a row") || !strings.Contains(s.Reason, "timeout") || !strings.Contains(s.Reason, "next try")) {
			t.Errorf("a failure says how often it failed and when it is tried again: %q", s.Reason)
		}
		if s.Ticker == "OLD" && s.CountDay != "2026-01-02" {
			t.Errorf("the count date is shown: %+v", s)
		}
		if s.Ticker == "VAL" && (s.PriceDay != "2026-11-04" || s.Classes != 1 || s.Valued != 1) {
			t.Errorf("%+v", s)
		}
	}
}

func TestAFigureSaysWhetherItIsTheIssuersAndWhyItIsMissing(t *testing.T) {
	profiles := []universe.Profile{prof("VAL", "1"), prof("OLD", "2")}
	two := prof("DUAL.A", "3")
	two.Siblings = []string{"DUAL.B"}
	profiles = append(profiles, two)
	set := Set{ByCIK: map[string]Value{
		"1": valueOf(t, "1", "VAL", nil),
		"2": valueOf(t, "2", "OLD", func(o *Obs, _ *Mapping, _ *Market) { o.AsOf = "2026-01-02" }),
		"3": valueOf(t, "3", "DUAL.A", nil),
	}}
	got := Standings(profiles, set, map[string]Check{"1": {At: now}, "2": {At: now}, "3": {At: now}}, now)
	if g := got["VAL"]; g.State != StateValued || g.Scope != "" || g.PriceDay != "2026-11-04" || g.Currency != "USD" {
		t.Errorf("VAL = %+v", g)
	}
	if g := got["OLD"]; g.State != StateStale || g.Reason == "" || g.CountDay != "2026-01-02" {
		t.Errorf("OLD = %+v", g)
	}
	if g := got["DUAL.A"]; g.Scope != "issuer" {
		t.Errorf("an issuer with several classes shows the issuer's figure as such: %+v", g)
	}
}
