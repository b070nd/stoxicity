package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/alerts"
	"github.com/b070nd/stoxicity/app/src/income"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

// The record kinds the producers read, written by the commands of the same
// names (review, broker).
const (
	KindReviewLog  = "review.log"
	KindBrokerDiff = "broker.difference"
)

// AlertScan looks at the plans, books, income, broker differences and orders of
// the tenant and says which alerts hold now. It reads and writes nothing. A
// rule whose producer failed is left out of Scanned, and its error is returned,
// so a failed look never withdraws an alert.
func (s Source) AlertScan(ctx context.Context, cfg alerts.Config) (alerts.Scan, []error) {
	sc := alerts.Scan{Scanned: map[alerts.Rule]bool{}}
	var errs []error
	run := func(rules []alerts.Rule, f func() ([]alerts.Alert, error)) {
		as, err := f()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rules[0], err))
			return
		}
		sc.Alerts = append(sc.Alerts, as...)
		for _, r := range rules {
			sc.Scanned[r] = true
		}
	}
	run([]alerts.Rule{alerts.PlanRevision}, func() ([]alerts.Alert, error) { return s.planAlerts(ctx) })
	run([]alerts.Rule{alerts.ReviewDue}, func() ([]alerts.Alert, error) { return s.reviewAlerts(ctx, cfg) })
	run([]alerts.Rule{alerts.DividendExDate, alerts.IncomeMissing, alerts.IncomeDifference}, func() ([]alerts.Alert, error) { return s.incomeAlerts(ctx, cfg) })
	run([]alerts.Rule{alerts.BrokerException}, func() ([]alerts.Alert, error) { return s.brokerAlerts(ctx) })
	run([]alerts.Rule{alerts.OrderUnknown}, func() ([]alerts.Alert, error) { return s.orderAlerts(ctx) })
	return sc, errs
}

// clean makes a name usable inside an alert key.
func clean(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "|", "~") }

func digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:10]
}

func (s Source) planAlerts(ctx context.Context) ([]alerts.Alert, error) {
	rows, err := s.PlanRows(ctx)
	if err != nil {
		return nil, err
	}
	var out []alerts.Alert
	for _, r := range rows {
		if r.State != string(plan.Draft) && r.State != string(plan.Proposed) {
			continue // accepted, rejected, expired or replaced: no decision is waiting
		}
		id, n, err := plan.ParseRef(r.Revision)
		if err != nil {
			return nil, err
		}
		what := r.Security
		if r.Name != "" {
			what += " (" + r.Name + ")"
		}
		qty := ""
		if r.Qty != "" {
			qty = fmt.Sprintf("- Quantity: %s\n", r.Qty)
		}
		out = append(out, alerts.Alert{
			Rule: alerts.PlanRevision, Subject: clean(id), Revision: fmt.Sprintf("r%d", n),
			Title: fmt.Sprintf("Plan %s is %s: %s %s", r.Revision, r.State, r.Action, r.Security),
			Body: fmt.Sprintf("- Plan: %s (%s)\n- Security: %s\n- Book: %s\n- Action: %s\n%s- Expires: %s\n- Data: %s; strategy: %s; execution: %s\n- Open questions: %d\n\nThe plan is a record of a view, not an order. Read it with `plan show %s`.",
				r.Revision, r.State, what, r.Book, r.Action, qty, r.Expiry, r.Data, r.Strategy, r.Execution, r.Unknowns, r.Revision),
			Link: "/plan/" + r.Revision,
		})
	}
	return out, nil
}

// firstEvent is the day of the first movement of the security in the ledger.
func firstEvent(l *portfolio.Ledger, sec string) string {
	first := ""
	for _, tx := range l.Txs() {
		d := tx.Event.UTC().Format(time.DateOnly)
		if first != "" && d >= first {
			continue
		}
		for _, e := range tx.Entries {
			if e.Security == sec {
				first = d
				break
			}
		}
	}
	return first
}

type reviewLogRec struct {
	Security string `json:"security"`
	Day      string `json:"day"`
}

