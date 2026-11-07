package fundamentals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures are small samples of live, public SEC frame answers, read once with
// the existing HTTP client. These tests state what the samples show; they do not
// state what the SEC guarantees. A field that is absent from a sample is absent
// from that sample only.

func loadFixture(t *testing.T, name string) (Fixture, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"taxonomy": f.Header["taxonomy"], "tag": f.Header["tag"], "ccp": f.Header["ccp"], "uom": f.Header["uom"], "label": f.Header["label"], "data": f.Data,
	})
	return f, body
}

func TestFrameFixturesKeepWhereAndWhenTheyWereRead(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.json")
	if len(files) < 3 {
		t.Fatalf("the annual duration frames and the instant frame are kept: %v", files)
	}
	for _, f := range files {
		fx, _ := loadFixture(t, filepath.Base(f))
		if !strings.HasPrefix(fx.SourceURL, "https://data.sec.gov/api/xbrl/frames/") {
			t.Errorf("%s: source %q", f, fx.SourceURL)
		}
		if _, err := time.Parse(time.DateOnly, fx.Retrieved); err != nil {
			t.Errorf("%s: retrieval date %q", f, fx.Retrieved)
		}
		if fx.Note == "" || len(fx.Data) == 0 {
			t.Errorf("%s: a fixture says what it is and holds records", f)
		}
	}
}

func TestDurationFrameSampleShowsStartAndEndAndNoDimensionFields(t *testing.T) {
	fx, body := loadFixture(t, "us-gaap_Revenues_USD_CY2024.json")
	p, err := DescribeFrame(body, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"accn", "cik", "end", "start", "val"} {
		if p.Fields[k] != p.Records {
			t.Errorf("in this sample every record has %q: %d of %d", k, p.Fields[k], p.Records)
		}
	}
	if got := p.DimensionalFields(); len(got) != 0 {
		t.Errorf("no dimensional field was seen in this sample (which says nothing of other answers): %v", got)
	}
	if p.BadAccessions != 0 || p.Accessions == 0 {
		t.Errorf("accessions in this sample: %d, malformed %d", p.Accessions, p.BadAccessions)
	}
	if len(p.Durations) == 0 {
		t.Error("an annual frame's records span a period")
	}
	for d := range p.Durations {
		if d < 300 || d > 400 {
			t.Errorf("a calendar-year frame holds a period of %d days in this sample", d)
		}
	}
	if len(p.Units) != 1 || p.Units[0] != "USD" || fx.Header["ccp"] != "CY2024" {
		t.Errorf("units %v header %v", p.Units, fx.Header)
	}
}

func TestInstantFrameSampleHasNoStartDate(t *testing.T) {
	_, body := loadFixture(t, "us-gaap_Assets_USD_CY2024Q4I.json")
	p, err := DescribeFrame(body, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p.Fields["start"] != 0 || p.Fields["end"] != p.Records || p.Fields["val"] != p.Records {
		t.Errorf("an instant is a single date: %v", p.Fields)
	}
	if len(p.Durations) != 0 || len(p.Sample) != 2 {
		t.Errorf("durations %v sample %d", p.Durations, len(p.Sample))
	}
}

func TestDescribeFrameReportsFieldsItDoesNotKnowAndRefusesWhatItCannotRead(t *testing.T) {
	body := `{"taxonomy":"us-gaap","tag":"X","ccp":"CY2024","uom":"USD","data":[{"accn":"0001-2","cik":1,"end":"2024-12-31","start":"2024-01-01","val":1,"segment":"A"},{"accn":"0000000001-25-000002","cik":1,"end":"2024-12-31","start":"2024-01-01","val":2}]}`
	p, err := DescribeFrame([]byte(body), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.DimensionalFields(); got["segment"] != 1 {
		t.Errorf("an unexpected dimensional field is reported, not dropped: %v", got)
	}
	if p.BadAccessions != 1 || p.DuplicateCIKs != 2 {
		t.Errorf("malformed accession %d, duplicate CIK records %d", p.BadAccessions, p.DuplicateCIKs)
	}
	if _, err := DescribeFrame([]byte(`{"data": 5}`), 1); err == nil {
		t.Error("a frame without a list of records is an error")
	}
	if _, err := DescribeFrame([]byte(`not json`), 1); err == nil {
		t.Error("not JSON is an error")
	}
}

func TestFixtureOfKeepsTheFirstRecordsAndTheWantedCompaniesWithTheirSource(t *testing.T) {
	_, body := loadFixture(t, "us-gaap_Revenues_USD_CY2024.json")
	p, _ := DescribeFrame(body, 1)
	p.URL, p.Retrieved = "https://data.sec.gov/api/xbrl/frames/us-gaap/Revenues/USD/CY2024.json", time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	fx, err := FixtureOf(p, body, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if fx.SourceURL != p.URL || fx.Retrieved != "2026-11-07" || len(fx.Data) != 1 {
		t.Errorf("%+v", fx)
	}
}
