package eventfact

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// AnalystExtractor names the rules that turn a file of analyst revisions into
// records. A change of what they read makes a new revision of each event.
const AnalystExtractor = "analyst-import-1"

// analystHeldWithin is how long after publication the owner's system may have
// recorded a revision and still count as holding it when it was published. It
// is the same 72 hours that research.Build allows for a fetched article.
const analystHeldWithin = 72 * time.Hour

// AnalystColumns are the columns of an analyst-revision file: the ones every
// file must have, then the ones it may have.
func AnalystColumns() (required, optional []string) {
	return []string{"firm", "symbol", "kind", "published_at", "source_ref"},
		[]string{"cik", "available_at", "held_at", "previous_rating", "new_rating", "previous_target", "new_target",
			"previous_estimate", "new_estimate", "metric", "period", "currency"}
}

// AnalystImport is what a file of analyst revisions became.
type AnalystImport struct {
	// Records are the revisions that passed, one per economic event, in the order
	// their rows first appear in the file.
	Records []Record
	// Rejected counts the rows that were left out, by the reason.
	Rejected map[string]int
}

// FromAnalystRows turns the rows of an analyst-revision file (columns as in
// AnalystColumns, plus "_line", the line of the row) into records, one per
// revision. Every record has source import. A row that cannot be a valid
// record is left out and counted by reason in Rejected; nothing is skipped
// silently.
//
// Point in time: PublishedAt is published_at. AvailableAt is available_at when
// the row states it, otherwise published_at itself; Precision is time when the
// row states a time (published_at with a time, or available_at) and day when
// published_at is only a date. A date is read as the start of that day in UTC
// and never turned into a time. Basis is known when the row states held_at, the
// time the owner's system held the revision, and that is no later than 72 hours
// after published_at; otherwise it is reconstructed.
//
// Key is the symbol, firm, kind, the day of published_at as the file writes it,
// the period and, for an estimate, the metric, so that a corrected row of the
// same revision is the same event. Of two rows with one key the later one wins.
// Figures are only those the row states, in the unit of its currency; nothing is
// computed, and no comparison with a consensus is made. security looks the
// symbol up and gives the issuer number the reference data holds for it; false
// means the symbol is not known.
func FromAnalystRows(rows []map[string]string, security func(symbol string) (cik string, ok bool)) AnalystImport {
	out := AnalystImport{Rejected: map[string]int{}}
	index := map[string]int{}
	for _, row := range rows {
		rec, reason := analystRecord(row, security)
		if reason != "" {
			out.Rejected[reason]++
			continue
		}
		if i, dup := index[rec.Key]; dup {
			out.Rejected["the same revision is on a later row, which replaces this one"]++
			out.Records[i] = rec
			continue
		}
		index[rec.Key] = len(out.Records)
		out.Records = append(out.Records, rec)
	}
	return out
}

func analystKind(s string) (Type, bool) {
	switch s {
	case "upgrade":
		return AnalystUpgrade, true
	case "downgrade":
		return AnalystDowngrade, true
	case "target_raised":
		return TargetRaised, true
	case "target_cut":
		return TargetCut, true
	case "estimate_raised":
		return EstimateRaised, true
	case "estimate_cut":
		return EstimateCut, true
	}
	return "", false
}

// analystTime reads an RFC 3339 time or a plain day; the precision says which.
func analystTime(s string) (time.Time, string, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, PrecisionTime, true
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, PrecisionDay, true
	}
	return time.Time{}, "", false
}

// analystFigure reads a stated number; stated is false for an empty cell and
// ok false for one that is not a finite number.
func analystFigure(row map[string]string, col string) (v float64, stated, ok bool) {
	s := row[col]
	if s == "" {
		return 0, false, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, true, false
	}
	return v, true, true
}

