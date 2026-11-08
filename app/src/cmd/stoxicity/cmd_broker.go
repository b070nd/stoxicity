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
	"time"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/broker/fixture"
	"github.com/b070nd/stoxicity/app/src/broker/ibkr"
	"github.com/b070nd/stoxicity/app/src/broker/recon"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/tier"
)

const (
	recBrokerConfig = "broker.config"
	recBrokerBind   = "broker.bind"
	recBrokerDiff   = "broker.difference"
	recBrokerSync   = "broker.sync"
)

// brokerConfig says how the program reaches a broker and what it may do there.
// It holds no credential: the user logs in in the gateway's own page.
type brokerConfig struct {
	Transport string      `json:"transport"` // fixture | ibkr
	Fixture   string      `json:"fixture,omitempty"`
	URL       string      `json:"url,omitempty"`
	CAFile    string      `json:"ca_file,omitempty"`
	Mode      broker.Mode `json:"mode"`
	By        string      `json:"by"`
	At        time.Time   `json:"at"`
}

type brokerBind struct {
	Book    string    `json:"book"`
	Account string    `json:"account"`
	By      string    `json:"by"`
	At      time.Time `json:"at"`
}

type brokerDiffRec struct {
	State string `json:"state"` // open | cleared
	// Episode counts the records of this difference so far: a difference that
	// comes back is a new record even if everything else is the same.
	Episode int              `json:"episode"`
	Book    string           `json:"book"`
	Diff    recon.Difference `json:"difference"`
	At      time.Time        `json:"at"`
}

type brokerSyncRec struct {
	Book        string    `json:"book"`
	At          time.Time `json:"at"`
	Applied     int       `json:"applied"`
	Differences int       `json:"differences"`
}

