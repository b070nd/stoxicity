// Package eventfact is the historical event database's record: one economic
// event of one security, with the figures that describe it and the time it
// could first have been acted on.
//
// The event model of package model answers "what happened, and what rests on
// what". It deliberately holds no figures. This package adds them, for the five
// categories whose market reaction is studied: earnings and guidance, buybacks,
// contracts, insider transactions and analyst revisions.
//
// The rules that keep a record usable for a historical study:
//
//   - A record is an economic event, not an article. Key is the economic
//     identity; every article, filing or amendment that reports the same thing
//     lands on the same Key and is listed as Evidence. Counting records therefore
//     never counts one event twice.
//   - PublishedAt is when the source made it public. AvailableAt is the earliest
//     time a reader could have acted on it and is never before PublishedAt.
//     Precision says whether that is a time or only a day; a day is never turned
//     into a time by assumption.
//   - Basis says whether the installation held the information then (known) or
//     rebuilt it later from public history (reconstructed). The two are never
//     merged silently.
//   - A figure is kept with the words it was read from (Text, Span) or with the
//     structured source it came from. A figure that the source does not state is
//     absent; nothing is estimated here.
//   - Extractor and Version name the rules that made the record, so a change of
//     the rules makes a new revision and the earlier one stays readable.
//
// The package is pure: it reads what the caller supplies and returns records.
// Study parameters such as the entry delay and the holding period are not part
// of an event; they belong to the study that reads it (package eventstudy).
package eventfact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Version names the record format and the meaning of the types and fields.
const Version = "eventfact-1"

// Category is one of the five families of events that are studied.
type Category string

// The categories.
const (
	Earnings Category = "earnings"
	Guidance Category = "guidance"
	Buyback  Category = "buyback"
	Contract Category = "contract"
	Insider  Category = "insider"
	Analyst  Category = "analyst"
)

// Type is what happened, from a fixed list, so that events can be grouped and
// compared across companies and years.
type Type string

// The types. A type that needs a comparison the source does not state (a
// surprise needs a consensus estimate) is only given when the source states it.
const (
	EarningsReported Type = "earnings_reported" // results were published; no surprise is known
	EarningsBeat     Type = "earnings_beat"     // the source says results were above expectations
	EarningsMiss     Type = "earnings_miss"     // the source says results were below expectations

	GuidanceIssued     Type = "guidance_issued"     // a first figure for the period
	GuidanceUpgrade    Type = "guidance_upgrade"    // the new figure is above the previous one
	GuidanceDowngrade  Type = "guidance_downgrade"  // the new figure is below the previous one
	GuidanceReaffirmed Type = "guidance_reaffirmed" // the figure was repeated unchanged
	GuidanceWithdrawn  Type = "guidance_withdrawn"  // the company no longer gives a figure

	BuybackAnnounced Type = "buyback_announced" // a new repurchase authorisation
	BuybackExpanded  Type = "buyback_expanded"  // an existing authorisation was raised

	ContractAward       Type = "contract_award"       // a contract or order was won or signed
	ContractTermination Type = "contract_termination" // a material contract ended early

	InsiderBuy  Type = "insider_buy"  // an officer, director or large holder bought common shares
	InsiderSell Type = "insider_sell" // an officer, director or large holder sold common shares

	AnalystUpgrade   Type = "analyst_upgrade"   // a rating was raised
	AnalystDowngrade Type = "analyst_downgrade" // a rating was lowered
	TargetRaised     Type = "target_raised"     // a price target was raised
	TargetCut        Type = "target_cut"        // a price target was lowered
	EstimateRaised   Type = "estimate_raised"   // an earnings estimate was raised
	EstimateCut      Type = "estimate_cut"      // an earnings estimate was lowered
)

// Field describes one figure or label a type may carry.
type Field struct {
	Name string
	// Unit is what the value is measured in: "currency" (the fact then names the
	// currency), "currency_per_share", "shares", "ratio" (0.05 is 5%), "years",
	// "count", or "label" for a word kept in Record.Labels instead of a figure.
	Unit    string
	Meaning string
}

type typeInfo struct {
	category Category
	fields   []Field
}

