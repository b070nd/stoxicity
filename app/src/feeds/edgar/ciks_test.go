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
