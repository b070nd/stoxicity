// Synthetic fixture, not verified against live data.

package trends

import (
	"reflect"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

// topic is an article with the given relevance and topics.
func topic(relevance float64, topics ...string) model.Article {
	return model.Article{Topics: topics, Score: model.Score{Relevance: relevance}}
}

func repeat(n int, a model.Article) []model.Article {
	out := make([]model.Article, n)
	for i := range out {
		out[i] = a
	}
	return out
}

func TestDetectNewTopicUsesFloor(t *testing.T) {
	today := repeat(3, topic(0.9, "ai"))
	got := Detect(today, nil, 14, Options{Threshold: 0.5})
	if len(got) != 1 {
		t.Fatalf("trends = %+v, want one", got)
	}
	// Baseline is 0, floored to 0.5, so lift is 3 / 0.5 = 6.
	if got[0].Baseline != 0 || got[0].Lift != 6 {
		t.Errorf("trend = %+v, want baseline 0 and lift 6", got[0])
	}
}

func TestDetectBaselineDivision(t *testing.T) {
	today := repeat(4, topic(0.9, "rates"))
	baseline := repeat(14, topic(0.9, "rates"))
	got := Detect(today, baseline, 14, Options{Threshold: 0.5})
	if len(got) != 1 || got[0].Baseline != 1 || got[0].Lift != 4 {
		t.Fatalf("trends = %+v, want baseline 1 and lift 4", got)
	}
}

func TestDetectBaselineDaysBelowOneCountsAsOne(t *testing.T) {
	today := repeat(6, topic(0.9, "energy"))
	baseline := repeat(2, topic(0.9, "energy"))
	got := Detect(today, baseline, 0, Options{Threshold: 0.5})
	// Two baseline articles over one day is 2 a day, so lift is 6 / 2 = 3.
	if len(got) != 1 || got[0].Baseline != 2 || got[0].Lift != 3 {
		t.Fatalf("trends = %+v, want baseline 2 and lift 3", got)
	}
}

func TestDetectThresholds(t *testing.T) {
	cases := []struct {
		name     string
		today    []model.Article
		baseline []model.Article
		opts     Options
		want     []string
	}{
		{
			name:  "fewer than MinToday",
			today: repeat(2, topic(0.9, "ai")),
			want:  nil,
		},
		{
			name:  "MinToday is inclusive",
			today: repeat(3, topic(0.9, "ai")),
			want:  []string{"ai"},
		},
		{
			name:     "lift below MinLift",
			today:    repeat(4, topic(0.9, "energy")),
			baseline: repeat(3, topic(0.9, "energy")),
			want:     nil,
		},
		{
			name:     "lift meets MinLift",
			today:    repeat(6, topic(0.9, "energy")),
			baseline: repeat(2, topic(0.9, "energy")),
			want:     []string{"energy"},
		},
		{
			name:  "irrelevant articles do not count",
			today: repeat(5, topic(0.1, "ai")),
			want:  nil,
		},
		{
			name:  "MinToday set explicitly",
			today: repeat(3, topic(0.9, "ai")),
			opts:  Options{MinToday: 4},
			want:  nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := c.opts
			opts.Threshold = 0.5
			var names []string
			for _, tr := range Detect(c.today, c.baseline, 1, opts) {
				names = append(names, tr.Topic)
			}
			if !reflect.DeepEqual(names, c.want) {
				t.Errorf("topics = %v, want %v", names, c.want)
			}
		})
	}
}

func TestDetectBaselineIrrelevantDoesNotCount(t *testing.T) {
	today := repeat(3, topic(0.9, "rates"))
	baseline := repeat(10, topic(0.1, "rates"))
	got := Detect(today, baseline, 1, Options{Threshold: 0.5})
	if len(got) != 1 || got[0].Baseline != 0 {
		t.Fatalf("trends = %+v, want baseline 0", got)
	}
}

func TestDetectCountsEachArticleOncePerTopic(t *testing.T) {
	today := repeat(3, topic(0.9, "ai", "ai", " ai "))
	got := Detect(today, nil, 1, Options{Threshold: 0.5})
	if len(got) != 1 || got[0].Today != 3 {
		t.Fatalf("trends = %+v, want today 3", got)
	}
}

func TestDetectOrder(t *testing.T) {
	var today []model.Article
	today = append(today, repeat(6, topic(0.9, "beta"))...)
	today = append(today, repeat(3, topic(0.9, "alpha"))...)
	today = append(today, repeat(3, topic(0.9, "gamma"))...)
	today = append(today, repeat(4, topic(0.9, "delta"))...)
	// Every topic is new, so lift is today times 2: beta 12, delta 8, alpha and
	// gamma 6 each, and the tie goes to the name.
	got := Detect(today, nil, 1, Options{Threshold: 0.5})
	want := []string{"beta", "delta", "alpha", "gamma"}
	var names []string
	for _, tr := range got {
		names = append(names, tr.Topic)
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

func TestDetectEqualLiftGoesToLargerToday(t *testing.T) {
	// x: 4 today against 2 a day, lift 2. y: 6 today against 3 a day, lift 2.
	// The lifts tie, so y, with more articles today, comes first although x
	// sorts earlier by name.
	today := append(repeat(4, topic(0.9, "x")), repeat(6, topic(0.9, "y"))...)
	baseline := append(repeat(2, topic(0.9, "x")), repeat(3, topic(0.9, "y"))...)
	got := Detect(today, baseline, 1, Options{Threshold: 0.5})
	if len(got) != 2 || got[0].Topic != "y" || got[0].Lift != got[1].Lift {
		t.Fatalf("trends = %+v, want y first with the same lift as x", got)
	}
}

func TestDetectEmpty(t *testing.T) {
	if got := Detect(nil, nil, 14, Options{Threshold: 0.5}); len(got) != 0 {
		t.Fatalf("Detect(nil) = %+v, want none", got)
	}
}

func TestSyndicatedCopiesOfOneStoryCountOnce(t *testing.T) {
	copyOf := func(id string) model.Article {
		a := topic(0.9, "ai")
		a.ID, a.ClusterID = id, "one-story"
		return a
	}
	today := []model.Article{copyOf("a"), copyOf("b"), copyOf("c")}
	if got := Detect(today, nil, 1, Options{Threshold: 0.5}); len(got) != 0 {
		t.Errorf("three copies of one story made a trend: %+v", got)
	}
	today = append(today, topic(0.9, "ai"), topic(0.9, "ai"))
	got := Detect(today, nil, 1, Options{Threshold: 0.5})
	if len(got) != 1 || got[0].Today != 3 {
		t.Errorf("trends = %+v, want today 3 (one story plus two others)", got)
	}
}
