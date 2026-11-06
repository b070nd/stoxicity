package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/domains"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/pages"
	"github.com/b070nd/stoxicity/app/src/status"
	"github.com/b070nd/stoxicity/app/src/summarize"
	"github.com/b070nd/stoxicity/app/src/tagcloud"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// Style names how a row is drawn; the shell maps it to colours.
type Style int

const (
	Plain Style = iota
	Heading
	Dim
	Good
	Bad
	Warn
	Cloud1 // Cloud1..Cloud5 are the tag cloud tiers
	Cloud2
	Cloud3
	Cloud4
	Cloud5
)

// Row is one line of a screen. A row is selectable when it carries an article
// or a route; enter opens it.
type Row struct {
	Text     string
	Style    Style
	Article  *model.Article
	Go       *nav.Route
	Watch    string // enter switches to this watch (the Watches screen)
	HasWatch bool
	URL      string // b opens this web address
	// Key names the row for the cursor to follow across a reload and a re-sort
	// when nothing else does (a source has no article, route or watch).
	Key string
	// Chips makes the row a line of chips (the exchange filter): not a row the
	// cursor stops on, but a zone you step up into from the first row and move
	// along with the left and right arrows.
	Chips []Chip
}

// Chip is one entry of a line of chips. Picking it makes Value the exchange
// every screen is narrowed to ("" is all of them).
type Chip struct {
	Text  string
	Style Style // how heavy it is drawn: the cloud tier of its count
	Value string
}

// Selectable reports whether the cursor can stop on the row.
func (r Row) Selectable() bool {
	return r.Article != nil || r.Go != nil || r.HasWatch || r.URL != "" || r.Key != ""
}

// Identity is what makes the row the same row after the screen is rebuilt:
// the cursor follows it when the rows are re-sorted or reloaded. Empty for a
// row that cannot hold the cursor.
func (r Row) Identity() string {
	switch {
	case r.Key != "":
		return "k:" + r.Key
	case r.Article != nil:
		return "a:" + r.Article.ID
	case r.Go != nil:
		return "g:" + r.Go.String()
	case r.HasWatch:
		return "w:" + r.Watch
	case r.URL != "":
		return "u:" + r.URL
	}
	return ""
}

// Screen is what a view shows: a title and its rows.
type Screen struct {
	Title string
	Rows  []Row
	// Table and Sort say how the rows of a screen with a table are ordered and
	// by which columns they can be (Table holds no items).
	Table *Table
	Sort  Sort
	// Group is how the rows are arranged in groups ("exchange", "kind",
	// "industry" or "none"); empty for a screen that has none.
	Group string
	// Empty is shown when there are no rows besides the title.
	Notice string
}

// Data is everything a screen is built from.
type Data struct {
	Params    pages.Params
	Articles  []model.Article // the window, plus the one before it
	Watchlist []string
	Topics    []string
	Name      func(ticker string) string
	Verdicts  map[string]labels.Verdict
	Sources   []status.Source
	Watches   []WatchInfo
	WatchID   string
	Now       time.Time
	Truncated bool
	// Universe is every known company; Following names the watches that follow
	// a ticker. Either may be missing.
	Universe  *universe.Universe
	Following func(ticker string) []string
	// Feeds lists the feeds recorded for a ticker; it may be missing.
	Feeds func(ticker string) []FeedInfo
	// Chosen is the exchange the user narrowed everything to with X, even where
	// the screen shows more (the list of exchanges marks it).
	Chosen string
	// Exchange is the exchange the screen is narrowed to; Articles holds the news
	// of its stocks only. Empty for none.
	Exchange string
	// Latest is the newest news of the stock whose page is built, of any age,
	// newest first; LatestOK says it has been asked for.
	Latest   []model.Article
	LatestOK bool
	// Sort is the order the user chose for the screen's table; the zero Sort
	// leaves it in its default order.
	Sort Sort
	// Group is the grouping the user chose for the catalogue; empty leaves it to
	// the query and then to the default, by exchange.
	Group string
}

func (d Data) name(t string) string {
	if d.Name == nil {
		return ""
	}
	return d.Name(t)
}

// homeRows is how many rows each Home section shows.
const homeRows = 8

// cloudWidth is where the tag cloud wraps.
const cloudWidth = 76

