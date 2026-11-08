package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	recBrokerLimits  = "broker.limits"
	recBrokerPreview = "broker.preview"
	payloadVersion   = "ibkr-order-v1"
	quoteWarning     = "no executing quote has been validated: the price collar and quote freshness are not checked here"
	brokerPrefix     = "broker: "
)

// brokerLimits are the checks made before an order may go to a broker. The
// values are starting points, not findings about any market: they stay
// unreviewed until a person has read them, and nothing is sent while they are.
type brokerLimits struct {
	QuoteMaxAge  string    `json:"quote_max_age"`  // a duration: how old a quote may be
	CollarPct    string    `json:"collar_percent"` // how far past the market a limit may reach
	MaxSpreadPct string    `json:"max_spread_percent"`
	PreviewValid string    `json:"preview_valid"` // a duration: how long a confirmation made against a preview lasts
	Reviewed     bool      `json:"reviewed"`
	By           string    `json:"by"`
	At           time.Time `json:"at"`
}

func defaultBrokerLimits() brokerLimits {
	return brokerLimits{QuoteMaxAge: "60s", CollarPct: "2", MaxSpreadPct: "1", PreviewValid: "5m"}
}

func (l brokerLimits) parse() (age, valid time.Duration, collar, spread money.Decimal, err error) {
	if age, err = time.ParseDuration(l.QuoteMaxAge); err != nil || age <= 0 {
		return 0, 0, collar, spread, fmt.Errorf("quote_max_age %q is not a positive duration", l.QuoteMaxAge)
	}
	if valid, err = time.ParseDuration(l.PreviewValid); err != nil || valid <= 0 {
		return 0, 0, collar, spread, fmt.Errorf("preview_valid %q is not a positive duration", l.PreviewValid)
	}
	if collar, err = money.ParseDecimal(l.CollarPct); err != nil || collar.Sign() <= 0 || collar.Cmp(money.Int(50)) > 0 {
		return 0, 0, collar, spread, fmt.Errorf("collar_percent %q must be above 0 and at most 50", l.CollarPct)
	}
	if spread, err = money.ParseDecimal(l.MaxSpreadPct); err != nil || spread.Sign() <= 0 || spread.Cmp(money.Int(50)) > 0 {
		return 0, 0, collar, spread, fmt.Errorf("max_spread_percent %q must be above 0 and at most 50", l.MaxSpreadPct)
	}
	return age, valid, collar, spread, nil
}

func loadBrokerLimits(ctx context.Context, a *app) (brokerLimits, bool, error) {
	rs, err := a.records()
	if err != nil {
		return brokerLimits{}, false, err
	}
	rec, err := rs.Latest(ctx, recBrokerLimits, "default")
	if errors.Is(err, store.ErrNoRecord) {
		return defaultBrokerLimits(), false, nil
	}
	if err != nil {
		return brokerLimits{}, false, err
	}
	var l brokerLimits
	if err := json.Unmarshal(rec.Body, &l); err != nil {
		return l, false, fmt.Errorf("the broker limits do not decode: %w", err)
	}
	return l, true, nil
}

