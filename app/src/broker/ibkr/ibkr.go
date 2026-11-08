// Package ibkr talks to the Interactive Brokers Client Portal Gateway running on
// this machine. The user starts the gateway and logs in in its own page; this
// package never sees a password, never stores a cookie and never logs in.
//
// What is verified: the request and answer shapes below are checked against
// contract fixtures in this package's tests, which were written from the
// gateway's published endpoint list as recalled when the adapter was written.
// What is not: no request has been made to a real gateway or a real account, so
// field names, units and status strings must be confirmed against the current
// documentation and a paper account before real use. docs/broker.md lists the
// assumptions one by one.
package ibkr

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/money"
)

// Options configure the adapter.
type Options struct {
	// BaseURL is the gateway, for example https://localhost:5000. It must be a
	// loopback address: the gateway holds a live brokerage session and is not
	// to be reached over a network.
	BaseURL string
	// CAFile is a PEM file with the certificate the gateway presents. The
	// gateway's own certificate is self-signed, so it is trusted explicitly;
	// verification is never switched off.
	CAFile string
	// RootCAs replaces CAFile in tests.
	RootCAs *x509.CertPool
	// MinInterval spaces requests out; the gateway limits the request rate.
	MinInterval time.Duration
	// Timeout bounds one request.
	Timeout time.Duration
	// Sleep waits between requests; time.Sleep-like, honouring the context.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Broker is the adapter.
type Broker struct {
	opt  Options
	base *url.URL
	hc   *http.Client

	mu       sync.Mutex
	last     time.Time
	accounts bool
}

// ValidateBaseURL refuses anything but https to a loopback address.
func ValidateBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("ibkr: %q is not a gateway address (expected https://localhost:5000)", raw)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("ibkr: the gateway address must be https, not %q", u.Scheme)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("ibkr: the gateway address must not carry credentials, a query or a fragment")
	}
	host := u.Hostname()
	if host != "localhost" && !strings.HasSuffix(host, ".localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("ibkr: %q is not a loopback address: the gateway must run on this machine", host)
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

// New returns an adapter.
func New(opt Options) (*Broker, error) {
	u, err := ValidateBaseURL(opt.BaseURL)
	if err != nil {
		return nil, err
	}
	pool := opt.RootCAs
	if pool == nil && opt.CAFile != "" {
		raw, err := os.ReadFile(opt.CAFile)
		if err != nil {
			return nil, fmt.Errorf("ibkr: %w", err)
		}
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(raw) {
			return nil, fmt.Errorf("ibkr: %s holds no certificate", opt.CAFile)
		}
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 15 * time.Second
	}
	if opt.MinInterval == 0 {
		opt.MinInterval = 150 * time.Millisecond
	}
	if opt.Sleep == nil {
		opt.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	hc := &http.Client{Transport: tr, Timeout: opt.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Broker{opt: opt, base: u, hc: hc}, nil
}

// pace waits so that requests are at least MinInterval apart.
func (b *Broker) pace(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if wait := b.opt.MinInterval - time.Since(b.last); wait > 0 && !b.last.IsZero() {
		if err := b.opt.Sleep(ctx, wait); err != nil {
			return err
		}
	}
	b.last = time.Now()
	return nil
}

// do makes one request and returns the body, or a classified error. Account
// numbers never appear in an error: the path is not echoed.
func (b *Broker) do(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
	if err := b.pace(ctx); err != nil {
		return nil, &broker.Error{Kind: broker.Unavailable, Message: err.Error()}
	}
	u := *b.base
	u.Path += "/v1/api" + path
	u.RawQuery = query.Encode()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "stoxicity")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.hc.Do(req)
	if err != nil {
		return nil, &broker.Error{Kind: broker.Unavailable, Message: "the gateway could not be reached: is it running and logged in? (" + shortErr(err) + ")"}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, &broker.Error{Kind: broker.Unavailable, Message: "the answer was cut off: " + shortErr(err)}
	}
	if resp.StatusCode/100 == 2 {
		return raw, nil
	}
	return nil, classify(resp, raw)
}

func shortErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	s := err.Error()
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// classify turns a failed response into a broker error.
func classify(resp *http.Response, raw []byte) error {
	msg := serverMessage(raw)
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return &broker.Error{Kind: broker.SessionExpired, Message: "the gateway has no authenticated session: log in again in its own page"}
	case http.StatusTooManyRequests:
		ra := 10 * time.Second
		if s := resp.Header.Get("Retry-After"); s != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 {
				ra = time.Duration(n) * time.Second
			}
		}
		return &broker.Error{Kind: broker.Pacing, Message: "the gateway is limiting requests", RetryAfter: ra}
	case http.StatusServiceUnavailable:
		return &broker.Error{Kind: broker.Maintenance, Message: orDefault(msg, "the gateway or the broker is unavailable, possibly for maintenance")}
	case http.StatusForbidden:
		return &broker.Error{Kind: broker.Entitlement, Message: orDefault(msg, "the account may not do this")}
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return &broker.Error{Kind: broker.Unavailable, Message: orDefault(msg, "the gateway could not reach the broker")}
	}
	if resp.StatusCode/100 == 4 {
		return &broker.Error{Kind: broker.Refused, Message: orDefault(msg, "the request was refused")}
	}
	return &broker.Error{Kind: broker.Unavailable, Message: fmt.Sprintf("unexpected answer %d", resp.StatusCode)}
}

