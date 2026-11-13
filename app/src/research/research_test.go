package research

// Every name, figure and price below is a synthetic fixture, not verified against live data.

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// fake is a small in-memory Store.
type fake struct {
	events    []store.SignalEvent
	articles  []model.Article
	backfills map[string]store.Backfill
	metas     map[string]store.FilingMeta
	trades    []model.InsiderTrade
	facts     []fundamentals.Fact
	refs      []store.RefTicker
	candles   map[string][]marketdata.Candle
	splits    []marketdata.Split
	stored    map[string]eventfact.Record
}

func (f *fake) SignalEvents(context.Context, store.SignalQuery) ([]store.SignalEvent, error) {
	return f.events, nil
}
func (f *fake) AttachOrigins(context.Context, []store.SignalEvent, time.Time) error { return nil }
func (f *fake) Range(context.Context, time.Time, time.Time) ([]model.Article, error) {
	return f.articles, nil
}
func (f *fake) Backfilled(context.Context, []string) (map[string]store.Backfill, error) {
	return f.backfills, nil
}
func (f *fake) FilingMetas(context.Context, []string) (map[string]store.FilingMeta, error) {
	return f.metas, nil
}
func (f *fake) InsiderTrades(context.Context, string, string, int) ([]model.InsiderTrade, error) {
	return f.trades, nil
}
func (f *fake) FactHistory(context.Context, string) ([]fundamentals.Fact, error) { return f.facts, nil }
func (f *fake) RefTickers(context.Context) ([]store.RefTicker, error)            { return f.refs, nil }
func (f *fake) PutEventFacts(_ context.Context, records []eventfact.Record, _ time.Time) (int, error) {
	if f.stored == nil {
		f.stored = map[string]eventfact.Record{}
	}
	n := 0
	for _, r := range records {
		if old, ok := f.stored[r.ID]; !ok || eventfact.Hash(old) != eventfact.Hash(r) {
			f.stored[r.ID] = r
			n++
		}
	}
	return n, nil
}
func (f *fake) EventFacts(context.Context, store.EventFactQuery) ([]eventfact.Record, error) {
	return nil, nil
}
func (f *fake) CandleSeries(context.Context, []string, string, string, time.Time) (map[string][]marketdata.Candle, error) {
	return f.candles, nil
}
func (f *fake) Splits(context.Context) ([]marketdata.Split, error) { return f.splits, nil }
func (f *fake) SplitsAsOf(context.Context, time.Time) ([]marketdata.Split, error) {
	return f.splits, nil
}

func fact(metric, unit, start, end, filed string, value float64) fundamentals.Fact {
	return fundamentals.Fact{CIK: "0000000001", Metric: metric, Unit: unit, Start: start, End: end, Filed: filed, Value: value}
}

func TestFiguresAsOf(t *testing.T) {
	facts := []fundamentals.Fact{
		fact(fundamentals.Revenue, "USD", "2023-01-01", "2023-12-31", "2024-02-15", 100),
		fact(fundamentals.NetIncome, "USD", "2023-01-01", "2023-12-31", "2024-02-15", 10),
		fact(fundamentals.AvgShares, "shares", "2023-01-01", "2023-12-31", "2024-02-15", 10),
		fact(fundamentals.Revenue, "USD", "2024-01-01", "2024-12-31", "2025-02-15", 120),
		fact(fundamentals.NetIncome, "USD", "2024-01-01", "2024-12-31", "2025-02-15", 15),
		fact(fundamentals.AvgShares, "shares", "2024-01-01", "2024-12-31", "2025-02-15", 10),
		fact(fundamentals.Revenue, "USD", "2025-01-01", "2025-12-31", "2026-02-15", 999), // filed after the day
		fact(fundamentals.Revenue, "USD", "2025-01-01", "2025-03-31", "", 500),           // no filing day
	}
	got, ok := figuresAsOf(facts, "2025-06-01")
	if !ok || got.Period != "FY ending 2024-12-31" || got.Currency != "USD" {
		t.Fatalf("figures = %+v, %v", got, ok)
	}
	near := func(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 1e-9 }
	if !near(got.Revenue, 120) || !near(got.RevenueYoY, 0.2) || !near(got.NetIncome, 15) || !near(got.NetIncomeYoY, 0.5) ||
		!near(got.EPS, 1.5) || !near(got.EPSYoY, 0.5) {
		t.Errorf("figures = %+v", got)
	}
	if _, ok := figuresAsOf(facts, "2023-06-01"); ok {
		t.Error("figures before any filing should not exist")
	}
}

