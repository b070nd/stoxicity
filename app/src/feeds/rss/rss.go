// Package rss reads RSS 2.0, RSS 1.0 (RDF) and Atom feeds: central banks,
// regulators and company investor-relations pages.
//
// A poll sends the stored ETag and Last-Modified, so an unchanged feed costs
// one cheap 304. A changed feed is decoded in its declared character set,
// every text field is stripped of markup and passed through package sanitize,
// and only entries whose ID is not in State.Seen are returned. An entry's ID
// is a hash of its canonical link (tracking parameters and fragment removed),
// or of its GUID when it has no link. Published is the feed's time in UTC; a
// missing or future time is replaced by the fetch time and noted in
// Score.Reasons. Only a headline, a link and a short extract are kept.
package rss

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/sanitize"
)

const (
	// DefaultMaxEntries is how many entries of one feed are read per poll.
	DefaultMaxEntries = 500
	// DefaultSeenCap is how many article IDs State.Seen remembers.
	DefaultSeenCap = 2000

	summaryRunes    = 600
	futureTolerance = 5 * time.Minute
)

// Config tunes a Fetcher. The zero value is usable.
type Config struct {
	// Now is the clock; time.Now if nil.
	Now func() time.Time
	// MaxEntries limits the entries read from one feed per poll; DefaultMaxEntries if 0.
	MaxEntries int
	// SeenCap limits State.Seen, dropping the oldest IDs; DefaultSeenCap if 0.
	SeenCap int
}

// Fetcher reads RSS and Atom feeds. It implements feeds.Fetcher.
type Fetcher struct {
	client httpx.Client
	cfg    Config
}

// New returns a Fetcher that makes all requests through client.
func New(client httpx.Client, cfg Config) *Fetcher {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = DefaultMaxEntries
	}
	if cfg.SeenCap <= 0 {
		cfg.SeenCap = DefaultSeenCap
	}
	return &Fetcher{client: client, cfg: cfg}
}

// Fetch polls the feed at src.URL. A 304 returns NotModified with prev
// unchanged. An HTML page instead of a feed is a Blocked error; a body that is
// not a feed is a Permanent error. Errors from the client pass through.
func (f *Fetcher) Fetch(ctx context.Context, src model.Source, prev feeds.State) (feeds.Result, error) {
	resp, err := f.client.Do(ctx, httpx.Request{
		URL:          src.URL,
		Header:       http.Header{"Accept": {"application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5"}},
		ETag:         prev.ETag,
		LastModified: prev.LastModified,
	})
	if err != nil {
		return feeds.Result{}, err
	}
	if resp.NotModified || resp.Status == http.StatusNotModified {
		return feeds.Result{State: prev, NotModified: true}, nil
	}
	if looksLikeHTML(resp.Body) {
		return feeds.Result{}, &httpx.Error{
			Class: httpx.Blocked, Status: resp.Status, URL: stripQuery(src.URL),
			Err: errors.New("rss: got an HTML page instead of a feed"),
		}
	}
	items, err := parse(resp.Body)
	if err != nil {
		return feeds.Result{}, &httpx.Error{Class: httpx.Permanent, Status: resp.Status, URL: stripQuery(src.URL), Err: err}
	}
	if len(items) > f.cfg.MaxEntries {
		items = items[:f.cfg.MaxEntries]
	}

	now := f.cfg.Now().UTC()
	known := make(map[string]bool, len(prev.Seen))
	for _, id := range prev.Seen {
		known[id] = true
	}
	var articles []model.Article
	for _, it := range items {
		a, ok := f.article(src, it, now)
		if !ok || known[a.ID] {
			continue
		}
		known[a.ID] = true
		articles = append(articles, a)
	}
	slices.SortStableFunc(articles, func(a, b model.Article) int { return b.Published.Compare(a.Published) })

	seen := make([]string, 0, len(articles)+len(prev.Seen))
	for _, a := range articles {
		seen = append(seen, a.ID)
	}
	seen = append(seen, prev.Seen...)
	if len(seen) > f.cfg.SeenCap {
		seen = seen[:f.cfg.SeenCap]
	}
	return feeds.Result{
		Articles: articles,
		State: feeds.State{
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
			Seen:         seen,
			Cursor:       prev.Cursor,
		},
	}, nil
}

