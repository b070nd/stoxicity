package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

// Errors of the portfolio store.
var (
	ErrNoBook     = errors.New("store: portfolio: no such book")
	ErrBookExists = errors.New("store: portfolio: a book with this account and name exists")
	// ErrCorrupt means the stored journal no longer adds up: a chain hash, a
	// lot record or a replay disagrees with what was written.
	ErrCorrupt = errors.New("store: portfolio: the journal is corrupt")
)

// Portfolios is the books of one tenant. Each tenant has a database of its own,
// and every book also records its tenant, so a file copied from one tenant to
// another shows nothing of the first one's books.
type Portfolios struct{ t tenantStore }

// Portfolios returns the portfolio store of one tenant. An invalid tenant ID
// fails on every call and creates nothing.
func (db *DB) Portfolios(tenantID string) Portfolios {
	return Portfolios{tenantStore{db: db, id: tenantID, valid: model.ValidID(tenantID)}}
}

// BookID is the identifier of the book of an account with a name in a tenant. It
// depends on all three, so the same names in two tenants are two different books
// with two different transaction IDs.
func BookID(tenant, account, name string) string {
	sum := sha256.Sum256([]byte(tenant + "|" + account + "|" + name))
	return "bk_" + hex.EncodeToString(sum[:6])
}

type pfQuery interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// ImportBatch records one file or one batch of statement lines.
type ImportBatch struct {
	File     string
	SHA256   string
	Rows     int
	Posted   int
	Repeated int
	At       time.Time
}

// CreateBook adds an empty book. Its ID is assigned.
func (p Portfolios) CreateBook(ctx context.Context, b portfolio.Book) (portfolio.Book, error) {
	h, err := p.t.handle()
	if err != nil {
		return portfolio.Book{}, err
	}
	b.Status = portfolio.Active
	if err := b.Validate(); err != nil {
		return portfolio.Book{}, err
	}
	b.ID = BookID(p.t.id, b.Account, b.Name)
	if err := insertBook(ctx, h.w, p.t.id, b, ms(p.t.db.now())); err != nil {
		return portfolio.Book{}, err
	}
	return b, nil
}

type pfExec interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func insertBook(ctx context.Context, x pfExec, tenant string, b portfolio.Book, at int64) error {
	neg := 0
	if b.AllowNegativeCash {
		neg = 1
	}
	_, err := x.ExecContext(ctx, `INSERT INTO pf_books (id, tenant_id, account, name, env, base, method, allow_negative_cash, status, created_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`, b.ID, tenant, b.Account, b.Name, string(b.Env), string(b.Base), string(b.Method), neg, at)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %s/%s", ErrBookExists, b.Account, b.Name)
		}
		return fmt.Errorf("store: portfolio: %w", err)
	}
	return nil
}

const bookCols = `id, account, name, env, base, method, allow_negative_cash, status`

func scanBook(r scanner) (portfolio.Book, error) {
	var b portfolio.Book
	var env, base, method, status string
	var neg int
	if err := r.Scan(&b.ID, &b.Account, &b.Name, &env, &base, &method, &neg, &status); err != nil {
		return portfolio.Book{}, err
	}
	b.Env, b.Base, b.Method, b.Status = portfolio.Env(env), money.Currency(base), portfolio.CostMethod(method), portfolio.BookStatus(status)
	b.AllowNegativeCash = neg == 1
	return b, nil
}

