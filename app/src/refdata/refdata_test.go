package refdata

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	masterURL = "https://sec.test/company_tickers_exchange.json"
	nasdaqURL = "https://nasdaq.test/nasdaqlisted.txt"
	otherURL  = "https://nasdaq.test/otherlisted.txt"
	subURL    = "https://sec.test/submissions/CIK%s.json"
)

// Synthetic fixtures in the documented shapes; the companies are invented.
const masterA = `{"data":[
 [1000001,"ACME CORP","ACME","Nasdaq"],
 [1000002,"Globex Holdings Inc","GLOBB","NYSE"],
 [1000002,"Globex Holdings Inc","GLOB","NYSE"],
 [1000003,"Initech Systems Corp","INIT","NYSE"]]}`

// The same companies, listed in another order and spaced differently.
const masterAShuffled = `{"data":[[1000003,"Initech Systems Corp","INIT","NYSE"],[1000002,"Globex Holdings Inc","GLOB","NYSE"],
 [1000001,"ACME CORP","ACME","Nasdaq"],[1000002,"Globex Holdings Inc","GLOBB","NYSE"]]}`

const masterB = `{"data":[
 [1000001,"ACME CORP","ACME","Nasdaq"],
 [1000002,"Globex Holdings Inc","GLOBB","NYSE"],
 [1000002,"Globex Holdings Inc","GLOB","NYSE"],
 [1000003,"Initech Systems Corp","INIT","NYSE"],
 [1000004,"Hooli Inc","HOOL","Nasdaq"]]}`

const nasdaqFile = `Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares
ACME|Acme Corp - Common Stock|Q|N|N|100|N|N
ETFX|Example Growth ETF|G|N|N|100|Y|N
File Creation Time: 1105202603:02|||||||
`

const otherFile = `ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol
GLOB|Globex Holdings Inc Common Stock|N|GLOB|N|100|N|GLOB
File Creation Time: 1105202603:02||||||
`

func submission(cik, name, sic, desc string) string {
	return `{"cik":"` + cik + `","entityType":"operating","sic":"` + sic + `","sicDescription":"` + desc +
		`","name":"` + name + `","fiscalYearEnd":"1231","stateOfIncorporation":"DE","category":"Large accelerated filer"}`
}

func body(s string, hdr ...string) httpx.Response {
	h := http.Header{}
	for i := 0; i+1 < len(hdr); i += 2 {
		h.Set(hdr[i], hdr[i+1])
	}
	return httpx.Response{Status: 200, Header: h, Body: []byte(s)}
}

var t0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

// fixture is a refresher over a fresh database and a fake SEC.
type fixture struct {
	r    *Refresher
	db   *store.DB
	fake *httpx.Fake
	now  time.Time
}

// serve makes a new fake SEC whose ticker list is answer (or err), keeping
// the other sources as they were. The fake repeats its last reply, so changing
// an answer means starting a new fake.
func (f *fixture) serve(answer httpx.Response, err error) {
	f.fake = httpx.NewFake()
	if err != nil {
		f.fake.Fail(masterURL, err)
	} else {
		f.fake.Reply(masterURL, answer)
	}
	f.fake.Reply(nasdaqURL, body(nasdaqFile))
	f.fake.Reply(otherURL, body(otherFile))
	f.fake.Reply(strings.Replace(subURL, "%s", "0001000001", 1), body(submission("0001000001", "Acme Corp", "7372", "Services-Prepackaged Software")))
	f.fake.Reply(strings.Replace(subURL, "%s", "0001000002", 1), body(submission("0001000002", "Globex", "3674", "Semiconductors")))
	f.fake.Reply(strings.Replace(subURL, "%s", "0001000003", 1), body(submission("0001000003", "Initech", "7373", "Computer Integrated Systems Design")))
	if f.r != nil {
		f.r.Client = f.fake
	}
}

