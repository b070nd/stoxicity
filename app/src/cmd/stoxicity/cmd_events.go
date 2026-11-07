package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/assertion"
	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/pipeline"
	"github.com/b070nd/stoxicity/app/src/store"
)

// cmdEvents works with the event history: what happened to which security, as
// revisions that are only ever appended.
func cmdEvents(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("want rebuild, timeline or show")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	switch args[0] {
	case "rebuild":
		return eventsRebuild(ctx, a, db, args[1:])
	case "timeline":
		return eventsTimeline(ctx, a, db, args[1:])
	case "show":
		return eventsShow(ctx, a, db, args[1:])
	}
	return usageErrorf("unknown subcommand %q; want rebuild, timeline or show", args[0])
}

// eventsRebuild derives the events again from the stored, scored articles. It
// adds revisions only where the understanding differs, so it can be repeated.
func eventsRebuild(ctx context.Context, a *app, db *store.DB, args []string) error {
	fs := newFlags("events rebuild")
	days := fs.Int("days", 365, "articles published in the last days")
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
	now := a.now()
	if rep, err := db.SyncSecurities(ctx, now); err != nil {
		return err
	} else {
		fmt.Fprintf(a.stdout, "securities: %d created, %d linked to a company, %d tickers passed to a new holder\n", rep.Created, rep.Linked, rep.Relabelled)
	}
	arts, err := db.Range(ctx, now.AddDate(0, 0, -*days), now.Add(24*time.Hour))
	if err != nil {
		return err
	}
	// What each article claims, one claim at a time: an event is named by its most
	// important current claim, not by the first keyword of the text.
	names := func(t string) []string {
		e, ok := a.entities().Lookup(t)
		if !ok {
			return nil
		}
		return append([]string{e.Name}, e.Aliases...)
	}
	claims := make(map[string]assertion.Result, len(arts))
	for _, art := range arts {
		claims[art.ID] = pipeline.Claims(art, names, edgar.Rate)
	}
	rep, in, err := pipeline.RebuildEvents(ctx, db, arts, claims, now)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "events: %d from %d articles, %d revisions written; %d stories read as more than one event; %d stored events withdrawn (nothing deleted)\n",
		rep.Events, len(arts), rep.Written, rep.Split, rep.Withdrawn)

	results, assessments := pipeline.Assessments(arts, claims, nil, now)
	na, err := db.PutAssertions(ctx, results, now)
	if err != nil {
		return err
	}
	nm, err := db.PutAssessments(ctx, assessments, now)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "assertions: %d claims of %d articles written (%s); assessments: %d readings written (%s)\n",
		na, len(results), assertion.Version, nm, assertion.AssessVersion)

	// The reporting origin of every article that is evidence of an event.
	evidence := map[string]bool{}
	for _, e := range in {
		for _, ev := range e.Evidence {
			evidence[ev.ArticleID] = true
		}
	}
	var rated []model.Article
	for _, art := range arts {
		if evidence[art.ID] {
			rated = append(rated, art)
		}
	}
	n, err := db.PutOrigins(ctx, pipeline.Origins(rated, a.isPrimary), now)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "origins: %d findings written for %d articles that are evidence (unchanged ones are not rewritten)\n", n, len(rated))
	return nil
}

// eventsTimeline prints the events of a stock as known at a time.
func eventsTimeline(ctx context.Context, a *app, db *store.DB, args []string) error {
	fs := newFlags("events timeline")
	at := fs.String("at", "", "what was known at this time (RFC 3339), default now")
	limit := fs.Int("limit", 20, "most events to show")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("want one ticker")
	}
	var known time.Time
	if *at != "" {
		if known, err = time.Parse(time.RFC3339, *at); err != nil {
			return usageErrorf("-at: %v", err)
		}
	}
	entries, err := db.SecurityTimeline(ctx, rest[0], known, *limit)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintf(a.stdout, "no events known for %s\n", strings.ToUpper(rest[0]))
		return nil
	}
	loc := a.location()
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "public\tmat\trole\tkind\tstatus\trev\tevidence\tevent\ttitle")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n", e.PublicAt.In(loc).Format("2006-01-02 15:04"), e.Materiality, e.Role,
			e.Kind, e.Status, e.Revision, e.Evidence, e.EventID, oneLine(e.Title))
	}
	return tw.Flush()
}

// eventsShow prints every revision of one event.
func eventsShow(ctx context.Context, a *app, db *store.DB, args []string) error {
	if len(args) != 1 {
		return usageErrorf("want one event ID")
	}
	hist, err := db.EventHistory(ctx, args[0])
	if err != nil {
		return err
	}
	if len(hist) == 0 {
		return fmt.Errorf("no event %q", args[0])
	}
	loc := a.location()
	for _, r := range hist {
		fmt.Fprintf(a.stdout, "revision %d (%s) recorded %s, public %s, supersedes %d\n", r.Revision, r.Status,
			r.RecordedAt.In(loc).Format(time.DateTime), r.PublicAt.In(loc).Format(time.DateTime), r.Supersedes)
		fmt.Fprintf(a.stdout, "  %s: %s (materiality %d, relevance %.2f, %s)\n", r.Kind, oneLine(r.Title), r.Materiality, r.Relevance, r.LensVersion)
		for _, s := range r.Securities {
			fmt.Fprintf(a.stdout, "  %-12s %-7s %s\n", s.Role, s.Ticker, s.SecurityID)
		}
		for _, e := range r.Evidence {
			fmt.Fprintf(a.stdout, "  evidence %s (%s)\n", e.ArticleID, e.SourceID)
		}
		for _, as := range r.Assessments {
			fmt.Fprintf(a.stdout, "  assessed by %s: %s, direction %s, horizon %s, confidence %.2f\n", as.Rule, as.Effect, as.Direction, as.Horizon, as.Confidence)
		}
	}
	return nil
}
