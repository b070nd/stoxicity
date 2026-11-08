package main

import (
	"regexp"
	"strings"
	"testing"
)

var orderIDRE = regexp.MustCompile(`ord_[0-9a-f]{12}_\d{4}`)

func TestOrderLifecycleOnALocalPaperBook(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "order").wantCode(t, exitUsage)
	e.run(t, "order", "frobnicate").wantCode(t, exitUsage)
	e.run(t, "portfolio", "book", "create", "-account", "paper", "-name", "sim", "-env", "paper_local", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "paper/sim", "deposit", "-date", "2026-10-01", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	e.run(t, "order", "list").wantOut(t, "no orders")

	r := e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "10", "-limit", "50", "-fee", "2.00 EUR", "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "held now", "502.00 EUR", "lapses", "draft", "Nothing was sent")
	id := orderIDRE.FindString(r.out)
	if id == "" {
		t.Fatal(r.out)
	}
	e.run(t, "portfolio", "show", "paper/sim").wantOut(t, "502.00")

	// the same money cannot be spent twice, and a refused draft leaves nothing behind
	e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "10", "-limit", "60", "-by", "me").wantCode(t, exitError)
	e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "1", "-limit", "5", "-by", "me", "-valid", "soon").wantCode(t, exitUsage)
	e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "1.5", "-limit", "5", "-by", "me").wantCode(t, exitError)

	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitError)
	e.run(t, "order", "confirm", id).wantCode(t, exitUsage)
	e.run(t, "order", "confirm", id, "-by", "me").wantOut(t, "confirmed by me", "exactly these terms")
	r = e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "local paper engine", "No broker was contacted")
	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitError)

	e.run(t, "order", "report", id, "-kind", "fill", "-fill-id", "e1", "-qty", "4", "-price", "49", "-fee", "1.00 EUR").wantOut(t, "partially_filled", "4 of 10 filled", "6 left")
	e.run(t, "order", "report", id, "-kind", "fill", "-fill-id", "e1", "-qty", "4", "-price", "49", "-fee", "1.00 EUR").wantOut(t, "4 of 10 filled")
	e.run(t, "order", "report", id, "-kind", "fill").wantCode(t, exitUsage)
	r = e.run(t, "order", "show", id)
	r.wantOut(t, "partially_filled", "fill e1: 4 at 49", "in the ledger", "hold "+id+"/r2", "acknowledged")
	e.run(t, "portfolio", "show", "paper/sim").wantOut(t, "sec_apple")
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)

	// a cancel request is not a cancellation
	r = e.run(t, "order", "cancel", id, "-by", "me")
	r.wantOut(t, "cancel requested", "not cancelled until")
	e.run(t, "order", "show", id).wantOut(t, "cancel_requested")
	e.run(t, "order", "report", id, "-kind", "fill", "-fill-id", "e2", "-qty", "1", "-price", "49").wantOut(t, "cancel_requested", "out of order: fill e2 arrived after the cancel request")
	e.run(t, "order", "report", id, "-kind", "cancelled").wantOut(t, "cancelled", "5 of 10 filled")
	e.run(t, "order", "list", "-open").wantOut(t, "no orders")
	r = e.run(t, "order", "list", "-json")
	r.wantOut(t, `"state": "cancelled"`, `"filled": "5"`)
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)

	// a draft that was never sent is closed at once and frees its hold
	r = e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "2", "-limit", "10", "-by", "me")
	id2 := orderIDRE.FindString(r.out)
	e.run(t, "order", "cancel", id2, "-by", "me").wantOut(t, "cancelled; what it held was released")
	e.run(t, "order", "resolve", id2, "-by", "me", "-note", "x").wantCode(t, exitError)
	e.run(t, "order", "show", "ord_000000000000_0001").wantCode(t, exitError)
}

