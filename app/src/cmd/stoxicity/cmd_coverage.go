package main

import (
	"context"
	"fmt"
	"github.com/b070nd/stoxicity/app/src/pages"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/coverage"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
)

const (
	defaultCoverageDays  = 7
	defaultCoverageLimit = 20
	defaultSourceDays    = 30
	// minSourceVerdicts is the number of verdicts a source needs before its
	// precision is shown as a number.
	minSourceVerdicts = 3
)

// coverageOptions says how stories are judged, from the source registry.
func (a *app) coverageOptions() coverage.Options {
	return coverage.Options{Tier: func(id string) model.Tier {
		if s, ok := a.reg[id]; ok {
			return s.Tier
		}
		return model.TierCommentary
	}}
}

// cmdCoverage lists the stories of the last days with how well each is
// covered: how many independent outlets report it and what it rests on.
func cmdCoverage(ctx context.Context, a *app, args []string) error {
	fs := newFlags("coverage")
	days := fs.Int("days", defaultCoverageDays, "days the list covers, ending today")
	limit := fs.Int("limit", defaultCoverageLimit, "most stories to print")
	backing := fs.String("backing", "", "only stories with this backing: primary, corroborated, unconfirmed or opinion")
	thin := fs.Bool("thin", false, "only thin stories: material, one outlet, no primary source")
	all := fs.Bool("all", false, "include stories below the relevance threshold")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if err := checkWindow(*days, *limit); err != nil {
		return err
	}
	var want coverage.Backing
	if *backing != "" {
		want = coverage.Backing(*backing)
		if want.Rank() == 0 {
			return usageErrorf("-backing must be primary, corroborated, unconfirmed or opinion")
		}
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	rd, err := a.reader()
	if err != nil {
		return err
	}
	p := pages.NewParams(a.now(), *days, *limit, a.cfg.Lens.Threshold, a.location())
	arts, err := rd.Range(ctx, p.First, p.End())
	if err != nil {
		return err
	}
	stories := coverage.Stories(arts, a.coverageOptions())
	renderCoverage(a.stdout, filterStories(stories, p.Threshold, *all, want, *thin), p, a.tenantID, *limit)
	return nil
}

// filterStories keeps the stories that pass the filters.
func filterStories(stories []coverage.Story, threshold float64, all bool, want coverage.Backing, thin bool) []coverage.Story {
	var out []coverage.Story
	for _, s := range stories {
		switch {
		case !all && s.Relevance < threshold:
		case want != "" && s.Backing != want:
		case thin && !s.Thin:
		default:
			out = append(out, s)
		}
	}
	return out
}

// renderCoverage writes the story list.
func renderCoverage(w io.Writer, stories []coverage.Story, p pages.Params, tenantID string, limit int) {
	counts := map[coverage.Backing]int{}
	thin := 0
	for _, s := range stories {
		counts[s.Backing]++
		if s.Thin {
			thin++
		}
	}
	fmt.Fprintf(w, "Coverage, tenant %s, %s to %s (%d days)\n", tenantID, p.First.Format(time.DateOnly), p.Last.Format(time.DateOnly), p.Days)
	fmt.Fprintf(w, "%d stories: %d primary, %d corroborated, %d unconfirmed (%d thin), %d opinion\n",
		len(stories), counts[coverage.BackingPrimary], counts[coverage.BackingCorroborated],
		counts[coverage.BackingUnconfirmed], thin, counts[coverage.BackingOpinion])
	if len(stories) == 0 {
		fmt.Fprintln(w, "\nNo stories match.")
		return
	}
	shown := pages.Head(stories, limit)
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "OUTLETS\tBACKING\tMAT\tTONE\tFIRST\tSTORY")
	for _, s := range shown {
		mark := ""
		if s.Thin {
			mark = " [thin]"
		}
		fmt.Fprintf(tw, "%d\t%s%s\t%d\t%+.2f\t%s %s\t%s\n", s.Breadth(), s.Backing, mark, s.Materiality, s.Tone,
			s.Lead.Published.In(p.Zone).Format("01-02 15:04"), outletName(s.Lead), truncate(oneLine(storyTitle(s)), 80))
	}
	tw.Flush()
	if len(stories) > len(shown) {
		fmt.Fprintf(w, "\n%d more; raise -limit to see them.\n", len(stories)-len(shown))
	}
}

