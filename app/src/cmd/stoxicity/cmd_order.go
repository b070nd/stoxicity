package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/b070nd/stoxicity/app/src/broker"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/store"
)

// listFlag is a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, "; ") }
func (l *listFlag) Set(s string) error { *l = append(*l, s); return nil }

var _ flag.Value = (*listFlag)(nil)

// cmdOrder keeps the local record of orders: the exact terms, the cash or shares
// held for them, and every fact learned about them. Nothing here reaches a
// broker; the only sender is the local paper engine's acknowledgement.
func cmdOrder(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: draft | confirm | submit | cancel | report | resolve | list | show | control | recover")
	}
	switch args[0] {
	case "draft":
		return orderDraft(ctx, a, args[1:])
	case "confirm":
		return orderConfirm(ctx, a, args[1:])
	case "submit":
		return orderSubmit(ctx, a, args[1:])
	case "cancel":
		return orderCancel(ctx, a, args[1:])
	case "report":
		return orderReport(ctx, a, args[1:])
	case "resolve":
		return orderResolve(ctx, a, args[1:])
	case "list":
		return orderList(ctx, a, args[1:])
	case "show":
		return orderShow(ctx, a, args[1:])
	case "control":
		return orderControl(ctx, a, args[1:])
	case "recover":
		return orderRecover(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (draft, confirm, submit, cancel, report, resolve, list, show, control, recover)", args[0])
}

func (a *app) orders() (store.Orders, error) {
	if _, err := a.requireTenant(); err != nil {
		return store.Orders{}, err
	}
	db, err := a.store()
	if err != nil {
		return store.Orders{}, err
	}
	db.SetClock(a.now)
	return db.Orders(a.tenantID), nil
}

// orderService reads the account limits from the active policy.
func orderService(ctx context.Context, a *app) (order.Service, error) {
	src, err := workflowSource(a)
	if err != nil {
		return order.Service{}, err
	}
	return src.OrderService(ctx)
}

// findOrder returns an order's view and the reference of its book.
func findOrder(ctx context.Context, a *app, id string) (order.View, error) {
	ords, err := a.orders()
	if err != nil {
		return order.View{}, err
	}
	logs, err := ords.Logs(ctx, "")
	if err != nil {
		return order.View{}, err
	}
	log, ok := logs[id]
	if !ok {
		return order.View{}, fmt.Errorf("no order %s (order list shows them)", id)
	}
	return order.Fold(log)
}

func orderDraft(ctx context.Context, a *app, args []string) error {
	fs := newFlags("order draft")
	sec := fs.String("security", "", "security ID")
	side := fs.String("side", "", "buy or sell")
	qty := fs.String("qty", "", "quantity (whole shares unless -fractional)")
	limit := fs.String("limit", "", "limit price per share")
	cur := fs.String("currency", "", "currency the price is in (default: the plan's price currency, then the book's base)")
	fee := fs.String("fee", "", "known fees of the whole order, e.g. 2.00 EUR")
	tick := fs.String("tick", "", "price increment the limit must be a multiple of")
	frac := fs.Bool("fractional", false, "the account is stated to trade part shares")
	planRef_ := fs.String("plan", "", "an accepted plan revision to take the security, quantity, limit ceiling and fee from")
	valid := fs.String("valid", "24h", "how long the draft may wait for a confirmation")
	by := fs.String("by", "", "who is drafting")
	var warns listFlag
	fs.Var(&warns, "warn", "a warning to carry on the ticket (repeatable)")
	asJSON := fs.Bool("json", false, "print the order as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" || *by == "" {
		return usageErrorf("usage: order draft BOOK (-plan PLAN.rN | -security S -side buy|sell -qty Q -limit P) -by NAME [-fee AMOUNT] [-currency C] [-valid 24h]")
	}
	ttl, err := time.ParseDuration(*valid)
	if err != nil || ttl <= 0 {
		return usageErrorf("-valid %q is not a duration like 24h", *valid)
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	b, err := ps.FindBook(ctx, book)
	if err != nil {
		return err
	}
	t := order.Terms{Book: b.ID, Account: b.Account, Env: b.Env, Security: *sec, Side: order.Side(*side), Type: "limit", TIF: "DAY", Fractional: *frac}
	if *planRef_ != "" {
		if err := termsFromPlan(ctx, a, b, *planRef_, &t, *limit, *qty); err != nil {
			return err
		}
	} else {
		if *sec == "" || *side == "" || *qty == "" || *limit == "" {
			return usageErrorf("without -plan an order needs -security, -side, -qty and -limit")
		}
		if t.Qty, err = money.ParseDecimal(*qty); err != nil {
			return usageErrorf("-qty: %v", err)
		}
		if t.Limit, err = money.ParseDecimal(*limit); err != nil {
			return usageErrorf("-limit: %v", err)
		}
	}
	if *cur != "" {
		t.Currency = money.Currency(strings.ToUpper(*cur))
	}
	if t.Currency == "" {
		t.Currency = b.Base
	}
	if *fee != "" {
		fa, err := money.ParseAmount(*fee)
		if err != nil {
			return usageErrorf("-fee: %v", err)
		}
		if fa.Currency() != t.Currency {
			return usageErrorf("-fee is in %s but the order is in %s", fa.Currency(), t.Currency)
		}
		t.FeeEstimate = fa.Minor()
	}
	if *tick != "" {
		if t.Tick, err = money.ParseDecimal(*tick); err != nil {
			return usageErrorf("-tick: %v", err)
		}
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	rec, active, err := db.Policies(a.tenantID).AsOf(ctx, time.Time{})
	if err != nil {
		return err
	}
	if active {
		t.PolicyEpoch, t.PolicyHash = rec.Epoch, rec.Hash
	} else if b.Env != portfolio.PaperLocal {
		return errors.New("no policy is active: an order for a real or broker book is checked against one (policy activate)")
	}
	warnings := []string(warns)
	if b.Env != portfolio.PaperLocal {
		warnings = append(warnings, "no executing quote has been validated: the price collar and quote freshness are not checked here")
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	id, err := svc.Draft(ctx, b.ID, order.DraftRequest{Terms: t, Warnings: warnings, By: *by, Valid: ttl})
	if err != nil {
		return err
	}
	v, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, orderJSON(v))
	}
	fmt.Fprintf(a.stdout, "%s drafted for %s (%s). The cash or shares it needs are held now, so no other proposal can spend them.\n", id, b.Account+"/"+b.Name, b.Env)
	printTicket(a, v)
	fmt.Fprintf(a.stdout, "Nothing was sent. Next: order confirm %s -by NAME%s, then order submit.\n", id, ackHint(v))
	return nil
}

func ackHint(v order.View) string {
	if len(v.Warnings) == 0 {
		return ""
	}
	return ` -ack "<each warning, exactly as shown>"`
}

// termsFromPlan fills the terms from an accepted, current plan revision. The
// plan sets the security, the side and a quantity; its entry ceiling bounds the
// limit.
func termsFromPlan(ctx context.Context, a *app, b portfolio.Book, ref string, t *order.Terms, limit, qty string) error {
	id, n, err := planRef(ref)
	if err != nil {
		return err
	}
	repo, err := planRepo(a)
	if err != nil {
		return err
	}
	rv, err := repo.Get(ctx, id, n)
	if err != nil {
		return err
	}
	st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
	if err != nil {
		return err
	}
	if eff := plan.Effective(st, rv.Card.Expiry, a.now()); eff != plan.Accepted {
		return fmt.Errorf("%s is %s: only an accepted plan that has not expired can be drafted as an order", rv.ID(), eff)
	}
	cur, err := currentManifest(ctx, a, rv)
	if err != nil {
		return err
	}
	if ch := rv.Manifest.Changed(cur); len(ch) > 0 {
		return fmt.Errorf("%s is stale: %s changed since it was generated", rv.ID(), strings.Join(ch, ", "))
	}
	if rv.Card.Book != b.ID && rv.Card.Book != b.Account+"/"+b.Name {
		return fmt.Errorf("%s is for book %s, not %s/%s", rv.ID(), rv.Card.Book, b.Account, b.Name)
	}
	c := rv.Card
	switch c.Action {
	case "BUY", "ADD":
		t.Side = order.Buy
	case "REDUCE", "SELL":
		t.Side = order.Sell
	default:
		return fmt.Errorf("%s says %s: only BUY, ADD, REDUCE and SELL become orders", rv.ID(), c.Action)
	}
	if c.Sizing == nil {
		return fmt.Errorf("%s has no quantity: give -qty", rv.ID())
	}
	t.Security, t.PlanID, t.PlanRevision = c.Security, rv.PlanID, rv.Rev
	if qty != "" {
		if t.Qty, err = money.ParseDecimal(qty); err != nil {
			return usageErrorf("-qty: %v", err)
		}
		if q, err := money.ParseDecimal(c.Sizing.Qty); err == nil && t.Qty.Cmp(q) > 0 {
			return fmt.Errorf("-qty %s is more than the %s the plan sized", qty, c.Sizing.Qty)
		}
	} else if t.Qty, err = money.ParseDecimal(c.Sizing.Qty); err != nil {
		return fmt.Errorf("the plan quantity %q: %w", c.Sizing.Qty, err)
	}
	if c.Sizing.PriceCurrency != "" {
		t.Currency = money.Currency(c.Sizing.PriceCurrency)
	}
	ceiling := c.Sizing.EntryMax
	switch {
	case limit != "":
		if t.Limit, err = money.ParseDecimal(limit); err != nil {
			return usageErrorf("-limit: %v", err)
		}
		if ceiling != "" && t.Side == order.Buy {
			if cd, err := money.ParseDecimal(ceiling); err == nil && t.Limit.Cmp(cd) > 0 {
				return fmt.Errorf("-limit %s is above the plan's entry ceiling %s", limit, ceiling)
			}
		}
	case ceiling != "" && t.Side == order.Buy:
		if t.Limit, err = money.ParseDecimal(ceiling); err != nil {
			return fmt.Errorf("the plan entry ceiling %q: %w", ceiling, err)
		}
	default:
		return usageErrorf("%s sets no price for a %s: give -limit", rv.ID(), c.Action)
	}
	return nil
}

func printTicket(a *app, v order.View) {
	t := v.Terms
	notional, _ := t.Notional(t.Qty)
	hold := ""
	if v.Reservation != "" {
		hold = v.Reservation
	}
	rows := [][]cliCell{
		{plain("order"), styled(v.ID, cliStrong)},
		{plain("account / environment"), plain(fmt.Sprintf("%s / %s", t.Account, t.Env))},
		{plain("book"), plain(t.Book)},
		{plain("security"), plain(t.Security)},
		{plain("side"), plain(string(t.Side))},
		{plain("quantity"), plain(t.Qty.String())},
		{plain("type"), plain(fmt.Sprintf("%s %s at most %s %s", t.Type, t.TIF, t.Limit, t.Currency))},
		{plain("value at the limit"), plain(notional.String())},
		{plain("known fees"), plain(feeText(t))},
		{plain("most cash it can use"), plain(commitText(t, v))},
		{plain("plan"), plain(planText(t))},
		{plain("policy"), plain(policyText(t))},
		{plain("terms hash"), plain(v.TermsHash)},
		{plain("hold"), plain(orDash(hold))},
		{plain("lapses"), plain(v.Expires.UTC().Format(time.RFC3339))},
		{plain("state"), orderStateCell(v.State)},
	}
	t2 := cliTable{Header: []string{"term", "value"}, Rows: rows}
	t2.write(a.stdout, useColor(a.stdout))
	for _, w := range v.Warnings {
		fmt.Fprintf(a.stdout, "warning: %s\n", w)
	}
}

func feeText(t order.Terms) string {
	if t.FeeEstimate == 0 {
		return "none known"
	}
	f, _ := money.FromMinor(t.FeeEstimate, t.Currency)
	return f.String()
}

func commitText(t order.Terms, v order.View) string {
	if t.Side == order.Sell {
		return fmt.Sprintf("none: it holds %s shares of %s", v.HoldQty(), t.Security)
	}
	c, err := t.Commitment(v.HoldQty(), v.FeesPaid)
	if err != nil {
		return "unavailable: " + err.Error()
	}
	return c.String()
}

func planText(t order.Terms) string {
	if t.PlanID == "" {
		return "none: drafted by hand"
	}
	return fmt.Sprintf("%s.r%d", t.PlanID, t.PlanRevision)
}

func policyText(t order.Terms) string {
	if t.PolicyHash == "" {
		return "none active"
	}
	return fmt.Sprintf("epoch %d (%s)", t.PolicyEpoch, t.PolicyHash)
}

func orderStateCell(s order.State) cliCell {
	switch s {
	case order.Unknown, order.Rejected:
		return styled(string(s), cliRed)
	case order.Cancelled, order.Expired, order.CancelRequested:
		return styled(string(s), cliYellow)
	case order.Filled:
		return styled(string(s), cliGreen)
	case order.Open, order.PartiallyFilled, order.Submitting:
		return styled(string(s), cliStrong)
	}
	return plain(string(s))
}

type orderOut struct {
	ID        string   `json:"id"`
	State     string   `json:"state"`
	Account   string   `json:"account"`
	Book      string   `json:"book"`
	Env       string   `json:"env"`
	Security  string   `json:"security"`
	Side      string   `json:"side"`
	Qty       string   `json:"qty"`
	Limit     string   `json:"limit"`
	Currency  string   `json:"currency"`
	Filled    string   `json:"filled"`
	Remaining string   `json:"remaining"`
	Hold      string   `json:"hold,omitempty"`
	BrokerRef string   `json:"broker_ref,omitempty"`
	TermsHash string   `json:"terms_hash"`
	Plan      string   `json:"plan,omitempty"`
	Why       []string `json:"why,omitempty"`
	Late      []string `json:"late,omitempty"`
}

func orderJSON(v order.View) orderOut {
	t := v.Terms
	return orderOut{ID: v.ID, State: string(v.State), Account: t.Account, Book: t.Book, Env: string(t.Env), Security: t.Security, Side: string(t.Side),
		Qty: t.Qty.String(), Limit: t.Limit.String(), Currency: string(t.Currency), Filled: v.Filled.String(), Remaining: v.Remaining.String(),
		Hold: v.Reservation, BrokerRef: v.BrokerRef, TermsHash: v.TermsHash, Plan: planText(t), Why: v.Why, Late: v.Late}
}

func orderConfirm(ctx context.Context, a *app, args []string) error {
	fs := newFlags("order confirm")
	by := fs.String("by", "", "who is confirming")
	ttl := fs.String("ttl", "15m", "how long the confirmation stays valid")
	ticket := fs.Bool("ticket", false, "confirm only as a ticket for placing by hand, even when a broker is connected")
	var acks listFlag
	fs.Var(&acks, "ack", "a warning acknowledged, exactly as shown (repeatable)")
	id, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if id == "" || *by == "" {
		return usageErrorf("usage: order confirm ORDER -by NAME [-ack WARNING]... [-ttl 15m] [-ticket]")
	}
	d, err := time.ParseDuration(*ttl)
	if err != nil || d <= 0 {
		return usageErrorf("-ttl %q is not a duration like 15m", *ttl)
	}
	v, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	if br, cfg, b, account, bound, err := brokerOrderContext(ctx, a, v); err != nil {
		return err
	} else if bound && !*ticket {
		pf, err := runPreflight(ctx, a, br, cfg, b, account, v, false)
		if err != nil {
			return err
		}
		if len(pf.failures()) > 0 {
			pf.print(a)
			return pf.failureError(id)
		}
		if pf.Valid < d {
			d = pf.Valid
		}
		// the checks above are the validation the draft's warning says is missing
		acked := append(append([]string(nil), acks...), quoteWarning)
		if err := svc.ConfirmBound(ctx, v.Terms.Book, id, *by, acked, d, order.Binding{Payload: pf.Payload, Warnings: pf.Warnings}); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s confirmed by %s for the broker: exactly these terms (hash %s) and this payload (%s), valid %s. A change in the broker's view of the order, the account, the plan or the funds invalidates it. Nothing was sent.\n", id, *by, v.TermsHash, pf.Payload[:12], d)
		return nil
	} else if !bound && v.Terms.Env != portfolio.PaperLocal {
		fmt.Fprintln(a.stdout, "no broker account is bound to this book: this is a ticket for placing by hand, and it cannot be sent from here.")
	}
	if err := svc.Confirm(ctx, v.Terms.Book, id, *by, acks, d); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s confirmed by %s for exactly these terms (hash %s), valid %s. Any change to the terms, the hold or the plan needs a new confirmation. Nothing was sent.\n", id, *by, v.TermsHash, *ttl)
	return nil
}

// localSender is the local paper engine's side of a submission: it
// acknowledges and nothing else. Fills are reported with order report or by the
// paper engine; a book that reaches a broker has no sender here.
func localSender(ctx context.Context, s order.Submission) (order.SendResult, error) {
	return order.SendResult{Broker: "local:" + s.OrderID}, nil
}

func orderSubmit(ctx context.Context, a *app, args []string) error {
	fs := newFlags("order submit")
	by := fs.String("by", "", "who is submitting")
	id, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if id == "" || *by == "" {
		return usageErrorf("usage: order submit ORDER -by NAME")
	}
	v, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	if v.Terms.Env != portfolio.PaperLocal {
		return submitToBroker(ctx, a, v, *by)
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	if err := svc.Submit(ctx, v.Terms.Book, id, *by, localSender); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s acknowledged by the local paper engine. No broker was contacted. Fills come from order report or the paper engine.\n", id)
	return nil
}

func orderCancel(ctx context.Context, a *app, args []string) error {
	fs := newFlags("order cancel")
	by := fs.String("by", "", "who is cancelling")
	id, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if id == "" || *by == "" {
		return usageErrorf("usage: order cancel ORDER -by NAME")
	}
	v, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	var ask order.Canceller
	if v.Terms.Env == portfolio.PaperLocal {
		ask = func(context.Context, string, string) error { return nil }
	} else if br, cfg, _, account, bound, err := brokerOrderContext(ctx, a, v); err != nil {
		return err
	} else if bound {
		ask = func(ctx context.Context, _, ref string) error {
			switch {
			case cfg.Mode != broker.Trading:
				return errors.New("the connection is read_only, so nothing was sent to the broker: cancel it at the broker yourself")
			case ref == "":
				return errors.New("the broker's order ID is not known yet: broker sync finds it")
			}
			return br.Cancel(ctx, account, ref)
		}
	}
	if err := svc.Cancel(ctx, v.Terms.Book, id, *by, ask); err != nil {
		return err
	}
	nv, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	if nv.State == order.CancelRequested {
		fmt.Fprintf(a.stdout, "%s: cancel requested. It is not cancelled until the other side says so: the hold stays, and a fill may still arrive. Record the outcome with order report -kind cancelled.\n", id)
		return nil
	}
	fmt.Fprintf(a.stdout, "%s is %s; what it held was released.\n", id, nv.State)
	return nil
}

func orderReport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("order report")
	kind := fs.String("kind", "", "ack, fill, cancelled, rejected or expired")
	key := fs.String("key", "", "the broker's reference for this fact (makes it idempotent)")
	broker := fs.String("broker", "", "with ack: the broker's order reference")
	fillID := fs.String("fill-id", "", "with fill: the execution ID or statement line")
	qty := fs.String("qty", "", "with fill: shares")
	price := fs.String("price", "", "with fill: price per share")
	fee := fs.String("fee", "", "with fill: commission, e.g. 1.00 EUR")
	gross := fs.String("gross", "", "with fill: consideration if the broker states it, e.g. 497.50 EUR")
	settle := fs.String("settle-day", "", "with fill: settlement day")
	at := fs.String("at", "", "with fill: when it executed (RFC 3339 or a day)")
	reason := fs.String("reason", "", "with rejected: what the broker said")
	id, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if id == "" || *kind == "" {
		return usageErrorf("usage: order report ORDER -kind ack|fill|cancelled|rejected|expired [...]")
	}
	r := order.Reported{Key: *key, Broker: *broker, Reason: *reason}
	switch *kind {
	case "ack":
		r.Kind = order.Acknowledged
	case "cancelled":
		r.Kind = order.CancelledEv
	case "rejected":
		r.Kind = order.RejectedEv
		if *reason == "" {
			return usageErrorf("a rejection says what the broker said (-reason)")
		}
	case "expired":
		r.Kind = order.ExpiredEv
	case "fill":
		r.Kind = order.FillEv
		if *fillID == "" || *qty == "" || *price == "" {
			return usageErrorf("a fill needs -fill-id, -qty and -price")
		}
		f := order.Fill{ID: *fillID, SettleDay: *settle}
		if f.Qty, err = money.ParseDecimal(*qty); err != nil {
			return usageErrorf("-qty: %v", err)
		}
		if f.Price, err = money.ParseDecimal(*price); err != nil {
			return usageErrorf("-price: %v", err)
		}
		if *fee != "" {
			fa, err := money.ParseAmount(*fee)
			if err != nil {
				return usageErrorf("-fee: %v", err)
			}
			f.Fee = fa.Minor()
		}
		if *gross != "" {
			ga, err := money.ParseAmount(*gross)
			if err != nil {
				return usageErrorf("-gross: %v", err)
			}
			f.Gross = ga.Minor()
		}
		if *at != "" {
			if f.At, err = portfolioAsOf(*at); err != nil {
				return err
			}
		}
		r.Fill = &f
	default:
		return usageErrorf("-kind %q is not ack, fill, cancelled, rejected or expired", *kind)
	}
	v, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	if err := svc.Report(ctx, v.Terms.Book, id, r); err != nil {
		return err
	}
	nv, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s is %s: %s of %s filled, %s left.\n", id, nv.State, nv.Filled, nv.Terms.Qty, nv.Remaining)
	for _, w := range nv.Why {
		fmt.Fprintf(a.stdout, "needs a person: %s\n", w)
	}
	for _, l := range nv.Late {
		fmt.Fprintf(a.stdout, "out of order: %s\n", l)
	}
	return nil
}

func orderResolve(ctx context.Context, a *app, args []string) error {
	fs := newFlags("order resolve")
	by := fs.String("by", "", "who compared the order with the other side")
	note := fs.String("note", "", "what was found")
	id, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if id == "" || *by == "" || *note == "" {
		return usageErrorf("usage: order resolve ORDER -by NAME -note WHAT-YOU-FOUND")
	}
	v, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	if err := svc.Resolve(ctx, v.Terms.Book, id, *by, *note); err != nil {
		return err
	}
	nv, _ := findOrder(ctx, a, id)
	fmt.Fprintf(a.stdout, "%s: recorded that %s looked: %s. It is now %s. The facts it holds are unchanged.\n", id, *by, *note, nv.State)
	return nil
}

func orderList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("order list")
	asJSON := fs.Bool("json", false, "print JSON")
	open := fs.Bool("open", false, "only orders that are not closed")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	vs, err := svc.Views(ctx, "")
	if err != nil {
		return err
	}
	bookID := ""
	if book != "" {
		ps, err := a.portfolios()
		if err != nil {
			return err
		}
		b, err := ps.FindBook(ctx, book)
		if err != nil {
			return err
		}
		bookID = b.ID
	}
	var out []order.View
	for _, v := range vs {
		if (bookID != "" && v.Terms.Book != bookID) || (*open && v.Terminal()) {
			continue
		}
		out = append(out, v)
	}
	if *asJSON {
		js := make([]orderOut, len(out))
		for i, v := range out {
			js[i] = orderJSON(v)
		}
		return printJSON(a, js)
	}
	if len(out) == 0 {
		fmt.Fprintln(a.stdout, "no orders")
		return nil
	}
	t := cliTable{Header: []string{"order", "environment", "account", "security", "side", "qty", "filled", "limit", "state", "plan"}, Right: []bool{false, false, false, false, false, true, true, true, false, false}}
	for _, v := range out {
		t.Rows = append(t.Rows, []cliCell{styled(v.ID, cliStrong), envCell(v.Terms.Env), plain(v.Terms.Account), plain(v.Terms.Security), plain(string(v.Terms.Side)),
			plain(v.Terms.Qty.String()), plain(v.Filled.String()), plain(v.Terms.Limit.String() + " " + string(v.Terms.Currency)), orderStateCell(v.State), plain(planText(v.Terms))})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func orderShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("order show")
	asJSON := fs.Bool("json", false, "print JSON")
	id, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if id == "" {
		return usageErrorf("usage: order show ORDER")
	}
	ords, err := a.orders()
	if err != nil {
		return err
	}
	log, err := ords.Events(ctx, id)
	if err != nil {
		return err
	}
	v, err := order.Fold(log)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, struct {
			Order  orderOut      `json:"order"`
			Events []order.Event `json:"events"`
		}{orderJSON(v), log})
	}
	printTicket(a, v)
	fmt.Fprintf(a.stdout, "\nfilled %s of %s, %s left; fees paid %s\n", v.Filled, v.Terms.Qty, v.Remaining, minorText(v.FeesPaid, v.Terms.Currency))
	if v.BrokerRef != "" {
		fmt.Fprintf(a.stdout, "broker reference %s\n", v.BrokerRef)
	}
	for _, w := range v.Why {
		fmt.Fprintf(a.stdout, "needs a person: %s\n", w)
	}
	for _, l := range v.Late {
		fmt.Fprintf(a.stdout, "out of order: %s\n", l)
	}
	fmt.Fprintln(a.stdout)
	t := cliTable{Header: []string{"#", "recorded", "event", "by", "detail"}, Right: []bool{true}}
	for _, e := range log {
		t.Rows = append(t.Rows, []cliCell{plain(fmt.Sprint(e.N)), plain(e.At.UTC().Format(time.RFC3339)), styled(string(e.Kind), cliStrong), plain(orDash(e.By)), plain(eventDetail(e))})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func eventDetail(e order.Event) string {
	var p []string
	if e.Reservation != "" {
		p = append(p, "hold "+e.Reservation)
	}
	if e.Held != "" {
		p = append(p, "holding "+e.Held)
	}
	if e.Fill != nil {
		posted := "in the ledger"
		if !e.Fill.Posted {
			posted = "NOT in the ledger"
		}
		p = append(p, fmt.Sprintf("fill %s: %s at %s, fee %d minor units, %s", e.Fill.ID, e.Fill.Qty, e.Fill.Price, e.Fill.Fee, posted))
	}
	if e.Broker != "" {
		p = append(p, "broker "+e.Broker)
	}
	if e.Hash != "" {
		p = append(p, "confirmation "+e.Hash)
	}
	if !e.Expires.IsZero() && e.Kind != order.Drafted {
		p = append(p, "valid until "+e.Expires.UTC().Format(time.RFC3339))
	}
	if e.Kind == order.Drafted && !e.Expires.IsZero() {
		p = append(p, "lapses "+e.Expires.UTC().Format(time.RFC3339))
	}
	if len(e.Acked) > 0 {
		p = append(p, fmt.Sprintf("acknowledged %q", e.Acked))
	}
	if e.Attempt > 0 {
		p = append(p, fmt.Sprintf("attempt %d", e.Attempt))
	}
	if e.Reason != "" {
		p = append(p, e.Reason)
	}
	return strings.Join(p, "; ")
}

func orderControl(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: show | enable | disable")
	}
	ords, err := a.orders()
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		cs, err := ords.Controls(ctx)
		if err != nil {
			return err
		}
		t := cliTable{Header: []string{"environment", "new orders", "since", "by", "reason"}}
		for _, env := range []portfolio.Env{portfolio.Real, portfolio.PaperBroker, portfolio.PaperLocal} {
			c, set := cs[env]
			if !set {
				c = order.DefaultControl(env)
			}
			state, style := "enabled", cliGreen
			if c.Disabled {
				state, style = "DISABLED", cliRed
			}
			since := "default"
			if set {
				since = c.At.UTC().Format(time.RFC3339)
			}
			t.Rows = append(t.Rows, []cliCell{styled(string(env), cliStrong), styled(state, style), plain(since), plain(orDash(c.By)), plain(orDash(c.Reason))})
		}
		t.write(a.stdout, useColor(a.stdout))
		fmt.Fprintln(a.stdout, "Cancelling and reconciling stay possible whatever this says.")
		return nil
	case "enable", "disable":
		fs := newFlags("order control " + args[0])
		envf := fs.String("env", "", "real, paper_broker or paper_local")
		reason := fs.String("reason", "", "why")
		by := fs.String("by", "", "who")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 || *envf == "" || *reason == "" || *by == "" {
			return usageErrorf("usage: order control %s -env ENV -reason TEXT -by NAME", args[0])
		}
		env := portfolio.Env(*envf)
		if !env.Valid() {
			return usageErrorf("-env %q is not real, paper_broker or paper_local", *envf)
		}
		if err := ords.SetControl(ctx, env, args[0] == "disable", *reason, *by); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "new orders for %s: %sd by %s (%s)\n", env, args[0], *by, *reason)
		return nil
	}
	return usageErrorf("unknown subcommand %q (show, enable, disable)", args[0])
}

