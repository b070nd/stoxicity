package research

// Every name, figure and price below is a synthetic fixture, not verified against live data.

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/playbook"
)

func replayPlaybook(trade *playbook.Trade) playbook.Playbook {
	ev := playbook.Evidence{Sessions: 5, MinEvents: 30, Direction: "up", MinEdge: 0.01}
	down := ev
	down.Direction = "down"
	return playbook.Playbook{ID: "pb", Study: "st", Rules: []playbook.Rule{
		{ID: "buy", Types: []string{"earnings"}, Evidence: ev, Action: playbook.Buy, Trade: trade},
		{ID: "sell", Types: []string{"guidance"}, Evidence: down, Action: playbook.Sell},
	}}
}

var replayTrade = &playbook.Trade{Notional: "1000", LimitOffset: 0.01, InvalidationOffset: 0.1}

func replaySignal(p playbook.Playbook, id, rule, security string, at time.Time, action playbook.Action) playbook.Signal {
	return playbook.Signal{ID: id, Playbook: p.ID, PlaybookHash: p.Hash(), Rule: rule, Security: security, AvailableAt: at, Action: action, Sessions: 5}
}

func replayCandle(ticker, day string, px float64, raw bool, currency string) marketdata.Candle {
	return marketdata.Candle{Ticker: ticker, Day: day, Open: px, High: px, Low: px, Close: px, Volume: 1000, Raw: raw, Currency: currency}
}

// closeOfTheFifth is 16:00 in New York on Wednesday 2025-03-05.
var closeOfTheFifth = time.Date(2025, 3, 5, 21, 0, 0, 0, time.UTC)

// aaaCandles are raw closes of 48, 49 and 50 on the third to the fifth of March, a
// price of entryDay on the sixth, and 25.5 on the seventh, the first day after a
// 2-for-1 split.
func aaaCandles(entryDay float64) []marketdata.Candle {
	return []marketdata.Candle{
		replayCandle("AAA", "2025-03-03", 48, true, "USD"), replayCandle("AAA", "2025-03-04", 49, true, "USD"),
		replayCandle("AAA", "2025-03-05", 50, true, "USD"), replayCandle("AAA", "2025-03-06", entryDay, true, "USD"),
		replayCandle("AAA", "2025-03-07", 25.5, true, "USD"),
	}
}

func replayFake(entryDay float64) *fake {
	return &fake{
		candles: map[string][]marketdata.Candle{
			"AAA":  aaaCandles(entryDay),
			"FUND": {replayCandle("FUND", "2025-03-05", 10, true, "USD"), replayCandle("FUND", "2025-03-07", 11, true, "USD")},
		},
		splits: []marketdata.Split{{Ticker: "AAA", Day: "2025-03-07", From: 1, To: 2}},
	}
}

func replayInput(sigs ...playbook.Signal) ReplayInput {
	pb := replayPlaybook(replayTrade)
	return ReplayInput{Playbook: pb, Signals: sigs, Spec: eventstudy.DefaultSpec(), Trial: "t1", Fund: "FUND"}
}

