// Package universe is the reference list of stocks: every company the program
// knows, with what is known about it, and ways to find, filter and group them.
//
// It joins four sources, each optional:
//
//   - the SEC ticker list (package company): ticker, name, CIK, exchange;
//   - the Nasdaq symbol directory: ETF or not, market tier, financial status;
//   - the SEC record of each company you follow: industry (SIC), state, fiscal
//     year end, former names;
//   - your own layer (stocks.yaml): lists, tags and companies the SEC lacks.
//
// A Universe is a snapshot, immutable and safe for concurrent use. It does no
// I/O; the caller loads the sources and builds a new one when they change.
package universe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/company"
)

// Kinds of security.
const (
	KindStock = "stock"
	KindETF   = "etf"
	KindTest  = "test" // a test symbol the exchange publishes
)

// Where a profile came from.
const (
	SourceSEC   = "sec"   // the SEC ticker list
	SourceExtra = "extra" // your extra entries
	// SourceListing is a security only the Nasdaq Trader directory lists: most
	// ETFs, preferred shares, warrants, test symbols. It has no CIK and so no
	// SEC record.
	SourceListing = "listing"
	SourceUser    = "user" // only named in a list, tag or watch; nothing else is known
)

// Profile is everything known about one ticker.
type Profile struct {
	Ticker string
	Name   string // tidy, for display
	CIK    string // ten digits; empty for non-SEC entries
	// Exchange is the SEC's word ("Nasdaq", "NYSE", "OTC", "CBOE", "TXSE").
	Exchange string
	// Venue is the Nasdaq directory's more exact exchange ("NYSE Arca"); empty
	// when the directory does not list the symbol.
	Venue string
	// Aliases are other names it goes by: the SEC's own spelling, former names,
	// and your extra aliases.
	Aliases []string
	// Kind is KindStock, KindETF or KindTest; empty when the Nasdaq directory
	// is missing or does not list the symbol.
	Kind string
	// Market is the Nasdaq tier; Status the financial status, "" when normal.
	Market, Status string
	// Security is the name the Nasdaq directory gives the listing, as it
	// writes it ("X - Warrant", "X - Common Stock"): the surest word for what
	// kind of security the ticker is. Empty when the directory does not list it.
	Security string

	SIC, Industry, State, FiscalYear, Category, Website, EntityType string
	Sector                                                          string // from the sector rules of the configuration
	ISIN                                                            string
	// HQ is the business address ("Miramar, FL"); Phone, EIN (employer ID) and
	// LEI (legal entity ID) are from the SEC record, Investor the
	// investor-relations website where the SEC has one.
	HQ, Phone, EIN, LEI, Investor string

	Tags     []string
	Lists    []string // IDs of the lists the stock is in
	Siblings []string // other tickers of the same company
	Source   string

	// typ is Type(), worked out once when the universe is built: a list of
	// twelve thousand asks for it on every keystroke.
	typ   string
	typed bool
}

// Inputs are the sources of a Universe.
type Inputs struct {
	Master   company.Master
	Listing  company.Listing
	Industry company.IndustryCache
	Layer    Layer
	// Websites are official websites by ticker (Wikidata's), for the companies
	// whose SEC record names none.
	Websites map[string]string
	// Sectors maps a ticker to a sector name (the configuration's sector
	// rules).
	Sectors map[string]string
	// Known are tickers that should exist even if no source lists them: the
	// watchlist and the tickers of watches.
	Known []string
}

// List is a list with its members.
type List struct {
	ID, Name string
	Tickers  []string
}

// Universe is the snapshot.
type Universe struct {
	profiles []Profile // sorted by ticker
	index    map[string]int
	lists    []List
	tags     map[string]int // tag -> number of stocks
}

