package corrections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/store"
)

// Record kinds, in the tenant's record store. Every kind is append-only.
const (
	KindDeps       = "plan.deps"
	KindRecipe     = "plan.recipe"
	KindRule       = "thesis.rule"
	KindCorrection = "plan.correction"
)

// Repo keeps dependencies, recipes, thesis rules and correction records.
type Repo struct{ RS store.Records }

func depsKey(plan string, rev int) string { return fmt.Sprintf("%s|%04d", plan, rev) }

// PutDeps records what a revision depends on. Writing the same dependencies
// again adds nothing.
func (r Repo) PutDeps(ctx context.Context, d Deps) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, _, err = r.RS.Append(ctx, store.RecordInput{Kind: KindDeps, Key: depsKey(d.Plan, d.Rev), Body: b})
	return err
}

// Deps returns the dependencies of a revision, or false when none were recorded
// (the revision was made before dependencies were kept).
func (r Repo) Deps(ctx context.Context, plan string, rev int) (Deps, bool, error) {
	rec, err := r.RS.Latest(ctx, KindDeps, depsKey(plan, rev))
	if errors.Is(err, store.ErrNoRecord) {
		return Deps{}, false, nil
	}
	if err != nil {
		return Deps{}, false, err
	}
	var d Deps
	if err := json.Unmarshal(rec.Body, &d); err != nil {
		return Deps{}, false, fmt.Errorf("corrections: dependencies %s are unreadable: %w", rec.Key, err)
	}
	return d, true, nil
}

// Recipe is how a plan was generated, so it can be generated again from the
// same inputs without anyone typing the command: the files it read (with their
// hashes at that time) and the options it was given.
type Recipe struct {
	Plan         string `json:"plan"`
	Rev          int    `json:"rev"`
	Book         string `json:"book"`
	CandFile     string `json:"candidates"`
	CandHash     string `json:"candidates_hash"`
	FeeFile      string `json:"fees,omitempty"`
	FeeHash      string `json:"fees_hash,omitempty"`
	Rates        string `json:"rates,omitempty"`
	Contribution string `json:"contribution,omitempty"`
	ValidDays    int    `json:"valid_days"`
}

// PutRecipe records the recipe of a plan; the newest one counts.
func (r Repo) PutRecipe(ctx context.Context, rc Recipe) error {
	b, err := json.Marshal(rc)
	if err != nil {
		return err
	}
	_, _, err = r.RS.Append(ctx, store.RecordInput{Kind: KindRecipe, Key: rc.Plan, Body: b})
	return err
}

// Recipe returns the newest recipe of a plan, or false when there is none.
func (r Repo) Recipe(ctx context.Context, plan string) (Recipe, bool, error) {
	rec, err := r.RS.Latest(ctx, KindRecipe, plan)
	if errors.Is(err, store.ErrNoRecord) {
		return Recipe{}, false, nil
	}
	if err != nil {
		return Recipe{}, false, err
	}
	var rc Recipe
	if err := json.Unmarshal(rec.Body, &rc); err != nil {
		return Recipe{}, false, fmt.Errorf("corrections: recipe %s is unreadable: %w", rec.Key, err)
	}
	return rc, true, nil
}

func ruleKey(security, id string) string { return security + "|" + id }

// PutRule records a thesis rule as the next version of its ID.
func (r Repo) PutRule(ctx context.Context, rule Rule, now time.Time) (Rule, error) {
	rule.ID = strings.TrimSpace(rule.ID)
	if err := rule.Validate(); err != nil {
		return Rule{}, err
	}
	rule.Version, rule.At = 1, now.UTC()
	if rec, err := r.RS.Latest(ctx, KindRule, ruleKey(rule.Security, rule.ID)); err == nil {
		var prev Rule
		if err := json.Unmarshal(rec.Body, &prev); err != nil {
			return Rule{}, fmt.Errorf("corrections: rule %s is unreadable: %w", rec.Key, err)
		}
		rule.Version = prev.Version + 1
	} else if !errors.Is(err, store.ErrNoRecord) {
		return Rule{}, err
	}
	sort.Strings(rule.Kinds)
	b, err := json.Marshal(rule)
	if err != nil {
		return Rule{}, err
	}
	if _, _, err := r.RS.Append(ctx, store.RecordInput{Kind: KindRule, Key: ruleKey(rule.Security, rule.ID), Body: b}); err != nil {
		return Rule{}, err
	}
	return rule, nil
}

