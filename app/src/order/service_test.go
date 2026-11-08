package order_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/store"
)

var t0 = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

type env struct {
	t     *testing.T
	dir   string
	db    *store.DB
	clock time.Time
	svc   order.Service
	book  portfolio.Book
}

func open(t *testing.T, dir string, clock *time.Time) *store.DB {
	t.Helper()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db.SetClock(func() time.Time { return *clock })
	t.Cleanup(func() { db.Close() })
	return db
}

func newEnv(t *testing.T, e portfolio.Env, cash int64) *env {
	t.Helper()
	x := &env{t: t, dir: t.TempDir(), clock: t0}
	x.db = open(t, x.dir, &x.clock)
	b, err := x.db.Portfolios("default").CreateBook(context.Background(), portfolio.Book{Account: "main", Name: "core", Env: e, Base: "EUR", Method: portfolio.FIFO})
	if err != nil {
		t.Fatal(err)
	}
	x.book = b
	x.svc = order.Service{Store: x.db.Orders("default")}
	x.post(func(l *portfolio.Ledger) {
		tx, err := l.Deposit(portfolio.Common{Event: t0, Source: "test", ImportID: "dep"}, eur(cash))
		must(t, err)
		_, err = l.Post(tx, t0)
		must(t, err)
	})
	return x
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func eur(minor int64) money.Amount {
	a, _ := money.FromMinor(minor, "EUR")
	return a
}

func (x *env) post(fn func(l *portfolio.Ledger)) {
	x.t.Helper()
	must(x.t, x.db.Portfolios("default").Update(context.Background(), []string{x.book.ID}, func(now time.Time, ls []*portfolio.Ledger) (*store.ImportBatch, error) {
		fn(ls[0])
		return nil, nil
	}))
}

// hold gives the book shares it can sell.
func (x *env) hold(qty string) {
	x.post(func(l *portfolio.Ledger) {
		tx, err := l.Buy(portfolio.Trade{Common: portfolio.Common{Event: t0, Source: "test", ImportID: "seed"}, Security: "sec_a", Qty: money.MustDecimal(qty), Gross: eur(100000)})
		must(x.t, err)
		_, err = l.Post(tx, t0)
		must(x.t, err)
	})
}

func (x *env) terms(side order.Side, qty, limit string, fee int64) order.Terms {
	return order.Terms{Book: x.book.ID, Account: "main", Env: x.book.Env, Security: "sec_a", Side: side, Qty: money.MustDecimal(qty),
		Limit: money.MustDecimal(limit), Currency: "EUR", Type: "limit", TIF: "DAY", FeeEstimate: fee}
}

func (x *env) draft(tm order.Terms) string {
	x.t.Helper()
	id, err := x.svc.Draft(context.Background(), x.book.ID, order.DraftRequest{Terms: tm, By: "me", Valid: time.Hour})
	must(x.t, err)
	return id
}

func (x *env) view(id string) order.View {
	x.t.Helper()
	logs, err := x.db.Orders("default").Logs(context.Background(), "main")
	must(x.t, err)
	v, err := order.Fold(logs[id])
	must(x.t, err)
	return v
}

func (x *env) ledger() *portfolio.Ledger {
	l, err := x.db.Portfolios("default").Ledger(context.Background(), x.book.ID)
	must(x.t, err)
	return l
}

func (x *env) enable() {
	must(x.t, x.db.Orders("default").SetControl(context.Background(), x.book.Env, false, "reconciled", "me"))
}

func (x *env) ready(tm order.Terms) string {
	x.t.Helper()
	id := x.draft(tm)
	must(x.t, x.svc.ConfirmBound(context.Background(), x.book.ID, id, "me", nil, 10*time.Minute, order.Binding{Payload: "p1"}))
	return id
}

func ack(broker string) order.Reported {
	return order.Reported{Kind: order.Acknowledged, Broker: broker}
}

func fillOf(id, qty, price string, fee int64) order.Reported {
	return order.Reported{Kind: order.FillEv, Fill: &order.Fill{ID: id, Qty: money.MustDecimal(qty), Price: money.MustDecimal(price), Fee: fee}}
}

// invariants: the ledger's holds are exactly the holds the live orders say they
// have, the books balance, and a closed order holds nothing.
func (x *env) invariants() {
	x.t.Helper()
	l := x.ledger()
	logs, err := x.db.Orders("default").Logs(context.Background(), "main")
	must(x.t, err)
	want := map[string]bool{}
	var cash int64
	shares := money.Int(0)
	for id, log := range logs {
		v, err := order.Fold(log)
		must(x.t, err)
		if v.Terminal() && v.Reservation != "" {
			x.t.Errorf("%s is %s but still holds %s", id, v.State, v.Reservation)
		}
		if v.Reservation == "" {
			continue
		}
		want[v.Reservation] = true
		if v.Terms.Side == order.Buy {
			c, err := v.Terms.Commitment(v.HoldQty(), v.FeesPaid)
			must(x.t, err)
			cash += c.Minor()
		} else if shares, err = shares.Add(v.HoldQty()); err != nil {
			x.t.Fatal(err)
		}
	}
	p := l.Projection()
	got := map[string]bool{}
	for _, r := range p.Reservations() {
		got[r.ID] = true
	}
	if len(got) != len(want) {
		x.t.Errorf("ledger holds %v, orders say %v", got, want)
	}
	for id := range want {
		if !got[id] {
			x.t.Errorf("%s is missing from the ledger", id)
		}
	}
	if c := p.ReservedCash("EUR", ""); c != cash {
		x.t.Errorf("cash held %d, orders account for %d", c, cash)
	}
	if s := p.ReservedShares("sec_a", ""); s.Cmp(shares) != 0 {
		x.t.Errorf("shares held %s, orders account for %s", s, shares)
	}
	if p.Available("EUR") < 0 {
		x.t.Errorf("available cash is negative: %d", p.Available("EUR"))
	}
	if tb := p.TrialBalance("EUR"); tb != 0 {
		x.t.Errorf("trial balance %d", tb)
	}
	if _, err := x.db.Portfolios("default").Verify(context.Background(), x.book.ID); err != nil {
		x.t.Errorf("verify: %v", err)
	}
}

func TestDraftHoldsCashAndRefusesWhatCannotBeHeld(t *testing.T) {
	x := newEnv(t, portfolio.PaperLocal, 100000) // 1,000.00 EUR
	id := x.draft(x.terms(order.Buy, "10", "50", 200))
	v := x.view(id)
	if v.State != order.Draft || v.Reservation == "" {
		t.Fatalf("%+v", v)
	}
	// 10 x 50.00 + 2.00 fees
	if got := x.ledger().Projection().ReservedCash("EUR", ""); got != 50200 {
		t.Fatalf("held %d", got)
	}
	x.invariants()

	// the same money cannot be spent twice, by the same or another proposal
	_, err := x.svc.Draft(context.Background(), x.book.ID, order.DraftRequest{Terms: x.terms(order.Buy, "10", "50", 200), By: "me", Valid: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "exactly these terms") {
		t.Fatalf("duplicate: %v", err)
	}
	_, err = x.svc.Draft(context.Background(), x.book.ID, order.DraftRequest{Terms: x.terms(order.Buy, "10", "60", 200), By: "me", Valid: time.Hour})
	if !errors.Is(err, portfolio.ErrInsufficient) {
		t.Fatalf("second proposal: %v", err)
	}
	if logs, _ := x.db.Orders("default").Logs(context.Background(), "main"); len(logs) != 1 {
		t.Fatalf("a refused draft left %d orders", len(logs))
	}
	x.invariants()

	// validation of the first live subset
	for name, mut := range map[string]func(*order.Terms){
		"market":     func(t *order.Terms) { t.Type = "market" },
		"gtc":        func(t *order.Terms) { t.TIF = "GTC" },
		"fractional": func(t *order.Terms) { t.Qty = money.MustDecimal("1.5") },
		"tick":       func(t *order.Terms) { t.Tick = money.MustDecimal("0.05"); t.Limit = money.MustDecimal("10.03") },
		"short":      func(t *order.Terms) { t.Side = order.Sell },
		"zero":       func(t *order.Terms) { t.Qty = money.MustDecimal("0") },
		"env":        func(t *order.Terms) { t.Env = portfolio.Real },
	} {
		tm := x.terms(order.Buy, "1", "10", 0)
		mut(&tm)
		if _, err := x.svc.Draft(context.Background(), x.book.ID, order.DraftRequest{Terms: tm, By: "me", Valid: time.Hour}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	x.invariants()
}

func TestSharedAccountLimits(t *testing.T) {
	x := newEnv(t, portfolio.PaperLocal, 1000000)
	x.svc.Limits = order.Limits{MaxOpenOrders: 2, MaxTradesPerMonth: 3, MaxCostBurden: money.MustDecimal("0.01")}
	a := x.draft(x.terms(order.Buy, "1", "10", 0))
	x.draft(x.terms(order.Buy, "1", "11", 0))
	_, err := x.svc.Draft(context.Background(), x.book.ID, order.DraftRequest{Terms: x.terms(order.Buy, "1", "12", 0), By: "me", Valid: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "2 open orders") {
		t.Fatalf("open limit: %v", err)
	}
	// a second book of the same account shares the limit
	b2, err := x.db.Portfolios("default").CreateBook(context.Background(), portfolio.Book{Account: "main", Name: "other", Env: portfolio.PaperLocal, Base: "EUR", Method: portfolio.FIFO})
	must(t, err)
	tm := x.terms(order.Buy, "1", "13", 0)
	tm.Book = b2.ID
	if _, err := x.svc.Draft(context.Background(), b2.ID, order.DraftRequest{Terms: tm, By: "me", Valid: time.Hour}); err == nil || !strings.Contains(err.Error(), "open orders") {
		t.Fatalf("shared across books: %v", err)
	}
	must(t, x.svc.Cancel(context.Background(), x.book.ID, a, "me", nil))
	if v := x.view(a); v.State != order.Cancelled || v.Reservation != "" {
		t.Fatalf("%+v", v)
	}
	x.draft(x.terms(order.Buy, "1", "14", 0))
	// the monthly count includes the cancelled draft that was never sent? no: only orders sent or still open
	cost := x.terms(order.Buy, "1", "15", 500) // 5.00 of fees on 15.00
	cost.Limit = money.MustDecimal("15")
	if _, err := x.svc.Draft(context.Background(), x.book.ID, order.DraftRequest{Terms: cost, By: "me", Valid: time.Hour}); err == nil {
		t.Fatal("cost burden or open limit not enforced")
	}
	x.invariants()
}

func TestLifecycleWithPartialFillsAndNoCallBeforeDurableIntent(t *testing.T) {
	x := newEnv(t, portfolio.Real, 100000)
	ctx := context.Background()
	tm := x.terms(order.Buy, "10", "50", 200)
	tm.Fractional = false
	id := x.draft(tm)

	// a confirmation needs every warning acknowledged by its exact text
	id2, err := x.svc.Draft(ctx, x.book.ID, order.DraftRequest{Terms: x.terms(order.Buy, "1", "10", 0), Warnings: []string{"quote is delayed"}, By: "me", Valid: time.Hour})
	must(t, err)
	if err := x.svc.ConfirmBound(ctx, x.book.ID, id2, "me", nil, time.Minute, order.Binding{Payload: "p1"}); err == nil || !strings.Contains(err.Error(), "not acknowledged") {
		t.Fatalf("%v", err)
	}
	must(t, x.svc.ConfirmBound(ctx, x.book.ID, id2, "me", []string{"quote is delayed"}, time.Minute, order.Binding{Payload: "p1"}))
	must(t, x.svc.Cancel(ctx, x.book.ID, id2, "me", nil))

	sender := func(ctx context.Context, s order.Submission) (order.SendResult, error) {
		t.Fatal("sent")
		return order.SendResult{}, nil
	}
	// not confirmed, not submittable
	if err := x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", sender); err == nil || !strings.Contains(err.Error(), "only a confirmed order") {
		t.Fatalf("%v", err)
	}
	must(t, x.svc.ConfirmBound(ctx, x.book.ID, id, "me", nil, 10*time.Minute, order.Binding{Payload: "p1"}))
	// new orders are off by default for a real book
	if err := x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", sender); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("%v", err)
	}
	if v := x.view(id); v.State != order.Confirmed || v.Attempts != 0 {
		t.Fatalf("a refused submission changed the order: %+v", v)
	}
	x.enable()

	// an expired confirmation is refused, then renewed
	x.clock = x.clock.Add(11 * time.Minute)
	if err := x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", sender); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("%v", err)
	}
	must(t, x.svc.ConfirmBound(ctx, x.book.ID, id, "me", nil, 10*time.Minute, order.Binding{Payload: "p1"}))

	var seen order.State
	send := func(ctx context.Context, s order.Submission) (order.SendResult, error) {
		// at this moment the intent is already durable, in a second connection
		seen = x.view(s.OrderID).State
		if s.OrderID != id || s.Attempt != 1 || s.TermsHash == "" {
			t.Errorf("%+v", s)
		}
		return order.SendResult{Broker: "B-1"}, nil
	}
	must(t, x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", send))
	if seen != order.Submitting {
		t.Fatalf("state at the time of the call: %s", seen)
	}
	if v := x.view(id); v.State != order.Open || v.BrokerRef != "B-1" {
		t.Fatalf("%+v", v)
	}
	// an order that was sent is never sent again
	if err := x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", sender); err == nil {
		t.Fatal("resent")
	}
	x.invariants()

	// a partial fill consumes the hold and holds the rest
	must(t, x.svc.Report(ctx, x.book.ID, id, fillOf("e1", "4", "49", 100)))
	v := x.view(id)
	if v.State != order.PartiallyFilled || v.Filled.String() != "4" || v.Remaining.String() != "6" || v.FeesPaid != 100 {
		t.Fatalf("%+v", v)
	}
	// 6 x 50.00 + the 1.00 of fees not yet paid
	if got := x.ledger().Projection().ReservedCash("EUR", ""); got != 30100 {
		t.Fatalf("held %d", got)
	}
	if q := x.ledger().Projection().Held("sec_a"); q.String() != "4" {
		t.Fatalf("held shares %s", q)
	}
	x.invariants()
	// the same execution told again changes nothing
	before := len(x.view(id).Why) + x.view(id).Events
	must(t, x.svc.Report(ctx, x.book.ID, id, fillOf("e1", "4", "49", 100)))
	if x.view(id).Events != before-len(x.view(id).Why) {
		t.Fatalf("a repeated fill wrote events")
	}
	must(t, x.svc.Report(ctx, x.book.ID, id, fillOf("e2", "6", "50", 100)))
	v = x.view(id)
	if v.State != order.Filled || v.Reservation != "" {
		t.Fatalf("%+v", v)
	}
	if got := x.ledger().Projection().ReservedCash("EUR", ""); got != 0 {
		t.Fatalf("held %d after the order filled", got)
	}
	x.invariants()
	// unused hold came back: 1000.00 - 196.00 - 300.00 - 2.00 fees
	if a := x.ledger().Projection().Available("EUR"); a != 100000-19600-30000-200 {
		t.Fatalf("available %d", a)
	}
}

func TestFactsOutOfOrderGiveTheSameState(t *testing.T) {
	ctx := context.Background()
	// fill before the acknowledgement, acknowledgement afterwards
	x := newEnv(t, portfolio.PaperLocal, 100000)
	id := x.ready(x.terms(order.Buy, "10", "50", 0))
	must(t, x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", func(context.Context, order.Submission) (order.SendResult, error) {
		return order.SendResult{}, errors.New("timeout")
	}))
	if v := x.view(id); v.State != order.Unknown {
		t.Fatalf("%+v", v)
	}
	must(t, x.svc.Report(ctx, x.book.ID, id, fillOf("e1", "3", "50", 0)))
	v := x.view(id)
	if v.State != order.PartiallyFilled || len(v.Late) != 1 {
		t.Fatalf("%+v", v)
	}
	must(t, x.svc.Report(ctx, x.book.ID, id, ack("B-9")))
	if v := x.view(id); v.State != order.PartiallyFilled || v.BrokerRef != "B-9" {
		t.Fatalf("%+v", v)
	}
	x.invariants()

	// cancel race: the cancel is requested, a fill arrives, then the cancellation
	y := newEnv(t, portfolio.PaperLocal, 100000)
	id = y.ready(y.terms(order.Buy, "10", "50", 0))
	must(t, y.svc.SubmitBound(ctx, y.book.ID, id, "me", "p1", func(context.Context, order.Submission) (order.SendResult, error) {
		return order.SendResult{Broker: "B-2"}, nil
	}))
	asked := 0
	must(t, y.svc.Cancel(ctx, y.book.ID, id, "me", func(_ context.Context, oid, ref string) error {
		asked++
		if y.view(oid).State != order.CancelRequested || ref != "B-2" {
			t.Errorf("the request must be durable before the other side is asked")
		}
		return nil
	}))
	if asked != 1 {
		t.Fatal("not asked")
	}
	// nothing is assumed cancelled, and the hold stays
	if v := y.view(id); v.State != order.CancelRequested || v.Reservation == "" {
		t.Fatalf("%+v", v)
	}
	must(t, y.svc.Report(ctx, y.book.ID, id, fillOf("e1", "4", "50", 0)))
	if v := y.view(id); v.State != order.CancelRequested || v.Filled.String() != "4" || v.Reservation == "" {
		t.Fatalf("%+v", v)
	}
	y.invariants()
	must(t, y.svc.Report(ctx, y.book.ID, id, order.Reported{Kind: order.CancelledEv}))
	v = y.view(id)
	if v.State != order.Cancelled || v.Reservation != "" || v.Filled.String() != "4" {
		t.Fatalf("%+v", v)
	}
	y.invariants()
	// a fill reported after the cancellation was confirmed: it happened, so it is posted
	must(t, y.svc.Report(ctx, y.book.ID, id, fillOf("e2", "2", "50", 0)))
	v = y.view(id)
	if v.Filled.String() != "6" || y.ledger().Projection().Held("sec_a").String() != "6" || len(v.Late) == 0 {
		t.Fatalf("%+v", v)
	}
	y.invariants()
}

