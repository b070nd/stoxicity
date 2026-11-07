package tui

import (
	"fmt"
	"strings"

	"github.com/b070nd/stoxicity/app/src/tagcloud"
)

// chipLabel is written before the first line of chips; the lines after it are
// indented by as much.
const chipLabel = "exchange  "

// exchangeChips adds the exchange filter to a screen: a tag cloud of the
// exchanges the companies are listed on, "All" first, then the biggest first.
// Each is as heavy as its count is among the others (a log scale, as in every
// cloud). It adds nothing when no company has an exchange.
func exchangeChips(b *builder, d Data) {
	list := d.Universe.Exchanges()
	if len(list) == 0 {
		return
	}
	counts := make(map[string]int, len(list))
	for _, c := range list {
		counts[c.Name] = c.Count
	}
	tier := map[string]int{}
	for _, t := range tagcloud.Build(counts, 0) {
		tier[t.Text] = t.Tier
	}
	chips := []Chip{{Text: "All " + thousands(d.Universe.Len()), Style: Cloud5}}
	for _, c := range list {
		chips = append(chips, Chip{
			Text: fmt.Sprintf("%s %s", c.Name, thousands(c.Count)), Style: Cloud1 + Style(tier[c.Name]-1), Value: c.Name,
		})
	}
	// Wrap into lines, so each fits a narrow terminal; the lines are one list to
	// the arrows.
	var line []Chip
	width := len(chipLabel)
	limit := cloudWidth
	if d.Width > 0 {
		limit = d.Width
	}
	flush := func() {
		if len(line) > 0 {
			texts := make([]string, len(line))
			for i, c := range line {
				texts[i] = c.Text
			}
			b.add(Row{Text: chipLabel + strings.Join(texts, "  "), Style: Dim, Chips: line})
			line = nil
			width = len(chipLabel)
		}
	}
	for _, c := range chips {
		if width+len(c.Text)+4 > limit {
			flush()
		}
		line = append(line, c)
		width += len(c.Text) + 4
	}
	flush()
}

// chipList is every chip of the screen, in reading order, with the index of the
// row each is on.
func (m *Model) chipList() (chips []Chip, rows []int) {
	for i, r := range m.scr.Rows {
		for _, c := range r.Chips {
			chips = append(chips, c)
			rows = append(rows, i)
		}
	}
	return chips, rows
}

// chosen reports whether the chip is the exchange every screen is narrowed to.
func (m *Model) chosen(c Chip) bool { return strings.EqualFold(c.Value, m.exchange) }

// enterChips puts the focus on the chips, on the one chosen.
func (m *Model) enterChips() bool {
	chips, _ := m.chipList()
	if len(chips) == 0 {
		return false
	}
	m.chipFocus, m.chip = true, 0
	for i, c := range chips {
		if m.chosen(c) {
			m.chip = i
		}
	}
	return true
}

// chipKey handles a key while the chips have the focus. It reports whether the
// key was used; any other key leaves the chips and does its usual work.
func (m *Model) chipKey(key string) bool {
	chips, _ := m.chipList()
	if len(chips) == 0 {
		m.chipFocus = false
		return false
	}
	m.chip = min(max(m.chip, 0), len(chips)-1)
	switch key {
	case "left":
		m.chip = max(m.chip-1, 0)
	case "right":
		m.chip = min(m.chip+1, len(chips)-1)
	case "home":
		m.chip = 0
	case "end":
		m.chip = len(chips) - 1
	case "enter", "space":
		m.setExchange(chips[m.chip].Value)
	case "down", "ctrl+n", "esc":
		m.chipFocus = false
	case "up", "ctrl+p":
		m.chipFocus, m.navFocus = false, true
	default:
		m.chipFocus = false
		return false
	}
	return true
}

// chipLine draws a row of chips: the chosen one marked ●, the one the arrows
// are on reversed, each in the weight of its count.
func (m *Model) chipLine(at int, row Row) string {
	chips, rows := m.chipList()
	first := 0
	for i := range rows {
		if rows[i] == at {
			first = i
			break
		}
	}
	var b strings.Builder
	label := chipLabel
	if first > 0 {
		label = strings.Repeat(" ", len(chipLabel))
	}
	b.WriteString(styleDim.Render(" " + label))
	for i, c := range row.Chips {
		mark := " "
		style := c.Style.lip()
		if m.chosen(c) {
			mark, style = "●", style.Bold(true)
		}
		cell := mark + clean(c.Text) + " "
		if m.chipFocus && first+i < len(chips) && first+i == m.chip {
			cell = styleCursor.Render(cell)
		} else {
			cell = style.Render(cell)
		}
		b.WriteString(cell + " ")
	}
	return strings.TrimRight(b.String(), " ")
}

// hasChips reports whether the screen has a filter to step up into.
func (m *Model) hasChips() bool {
	chips, _ := m.chipList()
	return len(chips) > 0
}
