package ibkr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/money"
)

// gateway is a stand-in for the Client Portal Gateway: answers by method and path.
type gateway struct {
	mu     sync.Mutex
	routes map[string]func(w http.ResponseWriter, r *http.Request)
	calls  []string
	bodies map[string]string
}

func newGateway(t *testing.T) (*gateway, *Broker) {
	t.Helper()
	g := &gateway{routes: map[string]func(http.ResponseWriter, *http.Request){}, bodies: map[string]string{}}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/v1/api")
		g.calls = append(g.calls, key)
		raw, _ := io.ReadAll(r.Body)
		g.bodies[key] = string(raw)
		h := g.routes[key]
		g.mu.Unlock()
		if h == nil {
			http.Error(w, `{"error":"no such route"}`, http.StatusNotFound)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	b, err := New(Options{BaseURL: srv.URL, RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, MinInterval: -1, Sleep: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	return g, b
}

func (g *gateway) json(route, body string) {
	g.routes[route] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func (g *gateway) status(route string, code int, header map[string]string, body string) {
	g.routes[route] = func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
}

func loggedIn(g *gateway) {
	g.json("GET /iserver/auth/status", `{"authenticated":true,"connected":true,"competing":false,"message":""}`)
	g.json("GET /portfolio/accounts", `[{"id":"DU1234567","accountId":"DU1234567","accountTitle":"Paper","type":"INDIVIDUAL"},{"id":"U7654321","accountTitle":"Live","type":"INDIVIDUAL"}]`)
}

func TestBaseURLMustBeHTTPSLoopback(t *testing.T) {
	for _, ok := range []string{"https://localhost:5000", "https://127.0.0.1:5000", "https://[::1]:5000", "https://gw.localhost:5000"} {
		if _, err := ValidateBaseURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://localhost:5000", "https://example.com:5000", "https://192.168.1.4:5000", "https://user:pw@localhost:5000", "localhost:5000", "https://localhost:5000?x=1", ""} {
		if _, err := ValidateBaseURL(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestSessionStates(t *testing.T) {
	g, b := newGateway(t)
	ctx := context.Background()
	g.json("GET /iserver/auth/status", `{"authenticated":true,"connected":true,"competing":true,"message":"another session"}`)
	s, err := b.Session(ctx)
	if err != nil || !s.Authenticated || !s.Competing || s.Message != "another session" {
		t.Fatalf("%+v %v", s, err)
	}
	// competing blocks everything that needs the session
	if _, err := b.Accounts(ctx); broker.KindOf(err) != broker.Competing {
		t.Fatalf("%v", err)
	}
	// an expired session is an answer to Session and an error everywhere else
	g.status("GET /iserver/auth/status", 401, nil, `{"error":"not authenticated"}`)
	if s, err := b.Session(ctx); err != nil || s.Authenticated {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := b.Accounts(ctx); broker.KindOf(err) != broker.SessionExpired {
		t.Fatalf("%v", err)
	}
	g.json("GET /iserver/auth/status", `{"authenticated":false,"connected":true,"competing":false}`)
	if _, err := b.Positions(ctx, "DU1234567"); broker.KindOf(err) != broker.SessionExpired {
		t.Fatalf("%v", err)
	}
}

func TestReadsBalancesPositionsOrdersAndExecutions(t *testing.T) {
	g, b := newGateway(t)
	loggedIn(g)
	ctx := context.Background()
	g.json("GET /portfolio/DU1234567/ledger", `{"USD":{"currency":"USD","cashbalance":1234.56,"settledcash":1000},"EUR":{"currency":"EUR","cashbalance":"10.5","settledcash":10.5},"BASE":{"cashbalance":1300}}`)
	g.json("GET /portfolio/DU1234567/positions/0", `[{"conid":265598,"ticker":"AAPL","position":12.5,"currency":"USD"},{"conid":1,"ticker":"ZERO","position":0,"currency":"USD"}]`)
	g.json("GET /iserver/account/orders", `{"orders":[{"acct":"DU1234567","orderId":1001,"order_ref":"ord_x","conid":265598,"side":"BUY","totalSize":10,"filledQuantity":4,"price":"190.10","timeInForce":"DAY","status":"Submitted"},{"acct":"U7654321","orderId":9,"order_ref":"other","conid":1,"side":"SELL","totalSize":1,"filledQuantity":0,"price":1,"status":"Submitted"}]}`)
	g.json("GET /iserver/account/trades", `[{"execution_id":"0001.01","order_id":"1001","order_ref":"ord_x","account":"DU1234567","conid":265598,"side":"B","size":4,"price":"189.95","commission":"1.02","currency":"USD","trade_time_r":1793718000000}]`)

	bal, err := b.Balances(ctx, "DU1234567")
	if err != nil || len(bal) != 2 {
		t.Fatalf("%+v %v", bal, err)
	}
	got := map[string][2]int64{}
	for _, x := range bal {
		got[string(x.Currency)] = [2]int64{x.Cash, x.Settled}
	}
	if got["USD"] != [2]int64{123456, 100000} || got["EUR"] != [2]int64{1050, 1050} {
		t.Fatalf("%v", got)
	}
	pos, err := b.Positions(ctx, "DU1234567")
	if err != nil || len(pos) != 1 || pos[0].Conid != 265598 || pos[0].Qty.String() != "12.5" {
		t.Fatalf("%+v %v", pos, err)
	}
	ords, err := b.OpenOrders(ctx, "DU1234567")
	if err != nil || len(ords) != 1 || ords[0].Ref != "ord_x" || ords[0].Side != "buy" || ords[0].Filled.String() != "4" || ords[0].Limit.String() != "190.10" {
		t.Fatalf("another account's order must not appear: %+v %v", ords, err)
	}
	ex, err := b.Executions(ctx, "DU1234567")
	if err != nil || len(ex) != 1 || ex[0].ID != "0001.01" || ex[0].Commission != 102 || ex[0].Side != "buy" || ex[0].Price.String() != "189.95" || ex[0].At.IsZero() {
		t.Fatalf("%+v %v", ex, err)
	}
}

func TestPositionsArePaged(t *testing.T) {
	g, b := newGateway(t)
	loggedIn(g)
	var page0 []string
	for i := 1; i <= 100; i++ {
		page0 = append(page0, `{"conid":`+strings.Repeat("1", 1)+`,"position":1,"currency":"USD"}`)
	}
	g.json("GET /portfolio/DU1234567/positions/0", "["+strings.Join(page0, ",")+"]")
	g.json("GET /portfolio/DU1234567/positions/1", `[{"conid":2,"position":3,"currency":"USD"}]`)
	pos, err := b.Positions(context.Background(), "DU1234567")
	if err != nil || len(pos) != 101 {
		t.Fatalf("%d %v", len(pos), err)
	}
}

func TestFailuresAreClassified(t *testing.T) {
	g, b := newGateway(t)
	loggedIn(g)
	ctx := context.Background()
	g.status("GET /portfolio/DU1234567/ledger", 429, map[string]string{"Retry-After": "7"}, `{"error":"too many"}`)
	_, err := b.Balances(ctx, "DU1234567")
	var be *broker.Error
	if broker.KindOf(err) != broker.Pacing || !asErr(err, &be) || be.RetryAfter != 7*time.Second {
		t.Fatalf("%v", err)
	}
	g.status("GET /portfolio/DU1234567/ledger", 503, nil, `{"error":"maintenance window"}`)
	if _, err := b.Balances(ctx, "DU1234567"); broker.KindOf(err) != broker.Maintenance || !strings.Contains(err.Error(), "maintenance") {
		t.Fatalf("%v", err)
	}
	g.status("GET /portfolio/DU1234567/ledger", 403, nil, `{"error":"no permission"}`)
	if _, err := b.Balances(ctx, "DU1234567"); broker.KindOf(err) != broker.Entitlement {
		t.Fatalf("%v", err)
	}
	g.status("GET /portfolio/DU1234567/ledger", 502, nil, ``)
	if _, err := b.Balances(ctx, "DU1234567"); broker.KindOf(err) != broker.Unavailable {
		t.Fatalf("%v", err)
	}
	g.json("GET /portfolio/DU1234567/ledger", `{"USD":`)
	if _, err := b.Balances(ctx, "DU1234567"); broker.KindOf(err) != broker.Malformed {
		t.Fatalf("%v", err)
	}
	g.json("GET /portfolio/DU1234567/ledger", `{"USD":{"currency":"USD","cashbalance":"abc"}}`)
	if _, err := b.Balances(ctx, "DU1234567"); broker.KindOf(err) != broker.Malformed {
		t.Fatalf("%v", err)
	}
}

func asErr(err error, target **broker.Error) bool {
	e, ok := err.(*broker.Error)
	if ok {
		*target = e
	}
	return ok
}

func TestErrorsNeverCarryTheAccountNumber(t *testing.T) {
	g, b := newGateway(t)
	loggedIn(g)
	g.status("GET /portfolio/DU1234567/ledger", 500, nil, `boom`)
	_, err := b.Balances(context.Background(), "DU1234567")
	if err == nil || strings.Contains(err.Error(), "1234567") {
		t.Fatalf("%v", err)
	}
}

func TestGatewayDownIsUnavailable(t *testing.T) {
	b, err := New(Options{BaseURL: "https://127.0.0.1:1", Timeout: 500 * time.Millisecond, MinInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Session(context.Background()); broker.KindOf(err) != broker.Unavailable {
		t.Fatalf("%v", err)
	}
}

func TestSelfSignedGatewayIsNotTrustedWithoutItsCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer srv.Close()
	b, err := New(Options{BaseURL: srv.URL, MinInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Session(context.Background()); broker.KindOf(err) != broker.Unavailable {
		t.Fatalf("an unverified certificate must be refused: %v", err)
	}
}

func TestQuoteDelayedAndMissingEntitlement(t *testing.T) {
	g, b := newGateway(t)
	loggedIn(g)
	ctx := context.Background()
	g.json("GET /iserver/marketdata/snapshot", `[{"conid":265598,"31":"190.5","84":"190.4","86":"190.6","6509":"D","_updated":1793718000000}]`)
	q, err := b.Quote(ctx, 265598)
	if err != nil || !q.Delayed || q.Last.String() != "190.5" || q.At.IsZero() {
		t.Fatalf("%+v %v", q, err)
	}
	g.json("GET /iserver/marketdata/snapshot", `[{"conid":265598,"31":"C190.5","6509":"R"}]`)
	if q, err = b.Quote(ctx, 265598); err != nil || q.Delayed || q.Last.String() != "190.5" {
		t.Fatalf("%+v %v", q, err)
	}
	g.json("GET /iserver/marketdata/snapshot", `[{"conid":265598}]`)
	if _, err = b.Quote(ctx, 265598); broker.KindOf(err) != broker.Entitlement {
		t.Fatalf("%v", err)
	}
}

func TestPlaceSendsTheConfirmedTermsAndReturnsQuestionsUnanswered(t *testing.T) {
	g, b := newGateway(t)
	loggedIn(g)
	ctx := context.Background()
	req := broker.OrderRequest{Account: "DU1234567", Conid: 265598, Side: "buy", Qty: dec("10"), Limit: dec("190.10"), TIF: "DAY", Ref: "ord_x"}
	g.json("POST /iserver/account/DU1234567/orders", `[{"id":"r-1","message":["The order price is outside the collar. Continue?"]}]`)
	res, err := b.Place(ctx, req)
	if err != nil || res.OrderID != "" || len(res.Replies) != 1 || res.Replies[0].ID != "r-1" || !strings.Contains(res.Replies[0].Messages[0], "collar") {
		t.Fatalf("%+v %v", res, err)
	}
	var sent struct {
		Orders []map[string]any `json:"orders"`
	}
	if err := json.Unmarshal([]byte(g.bodies["POST /iserver/account/DU1234567/orders"]), &sent); err != nil || len(sent.Orders) != 1 {
		t.Fatal(err)
	}
	o := sent.Orders[0]
	if o["orderType"] != "LMT" || o["side"] != "BUY" || o["tif"] != "DAY" || o["cOID"] != "ord_x" || o["acctId"] != "DU1234567" || o["quantity"].(float64) != 10 || o["price"].(float64) != 190.10 {
		t.Fatalf("%v", o)
	}
	for _, c := range g.calls {
		if strings.Contains(c, "/reply/") {
			t.Fatal("the adapter must never answer the broker's question by itself")
		}
	}
	g.json("POST /iserver/reply/r-1", `[{"order_id":"1001","order_status":"Submitted"}]`)
	if res, err = b.Confirm(ctx, "r-1"); err != nil || res.OrderID != "1001" {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(g.bodies["POST /iserver/reply/r-1"], `"confirmed":true`) {
		t.Fatal(g.bodies["POST /iserver/reply/r-1"])
	}
}

func TestPlaceRefusalsAndUnsupportedOrders(t *testing.T) {
	g, b := newGateway(t)
	loggedIn(g)
	ctx := context.Background()
	req := broker.OrderRequest{Account: "DU1234567", Conid: 1, Side: "buy", Qty: dec("1"), Limit: dec("1"), TIF: "DAY", Ref: "r"}
	g.json("POST /iserver/account/DU1234567/orders", `[{"error":"insufficient funds"}]`)
	if _, err := b.Place(ctx, req); broker.KindOf(err) != broker.Refused {
		t.Fatalf("%v", err)
	}
	g.json("POST /iserver/account/DU1234567/orders", `[]`)
	if _, err := b.Place(ctx, req); broker.KindOf(err) != broker.Malformed {
		t.Fatalf("%v", err)
	}
	bad := req
	bad.TIF = "GTC"
	if _, err := b.Place(ctx, bad); broker.KindOf(err) != broker.Refused {
		t.Fatalf("%v", err)
	}
	bad = req
	bad.Ref = ""
	if _, err := b.Place(ctx, bad); broker.KindOf(err) != broker.Refused {
		t.Fatalf("%v", err)
	}
}

func TestPreviewWarningsAndCancel(t *testing.T) {
	g, b := newGateway(t)
	loggedIn(g)
	ctx := context.Background()
	req := broker.OrderRequest{Account: "DU1234567", Conid: 1, Side: "sell", Qty: dec("1"), Limit: dec("2"), TIF: "DAY", Ref: "r"}
	g.json("POST /iserver/account/DU1234567/orders/whatif", `{"amount":{"amount":"2 USD","commission":"1.05 USD","total":"3.05 USD"},"warn":"Order exceeds 3% of average volume."}`)
	p, err := b.Preview(ctx, req)
	if err != nil || p.Commission != 105 || p.Currency != "USD" || len(p.Warnings) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	g.json("POST /iserver/account/DU1234567/orders/whatif", `{"error":"not allowed"}`)
	if _, err := b.Preview(ctx, req); broker.KindOf(err) != broker.Refused {
		t.Fatalf("%v", err)
	}
	g.json("DELETE /iserver/account/DU1234567/order/1001", `{"msg":"Request was submitted","order_id":1001}`)
	if err := b.Cancel(ctx, "DU1234567", "1001"); err != nil {
		t.Fatal(err)
	}
}

func TestPacingSpacesRequests(t *testing.T) {
	g, _ := newGateway(t)
	loggedIn(g)
	var waits []time.Duration
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"authenticated":true}`)) }))
	defer srv.Close()
	b, err := New(Options{BaseURL: srv.URL, RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, MinInterval: time.Hour,
		Sleep: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := b.Session(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(waits) != 2 || waits[0] <= 0 {
		t.Fatalf("%v", waits)
	}
}

func TestIsPaper(t *testing.T) {
	if !IsPaper("DU1234567") || IsPaper("U7654321") {
		t.Fatal("paper accounts begin with DU")
	}
}

func dec(s string) money.Decimal { return money.MustDecimal(s) }