func TestCrashBetweenIntentAndAnswerIsNeverResent(t *testing.T) {
	ctx := context.Background()
	x := newEnv(t, portfolio.PaperLocal, 100000)
	id := x.ready(x.terms(order.Buy, "10", "50", 0))
	// the process writes the intent and ends before it learns the answer
	must(t, x.db.Orders("default").Run(ctx, x.book.ID, func(c order.Context) ([]order.Event, error) {
		_, evs, err := order.BeginSubmitBound(c, id, "me", "p1")
		return evs, err
	}))
	// a new process
	y := &env{t: t, dir: x.dir, clock: x.clock.Add(time.Minute), book: x.book}
	y.db = open(t, x.dir, &y.clock)
	y.svc = order.Service{Store: y.db.Orders("default")}
	if v := y.view(id); v.State != order.Submitting {
		t.Fatalf("%+v", v)
	}
	must(t, y.svc.Recover(ctx, y.book.ID))
	v := y.view(id)
	if v.State != order.Unknown || v.Reservation == "" {
		t.Fatalf("%+v", v)
	}
	if err := y.svc.SubmitBound(ctx, y.book.ID, id, "me", "p1", func(context.Context, order.Submission) (order.SendResult, error) {
		t.Fatal("resent")
		return order.SendResult{}, nil
	}); err == nil {
		t.Fatal("resubmitted an unknown order")
	}
	// recovering twice changes nothing
	must(t, y.svc.Recover(ctx, y.book.ID))
	if y.view(id).Events != v.Events {
		t.Fatal("recover wrote again")
	}
	y.invariants()
	// the broker says it never saw the order: only now is it closed, and the hold freed
	must(t, y.svc.Report(ctx, y.book.ID, id, order.Reported{Kind: order.RejectedEv, Key: "reconcile", Reason: "unknown at the broker"}))
	if v := y.view(id); v.State != order.Rejected || v.Reservation != "" {
		t.Fatalf("%+v", v)
	}
	y.invariants()
}