// Build makes the screen of a route. It is a pure function of its inputs.
func Build(r nav.Route, d Data) Screen {
	s := build(r, d)
	if d.Exchange != "" && r.Kind != nav.Exchange && s.Notice != "loading…" {
		s.Title += " · exchange " + d.Exchange
	}
	return s
}

func build(r nav.Route, d Data) Screen {
	switch r.Kind {
	case nav.Home:
		return buildHome(d)
	case nav.Stocks:
		return buildStocks(d)
	case nav.Stock:
		return buildStock(r.Arg, d)
	case nav.Companies:
		return buildCompanies(r.Arg, d)
	case nav.Exchanges:
		return buildExchanges(d)
	case nav.Exchange:
		return buildExchange(r.Arg, d)
	case nav.Topics:
		return buildTopics(d)
	case nav.Topic:
		return buildTopic(r.Arg, d)
	case nav.Domains:
		return buildDomains(d)
	case nav.Domain:
		return buildDomain(r.Arg, d)
	case nav.Day:
		return buildDay(r.Arg, d)
	case nav.Search:
		return buildSearch(r.Arg, d)
	case nav.Sources:
		return buildSources(d)
	case nav.Watches:
		return buildWatches(d)
	}
	return Screen{Title: r.Label(), Notice: "no such screen"}
}

func route(k nav.Kind, arg string) *nav.Route { return &nav.Route{Kind: k, Arg: arg} }

type builder struct{ rows []Row }

func (b *builder) add(r Row)                       { b.rows = append(b.rows, r) }
func (b *builder) text(s string)                   { b.add(Row{Text: s}) }
func (b *builder) styled(st Style, s string)       { b.add(Row{Text: s, Style: st}) }
func (b *builder) blank()                          { b.add(Row{}) }
func (b *builder) heading(s string, to *nav.Route) { b.add(Row{Text: s, Style: Heading, Go: to}) }
func (b *builder) article(a model.Article, d Data) {
	a2 := a
	b.add(Row{Text: articleLine(a, d), Style: articleStyle(a, d), Article: &a2})
}
func (b *builder) articles(list []model.Article, d Data) {
	for _, a := range list {
		b.article(a, d)
	}
}

func articleStyle(a model.Article, d Data) Style {
	if !a.Score.Relevant(d.Params.Threshold) {
		return Dim
	}
	return Plain
}

// articleLine is the one-line form of an article.
func articleLine(a model.Article, d Data) string {
	zone := d.Params.Zone
	if zone == nil {
		zone = time.UTC
	}
	mark := " "
	switch d.Verdicts[a.ID] {
	case labels.Keep:
		mark = "+"
	case labels.Drop:
		mark = "-"
	}
	tick := ""
	if len(a.Tickers) > 0 {
		tick = " [" + strings.Join(pages.Head(a.Tickers, 3), " ") + "]"
	}
	return fmt.Sprintf("%s %s  M%d %s  %s  %s%s", mark, a.Published.In(zone).Format("01-02 15:04"),
		a.Score.Materiality, toneMark(a.Score.Tone), a.Title, a.Domain, tick)
}

func toneMark(t float64) string {
	switch {
	case t >= 0.25:
		return "▲"
	case t <= -0.25:
		return "▼"
	}
	return "·"
}

func tonePct(t float64) string { return fmt.Sprintf("%+.2f", t) }