func serverMessage(raw []byte) string {
	var m struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	s := m.Error
	if s == "" {
		s = m.Message
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func orDefault(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func decode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return &broker.Error{Kind: broker.Malformed, Message: "the answer is not in the expected shape: " + shortErr(err)}
	}
	return nil
}

func malformed(format string, a ...any) error {
	return &broker.Error{Kind: broker.Malformed, Message: fmt.Sprintf(format, a...)}
}

// number reads a JSON number or numeric string as an exact decimal.
func number(v any) (money.Decimal, bool, error) {
	switch x := v.(type) {
	case nil:
		return money.Decimal{}, false, nil
	case json.Number:
		d, err := money.ParseDecimal(x.String())
		return d, true, err
	case string:
		s := strings.TrimSpace(strings.ReplaceAll(x, ",", ""))
		if s == "" {
			return money.Decimal{}, false, nil
		}
		d, err := money.ParseDecimal(s)
		return d, true, err
	}
	return money.Decimal{}, false, fmt.Errorf("%v is not a number", v)
}

func minorOf(v any, cur money.Currency) (int64, error) {
	d, ok, err := number(v)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	e, err := cur.Exponent()
	if err != nil {
		return 0, err
	}
	d, err = d.Rescale(uint8(e), money.HalfEven)
	if err != nil {
		return 0, err
	}
	a, err := money.NewAmount(d, cur, money.HalfEven)
	if err != nil {
		return 0, err
	}
	return a.Minor(), nil
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch x := m[k].(type) {
		case string:
			if x != "" {
				return x
			}
		case json.Number:
			return x.String()
		}
	}
	return ""
}

func int64Of(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		n, _ := strconv.ParseInt(x.String(), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	}
	return 0
}

// Session implements broker.Broker.
func (b *Broker) Session(ctx context.Context) (broker.Session, error) {
	raw, err := b.do(ctx, http.MethodGet, "/iserver/auth/status", nil, nil)
	if err != nil {
		// an expired session is an answer to this question, not a failure of it
		if broker.KindOf(err) == broker.SessionExpired {
			return broker.Session{Message: "not logged in", At: time.Now()}, nil
		}
		return broker.Session{}, err
	}
	var m struct {
		Authenticated bool   `json:"authenticated"`
		Connected     bool   `json:"connected"`
		Competing     bool   `json:"competing"`
		Message       string `json:"message"`
	}
	if err := decode(raw, &m); err != nil {
		return broker.Session{}, err
	}
	return broker.Session{Authenticated: m.Authenticated, Connected: m.Connected, Competing: m.Competing, Message: m.Message, At: time.Now()}, nil
}

func (b *Broker) needSession(ctx context.Context) error {
	s, err := b.Session(ctx)
	if err != nil {
		return err
	}
	switch {
	case !s.Authenticated:
		return &broker.Error{Kind: broker.SessionExpired, Message: "the gateway has no authenticated session: log in again in its own page"}
	case s.Competing:
		return &broker.Error{Kind: broker.Competing, Message: "another session holds the brokerage session: close it or take the session back in the gateway page"}
	}
	return nil
}

