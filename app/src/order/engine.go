package order

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

// Limits are the account limits every order of an account shares, taken from
// the active policy. A zero value means no limit of that kind.
type Limits struct {
	MaxOpenOrders     int
	MaxTradesPerMonth int
	// MaxCostBurden is the largest known fee as a fraction of the order's value.
	MaxCostBurden money.Decimal
}

// Control is the durable switch for new orders of one environment. Cancelling
// and reconciling stay possible whatever it says.
type Control struct {
	Disabled bool
	Reason   string
	By       string
	At       time.Time
}

// DefaultControl is what an environment has before anyone set it: orders that
// would reach a broker are off until someone turns them on, a local paper book
// has no broker to protect.
func DefaultControl(env portfolio.Env) Control {
	if env == portfolio.PaperLocal {
		return Control{}
	}
	return Control{Disabled: true, Reason: "new orders are disabled until you enable them for " + string(env)}
}

// Gate is what the tier and the health of the execution inputs allow. It
// limits new risk only: a sale, a cancellation and a report are never gated, so
// an unhealthy or demoted account can always reduce risk.
type Gate struct {
	Tier int
	// Suspended lists the reasons new risk is suspended; empty means it is not.
	Suspended []string
}

// Check says whether an order with these terms may take this step.
func (g Gate) Check(t Terms, step string) error {
	if t.Env == portfolio.PaperLocal || t.Side == Sell {
		return nil
	}
	if g.Tier < 1 {
		return fmt.Errorf("order: tier %d allows paper trading only: a %s order cannot be %s (policy tier)", g.Tier, t.Env, step)
	}
	if len(g.Suspended) > 0 {
		return fmt.Errorf("order: new risk is suspended (%s): a buy cannot be %s; selling, cancelling and reporting stay possible", strings.Join(g.Suspended, "; "), step)
	}
	return nil
}

// Context is everything a command reads: the book's ledger, the logs of all
// orders of the account (so that limits are shared), and the controls.
type Context struct {
	Now      time.Time
	Ledger   *portfolio.Ledger
	Logs     map[string][]Event
	Controls map[portfolio.Env]Control
	Limits   Limits
	// Gate, when set, limits new risk by tier and health.
	Gate *Gate
}

func (c Context) gate(t Terms, step string) error {
	if c.Gate == nil {
		return nil
	}
	return c.Gate.Check(t, step)
}

func (c Context) control(env portfolio.Env) Control {
	if ct, ok := c.Controls[env]; ok {
		return ct
	}
	return DefaultControl(env)
}

