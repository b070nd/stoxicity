package income

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

var base = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func day(n int) time.Time { return base.AddDate(0, 0, n) } // day(8) is 2026-11-10

func dec(s string) money.Decimal { return money.MustDecimal(s) }
func eur(m int64) money.Amount   { a, _ := money.FromMinor(m, "EUR"); return a }
func com(n int) portfolio.Common { return portfolio.Common{Event: day(n), Source: "test"} }

func book(t *testing.T) *portfolio.Ledger {
	t.Helper()
	l, err := portfolio.NewLedger(portfolio.Book{ID: "b", Account: "a", Name: "n", Env: portfolio.Real, Base: "EUR", Method: portfolio.FIFO})
	if err != nil {
		t.Fatal(err)
	}
	do(t, l)(l.Deposit(com(0), eur(10_000_000)))
	return l
}

func do(t *testing.T, l *portfolio.Ledger) func(portfolio.Tx, error) portfolio.Posted {
	return func(tx portfolio.Tx, err error) portfolio.Posted {
		t.Helper()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		p, err := l.Post(tx, tx.Event.Add(time.Minute))
		if err != nil {
			t.Fatalf("post %s: %v", tx.Kind, err)
		}
		return p
	}
}

func buy(t *testing.T, l *portfolio.Ledger, n int, qty string) portfolio.Posted {
	t.Helper()
	return do(t, l)(l.Buy(portfolio.Trade{Common: com(n), Security: "ACME", Qty: dec(qty), Gross: eur(100_000)}))
}

func payout(typ distribution.Type, state distribution.State, perShare string) distribution.Payout {
	return distribution.Payout{
		Key: distribution.Key{SecurityID: "ACME", Type: typ, Period: "2026Q3"}, State: state,
		Amount: dec(perShare), Currency: "EUR", ExDay: "2026-11-10", PayDay: "2026-11-20",
	}
}

func declared(perShare string) distribution.Payout {
	return payout(distribution.Ordinary, distribution.StateDeclared, perShare)
}

const after = "2026-11-12"

func TestEntitlementFollowsRecordedFillsAroundTheExDate(t *testing.T) {
	l := book(t)
	buy(t, l, 7, "10")                                                                                     // 2026-11-09, the day before: earns
	buy(t, l, 8, "5")                                                                                      // on the ex-date: does not earn
	buy(t, l, 9, "3")                                                                                      // after: does not earn
	do(t, l)(l.Sell(portfolio.Trade{Common: com(8), Security: "ACME", Qty: dec("4"), Gross: eur(50_000)})) // sold on the ex-date: still earns on 10
	e := Expect(l, declared("0.50"), after)
	if e.Status != Entitled || e.Shares.String() != "10" || e.Gross.String() != "5.00 EUR" {
		t.Fatalf("%+v", e)
	}

	// a sale before the ex-date removes the entitlement of those shares
	l2 := book(t)
	buy(t, l2, 5, "10")
	do(t, l2)(l2.Sell(portfolio.Trade{Common: com(6), Security: "ACME", Qty: dec("10"), Gross: eur(100_000)}))
	if e := Expect(l2, declared("0.50"), after); e.Status != NotEntitled {
		t.Fatalf("%+v", e)
	}
	// nothing at all held
	if e := Expect(book(t), declared("0.50"), after); e.Status != NotEntitled || !strings.Contains(e.Reason, "no share") {
		t.Fatalf("%+v", e)
	}
}

