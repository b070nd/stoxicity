package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/exchange"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/pages"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// wrapWidth is where the prose of an exchange page wraps.
const wrapWidth = 80

// exchangeStat is what is counted of one exchange.
type exchangeStat struct {
	companies, stocks, etfs int
	news                    int // relevant articles in the window that name one of its stocks
}

// exchangeStats counts the companies of each exchange and the relevant news of
// the window about them.
func exchangeStats(d Data) map[string]*exchangeStat {
	out := map[string]*exchangeStat{}
	for _, p := range d.Universe.All() {
		if p.Exchange == "" {
			continue
		}
		s := out[p.Exchange]
		if s == nil {
			s = &exchangeStat{}
			out[p.Exchange] = s
		}
		s.companies++
		switch p.Kind {
		case universe.KindStock:
			s.stocks++
		case universe.KindETF:
			s.etfs++
		}
	}
	for _, a := range pages.InWindow(d.Articles, d.Params) {
		if !a.Score.Relevant(d.Params.Threshold) {
			continue
		}
		seen := map[string]bool{}
		for _, t := range a.Tickers {
			if p, ok := d.Universe.Get(t); ok && p.Exchange != "" && !seen[p.Exchange] {
				seen[p.Exchange] = true
				out[p.Exchange].news++
			}
		}
	}
	return out
}

// openWord is the status of a market in a word for a table.
func openWord(st exchange.Status) string {
	switch {
	case st.Open && st.Core:
		return "open"
	case st.Open:
		return "extended"
	}
	return "closed"
}

// coreHours writes the regular session of an exchange: "09:30–16:00".
func coreHours(i exchange.Info) string {
	for _, s := range i.Sessions {
		if s.Core {
			return s.From + "–" + s.To
		}
	}
	return ""
}

// buildExchanges lists the exchanges stocks are listed on, with whether each is
// trading now, how many companies it has and how much news they make.
func buildExchanges(d Data) Screen {
	s := Screen{Title: "Exchanges · " + d.Params.Describe()}
	names := d.Universe.Exchanges()
	if len(names) == 0 {
		s.Notice = "No company has an exchange yet. Run `stoxicity universe update` to load the SEC and Nasdaq lists."
		return s
	}
	stats := exchangeStats(d)
	t := Table{
		Columns: []Column{
			{Width: 1},
			{Title: "EXCHANGE", Key: "exchange", Width: 14},
			{Title: "NOW", Key: "now", Width: 8},
			{Title: "HOURS", Width: 11},
			{Title: "COMPANIES", Key: "companies", Width: 9, Right: true, Desc: true},
			{Title: "STOCKS", Key: "stocks", Width: 6, Right: true, Desc: true},
			{Title: "ETFS", Key: "etfs", Width: 5, Right: true, Desc: true},
			{Title: "NEWS", Key: "news", Width: 5, Right: true, Desc: true},
			{Title: "OPERATOR"},
		},
		Default: Sort{Key: "companies", Desc: true},
	}
	for _, c := range names {
		st := stats[c.Name]
		now, hours, operator, open := "—", "", "", 0.0
		if info, ok := exchange.Lookup(c.Name); ok {
			status := info.StatusAt(d.Now)
			now, hours, operator = openWord(status), coreHours(info), info.Operator
			if status.Open {
				open = 1
				if status.Core {
					open = 2
				}
			}
		}
		mark := " "
		if strings.EqualFold(c.Name, d.Chosen) {
			mark = "●"
		}
		t.Items = append(t.Items, Item{
			Cells: []Cell{Str(mark), Str(c.Name), Num(now, open), Str(hours),
				Num(thousands(st.companies), float64(st.companies)), Num(thousands(st.stocks), float64(st.stocks)),
				Num(thousands(st.etfs), float64(st.etfs)), Num(fmt.Sprint(st.news), float64(st.news)), Str(operator)},
			Row: Row{Go: route(nav.Exchange, c.Name)},
		})
	}
	var b builder
	b.styled(Dim, fmt.Sprintf("%d exchanges · NEWS = relevant articles in %s that name one of its stocks · hours are the regular session in the market's own time · → opens one, u narrows every screen to it (● = chosen), x cycles", len(names), d.Params.Describe()))
	exchangeChips(&b, d)
	b.rows = append(b.rows, t.Rows(d.Sort)...)
	s.Rows, s.Table, s.Sort = b.rows, t.shape(), t.Effective(d.Sort)
	return s
}

