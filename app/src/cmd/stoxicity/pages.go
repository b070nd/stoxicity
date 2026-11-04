package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/b070nd/stoxicity/app/src/domains"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/summarize"
	"github.com/b070nd/stoxicity/app/src/tagcloud"
	"github.com/b070nd/stoxicity/app/src/timeline"
	"github.com/b070nd/stoxicity/app/src/trends"
)

// The pages of the stock, stocks, topic and topics commands are built by pure
// functions over a slice of articles and a pageParams, so they can be tested
// without a store, and then written as plain text. Callers read the articles
// through the tenant's reader only.

const (
	// cloudMax is the most terms in one tag cloud.
	cloudMax = 30
	// cloudWidth is the width in characters at which a tag cloud wraps.
	cloudWidth = 78
	// summaryLines is the most lines in a page summary.
	summaryLines = 5
	// maxPageDays is the longest window a page command accepts.
	maxPageDays = 3650
	// closestTopics is how many similar topic names an unknown topic lists.
	closestTopics = 10
)

// pageParams say which articles a page covers and how it counts them.
type pageParams struct {
	// Threshold is the relevance at or above which an article counts.
	Threshold float64
	// Limit is the most rows in the article, domain and similar lists.
	Limit int
	// Zone is the time zone of days and weeks.
	Zone *time.Location
	// Days is the length of the window in calendar days.
	Days int
	// First and Last are the first and the last calendar day of the window,
	// midnight in Zone.
	First, Last time.Time
}

// newPageParams returns the window of the last days calendar days in loc,
// ending with the day of now.
func newPageParams(now time.Time, days, limit int, threshold float64, loc *time.Location) pageParams {
	if loc == nil {
		loc = time.UTC
	}
	y, m, d := now.In(loc).Date()
	last := time.Date(y, m, d, 0, 0, 0, 0, loc)
	return pageParams{
		Threshold: threshold, Limit: limit, Zone: loc, Days: days,
		First: last.AddDate(0, 0, -(days - 1)), Last: last,
	}
}

// End is the first instant after the window.
func (p pageParams) End() time.Time { return p.Last.AddDate(0, 0, 1) }

// previous returns the window of the same length that ends the day before p
// starts.
func (p pageParams) previous() pageParams {
	q := p
	q.Last = p.First.AddDate(0, 0, -1)
	q.First = q.Last.AddDate(0, 0, -(p.Days - 1))
	return q
}

// checkWindow validates the -days and -limit flags of the page commands.
func checkWindow(days, limit int) error {
	if days < 1 || days > maxPageDays {
		return usageErrorf("-days must be between 1 and %d", maxPageDays)
	}
	if limit < 1 {
		return usageErrorf("-limit must be at least 1")
	}
	return nil
}

// inWindow returns the articles published inside the window of p.
func inWindow(arts []model.Article, p pageParams) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if !a.Published.Before(p.First) && a.Published.Before(p.End()) {
			out = append(out, a)
		}
	}
	return out
}

// withTicker returns the articles that name ticker, ignoring case.
func withTicker(arts []model.Article, ticker string) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if containsFold(a.Tickers, ticker) {
			out = append(out, a)
		}
	}
	return out
}

// withTopic returns the articles filed under topic, ignoring case.
func withTopic(arts []model.Article, topic string) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if containsFold(a.Topics, topic) {
			out = append(out, a)
		}
	}
	return out
}

func containsFold(list []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return true
		}
	}
	return false
}

// newestFirst sorts articles by publication time, newest first, then by ID.
func newestFirst(arts []model.Article) {
	sort.SliceStable(arts, func(i, j int) bool {
		if !arts[i].Published.Equal(arts[j].Published) {
			return arts[i].Published.After(arts[j].Published)
		}
		return arts[i].ID < arts[j].ID
	})
}

// relevantNewest returns the relevant articles, newest first.
func relevantNewest(arts []model.Article, threshold float64) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if a.Score.Relevant(threshold) {
			out = append(out, a)
		}
	}
	newestFirst(out)
	return out
}

// moreImportant reports whether a ranks above b: higher materiality, then
// higher relevance, then newer, then the lower ID.
func moreImportant(a, b model.Article) bool {
	if a.Score.Materiality != b.Score.Materiality {
		return a.Score.Materiality > b.Score.Materiality
	}
	if a.Score.Relevance != b.Score.Relevance {
		return a.Score.Relevance > b.Score.Relevance
	}
	if !a.Published.Equal(b.Published) {
		return a.Published.After(b.Published)
	}
	return a.ID < b.ID
}

