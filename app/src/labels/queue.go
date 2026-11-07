package labels

import (
	"cmp"
	"hash/fnv"
	"math"
	"slices"
	"strconv"

	"github.com/b070nd/stoxicity/app/src/eval"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Target is the number of labels after which the measures of package eval are
// worth reading: a few hundred, spread over the kinds of event.
const Target = 300

// Pick is an article the queue asks the user to judge.
type Pick struct {
	Article model.Article
	// Sampled says the article was drawn at random instead of for being
	// uncertain, so its label belongs to the honest sample (Label.Sampled).
	Sampled bool
	// Why says in a few words why the queue chose it.
	Why string
}

// sampledShare is the fraction of the queue drawn at random. Labels of the
// uncertain items alone say where the lens is unsure, not how good it is; the
// random ones keep the precision estimate honest.
const sampledShare = 4 // one in four

// Queue chooses n unlabelled articles to judge next, the ones from which the
// lens learns most:
//
//   - most of them are the articles whose relevance is closest to the
//     threshold, where a verdict moves the line the most; they are taken round
//     and round the kinds of event, one of each kind in turn, so a kind with
//     few items is not starved by a kind with thousands, and one of each story;
//   - one in four is drawn at random from all that are left (Sampled).
//
// Equal scores are told apart by a hash of seed and ID, so the choice is the
// same for the same seed, whatever order the articles come in, and different
// from one day to the next when the seed is the day.
func Queue(articles []model.Article, labelled map[string]bool, threshold float64, n int, seed string) []Pick {
	if n <= 0 {
		return nil
	}
	var pool []model.Article
	seenStory := map[string]bool{}
	for _, a := range articles {
		if labelled[a.ID] {
			continue
		}
		if a.ClusterID != "" {
			if seenStory[a.ClusterID] {
				continue
			}
			seenStory[a.ClusterID] = true
		}
		pool = append(pool, a)
	}
	rank := func(a model.Article) uint32 { return hashOf(seed, a.ID) }
	slices.SortFunc(pool, func(a, b model.Article) int {
		da, db := math.Abs(a.Score.Relevance-threshold), math.Abs(b.Score.Relevance-threshold)
		if c := cmp.Compare(da, db); c != 0 {
			return c
		}
		return cmp.Compare(rank(a), rank(b))
	})

	random := min(max(n/sampledShare, 1), len(pool))
	uncertain := n - random
	var out []Pick
	taken := map[string]bool{}

	byKind := map[string][]model.Article{}
	var kinds []string
	for _, a := range pool {
		k := string(a.Score.Kind)
		if _, ok := byKind[k]; !ok {
			kinds = append(kinds, k)
		}
		byKind[k] = append(byKind[k], a)
	}
	// The kinds whose nearest article is nearest come first; the order inside a
	// round is then stable.
	for len(out) < uncertain {
		progressed := false
		for _, k := range kinds {
			if len(out) == uncertain {
				break
			}
			list := byKind[k]
			if len(list) == 0 {
				continue
			}
			a := list[0]
			byKind[k] = list[1:]
			progressed = true
			taken[a.ID] = true
			out = append(out, Pick{Article: a, Why: "close to the threshold (" + strconv.FormatFloat(a.Score.Relevance, 'f', 2, 64) + ")"})
		}
		if !progressed {
			break
		}
	}

	var rest []model.Article
	for _, a := range pool {
		if !taken[a.ID] {
			rest = append(rest, a)
		}
	}
	slices.SortFunc(rest, func(a, b model.Article) int {
		return cmp.Compare(hashOf(seed+"/random", a.ID), hashOf(seed+"/random", b.ID))
	})
	for _, a := range rest[:min(n-len(out), len(rest))] {
		out = append(out, Pick{Article: a, Sampled: true, Why: "random sample"})
	}
	return out
}

func hashOf(seed, id string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(seed))
	h.Write([]byte{0})
	h.Write([]byte(id))
	return h.Sum32()
}

// Judged is a label with the score of its article.
type Judged struct {
	Label Label
	Score model.Score
}

// Progress says how far the labelling is and how the lens does against it.
type Progress struct {
	Total, Kept, Dropped, Sampled int
	// Threshold is the relevance the lens's calls are made at.
	Threshold float64
	// All and Honest count the lens's calls against all labels and against the
	// sampled ones only.
	All, Honest eval.Confusion
}

// Measure counts the labels and compares the lens's calls with them.
func Measure(items []Judged, threshold float64) Progress {
	p := Progress{Threshold: threshold}
	for _, it := range items {
		kept := it.Label.Verdict == Keep
		p.Total++
		if kept {
			p.Kept++
		} else {
			p.Dropped++
		}
		called := it.Score.Relevant(threshold)
		p.All.Add(called, kept)
		if it.Label.Sampled {
			p.Sampled++
			p.Honest.Add(called, kept)
		}
	}
	return p
}
