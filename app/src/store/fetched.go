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
