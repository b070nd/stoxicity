// Package recon compares what a broker reports with what this program holds,
// and turns the comparison into two things: facts that can be applied safely
// (an acknowledgement, a fill, a cancellation the broker states about an order
// this program sent), and differences that need a person (a balance that does
// not match, a position nobody here bought, an order nobody here placed).
//
// It never overwrites local data with the broker's. A mismatch is recorded and
// left for a decision, because either side can be the one that is wrong: the
// ledger may be missing a deposit, or the broker may be showing something that
// has not settled.
//
// The package is pure: it reads snapshots and returns results.
package recon

import (
	"fmt"
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
)

// Kind of difference.
type Kind string

// The differences that can be found.
const (
	CashMismatch         Kind = "cash_mismatch"
	SettledMismatch      Kind = "settled_cash_mismatch"
	PositionMismatch     Kind = "position_mismatch"
	UnmappedPosition     Kind = "unmapped_position" // the broker holds a contract the instrument register does not know
	ForeignOrder         Kind = "foreign_order"     // the broker has an open order this program did not send
	MissingAtBroker      Kind = "missing_at_broker" // this program thinks an order is open, the broker does not list it
	OrderTermsDiffer     Kind = "order_terms_differ"
	FilledMismatch       Kind = "filled_mismatch"
	ForeignExecution     Kind = "foreign_execution"
	ExecutionTermsDiffer Kind = "execution_terms_differ"
	UnmappedExecution    Kind = "unmapped_execution"
)

// Difference is one thing that does not match, in words a person can act on.
type Difference struct {
	Kind    Kind   `json:"kind"`
	Subject string `json:"subject"`
	Local   string `json:"local"`
	Broker  string `json:"broker"`
	Hint    string `json:"hint"`
}

// Key identifies the difference so the same one is not recorded twice for the
// same pair of values.
func (d Difference) Key() string {
	return string(d.Kind) + "|" + d.Subject + "|" + d.Local + "|" + d.Broker
}

// Fact is something the broker states about an order sent from here and that
// can be applied through order.Report.
type Fact struct {
	Order    string         `json:"order"`
	Reported order.Reported `json:"reported"`
	Note     string         `json:"note"`
}

// Local is this program's side for one book.
type Local struct {
	// Cash is the total cash by currency (settled and unsettled); Settled is
	// the settled part.
	Cash    map[money.Currency]int64
	Settled map[money.Currency]int64
	// Held is the shares held, by security.
	Held map[string]money.Decimal
	// Orders are the orders of the book.
	Orders []order.View
	// SecurityOf maps a broker contract to a security of the instrument register.
	SecurityOf map[int64]string
}

// Snapshot is what the broker reported.
type Snapshot struct {
	Balances   []broker.Balance
	Positions  []broker.Position
	Open       []broker.OpenOrder
	Executions []broker.Execution
}

// Result is the outcome of a comparison.
type Result struct {
	Facts       []Fact       `json:"facts"`
	Differences []Difference `json:"differences"`
}

func sideOf(s order.Side) string { return string(s) }

