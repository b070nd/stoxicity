-- Class-level share observations and their mapping to securities.
--
-- share_class_obs: one share count of one class as a filing's cover page states
-- it. Append-only: a count read again is the same row, a different count for the
-- same class and date (a conflict, or an amendment) is another row, and which
-- one is used is decided on reading, never by deleting one. A row says when the
-- filing was public (filed_day, and accepted_ms with precision "time" when the
-- acceptance time is known; "day" when only the day is) and when this system
-- received it.
--   dimensions   the class as the filing names it; empty for the one total of
--                an issuer with a single class
--   duplicates   how many identical facts of the cover page this row stands for
--                (a count stated twice in two contexts of the same class)
CREATE TABLE share_class_obs (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    cik         TEXT NOT NULL,
    accession   TEXT NOT NULL,
    form        TEXT NOT NULL DEFAULT '',
    filed_day   TEXT NOT NULL DEFAULT '',
    accepted_ms INTEGER NOT NULL DEFAULT 0,
    precision   TEXT NOT NULL DEFAULT 'day',
    as_of       TEXT NOT NULL,
    dimensions  TEXT NOT NULL,
    shares      REAL NOT NULL,
    ticker      TEXT NOT NULL DEFAULT '',
    duplicates  INTEGER NOT NULL DEFAULT 1,
    source      TEXT NOT NULL,
    received_ms INTEGER NOT NULL
);
CREATE UNIQUE INDEX share_class_obs_key ON share_class_obs (cik, accession, as_of, dimensions, shares);
CREATE INDEX share_class_obs_cik ON share_class_obs (cik, as_of);

-- share_class_map: which security a class of an issuer is. Appended; the latest
-- row of a (cik, dimensions) is current, earlier rows are the history of the
-- mapping. status: mapped | ambiguous | unmapped. method and basis state how
-- it was decided, so a mapping can be audited and a later rule can be compared
-- with it.
CREATE TABLE share_class_map (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    cik         TEXT NOT NULL,
    dimensions  TEXT NOT NULL,
    security_id TEXT NOT NULL DEFAULT '',
    ticker      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL,
    method      TEXT NOT NULL,
    confidence  REAL NOT NULL,
    basis       TEXT NOT NULL,
    accession   TEXT NOT NULL,
    mapped_ms   INTEGER NOT NULL
);
CREATE INDEX share_class_map_class ON share_class_map (cik, dimensions, seq DESC);

-- The counts read before this table existed keep their provenance.
INSERT OR IGNORE INTO share_class_obs (cik, accession, form, filed_day, precision, as_of, dimensions, shares, ticker, source, received_ms)
SELECT cik, accession, form, filed_day, 'day', as_of, dimensions, shares, ticker, source, received_ms FROM share_class;

-- cover_check: where the reading of each issuer's cover page stands, so that an
-- issuer is not read again every cycle. problem is empty when the last reading
-- worked (accession names the report it read, or is empty when the issuer has
-- no machine-readable cover page).
CREATE TABLE cover_check (
    cik         TEXT PRIMARY KEY,
    checked_ms  INTEGER NOT NULL,
    accession   TEXT NOT NULL DEFAULT '',
    problem     TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
