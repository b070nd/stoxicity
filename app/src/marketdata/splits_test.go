package marketdata

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

func TestPolygonSplitsAsksFromTheDayAndFollowsPagesWithTheKey(t *testing.T) {
	f := httpx.NewFake()
	p := Polygon{Client: f, Key: "k", SplitsURL: "https://poly.test/splits"}
	f.Reply("https://poly.test/splits?apiKey=k&execution_date.gte=2026-01-01&limit=1000&order=asc", resp(`{"results":[
		{"ticker":"NVDA","execution_date":"2026-06-10","split_from":1,"split_to":10},
		{"ticker":"BAD","execution_date":"nonsense","split_from":1,"split_to":2},
		{"ticker":"ZERO","execution_date":"2026-06-10","split_from":0,"split_to":2}],
		"next_url":"https://poly.test/splits?cursor=abc"}`))
	f.Reply("https://poly.test/splits?cursor=abc&apiKey=k", resp(`{"results":[{"ticker":"GE","execution_date":"2026-07-01","split_from":8,"split_to":1}]}`))
	got, err := p.Splits(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Ticker != "NVDA" || got[0].Ratio() != 10 || got[1].Ticker != "GE" || math.Abs(got[1].Ratio()-0.125) > 1e-9 {
		t.Errorf("%+v", got)
	}
	if _, err := (Polygon{Client: f}).Splits(context.Background(), t0); err == nil {
		t.Error("no key, no splits")
	}
}

func TestPolygonSplitsReportsMorePagesThanItFollows(t *testing.T) {
	f := httpx.NewFake()
	p := Polygon{Client: f, Key: "k", SplitsURL: "https://poly.test/splits"}
	for i := 0; i < splitPages; i++ {
		url := "https://poly.test/splits?apiKey=k&execution_date.gte=2026-01-01&limit=1000&order=asc"
		if i > 0 {
			url = "https://poly.test/splits?cursor=" + strings.Repeat("a", i) + "&apiKey=k"
		}
		f.Reply(url, resp(`{"results":[],"next_url":"https://poly.test/splits?cursor=`+strings.Repeat("a", i+1)+`"}`))
	}
	if _, err := p.Splits(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("an incomplete reading is an error, not a short list")
	}
}

func raw(ticker, day string, close float64) Candle {
	return Candle{Ticker: ticker, Day: day, Open: close, High: close, Low: close, Close: close, Raw: true}
}

func TestCompare(t *testing.T) {
	known := SplitCoverage{Since: "2026-01-01", Read: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)}
	ten := Split{Ticker: "NVDA", Day: "2026-06-10", From: 1, To: 10}
	cases := []struct {
		name       string
		prev, cur  Candle
		splits     []Split
		cov        SplitCoverage
		state      MoveState
		wantChange float64
	}{
		{"an ordinary day", raw("KO", "2026-06-09", 100), raw("KO", "2026-06-10", 102), nil, known, MoveOK, 0.02},
		{"a split between two raw closes is reconciled, not hidden", raw("NVDA", "2026-06-09", 1000), raw("NVDA", "2026-06-10", 105), []Split{ten}, known, MoveSplit, 0.05},
		{"a large real move is shown as it is when no split explains it", raw("KO", "2026-06-09", 100), raw("KO", "2026-06-10", 40), nil, known, MoveOK, -0.6},
		{"half in a day with the splits known is a real fall", raw("KO", "2026-06-09", 100), raw("KO", "2026-06-10", 50), nil, known, MoveOK, -0.5},
		{"half in a day with no splits read is not explained", raw("KO", "2026-06-09", 100), raw("KO", "2026-06-10", 50), nil, SplitCoverage{}, MoveUnresolved, 0},
		{"the splits start after the earlier day: not covered", raw("KO", "2026-06-09", 100), raw("KO", "2026-06-10", 50), nil, SplitCoverage{Since: "2026-06-10", Read: known.Read}, MoveUnresolved, 0},
		{"the splits were read the day of the move: a split may be listed late", raw("KO", "2026-06-09", 100), raw("KO", "2026-06-10", 50), nil, SplitCoverage{Since: "2026-01-01", Read: time.Date(2026, 6, 10, 20, 0, 0, 0, time.UTC)}, MoveUnresolved, 0},
		{"unknown basis next to a split after it", Candle{Ticker: "NVDA", Day: "2026-06-09", Close: 100}, raw("NVDA", "2026-06-10", 10), []Split{ten}, known, MoveUnresolved, 0},
		{"unknown basis and no split after it", Candle{Ticker: "KO", Day: "2026-06-09", Close: 100}, Candle{Ticker: "KO", Day: "2026-06-10", Close: 101}, nil, known, MoveOK, 0.01},
		{"a split before the earlier day does not matter", raw("NVDA", "2026-06-11", 100), raw("NVDA", "2026-06-12", 101), []Split{ten}, known, MoveOK, 0.01},
		{"another stock's split does not matter", raw("KO", "2026-06-09", 100), raw("KO", "2026-06-10", 101), []Split{ten}, known, MoveOK, 0.01},
		{"a reverse split", raw("GE", "2026-07-01", 10), raw("GE", "2026-07-02", 81), []Split{{Ticker: "GE", Day: "2026-07-02", From: 8, To: 1}}, known, MoveSplit, 0.0125},
		{"sessions missing", raw("KO", "2026-06-03", 100), raw("KO", "2026-06-10", 101), nil, known, MoveGap, 0},
		{"share class spelling", raw("BRK-B", "2026-06-09", 400), raw("BRK-B", "2026-06-10", 40), []Split{{Ticker: "BRK.B", Day: "2026-06-10", From: 1, To: 10}}, known, MoveSplit, 0},
	}
	for _, c := range cases {
		m := Compare(c.prev, c.cur, c.splits, c.cov)
		if m.State != c.state {
			t.Errorf("%s: state %q (%s), want %q", c.name, m.State, m.Note, c.state)
			continue
		}
		if m.Shown() && math.Abs(m.Change-c.wantChange) > 1e-9 {
			t.Errorf("%s: change %v, want %v", c.name, m.Change, c.wantChange)
		}
	}
}

