package portfolio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Ledger is one book's journal and the projection it adds up to.
type Ledger struct {
	Book    Book
	p       *Projection
	txs     []Tx
	imports map[string]string // import ID -> transaction ID
	hashes  map[string]string // transaction ID -> content hash
}

// NewLedger starts an empty ledger for a book.
func NewLedger(b Book) (*Ledger, error) {
	if b.ID == "" {
		return nil, errors.New("portfolio: a book needs an ID")
	}
	if b.Status == "" {
		b.Status = Active
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return &Ledger{Book: b, p: newProjection(), imports: map[string]string{}, hashes: map[string]string{}}, nil
}

// Replay rebuilds a ledger from its recorded transactions, in recorded order.
// hashes are the stored content hashes by transaction ID (nil if not known; then
// an import ID cannot be compared by content until reposted). Any transaction
// that no longer applies is an error: a journal that does not replay is corrupt.
func Replay(b Book, txs []Tx, hashes map[string]string) (*Ledger, error) {
	l, err := NewLedger(b)
	if err != nil {
		return nil, err
	}
	archived := l.Book.Status == Archived
	l.Book.Status = Active // an archived book still replays; it just takes no more
	for i := range txs {
		tx := cloneTx(txs[i])
		if tx.Seq != int64(i+1) {
			return nil, fmt.Errorf("portfolio: book %s: transaction %d has sequence %d", b.ID, i+1, tx.Seq)
		}
		if tx.ID != TxID(b.ID, tx.Seq) {
			return nil, fmt.Errorf("portfolio: book %s: transaction %s has the wrong ID for position %d", b.ID, tx.ID, tx.Seq)
		}
		if err := l.p.apply(l.Book, &tx); err != nil {
			return nil, fmt.Errorf("portfolio: book %s: transaction %s (seq %d) does not replay: %w", b.ID, tx.ID, tx.Seq, err)
		}
		l.txs = append(l.txs, tx)
		if tx.ImportID != "" {
			l.imports[tx.ImportID] = tx.ID
		}
		if h, ok := hashes[tx.ID]; ok {
			l.hashes[tx.ID] = h
		}
	}
	if archived {
		l.Book.Status = Archived
	}
	return l, nil
}

// ReplayAsOf rebuilds the ledger from the transactions with an event time up to
// the cutoff, keeping their recorded order and IDs. It fails if the transactions
// it keeps depend on ones it leaves out, which the event-time rules of apply make
// impossible for a journal posted through Post.
func ReplayAsOf(b Book, txs []Tx, cutoff time.Time) (*Ledger, error) {
	l, err := NewLedger(b)
	if err != nil {
		return nil, err
	}
	l.Book.Status = Active
	for _, t := range txs {
		if t.Event.After(cutoff) {
			continue
		}
		tx := cloneTx(t)
		if err := l.p.apply(l.Book, &tx); err != nil {
			return nil, fmt.Errorf("portfolio: the book as of %s is not consistent (%s): %w", cutoff.UTC().Format(time.RFC3339), tx.ID, err)
		}
		l.txs = append(l.txs, tx)
	}
	l.Book.Status = b.Status
	return l, nil
}

func cloneTx(t Tx) Tx {
	c := t
	c.Entries = append([]Entry(nil), t.Entries...)
	if t.Meta != nil {
		c.Meta = map[string]string{}
		for k, v := range t.Meta {
			c.Meta[k] = v
		}
	}
	if t.Reservation != nil {
		r := *t.Reservation
		c.Reservation = &r
	}
	return c
}

// Projection is a copy of the current state.
func (l *Ledger) Projection() *Projection { return l.p.clone() }

// Txs returns the recorded transactions in order.
func (l *Ledger) Txs() []Tx {
	out := make([]Tx, len(l.txs))
	for i, t := range l.txs {
		out[i] = cloneTx(t)
	}
	return out
}

// Hash returns the stored content hash of a transaction.
func (l *Ledger) Hash(id string) string { return l.hashes[id] }

// Posted is the outcome of Post.
type Posted struct {
	Tx Tx
	// Existing says the import ID was already recorded with this very content, so
	// nothing was written.
	Existing bool
	// Hash is the content hash to store with the transaction.
	Hash string
}

// ContentHash identifies what a record states, so the same record reposted can
// be recognised and a changed one refused. It leaves out what the ledger derives
// from its own state at the time (which lots a sale used, the cost they carried,
// the realised gain, the lot IDs): a fill reposted after other trades must hash
// the same as when it was first recorded.
func ContentHash(t Tx) string {
	type ent struct {
		Account, Currency string
		Minor             int64
	}
	hasSec := false
	for _, e := range t.Entries {
		if strings.HasPrefix(e.Account, "securities:") {
			hasSec = true
		}
	}
	var ents []ent
	qty := map[string]money.Decimal{}
	acquired := ""
	for _, e := range t.Entries {
		switch {
		case t.Kind == Reversal:
		case strings.HasPrefix(e.Account, "securities:"):
			q, err := qty[e.Account].Add(e.Qty)
			if err == nil {
				qty[e.Account] = q
			}
			if e.NewLot && (t.Kind == Buy || t.Kind == DRIP) && acquired == "" {
				acquired = e.Acquired
			}
		case e.Account == "gain:realized", e.Account == "equity:transfers" && hasSec:
		default:
			ents = append(ents, ent{e.Account, string(e.Amount.Currency()), e.Amount.Minor()})
		}
	}
	accts := make([]string, 0, len(qty))
	for a := range qty {
		accts = append(accts, a)
	}
	sort.Strings(accts)
	shares := make([]string, len(accts))
	for i, a := range accts {
		shares[i] = a + "=" + qty[a].String()
	}
	doc := struct {
		Kind                                       Kind
		Event                                      int64
		Source, ImportID, Memo, Reverses, Releases string
		Reservation                                *Reservation
		Entries                                    []ent
		Shares                                     []string
		Acquired                                   string
		Meta                                       map[string]string
	}{t.Kind, t.Event.UnixMilli(), t.Source, t.ImportID, t.Memo, t.Reverses, t.Releases, t.Reservation, ents, shares, acquired, t.Meta}
	b, _ := json.Marshal(doc)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Imported returns the transaction recorded for an import ID, so an importer can
// skip a record it already holds before building it again.
func (l *Ledger) Imported(importID string) (Tx, bool) {
	id, ok := l.imports[importID]
	if !ok {
		return Tx{}, false
	}
	t, ok := l.p.Tx(id)
	return t, ok
}

// Check says whether tx would be accepted, without recording it.
func (l *Ledger) Check(tx Tx) error {
	_, _, err := l.prepare(tx, time.Time{})
	return err
}

func (l *Ledger) prepare(tx Tx, recorded time.Time) (Tx, *Projection, error) {
	tx = cloneTx(tx)
	tx.Book = l.Book.ID
	tx.Event = tx.Event.UTC().Truncate(time.Millisecond)
	tx.Seq = int64(len(l.txs) + 1)
	tx.ID = TxID(l.Book.ID, tx.Seq)
	tx.Recorded = recorded.UTC().Truncate(time.Millisecond)
	next := l.p.clone()
	if err := next.apply(l.Book, &tx); err != nil {
		return Tx{}, nil, err
	}
	return tx, next, nil
}

// Post records a transaction after checking it against the whole state. A
// transaction with an ImportID already recorded is a no-op if its content is the
// same and an error if not.
func (l *Ledger) Post(tx Tx, recorded time.Time) (Posted, error) {
	h := ContentHash(tx)
	if tx.ImportID != "" {
		if id, ok := l.imports[tx.ImportID]; ok {
			prev := l.hashes[id]
			if prev == "" || prev != h {
				return Posted{}, fmt.Errorf("%w: %q is %s", ErrImportConflict, tx.ImportID, id)
			}
			old, _ := l.p.Tx(id)
			return Posted{Tx: old, Existing: true, Hash: h}, nil
		}
	}
	done, next, err := l.prepare(tx, recorded)
	if err != nil {
		return Posted{}, err
	}
	l.p = next
	l.txs = append(l.txs, done)
	l.hashes[done.ID] = h
	if done.ImportID != "" {
		l.imports[done.ImportID] = done.ID
	}
	return Posted{Tx: done, Hash: h}, nil
}

// Archive stops the book taking transactions. Only a paper book can be archived:
// a real book is never closed by this program.
func (l *Ledger) Archive() error {
	if !l.Book.Env.Paper() {
		return fmt.Errorf("portfolio: a %s book cannot be archived or reset", l.Book.Env)
	}
	l.Book.Status = Archived
	return nil
}
