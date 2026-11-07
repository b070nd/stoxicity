package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReconcileFindsWhatTheLiveFeedMissedAndRepairsIt(t *testing.T) {
	e := newTestEnv(t)
	cfgPath := filepath.Join(e.dir, "config.yaml")
	cfg, _ := os.ReadFile(cfgPath)
	if err := os.WriteFile(cfgPath, append(cfg, []byte("  insiders_url: "+e.server.URL+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(e.dir, "sources.yaml")
	srcs, _ := os.ReadFile(srcPath)
	extra := "  - id: sec-edgar\n    name: SEC\n    domain: sec.gov\n    kind: filings\n    url: https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&output=atom\n    tier: 1\n    forms: [\"8-K\", \"4\"]\n    licence: redistributable\n    terms: https://www.sec.gov/about/privacy-information\n    enabled: false\n"
	if err := os.WriteFile(srcPath, append(srcs, []byte(extra)...), 0o644); err != nil {
		t.Fatal(err)
	}
	e.run(t, "universe", "update").wantCode(t, exitOK)

	r := e.run(t, "reconcile", "-days", "2")
	r.wantCode(t, exitOK)
	r.wantOut(t, "1 days read", "2026-11-02", "the live feed missed 2 of the wanted filings", "run with -repair")

	r = e.run(t, "reconcile", "-days", "2", "-repair")
	r.wantCode(t, exitOK)
	r.wantOut(t, "0 days read, 1 already stored", "repaired: 2 articles stored")

	r = e.run(t, "reconcile", "-days", "2")
	r.wantCode(t, exitOK)
	r.wantOut(t, "the live feed missed 0")
}
