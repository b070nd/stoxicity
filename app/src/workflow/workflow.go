// Package workflow reads the daily workflow of one tenant from the stores: the
// plans, the books, the income, the orders and the policy in force. It is the
// one place that knows how those records are put together, so the command line,
// the exports and the terminal interface show the same facts.
//
// Reading never fetches anything and never sends anything. The only write is
// ConfirmOrder, which records a go-ahead through the same order service and the
// same tier gate as the order confirm command.
package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/income"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/ops"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/readiness"
	"github.com/b070nd/stoxicity/app/src/report"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/tier"
)

// Record kinds read here. They are written by the commands of the same names.
const (
	KindTierSignal  = "tier.signal"
	KindBrokerConf  = "broker.config"
	KindBrokerBind  = "broker.bind"
	confirmValidity = 15 * time.Minute
)

// Source is a tenant's stores and a clock.
type Source struct {
	DB     *store.DB
	Tenant string
	Now    func() time.Time
	// DataDir is the data folder. When it was restored from a backup and the
	// broker has not been reconciled since, new orders are refused (ops.Suspension).
	DataDir string
}

func (s Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Source) prep() {
	s.DB.SetClock(s.now) // recorded times and "now" are one clock
}

func (s Source) records() store.Records { s.prep(); return s.DB.Records(s.Tenant) }

// Day is YYYY-MM-DD of the clock, in UTC like the commands.
func (s Source) Day() string { return s.now().UTC().Format(time.DateOnly) }

// Securities are the instrument register by ID.
func (s Source) Securities(ctx context.Context) (map[string]instrument.Security, error) {
	set, err := s.DB.InstrumentsAsOf(ctx, time.Time{})
	if err != nil {
		return nil, err
	}
	m := map[string]instrument.Security{}
	for _, x := range set.Securities {
		m[x.ID] = x
	}
	return m, nil
}

// Signals are the latest health signals.
func (s Source) Signals(ctx context.Context) ([]tier.Signal, error) {
	rs := s.records()
	var out []tier.Signal
	for _, k := range tier.Kinds() {
		rec, err := rs.Latest(ctx, KindTierSignal, string(k))
		if errors.Is(err, store.ErrNoRecord) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var sg tier.Signal
		if err := json.Unmarshal(rec.Body, &sg); err != nil {
			return nil, fmt.Errorf("signal %s does not decode: %w", k, err)
		}
		out = append(out, sg)
	}
	return out, nil
}

// Tier is the tier of the active policy; 0 when none is active.
func (s Source) Tier(ctx context.Context) (int, bool, error) {
	rec, active, err := s.DB.Policies(s.Tenant).AsOf(ctx, time.Time{})
	if err != nil || !active {
		return 0, false, err
	}
	return rec.Policy.Tier, true, nil
}

// Gate is what new orders are checked against.
func (s Source) Gate(ctx context.Context) (*order.Gate, error) {
	t, _, err := s.Tier(ctx)
	if err != nil {
		return nil, err
	}
	sig, err := s.Signals(ctx)
	if err != nil {
		return nil, err
	}
	susp := tier.Suspended(sig)
	if r := ops.Suspension(s.DataDir); r != "" {
		susp = append(susp, r)
	}
	return &order.Gate{Tier: t, Broker: tier.PermissionsOf(t).BrokerSubmission, Suspended: susp}, nil
}

// OrderService is the order workflow with the limits of the active policy and
// the tier gate.
func (s Source) OrderService(ctx context.Context) (order.Service, error) {
	s.prep()
	svc := order.Service{Store: s.DB.Orders(s.Tenant)}
	var err error
	if svc.Gate, err = s.Gate(ctx); err != nil {
		return svc, err
	}
	rec, active, err := s.DB.Policies(s.Tenant).AsOf(ctx, time.Time{})
	if err != nil {
		return svc, err
	}
	if active {
		svc.Limits = order.Limits{MaxTradesPerMonth: rec.Policy.Preferences.MaxTradesPerMonth, MaxCostBurden: rec.Policy.Constraints.MaxCostBurden}
	}
	return svc, nil
}

// Books lists the books of the tenant.
func (s Source) Books(ctx context.Context) ([]report.BookRef, error) {
	s.prep()
	bs, err := s.DB.Portfolios(s.Tenant).Books(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]report.BookRef, 0, len(bs))
	for _, b := range bs {
		out = append(out, report.BookRef{ID: b.ID, Account: b.Account, Name: b.Name, Env: string(b.Env), Base: string(b.Base), Status: string(b.Status)})
	}
	return out, nil
}

