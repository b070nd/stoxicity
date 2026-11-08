package report

import (
	"encoding/json"
	"strings"
	"testing"
)

func sample() Report {
	return Report{Program: "stoxicity", AsOf: "2026-11-03", Book: "main/core", Account: "main", Env: "paper", Base: "EUR", Ledger: "abc", Tier: 1, Policy: "epoch 1",
		Cash:     []Cash{{"EUR", "1000.00 EUR", "0.00 EUR", "0.00 EUR", "1000.00 EUR"}},
		Plans:    []Plan{{Revision: "pl_1.r1", Security: "sec_a", Action: "BUY", State: "draft", Reasons: []string{"a | b"}, Unknowns: []string{"spread\nunknown"}}},
		Omitted:  []string{"nothing secret"},
		Holdings: []Holding{}, Reservations: []Reservation{}, Orders: []Order{}}
}

func TestJSONNamesItsSchemaAndKeepsEveryKey(t *testing.T) {
	raw, err := sample().JSON()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["schema"] != Schema {
		t.Fatalf("schema %v", m["schema"])
	}
	for _, k := range []string{"as_of", "book", "environment", "base_currency", "ledger_fingerprint", "tier", "policy", "broker", "cash", "holdings", "reservations", "orders", "plans", "income", "capabilities", "omitted"} {
		if _, ok := m[k]; !ok {
			t.Errorf("key %s missing: a removed key needs a new schema version", k)
		}
	}
}

func TestMarkdownSaysWhenNotRealAndKeepsTablesIntact(t *testing.T) {
	md := sample().Markdown()
	for _, want := range []string{"PAPER (not real money)", "## Cash", "| EUR | 1000.00 EUR |", "a \\| b", "spread unknown", "_none_", "nothing secret", "nothing was fetched or sent"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown lacks %q:\n%s", want, md)
		}
	}
	r := sample()
	r.Env = "real"
	if strings.Contains(r.Markdown(), "not real money") {
		t.Error("a real book is not marked as unreal")
	}
}
