package main

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/broker/fixture"
	"github.com/b070nd/stoxicity/app/src/money"
)

// tierTwo goes through the door to tier 2 with the whole evidence, then
// activates a policy that carries it.
func tierTwo(t *testing.T, e *testEnv) {
	t.Helper()
	for _, it := range []string{"config_complete", "records_reconciled", "no_broker_discrepancy", "failure_scenarios", "evaluation_adequate"} {
		e.run(t, "tier", "evidence", "set", "-target", "2", "-item", it, "-ref", "test report", "-by", "me").wantCode(t, exitOK)
	}
	e.run(t, "tier", "config", "review").wantCode(t, exitOK)
	e.run(t, "tier", "unlock", "2", "-by", "me", "-accept-risk").wantCode(t, exitOK)
	activatePolicy(t, e, func(s string) string {
		s = strings.Replace(s, "tier: 0", "tier: 2", 1)
		return strings.Replace(capEdit(s), "max_trades_per_month: 2", "max_trades_per_month: 20", 1)
	})
}

var fixtureNow = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func goodQuote() broker.Quote {
	return broker.Quote{Conid: 265598, Last: money.MustDecimal("19.95"), Bid: money.MustDecimal("19.90"), Ask: money.MustDecimal("20.00"), At: fixtureNow.Add(-10 * time.Second)}
}

// sendEnv is a paper_broker book with a connected fixture broker, tier 2 and
// everything reviewed, ready to draft.
func sendEnv(t *testing.T) (*testEnv, func(func(*fixture.State)), string) {
	t.Helper()
	e := allocateEnv(t)
	raw := e.run(t, "instruments", "example").out
	with := strings.Replace(raw, "    from: \"1980-12-12\"\n    primary: \"yes\"\n", "    from: \"1980-12-12\"\n    primary: \"yes\"\n    contracts:\n      - broker: ibkr\n        conid: \"265598\"\n", 1)
	e.run(t, "instruments", "import", "-file", writeFile(t, e, "inst2.yaml", with), "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "pb", "-env", "paper_broker", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/pb", "deposit", "-date", "2026-06-01", "-amount", "1000.00 USD").wantCode(t, exitOK)
	tierUp(t, e, 1, nil)
	tierTwo(t, e)
	// each call rewrites the broker from a clean state plus the one edit
	reset := func(edit func(*fixture.State)) {
		writeBrokerFixture(t, e, func(s *fixture.State) {
			s.Quotes = []broker.Quote{goodQuote()}
			if edit != nil {
				edit(s)
			}
		})
	}
	fx := writeBrokerFixture(t, e, func(s *fixture.State) { s.Quotes = []broker.Quote{goodQuote()} })
	e.run(t, "broker", "config", "set", "-transport", "fixture", "-fixture", fx, "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "bind", "main/pb", "-account", "DU1234567", "-by", "me").wantCode(t, exitOK)
	return e, reset, fx
}

func draftBuy(t *testing.T, e *testEnv, qty, limit string) string {
	t.Helper()
	r := e.run(t, "order", "draft", "main/pb", "-security", "sec_apple", "-side", "buy", "-qty", qty, "-limit", limit, "-fee", "1.00 USD", "-by", "me")
	r.wantCode(t, exitOK)
	id := orderIDRE.FindString(r.out)
	if id == "" {
		t.Fatal(r.out)
	}
	return id
}

func TestPreviewConfirmSubmitReachTheBrokerOnlyThroughEveryGate(t *testing.T) {
	e, setFixture, _ := sendEnv(t)
	id := draftBuy(t, e, "5", "20")

	// the broker's preview and the checks, before anything is confirmed
	r := e.run(t, "broker", "preview", id)
	r.wantCode(t, exitOK)
	r.wantOut(t, "contract 265598 AAPL", "quote age", "collar", "All checks passed", "Nothing was sent")
	e.run(t, "order", "show", id).wantOut(t, "draft")

	// nothing goes before a confirmation made against a preview
	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitError)

	r = e.run(t, "order", "confirm", id, "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "for the broker", "payload", "Nothing was sent")

	// read-only by default
	r = e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "mode", "read_only", "broker mode trading")
	e.run(t, "broker", "mode", "trading", "-by", "me").wantCode(t, exitOK)
	// the limits are starting values until someone has read them
	r = e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "unreviewed")
	e.run(t, "broker", "limits", "show").wantOut(t, "UNREVIEWED", "illustrative")
	e.run(t, "broker", "limits", "review", "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "limits", "show").wantOut(t, "reviewed by me")
	// new orders are off by default
	r = e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "disabled")
	e.run(t, "order", "control", "enable", "-env", "paper_broker", "-by", "me", "-reason", "reconciled").wantCode(t, exitOK)
	if n := len(openOrders(t, e)); n != 0 {
		t.Fatalf("nothing may be at the broker before the submit: %d", n)
	}

	// a quote that went stale invalidates it, and nothing is recorded as sent
	setFixture(func(s *fixture.State) {
		q := goodQuote()
		q.At = fixtureNow.Add(-10 * time.Minute)
		s.Quotes = []broker.Quote{q}
	})
	r = e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "quote age")
	e.run(t, "order", "show", id).wantOut(t, "confirmed")

	// a delayed quote and a missing subscription are not execution grade
	setFixture(func(s *fixture.State) {
		q := goodQuote()
		q.Delayed = true
		s.Quotes = []broker.Quote{q}
	})
	e.run(t, "order", "submit", id, "-by", "me").wantErr(t, "delayed")
	setFixture(func(s *fixture.State) { s.Quotes = nil })
	e.run(t, "order", "submit", id, "-by", "me").wantErr(t, "no execution-grade quote")
	// the broker no longer has the money
	setFixture(func(s *fixture.State) {
		s.Balances["DU1234567"] = []broker.Balance{{Currency: "USD", Cash: 5000, Settled: 5000}}
	})
	e.run(t, "order", "submit", id, "-by", "me").wantErr(t, "funds")
	// a preview warning that was not acknowledged changes the payload
	setFixture(func(s *fixture.State) { s.Script.PreviewWarnings = []string{"Order exceeds 3% of average volume."} })
	e.run(t, "order", "submit", id, "-by", "me").wantErr(t, "not what was confirmed")
	if n := len(openOrders(t, e)); n != 0 {
		t.Fatalf("a refused submission reached the broker: %d", n)
	}
	setFixture(nil)
	e.run(t, "order", "show", id).wantOut(t, "confirmed")

	// the policy that governed the draft was replaced
	// (checked in its own test below); now everything is right
	r = e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "acknowledged", "broker order 1001", "****567")
	o := openOrders(t, e)
	if len(o) != 1 || o[0].Ref != id || o[0].Side != "buy" || o[0].Qty.String() != "5" || o[0].Limit.String() != "20" || o[0].TIF != "DAY" {
		t.Fatalf("the broker must hold exactly the confirmed terms: %+v", o)
	}
	e.run(t, "order", "show", id).wantOut(t, "open")
	// and it cannot be sent twice
	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitError)
	if n := len(openOrders(t, e)); n != 1 {
		t.Fatalf("sent twice: %d", n)
	}
}

