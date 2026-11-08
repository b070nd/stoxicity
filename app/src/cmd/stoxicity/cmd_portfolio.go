package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/quotes"
	"github.com/b070nd/stoxicity/app/src/store"
)

// cmdPortfolio is the accounts and the journal: books (real, local paper, broker
// paper), the transactions posted to them, and what those add up to. It never
// places an order and never reads a broker; a number it shows is a sum of lines
// somebody recorded.
func cmdPortfolio(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: book | add | import | transfer | show | journal | verify | reconcile")
	}
	switch args[0] {
	case "book":
		return portfolioBook(ctx, a, args[1:])
	case "add":
		return portfolioAdd(ctx, a, args[1:])
	case "import":
		return portfolioImport(ctx, a, args[1:])
	case "transfer":
		return portfolioTransfer(ctx, a, args[1:])
	case "show":
		return portfolioShow(ctx, a, args[1:])
	case "journal":
		return portfolioJournal(ctx, a, args[1:])
	case "verify":
		return portfolioVerify(ctx, a, args[1:])
	case "reconcile":
		return portfolioReconcile(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (book, add, import, transfer, show, journal, verify, reconcile)", args[0])
}

func (a *app) portfolios() (store.Portfolios, error) {
	if _, err := a.requireTenant(); err != nil {
		return store.Portfolios{}, err
	}
	db, err := a.store()
	if err != nil {
		return store.Portfolios{}, err
	}
	db.SetClock(a.now) // recorded times and "now" are one clock
	return db.Portfolios(a.tenantID), nil
}

func envCell(e portfolio.Env) cliCell {
	if e == portfolio.Real {
		return styled(string(e), cliStrong)
	}
	return styled(string(e), cliYellow)
}

func portfolioBook(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: book create | list | archive | reset")
	}
	switch args[0] {
	case "create":
		fs := newFlags("portfolio book create")
		acct := fs.String("account", "", "the account the book belongs to (letters, digits, - _ .)")
		name := fs.String("name", "", "the book's name")
		env := fs.String("env", "", "real, paper_local or paper_broker: there is no default, a real book is never an accident")
		base := fs.String("base", "EUR", "reporting currency")
		method := fs.String("method", "fifo", "which lots a sale uses: fifo or lifo")
		neg := fs.Bool("allow-negative-cash", false, "let settled cash go below zero (margin)")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 || *acct == "" || *name == "" || *env == "" {
			return usageErrorf("usage: portfolio book create -account A -name N -env real|paper_local|paper_broker [-base EUR] [-method fifo|lifo] [-allow-negative-cash]")
		}
		ps, err := a.portfolios()
		if err != nil {
			return err
		}
		b, err := ps.CreateBook(ctx, portfolio.Book{Account: *acct, Name: *name, Env: portfolio.Env(*env), Base: money.Currency(*base), Method: portfolio.CostMethod(*method), AllowNegativeCash: *neg})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "book %s/%s created: %s, %s, lots by %s, id %s\n", b.Account, b.Name, b.Env, b.Base, b.Method, b.ID)
		return nil
	case "list":
		fs := newFlags("portfolio book list")
		asJSON := fs.Bool("json", false, "print as JSON")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 {
			return usageErrorf("unexpected argument %q", rest[0])
		}
		ps, err := a.portfolios()
		if err != nil {
			return err
		}
		books, err := ps.Books(ctx)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(a.stdout, books)
		}
		if len(books) == 0 {
			fmt.Fprintln(a.stdout, "no books yet; start with: stoxicity portfolio book create -account A -name N -env paper_local")
			return nil
		}
		t := cliTable{Header: []string{"account", "book", "environment", "base", "lots", "status", "id"}}
		for _, b := range books {
			st := plain(string(b.Status))
			if b.Status == portfolio.Archived {
				st = styled("archived", cliDim)
			}
			t.Rows = append(t.Rows, []cliCell{plain(b.Account), styled(b.Name, cliStrong), envCell(b.Env), plain(string(b.Base)), plain(string(b.Method)), st, styled(b.ID, cliDim)})
		}
		t.write(a.stdout, useColor(a.stdout))
		return nil
	case "archive":
		if len(args) != 2 {
			return usageErrorf("usage: portfolio book archive BOOK (a paper book only)")
		}
		ps, err := a.portfolios()
		if err != nil {
			return err
		}
		if err := ps.Archive(ctx, args[1]); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "book %s archived: it keeps its journal and takes no more transactions\n", args[1])
		return nil
	case "reset":
		fs := newFlags("portfolio book reset")
		name := fs.String("name", "", "name of the new empty book")
		book, err := parseBookFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if book == "" || *name == "" {
			return usageErrorf("usage: portfolio book reset BOOK -name NEW (a paper book only)")
		}
		ps, err := a.portfolios()
		if err != nil {
			return err
		}
		nb, err := ps.ResetPaper(ctx, book, *name)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "book %s archived with its journal intact; new empty %s book %s/%s opened (id %s)\n", book, nb.Env, nb.Account, nb.Name, nb.ID)
		return nil
	}
	return usageErrorf("unknown subcommand %q (create, list, archive, reset)", args[0])
}

