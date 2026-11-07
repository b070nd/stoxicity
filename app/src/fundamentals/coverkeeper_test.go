package fundamentals

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

type coverMem struct {
	mu     sync.Mutex
	checks map[string]CoverCheck
	reads  []CoverRead
}

func newCoverMem() *coverMem { return &coverMem{checks: map[string]CoverCheck{}} }

func (m *coverMem) CoverChecks(context.Context) (map[string]CoverCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]CoverCheck{}
	for k, v := range m.checks {
		out[k] = v
	}
	return out, nil
}

func (m *coverMem) PutCover(_ context.Context, r CoverRead) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads = append(m.reads, r)
	m.checks[r.CIK] = CoverCheck{At: r.At, Accession: r.Accession}
	return nil
}

func (m *coverMem) PutCoverCheck(_ context.Context, cik string, n CoverCheck) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.checks[cik]
	acc := c.Accession
	c = n
	if c.Accession == "" {
		c.Accession = acc
	}
	m.checks[cik] = c
	return nil
}

var coverNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

const (
	subsFmt = "https://subs.test/CIK%s.json"
	arch    = "https://arch.test/"
)

func submissions(filings ...[5]string) httpx.Response { // accession, form, filed, doc, accepted
	var acc, form, filed, report, doc, accepted []string
	for _, f := range filings {
		acc, form, filed, report, doc, accepted = append(acc, f[0]), append(form, f[1]), append(filed, f[2]), append(report, f[2]), append(doc, f[3]), append(accepted, f[4])
	}
	q := func(s []string) string {
		for i := range s {
			s[i] = fmt.Sprintf("%q", s[i])
		}
		return "[" + strings.Join(s, ",") + "]"
	}
	body := fmt.Sprintf(`{"filings":{"recent":{"accessionNumber":%s,"form":%s,"filingDate":%s,"reportDate":%s,"primaryDocument":%s,"acceptanceDateTime":%s}}}`,
		q(acc), q(form), q(filed), q(report), q(doc), q(accepted))
	return httpx.Response{Status: 200, Body: []byte(body)}
}

// padded puts the cover first and filler after it, as a real report has.
func padded(front string, filler int) []byte {
	return []byte(strings.Replace(front, "</body>", strings.Repeat("<p>filler</p>", filler)+"</body>", 1))
}

func newKeeper(f *httpx.Fake, st *coverMem, cands ...CoverCandidate) *CoverKeeper {
	return &CoverKeeper{Store: st, Client: f, SubmissionsURL: subsFmt, Archives: arch, Now: func() time.Time { return coverNow },
		Candidates: func(context.Context) ([]CoverCandidate, error) { return cands, nil }}
}

func docURL(cik, acc, doc string) string {
	return fmt.Sprintf("%s%d/%s/%s", arch, 1, strings.ReplaceAll(acc, "-", ""), doc)
}

func TestCoverKeeperReadsTheStartOfTheDocumentAndStoresWithPublicAvailability(t *testing.T) {
	f := httpx.NewFake()
	f.Reply(fmt.Sprintf(subsFmt, "0000000001"), submissions([5]string{"0000000001-26-000010", "10-Q", "2026-10-30", "q.htm", "2026-10-30T16:05:12.000Z"}))
	f.Reply(docURL("1", "0000000001-26-000010", "q.htm"), httpx.Response{Status: http.StatusPartialContent, Body: padded(cover, 4000)})
	st := newCoverMem()
	rep, err := newKeeper(f, st, CoverCandidate{CIK: "0000000001", Ticker: "XA"}).Refresh(context.Background(), 5)
	if err != nil || rep.Stored != 1 || rep.Full != 0 || rep.Requests != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	calls := f.Calls()
	if got := calls[1].Header.Get("Range"); got != fmt.Sprintf("bytes=0-%d", coverPrefix-1) {
		t.Errorf("the start of the document is asked for: %q", got)
	}
	r := st.reads[0]
	if r.Accession != "0000000001-26-000010" || r.Filed != "2026-10-30" || len(r.Classes) != 2 ||
		!r.Accepted.Equal(time.Date(2026, 10, 30, 16, 5, 12, 0, time.UTC)) {
		t.Errorf("%+v", r)
	}
}

