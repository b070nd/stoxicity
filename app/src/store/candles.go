package store

import (
	"context"
	"fmt"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

var _ marketdata.Store = (*DB)(nil)

// PutCandles stores candles, replacing those of the same stock and day.
func (db *DB) PutCandles(ctx context.Context, rows []marketdata.Candle) error {
	if len(rows) == 0 {
		return nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: candles: %w", err)
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO candle (ticker, day, open, high, low, close, volume) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: candles: %w", err)
	}
	defer st.Close()
	for _, c := range rows {
		if !c.Valid() {
			continue
		}
		if _, err := st.ExecContext(ctx, c.Ticker, c.Day, c.Open, c.High, c.Low, c.Close, c.Volume); err != nil {
			return fmt.Errorf("store: candles: %w", err)
		}
	}
	return tx.Commit()
}

// NewestDays returns the newest stored day of each ticker.
func (db *DB) NewestDays(ctx context.Context) (map[string]string, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT ticker, MAX(day) FROM candle GROUP BY ticker`)
	if err != nil {
		return nil, fmt.Errorf("store: candles: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var t, d string
		if err := rows.Scan(&t, &d); err != nil {
			return nil, fmt.Errorf("store: candles: %w", err)
		}
		out[t] = d
	}
	return out, rows.Err()
}

// LatestCandles returns, for each stock, its newest candle and the one before,
// newest first: what a list needs to show the day and the move from the day
// before.
func (db *DB) LatestCandles(ctx context.Context) (map[string][]marketdata.Candle, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT ticker, day, open, high, low, close, volume FROM
		(SELECT *, ROW_NUMBER() OVER (PARTITION BY ticker ORDER BY day DESC) AS n FROM candle) WHERE n <= 2 ORDER BY ticker, day DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: candles: %w", err)
	}
	defer rows.Close()
	out := map[string][]marketdata.Candle{}
	for rows.Next() {
		var c marketdata.Candle
		if err := rows.Scan(&c.Ticker, &c.Day, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
			return nil, fmt.Errorf("store: candles: %w", err)
		}
		out[c.Ticker] = append(out[c.Ticker], c)
	}
	return out, rows.Err()
}

// Checks returns where the look-up of each stock (or market day) has been.
func (db *DB) Checks(ctx context.Context) (map[string]marketdata.Check, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT ticker, checked_ms, found, error FROM candle_check`)
	if err != nil {
		return nil, fmt.Errorf("store: candles: %w", err)
	}
	defer rows.Close()
	out := map[string]marketdata.Check{}
	for rows.Next() {
		var t string
		var c marketdata.Check
		var at int64
		if err := rows.Scan(&t, &at, &c.Found, &c.Error); err != nil {
			return nil, fmt.Errorf("store: candles: %w", err)
		}
		c.CheckedAt = fromMS(at)
		out[t] = c
	}
	return out, rows.Err()
}

// SaveChecks records the look-ups.
func (db *DB) SaveChecks(ctx context.Context, checks map[string]marketdata.Check) error {
	if len(checks) == 0 {
		return nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: candles: %w", err)
	}
	defer tx.Rollback()
	for t, c := range checks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO candle_check (ticker, checked_ms, found, error) VALUES (?, ?, ?, ?)
			ON CONFLICT (ticker) DO UPDATE SET checked_ms = excluded.checked_ms, found = excluded.found, error = excluded.error`,
			t, ms(c.CheckedAt), c.Found, truncate(c.Error, 300)); err != nil {
			return fmt.Errorf("store: candles: %w", err)
		}
	}
	return tx.Commit()
}

// CandleStats counts the stored candles: stocks that have any, and the newest day.
func (db *DB) CandleStats(ctx context.Context) (stocks int, newest string, err error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, "", err
	}
	var n *string
	err = h.r.QueryRowContext(ctx, `SELECT COUNT(DISTINCT ticker), MAX(day) FROM candle`).Scan(&stocks, &n)
	if n != nil {
		newest = *n
	}
	return stocks, newest, err
}
