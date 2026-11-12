package tui

// fitCompanyTable drops secondary columns before truncating the identity or
// numeric values. The active sort column stays visible. All columns remain
// available through the screen's full Table shape for sorting.
func fitCompanyTable(t Table, width int, sort Sort) Table {
	hasYearStrip := false
	for _, column := range t.Columns {
		if column.Title == "10Y" {
			hasYearStrip = true
			break
		}
	}
	// Prefer stable widths to resizing every time a refresh changes one row.
	for i := range t.Columns {
		c := &t.Columns[i]
		if c.Key == "name" {
			c.Width, c.MaxWidth = 22, 22
		}
	}
	t = t.sized()
	total := func() int {
		n := max(len(t.Columns)-1, 0)
		for i := range t.Columns {
			n += t.widthOf(i)
		}
		return n
	}
	// Numeric text is never clipped to make a percentage appear smaller.
	remove := func(at int) {
		t.Columns = append(t.Columns[:at:at], t.Columns[at+1:]...)
		t.widths = append(t.widths[:at:at], t.widths[at+1:]...)
		for i := range t.Items {
			cells := t.Items[i].Cells
			t.Items[i].Cells = append(cells[:at:at], cells[at+1:]...)
		}
	}
	for _, title := range []string{"TAGS", "DOMAIN", "TYPE", "REVENUE", "SELLS", "BUYS", "LISTED", "MARGIN", "GROWTH", "MCAP", "DAY", "NEWS"} {
		if title == "DAY" && hasYearStrip {
			continue
		}
		if total() <= width {
			break
		}
		for i, c := range t.Columns {
			if c.Title == title && c.Key != sort.Key {
				remove(i)
				break
			}
		}
	}
	if total() > width {
		for i := range t.Columns {
			if t.Columns[i].Key == "name" {
				w := max(8, t.widths[i]-(total()-width))
				t.Columns[i].Width, t.Columns[i].MaxWidth, t.widths[i] = w, w, w
			}
		}
	}
	if total() > width && sort.Key != "name" {
		for i, c := range t.Columns {
			if c.Key == "name" {
				remove(i)
				break
			}
		}
	}
	// On very narrow terminals, retain the sort field and ticker rather than
	// letting the terminal clip a numeric cell halfway through its value.
	for _, key := range []string{"close", "change", "day", "10y", "12m"} {
		if total() <= width {
			break
		}
		for i, c := range t.Columns {
			if (c.Key == key && c.Key != sort.Key) || (key == "day" && c.Title == "DAY") || (key == "10y" && c.Title == "10Y") || (key == "12m" && c.Title == "12M") {
				remove(i)
				break
			}
		}
	}
	return t
}
