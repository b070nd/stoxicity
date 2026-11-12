package store

import (
	"context"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

// MonthlyCandles reads the last stored session of each month and ticker in a
// bounded range. One bulk query serves the catalogue, not one query per stock.
func (db *DB) MonthlyCandles(ctx context.Context, from, through string) (map[string][]marketdata.Candle, error) {
	return db.CalendarCandles(ctx, from, from, through)
}

// CalendarCandles reads yearly endpoints for older history and monthly endpoints
// from monthlyFrom on, in one query. Both strips can share this bounded snapshot.
func (db *DB) CalendarCandles(ctx context.Context, from, monthlyFrom, through string) (map[string][]marketdata.Candle, error) {
	if _, err := time.Parse(time.DateOnly, from); err != nil {
		return nil, err
	}
	if _, err := time.Parse(time.DateOnly, through); err != nil {
		return nil, err
	}
	if _, err := time.Parse(time.DateOnly, monthlyFrom); err != nil {
		return nil, err
	}
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `WITH ends AS (
		SELECT ticker, MAX(day) AS day FROM candle WHERE day >= ?1 AND day <= ?3
		GROUP BY ticker, CASE WHEN day >= ?2 THEN substr(day,1,7) ELSE substr(day,1,4) END
	) SELECT c.ticker,c.day,c.open,c.high,c.low,c.close,c.volume,c.raw,c.currency,c.fetched_ms
	FROM candle c JOIN ends e ON c.ticker=e.ticker AND c.day=e.day ORDER BY c.ticker,c.day`, from, monthlyFrom, through)
	if err != nil {
		return nil, fmt.Errorf("store: calendar candles: %w", err)
	}
	defer rows.Close()
	return scanCandles(rows)
}