// The columns of a transaction line, shared by `add` (flags) and `import` (CSV),
// and which of them each kind uses. A column a kind does not use must be empty:
// a value is never silently ignored.
var portfolioUses = map[string][]string{
	"deposit": {"amount"}, "withdrawal": {"amount"}, "fee": {"amount"}, "commission": {"amount"},
	"buy":      {"security", "qty", "gross", "commission", "settle_day", "price", "order"},
	"sell":     {"security", "qty", "gross", "commission", "settle_day", "price", "order"},
	"settle":   {"ref"},
	"dividend": {"gross", "withheld"}, "interest": {"gross", "withheld"}, "other": {"gross", "withheld"},
	"reinvest":       {"security", "qty", "gross", "withheld"},
	"fx":             {"amount", "to", "commission", "rate"},
	"split":          {"security", "ratio"},
	"reverse":        {"ref"},
	"reserve_cash":   {"order", "amount"},
	"reserve_shares": {"order", "security", "qty"},
	"release":        {"order"},
}

var portfolioRequired = map[string][]string{
	"deposit": {"amount"}, "withdrawal": {"amount"}, "fee": {"amount"}, "commission": {"amount"},
	"buy": {"security", "qty", "gross"}, "sell": {"security", "qty", "gross"},
	"settle": {"ref"}, "dividend": {"gross"}, "interest": {"gross"}, "other": {"gross"},
	"reinvest": {"security", "qty", "gross"}, "fx": {"amount", "to"}, "split": {"security", "ratio"},
	"reverse": {"ref"}, "reserve_cash": {"order", "amount"}, "reserve_shares": {"order", "security", "qty"}, "release": {"order"},
}

var portfolioDataFields = []string{"security", "qty", "amount", "gross", "commission", "withheld", "to", "rate", "ratio", "ref", "order", "settle_day", "price"}

func portfolioKindNames() string {
	var ks []string
	for k := range portfolioUses {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

// txSpec is one line, read and checked for form but not yet for the ledger.
type txSpec struct {
	label                                   string
	importID, kind, source, memo            string
	event                                   time.Time
	security                                string
	qty                                     money.Decimal
	amount, gross, commission, withheld, to money.Amount
	rate, ref, order, settleDay, price      string
	ratioNew, ratioOld                      int64
	fields                                  map[string]string
}

func parseEvent(s string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q must be YYYY-MM-DD or RFC 3339", s)
	}
	return t.UTC(), nil
}

func specFromFields(f map[string]string, label, source string) (txSpec, error) {
	fail := func(format string, args ...any) (txSpec, error) {
		return txSpec{}, fmt.Errorf("%s: %s", label, fmt.Sprintf(format, args...))
	}
	s := txSpec{label: label, importID: f["id"], kind: f["kind"], source: source, memo: f["memo"], fields: map[string]string{}}
	uses, ok := portfolioUses[s.kind]
	if !ok {
		return fail("unknown kind %q (%s)", s.kind, portfolioKindNames())
	}
	if f["date"] == "" {
		return fail("a date is required")
	}
	var err error
	if s.event, err = parseEvent(f["date"]); err != nil {
		return fail("%v", err)
	}
	use := map[string]bool{}
	for _, u := range uses {
		use[u] = true
	}
	for _, name := range portfolioDataFields {
		if f[name] != "" && !use[name] {
			return fail("a %s line does not use %q", s.kind, name)
		}
		s.fields[name] = f[name]
	}
	for _, name := range portfolioRequired[s.kind] {
		if f[name] == "" {
			return fail("a %s line needs %q", s.kind, name)
		}
	}
	amount := func(name string) (money.Amount, error) {
		if f[name] == "" {
			return money.Amount{}, nil
		}
		v, err := money.ParseAmount(f[name])
		if err != nil {
			return money.Amount{}, fmt.Errorf("%s: %v", name, err)
		}
		return v, nil
	}
	for name, dst := range map[string]*money.Amount{"amount": &s.amount, "gross": &s.gross, "commission": &s.commission, "withheld": &s.withheld, "to": &s.to} {
		if *dst, err = amount(name); err != nil {
			return fail("%v", err)
		}
	}
	if f["qty"] != "" {
		if s.qty, err = money.ParseDecimal(f["qty"]); err != nil {
			return fail("qty: %v", err)
		}
	}
	if f["ratio"] != "" {
		n, o, ok := strings.Cut(f["ratio"], ":")
		var e1, e2 error
		s.ratioNew, e1 = strconv.ParseInt(n, 10, 64)
		s.ratioOld, e2 = strconv.ParseInt(o, 10, 64)
		if !ok || e1 != nil || e2 != nil || s.ratioNew < 1 || s.ratioOld < 1 {
			return fail("ratio %q must be NEW:OLD, for example 2:1 for a two-for-one split", f["ratio"])
		}
	}
	if f["settle_day"] != "" {
		if _, err := time.Parse(time.DateOnly, f["settle_day"]); err != nil {
			return fail("settle_day %q must be YYYY-MM-DD", f["settle_day"])
		}
	}
	s.rate, s.ref, s.order, s.settleDay, s.price = f["rate"], f["ref"], f["order"], f["settle_day"], f["price"]
	s.security = f["security"]
	return s, nil
}

// req identifies what the line asks for, apart from where it came from, so the
// same line imported again is recognised and a changed one is refused.
func (s txSpec) req() string {
	parts := []string{s.kind, strconv.FormatInt(s.event.UnixMilli(), 10)}
	for _, name := range portfolioDataFields {
		v := s.fields[name]
		switch name {
		case "amount", "gross", "commission", "withheld", "to":
			if p, err := money.ParseAmount(v); err == nil {
				v = p.String()
			}
		case "qty":
			v = s.qty.String()
		}
		parts = append(parts, v)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:10])
}

