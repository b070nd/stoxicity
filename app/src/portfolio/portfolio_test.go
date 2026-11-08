package portfolio

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

func dec(s string) money.Decimal { return money.MustDecimal(s) }
func eur(m int64) money.Amount   { return amt(m, "EUR") }
func usd(m int64) money.Amount   { return amt(m, "USD") }

var base = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func at(h int) time.Time  { return base.Add(time.Duration(h) * time.Hour) }
func rec(h int) time.Time { return at(h).Add(time.Minute) }
func com(h int) Common    { return Common{Event: at(h), Source: "manual"} }

func newL(t *testing.T, env Env, m CostMethod) *Ledger {
	t.Helper()
	return newNamed(t, "b1", env, m)
}

func newNamed(t *testing.T, id string, env Env, m CostMethod) *Ledger {
	t.Helper()
	l, err := NewLedger(Book{ID: id, Account: "acct", Name: id, Env: env, Base: "EUR", Method: m})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func do(t *testing.T, l *Ledger) func(Tx, error) Posted {
	return func(tx Tx, err error) Posted {
		t.Helper()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		p, err := l.Post(tx, rec(int(tx.Event.Sub(base).Hours())))
		if err != nil {
			t.Fatalf("post %s: %v", tx.Kind, err)
		}
		return p
	}
}

func doTx(t *testing.T, l *Ledger) func(Tx) Posted {
	return func(tx Tx) Posted {
		t.Helper()
		return do(t, l)(tx, nil)
	}
}

func reject(t *testing.T, l *Ledger, tx Tx, want error, contains string) {
	t.Helper()
	before := l.Projection().Fingerprint()
	n := len(l.Txs())
	_, err := l.Post(tx, rec(50))
	if err == nil {
		t.Fatalf("a %s transaction was accepted", tx.Kind)
	}
	if want != nil && !errors.Is(err, want) {
		t.Fatalf("error %v is not %v", err, want)
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q lacks %q", err, contains)
	}
	if len(l.Txs()) != n || l.Projection().Fingerprint() != before {
		t.Fatal("a refused transaction changed the ledger")
	}
}

func balanced(t *testing.T, l *Ledger) {
	t.Helper()
	p := l.Projection()
	for _, c := range p.Currencies() {
		if tb := p.TrialBalance(c); tb != 0 {
			t.Fatalf("trial balance %s = %d", c, tb)
		}
	}
}

func rebuilt(t *testing.T, l *Ledger) {
	t.Helper()
	hashes := map[string]string{}
	for _, x := range l.Txs() {
		hashes[x.ID] = l.Hash(x.ID)
	}
	r, err := Replay(l.Book, l.Txs(), hashes)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if r.Projection().Fingerprint() != l.Projection().Fingerprint() {
		t.Fatal("the rebuilt ledger differs from the live one")
	}
	for _, x := range l.Txs() {
		if got, _ := r.Projection().Tx(x.ID); got.Seq != x.Seq {
			t.Fatalf("transaction %s moved", x.ID)
		}
	}
}

func funded(t *testing.T, env Env, m CostMethod, minor int64) *Ledger {
	t.Helper()
	l := newL(t, env, m)
	tx, err := l.Deposit(com(0), eur(minor))
	do(t, l)(tx, err)
	return l
}

func TestBuySettleSellRealisesGainAndBalances(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	b := do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("10"), Gross: eur(100_000), Commission: eur(100), SettleDay: "2026-11-04", Price: "100.00"}))
	p := l.Projection()
	if p.Cash("EUR") != 999_900 || p.Unsettled("EUR") != -100_000 || p.Available("EUR") != 899_900 {
		t.Fatalf("after buy: cash %d unsettled %d available %d", p.Cash("EUR"), p.Unsettled("EUR"), p.Available("EUR"))
	}
	s, err := l.SettleTrade(com(3), b.Tx.ID)
	do(t, l)(s, err)
	if p = l.Projection(); p.Cash("EUR") != 899_900 || p.Unsettled("EUR") != 0 {
		t.Fatalf("after settle: cash %d unsettled %d", p.Cash("EUR"), p.Unsettled("EUR"))
	}
	sell := do(t, l)(mustSell(l, Trade{Common: com(4), Security: "ACME", Qty: dec("4"), Gross: eur(50_000), Commission: eur(100)}))
	s2, err := l.SettleTrade(com(6), sell.Tx.ID)
	do(t, l)(s2, err)
	p = l.Projection()
	if p.Cash("EUR") != 949_800 || p.RealizedGain("EUR") != 10_000 || p.Balance("expense:commission", "EUR") != 200 {
		t.Fatalf("cash %d gain %d commission %d", p.Cash("EUR"), p.RealizedGain("EUR"), p.Balance("expense:commission", "EUR"))
	}
	pos := p.Positions()
	if len(pos) != 1 || pos[0].Qty.String() != "6" || pos[0].Cost["EUR"] != 60_000 {
		t.Fatalf("%+v", pos)
	}
	balanced(t, l)
	rebuilt(t, l)
}

func mustBuy(l *Ledger, tr Trade) (Tx, error)  { return l.Buy(tr) }
func mustSell(l *Ledger, tr Trade) (Tx, error) { return l.Sell(tr) }

func TestFIFOAndLIFOConsumeDifferentLots(t *testing.T) {
	for _, tc := range []struct {
		m    CostMethod
		gain int64
	}{{FIFO, 2_000}, {LIFO, 1_000}} {
		l := funded(t, Real, tc.m, 1_000_000)
		do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("5"), Gross: eur(5_000)}))
		do(t, l)(mustBuy(l, Trade{Common: com(2), Security: "ACME", Qty: dec("5"), Gross: eur(6_000)}))
		do(t, l)(mustSell(l, Trade{Common: com(3), Security: "ACME", Qty: dec("5"), Gross: eur(7_000)}))
		if g := l.Projection().RealizedGain("EUR"); g != tc.gain {
			t.Errorf("%s: gain %d, want %d", tc.m, g, tc.gain)
		}
		balanced(t, l)
	}
}

func TestPartialSalesConserveCostWithHalfEvenRounding(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("3"), Gross: eur(10_000)}))
	var costs []int64
	for h := 2; h < 5; h++ {
		before := l.Projection().Balance("gain:realized", "EUR")
		do(t, l)(mustSell(l, Trade{Common: com(h), Security: "ACME", Qty: dec("1"), Gross: eur(4_000)}))
		costs = append(costs, 4_000+(l.Projection().Balance("gain:realized", "EUR")-before))
	}
	if fmt.Sprint(costs) != "[3333 3334 3333]" {
		t.Fatalf("costs %v", costs)
	}
	if costs[0]+costs[1]+costs[2] != 10_000 {
		t.Fatal("cost was not conserved")
	}
	if len(l.Projection().Positions()) != 0 {
		t.Fatal("shares left")
	}
}

