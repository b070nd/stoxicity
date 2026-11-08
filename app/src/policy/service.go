package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Record is one activation: a policy version that became the policy in force at
// a time. Epoch only ever grows; anything bound to an older epoch (a plan, an
// order confirmation) is stale.
type Record struct {
	Epoch        int64     `json:"epoch"`
	Hash         string    `json:"hash"`
	At           time.Time `json:"at"`
	Policy       Policy    `json:"policy"`
	RiskAccepted bool      `json:"risk_accepted"`
	RiskNotes    []string  `json:"risk_notes,omitempty"`
	Changes      []Change  `json:"changes,omitempty"`
	Effects      []string  `json:"effects,omitempty"`
}

// Activation is what a store appends.
type Activation struct {
	PrevEpoch    int64 // the epoch the preview was made against; 0 for the first
	Hash         string
	Policy       Policy
	At           time.Time
	RiskAccepted bool
	RiskNotes    []string
	Changes      []Change
	Effects      []string
}

// Store keeps the activations append-only.
type Store interface {
	// Append records an activation. It fails with ErrStale when PrevEpoch is not
	// the newest epoch, so two previews cannot both be activated.
	Append(ctx context.Context, a Activation) (Record, error)
	// AsOf returns the policy in force at t (the newest when t is zero), and
	// false when none was active then.
	AsOf(ctx context.Context, t time.Time) (Record, bool, error)
	// History returns every activation, oldest first.
	History(ctx context.Context) ([]Record, error)
}

// ErrStale is an activation made against a policy that is no longer the newest.
var ErrStale = errors.New("policy: the policy in force changed after this preview; preview again")

// Preview is what activating a candidate would do. It changes nothing.
type Preview struct {
	Hash          string    `json:"hash,omitempty"` // empty when the candidate has errors
	Problems      []Problem `json:"problems"`
	Active        *Record   `json:"active,omitempty"`
	Unchanged     bool      `json:"unchanged"`
	Changes       []Change  `json:"changes"`
	RiskIncreases []string  `json:"risk_increases"`
	Effects       []string  `json:"effects"`
	Unknowns      []Unknown `json:"unknowns"`
}

// Blocked lists why the candidate cannot be activated, or nil.
func (pv Preview) Blocked() []string {
	var out []string
	for _, p := range Errors(pv.Problems) {
		out = append(out, p.String())
	}
	return out
}

// MakePreview checks the candidate and compares it with the policy in force.
// With errors it stops after the findings: a malformed policy has no stable
// content to hash or compare.
func MakePreview(cand Policy, active *Record, asOf time.Time) (Preview, error) {
	pv := Preview{Problems: Validate(cand, asOf), Changes: []Change{}, RiskIncreases: []string{}, Effects: []string{}, Unknowns: []Unknown{}}
	if cand.Example {
		pv.Problems = append(pv.Problems, Problem{Error, "example",
			"this is an example profile; read every value, replace what is wrong and set example to false before activating"})
	}
	if len(Errors(pv.Problems)) > 0 {
		return pv, nil
	}
	h, err := Hash(cand)
	if err != nil {
		return pv, err
	}
	pv.Hash = h
	pv.Unknowns = append(pv.Unknowns, Unknowns(cand)...)
	if active == nil {
		pv.Effects = []string{"this is the first policy; plans can be built once it is active"}
		return pv, nil
	}
	pv.Active = active
	if active.Hash == h {
		pv.Unchanged = true
		return pv, nil
	}
	if pv.Changes, err = Diff(active.Policy, cand); err != nil {
		return pv, err
	}
	pv.RiskIncreases = RiskIncreases(active.Policy, cand)
	pv.Effects = Effects(pv.Changes)
	return pv, nil
}

// Activate makes the candidate the policy in force. The consent must be the
// hash the preview printed, so what is activated is exactly what was read; a
// change that lets the portfolio take more risk also needs acceptRisk. Nothing
// is invalidated by deletion: later plans and confirmations compare their epoch
// with the newest one.
func Activate(ctx context.Context, st Store, cand Policy, consent string, acceptRisk bool, now time.Time) (Record, error) {
	cur, ok, err := st.AsOf(ctx, time.Time{})
	if err != nil {
		return Record{}, err
	}
	var active *Record
	if ok {
		active = &cur
	}
	pv, err := MakePreview(cand, active, now)
	if err != nil {
		return Record{}, err
	}
	if blocked := pv.Blocked(); len(blocked) > 0 {
		return Record{}, fmt.Errorf("policy: cannot activate:\n  %s", strings.Join(blocked, "\n  "))
	}
	if consent == "" || consent != pv.Hash {
		return Record{}, fmt.Errorf("policy: consent must be the hash of the previewed policy (%s)", pv.Hash)
	}
	if pv.Unchanged {
		return cur, nil // activating what is already in force changes nothing and records nothing
	}
	if len(pv.RiskIncreases) > 0 && !acceptRisk {
		return Record{}, fmt.Errorf("policy: this raises risk and needs -accept-risk:\n  %s", strings.Join(pv.RiskIncreases, "\n  "))
	}
	a := Activation{
		Hash: pv.Hash, Policy: cand, At: now, RiskAccepted: len(pv.RiskIncreases) > 0 && acceptRisk,
		RiskNotes: pv.RiskIncreases, Changes: pv.Changes, Effects: pv.Effects,
	}
	if active != nil {
		a.PrevEpoch = active.Epoch
	}
	return st.Append(ctx, a)
}

// Stale reports whether something bound to epoch was made under an older policy
// than the one now in force.
func Stale(bound, current int64) bool { return bound != current }
