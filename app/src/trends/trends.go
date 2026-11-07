// Package trends detects themes (sectors, technologies, macro shifts) that
// are gaining coverage across relevant articles over time.
package trends

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

const (
	defaultMinToday = 3
	defaultMinLift  = 2

	// baselineFloor replaces a baseline below this value before the lift is
	// computed. A topic that never appeared in the baseline would otherwise
	// divide by zero and get an infinite lift. With the floor its lift is
	// Today / 0.5, which is twice Today, so it still has to clear MinToday and
	// MinLift like any other topic and cannot dominate the list on its own.
	baselineFloor = 0.5
)

// Trend is a topic and how its coverage is changing.
type Trend struct {
	Topic    string
	Today    int     // relevant articles today
	Baseline float64 // average per day over the comparison window
	Lift     float64 // Today divided by Baseline, with Baseline floored; see Detect
}

// Options controls Detect.
type Options struct {
	Threshold float64 // relevance at or above which an article counts
	MinToday  int     // relevant articles today needed; 0 means 3
	MinLift   float64 // lift needed; 0 means 2
}

// Detect returns the topics whose coverage today is high against the baseline.
// Today counts the relevant stories from today that carry the topic. Baseline
// is the relevant articles from the baseline that carry it, divided by
// baselineDays (values below 1 count as 1). Lift is Today divided by the
// baseline, floored as described at baselineFloor. Topics are sorted by lift,
// then today's count, then name. Each story counts once per topic: articles
// in one cluster are one story, not independent evidence.
func Detect(today, baseline []model.Article, baselineDays int, opts Options) []Trend {
	minToday := opts.MinToday
	if minToday <= 0 {
		minToday = defaultMinToday
	}
	minLift := opts.MinLift
	if minLift <= 0 {
		minLift = defaultMinLift
	}
	if baselineDays < 1 {
		baselineDays = 1
	}

	now := count(today, opts.Threshold)
	before := count(baseline, opts.Threshold)
	var out []Trend
	for topic, n := range now {
		if n < minToday {
			continue
		}
		base := float64(before[topic]) / float64(baselineDays)
		lift := float64(n) / math.Max(base, baselineFloor)
		if lift < minLift {
			continue
		}
		out = append(out, Trend{Topic: topic, Today: n, Baseline: base, Lift: lift})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Lift != b.Lift {
			return a.Lift > b.Lift
		}
		if a.Today != b.Today {
			return a.Today > b.Today
		}
		return a.Topic < b.Topic
	})
	return out
}

// count is the number of independent stories carrying each topic. Articles
// that share a non-empty ClusterID are copies or follow-ups of one story and
// count once per topic; an article without a cluster stands alone.
func count(articles []model.Article, threshold float64) map[string]int {
	out := make(map[string]int)
	type storyTopic struct{ story, topic string }
	seen := make(map[storyTopic]bool)
	for i, a := range articles {
		if !a.Score.Relevant(threshold) {
			continue
		}
		story := fmt.Sprintf("position:%d", i) // an article without a cluster stands alone
		if a.ClusterID != "" {
			story = "cluster:" + a.ClusterID
		}
		for _, t := range a.Topics {
			t = strings.TrimSpace(t)
			k := storyTopic{story, t}
			if t == "" || seen[k] {
				continue
			}
			seen[k] = true
			out[t]++
		}
	}
	return out
}
