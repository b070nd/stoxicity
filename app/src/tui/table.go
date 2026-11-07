package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Sort is the order a table is shown in: by the column with this key, largest
// (or last) first when Desc. The zero Sort means the order the table's items
// came in.
type Sort struct {
	Key  string
	Desc bool
}

// Column is one column of a Table.
type Column struct {
	Title string
	// Key names the column for sorting; a column without one cannot be sorted by.
	Key string
	// Width is the cells the column takes; the last column may leave it 0 to
	// take what is left, unpadded.
	Width int
	Right bool
	// Desc: the natural order of the column is largest first (counts, times),
	// so choosing it sorts that way first.
	Desc bool
}

// Cell is the text of one cell and the value the column sorts by: Num for
// numbers and times, Str (compared without regard to case) for names. A cell
// with a Str is compared by it, any other by Num.
type Cell struct {
	Text string
	Num  float64
	Str  string
	// Style draws the cell's text in a colour of its own; Plain leaves it to
	// the row.
	Style Style
}

// Styled is c drawn in the style.
func (c Cell) Styled(s Style) Cell { c.Style = s; return c }

// Str is a cell of text that sorts as text.
func Str(s string) Cell { return Cell{Text: s, Str: strings.ToLower(s)} }

// Num is a cell that shows text and sorts as the number n.
func Num(text string, n float64) Cell { return Cell{Text: text, Num: n} }

// Item is a row of a Table: its cells, and the Row it becomes (which says what
// enter opens). The Table writes the Row's Text.
type Item struct {
	Cells []Cell
	Row   Row
}

// Table lays out items in columns, with a header, and orders them by a
// column. It is the one place that knows how a column is padded, which one is
// sorted and how that is shown, so every list screen sorts the same way.
type Table struct {
	Columns []Column
	Items   []Item
	// Default is the order the items arrive in; the header marks it when no
	// other is chosen.
	Default Sort
	// Presorted: the caller has already put the items in the order of the Sort
	// it was given (a list too long to sort here, ordered by its own module); the
	// table only marks the header.
	Presorted bool
}

// Effective is s if it names a column of the table, else the default.
func (t Table) Effective(s Sort) Sort {
	if s.Key != "" && t.column(s.Key) >= 0 {
		return s
	}
	return t.Default
}

// shape is the table without its items: what a screen keeps of it to know the
// columns and how to step along them.
func (t Table) shape() *Table {
	t.Items = nil
	return &t
}

// Keys are the sort keys of the columns, left to right.
func (t Table) Keys() []string {
	var keys []string
	for _, c := range t.Columns {
		if c.Key != "" {
			keys = append(keys, c.Key)
		}
	}
	return keys
}

func (t Table) column(key string) int {
	return slices.IndexFunc(t.Columns, func(c Column) bool { return c.Key == key })
}

// Rows is the header and then the items ordered by s.
func (t Table) Rows(s Sort) []Row {
	s = t.Effective(s)
	return append([]Row{{Text: t.header(s), Style: Heading}}, t.body(s)...)
}

// Body is the items alone, ordered by s, for a table shown inside a screen
// that has a heading of its own.
func (t Table) Body(s Sort) []Row { return t.body(t.Effective(s)) }

func (t Table) body(s Sort) []Row {
	items := t.Items
	if c := t.column(s.Key); c >= 0 && !t.Presorted {
		items = slices.Clone(items)
		slices.SortStableFunc(items, func(a, b Item) int {
			d := compare(a.Cells[c], b.Cells[c])
			if s.Desc {
				d = -d
			}
			return d
		})
	}
	rows := make([]Row, len(items))
	for i, it := range items {
		rows[i] = it.Row
		rows[i].Text, rows[i].Spans = t.line(it.Cells)
	}
	return rows
}

func compare(a, b Cell) int {
	if a.Str != "" || b.Str != "" {
		return strings.Compare(a.Str, b.Str)
	}
	switch {
	case a.Num < b.Num:
		return -1
	case a.Num > b.Num:
		return 1
	}
	return 0
}

func (t Table) line(cells []Cell) (string, []Span) {
	texts := make([]string, len(t.Columns))
	for i := range t.Columns {
		if i < len(cells) {
			texts[i] = cells[i].Text
		}
	}
	text, at := t.layout(texts)
	var spans []Span
	for i := range t.Columns {
		if i < len(cells) && cells[i].Style != Plain && at[i][1] > at[i][0] {
			spans = append(spans, Span{From: at[i][0], To: at[i][1], Style: cells[i].Style})
		}
	}
	return text, spans
}

func (t Table) header(s Sort) string {
	titles := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		titles[i] = c.Title
		if c.Key != "" && c.Key == s.Key {
			arrow := "▲"
			if s.Desc {
				arrow = "▼"
			}
			titles[i] += arrow
		}
	}
	return t.join(titles)
}

// width is the cells the column takes: its Width, and at least its title and
// the arrow that marks it as the sorted one.
func (c Column) width() int {
	if c.Width == 0 {
		return 0
	}
	if c.Key != "" {
		return max(c.Width, utf8.RuneCountInString(c.Title)+1)
	}
	return c.Width
}

// join pads the texts to their columns and puts a space between them.
func (t Table) join(texts []string) string {
	s, _ := t.layout(texts)
	return s
}

// layout is join, and where in the line (in runes, as [from, to)) each
// column's text lies, padding left out; the text of a cell that is empty has
// an empty stretch.
func (t Table) layout(texts []string) (string, [][2]int) {
	var b strings.Builder
	at := make([][2]int, len(t.Columns))
	n := 0 // runes written
	for i, c := range t.Columns {
		text := oneLine(texts[i])
		lead, trail := 0, 0
		if w := c.width(); w > 0 {
			text = clip(text, w)
			pad := max(w-utf8.RuneCountInString(text), 0)
			if c.Right {
				lead = pad
			} else if i < len(t.Columns)-1 {
				trail = pad
			}
		}
		if i > 0 {
			b.WriteByte(' ')
			n++
		}
		b.WriteString(strings.Repeat(" ", lead))
		n += lead
		w := utf8.RuneCountInString(text)
		at[i] = [2]int{n, n + w}
		b.WriteString(text)
		n += w
		b.WriteString(strings.Repeat(" ", trail))
		n += trail
	}
	line := strings.TrimRight(b.String(), " ")
	end := utf8.RuneCountInString(line)
	for i := range at {
		at[i][0], at[i][1] = min(at[i][0], end), min(at[i][1], end)
	}
	return line, at
}

// Choose is the sort that follows cur when the user steps dir columns along
// the table's sortable columns (1 right, -1 left), wrapping around. The new
// column starts in its natural direction.
func (t Table) Choose(cur Sort, dir int) (Sort, bool) {
	keys := t.Keys()
	if len(keys) == 0 {
		return Sort{}, false
	}
	i := slices.Index(keys, t.Effective(cur).Key)
	if i < 0 {
		i = 0
		if dir < 0 {
			i = 1
		}
	}
	key := keys[((i+dir)%len(keys)+len(keys))%len(keys)]
	return Sort{Key: key, Desc: t.Columns[t.column(key)].Desc}, true
}

// Invert is s turned the other way round.
func (t Table) Invert(s Sort) Sort {
	s = t.Effective(s)
	s.Desc = !s.Desc
	return s
}

// String names a sort for the status line.
func (s Sort) String() string {
	if s.Key == "" {
		return ""
	}
	dir := "ascending"
	if s.Desc {
		dir = "descending"
	}
	return fmt.Sprintf("%s %s", s.Key, dir)
}
