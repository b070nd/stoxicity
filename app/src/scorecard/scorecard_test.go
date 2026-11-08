package scorecard

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

func d(metric, end, value string, extra ...string) Datum {
	x := Datum{Security: "ACME", Metric: metric, End: end, Currency: "EUR", Value: money.MustDecimal(value), Source: "company-report", Ref: "AR " + end[:4], Filed: ""}
	if metrics[metric].flow {
		y := end[:4]
		x.Start = y + "-01-01"
	}
	if metric == Shares {
		x.Currency = ""
	}
	for i := 0; i+1 < len(extra); i += 2 {
		switch extra[i] {
		case "currency":
			x.Currency = money.Currency(extra[i+1])
		case "filed":
			x.Filed = extra[i+1]
		case "source":
			x.Source = extra[i+1]
		case "start":
			x.Start = extra[i+1]
		}
	}
	return x
}

func year(y string, rev, ni, eq string) []Datum {
	e := y + "-12-31"
	next := fmt.Sprint(mustYear(y)+1) + "-03-01"
	return []Datum{d(Revenue, e, rev, "filed", next), d(NetIncome, e, ni, "filed", next), d(Equity, e, eq, "filed", next)}
}

func data() []Datum {
	var ds []Datum
	ds = append(ds, year("2023", "1000", "100", "500")...)
	ds = append(ds, year("2024", "1100", "121", "550")...)
	ds = append(ds, year("2025", "1210", "133", "600")...)
	return ds
}

func build(t *testing.T, tpl string, ds []Datum, notes []Annotation, known time.Time) Card {
	t.Helper()
	c, err := Build(Input{Security: "ACME", Template: tpl, Data: ds, Notes: notes, KnownAt: known})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func cell(c Card, i int, r string) Cell { return c.Years[i].Ratios[r] }

func TestMultiYearRatiosAreExactAndSourced(t *testing.T) {
	c := build(t, "ordinary", data(), nil, time.Time{})
	if len(c.Years) != 3 || c.Years[0].End != "2025-12-31" || c.Years[2].End != "2023-12-31" {
		t.Fatalf("%+v", c.Years)
	}
	if v := cell(c, 0, NetMargin); !v.Has() || v.Value.String() != "0.1099" {
		t.Fatalf("net margin %+v", v)
	}
	if v := cell(c, 0, ROE); v.Value.String() != "0.2217" {
		t.Fatalf("roe %+v", v)
	}
	if v := cell(c, 0, RevenueGrowth); v.Value.String() != "0.1000" {
		t.Fatalf("growth %+v", v)
	}
	if v := cell(c, 2, RevenueGrowth); v.Status != NoPriorYear {
		t.Fatalf("oldest year has no prior: %+v", v)
	}
	if len(cell(c, 0, NetMargin).Inputs) != 2 {
		t.Fatalf("a ratio names the figures it came from: %+v", cell(c, 0, NetMargin))
	}
	// missing inputs stay visible, with the reason
	if v := cell(c, 0, DebtToEquity); v.Status != Missing || !strings.Contains(v.Reason, "total_debt") {
		t.Fatalf("%+v", v)
	}
	if len(c.Gaps) == 0 || !strings.Contains(strings.Join(c.Gaps, ";"), "operating_cash_flow") {
		t.Fatalf("%v", c.Gaps)
	}
}

func TestUnsupportedBusinessesGetNoOrdinaryRatios(t *testing.T) {
	for tpl, allowed := range map[string][]string{
		"bank": {ROE, EquityToAssets, PayoutRatio}, "insurer": {ROE, EquityToAssets, PayoutRatio},
		"reit": {RevenueGrowth, InterestCover, EquityToAssets}, "fund": nil, "unknown": nil,
	} {
		c := build(t, tpl, data(), nil, time.Time{})
		for _, r := range Ratios {
			got := cell(c, 0, r)
			in := false
			for _, a := range allowed {
				in = in || a == r
			}
			if !in && (got.Status != Unsupported || got.Has()) {
				t.Errorf("%s/%s: %+v", tpl, r, got)
			}
			if in && got.Status == Unsupported {
				t.Errorf("%s/%s should be computed", tpl, r)
			}
		}
	}
	c := build(t, "reit", data(), nil, time.Time{})
	if r := cell(c, 0, PayoutRatio); !strings.Contains(r.Reason, "FFO") {
		t.Fatalf("a REIT's dividend is judged by FFO: %+v", r)
	}
	if _, err := Build(Input{Security: "ACME", Template: "spaceship"}); err == nil {
		t.Fatal("unknown template")
	}
}

func TestDenominatorPeriodAndCurrencyProblemsAreNamed(t *testing.T) {
	ds := data()
	// negative equity: ROE would be meaningless
	ds = append(ds, d(Equity, "2026-12-31", "-50", "filed", "2027-03-01"), d(NetIncome, "2026-12-31", "10", "filed", "2027-03-01"), d(Revenue, "2026-12-31", "0", "filed", "2027-03-01"))
	c := build(t, "ordinary", ds, nil, time.Time{})
	if got := cell(c, 0, ROE); got.Status != NegativeDenominator {
		t.Fatalf("%+v", got)
	}
	if got := cell(c, 0, NetMargin); got.Status != ZeroDenominator {
		t.Fatalf("%+v", got)
	}
	if got := cell(c, 0, RevenueGrowth); !got.Has() || got.Value.String() != "-1.0000" {
		t.Fatalf("revenue falling to zero is a 100%% fall: %+v", got)
	}
	if got := cell(c, 1, RevenueGrowth); !got.Has() {
		t.Fatalf("%+v", got)
	}

	// another currency for one input: no rate is applied
	ds = append(data(), d(TotalDebt, "2025-12-31", "300", "currency", "USD", "filed", "2026-03-01"))
	c = build(t, "ordinary", ds, nil, time.Time{})
	if got := cell(c, 0, DebtToEquity); got.Status != CurrencyMismatch || !strings.Contains(got.Reason, "no rate") {
		t.Fatalf("%+v", got)
	}
	// revenue reported in another currency a year earlier
	ds = data()
	ds[0] = d(Revenue, "2023-12-31", "1000", "currency", "USD", "filed", "2024-03-01")
	ds[3] = d(Revenue, "2024-12-31", "1100", "currency", "USD", "filed", "2025-03-01")
	c = build(t, "ordinary", ds, nil, time.Time{})
	if got := cell(c, 0, RevenueGrowth); got.Status != CurrencyMismatch {
		t.Fatalf("%+v", got)
	}
	// only a quarter is known for the year end
	q := d(Revenue, "2026-12-31", "300", "start", "2026-10-01", "filed", "2027-02-01")
	ds = append(data(), q, d(NetIncome, "2026-12-31", "30", "filed", "2027-02-01"))
	c = build(t, "ordinary", ds, nil, time.Time{})
	if got := c.Years[0].Raw[Revenue]; got.Status != PeriodMismatch {
		t.Fatalf("%+v", got)
	}
	// a transition period is not accepted at all
	bad := d(Revenue, "2026-06-30", "10", "start", "2026-01-01")
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "days") {
		t.Fatalf("%v", err)
	}
	// free cash flow: operating cash flow less capex, in one currency
	ds = append(data(), d(OperatingCashFlow, "2025-12-31", "200", "filed", "2026-03-01"), d(Capex, "2025-12-31", "50", "filed", "2026-03-01"))
	c = build(t, "ordinary", ds, nil, time.Time{})
	if got := cell(c, 0, FCFMargin); !got.Has() || got.Value.String() != "0.1240" {
		t.Fatalf("%+v", got)
	}
	if got := cell(c, 0, FCFConversion); got.Value.String() != "1.1278" {
		t.Fatalf("%+v", got)
	}
}