func TestReplayBuyIsSizedFromTheCloseBeforeTheEntrySession(t *testing.T) {
	pb := replayPlaybook(replayTrade)
	// The sixth is the entry session. A price of 9999 there must reach neither
	// the limit nor the stop.
	got, err := ReplayProposals(context.Background(), replayFake(9999), replayInput(replaySignal(pb, "sig_a", "buy", "AAA", closeOfTheFifth, playbook.Buy)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Proposals) != 1 || got.Trades != 1 || got.NoTrades != 0 || len(got.Skipped) != 0 {
		t.Fatalf("got %+v", got)
	}
	p := got.Proposals[0]
	// The close of the fifth, on the basis after the split, is 25.
	if p.ID != "sig_a" || p.Trial != "t1" || p.Security != "AAA" || p.Decided != "2025-03-05" || p.KnownOn != "2025-03-05" ||
		p.Limit.String() != "25.250000" || p.Qty.String() != "39" || p.Invalidation == nil || p.Invalidation.String() != "22.500000" ||
		p.HorizonSessions != 5 || p.Currency != "USD" {
		t.Errorf("proposal = %+v", p)
	}
	var secs []string
	for _, b := range got.Bars {
		if len(secs) == 0 || secs[len(secs)-1] != b.Security {
			secs = append(secs, b.Security)
		}
		if b.Security == "AAA" && b.Day == "2025-03-03" && (b.Close.String() != "24.000000" || b.Volume != 2000) {
			t.Errorf("a bar before the split is not on the new basis: %+v", b)
		}
	}
	if !reflect.DeepEqual(secs, []string{"AAA", "FUND"}) || got.From != "2025-02-21" || got.To != "2025-03-26" {
		t.Errorf("securities %v, span %s to %s", secs, got.From, got.To)
	}
}

func TestReplayReferenceDoesNotDependOnTheEntrySession(t *testing.T) {
	pb := replayPlaybook(replayTrade)
	in := replayInput(replaySignal(pb, "sig_a", "buy", "AAA", closeOfTheFifth, playbook.Buy))
	var proposals []string
	for _, price := range []float64{1, 52, 9999} {
		got, err := ReplayProposals(context.Background(), replayFake(price), in)
		if err != nil {
			t.Fatal(err)
		}
		p := got.Proposals[0]
		proposals = append(proposals, p.Limit.String()+" "+p.Qty.String()+" "+p.Invalidation.String())
	}
	if proposals[0] != proposals[1] || proposals[1] != proposals[2] {
		t.Errorf("the entry session's price changed the proposal: %v", proposals)
	}
}

func TestReplayWatchAndSellAreDecisionsNotToTrade(t *testing.T) {
	pb := replayPlaybook(replayTrade)
	tests := []struct {
		name   string
		action playbook.Action
		rule   string
	}{{"watch", playbook.Watch, "buy"}, {"sell", playbook.Sell, "sell"}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReplayProposals(context.Background(), replayFake(51), replayInput(replaySignal(pb, "sig_x", tc.rule, "AAA", closeOfTheFifth, tc.action)))
			if err != nil {
				t.Fatal(err)
			}
			p := got.Proposals[0]
			if len(got.Proposals) != 1 || p.ID != "sig_x" || p.Qty.Sign() != 0 || p.Decided != "2025-03-05" || got.NoTrades != 1 || got.Trades != 0 || len(got.Skipped) != 0 {
				t.Errorf("got %+v", got)
			}
			// Nothing is traded, so no security is read: not even the fund.
			if len(got.Bars) != 0 || got.From != "" {
				t.Errorf("bars were read for a decision not to trade: %d bars from %q", len(got.Bars), got.From)
			}
		})
	}
}

func TestReplaySkippedBuysKeepTheirReason(t *testing.T) {
	small := &playbook.Trade{Notional: "10", LimitOffset: 0.01}
	tests := []struct {
		name     string
		security string
		at       time.Time
		trade    *playbook.Trade
		currency string
		want     string
	}{
		{"no bars at all", "NONE", closeOfTheFifth, replayTrade, "", "no bars"},
		{"nothing before the decision", "AAA", time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC), replayTrade, "", "no bar of AAA on or before"},
		{"close too old", "AAA", time.Date(2025, 3, 20, 12, 0, 0, 0, time.UTC), replayTrade, "", "older than 5 days"},
		{"notional buys no share", "AAA", closeOfTheFifth, small, "", "buys no whole share"},
		{"currency unknown", "NOCUR", closeOfTheFifth, replayTrade, "", "currency"},
		{"unknown basis and a split", "RAWLESS", closeOfTheFifth, replayTrade, "", "one basis"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := replayFake(51)
			f.candles["NOCUR"] = []marketdata.Candle{replayCandle("NOCUR", "2025-03-05", 20, true, "")}
			f.candles["RAWLESS"] = []marketdata.Candle{replayCandle("RAWLESS", "2025-03-04", 20, false, "USD"), replayCandle("RAWLESS", "2025-03-05", 20, false, "USD")}
			f.splits = append(f.splits, marketdata.Split{Ticker: "RAWLESS", Day: "2025-03-05", From: 1, To: 2})
			pb := replayPlaybook(tc.trade)
			in := replayInput(replaySignal(pb, "sig_s", "buy", tc.security, tc.at, playbook.Buy))
			in.Playbook, in.Currency = pb, tc.currency
			got, err := ReplayProposals(context.Background(), f, in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Trades != 0 || len(got.Skipped) != 1 || len(got.Proposals) != 1 {
				t.Fatalf("got %+v", got)
			}
			s := got.Skipped[0]
			if s.Signal != "sig_s" || s.Security != tc.security || !strings.Contains(s.Reason, tc.want) {
				t.Errorf("skipped = %+v, want a reason with %q", s, tc.want)
			}
			p := got.Proposals[0]
			if p.Qty.Sign() != 0 || p.Invalidation != nil || p.Limit.Sign() != 0 {
				t.Errorf("a skipped buy kept a size or a price: %+v", p)
			}
		})
	}
}