// Accounts implements broker.Broker.
func (b *Broker) Accounts(ctx context.Context) ([]broker.Account, error) {
	if err := b.needSession(ctx); err != nil {
		return nil, err
	}
	raw, err := b.do(ctx, http.MethodGet, "/portfolio/accounts", nil, nil)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := decode(raw, &rows); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.accounts = true
	b.mu.Unlock()
	var out []broker.Account
	for _, r := range rows {
		id := str(r, "id", "accountId")
		if id == "" {
			return nil, malformed("an account without an id")
		}
		out = append(out, broker.Account{ID: id, Type: str(r, "type"), Title: str(r, "accountTitle", "desc"), Paper: IsPaper(id)})
	}
	return out, nil
}

// IsPaper reports whether an account number looks like a paper-trading account.
// Interactive Brokers paper accounts begin with DU; that is an assumption about
// the broker's numbering, not something the gateway states, so it is only used
// to refuse a mismatch between a book's environment and the account, never to
// allow one.
func IsPaper(id string) bool { return strings.HasPrefix(strings.ToUpper(id), "DU") }

// the portfolio endpoints answer only after the account list was requested once
func (b *Broker) warm(ctx context.Context) error {
	b.mu.Lock()
	done := b.accounts
	b.mu.Unlock()
	if done {
		return nil
	}
	_, err := b.Accounts(ctx)
	return err
}

// Balances implements broker.Broker.
func (b *Broker) Balances(ctx context.Context, account string) ([]broker.Balance, error) {
	if err := b.warm(ctx); err != nil {
		return nil, err
	}
	raw, err := b.do(ctx, http.MethodGet, "/portfolio/"+url.PathEscape(account)+"/ledger", nil, nil)
	if err != nil {
		return nil, err
	}
	var m map[string]map[string]any
	if err := decode(raw, &m); err != nil {
		return nil, err
	}
	var out []broker.Balance
	for k, r := range m {
		if strings.EqualFold(k, "BASE") {
			continue // the base line repeats the others, converted
		}
		cur := money.Currency(strings.ToUpper(orDefault(str(r, "currency"), k)))
		if !cur.Valid() {
			return nil, malformed("balance in an unknown currency %q", cur)
		}
		cash, err := minorOf(r["cashbalance"], cur)
		if err != nil {
			return nil, malformed("cash balance in %s: %v", cur, err)
		}
		settled, err := minorOf(r["settledcash"], cur)
		if err != nil {
			return nil, malformed("settled cash in %s: %v", cur, err)
		}
		out = append(out, broker.Balance{Currency: cur, Cash: cash, Settled: settled})
	}
	return out, nil
}

