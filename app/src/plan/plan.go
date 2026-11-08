// Package plan keeps recommendations as immutable, reproducible snapshots. A
// plan is a question about one security in one book under one strategy; each
// time it is generated the answer is a revision that is never edited. Every
// revision names the exact inputs it was computed from (the manifest), carries
// the whole plan card, and has a lifecycle of its own:
//
//	draft -> proposed -> accepted | rejected | expired | superseded
//
// Conventions, stated once:
//
//   - A revision is identified by the plan ID and its number. The semantic hash
//     covers the card and the manifest and nothing else, so two generations from
//     the same frozen inputs have the same hash and the second is not stored.
//     The time of generation is kept beside the revision, outside the hash.
//   - A refresh appends a revision. A draft or proposed predecessor is
//     superseded by it; an accepted one stays in force until the user accepts the
//     new revision, because a refresh must not silently rewrite what was accepted.
//   - A revision is stale when an input it names has changed (policy, strategy
//     configuration, scorecard, assumptions, the book). A stale revision stays
//     readable and is never rewritten, but it cannot be accepted.
//   - A plan is not an authorization. Accepting records the user's decision; an
//     order still needs its own preflight and confirmation.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// State of one revision.
type State string

const (
	Draft      State = "draft"
	Proposed   State = "proposed"
	Accepted   State = "accepted"
	Rejected   State = "rejected"
	Expired    State = "expired"
	Superseded State = "superseded"
)

var moves = map[State][]State{
	Draft:    {Proposed, Rejected, Expired, Superseded},
	Proposed: {Accepted, Rejected, Expired, Superseded},
	Accepted: {Expired, Superseded},
}

