package workflow

import (
	"testing"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

func view(id string, env portfolio.Env, st order.State) order.View {
	return order.View{ID: id, State: st, Terms: order.Terms{Env: env, Side: order.Buy, Security: "sec_a", Qty: money.MustDecimal("3")}}
}

// A correction interrupts quiet hours only for a sent order with real money
// that is still open; a paper order or a finished one does not.
func TestOnlyAnOpenSentOrderWithRealMoneyIsLiveExposure(t *testing.T) {
	byID := map[string]order.View{
		"ord_real_open":   view("ord_real_open", portfolio.Real, order.Open),
		"ord_real_done":   view("ord_real_done", portfolio.Real, order.Filled),
		"ord_paper_open":  view("ord_paper_open", portfolio.PaperLocal, order.Open),
		"ord_real_unsure": view("ord_real_unsure", portfolio.Real, order.Unknown),
	}
	for _, c := range []struct {
		ids  []string
		live bool
	}{
		{[]string{"ord_real_open"}, true},
		{[]string{"ord_real_unsure"}, true},
		{[]string{"ord_real_done"}, false},
		{[]string{"ord_paper_open"}, false},
		{[]string{"ord_paper_open", "ord_real_done"}, false},
		{nil, false},
		{[]string{"ord_gone"}, false},
	} {
		lines, live := sentOrders(byID, c.ids)
		if live != c.live {
			t.Errorf("%v: live = %v (%v)", c.ids, live, lines)
		}
	}
	if lines, _ := sentOrders(byID, []string{"ord_real_open"}); len(lines) != 1 || lines[0] != "ord_real_open (buy 3 sec_a, open)" {
		t.Errorf("the sent order is described: %v", lines)
	}
}
