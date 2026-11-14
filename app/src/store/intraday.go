package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

var (
	_ marketdata.IntradayStore      = (*DB)(nil)
	_ marketdata.IntradayLoadSource = (*DB)(nil)
)

// PutIntradayBars stores intraday bars, replacing those of the same stock, bar
// length and start. The version a different fetch replaces is kept, with when it
// was fetched, so that what was held at a past time can be read back.
func (db *DB) PutIntradayBars(ctx context.Context, bars []marketdata.IntradayBar) error {
	if len(bars) == 0 {
		return nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return fmt.Errorf("store: intraday bars: %w", err)
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO intraday_bar (ticker, interval_s, ts_utc_ms, open, high, low, close, volume, raw, currency, source, fetched_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (ticker, interval_s, ts_utc_ms) DO UPDATE SET open = excluded.open, high = excluded.high, low = excluded.low, close = excluded.close, volume = excluded.volume, raw = excluded.raw, currency = excluded.currency, source = excluded.source, fetched_ms = excluded.fetched_ms`)
	if err != nil {
		return fmt.Errorf("store: intraday bars: %w", err)
	}
	defer st.Close()
	keep, err := tx.PrepareContext(ctx, `INSERT INTO intraday_bar_revision (ticker, interval_s, ts_utc_ms, open, high, low, close, volume, raw, currency, source, fetched_ms, superseded_ms)
		SELECT ticker, interval_s, ts_utc_ms, open, high, low, close, volume, raw, currency, source, fetched_ms, ? FROM intraday_bar
		WHERE ticker = ? AND interval_s = ? AND ts_utc_ms = ? AND (open <> ? OR high <> ? OR low <> ? OR close <> ? OR volume <> ? OR raw <> ? OR currency <> ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return fmt.Errorf("store: intraday bars: %w", err)
	}
	defer keep.Close()
	at := ms(db.now())
	for _, b := range bars {
		if !b.Valid() {
			continue
		}
		secs, start := int64(b.Interval/time.Second), ms(b.Start)
		if _, err := keep.ExecContext(ctx, at, b.Ticker, secs, start, b.Open, b.High, b.Low, b.Close, b.Volume, b.Raw, b.Currency); err != nil {
			return fmt.Errorf("store: intraday bars: %w", err)
		}
		if _, err := st.ExecContext(ctx, b.Ticker, secs, start, b.Open, b.High, b.Low, b.Close, b.Volume, b.Raw, b.Currency, b.Source, at); err != nil {
			return fmt.Errorf("store: intraday bars: %w", err)
		}
	}
	return tx.Commit()
}

// IntradayBars returns the stored bars of one ticker and bar length that start
// between two times (UTC milliseconds, inclusive), oldest first. When known is
// not zero only what this installation had fetched by then is returned, each bar
// as the version held then (an earlier version is read from
// intraday_bar_revision, exactly as CandleSeries does for candles; a bar whose
// fetch time was never recorded is left out).
func (db *DB) IntradayBars(ctx context.Context, ticker string, interval time.Duration, fromMs, toMs int64, known time.Time) ([]marketdata.IntradayBar, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound, floor := int64(1)<<62, int64(-1)
	if !known.IsZero() {
		bound, floor = ms(known), 0
	}
	cols := `ticker, interval_s, ts_utc_ms, open, high, low, close, volume, raw, currency, source, fetched_ms`
	where := ` WHERE ticker = ?1 AND interval_s = ?2 AND ts_utc_ms >= ?3 AND ts_utc_ms <= ?4`
	rows, err := h.r.QueryContext(ctx, `WITH v AS (
			SELECT `+cols+` FROM intraday_bar`+where+`
			UNION ALL
			SELECT `+cols+` FROM intraday_bar_revision`+where+`),
		w AS (SELECT *, ROW_NUMBER() OVER (PARTITION BY ts_utc_ms ORDER BY fetched_ms DESC) AS rv FROM v WHERE fetched_ms <= ?5 AND fetched_ms > ?6)
		SELECT `+cols+` FROM w WHERE rv = 1 ORDER BY ts_utc_ms`, ticker, int64(interval/time.Second), fromMs, toMs, bound, floor)
	if err != nil {
		return nil, fmt.Errorf("store: intraday bars: %w", err)
	}
	defer rows.Close()
	var out []marketdata.IntradayBar
	for rows.Next() {
		var b marketdata.IntradayBar
		var secs, start, fetched int64
		if err := rows.Scan(&b.Ticker, &secs, &start, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume, &b.Raw, &b.Currency, &b.Source, &fetched); err != nil {
			return nil, fmt.Errorf("store: intraday bars: %w", err)
		}
		b.Interval, b.Start = time.Duration(secs)*time.Second, fromMS(start)
		if fetched > 0 {
			b.FetchedAt = fromMS(fetched)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// IntradayChecked returns, by ticker, the days the source has been asked for the
// bars of the length.
func (db *DB) IntradayChecked(ctx context.Context, interval time.Duration) (map[string]map[string]bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT ticker, day FROM intraday_check WHERE interval_s = ?`, int64(interval/time.Second))
	if err != nil {
		return nil, fmt.Errorf("store: intraday checks: %w", err)
	}
	defer rows.Close()
	return scanIntradayChecked(rows)
}

func scanIntradayChecked(rows *sql.Rows) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	for rows.Next() {
		var t, d string
		if err := rows.Scan(&t, &d); err != nil {
			return nil, fmt.Errorf("store: intraday checks: %w", err)
		}
		if out[t] == nil {
			out[t] = map[string]bool{}
		}
		out[t][d] = true
	}
	return out, rows.Err()
}

// PutIntradayChecks records the days the source has been asked for, replacing
// an earlier record of the same ticker, bar length and day.
func (db *DB) PutIntradayChecks(ctx context.Context, checks []marketdata.IntradayCheck) error {
	if len(checks) == 0 {
		return nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return fmt.Errorf("store: intraday checks: %w", err)
	}
	defer tx.Rollback()
	for _, c := range checks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO intraday_check (ticker, interval_s, day, bars, checked_ms) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (ticker, interval_s, day) DO UPDATE SET bars = excluded.bars, checked_ms = excluded.checked_ms`,
			c.Ticker, int64(c.Interval/time.Second), c.Day, c.Bars, ms(c.CheckedAt)); err != nil {
			return fmt.Errorf("store: intraday checks: %w", err)
		}
	}
	return tx.Commit()
}
