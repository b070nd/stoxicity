package tui

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/pages"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// facetRows is how many entries the long facet lines (industry, lists, tags)
// show; exchange and kind, which have few values, show up to shortFacets.
const (
	facetRows   = 6
	shortFacets = 12
)

// queryHint is what the catalogue says about its query language and keys.
const queryHint = "/ narrows as you type: words match ticker, name and industry · exchange:nyse · industry:semi · list:picks · tag:moat · kind:etf · news:>0 · watched:yes · -word excludes · sort:name|news|industry|exchange|kind (-news reverses) · j jumps to a letter · home/end/pgup/pgdn move"

// edgarURL is the SEC's page of a company, by CIK.
func edgarURL(cik string) string {
	return "https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&CIK=" + url.QueryEscape(cik) + "&type=&dateb=&owner=include&count=40"
}

// webAddress makes a company's website a web address: the SEC often gives it
// without a scheme.
func webAddress(site string) string {
	site = strings.TrimSpace(site)
	if site == "" {
		return ""
	}
	if !strings.Contains(site, "://") {
		site = "https://" + site
	}
	return site
}

// buildCompanies is the catalogue: every company the program knows, all of
// them, in the order of the tickers unless the query says otherwise. The
// screen holds one row per match, however many; only the rows in view are
// drawn.
func buildCompanies(q string, d Data) Screen {
	title := "Companies"
	if q != "" {
		title += ": " + q
	}
	s := Screen{Title: title}
	if d.Universe.Len() == 0 {
		s.Notice = "No companies yet. Run `stoxicity universe update` to load the SEC ticker list."
		return s
	}
	query, err := universe.ParseQuery(q)
	if err != nil {
		s.Notice = err.Error()
		return s
	}
	t := companyTable(query)
	s.Table, s.Sort = t.shape(), t.Effective(d.Sort)
	if s.Sort.Key != "" {
		query = query.WithSort(s.Sort.Key, s.Sort.Desc != (s.Sort.Key == "news"))
	}
	hits := d.Universe.Find(query, universe.FactsFrom(d.Articles, d.Params.Threshold, d.Watchlist), 0)
	if d.Exchange != "" {
		hits = slices.DeleteFunc(hits, func(h universe.Hit) bool { return !h.Profile.OnExchange(d.Exchange) })
	}
	if len(hits) == 0 {
		s.Notice = fmt.Sprintf("No company matches (of %d). Press / to change the query.", d.Universe.Len())
		return s
	}
	var b builder
	note := fmt.Sprintf("%s of %s companies", thousands(len(hits)), thousands(d.Universe.Len()))
	note += " · " + orderOf(query)
	b.styled(Dim, note+" · news counts cover "+d.Params.Describe()+" · ★ = on your watchlist")
	if q == "" {
		b.styled(Dim, queryHint)
	}
	exchangeChips(&b, d)
	f := universe.FacetsOf(hits, shortFacets)
	long := universe.FacetsOf(hits, facetRows)
	for _, line := range []struct {
		name string
		c    []universe.Count
	}{{"kind", f.Kind}, {"industry", long.Industry}, {"lists", long.List}, {"tags", long.Tag}} {
		if len(line.c) == 0 {
			continue
		}
		parts := make([]string, len(line.c))
		for i, c := range line.c {
			parts[i] = fmt.Sprintf("%s %s", c.Name, thousands(c.Count))
		}
		b.styled(Dim, line.name+": "+strings.Join(parts, " · "))
	}
	b.blank()
	watched := map[string]bool{}
	for _, w := range d.Watchlist {
		watched[universe.Ticker(w)] = true
	}
	group := d.Group
	if group == "" {
		group = query.GroupKey()
	}
	if group == "" {
		group = "exchange"
		if d.Exchange != "" {
			group = "kind" // one exchange would make a single group
		}
	}
	s.Group = group
	if group == "none" {
		group = ""
	}
	keys := make([]string, len(hits))
	for i, h := range hits {
		t.Items = append(t.Items, companyItem(h, watched[h.Profile.Ticker]))
		keys[i] = h.Group(group)
	}
	drill := func(value string) *nav.Route {
		if value == "" || strings.ContainsAny(value, `"*`) {
			return nil
		}
		return route(nav.Companies, strings.TrimSpace(q+" "+group+`:"`+value+`" group:none`))
	}
	b.rows = append(b.rows, groupRows(t, keys, group, s.Sort, drill)...)
	s.Rows = b.rows
	return s
}

