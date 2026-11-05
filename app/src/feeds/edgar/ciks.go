package edgar

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

// tickerRecord is one entry of the SEC's ticker list.
type tickerRecord struct {
	CIK      int64  `json:"cik_str"`
	Ticker   string `json:"ticker"`
	Title    string `json:"title"`
	Exchange string `json:"-"`
}

// ParseTickers reads the SEC's company ticker list. Two shapes are accepted,
// both checked against the live files:
//
//   - company_tickers.json: an object whose keys are "0", "1", and so on, each
//     holding a record with cik_str, ticker and title;
//   - company_tickers_exchange.json: an object whose "data" key holds records
//     written as arrays [cik, name, ticker, exchange]. If a "fields" key names
//     the columns, those names decide the order; otherwise it is the one given.
//     The exchange can be null.
//
// The SEC states that the accuracy and scope of these lists are not guaranteed,
// so a match made with them should be cross-checked.
//
// Each result has its ticker upper-cased and trimmed, its name from the title,
// its exchange as the SEC writes it ("Nasdaq", "NYSE", "OTC", ...; empty in the
// first shape), and its CIK as ten digits with leading zeros. Records with an
// empty ticker or a CIK of zero or less are skipped. Where a ticker appears more
// than once, the first record is kept. The result is sorted by ticker. Invalid
// JSON, and keys that are not numbers in the first shape, are errors.
func ParseTickers(r io.Reader) ([]model.Entity, error) {
	var top map[string]json.RawMessage
	if err := json.NewDecoder(r).Decode(&top); err != nil {
		return nil, fmt.Errorf("edgar: read ticker list: %w", err)
	}
	var recs []tickerRecord
	if data, ok := top["data"]; ok {
		var err error
		if recs, err = exchangeRecords(top["fields"], data); err != nil {
			return nil, err
		}
	} else {
		type keyed struct {
			n   int
			rec tickerRecord
		}
		ordered := make([]keyed, 0, len(top))
		for key, rawRec := range top {
			n, err := strconv.Atoi(key)
			if err != nil {
				return nil, fmt.Errorf("edgar: ticker list key %q is not a number", key)
			}
			var rec tickerRecord
			if err := json.Unmarshal(rawRec, &rec); err != nil {
				return nil, fmt.Errorf("edgar: read ticker list: record %q: %w", key, err)
			}
			ordered = append(ordered, keyed{n, rec})
		}
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].n < ordered[j].n })
		for _, k := range ordered {
			recs = append(recs, k.rec)
		}
	}

	seen := make(map[string]bool, len(recs))
	var out []model.Entity
	for _, rec := range recs {
		ticker := strings.ToUpper(strings.TrimSpace(rec.Ticker))
		if ticker == "" || rec.CIK <= 0 || seen[ticker] {
			continue
		}
		seen[ticker] = true
		out = append(out, model.Entity{
			Ticker:   ticker,
			Name:     rec.Title,
			CIK:      fmt.Sprintf("%010d", rec.CIK),
			Exchange: strings.TrimSpace(rec.Exchange),
		})
	}
	slices.SortFunc(out, func(a, b model.Entity) int { return strings.Compare(a.Ticker, b.Ticker) })
	return out, nil
}

// exchangeRecords reads the rows of company_tickers_exchange.json.
func exchangeRecords(fieldsRaw, dataRaw json.RawMessage) ([]tickerRecord, error) {
	col := map[string]int{"cik": 0, "name": 1, "ticker": 2, "exchange": 3}
	if len(fieldsRaw) > 0 && string(fieldsRaw) != "null" {
		var fields []string
		if err := json.Unmarshal(fieldsRaw, &fields); err != nil {
			return nil, fmt.Errorf("edgar: read ticker list: fields: %w", err)
		}
		col = map[string]int{}
		for i, f := range fields {
			col[strings.ToLower(f)] = i
		}
		for _, need := range []string{"cik", "name", "ticker"} {
			if _, ok := col[need]; !ok {
				return nil, fmt.Errorf("edgar: ticker list has no %q column", need)
			}
		}
	}
	var rows [][]json.RawMessage
	if err := json.Unmarshal(dataRaw, &rows); err != nil {
		return nil, fmt.Errorf("edgar: read ticker list: data: %w", err)
	}
	cell := func(row []json.RawMessage, name string, v any) error {
		i, ok := col[name]
		if !ok || i >= len(row) || string(row[i]) == "null" {
			return nil
		}
		return json.Unmarshal(row[i], v)
	}
	recs := make([]tickerRecord, 0, len(rows))
	for n, row := range rows {
		var rec tickerRecord
		for name, dst := range map[string]any{"cik": &rec.CIK, "name": &rec.Title, "ticker": &rec.Ticker, "exchange": &rec.Exchange} {
			if err := cell(row, name, dst); err != nil {
				return nil, fmt.Errorf("edgar: read ticker list: row %d: %s: %w", n, name, err)
			}
		}
		recs = append(recs, rec)
	}
	return recs, nil
}
