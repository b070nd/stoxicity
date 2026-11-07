package edgar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// fakeClient answers requests with fn and records them.
type fakeClient struct {
	mu   sync.Mutex
	fn   func(req httpx.Request) (*httpx.Response, error)
	reqs []httpx.Request
}

func (f *fakeClient) Do(_ context.Context, req httpx.Request) (*httpx.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	return f.fn(req)
}

// query returns a parameter of the i-th recorded request.
func (f *fakeClient) query(t *testing.T, i int, key string) string {
	t.Helper()
	u, err := url.Parse(f.reqs[i].URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}

func serve(body []byte) func(httpx.Request) (*httpx.Response, error) {
	return func(httpx.Request) (*httpx.Response, error) {
		return &httpx.Response{Status: 200, Header: http.Header{}, Body: body}, nil
	}
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
	fetchTime  = time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC)
	currentSrc = model.Source{ID: "sec-edgar", Kind: model.KindFilings, Paged: true,
		URL: "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&output=atom"}
)

func testConfig() Config {
	return Config{WebBase: "http://web.test", DataBase: "http://data.test", Now: func() time.Time { return fetchTime }}
}

func acc(n int) string { return fmt.Sprintf("0001000000-26-%06d", n) }

// atomPage builds a current-feed page of form entries; entry i has accession acc(i).
func atomPage(form string, ids []int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="ISO-8859-1" ?><feed xmlns="http://www.w3.org/2005/Atom"><title>t</title>`)
	if form == "" {
		form = "8-K"
	}
	base := time.Date(2026, 11, 4, 5, 0, 0, 0, time.FixedZone("EST", -5*3600))
	for _, i := range ids {
		fmt.Fprintf(&b, `<entry><title>%s - COMPANY %d (%010d) (Filer)</title><summary type="html">&lt;b&gt;Filed:&lt;/b&gt; 2026-11-04 &lt;b&gt;AccNo:&lt;/b&gt; %s &lt;b&gt;Size:&lt;/b&gt; 1 KB</summary><updated>%s</updated></entry>`,
			form, i, 5000+i, acc(i), base.Add(time.Duration(i)*time.Second).Format(time.RFC3339))
	}
	b.WriteString(`</feed>`)
	return []byte(b.String())
}

// pagedFeed serves total entries, newest (highest number) first, the way the
// SEC does: start and count select the window.
func pagedFeed(total int) func(httpx.Request) (*httpx.Response, error) {
	return func(req httpx.Request) (*httpx.Response, error) {
		u, _ := url.Parse(req.URL)
		start, _ := strconv.Atoi(u.Query().Get("start"))
		count, _ := strconv.Atoi(u.Query().Get("count"))
		var ids []int
		for p := start; p < start+count && p < total; p++ {
			ids = append(ids, total-1-p)
		}
		return &httpx.Response{Status: 200, Body: atomPage(u.Query().Get("type"), ids)}, nil
	}
}

func TestCurrentParse(t *testing.T) {
	cfg := testConfig()
	cfg.Tickers = map[string][]string{"0000320193": {"AAPL"}, "0000732717": {"T"}}
	c := &fakeClient{fn: serve(fixture(t, "current_8k.xml"))}
	res, err := NewCurrent(c, cfg).Fetch(context.Background(), currentSrc, feeds.State{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Articles) != 3 {
		t.Fatalf("got %d articles", len(res.Articles))
	}
	apple, att, nestle := res.Articles[0], res.Articles[1], res.Articles[2]

	if apple.ID != "0000320193-26-000026" || apple.Form != "8-K" || apple.Title != "8-K - Apple Inc." ||
		apple.URL != FilingIndexURL("0000320193", "0000320193-26-000026") || apple.Domain != "sec.gov" || apple.SourceID != "sec-edgar" {
		t.Errorf("apple: %+v", apple)
	}
	if want := time.Date(2026, 11, 4, 15, 58, 12, 0, time.UTC); !apple.Published.Equal(want) || apple.Published.Location() != time.UTC {
		t.Errorf("published %v, want %v in UTC", apple.Published, want)
	}
	if !slices.Equal(apple.Items, []string{"2.02", "9.01"}) {
		t.Errorf("items %v (the size 10.25 MB must not count)", apple.Items)
	}
	if !slices.Equal(apple.Tickers, []string{"AAPL"}) || !slices.Equal(att.Tickers, []string{"T"}) || nestle.Tickers != nil {
		t.Errorf("tickers %v %v %v", apple.Tickers, att.Tickers, nestle.Tickers)
	}
	if att.Title != "8-K - AT&T INC." || att.Items != nil {
		t.Errorf("entity decoding: %q items %v", att.Title, att.Items)
	}
	if nestle.Title != "8-K - NESTLÉ HOLDINGS & CO" {
		t.Errorf("ISO-8859-1 / double entity: %q", nestle.Title)
	}
	if !apple.Fetched.Equal(fetchTime) {
		t.Errorf("fetched %v", apple.Fetched)
	}
	if got := res.State.Seen; len(got) != 3 || got[0] != apple.ID {
		t.Errorf("seen %v", got)
	}
}

func TestCurrentWithoutTickerMapLeavesTickersEmpty(t *testing.T) {
	c := &fakeClient{fn: serve(fixture(t, "current_8k.xml"))}
	res, _ := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, feeds.State{})
	for _, a := range res.Articles {
		if a.Tickers != nil {
			t.Errorf("%s has tickers %v", a.ID, a.Tickers)
		}
	}
}

func TestCurrentNeverSendsValidators(t *testing.T) {
	c := &fakeClient{fn: serve(fixture(t, "current_8k.xml"))}
	prev := feeds.State{ETag: `"stale"`, LastModified: "yesterday", Seen: []string{"0000000000-26-000001"}}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, prev)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range c.reqs {
		if r.ETag != "" || r.LastModified != "" || r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
			t.Errorf("conditional headers sent: %+v", r)
		}
	}
	if res.State.ETag != "" || res.State.LastModified != "" || res.NotModified {
		t.Errorf("state %+v", res.State)
	}
	if !strings.HasPrefix(c.reqs[0].URL, "http://web.test/cgi-bin/browse-edgar?") ||
		c.query(t, 0, "action") != "getcurrent" || c.query(t, 0, "output") != "atom" ||
		c.query(t, 0, "start") != "0" || c.query(t, 0, "count") != "100" {
		t.Errorf("url %s", c.reqs[0].URL)
	}
}

func TestCurrentPagingStopsAtKnownAccession(t *testing.T) {
	// 250 entries, newest first; position 130 is already known.
	c := &fakeClient{fn: pagedFeed(250)}
	src := currentSrc
	src.Forms = []string{"8-K"}
	prev := feeds.State{Seen: []string{acc(249 - 130), acc(3)}}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), src, prev)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Articles) != 130 {
		t.Errorf("got %d articles, want 130", len(res.Articles))
	}
	if len(c.reqs) != 2 || c.query(t, 0, "start") != "0" || c.query(t, 1, "start") != "100" || c.query(t, 0, "type") != "8-K" {
		t.Errorf("requests: %d", len(c.reqs))
	}
	if len(res.State.Seen) != 132 || res.State.Seen[0] != acc(249) || res.State.Seen[130] != acc(249-130) {
		t.Errorf("seen: len %d first %s", len(res.State.Seen), res.State.Seen[0])
	}
}

func TestCurrentKnownFirstEntryEndsReadAtOnce(t *testing.T) {
	c := &fakeClient{fn: pagedFeed(250)}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, feeds.State{Seen: []string{acc(249)}})
	if err != nil || len(res.Articles) != 0 || len(c.reqs) != 1 || res.NotModified {
		t.Errorf("err %v, %d articles, %d requests", err, len(res.Articles), len(c.reqs))
	}
}

func TestCurrentStopsAtPageLimit(t *testing.T) {
	c := &fakeClient{fn: pagedFeed(2000)}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, feeds.State{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.reqs) != DefaultMaxPages || len(res.Articles) != 100*DefaultMaxPages {
		t.Errorf("%d requests, %d articles", len(c.reqs), len(res.Articles))
	}
	if !strings.Contains(res.Partial, "older filings are not read") {
		t.Errorf("Partial = %q: a backlog the page limit cut off must be reported", res.Partial)
	}
	if c.query(t, DefaultMaxPages-1, "start") != "400" {
		t.Errorf("last start %s", c.query(t, DefaultMaxPages-1, "start"))
	}

	cfg := testConfig()
	cfg.MaxPages = 2
	c = &fakeClient{fn: pagedFeed(2000)}
	if _, err := NewCurrent(c, cfg).Fetch(context.Background(), currentSrc, feeds.State{}); err != nil || len(c.reqs) != 2 {
		t.Errorf("MaxPages 2: err %v, %d requests", err, len(c.reqs))
	}
}

// A page of 100 entries one of which cannot be read is still a full page: the
// read must go on to the next.
func TestCurrentUnreadableEntryDoesNotMakeAPageShort(t *testing.T) {
	c := &fakeClient{fn: func(req httpx.Request) (*httpx.Response, error) {
		u, _ := url.Parse(req.URL)
		if u.Query().Get("start") != "0" {
			return &httpx.Response{Status: 200, Body: atomPage("8-K", []int{1})}, nil
		}
		page := atomPage("8-K", seq(2, 100)) // 99 readable entries
		page = bytes.Replace(page, []byte("</feed>"), []byte("<entry><title>junk</title></entry></feed>"), 1)
		return &httpx.Response{Status: 200, Body: page}, nil
	}}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, feeds.State{})
	if err != nil || len(c.reqs) != 2 || len(res.Articles) != 100 {
		t.Errorf("err %v, %d requests, %d articles; want the second page read", err, len(c.reqs), len(res.Articles))
	}
}

func seq(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func TestCurrentShortPageEndsRead(t *testing.T) {
	c := &fakeClient{fn: pagedFeed(40)}
	res, _ := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, feeds.State{})
	if len(c.reqs) != 1 || len(res.Articles) != 40 {
		t.Errorf("%d requests, %d articles", len(c.reqs), len(res.Articles))
	}
}

func TestCurrentSeenCap(t *testing.T) {
	cfg := testConfig()
	cfg.SeenCap = 50
	c := &fakeClient{fn: pagedFeed(80)}
	res, _ := NewCurrent(c, cfg).Fetch(context.Background(), currentSrc, feeds.State{Seen: []string{"old"}})
	if len(res.Articles) != 80 || len(res.State.Seen) != 50 || res.State.Seen[0] != acc(79) || slices.Contains(res.State.Seen, "old") {
		t.Errorf("%d articles, seen %d", len(res.Articles), len(res.State.Seen))
	}
}

func TestCurrentFormFilter(t *testing.T) {
	body := []byte(`<feed xmlns="http://www.w3.org/2005/Atom">` +
		entry("8-K", "ONE", 1, 1) + entry("424B2", "TWO", 2, 2) + entry("8-K/A", "THREE", 3, 3) + entry("8-KT", "FOUR", 4, 4) + entry("4", "FIVE", 5, 5) +
		`</feed>`)
	c := &fakeClient{fn: serve(body)}
	src := currentSrc
	src.Forms = []string{"8-K"}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), src, feeds.State{})
	if err != nil {
		t.Fatal(err)
	}
	var forms []string
	for _, a := range res.Articles {
		forms = append(forms, a.Form)
	}
	if !slices.Equal(forms, []string{"8-K", "8-K/A"}) && !slices.Equal(forms, []string{"8-K/A", "8-K"}) {
		t.Errorf("forms kept: %v", forms)
	}
}

func entry(form, company string, cik, n int) string {
	return fmt.Sprintf(`<entry><title>%s - %s (%010d) (Filer)</title><summary>AccNo: %s</summary><updated>2026-11-04T10:00:%02d-05:00</updated></entry>`,
		form, company, cik, acc(n), n)
}

func TestCurrentOneRequestSeriesPerForm(t *testing.T) {
	c := &fakeClient{fn: func(req httpx.Request) (*httpx.Response, error) {
		u, _ := url.Parse(req.URL)
		form := u.Query().Get("type")
		n := map[string]int{"8-K": 1, "10-K": 2, "4": 3}[form]
		return &httpx.Response{Status: 200, Body: []byte(`<feed xmlns="http://www.w3.org/2005/Atom">` + entry(form, "CO", n, n) + `</feed>`)}, nil
	}}
	src := currentSrc
	src.Forms = []string{"8-K", "10-K", "4"}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), src, feeds.State{})
	if err != nil || len(res.Articles) != 3 || len(c.reqs) != 3 {
		t.Fatalf("err %v, %d articles, %d requests", err, len(res.Articles), len(c.reqs))
	}
	for i, want := range src.Forms {
		if got := c.query(t, i, "type"); got != want {
			t.Errorf("request %d type %q, want %q", i, got, want)
		}
	}
}

func TestCurrentBlockedPage(t *testing.T) {
	for _, body := range [][]byte{fixture(t, "rate_limit.html"), []byte("\xef\xbb\xbf\n <!DOCTYPE HTML PUBLIC><html>")} {
		c := &fakeClient{fn: serve(body)}
		_, err := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, feeds.State{})
		var he *httpx.Error
		if !errors.As(err, &he) || he.Class != httpx.Blocked || strings.Contains(he.URL, "?") {
			t.Errorf("err = %v, want a Blocked *httpx.Error without a query", err)
		}
	}
}

func TestCurrentEmptyFeed(t *testing.T) {
	prev := feeds.State{Seen: []string{acc(1)}, Cursor: "c"}
	c := &fakeClient{fn: serve(fixture(t, "current_empty.xml"))}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, prev)
	if err != nil || len(res.Articles) != 0 || res.NotModified {
		t.Fatalf("err %v result %+v", err, res)
	}
	if !slices.Equal(res.State.Seen, prev.Seen) || res.State.Cursor != "c" {
		t.Errorf("state changed: %+v", res.State)
	}
}

func TestCurrentUnreadableFeed(t *testing.T) {
	for name, body := range map[string]string{
		"not xml":         "hello",
		"wrong root":      "<rss><channel/></rss>",
		"unknown entries": `<feed><entry><title>who knows</title></entry></feed>`,
		"no accession":    `<feed><entry><title>8-K - CO (0000000001) (Filer)</title><summary>nothing</summary></entry></feed>`,
	} {
		c := &fakeClient{fn: serve([]byte(body))}
		_, err := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, feeds.State{})
		var he *httpx.Error
		if !errors.As(err, &he) || he.Class != httpx.Permanent || !strings.Contains(err.Error(), "edgar:") {
			t.Errorf("%s: err = %v, want a Permanent edgar error", name, err)
		}
	}
}

func TestCurrentSkipsUnreadableEntries(t *testing.T) {
	body := `<feed>` + entry("8-K", "GOOD", 1, 1) + `<entry><title>junk</title></entry></feed>`
	c := &fakeClient{fn: serve([]byte(body))}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, feeds.State{})
	if err != nil || len(res.Articles) != 1 {
		t.Errorf("err %v, %d articles", err, len(res.Articles))
	}
}

func TestCurrentErrorHandling(t *testing.T) {
	boom := &httpx.Error{Class: httpx.Retryable, URL: "http://web.test/x", Err: errors.New("reset")}
	throttled := &httpx.Error{Class: httpx.Throttled, Status: 429, URL: "http://web.test/x", RetryAfter: time.Minute}
	src := currentSrc
	src.Forms = []string{"8-K", "10-K"}
	twoForms := func(second error) *fakeClient {
		return &fakeClient{fn: func(req httpx.Request) (*httpx.Response, error) {
			if strings.Contains(req.URL, "type=10-K") {
				return nil, second
			}
			return &httpx.Response{Status: 200, Body: []byte(`<feed>` + entry("8-K", "CO", 1, 1) + `</feed>`)}, nil
		}}
	}

	t.Run("other form still delivers", func(t *testing.T) {
		res, err := NewCurrent(twoForms(boom), testConfig()).Fetch(context.Background(), src, feeds.State{})
		if err != nil || len(res.Articles) != 1 {
			t.Errorf("err %v, %d articles", err, len(res.Articles))
		}
		if !strings.Contains(res.Partial, "10-K") {
			t.Errorf("Partial = %q: the failed form must be reported", res.Partial)
		}
	})
	t.Run("throttle is not hidden", func(t *testing.T) {
		_, err := NewCurrent(twoForms(throttled), testConfig()).Fetch(context.Background(), src, feeds.State{})
		if err != error(throttled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("all failing returns the first error unchanged", func(t *testing.T) {
		c := &fakeClient{fn: func(httpx.Request) (*httpx.Response, error) { return nil, boom }}
		_, err := NewCurrent(c, testConfig()).Fetch(context.Background(), src, feeds.State{})
		if err != error(boom) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestCurrentForm4MergesReportingAndIssuer(t *testing.T) {
	cfg := testConfig()
	cfg.Tickers = map[string][]string{"0000320193": {"AAPL"}}
	c := &fakeClient{fn: serve(fixture(t, "current_form4.xml"))}
	src := currentSrc
	src.Forms = []string{"10-K"} // a Form 4 source ignores the forms it is given
	res, err := NewForm4(c, cfg).Fetch(context.Background(), src, feeds.State{})
	if err != nil || len(res.Articles) != 1 {
		t.Fatalf("err %v, %d articles", err, len(res.Articles))
	}
	a := res.Articles[0]
	if a.Form != "4" || a.Title != "4 - Apple Inc." || !slices.Equal(a.Tickers, []string{"AAPL"}) ||
		a.URL != FilingIndexURL("0000320193", "0000320193-26-000030") {
		t.Errorf("article %+v", a)
	}
	if c.query(t, 0, "type") != "4" {
		t.Errorf("type %q", c.query(t, 0, "type"))
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2026-11-04T10:58:12-05:00", time.Date(2026, 11, 4, 15, 58, 12, 0, time.UTC)},
		{"2026-11-04T15:58:12Z", time.Date(2026, 11, 4, 15, 58, 12, 0, time.UTC)},
		{"2026-11-04T15:58:12.000Z", time.Date(2026, 11, 4, 15, 58, 12, 0, time.UTC)},
		{"2026-11-04T10:58:12", time.Date(2026, 11, 4, 15, 58, 12, 0, time.UTC)}, // no zone: US Eastern, EST in November
		{"2026-07-04T10:58:12", time.Date(2026, 7, 4, 14, 58, 12, 0, time.UTC)},  // EDT in July
	}
	for _, tc := range tests {
		got, ok := parseTime(tc.in)
		if !ok || !got.Equal(tc.want) || got.Location() != time.UTC {
			t.Errorf("parseTime(%q) = %v, %v; want %v", tc.in, got, ok, tc.want)
		}
	}
	if _, ok := parseTime("soon"); ok {
		t.Error("parsed garbage")
	}
}

func TestCurrentMissingAndFutureTimes(t *testing.T) {
	body := `<feed><entry><title>8-K - A (0000000001) (Filer)</title><summary>AccNo: ` + acc(1) + `</summary></entry>` +
		`<entry><title>8-K - B (0000000002) (Filer)</title><summary>AccNo: ` + acc(2) + `</summary><updated>2026-11-06T10:00:00-05:00</updated></entry>` +
		`<entry><title>8-K - C (0000000003) (Filer)</title><summary>Filed: 2026-11-03 AccNo: ` + acc(3) + `</summary></entry></feed>`
	c := &fakeClient{fn: serve([]byte(body))}
	res, err := NewCurrent(c, testConfig()).Fetch(context.Background(), currentSrc, feeds.State{})
	if err != nil || len(res.Articles) != 3 {
		t.Fatalf("err %v, %d articles", err, len(res.Articles))
	}
	for _, a := range res.Articles {
		switch a.ID {
		case acc(1), acc(2):
			if !a.Published.Equal(fetchTime) || len(a.Score.Reasons) != 1 {
				t.Errorf("%s: %v %v", a.ID, a.Published, a.Score.Reasons)
			}
		case acc(3):
			if want := time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC); !a.Published.Equal(want) {
				t.Errorf("filed-date fallback: %v", a.Published)
			}
		}
	}
}

func TestTickersByCIK(t *testing.T) {
	f, err := os.Open("testdata/company_tickers.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entities, err := ParseTickers(f)
	if err != nil {
		t.Fatal(err)
	}
	got := TickersByCIK(entities)
	if !slices.Equal(got["0000320193"], []string{"AAPL"}) || !slices.Equal(got["0001045810"], []string{"NVDA"}) {
		t.Errorf("got %v", got)
	}
	if len(TickersByCIK(append(entities, entities...))["0000320193"]) != 1 {
		t.Error("duplicate ticker kept")
	}
}

func FuzzParseCurrent(f *testing.F) {
	for _, n := range []string{"current_8k.xml", "current_form4.xml", "current_empty.xml", "rate_limit.html"} {
		b, err := os.ReadFile("testdata/" + n)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = parseCurrent(b) })
}