// Books lists the tenant's books, by account and name.
func (p Portfolios) Books(ctx context.Context) ([]portfolio.Book, error) {
	h, err := p.t.handle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT `+bookCols+` FROM pf_books WHERE tenant_id = ? ORDER BY account, name`, p.t.id)
	if err != nil {
		return nil, fmt.Errorf("store: portfolio: %w", err)
	}
	defer rows.Close()
	var out []portfolio.Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, fmt.Errorf("store: portfolio: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// FindBook returns a book by its ID or by "account/name".
func (p Portfolios) FindBook(ctx context.Context, ref string) (portfolio.Book, error) {
	h, err := p.t.handle()
	if err != nil {
		return portfolio.Book{}, err
	}
	return findBook(ctx, h.r, p.t.id, ref)
}

func findBook(ctx context.Context, q pfQuery, tenant, ref string) (portfolio.Book, error) {
	var row *sql.Row
	if acct, name, ok := strings.Cut(ref, "/"); ok {
		row = q.QueryRowContext(ctx, `SELECT `+bookCols+` FROM pf_books WHERE tenant_id = ? AND account = ? AND name = ?`, tenant, acct, name)
	} else {
		row = q.QueryRowContext(ctx, `SELECT `+bookCols+` FROM pf_books WHERE tenant_id = ? AND id = ?`, tenant, ref)
	}
	b, err := scanBook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return portfolio.Book{}, fmt.Errorf("%w: %q", ErrNoBook, ref)
	}
	if err != nil {
		return portfolio.Book{}, fmt.Errorf("store: portfolio: %w", err)
	}
	return b, nil
}

type journal struct {
	txs    []portfolio.Tx
	hashes map[string]string
	chains []string
}

func loadJournal(ctx context.Context, q pfQuery, bookID string) (journal, error) {
	j := journal{hashes: map[string]string{}}
	rows, err := q.QueryContext(ctx, `SELECT seq, id, kind, event_ms, recorded_ms, source, import_id, memo, reverses, releases, reservation, meta, content_hash, chain
		FROM pf_tx WHERE book_id = ? ORDER BY seq`, bookID)
	if err != nil {
		return j, fmt.Errorf("store: portfolio: %w", err)
	}
	for rows.Next() {
		var t portfolio.Tx
		var kind, resv, meta, hash, chain string
		var ev, rec int64
		if err := rows.Scan(&t.Seq, &t.ID, &kind, &ev, &rec, &t.Source, &t.ImportID, &t.Memo, &t.Reverses, &t.Releases, &resv, &meta, &hash, &chain); err != nil {
			rows.Close()
			return j, fmt.Errorf("store: portfolio: %w", err)
		}
		t.Book, t.Kind, t.Event, t.Recorded = bookID, portfolio.Kind(kind), fromMS(ev), fromMS(rec)
		if resv != "" {
			t.Reservation = new(portfolio.Reservation)
			if err := json.Unmarshal([]byte(resv), t.Reservation); err != nil {
				rows.Close()
				return j, fmt.Errorf("%w: %s: reservation: %v", ErrCorrupt, t.ID, err)
			}
		}
		if meta != "" {
			if err := json.Unmarshal([]byte(meta), &t.Meta); err != nil {
				rows.Close()
				return j, fmt.Errorf("%w: %s: meta: %v", ErrCorrupt, t.ID, err)
			}
		}
		j.txs = append(j.txs, t)
		j.hashes[t.ID] = hash
		j.chains = append(j.chains, chain)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return j, fmt.Errorf("store: portfolio: %w", err)
	}
	rows.Close()

	er, err := q.QueryContext(ctx, `SELECT seq, account, currency, minor, security, qty, lot, new_lot, acquired FROM pf_entry WHERE book_id = ? ORDER BY seq, n`, bookID)
	if err != nil {
		return j, fmt.Errorf("store: portfolio: %w", err)
	}
	defer er.Close()
	for er.Next() {
		var seq, minor int64
		var e portfolio.Entry
		var cur, qty string
		var nl int
		if err := er.Scan(&seq, &e.Account, &cur, &minor, &e.Security, &qty, &e.Lot, &nl, &e.Acquired); err != nil {
			return j, fmt.Errorf("store: portfolio: %w", err)
		}
		if seq < 1 || int(seq) > len(j.txs) || j.txs[seq-1].Seq != seq {
			return j, fmt.Errorf("%w: an entry belongs to no transaction (seq %d)", ErrCorrupt, seq)
		}
		if e.Amount, err = money.FromMinor(minor, money.Currency(cur)); err != nil {
			return j, fmt.Errorf("%w: seq %d: %v", ErrCorrupt, seq, err)
		}
		if e.Qty, err = money.ParseDecimal(qty); err != nil {
			return j, fmt.Errorf("%w: seq %d: quantity %q: %v", ErrCorrupt, seq, qty, err)
		}
		e.NewLot = nl == 1
		j.txs[seq-1].Entries = append(j.txs[seq-1].Entries, e)
	}
	if err := er.Err(); err != nil {
		return j, fmt.Errorf("store: portfolio: %w", err)
	}
	return j, nil
}

// Ledger loads a book and replays its journal. A journal that does not replay
// is an error, not a partial answer.
func (p Portfolios) Ledger(ctx context.Context, ref string) (*portfolio.Ledger, error) {
	h, err := p.t.handle()
	if err != nil {
		return nil, err
	}
	tx, err := h.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("store: portfolio: %w", err)
	}
	defer tx.Rollback()
	l, _, err := loadLedger(ctx, tx, p.t.id, ref)
	return l, err
}

func loadLedger(ctx context.Context, q pfQuery, tenant, ref string) (*portfolio.Ledger, journal, error) {
	b, err := findBook(ctx, q, tenant, ref)
	if err != nil {
		return nil, journal{}, err
	}
	j, err := loadJournal(ctx, q, b.ID)
	if err != nil {
		return nil, j, err
	}
	l, err := portfolio.Replay(b, j.txs, j.hashes)
	if err != nil {
		return nil, j, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return l, j, nil
}

// Basis says which clock an as-of read follows.
type Basis string

const (
	// ByEvent keeps the transactions that happened by the time.
	ByEvent Basis = "event"
	// ByRecorded keeps the transactions that had been written by the time: the
	// book as it was known then, late-arriving lines left out.
	ByRecorded Basis = "recorded"
)

// LedgerAsOf rebuilds a book as of a time on the chosen clock.
func (p Portfolios) LedgerAsOf(ctx context.Context, ref string, t time.Time, basis Basis) (*portfolio.Ledger, error) {
	h, err := p.t.handle()
	if err != nil {
		return nil, err
	}
	tx, err := h.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("store: portfolio: %w", err)
	}
	defer tx.Rollback()
	b, err := findBook(ctx, tx, p.t.id, ref)
	if err != nil {
		return nil, err
	}
	j, err := loadJournal(ctx, tx, b.ID)
	if err != nil {
		return nil, err
	}
	switch basis {
	case ByEvent:
		return portfolio.ReplayAsOf(b, j.txs, t)
	case ByRecorded:
		n := sort.Search(len(j.txs), func(i int) bool { return j.txs[i].Recorded.After(t) })
		return portfolio.Replay(b, j.txs[:n], j.hashes)
	}
	return nil, fmt.Errorf("store: portfolio: unknown basis %q (event, recorded)", basis)
}

// Update runs fn on the replayed ledgers of one or more books and, if fn
// succeeds, writes every transaction it posted in one database transaction. If
// fn or any write fails, nothing is written to any book. now is the time to
// record with: never earlier than what the books already hold. fn may return an
// import batch to record against the first book.
func (p Portfolios) Update(ctx context.Context, refs []string, fn func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error)) error {
	return p.update(ctx, refs, func(_ *sql.Tx, now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) { return fn(now, ls) }, nil)
}

// update is Update that also lets the caller read inside the transaction and
// write more rows into it, so that something else (an order's events) commits
// or fails together with the ledger transactions.
func (p Portfolios) update(ctx context.Context, refs []string, fn func(tx *sql.Tx, now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error), after func(tx *sql.Tx, now time.Time) error) error {
	h, err := p.t.handle()
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return errors.New("store: portfolio: no book to update")
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return fmt.Errorf("store: portfolio: %w", err)
	}
	defer tx.Rollback()

	ls := make([]*portfolio.Ledger, len(refs))
	js := make([]journal, len(refs))
	orig := make([]portfolio.Book, len(refs))
	seen := map[string]bool{}
	now := p.t.db.now()
	for i, ref := range refs {
		l, j, err := loadLedger(ctx, tx, p.t.id, ref)
		if err != nil {
			return err
		}
		if seen[l.Book.ID] {
			return fmt.Errorf("store: portfolio: book %s named twice", ref)
		}
		seen[l.Book.ID] = true
		ls[i], js[i], orig[i] = l, j, l.Book
		if n := len(j.txs); n > 0 && j.txs[n-1].Recorded.After(now) {
			now = j.txs[n-1].Recorded
		}
	}
	batch, err := fn(tx, now, ls)
	if err != nil {
		return err
	}
	for i, l := range ls {
		if l.Book != orig[i] {
			return fmt.Errorf("store: portfolio: book %s was changed in memory: archive and create books through the store", orig[i].ID)
		}
		all := l.Txs()
		if len(all) < len(js[i].txs) {
			return errors.New("store: portfolio: the journal shrank")
		}
		prev := ""
		if n := len(js[i].chains); n > 0 {
			prev = js[i].chains[n-1]
		}
		for _, t := range all[len(js[i].txs):] {
			if prev, err = insertTx(ctx, tx, l.Book, t, l.Hash(t.ID), prev); err != nil {
				return err
			}
		}
	}
	if batch != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pf_import (book_id, file, sha256, rows, posted, repeated, at_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ls[0].Book.ID, batch.File, batch.SHA256, batch.Rows, batch.Posted, batch.Repeated, ms(now)); err != nil {
			return fmt.Errorf("store: portfolio: %w", err)
		}
	}
	if after != nil {
		if err := after(tx, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: portfolio: %w", err)
	}
	return nil
}

func insertTx(ctx context.Context, x pfExec, b portfolio.Book, t portfolio.Tx, hash, prevChain string) (string, error) {
	if hash == "" {
		hash = portfolio.ContentHash(t)
	}
	resv, meta := "", ""
	if t.Reservation != nil {
		raw, err := json.Marshal(t.Reservation)
		if err != nil {
			return "", fmt.Errorf("store: portfolio: %w", err)
		}
		resv = string(raw)
	}
	if len(t.Meta) > 0 {
		raw, err := json.Marshal(t.Meta)
		if err != nil {
			return "", fmt.Errorf("store: portfolio: %w", err)
		}
		meta = string(raw)
	}
	chain := chainHash(prevChain, t, hash, resv, meta)
	if _, err := x.ExecContext(ctx, `INSERT INTO pf_tx (book_id, env, seq, id, kind, event_ms, recorded_ms, source, import_id, memo, reverses, releases, reservation, meta, content_hash, chain)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, string(b.Env), t.Seq, t.ID, string(t.Kind), ms(t.Event), ms(t.Recorded), t.Source, t.ImportID, t.Memo, t.Reverses, t.Releases, resv, meta, hash, chain); err != nil {
		return "", fmt.Errorf("store: portfolio: %s: %w", t.ID, err)
	}
	for n, e := range t.Entries {
		nl := 0
		if e.NewLot {
			nl = 1
		}
		if _, err := x.ExecContext(ctx, `INSERT INTO pf_entry (book_id, seq, n, account, currency, minor, security, qty, lot, new_lot, acquired) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			b.ID, t.Seq, n, e.Account, string(e.Amount.Currency()), e.Amount.Minor(), e.Security, e.Qty.String(), e.Lot, nl, e.Acquired); err != nil {
			return "", fmt.Errorf("store: portfolio: %s: %w", t.ID, err)
		}
		if e.NewLot {
			if _, err := x.ExecContext(ctx, `INSERT INTO pf_lot (book_id, env, id, security, currency, qty, cost, acquired, open_seq) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				b.ID, string(b.Env), e.Lot, e.Security, string(e.Amount.Currency()), e.Qty.String(), e.Amount.Minor(), e.Acquired, t.Seq); err != nil {
				return "", fmt.Errorf("store: portfolio: %s: %w", t.ID, err)
			}
		}
	}
	return chain, nil
}

