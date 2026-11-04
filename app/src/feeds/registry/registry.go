// Package registry wires the fetchers to the source kinds, so the scheduler
// can look up the right Fetcher for a model.Source by its Kind.
package registry

import (
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/feeds/api"
	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/feeds/rss"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Options are the settings the fetchers share. The zero value is right for
// production: the SEC's own addresses, the real clock and no ticker list.
type Options struct {
	// Tickers maps a CIK written with ten digits to its symbols, so filings
	// carry Article.Tickers. See edgar.TickersByCIK. Optional.
	Tickers map[string][]string
	// WebBase and DataBase replace https://www.sec.gov and https://data.sec.gov;
	// tests point them at httptest servers.
	WebBase  string
	DataBase string
	// Now is the clock of every fetcher; time.Now if nil.
	Now func() time.Time
}

// New returns one Fetcher for each source kind, all making their requests
// through client.
func New(client httpx.Client, opts Options) map[model.Kind]feeds.Fetcher {
	return map[model.Kind]feeds.Fetcher{
		model.KindRSS: rss.New(client, rss.Config{Now: opts.Now}),
		model.KindFilings: edgar.New(client, edgar.Config{
			WebBase:  opts.WebBase,
			DataBase: opts.DataBase,
			Tickers:  opts.Tickers,
			Now:      opts.Now,
		}),
		model.KindAPI: api.New(client),
	}
}