func TestOrderOnARealBookNeedsAPolicyAndNeverReachesABroker(t *testing.T) {
	e := allocateEnv(t)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-08-30", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	draft := func(extra ...string) result {
		return e.run(t, append([]string{"order", "draft", "main/core", "-security", "sec_apple", "-side", "buy", "-qty", "5", "-limit", "40", "-by", "me"}, extra...)...)
	}
	r := draft()
	r.wantCode(t, exitError)
	r.wantErr(t, "no policy is active")
	tierUp(t, e, 1, nil)
	r = draft()
	r.wantCode(t, exitOK)
	r.wantOut(t, "main / real", "200.00 EUR", "warning: no executing quote has been validated")
	id := orderIDRE.FindString(r.out)

	// every warning must be acknowledged by its exact text
	c := e.run(t, "order", "confirm", id, "-by", "me")
	c.wantCode(t, exitError)
	c.wantErr(t, "not acknowledged")
	e.run(t, "order", "confirm", id, "-by", "me", "-ack", "no executing quote has been validated: the price collar and quote freshness are not checked here").wantCode(t, exitOK)

	// new orders are off by default, and no broker is connected in any case
	e.run(t, "order", "control", "show").wantOut(t, "DISABLED", "enabled")
	r = e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "no broker is connected", "confirmed")
	e.run(t, "order", "control", "enable", "-env", "real", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "order", "control", "enable", "-env", "real", "-by", "me", "-reason", "reconciled with the broker").wantCode(t, exitOK)
	e.run(t, "order", "control", "show").wantOut(t, "reconciled with the broker")
	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitError)
	e.run(t, "order", "show", id).wantOut(t, "confirmed", "confirmation")

	// restart reconciliation switches broker environments off again
	r = e.run(t, "order", "recover", "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "switched off", "no order is waiting")
	out := e.run(t, "order", "control", "show").out
	if strings.Count(out, "DISABLED") < 2 {
		t.Fatal(out)
	}
}

func TestOrderFromAnAcceptedPlan(t *testing.T) {
	e, cands, fees, book := planFixture(t)
	tierUp(t, e, 1, capEdit)
	e.run(t, "portfolio", "add", book, "deposit", "-date", "2026-11-02", "-amount", "300.00 USD").wantCode(t, exitOK)
	gen := e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-as-of", "2026-11-03", "-rates", "USD=0.92")
	gen.wantCode(t, exitOK)
	var apple string
	for _, line := range strings.Split(gen.out, "\n") {
		if strings.Contains(line, "sec_apple") {
			apple = planIDRE.FindString(line)
		}
	}
	if apple == "" {
		t.Fatal(gen.out)
	}
	draft := func(extra ...string) result {
		return e.run(t, append([]string{"order", "draft", book, "-plan", apple, "-by", "me", "-fee", "1.25 USD"}, extra...)...)
	}
	r := draft()
	r.wantCode(t, exitError)
	r.wantErr(t, "draft", "only an accepted plan")
	e.run(t, "plan", "propose", apple, "-by", "me").wantCode(t, exitOK)
	e.run(t, "plan", "accept", apple, "-by", "me").wantCode(t, exitOK)
	// the plan bounds the order
	draft("-limit", "30").wantErr(t, "above the plan's entry ceiling")
	draft("-qty", "410").wantErr(t, "more than the 402 the plan sized")
	// the plan was sized with a conversion the order does not make: the hold is in the order's currency
	draft("-qty", "300").wantErr(t, "not enough cash")
	r = draft("-qty", "10")
	r.wantCode(t, exitOK)
	r.wantOut(t, "sec_apple", "buy", "10", "at most 22.5985 USD", apple+".r1", "epoch 2", "227.24 USD")
	id := orderIDRE.FindString(r.out)
	e.run(t, "order", "show", id).wantOut(t, apple+".r1")

	// drafting held cash, so the book is no longer the one the plan was built on:
	// another order needs a fresh plan
	r = draft("-qty", "1")
	r.wantCode(t, exitError)
	r.wantErr(t, "is stale", "book")
}