func TestUnfilledOrdersEarnNothingAndPartialFillsEarnOnTheFilledPart(t *testing.T) {
	l := book(t)
	do(t, l)(l.ReserveCash(com(5), "ord1", eur(200_000), "buy 10"))
	if e := Expect(l, declared("1.00"), after); e.Status != NotEntitled {
		t.Fatalf("an open order is not a holding: %+v", e)
	}
	// 3 of the 10 shares filled before the ex-date, the rest after
	do(t, l)(l.Buy(portfolio.Trade{Common: com(6), Security: "ACME", Qty: dec("3"), Gross: eur(30_000)}))
	do(t, l)(l.Buy(portfolio.Trade{Common: com(9), Security: "ACME", Qty: dec("7"), Gross: eur(70_000)}))
	e := Expect(l, declared("1.00"), after)
	if e.Status != Entitled || e.Shares.String() != "3" || e.Gross.String() != "3.00 EUR" {
		t.Fatalf("%+v", e)
	}
}

func TestFractionalSharesRoundOnceOnTheTotalHalfEven(t *testing.T) {
	l := book(t)
	do(t, l)(l.Buy(portfolio.Trade{Common: com(5), Security: "ACME", Qty: dec("0.5"), Gross: eur(5_000)}))
	// 0.5 x 0.25 = 0.125: a tie, rounded to the even cent
	if e := Expect(l, declared("0.25"), after); e.Gross.String() != "0.12 EUR" {
		t.Fatalf("%+v", e)
	}
	// 0.5 x 0.35 = 0.175 -> 0.18
	if e := Expect(l, declared("0.35"), after); e.Gross.String() != "0.18 EUR" {
		t.Fatalf("%+v", e)
	}
	// a split after the ex-date does not change what was held at it
	l2 := book(t)
	buy(t, l2, 5, "10")
	do(t, l2)(l2.SplitShares(com(9), "ACME", 1, 2))
	if e := Expect(l2, declared("1.00"), after); e.Shares.String() != "10" {
		t.Fatalf("%+v", e)
	}
	// and one before it does
	l3 := book(t)
	buy(t, l3, 3, "10")
	do(t, l3)(l3.SplitShares(com(5), "ACME", 1, 2))
	if e := Expect(l3, declared("1.00"), after); e.Shares.String() != "20" {
		t.Fatalf("%+v", e)
	}
}

func TestOnlyDeclaredOrdinaryPayoutsAreComputed(t *testing.T) {
	l := book(t)
	buy(t, l, 5, "10")
	for name, tc := range map[string]struct {
		p    distribution.Payout
		want Status
		why  string
	}{
		"estimate":  {payout(distribution.Ordinary, distribution.StateEstimated, "1"), NoIncome, "estimate"},
		"cancelled": {payout(distribution.Ordinary, distribution.StateCancelled, "1"), NoIncome, "cancelled"},
		"conflict":  {payout(distribution.Ordinary, distribution.StateConflicting, "1"), Review, "disagree"},
		"special":   {payout(distribution.Special, distribution.StateDeclared, "1"), Unsupported, "due-bill"},
		"capital":   {payout(distribution.ReturnOfCapital, distribution.StateDeclared, "1"), Unsupported, "not modelled"},
		"unknown":   {payout(distribution.TypeUnknown, distribution.StateDeclared, "1"), Unsupported, "not modelled"},
	} {
		e := Expect(l, tc.p, after)
		if e.Status != tc.want || !strings.Contains(e.Reason, tc.why) || e.Gross.Valid() {
			t.Errorf("%s: %+v", name, e)
		}
	}
	if e := Expect(l, declared("1"), "2026-11-09"); e.Status != Pending || !e.Gross.Valid() {
		t.Fatalf("before the ex-date: %+v", e)
	}
}