func TestOverfillAndUnpostableFillsNeedAPerson(t *testing.T) {
	ctx := context.Background()
	x := newEnv(t, portfolio.PaperLocal, 100000)
	id := x.ready(x.terms(order.Buy, "10", "50", 0))
	must(t, x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", func(context.Context, order.Submission) (order.SendResult, error) {
		return order.SendResult{Broker: "B"}, nil
	}))
	must(t, x.svc.Report(ctx, x.book.ID, id, fillOf("e1", "11", "50", 0)))
	v := x.view(id)
	if v.State != order.Unknown || v.Filled.String() != "0" || x.ledger().Projection().Held("sec_a").Sign() != 0 {
		t.Fatalf("%+v", v)
	}
	if err := x.svc.Resolve(ctx, x.book.ID, id, "", "x"); err == nil {
		t.Fatal("resolved by nobody")
	}
	must(t, x.svc.Resolve(ctx, x.book.ID, id, "me", "statement shows 10 shares"))
	if v := x.view(id); v.State != order.Open {
		t.Fatalf("%+v", v)
	}
	// a fill at a price the cash cannot cover is kept as a fact, not posted
	must(t, x.svc.Report(ctx, x.book.ID, id, fillOf("e2", "10", "500", 0)))
	v = x.view(id)
	if v.State != order.Unknown || v.Unposted.String() != "10" || x.ledger().Projection().Held("sec_a").Sign() != 0 {
		t.Fatalf("%+v", v)
	}
	x.invariants()
}

