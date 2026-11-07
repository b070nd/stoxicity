package fundamentals

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/universe"
)

var now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func frame(rows ...string) httpx.Response {
	body := `{"data":[`
	for i, r := range rows {
		if i > 0 {
			body += ","
		}
		body += r
	}
	return httpx.Response{Status: 200, Body: []byte(body + "]}")}
}

func dur(cik int, start, end string, val float64) string {
	return fmt.Sprintf(`{"cik":%d,"start":%q,"end":%q,"val":%v}`, cik, start, end, val)
}

func at(cik int, end string, val float64) string {
	return fmt.Sprintf(`{"cik":%d,"end":%q,"val":%v}`, cik, end, val)
}

// scripted answers every frame with "not published" except those given.
func scripted(answers map[string]httpx.Response, failures ...string) (*httpx.Fake, Fetcher) {
	f := httpx.NewFake()
	for _, s := range sources {
		var periods []string
		if s.instant {
			periods = quarterEnds(now)
		} else {
			for _, y := range annualYears(now) {
				periods = append(periods, fmt.Sprintf("CY%d", y))
			}
		}
		for _, p := range periods {
			u := fmt.Sprintf("https://frames.test/%s/%s/%s/%s.json", s.taxonomy, s.concept, s.unit, p)
			if slices.Contains(failures, s.concept+"/"+p) {
				f.Fail(u, &httpx.Error{Class: httpx.Blocked, Status: 403, URL: u})
			} else if r, ok := answers[s.concept+"/"+p]; ok {
				f.Reply(u, r)
			} else {
				f.Fail(u, &httpx.Error{Class: httpx.Permanent, Status: 404, URL: u})
			}
		}
	}
	return f, Fetcher{Client: f, URL: "https://frames.test/", Now: func() time.Time { return now }}
}

func TestPeriodsAskedAreTheLastYearsAndQuarters(t *testing.T) {
	if got := fmt.Sprint(annualYears(now)); got != "[2024 2025 2026]" {
		t.Errorf("years %s", got)
	}
	if got := fmt.Sprint(quarterEnds(now)); got != "[CY2026Q3I CY2026Q2I CY2026Q1I CY2025Q4I CY2025Q3I CY2025Q2I]" {
		t.Errorf("quarters %s", got)
	}
	if got := fmt.Sprint(quarterEnds(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))); got != "[CY2025Q4I CY2025Q3I CY2025Q2I CY2025Q1I CY2024Q4I CY2024Q3I]" {
		t.Errorf("quarters in the first quarter %s", got)
	}
}

func TestFetchKeepsYearsOnlyAndTheFirstConceptOfACompany(t *testing.T) {
	_, fe := scripted(map[string]httpx.Response{
		// Apple's fiscal year ends in September; a 9-month period is no year.
		"Revenues/CY2026": frame(dur(320193, "2025-09-29", "2026-09-26", 416), dur(5, "2026-01-01", "2026-09-30", 7)),
		// The second concept only fills what the first did not report.
		"RevenueFromContractWithCustomerExcludingAssessedTax/CY2026": frame(dur(320193, "2025-09-29", "2026-09-26", 999), dur(77, "2026-01-01", "2026-12-31", 12)),
	})
	facts, rep, err := fe.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, f := range facts {
		got[f.CIK+" "+f.Metric+" "+f.End] = f.Value
	}
	if len(got) != 2 || got["0000320193 revenue 2026-09-26"] != 416 || got["0000000077 revenue 2026-12-31"] != 12 {
		t.Errorf("facts %v", got)
	}
	if rep.Requests != 45 || rep.Missing != 43 || rep.Companies != 2 {
		t.Errorf("report %+v", rep)
	}
}

func TestFetchStopsOnAFailureThatIsNotAMissingFrame(t *testing.T) {
	_, fe := scripted(nil, "Revenues/CY2025")
	if _, _, err := fe.Fetch(context.Background()); err == nil {
		t.Error("a blocked request must end the fetch")
	}
}

func fact(metric, start, end string, v float64) Fact {
	return Fact{CIK: "0000000001", Metric: metric, Start: start, End: end, Value: v}
}

func TestSummarizeReadsGrowthMarginBalanceAndShares(t *testing.T) {
	s, ok := summarize([]Fact{
		fact(Revenue, "2024-10-01", "2025-09-30", 100),
		fact(Revenue, "2025-10-01", "2026-09-30", 120),
		fact(NetIncome, "2025-10-01", "2026-09-30", 24),
		fact(Assets, "", "2026-09-15", 500), // two weeks off the year-end: the nearest
		fact(Assets, "", "2025-09-30", 400),
		fact(Equity, "", "2026-09-30", 200),
		fact(Shares, "", "2026-10-20", 1000),
		fact(Shares, "", "2026-07-20", 990),
		fact(AvgShares, "2025-10-01", "2026-09-30", 5),
	})
	if !ok || s.End != "2026-09-30" {
		t.Fatalf("%+v %v", s, ok)
	}
	want := map[string]float64{Revenue: 120, NetIncome: 24, "growth": 0.2, "margin": 0.2, Assets: 500, Equity: 200, "roe": 0.12, "equity_ratio": 0.4, Shares: 1000}
	for k, v := range want {
		if got, has := s.Num[k]; !has || math.Abs(got-v) > 1e-9 {
			t.Errorf("%s = %v (%v), want %v", k, got, has, v)
		}
	}
	if mc, ok := s.MarketCap(10); !ok || mc != 10000 {
		t.Errorf("market cap %v %v", mc, ok)
	}
	if _, ok := s.MarketCap(0); ok {
		t.Error("no price, no market cap")
	}
}

