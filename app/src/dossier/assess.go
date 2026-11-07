package dossier

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/episode"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/origin"
	"github.com/b070nd/stoxicity/app/src/store"
)

// State is how far the evidence about something is established. It measures the
// breadth and independence of the reporting. It is not a statement that the news
// is true or that a price will move, and it is not a trading signal.
type State string

const (
	// StateNone: no event.
	StateNone State = "none"
	// StateUnresolved: the evidence does not establish independence. One
	// independent origin, articles of unknown origin, or only derivative articles.
	StateUnresolved State = "unresolved"
	// StatePrimary: a primary document (a filing) or the issuer's own release
	// reports it. That is primary evidence of what the issuer claims; it is not
	// independent corroboration of the claims, and articles quoting it add none.
	StatePrimary State = "primary"
	// StateCorroborated: at least two origins that do not rest on one another
	// report it (a primary document counts as one).
	StateCorroborated State = "corroborated"
)

func (s State) rank() int {
	switch s {
	case StateCorroborated:
		return 3
	case StatePrimary:
		return 2
	case StateUnresolved:
		return 1
	}
	return 0
}

// EpisodeVerdict is the judgement of one episode.
type EpisodeVerdict struct {
	Episode episode.Episode
	State   State
	// Own is set when a primary document or the issuer's own release is among
	// the evidence; they count together as one origin, the issuer's.
	Own bool
	// Independent are the keys of the origins that report on their own, copies
	// counted once.
	Independent []string
	// Derived articles rest on a document or on another report; Unknown ones
	// carry nothing that says where they came from; Unassessed ones have no
	// origin stored yet and are treated as unknown.
	Derived, Unknown, Unassessed int
	// Reports are the distinct origins in the episode, copies counted once: the
	// denominator of the concentration.
	Reports   int
	Publisher int
	Reason    string
	Lines     []string // one line per article: publisher, origin, why
}

// Verdict says how far the evidence around a security is established.
type Verdict struct {
	Events   int
	Episodes int
	State    State
	// Corroborated is State == StateCorroborated.
	Corroborated bool
	// Counts of episodes by state.
	CorroboratedEpisodes, PrimaryEpisodes, UnresolvedEpisodes int
	Publishers                                                int // distinct sites among all the evidence (a count of sites, not of reporting)
	Sources                                                   int
	// PeakShare is the share of the reports (distinct origins, copies counted
	// once, counted within each episode) that fall in the largest episode.
	// PeakReports and TotalReports are its numerator and denominator.
	PeakShare                 float64
	PeakReports, TotalReports int
	SinglePeak                bool
	Reason                    string
	Details                   []EpisodeVerdict
	Caveat                    string
}

// SinglePeakShare is the share of the reports above which one episode dominates.
const SinglePeakShare = 0.7

// Caveat is said with every verdict.
const Caveat = "this measures the breadth and independence of the reporting, not whether the news is true or will move a price; origins are found by rules that can miss an attribution, and an article without one stays unresolved"

// SignalEpisodeEvents turns the events of one security into the inputs of the
// episode rules.
func SignalEpisodeEvents(events []store.SignalEvent) []episode.Event {
	out := make([]episode.Event, 0, len(events))
	for _, e := range events {
		ev := episode.Event{ID: e.EventID, Kind: e.Kind, Title: e.Title, PublicAt: e.PublicAt, EffectiveDay: e.EffectiveDay, Counterparties: e.Counterparties, Origins: e.Origins}
		for _, x := range e.Evidence {
			ev.Articles = append(ev.Articles, x.ArticleID)
			if x.Form != "" {
				ev.Filings = append(ev.Filings, episode.Filing{Accession: x.ArticleID, Form: x.Form, Items: x.Items})
			}
		}
		out = append(out, ev)
	}
	return out
}

// Assess judges the events of one security, given the decisions on which of
// them are one episode (see package episode; nil decisions leave every event its
// own episode). isPrimary says whether a source is a primary one, used only for
// an article whose origin has not been assessed.
//
// Corroboration is judged per episode, from the reporting origins of its
// evidence, and never from materiality. The security's state is that of its
// best episode.
func Assess(events []store.SignalEvent, decisions []episode.Decision, isPrimary func(sourceID string) bool) Verdict {
	v := Verdict{Events: len(events), Caveat: Caveat, State: StateNone}
	if len(events) == 0 {
		v.Reason = "no event"
		return v
	}
	byID := map[string]store.SignalEvent{}
	for _, e := range events {
		byID[e.EventID] = e
	}
	eps := episode.Group(SignalEpisodeEvents(events), decisions)
	v.Episodes = len(eps)
	sites, sources := map[string]bool{}, map[string]bool{}
	for _, ep := range eps {
		ev := judge(ep, byID, isPrimary)
		for _, id := range ep.Events {
			for _, x := range byID[id].Evidence {
				if x.Domain != "" {
					sites[x.Domain] = true
				}
				sources[x.SourceID] = true
			}
		}
		v.Details = append(v.Details, ev)
		v.TotalReports += ev.Reports
		v.PeakReports = max(v.PeakReports, ev.Reports)
		switch ev.State {
		case StateCorroborated:
			v.CorroboratedEpisodes++
		case StatePrimary:
			v.PrimaryEpisodes++
		default:
			v.UnresolvedEpisodes++
		}
	}
	v.Publishers, v.Sources = len(sites), len(sources)
	if v.TotalReports > 0 {
		v.PeakShare = float64(v.PeakReports) / float64(v.TotalReports)
	}
	v.SinglePeak = v.Episodes <= 1 || v.PeakShare > SinglePeakShare
	best := v.Details[0]
	for _, d := range v.Details[1:] {
		if d.State.rank() > best.State.rank() {
			best = d
		}
	}
	v.State, v.Corroborated, v.Reason = best.State, best.State == StateCorroborated, best.Reason
	if v.Corroborated && v.SinglePeak {
		v.Reason += "; one episode holds most of the reports, read it before trusting the rest"
	}
	return v
}