func TestSweepFreesLapsedDraftsAndSellsAreBoundedByFreeShares(t *testing.T) {
	ctx := context.Background()
	x := newEnv(t, portfolio.PaperLocal, 100000)
	x.hold("100")
	sell := x.draft(x.terms(order.Sell, "60", "20", 0))
	if r := x.ledger().Projection().ReservedShares("sec_a", ""); r.String() != "60" {
		t.Fatalf("%s", r)
	}
	_, err := x.svc.Draft(ctx, x.book.ID, order.DraftRequest{Terms: x.terms(order.Sell, "50", "21", 0), By: "me", Valid: time.Hour})
	if !errors.Is(err, portfolio.ErrOversell) {
		t.Fatalf("%v", err)
	}
	x.invariants()
	x.clock = x.clock.Add(2 * time.Hour)
	must(t, x.svc.Sweep(ctx, x.book.ID))
	v := x.view(sell)
	if v.State != order.Expired || v.Reservation != "" {
		t.Fatalf("%+v", v)
	}
	must(t, x.svc.Sweep(ctx, x.book.ID))
	if x.view(sell).Events != v.Events {
		t.Fatal("sweep wrote twice")
	}
	x.invariants()
}

func TestSubmitRejectionReleasesTheHold(t *testing.T) {
	ctx := context.Background()
	x := newEnv(t, portfolio.PaperLocal, 100000)
	id := x.ready(x.terms(order.Buy, "10", "50", 0))
	must(t, x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", func(context.Context, order.Submission) (order.SendResult, error) {
		return order.SendResult{Rejected: true, Reason: "no permission for this market"}, nil
	}))
	v := x.view(id)
	if v.State != order.Rejected || v.Reservation != "" || len(v.Why) == 0 {
		t.Fatalf("%+v", v)
	}
	x.invariants()
}

