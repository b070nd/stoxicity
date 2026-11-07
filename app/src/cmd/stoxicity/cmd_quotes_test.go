package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withPolygon points the program's candle source at the test server.
func (e *testEnv) withPolygon(t *testing.T) {
	t.Helper()
	path := filepath.Join(e.dir, "config.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte("  quotes_url: "+e.server.URL+"/polygon/\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUniverseQuotesNeedsAKeyAndSaysWhere(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("STOXICITY_POLYGON_KEY", "")
	e.run(t, "universe", "update").wantCode(t, exitOK)
	r := e.run(t, "universe", "quotes")
	if r.code == exitOK || !strings.Contains(r.errOut, "STOXICITY_POLYGON_KEY") {
		t.Errorf("without a key: code %d, stderr %q", r.code, r.errOut)
	}
}

func TestUniverseQuotesStoresTheCandlesOfOurStocksAndShowsThem(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("STOXICITY_POLYGON_KEY", "test-key")
	e.withPolygon(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)

	r := e.run(t, "universe", "quotes", "acme")
	r.wantCode(t, exitOK)
	r.wantOut(t, "candles (polygon)", "ACME", "close 11.50", "stocks with a candle")
	// Only candles a chart can draw, for stocks the list knows, are kept: not the
	// one with its low over its high, not the ticker nobody lists.
	if strings.Contains(r.out, "GLOB ") || strings.Contains(r.out, "NOTLISTED") {
		t.Errorf("kept a candle it should not:\n%s", r.out)
	}

	// Asked again the same day, nothing is fetched twice.
	r = e.run(t, "universe", "quotes", "acme")
	r.wantCode(t, exitOK)
	r.wantOut(t, "0 asked")

	r = e.run(t, "universe", "quotes", "NOPE")
	if r.code == exitOK {
		t.Error("an unknown ticker should fail")
	}
}
