package rss

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
)

// Synthetic items, each shaped like the quirk its publisher's feed was seen to
// have on 2026-11-04 (docs/research/official-sources.md). The companies,
// titles and addresses are invented.
func TestQuirksOfOfficialFeeds(t *testing.T) {
	tests := []struct {
		name, body string
		wantURL    string
		wantTime   time.Time
		wantSum    string
	}{
		{
			// SEC: a link with trailing whitespace and a UUID guid that is not a URL.
			name: "sec-link-with-newline",
			body: `<rss version="2.0"><channel><title>Press Releases</title><item>
<title>SEC adopts a rule</title><link>https://www.sec.gov/newsroom/press-releases/2026-1
</link><guid isPermaLink="false">0d1c6e0e-7f3a-4a53-8c0e-1f2f2f2f2f2f</guid>
<pubDate>Mon, 02 Nov 2026 11:28:00 -0500</pubDate><description>Lead-in text…</description></item></channel></rss>`,
			wantURL:  "https://www.sec.gov/newsroom/press-releases/2026-1",
			wantTime: time.Date(2026, 11, 2, 16, 28, 0, 0, time.UTC),
			wantSum:  "Lead-in text…",
		},
		{
			// Bank of England: "Z" as the zone and a brace guid with isPermaLink false.
			name: "boe-z-zone",
			body: `<rss version="2.0"><channel><title>News</title><item>
<title>Summary of a forum</title><link>https://www.bankofengland.co.uk/news/2026/november/forum</link>
<description>Short.</description><guid isPermaLink="false">{AAAA-BBBB}</guid>
<pubDate>Mon, 02 Nov 2026 14:51:45 Z</pubDate></item></channel></rss>`,
			wantURL:  "https://www.bankofengland.co.uk/news/2026/november/forum",
			wantTime: time.Date(2026, 11, 2, 14, 51, 45, 0, time.UTC),
			wantSum:  "Short.",
		},
		{
			// ECB: no description at all, and a double slash in the path.
			name: "ecb-no-description",
			body: `<rss version="2.0"><channel><title>ECB</title><item>
<title>Monetary policy decisions</title><link>https://www.ecb.europa.eu//press/pr/date/2026/html/ecb.mp261029~abc.en.html</link>
<guid>https://www.ecb.europa.eu//press/pr/date/2026/html/ecb.mp261029~abc.en.html</guid>
<pubDate>Thu, 29 Oct 2026 14:15:00 +0100</pubDate></item></channel></rss>`,
			wantURL:  "https://www.ecb.europa.eu/press/pr/date/2026/html/ecb.mp261029~abc.en.html",
			wantTime: time.Date(2026, 10, 29, 13, 15, 0, 0, time.UTC),
		},
		{
			// SNB: RSS 2.0 with a Dublin Core date in UTC instead of pubDate.
			name: "snb-dc-date",
			body: `<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>SNB</title><item>
<title>2026-09-24 - Monetary policy assessment of 24 September 2026</title>
<link>https://www.snb.ch/en/publications/mopo/2026-09-24</link><guid>https://www.snb.ch/en/publications/mopo/2026-09-24</guid>
<dc:date>2026-09-24T07:30:00Z</dc:date></item></channel></rss>`,
			wantURL:  "https://www.snb.ch/en/publications/mopo/2026-09-24",
			wantTime: time.Date(2026, 9, 24, 7, 30, 0, 0, time.UTC),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFetcher(&fakeClient{replies: []reply{ok([]byte(tc.body), nil)}}, Config{})
			res, err := f.Fetch(context.Background(), source, feeds.State{})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Articles) != 1 {
				t.Fatalf("%d articles, want 1", len(res.Articles))
			}
			a := res.Articles[0]
			if a.URL != tc.wantURL {
				t.Errorf("URL = %q, want %q", a.URL, tc.wantURL)
			}
			if !a.Published.Equal(tc.wantTime) {
				t.Errorf("Published = %v, want %v", a.Published, tc.wantTime)
			}
			if a.Summary != tc.wantSum {
				t.Errorf("Summary = %q, want %q", a.Summary, tc.wantSum)
			}
		})
	}
}
