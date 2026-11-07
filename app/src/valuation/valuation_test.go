package valuation

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

const (
	moogA = "us-gaap:StatementClassOfStockAxis=mog:CommonClassAMember"
	moogB = "us-gaap:StatementClassOfStockAxis=mog:CommonClassBMember"
	gA    = "us-gaap:StatementClassOfStockAxis=goog:CapitalClassAMember"
	gB    = "us-gaap:StatementClassOfStockAxis=goog:CapitalClassBMember"
	gC    = "us-gaap:StatementClassOfStockAxis=goog:CapitalClassCMember"
)

func ob(cik, acc, dims, ticker string, shares float64) Obs {
	return Obs{CIK: cik, Accession: acc, Form: "10-Q", Filed: "2026-10-30", AsOf: "2026-10-24", Dimensions: dims, Shares: shares, Ticker: ticker,
		Duplicates: 1, Received: time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)}
}

func market(prices map[string]float64, splits ...marketdata.Split) Market {
	m := Market{Prices: map[string]Price{}, Splits: map[string][]marketdata.Split{},
		Coverage: marketdata.SplitCoverage{Since: "2025-01-01", Read: now}}
	for t, p := range prices {
		m.Prices[marketdata.NormTicker(t)] = Price{Ticker: t, Day: "2026-11-04", Close: p, Currency: QuoteCurrency, Raw: true}
	}
	for _, s := range splits {
		m.Splits[marketdata.NormTicker(s.Ticker)] = append(m.Splits[marketdata.NormTicker(s.Ticker)], s)
	}
	return m
}

func maps(ms []Mapping) map[string]Mapping {
	out := map[string]Mapping{}
	for _, m := range ms {
		out[m.Dimensions] = m
	}
	return out
}

func pickOf(t *testing.T, obs ...Obs) Pick {
	t.Helper()
	p, ok := Select(obs, time.Time{})
	if !ok {
		t.Fatal("nothing selected")
	}
	return p
}

func TestMoogBothClassesListedGivesClassAndIssuerValues(t *testing.T) {
	p := pickOf(t, ob("1", "a-1", moogA, "MOG.A", 28_000_000), ob("1", "a-1", moogB, "MOG.B", 4_000_000))
	ls := []Listing{{"sec_a", "MOG.A", "stock"}, {"sec_b", "MOG.B", "stock"}, {"sec_w", "MOGW", "warrant"}}
	ms := MapClasses(p, ls, now)
	for _, m := range ms {
		if m.Status != Mapped || m.Method != MethodCoverSymbol || m.Basis == "" {
			t.Fatalf("both classes carry their symbol on the cover: %+v", m)
		}
	}
	v := Compute(p, maps(ms), market(map[string]float64{"MOG.A": 200, "MOG.B": 190}), now)
	if !v.IssuerOK {
		t.Fatalf("issuer must be valued: %s", v.IssuerReason)
	}
	if got, want := v.Issuer, 28e6*200+4e6*190; got != want {
		t.Errorf("issuer value %v, want %v", got, want)
	}
	if v.Classes[0].Cap != 28e6*200 || v.Classes[1].Cap != 4e6*190 {
		t.Errorf("class values %+v", v.Classes)
	}
}

func TestAlphabetUnlistedClassKeepsClassValuesAndWithholdsTheIssuer(t *testing.T) {
	p := pickOf(t, ob("2", "g-1", gA, "GOOGL", 5_800_000_000), ob("2", "g-1", gB, "", 860_000_000), ob("2", "g-1", gC, "GOOG", 5_500_000_000))
	ls := []Listing{{"sec_l", "GOOGL", "stock"}, {"sec_g", "GOOG", "stock"}}
	ms := maps(MapClasses(p, ls, now))
	if ms[gB].Status != Unmapped || ms[gB].SecurityID != "" {
		t.Fatalf("class B has no listing and must stay unmapped, never take another class's price: %+v", ms[gB])
	}
	v := Compute(p, ms, market(map[string]float64{"GOOGL": 180, "GOOG": 182}), now)
	if v.IssuerOK || v.Issuer != 0 || !strings.Contains(v.IssuerReason, "CapitalClassB") {
		t.Fatalf("the issuer total needs every class valued: %+v", v)
	}
	by := map[string]ClassValue{}
	for _, c := range v.Classes {
		by[c.Dimensions] = c
	}
	if !by[gA].OK || by[gA].Cap != 5.8e9*180 || !by[gC].OK || by[gC].Cap != 5.5e9*182 {
		t.Errorf("listed classes keep their own values: %+v", v.Classes)
	}
	if by[gB].OK || by[gB].Reason == "" {
		t.Errorf("class B must say why it has no value: %+v", by[gB])
	}
}

