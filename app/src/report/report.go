// Package report is the exportable account of a book: its cash, holdings,
// reservations, orders, plans, income and the state of the product's own
// capabilities, as Markdown for a person and as versioned JSON for a program.
//
// A report is derived from stored records only. Building one fetches nothing,
// submits nothing and opens no book other than the one asked for. It carries
// no secrets (a broker is described by its transport and mode, never by its
// address, certificate or account number) and no text of any news source: a plan
// refers to evidence by identifier, so a licence that forbids redistribution is
// not breached by sharing a report. The report says so itself.
package report

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Schema is the version of the JSON form. A change that removes or renames a
// field is a new schema string.
const Schema = "stoxicity.report/1"

// Cash is one currency of a book.
type Cash struct {
	Currency  string `json:"currency"`
	Settled   string `json:"settled"`
	Unsettled string `json:"unsettled"`
	Reserved  string `json:"reserved"`
	Available string `json:"available"`
}

// Holding is one security held.
type Holding struct {
	Security string `json:"security"`
	Qty      string `json:"qty"`
	Cost     string `json:"cost"`
	Lots     int    `json:"lots"`
}

// Reservation is cash or shares held for an open order.
type Reservation struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	What   string `json:"what"`
	Intent string `json:"intent,omitempty"`
}

// Order is an order of the book.
type Order struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Env      string `json:"environment"`
	Side     string `json:"side"`
	Security string `json:"security"`
	Qty      string `json:"qty"`
	Filled   string `json:"filled"`
	Limit    string `json:"limit"`
	Currency string `json:"currency"`
	Unknown  bool   `json:"outcome_unknown,omitempty"`
}

// Plan is the latest revision of a plan of the book.
type Plan struct {
	Revision  string   `json:"revision"`
	Security  string   `json:"security"`
	Action    string   `json:"action"`
	State     string   `json:"state"`
	Expiry    string   `json:"expiry"`
	Data      string   `json:"data_state"`
	Strategy  string   `json:"strategy_state"`
	Execution string   `json:"execution_eligibility"`
	Reasons   []string `json:"reasons"`
	Unknowns  []string `json:"unknowns"`
	Manifest  string   `json:"manifest_hash"`
}

// Payout is a payout the ledger holds.
type Payout struct {
	Key      string `json:"key"`
	Currency string `json:"currency"`
	Gross    string `json:"gross"`
	State    string `json:"state"`
}

// Income is the income section.
type Income struct {
	Payouts  []Payout `json:"payouts"`
	Findings []string `json:"findings"`
	Note     string   `json:"note,omitempty"`
}

// Capability is a line of the product's readiness inventory.
type Capability struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Implemented string `json:"implemented"`
	Fixture     string `json:"fixture_verified"`
	Source      string `json:"source_verified"`
	Account     string `json:"account_verified"`
	Investment  string `json:"investment_evaluated"`
}

// Broker describes the broker without any address or identifier.
type Broker struct {
	Configured bool   `json:"configured"`
	Transport  string `json:"transport,omitempty"`
	Mode       string `json:"mode,omitempty"`
}

// Report is the whole export.
type Report struct {
	Schema       string        `json:"schema"`
	Program      string        `json:"program"`
	AsOf         string        `json:"as_of"`
	Book         string        `json:"book"`
	Account      string        `json:"account"`
	Env          string        `json:"environment"`
	Base         string        `json:"base_currency"`
	Ledger       string        `json:"ledger_fingerprint"`
	Tier         int           `json:"tier"`
	Policy       string        `json:"policy"`
	Broker       Broker        `json:"broker"`
	Cash         []Cash        `json:"cash"`
	Holdings     []Holding     `json:"holdings"`
	Reservations []Reservation `json:"reservations"`
	Orders       []Order       `json:"orders"`
	Plans        []Plan        `json:"plans"`
	Income       Income        `json:"income"`
	Capabilities []Capability  `json:"capabilities"`
	// Omitted lists, in words, what the report leaves out and why.
	Omitted []string `json:"omitted"`
}

// JSON is the machine form.
func (r Report) JSON() ([]byte, error) {
	r.Schema = Schema
	return json.MarshalIndent(r, "", "  ")
}

func esc(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
}

