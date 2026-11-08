package portfolio

import (
	"errors"
	"fmt"
	"sort"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Payout phases carried in Meta["phase"] next to Meta["payout"], the key of the
// payout the line is about. A payout claims income once: either an accrual
// (booked at the ex-date, paid later) or a direct receipt, never both.
const (
	PhaseAccrue = "accrue"
	PhasePay    = "pay"
	PhaseDirect = "direct"
	PhaseDrip   = "drip"
)

// PayoutInfo is what the ledger holds for one payout.
type PayoutInfo struct {
	Key       string
	Currency  money.Currency
	Gross     int64 // minor units booked as income
	AccrualTx string
	PaidTx    string
	Direct    bool // income was booked without a receivable
}

type payoutState struct {
	PayoutInfo
}

func (p *Projection) payoutOf(key string) *payoutState {
	if p.payouts == nil {
		p.payouts = map[string]*payoutState{}
	}
	return p.payouts[key]
}

// Payout returns the state of a payout key.
func (p *Projection) Payout(key string) (PayoutInfo, bool) {
	s, ok := p.payouts[key]
	if !ok {
		return PayoutInfo{}, false
	}
	return s.PayoutInfo, true
}

// Payouts lists every payout the ledger has booked, by key.
func (p *Projection) Payouts() []PayoutInfo {
	out := make([]PayoutInfo, 0, len(p.payouts))
	for _, s := range p.payouts {
		out = append(out, s.PayoutInfo)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// sumOf adds the entries of an account in a currency.
func sumOf(tx *Tx, account string) (int64, money.Currency, bool) {
	var s int64
	var cur money.Currency
	found := false
	for _, e := range tx.Entries {
		if e.Account == account {
			if found && cur != e.Amount.Currency() {
				return 0, "", false
			}
			cur, found = e.Amount.Currency(), true
			s += e.Amount.Minor()
		}
	}
	return s, cur, found
}

func hasAccount(tx *Tx, prefix string) bool {
	for _, e := range tx.Entries {
		if len(e.Account) >= len(prefix) && e.Account[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// checkPayout enforces that a keyed income line books income once and that a
// payment settles exactly the receivable that was accrued.
func (p *Projection) checkPayout(tx *Tx) error {
	key, phase := tx.Meta["payout"], tx.Meta["phase"]
	if key == "" {
		if phase != "" {
			return errors.New("portfolio: a payout phase needs the payout key")
		}
		return nil
	}
	st := p.payoutOf(key)
	inc, incCur, hasInc := sumOf(tx, "income:dividend")
	rec, recCur, hasRec := sumOf(tx, "receivable:dividend")
	switch phase {
	case PhaseAccrue:
		if tx.Kind != Income {
			return errors.New("portfolio: only an income line accrues a payout")
		}
		if st != nil {
			return fmt.Errorf("portfolio: payout %q already booked by %s: reverse it first", key, firstNonEmpty(st.AccrualTx, st.PaidTx))
		}
		if !hasInc || !hasRec || len(tx.Entries) != 2 || rec <= 0 || inc != -rec || incCur != recCur {
			return errors.New("portfolio: an accrual is a receivable and the income it books, and nothing else")
		}
	case PhasePay, PhaseDrip:
		if tx.Kind == Income && phase == PhaseDrip || tx.Kind == DRIP && phase == PhasePay {
			return fmt.Errorf("portfolio: phase %q does not fit a %s line", phase, tx.Kind)
		}
		if st != nil && st.PaidTx != "" {
			return fmt.Errorf("portfolio: payout %q was already paid by %s", key, st.PaidTx)
		}
		if st != nil && st.AccrualTx != "" {
			if hasInc {
				return fmt.Errorf("portfolio: payout %q is already booked as income by its accrual: this line would book it twice", key)
			}
			if !hasRec || rec != -st.Gross || recCur != st.Currency {
				return fmt.Errorf("portfolio: payout %q accrued %s: the payment must clear exactly that receivable", key, fmtMinor(st.Gross, st.Currency))
			}
			return nil
		}
		// no accrual: the line books the income itself, once
		if hasRec {
			return fmt.Errorf("portfolio: payout %q has no receivable to clear", key)
		}
		if !hasInc || inc >= 0 {
			return errors.New("portfolio: a payout with no accrual books its income in the same line")
		}
	case PhaseDirect:
		if tx.Kind != Income {
			return errors.New("portfolio: only an income line is a direct receipt")
		}
		if st != nil {
			return fmt.Errorf("portfolio: payout %q already booked: this line would book it twice", key)
		}
		if hasRec || !hasInc || inc >= 0 {
			return errors.New("portfolio: a direct receipt books income and cash, with no receivable")
		}
	default:
		return fmt.Errorf("portfolio: unknown payout phase %q (accrue, pay, direct, drip)", phase)
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// notePayout records what a keyed line did, after it was accepted.
func (p *Projection) notePayout(tx *Tx) {
	key, phase := tx.Meta["payout"], tx.Meta["phase"]
	if key == "" || (tx.Kind != Income && tx.Kind != DRIP) {
		return
	}
	st := p.payoutOf(key)
	switch phase {
	case PhaseAccrue:
		inc, cur, _ := sumOf(tx, "income:dividend")
		p.payouts[key] = &payoutState{PayoutInfo{Key: key, Currency: cur, Gross: -inc, AccrualTx: tx.ID}}
	case PhasePay, PhaseDrip:
		if st != nil {
			st.PaidTx = tx.ID
			return
		}
		inc, cur, _ := sumOf(tx, "income:dividend")
		p.payouts[key] = &payoutState{PayoutInfo{Key: key, Currency: cur, Gross: -inc, PaidTx: tx.ID, Direct: true}}
	case PhaseDirect:
		inc, cur, _ := sumOf(tx, "income:dividend")
		p.payouts[key] = &payoutState{PayoutInfo{Key: key, Currency: cur, Gross: -inc, PaidTx: tx.ID, Direct: true}}
	}
}

// checkPayoutReversal refuses to reverse an accrual that has been paid.
func (p *Projection) checkPayoutReversal(o *Tx) error {
	key := o.Meta["payout"]
	if key == "" {
		return nil
	}
	if st := p.payouts[key]; st != nil && o.ID == st.AccrualTx && st.PaidTx != "" {
		return fmt.Errorf("portfolio: payout %q was paid by %s: reverse the payment first", key, st.PaidTx)
	}
	return nil
}

// undoPayout clears the state a reversed keyed line had set.
func (p *Projection) undoPayout(o *Tx) {
	key := o.Meta["payout"]
	st := p.payouts[key]
	if key == "" || st == nil {
		return
	}
	switch {
	case o.ID == st.AccrualTx:
		delete(p.payouts, key)
	case o.ID == st.PaidTx && st.Direct:
		delete(p.payouts, key)
	case o.ID == st.PaidTx:
		st.PaidTx = ""
	}
}

// ---- builders ----

func payoutMeta(key, phase string) map[string]string {
	return map[string]string{"payout": key, "phase": phase}
}

// Accrue books the receivable and the income of a payout at its ex-date.
func (l *Ledger) Accrue(c Common, key string, gross money.Amount) (Tx, error) {
	if key == "" {
		return Tx{}, errors.New("portfolio: an accrual needs the payout key")
	}
	if err := positive(gross, "an accrual"); err != nil {
		return Tx{}, err
	}
	n, _ := gross.Neg()
	return c.tx(Income, payoutMeta(key, PhaseAccrue), Entry{Account: "receivable:dividend", Amount: gross}, Entry{Account: "income:dividend", Amount: n}), nil
}

// Receive books the payment of an accrued payout: the receivable is cleared, tax
// withheld at source is expensed and the rest arrives as cash. withheld may be
// the zero Amount.
func (l *Ledger) Receive(c Common, key string, withheld money.Amount) (Tx, error) {
	st, ok := l.p.Payout(key)
	if !ok || st.AccrualTx == "" {
		return Tx{}, fmt.Errorf("portfolio: payout %q has no accrual to receive", key)
	}
	if st.PaidTx != "" {
		return Tx{}, fmt.Errorf("portfolio: payout %q was already paid by %s", key, st.PaidTx)
	}
	w := int64(0)
	if withheld.Valid() {
		if withheld.Currency() != st.Currency || withheld.Sign() < 0 || withheld.Minor() >= st.Gross {
			return Tx{}, errors.New("portfolio: withholding must be in the payout's currency and less than the gross amount")
		}
		w = withheld.Minor()
	}
	es := plus(Entry{Account: "cash", Amount: amt(st.Gross-w, st.Currency)}, Entry{Account: "receivable:dividend", Amount: amt(-st.Gross, st.Currency)})
	if w > 0 {
		es = append(es, Entry{Account: "expense:withholding", Amount: amt(w, st.Currency)})
	}
	return c.tx(Income, payoutMeta(key, PhasePay), es...), nil
}

// ReceiveDirect books a payout that was never accrued: income and cash together.
func (l *Ledger) ReceiveDirect(c Common, key string, gross, withheld money.Amount) (Tx, error) {
	tx, err := l.Income(c, "dividend", gross, withheld)
	if err != nil {
		return Tx{}, err
	}
	tx.Meta = mergeMeta(tx.Meta, payoutMeta(key, PhaseDirect))
	return tx, nil
}

// ReinvestPayout books a payout that became shares. If the payout was accrued,
// the shares clear its receivable; otherwise the line books the income itself.
func (l *Ledger) ReinvestPayout(c Common, key, security string, qty money.Decimal, gross, withheld money.Amount) (Tx, error) {
	if st, ok := l.p.Payout(key); ok {
		if st.AccrualTx == "" || st.PaidTx != "" {
			return Tx{}, fmt.Errorf("portfolio: payout %q is already booked", key)
		}
		if qty.Sign() <= 0 || qty.Scale() > MaxQtyScale || security == "" {
			return Tx{}, errors.New("portfolio: a reinvestment needs a security and a positive quantity")
		}
		w := int64(0)
		if withheld.Valid() {
			if withheld.Currency() != st.Currency || withheld.Sign() < 0 || withheld.Minor() >= st.Gross {
				return Tx{}, errors.New("portfolio: withholding must be in the payout's currency and less than the gross amount")
			}
			w = withheld.Minor()
		}
		es := plus(
			Entry{Account: SecurityAccount(security), Security: security, Amount: amt(st.Gross-w, st.Currency), Qty: qty, NewLot: true, Acquired: c.Event.UTC().Format("2006-01-02")},
			Entry{Account: "receivable:dividend", Amount: amt(-st.Gross, st.Currency)},
		)
		if w > 0 {
			es = append(es, Entry{Account: "expense:withholding", Amount: amt(w, st.Currency)})
		}
		return c.tx(DRIP, payoutMeta(key, PhaseDrip), es...), nil
	}
	tx, err := l.Reinvest(c, security, qty, gross, withheld)
	if err != nil {
		return Tx{}, err
	}
	tx.Meta = mergeMeta(tx.Meta, payoutMeta(key, PhaseDrip))
	return tx, nil
}

func mergeMeta(a, b map[string]string) map[string]string {
	if a == nil {
		a = map[string]string{}
	}
	for k, v := range b {
		a[k] = v
	}
	return a
}