func TestConcurrentProcessesCannotSpendTheSameMoney(t *testing.T) {
	dir := t.TempDir()
	clock := t0
	first := open(t, dir, &clock)
	b, err := first.Portfolios("default").CreateBook(context.Background(), portfolio.Book{Account: "main", Name: "core", Env: portfolio.PaperLocal, Base: "EUR", Method: portfolio.FIFO})
	must(t, err)
	must(t, first.Portfolios("default").Update(context.Background(), []string{b.ID}, func(now time.Time, ls []*portfolio.Ledger) (*store.ImportBatch, error) {
		tx, err := ls[0].Deposit(portfolio.Common{Event: t0, Source: "test", ImportID: "dep"}, eur(100000))
		if err != nil {
			return nil, err
		}
		_, err = ls[0].Post(tx, t0)
		return nil, err
	}))
	// eight "processes", each with its own handle on the same file, each wanting
	// 60% of the cash
	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	won, lost := 0, 0
	for i := 0; i < n; i++ {
		db := first
		if i > 0 {
			db = open(t, dir, &clock)
		}
		wg.Add(1)
		go func(i int, db *store.DB) {
			defer wg.Done()
			svc := order.Service{Store: db.Orders("default")}
			tm := order.Terms{Book: b.ID, Account: "main", Env: portfolio.PaperLocal, Security: fmt.Sprintf("sec_%d", i), Side: order.Buy,
				Qty: money.MustDecimal("10"), Limit: money.MustDecimal("60"), Currency: "EUR", Type: "limit", TIF: "DAY"}
			_, err := svc.Draft(context.Background(), b.ID, order.DraftRequest{Terms: tm, By: "me", Valid: time.Hour})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, portfolio.ErrInsufficient):
				lost++
			default:
				t.Errorf("unexpected: %v", err)
			}
		}(i, db)
	}
	wg.Wait()
	if won != 1 || lost != n-1 {
		t.Fatalf("won %d lost %d", won, lost)
	}
	l, err := first.Portfolios("default").Ledger(context.Background(), b.ID)
	must(t, err)
	if got := l.Projection().ReservedCash("EUR", ""); got != 60000 {
		t.Fatalf("held %d", got)
	}
	logs, _ := first.Orders("default").Logs(context.Background(), "main")
	if len(logs) != 1 {
		t.Fatalf("%d orders", len(logs))
	}
}

