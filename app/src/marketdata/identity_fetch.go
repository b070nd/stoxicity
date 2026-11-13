package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// PolygonReferenceURL is the address Polygon's (Massive's) reference data is
// asked at; it ends with a slash and the paths of the endpoints follow it.
const PolygonReferenceURL = "https://api.polygon.io/"

// The sources the candidates of the Massive fetcher name. They stay in the
// source column of a stored span, so the provenance of a row is visible; a span
// a person verifies keeps its source with "verified:" before it.
const (
	// IdentityEventsSource is the source of spans read from ticker events.
	IdentityEventsSource = "massive:ticker-events"
	// IdentityTickersSource is the source of spans read from the ticker list and
	// the ticker overview.
	IdentityTickersSource = "massive:tickers"
)

// identityListLimit is how many tickers one page of the list holds, and
// identityListPages the most pages one run follows.
const (
	identityListLimit = 1000
	identityListPages = 50
)

// The notes a candidate carries, so that what the source does not say is not
// read out of the row.
const (
	identityEventsNote = "candidate from Massive ticker events: the reason of a change is never given, so a rename is not told from other causes, " +
		"and the date is not stated to be the first day traded under the symbol; not verified"
	identityDelistNote = "candidate from Massive: delisted_utc comes a few days after the last bar, not on it, so valid_to may be later than the last " +
		"trading day, and a merger is not told from a delisting; not verified"
)

// IdentityAnswer is what the provider says about one ticker, as candidate
// spans. A candidate is never verified.
type IdentityAnswer struct {
	// Spans are the candidates, each of which Validate accepts. They are empty
	// when the answer holds nothing usable: no composite FIGI to key the
	// security by, no list date, or a ticker that is still active.
	Spans []SymbolSpan
	// Found is false when the provider knows nothing of the ticker: it answers
	// 404 for a symbol that was acquired or delisted.
	Found bool
}

// InactivePage is one page of the provider's list of tickers no longer active.
type InactivePage struct {
	Tickers []string
	// Next asks for the page after this one, without any key; empty at the end.
	Next string
}

// IdentityFeed is a provider's reference data on which tickers were one
// security's.
type IdentityFeed interface {
	// Name is the provider's name.
	Name() string
	// Events returns the candidate spans of the ticker's ticker-change events,
	// for the entity that uses the ticker now; the ticker may also be a
	// composite FIGI.
	Events(ctx context.Context, ticker string) (IdentityAnswer, error)
	// Overview returns the candidate delisting span of a ticker that is no
	// longer active.
	Overview(ctx context.Context, ticker string) (IdentityAnswer, error)
	// Inactive returns a page of the tickers no longer active that sort after
	// the given one (all when after is empty); next is the Next of the page
	// before, empty for the first.
	Inactive(ctx context.Context, after, next string) (InactivePage, error)
}

// MassiveIdentity is Polygon's (Massive's) ticker events, ticker list and ticker
// overview. It needs the same key as Polygon. The security of every span it
// returns is the composite FIGI of the ticker. The security column of the
// history is an opaque text that is the same for every symbol a security has
// had, and a FIGI does not change when the ticker does. The instrument package
// keys its securities by a registry id, an ISIN where known, and holds no FIGI,
// so no convention there conflicts; a person who keys the same security by
// another text writes the verified span under that key.
type MassiveIdentity struct {
	Client httpx.Client
	Key    string
	// URL replaces PolygonReferenceURL (it ends with a slash); tests point it at a
	// server.
	URL string
}

// Name implements IdentityFeed.
func (MassiveIdentity) Name() string { return "massive" }

// address returns the address to ask for the path and query, with the key in it,
// and ref, the same without the host and the key, which is what a stored span
// names as its source_ref.
func (m MassiveIdentity) address(path string, q url.Values) (full, ref string, err error) {
	if m.Key == "" {
		return "", "", errors.New("marketdata: Massive needs an API key (STOXICITY_POLYGON_KEY or MASSIVE_API_KEY)")
	}
	base := m.URL
	if base == "" {
		base = PolygonReferenceURL
	}
	ref = "/" + path
	if len(q) > 0 {
		ref += "?" + q.Encode()
	}
	keyed := url.Values{"apiKey": {m.Key}}
	for k, v := range q {
		keyed[k] = v
	}
	return base + path + "?" + keyed.Encode(), ref, nil
}