func TestOversellIsRefusedEveryWay(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	b := do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("10"), Gross: eur(10_000)}))
	if _, err := l.Sell(Trade{Common: com(2), Security: "ACME", Qty: dec("10.00000001"), Gross: eur(1)}); !errors.Is(err, ErrOversell) {
		t.Fatalf("builder: %v", err)
	}
	if _, err := l.Sell(Trade{Common: com(2), Security: "NONE", Qty: dec("1"), Gross: eur(1)}); !errors.Is(err, ErrOversell) {
		t.Fatalf("unheld: %v", err)
	}
	lot := b.Tx.Entries[0].Lot
	raw := func(qty string, cost int64) Tx {
		n, _ := dec(qty).Neg()
		return Tx{Kind: Sell, Event: at(5), Source: "manual", Entries: []Entry{
			{Account: "securities:ACME", Security: "ACME", Amount: eur(-cost), Qty: n, Lot: lot},
			{Account: "unsettled", Amount: eur(5_000)},
			{Account: "gain:realized", Amount: eur(cost - 5_000)},
		}}
	}
	reject(t, l, raw("11", 11_000), ErrOversell, "")
	reject(t, l, raw("5", 4_000), nil, "proportional cost")
	missing := raw("5", 5_000)
	missing.Entries[0].Lot = "tx_nope#1"
	reject(t, l, missing, nil, "does not exist")
	grow := raw("5", 5_000)
	grow.Entries[0].Qty = dec("5")
	reject(t, l, grow, nil, "consumes shares")
	do(t, l)(raw("5", 5_000), nil)
	balanced(t, l)
}

func TestSellBeforeTheLotExistedIsRefusedAndLateRecordsKeepEarlierCost(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	do(t, l)(mustBuy(l, Trade{Common: com(5), Security: "ACME", Qty: dec("1"), Gross: eur(1_000)}))
	// the sale is dated before the purchase
	tx, err := l.Sell(Trade{Common: com(2), Security: "ACME", Qty: dec("1"), Gross: eur(1_200)})
	if err != nil {
		t.Fatal(err)
	}
	reject(t, l, tx, nil, "opened")

	// a purchase that arrives late and is cheaper cannot change what the earlier sale cost
	do(t, l)(mustSell(l, Trade{Common: com(6), Security: "ACME", Qty: dec("1"), Gross: eur(1_200)}))
	gain := l.Projection().RealizedGain("EUR")
	do(t, l)(mustBuy(l, Trade{Common: com(4), Security: "ACME", Qty: dec("1"), Gross: eur(100)}))
	if g := l.Projection().RealizedGain("EUR"); g != gain || gain != 200 {
		t.Fatalf("gain moved from %d to %d", gain, g)
	}
	rebuilt(t, l)
}

func TestCashCannotGoNegative(t *testing.T) {
	l := funded(t, Real, FIFO, 100_000)
	w, _ := l.Withdraw(com(1), eur(100_001))
	reject(t, l, w, ErrInsufficient, "")
	tx, _ := l.Buy(Trade{Common: com(1), Security: "ACME", Qty: dec("1"), Gross: eur(100_001)})
	reject(t, l, tx, ErrInsufficient, "")
	fee, _ := l.Fee(com(1), eur(100_001), false)
	reject(t, l, fee, ErrInsufficient, "")

	m := newL(t, Real, FIFO)
	m.Book.AllowNegativeCash = true
	w, _ = m.Withdraw(com(1), eur(5))
	do(t, m)(w, nil)
	if m.Projection().Cash("EUR") != -5 {
		t.Fatal("margin books may go negative")
	}
	balanced(t, m)
}

func TestMalformedTransactionsAreRefused(t *testing.T) {
	l := funded(t, Real, FIFO, 100_000)
	ok := func() Tx {
		return Tx{Kind: Deposit, Event: at(1), Source: "manual", Entries: []Entry{
			{Account: "cash", Amount: eur(100)}, {Account: "equity:contributions", Amount: eur(-100)}}}
	}
	t1 := ok()
	t1.Entries[1].Amount = eur(-99)
	reject(t, l, t1, ErrUnbalanced, "EUR off by 1")
	t2 := ok()
	t2.Entries[1] = Entry{Account: "income:dividend", Amount: eur(-100)}
	reject(t, l, t2, nil, "may not post")
	t3 := ok()
	t3.Entries[1].Account = "bank"
	reject(t, l, t3, nil, "unknown account")
	t4 := ok()
	t4.Entries = []Entry{{Account: "cash", Amount: eur(-100)}, {Account: "equity:contributions", Amount: eur(100)}}
	reject(t, l, t4, nil, "right way")
	t5 := ok()
	t5.Source = " "
	reject(t, l, t5, nil, "source")
	t6 := ok()
	t6.Event = time.Time{}
	reject(t, l, t6, nil, "event time")
	t7 := ok()
	t7.Kind = "gift"
	reject(t, l, t7, nil, "unknown transaction kind")
	t8 := ok()
	t8.Entries = nil
	reject(t, l, t8, nil, "needs entries")
	t9 := ok()
	t9.Entries[0].Security = "ACME"
	reject(t, l, t9, nil, "only securities")
	t10 := ok()
	t10.Entries[0].Amount = money.Amount{}
	reject(t, l, t10, nil, "no currency")
	// a deposit in one currency cannot be balanced by another
	t11 := ok()
	t11.Entries[1].Amount = usd(-100)
	reject(t, l, t11, ErrUnbalanced, "")
	// lots cannot be opened by an income transaction
	t12 := Tx{Kind: Income, Event: at(1), Source: "manual", Entries: []Entry{
		{Account: "securities:ACME", Security: "ACME", Amount: eur(100), Qty: dec("1"), NewLot: true},
		{Account: "income:dividend", Amount: eur(-100)}}}
	reject(t, l, t12, nil, "may not post")
	balanced(t, l)
}