func TestCoverKeeperReadsTheWholeDocumentWhenTheCoverMayBeCutOff(t *testing.T) {
	f := httpx.NewFake()
	f.Reply(fmt.Sprintf(subsFmt, "0000000001"), submissions([5]string{"0000000001-26-000010", "10-Q", "2026-10-30", "q.htm", ""}))
	u := docURL("1", "0000000001-26-000010", "q.htm")
	// The start ends right after the cover: the second class could have been cut off.
	f.Reply(u, httpx.Response{Status: http.StatusPartialContent, Body: []byte(cover)})
	f.Reply(u, httpx.Response{Status: 200, Body: padded(cover, 10)})
	st := newCoverMem()
	rep, err := newKeeper(f, st, CoverCandidate{CIK: "0000000001"}).Refresh(context.Background(), 5)
	if err != nil || rep.Full != 1 || rep.Stored != 1 || rep.Requests != 3 {
		t.Fatalf("%+v %v", rep, err)
	}
	if !st.reads[0].Accepted.IsZero() {
		t.Errorf("no acceptance time is given, so none is made up from the day: %v", st.reads[0].Accepted)
	}
}

func TestCoverKeeperTakesAWholeDocumentWhenTheServerIgnoresRanges(t *testing.T) {
	f := httpx.NewFake()
	f.Reply(fmt.Sprintf(subsFmt, "0000000001"), submissions([5]string{"0000000001-26-000010", "10-Q", "2026-10-30", "q.htm", ""}))
	f.Reply(docURL("1", "0000000001-26-000010", "q.htm"), httpx.Response{Status: 200, Body: []byte(cover)})
	rep, err := newKeeper(f, newCoverMem(), CoverCandidate{CIK: "0000000001"}).Refresh(context.Background(), 5)
	if err != nil || rep.Stored != 1 || rep.Requests != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestCoverKeeperDoesNotReadAReportItAlreadyHas(t *testing.T) {
	f := httpx.NewFake()
	f.Reply(fmt.Sprintf(subsFmt, "0000000001"), submissions([5]string{"0000000001-26-000010", "10-Q", "2026-10-30", "q.htm", ""}))
	st := newCoverMem()
	st.checks["0000000001"] = CoverCheck{At: coverNow.Add(-40 * 24 * time.Hour), Accession: "0000000001-26-000010"}
	rep, err := newKeeper(f, st, CoverCandidate{CIK: "0000000001"}).Refresh(context.Background(), 5)
	if err != nil || rep.Unchanged != 1 || rep.Stored != 0 || len(f.Calls()) != 1 {
		t.Fatalf("%+v %v calls %d", rep, err, len(f.Calls()))
	}
	if !st.checks["0000000001"].At.Equal(coverNow) {
		t.Error("the look is noted so that the issuer is not asked again tomorrow")
	}
}

func TestCoverKeeperFallsBackToTheReportBeforeWhenTheNewestHasNoMachineReadableCover(t *testing.T) {
	f := httpx.NewFake()
	f.Reply(fmt.Sprintf(subsFmt, "0000000001"), submissions(
		[5]string{"0000000001-26-000020", "10-Q", "2026-10-30", "new.htm", ""},
		[5]string{"0000000001-26-000010", "10-K", "2026-03-01", "old.htm", ""}))
	f.Reply(docURL("1", "0000000001-26-000020", "new.htm"), httpx.Response{Status: 200, Body: []byte("<html><body>plain</body></html>")})
	f.Reply(docURL("1", "0000000001-26-000010", "old.htm"), httpx.Response{Status: 200, Body: []byte(cover)})
	st := newCoverMem()
	if rep, err := newKeeper(f, st, CoverCandidate{CIK: "0000000001"}).Refresh(context.Background(), 5); err != nil || rep.Stored != 1 || st.reads[0].Accession != "0000000001-26-000010" {
		t.Fatalf("%+v %v %+v", rep, err, st.reads)
	}
}

func TestCoverKeeperStopsOnABlockAndNotesOtherFailuresForALaterRetry(t *testing.T) {
	f := httpx.NewFake()
	f.Fail(fmt.Sprintf(subsFmt, "0000000001"), &httpx.Error{Class: httpx.Permanent, Err: errors.New("gone")})
	f.Fail(fmt.Sprintf(subsFmt, "0000000002"), &httpx.Error{Class: httpx.Blocked, Err: errors.New("blocked")})
	st := newCoverMem()
	rep, err := newKeeper(f, st, CoverCandidate{CIK: "0000000001"}, CoverCandidate{CIK: "0000000002"}, CoverCandidate{CIK: "0000000003"}).Refresh(context.Background(), 5)
	var he *httpx.Error
	if !errors.As(err, &he) || he.Class != httpx.Blocked {
		t.Fatalf("a block is returned: %v", err)
	}
	if rep.Bad != 1 || st.checks["0000000001"].Problem == "" || len(f.Calls()) != 2 {
		t.Errorf("%+v %+v calls %d", rep, st.checks, len(f.Calls()))
	}
	if _, ok := st.checks["0000000002"]; ok {
		t.Error("a block says nothing about the issuer")
	}
}

func TestDueOrdersFollowedThenNeverReadThenSeveralListingsAndHonoursTheWaits(t *testing.T) {
	checks := map[string]CoverCheck{
		"read-old":   {At: coverNow.Add(-40 * 24 * time.Hour), Accession: "a"},
		"read-young": {At: coverNow.Add(-5 * 24 * time.Hour), Accession: "a"},
		"bad-young":  {At: coverNow.Add(-1 * 24 * time.Hour), Problem: "x"},
		"bad-old":    {At: coverNow.Add(-4 * 24 * time.Hour), Problem: "x"},
	}
	cands := []CoverCandidate{
		{CIK: "single"}, {CIK: "multi", Listings: 2}, {CIK: "prio", Priority: true},
		{CIK: "read-old", Priority: true}, {CIK: "read-young"}, {CIK: "bad-young"}, {CIK: "bad-old"}, {CIK: "single"},
	}
	var got []string
	for _, c := range Due(cands, checks, coverNow, 0, 0) {
		got = append(got, c.CIK)
	}
	want := "prio read-old multi single bad-old" // the followed issuers come first, then those never read
	if strings.Join(got, " ") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}

func TestParseCoverAtSaysWhereTheCoverEnds(t *testing.T) {
	_, last, err := ParseCoverAt(padded(cover, 100))
	if err != nil || last <= 0 || last > int64(len(cover)) {
		t.Errorf("the last cover fact lies within the cover, not the filler: %d (cover is %d bytes) %v", last, len(cover), err)
	}
}

func TestBackoffDoublesWithEachFailureInARowAndIsCapped(t *testing.T) {
	day := 24 * time.Hour
	for n, want := range map[int]time.Duration{1: 3 * day, 2: 6 * day, 3: 12 * day, 4: MaxCoverBackoff, 9: MaxCoverBackoff, 0: 3 * day} {
		if got := Backoff(0, n); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestAFailedLookIsRememberedWithItsRetryTimeAndAGoodOneForgetsIt(t *testing.T) {
	ctx := context.Background()
	st := newCoverMem()
	f := httpx.NewFake()
	f.Fail(fmt.Sprintf(subsFmt, "0000000001"), &httpx.Error{Class: httpx.Permanent, Err: errors.New("gone")})
	k := newKeeper(f, st, CoverCandidate{CIK: "0000000001"})
	if _, err := k.Refresh(ctx, 5); err != nil {
		t.Fatal(err)
	}
	c := st.checks["0000000001"]
	if c.Kind != CheckFailed || c.Failures != 1 || !c.Next.Equal(coverNow.Add(DefaultCoverRetry)) || c.Problem == "" {
		t.Fatalf("after one failure: %+v", c)
	}
	// Not asked again before the stored retry time; asked when it has come, and the wait doubles.
	if len(Due([]CoverCandidate{{CIK: "0000000001"}}, st.checks, coverNow.Add(DefaultCoverRetry-time.Minute), 0, 0)) != 0 {
		t.Error("asked again before the retry time")
	}
	later := coverNow.Add(DefaultCoverRetry + time.Minute)
	k.Now = func() time.Time { return later }
	k.Refresh(ctx, 5)
	if c := st.checks["0000000001"]; c.Failures != 2 || !c.Next.Equal(later.Add(2*DefaultCoverRetry)) {
		t.Errorf("after two failures: %+v", c)
	}
	// A look that works clears it.
	f2 := httpx.NewFake()
	f2.Reply(fmt.Sprintf(subsFmt, "0000000001"), submissions([5]string{"0000000001-26-000010", "10-Q", "2026-10-30", "q.htm", ""}))
	f2.Reply(docURL("1", "0000000001-26-000010", "q.htm"), httpx.Response{Status: 200, Body: []byte(cover)})
	k.Client = f2
	k.Now = func() time.Time { return later.Add(30 * 24 * time.Hour) }
	if _, err := k.Refresh(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if c := st.checks["0000000001"]; c.Kind != "" || c.Failures != 0 || c.Problem != "" || !c.Next.IsZero() {
		t.Errorf("after a good look: %+v", c)
	}
}

func TestAnIssuerWithoutAMachineReadableCoverIsUnsupportedNotFailed(t *testing.T) {
	f := httpx.NewFake()
	f.Reply(fmt.Sprintf(subsFmt, "0000000001"), submissions([5]string{"0000000001-26-000010", "10-Q", "2026-10-30", "q.htm", ""}))
	f.Reply(docURL("1", "0000000001-26-000010", "q.htm"), httpx.Response{Status: 200, Body: []byte("<html><body>plain</body></html>")})
	st := newCoverMem()
	rep, err := newKeeper(f, st, CoverCandidate{CIK: "0000000001"}).Refresh(context.Background(), 5)
	c := st.checks["0000000001"]
	if err != nil || rep.Empty != 1 || rep.Bad != 0 || c.Kind != CheckNoCover || c.Failures != 0 || !c.Next.Equal(coverNow.Add(DefaultCoverAge)) {
		t.Errorf("%+v %v %+v", rep, err, c)
	}
}

func TestAnInterruptedWalkResumesFromTheStoredStateFollowedFirst(t *testing.T) {
	ctx := context.Background()
	st := newCoverMem()
	f := httpx.NewFake()
	var cands []CoverCandidate
	for _, id := range []string{"0000000001", "0000000002", "0000000003", "0000000004"} {
		f.Reply(fmt.Sprintf(subsFmt, id), submissions([5]string{id + "-26-000010", "10-Q", "2026-10-30", "q.htm", ""}))
		f.Reply(fmt.Sprintf("%s%s/%s/q.htm", arch, strings.TrimLeft(id, "0"), strings.ReplaceAll(id+"-26-000010", "-", "")), httpx.Response{Status: 200, Body: []byte(cover)})
		cands = append(cands, CoverCandidate{CIK: id, Priority: id == "0000000004"})
	}
	// Two at a time: the followed issuer goes first, then the walk goes on where it stopped.
	k := newKeeper(f, st, cands...)
	var order []string
	for i := 0; i < 3; i++ {
		before := len(st.reads)
		if _, err := k.Refresh(ctx, 2); err != nil {
			t.Fatal(err)
		}
		for _, r := range st.reads[before:] {
			order = append(order, r.CIK)
		}
	}
	if strings.Join(order, " ") != "0000000004 0000000001 0000000002 0000000003" {
		t.Errorf("order = %v", order)
	}
}
