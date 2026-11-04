package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// The *In methods are the source-filtered reads of the shared base. Each takes
// the set of allowed source IDs and restricts the SQL itself to those sources,
// so the restriction happens before any LIMIT or OFFSET and a row from another
// source can never take the place of an allowed one.
//
// An allowed set is a map whose keys with the value true are the allowed IDs
// (the shape of sources.Registry.VisibleTo); keys with the value false are
// ignored. An EMPTY set, or one with no true value, allows NO source: the
// methods return nothing and never fall back to "all". The set may have any
// size; it travels as one JSON parameter read with json_each, so SQLite's
// limit on bound variables does not apply.
//
// The unfiltered Day, Range, Get, Search and Has stay for ingest, which must
// see every source.

// allowedJSON returns the allowed IDs as a sorted JSON array, and false when
// the set allows nothing.
func allowedJSON(allowed map[string]bool) (string, bool) {
	ids := make([]string, 0, len(allowed))
	for id, ok := range allowed {
		if ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "", false
	}
	sort.Strings(ids)
	b, _ := json.Marshal(ids) // a []string always marshals
	return string(b), true
}

// inSources is the SQL condition on the article table alias a.
const inSources = ` AND a.source_id IN (SELECT value FROM json_each(?))`

// DayIn is Day restricted to the allowed sources. The day is the calendar day
// of day in day's own location.
func (db *DB) DayIn(ctx context.Context, day time.Time, allowed map[string]bool) ([]model.Article, error) {
	y, m, d := day.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, day.Location())
	return db.RangeIn(ctx, start, start.AddDate(0, 0, 1), allowed)
}

// RangeIn is Range restricted to the allowed sources: the articles published
// in [from, to), newest first, then by ID. A zero from or to leaves that side
// open.
func (db *DB) RangeIn(ctx context.Context, from, to time.Time, allowed map[string]bool) ([]model.Article, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	list, ok := allowedJSON(allowed)
	if !ok {
		return nil, nil
	}
	q, args := windowQuery(articleSelect+` FROM articles a`+latestScoreJoin+` WHERE 1 = 1`, nil, from, to, list)
	out, err := queryArticles(ctx, h.r, q+articleOrder, args...)
	if err != nil {
		return nil, fmt.Errorf("store: range: %w", err)
	}
	return out, nil
}

// RangePageIn is RangeIn cut into pages: it skips the first offset articles of
// the ordered result and returns at most limit (limit <= 0 means 100). A short
// page means the result is exhausted. The order is newest first, then by ID, so
// pages do not overlap unless articles are saved between two calls.
func (db *DB) RangePageIn(ctx context.Context, from, to time.Time, allowed map[string]bool, limit, offset int) ([]model.Article, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	list, ok := allowedJSON(allowed)
	if !ok {
		return nil, nil
	}
	q, args := windowQuery(articleSelect+` FROM articles a`+latestScoreJoin+` WHERE 1 = 1`, nil, from, to, list)
	q, args = page(q+articleOrder, args, limit, offset)
	out, err := queryArticles(ctx, h.r, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: range: %w", err)
	}
	return out, nil
}

// GetIn is Get restricted to the allowed sources. An article of another
// source gives exactly the error of a missing id, an error wrapping
// ErrNotFound, so the caller cannot tell the two apart.
func (db *DB) GetIn(ctx context.Context, id string, allowed map[string]bool) (model.Article, error) {
	h, err := db.baseHandle()
	if err != nil {
		return model.Article{}, err
	}
	list, ok := allowedJSON(allowed)
	if !ok {
		return model.Article{}, fmt.Errorf("%w: article %q", ErrNotFound, id)
	}
	q := articleSelect + ` FROM articles a` + latestScoreJoin + ` WHERE a.id = ?` + inSources
	out, err := queryArticles(ctx, h.r, q, id, list)
	if err != nil {
		return model.Article{}, fmt.Errorf("store: get: %w", err)
	}
	if len(out) == 0 {
		return model.Article{}, fmt.Errorf("%w: article %q", ErrNotFound, id)
	}
	return out[0], nil
}

// SearchIn is Search restricted to the allowed sources, with paging. The
// source restriction is applied first, then the newest-first order, then offset
// articles are skipped and at most limit (limit <= 0 means 100) returned. A
// short page means the matches are exhausted. An empty or malformed match
// expression is an error, even when the allowed set is empty.
func (db *DB) SearchIn(ctx context.Context, match string, from, to time.Time, allowed map[string]bool, limit, offset int) ([]model.Article, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(match) == "" {
		return nil, errors.New("store: search: empty match expression")
	}
	list, ok := allowedJSON(allowed)
	if !ok {
		return nil, nil
	}
	q, args := windowQuery(articleSelect+` FROM articles_fts JOIN articles a ON a.seq = articles_fts.rowid`+
		latestScoreJoin+` WHERE articles_fts MATCH ?`, []any{match}, from, to, list)
	q, args = page(q+articleOrder, args, limit, offset)
	out, err := queryArticles(ctx, h.r, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: search: %w", err)
	}
	return out, nil
}

// HasIn is Has restricted to the allowed sources: an id stored under another
// source is reported as absent.
func (db *DB) HasIn(ctx context.Context, ids []string, allowed map[string]bool) (map[string]bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	found := make(map[string]bool, len(ids))
	list, ok := allowedJSON(allowed)
	if !ok || len(ids) == 0 {
		return found, nil
	}
	idList, _ := json.Marshal(ids) // a []string always marshals
	rows, err := h.r.QueryContext(ctx, `SELECT a.id FROM articles a
		WHERE a.id IN (SELECT value FROM json_each(?))`+inSources, string(idList), list)
	if err != nil {
		return nil, fmt.Errorf("store: has: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: has: %w", err)
		}
		found[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: has: %w", err)
	}
	return found, nil
}

// windowQuery appends the time window and the source condition to a query that
// ends in a WHERE clause. args are the arguments already bound.
func windowQuery(q string, args []any, from, to time.Time, list string) (string, []any) {
	if !from.IsZero() {
		q += ` AND a.published_ms >= ?`
		args = append(args, ms(from))
	}
	if !to.IsZero() {
		q += ` AND a.published_ms < ?`
		args = append(args, ms(to))
	}
	return q + inSources, append(args, list)
}

// page appends LIMIT and OFFSET.
func page(q string, args []any, limit, offset int) (string, []any) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	return q + ` LIMIT ? OFFSET ?`, append(args, limit, max(offset, 0))
}
