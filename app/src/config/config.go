// Package config loads the configuration folder: config.yaml for settings and
// sources.yaml for the list of news sources.
package config

import (
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/quota"
)

// Config is the parsed config.yaml.
type Config struct {
	// Since is the first day the aggregator covers, as YYYY-MM-DD.
	Since string `yaml:"since"`
	// Timezone is the IANA zone that decides which day an article belongs to,
	// for the timeline and the daily digest. Use the market's zone, not UTC.
	Timezone string `yaml:"timezone"`
	// DataDir holds the database and other runtime state.
	DataDir string `yaml:"data_dir"`
	// DatabaseURL, when set, makes the program keep its data in that Postgres
	// database instead of SQLite files in DataDir. It is meant for
	// STOXICITY_DATABASE_URL in .env.local, not for this file: it may hold a password.
	DatabaseURL string `yaml:"database_url"`
	// Watchlist is the symbols the user already follows.
	Watchlist []string `yaml:"watchlist"`
	Lens      Lens     `yaml:"lens"`
	Fetch     Fetch    `yaml:"fetch"`
	Refresh   Refresh  `yaml:"refresh"`
	Laya      Laya     `yaml:"laya"`
	Company   Company  `yaml:"company"`
	Tenancy   Tenancy  `yaml:"tenancy"`
	// Topics are the themes an article can be filed under. See package classify.
	Topics []TopicRule `yaml:"topics"`
	// Sectors are the sectors an article can belong to. See package classify.
	Sectors []SectorRule `yaml:"sectors"`
}

// TopicRule says when an article belongs to one topic: when its text holds any
// of Words as a whole word or phrase, or any of Tickers is among its tickers.
type TopicRule struct {
	// Name is the topic as filters and watches write it: a lower-case slug.
	Name    string   `yaml:"name"`
	Words   []string `yaml:"words"`
	Tickers []string `yaml:"tickers"` // upper-case symbols
}

// SectorRule says what points to one sector. An article gets the sector that
// scores highest: 3 for each ticker among its tickers, 1 for each distinct
// word found in its text.
type SectorRule struct {
	// Name is the sector as filters and watches write it: a lower-case slug.
	Name    string   `yaml:"name"`
	Words   []string `yaml:"words"`
	Tickers []string `yaml:"tickers"` // upper-case symbols
}

// Tenancy says where tenants live. A single-user install keeps the defaults
// and never thinks about it.
type Tenancy struct {
	// Dir holds one folder per tenant, relative to the configuration root.
	Dir string `yaml:"dir"`
	// Default is the tenant used when none is named.
	Default string `yaml:"default"`
}

// Refresh tunes the background scheduler.
type Refresh struct {
	Enabled    bool        `yaml:"enabled"`
	MaxBackoff string      `yaml:"max_backoff"` // longest wait after repeated failures
	PerHost    int         `yaml:"per_host"`    // polls of one host (one domain) at once
	Workers    int         `yaml:"workers"`     // polls at once over all hosts
	Fast       FastRefresh `yaml:"fast"`
}

// FastRefresh is the faster polling of filings sources while the US market
// day is on. It applies only to sources of kind filings (SEC EDGAR), because
// that is where minutes matter, and only when Enabled is true.
type FastRefresh struct {
	Enabled bool `yaml:"enabled"`
	// Interval is the polling interval inside the window, such as "2m".
	Interval string `yaml:"interval"`
	// Zone is the IANA zone the window is read in, so that it follows daylight
	// saving there.
	Zone string `yaml:"zone"`
	// From and To bound the window as HH:MM in Zone. From is inside, To is
	// not. To must be later than From; a window does not cross midnight.
	From string `yaml:"from"`
	To   string `yaml:"to"`
	// WeekdaysOnly leaves out Saturday and Sunday in Zone.
	WeekdaysOnly bool `yaml:"weekdays_only"`
}