func (c Context) views() ([]View, error) {
	ids := make([]string, 0, len(c.Logs))
	for id := range c.Logs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]View, 0, len(ids))
	for _, id := range ids {
		v, err := Fold(c.Logs[id])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (c Context) view(id string) (View, []Event, error) {
	log, ok := c.Logs[id]
	if !ok {
		return View{}, nil, fmt.Errorf("order: no order %s", id)
	}
	v, err := Fold(log)
	if err != nil {
		return View{}, nil, err
	}
	if v.Terms.Book != c.Ledger.Book.ID {
		return View{}, nil, fmt.Errorf("order: %s belongs to another book", id)
	}
	return v, log, nil
}

func (c Context) ev(order string, k Kind, by string) Event {
	return Event{Order: order, Kind: k, At: c.Now, By: by}
}

func has(log []Event, k Kind, key string) bool {
	for _, e := range log {
		if e.Kind == k && e.Key == key {
			return true
		}
	}
	return false
}

func (c Context) common(id, importID, memo string) portfolio.Common {
	return portfolio.Common{Event: c.Now, Source: "order:" + id, ImportID: importID, Memo: memo}
}

// reserve holds what the remaining quantity can still use. It posts to the
// ledger and returns the event that says so.
func (c Context) reserve(id string, t Terms, n int, remaining money.Decimal, feesPaid int64) (Event, error) {
	resID := fmt.Sprintf("%s/r%d", id, n)
	var tx portfolio.Tx
	var held string
	switch t.Side {
	case Buy:
		amt, err := t.Commitment(remaining, feesPaid)
		if err != nil {
			return Event{}, err
		}
		if tx, err = c.Ledger.ReserveCash(c.common(id, "reserve:"+resID, "cash held for "+id), resID, amt, id); err != nil {
			return Event{}, err
		}
		held = amt.String()
	default:
		tx = c.Ledger.ReserveSharesFor(c.common(id, "reserve:"+resID, "shares held for "+id), resID, t.Security, remaining, id)
		held = remaining.String() + " " + t.Security
	}
	if _, err := c.Ledger.Post(tx, c.Now); err != nil {
		return Event{}, fmt.Errorf("order: cannot hold what the order needs: %w", err)
	}
	e := c.ev(id, Reserved, "")
	e.Reservation, e.Held = resID, held
	return e, nil
}

// release gives the order's current hold back, if it has one.
func (c Context) release(v View) ([]Event, error) {
	if v.Reservation == "" {
		return nil, nil
	}
	active := false
	for _, r := range c.Ledger.Projection().Reservations() {
		if r.ID == v.Reservation {
			active = true
		}
	}
	if active {
		if _, err := c.Ledger.Post(c.Ledger.ReleaseReservation(c.common(v.ID, "release:"+v.Reservation, "hold of "+v.ID+" released"), v.Reservation), c.Now); err != nil {
			return nil, err
		}
	}
	e := c.ev(v.ID, Released, "")
	e.Reservation = v.Reservation
	return []Event{e}, nil
}

// DraftRequest is a request for a new order.
type DraftRequest struct {
	Terms    Terms
	Warnings []string
	By       string
	// Valid is how long the draft may wait for a confirmation.
	Valid time.Duration
}

// NewDraft records the intent and holds the cash or shares it needs, so that no
// other proposal can spend them. The intent and the hold are written together by
// the caller or not at all.
func NewDraft(c Context, d DraftRequest) (string, []Event, error) {
	t := d.Terms
	if err := t.Validate(); err != nil {
		return "", nil, err
	}
	if err := c.gate(t, "drafted"); err != nil {
		return "", nil, err
	}
	b := c.Ledger.Book
	if t.Book != b.ID || t.Account != b.Account || t.Env != b.Env {
		return "", nil, fmt.Errorf("order: the terms name %s/%s (%s) but the book is %s/%s (%s)", t.Account, t.Book, t.Env, b.Account, b.ID, b.Env)
	}
	if b.Status == portfolio.Archived {
		return "", nil, fmt.Errorf("order: book %s is archived", b.ID)
	}
	if d.Valid <= 0 {
		return "", nil, fmt.Errorf("order: a draft needs a validity longer than zero")
	}
	vs, err := c.views()
	if err != nil {
		return "", nil, err
	}
	if err := checkLimits(c, vs, t); err != nil {
		return "", nil, err
	}
	n := 0
	for _, v := range vs {
		if v.Terms.Book == b.ID {
			n++
		}
		if !v.Terminal() && v.TermsHash == t.Hash() {
			return "", nil, fmt.Errorf("order: %s already has exactly these terms and is %s", v.ID, v.State)
		}
	}
	id := fmt.Sprintf("ord_%s_%04d", b.ID[len("bk_"):], n+1)
	if _, exists := c.Logs[id]; exists {
		return "", nil, fmt.Errorf("order: the order ID %s is already used", id)
	}
	first := c.ev(id, Drafted, d.By)
	first.Terms, first.Warnings, first.Expires = &t, d.Warnings, c.Now.Add(d.Valid)
	res, err := c.reserve(id, t, 1, t.Qty, 0)
	if err != nil {
		return "", nil, err
	}
	res.By = d.By
	return id, []Event{first, res}, nil
}

func checkLimits(c Context, vs []View, t Terms) error {
	l := c.Limits
	open, month := 0, 0
	for _, v := range vs {
		if v.Terms.Account != t.Account {
			continue
		}
		if !v.Terminal() {
			open++
		}
		y, m, _ := v.Created.UTC().Date()
		ny, nm, _ := c.Now.UTC().Date()
		if y == ny && m == nm && (v.Sent() || !v.Terminal()) {
			month++
		}
	}
	if l.MaxOpenOrders > 0 && open+1 > l.MaxOpenOrders {
		return fmt.Errorf("order: account %s already has %d open orders; the limit is %d", t.Account, open, l.MaxOpenOrders)
	}
	if l.MaxTradesPerMonth > 0 && month+1 > l.MaxTradesPerMonth {
		return fmt.Errorf("order: account %s has %d orders this month; the policy allows %d", t.Account, month, l.MaxTradesPerMonth)
	}
	if l.MaxCostBurden.Sign() > 0 && t.FeeEstimate > 0 {
		n, err := t.Notional(t.Qty)
		if err != nil {
			return err
		}
		cap, err := n.Times(l.MaxCostBurden, money.Up)
		if err != nil {
			return err
		}
		if t.FeeEstimate > cap.Minor() {
			return fmt.Errorf("order: the known fees %d are more than %s of the order value (limit %s of %s)", t.FeeEstimate, l.MaxCostBurden, l.MaxCostBurden, n)
		}
	}
	return nil
}

// Confirm records the explicit go-ahead for the exact terms of a draft. Every
// warning the draft carries must be acknowledged by its exact text.
func Confirm(c Context, id, by string, acked []string, ttl time.Duration) ([]Event, error) {
	v, _, err := c.view(id)
	if err != nil {
		return nil, err
	}
	switch {
	case v.State == Draft:
	case v.State == Confirmed && v.Confirmation != nil && c.Now.After(v.Confirmation.Expires):
	default:
		return nil, fmt.Errorf("order: %s is %s; only a draft or an expired confirmation can be confirmed", id, v.State)
	}
	if by == "" {
		return nil, fmt.Errorf("order: a confirmation says who gives it")
	}
	if err := c.gate(v.Terms, "confirmed"); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("order: a confirmation needs a validity longer than zero")
	}
	if !c.Now.Before(v.Expires) {
		return nil, fmt.Errorf("order: the draft lapsed at %s", v.Expires.UTC().Format(time.RFC3339))
	}
	have := map[string]bool{}
	for _, a := range acked {
		have[a] = true
	}
	var missing []string
	for _, w := range v.Warnings {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("order: %d warning(s) not acknowledged: %q", len(missing), missing)
	}
	exp := c.Now.Add(ttl)
	if exp.After(v.Expires) {
		exp = v.Expires
	}
	e := c.ev(id, ConfirmedEv, by)
	e.Hash, e.Expires, e.Acked = ConfirmHash(v.TermsHash, v.Reservation), exp, acked
	return []Event{e}, nil
}

