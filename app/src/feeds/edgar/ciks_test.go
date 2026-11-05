package edgar

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

// TestParseTickersFixture reads testdata/company_tickers.json. The fixture is
// synthetic, not verified against live data: it was written by hand from the
// documented format as the author knows it, and its values are invented. JSON
// has no comments, so the note is here. It holds a blank ticker, a zero CIK, and
// a lower-case duplicate under key 10, which numeric key order must place after
// key 2.
func TestParseTickersFixture(t *testing.T) {
	f, err := os.Open("testdata/company_tickers.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := ParseTickers(f)
	if err != nil {
		t.Fatalf("ParseTickers: %v", err)
	}
	want := []model.Entity{
		{Ticker: "AAPL", Name: "Apple Inc.", CIK: "0000320193"},
		{Ticker: "GOOGL", Name: "Alphabet Inc.", CIK: "0001652044"},
		{Ticker: "MSFT", Name: "MICROSOFT CORP", CIK: "0000789019"},
		{Ticker: "NVDA", Name: "NVIDIA CORP", CIK: "0001045810"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTickers =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseTickers(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []model.Entity
		wantErr bool
	}{
		{
			name: "empty object",
			in:   `{}`,
			want: nil,
		},
		{
			name: "ticker upper-cased and trimmed, CIK padded",
			in:   `{"0":{"cik_str":1,"ticker":"  abc ","title":"Abc Co"}}`,
			want: []model.Entity{{Ticker: "ABC", Name: "Abc Co", CIK: "0000000001"}},
		},
		{
			name: "CIK of zero or less skipped",
			in:   `{"0":{"cik_str":0,"ticker":"AAA","title":"A"},"1":{"cik_str":-5,"ticker":"BBB","title":"B"}}`,
			want: nil,
		},
		{
			name: "invalid record does not block a later duplicate",
			in:   `{"0":{"cik_str":0,"ticker":"DUP","title":"Zero"},"1":{"cik_str":7,"ticker":"dup","title":"Seven"}}`,
			want: []model.Entity{{Ticker: "DUP", Name: "Seven", CIK: "0000000007"}},
		},
		{
			name: "duplicate keeps the first in numeric key order",
			in:   `{"10":{"cik_str":2,"ticker":"X","title":"Ten"},"2":{"cik_str":1,"ticker":"X","title":"Two"}}`,
			want: []model.Entity{{Ticker: "X", Name: "Two", CIK: "0000000001"}},
		},
		{
			name: "sorted by ticker",
			in:   `{"0":{"cik_str":3,"ticker":"ZZZ","title":"Z"},"1":{"cik_str":2,"ticker":"AAA","title":"A"}}`,
			want: []model.Entity{
				{Ticker: "AAA", Name: "A", CIK: "0000000002"},
				{Ticker: "ZZZ", Name: "Z", CIK: "0000000003"},
			},
		},
		{name: "invalid JSON", in: `{"0":`, wantErr: true},
		{name: "array instead of object", in: `[]`, wantErr: true},
		{name: "non-numeric key", in: `{"x":{"cik_str":1,"ticker":"A","title":"A"}}`, wantErr: true},
		{name: "empty input", in: ``, wantErr: true},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseTickers(strings.NewReader(c.in))
			if (err != nil) != c.wantErr {
				t.Fatalf("ParseTickers error = %v, wantErr %v", err, c.wantErr)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ParseTickers = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestParseTickersExchangeShape uses rows in the shape the live
// company_tickers_exchange.json has: {"data":[[cik,name,ticker,exchange],...]}.
// The first five rows are copied from the live file; the rest are invented to
// reach the null exchange, a blank ticker, a bad CIK and a duplicate ticker.
func TestParseTickersExchangeShape(t *testing.T) {
	in := `{"data":[
	 [1045810,"NVIDIA CORP","NVDA","Nasdaq"],
	 [320193,"Apple Inc.","AAPL","Nasdaq"],
	 [1067983,"BERKSHIRE HATHAWAY INC","BRK-B","NYSE"],
	 [2070829,"Contemporary Amperex Technology Co., Limited/ADR","CYATY","OTC"],
	 [1276187,"Energy Transfer LP","ET","TXSE"],
	 [9000001,"No Exchange Inc","NOEX",null],
	 [9000002,"Blank Ticker","",null],
	 [0,"Zero Cik","ZERO","NYSE"],
	 [9000003,"Duplicate","NVDA","NYSE"]
	]}`
	got, err := ParseTickers(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Entity{
		{Ticker: "AAPL", Name: "Apple Inc.", CIK: "0000320193", Exchange: "Nasdaq"},
		{Ticker: "BRK-B", Name: "BERKSHIRE HATHAWAY INC", CIK: "0001067983", Exchange: "NYSE"},
		{Ticker: "CYATY", Name: "Contemporary Amperex Technology Co., Limited/ADR", CIK: "0002070829", Exchange: "OTC"},
		{Ticker: "ET", Name: "Energy Transfer LP", CIK: "0001276187", Exchange: "TXSE"},
		{Ticker: "NOEX", Name: "No Exchange Inc", CIK: "0009000001"},
		{Ticker: "NVDA", Name: "NVIDIA CORP", CIK: "0001045810", Exchange: "Nasdaq"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTickers =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseTickersExchangeShapeFieldsDecideTheOrder(t *testing.T) {
	in := `{"fields":["ticker","exchange","cik","name"],"data":[["AAA","NYSE",5,"A Corp"]]}`
	got, err := ParseTickers(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Entity{{Ticker: "AAA", Name: "A Corp", CIK: "0000000005", Exchange: "NYSE"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	for _, bad := range []string{
		`{"fields":["cik","name"],"data":[]}`, // no ticker column
		`{"data":[["x","A","AAA","NYSE"]]}`,   // cik is not a number
		`{"data":"nope"}`,                     // data is not rows
	} {
		if _, err := ParseTickers(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}
