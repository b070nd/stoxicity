package tenant

import "testing"

func TestResolve(t *testing.T) {
	cases := []struct{ flag, env, want string }{
		{"a", "b", "a"},
		{"", "b", "b"},
		{"", "", Default},
	}
	for _, c := range cases {
		if got := Resolve(c.flag, c.env, Default); got != c.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", c.flag, c.env, got, c.want)
		}
	}
}

func TestValid(t *testing.T) {
	if !(Tenant{ID: "acme-desk"}).Valid() {
		t.Error("acme-desk should be valid")
	}
	if (Tenant{ID: "../etc"}).Valid() {
		t.Error("a path-like ID must be rejected")
	}
}