func newFixture(t *testing.T, master string) *fixture {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{db: db, now: t0}
	f.serve(body(master), nil)
	f.r = &Refresher{
		Ref: db, Client: f.fake, Now: func() time.Time { return f.now },
		Set: Settings{
			Dir: t.TempDir(), MasterURL: masterURL, NasdaqURL: nasdaqURL, OtherURL: otherURL, SubmissionsURL: subURL,
			MaxAge: time.Hour, Listing: true, PerCycle: 10,
		},
	}
	return f
}

func TestCycleFillsTheDatabaseAndTheUniverseComesFromIt(t *testing.T) {
	f := newFixture(t, masterA)
	ctx := context.Background()
	rep := f.r.Cycle(ctx, false)
	if err := rep.Err(); err != nil {
		t.Fatal(err)
	}
	if rep.Tickers.Added != 4 || rep.Listing.Added != 3 || rep.Companies.Added != 3 || rep.Enriched.Fetched != 3 {
		t.Fatalf("report = %+v", rep)
	}
	u, err := Build(ctx, f.db, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := u.Get("ACME")
	if !ok || p.Industry != "Services-Prepackaged Software" || p.Kind != "stock" || p.Name != "Acme Corp" {
		t.Errorf("ACME = %+v", p)
	}
	if g, _ := u.Get("GLOBB"); len(g.Siblings) != 1 || g.Siblings[0] != "GLOB" || g.Venue != "" && g.Exchange != "NYSE" {
		t.Errorf("GLOBB = %+v", g)
	}
	if e, _ := u.Get("ETFX"); e.Ticker != "" {
		t.Logf("ETFX is only in the Nasdaq directory: %+v", e) // not in the SEC list, so no profile
	}
	srcs, _ := f.db.RefSources(ctx)
	if len(srcs) != 3 {
		t.Errorf("%d sources, want tickers, nasdaq and company", len(srcs))
	}
}

func TestSecondCycleWithNothingNewChangesNothing(t *testing.T) {
	f := newFixture(t, masterA)
	ctx := context.Background()
	f.r.Cycle(ctx, false)
	u1, _ := Build(ctx, f.db, "", nil, nil)
	stamp1, _ := Stamp(ctx, f.db)

	f.now = f.now.Add(2 * time.Hour) // the lists are due again
	rep := f.r.Cycle(ctx, false)
	if err := rep.Err(); err != nil {
		t.Fatal(err)
	}
	if !rep.Tickers.Same || !rep.Listing.Same || !rep.Companies.Same && rep.Companies.Added+rep.Companies.Changed > 0 {
		t.Errorf("report = %+v, want everything the same", rep)
	}
	snaps, _ := f.db.RefSnapshots(ctx, store.SourceTickers, 10)
	if len(snaps) != 1 {
		t.Errorf("%d ticker snapshots after an unchanged refresh, want 1", len(snaps))
	}
	u2, _ := Build(ctx, f.db, "", nil, nil)
	if u1.Fingerprint() != u2.Fingerprint() {
		t.Error("the universe changed although nothing did")
	}
	if stamp2, _ := Stamp(ctx, f.db); stamp1 != stamp2 {
		t.Error("the stamp changed although nothing did")
	}
	src, _, _ := f.db.RefSource(ctx, store.SourceTickers)
	if !src.CheckedAt.Equal(f.now) || !src.ChangedAt.Equal(t0) {
		t.Errorf("source = %+v, want checked now and changed at the start", src)
	}
}

func TestAnUpstreamChangeIsAppliedAndLogged(t *testing.T) {
	f := newFixture(t, masterA)
	ctx := context.Background()
	f.r.Cycle(ctx, false)
	f.serve(body(masterB), nil)
	f.now = f.now.Add(2 * time.Hour)
	rep := f.r.Cycle(ctx, false)
	if rep.Tickers.Added != 1 {
		t.Fatalf("report = %+v", rep)
	}
	ch, _ := f.db.RefChanges(ctx, time.Time{}, 10)
	if len(ch) != 1 || ch[0].Kind != "added" || ch[0].Key != "HOOL" {
		t.Errorf("changes = %+v", ch)
	}
	u, _ := Build(ctx, f.db, "", nil, nil)
	if _, ok := u.Get("HOOL"); !ok {
		t.Error("HOOL is not in the universe")
	}
}

func TestAFailingSourceKeepsTheOldRowsAndSaysWhy(t *testing.T) {
	f := newFixture(t, masterA)
	ctx := context.Background()
	f.r.Cycle(ctx, false)
	f.serve(httpx.Response{}, &httpx.Error{Class: httpx.Retryable, URL: "https://sec.test", Err: errors.New("boom")})
	f.now = f.now.Add(2 * time.Hour)
	rep := f.r.Cycle(ctx, false)
	if rep.Err() == nil {
		t.Fatal("no problem reported")
	}
	if rows, _ := f.db.RefTickers(ctx); len(rows) != 4 {
		t.Errorf("%d tickers after a failed refresh, want 4", len(rows))
	}
	src, _, _ := f.db.RefSource(ctx, store.SourceTickers)
	if src.Error == "" {
		t.Error("the failure is not recorded in the source")
	}
	// A later success clears it.
	f.serve(body(masterA), nil)
	f.now = f.now.Add(2 * time.Hour)
	f.r.Cycle(ctx, false)
	if src, _, _ = f.db.RefSource(ctx, store.SourceTickers); src.Error != "" {
		t.Errorf("error after a success: %q", src.Error)
	}
}

func TestSameContentGivesTheSameDatabaseWhateverTheOrderAndTime(t *testing.T) {
	a := newFixture(t, masterA)
	b := newFixture(t, masterAShuffled)
	b.now = t0.Add(37 * 24 * time.Hour) // another day entirely
	ctx := context.Background()
	a.r.Cycle(ctx, false)
	b.r.Cycle(ctx, false)

	ua, _ := Build(ctx, a.db, "", nil, nil)
	ub, _ := Build(ctx, b.db, "", nil, nil)
	if ua.Fingerprint() == "" || ua.Fingerprint() != ub.Fingerprint() {
		t.Errorf("fingerprints differ:\n%s\n%s", ua.Fingerprint(), ub.Fingerprint())
	}
	sa, _, _ := a.db.RefSource(ctx, store.SourceTickers)
	sb, _, _ := b.db.RefSource(ctx, store.SourceTickers)
	if sa.Hash != sb.Hash {
		t.Errorf("content hashes differ: %s %s", sa.Hash, sb.Hash)
	}
}

func TestInterestingCompaniesAreFetchedFirstWithinTheBudget(t *testing.T) {
	f := newFixture(t, masterA)
	f.r.Set.PerCycle = 1
	f.r.Interesting = func(context.Context) []string { return []string{"INIT"} }
	f.r.Cycle(context.Background(), false)
	rows, _ := f.db.RefCompanies(context.Background())
	if len(rows) != 1 || rows[0].CIK != "0001000003" {
		t.Errorf("fetched %+v, want only Initech's record", rows)
	}
	// Next cycle: the rest, lowest CIK first.
	f.now = f.now.Add(time.Minute)
	f.r.Cycle(context.Background(), false)
	rows, _ = f.db.RefCompanies(context.Background())
	if len(rows) != 2 || rows[0].CIK != "0001000001" {
		t.Errorf("after two cycles: %+v", rows)
	}
}

func TestRunRepeatsUntilTheContextEnds(t *testing.T) {
	f := newFixture(t, masterA)
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var waits []time.Duration
	f.r.Logf = func(string, ...any) {}
	done := make(chan struct{})
	go func() {
		f.r.run(ctx, 6*time.Hour, func(d time.Duration) <-chan time.Time {
			mu.Lock()
			waits = append(waits, d)
			n := len(waits)
			mu.Unlock()
			if n == 3 {
				cancel()
			}
			c := make(chan time.Time, 1)
			c <- time.Time{}
			return c
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not end")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, w := range waits {
		if w != 6*time.Hour {
			t.Errorf("waited %v, want a fixed 6h", w)
		}
	}
	if len(waits) < 3 {
		t.Errorf("%d cycles", len(waits))
	}
}