func TestImportIsIdempotentAndConflictsAreRefused(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	c := com(1)
	c.ImportID = "stmt-1:line-7"
	first := do(t, l)(mustBuy(l, Trade{Common: c, Security: "ACME", Qty: dec("10"), Gross: eur(100_000)}))
	if first.Existing {
		t.Fatal("first post was a repeat")
	}
	n := len(l.Txs())
	// other trades move the lots on; the same record reposted is still the same record
	c2 := com(2)
	c2.ImportID = "stmt-1:line-8"
	do(t, l)(mustSell(l, Trade{Common: c2, Security: "ACME", Qty: dec("4"), Gross: eur(50_000)}))
	n++
	again, err := l.Post(mustTx(l.Sell(Trade{Common: c2, Security: "ACME", Qty: dec("4"), Gross: eur(50_000)})), rec(9))
	if err != nil || !again.Existing || len(l.Txs()) != n {
		t.Fatalf("repost of the sale: %+v %v (%d transactions, want %d)", again.Existing, err, len(l.Txs()), n)
	}
	if got, ok := l.Imported("stmt-1:line-8"); !ok || got.ID != again.Tx.ID {
		t.Fatal("Imported does not find it")
	}
	// the same ID with other content is a conflict
	diff, err := l.Buy(Trade{Common: c, Security: "ACME", Qty: dec("11"), Gross: eur(100_000)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.Post(diff, rec(9))
	if !errors.Is(err, ErrImportConflict) {
		t.Fatalf("%v", err)
	}
	// hashes survive a rebuild; without them the book refuses to guess
	hashes := map[string]string{}
	for _, x := range l.Txs() {
		hashes[x.ID] = l.Hash(x.ID)
	}
	r, err := Replay(l.Book, l.Txs(), hashes)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := r.Post(mustTx(l.Buy(Trade{Common: c, Security: "ACME", Qty: dec("10"), Gross: eur(100_000)})), rec(9)); err != nil || !p.Existing {
		t.Fatalf("after rebuild: %v", err)
	}
	r2, _ := Replay(l.Book, l.Txs(), nil)
	if _, err := r2.Post(mustTx(l.Buy(Trade{Common: c, Security: "ACME", Qty: dec("10"), Gross: eur(100_000)})), rec(9)); !errors.Is(err, ErrImportConflict) {
		t.Fatalf("without stored hashes: %v", err)
	}
}

func mustTx(tx Tx, err error) Tx {
	if err != nil {
		panic(err)
	}
	return tx
}

func TestContentHashIgnoresRecordingButNotContent(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	a := mustTx(l.Fee(com(1), eur(10), false))
	b := a
	b.ID, b.Seq, b.Recorded = "x", 9, rec(40)
	if ContentHash(a) != ContentHash(b) {
		t.Fatal("recording details changed the hash")
	}
	b.Memo = "other"
	if ContentHash(a) == ContentHash(b) {
		t.Fatal("memo ignored")
	}
	c := mustTx(l.Fee(com(2), eur(10), false))
	if ContentHash(a) == ContentHash(c) {
		t.Fatal("event time ignored")
	}
}

func TestReversalsAreExactAndOrdered(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	b := do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("10"), Gross: eur(100_000), Commission: eur(100)}))
	s := doTx(t, l)(mustTx(l.SettleTrade(com(2), b.Tx.ID)))

	// settled: the settlement goes first
	reject(t, l, mustTx(l.Reverse(com(3), b.Tx.ID)), nil, "settled by")
	doTx(t, l)(mustTx(l.Reverse(com(3), s.Tx.ID)))
	if l.Projection().Unsettled("EUR") != -100_000 {
		t.Fatal("settlement not undone")
	}
	// the settlement can be made again and reversed again
	s2 := doTx(t, l)(mustTx(l.SettleTrade(com(4), b.Tx.ID)))
	doTx(t, l)(mustTx(l.Reverse(com(5), s2.Tx.ID)))
	reject(t, l, mustTx(l.Reverse(com(5), s2.Tx.ID)), nil, "already reversed")

	r := doTx(t, l)(mustTx(l.Reverse(com(6), b.Tx.ID)))
	p := l.Projection()
	if p.Cash("EUR") != 1_000_000 || p.Unsettled("EUR") != 0 || len(p.Positions()) != 0 || p.Balance("expense:commission", "EUR") != 0 {
		t.Fatalf("cash %d unsettled %d positions %v", p.Cash("EUR"), p.Unsettled("EUR"), p.Positions())
	}
	if by, ok := p.Reversed(b.Tx.ID); !ok || by != r.Tx.ID {
		t.Fatal("reversal not recorded")
	}
	reject(t, l, mustTx(l.Reverse(com(7), b.Tx.ID)), nil, "already reversed")
	reject(t, l, mustTx(l.Reverse(com(7), r.Tx.ID)), nil, "cannot be reversed")
	if _, err := l.Reverse(com(7), "tx_nope"); err == nil {
		t.Fatal("reversed something that is not there")
	}
	// the original stays exactly as it was written
	if orig, _ := l.Projection().Tx(b.Tx.ID); len(orig.Entries) != len(b.Tx.Entries) || orig.Entries[0].Amount != b.Tx.Entries[0].Amount {
		t.Fatal("the original changed")
	}
	balanced(t, l)
	rebuilt(t, l)
}

func TestReversalRefusesWhatItCannotRestore(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	b := do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("10"), Gross: eur(10_000)}))
	sell := do(t, l)(mustSell(l, Trade{Common: com(2), Security: "ACME", Qty: dec("4"), Gross: eur(5_000)}))
	reject(t, l, mustTx(l.Reverse(com(3), b.Tx.ID)), nil, "changed again")
	doTx(t, l)(mustTx(l.Reverse(com(3), sell.Tx.ID)))
	sp := doTx(t, l)(mustTx(l.SplitShares(com(4), "ACME", 1, 2)))
	reject(t, l, mustTx(l.Reverse(com(5), b.Tx.ID)), nil, "changed again")
	doTx(t, l)(mustTx(l.Reverse(com(5), sp.Tx.ID)))
	doTx(t, l)(mustTx(l.Reverse(com(6), b.Tx.ID)))
	if len(l.Projection().Positions()) != 0 {
		t.Fatal("shares left")
	}

	// not before what it reverses
	d := funded(t, Real, FIFO, 100)
	first, _ := d.Txs()[0], 0
	back := mustTx(d.Reverse(com(-3), first.ID))
	reject(t, d, back, nil, "before what it reverses")
	// not a different set of numbers
	forged := mustTx(d.Reverse(com(1), first.ID))
	forged.Entries[0].Amount = eur(-99)
	forged.Entries[1].Amount = eur(99)
	reject(t, d, forged, nil, "not the opposite")
	short := mustTx(d.Reverse(com(1), first.ID))
	short.Entries = short.Entries[:1]
	reject(t, d, short, ErrUnbalanced, "")
	// reversing a deposit that has been spent overdraws
	e := funded(t, Real, FIFO, 100)
	doTx(t, e)(mustTx(e.Withdraw(com(1), eur(100))))
	reject(t, e, mustTx(e.Reverse(com(2), e.Txs()[0].ID)), ErrInsufficient, "")
}

