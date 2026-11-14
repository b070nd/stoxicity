package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
)

// Kinds of stock feed.
const (
	FeedSEC  = "sec"  // the company's filing record at the SEC
	FeedSite = "site" // a news or investor-relations feed found on its website
)

// StockFeed is one feed of one stock, with the state its next poll needs.
type StockFeed struct {
	Ticker string
	Kind   string
	URL    string
	Title  string
	// FoundAt is when the feed was first recorded.
	FoundAt time.Time
	State   feeds.State
	// NextDue is when the feed is due to be polled; the zero time means now.
	NextDue   time.Time
	LastOK    time.Time
	LastNew   int
	Failures  int
	LastError string
}

// PutStockFeeds records feeds that are not yet known and refreshes the title of
// those that are, leaving their fetch state and schedule alone. It returns how
// many were new.
func (db *DB) PutStockFeeds(ctx context.Context, list []StockFeed, at time.Time) (int, error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: stock feeds: %w", err)
	}
	defer tx.Rollback()
	added := 0
	for _, f := range list {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM stock_feed WHERE ticker = ? AND kind = ? AND url = ?`,
			f.Ticker, f.Kind, f.URL).Scan(&one)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `INSERT INTO stock_feed (ticker, kind, url, title, found_ms) VALUES (?, ?, ?, ?, ?)`,
				f.Ticker, f.Kind, f.URL, f.Title, ms(at))
			added++
		case err == nil:
			_, err = tx.ExecContext(ctx, `UPDATE stock_feed SET title = ? WHERE ticker = ? AND kind = ? AND url = ?`,
				f.Title, f.Ticker, f.Kind, f.URL)
		}
		if err != nil {
			return 0, fmt.Errorf("store: stock feeds: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: stock feeds: %w", err)
	}
	return added, nil
}

const stockFeedSelect = `SELECT ticker, kind, url, title, found_ms, state, next_due_ms, last_ok_ms, last_new, failures, last_error FROM stock_feed`

func scanStockFeed(r scanner) (StockFeed, error) {
	var f StockFeed
	var found, due, ok int64
	var state string
	if err := r.Scan(&f.Ticker, &f.Kind, &f.URL, &f.Title, &found, &state, &due, &ok, &f.LastNew, &f.Failures, &f.LastError); err != nil {
		return StockFeed{}, err
	}
	if err := json.Unmarshal([]byte(state), &f.State); err != nil {
		return StockFeed{}, fmt.Errorf("store: stock feed %s %s: %w", f.Ticker, f.URL, err)
	}
	if found > 0 {
		f.FoundAt = fromMS(found)
	}
	if due > 0 {
		f.NextDue = fromMS(due)
	}
	if ok > 0 {
		f.LastOK = fromMS(ok)
	}
	return f, nil
}

func (db *DB) queryStockFeeds(ctx context.Context, where string, args ...any) ([]StockFeed, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, stockFeedSelect+where, args...)
	if err != nil {
		return nil, fmt.Errorf("store: stock feeds: %w", err)
	}
	defer rows.Close()
	var out []StockFeed
	for rows.Next() {
		f, err := scanStockFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DueStockFeeds returns up to limit feeds of a kind whose time has come, the
// longest waiting first.
func (db *DB) DueStockFeeds(ctx context.Context, kind string, now time.Time, limit int) ([]StockFeed, error) {
	return db.queryStockFeeds(ctx, ` WHERE kind = ? AND next_due_ms <= ? ORDER BY next_due_ms, ticker LIMIT ?`,
		kind, ms(now), limit)
}

// StockFeedsOf returns the feeds of one stock.
func (db *DB) StockFeedsOf(ctx context.Context, ticker string) ([]StockFeed, error) {
	return db.queryStockFeeds(ctx, ` WHERE ticker = ? ORDER BY kind, url`, ticker)
}

// SaveStockFeed stores the state and schedule of a feed after a poll.
func (db *DB) SaveStockFeed(ctx context.Context, f StockFeed) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	state, err := json.Marshal(f.State)
	if err != nil {
		return fmt.Errorf("store: stock feed %s: %w", f.Ticker, err)
	}
	_, err = h.w.ExecContext(ctx, `UPDATE stock_feed SET state = ?, next_due_ms = ?, last_ok_ms = ?, last_new = ?,
		failures = ?, last_error = ? WHERE ticker = ? AND kind = ? AND url = ?`,
		string(state), msOrZero(f.NextDue), msOrZero(f.LastOK), f.LastNew, f.Failures, truncate(f.LastError, 300), f.Ticker, f.Kind, f.URL)
	if err != nil {
		return fmt.Errorf("store: stock feed %s: %w", f.Ticker, err)
	}
	return nil
}

// StockFeedStat counts the feeds of one kind.
type StockFeedStat struct {
	Feeds   int // known
	Stocks  int // stocks that have at least one
	Polled  int // polled at least once with success
	Failing int // failed their last poll
}

// StockFeedStats counts the feeds by kind.
func (db *DB) StockFeedStats(ctx context.Context) (map[string]StockFeedStat, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT kind, COUNT(*), COUNT(DISTINCT ticker),
		SUM(CASE WHEN last_ok_ms > 0 THEN 1 ELSE 0 END), SUM(CASE WHEN failures > 0 THEN 1 ELSE 0 END) FROM stock_feed GROUP BY kind`)
	if err != nil {
		return nil, fmt.Errorf("store: stock feeds: %w", err)
	}
	defer rows.Close()
	out := map[string]StockFeedStat{}
	for rows.Next() {
		var kind string
		var s StockFeedStat
		if err := rows.Scan(&kind, &s.Feeds, &s.Stocks, &s.Polled, &s.Failing); err != nil {
			return nil, fmt.Errorf("store: stock feeds: %w", err)
		}
		out[kind] = s
	}
	return out, rows.Err()
}

// StockFeedSearch is where the search for one stock's feed has been.
type StockFeedSearch struct {
	CheckedAt time.Time
	Found     int
	Error     string
}

// StockFeedSearches returns the searches made, by ticker.
func (db *DB) StockFeedSearches(ctx context.Context) (map[string]StockFeedSearch, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT ticker, checked_ms, found, error FROM stock_feed_search`)
	if err != nil {
		return nil, fmt.Errorf("store: stock feed searches: %w", err)
	}
	defer rows.Close()
	out := map[string]StockFeedSearch{}
	for rows.Next() {
		var t string
		var s StockFeedSearch
		var at int64
		if err := rows.Scan(&t, &at, &s.Found, &s.Error); err != nil {
			return nil, fmt.Errorf("store: stock feed searches: %w", err)
		}
		s.CheckedAt = fromMS(at)
		out[t] = s
	}
	return out, rows.Err()
}

// SaveStockFeedSearch records that the search for a stock's feeds was made.
func (db *DB) SaveStockFeedSearch(ctx context.Context, ticker string, s StockFeedSearch) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	_, err = h.w.ExecContext(ctx, `INSERT INTO stock_feed_search (ticker, checked_ms, found, error) VALUES (?, ?, ?, ?)
		ON CONFLICT (ticker) DO UPDATE SET checked_ms = excluded.checked_ms, found = excluded.found, error = excluded.error`,
		ticker, ms(s.CheckedAt), s.Found, truncate(s.Error, 300))
	if err != nil {
		return fmt.Errorf("store: stock feed search %s: %w", ticker, err)
	}
	return nil
}

// msOrZero is t in milliseconds, 0 for the zero time (never).
func msOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return ms(t)
}