// securitySet is the registered securities by ID. An empty register is not an
// error here: deposits, fees and transfers of cash need no security.
func securitySet(ctx context.Context, a *app) (map[string]instrument.Security, error) {
	db, err := a.store()
	if err != nil {
		return nil, err
	}
	set, err := db.InstrumentsAsOf(ctx, time.Time{})
	if err != nil {
		return nil, err
	}
	m := map[string]instrument.Security{}
	for _, s := range set.Securities {
		m[s.ID] = s
	}
	return m, nil
}

func knownSecurity(secs map[string]instrument.Security, id string) error {
	if _, ok := secs[id]; ok {
		return nil
	}
	return fmt.Errorf("security %q is not in the instrument register: import it with `instruments import` first, so a typo cannot create a position", id)
}

func (s txSpec) build(l *portfolio.Ledger, secs map[string]instrument.Security) (portfolio.Tx, error) {
	c := portfolio.Common{Event: s.event, Source: s.source, ImportID: s.importID, Memo: s.memo}
	if s.security != "" {
		if err := knownSecurity(secs, s.security); err != nil {
			return portfolio.Tx{}, err
		}
	}
	trade := portfolio.Trade{Common: c, Security: s.security, Qty: s.qty, Gross: s.gross, Commission: s.commission, SettleDay: s.settleDay, Price: s.price, Releases: s.order}
	var tx portfolio.Tx
	var err error
	switch s.kind {
	case "deposit":
		tx, err = l.Deposit(c, s.amount)
	case "withdrawal":
		tx, err = l.Withdraw(c, s.amount)
	case "fee":
		tx, err = l.Fee(c, s.amount, false)
	case "commission":
		tx, err = l.Fee(c, s.amount, true)
	case "buy":
		tx, err = l.Buy(trade)
	case "sell":
		tx, err = l.Sell(trade)
	case "settle", "reverse":
		id := s.ref
		if !strings.HasPrefix(id, "tx_") {
			t, ok := l.Imported(id)
			if !ok {
				return portfolio.Tx{}, fmt.Errorf("%q is neither a transaction ID nor an import ID of this book", s.ref)
			}
			id = t.ID
		}
		if s.kind == "settle" {
			tx, err = l.SettleTrade(c, id)
		} else {
			tx, err = l.Reverse(c, id)
		}
	case "dividend", "interest", "other":
		tx, err = l.Income(c, s.kind, s.gross, s.withheld)
	case "reinvest":
		tx, err = l.Reinvest(c, s.security, s.qty, s.gross, s.withheld)
	case "fx":
		tx, err = l.Convert(c, s.amount, s.to, s.commission, s.rate)
	case "split":
		tx, err = l.SplitShares(c, s.security, s.ratioOld, s.ratioNew)
	case "reserve_cash":
		tx, err = l.ReserveCash(c, s.order, s.amount, s.memo)
	case "reserve_shares":
		tx = l.ReserveSharesFor(c, s.order, s.security, s.qty, s.memo)
	case "release":
		tx = l.ReleaseReservation(c, s.order)
	}
	if err != nil {
		return portfolio.Tx{}, err
	}
	if tx.Meta == nil {
		tx.Meta = map[string]string{}
	}
	tx.Meta["req"] = s.req()
	return tx, nil
}

