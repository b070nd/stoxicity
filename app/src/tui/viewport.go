package tui

// Viewport is a window of a fixed height onto the rows of a screen, with a
// cursor that rests on the selectable ones. It is the only place that knows how
// the cursor and the scroll position go together: the cursor row is always in
// the window, a move never leaves it outside, and a resize keeps it in.
//
// The rows are not copied; the Viewport holds only which of them are
// selectable. The caller reads Window to know which rows to draw and Row to
// know which of them carries the cursor.
type Viewport struct {
	sel    []int // indices of the selectable rows
	rows   int   // how many rows there are
	cursor int   // index into sel
	top    int   // first row in the window
	height int   // rows the window shows (the screen's title is not one of them)
}

// Reset starts over on a new set of rows, with the cursor on the cursor-th
// selectable one (clamped to the last). The window starts at the first row,
// so the heading above the first selectable row stays in view.
func (v *Viewport) Reset(rows []Row, cursor int) {
	v.sel = v.sel[:0]
	for i, r := range rows {
		if r.Selectable() {
			v.sel = append(v.sel, i)
		}
	}
	v.rows = len(rows)
	v.cursor = min(max(cursor, 0), max(len(v.sel)-1, 0))
	v.top = 0
	v.follow()
}

// Reload replaces the rows with a new set that is mostly the old one (a
// refresh, a re-sort): the cursor index is kept (clamped) and the window stays
// where it was as far as it can, so the screen does not jump.
func (v *Viewport) Reload(rows []Row, cursor int) {
	top := v.top
	v.Reset(rows, cursor)
	v.top = min(top, max(v.rows-1, 0))
	v.follow()
}

// Clear empties the viewport, as for a screen that is still loading.
func (v *Viewport) Clear() { v.Reset(nil, 0) }

// Resize sets how many rows the window shows.
func (v *Viewport) Resize(height int) {
	v.height = max(height, 1)
	v.follow()
}

// Move moves the cursor by n selectable rows, stopping at the ends.
func (v *Viewport) Move(n int) { v.Select(v.cursor + n) }

// Page moves the cursor by one window of selectable rows: dir -1 up, 1 down.
func (v *Viewport) Page(dir int) { v.Move(dir * v.height) }

// Home puts the cursor on the first selectable row.
func (v *Viewport) Home() { v.Select(0) }

// End puts the cursor on the last selectable row.
func (v *Viewport) End() { v.Select(len(v.sel) - 1) }

// Select puts the cursor on the i-th selectable row (clamped) and scrolls so
// that it shows.
func (v *Viewport) Select(i int) {
	if len(v.sel) == 0 {
		return
	}
	v.cursor = min(max(i, 0), len(v.sel)-1)
	v.follow()
}

// Len is how many rows can hold the cursor.
func (v *Viewport) Len() int { return len(v.sel) }

// Cursor is the index of the cursor among the selectable rows.
func (v *Viewport) Cursor() int { return v.cursor }

// At is the row index of the i-th selectable row.
func (v *Viewport) At(i int) int { return v.sel[i] }

// IndexOf is the position among the selectable rows of row index at, or -1 if
// that row cannot hold the cursor.
func (v *Viewport) IndexOf(at int) int {
	for i, s := range v.sel {
		if s == at {
			return i
		}
	}
	return -1
}

// Row is the row index the cursor is on.
func (v *Viewport) Row() (int, bool) {
	if v.cursor < 0 || v.cursor >= len(v.sel) {
		return 0, false
	}
	return v.sel[v.cursor], true
}

// Window is the rows to draw: from the first up to, not including, the second.
func (v *Viewport) Window() (from, to int) {
	return v.top, min(v.top+v.height, v.rows)
}

// Top is the first row of the window.
func (v *Viewport) Top() int { return v.top }

// follow scrolls so that the cursor row is in the window. On the first
// selectable row the window goes to the very top, so the heading above it
// shows.
func (v *Viewport) follow() {
	at := 0
	if len(v.sel) > 0 && v.cursor > 0 {
		at = v.sel[v.cursor]
	}
	switch {
	case at < v.top:
		v.top = at
	case v.height > 0 && at >= v.top+v.height:
		v.top = at - v.height + 1
	}
	v.top = max(v.top, 0)
}
