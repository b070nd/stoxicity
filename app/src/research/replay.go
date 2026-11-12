package research

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/paper"
	"github.com/b070nd/stoxicity/app/src/playbook"
	"github.com/b070nd/stoxicity/app/src/walkfwd"
)

const (
	// barScale is how many digits after the point a price keeps when a bar or
	// a limit is turned into a decimal; six keeps the cents of a price that was
	// adjusted for a split.
	barScale = 6
	// barMarginDays is the room, in calendar days, given on top of what a
	// replay needs: for holidays between the exit and the sessions counted, and
	// for the next open after the last session held.
	barMarginDays = 14
	// referenceMarginDays is how many calendar days before the earliest
	// decision bars are read, beyond the study's longest allowed gap, so that a
	// close a long weekend before the decision is still found.
	referenceMarginDays = 7
)

// ReplayInput is a playbook's signals to be turned into proposals for the
// replay of walkfwd.
type ReplayInput struct {
	Playbook playbook.Playbook
	// Signals are the signals of this playbook revision, as research.Signals
	// gives them. They are not recomputed here.
	Signals []playbook.Signal
	// Spec is the study the signals read (the Report.Spec of the run they were
	// made from). Its session zone and entry delay decide the day an event can
	// be acted on, and its longest entry gap how old a close may be.
	Spec eventstudy.Spec
	// Trial is the registered trial the proposals are attributed to.
	Trial string
	// Fund is the diversified fund the replay compares with; empty for none.
	// Its bars are returned with the others.
	Fund string
	// Currency is used for a security whose bars state none; empty to refuse
	// such a security.
	Currency string
	// Known reads the candles and splits as they were held then; zero reads
	// them as they stand.
	Known time.Time
}

// Skipped says why a buy became a decision not to trade: a trade that cannot
// be sized or priced is kept as a decision not to trade, not dropped, so that
// coverage is not computed on the trades that were easy.
type Skipped struct {
	Signal   string
	Security string
	Reason   string
}

// Replay is what ReplayProposals gives to walkfwd.
type Replay struct {
	// Proposals has one per signal, in the order of the signals. A buy with a
	// price and a size has a quantity; every other proposal has a quantity of
	// zero, which walkfwd keeps as a decision not to trade.
	Proposals []walkfwd.Proposal
	// Trades, NoTrades and Skipped add up to the proposals: Trades have a
	// quantity, NoTrades are watch and sell signals, Skipped are buys that could
	// not be priced.
	Trades, NoTrades int
	Skipped          []Skipped
	// Bars are the daily bars of the securities that are traded and of the fund,
	// oldest first within a security, securities in alphabetical order.
	//
	// They are split-consistent on the basis of the newest bar: the right basis
	// for a replay, in which a limit, a stop, a quantity and a volume are all in
	// one unit, and not the price as traded on the day. Quantities are whole
	// shares of that basis.
	Bars []paper.Bar
	// DroppedBars counts the bars left out because they could not be a bar:
	// a price that does not fit, or an open or close outside the day's range.
	DroppedBars int
	// FundProblem says why the fund has no bars when one was asked for and
	// there are trades; empty otherwise.
	FundProblem string
	// From and To are the span the bars were read for, YYYY-MM-DD; empty when
	// there was no buy to read them for.
	From, To string
}