// postSpec posts one line, or recognises it as one already recorded.
func postSpec(l *portfolio.Ledger, secs map[string]instrument.Security, s txSpec, now time.Time) (portfolio.Tx, bool, error) {
	if s.importID != "" {
		if t, ok := l.Imported(s.importID); ok {
			if t.Meta["req"] != s.req() {
				return t, false, fmt.Errorf("%w: %q was recorded as %s with other content", portfolio.ErrImportConflict, s.importID, t.ID)
			}
			return t, true, nil
		}
	}
	tx, err := s.build(l, secs)
	if err != nil {
		return portfolio.Tx{}, false, err
	}
	p, err := l.Post(tx, now)
	if err != nil {
		return portfolio.Tx{}, false, err
	}
	return p.Tx, false, nil
}

func portfolioAdd(ctx context.Context, a *app, args []string) error {
	fs := newFlags("portfolio add")
	vals := map[string]*string{}
	for _, n := range append([]string{"date", "import-id", "memo", "source"}, portfolioDataFields...) {
		vals[n] = fs.String(n, "", "")
	}
	// the book and the kind come first, then flags
	if len(args) < 2 {
		return usageErrorf("usage: portfolio add BOOK KIND -date D [-import-id ID] [-memo M] and the columns the kind uses (%s)", portfolioKindNames())
	}
	book, kind := args[0], args[1]
	rest, err := parseFlags(fs, args[2:])
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	f := map[string]string{"kind": kind, "id": *vals["import-id"], "date": *vals["date"], "memo": *vals["memo"]}
	for _, n := range portfolioDataFields {
		f[n] = *vals[n]
	}
	source := *vals["source"]
	if source == "" {
		source = "manual"
	}
	s, err := specFromFields(f, "this line", source)
	if err != nil {
		return usageErrorf("%v", err)
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	var posted portfolio.Tx
	var repeated bool
	if err := ps.Update(ctx, []string{book}, func(now time.Time, ls []*portfolio.Ledger) (*store.ImportBatch, error) {
		var err error
		posted, repeated, err = postSpec(ls[0], secs, s, now)
		return nil, err
	}); err != nil {
		return err
	}
	if repeated {
		fmt.Fprintf(a.stdout, "already recorded as %s: nothing was written\n", posted.ID)
		return nil
	}
	fmt.Fprintf(a.stdout, "recorded %s %s on %s: %s\n", posted.ID, posted.Kind, posted.Event.Format(time.DateOnly), txEffect(posted))
	return nil
}

var portfolioCSVColumns = []string{"date", "kind", "security", "qty", "amount", "gross", "commission", "withheld", "to", "rate", "ratio", "ref", "order", "settle_day", "price", "memo"}

func portfolioImport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("portfolio import")
	file := fs.String("file", "", "CSV: id, date, kind and the columns each kind uses")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" || *file == "" {
		return usageErrorf("usage: portfolio import BOOK -file FILE")
	}
	body, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	rows, err := csvtable.Read(strings.NewReader(string(body)), []string{"id", "date", "kind"}, portfolioCSVColumns[2:])
	if err != nil {
		return err
	}
	source := "import:" + filepath.Base(*file)
	var specs []txSpec
	var problems []string
	seen := map[string]string{}
	for _, row := range rows {
		label := "line " + row["_line"]
		if row["id"] == "" {
			problems = append(problems, label+": every line needs an id, so importing it again changes nothing")
			continue
		}
		if prev, dup := seen[row["id"]]; dup {
			problems = append(problems, fmt.Sprintf("%s: id %q is also on %s", label, row["id"], prev))
			continue
		}
		seen[row["id"]] = label
		s, err := specFromFields(row, label, source)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		specs = append(specs, s)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(a.stderr, p)
		}
		return fmt.Errorf("%d of %d lines are wrong; nothing was imported", len(problems), len(rows))
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	sum := quotes.Hash(body)
	var postedN, repeatedN int
	if err := ps.Update(ctx, []string{book}, func(now time.Time, ls []*portfolio.Ledger) (*store.ImportBatch, error) {
		postedN, repeatedN = 0, 0
		for _, s := range specs {
			_, repeated, err := postSpec(ls[0], secs, s, now)
			if err != nil {
				return nil, fmt.Errorf("%s (%s): %w", s.label, s.importID, err)
			}
			if repeated {
				repeatedN++
			} else {
				postedN++
			}
		}
		return &store.ImportBatch{File: filepath.Base(*file), SHA256: sum, Rows: len(specs), Posted: postedN, Repeated: repeatedN}, nil
	}); err != nil {
		fmt.Fprintln(a.stderr, "nothing was imported")
		return err
	}
	fmt.Fprintf(a.stdout, "%d lines read, %d recorded, %d already recorded (%s)\n", len(specs), postedN, repeatedN, sum)
	return nil
}

