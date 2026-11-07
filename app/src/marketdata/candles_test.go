package marketdata

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

func resp(body string) httpx.Response { return httpx.Response{Status: 200, Body: []byte(body)} }

func TestChange(t *testing.T) {
	if got := Change(100, 102.5); got < 0.0249 || got > 0.0251 {
		t.Errorf("Change = %v", got)
	}
	if Change(0, 5) != 0 {
		t.Error("no previous close, no change")
	}
}

func TestSameTickerIgnoresCaseAndShareClassSeparator(t *testing.T) {
	if !SameTicker("BRK.B", "brk-b") || SameTicker("BRK.A", "BRK.B") {
		t.Error("SameTicker")
	}
}

// memStore is a Store in memory.
type memStore struct {
	mu     sync.Mutex
	rows   map[string]Candle // by ticker|day
	checks map[string]Check
}

func newMem() *memStore { return &memStore{rows: map[string]Candle{}, checks: map[string]Check{}} }
func (m *memStore) PutCandles(_ context.Context, rows []Candle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range rows {
		m.rows[c.Ticker+"|"+c.Day] = c
	}
	return nil
}
func (m *memStore) NewestDays(context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for _, c := range m.rows {
		if c.Day > out[c.Ticker] {
			out[c.Ticker] = c.Day
		}
	}
	return out, nil
}
func (m *memStore) Checks(context.Context) (map[string]Check, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]Check{}
	for k, v := range m.checks {
		out[k] = v
	}
	return out, nil
}
func (m *memStore) SaveChecks(_ context.Context, c map[string]Check) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range c {
		m.checks[k] = v
	}
	return nil
}

var t0 = time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC) // a Thursday

// perTicker is a PerTicker that answers from a script, for the keeper's tests.
type perTicker struct {
	mu    sync.Mutex
	calls []string
	reply map[string][]Candle
	fail  map[string]error
}

func (p *perTicker) Name() string { return "script" }
func (p *perTicker) History(_ context.Context, t string, since time.Time) ([]Candle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, t+"@"+since.Format(time.DateOnly))
	if err := p.fail[t]; err != nil {
		return nil, err
	}
	return p.reply[t], nil
}

func two(t string) []Candle {
	return []Candle{
		{Ticker: t, Day: "2026-11-02", Open: 70.1, High: 71, Low: 69.8, Close: 70.5, Volume: 1200},
		{Ticker: t, Day: "2026-11-03", Open: 70.5, High: 72, Low: 70.2, Close: 71.9, Volume: 1500},
	}
}

func TestRefreshStocksAsksPriorityFirstWithinTheBudgetAndRemembersTheAnswers(t *testing.T) {
	src := &perTicker{reply: map[string][]Candle{"KO": two("KO"), "NVDA": two("NVDA")}}
	st := newMem()
	k := &Keeper{Store: st, Source: src, Now: func() time.Time { return t0 }, Workers: 1}
	// Budget two: ZZZZ was named first, then the ticker order (KO).
	rep, err := k.Refresh(context.Background(), []string{"NVDA", "KO", "ZZZZ"}, []string{"ZZZZ"}, 2)
	if err != nil || rep.Asked != 2 || rep.Left != 1 || rep.Stored != 2 || rep.None != 1 {
		t.Fatalf("%+v, %v", rep, err)
	}
	if fmt.Sprint(src.calls) != "[ZZZZ@2026-10-29 KO@2026-10-29]" || st.rows["KO|2026-11-03"].Close != 71.9 {
		t.Errorf("calls %v, rows %v", src.calls, st.rows)
	}
	// Looked up lately: not asked again today; the one left is next.
	rep, _ = k.Refresh(context.Background(), []string{"NVDA", "KO", "ZZZZ"}, nil, 5)
	if rep.Asked != 1 || len(src.calls) != 3 {
		t.Errorf("second round: %+v, %v", rep, src.calls)
	}
	// A day later the stocks with candles are asked again, from the day after their newest.
	k.Now = func() time.Time { return t0.Add(25 * time.Hour) }
	src.reply["KO"] = nil
	rep, _ = k.Refresh(context.Background(), []string{"KO"}, nil, 5)
	if rep.Asked != 1 || rep.None != 0 || src.calls[len(src.calls)-1] != "KO@2026-11-04" {
		t.Errorf("a stock with no new candle is not 'none': %+v, %v", rep, src.calls)
	}
}

