// Package gold is the labelled test set that must exist before any language
// model is allowed to read figures from news, and the scorer that measures the
// existing rules against it.
//
// A gold file is JSON lines: a header line, then one line per item. An item is
// the text of one stored event (the titles and summaries of its evidence, as
// eventfact.EventText holds them) with the records a person says the text
// states: an event type, or "none" when the text states no economic event, and
// for each record its figures, each with the verbatim words it was read from.
//
// The rules of the discipline, which the code enforces where a file can:
//
//   - a draw is reproducible: the seed, the window and the drawing rule are in
//     the header, and an event is never drawn twice, not even into a later file
//     when the earlier ones are named;
//   - a figure's span is checked against the text, so a label cannot cite words
//     that are not there, and a type's figures are checked against the event
//     schema of package eventfact;
//   - the labelled items are split once, by a hash of a seed and the item ID,
//     into a development part, on which rules may be tuned, and a held-out test
//     part. The split is written to files that are never overwritten, the part
//     an item belongs to is checked against the recorded rule, and the test part
//     is scored only when the caller says it is the final reading, and then
//     reports counts and not which items failed.
//
// The package is pure: it reads what the caller supplies and returns results.
// The one reader of the database is Candidates, through a small interface.
package gold

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Version names the file format and the drawing, splitting and scoring rules.
const Version = "gold-1"

// None is the type of a record that says the text states no economic event.
const None = "none"

// Roles of a file that came out of a split.
const (
	RoleDev  = "dev"  // rules may be tuned on it
	RoleTest = "test" // held out: read once, at the end
)

// Passage is one piece of an item's text: a title or a summary, with the
// article it comes from.
type Passage struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
}

// Fact is a figure a record states. Span is the verbatim words it was read from.
// Value is a pointer so that a figure whose value was left out is noticed.
type Fact struct {
	Name  string   `json:"name"`
	Value *float64 `json:"value"`
	// Unit is the currency code for money ("USD"), "USD/share" for a price, or
	// the unit of the field ("shares", "ratio", "years", "count").
	Unit string `json:"unit"`
	Span string `json:"span"`
}

// Record is one economic event the text states: its type, its figures and its
// labels (the words the type keeps instead of a figure, such as "certainty").
// A record of type None has neither.
type Record struct {
	Type   string            `json:"type"`
	Facts  []Fact            `json:"facts,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Item is one event to be labelled, or already labelled.
type Item struct {
	// ID is the ID of the stored event.
	ID        string    `json:"id"`
	Symbol    string    `json:"symbol"`
	Kind      string    `json:"kind"`     // the stored event kind the rules dispatch on
	Category  string    `json:"category"` // the category of that kind, the stratum of the draw
	Published time.Time `json:"published"`
	Text      []Passage `json:"text"`
	// Labeller names who labelled the item; empty while it is not labelled.
	Labeller string `json:"labeller"`
	// Records are what the text states; empty while the item is not labelled.
	Records []Record `json:"records"`
}

// Labelled says a person has given the item's records.
func (it Item) Labelled() bool { return len(it.Records) > 0 }

// EventText is the item as the extractors read an event. The times and the
// source are fixed by the item, so that the same item always reads the same.
func (it Item) EventText() eventfact.EventText {
	refs := make([]string, 0, len(it.Text))
	passages := make([]eventfact.Passage, 0, len(it.Text))
	seen := map[string]bool{}
	for _, p := range it.Text {
		passages = append(passages, eventfact.Passage{Ref: p.Ref, Text: p.Text})
		if p.Ref != "" && !seen[p.Ref] {
			seen[p.Ref] = true
			refs = append(refs, p.Ref)
		}
	}
	ref := it.ID
	if len(refs) > 0 {
		ref = refs[0]
	}
	return eventfact.EventText{
		EventID:     it.ID,
		Symbol:      it.Symbol,
		Kind:        model.EventKind(it.Kind),
		PublishedAt: it.Published,
		AvailableAt: it.Published,
		Precision:   eventfact.PrecisionTime,
		Source:      eventfact.SourceMedia,
		SourceRef:   ref,
		Evidence:    refs,
		Basis:       eventfact.Reconstructed,
		Passages:    passages,
	}
}

// Drawn records how a file's items were drawn, so that the draw can be
// repeated and its population is known.
type Drawn struct {
	Seed     int64     `json:"seed"`
	N        int       `json:"n"`                  // how many were asked for
	Category string    `json:"category,omitempty"` // empty: all categories
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Eligible int       `json:"eligible"` // events the draw chose from
	Excluded int       `json:"excluded"` // events left out as already drawn
	Rule     string    `json:"rule"`
}

// SplitRule records how a labelled file was split.
type SplitRule struct {
	Seed        int64   `json:"seed"`
	DevFraction float64 `json:"dev_fraction"`
	// Population is a digest of the IDs of every item that was split, so that
	// the two parts can be shown to come from one labelled file.
	Population string `json:"population"`
	Items      int    `json:"items"` // how many items were split
	Rule       string `json:"rule"`
}

// Header is the first line of a gold file.
type Header struct {
	Format string `json:"format"`
	// Role is RoleDev or RoleTest for a file made by Split; empty otherwise.
	Role  string     `json:"role,omitempty"`
	Drawn *Drawn     `json:"drawn,omitempty"`
	Split *SplitRule `json:"split,omitempty"`
}

// File is a gold file.
type File struct {
	Header Header
	Items  []Item
}

// Read parses a gold file. It rejects a first line that is not a header of this
// version, fields it does not know, and a line that is not JSON.
func Read(r io.Reader) (File, error) {
	var f File
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	line, headed := 0, false
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if !headed {
			if err := dec.Decode(&f.Header); err != nil {
				return File{}, fmt.Errorf("gold: line %d: the header: %w", line, err)
			}
			if f.Header.Format != Version {
				return File{}, fmt.Errorf("gold: line %d: format is %q, not %q", line, f.Header.Format, Version)
			}
			headed = true
			continue
		}
		var it Item
		if err := dec.Decode(&it); err != nil {
			return File{}, fmt.Errorf("gold: line %d: %w", line, err)
		}
		if dec.More() {
			return File{}, fmt.Errorf("gold: line %d: more than one item on a line", line)
		}
		f.Items = append(f.Items, it)
	}
	if err := sc.Err(); err != nil {
		return File{}, fmt.Errorf("gold: reading: %w", err)
	}
	if !headed {
		return File{}, errors.New("gold: the file is empty: it has no header line")
	}
	return f, nil
}

// Write writes the file as JSON lines, one item to a line, with the words kept
// as they are (no escaping of <, > and &) so that a person can edit them.
func Write(w io.Writer, f File) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	h := f.Header
	h.Format = Version
	if err := enc.Encode(h); err != nil {
		return fmt.Errorf("gold: writing: %w", err)
	}
	for _, it := range f.Items {
		if it.Records == nil {
			it.Records = []Record{}
		}
		if err := enc.Encode(it); err != nil {
			return fmt.Errorf("gold: writing: %w", err)
		}
	}
	return nil
}
