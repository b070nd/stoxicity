package edgar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

var submissionsSrc = model.Source{ID: "apple-filings", Kind: model.KindFilings,
	URL: "https://data.sec.gov/submissions/CIK0000320193.json"}

func TestSubmissionsParse(t *testing.T) {
	c := &fakeClient{fn: serve(fixture(t, "submissions.json"))}
	res, err := NewSubmissions(c, testConfig()).Fetch(context.Background(), submissionsSrc, feeds.State{})
	if err != nil {
		t.Fatal(err)
	}
	if c.reqs[0].URL != "http://data.test/submissions/CIK0000320193.json" {
		t.Errorf("url %s", c.reqs[0].URL)
	}
	// "bogus-number" is skipped; the plain 18-digit accession is accepted.
	if len(res.Articles) != 4 {
		t.Fatalf("got %d articles", len(res.Articles))
	}
	byID := map[string]model.Article{}
	for _, a := range res.Articles {
		byID[a.ID] = a
	}

	eightK := byID["0000320193-26-000026"]
	if eightK.Form != "8-K" || eightK.Title != "8-K - Apple Inc." || eightK.Domain != "sec.gov" || eightK.SourceID != "apple-filings" ||
		!slices.Equal(eightK.Items, []string{"2.02", "9.01"}) || !slices.Equal(eightK.Tickers, []string{"AAPL"}) {
		t.Errorf("8-K: %+v", eightK)
	}
	if want := "http://web.test/Archives/edgar/data/320193/000032019326000026/aapl-20261104.htm"; eightK.URL != want {
		t.Errorf("url %s, want %s", eightK.URL, want)
	}
	if want := time.Date(2026, 11, 4, 15, 58, 12, 0, time.UTC); !eightK.Published.Equal(want) || eightK.Published.Location() != time.UTC {
		t.Errorf("published %v", eightK.Published)
	}
	if !strings.Contains(eightK.Summary, "items 2.02, 9.01") {
		t.Errorf("summary %q", eightK.Summary)
	}

	if q := byID["0000320193-26-000020"]; q.Form != "10-Q" || q.Items != nil {
		t.Errorf("10-Q: %+v", q)
	}
	four := byID["0000320193-26-000015"]
	if four.URL != FilingIndexURL("0000320193", "0000320193-26-000015") {
		t.Errorf("no primary document should give the index page: %s", four.URL)
	}
	if want := time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC); !four.Published.Equal(want) {
		t.Errorf("filing date fallback (midnight US Eastern): %v", four.Published)
	}
	if old := byID["0000320193-26-000010"]; !slices.Equal(old.Items, []string{"5.02", "9.01"}) {
		t.Errorf("items from text: %v", old.Items)
	}
	if res.State.Seen[0] != "0000320193-26-000026" || len(res.State.Seen) != 4 {
		t.Errorf("seen %v", res.State.Seen)
	}
}

func TestSubmissionsSkipsKnownAccessions(t *testing.T) {
	c := &fakeClient{fn: serve(fixture(t, "submissions.json"))}
	prev := feeds.State{Seen: []string{"0000320193-26-000026", "0000320193-26-000020", "0000320193-26-000010"}}
	res, err := NewSubmissions(c, testConfig()).Fetch(context.Background(), submissionsSrc, prev)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Articles) != 1 || res.Articles[0].ID != "0000320193-26-000015" {
		t.Errorf("articles %+v", res.Articles)
	}
	if len(res.State.Seen) != 4 || res.State.Seen[0] != "0000320193-26-000015" {
		t.Errorf("seen %v", res.State.Seen)
	}

	prev.Seen = append(prev.Seen, "0000320193-26-000015")
	res, _ = NewSubmissions(c, testConfig()).Fetch(context.Background(), submissionsSrc, prev)
	if len(res.Articles) != 0 || res.NotModified || len(res.State.Seen) != 4 {
		t.Errorf("all known: %d articles, %d seen", len(res.Articles), len(res.State.Seen))
	}
}

