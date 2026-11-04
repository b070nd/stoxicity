package tagcloud

import "testing"

func TestBuild(t *testing.T) {
	got := Build(map[string]int{"ai": 10, "energy": 6, "banks": 2, "retail": 2, "zero": 0}, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %v", len(got), got)
	}
	if got[0].Text != "ai" || got[0].Tier != Tiers {
		t.Errorf("top term = %+v, want ai at tier %d", got[0], Tiers)
	}
	if got[2].Text != "banks" || got[2].Tier != 1 {
		t.Errorf("last term = %+v, want banks at tier 1 (tie broken alphabetically)", got[2])
	}
}

func TestBuildAllEqual(t *testing.T) {
	for _, term := range Build(map[string]int{"a": 3, "b": 3}, 0) {
		if term.Tier != Tiers {
			t.Errorf("%+v: equal counts should all be top tier", term)
		}
	}
}

func TestBuildEmpty(t *testing.T) {
	if got := Build(nil, 5); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestBuildHeavyTail(t *testing.T) {
	// One dominant term must not crush the rest into the lowest tier.
	got := Build(map[string]int{"a": 100, "b": 30, "c": 10, "d": 3, "e": 1}, 0)
	want := []int{5, 4, 3, 2, 1}
	for i, term := range got {
		if term.Tier != want[i] {
			t.Errorf("%s (%d): tier %d, want %d", term.Text, term.Count, term.Tier, want[i])
		}
	}
}
