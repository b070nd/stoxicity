package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignalsGraphAndExportRunOverTheEventHistory(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)
	e.run(t, "fetch").wantCode(t, exitOK)
	e.run(t, "events", "rebuild").wantCode(t, exitOK)

	// One publisher is an echo, however material: not corroborated, shown only with -all.
	r := e.run(t, "signals", "-min", "1", "-days", "3650")
	r.wantCode(t, exitOK)
	r.wantOut(t, "nothing corroborated")
	r = e.run(t, "signals", "-all", "-min", "1", "-days", "3650")
	r.wantCode(t, exitOK)
	r.wantOut(t, "ACME", "single publisher", "not corroborated: one publisher", "not whether the news will move the price")

	r = e.run(t, "graph", "ACME", "-days", "3650")
	r.wantCode(t, exitOK)
	r.wantOut(t, "buyback: Acme Corp announces record earnings", "[subject]", "evidence: news.example.com (1)", "evidence: not corroborated (single publisher)")
	if r := e.run(t, "graph", "ACME", "-days", "3650", "-format", "dot"); !strings.Contains(r.out, "digraph signal") {
		t.Errorf("dot: %s", r.out)
	}
	if r := e.run(t, "graph", "NOSUCH"); r.code == exitOK {
		t.Error("an unknown security is an error")
	}

	dir := filepath.Join(e.dir, "research")
	r = e.run(t, "export", "-out", dir, "-days", "3650")
	r.wantCode(t, exitOK)
	r.wantOut(t, "exported 1 securities and 1 events", "2 files written")
	events, _ := os.ReadDir(filepath.Join(dir, "events"))
	secs, _ := os.ReadDir(filepath.Join(dir, "securities"))
	if len(events) != 1 || len(secs) != 1 || !strings.HasPrefix(events[0].Name(), "evt_") || !strings.HasPrefix(secs[0].Name(), "sec_") {
		t.Fatalf("files: %v %v", events, secs)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "events", events[0].Name()))
	if !strings.Contains(string(body), "## Assessments (interpretation, not observation)") {
		t.Errorf("event file:\n%s", body)
	}
	e.run(t, "export", "-out", dir, "-days", "3650").wantOut(t, "0 files written, 2 already up to date")
}