func TestSubmissionsFormFilterAndLimit(t *testing.T) {
	c := &fakeClient{fn: serve(fixture(t, "submissions.json"))}
	src := submissionsSrc
	src.Forms = []string{"8-K"}
	res, _ := NewSubmissions(c, testConfig()).Fetch(context.Background(), src, feeds.State{})
	if len(res.Articles) != 2 {
		t.Errorf("form filter kept %d", len(res.Articles))
	}

	cfg := testConfig()
	cfg.MaxFilings = 1
	res, _ = NewSubmissions(c, cfg).Fetch(context.Background(), submissionsSrc, feeds.State{})
	if len(res.Articles) != 1 || res.Articles[0].ID != "0000320193-26-000026" {
		t.Errorf("limit kept %+v", res.Articles)
	}
}

func TestSubmissionsTickersFromConfigWhenFileHasNone(t *testing.T) {
	body := `{"name":"X","filings":{"recent":{"accessionNumber":["0000320193-26-000026"],"form":["8-K"]}}}`
	cfg := testConfig()
	cfg.Tickers = map[string][]string{"0000320193": {"AAPL"}}
	res, err := NewSubmissions(&fakeClient{fn: serve([]byte(body))}, cfg).Fetch(context.Background(), submissionsSrc, feeds.State{})
	if err != nil || len(res.Articles) != 1 || !slices.Equal(res.Articles[0].Tickers, []string{"AAPL"}) {
		t.Fatalf("err %v articles %+v", err, res.Articles)
	}
	if !res.Articles[0].Published.Equal(fetchTime) || len(res.Articles[0].Score.Reasons) != 1 {
		t.Errorf("missing time: %v %v", res.Articles[0].Published, res.Articles[0].Score.Reasons)
	}
}

func TestSubmissionsETag(t *testing.T) {
	c := &fakeClient{fn: func(req httpx.Request) (*httpx.Response, error) {
		if req.ETag == `"abc"` {
			return &httpx.Response{Status: 304, NotModified: true}, nil
		}
		return &httpx.Response{Status: 200, Body: fixture(t, "submissions.json"),
			Header: http.Header{"Etag": {`"abc"`}, "Last-Modified": {"Wed, 04 Nov 2026 16:00:00 GMT"}}}, nil
	}}
	s := NewSubmissions(c, testConfig())
	first, err := s.Fetch(context.Background(), submissionsSrc, feeds.State{})
	if err != nil {
		t.Fatal(err)
	}
	if first.State.ETag != `"abc"` || first.State.LastModified != "Wed, 04 Nov 2026 16:00:00 GMT" {
		t.Errorf("state %+v", first.State)
	}
	second, err := s.Fetch(context.Background(), submissionsSrc, first.State)
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotModified || len(second.Articles) != 0 || second.State.ETag != `"abc"` || len(second.State.Seen) != 4 {
		t.Errorf("304 result %+v", second)
	}
	if c.reqs[0].ETag != "" || c.reqs[1].ETag != `"abc"` || c.reqs[1].LastModified != "Wed, 04 Nov 2026 16:00:00 GMT" {
		t.Errorf("validators sent: %+v", c.reqs)
	}
}

