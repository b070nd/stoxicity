package model

import (
	"strings"
	"testing"
)

func TestValidID(t *testing.T) {
	good := []string{"default", "buffett-core", "a", "0day", strings.Repeat("a", 40)}
	bad := []string{"", "-lead", "Upper", "has space", "a/b", "..", "a.b", "under_score", strings.Repeat("a", 41)}
	for _, s := range good {
		if !ValidID(s) {
			t.Errorf("ValidID(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidID(s) {
			t.Errorf("ValidID(%q) = true, want false", s)
		}
	}
}