func figures(category Category, fields ...Field) typeInfo {
	return typeInfo{category: category, fields: fields}
}

func schema() map[Type]typeInfo {
	earningsFields := []Field{
		{"revenue", "currency", "revenue of the period"},
		{"net_income", "currency", "net income of the period"},
		{"eps", "currency_per_share", "earnings per share of the period"},
		{"revenue_yoy", "ratio", "revenue against the same period a year before, as a change"},
		{"net_income_yoy", "ratio", "net income against the same period a year before, as a change"},
		{"eps_yoy", "ratio", "earnings per share against the same period a year before, as a change"},
		{"net_margin", "ratio", "net income over revenue of the period"},
		{"operating_margin", "ratio", "operating income over revenue of the period"},
		{"operating_cash_flow", "currency", "net cash from operating activities of the period"},
		{"cash_flow_margin", "ratio", "operating cash flow over revenue of the period"},
		{"eps_surprise", "ratio", "earnings per share against the stated consensus, as a change; only when the source states it"},
		{"revenue_surprise", "ratio", "revenue against the stated consensus, as a change; only when the source states it"},
	}
	guidanceFields := []Field{
		{"previous_guidance", "currency", "the figure the company gave before; the midpoint when it gave a range"},
		{"new_guidance", "currency", "the figure the company gives now; the midpoint when it gives a range"},
		{"new_guidance_low", "currency", "the lower end of the new range"},
		{"new_guidance_high", "currency", "the upper end of the new range"},
		{"change", "ratio", "new against previous, as a change"},
		{"metric", "label", "what is guided: eps, revenue, margin, cash_flow or other"},
	}
	buybackFields := []Field{
		{"amount", "currency", "the size of the authorisation"},
		{"shares", "shares", "the number of shares authorised, when the programme is stated in shares"},
		{"previous_amount", "currency", "the earlier authorisation, when this one raises it"},
		{"pct_of_market_cap", "ratio", "the amount against the market capitalisation at the time, when that is known; worked out when a study reads the event, not stored"},
		{"pct_of_shares_outstanding", "ratio", "the shares authorised against the shares outstanding at the time, when both are known; worked out when a study reads the event, not stored"},
		{"financing", "label", "how it is paid for: cash, debt, mixed or unknown"},
		{"kind", "label", "authorization when the board authorised a programme, executed when the company reports shares already bought; absent when the text says neither"},
	}
	contractFields := []Field{
		{"value", "currency", "the stated total value"},
		{"duration_years", "years", "the stated length"},
		{"annual_value", "currency", "value divided by duration, when both are stated"},
		{"revenue_impact", "ratio", "annual value against the last annual revenue, when that is known"},
		{"pct_of_revenue", "ratio", "annual value against the revenue of the latest fiscal year filed by then, when both are known; worked out when a study reads the event, not stored"},
		{"pct_of_market_cap", "ratio", "the stated total value against the market capitalisation at the time, when that is known; worked out when a study reads the event, not stored"},
		{"counterparty", "label", "the other side, as the source names it"},
		{"customer", "label", "government, commercial or unknown"},
		{"certainty", "label", "firm (awarded, signed, order received), conditional (subject to approval, financing or options) or framework (indefinite-quantity or master agreement, letter of intent, a ceiling value); absent when the text supports none"},
		{"value_kind", "label", "ceiling when the stated value is a maximum (up to, maximum); absent otherwise"},
	}
	insiderFields := []Field{
		{"value", "currency", "shares times price, summed over the transactions of the filing"},
		{"shares", "shares", "shares bought or sold"},
		{"price", "currency_per_share", "the share-weighted average price"},
		{"pct_of_holding", "ratio", "shares traded against the shares held before the trades"},
		{"transactions", "count", "how many transactions the filing reports in this direction"},
		{"role", "label", "officer, director, owner (a holder of 10% or more) or other; the strongest of them"},
		{"plan", "label", "yes when every transaction was under a Rule 10b5-1 plan, no when none was, mixed otherwise"},
		{"holding", "label", "direct, indirect or mixed"},
	}
	ratingFields := []Field{
		{"firm", "label", "who published the revision"},
		{"previous_rating", "label", "the rating before"},
		{"new_rating", "label", "the rating now"},
	}
	targetFields := []Field{
		{"firm", "label", "who published the revision"},
		{"previous_target", "currency_per_share", "the price target before"},
		{"new_target", "currency_per_share", "the price target now"},
		{"change", "ratio", "new against previous, as a change"},
	}
	estimateFields := []Field{
		{"firm", "label", "who published the revision"},
		{"previous_estimate", "currency_per_share", "the estimate before"},
		{"new_estimate", "currency_per_share", "the estimate now"},
		{"change", "ratio", "new against previous, as a change"},
	}
	return map[Type]typeInfo{
		EarningsReported: figures(Earnings, earningsFields...),
		EarningsBeat:     figures(Earnings, earningsFields...),
		EarningsMiss:     figures(Earnings, earningsFields...),

		GuidanceIssued:     figures(Guidance, guidanceFields...),
		GuidanceUpgrade:    figures(Guidance, guidanceFields...),
		GuidanceDowngrade:  figures(Guidance, guidanceFields...),
		GuidanceReaffirmed: figures(Guidance, guidanceFields...),
		GuidanceWithdrawn:  figures(Guidance, guidanceFields...),

		BuybackAnnounced: figures(Buyback, buybackFields...),
		BuybackExpanded:  figures(Buyback, buybackFields...),

		ContractAward:       figures(Contract, contractFields...),
		ContractTermination: figures(Contract, contractFields...),

		InsiderBuy:  figures(Insider, insiderFields...),
		InsiderSell: figures(Insider, insiderFields...),

		AnalystUpgrade:   figures(Analyst, ratingFields...),
		AnalystDowngrade: figures(Analyst, ratingFields...),
		TargetRaised:     figures(Analyst, targetFields...),
		TargetCut:        figures(Analyst, targetFields...),
		EstimateRaised:   figures(Analyst, estimateFields...),
		EstimateCut:      figures(Analyst, estimateFields...),
	}
}

