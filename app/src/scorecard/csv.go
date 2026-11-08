package scorecard

import (
	"fmt"
	"io"
	"strings"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/money"
)

// CSVColumns are the columns of an import file.
var CSVColumns = struct{ Required, Optional []string }{
	Required: []string{"security", "metric", "end", "value", "source", "ref"},
	Optional: []string{"start", "currency", "filed", "note"},
}

// ParseCSV reads figures from a file. Every problem in the file is reported
// with its line number before anything is accepted.
func ParseCSV(r io.Reader) ([]Datum, error) {
	rows, err := csvtable.Read(r, CSVColumns.Required, CSVColumns.Optional)
	if err != nil {
		return nil, err
	}
	var out []Datum
	var problems []string
	for _, row := range rows {
		v, err := money.ParseDecimal(row["value"])
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %s: value %q: %v", row["_line"], row["value"], err))
			continue
		}
		d := Datum{Security: row["security"], Metric: row["metric"], Start: row["start"], End: row["end"], Currency: money.Currency(row["currency"]),
			Value: v, Source: row["source"], Ref: row["ref"], Filed: row["filed"], Note: row["note"]}
		if err := d.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("line %s: %v", row["_line"], err))
			continue
		}
		out = append(out, d)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("scorecard: %d problem(s), nothing was imported:\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	return out, nil
}

// ExampleCSV is a small illustrative file. The numbers are invented.
const ExampleCSV = `security,metric,start,end,currency,value,source,ref,filed,note
sec_example,revenue,2025-01-01,2025-12-31,EUR,1210000000,company-report,Annual report 2025 p.41,2026-03-01,invented illustration
sec_example,net_income,2025-01-01,2025-12-31,EUR,133000000,company-report,Annual report 2025 p.41,2026-03-01,invented illustration
sec_example,equity,,2025-12-31,EUR,600000000,company-report,Annual report 2025 p.44,2026-03-01,invented illustration
`
