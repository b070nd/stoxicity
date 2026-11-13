package marketdata

import (
	"context"
	"sort"
	"strings"
	"time"
)

// SpanKind says how a symbol span ended, or, for a spinoff, what it records.
type SpanKind string

const (
	// SpanOpen is the kind of a span that has not ended: the symbol is still
	// the security's.
	SpanOpen SpanKind = ""
	// SpanRename says the span ended because the same security took another
	// symbol. The next span of the same security continues it, and a price
	// series joins across the two.
	SpanRename SpanKind = "rename"
	// SpanMerger says the span ended because the company was absorbed or
	// replaced by another security, named by the successor when it is known.
	// The successor is a different security, so no series joins across it.
	SpanMerger SpanKind = "merger"
	// SpanSpinoff records that a new security was cut out of this one, named by
	// the successor. The security kept trading, so the row marks the day and
	// joins nothing and ends nothing.
	SpanSpinoff SpanKind = "spinoff"
	// SpanDelisting says the span ended because the security stopped trading
	// and nothing says it came back. The series ends there for good.
	SpanDelisting SpanKind = "delisting"
	// SpanRelisting says the span ended because the security was delisted and
	// traded again later as the same security under the next span, which a
	// price series joins across the gap.
	SpanRelisting SpanKind = "relisting"
)

// Valid reports whether the kind is one that is known, including SpanOpen.
func (k SpanKind) Valid() bool {
	switch k {
	case SpanOpen, SpanRename, SpanMerger, SpanSpinoff, SpanDelisting, SpanRelisting:
		return true
	}
	return false
}

// stitches reports whether a series continues into the next span of the
// security after a span that ended this way.
func (k SpanKind) stitches() bool { return k == SpanRename || k == SpanRelisting }

// SymbolSpan is one period in which one symbol was one security's. It is
// evidence about identity: a span joins tickers only when Verified says
// somebody checked it against the document in SourceRef.
type SymbolSpan struct {
	// Security is the key of the security, the same for every symbol it has had.
	Security string
	// Symbol is the ticker as the source writes it, upper case.
	Symbol string
	// From is the first day the symbol was the security's, YYYY-MM-DD.
	From string
	// To is the last day it was (inclusive), YYYY-MM-DD; empty while it still is.
	To string
	// Kind says how the span ended; see SpanKind.
	Kind SpanKind
	// Successor is the key of the other security a merger or a spinoff led to;
	// empty when none, or when it is not known.
	Successor string
	// Source says where the row came from, such as "import:file.csv" or "manual".
	Source string
	// SourceRef names the document that states it: an identifier or an address.
	SourceRef string
	// Verified says a person checked the row against SourceRef.
	Verified bool
	// Note is free text.
	Note string
	// RecordedAt is when this revision was stored; zero before it is stored.
	RecordedAt time.Time
}

// Normalized returns the span with its text trimmed and the symbol in upper
// case, which is how it is stored and compared.
func (s SymbolSpan) Normalized() SymbolSpan {
	s.Security = strings.TrimSpace(s.Security)
	s.Symbol = strings.ToUpper(strings.TrimSpace(s.Symbol))
	s.From, s.To = strings.TrimSpace(s.From), strings.TrimSpace(s.To)
	s.Kind = SpanKind(strings.ToLower(strings.TrimSpace(string(s.Kind))))
	s.Successor = strings.TrimSpace(s.Successor)
	s.Source, s.SourceRef = strings.TrimSpace(s.Source), strings.TrimSpace(s.SourceRef)
	s.Note = strings.TrimSpace(s.Note)
	return s
}

