package fundamentals

import "testing"

func TestParseSubmissionsKeepsItems(t *testing.T) {
	body := []byte(`{"filings":{"recent":{
		"accessionNumber":["0000000001-26-000001","0000000001-26-000002"],
		"form":["8-K","10-Q"],
		"filingDate":["2026-08-03","2026-08-04"],
		"reportDate":["2026-08-03","2026-06-30"],
		"primaryDocument":["a.htm","b.htm"],
		"acceptanceDateTime":["2026-08-03T16:05:00.000Z","2026-08-04T12:00:00.000Z"],
		"items":["2.02,9.01",""]}}}`)
	s, err := ParseSubmissions(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Recent) != 2 || s.Recent[0].Items != "2.02,9.01" || s.Recent[1].Items != "" {
		t.Fatalf("items: %+v", s.Recent)
	}

	noItems := []byte(`{"filings":{"recent":{
		"accessionNumber":["0000000001-26-000001"],"form":["8-K"],"filingDate":["2026-08-03"],
		"reportDate":[""],"primaryDocument":["a.htm"]}}}`)
	s, err = ParseSubmissions(noItems)
	if err != nil || s.Recent[0].Items != "" {
		t.Fatalf("a record without items: %+v %v", s.Recent, err)
	}
}
