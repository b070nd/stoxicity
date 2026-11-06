package stockfeeds

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/b070nd/stoxicity/app/src/feeds/rss"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/store"
)

var (
	linkTag  = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	anchor   = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
	attrPair = regexp.MustCompile(`(?is)([a-z][a-z0-9:_-]*)\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	tagText  = regexp.MustCompile(`(?s)<[^>]*>`)
	newsLike = regexp.MustCompile(`(?i)(investor|newsroom|news-room|press|news|media|ir\.)`)
	noise    = regexp.MustCompile(`(?i)(comment|/tag/|/category/|/author/|search)`)
)

// candidate is a feed address found on a page.
type candidate struct{ url, title string }

// search reads the website of s and returns the feeds on it, each confirmed.
func (k *Keeper) search(ctx context.Context, s Stock) ([]store.StockFeed, error) {
	home := webAddress(s.Website)
	base, _ := url.Parse(home)
	robots := map[string]*robots{}

	page, err := k.page(ctx, robots, home)
	if err != nil {
		return nil, err
	}
	cands := alternates(page, home)
	if len(cands) == 0 {
		for _, next := range newsPages(page, home) {
			if body, err := k.page(ctx, robots, next); err == nil {
				cands = append(cands, alternates(body, next)...)
			}
			if len(cands) > 0 || ctx.Err() != nil {
				break
			}
		}
	}

	var out []store.StockFeed
	tried := map[string]bool{}
	for _, c := range cands {
		if len(out) >= maxFeeds || len(tried) >= maxCandidates || ctx.Err() != nil {
			break
		}
		if tried[c.url] || !sameSite(base.Hostname(), c.url) || noise.MatchString(c.url) {
			continue
		}
		tried[c.url] = true
		body, err := k.page(ctx, robots, c.url)
		if err != nil {
			continue
		}
		if n, err := rss.Check(body); err != nil || n == 0 {
			continue
		}
		title := strings.TrimSpace(c.title)
		if title == "" {
			title = hostOf(c.url) + " news"
		}
		out = append(out, store.StockFeed{Ticker: s.Ticker, Kind: store.FeedSite, URL: c.url, Title: title})
	}
	return out, nil
}

// page reads one address if the robots.txt of its host allows it; the files
// read so far are kept in rules, by host.
func (k *Keeper) page(ctx context.Context, rules map[string]*robots, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	r := rules[u.Host]
	if r == nil {
		r = newRobots(ctx, k.Client, u)
		rules[u.Host] = r
	}
	if !r.allowed(u.EscapedPath()) {
		return nil, errors.New("robots.txt asks robots not to read " + u.EscapedPath())
	}
	resp, err := k.Client.Do(ctx, httpx.Request{
		URL:    raw,
		Header: http.Header{"Accept": {"text/html, application/xhtml+xml, application/rss+xml, application/atom+xml, application/xml;q=0.9, */*;q=0.5"}},
	})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// attrs reads the attributes of a tag.
func attrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range attrPair.FindAllStringSubmatch(tag, -1) {
		out[strings.ToLower(m[1])] = strings.Trim(m[2], `"'`)
	}
	return out
}

// alternates are the feeds a page announces in its <link rel="alternate">.
func alternates(page []byte, base string) []candidate {
	var out []candidate
	if len(page) > 1<<20 {
		page = page[:1<<20]
	}
	for _, tag := range linkTag.FindAllString(string(page), -1) {
		a := attrs(tag)
		typ := strings.ToLower(a["type"])
		if !strings.Contains(strings.ToLower(a["rel"]), "alternate") ||
			(!strings.Contains(typ, "rss") && !strings.Contains(typ, "atom")) || a["href"] == "" {
			continue
		}
		if u := resolve(base, a["href"]); u != "" {
			out = append(out, candidate{url: u, title: a["title"]})
		}
	}
	return out
}

// newsPages are the news, press and investor pages a page links to on the
// same site, the shorter addresses first.
func newsPages(page []byte, base string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range anchor.FindAllStringSubmatch(string(page), 400) {
		a := attrs(m[1])
		text := strings.Join(strings.Fields(tagText.ReplaceAllString(m[2], " ")), " ")
		href := a["href"]
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "mailto:") {
			continue
		}
		if !newsLike.MatchString(href) && !newsLike.MatchString(text) || len(text) > 40 {
			continue
		}
		u := resolve(base, href)
		if u == "" || seen[u] || !sameSite(hostOf(base), u) || noise.MatchString(u) {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	// The home page links the same section many times; the shortest address is
	// its front page.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && len(out[j]) < len(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out[:min(len(out), maxPages)]
}

// resolve makes href an absolute https address on top of base, or "".
func resolve(base, href string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	h, err := url.Parse(strings.TrimSpace(htmlUnescape(href)))
	if err != nil {
		return ""
	}
	u := b.ResolveReference(h)
	if u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" {
		return ""
	}
	u.Scheme, u.Fragment, u.User = "https", "", nil
	return u.String()
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&#38;", "&", "&#x26;", "&").Replace(s)
}

// sameSite reports whether raw is on the company's own site: the same host, or
// one that shares its registered domain (investors.acme.com for www.acme.com).
func sameSite(host, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return registered(strings.ToLower(host)) == registered(strings.ToLower(u.Hostname()))
}

// registered is the domain a host is registered under: its last two labels, or
// three where the second-level label is generic (acme.co.uk).
func registered(host string) string {
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	n := len(labels)
	if n <= 2 {
		return strings.Join(labels, ".")
	}
	switch labels[n-2] {
	case "co", "com", "org", "net", "gov", "ac", "ne":
		if len(labels[n-1]) == 2 {
			return strings.Join(labels[n-3:], ".")
		}
	}
	return strings.Join(labels[n-2:], ".")
}
