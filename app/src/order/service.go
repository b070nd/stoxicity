package order

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/portfolio"
)

// Runner is the store the service works through. Run reads a book's ledger, the
// logs of its account and the controls, calls fn, and writes the ledger
// transactions and events fn produced in one transaction, or nothing.
type Runner interface {
	Run(ctx context.Context, book string, fn func(Context) ([]Event, error)) error
	Logs(ctx context.Context, account string) (map[string][]Event, error)
	Controls(ctx context.Context) (map[portfolio.Env]Control, error)
	SetControl(ctx context.Context, env portfolio.Env, disabled bool, reason, by string) error
}

// Sender places a submission with the other side: a broker adapter or the local
// paper engine. It is called only after the intent is durable, and an error from
// it means the outcome is not known.
type Sender func(ctx context.Context, s Submission) (SendResult, error)

// Canceller asks the other side to cancel. Its success means the request was
// made, not that the order ended.
type Canceller func(ctx context.Context, id, brokerRef string) error

// Service is the order workflow over a store.
type Service struct {
	Store  Runner
	Limits Limits
	// Gate limits new risk by tier and health; nil means no gate.
	Gate *Gate
}

func (s Service) run(ctx context.Context, book string, fn func(Context) ([]Event, error)) error {
	return s.Store.Run(ctx, book, func(c Context) ([]Event, error) {
		c.Limits, c.Gate = s.Limits, s.Gate
		return fn(c)
	})
}

// Draft records an intent and holds what it needs, atomically.
func (s Service) Draft(ctx context.Context, book string, d DraftRequest) (string, error) {
	var id string
	err := s.run(ctx, book, func(c Context) ([]Event, error) {
		var evs []Event
		var err error
		id, evs, err = NewDraft(c, d)
		return evs, err
	})
	return id, err
}

// Confirm records the explicit go-ahead for a draft.
func (s Service) Confirm(ctx context.Context, book, id, by string, acked []string, ttl time.Duration) error {
	return s.run(ctx, book, func(c Context) ([]Event, error) { return Confirm(c, id, by, acked, ttl) })
}

// ConfirmBound records the go-ahead for a draft together with what a broker was
// shown, so that only that can be sent.
func (s Service) ConfirmBound(ctx context.Context, book, id, by string, acked []string, ttl time.Duration, b Binding) error {
	return s.run(ctx, book, func(c Context) ([]Event, error) { return ConfirmBound(c, id, by, acked, ttl, b) })
}

// Submit writes the intent to send, calls send, and writes the answer. The two
// writes are separate transactions on purpose: the external call is never made
// inside one, and never made before the first is durable. Whatever happens to the
// process after the first write, the order is submitting or later, and an order
// that has been sent once is never sent again.
func (s Service) Submit(ctx context.Context, book, id, by string, send Sender) error {
	return s.SubmitBound(ctx, book, id, by, "", send)
}

// SubmitBound is Submit for an order that goes to a broker: payload is what
// would be sent now and must be what the confirmation covers.
func (s Service) SubmitBound(ctx context.Context, book, id, by, payload string, send Sender) error {
	var sub Submission
	if err := s.run(ctx, book, func(c Context) ([]Event, error) {
		var evs []Event
		var err error
		sub, evs, err = BeginSubmitBound(c, id, by, payload)
		return evs, err
	}); err != nil {
		return err
	}
	res, sendErr := send(ctx, sub)
	if err := s.run(ctx, book, func(c Context) ([]Event, error) { return FinishSubmit(c, id, res, sendErr) }); err != nil {
		return fmt.Errorf("order: %s was sent but its answer could not be recorded: %w", id, err)
	}
	return nil
}

// Cancel asks for the order to end. For a sent order the request is durable
// before the other side is asked, and nothing is assumed about the answer: the
// order stays cancel-requested until a cancellation is reported.
func (s Service) Cancel(ctx context.Context, book, id, by string, cancel Canceller) error {
	var ask bool
	var ref string
	if err := s.run(ctx, book, func(c Context) ([]Event, error) {
		evs, a, err := RequestCancel(c, id, by)
		ask = a
		if v, _, verr := c.view(id); verr == nil {
			ref = v.BrokerRef
		}
		return evs, err
	}); err != nil {
		return err
	}
	if !ask || cancel == nil {
		return nil
	}
	if err := cancel(ctx, id, ref); err != nil {
		return fmt.Errorf("order: the cancel request for %s is recorded but the other side could not be asked: %w", id, err)
	}
	return nil
}

// Report applies facts learned from outside to an order.
func (s Service) Report(ctx context.Context, book, id string, rs ...Reported) error {
	return s.run(ctx, book, func(c Context) ([]Event, error) {
		var all []Event
		for _, r := range rs {
			evs, err := Report(c, id, r)
			if err != nil {
				return nil, err
			}
			all = append(all, evs...)
			// later reports in the batch see what this one wrote
			if len(evs) > 0 {
				c.Logs = withEvents(c.Logs, id, evs)
			}
		}
		return all, nil
	})
}

func withEvents(logs map[string][]Event, id string, evs []Event) map[string][]Event {
	out := make(map[string][]Event, len(logs))
	for k, v := range logs {
		out[k] = v
	}
	out[id] = append(append([]Event(nil), logs[id]...), evs...)
	return out
}

// Recover is the restart of the local side for one book.
func (s Service) Recover(ctx context.Context, book string) error {
	return s.run(ctx, book, Recover)
}

// RevokeUnsent withdraws the confirmations of buys that were confirmed but not sent.
func (s Service) RevokeUnsent(ctx context.Context, book, reason string) error {
	return s.run(ctx, book, func(c Context) ([]Event, error) { return RevokeUnsent(c, reason) })
}

// Sweep closes lapsed unsent orders of one book and frees what they held.
func (s Service) Sweep(ctx context.Context, book string) error {
	return s.run(ctx, book, Sweep)
}

// Resolve clears the unknown flag after a person compared the order with the
// other side.
func (s Service) Resolve(ctx context.Context, book, id, by, note string) error {
	return s.run(ctx, book, func(c Context) ([]Event, error) { return Resolve(c, id, by, note) })
}

// Views folds every order of an account, or of all accounts when it is empty.
func (s Service) Views(ctx context.Context, account string) ([]View, error) {
	logs, err := s.Store.Logs(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(logs))
	for _, l := range logs {
		v, err := Fold(l)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
