package eventfact

import (
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Passage is one piece of evidence text: a title, a summary or a claim sentence.
type Passage struct {
	Ref  string // the article ID or accession it comes from
	Text string
}

// EventText is what is known of a stored event when its figures are read.
type EventText struct {
	EventID, SecurityID, Symbol, CIK string
	Kind                             model.EventKind
	Items                            []string // 8-K item numbers of the evidence
	PublishedAt                      time.Time
	AvailableAt                      time.Time
	Precision                        string // PrecisionTime or PrecisionDay
	Source                           Source
	SourceRef                        string
	Evidence                         []string
	Basis                            Basis
	Passages                         []Passage
}
