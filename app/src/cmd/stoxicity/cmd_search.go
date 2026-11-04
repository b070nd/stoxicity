package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/reader"
)

const (
	// defaultLimit is how many results search prints unless -limit says otherwise.
	defaultLimit = 50
)

// cmdSearch filters the stored news with a Google-style query.
func cmdSearch(ctx context.Context, a *app, args []string) error {
	fs := newFlags("search")
	fromFlag := fs.String("from", "", "first day, YYYY-MM-DD in the configured time zone")
	toFlag := fs.String("to", "", "last day included, YYYY-MM-DD")
	limit := fs.Int("limit", defaultLimit, "most results to print")
	words, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(words) == 0 {
		return usageErrorf("no query given")
	}
	if *limit < 1 {
		return usageErrorf("-limit must be at least 1")
	}
	loc := a.location()
	var from, to time.Time
	if *fromFlag != "" {
		if from, err = parseDay(*fromFlag, loc); err != nil {
			return usageErrorf("-from: %v", err)
		}
	}
	if *toFlag != "" {
		if to, err = parseDay(*toFlag, loc); err != nil {
			return usageErrorf("-to: %v", err)
		}
		to = to.AddDate(0, 0, 1)
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return usageErrorf("-from must not be after -to")
	}
	q, err := filter.Parse(strings.Join(words, " "))
	if err != nil {
		return err
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	rd, err := a.reader()
	if err != nil {
		return err
	}

	res, err := rd.Search(ctx, q, from, to, *limit)
	if err != nil {
		return err
	}
	if res.Truncated {
		fmt.Fprintf(a.stderr, "stoxicity: search: only the newest %d matching candidates were checked; narrow the search with -from and -to\n", reader.ScanCap)
	}
	for _, art := range res.Articles {
		printArticle(a.stdout, art, loc)
	}
	return nil
}

// printArticle writes one article as a line of tab-separated fields: time,
// domain, kind, relevance, title and URL.
func printArticle(w io.Writer, a model.Article, loc *time.Location) {
	fmt.Fprintf(w, "%s\t%s\t%s\t%.2f\t%s\t%s\n",
		a.Published.In(loc).Format("2006-01-02 15:04"), a.Domain, kindOf(a), a.Score.Relevance,
		oneLine(a.Title), a.URL)
}
