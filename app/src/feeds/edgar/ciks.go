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
	CIK    int64  `json:"cik_str"`
	Ticker string `json:"ticker"`
	Title  string `json:"title"`
}

// ParseTickers reads the SEC's company ticker list, which is a JSON object whose
// keys are "0", "1", and so on, each holding one record with cik_str, ticker and
// title. The shape was written from memory and has not been verified against
// the live file. The SEC states that the accuracy and scope of this list are
// not guaranteed, so a match made with it should be cross-checked.
//
// Each result has its ticker upper-cased and trimmed, its name from title, and
// its CIK as ten digits with leading zeros. Records with an empty ticker or a
// CIK of zero or less are skipped. Where a ticker appears more than once, the
// first record in numeric key order is kept. The result is sorted by ticker.
// Invalid JSON, and keys that are not numbers, are errors.
func ParseTickers(r io.Reader) ([]model.Entity, error) {
	var raw map[string]tickerRecord
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("edgar: read ticker list: %w", err)
	}
	type keyed struct {
		n   int
		rec tickerRecord
	}
	ordered := make([]keyed, 0, len(raw))
	for key, rec := range raw {
		n, err := strconv.Atoi(key)
		if err != nil {
			return nil, fmt.Errorf("edgar: ticker list key %q is not a number", key)
		}
		ordered = append(ordered, keyed{n, rec})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].n < ordered[j].n })

	seen := make(map[string]bool, len(ordered))
	var out []model.Entity
	for _, k := range ordered {
		ticker := strings.ToUpper(strings.TrimSpace(k.rec.Ticker))
		if ticker == "" || k.rec.CIK <= 0 || seen[ticker] {
			continue
		}
		seen[ticker] = true
		out = append(out, model.Entity{
			Ticker: ticker,
			Name:   k.rec.Title,
			CIK:    fmt.Sprintf("%010d", k.rec.CIK),
		})
	}
	slices.SortFunc(out, func(a, b model.Entity) int { return strings.Compare(a.Ticker, b.Ticker) })
	return out, nil
}