func brokerLimitsCmd(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: broker limits show|set|review")
	}
	save := func(l brokerLimits) error {
		if _, _, _, _, err := l.parse(); err != nil {
			return usageErrorf("%v", err)
		}
		l.At = a.now().UTC()
		rs, err := a.records()
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(l)
		_, _, err = rs.Append(ctx, store.RecordInput{Kind: recBrokerLimits, Key: "default", Body: raw})
		return err
	}
	switch args[0] {
	case "show":
		fs := newFlags("broker limits show")
		asJSON := fs.Bool("json", false, "print JSON")
		if _, err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		l, stored, err := loadBrokerLimits(ctx, a)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(a, l)
		}
		state := "UNREVIEWED"
		switch {
		case l.Reviewed:
			state = "reviewed by " + l.By
		case !stored:
			state = "UNREVIEWED (defaults, never set)"
		}
		color := useColor(a.stdout)
		fmt.Fprintf(a.stdout, "quote may be at most %s old; a limit may reach at most %s%% past the market; the spread may be at most %s%%; a confirmation made against a preview lasts at most %s.\n", l.QuoteMaxAge, l.CollarPct, l.MaxSpreadPct, l.PreviewValid)
		if l.Reviewed {
			fmt.Fprintln(a.stdout, "status: "+styledText(state, cliGreen, color))
		} else {
			fmt.Fprintln(a.stdout, "status: "+styledText(state, cliYellow, color)+". These are illustrative starting values, not findings about any market. Nothing is sent to a broker until you have read them and run broker limits review.")
		}
		return nil
	case "set":
		fs := newFlags("broker limits set")
		l, _, err := loadBrokerLimits(ctx, a)
		if err != nil {
			return err
		}
		fs.StringVar(&l.QuoteMaxAge, "quote-age", l.QuoteMaxAge, "oldest quote that may be used, e.g. 60s")
		fs.StringVar(&l.CollarPct, "collar", l.CollarPct, "percent a limit may reach past the market")
		fs.StringVar(&l.MaxSpreadPct, "spread", l.MaxSpreadPct, "widest bid-ask spread, in percent of the midpoint")
		fs.StringVar(&l.PreviewValid, "preview-valid", l.PreviewValid, "how long a confirmation made against a preview lasts")
		by := fs.String("by", "", "who is setting them")
		if _, err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if *by == "" {
			return usageErrorf("usage: broker limits set [-quote-age 60s] [-collar 2] [-spread 1] [-preview-valid 5m] -by NAME")
		}
		l.Reviewed, l.By = false, *by
		if err := save(l); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, "limits saved and marked unreviewed: read them with broker limits show, then broker limits review -by NAME.")
		return nil
	case "review":
		fs := newFlags("broker limits review")
		by := fs.String("by", "", "who reviewed them")
		if _, err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if *by == "" {
			return usageErrorf("usage: broker limits review -by NAME")
		}
		l, _, err := loadBrokerLimits(ctx, a)
		if err != nil {
			return err
		}
		l.Reviewed, l.By = true, *by
		if err := save(l); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "limits reviewed by %s: %s old quotes, %s%% collar, %s%% spread, %s preview.\n", *by, l.QuoteMaxAge, l.CollarPct, l.MaxSpreadPct, l.PreviewValid)
		return nil
	}
	return usageErrorf("unknown subcommand %q (show, set, review)", args[0])
}

