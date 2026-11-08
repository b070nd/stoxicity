package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Parse reads a policy file. Unknown keys are an error, so a misspelt limit is
// never silently ignored.
func Parse(r io.Reader) (Policy, error) {
	var p Policy
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		if err == io.EOF {
			return Policy{}, fmt.Errorf("policy: the file is empty")
		}
		return Policy{}, fmt.Errorf("policy: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return Policy{}, fmt.Errorf("policy: the file holds more than one document")
	}
	return p, nil
}

// Marshal writes the policy as YAML.
func Marshal(p Policy) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return b.Bytes(), enc.Close()
}

// Canonical is the policy as JSON in a fixed field order: the bytes that are
// stored and hashed.
func Canonical(p Policy) ([]byte, error) { return json.Marshal(p) }

// Hash identifies the exact content of a policy. A confirmation is made against
// it and a plan records it.
func Hash(p Policy) (string, error) {
	b, err := Canonical(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Change is one field that differs between two policies.
type Change struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// flatten turns a policy into path -> text. An amount stays one leaf.
func flatten(p Policy) (map[string]string, error) {
	b, err := Canonical(p)
	if err != nil {
		return nil, err
	}
	var tree any
	if err := json.Unmarshal(b, &tree); err != nil {
		return nil, err
	}
	out := map[string]string{}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if len(x) == 2 && x["amount"] != nil && x["currency"] != nil {
				out[path] = fmt.Sprint(x["amount"], " ", x["currency"])
				return
			}
			for k, c := range x {
				child := k
				if path != "" {
					child = path + "." + k
				}
				walk(child, c)
			}
		case []any:
			if len(x) == 0 {
				out[path] = "[]"
			}
			for i, c := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), c)
			}
		case nil:
			out[path] = ""
		default:
			out[path] = fmt.Sprint(x)
		}
	}
	walk("", tree)
	return out, nil
}

