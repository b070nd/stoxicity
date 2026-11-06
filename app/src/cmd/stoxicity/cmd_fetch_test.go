package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// writeWatch writes a watch file for the default tenant of a test environment.
func (e *testEnv) writeWatch(t *testing.T, id, yaml string) {
	t.Helper()
	path := filepath.Join(e.dir, "tenants", "default", "watches", id+".yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// markdownFiles lists the .md files in dir with their content.
func markdownFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

const fileAlertWatch = `id: all-relevant
name: Everything relevant
alerts:
  - name: any
    channels: [file]
`

func TestFetchDeliversAlertToFileChannelOnce(t *testing.T) {
	e := newTestEnv(t)
	out := t.TempDir()
	t.Setenv("STOXICITY_FILE_DIR", out)
	t.Setenv("STOXICITY_FEED_PATH", "")
	t.Setenv("STOXICITY_WEBHOOK_URL", "")
	e.writeWatch(t, "all-relevant", fileAlertWatch)

	r := e.run(t, "fetch")
	r.wantCode(t, exitOK)
	r.wantOut(t, "news-feed", "2 new")
	if r.errOut != "" {
		t.Errorf("stderr:\n%s", r.errOut)
	}
	first := markdownFiles(t, out)
	if len(first) != 1 {
		t.Fatalf("%d Markdown files after the first fetch, want 1: %v", len(first), first)
	}
	for name, body := range first {
		if !strings.Contains(name, "20261103-100000-default-all-relevant-") {
			t.Errorf("file name %q, want the detection time and the watch in it", name)
		}
		for _, want := range []string{"# Everything relevant: any (1)", "- Alert: any", "Acme Corp announces record earnings", "https://news.example.com/acme-earnings"} {
			if !strings.Contains(body, want) {
				t.Errorf("file lacks %q:\n%s", want, body)
			}
		}
		if strings.Contains(body, "Federal Reserve") {
			t.Errorf("file holds an irrelevant article:\n%s", body)
		}
	}

	// A second fetch finds the same articles, records nothing new and sends
	// nothing.
	r = e.run(t, "fetch")
	r.wantCode(t, exitOK)
	r.wantOut(t, "news-feed", "0 new")
	again := markdownFiles(t, out)
	if len(again) != 1 {
		t.Fatalf("%d Markdown files after the second fetch, want still 1: %v", len(again), again)
	}
	for name, body := range first {
		if again[name] != body {
			t.Errorf("file %q changed on the second fetch", name)
		}
	}

	// The hit is unread for the user, who has not looked yet.
	r = e.run(t, "watch", "list")
	r.wantCode(t, exitOK)
	if !regexp.MustCompile(`all-relevant\s+Everything relevant\s+1\s`).MatchString(r.out) {
		t.Errorf("watch list does not show one unread hit:\n%s", r.out)
	}
}

func TestFetchReportsUnbuildableChannelOnceAndGoesOn(t *testing.T) {
	e := newTestEnv(t)
	out := t.TempDir()
	t.Setenv("STOXICITY_FILE_DIR", out)
	t.Setenv("STOXICITY_WEBHOOK_URL", "")
	e.writeWatch(t, "all-relevant", `id: all-relevant
name: Everything relevant
alerts:
  - name: any
    channels: [file, webhook]
`)

	r := e.run(t, "fetch")
	r.wantCode(t, exitOK)
	if n := strings.Count(r.errOut, "STOXICITY_WEBHOOK_URL"); n != 1 {
		t.Errorf("stderr names STOXICITY_WEBHOOK_URL %d times, want once:\n%s", n, r.errOut)
	}
	if got := markdownFiles(t, out); len(got) != 1 {
		t.Errorf("%d files, want the file channel to work although the webhook does not: %v", len(got), got)
	}
}

func TestFetchWithoutAlertsBuildsNoChannels(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("STOXICITY_FILE_DIR", "")
	t.Setenv("STOXICITY_WEBHOOK_URL", "")
	r := e.run(t, "fetch")
	r.wantCode(t, exitOK)
	if r.errOut != "" {
		t.Errorf("stderr:\n%s", r.errOut)
	}
}

func TestFetchFileDirStartsAtTheFolderOfTheEnvFile(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("STOXICITY_WEBHOOK_URL", "")
	t.Setenv("STOXICITY_FILE_DIR", "alerts")
	e.writeWatch(t, "all-relevant", fileAlertWatch)
	r := e.run(t, "fetch")
	r.wantCode(t, exitOK)
	if got := markdownFiles(t, filepath.Join(e.dir, "alerts")); len(got) != 1 {
		t.Errorf("%d files in alerts/ under the configuration folder, want 1\nstderr:\n%s", len(got), r.errOut)
	}
}

func TestRunSendsDueDigestsOnTheTick(t *testing.T) {
	e := newTestEnv(t)
	out := t.TempDir()
	t.Setenv("STOXICITY_FILE_DIR", out)
	t.Setenv("STOXICITY_WEBHOOK_URL", "")
	e.writeWatch(t, "all-relevant", `id: all-relevant
name: Everything relevant
digest:
  cadence: daily
  at: "00:00"
  channels: [file]
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr syncBuffer
	codes := make(chan int, 1)
	go func() {
		codes <- run(ctx, []string{"-config-dir", e.dir, "run"}, &stdout, &stderr, runOptions{
			cwd: e.dir, client: e.client, now: func() time.Time { return testNow },
			rand: func() float64 { return 0 }, tick: 20 * time.Millisecond,
		})
	}()
	deadline := time.After(10 * time.Second)
	for len(markdownFiles(t, out)) == 0 {
		select {
		case <-deadline:
			t.Fatalf("no digest within 10s\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		case code := <-codes:
			t.Fatalf("run ended early with %d\nstderr:\n%s", code, stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	time.Sleep(200 * time.Millisecond) // many more ticks
	cancel()
	if code := <-codes; code != exitOK {
		t.Errorf("exit code %d\nstderr:\n%s", code, stderr.String())
	}
	files := markdownFiles(t, out)
	if len(files) != 1 {
		t.Fatalf("%d files, want one digest in spite of many ticks: %v", len(files), files)
	}
	for _, body := range files {
		for _, want := range []string{"# Everything relevant: daily digest 2026-11-03", "- Alert: digest", "Digest for 2026-11-02 to 2026-11-03 (Europe/Berlin), tenant default"} {
			if !strings.Contains(body, want) {
				t.Errorf("digest lacks %q:\n%s", want, body)
			}
		}
	}
}

func TestSourceHostGroupsTheSECHostsAndLowercases(t *testing.T) {
	for url, want := range map[string]string{
		"https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent": "sec",
		"https://data.sec.gov/submissions/CIK0000320193.json":        "sec",
		"https://WWW.FederalReserve.gov/feeds/press_all.xml":         "www.federalreserve.gov",
		"": "", "not a url": "",
	} {
		if got := sourceHost(model.Source{URL: url}); got != want {
			t.Errorf("sourceHost(%q) = %q, want %q", url, got, want)
		}
	}
}