// Submission is what a sender is given. The order ID is the client reference
// that correlates the order at the other side; sending the same one twice must
// not place two orders.
type Submission struct {
	OrderID   string
	Terms     Terms
	TermsHash string
	Attempt   int
}

// BeginSubmit checks that the order may leave and records that it is leaving,
// before any external call. If the process ends after this event the order is
// submitting: nothing is known about the outside and nothing is sent again.
func BeginSubmit(c Context, id, by string) (Submission, []Event, error) {
	v, _, err := c.view(id)
	if err != nil {
		return Submission{}, nil, err
	}
	if v.State != Confirmed {
		return Submission{}, nil, fmt.Errorf("order: %s is %s; only a confirmed order is submitted", id, v.State)
	}
	if v.Attempts > 0 {
		return Submission{}, nil, fmt.Errorf("order: %s was already sent once; reconcile it instead of sending again", id)
	}
	cf := v.Confirmation
	if cf == nil || c.Now.After(cf.Expires) {
		return Submission{}, nil, fmt.Errorf("order: the confirmation of %s has expired: confirm it again", id)
	}
	if cf.Hash != ConfirmHash(v.TermsHash, v.Reservation) {
		return Submission{}, nil, fmt.Errorf("order: the confirmation of %s no longer matches its terms and hold: confirm it again", id)
	}
	held := false
	for _, r := range c.Ledger.Projection().Reservations() {
		if r.ID == v.Reservation {
			held = true
		}
	}
	if !held {
		return Submission{}, nil, fmt.Errorf("order: the hold of %s is gone", id)
	}
	if err := c.gate(v.Terms, "submitted"); err != nil {
		return Submission{}, nil, err
	}
	if ct := c.control(v.Terms.Env); ct.Disabled {
		return Submission{}, nil, fmt.Errorf("order: new orders are disabled for %s: %s", v.Terms.Env, ct.Reason)
	}
	e := c.ev(id, SubmitStarted, by)
	e.Attempt = v.Attempts + 1
	return Submission{OrderID: id, Terms: v.Terms, TermsHash: v.TermsHash, Attempt: e.Attempt}, []Event{e}, nil
}

// SendResult is what the other side said to a submission.
type SendResult struct {
	Broker   string
	Rejected bool
	Reason   string
}