// Valid reports whether t is one of the types.
func (t Type) Valid() bool { _, ok := schema()[t]; return ok }

// Category is the family t belongs to; empty when t is not a type.
func (t Type) Category() Category { return schema()[t].category }

// Fields are the figures and labels a record of type t may carry, in a fixed
// order; nil when t is not a type.
func (t Type) Fields() []Field { return append([]Field(nil), schema()[t].fields...) }

// Types returns every type, sorted by name.
func Types() []Type {
	m := schema()
	out := make([]Type, 0, len(m))
	for t := range m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TypesOf returns the types of one category, sorted by name.
func TypesOf(c Category) []Type {
	var out []Type
	for _, t := range Types() {
		if t.Category() == c {
			out = append(out, t)
		}
	}
	return out
}

// Source says what kind of publication a record rests on.
type Source string

// The sources, strongest first.
const (
	SourceFiling  Source = "official_filing"          // a filing with a regulator
	SourceCompany Source = "official_company_release" // the issuer's own release
	SourceWire    Source = "wire"                     // a news agency, as attributed in the text
	SourceMedia   Source = "reported"                 // any other publisher
	SourceImport  Source = "import"                   // a file the user supplied
)

// Basis says how the record came to be held.
type Basis string

// The bases. They carry the same meaning as the replay kinds of package walkfwd.
const (
	Known         Basis = "known"         // stored when it was published; the installation had it then
	Reconstructed Basis = "reconstructed" // rebuilt later from public history
)

// Precisions of AvailableAt.
const (
	PrecisionTime = "time"
	PrecisionDay  = "day"
)

// Span is where in a source text a figure was read.
type Span struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// Fact is one figure of an event. Value is a float because it is a feature for
// statistics, not an amount that is booked; the words it was read from stay in
// Text so that the figure can be checked against the source.
type Fact struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	// Unit is the currency code for money ("USD"), "USD/share" style for a price,
	// or the unit of the field ("shares", "ratio", "years", "count").
	Unit string `json:"unit"`
	// Period is what the figure is for, as the source names it ("FY2024",
	// "Q3 2026"); empty when it has none.
	Period string `json:"period,omitempty"`
	// Text is the phrase the figure was read from; empty for a structured source.
	Text string `json:"text,omitempty"`
	// Span locates Text in the evidence it was read from; nil for a structured
	// source or a derived figure.
	Span *Span `json:"span,omitempty"`
	// Derived says the figure was computed from other figures of the record or
	// from stored facts (a ratio, an average), not stated by the source.
	Derived bool `json:"derived,omitempty"`
}