func portfolioTransfer(ctx context.Context, a *app, args []string) error {
	fs := newFlags("portfolio transfer")
	from := fs.String("from", "", "the book the cash or shares leave")
	to := fs.String("to", "", "the book they arrive in (the same environment)")
	amount := fs.String("amount", "", "cash to move, for example \"500.00 EUR\"")
	security := fs.String("security", "", "security to move, with -qty")
	qty := fs.String("qty", "", "shares to move")
	date := fs.String("date", "", "when, YYYY-MM-DD or RFC 3339")
	importID := fs.String("import-id", "", "makes the transfer idempotent")
	memo := fs.String("memo", "", "a note")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *from == "" || *to == "" || *date == "" {
		return usageErrorf("usage: portfolio transfer -from BOOK -to BOOK -date D (-amount \"500.00 EUR\" | -security S -qty Q) [-import-id ID] [-memo M]")
	}
	ev, err := parseEvent(*date)
	if err != nil {
		return usageErrorf("%v", err)
	}
	ta := portfolio.TransferArgs{Common: portfolio.Common{Event: ev, Source: "manual", ImportID: *importID, Memo: *memo}, Security: *security}
	if *amount != "" {
		if ta.Cash, err = money.ParseAmount(*amount); err != nil {
			return usageErrorf("-amount: %v", err)
		}
	}
	if *qty != "" {
		if ta.Qty, err = money.ParseDecimal(*qty); err != nil {
			return usageErrorf("-qty: %v", err)
		}
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	var po, pi portfolio.Posted
	if err := ps.Update(ctx, []string{*from, *to}, func(now time.Time, ls []*portfolio.Ledger) (*store.ImportBatch, error) {
		var err error
		po, pi, err = portfolio.PostTransfer(ls[0], ls[1], ta, now)
		return nil, err
	}); err != nil {
		return err
	}
	if po.Existing && pi.Existing {
		fmt.Fprintln(a.stdout, "already recorded: nothing was written")
		return nil
	}
	fmt.Fprintf(a.stdout, "moved: %s out of %s (%s), %s into %s (%s)\n", txEffect(po.Tx), *from, po.Tx.ID, txEffect(pi.Tx), *to, pi.Tx.ID)
	return nil
}

func amountText(minor int64, cur money.Currency) string {
	v, err := money.FromMinor(minor, cur)
	if err != nil {
		return fmt.Sprint(minor)
	}
	return v.String()
}

func signedText(minor int64, cur money.Currency) string {
	if minor > 0 {
		return "+" + amountText(minor, cur)
	}
	return amountText(minor, cur)
}

// txEffect says in one line what a transaction does to cash, unsettled cash and
// shares.
func txEffect(t portfolio.Tx) string {
	if r := t.Reservation; r != nil {
		if r.Kind == portfolio.ReserveCash {
			return fmt.Sprintf("hold %s for %s", amountText(r.Amount, r.Currency), r.ID)
		}
		return fmt.Sprintf("hold %s %s for %s", r.Qty, r.Security, r.ID)
	}
	if t.Kind == portfolio.Release {
		return "release " + t.Releases
	}
	sums := map[string]map[money.Currency]int64{"cash": {}, "unsettled": {}}
	shares := map[string]money.Decimal{}
	for _, e := range t.Entries {
		if m, ok := sums[e.Account]; ok {
			m[e.Amount.Currency()] += e.Amount.Minor()
		}
		if e.Security != "" {
			if q, err := shares[e.Security].Add(e.Qty); err == nil {
				shares[e.Security] = q
			}
		}
	}
	var parts []string
	for _, acct := range []string{"cash", "unsettled"} {
		var curs []string
		for c, v := range sums[acct] {
			if v != 0 {
				curs = append(curs, string(c))
			}
		}
		sort.Strings(curs)
		for _, cs := range curs {
			parts = append(parts, acct+" "+signedText(sums[acct][money.Currency(cs)], money.Currency(cs)))
		}
	}
	var secs []string
	for s := range shares {
		secs = append(secs, s)
	}
	sort.Strings(secs)
	for _, s := range secs {
		q := shares[s]
		sign := ""
		if q.Sign() > 0 {
			sign = "+"
		}
		parts = append(parts, fmt.Sprintf("%s %s%s", s, sign, q))
	}
	if len(parts) == 0 {
		return "no cash or share movement"
	}
	return strings.Join(parts, "; ")
}

// portfolioAsOf reads an as-of time. A bare date means the end of that day, so
// "as of 2026-11-05" includes everything that happened on the 5th; give an RFC
// 3339 time to cut inside a day.
func portfolioAsOf(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := parseEvent(s)
	if err != nil {
		return t, usageErrorf("-as-of: %v", err)
	}
	if len(s) == len(time.DateOnly) {
		t = t.Add(24*time.Hour - time.Millisecond)
	}
	return t, nil
}

type cashRow struct {
	Currency  money.Currency `json:"currency"`
	Settled   money.Amount   `json:"settled"`
	Unsettled money.Amount   `json:"unsettled"`
	Reserved  money.Amount   `json:"reserved"`
	Available money.Amount   `json:"available"`
}

type posRow struct {
	Security string         `json:"security"`
	Name     string         `json:"name,omitempty"`
	Qty      money.Decimal  `json:"qty"`
	Cost     []money.Amount `json:"cost"`
	Lots     int            `json:"lots"`
}

type bookView struct {
	Book      portfolio.Book `json:"book"`
	AsOf      string         `json:"as_of,omitempty"`
	Basis     string         `json:"basis,omitempty"`
	Txs       int            `json:"transactions"`
	Cash      []cashRow      `json:"cash"`
	Positions []posRow       `json:"positions"`
	Realized  []money.Amount `json:"realized_gain"`
	Holds     []string       `json:"open_orders"`
	Balanced  bool           `json:"balanced"`
}

func viewOf(l *portfolio.Ledger, names map[string]instrument.Security) bookView {
	p := l.Projection()
	v := bookView{Book: l.Book, Txs: len(l.Txs()), Balanced: true}
	mk := func(minor int64, c money.Currency) money.Amount { x, _ := money.FromMinor(minor, c); return x }
	for _, c := range p.Currencies() {
		if p.TrialBalance(c) != 0 {
			v.Balanced = false
		}
		cash, un, res := p.Cash(c), p.Unsettled(c), p.ReservedCash(c, "")
		if cash != 0 || un != 0 || res != 0 {
			v.Cash = append(v.Cash, cashRow{c, mk(cash, c), mk(un, c), mk(res, c), mk(p.Available(c), c)})
		}
		if g := p.RealizedGain(c); g != 0 {
			v.Realized = append(v.Realized, mk(g, c))
		}
	}
	for _, pos := range p.Positions() {
		r := posRow{Security: pos.Security, Name: names[pos.Security].Name, Qty: pos.Qty, Lots: pos.Lots}
		var curs []string
		for c := range pos.Cost {
			curs = append(curs, string(c))
		}
		sort.Strings(curs)
		for _, cs := range curs {
			r.Cost = append(r.Cost, mk(pos.Cost[money.Currency(cs)], money.Currency(cs)))
		}
		v.Positions = append(v.Positions, r)
	}
	for _, r := range p.Reservations() {
		if r.Kind == portfolio.ReserveCash {
			v.Holds = append(v.Holds, fmt.Sprintf("%s holds %s %s", r.ID, amountText(r.Amount, r.Currency), r.Intent))
		} else {
			v.Holds = append(v.Holds, fmt.Sprintf("%s holds %s %s %s", r.ID, r.Qty, r.Security, r.Intent))
		}
	}
	return v
}

func portfolioShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("portfolio show")
	asOf := fs.String("as-of", "", "the book as of this time (YYYY-MM-DD or RFC 3339); default now")
	basis := fs.String("basis", "event", "event: what had happened by then; recorded: what had been written by then")
	lots := fs.Bool("lots", false, "also list the open lots")
	asJSON := fs.Bool("json", false, "print as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: portfolio show BOOK [-as-of TIME] [-basis event|recorded] [-lots] [-json]")
	}
	when, err := portfolioAsOf(*asOf)
	if err != nil {
		return err
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	var l *portfolio.Ledger
	if when.IsZero() {
		l, err = ps.Ledger(ctx, book)
	} else {
		l, err = ps.LedgerAsOf(ctx, book, when, store.Basis(*basis))
	}
	if err != nil {
		return err
	}
	names, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	v := viewOf(l, names)
	if !when.IsZero() {
		v.AsOf, v.Basis = when.Format(time.RFC3339), *basis
	}
	if *asJSON {
		return writeJSON(a.stdout, v)
	}
	color := useColor(a.stdout)
	title := fmt.Sprintf("%s/%s", v.Book.Account, v.Book.Name)
	fmt.Fprintf(a.stdout, "%s  %s  base %s  lots by %s  %s", styledText(title, cliStrong, color), styledText(string(v.Book.Env), map[bool]cliStyle{true: cliStrong, false: cliYellow}[v.Book.Env == portfolio.Real], color), v.Book.Base, v.Book.Method, v.Book.Status)
	if v.AsOf != "" {
		fmt.Fprintf(a.stdout, "  as of %s by %s time", v.AsOf, v.Basis)
	}
	fmt.Fprintf(a.stdout, "\n%d transactions\n\n", v.Txs)
	if len(v.Cash) == 0 {
		fmt.Fprintln(a.stdout, "no cash")
	} else {
		t := cliTable{Header: []string{"currency", "settled", "unsettled", "held for open orders", "available"}, Right: []bool{false, true, true, true, true}}
		for _, c := range v.Cash {
			t.Rows = append(t.Rows, []cliCell{styled(string(c.Currency), cliStrong), plain(c.Settled.String()), plain(c.Unsettled.String()), plain(c.Reserved.String()), plain(c.Available.String())})
		}
		t.write(a.stdout, color)
	}
	fmt.Fprintln(a.stdout)
	if len(v.Positions) == 0 {
		fmt.Fprintln(a.stdout, "no positions")
	} else {
		t := cliTable{Header: []string{"security", "shares", "cost basis", "lots", "name"}, Right: []bool{false, true, true, true}}
		for _, p := range v.Positions {
			var cost []string
			for _, c := range p.Cost {
				cost = append(cost, c.String())
			}
			t.Rows = append(t.Rows, []cliCell{styled(p.Security, cliStrong), plain(p.Qty.String()), plain(strings.Join(cost, " + ")), plain(fmt.Sprint(p.Lots)), plain(p.Name)})
		}
		t.write(a.stdout, color)
	}
	if *lots {
		fmt.Fprintln(a.stdout)
		t := cliTable{Header: []string{"lot", "security", "acquired", "shares", "cost"}, Right: []bool{false, false, false, true, true}}
		for _, pos := range l.Projection().Positions() {
			for _, lot := range l.Projection().OpenLots(pos.Security, l.Book.Method) {
				t.Rows = append(t.Rows, []cliCell{plain(lot.ID), styled(lot.Security, cliStrong), plain(lot.Acquired), plain(lot.Qty.String()), plain(amountText(lot.Cost, lot.Currency))})
			}
		}
		t.write(a.stdout, color)
	}
	for _, g := range v.Realized {
		fmt.Fprintf(a.stdout, "\nrealised gain on sales: %s (cost by %s; a display figure, not tax reporting)", g, v.Book.Method)
	}
	for _, h := range v.Holds {
		fmt.Fprintf(a.stdout, "\nopen order: %s", h)
	}
	if v.Balanced {
		fmt.Fprintln(a.stdout, "\n\nthe books balance in every currency")
	} else {
		fmt.Fprintln(a.stdout, "\n\nTHE BOOKS DO NOT BALANCE: run portfolio verify")
		return errReported
	}
	return nil
}

