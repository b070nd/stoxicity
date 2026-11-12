package eventfact

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Vet checks a record proposed by an extractor that is not these rules (for
// example a language model) against the passages it claims to rest on. It
// keeps a figure only when its Span is verbatim in a passage and Amounts reads
// the same value and unit from that span; it drops labels and figures the type
// does not have. ok is false when the type is invalid, the key or times are
// unusable, or no stated figure survives. dropped says what was removed and
// why.
func Vet(r Record, passages []Passage) (vetted Record, dropped []string, ok bool) {
	if !r.Type.Valid() {
		return Record{}, []string{fmt.Sprintf("type %q is not an event type", r.Type)}, false
	}
	switch {
	case r.Key == "" || !strings.HasPrefix(r.Key, string(r.Type.Category())+"|"):
		return Record{}, []string{"the key is empty or does not start with the category of the type"}, false
	case r.Symbol == "" && r.SecurityID == "":
		return Record{}, []string{"the record names no security"}, false
	case r.PublishedAt.IsZero() || r.AvailableAt.IsZero() || r.AvailableAt.Before(r.PublishedAt):
		return Record{}, []string{"the times are missing or the record is available before it was published"}, false
	case r.Precision != PrecisionTime && r.Precision != PrecisionDay:
		return Record{}, []string{fmt.Sprintf("precision %q is not time or day", r.Precision)}, false
	}
	units := map[string]string{}
	for _, f := range r.Type.Fields() {
		units[f.Name] = f.Unit
	}
	v := r
	v.Category = r.Type.Category()
	v.ID = ID(r.Key)
	v.Facts, v.Labels = nil, nil

	var names []string
	for name := range r.Labels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if units[name] != "label" {
			dropped = append(dropped, fmt.Sprintf("label %s: type %s has no such label", name, r.Type))
			continue
		}
		if v.Labels == nil {
			v.Labels = map[string]string{}
		}
		v.Labels[name] = r.Labels[name]
	}

	refs := map[string]bool{}
	seen := map[string]bool{}
	var derived []Fact
	for _, f := range r.Facts {
		unit, known := units[f.Name]
		switch {
		case !known || unit == "label":
			dropped = append(dropped, fmt.Sprintf("figure %s: type %s has no such figure", f.Name, r.Type))
			continue
		case seen[f.Name]:
			dropped = append(dropped, fmt.Sprintf("figure %s: given twice", f.Name))
			continue
		case math.IsNaN(f.Value) || math.IsInf(f.Value, 0):
			dropped = append(dropped, fmt.Sprintf("figure %s: not a number", f.Name))
			continue
		case !unitFits(unit, f.Unit):
			dropped = append(dropped, fmt.Sprintf("figure %s: unit %q does not fit the field", f.Name, f.Unit))
			continue
		}
		if f.Derived {
			derived = append(derived, f)
			continue
		}
		if f.Span == nil {
			dropped = append(dropped, fmt.Sprintf("figure %s: no span", f.Name))
			continue
		}
		ref, reason := checkSpan(f, passages)
		if reason != "" {
			dropped = append(dropped, fmt.Sprintf("figure %s: %s", f.Name, reason))
			continue
		}
		seen[f.Name] = true
		refs[ref] = true
		f.Text = f.Span.Text
		v.Facts = append(v.Facts, f)
	}
	if len(v.Facts) == 0 {
		return Record{}, append(dropped, "no stated figure survives"), false
	}
	for _, f := range derived {
		if seen[f.Name] {
			dropped = append(dropped, fmt.Sprintf("figure %s: given twice", f.Name))
			continue
		}
		if !derivedHolds(f, v.Facts) {
			dropped = append(dropped, fmt.Sprintf("figure %s: derived, but not reproducible from the figures that survive", f.Name))
			continue
		}
		seen[f.Name] = true
		f.Span = nil
		v.Facts = append(v.Facts, f)
	}
	v.Evidence = uniqueSorted(r.Evidence, refs)
	return v, dropped, true
}

func unitFits(field, unit string) bool {
	code := func(u string) bool { return len(u) == 3 && u == strings.ToUpper(u) && isLetter(u[0]) }
	switch field {
	case "currency":
		return code(unit)
	case "currency_per_share":
		return strings.HasSuffix(unit, "/share") && code(strings.TrimSuffix(unit, "/share"))
	}
	return unit == field
}

func near(a, b float64) bool {
	return math.Abs(a-b) <= 1e-6*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// checkSpan finds a passage in which the span is verbatim and Amounts reads
// the fact's value and unit from it. It returns the passage's Ref.
func checkSpan(f Fact, passages []Passage) (string, string) {
	sp := *f.Span
	reason := "span is not verbatim in any passage"
	base := strings.TrimSuffix(f.Unit, "/share")
	for _, p := range passages {
		if sp.Start < 0 || sp.End > len(p.Text) || sp.Start >= sp.End || p.Text[sp.Start:sp.End] != sp.Text {
			continue
		}
		var in []Amount
		for _, a := range Amounts(p.Text) {
			if within(a, sp.Start, sp.End) {
				in = append(in, a)
			}
		}
		reason = "Amounts reads no figure from the span"
		switch len(in) {
		case 1:
			a := in[0]
			if a.Unit == base && near(a.Value, f.Value) && (!a.PerShare || strings.HasSuffix(f.Unit, "/share")) {
				return p.Ref, ""
			}
			reason = fmt.Sprintf("Amounts reads %g %q from the span, not %g %q", a.Value, a.Unit, f.Value, f.Unit)
		case 2:
			if in[0].Unit == base && in[1].Unit == base && (near(f.Value, in[0].Value) || near(f.Value, in[1].Value) || near(f.Value, (in[0].Value+in[1].Value)/2)) {
				return p.Ref, ""
			}
			reason = "the span holds two figures and the value is neither of them nor their midpoint"
		default:
			if len(in) > 2 {
				reason = "the span holds more than two figures"
			}
		}
	}
	return "", reason
}

func derivedHolds(f Fact, stated []Fact) bool {
	get := func(name string) (float64, bool) {
		for _, s := range stated {
			if s.Name == name {
				return s.Value, true
			}
		}
		return 0, false
	}
	switch f.Name {
	case "annual_value":
		val, ok1 := get("value")
		dur, ok2 := get("duration_years")
		return ok1 && ok2 && dur > 0 && near(f.Value, val/dur)
	case "change":
		for _, p := range [][2]string{{"new_guidance", "previous_guidance"}, {"new_target", "previous_target"}, {"new_estimate", "previous_estimate"}} {
			n, ok1 := get(p[0])
			o, ok2 := get(p[1])
			if ok1 && ok2 && o != 0 && near(f.Value, n/o-1) {
				return true
			}
		}
	}
	return false
}