func openOrders(t *testing.T, e *testEnv) []broker.OpenOrder {
	t.Helper()
	b, err := fixture.Open(e.dir + "/broker.json")
	if err != nil {
		t.Fatal(err)
	}
	o, err := b.OpenOrders(t.Context(), "DU1234567")
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func readyToSend(t *testing.T, e *testEnv, id string, ack ...string) {
	t.Helper()
	args := []string{"order", "confirm", id, "-by", "me"}
	for _, a := range ack {
		args = append(args, "-ack", a)
	}
	e.run(t, args...).wantCode(t, exitOK)
}

func TestAnswerLostAfterPlacementIsUnknownNeverResentAndFoundBySync(t *testing.T) {
	e, setFixture, _ := sendEnv(t)
	e.run(t, "broker", "mode", "trading", "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "limits", "review", "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "control", "enable", "-env", "paper_broker", "-by", "me", "-reason", "reconciled").wantCode(t, exitOK)
	id := draftBuy(t, e, "5", "20")
	readyToSend(t, e, id)

	// the order is placed and the answer is lost
	setFixture(func(s *fixture.State) {
		s.Failures = map[string]fixture.Failure{"Place": {Kind: broker.Unavailable, Message: "timeout", After: true, Once: true}}
	})
	r := e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "UNKNOWN", "will not be sent again", "broker sync")
	e.run(t, "order", "show", id).wantOut(t, "unknown")
	if n := len(openOrders(t, e)); n != 1 {
		t.Fatalf("%d", n)
	}
	// no blind retry, by this command or after a restart
	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitError)
	e.run(t, "order", "recover", "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitError)
	if n := len(openOrders(t, e)); n != 1 {
		t.Fatalf("resent: %d", n)
	}
	// asking the broker finds it by its client reference
	r = e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitOK)
	r.wantOut(t, "acknowledged", "both sides agree")
	e.run(t, "order", "show", id).wantOut(t, "open", "1001")
	e.run(t, "broker", "sync", "main/pb").wantOut(t, "both sides agree")
}