// Diff lists every field whose content differs, by path.
func Diff(old, next Policy) ([]Change, error) {
	a, err := flatten(old)
	if err != nil {
		return nil, err
	}
	b, err := flatten(next)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for k := range a {
		paths[k] = true
	}
	for k := range b {
		paths[k] = true
	}
	var out []Change
	for k := range paths {
		av, aok := a[k]
		bv, bok := b[k]
		if aok && bok && av == bv {
			continue
		}
		if !aok {
			av = "(absent)"
		}
		if !bok {
			bv = "(absent)"
		}
		out = append(out, Change{Path: k, Old: av, New: bv})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// RiskIncreases names the changes that let the portfolio take more risk or know
// less before it acts. They need the user's explicit acceptance. Lowering a
// limit, raising a floor or narrowing the universe never appears here.
func RiskIncreases(old, next Policy) []string {
	var out []string
	hi := func(name string, o, n money.Decimal) {
		if n.Cmp(o) > 0 {
			out = append(out, fmt.Sprintf("%s raised from %s to %s", name, o, n))
		}
	}
	lo := func(name string, o, n money.Decimal) {
		if n.Cmp(o) < 0 {
			out = append(out, fmt.Sprintf("%s lowered from %s to %s", name, o, n))
		}
	}
	o, n := old.Constraints, next.Constraints
	hi("constraints.max_issuer", o.MaxIssuer, n.MaxIssuer)
	hi("constraints.max_sector", o.MaxSector, n.MaxSector)
	hi("constraints.max_fund", o.MaxFund, n.MaxFund)
	hi("constraints.max_provider", o.MaxProvider, n.MaxProvider)
	hi("constraints.max_foreign_currency", o.MaxCurrency, n.MaxCurrency)
	hi("constraints.max_country", o.MaxCountry, n.MaxCountry)
	hi("constraints.max_event_sleeve", o.MaxEventSleeve, n.MaxEventSleeve)
	hi("constraints.max_event_trade_risk", o.MaxEventTradeRisk, n.MaxEventTradeRisk)
	hi("constraints.drawdown_review", o.DrawdownReview, n.DrawdownReview)
	hi("constraints.max_cost_burden", o.MaxCostBurden, n.MaxCostBurden)
	lo("constraints.min_evidence", o.MinEvidence, n.MinEvidence)
	lo("constraints.min_data_coverage", o.MinCoverage, n.MinCoverage)
	lo("finances.cash_reserve", old.Finances.CashReserve, next.Finances.CashReserve)
	hi("goal.acceptable_loss", old.Goal.AcceptableLoss, next.Goal.AcceptableLoss)
	if o.MinTurnover.Valid() && n.MinTurnover.Valid() && o.MinTurnover.Currency() == n.MinTurnover.Currency() {
		if c, err := n.MinTurnover.Cmp(o.MinTurnover); err == nil && c < 0 {
			out = append(out, fmt.Sprintf("constraints.min_daily_turnover lowered from %s to %s", o.MinTurnover, n.MinTurnover))
		}
	}
	if next.Tier > old.Tier {
		out = append(out, fmt.Sprintf("tier raised from %d to %d", old.Tier, next.Tier))
	}
	if next.Preferences.MaxTradesPerMonth > old.Preferences.MaxTradesPerMonth {
		out = append(out, fmt.Sprintf("preferences.max_trades_per_month raised from %d to %d", old.Preferences.MaxTradesPerMonth, next.Preferences.MaxTradesPerMonth))
	}
	widen := func(name string, o, n []string) {
		for _, v := range n {
			if !slices.Contains(o, v) {
				out = append(out, fmt.Sprintf("%s widened by %s", name, v))
			}
		}
	}
	narrow := func(name string, o, n []string) {
		for _, v := range o {
			if !slices.Contains(n, v) {
				out = append(out, fmt.Sprintf("%s no longer excludes %s", name, v))
			}
		}
	}
	widen("preferences.instruments", old.Preferences.Instruments, next.Preferences.Instruments)
	widen("preferences.markets", old.Preferences.Markets, next.Preferences.Markets)
	var oc, nc []string
	for _, c := range old.Preferences.Currencies {
		oc = append(oc, string(c))
	}
	for _, c := range next.Preferences.Currencies {
		nc = append(nc, string(c))
	}
	widen("preferences.currencies", oc, nc)
	narrow("preferences.excluded_sectors", old.Preferences.ExcludedSectors, next.Preferences.ExcludedSectors)
	narrow("preferences.excluded_issuers", old.Preferences.ExcludedIssuers, next.Preferences.ExcludedIssuers)
	narrow("preferences.excluded_structures", old.Preferences.ExcludedStructures, next.Preferences.ExcludedStructures)
	return out
}

// Effects says what a set of changes invalidates. Anything that changes the
// content of a policy invalidates the confirmations made against the old one,
// because a confirmation is bound to the policy hash; which proposals must be
// rebuilt depends on which part changed.
func Effects(changes []Change) []string {
	if len(changes) == 0 {
		return nil
	}
	groups := map[string]bool{}
	for _, c := range changes {
		root, _, _ := strings.Cut(c.Path, ".")
		root, _, _ = strings.Cut(root, "[")
		groups[root] = true
	}
	delete(groups, "name")
	if len(groups) == 0 {
		return []string{"only the name changed; nothing is invalidated"}
	}
	out := []string{"every unsubmitted order confirmation made under the previous policy becomes unusable"}
	switch {
	case groups["finances"] || groups["goal"] || groups["constraints"] || groups["preferences"] || groups["tier"] || groups["base_currency"]:
		out = append(out, "plan proposals built under the previous policy must be rebuilt before they can be accepted")
	case groups["account"]:
		out = append(out, "plan proposals keep their ranking, but costs, fractional handling and execution checks are recomputed")
	}
	if groups["tier"] {
		out = append(out, "the permitted workflows change with the tier")
	}
	if groups["constraints"] || groups["finances"] {
		out = append(out, "position sizes and allocations are recomputed")
	}
	if groups["preferences"] {
		out = append(out, "ranking, dividend preference and the allowed universe are recomputed")
	}
	return out
}
