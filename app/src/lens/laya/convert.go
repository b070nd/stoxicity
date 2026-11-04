package laya

import (
	"encoding/json"
	"math"

	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
)

// source is the name of this scorer in an Opinion.
const source = "laya"

// scoreLevels is how many levels the materiality and tone questions have.
const scoreLevels = 5

// answer is one answer from the server, with the fields of all three types.
type answer struct {
	Type             string   `json:"type"`
	Noul             *float64 `json:"noul"`
	Choice           string   `json:"choice"`
	Score            *float64 `json:"score"`
	AnswerConfidence float64  `json:"answer_confidence"`
}

// answer decodes the answer to question id when it has the wanted type. An
// answer that is missing, of another type or of a shape not known gives false.
func (r result) answer(id, typ string) (answer, bool) {
	raw, ok := r.Answers[id]
	if !ok {
		return answer{}, false
	}
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil || a.Type != typ {
		return answer{}, false
	}
	a.AnswerConfidence = clamp(a.AnswerConfidence, 0, 1)
	return a, true
}

// firstRound converts the answers of round 1 into an Opinion.
func (s *Scorer) firstRound(r result) lens.Opinion {
	op := lens.Opinion{Source: source, Version: s.version, Truncated: r.Usage.Truncated}

	if a, ok := r.answer(qRelevant, typeNoul); ok && a.Noul != nil {
		op.Relevance, op.RelevanceConfidence = clamp(*a.Noul, 0, 1), a.AnswerConfidence
	}
	if a, ok := r.answer(qGroup, typeChoice); ok && model.EventGroup(a.Choice).Valid() {
		op.Group, op.GroupConfidence = model.EventGroup(a.Choice), a.AnswerConfidence
	}
	if a, ok := r.answer(qMateriality, typeScore); ok && a.Score != nil {
		op.Materiality = clamp(*a.Score, 0, scoreLevels-1) + 1
		op.MaterialityConfidence = a.AnswerConfidence
	}
	if a, ok := r.answer(qTone, typeScore); ok && a.Score != nil {
		op.Tone = (clamp(*a.Score, 0, scoreLevels-1) - 2) / 2
		op.ToneConfidence = a.AnswerConfidence
	}
	if a, ok := r.answer(qSector, typeChoice); ok && s.sectorSet[a.Choice] {
		op.Sector, op.SectorConfidence = a.Choice, a.AnswerConfidence
	}
	if a, ok := r.answer(qTopic, typeChoice); ok && s.topicSet[a.Choice] {
		op.Topic, op.TopicConfidence = a.Choice, a.AnswerConfidence
	}
	return op
}

// secondRound adds the kind answered for group g to op. The kind counts only
// when it belongs to g; its confidence is the lower of the group's and its own.
func (s *Scorer) secondRound(op *lens.Opinion, g model.EventGroup, r result) {
	op.Truncated = op.Truncated || r.Usage.Truncated
	a, ok := r.answer(qKind, typeChoice)
	if !ok {
		return
	}
	k := model.EventKind(a.Choice)
	if !k.Valid() || k.Group() != g {
		return
	}
	op.Kind, op.KindConfidence = k, math.Min(op.GroupConfidence, a.AnswerConfidence)
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
