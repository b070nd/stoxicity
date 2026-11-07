package fundamentals

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// FrameProbe is what one live frame answer was seen to contain. It records
// behaviour that was observed in one answer on one day; it is not a statement of
// what the SEC guarantees, and a field that is absent here is absent from this
// answer only.
type FrameProbe struct {
	URL       string
	Retrieved time.Time
	Bytes     int
	// Top are the keys of the answer object, Fields the keys that occur in the
	// records of data, with how many records carry each.
	Top    []string
	Fields map[string]int
	// Header values of the answer that describe the frame (taxonomy, tag, ccp,
	// uom, pts), as the SEC wrote them.
	Header  map[string]string
	Records int
	// Durations counts the day spans of start..end among the records of a
	// duration frame; empty for an instant frame.
	Durations map[int]int
	// Accessions is how many distinct accession numbers the records name and
	// BadAccessions how many do not look like NNNNNNNNNN-YY-NNNNNN.
	Accessions, BadAccessions int
	// DuplicateCIKs counts records whose CIK appears more than once.
	DuplicateCIKs int
	// Units are the distinct values of the units the answer names.
	Units []string
	// Sample are the first records, verbatim, for a fixture.
	Sample []json.RawMessage
}

// ProbeFrame fetches one frame through the client and describes the answer.
func ProbeFrame(ctx context.Context, c httpx.Client, url string, now time.Time, sample int) (FrameProbe, []byte, error) {
	resp, err := c.Do(ctx, httpx.Request{URL: url})
	if err != nil {
		return FrameProbe{}, nil, err
	}
	p, err := DescribeFrame(resp.Body, sample)
	p.URL, p.Retrieved = url, now.UTC()
	return p, resp.Body, err
}

// DescribeFrame reads the shape of a frame answer. It decodes generically, so
// that fields it does not know about are reported rather than dropped.
func DescribeFrame(body []byte, sample int) (FrameProbe, error) {
	p := FrameProbe{Bytes: len(body), Fields: map[string]int{}, Header: map[string]string{}, Durations: map[int]int{}}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return p, fmt.Errorf("fundamentals: read the frame: %w", err)
	}
	for k := range top {
		p.Top = append(p.Top, k)
	}
	slices.Sort(p.Top)
	for _, k := range []string{"taxonomy", "tag", "ccp", "uom", "label", "pts"} {
		if raw, ok := top[k]; ok {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				s = strings.TrimSpace(string(bytes.TrimSpace(raw)))
			}
			p.Header[k] = s
		}
	}
	var data []map[string]json.RawMessage
	if err := json.Unmarshal(top["data"], &data); err != nil {
		return p, fmt.Errorf("fundamentals: the frame has no readable data list: %w", err)
	}
	p.Records = len(data)
	accs, ciks, units := map[string]bool{}, map[string]int{}, map[string]bool{}
	for i, rec := range data {
		for k := range rec {
			p.Fields[k]++
		}
		var accn, start, end, cik string
		_ = json.Unmarshal(rec["accn"], &accn)
		_ = json.Unmarshal(rec["start"], &start)
		_ = json.Unmarshal(rec["end"], &end)
		cik = strings.Trim(string(rec["cik"]), `"`)
		if accn != "" {
			accs[accn] = true
			if !looksLikeAccession(accn) {
				p.BadAccessions++
			}
		}
		if start != "" {
			if d, ok := daysBetween(start, end); ok {
				p.Durations[d]++
			}
		}
		ciks[cik]++
		var u string
		if json.Unmarshal(rec["uom"], &u) == nil && u != "" {
			units[u] = true
		}
		if i < sample {
			b, _ := json.Marshal(rec)
			p.Sample = append(p.Sample, b)
		}
	}
	for _, n := range ciks {
		if n > 1 {
			p.DuplicateCIKs += n
		}
	}
	p.Accessions = len(accs)
	if u := p.Header["uom"]; u != "" {
		units[u] = true
	}
	for u := range units {
		p.Units = append(p.Units, u)
	}
	slices.Sort(p.Units)
	return p, nil
}

func looksLikeAccession(s string) bool {
	if len(s) != 20 || s[10] != '-' || s[13] != '-' {
		return false
	}
	for i, r := range s {
		if i == 10 || i == 13 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// DimensionFields are the record fields that would carry a dimensional (axis
// and member) qualifier if an answer had them. A frame is documented to hold one
// consolidated value per company; whether a given answer carries such a field is
// a thing to observe, not to assume.
var DimensionFields = []string{"dimensions", "dimension", "axis", "member", "segment", "segments"}

// DimensionalFields returns which of DimensionFields occur in the probe, with
// how many records carry them.
func (p FrameProbe) DimensionalFields() map[string]int {
	out := map[string]int{}
	for _, k := range DimensionFields {
		if n := p.Fields[k]; n > 0 {
			out[k] = n
		}
	}
	return out
}

// Fixture is a small public-data sample kept in the repository: a few records of
// a live answer, with where and when they were read.
type Fixture struct {
	SourceURL string            `json:"source_url"`
	Retrieved string            `json:"retrieved"` // YYYY-MM-DD
	Note      string            `json:"note"`
	Header    map[string]string `json:"header"`
	Data      []json.RawMessage `json:"data"`
}

// FixtureOf makes the fixture of the records that are kept: those of the given
// CIKs and the first sample ones.
func FixtureOf(p FrameProbe, body []byte, ciks []string, note string) (Fixture, error) {
	var top struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		return Fixture{}, err
	}
	f := Fixture{SourceURL: p.URL, Retrieved: p.Retrieved.Format(time.DateOnly), Note: note, Header: p.Header}
	want := map[string]bool{}
	for _, c := range ciks {
		want[strings.TrimLeft(c, "0")] = true
	}
	for i, raw := range top.Data {
		var r struct {
			CIK json.Number `json:"cik"`
		}
		_ = json.Unmarshal(raw, &r)
		if i < len(p.Sample) || want[r.CIK.String()] {
			f.Data = append(f.Data, raw)
		}
	}
	return f, nil
}