type checkRow struct {
	Name   string `json:"check"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// preflight is everything known about sending one order right now.
type preflight struct {
	Order    string              `json:"order"`
	Checks   []checkRow          `json:"checks"`
	Request  broker.OrderRequest `json:"request"`
	Quote    *broker.Quote       `json:"quote,omitempty"`
	Preview  *broker.Preview     `json:"preview,omitempty"`
	Payload  string              `json:"payload"`
	Warnings []string            `json:"warnings"` // the broker's, as acknowledged
	Valid    time.Duration       `json:"-"`
	At       time.Time           `json:"at"`
}

func (p *preflight) add(name string, ok bool, format string, args ...any) {
	p.Checks = append(p.Checks, checkRow{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
}

func (p preflight) failures() []checkRow {
	var out []checkRow
	for _, c := range p.Checks {
		if !c.OK {
			out = append(out, c)
		}
	}
	return out
}

func (p preflight) failureError(id string) error {
	var parts []string
	for _, c := range p.failures() {
		parts = append(parts, c.Name+": "+c.Detail)
	}
	return fmt.Errorf("%s cannot go to the broker: %s", id, strings.Join(parts, "; "))
}

// conidOf finds the one broker contract of a security.
func conidOf(ctx context.Context, a *app, security string) (int64, error) {
	m, err := conidMap(ctx, a)
	if err != nil {
		return 0, err
	}
	var found []int64
	for c, s := range m {
		if s == security {
			found = append(found, c)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i] < found[j] })
	switch len(found) {
	case 0:
		return 0, fmt.Errorf("%s has no broker contract in the instrument register (contracts: [{broker: ibkr, conid: ...}]); broker contracts SYMBOL finds it", security)
	case 1:
		return found[0], nil
	}
	return 0, fmt.Errorf("%s has %d broker contracts in the register and the order does not say which listing: nothing is guessed", security, len(found))
}

// runPreflight reads the broker and checks one order against everything that
// can have changed since it was drafted. It changes nothing.
func runPreflight(ctx context.Context, a *app, br broker.Broker, cfg brokerConfig, b portfolio.Book, account string, v order.View, forSubmit bool) (preflight, error) {
	now := a.now().UTC()
	pf := preflight{Order: v.ID, At: now}
	t := v.Terms
	lim, _, err := loadBrokerLimits(ctx, a)
	if err != nil {
		return pf, err
	}
	age, valid, collar, spread, err := lim.parse()
	if err != nil {
		return pf, err
	}
	pf.Valid = valid
	if forSubmit {
		pf.add("mode", cfg.Mode == broker.Trading, "the connection is %s; broker mode trading -by NAME is the explicit choice to allow sending", cfg.Mode)
		pf.add("limits", lim.Reviewed, "the quote, collar and spread limits are %s", map[bool]string{true: "reviewed", false: "unreviewed: broker limits review"}[lim.Reviewed])
	}

	// the contract
	conid, err := conidOf(ctx, a, t.Security)
	if err != nil {
		pf.add("contract", false, "%v", err)
		return pf, nil
	}
	c, err := br.Contract(ctx, conid)
	switch {
	case err != nil:
		return pf, explainBrokerErr(err)
	case c.Currency != t.Currency:
		pf.add("contract", false, "the broker's contract %d trades in %s but the order is in %s", conid, c.Currency, t.Currency)
	default:
		pf.add("contract", true, "contract %d %s (%s, %s)", conid, c.Symbol, c.Exchange, c.Currency)
	}

	// the account
	accts, err := br.Accounts(ctx)
	if err != nil {
		return pf, explainBrokerErr(err)
	}
	var acct broker.Account
	for _, x := range accts {
		if x.ID == account {
			acct = x
		}
	}
	pf.add("account", acct.ID != "" && (b.Env == portfolio.PaperBroker) == acct.Paper, "account %s is %s and the book is %s", broker.Redact(account), map[bool]string{true: "paper", false: "live"}[acct.Paper], b.Env)

	// the policy and the plan
	db, err := a.store()
	if err != nil {
		return pf, err
	}
	rec, active, err := db.Policies(a.tenantID).AsOf(ctx, time.Time{})
	if err != nil {
		return pf, err
	}
	pf.add("policy", active && rec.Hash == t.PolicyHash, "the policy that governed the draft is %s", map[bool]string{true: "still the active one", false: "no longer the active one"}[active && rec.Hash == t.PolicyHash])
	if t.PlanID != "" {
		repo, err := planRepo(a)
		if err != nil {
			return pf, err
		}
		rv, err := repo.Get(ctx, t.PlanID, t.PlanRevision)
		if err != nil {
			return pf, err
		}
		st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
		if err != nil {
			return pf, err
		}
		eff := plan.Effective(st, rv.Card.Expiry, now)
		cur, err := currentManifest(ctx, a, rv)
		if err != nil {
			return pf, err
		}
		var changed []string
		for _, ch := range rv.Manifest.Changed(cur) {
			// the order's own hold changes the book; funds are checked against the broker below
			if ch != "book" {
				changed = append(changed, ch)
			}
		}
		pf.add("plan", eff == plan.Accepted && len(changed) == 0, "%s is %s%s", rv.ID(), eff, map[bool]string{true: "", false: "; changed since it was generated: " + strings.Join(changed, ", ")}[len(changed) == 0])
	}

	// the hold, and the funds or shares at the broker
	pf.add("hold", v.Reservation != "", "the order's hold is %s", map[bool]string{true: "in place", false: "gone"}[v.Reservation != ""])
	if t.Side == order.Buy {
		need, err := t.Commitment(v.Remaining, v.FeesPaid)
		if err != nil {
			return pf, err
		}
		bals, err := br.Balances(ctx, account)
		if err != nil {
			return pf, explainBrokerErr(err)
		}
		var settled int64
		for _, x := range bals {
			if x.Currency == t.Currency {
				settled = x.Settled
			}
		}
		pf.add("funds", settled >= need.Minor(), "settled cash at the broker %s, the order needs %s", minorText(settled, t.Currency), need)
	} else {
		pos, err := br.Positions(ctx, account)
		if err != nil {
			return pf, explainBrokerErr(err)
		}
		var held money.Decimal
		for _, x := range pos {
			if x.Conid == conid {
				if held, err = held.Add(x.Qty); err != nil {
					return pf, err
				}
			}
		}
		pf.add("shares", held.Cmp(v.Remaining) >= 0, "the broker holds %s, the order sells %s", held, v.Remaining)
	}

	// the quote: execution grade or no submission
	q, qerr := br.Quote(ctx, conid)
	switch {
	case qerr != nil && broker.KindOf(qerr) == broker.Entitlement:
		pf.add("quote", false, "no execution-grade quote is available: %v", qerr)
	case qerr != nil:
		return pf, explainBrokerErr(qerr)
	default:
		pf.Quote = &q
		pf.add("quote", true, "last %s, bid %s, ask %s", q.Last, q.Bid, q.Ask)
		pf.add("quote real time", !q.Delayed, "the quote is %s", map[bool]string{true: "delayed or frozen: not an executable price", false: "real time"}[q.Delayed])
		fresh := !q.At.IsZero() && now.Sub(q.At) <= age && !q.At.After(now.Add(time.Minute))
		pf.add("quote age", fresh, "the quote is %s old (at most %s)", map[bool]string{true: now.Sub(q.At).Round(time.Second).String(), false: "of unknown or unusable age"}[!q.At.IsZero()], age)
		if q.Bid.Sign() > 0 && q.Ask.Sign() > 0 && q.Ask.Cmp(q.Bid) >= 0 {
			diff, _ := q.Ask.Sub(q.Bid)
			sum, _ := q.Ask.Add(q.Bid)
			l, _ := diff.Mul(money.Int(200), 6, money.Up)
			r, _ := sum.Mul(spread, 6, money.Down)
			pf.add("spread", l.Cmp(r) <= 0, "bid %s ask %s (at most %s%% of the midpoint)", q.Bid, q.Ask, spread)
			hundred := money.Int(100)
			pct, _ := collar.Div(hundred, 6, money.Up)
			if t.Side == order.Buy {
				f, _ := money.Int(1).Add(pct)
				bound, _ := q.Ask.Mul(f, 6, money.Up)
				pf.add("collar", t.Limit.Cmp(bound) <= 0, "buy limit %s, ask %s (limit may be at most %s%% above it, %s)", t.Limit, q.Ask, collar, bound)
			} else {
				f, _ := money.Int(1).Sub(pct)
				bound, _ := q.Bid.Mul(f, 6, money.Down)
				pf.add("collar", t.Limit.Cmp(bound) >= 0, "sell limit %s, bid %s (limit may be at most %s%% below it, %s)", t.Limit, q.Bid, collar, bound)
			}
		} else {
			pf.add("spread", false, "bid %s and ask %s are not a usable two-sided market", q.Bid, q.Ask)
		}
	}

	// the broker's own preview
	req := broker.OrderRequest{Account: account, Conid: conid, Side: string(t.Side), Qty: t.Qty, Limit: t.Limit, TIF: t.TIF, Ref: v.ID}
	pf.Request = req
	prev, perr := br.Preview(ctx, req)
	switch {
	case perr != nil && broker.KindOf(perr) == broker.Refused:
		pf.add("preview", false, "%v", perr)
	case perr != nil:
		return pf, explainBrokerErr(perr)
	default:
		pf.Preview = &prev
		for _, w := range prev.Warnings {
			pf.Warnings = append(pf.Warnings, brokerPrefix+w)
		}
		pf.add("preview", true, "the broker previewed the order: commission %s, %d warning(s)", minorText(prev.Commission, nonEmptyCur(prev.Currency, t.Currency)), len(prev.Warnings))
	}

	pf.Payload = payloadHash(req, acct, c, pf.Warnings, prev)
	return pf, nil
}

func nonEmptyCur(a, b money.Currency) money.Currency {
	if a != "" {
		return a
	}
	return b
}

// payloadHash covers everything the broker is to be sent and everything it
// said about it that a person agreed to. The quote is not in it: it moves
// every second and is checked on its own at the moment of sending.
func payloadHash(req broker.OrderRequest, acct broker.Account, c broker.Contract, warnings []string, p broker.Preview) string {
	feats := make([]string, 0, len(acct.Features))
	for k, v := range acct.Features {
		feats = append(feats, k+"="+strconv.FormatBool(v))
	}
	sort.Strings(feats)
	ws := append([]string(nil), warnings...)
	sort.Strings(ws)
	raw, _ := json.Marshal(struct {
		Version    string
		Request    broker.OrderRequest
		Account    [4]string
		Features   []string
		Contract   broker.Contract
		Warnings   []string
		Commission int64
		Currency   money.Currency
	}{payloadVersion, req, [4]string{acct.ID, acct.Type, acct.Title, strconv.FormatBool(acct.Paper)}, feats, c, ws, p.Commission, p.Currency})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (p preflight) print(a *app) {
	color := useColor(a.stdout)
	t := cliTable{Header: []string{"check", "result", "detail"}}
	for _, c := range p.Checks {
		r := styled("ok", cliGreen)
		if !c.OK {
			r = styled("FAILED", cliRed)
		}
		t.Rows = append(t.Rows, []cliCell{styled(c.Name, cliStrong), r, plain(c.Detail)})
	}
	t.write(a.stdout, color)
	for _, w := range p.Warnings {
		fmt.Fprintln(a.stdout, styledText("warning", cliYellow, color)+" "+w)
	}
}

// brokerOrderContext returns the broker, config, book and account for an order,
// or nil when the order has no broker side.
func brokerOrderContext(ctx context.Context, a *app, v order.View) (broker.Broker, brokerConfig, portfolio.Book, string, bool, error) {
	if v.Terms.Env == portfolio.PaperLocal {
		return nil, brokerConfig{}, portfolio.Book{}, "", false, nil
	}
	if _, ok, err := loadBrokerConfig(ctx, a); err != nil || !ok {
		return nil, brokerConfig{}, portfolio.Book{}, "", false, err
	}
	binds, err := loadBinds(ctx, a)
	if err != nil {
		return nil, brokerConfig{}, portfolio.Book{}, "", false, err
	}
	if _, ok := binds[v.Terms.Book]; !ok {
		return nil, brokerConfig{}, portfolio.Book{}, "", false, nil
	}
	br, cfg, b, account, err := boundBroker(ctx, a, v.Terms.Book)
	return br, cfg, b, account, err == nil, err
}

func brokerPreview(ctx context.Context, a *app, args []string) error {
	fs := newFlags("broker preview")
	asJSON := fs.Bool("json", false, "print JSON")
	id, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if id == "" {
		return usageErrorf("usage: broker preview ORDER [-json]")
	}
	v, err := findOrder(ctx, a, id)
	if err != nil {
		return err
	}
	br, cfg, b, account, ok, err := brokerOrderContext(ctx, a, v)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s has no broker side: its book is paper_local, or no broker is configured and bound for it", id)
	}
	pf, err := runPreflight(ctx, a, br, cfg, b, account, v, false)
	if err != nil {
		return err
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(pf)
	if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recBrokerPreview, Key: id, Body: raw}); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, pf)
	}
	fmt.Fprintf(a.stdout, "preview of %s for account %s: %s %s %s at limit %s %s (%s)\n", id, broker.Redact(account), v.Terms.Side, v.Terms.Qty, v.Terms.Security, v.Terms.Limit, v.Terms.Currency, v.Terms.TIF)
	pf.print(a)
	if f := pf.failures(); len(f) > 0 {
		fmt.Fprintf(a.stdout, "%d check(s) failed: this order cannot be confirmed for the broker as it stands. Nothing was sent.\n", len(f))
		return nil
	}
	fmt.Fprintf(a.stdout, "All checks passed. order confirm %s -by NAME confirms exactly this payload (%s) for at most %s; any change at the broker or here invalidates it. Nothing was sent.\n", id, pf.Payload[:12], pf.Valid)
	return nil
}

// brokerSender sends the order the user confirmed. Questions the broker asks
// are answered only when they are warnings the user already acknowledged, word
// for word; any other question leaves the order unknown, never answered.
func brokerSender(br broker.Broker, req broker.OrderRequest, acked []string) order.Sender {
	allowed := map[string]bool{}
	for _, w := range acked {
		if strings.HasPrefix(w, brokerPrefix) {
			allowed[strings.TrimPrefix(w, brokerPrefix)] = true
		}
	}
	return func(ctx context.Context, s order.Submission) (order.SendResult, error) {
		res, err := br.Place(ctx, req)
		for round := 0; ; round++ {
			if err != nil {
				if broker.KindOf(err) == broker.Refused {
					return order.SendResult{Rejected: true, Reason: err.Error()}, nil
				}
				return order.SendResult{}, err
			}
			if res.OrderID != "" {
				return order.SendResult{Broker: res.OrderID}, nil
			}
			if len(res.Replies) == 0 || round >= 5 {
				return order.SendResult{}, errors.New("the broker's answer neither placed the order nor asked a question")
			}
			for _, rp := range res.Replies {
				for _, m := range rp.Messages {
					if !allowed[m] {
						return order.SendResult{}, fmt.Errorf("the broker asked %q, which was not acknowledged: the order may be waiting at the broker for an answer", m)
					}
				}
			}
			res, err = br.Confirm(ctx, res.Replies[0].ID)
		}
	}
}
