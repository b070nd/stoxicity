package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func (r result) wantCode(t *testing.T, want int) {
	t.Helper()
	if r.code != want {
		t.Fatalf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, want, r.out, r.errOut)
	}
}

func (r result) wantOut(t *testing.T, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(r.out, p) {
			t.Errorf("stdout lacks %q:\n%s", p, r.out)
		}
	}
}

func (r result) wantNotOut(t *testing.T, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(r.out, p) {
			t.Errorf("stdout has %q:\n%s", p, r.out)
		}
	}
}

func TestFetchSearchDigest(t *testing.T) {
	e := newTestEnv(t)

	r := e.run(t, "fetch")
	r.wantCode(t, exitOK)
	r.wantOut(t, "news-feed", "2 new")

	r = e.run(t, "fetch", "news-feed")
	r.wantCode(t, exitOK)
	r.wantOut(t, "news-feed", "0 new")

	r = e.run(t, "search", "earnings")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Acme Corp announces record earnings", "news.example.com", "https://news.example.com/acme-earnings", "2026-11-03 09:00", "\tresults\t")
	r.wantNotOut(t, "Federal Reserve")

	r = e.run(t, "search", "site:news.example.com", "-buyback")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Federal Reserve")
	r.wantNotOut(t, "Acme Corp")

	r = e.run(t, "search", "-limit", "1", "site:news.example.com")
	r.wantCode(t, exitOK)
	if n := strings.Count(r.out, "\n"); n != 1 {
		t.Errorf("-limit 1 printed %d lines:\n%s", n, r.out)
	}

	r = e.run(t, "search", "-from", "2026-11-04", "earnings")
	r.wantCode(t, exitOK)
	if r.out != "" {
		t.Errorf("search after the articles' day printed:\n%s", r.out)
	}

	r = e.run(t, "search", "ticker:ACME")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Acme Corp announces")
	r.wantNotOut(t, "Federal Reserve")

	r = e.run(t, "digest", "-day", "2026-11-03")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Digest for 2026-11-03 (Europe/Berlin)", "tenant default", "Acme Corp announces record earnings", "news.example.com")

	r = e.run(t, "digest")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Digest for 2026-11-03")

	r = e.run(t, "digest", "-day", "2026-11-02")
	r.wantCode(t, exitOK)
	r.wantOut(t, "0 articles")

	r = e.run(t, "sources", "list")
	r.wantCode(t, exitOK)
	r.wantOut(t, "news-feed", "broken", "GREEN", "redistributable")

	r = e.run(t, "watch", "list")
	r.wantCode(t, exitOK)
	r.wantOut(t, "all-relevant", "Everything relevant")
	// The fetch recorded one hit: only the buyback story is relevant.
	if !regexp.MustCompile(`all-relevant\s+Everything relevant\s+1\s`).MatchString(r.out) {
		t.Errorf("watch list does not show one unread hit:\n%s", r.out)
	}

	r = e.run(t, "watch", "test", "all-relevant", "-since", "48h")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Acme Corp announces record earnings", "matches in the last 48h")

	r = e.run(t, "tenant", "list")
	r.wantCode(t, exitOK)
	r.wantOut(t, "default", "all-relevant")
}

func TestFetchReportsErrorClass(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "fetch", "broken")
	r.wantCode(t, exitError)
	r.wantOut(t, "broken", "error (permanent)")
}

func TestFetchNeedsContact(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("STOXICITY_CONTACT", "")
	r := e.run(t, "fetch")
	r.wantCode(t, exitError)
	if !strings.Contains(r.errOut, "STOXICITY_CONTACT") {
		t.Errorf("stderr does not name STOXICITY_CONTACT:\n%s", r.errOut)
	}
	// Offline commands still work.
	e.run(t, "sources", "list").wantCode(t, exitOK)
	e.run(t, "tenant", "list").wantCode(t, exitOK)
	e.run(t, "search", "earnings").wantCode(t, exitOK)
}

func TestWatchCheck(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "watch", "check")
	r.wantCode(t, exitOK)
	r.wantOut(t, "ok    all-relevant")

	bad := filepath.Join(e.dir, "tenants", "default", "watches", "bad-query.yaml")
	if err := os.WriteFile(bad, []byte("id: bad-query\nname: Bad\nquery: \"ticker:\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run(t, "watch", "check")
	r.wantCode(t, exitError)
	r.wantOut(t, "ok    all-relevant", "FAIL", "bad-query")

	r = e.run(t, "watch", "check", "all-relevant")
	r.wantCode(t, exitOK)
	r.wantNotOut(t, "bad-query")
}

func TestUnknownTenant(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "-tenant", "nobody", "digest")
	r.wantCode(t, exitError)
	if !strings.Contains(r.errOut, "nobody") {
		t.Errorf("stderr does not name the tenant:\n%s", r.errOut)
	}
}

// syncBuffer is a bytes.Buffer that the program and the test may use at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunPollsUntilInterrupted(t *testing.T) {
	e := newTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut syncBuffer
	codes := make(chan int, 1)
	go func() {
		codes <- run(ctx, []string{"-config-dir", e.dir, "run"}, &out, &errOut, runOptions{
			cwd: e.dir, client: e.client, now: func() time.Time { return testNow },
			rand: func() float64 { return 0 },
		})
	}()

	deadline := time.After(10 * time.Second)
	for !strings.Contains(out.String(), "news-feed") {
		select {
		case <-deadline:
			t.Fatalf("no poll within 10s\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
		case code := <-codes:
			t.Fatalf("run ended early with %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	select {
	case code := <-codes:
		if code != exitOK {
			t.Errorf("exit code %d, want 0\nstderr:\n%s", code, errOut.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop after the context ended")
	}
	for _, want := range []string{"polling 1 sources", "11:00:00 news-feed", "2 new", "stopped"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, out.String())
		}
	}
}
