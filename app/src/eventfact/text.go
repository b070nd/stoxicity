package eventfact

import (
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

// TextExtractor names these rules. A change of what the rules read, such as a
// new label, takes a new number, so that the records made by the earlier rules
// stay readable as earlier revisions.
const TextExtractor = "text-rules-2"

// reading is one passage with what the rules need from it.
type reading struct {
	ref   string
	text  string
	low   string
	amts  []Amount
	sents [][2]int
}

// FromEvent returns the records the event's text supports. It returns none
// when the text states nothing the types need; it never guesses a figure.
func FromEvent(e EventText) []Record {
	rs := readPassages(e.Passages)
	var out []Record
	switch e.Kind {
	case model.EventGuidance:
		out = guidanceRecords(e, rs, true)
	case model.EventResults:
		out = guidanceRecords(e, rs, false)
	case model.EventBuyback:
		out = buybackRecords(e, rs)
	case model.EventAgreement, model.EventProduct:
		out = contractRecords(e, rs)
	case model.EventAnalystTgt:
		out = analystRecords(e, rs)
	}
	return out
}

func readPassages(ps []Passage) []reading {
	var out []reading
	for _, p := range ps {
		low := fold(p.Text)
		if addressedToReader(low) {
			continue
		}
		out = append(out, reading{ref: p.Ref, text: p.Text, low: low, amts: Amounts(p.Text), sents: sentences(p.Text)})
	}
	return out
}

// addressedToReader spots text that talks to whoever reads it instead of
// reporting an event.
func addressedToReader(low string) bool {
	for _, p := range []string{"ignore previous", "ignore all previous", "ignore the previous", "ignore the above", "ignore prior",
		"ignore your", "disregard", "classify this", "classify the", "system prompt", "you are an", "you are a ", "you must",
		"as an ai", "new instructions", "instructions above", "respond with", "output json", "override"} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func sentences(text string) [][2]int {
	var out [][2]int
	start := 0
	add := func(end int) {
		s := start
		for s < end && (text[s] == ' ' || text[s] == '\t' || text[s] == '\n' || text[s] == '\r') {
			s++
		}
		if s < end {
			out = append(out, [2]int{s, end})
		}
	}
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '\n':
			add(i)
			start = i + 1
		case '.', '!', '?':
			if i+1 < len(text) && text[i+1] != ' ' && text[i+1] != '\n' {
				continue
			}
			if c == '.' {
				w := fold(letterRun(text, wordStart(text, i)))
				switch w {
				case "inc", "corp", "co", "ltd", "plc", "vs", "mr", "mrs", "ms", "dr", "st", "no", "bros", "u", "s", "e", "g", "i":
					continue
				}
				if len(w) == 1 {
					continue
				}
				j := i + 1
				for j < len(text) && text[j] == ' ' {
					j++
				}
				if j < len(text) && text[j] >= 'a' && text[j] <= 'z' {
					continue
				}
			}
			add(i + 1)
			start = i + 1
		}
	}
	add(len(text))
	return out
}

func wordStart(text string, i int) int {
	for i > 0 && isLetter(text[i-1]) {
		i--
	}
	return i
}

// find returns the index of the first occurrence of p in low at a word start
// (and, unless stem is true, at a word end); -1 when there is none.
func find(low, p string, stem bool) int {
	for from := 0; from < len(low); {
		k := strings.Index(low[from:], p)
		if k < 0 {
			return -1
		}
		k += from
		from = k + 1
		if k > 0 && isAlnum(low[k-1]) {
			continue
		}
		if !stem && k+len(p) < len(low) && isAlnum(low[k+len(p)]) {
			continue
		}
		return k
	}
	return -1
}

func hasAny(low string, stem bool, ps ...string) bool {
	for _, p := range ps {
		if find(low, p, stem) >= 0 {
			return true
		}
	}
	return false
}

// speculative reports a sentence that denies, doubts or imagines what it
// says, so that no direction is read from it.
func speculative(low string) bool {
	low = strings.ReplaceAll(low, "no longer", "xx xxxxxx")
	if strings.Contains(low, "n't") || strings.Contains(low, "n’t") {
		return true
	}
	return hasAny(low, false, "not", "never", "no", "may", "might", "could", "would", "should", "if", "whether", "unless",
		"unlikely", "likely to", "expected to", "expected that", "consider", "considering", "potential", "potentially", "possibly", "without")
}

func within(a Amount, s, e int) bool { return a.Span.Start >= s && a.Span.End <= e }

func (r reading) amountsIn(s, e int) []Amount {
	var out []Amount
	for _, a := range r.amts {
		if within(a, s, e) {
			out = append(out, a)
		}
	}
	return out
}

func prevWords(low string, pos, lo, n int) []string {
	var out []string
	i := pos
	for len(out) < n {
		for i > lo && !isAlnum(low[i-1]) {
			i--
		}
		j := i
		for i > lo && isAlnum(low[i-1]) {
			i--
		}
		if i == j {
			break
		}
		out = append(out, low[i:j])
	}
	return out
}

func hasPrev(low string, pos, lo int, words ...string) bool {
	for _, w := range prevWords(low, pos, lo, 4) {
		for _, x := range words {
			if w == x {
				return true
			}
		}
	}
	return false
}

