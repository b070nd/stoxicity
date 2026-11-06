package stockfeeds

import (
	"context"
	"net/url"
	"strings"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// robots is what a site's robots.txt asks of robots, for the rules that apply
// to every robot and to this one.
type robots struct{ rules []rule }

type rule struct {
	prefix string
	allow  bool
}

// agent is the product token this program answers to in robots.txt.
const agent = "stoxicity"

// newRobots reads robots.txt of the site. A site with none, or that cannot be
// asked, allows everything, as the standard says for a missing file.
func newRobots(ctx context.Context, c httpx.Client, site *url.URL) *robots {
	resp, err := c.Do(ctx, httpx.Request{URL: "https://" + site.Host + "/robots.txt"})
	if err != nil || httpx.LooksLikeHTML(resp.Body) {
		return &robots{}
	}
	return parseRobots(string(resp.Body))
}

// parseRobots keeps the groups that name this program, or else "*".
func parseRobots(text string) *robots {
	type group struct {
		agents []string
		rules  []rule
	}
	var groups []*group
	var cur *group
	inAgents := false
	for _, line := range strings.Split(text, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
		switch key {
		case "user-agent":
			if !inAgents || cur == nil {
				cur = &group{}
				groups = append(groups, cur)
			}
			cur.agents = append(cur.agents, strings.ToLower(val))
			inAgents = true
		case "allow", "disallow":
			inAgents = false
			if cur != nil && val != "" {
				cur.rules = append(cur.rules, rule{prefix: val, allow: key == "allow"})
			}
		default:
			inAgents = false
		}
	}
	pick := func(name string) []rule {
		var out []rule
		for _, g := range groups {
			for _, a := range g.agents {
				if a == name || (name != "*" && strings.Contains(name, a) && a != "*") {
					out = append(out, g.rules...)
					break
				}
			}
		}
		return out
	}
	if r := pick(agent); len(r) > 0 {
		return &robots{rules: r}
	}
	return &robots{rules: pick("*")}
}

// allowed reports whether the path may be read: the longest matching rule
// decides, and an allow beats a disallow of the same length.
func (r *robots) allowed(path string) bool {
	if path == "" {
		path = "/"
	}
	best, allow := -1, true
	for _, ru := range r.rules {
		if matches(ru.prefix, path) && (len(ru.prefix) > best || len(ru.prefix) == best && ru.allow) {
			best, allow = len(ru.prefix), ru.allow
		}
	}
	return allow
}

// matches applies a robots pattern: a prefix, with * for any run and a final $
// for the end of the path.
func matches(pattern, path string) bool {
	end := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, p := range parts {
		if end && i == len(parts)-1 && i > 0 {
			return len(path)-pos >= len(p) && strings.HasSuffix(path, p)
		}
		j := strings.Index(path[pos:], p)
		if j < 0 || i == 0 && j != 0 {
			return false
		}
		pos += j + len(p)
	}
	return !end || pos == len(path)
}
