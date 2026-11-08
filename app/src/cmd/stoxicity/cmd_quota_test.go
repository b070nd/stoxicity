package main

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Scenario 6 uses the real HTTP/accounting seam and a local synthetic ECB feed.
// Every run reopens the database: a process restart must not refill the budget.
func TestAcceptanceProviderQuotaPreservesStoredDataAndOfflineWorkflows(t *testing.T) {
	e := newTestEnv(t)
	writeFile(t, e, "config.yaml", "since: 2026-11-01\ndata_dir: ./data\ncompany: {enabled: false}\nfetch:\n  quotas:\n    127.0.0.1: {requests: 1, period: 1h}\n")
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	url := e.server.URL + "/ecb/daily.xml"
	e.run(t, "fx", "fetch", "-url", url).wantOut(t, "3 rates read, 3 new")
	r := e.run(t, "fx", "fetch", "-url", url+"?apiKey=do-not-log")
	r.wantCode(t, exitError)
	r.wantErr(t, "provider quota 127.0.0.1", "configured request budget exhausted", "2026-11-03T11:00:00Z")
	if strings.Contains(r.errOut, "do-not-log") {
		t.Fatal("credential leaked")
	}
	r = e.run(t, "quota", "status", "-json")
	r.wantCode(t, exitOK)
	r.wantOut(t, "quota_exhausted", `"used": 1`, `"configured_requests": 1`)
	e.run(t, "fx", "list").wantOut(t, "EUR/USD", "ecb", "2026-11-06")
	e.run(t, "fx", "rate", "-from", "EUR", "-to", "USD", "-day", "2026-11-06").wantOut(t, "1.0850")
	r = e.run(t, "fx", "rate", "-from", "EUR", "-to", "USD", "-day", "2026-11-08")
	r.wantCode(t, exitError)
	r.wantOut(t, "stale")
	r = e.run(t, "fx", "rate", "-from", "EUR", "-to", "CHF", "-day", "2026-11-06")
	r.wantCode(t, exitError)
	r.wantOut(t, "missing")
	e.run(t, "portfolio", "show", "main/core").wantOut(t, "1000.00 EUR")
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)
	e.run(t, "plan", "list").wantCode(t, exitOK)
	e.run(t, "income", "expected", "main/core").wantCode(t, exitOK)
	e.run(t, "search", "earnings").wantCode(t, exitOK)
	// An expired budget permits a fresh request; existing observations are not
	// retimestamped or replaced as a result of the failed refresh.
	r = runWith(t, runOptions{cwd: e.dir, client: e.client, now: func() time.Time { return testNow.Add(time.Hour) }}, "-config-dir", e.dir, "fx", "fetch", "-url", url)
	r.wantCode(t, exitOK)
	r.wantOut(t, "0 new, 3 already stored")
}

func TestProviderThrottleSurvivesCLIReopenWithoutLosingObservations(t *testing.T) {
	e := newTestEnv(t)
	mux := e.server.Config.Handler.(*http.ServeMux)
	var hits atomic.Int32
	mux.HandleFunc("/quota.xml", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "86400")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	url := e.server.URL + "/quota.xml"
	for i := 0; i < 2; i++ {
		r := e.run(t, "fx", "fetch", "-url", url)
		r.wantCode(t, exitError)
		r.wantErr(t, "provider quota", "2026-11-04T10:00:00Z")
	}
	if hits.Load() != 1 {
		t.Fatalf("throttled provider got %d calls", hits.Load())
	}
	e.run(t, "quota", "status").wantOut(t, "quota_exhausted", "provider throttled")
	e.run(t, "fx", "list").wantCode(t, exitOK)
}

func TestQuotaStatusMatchesExtendedExhaustedWindow(t *testing.T) {
	e := newTestEnv(t)
	cfg := "data_dir: ./data\ncompany: {enabled: false}\nfetch:\n  quotas:\n    127.0.0.1: {requests: 1, period: 1h}\n"
	writeFile(t, e, "config.yaml", cfg)
	url := e.server.URL + "/ecb/daily.xml"
	e.run(t, "fx", "fetch", "-url", url).wantCode(t, exitOK)
	writeFile(t, e, "config.yaml", strings.ReplaceAll(cfg, "period: 1h", "period: 2h"))
	e.run(t, "quota", "status", "-json").wantOut(t, "quota_exhausted", "2026-11-03T12:00:00Z")
	e.run(t, "fx", "fetch", "-url", url).wantErr(t, "2026-11-03T12:00:00Z")
	r := runWith(t, runOptions{cwd: e.dir, client: e.client, now: func() time.Time { return testNow.Add(time.Hour) }}, "-config-dir", e.dir, "quota", "status", "-json")
	r.wantCode(t, exitOK)
	r.wantOut(t, "quota_exhausted", "2026-11-03T12:00:00Z")
}