func isMoney(a Amount) bool { return isMoneyUnit(a.Unit) }

func unitOf(a Amount, perShare bool) string {
	if perShare {
		return a.Unit + "/share"
	}
	return a.Unit
}

func factOf(name string, v float64, unit, period string, a Amount) Fact {
	s := a.Span
	return Fact{Name: name, Value: v, Unit: unit, Period: period, Text: s.Text, Span: &s}
}

// mark is a figure, or the two ends of a range.
type mark struct {
	a, b  Amount
	rng   bool
	viaTo bool
}

func (m mark) mid() float64 { return (m.a.Value + m.b.Value) / 2 }

func (m mark) fact(name string, v float64, unit, period, text string) Fact {
	s := Span{Start: m.a.Span.Start, End: m.b.Span.End}
	s.Text = text[s.Start:s.End]
	return Fact{Name: name, Value: v, Unit: unit, Period: period, Text: s.Text, Span: &s}
}

func (m mark) perShare() bool { return m.a.PerShare || m.b.PerShare }

// marks groups neighbouring figures of one unit joined by "-", "and",
// "through" or "to" into ranges. lo is where the clause starts.
func marks(low string, am []Amount, lo int, directional bool) []mark {
	var out []mark
	for k := 0; k < len(am); {
		m := mark{a: am[k], b: am[k]}
		j := k
		for j+1 < len(am) && !m.rng && am[j].Unit == am[j+1].Unit && am[j].Span.End <= am[j+1].Span.Start {
			between := strings.TrimSpace(low[am[j].Span.End:am[j+1].Span.Start])
			join, viaTo := false, false
			switch between {
			case "-", "–", "—", "and", "through":
				join = true
			case "to":
				join, viaTo = true, true
				first := j == 0 && k == 0
				if first && directional && hasPrev(low, am[j].Span.Start, lo, "from") && !hasAny(low[lo:am[j].Span.Start], false, "range", "between") {
					join = false
				}
			}
			if !join {
				break
			}
			m.b, m.rng, m.viaTo = am[j+1], true, viaTo
			j++
		}
		out = append(out, m)
		k = j + 1
	}
	return out
}

func (m mark) previous(low string, lo int) bool {
	if hasPrev(low, m.a.Span.Start, lo, "previously", "prior", "previous", "versus", "vs", "formerly", "earlier", "compared") {
		return true
	}
	return !(m.rng && m.viaTo) && hasPrev(low, m.a.Span.Start, lo, "from")
}

func lastIndexOf(low string, lo, hi int, words ...string) int {
	best := -1
	seg := low[lo:hi]
	for _, w := range words {
		for from := 0; from < len(seg); {
			k := find(seg[from:], w, true)
			if k < 0 {
				break
			}
			if lo+from+k > best {
				best = lo + from + k
			}
			from += k + 1
		}
	}
	return best
}

