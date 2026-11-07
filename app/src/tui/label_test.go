package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/eval"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
)

func TestLabelScreenQueuesTheDoubtfulArticlesWithoutTheirScoresAndShowsHowTheLensDoes(t *testing.T) {
	h := newHarness(t)
	pub := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	h.be.queue = []labels.Pick{
		{Article: model.Article{ID: "q1", Title: "Acme beats estimates", Domain: "sec.gov", Published: pub, Tickers: []string{"ACME"},
			Score: model.Score{Relevance: 0.51, Materiality: 4, Kind: model.EventResults}}, Why: "close to the threshold"},
		{Article: model.Article{ID: "q2", Title: "Board changes bylaws", Domain: "sec.gov", Published: pub,
			Score: model.Score{Relevance: 0.2, Materiality: 1, Kind: model.EventBylaws}}, Sampled: true, Why: "random sample"},
	}
	var all, honest eval.Confusion
	for _, x := range [][2]bool{{true, true}, {true, true}, {true, false}, {false, true}, {false, false}} {
		all.Add(x[0], x[1])
	}
	honest.Add(true, false)
	h.be.progress = labels.Progress{Total: 5, Kept: 3, Dropped: 2, Sampled: 1, Threshold: 0.5, All: all, Honest: honest}

	h.press("a")
	if h.crumbs() != "Home > Label" {
		t.Fatalf("a opens the Label screen: %q", h.crumbs())
	}
	h.mustContain("Teaching the lens", "5 labels of the 300", "kept 3 · dropped 2 · 1 from the random sample",
		"against all labels: precision 0.667 (2/3) · recall 0.667 (2/3)",
		"against the random sample (the honest number): precision 0.000 (0/1)",
		"Judge these", "Acme beats estimates", "Board changes bylaws", "[ACME]", "results", "bylaws",
		"labels 5 of 300 · precision 0.67 · recall 0.67") // the status line
	rows := h.rows()
	if strings.Contains(rows, "M4") || strings.Contains(rows, "0.51") {
		t.Errorf("the queue must not show the lens's scores:\n%s", rows)
	}
	if !strings.Contains(rows, "° 11-04 10:00") && !strings.Contains(rows, "°") {
		t.Error("the random ones are marked")
	}

	h.selectArticleID("q1")
	h.press("+")
	if got := h.be.labelled; len(got) != 1 || got[0] != "q1:true" {
		t.Fatalf("labelled = %v", got)
	}
}

func TestLabelScreenWithNothingToJudgeAndNoLabels(t *testing.T) {
	h := newHarness(t)
	h.press("a")
	h.mustContain("0 labels of the 300", "nothing left to judge")
	if strings.Contains(h.view(), "precision 0") {
		t.Error("no labels, no measure in the status line")
	}
}

func TestProgressTextLeavesOutWhatCannotBeMeasured(t *testing.T) {
	if got := progressText(labels.Progress{Total: 3}); got != "labels 3 of 300" {
		t.Errorf("%q", got)
	}
}
