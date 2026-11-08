// Package portfolio is the accounting of what is owned: books (real, local paper
// and broker paper) inside accounts, an immutable double-entry journal, tax lots,
// open-order reservations and settled versus unsettled cash. Everything a screen
// shows (cash, positions, cost, realised gain) is a projection rebuilt by
// replaying the journal in the order it was recorded; nothing is edited. A
// correction is a reversal and a new transaction.
//
// Conventions, stated once:
//
//   - Entries are signed: a debit (assets, expenses) is positive, a credit
//     (income, equity, gains, liabilities) negative. Every transaction sums to
//     zero in every currency on its own, so a foreign-exchange conversion goes
//     through an fx:clearing account that takes the leg in each currency.
//   - Cost basis is the price paid for the shares. Commissions and fees are
//     expenses of their own and are not added to cost or taken from proceeds. The
//     lot method (FIFO or LIFO) is the book's and is written on every sale. This
//     is a display convention for reporting, not tax reporting for any
//     jurisdiction.
//   - A trade settles later: it posts to the unsettled account and a settle
//     transaction moves it to cash. Settled cash cannot go negative unless the
//     book allows it.
//   - Event time says when something happened, recorded time when it was
//     written. Replay order is the order of recording, never of event time, so a
//     late-arriving statement line cannot change the cost of an earlier sale.
package portfolio