// Fingerprint identifies the exact content of a ledger.
func Fingerprint(l *portfolio.Ledger) string {
	h := sha256.New()
	for _, tx := range l.Txs() {
		h.Write([]byte(l.Hash(tx.ID)))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func amount(minor int64, c money.Currency) string {
	v, err := money.FromMinor(minor, c)
	if err != nil {
		return fmt.Sprint(minor)
	}
	return v.String()
}

// Payouts are the resolved payouts of every security the book has held or
// ordered, as known now.
func (s Source) Payouts(ctx context.Context, l *portfolio.Ledger) ([]distribution.Payout, error) {
	seen := map[string]bool{}
	var ids []string
	for _, tx := range l.Txs() {
		for _, e := range tx.Entries {
			if e.Security != "" && !seen[e.Security] {
				seen[e.Security] = true
				ids = append(ids, e.Security)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Strings(ids)
	obs, err := s.DB.DistributionObservations(ctx, ids, time.Time{})
	if err != nil {
		return nil, err
	}
	return distribution.Resolve(obs, time.Time{}), nil
}

// BookReport is the report of one book.
func (s Source) BookReport(ctx context.Context, book, day string) (report.Report, error) {
	s.prep()
	if day == "" {
		day = s.Day()
	}
	ps := s.DB.Portfolios(s.Tenant)
	l, err := ps.Ledger(ctx, book)
	if err != nil {
		return report.Report{}, err
	}
	p := l.Projection()
	r := report.Report{Program: "stoxicity", AsOf: day, Book: l.Book.Account + "/" + l.Book.Name, Account: l.Book.Account, Env: string(l.Book.Env), Base: string(l.Book.Base), Ledger: Fingerprint(l)}
	r.Cash, r.Holdings, r.Reservations, r.Orders, r.Plans = []report.Cash{}, []report.Holding{}, []report.Reservation{}, []report.Order{}, []report.Plan{}
	for _, c := range p.Currencies() {
		if p.TrialBalance(c) != 0 {
			return report.Report{}, errors.New("the books do not balance, so no report is made (run portfolio verify)")
		}
		cash, un, res := p.Cash(c), p.Unsettled(c), p.ReservedCash(c, "")
		if cash != 0 || un != 0 || res != 0 {
			r.Cash = append(r.Cash, report.Cash{Currency: string(c), Settled: amount(cash, c), Unsettled: amount(un, c), Reserved: amount(res, c), Available: amount(p.Available(c), c)})
		}
	}
	for _, pos := range p.Positions() {
		var curs []string
		for c := range pos.Cost {
			curs = append(curs, string(c))
		}
		sort.Strings(curs)
		var cost []string
		for _, c := range curs {
			cost = append(cost, amount(pos.Cost[money.Currency(c)], money.Currency(c)))
		}
		r.Holdings = append(r.Holdings, report.Holding{Security: pos.Security, Qty: pos.Qty.String(), Cost: strings.Join(cost, " + "), Lots: pos.Lots})
	}
	for _, x := range p.Reservations() {
		h := report.Reservation{ID: x.ID, Intent: x.Intent}
		if x.Kind == portfolio.ReserveCash {
			h.Kind, h.What = "cash", amount(x.Amount, x.Currency)
		} else {
			h.Kind, h.What = "shares", x.Qty.String()+" "+x.Security
		}
		r.Reservations = append(r.Reservations, h)
	}

	pol := report.PolicyStatus{}
	if pol, err = s.PolicyStatus(ctx); err != nil {
		return r, err
	}
	r.Policy = "none active"
	if pol.Active {
		r.Tier, r.Policy = pol.Tier, fmt.Sprintf("epoch %d, hash %s", pol.Epoch, pol.Hash)
	}

	// the broker as a transport and a mode only
	rs := s.records()
	if rec, err := rs.Latest(ctx, KindBrokerConf, "default"); err == nil {
		var bc struct {
			Transport string `json:"transport"`
			Mode      string `json:"mode"`
		}
		if err := json.Unmarshal(rec.Body, &bc); err != nil {
			return r, fmt.Errorf("the broker configuration does not decode: %w", err)
		}
		r.Broker = report.Broker{Configured: true, Transport: bc.Transport, Mode: bc.Mode}
	} else if !errors.Is(err, store.ErrNoRecord) {
		return r, err
	}

	orders, err := s.OrderRows(ctx)
	if err != nil {
		return r, err
	}
	for _, o := range orders {
		if o.Book != l.Book.ID {
			continue
		}
		r.Orders = append(r.Orders, report.Order{ID: o.ID, State: o.State, Env: o.Env, Side: o.Side, Security: o.Security, Qty: o.Qty, Filled: o.Filled, Limit: o.Limit, Currency: o.Currency, Unknown: o.Unknown})
	}

	repo := plan.NewRepo(rs)
	rvs, err := repo.Plans(ctx)
	if err != nil {
		return r, err
	}
	for _, rv := range rvs {
		if rv.Card.Book != l.Book.ID {
			continue
		}
		st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
		if err != nil {
			return r, err
		}
		c := rv.Card
		eff := plan.Effective(st, c.Expiry, s.now())
		r.Plans = append(r.Plans, report.Plan{Revision: rv.ID(), Security: c.Security, Action: c.Action, State: string(eff), Expiry: c.Expiry, Data: c.DataState, Strategy: c.StrategyState, Execution: c.ExecutionEligibility, Reasons: nz(c.Reasons), Unknowns: nz(c.Unknowns), Manifest: rv.ManifestHash})
	}
	sort.Slice(r.Plans, func(i, j int) bool { return r.Plans[i].Revision < r.Plans[j].Revision })

	r.Income = report.Income{Payouts: []report.Payout{}, Findings: []string{}}
	payouts, err := s.Payouts(ctx, l)
	if err != nil {
		return r, err
	}
	for _, p := range payouts {
		e := income.Expect(l, p, day)
		gross := "-"
		if e.Gross.Valid() {
			gross = e.Gross.String()
		}
		r.Income.Payouts = append(r.Income.Payouts, report.Payout{Key: e.Ledger, Currency: string(e.Currency), Gross: gross, State: string(e.Status)})
	}
	for _, f := range income.Check(l, payouts, day) {
		r.Income.Findings = append(r.Income.Findings, fmt.Sprintf("%s: %s (expected %s, ledger %s)", f.Payout, f.Reason, f.Expected, f.Ledger))
	}
	r.Income.Note = "Payouts are gross, before any tax withheld at source."

	for _, c := range readiness.Inventory() {
		r.Capabilities = append(r.Capabilities, report.Capability{ID: c.ID, Name: c.Name,
			Implemented: string(c.Level(readiness.Implemented)), Fixture: string(c.Level(readiness.FixtureVerified)),
			Source: string(c.Level(readiness.SourceVerified)), Account: string(c.Level(readiness.AccountVerified)), Investment: string(c.Level(readiness.InvestmentEvaluated))})
	}
	r.Omitted = []string{
		"No article text, headline or snippet of any source: plans refer to evidence by identifier, so a source licence that forbids redistribution is respected.",
		"No broker address, certificate path, fixture path, account identifier from a broker, API key or token.",
		"No other book of this tenant, and no price or exchange-rate data: nothing was fetched to make this report.",
		"No valuation of the holdings: a value would need prices, and the report does not fetch them.",
	}
	return r, nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// PlanRows are the latest revision of every plan of the tenant, newest first.
func (s Source) PlanRows(ctx context.Context) ([]report.PlanRow, error) {
	repo := plan.NewRepo(s.records())
	rvs, err := repo.Plans(ctx)
	if err != nil {
		return nil, err
	}
	books, err := s.bookNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]report.PlanRow, 0, len(rvs))
	for _, rv := range rvs {
		st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
		if err != nil {
			return nil, err
		}
		c := rv.Card
		qty, cur := "", ""
		if c.Sizing != nil {
			qty, cur = c.Sizing.Qty, c.Sizing.Base
		}
		book := books[c.Book]
		if book == "" {
			book = c.Book
		}
		out = append(out, report.PlanRow{Revision: rv.ID(), Security: c.Security, Name: c.Name, Book: book, Sleeve: c.Sleeve, Action: c.Action,
			State: string(plan.Effective(st, c.Expiry, s.now())), Recorded: string(st), Qty: qty, Currency: cur, Expiry: c.Expiry, Data: c.DataState, Strategy: c.StrategyState,
			Execution: c.ExecutionEligibility, Unknowns: len(c.Unknowns), ReviewBy: c.ReviewBy})
	}
	return out, nil
}

func (s Source) bookNames(ctx context.Context) (map[string]string, error) {
	bs, err := s.Books(ctx)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, b := range bs {
		m[b.ID] = b.Ref()
	}
	return m, nil
}

// PlanDoc is one revision with its state and log; ref is pl_x or pl_x.rN.
func (s Source) PlanDoc(ctx context.Context, ref string) (report.PlanDoc, error) {
	id, n, err := plan.ParseRef(ref)
	if err != nil {
		return report.PlanDoc{}, err
	}
	repo := plan.NewRepo(s.records())
	rv, err := repo.Get(ctx, id, n)
	if err != nil {
		return report.PlanDoc{}, err
	}
	st, log, err := repo.State(ctx, rv.PlanID, rv.Rev)
	if err != nil {
		return report.PlanDoc{}, err
	}
	if log == nil {
		log = []plan.Event{}
	}
	return report.PlanDoc{Program: "stoxicity", Revision: rv, State: string(st), Effective: string(plan.Effective(st, rv.Card.Expiry, s.now())), Intact: rv.Verify() == nil, Log: log}, nil
}

// Revisions lists the revision numbers a plan has, oldest first.
func (s Source) Revisions(ctx context.Context, planID string) ([]int, error) {
	repo := plan.NewRepo(s.records())
	latest, err := repo.Get(ctx, planID, 0)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, latest.Rev)
	for i := 1; i <= latest.Rev; i++ {
		out = append(out, i)
	}
	return out, nil
}

// Income is the income of every book on the given day (empty: today).
func (s Source) Income(ctx context.Context, day string) (report.IncomeView, error) {
	s.prep()
	if day == "" {
		day = s.Day()
	}
	v := report.IncomeView{Day: day, Rows: []report.IncomeRow{}, Findings: []string{}}
	ps := s.DB.Portfolios(s.Tenant)
	bs, err := ps.Books(ctx)
	if err != nil {
		return v, err
	}
	for _, b := range bs {
		l, err := ps.Ledger(ctx, b.ID)
		if err != nil {
			return v, err
		}
		payouts, err := s.Payouts(ctx, l)
		if err != nil {
			return v, err
		}
		ref := b.Account + "/" + b.Name
		for _, p := range payouts {
			e := income.Expect(l, p, day)
			gross := "-"
			if e.Gross.Valid() {
				gross = e.Gross.String()
			}
			booked := "-"
			if st, ok := l.Projection().Payout(e.Ledger); ok {
				booked = amount(st.Gross, st.Currency)
				if st.PaidTx != "" {
					booked += " paid"
				} else {
					booked += " accrued"
				}
			}
			v.Rows = append(v.Rows, report.IncomeRow{Book: ref, Payout: e.Ledger, Security: p.Key.SecurityID, ExDay: e.ExDay, PayDay: e.PayDay, Status: string(e.Status), Reason: e.Reason,
				Shares: e.Shares.String(), PerShare: e.PerShare.String() + " " + string(e.Currency), Gross: gross, Booked: booked})
		}
		for _, f := range income.Check(l, payouts, day) {
			v.Findings = append(v.Findings, fmt.Sprintf("%s, %s: %s (expected %s, ledger %s)", ref, f.Payout, f.Reason, f.Expected, f.Ledger))
		}
	}
	sort.SliceStable(v.Rows, func(i, j int) bool {
		if v.Rows[i].ExDay != v.Rows[j].ExDay {
			return v.Rows[i].ExDay < v.Rows[j].ExDay
		}
		return v.Rows[i].Payout < v.Rows[j].Payout
	})
	return v, nil
}

// bound is the set of book IDs bound to a broker account.
func (s Source) bound(ctx context.Context) (map[string]bool, error) {
	recs, err := s.records().Current(ctx, KindBrokerBind, "")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range recs {
		var b struct {
			Book string `json:"book"`
		}
		if err := json.Unmarshal(r.Body, &b); err != nil {
			return nil, fmt.Errorf("binding %s does not decode: %w", r.Key, err)
		}
		out[b.Book] = true
		out[r.Key] = true
	}
	return out, nil
}

// OrderRows are the orders of the tenant, newest first.
func (s Source) OrderRows(ctx context.Context) ([]report.OrderRow, error) {
	svc, err := s.OrderService(ctx)
	if err != nil {
		return nil, err
	}
	views, err := svc.Views(ctx, "")
	if err != nil {
		return nil, err
	}
	bound, err := s.bound(ctx)
	if err != nil {
		return nil, err
	}
	names, err := s.bookNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]report.OrderRow, 0, len(views))
	for _, v := range views {
		t := v.Terms
		row := report.OrderRow{ID: v.ID, Book: t.Book, BookName: names[t.Book], Env: string(t.Env), State: string(v.State), Side: string(t.Side), Security: t.Security,
			Qty: t.Qty.String(), Filled: v.Filled.String(), Remaining: v.Remaining.String(), Limit: t.Limit.String(), Currency: string(t.Currency), Type: t.Type, TIF: t.TIF,
			FeeEstimate: amount(t.FeeEstimate, t.Currency), TermsHash: v.TermsHash, Reservation: v.Reservation, BrokerRef: v.BrokerRef,
			Warnings: nzs(v.Warnings), Why: nzs(v.Why), Late: nzs(v.Late), Created: v.Created.UTC().Format(time.RFC3339), Unknown: v.State == order.Unknown}
		if t.PlanID != "" {
			row.Plan = fmt.Sprintf("%s.r%d", t.PlanID, t.PlanRevision)
		}
		if !v.Expires.IsZero() {
			row.Expires = v.Expires.UTC().Format(time.RFC3339)
		}
		if c := v.Confirmation; c != nil {
			row.ConfirmedBy, row.ConfirmedAt = c.By, c.Expires.UTC().Format(time.RFC3339)
		}
		switch {
		case v.State != order.Draft && !(v.State == order.Confirmed && v.Confirmation != nil && s.now().After(v.Confirmation.Expires)):
			row.NotBecause = "only a draft or an expired confirmation can be confirmed"
		case !v.Expires.IsZero() && !s.now().Before(v.Expires):
			row.NotBecause = "the draft lapsed at " + v.Expires.UTC().Format(time.RFC3339) + ": run `order recover` to free what it holds, then draft it again"
		case bound[t.Book]:
			row.NotBecause = "this book is bound to a broker account: confirm with the order confirm command, which checks the broker's preview first"
		default:
			row.Confirmable = true
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out, nil
}

func nzs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// BookName turns a book ID into the way it is written on the command line.
func (s Source) BookName(ctx context.Context, id string) string {
	m, err := s.bookNames(ctx)
	if err != nil || m[id] == "" {
		return id
	}
	return m[id]
}

// PolicyStatus is the policy in force and what its activation changed.
func (s Source) PolicyStatus(ctx context.Context) (report.PolicyStatus, error) {
	rec, active, err := s.DB.Policies(s.Tenant).AsOf(ctx, time.Time{})
	if err != nil || !active {
		return report.PolicyStatus{RiskNotes: []string{}, Changes: []report.PolicyChange{}, Effects: []string{}}, err
	}
	p := report.PolicyStatus{Active: true, Epoch: rec.Epoch, Hash: rec.Hash, Tier: rec.Policy.Tier, At: rec.At.UTC().Format(time.RFC3339), RiskAccepted: rec.RiskAccepted,
		RiskNotes: nzs(rec.RiskNotes), Effects: nzs(rec.Effects), Changes: []report.PolicyChange{}}
	for _, c := range rec.Changes {
		p.Changes = append(p.Changes, report.PolicyChange{Path: c.Path, Old: c.Old, New: c.New})
	}
	return p, nil
}

// ConfirmOrder records the go-ahead for a draft the interface may confirm. typed
// must be the order's ID, written out: it is the person's act. The warnings the
// screen showed are acknowledged by that act. An order of a book bound to a
// broker is refused; it needs the broker's preview and the command line.
func (s Source) ConfirmOrder(ctx context.Context, id, typed, by string) error {
	if strings.TrimSpace(typed) != id {
		return fmt.Errorf("to confirm, type the order ID %s exactly", id)
	}
	if by == "" {
		return errors.New("a confirmation says who gives it")
	}
	rows, err := s.OrderRows(ctx)
	if err != nil {
		return err
	}
	for _, o := range rows {
		if o.ID != id {
			continue
		}
		if !o.Confirmable {
			return fmt.Errorf("%s cannot be confirmed here: %s", id, o.NotBecause)
		}
		svc, err := s.OrderService(ctx)
		if err != nil {
			return err
		}
		return svc.Confirm(ctx, o.Book, id, by, o.Warnings, confirmValidity)
	}
	return fmt.Errorf("no order %s", id)
}
