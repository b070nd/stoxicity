package portfolio

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Errors a caller can test for.
var (
	ErrOversell       = errors.New("portfolio: not enough shares")
	ErrInsufficient   = errors.New("portfolio: not enough cash")
	ErrUnbalanced     = errors.New("portfolio: transaction does not balance")
	ErrImportConflict = errors.New("portfolio: import ID already used with different content")
	ErrArchived       = errors.New("portfolio: the book is archived")
)

// TxID is the identifier of the seq-th transaction of a book. It depends on
// nothing but the book and the position, so a replay gives the same IDs.
func TxID(book string, seq int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", book, seq)))
	return "tx_" + hex.EncodeToString(sum[:8])
}

// apply validates tx against the state and, if it holds, adds it. On error the
// state may be half changed: callers apply to a clone.
func (p *Projection) apply(book Book, tx *Tx) error {
	if tx.Book != book.ID {
		return fmt.Errorf("portfolio: transaction is for book %s, not %s", tx.Book, book.ID)
	}
	if book.Status == Archived {
		return ErrArchived
	}
	if !knownKind(tx.Kind) {
		return fmt.Errorf("portfolio: unknown transaction kind %q", tx.Kind)
	}
	if tx.Event.IsZero() {
		return errors.New("portfolio: a transaction needs an event time")
	}
	if strings.TrimSpace(tx.Source) == "" {
		return errors.New("portfolio: a transaction needs a source")
	}
	if len(tx.ImportID) > 200 || len(tx.Memo) > 500 {
		return errors.New("portfolio: import ID or memo too long")
	}
	if _, dup := p.txs[tx.ID]; dup {
		return fmt.Errorf("portfolio: transaction %s is already recorded", tx.ID)
	}

	switch tx.Kind {
	case Reserve:
		return p.applyReserve(tx)
	case Release:
		return p.applyRelease(tx)
	}
	if len(tx.Entries) == 0 {
		return fmt.Errorf("portfolio: a %s transaction needs entries", tx.Kind)
	}
	if err := checkEntries(tx); err != nil {
		return err
	}
	switch tx.Kind {
	case Reversal:
		if err := p.checkReversal(tx); err != nil {
			return err
		}
	case Settle:
		if err := p.checkSettle(tx); err != nil {
			return err
		}
	case Deposit, Withdrawal:
		if err := checkCashDirection(tx); err != nil {
			return err
		}
	case Income, DRIP:
		if err := p.checkPayout(tx); err != nil {
			return err
		}
	}

	touchedSec := map[string]bool{}
	cashBefore := map[money.Currency]int64{}
	availBefore := map[money.Currency]int64{}
	for _, c := range p.Currencies() {
		cashBefore[c], availBefore[c] = p.Cash(c), p.Cash(c)+p.Unsettled(c)
	}
	for _, e := range tx.Entries {
		cashBefore[e.Amount.Currency()] = p.Cash(e.Amount.Currency())
		availBefore[e.Amount.Currency()] = p.Cash(e.Amount.Currency()) + p.Unsettled(e.Amount.Currency())
	}

	seenLot := map[string]bool{}
	for i := range tx.Entries {
		e := &tx.Entries[i]
		if strings.HasPrefix(e.Account, "securities:") {
			if err := p.applyLot(tx, i, e, seenLot); err != nil {
				return err
			}
			touchedSec[e.Security] = true
		}
		cur := e.Amount.Currency()
		if p.bal[e.Account] == nil {
			p.bal[e.Account] = map[money.Currency]int64{}
		}
		n, err := addChecked(p.bal[e.Account][cur], e.Amount.Minor())
		if err != nil {
			return err
		}
		p.bal[e.Account][cur] = n
	}
	if tx.Kind == Split {
		if err := p.checkSplitComplete(tx); err != nil {
			return err
		}
	}

	if tx.Releases != "" {
		if _, ok := p.res[tx.Releases]; !ok {
			return fmt.Errorf("portfolio: reservation %q is not active", tx.Releases)
		}
	}
	for cur := range cashBefore {
		avail := p.Cash(cur) + p.Unsettled(cur)
		if !book.AllowNegativeCash && p.Cash(cur) < 0 && p.Cash(cur) < cashBefore[cur] {
			return fmt.Errorf("%w: settled %s cash would be %s", ErrInsufficient, cur, fmtMinor(p.Cash(cur), cur))
		}
		if held := p.ReservedCash(cur, tx.Releases); held > 0 && avail < availBefore[cur] && avail < held {
			return fmt.Errorf("%w: %s %s available after open orders hold %s", ErrInsufficient, cur, fmtMinor(avail, cur), fmtMinor(held, cur))
		}
		if !book.AllowNegativeCash && avail < 0 && avail < availBefore[cur] {
			return fmt.Errorf("%w: %s cash including unsettled trades would be %s", ErrInsufficient, cur, fmtMinor(avail, cur))
		}
	}
	for sec := range touchedSec {
		held := p.Held(sec)
		if held.Sign() < 0 {
			return fmt.Errorf("%w: %s", ErrOversell, sec)
		}
		if r := p.ReservedShares(sec, tx.Releases); r.Sign() > 0 && held.Cmp(r) < 0 {
			return fmt.Errorf("%w: %s held %s but open orders hold %s", ErrOversell, sec, held, r)
		}
	}
	if tx.Releases != "" {
		delete(p.res, tx.Releases)
	}

	p.record(tx)
	return nil
}