// orderRecover is what happens when the program starts again: anything caught
// submitting becomes unknown, lapsed unsent orders free what they held, and
// new orders for environments that reach a broker are switched off until
// someone turns them back on after looking at the broker.
func orderRecover(ctx context.Context, a *app, args []string) error {
	fs := newFlags("order recover")
	by := fs.String("by", "", "who is recovering")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *by == "" {
		return usageErrorf("usage: order recover -by NAME")
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	books, err := ps.Books(ctx)
	if err != nil {
		return err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	ords, _ := a.orders()
	for _, env := range []portfolio.Env{portfolio.Real, portfolio.PaperBroker} {
		if err := ords.SetControl(ctx, env, true, "restart reconciliation: broker state not yet compared", *by); err != nil {
			return err
		}
	}
	for _, b := range books {
		if b.Status != portfolio.Active {
			continue
		}
		if err := svc.Recover(ctx, b.ID); err != nil {
			return err
		}
		if err := svc.Sweep(ctx, b.ID); err != nil {
			return err
		}
	}
	vs, err := svc.Views(ctx, "")
	if err != nil {
		return err
	}
	var unknown []string
	for _, v := range vs {
		if v.State == order.Unknown {
			unknown = append(unknown, v.ID)
		}
	}
	sort.Strings(unknown)
	fmt.Fprintln(a.stdout, "new orders for real and paper_broker books are switched off until you enable them (order control enable) after comparing with the broker.")
	if len(unknown) == 0 {
		fmt.Fprintln(a.stdout, "no order is waiting for reconciliation.")
		return nil
	}
	fmt.Fprintf(a.stdout, "%d order(s) need reconciliation, none will be sent again: %s\n", len(unknown), strings.Join(unknown, ", "))
	return nil
}

// submitToBroker sends a confirmed order to the broker it is bound to. Every
// check runs again now, against the broker as it is, and the payload is
// compared with the one the confirmation covers before anything is recorded.
func submitToBroker(ctx context.Context, a *app, v order.View, by string) error {
	id := v.ID
	if _, ok, err := loadBrokerConfig(ctx, a); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("no broker is connected for %s orders: nothing can be sent, and %s stays %s. Place the order yourself, then record what happened with order report", v.Terms.Env, id, v.State)
	}
	br, cfg, b, account, bound, err := brokerOrderContext(ctx, a, v)
	if err != nil {
		return err
	}
	if !bound {
		return fmt.Errorf("%s is not bound to a broker account: nothing can be sent, and %s stays %s (broker bind)", v.Terms.Book, id, v.State)
	}
	if v.Confirmation == nil {
		return fmt.Errorf("%s is %s: confirm it against a broker preview first", id, v.State)
	}
	pf, err := runPreflight(ctx, a, br, cfg, b, account, v, true)
	if err != nil {
		return err
	}
	if len(pf.failures()) > 0 {
		pf.print(a)
		return pf.failureError(id)
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	if err := svc.SubmitBound(ctx, v.Terms.Book, id, by, pf.Payload, brokerSender(br, pf.Request, v.Confirmation.Acked)); err != nil {
		return err
	}
	nv, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	switch nv.State {
	case order.Unknown:
		fmt.Fprintf(a.stdout, "%s: the outcome is UNKNOWN (%s). It will not be sent again. Ask the broker with broker sync %s: an order the broker lists under this reference is acknowledged; one it does not list needs order resolve after you have looked.\n", id, strings.Join(nv.Why, "; "), v.Terms.Book)
	case order.Rejected:
		fmt.Fprintf(a.stdout, "%s was refused by the broker (%s); what it held was released.\n", id, strings.Join(nv.Why, "; "))
	default:
		fmt.Fprintf(a.stdout, "%s sent to account %s and acknowledged (broker order %s). Fills and the end of the order come from broker sync.\n", id, broker.Redact(account), nv.BrokerRef)
	}
	return nil
}