func TestAmbiguousMappingIsNotGuessed(t *testing.T) {
	// A class with no symbol and no letter, and two listings nobody claimed.
	p := pickOf(t, ob("3", "x-1", "ax=a:OneMember", "", 100), ob("3", "x-1", "ax=a:TwoMember", "", 50))
	ms := maps(MapClasses(p, []Listing{{"s1", "AAA", "stock"}, {"s2", "AAB", "stock"}}, now))
	for _, m := range ms {
		if m.Status != Ambiguous || m.SecurityID != "" || !strings.Contains(m.Basis, "AAA") {
			t.Fatalf("must be ambiguous and name the candidates: %+v", m)
		}
	}
	v := Compute(p, ms, market(map[string]float64{"AAA": 1, "AAB": 1}), now)
	if v.IssuerOK || !strings.Contains(v.IssuerReason, "cannot be told") {
		t.Errorf("an ambiguous mapping cannot be valued: %+v", v)
	}
}

func TestClassLetterMapsOnlyWhenOneListingCarriesIt(t *testing.T) {
	p := pickOf(t, ob("4", "y-1", "ax=a:ClassBMember", "", 10))
	one := MapClasses(p, []Listing{{"s1", "XYZ-A", "stock"}, {"s2", "XYZ-B", "stock"}}, now)[0]
	if one.Status != Mapped || one.Ticker != "XYZ-B" || one.Method != MethodClassSuffix || one.Confidence >= 0.95 {
		t.Errorf("one listing carries the letter: %+v", one)
	}
	two := MapClasses(p, []Listing{{"s1", "XYZ.B", "stock"}, {"s2", "XYZ-B", "stock"}}, now)[0]
	if two.Status != Ambiguous {
		t.Errorf("two listings carry the letter: %+v", two)
	}
}

func TestSoleListingOfASingleClassIssuer(t *testing.T) {
	p := pickOf(t, ob("5", "z-1", "", "", 1000))
	if m := MapClasses(p, []Listing{{"s1", "ONE", "stock"}}, now)[0]; m.Status != Mapped || m.Method != MethodSoleListing {
		t.Errorf("%+v", m)
	}
	if m := MapClasses(p, []Listing{{"s1", "ONE", "stock"}, {"s2", "ONEX", "stock"}}, now)[0]; m.Status != Ambiguous {
		t.Errorf("one count, two listings: %+v", m)
	}
	if m := MapClasses(p, []Listing{{"s1", "ONEW", "warrant"}}, now)[0]; m.Status != Unmapped {
		t.Errorf("a warrant is not the stock: %+v", m)
	}
}

func TestSplitBetweenCountAndPriceAlignsTheCount(t *testing.T) {
	p := pickOf(t, ob("6", "s-1", "", "", 100)) // counted 2026-10-24
	mp := maps([]Mapping{{Dimensions: "", Ticker: "SPL", SecurityID: "s", Status: Mapped}})
	// 2-for-1 on 2026-11-02: the count of 100 shares before it is 200 at the raw price after it.
	m := market(map[string]float64{"SPL": 10}, marketdata.Split{Ticker: "SPL", Day: "2026-11-02", From: 1, To: 2})
	v := Compute(p, mp, m, now)
	if !v.IssuerOK || v.Classes[0].Adjusted != 200 || v.Issuer != 2000 || v.Classes[0].SplitFactor != 2 {
		t.Fatalf("split-adjusted count: %+v", v)
	}
	// A reverse split 1-for-10 in the same place.
	m = market(map[string]float64{"SPL": 10}, marketdata.Split{Ticker: "SPL", Day: "2026-11-02", From: 10, To: 1})
	if v := Compute(p, mp, m, now); v.Classes[0].Adjusted != 10 {
		t.Errorf("reverse split: %+v", v.Classes[0])
	}
	// A split before the count is already in the count.
	m = market(map[string]float64{"SPL": 10}, marketdata.Split{Ticker: "SPL", Day: "2026-09-01", From: 1, To: 2})
	if v := Compute(p, mp, m, now); v.Classes[0].Adjusted != 100 {
		t.Errorf("an earlier split must not be applied: %+v", v.Classes[0])
	}
	// A split on the day of the price is in the raw price; the count before it is multiplied.
	m = market(map[string]float64{"SPL": 10}, marketdata.Split{Ticker: "SPL", Day: "2026-11-04", From: 1, To: 4})
	if v := Compute(p, mp, m, now); v.Classes[0].Adjusted != 400 {
		t.Errorf("split on the session day: %+v", v.Classes[0])
	}
}

