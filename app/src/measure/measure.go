// Package measure turns one bounded, read-only run over a month of public
// history into the feasibility report of PRD 7.1: what the free sources hold,
// what the current pipeline makes of it, what it cost to read, and what a
// six-month backfill would need.
//
// It computes only. The run that gathers the inputs (cmd/stoxicity measure)
// works in a scratch data folder, so nothing here touches a real database.
//
// The report keeps three reasons for a gap apart, because they call for
// different work:
//
//   - not recognized: the free data names the category (an 8-K item, a form),
//     but the current pipeline made no record of it. That is extraction work.
//   - not fetched: the category can only be told from a document's text or a
//     Form 4's XML, which this run does not download (or only samples). That is
//     sourcing work, still free.
//   - no free source: nothing free and authorized carries it. That is a
//     purchase decision (tier D).
package measure

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
)

// Filing is one filing the EDGAR daily indexes list for the window, with what
// the company's SEC record adds to it.
type Filing struct {
	Accession, CIK, Company, Form, Filed string
	// Tickers are the symbols the company list gives the CIK; none when it is
	// not listed (a private filer, a fund, a person filing a Form 4).
	Tickers []string
	// Asked is true when the company's SEC record was requested (the budget may
	// stop a run before every company is). InRecord is true when the record
	// listed the filing; Accepted is then its acceptance time, zero when not given.
	Asked    bool
	InRecord bool
	Accepted time.Time
	// Items are the 8-K item numbers from the record.
	Items []string
}

// Event is one event the pipeline stored, with the filings it rests on.
type Event struct {
	Kind       string
	Ticker     string
	Accessions []string
}

// Fact is one economic-event record of the event database.
type Fact struct {
	Category, Type, Symbol, Basis string
	Available                     time.Time
	Evidence                      []string
	Figures                       int
}

// InsiderSample is what reading a sample of the window's Form 4 filings gave.
type InsiderSample struct {
	Asked, Trades, Empty, Unreadable, Left int
}

// Prices says how many economic events have the daily bars a study needs.
type Prices struct {
	Checked   bool
	Note      string // why prices were not checked, or what limited them
	Benchmark string
	// Events with a symbol; with a bar on the entry session (the first session
	// after the event's day); with a bar 20 sessions later.
	Events, Entry, Horizon20 int
	// The same for the market benchmark on those days.
	BenchEntry, BenchHorizon20 int
}

// Host is the traffic to one host.
type Host struct {
	Requests, Failures int
	Bytes              int64
}

// Stage is one step of the run and how long it took.
type Stage struct {
	Name     string
	Duration time.Duration
	Note     string
}

// Inputs is everything one run gathered.
type Inputs struct {
	From, To  string
	Revision  string
	Started   time.Time
	Forms     []string
	Settings  []string // the budgets and choices of the run, as "name value"
	IndexDays struct{ Days, Complete, NonFiling, Unresolved int }
	Filings   []Filing
	Events    []Event
	Facts     []Fact
	Insiders  InsiderSample
	Prices    Prices
	Hosts     map[string]Host
	Stages    []Stage
	Stopped   string // why the run stopped early, if it did
	StoreSize int64  // bytes of the scratch database after the run
}

// Verdicts of a category.
const (
	Covered       = "covered"
	Partial       = "partly covered"
	NotRecognized = "not recognized"
	NotFetched    = "not fetched"
	NoFreeSource  = "no free source"
)

// Category is one PRD event category in the report.
type Category struct {
	Name string
	// Structured are the filings whose form or 8-K item names the category;
	// Companies the distinct CIKs among them.
	Structured, Companies int
	// TextOnly are filings that may hold the category but only their text tells.
	TextOnly int
	// Records are the economic events the pipeline made; Symbols the distinct
	// securities among them; WithFigures those that carry at least one figure.
	Records, Symbols, WithFigures int
	Verdict                       string
	Reason                        string
}

// FormCount is one form's filings and how many of them could be dated and named.
type FormCount struct {
	Form                            string
	Filings, Companies              int
	TimeKnown, DayOnly, NotInRecord int
	NotAsked                        int // the company's record was not requested
	WithTicker                      int
}

