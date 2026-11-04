// Package coverage answers two questions that a pile of articles hides: how
// well is each story covered, and how well does each source perform. It takes
// its design from the way news comparison services read the news (group the
// articles about one event, look at who covers it and who does not, and judge
// the outlets by what they do across many stories), adapted to a long-term
// investor: the question is not a political lean but whether a claim rests on a
// primary source, and whether a source is early, redundant or noise.
//
// A Story is a cluster of articles (see package cluster). Its Backing says what
// the story rests on, from strongest to weakest:
//
//	primary       a tier 1 source reports it (a filing, a regulator, a central bank)
//	corroborated  no primary source, but two or more independent outlets report it
//	unconfirmed   one outlet of tier 2 reports it, and no one else
//	opinion       only commentary (tier 3) reports it
//
// Outlets are counted by domain, not by source, so a feed that republishes
// another does not count twice. The package is pure: it reads articles, tiers
// and verdicts it is given and touches nothing else.
package coverage

import (
	"cmp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Backing is what a story rests on.
type Backing string

// The backings, from strongest to weakest.
const (
	BackingPrimary      Backing = "primary"
	BackingCorroborated Backing = "corroborated"
	BackingUnconfirmed  Backing = "unconfirmed"
	BackingOpinion      Backing = "opinion"
)

// Rank orders backings: larger is stronger.
func (b Backing) Rank() int {
	switch b {
	case BackingPrimary:
		return 4
	case BackingCorroborated:
		return 3
	case BackingUnconfirmed:
		return 2
	case BackingOpinion:
		return 1
	}
	return 0
}

// Story is the articles about one event.
type Story struct {
	ID       string
	Lead     model.Article   // the earliest report; it gets the credit
	Articles []model.Article // oldest first, Lead included
	// Outlets are the distinct domains that reported the story, sorted.
	Outlets []string
	// Sources are the distinct source IDs, sorted.
	Sources []string
	Backing Backing
	// Thin is true for a material story that rests on a single outlet that is
	// not a primary source: the case worth a second look before acting on it.
	Thin bool
	// Relevance and Materiality are the highest of the articles; Tone is their
	// mean.
	Relevance   float64
	Materiality int
	Tone        float64
	Tickers     []string
	Topics      []string
	// Spread is the time from the first report to the last.
	Spread time.Duration
}

// Breadth is the number of independent outlets.
func (s Story) Breadth() int { return len(s.Outlets) }

// Options say how stories are judged.
type Options struct {
	// Tier returns the tier of a source; an unknown source is treated as
	// commentary.
	Tier func(sourceID string) model.Tier
	// ThinMateriality is the materiality at or above which a single-outlet
	// story that is not primary counts as thin. Zero means 4.
	ThinMateriality int
}

// Stories groups articles by ClusterID (an article without one is a story of
// its own) and judges each group. The result is sorted by relevance, then
// breadth, then materiality, newest lead first.
func Stories(arts []model.Article, opt Options) []Story {
	if opt.Tier == nil {
		opt.Tier = func(string) model.Tier { return model.TierCommentary }
	}
	if opt.ThinMateriality == 0 {
		opt.ThinMateriality = 4
	}
	groups := map[string][]model.Article{}
	var order []string
	for _, a := range arts {
		key := a.ClusterID
		if key == "" {
			key = "article:" + a.ID
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], a)
	}
	out := make([]Story, 0, len(order))
	for _, key := range order {
		out = append(out, build(key, groups[key], opt))
	}
	slices.SortStableFunc(out, func(a, b Story) int {
		if c := cmp.Compare(b.Relevance, a.Relevance); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Breadth(), a.Breadth()); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Materiality, a.Materiality); c != 0 {
			return c
		}
		return b.Lead.Published.Compare(a.Lead.Published)
	})
	return out
}

func build(id string, arts []model.Article, opt Options) Story {
	slices.SortStableFunc(arts, func(a, b model.Article) int {
		if c := a.Published.Compare(b.Published); c != 0 {
			return c
		}
		if c := cmp.Compare(opt.Tier(a.SourceID), opt.Tier(b.SourceID)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	s := Story{ID: id, Lead: arts[0], Articles: arts}
	outlets, sources := set{}, set{}
	tickers, topics := set{}, set{}
	best := model.TierCommentary + 1
	var toneSum float64
	for _, a := range arts {
		outlets.add(outletOf(a))
		sources.add(a.SourceID)
		for _, t := range a.Tickers {
			tickers.add(t)
		}
		for _, t := range a.Topics {
			topics.add(t)
		}
		if t := opt.Tier(a.SourceID); t < best {
			best = t
		}
		s.Relevance = max(s.Relevance, a.Score.Relevance)
		s.Materiality = max(s.Materiality, a.Score.Materiality)
		toneSum += a.Score.Tone
	}
	s.Outlets, s.Sources = outlets.sorted(), sources.sorted()
	s.Tickers, s.Topics = tickers.sorted(), topics.sorted()
	s.Tone = toneSum / float64(len(arts))
	s.Spread = arts[len(arts)-1].Published.Sub(arts[0].Published)

	switch {
	case best == model.TierPrimary:
		s.Backing = BackingPrimary
	case best == model.TierReporting && s.Breadth() >= 2:
		s.Backing = BackingCorroborated
	case best == model.TierReporting:
		s.Backing = BackingUnconfirmed
	default:
		s.Backing = BackingOpinion
	}
	s.Thin = s.Breadth() == 1 && s.Backing != BackingPrimary && s.Materiality >= opt.ThinMateriality
	return s
}

// outletOf is the domain an article counts under.
func outletOf(a model.Article) string {
	d := strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(a.Domain)), "www."), ".")
	if d == "" {
		return "(unknown)"
	}
	return d
}