// cmdBroker is the connection to a broker. It reads, compares and records; it
// never changes the ledger except by applying facts the broker states about
// orders this program sent, through the same reports as any other source.
func cmdBroker(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: config | mode | bind | status | sync | differences | contracts | fixture")
	}
	switch args[0] {
	case "config":
		return brokerConfigCmd(ctx, a, args[1:])
	case "mode":
		return brokerMode(ctx, a, args[1:])
	case "bind":
		return brokerBindCmd(ctx, a, args[1:])
	case "status":
		return brokerStatus(ctx, a, args[1:])
	case "sync":
		return brokerSync(ctx, a, args[1:])
	case "differences":
		return brokerDifferences(ctx, a, args[1:])
	case "contracts":
		return brokerContracts(ctx, a, args[1:])
	case "fixture":
		return brokerFixture(a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (config, mode, bind, status, sync, differences, contracts, fixture)", args[0])
}

func loadBrokerConfig(ctx context.Context, a *app) (brokerConfig, bool, error) {
	rs, err := a.records()
	if err != nil {
		return brokerConfig{}, false, err
	}
	rec, err := rs.Latest(ctx, recBrokerConfig, "default")
	if errors.Is(err, store.ErrNoRecord) {
		return brokerConfig{}, false, nil
	}
	if err != nil {
		return brokerConfig{}, false, err
	}
	var c brokerConfig
	if err := json.Unmarshal(rec.Body, &c); err != nil {
		return c, false, fmt.Errorf("the broker configuration does not decode: %w", err)
	}
	return c, true, nil
}

func saveBrokerConfig(ctx context.Context, a *app, c brokerConfig) error {
	rs, err := a.records()
	if err != nil {
		return err
	}
	c.At = a.now().UTC()
	raw, _ := json.Marshal(c)
	_, _, err = rs.Append(ctx, store.RecordInput{Kind: recBrokerConfig, Key: "default", Body: raw})
	return err
}

// openBroker returns the configured broker. The default is no broker at all.
func openBroker(ctx context.Context, a *app) (broker.Broker, brokerConfig, error) {
	c, ok, err := loadBrokerConfig(ctx, a)
	if err != nil {
		return nil, c, err
	}
	if !ok {
		return nil, c, errors.New("no broker is configured: run broker config set (the default is offline: nothing is read from a broker and nothing is sent)")
	}
	switch c.Transport {
	case "fixture":
		b, err := fixture.Open(c.Fixture)
		if err != nil {
			return nil, c, err
		}
		b.Now = a.now
		return b, c, nil
	case "ibkr":
		b, err := ibkr.New(ibkr.Options{BaseURL: c.URL, CAFile: c.CAFile})
		return b, c, err
	}
	return nil, c, fmt.Errorf("the configured transport %q is not known", c.Transport)
}

func brokerConfigCmd(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: broker config set|show")
	}
	switch args[0] {
	case "show":
		fs := newFlags("broker config show")
		asJSON := fs.Bool("json", false, "print JSON")
		if _, err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		c, ok, err := loadBrokerConfig(ctx, a)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(a.stdout, "no broker is configured: offline. Nothing is read from a broker and nothing is sent.")
			return nil
		}
		if *asJSON {
			return printJSON(a, c)
		}
		fmt.Fprintf(a.stdout, "transport %s, mode %s, set by %s at %s\n", c.Transport, c.Mode, c.By, c.At.UTC().Format(time.RFC3339))
		if c.Transport == "ibkr" {
			fmt.Fprintf(a.stdout, "gateway %s", c.URL)
			if c.CAFile != "" {
				fmt.Fprintf(a.stdout, ", certificate %s", c.CAFile)
			}
			fmt.Fprintln(a.stdout)
		} else {
			fmt.Fprintf(a.stdout, "fixture file %s (a broker kept in a file: nothing leaves this machine)\n", c.Fixture)
		}
		return nil
	case "set":
		fs := newFlags("broker config set")
		tr := fs.String("transport", "", "fixture or ibkr")
		fx := fs.String("fixture", "", "fixture broker file (transport fixture)")
		url := fs.String("url", "", "Client Portal Gateway address on this machine, https://localhost:5000 (transport ibkr)")
		ca := fs.String("ca", "", "PEM file with the gateway's certificate (transport ibkr)")
		by := fs.String("by", "", "who is setting it")
		if _, err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		c := brokerConfig{Transport: *tr, Fixture: *fx, URL: *url, CAFile: *ca, Mode: broker.ReadOnly, By: *by}
		switch {
		case *by == "":
			return usageErrorf("usage: broker config set -transport fixture -fixture FILE -by NAME | -transport ibkr -url https://localhost:5000 [-ca FILE] -by NAME")
		case *tr == "fixture" && *fx == "":
			return usageErrorf("-fixture FILE is needed for the fixture transport (broker fixture example shows one)")
		case *tr == "ibkr":
			if _, err := ibkr.ValidateBaseURL(*url); err != nil {
				return usageErrorf("%v", err)
			}
		case *tr != "fixture":
			return usageErrorf("-transport must be fixture or ibkr")
		}
		if err := saveBrokerConfig(ctx, a, c); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "broker set to %s in read_only mode. Reading and comparing are possible; nothing can be sent until broker mode trading is chosen on purpose.\n", *tr)
		return nil
	}
	return usageErrorf("unknown subcommand %q (set, show)", args[0])
}

func brokerMode(ctx context.Context, a *app, args []string) error {
	fs := newFlags("broker mode")
	by := fs.String("by", "", "who is changing it")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || *by == "" || (rest[0] != string(broker.ReadOnly) && rest[0] != string(broker.Trading)) {
		return usageErrorf("usage: broker mode read_only|trading -by NAME")
	}
	c, ok, err := loadBrokerConfig(ctx, a)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("no broker is configured: run broker config set first")
	}
	c.Mode, c.By = broker.Mode(rest[0]), *by
	if err := saveBrokerConfig(ctx, a, c); err != nil {
		return err
	}
	if c.Mode == broker.Trading {
		fmt.Fprintln(a.stdout, "mode is trading: an order may be sent to the broker, but only a confirmed order, within the tier, with new orders enabled and every check passing. Nothing was sent.")
	} else {
		fmt.Fprintln(a.stdout, "mode is read_only: nothing can be sent to the broker.")
	}
	return nil
}

