package lens

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

// base is a rule-based score that found no kind, with one reason of its own.
func base() model.Score {
	return model.Score{
		Kind: model.EventOther, Materiality: 1, Relevance: 0.2,
		Reasons: []string{"no kind rule matched"}, LensVersion: "l0-1-test",
	}
}

// with returns s changed by f.
func with(s model.Score, f func(*model.Score)) model.Score {
	s.Reasons = slices.Clone(s.Reasons)
	f(&s)
	return s
}

// added returns s with extra reasons appended to its own.
func added(s model.Score, reasons ...string) model.Score {
	return with(s, func(s *model.Score) { s.Reasons = append(s.Reasons, reasons...) })
}

var allOn = Use{Kind: true, Tone: true, Sector: true, Topics: true, Materiality: true, Relevance: true, RelevanceWeight: 0.3}

func TestApply(t *testing.T) {
	tests := []struct {
		name      string
		score     model.Score
		op        Opinion
		use       Use
		min       float64
		want      model.Score
		wantTopic string
	}{
		// Nothing to say.
		{
			name:  "all confidences zero changes nothing",
			score: base(),
			op:    Opinion{Kind: model.EventResults, Tone: 0.5, Sector: "energy", Topic: "oil", Materiality: 5, Relevance: 0.9, Truncated: true},
			use:   allOn,
			want:  base(),
		},
		{
			name:  "zero opinion with no values",
			score: base(), op: Opinion{}, use: allOn, want: base(),
		},

		// Kind.
		{
			name:  "kind fills other: materiality to base, relevance up",
			score: base(),
			op:    Opinion{Kind: model.EventResults, KindConfidence: 0.82},
			use:   Use{Kind: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) {
				s.Kind, s.Materiality, s.Relevance = model.EventResults, 3, 0.5
			}), "laya: kind results (0.82)"),
		},
		{
			name:  "kind fills an empty kind",
			score: with(base(), func(s *model.Score) { s.Kind = "" }),
			op:    Opinion{Kind: model.EventNonReliance, KindConfidence: 0.9},
			use:   Use{Kind: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) {
				s.Kind, s.Materiality, s.Relevance = model.EventNonReliance, 4, 0.65
			}), "laya: kind non_reliance (0.90)"),
		},
		{
			name:  "kind keeps a higher lens materiality and moves relevance by the gain only",
			score: with(base(), func(s *model.Score) { s.Materiality, s.Relevance = 4, 0.65 }),
			op:    Opinion{Kind: model.EventResults, KindConfidence: 0.8}, // base 3 < 4
			use:   Use{Kind: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) {
				s.Kind, s.Materiality, s.Relevance = model.EventResults, 4, 0.65
			}), "laya: kind results (0.80)"),
		},
		{
			name:  "kind at exactly the minimum confidence is confident",
			score: base(),
			op:    Opinion{Kind: model.EventDividend, KindConfidence: 0.7},
			use:   Use{Kind: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) {
				s.Kind, s.Materiality, s.Relevance = model.EventDividend, 3, 0.5
			}), "laya: kind dividend (0.70)"),
		},
		{
			name:  "noise kind caps relevance even without a gain",
			score: with(base(), func(s *model.Score) { s.Relevance = 0.5 }),
			op:    Opinion{Kind: model.EventAnalystTgt, KindConfidence: 0.9},
			use:   Use{Kind: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) {
				s.Kind, s.Relevance = model.EventAnalystTgt, 0.35
			}), "laya: kind analyst_target (0.90)"),
		},
		{
			name:  "noise kind leaves a lower relevance alone",
			score: with(base(), func(s *model.Score) { s.Materiality, s.Relevance = 2, 0.35 }),
			op:    Opinion{Kind: model.EventPriceChatter, KindConfidence: 0.9},
			use:   Use{Kind: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) {
				s.Kind, s.Materiality, s.Relevance = model.EventPriceChatter, 2, 0.35
			}), "laya: kind price_chatter (0.90)"),
		},
		{
			name:  "kind not confident is unsure",
			score: base(),
			op:    Opinion{Kind: model.EventResults, KindConfidence: 0.69},
			use:   Use{Kind: true}, min: 0.7,
			want: added(base(), "laya (unsure): kind results (0.69)"),
		},
		{
			name:  "kind switched off is advisory",
			score: base(),
			op:    Opinion{Kind: model.EventResults, KindConfidence: 0.95},
			min:   0.7,
			want:  added(base(), "laya (advisory): kind results (0.95)"),
		},
		{
			name:  "kind other from laya is advisory, never applied",
			score: base(),
			op:    Opinion{Kind: model.EventOther, KindConfidence: 0.95},
			use:   Use{Kind: true}, min: 0.7,
			want: added(base(), "laya (advisory): kind other (0.95)"),
		},
		{
			name:  "invalid kind is ignored",
			score: base(),
			op:    Opinion{Kind: "mergers", KindConfidence: 0.95},
			use:   Use{Kind: true}, min: 0.7,
			want: base(),
		},
		{
			name:  "kind disagreement with a real lens kind is not applied",
			score: with(base(), func(s *model.Score) { s.Kind, s.Materiality, s.Relevance = model.EventResults, 3, 0.5 }),
			op:    Opinion{Kind: model.EventGuidance, KindConfidence: 0.9},
			use:   Use{Kind: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Kind, s.Materiality, s.Relevance = model.EventResults, 3, 0.5 }),
				"laya (disagrees): kind guidance (0.90) against results"),
		},
		{
			name:  "kind disagreement is noted even with the switch off",
			score: with(base(), func(s *model.Score) { s.Kind = model.EventDividend }),
			op:    Opinion{Kind: model.EventBuyback, KindConfidence: 0.9},
			min:   0.7,
			want: added(with(base(), func(s *model.Score) { s.Kind = model.EventDividend }),
				"laya (disagrees): kind buyback (0.90) against dividend"),
		},
		{
			name:  "unconfident disagreement is unsure",
			score: with(base(), func(s *model.Score) { s.Kind = model.EventDividend }),
			op:    Opinion{Kind: model.EventBuyback, KindConfidence: 0.3},
			use:   Use{Kind: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Kind = model.EventDividend }),
				"laya (unsure): kind buyback (0.30)"),
		},
		{
			name:  "agreeing kind is advisory",
			score: with(base(), func(s *model.Score) { s.Kind = model.EventDividend }),
			op:    Opinion{Kind: model.EventDividend, KindConfidence: 0.9},
			use:   Use{Kind: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Kind = model.EventDividend }),
				"laya (advisory): kind dividend (0.90)"),
		},

		// Tone.
		{
			name:  "tone fills zero and is rounded",
			score: base(),
			op:    Opinion{Tone: -0.456, ToneConfidence: 0.8},
			use:   Use{Tone: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Tone = -0.46 }), "laya: tone -0.46"),
		},
		{
			name:  "tone is limited to -1..1",
			score: base(),
			op:    Opinion{Tone: 1.7, ToneConfidence: 0.8},
			use:   Use{Tone: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Tone = 1 }), "laya: tone 1.00"),
		},
		{
			name:  "tone already set by the lens is advisory",
			score: with(base(), func(s *model.Score) { s.Tone = 0.2 }),
			op:    Opinion{Tone: -0.5, ToneConfidence: 0.8},
			use:   Use{Tone: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Tone = 0.2 }), "laya (advisory): tone -0.50 (0.80)"),
		},
		{
			name:  "tone unconfident is unsure",
			score: base(),
			op:    Opinion{Tone: 0.5, ToneConfidence: 0.4},
			use:   Use{Tone: true}, min: 0.7,
			want: added(base(), "laya (unsure): tone 0.50 (0.40)"),
		},
		{
			name:  "tone switched off is advisory",
			score: base(),
			op:    Opinion{Tone: 0.5, ToneConfidence: 0.9},
			min:   0.7,
			want:  added(base(), "laya (advisory): tone 0.50 (0.90)"),
		},
		{
			name:  "tone NaN is ignored",
			score: base(),
			op:    Opinion{Tone: math.NaN(), ToneConfidence: 0.9},
			use:   Use{Tone: true}, min: 0.7,
			want: base(),
		},

		// Sector.
		{
			name:  "sector fills empty",
			score: base(),
			op:    Opinion{Sector: "energy", SectorConfidence: 0.75},
			use:   Use{Sector: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Sector = "energy" }), "laya: sector energy (0.75)"),
		},
		{
			name:  "sector never replaces the classifier's",
			score: with(base(), func(s *model.Score) { s.Sector = "technology" }),
			op:    Opinion{Sector: "consumer-discretionary", SectorConfidence: 0.99},
			use:   allOn, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Sector = "technology" }),
				"laya (advisory): sector consumer-discretionary (0.99)"),
		},
		{
			name:  "sector unconfident is unsure",
			score: base(),
			op:    Opinion{Sector: "energy", SectorConfidence: 0.2},
			use:   Use{Sector: true}, min: 0.7,
			want: added(base(), "laya (unsure): sector energy (0.20)"),
		},
		{
			name:  "sector switched off is advisory",
			score: base(),
			op:    Opinion{Sector: "energy", SectorConfidence: 0.8},
			min:   0.7,
			want:  added(base(), "laya (advisory): sector energy (0.80)"),
		},

		// Topic.
		{
			name:  "topic is returned",
			score: base(),
			op:    Opinion{Topic: "oil-and-gas", TopicConfidence: 0.9},
			use:   Use{Topics: true}, min: 0.7,
			want:      added(base(), "laya: topic oil-and-gas (0.90)"),
			wantTopic: "oil-and-gas",
		},
		{
			name:  "topic switched off is advisory and not returned",
			score: base(),
			op:    Opinion{Topic: "oil-and-gas", TopicConfidence: 0.9},
			min:   0.7,
			want:  added(base(), "laya (advisory): topic oil-and-gas (0.90)"),
		},
		{
			name:  "topic unconfident is unsure and not returned",
			score: base(),
			op:    Opinion{Topic: "oil-and-gas", TopicConfidence: 0.5},
			use:   Use{Topics: true}, min: 0.7,
			want: added(base(), "laya (unsure): topic oil-and-gas (0.50)"),
		},
		{
			name:  "no topic means nothing to say",
			score: base(),
			op:    Opinion{Topic: "", TopicConfidence: 0.9},
			use:   Use{Topics: true}, min: 0.7,
			want: base(),
		},

		// Materiality.
		{
			name:  "materiality rounds the mean up and raises relevance",
			score: with(base(), func(s *model.Score) { s.Materiality, s.Relevance = 3, 0.5 }),
			op:    Opinion{Materiality: 4.4, MaterialityConfidence: 0.8},
			use:   Use{Materiality: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Materiality, s.Relevance = 4, 0.65 }),
				"laya: materiality 4 (4.4) (0.80)"),
		},
		{
			name:  "materiality lowers relevance by the steps lost",
			score: with(base(), func(s *model.Score) { s.Materiality, s.Relevance = 3, 0.5 }),
			op:    Opinion{Materiality: 1, MaterialityConfidence: 0.8},
			use:   Use{Materiality: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Materiality, s.Relevance = 2, 0.35 }),
				"laya: materiality 2 (1.0) (0.80)"),
		},
		{
			name:  "materiality is not raised above 5",
			score: with(base(), func(s *model.Score) { s.Materiality, s.Relevance = 5, 0.8 }),
			op:    Opinion{Materiality: 9, MaterialityConfidence: 0.8},
			use:   Use{Materiality: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Materiality, s.Relevance = 5, 0.8 }),
				"laya: materiality 5 (9.0) (0.80)"),
		},
		{
			name:  "materiality is not lowered below 1",
			score: base(),
			op:    Opinion{Materiality: -3, MaterialityConfidence: 0.8},
			use:   Use{Materiality: true}, min: 0.7,
			want: added(base(), "laya: materiality 1 (-3.0) (0.80)"),
		},
		{
			name:  "materiality shift respects the noise cap",
			score: with(base(), func(s *model.Score) { s.Kind, s.Materiality, s.Relevance = model.EventAnalystTgt, 1, 0.35 }),
			op:    Opinion{Materiality: 5, MaterialityConfidence: 0.8},
			use:   Use{Materiality: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Kind, s.Materiality, s.Relevance = model.EventAnalystTgt, 3, 0.35 }),
				"laya: materiality 3 (5.0) (0.80)"),
		},
		{
			name:  "materiality unconfident is unsure",
			score: with(base(), func(s *model.Score) { s.Materiality = 3 }),
			op:    Opinion{Materiality: 5, MaterialityConfidence: 0.1},
			use:   Use{Materiality: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Materiality = 3 }), "laya (unsure): materiality 5.0 (0.10)"),
		},
		{
			name:  "materiality switched off is advisory",
			score: base(),
			op:    Opinion{Materiality: 4.2, MaterialityConfidence: 0.8},
			min:   0.7,
			want:  added(base(), "laya (advisory): materiality 4.2 (0.80)"),
		},

		// Relevance.
		{
			name:  "relevance is blended by weight",
			score: with(base(), func(s *model.Score) { s.Relevance = 0.5 }),
			op:    Opinion{Relevance: 1, RelevanceConfidence: 0.9},
			use:   Use{Relevance: true, RelevanceWeight: 0.3}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Relevance = 0.65 }),
				"laya: relevance 0.650 (1.00, weight 0.30) (0.90)"),
		},
		{
			name:  "relevance weight above one counts as one",
			score: with(base(), func(s *model.Score) { s.Relevance = 0.5 }),
			op:    Opinion{Relevance: 0.1, RelevanceConfidence: 0.9},
			use:   Use{Relevance: true, RelevanceWeight: 4}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Relevance = 0.1 }),
				"laya: relevance 0.100 (0.10, weight 1.00) (0.90)"),
		},
		{
			name:  "relevance weight below zero counts as zero",
			score: with(base(), func(s *model.Score) { s.Relevance = 0.5 }),
			op:    Opinion{Relevance: 0.1, RelevanceConfidence: 0.9},
			use:   Use{Relevance: true, RelevanceWeight: -1}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Relevance = 0.5 }),
				"laya: relevance 0.500 (0.10, weight 0.00) (0.90)"),
		},
		{
			name:  "relevance is rounded to three decimals",
			score: with(base(), func(s *model.Score) { s.Relevance = 0.333 }),
			op:    Opinion{Relevance: 0.777, RelevanceConfidence: 0.9},
			use:   Use{Relevance: true, RelevanceWeight: 0.3}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Relevance = 0.466 }),
				"laya: relevance 0.466 (0.78, weight 0.30) (0.90)"),
		},
		{
			name:  "relevance respects the noise cap",
			score: with(base(), func(s *model.Score) { s.Kind, s.Relevance = model.EventPriceChatter, 0.3 }),
			op:    Opinion{Relevance: 1, RelevanceConfidence: 0.9},
			use:   Use{Relevance: true, RelevanceWeight: 1}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Kind, s.Relevance = model.EventPriceChatter, 0.35 }),
				"laya: relevance 0.350 (1.00, weight 1.00) (0.90)"),
		},
		{
			name:  "relevance outside 0..1 from the scorer is limited",
			score: with(base(), func(s *model.Score) { s.Relevance = 0.5 }),
			op:    Opinion{Relevance: 7, RelevanceConfidence: 0.9},
			use:   Use{Relevance: true, RelevanceWeight: 1}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Relevance = 1 }),
				"laya: relevance 1.000 (7.00, weight 1.00) (0.90)"),
		},
		{
			name:  "relevance unconfident is unsure",
			score: base(),
			op:    Opinion{Relevance: 0.1, RelevanceConfidence: 0.3},
			use:   Use{Relevance: true, RelevanceWeight: 0.3}, min: 0.7,
			want: added(base(), "laya (unsure): relevance 0.10 (0.30)"),
		},
		{
			name:  "relevance switched off is advisory",
			score: base(),
			op:    Opinion{Relevance: 0.1, RelevanceConfidence: 0.9},
			min:   0.7,
			want:  added(base(), "laya (advisory): relevance 0.10 (0.90)"),
		},

		// Several fields.
		{
			name:  "truncated opinion says so first",
			score: base(),
			op:    Opinion{Truncated: true, ToneConfidence: 0.2, Tone: 0.1},
			use:   Use{Tone: true}, min: 0.7,
			want: added(base(), "laya: text truncated", "laya (unsure): tone 0.10 (0.20)"),
		},
		{
			name:  "kind first then the materiality and relevance that depend on it",
			score: base(),
			op: Opinion{
				Kind: model.EventResults, KindConfidence: 0.9,
				Materiality: 5, MaterialityConfidence: 0.9,
				Relevance: 1, RelevanceConfidence: 0.9,
			},
			use: allOn, min: 0.7,
			// kind: materiality 1 to 3, relevance 0.2 to 0.5; materiality: (3+5)/2 = 4,
			// relevance 0.65; relevance: 0.7*0.65 + 0.3*1 = 0.755.
			want: added(with(base(), func(s *model.Score) {
				s.Kind, s.Materiality, s.Relevance = model.EventResults, 4, 0.755
			}), "laya: kind results (0.90)", "laya: materiality 4 (5.0) (0.90)", "laya: relevance 0.755 (1.00, weight 0.30) (0.90)"),
		},
		{
			name:  "a source name replaces laya in the reasons",
			score: base(),
			op:    Opinion{Source: "other-model", Tone: 0.5, ToneConfidence: 0.9},
			use:   Use{Tone: true}, min: 0.7,
			want: added(with(base(), func(s *model.Score) { s.Tone = 0.5 }), "other-model: tone 0.50"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.score
			in.Reasons = slices.Clone(tt.score.Reasons)
			got, topic := Apply(tt.score, tt.op, tt.use, tt.min)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("score =\n %+v\nwant\n %+v", got, tt.want)
			}
			if topic != tt.wantTopic {
				t.Errorf("topic = %q, want %q", topic, tt.wantTopic)
			}
			if !reflect.DeepEqual(tt.score, in) {
				t.Errorf("Apply modified its argument: %+v, was %+v", tt.score, in)
			}
		})
	}
}

