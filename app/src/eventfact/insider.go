package eventfact

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/model"
)

// InsiderExtractor names these rules.
const InsiderExtractor = "form4-1"

// SecurityRef names the security of an issuer.
type SecurityRef struct{ SecurityID, Symbol string }

// FilingTime is when a filing became public.
type FilingTime struct {
	Accepted  time.Time // zero when only the day is known
	Precision string    // PrecisionTime or PrecisionDay
}

type insiderTrade struct {
	model.InsiderTrade
	at        time.Time
	precision string
}

// FromInsider turns Form 4 transactions into one record per issuer, owner, reported period and direction.
func FromInsider(trades []model.InsiderTrade, securities map[string]SecurityRef, times map[string]FilingTime, basis func(accession string) Basis) []Record {
	groups := map[string][]insiderTrade{}
	for _, t := range trades {
		kind := t.Kind()
		if (kind != model.TradeBuy && kind != model.TradeSell) || !t.IsCommon() || t.Shares <= 0 {
			continue
		}
		if _, ok := securities[t.CIK]; !ok {
			continue
		}
		at, precision, ok := filingMoment(t, times)
		if !ok {
			continue
		}
		owner := t.OwnerCIK
		if owner == "" {
			owner = strings.ToLower(t.Owner)
		}
		period := t.Period
		if period == "" {
			period = t.Date
		}
		key := NewKey(Insider, t.CIK, owner, period, kind)
		groups[key] = append(groups[key], insiderTrade{t, at, precision})
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []Record
	for _, key := range keys {
		r, ok := insiderRecord(key, groups[key], securities, basis)
		if ok && r.Validate() == nil {
			out = append(out, r)
		}
	}
	sortRecords(out)
	return out
}

// filingMoment is when the filing of the trade became public, and how exactly that is known.
func filingMoment(t model.InsiderTrade, times map[string]FilingTime) (time.Time, string, bool) {
	if ft, ok := times[t.Accession]; ok && ft.Precision == PrecisionTime && !ft.Accepted.IsZero() {
		return ft.Accepted.UTC(), PrecisionTime, true
	}
	day, err := time.Parse("2006-01-02", t.Filed)
	if err != nil {
		return time.Time{}, "", false
	}
	return day.UTC(), PrecisionDay, true
}

func insiderRecord(key string, group []insiderTrade, securities map[string]SecurityRef, basis func(string) Basis) (Record, bool) {
	sort.SliceStable(group, func(i, j int) bool {
		a, b := group[i], group[j]
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		if a.Accession != b.Accession {
			return a.Accession < b.Accession
		}
		return a.Seq < b.Seq
	})

	earliest := group[0]
	accessions := map[string]bool{}
	var hasOriginal, hasAmendment bool
	for _, t := range group {
		accessions[t.Accession] = true
		if t.at.Before(earliest.at) || (t.at.Equal(earliest.at) && t.Accession < earliest.Accession) {
			earliest = t
		}
		if t.Form == "4/A" {
			hasAmendment = true
		} else if t.Form == "4" {
			hasOriginal = true
		}
	}

	figures := group
	sourceRef := earliest.Accession
	if hasOriginal && hasAmendment {
		var newest insiderTrade
		found := false
		for _, t := range group {
			if t.Form != "4/A" {
				continue
			}
			if !found || t.Filed > newest.Filed || (t.Filed == newest.Filed && t.Accession > newest.Accession) {
				newest, found = t, true
			}
		}
		figures = nil
		for _, t := range group {
			if t.Accession == newest.Accession {
				figures = append(figures, t)
			}
		}
		sourceRef = newest.Accession
	}

	evidence := make([]string, 0, len(accessions))
	for a := range accessions {
		evidence = append(evidence, a)
	}
	sort.Strings(evidence)

	var shares, value, pricedShares float64
	var plans, direct int
	role := "other"
	for _, t := range figures {
		shares += t.Shares
		if t.Price > 0 {
			value += t.Value()
			pricedShares += t.Shares
		}
		if t.Plan {
			plans++
		}
		if t.Direct {
			direct++
		}
		if insiderRank(insiderRole(t.Role)) > insiderRank(role) {
			role = insiderRole(t.Role)
		}
	}
	kind := figures[0].Kind()
	typ := InsiderBuy
	if kind == model.TradeSell {
		typ = InsiderSell
	}

	facts := []Fact{{Name: "shares", Value: shares, Unit: "shares"}}
	if pricedShares > 0 {
		facts = append(facts,
			Fact{Name: "value", Value: value, Unit: "USD"},
			Fact{Name: "price", Value: value / pricedShares, Unit: "USD/share"})
	}
	after := figures[len(figures)-1].After
	if after != 0 {
		held := after - shares
		if kind == model.TradeSell {
			held = after + shares
		}
		if held > 0 {
			facts = append(facts, Fact{Name: "pct_of_holding", Value: shares / held, Unit: "ratio", Derived: true})
		}
	}
	facts = append(facts, Fact{Name: "transactions", Value: float64(len(figures)), Unit: "count"})

	n := len(figures)
	b := Known
	if basis != nil {
		b = basis(earliest.Accession)
	}
	ref := securities[earliest.CIK]
	return Record{
		ID:          ID(key),
		Key:         key,
		Category:    Insider,
		Type:        typ,
		SecurityID:  ref.SecurityID,
		Symbol:      ref.Symbol,
		CIK:         earliest.CIK,
		PublishedAt: earliest.at,
		AvailableAt: earliest.at,
		Precision:   earliest.precision,
		Source:      SourceFiling,
		SourceRef:   sourceRef,
		Evidence:    evidence,
		Facts:       facts,
		Labels: map[string]string{
			"role":    role,
			"plan":    threeWay(plans, n, "yes", "no"),
			"holding": threeWay(direct, n, "direct", "indirect"),
		},
		Basis:     b,
		Extractor: InsiderExtractor,
		Version:   Version,
	}, true
}

// threeWay is all when every one of n counts, none when no one does, and mixed otherwise.
func threeWay(count, n int, all, none string) string {
	switch count {
	case 0:
		return none
	case n:
		return all
	}
	return "mixed"
}

func insiderRank(role string) int {
	switch role {
	case "officer":
		return 3
	case "director":
		return 2
	case "owner":
		return 1
	}
	return 0
}

// insiderRole reads the strongest of officer, director, owner from the role text of a form.
func insiderRole(text string) string {
	lower := strings.ToLower(text)
	words := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) })
	set := map[string]bool{}
	for _, w := range words {
		set[w] = true
	}
	switch {
	case strings.Contains(lower, "officer") || strings.Contains(lower, "chief") || strings.Contains(lower, "president") ||
		set["ceo"] || set["cfo"] || set["coo"] || set["vp"] || set["svp"] || set["evp"]:
		return "officer"
	case strings.Contains(lower, "director"):
		return "director"
	case strings.Contains(lower, "10%") || strings.Contains(lower, "owner"):
		return "owner"
	}
	return "other"
}

func sortRecords(rs []Record) {
	sort.SliceStable(rs, func(i, j int) bool {
		if !rs[i].AvailableAt.Equal(rs[j].AvailableAt) {
			return rs[i].AvailableAt.Before(rs[j].AvailableAt)
		}
		return rs[i].ID < rs[j].ID
	})
}
