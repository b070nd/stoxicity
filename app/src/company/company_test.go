package company

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/httpx"
)

// Synthetic fixture in the shape the SEC documents for company_tickers.json
// (confirmed against the live file's first and last entries on 2026-11-04);
// the companies are invented except for the share-class pair, which is spelled
// the way that file spells one company with two tickers.
const tickerList = `{
 "0":{"cik_str":1000001,"ticker":"ACME","title":"Acme Corp"},
 "1":{"cik_str":1000002,"ticker":"GLOBB","title":"Globex Holdings Inc"},
 "2":{"cik_str":1000002,"ticker":"GLOB","title":"Globex Holdings Inc"},
 "3":{"cik_str":1000003,"ticker":"INIT","title":"INITECH SYSTEMS CORP"}
}`

var t0 = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

func resp(body string, hdr ...string) httpx.Response {
	h := http.Header{}
	for i := 0; i+1 < len(hdr); i += 2 {
		h.Set(hdr[i], hdr[i+1])
	}
	return httpx.Response{Status: 200, Header: h, Body: []byte(body)}
}

func TestLoadWithoutCacheIsEmpty(t *testing.T) {
	m, err := Load(t.TempDir())
	if err != nil || !m.Empty() {
		t.Fatalf("Load = %v, %v; want empty, nil", m, err)
	}
}

func TestRefreshStoresListAndMeta(t *testing.T) {
	dir := t.TempDir()
	f := httpx.NewFake()
	f.Reply(DefaultURL, resp(tickerList, "ETag", `"v1"`))
	res, err := Refresh(context.Background(), f, dir, Options{Now: func() time.Time { return t0 }})
	if err != nil || !res.Updated || res.Records != 4 {
		t.Fatalf("Refresh = %+v, %v", res, err)
	}
	m, err := Load(dir)
	if err != nil || len(m.Entities) != 4 || !m.FetchedAt.Equal(t0) {
		t.Fatalf("Load = %d entities, fetched %v, %v", len(m.Entities), m.FetchedAt, err)
	}
	if m.Entities[0].CIK != "0001000001" {
		t.Errorf("CIK = %q, want ten digits", m.Entities[0].CIK)
	}
}

