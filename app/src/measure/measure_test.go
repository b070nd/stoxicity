package measure

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

var now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func sample() Inputs {
	acc := time.Date(2026, 8, 3, 20, 5, 0, 0, time.UTC)
	in := Inputs{From: "2026-08-01", To: "2026-08-31", Started: now.Add(-time.Hour)}
	in.IndexDays.Days, in.IndexDays.Complete, in.IndexDays.NonFiling = 31, 21, 10
	in.Filings = []Filing{
		{Accession: "a1", CIK: "1", Form: "8-K", Filed: "2026-08-03", Tickers: []string{"AAA"}, Asked: true, InRecord: true, Accepted: acc, Items: []string{"2.02", "9.01"}},
		{Accession: "a2", CIK: "1", Form: "10-Q", Filed: "2026-08-04", Tickers: []string{"AAA"}, Asked: true, InRecord: true, Accepted: acc},
		{Accession: "a3", CIK: "2", Form: "8-K", Filed: "2026-08-05", Tickers: []string{"BBB"}, Asked: true, InRecord: true, Items: []string{"1.01"}},
		{Accession: "a4", CIK: "3", Form: "8-K/A", Filed: "2026-08-06", Asked: true, InRecord: false, Items: nil},
		{Accession: "a5", CIK: "4", Form: "4", Filed: "2026-08-06", Tickers: []string{"CCC"}, Asked: true, InRecord: true, Accepted: acc},
		{Accession: "a6", CIK: "5", Form: "8-K", Filed: "2026-08-07", Tickers: []string{"DDD"}, Asked: true, InRecord: true, Accepted: acc, Items: []string{"8.01"}},
	}
	in.Events = []Event{{Kind: "results", Ticker: "AAA", Accessions: []string{"a2"}}}
	in.Facts = []Fact{{Category: "earnings", Type: "earnings_reported", Symbol: "AAA", Basis: "reconstructed", Evidence: []string{"a1", "a2"}, Figures: 2}}
	in.Hosts = map[string]Host{"www.sec.gov": {Requests: 30, Bytes: 40 << 20}, "data.sec.gov": {Requests: 6, Bytes: 1 << 20, Failures: 1}}
	in.Prices = Prices{Checked: true, Benchmark: "SPY", Events: 1, Entry: 1, Horizon20: 1, BenchEntry: 1, BenchHorizon20: 1}
	return in
}

func category(r Report, name string) Category {
	for _, c := range r.Categories {
		if c.Name == name {
			return c
		}
	}
	return Category{}
}

func TestBuildCountsFormsTimingAndIdentity(t *testing.T) {
	r := Build(sample(), now)
	if r.Days != 31 || r.Totals.Filings != 6 || r.Totals.Companies != 5 {
		t.Fatalf("totals: %+v days %d", r.Totals, r.Days)
	}
	if r.Totals.TimeKnown != 4 || r.Totals.DayOnly != 1 || r.Totals.NotInRecord != 1 || r.Totals.WithTicker != 5 {
		t.Fatalf("timing and identity: %+v", r.Totals)
	}
	var eightK FormCount
	for _, f := range r.Forms {
		if f.Form == "8-K" {
			eightK = f
		}
	}
	if eightK.Filings != 4 { // the amendment counts with its form
		t.Fatalf("8-K: %+v", eightK)
	}
	if r.Requests != 36 || r.Failures != 1 || r.Bytes != 41<<20 {
		t.Fatalf("traffic: %d %d %d", r.Requests, r.Failures, r.Bytes)
	}
	if r.Records != 1 || r.Rebuilt != 1 || r.Known != 0 {
		t.Fatalf("records: %d known %d rebuilt %d", r.Records, r.Known, r.Rebuilt)
	}
}

func TestBuildKeepsMissingApartFromUnrecognized(t *testing.T) {
	r := Build(sample(), now)
	if c := category(r, "earnings"); c.Verdict != Covered || c.Structured != 2 || c.Companies != 1 || c.Symbols != 1 || c.WithFigures != 1 {
		t.Fatalf("earnings: %+v", c)
	}
	// Item 1.01 names a material agreement, and the pipeline made nothing of it.
	if c := category(r, "contract"); c.Verdict != NotRecognized || c.Structured != 1 || c.TextOnly != 1 {
		t.Fatalf("contract: %+v", c)
	}
	// Only release text tells of guidance and buybacks; no text was read.
	if c := category(r, "guidance"); c.Verdict != NotFetched || c.TextOnly != 2 {
		t.Fatalf("guidance: %+v", c)
	}
	if c := category(r, "buyback"); c.Verdict != NotFetched {
		t.Fatalf("buyback: %+v", c)
	}
	// Form 4 XML was not read at all.
	if c := category(r, "insider"); c.Verdict != NotFetched || c.Structured != 1 {
		t.Fatalf("insider: %+v", c)
	}
	if c := category(r, "analyst"); c.Verdict != NoFreeSource {
		t.Fatalf("analyst: %+v", c)
	}
	// 8-Ks whose items name a kind: a1 (results), a3 (agreement), a6 (other); the
	// pipeline gave none of them their kind (only the 10-Q got "results").
	if r.KindByItems != 3 || r.KindAssigned != 0 {
		t.Fatalf("kinds by item: %d assigned %d", r.KindByItems, r.KindAssigned)
	}
}