func (s Source) reviewAlerts(ctx context.Context, cfg alerts.Config) ([]alerts.Alert, error) {
	day := s.Day()
	var out []alerts.Alert

	// accepted plans past their review date
	rows, err := s.PlanRows(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Recorded != string(plan.Accepted) || r.ReviewBy == "" || r.ReviewBy > day {
			continue
		}
		id, n, err := plan.ParseRef(r.Revision)
		if err != nil {
			return nil, err
		}
		out = append(out, alerts.Alert{
			Rule: alerts.ReviewDue, Subject: "plan/" + clean(id), Revision: r.ReviewBy,
			Title: fmt.Sprintf("Review of plan %s was due on %s", r.Revision, r.ReviewBy),
			Body: fmt.Sprintf("- Plan: %s (accepted revision %d)\n- Security: %s\n- Review by: %s\n\nAccepting a plan is not a standing authorization: read it again against what has happened (`plan show %s`) and record the outcome in the journal.",
				r.Revision, n, r.Security, r.ReviewBy, r.Revision),
			Link: "/plan/" + r.Revision,
		})
	}

	// holdings that have gone the review interval without a review
	rs := s.records()
	logs, err := rs.Current(ctx, KindReviewLog, "")
	if err != nil {
		return nil, err
	}
	last := map[string]string{}
	for _, r := range logs {
		var l reviewLogRec
		if err := json.Unmarshal(r.Body, &l); err != nil {
			return nil, fmt.Errorf("review log %s does not decode: %w", r.Key, err)
		}
		if l.Day > last[l.Security] {
			last[l.Security] = l.Day
		}
	}
	ps := s.DB.Portfolios(s.Tenant)
	bs, err := ps.Books(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		l, err := ps.Ledger(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		for _, pos := range l.Projection().Positions() {
			since := last[pos.Security]
			basis := "the last review"
			if since == "" {
				since, basis = firstEvent(l, pos.Security), "the first purchase"
			}
			t, err := time.Parse(time.DateOnly, since)
			if err != nil {
				continue
			}
			due := t.AddDate(0, 0, cfg.ReviewDays).Format(time.DateOnly)
			if due > day {
				continue
			}
			ref := b.Account + "/" + b.Name
			out = append(out, alerts.Alert{
				Rule: alerts.ReviewDue, Subject: clean(ref) + "/" + clean(pos.Security), Revision: due,
				Title: fmt.Sprintf("Review of %s in %s was due on %s", pos.Security, ref, due),
				Body: fmt.Sprintf("- Holding: %s %s in %s\n- Counted from %s on %s, with an interval of %d days\n\nRun `review run` for the findings and `review done` when you have looked.",
					pos.Qty.String(), pos.Security, ref, basis, since, cfg.ReviewDays),
				Link: "/book/" + b.ID,
			})
		}
	}
	return out, nil
}

func (s Source) incomeAlerts(ctx context.Context, cfg alerts.Config) ([]alerts.Alert, error) {
	day := s.Day()
	today, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return nil, err
	}
	var out []alerts.Alert
	ps := s.DB.Portfolios(s.Tenant)
	bs, err := ps.Books(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		l, err := ps.Ledger(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		payouts, err := s.Payouts(ctx, l)
		if err != nil {
			return nil, err
		}
		ref := b.Account + "/" + b.Name
		for _, p := range payouts {
			e := income.Expect(l, p, day)
			if e.Status != income.Entitled && e.Status != income.Pending {
				continue
			}
			ex, err := time.Parse(time.DateOnly, e.ExDay)
			if err != nil {
				continue
			}
			ahead := int(ex.Sub(today).Hours() / 24)
			if ahead > cfg.DividendDays || ahead < -cfg.DividendDays {
				continue
			}
			gross := "unknown"
			if e.Gross.Valid() {
				gross = e.Gross.String()
			}
			when := fmt.Sprintf("goes ex in %d days", ahead)
			switch {
			case ahead == 0:
				when = "goes ex today"
			case ahead < 0:
				when = fmt.Sprintf("went ex %d days ago", -ahead)
			}
			out = append(out, alerts.Alert{
				Rule: alerts.DividendExDate, Subject: clean(ref) + "/" + clean(e.Ledger), Revision: e.ExDay + "/" + clean(gross),
				Title: fmt.Sprintf("%s %s (%s)", p.Key.SecurityID, when, e.ExDay),
				Body: fmt.Sprintf("- Book: %s\n- Payout: %s\n- Ex-date: %s; pay date: %s\n- Shares counted: %s at %s %s\n- Gross: %s, before any tax withheld at source\n\nThe amount is expected, not booked: use `income accrue` once the payout is declared.",
					ref, e.Ledger, e.ExDay, orDash(e.PayDay), e.Shares.String(), e.PerShare.String(), e.Currency, gross),
				Link: "/income",
			})
		}
		for _, f := range income.Check(l, payouts, day) {
			rule := alerts.IncomeDifference
			if f.Kind == income.MissingAccrual || f.Kind == income.PaymentOverdue {
				rule = alerts.IncomeMissing
			}
			out = append(out, alerts.Alert{
				Rule: rule, Subject: clean(ref) + "/" + clean(f.Payout), Revision: string(f.Kind) + "/" + digest(f.Expected, f.Ledger, f.Reason),
				Title: fmt.Sprintf("Income %s: %s", strings.ReplaceAll(string(f.Kind), "_", " "), f.Payout),
				Body: fmt.Sprintf("- Book: %s\n- Payout: %s\n- Expected: %s\n- In the ledger: %s\n- %s\n\nNothing was changed. Decide what is right, then book or reverse it yourself (`income check` shows the same finding).",
					ref, f.Payout, f.Expected, f.Ledger, f.Reason),
				Link: "/income",
			})
		}
	}
	return out, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

type brokerDiff struct {
	State   string `json:"state"`
	Episode int    `json:"episode"`
	Book    string `json:"book"`
	Diff    struct {
		Kind    string `json:"kind"`
		Subject string `json:"subject"`
		Local   string `json:"local"`
		Broker  string `json:"broker"`
		Hint    string `json:"hint"`
	} `json:"difference"`
}

func (s Source) brokerAlerts(ctx context.Context) ([]alerts.Alert, error) {
	recs, err := s.records().Current(ctx, KindBrokerDiff, "")
	if err != nil {
		return nil, err
	}
	names, err := s.bookNames(ctx)
	if err != nil {
		return nil, err
	}
	var out []alerts.Alert
	for _, r := range recs {
		var d brokerDiff
		if err := json.Unmarshal(r.Body, &d); err != nil {
			return nil, fmt.Errorf("broker difference %s does not decode: %w", r.Key, err)
		}
		if d.State != "open" {
			continue
		}
		book := names[d.Book]
		if book == "" {
			book = d.Book
		}
		out = append(out, alerts.Alert{
			Rule: alerts.BrokerException, Subject: clean(d.Book) + "/" + digest(r.Key), Revision: fmt.Sprintf("e%d", d.Episode),
			Title: fmt.Sprintf("Broker difference in %s: %s %s", book, strings.ReplaceAll(d.Diff.Kind, "_", " "), d.Diff.Subject),
			Body: fmt.Sprintf("- Book: %s\n- Kind: %s\n- Subject: %s\n- Here: %s\n- At the broker: %s\n- %s\n\nNothing was changed on either side. `broker differences` lists it.",
				book, d.Diff.Kind, d.Diff.Subject, d.Diff.Local, d.Diff.Broker, d.Diff.Hint),
			Link: "/book/" + d.Book,
		})
	}
	return out, nil
}

func (s Source) orderAlerts(ctx context.Context) ([]alerts.Alert, error) {
	rows, err := s.OrderRows(ctx)
	if err != nil {
		return nil, err
	}
	var out []alerts.Alert
	for _, r := range rows {
		if r.State != string(order.Unknown) {
			continue
		}
		out = append(out, alerts.Alert{
			Rule: alerts.OrderUnknown, Subject: clean(r.ID), Revision: "unknown",
			Title: fmt.Sprintf("Order %s is in an unknown state: %s %s %s", r.ID, r.Side, r.Qty, r.Security),
			Body: fmt.Sprintf("- Order: %s (%s book %s)\n- %s %s %s, filled %s\n- The log and the world may disagree: the program does not know whether the broker has this order.\n\nThe program will not send it again. `order recover` lists the orders that need reconciliation; settle this one with `order resolve` or, for a broker-bound book, `broker reconcile`.",
				r.ID, r.Env, nameOr(r.BookName, r.Book), r.Side, r.Qty, r.Security, r.Filled),
			Link: "/order/" + r.ID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

func nameOr(name, id string) string {
	if name != "" {
		return name
	}
	return id
}
