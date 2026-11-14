package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

var _ marketdata.SplitStore = (*DB)(nil)

// PutSplits adds the splits; one already stored stays as it was first seen. It
// also notes that the splits executed from since on were read at the time: the
// coverage only grows backwards (an earlier since) and forwards (a later read).
func (db *DB) PutSplits(ctx context.Context, splits []marketdata.Split, since time.Time, at time.Time) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	if at.IsZero() {
		at = db.now()
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return fmt.Errorf("store: splits: %w", err)
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO split (ticker, exec_day, split_from, split_to, source, first_seen_ms) VALUES (?, ?, ?, ?, 'polygon', ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return fmt.Errorf("store: splits: %w", err)
	}
	defer st.Close()
	for _, s := range splits {
		if s.Ticker == "" || s.Day == "" || s.From <= 0 || s.To <= 0 {
			continue
		}
		if _, err := st.ExecContext(ctx, s.Ticker, s.Day, s.From, s.To, ms(at)); err != nil {
			return fmt.Errorf("store: splits: %w", err)
		}
	}
	// The coverage is kept whole: a reading that starts later than the one before
	// does not shorten it, but only joins it when it starts before the earlier
	// reading ended. The caller reads from before the last reading, so it does.
	if _, err := tx.ExecContext(ctx, `INSERT INTO split_run (id, since_day, read_ms) VALUES (1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET since_day = CASE WHEN split_run.since_day <= excluded.since_day THEN split_run.since_day ELSE excluded.since_day END,
			read_ms = CASE WHEN split_run.read_ms >= excluded.read_ms THEN split_run.read_ms ELSE excluded.read_ms END`,
		since.Format(time.DateOnly), ms(at)); err != nil {
		return fmt.Errorf("store: splits: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO split_read (since_day, read_ms) VALUES (?, ?)`, since.Format(time.DateOnly), ms(at)); err != nil {
		return fmt.Errorf("store: splits: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: splits: %w", err)
	}
	return nil
}

// SplitCoverage returns what the stored splits cover; the zero value when no
// reading was made.
func (db *DB) SplitCoverage(ctx context.Context) (marketdata.SplitCoverage, error) {
	h, err := db.baseHandle()
	if err != nil {
		return marketdata.SplitCoverage{}, err
	}
	var c marketdata.SplitCoverage
	var read int64
	err = h.r.QueryRowContext(ctx, `SELECT since_day, read_ms FROM split_run WHERE id = 1`).Scan(&c.Since, &read)
	if errors.Is(err, sql.ErrNoRows) {
		return marketdata.SplitCoverage{}, nil
	}
	if err != nil {
		return marketdata.SplitCoverage{}, fmt.Errorf("store: splits: %w", err)
	}
	c.Read = fromMS(read)
	return c, nil
}

// Splits returns every stored split, oldest first.
func (db *DB) Splits(ctx context.Context) ([]marketdata.Split, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT ticker, exec_day, split_from, split_to FROM split ORDER BY exec_day, ticker`)
	if err != nil {
		return nil, fmt.Errorf("store: splits: %w", err)
	}
	defer rows.Close()
	var out []marketdata.Split
	for rows.Next() {
		var s marketdata.Split
		if err := rows.Scan(&s.Ticker, &s.Day, &s.From, &s.To); err != nil {
			return nil, fmt.Errorf("store: splits: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SplitsAsOf returns the splits this system had stored by the time (zero: all),
// oldest first.
func (db *DB) SplitsAsOf(ctx context.Context, known time.Time) ([]marketdata.Split, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !known.IsZero() {
		bound = ms(known)
	}
	rows, err := h.r.QueryContext(ctx, `SELECT ticker, exec_day, split_from, split_to FROM split WHERE first_seen_ms <= ? ORDER BY exec_day, ticker`, bound)
	if err != nil {
		return nil, fmt.Errorf("store: splits: %w", err)
	}
	defer rows.Close()
	var out []marketdata.Split
	for rows.Next() {
		var s marketdata.Split
		if err := rows.Scan(&s.Ticker, &s.Day, &s.From, &s.To); err != nil {
			return nil, fmt.Errorf("store: splits: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SplitCoverageAsOf returns what the readings made by the time (zero: all)
// covered: the earliest day any of them started from and the newest reading. It
// is the zero value when none had been made.
func (db *DB) SplitCoverageAsOf(ctx context.Context, known time.Time) (marketdata.SplitCoverage, error) {
	h, err := db.baseHandle()
	if err != nil {
		return marketdata.SplitCoverage{}, err
	}
	bound := int64(1) << 62
	if !known.IsZero() {
		bound = ms(known)
	}
	var since *string
	var read *int64
	if err := h.r.QueryRowContext(ctx, `SELECT MIN(since_day), MAX(read_ms) FROM split_read WHERE read_ms <= ?`, bound).Scan(&since, &read); err != nil {
		return marketdata.SplitCoverage{}, fmt.Errorf("store: splits: %w", err)
	}
	if since == nil || read == nil {
		return marketdata.SplitCoverage{}, nil
	}
	return marketdata.SplitCoverage{Since: *since, Read: fromMS(*read)}, nil
}