func loadBinds(ctx context.Context, a *app) (map[string]brokerBind, error) {
	rs, err := a.records()
	if err != nil {
		return nil, err
	}
	recs, err := rs.Current(ctx, recBrokerBind, "")
	if err != nil {
		return nil, err
	}
	out := map[string]brokerBind{}
	for _, r := range recs {
		var b brokerBind
		if err := json.Unmarshal(r.Body, &b); err != nil {
			return nil, fmt.Errorf("binding %s does not decode: %w", r.Key, err)
		}
		out[b.Book] = b
	}
	return out, nil
}

func brokerBindCmd(ctx context.Context, a *app, args []string) error {
	fs := newFlags("broker bind")
	acct := fs.String("account", "", "broker account number")
	by := fs.String("by", "", "who is binding")
	replace := fs.Bool("replace", false, "replace an existing binding of the book")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" || *acct == "" || *by == "" {
		return usageErrorf("usage: broker bind BOOK -account ID -by NAME")
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	b, err := ps.FindBook(ctx, book)
	if err != nil {
		return err
	}
	if b.Env == portfolio.PaperLocal {
		return fmt.Errorf("%s is a paper_local book: it is simulated here and has no broker account", b.ID)
	}
	br, _, err := openBroker(ctx, a)
	if err != nil {
		return err
	}
	accts, err := br.Accounts(ctx)
	if err != nil {
		return explainBrokerErr(err)
	}
	var found *broker.Account
	for i := range accts {
		if accts[i].ID == *acct {
			found = &accts[i]
		}
	}
	if found == nil {
		return fmt.Errorf("the session cannot see account %s", broker.Redact(*acct))
	}
	// paper and live never share a book: the book's environment must match the account
	switch {
	case b.Env == portfolio.PaperBroker && !found.Paper:
		return fmt.Errorf("%s is a paper_broker book but account %s is not a paper account", b.ID, broker.Redact(*acct))
	case b.Env == portfolio.Real && found.Paper:
		return fmt.Errorf("%s is a real book but account %s is a paper account", b.ID, broker.Redact(*acct))
	}
	binds, err := loadBinds(ctx, a)
	if err != nil {
		return err
	}
	for _, o := range binds {
		if o.Account == *acct && o.Book != b.ID {
			return fmt.Errorf("account %s is already bound to book %s: one broker account has one book", broker.Redact(*acct), o.Book)
		}
	}
	if old, ok := binds[b.ID]; ok && old.Account != *acct && !*replace {
		return fmt.Errorf("%s is bound to account %s; pass -replace to rebind it", b.ID, broker.Redact(old.Account))
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(brokerBind{Book: b.ID, Account: *acct, By: *by, At: a.now().UTC()})
	if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recBrokerBind, Key: b.ID, Body: raw}); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s (%s) is bound to account %s (%s).\n", b.ID, b.Env, broker.Redact(*acct), map[bool]string{true: "paper", false: "live"}[found.Paper])
	return nil
}

// explainBrokerErr adds what to do to a classified error.
func explainBrokerErr(err error) error {
	switch broker.KindOf(err) {
	case broker.SessionExpired:
		return fmt.Errorf("%w. The program never logs in: open the gateway page, log in, then try again", err)
	case broker.Competing:
		return fmt.Errorf("%w. Only one brokerage session can be active", err)
	case broker.Pacing:
		return fmt.Errorf("%w. Nothing was retried", err)
	case broker.Maintenance:
		return fmt.Errorf("%w. Brokers have daily maintenance windows; try later", err)
	case broker.Entitlement:
		return fmt.Errorf("%w. The account may lack a permission or market-data subscription", err)
	}
	return err
}

