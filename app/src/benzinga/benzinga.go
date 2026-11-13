// Package benzinga reads Benzinga's analyst ratings, which Massive sells as a
// separate add-on, and turns them into the rows the analyst-revision importer
// (eventfact.FromAnalystRows) reads. It makes no judgement of its own: a rating
// or target change becomes a row, every other kind of record is counted and
// left out, and the importer decides what is valid.
package benzinga

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// VendorPrefix starts the source_ref of every row, so that the events made from
// Benzinga's data can be found by their evidence later (the licence asks for the
// stored data to be deleted when the add-on ends).
const VendorPrefix = "benzinga:"

// Rating is one record of Benzinga's ratings endpoint, with the fields the
// conversion reads. Prices are kept as the numbers Benzinga wrote, and are the
// figures as issued: the split-adjusted ones are never read.
type Rating struct {
	ID                string      `json:"benzinga_id"`
	Firm              string      `json:"firm"`
	Ticker            string      `json:"ticker"`
	Currency          string      `json:"currency"`
	Rating            string      `json:"rating"`
	PreviousRating    string      `json:"previous_rating"`
	RatingAction      string      `json:"rating_action"`
	PriceTarget       json.Number `json:"price_target"`
	PreviousTarget    json.Number `json:"previous_price_target"`
	PriceTargetAction string      `json:"price_target_action"`
	// Date is YYYY-MM-DD and Time is HH:MM:SS, in a zone the caller names: the
	// documentation says UTC, but the vendor's own samples fit New York time.
	Date string `json:"date"`
	Time string `json:"time"`
	// LastUpdated is when Benzinga last changed the record; it can move, so it is
	// never used as a time of publication.
	LastUpdated string `json:"last_updated"`
}

// Convert turns one record into the rows of the analyst importer: one for a
// rating upgrade or downgrade, one for a price target raised or lowered, two for
// a record that has both (they repeat source_ref, which is Benzinga's record
// number with VendorPrefix). It returns no rows and a reason for a record that
// is neither, such as a reiterated rating or a new coverage, and for one whose
// date or time cannot be read.
//
// published_at is Date and Time read in zone, with the zone's offset; a record
// without a time is a day (precision day). available_at is left out, so it is
// published_at. held_at is heldAt, the time this program fetched the record, so
// the importer's own rule decides between known and reconstructed. A missing
// currency or a missing figure is passed on as it is and the importer rejects
// the row; nothing is guessed. The symbol is spelled as the reference tickers
// spell it (BRK-B where Benzinga writes BRK.B).
func Convert(r Rating, zone *time.Location, heldAt time.Time) (rows []map[string]string, reason string) {
	if _, err := time.Parse(time.DateOnly, r.Date); err != nil {
		return nil, "date is not YYYY-MM-DD"
	}
	published := r.Date
	if r.Time != "" {
		t, err := time.ParseInLocation(time.DateTime, r.Date+" "+r.Time, zone)
		if err != nil {
			return nil, "time is not HH:MM:SS"
		}
		published = t.Format(time.RFC3339)
	}
	ref := ""
	if r.ID != "" {
		ref = VendorPrefix + r.ID
	}
	base := map[string]string{
		"firm":         r.Firm,
		"symbol":       strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(r.Ticker)), ".", "-"),
		"published_at": published,
		"held_at":      heldAt.UTC().Format(time.RFC3339),
		"source_ref":   ref,
		"currency":     r.Currency,
	}
	row := func(extra map[string]string) {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		rows = append(rows, m)
	}

	ratingAction := strings.ToLower(strings.TrimSpace(r.RatingAction))
	if ratingAction == "upgrades" || ratingAction == "downgrades" {
		row(map[string]string{
			"kind":            strings.TrimSuffix(ratingAction, "s"),
			"previous_rating": r.PreviousRating,
			"new_rating":      r.Rating,
		})
	}
	targetAction := strings.ToLower(strings.TrimSpace(r.PriceTargetAction))
	if targetAction == "raises" || targetAction == "lowers" {
		kind := "target_raised"
		if targetAction == "lowers" {
			kind = "target_cut"
		}
		row(map[string]string{
			"kind":            kind,
			"previous_target": r.PreviousTarget.String(),
			"new_target":      r.PriceTarget.String(),
		})
	}
	if len(rows) > 0 {
		return rows, ""
	}
	var seen []string
	if ratingAction != "" {
		seen = append(seen, "rating "+ratingAction)
	}
	if targetAction != "" {
		seen = append(seen, "target "+targetAction)
	}
	if len(seen) == 0 {
		seen = append(seen, "no action stated")
	}
	return nil, fmt.Sprintf("not a rating or target change (%s)", strings.Join(seen, ", "))
}
