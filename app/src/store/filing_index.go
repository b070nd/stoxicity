package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FilingRef is a filing as an EDGAR index lists it.
type FilingRef struct {
	Accession, CIK, Form, Filed, Company string
}

// DayStatus says what is known of one day of the EDGAR index.
type DayStatus string

const (
	// DayComplete: read after the day was over; its filings are stored.
	DayComplete DayStatus = "complete"
	// DayPartial: read before the day was over; read again.
	DayPartial DayStatus = "partial"
	// DayUnpublished: the SEC had no index for a day that should have one. It
	// is retried on every run; absence now says nothing about later.
	DayUnpublished DayStatus = "unpublished"
	// DayNonFiling: a day on which nothing is filed, with the basis stated: the
	// calendar (a weekend, a federal holiday) or a completed quarterly index
	// that lists no filing for it.
	DayNonFiling DayStatus = "nonfiling"
	// DayError: a request failed; retried.
	DayError DayStatus = "error"
)

// Resolved reports whether the day needs no further reading.
func (s DayStatus) Resolved() bool { return s == DayComplete || s == DayNonFiling }

// IndexDay says what is stored of one day of the index.
type IndexDay struct {
	Day       string
	Status    DayStatus
	Basis     string // for a non-filing day: why; for others: where it was read ("daily", "quarterly")
	Source    string
	Read      time.Time
	Listed    int // filings the index listed that day, all forms
	Attempts  int
	LastError string
	// Forms is the selection of forms the day was read for (sorted, comma
	// separated); empty when it was not recorded.
	Forms string
}

// DayResult is what one reading of a day found.
type DayResult struct {
	Day    string
	Status DayStatus
	Basis  string
	Source string // daily | quarterly | calendar
	Err    string
	Refs   []FilingRef
	Listed int
	// Forms is the selection of forms the wanted filings were taken for.
	Forms string
}

// PutFilingDay stores the wanted filings of one day and the outcome of reading
// it. Filings are deduplicated by accession (a day read again, or read from the
// daily and the quarterly index, adds the ones not stored and changes none). A
// day that was resolved stays so unless the result is also resolved: a failed
// retry never undoes a read.
func (db *DB) PutFilingDay(ctx context.Context, r DayResult, at time.Time) (added int, err error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	if r.Source == "" {
		r.Source = "daily"
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: filing index: %w", err)
	}
	defer tx.Rollback()
	for _, f := range r.Refs {
		res, err := tx.ExecContext(ctx, `INSERT INTO filing_index (accession, cik, form, filed_day, company, source, first_seen_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, f.Accession, f.CIK, f.Form, f.Filed, f.Company, "edgar-"+r.Source+"-index", ms(at))
		if err != nil {
			return 0, fmt.Errorf("store: filing index: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	var cur string
	err = tx.QueryRowContext(ctx, `SELECT status FROM filing_index_day WHERE day = ?`, r.Day).Scan(&cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO filing_index_day (day, read_ms, listed, missing, status, basis, source, attempts, last_attempt_ms, last_error, forms)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`, r.Day, ms(at), r.Listed, r.Status == DayUnpublished, string(r.Status), r.Basis, r.Source, ms(at), r.Err, r.Forms)
	case err != nil:
		return 0, fmt.Errorf("store: filing index: %w", err)
	case DayStatus(cur).Resolved() && !r.Status.Resolved():
		// keep the earlier read; note only that a later attempt failed
		_, err = tx.ExecContext(ctx, `UPDATE filing_index_day SET attempts = attempts + 1, last_attempt_ms = ?, last_error = ? WHERE day = ?`, ms(at), r.Err, r.Day)
	default:
		_, err = tx.ExecContext(ctx, `UPDATE filing_index_day SET read_ms = ?, listed = ?, missing = ?, status = ?, basis = ?, source = ?,
			attempts = attempts + 1, last_attempt_ms = ?, last_error = ?, forms = ? WHERE day = ?`,
			ms(at), r.Listed, r.Status == DayUnpublished, string(r.Status), r.Basis, r.Source, ms(at), r.Err, r.Forms, r.Day)
	}
	if err != nil {
		return 0, fmt.Errorf("store: filing index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: filing index: %w", err)
	}
	return added, nil
}

