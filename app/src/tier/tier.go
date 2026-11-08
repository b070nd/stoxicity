// Package tier keeps the progression tiers apart from the investment limits.
//
// A tier says what the program may DO (paper only, a manual real portfolio,
// confirmed execution through a broker). The limits on how much risk the money
// may take are the policy's, and a higher tier does not loosen them: more
// reliable order handling is not a reason for more investment risk.
//
// A tier is unlocked by readiness evidence the user has recorded, with a source
// for each item, and by the user's explicit acceptance of the risk. Profit, a
// streak, or time passing cannot unlock it: nothing in this package takes a
// return, a profit or a count of wins as input, and elapsed sessions are only
// one necessary item among the rest. Health problems suspend NEW risk and
// revoke confirmations that were not sent; they never cancel an order the broker
// holds or sell a holding.
package tier

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Permissions are what a tier lets the program do. They are operational, not
// investment limits.
type Permissions struct {
	Paper            bool `json:"paper"`
	ManualReal       bool `json:"manual_real"`
	BrokerSubmission bool `json:"broker_submission"`
	EventSleeveReal  bool `json:"event_sleeve_real"`
}

// Name of a tier, from the plan's template.
func Name(t int) string {
	switch t {
	case 0:
		return "learn and build"
	case 1:
		return "conservative ownership"
	case 2:
		return "confirmed execution"
	case 3:
		return "broader discretionary"
	}
	return "unknown"
}

// PermissionsOf is the operational permissions of a tier.
func PermissionsOf(t int) Permissions {
	return Permissions{Paper: true, ManualReal: t >= 1, BrokerSubmission: t >= 2, EventSleeveReal: t >= 2}
}

// Item of readiness evidence.
type Item string

// The readiness items of section 5.9.
const (
	ConfigComplete     Item = "config_complete"       // configuration completed: policy, instruments, fees
	RecordsReconciled  Item = "records_reconciled"    // real and paper records reconciled
	BrokerClean        Item = "no_broker_discrepancy" // no unresolved broker discrepancies
	FailureScenarios   Item = "failure_scenarios"     // failure scenarios passed
	EvaluationAdequate Item = "evaluation_adequate"   // strategy and data evaluation adequate for the requested use
	Tier3Review        Item = "tier3_review"          // a new review and explicit policy for tier 3
)

// Describe says what an item means, for the user who attests it.
func (i Item) Describe() string {
	switch i {
	case ConfigComplete:
		return "the configuration is complete: an active reviewed policy, instruments, fee schedule and account facts"
	case RecordsReconciled:
		return "real and paper records are reconciled against statements, with no open exception"
	case BrokerClean:
		return "no unresolved difference between the broker and the local records"
	case FailureScenarios:
		return "the failure scenarios were run and passed (timeouts, restarts, cancel races, partial fills, stale quotes)"
	case EvaluationAdequate:
		return "the strategy and data evaluation is adequate for the use requested, not merely for paper practice"
	case Tier3Review:
		return "a new review of the account, the limits and the strategy was done, and an explicit policy for tier 3 is written"
	}
	return string(i)
}

// Required lists the evidence a target tier needs.
func Required(target int) []Item {
	switch target {
	case 1:
		return []Item{ConfigComplete, RecordsReconciled}
	case 2:
		return []Item{ConfigComplete, RecordsReconciled, BrokerClean, FailureScenarios, EvaluationAdequate}
	case 3:
		return []Item{ConfigComplete, RecordsReconciled, BrokerClean, FailureScenarios, EvaluationAdequate, Tier3Review}
	}
	return nil
}

// Evidence is one dated, sourced statement that an item is met (or not).
type Evidence struct {
	Target int       `json:"target"`
	Item   Item      `json:"item"`
	Met    bool      `json:"met"`
	Ref    string    `json:"ref"` // where the proof is: a reconciliation run, a document, a test report
	Note   string    `json:"note,omitempty"`
	By     string    `json:"by"`
	At     time.Time `json:"at"`
}

// Validate refuses evidence without a source.
func (e Evidence) Validate() error {
	ok := false
	for _, i := range Required(e.Target) {
		ok = ok || i == e.Item
	}
	switch {
	case !ok:
		return fmt.Errorf("tier: %q is not evidence for tier %d", e.Item, e.Target)
	case strings.TrimSpace(e.Ref) == "":
		return errors.New("tier: evidence needs a source (-ref): a statement that something is met, with nothing to look at, is not evidence")
	case strings.TrimSpace(e.By) == "":
		return errors.New("tier: evidence says who attests it")
	}
	return nil
}

