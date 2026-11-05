package tui

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/pages"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// companyRows is how many companies the Companies screen lists; the counts
// above them cover every match.
const companyRows = 300

// facetRows is how many entries each facet line shows.
const facetRows = 6

// queryHint is what the Companies screen says about its query language.
const queryHint = "type / to narrow: words match ticker, name and industry · exchange:nyse · industry:semi · list:picks · tag:moat · kind:etf · news:>0 · watched:yes · -word excludes"

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
	hits := d.Universe.Find(query, universe.FactsFrom(d.Articles, d.Params.Threshold, d.Watchlist), 0)
	if len(hits) == 0 {
		s.Notice = fmt.Sprintf("No company matches (of %d). Press / to change the query.", d.Universe.Len())
		return s
	}
	var b builder
	note := fmt.Sprintf("%d of %d companies", len(hits), d.Universe.Len())
	if len(hits) > companyRows {
		note += fmt.Sprintf(" (first %d shown)", companyRows)
	}
	b.styled(Dim, note+" · news counts cover "+d.Params.Describe()+" · ★ = on your watchlist")
	if q == "" {
		b.styled(Dim, queryHint)
	}
	f := universe.FacetsOf(hits, facetRows)
	for _, line := range []struct {
		name string
		c    []universe.Count
	}{{"exchange", f.Exchange}, {"kind", f.Kind}, {"industry", f.Industry}, {"lists", f.List}, {"tags", f.Tag}} {
		if len(line.c) == 0 {
			continue
		}
		parts := make([]string, len(line.c))
		for i, c := range line.c {
			parts[i] = fmt.Sprintf("%s %d", c.Name, c.Count)
		}
		b.styled(Dim, line.name+": "+strings.Join(parts, " · "))
	}
	b.blank()
	watched := map[string]bool{}
	for _, w := range d.Watchlist {
		watched[universe.Ticker(w)] = true
	}
	for _, h := range hits[:min(len(hits), companyRows)] {
		b.add(companyLine(h, watched[h.Profile.Ticker]))
	}
	s.Rows = b.rows
	return s
}

func companyLine(h universe.Hit, watched bool) Row {
	p := h.Profile
	star := " "
	if watched {
		star = "★"
	}
	news := ""
	if h.News > 0 {
		news = fmt.Sprintf("%d news", h.News)
	}
	var tags []string
	for _, t := range p.Tags {
		tags = append(tags, "#"+t)
	}
	text := fmt.Sprintf("%s %-7s %-30s %-7s %-5s %-24s %-7s %s", star, p.Ticker, clip(p.Name, 30),
		clip(p.Exchange, 7), p.Kind, clip(p.Industry, 24), news, strings.Join(tags, " "))
	return Row{Text: strings.TrimRight(text, " "), Go: route(nav.Stock, p.Ticker)}
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
