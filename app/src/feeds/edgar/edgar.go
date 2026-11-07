// Package edgar reads SEC EDGAR: new filings as they appear, the history of
// the companies that are followed, and structured facts for the stock pages.
// The design, endpoints and open questions are in docs/fetchers.md.
//
// All requests go through one shared limiter of at most 10 requests a second
// for the whole program, whatever the number of sources, and carry the
// User-Agent built from STOXICITY_CONTACT. Both are the job of the httpx client
// this package is given; nothing here sets, logs or returns the User-Agent.
//
// Fetcher serves model.KindFilings sources and picks the reader from the
// source: a URL whose path contains /submissions/ is read by Submissions, a
// source tagged "form4" by Form4, and anything else by Current. Files:
//
//	current.go      the current-filings Atom feed, read page by page
//	submissions.go  per-company filing history (data.sec.gov)
//	form4.go        insider transactions: the current feed narrowed to Form 4
//	ownership.go    what a Form 4 reports: who traded how many shares at what price
//	facts.go        XBRL company facts for fundamentals
//	bulk.go         nightly bulk files, for the first load and for back-fill
//	index.go        daily index files, to back-fill since the configured start date
//	accession.go    accession numbers: the article key and the dedupe key
//	items.go        8-K item numbers mapped to model.EventKind
//	ciks.go         the ticker and CIK list that entity resolution uses
package edgar

import (
	"bytes"
	"context"
	"errors"
	"html"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/sanitize"
)

const (
	// DefaultWebBase is where the current-filings feed and the filing pages live.
	DefaultWebBase = "https://www.sec.gov"
	// DefaultDataBase is where the submissions files live.
	DefaultDataBase = "https://data.sec.gov"
	// DefaultMaxPages is how many 100-entry pages of the current feed are read
	// per form and poll.
	DefaultMaxPages = 5
	// DefaultSeenCap is how many accession numbers State.Seen remembers.
	DefaultSeenCap = 2000
	// DefaultMaxFilings is how many new filings are taken from one company's
	// submissions file per poll.
	DefaultMaxFilings = 100

	domain          = "sec.gov"
	futureTolerance = 5 * time.Minute
)

// Config tunes the fetchers of this package. The zero value uses the SEC's own
// addresses and the defaults above; tests point the bases at httptest servers.
type Config struct {
	// WebBase is the scheme and host of www.sec.gov, without a trailing slash.
	WebBase string
	// DataBase is the scheme and host of data.sec.gov, without a trailing slash.
	DataBase string
	// Tickers maps a CIK written with ten digits to the symbols of that company.
	// It is optional; without it Article.Tickers stays empty for the current
	// feed. TickersByCIK builds it from the SEC's ticker list.
	Tickers map[string][]string
	// Now is the clock; time.Now if nil.
	Now func() time.Time
	// MaxPages limits pages per form and poll; DefaultMaxPages if 0.
	MaxPages int
	// SeenCap limits State.Seen, dropping the oldest; DefaultSeenCap if 0.
	SeenCap int
	// MaxFilings limits new filings per company and poll; DefaultMaxFilings if 0.
	MaxFilings int
}

