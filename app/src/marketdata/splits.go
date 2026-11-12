package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// Split is a stock split: from the first Day the stock trades at the new price.
// One share became To/From shares: 2-for-1 is From 1, To 2; a 1-for-10 reverse
// split is From 10, To 1.
type Split struct {
	Ticker string
	Day    string // YYYY-MM-DD, the first day at the new price
	From   float64
	To     float64
}

// Ratio is how many shares one share became (2 for 2-for-1, 0.1 for 1-for-10).
func (s Split) Ratio() float64 {
	if s.From <= 0 || s.To <= 0 {
		return 1
	}
	return s.To / s.From
}

func (s Split) String() string {
	return fmt.Sprintf("%s %g-for-%g on %s", s.Ticker, s.To, s.From, s.Day)
}

// SplitCoverage says which splits are known: those with a Day from Since on, as
// read at Read. A price move that no known split explains is a real move only
// where the coverage reaches.
type SplitCoverage struct {
	Since string
	Read  time.Time
}

// Covers reports whether a split on any day after prevDay up to day would be in
// the known splits: they start by prevDay and were read after day was over.
func (c SplitCoverage) Covers(prevDay, day string) bool {
	d, err := time.Parse(time.DateOnly, day)
	return err == nil && c.Since != "" && c.Since <= prevDay && c.Read.After(d.AddDate(0, 0, 2))
}

// SplitSource gives the splits of the market.
type SplitSource interface {
	// Splits returns the splits executed on or after since.
	Splits(ctx context.Context, since time.Time) ([]Split, error)
}

// SplitStore keeps splits.
type SplitStore interface {
	// PutSplits adds the splits (the ones already stored stay as they are) and
	// notes that the splits executed from since on were read at the time.
	PutSplits(ctx context.Context, splits []Split, since time.Time, at time.Time) error
	// SplitCoverage returns what the stored splits cover.
	SplitCoverage(ctx context.Context) (SplitCoverage, error)
}

// PolygonSplitsURL is Polygon's reference list of splits, for the whole market.
const PolygonSplitsURL = "https://api.polygon.io/v3/reference/splits"

// splitPages is the most pages one reading follows; a year of splits is a few
// hundred and a page holds up to a thousand.
const splitPages = 5

// Splits implements SplitSource.
func (p Polygon) Splits(ctx context.Context, since time.Time) ([]Split, error) {
	if p.Key == "" {
		return nil, errors.New("marketdata: Polygon needs an API key (STOXICITY_POLYGON_KEY)")
	}
	base := p.SplitsURL
	switch {
	case base != "":
	case p.URL != "":
		base = p.URL + "splits" // an overridden address (a test) holds both
	default:
		base = PolygonSplitsURL
	}
	next := base + "?" + url.Values{"execution_date.gte": {since.Format(time.DateOnly)}, "order": {"asc"}, "limit": {"1000"}, "apiKey": {p.Key}}.Encode()
	var out []Split
	for page := 0; page < splitPages && next != ""; page++ {
		resp, err := p.Client.Do(ctx, httpx.Request{URL: next})
		if err != nil {
			return nil, err
		}
		var ans struct {
			Results []struct {
				Ticker string  `json:"ticker"`
				Day    string  `json:"execution_date"`
				From   float64 `json:"split_from"`
				To     float64 `json:"split_to"`
			} `json:"results"`
			Next string `json:"next_url"`
		}
		if err := json.Unmarshal(resp.Body, &ans); err != nil {
			return nil, fmt.Errorf("marketdata: read Polygon's splits: %w", err)
		}
		for _, r := range ans.Results {
			if r.Ticker == "" || r.From <= 0 || r.To <= 0 {
				continue
			}
			if _, err := time.Parse(time.DateOnly, r.Day); err != nil {
				continue
			}
			out = append(out, Split{Ticker: r.Ticker, Day: r.Day, From: r.From, To: r.To})
		}
		next = ""
		if ans.Next != "" {
			// The next page's address does not carry the key.
			next = ans.Next
			if !strings.Contains(next, "apiKey=") {
				sep := "?"
				if strings.Contains(next, "?") {
					sep = "&"
				}
				next += sep + url.Values{"apiKey": {p.Key}}.Encode()
			}
			if page == splitPages-1 {
				return out, fmt.Errorf("marketdata: Polygon has more splits than %d pages hold", splitPages)
			}
		}
	}
	return out, nil
}

// MoveState says what a move between two closes is.
type MoveState string

// The states of a move.
const (
	// MoveOK is a move between the closes of consecutive sessions.
	MoveOK MoveState = "ok"
	// MoveSplit is such a move after the earlier close was put on the basis of the
	// later one, for a split that took effect between them.
	MoveSplit MoveState = "split"
	// MoveGap is no move: sessions are missing between the two closes.
	MoveGap MoveState = "gap"
	// MoveUnresolved is no move shown: the closes may be on different bases (a
	// split took effect and the candle's basis is not known) or the change looks
	// like a split that the known splits do not list. It is not hidden as noise;
	// it is flagged as not yet explained.
	MoveUnresolved MoveState = "unresolved"
)

// Move is the change from one close to the next, and how far it can be trusted.
type Move struct {
	Change float64   // a fraction (0.021 is +2.1%); only meaningful when Shown
	State  MoveState // what the move is
	Note   string    // why it is not shown, or which split was applied
}

// Shown says the change can be displayed as a day's move.
func (m Move) Shown() bool { return m.State == MoveOK || m.State == MoveSplit }

// Compare is the move from the candle prev to the candle cur of one stock,
// reconciled with the splits.
//
//   - Sessions missing between the two (more than a holiday) give no move.
//   - Raw candles are put on one basis by the splits that took effect in
//     between: the earlier close is divided by their ratios. A move that is then
//     large is shown as it is; size alone hides nothing.
//   - A candle of unknown basis (made before candles were kept raw) next to a
//     known split after the earlier day cannot be reconciled: unresolved.
//   - A move with the ratio of a split that no split explains is a real move
//     where the coverage reaches the two days, and unresolved where it does not.
func Compare(prev, cur Candle, splits []Split, cov SplitCoverage) Move {
	if prev.Close <= 0 || cur.Close <= 0 || prev.Day == "" || cur.Day == "" {
		return Move{State: MoveGap, Note: "no earlier close"}
	}
	if missed, ok := MissingWeekdays(prev.Day, cur.Day); !ok || missed > MaxMissingWeekdays {
		return Move{State: MoveGap, Note: "sessions missing between the two closes"}
	}
	after := splitsBetween(splits, prev.Day, "", cur.Ticker, prev.Ticker)
	between := splitsBetween(after, prev.Day, cur.Day, cur.Ticker, prev.Ticker)
	if !(prev.Raw && cur.Raw) && len(after) > 0 {
		return Move{State: MoveUnresolved, Note: fmt.Sprintf("candle of unknown basis and %s", after[0])}
	}
	ratio := splitFactor(between, prev.Day)
	base := prev.Close / ratio
	m := Move{Change: cur.Close/base - 1, State: MoveOK}
	if len(between) > 0 {
		m.State, m.Note = MoveSplit, between[0].String()
		return m
	}
	if r := cur.Close / prev.Close; (r > MaxMove || r < 1/MaxMove || LooksLikeSplit(r)) && !cov.Covers(prev.Day, cur.Day) {
		return Move{State: MoveUnresolved, Note: "the move looks like a split and the known splits do not reach these days"}
	}
	return m
}