// CanMove reports whether the lifecycle allows from -> to.
func CanMove(from, to State) bool {
	for _, s := range moves[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Terminal states take no further move.
func (s State) Terminal() bool { return len(moves[s]) == 0 }

// Valid reports whether s is a lifecycle state.
func (s State) Valid() bool {
	switch s {
	case Draft, Proposed, Accepted, Rejected, Expired, Superseded:
		return true
	}
	return false
}

// Actionable actions can be accepted; the others record a view, not a thing to do.
func Actionable(action string) bool {
	switch action {
	case "BUY", "ADD", "REDUCE", "SELL":
		return true
	}
	return false
}

var knownActions = map[string]bool{"BUY": true, "ADD": true, "HOLD": true, "REDUCE": true, "SELL": true, "WATCH": true, "WAIT": true, "ABSTAIN": true}

// Component is one scored test, kept so a score can be re-read test by test.
type Component struct {
	Group    string `json:"group"`
	ID       string `json:"id"`
	Result   string `json:"result"`
	Required bool   `json:"required,omitempty"`
	Awarded  int    `json:"awarded"`
	Max      int    `json:"max"`
	Detail   string `json:"detail,omitempty"`
}

// Scenarios are assumptions laid out, not targets.
type Scenarios struct {
	Currency    string `json:"currency"`
	Bear        string `json:"bear"`
	Base        string `json:"base"`
	Bull        string `json:"bull"`
	Ceiling     string `json:"entry_ceiling"`
	Entry       string `json:"entry_scenario"`
	Margin      string `json:"margin_of_safety"`
	Assumptions string `json:"assumptions_hash"`
}

// Sizing is the quantity and what it costs, in the book's base currency.
type Sizing struct {
	Qty string `json:"qty"`
	// Reference is the price the quantity was sized at; EntryMax is the most the
	// strategy would pay (its entry ceiling), empty for a fund, which has no price target.
	Reference     string `json:"reference_price,omitempty"`
	EntryMax      string `json:"entry_max,omitempty"`
	PriceCurrency string `json:"price_currency,omitempty"`
	Base          string `json:"base"`
	Notional      int64  `json:"notional"`
	Fee           int64  `json:"fee"`
	Burden        string `json:"cost_burden,omitempty"`
	CashBefore    int64  `json:"cash_before"`
	CashAfter     int64  `json:"cash_after"`
	Reserve       string `json:"cash_reserve"`
	Binding       string `json:"binding,omitempty"`
}

// Concentration is one exposure before and after the plan.
type Concentration struct {
	Dimension string `json:"dimension"`
	Name      string `json:"name"`
	Before    string `json:"before"`
	After     string `json:"after"`
}

// Card is everything a plan states (section 5.6 of the production plan). Every
// field is a stored fact or an explicit "unavailable"; none is a forecast.
type Card struct {
	AsOf                 string            `json:"as_of"`
	Expiry               string            `json:"expiry"`
	Security             string            `json:"security"`
	Name                 string            `json:"name,omitempty"`
	Book                 string            `json:"book"`
	Sleeve               string            `json:"sleeve"`
	Strategy             string            `json:"strategy"`
	Action               string            `json:"action"`
	DataState            string            `json:"data_state"`
	StrategyState        string            `json:"strategy_state"`
	ExecutionEligibility string            `json:"execution_eligibility"`
	Reasons              []string          `json:"reasons"`
	Unknowns             []string          `json:"unknowns"`
	Thesis               []string          `json:"thesis,omitempty"`
	Counter              []string          `json:"counter_evidence,omitempty"`
	Score                int               `json:"score"`
	MaxScore             int               `json:"max_score"`
	Coverage             string            `json:"coverage,omitempty"`
	Components           []Component       `json:"components,omitempty"`
	IncomePreference     bool              `json:"income_preference"`
	NextExDay            string            `json:"next_ex_day,omitempty"`
	Scenarios            *Scenarios        `json:"scenarios,omitempty"`
	Sizing               *Sizing           `json:"sizing,omitempty"`
	Costs                map[string]string `json:"costs,omitempty"`
	Concentration        []Concentration   `json:"concentration,omitempty"`
	Held                 string            `json:"held,omitempty"`
	Free                 string            `json:"free,omitempty"`
	Horizon              string            `json:"horizon,omitempty"`
	ReviewBy             string            `json:"review_by,omitempty"`
	Invalidation         []string          `json:"invalidation,omitempty"`
	ExitRules            []string          `json:"exit_rules,omitempty"`
	Alternatives         []string          `json:"alternatives,omitempty"`
	WouldChange          []string          `json:"would_change,omitempty"`
	// Evidence lists the events the plan rests on, each with the version it was
	// read at. They are context the plan cites, not inputs of its score.
	Evidence []string `json:"evidence,omitempty"`
	// Corrections explains why this revision exists when a change in its
	// supporting evidence, a revised figure or a thesis rule produced it.
	Corrections []string `json:"corrections,omitempty"`
	// Review says the plan could not be reassessed with confidence and what a
	// person must look at; such a plan proposes nothing.
	Review []string `json:"review,omitempty"`
}

// Validate says what makes a card unusable. It is the guard on the rules that
// must hold whatever produced the card.
func (c Card) Validate() error {
	var bad []string
	if c.Security == "" || c.Book == "" || c.Strategy == "" || c.Sleeve == "" {
		bad = append(bad, "security, book, sleeve and strategy are required")
	}
	if !knownActions[c.Action] {
		bad = append(bad, fmt.Sprintf("unknown action %q", c.Action))
	}
	if _, err := time.Parse(time.DateOnly, c.AsOf); err != nil {
		bad = append(bad, "as_of must be YYYY-MM-DD")
	}
	if c.Expiry != "" {
		if e, err := time.Parse(time.DateOnly, c.Expiry); err != nil {
			bad = append(bad, "expiry must be YYYY-MM-DD")
		} else if a, err := time.Parse(time.DateOnly, c.AsOf); err == nil && e.Before(a) {
			bad = append(bad, "the plan expires before its as-of day")
		}
	}
	if c.Expiry == "" {
		bad = append(bad, "a plan needs an expiry: a stored plan is not a standing authorization")
	}
	held := c.Held != "" && c.Held != "0"
	if c.Action == "ADD" && !held {
		bad = append(bad, "an instrument that is not held cannot receive ADD")
	}
	if (c.Action == "REDUCE" || c.Action == "SELL") && c.Sizing != nil && c.Free != "" && cmpQty(c.Sizing.Qty, c.Free) > 0 {
		bad = append(bad, fmt.Sprintf("a sale of %s exceeds the %s free to sell", c.Sizing.Qty, c.Free))
	}
	if Actionable(c.Action) && c.Action != "SELL" && c.Action != "REDUCE" && c.Sizing == nil {
		bad = append(bad, c.Action+" needs a quantity and its costs")
	}
	if len(c.Review) > 0 && (Actionable(c.Action) || c.Action == "HOLD") {
		bad = append(bad, "a plan that needs review proposes nothing and is not a fresh "+c.Action)
	}
	if len(bad) > 0 {
		return errors.New("plan: " + strings.Join(bad, "; "))
	}
	return nil
}

// cmpQty compares two decimal quantity strings; unparsable values compare equal.
func cmpQty(a, b string) int {
	x, ok1 := ratOf(a)
	y, ok2 := ratOf(b)
	if !ok1 || !ok2 {
		return 0
	}
	return x.Cmp(y)
}

// Manifest names the inputs a revision was computed from.
type Manifest struct {
	AsOf        string `json:"as_of"`
	PolicyEpoch int64  `json:"policy_epoch"`
	Policy      string `json:"policy"`
	Strategy    string `json:"strategy"`
	Config      string `json:"config"`
	Scorecard   string `json:"scorecard,omitempty"`
	Assumptions string `json:"assumptions,omitempty"`
	Price       string `json:"price,omitempty"`
	Payouts     int    `json:"payouts"`
	Fees        string `json:"fees,omitempty"`
	Rates       string `json:"rates,omitempty"`
	// Decisions hashes the recorded thesis, instruction and review log a review read.
	Decisions string `json:"decisions,omitempty"`
	// Evidence is the hash of the versions of the events the plan cites.
	Evidence string `json:"evidence,omitempty"`
	Ledger   string `json:"ledger"`
}

// Hash of the manifest.
func (m Manifest) Hash() string { return hashJSON(m) }

// Changed lists the inputs that differ between a revision's manifest and the
// current one, in a fixed order. The as-of day is not an input.
func (m Manifest) Changed(cur Manifest) []string {
	var out []string
	add := func(name string, differs bool) {
		if differs {
			out = append(out, name)
		}
	}
	add("policy", m.Policy != cur.Policy || m.PolicyEpoch != cur.PolicyEpoch)
	add("strategy configuration", m.Strategy != cur.Strategy || m.Config != cur.Config)
	add("scorecard", m.Scorecard != cur.Scorecard)
	add("valuation assumptions", m.Assumptions != cur.Assumptions)
	add("price", m.Price != cur.Price)
	add("dividend observations", m.Payouts != cur.Payouts)
	add("fee schedule", m.Fees != cur.Fees)
	add("exchange rates", m.Rates != cur.Rates)
	add("recorded decisions", m.Decisions != cur.Decisions)
	add("supporting evidence", m.Evidence != cur.Evidence)
	add("book", m.Ledger != cur.Ledger)
	return out
}

// Change is one difference between two cards.
type Change struct {
	Field    string `json:"field"`
	From     string `json:"from"`
	To       string `json:"to"`
	Material bool   `json:"material"`
}

// Revision is one immutable snapshot.
type Revision struct {
	PlanID       string    `json:"plan_id"`
	Rev          int       `json:"rev"`
	Generated    time.Time `json:"generated"`
	Card         Card      `json:"card"`
	Manifest     Manifest  `json:"manifest"`
	ManifestHash string    `json:"manifest_hash"`
	Semantic     string    `json:"semantic"`
	Previous     string    `json:"previous,omitempty"`
	Changes      []Change  `json:"changes,omitempty"`
}

// ID is the reference to one revision.
func (r Revision) ID() string { return fmt.Sprintf("%s.r%d", r.PlanID, r.Rev) }

// Material reports whether the revision differs materially from its predecessor.
// The first revision is material by definition.
func (r Revision) Material() bool {
	if r.Rev <= 1 {
		return true
	}
	for _, c := range r.Changes {
		if c.Material {
			return true
		}
	}
	return false
}

// Verify recomputes the hashes. A stored revision that fails was changed after it was written.
func (r Revision) Verify() error {
	if r.Manifest.Hash() != r.ManifestHash {
		return fmt.Errorf("plan: revision %s: the manifest no longer matches its hash", r.ID())
	}
	if semantic(r.Card, r.Manifest) != r.Semantic {
		return fmt.Errorf("plan: revision %s: the card no longer matches its hash", r.ID())
	}
	return r.Card.Validate()
}

func semantic(c Card, m Manifest) string {
	return hashJSON(struct {
		C Card     `json:"card"`
		M Manifest `json:"manifest"`
	}{c, m})
}

// PlanID is the identity of the question: the same book, security, sleeve and
// strategy always give the same plan.
func PlanID(book, security, sleeve, strategy string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{book, security, sleeve, strategy}, "\x1f")))
	return "pl_" + hex.EncodeToString(sum[:])[:12]
}

