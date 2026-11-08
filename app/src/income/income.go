// Package income says what a book is entitled to from the payouts it holds
// shares for, and whether the ledger agrees. Entitlement is read from the
// book's own recorded fills, never from an order or an intention: an unfilled
// order earns nothing and a partial fill earns on the filled part.
//
// The convention is stated once. A share earns an ordinary payout if it was held
// on the trade date before the ex-date: a fill dated on or after the ex-date
// does not earn it, and a sale on or after the ex-date does not remove it. The
// ex-date is the one announced for the payout, not a fixed offset from the
// record date, because rules differ by venue and event. Special distributions,
// returns of capital and payouts of unknown type can carry due-bill or
// other entitlement terms that are not modelled here, so they are reported as
// unsupported, never computed.
package income

import (
	"fmt"
	"sort"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

// PayoutKey is the identity of a payout in the ledger.
func PayoutKey(k distribution.Key) string {
	return fmt.Sprintf("%s|%s|%s", k.SecurityID, k.Type, k.Period)
}

// SharesAt is the shares of a security held before the ex-date: every recorded
// movement dated before it, leaving out corrected (reversed) lines and the
// reversals themselves.
func SharesAt(l *portfolio.Ledger, security, exDay string) money.Decimal {
	p := l.Projection()
	var q money.Decimal
	for _, t := range l.Txs() {
		if t.Kind == portfolio.Reversal {
			continue
		}
		if _, gone := p.Reversed(t.ID); gone {
			continue
		}
		if t.Event.UTC().Format("2006-01-02") >= exDay {
			continue
		}
		for _, e := range t.Entries {
			if e.Security == security {
				if s, err := q.Add(e.Qty); err == nil {
					q = s
				}
			}
		}
	}
	return q
}

// Status of an expectation.
type Status string

const (
	// Entitled: the book holds shares for a declared ordinary payout; Gross is the expectation.
	Entitled Status = "entitled"
	// Pending: entitled, but the ex-date has not come, so nothing can be booked yet.
	Pending Status = "pending"
	// NotEntitled: no share was held before the ex-date.
	NotEntitled Status = "not_entitled"
	// NoIncome: the payout is an estimate or was cancelled; nothing accrues.
	NoIncome Status = "no_income"
	// Review: the sources disagree; nothing accrues until a person decides.
	Review Status = "review"
	// Unsupported: a payout type whose entitlement is not modelled.
	Unsupported Status = "unsupported"
)

// Expectation is what the ledger should hold for one payout.
type Expectation struct {
	Key      distribution.Key
	Ledger   string // the key in the ledger
	Status   Status
	Reason   string
	ExDay    string
	PayDay   string
	Shares   money.Decimal
	PerShare money.Decimal
	Currency money.Currency
	// Gross is the payout before tax, rounded half-even to a minor unit once, on
	// the total. Valid only when Status is Entitled or Pending.
	Gross money.Amount
}

// Expect works out one payout for one book. today is YYYY-MM-DD.
func Expect(l *portfolio.Ledger, p distribution.Payout, today string) Expectation {
	e := Expectation{Key: p.Key, Ledger: PayoutKey(p.Key), ExDay: p.ExDay, PayDay: p.PayDay, PerShare: p.Amount, Currency: p.Currency}
	switch p.State {
	case distribution.StateEstimated:
		e.Status, e.Reason = NoIncome, "only an estimate: nothing accrues until the payout is declared"
		return e
	case distribution.StateCancelled:
		e.Status, e.Reason = NoIncome, "cancelled: no income"
		return e
	case distribution.StateDeclared:
	default:
		e.Status, e.Reason = Review, "sources disagree about this payout: decide before anything is booked"
		return e
	}
	if p.Key.Type != distribution.Ordinary {
		e.Status = Unsupported
		e.Reason = fmt.Sprintf("a %s payout can carry due-bill or other entitlement terms that are not modelled: book it by hand once the broker pays", p.Key.Type)
		return e
	}
	e.Shares = SharesAt(l, p.Key.SecurityID, p.ExDay)
	if e.Shares.Sign() <= 0 {
		e.Status, e.Reason = NotEntitled, "no share was held before the ex-date"
		return e
	}
	scale := e.Shares.Scale() + p.Amount.Scale()
	if scale > money.MaxScale {
		scale = money.MaxScale
	}
	total, err := e.Shares.Mul(p.Amount, uint8(scale), money.Exact)
	if err != nil {
		e.Status, e.Reason = Review, "the payout amount cannot be computed exactly: "+err.Error()
		return e
	}
	if e.Gross, err = money.NewAmount(total, p.Currency, money.HalfEven); err != nil {
		e.Status, e.Reason = Review, err.Error()
		return e
	}
	e.Status = Entitled
	if today < p.ExDay {
		e.Status, e.Reason = Pending, "the ex-date has not come: expected, not yet bookable"
	}
	return e
}

// Kind of finding.
type Kind string

const (
	MissingAccrual   Kind = "missing_accrual"
	AccrualDiffers   Kind = "accrual_differs"
	AccrualUnbacked  Kind = "accrual_unbacked"
	PaymentOverdue   Kind = "payment_overdue"
	NotAPayoutOfBook Kind = "unknown_payout"
)

// Finding is a difference between what the book is entitled to and what the
// ledger holds. It is a question for a person; nothing is corrected here.
type Finding struct {
	Kind     Kind
	Payout   string
	Expected string
	Ledger   string
	Reason   string
}

func text(a money.Amount) string {
	if !a.Valid() {
		return "-"
	}
	return a.String()
}

func minor(v int64, c money.Currency) string {
	a, err := money.FromMinor(v, c)
	if err != nil {
		return fmt.Sprint(v)
	}
	return a.String()
}

// Check compares the expectations for the given payouts with the ledger's
// payout state and says every difference. A corrected fill that changes the
// entitlement shows up here as an accrual that differs.
func Check(l *portfolio.Ledger, payouts []distribution.Payout, today string) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, p := range payouts {
		e := Expect(l, p, today)
		seen[e.Ledger] = true
		info, booked := l.Projection().Payout(e.Ledger)
		switch e.Status {
		case Entitled:
			switch {
			case !booked:
				out = append(out, Finding{MissingAccrual, e.Ledger, text(e.Gross), "-", "entitled and past the ex-date, but nothing is booked"})
			case info.Gross != e.Gross.Minor() || info.Currency != e.Currency:
				out = append(out, Finding{AccrualDiffers, e.Ledger, text(e.Gross), minor(info.Gross, info.Currency),
					"the booked amount no longer matches the shares held before the ex-date (a corrected fill?): reverse it and book it again"})
			case info.AccrualTx != "" && info.PaidTx == "" && p.PayDay != "" && p.PayDay < today:
				out = append(out, Finding{PaymentOverdue, e.Ledger, text(e.Gross), "accrued", "the pay date has passed and no payment is recorded"})
			}
		case NotEntitled, NoIncome, Review, Unsupported:
			if booked {
				out = append(out, Finding{AccrualUnbacked, e.Ledger, "-", minor(info.Gross, info.Currency), "booked, but the book is not entitled to it now: " + e.Reason})
			}
		case Pending:
			if booked {
				out = append(out, Finding{AccrualUnbacked, e.Ledger, text(e.Gross), minor(info.Gross, info.Currency), "booked before the ex-date"})
			}
		}
	}
	for _, info := range l.Projection().Payouts() {
		if !seen[info.Key] {
			out = append(out, Finding{NotAPayoutOfBook, info.Key, "-", minor(info.Gross, info.Currency), "booked for a payout that is not in the observations given"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Payout < out[j].Payout })
	return out
}

// TaxBasis says how a net figure is known.
type TaxBasis string

const (
	// TaxUnknown: nothing is known about withholding; there is no net figure.
	TaxUnknown TaxBasis = "unknown"
	// TaxActual: the broker's payment was recorded; Net is what arrived.
	TaxActual TaxBasis = "actual"
	// TaxAssumed: a rate somebody supplied; a scenario, never booked.
	TaxAssumed TaxBasis = "assumed"
)

// Outlook is a gross figure with what is and is not known about the net.
type Outlook struct {
	Gross    money.Amount
	Basis    TaxBasis
	Withheld money.Amount // valid unless Basis is TaxUnknown
	Net      money.Amount // valid unless Basis is TaxUnknown
	Note     string
}

// OutlookFor gives the gross/net view of one payout. A recorded payment
// (actualWithheld non-nil) gives the actual net; otherwise an assumed rate, if
// one is given, gives a scenario labelled assumed; otherwise the net is unknown
// and no tax is guessed from the investor's locale.
func OutlookFor(gross money.Amount, actualWithheld *money.Amount, assumedRate *money.Decimal) Outlook {
	o := Outlook{Gross: gross, Basis: TaxUnknown, Note: "withholding is not known until the broker pays: no net figure is shown"}
	switch {
	case actualWithheld != nil:
		net, err := gross.Sub(*actualWithheld)
		if err == nil {
			o.Basis, o.Withheld, o.Net, o.Note = TaxActual, *actualWithheld, net, "the broker's recorded payment"
		}
	case assumedRate != nil:
		w, err := gross.Times(*assumedRate, money.HalfEven)
		if err == nil {
			if net, err := gross.Sub(w); err == nil {
				o.Basis, o.Withheld, o.Net, o.Note = TaxAssumed, w, net, "a scenario at the rate you supplied, not a tax estimate and never booked"
			}
		}
	}
	return o
}

// Withheld is the tax withheld on a paid payout, from the ledger's own payment.
func Withheld(l *portfolio.Ledger, key string) (money.Amount, bool) {
	info, ok := l.Projection().Payout(key)
	if !ok || info.PaidTx == "" {
		return money.Amount{}, false
	}
	t, ok := l.Projection().Tx(info.PaidTx)
	if !ok {
		return money.Amount{}, false
	}
	w, _ := money.FromMinor(0, info.Currency)
	for _, e := range t.Entries {
		if e.Account == "expense:withholding" {
			w, _ = w.Add(e.Amount)
		}
	}
	return w, true
}
