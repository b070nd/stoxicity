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
