package portfolio

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Common is what every transaction says about itself.
type Common struct {
	Event    time.Time
	Source   string
	ImportID string
	Memo     string
}

func (c Common) tx(k Kind, meta map[string]string, es ...Entry) Tx {
	return Tx{Kind: k, Event: c.Event, Source: c.Source, ImportID: c.ImportID, Memo: c.Memo, Entries: es, Meta: meta}
}

func amt(minor int64, cur money.Currency) money.Amount {
	a, _ := money.FromMinor(minor, cur)
	return a
}

func plus(e ...Entry) []Entry { return e }

func positive(a money.Amount, what string) error {
	if !a.Valid() || a.Sign() <= 0 {
		return fmt.Errorf("portfolio: %s must be a positive amount with a currency", what)
	}
	return nil
}

// Deposit puts cash into the book from outside.
func (l *Ledger) Deposit(c Common, a money.Amount) (Tx, error) {
	if err := positive(a, "a deposit"); err != nil {
		return Tx{}, err
	}
	n, _ := a.Neg()
	return c.tx(Deposit, nil, Entry{Account: "cash", Amount: a}, Entry{Account: "equity:contributions", Amount: n}), nil
}

// Withdraw takes cash out of the book.
func (l *Ledger) Withdraw(c Common, a money.Amount) (Tx, error) {
	if err := positive(a, "a withdrawal"); err != nil {
		return Tx{}, err
	}
	n, _ := a.Neg()
	return c.tx(Withdrawal, nil, Entry{Account: "cash", Amount: n}, Entry{Account: "equity:contributions", Amount: a}), nil
}

// Trade says what a buy or sell was.
type Trade struct {
	Common
	Security string
	Qty      money.Decimal
	// Gross is the consideration, before commission, in the currency traded.
	Gross money.Amount
	// Commission is paid from cash at once; the zero Amount means none.
	Commission money.Amount
	SettleDay  string
	// Price is recorded as stated by the broker; it is not used for arithmetic.
	Price string
	// Releases names the reservation this fill consumes.
	Releases string
}

func (t Trade) meta() map[string]string {
	m := map[string]string{}
	if t.SettleDay != "" {
		m["settle_day"] = t.SettleDay
	}
	if t.Price != "" {
		m["price"] = t.Price
	}
	return m
}

func (t Trade) check() error {
	if t.Security == "" {
		return errors.New("portfolio: a trade needs a security")
	}
	if t.Qty.Sign() <= 0 || t.Qty.Scale() > MaxQtyScale {
		return fmt.Errorf("portfolio: trade quantity %s must be positive with at most %d digits", t.Qty, MaxQtyScale)
	}
	if err := positive(t.Gross, "the gross amount"); err != nil {
		return err
	}
	if t.Commission.Valid() {
		if t.Commission.Sign() < 0 {
			return errors.New("portfolio: commission cannot be negative")
		}
		if t.Commission.Currency() != t.Gross.Currency() {
			return errors.New("portfolio: commission in another currency is a separate fee transaction")
		}
	}
	if t.SettleDay != "" && !validDay(t.SettleDay) {
		return fmt.Errorf("portfolio: settle day %q is not YYYY-MM-DD", t.SettleDay)
	}
	return nil
}

func commissionEntries(t Trade) []Entry {
	if !t.Commission.Valid() || t.Commission.Sign() == 0 {
		return nil
	}
	n, _ := t.Commission.Neg()
	return plus(Entry{Account: "expense:commission", Amount: t.Commission}, Entry{Account: "cash", Amount: n})
}

// Buy records shares bought. The cost is unsettled until Settle.
func (l *Ledger) Buy(t Trade) (Tx, error) {
	if err := t.check(); err != nil {
		return Tx{}, err
	}
	n, _ := t.Gross.Neg()
	es := plus(
		Entry{Account: SecurityAccount(t.Security), Security: t.Security, Amount: t.Gross, Qty: t.Qty, NewLot: true, Acquired: t.Event.UTC().Format(time.DateOnly)},
		Entry{Account: "unsettled", Amount: n},
	)
	tx := t.Common.tx(Buy, t.meta(), append(es, commissionEntries(t)...)...)
	tx.Releases = t.Releases
	return tx, nil
}

