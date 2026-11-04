package lens

import (
	"fmt"
	"math"
	"slices"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Apply merges a second scorer's Opinion into the score of the rule-based lens
// and returns the new score and the topic to add to the article ("" if none).
// The rule-based score stays the base; the opinion is never trusted blindly.
//
// A field of the opinion is present when its confidence is above zero, and
// confident when its confidence is at least minConfidence. Fields are handled
// in this order, each seeing the score the earlier ones produced:
//
//   - Kind (use.Kind) is applied only when the lens found no kind (other or
//     empty), the opinion's kind is valid and not other, and it is confident.
//     The kind is set, the materiality becomes the larger of the current one and
//     the kind's base, the relevance moves by 0.12 for each step of materiality
//     gained, and a noise kind caps it at 0.35. A confident kind that differs
//     from a real kind of the lens is not applied and is noted as a
//     disagreement.
//   - Tone (use.Tone) is set when confident and the lens left it at zero.
//   - Sector (use.Sector) is set when confident and the sector is empty.
//   - Topic (use.Topics) is returned when confident.
//   - Materiality (use.Materiality) becomes the rounded mean of the lens's and
//     the opinion's, within 1 to 5, and the relevance moves by 0.12 per step.
//   - Relevance (use.Relevance) becomes (1-w)*lens + w*opinion with w =
//     use.RelevanceWeight limited to 0..1.
//
// Relevance stays within [0,1], is rounded to 3 decimals and is capped at 0.35
// while the kind is a noise kind.
//
// Every present field that was not applied adds a reason: "advisory" when it
// was confident but switched off or the lens already had a value, "unsure"
// when it was not confident. The reasons follow the lens's own in the order
// kind, tone, sector, topic, materiality, relevance, after a note when the
// text was truncated. An opinion without any confidence changes nothing and
// adds no reason. Apply is a pure function of its arguments and does not
// modify score.Reasons.
func Apply(score model.Score, op Opinion, use Use, minConfidence float64) (model.Score, string) {
	if !anyPresent(op) {
		return score, ""
	}
	a := applier{
		score:  score,
		op:     op,
		use:    use,
		min:    minConfidence,
		source: op.Source,
	}
	if a.source == "" {
		a.source = "laya"
	}
	a.score.Reasons = slices.Clone(score.Reasons)
	if op.Truncated {
		a.add("%s: text truncated", a.source)
	}
	a.kind()
	a.tone()
	a.sector()
	topic := a.topic()
	a.materiality()
	a.relevance()
	return a.score, topic
}

// anyPresent reports whether the opinion holds an answer at all.
func anyPresent(op Opinion) bool {
	for _, c := range []float64{
		op.RelevanceConfidence, op.GroupConfidence, op.KindConfidence,
		op.MaterialityConfidence, op.ToneConfidence, op.SectorConfidence, op.TopicConfidence,
	} {
		if present(c) {
			return true
		}
	}
	return false
}

// present reports whether a confidence says the scorer answered. NaN does not.
func present(conf float64) bool { return conf > 0 }

// applier carries the score through the steps of Apply.
type applier struct {
	score  model.Score
	op     Opinion
	use    Use
	min    float64
	source string
}

func (a *applier) add(format string, args ...any) {
	a.score.Reasons = append(a.score.Reasons, fmt.Sprintf(format, args...))
}

func (a *applier) confident(conf float64) bool { return conf >= a.min }

// note records a present field that was not applied: unsure when the scorer was
// not confident, advisory otherwise.
func (a *applier) note(conf float64, field, value string) {
	if !a.confident(conf) {
		a.add("%s (unsure): %s %s (%.2f)", a.source, field, value, conf)
		return
	}
	a.add("%s (advisory): %s %s (%.2f)", a.source, field, value, conf)
}

func (a *applier) kind() {
	op := a.op
	if !present(op.KindConfidence) || !op.Kind.Valid() {
		return
	}
	value := string(op.Kind)
	conf := op.KindConfidence
	lensKind := a.score.Kind
	switch {
	case lensKind != model.EventOther && lensKind != "":
		if a.confident(conf) && op.Kind != lensKind {
			a.add("%s (disagrees): kind %s (%.2f) against %s", a.source, value, conf, lensKind)
			return
		}
		a.note(conf, "kind", value)
	case a.use.Kind && a.confident(conf) && op.Kind != model.EventOther:
		before := a.score.Materiality
		a.score.Kind = op.Kind
		a.score.Materiality = max(before, op.Kind.BaseMateriality())
		a.moveRelevance(a.score.Materiality - before)
		a.add("%s: kind %s (%.2f)", a.source, value, conf)
	default:
		a.note(conf, "kind", value)
	}
}

func (a *applier) tone() {
	op := a.op
	if !present(op.ToneConfidence) || !finite(op.Tone) {
		return
	}
	tone := math.Round(math.Min(math.Max(op.Tone, -1), 1)*100) / 100
	value := fmt.Sprintf("%.2f", tone)
	if a.use.Tone && a.confident(op.ToneConfidence) && a.score.Tone == 0 {
		a.score.Tone = tone
		a.add("%s: tone %s", a.source, value)
		return
	}
	a.note(op.ToneConfidence, "tone", value)
}

func (a *applier) sector() {
	op := a.op
	if !present(op.SectorConfidence) || op.Sector == "" {
		return
	}
	if a.use.Sector && a.confident(op.SectorConfidence) && a.score.Sector == "" {
		a.score.Sector = op.Sector
		a.add("%s: sector %s (%.2f)", a.source, op.Sector, op.SectorConfidence)
		return
	}
	a.note(op.SectorConfidence, "sector", op.Sector)
}

// topic returns the topic to add, if it is used and confident.
func (a *applier) topic() string {
	op := a.op
	if !present(op.TopicConfidence) || op.Topic == "" {
		return ""
	}
	if a.use.Topics && a.confident(op.TopicConfidence) {
		a.add("%s: topic %s (%.2f)", a.source, op.Topic, op.TopicConfidence)
		return op.Topic
	}
	a.note(op.TopicConfidence, "topic", op.Topic)
	return ""
}

func (a *applier) materiality() {
	op := a.op
	if !present(op.MaterialityConfidence) || !finite(op.Materiality) {
		return
	}
	value := fmt.Sprintf("%.1f", op.Materiality)
	if !a.use.Materiality || !a.confident(op.MaterialityConfidence) {
		a.note(op.MaterialityConfidence, "materiality", value)
		return
	}
	before := a.score.Materiality
	a.score.Materiality = min(max(int(math.Round((float64(before)+op.Materiality)/2)), 1), 5)
	a.moveRelevance(a.score.Materiality - before)
	a.add("%s: materiality %d (%s) (%.2f)", a.source, a.score.Materiality, value, op.MaterialityConfidence)
}

func (a *applier) relevance() {
	op := a.op
	if !present(op.RelevanceConfidence) || !finite(op.Relevance) {
		return
	}
	value := fmt.Sprintf("%.2f", op.Relevance)
	if !a.use.Relevance || !a.confident(op.RelevanceConfidence) {
		a.note(op.RelevanceConfidence, "relevance", value)
		return
	}
	w := clamp01(a.use.RelevanceWeight)
	rel := (1-w)*a.score.Relevance + w*clamp01(op.Relevance)
	a.score.Relevance = a.limit(rel)
	a.add("%s: relevance %.3f (%s, weight %.2f) (%.2f)", a.source, a.score.Relevance, value, w, op.RelevanceConfidence)
}

// moveRelevance changes the relevance by steps levels of materiality, which
// may be negative or zero.
func (a *applier) moveRelevance(steps int) {
	a.score.Relevance = a.limit(a.score.Relevance + perLevel*float64(steps))
}

// limit keeps rel within [0,1], under the cap of a noise kind, rounded to 3
// decimals.
func (a *applier) limit(rel float64) float64 {
	rel = clamp01(rel)
	if noiseKinds[a.score.Kind] && rel > noiseCap {
		rel = noiseCap
	}
	return math.Round(rel*1000) / 1000
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
