package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/nav"
)

func sampleTable() Table {
	t := Table{
		Columns: []Column{
			{Title: "NAME", Key: "name", Width: 8},
			{Title: "N", Key: "n", Width: 3, Right: true, Desc: true},
			{Title: "NOTE"},
		},
		Default: Sort{Key: "n", Desc: true},
	}
	for _, r := range []struct {
		name string
		n    float64
		note string
	}{{"pear", 3, "x"}, {"Apple", 10, "y"}, {"fig", 3, "z"}} {
		t.Items = append(t.Items, Item{
			Cells: []Cell{Str(r.name), Num(strings.TrimSuffix(strings.TrimSpace(fmtF(r.n)), ".0"), r.n), Str(r.note)},
			Row:   Row{Go: route(nav.Stock, strings.ToUpper(r.name))},
		})
	}
	return t
}

func fmtF(f float64) string {
	if f == 10 {
		return "10"
	}
	return "3"
}

func names(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, strings.Fields(r.Text)[0])
	}
	return out
}

func TestTableOrdersByTheChosenColumnAndKeepsTiesInArrivalOrder(t *testing.T) {
	tb := sampleTable()
	if got := names(tb.Body(Sort{})); !slices.Equal(got, []string{"Apple", "pear", "fig"}) {
		t.Errorf("default (n, largest first; pear before fig as they arrived) = %v", got)
	}
	if got := names(tb.Body(Sort{Key: "name"})); !slices.Equal(got, []string{"Apple", "fig", "pear"}) {
		t.Errorf("by name, ignoring case = %v", got)
	}
	if got := names(tb.Body(Sort{Key: "name", Desc: true})); !slices.Equal(got, []string{"pear", "fig", "Apple"}) {
		t.Errorf("by name, reversed = %v", got)
	}
	if got := names(tb.Body(Sort{Key: "nonsense"})); !slices.Equal(got, []string{"Apple", "pear", "fig"}) {
		t.Errorf("an unknown column falls back to the default: %v", got)
	}
}

func TestTableHeaderMarksTheSortedColumn(t *testing.T) {
	tb := sampleTable()
	if h := tb.Rows(Sort{Key: "name"})[0].Text; !strings.HasPrefix(h, "NAME▲") || !strings.Contains(h, " N ") {
		t.Errorf("header = %q", h)
	}
	if h := tb.Rows(Sort{})[0].Text; !strings.Contains(h, "N▼") || strings.Contains(h, "▲") {
		t.Errorf("default header = %q", h)
	}
	if tb.Rows(Sort{})[0].Style != Heading || tb.Rows(Sort{})[0].Selectable() {
		t.Error("the header is a heading nothing can select")
	}
}

func TestTableChooseStepsAlongSortableColumnsAndWraps(t *testing.T) {
	tb := sampleTable() // sortable: name, n
	s, ok := tb.Choose(Sort{}, 1)
	if !ok || s.Key != "name" || s.Desc {
		t.Errorf("after the default n comes name, ascending: %+v", s)
	}
	s, _ = tb.Choose(s, 1)
	if s.Key != "n" || !s.Desc {
		t.Errorf("then n, in its natural largest-first order: %+v", s)
	}
	s, _ = tb.Choose(Sort{Key: "name"}, -1)
	if s.Key != "n" {
		t.Errorf("before name wraps to n: %+v", s)
	}
	if inv := tb.Invert(Sort{Key: "name"}); inv.Key != "name" || !inv.Desc {
		t.Errorf("invert: %+v", inv)
	}
	if _, ok := (Table{}).Choose(Sort{}, 1); ok {
		t.Error("a table without sortable columns has nothing to choose")
	}
}

func TestTablePresortedKeepsItemOrderButLabelsTheHeader(t *testing.T) {
	tb := sampleTable()
	tb.Presorted = true
	if got := names(tb.Body(Sort{Key: "name"})); !slices.Equal(got, []string{"pear", "Apple", "fig"}) {
		t.Errorf("presorted items must not be re-ordered: %v", got)
	}
}

func TestTableWidensAColumnToItsLongestTextAndCutsNothing(t *testing.T) {
	tb := sampleTable()
	tb.Items[0].Cells[0] = Str("a very long name indeed")
	rows := tb.Rows(Sort{})
	for _, r := range rows {
		if strings.Contains(r.Text, "…") {
			t.Errorf("a cell was cut: %q", r.Text)
		}
	}
	var long, other, head string
	for _, r := range rows {
		switch {
		case strings.HasPrefix(r.Text, "a very long name indeed"):
			long = r.Text
		case strings.HasPrefix(r.Text, "Apple"):
			other = r.Text
		case strings.HasPrefix(r.Text, "NAME"):
			head = r.Text
		}
	}
	if long == "" || other == "" || head == "" {
		t.Fatalf("rows missing: %v", rows)
	}
	if !strings.HasPrefix(head, "NAME"+strings.Repeat(" ", len("a very long name indeed")-len("NAME")+1)) {
		t.Errorf("the header is not as wide as the longest cell: %q", head)
	}
}