func TestRefreshStocksStopsWhenThePushBackComes(t *testing.T) {
	src := &perTicker{fail: map[string]error{"A": &httpx.Error{Class: httpx.Throttled, Status: 429, URL: "x"}}}
	k := &Keeper{Store: newMem(), Source: src, Now: func() time.Time { return t0 }, Workers: 1}
	rep, err := k.Refresh(context.Background(), []string{"A", "B", "C"}, nil, 10)
	if err == nil || rep.Asked != 0 || len(src.calls) != 1 {
		t.Errorf("%+v, %v, %v", rep, err, src.calls)
	}
}

func mustDay(d string) time.Time { t, _ := time.Parse(time.DateOnly, d); return t }

func polygonDay(s string) string {
	return fmt.Sprintf(`{"results":[{"T":"KO","t":1700000000000,"o":70,"h":72,"l":69,"c":71,"v":100},{"T":"BRK.B","o":400,"h":410,"l":399,"c":405,"v":5},{"T":"OTHER","o":1,"h":2,"l":1,"c":2,"v":1},{"T":"BAD","o":1,"h":0,"l":1,"c":2,"v":1}],"day":%q}`, s)
}

func TestRefreshDaysFetchesTheMissingMarketDaysOnceAndKeepsOnlyOurStocks(t *testing.T) {
	f := httpx.NewFake()
	p := Polygon{Client: f, Key: "k", URL: "https://poly.test/"}
	// Now is Thursday 2026-11-05 15:00 UTC: the last day that is over is Wednesday the 4th.
	st := newMem()
	st.rows["KO|2026-11-02"] = Candle{Ticker: "KO", Day: "2026-11-02", Open: 1, High: 2, Low: 1, Close: 1}
	for _, d := range []string{"2026-11-03", "2026-11-04"} {
		f.Reply("https://poly.test/"+d+"?adjusted=true&apiKey=k", resp(polygonDay(d)))
	}
	k := &Keeper{Store: st, Source: p, Now: func() time.Time { return t0 }}
	rep, err := k.Refresh(context.Background(), []string{"KO", "BRK-B"}, nil, 10)
	if err != nil || rep.Asked != 2 || rep.Stored != 4 {
		t.Fatalf("%+v, %v", rep, err)
	}
	if c := st.rows["BRK-B|2026-11-04"]; c.Close != 405 {
		t.Errorf("BRK.B is stored under our spelling BRK-B: %+v", st.rows)
	}
	if _, ok := st.rows["OTHER|2026-11-04"]; ok {
		t.Error("stocks we do not know are not kept")
	}
	if rep, _ = k.Refresh(context.Background(), []string{"KO"}, nil, 10); rep.Asked != 0 || len(f.Calls()) != 2 {
		t.Errorf("nothing is missing: %+v, %d calls", rep, len(f.Calls()))
	}
}

func TestRefreshDaysRemembersAHolidayAndNeedsAKey(t *testing.T) {
	f := httpx.NewFake()
	p := Polygon{Client: f, Key: "k", URL: "https://poly.test/"}
	st := newMem()
	st.rows["KO|2026-11-02"] = Candle{Ticker: "KO", Day: "2026-11-02", Open: 1, High: 2, Low: 1, Close: 1}
	f.Reply("https://poly.test/2026-11-03?adjusted=true&apiKey=k", resp(`{"results":[]}`))
	f.Reply("https://poly.test/2026-11-04?adjusted=true&apiKey=k", resp(polygonDay("2026-11-04")))
	k := &Keeper{Store: st, Source: p, Now: func() time.Time { return t0 }}
	if rep, err := k.Refresh(context.Background(), []string{"KO"}, nil, 10); err != nil || rep.None != 1 || rep.Stored != 1 {
		t.Fatalf("%+v, %v", rep, err)
	}
	if _, ok := st.checks["day:2026-11-03"]; !ok {
		t.Error("a day with no candles is remembered, not asked again")
	}
	if _, err := (Polygon{Client: f}).Day(context.Background(), t0); err == nil {
		t.Error("no key, no look-up")
	}
}