func TestSummarizeLeavesOutWhatTheFilingsDoNotSay(t *testing.T) {
	// Income of a year the revenue is not for: the newer one is kept alone (no
	// revenue of another year beside it, so no margin). Balance 3 months away from
	// the year: not the year's balance. Average shares are kept apart and never
	// stand in for the cover page count.
	s, ok := summarize([]Fact{
		fact(Revenue, "2024-01-01", "2024-12-31", 100),
		fact(NetIncome, "2025-01-01", "2025-12-31", 10),
		fact(Equity, "", "2025-03-31", 50),
		fact(AvgShares, "2025-01-01", "2025-12-31", 8),
	})
	if !ok {
		t.Fatal("expected a summary")
	}
	for _, k := range []string{"margin", "growth", "roe", Equity, Revenue, Shares} {
		if _, has := s.Num[k]; has {
			t.Errorf("%s should be unknown: %v", k, s.Num)
		}
	}
	if s.Num[AvgShares] != 8 || s.Num[NetIncome] != 10 || s.End != "2025-12-31" {
		t.Errorf("%+v", s)
	}
	if _, ok := summarize(nil); ok {
		t.Error("no facts, no summary")
	}
}

func TestFiguresAreForCommonStockOnlyAndMarketCapNeedsUSGAAP(t *testing.T) {
	sums := Summarize([]Fact{
		{CIK: "0000000001", Metric: Revenue, End: "2025-12-31", Start: "2025-01-01", Value: 100},
		{CIK: "0000000001", Metric: Shares, End: "2026-01-31", Value: 10},
		{CIK: "0000000002", Metric: Shares, End: "2026-01-31", Value: 1000}, // a foreign issuer: cover page only
	})
	profiles := []universe.Profile{
		{Ticker: "AAA", CIK: "0000000001", Kind: universe.KindStock},
		{Ticker: "AAAW", CIK: "0000000001", Kind: universe.KindStock, Security: "AAA - Warrant"},
		{Ticker: "FFF", CIK: "0000000002", Kind: universe.KindStock},
	}
	got := Figures(sums, profiles, map[string]float64{"AAA": 5, "AAAW": 1, "FFF": 3})
	if got["revenue"]["AAA"] != 100 || got["marketcap"]["AAA"] != 50 {
		t.Errorf("%v", got)
	}
	if _, ok := got["revenue"]["AAAW"]; ok {
		t.Error("a warrant has no revenue")
	}
	if _, ok := got["marketcap"]["FFF"]; ok {
		t.Error("shares of a foreign issuer say nothing of its market value")
	}
	if len(Figures(nil, profiles, nil)) != 0 {
		t.Error("no sums, no figures")
	}
}

type memStore struct {
	mu    sync.Mutex
	facts []Fact
	read  time.Time
}

func (m *memStore) PutFundamentals(_ context.Context, facts []Fact, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.facts, m.read = facts, at
	return nil
}

func (m *memStore) FundamentalsRead(context.Context) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.read, nil
}

func TestKeeperReadsOnceAWeekUnlessForced(t *testing.T) {
	f, fe := scripted(map[string]httpx.Response{"Revenues/CY2026": frame(dur(1, "2026-01-01", "2026-12-31", 5))})
	st := &memStore{}
	clock := now
	k := &Keeper{Store: st, Fetcher: fe, Now: func() time.Time { return clock }}
	if _, ran, err := k.Refresh(context.Background(), false); err != nil || !ran || len(st.facts) != 1 {
		t.Fatalf("first reading: %v %v %v", ran, err, st.facts)
	}
	n := len(f.Calls())
	clock = now.Add(3 * 24 * time.Hour)
	if _, ran, _ := k.Refresh(context.Background(), false); ran || len(f.Calls()) != n {
		t.Error("a young reading is not made again")
	}
	if _, ran, _ := k.Refresh(context.Background(), true); !ran || len(f.Calls()) == n {
		t.Error("force reads again")
	}
	clock = now.Add(10 * 24 * time.Hour)
	if _, ran, _ := k.Refresh(context.Background(), false); !ran {
		t.Error("an old reading is made again")
	}
}

func TestRevenueAndIncomeAreOfTheSameYearAndAClassOfSharesIsNotTheCompany(t *testing.T) {
	s, _ := summarize([]Fact{
		fact(Revenue, "2025-01-01", "2025-12-31", 200), fact(Revenue, "2024-01-01", "2024-12-31", 100),
		fact(NetIncome, "2024-01-01", "2024-12-31", 20), // 2025 income is not reported yet
	})
	if s.End != "2024-12-31" || s.Num[Revenue] != 100 || s.Num[NetIncome] != 20 || s.Num["margin"] != 0.2 {
		t.Errorf("the newest year both are reported for is 2024: %+v", s)
	}

	sums := map[string]Summary{"0000000001": {Num: map[string]float64{Revenue: 100, NetIncome: 10, Shares: 1e6}}}
	profiles := []universe.Profile{
		{Ticker: "AAA", CIK: "0000000001", Kind: universe.KindStock},
		{Ticker: "AAB", CIK: "0000000001", Kind: universe.KindStock}, // a second class
	}
	got := Figures(sums, profiles, map[string]float64{"AAA": 100, "AAB": 10})
	if got["revenue"]["AAA"] != 100 {
		t.Errorf("the company's revenue is the same for both: %v", got)
	}
	for _, name := range []string{"marketcap", Shares} {
		if _, has := got[name]["AAA"]; has {
			t.Errorf("%s of a company with two classes is not known: %v", name, got)
		}
		if _, has := got[name]["AAB"]; has {
			t.Errorf("%s of a company with two classes is not known: %v", name, got)
		}
	}
}