// article turns one parsed entry into an Article. It reports false for an
// entry with neither a title nor a link.
func (f *Fetcher) article(src model.Source, it item, now time.Time) (model.Article, bool) {
	link := canonicalURL(it.link, src.URL)
	title := cleanText(it.title)
	if title == "" {
		title = link
	}
	if title == "" {
		return model.Article{}, false
	}
	a := model.Article{
		SourceID: src.ID,
		Domain:   domainOf(link, src.Domain),
		URL:      link,
		Title:    title,
		Summary:  truncate(cleanText(it.summary), summaryRunes),
		Fetched:  now,
	}
	switch {
	case link != "":
		a.ID = hashID(link)
	case strings.TrimSpace(it.guid) != "":
		a.ID = hashID("guid:" + src.ID + "\x00" + strings.TrimSpace(it.guid))
	default:
		a.ID = hashID("title:" + src.ID + "\x00" + title)
	}
	switch t, ok := parseDate(it.date); {
	case !ok:
		a.Published = now
		a.Score.Reasons = append(a.Score.Reasons, "published time missing, fetch time used")
	case t.After(now.Add(futureTolerance)):
		a.Published = now
		a.Score.Reasons = append(a.Score.Reasons, "published time in the future, fetch time used")
	default:
		a.Published = t
	}
	return a, true
}

// item is one feed entry in the common shape of RSS, RDF and Atom.
type item struct {
	guid, title, link, date, summary string
}

type feedDoc struct {
	XMLName xml.Name
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
	Items   []rssItem   `xml:"item"` // RSS 1.0 puts items beside the channel
	Entries []atomEntry `xml:"entry"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Links       []string `xml:"link"` // atom:link siblings also match, with no text
	GUID        string   `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	DCDate      string   `xml:"date"`
	Description string   `xml:"description"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	ID        string     `xml:"id"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

// parse decodes RSS 2.0, RSS 1.0 or Atom into items.
func parse(body []byte) ([]item, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	body = bytes.TrimLeft(body, " \t\r\n")
	body = dropControlBytes(body)
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = CharsetReader
	var doc feedDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("rss: parse feed: %w", err)
	}
	var out []item
	switch doc.XMLName.Local {
	case "rss", "RDF":
		all := append(doc.Channel.Items, doc.Items...)
		for _, r := range all {
			link := ""
			for _, l := range r.Links {
				if l = strings.TrimSpace(l); l != "" {
					link = l
					break
				}
			}
			date := r.PubDate
			if strings.TrimSpace(date) == "" {
				date = r.DCDate
			}
			out = append(out, item{guid: r.GUID, title: r.Title, link: link, date: date, summary: r.Description})
		}
	case "feed":
		for _, e := range doc.Entries {
			date := e.Published
			if strings.TrimSpace(date) == "" {
				date = e.Updated
			}
			summary := e.Summary
			if strings.TrimSpace(summary) == "" {
				summary = e.Content
			}
			out = append(out, item{guid: e.ID, title: e.Title, link: atomHref(e.Links), date: date, summary: summary})
		}
	default:
		return nil, fmt.Errorf("rss: parse feed: root element <%s> is not rss, RDF or feed", doc.XMLName.Local)
	}
	return out, nil
}

// dropControlBytes removes the C0 control bytes XML forbids, so one stray byte
// does not cost the whole feed. It works on bytes because the body may not be UTF-8.
func dropControlBytes(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c >= 0x20 || c == '\t' || c == '\n' || c == '\r' {
			out = append(out, c)
		}
	}
	return out
}

// atomHref picks the entry's own page: the alternate link, else any link.
func atomHref(links []atomLink) string {
	for _, l := range links {
		if (l.Rel == "" || l.Rel == "alternate") && strings.TrimSpace(l.Href) != "" {
			return strings.TrimSpace(l.Href)
		}
	}
	for _, l := range links {
		if strings.TrimSpace(l.Href) != "" {
			return strings.TrimSpace(l.Href)
		}
	}
	return ""
}

// CharsetReader converts a feed declared as ISO-8859-1 or Windows-1252 to
// UTF-8, and other declared character sets where the IANA registry knows
// them. Labels for ISO-8859-1 decode as Windows-1252, a superset that web
// feeds mean in practice. It is meant for xml.Decoder.CharsetReader.
func CharsetReader(label string, input io.Reader) (io.Reader, error) {
	var enc encoding.Encoding
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	case "iso-8859-1", "iso8859-1", "iso_8859-1", "latin1", "l1", "windows-1252", "cp1252":
		enc = charmap.Windows1252
	default:
		var err error
		enc, err = ianaindex.IANA.Encoding(label)
		if err != nil || enc == nil {
			return nil, fmt.Errorf("rss: unsupported character set %q", label)
		}
	}
	return enc.NewDecoder().Reader(input), nil
}

