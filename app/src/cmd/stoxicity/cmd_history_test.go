package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (e *testEnv) withFundamentals(t *testing.T) {
	t.Helper()
	path := filepath.Join(e.dir, "config.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte("  fundamentals_url: "+e.server.URL+"/fund/\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFundamentalsHistoryShowsTheFiguresAsKnownThenAndTheRestatement(t *testing.T) {
	e := newTestEnv(t)
	e.withFundamentals(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)

	r := e.run(t, "universe", "fundamentals", "history", "-at", "2025-03-01", "ACME")
	r.wantCode(t, exitOK)
	r.wantOut(t, "2 facts read, 2 new", "known at 2025-03-02", "revenue")
	if strings.Contains(r.out, "restated") {
		t.Errorf("the amendment was not filed yet, so no restatement is known:\n%s", r.out)
	}
	if !strings.Contains(r.out, "100") || strings.Contains(r.out, "110  ") {
		t.Errorf("before the amendment the first figure:\n%s", r.out)
	}
	// From what is stored, no request: after the amendment the restated figure.
	r = e.run(t, "universe", "fundamentals", "history", "-stored", "-at", "2025-07-01", "ACME")
	r.wantCode(t, exitOK)
	r.wantOut(t, "known at 2025-07-02", "restated Revenues 2024-12-31")
	if !strings.Contains(r.out, "110") {
		t.Errorf("after the amendment:\n%s", r.out)
	}
	if again := e.run(t, "universe", "fundamentals", "history", "ACME"); !strings.Contains(again.out, "2 facts read, 0 new") {
		t.Errorf("reading again adds nothing:\n%s", again.out)
	}
}

func TestFundamentalsClassesReadsTheCoverPageAndValuesTheIssuer(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("STOXICITY_POLYGON_KEY", "test-key")
	e.withPolygon(t)
	e.withFundamentals(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)
	e.run(t, "universe", "quotes", "acme").wantCode(t, exitOK)

	r := e.run(t, "universe", "fundamentals", "classes", "ACME")
	r.wantCode(t, exitOK)
	r.wantOut(t, "read the cover page of ACME", "all shares", "2000000", "mapped (sole-listing", "issuer capitalisation")
}
