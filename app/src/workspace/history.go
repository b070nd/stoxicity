package workspace

import (
	"context"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/quota"
)

// StartHistoryRefresh fills public chart history while the TUI is open. Rendering
// stays offline; leases and durable checks coordinate with a concurrent CLI run.
// Stop must run before Close.
func (w *Workspace) StartHistoryRefresh(ctx context.Context) func() {
	c := w.cfg.Company
	if !c.Enabled || c.History == "off" || c.HistoryPerCycle == 0 {
		return func() {}
	}
	limits := map[string]quota.Limit{}
	for key, budget := range w.cfg.Fetch.Quotas {
		limits[key] = budget.Limit()
	}
	client, err := httpx.New(httpx.Config{UserAgent: "stoxicity-tui/public-history", Timeout: w.cfg.FetchTimeout(), DefaultRate: 1, Burst: 1, QuotaStore: w.db, Quotas: limits, QuotaNow: w.now})
	if err != nil {
		return func() {}
	} // configuration is validated before Open
	k := marketdata.HistoryKeeper{Store: w.db, Source: marketdata.YahooHistory{Client: client, URL: c.HistoryURL, Now: w.now}, Now: w.now}
	every, _ := time.ParseDuration(c.HistoryEvery)
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		k.Run(rctx, func(ctx context.Context) ([]string, []string, error) {
			var all, priority []string
			priority = append(priority, w.cfg.Watchlist...)
			for _, profile := range w.Universe().All() {
				all = append(all, profile.Ticker)
				if len(profile.Lists) > 0 || len(profile.Tags) > 0 {
					priority = append(priority, profile.Ticker)
				}
			}
			// Visible recent-news stocks fill before the remainder of the catalogue.
			articles, err := w.read.Range(ctx, w.now().AddDate(0, 0, -30), w.now().Add(time.Hour))
			if err != nil {
				return nil, nil, err
			}
			for _, article := range articles {
				priority = append(priority, article.Tickers...)
			}
			return all, marketdata.Tidy(priority), nil
		}, every, c.HistoryPerCycle, nil)
	}()
	return func() { cancel(); <-done }
}