// chainHash covers everything a transaction says, and the chain hash of the one
// before it.
func chainHash(prev string, t portfolio.Tx, hash, resv, meta string) string {
	type ent struct {
		Account, Currency, Security, Qty, Lot, Acquired string
		Minor                                           int64
		NewLot                                          bool
	}
	es := make([]ent, len(t.Entries))
	for i, e := range t.Entries {
		es[i] = ent{e.Account, string(e.Amount.Currency()), e.Security, e.Qty.String(), e.Lot, e.Acquired, e.Amount.Minor(), e.NewLot}
	}
	doc := struct {
		Prev, Book, ID, Kind, Source, ImportID, Memo, Reverses, Releases, Reservation, Meta, Hash string
		Seq, Event, Recorded                                                                      int64
		Entries                                                                                   []ent
	}{prev, t.Book, t.ID, string(t.Kind), t.Source, t.ImportID, t.Memo, t.Reverses, t.Releases, resv, meta, hash,
		t.Seq, ms(t.Event), ms(t.Recorded), es}
	b, _ := json.Marshal(doc)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Archive stops a paper book taking transactions. A real book is never archived.
func (p Portfolios) Archive(ctx context.Context, ref string) error {
	h, err := p.t.handle()
	if err != nil {
		return err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return fmt.Errorf("store: portfolio: %w", err)
	}
	defer tx.Rollback()
	b, err := findBook(ctx, tx, p.t.id, ref)
	if err != nil {
		return err
	}
	if err := archiveBook(ctx, tx, b, ms(p.t.db.now())); err != nil {
		return err
	}
	return tx.Commit()
}

func archiveBook(ctx context.Context, x pfExec, b portfolio.Book, at int64) error {
	if !b.Env.Paper() {
		return fmt.Errorf("portfolio: a %s book cannot be archived or reset", b.Env)
	}
	if b.Status == portfolio.Archived {
		return fmt.Errorf("portfolio: book %s/%s is already archived", b.Account, b.Name)
	}
	if _, err := x.ExecContext(ctx, `UPDATE pf_books SET status = 'archived', archived_ms = ? WHERE id = ?`, at, b.ID); err != nil {
		return fmt.Errorf("store: portfolio: %w", err)
	}
	return nil
}

// ResetPaper archives a paper book and opens a new empty one in its place with
// the same account, environment, currency and method. The old journal stays as
// it was; the new book shares nothing with it.
func (p Portfolios) ResetPaper(ctx context.Context, ref, newName string) (portfolio.Book, error) {
	h, err := p.t.handle()
	if err != nil {
		return portfolio.Book{}, err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return portfolio.Book{}, fmt.Errorf("store: portfolio: %w", err)
	}
	defer tx.Rollback()
	old, err := findBook(ctx, tx, p.t.id, ref)
	if err != nil {
		return portfolio.Book{}, err
	}
	if !old.Env.Paper() {
		return portfolio.Book{}, fmt.Errorf("portfolio: a %s book cannot be archived or reset", old.Env)
	}
	nb := old
	nb.Name, nb.Status = newName, portfolio.Active
	if nb.Name == old.Name {
		return portfolio.Book{}, errors.New("portfolio: the new book needs a different name")
	}
	if err := nb.Validate(); err != nil {
		return portfolio.Book{}, err
	}
	nb.ID = BookID(p.t.id, nb.Account, nb.Name)
	at := ms(p.t.db.now())
	if old.Status == portfolio.Active {
		if err := archiveBook(ctx, tx, old, at); err != nil {
			return portfolio.Book{}, err
		}
	}
	if err := insertBook(ctx, tx, p.t.id, nb, at); err != nil {
		return portfolio.Book{}, err
	}
	if err := tx.Commit(); err != nil {
		return portfolio.Book{}, fmt.Errorf("store: portfolio: %w", err)
	}
	return nb, nil
}

// Imports lists the import batches of a book, oldest first.
func (p Portfolios) Imports(ctx context.Context, ref string) ([]ImportBatch, error) {
	h, err := p.t.handle()
	if err != nil {
		return nil, err
	}
	b, err := findBook(ctx, h.r, p.t.id, ref)
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT file, sha256, rows, posted, repeated, at_ms FROM pf_import WHERE book_id = ? ORDER BY id`, b.ID)
	if err != nil {
		return nil, fmt.Errorf("store: portfolio: %w", err)
	}
	defer rows.Close()
	var out []ImportBatch
	for rows.Next() {
		var ib ImportBatch
		var at int64
		if err := rows.Scan(&ib.File, &ib.SHA256, &ib.Rows, &ib.Posted, &ib.Repeated, &at); err != nil {
			return nil, fmt.Errorf("store: portfolio: %w", err)
		}
		ib.At = fromMS(at)
		out = append(out, ib)
	}
	return out, rows.Err()
}

// VerifyReport is what Verify checked.
type VerifyReport struct {
	Book        portfolio.Book
	Txs, Lots   int
	Fingerprint string
	Head        string // the chain hash of the newest transaction
}

// Verify proves a book is intact: the sequence is whole, every chain hash
// matches the rows, the journal replays under the rules, every currency
// balances, and the stored lot openings are exactly the ones the journal opens.
func (p Portfolios) Verify(ctx context.Context, ref string) (VerifyReport, error) {
	h, err := p.t.handle()
	if err != nil {
		return VerifyReport{}, err
	}
	tx, err := h.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return VerifyReport{}, fmt.Errorf("store: portfolio: %w", err)
	}
	defer tx.Rollback()
	l, j, err := loadLedger(ctx, tx, p.t.id, ref)
	if err != nil {
		return VerifyReport{}, err
	}
	rep := VerifyReport{Book: l.Book, Txs: len(j.txs)}
	prev := ""
	for i, t := range j.txs {
		resv, meta := "", ""
		if t.Reservation != nil {
			raw, _ := json.Marshal(t.Reservation)
			resv = string(raw)
		}
		if len(t.Meta) > 0 {
			raw, _ := json.Marshal(t.Meta)
			meta = string(raw)
		}
		prev = chainHash(prev, t, j.hashes[t.ID], resv, meta)
		if prev != j.chains[i] {
			return rep, fmt.Errorf("%w: %s (seq %d) does not match its chain hash", ErrCorrupt, t.ID, t.Seq)
		}
	}
	rep.Head = prev
	proj := l.Projection()
	for _, c := range proj.Currencies() {
		if tb := proj.TrialBalance(c); tb != 0 {
			return rep, fmt.Errorf("%w: %s does not balance (%d)", ErrCorrupt, c, tb)
		}
	}
	want := map[string]string{}
	for _, t := range l.Txs() {
		for _, e := range t.Entries {
			if e.NewLot {
				want[e.Lot] = fmt.Sprintf("%s|%s|%s|%d|%s|%d", e.Security, e.Amount.Currency(), e.Qty, e.Amount.Minor(), e.Acquired, t.Seq)
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, security, currency, qty, cost, acquired, open_seq FROM pf_lot WHERE book_id = ?`, l.Book.ID)
	if err != nil {
		return rep, fmt.Errorf("store: portfolio: %w", err)
	}
	defer rows.Close()
	have := 0
	for rows.Next() {
		var id, sec, cur, qty, acq string
		var cost, seq int64
		if err := rows.Scan(&id, &sec, &cur, &qty, &cost, &acq, &seq); err != nil {
			return rep, fmt.Errorf("store: portfolio: %w", err)
		}
		have++
		if want[id] != fmt.Sprintf("%s|%s|%s|%d|%s|%d", sec, cur, qty, cost, acq, seq) {
			return rep, fmt.Errorf("%w: lot record %s does not match the journal", ErrCorrupt, id)
		}
	}
	if err := rows.Err(); err != nil {
		return rep, fmt.Errorf("store: portfolio: %w", err)
	}
	if have != len(want) {
		return rep, fmt.Errorf("%w: %d lot records for %d lot openings", ErrCorrupt, have, len(want))
	}
	rep.Lots, rep.Fingerprint = have, proj.Fingerprint()
	return rep, nil
}
