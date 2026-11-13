package eventfact

// synthetic fixture, not verified against live data

import (
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

func ptr(v float64) *float64 { return &v }

func trade(acc, form, code, filed string, shares, price, after float64) model.InsiderTrade {
	return model.InsiderTrade{Accession: acc, Seq: 1, CIK: "0000000001", Owner: "Jane Roe", OwnerCIK: "0000000002",
		Role: "Chief Financial Officer", Security: "Common Stock", Form: form, Period: "2026-03-02", Date: "2026-03-02",
		Filed: filed, Code: code, Shares: shares, Price: price, After: after, Direct: true}
}

func TestFromInsider(t *testing.T) {
	securities := map[string]SecurityRef{"0000000001": {SecurityID: "sec1", Symbol: "ABC"}}
	accepted := time.Date(2026, 3, 4, 21, 30, 0, 0, time.UTC)
	timed := map[string]FilingTime{"a1": {Accepted: accepted, Precision: PrecisionTime}}
	grant := trade("a1", "4", "A", "2026-03-04", 100, 0, 1000)
	cases := []struct {
		name      string
		trades    []model.InsiderTrade
		times     map[string]FilingTime
		wantTypes []Type
		check     func(t *testing.T, rs []Record)
	}{
		{"buy and sale make two records", []model.InsiderTrade{trade("a1", "4", "P", "2026-03-04", 100, 10, 1100), trade("a1", "4", "S", "2026-03-04", 50, 12, 950)},
			timed, []Type{InsiderBuy, InsiderSell}, func(t *testing.T, rs []Record) {
				if rs[0].Precision != PrecisionTime || !rs[0].AvailableAt.Equal(accepted) {
					t.Errorf("want time precision %v, got %s %v", accepted, rs[0].Precision, rs[0].AvailableAt)
				}
				if f, _ := rs[0].Fact("pct_of_holding"); f.Value != 0.1 {
					t.Errorf("pct_of_holding = %v, want 0.1", f.Value)
				}
			}},
		{"a grant makes none", []model.InsiderTrade{grant}, timed, nil, nil},
		{"amendment replaces figures, keeps earlier availability", []model.InsiderTrade{trade("a1", "4", "P", "2026-03-04", 100, 10, 1100), trade("a2", "4/A", "P", "2026-03-10", 80, 10, 1080)},
			nil, []Type{InsiderBuy}, func(t *testing.T, rs []Record) {
				r := rs[0]
				if f, _ := r.Fact("shares"); f.Value != 80 {
					t.Errorf("shares = %v, want 80", f.Value)
				}
				if r.SourceRef != "a2" || len(r.Evidence) != 2 {
					t.Errorf("source %s evidence %v", r.SourceRef, r.Evidence)
				}
				if r.Precision != PrecisionDay || !r.AvailableAt.Equal(time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)) {
					t.Errorf("availability %v %s", r.AvailableAt, r.Precision)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := FromInsider(c.trades, securities, c.times, nil)
			if len(rs) != len(c.wantTypes) {
				t.Fatalf("got %d records, want %d", len(rs), len(c.wantTypes))
			}
			got := map[Type]bool{}
			for _, r := range rs {
				got[r.Type] = true
			}
			for _, w := range c.wantTypes {
				if !got[w] {
					t.Errorf("missing %s", w)
				}
			}
			if c.check != nil {
				c.check(t, rs)
			}
		})
	}
}

func TestFromResults(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n) }
	ev := func(id string, n int) EventText {
		return EventText{EventID: id, SecurityID: "sec1", Symbol: "ABC", CIK: "0000000001", Kind: model.EventResults,
			PublishedAt: day(n), AvailableAt: day(n), Precision: PrecisionDay, Source: SourceCompany, SourceRef: "ref-" + id,
			Evidence: []string{"ref-" + id}, Basis: Known, Passages: []Passage{{Ref: id, Text: "results beat"}}}
	}
	events := []EventText{ev("e3", 100), ev("e2", 5), ev("e1", 0)}
	var gotDay string
	figures := func(cik, d string) (Figures, bool) {
		if gotDay == "" {
			gotDay = d
		}
		return Figures{Period: "Q4 2025", Currency: "USD", Revenue: ptr(1000), EPSYoY: ptr(0.05)}, true
	}
	surprise := func(p []Passage) (Type, []Fact, bool) {
		return EarningsBeat, []Fact{{Name: "eps_surprise", Value: 0.1, Unit: "ratio"}, {Name: "revenue", Value: 1, Unit: "USD"}, {Name: "bogus", Value: 1, Unit: "x"}}, len(p) > 0
	}
	rs := FromResults(events, figures, surprise)
	if len(rs) != 2 {
		t.Fatalf("got %d records, want 2", len(rs))
	}
	r := rs[0]
	if r.Key != "earnings|sec1|2026-01-10" || r.Type != EarningsBeat || len(r.Evidence) != 2 {
		t.Errorf("first record: key %s type %s evidence %v", r.Key, r.Type, r.Evidence)
	}
	if gotDay != "2026-01-15" {
		t.Errorf("figures asked for day %s, want 2026-01-15", gotDay)
	}
	if f, ok := r.Fact("revenue"); !ok || f.Value != 1000 || f.Period != "Q4 2025" {
		t.Errorf("revenue fact %+v", f)
	}
	if _, ok := r.Fact("eps_surprise"); !ok {
		t.Error("missing eps_surprise")
	}
	if _, ok := r.Fact("bogus"); ok {
		t.Error("unknown fact kept")
	}
	if plain := FromResults(events, nil, nil); len(plain) != 2 || plain[0].Type != EarningsReported {
		t.Errorf("without helpers: %+v", plain)
	}
}