// New joins the sources.
func New(in Inputs) *Universe {
	u := &Universe{index: map[string]int{}}
	byCIK := map[string][]string{}
	add := func(p Profile) {
		if _, dup := u.index[p.Ticker]; dup || p.Ticker == "" {
			return
		}
		u.index[p.Ticker] = len(u.profiles)
		u.profiles = append(u.profiles, p)
	}

	for _, e := range in.Master.Entities {
		p := Profile{
			Ticker: Ticker(e.Ticker), Name: company.TidyName(e.Name), CIK: e.CIK,
			Exchange: e.Exchange, Source: SourceSEC,
		}
		if raw := company.CleanTags(e.Name); raw != p.Name {
			p.Aliases = append(p.Aliases, raw)
		}
		var rec company.Industry // the SEC record, when there is one
		if ind, ok := in.Industry[e.CIK]; ok && !ind.Missing {
			p.SIC, p.Industry, p.State, p.FiscalYear = ind.SIC, ind.Description, ind.State, ind.FiscalYear
			p.Category, p.Website, p.EntityType = ind.Category, ind.Website, ind.EntityType
			p.HQ, p.Phone, p.EIN, p.LEI, p.Investor = hq(ind.City, ind.Region), ind.Phone, ind.EIN, ind.LEI, ind.Investor
			rec = ind
			for _, f := range ind.Former {
				p.Aliases = append(p.Aliases, company.CleanTags(f))
			}
		}
		if sec, ok := in.Listing.Get(p.Ticker); ok {
			p.Venue, p.Market, p.Status = sec.Venue, sec.Market, sec.Status
			p.Security = sec.Name
			if p.Exchange == "" {
				p.Exchange = sec.Venue // the older SEC list has no exchanges
			}
			switch {
			case sec.Test:
				p.Kind = KindTest
			case sec.ETF:
				p.Kind = KindETF
			default:
				p.Kind = KindStock
			}
		}
		// A company the exchanges' lists lack (most of the over-the-counter ones)
		// still says where its stock trades, and an operating company with shares
		// is a stock.
		if p.Exchange == "" && len(rec.Exchanges) > 0 {
			p.Exchange = rec.Exchanges[0]
		}
		if p.Kind == "" && rec.EntityType == "operating" {
			p.Kind = KindStock
		}
		if e.CIK != "" {
			byCIK[e.CIK] = append(byCIK[e.CIK], p.Ticker)
		}
		add(p)
	}
	for _, x := range in.Layer.Extra {
		add(Profile{
			Ticker: Ticker(x.Ticker), Name: x.Name, Exchange: x.Exchange, ISIN: x.ISIN,
			Sector: x.Sector, Aliases: slices.Clone(x.Aliases), Source: SourceExtra,
		})
	}
	// Securities the exchanges list and the SEC list lacks complete the
	// catalogue. The SEC's entry, and yours, win where both have a ticker.
	for _, sec := range in.Listing.All() {
		p := Profile{
			Ticker: Ticker(sec.Symbol), Name: company.TidyName(sec.Name), Exchange: sec.Venue,
			Venue: sec.Venue, Market: sec.Market, Status: sec.Status, Security: sec.Name, Source: SourceListing,
		}
		switch {
		case sec.Test:
			p.Kind = KindTest
		case sec.ETF:
			p.Kind = KindETF
		default:
			p.Kind = KindStock
		}
		add(p)
	}
	named := func(t string) {
		if t = Ticker(t); t != "" {
			add(Profile{Ticker: t, Source: SourceUser})
		}
	}
	for _, t := range in.Known {
		named(t)
	}
	for _, d := range in.Layer.Lists {
		for _, t := range d.Tickers {
			named(t)
		}
	}
	for t := range in.Layer.Tags {
		named(t)
	}

	slices.SortFunc(u.profiles, func(a, b Profile) int { return strings.Compare(a.Ticker, b.Ticker) })
	for i := range u.profiles {
		p := &u.profiles[i]
		u.index[p.Ticker] = i
		if p.Website == "" {
			p.Website = in.Websites[p.Ticker]
		}
		// Aliases come from several sources in the order those list them; sorted
		// and without repeats they do not depend on it.
		slices.Sort(p.Aliases)
		p.Aliases = slices.Compact(slices.DeleteFunc(p.Aliases, func(a string) bool { return a == "" || a == p.Name }))
		if len(p.Aliases) == 0 {
			p.Aliases = nil
		}
	}

	// Membership, siblings and sectors are filled once the order is final.
	for _, d := range in.Layer.Lists {
		l := List{ID: d.ID, Name: d.Name, Tickers: cleanTickers(d.Tickers)}
		if l.Name == "" {
			l.Name = l.ID
		}
		u.lists = append(u.lists, l)
		for _, t := range l.Tickers {
			p := &u.profiles[u.index[t]]
			p.Lists = append(p.Lists, l.ID)
		}
	}
	slices.SortFunc(u.lists, func(a, b List) int { return strings.Compare(a.ID, b.ID) })
	u.tags = map[string]int{}
	for t, tags := range in.Layer.Tags {
		p := &u.profiles[u.index[Ticker(t)]]
		p.Tags = slices.Clone(tags)
		slices.Sort(p.Tags)
		for _, g := range p.Tags {
			u.tags[g]++
		}
	}
	for i := range u.profiles {
		p := &u.profiles[i]
		slices.Sort(p.Lists)
		if s, ok := in.Sectors[p.Ticker]; ok && s != "" {
			p.Sector = s
		}
		if sib := byCIK[p.CIK]; p.CIK != "" && len(sib) > 1 {
			for _, t := range sib {
				if t != p.Ticker {
					p.Siblings = append(p.Siblings, t)
				}
			}
			slices.Sort(p.Siblings)
		}
	}
	// The type is read last: the way the exchanges spell a SPAC's units, warrants
	// and rights (the share's ticker and one letter) needs the siblings.
	for i := range u.profiles {
		p := &u.profiles[i]
		p.typ, p.typed = p.work(), true
	}
	return u
}

