package gold

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/eventfact"
)

// Extractor reads the records an event's text supports. eventfact.FromEvent is
// one; a language model behind a vetting step would be another.
type Extractor func(eventfact.EventText) []eventfact.Record

// TypeScore compares the records of one type with the labels.
type TypeScore struct {
	Type string `json:"type"`
	// Expected is how many labelled records have the type, Predicted how many
	// the extractor made, Matched how many items have both.
	Expected  int      `json:"expected"`
	Predicted int      `json:"predicted"`
	Matched   int      `json:"matched"`
	Precision *float64 `json:"precision"` // Matched over Predicted; null when nothing was predicted
	Recall    *float64 `json:"recall"`    // Matched over Expected; null when nothing was expected
}

// FieldScore compares one figure, or one label, of one type with the labels.
// It looks at the records that were matched by type for Both and Agreed, and at
// every record for Expected and Predicted: a figure of a record that was missed
// or invented counts as missed or invented.
type FieldScore struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Label bool   `json:"label,omitempty"` // a label (a word), not a figure
	// Expected is how many labelled records state it, Predicted how many of the
	// extractor's make it, Both how many matched records have it on both sides,
	// Agreed how many of those have the same value (the same unit and a value
	// equal to a millionth, or the same word ignoring case).
	Expected  int      `json:"expected"`
	Predicted int      `json:"predicted"`
	Both      int      `json:"both"`
	Agreed    int      `json:"agreed"`
	Precision *float64 `json:"precision"` // Both over Predicted
	Recall    *float64 `json:"recall"`    // Both over Expected
	Exact     *float64 `json:"exact"`     // Agreed over Both
}

// Report is what the extractor did on the labelled items of a file.
type Report struct {
	Extractor string `json:"extractor"`
	Version   string `json:"version"`
	Role      string `json:"role,omitempty"`
	Items     int    `json:"items"`
	Labelled  int    `json:"labelled"`
	// NoneItems are the labelled items whose text states no economic event;
	// FalseEvents how many of them the extractor made a record for.
	NoneItems   int `json:"none_items"`
	FalseEvents int `json:"false_events"`
	// FalseEventItems names those items. It is empty for the held-out part: the
	// counts are the result, and which items failed is not shown so that the
	// rules cannot be tuned on them.
	FalseEventItems []string     `json:"false_event_items,omitempty"`
	Types           []TypeScore  `json:"types"`
	Fields          []FieldScore `json:"fields"`
}

// ErrHeldOut is the refusal to score the held-out part without being told it is
// the final reading.
var ErrHeldOut = errors.New("gold: this is the held-out test part; score it only as the final reading, once the rules are fixed")

func ratio(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	v := float64(n) / float64(d)
	return &v
}

// reading is one figure or label as one side states it.
type reading struct {
	label bool
	num   float64
	unit  string
	word  string
}

func (r reading) agrees(o reading) bool {
	if r.label {
		return strings.EqualFold(strings.TrimSpace(r.word), strings.TrimSpace(o.word))
	}
	return r.unit == o.unit && math.Abs(r.num-o.num) <= 1e-6*math.Max(1, math.Max(math.Abs(r.num), math.Abs(o.num)))
}

// expectedOf lists by name what a labelled record states.
func expectedOf(r Record) map[string]reading {
	out := map[string]reading{}
	for _, f := range r.Facts {
		if f.Value != nil {
			out[f.Name] = reading{num: *f.Value, unit: f.Unit}
		}
	}
	for name, word := range r.Labels {
		out[name] = reading{label: true, word: word}
	}
	return out
}

// predicted lists by name what an extracted record states. A figure the
// extractor derived by arithmetic from others is not read from the text, so it is
// not counted.
func predicted(r eventfact.Record) map[string]reading {
	out := map[string]reading{}
	for _, f := range r.Facts {
		if !f.Derived {
			if _, dup := out[f.Name]; !dup {
				out[f.Name] = reading{num: f.Value, unit: f.Unit}
			}
		}
	}
	for name, word := range r.Labels {
		out[name] = reading{label: true, word: word}
	}
	return out
}

type fieldKey struct{ typ, name string }