func uniqueSorted(in []string, extra map[string]bool) []string {
	set := map[string]bool{}
	for _, s := range in {
		if s != "" {
			set[s] = true
		}
	}
	for s := range extra {
		if s != "" {
			set[s] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func security(e EventText) string {
	if e.SecurityID != "" {
		return e.SecurityID
	}
	return e.Symbol
}

// build assembles a record and returns it only when it passes Validate.
func build(e EventText, typ Type, key string, facts []Fact, labels map[string]string, refs map[string]bool) (Record, bool) {
	r := Record{
		ID: ID(key), Key: key, Category: typ.Category(), Type: typ,
		SecurityID: e.SecurityID, Symbol: e.Symbol, CIK: e.CIK, EventID: e.EventID,
		PublishedAt: e.PublishedAt, AvailableAt: e.AvailableAt, Precision: e.Precision,
		Source: e.Source, SourceRef: e.SourceRef, Evidence: uniqueSorted(e.Evidence, refs),
		Facts: facts, Basis: e.Basis, Extractor: TextExtractor, Version: Version,
	}
	if len(labels) > 0 {
		r.Labels = labels
	}
	if r.Validate() != nil {
		return Record{}, false
	}
	return r, true
}

// clauses splits a sentence where a new statement begins.
func clauses(low string, s, e int) [][2]int {
	var cuts []int
	seg := low[s:e]
	for i := 0; i < len(seg); i++ {
		switch {
		case seg[i] == ';':
			cuts = append(cuts, s+i)
		case strings.HasPrefix(seg[i:], " but "), strings.HasPrefix(seg[i:], " while "), strings.HasPrefix(seg[i:], " whereas "), strings.HasPrefix(seg[i:], " although "):
			cuts = append(cuts, s+i)
		case seg[i] == ',':
			rest := strings.TrimPrefix(strings.TrimLeft(seg[i+1:], " "), "and ")
			w := letterRun(rest, 0)
			if direction(w) != "" {
				cuts = append(cuts, s+i)
			}
		}
	}
	var out [][2]int
	prev := s
	for _, c := range cuts {
		out = append(out, [2]int{prev, c})
		prev = c + 1
	}
	return append(out, [2]int{prev, e})
}

// direction classifies a word as a guidance action.
func direction(w string) string {
	for _, p := range []string{"withdr", "suspend"} {
		if strings.HasPrefix(w, p) {
			return "withdraw"
		}
	}
	for _, p := range []string{"reaffirm", "reiterat", "maintain", "affirm", "confirm", "reconfirm", "unchanged"} {
		if strings.HasPrefix(w, p) {
			return "reaffirm"
		}
	}
	for _, p := range []string{"raise", "lift", "increas", "boost", "hike", "upgrad"} {
		if strings.HasPrefix(w, p) {
			return "raise"
		}
	}
	for _, p := range []string{"cut", "lower", "reduc", "slash", "trim", "downgrad", "decreas", "pare"} {
		if strings.HasPrefix(w, p) {
			return "cut"
		}
	}
	return ""
}

// action finds the single guidance action a clause states; "" when it states
// none or states two that disagree.
func action(low string, s, e int) string {
	seg := low[s:e]
	for _, p := range []string{"lower end", "low end", "higher end", "high end", "upper end", "lower half", "upper half", "high-end", "low-end"} {
		seg = strings.ReplaceAll(seg, p, strings.Repeat("x", len(p)))
	}
	seg = strings.ReplaceAll(seg, "no longer", "xx xxxxxx")
	if i := strings.Index(seg, "no longer"); i >= 0 {
		return "withdraw"
	}
	found := ""
	for i := 0; i < len(seg); {
		if !isLetter(seg[i]) {
			i++
			continue
		}
		w := letterRun(seg, i)
		if d := direction(w); d != "" {
			if found != "" && found != d {
				return ""
			}
			found = d
		}
		i += len(w)
	}
	if strings.Contains(low[s:e], "no longer") && hasAny(low[s:e], false, "provide", "give", "issue", "guide") {
		return "withdraw"
	}
	return found
}

func metricOf(low string, lo, hi int) string {
	best, name := -1, ""
	for _, m := range []struct {
		name  string
		words []string
	}{
		{"eps", []string{"eps", "earnings per share"}},
		{"cash_flow", []string{"cash flow", "fcf"}},
		{"margin", []string{"margin"}},
		{"revenue", []string{"revenue", "revenues", "sales"}},
	} {
		if k := lastIndexOf(low, lo, hi, m.words...); k > best {
			best, name = k, m.name
		}
	}
	return name
}

func guidanceRecords(e EventText, rs []reading, kindGuidance bool) []Record {
	type found struct {
		typ     Type
		facts   []Fact
		metric  string
		period  string
		ref     string
		changed bool
	}
	var all []found
	for _, r := range rs {
		for _, s := range r.sents {
			if speculative(r.low[s[0]:s[1]]) {
				continue
			}
			for _, c := range clauses(r.low, s[0], s[1]) {
				cl := r.low[c[0]:c[1]]
				strict := hasAny(cl, true, "guidance", "outlook", "forecast", "guide")
				loose := kindGuidance && hasAny(cl, true, "expect", "anticipat", "project", "sees", "targets")
				if !strict && !loose {
					continue
				}
				if !hasAny(cl, true, "guidance", "outlook") && hasAny(cl, true, "consensus", "analyst", "street", "estimate") {
					continue
				}
				act := action(r.low, c[0], c[1])
				period := Period(r.text[c[0]:c[1]])
				if period == "" {
					period = Period(r.text[s[0]:s[1]])
				}
				if period == "" {
					period = Period(r.text)
				}
				ms := marks(r.low, r.amountsIn(c[0], c[1]), c[0], act == "raise" || act == "cut")
				type bucket struct{ news, prevs []mark }
				buckets := map[string]*bucket{}
				var order []string
				cur := ""
				lastEnd := c[0]
				for _, m := range ms {
					metric := metricOf(r.low, lastEnd, m.a.Span.Start)
					if metric == "" {
						metric = cur
					}
					if metric == "" {
						metric = metricOf(r.low, c[0], c[1])
					}
					if m.perShare() {
						metric = "eps"
					}
					if metric == "" {
						metric = "other"
					}
					lastEnd, cur = m.b.Span.End, metric
					if (metric == "margin") != (m.a.Unit == "ratio") || (m.a.Unit != "ratio" && !isMoney(m.a)) {
						continue
					}
					b := buckets[metric]
					if b == nil {
						b = &bucket{}
						buckets[metric] = b
						order = append(order, metric)
					}
					if m.previous(r.low, c[0]) {
						b.prevs = append(b.prevs, m)
					} else {
						b.news = append(b.news, m)
					}
				}
				if len(order) == 0 {
					m := metricOf(r.low, c[0], c[1])
					if m == "" {
						m = "other"
					}
					order = append(order, m)
					buckets[m] = &bucket{}
				}
				for _, metric := range order {
					b := buckets[metric]
					if len(b.news) > 1 || len(b.prevs) > 1 || (len(b.news) == 0 && len(b.prevs) > 0) {
						continue
					}
					var nm, pm *mark
					if len(b.news) == 1 {
						nm = &b.news[0]
					}
					if len(b.prevs) == 1 {
						pm = &b.prevs[0]
					}
					typ := Type("")
					switch act {
					case "withdraw":
						if nm == nil && pm == nil {
							typ = GuidanceWithdrawn
						}
					case "reaffirm":
						typ = GuidanceReaffirmed
					case "raise":
						typ = GuidanceUpgrade
					case "cut":
						typ = GuidanceDowngrade
					}
					var facts []Fact
					if nm != nil {
						unit := unitOf(nm.a, nm.perShare() || metric == "eps")
						if pm != nil && unitOf(pm.a, pm.perShare() || metric == "eps") != unit {
							continue
						}
						if metric == "margin" {
							unit = "ratio"
						}
						facts = append(facts, nm.fact("new_guidance", nm.mid(), unit, period, r.text))
						if nm.rng {
							facts = append(facts,
								factOf("new_guidance_low", nm.a.Value, unit, period, nm.a),
								factOf("new_guidance_high", nm.b.Value, unit, period, nm.b))
						}
						if pm != nil {
							facts = append(facts, pm.fact("previous_guidance", pm.mid(), unit, period, r.text))
							if pm.mid() != 0 {
								facts = append(facts, Fact{Name: "change", Value: nm.mid()/pm.mid() - 1, Unit: "ratio", Period: period, Derived: true})
							}
							cmp := nm.mid() - pm.mid()
							switch {
							case cmp > 0 && (typ == "" || typ == GuidanceUpgrade):
								typ = GuidanceUpgrade
							case cmp < 0 && (typ == "" || typ == GuidanceDowngrade):
								typ = GuidanceDowngrade
							case cmp == 0 && (typ == "" || typ == GuidanceReaffirmed):
								typ = GuidanceReaffirmed
							default:
								typ = ""
							}
						} else if typ == "" && act == "" {
							typ = GuidanceIssued
						}
					}
					if typ == "" {
						continue
					}
					all = append(all, found{typ: typ, facts: facts, metric: metric, period: period, ref: r.ref})
				}
			}
		}
	}
	seen := map[string]int{}
	var recs []Record
	var keys []string
	sec := security(e)
	for _, f := range all {
		key := NewKey(Guidance, sec, e.EventID, f.metric, f.period)
		if i, ok := seen[key]; ok {
			if i >= 0 && recs[i].Type != f.typ {
				seen[key] = -1
			}
			continue
		}
		r, ok := build(e, f.typ, key, f.facts, map[string]string{"metric": f.metric}, map[string]bool{f.ref: true})
		if !ok {
			continue
		}
		seen[key] = len(recs)
		recs = append(recs, r)
		keys = append(keys, key)
	}
	var out []Record
	for i, r := range recs {
		if seen[keys[i]] >= 0 {
			out = append(out, r)
		}
	}
	return out
}

func buybackRecords(e EventText, rs []reading) []Record {
	var facts []Fact
	var labels map[string]string
	refs := map[string]bool{}
	typ := Type("")
	haveFacts := false
	kind, mixed := "", false
	for _, r := range rs {
		for _, s := range r.sents {
			low := r.low[s[0]:s[1]]
			if speculative(low) || !hasAny(low, true, "repurchase", "buyback", "buy back", "buy-back") || !buybackCue(r, s, low) {
				continue
			}
			if typ == "" {
				typ = BuybackAnnounced
			}
			if hasAny(low, true, "increas", "expand", "raise", "upsiz", "additional", "boost") {
				typ = BuybackExpanded
			}
			refs[r.ref] = true
			if k := buybackKind(low); k != "" {
				mixed = mixed || (kind != "" && kind != k)
				kind = k
			}
			if haveFacts {
				continue
			}
			additional := hasAny(low, true, "additional")
			var fs []Fact
			var amt, prev, shr []Amount
			for _, a := range r.amountsIn(s[0], s[1]) {
				switch {
				case a.Unit == "shares":
					shr = append(shr, a)
				case !isMoney(a) || a.PerShare:
				case hasPrev(r.low, a.Span.Start, s[0], "from", "previously", "prior", "previous", "existing", "formerly"):
					prev = append(prev, a)
				case hasPrev(r.low, a.Span.Start, s[0], "remaining") || hasAny(r.low[a.Span.End:min(s[1], a.Span.End+14)], true, "remaining"):
				default:
					amt = append(amt, a)
				}
			}
			if len(amt) == 1 && !additional {
				fs = append(fs, factOf("amount", amt[0].Value, amt[0].Unit, "", amt[0]))
			}
			if len(shr) == 1 {
				fs = append(fs, factOf("shares", shr[0].Value, "shares", "", shr[0]))
			}
			if len(prev) == 1 && len(amt) == 1 && !additional {
				fs = append(fs, factOf("previous_amount", prev[0].Value, prev[0].Unit, "", prev[0]))
			}
			if hasAny(low, true, "fund", "financ", "paid") {
				debt := hasAny(low, true, "debt", "borrow", "notes", "credit facility", "term loan", "bond")
				cash := hasAny(low, false, "cash on hand", "existing cash", "cash balance", "available cash", "cash flow", "cash")
				switch {
				case debt && cash:
					labels = map[string]string{"financing": "mixed"}
				case debt:
					labels = map[string]string{"financing": "debt"}
				case cash:
					labels = map[string]string{"financing": "cash"}
				}
			}
			facts, haveFacts = fs, len(fs) > 0
		}
	}
	if typ == "" {
		return nil
	}
	if kind != "" && !mixed {
		if labels == nil {
			labels = map[string]string{}
		}
		labels["kind"] = kind
	}
	r, ok := build(e, typ, NewKey(Buyback, security(e), e.EventID), facts, labels, refs)
	if !ok {
		return nil
	}
	return []Record{r}
}

// buybackCue reports whether a repurchase sentence announces or expands a
// program. A sentence that only reports shares already bought back is not
// news of a program, and a bare mention of a plan or program, with no amount
// and no word of authorization, announcement or increase, is not either.
func buybackCue(r reading, s [2]int, low string) bool {
	if hasAny(low, true, "approv", "announc", "increas", "expand", "additional") {
		return true
	}
	if buybackKind(low) == "executed" {
		return false
	}
	if hasAny(low, true, "authoriz", "authoris") {
		return true
	}
	if !hasAny(low, true, "program", "plan") {
		return false
	}
	for _, a := range r.amountsIn(s[0], s[1]) {
		if (isMoney(a) || a.Unit == "shares") && !a.PerShare {
			return true
		}
	}
	return false
}

// buybackKind reads whether a repurchase sentence reports a decision of the
// board (authorization) or shares already bought (executed). It returns ""
// when the sentence supports neither, or both, so that a sentence such as "the
// board authorized X and the company repurchased Y" gets no label.
func buybackKind(low string) string {
	executed := hasAny(low, false, "repurchased", "bought back", "returned")
	decided := hasAny(low, false, "authorized", "authorised", "authorizes", "authorises", "approved", "approves", "announced", "announces") ||
		hasAny(low, true, "increas", "expand", "additional") ||
		(hasAny(low, true, "authoriz", "authoris") && (hasAny(low, false, "new") || hasAny(low, true, "rais", "upsiz", "boost")))
	switch {
	case executed && decided:
		return ""
	case executed:
		return "executed"
	case decided || hasAny(low, false, "up to"):
		return "authorization"
	}
	return ""
}

// capRun reads a capitalised name that starts at or after pos.
func capRun(text string, pos int) string {
	rest := strings.TrimLeft(text[pos:], " ")
	if len(rest) > 4 && fold(rest[:4]) == "the " {
		rest = strings.TrimLeft(rest[4:], " ")
	}
	var toks []string
	for _, t := range strings.Fields(rest) {
		end := false
		for len(t) > 0 && strings.ContainsRune(",;:)", rune(t[len(t)-1])) {
			t, end = t[:len(t)-1], true
		}
		if t == "" {
			break
		}
		connector := t == "of" || t == "and" || t == "&" || t == "for" || t == "the"
		if !(t[0] >= 'A' && t[0] <= 'Z') && !connector {
			break
		}
		toks = append(toks, t)
		if end || len(toks) >= 6 {
			break
		}
	}
	for len(toks) > 0 {
		last := toks[len(toks)-1]
		if last == "of" || last == "and" || last == "&" || last == "for" || last == "the" {
			toks = toks[:len(toks)-1]
			continue
		}
		break
	}
	if len(toks) == 0 {
		return ""
	}
	toks[len(toks)-1] = strings.TrimSuffix(toks[len(toks)-1], ".")
	name := strings.Join(toks, " ")
	switch fold(toks[0]) {
	case "a", "an", "company", "customer", "partner", "it", "its", "our", "we", "us", "this", "that", "each", "both", "one", "two", "another", "i":
		return ""
	}
	if len(name) < 2 {
		return ""
	}
	return name
}

func contractRecords(e EventText, rs []reading) []Record {
	for _, r := range rs {
		for _, s := range r.sents {
			low := r.low[s[0]:s[1]]
			if speculative(low) || !hasAny(low, true, "contract", "order", "award", "agreement", "deal", "subcontract") {
				continue
			}
			typ := Type("")
			switch {
			case hasAny(low, false, "terminated", "terminates", "cancelled", "canceled", "cancels"):
				typ = ContractTermination
			case hasAny(low, false, "won", "wins", "awarded", "awards", "signed", "signs", "secured", "secures", "secure", "win"):
				typ = ContractAward
			}
			if typ == "" {
				continue
			}
			var facts []Fact
			var totals, annuals, years []Amount
			for _, a := range r.amountsIn(s[0], s[1]) {
				switch {
				case a.Unit == "years":
					years = append(years, a)
				case !isMoney(a) || a.PerShare:
				case annualTail(r.low[a.Span.End:min(s[1], a.Span.End+14)]):
					annuals = append(annuals, a)
				default:
					totals = append(totals, a)
				}
			}
			var value, annual, dur *Amount
			if len(totals) == 1 {
				value = &totals[0]
				facts = append(facts, factOf("value", value.Value, value.Unit, "", *value))
			}
			if len(years) == 1 {
				dur = &years[0]
				facts = append(facts, factOf("duration_years", dur.Value, "years", "", *dur))
			}
			if len(annuals) == 1 {
				annual = &annuals[0]
				facts = append(facts, factOf("annual_value", annual.Value, annual.Unit, "", *annual))
			} else if value != nil && dur != nil && dur.Value > 0 {
				facts = append(facts, Fact{Name: "annual_value", Value: value.Value / dur.Value, Unit: value.Unit, Derived: true})
			}
			labels := map[string]string{}
			if c := contractCertainty(low, ceilingIn(r.low, s[0], totals, annuals)); typ == ContractAward && c != "" {
				labels["certainty"] = c
			}
			if value != nil && ceilingIn(r.low, s[0], totals) {
				labels["value_kind"] = "ceiling"
			}
			name := ""
			for _, w := range []string{" with ", " from "} {
				for from := 0; from < len(low); {
					k := strings.Index(low[from:], w)
					if k < 0 {
						break
					}
					if n := capRun(r.text[:s[1]], s[0]+from+k+len(w)); n != "" && name == "" {
						name = n
					}
					from += k + 1
				}
			}
			if name != "" {
				labels["counterparty"] = name
			}
			gov := hasAny(fold(name+" "+nameIfNone(name, r.text[s[0]:s[1]])), false, "department", "army", "navy", "air force", "nasa",
				"ministry", "agency", "pentagon", "marine corps", "space force", "government", "administration")
			switch {
			case gov:
				labels["customer"] = "government"
			case name != "":
				labels["customer"] = "commercial"
			default:
				labels["customer"] = "unknown"
			}
			rec, ok := build(e, typ, NewKey(Contract, security(e), e.EventID), facts, labels, map[string]bool{r.ref: true})
			if ok {
				return []Record{rec}
			}
		}
	}
	return nil
}

// contractCertainty reads how firm an award sentence says the contract is:
// conditional when it hangs on an approval, a financing or an option,
// framework when it is a standing agreement or states only a ceiling, firm when
// it says the award was made or the order signed. It returns "" when the
// sentence supports none, and never assumes firm.
func contractCertainty(low string, ceiling bool) string {
	switch {
	case hasAny(low, true, "subject to", "contingent", "conditional", "conditioned", "pending approval", "pending financing",
		"pending regulatory", "pending final", "pending funding", "pending completion", "option"):
		return "conditional"
	case ceiling || hasAny(low, false, "idiq", "mou", "indefinite-delivery", "indefinite delivery", "indefinite-quantity", "indefinite quantity",
		"framework agreement", "framework contract", "master agreement", "master services agreement", "master service agreement",
		"master supply agreement", "letter of intent", "memorandum of understanding"):
		return "framework"
	case hasAny(low, false, "awarded", "awards", "won", "wins", "signed", "signs", "secured", "secures", "received", "receives"):
		return "firm"
	}
	return ""
}

// ceilingIn reports whether the words before any of the figures say it is a
// maximum ("up to $X", "a maximum value of $X", "ceiling of $X"). lo is where
// the sentence starts.
func ceilingIn(low string, lo int, groups ...[]Amount) bool {
	for _, g := range groups {
		for _, a := range g {
			ws := prevWords(low, a.Span.Start, lo, 4)
			for i, w := range ws {
				if isDigit(w[0]) {
					break
				}
				if w == "maximum" || w == "ceiling" || (w == "to" && i+1 < len(ws) && ws[i+1] == "up") {
					return true
				}
			}
		}
	}
	return false
}

func nameIfNone(name, sentence string) string {
	if name != "" {
		return ""
	}
	return sentence
}

func analystRecords(e EventText, rs []reading) []Record {
	var out []Record
	seen := map[string]bool{}
	sec := security(e)
	for _, r := range rs {
		for _, s := range r.sents {
			low := r.low[s[0]:s[1]]
			if speculative(low) {
				continue
			}
			typ, vstart := analystType(low)
			if typ == "" {
				continue
			}
			facts, labels := analystFigures(r, s, typ)
			if facts == nil && labels == nil && typ != AnalystUpgrade && typ != AnalystDowngrade {
				// a direction without any figure is still a revision
				labels = map[string]string{}
			}
			if labels == nil {
				labels = map[string]string{}
			}
			if t, ok := resolveDirection(typ, facts, labels); ok {
				typ = t
			} else {
				continue
			}
			firm := firmOf(r.text, s[0], s[0]+vstart, s[1])
			if firm != "" {
				labels["firm"] = firm
			}
			key := NewKey(Analyst, sec, e.EventID, firm)
			if seen[key] {
				continue
			}
			rec, ok := build(e, typ, key, facts, labels, map[string]bool{r.ref: true})
			if ok {
				seen[key] = true
				out = append(out, rec)
			}
		}
	}
	return out
}

// analystType names the kind of revision a sentence reports and where its verb is.
func analystType(low string) (Type, int) {
	up, down := find(low, "upgrad", true), find(low, "downgrad", true)
	if up >= 0 && down >= 0 {
		return "", 0
	}
	if up >= 0 {
		return AnalystUpgrade, up
	}
	if down >= 0 {
		return AnalystDowngrade, down
	}
	raise, cut := -1, -1
	for i := 0; i < len(low); {
		if !isLetter(low[i]) {
			i++
			continue
		}
		w := letterRun(low, i)
		switch direction(w) {
		case "raise":
			if raise < 0 {
				raise = i
			}
		case "cut":
			if cut < 0 {
				cut = i
			}
		}
		i += len(w)
	}
	if raise >= 0 && cut >= 0 {
		return "", 0
	}
	target := hasAny(low, false, "price target", "target price", "price objective", "pt", "target")
	estimate := hasAny(low, true, "estimate", "forecast", "eps")
	pos := max(raise, cut)
	switch {
	case target && raise >= 0:
		return TargetRaised, pos
	case target && cut >= 0:
		return TargetCut, pos
	case estimate && raise >= 0 && !hasAny(low, true, "beat", "top", "miss"):
		return EstimateRaised, pos
	case estimate && cut >= 0 && !hasAny(low, true, "beat", "top", "miss"):
		return EstimateCut, pos
	case target && hasAny(low, false, "to") && hasAny(low, false, "from"):
		return TargetRaised, 0
	}
	return "", 0
}

func analystFigures(r reading, s [2]int, typ Type) ([]Fact, map[string]string) {
	low := r.low
	switch typ {
	case AnalystUpgrade, AnalystDowngrade:
		labels := map[string]string{}
		seg := low[s[0]:s[1]]
		if k := find(seg, "to", false); k >= 0 {
			if name := ratingAt(seg, k+2); name != "" {
				labels["new_rating"] = strings.TrimSpace(r.text[s[0]+k+2+leadingSpace(seg[k+2:]) : s[0]+k+2+leadingSpace(seg[k+2:])+len(name)])
			}
		}
		if k := find(seg, "from", false); k >= 0 {
			if name := ratingAt(seg, k+4); name != "" {
				labels["previous_rating"] = strings.TrimSpace(r.text[s[0]+k+4+leadingSpace(seg[k+4:]) : s[0]+k+4+leadingSpace(seg[k+4:])+len(name)])
			}
		}
		return nil, labels
	}
	newName, prevName := "new_target", "previous_target"
	if typ == EstimateRaised || typ == EstimateCut {
		newName, prevName = "new_estimate", "previous_estimate"
	}
	var news, prevs []Amount
	for _, a := range r.amountsIn(s[0], s[1]) {
		if !isMoney(a) {
			continue
		}
		if hasPrev(low, a.Span.Start, s[0], "from", "previously", "prior", "previous", "versus", "vs", "formerly", "was") {
			prevs = append(prevs, a)
		} else {
			news = append(news, a)
		}
	}
	var facts []Fact
	if len(news) == 1 && len(prevs) <= 1 {
		facts = append(facts, factOf(newName, news[0].Value, news[0].Unit+"/share", "", news[0]))
		if len(prevs) == 1 && prevs[0].Unit == news[0].Unit {
			facts = append(facts, factOf(prevName, prevs[0].Value, prevs[0].Unit+"/share", "", prevs[0]))
			if prevs[0].Value != 0 {
				facts = append(facts, Fact{Name: "change", Value: news[0].Value/prevs[0].Value - 1, Unit: "ratio", Derived: true})
			}
		}
	}
	return facts, nil
}

func leadingSpace(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

func ratingAt(low string, i int) string {
	i += leadingSpace(low[i:])
	for _, p := range []string{"strong buy", "strong sell", "market perform", "equal weight", "equal-weight", "sector perform", "sector weight",
		"peer perform", "in line", "outperform", "overweight", "underperform", "underweight", "neutral", "accumulate", "reduce",
		"buy", "hold", "sell", "add", "positive", "negative"} {
		if strings.HasPrefix(low[i:], p) && (i+len(p) >= len(low) || !isAlnum(low[i+len(p)])) {
			return p
		}
	}
	return ""
}

func ratingRank(label string) int {
	switch strings.ReplaceAll(fold(label), "-", " ") {
	case "strong buy":
		return 5
	case "buy", "outperform", "overweight", "accumulate", "add", "positive":
		return 4
	case "hold", "neutral", "market perform", "equal weight", "sector perform", "in line", "peer perform", "sector weight":
		return 3
	case "underperform", "underweight", "reduce":
		return 2
	case "sell", "strong sell", "negative":
		return 1
	}
	return 0
}

// resolveDirection checks the type a verb suggests against the figures or
// ratings the sentence states.
func resolveDirection(typ Type, facts []Fact, labels map[string]string) (Type, bool) {
	switch typ {
	case AnalystUpgrade, AnalystDowngrade:
		n, p := ratingRank(labels["new_rating"]), ratingRank(labels["previous_rating"])
		if n > 0 && p > 0 {
			if (typ == AnalystUpgrade && n < p) || (typ == AnalystDowngrade && n > p) {
				return "", false
			}
		}
		return typ, true
	}
	var nv, pv float64
	have := 0
	for _, f := range facts {
		switch f.Name {
		case "new_target", "new_estimate":
			nv, have = f.Value, have|1
		case "previous_target", "previous_estimate":
			pv, have = f.Value, have|2
		}
	}
	if have != 3 {
		return typ, true
	}
	up := nv > pv
	if nv == pv {
		return "", false
	}
	switch typ {
	case TargetRaised, TargetCut:
		if up {
			return TargetRaised, typ == TargetRaised
		}
		return TargetCut, typ == TargetCut
	default:
		if up {
			return EstimateRaised, typ == EstimateRaised
		}
		return EstimateCut, typ == EstimateCut
	}
}

// firmOf reads the firm that published a revision: the capitalised name before
// the verb, or the name after "by".
func firmOf(text string, sStart, verb, sEnd int) string {
	low := fold(text[verb:sEnd])
	if k := find(low, "by", false); k >= 0 {
		if n := capRun(text[:sEnd], verb+k+2); n != "" {
			return n
		}
	}
	prefix := strings.TrimSpace(text[sStart:verb])
	for _, sep := range []string{":", " - ", " – ", " | "} {
		if k := strings.LastIndex(prefix, sep); k >= 0 {
			prefix = strings.TrimSpace(prefix[k+len(sep):])
		}
	}
	toks := strings.Fields(prefix)
	for len(toks) > 0 {
		switch fold(toks[len(toks)-1]) {
		case "analyst", "analysts", "also", "now", "just", "again":
			toks = toks[:len(toks)-1]
			continue
		}
		break
	}
	if len(toks) == 0 || len(toks) > 4 {
		return ""
	}
	for i, t := range toks {
		if t == "&" || ((t == "of" || t == "and") && i > 0 && i < len(toks)-1) {
			continue
		}
		if !(t[0] >= 'A' && t[0] <= 'Z') {
			return ""
		}
		for j := 0; j < len(t); j++ {
			if !isAlnum(t[j]) && !strings.ContainsRune("&.'-", rune(t[j])) {
				return ""
			}
		}
	}
	return strings.Join(toks, " ")
}

// Surprise reads whether the text says results were above or below
// expectations. ok is false when it says neither or both.
func Surprise(passages []Passage) (t Type, facts []Fact, ok bool) {
	beat, miss := false, false
	for _, r := range readPassages(passages) {
		for _, s := range r.sents {
			low := r.low[s[0]:s[1]]
			if speculative(low) {
				continue
			}
			b, m := surpriseWords(low)
			if !b && !m {
				continue
			}
			beat, miss = beat || b, miss || m
			if b == m {
				continue
			}
			var ratios []Amount
			for _, a := range r.amountsIn(s[0], s[1]) {
				if a.Unit == "ratio" {
					ratios = append(ratios, a)
				}
			}
			eps := hasAny(low, true, "eps", "earnings per share", "per share")
			rev := hasAny(low, true, "revenue", "sales")
			if len(ratios) != 1 || eps == rev {
				continue
			}
			v, name := ratios[0].Value, "eps_surprise"
			if rev {
				name = "revenue_surprise"
			}
			if m {
				v = -v
			}
			facts = append(facts, factOf(name, v, "ratio", "", ratios[0]))
		}
	}
	switch {
	case beat && !miss:
		return EarningsBeat, facts, true
	case miss && !beat:
		return EarningsMiss, facts, true
	}
	return "", nil, false
}

func surpriseWords(low string) (beat, miss bool) {
	var toks []string
	for i := 0; i < len(low); {
		if !isAlnum(low[i]) {
			i++
			continue
		}
		j := i
		for j < len(low) && isAlnum(low[j]) {
			j++
		}
		toks = append(toks, low[i:j])
		i = j
	}
	targets := map[string]bool{"estimates": true, "estimate": true, "consensus": true, "expectations": true, "forecasts": true,
		"forecast": true, "projections": true, "analysts": true, "street": true}
	pats := []struct {
		seq  []string
		beat bool
	}{
		{[]string{"beat"}, true}, {[]string{"beats"}, true}, {[]string{"beating"}, true}, {[]string{"tops"}, true},
		{[]string{"topped"}, true}, {[]string{"exceeds"}, true}, {[]string{"exceeded"}, true}, {[]string{"exceeding"}, true},
		{[]string{"surpasses"}, true}, {[]string{"surpassed"}, true}, {[]string{"above"}, true}, {[]string{"ahead", "of"}, true},
		{[]string{"better", "than"}, true},
		{[]string{"misses"}, false}, {[]string{"missed"}, false}, {[]string{"miss"}, false}, {[]string{"falls", "short", "of"}, false},
		{[]string{"fell", "short", "of"}, false}, {[]string{"short", "of"}, false}, {[]string{"below"}, false}, {[]string{"lags"}, false},
		{[]string{"lagged"}, false}, {[]string{"trails"}, false}, {[]string{"trailed"}, false}, {[]string{"worse", "than"}, false},
	}
	for i := range toks {
		for _, p := range pats {
			if i+len(p.seq) > len(toks) {
				continue
			}
			match := true
			for k, w := range p.seq {
				if toks[i+k] != w {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			for k := i + len(p.seq); k < len(toks) && k <= i+len(p.seq)+2; k++ {
				if targets[toks[k]] {
					if p.beat {
						beat = true
					} else {
						miss = true
					}
					break
				}
			}
		}
	}
	return beat, miss
}

func annualTail(tail string) bool {
	tail = strings.TrimLeft(tail, " ,")
	for _, p := range []string{"per year", "a year", "annually", "per annum", "each year", "every year"} {
		if strings.HasPrefix(tail, p) {
			return true
		}
	}
	return false
}
