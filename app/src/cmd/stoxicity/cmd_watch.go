package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/watch"
)

const defaultTestSince = 24 * time.Hour

// cmdWatch lists, checks and tries the selected tenant's watches.
func cmdWatch(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: list, check or test")
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		if len(args) > 1 {
			return usageErrorf("list takes no arguments")
		}
		return watchList(ctx, a)
	case "check":
		return watchCheck(a, args[1:])
	case "test":
		return watchTest(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q, expected: list, check or test", args[0])
}

// watchList prints the tenant's watches with their unread counts.
func watchList(ctx context.Context, a *app) error {
	ws, err := a.watches(a.tenantID).List()
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	ts := db.Tenant(a.tenantID)
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tUNREAD\tQUERY")
	for _, w := range ws {
		unread, err := ts.Unread(ctx, w.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", w.ID, w.Name, unread, oneLine(w.Query))
	}
	return tw.Flush()
}

// watchCheck validates watch files, all of them if none is named, and prints
// every problem found.
func watchCheck(a *app, ids []string) error {
	ok, problems := checkWatches(a.watches(a.tenantID), a.watchDir(), ids, a.filterEnv(a.tenantID))
	for _, id := range ok {
		fmt.Fprintf(a.stdout, "ok    %s\n", id)
	}
	for _, p := range problems {
		fmt.Fprintf(a.stdout, "FAIL  %s\n", oneLine(p.Error()))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problems found", len(problems))
	}
	return nil
}

// watchDir is the watches folder of the selected tenant.
func (a *app) watchDir() string { return a.watches(a.tenantID).Dir }

// checkWatches loads the named watches, or every *.yaml file of dir, and parses
// their queries. It returns the IDs that are fine and one error per problem.
func checkWatches(s watch.FSStore, dir string, ids []string, env filter.Env) (ok []string, problems []error) {
	if len(ids) == 0 {
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, []error{err}
		}
		for _, e := range entries {
			if id, found := strings.CutSuffix(e.Name(), ".yaml"); found && !e.IsDir() {
				ids = append(ids, id)
			}
		}
	}
	for _, id := range ids {
		w, err := s.Get(id)
		if err == nil {
			_, err = filter.ParseWith(w.Query, env)
			if err != nil {
				err = fmt.Errorf("watch %q: query: %w", id, err)
			}
		}
		if err != nil {
			problems = append(problems, err)
			continue
		}
		ok = append(ok, id)
	}
	return ok, problems
}

// watchTest prints the articles a watch would have matched, without recording
// anything.
func watchTest(ctx context.Context, a *app, args []string) error {
	fs := newFlags("watch test")
	sinceFlag := fs.String("since", defaultTestSince.String(), "how far back to look, such as 24h or 7d")
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(ids) != 1 {
		return usageErrorf("expected one watch ID")
	}
	since, err := parseSince(*sinceFlag)
	if err != nil {
		return usageErrorf("-since: %v", err)
	}
	w, err := a.watches(a.tenantID).Get(ids[0])
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	matched, err := a.ledger(db, nil).Preview(ctx, a.tenantID, w, a.now().Add(-since))
	if err != nil {
		return err
	}
	loc := a.location()
	for _, art := range matched {
		printArticle(a.stdout, art, loc)
	}
	fmt.Fprintf(a.stdout, "%d matches in the last %s\n", len(matched), *sinceFlag)
	return nil
}