func TestUnsettledCashAndSettlementRules(t *testing.T) {
	l := funded(t, Real, FIFO, 100_000)
	b := do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("1"), Gross: eur(60_000)}))
	// 60_000 is committed even though it has not left settled cash
	tx, _ := l.Buy(Trade{Common: com(1), Security: "BETA", Qty: dec("1"), Gross: eur(50_000)})
	reject(t, l, tx, ErrInsufficient, "")
	// a settlement must clear exactly what is due, once
	bad := mustTx(l.SettleTrade(com(2), b.Tx.ID))
	bad.Entries[0].Amount = eur(59_999)
	bad.Entries[1].Amount = eur(-59_999)
	reject(t, l, bad, nil, "must move")
	reject(t, l, mustTx(l.SettleTrade(com(0), b.Tx.ID)), nil, "before the trade")
	doTx(t, l)(mustTx(l.SettleTrade(com(2), b.Tx.ID)))
	if _, err := l.Post(mustTx(l.SettleTrade(com(3), b.Tx.ID)), rec(5)); err == nil {
		t.Fatal("settled twice")
	}
	dep := funded(t, Real, FIFO, 10)
	if _, err := dep.SettleTrade(com(1), dep.Txs()[0].ID); err == nil {
		t.Fatal("a deposit has nothing to settle")
	}
	// a reversed trade cannot be settled
	c := funded(t, Real, FIFO, 100_000)
	cb := do(t, c)(mustBuy(c, Trade{Common: com(1), Security: "ACME", Qty: dec("1"), Gross: eur(1_000)}))
	doTx(t, c)(mustTx(c.Reverse(com(2), cb.Tx.ID)))
	reject(t, c, mustTx(c.SettleTrade(com(3), cb.Tx.ID)), nil, "reversed")
	balanced(t, l)
}

func TestForeignExchangeKeepsNativeLegs(t *testing.T) {
	l := funded(t, Real, FIFO, 100_000)
	doTx(t, l)(mustTx(l.Convert(com(1), eur(10_000), usd(10_800), eur(50), "1.08")))
	p := l.Projection()
	if p.Cash("EUR") != 89_950 || p.Cash("USD") != 10_800 || p.Balance("fx:clearing", "EUR") != 10_000 || p.Balance("fx:clearing", "USD") != -10_800 {
		t.Fatalf("EUR %d USD %d clearing %d/%d", p.Cash("EUR"), p.Cash("USD"), p.Balance("fx:clearing", "EUR"), p.Balance("fx:clearing", "USD"))
	}
	if x := l.Txs()[1]; x.Meta["rate"] != "1.08" || x.Meta["from"] != "10000 EUR" && !strings.Contains(x.Meta["from"], "EUR") {
		t.Fatalf("%v", x.Meta)
	}
	balanced(t, l)
	// each currency stands on its own
	tx, _ := l.Convert(com(2), usd(10_801), eur(9_000), money.Amount{}, "")
	reject(t, l, tx, ErrInsufficient, "")
	if _, err := l.Convert(com(2), eur(1), eur(1), money.Amount{}, ""); err == nil {
		t.Fatal("same currency")
	}
	if _, err := l.Convert(com(2), eur(0), usd(1), money.Amount{}, ""); err == nil {
		t.Fatal("zero amount")
	}
	// USD shares are bought with USD only
	b, err := l.Buy(Trade{Common: com(3), Security: "BETA", Qty: dec("2"), Gross: usd(10_000)})
	do(t, l)(b, err)
	if l.Projection().Unsettled("USD") != -10_000 || l.Projection().Cash("EUR") != 89_950 {
		t.Fatal("wrong currency touched")
	}
	// a lot is held at cost in one currency
	if _, err := l.Sell(Trade{Common: com(4), Security: "BETA", Qty: dec("1"), Gross: eur(5_000)}); err == nil {
		t.Fatal("sold USD lots for EUR without a conversion")
	}
	rebuilt(t, l)
}

func TestIncomeWithWithholdingAndReinvestment(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("10"), Gross: eur(10_000)}))
	doTx(t, l)(mustTx(l.Income(com(2), "dividend", eur(10_000), eur(1_500))))
	p := l.Projection()
	if p.Cash("EUR") != 1_000_000+8_500 || p.Balance("income:dividend", "EUR") != -10_000 || p.Balance("expense:withholding", "EUR") != 1_500 {
		t.Fatalf("%d %d %d", p.Cash("EUR"), p.Balance("income:dividend", "EUR"), p.Balance("expense:withholding", "EUR"))
	}
	if _, err := l.Income(com(2), "windfall", eur(1), money.Amount{}); err == nil {
		t.Fatal("unknown kind")
	}
	if _, err := l.Income(com(2), "dividend", eur(100), eur(100)); err == nil {
		t.Fatal("withholding of everything")
	}
	if _, err := l.Income(com(2), "dividend", eur(100), usd(10)); err == nil {
		t.Fatal("withholding in another currency")
	}
	cash := l.Projection().Cash("EUR")
	doTx(t, l)(mustTx(l.Reinvest(com(3), "ACME", dec("0.5"), eur(10_000), eur(1_500))))
	p = l.Projection()
	if p.Cash("EUR") != cash || p.Held("ACME").String() != "10.5" {
		t.Fatalf("cash %d held %s", p.Cash("EUR"), p.Held("ACME"))
	}
	if pos := p.Positions()[0]; pos.Cost["EUR"] != 10_000+8_500 || pos.Lots != 2 {
		t.Fatalf("%+v", pos)
	}
	balanced(t, l)
	rebuilt(t, l)
}

