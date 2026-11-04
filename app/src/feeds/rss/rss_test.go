package rss

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// reply is one scripted answer of fakeClient.
type reply struct {
	resp *httpx.Response
	err  error
}

// fakeClient answers requests from a script and records them.
type fakeClient struct {
	mu      sync.Mutex
	replies []reply
	reqs    []httpx.Request
}

func (f *fakeClient) Do(_ context.Context, req httpx.Request) (*httpx.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if len(f.replies) == 0 {
		return nil, errors.New("fakeClient: no reply scripted")
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r.resp, r.err
}

func ok(body []byte, h http.Header) reply {
	return reply{resp: &httpx.Response{Status: 200, Header: h, Body: body}}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var (
	fetchTime = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	source    = model.Source{ID: "test-feed", Domain: "fallback.example", Kind: model.KindRSS, URL: "https://www.example.org/feed.xml"}
)

func newFetcher(c httpx.Client, cfg Config) *Fetcher {
	cfg.Now = func() time.Time { return fetchTime }
	return New(c, cfg)
}

func fetchFixture(t *testing.T, name string) feeds.Result {
	t.Helper()
	f := newFetcher(&fakeClient{replies: []reply{ok(fixture(t, name), nil)}}, Config{})
	res, err := f.Fetch(context.Background(), source, feeds.State{})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func TestFormats(t *testing.T) {
	tests := []struct {
		file string
		want []model.Article // compared on the fields below
	}{
		{"rss2.xml", []model.Article{
			{Title: "No date here", URL: "https://www.example.org/news/nodate", Domain: "example.org", Published: fetchTime},
			{Title: "Rates & outlook: hold steady", URL: "https://www.example.org/news/rates?id=7", Domain: "example.org",
				Summary: "The board \"held\" rates.", Published: time.Date(2026, 11, 2, 13, 30, 0, 0, time.UTC)},
			{Title: "Second release", URL: "https://www.example.org/news/second", Domain: "example.org",
				Summary: "Plain text summary.", Published: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)},
		}},
		{"rdf.xml", []model.Article{
			{Title: "RDF item two", URL: "https://example.net/b", Domain: "example.net", Published: time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)},
			{Title: "RDF item one", URL: "https://example.net/a", Domain: "example.net", Summary: "First RDF summary", Published: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)},
		}},
		{"atom.xml", []model.Article{
			{Title: "Company reports results", URL: "https://www.example.com/results", Domain: "example.com",
				Summary: "Quarterly results.", Published: time.Date(2026, 11, 2, 16, 0, 0, 0, time.UTC)},
			{Title: "Entry with content only", URL: "https://www.example.com/content-only", Domain: "example.com",
				Summary: "Body & more", Published: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)},
		}},
		{"latin1.xml", []model.Article{
			{Title: "Café Zürich “quoted”", URL: "https://example.ch/cafe", Domain: "example.ch",
				Summary: "Société générale", Published: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			res := fetchFixture(t, tc.file)
			if len(res.Articles) != len(tc.want) {
				t.Fatalf("got %d articles, want %d: %+v", len(res.Articles), len(tc.want), res.Articles)
			}
			for i, w := range tc.want {
				g := res.Articles[i]
				if g.Title != w.Title || g.URL != w.URL || g.Domain != w.Domain || g.Summary != w.Summary || !g.Published.Equal(w.Published) {
					t.Errorf("article %d:\n got %q %q %q %q %v\nwant %q %q %q %q %v", i,
						g.Title, g.URL, g.Domain, g.Summary, g.Published, w.Title, w.URL, w.Domain, w.Summary, w.Published)
				}
				if g.Published.Location() != time.UTC {
					t.Errorf("article %d: Published is not UTC", i)
				}
				if g.SourceID != source.ID || !g.Fetched.Equal(fetchTime) || len(g.ID) != 32 {
					t.Errorf("article %d: SourceID %q Fetched %v ID %q", i, g.SourceID, g.Fetched, g.ID)
				}
			}
		})
	}
}

