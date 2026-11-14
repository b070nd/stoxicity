package store

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

var _ marketdata.HistoryStore = (*DB)(nil)

func (db *DB) HistoryStates(ctx context.Context) (map[string]marketdata.HistoryState, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT s.ticker,s.checked_ms,s.fetched_ms,s.lease_until_ms,s.problem,(SELECT COUNT(*) FROM price_history_month m WHERE m.ticker=s.ticker) FROM price_history_state s`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]marketdata.HistoryState{}
	for rows.Next() {
		var s marketdata.HistoryState
		var checked, fetched, lease int64
		if err := rows.Scan(&s.Ticker, &checked, &fetched, &lease, &s.Problem, &s.Months); err != nil {
			return nil, err
		}
		if checked != 0 {
			s.CheckedAt = fromMS(checked)
		}
		if fetched != 0 {
			s.FetchedAt = fromMS(fetched)
		}
		if lease != 0 {
			s.LeaseUntil = fromMS(lease)
		}
		out[s.Ticker] = s
	}
	return out, rows.Err()
}

func (db *DB) ClaimHistory(ctx context.Context, ticker string, checked, now time.Time) (bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return false, err
	}
	var previous int64
	if !checked.IsZero() {
		previous = ms(checked)
	}
	res, err := h.w.ExecContext(ctx, `INSERT INTO price_history_state(ticker,lease_until_ms) VALUES(?,?)
		ON CONFLICT(ticker) DO UPDATE SET lease_until_ms=excluded.lease_until_ms WHERE price_history_state.lease_until_ms<=? AND price_history_state.checked_ms<=?`, ticker, ms(now.Add(5*time.Minute)), ms(now), previous)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (db *DB) PutPriceHistory(ctx context.Context, history marketdata.PriceHistory) error {
	if history.Ticker == "" || history.Symbol == "" || history.Currency == "" || history.FetchedAt.IsZero() || len(history.Months) == 0 {
		return fmt.Errorf("history: invalid snapshot")
	}
	if _, err := time.Parse(time.DateOnly, history.LatestDay); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, month := range history.Months {
		if _, err := time.Parse("2006-01", month.Month); err != nil {
			return err
		}
		if seen[month.Month] || month.Month > history.LatestDay[:7] || month.Close <= 0 || math.IsNaN(month.Close) || math.IsInf(month.Close, 0) {
			return fmt.Errorf("history: invalid or repeated monthly close")
		}
		seen[month.Month] = true
	}
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousDay string
	var previousFetch int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(latest_day),''),COALESCE(MAX(fetched_ms),0) FROM price_history_state WHERE ticker=?`, history.Ticker).Scan(&previousDay, &previousFetch); err != nil {
		return err
	}
	if history.LatestDay < previousDay || ms(history.FetchedAt) < previousFetch {
		return fmt.Errorf("history: older snapshot refused; previous history retained")
	}
	// A truncated refresh must not replace previously known observations inside
	// the returned window, nor mix stale pre-split values with the new snapshot.
	first := fmt.Sprintf("%04d-01", history.FetchedAt.Year()-10)
	rows, err := tx.QueryContext(ctx, `SELECT month FROM price_history_month WHERE ticker=? AND month>=?`, history.Ticker, first)
	if err != nil {
		return err
	}
	for rows.Next() {
		var month string
		if err := rows.Scan(&month); err != nil {
			rows.Close()
			return err
		}
		if !seen[month] {
			rows.Close()
			return fmt.Errorf("history: incomplete refresh; previous snapshot retained")
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	_, err = tx.ExecContext(ctx, `INSERT INTO price_history_state(ticker,symbol,currency,latest_day,checked_ms,fetched_ms) VALUES(?,?,?,?,?,?)
		ON CONFLICT(ticker) DO UPDATE SET symbol=excluded.symbol,currency=excluded.currency,latest_day=excluded.latest_day,checked_ms=excluded.checked_ms,fetched_ms=excluded.fetched_ms,lease_until_ms=0,problem=''`, history.Ticker, history.Symbol, history.Currency, history.LatestDay, ms(history.FetchedAt), ms(history.FetchedAt))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM price_history_month WHERE ticker=?`, history.Ticker); err != nil {
		return err
	}
	for _, month := range history.Months {
		if _, err := tx.ExecContext(ctx, `INSERT INTO price_history_month(ticker,month,close) VALUES(?,?,?)`, history.Ticker, month.Month, month.Close); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) HistoryFailure(ctx context.Context, ticker string, at time.Time, problem string) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	var checked int64
	if !at.IsZero() {
		checked = ms(at)
	}
	_, err = h.w.ExecContext(ctx, `INSERT INTO price_history_state(ticker,checked_ms,problem) VALUES(?,?,?) ON CONFLICT(ticker) DO UPDATE SET checked_ms=excluded.checked_ms,lease_until_ms=0,problem=excluded.problem`, ticker, checked, problem)
	return err
}

// CalendarHistories selects only the endpoints needed by the two UI strips.
func (db *DB) CalendarHistories(ctx context.Context, from, monthlyFrom, through string) (map[string]marketdata.PriceHistory, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `WITH ends AS (
		SELECT ticker,MAX(month) AS month FROM price_history_month WHERE month>=?1 AND month<=?3
		GROUP BY ticker,CASE WHEN month>=?2 THEN month ELSE substr(month,1,4) END)
		SELECT s.ticker,s.symbol,s.currency,s.latest_day,s.fetched_ms,m.month,m.close FROM ends e JOIN price_history_month m ON m.ticker=e.ticker AND m.month=e.month JOIN price_history_state s ON s.ticker=e.ticker ORDER BY s.ticker,m.month`, from, monthlyFrom, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]marketdata.PriceHistory{}
	for rows.Next() {
		var snapshot marketdata.PriceHistory
		var month marketdata.HistoryMonth
		var fetched int64
		if err := rows.Scan(&snapshot.Ticker, &snapshot.Symbol, &snapshot.Currency, &snapshot.LatestDay, &fetched, &month.Month, &month.Close); err != nil {
			return nil, err
		}
		previous := out[snapshot.Ticker]
		snapshot.Months = append(previous.Months, month)
		snapshot.FetchedAt = fromMS(fetched)
		out[snapshot.Ticker] = snapshot
	}
	return out, rows.Err()
}
