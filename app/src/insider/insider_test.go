package insider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/universe"
)

var now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func trade(cik, owner, code string, shares, price float64, day string) model.InsiderTrade {
	return model.InsiderTrade{CIK: cik, OwnerCIK: owner, Owner: owner, Code: code, Shares: shares, Price: price, Date: day, Security: "Common Stock"}
}

func TestSummarizeCountsOnlyOpenMarketTradesOfTheLastQuarterOnce(t *testing.T) {
	sums := Summarize([]model.InsiderTrade{
		trade("A", "ceo", "P", 1000, 10, "2026-11-03"),
		trade("A", "ceo", "P", 500, 12, "2026-10-01"),
		trade("A", "cfo", "P", 100, 10, "2026-09-20"),
		trade("A", "dir", "S", 200, 15, "2026-10-15"),
		trade("A", "ceo", "A", 9000, 0, "2026-11-03"), // an award
		trade("A", "ceo", "F", 300, 11, "2026-11-03"), // tax withholding
		trade("A", "old", "P", 1e6, 10, "2026-06-01"), // older than a quarter
		trade("A", "nop", "P", 100, 0, "2026-11-01"),  // no price, no value
		trade("B", "ceo", "S", 10, 100, "2026-11-05"),
	}, now)
	a := sums["A"]
	if a.Buys != 1000*10+500*12+100*10 || a.Buyers != 2 || a.Sells != 3000 || a.Sellers != 1 {
		t.Errorf("A: %+v", a)
	}
	if a.LastBuy != "2026-11-03" || a.LastSell != "2026-10-15" {
		t.Errorf("dates: %+v", a)
	}
	if b := sums["B"]; b.Buys != 0 || b.Sells != 1000 || b.Buyers != 0 {
		t.Errorf("B: %+v", b)
	}
	if _, ok := sums["C"]; ok || len(sums) != 2 {
		t.Errorf("only companies with a trade: %v", sums)
	}
}

func TestFiguresGiveCommonStocksWithATradeAFigureAndNoOneElse(t *testing.T) {
	sums := map[string]Summary{"0000000001": {Buys: 5000, Sells: 0, Buyers: 2}, "0000000002": {Sells: 10}}
	profs := []universe.Profile{
		{Ticker: "AAA", CIK: "0000000001", Kind: "stock", Name: "Alpha Corp"},
		{Ticker: "AAAW", CIK: "0000000001", Kind: "stock", Name: "Alpha Corp Warrant", Security: "Alpha Corp - Warrant"},
		{Ticker: "ZZZ", CIK: "0000000003", Kind: "stock", Name: "Zeta Corp"},
	}
	f := Figures(sums, profs)
	if f["buys"]["AAA"] != 5000 || f["buyers"]["AAA"] != 2 {
		t.Errorf("%v", f)
	}
	if _, ok := f["buys"]["AAAW"]; ok {
		t.Error("a warrant has no insider figure")
	}
	if _, ok := f["buys"]["ZZZ"]; ok || len(Figures(nil, profs)) != 0 {
		t.Error("no trade, no figure")
	}
}

// mem is a Store in memory.
type mem struct {
	unread []Filing
	puts   map[string]string // accession -> problem
	trades int
}

func (m *mem) UnreadForm4(_ context.Context, _ time.Time, limit int) ([]Filing, int, error) {
	list := m.unread
	if len(list) > limit {
		list = list[:limit]
	}
	return list, len(m.unread), nil
}
func (m *mem) PutInsider(_ context.Context, f Filing, trades []model.InsiderTrade, problem string, _ time.Time) error {
	m.puts[f.Accession] = problem
	m.trades += len(trades)
	return nil
}

// script answers per accession.
type script map[string]func() ([]model.InsiderTrade, error)

func (s script) Trades(_ context.Context, acc edgar.Accession, _, _ string) ([]model.InsiderTrade, error) {
	return s[string(acc)]()
}

func filing(n int) Filing {
	return Filing{Accession: fmt.Sprintf("0000000001-26-%06d", n), CIK: "0000000001", Filed: "2026-11-06"}
}