func TestYoungCacheAsksNothing(t *testing.T) {
	dir := t.TempDir()
	f := httpx.NewFake()
	f.Reply(DefaultURL, resp(tickerList))
	now := func() time.Time { return t0 }
	if _, err := Refresh(context.Background(), f, dir, Options{Now: now}); err != nil {
		t.Fatal(err)
	}
	later := func() time.Time { return t0.Add(24 * time.Hour) }
	res, err := Refresh(context.Background(), f, dir, Options{Now: later})
	if err != nil || !res.Skipped {
		t.Fatalf("Refresh = %+v, %v; want skipped", res, err)
	}
	if n := len(f.Calls()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

func TestStaleCacheSendsETagAndRenewsOn304(t *testing.T) {
	dir := t.TempDir()
	f := httpx.NewFake()
	f.Reply(DefaultURL, resp(tickerList, "ETag", `"v1"`, "Last-Modified", "Mon, 02 Nov 2026 00:00:00 GMT"))
	f.Reply(DefaultURL, httpx.Response{Status: 304, NotModified: true})
	if _, err := Refresh(context.Background(), f, dir, Options{Now: func() time.Time { return t0 }}); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(8 * 24 * time.Hour)
	res, err := Refresh(context.Background(), f, dir, Options{Now: func() time.Time { return later }})
	if err != nil || res.Updated || res.Skipped || res.Records != 4 {
		t.Fatalf("Refresh = %+v, %v", res, err)
	}
	calls := f.Calls()
	if got := calls[len(calls)-1]; got.ETag != `"v1"` || got.LastModified == "" {
		t.Errorf("conditional request = %+v, want the stored ETag and Last-Modified", got)
	}
	m, _ := Load(dir)
	if !m.FetchedAt.Equal(later) {
		t.Errorf("FetchedAt = %v, want %v", m.FetchedAt, later)
	}
}

func TestForceAsksEvenWhenYoung(t *testing.T) {
	dir := t.TempDir()
	f := httpx.NewFake()
	f.Reply(DefaultURL, resp(tickerList))
	now := func() time.Time { return t0 }
	Refresh(context.Background(), f, dir, Options{Now: now})
	res, err := Refresh(context.Background(), f, dir, Options{Now: now, Force: true})
	if err != nil || !res.Updated {
		t.Fatalf("Refresh = %+v, %v", res, err)
	}
}

func TestBadAnswersNeverReplaceAGoodCache(t *testing.T) {
	for name, body := range map[string]string{
		"not json":   `<html>Undeclared Automated Tool</html>`,
		"empty list": `{}`,
		"too small":  `{"0":{"cik_str":1,"ticker":"ONE","title":"One"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			f := httpx.NewFake()
			f.Reply(DefaultURL, resp(tickerList))
			f.Reply(DefaultURL, resp(body))
			Refresh(context.Background(), f, dir, Options{Now: func() time.Time { return t0 }})
			_, err := Refresh(context.Background(), f, dir, Options{Force: true, Now: func() time.Time { return t0 }})
			if err == nil {
				t.Fatal("no error")
			}
			if m, _ := Load(dir); len(m.Entities) != 4 {
				t.Errorf("cache has %d entities after a bad answer, want 4", len(m.Entities))
			}
		})
	}
}

func TestDamagedCacheIsReplaced(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, listFile), []byte("{"), 0o644)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("Load error = %v, want damaged", err)
	}
	f := httpx.NewFake()
	f.Reply(DefaultURL, resp(tickerList))
	if res, err := Refresh(context.Background(), f, dir, Options{Now: func() time.Time { return t0 }}); err != nil || !res.Updated {
		t.Fatalf("Refresh = %+v, %v", res, err)
	}
}

func TestFindRanksTickerBeforeName(t *testing.T) {
	ents, _ := edgar.ParseTickers(strings.NewReader(tickerList))
	m := Master{Entities: ents}
	got := m.Find("glob", 10)
	if len(got) != 2 || got[0].Ticker != "GLOB" || got[1].Ticker != "GLOBB" {
		t.Fatalf("Find(glob) = %v", got)
	}
	if got := m.Find("initech", 10); len(got) != 1 || got[0].Ticker != "INIT" {
		t.Errorf("Find(initech) = %v", got)
	}
	if got := m.Find("", 10); got != nil {
		t.Errorf("Find(empty) = %v", got)
	}
}

func TestResolverFindsAShareClassPairOnceByName(t *testing.T) {
	ents, _ := edgar.ParseTickers(strings.NewReader(tickerList))
	r := NewResolver(Master{Entities: ents}, []string{"sap", "ACME"})

	found := r.Find("Globex raised its guidance, the company said.")
	if len(found) != 1 || found[0].Ticker != "GLOB" || found[0].Name != "Globex Holdings Inc" {
		t.Fatalf("Find by name = %v, want GLOB once with its name", found)
	}
	if e, ok := r.Lookup("globb"); !ok || e.Name != "Globex Holdings Inc" || e.CIK != "0001000002" {
		t.Errorf("Lookup(globb) = %v, %v; want the name and CIK", e, ok)
	}
	if found := r.Find("Shares of GLOBB rose on strong earnings."); len(found) != 1 || found[0].Ticker != "GLOBB" {
		t.Errorf("Find by second ticker = %v", found)
	}
	if e, ok := r.Lookup("SAP"); !ok || e.Name != "" {
		t.Errorf("Lookup(SAP) = %v, %v; want the extra ticker without a name", e, ok)
	}
	for _, text := range []string{"Initech Systems beat estimates.", "INITECH SYSTEMS beat estimates."} {
		if found := r.Find(text); len(found) != 1 || found[0].Ticker != "INIT" || found[0].Name != "Initech Systems Corp" {
			t.Errorf("Find(%q) = %v, want INIT named Initech Systems Corp", text, found)
		}
	}
}

func TestTidyName(t *testing.T) {
	for in, want := range map[string]string{
		"NVIDIA CORP":             "Nvidia Corp",
		"Apple Inc.":              "Apple Inc.",
		"IBM CORP":                "IBM Corp",
		"3M CO":                   "3M Co",
		"EXXON MOBIL CORP /DE/":   "Exxon Mobil Corp",
		"BERKSHIRE HATHAWAY INC":  "Berkshire Hathaway Inc",
		"Tesla, Inc.":             "Tesla, Inc.",
		"FOOBAR HOLDINGS \\NEW\\": "Foobar Holdings",
	} {
		if got := tidyName(in); got != want {
			t.Errorf("tidyName(%q) = %q, want %q", in, got, want)
		}
	}
}