func TestFromResultsGivesMarginsAndCashFlowOnlyWhenKnown(t *testing.T) {
	ev := EventText{EventID: "e1", SecurityID: "sec1", Symbol: "ABC", CIK: "0000000001", Kind: model.EventResults,
		PublishedAt: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC), AvailableAt: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
		Precision: PrecisionDay, Source: SourceCompany, SourceRef: "ref-e1", Evidence: []string{"ref-e1"}, Basis: Known}
	cases := []struct {
		name    string
		figures Figures
		want    map[string]string // name to unit
		derived map[string]bool
	}{
		{"all", Figures{Period: "FY2025", Currency: "USD", NetMargin: ptr(0.1), OperatingMargin: ptr(0.25), OperatingCashFlow: ptr(80), CashFlowMargin: ptr(0.4)},
			map[string]string{"net_margin": "ratio", "operating_margin": "ratio", "operating_cash_flow": "USD", "cash_flow_margin": "ratio"},
			map[string]bool{"net_margin": true, "operating_margin": true, "operating_cash_flow": false, "cash_flow_margin": true}},
		{"only net margin", Figures{Period: "FY2025", Currency: "USD", Revenue: ptr(10), NetMargin: ptr(0.1)},
			map[string]string{"revenue": "USD", "net_margin": "ratio"}, map[string]bool{"net_margin": true}},
		{"none", Figures{Period: "FY2025", Currency: "USD"}, map[string]string{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := FromResults([]EventText{ev}, func(string, string) (Figures, bool) { return c.figures, true }, nil)
			if len(rs) != 1 {
				t.Fatalf("got %d records, want 1 (Validate must accept the facts)", len(rs))
			}
			if err := rs[0].Validate(); err != nil {
				t.Fatal(err)
			}
			if len(rs[0].Facts) != len(c.want) {
				t.Fatalf("facts %+v, want %v", rs[0].Facts, c.want)
			}
			for name, unit := range c.want {
				f, ok := rs[0].Fact(name)
				if !ok || f.Unit != unit || f.Period != "FY2025" || f.Derived != c.derived[name] {
					t.Errorf("%s = %+v, %v", name, f, ok)
				}
			}
		})
	}
}
