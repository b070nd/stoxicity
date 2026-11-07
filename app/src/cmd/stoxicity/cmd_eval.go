package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/b070nd/stoxicity/app/src/pages"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eval"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	// defaultEvalDays is how far back eval takes labels.
	defaultEvalDays = 90
	// minEvalLabels is the number of evaluated labels below which the
	// numbers are not worth reading.
	minEvalLabels = 20
	// minDomainLabels is the number of labels a domain needs for its row.
	minDomainLabels = 3
	// unscored names the lens version of articles that have no score.
	unscored = "(unscored)"
)

// labelled is a label and the article it judges, with that article's latest
// score.
type labelled struct {
	Label   labels.Label
	Article model.Article
}

// newConfusion counts items at threshold.
func newConfusion(items []labelled, threshold float64) eval.Confusion {
	var c eval.Confusion
	for _, it := range items {
		c.Add(it.Article.Score.Relevant(threshold), it.Label.Verdict == labels.Keep)
	}
	return c
}

// versionRow is the evaluation of the items one lens version scored.
type versionRow struct {
	Version string
	Labels  int
	Kept    int
	Brier   float64
	Conf    eval.Confusion
}

// domainRow is the precision of the lens's relevant calls in one domain.
type domainRow struct {
	Domain string
	Labels int
	Conf   eval.Confusion
}

// kindRow is the lens's performance on one kind of event: the precision of its
// relevant calls and the share of the kept items it called.
type kindRow struct {
	Kind   string
	Labels int
	Conf   eval.Confusion
}

// evalReport is the outcome of eval.
type evalReport struct {
	Threshold float64
	From      time.Time // first day of the window
	Days      int
	Labels    int // labels in the window
	Skipped   int // of them, labels whose article is gone or not visible
	Items     int // evaluated labels
	Kept      int
	Dropped   int
	Sampled   int
	Cases     []eval.Case // ranked by score, highest first
	Brier     float64
	ECE       float64
	All       eval.Confusion
	Samples   []labelled // the sampled labels only
	SampleCon eval.Confusion
	Versions  []versionRow
	Domains   []domainRow
	Kinds     []kindRow
}

// buildEval measures the lens against items. skipped is the number of labels
// that could not be joined with an article.
func buildEval(items []labelled, skipped int, threshold float64) evalReport {
	r := evalReport{Threshold: threshold, Labels: len(items) + skipped, Skipped: skipped, Items: len(items)}
	for _, it := range items {
		if it.Label.Verdict == labels.Keep {
			r.Kept++
		} else {
			r.Dropped++
		}
		if it.Label.Sampled {
			r.Sampled++
			r.Samples = append(r.Samples, it)
		}
	}
	r.Cases = evalCases(items)
	r.Brier = eval.Brier(r.Cases)
	r.ECE = eval.ECE(r.Cases, 10)
	r.All = newConfusion(items, threshold)
	r.SampleCon = newConfusion(r.Samples, threshold)

	byVersion := map[string][]labelled{}
	byDomain := map[string][]labelled{}
	byKind := map[string][]labelled{}
	for _, it := range items {
		k := string(it.Article.Score.Kind)
		if k == "" {
			k = "(none)"
		}
		byKind[k] = append(byKind[k], it)
		v := it.Article.Score.LensVersion
		if v == "" {
			v = unscored
		}
		byVersion[v] = append(byVersion[v], it)
		d := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(it.Article.Domain)), "www.")
		if d == "" {
			d = "(unknown)"
		}
		byDomain[d] = append(byDomain[d], it)
	}
	for v, group := range byVersion {
		c := newConfusion(group, threshold)
		r.Versions = append(r.Versions, versionRow{
			Version: v, Labels: len(group), Kept: c.Kept(), Brier: eval.Brier(evalCases(group)), Conf: c,
		})
	}
	sort.Slice(r.Versions, func(i, j int) bool { return r.Versions[i].Version < r.Versions[j].Version })
	for d, group := range byDomain {
		if len(group) >= minDomainLabels {
			r.Domains = append(r.Domains, domainRow{Domain: d, Labels: len(group), Conf: newConfusion(group, threshold)})
		}
	}
	for k, group := range byKind {
		r.Kinds = append(r.Kinds, kindRow{Kind: k, Labels: len(group), Conf: newConfusion(group, threshold)})
	}
	sort.Slice(r.Kinds, func(i, j int) bool {
		if r.Kinds[i].Labels != r.Kinds[j].Labels {
			return r.Kinds[i].Labels > r.Kinds[j].Labels
		}
		return r.Kinds[i].Kind < r.Kinds[j].Kind
	})
	sort.Slice(r.Domains, func(i, j int) bool {
		if r.Domains[i].Labels != r.Domains[j].Labels {
			return r.Domains[i].Labels > r.Domains[j].Labels
		}
		return r.Domains[i].Domain < r.Domains[j].Domain
	})
	return r
}

// evalCases turns items into cases ranked by score, highest first, ties by
// article ID.
func evalCases(items []labelled) []eval.Case {
	sorted := append([]labelled(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Article.Score.Relevance != b.Article.Score.Relevance {
			return a.Article.Score.Relevance > b.Article.Score.Relevance
		}
		return a.Article.ID < b.Article.ID
	})
	cases := make([]eval.Case, len(sorted))
	for i, it := range sorted {
		cases[i] = eval.Case{Score: it.Article.Score.Relevance, Relevant: it.Label.Verdict == labels.Keep}
	}
	return cases
}

