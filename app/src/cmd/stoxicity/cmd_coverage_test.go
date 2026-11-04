package main

import (
	"bytes"
	"github.com/b070nd/stoxicity/app/src/pages"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/coverage"
	"github.com/b070nd/stoxicity/app/src/model"
)

func TestCoverageAndSourceEvalOnFetchedNews(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "fetch").wantCode(t, exitOK)

	r := e.run(t, "coverage", "-all")
	r.wantCode(t, exitOK)
	r.wantOut(t, "2 stories", "OUTLETS", "Acme Corp announces record earnings", "news.example.com")

	r = e.run(t, "coverage", "-all", "-backing", "primary")
	r.wantOut(t, "0 stories", "No stories match")

	r = e.run(t, "sources", "eval")
	r.wantCode(t, exitOK)
	r.wantOut(t, "news-feed", "SHARED", "2 articles in 2 stories")
}

func TestCoverageRefusesABadBacking(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "coverage", "-backing", "rumour")
	if r.code != exitUsage {
		t.Errorf("exit %d, want the usage code", r.code)
	}
}

func TestRenderCoverageMarksThinStories(t *testing.T) {
	loc := time.UTC
	p := pages.NewParams(time.Date(2026, 11, 3, 12, 0, 0, 0, loc), 7, 5, 0.5, loc)
	lead := model.Article{ID: "a", Domain: "wire.example", Title: "Acme warns on margins", Published: time.Date(2026, 11, 3, 9, 0, 0, 0, loc)}
	stories := []coverage.Story{{
		ID: "s", Lead: lead, Outlets: []string{"wire.example"}, Backing: coverage.BackingUnconfirmed,
		Thin: true, Materiality: 4, Tickers: []string{"ACME"},
	}}
	var b bytes.Buffer
	renderCoverage(&b, stories, p, "default", 5)
	out := b.String()
	for _, want := range []string{"1 stories: 0 primary, 0 corroborated, 1 unconfirmed (1 thin)", "unconfirmed [thin]", "Acme warns on margins (ACME)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestFilterStories(t *testing.T) {
	stories := []coverage.Story{
		{ID: "a", Relevance: 0.9, Backing: coverage.BackingPrimary},
		{ID: "b", Relevance: 0.2, Backing: coverage.BackingPrimary},
		{ID: "c", Relevance: 0.9, Backing: coverage.BackingUnconfirmed, Thin: true},
	}
	ids := func(ss []coverage.Story) string {
		var out []string
		for _, s := range ss {
			out = append(out, s.ID)
		}
		return strings.Join(out, "")
	}
	for name, tc := range map[string]struct {
		all     bool
		backing coverage.Backing
		thin    bool
		want    string
	}{
		"relevant only":   {false, "", false, "ac"},
		"all":             {true, "", false, "abc"},
		"primary":         {false, coverage.BackingPrimary, false, "a"},
		"thin":            {false, "", true, "c"},
		"all and primary": {true, coverage.BackingPrimary, false, "ab"},
	} {
		if got := ids(filterStories(stories, 0.5, tc.all, tc.backing, tc.thin)); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
