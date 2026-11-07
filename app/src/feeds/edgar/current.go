package edgar

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/feeds/rss"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// pageSize is the number of entries asked for per page of the current feed.
const pageSize = 100

// Current reads the current-filings Atom feed for each form listed on the
// source. The feed sends no ETag or Last-Modified, so it never sends
// conditional headers; it is read from the first page (start=0) onward until
// an accession number in State.Seen appears, or MaxPages pages were read.
// Entries are decoded from ISO-8859-1; the form, company name and CIK come
// from the title; the accession number comes from the summary. A page that
// hits the page limit is not reported to the scheduler (Result has no field
// for it); the entries beyond it are lost.
type Current struct {
	client httpx.Client
	cfg    Config
}

// NewCurrent returns a Current reader that makes all requests through client.
func NewCurrent(client httpx.Client, cfg Config) *Current {
	return &Current{client: client, cfg: cfg.withDefaults()}
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title   string `xml:"title"`
	Summary string `xml:"summary"`
	ID      string `xml:"id"`
	Updated string `xml:"updated"`
	Link    struct {
		Href string `xml:"href,attr"`
	} `xml:"link"`
}

// filing is one parsed entry of the current feed.
type filing struct {
	acc     Accession
	form    string
	company string
	cik     string // ten digits
	role    string // Filer, Issuer, Reporting, Subject
	updated string
	filed   string // the "Filed:" date of the summary
	summary string // cleaned summary text
}

var (
	titlePattern = regexp.MustCompile(`^(.+?) - (.+) \((\d{1,10})\) \(([^)]*)\)$`)
	accPattern   = regexp.MustCompile(`\d{10}-\d{2}-\d{6}`)
	filedPattern = regexp.MustCompile(`Filed:\s*(\d{4}-\d{2}-\d{2})`)
	sizePattern  = regexp.MustCompile(`Size:\s*[\d.,]+\s*[KMGkmg]?[Bb]`)
)

// rolePriority orders the entries one filing has: Form 4 lists the issuer and
// each reporting person, and the issuer is the company the news is about.
func rolePriority(role string) int {
	switch strings.ToLower(role) {
	case "issuer", "subject":
		return 4
	case "filer":
		return 3
	case "reporting":
		return 1
	}
	return 2
}

