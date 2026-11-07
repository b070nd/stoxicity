package tui

import (
	"context"
	"github.com/b070nd/stoxicity/app/src/exchange"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/status"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// WatchInfo is a watch as the watch bar shows it.
type WatchInfo struct {
	ID     string
	Name   string
	Unread int
}

// FeedInfo is one feed of a stock as its profile shows it: the company's filing
// record at the SEC or the news feed its website points to.
type FeedInfo struct {
	Kind      string // "sec" or "site"
	Title     string
	URL       string // the feed's address; for the SEC, the company's page on EDGAR
	LastOK    time.Time
	LastNew   int
	Failures  int
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
	// Universe is the current snapshot of every known company. It is replaced
	// as a whole after Tag and SetListMember, so ask again after them.
	Universe() *universe.Universe
	// Following lists the names of the watches whose scope names the ticker.
	Following(ticker string) []string
	// StockFeeds lists the feeds recorded for a ticker (not those of the other
	// tickers of its company), the SEC record first.
	StockFeeds(ticker string) []FeedInfo
	// Quotes are the newest daily candle of every stock that has one, by
	// ticker, with the close before it. The map is read-only; it is empty when
	// no price source is set up.
	Quotes() map[string]Quote
	// Numbers are the figures of the companies' latest fiscal year and market
	// value, by name (marketcap, revenue, income, assets, equity, growth, margin,
	// roe, equity_ratio, shares) and then ticker. Only common stocks have them.
	// The map is read-only; it is empty when the SEC's figures are not loaded.
	Numbers() map[string]map[string]float64
	// Insiders lists the insider trades of the company a ticker belongs to over
	// the last year, newest first: who traded, what, how many shares, at what
	// price. Empty when the Form 4 filings are not read.
	Insiders(ticker string) []model.InsiderTrade

	Watches(ctx context.Context) ([]WatchInfo, error)
	// Articles are the visible articles published in [from, to). With a watch
	// ID they are the ones the watch matches (scope, lens and query); with ""
	// they are all of them.
	Articles(ctx context.Context, watchID string, from, to time.Time) ([]model.Article, error)
	// Search runs a Google-style query (package filter) over the same set.
	Search(ctx context.Context, watchID, query string, from, to time.Time, limit int) ([]model.Article, error)
	Sources(ctx context.Context) ([]status.Source, error)
	Verdicts(ctx context.Context) (map[string]labels.Verdict, error)
	Label(ctx context.Context, articleID string, keep bool) error
	// LabelQueue chooses the next n articles to judge: the ones the lens is least
	// sure of, kind by kind, and a random fourth.
	LabelQueue(ctx context.Context, n int) ([]labels.Pick, error)
	// LabelProgress counts the labels and measures the lens against them.
	LabelProgress(ctx context.Context) (labels.Progress, error)
	// MarkSeen moves the watch's read marker to now.
	MarkSeen(ctx context.Context, watchID string) error
	// Tag puts a tag on a stock, or takes it off.
	Tag(ctx context.Context, ticker, tag string, add bool) error
	// SetListMember puts a stock in one of the user's lists, or takes it out.
	// A list that does not exist is created when a stock is added.
	SetListMember(ctx context.Context, listID, ticker string, add bool) error
}

// Explainer is implemented by a Backend that can say why a figure is missing:
// Why gives, by figure name and then ticker, the reason a market value could not
// be calculated.
type Explainer interface {
	Why() map[string]map[string]string
}

// CapStanding is where a stock's capitalisation stands: the state in a word, why
// when there is no figure, the dates of the count and the price it rests on, and
// whether the figure is the issuer's (every class of stock together) for a
// security that is one of several classes.
type CapStanding struct {
	// State is one of valued, stale, incomplete, unresolved, unsupported, failed or
	// pending (see package valuation).
	State              string
	Reason             string
	CountDay, PriceDay string
	// Scope is "issuer" when the figure is the whole issuer's and the security is
	// one of several classes; empty otherwise.
	Scope    string
	Currency string
}

// Standinger is implemented by a Backend that can say where each capitalisation
// stands. It reads what is stored and makes no request.
type Standinger interface {
	Standings() map[string]CapStanding
}

// Calendarer is implemented by a Backend that holds the exchange calendar with
// its dated, sourced overrides. A Backend that does not gets the recurring rules.
type Calendarer interface {
	Calendar() exchange.Calendar
}