// Report is the feasibility report.
type Report struct {
	From, To       string
	Days           int
	Revision       string
	Generated      time.Time
	Settings       []string
	IndexDays      struct{ Days, Complete, NonFiling, Unresolved int }
	Forms          []FormCount
	Totals         FormCount
	ItemCounts     map[string]int // 8-K item number -> filings
	KindByItems    int            // 8-Ks whose items name an event kind
	KindAssigned   int            // ... of which the pipeline gave that kind
	PipelineEvents map[string]int // stored events by kind
	Categories     []Category
	Records        int // economic events in the window
	Known, Rebuilt int
	Insiders       InsiderSample
	Prices         Prices
	Hosts          map[string]Host
	Requests       int
	Bytes          int64
	Failures       int
	Stages         []Stage
	Elapsed        time.Duration
	Stopped        string
	StoreSize      int64
	Projection     Projection
	Recommendation string
	Conditions     []string
}

// Projection scales the run to six months.
type Projection struct {
	Factor            float64
	Filings, Requests int
	Bytes, Storage    int64
	Elapsed           time.Duration
	PriceRequests     int
	Cost              string
	Notes             []string
}

func (in Inputs) days() int {
	f, err1 := time.Parse(time.DateOnly, in.From)
	t, err2 := time.Parse(time.DateOnly, in.To)
	if err1 != nil || err2 != nil || t.Before(f) {
		return 0
	}
	return int(t.Sub(f).Hours()/24) + 1
}

// baseForm folds an amendment into its form: 8-K/A counts as 8-K.
func baseForm(f string) string { return strings.TrimSuffix(strings.ToUpper(f), "/A") }

func periodic(f string) bool {
	switch baseForm(f) {
	case "10-K", "10-Q", "10-KT", "10-QT":
		return true
	}
	return false
}