// Retire ends a rule; the history stays.
func (r Repo) Retire(ctx context.Context, security, id, by string, now time.Time) (Rule, error) {
	rec, err := r.RS.Latest(ctx, KindRule, ruleKey(security, id))
	if errors.Is(err, store.ErrNoRecord) {
		return Rule{}, fmt.Errorf("corrections: no rule %s for %s", id, security)
	}
	if err != nil {
		return Rule{}, err
	}
	var rule Rule
	if err := json.Unmarshal(rec.Body, &rule); err != nil {
		return Rule{}, err
	}
	if rule.Retired {
		return rule, nil
	}
	rule.Retired, rule.Version, rule.By, rule.At = true, rule.Version+1, by, now.UTC()
	b, _ := json.Marshal(rule)
	if _, _, err := r.RS.Append(ctx, store.RecordInput{Kind: KindRule, Key: ruleKey(security, id), Body: b}); err != nil {
		return Rule{}, err
	}
	return rule, nil
}

// Rules lists the newest version of every rule; of a security when it is not empty.
func (r Repo) Rules(ctx context.Context, security string) ([]Rule, error) {
	prefix := ""
	if security != "" {
		prefix = security + "|"
	}
	recs, err := r.RS.Current(ctx, KindRule, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(recs))
	for _, rec := range recs {
		var rule Rule
		if err := json.Unmarshal(rec.Body, &rule); err != nil {
			return nil, fmt.Errorf("corrections: rule %s is unreadable: %w", rec.Key, err)
		}
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Security != out[j].Security {
			return out[i].Security < out[j].Security
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Change states.
const (
	Detected = "detected" // found, and the work below is not known to be finished
	Done     = "done"
)

// Change is one material change of one plan, with what was done about it.
type Change struct {
	Plan    string `json:"plan"`
	ID      string `json:"id"`
	State   string `json:"state"`
	FromRev int    `json:"from_rev"`
	ToRev   int    `json:"to_rev,omitempty"`
	Book    string `json:"book"`
	// Day is the day the new revision is made for, fixed when the change is
	// found so that finishing it after a restart makes the same revision.
	Day      string    `json:"day"`
	Strength Effect    `json:"strength"`
	Findings []Finding `json:"findings"`
	// Revoked are the orders whose confirmation was withdrawn. Submitted are
	// the orders already sent, left exactly as they are.
	Revoked   []string  `json:"revoked,omitempty"`
	Submitted []string  `json:"submitted,omitempty"`
	At        time.Time `json:"at"`
}

func changeKey(plan, id string) string { return plan + "|" + id }

// PutChange records a step of a change. A state already written adds nothing.
func (r Repo) PutChange(ctx context.Context, c Change) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, _, err = r.RS.Append(ctx, store.RecordInput{Kind: KindCorrection, Key: changeKey(c.Plan, c.ID), Body: b})
	return err
}

// Change returns the newest state of a change, or false when it was never found.
func (r Repo) Change(ctx context.Context, plan, id string) (Change, bool, error) {
	rec, err := r.RS.Latest(ctx, KindCorrection, changeKey(plan, id))
	if errors.Is(err, store.ErrNoRecord) {
		return Change{}, false, nil
	}
	if err != nil {
		return Change{}, false, err
	}
	var c Change
	if err := json.Unmarshal(rec.Body, &c); err != nil {
		return Change{}, false, fmt.Errorf("corrections: change %s is unreadable: %w", rec.Key, err)
	}
	return c, true, nil
}

// Changes lists the newest state of every change, oldest first.
func (r Repo) Changes(ctx context.Context) ([]Change, error) {
	recs, err := r.RS.Current(ctx, KindCorrection, "")
	if err != nil {
		return nil, err
	}
	out := make([]Change, 0, len(recs))
	for _, rec := range recs {
		var c Change
		if err := json.Unmarshal(rec.Body, &c); err != nil {
			return nil, fmt.Errorf("corrections: change %s is unreadable: %w", rec.Key, err)
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
