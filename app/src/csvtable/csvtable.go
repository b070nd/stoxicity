// Package csvtable reads the small strict CSV files the importers take: a header
// naming known columns, no unknown or repeated column, no short row, at least one
// row. Anything else is an error with its line number, so a misspelt column is
// never silently ignored.
package csvtable

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Read returns the rows as maps from column name to trimmed value, plus "_line",
// the line number in the file.
func Read(r io.Reader, required, optional []string) ([]map[string]string, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	head, err := cr.Read()
	if err == io.EOF {
		return nil, errors.New("csvtable: the file is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("csvtable: %w", err)
	}
	allowed := map[string]bool{}
	for _, c := range append(append([]string{}, required...), optional...) {
		allowed[c] = true
	}
	have := map[string]int{}
	for i, h := range head {
		h = strings.TrimSpace(h)
		if !allowed[h] {
			return nil, fmt.Errorf("csvtable: unknown column %q (columns: %s)", h, strings.Join(append(append([]string{}, required...), optional...), ", "))
		}
		if _, dup := have[h]; dup {
			return nil, fmt.Errorf("csvtable: column %q appears twice", h)
		}
		have[h] = i
	}
	for _, c := range required {
		if _, ok := have[c]; !ok {
			return nil, fmt.Errorf("csvtable: the file has no %q column", c)
		}
	}
	var rows []map[string]string
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csvtable: line %d: %w", line, err)
		}
		if len(rec) != len(head) {
			return nil, fmt.Errorf("csvtable: line %d has %d fields, the header has %d", line, len(rec), len(head))
		}
		row := map[string]string{"_line": fmt.Sprint(line)}
		for name, i := range have {
			row[name] = strings.TrimSpace(rec[i])
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errors.New("csvtable: the file has a header and no rows")
	}
	return rows, nil
}
