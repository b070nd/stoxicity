package main

import (
	"context"
	"strings"
)

const (
	// defaultPageDays is how many days a stock or topic page covers.
	defaultPageDays = 30
	// defaultPageLimit is how many rows its lists show.
	defaultPageLimit = 10
	// defaultListDays is how many days the stocks and topics lists cover.
	defaultListDays = 14
)

// cmdStock prints the page of one stock.
func cmdStock(ctx context.Context, a *app, args []string) error {
	fs := newFlags("stock")
	days := fs.Int("days", defaultPageDays, "days the page covers, ending today")
	limit := fs.Int("limit", defaultPageLimit, "most rows in the article and domain lists")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("want exactly one ticker")
	}
	ticker := strings.ToUpper(strings.TrimSpace(rest[0]))
	if !validTicker(ticker) {
		return usageErrorf("%q is not a ticker symbol", rest[0])
	}
	if err := checkWindow(*days, *limit); err != nil {
		return err
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	rd, err := a.reader()
	if err != nil {
		return err
	}
	p := newPageParams(a.now(), *days, *limit, a.cfg.Lens.Threshold, a.location())
	arts, err := rd.Range(ctx, p.First, p.End())
	if err != nil {
		return err
	}
	name := ""
	if e, ok := a.entities().Lookup(ticker); ok {
		name = e.Name
	}
	renderStockPage(a.stdout, buildStockPage(arts, ticker, name, p), a.tenantID)
	return nil
}

// cmdStocks lists the stocks seen in relevant news.
func cmdStocks(ctx context.Context, a *app, args []string) error {
	fs := newFlags("stocks")
	days := fs.Int("days", defaultListDays, "days the list covers, ending today")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if err := checkWindow(*days, 1); err != nil {
		return err
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	rd, err := a.reader()
	if err != nil {
		return err
	}
	p := newPageParams(a.now(), *days, 0, a.cfg.Lens.Threshold, a.location())
	arts, err := rd.Range(ctx, p.First, p.End())
	if err != nil {
		return err
	}
	renderStocksPage(a.stdout, buildStocksPage(arts, a.cfg.Watchlist, p), a.tenantID)
	return nil
}

// validTicker reports whether s looks like a ticker symbol: letters, digits,
// dots and hyphens, starting with a letter or digit, at most 12 characters.
func validTicker(s string) bool {
	if s == "" || len(s) > 12 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case (r == '.' || r == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}
