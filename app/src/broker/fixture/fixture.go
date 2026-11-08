// Package fixture is a broker kept in a JSON file. It answers like a broker
// would, applies placed orders to the file, and can be scripted to fail the way
// the real one does (an expired session, a competing session, pacing, a missing
// entitlement, an answer lost after the order was placed), so those paths are
// exercised without an account. Nothing here ever leaves the machine.
package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/money"
)

// Failure is a scripted failure of one method.
type Failure struct {
	Kind         broker.Kind `json:"kind"`
	Message      string      `json:"message"`
	RetryAfterMS int64       `json:"retry_after_ms,omitempty"`
	// Once removes the failure after it happened.
	Once bool `json:"once,omitempty"`
	// After makes the effect happen and the answer fail: for Place, the order is
	// placed at the broker and the caller is told it could not be reached.
	After bool `json:"after,omitempty"`
}

// Script scripts the broker's behaviour for orders.
type Script struct {
	PreviewWarnings []string `json:"preview_warnings,omitempty"`
	PlaceReplies    []string `json:"place_replies,omitempty"` // messages the broker wants confirmed
	PlaceReject     string   `json:"place_reject,omitempty"`
	Commission      int64    `json:"commission,omitempty"`
}

// State is the whole broker.
type State struct {
	Session    broker.Session                 `json:"session"`
	Accounts   []broker.Account               `json:"accounts"`
	Balances   map[string][]broker.Balance    `json:"balances"`
	Positions  map[string][]broker.Position   `json:"positions"`
	Orders     map[string][]broker.OpenOrder  `json:"orders"`
	Executions map[string][]broker.Execution  `json:"executions"`
	Contracts  []broker.Contract              `json:"contracts"`
	Quotes     []broker.Quote                 `json:"quotes"`
	Script     Script                         `json:"script"`
	Failures   map[string]Failure             `json:"failures,omitempty"` // by method name
	Pending    map[string]broker.OrderRequest `json:"pending,omitempty"`  // replies waiting for a yes
	NextOrder  int                            `json:"next_order"`
	// Placed is every order request that was accepted, for assertions.
	Placed []broker.OrderRequest `json:"placed,omitempty"`
}

// Broker is a fixture broker over a file.
type Broker struct {
	path string
	mu   sync.Mutex
	// Now stamps what the fixture creates; the real clock when nil.
	Now func() time.Time
}

// Open returns the fixture broker kept in path. The file must exist.
func Open(path string) (*Broker, error) {
	b := &Broker{path: path}
	if _, err := b.load(); err != nil {
		return nil, err
	}
	return b, nil
}