// bar draws n of max as a bar of at most width cells, with the first rel cells
// solid and the rest light.
func bar(total, rel, max, width int) string {
	if max <= 0 || total <= 0 {
		return ""
	}
	t := max2(1, total*width/max)
	r := rel * width / max
	if rel > 0 && r == 0 {
		r = 1
	}
	if r > t {
		t = r
	}
	return strings.Repeat("█", r) + strings.Repeat("░", t-r)
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func timelineRows(b *builder, days []pages.DayRow) {
	most := 0
	for _, day := range days {
		most = max2(most, day.Total)
	}
	for i := len(days) - 1; i >= 0; i-- { // newest first
		day := days[i]
		date := day.Date.Format("2006-01-02")
		r := Row{
			Text: fmt.Sprintf("%s %s  %-20s %d/%d", day.Date.Format("Mon"), date, bar(day.Total, day.Relevant, most, 20), day.Relevant, day.Total),
			Go:   route(nav.Day, date),
		}
		if day.Total == 0 {
			r.Style = Dim
		}
		b.add(r)
	}
}

// cloudRows wraps the cloud into lines; the lines are not selectable.
func cloudRows(b *builder, terms []tagcloud.Term, max int) {
	terms = pages.Head(terms, max)
	if len(terms) == 0 {
		return
	}
	// One row per tier run keeps each word styled by its own weight: a row
	// carries one style, so a line holds words of one tier.
	for tier := tagcloud.Tiers; tier >= 1; tier-- {
		var line []string
		flush := func() {
			if len(line) > 0 {
				b.add(Row{Text: strings.Join(line, "  "), Style: Cloud1 + Style(tier-1)})
				line = nil
			}
		}
		width := 0
		for _, t := range terms {
			if t.Tier != tier {
				continue
			}
			w := fmt.Sprintf("%s(%d)", t.Text, t.Count)
			if width+len(w)+2 > cloudWidth {
				flush()
				width = 0
			}
			line = append(line, w)
			width += len(w) + 2
		}
		flush()
	}
}

// summaryRows adds the lines of a summary; each opens the article it came from.
func summaryRows(b *builder, s summarize.Summary) {
	for _, l := range s.Lines {
		a := l.Article
		b.add(Row{Text: "• " + l.Text, Article: &a})
	}
}

func buildHome(d Data) Screen {
	p := d.Params
	window := pages.InWindow(d.Articles, p)
	rel := pages.RelevantNewest(window, p.Threshold)
	s := Screen{Title: "Home · " + p.Describe()}
	if len(window) == 0 {
		s.Notice = "No articles in this window. Run `stoxicity fetch` or `stoxicity run`."
		return s
	}
	var b builder
	b.styled(Dim, fmt.Sprintf("%d articles, %d relevant", len(window), len(rel)))
	exchangeChips(&b, d)
	b.blank()
	b.heading("Digest", nil)
	sum := summarize.Summarize(rel, pages.SummaryLines)
	if len(sum.Lines) == 0 {
		b.styled(Dim, "nothing relevant yet")
	}
	summaryRows(&b, sum)
	b.blank()
	b.heading("Top articles ›", route(nav.Search, "after:"+p.First.Format(time.DateOnly)))
	b.articles(pages.TopArticles(rel, homeRows), d)

	stocks := pages.BuildStocksPage(d.Articles, d.Watchlist, p)
	b.blank()
	b.heading("Stocks ›", &nav.Route{Kind: nav.Stocks})
	b.rows = append(b.rows, stockTable(pages.Head(stocks.Rows, homeRows), d).Body(Sort{})...)

	doms := pages.Head(domains.Aggregate(window, p.Threshold), homeRows)
	b.blank()
	b.heading("Domains ›", &nav.Route{Kind: nav.Domains})
	for _, st := range doms {
		b.add(domainLine(st))
	}

	topics := pages.BuildTopicsPage(d.Articles, p)
	b.blank()
	b.heading("Topics ›", &nav.Route{Kind: nav.Topics})
	cloudRows(&b, topics.Cloud, 24)

	b.blank()
	b.heading("Timeline", nil)
	timelineRows(&b, pages.DayRows(window, p))
	s.Rows = b.rows
	return s
}

// stockColumns lay out a list of stocks; the first column holds the star.
var stockColumns = []Column{
	{Width: 1},
	{Title: "TICKER", Key: "ticker", Width: 6},
	{Title: "NAME", Key: "name", Width: 28},
	{Title: "NEWS", Key: "mentions", Width: 4, Right: true, Desc: true},
	{Title: "MAT", Key: "materiality", Width: 4, Right: true, Desc: true},
	{Title: "TONE", Key: "tone", Width: 5, Right: true, Desc: true},
}

// stockTable is the stocks of the news, most mentioned first unless sorted.
func stockTable(rows []pages.StockRow, d Data) Table {
	t := Table{Columns: stockColumns, Default: Sort{Key: "mentions", Desc: true}}
	for _, r := range rows {
		star := " "
		if r.Watched {
			star = "★"
		}
		t.Items = append(t.Items, Item{
			Cells: []Cell{
				Str(star),
				Str(r.Ticker),
				Str(clip(d.name(r.Ticker), 28)),
				Num(fmt.Sprint(r.Mentions), float64(r.Mentions)),
				Num(fmt.Sprintf("%.1f", r.Materiality), r.Materiality),
				Num(tonePct(r.Tone), r.Tone),
			},
			Row: Row{Go: route(nav.Stock, r.Ticker)},
		})
	}
	return t
}

func domainLine(st domains.Stat) Row {
	return Row{
		Text: fmt.Sprintf("  %-32s %3d relevant of %-4d %3.0f%%", clip(st.Domain, 32), st.Relevant, st.Total, st.Ratio()*100),
		Go:   route(nav.Domain, st.Domain),
	}
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func buildStocks(d Data) Screen {
	page := pages.BuildStocksPage(d.Articles, d.Watchlist, d.Params)
	s := Screen{Title: "Stocks · " + d.Params.Describe()}
	if len(page.Rows) == 0 {
		s.Notice = "No stock appears in relevant news in this window."
		return s
	}
	var b builder
	b.styled(Dim, fmt.Sprintf("%d stocks in %d relevant articles (★ = on your watchlist) · < > pick the column, I turns it round", len(page.Rows), page.Relevant))
	t := stockTable(page.Rows, d)
	b.rows = append(b.rows, t.Rows(d.Sort)...)
	s.Rows, s.Table, s.Sort = b.rows, t.shape(), t.Effective(d.Sort)
	return s
}

func buildStock(ticker string, d Data) Screen {
	ticker = strings.ToUpper(ticker)
	page := pages.BuildStockPage(d.Articles, ticker, d.name(ticker), d.Params)
	title := "Stock " + ticker
	if page.Name != "" {
		title += " · " + page.Name
	}
	s := Screen{Title: title}
	var b builder
	known := profileRows(&b, ticker, d)
	if page.Total == 0 && len(d.Latest) == 0 {
		if !known {
			s.Notice = "No article names " + ticker + " in this window, and no company list knows it. Run `stoxicity universe update`, or tag it with # to keep it."
			return s
		}
		b.blank()
		b.heading("News", nil)
		switch {
		case !d.LatestOK:
			b.styled(Dim, "  Loading the latest news…")
		default:
			b.styled(Dim, "  No article names "+ticker+", in "+d.Params.Describe()+" or before.")
		}
		s.Rows = b.rows
		return s
	}
	if known {
		b.blank()
	}
	latestRows(&b, ticker, d)
	if page.Total == 0 {
		b.blank()
		b.styled(Dim, "No article names "+ticker+" in "+d.Params.Describe()+": only older news is shown.")
		s.Rows = b.rows
		return s
	}
	b.blank()
	b.styled(Dim, fmt.Sprintf("%d articles, %d relevant · %s", page.Total, page.Relevant, d.Params.Describe()))
	if len(page.Summary.Lines) > 0 {
		b.blank()
		b.heading("Summary", nil)
		summaryRows(&b, page.Summary)
	}
	b.blank()
	b.heading("Timeline", nil)
	timelineRows(&b, page.Days)
	if len(page.Weeks) > 0 {
		b.blank()
		b.heading("Weekly tone and materiality", nil)
		for _, w := range page.Weeks {
			st := Plain
			if w.Tone <= -0.25 {
				st = Bad
			} else if w.Tone >= 0.25 {
				st = Good
			}
			b.styled(st, fmt.Sprintf("week of %s  %3d articles  mat %.1f  tone %s", w.Week.Format("2006-01-02"), w.Articles, w.Materiality, tonePct(w.Tone)))
		}
	}
	if len(page.Kinds) > 0 {
		b.blank()
		b.heading("What happened", nil)
		for _, k := range page.Kinds {
			b.text(fmt.Sprintf("  %-28s %d", k.Name, k.Count))
		}
	}
	if len(page.Domains) > 0 {
		b.blank()
		b.heading("Domains ›", &nav.Route{Kind: nav.Domains})
		for _, st := range page.Domains {
			b.add(domainLine(st))
		}
	}
	if len(page.Topics) > 0 {
		b.blank()
		b.heading("Topics", nil)
		for _, t := range pages.Head(page.Topics, 10) {
			b.add(Row{Text: fmt.Sprintf("  %-28s %d", t.Text, t.Count), Go: route(nav.Topic, t.Text)})
		}
	}
	b.blank()
	b.heading("Articles", nil)
	b.articles(page.Newest, d)
	s.Rows = b.rows
	return s
}

func buildTopics(d Data) Screen {
	page := pages.BuildTopicsPage(d.Articles, d.Params)
	s := Screen{Title: "Topics · " + d.Params.Describe()}
	if len(page.Rows) == 0 {
		s.Notice = "No topic appears in relevant news in this window."
		return s
	}
	var b builder
	b.styled(Dim, fmt.Sprintf("%d topics in %d relevant articles; lift compares with the window before", len(page.Rows), page.Relevant))
	cloudRows(&b, page.Cloud, 40)
	b.blank()
	t := Table{
		Columns: []Column{
			{Title: "TOPIC", Key: "topic", Width: 28},
			{Title: "NEWS", Key: "count", Width: 5, Right: true, Desc: true},
			{Title: "BEFORE", Key: "before", Width: 7, Right: true, Desc: true},
			{Title: "LIFT", Key: "lift", Width: 6, Right: true, Desc: true},
		},
		Default: Sort{Key: "count", Desc: true},
	}
	for _, r := range page.Rows {
		t.Items = append(t.Items, Item{
			Cells: []Cell{
				Str(r.Topic), Num(fmt.Sprint(r.Count), float64(r.Count)),
				Num(fmt.Sprintf("%.1f", r.Prior), r.Prior), Num(fmt.Sprintf("%.1fx", r.Lift), r.Lift),
			},
			Row: Row{Go: route(nav.Topic, r.Topic)},
		})
	}
	b.rows = append(b.rows, t.Rows(d.Sort)...)
	s.Rows, s.Table, s.Sort = b.rows, t.shape(), t.Effective(d.Sort)
	return s
}

func buildTopic(topic string, d Data) Screen {
	page := pages.BuildTopicPage(d.Articles, topic, d.Params)
	s := Screen{Title: "Topic " + topic}
	if page.Total == 0 {
		s.Notice = "No article is filed under " + topic + " in this window."
		if near := pages.ClosestNames(topic, pages.KnownTopics(d.Articles, d.Topics), 5); len(near) > 0 {
			s.Notice += " Did you mean: " + strings.Join(near, ", ") + "?"
		}
		return s
	}
	var b builder
	b.styled(Dim, fmt.Sprintf("%d articles, %d relevant · %s", page.Total, page.Relevant, d.Params.Describe()))
	if len(page.Summary.Lines) > 0 {
		b.blank()
		b.heading("Summary", nil)
		summaryRows(&b, page.Summary)
	}
	if len(page.Stocks) > 0 {
		b.blank()
		b.heading("Stocks ›", &nav.Route{Kind: nav.Stocks})
		for _, t := range pages.Head(page.Stocks, 10) {
			b.add(Row{Text: fmt.Sprintf("  %-8s %-28s %d", t.Text, clip(d.name(t.Text), 28), t.Count), Go: route(nav.Stock, t.Text)})
		}
	}
	if len(page.Domains) > 0 {
		b.blank()
		b.heading("Domains ›", &nav.Route{Kind: nav.Domains})
		for _, st := range page.Domains {
			b.add(domainLine(st))
		}
	}
	b.blank()
	b.heading("Timeline", nil)
	timelineRows(&b, page.Days)
	if len(page.Related) > 0 {
		b.blank()
		b.heading("Related topics", nil)
		for _, t := range pages.Head(page.Related, 10) {
			b.add(Row{Text: fmt.Sprintf("  %-28s %d", t.Text, t.Count), Go: route(nav.Topic, t.Text)})
		}
	}
	b.blank()
	b.heading("Top articles", nil)
	b.articles(page.Top, d)
	s.Rows = b.rows
	return s
}

func buildDomains(d Data) Screen {
	window := pages.InWindow(d.Articles, d.Params)
	stats := domains.Aggregate(window, d.Params.Threshold)
	s := Screen{Title: "Domains · " + d.Params.Describe()}
	if len(stats) == 0 {
		s.Notice = "No articles in this window."
		return s
	}
	var b builder
	b.styled(Dim, fmt.Sprintf("%d domains; relevant against total", len(stats)))
	t := Table{
		Columns: []Column{
			{Title: "DOMAIN", Key: "domain", Width: 32},
			{Title: "RELEVANT", Key: "relevant", Width: 9, Right: true, Desc: true},
			{Title: "TOTAL", Key: "total", Width: 6, Right: true, Desc: true},
			{Title: "SHARE", Key: "share", Width: 6, Right: true, Desc: true},
		},
		Default: Sort{Key: "relevant", Desc: true},
	}
	for _, st := range stats {
		t.Items = append(t.Items, Item{
			Cells: []Cell{
				Str(st.Domain), Num(fmt.Sprint(st.Relevant), float64(st.Relevant)),
				Num(fmt.Sprint(st.Total), float64(st.Total)),
				Num(fmt.Sprintf("%.0f%%", st.Ratio()*100), st.Ratio()),
			},
			Row: Row{Go: route(nav.Domain, st.Domain)},
		})
	}
	b.rows = append(b.rows, t.Rows(d.Sort)...)
	s.Rows, s.Table, s.Sort = b.rows, t.shape(), t.Effective(d.Sort)
	return s
}

func domainArticles(arts []model.Article, domain string) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if strings.EqualFold(strings.TrimPrefix(a.Domain, "www."), domain) {
			out = append(out, a)
		}
	}
	return out
}