func TestSplitsRescaleEveryLotAndKeepCost(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("10"), Gross: eur(10_000)}))
	do(t, l)(mustBuy(l, Trade{Common: com(2), Security: "ACME", Qty: dec("5"), Gross: eur(6_000)}))
	doTx(t, l)(mustTx(l.SplitShares(com(3), "ACME", 1, 2)))
	pos := l.Projection().Positions()[0]
	if pos.Qty.String() != "30" || pos.Cost["EUR"] != 16_000 || pos.Lots != 2 {
		t.Fatalf("%+v", pos)
	}
	// a lot bought after the split event is already on the new basis
	do(t, l)(mustBuy(l, Trade{Common: com(4), Security: "ACME", Qty: dec("1"), Gross: eur(500)}))
	doTx(t, l)(mustTx(l.SplitShares(com(5), "ACME", 2, 1)))
	if h := l.Projection().Held("ACME").String(); h != "15.5" {
		t.Fatalf("after a reverse split: %s", h)
	}
	if _, err := l.SplitShares(com(6), "ACME", 3, 1); err == nil || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("15.5 shares do not divide by 3: %v", err)
	}
	if _, err := l.SplitShares(com(6), "ACME", 2, 2); err == nil {
		t.Fatal("a 2-for-2 split")
	}
	if _, err := l.SplitShares(com(6), "NONE", 1, 2); err == nil {
		t.Fatal("split of an unheld security")
	}
	balanced(t, l)

	// hand-built: every lot has to move, with no cost
	h := funded(t, Real, FIFO, 100_000)
	a := do(t, h)(mustBuy(h, Trade{Common: com(1), Security: "ACME", Qty: dec("1"), Gross: eur(100)}))
	do(t, h)(mustBuy(h, Trade{Common: com(1), Security: "ACME", Qty: dec("1"), Gross: eur(100)}))
	raw := Tx{Kind: Split, Event: at(2), Source: "manual", Entries: []Entry{
		{Account: "securities:ACME", Security: "ACME", Amount: eur(0), Qty: dec("1"), Lot: a.Tx.Entries[0].Lot}}}
	reject(t, h, raw, nil, "old basis")
	raw.Entries[0].Amount = eur(5)
	raw.Entries = append(raw.Entries, Entry{Account: "cash", Amount: eur(-5)})
	reject(t, h, raw, nil, "may not post")
}

func TestReservationsHoldCashAndSharesForOpenOrders(t *testing.T) {
	l := funded(t, Real, FIFO, 100_000)
	doTx(t, l)(mustTx(l.ReserveCash(com(1), "ord1", eur(60_000), "buy ACME")))
	if l.Projection().Available("EUR") != 40_000 || l.Projection().Cash("EUR") != 100_000 {
		t.Fatal("a reservation moves no money but limits spending")
	}
	tx, _ := l.Buy(Trade{Common: com(2), Security: "BETA", Qty: dec("1"), Gross: eur(50_000)})
	reject(t, l, tx, ErrInsufficient, "")
	w, _ := l.Withdraw(com(2), eur(40_001))
	reject(t, l, w, ErrInsufficient, "")
	doTx(t, l)(mustTx(l.Withdraw(com(2), eur(40_000))))
	over, err := l.ReserveCash(com(2), "ord2", eur(1), "")
	if err != nil {
		t.Fatal(err)
	}
	reject(t, l, over, ErrInsufficient, "")

	// the fill that names the reservation may spend it
	fill := Trade{Common: com(3), Security: "ACME", Qty: dec("1"), Gross: eur(60_000), Releases: "ord1"}
	do(t, l)(mustBuy(l, fill))
	if len(l.Projection().Reservations()) != 0 {
		t.Fatal("the reservation outlived its fill")
	}
	reject(t, l, mustTx(l.ReserveCash(com(4), "ord1", eur(1), "")), nil, "already used")
	reject(t, l, l.ReleaseReservation(com(4), "ord1"), nil, "not active")
	if _, err := l.ReserveCash(com(4), "x", eur(0), ""); err == nil {
		t.Fatal("zero reservation")
	}

	// shares
	doTx(t, l)(l.ReserveSharesFor(com(5), "ord3", "ACME", dec("1"), "sell ACME"))
	sell, err := l.Sell(Trade{Common: com(6), Security: "ACME", Qty: dec("1"), Gross: eur(1)})
	if !errors.Is(err, ErrOversell) {
		t.Fatalf("a sale of reserved shares: %v %v", sell.Kind, err)
	}
	reject(t, l, l.ReserveSharesFor(com(6), "ord4", "ACME", dec("1"), ""), ErrOversell, "")
	do(t, l)(mustSell(l, Trade{Common: com(7), Security: "ACME", Qty: dec("1"), Gross: eur(61_000), Releases: "ord3"}))
	doTx(t, l)(mustTx(l.ReserveCash(com(8), "ord5", eur(100), "")))
	doTx(t, l)(l.ReleaseReservation(com(9), "ord5"))
	if len(l.Projection().Reservations()) != 0 || l.Projection().Available("EUR") != l.Projection().Cash("EUR")+l.Projection().Unsettled("EUR") {
		t.Fatal("release")
	}
	balanced(t, l)
	rebuilt(t, l)
}

func TestTransfersMoveCashAndLotsBetweenBooksOfOneEnvironment(t *testing.T) {
	a, b := newNamed(t, "a", PaperLocal, FIFO), newNamed(t, "b", PaperLocal, FIFO)
	doTx(t, a)(mustTx(a.Deposit(com(0), eur(100_000))))
	tc := TransferArgs{Common: Common{Event: at(1), Source: "manual", ImportID: "tr-1"}, Cash: eur(30_000)}
	po, pi, err := PostTransfer(a, b, tc, rec(1))
	if err != nil || po.Existing || pi.Existing {
		t.Fatal(err)
	}
	if a.Projection().Cash("EUR") != 70_000 || b.Projection().Cash("EUR") != 30_000 {
		t.Fatal("cash did not move")
	}
	if po.Tx.Meta["transfer_id"] == "" || po.Tx.Meta["transfer_id"] != pi.Tx.Meta["transfer_id"] {
		t.Fatal("the legs do not name each other")
	}
	// the same transfer again changes nothing
	po2, pi2, err := PostTransfer(a, b, tc, rec(2))
	if err != nil || !po2.Existing || !pi2.Existing || len(a.Txs()) != 2 || len(b.Txs()) != 1 {
		t.Fatalf("repost: %v", err)
	}

	// shares keep their cost and acquisition day
	do(t, a)(mustBuy(a, Trade{Common: com(2), Security: "ACME", Qty: dec("10"), Gross: eur(10_000)}))
	_, _, err = PostTransfer(a, b, TransferArgs{Common: Common{Event: at(3), Source: "manual", ImportID: "tr-2"}, Security: "ACME", Qty: dec("4")}, rec(3))
	if err != nil {
		t.Fatal(err)
	}
	pa, pb := a.Projection(), b.Projection()
	if pa.Held("ACME").String() != "6" || pb.Held("ACME").String() != "4" || pa.Positions()[0].Cost["EUR"] != 6_000 || pb.Positions()[0].Cost["EUR"] != 4_000 {
		t.Fatalf("%+v %+v", pa.Positions(), pb.Positions())
	}
	if pb.OpenLots("ACME", FIFO)[0].Acquired != "2026-11-02" {
		t.Fatal("acquisition day lost")
	}
	// the receiving book can sell them
	do(t, b)(mustSell(b, Trade{Common: com(4), Security: "ACME", Qty: dec("4"), Gross: eur(5_000)}))
	if b.Projection().RealizedGain("EUR") != 1_000 {
		t.Fatal("cost did not travel")
	}
	balanced(t, a)
	balanced(t, b)
	rebuilt(t, a)
	rebuilt(t, b)
}