func TestTheBrokersQuestionIsAnsweredOnlyWhenItWasAcknowledgedWordForWord(t *testing.T) {
	e, setFixture, _ := sendEnv(t)
	e.run(t, "broker", "mode", "trading", "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "limits", "review", "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "control", "enable", "-env", "paper_broker", "-by", "me", "-reason", "reconciled").wantCode(t, exitOK)
	const q = "The order price is outside the collar. Continue?"

	// a question the preview did not announce: nothing is answered
	id := draftBuy(t, e, "5", "20")
	readyToSend(t, e, id)
	setFixture(func(s *fixture.State) { s.Script.PlaceReplies = []string{q} })
	// (the scripted question changes nothing in the preview, so the payload still matches)
	r := e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "UNKNOWN", "not acknowledged")
	if n := len(openOrders(t, e)); n != 0 {
		t.Fatalf("an unanswered question placed an order: %d", n)
	}
	e.run(t, "order", "show", id).wantOut(t, "unknown")

	// a question the preview announced, acknowledged by its exact text
	// cancelling it records the request, but the broker cannot be asked about an order it never listed
	r = e.run(t, "order", "cancel", id, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "recorded", "broker sync finds it")
	id2 := draftBuy(t, e, "4", "20")
	setFixture(func(s *fixture.State) { s.Script.PreviewWarnings = []string{q} })
	r = e.run(t, "order", "confirm", id2, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "not acknowledged")
	readyToSend(t, e, id2, "broker: "+q)
	r = e.run(t, "order", "submit", id2, "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "acknowledged")
	if o := openOrders(t, e); len(o) != 1 || o[0].Ref != id2 {
		t.Fatalf("%+v", o)
	}
}

func TestBrokerRefusalReleasesTheHold(t *testing.T) {
	e, setFixture, _ := sendEnv(t)
	e.run(t, "broker", "mode", "trading", "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "limits", "review", "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "control", "enable", "-env", "paper_broker", "-by", "me", "-reason", "reconciled").wantCode(t, exitOK)
	id := draftBuy(t, e, "5", "20")
	readyToSend(t, e, id)
	setFixture(func(s *fixture.State) { s.Script.PlaceReject = "insufficient permissions for this security" })
	r := e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "refused by the broker", "insufficient permissions", "released")
	e.run(t, "order", "show", id).wantOut(t, "rejected")
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)
}

func orderOf(t *testing.T, e *testEnv) string {
	t.Helper()
	o := openOrders(t, e)
	if len(o) == 0 {
		t.Fatal("no order at the broker")
	}
	return o[0].Ref
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestFillsDuringCancellationAndCorrectionsReconcileWithoutOverwriting(t *testing.T) {
	e, setFixture, _ := sendEnv(t)
	e.run(t, "broker", "mode", "trading", "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "limits", "review", "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "control", "enable", "-env", "paper_broker", "-by", "me", "-reason", "reconciled").wantCode(t, exitOK)
	id := draftBuy(t, e, "10", "20")
	readyToSend(t, e, id)
	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitOK)

	px := money.MustDecimal("19.95")
	exec := func(eid, qty string, fee int64, sec int) broker.Execution {
		return broker.Execution{ID: eid, OrderID: "1001", Ref: id, Conid: 265598, Side: "buy", Qty: money.MustDecimal(qty), Price: px, Commission: fee, Currency: "USD", At: fixtureNow.Add(time.Duration(sec) * time.Second)}
	}
	// the broker's whole account at one moment: the order, its executions (each
	// told twice, as duplicate messages are), the holding and the cash
	atBroker := func(status, filled string, held string, cash int64, xs ...broker.Execution) {
		setFixture(func(s *fixture.State) {
			s.Orders["DU1234567"] = []broker.OpenOrder{{ID: "1001", Ref: id, Conid: 265598, Side: "buy", Qty: money.MustDecimal("10"), Filled: money.MustDecimal(filled), Limit: money.MustDecimal("20"), TIF: "DAY", Status: status}}
			s.Executions["DU1234567"] = append(append([]broker.Execution(nil), xs...), xs...)
			s.Positions["DU1234567"] = []broker.Position{{Conid: 265598, Symbol: "AAPL", Qty: money.MustDecimal(held), Currency: "USD"}}
			s.Balances["DU1234567"] = []broker.Balance{{Currency: "USD", Cash: cash, Settled: cash}}
		})
	}

	// 4 shares at 19.95 and a 1.00 commission
	atBroker("Submitted", "4", "4", 100000-7980-100, exec("e1", "4", 100, 0))
	r := e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitOK)
	r.wantOut(t, "fill", "both sides agree")
	e.run(t, "order", "show", id).wantOut(t, "partially_filled", "fill ibkr:e1: 4 at 19.95", "in the ledger")
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)
	// the same messages again change nothing
	r = e.run(t, "broker", "sync", "main/pb")
	r.wantOut(t, "both sides agree")
	if strings.Contains(r.out, "applied") {
		t.Fatalf("a second sync applied something:\n%s", r.out)
	}

	// cancelling asks the broker; it is not cancelled until the broker says so
	r = e.run(t, "order", "cancel", id, "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "cancel requested", "not cancelled until")
	e.run(t, "order", "show", id).wantOut(t, "cancel_requested")

	// a fill while the cancel is pending is still a fill
	atBroker("PendingCancel", "6", "6", 100000-7980-100-3990-100, exec("e1", "4", 100, 0), exec("e2", "2", 100, 30))
	e.run(t, "broker", "sync", "main/pb").wantOut(t, "both sides agree")
	e.run(t, "order", "show", id).wantOut(t, "cancel_requested", "arrived after the cancel request")

	// a correction of an execution that is already booked is a difference, never an overwrite
	atBroker("PendingCancel", "6", "6", 100000-7980-100-3990-135, exec("e1", "4", 100, 0), exec("e2", "2", 135, 30))
	r = e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitOK)
	r.wantOut(t, "execution_corrected", "fee 1.00 USD", "fee 1.35 USD", "NEW RISK IS SUSPENDED", "not changed")
	e.run(t, "order", "show", id).wantOut(t, "fill ibkr:e2: 2 at 19.95")
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)

	// the broker confirms the cancellation: only what was still held is released
	atBroker("Cancelled", "6", "6", 100000-7980-100-3990-100, exec("e1", "4", 100, 0), exec("e2", "2", 100, 30))
	e.run(t, "broker", "sync", "main/pb").wantOut(t, "cancelled", "both sides agree")
	e.run(t, "order", "show", id).wantOut(t, "cancelled", "filled 6 of 10")
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)
	e.run(t, "portfolio", "show", "main/pb").wantOut(t, "sec_apple")
	e.run(t, "broker", "differences").wantOut(t, "no open differences")
	// the new-risk suspension from the correction stays until a person lifts it
	e.run(t, "tier", "signal", "show").wantOut(t, "reconciliation_failed", "ACTIVE")
}

