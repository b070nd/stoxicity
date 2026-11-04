package lens

import "github.com/b070nd/stoxicity/app/src/model"

// Opinion is what a second scorer, such as laya, says about one article. The
// rule-based lens (stage L0) stays the base; an Opinion adds to it under the
// policy of Apply.
//
// Every judgement comes with the scorer's confidence in it, a probability in
// (0, 1]. A confidence of zero means the scorer gave no opinion on that field,
// and the field's value must be ignored. Confidence is the scorer's own
// number and is not calibrated until it has been measured against labels
// (package eval).
type Opinion struct {
	// Source names the scorer in reasons, for example "laya".
	Source string
	// Version identifies the model and the questions that produced the
	// opinion. It becomes part of the stored lens version, so a change of
	// either adds a new score row instead of overwriting the old one.
	Version string

	// Relevance is the probability that the article bears on the long-term
	// value of a business.
	Relevance           float64
	RelevanceConfidence float64

	// Group and Kind are the event family and the event within it. The kind
	// confidence is the lower of the two answers' confidences.
	Group           model.EventGroup
	GroupConfidence float64
	Kind            model.EventKind
	KindConfidence  float64

	// Materiality is an expected value between 1 and 5.
	Materiality           float64
	MaterialityConfidence float64

	// Tone runs from -1 (bad for the business) to 1 (good for it).
	Tone           float64
	ToneConfidence float64

	// Sector is one of the configured sector names.
	Sector           string
	SectorConfidence float64

	// Topic is the one configured topic that fits best, or empty if none does.
	Topic           string
	TopicConfidence float64

	// Truncated is true when the article text did not fit the model and the
	// answers saw only the start of it.
	Truncated bool
}

// Use says which of an Opinion's judgements change the score. A judgement
// that is not used is still recorded in the score's reasons, so that package
// eval and the user can compare it with the labels before switching it on.
type Use struct {
	// Kind replaces the kind when the lens found none (kind "other").
	Kind bool
	// Tone sets the tone, which the rule-based lens leaves at zero.
	Tone bool
	// Sector sets the sector when the classifier left it empty.
	Sector bool
	// Topics adds the topic to the article's topics.
	Topics bool
	// Materiality averages the lens's materiality with the opinion's.
	Materiality bool
	// Relevance blends the opinion's relevance into the lens's, with weight
	// RelevanceWeight (0 to 1) on the opinion.
	Relevance       bool
	RelevanceWeight float64
}