func buildDomain(domain string, d Data) Screen {
	window := pages.InWindow(d.Articles, d.Params)
	mine := domainArticles(window, domain)
	s := Screen{Title: "Domain " + domain}
	if len(mine) == 0 {
		s.Notice = "No article from " + domain + " in this window."
		return s
	}
	rel := pages.RelevantNewest(mine, d.Params.Threshold)
	var b builder
	b.styled(Dim, fmt.Sprintf("%d articles, %d relevant (%.0f%%) · %s", len(mine), len(rel),
		100*float64(len(rel))/float64(len(mine)), d.Params.Describe()))
	// Precision against the user's labels: of the articles the lens called
	// relevant and the user judged, how many did the user keep.
	var keep, drop int
	for _, a := range rel {
		switch d.Verdicts[a.ID] {
		case labels.Keep:
			keep++
		case labels.Drop:
			drop++
		}
	}
	if keep+drop > 0 {
		b.styled(Dim, fmt.Sprintf("your labels on its relevant articles: %d kept, %d dropped (precision %.0f%%)",
			keep, drop, 100*float64(keep)/float64(keep+drop)))
	} else {
		b.styled(Dim, "no labels yet on its relevant articles (+ keeps, - drops)")
	}
	b.blank()
	b.heading("Timeline", nil)
	timelineRows(&b, pages.DayRows(mine, d.Params))
	b.blank()
	b.heading("Articles", nil)
	b.articles(pages.Head(mine, max2(d.Params.Limit, 20)), d)
	s.Rows = b.rows
	return s
}

