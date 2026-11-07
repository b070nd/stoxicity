// Package store persists the information base and the tenants' data. It is the
// only package that talks to the database.
//
// Two kinds of data, kept apart on purpose:
//
//   - Base data is shared by every tenant: articles, clusters, entities and
//     base scores. Only ingest (package pipeline) writes it.
//   - Tenant data belongs to one tenant: labels, alert hits, read markers and
//     the state of the hit ledger (evaluation cursors, delivered alerts, sent
//     digests). It is reachable only through a TenantStore, which is bound to
//     one tenant when it is created, so a query cannot forget the tenant filter.
package store

import (
	"context"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/scheduler"
	"github.com/b070nd/stoxicity/app/src/watch"
)

// Base is the shared information base.
type Base interface {
	// Save inserts or updates articles, deduplicated by Article.ID. A score is
	// stored with its lens version and never overwrites a different version.
	Save(ctx context.Context, articles []model.Article) error
	// Day returns the articles published on the given day.
	Day(ctx context.Context, day time.Time) ([]model.Article, error)
	// Range returns the articles published in [from, to).
	Range(ctx context.Context, from, to time.Time) ([]model.Article, error)
	// Get returns one article. A missing id gives an error wrapping ErrNotFound.
	Get(ctx context.Context, id string) (model.Article, error)
	// Search returns the articles matching an FTS5 MATCH expression over
	// title, summary and tickers, published in [from, to) (a zero bound is
	// open), newest first. limit <= 0 means 100. An empty or malformed
	// expression is an error.
	Search(ctx context.Context, match string, from, to time.Time, limit int) ([]model.Article, error)
	// Has reports which of ids are stored, for deduplication. Stored ids map
	// to true; others are absent.
	Has(ctx context.Context, ids []string) (map[string]bool, error)

	// The *In reads are the same reads restricted to a set of allowed source
	// IDs (keys with the value true), applied inside the query before any
	// limit. An empty set allows NO source and yields nothing, never "all".
	// This is the tenant licence rule: readers of tenant-facing data use these
	// (through package reader), ingest uses the unfiltered methods above.

	// DayIn is Day restricted to the allowed sources.
	DayIn(ctx context.Context, day time.Time, allowed map[string]bool) ([]model.Article, error)
	// RangeIn is Range restricted to the allowed sources; a zero bound is open.
	RangeIn(ctx context.Context, from, to time.Time, allowed map[string]bool) ([]model.Article, error)
	// RangePageIn is RangeIn with limit and offset (limit <= 0 means 100).
	RangePageIn(ctx context.Context, from, to time.Time, allowed map[string]bool, limit, offset int) ([]model.Article, error)
	// GetIn is Get restricted to the allowed sources. An article of another
	// source gives the same ErrNotFound error as a missing one.
	GetIn(ctx context.Context, id string, allowed map[string]bool) (model.Article, error)
	// SearchIn is Search restricted to the allowed sources, applying limit and
	// offset after the restriction, so a page of results is a page of allowed
	// articles only.
	SearchIn(ctx context.Context, match string, from, to time.Time, allowed map[string]bool, limit, offset int) ([]model.Article, error)
	// HasIn is Has restricted to the allowed sources.
	HasIn(ctx context.Context, ids []string, allowed map[string]bool) (map[string]bool, error)
	// FetchedAfter returns the articles of the allowed sources that were first
	// fetched after the given time (a zero time means all), oldest fetch first,
	// then by ID. It is how the hit ledger finds what is new since it last
	// looked: publication times say nothing about that, because an article can
	// be fetched long after it was published.
	//
	// limit <= 0 means 500. A page is cut at a fetch time, never inside a group
	// of articles that share one (a poll stamps its whole batch with one time),
	// so it can hold more than limit articles. The caller asks for the next page
	// with the fetch time of the last article and stops at a page shorter than
	// limit. Like the other *In reads it restricts the SQL to the allowed set,
	// and an empty set yields nothing.
	FetchedAfter(ctx context.Context, after time.Time, allowed map[string]bool, limit int) ([]model.Article, error)
	// ChangedAfter returns the articles of the allowed sources inserted or
	// changed by a commit after change number after, in the order of the
	// commits, at most limit (<= 0 means 500), and the number of the last one
	// (after itself when there are none). The numbers are given inside the
	// committing transaction, so a consumer that keeps the last number it
	// handled cannot miss an article that commits late; this, not a time, is the
	// cursor of the hit ledger.
	ChangedAfter(ctx context.Context, after int64, allowed map[string]bool, limit int) ([]model.Article, int64, error)
	// ChangeSeqAt is the change number reached by the commits fetched at or
	// before t, to move a position kept as a time to the numbered order.
	ChangeSeqAt(ctx context.Context, t time.Time) (int64, error)
	// Backfilled returns the marks of those of the articles that were stored by
	// a repair or a historical range instead of delivered by the live feed. The
	// hit ledger raises no alert for them: they were not news when stored.
	Backfilled(ctx context.Context, ids []string) (map[string]Backfill, error)
}