func (c Config) withDefaults() Config {
	c.WebBase = strings.TrimRight(c.WebBase, "/")
	c.DataBase = strings.TrimRight(c.DataBase, "/")
	if c.WebBase == "" {
		c.WebBase = DefaultWebBase
	}
	if c.DataBase == "" {
		c.DataBase = DefaultDataBase
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.MaxPages <= 0 {
		c.MaxPages = DefaultMaxPages
	}
	if c.SeenCap <= 0 {
		c.SeenCap = DefaultSeenCap
	}
	if c.MaxFilings <= 0 {
		c.MaxFilings = DefaultMaxFilings
	}
	return c
}

// TickersByCIK builds Config.Tickers from entities such as those ParseTickers
// returns. Entities without a CIK or ticker are skipped.
func TickersByCIK(entities []model.Entity) map[string][]string {
	out := make(map[string][]string)
	for _, e := range entities {
		cik := padCIK(e.CIK)
		if cik == "" || e.Ticker == "" || slices.Contains(out[cik], e.Ticker) {
			continue
		}
		out[cik] = append(out[cik], e.Ticker)
	}
	return out
}

// Fetcher reads every kind of EDGAR source. It implements feeds.Fetcher.
type Fetcher struct {
	current     *Current
	submissions *Submissions
	form4       *Form4
}

// New returns a Fetcher that makes all requests through client.
func New(client httpx.Client, cfg Config) *Fetcher {
	return &Fetcher{
		current:     NewCurrent(client, cfg),
		submissions: NewSubmissions(client, cfg),
		form4:       NewForm4(client, cfg),
	}
}

var submissionsPath = regexp.MustCompile(`^https?://[^/?#]+/submissions/`)

// Fetch hands the poll to the reader that fits src.
func (f *Fetcher) Fetch(ctx context.Context, src model.Source, prev feeds.State) (feeds.Result, error) {
	switch {
	case slices.Contains(src.Tags, "form4"):
		return f.form4.Fetch(ctx, src, prev)
	case submissionsPath.MatchString(src.URL):
		return f.submissions.Fetch(ctx, src, prev)
	}
	return f.current.Fetch(ctx, src, prev)
}

// looksLikeHTML reports whether body starts, after an optional byte order mark
// and whitespace, with an HTML document. The SEC answers a burst of requests
// with an HTML rate-limit page and status 200.
func looksLikeHTML(body []byte) bool {
	b := bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	b = bytes.TrimLeft(b, " \t\r\n")
	if len(b) > 15 {
		b = b[:15]
	}
	b = bytes.ToLower(b)
	return bytes.HasPrefix(b, []byte("<!doctype html")) || bytes.HasPrefix(b, []byte("<html"))
}

// blockedError is what a 200 response with an HTML page gives.
func blockedError(rawURL string, status int) error {
	return &httpx.Error{
		Class: httpx.Blocked, Status: status, URL: stripQuery(rawURL),
		Err: errors.New("edgar: got an HTML page instead of data (rate limit page?)"),
	}
}

// permanentError marks content that will not get better by asking again.
func permanentError(rawURL string, status int, err error) error {
	return &httpx.Error{Class: httpx.Permanent, Status: status, URL: stripQuery(rawURL), Err: err}
}

// mustStop reports whether err means the whole of EDGAR should be left alone for
// a while, so a partial result must not hide it.
func mustStop(err error) bool {
	var he *httpx.Error
	return errors.As(err, &he) && (he.Class == httpx.Blocked || he.Class == httpx.Throttled)
}

func stripQuery(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

var markup = regexp.MustCompile(`(?s)<[a-zA-Z/!][^>]*>`)

// cleanText strips markup and entities, removes hostile characters and
// collapses white space to single spaces.
func cleanText(s string) string {
	s = markup.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(sanitize.Text(s)), " ")
}

// padCIK returns digits written as ten digits with leading zeros, or "" if s is
// not a CIK.
func padCIK(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 10 || strings.Trim(s, "0123456789") != "" {
		return ""
	}
	return strings.Repeat("0", 10-len(s)) + s
}

// mergeSeen puts newIDs (newest first) in front of prev and keeps limit IDs.
func mergeSeen(newIDs, prev []string, limit int) []string {
	out := make([]string, 0, len(newIDs)+len(prev))
	out = append(out, newIDs...)
	out = append(out, prev...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

var eastern = sync.OnceValue(func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
})

// parseTime reads a timestamp from EDGAR. One with an offset or Z is exact. One
// without is taken as US Eastern time, EDGAR's own clock (the feed's offset is
// unverified, see docs/fetchers.md section 7.6). The result is UTC.
func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), true
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, eastern()); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// published picks an article's time: t if it is known and not in the future,
// else now, with the reason added to the article's score notes.
func published(t time.Time, known bool, now time.Time) (time.Time, []string) {
	switch {
	case !known:
		return now, []string{"published time missing, fetch time used"}
	case t.After(now.Add(futureTolerance)):
		return now, []string{"published time in the future, fetch time used"}
	}
	return t, nil
}