func buildDay(day string, d Data) Screen {
	s := Screen{Title: "Day " + day}
	if len(d.Articles) == 0 {
		s.Notice = "No articles on " + day + "."
		return s
	}
	rel := pages.RelevantNewest(d.Articles, d.Params.Threshold)
	var b builder
	b.styled(Dim, fmt.Sprintf("%d articles, %d relevant", len(d.Articles), len(rel)))
	if sum := summarize.Summarize(rel, pages.SummaryLines); len(sum.Lines) > 0 {
		b.blank()
		b.heading("Digest", nil)
		summaryRows(&b, sum)
	}
	b.blank()
	b.heading("Relevant", nil)
	b.articles(pages.TopArticles(rel, len(rel)), d)
	var rest []model.Article
	for _, a := range d.Articles {
		if !a.Score.Relevant(d.Params.Threshold) {
			rest = append(rest, a)
		}
	}
	if len(rest) > 0 {
		b.blank()
		b.heading(fmt.Sprintf("Not relevant (%d)", len(rest)), nil)
		b.articles(rest, d)
	}
	s.Rows = b.rows
	return s
}

func buildSearch(q string, d Data) Screen {
	s := Screen{Title: "Search: " + q}
	if len(d.Articles) == 0 {
		s.Notice = "Nothing matches."
		return s
	}
	var b builder
	note := fmt.Sprintf("%d articles", len(d.Articles))
	if d.Truncated {
		note += " (cut at the limit)"
	}
	b.styled(Dim, note)
	stats := domains.Aggregate(d.Articles, d.Params.Threshold)
	if len(stats) > 0 {
		var parts []string
		for _, st := range pages.Head(stats, 6) {
			parts = append(parts, fmt.Sprintf("%s %d", st.Domain, st.Total))
		}
		b.styled(Dim, "domains: "+strings.Join(parts, " · "))
	}
	kinds := pages.KindCounts(d.Articles)
	if len(kinds) > 0 {
		var parts []string
		for _, k := range pages.Head(kinds, 6) {
			parts = append(parts, fmt.Sprintf("%s %d", k.Name, k.Count))
		}
		b.styled(Dim, "events: "+strings.Join(parts, " · "))
	}
	b.blank()
	b.articles(d.Articles, d)
	s.Rows = b.rows
	return s
}

