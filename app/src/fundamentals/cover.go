package fundamentals

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// ClassShares is the shares outstanding of one class of an issuer's stock, as
// the cover page of a filing states them.
type ClassShares struct {
	// Dimensions names the class as the filing does ("us-gaap:StatementClassOfStockAxis=goog:CapitalClassAMember");
	// empty is the one total of an issuer with a single class.
	Dimensions string
	AsOf       string // YYYY-MM-DD, the date of the count
	Shares     float64
	// Ticker is the trading symbol the cover lists for the class, when it does.
	Ticker string
}

// ParseCover reads the cover page of an inline XBRL filing (a 10-K or 10-Q): the
// dei:EntityCommonStockSharesOutstanding facts, one per class, with the class
// each stands for and its trading symbol. A fact whose number cannot be read is
// an error, not a zero.
func ParseCover(doc []byte) ([]ClassShares, error) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }

	type ctx struct {
		instant string
		members []string
	}
	contexts := map[string]*ctx{}
	type shareFact struct {
		ref   string
		value float64
	}
	var shares []shareFact
	symbols := map[string]string{} // contextRef -> trading symbol

	var cur *ctx
	var dim string // explicit member being read
	type open struct {
		name, ref, scale, sign, format string
	}
	var stack []open
	var text strings.Builder
	var bad error
	local := func(n xml.Name) string { return n.Local }
	attr := func(se xml.StartElement, name string) string {
		for _, a := range se.Attr {
			if a.Name.Local == name {
				return a.Value
			}
		}
		return ""
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if len(shares) > 0 {
				break // the rest of a large document is not needed, and not always well formed
			}
			return nil, fmt.Errorf("fundamentals: read the cover page: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch local(t.Name) {
			case "context":
				cur = &ctx{}
				contexts[attr(t, "id")] = cur
			case "instant":
				text.Reset()
			case "explicitMember":
				dim = attr(t, "dimension")
				text.Reset()
			case "nonFraction", "nonNumeric":
				stack = append(stack, open{attr(t, "name"), attr(t, "contextRef"), attr(t, "scale"), attr(t, "sign"), attr(t, "format")})
				text.Reset()
			default:
				if len(stack) == 0 {
					text.Reset()
				}
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			switch local(t.Name) {
			case "instant":
				if cur != nil {
					cur.instant = strings.TrimSpace(text.String())
				}
			case "explicitMember":
				if cur != nil && dim != "" {
					cur.members = append(cur.members, dim+"="+strings.TrimSpace(text.String()))
				}
				dim = ""
			case "context":
				cur = nil
			case "nonFraction", "nonNumeric":
				if len(stack) == 0 {
					continue
				}
				o := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				raw := strings.TrimSpace(text.String())
				switch o.name {
				case "dei:EntityCommonStockSharesOutstanding":
					v, err := parseIXNumber(raw, o.scale, o.sign, o.format)
					if err != nil {
						bad = errors.Join(bad, fmt.Errorf("shares %q in context %s: %w", raw, o.ref, err))
						continue
					}
					shares = append(shares, shareFact{o.ref, v})
				case "dei:TradingSymbol":
					symbols[o.ref] = raw
				}
			}
		}
	}
	if bad != nil {
		return nil, fmt.Errorf("fundamentals: cover page: %w", bad)
	}
	var out []ClassShares
	for _, s := range shares {
		c := contexts[s.ref]
		if c == nil {
			return nil, fmt.Errorf("fundamentals: cover page: shares refer to a context %q the filing does not define", s.ref)
		}
		members := slices.Clone(c.members)
		slices.Sort(members)
		cs := ClassShares{Dimensions: strings.Join(members, ";"), AsOf: c.instant, Shares: s.value}
		// The symbol of the class: the cover lists its securities in contexts of
		// the same class member.
		for ref, sym := range symbols {
			if oc := contexts[ref]; oc != nil {
				om := slices.Clone(oc.members)
				slices.Sort(om)
				if strings.Join(om, ";") == cs.Dimensions && cs.Dimensions != "" {
					if cs.Ticker == "" || sym < cs.Ticker {
						cs.Ticker = sym
					}
				}
			}
		}
		out = append(out, cs)
	}
	slices.SortFunc(out, func(a, b ClassShares) int { return strings.Compare(a.Dimensions, b.Dimensions) })
	return out, nil
}

// parseIXNumber reads the value of an inline XBRL number: digits with the
// format's separators, multiplied by 10^scale. Formats other than plain numbers
// (words, dashes) are refused rather than guessed.
func parseIXNumber(raw, scale, sign, format string) (float64, error) {
	s := strings.TrimSpace(raw)
	// The registries spell the formats "numdotdecimal" (older) and
	// "num-dot-decimal" (current): the hyphens are not part of the meaning.
	switch f := strings.ReplaceAll(format[strings.LastIndex(format, ":")+1:], "-", ""); f {
	case "", "numdotdecimal":
		s = strings.ReplaceAll(s, ",", "")
	case "numcommadecimal":
		s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
	case "zerodash", "fixed-zero":
		s = "0"
	default:
		return 0, fmt.Errorf("number format %q is not read", format)
	}
	s = strings.ReplaceAll(s, " ", "")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if scale != "" {
		sc, err := strconv.Atoi(scale)
		if err != nil || sc < -12 || sc > 12 {
			return 0, fmt.Errorf("scale %q", scale)
		}
		for ; sc > 0; sc-- {
			v *= 10
		}
		for ; sc < 0; sc++ {
			v /= 10
		}
	}
	if sign == "-" {
		v = -v
	}
	return v, nil
}

// Capitalisation is what the share counts and prices say of a company's size.
// Issuer is the value of all the classes together and is only given when every
// class is priced; a class that has no price of its own (it is not listed, or
// the cover names no symbol for it) makes the issuer figure unknown rather than
// an estimate from another class's price.
type Capitalisation struct {
	Classes     []ClassCap
	Issuer      float64
	IssuerKnown bool
	Note        string
}

// ClassCap is the value of one class.
type ClassCap struct {
	ClassShares
	Price  float64
	Priced bool
	Cap    float64
}

// Capitalise values each class at the price of its symbol. An issuer with one
// class (empty dimensions) is valued at price(fallback), the ticker asked for.
func Capitalise(classes []ClassShares, fallback string, price func(ticker string) (float64, bool)) Capitalisation {
	var c Capitalisation
	if len(classes) == 0 {
		c.Note = "no share count on the cover page"
		return c
	}
	all := true
	for _, cl := range classes {
		cc := ClassCap{ClassShares: cl}
		sym := cl.Ticker
		if cl.Dimensions == "" {
			sym = fallback
		}
		if sym != "" {
			if p, ok := price(sym); ok && p > 0 {
				cc.Price, cc.Priced, cc.Cap = p, true, p*cl.Shares
			}
		}
		if cc.Priced {
			c.Issuer += cc.Cap
		} else {
			all = false
		}
		c.Classes = append(c.Classes, cc)
	}
	c.IssuerKnown = all
	if !all {
		c.Issuer = 0
		c.Note = "a class has no price, so the issuer's value is not known"
	}
	return c
}