// fullOpinion answers every field with a confidence of 0.9.
func fullOpinion() Opinion {
	return Opinion{
		Truncated: true,
		Relevance: 0.6, RelevanceConfidence: 0.9,
		Kind: model.EventResults, KindConfidence: 0.9,
		Materiality: 4, MaterialityConfidence: 0.9,
		Tone: 0.4, ToneConfidence: 0.9,
		Sector: "energy", SectorConfidence: 0.9,
		Topic: "oil-and-gas", TopicConfidence: 0.9,
	}
}

func TestApplyReasonsFollowTheFixedFieldOrder(t *testing.T) {
	order := []string{"text truncated", "kind", "tone", "sector", "topic", "materiality", "relevance"}
	for name, use := range map[string]Use{
		"all applied":  allOn,
		"all advisory": {},
	} {
		t.Run(name, func(t *testing.T) {
			s := base()
			got, _ := Apply(s, fullOpinion(), use, 0.7)
			extra := got.Reasons[len(s.Reasons):]
			if !slices.Equal(got.Reasons[:len(s.Reasons)], s.Reasons) {
				t.Errorf("the lens's reasons were changed: %q", got.Reasons)
			}
			if len(extra) != len(order) {
				t.Fatalf("added %d reasons, want %d: %q", len(extra), len(order), extra)
			}
			if len(extra) > 8 {
				t.Errorf("added %d reasons, at most 8 allowed", len(extra))
			}
			for i, field := range order {
				if !strings.Contains(extra[i], field) {
					t.Errorf("reason %d is %q, want it to be about %q", i, extra[i], field)
				}
			}

			again, _ := Apply(s, fullOpinion(), use, 0.7)
			if !reflect.DeepEqual(got, again) {
				t.Errorf("a second call gave a different score:\n%+v\n%+v", got, again)
			}
		})
	}
}

