package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/b070nd/stoxicity/app/src/pages"
	"math/rand"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/reader"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	// defaultLabelList is how many labels `label list` shows.
	defaultLabelList = 20
	// defaultSampleSize and defaultSampleDays are the defaults of `label sample`.
	defaultSampleSize = 10
	defaultSampleDays = 7
)

// cmdLabel records and lists the tenant's keep and drop verdicts.
func cmdLabel(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("want keep, drop, list, sample or queue")
	}
	switch args[0] {
	case "keep":
		return labelArticles(ctx, a, labels.Keep, args[1:])
	case "drop":
		return labelArticles(ctx, a, labels.Drop, args[1:])
	case "list":
		return labelList(ctx, a, args[1:])
	case "sample":
		return labelSample(ctx, a, args[1:])
	case "queue":
		return labelQueue(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q; want keep, drop, list, sample or queue", args[0])
}

// labelAccess is what the label commands read and write: the selected
// tenant's labels, and its articles through the tenant's reader.
type labelAccess struct {
	labels labels.Store
	rd     *reader.Reader
}

// openLabels checks that the tenant exists and opens its labels and reader.
func openLabels(a *app) (labelAccess, error) {
	if _, err := a.requireTenant(); err != nil {
		return labelAccess{}, err
	}
	rd, err := a.reader()
	if err != nil {
		return labelAccess{}, err
	}
	db, err := a.store()
	if err != nil {
		return labelAccess{}, err
	}
	return labelAccess{labels: db.Tenant(a.tenantID).Labels(), rd: rd}, nil
}

// labelArticles writes one verdict for each article ID. An ID that is unknown
// or not visible to the tenant is reported by name and nothing is written for
// it; the others are still labelled, and the command fails at the end.
func labelArticles(ctx context.Context, a *app, verdict labels.Verdict, args []string) error {
	fs := newFlags("label " + string(verdict))
	sampled := fs.Bool("sampled", false, "the articles came from `label sample`, not from the ranking")
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return usageErrorf("no article ID given")
	}
	acc, err := openLabels(a)
	if err != nil {
		return err
	}

	failed := false
	done := map[string]bool{}
	for _, id := range ids {
		if done[id] {
			continue
		}
		done[id] = true
		art, err := acc.rd.Get(ctx, id)
		if err != nil {
			failed = true
			if errors.Is(err, store.ErrNotFound) {
				fmt.Fprintf(a.stderr, "stoxicity: label: no article %q\n", id)
			} else {
				fmt.Fprintf(a.stderr, "stoxicity: label: article %q: %v\n", id, err)
			}
			continue
		}
		err = acc.labels.Put(ctx, labels.Label{
			Verdict: verdict, ArticleID: art.ID, LensVersion: art.Score.LensVersion,
			Sampled: *sampled, At: a.now(),
		})
		if err != nil {
			failed = true
			fmt.Fprintf(a.stderr, "stoxicity: label: article %q: %v\n", id, err)
			continue
		}
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", verdict, art.ID, oneLine(art.Title))
	}
	if failed {
		return errReported
	}
	return nil
}

// labelList prints the latest labels, newest first, with the article titles.
// Labels of articles the tenant can no longer read are left out.
func labelList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("label list")
	n := fs.Int("n", defaultLabelList, "most labels to show")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if *n < 1 {
		return usageErrorf("-n must be at least 1")
	}
	acc, err := openLabels(a)
	if err != nil {
		return err
	}
	all, err := acc.labels.All(ctx)
	if err != nil {
		return err
	}
	loc := a.location()
	shown := 0
	for _, l := range all {
		if shown == *n {
			break
		}
		art, err := acc.rd.Get(ctx, l.ArticleID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		sampled := ""
		if l.Sampled {
			sampled = "sampled"
		}
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\n", l.At.In(loc).Format("2006-01-02 15:04"), l.Verdict, sampled, art.ID, oneLine(art.Title))
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(a.stdout, "no labels yet")
	}
	return nil
}