func TestAccrualPaymentAndWithholdingReconcileOnce(t *testing.T) {
	l := book(t)
	buy(t, l, 5, "10")
	p := declared("1.00")
	key := PayoutKey(p.Key)
	e := Expect(l, p, after)
	if f := Check(l, []distribution.Payout{p}, after); len(f) != 1 || f[0].Kind != MissingAccrual {
		t.Fatalf("%+v", f)
	}
	acc := do(t, l)(l.Accrue(portfolio.Common{Event: day(8), Source: "test", ImportID: "accrual:" + key}, key, e.Gross))
	if f := Check(l, []distribution.Payout{p}, after); len(f) != 0 {
		t.Fatalf("%+v", f)
	}
	pr := l.Projection()
	if pr.Balance("receivable:dividend", "EUR") != 1_000 || pr.Cash("EUR") != 10_000_000 || pr.Balance("income:dividend", "EUR") != -1_000 {
		t.Fatalf("receivable %d cash %d income %d", pr.Balance("receivable:dividend", "EUR"), pr.Cash("EUR"), pr.Balance("income:dividend", "EUR"))
	}
	// the pay date has passed with nothing received
	if f := Check(l, []distribution.Payout{p}, "2026-11-25"); len(f) != 1 || f[0].Kind != PaymentOverdue {
		t.Fatalf("%+v", f)
	}
	// booking it again, by any other means, would count the income twice
	if _, err := l.Post(mustTx(l.Accrue(portfolio.Common{Event: day(9), Source: "t", ImportID: "again"}, key, e.Gross)), day(9)); err == nil || !strings.Contains(err.Error(), "already booked") {
		t.Fatalf("second accrual: %v", err)
	}
	if _, err := l.Post(mustTx(l.ReceiveDirect(portfolio.Common{Event: day(9), Source: "t"}, key, eur(1_000), money.Amount{})), day(9)); err == nil {
		t.Fatal("direct receipt after an accrual")
	}
	cash := l.Projection().Cash("EUR")
	do(t, l)(l.Receive(portfolio.Common{Event: day(18), Source: "test"}, key, eur(150)))
	pr = l.Projection()
	if pr.Cash("EUR") != cash+850 || pr.Balance("receivable:dividend", "EUR") != 0 || pr.Balance("expense:withholding", "EUR") != 150 || pr.Balance("income:dividend", "EUR") != -1_000 {
		t.Fatalf("cash %+d receivable %d", pr.Cash("EUR")-cash, pr.Balance("receivable:dividend", "EUR"))
	}
	if f := Check(l, []distribution.Payout{p}, "2026-11-25"); len(f) != 0 {
		t.Fatalf("%+v", f)
	}
	if _, err := l.Receive(portfolio.Common{Event: day(19), Source: "t"}, key, money.Amount{}); err == nil {
		t.Fatal("paid twice")
	}
	if w, ok := Withheld(l, key); !ok || w.String() != "1.50 EUR" {
		t.Fatalf("%v %v", w, ok)
	}
	// the accrual cannot be reversed under a payment; the payment first, then it can be paid again
	if _, err := l.Post(mustTx(l.Reverse(portfolio.Common{Event: day(20), Source: "t"}, acc.Tx.ID)), day(20)); err == nil || !strings.Contains(err.Error(), "reverse the payment first") {
		t.Fatalf("%v", err)
	}
	payTx := l.Txs()[len(l.Txs())-1]
	do(t, l)(l.Reverse(portfolio.Common{Event: day(20), Source: "t"}, payTx.ID))
	do(t, l)(l.Receive(portfolio.Common{Event: day(21), Source: "t"}, key, eur(200)))
	if l.Projection().Balance("receivable:dividend", "EUR") != 0 || l.Projection().Balance("income:dividend", "EUR") != -1_000 {
		t.Fatal("a corrected payment changed the income")
	}
	balanced(t, l)
	rebuilt(t, l)
}

