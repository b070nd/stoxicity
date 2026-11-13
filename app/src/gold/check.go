package gold

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/eventfact"
)

// Problem is one thing wrong with a file. Item is the item's ID, empty for the
// file as a whole.
type Problem struct {
	Item    string
	Message string
}

// String reads as "ID: message".
func (p Problem) String() string {
	if p.Item == "" {
		return p.Message
	}
	return p.Item + ": " + p.Message
}

// Counts says how many items a file has and how many of them are labelled.
type Counts struct {
	Items, Labelled int
}

// Count counts the items and the labelled ones.
func Count(f File) Counts {
	c := Counts{Items: len(f.Items)}
	for _, it := range f.Items {
		if it.Labelled() {
			c.Labelled++
		}
	}
	return c
}

// Check lists everything wrong with a file, in the order of its items; none
// means the labels can be used. An item that is not labelled yet is not a
// problem. For a labelled item it checks that:
//
//   - the item has an ID, a symbol, a publication time, text, and a kind whose
//     category is the item's category, and the ID is not used by another item;
//   - the labeller is named, and each type is "none" alone or a known type used
//     once;
//   - each figure has a name the type has and that is a figure (not a label), a
//     value, a unit that fits the field, and a span that is verbatim in one
//     passage of the text; no figure is given twice;
//   - each label has a name the type has and that is a label, and a word.
//
// A file made by Split must also have each item in the part the recorded rule
// puts it in.
func Check(f File) []Problem {
	var out []Problem
	add := func(id, format string, args ...any) {
		out = append(out, Problem{Item: id, Message: fmt.Sprintf(format, args...)})
	}
	if f.Header.Format != Version {
		add("", "the format is %q, not %q", f.Header.Format, Version)
	}
	switch f.Header.Role {
	case "":
	case RoleDev, RoleTest:
		if f.Header.Split == nil {
			add("", "the role is %s but the header records no split", f.Header.Role)
		}
	default:
		add("", "the role %q is not %s or %s", f.Header.Role, RoleDev, RoleTest)
	}
	ids := map[string]bool{}
	for _, it := range f.Items {
		if it.ID == "" {
			add("", "an item has no ID")
			continue
		}
		if ids[it.ID] {
			add(it.ID, "the ID is used by more than one item")
		}
		ids[it.ID] = true
		checkItem(it, add)
		if f.Header.Split != nil && (f.Header.Role == RoleDev || f.Header.Role == RoleTest) {
			if want := Side(f.Header.Split.Seed, f.Header.Split.DevFraction, it.ID); want != f.Header.Role {
				add(it.ID, "the recorded split rule puts this item in the %s part, not the %s part", want, f.Header.Role)
			}
		}
	}
	return out
}

func checkItem(it Item, add func(id, format string, args ...any)) {
	cat, ok := CategoryOf(it.Kind)
	switch {
	case it.Symbol == "":
		add(it.ID, "no symbol")
	case it.Published.IsZero():
		add(it.ID, "no publication time")
	case len(it.Text) == 0:
		add(it.ID, "no text")
	case !ok:
		add(it.ID, "kind %q is not one the draw reads", it.Kind)
	case string(cat) != it.Category:
		add(it.ID, "category %q is not %q, the category of kind %s", it.Category, cat, it.Kind)
	}
	if !it.Labelled() {
		return
	}
	if strings.TrimSpace(it.Labeller) == "" {
		add(it.ID, "labelled, but the labeller is not named")
	}
	types := map[string]bool{}
	for _, r := range it.Records {
		switch {
		case r.Type == None:
			if len(it.Records) > 1 {
				add(it.ID, "type none is given beside another record; none stands alone")
			}
			if len(r.Facts) > 0 || len(r.Labels) > 0 {
				add(it.ID, "type none has figures or labels")
			}
			continue
		case !eventfact.Type(r.Type).Valid():
			add(it.ID, "%q is not an event type (or none)", r.Type)
			continue
		case types[r.Type]:
			add(it.ID, "type %s is given twice; give one record per type", r.Type)
			continue
		}
		types[r.Type] = true
		checkRecord(it, r, add)
	}
}

func checkRecord(it Item, r Record, add func(id, format string, args ...any)) {
	fields := map[string]string{}
	for _, f := range eventfact.Type(r.Type).Fields() {
		fields[f.Name] = f.Unit
	}
	seen := map[string]bool{}
	for _, f := range r.Facts {
		unit, known := fields[f.Name]
		switch {
		case !known:
			add(it.ID, "%s: type %s has no figure %q", r.Type, r.Type, f.Name)
			continue
		case unit == "label":
			add(it.ID, "%s: %q is a label of the type, not a figure; give it under labels", r.Type, f.Name)
			continue
		case seen[f.Name]:
			add(it.ID, "%s: figure %q is given twice", r.Type, f.Name)
			continue
		}
		seen[f.Name] = true
		switch {
		case f.Value == nil:
			add(it.ID, "%s: figure %q has no value", r.Type, f.Name)
		case math.IsNaN(*f.Value) || math.IsInf(*f.Value, 0):
			add(it.ID, "%s: figure %q is not a number", r.Type, f.Name)
		}
		if !UnitFits(unit, f.Unit) {
			add(it.ID, "%s: unit %q does not fit figure %q (%s)", r.Type, f.Unit, f.Name, unit)
		}
		switch {
		case f.Span == "":
			add(it.ID, "%s: figure %q has no span", r.Type, f.Name)
		case !slices.ContainsFunc(it.Text, func(p Passage) bool { return strings.Contains(p.Text, f.Span) }):
			add(it.ID, "%s: the span of %q is not verbatim in the text: %q", r.Type, f.Name, f.Span)
		}
	}
	names := make([]string, 0, len(r.Labels))
	for name := range r.Labels {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		word := r.Labels[name]
		switch unit, known := fields[name]; {
		case !known:
			add(it.ID, "%s: type %s has no label %q", r.Type, r.Type, name)
		case unit != "label":
			add(it.ID, "%s: %q is a figure of the type, not a label; give it under facts", r.Type, name)
		case strings.TrimSpace(word) == "":
			add(it.ID, "%s: label %q is empty", r.Type, name)
		}
	}
}

// UnitFits says whether unit is a unit that a field measured in field can have:
// a three-letter upper-case currency code for "currency", that code and
// "/share" for "currency_per_share", and the field's own unit otherwise.
func UnitFits(field, unit string) bool {
	code := func(u string) bool {
		return len(u) == 3 && u == strings.ToUpper(u) && u[0] >= 'A' && u[0] <= 'Z' && u[1] >= 'A' && u[1] <= 'Z' && u[2] >= 'A' && u[2] <= 'Z'
	}
	switch field {
	case "currency":
		return code(unit)
	case "currency_per_share":
		return strings.HasSuffix(unit, "/share") && code(strings.TrimSuffix(unit, "/share"))
	}
	return unit == field
}