// consume picks lots for qty of a security by the book's method and returns the
// entries that remove them, with the total cost removed.
func (l *Ledger) consume(security string, qty money.Decimal, cur money.Currency, releases string) ([]Entry, int64, error) {
	held := l.p.Held(security)
	free, err := held.Sub(l.p.ReservedShares(security, releases))
	if err != nil {
		return nil, 0, err
	}
	if free.Cmp(qty) < 0 {
		return nil, 0, fmt.Errorf("%w: %s asked %s, %s held and %s free of open orders", ErrOversell, security, qty, held, free)
	}
	need := qty
	var es []Entry
	var total int64
	for _, lot := range l.p.OpenLots(security, l.Book.Method) {
		if need.Sign() == 0 {
			break
		}
		if lot.Currency != cur {
			return nil, 0, fmt.Errorf("portfolio: lot %s is held at cost in %s and this is in %s: convert with explicit FX legs first", lot.ID, lot.Currency, cur)
		}
		take := need
		if lot.Qty.Cmp(take) < 0 {
			take = lot.Qty
		}
		cost, err := partialCost(&lot, take)
		if err != nil {
			return nil, 0, err
		}
		nq, _ := take.Neg()
		es = append(es, Entry{Account: SecurityAccount(security), Security: security, Amount: amt(-cost, cur), Qty: nq, Lot: lot.ID})
		total += cost
		if need, err = need.Sub(take); err != nil {
			return nil, 0, err
		}
	}
	if need.Sign() != 0 {
		return nil, 0, fmt.Errorf("%w: %s short by %s", ErrOversell, security, need)
	}
	return es, total, nil
}

// Sell records shares sold. Lots are consumed by the book's method and the cost,
// proceeds and realised gain are written out in full.
func (l *Ledger) Sell(t Trade) (Tx, error) {
	if err := t.check(); err != nil {
		return Tx{}, err
	}
	cur := t.Gross.Currency()
	es, cost, err := l.consume(t.Security, t.Qty, cur, t.Releases)
	if err != nil {
		return Tx{}, err
	}
	gain := t.Gross.Minor() - cost
	es = append(es,
		Entry{Account: "unsettled", Amount: t.Gross},
		Entry{Account: "gain:realized", Amount: amt(-gain, cur)},
	)
	m := t.meta()
	m["method"] = string(l.Book.Method)
	tx := t.Common.tx(Sell, m, append(es, commissionEntries(t)...)...)
	tx.Releases = t.Releases
	return tx, nil
}

// SettleTrade moves the unsettled amount of a trade into cash.
func (l *Ledger) SettleTrade(c Common, tradeID string) (Tx, error) {
	t, ok := l.p.Tx(tradeID)
	if !ok {
		return Tx{}, fmt.Errorf("portfolio: %q is not a transaction of this book", tradeID)
	}
	due := map[money.Currency]int64{}
	for _, e := range t.Entries {
		if e.Account == "unsettled" {
			due[e.Amount.Currency()] += e.Amount.Minor()
		}
	}
	var curs []string
	for cur, d := range due {
		if d != 0 {
			curs = append(curs, string(cur))
		}
	}
	sort.Strings(curs)
	var es []Entry
	for _, cs := range curs {
		cur := money.Currency(cs)
		es = append(es, Entry{Account: "unsettled", Amount: amt(-due[cur], cur)}, Entry{Account: "cash", Amount: amt(due[cur], cur)})
	}
	if len(es) == 0 {
		return Tx{}, fmt.Errorf("portfolio: %s has nothing unsettled", tradeID)
	}
	return c.tx(Settle, map[string]string{"settle_of": tradeID}, es...), nil
}

// Fee charges a fee or a stand-alone commission against cash.
func (l *Ledger) Fee(c Common, a money.Amount, commission bool) (Tx, error) {
	if err := positive(a, "a fee"); err != nil {
		return Tx{}, err
	}
	acct := "expense:fee"
	if commission {
		acct = "expense:commission"
	}
	n, _ := a.Neg()
	return c.tx(Fee, nil, Entry{Account: acct, Amount: a}, Entry{Account: "cash", Amount: n}), nil
}

