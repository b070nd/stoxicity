package labels

import (
	"fmt"
	"slices"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

func art(id string, kind model.EventKind, rel float64) model.Article {
	return model.Article{ID: id, Title: id, Score: model.Score{Kind: kind, Relevance: rel}}
}

func ids(p []Pick) []string {
	var out []string
	for _, x := range p {
		out = append(out, x.Article.ID)
	}
	return out
}

func TestQueueTakesTheNearestToTheThresholdOfEachKindInTurnAndOneInFourAtRandom(t *testing.T) {
	var arts []model.Article
	for i := 0; i < 40; i++ { // a kind with thousands of items nearly at the line...
		arts = append(arts, art(fmt.Sprintf("res%02d", i), model.EventResults, 0.50+float64(i)/1000))
	}
	// ...must not starve a kind with two, far from the line.
	arts = append(arts, art("bk1", model.EventBankruptcy, 0.95), art("off1", model.EventOfficerChange, 0.30))
	got := Queue(arts, map[string]bool{"res00": true}, 0.5, 8, "2026-11-07")
	if len(got) != 8 {
		t.Fatalf("%d picks: %v", len(got), ids(got))
	}
	uncertain, sampled := 0, 0
	kinds := map[model.EventKind]bool{}
	for _, p := range got {
		if p.Article.ID == "res00" {
			t.Error("an article that has a label is not asked again")
		}
		if p.Sampled {
			sampled++
		} else {
			uncertain++
			kinds[p.Article.Score.Kind] = true
		}
		if p.Why == "" {
			t.Errorf("no reason for %s", p.Article.ID)
		}
	}
	if sampled != 2 || uncertain != 6 {
		t.Errorf("one in four is drawn at random: %d sampled, %d uncertain", sampled, uncertain)
	}
	if !kinds[model.EventBankruptcy] || !kinds[model.EventOfficerChange] || !kinds[model.EventResults] {
		t.Errorf("every kind gets a turn: %v", kinds)
	}
	// Same seed, same queue, whatever the order the articles come in.
	rev := slices.Clone(arts)
	slices.Reverse(rev)
	if a, b := ids(Queue(arts, nil, 0.5, 8, "x")), ids(Queue(rev, nil, 0.5, 8, "x")); !slices.Equal(a, b) {
		t.Errorf("the choice depends on the order: %v vs %v", a, b)
	}
	if a, b := ids(Queue(arts, nil, 0.5, 8, "x")), ids(Queue(arts, nil, 0.5, 8, "y")); slices.Equal(a, b) {
		t.Error("another seed should draw another random sample")
	}
}

func TestQueueTakesOneArticlePerStoryAndHandlesLittleToChooseFrom(t *testing.T) {
	a, b := art("a", model.EventResults, 0.5), art("b", model.EventResults, 0.5)
	a.ClusterID, b.ClusterID = "story", "story"
	got := Queue([]model.Article{a, b, art("c", model.EventOther, 0.1)}, nil, 0.5, 10, "s")
	if len(got) != 2 {
		t.Errorf("one of a story, so two picks: %v", ids(got))
	}
	if got := Queue(nil, nil, 0.5, 5, "s"); len(got) != 0 {
		t.Errorf("%v", got)
	}
	if got := Queue([]model.Article{a}, nil, 0.5, 0, "s"); got != nil {
		t.Errorf("%v", got)
	}
}

func TestMeasureCountsTheLensAgainstAllLabelsAndTheSampledOnes(t *testing.T) {
	j := func(v Verdict, sampled bool, rel float64) Judged {
		return Judged{Label: Label{Verdict: v, Sampled: sampled}, Score: model.Score{Relevance: rel}}
	}
	p := Measure([]Judged{
		j(Keep, false, 0.9), j(Keep, true, 0.9), j(Drop, true, 0.8), j(Keep, true, 0.2), j(Drop, false, 0.1),
	}, 0.5)
	if p.Total != 5 || p.Kept != 3 || p.Dropped != 2 || p.Sampled != 3 {
		t.Errorf("%+v", p)
	}
	if pr, ok := p.All.PrecisionValue(); !ok || pr < 0.66 || pr > 0.67 {
		t.Errorf("precision of all: %v", pr)
	}
	if r, ok := p.Honest.RecallValue(); !ok || r != 0.5 {
		t.Errorf("recall of the sample: %v", r)
	}
}