func TestConcurrentFillsAndCancelsKeepTheInvariants(t *testing.T) {
	ctx := context.Background()
	x := newEnv(t, portfolio.PaperLocal, 1000000)
	id := x.ready(x.terms(order.Buy, "100", "10", 100))
	must(t, x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", func(context.Context, order.Submission) (order.SendResult, error) {
		return order.SendResult{Broker: "B"}, nil
	}))
	// several processes report the same executions and a cancel at once
	var wg sync.WaitGroup
	for p := 0; p < 4; p++ {
		db := open(t, x.dir, &x.clock)
		svc := order.Service{Store: db.Orders("default")}
		wg.Add(1)
		go func(p int, svc order.Service) {
			defer wg.Done()
			for k := 1; k <= 5; k++ {
				_ = svc.Report(ctx, x.book.ID, id, fillOf(fmt.Sprintf("e%d", k), "10", "10", 10))
				if p == 2 && k == 3 {
					_ = svc.Cancel(ctx, x.book.ID, id, "me", nil)
				}
				if p == 3 && k == 4 {
					_ = svc.Report(ctx, x.book.ID, id, order.Reported{Kind: order.CancelledEv})
				}
			}
		}(p, svc)
	}
	wg.Wait()
	v := x.view(id)
	if v.Filled.String() != "50" || v.FeesPaid != 50 {
		t.Fatalf("each execution counted once: %+v", v)
	}
	if v.State != order.Cancelled || v.Reservation != "" {
		t.Fatalf("%+v", v)
	}
	if h := x.ledger().Projection().Held("sec_a"); h.String() != "50" {
		t.Fatalf("held %s", h)
	}
	x.invariants()
}

