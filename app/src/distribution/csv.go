package distribution

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
)

// ParseCSV reads payouts. Columns: type, period, status, amount, currency and
// ex_day are required; isin, mic and ticker name the security (an ISIN, or a
// venue and ticker, or both, which must agree); record_day, pay_day and
// declared_day are optional.
//
// The security is the share class, resolved through the registry on the ex-day.
// A row that names a class the instrument record says accumulates is refused:
// either the file or the record is wrong, and silence would hide it. The payout
// currency is always stated by the row; it is not taken from the listing, since
// a fund can pay in a currency other than the one it trades in.
func ParseCSV(r io.Reader, reg *instrument.Registry, source, fileRef string, received time.Time) ([]Obs, error) {
	rows, err := csvtable.Read(r,
		[]string{"type", "period", "status", "amount", "currency", "ex_day"},
		[]string{"isin", "mic", "ticker", "record_day", "pay_day", "declared_day"})
	if err != nil {
		return nil, err
	}
	var out []Obs
	var bad []string
	for _, row := range rows {
		fail := func(f string, a ...any) {
			bad = append(bad, fmt.Sprintf("line %s: %s", row["_line"], fmt.Sprintf(f, a...)))
		}
		if row["isin"] == "" && row["ticker"] == "" {
			fail("name the security with an isin, or a mic and ticker")
			continue
		}
		res := reg.ResolveSecurity(instrument.Query{ISIN: row["isin"], MIC: row["mic"], Ticker: row["ticker"], Day: row["ex_day"]})
		if res.Status != instrument.Resolved {
			fail("the security is %s: %s", res.Status, strings.Join(res.Reasons, "; "))
			continue
		}
		sec := res.Security
		if sec.Distribution == instrument.Accumulating {
			fail("%s (%s) is an accumulating class and pays no cash distribution; the file or the instrument record is wrong", sec.Name, sec.ID)
			continue
		}
		amt, err := money.ParseDecimal(row["amount"])
		if err != nil {
			fail("%v", err)
			continue
		}
		cur, err := money.ParseCurrency(row["currency"])
		if err != nil {
			fail("%v", err)
			continue
		}
		o := Obs{SecurityID: sec.ID, Type: Type(row["type"]), Period: row["period"], Status: Status(row["status"]), Amount: amt, Currency: cur,
			ExDay: row["ex_day"], RecordDay: row["record_day"], PayDay: row["pay_day"], DeclaredDay: row["declared_day"],
			Source: source, SourceRef: fileRef, Received: received}
		if err := o.Validate(); err != nil {
			fail("%v", err)
			continue
		}
		out = append(out, o)
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("distribution: not imported:\n  %s", strings.Join(bad, "\n  "))
	}
	return out, nil
}

// ParseSplitsCSV reads share splits: security (an ID), day, from, to.
func ParseSplitsCSV(r io.Reader) ([]Split, error) {
	rows, err := csvtable.Read(r, []string{"security", "day", "from", "to"}, nil)
	if err != nil {
		return nil, err
	}
	var out []Split
	for _, row := range rows {
		var s Split
		s.SecurityID, s.Day = row["security"], row["day"]
		if _, err := fmt.Sscanf(row["from"]+" "+row["to"], "%d %d", &s.From, &s.To); err != nil {
			return nil, fmt.Errorf("distribution: line %s: from and to must be whole numbers", row["_line"])
		}
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("distribution: line %s: %w", row["_line"], err)
		}
		out = append(out, s)
	}
	return out, nil
}