// Fingerprint is a hash of everything the universe holds: the same inputs give
// the same fingerprint, whatever order the sources listed them in. It is what
// "the same data" means in tests and in the status line.
func (u *Universe) Fingerprint() string {
	if u == nil {
		return ""
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	enc.Encode(u.profiles)
	enc.Encode(u.lists)
	enc.Encode(u.Tags())
	return hex.EncodeToString(h.Sum(nil))
}

// Len is the number of profiles.
func (u *Universe) Len() int {
	if u == nil {
		return 0
	}
	return len(u.profiles)
}

// Get returns the profile of a ticker. The SEC's spelling (BRK-B) and the
// exchanges' (BRK.B) both find it.
func (u *Universe) Get(ticker string) (Profile, bool) {
	if u == nil {
		return Profile{}, false
	}
	t := Ticker(ticker)
	if i, ok := u.index[t]; ok {
		return u.profiles[i], true
	}
	if i, ok := u.index[company.NormalizeSymbol(t)]; ok {
		return u.profiles[i], true
	}
	return Profile{}, false
}

// All returns every profile, sorted by ticker. The slice is shared; do not
// change it.
func (u *Universe) All() []Profile {
	if u == nil {
		return nil
	}
	return u.profiles
}

// Lists returns the lists, sorted by ID.
func (u *Universe) Lists() []List {
	if u == nil {
		return nil
	}
	return u.lists
}

// List returns one list.
func (u *Universe) List(id string) (List, bool) {
	if u != nil {
		for _, l := range u.lists {
			if l.ID == strings.ToLower(strings.TrimSpace(id)) {
				return l, true
			}
		}
	}
	return List{}, false
}

// Count is how many of something there are.
type Count struct {
	Name  string
	Count int
}

// Tags returns every tag with the number of stocks that carry it, the most
// used first.
func (u *Universe) Tags() []Count {
	if u == nil {
		return nil
	}
	out := make([]Count, 0, len(u.tags))
	for t, n := range u.tags {
		out = append(out, Count{t, n})
	}
	sortCounts(out)
	return out
}

func sortCounts(c []Count) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Count != c[j].Count {
			return c[i].Count > c[j].Count
		}
		return c[i].Name < c[j].Name
	})
}

// Facets counts the profiles by exchange, kind and industry, to show how a
// result can be narrowed. Each list is sorted by count and holds at most n.
type Facets struct {
	Exchange, Kind, Industry, List, Tag []Count
}

// FacetsOf counts a result.
func FacetsOf(hits []Hit, n int) Facets {
	count := func(get func(p *Profile) []string) []Count {
		m := map[string]int{}
		for _, h := range hits {
			for _, v := range get(h.Profile) {
				if v != "" {
					m[v]++
				}
			}
		}
		out := make([]Count, 0, len(m))
		for k, v := range m {
			out = append(out, Count{k, v})
		}
		sortCounts(out)
		if len(out) > n {
			out = out[:n]
		}
		return out
	}
	one := func(f func(p *Profile) string) func(p *Profile) []string {
		return func(p *Profile) []string { return []string{f(p)} }
	}
	return Facets{
		Exchange: count(one(func(p *Profile) string { return p.Exchange })),
		Kind:     count(one(func(p *Profile) string { return p.Kind })),
		Industry: count(one(func(p *Profile) string { return p.Industry })),
		List:     count(func(p *Profile) []string { return p.Lists }),
		Tag:      count(func(p *Profile) []string { return p.Tags }),
	}
}

// Sources say where Load finds the inputs.
type Sources struct {
	// CompanyDir holds the cached SEC and Nasdaq files (data/company).
	CompanyDir string
	// LayerPath is the tenant's stocks.yaml.
	LayerPath string
	Sectors   map[string]string
	Known     []string
}

// Load reads the cached sources and joins them. It returns a usable Universe
// even when a source is damaged or missing: the problems come back as one
// error that the caller can show as a warning. A source that has not been
// fetched yet is not a problem.
func Load(s Sources) (*Universe, error) {
	var errs []error
	in := Inputs{Sectors: s.Sectors, Known: s.Known}
	var err error
	if in.Master, err = company.Load(s.CompanyDir); err != nil {
		errs = append(errs, err)
	}
	if in.Listing, err = company.LoadListing(s.CompanyDir); err != nil {
		errs = append(errs, err)
	}
	if in.Industry, err = company.LoadIndustry(s.CompanyDir); err != nil {
		errs = append(errs, err)
	}
	if sites, err := company.LoadWebsites(s.CompanyDir); err != nil {
		errs = append(errs, err)
	} else {
		in.Websites = make(map[string]string, len(sites))
		for t, w := range sites {
			if w.URL != "" {
				in.Websites[t] = w.URL
			}
		}
	}
	if s.LayerPath != "" {
		if in.Layer, err = LoadLayer(s.LayerPath); err != nil {
			errs = append(errs, err)
		}
	}
	return New(in), errors.Join(errs...)
}

// hq writes the business address as "Miramar, FL": the SEC writes the city in
// capitals.
func hq(city, region string) string {
	words := strings.Fields(strings.ToLower(city))
	for i, w := range words {
		r := []rune(w)
		words[i] = strings.ToUpper(string(r[:1])) + string(r[1:])
	}
	parts := []string{strings.Join(words, " ")}
	if region != "" {
		parts = append(parts, region)
	}
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), ", ")
}