// FinishSubmit records the outcome of the external call. An error, including a
// timeout, is not a rejection: the order may be working, so it is unknown until
// the outside is asked.
func FinishSubmit(c Context, id string, res SendResult, sendErr error) ([]Event, error) {
	v, _, err := c.view(id)
	if err != nil {
		return nil, err
	}
	if v.State != Submitting {
		return nil, fmt.Errorf("order: %s is %s, not submitting", id, v.State)
	}
	switch {
	case sendErr != nil:
		e := c.ev(id, UnknownEv, "")
		e.Reason = "no answer to the submission: " + sendErr.Error() + "; ask the broker before sending anything again"
		return []Event{e}, nil
	case res.Rejected:
		return Report(c, id, Reported{Kind: RejectedEv, Key: "submit", Reason: res.Reason})
	default:
		return Report(c, id, Reported{Kind: Acknowledged, Key: "ack", Broker: res.Broker})
	}
}

// RequestCancel asks for the order to end. An order that never left is closed
// at once and its hold released; for a sent order the request is recorded and
// the order stays as it is until the other side confirms. The second result says
// the other side must be asked.
func RequestCancel(c Context, id, by string) ([]Event, bool, error) {
	v, _, err := c.view(id)
	if err != nil {
		return nil, false, err
	}
	if v.Terminal() {
		return nil, false, fmt.Errorf("order: %s is already %s", id, v.State)
	}
	if !v.Sent() {
		e := c.ev(id, LocalCancelled, by)
		rel, err := c.release(v)
		if err != nil {
			return nil, false, err
		}
		return append([]Event{e}, rel...), false, nil
	}
	if v.State == CancelRequested {
		return nil, true, nil
	}
	return []Event{c.ev(id, CancelRequestEv, by)}, true, nil
}

// Reported is a fact learned about an order from outside.
type Reported struct {
	Kind   Kind // Acknowledged, FillEv, CancelledEv, RejectedEv or ExpiredEv
	Key    string
	Broker string
	Fill   *Fill
	Reason string
}

// Report applies a fact learned from the broker, a paper engine or a statement.
// Facts may come in any order and any number of times. A fill is posted to the
// ledger, consuming the hold and holding what is left; a cancellation, rejection
// or expiry releases only what is still held.
func Report(c Context, id string, r Reported) ([]Event, error) {
	v, log, err := c.view(id)
	if err != nil {
		return nil, err
	}
	key := r.Key
	if r.Kind == FillEv {
		if r.Fill == nil || r.Fill.ID == "" {
			return nil, fmt.Errorf("order: a fill needs an execution ID")
		}
		key = r.Fill.ID
	}
	if key == "" {
		key = string(r.Kind)
	}
	if has(log, r.Kind, key) {
		return nil, nil
	}
	e := c.ev(id, r.Kind, "")
	e.Key, e.Broker, e.Reason = key, r.Broker, r.Reason
	switch r.Kind {
	case Acknowledged:
		return []Event{e}, nil
	case FillEv:
		return fill(c, v, *r.Fill)
	case CancelledEv, ExpiredEv:
		rel, err := c.release(v)
		if err != nil {
			return nil, err
		}
		return append([]Event{e}, rel...), nil
	case RejectedEv:
		evs := []Event{e}
		if v.Filled.IsZero() {
			rel, err := c.release(v)
			if err != nil {
				return nil, err
			}
			evs = append(evs, rel...)
		}
		return evs, nil
	}
	return nil, fmt.Errorf("order: %q is not something that can be reported", r.Kind)
}

func fill(c Context, v View, f Fill) ([]Event, error) {
	e := c.ev(v.ID, FillEv, "")
	e.Key = f.ID
	if f.Qty.Sign() <= 0 {
		return nil, fmt.Errorf("order: fill %s has no positive quantity", f.ID)
	}
	if f.At.IsZero() {
		f.At = c.Now
	}
	next, err := v.Filled.Add(f.Qty)
	if err != nil {
		return nil, err
	}
	if next.Cmp(v.Terms.Qty) > 0 {
		f.Posted = false
		e.Fill, e.Reason = &f, "more shares than were ordered"
		return []Event{e}, nil
	}
	posted, perr := postFill(c, v, f)
	if perr != nil {
		f.Posted = false
		e.Fill, e.Reason = &f, perr.Error()
		return []Event{e}, nil
	}
	f.Posted = posted
	e.Fill = &f
	evs := []Event{e}
	if v.Reservation != "" {
		// the trade consumed the whole hold
		r := c.ev(v.ID, Released, "")
		r.Reservation = v.Reservation
		evs = append(evs, r)
	}
	left, err := v.HoldQty().Sub(f.Qty)
	if err != nil {
		return nil, err
	}
	live := !v.State.Closed() && v.State != Cancelled
	if left.Sign() > 0 && live && v.Reservation != "" {
		res, rerr := c.reserve(v.ID, v.Terms, v.Reserved+1, left, v.FeesPaid+f.Fee)
		if rerr != nil {
			a := c.ev(v.ID, Anomaly, "")
			a.Reason = "the hold for the rest of the order could not be made: " + rerr.Error()
			return append(evs, a), nil
		}
		evs = append(evs, res)
	}
	return evs, nil
}

