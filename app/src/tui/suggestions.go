package tui

import (
	"strings"

	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/universe"
)

type suggestion struct{ value, label string }

// updateSuggestions uses the local reference snapshot, never a network request.
// Only the final, unquoted token is completed; editing inside a query is left alone.
func (m *Model) updateSuggestions() {
	m.suggestions, m.suggestionAt = nil, -1
	q := m.prompt.Text()
	if m.prompt.pos != len(m.prompt.text) || strings.Count(q, `"`)%2 != 0 || strings.HasSuffix(q, " ") {
		return
	}
	_, token := completionToken(q)
	if token == "" {
		return
	}
	low := strings.ToLower(token)
	companies := m.stack.Current().Kind == nav.Companies
	terms := []string{"ticker:", "site:", "topic:", "after:", "before:"}
	if companies {
		terms = []string{"type:stock", "type:etf", "type:preferred", "type:adr", "type:note", "type:warrant", "exchange:", "industry:", "domain:", "list:", "tag:", "watched:yes", "news:>0", "sort:news", "sort:name", "sort:-change", "sort:-marketcap", "group:none", "group:exchange"}
	}
	add := func(value, label string) {
		if len(m.suggestions) < 5 {
			m.suggestions = append(m.suggestions, suggestion{value, label})
		}
	}
	for _, term := range terms {
		if strings.HasPrefix(term, low) && term != low {
			add(term, term)
		}
	}
	if strings.HasPrefix(low, "exchange:") && companies {
		for _, ex := range m.be.Universe().Exchanges() {
			value := "exchange:" + ex.Name
			if strings.Contains(ex.Name, " ") {
				value = `exchange:"` + ex.Name + `"`
			}
			if strings.HasPrefix(strings.ToLower(ex.Name), strings.TrimPrefix(low, "exchange:")) {
				add(value, ex.Name)
			}
		}
		return
	}
	search := token
	if strings.HasPrefix(low, "ticker:") {
		search = token[len("ticker:"):]
	} else if strings.Contains(token, ":") {
		return
	}
	if search == "" {
		return
	}
	query, err := universe.ParseQuery(search)
	if err != nil {
		return
	}
	for _, hit := range m.be.Universe().Find(query, universe.Facts{}, 8) {
		p := hit.Profile
		if m.exchange != "" && !p.OnExchange(m.exchange) {
			continue
		}
		value := p.Ticker
		if !companies {
			value = "ticker:" + value
		}
		add(value, p.Ticker+"  "+p.Name+"  · "+p.Exchange)
	}
}

func completionToken(q string) (string, string) {
	at := strings.LastIndexAny(q, " \t") + 1
	return q[:at], q[at:]
}

func (m *Model) suggestionKey(key string) bool {
	if len(m.suggestions) == 0 {
		return false
	}
	switch key {
	case "down":
		m.suggestionAt = (m.suggestionAt + 1) % len(m.suggestions)
		return true
	case "up":
		if m.suggestionAt < 0 {
			m.suggestionAt = len(m.suggestions) - 1
		} else {
			m.suggestionAt = (m.suggestionAt - 1 + len(m.suggestions)) % len(m.suggestions)
		}
		return true
	case "tab", "enter":
		if key == "enter" && m.suggestionAt < 0 {
			return false
		}
		i := max(m.suggestionAt, 0)
		prefix, _ := completionToken(m.prompt.Text())
		m.prompt.Set(prefix + m.suggestions[i].value)
		m.suggestions, m.suggestionAt = nil, -1
		if m.stack.Current().Kind == nav.Companies {
			m.rebuild()
		}
		return true
	}
	return false
}

func (m *Model) suggestionLines() []string {
	lines := []string{styleDim.Render("  Suggestions · ↑/↓ choose · Tab accepts · Enter applies query")}
	for i, s := range m.suggestions {
		style, lead := styleDim, "  "
		if i == m.suggestionAt {
			style, lead = styleCursor, "▸ "
		}
		lines = append(lines, style.Render(lead+clean(s.label)))
	}
	return lines
}
