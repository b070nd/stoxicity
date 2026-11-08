package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/alerts"
	"github.com/b070nd/stoxicity/app/src/journal"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/tier"
)

// Section is one part of the daily review: what is there, or why nothing is.
type Section struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
	Empty string   `json:"empty"` // said when there are no lines
}

// Daily is the daily review: everything that asks for the user's attention
// today, read from the stores. It ends with the journal, because a day's
// decisions are not finished until the reasoning and the outcomes of earlier
// ones are written down.
type Daily struct {
	Day      string    `json:"day"`
	Sections []Section `json:"sections"`
	Closing  string    `json:"closing"`
}

// Daily reads the review of the day. It fetches nothing and writes nothing.
func (s Source) Daily(ctx context.Context, cfg alerts.Config) (Daily, error) {
	d := Daily{Day: s.Day()}
	add := func(title, empty string, lines ...string) {
		if lines == nil {
			lines = []string{}
		}
		d.Sections = append(d.Sections, Section{Title: title, Lines: lines, Empty: empty})
	}

	// health
	sig, err := s.Signals(ctx)
	if err != nil {
		return d, err
	}
	t, active, err := s.Tier(ctx)
	if err != nil {
		return d, err
	}
	var health []string
	if active {
		health = append(health, fmt.Sprintf("Policy in force, tier %d.", t))
	} else {
		health = append(health, "No policy is in force: new orders are refused until one is activated.")
	}
	for _, x := range sig {
		if x.Active {
			health = append(health, fmt.Sprintf("Health signal %s is active: %s (by %s, %s). New risk is suspended.", x.Kind, x.Reason, x.By, x.At.UTC().Format("2006-01-02")))
		}
	}
	if len(tier.Suspended(sig)) == 0 {
		health = append(health, "No health signal is active.")
	}
	add("Health", "", health...)

	// alerts
	ob := alerts.Outbox{RS: s.records(), Tenant: s.Tenant, Now: s.now}
	rows, err := ob.Status(ctx, false)
	if err != nil {
		return d, err
	}
	var al []string
	for _, r := range rows {
		waiting := 0
		for _, x := range r.Deliveries {
			if x.State.Waiting() {
				waiting++
			}
		}
		line := fmt.Sprintf("%s  %s", r.Alert.ID(), r.Alert.Title)
		if waiting > 0 {
			line += fmt.Sprintf(" (%d not delivered yet)", waiting)
		}
		al = append(al, line)
	}
	add("Alerts waiting for you", "None. `alerts scan` looks for new ones.", al...)

	// plans waiting for a decision
	prow, err := s.PlanRows(ctx)
	if err != nil {
		return d, err
	}
	var pl []string
	for _, r := range prow {
		if r.State == string(plan.Draft) || r.State == string(plan.Proposed) {
			pl = append(pl, fmt.Sprintf("%s  %s %s, %s, expires %s, %d open question(s)", r.Revision, r.Action, r.Security, r.State, r.Expiry, r.Unknowns))
		}
	}
	add("Plans waiting for a decision", "None.", pl...)

	// reviews due
	rv, err := s.reviewAlerts(ctx, cfg)
	if err != nil {
		return d, err
	}
	var rl []string
	for _, a := range rv {
		rl = append(rl, a.Title)
	}
	add("Reviews due", "None.", rl...)

	// income in the next two weeks
	inc, err := s.Income(ctx, d.Day)
	if err != nil {
		return d, err
	}
	var il []string
	for _, r := range inc.Rows {
		if r.Status != "entitled" && r.Status != "pending" {
			continue
		}
		if r.ExDay >= d.Day && r.ExDay <= addDays(d.Day, 14) {
			il = append(il, fmt.Sprintf("%s goes ex on %s (%s), %s, gross %s", r.Security, r.ExDay, r.Book, r.PerShare, r.Gross))
		}
		if r.PayDay != "" && r.PayDay >= d.Day && r.PayDay <= addDays(d.Day, 14) {
			il = append(il, fmt.Sprintf("%s pays on %s (%s), gross %s, booked %s", r.Security, r.PayDay, r.Book, r.Gross, r.Booked))
		}
	}
	for _, f := range inc.Findings {
		il = append(il, "Difference: "+f)
	}
	add("Income, next 14 days", "Nothing expected, and no difference found.", il...)

	// orders
	orows, err := s.OrderRows(ctx)
	if err != nil {
		return d, err
	}
	var ol []string
	for _, r := range orows {
		switch order.State(r.State) {
		case order.Unknown:
			ol = append(ol, fmt.Sprintf("%s is UNKNOWN: %s %s %s. Settle it before anything else.", r.ID, r.Side, r.Qty, r.Security))
		case order.Draft, order.Confirmed:
			ol = append(ol, fmt.Sprintf("%s is %s: %s %s %s (%s)", r.ID, r.State, r.Side, r.Qty, r.Security, r.Env))
		}
	}
	add("Orders needing attention", "None.", ol...)

	// the journal
	jr := journal.Repo{RS: s.records(), Now: s.now}
	due, err := jr.Due(ctx, d.Day)
	if err != nil {
		return d, err
	}
	var jl []string
	for _, e := range due {
		jl = append(jl, fmt.Sprintf("%s  %s %s decided %s on %s, horizon %s: has the thesis held?", e.ID, e.Decision, e.Security, e.Plan, e.Day, e.Horizon))
	}
	add("Journal: outcomes to write", "No decision has reached its horizon.", jl...)

	d.Closing = "Before you close: write the reasoning for each decision you made today (`journal add`) and the outcome of each entry listed above (`journal outcome`). A decision without its reasoning cannot be judged later."
	return d, nil
}

func addDays(day string, n int) string {
	t, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// Text renders the review as plain text.
func (d Daily) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Daily review for %s\n", d.Day)
	for _, s := range d.Sections {
		fmt.Fprintf(&b, "\n%s\n", s.Title)
		if len(s.Lines) == 0 {
			fmt.Fprintf(&b, "  %s\n", s.Empty)
			continue
		}
		for _, l := range s.Lines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}
	fmt.Fprintf(&b, "\n%s\n", d.Closing)
	return b.String()
}