// Record is one economic event of one security.
type Record struct {
	// ID is derived from Key and is the same for every revision of the event.
	ID string `json:"id"`
	// Key is the economic identity. Two reports of the same thing have the same
	// Key, whatever their wording, publisher or filing.
	Key      string   `json:"key"`
	Category Category `json:"category"`
	Type     Type     `json:"event_type"`

	SecurityID string `json:"security_id,omitempty"`
	Symbol     string `json:"symbol"`
	CIK        string `json:"cik,omitempty"`
	// EventID is the stored event this record elaborates; empty when the record
	// comes from a structured filing that makes no event of its own.
	EventID string `json:"event_id,omitempty"`

	PublishedAt time.Time `json:"published_at"`
	AvailableAt time.Time `json:"available_at"`
	Precision   string    `json:"precision"`

	Source Source `json:"source"`
	// SourceRef identifies the publication the record was read from: an accession
	// number or an article ID.
	SourceRef string `json:"source_ref"`
	// Evidence lists every publication that reports the event, SourceRef
	// included, sorted.
	Evidence []string `json:"evidence"`

	Facts  []Fact            `json:"facts,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`

	Basis     Basis  `json:"basis"`
	Extractor string `json:"extractor"`
	Version   string `json:"version"`
}

// ID is the identifier of the event with the economic identity key.
func ID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "ef_" + hex.EncodeToString(sum[:8])
}

// Fact returns the figure with the name; false when the record has none.
func (r Record) Fact(name string) (Fact, bool) {
	for _, f := range r.Facts {
		if f.Name == name {
			return f, true
		}
	}
	return Fact{}, false
}

// Validate says why the record cannot be stored: an unknown type, a figure or
// label the type does not have, a time that cannot be right.
func (r Record) Validate() error {
	info, ok := schema()[r.Type]
	switch {
	case !ok:
		return fmt.Errorf("eventfact: %q is not an event type", r.Type)
	case r.Category != info.category:
		return fmt.Errorf("eventfact: type %s belongs to category %s, not %q", r.Type, info.category, r.Category)
	case r.Key == "" || r.ID != ID(r.Key):
		return errors.New("eventfact: the record needs a key, and its ID must be the ID of that key")
	case r.Symbol == "" && r.SecurityID == "":
		return errors.New("eventfact: the record names no security")
	case r.PublishedAt.IsZero() || r.AvailableAt.IsZero():
		return errors.New("eventfact: the record needs the time it was published and the time it was available")
	case r.AvailableAt.Before(r.PublishedAt):
		return errors.New("eventfact: a record cannot be available before it was published")
	case r.Precision != PrecisionTime && r.Precision != PrecisionDay:
		return fmt.Errorf("eventfact: precision %q is not time or day", r.Precision)
	case r.Basis != Known && r.Basis != Reconstructed:
		return fmt.Errorf("eventfact: basis %q is not known or reconstructed", r.Basis)
	case r.Source == "" || r.SourceRef == "":
		return errors.New("eventfact: the record names no source")
	case r.Extractor == "" || r.Version == "":
		return errors.New("eventfact: the record does not say which rules made it")
	}
	units := map[string]string{}
	for _, f := range info.fields {
		units[f.Name] = f.Unit
	}
	seen := map[string]bool{}
	for _, f := range r.Facts {
		unit, ok := units[f.Name]
		switch {
		case !ok || unit == "label":
			return fmt.Errorf("eventfact: type %s has no figure %q", r.Type, f.Name)
		case seen[f.Name]:
			return fmt.Errorf("eventfact: figure %q is given twice", f.Name)
		case f.Unit == "":
			return fmt.Errorf("eventfact: figure %q has no unit", f.Name)
		case f.Value != f.Value || f.Value-f.Value != 0:
			return fmt.Errorf("eventfact: figure %q is not a number", f.Name)
		}
		seen[f.Name] = true
	}
	for name := range r.Labels {
		if units[name] != "label" {
			return fmt.Errorf("eventfact: type %s has no label %q", r.Type, name)
		}
	}
	return nil
}