func TestCountNewerThanThePriceIsBroughtBackAcrossASplit(t *testing.T) {
	o := ob("7", "n-1", "", "", 200)
	o.AsOf = "2026-11-04"
	p := pickOf(t, o)
	mp := maps([]Mapping{{Ticker: "NEW", SecurityID: "s", Status: Mapped}})
	m := market(map[string]float64{"NEW": 10}, marketdata.Split{Ticker: "NEW", Day: "2026-11-03", From: 1, To: 2})
	pr := m.Prices["NEW"]
	pr.Day = "2026-11-02"
	m.Prices["NEW"] = pr // the stored price is from before the split, the count after it
	if v := Compute(p, mp, m, now); !v.IssuerOK || v.Classes[0].Adjusted != 100 {
		t.Errorf("a count after the split against a price before it: %+v", v.Classes[0])
	}
}

func TestInputsThatCannotBeMatchedAreRejectedWithReasons(t *testing.T) {
	p := pickOf(t, ob("8", "r-1", "", "", 100))
	mp := maps([]Mapping{{Ticker: "REJ", SecurityID: "s", Status: Mapped}})
	base := func() Market { return market(map[string]float64{"REJ": 5}) }
	cases := []struct {
		name string
		mut  func(*Market, *map[string]Mapping, *time.Time)
		want string
	}{
		{"missing price", func(m *Market, _ *map[string]Mapping, _ *time.Time) { m.Prices = map[string]Price{} }, "no price"},
		{"adjusted price", func(m *Market, _ *map[string]Mapping, _ *time.Time) {
			p := m.Prices["REJ"]
			p.Raw = false
			m.Prices["REJ"] = p
		}, "adjusted for splits"},
		{"foreign currency", func(m *Market, _ *map[string]Mapping, _ *time.Time) {
			p := m.Prices["REJ"]
			p.Currency = "EUR"
			m.Prices["REJ"] = p
		}, "EUR"},
		{"no currency", func(m *Market, _ *map[string]Mapping, _ *time.Time) {
			p := m.Prices["REJ"]
			p.Currency = ""
			m.Prices["REJ"] = p
		}, "no currency"},
		{"stale price", func(_ *Market, _ *map[string]Mapping, n *time.Time) { *n = n.AddDate(0, 0, 20) }, "days old"},
		{"split list does not reach back", func(m *Market, _ *map[string]Mapping, _ *time.Time) { m.Coverage.Since = "2026-10-30" }, "split list does not cover"},
		{"split list is older than the price", func(m *Market, _ *map[string]Mapping, _ *time.Time) {
			m.Coverage.Read = time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
		}, "split list does not cover"},
		{"depositary receipt", func(_ *Market, mp *map[string]Mapping, _ *time.Time) {
			x := (*mp)[""]
			x.Type = "adr"
			(*mp)[""] = x
		}, "depositary receipt"},
		{"not yet mapped", func(_ *Market, mp *map[string]Mapping, _ *time.Time) { *mp = map[string]Mapping{} }, "not been mapped"},
	}
	for _, c := range cases {
		m, mps, n := base(), maps([]Mapping{mp[""]}), now
		c.mut(&m, &mps, &n)
		v := Compute(p, mps, m, n)
		if v.IssuerOK || v.Classes[0].OK || !strings.Contains(v.Classes[0].Reason, c.want) {
			t.Errorf("%s: want a rejection mentioning %q, got ok=%v reason=%q", c.name, c.want, v.Classes[0].OK, v.Classes[0].Reason)
		}
	}
}

func TestStaleCountIsRejected(t *testing.T) {
	o := ob("9", "o-1", "", "", 100)
	o.AsOf = "2026-01-02"
	v := Compute(pickOf(t, o), maps([]Mapping{{Ticker: "OLD", SecurityID: "s", Status: Mapped}}), market(map[string]float64{"OLD": 3}), now)
	if v.IssuerOK || !strings.Contains(v.Classes[0].Reason, "days old") {
		t.Errorf("%+v", v.Classes[0])
	}
}