// Build computes the report.
func Build(in Inputs, now time.Time) Report {
	r := Report{From: in.From, To: in.To, Days: in.days(), Revision: in.Revision, Generated: now, Settings: in.Settings,
		IndexDays: in.IndexDays, ItemCounts: map[string]int{}, PipelineEvents: map[string]int{}, Insiders: in.Insiders,
		Prices: in.Prices, Hosts: in.Hosts, Stages: in.Stages, Stopped: in.Stopped, StoreSize: in.StoreSize}
	if !in.Started.IsZero() {
		r.Elapsed = now.Sub(in.Started)
	}

	// Filings by form, and the totals.
	byForm := map[string]*FormCount{}
	ciks := map[string]map[string]bool{}
	allCIKs := map[string]bool{}
	for _, f := range in.Filings {
		form := baseForm(f.Form)
		fc := byForm[form]
		if fc == nil {
			fc = &FormCount{Form: form}
			byForm[form] = fc
			ciks[form] = map[string]bool{}
		}
		fc.Filings++
		ciks[form][f.CIK] = true
		allCIKs[f.CIK] = true
		switch {
		case !f.Asked:
			fc.NotAsked++
		case !f.InRecord:
			fc.NotInRecord++
		case f.Accepted.IsZero():
			fc.DayOnly++
		default:
			fc.TimeKnown++
		}
		if len(f.Tickers) > 0 {
			fc.WithTicker++
		}
	}
	for form, fc := range byForm {
		fc.Companies = len(ciks[form])
		r.Forms = append(r.Forms, *fc)
		r.Totals.Filings += fc.Filings
		r.Totals.TimeKnown += fc.TimeKnown
		r.Totals.DayOnly += fc.DayOnly
		r.Totals.NotInRecord += fc.NotInRecord
		r.Totals.NotAsked += fc.NotAsked
		r.Totals.WithTicker += fc.WithTicker
	}
	r.Totals.Form, r.Totals.Companies = "all", len(allCIKs)
	sort.Slice(r.Forms, func(i, j int) bool { return r.Forms[i].Filings > r.Forms[j].Filings })

	// What the pipeline stored, by filing.
	kindsOf := map[string]map[string]bool{}
	for _, e := range in.Events {
		r.PipelineEvents[e.Kind]++
		for _, acc := range e.Accessions {
			if kindsOf[acc] == nil {
				kindsOf[acc] = map[string]bool{}
			}
			kindsOf[acc][e.Kind] = true
		}
	}
	for _, f := range in.Filings {
		if baseForm(f.Form) != "8-K" {
			continue
		}
		for _, it := range f.Items {
			r.ItemCounts[it]++
		}
		if k, ok := edgar.Kinds(f.Items); ok {
			r.KindByItems++
			if kindsOf[f.Accession][string(k)] {
				r.KindAssigned++
			}
		}
	}

	// The event database.
	type tally struct {
		records, figures int
		symbols          map[string]bool
	}
	got := map[string]*tally{}
	for _, f := range in.Facts {
		t := got[f.Category]
		if t == nil {
			t = &tally{symbols: map[string]bool{}}
			got[f.Category] = t
		}
		t.records++
		if f.Figures > 0 {
			t.figures++
		}
		if f.Symbol != "" {
			t.symbols[f.Symbol] = true
		}
		r.Records++
		if f.Basis == "known" {
			r.Known++
		} else {
			r.Rebuilt++
		}
	}

	// The categories: what the free data names, what the pipeline made of it.
	structured := map[string]map[string]bool{} // category -> accession set
	companies := map[string]map[string]bool{}
	textOnly := map[string]int{}
	mark := func(cat string, f Filing) {
		if structured[cat] == nil {
			structured[cat], companies[cat] = map[string]bool{}, map[string]bool{}
		}
		structured[cat][f.Accession] = true
		companies[cat][f.CIK] = true
	}
	for _, f := range in.Filings {
		form := baseForm(f.Form)
		switch {
		case periodic(form):
			mark("earnings", f)
		case form == "4":
			mark("insider", f)
		case form == "8-K":
			has := func(it string) bool { return slices.Contains(f.Items, it) }
			if has("2.02") {
				mark("earnings", f)
				textOnly["guidance"]++ // an earnings release often carries the outlook
			}
			if has("1.01") || has("1.02") {
				mark("contract", f) // a material agreement: a contract, but also a loan or a merger agreement
			}
			if has("7.01") || has("8.01") {
				textOnly["guidance"]++
				textOnly["buyback"]++
				textOnly["contract"]++
			}
		}
	}
	for _, name := range []string{"earnings", "guidance", "buyback", "contract", "insider", "analyst"} {
		c := Category{Name: name, Structured: len(structured[name]), Companies: len(companies[name]), TextOnly: textOnly[name]}
		if t := got[name]; t != nil {
			c.Records, c.Symbols, c.WithFigures = t.records, len(t.symbols), t.figures
		}
		c.Verdict, c.Reason = verdict(c, in.Insiders)
		r.Categories = append(r.Categories, c)
	}

	for _, h := range in.Hosts {
		r.Requests += h.Requests
		r.Bytes += h.Bytes
		r.Failures += h.Failures
	}
	r.Projection = project(r, in)
	r.Recommendation, r.Conditions = recommend(r)
	return r
}

// verdict names the category's state and the reason, from its counts.
func verdict(c Category, ins InsiderSample) (string, string) {
	switch c.Name {
	case "analyst":
		if c.Records > 0 {
			return Partial, "only revisions a stored article states; no free structured source of point-in-time revisions (ADR-0004)"
		}
		return NoFreeSource, "no free, authorized source of point-in-time analyst revisions (ADR-0004); a paid one is a tier D decision"
	case "insider":
		if c.Records == 0 && ins.Asked == 0 {
			return NotFetched, "Form 4 transactions are in each filing's XML, which this run did not read"
		}
		if ins.Left > 0 {
			return Partial, fmt.Sprintf("Form 4 XML read for a sample of %d filings; %d left unread", ins.Asked, ins.Left)
		}
	case "earnings":
		if c.Records > 0 && c.WithFigures*2 < c.Records {
			return Partial, fmt.Sprintf("%d of %d records carry reported figures; the rest say only that results were filed", c.WithFigures, c.Records)
		}
	case "guidance", "buyback":
		if c.Records == 0 {
			return NotFetched, "no form or 8-K item names it; only the release text tells, and this run reads no document text"
		}
		return Partial, "found only where a stored title or summary states it; the release text is not read"
	}
	switch {
	case c.Structured == 0 && c.TextOnly > 0 && c.Records == 0:
		return NotFetched, "only the document text tells, and this run reads none"
	case c.Structured > 0 && c.Records == 0:
		return NotRecognized, "the free data names it, but the pipeline made no record of it"
	case c.Companies > 0 && c.Symbols*2 < c.Companies:
		return Partial, fmt.Sprintf("records for %d securities against %d companies the free data names", c.Symbols, c.Companies)
	case c.Records > 0:
		return Covered, ""
	}
	return NotFetched, "nothing in the window"
}

