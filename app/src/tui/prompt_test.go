package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typed(p *Prompt, keys ...string) PromptEvent {
	ev := Editing
	for _, k := range keys {
		ev = p.Key(keyMsg(k))
	}
	return ev
}

func word(p *Prompt, s string) {
	for _, r := range s {
		p.Key(keyMsg(string(r)))
	}
}

func TestPromptEditsInsideTheLine(t *testing.T) {
	var p Prompt
	p.Open("t", "> ", "", "", false)
	word(&p, "helo")
	typed(&p, "left")
	word(&p, "l")
	if p.Text() != "hello" {
		t.Fatalf("insert at the cursor: %q", p.Text())
	}
	typed(&p, "home", "delete")
	if p.Text() != "ello" {
		t.Errorf("delete: %q", p.Text())
	}
	typed(&p, "end", "backspace")
	if p.Text() != "ell" {
		t.Errorf("backspace: %q", p.Text())
	}
	if got := p.Line(); got != "> ell█" {
		t.Errorf("line = %q", got)
	}
	typed(&p, "left", "left")
	if got := p.Line(); got != "> e█ll" {
		t.Errorf("cursor drawn inside the text: %q", got)
	}
}

func TestPromptCutsWordsAndLines(t *testing.T) {
	var p Prompt
	p.Open("t", "", "", "exchange:nyse tag:moat", false)
	typed(&p, "ctrl+w")
	if p.Text() != "exchange:nyse " {
		t.Errorf("ctrl+w cuts the last word: %q", p.Text())
	}
	typed(&p, "ctrl+w")
	if p.Text() != "" {
		t.Errorf("and the one before: %q", p.Text())
	}
	p.Set("abc def")
	typed(&p, "ctrl+a", "right", "right", "right", "right", "ctrl+k")
	if p.Text() != "abc " {
		t.Errorf("ctrl+k cuts to the end: %q", p.Text())
	}
	typed(&p, "ctrl+u")
	if p.Text() != "" {
		t.Errorf("ctrl+u cuts to the start: %q", p.Text())
	}
}

func TestPromptEndsWithEnterOrEscape(t *testing.T) {
	var p Prompt
	p.Open("t", "", "", "x", false)
	if ev := typed(&p, "enter"); ev != Submitted {
		t.Errorf("enter: %v", ev)
	}
	if ev := typed(&p, "esc"); ev != Cancelled {
		t.Errorf("esc: %v", ev)
	}
	if ev := p.Key(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); ev != Cancelled {
		t.Errorf("ctrl+c: %v", ev)
	}
}

func TestPromptRecallsEarlierEntriesOfItsOwnKind(t *testing.T) {
	var p Prompt
	for _, q := range []string{"one", "two", "two"} {
		p.Open("filter", "", "", q, false)
		p.Remember()
	}
	p.Open("tag", "", "", "moat", false)
	p.Remember()

	p.Open("filter", "", "", "", false)
	word(&p, "dra")
	typed(&p, "up")
	if p.Text() != "two" {
		t.Errorf("up recalls the latest: %q", p.Text())
	}
	typed(&p, "up")
	if p.Text() != "one" {
		t.Errorf("and the one before (a repeat was kept once): %q", p.Text())
	}
	typed(&p, "up")
	if p.Text() != "one" {
		t.Errorf("up stops at the oldest: %q", p.Text())
	}
	typed(&p, "down", "down")
	if p.Text() != "dra" {
		t.Errorf("down comes back to what was being typed: %q", p.Text())
	}
	p.Open("tag", "", "", "", false)
	typed(&p, "up")
	if p.Text() != "moat" {
		t.Errorf("tags have a history of their own: %q", p.Text())
	}
}

func TestPromptLeavesMovementKeysToTheScreenWhenAsked(t *testing.T) {
	var p Prompt
	p.Open("jump", "", "", "ab", true)
	for _, k := range []string{"up", "down", "pgup", "pgdown", "home", "end"} {
		if ev := typed(&p, k); ev != PassedOn {
			t.Errorf("%s: %v, want the key passed on", k, ev)
		}
	}
	if p.Text() != "ab" {
		t.Errorf("text changed: %q", p.Text())
	}
}

func TestEmptyAndRepeatedEntriesAreNotRemembered(t *testing.T) {
	var p Prompt
	p.Open("t", "", "", "  ", false)
	p.Remember()
	if len(p.history["t"]) != 0 {
		t.Error("blank entry kept")
	}
	for range historyLimit + 10 {
		p.Open("t", "", "", "x", false)
		p.Remember()
		p.Set("y")
		p.Remember()
	}
	if n := len(p.history["t"]); n != historyLimit {
		t.Errorf("history holds %d entries, want it capped at %d", n, historyLimit)
	}
}