func TestACorrectedFillIsFlaggedAndFixedByReversal(t *testing.T) {
	l := book(t)
	b := buy(t, l, 5, "10")
	p := declared("1.00")
	key := PayoutKey(p.Key)
	acc := do(t, l)(l.Accrue(portfolio.Common{Event: day(8), Source: "t"}, key, Expect(l, p, after).Gross))
	// the fill is found to have been wrong and is reversed
	do(t, l)(l.Reverse(portfolio.Common{Event: day(9), Source: "t"}, b.Tx.ID))
	f := Check(l, []distribution.Payout{p}, after)
	if len(f) != 1 || f[0].Kind != AccrualUnbacked {
		t.Fatalf("%+v", f)
	}
	do(t, l)(l.Reverse(portfolio.Common{Event: day(9), Source: "t"}, acc.Tx.ID))
	if f := Check(l, []distribution.Payout{p}, after); len(f) != 0 {
		t.Fatalf("%+v", f)
	}
	// a replacement fill of another size is booked afresh
	buy(t, l, 6, "4")
	e := Expect(l, p, after)
	if e.Shares.String() != "4" {
		t.Fatalf("%+v", e)
	}
	do(t, l)(l.Accrue(portfolio.Common{Event: day(8), Source: "t"}, key, e.Gross))
	if f := Check(l, []distribution.Payout{p}, after); len(f) != 0 {
		t.Fatalf("%+v", f)
	}
	// a fill corrected to another quantity, with the accrual left in place, differs
	l2 := book(t)
	b2 := buy(t, l2, 5, "10")
	do(t, l2)(l2.Accrue(portfolio.Common{Event: day(8), Source: "t"}, key, Expect(l2, p, after).Gross))
	do(t, l2)(l2.Reverse(portfolio.Common{Event: day(9), Source: "t"}, b2.Tx.ID))
	buy(t, l2, 5, "6")
	f = Check(l2, []distribution.Payout{p}, after)
	if len(f) != 1 || f[0].Kind != AccrualDiffers || f[0].Expected != "6.00 EUR" || f[0].Ledger != "10.00 EUR" {
		t.Fatalf("%+v", f)
	}
	// income booked for a payout nobody listed is reported too
	if f := Check(l2, nil, after); len(f) != 1 || f[0].Kind != NotAPayoutOfBook {
		t.Fatalf("%+v", f)
	}
}

func TestReinvestedPayoutClearsTheReceivableWithoutDoubleIncome(t *testing.T) {
	l := book(t)
	buy(t, l, 5, "10")
	p := declared("1.00")
	key := PayoutKey(p.Key)
	do(t, l)(l.Accrue(portfolio.Common{Event: day(8), Source: "t"}, key, eur(1_000)))
	do(t, l)(l.ReinvestPayout(portfolio.Common{Event: day(18), Source: "t"}, key, "ACME", dec("0.1"), eur(1_000), eur(150)))
	pr := l.Projection()
	if pr.Balance("receivable:dividend", "EUR") != 0 || pr.Balance("income:dividend", "EUR") != -1_000 || pr.Held("ACME").String() != "10.1" {
		t.Fatalf("receivable %d income %d held %s", pr.Balance("receivable:dividend", "EUR"), pr.Balance("income:dividend", "EUR"), pr.Held("ACME"))
	}
	if pos := pr.Positions()[0]; pos.Cost["EUR"] != 100_000+850 {
		t.Fatalf("%+v", pos)
	}
	if _, err := l.Receive(portfolio.Common{Event: day(19), Source: "t"}, key, money.Amount{}); err == nil {
		t.Fatal("cash received for a payout that became shares")
	}
	// a reinvestment with no accrual books the income in the same line, once
	k2 := "ACME|ordinary|2027Q1"
	do(t, l)(l.ReinvestPayout(portfolio.Common{Event: day(30), Source: "t"}, k2, "ACME", dec("0.1"), eur(1_000), money.Amount{}))
	if _, err := l.Post(mustTx(l.Accrue(portfolio.Common{Event: day(31), Source: "t"}, k2, eur(1_000))), day(31)); err == nil {
		t.Fatal("accrued after being reinvested")
	}
	balanced(t, l)
	rebuilt(t, l)
}

