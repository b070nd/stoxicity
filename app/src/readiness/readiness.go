// Package readiness says what the software can do, and on what evidence. The
// five dimensions are kept apart on purpose: code that exists is not code that
// was tested, a test against fixtures is not a check against the real source,
// and neither is a check against the user's actual account or a strategy that
// was evaluated on history. A claim of "yes" must point at the file that
// supports it, and a test keeps those pointers from rotting.
package readiness

import (
	"fmt"
	"slices"
	"sort"
)

// Dimension is one kind of readiness. They are not interchangeable.
type Dimension string

const (
	Implemented         Dimension = "implemented"
	FixtureVerified     Dimension = "fixture_verified"
	SourceVerified      Dimension = "source_verified"
	AccountVerified     Dimension = "account_verified"
	InvestmentEvaluated Dimension = "investment_evaluated"
)

// Dimensions in the order a report shows them.
var Dimensions = []Dimension{Implemented, FixtureVerified, SourceVerified, AccountVerified, InvestmentEvaluated}

// Level is the state of one dimension of one capability.
type Level string

const (
	Yes           Level = "yes"
	No            Level = "no"
	Unknown       Level = "unknown" // nobody has recorded an answer; this is not "no"
	NotApplicable Level = "not_applicable"
)

// Scope groups the capabilities by the workflow they belong to.
type Scope string

const (
	Research  Scope = "research"
	Portfolio Scope = "portfolio"
	Broker    Scope = "broker"
)

// Scopes lists the valid scopes.
var Scopes = []Scope{Research, Portfolio, Broker}

// ParseScope reads a scope name.
func ParseScope(s string) (Scope, error) {
	for _, k := range Scopes {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("unknown scope %q (research, portfolio or broker)", s)
}

// Capability is one thing the product does or is to do.
type Capability struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scope Scope  `json:"scope"`
	// Story is the production-plan story that delivers it (US-nnn), or "" for
	// what existed before the plan.
	Story string `json:"story,omitempty"`

	Levels map[Dimension]Level `json:"levels"`
	// Evidence names, per dimension answered "yes", the repository file or
	// directory that supports the claim. A "yes" without it is a defect.
	Evidence map[Dimension]string `json:"evidence,omitempty"`
	// Blocker says what stands in the way of the next dimension, in plain words.
	Blocker string `json:"blocker,omitempty"`
}

// Level returns the level of a dimension; a missing entry is Unknown.
func (c Capability) Level(d Dimension) Level {
	if l, ok := c.Levels[d]; ok {
		return l
	}
	return Unknown
}

// Gap is a known weakness that is not a missing feature.
type Gap struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
	Story   string `json:"story,omitempty"`
	Source  string `json:"source"`
}

// State is the runtime state of one doctor check.
type State string

const (
	StateImplemented State = "implemented" // the code is there, nothing was configured or tried
	StateConfigured  State = "configured"  // settings exist
	StateVerified    State = "verified"    // checked just now and it worked
	StateStale       State = "stale"       // worked, but not recently enough
	StateUnsupported State = "unsupported" // the product does not do this
	StateUnknown     State = "unknown"     // not checked
	StateFailed      State = "failed"      // the check ran and did not pass
)

// Check is one doctor result.
type Check struct {
	Name   string `json:"name"`
	Level  string `json:"level"` // OK, WARN or FAIL
	State  State  `json:"state"`
	Detail string `json:"detail"`
}

// Schema describes the databases: what this binary knows and what is on disk.
type Schema struct {
	KnownBase   int  `json:"known_base"`
	KnownTenant int  `json:"known_tenant"`
	AppliedBase int  `json:"applied_base"`
	BaseExists  bool `json:"base_exists"`
	PendingBase int  `json:"pending_base"`
}

// ReportSchema names the version of the JSON report.
const ReportSchema = "stoxicity.doctor/1"

// Report is the machine-readable doctor output.
type Report struct {
	Schema       string       `json:"schema"`
	Version      string       `json:"version"`
	Scope        Scope        `json:"scope"`
	Checks       []Check      `json:"checks"`
	Database     Schema       `json:"database"`
	Capabilities []Capability `json:"capabilities"`
	Gaps         []Gap        `json:"gaps"`
	Summary      Summary      `json:"summary"`
}

// Summary counts the capabilities per dimension, so the five readinesses are
// never collapsed into one number.
type Summary struct {
	Total  int                         `json:"total"`
	Counts map[Dimension]map[Level]int `json:"counts"`
}

// Summarize counts the levels of caps.
func Summarize(caps []Capability) Summary {
	s := Summary{Total: len(caps), Counts: map[Dimension]map[Level]int{}}
	for _, d := range Dimensions {
		s.Counts[d] = map[Level]int{}
		for _, c := range caps {
			s.Counts[d][c.Level(d)]++
		}
	}
	return s
}

// InScope returns the capabilities of one scope, in a stable order.
func InScope(caps []Capability, scope Scope) []Capability {
	var out []Capability
	for _, c := range caps {
		if c.Scope == scope {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Build assembles a report for one scope.
func Build(version string, scope Scope, checks []Check, db Schema) Report {
	caps := InScope(Inventory(), scope)
	gaps := slices.Clone(Gaps())
	return Report{
		Schema: ReportSchema, Version: version, Scope: scope,
		Checks: checks, Database: db, Capabilities: caps, Gaps: gaps,
		Summary: Summarize(caps),
	}
}
