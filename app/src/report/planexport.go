package report

import (
	"fmt"
	"strings"

	"github.com/b070nd/stoxicity/app/src/plan"
)

// PlanSchema is the version of the JSON form of an exported plan.
const PlanSchema = "stoxicity.plan-export/1"

// PlanDoc is one plan revision, complete, with its state and its log.
type PlanDoc struct {
	Schema    string        `json:"schema"`
	Program   string        `json:"program"`
	Revision  plan.Revision `json:"revision"`
	State     string        `json:"recorded_state"`
	Effective string        `json:"effective_state"`
	Intact    bool          `json:"intact"`
	Log       []plan.Event  `json:"log"`
	Note      string        `json:"note"`
}

// PlanNote is stated in every exported plan.
const PlanNote = "A plan is a record of a proposal, not an order. It names the evidence it used by identifier and carries no article text, so it can be shared without breaching a source licence."

// JSON is the machine form.
func (d PlanDoc) JSON() ([]byte, error) {
	d.Schema, d.Note = PlanSchema, PlanNote
	return jsonIndent(d)
}

func list(b *strings.Builder, title string, xs []string) {
	if len(xs) == 0 {
		return
	}
	b.WriteString("\n### " + title + "\n\n")
	for _, x := range xs {
		b.WriteString("- " + x + "\n")
	}
}

// Markdown is the form for a person.
func (d PlanDoc) Markdown() string {
	c := d.Revision.Card
	var b strings.Builder
	fmt.Fprintf(&b, "# Plan %s: %s %s\n\n", d.Revision.ID(), c.Action, c.Security)
	if c.Name != "" {
		fmt.Fprintf(&b, "%s\n\n", c.Name)
	}
	fmt.Fprintf(&b, "Schema `%s`. State **%s**", PlanSchema, d.Effective)
	if d.Effective != d.State {
		fmt.Fprintf(&b, " (recorded as %s; past its expiry day)", d.State)
	}
	fmt.Fprintf(&b, ". Book %s, sleeve %s, strategy %s. As of %s, expires %s, generated %s.\n\n", c.Book, c.Sleeve, c.Strategy, c.AsOf, c.Expiry, d.Revision.Generated.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(&b, "Data: %s. Strategy: %s. Execution: %s. Score %d of %d (a policy ordering, not a probability). Coverage %s.\n\n", c.DataState, c.StrategyState, c.ExecutionEligibility, c.Score, c.MaxScore, dash(c.Coverage))
	if !d.Intact {
		b.WriteString("**THE REVISION DOES NOT MATCH ITS MANIFEST HASH. Do not rely on it.**\n\n")
	}
	if s := c.Sizing; s != nil {
		fmt.Fprintf(&b, "Quantity %s at reference %s %s, entry at most %s, cost burden %s, reserve %s.\n", s.Qty, dash(s.Reference), s.PriceCurrency, dash(s.EntryMax), dash(s.Burden), s.Reserve)
		if s.Binding != "" {
			fmt.Fprintf(&b, "Sized by: %s.\n", s.Binding)
		}
	}
	list(&b, "Reasons", c.Reasons)
	list(&b, "Unknowns", c.Unknowns)
	list(&b, "Thesis", c.Thesis)
	list(&b, "Counter-evidence", c.Counter)
	list(&b, "Invalidation", c.Invalidation)
	list(&b, "Exit rules", c.ExitRules)
	list(&b, "Alternatives", c.Alternatives)
	list(&b, "What would change this", c.WouldChange)
	if len(c.Concentration) > 0 {
		var rows [][]string
		for _, x := range c.Concentration {
			rows = append(rows, []string{x.Dimension, x.Name, x.Before, x.After})
		}
		b.WriteString("\n### Concentration\n\n")
		table(&b, []string{"exposure", "name", "before", "after"}, rows)
	}
	if len(d.Log) > 0 {
		b.WriteString("\n### Log\n\n")
		for _, e := range d.Log {
			fmt.Fprintf(&b, "- %s: %s to %s by %s %s\n", e.At.UTC().Format("2006-01-02T15:04:05Z"), e.From, e.To, e.By, e.Note)
		}
	}
	fmt.Fprintf(&b, "\nManifest hash `%s`.\n\n%s\n", d.Revision.ManifestHash, PlanNote)
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
