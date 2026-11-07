package fundamentals

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ArchivesURL is where the SEC keeps the documents of filings; it ends with a slash.
const ArchivesURL = "https://www.sec.gov/Archives/edgar/data/"

// Filing is one filing of a company, as the SEC's record of the company lists it.
type Filing struct {
	Accession string // with dashes
	Form      string
	Filed     string // YYYY-MM-DD
	Report    string // YYYY-MM-DD, the period the filing is for
	Document  string // the main document's file name
	// Accepted is the acceptance time as the SEC writes it (RFC 3339), empty
	// when the record does not give one.
	Accepted string
}

// Periodic reports whether the filing is an annual or quarterly report, which
// carry the cover page with the share counts.
func (f Filing) Periodic() bool {
	switch f.Form {
	case "10-K", "10-Q", "10-K/A", "10-Q/A", "10-KT", "10-QT":
		return true
	}
	return false
}

// DocumentURL is the address of the main document of the filing.
func (f Filing) DocumentURL(base, cik string) string {
	if base == "" {
		base = ArchivesURL
	}
	n, _ := strconv.ParseInt(cik, 10, 64)
	return fmt.Sprintf("%s%d/%s/%s", base, n, strings.ReplaceAll(f.Accession, "-", ""), f.Document)
}

// FileRef names an older part of a company's filing list, with the filing days
// it covers.
type FileRef struct{ Name, From, To string }

// Submissions is a company's record of its filings: the recent ones, and the
// names of the files that hold older ones.
type Submissions struct {
	Recent []Filing
	Files  []FileRef
}

// RecentFilings reads the filings the company's record lists as recent, newest
// first as the SEC gives them.
func RecentFilings(body []byte) ([]Filing, error) {
	s, err := ParseSubmissions(body)
	return s.Recent, err
}

// ParseSubmissions reads a company's record. The same parallel arrays hold the
// recent filings of the record itself and the older filings of its extra files.
func ParseSubmissions(body []byte) (Submissions, error) {
	type arrays struct {
		Accession []string `json:"accessionNumber"`
		Form      []string `json:"form"`
		Filed     []string `json:"filingDate"`
		Report    []string `json:"reportDate"`
		Document  []string `json:"primaryDocument"`
		Accepted  []string `json:"acceptanceDateTime"`
	}
	var ans struct {
		arrays
		Filings struct {
			Recent arrays `json:"recent"`
			Files  []struct {
				Name string `json:"name"`
				From string `json:"filingFrom"`
				To   string `json:"filingTo"`
			} `json:"files"`
		} `json:"filings"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return Submissions{}, fmt.Errorf("fundamentals: read the company's filings: %w", err)
	}
	r := ans.Filings.Recent
	if len(r.Accession) == 0 && len(ans.Accession) > 0 { // an extra file holds the arrays at the top
		r = ans.arrays
	}
	filings, err := fromArrays(r.Accession, r.Form, r.Filed, r.Report, r.Document, r.Accepted)
	if err != nil {
		return Submissions{}, err
	}
	out := Submissions{Recent: filings}
	for _, f := range ans.Filings.Files {
		out.Files = append(out.Files, FileRef{f.Name, f.From, f.To})
	}
	return out, nil
}

func fromArrays(acc, form, filed, report, doc, accepted []string) ([]Filing, error) {
	n := len(acc)
	if len(form) != n || len(filed) != n || len(report) != n || len(doc) != n || (len(accepted) != 0 && len(accepted) != n) {
		return nil, errors.New("fundamentals: the company's filing lists differ in length")
	}
	out := make([]Filing, n)
	for i := range out {
		out[i] = Filing{Accession: acc[i], Form: form[i], Filed: filed[i], Report: report[i], Document: doc[i]}
		if len(accepted) == n {
			out[i].Accepted = accepted[i]
		}
	}
	return out, nil
}

// AcceptedTime reads the acceptance time. ok is false when the record gives
// none or one that cannot be read; a time is never built from the filing day.
func (f Filing) AcceptedTime() (t time.Time, ok bool) {
	if f.Accepted == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, f.Accepted); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
