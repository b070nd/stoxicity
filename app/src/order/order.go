// Package order is the local record of an order: its exact terms, the cash or
// shares held for it, and every fact learned about it afterwards.
//
// The record is a log of events. The state of an order is never stored; it is
// derived by folding the log, so that facts arriving in the wrong order (a fill
// before the acknowledgement, a fill after a cancel request, the same fill told
// twice) give the same state as the same facts in the right order. The package
// is pure: it reads a ledger and writes events and ledger transactions, and the
// store applies both in one database transaction. Nothing here talks to a broker.
package order

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

// Side of an order. The first live subset is long-only: a sell reduces a holding.
type Side string

// The sides.
const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// State of an order, derived from its events.
type State string

// The states of section 5.11. Unknown means the log and the world may disagree
// and a person or a reconciliation must look before anything else happens.
const (
	Draft           State = "draft"
	Confirmed       State = "confirmed"
	Submitting      State = "submitting"
	Open            State = "open"
	PartiallyFilled State = "partially_filled"
	Filled          State = "filled"
	CancelRequested State = "cancel_requested"
	Cancelled       State = "cancelled"
	Rejected        State = "rejected"
	Expired         State = "expired"
	Unknown         State = "unknown"
)

// Closed reports whether nothing more can happen to the order except a late
// report of something that already happened.
func (s State) Closed() bool {
	switch s {
	case Filled, Cancelled, Rejected, Expired:
		return true
	}
	return false
}

// Terms are everything that defines an order. A confirmation is bound to their
// hash, so changing any of them needs a new confirmation.
type Terms struct {
	Book     string         `json:"book"`
	Account  string         `json:"account"`
	Env      portfolio.Env  `json:"env"`
	Security string         `json:"security"`
	Side     Side           `json:"side"`
	Qty      money.Decimal  `json:"qty"`
	Limit    money.Decimal  `json:"limit"`
	Currency money.Currency `json:"currency"`
	Type     string         `json:"type"` // "limit"
	TIF      string         `json:"tif"`  // "DAY"
	// FeeEstimate is the known fees of the whole order, in minor units of the
	// currency. A buy holds it in addition to quantity times limit.
	FeeEstimate int64 `json:"fee_estimate"`
	// Tick, when set, is the price increment the limit must be a multiple of.
	Tick money.Decimal `json:"tick,omitempty"`
	// Fractional says the account may trade part shares; otherwise the quantity
	// must be whole.
	Fractional   bool   `json:"fractional,omitempty"`
	PlanID       string `json:"plan_id,omitempty"`
	PlanRevision int    `json:"plan_revision,omitempty"`
	PolicyEpoch  int64  `json:"policy_epoch,omitempty"`
	PolicyHash   string `json:"policy_hash,omitempty"`
}

// Validate checks the terms against the first live subset: a limit DAY order,
// long-only, at a valid quantity and price.
func (t Terms) Validate() error {
	switch {
	case t.Book == "" || t.Account == "":
		return errors.New("order: the terms need a book and an account")
	case !t.Env.Valid():
		return fmt.Errorf("order: unknown environment %q", t.Env)
	case t.Security == "":
		return errors.New("order: the terms need a security")
	case t.Side != Buy && t.Side != Sell:
		return fmt.Errorf("order: side %q must be buy or sell", t.Side)
	case t.Type != "limit":
		return fmt.Errorf("order: only limit orders are supported, not %q", t.Type)
	case t.TIF != "DAY":
		return fmt.Errorf("order: only DAY orders are supported, not %q", t.TIF)
	case !t.Currency.Valid():
		return errors.New("order: the terms need a currency")
	case t.Qty.Sign() <= 0 || t.Qty.Scale() > portfolio.MaxQtyScale:
		return fmt.Errorf("order: quantity %s must be positive with at most %d digits", t.Qty, portfolio.MaxQtyScale)
	case t.Limit.Sign() <= 0:
		return errors.New("order: the limit price must be positive")
	case t.FeeEstimate < 0:
		return errors.New("order: the fee estimate cannot be negative")
	}
	if !t.Fractional {
		if r, err := t.Qty.Mod(money.Int(1)); err != nil || !r.IsZero() {
			return fmt.Errorf("order: quantity %s is not whole and the account is not stated to trade part shares", t.Qty)
		}
	}
	if t.Tick.Sign() > 0 {
		if r, err := t.Limit.Mod(t.Tick); err != nil || !r.IsZero() {
			return fmt.Errorf("order: limit %s is not a multiple of the tick %s", t.Limit, t.Tick)
		}
	}
	return nil
}

