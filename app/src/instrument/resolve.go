package instrument

import (
	"fmt"
	"sort"
	"strings"
)

// Registry resolves identifiers against a validated set.
type Registry struct {
	set    Set
	issuer map[string]Issuer
	sec    map[string]Security
}

// New validates the set and builds a registry; any error stops it.
func New(s Set) (*Registry, []Problem, error) {
	ps := Validate(s)
	if errs := Errors(ps); len(errs) > 0 {
		return nil, ps, fmt.Errorf("instrument: %d errors, first: %s", len(errs), errs[0])
	}
	s = Normalize(s)
	r := &Registry{set: s, issuer: map[string]Issuer{}, sec: map[string]Security{}}
	for _, i := range s.Issuers {
		r.issuer[i.ID] = i
	}
	for _, x := range s.Securities {
		r.sec[x.ID] = x
	}
	return r, ps, nil
}

// Set returns the instruments in the registry.
func (r *Registry) Set() Set { return r.set }

// Status of a resolution.
type Status string

const (
	Resolved  Status = "resolved"
	Ambiguous Status = "ambiguous" // more than one instrument fits
	NotFound  Status = "not_found"
	Conflict  Status = "conflict" // identifiers that each match something, but not the same thing
	Invalid   Status = "invalid"  // an identifier is malformed
)

// Query names what to look for. Identifiers are conjunctive: each one given must
// fit. The day says which listings count; it is required because tickers are
// reused.
type Query struct {
	ISIN     string
	MIC      string
	Ticker   string
	Currency string // the currency the listing trades in
	Broker   string
	ConID    string
	Day      string // YYYY-MM-DD
}

// Candidate is one listing that fits, with what tells it apart from the others.
type Candidate struct {
	Listing  Listing  `json:"listing"`
	Security Security `json:"security"`
	Issuer   Issuer   `json:"issuer"`
}

// Result is the outcome. Listing and Security are set only when Status is
// resolved (Listing only for a listing-level question).
type Result struct {
	Status     Status      `json:"status"`
	Listing    *Listing    `json:"listing,omitempty"`
	Security   *Security   `json:"security,omitempty"`
	Issuer     *Issuer     `json:"issuer,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"`
	Reasons    []string    `json:"reasons"`
}

func (r *Registry) candidate(l Listing) Candidate {
	s := r.sec[l.SecurityID]
	return Candidate{Listing: l, Security: s, Issuer: r.issuer[s.IssuerID]}
}

func describe(c Candidate) string {
	d := fmt.Sprintf("%s %s on %s in %s (%s)", c.Security.Name, c.Listing.Ticker, c.Listing.MIC, c.Listing.Currency, c.Listing.QuoteUnit)
	if c.Security.ShareClass != "" {
		d += ", class " + c.Security.ShareClass
	}
	if c.Security.Distribution == Distributing || c.Security.Distribution == Accumulating {
		d += ", " + string(c.Security.Distribution)
	}
	if c.Security.ISIN != "" {
		d += ", " + c.Security.ISIN
	}
	return d
}

type matcher struct {
	name string
	fn   func(Listing, Security) bool
}

func (q Query) matchers() ([]matcher, error) {
	var ms []matcher
	if q.ISIN != "" {
		if err := ValidISIN(q.ISIN); err != nil {
			return nil, err
		}
		isin := q.ISIN
		ms = append(ms, matcher{"ISIN " + isin, func(_ Listing, s Security) bool { return s.ISIN == isin }})
	}
	if q.MIC != "" {
		if err := ValidMIC(q.MIC); err != nil {
			return nil, err
		}
		mic := q.MIC
		ms = append(ms, matcher{"venue " + mic, func(l Listing, _ Security) bool { return l.MIC == mic }})
	}
	if q.Ticker != "" {
		t := strings.ToUpper(strings.TrimSpace(q.Ticker))
		ms = append(ms, matcher{"ticker " + t, func(l Listing, _ Security) bool { return l.Ticker == t }})
	}
	if q.Currency != "" {
		c := q.Currency
		ms = append(ms, matcher{"currency " + c, func(l Listing, _ Security) bool { return l.Currency == c }})
	}
	if q.ConID != "" {
		if q.Broker == "" {
			return nil, fmt.Errorf("a broker contract id needs the broker")
		}
		b, id := q.Broker, q.ConID
		ms = append(ms, matcher{"contract " + b + "/" + id, func(l Listing, _ Security) bool {
			for _, c := range l.Contracts {
				if c.Broker == b && c.ConID == id {
					return true
				}
			}
			return false
		}})
	}
	return ms, nil
}

