package fixture

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/money"
)

func base() State {
	return State{
		Session:    broker.Session{Authenticated: true, Connected: true},
		Accounts:   []broker.Account{{ID: "DU1234567", Paper: true}},
		Balances:   map[string][]broker.Balance{"DU1234567": {{Currency: "USD", Cash: 1000, Settled: 1000}}},
		Positions:  map[string][]broker.Position{},
		Orders:     map[string][]broker.OpenOrder{},
		Executions: map[string][]broker.Execution{},
		Contracts:  []broker.Contract{{Conid: 1, Symbol: "AAA", Currency: "USD"}},
	}
}

func req() broker.OrderRequest {
	return broker.OrderRequest{Account: "DU1234567", Conid: 1, Side: "buy", Qty: money.MustDecimal("1"), Limit: money.MustDecimal("2"), TIF: "DAY", Ref: "ord_1"}
}

func TestPlaceIsPersistedAcrossOpens(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.json")
	b, err := Create(p, base())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := b.Place(ctx, req())
	if err != nil || res.OrderID == "" {
		t.Fatalf("%+v %v", res, err)
	}
	b2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	o, err := b2.OpenOrders(ctx, "DU1234567")
	if err != nil || len(o) != 1 || o[0].Ref != "ord_1" {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestScriptedQuestionAndRejection(t *testing.T) {
	s := base()
	s.Script.PlaceReplies = []string{"outside the collar"}
	b, _ := Create(filepath.Join(t.TempDir(), "b.json"), s)
	ctx := context.Background()
	res, err := b.Place(ctx, req())
	if err != nil || len(res.Replies) != 1 || res.OrderID != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if o, _ := b.OpenOrders(ctx, "DU1234567"); len(o) != 0 {
		t.Fatal("an unanswered question places nothing")
	}
	if res, err = b.Confirm(ctx, res.Replies[0].ID); err != nil || res.OrderID == "" {
		t.Fatalf("%+v %v", res, err)
	}
	s = base()
	s.Script.PlaceReject = "no"
	b, _ = Create(filepath.Join(t.TempDir(), "b.json"), s)
	if _, err := b.Place(ctx, req()); broker.KindOf(err) != broker.Refused {
		t.Fatalf("%v", err)
	}
}

func TestAnswerLostAfterThePlacement(t *testing.T) {
	s := base()
	s.Failures = map[string]Failure{"Place": {Kind: broker.Unavailable, Message: "timeout", After: true, Once: true}}
	b, _ := Create(filepath.Join(t.TempDir(), "b.json"), s)
	ctx := context.Background()
	if _, err := b.Place(ctx, req()); broker.KindOf(err) != broker.Unavailable {
		t.Fatalf("%v", err)
	}
	o, _ := b.OpenOrders(ctx, "DU1234567")
	if len(o) != 1 {
		t.Fatalf("the order is at the broker although the caller was told it failed: %+v", o)
	}
}

func TestSessionStatesAndOtherAccounts(t *testing.T) {
	s := base()
	s.Session.Authenticated = false
	b, _ := Create(filepath.Join(t.TempDir(), "b.json"), s)
	ctx := context.Background()
	if _, err := b.Accounts(ctx); broker.KindOf(err) != broker.SessionExpired {
		t.Fatalf("%v", err)
	}
	s = base()
	s.Session.Competing = true
	b, _ = Create(filepath.Join(t.TempDir(), "b.json"), s)
	if _, err := b.Balances(ctx, "DU1234567"); broker.KindOf(err) != broker.Competing {
		t.Fatalf("%v", err)
	}
	b, _ = Create(filepath.Join(t.TempDir(), "b.json"), base())
	if _, err := b.Balances(ctx, "U999"); broker.KindOf(err) != broker.Entitlement {
		t.Fatalf("%v", err)
	}
	if _, err := b.Quote(ctx, 1); broker.KindOf(err) != broker.Entitlement {
		t.Fatalf("no quote scripted means no market data: %v", err)
	}
}

func TestOnceFailureHappensOnce(t *testing.T) {
	s := base()
	s.Failures = map[string]Failure{"Positions": {Kind: broker.Pacing, Message: "slow", RetryAfterMS: 2000, Once: true}}
	b, _ := Create(filepath.Join(t.TempDir(), "b.json"), s)
	ctx := context.Background()
	_, err := b.Positions(ctx, "DU1234567")
	if be, ok := err.(*broker.Error); !ok || be.Kind != broker.Pacing || be.RetryAfter.Milliseconds() != 2000 {
		t.Fatalf("%v", err)
	}
	if _, err := b.Positions(ctx, "DU1234567"); err != nil {
		t.Fatal(err)
	}
}