// Income records a dividend, interest or other income paid in cash, with the tax
// withheld at source. Gross is the declared amount; the cash received is gross
// less withholding. Withholding may be the zero Amount.
func (l *Ledger) Income(c Common, kind string, gross, withheld money.Amount) (Tx, error) {
	acct := map[string]string{"dividend": "income:dividend", "interest": "income:interest", "other": "income:other"}[kind]
	if acct == "" {
		return Tx{}, fmt.Errorf("portfolio: unknown income kind %q (dividend, interest, other)", kind)
	}
	if err := positive(gross, "gross income"); err != nil {
		return Tx{}, err
	}
	w := int64(0)
	if withheld.Valid() {
		if withheld.Currency() != gross.Currency() || withheld.Sign() < 0 || withheld.Minor() >= gross.Minor() {
			return Tx{}, errors.New("portfolio: withholding must be in the same currency and less than the gross amount")
		}
		w = withheld.Minor()
	}
	cur := gross.Currency()
	es := plus(Entry{Account: "cash", Amount: amt(gross.Minor()-w, cur)}, Entry{Account: acct, Amount: amt(-gross.Minor(), cur)})
	if w > 0 {
		es = append(es, Entry{Account: "expense:withholding", Amount: amt(w, cur)})
	}
	return c.tx(Income, map[string]string{"kind": kind}, es...), nil
}

// Reinvest records a dividend that bought more shares of the same security
// without passing through cash. Qty is the shares received; the cost of the new
// lot is the gross dividend less withholding.
func (l *Ledger) Reinvest(c Common, security string, qty money.Decimal, gross, withheld money.Amount) (Tx, error) {
	if err := positive(gross, "gross income"); err != nil {
		return Tx{}, err
	}
	if security == "" || qty.Sign() <= 0 || qty.Scale() > MaxQtyScale {
		return Tx{}, errors.New("portfolio: a reinvestment needs a security and a positive quantity")
	}
	cur := gross.Currency()
	w := int64(0)
	if withheld.Valid() {
		if withheld.Currency() != cur || withheld.Sign() < 0 || withheld.Minor() >= gross.Minor() {
			return Tx{}, errors.New("portfolio: withholding must be in the same currency and less than the gross amount")
		}
		w = withheld.Minor()
	}
	es := plus(
		Entry{Account: SecurityAccount(security), Security: security, Amount: amt(gross.Minor()-w, cur), Qty: qty, NewLot: true, Acquired: c.Event.UTC().Format(time.DateOnly)},
		Entry{Account: "income:dividend", Amount: amt(-gross.Minor(), cur)},
	)
	if w > 0 {
		es = append(es, Entry{Account: "expense:withholding", Amount: amt(w, cur)})
	}
	return c.tx(DRIP, map[string]string{"kind": "dividend"}, es...), nil
}

// Convert records a currency conversion as two legs through the clearing account,
// so each currency balances on its own and the native amounts are kept. rate is
// the broker's stated rate, kept as text. fee may be the zero Amount.
func (l *Ledger) Convert(c Common, from, to money.Amount, fee money.Amount, rate string) (Tx, error) {
	if err := positive(from, "the amount sold"); err != nil {
		return Tx{}, err
	}
	if err := positive(to, "the amount bought"); err != nil {
		return Tx{}, err
	}
	if from.Currency() == to.Currency() {
		return Tx{}, errors.New("portfolio: a conversion needs two currencies")
	}
	nf, _ := from.Neg()
	nt, _ := to.Neg()
	es := plus(
		Entry{Account: "cash", Amount: nf}, Entry{Account: "fx:clearing", Amount: from},
		Entry{Account: "cash", Amount: to}, Entry{Account: "fx:clearing", Amount: nt},
	)
	if fee.Valid() && fee.Sign() != 0 {
		if fee.Sign() < 0 {
			return Tx{}, errors.New("portfolio: a fee cannot be negative")
		}
		nfee, _ := fee.Neg()
		es = append(es, Entry{Account: "expense:fee", Amount: fee}, Entry{Account: "cash", Amount: nfee})
	}
	m := map[string]string{"from": from.String(), "to": to.String()}
	if rate != "" {
		m["rate"] = rate
	}
	return c.tx(FX, m, es...), nil
}

