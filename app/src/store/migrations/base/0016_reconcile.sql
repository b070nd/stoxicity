-- Resumable, historically configurable reconciliation with the EDGAR indexes.
--
-- filing_index_day gets a status per day, so that "the index was not there" is
-- no longer one fact with one meaning:
--   complete     the index was read after the day was over and its filings stored
--   partial      read before the day was over: read again
--   unpublished  the SEC had no index for a day that should have one: retried
--   nonfiling    a day on which the SEC publishes nothing (weekend, federal
--                holiday), or one a completed quarterly index shows empty
--   error        a request failed: retried
-- Rows made before this migration are classified from what they hold.
ALTER TABLE filing_index_day ADD COLUMN status TEXT NOT NULL DEFAULT '';
ALTER TABLE filing_index_day ADD COLUMN basis TEXT NOT NULL DEFAULT '';
ALTER TABLE filing_index_day ADD COLUMN source TEXT NOT NULL DEFAULT 'daily';
ALTER TABLE filing_index_day ADD COLUMN attempts INTEGER NOT NULL DEFAULT 1;
ALTER TABLE filing_index_day ADD COLUMN last_attempt_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE filing_index_day ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
UPDATE filing_index_day SET last_attempt_ms = read_ms,
    status = CASE
        WHEN missing = 1 THEN 'unpublished'
        WHEN read_ms >= (CAST(strftime('%s', day, '+1 day') AS INTEGER) * 1000) THEN 'complete'
        ELSE 'partial' END;

-- filing_index_quarter: the quarterly indexes read, for historical ranges.
CREATE TABLE filing_index_quarter (
    year             INTEGER NOT NULL,
    quarter          INTEGER NOT NULL,
    status           TEXT NOT NULL,   -- complete | partial | error
    listed           INTEGER NOT NULL DEFAULT 0,
    wanted           INTEGER NOT NULL DEFAULT 0,
    read_ms          INTEGER NOT NULL,
    attempts         INTEGER NOT NULL DEFAULT 1,
    last_error       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (year, quarter)
) WITHOUT ROWID;

-- reconcile_run: one run of the reconciliation: its range and how far it got,
-- so an interrupted run is visible and the next run continues where it stopped.
CREATE TABLE reconcile_run (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    from_day    TEXT NOT NULL,
    to_day      TEXT NOT NULL,
    mode        TEXT NOT NULL,      -- rolling | range
    started_ms  INTEGER NOT NULL,
    finished_ms INTEGER NOT NULL DEFAULT 0,
    days_done   INTEGER NOT NULL DEFAULT 0,
    days_total  INTEGER NOT NULL DEFAULT 0,
    note        TEXT NOT NULL DEFAULT ''
);

-- filing_meta: what the SEC's record of a company says about one filing, read
-- by accession. accepted_ms is the acceptance time when known (0 when it is
-- not); precision says whether the time of day is known ("time") or only the
-- filing day ("day"). A time is never made up from a day.
CREATE TABLE filing_meta (
    accession    TEXT PRIMARY KEY,
    cik          TEXT NOT NULL,
    form         TEXT NOT NULL,
    filed_day    TEXT NOT NULL,
    accepted_ms  INTEGER NOT NULL DEFAULT 0,
    precision    TEXT NOT NULL,
    report_day   TEXT NOT NULL DEFAULT '',
    document     TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL,
    read_ms      INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX filing_meta_by_cik ON filing_meta (cik, filed_day);

-- article_backfill: articles stored by a repair or a historical range rather
-- than by the live feed. They are evidence, but they were not news when this
-- system stored them, so an alert must not treat them as newly published.
CREATE TABLE article_backfill (
    article_id   TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,       -- late (found within days) | historical
    run_id       INTEGER NOT NULL DEFAULT 0,
    filed_day    TEXT NOT NULL DEFAULT '',
    precision    TEXT NOT NULL DEFAULT 'day',   -- day: only the filing day is known; time: the acceptance time is
    recovered_ms INTEGER NOT NULL,
    reason       TEXT NOT NULL
) WITHOUT ROWID;
