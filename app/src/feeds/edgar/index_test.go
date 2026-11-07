package edgar

import (
	"strings"
	"testing"
	"time"
)

const sampleIndex = `Description: Master Index

CIK|Company Name|Form Type|Date Filed|Filename
--------------------------------------------------------------------------------
320193|Apple Inc.|8-K|20261102|edgar/data/320193/0000320193-26-000100.txt
789019|MICROSOFT CORP|4/A|2026-11-02|edgar/data/789019/0001-not-an-accession.txt
1000001|ACME & SONS|10-Q|20261102|edgar/data/1000001/0001000001-26-000011.txt
`

func TestParseDailyIndexReadsFilingsAndCountsTheBadLines(t *testing.T) {
	got, bad, err := ParseDailyIndex([]byte(strings.Replace(sampleIndex, "0001-not-an-accession", "0000789019-26-000002", 1)))
	if err != nil || bad != 0 || len(got) != 3 {
		t.Fatalf("%+v %d %v", got, bad, err)
	}
	if got[0].CIK != "0000320193" || got[0].Accession != "0000320193-26-000100" || got[0].Filed != "2026-11-02" || got[1].Filed != "2026-11-02" {
		t.Errorf("%+v", got)
	}
	if _, bad, err := ParseDailyIndex([]byte(sampleIndex)); bad != 1 || err == nil {
		t.Errorf("one bad line of three is more than a tenth: bad %d, err %v", bad, err)
	}
	if _, _, err := ParseDailyIndex([]byte("not an index")); err == nil {
		t.Error("no table, no index")
	}
}

func TestDailyIndexURLIsInTheDaysQuarter(t *testing.T) {
	got := Config{WebBase: "https://x.test"}.DailyIndexURL(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
	if got != "https://x.test/Archives/edgar/daily-index/2026/QTR4/master.20261102.idx" {
		t.Error(got)
	}
}

func TestWantedKeepsAmendmentsOfWantedForms(t *testing.T) {
	es := []IndexEntry{{Form: "8-K"}, {Form: "8-K/A"}, {Form: "424B2"}, {Form: "4"}}
	if got := Wanted(es, []string{"8-K", "4"}); len(got) != 3 {
		t.Errorf("%+v", got)
	}
}
