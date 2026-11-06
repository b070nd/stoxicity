package tui

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/pages"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// facetRows is how many entries the long facet lines (industry, lists, tags)
// show; exchange and kind, which have few values, show up to shortFacets.
const (
	facetRows   = 6
	shortFacets = 12
)

// queryHint is what the catalogue says about its query language and keys.
const queryHint = "/ narrows as you type: words match ticker, name and industry · exchange:nyse · industry:semi · list:picks · tag:moat · kind:etf · news:>0 · watched:yes · -word excludes · sort:name|news|industry|exchange|kind (-news reverses) · g jumps to a letter · home/end/pgup/pgdn move"

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
	f := universe.FacetsOf(hits, shortFacets)
	long := universe.FacetsOf(hits, facetRows)
	for _, line := range []struct {
		name string
		c    []universe.Count
	}{{"exchange", f.Exchange}, {"kind", f.Kind}, {"industry", long.Industry}, {"lists", long.List}, {"tags", long.Tag}} {
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
	for _, h := range hits {
		t.Items = append(t.Items, companyItem(h, watched[h.Profile.Ticker]))
	}
	b.rows = append(b.rows, t.Rows(s.Sort)...)
	s.Rows = b.rows
	return s
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
	field("listed", listing)
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
		b.add(Row{Text: "  website    " + site + "   (o opens)", URL: site})
	}
	if p.CIK != "" {
		b.add(Row{Text: "  SEC filings on EDGAR   (o opens)", URL: edgarURL(p.CIK)})
	}

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
