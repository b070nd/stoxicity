package main

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/b070nd/stoxicity/app/src/domains"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/pages"
	"github.com/b070nd/stoxicity/app/src/summarize"
	"github.com/b070nd/stoxicity/app/src/tagcloud"
)

const (
	// cloudWidth is the width in characters at which a tag cloud wraps.
	cloudWidth = 78
)

// checkWindow validates the -days and -limit flags of the page commands.
func checkWindow(days, limit int) error {
	if days < 1 || days > pages.MaxDays {
		return usageErrorf("-days must be between 1 and %d", pages.MaxDays)
	}
	if limit < 1 {
		return usageErrorf("-limit must be at least 1")
	}
	return nil
}

// renderCloud writes a tag cloud as wrapped lines in a monospace font. The
// size of a term follows its tier from package tagcloud (1 to 5): one asterisk
// on each side per tier above the lowest, and upper case from tier 4, with the
// count in brackets, for example "tax(2)", "**ai-infra**(9)", "***CHIPS***(14)".
// Terms come most frequent first.
func renderCloud(w io.Writer, terms []tagcloud.Term) {
	line := ""
	for _, t := range terms {
		text := t.Text
		if t.Tier >= 4 {
			text = strings.ToUpper(text)
		}
		stars := strings.Repeat("*", max(t.Tier-1, 0))
		word := fmt.Sprintf("%s%s%s(%d)", stars, text, stars, t.Count)
		if line != "" && utf8.RuneCountInString(line)+2+utf8.RuneCountInString(word) > cloudWidth {
			fmt.Fprintln(w, line)
			line = ""
		}
		if line == "" {
			line = "  " + word
		} else {
			line += "  " + word
		}
	}
	if line != "" {
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w, "  (more asterisks and capitals: more articles; the number is the count)")
}

func renderSummary(w io.Writer, s summarize.Summary) {
	if len(s.Lines) == 0 {
		return
	}
	fmt.Fprintln(w, "\nSummary")
	for _, l := range s.Lines {
		fmt.Fprintf(w, "  - %s (%s)\n", oneLine(l.Text), l.Article.Domain)
	}
}

func renderDays(w io.Writer, rows []pages.DayRow, loc *time.Location) {
	fmt.Fprintln(w, "\nTimeline (relevant / total per day, with the top event)")
	for _, r := range rows {
		fmt.Fprintf(w, "  %s  %d / %d", r.Date.Format("2006-01-02 Mon"), r.Relevant, r.Total)
		if r.HasTop {
			fmt.Fprintf(w, "  %s %s  %s  materiality %d  %s", r.Top.Published.In(loc).Format("15:04"), kindOf(r.Top), r.Top.Domain, r.Top.Score.Materiality, oneLine(r.Top.Title))
		}
		fmt.Fprintln(w)
	}
}

func renderDomains(w io.Writer, rows []domains.Stat) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w, "\nTop domains (relevant / total)")
	for _, s := range rows {
		fmt.Fprintf(w, "  %-32s %d / %d\n", s.Domain, s.Relevant, s.Total)
	}
}

func renderCloudSection(w io.Writer, title string, terms []tagcloud.Term) {
	if len(terms) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", title)
	renderCloud(w, terms)
}

// renderArticles writes numbered articles with their kind and scores.
func renderArticles(w io.Writer, title string, arts []model.Article, loc *time.Location) {
	if len(arts) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", title)
	for i, a := range arts {
		fmt.Fprintf(w, "  %d. %s  %s  materiality %d  relevance %.2f\n", i+1,
			a.Published.In(loc).Format("2006-01-02 15:04"), kindOf(a), a.Score.Materiality, a.Score.Relevance)
		fmt.Fprintf(w, "     %s\n     %s  %s\n", oneLine(a.Title), a.Domain, a.URL)
	}
}

