package portfolio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// MaxQtyScale is the most digits after the point a share quantity may have.
const MaxQtyScale = 8

// Lot is one acquisition of one security, with what is left of it.
type Lot struct {
	ID       string
	Security string
	Currency money.Currency
	// Qty is the shares left; Cost the cost basis left, in minor units.
	Qty      money.Decimal
	Cost     int64
	Acquired string
	Opened   time.Time // event time of the transaction that opened it
	OpenSeq  int64
	OpenTx   string
}

// Projection is the state the journal adds up to. It is rebuilt by replay and
// never stored as truth.
type Projection struct {
	bal       map[string]map[money.Currency]int64
	lots      map[string]*Lot
	res       map[string]Reservation
	resSeen   map[string]bool
	reversed  map[string]string
	settledBy map[string]string
	txs       map[string]*Tx
	order     []string
	payouts   map[string]*payoutState
}

func newProjection() *Projection {
	return &Projection{bal: map[string]map[money.Currency]int64{}, lots: map[string]*Lot{}, res: map[string]Reservation{},
		resSeen: map[string]bool{}, reversed: map[string]string{}, settledBy: map[string]string{}, txs: map[string]*Tx{}}
}

func (p *Projection) clone() *Projection {
	n := newProjection()
	for a, m := range p.bal {
		n.bal[a] = map[money.Currency]int64{}
		for c, v := range m {
			n.bal[a][c] = v
		}
	}
	for k, l := range p.lots {
		c := *l
		n.lots[k] = &c
	}
	for k, v := range p.res {
		n.res[k] = v
	}
	for k := range p.resSeen {
		n.resSeen[k] = true
	}
	for k, v := range p.reversed {
		n.reversed[k] = v
	}
	for k, v := range p.settledBy {
		n.settledBy[k] = v
	}
	for k, v := range p.txs {
		n.txs[k] = v
	}
	n.order = append([]string(nil), p.order...)
	for k, v := range p.payouts {
		c := *v
		if n.payouts == nil {
			n.payouts = map[string]*payoutState{}
		}
		n.payouts[k] = &c
	}
	return n
}

// Balance is the signed balance of an account in a currency.
func (p *Projection) Balance(account string, cur money.Currency) int64 { return p.bal[account][cur] }

// Cash is the settled cash in a currency, in minor units.
func (p *Projection) Cash(cur money.Currency) int64 { return p.Balance("cash", cur) }

// Unsettled is the net of trades not yet settled: negative is owed, positive is due.
func (p *Projection) Unsettled(cur money.Currency) int64 { return p.Balance("unsettled", cur) }