// Company is the company master: the SEC's list of tickers, names, CIKs and
// exchanges, with the Nasdaq symbol directory and the SEC's industry records,
// cached under data_dir/company. See package company.
type Company struct {
	// Enabled makes fetch and run keep the cached list up to date. Off, the
	// cache is still read, and "stoxicity company update" still works.
	Enabled bool `yaml:"enabled"`
	// MaxAge is how old the cached list may get before the SEC is asked again.
	MaxAge string `yaml:"max_age"`
	// URL replaces the SEC's address, for a mirror or a test. Empty means the
	// SEC.
	URL string `yaml:"url"`

	// Listing keeps the Nasdaq Trader symbol directory next to the list, to
	// tell ETFs from stocks and to show the market tier and financial status.
	// Nasdaq allows one personal, non-commercial copy of its pages; the copy
	// stays in the data folder.
	Listing bool `yaml:"listing"`
	// NasdaqURL, OtherURL and SubmissionsURL replace the addresses of the two
	// directory files and of the SEC's per-company record (which must hold %s
	// for the ten-digit CIK). Empty means the real ones; tests set them.
	NasdaqURL      string `yaml:"nasdaq_url"`
	OtherURL       string `yaml:"other_url"`
	SubmissionsURL string `yaml:"submissions_url"`
	// SitesURL replaces the address of Wikidata's query service, where the
	// official website of each stock comes from (the SEC leaves its own field
	// empty). Empty means Wikidata.
	SitesURL string `yaml:"sites_url"`

	// RefreshEvery is how often "stoxicity run" refreshes the reference
	// database in the background: it asks the sources that are due (company.
	// max_age decides which) and fetches EnrichPerCycle company records. A
	// duration, or "off".
	RefreshEvery string `yaml:"refresh_every"`
	// EnrichPerCycle is how many SEC company records (industry, state, fiscal
	// year) one background cycle fetches. The ones you follow come first, then
	// the rest in CIK order, so the whole list fills in over a few days. Zero
	// fetches none.
	EnrichPerCycle int `yaml:"enrich_per_cycle"`
	// Quotes is where daily candles come from: "polygon" (a key of your own in
	// STOXICITY_POLYGON_KEY; the whole market a day at a time) or "off". Without
	// a key nothing is asked. QuotesPerCycle is how many market days one
	// background cycle brings up to date (the free plan allows five requests a
	// minute); QuotesURL replaces the provider's address, for a test.
	Quotes         string `yaml:"quotes"`
	QuotesPerCycle int    `yaml:"quotes_per_cycle"`
	QuotesURL      string `yaml:"quotes_url"`
	// History fills the 12M/10Y strips from public monthly chart data, separately
	// from daily candles. Zero HistoryPerCycle disables background filling.
	History         string `yaml:"history"`
	HistoryURL      string `yaml:"history_url"`
	HistoryPerCycle int    `yaml:"history_per_cycle"`
	HistoryEvery    string `yaml:"history_every"`
	// Fundamentals is where revenue, profit, assets, equity and shares come
	// from: "sec" (the XBRL frames of the SEC, no key, a few dozen small
	// requests a week for the whole market) or "off". FundamentalsURL replaces
	// the frames address, for a test.
	Fundamentals    string `yaml:"fundamentals"`
	FundamentalsURL string `yaml:"fundamentals_url"`
	// Insiders is where insider trades come from: "sec" (the Form 4 filings the
	// feed found, one small request each) or "off". InsidersPerCycle is how many
	// unread filings one background cycle reads (the whole backlog of a few
	// hundred a day is a minute of requests); InsidersURL replaces the SEC's web
	// address, for a test.
	//
	// CoversPerCycle is how many companies' cover pages one background cycle
	// reads for the share count of each class of stock (one request for the
	// company's record of filings and one for the start of the newest report).
	// Zero reads none, and then no class-level market value is available.
	CoversPerCycle   int    `yaml:"covers_per_cycle"`
	Insiders         string `yaml:"insiders"`
	InsidersPerCycle int    `yaml:"insiders_per_cycle"`
	InsidersURL      string `yaml:"insiders_url"`
	// PolygonKey is read from STOXICITY_POLYGON_KEY or MASSIVE_API_KEY (Polygon.io
	// is now called Massive), never from a file that is
	// checked in.
	PolygonKey string `yaml:"-"`
	// SitesPerCycle is how many tickers one background cycle looks up the
	// official website of, forty to a request. Zero looks up none.
	SitesPerCycle int `yaml:"sites_per_cycle"`
	// FeedSearchPerCycle is how many company websites one background cycle
	// reads to find the company's own news feed (the SEC filing record of each
	// stock needs no search). The ones you follow come first. A website is read
	// again only after a month. Zero looks for none.
	FeedSearchPerCycle int `yaml:"feed_search_per_cycle"`
}

