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

	// One site with no attribution is unresolved however material: hidden by default, shown with -all.
	r := e.run(t, "signals", "-days", "3650")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no security with primary or corroborated evidence")
	r.wantNotOut(t, "materiality filter")
	r = e.run(t, "signals", "-all", "-days", "3650", "-explain")
	r.wantCode(t, exitOK)
	r.wantOut(t, "ACME", "unresolved", "independence not established", "unknown [unknown:news.example.com", "not whether the news is true or will move a price")
	// An explicit materiality filter is labelled, and says how many events it removed.
	r = e.run(t, "signals", "-all", "-min", "5", "-days", "3650")
	r.wantCode(t, exitOK)
	r.wantOut(t, "materiality filter -min 5", "can be lower than with every event")
	// Judging as of a time before anything was stored finds nothing, and stores nothing.
	r = e.run(t, "signals", "-all", "-days", "3650", "-at", "2001-01-01")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no security with primary or corroborated evidence")

	r = e.run(t, "graph", "ACME", "-days", "3650")
	r.wantCode(t, exitOK)
	// The story says two things; the more important claim names the event, not the first keyword.
	r.wantOut(t, "results: Acme Corp announces record earnings", "[subject]", "buyback: Acme Corp announces", "evidence: news.example.com (2)", "evidence: unresolved: independence not established")
	if r := e.run(t, "graph", "ACME", "-days", "3650", "-format", "dot"); !strings.Contains(r.out, "digraph signal") {
		t.Errorf("dot: %s", r.out)
	}
	if r := e.run(t, "graph", "NOSUCH"); r.code == exitOK {
		t.Error("an unknown security is an error")
	}

	dir := filepath.Join(e.dir, "research")
	r = e.run(t, "export", "-out", dir, "-days", "3650")
	r.wantCode(t, exitOK)
	r.wantOut(t, "exported 1 securities and 2 events", "3 files written")
	events, _ := os.ReadDir(filepath.Join(dir, "events"))
	secs, _ := os.ReadDir(filepath.Join(dir, "securities"))
	// One story, two claims, two events; the second carries its kind in its name.
	if len(events) != 2 || len(secs) != 1 || !strings.HasPrefix(events[0].Name(), "evt_") || !strings.HasPrefix(secs[0].Name(), "sec_") || !strings.Contains(events[1].Name(), "~buyback") {
		t.Fatalf("files: %v %v", events, secs)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "events", events[0].Name()))
	if !strings.Contains(string(body), "## Assessments (interpretation, not observation)") {
		t.Errorf("event file:\n%s", body)
	}
	e.run(t, "export", "-out", dir, "-days", "3650").wantOut(t, "0 files written, 3 already up to date")
}
