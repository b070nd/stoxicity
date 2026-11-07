package fundamentals

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

// RecentFilings reads the filings the company's record lists as recent, newest
// first as the SEC gives them.
func RecentFilings(body []byte) ([]Filing, error) {
	var ans struct {
		Filings struct {
			Recent struct {
				Accession []string `json:"accessionNumber"`
				Form      []string `json:"form"`
				Filed     []string `json:"filingDate"`
				Report    []string `json:"reportDate"`
				Document  []string `json:"primaryDocument"`
			} `json:"recent"`
		} `json:"filings"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, fmt.Errorf("fundamentals: read the company's filings: %w", err)
	}
	r := ans.Filings.Recent
	n := len(r.Accession)
	if len(r.Form) != n || len(r.Filed) != n || len(r.Report) != n || len(r.Document) != n {
		return nil, errors.New("fundamentals: the company's filing lists differ in length")
	}
	out := make([]Filing, n)
	for i := range out {
		out[i] = Filing{r.Accession[i], r.Form[i], r.Filed[i], r.Report[i], r.Document[i]}
	}
	return out, nil
}