// ReplayProposals turns signals into proposals for the replay of walkfwd.
//
// Every signal gives one proposal, with the ID of the signal. A watch or sell
// signal is a decision not to trade (a sell is never a short). A buy takes the
// size and prices of its rule's trade stanza, and every number is derived from
// bars that were finished before the entry session:
//
//   - Decided and KnownOn are the same day, the day the event became usable:
//     the day of the time it became available plus the study's entry delay, in
//     the exchange's zone or in UTC, whichever is later. UTC is taken too
//     because an event known only to the day is stored at midnight UTC of that
//     day, and for it the day in a zone behind UTC would be a day too early.
//     The study's entry rule (next open or next close) does not change the
//     day: walkfwd enters in the first session after the decision day, which is
//     never earlier than either rule would enter, so the replay is the stricter
//     of the two. Information usable later than the decision is expressed by a
//     later Decided, never by a later KnownOn, which walkfwd would exclude as
//     look-ahead.
//   - The reference is the close of the last bar on or before the decision day,
//     which is the last finished bar before the entry session; no later bar is
//     read. A reference older than the study's longest entry gap is refused.
//   - Limit is the reference times one plus the limit offset, Invalidation the
//     reference times one minus the invalidation offset (none when the offset is
//     zero), and Qty the notional divided by the limit, in whole shares.
//
// A buy with no reference, no bars, bars that cannot be put on one basis, no
// currency or a notional that buys no whole share is kept as a quantity of zero
// and listed in Skipped with the reason. It is an error when a rule that buys
// has no trade stanza, or a signal was not made by this playbook revision.
//
// The bars returned cover the traded securities and the fund from before the
// earliest decision to after the latest one plus the longest holding period in
// calendar days. Securities of watch and sell signals are not read: a decision
// not to trade is never simulated against bars.
func ReplayProposals(ctx context.Context, s Store, in ReplayInput) (Replay, error) {
	if err := in.Playbook.Validate(); err != nil {
		return Replay{}, err
	}
	if in.Trial == "" {
		return Replay{}, errors.New("research: a replay needs the trial its proposals belong to")
	}
	trades := map[string]playbook.Trade{}
	var untraded []string
	for _, r := range in.Playbook.Rules {
		switch {
		case r.Trade != nil:
			trades[r.ID] = *r.Trade
		case r.Action == playbook.Buy:
			untraded = append(untraded, r.ID)
		}
	}
	if len(untraded) > 0 {
		return Replay{}, fmt.Errorf("research: rule %s of playbook %s has no trade stanza (notional, limit_offset, invalidation_offset), so it cannot be replayed with fills", strings.Join(untraded, ", "), in.Playbook.ID)
	}
	loc, err := time.LoadLocation(in.Spec.Session.Zone)
	if err != nil {
		return Replay{}, fmt.Errorf("research: the session zone %q of the study is not known", in.Spec.Session.Zone)
	}
	if in.Spec.EntryDelayMinutes < 0 || in.Spec.MaxEntryGapDays < 1 {
		return Replay{}, errors.New("research: the study needs an entry delay of at least 0 and an entry gap of at least 1 day")
	}
	hash := in.Playbook.Hash()
	for _, sig := range in.Signals {
		if sig.PlaybookHash != hash {
			return Replay{}, fmt.Errorf("research: signal %s was made by another revision of playbook %s (hash %s, not %s): compute the signals again", sig.ID, sig.Playbook, short(sig.PlaybookHash), short(hash))
		}
	}

	out := Replay{Proposals: make([]walkfwd.Proposal, len(in.Signals))}
	horizon := 0
	var wanted []string
	first, last := "", ""
	for i, sig := range in.Signals {
		day := usableDay(sig.AvailableAt, in.Spec, loc)
		out.Proposals[i] = walkfwd.Proposal{ID: sig.ID, Trial: in.Trial, Security: sig.Security, Decided: day, KnownOn: day,
			HorizonSessions: sig.Sessions, Currency: money.Currency(in.Currency)}
		if sig.Action != playbook.Buy {
			continue
		}
		wanted = append(wanted, sig.Security)
		horizon = max(horizon, sig.Sessions)
		if first == "" || day < first {
			first = day
		}
		if day > last {
			last = day
		}
	}
	prepared := map[string]prep{}
	if first != "" {
		if in.Fund != "" {
			wanted = append(wanted, in.Fund)
		}
		if out.From, out.To, err = replaySpan(first, last, horizon, in.Spec.MaxEntryGapDays); err != nil {
			return Replay{}, err
		}
		series, unresolved, err := marketdata.LoadSeries(ctx, s, wanted, out.From, out.To, in.Known)
		if err != nil {
			return Replay{}, fmt.Errorf("research: %w", err)
		}
		for ticker, why := range unresolved {
			prepared[ticker] = prep{problem: "the bars of " + ticker + " cannot be put on one basis: " + why}
		}
		for ticker, ser := range series {
			var dropped int
			prepared[ticker], dropped = prepare(ticker, ser, in.Currency)
			out.DroppedBars += dropped
		}
		if in.Fund != "" {
			out.FundProblem = prepared[in.Fund].problem
		}
	}

	for i, sig := range in.Signals {
		p := &out.Proposals[i]
		if sig.Action != playbook.Buy {
			out.NoTrades++
			continue
		}
		if reason := sizeBuy(p, prepared[sig.Security], trades[sig.Rule], in.Spec.MaxEntryGapDays); reason != "" {
			out.Skipped = append(out.Skipped, Skipped{Signal: sig.ID, Security: sig.Security, Reason: reason})
			continue
		}
		out.Trades++
	}

	// Only securities that are traded, or the fund, are returned; a security
	// whose every buy was skipped is not needed.
	needed := map[string]bool{}
	if in.Fund != "" {
		needed[in.Fund] = true
	}
	for _, p := range out.Proposals {
		if p.Qty.Sign() > 0 {
			needed[p.Security] = true
		}
	}
	names := make([]string, 0, len(needed))
	for name := range needed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.Bars = append(out.Bars, prepared[name].bars...)
	}
	return out, nil
}

// usableDay is the day an event can first be acted on: the day of the time it
// became available plus the entry delay, in the exchange's zone or in UTC,
// whichever is later.
func usableDay(at time.Time, spec eventstudy.Spec, loc *time.Location) string {
	usable := at.Add(time.Duration(spec.EntryDelayMinutes) * time.Minute)
	return max(usable.In(loc).Format(time.DateOnly), usable.UTC().Format(time.DateOnly))
}

