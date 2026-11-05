package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUniverseStatusBeforeAndAfterUpdate(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "universe", "status")
	r.wantCode(t, exitOK)
	r.wantOut(t, "none yet", "1 profiles") // the watchlist names ACME

	r = e.run(t, "universe", "update")
	r.wantCode(t, exitOK)
	r.wantOut(t, "SEC list      new copy stored", "symbol files  new copy stored, 3 entries")

	r = e.run(t, "universe", "status")
	r.wantOut(t, "3 tickers", "3 symbols", "profiles")

	r = e.run(t, "universe", "update")
	r.wantOut(t, "young enough")
}

func TestUniverseFindFiltersByExchangeKindAndText(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)

	r := e.run(t, "universe", "find", "acme")
	r.wantCode(t, exitOK)
	r.wantOut(t, "ACME", "Acme Corp", "Nasdaq", "stock")

	r = e.run(t, "universe", "find", "exchange:nyse")
	r.wantOut(t, "GLOB")
	if strings.Contains(r.out, "ACME") {
		t.Errorf("exchange:nyse listed ACME:\n%s", r.out)
	}

	r = e.run(t, "universe", "find", "-limit", "1", "ticker:G*")
	r.wantOut(t, "GLOB", "1 more")

	r = e.run(t, "universe", "find", "nothing-like-this")
	r.wantOut(t, "no company matches")
}

func TestUniverseFindRejectsABadQuery(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)
	r := e.run(t, "universe", "find", "nope:x")
	r.wantCode(t, exitUsage)
	if !strings.Contains(r.errOut, "unknown operator") {
		t.Errorf("stderr:\n%s", r.errOut)
	}
}

func TestUniverseFindWithoutAListSaysHowToGetOne(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "universe", "find", "acme")
	if r.code == exitOK || !strings.Contains(r.errOut, "universe update") {
		t.Errorf("exit %d, stderr:\n%s", r.code, r.errOut)
	}
}

func TestUniverseTagsAndListsPersistPerTenant(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)

	r := e.run(t, "universe", "tag", "acme", "Moat", "software")
	r.wantCode(t, exitOK)
	r.wantOut(t, "ACME tags: moat, software")
	r = e.run(t, "universe", "tag", "acme", "-software")
	r.wantOut(t, "ACME tags: moat")

	r = e.run(t, "universe", "tag", "acme", "Not A Tag")
	if r.code == exitOK {
		t.Error("a bad tag should fail")
	}

	r = e.run(t, "universe", "list")
	r.wantOut(t, "no lists yet")
	r = e.run(t, "universe", "list", "add", "picks", "acme", "glob")
	r.wantCode(t, exitOK)
	r.wantOut(t, "picks (picks): ACME GLOB")
	r = e.run(t, "universe", "list", "remove", "picks", "glob")
	r.wantOut(t, "picks (picks): ACME")
	r = e.run(t, "universe", "list")
	r.wantOut(t, "picks", "1")

	r = e.run(t, "universe", "find", "list:picks", "tag:moat")
	r.wantOut(t, "ACME")
	r = e.run(t, "universe", "find", "--", "-tag:moat")
	if strings.Contains(r.out, "ACME") {
		t.Errorf("-tag:moat listed ACME:\n%s", r.out)
	}

	file := filepath.Join(e.dir, "tenants", "default", "stocks.yaml")
	raw, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(raw), "moat") {
		t.Fatalf("stocks.yaml: %v\n%s", err, raw)
	}
}

func TestUniverseEnrichStoresIndustryAndShowPrintsIt(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)

	r := e.run(t, "universe", "show", "acme")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Acme Corp", "0001000001", "Nasdaq", "no industry yet", "stoxicity universe enrich ACME", "browse-edgar")

	r = e.run(t, "universe", "enrich", "ACME")
	r.wantCode(t, exitOK)
	r.wantOut(t, "stored 1 records")

	r = e.run(t, "universe", "show", "acme")
	r.wantOut(t, "Services-Prepackaged Software (SIC 7372)", "DE", "1231", "ACME HOLDINGS")

	r = e.run(t, "universe", "find", "industry:software")
	r.wantOut(t, "ACME")

	r = e.run(t, "universe", "enrich", "ACME")
	r.wantOut(t, "1 were young enough")

	r = e.run(t, "universe", "enrich", "NOPE")
	if r.code == exitOK {
		t.Error("an unknown ticker should fail")
	}
}

func TestUniverseShowUnknownTicker(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)
	r := e.run(t, "universe", "show", "nope")
	if r.code == exitOK || !strings.Contains(r.errOut, "universe find") {
		t.Errorf("exit %d, stderr:\n%s", r.code, r.errOut)
	}
}

func TestUniverseUsage(t *testing.T) {
	e := newTestEnv(t)
	for _, args := range [][]string{{"universe"}, {"universe", "wat"}, {"universe", "tag", "ACME"}, {"universe", "list", "add", "x"}, {"universe", "show"}} {
		if r := e.run(t, args...); r.code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, r.code, exitUsage)
		}
	}
}

func TestFetchAlsoStoresTheSymbolDirectory(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "fetch").wantCode(t, exitOK)
	if _, err := os.Stat(filepath.Join(e.dir, "data", "company", "nasdaqlisted.txt")); err != nil {
		t.Fatalf("fetch did not store the symbol directory: %v", err)
	}
}

func TestSearchAndWatchQueriesUnderstandCompaniesListsAndTags(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "fetch").wantCode(t, exitOK)
	e.run(t, "universe", "list", "add", "picks", "acme").wantCode(t, exitOK)
	e.run(t, "universe", "tag", "acme", "moat").wantCode(t, exitOK)

	for _, q := range []string{"company:acme", "list:picks", "tag:moat", "exchange:nasdaq", "-list:picks"} {
		r := e.run(t, "search", "--", q)
		r.wantCode(t, exitOK)
		has := strings.Contains(r.out, "Acme Corp announces")
		if want := q != "-list:picks"; has != want {
			t.Errorf("search %q: Acme article listed = %v, want %v\n%s", q, has, want, r.out)
		}
	}
	if r := e.run(t, "search", "--", "list:nope"); r.code == exitOK || !strings.Contains(r.errOut, "picks") {
		t.Errorf("an unknown list should name the lists: exit %d, stderr:\n%s", r.code, r.errOut)
	}

	dir := filepath.Join(e.dir, "tenants", "default", "watches")
	os.WriteFile(filepath.Join(dir, "good.yaml"), []byte("id: good\nname: Good\nquery: list:picks\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("id: bad\nname: Bad\nquery: list:nope\n"), 0o644)
	r := e.run(t, "watch", "check")
	if r.code == exitOK {
		t.Errorf("a watch with an unknown list should fail the check:\n%s", r.out)
	}
	r.wantOut(t, "ok    good", "FAIL  watch \"bad\"")
}
