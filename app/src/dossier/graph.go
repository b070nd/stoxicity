package dossier

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/episode"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/origin"
	"github.com/b070nd/stoxicity/app/src/store"
)

// Node kinds of the graph.
const (
	NodeSecurity  = "security"
	NodeEvent     = "event"
	NodeArticle   = "article"
	NodePublisher = "publisher"
	NodeTopic     = "topic"
)

// Node is a thing in the graph.
type Node struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// Edge is a relation. Rel is an observed relation (subject, counterparty,
// mentioned, evidence, published-by, about); the graph holds no assessed effects.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rel  string `json:"rel"`
}

// Graph is the knowledge around one security.
type Graph struct {
	Focus string `json:"focus"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
	// Evidence is the state of the evidence around the focus, judged from
	// reporting origins and never from materiality.
	Evidence Summary `json:"evidence"`
}

// Summary is a verdict as the graph and the exports state it.
type Summary struct {
	State    State  `json:"state"`
	Reason   string `json:"reason"`
	Events   int    `json:"events"`
	Episodes int    `json:"episodes"`
	// Concentration: PeakReports of TotalReports reports (distinct origins,
	// copies once, counted within each episode) are in the largest episode.
	PeakReports  int    `json:"peak_reports"`
	TotalReports int    `json:"total_reports"`
	Caveat       string `json:"caveat"`
}

// BuildGraph joins the events of a security with their evidence. timeline is
// the security's events; history gives every event's revisions; articles the
// evidence by ID; stored the origins already stored (an article without one is
// read by rules, now). Only the latest revision of each event is drawn. The
// verdict is judged with fresh episode decisions and is not stored.
func BuildGraph(sec store.SecurityInfo, timeline []store.TimelineEntry, history map[string][]model.EventRevision, articles map[string]model.Article, stored map[string]origin.Origin, isPrimary func(sourceID string) bool) (Graph, Verdict) {
	fresh := map[string]origin.Origin{}
	{
		var all []model.Article
		for _, a := range articles {
			if _, ok := stored[a.ID]; !ok {
				all = append(all, a)
			}
		}
		slices.SortFunc(all, func(x, y model.Article) int { return cmp.Compare(x.ID, y.ID) })
		fresh = OriginsOf(all, isPrimary)
	}
	g := Graph{Focus: sec.ID}
	seen := map[string]bool{}
	node := func(id, kind, label string) {
		if !seen[id] {
			seen[id] = true
			g.Nodes = append(g.Nodes, Node{id, kind, line(label)})
		}
	}
	edge := func(from, to, rel string) { g.Edges = append(g.Edges, Edge{from, to, rel}) }
	node(sec.ID, NodeSecurity, sec.Ticker)
	var signal []store.SignalEvent
	for _, t := range timeline {
		h := history[t.EventID]
		if len(h) == 0 {
			continue
		}
		cur := h[len(h)-1]
		var role model.Role
		for _, s := range cur.Securities {
			if s.SecurityID == sec.ID {
				role = s.Role
			}
		}
		if role == "" {
			continue
		}
		se := store.SignalEvent{SecurityID: sec.ID, Ticker: sec.Ticker, EventID: cur.EventID, Kind: string(cur.Kind), Title: cur.Title, EffectiveDay: cur.EffectiveDay,
			PublicAt: cur.PublicAt, Materiality: cur.Materiality, Origins: map[string]origin.Origin{}}
		for _, s := range cur.Securities {
			if s.Role == model.RoleCounterparty {
				se.Counterparties = append(se.Counterparties, s.SecurityID)
			}
		}
		node(cur.EventID, NodeEvent, fmt.Sprintf("%s: %s", cur.Kind, cur.Title))
		edge(cur.EventID, sec.ID, string(role))
		for _, s := range cur.Securities {
			if s.SecurityID != sec.ID {
				node(s.SecurityID, NodeSecurity, cmp.Or(s.Ticker, s.SecurityID))
				edge(cur.EventID, s.SecurityID, string(s.Role))
			}
		}
		for _, topic := range cur.Topics {
			node("topic:"+topic, NodeTopic, topic)
			edge(cur.EventID, "topic:"+topic, "about")
		}
		for _, ev := range cur.Evidence {
			a, ok := articles[ev.ArticleID]
			label := ev.ArticleID
			if ok {
				label = a.Title
			}
			node("article:"+ev.ArticleID, NodeArticle, label)
			edge("article:"+ev.ArticleID, cur.EventID, "evidence")
			se.Sources = append(se.Sources, ev.SourceID)
			x := store.SignalEvidence{ArticleID: ev.ArticleID, SourceID: ev.SourceID}
			if ok {
				x.Domain, x.Form, x.CIK, x.Title, x.URL, x.Items, x.Published = a.Domain, a.Form, a.CIK, a.Title, a.URL, a.Items, a.Published
				if o, has := stored[a.ID]; has {
					se.Origins[a.ID] = o
				} else if o, has := fresh[a.ID]; has {
					se.Origins[a.ID] = o
				}
			}
			se.Evidence = append(se.Evidence, x)
			if ok && a.Domain != "" {
				se.Domains = append(se.Domains, a.Domain)
				node("publisher:"+a.Domain, NodePublisher, a.Domain)
				edge("article:"+ev.ArticleID, "publisher:"+a.Domain, "published-by")
			}
		}
		if role == model.RoleSubject { // other roles are drawn, but only the subject of an event counts as its signal
			signal = append(signal, se)
		}
	}
	slices.SortFunc(g.Nodes, func(a, b Node) int { return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID)) })
	slices.SortFunc(g.Edges, func(a, b Edge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To), cmp.Compare(a.Rel, b.Rel))
	})
	v := Assess(signal, episode.Decisions(SignalEpisodeEvents(signal)), isPrimary)
	g.Evidence = Summary{State: v.State, Reason: v.Reason, Events: v.Events, Episodes: v.Episodes, PeakReports: v.PeakReports, TotalReports: v.TotalReports, Caveat: v.Caveat}
	return g, v
}

// JSON writes the graph as indented JSON.
func (g Graph) JSON() string {
	b, _ := json.MarshalIndent(g, "", "  ")
	return string(b) + "\n"
}

// DOT writes the graph for Graphviz.
func (g Graph) DOT() string {
	shape := map[string]string{NodeSecurity: "box", NodeEvent: "ellipse", NodeArticle: "note", NodePublisher: "folder", NodeTopic: "plaintext"}
	var b strings.Builder
	b.WriteString("digraph signal {\n  rankdir=LR;\n")
	fmt.Fprintf(&b, "  // evidence: %s (%s)\n", g.Evidence.State, strings.ReplaceAll(g.Evidence.Reason, "\n", " "))
	for _, n := range g.Nodes {
		fmt.Fprintf(&b, "  %q [label=%q, shape=%s];\n", n.ID, n.Label, shape[n.Kind])
	}
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %q -> %q [label=%q];\n", e.From, e.To, e.Rel)
	}
	b.WriteString("}\n")
	return b.String()
}

// Text writes the graph as an outline: the focus, its events, and under each
// event its securities, topics and the publishers of its evidence.
func (g Graph) Text() string {
	label := map[string]Node{}
	for _, n := range g.Nodes {
		label[n.ID] = n
	}
	out := map[string][]Edge{}
	for _, e := range g.Edges {
		out[e.From] = append(out[e.From], e)
	}
	in := map[string][]Edge{}
	for _, e := range g.Edges {
		in[e.To] = append(in[e.To], e)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", label[g.Focus].Label)
	for _, e := range in[g.Focus] {
		ev := label[e.From]
		fmt.Fprintf(&b, "  %s [%s]\n", ev.Label, e.Rel)
		for _, o := range out[ev.ID] {
			if o.To == g.Focus {
				continue
			}
			fmt.Fprintf(&b, "    %s: %s\n", o.Rel, label[o.To].Label)
		}
		pubs := map[string]int{}
		for _, a := range in[ev.ID] {
			for _, p := range out[a.From] {
				if p.Rel == "published-by" {
					pubs[label[p.To].Label]++
				}
			}
		}
		for _, p := range slices.Sorted(func(yield func(string) bool) {
			for k := range pubs {
				if !yield(k) {
					return
				}
			}
		}) {
			fmt.Fprintf(&b, "    evidence: %s (%d)\n", p, pubs[p])
		}
	}
	return b.String()
}