func TestApplyDoesNotShareTheReasonsSlice(t *testing.T) {
	s := base()
	s.Reasons = make([]string, 1, 16) // spare capacity that append would reuse
	s.Reasons[0] = "first"
	got, _ := Apply(s, fullOpinion(), allOn, 0.7)
	if len(s.Reasons) != 1 || s.Reasons[0] != "first" {
		t.Errorf("input reasons changed: %q", s.Reasons)
	}
	if &got.Reasons[0] == &s.Reasons[0] {
		t.Error("the result shares its reasons with the input")
	}
	if full := s.Reasons[:2]; full[1] != "" {
		t.Errorf("Apply wrote into the spare capacity of the input: %q", full)
	}
}

func TestApplyKeepsMaterialityAndRelevanceInRange(t *testing.T) {
	confs := []float64{0, 0.5, 1}
	values := []float64{-5, 0, 0.5, 1, 3.5, 5, 99}
	kinds := []model.EventKind{"", model.EventOther, model.EventResults, model.EventAnalystTgt, model.EventNonReliance}
	weights := []float64{-1, 0, 0.3, 1, 2}
	for _, kind := range kinds {
		for mat := 1; mat <= 5; mat++ {
			for _, rel := range []float64{0, 0.2, 0.35, 0.7, 1} {
				for _, v := range values {
					for _, w := range weights {
						for _, c := range confs {
							s := model.Score{Kind: kind, Materiality: mat, Relevance: rel}
							op := Opinion{
								Kind: model.EventPriceChatter, KindConfidence: c,
								Materiality: v, MaterialityConfidence: c,
								Relevance: v, RelevanceConfidence: c,
								Tone: v, ToneConfidence: c,
							}
							got, _ := Apply(s, op, Use{Kind: true, Materiality: true, Relevance: true, RelevanceWeight: w, Tone: true}, 0.5)
							if got.Materiality < 1 || got.Materiality > 5 {
								t.Fatalf("materiality %d out of 1..5 for %+v with %+v", got.Materiality, s, op)
							}
							if got.Relevance < 0 || got.Relevance > 1 || math.IsNaN(got.Relevance) {
								t.Fatalf("relevance %v out of 0..1 for %+v with %+v", got.Relevance, s, op)
							}
							if got.Tone < -1 || got.Tone > 1 {
								t.Fatalf("tone %v out of -1..1", got.Tone)
							}
							if noiseKinds[got.Kind] && got.Relevance > noiseCap && got.Relevance != s.Relevance {
								t.Fatalf("noise kind %s has relevance %v above the cap for %+v with %+v", got.Kind, got.Relevance, s, op)
							}
						}
					}
				}
			}
		}
	}
}

func TestApplyNeverTouchesWhatTheScorerDidNotAnswer(t *testing.T) {
	s := with(base(), func(s *model.Score) { s.Sector, s.Tone, s.Materiality = "energy", 0.3, 3 })
	// Only the tone is answered, and without confidence in the rest.
	got, _ := Apply(s, Opinion{Tone: 0.1, ToneConfidence: 0.99, Kind: model.EventResults}, allOn, 0.7)
	want := added(s, "laya (advisory): tone 0.10 (0.99)")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
