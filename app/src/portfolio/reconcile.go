package portfolio

import (
	"fmt"
	"io"
	"sort"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/money"
)

// Statement is what a broker or custodian says is held: settled cash by currency
// and shares by security, as of one moment. It is read, never believed: it is
// compared with the ledger and every difference is reported.
type Statement struct {
	Cash      map[money.Currency]int64
	Positions map[string]money.Decimal
}

// DiffKind says how the statement and the ledger disagree.
type DiffKind string

const (
	CashDiffers      DiffKind = "cash_differs"
	QuantityDiffers  DiffKind = "quantity_differs"
	NotInLedger      DiffKind = "not_in_ledger"
	NotInStatement   DiffKind = "not_in_statement"
	UnsettledPending DiffKind = "unsettled_pending"
)

// Difference is one item that does not match.
type Difference struct {
	Kind      DiffKind
	Item      string
	Ledger    string
	Statement string
}

// Reconcile compares a statement with a projection. It changes nothing and
// proposes no correction: a difference is a question for a person, answered by a
// recorded transaction (a missed fee, a reversal, a split) with its own source.
// Cash that the ledger holds as unsettled is not settled cash; when it explains
// a difference it is said so rather than silently added.
func Reconcile(p *Projection, st Statement) []Difference {
	var out []Difference
	curs := map[money.Currency]bool{}
	for _, c := range p.Currencies() {
		if p.Cash(c) != 0 {
			curs[c] = true
		}
	}
	for c := range st.Cash {
		curs[c] = true
	}
	sorted := make([]string, 0, len(curs))
	for c := range curs {
		sorted = append(sorted, string(c))
	}
	sort.Strings(sorted)
	text := func(v int64, c money.Currency) string {
		a, err := money.FromMinor(v, c)
		if err != nil {
			return fmt.Sprint(v)
		}
		return a.String()
	}
	for _, cs := range sorted {
		c := money.Currency(cs)
		l, s := p.Cash(c), st.Cash[c]
		if l == s {
			continue
		}
		kind := CashDiffers
		if u := p.Unsettled(c); u != 0 && l+u == s {
			kind = UnsettledPending
		}
		out = append(out, Difference{Kind: kind, Item: "cash " + cs, Ledger: text(l, c), Statement: text(s, c)})
	}
	held := map[string]money.Decimal{}
	for _, pos := range p.Positions() {
		held[pos.Security] = pos.Qty
	}
	secs := map[string]bool{}
	for s := range held {
		secs[s] = true
	}
	for s := range st.Positions {
		secs[s] = true
	}
	names := make([]string, 0, len(secs))
	for s := range secs {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		l, inL := held[s]
		q, inS := st.Positions[s]
		switch {
		case inL && !inS:
			out = append(out, Difference{Kind: NotInStatement, Item: s, Ledger: l.String(), Statement: "-"})
		case !inL && inS:
			out = append(out, Difference{Kind: NotInLedger, Item: s, Ledger: "-", Statement: q.String()})
		case l.Cmp(q) != 0:
			out = append(out, Difference{Kind: QuantityDiffers, Item: s, Ledger: l.String(), Statement: q.String()})
		}
	}
	return out
}

// ParseStatementCSV reads a statement: one line per currency of settled cash
// (kind cash, currency, amount) and one per security held (kind position,
// security, qty). A line that fills a column its kind does not use is refused.
func ParseStatementCSV(r io.Reader) (Statement, error) {
	rows, err := csvtable.Read(r, []string{"kind"}, []string{"security", "qty", "currency", "amount"})
	if err != nil {
		return Statement{}, err
	}
	st := Statement{Cash: map[money.Currency]int64{}, Positions: map[string]money.Decimal{}}
	for _, row := range rows {
		at := "line " + row["_line"]
		switch row["kind"] {
		case "cash":
			if row["security"] != "" || row["qty"] != "" {
				return Statement{}, fmt.Errorf("%s: a cash line has a currency and an amount, not a security or quantity", at)
			}
			c, err := money.ParseCurrency(row["currency"])
			if err != nil {
				return Statement{}, fmt.Errorf("%s: %w", at, err)
			}
			d, err := money.ParseDecimal(row["amount"])
			if err != nil {
				return Statement{}, fmt.Errorf("%s: amount: %w", at, err)
			}
			a, err := money.NewAmount(d, c, money.Exact)
			if err != nil {
				return Statement{}, fmt.Errorf("%s: %w", at, err)
			}
			if _, dup := st.Cash[c]; dup {
				return Statement{}, fmt.Errorf("%s: cash in %s appears twice", at, c)
			}
			st.Cash[c] = a.Minor()
		case "position":
			if row["currency"] != "" || row["amount"] != "" {
				return Statement{}, fmt.Errorf("%s: a position line has a security and a quantity, not a currency or amount", at)
			}
			if !ValidAccount(SecurityAccount(row["security"])) {
				return Statement{}, fmt.Errorf("%s: %q is not a valid security ID", at, row["security"])
			}
			q, err := money.ParseDecimal(row["qty"])
			if err != nil || q.Sign() <= 0 || q.Scale() > MaxQtyScale {
				return Statement{}, fmt.Errorf("%s: quantity %q must be positive with at most %d digits", at, row["qty"], MaxQtyScale)
			}
			if _, dup := st.Positions[row["security"]]; dup {
				return Statement{}, fmt.Errorf("%s: %s appears twice", at, row["security"])
			}
			st.Positions[row["security"]] = q
		default:
			return Statement{}, fmt.Errorf("%s: kind %q must be cash or position", at, row["kind"])
		}
	}
	return st, nil
}