// renderStockPage writes the stock page as plain text.
func renderStockPage(w io.Writer, pg pages.StockPage, tenantID string) {
	p := pg.Params
	title := pg.Ticker
	if pg.Name != "" {
		title += " - " + pg.Name
	}
	fmt.Fprintf(w, "Stock %s, tenant %s\n", title, tenantID)
	fmt.Fprintf(w, "Window %s\n", p.Describe())
	fmt.Fprintln(w, "price and fundamentals: not available (no market data provider is configured)")
	if pg.Total == 0 {
		fmt.Fprintf(w, "\nNo articles name %s in this window.\n", pg.Ticker)
		return
	}
	fmt.Fprintf(w, "%d articles name %s, %d relevant (relevance at least %.2f)\n", pg.Total, pg.Ticker, pg.Relevant, p.Threshold)
	if pg.Relevant == 0 {
		fmt.Fprintf(w, "\nNo article reaches the relevance threshold, so there is no summary or trend.\n")
	}

	renderSummary(w, pg.Summary)
	renderDays(w, pg.Days, p.Zone)
	if len(pg.Weeks) > 0 {
		fmt.Fprintln(w, "\nTone and materiality by week (mean over relevant articles)")
		for _, r := range pg.Weeks {
			fmt.Fprintf(w, "  week of %s  %d articles  materiality %.1f  tone %+.2f\n", r.Week.Format(time.DateOnly), r.Articles, r.Materiality, r.Tone)
		}
	}
	if len(pg.Kinds) > 0 {
		fmt.Fprintln(w, "\nEvent kinds (relevant articles)")
		for _, k := range pg.Kinds {
			fmt.Fprintf(w, "  %-20s %d\n", k.Name, k.Count)
		}
	}
	renderDomains(w, pg.Domains)
	renderCloudSection(w, "Related topics", pg.Topics)
	renderArticles(w, "Newest relevant articles", pg.Newest, p.Zone)
}

// renderTopicPage writes the topic page as plain text.
func renderTopicPage(w io.Writer, pg pages.TopicPage, tenantID string) {
	p := pg.Params
	fmt.Fprintf(w, "Topic %s, tenant %s\n", pg.Topic, tenantID)
	fmt.Fprintf(w, "Window %s\n", p.Describe())
	if pg.Total == 0 {
		fmt.Fprintf(w, "\nNo articles are filed under %s in this window.\n", pg.Topic)
		return
	}
	fmt.Fprintf(w, "%d articles, %d relevant (relevance at least %.2f)\n", pg.Total, pg.Relevant, p.Threshold)

	renderSummary(w, pg.Summary)
	renderCloudSection(w, "Stocks (by relevant articles that name them)", pg.Stocks)
	renderDomains(w, pg.Domains)
	renderDays(w, pg.Days, p.Zone)
	renderCloudSection(w, "Related topics", pg.Related)
	renderArticles(w, "Top articles", pg.Top, p.Zone)
}

// renderTopicsPage writes the list of topics as plain text.
func renderTopicsPage(w io.Writer, pg pages.TopicsPage, tenantID string) {
	p := pg.Params
	fmt.Fprintf(w, "Topics, tenant %s\n", tenantID)
	fmt.Fprintf(w, "Window %s\n", p.Describe())
	fmt.Fprintf(w, "%d articles, %d relevant (relevance at least %.2f)\n", pg.Total, pg.Relevant, p.Threshold)
	if len(pg.Rows) == 0 {
		fmt.Fprintln(w, "\nNo relevant article is filed under a topic in this window.")
		return
	}
	renderCloudSection(w, "Cloud", pg.Cloud)
	fmt.Fprintf(w, "\n%-28s %6s %8s %6s\n", "Topic", "count", "before", "lift")
	for _, r := range pg.Rows {
		fmt.Fprintf(w, "%-28s %6d %8.0f %6.1f\n", r.Topic, r.Count, r.Prior, r.Lift)
	}
	fmt.Fprintf(w, "\nbefore is the count in the %d days before the window; lift is count over before, with before at least 0.5\n", p.Days)
}

// renderStocksPage writes the list of stocks as plain text.
func renderStocksPage(w io.Writer, pg pages.StocksPage, tenantID string) {
	p := pg.Params
	fmt.Fprintf(w, "Stocks, tenant %s\n", tenantID)
	fmt.Fprintf(w, "Window %s\n", p.Describe())
	fmt.Fprintf(w, "%d articles, %d relevant (relevance at least %.2f)\n", pg.Total, pg.Relevant, p.Threshold)
	if len(pg.Rows) == 0 {
		fmt.Fprintln(w, "\nNo relevant article names a stock in this window.")
		return
	}
	fmt.Fprintf(w, "\n%-10s %8s %12s %7s\n", "Stock", "mentions", "materiality", "tone")
	for _, r := range pg.Rows {
		mark := ""
		if r.Watched {
			mark = " (watchlist)"
		}
		fmt.Fprintf(w, "%-10s %8d %12.1f %+7.2f%s\n", r.Ticker, r.Mentions, r.Materiality, r.Tone, mark)
	}
}