// topArticles returns up to limit articles, most important first.
func topArticles(arts []model.Article, limit int) []model.Article {
	out := append([]model.Article(nil), arts...)
	sort.SliceStable(out, func(i, j int) bool { return moreImportant(out[i], out[j]) })
	return head(out, limit)
}

// head returns the first n elements of s, or all of them.
func head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// dayRow is one line of the day-by-day timeline.
type dayRow struct {
	timeline.Day
	// Top is the most important relevant article of the day, if HasTop.
	Top    model.Article
	HasTop bool
}

// dayRows builds the day-by-day timeline of arts over the window of p, with
// every day of it, empty ones included. Days are calendar days in p.Zone.
func dayRows(arts []model.Article, p pageParams) []dayRow {
	days := timeline.Daily(arts, p.Threshold, p.First, p.Last, p.Zone)
	rows := make([]dayRow, len(days))
	index := make(map[string]int, len(days))
	for i, d := range days {
		rows[i].Day = d
		index[d.Date.Format(time.DateOnly)] = i
	}
	for _, a := range arts {
		if !a.Score.Relevant(p.Threshold) {
			continue
		}
		i, ok := index[a.Published.In(p.Zone).Format(time.DateOnly)]
		if !ok {
			continue
		}
		if !rows[i].HasTop || moreImportant(a, rows[i].Top) {
			rows[i].Top, rows[i].HasTop = a, true
		}
	}
	return rows
}

// weekRow is the mean materiality and tone of one week's relevant articles.
type weekRow struct {
	Week        time.Time // the Monday, midnight in the zone
	Articles    int
	Materiality float64
	Tone        float64
}

