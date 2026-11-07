package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/fundamentals"
)

var _ fundamentals.Store = (*DB)(nil)

// PutFundamentals implements fundamentals.Store. Rows of periods more than four
// years old are dropped with each reading, so the table keeps to a window.
func (db *DB) PutFundamentals(ctx context.Context, facts []fundamentals.Fact, at time.Time) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: fundamentals: %w", err)
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO fundamental (cik, metric, period_end, period_start, value) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: fundamentals: %w", err)
	}
	defer st.Close()
	for _, f := range facts {
		if _, err := st.ExecContext(ctx, f.CIK, f.Metric, f.End, f.Start, f.Value); err != nil {
			return fmt.Errorf("store: fundamentals: %w", err)
		}
	}
	// Every reading is also kept as history: the table above holds the newest
	// figure, this one every figure that was read and from which filing.
	if err := putFacts(ctx, tx, facts, fundamentals.SourceFrames, at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fundamental WHERE period_end < ?`, at.AddDate(-4, 0, 0).Format(time.DateOnly)); err != nil {
		return fmt.Errorf("store: fundamentals: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fundamental_run (id, read_ms, facts) VALUES (1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET read_ms = excluded.read_ms, facts = excluded.facts`, ms(at), len(facts)); err != nil {
		return fmt.Errorf("store: fundamentals: %w", err)
	}
	return tx.Commit()
}

// FundamentalsRead implements fundamentals.Store.
func (db *DB) FundamentalsRead(ctx context.Context) (time.Time, error) {
	h, err := db.baseHandle()
	if err != nil {
		return time.Time{}, err
	}
	var at int64
	err = h.r.QueryRowContext(ctx, `SELECT read_ms FROM fundamental_run WHERE id = 1`).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("store: fundamentals: %w", err)
	}
	return fromMS(at), nil
}

// Fundamentals returns every stored fact.
func (db *DB) Fundamentals(ctx context.Context) ([]fundamentals.Fact, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT cik, metric, period_start, period_end, value FROM fundamental ORDER BY cik, metric, period_end`)
	if err != nil {
		return nil, fmt.Errorf("store: fundamentals: %w", err)
	}
	defer rows.Close()
	var out []fundamentals.Fact
	for rows.Next() {
		var f fundamentals.Fact
		if err := rows.Scan(&f.CIK, &f.Metric, &f.Start, &f.End, &f.Value); err != nil {
			return nil, fmt.Errorf("store: fundamentals: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// execer is what putFacts needs of a transaction.
type execer interface {
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

func putFacts(ctx context.Context, tx execer, facts []fundamentals.Fact, source string, at time.Time) error {
	// The same figure of the same filing read again changes nothing, except that a
	// filing day or form that was not known is filled in: the figure is the same.
	st, err := tx.PrepareContext(ctx, `INSERT INTO fundamental_fact
		(cik, metric, taxonomy, concept, unit, period_start, period_end, dimensions, accession, form, filed_day, value, source, received_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (cik, concept, unit, period_start, period_end, dimensions, accession, value) DO UPDATE SET
			filed_day = excluded.filed_day, form = excluded.form
		WHERE fundamental_fact.filed_day = '' AND excluded.filed_day <> ''`)
	if err != nil {
		return fmt.Errorf("store: fundamental history: %w", err)
	}
	defer st.Close()
	for _, f := range facts {
		if f.Accession == "" {
			continue // a figure with no filing cannot be placed in time
		}
		if _, err := st.ExecContext(ctx, f.CIK, f.Metric, f.Taxonomy, f.Concept, f.Unit, f.Start, f.End, f.Dimensions,
			f.Accession, f.Form, f.Filed, f.Value, source, ms(at)); err != nil {
			return fmt.Errorf("store: fundamental history: %w", err)
		}
	}
	return nil
}

// PutFactHistory adds the facts of a company's history. A fact already stored
// (same filing and value) stays as it is, with its first receipt time.
func (db *DB) PutFactHistory(ctx context.Context, facts []fundamentals.Fact, at time.Time) (int, error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	var before int
	if err := h.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM fundamental_fact`).Scan(&before); err != nil {
		return 0, fmt.Errorf("store: fundamental history: %w", err)
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: fundamental history: %w", err)
	}
	defer tx.Rollback()
	if err := putFacts(ctx, tx, facts, fundamentals.SourceCompanyFacts, at); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: fundamental history: %w", err)
	}
	var after int
	if err := h.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM fundamental_fact`).Scan(&after); err != nil {
		return 0, fmt.Errorf("store: fundamental history: %w", err)
	}
	return after - before, nil
}

// FactHistory returns every fact stored for the company, each with the day its
// filing was filed: its own, else the day the EDGAR index gives the accession.
func (db *DB) FactHistory(ctx context.Context, cik string) ([]fundamentals.Fact, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT f.cik, f.metric, f.taxonomy, f.concept, f.unit, f.period_start, f.period_end, f.dimensions,
			f.accession, f.form, COALESCE(NULLIF(f.filed_day, ''), (SELECT i.filed_day FROM filing_index i WHERE i.accession = f.accession), ''), f.value
		FROM fundamental_fact f WHERE f.cik = ? ORDER BY f.metric, f.period_end, f.seq`, cik)
	if err != nil {
		return nil, fmt.Errorf("store: fundamental history: %w", err)
	}
	defer rows.Close()
	var out []fundamentals.Fact
	for rows.Next() {
		var f fundamentals.Fact
		if err := rows.Scan(&f.CIK, &f.Metric, &f.Taxonomy, &f.Concept, &f.Unit, &f.Start, &f.End, &f.Dimensions,
			&f.Accession, &f.Form, &f.Filed, &f.Value); err != nil {
			return nil, fmt.Errorf("store: fundamental history: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// PutShareClasses adds the cover-page share counts of a filing; one already
// stored stays as it was first read.
func (db *DB) PutShareClasses(ctx context.Context, cik, accession, form, filed string, classes []fundamentals.ClassShares, at time.Time) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: share classes: %w", err)
	}
	defer tx.Rollback()
	for _, c := range classes {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO share_class (cik, accession, as_of, dimensions, shares, ticker, form, filed_day, source, received_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'sec-cover', ?)`, cik, accession, c.AsOf, c.Dimensions, c.Shares, c.Ticker, form, filed, ms(at)); err != nil {
			return fmt.Errorf("store: share classes: %w", err)
		}
	}
	return tx.Commit()
}

// ShareClasses returns the share counts of the company's newest filing that has
// any, and that filing's accession and filing day.
func (db *DB) ShareClasses(ctx context.Context, cik string) ([]fundamentals.ClassShares, string, string, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, "", "", err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT accession, filed_day, as_of, dimensions, shares, ticker FROM share_class
		WHERE cik = ? AND accession = (SELECT accession FROM share_class WHERE cik = ? ORDER BY as_of DESC, filed_day DESC, accession DESC LIMIT 1)
		ORDER BY dimensions`, cik, cik)
	if err != nil {
		return nil, "", "", fmt.Errorf("store: share classes: %w", err)
	}
	defer rows.Close()
	var out []fundamentals.ClassShares
	var acc, filed string
	for rows.Next() {
		var c fundamentals.ClassShares
		if err := rows.Scan(&acc, &filed, &c.AsOf, &c.Dimensions, &c.Shares, &c.Ticker); err != nil {
			return nil, "", "", fmt.Errorf("store: share classes: %w", err)
		}
		out = append(out, c)
	}
	return out, acc, filed, rows.Err()
}