// Create writes a state to path and returns the broker over it.
func Create(path string, s State) (*Broker, error) {
	b := &Broker{path: path}
	if err := b.save(&s); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Broker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Broker) load() (*State, error) {
	raw, err := os.ReadFile(b.path)
	if err != nil {
		return nil, fmt.Errorf("fixture broker: %w", err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("fixture broker: %s: %w", b.path, err)
	}
	return &s, nil
}

func (b *Broker) save(s *State) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

// scripted returns the scripted failure of a method, and removes it when it was
// to happen once.
func (b *Broker) scripted(s *State, method string, after bool) error {
	f, ok := s.Failures[method]
	if !ok || f.After != after {
		return nil
	}
	if f.Once {
		delete(s.Failures, method)
		if err := b.save(s); err != nil {
			return err
		}
	}
	return &broker.Error{Kind: f.Kind, Message: f.Message, RetryAfter: time.Duration(f.RetryAfterMS) * time.Millisecond}
}

// read runs a query on the state after the scripted failure for the method.
func read[T any](b *Broker, method string, q func(*State) (T, error)) (T, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var zero T
	s, err := b.load()
	if err != nil {
		return zero, err
	}
	if err := b.scripted(s, method, false); err != nil {
		return zero, err
	}
	return q(s)
}

// Session implements broker.Broker.
func (b *Broker) Session(_ context.Context) (broker.Session, error) {
	return read(b, "Session", func(s *State) (broker.Session, error) {
		x := s.Session
		if x.At.IsZero() {
			x.At = b.now()
		}
		return x, nil
	})
}

// need fails like the real gateway does when nobody is logged in.
func need(s *State) error {
	switch {
	case !s.Session.Authenticated:
		return &broker.Error{Kind: broker.SessionExpired, Message: "the session is not authenticated: log in again in the broker's own page"}
	case s.Session.Competing:
		return &broker.Error{Kind: broker.Competing, Message: "another session holds the brokerage session"}
	}
	return nil
}

// Accounts implements broker.Broker.
func (b *Broker) Accounts(_ context.Context) ([]broker.Account, error) {
	return read(b, "Accounts", func(s *State) ([]broker.Account, error) { return s.Accounts, need(s) })
}

func hasAccount(s *State, id string) error {
	for _, a := range s.Accounts {
		if a.ID == id {
			return nil
		}
	}
	return &broker.Error{Kind: broker.Entitlement, Message: "the session has no access to account " + broker.Redact(id)}
}

// Balances implements broker.Broker.
func (b *Broker) Balances(_ context.Context, account string) ([]broker.Balance, error) {
	return read(b, "Balances", func(s *State) ([]broker.Balance, error) {
		if err := need(s); err != nil {
			return nil, err
		}
		return s.Balances[account], hasAccount(s, account)
	})
}

// Positions implements broker.Broker.
func (b *Broker) Positions(_ context.Context, account string) ([]broker.Position, error) {
	return read(b, "Positions", func(s *State) ([]broker.Position, error) {
		if err := need(s); err != nil {
			return nil, err
		}
		return s.Positions[account], hasAccount(s, account)
	})
}

// OpenOrders implements broker.Broker.
func (b *Broker) OpenOrders(_ context.Context, account string) ([]broker.OpenOrder, error) {
	return read(b, "OpenOrders", func(s *State) ([]broker.OpenOrder, error) {
		if err := need(s); err != nil {
			return nil, err
		}
		return s.Orders[account], hasAccount(s, account)
	})
}

// Executions implements broker.Broker.
func (b *Broker) Executions(_ context.Context, account string) ([]broker.Execution, error) {
	return read(b, "Executions", func(s *State) ([]broker.Execution, error) {
		if err := need(s); err != nil {
			return nil, err
		}
		return s.Executions[account], hasAccount(s, account)
	})
}

// Contract implements broker.Broker.
func (b *Broker) Contract(_ context.Context, conid int64) (broker.Contract, error) {
	return read(b, "Contract", func(s *State) (broker.Contract, error) {
		if err := need(s); err != nil {
			return broker.Contract{}, err
		}
		for _, c := range s.Contracts {
			if c.Conid == conid {
				return c, nil
			}
		}
		return broker.Contract{}, &broker.Error{Kind: broker.Refused, Message: fmt.Sprintf("no contract %d", conid)}
	})
}

// Search implements broker.Broker.
func (b *Broker) Search(_ context.Context, symbol string) ([]broker.Contract, error) {
	return read(b, "Search", func(s *State) ([]broker.Contract, error) {
		if err := need(s); err != nil {
			return nil, err
		}
		var out []broker.Contract
		for _, c := range s.Contracts {
			if strings.EqualFold(c.Symbol, symbol) {
				out = append(out, c)
			}
		}
		return out, nil
	})
}

// Quote implements broker.Broker.
func (b *Broker) Quote(_ context.Context, conid int64) (broker.Quote, error) {
	return read(b, "Quote", func(s *State) (broker.Quote, error) {
		if err := need(s); err != nil {
			return broker.Quote{}, err
		}
		for _, q := range s.Quotes {
			if q.Conid == conid {
				return q, nil
			}
		}
		return broker.Quote{}, &broker.Error{Kind: broker.Entitlement, Message: fmt.Sprintf("no market data for contract %d: the account may lack the subscription", conid)}
	})
}

func checkRequest(s *State, r broker.OrderRequest) error {
	if err := need(s); err != nil {
		return err
	}
	if err := hasAccount(s, r.Account); err != nil {
		return err
	}
	if r.Ref == "" {
		return &broker.Error{Kind: broker.Refused, Message: "an order without a client reference cannot be matched later"}
	}
	if r.Qty.Sign() <= 0 || r.Limit.Sign() <= 0 {
		return &broker.Error{Kind: broker.Refused, Message: "quantity and limit must be positive"}
	}
	return nil
}

// Preview implements broker.Broker.
func (b *Broker) Preview(_ context.Context, r broker.OrderRequest) (broker.Preview, error) {
	return read(b, "Preview", func(s *State) (broker.Preview, error) {
		if err := checkRequest(s, r); err != nil {
			return broker.Preview{}, err
		}
		return broker.Preview{Warnings: s.Script.PreviewWarnings, Commission: s.Script.Commission, Currency: "USD"}, nil
	})
}

// Place implements broker.Broker.
func (b *Broker) Place(_ context.Context, r broker.OrderRequest) (broker.PlaceResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, err := b.load()
	if err != nil {
		return broker.PlaceResult{}, err
	}
	if err := b.scripted(s, "Place", false); err != nil {
		return broker.PlaceResult{}, err
	}
	if err := checkRequest(s, r); err != nil {
		return broker.PlaceResult{}, err
	}
	if s.Script.PlaceReject != "" {
		return broker.PlaceResult{}, &broker.Error{Kind: broker.Refused, Message: s.Script.PlaceReject}
	}
	if len(s.Script.PlaceReplies) > 0 {
		if s.Pending == nil {
			s.Pending = map[string]broker.OrderRequest{}
		}
		id := fmt.Sprintf("reply-%d", len(s.Pending)+1)
		s.Pending[id] = r
		if err := b.save(s); err != nil {
			return broker.PlaceResult{}, err
		}
		return broker.PlaceResult{Replies: []broker.Reply{{ID: id, Messages: s.Script.PlaceReplies}}}, nil
	}
	res := b.accept(s, r)
	if err := b.save(s); err != nil {
		return broker.PlaceResult{}, err
	}
	if err := b.scripted(s, "Place", true); err != nil {
		// the order is at the broker and the answer was lost
		return broker.PlaceResult{}, err
	}
	return res, nil
}

// accept puts the order on the book.
func (b *Broker) accept(s *State, r broker.OrderRequest) broker.PlaceResult {
	s.NextOrder++
	id := fmt.Sprintf("%d", 1000+s.NextOrder)
	s.Orders[r.Account] = append(s.Orders[r.Account], broker.OpenOrder{ID: id, Ref: r.Ref, Conid: r.Conid, Side: r.Side, Qty: r.Qty, Filled: money.Int(0), Limit: r.Limit, TIF: r.TIF, Status: "Submitted"})
	s.Placed = append(s.Placed, r)
	return broker.PlaceResult{OrderID: id, Status: "Submitted"}
}

// Confirm implements broker.Broker.
func (b *Broker) Confirm(_ context.Context, replyID string) (broker.PlaceResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, err := b.load()
	if err != nil {
		return broker.PlaceResult{}, err
	}
	if err := need(s); err != nil {
		return broker.PlaceResult{}, err
	}
	r, ok := s.Pending[replyID]
	if !ok {
		return broker.PlaceResult{}, &broker.Error{Kind: broker.Refused, Message: "no question " + replyID + " is waiting"}
	}
	delete(s.Pending, replyID)
	res := b.accept(s, r)
	return res, b.save(s)
}

// Cancel implements broker.Broker. The order stays until the broker reports it
// cancelled; here that is the status the file shows afterwards.
func (b *Broker) Cancel(_ context.Context, account, orderID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, err := b.load()
	if err != nil {
		return err
	}
	if err := b.scripted(s, "Cancel", false); err != nil {
		return err
	}
	if err := need(s); err != nil {
		return err
	}
	for i, o := range s.Orders[account] {
		if o.ID == orderID {
			s.Orders[account][i].Status = "PendingCancel"
			return b.save(s)
		}
	}
	return &broker.Error{Kind: broker.Refused, Message: "no open order " + orderID}
}

var _ broker.Broker = (*Broker)(nil)
