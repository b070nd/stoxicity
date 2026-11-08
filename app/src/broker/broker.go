// Package broker is the seam between this program and a broker. Everything the
// rest of the program knows about a broker is in this file: what it can be
// asked, what it can answer, and the ways it can fail. Transport and
// authentication stay behind the interface, so the order, reconciliation and
// tier code never see a URL, a cookie or a credential.
//
// Two implementations exist. package fixture is a broker in a JSON file, for
// tests and for practising the workflow; package ibkr talks to the Interactive
// Brokers Client Portal Gateway on this machine. Neither is a promise that a
// particular account can do a particular thing: entitlements, paper access,
// market data and order types differ between accounts, and the answer for the
// account in hand is what Session, Accounts and Quote report.
package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Session is the state of the login the user performed. The program never
// logs in: a person does, in the broker's own page.
type Session struct {
	Authenticated bool `json:"authenticated"`
	Connected     bool `json:"connected"`
	// Competing means another session (the mobile app, the web page) holds the
	// brokerage session, so this one cannot trade until it is taken back.
	Competing bool      `json:"competing"`
	Message   string    `json:"message"`
	At        time.Time `json:"at"`
}

// Account is a broker account. Paper says it is the broker's own simulated
// account, which is a different thing from this program's local paper book.
type Account struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Paper bool   `json:"paper"`
	// Features the account reports it may use; a feature that is absent is not
	// assumed.
	Features map[string]bool `json:"features"`
}

// Balance is cash in one currency, in minor units.
type Balance struct {
	Currency money.Currency `json:"currency"`
	Cash     int64          `json:"cash"`
	Settled  int64          `json:"settled"`
}

// Position is a holding at the broker.
type Position struct {
	Conid    int64          `json:"conid"`
	Symbol   string         `json:"symbol"`
	Qty      money.Decimal  `json:"qty"`
	Currency money.Currency `json:"currency"`
}

// OpenOrder is an order the broker holds. Ref is the client reference this
// program sent with it, which is how the two sides match.
type OpenOrder struct {
	ID     string        `json:"id"`
	Ref    string        `json:"ref"`
	Conid  int64         `json:"conid"`
	Side   string        `json:"side"` // buy or sell
	Qty    money.Decimal `json:"qty"`
	Filled money.Decimal `json:"filled"`
	Limit  money.Decimal `json:"limit"`
	TIF    string        `json:"tif"`
	Status string        `json:"status"`
}

// Execution is one fill the broker reports.
type Execution struct {
	ID         string         `json:"id"`
	OrderID    string         `json:"order_id"`
	Ref        string         `json:"ref"`
	Conid      int64          `json:"conid"`
	Side       string         `json:"side"`
	Qty        money.Decimal  `json:"qty"`
	Price      money.Decimal  `json:"price"`
	Commission int64          `json:"commission"` // minor units of Currency
	Currency   money.Currency `json:"currency"`
	At         time.Time      `json:"at"`
}

// Contract is the broker's description of a tradable line.
type Contract struct {
	Conid    int64          `json:"conid"`
	Symbol   string         `json:"symbol"`
	Name     string         `json:"name"`
	Exchange string         `json:"exchange"`
	Currency money.Currency `json:"currency"`
	Type     string         `json:"type"`
}

// Quote is a price the broker gave, with its age and whether it is delayed. A
// delayed or old quote is not an executable price.
type Quote struct {
	Conid   int64         `json:"conid"`
	Last    money.Decimal `json:"last"`
	Bid     money.Decimal `json:"bid"`
	Ask     money.Decimal `json:"ask"`
	At      time.Time     `json:"at"`
	Delayed bool          `json:"delayed"`
}

// OrderRequest is what is sent to place or preview an order. It is the same
// terms the user confirmed: nothing is added or changed on the way.
type OrderRequest struct {
	Account string        `json:"account"`
	Conid   int64         `json:"conid"`
	Side    string        `json:"side"`
	Qty     money.Decimal `json:"qty"`
	Limit   money.Decimal `json:"limit"`
	TIF     string        `json:"tif"`
	Ref     string        `json:"ref"`
}

// Preview is the broker's answer to "what would happen if". Warnings are shown
// to the user word for word and must be acknowledged; they are never dropped.
type Preview struct {
	Warnings   []string       `json:"warnings"`
	Commission int64          `json:"commission"`
	Currency   money.Currency `json:"currency"`
}

// Reply is the broker asking for a confirmation before it will place an order.
type Reply struct {
	ID       string   `json:"id"`
	Messages []string `json:"messages"`
}

// PlaceResult is what placing an order answered: an order ID, or questions
// that must be answered first.
type PlaceResult struct {
	OrderID string  `json:"order_id"`
	Status  string  `json:"status"`
	Replies []Reply `json:"replies"`
}

// Broker is what the program may ask of a broker.
type Broker interface {
	Session(ctx context.Context) (Session, error)
	Accounts(ctx context.Context) ([]Account, error)
	Balances(ctx context.Context, account string) ([]Balance, error)
	Positions(ctx context.Context, account string) ([]Position, error)
	OpenOrders(ctx context.Context, account string) ([]OpenOrder, error)
	Executions(ctx context.Context, account string) ([]Execution, error)
	Contract(ctx context.Context, conid int64) (Contract, error)
	Search(ctx context.Context, symbol string) ([]Contract, error)
	Quote(ctx context.Context, conid int64) (Quote, error)
	Preview(ctx context.Context, r OrderRequest) (Preview, error)
	Place(ctx context.Context, r OrderRequest) (PlaceResult, error)
	// Confirm answers one of the broker's questions with yes.
	Confirm(ctx context.Context, replyID string) (PlaceResult, error)
	Cancel(ctx context.Context, account, orderID string) error
}

// Kind of failure.
type Kind string

// The failures a caller must tell apart.
const (
	SessionExpired Kind = "session_expired" // log in again in the broker's own page
	Competing      Kind = "competing_session"
	Pacing         Kind = "pacing" // too many requests; wait, do not retry blindly
	Maintenance    Kind = "maintenance"
	Entitlement    Kind = "entitlement" // the account may not see or do this
	Refused        Kind = "refused"     // the broker said no to this request
	Unavailable    Kind = "unavailable" // the broker could not be reached
	Malformed      Kind = "malformed"   // the answer was not in the expected shape
)

// Error is a classified broker failure.
type Error struct {
	Kind       Kind
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	s := fmt.Sprintf("broker: %s: %s", e.Kind, e.Message)
	if e.RetryAfter > 0 {
		s += fmt.Sprintf(" (try again after %s)", e.RetryAfter)
	}
	return s
}

// KindOf returns the kind of a broker error, or "" for any other error.
func KindOf(err error) Kind {
	var be *Error
	if errors.As(err, &be) {
		return be.Kind
	}
	return ""
}

// Mode is what the user allowed this connection to do.
type Mode string

// The modes: reading is the default, and trading is a separate, explicit choice.
const (
	ReadOnly Mode = "read_only"
	Trading  Mode = "trading"
)

// Redact hides all but the end of an identifier, so a status line or a log
// shows enough to recognise an account and not enough to use it.
func Redact(id string) string {
	id = strings.TrimSpace(id)
	switch n := len(id); {
	case n == 0:
		return ""
	case n <= 4:
		return strings.Repeat("*", n)
	default:
		return strings.Repeat("*", n-3) + id[n-3:]
	}
}
