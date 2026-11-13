package eventfact

import (
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// EarningsExtractor names these rules.
const EarningsExtractor = "results-1"

// Figures are the reported numbers of one fiscal period, as they were known on a day.
type Figures struct {
	Period                           string // "Q3 2026", "FY2025"; may be empty
	Currency                         string // "USD"
	Revenue, NetIncome, EPS          *float64
	RevenueYoY, NetIncomeYoY, EPSYoY *float64 // changes, 0.05 is +5%

	// OperatingCashFlow is in Currency; the margins are of revenue, 0.1 is 10%. Each is nil when the
	// figures it needs are not known for the period.
	OperatingCashFlow                          *float64
	NetMargin, OperatingMargin, CashFlowMargin *float64

	// The margin changes are differences of ratios against the same period a year earlier: 0.02
	// means the margin rose by two percentage points. Each is nil unless both periods have both inputs.
	NetMarginChange, OperatingMarginChange, CashFlowMarginChange *float64
}

// EpisodeDays is how long after the first results publication of a security further results publications are taken for the same reporting period (the release, its 8-K and the 10-Q that follows).
const EpisodeDays = 30

// FromResults turns results events into one earnings record per security and reporting episode.
func FromResults(events []EventText, figures func(cik string, day string) (Figures, bool), surprise func([]Passage) (Type, []Fact, bool)) []Record {
	bySecurity := map[string][]EventText{}
	for _, e := range events {
		if e.Kind != model.EventResults {
			continue
		}
		security := e.SecurityID
		if security == "" {
			security = e.Symbol
		}
		if security == "" {
			continue
		}
		bySecurity[security] = append(bySecurity[security], e)
	}
	securities := make([]string, 0, len(bySecurity))
	for s := range bySecurity {
		securities = append(securities, s)
	}
	sort.Strings(securities)

	var out []Record
	for _, security := range securities {
		evs := bySecurity[security]
		sort.SliceStable(evs, func(i, j int) bool {
			if !evs[i].AvailableAt.Equal(evs[j].AvailableAt) {
				return evs[i].AvailableAt.Before(evs[j].AvailableAt)
			}
			return evs[i].EventID < evs[j].EventID
		})
		start := 0
		for i := 1; i <= len(evs); i++ {
			if i < len(evs) && evs[i].AvailableAt.Before(evs[start].AvailableAt.Add(EpisodeDays*24*time.Hour)) {
				continue
			}
			r := earningsRecord(security, evs[start:i], figures, surprise)
			if r.Validate() == nil {
				out = append(out, r)
			}
			start = i
		}
	}
	sortRecords(out)
	return out
}

func earningsRecord(security string, episode []EventText, figures func(string, string) (Figures, bool), surprise func([]Passage) (Type, []Fact, bool)) Record {
	first, last := episode[0], episode[len(episode)-1]
	key := NewKey(Earnings, security, first.AvailableAt.UTC().Format("2006-01-02"))

	seen := map[string]bool{}
	var evidence []string
	var passages []Passage
	for _, e := range episode {
		for _, ref := range append(append([]string(nil), e.Evidence...), e.SourceRef) {
			if ref != "" && !seen[ref] {
				seen[ref] = true
				evidence = append(evidence, ref)
			}
		}
		passages = append(passages, e.Passages...)
	}
	sort.Strings(evidence)

	basis := Reconstructed
	if first.Basis == Known {
		basis = Known
	}

	var facts []Fact
	if figures != nil && first.CIK != "" {
		if f, ok := figures(first.CIK, last.AvailableAt.UTC().Format("2006-01-02")); ok {
			add := func(name string, v *float64, unit string, derived bool) {
				if v != nil {
					facts = append(facts, Fact{Name: name, Value: *v, Unit: unit, Period: f.Period, Derived: derived})
				}
			}
			add("revenue", f.Revenue, f.Currency, false)
			add("net_income", f.NetIncome, f.Currency, false)
			add("eps", f.EPS, f.Currency+"/share", false)
			add("revenue_yoy", f.RevenueYoY, "ratio", true)
			add("net_income_yoy", f.NetIncomeYoY, "ratio", true)
			add("eps_yoy", f.EPSYoY, "ratio", true)
			add("net_margin", f.NetMargin, "ratio", true)
			add("operating_margin", f.OperatingMargin, "ratio", true)
			add("operating_cash_flow", f.OperatingCashFlow, f.Currency, false)
			add("cash_flow_margin", f.CashFlowMargin, "ratio", true)
			add("net_margin_change", f.NetMarginChange, "ratio", true)
			add("operating_margin_change", f.OperatingMarginChange, "ratio", true)
			add("cash_flow_margin_change", f.CashFlowMarginChange, "ratio", true)
		}
	}

	typ := EarningsReported
	if surprise != nil {
		if t, extra, ok := surprise(passages); ok && (t == EarningsBeat || t == EarningsMiss) {
			typ = t
			allowed := map[string]bool{}
			for _, fd := range t.Fields() {
				if fd.Unit != "label" {
					allowed[fd.Name] = true
				}
			}
			have := map[string]bool{}
			for _, f := range facts {
				have[f.Name] = true
			}
			for _, f := range extra {
				if allowed[f.Name] && !have[f.Name] {
					have[f.Name] = true
					facts = append(facts, f)
				}
			}
		}
	}

	return Record{
		ID:          ID(key),
		Key:         key,
		Category:    Earnings,
		Type:        typ,
		SecurityID:  first.SecurityID,
		Symbol:      first.Symbol,
		CIK:         first.CIK,
		EventID:     first.EventID,
		PublishedAt: first.PublishedAt,
		AvailableAt: first.AvailableAt,
		Precision:   first.Precision,
		Source:      first.Source,
		SourceRef:   first.SourceRef,
		Evidence:    evidence,
		Facts:       facts,
		Basis:       basis,
		Extractor:   EarningsExtractor,
		Version:     Version,
	}
}
