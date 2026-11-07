package tui

import (
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

func TestAnOlderAnswerForLatestNewsDoesNotReplaceANewerOne(t *testing.T) {
	h := newHarness(t)
	h.m.latestAsked["NVDA"] = 2 // two requests were made; the second is the one the screen waits for
	release := model.Article{ID: "r", Title: "Earnings release"}
	h.m.Update(latestMsg{ticker: "NVDA", gen: 2, arts: []model.Article{release}})
	h.m.Update(latestMsg{ticker: "NVDA", gen: 1, arts: nil}) // the slow one, asked before the release
	if got := h.m.latest["NVDA"]; len(got) != 1 || got[0].ID != "r" {
		t.Errorf("the release was erased by an older answer: %v", got)
	}
}