func canon(d money.Decimal) string {
	s := d.String()
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// Hash identifies the exact terms.
func (t Terms) Hash() string {
	parts := []string{t.Book, t.Account, string(t.Env), t.Security, string(t.Side), canon(t.Qty), canon(t.Limit), string(t.Currency),
		t.Type, t.TIF, fmt.Sprint(t.FeeEstimate), canon(t.Tick), fmt.Sprint(t.Fractional), t.PlanID, fmt.Sprint(t.PlanRevision), fmt.Sprint(t.PolicyEpoch), t.PolicyHash}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// Notional is quantity times limit, rounded up to the minor unit: the most the
// shares can cost before fees.
func (t Terms) Notional(qty money.Decimal) (money.Amount, error) {
	e, err := t.Currency.Exponent()
	if err != nil {
		return money.Amount{}, err
	}
	d, err := t.Limit.Mul(qty, uint8(e), money.Up)
	if err != nil {
		return money.Amount{}, err
	}
	return money.NewAmount(d, t.Currency, money.Up)
}

// Commitment is the most cash a buy can still use when qty shares are left and
// feesPaid of the estimated fees are already paid; a sell commits shares, not cash.
func (t Terms) Commitment(qty money.Decimal, feesPaid int64) (money.Amount, error) {
	n, err := t.Notional(qty)
	if err != nil {
		return money.Amount{}, err
	}
	fee := t.FeeEstimate - feesPaid
	if fee < 0 || qty.IsZero() {
		fee = 0
	}
	return money.FromMinor(n.Minor()+fee, t.Currency)
}

// Kind of an event.
type Kind string

// The event kinds. Drafted .. Unknown are local acts, the rest are facts about
// the order learned from a broker, a paper engine or a statement.
const (
	Drafted         Kind = "drafted"
	Reserved        Kind = "reserved"
	Released        Kind = "released"
	ConfirmedEv     Kind = "confirmed"
	SubmitStarted   Kind = "submit_started"
	CancelRequestEv Kind = "cancel_requested"
	LocalCancelled  Kind = "local_cancelled" // never sent; closed here
	LocalExpired    Kind = "local_expired"   // never sent; confirmation or draft lapsed
	UnknownEv       Kind = "unknown"
	Anomaly         Kind = "anomaly"
	Resolved        Kind = "resolved"
	Acknowledged    Kind = "acknowledged"
	FillEv          Kind = "fill"
	CancelledEv     Kind = "cancelled"
	RejectedEv      Kind = "rejected"
	ExpiredEv       Kind = "expired"
)

// Fill is one execution.
type Fill struct {
	ID    string        `json:"id"` // the broker's execution ID or the statement line
	Qty   money.Decimal `json:"qty"`
	Price money.Decimal `json:"price"`
	// Gross, when not zero, is the consideration in minor units as reported;
	// otherwise it is quantity times price.
	Gross     int64     `json:"gross,omitempty"`
	Fee       int64     `json:"fee,omitempty"` // commission in minor units
	At        time.Time `json:"at,omitempty"`
	SettleDay string    `json:"settle_day,omitempty"`
	// Posted says the fill reached the ledger. A fill that could not be posted is
	// still a fact about the order, and leaves it unknown.
	Posted bool `json:"posted"`
}

// Event is one entry of an order's log. N is the order's own sequence, assigned
// by the store; At is when it was recorded.
type Event struct {
	Order string    `json:"order"`
	N     int       `json:"n"`
	Kind  Kind      `json:"kind"`
	At    time.Time `json:"at"`
	By    string    `json:"by,omitempty"`
	// Key makes a reported fact idempotent: the same key under the same order and
	// kind is the same fact. Fills use their execution ID.
	Key string `json:"key,omitempty"`

	Terms    *Terms    `json:"terms,omitempty"`    // Drafted
	Warnings []string  `json:"warnings,omitempty"` // Drafted: checks that need an explicit acknowledgement
	Expires  time.Time `json:"expires,omitempty"`  // Drafted, ConfirmedEv

	Reservation string `json:"reservation,omitempty"` // Reserved, Released
	Held        string `json:"held,omitempty"`        // Reserved: what is held, for display

	Hash  string   `json:"hash,omitempty"`  // ConfirmedEv: the confirmation hash
	Acked []string `json:"acked,omitempty"` // ConfirmedEv: warnings acknowledged

	Attempt int    `json:"attempt,omitempty"` // SubmitStarted
	Broker  string `json:"broker,omitempty"`  // Acknowledged: the broker's reference
	Fill    *Fill  `json:"fill,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Confirmation is the explicit go-ahead for exact terms.
type Confirmation struct {
	Hash    string
	At      time.Time
	Expires time.Time
	By      string
	Acked   []string
}

// View is an order as the log says it is.
type View struct {
	ID        string
	Terms     Terms
	TermsHash string
	State     State
	Warnings  []string
	Expires   time.Time // when an unsent draft or confirmation lapses
	Created   time.Time
	Updated   time.Time

	Confirmation *Confirmation
	Attempts     int
	BrokerRef    string
	Filled       money.Decimal
	Remaining    money.Decimal
	FeesPaid     int64
	Unposted     money.Decimal // fills the broker reported that the ledger does not hold
	// Reservation is the hold currently kept for the order, empty when none.
	Reservation string
	Reserved    int // how many holds were ever made: the next ID is one more
	Events      int
	// Why is the reason of a rejection, an unknown outcome or an anomaly.
	Why []string
	// Late lists facts that arrived out of order, in plain words.
	Late []string
}

// HoldQty is the quantity the order's hold covers: what is ordered less what
// the ledger already holds as filled. A fill the ledger could not take does not
// reduce it.
func (v View) HoldQty() money.Decimal {
	q, err := v.Terms.Qty.Sub(v.postedQty())
	if err != nil {
		return v.Remaining
	}
	return q
}

func (v View) postedQty() money.Decimal {
	p, err := v.Filled.Sub(v.Unposted)
	if err != nil {
		return v.Filled
	}
	return p
}

// Terminal is Closed for the whole view.
func (v View) Terminal() bool { return v.State.Closed() }

// Sent reports whether the order ever left this program.
func (v View) Sent() bool { return v.Attempts > 0 }

// Fold derives the view of an order from its events, in the order they were
// recorded.
func Fold(events []Event) (View, error) {
	if len(events) == 0 {
		return View{}, errors.New("order: an order with no events")
	}
	first := events[0]
	if first.Kind != Drafted || first.Terms == nil {
		return View{}, fmt.Errorf("order: %s does not start with its terms", first.Order)
	}
	v := View{ID: first.Order, Terms: *first.Terms, TermsHash: first.Terms.Hash(), Warnings: first.Warnings, Expires: first.Expires, Created: first.At}
	seen := map[string]bool{}
	var (
		acked, cancelReq, cancelled, rejected, expired, local bool
		unknown, human                                        bool
		whySet                                                []string
	)
	filled := money.Int(0)
	unposted := money.Int(0)
	for i, e := range events {
		v.Events = i + 1
		v.Updated = e.At
		if e.Key != "" {
			// the same fact told twice changes nothing
			id := string(e.Kind) + "|" + e.Key
			if seen[id] {
				continue
			}
			seen[id] = true
		}
		switch e.Kind {
		case Drafted:
			if i != 0 {
				return View{}, fmt.Errorf("order: %s is drafted twice", e.Order)
			}
		case Reserved:
			v.Reservation = e.Reservation
			v.Reserved++
		case Released:
			if e.Reservation == v.Reservation {
				v.Reservation = ""
			}
		case ConfirmedEv:
			v.Confirmation = &Confirmation{Hash: e.Hash, At: e.At, Expires: e.Expires, By: e.By, Acked: e.Acked}
		case SubmitStarted:
			v.Attempts++
		case CancelRequestEv:
			cancelReq = true
		case LocalCancelled:
			local, cancelled = true, true
		case LocalExpired:
			local, expired = true, true
		case UnknownEv:
			unknown = true
			whySet = append(whySet, e.Reason)
		case Anomaly:
			human = true
			whySet = append(whySet, e.Reason)
		case Resolved:
			unknown, human = false, false
			whySet = nil
		case Acknowledged:
			acked, unknown = true, false
			if e.Broker != "" {
				v.BrokerRef = e.Broker
			}
		case FillEv:
			if e.Fill == nil {
				return View{}, fmt.Errorf("order: %s has a fill event without a fill", e.Order)
			}
			unknown = false
			if !acked && !cancelReq {
				v.Late = append(v.Late, fmt.Sprintf("fill %s arrived before the order was acknowledged", e.Fill.ID))
			}
			if cancelReq && !cancelled {
				v.Late = append(v.Late, fmt.Sprintf("fill %s arrived after the cancel request", e.Fill.ID))
			}
			if cancelled && !local {
				v.Late = append(v.Late, fmt.Sprintf("fill %s was reported after the cancellation", e.Fill.ID))
			}
			if v.Attempts == 0 {
				human = true
				whySet = append(whySet, fmt.Sprintf("fill %s for an order that was never sent from here", e.Fill.ID))
			}
			next, err := filled.Add(e.Fill.Qty)
			if err != nil {
				return View{}, err
			}
			if next.Cmp(v.Terms.Qty) > 0 {
				human = true
				whySet = append(whySet, fmt.Sprintf("fills would total %s of %s ordered shares: fill %s is not counted", next, v.Terms.Qty, e.Fill.ID))
				continue
			}
			filled = next
			if e.Fill.Posted {
				v.FeesPaid += e.Fill.Fee
			} else {
				if unposted, err = unposted.Add(e.Fill.Qty); err != nil {
					return View{}, err
				}
				human = true
				whySet = append(whySet, fmt.Sprintf("fill %s of %s is not in the ledger: %s", e.Fill.ID, e.Fill.Qty, e.Reason))
			}
		case CancelledEv:
			cancelled, unknown = true, false
		case RejectedEv:
			rejected, unknown = true, false
			whySet = append(whySet, e.Reason)
		case ExpiredEv:
			expired, unknown = true, false
		default:
			return View{}, fmt.Errorf("order: unknown event kind %q", e.Kind)
		}
	}
	v.Filled = filled
	v.Unposted = unposted
	rem, err := v.Terms.Qty.Sub(filled)
	if err != nil {
		return View{}, err
	}
	v.Remaining = rem
	v.Why = whySet

	switch {
	case rejected && filled.Sign() > 0:
		v.State = Unknown
		v.Why = append(v.Why, "the order is reported rejected but also filled")
	case human || unknown:
		v.State = Unknown
	case rem.IsZero():
		v.State = Filled
	case rejected:
		v.State = Rejected
	case cancelled:
		v.State = Cancelled
	case expired:
		v.State = Expired
	case cancelReq:
		v.State = CancelRequested
	case filled.Sign() > 0:
		v.State = PartiallyFilled
	case acked:
		v.State = Open
	case v.Attempts > 0:
		v.State = Submitting
	case v.Confirmation != nil:
		v.State = Confirmed
	default:
		v.State = Draft
	}
	return v, nil
}

// ConfirmHash binds a confirmation to the exact terms and to the hold made for
// them: if either changes, the confirmation no longer matches.
func ConfirmHash(termsHash, reservation string) string {
	sum := sha256.Sum256([]byte(termsHash + "\x1f" + reservation))
	return hex.EncodeToString(sum[:])
}
