package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

const defaultFetchedLimit = 500

// FetchedAfter returns the articles of the allowed sources first fetched after
// the given time, oldest fetch first. A page ends at a fetch time, never inside
// a group of articles that share one, so it may hold more than limit articles.
func (db *DB) FetchedAfter(ctx context.Context, after time.Time, allowed map[string]bool, limit int) ([]model.Article, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	list, ok := allowedJSON(allowed)
	if !ok {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultFetchedLimit
	}
	from := int64(math.MinInt64)
	if !after.IsZero() {
		from = ms(after)
	}

	// The fetch time of the limit-th article closes the page.
	upTo := int64(math.MaxInt64)
	var cut int64
	err = h.r.QueryRowContext(ctx, `SELECT a.fetched_ms FROM articles a WHERE a.fetched_ms > ?`+inSources+
		` ORDER BY a.fetched_ms, a.id LIMIT 1 OFFSET ?`, from, list, limit-1).Scan(&cut)
	switch {
	case err == nil:
		upTo = cut
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("store: fetched after: %w", err)
	}

	q := articleSelect + ` FROM articles a` + latestScoreJoin +
		` WHERE a.fetched_ms > ? AND a.fetched_ms <= ?` + inSources + ` ORDER BY a.fetched_ms ASC, a.id ASC`
	out, err := queryArticles(ctx, h.r, q, from, upTo, list)
	if err != nil {
		return nil, fmt.Errorf("store: fetched after: %w", err)
	}
	return out, nil
}

// ChangedAfter returns the articles of the allowed sources that were inserted or
// changed by a commit after change number after (0 reads from the beginning),
// in the order of the commits, at most limit of them (<= 0 means 500), and the
// change number of the last one (after when there are none). The numbers are
// handed out inside the committing transaction, so an article that commits late
// still gets a number above every earlier one and is never behind the caller's
// position.
func (db *DB) ChangedAfter(ctx context.Context, after int64, allowed map[string]bool, limit int) ([]model.Article, int64, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, after, err
	}
	list, ok := allowedJSON(allowed)
	if !ok {
		return nil, after, nil
	}
	if limit <= 0 {
		limit = defaultFetchedLimit
	}
	q := articleSelect + `, a.change_seq FROM articles a` + latestScoreJoin +
		` WHERE a.change_seq > ?` + inSources + ` ORDER BY a.change_seq ASC LIMIT ?`
	rows, err := h.r.QueryContext(ctx, q, after, list, limit)
	if err != nil {
		return nil, after, fmt.Errorf("store: changed after: %w", err)
	}
	defer rows.Close()
	var out []model.Article
	newest := after
	for rows.Next() {
		var seq int64
		a, err := scanArticle(rows, &seq)
		if err != nil {
			return nil, after, fmt.Errorf("store: changed after: %w", err)
		}
		out = append(out, a)
		newest = seq
	}
	if err := rows.Err(); err != nil {
		return nil, after, fmt.Errorf("store: changed after: %w", err)
	}
	return out, newest, nil
}

// ChangeSeqAt returns the change number just below the first commit that was
// fetched after t (the newest number when none was): reading after it gives
// every article fetched after t. It moves a position kept as a time to the
// numbered order.
func (db *DB) ChangeSeqAt(ctx context.Context, t time.Time) (int64, error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	var first sql.NullInt64
	if err := h.r.QueryRowContext(ctx, `SELECT MIN(change_seq) FROM articles WHERE fetched_ms > ?`, ms(t)).Scan(&first); err != nil {
		return 0, fmt.Errorf("store: change seq at: %w", err)
	}
	if first.Valid {
		return first.Int64 - 1, nil
	}
	var newest sql.NullInt64
	if err := h.r.QueryRowContext(ctx, `SELECT MAX(change_seq) FROM articles`).Scan(&newest); err != nil {
		return 0, fmt.Errorf("store: change seq at: %w", err)
	}
	return newest.Int64, nil
}