func table(b *strings.Builder, header []string, rows [][]string) {
	if len(rows) == 0 {
		b.WriteString("_none_\n\n")
		return
	}
	b.WriteString("| " + strings.Join(header, " | ") + " |\n|" + strings.Repeat(" --- |", len(header)) + "\n")
	for _, r := range rows {
		for i := range r {
			r[i] = esc(r[i])
		}
		b.WriteString("| " + strings.Join(r, " | ") + " |\n")
	}
	b.WriteString("\n")
}

// Markdown is the form for a person. Nothing in a table is shortened.
func (r Report) Markdown() string {
	var b strings.Builder
	env := r.Env
	if env != "real" {
		env = strings.ToUpper(env) + " (not real money)"
	}
	fmt.Fprintf(&b, "# Report for %s, as of %s\n\n", r.Book, r.AsOf)
	fmt.Fprintf(&b, "Schema `%s`. Account %s, environment **%s**, base currency %s, tier %d, policy %s. Ledger fingerprint `%s`.\n\n", Schema, r.Account, env, r.Base, r.Tier, r.Policy, r.Ledger)
	br := "no broker configured"
	if r.Broker.Configured {
		br = fmt.Sprintf("broker transport %s, mode %s", r.Broker.Transport, r.Broker.Mode)
	}
	fmt.Fprintf(&b, "%s. This report was built from stored records only: nothing was fetched or sent.\n\n", br)

	b.WriteString("## Cash\n\n")
	var rows [][]string
	for _, c := range r.Cash {
		rows = append(rows, []string{c.Currency, c.Settled, c.Unsettled, c.Reserved, c.Available})
	}
	table(&b, []string{"currency", "settled", "unsettled", "reserved", "available"}, rows)

	b.WriteString("## Holdings\n\n")
	rows = nil
	for _, h := range r.Holdings {
		rows = append(rows, []string{h.Security, h.Qty, h.Cost, fmt.Sprint(h.Lots)})
	}
	table(&b, []string{"security", "quantity", "cost", "lots"}, rows)

	b.WriteString("## Reservations\n\n")
	rows = nil
	for _, x := range r.Reservations {
		rows = append(rows, []string{x.ID, x.Kind, x.What, x.Intent})
	}
	table(&b, []string{"id", "kind", "held", "order"}, rows)

	b.WriteString("## Orders\n\n")
	rows = nil
	for _, o := range r.Orders {
		st := o.State
		if o.Unknown {
			st += " (OUTCOME UNKNOWN)"
		}
		rows = append(rows, []string{o.ID, st, o.Env, o.Side, o.Security, o.Qty, o.Filled, o.Limit + " " + o.Currency})
	}
	table(&b, []string{"id", "state", "environment", "side", "security", "quantity", "filled", "limit"}, rows)

	b.WriteString("## Plans\n\nThe latest revision of each plan of this book. A plan is a record, not an order.\n\n")
	rows = nil
	for _, p := range r.Plans {
		rows = append(rows, []string{p.Revision, p.Security, p.Action, p.State, p.Expiry, p.Data + " / " + p.Strategy + " / " + p.Execution, strings.Join(p.Reasons, "; "), strings.Join(p.Unknowns, "; ")})
	}
	table(&b, []string{"revision", "security", "action", "state", "expires", "data / strategy / execution", "reasons", "unknowns"}, rows)

	b.WriteString("## Income\n\n")
	rows = nil
	for _, p := range r.Income.Payouts {
		rows = append(rows, []string{p.Key, p.Gross + " " + p.Currency, p.State})
	}
	table(&b, []string{"payout", "gross", "state"}, rows)
	if len(r.Income.Findings) == 0 {
		b.WriteString("No finding: the income booked agrees with the declared payouts the ledger was entitled to.\n\n")
	}
	for _, f := range r.Income.Findings {
		b.WriteString("- " + f + "\n")
	}
	if r.Income.Note != "" {
		b.WriteString("\n" + r.Income.Note + "\n")
	}

	b.WriteString("\n## Capabilities\n\nWhat the product itself claims, level by level. A level is never summed into a score.\n\n")
	rows = nil
	for _, c := range r.Capabilities {
		rows = append(rows, []string{c.ID, c.Name, c.Implemented, c.Fixture, c.Source, c.Account, c.Investment})
	}
	table(&b, []string{"id", "capability", "implemented", "fixture verified", "source verified", "account verified", "investment evaluated"}, rows)

	b.WriteString("## Left out\n\n")
	for _, o := range r.Omitted {
		b.WriteString("- " + o + "\n")
	}
	return b.String()
}

func jsonIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