// SplitShares records a share split: every open lot of the security is
// multiplied by to/from with its cost unchanged. A split that would leave a
// quantity that cannot be written exactly (beyond eight digits) is refused:
// cash in lieu is not modelled.
func (l *Ledger) SplitShares(c Common, security string, from, to int64) (Tx, error) {
	if from < 1 || to < 1 || from == to {
		return Tx{}, errors.New("portfolio: a split needs positive, different from and to")
	}
	var es []Entry
	for _, lot := range l.p.OpenLots(security, FIFO) {
		if lot.Opened.After(c.Event) {
			continue
		}
		scaled, err := lot.Qty.Mul(money.Int(to), uint8(lot.Qty.Scale()), money.Exact)
		if err != nil {
			return Tx{}, err
		}
		nq, err := scaled.Div(money.Int(from), MaxQtyScale, money.Exact)
		if err != nil {
			return Tx{}, fmt.Errorf("portfolio: lot %s of %s shares does not divide exactly into a %d-for-%d split: cash in lieu is not modelled", lot.ID, lot.Qty, to, from)
		}
		delta, err := nq.Sub(lot.Qty)
		if err != nil {
			return Tx{}, err
		}
		delta = trimScale(delta)
		es = append(es, Entry{Account: SecurityAccount(security), Security: security, Amount: amt(0, lot.Currency), Qty: delta, Lot: lot.ID})
	}
	if len(es) == 0 {
		return Tx{}, fmt.Errorf("portfolio: no shares of %s are held to split", security)
	}
	return c.tx(Split, map[string]string{"ratio": fmt.Sprintf("%d-for-%d", to, from)}, es...), nil
}

// Reverse corrects a transaction by posting its exact opposite. The original is
// never touched.
func (l *Ledger) Reverse(c Common, txID string) (Tx, error) {
	o, ok := l.p.Tx(txID)
	if !ok {
		return Tx{}, fmt.Errorf("portfolio: %q is not a transaction of this book", txID)
	}
	es := make([]Entry, len(o.Entries))
	for i, e := range o.Entries {
		n, err := e.Amount.Neg()
		if err != nil {
			return Tx{}, err
		}
		q, _ := e.Qty.Neg()
		es[i] = Entry{Account: e.Account, Amount: n, Security: e.Security, Qty: q, Lot: e.Lot}
	}
	tx := c.tx(Reversal, map[string]string{"reverses_kind": string(o.Kind)}, es...)
	tx.Reverses = txID
	return tx, nil
}

// ReserveCash holds cash for an open order.
func (l *Ledger) ReserveCash(c Common, id string, a money.Amount, intent string) (Tx, error) {
	if err := positive(a, "a cash reservation"); err != nil {
		return Tx{}, err
	}
	tx := c.tx(Reserve, nil)
	tx.Reservation = &Reservation{ID: id, Kind: ReserveCash, Currency: a.Currency(), Amount: a.Minor(), Intent: intent}
	return tx, nil
}

// ReserveSharesFor holds shares for an open sell order.
func (l *Ledger) ReserveSharesFor(c Common, id, security string, qty money.Decimal, intent string) Tx {
	tx := c.tx(Reserve, nil)
	tx.Reservation = &Reservation{ID: id, Kind: ReserveShares, Security: security, Qty: qty, Intent: intent}
	return tx
}

// ReleaseReservation gives a hold back.
func (l *Ledger) ReleaseReservation(c Common, id string) Tx {
	tx := c.tx(Release, nil)
	tx.Releases = id
	return tx
}

// TransferArgs says what moves between two books. Exactly one of Cash or
// Security is set.
type TransferArgs struct {
	Common
	Cash     money.Amount
	Security string
	Qty      money.Decimal
}

