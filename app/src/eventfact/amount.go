package eventfact

import (
	"math"
	"strconv"
	"strings"
)

// Amount is one figure read from text.
type Amount struct {
	Value    float64 // in base units: "$1.2 billion" is 1.2e9; "5%" is 0.05
	Unit     string  // ISO currency code, "ratio" for a percentage, "shares", "years", or "" for a bare number
	PerShare bool    // "$2.10 per share", "$2.10 a share", "EPS of $2.10"
	Span     Span    // byte offsets into the text and the exact phrase
}

// figure is an Amount before its scale and its range neighbours are settled.
type figure struct {
	Amount
	raw   float64
	scale float64
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isAlnum(c byte) bool  { return isDigit(c) || isLetter(c) }

// fold lower-cases ASCII letters only, so byte offsets in the result are the
// offsets in the original.
func fold(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func letterRun(s string, i int) string {
	j := i
	for j < len(s) && isLetter(s[j]) {
		j++
	}
	return s[i:j]
}

func isMoneyUnit(u string) bool {
	switch u {
	case "USD", "EUR", "GBP", "CHF", "JPY", "CAD":
		return true
	}
	return false
}

// Amounts returns every figure in text, in order of position.
func Amounts(text string) []Amount {
	var figs []figure
	for i := 0; i < len(text); {
		f, end, ok := readFigure(text, i)
		if ok {
			figs = append(figs, f)
		}
		if end > i {
			i = end
		} else {
			i++
		}
	}
	linkRanges(text, figs)
	out := make([]Amount, len(figs))
	for k, f := range figs {
		f.Value = f.raw * f.scale
		if f.Unit == "ratio" {
			f.Value /= 100
		}
		out[k] = f.Amount
	}
	return out
}

func currencyPrefix(text string, i int) (string, int) {
	rest := text[i:]
	for _, p := range [][2]string{
		{"US$", "USD"}, {"CA$", "CAD"}, {"C$", "CAD"}, {"$", "USD"}, {"€", "EUR"}, {"£", "GBP"}, {"¥", "JPY"},
		{"USD", "USD"}, {"EUR", "EUR"}, {"GBP", "GBP"}, {"CHF", "CHF"}, {"JPY", "JPY"}, {"CAD", "CAD"},
	} {
		if !strings.HasPrefix(rest, p[0]) {
			continue
		}
		n := len(p[0])
		if isLetter(p[0][n-1]) && n < len(rest) && isLetter(rest[n]) {
			continue
		}
		return p[1], n
	}
	return "", 0
}

func numberWord(s string) (float64, bool) {
	words := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
		"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen", "twenty"}
	s = fold(s)
	for i, w := range words {
		if s == w {
			return float64(i + 1), true
		}
	}
	return 0, false
}

// readNumber reads digits with thousands separators and decimals at i.
func readNumber(text string, i int) (val float64, end int, yearLike, ok bool) {
	j := i
	for j < len(text) && isDigit(text[j]) {
		j++
	}
	firstLen := j - i
	var b strings.Builder
	b.WriteString(text[i:j])
	commas := false
	for firstLen <= 3 && j+3 < len(text) && text[j] == ',' && isDigit(text[j+1]) && isDigit(text[j+2]) && isDigit(text[j+3]) &&
		(j+4 >= len(text) || !isDigit(text[j+4])) {
		b.WriteString(text[j+1 : j+4])
		j += 4
		commas = true
	}
	decimal := false
	if j+1 < len(text) && text[j] == '.' && isDigit(text[j+1]) {
		k := j + 1
		for k < len(text) && isDigit(text[k]) {
			k++
		}
		b.WriteString(text[j:k])
		j = k
		decimal = true
	}
	v, err := strconv.ParseFloat(b.String(), 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, j, false, false
	}
	yearLike = !commas && !decimal && firstLen == 4 && v >= 1900 && v <= 2100
	return v, j, yearLike, true
}

func readScale(text string, i int, hasCurrency bool) (float64, int, bool) {
	k := i
	spaced := false
	if k < len(text) && text[k] == ' ' {
		k++
		spaced = true
	}
	run := letterRun(text, k)
	if run == "" {
		return 0, 0, false
	}
	e := k + len(run)
	switch fold(run) {
	case "thousand":
		return 1e3, e, true
	case "million", "mn", "mln":
		return 1e6, e, true
	case "billion", "bn", "bln":
		return 1e9, e, true
	case "trillion", "tn":
		return 1e12, e, true
	case "k", "m", "b":
		if (spaced && !hasCurrency) || (e < len(text) && (isAlnum(text[e]) || text[e] == '-')) {
			return 0, 0, false
		}
		return map[string]float64{"k": 1e3, "m": 1e6, "b": 1e9}[fold(run)], e, true
	}
	return 0, 0, false
}

// suffixUnit reads the unit that follows a number: a percent sign or word, a
// duration, a currency word or a share count.
func suffixUnit(text string, i int, hasScale bool) (string, int) {
	k := i
	if k < len(text) && text[k] == ' ' {
		k++
	}
	if !hasScale && k < len(text) && text[k] == '%' {
		return "ratio", k + 1
	}
	run := letterRun(text, k)
	low := fold(run)
	e := k + len(run)
	if !hasScale {
		switch low {
		case "percent":
			return "ratio", e
		case "percentage":
			if w := letterRun(text, e+1); e < len(text) && text[e] == ' ' && (fold(w) == "point" || fold(w) == "points") {
				return "ratio", e + 1 + len(w)
			}
		case "per":
			if strings.HasPrefix(fold(text[e:min(len(text), e+6)]), " cent") && (e+5 >= len(text) || !isLetter(text[e+5])) {
				return "ratio", e + 5
			}
		}
		// A duration may be joined by a hyphen: "5-year".
		y := i
		if y < len(text) && (text[y] == '-' || text[y] == ' ') {
			y++
		}
		yr := letterRun(text, y)
		if fy := fold(yr); fy == "year" || fy == "years" {
			ye := y + len(yr)
			tail := fold(text[ye:min(len(text), ye+6)])
			if !strings.HasPrefix(tail, "-old") && !strings.HasPrefix(tail, "-over") && !strings.HasPrefix(tail, " ago") {
				return "years", ye
			}
		}
	}
	switch low {
	case "dollars", "dollar", "usd":
		return "USD", e
	case "euros", "euro", "eur":
		return "EUR", e
	case "pounds", "gbp":
		if low == "gbp" || hasScale {
			return "GBP", e
		}
	case "francs", "chf":
		return "CHF", e
	case "yen", "jpy":
		return "JPY", e
	case "cad":
		return "CAD", e
	case "us", "canadian":
		if w := letterRun(text, e+1); e < len(text) && text[e] == ' ' && (fold(w) == "dollars" || fold(w) == "dollar") {
			if low == "us" {
				return "USD", e + 1 + len(w)
			}
			return "CAD", e + 1 + len(w)
		}
	case "shares":
		return "shares", e
	case "common", "ordinary":
		if w := letterRun(text, e+1); e < len(text) && text[e] == ' ' && fold(w) == "shares" {
			return "shares", e + 1 + len(w)
		}
	}
	return "", 0
}

func perShareAfter(text string, end int) int {
	k := end
	for k < len(text) && text[k] == ' ' && k < end+3 {
		k++
	}
	rest := fold(text[k:min(len(text), k+24)])
	for _, p := range []string{"per diluted share", "per basic share", "per common share", "per ordinary share", "per share", "a diluted share", "a share", "/share"} {
		if strings.HasPrefix(rest, p) && (len(p) >= len(rest) || !isLetter(rest[len(p)])) {
			return k + len(p)
		}
	}
	return 0
}

func epsBefore(text string, start int) bool {
	low := strings.TrimRight(fold(text[max(0, start-30):start]), " (")
	for _, p := range []string{"eps of", "eps at", "eps to", "eps from", "eps", "earnings per share of", "earnings per share at",
		"earnings per share to", "earnings per share from", "earnings per share", "per share of", "per share"} {
		if strings.HasSuffix(low, p) && (len(low) == len(p) || !isAlnum(low[len(low)-len(p)-1])) {
			return true
		}
	}
	return false
}

func prevWordOf(text string, pos int) string {
	i := pos
	for i > 0 && text[i-1] == ' ' {
		i--
	}
	if i > 0 && text[i-1] == '.' {
		i--
	}
	j := i
	for i > 0 && isLetter(text[i-1]) {
		i--
	}
	return fold(text[i:j])
}

// bareOK says whether a number with no unit and no scale is a figure and not a
// year, a date, a time or an item number.
func bareOK(text string, start, end int, yearLike bool) bool {
	if yearLike {
		return false
	}
	if end < len(text) {
		c := text[end]
		if isLetter(c) || c == ':' || c == '/' || (c == '-' && end+1 < len(text) && isLetter(text[end+1])) {
			return false
		}
		if tail := fold(text[end:min(len(text), end+5)]); strings.HasPrefix(tail, " a.m") || strings.HasPrefix(tail, " p.m") ||
			strings.HasPrefix(tail, " am") || strings.HasPrefix(tail, " pm") {
			return false
		}
	}
	if start > 0 && (text[start-1] == ':' || text[start-1] == '/' || text[start-1] == '#') {
		return false
	}
	switch prevWordOf(text, start) {
	case "item", "items", "section", "sections", "form", "no", "number", "page", "pages", "q", "fy", "fiscal", "rule", "exhibit",
		"article", "version", "v", "part", "table", "figure", "note", "chapter", "release", "jan", "feb", "mar", "apr", "may", "jun",
		"jul", "aug", "sep", "sept", "oct", "nov", "dec", "january", "february", "march", "april", "june", "july", "august",
		"september", "october", "november", "december", "quarter", "week", "day":
		return false
	}
	return true
}

func readFigure(text string, i int) (figure, int, bool) {
	if i > 0 {
		p := text[i-1]
		if isAlnum(p) || ((p == '.' || p == ',') && i > 1 && isDigit(text[i-2])) {
			return figure{}, 0, false
		}
	}
	f := figure{scale: 1}
	if isLetter(text[i]) {
		run := letterRun(text, i)
		v, ok := numberWord(run)
		if !ok {
			cur, n := currencyPrefix(text, i)
			if n == 0 {
				return figure{}, len(run), false
			}
			return readAfterPrefix(text, i, n, cur)
		}
		e := i + len(run)
		if e < len(text) && (text[e] == '-' || text[e] == ' ') {
			w := letterRun(text, e+1)
			if fw := fold(w); fw == "year" || fw == "years" {
				ye := e + 1 + len(w)
				if tail := fold(text[ye:min(len(text), ye+6)]); !strings.HasPrefix(tail, "-old") && !strings.HasPrefix(tail, "-over") && !strings.HasPrefix(tail, " ago") {
					f.raw, f.Unit = v, "years"
					f.Span = Span{Start: i, End: ye, Text: text[i:ye]}
					return f, ye, true
				}
			}
		}
		return figure{}, len(run), false
	}
	if cur, n := currencyPrefix(text, i); n > 0 {
		return readAfterPrefix(text, i, n, cur)
	}
	if !isDigit(text[i]) {
		return figure{}, 0, false
	}
	return readBody(text, i, i, "")
}

func readAfterPrefix(text string, start, n int, cur string) (figure, int, bool) {
	pos := start + n
	if pos < len(text) && text[pos] == ' ' {
		pos++
	}
	if pos >= len(text) || !isDigit(text[pos]) {
		return figure{}, 0, false
	}
	return readBody(text, start, pos, cur)
}

func readBody(text string, start, numStart int, cur string) (figure, int, bool) {
	f := figure{scale: 1}
	f.Unit = cur
	raw, numEnd, yearLike, ok := readNumber(text, numStart)
	if !ok {
		return figure{}, numEnd, false
	}
	f.raw = raw
	end := numEnd
	hasScale := false
	if sc, e, ok := readScale(text, end, cur != ""); ok {
		f.scale, end, hasScale = sc, e, true
	}
	if cur == "" {
		if u, e := suffixUnit(text, end, hasScale); u != "" {
			f.Unit, end = u, e
		}
	}
	if isMoneyUnit(f.Unit) {
		if e := perShareAfter(text, end); e > 0 {
			f.PerShare, end = true, e
		} else if epsBefore(text, start) {
			f.PerShare = true
		}
	}
	if f.Unit == "" && !hasScale && !bareOK(text, start, end, yearLike) {
		return figure{}, end, false
	}
	if f.Unit == "" && end < len(text) && isLetter(text[end]) {
		return figure{}, end, false
	}
	f.Span = Span{Start: start, End: end, Text: text[start:end]}
	return f, end, true
}

// linkRanges lets the two ends of a range share what only one of them states:
// the currency, the scale and the per-share mark.
func linkRanges(text string, figs []figure) {
	for k := 0; k+1 < len(figs); k++ {
		a, b := &figs[k], &figs[k+1]
		if a.Span.End > b.Span.Start {
			continue
		}
		switch strings.TrimSpace(fold(text[a.Span.End:b.Span.Start])) {
		case "to", "-", "–", "—", "and", "through":
		default:
			continue
		}
		if a.Unit == "" && (isMoneyUnit(b.Unit) || b.Unit == "ratio") {
			a.Unit = b.Unit
		}
		if b.Unit == "" && (isMoneyUnit(a.Unit) || a.Unit == "ratio") {
			b.Unit = a.Unit
		}
		if a.scale == 1 && b.scale > 1 && a.raw <= b.raw && (a.Unit == "" || isMoneyUnit(a.Unit)) {
			a.scale = b.scale
		}
		if a.PerShare != b.PerShare && isMoneyUnit(a.Unit) && a.Unit == b.Unit {
			a.PerShare, b.PerShare = true, true
		}
	}
}

// Period returns the fiscal period a sentence names in a normal form such as
// "FY2024", "Q3 2026" or "Q3 FY2025"; empty when it names none.
func Period(text string) string {
	var toks []string
	for i := 0; i < len(text); {
		if !isAlnum(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && isAlnum(text[j]) {
			j++
		}
		toks = append(toks, fold(text[i:j]))
		i = j
	}
	year := func(w string) (int, bool, bool) {
		digits, fiscal := w, false
		if strings.HasPrefix(w, "fy") {
			digits, fiscal = w[2:], true
		}
		if n, err := strconv.Atoi(digits); err == nil && len(digits) == 4 && n >= 1990 && n <= 2100 {
			return n, fiscal, true
		}
		if fiscal && len(digits) == 2 {
			if n, err := strconv.Atoi(digits); err == nil {
				return 2000 + n, true, true
			}
		}
		return 0, false, false
	}
	quarter := func(i int) (q, next int, fiscal bool) {
		w := toks[i]
		if len(w) == 2 && w[0] == 'q' && w[1] >= '1' && w[1] <= '4' {
			return int(w[1] - '0'), i + 1, i > 0 && toks[i-1] == "fiscal"
		}
		ord := map[string]int{"first": 1, "second": 2, "third": 3, "fourth": 4, "1st": 1, "2nd": 2, "3rd": 3, "4th": 4}[w]
		if ord > 0 && i+1 < len(toks) && toks[i+1] == "quarter" {
			return ord, i + 2, i > 0 && toks[i-1] == "fiscal"
		}
		return 0, 0, false
	}
	for i := range toks {
		if q, next, fiscal := quarter(i); q > 0 {
			for j := next; j < len(toks) && j <= next+3; j++ {
				switch toks[j] {
				case "of", "year":
					continue
				case "fiscal", "fy":
					fiscal = true
					continue
				}
				if y, fy, ok := year(toks[j]); ok {
					if fy || fiscal {
						return "Q" + strconv.Itoa(q) + " FY" + strconv.Itoa(y)
					}
					return "Q" + strconv.Itoa(q) + " " + strconv.Itoa(y)
				}
				break
			}
			continue
		}
		marker := toks[i] == "fiscal" || toks[i] == "fy" || (toks[i] == "full" && i+1 < len(toks) && toks[i+1] == "year")
		if y, fy, ok := year(toks[i]); ok && fy {
			return "FY" + strconv.Itoa(y)
		} else if marker {
			for j := i + 1; j < len(toks) && j <= i+3; j++ {
				if toks[j] == "year" || toks[j] == "of" || toks[j] == "full" {
					continue
				}
				if y, _, ok := year(toks[j]); ok {
					return "FY" + strconv.Itoa(y)
				}
				break
			}
		}
	}
	return ""
}