func buildSources(d Data) Screen {
	s := Screen{Title: "Source health"}
	if len(d.Sources) == 0 {
		s.Notice = "No sources are configured."
		return s
	}
	zone := d.Params.Zone
	if zone == nil {
		zone = time.UTC
	}
	t := Table{
		Columns: []Column{
			{Title: "ID", Key: "id", Width: 21},
			{Title: "KIND", Key: "kind", Width: 8},
			{Title: "LICENCE", Key: "licence", Width: 14},
			{Title: "HEALTH", Key: "health", Width: 7},
			{Title: "STATE", Key: "state", Width: 8},
			{Title: "LAST OK", Key: "lastok", Width: 17, Desc: true},
			{Title: "NEXT", Key: "next", Width: 6},
			{Title: "FAILS", Key: "fails", Width: 5, Right: true, Desc: true},
			{Title: "LAST ERROR"},
		},
		Default: Sort{Key: "health"},
	}
	rows := append([]status.Source(nil), d.Sources...)
	status.Sort(rows)
	for _, r := range rows {
		health, last, next, fails := string(r.Level), "-", "-", ""
		if health == "" {
			health = "-"
		}
		if !r.LastOK.IsZero() {
			last = r.LastOK.In(zone).Format("2006-01-02 15:04")
		}
		if !r.Until.IsZero() {
			next = r.Until.In(zone).Format("15:04")
		}
		if r.Failures > 0 {
			fails = fmt.Sprint(r.Failures)
		}
		st := Plain
		switch r.Level {
		case status.Red:
			st = Bad
		case status.Yellow:
			st = Warn
		case status.Green:
			st = Good
		}
		if !r.Enabled {
			st = Dim
		}
		t.Items = append(t.Items, Item{
			Cells: []Cell{
				Str(r.ID), Str(r.Kind), Str(r.Licence),
				Num(health, float64(r.Needs())), Str(string(r.State)),
				Num(last, float64(r.LastOK.Unix())), Num(next, dueNum(r.Until)),
				Num(fails, float64(r.Failures)), Str(oneLine(r.LastError)),
			},
			Row: Row{Style: st, Key: "source:" + r.ID},
		})
	}
	s.Rows, s.Table, s.Sort = t.Rows(d.Sort), t.shape(), t.Effective(d.Sort)
	return s
}

