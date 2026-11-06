package tui

import (
	"testing"
	"time"
)

// A full catalogue (about 12,000 listed securities) must rebuild while a query
// is being typed without the keys feeling slow.
func TestCatalogueOfTwelveThousandRebuildsQuickly(t *testing.T) {
	h := newHarness(t)
	h.bigCatalogue(12000)
	h.press("c")
	start := time.Now()
	for _, q := range []string{"/", "c", "o", "m", "p"} {
		h.press(q)
	}
	per := time.Since(start) / 5
	if h.m.vp.Len() == 0 {
		t.Fatal("no rows")
	}
	if per > 150*time.Millisecond {
		t.Errorf("a keystroke takes %v on 12,000 companies, want under 150ms", per)
	}
	t.Logf("%v per keystroke, %d rows", per, h.m.vp.Len())
}
