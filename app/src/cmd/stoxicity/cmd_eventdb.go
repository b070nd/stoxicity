package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/research"
	"github.com/b070nd/stoxicity/app/src/store"
)

// cmdEventdb is the historical event database: economic events with their figures.
func cmdEventdb(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: build | list | show | coverage | types")
	}
	switch args[0] {
	case "build":
		return labEventdbBuild(ctx, a, args[1:])
	case "list":
		return labEventdbList(ctx, a, args[1:])
	case "show":
		return labEventdbShow(ctx, a, args[1:])
	case "coverage":
		return labEventdbCoverage(ctx, a, args[1:])
	case "types":
		if len(args) > 1 {
			return usageErrorf("eventdb types takes no arguments")
		}
		printEventTypes(a)
		return nil
	}
	return usageErrorf("unknown subcommand %q (build, list, show, coverage, types)", args[0])
}

// labEndOfDay is the last moment of the day that parseDay reads.
func labEndOfDay(day time.Time) time.Time { return day.AddDate(0, 0, 1).Add(-time.Millisecond) }

func labEventdbBuild(ctx context.Context, a *app, args []string) error {
	fs := newFlags("eventdb build")
	days := fs.Int("days", 365, "build the last N days up to now")
	from := fs.String("from", "", "first day, YYYY-MM-DD (with -to)")
	to := fs.String("to", "", "last day, YYYY-MM-DD, inclusive (with -from)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("eventdb build takes no arguments, got %q", rest[0])
	}
	daysSet := false
	fs.Visit(func(f *flag.Flag) { daysSet = daysSet || f.Name == "days" })
	now := a.now()
	start, end := now.AddDate(0, 0, -*days), now
	switch {
	case *from != "" || *to != "":
		if *from == "" || *to == "" || daysSet {
			return usageErrorf("give either -days or both -from and -to")
		}
		if start, err = parseDay(*from, a.location()); err != nil {
			return usageErrorf("-from: %v", err)
		}
		last, err := parseDay(*to, a.location())
		if err != nil {
			return usageErrorf("-to: %v", err)
		}
		if end = labEndOfDay(last); end.Before(start) {
			return usageErrorf("-to is before -from")
		}
	case *days < 1:
		return usageErrorf("-days must be at least 1")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	res, err := research.Build(ctx, db, start, end, now)
	if err != nil {
		return err
	}
	printBuildResult(a, res)
	return nil
}

func labEventdbList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("eventdb list")
	typ := fs.String("type", "", "only this event type (eventdb types lists them)")
	symbol := fs.String("symbol", "", "only this ticker")
	from := fs.String("from", "", "first day available, YYYY-MM-DD")
	to := fs.String("to", "", "last day available, YYYY-MM-DD, inclusive")
	at := fs.String("at", "", "read the records as recorded by this time (RFC 3339)")
	limit := fs.Int("limit", 50, "at most this many records")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("eventdb list takes no arguments, got %q", rest[0])
	}
	if *limit < 1 {
		return usageErrorf("-limit must be at least 1")
	}
	q := store.EventFactQuery{Symbol: strings.ToUpper(strings.TrimSpace(*symbol)), Limit: *limit}
	if *typ != "" {
		t := eventfact.Type(*typ)
		if !t.Valid() {
			return usageErrorf("%q is not an event type (eventdb types lists them)", *typ)
		}
		q.Types = []eventfact.Type{t}
	}
	if *from != "" {
		if q.From, err = parseDay(*from, a.location()); err != nil {
			return usageErrorf("-from: %v", err)
		}
	}
	if *to != "" {
		d, err := parseDay(*to, a.location())
		if err != nil {
			return usageErrorf("-to: %v", err)
		}
		q.To = labEndOfDay(d)
	}
	if *at != "" {
		if q.AsOf, err = time.Parse(time.RFC3339, *at); err != nil {
			return usageErrorf("-at %q is not an RFC 3339 time such as 2026-11-03T10:00:00Z", *at)
		}
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	recs, err := db.EventFacts(ctx, q)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, recs)
	}
	printEventFacts(a, recs)
	return nil
}

func labEventdbShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("eventdb show")
	asJSON := fs.Bool("json", false, "print as JSON")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: eventdb show ID [-json]")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	hist, err := db.EventFactHistory(ctx, pos[0])
	if err != nil {
		return err
	}
	if len(hist) == 0 {
		return fmt.Errorf("no economic event %s", pos[0])
	}
	latest := hist[len(hist)-1].Record
	if *asJSON {
		return writeJSON(a.stdout, map[string]any{"event": latest, "history": hist})
	}
	printEventFact(a, latest, hist)
	return nil
}

func labEventdbCoverage(ctx context.Context, a *app, args []string) error {
	fs := newFlags("eventdb coverage")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("eventdb coverage takes no arguments, got %q", rest[0])
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	counts, err := db.EventFactCounts(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, counts)
	}
	printFactCounts(a, counts)
	return nil
}
