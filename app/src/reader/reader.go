// Package reader is how a tenant reads the shared article base. It holds the
// licence rule in one place: a tenant may only read articles from sources
// visible to it. A Reader is bound to one tenant, its visible sources and its
// time zone when it is created, and every read applies the visibility inside
// the database query, before any limit, so articles from invisible sources
// can neither be returned nor crowd visible ones out of a capped result.
//
// A Reader with no visible source reads nothing. An article of an invisible
// source is reported as not found, exactly like a missing one, so its
// existence does not leak.
//
// Ingest does not use this package; it needs every source and talks to
// package store directly.
package reader

import (
	"context"
	"maps"
	"time"

	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	// BatchSize is how many candidates Search fetches from the store at once.
	BatchSize = 500
	// ScanCap is the most candidates Search looks at before it gives up and
	// reports the result as truncated.
	ScanCap = 20000

	defaultLimit    = 100
	defaultLookback = 30 * 24 * time.Hour
	scanAhead       = 24 * time.Hour
)

// Reader reads the articles one tenant may see. It is safe for concurrent use
// if the store is.
type Reader struct {
	db       store.Store
	tenantID string
	visible  map[string]bool
	zone     *time.Location
	now      func() time.Time
	since    time.Time

	batch, scanCap int // BatchSize and ScanCap; tests make them smaller
}

// Option changes a Reader at creation.
type Option func(*Reader)

// WithNow sets the clock that Search uses to pick its default window. The
// default is time.Now.
func WithNow(now func() time.Time) Option {
	return func(r *Reader) {
		if now != nil {
			r.now = now
		}
	}
}

// WithSince sets the earliest time Search looks at when the query has no
// usable full-text part and the caller gave no start. The default is 30 days
// before the clock.
func WithSince(t time.Time) Option {
	return func(r *Reader) { r.since = t }
}

// New returns a Reader for tenantID that sees the sources that are true in
// visible, with calendar days and after: or before: terms read in zone (nil
// means UTC). The map is copied, and entries that are false are dropped.
func New(db store.Store, tenantID string, visible map[string]bool, zone *time.Location, opts ...Option) *Reader {
	if zone == nil {
		zone = time.UTC
	}
	r := &Reader{
		db: db, tenantID: tenantID, visible: map[string]bool{}, zone: zone,
		now: time.Now, batch: BatchSize, scanCap: ScanCap,
	}
	for id, ok := range visible {
		if ok {
			r.visible[id] = true
		}
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ForTenant is the tenant the Reader is bound to.
func (r *Reader) ForTenant() string { return r.tenantID }

// Visible returns a copy of the visible source IDs.
func (r *Reader) Visible() map[string]bool { return maps.Clone(r.visible) }

// Day returns the visible articles published on the calendar day of day in the
// Reader's zone, newest first.
func (r *Reader) Day(ctx context.Context, day time.Time) ([]model.Article, error) {
	return r.db.DayIn(ctx, day.In(r.zone), r.visible)
}

// Range returns the visible articles published in [from, to), newest first. A
// zero bound leaves that side open.
func (r *Reader) Range(ctx context.Context, from, to time.Time) ([]model.Article, error) {
	return r.db.RangeIn(ctx, from, to, r.visible)
}

// Get returns one visible article. An article of an invisible source gives
// the same error as a missing id, one wrapping store.ErrNotFound.
func (r *Reader) Get(ctx context.Context, id string) (model.Article, error) {
	return r.db.GetIn(ctx, id, r.visible)
}

// Result is the outcome of a Search.
type Result struct {
	// Articles are the matches, newest first, at most the limit.
	Articles []model.Article
	// Truncated is true when the search stopped at the scan cap with
	// candidates left unchecked, so older matches may exist. It is false when
	// the search ended because the limit was reached or the candidates ran out.
	Truncated bool
}

// Search returns up to limit visible articles that satisfy q, newest first
// (limit <= 0 means 100). from and to bound the publication time as [from, to);
// a zero bound is open, except as noted below.
//
// If q has a usable full-text part (filter.Query.FTS), the store narrows the
// candidates with it. Otherwise (the query is only negations and operators)
// every article of the window is a candidate: a zero from means the Since
// option or, without one, 30 days before the clock, and a zero to means 24
// hours after the clock.
//
// Candidates are fetched newest first in batches of BatchSize, already
// restricted to visible sources, and each is checked with q.MatchIn in the
// Reader's zone. The search ends when limit matches are found, the candidates
// run out, or ScanCap candidates have been checked; in the last case with more
// candidates left, Truncated is set.
func (r *Reader) Search(ctx context.Context, q filter.Query, from, to time.Time, limit int) (Result, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if len(r.visible) == 0 {
		return Result{}, nil
	}
	var fetch func(limit, offset int) ([]model.Article, error)
	if match, ok := q.FTS(); ok {
		fetch = func(limit, offset int) ([]model.Article, error) {
			return r.db.SearchIn(ctx, match, from, to, r.visible, limit, offset)
		}
	} else {
		lo, hi := from, to
		if lo.IsZero() {
			lo = r.since
			if lo.IsZero() {
				lo = r.now().Add(-defaultLookback)
			}
		}
		if hi.IsZero() {
			hi = r.now().Add(scanAhead)
		}
		fetch = func(limit, offset int) ([]model.Article, error) {
			return r.db.RangePageIn(ctx, lo, hi, r.visible, limit, offset)
		}
	}

	var res Result
	seen := map[string]bool{} // articles saved during the scan can shift a page
	scanned := 0
	for scanned < r.scanCap {
		n := min(r.batch, r.scanCap-scanned)
		batch, err := fetch(n, scanned)
		if err != nil {
			return Result{}, err
		}
		for _, a := range batch {
			if seen[a.ID] {
				continue
			}
			seen[a.ID] = true
			if q.MatchIn(a, r.zone) {
				res.Articles = append(res.Articles, a)
				if len(res.Articles) == limit {
					return res, nil
				}
			}
		}
		scanned += len(batch)
		if len(batch) < n {
			return res, nil
		}
	}
	more, err := fetch(1, scanned)
	if err != nil {
		return Result{}, err
	}
	res.Truncated = len(more) > 0
	return res, nil
}