// looksLikeHTML reports whether body starts, after an optional byte order mark
// and whitespace, with an HTML document. A server that answers 200 with an HTML
// page where a feed was expected is blocking or throttling the client.
func looksLikeHTML(body []byte) bool {
	b := bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	b = bytes.TrimLeft(b, " \t\r\n")
	if len(b) > 15 {
		b = b[:15]
	}
	b = bytes.ToLower(b)
	return bytes.HasPrefix(b, []byte("<!doctype html")) || bytes.HasPrefix(b, []byte("<html"))
}

var (
	scriptStyle = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
	markup      = regexp.MustCompile(`(?s)<[a-zA-Z/!][^>]*>`)
)

// cleanText strips markup and entities, removes hostile characters and
// collapses white space to single spaces.
func cleanText(s string) string {
	s = scriptStyle.ReplaceAllString(s, " ")
	s = markup.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(sanitize.Text(s)), " ")
}

// truncate shortens s to at most n runes, ending in an ellipsis when it cuts.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n-1]
	return strings.TrimSpace(string(r)) + "…"
}

// trackingParam reports whether a query parameter only identifies a campaign.
func trackingParam(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case "fbclid", "gclid", "mc_cid", "mc_eid", "igshid":
		return true
	}
	return strings.HasPrefix(name, "utm_")
}

// canonicalURL resolves raw against base, keeps only http and https, lower-cases
// the host, and removes the fragment and tracking parameters. It returns "" for
// anything else.
func canonicalURL(raw, base string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if !u.IsAbs() {
		b, err := url.Parse(base)
		if err != nil {
			return ""
		}
		u = b.ResolveReference(u)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	// Some publishers write a double slash in the path of every link (the ECB
	// does); the page is the same, so one spelling keeps one identity.
	if strings.Contains(u.Path, "//") {
		u.Path = slashRun.ReplaceAllString(u.Path, "/")
		u.RawPath = ""
	}
	if u.RawQuery != "" {
		q := u.Query()
		for name := range q {
			if trackingParam(name) {
				q.Del(name)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String()
}

var slashRun = regexp.MustCompile(`/{2,}`)

// domainOf is the host of link without a leading "www.", or fallback.
func domainOf(link, fallback string) string {
	if u, err := url.Parse(link); err == nil && u.Hostname() != "" {
		return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	}
	return fallback
}

func hashID(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// stripQuery removes the query string and fragment, so tokens never reach an error.
func stripQuery(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

var (
	zoneSuffix = regexp.MustCompile(`\s+([A-Za-z]{1,4})$`)
	zoneOffset = map[string]string{
		"UT": "+0000", "UTC": "+0000", "GMT": "+0000", "Z": "+0000",
		"EST": "-0500", "EDT": "-0400", "CST": "-0600", "CDT": "-0500",
		"MST": "-0700", "MDT": "-0600", "PST": "-0800", "PDT": "-0700",
		"BST": "+0100", "CET": "+0100", "CEST": "+0200",
	}
	dateLayouts = []string{
		time.RFC3339Nano,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 -07:00",
		"Mon, 2 Jan 2006 15:04 -0700",
		"2 Jan 2006 15:04:05 -0700",
		"2 Jan 2006 15:04 -0700",
		"Mon, 2 Jan 06 15:04:05 -0700",
		"2 Jan 06 15:04 -0700",
		"Mon, 2 January 2006 15:04:05 -0700",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"Mon Jan 2 15:04:05 2006",
		"January 2, 2006",
	}
)

// parseDate reads the date formats feeds use (RFC 1123, RFC 822, RFC 3339 and
// common variants), with named US and European zones, and returns UTC. A time
// without a zone is taken as UTC.
func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if m := zoneSuffix.FindStringSubmatchIndex(s); m != nil {
		if off, ok := zoneOffset[strings.ToUpper(s[m[2]:m[3]])]; ok {
			s = s[:m[0]] + " " + off
		}
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// Check reports how many entries body holds if it is an RSS or Atom feed, and
// why it is not one otherwise. It is how a feed found on a web page is
// confirmed before anything is stored for it.
func Check(body []byte) (entries int, err error) {
	if looksLikeHTML(body) {
		return 0, errors.New("rss: an HTML page, not a feed")
	}
	items, err := parse(body)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}