func TestTransfersNeverCrossEnvironmentsOrHalfHappen(t *testing.T) {
	real1, paper, broker := newNamed(t, "r", Real, FIFO), newNamed(t, "p", PaperLocal, FIFO), newNamed(t, "q", PaperBroker, FIFO)
	for _, l := range []*Ledger{real1, paper, broker} {
		doTx(t, l)(mustTx(l.Deposit(com(0), eur(10_000))))
	}
	cash := TransferArgs{Common: com(1), Cash: eur(100)}
	for _, pair := range [][2]*Ledger{{real1, paper}, {paper, real1}, {paper, broker}, {broker, real1}} {
		before := [2]string{pair[0].Projection().Fingerprint(), pair[1].Projection().Fingerprint()}
		if _, _, err := PostTransfer(pair[0], pair[1], cash, rec(1)); err == nil || !strings.Contains(err.Error(), "never exchange") {
			t.Fatalf("%s -> %s: %v", pair[0].Book.Env, pair[1].Book.Env, err)
		}
		if pair[0].Projection().Fingerprint() != before[0] || pair[1].Projection().Fingerprint() != before[1] {
			t.Fatal("a refused transfer changed a book")
		}
	}
	if _, _, err := PostTransfer(paper, paper, cash, rec(1)); err == nil {
		t.Fatal("transfer to itself")
	}
	both := TransferArgs{Common: com(1), Cash: eur(1), Security: "ACME", Qty: dec("1")}
	if _, _, err := PostTransfer(paper, newNamed(t, "p2", PaperLocal, FIFO), both, rec(1)); err == nil {
		t.Fatal("cash and shares at once")
	}
	if _, _, err := PostTransfer(paper, newNamed(t, "p2", PaperLocal, FIFO), TransferArgs{Common: com(1)}, rec(1)); err == nil {
		t.Fatal("nothing to move")
	}

	// the sending side fails: the receiving side is untouched
	p2 := newNamed(t, "p2", PaperLocal, FIFO)
	fp := p2.Projection().Fingerprint()
	if _, _, err := PostTransfer(paper, p2, TransferArgs{Common: com(1), Cash: eur(10_001)}, rec(1)); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("%v", err)
	}
	if _, _, err := PostTransfer(paper, p2, TransferArgs{Common: com(1), Security: "ACME", Qty: dec("1")}, rec(1)); !errors.Is(err, ErrOversell) {
		t.Fatalf("%v", err)
	}
	// the receiving side fails: the sending side is untouched
	fpPaper := paper.Projection().Fingerprint()
	if err := p2.Archive(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PostTransfer(paper, p2, TransferArgs{Common: com(1), Cash: eur(5)}, rec(1)); !errors.Is(err, ErrArchived) {
		t.Fatalf("%v", err)
	}
	if paper.Projection().Fingerprint() != fpPaper || p2.Projection().Fingerprint() != fp {
		t.Fatal("a half transfer happened")
	}
	// reserved shares do not leave
	do(t, paper)(mustBuy(paper, Trade{Common: com(1), Security: "ACME", Qty: dec("2"), Gross: eur(100)}))
	doTx(t, paper)(paper.ReserveSharesFor(com(2), "o", "ACME", dec("2"), ""))
	p3 := newNamed(t, "p3", PaperLocal, FIFO)
	if _, _, err := PostTransfer(paper, p3, TransferArgs{Common: com(3), Security: "ACME", Qty: dec("1")}, rec(3)); !errors.Is(err, ErrOversell) {
		t.Fatalf("%v", err)
	}
}

func TestOnlyPaperBooksAreResetAndArchivedBooksTakeNothing(t *testing.T) {
	r := funded(t, Real, FIFO, 100)
	if err := r.Archive(); err == nil || !strings.Contains(err.Error(), "cannot be archived or reset") {
		t.Fatalf("a real book was closed: %v", err)
	}
	if r.Book.Status != Active {
		t.Fatal("status changed")
	}
	for _, env := range []Env{PaperLocal, PaperBroker} {
		p := newNamed(t, "x", env, FIFO)
		doTx(t, p)(mustTx(p.Deposit(com(0), eur(100))))
		if err := p.Archive(); err != nil {
			t.Fatal(err)
		}
		reject(t, p, mustTx(p.Deposit(com(1), eur(1))), ErrArchived, "")
		// an archived book still replays and still answers
		again, err := Replay(p.Book, p.Txs(), nil)
		if err != nil || again.Book.Status != Archived || again.Projection().Cash("EUR") != 100 {
			t.Fatalf("%v", err)
		}
	}
	// a paper reset starts a new book; it cannot reach into the old one or into a real book
	old := newNamed(t, "paper-1", PaperLocal, FIFO)
	doTx(t, old)(mustTx(old.Deposit(com(0), eur(500))))
	_ = old.Archive()
	fresh := newNamed(t, "paper-2", PaperLocal, FIFO)
	if len(fresh.Txs()) != 0 || fresh.Projection().Cash("EUR") != 0 {
		t.Fatal("a reset book starts empty")
	}
	if _, _, err := PostTransfer(old, fresh, TransferArgs{Common: com(1), Cash: eur(1)}, rec(1)); !errors.Is(err, ErrArchived) {
		t.Fatalf("%v", err)
	}
}