func (r *Registry) filter(day string, ms []matcher) []Listing {
	var out []Listing
	for _, l := range r.set.Listings {
		if !l.Active(day) {
			continue
		}
		s := r.sec[l.SecurityID]
		ok := true
		for _, m := range ms {
			if !m.fn(l, s) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// resolve runs the common part; level says what the question needs.
func (r *Registry) resolve(q Query, listingLevel bool) Result {
	if !validDay(q.Day) {
		return Result{Status: Invalid, Reasons: []string{fmt.Sprintf("the day %q is not a date (YYYY-MM-DD); tickers are reused, so the day is required", q.Day)}}
	}
	ms, err := q.matchers()
	if err != nil {
		return Result{Status: Invalid, Reasons: []string{err.Error()}}
	}
	if len(ms) == 0 {
		return Result{Status: Invalid, Reasons: []string{"give at least an ISIN, a ticker or a broker contract"}}
	}
	hits := r.filter(q.Day, ms)
	if len(hits) == 0 {
		return r.explainMiss(q, ms)
	}
	var cands []Candidate
	secs := map[string]bool{}
	for _, l := range hits {
		cands = append(cands, r.candidate(l))
		secs[l.SecurityID] = true
	}
	if len(secs) == 1 && (!listingLevel || len(hits) == 1) {
		c := cands[0]
		res := Result{Status: Resolved, Security: &c.Security, Issuer: &c.Issuer, Reasons: []string{}}
		if listingLevel {
			res.Listing = &c.Listing
		} else if len(hits) > 1 {
			res.Candidates = cands
			res.Reasons = append(res.Reasons, fmt.Sprintf("%d listings of this security; add a venue or currency to choose one", len(hits)))
		}
		return res
	}
	res := Result{Status: Ambiguous, Candidates: cands}
	if len(secs) > 1 {
		res.Reasons = append(res.Reasons, fmt.Sprintf("%d different securities fit; add an ISIN or a venue to tell them apart", len(secs)))
	} else {
		res.Reasons = append(res.Reasons, fmt.Sprintf("%d listings of the same security fit; add a venue or a currency", len(hits)))
	}
	for _, c := range cands {
		res.Reasons = append(res.Reasons, describe(c))
	}
	return res
}

// explainMiss says whether nothing matches, or whether each identifier matches
// something but not the same thing.
func (r *Registry) explainMiss(q Query, ms []matcher) Result {
	if len(ms) < 2 {
		return Result{Status: NotFound, Reasons: []string{ms[0].name + " matches no instrument on " + q.Day}}
	}
	var alone, none []string
	var conflicting []Candidate
	for _, m := range ms {
		hits := r.filter(q.Day, []matcher{m})
		if len(hits) == 0 {
			none = append(none, m.name)
			continue
		}
		alone = append(alone, m.name)
		for _, l := range hits {
			conflicting = append(conflicting, r.candidate(l))
		}
	}
	if len(none) > 0 {
		return Result{Status: NotFound, Reasons: []string{strings.Join(none, ", ") + " matches no instrument on " + q.Day}}
	}
	res := Result{Status: Conflict, Reasons: []string{fmt.Sprintf("%s each match an instrument, but no instrument fits all of them", strings.Join(alone, ", "))}}
	seen := map[string]bool{}
	for _, c := range conflicting {
		if !seen[c.Listing.ID] {
			seen[c.Listing.ID] = true
			res.Candidates = append(res.Candidates, c)
			res.Reasons = append(res.Reasons, describe(c))
		}
	}
	return res
}

// ResolveListing finds the one listing the query names. An identifier that fits
// several is an ambiguity, never a pick of the first.
func (r *Registry) ResolveListing(q Query) Result { return r.resolve(q, true) }

// ResolveSecurity finds the one security the query names; an ISIN alone is
// enough, and its listings are returned as candidates.
func (r *Registry) ResolveSecurity(q Query) Result { return r.resolve(q, false) }