// judge reads one episode.
func judge(ep episode.Episode, byID map[string]store.SignalEvent, isPrimary func(string) bool) EpisodeVerdict {
	r := EpisodeVerdict{Episode: ep}
	independent := map[string]bool{}
	reports := map[string]bool{}
	sites := map[string]bool{}
	seen := map[string]bool{}
	for _, id := range ep.Events {
		for _, x := range byID[id].Evidence {
			if seen[x.ArticleID] {
				continue
			}
			seen[x.ArticleID] = true
			sites[x.Domain] = true
			o, ok := byID[id].Origins[x.ArticleID]
			if !ok {
				r.Unassessed++
				o = origin.Detect(origin.Input{ArticleID: x.ArticleID, SourceID: x.SourceID, Domain: x.Domain, URL: x.URL, Title: x.Title, Form: x.Form,
					Primary: isPrimary != nil && isPrimary(x.SourceID), Published: x.Published})
				r.Lines = append(r.Lines, fmt.Sprintf("%s (%s): origin not assessed yet, read now as %s", x.Domain, x.ArticleID, o.Kind))
			} else {
				r.Lines = append(r.Lines, fmt.Sprintf("%s (%s): %s", cmp.Or(x.Domain, o.Publisher), x.ArticleID, describe(o)))
			}
			reports[o.Key] = true
			switch {
			case o.Own():
				r.Own = true
			case o.Independent():
				independent[o.Key] = true
			case o.Kind == origin.KindUnknown:
				r.Unknown++
			default:
				r.Derived++
			}
		}
	}
	r.Independent = sortedKeys(independent)
	r.Reports, r.Publisher = len(reports), len(sites)
	i := len(r.Independent)
	own := 0
	if r.Own {
		own = 1
	}
	switch {
	case i+own >= 2 && i >= 1:
		r.State = StateCorroborated
		if r.Own {
			r.Reason = fmt.Sprintf("a primary document or issuer statement and %d independent origin(s): %s", i, strings.Join(r.Independent, ", "))
		} else {
			r.Reason = fmt.Sprintf("%d independent origins: %s", i, strings.Join(r.Independent, ", "))
		}
	case r.Own:
		r.State = StatePrimary
		r.Reason = "primary evidence only: the issuer's own document or release; coverage resting on it is not independent confirmation"
	default:
		r.State = StateUnresolved
		switch {
		case i == 1:
			r.Reason = "one independent origin (" + r.Independent[0] + "), a second is needed"
		case r.Unknown > 0 || r.Unassessed > 0:
			r.Reason = fmt.Sprintf("independence not established: %d article(s) carry no attribution", r.Unknown+r.Unassessed)
		default:
			r.Reason = "only derivative articles: nothing in them is a report of their own"
		}
	}
	if r.Derived > 0 && r.State != StateUnresolved {
		r.Reason += fmt.Sprintf("; %d derivative article(s) not counted", r.Derived)
	}
	return r
}

func describe(o origin.Origin) string {
	s := fmt.Sprintf("%s [%s, %s, confidence %.2f]", o.Kind, o.Key, o.Method, o.Confidence)
	if o.DerivedFrom != "" {
		s += ", rests on " + o.DerivedFrom
	}
	if o.CopyOf != "" {
		s += ", copy of " + o.CopyOf
	}
	return s
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// OriginsOf reads the origins of the articles by rules alone, when none are
// stored: the articles of each event are compared with one another, and the
// result is not stored.
func OriginsOf(articles []model.Article, isPrimary func(sourceID string) bool) map[string]origin.Origin {
	ins := make([]origin.Input, len(articles))
	os := make([]origin.Origin, len(articles))
	for i, a := range articles {
		ins[i] = origin.Input{ArticleID: a.ID, SourceID: a.SourceID, Domain: a.Domain, URL: a.URL, Title: a.Title, Summary: a.Summary, Form: a.Form,
			Primary: isPrimary != nil && isPrimary(a.SourceID), Published: a.Published}
		os[i] = origin.Detect(ins[i])
	}
	out := map[string]origin.Origin{}
	for _, o := range origin.Link(ins, os) {
		out[o.ArticleID] = o
	}
	return out
}
