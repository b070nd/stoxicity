package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/b070nd/stoxicity/app/src/fundamentals"
)

// fundamentalsProbe reads one annual and one instant frame of the SEC through
// the ordinary HTTP client (same contact identification and rate limit), says
// what the answers were seen to contain, compares one company's records with
// that company's own history, and can keep a small fixture of the records.
// It writes nothing to the database.
func (a *app) fundamentalsProbe(ctx context.Context, args []string) error {
	fs := newFlags("universe fundamentals probe")
	annual := fs.String("annual", "us-gaap/RevenueFromContractWithCustomerExcludingAssessedTax/USD/CY2024", "taxonomy/concept/unit/period of a duration frame")
	instant := fs.String("instant", "us-gaap/Assets/USD/CY2024Q4I", "taxonomy/concept/unit/period of an instant frame")
	out := fs.String("out", "", "folder to write the fixtures to (default: none written)")
	sample := fs.Int("sample", 3, "records kept in a fixture besides the compared company's")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("probe takes one ticker to compare against")
	}
	p, ok := a.universe().Get(rest[0])
	if !ok || p.CIK == "" {
		return fmt.Errorf("%s has no SEC number in the company list", strings.ToUpper(rest[0]))
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	f := a.fundamentalsFetcher(client)
	base := f.URL
	if base == "" {
		base = fundamentals.URL
	}
	hist, err := f.History(ctx, p.CIK)
	if err != nil {
		return fmt.Errorf("history of %s: %w", p.Ticker, err)
	}
	for _, spec := range []struct{ label, path string }{{"duration (annual)", *annual}, {"instant", *instant}} {
		parts := strings.Split(spec.path, "/")
		if len(parts) != 4 {
			return usageErrorf("%q is not taxonomy/concept/unit/period", spec.path)
		}
		url := base + spec.path + ".json"
		probe, body, err := fundamentals.ProbeFrame(ctx, client, url, a.now(), *sample)
		if err != nil {
			return fmt.Errorf("%s: %w", spec.label, err)
		}
		a.printProbe(spec.label, probe)
		a.compareProbe(body, p.CIK, parts[1], hist)
		if *out != "" {
			fx, err := fundamentals.FixtureOf(probe, body, []string{p.CIK}, "records of one live answer, read-only; the sample is not exhaustive and shows nothing about records that were not kept")
			if err != nil {
				return err
			}
			b, _ := json.MarshalIndent(fx, "", "  ")
			path := filepath.Join(*out, strings.ReplaceAll(spec.path, "/", "_")+".json")
			if err := os.MkdirAll(*out, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "fixture: %s (%d records)\n", path, len(fx.Data))
		}
	}
	return nil
}

func (a *app) printProbe(label string, p fundamentals.FrameProbe) {
	fmt.Fprintf(a.stdout, "\n%s frame\n  url:        %s\n  retrieved:  %s\n  size:       %d bytes, %d records\n  answer keys: %s\n",
		label, p.URL, p.Retrieved.Format("2006-01-02 15:04 UTC"), p.Bytes, p.Records, strings.Join(p.Top, ", "))
	var hdr []string
	for _, k := range slices.Sorted(maps.Keys(p.Header)) {
		hdr = append(hdr, k+"="+p.Header[k])
	}
	fmt.Fprintf(a.stdout, "  header:     %s\n", strings.Join(hdr, " "))
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprint(tw, "  record fields (records carrying each):")
	for _, k := range slices.Sorted(maps.Keys(p.Fields)) {
		fmt.Fprintf(tw, " %s=%d", k, p.Fields[k])
	}
	fmt.Fprintln(tw)
	tw.Flush()
	if len(p.Durations) > 0 {
		var spans []string
		for _, d := range slices.Sorted(maps.Keys(p.Durations)) {
			spans = append(spans, fmt.Sprintf("%dd x%d", d, p.Durations[d]))
		}
		fmt.Fprintf(a.stdout, "  duration spans seen: %s\n", strings.Join(spans, ", "))
	}
	fmt.Fprintf(a.stdout, "  units named: %s; distinct accessions: %d (%d not in the NNNNNNNNNN-YY-NNNNNN form); records sharing a CIK with another: %d\n",
		strings.Join(p.Units, ", "), p.Accessions, p.BadAccessions, p.DuplicateCIKs)
	if d := p.DimensionalFields(); len(d) == 0 {
		fmt.Fprintln(a.stdout, "  dimensional fields: none of "+strings.Join(fundamentals.DimensionFields, ", ")+" occurs in this answer (observed here only; not a guarantee for other frames)")
	} else {
		fmt.Fprintf(a.stdout, "  dimensional fields present: %v\n", d)
	}
}

// compareProbe finds the company's record in the frame and the facts of the
// company's own history that agree with it on period end and value.
func (a *app) compareProbe(body []byte, cik, concept string, hist []fundamentals.Fact) {
	var ans struct {
		Data []struct {
			CIK   int64   `json:"cik"`
			Start string  `json:"start"`
			End   string  `json:"end"`
			Val   float64 `json:"val"`
			Accn  string  `json:"accn"`
			Name  string  `json:"entityName"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &ans) != nil {
		return
	}
	for _, d := range ans.Data {
		if fmt.Sprintf("%010d", d.CIK) != cik {
			continue
		}
		fmt.Fprintf(a.stdout, "  %s in the frame: accn %s, period %s..%s, value %.0f\n", cik, d.Accn, d.Start, d.End, d.Val)
		var match, sameEndOnly []fundamentals.Fact
		for _, h := range hist {
			if h.Concept != concept || h.End != d.End {
				continue
			}
			if h.Value == d.Val && h.Accession == d.Accn {
				match = append(match, h)
			} else {
				sameEndOnly = append(sameEndOnly, h)
			}
		}
		for _, h := range match {
			fmt.Fprintf(a.stdout, "    matches the company's own history: %s %s filed %s, dimensions %q\n", h.Form, h.Accession, orUnknown(h.Filed), h.Dimensions)
		}
		if len(match) == 0 {
			fmt.Fprintf(a.stdout, "    no record of the company's history agrees on concept %s, accession, period end and value (%d records share the period end)\n", concept, len(sameEndOnly))
		}
		return
	}
	fmt.Fprintf(a.stdout, "  %s does not occur in this frame\n", cik)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
