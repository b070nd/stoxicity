package tui

import (
	"context"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
)

// WatchInfo is a watch as the watch bar shows it.
type WatchInfo struct {
	ID     string
	Name   string
	Unread int
}

// SourceRow is one line of the source health screen.
type SourceRow struct {
	ID        string
	Kind      string
	Enabled   bool
	Licence   string
	Health    string // green, yellow, red, or empty before the first poll
	LastOK    time.Time
	LastError string
}

// Backend is everything the interface reads and writes. The workspace package
// implements it over the stores; tests use a fake. Every article it returns has
// passed the tenant's licence visibility, so a view never filters for it.
type Backend interface {
	Tenant() string
	Zone() *time.Location
	// Threshold is the relevance at or above which an article counts.
	Threshold() float64
	Now() time.Time
	// DefaultWatch is the watch to open when none is asked for; empty means
	// all news.
	DefaultWatch() string
	// Watchlist is the tickers the user follows.
	Watchlist() []string
	// TopicNames are the topics the configuration defines.
	TopicNames() []string
	// CompanyName is the name of a ticker, or "" when unknown.
	CompanyName(ticker string) string

	Watches(ctx context.Context) ([]WatchInfo, error)
	// Articles are the visible articles published in [from, to). With a watch
	// ID they are the ones the watch matches (scope, lens and query); with ""
	// they are all of them.
	Articles(ctx context.Context, watchID string, from, to time.Time) ([]model.Article, error)
	// Search runs a Google-style query (package filter) over the same set.
	Search(ctx context.Context, watchID, query string, from, to time.Time, limit int) ([]model.Article, error)
	Sources(ctx context.Context) ([]SourceRow, error)
	Verdicts(ctx context.Context) (map[string]labels.Verdict, error)
	Label(ctx context.Context, articleID string, keep bool) error
	// MarkSeen moves the watch's read marker to now.
	MarkSeen(ctx context.Context, watchID string) error
}
