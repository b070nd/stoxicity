package order

import (
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

func baseTerms() Terms {
	return Terms{Book: "bk_1", Account: "main", Env: portfolio.PaperLocal, Security: "sec_a", Side: Buy, Qty: money.MustDecimal("10"),
		Limit: money.MustDecimal("50.10"), Currency: "EUR", Type: "limit", TIF: "DAY", FeeEstimate: 150}
}

func TestTermsHashBindsEveryTermAndIgnoresTrailingZeros(t *testing.T) {
	a := baseTerms()
	b := baseTerms()
	b.Limit = money.MustDecimal("50.1000")
	b.Qty = money.MustDecimal("10.0")
	if a.Hash() != b.Hash() {
		t.Fatal("equal terms hash differently")
	}
	for name, mut := range map[string]func(*Terms){
		"account": func(t *Terms) { t.Account = "x" }, "env": func(t *Terms) { t.Env = portfolio.Real }, "security": func(t *Terms) { t.Security = "b" },
		"side": func(t *Terms) { t.Side = Sell }, "qty": func(t *Terms) { t.Qty = money.MustDecimal("11") }, "limit": func(t *Terms) { t.Limit = money.MustDecimal("50.11") },
		"fee": func(t *Terms) { t.FeeEstimate = 151 }, "plan": func(t *Terms) { t.PlanRevision = 2 }, "policy": func(t *Terms) { t.PolicyHash = "h" },
		"currency": func(t *Terms) { t.Currency = "USD" }, "book": func(t *Terms) { t.Book = "bk_2" },
	} {
		c := baseTerms()
		mut(&c)
		if c.Hash() == a.Hash() {
			t.Errorf("%s does not change the hash", name)
		}
	}
	if ConfirmHash(a.Hash(), "r1") == ConfirmHash(a.Hash(), "r2") {
		t.Fatal("the confirmation ignores the hold")
	}
}

func TestCommitmentRoundsUpAndDropsPaidFees(t *testing.T) {
	tm := baseTerms()
	tm.Limit = money.MustDecimal("10.005")
	c, err := tm.Commitment(money.MustDecimal("3"), 0)
	if err != nil || c.Minor() != 3002+150 { // 30.015 rounds up to 30.02
		t.Fatalf("%v %v", c, err)
	}
	if c, _ = tm.Commitment(money.MustDecimal("3"), 100); c.Minor() != 3002+50 {
		t.Fatalf("%v", c)
	}
	if c, _ = tm.Commitment(money.MustDecimal("3"), 400); c.Minor() != 3002 {
		t.Fatalf("overpaid fees must not reduce the hold: %v", c)
	}
}

func ev(k Kind, key string) Event { return Event{Order: "o", Kind: k, Key: key, At: time.Unix(1, 0)} }

func drafted() []Event {
	tm := baseTerms()
	e := ev(Drafted, "")
	e.Terms = &tm
	return []Event{e}
}

func fillEv(id, qty string, posted bool) Event {
	e := ev(FillEv, id)
	e.Fill = &Fill{ID: id, Qty: money.MustDecimal(qty), Posted: posted}
	return e
}

func TestFoldStatesAndIdempotence(t *testing.T) {
	cases := []struct {
		name string
		add  []Event
		want State
	}{
		{"draft", nil, Draft},
		{"confirmed", []Event{ev(ConfirmedEv, "")}, Confirmed},
		{"submitting", []Event{ev(ConfirmedEv, ""), ev(SubmitStarted, "")}, Submitting},
		{"open", []Event{ev(SubmitStarted, ""), ev(Acknowledged, "ack")}, Open},
		{"partial", []Event{ev(SubmitStarted, ""), ev(Acknowledged, "ack"), fillEv("1", "4", true)}, PartiallyFilled},
		{"filled", []Event{ev(SubmitStarted, ""), fillEv("1", "4", true), fillEv("2", "6", true)}, Filled},
		{"fill before ack", []Event{ev(SubmitStarted, ""), fillEv("1", "4", true), ev(Acknowledged, "ack")}, PartiallyFilled},
		{"duplicate fill", []Event{ev(SubmitStarted, ""), fillEv("1", "4", true), fillEv("1", "4", true)}, PartiallyFilled},
		{"cancel requested", []Event{ev(SubmitStarted, ""), ev(CancelRequestEv, "")}, CancelRequested},
		{"fill after request", []Event{ev(SubmitStarted, ""), ev(CancelRequestEv, ""), fillEv("1", "4", true)}, CancelRequested},
		{"cancelled", []Event{ev(SubmitStarted, ""), ev(CancelRequestEv, ""), ev(CancelledEv, "c")}, Cancelled},
		{"cancel after full fill", []Event{ev(SubmitStarted, ""), fillEv("1", "10", true), ev(CancelRequestEv, ""), ev(CancelledEv, "c")}, Filled},
		{"late ack after cancel", []Event{ev(SubmitStarted, ""), ev(CancelledEv, "c"), ev(Acknowledged, "ack")}, Cancelled},
		{"rejected", []Event{ev(SubmitStarted, ""), ev(RejectedEv, "r")}, Rejected},
		{"rejected but filled", []Event{ev(SubmitStarted, ""), fillEv("1", "4", true), ev(RejectedEv, "r")}, Unknown},
		{"expired", []Event{ev(SubmitStarted, ""), ev(ExpiredEv, "x")}, Expired},
		{"local cancel", []Event{ev(LocalCancelled, "")}, Cancelled},
		{"unknown after timeout", []Event{ev(SubmitStarted, ""), ev(UnknownEv, "")}, Unknown},
		{"unknown cleared by evidence", []Event{ev(SubmitStarted, ""), ev(UnknownEv, ""), ev(Acknowledged, "ack")}, Open},
		{"fill never sent", []Event{fillEv("1", "1", true)}, Unknown},
		{"overfill", []Event{ev(SubmitStarted, ""), fillEv("1", "11", true)}, Unknown},
		{"unposted", []Event{ev(SubmitStarted, ""), fillEv("1", "4", false)}, Unknown},
		{"resolved", []Event{ev(SubmitStarted, ""), fillEv("1", "4", false), ev(Resolved, "")}, PartiallyFilled},
	}
	for _, c := range cases {
		v, err := Fold(append(drafted(), c.add...))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if v.State != c.want {
			t.Errorf("%s: %s, want %s (%v)", c.name, v.State, c.want, v.Why)
		}
	}
	// the same set of facts in two orders gives the same total
	a, _ := Fold(append(drafted(), ev(SubmitStarted, ""), fillEv("1", "4", true), fillEv("2", "3", true), ev(Acknowledged, "ack")))
	b, _ := Fold(append(drafted(), ev(SubmitStarted, ""), ev(Acknowledged, "ack"), fillEv("2", "3", true), fillEv("1", "4", true)))
	if a.State != b.State || a.Filled.String() != b.Filled.String() || a.Remaining.String() != "3" {
		t.Fatalf("%+v %+v", a, b)
	}
	if _, err := Fold(nil); err == nil {
		t.Fatal("empty log")
	}
	if _, err := Fold([]Event{ev(SubmitStarted, "")}); err == nil {
		t.Fatal("log without terms")
	}
}