// IndexDays returns the stored days of the index from the day on (and, when to
// is not empty, up to it).
func (db *DB) IndexDays(ctx context.Context, from string, to ...string) (map[string]IndexDay, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	upper := "9999-12-31"
	if len(to) > 0 && to[0] != "" {
		upper = to[0]
	}
	rows, err := h.r.QueryContext(ctx, `SELECT day, read_ms, listed, status, basis, source, attempts, last_error, forms FROM filing_index_day WHERE day >= ? AND day <= ?`, from, upper)
	if err != nil {
		return nil, fmt.Errorf("store: filing index: %w", err)
	}
	defer rows.Close()
	out := map[string]IndexDay{}
	for rows.Next() {
		var d IndexDay
		var read int64
		var status string
		if err := rows.Scan(&d.Day, &read, &d.Listed, &status, &d.Basis, &d.Source, &d.Attempts, &d.LastError, &d.Forms); err != nil {
			return nil, fmt.Errorf("store: filing index: %w", err)
		}
		d.Status, d.Read = DayStatus(status), fromMS(read)
		out[d.Day] = d
	}
	return out, rows.Err()
}

// LastResolvedDay returns the newest day that is complete or known not to have
// filings, and false when there is none.
func (db *DB) LastResolvedDay(ctx context.Context) (string, bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return "", false, err
	}
	var day sql.NullString
	if err := h.r.QueryRowContext(ctx, `SELECT MAX(day) FROM filing_index_day WHERE status IN ('complete', 'nonfiling')`).Scan(&day); err != nil {
		return "", false, fmt.Errorf("store: filing index: %w", err)
	}
	return day.String, day.Valid, nil
}

// UnresolvedDays returns the stored days in [from, to] that still need reading.
func (db *DB) UnresolvedDays(ctx context.Context, from, to string) ([]IndexDay, error) {
	all, err := db.IndexDays(ctx, from, to)
	if err != nil {
		return nil, err
	}
	var out []IndexDay
	for _, d := range all {
		if !d.Status.Resolved() {
			out = append(out, d)
		}
	}
	return out, nil
}

// QuarterRow says what is stored of one quarterly index.
type QuarterRow struct {
	Year, Quarter            int
	Status                   string // complete | partial | error
	Listed, Wanted, Attempts int
	Read                     time.Time
	LastError                string
}

// PutFilingQuarter records the reading of a quarterly index.
func (db *DB) PutFilingQuarter(ctx context.Context, q QuarterRow, at time.Time) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	_, err = h.w.ExecContext(ctx, `INSERT INTO filing_index_quarter (year, quarter, status, listed, wanted, read_ms, attempts, last_error) VALUES (?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT (year, quarter) DO UPDATE SET status = excluded.status, listed = excluded.listed, wanted = excluded.wanted, read_ms = excluded.read_ms,
			attempts = attempts + 1, last_error = excluded.last_error`, q.Year, q.Quarter, q.Status, q.Listed, q.Wanted, ms(at), q.LastError)
	if err != nil {
		return fmt.Errorf("store: filing index: %w", err)
	}
	return nil
}

// FilingQuarter returns what is stored of a quarterly index.
func (db *DB) FilingQuarter(ctx context.Context, year, quarter int) (QuarterRow, bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return QuarterRow{}, false, err
	}
	q := QuarterRow{Year: year, Quarter: quarter}
	var read int64
	err = h.r.QueryRowContext(ctx, `SELECT status, listed, wanted, read_ms, attempts, last_error FROM filing_index_quarter WHERE year = ? AND quarter = ?`, year, quarter).
		Scan(&q.Status, &q.Listed, &q.Wanted, &read, &q.Attempts, &q.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return QuarterRow{}, false, nil
	}
	if err != nil {
		return QuarterRow{}, false, fmt.Errorf("store: filing index: %w", err)
	}
	q.Read = fromMS(read)
	return q, true, nil
}

// ReconcileRun is one run of the reconciliation, kept so an interrupted run is
// visible.
type ReconcileRun struct {
	ID                  int64
	From, To, Mode      string
	Started, Finished   time.Time
	DaysDone, DaysTotal int
	Note                string
}