func TestBookAndAccountCodes(t *testing.T) {
	good := Book{ID: "b", Account: "main", Name: "core", Env: Real, Base: "EUR", Method: FIFO}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*Book){
		"account": func(b *Book) { b.Account = "has space" },
		"name":    func(b *Book) { b.Name = "" },
		"env":     func(b *Book) { b.Env = "live" },
		"base":    func(b *Book) { b.Base = "XXX" },
		"method":  func(b *Book) { b.Method = "average" },
	} {
		b := good
		edit(&b)
		if b.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := NewLedger(Book{}); err == nil {
		t.Fatal("no ID")
	}
	for _, c := range []string{"cash", "unsettled", "securities:ACME_B", "income:dividend", "expense:withholding", "fx:clearing", "receivable:dividend"} {
		if !ValidAccount(c) {
			t.Errorf("%s", c)
		}
	}
	for _, c := range []string{"", "bank", "securities:", "securities:a b", "income:salary", "cash "} {
		if ValidAccount(c) {
			t.Errorf("%q", c)
		}
	}
}

func TestReplayRefusesACorruptJournal(t *testing.T) {
	l := funded(t, Real, FIFO, 100_000)
	do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("1"), Gross: eur(90_000)}))
	doTx(t, l)(mustTx(l.Fee(com(2), eur(10), false)))
	good := l.Txs()

	cp := func() []Tx { return l.Txs() }
	tamper := cp()
	tamper[0].Entries[0].Amount = eur(10_000) // the deposit now balances nothing
	if _, err := Replay(l.Book, tamper, nil); err == nil || !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("one-sided edit: %v", err)
	}
	both := cp()
	both[0].Entries[0].Amount, both[0].Entries[1].Amount = eur(10_000), eur(-10_000)
	if _, err := Replay(l.Book, both, nil); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("a smaller deposit that still balances must overdraw the later buy: %v", err)
	}
	swap := cp()
	swap[1], swap[2] = swap[2], swap[1]
	if _, err := Replay(l.Book, swap, nil); err == nil || !strings.Contains(err.Error(), "sequence") {
		t.Fatalf("reordered: %v", err)
	}
	gap := append(cp()[:1], cp()[2:]...)
	if _, err := Replay(l.Book, gap, nil); err == nil {
		t.Fatal("a missing transaction")
	}
	id := cp()
	id[1].ID = TxID("other", 2)
	if _, err := Replay(l.Book, id, nil); err == nil || !strings.Contains(err.Error(), "wrong ID") {
		t.Fatalf("%v", err)
	}
	if _, err := Replay(Book{ID: "b1", Account: "acct", Name: "b1", Env: Real, Base: "EUR", Method: FIFO}, good, nil); err != nil {
		t.Fatalf("the untouched journal must replay: %v", err)
	}
	// another book's journal is not this book's
	other := newNamed(t, "b2", Real, FIFO)
	if _, err := Replay(other.Book, good, nil); err == nil {
		t.Fatal("replayed the journal of another book")
	}
}

func TestPointInTimeReplayMatchesTheLedgerAsItWas(t *testing.T) {
	l := newL(t, Real, FIFO)
	var prints []string
	var events []time.Time
	step := func(tx Tx, err error) {
		do(t, l)(tx, err)
		prints = append(prints, l.Projection().Fingerprint())
		events = append(events, tx.Event)
	}
	step(l.Deposit(com(0), eur(1_000_000)))
	step(l.Buy(Trade{Common: com(1), Security: "ACME", Qty: dec("10"), Gross: eur(10_000)}))
	step(l.Buy(Trade{Common: com(2), Security: "ACME", Qty: dec("10"), Gross: eur(12_000)}))
	step(l.Sell(Trade{Common: com(3), Security: "ACME", Qty: dec("15"), Gross: eur(20_000)}))
	step(l.SplitShares(com(4), "ACME", 1, 2))
	step(l.Income(com(5), "dividend", eur(500), eur(75)))
	for i, ev := range events {
		got, err := ReplayAsOf(l.Book, l.Txs(), ev)
		if err != nil {
			t.Fatal(err)
		}
		if got.Projection().Fingerprint() != prints[i] {
			t.Errorf("as of step %d the rebuilt book differs", i+1)
		}
	}
}

// A seeded random walk through every kind of transaction. Whatever is refused or
// accepted, the books must balance in every currency, never hold negative cash or
// shares, and rebuild exactly from their journal and as of every earlier moment.
func TestRandomJournalsAlwaysRebuildAndBalance(t *testing.T) {
	counts := map[Kind]int{}
	for seed := int64(1); seed <= 24; seed++ {
		rng := rand.New(rand.NewSource(seed))
		l := newNamed(t, fmt.Sprintf("rb%d", seed), Real, []CostMethod{FIFO, LIFO}[seed%2])
		var prints []string
		var events []time.Time
		secs := []struct {
			id  string
			cur money.Currency
		}{{"ACME", "EUR"}, {"BETA", "USD"}}
		for i := 0; i < 120; i++ {
			c := Common{Event: at(i), Source: "random"}
			if rng.Intn(4) == 0 {
				c.ImportID = fmt.Sprintf("imp-%d", rng.Intn(60))
			}
			s := secs[rng.Intn(2)]
			cash := func(m int64) money.Amount { return amt(m, s.cur) }
			qty := dec(fmt.Sprint(1 + rng.Intn(9)))
			gross := int64(100 * (1 + rng.Intn(400)))
			var tx Tx
			var err error
			switch op := rng.Intn(15); op {
			case 0, 1:
				tx, err = l.Deposit(c, cash(int64(10_000*(1+rng.Intn(50)))))
			case 2:
				tx, err = l.Withdraw(c, cash(int64(100*(1+rng.Intn(500)))))
			case 3, 4, 5:
				tx, err = l.Buy(Trade{Common: c, Security: s.id, Qty: qty, Gross: cash(gross), Commission: cash(int64(rng.Intn(3) * 50))})
			case 6, 7:
				tx, err = l.Sell(Trade{Common: c, Security: s.id, Qty: qty, Gross: cash(gross)})
			case 8, 9:
				var open []string
				for _, x := range l.Txs() {
					if _, done := l.p.settledBy[x.ID]; !done && (x.Kind == Buy || x.Kind == Sell) {
						open = append(open, x.ID)
					}
				}
				if len(open) == 0 {
					continue
				}
				tx, err = l.SettleTrade(c, open[rng.Intn(len(open))])
			case 10:
				tx, err = l.Convert(c, eur(int64(100*(1+rng.Intn(300)))), usd(int64(100*(1+rng.Intn(300)))), money.Amount{}, "")
			case 11:
				all := l.Txs()
				if len(all) == 0 {
					continue
				}
				tx, err = l.Reverse(c, all[rng.Intn(len(all))].ID)
			case 12:
				tx, err = l.Income(c, "dividend", cash(int64(100*(1+rng.Intn(20)))), cash(int64(rng.Intn(3))))
			case 13:
				if rng.Intn(3) == 0 {
					tx, err = l.SplitShares(c, s.id, 1, 2)
				} else {
					tx, err = l.ReserveCash(c, fmt.Sprintf("r%d", i), cash(int64(100*(1+rng.Intn(50)))), "")
				}
			case 14:
				if rs := l.Projection().Reservations(); len(rs) > 0 {
					tx = l.ReleaseReservation(c, rs[rng.Intn(len(rs))].ID)
				} else {
					tx = l.ReserveSharesFor(c, fmt.Sprintf("s%d", i), s.id, qty, "")
				}
			}
			if err != nil {
				continue
			}
			p, perr := l.Post(tx, c.Event.Add(time.Minute))
			if perr != nil {
				continue
			}
			if !p.Existing {
				counts[p.Tx.Kind]++
				prints = append(prints, l.Projection().Fingerprint())
				events = append(events, p.Tx.Event)
			}
			pr := l.Projection()
			for _, cur := range pr.Currencies() {
				if pr.TrialBalance(cur) != 0 || pr.Cash(cur) < 0 {
					t.Fatalf("seed %d step %d: %s trial balance %d cash %d", seed, i, cur, pr.TrialBalance(cur), pr.Cash(cur))
				}
			}
			for _, pos := range pr.Positions() {
				if pos.Qty.Sign() <= 0 {
					t.Fatalf("seed %d: %+v", seed, pos)
				}
			}
		}
		rebuilt(t, l)
		for i, ev := range events {
			got, err := ReplayAsOf(l.Book, l.Txs(), ev)
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			if got.Projection().Fingerprint() != prints[i] {
				t.Fatalf("seed %d: as of step %d the rebuilt book differs", seed, i+1)
			}
		}
	}
	for _, k := range []Kind{Deposit, Withdrawal, Buy, Sell, Settle, FX, Reversal, Income, Split, Reserve, Release} {
		if counts[k] == 0 {
			t.Errorf("the random walk never produced a %s", k)
		}
	}
}

