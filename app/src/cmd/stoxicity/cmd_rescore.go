package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/b070nd/stoxicity/app/src/pages"
	"time"
)

// defaultRescoreDays is how far back rescore goes unless told otherwise.
const defaultRescoreDays = 30

// cmdRescore scores the stored articles of a range again with the current
// lens and classifier, so a change of the lens takes effect on old news.
func cmdRescore(ctx context.Context, a *app, args []string) error {
	fs := newFlags("rescore")
	days := fs.Int("days", defaultRescoreDays, "rescore the last days, ending today")
	fromFlag := fs.String("from", "", "first day, YYYY-MM-DD in the configured time zone")
	toFlag := fs.String("to", "", "last day included, YYYY-MM-DD")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	daysSet := false
	fs.Visit(func(f *flag.Flag) { daysSet = daysSet || f.Name == "days" })
	loc := a.location()
	var from, to time.Time
	switch {
	case *fromFlag != "" || *toFlag != "":
		if daysSet {
			return usageErrorf("-days cannot be combined with -from and -to")
		}
		if *fromFlag == "" || *toFlag == "" {
			return usageErrorf("-from and -to go together")
		}
		if from, err = parseDay(*fromFlag, loc); err != nil {
			return usageErrorf("-from: %v", err)
		}
		if to, err = parseDay(*toFlag, loc); err != nil {
			return usageErrorf("-to: %v", err)
		}
		to = to.AddDate(0, 0, 1)
		if !from.Before(to) {
			return usageErrorf("-from must not be after -to")
		}
	default:
		if err := checkWindow(*days, 1); err != nil {
			return err
		}
		p := pages.NewParams(a.now(), *days, 1, 0, loc)
		from, to = p.First, p.End()
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	p := a.pipeline(db)
	n, err := p.Rescore(ctx, from, to)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "rescored %d articles published from %s to %s (%s)\n", n,
		from.Format(time.DateOnly), to.AddDate(0, 0, -1).Format(time.DateOnly), loc)
	fmt.Fprintf(a.stdout, "lens version in force: %s\n", p.Version())
	return nil
}
