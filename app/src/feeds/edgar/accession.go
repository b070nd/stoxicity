package edgar

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Accession is an EDGAR accession number in its canonical dashed form, for
// example 0000320193-26-000026: ten digits, two digits, six digits. It is
// Article.ID for filings and the dedupe key between the live feed, the
// submissions files and the daily index.
//
// The first ten digits are the id of whoever submitted the filing. That is
// often the company, but a filing agent or a law firm can submit for a company,
// so Prefix must not be used as the company's CIK.
type Accession string

var (
	dashedAccession = regexp.MustCompile(`^(\d{10})-(\d{2})-(\d{6})$`)
	plainAccession  = regexp.MustCompile(`^(\d{10})(\d{2})(\d{6})$`)
)

// ParseAccession accepts an accession number in the dashed form
// 0000320193-26-000026 or the 18-digit form 000032019326000026, and returns the
// dashed form. Anything else is an error; the input is not trimmed.
func ParseAccession(s string) (Accession, error) {
	m := dashedAccession.FindStringSubmatch(s)
	if m == nil {
		m = plainAccession.FindStringSubmatch(s)
	}
	if m == nil {
		return "", fmt.Errorf("edgar: invalid accession number %q", s)
	}
	return Accession(m[1] + "-" + m[2] + "-" + m[3]), nil
}

// Prefix is the first ten digits, the id of the submitter (see Accession). It
// is empty if a is not a valid accession number.
func (a Accession) Prefix() string {
	if !a.valid() {
		return ""
	}
	return string(a[:10])
}

// Year is the filing year: 2000 plus the two-digit year in the middle field.
// It is 0 if a is not a valid accession number.
func (a Accession) Year() int {
	if !a.valid() {
		return 0
	}
	yy, _ := strconv.Atoi(string(a[11:13]))
	return 2000 + yy
}

// Seq is the sequence number in the last field, read as an integer. It is 0 if
// a is not a valid accession number.
func (a Accession) Seq() int {
	if !a.valid() {
		return 0
	}
	n, _ := strconv.Atoi(string(a[14:]))
	return n
}

// valid reports whether a has the canonical dashed form. It is used by the
// methods so that a hand-made Accession cannot cause a panic.
func (a Accession) valid() bool { return dashedAccession.MatchString(string(a)) }

// FilingIndexURL is the EDGAR index page for one filing of company cik, which
// is written with or without leading zeros. The URL pattern was written from
// memory and has not been verified against the live site.
func FilingIndexURL(cik string, a Accession) string {
	return "https://www.sec.gov/Archives/edgar/data/" + strings.TrimLeft(cik, "0") +
		"/" + strings.ReplaceAll(string(a), "-", "") + "/" + string(a) + "-index.htm"
}