func TestRefreshReadsTheSplitsOnceADayAndStoresThem(t *testing.T) {
	f := httpx.NewFake()
	p := Polygon{Client: f, Key: "k", URL: "https://poly.test/", SplitsURL: "https://poly.test/splits"}
	st := newMemSplits()
	since := t0.Add(-splitsFirst).Format(time.DateOnly)
	f.Reply("https://poly.test/splits?apiKey=k&execution_date.gte="+since+"&limit=1000&order=asc", resp(`{"results":[{"ticker":"NVDA","execution_date":"2026-06-10","split_from":1,"split_to":10}]}`))
	for _, d := range []string{"2026-11-03", "2026-11-04"} {
		f.Reply("https://poly.test/"+d+"?adjusted=false&apiKey=k", resp(polygonDay(d)))
	}
	st.rows["KO|2026-11-02"] = Candle{Ticker: "KO", Day: "2026-11-02", Open: 1, High: 2, Low: 1, Close: 1}
	k := &Keeper{Store: st, Source: p, Splits: p, Now: func() time.Time { return t0 }}
	rep, err := k.Refresh(context.Background(), []string{"KO"}, nil, 10)
	if err != nil || rep.Splits != 1 || len(st.splits) != 1 {
		t.Fatalf("%+v, %v, %+v", rep, err, st.splits)
	}
	calls := len(f.Calls())
	if rep, err = k.Refresh(context.Background(), []string{"KO"}, nil, 10); err != nil || rep.Splits != 0 || len(f.Calls()) != calls {
		t.Errorf("read again within the day: %+v, %v, %d calls (was %d)", rep, err, len(f.Calls()), calls)
	}
}

func TestRefreshSplitsStartsBeforeTheLastReadingSoTheyJoin(t *testing.T) {
	f := httpx.NewFake()
	p := Polygon{Client: f, Key: "k", URL: "https://poly.test/", SplitsURL: "https://poly.test/splits"}
	st := newMemSplits()
	st.cov = SplitCoverage{Since: "2025-11-05", Read: t0.Add(-100 * 24 * time.Hour)}
	k := &Keeper{Store: st, Source: p, Splits: p, Now: func() time.Time { return t0 }}
	want := t0.Add(-100*24*time.Hour).AddDate(0, 0, -7).Format(time.DateOnly)
	f.Reply("https://poly.test/splits?apiKey=k&execution_date.gte="+want+"&limit=1000&order=asc", resp(`{"results":[]}`))
	if n, err := k.refreshSplits(context.Background()); err != nil || n != 0 {
		t.Fatalf("%d, %v, calls %v", n, err, f.Calls())
	}
}

func TestRefetchAsksForStoredDaysAgainAndReplacesThemWithRawCandles(t *testing.T) {
	f := httpx.NewFake()
	p := Polygon{Client: f, Key: "k", URL: "https://poly.test/"}
	st := newMem()
	st.rows["KO|2026-11-04"] = Candle{Ticker: "KO", Day: "2026-11-04", Open: 1, High: 2, Low: 1, Close: 1} // unknown basis
	f.Reply("https://poly.test/2026-11-04?adjusted=false&apiKey=k", resp(polygonDay("2026-11-04")))
	k := &Keeper{Store: st, Source: p, Now: func() time.Time { return t0 }, Refetch: 1}
	rep, err := k.Refresh(context.Background(), []string{"KO"}, nil, 10)
	if err != nil || rep.Asked != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if c := st.rows["KO|2026-11-04"]; !c.Raw || c.Close != 71 {
		t.Errorf("replaced by the raw candle: %+v", c)
	}
}

func TestRefetchSkipsDaysAlreadyRawAndPaces(t *testing.T) {
	f := httpx.NewFake()
	p := Polygon{Client: f, Key: "k", URL: "https://poly.test/"}
	st := newMem()
	st.rows["KO|2026-11-03"] = Candle{Ticker: "KO", Day: "2026-11-03", Open: 1, High: 2, Low: 1, Close: 1, Raw: true}
	st.checks["day:2026-11-03"] = Check{CheckedAt: t0, Found: 5}
	st.checks["day:2026-11-04"] = Check{CheckedAt: t0, Found: 5} // read before, but not raw: asked again in a refetch
	f.Reply("https://poly.test/2026-11-04?adjusted=false&apiKey=k", resp(polygonDay("2026-11-04")))
	k := &Keeper{Store: st, Source: p, Now: func() time.Time { return t0 }, Refetch: 2, Pace: time.Millisecond}
	rep, err := k.Refresh(context.Background(), []string{"KO"}, nil, 10)
	if err != nil || rep.Asked != 1 || len(f.Calls()) != 1 {
		t.Fatalf("only the day not yet raw is asked: %+v %v %d calls", rep, err, len(f.Calls()))
	}
}