func TestHandBuiltPayoutLinesAreHeldToTheRules(t *testing.T) {
	l := book(t)
	buy(t, l, 5, "10")
	key := "ACME|ordinary|2026Q3"
	bad := func(name string, tx portfolio.Tx, want string) {
		t.Helper()
		if _, err := l.Post(tx, day(20)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v (want %q)", name, err, want)
		}
	}
	mk := func(meta map[string]string, es ...portfolio.Entry) portfolio.Tx {
		return portfolio.Tx{Kind: portfolio.Income, Event: day(10), Source: "t", Meta: meta, Entries: es}
	}
	rec := func(m int64) portfolio.Entry { return portfolio.Entry{Account: "receivable:dividend", Amount: eur(m)} }
	inc := func(m int64) portfolio.Entry { return portfolio.Entry{Account: "income:dividend", Amount: eur(m)} }
	cash := func(m int64) portfolio.Entry { return portfolio.Entry{Account: "cash", Amount: eur(m)} }
	bad("pay with no accrual and no income", mk(map[string]string{"payout": key, "phase": "pay"}, cash(100), rec(-100)), "has no receivable to clear")
	bad("accrual with cash", mk(map[string]string{"payout": key, "phase": "accrue"}, rec(100), inc(-50), cash(-50)), "nothing else")
	bad("unknown phase", mk(map[string]string{"payout": key, "phase": "bonus"}, cash(100), inc(-100)), "unknown payout phase")
	bad("phase without key", mk(map[string]string{"phase": "pay"}, cash(100), inc(-100)), "needs the payout key")
	bad("direct with receivable", mk(map[string]string{"payout": key, "phase": "direct"}, rec(100), inc(-100)), "no receivable")
	do(t, l)(l.Accrue(portfolio.Common{Event: day(8), Source: "t"}, key, eur(1_000)))
	bad("pay the wrong amount", mk(map[string]string{"payout": key, "phase": "pay"}, cash(900), rec(-900)), "exactly that receivable")
	bad("pay that books income again", mk(map[string]string{"payout": key, "phase": "pay"}, cash(1_000), inc(-1_000), rec(-1_000), rec(1_000)), "twice")
}

func TestGrossAndNetAreNeverConfused(t *testing.T) {
	g := eur(10_000)
	u := OutlookFor(g, nil, nil)
	if u.Basis != TaxUnknown || u.Net.Valid() || !strings.Contains(u.Note, "not known") {
		t.Fatalf("%+v", u)
	}
	w := eur(1_500)
	a := OutlookFor(g, &w, nil)
	if a.Basis != TaxActual || a.Net.String() != "85.00 EUR" || a.Withheld.String() != "15.00 EUR" {
		t.Fatalf("%+v", a)
	}
	r := dec("0.26375")
	s := OutlookFor(g, nil, &r)
	if s.Basis != TaxAssumed || s.Net.String() != "73.62 EUR" || !strings.Contains(s.Note, "never booked") {
		t.Fatalf("%+v", s)
	}
	// an actual payment wins over an assumption
	if o := OutlookFor(g, &w, &r); o.Basis != TaxActual {
		t.Fatalf("%+v", o)
	}
}

func mustTx(tx portfolio.Tx, err error) portfolio.Tx {
	if err != nil {
		panic(err)
	}
	return tx
}

func balanced(t *testing.T, l *portfolio.Ledger) {
	t.Helper()
	p := l.Projection()
	for _, c := range p.Currencies() {
		if p.TrialBalance(c) != 0 {
			t.Fatalf("%s does not balance", c)
		}
	}
}

func rebuilt(t *testing.T, l *portfolio.Ledger) {
	t.Helper()
	hashes := map[string]string{}
	for _, x := range l.Txs() {
		hashes[x.ID] = l.Hash(x.ID)
	}
	r, err := portfolio.Replay(l.Book, l.Txs(), hashes)
	if err != nil {
		t.Fatal(err)
	}
	if r.Projection().Fingerprint() != l.Projection().Fingerprint() {
		t.Fatal("the rebuilt ledger differs")
	}
}