// Score runs the extractor over the item texts of the labelled items in the
// file and compares what it makes with the labels. extractor names the rules
// for the report. Items that are not labelled are left out and counted.
//
// The records of an item are paired by type. A type the labels give and the
// extractor made is matched; a type only the labels give is missed; a type only
// the extractor made, which includes every record it makes for an item labelled
// none, is invented. A second record of a type the extractor makes for one item
// is invented too: the labels give one record per type.
//
// A file that is the held-out part is scored only when final is true, and its
// report does not name the items that failed. A file that does not pass Check
// is not scored.
func Score(f File, extractor string, extract Extractor, final bool) (Report, error) {
	if f.Header.Role == RoleTest && !final {
		return Report{}, ErrHeldOut
	}
	if ps := Check(f); len(ps) > 0 {
		return Report{}, fmt.Errorf("gold: the labels have %d problems (gold check lists them), first: %s", len(ps), ps[0])
	}
	rep := Report{Extractor: extractor, Version: eventfact.Version, Role: f.Header.Role, Items: len(f.Items)}
	types := map[string]*TypeScore{}
	fields := map[fieldKey]*FieldScore{}
	typeOf := func(t string) *TypeScore {
		if types[t] == nil {
			types[t] = &TypeScore{Type: t}
		}
		return types[t]
	}
	fieldOf := func(t, name string, label bool) *FieldScore {
		k := fieldKey{t, name}
		if fields[k] == nil {
			fields[k] = &FieldScore{Type: t, Name: name, Label: label}
		}
		return fields[k]
	}
	for _, it := range f.Items {
		if !it.Labelled() {
			continue
		}
		rep.Labelled++
		made := extract(it.EventText())
		sort.SliceStable(made, func(i, j int) bool { return made[i].Key < made[j].Key })
		none := len(it.Records) == 1 && it.Records[0].Type == None
		if none {
			rep.NoneItems++
			if len(made) > 0 {
				rep.FalseEvents++
				if f.Header.Role != RoleTest {
					rep.FalseEventItems = append(rep.FalseEventItems, it.ID)
				}
			}
		}
		used := make([]bool, len(made))
		for _, want := range it.Records {
			if want.Type == None {
				continue
			}
			ts := typeOf(want.Type)
			ts.Expected++
			exp := expectedOf(want)
			pair := -1
			for i, m := range made {
				if !used[i] && string(m.Type) == want.Type {
					pair = i
					break
				}
			}
			var got map[string]reading
			if pair >= 0 {
				used[pair] = true
				ts.Matched++
				got = predicted(made[pair])
				ts.Predicted++
			}
			for name, e := range exp {
				fs := fieldOf(want.Type, name, e.label)
				fs.Expected++
				if g, ok := got[name]; ok {
					fs.Both++
					if e.agrees(g) {
						fs.Agreed++
					}
				}
			}
			for name, g := range got {
				fs := fieldOf(want.Type, name, g.label)
				fs.Predicted++
			}
		}
		for i, m := range made {
			if used[i] {
				continue
			}
			typeOf(string(m.Type)).Predicted++
			for name, g := range predicted(m) {
				fieldOf(string(m.Type), name, g.label).Predicted++
			}
		}
	}
	for _, ts := range types {
		ts.Precision, ts.Recall = ratio(ts.Matched, ts.Predicted), ratio(ts.Matched, ts.Expected)
		rep.Types = append(rep.Types, *ts)
	}
	sort.Slice(rep.Types, func(i, j int) bool { return rep.Types[i].Type < rep.Types[j].Type })
	for _, fs := range fields {
		fs.Precision, fs.Recall, fs.Exact = ratio(fs.Both, fs.Predicted), ratio(fs.Both, fs.Expected), ratio(fs.Agreed, fs.Both)
		rep.Fields = append(rep.Fields, *fs)
	}
	sort.Slice(rep.Fields, func(i, j int) bool {
		a, b := rep.Fields[i], rep.Fields[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Name < b.Name
	})
	sort.Strings(rep.FalseEventItems)
	if rep.Labelled == 0 {
		return Report{}, errors.New("gold: no item is labelled, so there is nothing to score")
	}
	return rep, nil
}