// buildExchange is the page of one exchange: what it is, when it trades, what
// is counted of it, what it lists, the newest news about its stocks and its
// stocks.
func buildExchange(name string, d Data) Screen {
	s := Screen{Title: "Exchange " + name}
	var profiles []*universe.Profile
	all := d.Universe.All()
	for i := range all {
		if all[i].OnExchange(name) {
			profiles = append(profiles, &all[i])
		}
	}
	info, known := exchange.Lookup(name)
	if !known && len(profiles) == 0 {
		s.Notice = "No exchange called " + name + ": press e for the list."
		return s
	}
	if known {
		s.Title += " · " + info.Full
	}
	var b builder
	if known {
		aboutRows(&b, info)
		b.blank()
		hoursRows(&b, info, d)
	} else {
		b.heading("About", nil)
		b.styled(Dim, "  No reference data for this exchange: only what the company lists say is shown.")
	}
	b.blank()
	statRows(&b, profiles, name, d)
	b.blank()
	typeRows(&b, info, known, profiles)

	window := pages.InWindow(d.Articles, d.Params)
	if rel := pages.RelevantNewest(window, d.Params.Threshold); len(rel) > 0 {
		b.blank()
		b.heading("Latest news about its stocks ›", route(nav.Search, "after:"+d.Params.First.Format(time.DateOnly)+" ("+tickerTerms(rel, 12)+")"))
		b.articles(pages.Head(rel, 10), d)
		// The same news, the other way round: the stocks it is about, each a link to
		// its page, the most mentioned first.
		if stocks := pages.BuildStocksPage(d.Articles, d.Watchlist, d.Params); len(stocks.Rows) > 0 {
			b.blank()
			b.heading("Stocks in the news ›", route(nav.Companies, `exchange:"`+name+`" sort:news group:none`))
			b.rows = append(b.rows, stockTable(pages.Head(stocks.Rows, 8), d).Body(Sort{})...)
		}
	}

	if len(profiles) > 0 {
		b.blank()
		b.heading(fmt.Sprintf("Stocks · %s listed", thousands(len(profiles))), nil)
		q, _ := universe.ParseQuery("sort:news")
		cols := d.companyColumns()
		t := companyTable(q, cols)
		s.Table, s.Sort = t.shape(), t.Effective(d.Sort)
		if s.Sort.Key != "" {
			q = q.WithSort(s.Sort.Key, s.Sort.Desc != (s.Sort.Key == "news"))
		}
		watched := map[string]bool{}
		for _, w := range d.Watchlist {
			watched[universe.Ticker(w)] = true
		}
		for _, h := range d.Universe.Find(q, d.companyFacts(), 0) {
			if h.Profile.OnExchange(name) {
				t.Items = append(t.Items, companyItem(h, watched[h.Profile.Ticker], d, cols))
			}
		}
		b.rows = append(b.rows, t.Rows(s.Sort)...)
	}
	s.Rows = b.rows
	return s
}

// tickerTerms writes the tickers the articles name, most often first, as a
// search that matches any of them: ticker:A OR ticker:B.
func tickerTerms(arts []model.Article, n int) string {
	count := map[string]int{}
	for _, a := range arts {
		for _, t := range a.Tickers {
			count[t]++
		}
	}
	tickers := make([]string, 0, len(count))
	for t := range count {
		tickers = append(tickers, t)
	}
	slices.SortFunc(tickers, func(a, b string) int {
		if count[a] != count[b] {
			return count[b] - count[a]
		}
		return strings.Compare(a, b)
	})
	tickers = pages.Head(tickers, n)
	for i, t := range tickers {
		tickers[i] = "ticker:" + t
	}
	return strings.Join(tickers, " OR ")
}

func field(b *builder, label, value string) {
	if value != "" {
		b.text(fmt.Sprintf("  %-12s %s", label, value))
	}
}

func prose(b *builder, label, text string) {
	for i, line := range wrap(text, wrapWidth) {
		l := ""
		if i == 0 {
			l = label
		}
		b.text(fmt.Sprintf("  %-12s %s", l, line))
	}
}

func aboutRows(b *builder, i exchange.Info) {
	b.heading("About", nil)
	field(b, "name", i.Full)
	field(b, "run by", i.Operator)
	place := i.Country
	if i.City != "" {
		place = i.City + ", " + i.Country
	}
	field(b, "where", place)
	field(b, "market ID", i.MIC)
	field(b, "currency", i.Currency)
	prose(b, "what it is", i.About)
	if i.Website != "" {
		b.add(Row{Text: fmt.Sprintf("  %-12s %s   (b opens)", "website", i.Website), URL: i.Website})
	}
}

// hoursRows says whether the market is trading now, its sessions in its own time
// and the user's, today's calendar and the next holidays.
func hoursRows(b *builder, i exchange.Info, d Data) {
	b.heading("Opening hours", nil)
	now := d.Now
	st := i.StatusAt(now)
	style := Dim
	if st.Open {
		style = Good
	}
	b.styled(style, "  now          "+st.Describe(now))
	user := d.Params.Zone
	if user == nil {
		user = time.UTC
	}
	loc := i.Location()
	for _, w := range i.Normal(now) {
		line := fmt.Sprintf("  %-12s %s–%s %s", w.Name, w.From.Format("15:04"), w.To.Format("15:04"), w.From.Format("MST"))
		if u0, u1 := w.From.In(user), w.To.In(user); u0.Format("15:04") != w.From.Format("15:04") || u1.Format("15:04") != w.To.Format("15:04") {
			line += fmt.Sprintf("   = %s–%s %s", u0.Format("15:04"), u1.Format("15:04"), u0.Format("MST"))
		}
		if w.Core {
			line += "   regular session"
		}
		b.text(line)
	}
	b.text("  days         Monday to Friday, except the market holidays")
	cal := i.DayOf(now)
	switch {
	case cal.Closed != "":
		b.text(fmt.Sprintf("  today        closed: %s", cal.Closed))
	case cal.Early:
		b.text(fmt.Sprintf("  today        early close at %s (%s)", i.EarlyClose(), cal.Why))
	default:
		b.text("  today        a full day (" + now.In(loc).Format("Mon 2 Jan") + ")")
	}
	for k, h := range i.Holidays(now, 3) {
		label := ""
		if k == 0 {
			label = "closed on"
		}
		b.text(fmt.Sprintf("  %-12s %s  %s", label, h.Date.Format("Mon 2 Jan 2006"), h.Name))
	}
	prose(b, "note", i.Note)
}