// project scales the run to six months. Filing traffic scales with the number
// of days; the price check is one request per market day.
func project(r Report, in Inputs) Projection {
	p := Projection{Cost: "0 (free sources and free plans only)"}
	if r.Days == 0 {
		return p
	}
	p.Factor = 182.0 / float64(r.Days)
	scale := func(v float64) float64 { return math.Round(v * p.Factor) }
	p.Filings = int(scale(float64(r.Totals.Filings)))
	var sec, other int
	for host, h := range r.Hosts {
		if strings.Contains(host, "sec.gov") {
			sec += h.Requests
		} else {
			other += h.Requests
		}
	}
	p.Requests = int(scale(float64(sec)))
	p.Bytes = int64(scale(float64(r.Bytes)))
	p.Storage = int64(scale(float64(r.StoreSize)))
	p.Elapsed = time.Duration(scale(float64(r.Elapsed)))
	p.PriceRequests = 126 + 30 // six months of market days, plus the 20-session horizon and a margin
	p.Notes = []string{
		"Linear in days. The window is an earnings month, so filing counts are likely above a six-month average and the figures an upper estimate.",
		"Price bars: one request per market day on the free plan (five a minute), about half an hour for six months.",
		"Document text and Form 4 XML are not in these figures beyond the sample: reading all of them adds one request per filing.",
	}
	return p
}

// recommend gives GO, CONDITIONAL GO or NO-GO with the conditions.
func recommend(r Report) (string, []string) {
	var stop, cond []string
	if r.Stopped != "" {
		stop = append(stop, "the run stopped early: "+r.Stopped)
	}
	if r.IndexDays.Days > 0 && r.IndexDays.Unresolved*10 > r.IndexDays.Days {
		stop = append(stop, fmt.Sprintf("%d of %d index days could not be resolved", r.IndexDays.Unresolved, r.IndexDays.Days))
	}
	if r.Totals.Filings == 0 {
		stop = append(stop, "no filings were read")
	}
	if len(stop) > 0 {
		return "NO-GO", stop
	}
	if r.Totals.NotAsked > 0 {
		cond = append(cond, fmt.Sprintf("the SEC record of the company was not read for %d of %d filings (budget): their timing is unknown here", r.Totals.NotAsked, r.Totals.Filings))
	} else if r.Totals.Filings > 0 && r.Totals.TimeKnown*2 < r.Totals.Filings {
		cond = append(cond, fmt.Sprintf("acceptance times are known for only %d of %d filings; the rest are dated to their day", r.Totals.TimeKnown, r.Totals.Filings))
	}
	for _, c := range r.Categories {
		switch c.Verdict {
		case NotRecognized:
			cond = append(cond, fmt.Sprintf("%s: %s (extraction work)", c.Name, c.Reason))
		case NotFetched:
			cond = append(cond, fmt.Sprintf("%s: %s (free sourcing work)", c.Name, c.Reason))
		case NoFreeSource:
			cond = append(cond, fmt.Sprintf("%s: %s", c.Name, c.Reason))
		case Partial:
			cond = append(cond, fmt.Sprintf("%s: %s", c.Name, c.Reason))
		}
	}
	if r.KindByItems > 0 && r.KindAssigned*2 < r.KindByItems {
		cond = append(cond, fmt.Sprintf("the pipeline gave the kind the 8-K items name to %d of %d filings: backfilled filings carry no item numbers (extraction work)", r.KindAssigned, r.KindByItems))
	}
	if !r.Prices.Checked {
		cond = append(cond, "price coverage was not checked: "+r.Prices.Note)
	} else if r.Prices.Events > 0 && r.Prices.Horizon20*10 < r.Prices.Events*9 {
		cond = append(cond, fmt.Sprintf("only %d of %d events have a bar 20 sessions after entry", r.Prices.Horizon20, r.Prices.Events))
	}
	if len(cond) > 0 {
		return "CONDITIONAL GO", cond
	}
	return "GO", nil
}
