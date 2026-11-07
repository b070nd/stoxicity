package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A letter must never mean one thing capitalised and another in lower case:
// that is a collision the user meets with a stuck shift key.
func TestNoKeyBoundTwiceIgnoringCase(t *testing.T) {
	seen := map[string]Action{}
	for _, b := range DefaultKeys {
		if len(b.Keys) == 0 {
			t.Errorf("%s has no keys", b.Action)
		}
		for _, k := range b.Keys {
			if k != strings.ToLower(k) {
				t.Errorf("key %q of %s is capitalised: keys are lower case", k, b.Action)
			}
			k = strings.ToLower(k)
			if prev, dup := seen[k]; dup {
				t.Errorf("key %q is bound to both %s and %s", k, prev, b.Action)
			}
			seen[k] = b.Action
		}
	}
}

// Every screen tab is reached by a letter that is in its own name, so the tab
// bar can underline it there, and the key really goes to that screen.
func TestEachScreenTabKeyIsInItsLabelAndOpensIt(t *testing.T) {
	h := newHarness(t)
	for _, tab := range screenTabs {
		if _, ok := hotKey(tab.key, tab.label); !ok {
			t.Errorf("tab %s: key %q is not a letter of the label", tab.label, tab.key)
		}
		b, ok := Lookup(tab.key)
		if !ok {
			t.Errorf("tab %s: key %q is unbound", tab.label, tab.key)
			continue
		}
		h.press("h")
		h.press(tab.key)
		if got := h.m.litTab(); got != tab.label {
			t.Errorf("%s (%s) lit %q instead", tab.key, b.Action, got)
		}
	}
}

// A key bar entry is written in the word when its key is a letter of it, and
// in front of the word when it is not; nothing is written twice.
func TestBarLabelsCarryTheirKeysInTheWord(t *testing.T) {
	for _, b := range DefaultKeys {
		if b.Bar == "" {
			continue
		}
		key := b.Keys[0]
		_, inWord := hotKey(key, b.Bar)
		if len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' && !inWord {
			t.Errorf("%s: key %q is a letter but the label %q lacks it; pick a key that is in the word", b.Action, key, b.Bar)
		}
		plain := ansi.Strip(hotLabel(key, b.Bar, styleDim, styleHot))
		want := b.Bar
		if !inWord {
			want = keyName(key) + " " + b.Bar
		}
		if plain != want {
			t.Errorf("%s: drawn %q, want %q", b.Action, plain, want)
		}
	}
}

func TestHotKeyFindsTheFirstLetterIgnoringCase(t *testing.T) {
	for _, c := range []struct {
		key, label string
		at         int
		ok         bool
	}{
		{"h", "Home", 0, true}, {"o", "Sources", 1, true}, {"x", "exchange", 1, true},
		{"1", "1 Tech", 0, true}, {"z", "Home", 0, false}, {"/", "filter", 0, false}, {"é", "été", 0, false},
	} {
		if at, ok := hotKey(c.key, c.label); ok != c.ok || (ok && at != c.at) {
			t.Errorf("hotKey(%q, %q) = %d, %v; want %d, %v", c.key, c.label, at, ok, c.at, c.ok)
		}
	}
}

func TestWatchSwitchingIsOneKey(t *testing.T) {
	for key, want := range map[string]Action{"ctrl+tab": NextWatch, "ctrl+shift+tab": PrevWatch, "3": JumpWatch, "tab": NextSection, "shift+tab": PrevSection} {
		b, ok := Lookup(key)
		if !ok || b.Action != want {
			t.Errorf("Lookup(%q) = %+v, %v; want %s", key, b, ok, want)
		}
	}
	if _, ok := Lookup("no-such-key"); ok {
		t.Error("Lookup of an unbound key should fail")
	}
}
