package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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
	st, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO candle (ticker, day, open, high, low, close, volume, raw, currency, fetched_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: candles: %w", err)
	}
	defer st.Close()
	// The version a different fetch replaces is kept, with when it was fetched.
	keep, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO candle_revision (ticker, day, open, high, low, close, volume, raw, currency, fetched_ms, superseded_ms)
		SELECT ticker, day, open, high, low, close, volume, raw, currency, fetched_ms, ? FROM candle
		WHERE ticker = ? AND day = ? AND (open <> ? OR high <> ? OR low <> ? OR close <> ? OR volume <> ? OR raw <> ? OR currency <> ?)`)
	if err != nil {
		return fmt.Errorf("store: candles: %w", err)
	}
	defer keep.Close()
	at := ms(db.now())
	for _, c := range rows {
		if !c.Valid() {
			continue
		}
		if _, err := keep.ExecContext(ctx, at, c.Ticker, c.Day, c.Open, c.High, c.Low, c.Close, c.Volume, c.Raw, c.Currency); err != nil {
			return fmt.Errorf("store: candles: %w", err)
		}
		if _, err := st.ExecContext(ctx, c.Ticker, c.Day, c.Open, c.High, c.Low, c.Close, c.Volume, c.Raw, c.Currency, at); err != nil {
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
	rows, err := h.r.QueryContext(ctx, `SELECT ticker, day, open, high, low, close, volume, raw, currency, fetched_ms FROM
		(SELECT *, ROW_NUMBER() OVER (PARTITION BY ticker ORDER BY day DESC) AS n FROM candle) WHERE n <= 2 ORDER BY ticker, day DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: candles: %w", err)
	}
	defer rows.Close()
	return scanCandles(rows)
}

func scanCandles(rows *sql.Rows) (map[string][]marketdata.Candle, error) {
	out := map[string][]marketdata.Candle{}
	for rows.Next() {
		var c marketdata.Candle
		var fetched int64
		if err := rows.Scan(&c.Ticker, &c.Day, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.Raw, &c.Currency, &fetched); err != nil {
			return nil, fmt.Errorf("store: candles: %w", err)
		}
		if fetched > 0 {
			c.FetchedAt = fromMS(fetched)
		}
		out[c.Ticker] = append(out[c.Ticker], c)
	}
	return out, rows.Err()
}

// CandlesAsOf returns, for each stock, its newest candle that had been public by
// the time, and the one before, newest first. A session is public from the end of
// its day (UTC): the earliest moment at which it is certain to have closed. When
// known is not zero, only what this system had fetched by then counts, each
// candle as the version held then (an earlier version a later fetch replaced is
// read from candle_revision); a candle whose fetch time was never recorded
// (fetched_ms 0) cannot be shown to have been held, and is left out.
func (db *DB) CandlesAsOf(ctx context.Context, at time.Time, known time.Time) (map[string][]marketdata.Candle, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	// The last session whose day had ended by at.
	lastDay := at.UTC().Add(-24 * time.Hour).Format(time.DateOnly)
	bound := int64(1) << 62
	floor := int64(-1)
	if !known.IsZero() {
		bound, floor = ms(known), 0
	}
	rows, err := h.r.QueryContext(ctx, `WITH v AS (
			SELECT ticker, day, open, high, low, close, volume, raw, currency, fetched_ms FROM candle WHERE day <= ?1
			UNION ALL
			SELECT ticker, day, open, high, low, close, volume, raw, currency, fetched_ms FROM candle_revision WHERE day <= ?1),
		w AS (SELECT *, ROW_NUMBER() OVER (PARTITION BY ticker, day ORDER BY fetched_ms DESC) AS rv FROM v WHERE fetched_ms <= ?2 AND fetched_ms > ?3),
		x AS (SELECT *, ROW_NUMBER() OVER (PARTITION BY ticker ORDER BY day DESC) AS n FROM w WHERE rv = 1)
		SELECT ticker, day, open, high, low, close, volume, raw, currency, fetched_ms FROM x WHERE n <= 2 ORDER BY ticker, day DESC`, lastDay, bound, floor)
	if err != nil {
		return nil, fmt.Errorf("store: candles: %w", err)
	}
	defer rows.Close()
	return scanCandles(rows)
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

// RawDays returns the days for which raw candles are stored.
func (db *DB) RawDays(ctx context.Context) (map[string]bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT DISTINCT day FROM candle WHERE raw = 1`)
	if err != nil {
		return nil, fmt.Errorf("store: candles: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("store: candles: %w", err)
		}
		out[d] = true
	}
	return out, rows.Err()
}