// The reasons a symbol span is refused. Each is the whole error text, with
// nothing of the row in it, so that rows refused for one reason can be counted.
const (
	// SpanNoSecurity says the security key is empty.
	SpanNoSecurity = "marketdata: symbol span: no security"
	// SpanNoSymbol says the symbol is empty.
	SpanNoSymbol = "marketdata: symbol span: no symbol"
	// SpanBadFrom says valid_from is not a day.
	SpanBadFrom = "marketdata: symbol span: valid_from is not a day in the form YYYY-MM-DD"
	// SpanBadTo says valid_to is not a day, or is before valid_from.
	SpanBadTo = "marketdata: symbol span: valid_to is not a day on or after valid_from"
	// SpanBadKind says the kind is not one that is known.
	SpanBadKind = "marketdata: symbol span: unknown kind"
	// SpanEndNeedsKind says a span that ended does not say how.
	SpanEndNeedsKind = "marketdata: symbol span: valid_to is set but kind is empty"
	// SpanKindNeedsEnd says a kind that ends a span has no valid_to.
	SpanKindNeedsEnd = "marketdata: symbol span: kind ends the span but valid_to is empty"
	// SpanSuccessorKind says a successor is given for a kind that has none.
	SpanSuccessorKind = "marketdata: symbol span: a successor only goes with a merger or a spinoff"
	// SpanSpinoffNeedsSuccessor says a spinoff does not name the new security.
	SpanSpinoffNeedsSuccessor = "marketdata: symbol span: a spinoff needs a successor"
	// SpanNoSource says the source is empty.
	SpanNoSource = "marketdata: symbol span: no source"
	// SpanNoSourceRef says a verified span names no document.
	SpanNoSourceRef = "marketdata: symbol span: verified without a source_ref"
)

type spanError string

func (e spanError) Error() string { return string(e) }

