// Package pages builds the read models of the stock, stocks, topic and topics
// pages: pure functions over a slice of articles and a Params, so they are
// tested without a store. The text commands and the terminal interface both
// draw from them, and neither adds a rule of its own. Callers read the
// articles through the tenant's reader only, so licence visibility is applied
// before anything gets here.
package pages

import (
	"fmt"
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

const (
	// CloudMax is the most terms in one tag cloud.
	CloudMax = 30
	// SummaryLines is the most lines in a page summary.
	SummaryLines = 5
	// MaxDays is the longest window a page command accepts.
	MaxDays = 3650
	// ClosestTopics is how many similar topic names an unknown topic lists.
	ClosestTopics = 10
)

// Params say which articles a page covers and how it counts them.
type Params struct {
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

// NewParams returns the window of the last days calendar days in loc,
// ending with the day of now.
func NewParams(now time.Time, days, limit int, threshold float64, loc *time.Location) Params {
	if loc == nil {
		loc = time.UTC
	}
	y, m, d := now.In(loc).Date()
	last := time.Date(y, m, d, 0, 0, 0, 0, loc)
	return Params{
		Threshold: threshold, Limit: limit, Zone: loc, Days: days,
		First: last.AddDate(0, 0, -(days - 1)), Last: last,
	}
}

// End is the first instant after the window.
func (p Params) End() time.Time { return p.Last.AddDate(0, 0, 1) }

// previous returns the window of the same length that ends the day before p
// starts.
func (p Params) Previous() Params {
	q := p
	q.Last = p.First.AddDate(0, 0, -1)
	q.First = q.Last.AddDate(0, 0, -(p.Days - 1))
	return q
}

// InWindow returns the articles published inside the window of p.
func InWindow(arts []model.Article, p Params) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if !a.Published.Before(p.First) && a.Published.Before(p.End()) {
			out = append(out, a)
		}
	}
	return out
}

// WithTicker returns the articles that name ticker, ignoring case.
func WithTicker(arts []model.Article, ticker string) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if ContainsFold(a.Tickers, ticker) {
			out = append(out, a)
		}
	}
	return out
}

// WithTopic returns the articles filed under topic, ignoring case.
func WithTopic(arts []model.Article, topic string) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if ContainsFold(a.Topics, topic) {
			out = append(out, a)
		}
	}
	return out
}

func ContainsFold(list []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return true
		}
	}
	return false
}

// NewestFirst sorts articles by publication time, newest first, then by ID.
func NewestFirst(arts []model.Article) {
	sort.SliceStable(arts, func(i, j int) bool {
		if !arts[i].Published.Equal(arts[j].Published) {
			return arts[i].Published.After(arts[j].Published)
		}
		return arts[i].ID < arts[j].ID
	})
}

// RelevantNewest returns the relevant articles, newest first.
func RelevantNewest(arts []model.Article, threshold float64) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if a.Score.Relevant(threshold) {
			out = append(out, a)
		}
	}
	NewestFirst(out)
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

// TopArticles returns up to limit articles, most important first.
func TopArticles(arts []model.Article, limit int) []model.Article {
	out := append([]model.Article(nil), arts...)
	sort.SliceStable(out, func(i, j int) bool { return moreImportant(out[i], out[j]) })
	return Head(out, limit)
}

// Head returns the first n elements of s, or all of them.
func Head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// DayRow is one line of the day-by-day timeline.
type DayRow struct {
	timeline.Day
	// Top is the most important relevant article of the day, if HasTop.
	Top    model.Article
	HasTop bool
}