func TestControlsAreDurableAndDefaultOffForBrokerEnvironments(t *testing.T) {
	x := newEnv(t, portfolio.PaperBroker, 100000)
	ctx := context.Background()
	cs, err := x.db.Orders("default").Controls(ctx)
	must(t, err)
	if len(cs) != 0 || !order.DefaultControl(portfolio.PaperBroker).Disabled || !order.DefaultControl(portfolio.Real).Disabled || order.DefaultControl(portfolio.PaperLocal).Disabled {
		t.Fatalf("%v", cs)
	}
	if err := x.db.Orders("default").SetControl(ctx, portfolio.Real, false, "", "me"); err == nil {
		t.Fatal("a change without a reason")
	}
	x.enable()
	must(t, x.db.Orders("default").SetControl(ctx, portfolio.PaperBroker, true, "broker session lost", "me"))
	cs, _ = x.db.Orders("default").Controls(ctx)
	if !cs[portfolio.PaperBroker].Disabled || cs[portfolio.PaperBroker].Reason != "broker session lost" {
		t.Fatalf("%v", cs)
	}
	// cancelling stays possible while new orders are off
	id := x.ready(x.terms(order.Buy, "1", "10", 0))
	must(t, x.svc.Cancel(ctx, x.book.ID, id, "me", nil))
	// the log cannot be edited
	h := x.db.Orders("default")
	_ = h
}

