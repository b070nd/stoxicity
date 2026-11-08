package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

var pfT0 = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// clock returns a clock that moves a minute on every reading.
func clock(start time.Time) func() time.Time {
	var mu sync.Mutex
	cur := start
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		cur = cur.Add(time.Minute)
		return cur
	}
}

func eur(m int64) money.Amount {
	a, _ := money.FromMinor(m, "EUR")
	return a
}

func newPF(t *testing.T) (*DB, Portfolios) {
	t.Helper()
	db, _ := openTemp(t)
	db.now = clock(pfT0)
	return db, db.Portfolios("default")
}

func mkBook(t *testing.T, p Portfolios, acct, name string, env portfolio.Env) portfolio.Book {
	t.Helper()
	b, err := p.CreateBook(context.Background(), portfolio.Book{Account: acct, Name: name, Env: env, Base: "EUR", Method: portfolio.FIFO})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func deposit(t *testing.T, p Portfolios, ref string, minor int64, importID string) {
	t.Helper()
	if err := tryDeposit(p, ref, minor, importID); err != nil {
		t.Fatal(err)
	}
}

func tryDeposit(p Portfolios, ref string, minor int64, importID string) error {
	return p.Update(context.Background(), []string{ref}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		l := ls[0]
		tx, err := l.Deposit(portfolio.Common{Event: now, Source: "test", ImportID: importID}, eur(minor))
		if err != nil {
			return nil, err
		}
		_, err = l.Post(tx, now)
		return nil, err
	})
}

func TestBooksAreCreatedFoundAndKeptApartByTenant(t *testing.T) {
	ctx := context.Background()
	db, p := newPF(t)
	a := mkBook(t, p, "main", "core", portfolio.Real)
	b := mkBook(t, p, "main", "paper", portfolio.PaperLocal)
	if a.ID == b.ID || !strings.HasPrefix(a.ID, "bk_") || a.ID != BookID("default", "main", "core") {
		t.Fatalf("%s %s", a.ID, b.ID)
	}
	if _, err := p.CreateBook(ctx, portfolio.Book{Account: "main", Name: "core", Env: portfolio.Real, Base: "EUR", Method: portfolio.LIFO}); !errors.Is(err, ErrBookExists) {
		t.Fatalf("%v", err)
	}
	if _, err := p.CreateBook(ctx, portfolio.Book{Account: "main", Name: "x", Env: "live", Base: "EUR", Method: portfolio.FIFO}); err == nil {
		t.Fatal("unknown environment")
	}
	for _, ref := range []string{a.ID, "main/core"} {
		got, err := p.FindBook(ctx, ref)
		if err != nil || got != a {
			t.Fatalf("%s: %+v %v", ref, got, err)
		}
	}
	if _, err := p.FindBook(ctx, "main/none"); !errors.Is(err, ErrNoBook) {
		t.Fatalf("%v", err)
	}
	books, err := p.Books(ctx)
	if err != nil || len(books) != 2 || books[0].Name != "core" {
		t.Fatalf("%+v %v", books, err)
	}

	// another tenant sees nothing, cannot read or write by ID or name, and its
	// own book of the same name is a different book
	other := db.Portfolios("other")
	if bs, err := other.Books(ctx); err != nil || len(bs) != 0 {
		t.Fatalf("%+v %v", bs, err)
	}
	deposit(t, p, "main/core", 5_000, "d1")
	for _, ref := range []string{a.ID, "main/core"} {
		if _, err := other.FindBook(ctx, ref); !errors.Is(err, ErrNoBook) {
			t.Fatalf("read %s: %v", ref, err)
		}
		if _, err := other.Ledger(ctx, ref); !errors.Is(err, ErrNoBook) {
			t.Fatalf("ledger %s: %v", ref, err)
		}
		if err := tryDeposit(other, ref, 1, "x"); !errors.Is(err, ErrNoBook) {
			t.Fatalf("write %s: %v", ref, err)
		}
	}
	oa := mkBook(t, other, "main", "core", portfolio.Real)
	if oa.ID == a.ID {
		t.Fatal("two tenants share a book ID")
	}
	deposit(t, other, "main/core", 7, "d1")
	l1, _ := p.Ledger(ctx, "main/core")
	l2, _ := other.Ledger(ctx, "main/core")
	if l1.Projection().Cash("EUR") != 5_000 || l2.Projection().Cash("EUR") != 7 || l1.Txs()[0].ID == l2.Txs()[0].ID {
		t.Fatal("tenants are mixed")
	}
	// an invalid tenant creates nothing
	if _, err := db.Portfolios("../x").Books(ctx); err == nil {
		t.Fatal("invalid tenant")
	}
}

