package fundamentals

import (
	"math"
	"strings"
	"testing"
	"time"
)

const companyFacts = `{"cik":1000001,"facts":{"us-gaap":{
 "Revenues":{"units":{"USD":[
  {"start":"2024-01-01","end":"2024-12-31","val":100,"accn":"0001-25-000001","form":"10-K","filed":"2025-02-20"},
  {"start":"2024-01-01","end":"2024-12-31","val":110,"accn":"0001-25-000099","form":"10-K/A","filed":"2025-06-01"},
  {"start":"2024-01-01","end":"2024-03-31","val":25,"accn":"0001-24-000030","form":"10-Q","filed":"2024-05-01"},
  {"start":"2023-01-01","end":"2023-12-31","val":80,"accn":"0001-24-000002","form":"10-K","filed":"2024-02-20"}]}},
 "RevenueFromContractWithCustomerExcludingAssessedTax":{"units":{"USD":[
  {"start":"2024-01-01","end":"2024-12-31","val":999,"accn":"0001-25-000001","form":"10-K","filed":"2025-02-20"}]}},
 "Assets":{"units":{"USD":[{"end":"2024-12-31","val":500,"accn":"0001-25-000001","form":"10-K","filed":"2025-02-20"}]}}}}}`

func TestCompanyFactsKeepsEveryFilingOfAFigureAndOnlyYears(t *testing.T) {
	facts, err := parseCompanyFacts([]byte(companyFacts), "0001000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 5 {
		t.Fatalf("a quarter is not a year: %d facts %+v", len(facts), facts)
	}
	for _, f := range facts {
		if f.Accession == "" || f.Filed == "" || f.Concept == "" || f.Unit == "" {
			t.Errorf("a fact keeps where it comes from: %+v", f)
		}
	}
}

func TestAsOfShowsOnlyWhatHadBeenFiledAndTheRestatementOnlyAfterIt(t *testing.T) {
	facts, _ := parseCompanyFacts([]byte(companyFacts), "0001000001")
	rev := func(at string) float64 {
		d, _ := time.Parse(time.DateOnly, at)
		return Summarize(AsOf(facts, d))["0001000001"].Num[Revenue]
	}
	if got := rev("2024-12-31"); got != 80 {
		t.Errorf("before the 10-K only the year before is known: %v", got)
	}
	if got := rev("2025-03-01"); got != 100 {
		t.Errorf("after the 10-K, the preferred concept (Revenues, not the later-listed one): %v", got)
	}
	if got := rev("2025-02-20"); got == 100 {
		t.Errorf("on the filing day itself it was not yet counted public: %v", got)
	}
	if got := rev("2025-07-01"); got != 110 {
		t.Errorf("after the amendment the restated figure: %v", got)
	}
	r := Revisions(facts)
	if got := r["Revenues 2024-12-31"]; len(got) != 2 || got[0].Value != 100 || got[1].Value != 110 {
		t.Errorf("revisions, the amendment last: %+v", r)
	}
	if _, ok := r["RevenueFromContractWithCustomerExcludingAssessedTax 2024-12-31"]; ok {
		t.Error("one value in one filing is no revision")
	}
}

func TestAsOfLeavesOutFactsWithoutAFilingDay(t *testing.T) {
	facts := []Fact{{CIK: "1", Metric: Revenue, Start: "2024-01-01", End: "2024-12-31", Value: 5, Accession: "a"}}
	if got := AsOf(facts, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); len(got) != 0 {
		t.Errorf("a fact that cannot be placed in time was shown: %+v", got)
	}
}

const cover = `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:ix="http://www.xbrl.org/2013/inlineXBRL" xmlns:xbrli="http://www.xbrl.org/2003/instance" xmlns:xbrldi="http://xbrl.org/2006/xbrldi">
<body><div style="display:none"><ix:header><ix:resources>
<xbrli:context id="c1"><xbrli:entity><xbrli:segment><xbrldi:explicitMember dimension="us-gaap:StatementClassOfStockAxis">x:ClassAMember</xbrldi:explicitMember></xbrli:segment></xbrli:entity><xbrli:period><xbrli:instant>2026-10-20</xbrli:instant></xbrli:period></xbrli:context>
<xbrli:context id="c2"><xbrli:entity><xbrli:segment><xbrldi:explicitMember dimension="us-gaap:StatementClassOfStockAxis">x:ClassBMember</xbrldi:explicitMember></xbrli:segment></xbrli:entity><xbrli:period><xbrli:instant>2026-10-20</xbrli:instant></xbrli:period></xbrli:context>
<xbrli:context id="c3"><xbrli:entity><xbrli:segment><xbrldi:explicitMember dimension="us-gaap:StatementClassOfStockAxis">x:ClassAMember</xbrldi:explicitMember></xbrli:segment></xbrli:entity><xbrli:period><xbrli:startDate>2026-01-01</xbrli:startDate><xbrli:endDate>2026-09-30</xbrli:endDate></xbrli:period></xbrli:context>
</ix:resources></ix:header></div>
<p>Class A: <ix:nonFraction name="dei:EntityCommonStockSharesOutstanding" contextRef="c1" unitRef="shares" scale="3" format="ixt:num-dot-decimal">5,800,000</ix:nonFraction> &amp; Class B: <ix:nonFraction name="dei:EntityCommonStockSharesOutstanding" contextRef="c2" unitRef="shares" decimals="0">&nbsp;1,000</ix:nonFraction></p>
<ix:nonNumeric name="dei:TradingSymbol" contextRef="c3">XA</ix:nonNumeric>
</body></html>`