func TestStatementReconciliationReportsEveryDifference(t *testing.T) {
	l := funded(t, Real, FIFO, 1_000_000)
	b := do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "ACME", Qty: dec("10"), Gross: eur(100_000)}))
	do(t, l)(mustBuy(l, Trade{Common: com(1), Security: "BETA", Qty: dec("2.5"), Gross: eur(5_000)}))
	parse := func(body string) Statement {
		t.Helper()
		st, err := ParseStatementCSV(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	// unsettled trades have not moved settled cash, so the statement matches as it stands
	st := parse("kind,security,qty,currency,amount\ncash,,,EUR,10000.00\nposition,ACME,10,,\nposition,BETA,2.50,,\n")
	if diffs := Reconcile(l.Projection(), st); len(diffs) != 0 {
		t.Fatalf("%+v", diffs)
	}
	do(t, l)(l.SettleTrade(com(3), b.Tx.ID))
	// a statement whose trade settles later than the ledger says
	late := parse("kind,security,qty,currency,amount\ncash,,,EUR,9000.00\nposition,ACME,10,,\nposition,BETA,2.5,,\n")
	if d := Reconcile(l.Projection(), late); len(d) != 0 {
		t.Fatalf("the ledger has settled ACME and holds BETA unsettled: %+v", d)
	}
	st = parse("kind,security,qty,currency,amount\ncash,,,EUR,9000.00\ncash,,,USD,5.00\nposition,ACME,9,,\nposition,GAMMA,1,,\n")
	diffs := Reconcile(l.Projection(), st)
	kinds := map[string]DiffKind{}
	for _, d := range diffs {
		kinds[d.Item] = d.Kind
	}
	want := map[string]DiffKind{"cash USD": CashDiffers, "ACME": QuantityDiffers, "GAMMA": NotInLedger, "BETA": NotInStatement}
	if len(kinds) != len(want) {
		t.Fatalf("%+v", diffs)
	}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("%s: %q, want %q (%+v)", k, kinds[k], v, diffs)
		}
	}
	// settled cash short of a pending settlement is recognised, not guessed away
	l2 := funded(t, Real, FIFO, 1_000_000)
	do(t, l2)(mustBuy(l2, Trade{Common: com(1), Security: "ACME", Qty: dec("1"), Gross: eur(1_000)}))
	pending := Reconcile(l2.Projection(), parse("kind,security,qty,currency,amount\ncash,,,EUR,9990.00\nposition,ACME,1,,\n"))
	if len(pending) != 1 || pending[0].Kind != UnsettledPending {
		t.Fatalf("%+v", pending)
	}
	// the ledger is untouched by all of this
	before := l.Projection().Fingerprint()
	Reconcile(l.Projection(), st)
	if l.Projection().Fingerprint() != before {
		t.Fatal("reconcile changed the ledger")
	}
}

func TestStatementParsingIsStrict(t *testing.T) {
	for name, body := range map[string]string{
		"unknown kind":      "kind,security,qty,currency,amount\nholding,ACME,1,,\n",
		"cash with qty":     "kind,security,qty,currency,amount\ncash,,3,EUR,1.00\n",
		"position amount":   "kind,security,qty,currency,amount\nposition,ACME,1,EUR,1.00\n",
		"bad currency":      "kind,security,qty,currency,amount\ncash,,,XXX,1.00\n",
		"too many digits":   "kind,security,qty,currency,amount\ncash,,,EUR,1.001\n",
		"duplicate cash":    "kind,security,qty,currency,amount\ncash,,,EUR,1.00\ncash,,,EUR,2.00\n",
		"duplicate shares":  "kind,security,qty,currency,amount\nposition,ACME,1,,\nposition,ACME,2,,\n",
		"zero shares":       "kind,security,qty,currency,amount\nposition,ACME,0,,\n",
		"bad security":      "kind,security,qty,currency,amount\nposition,AC ME,1,,\n",
		"nine digit shares": "kind,security,qty,currency,amount\nposition,ACME,0.123456789,,\n",
		"no rows":           "kind,security,qty,currency,amount\n",
		"unknown column":    "kind,isin\ncash,x\n",
	} {
		if _, err := ParseStatementCSV(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
