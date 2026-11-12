package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

// candleSeriesChunk keeps the ticker list of one query well below SQLite's
// limit on parameters.
const candleSeriesChunk = 500

// CandleSeries returns the stored daily candles of the tickers between two days
// (YYYY-MM-DD, inclusive), oldest first, by ticker. When known is not zero only
// what this installation had fetched by then is returned, each candle as the
// version held then (an earlier version is read from candle_revision, exactly as
// CandlesAsOf does; a candle whose fetch time was never recorded is left out).
// An empty tickers list returns every ticker; from or to may be empty for an
// open end.
func (db *DB) CandleSeries(ctx context.Context, tickers []string, from, to string, known time.Time) (map[string][]marketdata.Candle, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	out := map[string][]marketdata.Candle{}
	run := func(chunk []string) error {
		bound, floor := int64(1)<<62, int64(-1)
		if !known.IsZero() {
			bound, floor = ms(known), 0
		}
		args := []any{from, to, bound, floor}
		filter := ""
		if len(chunk) > 0 {
			marks := make([]string, len(chunk))
			for i, t := range chunk {
				marks[i] = fmt.Sprintf("?%d", i+5)
				args = append(args, t)
			}
			filter = " AND ticker IN (" + strings.Join(marks, ",") + ")"
		}
		cols := `ticker, day, open, high, low, close, volume, raw, currency, fetched_ms`
		where := ` WHERE (?1 = '' OR day >= ?1) AND (?2 = '' OR day <= ?2)` + filter
		q := `WITH v AS (
				SELECT ` + cols + ` FROM candle` + where + `
				UNION ALL
				SELECT ` + cols + ` FROM candle_revision` + where + `),
			w AS (SELECT *, ROW_NUMBER() OVER (PARTITION BY ticker, day ORDER BY fetched_ms DESC) AS rv FROM v WHERE fetched_ms <= ?3 AND fetched_ms > ?4)
			SELECT ` + cols + ` FROM w WHERE rv = 1 ORDER BY ticker, day`
		rows, err := h.r.QueryContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("store: candles: %w", err)
		}
		defer rows.Close()
		got, err := scanCandles(rows)
		if err != nil {
			return err
		}
		for t, cs := range got {
			out[t] = append(out[t], cs...)
		}
		return nil
	}
	if len(tickers) == 0 {
		if err := run(nil); err != nil {
			return nil, err
		}
		return out, nil
	}
	seen := map[string]bool{}
	var uniq []string
	for _, t := range tickers {
		if !seen[t] {
			seen[t] = true
			uniq = append(uniq, t)
		}
	}
	tickers = uniq
	for i := 0; i < len(tickers); i += candleSeriesChunk {
		if err := run(tickers[i:min(i+candleSeriesChunk, len(tickers))]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