// New builds revision 1 of a card; Save assigns the number once the history is known.
func New(card Card, m Manifest, generated time.Time) (Revision, error) {
	if err := card.Validate(); err != nil {
		return Revision{}, err
	}
	return Revision{PlanID: PlanID(card.Book, card.Security, card.Sleeve, card.Strategy), Rev: 1, Generated: generated.UTC(), Card: card, Manifest: m, ManifestHash: m.Hash(), Semantic: semantic(card, m)}, nil
}

func hashJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // the types here always marshal
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Diff lists what changed between two cards and which changes matter. Material
// changes are those that alter what the user would do or why: the action, the
// eligibility, the data and strategy state, the quantity and price bounds, the
// ceiling, any test result, and the unknowns, reasons and invalidation.
func Diff(a, b Card) []Change {
	var out []Change
	add := func(field, from, to string, material bool) {
		if from != to {
			out = append(out, Change{Field: field, From: orNone(from), To: orNone(to), Material: material})
		}
	}
	set := func(field string, from, to []string, material bool) {
		x, y := sorted(from), sorted(to)
		add(field, strings.Join(x, "; "), strings.Join(y, "; "), material)
	}
	add("action", a.Action, b.Action, true)
	add("execution eligibility", a.ExecutionEligibility, b.ExecutionEligibility, true)
	add("data state", a.DataState, b.DataState, true)
	add("strategy state", a.StrategyState, b.StrategyState, true)
	add("score", fmt.Sprint(a.Score), fmt.Sprint(b.Score), false)
	ra, rb := results(a), results(b)
	ids := map[string]bool{}
	for k := range ra {
		ids[k] = true
	}
	for k := range rb {
		ids[k] = true
	}
	keys := make([]string, 0, len(ids))
	for k := range ids {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		add("test "+k, ra[k], rb[k], true)
	}
	qa, ea, na := sizing(a)
	qb, eb, nb := sizing(b)
	add("quantity", qa, qb, true)
	add("entry maximum", ea, eb, true)
	add("notional", na, nb, true)
	ca, cb := ceiling(a), ceiling(b)
	add("entry ceiling", ca, cb, true)
	set("unknowns", a.Unknowns, b.Unknowns, true)
	set("reasons", a.Reasons, b.Reasons, true)
	set("invalidation", a.Invalidation, b.Invalidation, true)
	set("exit rules", a.ExitRules, b.ExitRules, true)
	add("income preference", fmt.Sprint(a.IncomePreference), fmt.Sprint(b.IncomePreference), true)
	add("next ex-date", a.NextExDay, b.NextExDay, false)
	add("review by", a.ReviewBy, b.ReviewBy, false)
	add("expiry", a.Expiry, b.Expiry, false)
	set("evidence", a.Evidence, b.Evidence, true)
	set("corrections", a.Corrections, b.Corrections, true)
	set("review required", a.Review, b.Review, true)
	set("thesis", a.Thesis, b.Thesis, false)
	set("counter-evidence", a.Counter, b.Counter, false)
	set("alternatives", a.Alternatives, b.Alternatives, false)
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func results(c Card) map[string]string {
	m := map[string]string{}
	for _, t := range c.Components {
		m[t.ID] = fmt.Sprintf("%s %d/%d", t.Result, t.Awarded, t.Max)
	}
	return m
}

func sizing(c Card) (qty, entry, notional string) {
	if c.Sizing == nil {
		return "", "", ""
	}
	return c.Sizing.Qty, c.Sizing.EntryMax + "|" + c.Sizing.Reference, fmt.Sprint(c.Sizing.Notional)
}

func ceiling(c Card) string {
	if c.Scenarios == nil {
		return ""
	}
	return c.Scenarios.Ceiling
}

// Event is one move of a revision through its lifecycle.
type Event struct {
	PlanID string    `json:"plan_id"`
	Rev    int       `json:"rev"`
	Seq    int       `json:"seq"`
	From   State     `json:"from"`
	To     State     `json:"to"`
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Note   string    `json:"note,omitempty"`
}

// StateOf is the state a log of events leaves a revision in.
func StateOf(log []Event) State {
	if len(log) == 0 {
		return Draft
	}
	return log[len(log)-1].To
}

// Effective is the state at a moment: a plan past its expiry day that was not
// settled is expired even if no one has recorded it yet.
func Effective(s State, expiry string, now time.Time) State {
	if s.Terminal() || expiry == "" {
		return s
	}
	e, err := time.Parse(time.DateOnly, expiry)
	if err != nil {
		return s
	}
	if !now.UTC().Before(e.Add(24 * time.Hour)) {
		return Expired
	}
	return s
}

// CheckMove says why a revision cannot move to a state now. current is the
// manifest recomputed from today's stored inputs.
func CheckMove(r Revision, log []Event, to State, by, note string, current Manifest, now time.Time) error {
	from := StateOf(log)
	if !to.Valid() {
		return fmt.Errorf("plan: %q is not a state", to)
	}
	if Effective(from, r.Card.Expiry, now) == Expired && from != Expired && to != Expired {
		return fmt.Errorf("plan: revision %s expired on %s", r.ID(), r.Card.Expiry)
	}
	if !CanMove(from, to) {
		return fmt.Errorf("plan: revision %s is %s and cannot become %s", r.ID(), from, to)
	}
	if strings.TrimSpace(by) == "" {
		return errors.New("plan: say who is deciding (-by)")
	}
	switch to {
	case Rejected, Superseded, Expired:
		if strings.TrimSpace(note) == "" && to == Rejected {
			return errors.New("plan: a rejection needs a reason (-note)")
		}
	case Accepted:
		if !Actionable(r.Card.Action) {
			return fmt.Errorf("plan: a %s plan records a view and has nothing to accept", r.Card.Action)
		}
		if err := r.Verify(); err != nil {
			return err
		}
		if ch := r.Manifest.Changed(current); len(ch) > 0 {
			return fmt.Errorf("plan: revision %s is stale: %s changed since it was generated; regenerate it", r.ID(), strings.Join(ch, ", "))
		}
	}
	return nil
}