func brokerStatus(ctx context.Context, a *app, args []string) error {
	fs := newFlags("broker status")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	c, ok, err := loadBrokerConfig(ctx, a)
	if err != nil {
		return err
	}
	type acct struct {
		ID    string `json:"account"`
		Paper bool   `json:"paper"`
		Book  string `json:"book,omitempty"`
	}
	out := struct {
		Configured bool            `json:"configured"`
		Transport  string          `json:"transport,omitempty"`
		Mode       broker.Mode     `json:"mode,omitempty"`
		Session    *broker.Session `json:"session,omitempty"`
		Accounts   []acct          `json:"accounts,omitempty"`
		Error      string          `json:"error,omitempty"`
		Verified   string          `json:"real_account_verification"`
	}{Configured: ok, Transport: c.Transport, Mode: c.Mode,
		Verified: "not verified: no request has been made to a real gateway or a real account; the adapter is checked only against recorded contract fixtures"}
	if ok {
		br, _, err := openBroker(ctx, a)
		if err != nil {
			out.Error = err.Error()
		} else if s, err := br.Session(ctx); err != nil {
			out.Error = explainBrokerErr(err).Error()
		} else {
			out.Session = &s
			if as, err := br.Accounts(ctx); err != nil {
				out.Error = explainBrokerErr(err).Error()
			} else {
				binds, berr := loadBinds(ctx, a)
				if berr != nil {
					return berr
				}
				names, nerr := bookNames(ctx, a)
				if nerr != nil {
					return nerr
				}
				byAcct := map[string]string{}
				for _, b := range binds {
					byAcct[b.Account] = names[b.Book]
					if byAcct[b.Account] == "" {
						byAcct[b.Account] = b.Book
					}
				}
				for _, x := range as {
					out.Accounts = append(out.Accounts, acct{ID: broker.Redact(x.ID), Paper: x.Paper, Book: byAcct[x.ID]})
				}
			}
		}
	}
	if *asJSON {
		return printJSON(a, out)
	}
	color := useColor(a.stdout)
	if !ok {
		fmt.Fprintln(a.stdout, styledText("OFFLINE", cliYellow, color)+"  no broker is configured: nothing is read from a broker and nothing is sent.")
		fmt.Fprintln(a.stdout, "real-account verification: "+out.Verified)
		return nil
	}
	fmt.Fprintf(a.stdout, "transport %s, mode %s\n", c.Transport, styledText(string(c.Mode), map[bool]cliStyle{true: cliGreen, false: cliRed}[c.Mode == broker.ReadOnly], color))
	switch s := out.Session; {
	case s == nil:
	case !s.Authenticated:
		fmt.Fprintln(a.stdout, "session: "+styledText("NOT LOGGED IN", cliRed, color)+" (log in in the gateway's own page; this program never logs in)")
	case s.Competing:
		fmt.Fprintln(a.stdout, "session: "+styledText("COMPETING", cliRed, color)+" - another session holds the brokerage session")
	default:
		fmt.Fprintln(a.stdout, "session: "+styledText("authenticated", cliGreen, color))
	}
	if out.Error != "" {
		fmt.Fprintln(a.stdout, styledText("problem: ", cliRed, color)+out.Error)
	}
	if len(out.Accounts) > 0 {
		t := cliTable{Header: []string{"account", "kind", "book"}}
		for _, x := range out.Accounts {
			kind, st := "live", cliStrong
			if x.Paper {
				kind, st = "paper", cliYellow
			}
			book := x.Book
			if book == "" {
				book = "(not bound)"
			}
			t.Rows = append(t.Rows, []cliCell{plain(x.ID), styled(kind, st), plain(book)})
		}
		t.write(a.stdout, color)
	}
	fmt.Fprintln(a.stdout, "real-account verification: "+styledText(out.Verified, cliYellow, color))
	return nil
}

