package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/refdata"
)

const universeHistoryUsage = "history [-max N] [-force] [-fill] [-status] [TICKER...]"

func (a *app) historyKeeper() (*marketdata.HistoryKeeper, error) {
	db, err := a.store()
	if err != nil {
		return nil, err
	}
	client, err := a.fetchClient("stoxicity/" + version + " (public price history)")
	if err != nil {
		return nil, err
	}
	return &marketdata.HistoryKeeper{Store: db, Source: marketdata.YahooHistory{Client: client, URL: a.cfg.Company.HistoryURL, Now: a.now}, Now: a.now}, nil
}

func (a *app) universeHistory(ctx context.Context, args []string) error {
	fs := newFlags("universe history")
	maxN := fs.Int("max", 100, "most symbols to fill; 0 processes every due symbol")
	force := fs.Bool("force", false, "refresh even if already checked recently")
	status := fs.Bool("status", false, "show stored coverage without fetching")
	fill := fs.Bool("fill", false, "continue bounded batches until every due symbol has been attempted; resume provider cooldowns")
	tickers, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if *maxN < 0 {
		return usageErrorf("-max must not be negative")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	if *status {
		states, err := db.HistoryStates(ctx)
		if err != nil {
			return err
		}
		fetched, months, failed := 0, 0, 0
		for _, s := range states {
			if !s.FetchedAt.IsZero() {
				fetched++
				months += s.Months
			}
			if s.Problem != "" {
				failed++
			}
		}
		fmt.Fprintf(a.stdout, "public history: %d symbols stored, %d monthly closes, %d last attempts failed\n", fetched, months, failed)
		for _, ticker := range tickers {
			ticker = strings.ToUpper(ticker)
			s := states[ticker]
			fmt.Fprintf(a.stdout, "%s: %d months; fetched %s", ticker, s.Months, s.FetchedAt.UTC().Format(time.RFC3339))
			if s.Problem != "" {
				fmt.Fprintf(a.stdout, "; %s", s.Problem)
			}
			fmt.Fprintln(a.stdout)
		}
		return nil
	}
	if a.cfg.Company.History == "off" {
		return fmt.Errorf("history is off: set company.history to yahoo")
	}
	all := marketdata.Tidy(tickers)
	for i := range all {
		all[i] = strings.ToUpper(all[i])
	}
	all = marketdata.Tidy(all)
	priority := all
	if len(all) == 0 {
		all, err = refdata.QuoteTickers(ctx, db)
		if err != nil {
			return err
		}
		priority = interestingTickers(ctx, db, a.knownTickers(), a.layerPath(), a.cfg.Lens.Threshold, a.now())
	}
	if len(all) == 0 {
		return fmt.Errorf("no symbols yet: run stoxicity universe update, or name symbols explicitly")
	}
	k, err := a.historyKeeper()
	if err != nil {
		return err
	}
	var problems error
	for {
		rep, err := k.Refresh(ctx, all, priority, *maxN, *force)
		fmt.Fprintf(a.stdout, "public history (yahoo, 12M + 10Y): %s\n", rep)
		if !*fill {
			return err
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "price history: %v\n", err)
			var throttle *httpx.Error
			if !errors.As(err, &throttle) || throttle.Class != httpx.Throttled || rep.Failed > 1 {
				problems = err
			}
		}
		var blocked *httpx.Error
		if errors.As(err, &blocked) && blocked.Class == httpx.Blocked {
			return err
		}
		if rep.Left == 0 || ctx.Err() != nil {
			return errors.Join(problems, ctx.Err())
		}
		wait, _ := time.ParseDuration(a.cfg.Company.HistoryEvery)
		var he *httpx.Error
		if errors.As(err, &he) && he.RetryAfter > wait {
			wait = he.RetryAfter
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		*force = false // a fill resumes remaining work, not an infinite forced refresh
	}
}

func (a *app) startHistoryRefresh(ctx context.Context) func() {
	c := a.cfg.Company
	if !c.Enabled || c.History == "off" || c.HistoryPerCycle == 0 {
		return func() {}
	}
	k, err := a.historyKeeper()
	if err != nil {
		fmt.Fprintf(a.stderr, "history: %v\n", err)
		return func() {}
	}
	db := a.db
	known, layer, threshold := a.knownTickers(), a.layerPath(), a.cfg.Lens.Threshold
	every, _ := time.ParseDuration(c.HistoryEvery)
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		k.Run(rctx, func(ctx context.Context) ([]string, []string, error) {
			all, err := refdata.QuoteTickers(ctx, db)
			return all, interestingTickers(ctx, db, known, layer, threshold, a.now()), err
		}, every, c.HistoryPerCycle, func(rep marketdata.HistoryReport, err error) {
			if rep.Asked > 0 {
				fmt.Fprintf(a.stdout, "price history: %s\n", rep)
			}
			if err != nil && rctx.Err() == nil {
				fmt.Fprintf(a.stderr, "price history: %v\n", err)
			}
		})
	}()
	return func() { cancel(); <-done }
}