// replaySpan is the span of days the bars are read for: from the longest entry
// gap and a margin before the earliest decision to the longest holding period,
// in calendar days, and a margin after the latest.
func replaySpan(first, last string, horizon, maxGap int) (from, to string, err error) {
	f, err1 := time.Parse(time.DateOnly, first)
	l, err2 := time.Parse(time.DateOnly, last)
	if err := errors.Join(err1, err2); err != nil {
		return "", "", fmt.Errorf("research: a decision day is not a day: %w", err)
	}
	weeks := (horizon + 4) / 5
	f = f.AddDate(0, 0, -(maxGap + referenceMarginDays))
	l = l.AddDate(0, 0, weeks*7+barMarginDays)
	return f.Format(time.DateOnly), l.Format(time.DateOnly), nil
}

// prep is the bars of one security as the replay uses them, or why it has none.
type prep struct {
	bars     []paper.Bar
	currency money.Currency
	problem  string
}

// prepare turns a series into bars: the currency the bars state, or the one
// given when they state none, and each bar as a decimal. It counts the bars
// that could not be one.
func prepare(ticker string, ser marketdata.Series, fallback string) (prep, int) {
	cur := strings.ToUpper(ser.Currency)
	if cur == "" {
		cur = fallback
	}
	if cur == "" {
		return prep{problem: "the currency of the prices of " + ticker + " is not known: give one"}, 0
	}
	c, err := money.ParseCurrency(cur)
	if err != nil {
		return prep{problem: err.Error()}, 0
	}
	p := prep{currency: c}
	dropped := 0
	for _, b := range ser.Bars {
		pb, ok := toBar(ticker, c, b)
		if !ok {
			dropped++
			continue
		}
		p.bars = append(p.bars, pb)
	}
	if len(p.bars) == 0 {
		p.problem = "no usable bar of " + ticker
	}
	return p, dropped
}

// toBar turns a bar of a series into a bar of the replay; false when the
// result would not be a valid bar.
func toBar(ticker string, cur money.Currency, b marketdata.Bar) (paper.Bar, bool) {
	pb := paper.Bar{Security: ticker, Day: b.Day, Volume: b.Volume, Currency: cur}
	for dst, v := range map[*money.Decimal]float64{&pb.Open: b.Open, &pb.High: b.High, &pb.Low: b.Low, &pb.Close: b.Close} {
		d, err := money.FromFloat(v, barScale, money.HalfUp)
		if err != nil {
			return paper.Bar{}, false
		}
		*dst = d
	}
	return pb, pb.Validate() == nil
}

// sizeBuy sets the currency, quantity, limit and stop of a buy proposal from
// the last finished bar before the entry session; it says why not, and leaves
// the proposal a decision not to trade, when it cannot.
func sizeBuy(p *walkfwd.Proposal, px prep, t playbook.Trade, maxGap int) string {
	if px.problem != "" {
		return px.problem
	}
	// The entry session is the first bar after the decision day; the reference
	// is the bar before it. Nothing from the entry session on is looked at.
	n := sort.Search(len(px.bars), func(i int) bool { return px.bars[i].Day > p.Decided })
	if n == 0 {
		return "no bar of " + p.Security + " on or before the decision day " + p.Decided
	}
	ref := px.bars[n-1]
	if age, ok := daysBetween(ref.Day, p.Decided); !ok || age > maxGap {
		return fmt.Sprintf("the last close before the decision (%s) is older than %d days", ref.Day, maxGap)
	}
	limit, err := scaled(ref.Close, t.LimitOffset)
	if err != nil || limit.Sign() <= 0 {
		return "the limit cannot be priced from the close " + ref.Close.String()
	}
	notional, err := money.ParseDecimal(t.Notional)
	if err != nil {
		return err.Error()
	}
	qty, err := notional.Div(limit, 0, money.Down)
	if err != nil || qty.Sign() <= 0 {
		return fmt.Sprintf("a notional of %s buys no whole share at the limit %s", t.Notional, limit)
	}
	var stop *money.Decimal
	if t.InvalidationOffset > 0 {
		s, err := scaled(ref.Close, -t.InvalidationOffset)
		if err != nil || s.Sign() <= 0 {
			return "the stop cannot be priced from the close " + ref.Close.String()
		}
		stop = &s
	}
	p.Currency, p.Limit, p.Qty, p.Invalidation = px.currency, limit, qty, stop
	return ""
}

// scaled is price times one plus offset, rounded to barScale digits.
func scaled(price money.Decimal, offset float64) (money.Decimal, error) {
	off, err := money.FromFloat(offset, barScale, money.HalfUp)
	if err != nil {
		return money.Decimal{}, err
	}
	factor, err := money.Int(1).Add(off)
	if err != nil {
		return money.Decimal{}, err
	}
	return price.Mul(factor, barScale, money.HalfUp)
}

func daysBetween(from, to string) (int, bool) {
	a, err1 := time.Parse(time.DateOnly, from)
	b, err2 := time.Parse(time.DateOnly, to)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return int(b.Sub(a).Hours() / 24), true
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