// get asks for the address; found is false for a 404.
func (m MassiveIdentity) get(ctx context.Context, address string) (body []byte, found bool, err error) {
	resp, err := m.Client.Do(ctx, httpx.Request{URL: address})
	var he *httpx.Error
	if errors.As(err, &he) && he.Status == http.StatusNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return resp.Body, true, nil
}

// identityID is how the provider is asked for a ticker, or for a composite FIGI,
// which is given as it is.
func identityID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\?#\r\n\x00") {
		return "", errors.New("marketdata: identity: invalid ticker")
	}
	if len(id) == 12 && strings.HasPrefix(strings.ToUpper(id), "BBG") {
		return strings.ToUpper(id), nil
	}
	return polygonSymbol(id), nil
}

// identityNote joins the note of a candidate with what the provider gave about
// the issuer and the share class.
func identityNote(note, name, cik, shareClass string) string {
	var facts []string
	for _, f := range [][2]string{{"name", name}, {"cik", cik}, {"share class figi", shareClass}} {
		if v := strings.TrimSpace(f[1]); v != "" {
			facts = append(facts, f[0]+" "+v)
		}
	}
	if len(facts) == 0 {
		return note
	}
	return note + "; " + strings.Join(facts, ", ")
}

// Events implements IdentityFeed. The events of one security, in date order,
// are its symbols: each one is the span from the event's date to the day before
// the next event's, which ended by a rename, and the last one is open. Nothing
// is guessed when two events share a day with different tickers: the whole
// answer is then an error.
func (m MassiveIdentity) Events(ctx context.Context, ticker string) (IdentityAnswer, error) {
	id, err := identityID(ticker)
	if err != nil {
		return IdentityAnswer{}, err
	}
	address, ref, err := m.address("vX/reference/tickers/"+url.PathEscape(id)+"/events", url.Values{"types": {"ticker_change"}})
	if err != nil {
		return IdentityAnswer{}, err
	}
	body, found, err := m.get(ctx, address)
	if err != nil || !found {
		return IdentityAnswer{}, err
	}
	var ans struct {
		Results struct {
			Name   string `json:"name"`
			CIK    string `json:"cik"`
			Figi   string `json:"composite_figi"`
			Events []struct {
				Date   string `json:"date"`
				Type   string `json:"type"`
				Change struct {
					Ticker string `json:"ticker"`
				} `json:"ticker_change"`
			} `json:"events"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return IdentityAnswer{}, fmt.Errorf("marketdata: read Massive's ticker events for %s: %w", ticker, err)
	}
	r := ans.Results
	security := strings.TrimSpace(r.Figi)
	if security == "" {
		return IdentityAnswer{Found: true}, nil
	}
	type change struct{ day, symbol string }
	var changes []change
	for _, e := range r.Events {
		if e.Type == "ticker_change" && strings.TrimSpace(e.Change.Ticker) != "" {
			changes = append(changes, change{strings.TrimSpace(e.Date), strings.ToUpper(strings.TrimSpace(e.Change.Ticker))})
		}
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].day < changes[j].day })
	note := identityNote(identityEventsNote, r.Name, r.CIK, "")
	var spans []SymbolSpan
	for i, c := range changes {
		if i > 0 && c.day == changes[i-1].day {
			if c.symbol == changes[i-1].symbol {
				continue
			}
			return IdentityAnswer{}, fmt.Errorf("marketdata: Massive's ticker events for %s give two tickers on %s", ticker, c.day)
		}
		spans = append(spans, SymbolSpan{Security: security, Symbol: c.symbol, From: c.day, Source: IdentityEventsSource, SourceRef: ref, Note: note})
	}
	for i := 0; i+1 < len(spans); i++ {
		next, err := time.Parse(time.DateOnly, spans[i+1].From)
		if err != nil {
			return IdentityAnswer{}, fmt.Errorf("marketdata: Massive's ticker events for %s: %w", ticker, spanError(SpanBadFrom))
		}
		spans[i].To, spans[i].Kind = next.AddDate(0, 0, -1).Format(time.DateOnly), SpanRename
	}
	for i := range spans {
		spans[i] = spans[i].Normalized()
		if err := spans[i].Validate(); err != nil {
			return IdentityAnswer{}, fmt.Errorf("marketdata: Massive's ticker events for %s: %w", ticker, err)
		}
	}
	return IdentityAnswer{Spans: spans, Found: true}, nil
}

// utcDay reads the day of a time the provider gives either as a time or as a
// day, in UTC.
func utcDay(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.DateOnly), true
	}
	if _, err := time.Parse(time.DateOnly, s); err == nil {
		return s, true
	}
	return "", false
}

// Overview implements IdentityFeed. A ticker that is no longer active, with its
// delisting time, list date and composite FIGI, gives one span from the list
// date to the day of delisted_utc, of kind delisting; that day is a candidate
// only, as the note says. A ticker that is still active gives none.
func (m MassiveIdentity) Overview(ctx context.Context, ticker string) (IdentityAnswer, error) {
	id, err := identityID(ticker)
	if err != nil {
		return IdentityAnswer{}, err
	}
	address, ref, err := m.address("v3/reference/tickers/"+url.PathEscape(id), url.Values{})
	if err != nil {
		return IdentityAnswer{}, err
	}
	body, found, err := m.get(ctx, address)
	if err != nil || !found {
		return IdentityAnswer{}, err
	}
	var ans struct {
		Results struct {
			Ticker     string `json:"ticker"`
			Name       string `json:"name"`
			Active     bool   `json:"active"`
			Delisted   string `json:"delisted_utc"`
			ListDate   string `json:"list_date"`
			CIK        string `json:"cik"`
			Figi       string `json:"composite_figi"`
			ShareClass string `json:"share_class_figi"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return IdentityAnswer{}, fmt.Errorf("marketdata: read Massive's overview of %s: %w", ticker, err)
	}
	r := ans.Results
	ended, ok := utcDay(r.Delisted)
	listed, listedOK := utcDay(r.ListDate)
	if r.Active || !ok || !listedOK || strings.TrimSpace(r.Figi) == "" {
		return IdentityAnswer{Found: true}, nil
	}
	symbol := r.Ticker
	if strings.TrimSpace(symbol) == "" {
		symbol = ticker
	}
	s := SymbolSpan{Security: r.Figi, Symbol: symbol, From: listed, To: ended, Kind: SpanDelisting, Source: IdentityTickersSource, SourceRef: ref,
		Note: identityNote(identityDelistNote, r.Name, r.CIK, r.ShareClass)}.Normalized()
	if err := s.Validate(); err != nil {
		return IdentityAnswer{}, fmt.Errorf("marketdata: Massive's overview of %s: %w", ticker, err)
	}
	return IdentityAnswer{Spans: []SymbolSpan{s}, Found: true}, nil
}

// Inactive implements IdentityFeed. The next page's address comes from the
// provider and does not carry the key, which is added here; it is only followed
// on the host the first page was asked of, so that the key goes nowhere else.
func (m MassiveIdentity) Inactive(ctx context.Context, after, next string) (InactivePage, error) {
	var address string
	if next == "" {
		q := url.Values{"active": {"false"}, "market": {"stocks"}, "sort": {"ticker"}, "order": {"asc"}, "limit": {fmt.Sprint(identityListLimit)}}
		if after = strings.ToUpper(strings.TrimSpace(after)); after != "" {
			q.Set("ticker.gt", after)
		}
		var err error
		if address, _, err = m.address("v3/reference/tickers", q); err != nil {
			return InactivePage{}, err
		}
	} else {
		first, _, err := m.address("v3/reference/tickers", nil)
		if err != nil {
			return InactivePage{}, err
		}
		a, err1 := url.Parse(first)
		b, err2 := url.Parse(next)
		if err := errors.Join(err1, err2); err != nil || a.Host != b.Host {
			return InactivePage{}, errors.New("marketdata: Massive's next page is not on the host of the first page")
		}
		q := b.Query()
		q.Set("apiKey", m.Key)
		b.RawQuery = q.Encode()
		address = b.String()
	}
	body, found, err := m.get(ctx, address)
	if err != nil {
		return InactivePage{}, err
	}
	if !found {
		return InactivePage{}, nil
	}
	var ans struct {
		Results []struct {
			Ticker string `json:"ticker"`
		} `json:"results"`
		Next string `json:"next_url"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return InactivePage{}, fmt.Errorf("marketdata: read Massive's list of inactive tickers: %w", err)
	}
	var page InactivePage
	for _, r := range ans.Results {
		if t := strings.TrimSpace(r.Ticker); t != "" {
			page.Tickers = append(page.Tickers, t)
		}
	}
	if ans.Next != "" {
		u, err := url.Parse(ans.Next)
		if err != nil {
			return InactivePage{}, errors.New("marketdata: Massive's next page address is not an address")
		}
		q := u.Query()
		q.Del("apiKey")
		u.RawQuery = q.Encode()
		page.Next = u.String()
	}
	return page, nil
}

// IdentityAsk says what is asked of the provider about a ticker.
type IdentityAsk string

const (
	// AskEvents asks for the ticker-change events of the ticker.
	AskEvents IdentityAsk = "events"
	// AskOverview asks for the overview of a ticker that is no longer active.
	AskOverview IdentityAsk = "overview"
)

// IdentityRequest is one request to the provider.
type IdentityRequest struct {
	Ask    IdentityAsk
	Ticker string
}

// IdentityPlan is what a wish for candidate spans comes to.
type IdentityPlan struct {
	// Requests are the requests about named tickers, in ticker order.
	Requests []IdentityRequest
	// ListInactive says that the provider's list of tickers no longer active is
	// read first, a page of identityListLimit per request, and each ticker on it
	// that sorts after After is asked the overview of.
	ListInactive bool
	// After is the ticker a run continues after; empty for the start.
	After string
}

// NewIdentityPlan asks for each ticker that sorts after the given one (all when
// it is empty), once, in ticker order: its overview when delisted is set, its
// events otherwise.
func NewIdentityPlan(tickers []string, delisted bool, after string) IdentityPlan {
	after = strings.ToUpper(strings.TrimSpace(after))
	ask := AskEvents
	if delisted {
		ask = AskOverview
	}
	seen := map[string]bool{}
	var plan IdentityPlan
	plan.After = after
	for _, t := range tickers {
		if t = strings.ToUpper(strings.TrimSpace(t)); t != "" && t > after && !seen[t] {
			seen[t] = true
			plan.Requests = append(plan.Requests, IdentityRequest{Ask: ask, Ticker: t})
		}
	}
	sort.Slice(plan.Requests, func(i, j int) bool { return plan.Requests[i].Ticker < plan.Requests[j].Ticker })
	return plan
}

// IdentityStore is the part of the database the identity keeper needs.
type IdentityStore interface {
	PutSymbolHistory(ctx context.Context, rows []SymbolSpan, at time.Time) (int, error)
}

// IdentityKeeper stores the candidate spans a provider gives. It makes the
// requests one by one, within the request budget the HTTP client holds.
type IdentityKeeper struct {
	Store IdentityStore
	Feed  IdentityFeed
	Now   func() time.Time
	// Pace is the wait between two requests, for a plan that allows few requests
	// a minute.
	Pace time.Duration
}

func (k *IdentityKeeper) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

// IdentityReport says what a fetch did.
type IdentityReport struct {
	Asked      int // requests made
	Spans      int // candidate spans found
	Delistings int // of which delistings
	Stored     int // new revisions stored
	None       int // requests the provider knows nothing of (404): no events
	Unusable   int // answers with no candidate to store
	Failed     int
	Left       int // requests planned but not made, because of the budget or a stop
	// Last is the ticker of the last request made about a named ticker or a
	// listed one; a later run continues after it.
	Last string
}

func (r IdentityReport) String() string {
	return fmt.Sprintf("%d asked, %d spans (%d delistings), %d new revisions stored, %d with no events, %d unusable, %d failed, %d left",
		r.Asked, r.Spans, r.Delistings, r.Stored, r.None, r.Unusable, r.Failed, r.Left)
}

// pause waits for the pace; it waits for nothing before the first request.
func (k *IdentityKeeper) pause(ctx context.Context, first bool) error {
	if first || k.Pace <= 0 {
		return ctx.Err()
	}
	select {
	case <-time.After(k.Pace):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pushedBack reports whether the provider refused or throttled the request.
func pushedBack(err error) bool {
	var he *httpx.Error
	return errors.As(err, &he) && (he.Class == httpx.Throttled || he.Class == httpx.Blocked)
}

// Fetch makes at most budget requests (all when it is zero or less), storing
// what each brings as soon as it comes, so that a stop loses nothing already
// asked for. When the plan lists the inactive tickers, each page is one request
// and the overviews of the tickers on it follow the named ones. It stops at
// once when the provider or the request budget pushes back; the requests not
// made are counted in Left, and so is the rest of a list not read to its end. A
// request that fails is counted and the run goes on.
func (k *IdentityKeeper) Fetch(ctx context.Context, plan IdentityPlan, budget int) (IdentityReport, error) {
	var rep IdentityReport
	var runErr error
	spent := 0
	take := func() bool {
		if budget > 0 && spent >= budget {
			return false
		}
		spent++
		return true
	}
	queue := append([]IdentityRequest(nil), plan.Requests...)
	if plan.ListInactive {
		next := ""
		for page := 0; ; page++ {
			if !take() {
				rep.Left++
				break
			}
			if err := k.pause(ctx, spent == 1); err != nil {
				rep.Left += len(queue) + 1
				return rep, err
			}
			got, err := k.Feed.Inactive(ctx, plan.After, next)
			rep.Asked++
			if pushedBack(err) {
				rep.Left += len(queue) + 1
				return rep, fmt.Errorf("marketdata: %s is pushing back, stopping: %w", k.Feed.Name(), err)
			}
			if err != nil {
				rep.Failed++
				rep.Left++
				runErr = fmt.Errorf("the list of inactive tickers: %w", err)
				break
			}
			for _, t := range got.Tickers {
				queue = append(queue, IdentityRequest{Ask: AskOverview, Ticker: t})
			}
			if next = got.Next; next == "" {
				break
			}
			if page == identityListPages-1 {
				runErr = fmt.Errorf("marketdata: Massive has more inactive tickers than %d pages hold; continue with -after", identityListPages)
				break
			}
		}
	}
	for i, req := range queue {
		if !take() {
			rep.Left += len(queue) - i
			break
		}
		if err := k.pause(ctx, spent == 1); err != nil {
			rep.Left += len(queue) - i
			return rep, err
		}
		var ans IdentityAnswer
		var err error
		if req.Ask == AskOverview {
			ans, err = k.Feed.Overview(ctx, req.Ticker)
		} else {
			ans, err = k.Feed.Events(ctx, req.Ticker)
		}
		rep.Asked++
		rep.Last = req.Ticker
		if pushedBack(err) {
			rep.Left += len(queue) - i - 1
			return rep, fmt.Errorf("marketdata: %s is pushing back, stopping: %w", k.Feed.Name(), err)
		}
		switch {
		case err != nil:
			rep.Failed++
			runErr = errors.Join(runErr, fmt.Errorf("%s %s: %w", req.Ticker, req.Ask, err))
			continue
		case !ans.Found:
			rep.None++
			continue
		case len(ans.Spans) == 0:
			rep.Unusable++
			continue
		}
		stored, err := k.Store.PutSymbolHistory(context.WithoutCancel(ctx), ans.Spans, k.now())
		if err != nil {
			return rep, err
		}
		rep.Spans += len(ans.Spans)
		rep.Stored += stored
		for _, s := range ans.Spans {
			if s.Kind == SpanDelisting {
				rep.Delistings++
			}
		}
	}
	return rep, runErr
}