func TestEarningsWithoutFiguresArePartial(t *testing.T) {
	in := sample()
	in.Facts[0].Figures = 0
	if c := category(Build(in, now), "earnings"); c.Verdict != Partial || !strings.Contains(c.Reason, "0 of 1 records") {
		t.Fatalf("earnings without figures: %+v", c)
	}
}

func TestUnaskedRecordsAreNotCalledMissing(t *testing.T) {
	in := sample()
	in.Filings[5].Asked, in.Filings[5].InRecord, in.Filings[5].Accepted = false, false, time.Time{}
	r := Build(in, now)
	if r.Totals.NotAsked != 1 || r.Totals.NotInRecord != 1 {
		t.Fatalf("not asked %d, not in record %d", r.Totals.NotAsked, r.Totals.NotInRecord)
	}
	if !strings.Contains(strings.Join(r.Conditions, "\n"), "was not read for 1 of 6 filings") {
		t.Fatalf("conditions: %v", r.Conditions)
	}
}

func TestRecommendation(t *testing.T) {
	r := Build(sample(), now)
	if r.Recommendation != "CONDITIONAL GO" {
		t.Fatalf("recommendation %q", r.Recommendation)
	}
	joined := strings.Join(r.Conditions, "\n")
	for _, want := range []string{"contract:", "(extraction work)", "guidance:", "(free sourcing work)", "analyst:", "8-K items"} {
		if !strings.Contains(joined, want) {
			t.Errorf("conditions lack %q:\n%s", want, joined)
		}
	}

	stopped := sample()
	stopped.Stopped = "the request budget of 10 was reached"
	if r := Build(stopped, now); r.Recommendation != "NO-GO" {
		t.Fatalf("a stopped run must be NO-GO, got %q", r.Recommendation)
	}
	bad := sample()
	bad.IndexDays.Unresolved = 5
	if r := Build(bad, now); r.Recommendation != "NO-GO" {
		t.Fatalf("unresolved index days must be NO-GO, got %q", r.Recommendation)
	}
	if r := Build(Inputs{From: "2026-08-01", To: "2026-08-31"}, now); r.Recommendation != "NO-GO" {
		t.Fatalf("no filings must be NO-GO, got %q", r.Recommendation)
	}
}

func TestProjectionScalesToSixMonths(t *testing.T) {
	in := sample()
	in.StoreSize = 10 << 20
	r := Build(in, now)
	p := r.Projection
	if p.Factor < 5.8 || p.Factor > 5.9 {
		t.Fatalf("factor %v", p.Factor)
	}
	if p.Filings != 35 || p.Requests != 211 { // 6 and 36 requests, times 182/31
		t.Fatalf("projection %+v", p)
	}
	if !strings.HasPrefix(p.Cost, "0") {
		t.Fatalf("cost %q", p.Cost)
	}
}

func TestMarkdownStatesTheLimits(t *testing.T) {
	md := Build(sample(), now).Markdown()
	for _, want := range []string{"## Recommendation: CONDITIONAL GO", "Missing data against unrecognized data", "| contract |", "**not recognized**",
		"does not say that any strategy is profitable", "SPY"} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q", want)
		}
	}
}

type fakeClient struct{ calls int }

func (f *fakeClient) Do(_ context.Context, _ httpx.Request) (*httpx.Response, error) {
	f.calls++
	if f.calls == 2 {
		return nil, errors.New("boom")
	}
	return &httpx.Response{Status: 200, Body: make([]byte, 600<<10)}, nil
}

func TestCounterBudgets(t *testing.T) {
	inner := &fakeClient{}
	c := &Counter{Inner: inner, MaxRequests: 3}
	for range 3 {
		_, _ = c.Do(context.Background(), httpx.Request{URL: "https://www.sec.gov/x"})
	}
	if _, err := c.Do(context.Background(), httpx.Request{URL: "https://www.sec.gov/y"}); !errors.Is(err, ErrBudget) {
		t.Fatalf("the fourth request must be refused, got %v", err)
	}
	if inner.calls != 3 {
		t.Fatalf("sent %d requests", inner.calls)
	}
	h := c.Hosts()["www.sec.gov"]
	if h.Requests != 3 || h.Failures != 1 || h.Bytes != 1200<<10 || c.Stopped() == "" {
		t.Fatalf("counts %+v stopped %q", h, c.Stopped())
	}

	byBytes := &Counter{Inner: &fakeClient{}, MaxBytes: 1 << 20}
	_, _ = byBytes.Do(context.Background(), httpx.Request{URL: "https://a/1"})
	_, _ = byBytes.Do(context.Background(), httpx.Request{URL: "https://a/2"}) // fails, nothing counted
	_, _ = byBytes.Do(context.Background(), httpx.Request{URL: "https://a/3"}) // reaches 1.2 MB
	if _, err := byBytes.Do(context.Background(), httpx.Request{URL: "https://a/4"}); !errors.Is(err, ErrBudget) {
		t.Fatalf("the byte budget must stop the run, got %v", err)
	}
}