// Fetch implements feeds.Fetcher.
func (c *Current) Fetch(ctx context.Context, src model.Source, prev feeds.State) (feeds.Result, error) {
	forms := src.Forms
	if len(forms) == 0 {
		forms = []string{""}
	}
	known := make(map[string]bool, len(prev.Seen))
	for _, id := range prev.Seen {
		known[id] = true
	}

	var (
		order    []Accession
		best     = map[Accession]filing{}
		firstErr error
		finished int
	)
	for _, form := range forms {
		found, err := c.readForm(ctx, form, src.Forms, known)
		if err != nil {
			if mustStop(err) {
				return feeds.Result{}, err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		finished++
		for _, fl := range found {
			cur, dup := best[fl.acc]
			if !dup {
				order = append(order, fl.acc)
			}
			if !dup || rolePriority(fl.role) > rolePriority(cur.role) {
				best[fl.acc] = fl
			}
		}
	}
	if finished == 0 {
		return feeds.Result{}, firstErr
	}

	now := c.cfg.Now().UTC()
	articles := make([]model.Article, 0, len(order))
	for _, acc := range order {
		articles = append(articles, c.article(src, best[acc], now))
	}
	slices.SortStableFunc(articles, func(a, b model.Article) int { return b.Published.Compare(a.Published) })
	ids := make([]string, len(articles))
	for i, a := range articles {
		ids[i] = a.ID
	}
	return feeds.Result{
		Articles: articles,
		State:    feeds.State{Seen: mergeSeen(ids, prev.Seen, c.cfg.SeenCap), Cursor: prev.Cursor},
	}, nil
}

// readForm reads pages of the feed for one form type until a known accession,
// a short page or the page limit. wanted, when not empty, is the list of forms
// to keep.
func (c *Current) readForm(ctx context.Context, form string, wanted []string, known map[string]bool) ([]filing, error) {
	var out []filing
	for page := range c.cfg.MaxPages {
		endpoint := c.pageURL(form, page*pageSize)
		resp, err := c.client.Do(ctx, httpx.Request{
			URL:    endpoint,
			Header: http.Header{"Accept": {"application/atom+xml, application/xml;q=0.9, */*;q=0.5"}},
		})
		if err != nil {
			return nil, err
		}
		if looksLikeHTML(resp.Body) {
			return nil, blockedError(endpoint, resp.Status)
		}
		entries, err := parseCurrent(resp.Body)
		if err != nil {
			return nil, permanentError(endpoint, resp.Status, err)
		}
		reachedKnown := false
		for _, fl := range entries {
			if known[string(fl.acc)] {
				reachedKnown = true
				break
			}
			if keepForm(fl.form, wanted) {
				out = append(out, fl)
			}
		}
		if reachedKnown || len(entries) < pageSize {
			break
		}
	}
	return out, nil
}

// pageURL is the current-filings feed for one form, starting at entry start.
func (c *Current) pageURL(form string, start int) string {
	q := url.Values{}
	q.Set("action", "getcurrent")
	q.Set("type", form)
	q.Set("company", "")
	q.Set("dateb", "")
	q.Set("owner", "include")
	q.Set("start", strconv.Itoa(start))
	q.Set("count", strconv.Itoa(pageSize))
	q.Set("output", "atom")
	return c.cfg.WebBase + "/cgi-bin/browse-edgar?" + q.Encode()
}

// keepForm reports whether a filing of the given form is wanted. The feed's
// type filter matches by prefix, so "4" also returns "424B2"; an amendment
// such as "8-K/A" counts as its base form.
func keepForm(form string, wanted []string) bool {
	if len(wanted) == 0 {
		return true
	}
	form = strings.ToUpper(form)
	for _, w := range wanted {
		w = strings.ToUpper(strings.TrimSpace(w))
		if form == w || form == w+"/A" {
			return true
		}
	}
	return false
}

// parseCurrent reads the Atom feed. Entries whose title or accession number
// cannot be read are skipped, but a feed whose every entry is unreadable is an
// error: the format has changed.
func parseCurrent(body []byte) ([]filing, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	dec := xml.NewDecoder(bytes.NewReader(bytes.TrimLeft(body, " \t\r\n")))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = rss.CharsetReader
	var doc struct {
		XMLName xml.Name
		atomFeed
	}
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("edgar: parse current feed: %w", err)
	}
	if doc.XMLName.Local != "feed" {
		return nil, fmt.Errorf("edgar: parse current feed: root element <%s> is not feed", doc.XMLName.Local)
	}
	var out []filing
	for _, e := range doc.Entries {
		if fl, ok := parseEntry(e); ok {
			out = append(out, fl)
		}
	}
	if len(doc.Entries) > 0 && len(out) == 0 {
		return nil, fmt.Errorf("edgar: parse current feed: none of %d entries has a readable title and accession number", len(doc.Entries))
	}
	return out, nil
}

func parseEntry(e atomEntry) (filing, bool) {
	m := titlePattern.FindStringSubmatch(cleanText(e.Title))
	if m == nil {
		return filing{}, false
	}
	summary := cleanText(e.Summary)
	var acc Accession
	for _, text := range []string{summary, e.ID, e.Link.Href} {
		if raw := accPattern.FindString(text); raw != "" {
			if a, err := ParseAccession(raw); err == nil {
				acc = a
				break
			}
		}
	}
	if acc == "" {
		return filing{}, false
	}
	fl := filing{
		acc: acc, form: m[1], company: m[2], cik: padCIK(m[3]), role: m[4],
		updated: e.Updated, summary: summary,
	}
	if d := filedPattern.FindStringSubmatch(summary); d != nil {
		fl.filed = d[1]
	}
	return fl, fl.cik != ""
}

// summaryItems takes 8-K item numbers from a summary that carries them, such as
// "Items: 2.02, 9.01". The size is removed first because a number like
// "10.25 MB" looks like an item.
func summaryItems(form, summary string) []string {
	if !strings.HasPrefix(strings.ToUpper(form), "8-K") {
		return nil
	}
	summary = sizePattern.ReplaceAllString(summary, " ")
	i := strings.Index(strings.ToLower(summary), "item")
	if i < 0 {
		return nil
	}
	return ParseItems(summary[i:])
}

func (c *Current) article(src model.Source, fl filing, now time.Time) model.Article {
	t, known := parseTime(fl.updated)
	if !known {
		t, known = parseTime(fl.filed)
	}
	pub, reasons := published(t, known, now)
	a := model.Article{
		ID:        string(fl.acc),
		SourceID:  src.ID,
		Domain:    domain,
		URL:       FilingIndexURL(fl.cik, fl.acc),
		Title:     fl.form + " - " + fl.company,
		Summary:   fmt.Sprintf("Form %s filed by %s (CIK %s), accession %s", fl.form, fl.company, fl.cik, fl.acc),
		Published: pub,
		Fetched:   now,
		Form:      fl.form,
		CIK:       fl.cik,
		Items:     summaryItems(fl.form, fl.summary),
		Tickers:   slices.Clone(c.cfg.Tickers[fl.cik]),
	}
	a.Score.Reasons = reasons
	return a
}