// --- sync ---

func conidMap(ctx context.Context, a *app) (map[int64]string, error) {
	db, err := a.store()
	if err != nil {
		return nil, err
	}
	set, err := db.InstrumentsAsOf(ctx, time.Time{})
	if err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, l := range set.Listings {
		for _, c := range l.Contracts {
			if c.Broker != "ibkr" {
				continue
			}
			n, err := strconv.ParseInt(c.ConID, 10, 64)
			if err != nil {
				continue
			}
			out[n] = l.SecurityID
		}
	}
	return out, nil
}

func localSide(ctx context.Context, a *app, b portfolio.Book) (recon.Local, error) {
	ps, err := a.portfolios()
	if err != nil {
		return recon.Local{}, err
	}
	l, err := ps.Ledger(ctx, b.ID)
	if err != nil {
		return recon.Local{}, err
	}
	p := l.Projection()
	loc := recon.Local{Cash: map[money.Currency]int64{}, Settled: map[money.Currency]int64{}, Held: map[string]money.Decimal{}}
	for _, cur := range p.Currencies() {
		loc.Cash[cur] = p.Cash(cur) + p.Unsettled(cur)
		loc.Settled[cur] = p.Cash(cur)
	}
	for _, x := range p.Positions() {
		loc.Held[x.Security] = x.Qty
	}
	if loc.Orders, err = paperViews(ctx, a, b); err != nil {
		return loc, err
	}
	if loc.SecurityOf, err = conidMap(ctx, a); err != nil {
		return loc, err
	}
	return loc, nil
}

func snapshot(ctx context.Context, br broker.Broker, account string) (recon.Snapshot, error) {
	var s recon.Snapshot
	var err error
	if s.Balances, err = br.Balances(ctx, account); err != nil {
		return s, err
	}
	if s.Positions, err = br.Positions(ctx, account); err != nil {
		return s, err
	}
	if s.Open, err = br.OpenOrders(ctx, account); err != nil {
		return s, err
	}
	s.Executions, err = br.Executions(ctx, account)
	return s, err
}

// boundBroker returns the broker, the book and its account, after the checks
// every read and write of a book shares.
func boundBroker(ctx context.Context, a *app, bookRef string) (broker.Broker, brokerConfig, portfolio.Book, string, error) {
	ps, err := a.portfolios()
	if err != nil {
		return nil, brokerConfig{}, portfolio.Book{}, "", err
	}
	b, err := ps.FindBook(ctx, bookRef)
	if err != nil {
		return nil, brokerConfig{}, b, "", err
	}
	if b.Env == portfolio.PaperLocal {
		return nil, brokerConfig{}, b, "", fmt.Errorf("%s is a paper_local book: it has no broker", b.ID)
	}
	binds, err := loadBinds(ctx, a)
	if err != nil {
		return nil, brokerConfig{}, b, "", err
	}
	bind, ok := binds[b.ID]
	if !ok {
		return nil, brokerConfig{}, b, "", fmt.Errorf("%s is not bound to a broker account: run broker bind %s -account ID -by NAME", b.ID, b.ID)
	}
	br, c, err := openBroker(ctx, a)
	if err != nil {
		return nil, c, b, "", err
	}
	// the environment is checked against the broker on every use, not only when binding
	accts, err := br.Accounts(ctx)
	if err != nil {
		return nil, c, b, "", explainBrokerErr(err)
	}
	for _, x := range accts {
		if x.ID != bind.Account {
			continue
		}
		if (b.Env == portfolio.PaperBroker) != x.Paper {
			return nil, c, b, "", fmt.Errorf("%s is a %s book but the broker now reports account %s as %s: refusing to continue", b.ID, b.Env, broker.Redact(x.ID), map[bool]string{true: "paper", false: "live"}[x.Paper])
		}
		return br, c, b, bind.Account, nil
	}
	return nil, c, b, "", fmt.Errorf("the session no longer sees account %s bound to %s", broker.Redact(bind.Account), b.ID)
}