func TestFiguresAsOfMarginsAndCashFlow(t *testing.T) {
	const filed = "2025-02-15"
	year := func(metric string, value float64) fundamentals.Fact {
		return fact(metric, "USD", "2024-01-01", "2024-12-31", filed, value)
	}
	otherYear := func(metric string, value float64) fundamentals.Fact {
		return fact(metric, "USD", "2023-01-01", "2023-12-31", filed, value)
	}
	cases := []struct {
		name                                         string
		facts                                        []fundamentals.Fact
		netMargin, operatingMargin, flow, flowMargin *float64
	}{
		{"all present", []fundamentals.Fact{
			year(fundamentals.Revenue, 200), year(fundamentals.NetIncome, 20),
			year(fundamentals.OperatingIncome, 50), year(fundamentals.OperatingCashFlow, 80)},
			ptr(0.1), ptr(0.25), ptr(80), ptr(0.4)},
		{"operating income missing", []fundamentals.Fact{
			year(fundamentals.Revenue, 200), year(fundamentals.NetIncome, 20), year(fundamentals.OperatingCashFlow, 80)},
			ptr(0.1), nil, ptr(80), ptr(0.4)},
		{"stored before the operating concepts were read", []fundamentals.Fact{
			year(fundamentals.Revenue, 200), year(fundamentals.NetIncome, 20)},
			ptr(0.1), nil, nil, nil},
		{"revenue zero", []fundamentals.Fact{
			year(fundamentals.Revenue, 0), year(fundamentals.NetIncome, 20),
			year(fundamentals.OperatingIncome, 50), year(fundamentals.OperatingCashFlow, 80)},
			nil, nil, ptr(80), nil},
		{"figures of another period are not used", []fundamentals.Fact{
			year(fundamentals.Revenue, 200), year(fundamentals.NetIncome, 20),
			otherYear(fundamentals.OperatingIncome, 50), otherYear(fundamentals.OperatingCashFlow, 80)},
			ptr(0.1), nil, nil, nil},
		{"revenue of another period is not used", []fundamentals.Fact{
			otherYear(fundamentals.Revenue, 100), year(fundamentals.NetIncome, 20), year(fundamentals.OperatingIncome, 50)},
			nil, nil, nil, nil},
	}
	same := func(got, want *float64) bool {
		if got == nil || want == nil {
			return got == want
		}
		return math.Abs(*got-*want) < 1e-9
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := figuresAsOf(c.facts, "2025-06-01")
			if !ok {
				t.Fatal("no figures")
			}
			if !same(got.NetMargin, c.netMargin) || !same(got.OperatingMargin, c.operatingMargin) ||
				!same(got.OperatingCashFlow, c.flow) || !same(got.CashFlowMargin, c.flowMargin) {
				t.Errorf("net %v operating %v flow %v flow margin %v", got.NetMargin, got.OperatingMargin, got.OperatingCashFlow, got.CashFlowMargin)
			}
		})
	}
}

func ptr(v float64) *float64 { return &v }

func TestPrices(t *testing.T) {
	day := func(d string, price float64, raw bool) marketdata.Candle {
		return marketdata.Candle{Day: d, Open: price, High: price, Low: price, Close: price, Volume: 100, Raw: raw}
	}
	f := &fake{
		candles: map[string][]marketdata.Candle{
			"AAA": {day("2025-03-03", 52, true), day("2025-02-28", 100, true)},
			"BBB": {day("2025-02-28", 100, false), day("2025-03-03", 52, false)},
		},
		splits: []marketdata.Split{{Ticker: "AAA", Day: "2025-03-03", From: 1, To: 2}, {Ticker: "BBB", Day: "2025-03-03", From: 1, To: 2}},
	}
	got, res, err := Prices(context.Background(), f, []string{"BBB", "AAA", "CCC"}, "2025-01-01", "2025-04-01", time.Time{}, false)
	if err != nil {
		t.Fatal(err)
	}
	a := got["AAA"]
	if len(a.Bars) != 2 || a.Bars[0].Close != 50 || a.Bars[0].Volume != 200 || a.Bars[1].Close != 52 {
		t.Errorf("AAA = %+v", a.Bars)
	}
	if _, ok := got["BBB"]; ok || res.Unresolved["BBB"] != "candles of unknown basis and a split on 2025-03-03" {
		t.Errorf("BBB: %v", res.Unresolved)
	}
	if res.Unresolved["CCC"] != "no bars" || res.Series != 1 || res.First != "2025-02-28" || res.Last != "2025-03-03" {
		t.Errorf("result = %+v", res)
	}
}

