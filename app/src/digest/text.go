package digest

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Text writes the digest as plain text that also reads as Markdown: the
// command line prints it and the digest notification carries it. tenantID names
// the reader, loc is the zone of the times shown, total is the number of
// articles the digest was drawn from and threshold is the relevance that counted
// as relevant. A digest of a span (Until set) says so in its heading.
func Text(w io.Writer, dg Digest, tenantID string, loc *time.Location, total int, threshold float64) {
	if dg.Until.IsZero() {
		fmt.Fprintf(w, "Digest for %s (%s), tenant %s\n", dg.Day.Format(time.DateOnly), loc, tenantID)
	} else {
		fmt.Fprintf(w, "Digest for %s to %s (%s), tenant %s\n",
			dg.Day.Format(time.DateOnly), dg.Until.In(loc).Format(time.DateOnly), loc, tenantID)
	}
	relevant := 0
	for _, s := range dg.Domains {
		relevant += s.Relevant
	}
	fmt.Fprintf(w, "%d articles, %d relevant (relevance at least %.2f)\n", total, relevant, threshold)

	if len(dg.Summary.Lines) > 0 {
		fmt.Fprintln(w, "\nSummary")
		for _, l := range dg.Summary.Lines {
			fmt.Fprintf(w, "  - %s (%s)\n", oneLine(l.Text), l.Article.Domain)
		}
	}
	if len(dg.Top) > 0 {
		fmt.Fprintln(w, "\nTop")
		for i, a := range dg.Top {
			kind := "-"
			if a.Score.Kind != "" {
				kind = string(a.Score.Kind)
			}
			fmt.Fprintf(w, "  %d. %s  %s  materiality %d  relevance %.2f\n", i+1,
				a.Published.In(loc).Format("15:04"), kind, a.Score.Materiality, a.Score.Relevance)
			fmt.Fprintf(w, "     %s\n     %s  %s\n", oneLine(a.Title), a.Domain, a.URL)
		}
	}
	if len(dg.Domains) > 0 {
		fmt.Fprintln(w, "\nDomains (relevant / total)")
		for _, s := range dg.Domains {
			fmt.Fprintf(w, "  %-32s %d / %d\n", s.Domain, s.Relevant, s.Total)
		}
	}
	if len(dg.Candidates) > 0 {
		fmt.Fprintln(w, "\nNew stock candidates")
		for _, c := range dg.Candidates {
			fmt.Fprintf(w, "  %-8s %d stories, weight %d\n", c.Ticker, c.Mentions, c.Weight)
		}
	}
	if len(dg.Trends) > 0 {
		fmt.Fprintln(w, "\nRising topics")
		for _, t := range dg.Trends {
			fmt.Fprintf(w, "  %-24s %d today, %.1f a day before, lift %.1f\n", t.Topic, t.Today, t.Baseline, t.Lift)
		}
	}
}

// oneLine collapses all white space, line breaks included, so a value from an
// article stays on its line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