func TestParseCoverReadsOneCountPerClassWithItsSymbol(t *testing.T) {
	got, err := ParseCover([]byte(strings.Replace(cover, "ixt:num-dot-decimal", "ixt:numdotdecimal", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	a, b := got[0], got[1]
	if !strings.HasSuffix(a.Dimensions, "x:ClassAMember") || a.Shares != 5.8e9 || a.AsOf != "2026-10-20" || a.Ticker != "XA" {
		t.Errorf("class A: %+v", a)
	}
	if !strings.HasSuffix(b.Dimensions, "x:ClassBMember") || b.Shares != 1000 || b.Ticker != "" {
		t.Errorf("class B (no symbol listed, never guessed): %+v", b)
	}
}

func TestParseCoverReadsBothSpellingsOfTheNumberFormat(t *testing.T) {
	// The SEC's current filings write ixt:num-dot-decimal (found on a live cover page).
	got, err := ParseCover([]byte(cover))
	if err != nil || len(got) != 2 || got[0].Shares != 5.8e9 {
		t.Errorf("%+v %v", got, err)
	}
}

func TestParseCoverRefusesANumberItCannotRead(t *testing.T) {
	if _, err := ParseCover([]byte(strings.Replace(cover, "ixt:num-dot-decimal", "ixt-sec:numwordsen", 1))); err == nil {
		t.Error("an unknown number format must fail, not become a zero")
	}
}

func TestCapitaliseKeepsTheClassApartFromTheIssuer(t *testing.T) {
	classes := []ClassShares{{Dimensions: "a=A", Shares: 10, Ticker: "XA"}, {Dimensions: "a=B", Shares: 5}}
	price := func(t string) (float64, bool) { return 2, t == "XA" }
	c := Capitalise(classes, "XA", price)
	if !c.Classes[0].Priced || c.Classes[0].Cap != 20 || c.Classes[1].Priced {
		t.Errorf("classes: %+v", c.Classes)
	}
	if c.IssuerKnown || c.Issuer != 0 {
		t.Errorf("a class without a price makes the issuer value unknown, not estimated: %+v", c)
	}
	one := Capitalise([]ClassShares{{Shares: 10}}, "XA", price)
	if !one.IssuerKnown || math.Abs(one.Issuer-20) > 1e-9 {
		t.Errorf("one class is the issuer: %+v", one)
	}
}

func TestRecentFilingsAndTheirDocumentAddress(t *testing.T) {
	body := `{"filings":{"recent":{"accessionNumber":["0001-26-000005","0001-26-000004"],"form":["8-K","10-Q"],"filingDate":["2026-11-01","2026-10-25"],"reportDate":["","2026-09-30"],"primaryDocument":["a.htm","q.htm"]}}}`
	fs, err := RecentFilings([]byte(body))
	if err != nil || len(fs) != 2 || fs[0].Periodic() || !fs[1].Periodic() {
		t.Fatalf("%+v %v", fs, err)
	}
	if got := fs[1].DocumentURL("https://a.test/", "0001000001"); got != "https://a.test/1000001/000126000004/q.htm" {
		t.Errorf("address %q", got)
	}
}

func TestParseCoverKeepsTwoFactsForOneClassAndDateAsOneObservationPerValue(t *testing.T) {
	// The same class and date stated twice with the same count is two facts of one
	// observation; the store merges them. The parser reports both, never sums them.
	dup := strings.Replace(cover, `<ix:nonNumeric`, `<ix:nonFraction name="dei:EntityCommonStockSharesOutstanding" contextRef="c1" unitRef="shares" scale="3" format="ixt:num-dot-decimal">5,800,000</ix:nonFraction><ix:nonNumeric`, 1)
	got, err := ParseCover([]byte(dup))
	if err != nil {
		t.Fatal(err)
	}
	var a []ClassShares
	for _, c := range got {
		if strings.HasSuffix(c.Dimensions, "x:ClassAMember") {
			a = append(a, c)
		}
	}
	if len(a) != 2 || a[0].Shares != a[1].Shares || a[0].AsOf != a[1].AsOf {
		t.Errorf("both facts of class A are reported as they are: %+v", a)
	}
}