func TestReplayCurrencyFallback(t *testing.T) {
	f := replayFake(51)
	f.candles["NOCUR"] = []marketdata.Candle{replayCandle("NOCUR", "2025-03-05", 20, true, "")}
	pb := replayPlaybook(replayTrade)
	in := replayInput(replaySignal(pb, "sig_c", "buy", "NOCUR", closeOfTheFifth, playbook.Buy))
	in.Currency = "EUR"
	got, err := ReplayProposals(context.Background(), f, in)
	if err != nil || got.Trades != 1 || got.Proposals[0].Currency != "EUR" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestReplayRefusals(t *testing.T) {
	pb := replayPlaybook(replayTrade)
	old := pb
	old.Note = "an earlier revision"
	tests := []struct {
		name string
		edit func(in *ReplayInput)
		want string
	}{
		{"buy rule without a trade", func(in *ReplayInput) { in.Playbook = replayPlaybook(nil) }, "no trade stanza"},
		{"signal of another revision", func(in *ReplayInput) { in.Signals[0].PlaybookHash = old.Hash() }, "another revision"},
		{"no trial", func(in *ReplayInput) { in.Trial = "" }, "trial"},
		{"unknown zone", func(in *ReplayInput) { in.Spec.Session.Zone = "Nowhere/Land" }, "zone"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := replayInput(replaySignal(pb, "sig_a", "buy", "AAA", closeOfTheFifth, playbook.Buy))
			tc.edit(&in)
			_, err := ReplayProposals(context.Background(), replayFake(51), in)
			if err == nil || !strings.HasPrefix(err.Error(), "research: ") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error with %q", err, tc.want)
			}
		})
	}
}

func TestReplayIsDeterministic(t *testing.T) {
	pb := replayPlaybook(replayTrade)
	in := replayInput(
		replaySignal(pb, "sig_a", "buy", "AAA", closeOfTheFifth, playbook.Buy),
		replaySignal(pb, "sig_b", "sell", "AAA", closeOfTheFifth, playbook.Sell),
		replaySignal(pb, "sig_c", "buy", "NONE", closeOfTheFifth, playbook.Buy))
	a, err := ReplayProposals(context.Background(), replayFake(51), in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReplayProposals(context.Background(), replayFake(51), in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || a.Trades != 1 || a.NoTrades != 1 || len(a.Skipped) != 1 || len(a.Proposals) != 3 {
		t.Errorf("a = %+v\nb = %+v", a, b)
	}
}

func TestReplayDropsBarsThatAreNotBars(t *testing.T) {
	f := replayFake(51)
	// An open above the high is a candle the source got wrong.
	f.candles["AAA"] = append(f.candles["AAA"], marketdata.Candle{Ticker: "AAA", Day: "2025-03-10", Open: 30, High: 25, Low: 24, Close: 25, Volume: 1, Raw: false, Currency: "USD"})
	f.splits = nil
	pb := replayPlaybook(replayTrade)
	got, err := ReplayProposals(context.Background(), f, replayInput(replaySignal(pb, "sig_a", "buy", "AAA", closeOfTheFifth, playbook.Buy)))
	if err != nil || got.DroppedBars != 1 || got.Trades != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, b := range got.Bars {
		if b.Day == "2025-03-10" {
			t.Errorf("a bar that is not a bar was returned: %+v", b)
		}
	}
}

func TestUsableDay(t *testing.T) {
	tests := []struct {
		name  string
		at    time.Time
		zone  string
		delay int
		want  string
	}{
		{"after the close in New York", time.Date(2025, 3, 5, 21, 0, 0, 0, time.UTC), "America/New_York", 15, "2025-03-05"},
		{"evening in New York is already the next day in UTC", time.Date(2025, 3, 6, 2, 0, 0, 0, time.UTC), "America/New_York", 15, "2025-03-06"},
		{"a day-precision event is at midnight UTC", time.Date(2025, 3, 5, 0, 0, 0, 0, time.UTC), "America/New_York", 15, "2025-03-05"},
		{"the zone ahead of UTC counts when it is later", time.Date(2025, 3, 5, 20, 0, 0, 0, time.UTC), "Asia/Tokyo", 15, "2025-03-06"},
		{"the delay carries past midnight", time.Date(2025, 3, 5, 23, 50, 0, 0, time.UTC), "America/New_York", 15, "2025-03-06"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := eventstudy.DefaultSpec()
			spec.Session.Zone, spec.EntryDelayMinutes = tc.zone, tc.delay
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			if got := usableDay(tc.at, spec, loc); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestReplayNamesAFundWithoutBars(t *testing.T) {
	f := replayFake(51)
	delete(f.candles, "FUND")
	pb := replayPlaybook(replayTrade)
	got, err := ReplayProposals(context.Background(), f, replayInput(replaySignal(pb, "sig_a", "buy", "AAA", closeOfTheFifth, playbook.Buy)))
	if err != nil || got.Trades != 1 || !strings.Contains(got.FundProblem, "no bars") {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, b := range got.Bars {
		if b.Security != "AAA" {
			t.Errorf("bar of %s returned", b.Security)
		}
	}
}
