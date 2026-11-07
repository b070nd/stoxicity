package model

import "time"

// An event is one thing that happened. An Article is evidence about an event,
// not the event itself: ten syndicated copies are one event, one filing may be
// several. What is understood about an event is kept as numbered revisions that
// are only ever appended; a correction is a new revision, so both "what do we
// believe now" and "what did the system know at that time" can be answered.

// Role is the relationship between a security and an event as seen in the
// evidence. It is an observation, not a judgement of what the event does to the
// price.
type Role string

// The roles. A security with a stronger role is the more certain to be what the
// event is about.
const (
	RoleSubject      Role = "subject"      // the event is about it (the filer, the company named in the title)
	RoleCounterparty Role = "counterparty" // the other side of the event
	RoleMentioned    Role = "mentioned"    // named in the evidence, not what it is about
)

// Strength orders the roles: a larger number is a stronger role.
func (r Role) Strength() int {
	switch r {
	case RoleSubject:
		return 3
	case RoleCounterparty:
		return 2
	case RoleMentioned:
		return 1
	}
	return 0
}

// EventSecurity ties an event to a security. SecurityID is the internal stable
// identity; Ticker is its label at the time and is only needed to resolve it.
type EventSecurity struct {
	SecurityID string
	Ticker     string
	Role       Role
}

// Evidence is an observation an event revision rests on: the article ID and the
// source that said it.
type Evidence struct {
	ArticleID, SourceID string
}

// Relations of evidence to an event.
const (
	Supports    = "supports"
	Contradicts = "contradicts"
)

// Assessment is an interpretation of an event for one security. It is kept
// apart from the observation: it has its own horizon, confidence and
// rationale, and the version of the rule or model that made it.
type Assessment struct {
	SecurityID string
	Rule       string // lens version or model name
	Effect     string // what is assessed, e.g. "materiality"
	Direction  string // "up", "down" or "unknown": a relationship is not a direction
	Horizon    string // "unspecified", "1d", "1y"...
	Confidence float64
	Rationale  string
	At         time.Time
}

// Directions of an assessment.
const (
	DirectionUnknown = "unknown"
	DirectionUp      = "up"
	DirectionDown    = "down"
)

// EventRevision is one understanding of an event.
type EventRevision struct {
	EventID      string
	Revision     int
	Kind         EventKind
	Status       string // "reported" or "corrected"
	Title        string
	EffectiveDay string    // the day the action happened or takes effect, when known
	PublicAt     time.Time // when the earliest evidence was published
	RecordedAt   time.Time // when this system committed the revision
	Materiality  int
	Relevance    float64
	LensVersion  string
	Supersedes   int
	Securities   []EventSecurity
	Evidence     []Evidence
	Topics       []string
	Assessments  []Assessment
}

// Event statuses.
const (
	StatusReported  = "reported"
	StatusCorrected = "corrected"
)