// Config holds the editable thresholds. The numbers are illustrations until you
// have read them and marked them reviewed.
type Config struct {
	// ObservationSessions is the completed market sessions to observe before a
	// tier is considered, by target tier. It is a floor, never a reason.
	ObservationSessions map[int]int `json:"observation_sessions"`
	// DecisionValidDays is how long an unlock decision may wait for the policy
	// that uses it.
	DecisionValidDays int  `json:"decision_valid_days"`
	Reviewed          bool `json:"reviewed"`
}

// ExampleConfig is the plan's illustration: 30 sessions, unreviewed.
func ExampleConfig() Config {
	return Config{ObservationSessions: map[int]int{1: 0, 2: 30, 3: 30}, DecisionValidDays: 7}
}

// Validate checks the numbers are sane.
func (c Config) Validate() error {
	for t, n := range c.ObservationSessions {
		if t < 1 || t > 3 || n < 0 || n > 1000 {
			return fmt.Errorf("tier: observation_sessions for tier %d is %d: tiers are 1 to 3 and sessions 0 to 1000", t, n)
		}
	}
	if c.DecisionValidDays < 1 || c.DecisionValidDays > 90 {
		return errors.New("tier: decision_valid_days must be 1 to 90")
	}
	return nil
}

// Row is the state of one readiness item.
type Row struct {
	Item   Item   `json:"item"`
	Met    bool   `json:"met"`
	Ref    string `json:"ref,omitempty"`
	By     string `json:"by,omitempty"`
	At     string `json:"at,omitempty"`
	Note   string `json:"note,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Readiness is the assessment of one target tier.
type Readiness struct {
	Target   int      `json:"target"`
	Rows     []Row    `json:"rows"`
	Sessions int      `json:"sessions"`
	Floor    int      `json:"floor"`
	Reviewed bool     `json:"thresholds_reviewed"`
	Ready    bool     `json:"ready"`
	Missing  []string `json:"missing"`
	// Hash identifies the evidence this assessment rests on: a decision is bound
	// to it, and new evidence makes an older decision stale.
	Hash string `json:"hash"`
}

// Assess says whether the evidence supports the target tier. It takes no
// return, profit or count of wins: sessions is only elapsed observation, and it
// satisfies one necessary condition among several.
func Assess(target int, evidence map[Item]Evidence, cfg Config, sessions int) (Readiness, error) {
	need := Required(target)
	if need == nil {
		return Readiness{}, fmt.Errorf("tier: there is nothing to unlock for tier %d (1 to 3)", target)
	}
	r := Readiness{Target: target, Sessions: sessions, Floor: cfg.ObservationSessions[target], Reviewed: cfg.Reviewed}
	for _, it := range need {
		ev, ok := evidence[it]
		row := Row{Item: it}
		switch {
		case !ok:
			row.Reason = "no evidence recorded"
		case !ev.Met:
			row.Ref, row.By, row.At, row.Note = ev.Ref, ev.By, ev.At.UTC().Format(time.DateOnly), ev.Note
			row.Reason = "recorded as not met"
		default:
			row.Met, row.Ref, row.By, row.At, row.Note = true, ev.Ref, ev.By, ev.At.UTC().Format(time.DateOnly), ev.Note
		}
		if !row.Met {
			r.Missing = append(r.Missing, string(it)+": "+row.Reason)
		}
		r.Rows = append(r.Rows, row)
	}
	if !cfg.Reviewed {
		r.Missing = append(r.Missing, "the thresholds are illustrations you have not reviewed (tier config review)")
	}
	if sessions < r.Floor {
		r.Missing = append(r.Missing, fmt.Sprintf("observation: %d of %d completed sessions (a floor, not a reason to unlock)", sessions, r.Floor))
	}
	r.Ready = len(r.Missing) == 0
	r.Hash = hashOf(struct {
		Target int
		Rows   []Row
		Floor  int
	}{target, r.Rows, r.Floor})
	return r, nil
}

func hashOf(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Decision is the user's recorded acceptance of the risk of a tier, bound to the
// evidence it was made on.
type Decision struct {
	Target       int       `json:"target"`
	EvidenceHash string    `json:"evidence_hash"`
	By           string    `json:"by"`
	At           time.Time `json:"at"`
	RiskText     string    `json:"risk_text"`
	Note         string    `json:"note,omitempty"`
}

// RiskText is what the user accepts, in words. The decision records it as
// shown, so a later change of the text does not rewrite what was accepted.
func RiskText(target int) string {
	p := PermissionsOf(target)
	var parts []string
	if p.ManualReal {
		parts = append(parts, "a real portfolio managed by hand")
	}
	if p.BrokerSubmission {
		parts = append(parts, "orders submitted through a broker after my confirmation")
	}
	if p.EventSleeveReal {
		parts = append(parts, "a small real event sleeve")
	}
	return fmt.Sprintf("I accept tier %d (%s): %s. I understand that I can lose money, that a tier does not make an investment safe, that its example limits are not calibrated, and that nothing here is advice.",
		target, Name(target), strings.Join(parts, ", "))
}

// Allows reports whether a decision still supports activating a policy with the
// target tier now: it exists, rests on the evidence as it is, and has not
// waited longer than the configuration allows.
func (d Decision) Allows(r Readiness, cfg Config, now time.Time) error {
	switch {
	case d.Target != r.Target:
		return fmt.Errorf("tier: the decision is for tier %d, not %d", d.Target, r.Target)
	case !r.Ready:
		return fmt.Errorf("tier: tier %d is not ready: %s", r.Target, strings.Join(r.Missing, "; "))
	case d.EvidenceHash != r.Hash:
		return errors.New("tier: the evidence changed after the decision was made: decide again")
	case now.After(d.At.AddDate(0, 0, cfg.DecisionValidDays)):
		return fmt.Errorf("tier: the decision of %s is older than %d days: decide again", d.At.UTC().Format(time.DateOnly), cfg.DecisionValidDays)
	}
	return nil
}

// Kind of a health signal that suspends new risk.
type Kind string

// The signals of section 5.9.
const (
	Drawdown         Kind = "drawdown"
	StaleInputs      Kind = "stale_execution_inputs"
	ReconcileFailed  Kind = "reconciliation_failed"
	PermissionsMoved Kind = "account_permissions_changed"
	BrokerUnhealthy  Kind = "broker_unhealthy"
	Manual           Kind = "manual"
)

// Kinds lists them.
func Kinds() []Kind {
	return []Kind{Drawdown, StaleInputs, ReconcileFailed, PermissionsMoved, BrokerUnhealthy, Manual}
}

// Valid reports whether k is a known signal.
func (k Kind) Valid() bool {
	for _, x := range Kinds() {
		if x == k {
			return true
		}
	}
	return false
}

// Signal is the newest state of one kind of health problem.
type Signal struct {
	Kind   Kind      `json:"kind"`
	Active bool      `json:"active"`
	Reason string    `json:"reason"`
	By     string    `json:"by"`
	At     time.Time `json:"at"`
}

// Suspended returns the reasons new risk is suspended, sorted; empty when it is not.
func Suspended(signals []Signal) []string {
	var out []string
	for _, s := range signals {
		if s.Active {
			out = append(out, fmt.Sprintf("%s: %s", s.Kind, s.Reason))
		}
	}
	sort.Strings(out)
	return out
}

// MaxDrawdown is the largest fall from a peak to a later low in a series of
// equity values in minor units, as the fall and the peak it fell from, so the
// fraction is never rounded. A series without a positive peak has none.
func MaxDrawdown(values []int64) (fall, peak int64) {
	var top int64
	for _, v := range values {
		if v > top {
			top = v
		}
		if top <= 0 {
			continue
		}
		f := top - v
		// f/top > fall/peak without division
		if peak == 0 || new(big.Int).Mul(big.NewInt(f), big.NewInt(peak)).Cmp(new(big.Int).Mul(big.NewInt(fall), big.NewInt(top))) > 0 {
			fall, peak = f, top
		}
	}
	return fall, peak
}

// Breaches reports whether a fall of fall from peak reaches the threshold
// fraction. An unset (zero) threshold never breaches.
func Breaches(fall, peak int64, threshold money.Decimal) (bool, error) {
	if threshold.Sign() <= 0 || peak <= 0 {
		return false, nil
	}
	limit, err := threshold.Mul(money.Int(peak), uint8(threshold.Scale()), money.Exact)
	if err != nil {
		return false, err
	}
	return money.Int(fall).Cmp(limit) >= 0, nil
}