func diffID(book string, d recon.Difference) string {
	sum := sha256.Sum256([]byte(d.Key()))
	return book + "/" + hex.EncodeToString(sum[:6])
}

func brokerSync(ctx context.Context, a *app, args []string) error {
	fs := newFlags("broker sync")
	asJSON := fs.Bool("json", false, "print JSON")
	bookRef, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if bookRef == "" {
		return usageErrorf("usage: broker sync BOOK [-json]")
	}
	br, _, b, account, err := boundBroker(ctx, a, bookRef)
	if err != nil {
		if k := broker.KindOf(err); k == broker.Maintenance || k == broker.Malformed {
			_ = noteBrokerUnhealthy(ctx, a, err)
		}
		return err
	}
	snap, err := snapshot(ctx, br, account)
	if err != nil {
		if k := broker.KindOf(err); k == broker.Maintenance || k == broker.Malformed {
			_ = noteBrokerUnhealthy(ctx, a, err)
		}
		return explainBrokerErr(err)
	}
	loc, err := localSide(ctx, a, b)
	if err != nil {
		return err
	}
	first := recon.Compare(loc, snap)
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	by := map[string][]order.Reported{}
	var ids []string
	for _, f := range first.Facts {
		if _, ok := by[f.Order]; !ok {
			ids = append(ids, f.Order)
		}
		by[f.Order] = append(by[f.Order], f.Reported)
	}
	for _, id := range ids {
		if err := svc.Report(ctx, b.ID, id, by[id]...); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	// what is left after the facts were applied is what needs a person
	if loc, err = localSide(ctx, a, b); err != nil {
		return err
	}
	final := recon.Compare(loc, snap)
	n, err := recordDifferences(ctx, a, b.ID, final.Differences)
	if err != nil {
		return err
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(brokerSyncRec{Book: b.ID, At: a.now().UTC(), Applied: len(first.Facts), Differences: len(final.Differences)})
	if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recBrokerSync, Key: b.ID, Body: raw}); err != nil {
		return err
	}
	suspended := false
	if len(final.Differences) > 0 {
		if err := recordSignal(ctx, a, tier.Signal{Kind: tier.ReconcileFailed, Active: true, Reason: fmt.Sprintf("%s: %d difference(s) with the broker", b.ID, len(final.Differences)), By: "broker sync", At: a.now().UTC()}); err != nil {
			return err
		}
		if _, err := revokeNewRisk(ctx, a, "reconciliation_failed: "+b.ID); err != nil {
			return err
		}
		suspended = true
	}
	if *asJSON {
		return printJSON(a, struct {
			Book        string             `json:"book"`
			Applied     []recon.Fact       `json:"applied"`
			Differences []recon.Difference `json:"differences"`
			NewRecords  int                `json:"new_difference_records"`
			Suspended   bool               `json:"new_risk_suspended"`
		}{b.ID, first.Facts, final.Differences, n, suspended})
	}
	color := useColor(a.stdout)
	fmt.Fprintf(a.stdout, "synced %s with account %s: %d balance line(s), %d position(s), %d open order(s), %d execution(s).\n", b.ID, broker.Redact(account), len(snap.Balances), len(snap.Positions), len(snap.Open), len(snap.Executions))
	if len(first.Facts) > 0 {
		t := cliTable{Header: []string{"order", "applied", "note"}}
		for _, f := range first.Facts {
			t.Rows = append(t.Rows, []cliCell{styled(f.Order, cliStrong), plain(string(f.Reported.Kind)), plain(f.Note)})
		}
		t.write(a.stdout, color)
		fmt.Fprintln(a.stdout, "Facts are applied by their broker IDs: running sync again changes nothing.")
	}
	if len(final.Differences) == 0 {
		fmt.Fprintln(a.stdout, styledText("both sides agree.", cliGreen, color))
		return nil
	}
	printDifferences(a, final.Differences)
	fmt.Fprintln(a.stdout, styledText("NEW RISK IS SUSPENDED", cliRed, color)+" until the differences are explained and tier signal clear reconciliation_failed is run by a person. Holdings and open orders are untouched. Nothing at the broker or in the ledger was overwritten.")
	return nil
}