import (
	"fmt"
	"regexp"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Env separates books that must never mix.
type Env string

const (
	Real        Env = "real"
	PaperLocal  Env = "paper_local"
	PaperBroker Env = "paper_broker"
)

// Valid reports whether e is one of the three.
func (e Env) Valid() bool { return e == Real || e == PaperLocal || e == PaperBroker }

// Paper reports whether the book is simulated.
func (e Env) Paper() bool { return e == PaperLocal || e == PaperBroker }

// CostMethod picks which lots a sale consumes.
type CostMethod string

const (
	FIFO CostMethod = "fifo"
	LIFO CostMethod = "lifo"
)

// BookStatus of a book.
type BookStatus string

const (
	Active   BookStatus = "active"
	Archived BookStatus = "archived"
)

// Book is one ledger: one environment of one account of a tenant.
type Book struct {
	ID      string
	Account string
	Name    string
	Env     Env
	// Base is the reporting currency.
	Base   money.Currency
	Method CostMethod
	// AllowNegativeCash lets settled cash go below zero (margin). Off by default.
	AllowNegativeCash bool
	Status            BookStatus
}

// Validate says why a book cannot be created.
func (b Book) Validate() error {
	switch {
	case !validName(b.Account):
		return fmt.Errorf("portfolio: the account %q must be 1-64 letters, digits, dash, underscore or dot", b.Account)
	case !validName(b.Name):
		return fmt.Errorf("portfolio: the book name %q must be 1-64 letters, digits, dash, underscore or dot", b.Name)
	case !b.Env.Valid():
		return fmt.Errorf("portfolio: unknown environment %q (real, paper_local, paper_broker)", b.Env)
	case !b.Base.Valid():
		return fmt.Errorf("portfolio: the base currency %q is not known", b.Base)
	case b.Method != FIFO && b.Method != LIFO:
		return fmt.Errorf("portfolio: unknown cost method %q (fifo, lifo)", b.Method)
	}
	return nil
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validName(s string) bool { return nameRE.MatchString(s) }

// Kind of transaction.
type Kind string

const (
	Deposit     Kind = "deposit"
	Withdrawal  Kind = "withdrawal"
	Buy         Kind = "buy"
	Sell        Kind = "sell"
	Fee         Kind = "fee"
	Income      Kind = "income" // dividend or interest, with withholding
	DRIP        Kind = "drip"   // a dividend reinvested in the same security
	FX          Kind = "fx"
	Split       Kind = "split"
	Settle      Kind = "settle"
	TransferOut Kind = "transfer_out"
	TransferIn  Kind = "transfer_in"
	Reversal    Kind = "reversal"
	Reserve     Kind = "reserve"
	Release     Kind = "release"
)

var allKinds = []Kind{Deposit, Withdrawal, Buy, Sell, Fee, Income, DRIP, FX, Split, Settle, TransferOut, TransferIn, Reversal, Reserve, Release}

// Entry is one posting.
type Entry struct {
	// Account is an account code (see ValidAccount).
	Account string `json:"account"`
	// Amount is signed: debit positive, credit negative.
	Amount money.Amount `json:"amount"`
	// Security, Qty and the lot fields are for entries on securities:<id> accounts.
	// Qty is signed shares; Amount is the cost carried with them.
	Security string        `json:"security,omitempty"`
	Qty      money.Decimal `json:"qty,omitempty"`
	// Lot names the lot the entry changes. NewLot opens one (its ID is assigned
	// when the transaction is recorded).
	Lot    string `json:"lot,omitempty"`
	NewLot bool   `json:"new_lot,omitempty"`
	// Acquired is the day a new lot was acquired (YYYY-MM-DD), kept across transfers.
	Acquired string `json:"acquired,omitempty"`
}

// ReservationKind says what a reservation holds back.
type ReservationKind string

const (
	ReserveCash   ReservationKind = "cash"
	ReserveShares ReservationKind = "shares"
)

// Reservation holds cash or shares for an open order so that nothing else can
// spend them. It is released explicitly or consumed by the fill that names it.
type Reservation struct {
	ID       string          `json:"id"`
	Kind     ReservationKind `json:"kind"`
	Currency money.Currency  `json:"currency,omitempty"`
	Amount   int64           `json:"amount,omitempty"` // minor units, for cash
	Security string          `json:"security,omitempty"`
	Qty      money.Decimal   `json:"qty,omitempty"` // for shares
	Intent   string          `json:"intent,omitempty"`
}

// Tx is one immutable transaction.
type Tx struct {
	ID       string
	Seq      int64
	Book     string
	Kind     Kind
	Event    time.Time
	Recorded time.Time
	// Source says where it came from ("manual", "ibkr-statement", a file name).
	Source string
	// ImportID is the external identifier of the record this came from. A book never
	// holds two transactions with the same one: reposting the same content is a
	// no-op and different content is refused.
	ImportID string
	Memo     string
	Reverses string
	// Releases names a reservation this transaction consumes.
	Releases string
	// Reservation is the hold a Reserve transaction creates.
	Reservation *Reservation
	Entries     []Entry
	// Meta holds facts that are not postings: price, settle_day, rate, settle_of.
	Meta map[string]string
}

var accountRE = regexp.MustCompile(`^(cash|unsettled|equity:contributions|equity:transfers|income:(dividend|interest|other)|expense:(commission|fee|withholding)|gain:realized|fx:clearing|receivable:dividend|securities:[A-Za-z0-9_]{1,64})$`)

// ValidAccount reports whether code is an account this ledger knows.
func ValidAccount(code string) bool { return accountRE.MatchString(code) }

// SecurityAccount is the account code of a security's holding.
func SecurityAccount(id string) string { return "securities:" + id }

// allowed lists, per kind, the account prefixes it may touch. A transaction that
// posts outside its kind is refused: a "deposit" cannot book income.
var allowed = map[Kind][]string{
	Deposit:     {"cash", "equity:contributions"},
	Withdrawal:  {"cash", "equity:contributions"},
	Buy:         {"securities:", "unsettled", "expense:commission", "expense:fee", "cash"},
	Sell:        {"securities:", "unsettled", "gain:realized", "expense:commission", "expense:fee", "cash"},
	Fee:         {"expense:fee", "expense:commission", "cash"},
	Income:      {"cash", "unsettled", "income:", "expense:withholding", "receivable:dividend"},
	DRIP:        {"securities:", "income:dividend", "expense:withholding"},
	FX:          {"cash", "fx:clearing", "expense:fee"},
	Split:       {"securities:"},
	Settle:      {"cash", "unsettled"},
	TransferOut: {"cash", "securities:", "equity:transfers"},
	TransferIn:  {"cash", "securities:", "equity:transfers"},
}

func allowedAccount(k Kind, code string) bool {
	if k == Reversal {
		return true // checked against the transaction it reverses
	}
	for _, p := range allowed[k] {
		if code == p || (len(p) > 0 && p[len(p)-1] == ':' && len(code) > len(p) && code[:len(p)] == p) {
			return true
		}
	}
	return false
}