// cmdEval measures the lens against the tenant's labels.
func cmdEval(ctx context.Context, a *app, args []string) error {
	fs := newFlags("eval")
	days := fs.Int("days", defaultEvalDays, "use labels made in the last days")
	threshold := fs.Float64("threshold", a.cfg.Lens.Threshold, "relevance at or above which the lens calls an article relevant (default: lens.threshold)")
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
	if !(*threshold >= 0 && *threshold <= 1) {
		return usageErrorf("-threshold must be between 0 and 1")
	}
	acc, err := openLabels(a)
	if err != nil {
		return err
	}
	all, err := acc.labels.All(ctx)
	if err != nil {
		return err
	}
	p := pages.NewParams(a.now(), *days, 0, *threshold, a.location())

	var items []labelled
	skipped := 0
	for _, l := range all {
		if l.At.Before(p.First) {
			continue
		}
		art, err := acc.rd.Get(ctx, l.ArticleID)
		if errors.Is(err, store.ErrNotFound) {
			skipped++
			continue
		}
		if err != nil {
			return err
		}
		items = append(items, labelled{Label: l, Article: art})
	}
	rep := buildEval(items, skipped, *threshold)
	rep.From, rep.Days = p.First, p.Days
	renderEval(a.stdout, rep, a.tenantID)
	return nil
}

// renderEval writes the evaluation as plain text.
func renderEval(w io.Writer, r evalReport, tenantID string) {
	fmt.Fprintf(w, "Evaluation of the lens, tenant %s\n", tenantID)
	fmt.Fprintf(w, "labels made since %s (%d days): %d, of which %d skipped because the article is gone or not visible\n",
		r.From.Format(time.DateOnly), r.Days, r.Labels, r.Skipped)
	fmt.Fprintf(w, "evaluated: %d (kept %d, dropped %d, sampled %d)\n", r.Items, r.Kept, r.Dropped, r.Sampled)
	if r.Items < minEvalLabels {
		fmt.Fprintf(w, "warning: only %d evaluated labels (fewer than %d); the numbers below are not meaningful\n", r.Items, minEvalLabels)
	}
	if r.Items == 0 {
		fmt.Fprintln(w, "\nNo labelled articles to evaluate. Label some with: stoxicity label sample")
		return
	}
	fmt.Fprintf(w, "threshold: %.2f\n", r.Threshold)

	fmt.Fprintln(w, "\nRanking (labelled articles, best score first)")
	for _, k := range []int{10, 20, 50} {
		fmt.Fprintf(w, "  precision@%d  %.3f", k, eval.PrecisionAtK(r.Cases, k))
		if len(r.Cases) < k {
			fmt.Fprintf(w, "  (only %d cases)", len(r.Cases))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "\nCalibration")
	fmt.Fprintf(w, "  Brier  %.3f\n  ECE    %.3f\n", r.Brier, r.ECE)

	fmt.Fprintf(w, "\nAt threshold %.2f, all labels\n", r.Threshold)
	fmt.Fprintf(w, "  precision  %s\n  recall     %s\n", r.All.Precision(), r.All.Recall())
	fmt.Fprintf(w, "\nAt threshold %.2f, sampled labels only (%d; the honest estimate, not chosen by rank)\n", r.Threshold, r.Sampled)
	fmt.Fprintf(w, "  precision  %s\n  recall     %s\n", r.SampleCon.Precision(), r.SampleCon.Recall())

	fmt.Fprintf(w, "\nBy lens version (the version of the score each article has now)\n")
	fmt.Fprintf(w, "  %-34s %6s %5s %10s %10s %7s\n", "version", "labels", "kept", "precision", "recall", "Brier")
	for _, v := range r.Versions {
		fmt.Fprintf(w, "  %-34s %6d %5d %10s %10s %7.3f\n", v.Version, v.Labels, v.Kept, short(v.Conf.Precision()), short(v.Conf.Recall()), v.Brier)
	}

	fmt.Fprintf(w, "\nBy kind of event (precision of the lens's relevant calls, and recall of what you kept; few labels make a row unreliable)\n")
	fmt.Fprintf(w, "  %-22s %6s %6s %5s %10s %10s\n", "kind", "labels", "calls", "kept", "precision", "recall")
	for _, k := range r.Kinds {
		fmt.Fprintf(w, "  %-22s %6d %6d %5d %10s %10s\n", k.Kind, k.Labels, k.Conf.Calls(), k.Conf.Kept(), short(k.Conf.Precision()), short(k.Conf.Recall()))
	}

	fmt.Fprintf(w, "\nBy domain (at least %d labels; precision of the lens's relevant calls)\n", minDomainLabels)
	if len(r.Domains) == 0 {
		fmt.Fprintln(w, "  no domain has enough labels")
		return
	}
	fmt.Fprintf(w, "  %-32s %6s %6s %5s %10s\n", "domain", "labels", "calls", "kept", "precision")
	for _, d := range r.Domains {
		fmt.Fprintf(w, "  %-32s %6d %6d %5d %10s\n", d.Domain, d.Labels, d.Conf.Calls(), d.Conf.TP, short(d.Conf.Precision()))
	}
}

// short drops the "(a/b)" part of a ratio, for table cells.
func short(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}