func validSpanDay(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// Validate returns an error, one of the span reasons, when the span is not
// consistent. The span is checked as it is given; use Normalized first.
func (s SymbolSpan) Validate() error {
	switch {
	case s.Security == "":
		return spanError(SpanNoSecurity)
	case s.Symbol == "":
		return spanError(SpanNoSymbol)
	case !validSpanDay(s.From):
		return spanError(SpanBadFrom)
	case s.To != "" && (!validSpanDay(s.To) || s.To < s.From):
		return spanError(SpanBadTo)
	case !s.Kind.Valid():
		return spanError(SpanBadKind)
	case s.To != "" && s.Kind == SpanOpen:
		return spanError(SpanEndNeedsKind)
	case s.To == "" && s.Kind != SpanOpen && s.Kind != SpanSpinoff:
		return spanError(SpanKindNeedsEnd)
	case s.Successor != "" && s.Kind != SpanMerger && s.Kind != SpanSpinoff:
		return spanError(SpanSuccessorKind)
	case s.Kind == SpanSpinoff && s.Successor == "":
		return spanError(SpanSpinoffNeedsSuccessor)
	case s.Source == "":
		return spanError(SpanNoSource)
	case s.Verified && s.SourceRef == "":
		return spanError(SpanNoSourceRef)
	}
	return nil
}

// covers reports whether the symbol was the security's on the day.
func (s SymbolSpan) covers(day string) bool {
	return s.From <= day && (s.To == "" || day <= s.To)
}

// Link is one symbol of a security over the days it was the security's.
type Link struct {
	Symbol string
	From   string // first day, YYYY-MM-DD
	To     string // last day, inclusive; empty while it still is
}

// Lineage is what the verified spans say about the security a ticker belongs
// to.
type Lineage struct {
	// Security is the key of the security; empty when no verified span names
	// the ticker, and then the rest holds only what was ignored.
	Security string
	// Spans are the verified spans of the security that make one series,
	// oldest first: a rename or a relisting joins a span to the next.
	Spans []SymbolSpan
	// EndedOn is the last day of the final span when it ended by a delisting or
	// a merger, so that a study can tell a series that legitimately stops from
	// one with missing data. Empty otherwise.
	EndedOn string
	// EndKind is SpanDelisting or SpanMerger when EndedOn is set.
	EndKind SpanKind
	// Ignored are the spans that were not used because they are not verified
	// and name the ticker or the security. They are information only.
	Ignored []SymbolSpan
}

// Links returns the symbol, first day and last day of each span, oldest first.
func (l Lineage) Links() []Link {
	out := make([]Link, len(l.Spans))
	for i, s := range l.Spans {
		out[i] = Link{Symbol: s.Symbol, From: s.From, To: s.To}
	}
	return out
}

// Chain returns the tickers of the one security that symbol belonged to on the
// day, in order, using only verified spans; an unverified span never joins two
// tickers and is listed in Ignored. With an empty day the newest span that
// names the symbol is used.
//
//   - The security is the one whose verified span carried the symbol on the day
//     (compared as SameTicker does). When none did, or verified spans of two
//     securities did, nothing is joined.
//   - Going back and forward from that span, a span joins the next span of the
//     same security only when it ended by a rename or a relisting and the next
//     one began after it ended. A merger, a delisting, or a gap without such a
//     kind stops the chain: a merger's successor is another security.
//   - A spinoff row is a note about a date; it is never part of a chain.
func Chain(spans []SymbolSpan, symbol, day string) Lineage {
	var verified []SymbolSpan
	for _, s := range spans {
		if s.Verified && s.Kind != SpanSpinoff {
			verified = append(verified, s)
		}
	}
	sort.SliceStable(verified, func(i, j int) bool { return verified[i].From < verified[j].From })
	anchor, conflict := -1, false
	for i, s := range verified {
		if !SameTicker(s.Symbol, symbol) || (day != "" && !s.covers(day)) {
			continue
		}
		conflict = conflict || (day != "" && anchor >= 0 && verified[anchor].Security != s.Security)
		anchor = i
	}
	if anchor < 0 || conflict {
		return Lineage{Ignored: ignoredSpans(spans, symbol, "")}
	}
	security := verified[anchor].Security
	var mine []SymbolSpan
	at := 0
	for i, s := range verified {
		if s.Security != security {
			continue
		}
		if i == anchor {
			at = len(mine)
		}
		mine = append(mine, s)
	}
	lo, hi := at, at
	for lo > 0 && joins(mine[lo-1], mine[lo]) {
		lo--
	}
	for hi < len(mine)-1 && joins(mine[hi], mine[hi+1]) {
		hi++
	}
	out := Lineage{Security: security, Spans: mine[lo : hi+1], Ignored: ignoredSpans(spans, symbol, security)}
	if last := out.Spans[len(out.Spans)-1]; last.Kind == SpanDelisting || last.Kind == SpanMerger {
		out.EndedOn, out.EndKind = last.To, last.Kind
	}
	return out
}

// anchorDay is the day a ticker is read on for the range from to to (either may
// be empty): the last day of the range on which a verified span carries the
// symbol, taken from the newest such span. It reports false when no verified
// span carries the symbol in the range. When neither the range nor the span has
// an end the day is empty, which Chain reads as the newest span.
func anchorDay(spans []SymbolSpan, symbol, from, to string) (string, bool) {
	var best *SymbolSpan
	for i, s := range spans {
		if !s.Verified || s.Kind == SpanSpinoff || !SameTicker(s.Symbol, symbol) {
			continue
		}
		if (to != "" && s.From > to) || (from != "" && s.To != "" && s.To < from) {
			continue
		}
		if best == nil || s.From > best.From {
			best = &spans[i]
		}
	}
	if best == nil {
		return "", false
	}
	switch {
	case best.To == "":
		return to, true
	case to == "" || best.To < to:
		return best.To, true
	}
	return to, true
}

// joins reports whether a series continues from the span a into the span b.
func joins(a, b SymbolSpan) bool {
	return a.Kind.stitches() && a.To != "" && b.From > a.To
}

// ignoredSpans returns the unverified spans of the symbol or of the security.
func ignoredSpans(spans []SymbolSpan, symbol, security string) []SymbolSpan {
	var out []SymbolSpan
	for _, s := range spans {
		if !s.Verified && (SameTicker(s.Symbol, symbol) || (security != "" && s.Security == security)) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out
}

// IdentitySource is what LoadSeriesWith reads to know which tickers are one
// security.
type IdentitySource interface {
	// SymbolHistory returns the stored symbol spans; when asOf is not zero, as
	// held at that time.
	SymbolHistory(ctx context.Context, asOf time.Time) ([]SymbolSpan, error)
}
