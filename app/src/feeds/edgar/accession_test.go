package edgar

import "testing"

func TestParseAccession(t *testing.T) {
	good := []struct{ in, want string }{
		{"0000320193-26-000026", "0000320193-26-000026"},
		{"000032019326000026", "0000320193-26-000026"},
		{"0000320193-99-999999", "0000320193-99-999999"},
	}
	for _, c := range good {
		got, err := ParseAccession(c.in)
		if err != nil {
			t.Errorf("ParseAccession(%q) error: %v", c.in, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("ParseAccession(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	bad := []string{
		"",
		"0000320193-26-00026",
		"000032019-26-000026",
		"0000320193_26_000026",
		" 0000320193-26-000026",
		"0000320193-26-000026 ",
		"00003201932600002",
		"0000320193-26-0000x6",
		"https://www.sec.gov/Archives/edgar/data/320193/000032019326000026",
	}
	for _, s := range bad {
		if got, err := ParseAccession(s); err == nil {
			t.Errorf("ParseAccession(%q) = %q, want error", s, got)
		}
	}
}

func TestAccessionParts(t *testing.T) {
	tests := []struct {
		a      Accession
		prefix string
		year   int
		seq    int
	}{
		{"0000320193-26-000026", "0000320193", 2026, 26},
		{"0000320193-99-999999", "0000320193", 2099, 999999},
		{"0000000001-00-000001", "0000000001", 2000, 1},
		{"not-an-accession", "", 0, 0},
	}
	for _, c := range tests {
		if got := c.a.Prefix(); got != c.prefix {
			t.Errorf("%q.Prefix() = %q, want %q", c.a, got, c.prefix)
		}
		if got := c.a.Year(); got != c.year {
			t.Errorf("%q.Year() = %d, want %d", c.a, got, c.year)
		}
		if got := c.a.Seq(); got != c.seq {
			t.Errorf("%q.Seq() = %d, want %d", c.a, got, c.seq)
		}
	}
}

func TestFilingIndexURL(t *testing.T) {
	const want = "https://www.sec.gov/Archives/edgar/data/320193/000032019326000026/0000320193-26-000026-index.htm"
	a := Accession("0000320193-26-000026")
	for _, cik := range []string{"0000320193", "320193"} {
		if got := FilingIndexURL(cik, a); got != want {
			t.Errorf("FilingIndexURL(%q, %q) = %q, want %q", cik, a, got, want)
		}
	}
}