func analystRecord(row map[string]string, security func(string) (string, bool)) (Record, string) {
	typ, ok := analystKind(strings.ToLower(row["kind"]))
	if !ok {
		return Record{}, "kind is not upgrade, downgrade, target_raised, target_cut, estimate_raised or estimate_cut"
	}
	symbol := strings.ToUpper(row["symbol"])
	firm := row["firm"]
	switch {
	case symbol == "":
		return Record{}, "no symbol"
	case firm == "":
		return Record{}, "no firm"
	case row["source_ref"] == "":
		return Record{}, "no source_ref"
	}
	cik, known := security(symbol)
	if !known {
		return Record{}, "symbol is not in the reference tickers"
	}
	if row["cik"] != "" {
		if cik != "" && row["cik"] != cik {
			return Record{}, "cik differs from the one the reference tickers give for the symbol"
		}
		cik = row["cik"]
	}

	published, precision, ok := analystTime(row["published_at"])
	if !ok {
		return Record{}, "published_at is not an RFC 3339 time with an offset or a YYYY-MM-DD day"
	}
	available := published
	if row["available_at"] != "" {
		t, err := time.Parse(time.RFC3339, row["available_at"])
		if err != nil {
			return Record{}, "available_at is not an RFC 3339 time with an offset"
		}
		available, precision = t, PrecisionTime
	}
	if available.Before(published) {
		return Record{}, "available_at is before published_at"
	}
	basis := Reconstructed
	if row["held_at"] != "" {
		held, err := time.Parse(time.RFC3339, row["held_at"])
		if err != nil {
			return Record{}, "held_at is not an RFC 3339 time with an offset"
		}
		if held.Before(published) {
			return Record{}, "held_at is before published_at"
		}
		if held.Sub(published) <= analystHeldWithin {
			basis = Known
		}
	}

	period := row["period"]
	labels := map[string]string{"firm": firm}
	var facts []Fact
	parts := []string{symbol, firm, string(typ), published.Format(time.DateOnly), period}
	switch typ {
	case AnalystUpgrade, AnalystDowngrade:
		if row["new_rating"] == "" {
			return Record{}, "a rating revision without new_rating"
		}
		labels["new_rating"] = row["new_rating"]
		if row["previous_rating"] != "" {
			labels["previous_rating"] = row["previous_rating"]
		}
	default:
		currency := strings.ToUpper(row["currency"])
		if len(currency) != 3 || strings.Trim(currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			return Record{}, "currency is not a three-letter code"
		}
		newName, prevName, unit := "new_target", "previous_target", currency+"/share"
		if typ == EstimateRaised || typ == EstimateCut {
			newName, prevName = "new_estimate", "previous_estimate"
			metric := strings.ToLower(row["metric"])
			switch metric {
			case "eps":
			case "revenue":
				unit = currency
			default:
				return Record{}, "an estimate revision whose metric is not eps or revenue"
			}
			if period == "" {
				return Record{}, "an estimate revision without period"
			}
			parts = append(parts, metric)
		}
		for _, name := range [2]string{newName, prevName} {
			v, stated, ok := analystFigure(row, name)
			switch {
			case !ok:
				return Record{}, "a figure is not a number"
			case !stated && name == newName:
				return Record{}, "a target or estimate revision without the new figure"
			case !stated:
				continue
			case (typ == TargetRaised || typ == TargetCut) && v <= 0:
				return Record{}, "a price target that is not above zero"
			}
			facts = append(facts, Fact{Name: name, Value: v, Unit: unit, Period: period})
		}
	}
	if _, ok := resolveDirection(typ, facts, labels); !ok {
		return Record{}, "kind contradicts the stated ratings or figures"
	}

	key := NewKey(Analyst, parts...)
	rec := Record{
		ID: ID(key), Key: key, Category: Analyst, Type: typ,
		Symbol: symbol, CIK: cik,
		PublishedAt: published, AvailableAt: available, Precision: precision,
		Source: SourceImport, SourceRef: row["source_ref"], Evidence: []string{row["source_ref"]},
		Facts: facts, Labels: labels,
		Basis: basis, Extractor: AnalystExtractor, Version: Version,
	}
	if err := rec.Validate(); err != nil {
		return Record{}, fmt.Sprint(err)
	}
	return rec, ""
}
