package tui

import "testing"

func TestNoKeyBoundTwice(t *testing.T) {
	seen := map[string]Action{}
	for _, b := range DefaultKeys {
		if len(b.Keys) == 0 {
			t.Errorf("%s has no keys", b.Action)
		}
		for _, k := range b.Keys {
			if prev, dup := seen[k]; dup {
				t.Errorf("key %q is bound to both %s and %s", k, prev, b.Action)
			}
			seen[k] = b.Action
		}
	}
}

func TestWatchSwitchingIsOneKey(t *testing.T) {
	for key, want := range map[string]Action{"tab": NextWatch, "shift+tab": PrevWatch, "3": JumpWatch} {
		b, ok := Lookup(key)
		if !ok || b.Action != want {
			t.Errorf("Lookup(%q) = %+v, %v; want %s", key, b, ok, want)
		}
	}
	if _, ok := Lookup("no-such-key"); ok {
		t.Error("Lookup of an unbound key should fail")
	}
}