// Currencies lists every currency with any balance, sorted.
func (p *Projection) Currencies() []money.Currency {
	seen := map[money.Currency]bool{}
	for _, m := range p.bal {
		for c := range m {
			seen[c] = true
		}
	}
	out := make([]money.Currency, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Accounts lists the accounts with a balance, sorted.
func (p *Projection) Accounts() []string {
	out := make([]string, 0, len(p.bal))
	for a := range p.bal {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// TrialBalance is the sum of every account in a currency. It is zero in every
// currency, always; a non-zero value means the ledger is corrupt.
func (p *Projection) TrialBalance(cur money.Currency) int64 {
	var s int64
	for _, m := range p.bal {
		s += m[cur]
	}
	return s
}

// OpenLots returns the lots of a security with shares left, in the order a sale
// by the method would consume them.
func (p *Projection) OpenLots(security string, m CostMethod) []Lot {
	var out []Lot
	for _, l := range p.lots {
		if l.Security == security && l.Qty.Sign() > 0 {
			out = append(out, *l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Acquired != out[j].Acquired {
			return out[i].Acquired < out[j].Acquired
		}
		return out[i].OpenSeq < out[j].OpenSeq
	})
	if m == LIFO {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

// Lot returns a lot by ID.
func (p *Projection) Lot(id string) (Lot, bool) {
	l, ok := p.lots[id]
	if !ok {
		return Lot{}, false
	}
	return *l, true
}

// Position is the shares held of a security and their cost, per currency.
type Position struct {
	Security string
	Qty      money.Decimal
	Cost     map[money.Currency]int64
	Lots     int
}

// Positions lists the securities with shares held, sorted by ID.
func (p *Projection) Positions() []Position {
	by := map[string]*Position{}
	for _, l := range p.lots {
		if l.Qty.Sign() <= 0 {
			continue
		}
		pos := by[l.Security]
		if pos == nil {
			pos = &Position{Security: l.Security, Cost: map[money.Currency]int64{}}
			by[l.Security] = pos
		}
		q, err := pos.Qty.Add(l.Qty)
		if err == nil {
			pos.Qty = q
		}
		pos.Cost[l.Currency] += l.Cost
		pos.Lots++
	}
	out := make([]Position, 0, len(by))
	for _, v := range by {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Security < out[j].Security })
	return out
}

// Held is the shares held of a security.
func (p *Projection) Held(security string) money.Decimal {
	var q money.Decimal
	for _, l := range p.lots {
		if l.Security == security {
			if s, err := q.Add(l.Qty); err == nil {
				q = s
			}
		}
	}
	return q
}

// Reservations lists the active reservations, sorted by ID.
func (p *Projection) Reservations() []Reservation {
	out := make([]Reservation, 0, len(p.res))
	for _, r := range p.res {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ReservedCash is the cash held back for open orders in a currency, in minor
// units, leaving out the reservation named except.
func (p *Projection) ReservedCash(cur money.Currency, except string) int64 {
	var s int64
	for id, r := range p.res {
		if r.Kind == ReserveCash && r.Currency == cur && id != except {
			s += r.Amount
		}
	}
	return s
}

// ReservedShares is the shares held back for open sell orders.
func (p *Projection) ReservedShares(security, except string) money.Decimal {
	var q money.Decimal
	for id, r := range p.res {
		if r.Kind == ReserveShares && r.Security == security && id != except {
			if s, err := q.Add(r.Qty); err == nil {
				q = s
			}
		}
	}
	return q
}

// Available is what can still be spent in a currency: settled cash plus the net
// of unsettled trades, less what open orders hold.
func (p *Projection) Available(cur money.Currency) int64 {
	return p.Cash(cur) + p.Unsettled(cur) - p.ReservedCash(cur, "")
}

// Reversed reports the transaction that reversed id, if any.
func (p *Projection) Reversed(id string) (string, bool) { r, ok := p.reversed[id]; return r, ok }

// Tx returns a recorded transaction.
func (p *Projection) Tx(id string) (Tx, bool) {
	t, ok := p.txs[id]
	if !ok {
		return Tx{}, false
	}
	return *t, true
}

// RealizedGain is the realised gain in a currency, positive for a gain. The
// ledger stores it as a credit, so the sign is flipped.
func (p *Projection) RealizedGain(cur money.Currency) int64 { return -p.Balance("gain:realized", cur) }

// Fingerprint identifies the state: two projections with the same fingerprint
// are the same balances, lots and reservations. It is how a rebuild is shown to
// match the live state.
func (p *Projection) Fingerprint() string {
	var b strings.Builder
	for _, a := range p.Accounts() {
		var cs []string
		for c, v := range p.bal[a] {
			if v != 0 {
				cs = append(cs, fmt.Sprintf("%s=%d", c, v))
			}
		}
		sort.Strings(cs)
		if len(cs) > 0 {
			fmt.Fprintf(&b, "bal %s %s\n", a, strings.Join(cs, ","))
		}
	}
	ids := make([]string, 0, len(p.lots))
	for id := range p.lots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		l := p.lots[id]
		fmt.Fprintf(&b, "lot %s %s %s %s %d %s\n", id, l.Security, l.Currency, l.Qty, l.Cost, l.Acquired)
	}
	for _, r := range p.Reservations() {
		fmt.Fprintf(&b, "res %s %s %s %d %s %s\n", r.ID, r.Kind, r.Currency, r.Amount, r.Security, r.Qty)
	}
	for _, po := range p.Payouts() {
		fmt.Fprintf(&b, "payout %s %s %d %s %s %v\n", po.Key, po.Currency, po.Gross, po.AccrualTx, po.PaidTx, po.Direct)
	}
	rev := make([]string, 0, len(p.reversed))
	for k, v := range p.reversed {
		rev = append(rev, k+">"+v)
	}
	sort.Strings(rev)
	fmt.Fprintf(&b, "rev %s\n", strings.Join(rev, ","))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// addChecked adds with overflow detection.
func addChecked(a, b int64) (int64, error) {
	s := new(big.Int).Add(big.NewInt(a), big.NewInt(b))
	if !s.IsInt64() {
		return 0, fmt.Errorf("portfolio: amount overflow adding %d and %d", a, b)
	}
	return s.Int64(), nil
}

// partialCost is the cost carried by consuming qty of a lot: all of it when the
// lot is used up, otherwise the proportional share rounded half-even to a minor
// unit.
func partialCost(l *Lot, consume money.Decimal) (int64, error) {
	if consume.Cmp(l.Qty) == 0 {
		return l.Cost, nil
	}
	num, err := money.Int(l.Cost).Mul(consume, uint8(consume.Scale()), money.Exact)
	if err != nil {
		return 0, err
	}
	q, err := num.Div(l.Qty, 0, money.HalfEven)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(q.String(), 10, 64)
}

func parseDay(s string) (time.Time, error) { return time.Parse(time.DateOnly, s) }