func portfolioJournal(ctx context.Context, a *app, args []string) error {
	fs := newFlags("portfolio journal")
	limit := fs.Int("limit", 0, "show only the newest N transactions")
	asJSON := fs.Bool("json", false, "print as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: portfolio journal BOOK [-limit N] [-json]")
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	l, err := ps.Ledger(ctx, book)
	if err != nil {
		return err
	}
	txs := l.Txs()
	if *limit > 0 && len(txs) > *limit {
		txs = txs[len(txs)-*limit:]
	}
	if *asJSON {
		type row struct {
			portfolio.Tx
			ReversedBy string `json:"reversed_by,omitempty"`
		}
		var out []row
		for _, t := range txs {
			by, _ := l.Projection().Reversed(t.ID)
			out = append(out, row{t, by})
		}
		return writeJSON(a.stdout, out)
	}
	if len(txs) == 0 {
		fmt.Fprintln(a.stdout, "the journal is empty")
		return nil
	}
	t := cliTable{Header: []string{"seq", "event", "kind", "source", "import id", "id", "effect", "note"}, Right: []bool{true}}
	for _, x := range txs {
		note := x.Memo
		if by, ok := l.Projection().Reversed(x.ID); ok {
			note = "reversed by " + by
		} else if x.Reverses != "" {
			note = "reverses " + x.Reverses
		}
		kind := styled(string(x.Kind), cliStrong)
		if x.Kind == portfolio.Reversal {
			kind = styled(string(x.Kind), cliYellow)
		}
		t.Rows = append(t.Rows, []cliCell{plain(fmt.Sprint(x.Seq)), plain(x.Event.Format("2006-01-02 15:04")), kind, plain(x.Source), plain(x.ImportID), styled(x.ID, cliDim), plain(txEffect(x)), plain(note)})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func portfolioVerify(ctx context.Context, a *app, args []string) error {
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		return usageErrorf("usage: portfolio verify [BOOK...]")
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	refs := args
	if len(refs) == 0 {
		books, err := ps.Books(ctx)
		if err != nil {
			return err
		}
		for _, b := range books {
			refs = append(refs, b.ID)
		}
	}
	if len(refs) == 0 {
		fmt.Fprintln(a.stdout, "no books to verify")
		return nil
	}
	t := cliTable{Header: []string{"book", "environment", "transactions", "lots", "result", "chain head"}, Right: []bool{false, false, true, true}}
	bad := 0
	for _, ref := range refs {
		rep, err := ps.Verify(ctx, ref)
		name := ref
		if rep.Book.Name != "" {
			name = rep.Book.Account + "/" + rep.Book.Name
		}
		if err != nil {
			bad++
			t.Rows = append(t.Rows, []cliCell{plain(name), envCell(rep.Book.Env), plain(fmt.Sprint(rep.Txs)), plain(fmt.Sprint(rep.Lots)), styled("FAILED: "+err.Error(), cliRed), plain("")})
			continue
		}
		t.Rows = append(t.Rows, []cliCell{plain(name), envCell(rep.Book.Env), plain(fmt.Sprint(rep.Txs)), plain(fmt.Sprint(rep.Lots)), styled("intact", cliGreen), styled(orDash(rep.Head), cliDim)})
	}
	t.write(a.stdout, useColor(a.stdout))
	if bad > 0 {
		return errors.New("a journal failed verification")
	}
	fmt.Fprintln(a.stdout, "\nEach journal replays under the ledger rules, balances in every currency, and its chain hashes and lot records match the rows.")
	return nil
}

// parseBookFlags reads a command that takes one book and some flags, the book
// either first or last.
func parseBookFlags(fs *flag.FlagSet, args []string) (string, error) {
	book := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		book, args = args[0], args[1:]
	}
	rest, err := parseFlags(fs, args)
	if err != nil {
		return "", err
	}
	if book == "" && len(rest) == 1 {
		book, rest = rest[0], nil
	}
	if len(rest) > 0 {
		return "", usageErrorf("unexpected argument %q", rest[0])
	}
	return book, nil
}

func portfolioReconcile(ctx context.Context, a *app, args []string) error {
	fs := newFlags("portfolio reconcile")
	file := fs.String("file", "", "CSV statement: kind (cash or position), security, qty, currency, amount")
	asOf := fs.String("as-of", "", "the moment the statement describes (a bare date means the end of that day)")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" || *file == "" || *asOf == "" {
		return usageErrorf("usage: portfolio reconcile BOOK -file STATEMENT.csv -as-of TIME")
	}
	when, err := portfolioAsOf(*asOf)
	if err != nil {
		return err
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := portfolio.ParseStatementCSV(f)
	if err != nil {
		return err
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	l, err := ps.LedgerAsOf(ctx, book, when, store.ByEvent)
	if err != nil {
		return err
	}
	diffs := portfolio.Reconcile(l.Projection(), st)
	if len(diffs) == 0 {
		fmt.Fprintf(a.stdout, "reconciled: the settled cash of %d currencies and %d positions match %s/%s as of %s\n", len(st.Cash), len(st.Positions), l.Book.Account, l.Book.Name, when.Format(time.RFC3339))
		fmt.Fprintln(a.stdout, "This shows the ledger agrees with this statement, not that the statement is right.")
		return nil
	}
	why := map[portfolio.DiffKind]string{
		portfolio.CashDiffers:      "settled cash differs: look for a missing or extra fee, deposit or settlement",
		portfolio.UnsettledPending: "equals the trades the ledger holds as unsettled: the statement settles them on another day",
		portfolio.QuantityDiffers:  "shares differ: look for a missed fill, split, transfer or reinvestment",
		portfolio.NotInLedger:      "the statement holds shares the ledger does not know",
		portfolio.NotInStatement:   "the ledger holds shares the statement does not list",
	}
	t := cliTable{Header: []string{"item", "ledger", "statement", "difference"}, Right: []bool{false, true, true}}
	for _, d := range diffs {
		st := cliRed
		if d.Kind == portfolio.UnsettledPending {
			st = cliYellow
		}
		t.Rows = append(t.Rows, []cliCell{styled(d.Item, cliStrong), plain(d.Ledger), plain(d.Statement), styled(why[d.Kind], st)})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintln(a.stdout, "\nNothing was changed. Answer a difference with a recorded transaction that names its source.")
	return fmt.Errorf("%d differences between the statement and the ledger", len(diffs))
}