// StartReconcileRun records the start of a run over [from, to].
func (db *DB) StartReconcileRun(ctx context.Context, from, to, mode string, total int, at time.Time) (int64, error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	var id int64
	err = h.w.QueryRowContext(ctx, `INSERT INTO reconcile_run (from_day, to_day, mode, started_ms, days_total) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		from, to, mode, ms(at), total).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: reconcile run: %w", err)
	}
	return id, nil
}

// UpdateReconcileRun records progress; a non-zero finished time ends the run.
func (db *DB) UpdateReconcileRun(ctx context.Context, id int64, done int, note string, finished time.Time) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	var fin int64
	if !finished.IsZero() {
		fin = ms(finished)
	}
	_, err = h.w.ExecContext(ctx, `UPDATE reconcile_run SET days_done = ?, note = ?, finished_ms = ? WHERE id = ?`, done, note, fin, id)
	if err != nil {
		return fmt.Errorf("store: reconcile run: %w", err)
	}
	return nil
}

// LastReconcileRun returns the newest run.
func (db *DB) LastReconcileRun(ctx context.Context) (ReconcileRun, bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return ReconcileRun{}, false, err
	}
	var r ReconcileRun
	var started, finished int64
	err = h.r.QueryRowContext(ctx, `SELECT id, from_day, to_day, mode, started_ms, finished_ms, days_done, days_total, note FROM reconcile_run ORDER BY id DESC LIMIT 1`).
		Scan(&r.ID, &r.From, &r.To, &r.Mode, &started, &finished, &r.DaysDone, &r.DaysTotal, &r.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return ReconcileRun{}, false, nil
	}
	if err != nil {
		return ReconcileRun{}, false, fmt.Errorf("store: reconcile run: %w", err)
	}
	r.Started = fromMS(started)
	if finished > 0 {
		r.Finished = fromMS(finished)
	}
	return r, true, nil
}

// FilingMeta is what the SEC's record of a company says about one filing.
type FilingMeta struct {
	Accession, CIK, Form, FiledDay string
	// Accepted is the acceptance time; zero when it is not known. Precision is
	// "time" when it is and "day" when only the filing day is.
	Accepted  time.Time
	Precision string
	ReportDay string
	Document  string
	Source    string
}

// Precisions of a filing's timing.
const (
	PrecisionTime = "time"
	PrecisionDay  = "day"
)

// PutFilingMeta stores what is known of filings. An accession already stored
// keeps its first reading unless the new one adds the acceptance time the first
// did not have; nothing is ever made more precise than the source said.
func (db *DB) PutFilingMeta(ctx context.Context, metas []FilingMeta, at time.Time) (int, error) {
	if len(metas) == 0 {
		return 0, nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: filing meta: %w", err)
	}
	defer tx.Rollback()
	wrote := 0
	for _, m := range metas {
		if m.Accession == "" || m.FiledDay == "" {
			continue
		}
		var accepted int64
		if !m.Accepted.IsZero() {
			accepted = ms(m.Accepted)
			m.Precision = PrecisionTime
		} else {
			m.Precision = PrecisionDay
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO filing_meta (accession, cik, form, filed_day, accepted_ms, precision, report_day, document, source, read_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (accession) DO UPDATE SET accepted_ms = excluded.accepted_ms, precision = excluded.precision, read_ms = excluded.read_ms
			WHERE filing_meta.precision = 'day' AND excluded.precision = 'time'`,
			m.Accession, m.CIK, m.Form, m.FiledDay, accepted, m.Precision, m.ReportDay, m.Document, m.Source, ms(at))
		if err != nil {
			return 0, fmt.Errorf("store: filing meta: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			wrote++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: filing meta: %w", err)
	}
	return wrote, nil
}