func (p *Projection) record(tx *Tx) {
	cp := *tx
	p.txs[tx.ID] = &cp
	p.order = append(p.order, tx.ID)
	switch tx.Kind {
	case Reversal:
		p.reversed[tx.Reverses] = tx.ID
		if o, ok := p.txs[tx.Reverses]; ok {
			p.undoPayout(o)
		}
	case Settle:
		p.settledBy[tx.Meta["settle_of"]] = tx.ID
	case Income, DRIP:
		p.notePayout(tx)
	}
}

func knownKind(k Kind) bool {
	for _, x := range allKinds {
		if x == k {
			return true
		}
	}
	return false
}

func fmtMinor(v int64, cur money.Currency) string {
	a, err := money.FromMinor(v, cur)
	if err != nil {
		return fmt.Sprint(v)
	}
	return a.String()
}

// checkEntries is the shape every transaction must have: known accounts allowed
// to its kind, securities fields only where they belong, quantities within the
// precision, and a sum of zero in every currency.
func checkEntries(tx *Tx) error {
	sums := map[money.Currency]*big.Int{}
	for i, e := range tx.Entries {
		at := fmt.Sprintf("entry %d (%s)", i+1, e.Account)
		if !ValidAccount(e.Account) {
			return fmt.Errorf("portfolio: %s: unknown account", at)
		}
		if !allowedAccount(tx.Kind, e.Account) {
			return fmt.Errorf("portfolio: %s: a %s transaction may not post to this account", at, tx.Kind)
		}
		if !e.Amount.Valid() {
			return fmt.Errorf("portfolio: %s: no currency", at)
		}
		sec := strings.HasPrefix(e.Account, "securities:")
		switch {
		case sec:
			if e.Security == "" || e.Account != SecurityAccount(e.Security) {
				return fmt.Errorf("portfolio: %s: the security does not match the account", at)
			}
			if e.Qty.Scale() > MaxQtyScale {
				return fmt.Errorf("portfolio: %s: quantity %s has more than %d digits", at, e.Qty, MaxQtyScale)
			}
			if e.NewLot && e.Qty.Sign() <= 0 {
				return fmt.Errorf("portfolio: %s: a new lot needs a positive quantity", at)
			}
			if !e.NewLot && e.Lot == "" {
				return fmt.Errorf("portfolio: %s: name the lot it changes", at)
			}
			if e.NewLot && e.Acquired != "" && !validDay(e.Acquired) {
				return fmt.Errorf("portfolio: %s: acquired %q is not YYYY-MM-DD", at, e.Acquired)
			}
		default:
			if e.Security != "" || !e.Qty.IsZero() || e.Lot != "" || e.NewLot {
				return fmt.Errorf("portfolio: %s: only securities entries carry shares or lots", at)
			}
		}
		s := sums[e.Amount.Currency()]
		if s == nil {
			s = new(big.Int)
			sums[e.Amount.Currency()] = s
		}
		s.Add(s, big.NewInt(e.Amount.Minor()))
	}
	var bad []string
	for c, s := range sums {
		if s.Sign() != 0 {
			bad = append(bad, fmt.Sprintf("%s off by %s minor units", c, s))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("%w: %s", ErrUnbalanced, strings.Join(bad, "; "))
	}
	return nil
}

func checkCashDirection(tx *Tx) error {
	var net int64
	for _, e := range tx.Entries {
		if e.Account == "cash" {
			net += e.Amount.Minor()
		}
	}
	if tx.Kind == Deposit && net <= 0 || tx.Kind == Withdrawal && net >= 0 {
		return fmt.Errorf("portfolio: a %s must move cash the right way", tx.Kind)
	}
	return nil
}

func validDay(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, err := parseDay(s)
	return err == nil
}

// applyLot validates and applies the lot side of one securities entry.
func (p *Projection) applyLot(tx *Tx, idx int, e *Entry, seen map[string]bool) error {
	cur := e.Amount.Currency()
	if e.NewLot {
		switch tx.Kind {
		case Buy, DRIP, TransferIn:
		default:
			return fmt.Errorf("portfolio: a %s transaction cannot open a lot", tx.Kind)
		}
		if e.Amount.Minor() < 0 {
			return errors.New("portfolio: a new lot cannot have negative cost")
		}
		id := fmt.Sprintf("%s#%d", tx.ID, idx+1)
		if e.Lot != "" && e.Lot != id {
			return fmt.Errorf("portfolio: lot %q does not belong to this transaction", e.Lot)
		}
		e.Lot = id
		if _, dup := p.lots[id]; dup {
			return fmt.Errorf("portfolio: lot %s exists", id)
		}
		acq := e.Acquired
		if acq == "" {
			acq = tx.Event.UTC().Format("2006-01-02")
			e.Acquired = acq
		}
		p.lots[id] = &Lot{ID: id, Security: e.Security, Currency: cur, Qty: e.Qty, Cost: e.Amount.Minor(), Acquired: acq, Opened: tx.Event, OpenSeq: tx.Seq, OpenTx: tx.ID}
		return nil
	}
	l, ok := p.lots[e.Lot]
	if !ok {
		return fmt.Errorf("portfolio: lot %q does not exist in this book", e.Lot)
	}
	if seen[e.Lot] {
		return fmt.Errorf("portfolio: lot %s appears twice in one transaction", e.Lot)
	}
	seen[e.Lot] = true
	if l.Security != e.Security || l.Currency != cur {
		return fmt.Errorf("portfolio: lot %s is %s in %s, not %s in %s", l.ID, l.Security, l.Currency, e.Security, cur)
	}
	if tx.Event.Before(l.Opened) {
		return fmt.Errorf("portfolio: lot %s was opened on %s, after this transaction's event time", l.ID, l.Opened.UTC().Format("2006-01-02"))
	}
	switch tx.Kind {
	case Sell, TransferOut:
		if e.Qty.Sign() >= 0 {
			return fmt.Errorf("portfolio: lot %s: a %s consumes shares", l.ID, tx.Kind)
		}
		take, _ := e.Qty.Neg()
		if take.Cmp(l.Qty) > 0 {
			return fmt.Errorf("%w: lot %s has %s, %s asked", ErrOversell, l.ID, l.Qty, take)
		}
		want, err := partialCost(l, take)
		if err != nil {
			return err
		}
		if e.Amount.Minor() != -want {
			return fmt.Errorf("portfolio: lot %s: cost removed %s but the lot's proportional cost is %s", l.ID, fmtMinor(-e.Amount.Minor(), cur), fmtMinor(want, cur))
		}
	case Split:
		if e.Amount.Minor() != 0 {
			return errors.New("portfolio: a split carries no cost")
		}
	case Reversal:
	default:
		return fmt.Errorf("portfolio: a %s transaction cannot change lot %s", tx.Kind, l.ID)
	}
	nq, err := l.Qty.Add(e.Qty)
	if err != nil {
		return err
	}
	nc, err := addChecked(l.Cost, e.Amount.Minor())
	if err != nil {
		return err
	}
	if nq.Sign() < 0 {
		return fmt.Errorf("%w: lot %s would hold %s", ErrOversell, l.ID, nq)
	}
	if nc < 0 || (nq.Sign() == 0 && nc != 0) {
		return fmt.Errorf("portfolio: lot %s would be left with cost %s for %s shares", l.ID, fmtMinor(nc, cur), nq)
	}
	l.Qty, l.Cost = nq, nc
	return nil
}

// checkSplitComplete requires a split to touch every open lot of the security it
// names, so no lot is left on the old share basis.
func (p *Projection) checkSplitComplete(tx *Tx) error {
	sec := tx.Entries[0].Security
	in := map[string]bool{}
	for _, e := range tx.Entries {
		if e.Security != sec {
			return errors.New("portfolio: a split is for one security")
		}
		in[e.Lot] = true
	}
	for _, l := range p.lots {
		if l.Security == sec && l.Qty.Sign() > 0 && !in[l.ID] && l.Opened.Before(tx.Event) {
			return fmt.Errorf("portfolio: the split leaves lot %s on the old basis", l.ID)
		}
	}
	return nil
}

func (p *Projection) applyReserve(tx *Tx) error {
	r := tx.Reservation
	if r == nil || len(tx.Entries) != 0 {
		return errors.New("portfolio: a reserve transaction carries a reservation and no entries")
	}
	if r.ID == "" || p.resSeen[r.ID] {
		return fmt.Errorf("portfolio: reservation ID %q is empty or already used", r.ID)
	}
	switch r.Kind {
	case ReserveCash:
		if !r.Currency.Valid() || r.Amount <= 0 {
			return errors.New("portfolio: a cash reservation needs a currency and a positive amount")
		}
		if avail := p.Available(r.Currency); avail < r.Amount {
			return fmt.Errorf("%w: %s %s available, %s asked to hold", ErrInsufficient, r.Currency, fmtMinor(avail, r.Currency), fmtMinor(r.Amount, r.Currency))
		}
	case ReserveShares:
		if r.Security == "" || r.Qty.Sign() <= 0 || r.Qty.Scale() > MaxQtyScale {
			return errors.New("portfolio: a share reservation needs a security and a positive quantity")
		}
		free, err := p.Held(r.Security).Sub(p.ReservedShares(r.Security, ""))
		if err != nil {
			return err
		}
		if free.Cmp(r.Qty) < 0 {
			return fmt.Errorf("%w: %s free %s, %s asked to hold", ErrOversell, r.Security, free, r.Qty)
		}
	default:
		return fmt.Errorf("portfolio: unknown reservation kind %q", r.Kind)
	}
	p.res[r.ID] = *r
	p.resSeen[r.ID] = true
	p.record(tx)
	return nil
}

func (p *Projection) applyRelease(tx *Tx) error {
	if len(tx.Entries) != 0 || tx.Releases == "" {
		return errors.New("portfolio: a release names a reservation and carries no entries")
	}
	if _, ok := p.res[tx.Releases]; !ok {
		return fmt.Errorf("portfolio: reservation %q is not active", tx.Releases)
	}
	delete(p.res, tx.Releases)
	p.record(tx)
	return nil
}

// checkReversal requires the entries to be the exact opposite of the transaction
// reversed, which must exist, be unreversed, and not be settled.
func (p *Projection) checkReversal(tx *Tx) error {
	o, ok := p.txs[tx.Reverses]
	if !ok {
		return fmt.Errorf("portfolio: transaction %q to reverse is not in this book", tx.Reverses)
	}
	switch o.Kind {
	case Reversal, Reserve, Release:
		return fmt.Errorf("portfolio: a %s cannot be reversed; post a new transaction", o.Kind)
	}
	if r, done := p.reversed[o.ID]; done {
		return fmt.Errorf("portfolio: %s was already reversed by %s", o.ID, r)
	}
	if s, done := p.settledBy[o.ID]; done {
		if _, undone := p.reversed[s]; !undone {
			return fmt.Errorf("portfolio: %s was settled by %s: reverse the settlement first", o.ID, s)
		}
	}
	if tx.Event.Before(o.Event) {
		return errors.New("portfolio: a reversal cannot be dated before what it reverses")
	}
	if err := p.checkPayoutReversal(o); err != nil {
		return err
	}
	// A lot changed by a later live transaction (a sale, a split) would be left
	// in a state the reversal cannot restore; that transaction is reversed first.
	for _, e := range o.Entries {
		if e.Lot == "" {
			continue
		}
		for _, id := range p.order {
			t := p.txs[id]
			if t.Seq <= o.Seq || t.Kind == Reversal {
				continue
			}
			if _, gone := p.reversed[t.ID]; gone {
				continue
			}
			for _, te := range t.Entries {
				if te.Lot == e.Lot {
					return fmt.Errorf("portfolio: lot %s was changed again by %s: reverse that first", e.Lot, t.ID)
				}
			}
		}
	}
	if len(tx.Entries) != len(o.Entries) {
		return errors.New("portfolio: a reversal must negate every entry of the original")
	}
	for i, e := range tx.Entries {
		oe := o.Entries[i]
		neg, err := oe.Amount.Neg()
		nq, _ := oe.Qty.Neg()
		if err != nil || e.Account != oe.Account || e.Amount != neg || e.Security != oe.Security || e.Qty.Cmp(nq) != 0 || e.Lot != oe.Lot || e.NewLot {
			return fmt.Errorf("portfolio: reversal entry %d is not the opposite of the original", i+1)
		}
	}
	return nil
}

// checkSettle requires the entries to clear exactly the unsettled postings of the
// trade named in Meta["settle_of"], once.
func (p *Projection) checkSettle(tx *Tx) error {
	id := tx.Meta["settle_of"]
	t, ok := p.txs[id]
	if !ok {
		return fmt.Errorf("portfolio: settle_of %q is not a transaction of this book", id)
	}
	if s, done := p.settledBy[id]; done {
		if _, undone := p.reversed[s]; !undone {
			return fmt.Errorf("portfolio: %s is already settled by %s", id, s)
		}
	}
	if _, rev := p.reversed[id]; rev {
		return fmt.Errorf("portfolio: %s was reversed", id)
	}
	if tx.Event.Before(t.Event) {
		return errors.New("portfolio: a settlement cannot be dated before the trade")
	}
	due := map[money.Currency]int64{}
	for _, e := range t.Entries {
		if e.Account == "unsettled" {
			due[e.Amount.Currency()] += e.Amount.Minor()
		}
	}
	if len(due) == 0 {
		return fmt.Errorf("portfolio: %s has nothing unsettled", id)
	}
	got := map[string]map[money.Currency]int64{"cash": {}, "unsettled": {}}
	for _, e := range tx.Entries {
		got[e.Account][e.Amount.Currency()] += e.Amount.Minor()
	}
	for cur, d := range due {
		if d == 0 {
			continue
		}
		if got["unsettled"][cur] != -d || got["cash"][cur] != d {
			return fmt.Errorf("portfolio: the settlement of %s must move %s from unsettled to cash", id, fmtMinor(d, cur))
		}
	}
	for cur := range got["cash"] {
		if _, ok := due[cur]; !ok {
			return fmt.Errorf("portfolio: %s has nothing unsettled in %s", id, cur)
		}
	}
	return nil
}
