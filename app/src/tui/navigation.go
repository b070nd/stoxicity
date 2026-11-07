package tui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/sanitize"
)

// safeLine removes terminal controls without destroying table padding.
func safeLine(s string) string {
	// Text trims its input. Sentinels keep intentional edge padding inside the
	// sanitized string; an unterminated escape may legitimately consume the tail.
	s = sanitize.Text("[" + s + "]")
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, s)
}

func relevantArticles(arts []model.Article, threshold float64) []model.Article {
	out := make([]model.Article, 0, len(arts))
	for _, a := range arts {
		if a.Score.Relevant(threshold) {
			out = append(out, a)
		}
	}
	return out
}

func (m *Model) scopeLine() string {
	check := "[ ]"
	if m.relevantOnly {
		check = "[x]"
	}
	watch := "All"
	if m.stack.Watch() != "" {
		watch = clean(m.watchName(m.stack.Watch()))
	}
	exchange := "All exchanges"
	if m.exchange != "" {
		exchange = clean(m.exchange)
	}
	return " " + styleBold.Render(check+" Relevant only") + styleDim.Render(" (v)  │  ") +
		"Watch: " + watch + styleDim.Render("  │  ") + exchange
}

func (m *Model) section(dir int) tea.Cmd {
	i := 0
	for j, t := range screenTabs {
		if t.label == m.litTab() {
			i = j
			break
		}
	}
	i = (i + dir + len(screenTabs)) % len(screenTabs)
	t := screenTabs[i]
	r := nav.Route{Kind: t.kinds[0]}
	if t.label == "Stocks" {
		r = nav.Route{Kind: nav.Companies, Arg: stocksQuery}
	}
	cmd := m.goTo(r)
	m.navFocus, m.chipFocus = true, false
	return cmd
}

func (m *Model) toggleRelevant() tea.Cmd {
	m.relevantOnly = !m.relevantOnly
	// The bundled all-relevant watch filters upstream. Clear it when the user
	// explicitly asks to see all news; custom watch constraints remain intact.
	if !m.relevantOnly && m.stack.Watch() == "all-relevant" {
		return m.switchWatch("")
	}
	m.rebuild()
	m.ok("relevant only: %t", m.relevantOnly)
	return nil
}

func (m *Model) navigationKey(key string) (tea.Cmd, bool) {
	switch key {
	case "tab":
		return m.section(1), true
	case "shift+tab":
		return m.section(-1), true
	case "f6":
		m.navFocus, m.chipFocus = !m.navFocus, false
		return nil, true
	}
	if !m.navFocus {
		return nil, false
	}
	switch key {
	case "right":
		return m.section(1), true
	case "left":
		return m.section(-1), true
	case "down", "enter", "esc", "space":
		m.navFocus = false
		return nil, true
	case "up":
		return nil, true
	}
	m.navFocus = false
	return nil, false
}

func (m *Model) navigationHint() string {
	return "Tab/←/→ sections  F6 focus  ↓ content"
}