// Store is the whole database.
type Store interface {
	Base
	// LoadSource and SaveSource keep the scheduler's per-source State and
	// Health (they satisfy scheduler.StateStore).
	LoadSource(ctx context.Context, sourceID string) (feeds.State, scheduler.Health, error)
	SaveSource(ctx context.Context, sourceID string, st feeds.State, h scheduler.Health) error
	// Tenant returns the handle for one tenant. It does not check that the
	// tenant exists; the registry does.
	Tenant(id string) TenantStore
	Close() error
}

// Cursor is how far a watch has been evaluated: the fetch time of the newest
// article it has seen, and the hash of the watch definition that evaluation
// used (see package ledger).
type Cursor struct {
	Fetched time.Time
	// Seq is the change number the watch has been evaluated up to (see
	// Base.ChangedAfter); 0 for a cursor kept before the numbers existed, which
	// is then read from Fetched.
	Seq  int64
	Hash string
}

// PendingAlert is one alert name of one hit that has not been delivered yet. At
// is the detection time of the hit.
type PendingAlert struct {
	WatchID   string
	ArticleID string
	Alert     string
	At        time.Time
}

// TenantStore is everything one tenant owns.
type TenantStore interface {
	ID() string
	// Labels is this tenant's keep/drop verdicts.
	Labels() labels.Store
	// PutHits records that articles matched a watch. A hit already recorded
	// for the same watch and article is left as it is.
	PutHits(ctx context.Context, hits []watch.Hit) error
	// AddHits is PutHits that also returns how many hits were new, that is,
	// not recorded before for their watch and article.
	AddHits(ctx context.Context, hits []watch.Hit) (int, error)
	// Hits returns a watch's hits at or after since, newest first.
	Hits(ctx context.Context, watchID string, since time.Time) ([]watch.Hit, error)
	// Unread counts a watch's hits newer than its read marker. The watch
	// switcher shows it as a badge. A hit's time is when it was detected, so a
	// late-fetched article counts as unread.
	Unread(ctx context.Context, watchID string) (int, error)
	// MarkSeen moves a watch's read marker to t.
	MarkSeen(ctx context.Context, watchID string, t time.Time) error

	// The methods below are the state of the hit ledger (package ledger).

	// Cursor returns a watch's evaluation cursor, and false when the watch has
	// none yet.
	Cursor(ctx context.Context, watchID string) (Cursor, bool, error)
	// SetCursor stores a watch's evaluation cursor, replacing the old one.
	SetCursor(ctx context.Context, watchID string, c Cursor) error
	// PendingAlerts returns every alert name of every hit that has no delivery
	// record yet, oldest hit first, then by watch, article and alert. Hits
	// without alert names do not appear.
	PendingAlerts(ctx context.Context) ([]PendingAlert, error)
	// MarkDelivered records that the alerts were delivered at the given time.
	// Recording one twice keeps the first record.
	MarkDelivered(ctx context.Context, alerts []PendingAlert, at time.Time) error
	// DeliveredChannels returns the channels, by article ID, that the alert of
	// the watch was already sent to (see MarkSentTo).
	DeliveredChannels(ctx context.Context, watchID, alert string) (map[string]map[string]bool, error)
	// MarkSentTo records that the alerts were sent to the channel. The alert
	// is not delivered until MarkDelivered says so, which the caller does when
	// every channel it names has the record. Recording one twice keeps the
	// first record.
	MarkSentTo(ctx context.Context, alerts []PendingAlert, channel string, at time.Time) error
	// LastDelivery returns when the latest alert of a watch with the given
	// name was delivered, and false when none was. It is what an alert's
	// cooldown is measured from.
	LastDelivery(ctx context.Context, watchID, alert string) (time.Time, bool, error)
	// DigestSent reports whether a watch's digest for the period (a day such
	// as "2026-11-03" or an ISO week such as "2026-W45") was sent.
	DigestSent(ctx context.Context, watchID, periodKey string) (bool, error)
	// MarkDigestSent records that a watch's digest for the period was sent at
	// the given time. Recording one twice keeps the first record.
	MarkDigestSent(ctx context.Context, watchID, periodKey string, at time.Time) error
}