func TestPolicyReplacedAfterConfirmationInvalidatesTheOrder(t *testing.T) {
	e, _, _ := sendEnv(t)
	e.run(t, "broker", "mode", "trading", "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "limits", "review", "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "control", "enable", "-env", "paper_broker", "-by", "me", "-reason", "reconciled").wantCode(t, exitOK)
	id := draftBuy(t, e, "5", "20")
	readyToSend(t, e, id)
	activatePolicy(t, e, func(s string) string {
		s = strings.Replace(s, "tier: 0", "tier: 2", 1)
		s = strings.Replace(capEdit(s), "max_trades_per_month: 2", "max_trades_per_month: 20", 1)
		return strings.Replace(s, "drawdown_review: \"0.15\"", "drawdown_review: \"0.18\"", 1)
	})
	r := e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "policy", "no longer the active one")
	if n := len(openOrders(t, e)); n != 0 {
		t.Fatalf("%d", n)
	}
}

func TestTierOneConfirmsTicketsAndTierTwoIsNeededToSend(t *testing.T) {
	e := allocateEnv(t)
	raw := e.run(t, "instruments", "example").out
	with := strings.Replace(raw, "    from: \"1980-12-12\"\n    primary: \"yes\"\n", "    from: \"1980-12-12\"\n    primary: \"yes\"\n    contracts:\n      - broker: ibkr\n        conid: \"265598\"\n", 1)
	e.run(t, "instruments", "import", "-file", writeFile(t, e, "inst2.yaml", with), "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "pb", "-env", "paper_broker", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/pb", "deposit", "-date", "2026-06-01", "-amount", "1000.00 USD").wantCode(t, exitOK)
	tierUp(t, e, 1, func(s string) string {
		return strings.Replace(capEdit(s), "max_trades_per_month: 2", "max_trades_per_month: 20", 1)
	})
	fx := writeBrokerFixture(t, e, func(s *fixture.State) { s.Quotes = []broker.Quote{goodQuote()} })
	e.run(t, "broker", "config", "set", "-transport", "fixture", "-fixture", fx, "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "bind", "main/pb", "-account", "DU1234567", "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "mode", "trading", "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "limits", "review", "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "control", "enable", "-env", "paper_broker", "-by", "me", "-reason", "reconciled").wantCode(t, exitOK)
	id := draftBuy(t, e, "5", "20")
	e.run(t, "order", "confirm", id, "-by", "me").wantCode(t, exitOK)
	r := e.run(t, "order", "submit", id, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "tier 1", "tier 2 and up")
	if n := len(openOrders(t, e)); n != 0 {
		t.Fatalf("%d", n)
	}
	// a ticket stays a ticket
	id2 := draftBuy(t, e, "4", "20")
	e.run(t, "order", "confirm", id2, "-by", "me", "-ticket", "-ack", quoteWarning).wantCode(t, exitOK)
	e.run(t, "order", "submit", id2, "-by", "me").wantCode(t, exitError)
}