// groupRows is the table's rows arranged under a heading for each value of the
// group (keys holds one per item): the biggest group first, the items with no
// value last. A heading opens the list narrowed to its group (enter). Inside a group the items keep the order they came in. Without a
// group it is the table as it is.
func groupRows(t Table, keys []string, group string, sort Sort, drill func(value string) *nav.Route) []Row {
	if group == "" {
		return t.Rows(sort)
	}
	items := map[string][]Item{}
	for i, it := range t.Items {
		items[keys[i]] = append(items[keys[i]], it)
	}
	order := make([]string, 0, len(items))
	for k := range items {
		order = append(order, k)
	}
	slices.SortFunc(order, func(a, b string) int {
		switch {
		case (a == "") != (b == ""):
			if a == "" {
				return 1
			}
			return -1
		case len(items[a]) != len(items[b]):
			return len(items[b]) - len(items[a])
		}
		return strings.Compare(a, b)
	})
	head := t
	head.Items = nil
	rows := head.Rows(sort) // the header alone
	for _, k := range order {
		part := t
		part.Items = items[k]
		rows = append(rows, Row{Text: fmt.Sprintf("%s · %s", groupName(group, k), thousands(len(part.Items))), Style: Heading, Go: drill(k)})
		rows = append(rows, part.Body(sort)...)
	}
	return rows
}

// groupName is what a group is called in its heading.
func groupName(group, value string) string {
	if value != "" {
		return strings.ToUpper(group[:1]) + group[1:] + " " + value
	}
	switch group {
	case "exchange":
		return "No exchange (SEC list only)"
	case "industry":
		return "No industry recorded yet"
	}
	return "No " + group
}

// companyTable is the columns of the catalogue. The list is ordered by the
// universe (which sorts twelve thousand companies, and knows the order of a
// ranked search), so the table only labels the header: its default is what the
// query asks for. News is listed busiest first when ascending, which is how
// the query language says it, so the header shows the opposite arrow.
func companyTable(q universe.Query) Table {
	t := Table{
		Columns: []Column{
			{Width: 1},
			{Title: "TICKER", Key: "ticker", Width: 7},
			{Title: "NAME", Key: "name", Width: 30},
			{Title: "LISTED", Key: "exchange", Width: 7},
			{Title: "KIND", Key: "kind", Width: 5},
			{Title: "INDUSTRY", Key: "industry", Width: 24},
			{Title: "NEWS", Key: "news", Width: 5, Right: true, Desc: true},
			{Title: "TAGS"},
		},
		Presorted: true,
	}
	key, desc := q.SortKey()
	switch {
	case key != "":
		t.Default = Sort{Key: key, Desc: desc != (key == "news")}
	case !q.HasText():
		t.Default = Sort{Key: "ticker"}
	}
	return t
}

// orderOf says in words how the query orders the list.
func orderOf(q universe.Query) string {
	if key, desc := q.SortKey(); key != "" {
		dir := ""
		if desc {
			dir = ", reversed"
		}
		return "by " + key + dir
	}
	if q.HasText() {
		return "best match first"
	}
	return "by ticker"
}

// thousands writes n with a comma between groups of three digits.
func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func companyItem(h universe.Hit, watched bool) Item {
	p := h.Profile
	star := " "
	if watched {
		star = "★"
	}
	news := ""
	if h.News > 0 {
		news = fmt.Sprint(h.News)
	}
	var tags []string
	for _, t := range p.Tags {
		tags = append(tags, "#"+t)
	}
	return Item{
		Cells: []Cell{Str(star), Str(p.Ticker), Str(p.Name), Str(p.Exchange), Str(p.Kind), Str(p.Industry),
			Num(news, float64(h.News)), Str(strings.Join(tags, " "))},
		Row: Row{Go: route(nav.Stock, p.Ticker)},
	}
}