// BuildTransfer builds the two legs of a transfer between books of the same
// environment. Real and paper books never exchange anything.
func BuildTransfer(from, to *Ledger, a TransferArgs) (out, in Tx, err error) {
	if from.Book.ID == to.Book.ID {
		return out, in, errors.New("portfolio: a transfer needs two different books")
	}
	if from.Book.Env != to.Book.Env {
		return out, in, fmt.Errorf("portfolio: a %s book and a %s book never exchange cash or shares", from.Book.Env, to.Book.Env)
	}
	if from.Book.Status == Archived || to.Book.Status == Archived {
		return out, in, ErrArchived
	}
	if a.Cash.Valid() == (a.Security != "") {
		return out, in, errors.New("portfolio: a transfer moves cash or shares, one of the two")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", from.Book.ID, to.Book.ID, a.ImportID, a.Event.UnixMilli())))
	tid := hex.EncodeToString(sum[:6])
	meta := map[string]string{"transfer_id": tid, "from_book": from.Book.ID, "to_book": to.Book.ID}
	outC, inC := a.Common, a.Common
	if a.ImportID != "" {
		outC.ImportID, inC.ImportID = a.ImportID+":out", a.ImportID+":in"
	}
	if a.Cash.Valid() {
		if err = positive(a.Cash, "a transfer"); err != nil {
			return
		}
		n, _ := a.Cash.Neg()
		out = outC.tx(TransferOut, meta, Entry{Account: "cash", Amount: n}, Entry{Account: "equity:transfers", Amount: a.Cash})
		in = inC.tx(TransferIn, meta, Entry{Account: "cash", Amount: a.Cash}, Entry{Account: "equity:transfers", Amount: n})
		return
	}
	if a.Qty.Sign() <= 0 || a.Qty.Scale() > MaxQtyScale {
		return out, in, errors.New("portfolio: a share transfer needs a positive quantity")
	}
	lots := from.p.OpenLots(a.Security, from.Book.Method)
	if len(lots) == 0 {
		return out, in, fmt.Errorf("%w: %s", ErrOversell, a.Security)
	}
	need := a.Qty
	var outE, inE []Entry
	costByCur := map[money.Currency]int64{}
	free, _ := from.p.Held(a.Security).Sub(from.p.ReservedShares(a.Security, ""))
	if free.Cmp(a.Qty) < 0 {
		return out, in, fmt.Errorf("%w: %s asked %s, %s free", ErrOversell, a.Security, a.Qty, free)
	}
	for _, lot := range lots {
		if need.Sign() == 0 {
			break
		}
		take := need
		if lot.Qty.Cmp(take) < 0 {
			take = lot.Qty
		}
		cost, cerr := partialCost(&lot, take)
		if cerr != nil {
			return out, in, cerr
		}
		nq, _ := take.Neg()
		outE = append(outE, Entry{Account: SecurityAccount(a.Security), Security: a.Security, Amount: amt(-cost, lot.Currency), Qty: nq, Lot: lot.ID})
		inE = append(inE, Entry{Account: SecurityAccount(a.Security), Security: a.Security, Amount: amt(cost, lot.Currency), Qty: take, NewLot: true, Acquired: lot.Acquired})
		costByCur[lot.Currency] += cost
		if need, err = need.Sub(take); err != nil {
			return
		}
	}
	var curs []string
	for c := range costByCur {
		curs = append(curs, string(c))
	}
	sort.Strings(curs)
	for _, cs := range curs {
		c := money.Currency(cs)
		outE = append(outE, Entry{Account: "equity:transfers", Amount: amt(costByCur[c], c)})
		inE = append(inE, Entry{Account: "equity:transfers", Amount: amt(-costByCur[c], c)})
	}
	return outC.tx(TransferOut, meta, outE...), inC.tx(TransferIn, meta, inE...), nil
}

// PostTransfer posts both legs or neither: each is checked against its book
// before either is recorded.
func PostTransfer(from, to *Ledger, a TransferArgs, recorded time.Time) (Posted, Posted, error) {
	out, in, err := BuildTransfer(from, to, a)
	if err != nil {
		return Posted{}, Posted{}, err
	}
	if out.ImportID == "" || from.imports[out.ImportID] == "" {
		if err := from.Check(out); err != nil {
			return Posted{}, Posted{}, fmt.Errorf("transfer out: %w", err)
		}
	}
	if in.ImportID == "" || to.imports[in.ImportID] == "" {
		if err := to.Check(in); err != nil {
			return Posted{}, Posted{}, fmt.Errorf("transfer in: %w", err)
		}
	}
	po, err := from.Post(out, recorded)
	if err != nil {
		return Posted{}, Posted{}, err
	}
	pi, err := to.Post(in, recorded)
	if err != nil {
		return po, Posted{}, err
	}
	return po, pi, nil
}

// trimScale drops trailing zero digits so 30.00000000 is kept as 30.
func trimScale(d money.Decimal) money.Decimal {
	for d.Scale() > 0 {
		r, err := d.Rescale(uint8(d.Scale()-1), money.Exact)
		if err != nil {
			break
		}
		d = r
	}
	return d
}