func TestSelectDuplicatesConflictsAndAmendments(t *testing.T) {
	a := ob("1", "a-1", "", "", 100)
	dup := a
	dup.Duplicates = 2
	// The same count twice is one count.
	if p := pickOf(t, a, dup); len(p.Classes) != 1 || p.Classes[0].Issue() != "" {
		t.Errorf("duplicate contexts are not a conflict: %+v", p.Classes)
	}
	// Two counts for the same class and date in one filing are a conflict, and no count is used.
	b := a
	b.Shares = 150
	p := pickOf(t, a, b)
	if p.Classes[0].Issue() == "" || len(p.Classes[0].Conflict) != 2 {
		t.Fatalf("conflicting counts: %+v", p.Classes[0])
	}
	v := Compute(p, maps([]Mapping{{Ticker: "CF", SecurityID: "s", Status: Mapped}}), market(map[string]float64{"CF": 1}), now)
	if v.IssuerOK || !strings.Contains(v.Classes[0].Reason, "more than one count") {
		t.Errorf("a conflict is never resolved by picking one: %+v", v.Classes[0])
	}
	// An amendment filed later with another count for the same date replaces the original, and says so.
	am := ob("1", "a-2", "", "", 120)
	am.Form, am.Filed = "10-Q/A", "2026-11-02"
	p = pickOf(t, a, am)
	c := p.Classes[0]
	if p.Accession != "a-2" || c.Shares != 120 || !c.Revised || c.Was != 100 || c.WasAccession != "a-1" || !c.Amendment() {
		t.Errorf("amendment: %+v", c)
	}
	// A later count date wins over a later filing of an older date.
	newer := ob("1", "a-3", "", "", 90)
	newer.AsOf, newer.Filed = "2026-11-01", "2026-11-03"
	older := ob("1", "a-4", "", "", 80)
	older.AsOf, older.Filed = "2026-09-01", "2026-11-04"
	if p := pickOf(t, newer, older); p.Accession != "a-3" {
		t.Errorf("the newest count date wins: %s", p.Accession)
	}
}

func TestDroppedClassWithholdsTheIssuer(t *testing.T) {
	old := ob("1", "k-1", moogB, "MOG.B", 4)
	old.AsOf, old.Filed = "2026-07-20", "2026-07-30"
	p := pickOf(t, old, ob("1", "k-1", moogA, "MOG.A", 28), ob("1", "k-2", moogA, "MOG.A", 28))
	if len(p.Dropped) != 1 || p.Dropped[0] != moogB {
		t.Fatalf("the report before stated class B and this one does not: %+v", p.Dropped)
	}
	ms := maps([]Mapping{{Dimensions: moogA, Ticker: "MOG.A", SecurityID: "a", Status: Mapped}})
	if v := Compute(p, ms, market(map[string]float64{"MOG.A": 100}), now); v.IssuerOK || !strings.Contains(v.IssuerReason, "stated by the report before") {
		t.Errorf("%+v", v)
	}
}

func TestSelectIsAsOfAKnownTime(t *testing.T) {
	first := ob("1", "a-1", "", "", 100)
	second := ob("1", "a-2", "", "", 130)
	second.AsOf, second.Filed = "2026-11-02", "2026-11-03"
	second.Received = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	all := []Obs{first, second}
	for _, c := range []struct {
		name  string
		known time.Time
		acc   string
		ok    bool
	}{
		{"before anything was public", time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC), "", false},
		{"first stored and public", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), "a-1", true},
		{"second filed but only the day is known: not yet public until the day ends", time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), "a-1", true},
		{"second public but not yet stored", time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), "a-1", true},
		{"both known", time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), "a-2", true},
	} {
		p, ok := Select(all, c.known)
		if ok != c.ok || p.Accession != c.acc {
			t.Errorf("%s: %s %v", c.name, p.Accession, ok)
		}
	}
	// An acceptance time makes the filing public at that time, not at the end of the day.
	second.Accepted = time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)
	second.Received = time.Date(2026, 11, 3, 14, 5, 0, 0, time.UTC)
	if p, _ := Select([]Obs{first, second}, time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)); p.Accession != "a-2" {
		t.Errorf("an acceptance time is used when it is known: %s", p.Accession)
	}
}

func TestClassName(t *testing.T) {
	for in, want := range map[string]string{"": "all shares", gA: "CapitalClassA", moogB + ";x:Y=z:OtherMember": "CommonClassB / Other"} {
		if got := ClassName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
