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
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/report"
)

// The record kinds the producers read, written by the commands of the same
// names (review, broker).
const (
	KindReviewLog  = "review.log"
	KindBrokerDiff = "broker.difference"
	KindBrokerSync = "broker.sync"
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
	run([]alerts.Rule{alerts.BrokerException}, func() ([]alerts.Alert, error) { return s.brokerAlerts(ctx, cfg) })
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
	At        time.Time `json:"at"`
	Seen      int       `json:"seen"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// brokerSync is the record of one completed sync; LastClean is the last sync
// that found nothing to differ.
type brokerSync struct {
	Book      string    `json:"book"`
	At        time.Time `json:"at"`
	LastClean time.Time `json:"last_clean"`
}

// settleWindow is how long after an order closed the cash and positions of a
// ledger and a broker may disagree because one of them has not settled yet.
const settleWindow = 48 * time.Hour

// pendingActivity says whether a known open order, or one that closed within
// the settlement window, explains a difference. A difference with an explanation
// is shown but is not an incident.
func pendingActivity(kind, subject string, views []order.View, book string, now time.Time) (string, bool) {
	for _, v := range views {
		if v.Terms.Book != book {
			continue
		}
		cash := kind == "cash_mismatch"
		if !cash && v.Terms.Security != subject {
			continue
		}
		switch {
		case v.State == order.Unknown:
			return fmt.Sprintf("order %s is in an unknown state and has its own alert", v.ID), true
		case !v.State.Closed():
			return fmt.Sprintf("order %s is still working at the broker (%s)", v.ID, v.State), true
		case (v.State == order.Filled || v.State == order.PartiallyFilled) && !v.Updated.IsZero() && now.Sub(v.Updated) < settleWindow:
			return fmt.Sprintf("order %s was filled less than %d hours ago and may not have settled on both sides", v.ID, int(settleWindow/time.Hour)), true
		}
	}
	return "", false
}

// withinTolerance reports whether a cash difference is no larger than the
// rounding tolerance.
func withinTolerance(local, brk string, tol money.Decimal) bool {
	l, err1 := parseLeading(local)
	b, err2 := parseLeading(brk)
	if err1 != nil || err2 != nil {
		return false
	}
	d, err := l.Sub(b)
	if err != nil {
		return false
	}
	if d, err = d.Abs(); err != nil {
		return false
	}
	return d.Cmp(tol) <= 0
}

// parseLeading reads the number of an amount such as "12.34 EUR".
func parseLeading(s string) (money.Decimal, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return money.Decimal{}, fmt.Errorf("no number in %q", s)
	}
	return money.ParseDecimal(f[0])
}

func (s Source) brokerAlerts(ctx context.Context, cfg alerts.Config) ([]alerts.Alert, error) {
	recs, err := s.records().Current(ctx, KindBrokerDiff, "")
	if err != nil {
		return nil, err
	}
	books, err := s.Books(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]report.BookRef{}
	for _, b := range books {
		byID[b.ID] = b
	}
	syncs, err := s.records().Current(ctx, KindBrokerSync, "")
	if err != nil {
		return nil, err
	}
	lastClean := map[string]time.Time{}
	lastSync := map[string]time.Time{}
	for _, r := range syncs {
		var y brokerSync
		if err := json.Unmarshal(r.Body, &y); err != nil {
			return nil, fmt.Errorf("broker sync %s does not decode: %w", r.Key, err)
		}
		lastClean[y.Book], lastSync[y.Book] = y.LastClean, y.At
	}
	var views []order.View
	loaded := false
	now := s.now().UTC()
	var out []alerts.Alert
	for _, r := range recs {
		var d brokerDiff
		if err := json.Unmarshal(r.Body, &d); err != nil {
			return nil, fmt.Errorf("broker difference %s does not decode: %w", r.Key, err)
		}
		if d.State != "open" {
			continue
		}
		b := byID[d.Book]
		name := b.Ref()
		if b.ID == "" {
			name = d.Book
		}
		seen, first, last := d.Seen, d.FirstSeen, d.LastSeen
		if seen == 0 {
			seen, first, last = 1, d.At, d.At
		}
		// Whether this is an incident: live money, found again and again over
		// time, not explained by an order in flight or by rounding.
		var why []string
		critical := true
		if b.Env != string(portfolio.Real) {
			critical = false
			why = append(why, "the book is a paper book, so nothing real is at risk")
		}
		if critical && seen < cfg.Syncs() {
			critical = false
			why = append(why, fmt.Sprintf("found by %d of %d syncs so far", seen, cfg.Syncs()))
		}
		if critical && last.Sub(first) < time.Duration(cfg.Minutes())*time.Minute {
			critical = false
			why = append(why, fmt.Sprintf("has stood for %d of %d minutes so far", int(last.Sub(first)/time.Minute), cfg.Minutes()))
		}
		if critical && d.Diff.Kind == "cash_mismatch" && withinTolerance(d.Diff.Local, d.Diff.Broker, cfg.Tolerance()) {
			critical = false
			why = append(why, "the cash difference is within the rounding tolerance")
		}
		if critical {
			if !loaded {
				svc, err := s.OrderService(ctx)
				if err != nil {
					return nil, err
				}
				if views, err = svc.Views(ctx, ""); err != nil {
					return nil, err
				}
				loaded = true
			}
			if reason, ok := pendingActivity(d.Diff.Kind, d.Diff.Subject, views, d.Book, now); ok {
				critical = false
				why = append(why, reason)
			}
		}
		rev := fmt.Sprintf("e%d", d.Episode)
		if critical {
			rev += ".urgent"
		}
		clean0 := "never"
		if t := lastClean[d.Book]; !t.IsZero() {
			clean0 = t.UTC().Format(time.RFC3339)
		}
		next := "Compare with the broker statement, then book the missing trade, fee or deposit, or ask the broker. Nothing here changes either side."
		if d.Diff.Hint != "" {
			next = d.Diff.Hint
		}
		var note string
		if critical {
			note = fmt.Sprintf("\n- Why this is urgent: real money, found by %d syncs over %d minutes, no order in flight explains it.", seen, int(last.Sub(first)/time.Minute))
		} else if len(why) > 0 {
			note = "\n- Not urgent because: " + strings.Join(why, "; ") + "."
		}
		out = append(out, alerts.Alert{
			Rule: alerts.BrokerException, Subject: clean(d.Book) + "/" + digest(r.Key), Revision: rev, Critical: critical,
			Title: fmt.Sprintf("Broker difference in %s: %s %s", name, strings.ReplaceAll(d.Diff.Kind, "_", " "), d.Diff.Subject),
			Body: fmt.Sprintf("- Book: %s (%s)\n- Account: %s\n- Kind: %s\n- Subject: %s\n- Here: %s\n- At the broker: %s\n- Last sync that matched: %s\n- Next: %s%s\n\nNothing was changed on either side. `broker differences` lists it.",
				name, orDash(b.Env), orDash(b.Account), d.Diff.Kind, d.Diff.Subject, d.Diff.Local, d.Diff.Broker, clean0, next, note),
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
		// An order that exists only in a paper book risks nothing real.
		live := r.Env == string(portfolio.Real)
		kind := "paper"
		if live {
			kind = "real"
		}
		out = append(out, alerts.Alert{
			Rule: alerts.OrderUnknown, Subject: clean(r.ID), Revision: "unknown", Critical: live,
			Title: fmt.Sprintf("Order %s is in an unknown state: %s %s %s", r.ID, r.Side, r.Qty, r.Security),
			Body: fmt.Sprintf("- Order: %s (%s book %s)\n- Money: %s\n- %s %s %s, filled %s\n- The log and the world may disagree: the program does not know whether the broker has this order.\n\nThe program will not send it again. `order recover` lists the orders that need reconciliation; settle this one with `order resolve` or, for a broker-bound book, `broker reconcile`.",
				r.ID, r.Env, nameOr(r.BookName, r.Book), kind, r.Side, r.Qty, r.Security, r.Filled),
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
