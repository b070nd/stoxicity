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
	title := "Stocks"
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
	cols := d.companyColumns()
	t := companyTable(query, cols)
	s.Table, s.Sort = t.shape(), t.Effective(d.Sort)
	if s.Sort.Key != "" {
		query = query.WithSort(s.Sort.Key, s.Sort.Desc != (s.Sort.Key == "news"))
	}
	hits := d.Universe.Find(query, d.companyFacts(), 0)
	if d.Exchange != "" {
		hits = slices.DeleteFunc(hits, func(h universe.Hit) bool { return !h.Profile.OnExchange(d.Exchange) })
	}
	if len(hits) == 0 {
		s.Notice = fmt.Sprintf("No company matches (of %d). Press / to change the query.", d.Universe.Len())
		return s
	}
	var b builder
	note := fmt.Sprintf("%s of %s securities", thousands(len(hits)), thousands(d.Universe.Len()))
	note += " · " + orderOf(query)
	b.styled(Dim, note+" · ★ watchlist · / search · f facets")
	if q != "" && q != stocksQuery {
		b.styled(Dim, "Query: "+q)
	}
	if !cols.Priced {
		b.styled(Dim, "no prices yet · configure the provider and run stoxicity universe quotes")
	} else {
		b.styled(Dim, "Daily candles · green up / yellow flat / red down · dim = stale · latest session "+d.latestQuoteDay())
	}
	if cols.Funded || cols.Insiders {
		for _, line := range legendLines(cols) {
			b.styled(Dim, line)
		}
	}
	exchangeChips(&b, d)
	if d.ShowFacets {
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
		t.Items = append(t.Items, companyItem(h, watched[h.Profile.Ticker], d, cols))
		keys[i] = h.Group(group)
	}
	if d.Width > 0 {
		t = fitCompanyTable(t, d.Width, s.Sort)
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
	t = t.sized()
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
func companyTable(q universe.Query, cols companyCols) Table {
	columns := []Column{
		{Width: 1},
		{Title: "TICKER", Key: "ticker", Width: 8},
		{Title: "NAME", Key: "name", Width: 22, MaxWidth: 28},
	}
	if cols.Priced {
		columns = append(columns,
			Column{Title: "DAY", Width: candleWidth},
			Column{Title: "CLOSE", Key: "close", Width: 8, Right: true, Desc: true},
			Column{Title: "CHG", Key: "change", Width: 7, Right: true, Desc: true})
	}
	if cols.Funded {
		columns = append(columns,
			Column{Title: "MCAP", Key: "marketcap", Width: 7, Right: true, Desc: true},
			Column{Title: "REVENUE", Key: "revenue", Width: 7, Right: true, Desc: true},
			Column{Title: "GROWTH", Key: "growth", Width: 7, Right: true, Desc: true},
			Column{Title: "MARGIN", Key: "margin", Width: 7, Right: true, Desc: true})
	}
	if cols.Insiders {
		columns = append(columns,
			Column{Title: "BUYS", Key: "buys", Width: 6, Right: true, Desc: true},
			Column{Title: "SELLS", Key: "sells", Width: 6, Right: true, Desc: true})
	}
	columns = append(columns,
		Column{Title: "NEWS", Key: "news", Width: 5, Right: true, Desc: true},
		Column{Title: "TYPE", Key: "type", Width: 9, MaxWidth: 10},
		Column{Title: "LISTED", Key: "exchange", Width: 10, MaxWidth: 14},
		Column{Title: "DOMAIN", Key: "domain", Width: 14, MaxWidth: 18},
		Column{Title: "TAGS", Width: 8, MaxWidth: 16})
	t := Table{Columns: columns, Presorted: true}
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

// companyFacts are the facts a company query can ask about: the news of the
// window, the watchlist and the newest prices.
func (d Data) companyFacts() universe.Facts {
	f := universe.FactsFrom(d.Articles, d.Params.Threshold, d.Watchlist)
	f.Num = d.Numbers
	f.Close, f.Change = make(map[string]float64, len(d.Quotes)), make(map[string]float64, len(d.Quotes))
	for tk, q := range d.Quotes {
		f.Close[tk] = q.Close
		if ch, ok := q.Change(); ok {
			f.Change[tk] = ch
		}
	}
	return f
}

// latestQuoteDay is the newest trading day any candle is for.
func (d Data) latestQuoteDay() string {
	newest := ""
	for _, q := range d.Quotes {
		newest = max(newest, q.Day)
	}
	return newest
}

// companyCols says which optional column groups the table has: those whose
// data exists.
type companyCols struct{ Priced, Funded, Insiders bool }

func (d Data) companyColumns() companyCols {
	return companyCols{Priced: len(d.Quotes) > 0, Funded: len(d.Numbers["revenue"]) > 0, Insiders: len(d.Numbers["buys"]) > 0}
}

func companyItem(h universe.Hit, watched bool, d Data, cols companyCols) Item {
	quotes, now := d.Quotes, d.Now
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
	typ, dom := p.Type(), p.Domain()
	cells := []Cell{Str(star).Styled(Warn), Str(p.Ticker).Styled(Strong), Str(p.Name)}
	if cols.Priced {
		var bar, close, move Cell
		if q, ok := quotes[p.Ticker]; ok {
			text, style := candleBar(q, candleWidth, now)
			bar = Str(text).Styled(style)
			close = Num(priceText(q.Close), q.Close).Styled(style)
			mt, ms := changeText(q, now)
			move = Num(mt, 0).Styled(ms)
		} else {
			bar, close, move = Str("—").Styled(Dim), Str("—").Styled(Dim), Str("—").Styled(Dim)
		}
		cells = append(cells, bar, close, move)
	}
	if cols.Funded {
		num := func(key string) (float64, bool) { v, ok := d.Numbers[key][p.Ticker]; return v, ok }
		missing := Str("—").Styled(Dim)
		mcap, rev, growth, margin := missing, missing, missing, missing
		mcap = capCell(d, p.Ticker)
		if v, ok := num("revenue"); ok {
			rev = Num(moneyText(v), v)
		}
		if v, ok := num("growth"); ok {
			growth = Num(signedPercent(v), v).Styled(gainStyle(v))
		}
		if v, ok := num("margin"); ok {
			margin = Num(percentText(v), v).Styled(gainStyle(v))
		}
		cells = append(cells, mcap, rev, growth, margin)
	}
	if cols.Insiders {
		// Buying and selling are facts, not verdicts: two colours tell the columns apart.
		cells = append(cells, insiderCell(d, "buys", p.Ticker, Info), insiderCell(d, "sells", p.Ticker, Accent))
	}
	cells = append(cells, Num(news, float64(h.News)).Styled(newsStyle(h.News)), Str(typ).Styled(typeStyle(typ)), Str(p.Exchange).Styled(exchangeStyle(p.Exchange)),
		Str(dom).Styled(domainStyle(dom)), Str(strings.Join(tags, " ")).Styled(Accent))
	return Item{Cells: cells, Row: Row{Go: route(nav.Stock, p.Ticker)}}
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
			b.add(Row{Text: fmt.Sprintf("  %-10s %-8s %s", "", sib, d.name(sib)), Go: route(nav.Stock, sib)})
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
	if typ := p.Type(); typ != "" && typ != p.Kind {
		field("type", typ)
	}
	if q, ok := d.Quotes[p.Ticker]; ok {
		text, style := changeText(q, d.Now)
		line := fmt.Sprintf("  %-10s %s on %s", "last close", priceText(q.Close), q.Day)
		if text != "" {
			line += " (" + text + ")"
		}
		b.add(Row{Text: line, Style: map[bool]Style{true: Dim, false: style}[q.stale(d.Now)]})
		if bar, bs := candleBar(q, candleWidth, d.Now); bar != "" {
			b.add(Row{Text: fmt.Sprintf("  %-10s %s  low %s · open %s · close %s · high %s", "day", bar, priceText(q.Low), priceText(q.Open), priceText(q.Close), priceText(q.High)), Style: bs})
		}
	}
	_, hasRev := d.Numbers["revenue"][p.Ticker]
	if hasRev || d.Numbers["marketcap"][p.Ticker] != 0 || d.Why["marketcap"][p.Ticker] != "" {
		figure := func(name, key string, text func(float64) string) {
			if v, ok := d.Numbers[key][p.Ticker]; ok {
				b.add(Row{Text: fmt.Sprintf("  %-12s %s", name, text(v)), Style: Plain})
			} else if why := d.Why[key][p.Ticker]; why != "" {
				// A value that cannot be calculated says why, in words, never as a blank.
				state := ""
				if st := d.Standing[p.Ticker]; st.State != "" && st.State != "valued" && (key == "marketcap" || key == "classcap" || key == "issuercap") {
					state = " (" + st.State + ")"
				}
				b.add(Row{Text: fmt.Sprintf("  %-12s unavailable%s: %s", name, state, why), Style: capStyle(state)})
			}
		}
		figure("market cap", "marketcap", moneyText)
		figure("class cap", "classcap", moneyText)
		figure("issuer cap", "issuercap", moneyText)
		if st, ok := d.Standing[p.Ticker]; ok && st.State != "" {
			basis := "  cap basis    " + st.State
			if st.CountDay != "" {
				basis += " · share count of " + st.CountDay
			}
			if st.PriceDay != "" {
				basis += " · last close of " + st.PriceDay
			}
			if st.Currency != "" {
				basis += " · " + st.Currency
			}
			if st.Scope == "issuer" {
				basis += " · market cap is the whole issuer's, shared by its classes; class cap is this class's own"
			}
			b.styled(Dim, basis)
		}
		figure("revenue", "revenue", moneyText)
		figure("growth", "growth", signedPercent)
		figure("net income", "income", moneyText)
		figure("margin", "margin", percentText)
		figure("assets", "assets", moneyText)
		figure("equity", "equity", moneyText)
		figure("roe", "roe", percentText)
		figure("shares", "shares", moneyText)
		b.styled(Dim, "  fiscal year figures from the SEC's XBRL data; market cap is the issuer's: the value of every class of its stock, from the class counts on the cover page and each class's own last close")
	}
	switch {
	case p.SIC != "" && p.Industry != "":
		field("industry", p.Industry+" (SIC "+p.SIC+")")
	case p.Industry != "":
		field("industry", p.Industry)
	}
	field("sector", p.Sector)
	field("based in", p.HQ)
	field("phone", p.Phone)
	field("state", p.State)
	field("fiscal year", p.FiscalYear)
	field("category", p.Category)
	field("entity", p.EntityType)
	field("ISIN", p.ISIN)
	field("CIK", p.CIK)
	field("EIN", p.EIN)
	field("LEI", p.LEI)
	if len(p.Aliases) > 0 {
		field("aka", strings.Join(pages.Head(p.Aliases, 4), " · "))
	}
	if p.Industry == "" && p.CIK != "" {
		b.styled(Dim, "  the SEC record is not loaded: run `stoxicity universe enrich "+p.Ticker+"`")
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
		b.styled(Dim, "  lists   none (l adds the stock to a list)")
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
			state = fmt.Sprintf("failing (%d×): %s", f.Failures, f.LastError)
		case !f.LastOK.IsZero():
			state = "read " + shortAgo(d.Now.Sub(f.LastOK))
			if f.LastNew > 0 {
				state += fmt.Sprintf(", %d new", f.LastNew)
			}
		}
		text := fmt.Sprintf("  %-10s %-12s %s", label, kind, state)
		if f.Kind != store.FeedSEC && f.Title != "" {
			text += " · " + f.Title
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

// capStyles colours the words that stand where a capitalisation figure is
// missing. None is red or green: those colours mean the sign of a change in this
// table, and a missing figure is not a negative one.
var capStyles = map[string]Style{
	"stale": Warn, "incomplete": Warn, "unresolved": Hot, "unsupported": Dim, "failed": Hot, "pending": Dim,
}

// capCell is the MCAP cell of a stock: the issuer's capitalisation when there is
// one (prefixed "issuer" on a security that is one of several classes, so that a
// class's row is not read as the class's own value), otherwise the word for why
// not. The number and the word agree with sorting and filtering, which see no
// number for a stock without a figure.
func capCell(d Data, ticker string) Cell {
	st, known := d.Standing[ticker]
	if v, ok := d.Numbers["marketcap"][ticker]; ok {
		text := moneyText(v)
		if known && st.Scope == "issuer" {
			text = "issuer " + text
		}
		return Num(text, v).Styled(Info)
	}
	if known && st.State != "" && st.State != "valued" {
		return Str(st.State).Styled(capStyles[st.State])
	}
	if d.Why["marketcap"][ticker] != "" {
		return Str("incomplete").Styled(Warn) // the stock page says why
	}
	return Str("—").Styled(Dim)
}

// legendLines say what the colours and words of the figure columns mean, so that
// no colour has to be guessed.
func legendLines(cols companyCols) []string {
	var out []string
	if cols.Funded {
		out = append(out,
			"MCAP is the issuer's whole capitalisation in US dollars (every class of stock, from the cover page counts and each class's own last close); \"issuer\" marks a class that shares it · cyan = a figure",
			"A missing MCAP says why: stale (count or price too old) · incomplete (a class has no value yet) · unresolved (a class cannot be told apart) · unsupported (cannot be valued by this method) · pending (not read yet) · \"—\" no figure expected",
			"GROWTH and MARGIN: green = positive, red = negative, yellow = zero. The colour is the sign, never a judgement of the stock or of a trade")
	}
	if cols.Insiders {
		out = append(out, "BUYS and SELLS are insiders' open-market purchases and sales over the last year; cyan and magenta only tell the two columns apart")
	}
	return out
}

// capStyle is the style of a line saying a capitalisation is unavailable.
func capStyle(state string) Style {
	if state == "" {
		return Warn
	}
	if s, ok := capStyles[strings.Trim(state, " ()")]; ok {
		return s
	}
	return Warn
}
