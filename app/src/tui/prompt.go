package tui

import (
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// PromptEvent is what a key did to the prompt.
type PromptEvent int

const (
	// Editing: the text or the cursor changed (or the key meant nothing); the
	// prompt stays open.
	Editing PromptEvent = iota
	// Submitted: enter. The caller decides whether the text is acceptable; if it
	// is, it calls Remember and closes the prompt, and if not it leaves the
	// prompt as it is.
	Submitted
	// Cancelled: esc.
	Cancelled
	// PassedOn: a key the prompt leaves to the screen behind it (the movement
	// keys of a prompt opened with passNav).
	PassedOn
)

// historyLimit is how many entries each kind of prompt remembers.
const historyLimit = 50

// Prompt is the one line the user types into: the filter bar, the tag and list
// prompts, the jump. It owns the text, the cursor inside it and the history of
// each kind of prompt, so every prompt edits the same way: the cursor moves with
// the arrows, backspace and delete work, ctrl+w, ctrl+u and ctrl+k cut a word,
// the start and the end of the line, and up and down recall what was typed
// before. What the text means, and what submitting it does, is the caller's.
type Prompt struct {
	kind  string
	label string
	hint  string
	text  []rune
	pos   int
	// passNav: up, down, page up, page down, home and end are not for the
	// prompt; they move the cursor of the screen behind it.
	passNav bool

	history map[string][]string
	recall  int    // index into the kind's history while recalling, else -1
	draft   string // what was typed before recalling began
}

// Open starts a prompt of the given kind (which names its history), with the
// label and hint it draws and the text it starts with, the cursor at the end.
func (p *Prompt) Open(kind, label, hint, initial string, passNav bool) {
	p.kind, p.label, p.hint, p.passNav = kind, label, hint, passNav
	p.text = []rune(initial)
	p.pos = len(p.text)
	p.recall, p.draft = -1, ""
}

// Text is what has been typed.
func (p *Prompt) Text() string { return string(p.text) }

// Set replaces the text, the cursor at the end.
func (p *Prompt) Set(s string) {
	p.text = []rune(s)
	p.pos = len(p.text)
}

// Remember puts the text in the history of the prompt's kind, unless it is
// empty or the same as the latest entry.
func (p *Prompt) Remember() {
	s := strings.TrimSpace(p.Text())
	if s == "" {
		return
	}
	if p.history == nil {
		p.history = map[string][]string{}
	}
	h := p.history[p.kind]
	if len(h) == 0 || h[len(h)-1] != s {
		h = append(h, s)
		if len(h) > historyLimit {
			h = h[len(h)-historyLimit:]
		}
	}
	p.history[p.kind] = h
}

// Line is the prompt as drawn: the label, the text with the cursor in it, the
// hint.
func (p *Prompt) Line() string {
	line := p.label + string(p.text[:p.pos]) + "█" + string(p.text[p.pos:])
	if p.hint != "" {
		line += "  (" + p.hint + ")"
	}
	return line
}

// Key edits the text with one key press.
func (p *Prompt) Key(k tea.KeyPressMsg) PromptEvent {
	switch s := k.String(); s {
	case "enter":
		return Submitted
	case "esc", "ctrl+c":
		return Cancelled
	case "up", "down", "pgup", "pgdown", "home", "end":
		if p.passNav {
			return PassedOn
		}
		switch s {
		case "up":
			p.older()
		case "down":
			p.newer()
		case "home":
			p.pos = 0
		case "end":
			p.pos = len(p.text)
		}
	case "left", "ctrl+b":
		p.pos = max(p.pos-1, 0)
	case "right", "ctrl+f":
		p.pos = min(p.pos+1, len(p.text))
	case "ctrl+a":
		p.pos = 0
	case "ctrl+e":
		p.pos = len(p.text)
	case "backspace":
		if p.pos > 0 {
			p.text = slices.Delete(p.text, p.pos-1, p.pos)
			p.pos--
		}
	case "delete", "ctrl+d":
		if p.pos < len(p.text) {
			p.text = slices.Delete(p.text, p.pos, p.pos+1)
		}
	case "ctrl+u":
		p.text, p.pos = slices.Delete(p.text, 0, p.pos), 0
	case "ctrl+k":
		p.text = p.text[:p.pos]
	case "ctrl+w":
		from := p.pos
		for from > 0 && unicode.IsSpace(p.text[from-1]) {
			from--
		}
		for from > 0 && !unicode.IsSpace(p.text[from-1]) {
			from--
		}
		p.text = slices.Delete(p.text, from, p.pos)
		p.pos = from
	default:
		if t := k.Key().Text; t != "" {
			in := []rune(t)
			p.text = slices.Insert(p.text, p.pos, in...)
			p.pos += len(in)
		}
	}
	return Editing
}

// older recalls the previous entry of the history, the first press keeping
// what was typed so far to come back to.
func (p *Prompt) older() {
	h := p.history[p.kind]
	if len(h) == 0 {
		return
	}
	switch {
	case p.recall < 0:
		p.draft, p.recall = p.Text(), len(h)-1
	case p.recall > 0:
		p.recall--
	}
	p.Set(h[p.recall])
}

// newer steps forward again, ending at what was being typed.
func (p *Prompt) newer() {
	h := p.history[p.kind]
	switch {
	case p.recall < 0:
		return
	case p.recall < len(h)-1:
		p.recall++
		p.Set(h[p.recall])
	default:
		p.recall = -1
		p.Set(p.draft)
	}
}
