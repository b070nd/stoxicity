-- Fundamentals as they were known, not only as they are.
--
-- fundamental holds the newest reading of each company's figures for the lists.
-- fundamental_fact keeps every fact that was ever read, with the filing it
-- comes from; a restated figure is a new row beside the old one. Nothing here
-- is updated or deleted. A fact was public from the day its filing was filed:
-- filed_day when the source says it (companyfacts), else the day of the same
-- accession in filing_index (the EDGAR indexes), else it is not known when.
CREATE TABLE fundamental_fact (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    cik          TEXT NOT NULL,
    metric       TEXT NOT NULL,
    taxonomy     TEXT NOT NULL,
    concept      TEXT NOT NULL,
    unit         TEXT NOT NULL,
    period_start TEXT NOT NULL,
    period_end   TEXT NOT NULL,
    dimensions   TEXT NOT NULL DEFAULT '',
    accession    TEXT NOT NULL,
    form         TEXT NOT NULL DEFAULT '',
    filed_day    TEXT NOT NULL DEFAULT '',
    value        REAL NOT NULL,
    source       TEXT NOT NULL,
    received_ms  INTEGER NOT NULL
);
CREATE UNIQUE INDEX fundamental_fact_key ON fundamental_fact (cik, concept, unit, period_start, period_end, dimensions, accession, value);
CREATE INDEX fundamental_fact_company ON fundamental_fact (cik, metric, period_end);

-- filing_index: the day each filing was filed, from the EDGAR indexes.
CREATE TABLE filing_index (
    accession     TEXT PRIMARY KEY,
    cik           TEXT NOT NULL,
    form          TEXT NOT NULL,
    filed_day     TEXT NOT NULL,
    company       TEXT NOT NULL DEFAULT '',
    source        TEXT NOT NULL,
    first_seen_ms INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX filing_index_by_day ON filing_index (filed_day, form);
CREATE INDEX filing_index_by_cik ON filing_index (cik, filed_day);

-- filing_index_day: which days of the index were read, and how many filings
-- the day listed, so a day is read once and a gap is visible.
CREATE TABLE filing_index_day (
    day      TEXT PRIMARY KEY,
    read_ms  INTEGER NOT NULL,
    listed   INTEGER NOT NULL,
    missing  INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

-- share_class: shares outstanding on a filing's cover page, per class. An
-- empty dimensions is the single total of an issuer with one class; otherwise
-- each row is one class (dimensions names the axis and member) with its own
-- trading symbol when the cover lists one.
CREATE TABLE share_class (
    cik         TEXT NOT NULL,
    accession   TEXT NOT NULL,
    as_of       TEXT NOT NULL,
    dimensions  TEXT NOT NULL,
    shares      REAL NOT NULL,
    ticker      TEXT NOT NULL DEFAULT '',
    form        TEXT NOT NULL DEFAULT '',
    filed_day   TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL,
    received_ms INTEGER NOT NULL,
    PRIMARY KEY (cik, accession, as_of, dimensions)
) WITHOUT ROWID;
