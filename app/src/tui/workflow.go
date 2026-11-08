package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/report"
)

// Workflow is implemented by a Backend that reads the tenant's plans, books,
// income, orders and policy. A backend without it still runs the news screens;
// the workflow screens then say what is missing. Every method reads stored
// records: none fetches data and none sends an order.
type Workflow interface {
	PolicyStatus(ctx context.Context) (report.PolicyStatus, error)
	PlanRows(ctx context.Context) ([]report.PlanRow, error)
	// PlanDoc is one revision (pl_x or pl_x.rN) and the numbers of all the
	// revisions the plan has.
	PlanDoc(ctx context.Context, ref string) (report.PlanDoc, []int, error)
	Books(ctx context.Context) ([]report.BookRef, error)
	BookReport(ctx context.Context, book string) (report.Report, error)
	Income(ctx context.Context) (report.IncomeView, error)
	OrderRows(ctx context.Context) ([]report.OrderRow, error)
	// ConfirmOrder records the go-ahead for a draft. typed is what the person
	// typed and must be the order's ID. It sends nothing.
	ConfirmOrder(ctx context.Context, id, typed string) error
}

// WorkflowData is what the workflow screens are built from.
type WorkflowData struct {
	// Unavailable: the backend does not read the workflow.
	Unavailable bool
	Today       string
	Policy      report.PolicyStatus
	Plans       []report.PlanRow
	Doc         *report.PlanDoc
	Revs        []int
	Books       []report.BookRef
	Book        *report.Report
	Income      *report.IncomeView
	Orders      []report.OrderRow
}

// loadWorkflow reads what the screen of the route needs.
func loadWorkflow(ctx context.Context, be Backend, r nav.Route, today string) (*WorkflowData, error) {
	wf, ok := be.(Workflow)
	if !ok {
		return &WorkflowData{Unavailable: true, Today: today}, nil
	}
	d := &WorkflowData{Today: today}
	var err error
	switch r.Kind {
	case nav.Plans:
		if d.Policy, err = wf.PolicyStatus(ctx); err == nil {
			d.Plans, err = wf.PlanRows(ctx)
		}
	case nav.Plan:
		var doc report.PlanDoc
		if doc, d.Revs, err = wf.PlanDoc(ctx, r.Arg); err == nil {
			d.Doc = &doc
			if d.Orders, err = wf.OrderRows(ctx); err == nil {
				d.Books, err = wf.Books(ctx)
			}
		}
	case nav.Portfolio:
		d.Books, err = wf.Books(ctx)
	case nav.Book:
		var rep report.Report
		if rep, err = wf.BookReport(ctx, r.Arg); err == nil {
			d.Book = &rep
		}
	case nav.Income:
		var v report.IncomeView
		if v, err = wf.Income(ctx); err == nil {
			d.Income = &v
		}
	case nav.Orders, nav.Order:
		d.Orders, err = wf.OrderRows(ctx)
	case nav.Policy:
		d.Policy, err = wf.PolicyStatus(ctx)
	}
	return d, err
}

// ---- text that is never cut -------------------------------------------------

const minWrap = 20