// weekRows averages materiality and tone per week, weeks starting on Monday in
// loc, oldest first. Only weeks that have articles appear.
func weekRows(arts []model.Article, loc *time.Location) []weekRow {
	type sum struct {
		week      time.Time
		n         int
		mat, tone float64
	}
	byWeek := map[string]*sum{}
	for _, a := range arts {
		t := a.Published.In(loc)
		y, m, d := t.Date()
		monday := time.Date(y, m, d-(int(t.Weekday())+6)%7, 0, 0, 0, 0, loc)
		key := monday.Format(time.DateOnly)
		s := byWeek[key]
		if s == nil {
			s = &sum{week: monday}
			byWeek[key] = s
		}
		s.n++
		s.mat += float64(a.Score.Materiality)
		s.tone += a.Score.Tone
	}
	rows := make([]weekRow, 0, len(byWeek))
	for _, s := range byWeek {
		rows = append(rows, weekRow{Week: s.week, Articles: s.n, Materiality: s.mat / float64(s.n), Tone: s.tone / float64(s.n)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Week.Before(rows[j].Week) })
	return rows
}

// countRow is a name and how often it occurs.
type countRow struct {
	Name  string
	Count int
}

// kindCounts counts the articles per event kind, the most frequent first.
func kindCounts(arts []model.Article) []countRow {
	counts := map[string]int{}
	for _, a := range arts {
		counts[kindOf(a)]++
	}
	var rows []countRow
	for k, n := range counts {
		rows = append(rows, countRow{Name: k, Count: n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// cloudOf counts the values pick returns for each article, once per article,
// leaves out skip (ignoring case) and builds the tag cloud of the most frequent.
func cloudOf(arts []model.Article, pick func(model.Article) []string, skip string) []tagcloud.Term {
	counts := map[string]int{}
	for _, a := range arts {
		seen := map[string]bool{}
		for _, v := range pick(a) {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] || strings.EqualFold(v, skip) {
				continue
			}
			seen[v] = true
			counts[v]++
		}
	}
	return tagcloud.Build(counts, cloudMax)
}

func topicsOf(a model.Article) []string { return a.Topics }

func tickersOf(a model.Article) []string {
	out := make([]string, len(a.Tickers))
	for i, t := range a.Tickers {
		out[i] = strings.ToUpper(strings.TrimSpace(t))
	}
	return out
}

// stockPage is everything the page of one stock shows.
type stockPage struct {
	Params   pageParams
	Ticker   string
	Name     string
	Total    int // articles in the window that name the ticker
	Relevant int
	Summary  summarize.Summary
	Days     []dayRow
	Weeks    []weekRow
	Kinds    []countRow
	Domains  []domains.Stat
	Topics   []tagcloud.Term
	Newest   []model.Article
}

// buildStockPage assembles the page of ticker from arts, which may hold any
// articles: those outside the window or not naming the ticker are ignored.
// name is the company name, empty if unknown.
func buildStockPage(arts []model.Article, ticker, name string, p pageParams) stockPage {
	mine := withTicker(inWindow(arts, p), ticker)
	rel := relevantNewest(mine, p.Threshold)
	return stockPage{
		Params:   p,
		Ticker:   ticker,
		Name:     name,
		Total:    len(mine),
		Relevant: len(rel),
		Summary:  summarize.Summarize(rel, summaryLines),
		Days:     dayRows(mine, p),
		Weeks:    weekRows(rel, p.Zone),
		Kinds:    kindCounts(rel),
		Domains:  head(domains.Aggregate(mine, p.Threshold), p.Limit),
		Topics:   cloudOf(rel, topicsOf, ""),
		Newest:   head(rel, p.Limit),
	}
}

// topicPage is everything the page of one topic shows.
type topicPage struct {
	Params   pageParams
	Topic    string
	Total    int // articles in the window filed under the topic
	Relevant int
	Summary  summarize.Summary
	Stocks   []tagcloud.Term
	Domains  []domains.Stat
	Days     []dayRow
	Related  []tagcloud.Term
	Top      []model.Article
}

// buildTopicPage assembles the page of topic from arts, which may hold any
// articles: those outside the window or not filed under the topic are ignored.
func buildTopicPage(arts []model.Article, topic string, p pageParams) topicPage {
	mine := withTopic(inWindow(arts, p), topic)
	rel := relevantNewest(mine, p.Threshold)
	return topicPage{
		Params:   p,
		Topic:    topic,
		Total:    len(mine),
		Relevant: len(rel),
		Summary:  summarize.Summarize(rel, summaryLines),
		Stocks:   cloudOf(rel, tickersOf, ""),
		Domains:  head(domains.Aggregate(mine, p.Threshold), p.Limit),
		Days:     dayRows(mine, p),
		Related:  cloudOf(rel, topicsOf, topic),
		Top:      topArticles(rel, p.Limit),
	}
}

// topicRow is one line of the topics table.
type topicRow struct {
	Topic string
	Count int     // relevant articles in the window
	Prior float64 // relevant articles in the window before
	Lift  float64 // Count over Prior, with Prior floored at 0.5
	Tier  int     // the topic's tier in the cloud
}

// topicsPage is the list of all topics.
type topicsPage struct {
	Params   pageParams
	Total    int // articles in the window
	Relevant int
	Cloud    []tagcloud.Term
	Rows     []topicRow
}

// buildTopicsPage counts the topics of the relevant articles of the window of
// p and compares each with the window before it. arts holds the articles of
// both windows; others are ignored. The lift comes
// from package trends: this window's count over the earlier window's count,
// which the package floors at 0.5 so a new topic does not get an infinite lift.
// Rows are sorted by count, then name.
func buildTopicsPage(arts []model.Article, p pageParams) topicsPage {
	window := inWindow(arts, p)
	rel := relevantNewest(window, p.Threshold)
	page := topicsPage{Params: p, Total: len(window), Relevant: len(rel), Cloud: cloudOf(rel, topicsOf, "")}

	// With a baseline of one day the package's per-day average is the whole
	// earlier window, so the lift compares two windows of equal length.
	found := trends.Detect(rel, inWindow(arts, p.previous()), 1,
		trends.Options{Threshold: p.Threshold, MinToday: 1, MinLift: 1e-9})
	byTopic := make(map[string]trends.Trend, len(found))
	for _, t := range found {
		byTopic[t.Topic] = t
	}
	for _, term := range page.Cloud {
		t := byTopic[term.Text]
		page.Rows = append(page.Rows, topicRow{Topic: term.Text, Count: term.Count, Prior: t.Baseline, Lift: t.Lift, Tier: term.Tier})
	}
	return page
}

// stockRow is one line of the stocks table.
type stockRow struct {
	Ticker      string
	Mentions    int // relevant articles that name the ticker
	Materiality float64
	Tone        float64
	Watched     bool
}

// stocksPage is the list of stocks seen in relevant news.
type stocksPage struct {
	Params   pageParams
	Total    int
	Relevant int
	Rows     []stockRow
}

// buildStocksPage counts the tickers of the relevant articles of the window,
// with the mean materiality and tone of the articles that name each, most
// mentioned first. Tickers on the watchlist are marked.
func buildStocksPage(arts []model.Article, watchlist []string, p pageParams) stocksPage {
	window := inWindow(arts, p)
	rel := relevantNewest(window, p.Threshold)
	type sum struct {
		n         int
		mat, tone float64
	}
	by := map[string]*sum{}
	for _, a := range rel {
		seen := map[string]bool{}
		for _, t := range tickersOf(a) {
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			s := by[t]
			if s == nil {
				s = &sum{}
				by[t] = s
			}
			s.n++
			s.mat += float64(a.Score.Materiality)
			s.tone += a.Score.Tone
		}
	}
	page := stocksPage{Params: p, Total: len(window), Relevant: len(rel)}
	for t, s := range by {
		page.Rows = append(page.Rows, stockRow{
			Ticker: t, Mentions: s.n, Materiality: s.mat / float64(s.n), Tone: s.tone / float64(s.n),
			Watched: containsFold(watchlist, t),
		})
	}
	sort.Slice(page.Rows, func(i, j int) bool {
		a, b := page.Rows[i], page.Rows[j]
		if a.Mentions != b.Mentions {
			return a.Mentions > b.Mentions
		}
		if a.Materiality != b.Materiality {
			return a.Materiality > b.Materiality
		}
		return a.Ticker < b.Ticker
	})
	return page
}

// knownTopics lists the topic names in arts and in configured, once each (the
// first spelling wins), sorted.
func knownTopics(arts []model.Article, configured []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.TrimSpace(t)
		if k := strings.ToLower(t); t != "" && !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	for _, t := range configured {
		add(t)
	}
	for _, a := range arts {
		for _, t := range a.Topics {
			add(t)
		}
	}
	sort.Strings(out)
	return out
}

// closestNames returns up to n of known that resemble name, best first: names
// that start with it, then names that contain it, then names it contains.
// Comparison ignores case; ties go alphabetically.
func closestNames(name string, known []string, n int) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	type match struct {
		rank int
		name string
	}
	var found []match
	for _, k := range known {
		lk := strings.ToLower(k)
		switch {
		case lk == name:
		case strings.HasPrefix(lk, name):
			found = append(found, match{0, k})
		case strings.Contains(lk, name):
			found = append(found, match{1, k})
		case utf8.RuneCountInString(lk) >= 3 && strings.Contains(name, lk):
			found = append(found, match{2, k})
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].rank != found[j].rank {
			return found[i].rank < found[j].rank
		}
		return found[i].name < found[j].name
	})
	out := make([]string, 0, min(n, len(found)))
	for _, m := range head(found, n) {
		out = append(out, m.name)
	}
	return out
}

// Text rendering.

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

// describe writes the window of p for a page header.
func (p pageParams) describe() string {
	return fmt.Sprintf("%s to %s (%d days, %s)", p.First.Format(time.DateOnly), p.Last.Format(time.DateOnly), p.Days, p.Zone)
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

func renderDays(w io.Writer, rows []dayRow, loc *time.Location) {
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
func renderStockPage(w io.Writer, pg stockPage, tenantID string) {
	p := pg.Params
	title := pg.Ticker
	if pg.Name != "" {
		title += " - " + pg.Name
	}
	fmt.Fprintf(w, "Stock %s, tenant %s\n", title, tenantID)
	fmt.Fprintf(w, "Window %s\n", p.describe())
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
func renderTopicPage(w io.Writer, pg topicPage, tenantID string) {
	p := pg.Params
	fmt.Fprintf(w, "Topic %s, tenant %s\n", pg.Topic, tenantID)
	fmt.Fprintf(w, "Window %s\n", p.describe())
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
func renderTopicsPage(w io.Writer, pg topicsPage, tenantID string) {
	p := pg.Params
	fmt.Fprintf(w, "Topics, tenant %s\n", tenantID)
	fmt.Fprintf(w, "Window %s\n", p.describe())
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
func renderStocksPage(w io.Writer, pg stocksPage, tenantID string) {
	p := pg.Params
	fmt.Fprintf(w, "Stocks, tenant %s\n", tenantID)
	fmt.Fprintf(w, "Window %s\n", p.describe())
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
