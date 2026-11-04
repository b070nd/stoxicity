package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompanyStatusBeforeAndAfterUpdate(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "company", "status")
	r.wantCode(t, exitOK)
	r.wantOut(t, "none yet")

	r = e.run(t, "company", "update")
	r.wantCode(t, exitOK)
	r.wantOut(t, "stored a new list: 3 tickers")

	r = e.run(t, "company", "status")
	r.wantOut(t, "tickers   3", "company.enabled")

	r = e.run(t, "company", "update")
	r.wantOut(t, "younger than company.max_age")

	r = e.run(t, "company", "update", "-force")
	r.wantCode(t, exitOK)
}

func TestCompanyFindShowsTidyNamesNotRawOnes(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "company", "update").wantCode(t, exitOK)
	r := e.run(t, "company", "find", "glob")
	r.wantCode(t, exitOK)
	r.wantOut(t, "GLOB", "GLOBB", "0001000002")

	r = e.run(t, "company", "find", "nothing-like-this")
	r.wantOut(t, "no company matches")
}

func TestCompanyFindWithoutAListSaysHowToGetOne(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "company", "find", "acme")
	if r.code == exitOK {
		t.Fatalf("exit %d, want an error", r.code)
	}
	if !strings.Contains(r.errOut, "company update") {
		t.Errorf("stderr:\n%s\nwant a pointer to \"company update\"", r.errOut)
	}
}

func TestFetchStoresTheCompanyMasterOnce(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "fetch").wantCode(t, exitOK)
	list := filepath.Join(e.dir, "data", "company", "company_tickers.json")
	if _, err := os.Stat(list); err != nil {
		t.Fatalf("fetch did not store the company master: %v", err)
	}
}

func TestCompanyMasterNamesReachTheStockPage(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "company", "update").wantCode(t, exitOK)
	e.run(t, "fetch").wantCode(t, exitOK)
	r := e.run(t, "stock", "ACME")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Acme Corp")
}