func noteBrokerUnhealthy(ctx context.Context, a *app, cause error) error {
	if err := recordSignal(ctx, a, tier.Signal{Kind: tier.BrokerUnhealthy, Active: true, Reason: cause.Error(), By: "broker sync", At: a.now().UTC()}); err != nil {
		return err
	}
	_, err := revokeNewRisk(ctx, a, "broker_unhealthy")
	return err
}

func printDifferences(a *app, ds []recon.Difference) {
	t := cliTable{Header: []string{"kind", "subject", "here", "broker", "what to do"}}
	for _, d := range ds {
		t.Rows = append(t.Rows, []cliCell{styled(string(d.Kind), cliRed), styled(d.Subject, cliStrong), plain(d.Local), plain(d.Broker), plain(d.Hint)})
	}
	t.write(a.stdout, useColor(a.stdout))
}

// recordDifferences keeps a record for every open difference and closes the ones
// that are gone. A difference that is already open is not written again.
func recordDifferences(ctx context.Context, a *app, book string, ds []recon.Difference) (int, error) {
	rs, err := a.records()
	if err != nil {
		return 0, err
	}
	cur, err := rs.Current(ctx, recBrokerDiff, book+"/")
	if err != nil {
		return 0, err
	}
	state := map[string]brokerDiffRec{}
	for _, r := range cur {
		var d brokerDiffRec
		if err := json.Unmarshal(r.Body, &d); err != nil {
			return 0, err
		}
		state[r.Key] = d
	}
	now := a.now().UTC()
	live := map[string]bool{}
	var in []store.RecordInput
	for _, d := range ds {
		k := diffID(book, d)
		live[k] = true
		if s, ok := state[k]; ok && s.State == "open" {
			continue
		}
		h, err := rs.History(ctx, recBrokerDiff, k)
		if err != nil {
			return 0, err
		}
		raw, _ := json.Marshal(brokerDiffRec{State: "open", Episode: len(h), Book: book, Diff: d, At: now})
		in = append(in, store.RecordInput{Kind: recBrokerDiff, Key: k, Body: raw})
	}
	for k, s := range state {
		if s.State == "open" && !live[k] {
			raw, _ := json.Marshal(brokerDiffRec{State: "cleared", Episode: s.Episode, Book: book, Diff: s.Diff, At: now})
			in = append(in, store.RecordInput{Kind: recBrokerDiff, Key: k, Body: raw})
		}
	}
	if len(in) == 0 {
		return 0, nil
	}
	_, n, err := rs.Append(ctx, in...)
	return n, err
}