// dueNum sorts a time to come by when it comes, and a missing one last.
func dueNum(t time.Time) float64 {
	if t.IsZero() {
		return 1e18
	}
	return float64(t.Unix())
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func buildWatches(d Data) Screen {
	s := Screen{Title: "Watches"}
	var b builder
	b.styled(Dim, "enter shows a watch; watches are YAML files in tenants/<tenant>/watches")
	b.add(Row{Text: fmt.Sprintf("  %-24s %s", "All news", "every visible article"), Watch: "", HasWatch: true})
	for _, w := range d.Watches {
		unread := ""
		if w.Unread > 0 {
			unread = fmt.Sprintf("%d unread", w.Unread)
		}
		cur := " "
		if w.ID == d.WatchID {
			cur = "›"
		}
		b.add(Row{Text: fmt.Sprintf("%s %-24s %s", cur, clip(w.Name, 24), unread), Watch: w.ID, HasWatch: true})
	}
	s.Rows = b.rows
	return s
}

// latestShown is how many of the newest articles the stock page lists.
const latestShown = 12

// latestRows lists the newest news about a stock, whatever its age and whether
// or not it passed the relevance threshold (the dim ones did not).
func latestRows(b *builder, ticker string, d Data) {
	b.heading("Latest news", nil)
	switch {
	case len(d.Latest) > 0:
		b.articles(pages.Head(d.Latest, latestShown), d)
		if more := len(d.Latest) - latestShown; more > 0 {
			b.styled(Dim, fmt.Sprintf("  and %d older: search ticker:%s", more, ticker))
		}
	case !d.LatestOK:
		b.styled(Dim, "  loading…")
	default:
		b.styled(Dim, "  nothing yet")
	}
}
