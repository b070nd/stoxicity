package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// writeLensConfig replaces config.yaml of the test environment with one whose
// lens boosts the given word, and returns the lens version that config makes.
func writeLensConfig(t *testing.T, e *testEnv, boost string) string {
	t.Helper()
	body := fmt.Sprintf("since: 2026-11-01\ndata_dir: ./data\nwatchlist: [ACME]\nlens:\n  threshold: 0.5\n  boost: [%s]\n  demote: []\n", boost)
	if err := os.WriteFile(filepath.Join(e.dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	return lens.New(cfg.Lens, lens.Options{}).Version()
}

// storedScore reads the latest score of an article straight from the database.
func storedScore(t *testing.T, e *testEnv, id string) model.Score {
	t.Helper()
	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := db.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a.Score
}

func TestRescoreAddsNewLensVersion(t *testing.T) {
	e := newTestEnv(t)
	addOtherTenant(t, e)
	versionA := writeLensConfig(t, e, "alpha")

	article := func(id, source, domain string, published int, day int) model.Article {
		a := scoredArticle(id, source, domain, utc(day, published, 0), 0.5, 3, model.EventResults, nil, nil)
		a.Title = "Alpha and beta results of the company"
		return a
	}
	old := article("r3", "news-feed", "news.example.com", 8, 1)
	old.Published = utc(3, 8, 0).AddDate(0, 0, -33)
	savePageArticles(t, e,
		article("r1", "news-feed", "news.example.com", 8, 3),
		article("r2", "news-feed", "news.example.com", 12, 2),
		old,
		article("rp", "private-feed", "private.example.com", 7, 3),
	)
	if v := storedScore(t, e, "r1").LensVersion; v != "v1" {
		t.Fatalf("seeded version %q", v)
	}

	// The base is shared, so the count includes the other tenant's source.
	r := e.run(t, "rescore")
	r.wantCode(t, exitOK)
	r.wantOut(t, "rescored 3 articles published from 2026-10-05 to 2026-11-03 (Europe/Berlin)", "lens version in force: "+versionA)
	r.wantNotOut(t, "private", "rp")
	s := storedScore(t, e, "r1")
	if s.LensVersion != versionA {
		t.Errorf("r1 has version %q, want %q", s.LensVersion, versionA)
	}
	if s.Relevance == 0.5 {
		t.Errorf("r1 relevance %v: the new lens did not score it", s.Relevance)
	}
	if v := storedScore(t, e, "r3").LensVersion; v != "v1" {
		t.Errorf("r3 is older than 30 days and must keep version v1, has %q", v)
	}

	// A different lens gives a different version, and the range limits what is scored.
	versionB := writeLensConfig(t, e, "beta")
	if versionA == versionB {
		t.Fatal("the two lens configs have the same version")
	}
	r = e.run(t, "rescore", "-from", "2026-11-03", "-to", "2026-11-03")
	r.wantCode(t, exitOK)
	r.wantOut(t, "rescored 2 articles published from 2026-11-03 to 2026-11-03", "lens version in force: "+versionB)
	if s := storedScore(t, e, "r1"); s.LensVersion != versionB {
		t.Errorf("r1 has version %q, want %q", s.LensVersion, versionB)
	}
	if v := storedScore(t, e, "r2").LensVersion; v != versionA {
		t.Errorf("r2 is outside the range and must keep %q, has %q", versionA, v)
	}

	r = e.run(t, "rescore", "-days", "40")
	r.wantCode(t, exitOK)
	r.wantOut(t, "rescored 4 articles")
	if v := storedScore(t, e, "r3").LensVersion; v != versionB {
		t.Errorf("r3 has version %q after rescoring 40 days, want %q", v, versionB)
	}
}

func TestRescoreWithoutArticles(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "rescore", "-days", "3")
	r.wantCode(t, exitOK)
	r.wantOut(t, "rescored 0 articles published from 2026-11-01 to 2026-11-03")
}