// statRows counts the exchange: how many companies, of what kind, in which
// tiers and industries, and the news about them.
func statRows(b *builder, profiles []*universe.Profile, name string, d Data) {
	b.heading("Statistics", nil)
	total := d.Universe.Len()
	pct := 0.0
	if total > 0 {
		pct = float64(len(profiles)) * 100 / float64(total)
	}
	b.text(fmt.Sprintf("  %-12s %s of the %s companies the program knows (%.1f%%)", "listed", thousands(len(profiles)), thousands(total), pct))
	kinds := map[string]int{}
	tiers := map[string]int{}
	withSEC, deficient := 0, 0
	hits := make([]universe.Hit, 0, len(profiles))
	for _, p := range profiles {
		kinds[p.Kind]++
		if p.Market != "" {
			tiers[p.Market]++
		}
		if p.CIK != "" {
			withSEC++
		}
		if p.Status != "" {
			deficient++
		}
		hits = append(hits, universe.Hit{Profile: p})
	}
	if len(profiles) > 0 {
		b.text(fmt.Sprintf("  %-12s %s stocks · %s ETFs · %s other", "of which", thousands(kinds[universe.KindStock]),
			thousands(kinds[universe.KindETF]), thousands(len(profiles)-kinds[universe.KindStock]-kinds[universe.KindETF])))
		b.text(fmt.Sprintf("  %-12s %s have an SEC record (CIK, filings)", "filings", thousands(withSEC)))
	}
	if len(tiers) > 0 {
		keys := make([]string, 0, len(tiers))
		for k := range tiers {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, func(a, b string) int { return tiers[b] - tiers[a] })
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s %s", k, thousands(tiers[k]))
		}
		b.text(fmt.Sprintf("  %-12s %s", "market tiers", strings.Join(parts, " · ")))
	}
	if deficient > 0 {
		b.text(fmt.Sprintf("  %-12s %s with a deficiency or other special status", "status", thousands(deficient)))
	}
	if inds := universe.FacetsOf(hits, 5).Industry; len(inds) > 0 {
		parts := make([]string, len(inds))
		for i, c := range inds {
			parts[i] = fmt.Sprintf("%s %s", c.Name, thousands(c.Count))
		}
		b.text(fmt.Sprintf("  %-12s %s", "industries", strings.Join(parts, " · ")))
	}
	watched := 0
	for _, w := range d.Watchlist {
		if p, ok := d.Universe.Get(w); ok && p.OnExchange(name) {
			watched++
		}
	}
	if watched > 0 {
		b.text(fmt.Sprintf("  %-12s %d of the stocks you watch are listed here", "★ yours", watched))
	}

	window := pages.InWindow(d.Articles, d.Params)
	rel := pages.RelevantNewest(window, d.Params.Threshold)
	stocks := map[string]bool{}
	mat, tone := 0.0, 0.0
	for _, a := range rel {
		for _, t := range a.Tickers {
			stocks[t] = true
		}
		mat += float64(a.Score.Materiality)
		tone += a.Score.Tone
	}
	if len(rel) == 0 {
		b.styled(Dim, fmt.Sprintf("  %-12s no relevant article names a stock of this exchange in %s", "news", d.Params.Describe()))
		return
	}
	n := float64(len(rel))
	b.text(fmt.Sprintf("  %-12s %d articles, %d relevant, naming %d of its stocks · %s", "news", len(window), len(rel), len(stocks), d.Params.Describe()))
	b.text(fmt.Sprintf("  %-12s materiality %.1f · tone %s (average over the relevant articles)", "", mat/n, tonePct(tone/n)))
}

// typeRows says what kind of securities the exchange lists, with the counts.
func typeRows(b *builder, i exchange.Info, known bool, profiles []*universe.Profile) {
	b.heading("Types of stocks", nil)
	if known {
		prose(b, "lists", i.Lists)
	}
	byKind := map[string]int{}
	for _, p := range profiles {
		byKind[p.Kind]++
	}
	if len(profiles) > 0 {
		b.text(fmt.Sprintf("  %-12s %s stocks · %s ETFs (the table at the bottom lists them)", "here", thousands(byKind[universe.KindStock]), thousands(byKind[universe.KindETF])))
	}
}