type set map[string]struct{}

func (s set) add(v string) { s[v] = struct{}{} }
func (s set) sorted() []string {
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// SourceStat is how one source performed over a set of stories.
type SourceStat struct {
	SourceID string
	Articles int
	// Relevant is the articles at or above the threshold.
	Relevant int
	// Stories is the number of stories the source is part of.
	Stories int
	// Shared counts stories that at least one other outlet also reported.
	Shared int
	// First counts shared stories where this source's outlet reported first.
	First int
	// Alone counts stories only this source's outlet reported.
	Alone int
	// Backed counts stories that rest on a primary source.
	Backed int
	// MedianLag is the median delay behind the first report, over the shared
	// stories where the source was not first; zero when there are none.
	MedianLag  time.Duration
	LagSamples int
	// Kept and Dropped are the user's verdicts on this source's articles.
	Kept, Dropped int
}

// Scoop is the share of shared stories this source reported first; ok is false
// when it shared none.
func (s SourceStat) Scoop() (rate float64, ok bool) {
	if s.Shared == 0 {
		return 0, false
	}
	return float64(s.First) / float64(s.Shared), true
}

// RelevantShare is the share of the source's articles that were relevant.
func (s SourceStat) RelevantShare() float64 {
	if s.Articles == 0 {
		return 0
	}
	return float64(s.Relevant) / float64(s.Articles)
}

// Precision is the share of the user's verdicts on this source that were keep;
// ok is false when there are none.
func (s SourceStat) Precision() (p float64, ok bool) {
	n := s.Kept + s.Dropped
	if n == 0 {
		return 0, false
	}
	return float64(s.Kept) / float64(n), true
}

// Sources judges every source that appears in stories. verdicts maps an
// article ID to the user's verdict and may be nil. The result is sorted by
// relevant articles, then by source ID.
func Sources(stories []Story, threshold float64, verdicts map[string]labels.Verdict) []SourceStat {
	stats := map[string]*SourceStat{}
	lags := map[string][]time.Duration{}
	get := func(id string) *SourceStat {
		if s, ok := stats[id]; ok {
			return s
		}
		s := &SourceStat{SourceID: id}
		stats[id] = s
		return s
	}
	for _, st := range stories {
		leadOutlet := outletOf(st.Lead)
		seen := set{}
		earliest := map[string]time.Time{} // per source, its first article
		for _, a := range st.Articles {
			s := get(a.SourceID)
			s.Articles++
			if a.Score.Relevant(threshold) {
				s.Relevant++
			}
			switch verdicts[a.ID] {
			case labels.Keep:
				s.Kept++
			case labels.Drop:
				s.Dropped++
			}
			seen.add(a.SourceID)
			if _, ok := earliest[a.SourceID]; !ok {
				earliest[a.SourceID] = a.Published
			}
		}
		for id := range seen {
			s := get(id)
			s.Stories++
			if st.Backing == BackingPrimary {
				s.Backed++
			}
			if st.Breadth() == 1 {
				s.Alone++
				continue
			}
			s.Shared++
			switch {
			case id == st.Lead.SourceID:
				s.First++
			case outletOfSource(st, id) == leadOutlet:
				// Another feed of the lead's own outlet: neither early nor late.
			default:
				lags[id] = append(lags[id], earliest[id].Sub(st.Lead.Published))
			}
		}
	}
	out := make([]SourceStat, 0, len(stats))
	for id, s := range stats {
		if l := lags[id]; len(l) > 0 {
			slices.Sort(l)
			s.MedianLag = l[len(l)/2]
			s.LagSamples = len(l)
		}
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b SourceStat) int {
		if c := cmp.Compare(b.Relevant, a.Relevant); c != 0 {
			return c
		}
		return strings.Compare(a.SourceID, b.SourceID)
	})
	return out
}

// outletOfSource is the outlet of the first article of the source in the story.
func outletOfSource(st Story, sourceID string) string {
	for _, a := range st.Articles {
		if a.SourceID == sourceID {
			return outletOf(a)
		}
	}
	return ""
}
