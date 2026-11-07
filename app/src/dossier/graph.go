package dossier

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
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
}

// Verdict says how far the evidence around a security is corroborated. It
// measures the breadth and concentration of the evidence, not whether the news
// predicts a price move, and it is not a trading signal.
type Verdict struct {
	Events   int // events as stored
	Episodes int // events of one kind within EpisodeSpan counted once
	// Publishers are the distinct domains of the evidence that is not a primary
	// source. When a primary source (a filing) is among the evidence, everything
	// quoting it is a copy of it, not another confirmation, and is not counted.
	Publishers int
	Primary    int // distinct primary sources
	Sources    int
	// PeakShare is the share of the weight in the heaviest episode. The weight
	// is materiality, which is only an ordering of what to read first.
	PeakShare    float64
	Basis        string // "primary", "publishers", "single publisher" or "none"
	Corroborated bool
	SinglePeak   bool
	Reason       string
	Caveat       string
}

// The basis of a verdict.
const (
	BasisPrimary         = "primary"
	BasisPublishers      = "publishers"
	BasisSinglePublisher = "single publisher"
	BasisNone            = "none"
)

// EpisodeSpan is how close events of one kind for one security must be to count
// as one episode: an earnings release, its 8-K and the stories that follow are
// one episode, not three.
const EpisodeSpan = 72 * time.Hour

// SinglePeakShare is the share of the weight above which one episode dominates.
const SinglePeakShare = 0.7

// Caveat is said with every verdict: independence of origin is not established.
const Caveat = "publishers are counted by domain; copies of one wire story are not detected as one origin, and materiality comes from a keyword rule"

// Assess judges the events of one security. isPrimary says whether a source is a
// primary one (a filing, a regulator).
//
// Corroborated means: a primary source reports it, or at least two distinct
// publishers that are not primary sources do. Materiality takes no part in that.
// Episodes, not events, are counted, so one story reported through several
// filings and articles is not "many events".
func Assess(events []store.SignalEvent, isPrimary func(sourceID string) bool) Verdict {
	v := Verdict{Events: len(events), Caveat: Caveat, Basis: BasisNone}
	if len(events) == 0 {
		v.Reason = "no event"
		return v
	}
	sorted := slices.Clone(events)
	slices.SortFunc(sorted, func(a, b store.SignalEvent) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), a.PublicAt.Compare(b.PublicAt))
	})
	type episode struct {
		kind string
		last time.Time
		mat  int
	}
	var eps []episode
	for _, e := range sorted {
		if n := len(eps); n > 0 && eps[n-1].kind == e.Kind && e.PublicAt.Sub(eps[n-1].last) <= EpisodeSpan {
			eps[n-1].last = e.PublicAt
			eps[n-1].mat = max(eps[n-1].mat, e.Materiality)
			continue
		}
		eps = append(eps, episode{e.Kind, e.PublicAt, e.Materiality})
	}
	v.Episodes = len(eps)
	var sum, top int
	for _, ep := range eps {
		sum += ep.mat
		top = max(top, ep.mat)
	}
	if sum > 0 {
		v.PeakShare = float64(top) / float64(sum)
	}
	v.SinglePeak = v.Episodes <= 1 || v.PeakShare > SinglePeakShare

	domains, primary, sources := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range events {
		for _, src := range e.Sources {
			sources[src] = true
			if isPrimary != nil && isPrimary(src) {
				primary[src] = true
			}
		}
	}
	v.Primary, v.Sources = len(primary), len(sources)
	for _, e := range events {
		for _, d := range e.Domains {
			domains[d] = true
		}
	}
	if v.Primary == 0 {
		v.Publishers = len(domains)
	}
	switch {
	case v.Primary > 0:
		v.Basis, v.Corroborated = BasisPrimary, true
		v.Reason = fmt.Sprintf("reported by a primary source; %d episodes", v.Episodes)
	case v.Publishers >= 2:
		v.Basis, v.Corroborated = BasisPublishers, true
		v.Reason = fmt.Sprintf("%d publishers, %d episodes", v.Publishers, v.Episodes)
	default:
		v.Basis = BasisSinglePublisher
		v.Reason = "one publisher: not enough to tell a fact from an echo"
	}
	if v.Corroborated && v.SinglePeak {
		v.Reason += "; one episode dominates, read it before trusting the rest"
	}
	return v
}

// BuildGraph joins the events of a security with their evidence. timeline is
// the security's events; history gives every event's revisions; articles the
// evidence by ID. Only the latest revision of each event is drawn.
func BuildGraph(sec store.SecurityInfo, timeline []store.TimelineEntry, history map[string][]model.EventRevision, articles map[string]model.Article, isPrimary func(sourceID string) bool) (Graph, Verdict) {
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
		se := store.SignalEvent{SecurityID: sec.ID, Ticker: sec.Ticker, EventID: cur.EventID, Kind: string(cur.Kind), PublicAt: cur.PublicAt, Materiality: cur.Materiality}
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
	return g, Assess(signal, isPrimary)
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