// Compare compares the two sides. Facts are about orders this program sent and
// are safe to apply in any order and any number of times.
func Compare(l Local, s Snapshot) Result {
	var res Result
	diff := func(d Difference) { res.Differences = append(res.Differences, d) }

	// cash
	seen := map[money.Currency]bool{}
	for _, b := range s.Balances {
		seen[b.Currency] = true
		if lc := l.Cash[b.Currency]; lc != b.Cash {
			diff(Difference{CashMismatch, string(b.Currency), minor(lc, b.Currency), minor(b.Cash, b.Currency),
				"compare with the broker statement; a missing deposit, fee or dividend is the usual cause. Nothing was changed."})
		}
		if ls := l.Settled[b.Currency]; ls != b.Settled {
			diff(Difference{SettledMismatch, string(b.Currency), minor(ls, b.Currency), minor(b.Settled, b.Currency),
				"settled and total cash can differ by trades that have not settled; check the settlement dates. Nothing was changed."})
		}
	}
	for cur, lc := range l.Cash {
		if !seen[cur] && lc != 0 {
			diff(Difference{CashMismatch, string(cur), minor(lc, cur), "not reported", "the broker reported no balance in this currency. Nothing was changed."})
		}
	}

	// positions
	bh := map[string]money.Decimal{}
	for _, p := range s.Positions {
		sec, ok := l.SecurityOf[p.Conid]
		if !ok {
			diff(Difference{UnmappedPosition, fmt.Sprintf("conid %d %s", p.Conid, p.Symbol), "unknown", p.Qty.String() + " " + string(p.Currency),
				"add the contract to the instrument register (contracts: broker ibkr, conid) so it can be matched. Nothing was changed."})
			continue
		}
		if cur, err := bh[sec].Add(p.Qty); err == nil {
			bh[sec] = cur
		}
	}
	secs := map[string]bool{}
	for k := range bh {
		secs[k] = true
	}
	for k := range l.Held {
		secs[k] = true
	}
	for _, sec := range sortedKeys(secs) {
		if l.Held[sec].Cmp(bh[sec]) != 0 {
			diff(Difference{PositionMismatch, sec, l.Held[sec].String(), bh[sec].String(),
				"a trade missing from the ledger, or one not yet reported by the broker. Nothing was changed."})
		}
	}

	// orders and executions
	byID := map[string]order.View{}
	for _, v := range l.Orders {
		byID[v.ID] = v
	}
	atBroker := map[string]bool{}
	for _, o := range s.Open {
		v, ok := byID[o.Ref]
		if !ok {
			diff(Difference{ForeignOrder, "broker order " + o.ID, "none", describe(o),
				"an order this program did not send (placed in the broker's own app?). It is not touched and does not hold cash here."})
			continue
		}
		atBroker[v.ID] = true
		res.Facts = append(res.Facts, orderFacts(v, o, l.SecurityOf, &res)...)
	}
	for _, v := range l.Orders {
		if v.Sent() && !v.Terminal() && !atBroker[v.ID] && v.State != order.Submitting {
			diff(Difference{MissingAtBroker, v.ID, string(v.State), "not listed",
				"the broker does not list this order: it may have filled, expired or been cancelled while this program did not look. The executions below decide; otherwise order report or order resolve."})
		}
	}
	execs := append([]broker.Execution(nil), s.Executions...)
	sort.SliceStable(execs, func(i, j int) bool { return execs[i].At.Before(execs[j].At) })
	for _, x := range execs {
		v, ok := byID[x.Ref]
		if !ok {
			diff(Difference{ForeignExecution, "execution " + x.ID, "none", fmt.Sprintf("%s %s @ %s", x.Side, x.Qty, x.Price),
				"an execution of an order this program did not send. It is not applied."})
			continue
		}
		sec, mapped := l.SecurityOf[x.Conid]
		switch {
		case !mapped:
			diff(Difference{UnmappedExecution, "execution " + x.ID, v.Terms.Security, fmt.Sprintf("conid %d", x.Conid), "the contract is not in the instrument register. Not applied."})
			continue
		case sec != v.Terms.Security || x.Side != sideOf(v.Terms.Side) || x.Currency != v.Terms.Currency:
			diff(Difference{ExecutionTermsDiffer, "execution " + x.ID, fmt.Sprintf("%s %s %s", v.Terms.Side, v.Terms.Security, v.Terms.Currency),
				fmt.Sprintf("%s conid %d %s", x.Side, x.Conid, x.Currency), "the execution does not match the order it names. Not applied."})
			continue
		}
		gross, err := gross(x, v.Terms.Currency)
		if err != nil {
			diff(Difference{ExecutionTermsDiffer, "execution " + x.ID, v.ID, err.Error(), "the amount could not be computed. Not applied."})
			continue
		}
		res.Facts = append(res.Facts, Fact{Order: v.ID, Note: fmt.Sprintf("execution %s: %s at %s", x.ID, x.Qty, x.Price), Reported: order.Reported{
			Kind: order.FillEv,
			Fill: &order.Fill{ID: "ibkr:" + x.ID, Qty: x.Qty, Price: x.Price, Gross: gross, Fee: x.Commission, At: x.At},
		}})
	}
	// acknowledgements first, then fills, then endings: an ending releases only
	// what the fills left held
	rank := func(k order.Kind) int {
		switch k {
		case order.Acknowledged:
			return 0
		case order.FillEv:
			return 1
		}
		return 2
	}
	sort.SliceStable(res.Facts, func(i, j int) bool { return rank(res.Facts[i].Reported.Kind) < rank(res.Facts[j].Reported.Kind) })
	sort.SliceStable(res.Differences, func(i, j int) bool { return res.Differences[i].Key() < res.Differences[j].Key() })
	return res
}

func gross(x broker.Execution, cur money.Currency) (int64, error) {
	e, err := cur.Exponent()
	if err != nil {
		return 0, err
	}
	d, err := x.Price.Mul(x.Qty, uint8(e), money.HalfEven)
	if err != nil {
		return 0, err
	}
	a, err := money.NewAmount(d, cur, money.HalfEven)
	if err != nil {
		return 0, err
	}
	return a.Minor(), nil
}

// orderFacts says what a listed order of ours tells us.
func orderFacts(v order.View, o broker.OpenOrder, secOf map[int64]string, res *Result) []Fact {
	var out []Fact
	t := v.Terms
	if sec := secOf[o.Conid]; sec != t.Security || o.Side != sideOf(t.Side) || o.Qty.Cmp(t.Qty) != 0 || o.Limit.Cmp(t.Limit) != 0 {
		res.Differences = append(res.Differences, Difference{OrderTermsDiffer, v.ID,
			fmt.Sprintf("%s %s %s @ %s", t.Side, t.Qty, t.Security, t.Limit), describe(o),
			"the broker holds different terms than the ones confirmed here. Cancel it at the broker and re-check before anything else."})
		return nil
	}
	if o.Filled.Cmp(v.Filled) != 0 && v.Unposted.IsZero() {
		res.Differences = append(res.Differences, Difference{FilledMismatch, v.ID, v.Filled.String(), o.Filled.String(),
			"the executions decide: they are applied one by one by their IDs. A quantity that never gets an execution stays a difference."})
	}
	switch strings.ToLower(o.Status) {
	case "cancelled", "apicancelled":
		out = append(out, Fact{v.ID, order.Reported{Kind: order.CancelledEv, Key: "ibkr:cancelled:" + o.ID, Broker: o.ID}, "the broker reports the order cancelled"})
	case "inactive", "rejected":
		out = append(out, Fact{v.ID, order.Reported{Kind: order.RejectedEv, Key: "ibkr:rejected:" + o.ID, Broker: o.ID, Reason: "the broker reports the order " + o.Status}, "the broker reports the order " + o.Status})
	default:
		out = append(out, Fact{v.ID, order.Reported{Kind: order.Acknowledged, Key: "ibkr:ack:" + o.ID, Broker: o.ID}, "the broker lists the order as " + o.Status})
	}
	return out
}

func describe(o broker.OpenOrder) string {
	return fmt.Sprintf("%s %s of conid %d @ %s (%s, %s filled)", o.Side, o.Qty, o.Conid, o.Limit, o.Status, o.Filled)
}

func minor(v int64, cur money.Currency) string {
	a, err := money.FromMinor(v, cur)
	if err != nil {
		return fmt.Sprint(v)
	}
	return a.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