func brokerDifferences(ctx context.Context, a *app, args []string) error {
	fs := newFlags("broker differences")
	all := fs.Bool("all", false, "include cleared differences")
	asJSON := fs.Bool("json", false, "print JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	prefix := ""
	if book != "" {
		ps, err := a.portfolios()
		if err != nil {
			return err
		}
		b, err := ps.FindBook(ctx, book)
		if err != nil {
			return err
		}
		prefix = b.ID + "/"
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	recs, err := rs.Current(ctx, recBrokerDiff, prefix)
	if err != nil {
		return err
	}
	var rows []brokerDiffRec
	for _, r := range recs {
		var d brokerDiffRec
		if err := json.Unmarshal(r.Body, &d); err != nil {
			return err
		}
		if d.State == "open" || *all {
			rows = append(rows, d)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].At.Before(rows[j].At) })
	if *asJSON {
		return printJSON(a, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(a.stdout, "no open differences with a broker.")
		return nil
	}
	t := cliTable{Header: []string{"state", "since", "book", "kind", "subject", "here", "broker", "what to do"}}
	for _, d := range rows {
		st := styled("OPEN", cliRed)
		if d.State == "cleared" {
			st = styled("cleared", cliGreen)
		}
		t.Rows = append(t.Rows, []cliCell{st, plain(d.At.UTC().Format(time.RFC3339)), plain(d.Book), plain(string(d.Diff.Kind)), styled(d.Diff.Subject, cliStrong), plain(d.Diff.Local), plain(d.Diff.Broker), plain(d.Diff.Hint)})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func brokerContracts(ctx context.Context, a *app, args []string) error {
	fs := newFlags("broker contracts")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("usage: broker contracts SYMBOL [-json]")
	}
	br, _, err := openBroker(ctx, a)
	if err != nil {
		return err
	}
	cs, err := br.Search(ctx, rest[0])
	if err != nil {
		return explainBrokerErr(err)
	}
	known, err := conidMap(ctx, a)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, cs)
	}
	if len(cs) == 0 {
		fmt.Fprintf(a.stdout, "the broker knows no contract for %s.\n", rest[0])
		return nil
	}
	t := cliTable{Header: []string{"conid", "symbol", "name", "exchange", "currency", "type", "in the instrument register"}}
	for _, c := range cs {
		reg := styled("no: add contracts: [{broker: ibkr, conid: \""+strconv.FormatInt(c.Conid, 10)+"\"}] to its listing", cliYellow)
		if sec, ok := known[c.Conid]; ok {
			reg = styled("yes: "+sec, cliGreen)
		}
		t.Rows = append(t.Rows, []cliCell{plain(strconv.FormatInt(c.Conid, 10)), styled(c.Symbol, cliStrong), plain(c.Name), plain(c.Exchange), plain(string(c.Currency)), plain(c.Type), reg})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintln(a.stdout, "A symbol can name several contracts on several venues; pick the one that matches the listing you hold. Nothing is matched by symbol alone.")
	return nil
}

const fixtureExample = `{
  "session": {"authenticated": true, "connected": true, "competing": false, "message": ""},
  "accounts": [{"id": "DU1234567", "type": "INDIVIDUAL", "title": "Paper", "paper": true, "features": {}}],
  "balances": {"DU1234567": [{"currency": "USD", "cash": 100000, "settled": 100000}]},
  "positions": {"DU1234567": []},
  "orders": {"DU1234567": []},
  "executions": {"DU1234567": []},
  "contracts": [{"conid": 265598, "symbol": "AAPL", "name": "APPLE INC", "exchange": "NASDAQ", "currency": "USD", "type": "STK"}],
  "quotes": [],
  "script": {"preview_warnings": [], "place_replies": [], "place_reject": "", "commission": 100},
  "failures": {},
  "next_order": 0
}
`

func brokerFixture(a *app, args []string) error {
	if len(args) != 1 || args[0] != "example" {
		return usageErrorf("usage: broker fixture example")
	}
	fmt.Fprintln(a.stdout, "# a broker kept in a file: amounts are minor units (100000 = 1000.00 USD). Save it and run broker config set -transport fixture -fixture FILE")
	fmt.Fprintln(a.stdout, "# failures: {\"Place\": {\"kind\": \"unavailable\", \"message\": \"timeout\", \"once\": true, \"after\": true}} places the order and loses the answer")
	_, err := fmt.Fprint(a.stdout, fixtureExample)
	return err
}

// bookNames maps a book ID to the name a person uses, account/name.
func bookNames(ctx context.Context, a *app) (map[string]string, error) {
	ps, err := a.portfolios()
	if err != nil {
		return nil, err
	}
	books, err := ps.Books(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, b := range books {
		out[b.ID] = b.Account + "/" + b.Name
	}
	return out, nil
}
