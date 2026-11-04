package main

import (
	"context"
	"time"

	"github.com/b070nd/stoxicity/app/src/digest"
	"github.com/b070nd/stoxicity/app/src/model"
)

// baselineDays is how many days before the digest day the trends are measured
// against.
const baselineDays = 14

// cmdDigest prints the tenant's digest for one day of the configured zone.
func cmdDigest(ctx context.Context, a *app, args []string) error {
	fs := newFlags("digest")
	dayFlag := fs.String("day", "", "the day, YYYY-MM-DD in the configured time zone (default: today)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	loc := a.location()
	day := a.now().In(loc)
	if *dayFlag != "" {
		if day, err = parseDay(*dayFlag, loc); err != nil {
			return usageErrorf("-day: %v", err)
		}
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	rd, err := a.reader()
	if err != nil {
		return err
	}

	y, m, d := day.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	today, err := rd.Day(ctx, start)
	if err != nil {
		return err
	}
	baseline, err := rd.Range(ctx, start.AddDate(0, 0, -baselineDays), start)
	if err != nil {
		return err
	}

	dg := digest.Build(start, loc, today, baseline, a.cfg.Watchlist, digest.Options{
		Threshold:    a.cfg.Lens.Threshold,
		BaselineDays: baselineDays,
	})
	digest.Text(a.stdout, dg, a.tenantID, loc, len(today), a.cfg.Lens.Threshold)
	return nil
}

// kindOf is the event kind of an article, or "-" if it has none.
func kindOf(a model.Article) string {
	if a.Score.Kind == "" {
		return "-"
	}
	return string(a.Score.Kind)
}