// labelSample prints a random sample of the recent visible articles that have
// no label, without their scores, so that the verdict does not follow the
// lens. The clock seeds the choice, so a test with a fixed clock gets a fixed
// sample.
func labelSample(ctx context.Context, a *app, args []string) error {
	fs := newFlags("label sample")
	n := fs.Int("n", defaultSampleSize, "articles to show")
	days := fs.Int("days", defaultSampleDays, "days to draw from, ending today")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if *n < 1 {
		return usageErrorf("-n must be at least 1")
	}
	if err := checkWindow(*days, 1); err != nil {
		return err
	}
	acc, err := openLabels(a)
	if err != nil {
		return err
	}
	loc := a.location()
	p := pages.NewParams(a.now(), *days, 0, a.cfg.Lens.Threshold, loc)
	arts, err := acc.rd.Range(ctx, p.First, p.End())
	if err != nil {
		return err
	}
	have, err := acc.labels.All(ctx)
	if err != nil {
		return err
	}
	labelled := make(map[string]bool, len(have))
	for _, l := range have {
		labelled[l.ArticleID] = true
	}
	var pool []model.Article
	for _, art := range arts {
		if !labelled[art.ID] {
			pool = append(pool, art)
		}
	}
	if len(pool) == 0 {
		fmt.Fprintf(a.stdout, "no unlabelled articles between %s and %s\n", p.First.Format(time.DateOnly), p.Last.Format(time.DateOnly))
		return nil
	}
	pages.NewestFirst(pool)
	rng := rand.New(rand.NewSource(a.now().UnixNano()))
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	pool = pages.Head(pool, *n)

	fmt.Fprintf(a.stdout, "%d of the unlabelled articles between %s and %s; judge each, then run: stoxicity label keep|drop -sampled ID...\n",
		len(pool), p.First.Format(time.DateOnly), p.Last.Format(time.DateOnly))
	for _, art := range pool {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\n", art.ID, art.Published.In(loc).Format("2006-01-02 15:04"), art.Domain, oneLine(art.Title), art.URL)
	}
	return nil
}

// labelQueue prints the articles whose verdicts teach the lens most: those it
// is least sure of, one kind of event after the other, and a random fourth that
// keeps the precision estimate honest. The scores are not shown, so the verdict
// does not follow the lens.
func labelQueue(ctx context.Context, a *app, args []string) error {
	fs := newFlags("label queue")
	n := fs.Int("n", defaultSampleSize, "articles to show")
	days := fs.Int("days", 30, "days to draw from, ending today")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if *n < 1 {
		return usageErrorf("-n must be at least 1")
	}
	if err := checkWindow(*days, 1); err != nil {
		return err
	}
	acc, err := openLabels(a)
	if err != nil {
		return err
	}
	loc := a.location()
	p := pages.NewParams(a.now(), *days, 0, a.cfg.Lens.Threshold, loc)
	arts, err := acc.rd.Range(ctx, p.First, p.End())
	if err != nil {
		return err
	}
	have, err := acc.labels.All(ctx)
	if err != nil {
		return err
	}
	done := make(map[string]bool, len(have))
	for _, l := range have {
		done[l.ArticleID] = true
	}
	picks := labels.Queue(arts, done, a.cfg.Lens.Threshold, *n, a.now().In(loc).Format(time.DateOnly))
	if len(picks) == 0 {
		fmt.Fprintf(a.stdout, "no unlabelled articles between %s and %s\n", p.First.Format(time.DateOnly), p.Last.Format(time.DateOnly))
		return nil
	}
	fmt.Fprintf(a.stdout, "%d labels so far, %d to go to %d. Judge each, then run: stoxicity label keep|drop ID... (add -sampled for those marked sampled)\n",
		len(have), max(labels.Target-len(have), 0), labels.Target)
	for _, pk := range picks {
		art, mark := pk.Article, ""
		if pk.Sampled {
			mark = "sampled"
		}
		title := oneLine(art.Title)
		if items := model.ItemLabel(art.Items, 3); items != "" {
			title += " · " + items // an 8-K says what it reports in its items, not in its title
		}
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", art.ID, mark, art.Score.Kind, art.Published.In(loc).Format("2006-01-02 15:04"), art.Domain, title, art.URL)
	}
	return nil
}
