package valuation

import (
	"time"

	"github.com/b070nd/stoxicity/app/src/exchange"
)

// Mode says what a valuation as of a past time claims to be.
type Mode string

const (
	// ModeCurrent is today's valuation from everything stored.
	ModeCurrent Mode = ""
	// ModeKnown is what this installation held at the time: only counts, mappings,
	// prices and splits it had recorded by then, each as it was then. A candle
	// whose fetch time was never recorded cannot be shown to have been held and is
	// left out.
	ModeKnown Mode = "known"
	// ModeReconstructed is what the record says was true at the time, using
	// everything stored now: filings public by then, sessions closed by then, splits
	// executed by then, whenever this installation found them. Mappings are today's
	// (a mapping is this system's judgement and is not backdated). It is not what the
	// installation would have shown then.
	ModeReconstructed Mode = "reconstructed"
)

// View is the time a valuation is read as of and what that means.
type View struct {
	Mode Mode
	At   time.Time
}

// Historical says the view is of a past time.
func (v View) Historical() bool { return v.Mode != ModeCurrent && !v.At.IsZero() }

// Ref is the time the ages of the inputs are measured against: the view's time
// for a historical view, otherwise now.
func (v View) Ref(now time.Time) time.Time {
	if v.Historical() {
		return v.At
	}
	return now
}

// Caveat says in words what the view is, empty for the current one.
func (v View) Caveat() string {
	switch {
	case !v.Historical():
		return ""
	case v.Mode == ModeKnown:
		return "as this installation had recorded it by " + v.At.UTC().Format(time.RFC3339) + ": counts, mappings, prices and splits it had stored by then; later filings, mappings, prices and splits are excluded"
	default:
		return "reconstructed for " + v.At.UTC().Format(time.RFC3339) + " from what is stored now: filings public, sessions closed and splits executed by then, whenever they were found; mappings are today's; this is not what the installation showed then"
	}
}

// visible says whether the count was part of the view.
func (v View) visible(o Obs) bool {
	if !v.Historical() {
		return true
	}
	pub := o.PublicAt()
	if pub.IsZero() || pub.After(v.At) {
		return false
	}
	return v.Mode != ModeKnown || !o.Received.After(v.At)
}

// QuoteCurrency says in which currency the price of a security is quoted and how
// that is known. stated is what the price source says (empty when it does not),
// venue the exchange the security is listed on (the universe's word). It never
// assumes: with neither, the currency is unknown (cur is empty) and problem says
// so; a source and a venue that disagree are a problem too.
func QuoteCurrency(stated, venue string) (cur, basis, problem string) {
	var vcur string
	var vname string
	if i, ok := exchange.Lookup(venue); ok && i.Currency != "" {
		vcur, vname = i.Currency, i.Name
	}
	switch {
	case stated != "" && vcur != "" && stated != vcur:
		return "", "", "the price source states " + stated + " and the listing venue " + vname + " quotes in " + vcur
	case stated != "":
		return stated, "stated by the price source", ""
	case vcur != "":
		return vcur, "the listing venue " + vname + " quotes in " + vcur + "; the stored price does not state its currency", ""
	}
	return "", "", "the stored price does not state its currency and the listing venue is not known"
}