func TestBuildStoresOnce(t *testing.T) {
	pub := time.Date(2026, 2, 10, 21, 0, 0, 0, time.UTC)
	accession := "0000000001-26-000001"
	f := &fake{
		events: []store.SignalEvent{{
			SecurityID: "sec_1", Ticker: "ACME", EventID: "ev1", Kind: string(model.EventResults),
			Evidence: []store.SignalEvidence{{ArticleID: accession, Form: "8-K", CIK: "0000000001", Items: []string{"2.02"},
				Title: "Acme reports fourth quarter results", Published: pub}},
		}},
		articles: []model.Article{{ID: accession, Title: "Acme reports fourth quarter results", Published: pub, Fetched: pub.Add(time.Minute)}},
		metas: map[string]store.FilingMeta{accession: {Accession: accession, FiledDay: "2026-02-10",
			Accepted: pub.Add(5 * time.Minute), Precision: store.PrecisionTime}},
		facts: []fundamentals.Fact{
			fact(fundamentals.Revenue, "USD", "2025-01-01", "2025-12-31", "2026-02-10", 120),
			fact(fundamentals.Revenue, "USD", "2024-01-01", "2024-12-31", "2025-02-10", 100),
		},
		refs: []store.RefTicker{{Ticker: "ACME", CIK: "0000000001"}, {Ticker: "ACMEW", CIK: "0000000001"}},
		trades: []model.InsiderTrade{
			{Accession: "0000000002-26-000007", Seq: 1, CIK: "0000000001", Owner: "Pat Doe", OwnerCIK: "0000000002", Role: "director",
				Security: "Common Stock", Form: "4", Period: "2026-02-04", Date: "2026-02-04", Filed: "2026-02-06", Code: "P", Acquired: true, Shares: 100, Price: 10, Direct: true},
			{Accession: "0000000003-26-000009", Seq: 1, CIK: "0000000001", Owner: "Sam Roe", OwnerCIK: "0000000003", Role: "Chief Executive Officer",
				Security: "Common Stock", Form: "4", Period: "2026-02-05", Date: "2026-02-05", Filed: "2026-02-06", Code: "S", Shares: 50, Price: 11, Direct: true},
		},
	}
	from, to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	now := to.AddDate(0, 0, 1)
	res, err := Build(context.Background(), f, from, to, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Events != 1 || res.Trades != 2 || res.Records != 3 || res.Written != 3 ||
		res.ByType[string(eventfact.EarningsReported)] != 1 || res.ByType[string(eventfact.InsiderBuy)] != 1 || res.ByType[string(eventfact.InsiderSell)] != 1 {
		t.Errorf("first build = %+v", res)
	}
	earnings := f.stored[eventfact.ID(eventfact.NewKey(eventfact.Earnings, "sec_1", "2026-02-10"))]
	if rv, ok := earnings.Fact("revenue_yoy"); !ok || math.Abs(rv.Value-0.2) > 1e-9 || !earnings.AvailableAt.Equal(pub.Add(5*time.Minute)) {
		t.Errorf("earnings = %+v", earnings)
	}
	again, err := Build(context.Background(), f, from, to, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.Records != 3 || again.Written != 0 {
		t.Errorf("second build = %+v", again)
	}
}

func TestObservations(t *testing.T) {
	at := time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
	records := []eventfact.Record{
		{ID: "ef_b", Symbol: "BBB", Type: eventfact.InsiderBuy, Category: eventfact.Insider, AvailableAt: at, Precision: eventfact.PrecisionDay,
			Source: eventfact.SourceFiling, Basis: eventfact.Reconstructed, Facts: []eventfact.Fact{{Name: "value", Value: 1000, Unit: "USD"}},
			Labels: map[string]string{"role": "director"}},
		{ID: "ef_a", Symbol: "AAA", Type: eventfact.EarningsReported, Category: eventfact.Earnings, AvailableAt: at, Precision: eventfact.PrecisionTime,
			Source: eventfact.SourceWire, Basis: eventfact.Known},
	}
	obs := Observations(records, map[string]string{"BBB": "Tech"})
	if len(obs) != 2 || obs[0].ID != "ef_a" || obs[1].ID != "ef_b" {
		t.Fatalf("order = %+v", obs)
	}
	b := obs[1]
	if !b.DayPrecision || !b.Reconstructed || b.Sector != "Tech" || b.Numbers["value"] != 1000 ||
		b.Labels["role"] != "director" || b.Labels["source"] != "official_filing" || b.Labels["category"] != "insider" || b.MarketCap != 0 {
		t.Errorf("observation = %+v", b)
	}
	if a := obs[0]; a.DayPrecision || a.Reconstructed {
		t.Errorf("observation = %+v", a)
	}
}

// One issuer's event is kept for one of its securities: the common share
// before a preferred series or a note.
func TestOneSecurityPerIssuer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"JPM", "JPM-PC", true}, {"JPM-PC", "JPM", false}, {"JPM", "AMJB", true}, {"ASB", "ASBA", true}, {"BRK.A", "BRK.B", true}, {"AAA", "AAB", true}} {
		if got := preferred(tc.a, tc.b); got != tc.want {
			t.Errorf("preferred(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	texts := []eventfact.EventText{
		{EventID: "e1", CIK: "0000019617", Symbol: "JPM-PC"},
		{EventID: "e1", CIK: "0000019617", Symbol: "JPM"},
		{EventID: "e1", CIK: "0000019617", Symbol: "AMJB"},
		{EventID: "e2", CIK: "0000019617", Symbol: "JPM-PD"},
		{EventID: "e3", Symbol: "XYZ"},
	}
	got := oneSecurityPerIssuer(texts)
	if len(got) != 3 || got[0].Symbol != "JPM" || got[1].Symbol != "JPM-PD" || got[2].Symbol != "XYZ" {
		t.Errorf("kept %+v", got)
	}
}