func postFill(c Context, v View, f Fill) (bool, error) {
	t := v.Terms
	gross := f.Gross
	if gross == 0 {
		e, err := t.Currency.Exponent()
		if err != nil {
			return false, err
		}
		d, err := f.Price.Mul(f.Qty, uint8(e), money.HalfEven)
		if err != nil {
			return false, err
		}
		a, err := money.NewAmount(d, t.Currency, money.HalfEven)
		if err != nil {
			return false, err
		}
		gross = a.Minor()
	}
	ga, err := money.FromMinor(gross, t.Currency)
	if err != nil {
		return false, err
	}
	tr := portfolio.Trade{
		Common:    c.common(v.ID, "fill:"+v.ID+":"+f.ID, fmt.Sprintf("fill %s of %s", f.ID, v.ID)),
		Security:  t.Security,
		Qty:       f.Qty,
		Gross:     ga,
		SettleDay: f.SettleDay,
		Price:     f.Price.String(),
		Releases:  v.Reservation,
	}
	tr.Event = f.At
	if f.Fee > 0 {
		if tr.Commission, err = money.FromMinor(f.Fee, t.Currency); err != nil {
			return false, err
		}
	}
	var tx portfolio.Tx
	if t.Side == Buy {
		tx, err = c.Ledger.Buy(tr)
	} else {
		tx, err = c.Ledger.Sell(tr)
	}
	if err != nil {
		return false, err
	}
	if _, err := c.Ledger.Post(tx, c.Now); err != nil {
		return false, err
	}
	return true, nil
}

// Recover is the restart reconciliation of the local side: an order caught
// while submitting is unknown, because the process ended between writing the
// intent and learning the answer. It is never resent.
func Recover(c Context) ([]Event, error) {
	vs, err := c.views()
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, v := range vs {
		if v.Terms.Book == c.Ledger.Book.ID && v.State == Submitting {
			e := c.ev(v.ID, UnknownEv, "")
			e.Reason = "the program ended while the order was being submitted; ask the broker before sending anything again"
			out = append(out, e)
		}
	}
	return out, nil
}

// Sweep closes drafts and confirmed orders that were never sent and have lapsed,
// releasing what they held.
func Sweep(c Context) ([]Event, error) {
	vs, err := c.views()
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, v := range vs {
		if v.Terms.Book != c.Ledger.Book.ID || v.Sent() || v.Terminal() || c.Now.Before(v.Expires) {
			continue
		}
		out = append(out, c.ev(v.ID, LocalExpired, ""))
		rel, err := c.release(v)
		if err != nil {
			return nil, err
		}
		out = append(out, rel...)
	}
	return out, nil
}

// RevokeUnsent withdraws the confirmation of every buy that was confirmed but
// not sent, in a book whose environment reaches a broker. The orders stay as
// drafts with their holds; holdings, open orders and sales are untouched.
func RevokeUnsent(c Context, reason string) ([]Event, error) {
	vs, err := c.views()
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, v := range vs {
		if v.Terms.Book != c.Ledger.Book.ID || v.State != Confirmed || v.Sent() || v.Terms.Side != Buy || v.Terms.Env == portfolio.PaperLocal {
			continue
		}
		e := c.ev(v.ID, RevokedEv, "")
		e.Reason = reason
		out = append(out, e)
	}
	return out, nil
}

// Resolve records that a person looked at an unknown order and compared it with
// the other side. It clears the flag, not the facts.
func Resolve(c Context, id, by, note string) ([]Event, error) {
	v, _, err := c.view(id)
	if err != nil {
		return nil, err
	}
	if v.State != Unknown {
		return nil, fmt.Errorf("order: %s is %s, not unknown", id, v.State)
	}
	if by == "" || note == "" {
		return nil, fmt.Errorf("order: resolving says who looked and what was found")
	}
	e := c.ev(id, Resolved, by)
	e.Reason = note
	return []Event{e}, nil
}