// DayRows builds the day-by-day timeline of arts over the window of p, with
// every day of it, empty ones included. Days are calendar days in p.Zone.
func DayRows(arts []model.Article, p Params) []DayRow {
	days := timeline.Daily(arts, p.Threshold, p.First, p.Last, p.Zone)
	rows := make([]DayRow, len(days))
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

// WeekRow is the mean materiality and tone of one week's relevant articles.
type WeekRow struct {
	Week        time.Time // the Monday, midnight in the zone
	Articles    int
	Materiality float64
	Tone        float64
}

// WeekRows averages materiality and tone per week, weeks starting on Monday in
// loc, oldest first. Only weeks that have articles appear.
func WeekRows(arts []model.Article, loc *time.Location) []WeekRow {
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
	rows := make([]WeekRow, 0, len(byWeek))
	for _, s := range byWeek {
		rows = append(rows, WeekRow{Week: s.week, Articles: s.n, Materiality: s.mat / float64(s.n), Tone: s.tone / float64(s.n)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Week.Before(rows[j].Week) })
	return rows
}

// CountRow is a name and how often it occurs.
type CountRow struct {
	Name  string
	Count int
}

// KindCounts counts the articles per event kind, the most frequent first.
func KindCounts(arts []model.Article) []CountRow {
	counts := map[string]int{}
	for _, a := range arts {
		counts[KindOf(a)]++
	}
	var rows []CountRow
	for k, n := range counts {
		rows = append(rows, CountRow{Name: k, Count: n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// CloudOf counts the values pick returns for each article, once per article,
// leaves out skip (ignoring case) and builds the tag cloud of the most frequent.
func CloudOf(arts []model.Article, pick func(model.Article) []string, skip string) []tagcloud.Term {
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
	return tagcloud.Build(counts, CloudMax)
}

func TopicsOf(a model.Article) []string { return a.Topics }

func TickersOf(a model.Article) []string {
	out := make([]string, len(a.Tickers))
	for i, t := range a.Tickers {
		out[i] = strings.ToUpper(strings.TrimSpace(t))
	}
	return out
}

// StockPage is everything the page of one stock shows.
type StockPage struct {
	Params   Params
	Ticker   string
	Name     string
	Total    int // articles in the window that name the ticker
	Relevant int
	Summary  summarize.Summary
	Days     []DayRow
	Weeks    []WeekRow
	Kinds    []CountRow
	Domains  []domains.Stat
	Topics   []tagcloud.Term
	Newest   []model.Article
}

// BuildStockPage assembles the page of ticker from arts, which may hold any
// articles: those outside the window or not naming the ticker are ignored.
// name is the company name, empty if unknown.
func BuildStockPage(arts []model.Article, ticker, name string, p Params) StockPage {
	mine := WithTicker(InWindow(arts, p), ticker)
	rel := RelevantNewest(mine, p.Threshold)
	return StockPage{
		Params:   p,
		Ticker:   ticker,
		Name:     name,
		Total:    len(mine),
		Relevant: len(rel),
		Summary:  summarize.Summarize(rel, SummaryLines),
		Days:     DayRows(mine, p),
		Weeks:    WeekRows(rel, p.Zone),
		Kinds:    KindCounts(rel),
		Domains:  Head(domains.Aggregate(mine, p.Threshold), p.Limit),
		Topics:   CloudOf(rel, TopicsOf, ""),
		Newest:   Head(rel, p.Limit),
	}
}

// TopicPage is everything the page of one topic shows.
type TopicPage struct {
	Params   Params
	Topic    string
	Total    int // articles in the window filed under the topic
	Relevant int
	Summary  summarize.Summary
	Stocks   []tagcloud.Term
	Domains  []domains.Stat
	Days     []DayRow
	Related  []tagcloud.Term
	Top      []model.Article
}

// BuildTopicPage assembles the page of topic from arts, which may hold any
// articles: those outside the window or not filed under the topic are ignored.
func BuildTopicPage(arts []model.Article, topic string, p Params) TopicPage {
	mine := WithTopic(InWindow(arts, p), topic)
	rel := RelevantNewest(mine, p.Threshold)
	return TopicPage{
		Params:   p,
		Topic:    topic,
		Total:    len(mine),
		Relevant: len(rel),
		Summary:  summarize.Summarize(rel, SummaryLines),
		Stocks:   CloudOf(rel, TickersOf, ""),
		Domains:  Head(domains.Aggregate(mine, p.Threshold), p.Limit),
		Days:     DayRows(mine, p),
		Related:  CloudOf(rel, TopicsOf, topic),
		Top:      TopArticles(rel, p.Limit),
	}
}

// TopicRow is one line of the topics table.
type TopicRow struct {
	Topic string
	Count int     // relevant articles in the window
	Prior float64 // relevant articles in the window before
	Lift  float64 // Count over Prior, with Prior floored at 0.5
	Tier  int     // the topic's tier in the cloud
}

// TopicsPage is the list of all topics.
type TopicsPage struct {
	Params   Params
	Total    int // articles in the window
	Relevant int
	Cloud    []tagcloud.Term
	Rows     []TopicRow
}

// BuildTopicsPage counts the topics of the relevant articles of the window of
// p and compares each with the window before it. arts holds the articles of
// both windows; others are ignored. The lift comes
// from package trends: this window's count over the earlier window's count,
// which the package floors at 0.5 so a new topic does not get an infinite lift.
// Rows are sorted by count, then name.
func BuildTopicsPage(arts []model.Article, p Params) TopicsPage {
	window := InWindow(arts, p)
	rel := RelevantNewest(window, p.Threshold)
	page := TopicsPage{Params: p, Total: len(window), Relevant: len(rel), Cloud: CloudOf(rel, TopicsOf, "")}

	// With a baseline of one day the package's per-day average is the whole
	// earlier window, so the lift compares two windows of equal length.
	found := trends.Detect(rel, InWindow(arts, p.Previous()), 1,
		trends.Options{Threshold: p.Threshold, MinToday: 1, MinLift: 1e-9})
	byTopic := make(map[string]trends.Trend, len(found))
	for _, t := range found {
		byTopic[t.Topic] = t
	}
	for _, term := range page.Cloud {
		t := byTopic[term.Text]
		page.Rows = append(page.Rows, TopicRow{Topic: term.Text, Count: term.Count, Prior: t.Baseline, Lift: t.Lift, Tier: term.Tier})
	}
	return page
}

// StockRow is one line of the stocks table.
type StockRow struct {
	Ticker      string
	Mentions    int // relevant articles that name the ticker
	Materiality float64
	Tone        float64
	Watched     bool
}

// StocksPage is the list of stocks seen in relevant news.
type StocksPage struct {
	Params   Params
	Total    int
	Relevant int
	Rows     []StockRow
}

// BuildStocksPage counts the tickers of the relevant articles of the window,
// with the mean materiality and tone of the articles that name each, most
// mentioned first. Tickers on the watchlist are marked.
func BuildStocksPage(arts []model.Article, watchlist []string, p Params) StocksPage {
	window := InWindow(arts, p)
	rel := RelevantNewest(window, p.Threshold)
	type sum struct {
		n         int
		mat, tone float64
	}
	by := map[string]*sum{}
	for _, a := range rel {
		seen := map[string]bool{}
		for _, t := range TickersOf(a) {
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
	page := StocksPage{Params: p, Total: len(window), Relevant: len(rel)}
	for t, s := range by {
		page.Rows = append(page.Rows, StockRow{
			Ticker: t, Mentions: s.n, Materiality: s.mat / float64(s.n), Tone: s.tone / float64(s.n),
			Watched: ContainsFold(watchlist, t),
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

// KnownTopics lists the topic names in arts and in configured, once each (the
// first spelling wins), sorted.
func KnownTopics(arts []model.Article, configured []string) []string {
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

// ClosestNames returns up to n of known that resemble name, best first: names
// that start with it, then names that contain it, then names it contains.
// Comparison ignores case; ties go alphabetically.
func ClosestNames(name string, known []string, n int) []string {
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
	for _, m := range Head(found, n) {
		out = append(out, m.name)
	}
	return out
}

// Text rendering.

// Describe writes the window of p for a page header.
func (p Params) Describe() string {
	return fmt.Sprintf("%s to %s (%d days, %s)", p.First.Format(time.DateOnly), p.Last.Format(time.DateOnly), p.Days, p.Zone)
}

// KindOf is the event kind of an article, or "-" if it has none.
func KindOf(a model.Article) string {
	if a.Score.Kind == "" {
		return "-"
	}
	return string(a.Score.Kind)
}
