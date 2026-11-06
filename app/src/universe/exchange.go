package universe

import (
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

// OnExchange reports whether the stock is listed on the exchange, ignoring
// case. It compares the whole name: NYSE is not NYSE Arca.
func (p Profile) OnExchange(exchange string) bool {
	return exchange != "" && strings.EqualFold(p.Exchange, exchange)
}

// Exchanges lists the exchanges the companies are listed on, the one with the
// most companies first (ties by name). Companies with no exchange are not
// counted under any.
func (u *Universe) Exchanges() []Count {
	if u == nil {
		return nil
	}
	m := map[string]int{}
	for i := range u.profiles {
		if e := u.profiles[i].Exchange; e != "" {
			m[e]++
		}
	}
	out := make([]Count, 0, len(m))
	for name, n := range m {
		out = append(out, Count{Name: name, Count: n})
	}
	slices.SortFunc(out, func(a, b Count) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// ResolveExchange finds the exchange a name stands for among those the
// universe knows: the whole name ignoring case, else the only one it is a
// prefix of. It returns the name as the universe writes it.
func (u *Universe) ResolveExchange(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	var prefixed []string
	for _, c := range u.Exchanges() {
		if strings.EqualFold(c.Name, name) {
			return c.Name, true
		}
		if len(name) <= len(c.Name) && strings.EqualFold(c.Name[:len(name)], name) {
			prefixed = append(prefixed, c.Name)
		}
	}
	if len(prefixed) == 1 {
		return prefixed[0], true
	}
	return "", false
}

// ScopeNews narrows articles to the news of one exchange: an article stays if it
// names a stock listed there, and its tickers are cut down to those stocks, so
// everything counted from it (stocks, mentions, tone) speaks of that exchange
// only. Articles that name no stock of the exchange are left out. The articles
// given are not changed. An empty exchange returns them all as they are.
func (u *Universe) ScopeNews(arts []model.Article, exchange string) []model.Article {
	if exchange == "" {
		return arts
	}
	out := make([]model.Article, 0, len(arts)/2)
	for _, a := range arts {
		var mine []string
		for _, t := range a.Tickers {
			if p, ok := u.Get(t); ok && p.OnExchange(exchange) {
				mine = append(mine, t)
			}
		}
		if len(mine) == 0 {
			continue
		}
		a.Tickers = mine
		out = append(out, a)
	}
	return out
}
