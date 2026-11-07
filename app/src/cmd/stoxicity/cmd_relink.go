package main

import (
	"context"
	"fmt"
	"time"
)

// cmdRelink gives the stored SEC filings the tickers of their CIK. A filing
// names its company by CIK; older versions of the program also searched its
// text and attached tickers that were not its own (the word "CIK" itself, for
// one). It needs no network and makes no score again.
func cmdRelink(ctx context.Context, a *app, args []string) error {
	fs := newFlags("relink")
	days := fs.Int("days", 0, "only the filings of the last days; 0 means all of them")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if *days < 0 {
		return usageErrorf("-days must not be negative")
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	to := a.now().AddDate(0, 0, 2)
	from := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	if *days > 0 {
		from = a.now().AddDate(0, 0, -*days)
	}
	res, err := a.pipeline(db).Relink(ctx, from, to)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "relinked %d filings: %d changed, %d of them left without a ticker (their CIK is not in the SEC list)\n",
		res.Filings, res.Changed, res.Cleared)
	return nil
}