func TestADatabaseFileCopiedToAnotherTenantShowsNoBooks(t *testing.T) {
	ctx := context.Background()
	db, dir := openTemp(t)
	db.now = clock(pfT0)
	p := db.Portfolios("default")
	mkBook(t, p, "main", "core", portfolio.Real)
	deposit(t, p, "main/core", 100, "d1")
	h, err := db.tenantHandle("default")
	if err != nil {
		t.Fatal(err)
	}
	copyTo := filepath.Join(dir, "tenants", "other.db")
	if _, err := h.w.Exec(`VACUUM INTO '` + copyTo + `'`); err != nil {
		t.Fatal(err)
	}
	other := db.Portfolios("other")
	if bs, err := other.Books(ctx); err != nil || len(bs) != 0 {
		t.Fatalf("%+v %v", bs, err)
	}
	if _, err := other.FindBook(ctx, BookID("default", "main", "core")); !errors.Is(err, ErrNoBook) {
		t.Fatalf("%v", err)
	}
	if err := tryDeposit(other, BookID("default", "main", "core"), 1, "x"); !errors.Is(err, ErrNoBook) {
		t.Fatalf("%v", err)
	}
}

func TestUpdatePersistsAndReloadsTheSameLedger(t *testing.T) {
	ctx := context.Background()
	_, p := newPF(t)
	mkBook(t, p, "main", "core", portfolio.Real)
	deposit(t, p, "main/core", 1_000_000, "d1")
	var live string
	err := p.Update(ctx, []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		l := ls[0]
		c := portfolio.Common{Event: now, Source: "test", ImportID: "t1"}
		for _, f := range []func() (portfolio.Tx, error){
			func() (portfolio.Tx, error) {
				return l.Buy(portfolio.Trade{Common: c, Security: "ACME", Qty: money.MustDecimal("10"), Gross: eur(100_000), Commission: eur(100)})
			},
		} {
			tx, err := f()
			if err != nil {
				return nil, err
			}
			if _, err := l.Post(tx, now); err != nil {
				return nil, err
			}
		}
		live = l.Projection().Fingerprint()
		return &ImportBatch{File: "stmt.csv", SHA256: "abc", Rows: 1, Posted: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := p.Ledger(ctx, "main/core")
	if err != nil {
		t.Fatal(err)
	}
	if l.Projection().Fingerprint() != live || len(l.Txs()) != 2 {
		t.Fatal("the reloaded ledger differs")
	}
	if pos := l.Projection().Positions(); len(pos) != 1 || pos[0].Qty.String() != "10" {
		t.Fatalf("%+v", pos)
	}
	rep, err := p.Verify(ctx, "main/core")
	if err != nil || rep.Txs != 2 || rep.Lots != 1 || rep.Fingerprint != live || rep.Head == "" {
		t.Fatalf("%+v %v", rep, err)
	}
	ib, err := p.Imports(ctx, "main/core")
	if err != nil || len(ib) != 1 || ib[0].File != "stmt.csv" || ib[0].Posted != 1 {
		t.Fatalf("%+v %v", ib, err)
	}
	// the sale uses the persisted lot
	err = p.Update(ctx, []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		tx, err := ls[0].Sell(portfolio.Trade{Common: portfolio.Common{Event: now, Source: "test"}, Security: "ACME", Qty: money.MustDecimal("4"), Gross: eur(50_000)})
		if err != nil {
			return nil, err
		}
		_, err = ls[0].Post(tx, now)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	l, _ = p.Ledger(ctx, "main/core")
	if l.Projection().RealizedGain("EUR") != 10_000 {
		t.Fatalf("gain %d", l.Projection().RealizedGain("EUR"))
	}
	if _, err := p.Verify(ctx, "main/core"); err != nil {
		t.Fatal(err)
	}
}

func TestAFailedUpdateWritesNothing(t *testing.T) {
	ctx := context.Background()
	_, p := newPF(t)
	mkBook(t, p, "main", "core", portfolio.Real)
	deposit(t, p, "main/core", 1_000, "d1")
	boom := errors.New("boom")
	err := p.Update(ctx, []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		tx, _ := ls[0].Deposit(portfolio.Common{Event: now, Source: "t"}, eur(5))
		if _, err := ls[0].Post(tx, now); err != nil {
			return nil, err
		}
		return &ImportBatch{File: "x"}, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
	l, _ := p.Ledger(ctx, "main/core")
	if len(l.Txs()) != 1 {
		t.Fatalf("%d transactions", len(l.Txs()))
	}
	if ib, _ := p.Imports(ctx, "main/core"); len(ib) != 0 {
		t.Fatal("an import was recorded for a failed update")
	}
	// a refused transaction inside the callback stops the whole batch
	err = p.Update(ctx, []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		ok, _ := ls[0].Deposit(portfolio.Common{Event: now, Source: "t"}, eur(5))
		if _, err := ls[0].Post(ok, now); err != nil {
			return nil, err
		}
		over, _ := ls[0].Withdraw(portfolio.Common{Event: now, Source: "t"}, eur(10_000))
		_, err := ls[0].Post(over, now)
		return nil, err
	})
	if !errors.Is(err, portfolio.ErrInsufficient) {
		t.Fatalf("%v", err)
	}
	l, _ = p.Ledger(ctx, "main/core")
	if len(l.Txs()) != 1 || l.Projection().Cash("EUR") != 1_000 {
		t.Fatal("the first half of a refused batch was kept")
	}
	// the same book twice, or none
	if err := p.Update(ctx, []string{"main/core", "main/core"}, func(time.Time, []*portfolio.Ledger) (*ImportBatch, error) { return nil, nil }); err == nil {
		t.Fatal("the same book twice")
	}
	if err := p.Update(ctx, nil, nil); err == nil {
		t.Fatal("no book")
	}
	// the callback cannot reshape the book
	err = p.Update(ctx, []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		ls[0].Book.Env = portfolio.PaperLocal
		return nil, nil
	})
	if err == nil {
		t.Fatal("a book was changed in memory")
	}
}

func TestImportingTheSameRecordTwiceWritesOnce(t *testing.T) {
	ctx := context.Background()
	_, p := newPF(t)
	mkBook(t, p, "main", "core", portfolio.Real)
	for i := 0; i < 3; i++ {
		err := p.Update(ctx, []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
			tx, _ := ls[0].Deposit(portfolio.Common{Event: pfT0, Source: "stmt", ImportID: "line-1"}, eur(500))
			ps, err := ls[0].Post(tx, now)
			if err != nil {
				return nil, err
			}
			if (i == 0) == ps.Existing {
				return nil, fmt.Errorf("round %d: existing=%v", i, ps.Existing)
			}
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	l, _ := p.Ledger(ctx, "main/core")
	if len(l.Txs()) != 1 || l.Projection().Cash("EUR") != 500 {
		t.Fatal("the record was written more than once")
	}
	// the same ID with other content is refused after a reload too
	err := p.Update(ctx, []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		tx, _ := ls[0].Deposit(portfolio.Common{Event: pfT0, Source: "stmt", ImportID: "line-1"}, eur(501))
		_, err := ls[0].Post(tx, now)
		return nil, err
	})
	if !errors.Is(err, portfolio.ErrImportConflict) {
		t.Fatalf("%v", err)
	}
	// and so is a book that holds the line under another book's ID
	mkBook(t, p, "main", "second", portfolio.Real)
	deposit(t, p, "main/second", 9, "line-1")
}

func TestTransferBetweenBooksIsAtomic(t *testing.T) {
	ctx := context.Background()
	_, p := newPF(t)
	mkBook(t, p, "main", "a", portfolio.PaperLocal)
	mkBook(t, p, "main", "b", portfolio.PaperLocal)
	mkBook(t, p, "main", "r", portfolio.Real)
	deposit(t, p, "main/a", 10_000, "d")
	deposit(t, p, "main/r", 10_000, "d")
	xfer := func(from, to string, cash int64) error {
		return p.Update(ctx, []string{from, to}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
			_, _, err := portfolio.PostTransfer(ls[0], ls[1], portfolio.TransferArgs{Common: portfolio.Common{Event: pfT0, Source: "test", ImportID: "tr-1"}, Cash: eur(cash)}, now)
			return nil, err
		})
	}
	if err := xfer("main/a", "main/b", 4_000); err != nil {
		t.Fatal(err)
	}
	a, _ := p.Ledger(ctx, "main/a")
	b, _ := p.Ledger(ctx, "main/b")
	if a.Projection().Cash("EUR") != 6_000 || b.Projection().Cash("EUR") != 4_000 {
		t.Fatal("cash did not move")
	}
	// repeated: nothing more is written to either book
	if err := xfer("main/a", "main/b", 4_000); err != nil {
		t.Fatal(err)
	}
	a2, _ := p.Ledger(ctx, "main/a")
	if len(a2.Txs()) != len(a.Txs()) {
		t.Fatal("the transfer was written twice")
	}
	// a real book and a paper book never exchange; neither book changes
	for _, pair := range [][2]string{{"main/r", "main/a"}, {"main/a", "main/r"}} {
		if err := xfer(pair[0], pair[1], 1); err == nil || !strings.Contains(err.Error(), "never exchange") {
			t.Fatalf("%v", err)
		}
	}
	r, _ := p.Ledger(ctx, "main/r")
	if len(r.Txs()) != 1 {
		t.Fatal("the real book changed")
	}
	// the second leg failing takes the first leg back
	if err := p.Archive(ctx, "main/b"); err != nil {
		t.Fatal(err)
	}
	before, _ := p.Ledger(ctx, "main/a")
	err := p.Update(ctx, []string{"main/a", "main/b"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		_, _, err := portfolio.PostTransfer(ls[0], ls[1], portfolio.TransferArgs{Common: portfolio.Common{Event: now, Source: "t"}, Cash: eur(1)}, now)
		return nil, err
	})
	if !errors.Is(err, portfolio.ErrArchived) {
		t.Fatalf("%v", err)
	}
	after, _ := p.Ledger(ctx, "main/a")
	if after.Projection().Fingerprint() != before.Projection().Fingerprint() {
		t.Fatal("a half transfer was kept")
	}
}

func TestRecordedTimeNeverGoesBackwardsAndAsOfReadsBothClocks(t *testing.T) {
	ctx := context.Background()
	db, p := newPF(t)
	mkBook(t, p, "main", "core", portfolio.Real)
	// events 10:00, 11:00, 12:00; the third is recorded last but is the oldest news
	post := func(event time.Time, minor int64, id string) time.Time {
		var rec time.Time
		err := p.Update(ctx, []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
			tx, _ := ls[0].Deposit(portfolio.Common{Event: event, Source: "t", ImportID: id}, eur(minor))
			rec = now
			_, err := ls[0].Post(tx, now)
			return nil, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	h := func(n int) time.Time { return pfT0.Add(time.Duration(n) * time.Hour) }
	post(h(1), 100, "a")
	r2 := post(h(2), 200, "b")
	post(h(0), 400, "late") // happened first, recorded last
	// a clock that stepped back is not believed
	db.now = func() time.Time { return pfT0.Add(-24 * time.Hour) }
	post(h(3), 800, "c")
	l, _ := p.Ledger(ctx, "main/core")
	var last time.Time
	for _, x := range l.Txs() {
		if x.Recorded.Before(last) {
			t.Fatal("recorded time went backwards")
		}
		last = x.Recorded
	}
	cash := func(l *portfolio.Ledger, err error) int64 {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return l.Projection().Cash("EUR")
	}
	// by event time the late line is part of 10:30; by recorded time it was not yet known
	if got := cash(p.LedgerAsOf(ctx, "main/core", h(0).Add(30*time.Minute), ByEvent)); got != 400 {
		t.Fatalf("by event: %d", got)
	}
	if got := cash(p.LedgerAsOf(ctx, "main/core", r2, ByRecorded)); got != 300 {
		t.Fatalf("by recorded: %d", got)
	}
	if got := cash(p.LedgerAsOf(ctx, "main/core", r2, ByEvent)); got != 400 {
		t.Fatalf("only the late line happened by the time b was recorded: %d", got)
	}
	if got := cash(p.LedgerAsOf(ctx, "main/core", pfT0.Add(-48*time.Hour), ByRecorded)); got != 0 {
		t.Fatalf("before anything was recorded: %d", got)
	}
	if _, err := p.LedgerAsOf(ctx, "main/core", r2, "wall"); err == nil {
		t.Fatal("unknown basis")
	}
}

func TestDatabaseRefusesWhatTheProgramWouldNotDo(t *testing.T) {
	_, p := newPF(t)
	real1 := mkBook(t, p, "main", "core", portfolio.Real)
	paper := mkBook(t, p, "main", "paper", portfolio.PaperLocal)
	deposit(t, p, "main/core", 100_000, "d")
	deposit(t, p, "main/paper", 100, "d")
	if err := p.Update(context.Background(), []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		tx, err := ls[0].Buy(portfolio.Trade{Common: portfolio.Common{Event: now, Source: "t"}, Security: "ACME", Qty: money.MustDecimal("1"), Gross: eur(1_000)})
		if err != nil {
			return nil, err
		}
		_, err = ls[0].Post(tx, now)
		return &ImportBatch{File: "f.csv", SHA256: "x", Rows: 1, Posted: 1}, err
	}); err != nil {
		t.Fatal(err)
	}
	h, _ := p.t.handle()
	exec := func(q string, args ...any) error { _, err := h.w.Exec(q, args...); return err }
	insert := func(book, env string, seq int64, rec int64) error {
		return exec(`INSERT INTO pf_tx (book_id, env, seq, id, kind, event_ms, recorded_ms, source, import_id, memo, reverses, releases, reservation, meta, content_hash, chain)
			VALUES (?, ?, ?, ?, 'fee', 1, ?, 's', '', '', '', '', '', '', 'h', 'c')`, book, env, seq, fmt.Sprintf("tx-%s-%d", book, seq), rec)
	}
	want := func(name string, err error, sub string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), sub) {
			t.Errorf("%s: %v (want %q)", name, err, sub)
		}
	}
	want("edit a transaction", exec(`UPDATE pf_tx SET memo = 'x'`), "append-only")
	want("delete a transaction", exec(`DELETE FROM pf_tx`), "append-only")
	want("edit an entry", exec(`UPDATE pf_entry SET minor = 1`), "append-only")
	want("delete an entry", exec(`DELETE FROM pf_entry`), "append-only")
	want("delete a book", exec(`DELETE FROM pf_books`), "never deleted")
	want("edit a lot", exec(`UPDATE pf_lot SET cost = 1`), "append-only")
	want("edit an import", exec(`UPDATE pf_import SET rows = 1`), "append-only")
	want("a gap in the sequence", insert(real1.ID, "real", 5, 9_999_999_999_999), "no gaps")
	want("a repeated sequence", insert(real1.ID, "real", 1, 9_999_999_999_999), "no gaps")
	want("a paper transaction in a real book", insert(real1.ID, "paper_local", 3, 9_999_999_999_999), "FOREIGN KEY")
	want("a real transaction in a paper book", insert(paper.ID, "real", 2, 9_999_999_999_999), "FOREIGN KEY")
	want("recorded time going back", insert(real1.ID, "real", 3, 1), "backwards")
	want("a missing book", insert("bk_none", "real", 1, 1), "FOREIGN KEY")
	want("a transaction without a source", exec(`INSERT INTO pf_tx (book_id, env, seq, id, kind, event_ms, recorded_ms, source, import_id, memo, reverses, releases, reservation, meta, content_hash, chain)
		VALUES (?, 'real', 3, 'z', 'fee', 1, 9999999999999, '', '', '', '', '', '', '', 'h', 'c')`, real1.ID), "CHECK")

	// archive: a real book cannot, whatever the route
	want("archive a real book", exec(`UPDATE pf_books SET status = 'archived', archived_ms = 1 WHERE id = ?`, real1.ID), "only a paper book")
	want("rename a book", exec(`UPDATE pf_books SET name = 'other' WHERE id = ?`, paper.ID), "only a paper book")
	want("turn a paper book real", exec(`UPDATE pf_books SET env = 'real' WHERE id = ?`, paper.ID), "only a paper book")
	want("reopen an archived book", func() error {
		if err := exec(`UPDATE pf_books SET status = 'archived', archived_ms = 1 WHERE id = ?`, paper.ID); err != nil {
			return err
		}
		return exec(`UPDATE pf_books SET status = 'active' WHERE id = ?`, paper.ID)
	}(), "only a paper book")
	want("write to an archived book", insert(paper.ID, "paper_local", 2, 9_999_999_999_999), "archived")
}

func TestVerifyFindsEditsMadeOutsideTheProgram(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, drop, sql string
		wantVerify      string
		blocksWrites    bool
	}{
		{"a memo", "pf_tx_no_update", `UPDATE pf_tx SET memo = 'edited' WHERE seq = 1`, "chain hash", false},
		{"an amount that still balances nowhere", "pf_entry_no_update", `UPDATE pf_entry SET minor = minor + 1 WHERE seq = 1 AND n = 0`, "corrupt", true},
		{"a lot record", "pf_lot_no_update", `UPDATE pf_lot SET cost = cost + 1`, "lot record", false},
		{"a deleted lot record", "pf_lot_no_delete", `DELETE FROM pf_lot`, "lot records", false},
		{"a deleted entry", "pf_entry_no_delete", `DELETE FROM pf_entry WHERE seq = 2 AND n = 1`, "corrupt", true},
		{"an event time", "pf_tx_no_update", `UPDATE pf_tx SET event_ms = event_ms + 1 WHERE seq = 2`, "chain hash", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := newPF(t)
			mkBook(t, p, "main", "core", portfolio.Real)
			deposit(t, p, "main/core", 1_000_000, "d")
			if err := p.Update(ctx, []string{"main/core"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
				tx, err := ls[0].Buy(portfolio.Trade{Common: portfolio.Common{Event: now, Source: "t"}, Security: "ACME", Qty: money.MustDecimal("1"), Gross: eur(1_000)})
				if err != nil {
					return nil, err
				}
				_, err = ls[0].Post(tx, now)
				return nil, err
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Verify(ctx, "main/core"); err != nil {
				t.Fatalf("before the edit: %v", err)
			}
			h, _ := p.t.handle()
			if _, err := h.w.Exec(`DROP TRIGGER ` + tc.drop); err != nil {
				t.Fatal(err)
			}
			if _, err := h.w.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			_, err := p.Verify(ctx, "main/core")
			if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), tc.wantVerify) {
				t.Fatalf("verify: %v", err)
			}
			if err := tryDeposit(p, "main/core", 1, "after"); err == nil && tc.blocksWrites {
				t.Fatal("a corrupt book took a new transaction")
			}
		})
	}
}