func TestPointInTimeReadingIgnoresLaterFilingsAndUndatedFigures(t *testing.T) {
	ds := data()
	// the 2025 revenue is restated after the fact
	ds = append(ds, d(Revenue, "2025-12-31", "1300", "filed", "2026-08-01", "source", "company-report"))
	now := build(t, "ordinary", ds, nil, time.Time{})
	if now.Years[0].Raw[Revenue].Value.String() != "1300" {
		t.Fatalf("%+v", now.Years[0].Raw[Revenue])
	}
	then := build(t, "ordinary", ds, nil, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if then.Years[0].Raw[Revenue].Value.String() != "1210" {
		t.Fatalf("a restatement filed later must not leak: %+v", then.Years[0].Raw[Revenue])
	}
	if now.Hash == then.Hash {
		t.Fatal("different inputs, same hash")
	}
	// a year none of whose figures had been filed yet does not exist as of that day
	early := build(t, "ordinary", ds, nil, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	if len(early.Years) != 2 || early.Years[0].End != "2024-12-31" {
		t.Fatalf("2025 was filed in 2026: %d years, newest %s", len(early.Years), early.Years[0].End)
	}
	// an undated figure cannot be shown as of a day
	u := []Datum{d(Revenue, "2025-12-31", "5"), d(NetIncome, "2025-12-31", "1", "filed", "2026-03-01")}
	c := build(t, "ordinary", u, nil, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if got := c.Years[0].Raw[Revenue]; got.Status != NotYetFiled {
		t.Fatalf("%+v", got)
	}
	// two sources that disagree on the same day are a conflict, not an average
	cf := append(data(), d(Revenue, "2025-12-31", "1215", "filed", "2026-03-01", "source", "aggregator"))
	c = build(t, "ordinary", cf, nil, time.Time{})
	if got := c.Years[0].Raw[Revenue]; got.Status != Conflicting {
		t.Fatalf("%+v", got)
	}
}

func TestProvenanceIsRequired(t *testing.T) {
	x := d(Revenue, "2025-12-31", "10")
	x.Ref = ""
	if err := x.Validate(); err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("%v", err)
	}
	x = d(Revenue, "2025-12-31", "10")
	x.Currency = ""
	if err := x.Validate(); err == nil {
		t.Fatal("money without a currency")
	}
	x = d(Capex, "2025-12-31", "-5")
	if err := x.Validate(); err == nil || !strings.Contains(err.Error(), "positive amount") {
		t.Fatalf("%v", err)
	}
	x = d(Revenue, "2025-12-31", "10", "filed", "2025-06-30")
	if err := x.Validate(); err == nil {
		t.Fatal("filed before the period ended")
	}
	x = d(Shares, "2025-12-31", "10")
	x.Currency = "EUR"
	if err := x.Validate(); err == nil {
		t.Fatal("a count with a currency")
	}
	if err := d(Metrics()[0], "2025-12-31", "1").Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(Input{Security: "ACME", Template: "ordinary", Data: []Datum{x}}); err == nil {
		t.Fatal("an invalid figure must stop the build")
	}
}

func TestVersionsAndNotesAreInTheContentHash(t *testing.T) {
	n := Annotation{Security: "ACME", Topic: "risk", Stance: Counter, Text: "A class action about the 2024 restatement and covenant waivers.", Source: "analyst", Ref: "memo 12", At: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)}
	a := build(t, "ordinary", data(), nil, time.Time{})
	b := build(t, "ordinary", data(), []Annotation{n}, time.Time{})
	if a.Hash == b.Hash {
		t.Fatal("a note must change the hash")
	}
	if got := strings.Join(b.Notes[0].Flags, ","); got != "litigation,refinancing,restatement" {
		t.Fatalf("flags %s", got)
	}
	if a2 := build(t, "ordinary", data(), nil, time.Time{}); a2.Hash != a.Hash {
		t.Fatal("same inputs, same hash")
	}
	n.Topic = "astrology"
	if _, err := Build(Input{Security: "ACME", Template: "ordinary", Notes: []Annotation{n}}); err == nil {
		t.Fatal("unknown topic")
	}
	n.Topic, n.Ref = "risk", ""
	if err := n.Validate(); err == nil {
		t.Fatal("a note without a reference")
	}
	v := CurrentVersions()
	for _, s := range []string{v.Extractor, v.Rules, v.Keywords} {
		if !strings.HasPrefix(s, "scorecard-") {
			t.Fatalf("%+v", v)
		}
	}
	if !strings.Contains(v.Keywords, KeywordDigest()) || !strings.Contains(v.Rules, rulesDigest()) {
		t.Fatalf("%+v", v)
	}

	// a different keyword table is a different version
	saved := keywordTable
	defer func() { keywordTable = saved }()
	d0 := KeywordDigest()
	keywordTable = append(append([]keyword(nil), keywordTable...), keyword{"new_theme", []string{"foo"}})
	if KeywordDigest() == d0 {
		t.Fatal("the keyword table digest did not change")
	}
	if c := build(t, "ordinary", data(), nil, time.Time{}); c.Hash == a.Hash {
		t.Fatal("a changed keyword table must change the card hash")
	}
}

func mustYear(y string) int {
	n, err := strconv.Atoi(y)
	if err != nil {
		panic(err)
	}
	return n
}

func TestParseCSVReportsEveryProblemAndAcceptsNothingOnError(t *testing.T) {
	ds, err := ParseCSV(strings.NewReader(ExampleCSV))
	if err != nil || len(ds) != 3 || ds[2].Metric != Equity {
		t.Fatalf("%v %v", err, ds)
	}
	bad := "security,metric,start,end,currency,value,source,ref,filed,note\n" +
		"s,revenue,2025-01-01,2025-12-31,EUR,abc,x,y,,\n" +
		"s,revenue,2025-01-01,2025-12-31,EUR,1,,y,,\n" +
		"s,bogus,,2025-12-31,EUR,1,x,y,,\n"
	_, err = ParseCSV(strings.NewReader(bad))
	if err == nil || !strings.Contains(err.Error(), "3 problem(s)") || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("%v", err)
	}
	if _, err := ParseCSV(strings.NewReader("security,metric\n")); err == nil {
		t.Fatal("missing columns")
	}
}