// profileRows adds the identity block of a stock: what is known about the
// company, where it is listed, what you filed it under, and who follows it.
func profileRows(b *builder, ticker string, d Data) (known bool) {
	p, ok := d.Universe.Get(ticker)
	if !ok {
		return false
	}
	field := func(name, value string) {
		if value != "" {
			b.text(fmt.Sprintf("  %-10s %s", name, value))
		}
	}
	b.heading("Profile", nil)
	field("name", p.Name)
	if len(p.Siblings) > 0 {
		b.text(fmt.Sprintf("  %-10s %s", "also", "the same company trades as:"))
		for _, sib := range p.Siblings {
			b.add(Row{Text: fmt.Sprintf("  %-10s %-8s %s", "", sib, clip(d.name(sib), 40)), Go: route(nav.Stock, sib)})
		}
	}
	listing := p.Exchange
	if p.Venue != "" && p.Venue != p.Exchange {
		listing = strings.TrimSpace(listing + " (" + p.Venue + ")")
	}
	if p.Exchange != "" { // the exchange it is listed on is a page of its own: → opens it
		b.add(Row{Text: fmt.Sprintf("  %-10s %s ›", "listed", listing), Go: route(nav.Exchange, p.Exchange)})
	} else {
		field("listed", listing)
	}
	kind := p.Kind
	if p.Market != "" {
		kind = strings.TrimSpace(kind + " · market tier " + p.Market)
	}
	if p.Status != "" {
		kind += " · status " + p.Status
	}
	field("kind", kind)
	switch {
	case p.SIC != "" && p.Industry != "":
		field("industry", p.Industry+" (SIC "+p.SIC+")")
	case p.Industry != "":
		field("industry", p.Industry)
	}
	field("sector", p.Sector)
	field("state", p.State)
	field("fiscal year", p.FiscalYear)
	field("category", p.Category)
	field("entity", p.EntityType)
	field("ISIN", p.ISIN)
	field("CIK", p.CIK)
	if len(p.Aliases) > 0 {
		field("aka", strings.Join(pages.Head(p.Aliases, 4), " · "))
	}
	if p.Industry == "" && p.CIK != "" {
		b.styled(Dim, "  industry and fiscal year are not loaded: run `stoxicity universe enrich "+p.Ticker+"`")
	}
	if p.Source == universe.SourceListing {
		b.styled(Dim, "  listed by the exchanges only: the SEC list has no record of it, so no CIK, filings or industry")
	}
	if p.Source == universe.SourceUser {
		b.styled(Dim, "  only your lists, tags or watches name this ticker; the SEC list does not")
	}
	if site := webAddress(p.Website); site != "" {
		b.add(Row{Text: "  website    " + site + "   (b opens)", URL: site})
	}
	if p.CIK != "" {
		b.add(Row{Text: "  SEC filings on EDGAR   (b opens)", URL: edgarURL(p.CIK)})
	}

	feedRows(b, p, d)

	b.blank()
	b.heading("Yours", nil)
	if d.watched(p.Ticker) {
		b.text("  ★ on your watchlist")
	}
	if len(p.Lists) > 0 {
		b.text("  lists   " + strings.Join(p.Lists, ", "))
	} else {
		b.styled(Dim, "  lists   none (L adds the stock to a list)")
	}
	if len(p.Tags) > 0 {
		tags := make([]string, len(p.Tags))
		for i, t := range p.Tags {
			tags[i] = "#" + t
		}
		b.text("  tags    " + strings.Join(tags, " "))
	} else {
		b.styled(Dim, "  tags    none (# adds a tag)")
	}
	if d.Following != nil {
		if w := d.Following(p.Ticker); len(w) > 0 {
			b.text("  watches " + strings.Join(w, ", "))
		}
	}
	return true
}

func (d Data) watched(ticker string) bool {
	for _, w := range d.Watchlist {
		if universe.Ticker(w) == ticker {
			return true
		}
	}
	return false
}

// feedRows lists the feeds of a stock: the SEC filing record and the news feed
// of the company's website, with when each was last read. The tickers of one
// company share its records, so the siblings are asked too.
func feedRows(b *builder, p universe.Profile, d Data) {
	if d.Feeds == nil {
		return
	}
	var list []FeedInfo
	seen := map[string]bool{}
	for _, t := range append([]string{p.Ticker}, p.Siblings...) {
		for _, f := range d.Feeds(t) {
			if !seen[f.URL] {
				seen[f.URL] = true
				list = append(list, f)
			}
		}
	}
	if len(list) == 0 {
		if p.CIK != "" || p.Website != "" {
			b.styled(Dim, "  feeds      none recorded yet: the background refresh of `stoxicity run` (or `stoxicity universe feeds`) finds them")
		}
		return
	}
	for i, f := range list {
		label := "feeds"
		if i > 0 {
			label = ""
		}
		kind := "company site"
		url := f.URL
		if f.Kind == store.FeedSEC {
			kind, url = "SEC filings", edgarURL(p.CIK)
		}
		state := "not read yet"
		switch {
		case f.Failures > 0:
			state = fmt.Sprintf("failing (%d×): %s", f.Failures, clip(f.LastError, 50))
		case !f.LastOK.IsZero():
			state = "read " + shortAgo(d.Now.Sub(f.LastOK))
			if f.LastNew > 0 {
				state += fmt.Sprintf(", %d new", f.LastNew)
			}
		}
		text := fmt.Sprintf("  %-10s %-12s %s", label, kind, state)
		if f.Kind != store.FeedSEC && f.Title != "" {
			text += " · " + clip(f.Title, 40)
		}
		b.add(Row{Text: text + "   (b opens)", URL: url})
	}
}

// shortAgo says how long ago in the biggest unit: "5m ago", "3h ago", "2d ago".
func shortAgo(d time.Duration) string {
	switch d = max(d, 0); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}