// wrapText breaks text into lines of at most w cells, at spaces, and inside a
// word that is wider than the line. Nothing is dropped.
func wrapText(text string, w int) []string {
	text = clean(text)
	if w < minWrap {
		w = minWrap
	}
	var lines []string
	cur := ""
	flush := func() {
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
	}
	for _, word := range strings.Fields(text) {
		for ansi.StringWidth(word) > w { // a word wider than the line
			flush()
			cut, rest := splitCells(word, w)
			lines = append(lines, cut)
			word = rest
		}
		switch {
		case cur == "":
			cur = word
		case ansi.StringWidth(cur)+1+ansi.StringWidth(word) <= w:
			cur += " " + word
		default:
			flush()
			cur = word
		}
	}
	flush()
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// splitCells cuts s after at most w cells.
func splitCells(s string, w int) (string, string) {
	used := 0
	for i, r := range s {
		rw := ansi.StringWidth(string(r))
		if used+rw > w {
			if i == 0 { // a cell wider than the line: take it whole
				_, size := firstRune(s)
				return s[:size], s[size:]
			}
			return s[:i], s[i:]
		}
		used += rw
	}
	return s, ""
}

func firstRune(s string) (rune, int) {
	for i, r := range s {
		_ = i
		return r, len(string(r))
	}
	return 0, 0
}

// wrapped adds the text as rows of at most w cells; the second and later lines
// are indented by hang.
func (b *builder) wrapped(w int, st Style, lead, hang, text string) {
	if w-ansi.StringWidth(lead) < minWrap && strings.TrimSpace(lead) != "" {
		// no room for the lead and a readable line beside it: the lead stands
		// alone and the text follows, indented
		b.add(Row{Text: strings.TrimRight(lead, " "), Style: st})
		lead = hang
		if lead == "" {
			lead, hang = "  ", "  "
		}
	}
	lw := w - ansi.StringWidth(lead)
	for i, l := range wrapText(text, lw) {
		p := lead
		if i > 0 {
			p = hang
		}
		b.add(Row{Text: p + l, Style: st})
	}
}

// packed adds the parts on as few lines of at most w cells as hold them, parts
// separated by two spaces, never splitting a part unless it is wider than a
// line. The first line carries the route and the key, so the cursor stops on
// the record and enter opens it.
func (b *builder) packed(w int, st Style, to *nav.Route, key string, parts ...string) {
	if w < minWrap {
		w = minWrap
	}
	w -= 2 // the lines after the first are indented by two
	var lines []string
	cur := ""
	for _, p := range parts {
		p = clean(p)
		if p == "" {
			continue
		}
		if ansi.StringWidth(p) > w { // a part wider than a line wraps at its spaces
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			wl := wrapText(p, w)
			lines = append(lines, wl[:len(wl)-1]...)
			p = wl[len(wl)-1]
		}
		switch {
		case cur == "":
			cur = p
		case ansi.StringWidth(cur)+2+ansi.StringWidth(p) <= w:
			cur += "  " + p
		default:
			lines = append(lines, cur)
			cur = p
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	for i, l := range lines {
		row := Row{Text: l, Style: st}
		if i == 0 {
			row.Go, row.Key = to, key
		} else if i > 0 {
			row.Text = "  " + l
		}
		b.add(row)
	}
}

// say adds a line of prose, wrapped to the width.
func (b *builder) say(w int, st Style, text string) { b.wrapped(w, st, "", "", text) }

// kv adds "label: value", the value wrapped under itself.
func (b *builder) kv(w int, label, value string) {
	if strings.TrimSpace(value) == "" {
		value = "-"
	}
	lead := label + ": "
	b.wrapped(w, Plain, lead, strings.Repeat(" ", ansi.StringWidth(lead)), value)
}

func (b *builder) list(w int, title string, items []string) {
	if len(items) == 0 {
		return
	}
	b.blank()
	b.say(w, Heading, title)
	for _, it := range items {
		b.wrapped(w, Plain, "  - ", "    ", it)
	}
}

// ---- environment and state words -------------------------------------------

// envWords says which kind of account it is, in words, never in colour alone.
func envWords(env string) (string, Style) {
	switch env {
	case "real":
		return "REAL MONEY", Strong
	case "paper_local":
		return "PAPER (local engine): not real money", Warn
	case "paper_broker":
		return "PAPER (broker paper account): not real money", Warn
	}
	return strings.ToUpper(env) + " (unknown kind of account)", Warn
}

func (b *builder) banner(w int, env string) {
	words, st := envWords(env)
	b.say(w, st, " "+words+" ")
}

// stateStyle colours a state by what it needs of the person, not by whether it
// is a purchase or a sale: waiting for a decision stands out, settled is dim,
// failures and unknowns are called out in words as well.
func stateStyle(s string) Style {
	switch s {
	case "draft", "proposed", "confirmed", "stale", "unknown", "submitting", "cancel_requested":
		return Warn
	case "accepted", "filled", "open", "partially_filled":
		return Plain
	case "rejected", "expired", "superseded", "cancelled":
		return Dim
	}
	return Plain
}

func stateWords(s string, unknown bool) string {
	if unknown {
		return "OUTCOME UNKNOWN: compare with the other side"
	}
	return s
}

// ---- filters ----------------------------------------------------------------

type term struct{ key, val string }

// parseFilter reads "key:value" words and plain words; all must match.
func parseFilter(q string) []term {
	var ts []term
	for _, w := range strings.Fields(strings.ToLower(q)) {
		if k, v, ok := strings.Cut(w, ":"); ok && k != "" && v != "" {
			ts = append(ts, term{k, v})
			continue
		}
		ts = append(ts, term{"", w})
	}
	return ts
}

func matches(ts []term, fields map[string]string) bool {
	var hay []string
	for _, v := range fields {
		hay = append(hay, strings.ToLower(v))
	}
	all := strings.Join(hay, "\n")
	for _, t := range ts {
		if t.key == "" {
			if !strings.Contains(all, t.val) {
				return false
			}
			continue
		}
		v, ok := fields[t.key]
		if !ok || !strings.Contains(strings.ToLower(v), t.val) {
			return false
		}
	}
	return true
}

func filterNote(b *builder, w int, q string, shown, total int, keys string) {
	if q == "" {
		b.say(w, Dim, "filter with /  ("+keys+" or plain words)")
		return
	}
	b.say(w, Info, fmt.Sprintf("filter %q: %d of %d shown; / changes it, an empty filter shows all", q, shown, total))
}

func unavailable(title string) Screen {
	return Screen{Title: title, Notice: "this backend does not read plans, books, income or orders (it is not the workspace over the stores)"}
}

func routeTo(k nav.Kind, arg string) *nav.Route { return &nav.Route{Kind: k, Arg: arg} }

// ---- the screens --------------------------------------------------------------

func buildWorkflow(r nav.Route, d Data) Screen {
	scr := buildWorkflowRows(r, d)
	scr.Rows = reachable(scr.Rows, nav.IsWorkflowList(r.Kind))
	return scr
}

// reachable lets the cursor stop on every line, so that a long screen can be
// read to its end with the arrows and the page keys: a viewport scrolls only to
// follow its cursor, and a card with no row to stop on could not be scrolled at
// all. In a list, the lines under a record belong to it: enter on any of them
// opens it.
func reachable(rows []Row, list bool) []Row {
	out := make([]Row, len(rows))
	copy(out, rows)
	var owner *Row
	n := 0
	for i := range out {
		r := &out[i]
		if r.Selectable() {
			owner, n = r, 0
			continue
		}
		if strings.TrimSpace(r.Text) == "" {
			owner = nil
			continue
		}
		if list && owner != nil && strings.HasPrefix(r.Text, " ") && (owner.Go != nil) {
			n++
			r.Go, r.Key = owner.Go, fmt.Sprintf("%s#%d", owner.Key, n)
			continue
		}
		owner = nil
		r.Key = fmt.Sprintf("line:%d", i)
	}
	return out
}

func buildWorkflowRows(r nav.Route, d Data) Screen {
	wf := d.WF
	if wf == nil || wf.Unavailable {
		return unavailable(r.Label())
	}
	switch r.Kind {
	case nav.Plans:
		return buildPlans(r.Arg, d, wf)
	case nav.Plan:
		return buildPlan(d, wf)
	case nav.Portfolio:
		return buildPortfolio(r.Arg, d, wf)
	case nav.Book:
		return buildBook(r.Arg, d, wf)
	case nav.Income:
		return buildIncome(r.Arg, d, wf)
	case nav.Orders:
		return buildOrders(r.Arg, d, wf)
	case nav.Order:
		return buildOrder(r.Arg, d, wf)
	case nav.Policy:
		return buildPolicy(d, wf)
	}
	return Screen{Title: r.Label(), Notice: "no such screen"}
}

func width(d Data) int {
	if d.Width <= 0 {
		return 100
	}
	return d.Width
}

func buildPlans(q string, d Data, wf *WorkflowData) Screen {
	w := width(d)
	var b builder
	b.say(w, Dim, "A plan is a record of a proposal, not an order. Nothing here is placed.")
	p := wf.Policy
	if p.Active {
		b.packed(w, Plain, routeTo(nav.Policy, ""), "policy", "Policy", fmt.Sprintf("epoch %d", p.Epoch), fmt.Sprintf("tier %d", p.Tier), fmt.Sprintf("%d setting(s) changed at the last activation", len(p.Changes)), "enter: history")
	} else {
		b.packed(w, Warn, routeTo(nav.Policy, ""), "policy", "NO ACTIVE POLICY", "tier 0: paper only", "enter: status")
	}
	ts := parseFilter(q)
	type grp struct {
		title string
		rows  []report.PlanRow
	}
	groups := []*grp{{title: "Due now: reviews due or expiring today"}, {title: "Buy candidates"}, {title: "Watch, wait and blocked"}, {title: "Settled: accepted, rejected, expired or superseded"}}
	shown := 0
	for _, pr := range wf.Plans {
		if !matches(ts, map[string]string{"book": pr.Book, "account": pr.Book, "sleeve": pr.Sleeve, "action": pr.Action, "currency": pr.Currency,
			"data": pr.Data, "strategy": pr.Strategy, "execution": pr.Execution, "state": pr.State, "security": pr.Security, "name": pr.Name, "plan": pr.Revision}) {
			continue
		}
		shown++
		open := pr.State == "draft" || pr.State == "proposed" || pr.State == "accepted"
		g := 3
		switch {
		case !open:
			g = 3
		case (pr.ReviewBy != "" && pr.ReviewBy <= wf.Today) || pr.Expiry == wf.Today:
			g = 0
		case pr.Action == "BUY" || pr.Action == "ADD":
			g = 1
		default:
			g = 2
		}
		if !open {
			g = 3
		}
		groups[g].rows = append(groups[g].rows, pr)
	}
	filterNote(&b, w, q, shown, len(wf.Plans), "account:, sleeve:, action:, currency:, data:, strategy:, execution:, state:")
	for _, g := range groups {
		if len(g.rows) == 0 {
			continue
		}
		b.blank()
		b.say(w, Heading, fmt.Sprintf("%s (%d)", g.title, len(g.rows)))
		for _, pr := range g.rows {
			rev := pr.Revision
			facts := []string{rev, pr.Action, pr.Security, pr.State}
			if pr.State != pr.Recorded {
				facts = append(facts, "recorded as "+pr.Recorded+": past its expiry day")
			}
			b.packed(w, stateStyle(pr.State), routeTo(nav.Plan, rev), "plan:"+rev, facts...)
			b.wrapped(w, Dim, "    ", "    ", fmt.Sprintf("%s · book %s · sleeve %s · quantity %s · expires %s · data %s, strategy %s, execution %s · %d unknown(s)", pr.Name, pr.Book, pr.Sleeve, orDash(pr.Qty), pr.Expiry, pr.Data, pr.Strategy, pr.Execution, pr.Unknowns))
		}
	}
	if len(wf.Plans) == 0 {
		return Screen{Title: "Plans", Rows: b.rows, Notice: "no plans yet: stoxicity plan generate BOOK -candidates FILE -fees FILE"}
	}
	return Screen{Title: "Plans", Rows: b.rows}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func buildPlan(d Data, wf *WorkflowData) Screen {
	doc := wf.Doc
	if doc == nil {
		return Screen{Title: "Plan", Notice: "no such plan"}
	}
	w := width(d)
	rv, c := doc.Revision, doc.Revision.Card
	var b builder
	b.packed(w, Strong, nil, "", rv.ID(), c.Action, c.Security, c.Name)
	st := doc.Effective
	if doc.Effective != doc.State {
		st += " (recorded as " + doc.State + ": past its expiry day)"
	}
	bookName := c.Book
	for _, bk := range wf.Books {
		if bk.ID == c.Book {
			bookName = bk.Ref()
		}
	}
	b.packed(w, stateStyle(doc.Effective), nil, "", "state "+st, "book "+bookName, "sleeve "+c.Sleeve, "strategy "+c.Strategy)
	b.packed(w, Plain, nil, "", "as of "+c.AsOf, "expires "+c.Expiry, "generated "+rv.Generated.UTC().Format("2006-01-02T15:04:05Z"))
	b.packed(w, Plain, nil, "", "data "+c.DataState, "strategy "+c.StrategyState, "execution "+c.ExecutionEligibility)
	b.packed(w, Plain, nil, "", fmt.Sprintf("score %d of %d (a policy ordering, not a probability)", c.Score, c.MaxScore), "coverage "+orDash(c.Coverage))
	if !doc.Intact {
		b.say(w, Bad, "THE REVISION DOES NOT MATCH ITS MANIFEST HASH: do not rely on it")
	}
	if rv.Rev > 1 || len(wf.Revs) > 1 {
		b.blank()
		b.say(w, Heading, "Versions (enter opens one)")
		for _, n := range wf.Revs {
			id := fmt.Sprintf("%s.r%d", rv.PlanID, n)
			mark := ""
			if n == rv.Rev {
				mark = "(this one)"
			}
			b.packed(w, Plain, routeTo(nav.Plan, id), "ver:"+id, id, mark)
		}
		if len(rv.Changes) > 0 {
			b.say(w, Heading, "Changed since the previous version")
			for _, ch := range rv.Changes {
				material := ""
				if ch.Material {
					material = " (material)"
				}
				b.wrapped(w, Plain, "  - ", "    ", fmt.Sprintf("%s: %s -> %s%s", ch.Field, ch.From, ch.To, material))
			}
		}
	}
	if s := c.Sizing; s != nil {
		b.blank()
		b.say(w, Heading, "Quantity and cost")
		b.packed(w, Plain, nil, "", "quantity "+s.Qty, "reference "+orDash(s.Reference)+" "+s.PriceCurrency, "entry at most "+orDash(s.EntryMax))
		b.packed(w, Plain, nil, "", "notional "+minorStr(s.Notional, s.Base), "fee "+minorStr(s.Fee, s.Base), "burden "+orDash(s.Burden))
		b.packed(w, Plain, nil, "", "cash "+minorStr(s.CashBefore, s.Base)+" -> "+minorStr(s.CashAfter, s.Base), "reserve "+s.Reserve)
		if s.Binding != "" {
			b.kv(w, "sized by", s.Binding)
		}
	}
	if v := c.Scenarios; v != nil {
		b.blank()
		b.say(w, Heading, "Scenarios per share in "+v.Currency+" (assumptions, not targets)")
		b.packed(w, Plain, nil, "", "bear "+v.Bear, "base "+v.Base, "bull "+v.Bull, "entry ceiling "+v.Ceiling+" ("+v.Entry+" less "+v.Margin+" margin of safety)")
	}
	if len(c.Concentration) > 0 {
		b.blank()
		b.say(w, Heading, "Concentration, before -> after")
		for _, x := range c.Concentration {
			b.wrapped(w, Plain, "  - ", "    ", fmt.Sprintf("%s %s: %s -> %s", x.Dimension, x.Name, x.Before, x.After))
		}
	}
	b.list(w, "Reasons", c.Reasons)
	b.list(w, "Unknown (these are not filled in)", c.Unknowns)
	b.list(w, "Thesis", c.Thesis)
	b.list(w, "Counter-evidence", c.Counter)
	b.list(w, "Invalidation: what would end this", c.Invalidation)
	b.list(w, "Exit rules", c.ExitRules)
	b.list(w, "Alternatives", c.Alternatives)
	b.list(w, "What would change this", c.WouldChange)
	if len(c.Components) > 0 {
		b.blank()
		b.say(w, Heading, "Tests, one by one")
		for _, x := range c.Components {
			req := ""
			if x.Required {
				req = " (required)"
			}
			b.wrapped(w, Plain, "  - ", "    ", fmt.Sprintf("%s/%s: %s, %d of %d%s %s", x.Group, x.ID, x.Result, x.Awarded, x.Max, req, x.Detail))
		}
	}
	if len(c.Costs) > 0 {
		keys := make([]string, 0, len(c.Costs))
		for k := range c.Costs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var cs []string
		for _, k := range keys {
			cs = append(cs, k+": "+c.Costs[k])
		}
		b.list(w, "Costs and assumptions", cs)
	}
	b.blank()
	b.say(w, Heading, "Evidence and the exact inputs of this revision")
	m := rv.Manifest
	b.packed(w, Plain, nil, "", "manifest "+rv.ManifestHash, "as of "+m.AsOf)
	b.packed(w, Plain, nil, "", fmt.Sprintf("policy epoch %d", m.PolicyEpoch), "policy "+m.Policy)
	b.packed(w, Plain, nil, "", "strategy "+m.Strategy, "config "+m.Config)
	b.packed(w, Plain, nil, "", "scorecard "+orDash(m.Scorecard), "assumptions "+orDash(m.Assumptions), "price "+orDash(m.Price))
	b.packed(w, Plain, nil, "", fmt.Sprintf("payouts %d", m.Payouts), "fees "+orDash(m.Fees), "rates "+orDash(m.Rates), "decisions "+orDash(m.Decisions), "ledger "+m.Ledger)
	b.say(w, Dim, "These are hashes of stored records; the plan names its evidence by identifier and holds no article text.")
	b.say(w, Dim, "What-if: change a limit or a fee and run `plan generate` again; this screen is offline and does not recompute.")
	if len(doc.Log) > 0 {
		b.blank()
		b.say(w, Heading, "History of this revision")
		for _, e := range doc.Log {
			b.wrapped(w, Plain, "  - ", "    ", fmt.Sprintf("%s: %s to %s by %s %s", e.At.UTC().Format("2006-01-02 15:04"), e.From, e.To, e.By, e.Note))
		}
	}
	var linked []report.OrderRow
	for _, o := range wf.Orders {
		if o.Plan == rv.ID() {
			linked = append(linked, o)
		}
	}
	if len(linked) > 0 {
		b.blank()
		b.say(w, Heading, "Orders drafted from this revision (enter opens one)")
		for _, o := range linked {
			words, _ := envWords(o.Env)
			b.packed(w, stateStyle(o.State), routeTo(nav.Order, o.ID), "order:"+o.ID, o.ID, stateWords(o.State, o.Unknown), words)
		}
	}
	return Screen{Title: "Plan " + rv.ID(), Rows: b.rows}
}

func buildPortfolio(q string, d Data, wf *WorkflowData) Screen {
	w := width(d)
	var b builder
	b.say(w, Dim, "Books of this tenant. A paper book is never mixed with a real one.")
	ts := parseFilter(q)
	shown := 0
	var rows []report.BookRef
	for _, bk := range wf.Books {
		if matches(ts, map[string]string{"account": bk.Account, "name": bk.Name, "env": bk.Env, "currency": bk.Base, "base": bk.Base, "status": bk.Status}) {
			rows = append(rows, bk)
			shown++
		}
	}
	filterNote(&b, w, q, shown, len(wf.Books), "account:, env:, currency:, status:")
	for _, bk := range rows {
		words, st := envWords(bk.Env)
		b.packed(w, st, routeTo(nav.Book, bk.Ref()), "book:"+bk.Ref(), bk.Ref(), words, "base "+bk.Base, bk.Status)
	}
	b.blank()
	b.packed(w, Plain, routeTo(nav.Orders, ""), "orders", "Orders", "enter: every order, with its state and confirmation")
	b.packed(w, Plain, routeTo(nav.Income, ""), "income", "Income", "enter: declared payouts, entitlements and what was received")
	if len(wf.Books) == 0 {
		return Screen{Title: "Portfolio", Rows: b.rows, Notice: "no books yet: stoxicity portfolio book create"}
	}
	return Screen{Title: "Portfolio", Rows: b.rows}
}

func buildBook(ref string, d Data, wf *WorkflowData) Screen {
	r := wf.Book
	if r == nil {
		return Screen{Title: "Book " + ref, Notice: "no such book"}
	}
	w := width(d)
	var b builder
	b.banner(w, r.Env)
	b.packed(w, Strong, nil, "", r.Book, "base "+r.Base, "as of "+r.AsOf, "ledger "+r.Ledger)
	b.say(w, Heading, "Cash")
	if len(r.Cash) == 0 {
		b.say(w, Dim, "  no cash")
	}
	for _, c := range r.Cash {
		b.packed(w, Plain, nil, "", c.Currency, "settled "+c.Settled, "unsettled "+c.Unsettled, "held for open orders "+c.Reserved, "available "+c.Available)
	}
	b.blank()
	b.say(w, Heading, "Holdings")
	if len(r.Holdings) == 0 {
		b.say(w, Dim, "  no positions")
	}
	for _, h := range r.Holdings {
		b.packed(w, Plain, nil, "", h.Security, h.Qty+" shares", "cost "+orDash(h.Cost), fmt.Sprintf("%d lot(s)", h.Lots))
	}
	if len(r.Reservations) > 0 {
		b.blank()
		b.say(w, Heading, "Held for open orders")
		for _, x := range r.Reservations {
			b.packed(w, Plain, nil, "", x.ID, x.Kind, x.What, x.Intent)
		}
	}
	b.blank()
	b.say(w, Heading, "Orders (enter opens one)")
	if len(r.Orders) == 0 {
		b.say(w, Dim, "  none")
	}
	for _, o := range r.Orders {
		b.packed(w, stateStyle(o.State), routeTo(nav.Order, o.ID), "order:"+o.ID, o.ID, stateWords(o.State, o.Unknown), o.Side, o.Security, o.Qty+" @ "+o.Limit+" "+o.Currency)
	}
	b.blank()
	b.say(w, Heading, "Plans for this book (enter opens one)")
	if len(r.Plans) == 0 {
		b.say(w, Dim, "  none")
	}
	for _, p := range r.Plans {
		b.packed(w, stateStyle(p.State), routeTo(nav.Plan, p.Revision), "plan:"+p.Revision, p.Revision, p.Action, p.Security, p.State, "expires "+p.Expiry)
	}
	b.blank()
	b.say(w, Heading, "Income")
	for _, p := range r.Income.Payouts {
		b.packed(w, Plain, nil, "", p.Key, p.State, "gross "+p.Gross)
	}
	if len(r.Income.Payouts) == 0 {
		b.say(w, Dim, "  no declared or estimated payout is known for the securities of this book")
	}
	if len(r.Income.Findings) == 0 {
		b.say(w, Dim, "  the income booked agrees with the declared payouts")
	}
	for _, f := range r.Income.Findings {
		b.wrapped(w, Bad, "  ! ", "    ", f)
	}
	b.blank()
	b.say(w, Heading, "Not shown on this screen")
	b.wrapped(w, Dim, "  - ", "    ", "Value, allocation, risk and performance: they need prices and exchange rates, which a screen never fetches. Use `performance BOOK -marks FILE -rates FILE`.")
	b.wrapped(w, Dim, "  - ", "    ", "Reconciliation exceptions: `broker differences` and `portfolio reconcile` list them.")
	b.kv(w, "policy", fmt.Sprintf("%s, tier %d", r.Policy, r.Tier))
	return Screen{Title: "Book " + r.Book, Rows: b.rows}
}

func buildIncome(q string, d Data, wf *WorkflowData) Screen {
	v := wf.Income
	if v == nil {
		return Screen{Title: "Income", Notice: "no income data"}
	}
	w := width(d)
	var b builder
	b.say(w, Dim, "Gross, before any tax withheld at source. An estimate is never booked.")
	ts := parseFilter(q)
	order := []string{"entitled", "pending", "not_entitled", "no_income"}
	rank := func(s string) int {
		for i, o := range order {
			if o == s {
				return i
			}
		}
		return len(order)
	}
	var rows []report.IncomeRow
	for _, r := range v.Rows {
		if matches(ts, map[string]string{"book": r.Book, "status": r.Status, "security": r.Security, "payout": r.Payout}) {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rank(rows[i].Status) < rank(rows[j].Status) })
	filterNote(&b, w, q, len(rows), len(v.Rows), "book:, status:, security:")
	if len(v.Findings) > 0 {
		b.blank()
		b.say(w, Heading, "Income check: differences between what is booked and what is owed")
		for _, f := range v.Findings {
			b.wrapped(w, Bad, "  ! ", "    ", f)
		}
	}
	last := ""
	for _, r := range rows {
		if r.Status != last {
			last = r.Status
			b.blank()
			b.say(w, Heading, "Status: "+r.Status)
		}
		st := Plain
		switch r.Status {
		case "entitled":
			st = Good
		case "pending":
			st = Warn
		case "not_entitled", "no_income":
			st = Dim
		}
		b.packed(w, st, nil, "key:"+r.Book+"|"+r.Payout, r.Payout, r.Book, "ex "+orDash(r.ExDay), "pay "+orDash(r.PayDay), r.Shares+" shares x "+r.PerShare, "gross "+r.Gross, "booked "+r.Booked)
		if r.Reason != "" {
			b.wrapped(w, Dim, "    ", "    ", r.Reason)
		}
	}
	if len(v.Rows) == 0 {
		return Screen{Title: "Income", Rows: b.rows, Notice: "no declared or estimated payout is known for the securities of any book: stoxicity dividends import"}
	}
	return Screen{Title: "Income as of " + v.Day, Rows: b.rows}
}

func buildOrders(q string, d Data, wf *WorkflowData) Screen {
	w := width(d)
	var b builder
	b.say(w, Dim, "Orders stay local until a broker is connected and you confirm. Confirming here sends nothing.")
	ts := parseFilter(q)
	shown := 0
	var rows []report.OrderRow
	for _, o := range wf.Orders {
		if matches(ts, map[string]string{"book": o.Book, "state": o.State, "env": o.Env, "side": o.Side, "security": o.Security, "id": o.ID, "plan": o.Plan}) {
			rows = append(rows, o)
			shown++
		}
	}
	filterNote(&b, w, q, shown, len(wf.Orders), "state:, env:, side:, security:, book:")
	for _, o := range rows {
		words, _ := envWords(o.Env)
		st := stateStyle(o.State)
		if o.Unknown {
			st = Bad
		}
		b.packed(w, st, routeTo(nav.Order, o.ID), "order:"+o.ID, o.ID, stateWords(o.State, o.Unknown), words)
		b.wrapped(w, Dim, "    ", "    ", fmt.Sprintf("%s %s %s @ %s %s, filled %s", o.Side, o.Qty, o.Security, o.Limit, o.Currency, o.Filled))
	}
	if len(wf.Orders) == 0 {
		return Screen{Title: "Orders", Rows: b.rows, Notice: "no orders: stoxicity order draft"}
	}
	return Screen{Title: "Orders", Rows: b.rows}
}

func buildOrder(id string, d Data, wf *WorkflowData) Screen {
	var o *report.OrderRow
	for i := range wf.Orders {
		if wf.Orders[i].ID == id {
			o = &wf.Orders[i]
		}
	}
	if o == nil {
		return Screen{Title: "Order " + id, Notice: "no such order"}
	}
	w := width(d)
	var b builder
	b.banner(w, o.Env)
	st := stateStyle(o.State)
	if o.Unknown {
		st = Bad
	}
	b.packed(w, st, nil, "", o.ID, "state "+stateWords(o.State, o.Unknown))
	b.blank()
	b.say(w, Heading, "The exact terms (a confirmation covers these and nothing else)")
	b.packed(w, Plain, nil, "", o.Side, o.Qty, o.Security, "limit price "+o.Limit+" "+o.Currency, "order type "+o.Type, "good for "+o.TIF)
	b.packed(w, Plain, nil, "", "book "+orBook(*o), "fee estimate "+o.FeeEstimate, "filled "+o.Filled, "remaining "+o.Remaining)
	b.kv(w, "terms hash", o.TermsHash)
	if o.Plan != "" {
		b.packed(w, Plain, routeTo(nav.Plan, o.Plan), "plan:"+o.Plan, "from plan "+o.Plan, "enter: open it")
	}
	if o.Reservation != "" {
		b.kv(w, "hold", o.Reservation)
	}
	if o.BrokerRef != "" {
		b.kv(w, "other side's reference", o.BrokerRef)
	}
	b.packed(w, Plain, nil, "", "created "+o.Created, "lapses "+orDash(o.Expires))
	if o.ConfirmedBy != "" {
		b.packed(w, Plain, nil, "", "confirmed by "+o.ConfirmedBy, "valid until "+o.ConfirmedAt)
	}
	b.list(w, "Warnings (confirming acknowledges these)", o.Warnings)
	b.list(w, "Why (rejection, unknown outcome or anomaly)", o.Why)
	b.list(w, "Facts that arrived out of order", o.Late)
	b.blank()
	b.say(w, Heading, "Confirm")
	if o.Confirmable {
		b.add(Row{Text: "▶ Confirm this order: you will be asked to type its ID", Style: Strong, Key: "confirm:" + o.ID})
		from := len(b.rows)
		b.say(w, Dim, "A confirmation records your go-ahead for exactly these terms. Nothing is sent. Cancel, replace and submit are command-line acts.")
		for i := from; i < len(b.rows); i++ { // the lines under the row belong to it
			b.rows[i].Key = fmt.Sprintf("confirm:%s#%d", o.ID, i-from+1)
		}
	} else {
		b.wrapped(w, Warn, "Not confirmable here: ", "  ", o.NotBecause)
	}
	return Screen{Title: "Order " + o.ID, Rows: b.rows}
}

func buildPolicy(d Data, wf *WorkflowData) Screen {
	p := wf.Policy
	w := width(d)
	var b builder
	if !p.Active {
		b.say(w, Warn, "NO ACTIVE POLICY: tier 0, paper only. Nothing can be sent to a broker.")
		b.say(w, Dim, "stoxicity policy example, then policy preview -file FILE and policy activate show the change before it applies.")
		return Screen{Title: "Policy", Rows: b.rows}
	}
	b.packed(w, Strong, nil, "", fmt.Sprintf("epoch %d", p.Epoch), fmt.Sprintf("tier %d", p.Tier), "active since "+p.At)
	b.kv(w, "hash", p.Hash)
	if p.RiskAccepted {
		b.say(w, Warn, "A higher risk was accepted at this activation.")
	}
	b.blank()
	b.say(w, Heading, "What the last activation changed: before -> after")
	if len(p.Changes) == 0 {
		b.say(w, Dim, "  nothing: this was the first activation or nothing differed")
	}
	for _, c := range p.Changes {
		b.wrapped(w, Plain, "  - ", "    ", fmt.Sprintf("%s: %s -> %s", c.Path, orDash(c.Old), orDash(c.New)))
	}
	b.list(w, "What that did to existing plans and orders", p.Effects)
	b.list(w, "Risk notes", p.RiskNotes)
	b.blank()
	b.wrapped(w, Dim, "", "", "To see the effect of a new policy before it applies: `policy preview -file FILE`. Resetting to the example profile never activates a higher risk by itself; `policy activate` is the only way.")
	return Screen{Title: "Policy", Rows: b.rows}
}

// orBook is the book as it is written on the command line, or its ID when the
// name is not known.
func orBook(o report.OrderRow) string {
	if o.BookName != "" {
		return o.BookName
	}
	return o.Book
}

// minorStr writes an amount held in minor units with its currency.
func minorStr(minor int64, cur string) string {
	a, err := money.FromMinor(minor, money.Currency(cur))
	if err != nil {
		return fmt.Sprintf("%d minor units of %s", minor, cur)
	}
	return a.String()
}