// storyTitle is the lead's title with the tickers appended when there are any.
func storyTitle(s coverage.Story) string {
	if len(s.Tickers) == 0 {
		return s.Lead.Title
	}
	return s.Lead.Title + " (" + strings.Join(pages.Head(s.Tickers, 4), ", ") + ")"
}

// outletName is the domain of an article, or its source when it has none.
func outletName(a model.Article) string {
	if a.Domain != "" {
		return a.Domain
	}
	return a.SourceID
}

// cmdSourcesEval judges every source by what it does across stories: how often
// it is first, how late it is otherwise, how often it stands alone, how much it
// rests on primary sources, and how often you kept what it sent.
func cmdSourcesEval(ctx context.Context, a *app, args []string) error {
	fs := newFlags("sources eval")
	days := fs.Int("days", defaultSourceDays, "days the evaluation covers, ending today")
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
	acc, err := openLabels(a)
	if err != nil {
		return err
	}
	p := pages.NewParams(a.now(), *days, 1, a.cfg.Lens.Threshold, a.location())
	arts, err := acc.rd.Range(ctx, p.First, p.End())
	if err != nil {
		return err
	}
	all, err := acc.labels.All(ctx)
	if err != nil {
		return err
	}
	verdicts := make(map[string]labels.Verdict, len(all))
	for _, l := range all {
		verdicts[l.ArticleID] = l.Verdict
	}
	stories := coverage.Stories(arts, a.coverageOptions())
	renderSourceEval(a.stdout, coverage.Sources(stories, p.Threshold, verdicts), len(stories), len(arts), p, a.tenantID)
	return nil
}

// renderSourceEval writes the per-source table.
func renderSourceEval(w io.Writer, stats []coverage.SourceStat, stories, articles int, p pages.Params, tenantID string) {
	fmt.Fprintf(w, "Sources, tenant %s, %s to %s (%d days): %d articles in %d stories\n",
		tenantID, p.First.Format(time.DateOnly), p.Last.Format(time.DateOnly), p.Days, articles, stories)
	if len(stats) == 0 {
		fmt.Fprintln(w, "\nNo articles in the window.")
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SOURCE\tARTICLES\tRELEVANT\tSTORIES\tSHARED\tFIRST\tLAG\tALONE\tPRIMARY\tKEPT")
	for _, s := range stats {
		first, lag, kept := "-", "-", "-"
		if r, ok := s.Scoop(); ok {
			first = fmt.Sprintf("%.0f%%", 100*r)
		}
		if s.LagSamples > 0 {
			lag = s.MedianLag.Round(time.Minute).String()
		}
		if n := s.Kept + s.Dropped; n >= minSourceVerdicts {
			pr, _ := s.Precision()
			kept = fmt.Sprintf("%.0f%% of %d", 100*pr, n)
		} else if n > 0 {
			kept = fmt.Sprintf("%d of %d", s.Kept, n)
		}
		fmt.Fprintf(tw, "%s\t%d\t%.0f%%\t%d\t%d\t%s\t%s\t%d\t%d\t%s\n", s.SourceID, s.Articles,
			100*s.RelevantShare(), s.Stories, s.Shared, first, lag, s.Alone, s.Backed, kept)
	}
	tw.Flush()
	fmt.Fprintln(w, `
RELEVANT  share of the source's articles at or above the lens threshold
SHARED    stories at least one other outlet also reported
FIRST     share of shared stories where this source reported first
LAG       median delay behind the first report, when it was not first
ALONE     stories only this outlet reported
PRIMARY   stories that rest on a primary (tier 1) source
KEPT      share of your keep/drop verdicts on this source that were keep`)
}
