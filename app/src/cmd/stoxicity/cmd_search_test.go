package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// seedLicenceMix adds a personal source owned by another tenant to the test
// environment and stores 3 older articles of the visible source "news-feed"
// and 1,200 newer ones of that private source, all with the word "zebra".
func seedLicenceMix(t *testing.T, e *testEnv) {
	t.Helper()
	dir := filepath.Join(e.dir, "sources")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	private := fmt.Sprintf(`sources:
  - id: private-feed
    name: Someone else's feed
    domain: private.example.com
    kind: rss
    url: %s/private.xml
    tier: 2
    licence: personal
    owner: other
    enabled: true
`, e.server.URL)
	if err := os.WriteFile(filepath.Join(dir, "private.yaml"), []byte(private), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var as []model.Article
	for i := 0; i < 1200; i++ {
		id := fmt.Sprintf("secret-%04d", i)
		as = append(as, model.Article{
			ID: id, SourceID: "private-feed", Domain: "private.example.com", URL: "https://private.example.com/" + id,
			Title: "Zebra secret " + id, Published: testNow.Add(-time.Duration(i) * time.Second),
		})
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("open-%d", i)
		as = append(as, model.Article{
			ID: id, SourceID: "news-feed", Domain: "news.example.com", URL: "https://news.example.com/" + id,
			Title: "Zebra open " + id, Published: testNow.Add(-time.Hour - time.Duration(i)*time.Minute),
		})
	}
	if err := db.Save(context.Background(), as); err != nil {
		t.Fatal(err)
	}
}

func TestSearchHidesOtherTenantsSourcesWithoutStarving(t *testing.T) {
	e := newTestEnv(t)
	seedLicenceMix(t, e)

	for _, query := range [][]string{{"zebra"}, {"site:news.example.com", "-nothing"}, {"zebra", "-nothing"}} {
		r := e.run(t, append([]string{"search"}, query...)...)
		r.wantCode(t, exitOK)
		if n := strings.Count(r.out, "Zebra open"); n != 3 {
			t.Errorf("search %v printed %d visible articles, want 3:\n%s", query, n, r.out)
		}
		r.wantNotOut(t, "secret", "private.example.com")
		if r.errOut != "" {
			t.Errorf("search %v wrote to stderr:\n%s", query, r.errOut)
		}
	}

	r := e.run(t, "digest", "-day", "2026-11-03")
	r.wantCode(t, exitOK)
	r.wantOut(t, "3 articles")
	r.wantNotOut(t, "secret", "private.example.com")
}

func TestSearchOwnerSeesPersonalSource(t *testing.T) {
	e := newTestEnv(t)
	seedLicenceMix(t, e)
	if err := os.MkdirAll(filepath.Join(e.dir, "tenants", "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "tenants", "other", "tenant.yaml"), []byte("id: other\nname: Other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.run(t, "-tenant", "other", "search", "-limit", "5", "zebra")
	r.wantCode(t, exitOK)
	if n := strings.Count(r.out, "Zebra secret"); n != 5 {
		t.Errorf("owner saw %d private articles, want 5:\n%s", n, r.out)
	}
}
