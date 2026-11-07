// Package valuation values a company's shares class by class.
//
// The share counts come from the cover pages of annual and quarterly reports,
// one count per class of stock, kept as observations with the filing they come
// from (package store). A class is tied to a security by a mapping that states
// how it was decided. A class is valued at its own raw price with its count
// brought to the same side of any stock split; the issuer is valued only when
// every class of the issuer can be valued that way. Whatever cannot be valued
// says why.
//
// Nothing here reads a database or the network: the same calculation serves the
// command line, the terminal screens and the exports.
package valuation

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Obs is one share count of one class, as a filing's cover page states it.
type Obs struct {
	CIK       string
	Accession string
	Form      string
	// Filed is the filing day (YYYY-MM-DD). Accepted is the acceptance time when
	// the SEC's record gives one; zero when only the day is known, and never
	// built from the day.
	Filed    string
	Accepted time.Time
	// AsOf is the date of the count (YYYY-MM-DD).
	AsOf string
	// Dimensions names the class as the filing does; empty is the one total of an
	// issuer with a single class.
	Dimensions string
	Shares     float64
	// Ticker is the trading symbol the cover lists for the class, when it does.
	Ticker string
	// Duplicates is how many identical facts of the filing this observation
	// stands for.
	Duplicates int
	// Received is when this system stored the observation.
	Received time.Time
}

// PublicAt is when the filing was public: its acceptance time, or when only the
// day is known the end of that day, which is the earliest moment at which it is
// certain to have been public.
func (o Obs) PublicAt() time.Time {
	if !o.Accepted.IsZero() {
		return o.Accepted
	}
	d, err := time.Parse(time.DateOnly, o.Filed)
	if err != nil {
		return time.Time{}
	}
	return d.Add(24 * time.Hour)
}

// Amendment reports whether the filing amends an earlier one.
func (o Obs) Amendment() bool { return strings.HasSuffix(o.Form, "/A") }

// Class is the count chosen for one class.
type Class struct {
	Obs
	// Conflict holds every different count the same filing states for the class
	// and date (including Shares) when there is more than one; no count is chosen
	// then.
	Conflict []float64
	// Revised says an earlier filing stated a different count for the same class
	// and date; Was is that count and WasAccession its filing.
	Revised      bool
	Was          float64
	WasAccession string
}

// Name is the class as a person reads it: the member of the class axis.
func (c Class) Name() string { return ClassName(c.Dimensions) }

// ClassName writes dimensions ("us-gaap:StatementClassOfStockAxis=goog:CapitalClassAMember")
// as a short label ("CapitalClassA"); empty dimensions are the single class.
func ClassName(dimensions string) string {
	if dimensions == "" {
		return "all shares"
	}
	var parts []string
	for _, d := range strings.Split(dimensions, ";") {
		if i := strings.LastIndex(d, "="); i >= 0 {
			d = d[i+1:]
		}
		if i := strings.LastIndex(d, ":"); i >= 0 {
			d = d[i+1:]
		}
		parts = append(parts, strings.TrimSuffix(d, "Member"))
	}
	return strings.Join(parts, " / ")
}

// Pick is the filing whose counts are used and the classes it states.
type Pick struct {
	CIK       string
	Accession string
	Form      string
	Filed     string
	Classes   []Class
	// Dropped are classes that the filing before this one stated and this one
	// does not, so that the issuer's whole count cannot be known.
	Dropped []string
}

// Select chooses, from the observations of one issuer, the counts to use as
// known at the time (zero: all). An observation counts when it had been stored
// and its filing was public by then. The filing with the newest count date wins,
// then the later one (an amendment replaces what it amends); within it the
// newest count date of each class. A filing that states a class twice with
// different counts is a conflict and gives that class no count. ok is false
// when nothing was known.
func Select(obs []Obs, known time.Time) (Pick, bool) {
	var seen []Obs
	for _, o := range obs {
		if !known.IsZero() && (o.Received.After(known) || o.PublicAt().After(known) || o.PublicAt().IsZero()) {
			continue
		}
		seen = append(seen, o)
	}
	if len(seen) == 0 {
		return Pick{}, false
	}
	byFiling := map[string][]Obs{}
	for _, o := range seen {
		byFiling[o.Accession] = append(byFiling[o.Accession], o)
	}
	type filing struct {
		acc, asOf, filed string
	}
	var fs []filing
	for acc, os := range byFiling {
		f := filing{acc: acc, filed: os[0].Filed}
		for _, o := range os {
			if o.AsOf > f.asOf {
				f.asOf = o.AsOf
			}
		}
		fs = append(fs, f)
	}
	slices.SortFunc(fs, func(a, b filing) int {
		switch {
		case a.asOf != b.asOf:
			return strings.Compare(b.asOf, a.asOf)
		case a.filed != b.filed:
			return strings.Compare(b.filed, a.filed)
		}
		return strings.Compare(b.acc, a.acc)
	})
	best := byFiling[fs[0].acc]
	p := Pick{CIK: best[0].CIK, Accession: fs[0].acc, Form: best[0].Form, Filed: best[0].Filed}

	// The newest count date of each class, within the filing.
	newest := map[string]string{}
	for _, o := range best {
		if o.AsOf > newest[o.Dimensions] {
			newest[o.Dimensions] = o.AsOf
		}
	}
	dims := make([]string, 0, len(newest))
	for d := range newest {
		dims = append(dims, d)
	}
	slices.Sort(dims)
	for _, d := range dims {
		var c Class
		var counts []float64
		for _, o := range best {
			if o.Dimensions != d || o.AsOf != newest[d] {
				continue
			}
			if len(counts) == 0 {
				c.Obs = o
			}
			if !slices.Contains(counts, o.Shares) {
				counts = append(counts, o.Shares)
			}
			if o.Ticker != "" && c.Ticker == "" {
				c.Ticker = o.Ticker
			}
		}
		if len(counts) > 1 {
			slices.Sort(counts)
			c.Conflict = counts
		}
		// An earlier filing with another count for the same class and date.
		for _, fl := range fs[1:] {
			for _, o := range byFiling[fl.acc] {
				if o.Dimensions == d && o.AsOf == newest[d] && o.Shares != c.Shares && len(counts) == 1 {
					c.Revised, c.Was, c.WasAccession = true, o.Shares, o.Accession
					break
				}
			}
			if c.Revised {
				break
			}
		}
		p.Classes = append(p.Classes, c)
	}
	// Classes the filing before this one stated and this one does not.
	if len(fs) > 1 {
		for _, o := range byFiling[fs[1].acc] {
			if _, ok := newest[o.Dimensions]; !ok && !slices.Contains(p.Dropped, o.Dimensions) {
				p.Dropped = append(p.Dropped, o.Dimensions)
			}
		}
		slices.Sort(p.Dropped)
	}
	return p, true
}

// Issue describes what is wrong with a class's count, empty when nothing is.
func (c Class) Issue() string {
	if len(c.Conflict) > 1 {
		parts := make([]string, len(c.Conflict))
		for i, v := range c.Conflict {
			parts[i] = fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("the filing states more than one count for %s on %s (%s)", c.Name(), c.AsOf, strings.Join(parts, ", "))
	}
	return ""
}