func TestSubmissionsSeveralCompanies(t *testing.T) {
	src := model.Source{ID: "watch", Kind: model.KindFilings, Tags: []string{"filings", "cik:320193", "cik:0000789019", "cik:320193"}}
	if got := sourceCIKs(src); !slices.Equal(got, []string{"0000320193", "0000789019"}) {
		t.Fatalf("ciks %v", got)
	}
	file := func(accNo string) []byte {
		return []byte(`{"name":"Co","filings":{"recent":{"accessionNumber":["` + accNo + `"],"form":["8-K"],"acceptanceDateTime":["2026-11-04T15:00:00.000Z"]}}}`)
	}
	c := &fakeClient{fn: func(req httpx.Request) (*httpx.Response, error) {
		switch {
		case strings.Contains(req.URL, "CIK0000320193") && req.ETag == `"a1"`:
			return &httpx.Response{Status: 304, NotModified: true}, nil
		case strings.Contains(req.URL, "CIK0000320193"):
			return &httpx.Response{Status: 200, Body: file("0000320193-26-000001"), Header: http.Header{"Etag": {`"a1"`}}}, nil
		}
		return &httpx.Response{Status: 200, Body: file("0000789019-26-000001"), Header: http.Header{"Etag": {`"m1"`}}}, nil
	}}
	s := NewSubmissions(c, testConfig())
	first, err := s.Fetch(context.Background(), src, feeds.State{})
	if err != nil || len(first.Articles) != 2 {
		t.Fatalf("err %v, %d articles", err, len(first.Articles))
	}
	if first.State.ETag != "" {
		t.Errorf("a shared ETag for two companies: %q", first.State.ETag)
	}
	var cursor map[string]validators
	if err := json.Unmarshal([]byte(first.State.Cursor), &cursor); err != nil || cursor["0000320193"].ETag != `"a1"` || cursor["0000789019"].ETag != `"m1"` {
		t.Errorf("cursor %q", first.State.Cursor)
	}

	c.reqs = nil
	second, err := s.Fetch(context.Background(), src, first.State)
	if err != nil {
		t.Fatal(err)
	}
	// Apple answers 304; Microsoft's file has nothing new.
	if second.NotModified || len(second.Articles) != 0 || c.reqs[0].ETag != `"a1"` || c.reqs[1].ETag != `"m1"` {
		t.Errorf("second: %+v reqs %+v", second, c.reqs)
	}
}

func TestSubmissionsErrors(t *testing.T) {
	t.Run("no company", func(t *testing.T) {
		src := model.Source{ID: "x", Kind: model.KindFilings, URL: "https://data.sec.gov/other"}
		_, err := NewSubmissions(&fakeClient{fn: serve(nil)}, testConfig()).Fetch(context.Background(), src, feeds.State{})
		var he *httpx.Error
		if !errors.As(err, &he) || he.Class != httpx.Permanent {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("rate limit page", func(t *testing.T) {
		c := &fakeClient{fn: serve(fixture(t, "rate_limit.html"))}
		_, err := NewSubmissions(c, testConfig()).Fetch(context.Background(), submissionsSrc, feeds.State{})
		var he *httpx.Error
		if !errors.As(err, &he) || he.Class != httpx.Blocked {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("bad json", func(t *testing.T) {
		c := &fakeClient{fn: serve([]byte(`{"filings":`))}
		_, err := NewSubmissions(c, testConfig()).Fetch(context.Background(), submissionsSrc, feeds.State{})
		var he *httpx.Error
		if !errors.As(err, &he) || he.Class != httpx.Permanent || !strings.Contains(err.Error(), "edgar:") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("client error unchanged", func(t *testing.T) {
		want := &httpx.Error{Class: httpx.Retryable, URL: "http://data.test/x", Err: errors.New("reset")}
		c := &fakeClient{fn: func(httpx.Request) (*httpx.Response, error) { return nil, want }}
		_, err := NewSubmissions(c, testConfig()).Fetch(context.Background(), submissionsSrc, feeds.State{})
		if err != error(want) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestDispatch(t *testing.T) {
	c := &fakeClient{fn: serve(fixture(t, "current_empty.xml"))}
	f := New(c, testConfig())
	if _, err := f.Fetch(context.Background(), currentSrc, feeds.State{}); err != nil || !strings.Contains(c.reqs[0].URL, "/cgi-bin/browse-edgar") {
		t.Errorf("current: %v %v", err, c.reqs)
	}
	c.fn = serve(fixture(t, "submissions.json"))
	if _, err := f.Fetch(context.Background(), submissionsSrc, feeds.State{}); err != nil || !strings.Contains(c.reqs[1].URL, "/submissions/") {
		t.Errorf("submissions: %v %v", err, c.reqs)
	}
}
