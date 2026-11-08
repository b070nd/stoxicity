package paper

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
)

func view(side order.Side, qty, limit string) order.View {
	t := order.Terms{Book: "bk_1", Account: "p", Env: "paper_local", Security: "sec_a", Side: side, Qty: money.MustDecimal(qty), Limit: money.MustDecimal(limit),
		Currency: "EUR", Type: "limit", TIF: "DAY"}
	return order.View{ID: "ord_x_0001", Terms: t, State: order.Open, AckedAt: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC), Remaining: t.Qty}
}

func bar(day, o, h, l, c string, vol int64) Bar {
	return Bar{Security: "sec_a", Day: day, Open: money.MustDecimal(o), High: money.MustDecimal(h), Low: money.MustDecimal(l), Close: money.MustDecimal(c), Volume: vol}
}

func noFee(order.View, int64, money.Decimal) int64 { return 0 }

func run(t *testing.T, cfg Config, v order.View, bars ...Bar) Result {
	t.Helper()
	r, err := Simulate(cfg, []order.View{v}, bars, "2026-12-31", noFee)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func kinds(r Result) string {
	var k []string
	for _, o := range r.Outcomes {
		k = append(k, string(o.Report.Kind))
	}
	return strings.Join(k, ",")
}

func TestNoFillInTheBarOfSubmissionOrOnATouch(t *testing.T) {
	cfg := DefaultConfig()
	v := view(order.Buy, "10", "50")
	// the bar of the submission day is never used; the next one decides
	r := run(t, cfg, v, bar("2026-11-03", "49", "51", "40", "50", 1000000))
	if len(r.Outcomes) != 0 || len(r.Pending) != 1 {
		t.Fatalf("%+v", r)
	}
	// the low only touches the limit: not a fill, and the DAY order expires
	r = run(t, cfg, v, bar("2026-11-04", "51", "52", "50", "51", 1000000))
	if kinds(r) != "expired" || !strings.Contains(r.Outcomes[0].Note, "touched") {
		t.Fatalf("%+v", r)
	}
	// never reached
	r = run(t, cfg, v, bar("2026-11-04", "55", "56", "51", "52", 1000000))
	if kinds(r) != "expired" || !strings.Contains(r.Outcomes[0].Note, "never reached") {
		t.Fatalf("%+v", r)
	}
	// a sell needs the high strictly above
	s := view(order.Sell, "10", "50")
	if r = run(t, cfg, s, bar("2026-11-04", "48", "50", "47", "49", 1000000)); kinds(r) != "expired" {
		t.Fatalf("%+v", r)
	}
}

func TestFillIsAtTheLimitBoundedByVolumeAndReproducible(t *testing.T) {
	cfg := DefaultConfig()
	v := view(order.Buy, "10", "50")
	// a gap below the limit: the price is the limit, never the better open
	b := bar("2026-11-04", "45", "49", "44", "48", 1000000)
	r := run(t, cfg, v, b)
	if kinds(r) != "fill" {
		t.Fatalf("%+v", r)
	}
	f := r.Outcomes[0].Report.Fill
	if f.Price.String() != "50" || f.Qty.String() != "10" || f.Gross != 50000 || f.ID != "paper:ord_x_0001:2026-11-04" || f.SettleDay != "2026-11-06" {
		t.Fatalf("%+v", f)
	}
	// the same inputs again: the same outcome, byte for byte
	r2 := run(t, cfg, v, b)
	if *r2.Outcomes[0].Report.Fill != *f {
		t.Fatal("not reproducible")
	}
	// a thin day: participation 1% of 600 shares is 6, at least a quarter of that
	thin := bar("2026-11-04", "45", "49", "44", "48", 600)
	r = run(t, cfg, v, thin)
	q := r.Outcomes[0].Report.Fill.Qty
	if kinds(r) != "fill,expired" || q.Cmp(money.MustDecimal("6")) > 0 || q.Sign() < 0 {
		t.Fatalf("%+v", r)
	}
	// the seed changes the draw, never the bounds
	seen := map[string]bool{}
	for s := int64(1); s <= 40; s++ {
		c := cfg
		c.Seed = s
		rs := run(t, c, view(order.Buy, "10", "50"), bar("2026-11-04", "45", "49", "44", "48", 1000))
		for _, o := range rs.Outcomes {
			if o.Report.Kind == order.FillEv {
				seen[o.Report.Fill.Qty.String()] = true
				if o.Report.Fill.Qty.Cmp(money.MustDecimal("10")) > 0 {
					t.Fatalf("over the order: %v", o.Report.Fill.Qty)
				}
			}
		}
	}
	if len(seen) < 2 {
		t.Fatalf("the seed has no effect: %v", seen)
	}
	// no volume, no fill
	if r = run(t, cfg, v, bar("2026-11-04", "45", "49", "44", "48", 0)); kinds(r) != "expired" || !strings.Contains(r.Outcomes[0].Note, "no volume") {
		t.Fatalf("%+v", r)
	}
	// whole shares only unless fractional: 1% of 100 shares is 1
	if r = run(t, cfg, v, bar("2026-11-04", "45", "49", "44", "48", 100)); r.Outcomes[0].Report.Kind == order.FillEv && r.Outcomes[0].Report.Fill.Qty.Scale() != 0 {
		t.Fatalf("part share: %+v", r.Outcomes[0].Report.Fill)
	}
}

func TestCancelAndMissingDataAreNotInvented(t *testing.T) {
	cfg := DefaultConfig()
	v := view(order.Buy, "10", "50")
	v.State = order.CancelRequested
	v.CancelAt = time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC)
	r := run(t, cfg, v, bar("2026-11-04", "45", "49", "44", "48", 1000000))
	if kinds(r) != "cancelled" {
		t.Fatalf("%+v", r)
	}
	// asked for during the session: the session runs, the rest is cancelled
	v.CancelAt = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	r = run(t, cfg, v, bar("2026-11-04", "45", "49", "44", "48", 600))
	if kinds(r) != "fill,cancelled" {
		t.Fatalf("%+v", r)
	}
	// no bar yet, or one a week later: nothing is decided
	v = view(order.Buy, "10", "50")
	if r = run(t, cfg, v); len(r.Pending) != 1 || len(r.Outcomes) != 0 {
		t.Fatalf("%+v", r)
	}
	if r = run(t, cfg, v, bar("2026-11-12", "45", "49", "44", "48", 1000000)); len(r.Pending) != 1 || !strings.Contains(r.Pending[0].Why, "more than five days") {
		t.Fatalf("%+v", r)
	}
	// a bar after the allowed day is not used
	if r, _ = Simulate(cfg, []order.View{v}, []Bar{bar("2026-11-04", "45", "49", "44", "48", 1000000)}, "2026-11-03", noFee); len(r.Outcomes) != 0 {
		t.Fatalf("%+v", r)
	}
	// closed orders are left alone
	v.State = order.Filled
	if r = run(t, cfg, v, bar("2026-11-04", "45", "49", "44", "48", 1000000)); len(r.Outcomes) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestBadBarsAreRefused(t *testing.T) {
	cfg := DefaultConfig()
	v := view(order.Buy, "10", "50")
	for name, b := range map[string]Bar{
		"high below low": bar("2026-11-04", "45", "44", "46", "45", 10),
		"open outside":   bar("2026-11-04", "60", "49", "44", "48", 10),
		"close outside":  bar("2026-11-04", "45", "49", "44", "30", 10),
		"zero price":     bar("2026-11-04", "0", "49", "0", "48", 10),
		"bad day":        bar("04/11/2026", "45", "49", "44", "48", 10),
		"negative vol":   bar("2026-11-04", "45", "49", "44", "48", -1),
	} {
		if _, err := Simulate(cfg, []order.View{v}, []Bar{b}, "2026-12-31", noFee); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	ok := bar("2026-11-04", "45", "49", "44", "48", 10)
	if _, err := Simulate(cfg, []order.View{v}, []Bar{ok, ok}, "2026-12-31", noFee); err == nil {
		t.Error("two bars for a day accepted")
	}
	bad := cfg
	bad.Participation = money.MustDecimal("0")
	if _, err := Simulate(bad, nil, nil, "2026-12-31", noFee); err == nil {
		t.Error("zero participation accepted")
	}
}

func TestAddWeekdays(t *testing.T) {
	for _, c := range []struct {
		in, out string
		n       int
	}{{"2026-11-04", "2026-11-06", 2}, {"2026-11-05", "2026-11-09", 2}, {"2026-11-06", "2026-11-10", 2}, {"2026-11-06", "2026-11-06", 0}} {
		if got, _ := AddWeekdays(c.in, c.n); got != c.out {
			t.Errorf("%s+%d = %s, want %s", c.in, c.n, got, c.out)
		}
	}
}