func TestMissingDateNoted(t *testing.T) {
	res := fetchFixture(t, "rss2.xml")
	for _, a := range res.Articles {
		noted := len(a.Score.Reasons) == 1
		if (a.Title == "No date here") != noted {
			t.Errorf("%q: reasons %v", a.Title, a.Score.Reasons)
		}
	}
}

func TestFutureDateReplaced(t *testing.T) {
	body := `<rss><channel><item><title>Soon</title><link>https://a.example/x</link><pubDate>Fri, 06 Nov 2026 10:00:00 GMT</pubDate></item></channel></rss>`
	f := newFetcher(&fakeClient{replies: []reply{ok([]byte(body), nil)}}, Config{})
	res, err := f.Fetch(context.Background(), source, feeds.State{})
	if err != nil {
		t.Fatal(err)
	}
	a := res.Articles[0]
	if !a.Published.Equal(fetchTime) || len(a.Score.Reasons) != 1 || !strings.Contains(a.Score.Reasons[0], "future") {
		t.Errorf("got %v %v", a.Published, a.Score.Reasons)
	}
}

func TestParseDate(t *testing.T) {
	want := time.Date(2026, 11, 2, 13, 30, 5, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"Mon, 02 Nov 2026 14:30:05 +0100", want, true},
		{"Mon, 2 Nov 2026 14:30:05 +0100", want, true},
		{"Mon, 02 Nov 2026 13:30:05 GMT", want, true},
		{"Mon, 02 Nov 2026 13:30:05 UT", want, true},
		{"Mon, 02 Nov 2026 08:30:05 EST", want, true},
		{"Mon, 02 Nov 2026 05:30:05 PST", want, true},
		{"Mon, 02 Nov 2026 14:30:05 +01:00", want, true},
		{"02 Nov 2026 14:30:05 +0100", want, true},
		{"Mon, 02 Nov 26 14:30:05 +0100", want, true},
		{"2026-11-02T13:30:05Z", want, true},
		{"2026-11-02T14:30:05+01:00", want, true},
		{"2026-11-02T13:30:05.000Z", want, true},
		{"2026-11-02T13:30:05", want, true},
		{"2026-11-02 13:30:05", want, true},
		{"2026-11-02", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), true},
		{"Mon Nov 2 13:30:05 2026", want, true},
		{"Mon, 02 Nov 2026 13:30 GMT", time.Date(2026, 11, 2, 13, 30, 0, 0, time.UTC), true},
		{"  Mon, 02 Nov 2026 13:30:05 GMT  ", want, true},
		{"", time.Time{}, false},
		{"yesterday", time.Time{}, false},
	}
	for _, tc := range tests {
		got, ok := parseDate(tc.in)
		if ok != tc.ok || (ok && !got.Equal(tc.want)) || (ok && got.Location() != time.UTC) {
			t.Errorf("parseDate(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCanonicalURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://www.example.org/a?utm_source=x&id=1&utm_campaign=y#frag", "https://www.example.org/a?id=1"},
		{"https://www.example.org/a?UTM_Medium=x", "https://www.example.org/a"},
		{"https://www.example.org/a?fbclid=zz&b=2&a=1", "https://www.example.org/a?a=1&b=2"},
		{"HTTPS://WWW.Example.ORG/Path#top", "https://www.example.org/Path"},
		{"/relative/page?utm_term=t", "https://www.example.org/relative/page"},
		{"javascript:alert(1)", ""},
		{"mailto:a@b.c", ""},
		{"  ", ""},
	}
	for _, tc := range tests {
		if got := canonicalURL(tc.in, "https://www.example.org/feed.xml"); got != tc.want {
			t.Errorf("canonicalURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTrackingParamsGiveSameID(t *testing.T) {
	a := hashID(canonicalURL("https://x.example/p?utm_source=a#1", ""))
	b := hashID(canonicalURL("https://x.example/p?utm_source=b", ""))
	if a != b {
		t.Errorf("IDs differ: %s %s", a, b)
	}
}

func TestDedupeViaSeen(t *testing.T) {
	body := fixture(t, "rss2.xml")
	f := newFetcher(&fakeClient{replies: []reply{ok(body, nil), ok(body, nil)}}, Config{})
	first, err := f.Fetch(context.Background(), source, feeds.State{})
	if err != nil || len(first.Articles) != 3 || len(first.State.Seen) != 3 {
		t.Fatalf("first: %v %d %v", err, len(first.Articles), first.State.Seen)
	}
	second, err := f.Fetch(context.Background(), source, first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Articles) != 0 || second.NotModified || len(second.State.Seen) != 3 {
		t.Errorf("second poll returned %d articles, seen %d", len(second.Articles), len(second.State.Seen))
	}

	partial := feeds.State{Seen: []string{first.Articles[0].ID}}
	f = newFetcher(&fakeClient{replies: []reply{ok(body, nil)}}, Config{})
	res, _ := f.Fetch(context.Background(), source, partial)
	if len(res.Articles) != 2 || res.State.Seen[2] != first.Articles[0].ID {
		t.Errorf("partial: %d articles, seen %v", len(res.Articles), res.State.Seen)
	}
}

func TestSeenCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("<rss><channel>")
	for i := range 10 {
		fmt.Fprintf(&b, "<item><title>t%d</title><link>https://a.example/%d</link><pubDate>Mon, 02 Nov 2026 10:%02d:00 GMT</pubDate></item>", i, i, i)
	}
	b.WriteString("</channel></rss>")
	f := newFetcher(&fakeClient{replies: []reply{ok([]byte(b.String()), nil)}}, Config{SeenCap: 6})
	prev := feeds.State{Seen: []string{"old1", "old2", "old3", "old4"}}
	res, err := f.Fetch(context.Background(), source, prev)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Articles) != 10 || len(res.State.Seen) != 6 {
		t.Fatalf("%d articles, %d seen", len(res.Articles), len(res.State.Seen))
	}
	if res.State.Seen[0] != res.Articles[0].ID || res.Articles[0].Title != "t9" {
		t.Errorf("newest article is not first in Seen")
	}
	for _, id := range res.State.Seen {
		if strings.HasPrefix(id, "old") {
			t.Errorf("old ID %q survived the cap", id)
		}
	}
}

func TestEntryCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("<rss><channel>")
	for i := range 620 {
		fmt.Fprintf(&b, "<item><title>t%d</title><link>https://a.example/%d</link></item>", i, i)
	}
	b.WriteString("</channel></rss>")
	f := newFetcher(&fakeClient{replies: []reply{ok([]byte(b.String()), nil)}}, Config{})
	res, err := f.Fetch(context.Background(), source, feeds.State{})
	if err != nil || len(res.Articles) != 500 {
		t.Fatalf("got %d articles, err %v", len(res.Articles), err)
	}
}

