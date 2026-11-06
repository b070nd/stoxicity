package tui

import (
	"testing"

	"github.com/b070nd/stoxicity/app/src/nav"
)

// rowsWithHeadings is n rows where every fifth is a heading nothing can select.
func rowsWithHeadings(n int) []Row {
	rows := make([]Row, n)
	for i := range rows {
		if i%5 != 0 {
			rows[i] = Row{Text: "row", Go: &nav.Route{Kind: nav.Home}}
		}
	}
	return rows
}

func TestViewportSkipsRowsThatCannotHoldTheCursor(t *testing.T) {
	var v Viewport
	v.Resize(6)
	v.Reset(rowsWithHeadings(20), 0)
	if at, _ := v.Row(); at != 1 {
		t.Fatalf("first selectable row = %d, want 1", at)
	}
	v.Move(4) // rows 1..4, then 6
	if at, _ := v.Row(); at != 6 {
		t.Errorf("row after 4 moves = %d, want 6 (the heading at 5 is skipped)", at)
	}
	if got := v.Len(); got != 16 {
		t.Errorf("Len = %d, want 16", got)
	}
}

func TestViewportKeepsTheCursorInTheWindow(t *testing.T) {
	var v Viewport
	v.Resize(6)
	v.Reset(rowsWithHeadings(100), 0)
	check := func(step string) {
		t.Helper()
		at, _ := v.Row()
		from, to := v.Window()
		if at < from || at >= to {
			t.Fatalf("%s: cursor row %d outside window [%d,%d)", step, at, from, to)
		}
	}
	for i := 0; i < 60; i++ {
		v.Move(1)
		check("down")
	}
	v.Page(1)
	check("page down")
	v.End()
	check("end")
	v.Resize(3) // a smaller window must keep the cursor
	check("resize")
	v.Page(-1)
	check("page up")
	v.Home()
	if from, _ := v.Window(); from != 0 {
		t.Errorf("on the first row the heading above it must show: window starts at %d", from)
	}
}

func TestViewportClampsAndHandlesNothingToSelect(t *testing.T) {
	var v Viewport
	v.Resize(4)
	v.Reset(nil, 7)
	v.Move(3)
	v.Page(1)
	v.End()
	if _, ok := v.Row(); ok {
		t.Error("an empty viewport has no cursor row")
	}
	v.Reset(rowsWithHeadings(12), 99)
	if v.Cursor() != v.Len()-1 {
		t.Errorf("a remembered cursor past the end is clamped: %d of %d", v.Cursor(), v.Len())
	}
	if got := v.IndexOf(5); got != -1 {
		t.Errorf("IndexOf(heading) = %d, want -1", got)
	}
	if got := v.IndexOf(6); got != 4 {
		t.Errorf("IndexOf(6) = %d, want 4", got)
	}
}