// Positions implements broker.Broker.
func (b *Broker) Positions(ctx context.Context, account string) ([]broker.Position, error) {
	if err := b.warm(ctx); err != nil {
		return nil, err
	}
	var out []broker.Position
	for page := 0; page < 50; page++ {
		raw, err := b.do(ctx, http.MethodGet, fmt.Sprintf("/portfolio/%s/positions/%d", url.PathEscape(account), page), nil, nil)
		if err != nil {
			return nil, err
		}
		var rows []map[string]any
		if err := decode(raw, &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			q, ok, err := number(r["position"])
			if err != nil || !ok {
				return nil, malformed("a position without a quantity")
			}
			if q.IsZero() {
				continue
			}
			c := int64Of(r["conid"])
			if c == 0 {
				return nil, malformed("a position without a contract")
			}
			out = append(out, broker.Position{Conid: c, Symbol: str(r, "ticker", "contractDesc"), Qty: q, Currency: money.Currency(strings.ToUpper(str(r, "currency")))})
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
	return nil, malformed("more than 50 pages of positions")
}

func sideIn(s string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "BUY", "B", "BOT":
		return "buy", nil
	case "SELL", "S", "SLD":
		return "sell", nil
	}
	return "", fmt.Errorf("unknown side %q", s)
}

// OpenOrders implements broker.Broker.
func (b *Broker) OpenOrders(ctx context.Context, account string) ([]broker.OpenOrder, error) {
	if err := b.warm(ctx); err != nil {
		return nil, err
	}
	raw, err := b.do(ctx, http.MethodGet, "/iserver/account/orders", url.Values{"force": {"true"}}, nil)
	if err != nil {
		return nil, err
	}
	var m struct {
		Orders []map[string]any `json:"orders"`
	}
	if err := decode(raw, &m); err != nil {
		return nil, err
	}
	var out []broker.OpenOrder
	for _, r := range m.Orders {
		if acct := str(r, "acct", "account"); acct != "" && acct != account {
			continue
		}
		side, err := sideIn(str(r, "side"))
		if err != nil {
			return nil, malformed("%v", err)
		}
		qty, ok1, e1 := number(r["totalSize"])
		filled, _, e2 := number(r["filledQuantity"])
		limit, _, e3 := number(r["price"])
		if !ok1 || e1 != nil || e2 != nil || e3 != nil {
			return nil, malformed("order %s has an unreadable quantity or price", str(r, "orderId"))
		}
		out = append(out, broker.OpenOrder{ID: str(r, "orderId"), Ref: str(r, "order_ref", "cOID"), Conid: int64Of(r["conid"]), Side: side, Qty: qty, Filled: filled, Limit: limit, TIF: str(r, "timeInForce", "tif"), Status: str(r, "status")})
	}
	return out, nil
}

// Executions implements broker.Broker.
func (b *Broker) Executions(ctx context.Context, account string) ([]broker.Execution, error) {
	if err := b.warm(ctx); err != nil {
		return nil, err
	}
	raw, err := b.do(ctx, http.MethodGet, "/iserver/account/trades", nil, nil)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := decode(raw, &rows); err != nil {
		return nil, err
	}
	var out []broker.Execution
	for _, r := range rows {
		if acct := str(r, "account", "acct"); acct != "" && acct != account {
			continue
		}
		side, err := sideIn(str(r, "side"))
		if err != nil {
			return nil, malformed("%v", err)
		}
		qty, ok, e1 := number(r["size"])
		price, ok2, e2 := number(r["price"])
		if !ok || !ok2 || e1 != nil || e2 != nil {
			return nil, malformed("execution %s has an unreadable size or price", str(r, "execution_id"))
		}
		cur := money.Currency(strings.ToUpper(orDefault(str(r, "currency"), "USD")))
		fee, err := minorOf(r["commission"], cur)
		if err != nil {
			return nil, malformed("commission of execution %s: %v", str(r, "execution_id"), err)
		}
		at := time.Time{}
		if ms := int64Of(r["trade_time_r"]); ms > 0 {
			at = time.UnixMilli(ms).UTC()
		}
		id := str(r, "execution_id")
		if id == "" {
			return nil, malformed("an execution without an id")
		}
		out = append(out, broker.Execution{ID: id, OrderID: str(r, "order_id"), Ref: str(r, "order_ref"), Conid: int64Of(r["conid"]), Side: side, Qty: qty, Price: price, Commission: fee, Currency: cur, At: at})
	}
	return out, nil
}

func contractOf(r map[string]any) (broker.Contract, error) {
	c := broker.Contract{Conid: int64Of(r["con_id"]), Symbol: str(r, "symbol"), Name: str(r, "company_name", "companyName"), Exchange: str(r, "exchange", "listing_exchange"), Currency: money.Currency(strings.ToUpper(str(r, "currency"))), Type: str(r, "instrument_type", "secType")}
	if c.Conid == 0 {
		c.Conid = int64Of(r["conid"])
	}
	if c.Conid == 0 {
		return c, malformed("a contract without an id")
	}
	return c, nil
}

// Contract implements broker.Broker.
func (b *Broker) Contract(ctx context.Context, conid int64) (broker.Contract, error) {
	if err := b.needSession(ctx); err != nil {
		return broker.Contract{}, err
	}
	raw, err := b.do(ctx, http.MethodGet, fmt.Sprintf("/iserver/contract/%d/info", conid), nil, nil)
	if err != nil {
		return broker.Contract{}, err
	}
	var r map[string]any
	if err := decode(raw, &r); err != nil {
		return broker.Contract{}, err
	}
	c, err := contractOf(r)
	if err != nil {
		return c, err
	}
	if c.Conid != conid {
		return c, malformed("asked for contract %d and was given %d", conid, c.Conid)
	}
	return c, nil
}

// Search implements broker.Broker.
func (b *Broker) Search(ctx context.Context, symbol string) ([]broker.Contract, error) {
	if err := b.needSession(ctx); err != nil {
		return nil, err
	}
	raw, err := b.do(ctx, http.MethodPost, "/iserver/secdef/search", nil, map[string]any{"symbol": symbol, "name": false, "secType": "STK"})
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := decode(raw, &rows); err != nil {
		return nil, err
	}
	var out []broker.Contract
	for _, r := range rows {
		c, err := contractOf(r)
		if err != nil {
			return nil, err
		}
		if c.Symbol == "" {
			c.Symbol = symbol
		}
		out = append(out, c)
	}
	return out, nil
}

// Quote implements broker.Broker. The gateway answers the first snapshot request
// for a contract with the contract only and starts the feed, so the request is
// repeated once.
func (b *Broker) Quote(ctx context.Context, conid int64) (broker.Quote, error) {
	if err := b.needSession(ctx); err != nil {
		return broker.Quote{}, err
	}
	for try := 0; try < 2; try++ {
		raw, err := b.do(ctx, http.MethodGet, "/iserver/marketdata/snapshot", url.Values{"conids": {strconv.FormatInt(conid, 10)}, "fields": {"31,84,86,6509"}}, nil)
		if err != nil {
			return broker.Quote{}, err
		}
		var rows []map[string]any
		if err := decode(raw, &rows); err != nil {
			return broker.Quote{}, err
		}
		for _, r := range rows {
			if int64Of(r["conid"]) != conid {
				continue
			}
			last, hasLast, e1 := number(trimMark(str(r, "31")))
			bid, hasBid, e2 := number(trimMark(str(r, "84")))
			ask, hasAsk, e3 := number(trimMark(str(r, "86")))
			if e1 != nil || e2 != nil || e3 != nil {
				return broker.Quote{}, malformed("the quote of contract %d is unreadable", conid)
			}
			if !hasLast && !hasBid && !hasAsk {
				continue
			}
			q := broker.Quote{Conid: conid, Last: last, Bid: bid, Ask: ask}
			if ms := int64Of(r["_updated"]); ms > 0 {
				q.At = time.UnixMilli(ms).UTC()
			}
			// R real time; D delayed; Z frozen; Y frozen delayed
			q.Delayed = !strings.HasPrefix(strings.ToUpper(str(r, "6509")), "R")
			return q, nil
		}
		if err := b.opt.Sleep(ctx, 300*time.Millisecond); err != nil {
			return broker.Quote{}, &broker.Error{Kind: broker.Unavailable, Message: err.Error()}
		}
	}
	return broker.Quote{}, &broker.Error{Kind: broker.Entitlement, Message: fmt.Sprintf("no market data for contract %d: the account may lack the subscription", conid)}
}

// trimMark removes the letter prefix the gateway puts on some prices (C closing, H halted).
func trimMark(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && (s[0] < '0' || s[0] > '9') && s[0] != '-' && s[0] != '.' {
		return s[1:]
	}
	return s
}

func orderBody(r broker.OrderRequest) (map[string]any, error) {
	if r.Account == "" || r.Ref == "" || r.Conid == 0 {
		return nil, errors.New("an order needs an account, a contract and a client reference")
	}
	side := strings.ToUpper(r.Side)
	if side != "BUY" && side != "SELL" {
		return nil, fmt.Errorf("unknown side %q", r.Side)
	}
	if r.TIF != "DAY" {
		return nil, fmt.Errorf("only DAY orders are supported, not %q", r.TIF)
	}
	return map[string]any{"acctId": r.Account, "conid": r.Conid, "orderType": "LMT", "side": side, "tif": r.TIF, "quantity": json.Number(r.Qty.String()), "price": json.Number(r.Limit.String()), "cOID": r.Ref}, nil
}

// Preview implements broker.Broker.
func (b *Broker) Preview(ctx context.Context, r broker.OrderRequest) (broker.Preview, error) {
	if err := b.warm(ctx); err != nil {
		return broker.Preview{}, err
	}
	o, err := orderBody(r)
	if err != nil {
		return broker.Preview{}, &broker.Error{Kind: broker.Refused, Message: err.Error()}
	}
	raw, err := b.do(ctx, http.MethodPost, "/iserver/account/"+url.PathEscape(r.Account)+"/orders/whatif", nil, map[string]any{"orders": []any{o}})
	if err != nil {
		return broker.Preview{}, err
	}
	var m map[string]any
	if err := decode(raw, &m); err != nil {
		return broker.Preview{}, err
	}
	if e := str(m, "error"); e != "" {
		return broker.Preview{}, &broker.Error{Kind: broker.Refused, Message: "the broker's preview says: " + e}
	}
	var p broker.Preview
	if w := str(m, "warn"); w != "" {
		p.Warnings = append(p.Warnings, w)
	}
	if am, ok := m["amount"].(map[string]any); ok {
		// "1.05 USD"
		f := strings.Fields(str(am, "commission"))
		if len(f) == 2 {
			cur := money.Currency(strings.ToUpper(f[1]))
			if cur.Valid() {
				if p.Commission, err = minorOf(f[0], cur); err != nil {
					return broker.Preview{}, malformed("the commission %q is unreadable", f[0])
				}
				p.Currency = cur
			}
		}
	}
	return p, nil
}

// placed reads the answer to placing or confirming an order.
func placed(raw []byte) (broker.PlaceResult, error) {
	var rows []map[string]any
	if err := decode(raw, &rows); err != nil {
		return broker.PlaceResult{}, err
	}
	var res broker.PlaceResult
	for _, r := range rows {
		if e := str(r, "error"); e != "" {
			return broker.PlaceResult{}, &broker.Error{Kind: broker.Refused, Message: e}
		}
		if id := str(r, "order_id"); id != "" {
			res.OrderID, res.Status = id, str(r, "order_status")
			continue
		}
		if id := str(r, "id"); id != "" {
			rep := broker.Reply{ID: id}
			switch ms := r["message"].(type) {
			case []any:
				for _, m := range ms {
					if s, ok := m.(string); ok {
						rep.Messages = append(rep.Messages, s)
					}
				}
			case string:
				rep.Messages = []string{ms}
			}
			res.Replies = append(res.Replies, rep)
			continue
		}
		return broker.PlaceResult{}, malformed("an answer to an order that is neither an order nor a question")
	}
	if res.OrderID == "" && len(res.Replies) == 0 {
		return res, malformed("an empty answer to an order")
	}
	return res, nil
}

// Place implements broker.Broker. A question from the broker is returned, never
// answered here.
func (b *Broker) Place(ctx context.Context, r broker.OrderRequest) (broker.PlaceResult, error) {
	if err := b.needSession(ctx); err != nil {
		return broker.PlaceResult{}, err
	}
	o, err := orderBody(r)
	if err != nil {
		return broker.PlaceResult{}, &broker.Error{Kind: broker.Refused, Message: err.Error()}
	}
	raw, err := b.do(ctx, http.MethodPost, "/iserver/account/"+url.PathEscape(r.Account)+"/orders", nil, map[string]any{"orders": []any{o}})
	if err != nil {
		return broker.PlaceResult{}, err
	}
	return placed(raw)
}

// Confirm implements broker.Broker.
func (b *Broker) Confirm(ctx context.Context, replyID string) (broker.PlaceResult, error) {
	if err := b.needSession(ctx); err != nil {
		return broker.PlaceResult{}, err
	}
	raw, err := b.do(ctx, http.MethodPost, "/iserver/reply/"+url.PathEscape(replyID), nil, map[string]any{"confirmed": true})
	if err != nil {
		return broker.PlaceResult{}, err
	}
	return placed(raw)
}

// Cancel implements broker.Broker.
func (b *Broker) Cancel(ctx context.Context, account, orderID string) error {
	if err := b.needSession(ctx); err != nil {
		return err
	}
	_, err := b.do(ctx, http.MethodDelete, "/iserver/account/"+url.PathEscape(account)+"/order/"+url.PathEscape(orderID), nil, nil)
	return err
}

var _ broker.Broker = (*Broker)(nil)
