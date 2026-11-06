// Package model holds the types every other package shares. It imports
// nothing from this module, so any package may depend on it.
package model

import "time"

// Kind is how a source is read.
type Kind string

const (
	KindRSS     Kind = "rss"     // RSS or Atom feed
	KindFilings Kind = "filings" // regulator filings, such as SEC EDGAR
	KindAPI     Kind = "api"     // JSON API
	// KindStockFeeds is a pool: one source that stands for the feeds of every
	// stock (their SEC filing records, or their companies' own news feeds). The
	// feeds are rows of the database with a schedule of their own, so thousands
	// of them are one line on the board. See Source.Pool.
	KindStockFeeds Kind = "stockfeeds"
)

// Tier says how much a source is trusted. Primary sources (filings, company
// reports, central banks) outrank commentary, which suits a long-term reader.
type Tier int

const (
	TierPrimary    Tier = 1 // the company or regulator itself
	TierReporting  Tier = 2 // established newsroom reporting facts
	TierCommentary Tier = 3 // analysis and opinion
)

// Article is one fetched news item, before and after scoring.
type Article struct {
	// ID is a stable key. For SEC filings it is the accession number; for
	// everything else a hash of the canonical URL (tracking parameters removed).
	ID        string
	SourceID  string
	Domain    string // the publisher's host without a leading www; aggregations keep subdomains apart, the site: filter matches them
	URL       string
	Title     string
	Summary   string
	Published time.Time
	Fetched   time.Time
	Form      string   // SEC form type such as "8-K", empty for non-filings
	Items     []string // 8-K item numbers such as "2.02", the strongest signal of what happened
	Tickers   []string // symbols the text mentions
	Topics    []string // trends or themes it was filed under
	ClusterID string   // stories about the same event share one; empty until clustered
	Score     Score
}

// Score is what the lens decided about an Article. It is stored together with
// LensVersion so a result can always be traced to the rules that made it.
type Score struct {
	Relevance   float64   // 0..1, calibrated; at or above the configured threshold counts as relevant
	Materiality int       // 1..5, see MaterialityLevels
	Tone        float64   // -1..1, negative to positive for the business
	Kind        EventKind // what happened
	Sector      string    // from the fixed sector list
	Reasons     []string  // which lens rules fired, so the verdict can be explained
	LensVersion string
}

// Relevant reports whether the score reaches threshold.
func (s Score) Relevant(threshold float64) bool { return s.Relevance >= threshold }

// Entity is a company the aggregator can recognise in text. Tickers are reused
// and reassigned, so the identifiers below are what makes a match reliable.
type Entity struct {
	Ticker   string
	Exchange string
	MIC      string // ISO 10383 market identifier of the listing
	Name     string
	CIK      string // SEC identifier, empty for non-US listings
	FIGI     string // OpenFIGI identifier
	ISIN     string
	LEI      string   // legal entity identifier
	Aliases  []string // other names the press uses
	Sector   string
}