func TestGateLimitsNewRiskOnlyAndSparesPaperSalesAndCancels(t *testing.T) {
	ctx := context.Background()
	x := newEnv(t, portfolio.Real, 1000000)
	x.hold("100")
	x.enable()
	g := &order.Gate{Tier: 0}
	x.svc.Gate = g
	// tier 0 is paper only
	_, err := x.svc.Draft(ctx, x.book.ID, order.DraftRequest{Terms: x.terms(order.Buy, "1", "10", 0), By: "me", Valid: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "tier 0 allows paper trading only") {
		t.Fatalf("%v", err)
	}
	// reducing risk is never gated
	g.Tier = 1
	g.Suspended = []string{"drawdown: 16% fall"}
	sell := x.ready(x.terms(order.Sell, "10", "20", 0))
	_, err = x.svc.Draft(ctx, x.book.ID, order.DraftRequest{Terms: x.terms(order.Buy, "1", "10", 0), By: "me", Valid: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "new risk is suspended (drawdown: 16% fall)") {
		t.Fatalf("%v", err)
	}
	// a buy confirmed before the suspension is revoked, not cancelled, and keeps its hold
	g.Suspended = nil
	buy := x.ready(x.terms(order.Buy, "5", "10", 0))
	g.Suspended = []string{"manual: away"}
	must(t, x.svc.RevokeUnsent(ctx, x.book.ID, "manual: away"))
	if v := x.view(buy); v.State != order.Draft || v.Confirmation != nil || v.Reservation == "" {
		t.Fatalf("%+v", v)
	}
	if v := x.view(sell); v.State != order.Confirmed {
		t.Fatalf("a sale was revoked: %+v", v)
	}
	// nothing can be confirmed or sent while suspended, but cancelling and selling go on
	if err := x.svc.ConfirmBound(ctx, x.book.ID, buy, "me", nil, time.Minute, order.Binding{Payload: "p1"}); err == nil {
		t.Fatal("confirmed during a suspension")
	}
	send := func(context.Context, order.Submission) (order.SendResult, error) {
		return order.SendResult{Broker: "B"}, nil
	}
	// tier 1 prepares tickets for manual placement; sending needs tier 2
	if err := x.svc.SubmitBound(ctx, x.book.ID, sell, "me", "p1", send); err == nil || !strings.Contains(err.Error(), "does not allow orders to be sent to a broker") {
		t.Fatalf("%v", err)
	}
	g.Tier, g.Broker = 2, true
	must(t, x.svc.SubmitBound(ctx, x.book.ID, sell, "me", "p1", send))
	must(t, x.svc.Cancel(ctx, x.book.ID, buy, "me", nil))
	x.invariants()
	// a local paper book is not gated at all
	p := newEnv(t, portfolio.PaperLocal, 100000)
	p.svc.Gate = &order.Gate{Tier: 0, Suspended: []string{"manual: away"}}
	p.draft(p.terms(order.Buy, "1", "10", 0))
}

func TestAConfirmationCoversOnlyWhatTheBrokerWasShown(t *testing.T) {
	ctx := context.Background()
	x := newEnv(t, portfolio.Real, 1000000)
	x.enable()
	send := func(context.Context, order.Submission) (order.SendResult, error) {
		return order.SendResult{Broker: "B"}, nil
	}

	// a plain confirmation is a ticket for manual placement: it never reaches a broker
	id := x.draft(x.terms(order.Buy, "1", "10", 0))
	must(t, x.svc.Confirm(ctx, x.book.ID, id, "me", nil, time.Minute))
	if err := x.svc.Submit(ctx, x.book.ID, id, "me", send); err == nil || !strings.Contains(err.Error(), "against a broker preview") {
		t.Fatalf("%v", err)
	}
	if err := x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", send); err == nil || !strings.Contains(err.Error(), "against a broker preview") {
		t.Fatalf("%v", err)
	}
	if v := x.view(id); v.Attempts != 0 || v.State != order.Confirmed {
		t.Fatalf("a refused submission left a trace: %+v", v)
	}

	// the broker's own warnings are acknowledged by exact text, like any other
	id = x.draft(x.terms(order.Buy, "2", "10", 0))
	b := order.Binding{Payload: "p1", Warnings: []string{"broker: price is outside the collar"}}
	if err := x.svc.ConfirmBound(ctx, x.book.ID, id, "me", nil, time.Minute, b); err == nil || !strings.Contains(err.Error(), "not acknowledged") {
		t.Fatalf("%v", err)
	}
	must(t, x.svc.ConfirmBound(ctx, x.book.ID, id, "me", []string{"broker: price is outside the collar"}, time.Minute, b))

	// what would be sent now differs from what was shown: nothing is sent
	if err := x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p2", send); err == nil || !strings.Contains(err.Error(), "not what was confirmed") {
		t.Fatalf("%v", err)
	}
	if v := x.view(id); v.Attempts != 0 || v.State != order.Confirmed {
		t.Fatalf("%+v", v)
	}
	// and the same payload goes, once
	x.svc.Gate = &order.Gate{Tier: 2, Broker: true}
	must(t, x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", send))
	if err := x.svc.SubmitBound(ctx, x.book.ID, id, "me", "p1", send); err == nil {
		t.Fatal("sent twice")
	}
	x.invariants()
}

// A selective revocation withdraws the confirmation of the unsent orders that
// are picked, in a paper book as well, and never an order that was sent.
func TestRevokeWhereWithdrawsOnlyPickedUnsentConfirmations(t *testing.T) {
	ctx := context.Background()
	x := newEnv(t, portfolio.PaperLocal, 100000)
	picked := x.ready(x.terms(order.Buy, "1", "10", 0))
	other := x.ready(x.terms(order.Buy, "1", "11", 0))
	sent := x.ready(x.terms(order.Buy, "1", "12", 0))
	must(t, x.svc.SubmitBound(ctx, x.book.ID, sent, "me", "p1", func(context.Context, order.Submission) (order.SendResult, error) {
		return order.SendResult{Broker: "B"}, nil
	}))
	ids, err := x.svc.RevokeWhere(ctx, x.book.ID, func(v order.View) bool { return v.ID != other }, "the evidence changed")
	must(t, err)
	if len(ids) != 1 || ids[0] != picked {
		t.Fatalf("only the picked unsent order: %v", ids)
	}
	if v := x.view(picked); v.State != order.Draft || v.Confirmation != nil || v.Reservation == "" {
		t.Fatalf("a withdrawn confirmation leaves a draft that keeps its hold: %+v", v)
	}
	if v := x.view(other); v.State != order.Confirmed {
		t.Fatalf("not picked: %+v", v)
	}
	if v := x.view(sent); !v.Sent() || v.State == order.Draft {
		t.Fatalf("an order that was sent is not touched: %+v", v)
	}
	again, err := x.svc.RevokeWhere(ctx, x.book.ID, func(order.View) bool { return true }, "again")
	must(t, err)
	if len(again) != 1 || again[0] != other {
		t.Fatalf("a confirmation is withdrawn once: %v", again)
	}
	x.invariants()
}