func TestRefreshReadsTheBudgetOfFilingsAndRemembersTheEmptyAndTheUnreadable(t *testing.T) {
	st := &mem{unread: []Filing{filing(1), filing(2), filing(3), filing(4)}, puts: map[string]string{}}
	ok := func() ([]model.InsiderTrade, error) { return []model.InsiderTrade{{Code: "P"}, {Code: "S"}}, nil }
	empty := func() ([]model.InsiderTrade, error) { return nil, nil }
	bad := func() ([]model.InsiderTrade, error) {
		return nil, &httpx.Error{Class: httpx.Permanent, Status: 404, URL: "x", Err: errors.New("gone")}
	}
	k := &Keeper{Store: st, Reader: script{filing(1).Accession: ok, filing(2).Accession: empty, filing(3).Accession: bad}, Now: func() time.Time { return now }}
	rep, err := k.Refresh(context.Background(), 3)
	if err != nil || rep != (Report{Asked: 3, Trades: 2, Empty: 1, Bad: 1, Left: 1}) {
		t.Fatalf("%+v, %v", rep, err)
	}
	if st.puts[filing(3).Accession] == "" || st.puts[filing(1).Accession] != "" || len(st.puts) != 3 {
		t.Errorf("bad filings are stored with the reason, good ones without: %v", st.puts)
	}
	if rep, err := k.Refresh(context.Background(), 0); err != nil || rep.Asked != 0 {
		t.Errorf("no budget, no work: %+v, %v", rep, err)
	}
}

func TestRefreshStopsAtTheFirstBlockAndOnAFailedConnectionWithoutMarkingTheFiling(t *testing.T) {
	for name, fail := range map[string]error{
		"blocked":    &httpx.Error{Class: httpx.Blocked, Status: 200, URL: "x", Err: errors.New("rate limit page")},
		"throttled":  &httpx.Error{Class: httpx.Throttled, Status: 429, URL: "x", Err: errors.New("slow down")},
		"connection": &httpx.Error{Class: httpx.Retryable, URL: "x", Err: errors.New("reset")},
	} {
		st := &mem{unread: []Filing{filing(1), filing(2)}, puts: map[string]string{}}
		fn := func() ([]model.InsiderTrade, error) { return nil, fail }
		k := &Keeper{Store: st, Reader: script{filing(1).Accession: fn, filing(2).Accession: fn}, Now: func() time.Time { return now }}
		rep, err := k.Refresh(context.Background(), 5)
		if err == nil || rep.Asked != 0 || len(st.puts) != 0 {
			t.Errorf("%s: %+v, %v, %v", name, rep, err, st.puts)
		}
	}
}

func TestSummarizeLeavesOutOtherSecuritiesCountsUnpricedAndFollowsAmendments(t *testing.T) {
	pref := trade("A", "ceo", "P", 1000, 25, "2026-11-03")
	pref.Security = "Series B Preferred Stock"
	unpriced := trade("A", "cfo", "P", 500, 0, "2026-11-02") // the price is in a footnote
	orig := trade("A", "dir", "P", 100000, 20, "2026-11-01")
	orig.Period, orig.Form, orig.Filed, orig.Accession = "2026-11-01", "4", "2026-11-03", "0000000001-26-000001"
	fix := trade("A", "dir", "P", 1000, 20, "2026-11-01") // corrected from 100,000 shares to 1,000
	fix.Period, fix.Form, fix.Filed, fix.Accession = "2026-11-01", "4/A", "2026-11-05", "0000000001-26-000002"
	sums := Summarize([]model.InsiderTrade{pref, unpriced, orig, fix}, now)
	a := sums["A"]
	if a.Buys != 20000 || a.Buyers != 1 {
		t.Errorf("the amendment replaces the original and preferred stock is not common stock: %+v", a)
	}
	if a.UnpricedBuys != 1 {
		t.Errorf("a purchase without a price is counted, not forgotten: %+v", a)
	}
}

func TestLatestKeepsFormsFiledAfterTheAmendmentAndTradesWithNoPeriod(t *testing.T) {
	mk := func(acc, form, filed string, shares float64) model.InsiderTrade {
		x := trade("A", "dir", "P", shares, 10, "2026-11-01")
		x.Accession, x.Form, x.Filed, x.Period = acc, form, filed, "2026-11-01"
		return x
	}
	got := Latest([]model.InsiderTrade{
		mk("1", "4", "2026-11-02", 1), mk("2", "4/A", "2026-11-03", 2), mk("3", "4/A", "2026-11-04", 3), mk("4", "4", "2026-11-05", 4),
	})
	var accs []string
	for _, x := range got {
		accs = append(accs, x.Accession)
	}
	if len(accs) != 2 || accs[0] != "3" || accs[1] != "4" {
		t.Errorf("the newest amendment and the later form stay: %v", accs)
	}
	noPeriod := trade("A", "dir", "P", 5, 10, "2026-11-01")
	noPeriod.Form = "4/A"
	if got := Latest([]model.InsiderTrade{noPeriod, mk("1", "4", "2026-11-02", 1)}); len(got) != 2 {
		t.Errorf("without a period nothing can be matched, so nothing is dropped: %v", got)
	}
}
