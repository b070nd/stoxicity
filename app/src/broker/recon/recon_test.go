package recon

import (
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
)

func d(s string) money.Decimal { return money.MustDecimal(s) }

func view(id string, st order.State, filled string) order.View {
	return order.View{ID: id, State: st, Attempts: 1, Filled: d(filled), Terms: order.Terms{Security: "sec_a", Side: order.Buy, Qty: d("10"), Limit: d("20"), Currency: "USD"}}
}

func local() Local {
	return Local{
		Cash:       map[money.Currency]int64{"USD": 100000},
		Held:       map[string]money.Decimal{"sec_a": d("5")},
		SecurityOf: map[int64]string{111: "sec_a"},
	}
}

func snap() Snapshot {
	return Snapshot{
		Balances:  []broker.Balance{{Currency: "USD", Cash: 100000, Settled: 100000}},
		Positions: []broker.Position{{Conid: 111, Symbol: "A", Qty: d("5"), Currency: "USD"}},
	}
}

func kinds(r Result) map[Kind]int {
	m := map[Kind]int{}
	for _, x := range r.Differences {
		m[x.Kind]++
	}
	return m
}

func TestMatchingSidesHaveNoDifferences(t *testing.T) {
	if r := Compare(local(), snap()); len(r.Differences) != 0 || len(r.Facts) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestCashAndPositionMismatchesAreRecordedNotFixed(t *testing.T) {
	s := snap()
	s.Balances[0].Cash = 99000
	s.Positions[0].Qty = d("6")
	s.Positions = append(s.Positions, broker.Position{Conid: 999, Symbol: "Z", Qty: d("1"), Currency: "USD"})
	r := Compare(local(), s)
	k := kinds(r)
	if k[CashMismatch] != 1 || k[PositionMismatch] != 1 || k[UnmappedPosition] != 1 || len(r.Facts) != 0 {
		t.Fatalf("%v %+v", k, r)
	}
	for _, x := range r.Differences {
		if x.Hint == "" || x.Local == "" || x.Broker == "" {
			t.Fatalf("a difference must be actionable: %+v", x)
		}
	}
}

func TestOrderFactsAndForeignOrders(t *testing.T) {
	l := local()
	l.Orders = []order.View{view("ord_1", order.Submitting, "0")}
	s := snap()
	s.Open = []broker.OpenOrder{
		{ID: "1001", Ref: "ord_1", Conid: 111, Side: "buy", Qty: d("10"), Filled: d("0"), Limit: d("20"), Status: "Submitted"},
		{ID: "1002", Ref: "someone-else", Conid: 111, Side: "buy", Qty: d("1"), Filled: d("0"), Limit: d("5"), Status: "Submitted"},
	}
	r := Compare(l, s)
	if len(r.Facts) != 1 || r.Facts[0].Reported.Kind != order.Acknowledged || r.Facts[0].Reported.Broker != "1001" {
		t.Fatalf("%+v", r.Facts)
	}
	if kinds(r)[ForeignOrder] != 1 {
		t.Fatalf("%+v", r.Differences)
	}
}

func TestTermsThatDifferAreNotAcknowledged(t *testing.T) {
	l := local()
	l.Orders = []order.View{view("ord_1", order.Submitting, "0")}
	s := snap()
	s.Open = []broker.OpenOrder{{ID: "1001", Ref: "ord_1", Conid: 111, Side: "buy", Qty: d("10"), Filled: d("0"), Limit: d("21"), Status: "Submitted"}}
	r := Compare(l, s)
	if len(r.Facts) != 0 || kinds(r)[OrderTermsDiffer] != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestExecutionsBecomeIdempotentFillsInTimeOrder(t *testing.T) {
	l := local()
	l.Orders = []order.View{view("ord_1", order.Open, "0")}
	t0 := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	s := snap()
	s.Open = []broker.OpenOrder{{ID: "1001", Ref: "ord_1", Conid: 111, Side: "buy", Qty: d("10"), Filled: d("10"), Limit: d("20"), Status: "Cancelled"}}
	s.Executions = []broker.Execution{
		{ID: "e2", Ref: "ord_1", Conid: 111, Side: "buy", Qty: d("4"), Price: d("19.5"), Commission: 100, Currency: "USD", At: t0.Add(time.Minute)},
		{ID: "e1", Ref: "ord_1", Conid: 111, Side: "buy", Qty: d("6"), Price: d("19.5"), Commission: 100, Currency: "USD", At: t0},
	}
	r := Compare(l, s)
	if len(r.Facts) != 3 {
		t.Fatalf("%+v", r.Facts)
	}
	if r.Facts[0].Reported.Fill.ID != "ibkr:e1" || r.Facts[1].Reported.Fill.ID != "ibkr:e2" || r.Facts[2].Reported.Kind != order.CancelledEv {
		t.Fatalf("fills before the ending, in time order: %+v", r.Facts)
	}
	if g := r.Facts[0].Reported.Fill.Gross; g != 11700 {
		t.Fatalf("gross %d", g)
	}
	again := Compare(l, s)
	if len(again.Facts) != len(r.Facts) || again.Facts[0].Reported.Fill.ID != r.Facts[0].Reported.Fill.ID {
		t.Fatal("same input, same facts")
	}
}

func TestForeignAndMismatchedExecutionsAreNotApplied(t *testing.T) {
	l := local()
	l.Orders = []order.View{view("ord_1", order.Open, "0")}
	s := snap()
	s.Executions = []broker.Execution{
		{ID: "x1", Ref: "unknown", Conid: 111, Side: "buy", Qty: d("1"), Price: d("1"), Currency: "USD"},
		{ID: "x2", Ref: "ord_1", Conid: 111, Side: "sell", Qty: d("1"), Price: d("1"), Currency: "USD"},
		{ID: "x3", Ref: "ord_1", Conid: 555, Side: "buy", Qty: d("1"), Price: d("1"), Currency: "USD"},
	}
	r := Compare(l, s)
	k := kinds(r)
	if len(r.Facts) != 0 || k[ForeignExecution] != 1 || k[ExecutionTermsDiffer] != 1 || k[UnmappedExecution] != 1 {
		t.Fatalf("%v %+v", k, r)
	}
}

func TestSentOrderThatTheBrokerDoesNotListIsFlagged(t *testing.T) {
	l := local()
	l.Orders = []order.View{view("ord_1", order.Open, "0")}
	if r := Compare(l, snap()); kinds(r)[MissingAtBroker] != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestACorrectedExecutionIsADifferenceNotAnOverwrite(t *testing.T) {
	l := local()
	ov := view("ord_1", order.Open, "4")
	ov.BrokerRef, ov.AckedAt = "1001", time.Now()
	l.Orders = []order.View{ov}
	booked := order.Fill{ID: "ibkr:e1", Qty: d("4"), Price: d("19.5"), Fee: 100}
	l.Fills = map[string]order.Fill{"ibkr:e1": booked}
	s := snap()
	s.Open = []broker.OpenOrder{{ID: "1001", Ref: "ord_1", Conid: 111, Side: "buy", Qty: d("10"), Filled: d("4"), Limit: d("20"), Status: "Submitted"}}
	x := broker.Execution{ID: "e1", Ref: "ord_1", Conid: 111, Side: "buy", Qty: d("4"), Price: d("19.5"), Commission: 100, Currency: "USD"}
	s.Executions = []broker.Execution{x}
	if r := Compare(l, s); len(r.Facts) != 0 || len(r.Differences) != 0 {
		t.Fatalf("an execution booked as reported is nothing new: %+v", r)
	}
	x.Commission = 135
	s.Executions = []broker.Execution{x}
	r := Compare(l, s)
	if len(r.Facts) != 0 || kinds(r)[ExecutionCorrected] != 1 {
		t.Fatalf("%+v", r)
	}
}