func TestConditionalGet(t *testing.T) {
	h := http.Header{"Etag": {`"v2"`}, "Last-Modified": {"Tue, 03 Nov 2026 10:00:00 GMT"}}
	c := &fakeClient{replies: []reply{
		ok(fixture(t, "atom.xml"), h),
		{resp: &httpx.Response{Status: 304, NotModified: true}},
	}}
	f := newFetcher(c, Config{})
	first, err := f.Fetch(context.Background(), source, feeds.State{Cursor: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	if first.State.ETag != `"v2"` || first.State.LastModified != "Tue, 03 Nov 2026 10:00:00 GMT" || first.State.Cursor != "keep" {
		t.Errorf("state %+v", first.State)
	}
	second, err := f.Fetch(context.Background(), source, first.State)
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotModified || len(second.Articles) != 0 || second.State.ETag != `"v2"` || len(second.State.Seen) != 2 {
		t.Errorf("304 result %+v", second)
	}
	if c.reqs[0].ETag != "" || c.reqs[1].ETag != `"v2"` || c.reqs[1].LastModified != "Tue, 03 Nov 2026 10:00:00 GMT" {
		t.Errorf("validators sent: %+v", c.reqs)
	}
}

func TestHTMLIsBlocked(t *testing.T) {
	for _, body := range []string{string(fixture(t, "blocked.html")), "\xef\xbb\xbf  \n<HTML><body>x"} {
		f := newFetcher(&fakeClient{replies: []reply{ok([]byte(body), nil)}}, Config{})
		_, err := f.Fetch(context.Background(), source, feeds.State{})
		var he *httpx.Error
		if !errors.As(err, &he) || he.Class != httpx.Blocked {
			t.Errorf("err = %v, want a Blocked *httpx.Error", err)
		}
	}
}

func TestUnusableBodyIsPermanent(t *testing.T) {
	for _, body := range []string{"not xml at all", "<svg/>", `<?xml version="1.0"?><svg/>`} {
		f := newFetcher(&fakeClient{replies: []reply{ok([]byte(body), nil)}}, Config{})
		_, err := f.Fetch(context.Background(), source, feeds.State{})
		var he *httpx.Error
		if !errors.As(err, &he) || he.Class != httpx.Permanent || !strings.Contains(err.Error(), "rss:") {
			t.Errorf("%q: err = %v, want a Permanent rss error", body, err)
		}
	}
}

func TestEmptyFeed(t *testing.T) {
	res := fetchFixture(t, "empty.xml")
	if len(res.Articles) != 0 || res.NotModified {
		t.Errorf("got %+v", res)
	}
}

func TestClientErrorPassesThrough(t *testing.T) {
	want := &httpx.Error{Class: httpx.Throttled, Status: 429, URL: "https://www.example.org/feed.xml", RetryAfter: time.Minute}
	f := newFetcher(&fakeClient{replies: []reply{{err: want}}}, Config{})
	_, err := f.Fetch(context.Background(), source, feeds.State{})
	if err != error(want) {
		t.Errorf("err = %v, want the client's error unchanged", err)
	}
}

func TestHostileTextIsCleaned(t *testing.T) {
	body := "<rss><channel><item><title>Buy\u202e now\x1b red\u200b</title><link>https://a.example/h</link>" +
		"<description>&lt;style&gt;p{}&lt;/style&gt;  a \t b\n\n c &amp;amp; d</description></item></channel></rss>"
	f := newFetcher(&fakeClient{replies: []reply{ok([]byte(body), nil)}}, Config{})
	res, err := f.Fetch(context.Background(), source, feeds.State{})
	if err != nil {
		t.Fatal(err)
	}
	a := res.Articles[0]
	if a.Title != "Buy now red" || a.Summary != "a b c & d" {
		t.Errorf("title %q summary %q", a.Title, a.Summary)
	}
}

func TestSummaryTruncated(t *testing.T) {
	body := "<rss><channel><item><title>t</title><link>https://a.example/l</link><description>" + strings.Repeat("word ", 400) + "</description></item></channel></rss>"
	f := newFetcher(&fakeClient{replies: []reply{ok([]byte(body), nil)}}, Config{})
	res, _ := f.Fetch(context.Background(), source, feeds.State{})
	s := res.Articles[0].Summary
	if n := len([]rune(s)); n > summaryRunes || !strings.HasSuffix(s, "…") {
		t.Errorf("summary has %d runes, ends %q", n, s[len(s)-6:])
	}
}

func TestEntryWithoutLinkUsesGUID(t *testing.T) {
	body := `<rss><channel><item><title>A</title><guid>g-1</guid></item><item><title>A</title><guid>g-2</guid></item><item/></channel></rss>`
	f := newFetcher(&fakeClient{replies: []reply{ok([]byte(body), nil)}}, Config{})
	res, err := f.Fetch(context.Background(), source, feeds.State{})
	if err != nil || len(res.Articles) != 2 || res.Articles[0].ID == res.Articles[1].ID {
		t.Fatalf("articles %+v err %v", res.Articles, err)
	}
	if res.Articles[0].Domain != "fallback.example" || res.Articles[0].URL != "" {
		t.Errorf("domain %q url %q", res.Articles[0].Domain, res.Articles[0].URL)
	}
}

func FuzzParse(f *testing.F) {
	for _, n := range []string{"rss2.xml", "rdf.xml", "atom.xml", "latin1.xml", "empty.xml"} {
		b, err := os.ReadFile("testdata/" + n)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = parse(b) })
}