// FilingMetas returns the stored metadata of the accessions.
func (db *DB) FilingMetas(ctx context.Context, accessions []string) (map[string]FilingMeta, error) {
	out := map[string]FilingMeta{}
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(accessions); start += 400 {
		chunk := accessions[start:min(start+400, len(accessions))]
		args := make([]any, len(chunk))
		for i, a := range chunk {
			args[i] = a
		}
		rows, err := h.r.QueryContext(ctx, `SELECT accession, cik, form, filed_day, accepted_ms, precision, report_day, document, source FROM filing_meta
			WHERE accession IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("store: filing meta: %w", err)
		}
		for rows.Next() {
			var m FilingMeta
			var accepted int64
			if err := rows.Scan(&m.Accession, &m.CIK, &m.Form, &m.FiledDay, &accepted, &m.Precision, &m.ReportDay, &m.Document, &m.Source); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: filing meta: %w", err)
			}
			if accepted > 0 {
				m.Accepted = fromMS(accepted)
			}
			out[m.Accession] = m
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: filing meta: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

// Backfill says an article was stored by a repair or a historical range.
type Backfill struct {
	ArticleID string
	Kind      string // late | historical
	RunID     int64
	FiledDay  string
	Precision string
	Recovered time.Time
	Reason    string
}

// Kinds of backfill.
const (
	BackfillLate       = "late"       // recovered within days of the filing
	BackfillHistorical = "historical" // recovered from a historical range
)

// PutBackfill marks articles as backfilled. It is called before the articles
// are stored, so an alert pass never sees one unmarked; an article already
// marked keeps its first mark.
func (db *DB) PutBackfill(ctx context.Context, bs []Backfill, at time.Time) error {
	if len(bs) == 0 {
		return nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return fmt.Errorf("store: backfill: %w", err)
	}
	defer tx.Rollback()
	for _, b := range bs {
		if b.ArticleID == "" {
			return errors.New("store: backfill: empty article ID")
		}
		if b.Precision == "" {
			b.Precision = PrecisionDay
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO article_backfill (article_id, kind, run_id, filed_day, precision, recovered_ms, reason) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			b.ArticleID, b.Kind, b.RunID, b.FiledDay, b.Precision, ms(at), b.Reason); err != nil {
			return fmt.Errorf("store: backfill: %w", err)
		}
	}
	return tx.Commit()
}

// Backfilled returns the marks of the articles that have one.
func (db *DB) Backfilled(ctx context.Context, ids []string) (map[string]Backfill, error) {
	out := map[string]Backfill{}
	if len(ids) == 0 {
		return out, nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(ids); start += 400 {
		chunk := ids[start:min(start+400, len(ids))]
		args := make([]any, len(chunk))
		for i, a := range chunk {
			args[i] = a
		}
		rows, err := h.r.QueryContext(ctx, `SELECT article_id, kind, run_id, filed_day, precision, recovered_ms, reason FROM article_backfill
			WHERE article_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("store: backfill: %w", err)
		}
		for rows.Next() {
			var b Backfill
			var rec int64
			if err := rows.Scan(&b.ArticleID, &b.Kind, &b.RunID, &b.FiledDay, &b.Precision, &rec, &b.Reason); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: backfill: %w", err)
			}
			b.Recovered = fromMS(rec)
			out[b.ArticleID] = b
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: backfill: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

// MissingFilings returns the filings the index lists for the days that no stored
// article has, oldest first.
func (db *DB) MissingFilings(ctx context.Context, from, to string) ([]FilingRef, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT i.accession, i.cik, i.form, i.filed_day, i.company FROM filing_index i
		WHERE i.filed_day >= ? AND i.filed_day <= ? AND NOT EXISTS (SELECT 1 FROM articles a WHERE a.id = i.accession)
		ORDER BY i.filed_day, i.accession`, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: filing index: %w", err)
	}
	defer rows.Close()
	var out []FilingRef
	for rows.Next() {
		var r FilingRef
		if err := rows.Scan(&r.Accession, &r.CIK, &r.Form, &r.Filed, &r.Company); err != nil {
			return nil, fmt.Errorf("store: filing index: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FilingsOnDays counts, for each day, the filings the index lists and how many
// of them have an article.
func (db *DB) FilingsOnDays(ctx context.Context, from, to string) (map[string][2]int, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT i.filed_day, COUNT(*), SUM(CASE WHEN EXISTS (SELECT 1 FROM articles a WHERE a.id = i.accession) THEN 1 ELSE 0 END)
		FROM filing_index i WHERE i.filed_day >= ? AND i.filed_day <= ? GROUP BY i.filed_day`, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: filing index: %w", err)
	}
	defer rows.Close()
	out := map[string][2]int{}
	for rows.Next() {
		var d string
		var n, have int
		if err := rows.Scan(&d, &n, &have); err != nil {
			return nil, fmt.Errorf("store: filing index: %w", err)
		}
		out[d] = [2]int{n, have}
	}
	return out, rows.Err()
}

// FactsWithoutFilingDay returns, by company, the accessions of stored
// fundamental facts whose filing day is not known from the fact itself, from the
// EDGAR index or from a stored filing record: the filings whose metadata is
// worth asking the SEC for. Only these are asked for, not the company's other
// filings.
func (db *DB) FactsWithoutFilingDay(ctx context.Context) (map[string][]string, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT DISTINCT f.cik, f.accession FROM fundamental_fact f
		WHERE f.filed_day = '' AND NOT EXISTS (SELECT 1 FROM filing_index i WHERE i.accession = f.accession)
		  AND NOT EXISTS (SELECT 1 FROM filing_meta m WHERE m.accession = f.accession) ORDER BY f.cik, f.accession`)
	if err != nil {
		return nil, fmt.Errorf("store: filing index: %w", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var cik, acc string
		if err := rows.Scan(&cik, &acc); err != nil {
			return nil, fmt.Errorf("store: filing index: %w", err)
		}
		out[cik] = append(out[cik], acc)
	}
	return out, rows.Err()
}
