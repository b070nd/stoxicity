package company

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// SitesURL is Wikidata's query service. Wikidata is open data (CC0) and says
// which company has which official website (property P856) and under which
// ticker it trades (P249, a qualifier of its stock exchange, P414). The SEC no
// longer fills in the website of a company, so this is where a stock's own
// site, and with it its news feed, comes from.
const SitesURL = "https://query.wikidata.org/sparql"

const websitesFile = "websites.json"

// Ages after which a website is asked for again: a found one is stable, a
// missing one may have been added since.
const (
	DefaultWebsiteAge = 90 * 24 * time.Hour
	MissingWebsiteAge = 30 * 24 * time.Hour
)

// sitesBatch is how many tickers one query asks about; the query is a GET and
// must stay well under the length servers accept.
const sitesBatch = 40

// Website is what is known of the official website of one ticker.
type Website struct {
	// URL is empty when Wikidata has no such company, or none that matches the
	// name; the question is not asked again until MissingWebsiteAge has passed.
	URL string `json:"url,omitempty"`
	// Label is the name Wikidata knows the company by, for the record.
	Label     string    `json:"label,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
}

// WebsiteCache holds the websites by ticker.
type WebsiteCache map[string]Website

// LoadWebsites reads the cache in dir. A missing file is an empty cache.
func LoadWebsites(dir string) (WebsiteCache, error) {
	raw, err := os.ReadFile(filepath.Join(dir, websitesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return WebsiteCache{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("company: %w", err)
	}
	var c WebsiteCache
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("company: %s is damaged (delete it and run \"stoxicity universe enrich\"): %w", websitesFile, err)
	}
	return c, nil
}

func saveWebsites(dir string, c WebsiteCache) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("company: %w", err)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("company: %w", err)
	}
	return writeFile(filepath.Join(dir, websitesFile), raw)
}

// Tickers returns the tickers of the cache, sorted.
func (c WebsiteCache) Tickers() []string { return slices.Sorted(maps.Keys(c)) }

// SiteTarget is a stock to find the website of: the name is what the company
// found must match, so that a ticker another company uses on another exchange
// does not give the wrong site.
type SiteTarget struct{ Ticker, Name string }

// SiteOptions are the settings of EnrichWebsites.
type SiteOptions struct {
	// URL replaces SitesURL; tests point it at a server.
	URL   string
	Force bool
	// Max is the most tickers to ask about; 0 means no limit.
	Max      int
	Now      func() time.Time
	Progress func(done, total int)
}

// SiteResult says what EnrichWebsites did.
type SiteResult struct {
	Found   int      // tickers given a website
	Missing int      // tickers Wikidata has no matching company with a website for
	Skipped int      // tickers whose answer was young enough
	Left    int      // tickers not asked because of Max
	Failed  []string // one line per query that failed
}

// EnrichWebsites asks Wikidata for the website of each ticker that has no
// answer or an old one and stores the answers in dir. Tickers go in batches of
// sitesBatch, one query after the other, in ticker order, and the cache is
// written after every batch.
func EnrichWebsites(ctx context.Context, client httpx.Client, dir string, targets []SiteTarget, opts SiteOptions) (SiteResult, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	endpoint := opts.URL
	if endpoint == "" {
		endpoint = SitesURL
	}
	cache, err := LoadWebsites(dir)
	if err != nil {
		cache = WebsiteCache{}
	}
	var res SiteResult
	var todo []SiteTarget
	seen := map[string]bool{}
	for _, t := range targets {
		if t.Ticker == "" || seen[t.Ticker] {
			continue
		}
		seen[t.Ticker] = true
		if old, ok := cache[t.Ticker]; ok && !opts.Force {
			age := DefaultWebsiteAge
			if old.URL == "" {
				age = MissingWebsiteAge
			}
			if now().Sub(old.FetchedAt) < age {
				res.Skipped++
				continue
			}
		}
		todo = append(todo, t)
	}
	slices.SortFunc(todo, func(a, b SiteTarget) int { return strings.Compare(a.Ticker, b.Ticker) })
	if opts.Max > 0 && len(todo) > opts.Max {
		res.Left = len(todo) - opts.Max
		todo = todo[:opts.Max]
	}
	for from := 0; from < len(todo); from += sitesBatch {
		if err := ctx.Err(); err != nil {
			res.Left += len(todo) - from
			return res, errors.Join(err, saveWebsites(dir, cache))
		}
		batch := todo[from:min(from+sitesBatch, len(todo))]
		found, err := askSites(ctx, client, endpoint, batch)
		var he *httpx.Error
		switch {
		case errors.As(err, &he) && (he.Class == httpx.Throttled || he.Class == httpx.Blocked):
			res.Left += len(todo) - from
			return res, errors.Join(fmt.Errorf("company: Wikidata is pushing back, stopping: %w", err), saveWebsites(dir, cache))
		case err != nil:
			res.Failed = append(res.Failed, fmt.Sprintf("websites %s…: %v", batch[0].Ticker, err))
		default:
			for _, t := range batch {
				w := found[t.Ticker]
				w.FetchedAt = now().UTC()
				cache[t.Ticker] = w
				if w.URL != "" {
					res.Found++
				} else {
					res.Missing++
				}
			}
			if err := saveWebsites(dir, cache); err != nil {
				return res, err
			}
		}
		if opts.Progress != nil {
			opts.Progress(min(from+sitesBatch, len(todo)), len(todo))
		}
	}
	return res, nil
}

// sitesQuery asks, for each ticker, for the companies that trade under it and
// their websites with the rank Wikidata gives them.
func sitesQuery(batch []SiteTarget) string {
	var b strings.Builder
	b.WriteString("SELECT ?t ?label ?site ?rank WHERE { VALUES ?t {")
	for _, t := range batch {
		fmt.Fprintf(&b, " %q", t.Ticker)
	}
	b.WriteString(" } ?item p:P414 ?st . ?st pq:P249 ?t . ?item rdfs:label ?label . FILTER(LANG(?label) = \"en\")" +
		" ?item p:P856 ?ws . ?ws ps:P856 ?site ; wikibase:rank ?rank . FILTER(?rank != wikibase:DeprecatedRank) }")
	return b.String()
}

type sitesAnswer struct {
	Results struct {
		Bindings []map[string]struct {
			Value string `json:"value"`
		} `json:"bindings"`
	} `json:"results"`
}

func sitesRequestURL(endpoint string, batch []SiteTarget) string {
	return endpoint + "?" + url.Values{"query": {sitesQuery(batch)}, "format": {"json"}}.Encode()
}

func askSites(ctx context.Context, client httpx.Client, endpoint string, batch []SiteTarget) (map[string]Website, error) {
	resp, err := client.Do(ctx, httpx.Request{URL: sitesRequestURL(endpoint, batch), Header: map[string][]string{"Accept": {"application/sparql-results+json"}}})
	if err != nil {
		return nil, err
	}
	var ans sitesAnswer
	if err := json.Unmarshal(resp.Body, &ans); err != nil {
		return nil, fmt.Errorf("company: read Wikidata's answer: %w", err)
	}
	names := map[string]string{}
	for _, t := range batch {
		names[t.Ticker] = t.Name
	}
	type cand struct {
		label, site string
		preferred   bool
	}
	cands := map[string][]cand{}
	for _, b := range ans.Results.Bindings {
		t := b["t"].Value
		site, ok := cleanSite(b["site"].Value)
		if !ok || !SameCompany(names[t], b["label"].Value) {
			continue
		}
		cands[t] = append(cands[t], cand{b["label"].Value, site, strings.HasSuffix(b["rank"].Value, "PreferredRank")})
	}
	out := map[string]Website{}
	for t, list := range cands {
		// A preferred site first, then the shortest address (the home page, not
		// a page of a country or a language), then the alphabetical first, so the
		// answer does not depend on the order Wikidata lists them in.
		slices.SortFunc(list, func(a, b cand) int {
			switch {
			case a.preferred != b.preferred:
				if a.preferred {
					return -1
				}
				return 1
			case len(a.site) != len(b.site):
				return len(a.site) - len(b.site)
			}
			return strings.Compare(a.site, b.site)
		})
		out[t] = Website{URL: list[0].site, Label: list[0].label}
	}
	return out, nil
}

// cleanSite accepts an http or https address with a host and returns it
// without a fragment or query.
func cleanSite(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || !strings.Contains(u.Hostname(), ".") {
		return "", false
	}
	u.Fragment, u.RawQuery = "", ""
	return u.String(), true
}

var companySuffixes = map[string]bool{
	"inc": true, "incorporated": true, "corp": true, "corporation": true, "co": true, "company": true, "ltd": true,
	"limited": true, "plc": true, "llc": true, "lp": true, "sa": true, "nv": true, "ag": true, "the": true, "holdings": true,
	"holding": true, "group": true, "de": true, "cl": true, "class": true, "trust": true,
}

// nameCore is a company name reduced to its distinctive words: lower case,
// letters and digits only, without the words every company name ends in.
func nameCore(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var keep []string
	for _, w := range words {
		if !companySuffixes[w] {
			keep = append(keep, w)
		}
	}
	return strings.Join(keep, "")
}

// SameCompany reports whether two names can be one company's: after dropping
// "Inc", "Corp", "The" and the like they are equal, or one begins with the
// other (at least four letters long), as "Alphabet" and "Alphabet Inc." do.
func SameCompany(a, b string) bool {
	x, y := nameCore(a), nameCore(b)
	if x == "" || y == "" {
		return false
	}
	if x == y {
		return true
	}
	if len(x) > len(y) {
		x, y = y, x
	}
	return len(x) >= 4 && strings.HasPrefix(y, x)
}