func TestOnlyPaperBooksAreArchivedOrReset(t *testing.T) {
	ctx := context.Background()
	_, p := newPF(t)
	mkBook(t, p, "main", "core", portfolio.Real)
	mkBook(t, p, "main", "paper", portfolio.PaperBroker)
	deposit(t, p, "main/core", 100, "d")
	deposit(t, p, "main/paper", 100, "d")
	if err := p.Archive(ctx, "main/core"); err == nil || !strings.Contains(err.Error(), "cannot be archived or reset") {
		t.Fatalf("%v", err)
	}
	if _, err := p.ResetPaper(ctx, "main/core", "fresh"); err == nil || !strings.Contains(err.Error(), "cannot be archived or reset") {
		t.Fatalf("%v", err)
	}
	if b, _ := p.FindBook(ctx, "main/core"); b.Status != portfolio.Active {
		t.Fatal("the real book changed")
	}
	if _, err := p.ResetPaper(ctx, "main/paper", "paper"); err == nil {
		t.Fatal("reset into the same name")
	}
	nb, err := p.ResetPaper(ctx, "main/paper", "paper-2")
	if err != nil {
		t.Fatal(err)
	}
	if nb.Env != portfolio.PaperBroker || nb.Status != portfolio.Active || nb.ID == BookID("default", "main", "paper") {
		t.Fatalf("%+v", nb)
	}
	old, _ := p.Ledger(ctx, "main/paper")
	fresh, _ := p.Ledger(ctx, "main/paper-2")
	if old.Book.Status != portfolio.Archived || old.Projection().Cash("EUR") != 100 || len(fresh.Txs()) != 0 {
		t.Fatal("the reset did not separate the books")
	}
	if err := tryDeposit(p, "main/paper", 1, "x"); !errors.Is(err, portfolio.ErrArchived) {
		t.Fatalf("%v", err)
	}
	if _, err := p.Verify(ctx, "main/paper"); err != nil {
		t.Fatalf("an archived book still verifies: %v", err)
	}
	if err := p.Archive(ctx, "main/paper"); err == nil {
		t.Fatal("archived twice")
	}
	// a reset book cannot take what the old one held
	err = p.Update(ctx, []string{"main/paper", "main/paper-2"}, func(now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		_, _, err := portfolio.PostTransfer(ls[0], ls[1], portfolio.TransferArgs{Common: portfolio.Common{Event: now, Source: "t"}, Cash: eur(1)}, now)
		return nil, err
	})
	if !errors.Is(err, portfolio.ErrArchived) {
		t.Fatalf("%v", err)
	}
}

func TestConcurrentWritersKeepOneGaplessJournal(t *testing.T) {
	ctx := context.Background()
	_, p := newPF(t)
	mkBook(t, p, "main", "core", portfolio.Real)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- tryDeposit(p, "main/core", int64(i+1), fmt.Sprintf("c%d", i))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	l, err := p.Ledger(ctx, "main/core")
	if err != nil || len(l.Txs()) != 16 || l.Projection().Cash("EUR") != 136 {
		t.Fatalf("%d %v", len(l.Txs()), err)
	}
	if _, err := p.Verify(ctx, "main/core"); err != nil {
		t.Fatal(err)
	}
}