// Hash is a digest of everything the record says. Two records with the same ID
// and the same Hash are the same revision.
func Hash(r Record) string {
	c := r
	c.Evidence = append([]string(nil), r.Evidence...)
	sort.Strings(c.Evidence)
	c.Facts = append([]Fact(nil), r.Facts...)
	sort.SliceStable(c.Facts, func(i, j int) bool { return c.Facts[i].Name < c.Facts[j].Name })
	c.PublishedAt, c.AvailableAt = r.PublishedAt.UTC(), r.AvailableAt.UTC()
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Merge joins two reports of one economic event: the earlier availability
// wins (the event was actionable from the first report), the stronger source
// supplies the figures, and the evidence of both is kept. It fails when the two
// do not have the same Key.
func Merge(a, b Record) (Record, error) {
	if a.Key != b.Key {
		return Record{}, fmt.Errorf("eventfact: cannot merge %s and %s: different events", a.ID, b.ID)
	}
	lead, other := a, b
	if strength(b.Source) > strength(a.Source) || (strength(b.Source) == strength(a.Source) && len(b.Facts) > len(a.Facts)) {
		lead, other = b, a
	}
	out := lead
	if other.AvailableAt.Before(out.AvailableAt) {
		out.AvailableAt, out.Precision = other.AvailableAt, other.Precision
	}
	if other.PublishedAt.Before(out.PublishedAt) {
		out.PublishedAt = other.PublishedAt
	}
	if other.Basis == Known {
		out.Basis = Known
	}
	seen := map[string]bool{}
	out.Evidence = nil
	for _, e := range append(append([]string(nil), lead.Evidence...), other.Evidence...) {
		if e != "" && !seen[e] {
			seen[e] = true
			out.Evidence = append(out.Evidence, e)
		}
	}
	sort.Strings(out.Evidence)
	out.Facts = append([]Fact(nil), lead.Facts...)
	for _, f := range other.Facts {
		if _, ok := out.Fact(f.Name); !ok {
			out.Facts = append(out.Facts, f)
		}
	}
	if len(other.Labels) > 0 {
		labels := map[string]string{}
		for k, v := range other.Labels {
			labels[k] = v
		}
		for k, v := range lead.Labels {
			labels[k] = v
		}
		out.Labels = labels
	}
	return out, nil
}

func strength(s Source) int {
	switch s {
	case SourceFiling:
		return 5
	case SourceCompany:
		return 4
	case SourceWire:
		return 3
	case SourceMedia:
		return 2
	case SourceImport:
		return 1
	}
	return 0
}

// Collapse merges the records that share a Key and returns one record per
// economic event, sorted by availability and then by ID.
func Collapse(records []Record) ([]Record, error) {
	byKey := map[string]Record{}
	var order []string
	for _, r := range records {
		cur, ok := byKey[r.Key]
		if !ok {
			byKey[r.Key] = r
			order = append(order, r.Key)
			continue
		}
		m, err := Merge(cur, r)
		if err != nil {
			return nil, err
		}
		byKey[r.Key] = m
	}
	out := make([]Record, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].AvailableAt.Equal(out[j].AvailableAt) {
			return out[i].AvailableAt.Before(out[j].AvailableAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// NewKey builds an economic identity from its parts, lower-cased and joined so
// that no part can run into the next.
func NewKey(category Category, parts ...string) string {
	clean := make([]string, 0, len(parts)+1)
	clean = append(clean, string(category))
	for _, p := range parts {
		clean = append(clean, strings.ToLower(strings.TrimSpace(strings.ReplaceAll(p, "|", "/"))))
	}
	return strings.Join(clean, "|")
}