// Laya points at a laya-serve sidecar used for the second scoring stage.
// When Enabled is false the keyword rules in Lens decide alone.
type Laya struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`   // for example http://127.0.0.1:8000
	Model   string `yaml:"model"` // checkpoint name
	// MinConfidence is the confidence an answer needs before it may change a
	// score. laya's confidence is not calibrated; the number is a starting
	// point to tune on labels.
	MinConfidence float64 `yaml:"min_confidence"`
	Timeout       string  `yaml:"timeout"`
	// Apply lists the answers that change the score: any of the names in
	// LayaFields. The others are recorded in the score's reasons only.
	Apply []string `yaml:"apply"`
	// RelevanceWeight is the weight, 0 to 1, of laya's relevance when "relevance"
	// is applied.
	RelevanceWeight float64 `yaml:"relevance_weight"`
	// BatchSize is how many articles go into one request to the sidecar.
	BatchSize int `yaml:"batch_size"`
	// Manage lets "stoxicity run" start the server when nothing answers at URL
	// and stop it when run ends. It only applies to a server on this machine.
	Manage bool `yaml:"manage"`
	// Command is the program that starts the server: a name in PATH or a path.
	Command string `yaml:"command"`
	// StartTimeout is how long run waits for a started server to answer; the
	// first start loads the checkpoints.
	StartTimeout string `yaml:"start_timeout"`

	// The next three apply to a server that "run" starts, and make its answers
	// repeatable: the same article gets the same opinion on every start.
	//
	// Device is the torch device ("cpu", "mps", "cuda"); empty lets laya pick,
	// which can differ between machines and runs of the same machine. CPU is
	// the repeatable choice.
	Device string `yaml:"device"`
	// Threads caps the CPU threads of the server; 0 leaves torch's choice.
	Threads int `yaml:"threads"`
	// Offline stops the server from asking the model hub for anything, so the
	// checkpoints are exactly the ones already downloaded. Set it to false for
	// the first start, which downloads them.
	Offline bool `yaml:"offline"`
}

// LayaFields are the names Laya.Apply accepts.
var LayaFields = []string{"kind", "tone", "sector", "topics", "materiality", "relevance"}

// Applies reports whether the answer called field is switched on in Apply.
func (l Laya) Applies(field string) bool { return slices.Contains(l.Apply, field) }

// Lens tunes the long-term-investor filter. See package lens.
type Lens struct {
	// Threshold is the lowest relevance score that counts as relevant.
	Threshold float64 `yaml:"threshold"`
	// Boost words raise relevance (earnings, moat, buyback, ...).
	Boost []string `yaml:"boost"`
	// Demote words lower it (price target, intraday, meme, ...).
	Demote []string `yaml:"demote"`
}

// Fetch tunes how sources are polled.
type Fetch struct {
	// Contact is an email address or https URL of the person running the
	// program. It goes into the User-Agent; the SEC blocks clients without one.
	Contact  string `yaml:"contact"`
	Interval string `yaml:"interval"` // for example "30m"
	Timeout  string `yaml:"timeout"`  // per request
	// Quotas are installation-wide request budgets, keyed by lower-case host
	// (or "sec" for the grouped SEC hosts). No subscription allowance is assumed.
	Quotas map[string]RequestQuota `yaml:"quotas"`
}

type RequestQuota struct {
	Requests int    `yaml:"requests"`
	Period   string `yaml:"period"` // duration, e.g. 1m or 24h; starts at first attempt
}

// Limit converts a validated configuration budget for enforcement and status.
func (q RequestQuota) Limit() quota.Limit {
	period, _ := time.ParseDuration(q.Period)
	return quota.Limit{Requests: q.Requests, Period: period}
}

// Location returns the zone named by Timezone, or UTC if the name does not
// load. Load rejects such a name, so UTC only appears on an unvalidated Config.
func (c Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// SinceTime returns midnight at the start of Since in Location. It returns the
// zero time if Since does not parse.
func (c Config) SinceTime() time.Time {
	t, err := time.ParseInLocation(dateLayout, c.Since, c.Location())
	if err != nil {
		return time.Time{}
	}
	return t
}

// FetchInterval returns Fetch.Interval as a duration, or zero if it does not
// parse.
func (c Config) FetchInterval() time.Duration { return parseDuration(c.Fetch.Interval) }

// FetchTimeout returns Fetch.Timeout as a duration, or zero if it does not
// parse.
func (c Config) FetchTimeout() time.Duration { return parseDuration(c.Fetch.Timeout) }

// MaxBackoff returns Refresh.MaxBackoff as a duration, or zero if it does not
// parse.
func (c Config) MaxBackoff() time.Duration { return parseDuration(c.Refresh.MaxBackoff) }

// CompanyMaxAge returns Company.MaxAge as a duration, or zero if it does not
// parse.
func (c Config) CompanyMaxAge() time.Duration { return parseDuration(c.Company.MaxAge) }

// CompanyRefreshEvery returns Company.RefreshEvery as a duration; zero means
// the background refresh is off.
func (c Config) CompanyRefreshEvery() time.Duration {
	if strings.EqualFold(strings.TrimSpace(c.Company.RefreshEvery), "off") {
		return 0
	}
	return parseDuration(c.Company.RefreshEvery)
}

// LayaTimeout returns Laya.Timeout as a duration, or zero if it does not parse.
func (c Config) LayaTimeout() time.Duration { return parseDuration(c.Laya.Timeout) }

// LayaStartTimeout returns Laya.StartTimeout as a duration, or zero if it does
// not parse.
func (c Config) LayaStartTimeout() time.Duration { return parseDuration(c.Laya.StartTimeout) }

// IntervalFor is the polling interval of a source at the time now, for
// scheduler.Options.IntervalFor. It is the fast interval for a filings source
// when refresh.fast is enabled and now falls inside its window, and zero
// otherwise, which makes the scheduler use the source's own interval or the
// default.
func (c Config) IntervalFor(src model.Source, now time.Time) time.Duration {
	f := c.Refresh.Fast
	if !f.Enabled || src.Kind != model.KindFilings {
		return 0
	}
	loc, err := time.LoadLocation(f.Zone)
	if err != nil {
		return 0
	}
	local := now.In(loc)
	if f.WeekdaysOnly && (local.Weekday() == time.Saturday || local.Weekday() == time.Sunday) {
		return 0
	}
	from, ok1 := minutesOfDay(f.From)
	to, ok2 := minutesOfDay(f.To)
	if !ok1 || !ok2 {
		return 0
	}
	if m := local.Hour()*60 + local.Minute(); m < from || m >= to {
		return 0
	}
	return parseDuration(f.Interval)
}

// minutesOfDay reads "HH:MM" as minutes since midnight.
func minutesOfDay(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// parseDuration returns s as a duration, or zero if s does not parse.
func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
